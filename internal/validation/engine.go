// Package validation implements the set-level (database-wide) validation
// rules of PROJECT_BRIEF §7: E005 subnet_overlap, E006 duplicate_address and
// I001 unlinked_device.
//
// The engine is deterministic (ACC-4): for a fixed database state and
// domain.RuleVersion it always computes the same findings, ordered by
// (rule_code, entity natural key), and rewrites them atomically — the old
// set-level issues are purged (Issues.DeleteSetLevel, batch_id NULL + set
// rule codes) and the fresh issues inserted with batch_id NULL, so callers
// can recompute on demand and stay idempotent. Row-phase issues (E001-E004,
// W001-W003, recorded by the importer with a non-NULL batch_id) are never
// touched.
package validation

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/repo"
)

// Engine computes the set-level validation rules over the whole database.
// It holds no mutable state; Run can be called any number of times.
type Engine struct {
	db *repo.DB
}

// New returns an Engine bound to db.
func New(db *repo.DB) *Engine {
	return &Engine{db: db}
}

// Result is the outcome of one Run: how many set-level issues were purged
// before the recompute and how many fresh issues each rule produced.
type Result struct {
	// RuleVersion is the validation rule catalog version the run used.
	RuleVersion string `json:"ruleVersion"`
	// Deleted counts the set-level issues purged before the recompute.
	Deleted int `json:"deleted"`
	// InsertedByRule counts the freshly inserted issues per rule code.
	InsertedByRule map[string]int `json:"insertedByRule"`
	// Inserted is the total number of freshly inserted issues.
	Inserted int `json:"inserted"`
}

// Run recomputes all set-level issues in one transaction:
//
//  1. purge the previous set-level issues (batch_id NULL, set rule codes),
//  2. compute E005/E006/I001 over the current database state,
//  3. insert the fresh issues in deterministic (rule_code, entity natural
//     key) order with batch_id NULL, rule_version domain.RuleVersion and
//     severity domain.SeverityForCode,
//  4. return the Result with the per-rule insert counts.
//
// Any error rolls the whole recompute back — the database then keeps the
// previous set-level issues untouched.
func (e *Engine) Run(ctx context.Context) (Result, error) {
	result := Result{RuleVersion: domain.RuleVersion, InsertedByRule: map[string]int{}}
	err := e.db.Do(ctx, func(tx *repo.Tx) error {
		deleted, err := tx.Issues.DeleteSetLevel(ctx)
		if err != nil {
			return err
		}
		result.Deleted = int(deleted)

		findings, err := e.compute(ctx, tx)
		if err != nil {
			return err
		}

		now := time.Now().UTC()
		for _, f := range findings {
			iss := f.issue
			iss.ID = domain.NewID("iss")
			iss.RuleVersion = domain.RuleVersion
			iss.CreatedAt = now
			if err := tx.Issues.Insert(ctx, iss); err != nil {
				return err
			}
			result.InsertedByRule[iss.RuleCode]++
			result.Inserted++
		}
		return nil
	})
	if err != nil {
		return Result{}, fmt.Errorf("validation: run: %w", err)
	}
	return result, nil
}

// finding is one computed set-level issue plus the natural key of its
// entity, which orders the deterministic insertion sequence.
type finding struct {
	issue     domain.Issue
	entityKey string
}

// compute runs the three set-level rules against the transaction's view of
// the database and returns the findings sorted by (rule_code, entity
// natural key). Rules read resources in (resource_type, natural_key) order
// (repo Search ordering), so every iteration is deterministic.
func (e *Engine) compute(ctx context.Context, tx *repo.Tx) ([]finding, error) {
	devices, err := listResources(ctx, tx, domain.ResourceTypeDevice)
	if err != nil {
		return nil, err
	}
	pools, err := listResources(ctx, tx, domain.ResourceTypeIPPool)
	if err != nil {
		return nil, err
	}

	var findings []finding
	f05, err := subnetOverlap(pools)
	if err != nil {
		return nil, err
	}
	findings = append(findings, f05...)
	f06, err := duplicateAddress(devices, pools)
	if err != nil {
		return nil, err
	}
	findings = append(findings, f06...)
	f01, err := unlinkedDevices(ctx, tx, devices)
	if err != nil {
		return nil, err
	}
	findings = append(findings, f01...)

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].issue.RuleCode != findings[j].issue.RuleCode {
			return findings[i].issue.RuleCode < findings[j].issue.RuleCode
		}
		return findings[i].entityKey < findings[j].entityKey
	})
	return findings, nil
}

// subnetOverlap implements E005: every pair of ip_pool CIDRs that overlap
// (same address family) yields one error issue. The entity is the pool with
// the lexicographically smaller natural key (the pools arrive sorted by
// natural key, so pairs enumerate pool_a ≤ pool_b); pools whose cidr
// attribute is absent or unparseable are skipped — the row phase already
// flagged those rows (E002/E004) and they were never stored. Evidence keys
// pool_a/pool_b carry "name(cidr)" strings, overlap_cidr the intersection
// (the narrower prefix); the extra pool_a_id/pool_b_id keys satisfy §7's
// pool(id,name,cidr) evidence contract.
func subnetOverlap(pools []domain.Resource) ([]finding, error) {
	type poolNet struct {
		res    domain.Resource
		cidr   string       // stored attribute text
		prefix netip.Prefix // masked; IPv4 only
	}
	nets := make([]poolNet, 0, len(pools))
	for _, p := range pools {
		cidr := p.Attributes["cidr"]
		if cidr == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil || !prefix.Addr().Is4() {
			continue
		}
		nets = append(nets, poolNet{res: p, cidr: cidr, prefix: prefix.Masked()})
	}

	var findings []finding
	for i := 0; i < len(nets); i++ {
		for j := i + 1; j < len(nets); j++ {
			a, b := nets[i], nets[j]
			if !a.prefix.Overlaps(b.prefix) {
				continue
			}
			// Two prefixes of the same family either nest or are disjoint,
			// so the overlap is the narrower (higher-bit) prefix.
			overlap := a.prefix
			if b.prefix.Bits() > a.prefix.Bits() {
				overlap = b.prefix
			}
			findings = append(findings, finding{
				entityKey: a.res.NaturalKey,
				issue: domain.Issue{
					RuleCode:   domain.RuleSubnetOverlap,
					Severity:   domain.SeverityError,
					EntityKind: domain.EntityKindResource,
					EntityID:   a.res.ID,
					Message: fmt.Sprintf("地址池 %s(%s) 与 %s(%s) 网段重叠",
						a.res.NaturalKey, a.cidr, b.res.NaturalKey, b.cidr),
					Evidence: map[string]string{
						"pool_a":       a.res.NaturalKey + "(" + a.cidr + ")",
						"pool_b":       b.res.NaturalKey + "(" + b.cidr + ")",
						"pool_a_id":    a.res.ID,
						"pool_b_id":    b.res.ID,
						"overlap_cidr": overlap.String(),
					},
				},
			})
		}
	}
	return findings, nil
}

// duplicateAddress implements E006: an address (device mgmt_ip or ip_pool
// gateway) used by more than one resource yields one error issue. The
// entity is the first resource by (type, natural_key); the usage lists are
// built in that order because devices and pools arrive sorted by natural
// key and devices are scanned first. Evidence carries the address plus the
// resources as a sorted "code=value" list.
func duplicateAddress(devices, pools []domain.Resource) ([]finding, error) {
	type use struct {
		res   domain.Resource
		value string
	}
	usage := map[string][]use{}
	for _, d := range devices {
		if v := d.Attributes["mgmt_ip"]; v != "" {
			usage[v] = append(usage[v], use{res: d, value: v})
		}
	}
	for _, p := range pools {
		if v := p.Attributes["gateway"]; v != "" {
			usage[v] = append(usage[v], use{res: p, value: v})
		}
	}

	var findings []finding
	for _, addr := range sortedKeys(usage) {
		uses := usage[addr]
		if len(uses) < 2 {
			continue
		}
		rendered := make([]string, 0, len(uses))
		codes := make([]string, 0, len(uses))
		for _, u := range uses {
			rendered = append(rendered, u.res.NaturalKey+"="+u.value)
			codes = append(codes, u.res.NaturalKey)
		}
		entity := uses[0].res
		findings = append(findings, finding{
			entityKey: entity.NaturalKey,
			issue: domain.Issue{
				RuleCode:   domain.RuleDuplicateAddress,
				Severity:   domain.SeverityError,
				EntityKind: domain.EntityKindResource,
				EntityID:   entity.ID,
				Message: fmt.Sprintf("地址 %s 被 %d 个资源重复使用：%s",
					addr, len(uses), strings.Join(codes, "、")),
				Evidence: map[string]string{
					"address":   addr,
					"resources": strings.Join(rendered, ", "),
				},
			},
		})
	}
	return findings, nil
}

// unlinkedDevices implements I001: every device participating in zero
// relations yields one info issue. RelationRepo.CountByResourceIDs counts
// both endpoints (from and to) of every relation, so a device referenced
// only as a target is linked too.
func unlinkedDevices(ctx context.Context, tx *repo.Tx, devices []domain.Resource) ([]finding, error) {
	ids := make([]string, 0, len(devices))
	for _, d := range devices {
		ids = append(ids, d.ID)
	}
	counts, err := tx.Relations.CountByResourceIDs(ctx, ids)
	if err != nil {
		return nil, err
	}

	var findings []finding
	for _, d := range devices {
		if counts[d.ID] != 0 {
			continue
		}
		findings = append(findings, finding{
			entityKey: d.NaturalKey,
			issue: domain.Issue{
				RuleCode:   domain.RuleUnlinkedDevice,
				Severity:   domain.SeverityInfo,
				EntityKind: domain.EntityKindResource,
				EntityID:   d.ID,
				Message: fmt.Sprintf("设备 %s（%s）未关联任何关系",
					d.NaturalKey, d.Name),
				Evidence: map[string]string{
					"device_id":   d.ID,
					"device_code": d.NaturalKey,
					"device_name": d.Name,
				},
			},
		})
	}
	return findings, nil
}

// listResources returns all resources of one type ordered by natural key
// (repo Search ordering), read through the current transaction.
func listResources(ctx context.Context, tx *repo.Tx, rt domain.ResourceType) ([]domain.Resource, error) {
	res, _, err := tx.Resources.Search(ctx, repo.ResourceSearchFilter{Type: rt})
	if err != nil {
		return nil, fmt.Errorf("validation: list %s resources: %w", rt, err)
	}
	return res, nil
}

// sortedKeys returns the keys of m in sorted order (deterministic iteration).
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
