package sim

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// SevCounts counts check findings per severity (info findings are free in
// the score formula).
type SevCounts struct {
	Error   int `json:"error"`
	Warning int `json:"warning"`
	Info    int `json:"info"`
}

// RunResult is the outcome of one check run. Results carries the full row
// set (findings then baselines) for API consumers; it is excluded from the
// JSON summary on purpose (GetRun exposes it).
type RunResult struct {
	RunID         string               `json:"runId"`
	Trigger       string               `json:"trigger"`
	Counts        SevCounts            `json:"counts"`
	Score         int                  `json:"score"`
	WorkspaceHash string               `json:"workspaceHash"`
	DurationMs    int                  `json:"durationMs"`
	Results       []domain.CheckResult `json:"-"`
}

// Entity kinds of the check catalog (free-form strings per the
// domain.CheckResult contract; deliberately NOT the Phase 1 EntityKind
// enum whose values are "resource"/"relation").
const (
	entityKindWorkspace = "workspace"
	entityKindDevice    = "device"
	entityKindInterface = "interface"
)

// checkSeverity is the catalog severity of every check code
// (PROJECT_BRIEF_P2 §5).
var checkSeverity = map[string]domain.Severity{
	domain.CheckDuplicateHostAddress:  domain.SeverityError,
	domain.CheckSubnetOverlap:         domain.SeverityError,
	domain.CheckIsolatedDevice:        domain.SeverityWarning,
	domain.CheckPartialPartition:      domain.SeverityInfo,
	domain.CheckUnconfiguredInterface: domain.SeverityInfo,
}

// checkEntityKind is the entity kind of every check code.
var checkEntityKind = map[string]string{
	domain.CheckDuplicateHostAddress:  entityKindWorkspace,
	domain.CheckSubnetOverlap:         entityKindWorkspace,
	domain.CheckIsolatedDevice:        entityKindDevice,
	domain.CheckPartialPartition:      entityKindWorkspace,
	domain.CheckUnconfiguredInterface: entityKindInterface,
}

// RunChecks executes the five deterministic checks against the current
// workspace state and stores the run (plus its result rows and the
// canonical edge snapshot) in ONE transaction. Identical state plus
// domain.SimRuleVersion always yields identical findings in identical
// order. trigger records what started the run (manual/api/mcp/
// auto_after_ops); runs triggered right after a changeset pass its id via
// the internal runChecksWith.
func (s *Service) RunChecks(ctx context.Context, workspaceID string, trigger domain.CheckTrigger) (RunResult, error) {
	if !trigger.IsValid() {
		return RunResult{}, fmt.Errorf("sim: run checks: invalid trigger %q: %w", trigger, domain.ErrInvalidOp)
	}
	if _, err := s.mustWorkspace(ctx, workspaceID); err != nil {
		return RunResult{}, err
	}
	return s.runChecksWith(ctx, workspaceID, trigger, "")
}

// runChecksWith is the storage path of RunChecks; changesetID is set for
// the auto_after_ops runs attached to a just-applied changeset.
func (s *Service) runChecksWith(ctx context.Context, workspaceID string, trigger domain.CheckTrigger, changesetID string) (RunResult, error) {
	startedAt := s.clock() // recorded timestamps honor the Now hook
	startedReal := time.Now()
	var run RunResult
	err := s.db.Do(ctx, func(tx *repo.Tx) error {
		state, err := loadState(ctx, storeOfTx(tx), workspaceID)
		if err != nil {
			return err
		}
		findings, counts, score := computeChecks(state, workspaceID)
		topoJSON, err := state.topologyJSON()
		if err != nil {
			return err
		}
		hash, err := state.hash()
		if err != nil {
			return err
		}

		n, err := tx.Sequences.Allocate(ctx, domain.SeqCheckRun)
		if err != nil {
			return fmt.Errorf("sim: allocate check run id: %w", err)
		}
		runID := domain.FormatSimID(domain.SeqCheckRun, n)

		// Findings first (seq 1..N in canonical order), then one passed
		// baseline row per check code (seq N+1..N+5, CheckCodes order,
		// message "ok" — every run is self-describing).
		results := make([]domain.CheckResult, 0, len(findings)+len(domain.CheckCodes))
		for i, f := range findings {
			results = append(results, f.result(runID, i+1))
		}
		for i, code := range domain.CheckCodes {
			results = append(results, domain.CheckResult{
				RunID:      runID,
				Seq:        len(findings) + i + 1,
				CheckCode:  code,
				Severity:   checkSeverity[code],
				Passed:     true,
				Message:    "ok",
				DetailJSON: "{}",
			})
		}

		durationMs := int(time.Since(startedReal).Milliseconds())
		if err := tx.CheckRuns.Insert(ctx, domain.CheckRun{
			ID: runID, WorkspaceID: workspaceID, ChangesetID: changesetID,
			Trigger: trigger, RuleVersion: domain.SimRuleVersion,
			StartedAt: startedAt, DurationMs: durationMs,
			Score: score, TopologyJSON: topoJSON,
		}, results); err != nil {
			return fmt.Errorf("sim: insert check run %s: %w", runID, err)
		}
		run = RunResult{
			RunID: runID, Trigger: trigger.String(), Counts: counts, Score: score,
			WorkspaceHash: hash, DurationMs: durationMs, Results: results,
		}
		return nil
	})
	if err != nil {
		return RunResult{}, fmt.Errorf("sim: run checks: %w", err)
	}
	return run, nil
}

// ListRuns returns one page of the workspace's check runs (repo passthrough,
// newest first — ids are globally monotonic run-<n>).
func (s *Service) ListRuns(ctx context.Context, workspaceID string, page, pageSize int) ([]domain.CheckRun, int, error) {
	if _, err := s.mustWorkspace(ctx, workspaceID); err != nil {
		return nil, 0, err
	}
	runs, total, err := s.db.CheckRuns.ListByWorkspace(ctx, workspaceID, page, pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("sim: list runs of workspace %s: %w", workspaceID, err)
	}
	return runs, total, nil
}

// GetRun returns one check run of the workspace with its result rows. A
// run of another workspace behaves like an unknown run
// (domain.ErrNotFound).
func (s *Service) GetRun(ctx context.Context, workspaceID, runID string) (domain.CheckRun, []domain.CheckResult, error) {
	if _, err := s.mustWorkspace(ctx, workspaceID); err != nil {
		return domain.CheckRun{}, nil, err
	}
	run, err := s.db.CheckRuns.GetByID(ctx, runID)
	if err != nil {
		return domain.CheckRun{}, nil, fmt.Errorf("sim: check run %s: %w", runID, err)
	}
	if run.WorkspaceID != workspaceID {
		return domain.CheckRun{}, nil, fmt.Errorf("sim: check run %s: %w", runID, domain.ErrNotFound)
	}
	results, err := s.db.CheckRuns.GetResults(ctx, runID)
	if err != nil {
		return domain.CheckRun{}, nil, fmt.Errorf("sim: results of check run %s: %w", runID, err)
	}
	return run, results, nil
}

// LatestRun returns the newest check run of the workspace with its result
// rows, or domain.ErrNotFound when none exists.
func (s *Service) LatestRun(ctx context.Context, workspaceID string) (domain.CheckRun, []domain.CheckResult, error) {
	if _, err := s.mustWorkspace(ctx, workspaceID); err != nil {
		return domain.CheckRun{}, nil, err
	}
	run, err := s.db.CheckRuns.Latest(ctx, workspaceID)
	if err != nil {
		return domain.CheckRun{}, nil, fmt.Errorf("sim: latest run of workspace %s: %w", workspaceID, err)
	}
	results, err := s.db.CheckRuns.GetResults(ctx, run.ID)
	if err != nil {
		return domain.CheckRun{}, nil, fmt.Errorf("sim: results of check run %s: %w", run.ID, err)
	}
	return run, results, nil
}

// ---- check computation (deterministic; rule_version domain.SimRuleVersion) ----

// checkFinding is one computed finding before canonical ordering.
type checkFinding struct {
	code     string
	sortKey  string
	entityID string
	message  string
	detail   any
}

// result renders the finding as a stored row (severity and entity kind
// come from the catalog).
func (f checkFinding) result(runID string, seq int) domain.CheckResult {
	return domain.CheckResult{
		RunID:      runID,
		Seq:        seq,
		CheckCode:  f.code,
		Severity:   checkSeverity[f.code],
		Passed:     false,
		EntityKind: checkEntityKind[f.code],
		EntityID:   f.entityID,
		Message:    f.message,
		DetailJSON: detailJSONOf(f.detail),
	}
}

// ifaceView pairs one interface with its device; the check functions
// consume the (deviceCode, name)-sorted canonical list.
type ifaceView struct {
	iface  domain.SimInterface
	device domain.VirtualDevice
}

// computeChecks runs all five checks over the canonical state and returns
// the findings in canonical order — (check_code, entity key, detail
// canonical JSON) — plus the severity counts and the score
// max(0, 100 − 5·error_findings − 1·warning_findings). Entity keys are
// business keys: CHK-001 the address, CHK-002 the (a,b) endpoint pair,
// CHK-003 the device code, CHK-004 the sorted member codes, CHK-005
// deviceCode/interfaceName.
func computeChecks(state *simState, workspaceID string) ([]checkFinding, SevCounts, int) {
	ifaces := make([]ifaceView, 0)
	for _, d := range state.devices {
		for _, i := range d.Interfaces {
			ifaces = append(ifaces, ifaceView{iface: i, device: d.Dev})
		}
	}

	var findings []checkFinding
	findings = append(findings, checkDuplicateHostAddress(workspaceID, ifaces)...)
	findings = append(findings, checkSubnetOverlap(workspaceID, ifaces)...)
	findings = append(findings, checkIsolatedDevice(state)...)
	findings = append(findings, checkPartialPartition(state, workspaceID)...)
	findings = append(findings, checkUnconfiguredInterface(ifaces)...)

	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].code != findings[j].code {
			return findings[i].code < findings[j].code
		}
		if findings[i].sortKey != findings[j].sortKey {
			return findings[i].sortKey < findings[j].sortKey
		}
		return detailJSONOf(findings[i].detail) < detailJSONOf(findings[j].detail)
	})

	counts := SevCounts{}
	for _, f := range findings {
		switch checkSeverity[f.code] {
		case domain.SeverityError:
			counts.Error++
		case domain.SeverityWarning:
			counts.Warning++
		case domain.SeverityInfo:
			counts.Info++
		}
	}
	score := 100 - 5*counts.Error - 1*counts.Warning
	if score < 0 {
		score = 0
	}
	return findings, counts, score
}

// ifaceRef is the interface reference shape of the CHK-001 detail.
type ifaceRef struct {
	InterfaceID string `json:"interfaceId"`
	DeviceID    string `json:"deviceId"`
	DeviceCode  string `json:"deviceCode"`
	Name        string `json:"name"`
}

// netIfaceRef extends ifaceRef with the configured address (CHK-002).
type netIfaceRef struct {
	InterfaceID string `json:"interfaceId"`
	DeviceID    string `json:"deviceId"`
	DeviceCode  string `json:"deviceCode"`
	Name        string `json:"name"`
	IPv4Addr    string `json:"ipv4Addr"`
	IPv4Prefix  int    `json:"ipv4Prefix"`
}

// dupAddrDetail is the CHK-001 detail payload.
type dupAddrDetail struct {
	Addr       string     `json:"addr"`
	Interfaces []ifaceRef `json:"interfaces"`
}

// overlapDetail is the CHK-002 detail payload.
type overlapDetail struct {
	AInterface netIfaceRef `json:"aInterface"`
	BInterface netIfaceRef `json:"bInterface"`
	Kind       string      `json:"kind"`
	NetworkA   string      `json:"networkA"`
	NetworkB   string      `json:"networkB"`
}

// isolatedDetail is the CHK-003 detail payload.
type isolatedDetail struct {
	DeviceID   string `json:"deviceId"`
	Code       string `json:"code"`
	TotalLinks int    `json:"totalLinks"`
	UpLinks    int    `json:"upLinks"`
}

// partitionDetail is the CHK-004 detail payload.
type partitionDetail struct {
	Members []string `json:"members"`
}

// unconfiguredDetail is the CHK-005 detail payload.
type unconfiguredDetail struct {
	InterfaceID string `json:"interfaceId"`
	DeviceID    string `json:"deviceId"`
	DeviceCode  string `json:"deviceCode"`
	Name        string `json:"name"`
}

// checkDuplicateHostAddress implements CHK-001: every IPv4 address
// configured on ≥2 interfaces that are up on up devices yields one error
// finding; the detail lists the interfaces sorted by (deviceCode, name)
// (ifaces arrives in that order).
func checkDuplicateHostAddress(workspaceID string, ifaces []ifaceView) []checkFinding {
	byAddr := map[string][]ifaceView{}
	for _, v := range ifaces {
		if v.iface.Status != domain.EntityStatusUp || v.device.Status != domain.EntityStatusUp || v.iface.IPv4Addr == "" {
			continue
		}
		byAddr[v.iface.IPv4Addr] = append(byAddr[v.iface.IPv4Addr], v)
	}
	var findings []checkFinding
	for _, addr := range sortedKeysOf(byAddr) {
		group := byAddr[addr]
		if len(group) < 2 {
			continue
		}
		refs := make([]ifaceRef, 0, len(group))
		for _, v := range group {
			refs = append(refs, ifaceRef{
				InterfaceID: v.iface.ID, DeviceID: v.device.ID,
				DeviceCode: v.device.Code, Name: v.iface.Name,
			})
		}
		findings = append(findings, checkFinding{
			code:     domain.CheckDuplicateHostAddress,
			sortKey:  addr,
			entityID: workspaceID,
			message:  fmt.Sprintf("duplicate host address %s used by %d interfaces", addr, len(group)),
			detail:   dupAddrDetail{Addr: addr, Interfaces: refs},
		})
	}
	return findings
}

// checkSubnetOverlap implements CHK-002: every unordered pair of
// configured up interfaces (up devices) whose subnets overlap yields one
// error finding — EXCEPT pairs with the identical address (CHK-001 owns
// them). a < b by (deviceCode, name); the interfaces arrive sorted, so the
// i<j enumeration is the canonical pair order.
//
// Overlap kinds (interpretation pinned in the worklog — pure CIDR ranges
// can only nest or be disjoint, so "partial" denotes OFFSET nesting):
//
//	identical — same masked network (e.g. 10.0.0.1/24 vs 10.0.0.9/24)
//	contains  — the smaller network sits at the base of the larger (same
//	            network base, e.g. 10.0.0.1/16 vs 10.0.0.9/24)
//	partial   — the smaller network is nested but offset from the larger's
//	            base (e.g. 10.0.0.1/24 vs 10.0.0.130/25)
func checkSubnetOverlap(workspaceID string, ifaces []ifaceView) []checkFinding {
	type configured struct {
		view ifaceView
		sub  subnet
	}
	up := make([]configured, 0, len(ifaces))
	for _, v := range ifaces {
		if v.iface.Status != domain.EntityStatusUp || v.device.Status != domain.EntityStatusUp || v.iface.IPv4Addr == "" {
			continue
		}
		sub, err := subnetOf(v.iface.IPv4Addr, v.iface.IPv4Prefix)
		if err != nil {
			continue // unreachable: ops validate addresses before storage
		}
		up = append(up, configured{view: v, sub: sub})
	}

	var findings []checkFinding
	for i := 0; i < len(up); i++ {
		for j := i + 1; j < len(up); j++ {
			a, b := up[i], up[j]
			if a.view.iface.IPv4Addr == b.view.iface.IPv4Addr {
				continue // identical addresses belong to CHK-001
			}
			kind, netA, netB, ok := classifyOverlap(a.sub, b.sub)
			if !ok {
				continue // disjoint networks
			}
			findings = append(findings, checkFinding{
				code: domain.CheckSubnetOverlap,
				sortKey: a.view.device.Code + "\x00" + a.view.iface.Name + "\x00" +
					b.view.device.Code + "\x00" + b.view.iface.Name,
				entityID: workspaceID,
				message:  fmt.Sprintf("subnets %s and %s overlap (%s)", netA, netB, kind),
				detail: overlapDetail{
					AInterface: netRefOf(a.view),
					BInterface: netRefOf(b.view),
					Kind:       kind, NetworkA: netA, NetworkB: netB,
				},
			})
		}
	}
	return findings
}

// checkIsolatedDevice implements CHK-003: every up device whose live-link
// count is zero (no interfaces, or every touching link failed one of the
// up conditions) yields one warning. "Live" matches the §5 connectivity
// predicate (link up + both interfaces up + both devices up) so CHK-003
// (singletons) and CHK-004 (multi-member components) partition the
// up-device graph coherently.
func checkIsolatedDevice(state *simState) []checkFinding {
	live, total := liveLinkCounts(state)
	var findings []checkFinding
	for _, d := range state.devices {
		if d.Dev.Status != domain.EntityStatusUp || live[d.Dev.ID] != 0 {
			continue
		}
		findings = append(findings, checkFinding{
			code:     domain.CheckIsolatedDevice,
			sortKey:  d.Dev.Code,
			entityID: d.Dev.ID,
			message:  fmt.Sprintf("device %s is isolated (no up links)", d.Dev.Code),
			detail: isolatedDetail{
				DeviceID:   d.Dev.ID,
				Code:       d.Dev.Code,
				TotalLinks: total[d.Dev.ID],
				UpLinks:    live[d.Dev.ID],
			},
		})
	}
	return findings
}

// checkPartialPartition implements CHK-004: the up devices split into
// connected components via live links; the largest component is the
// reference (ties → lexicographically smallest member set) and every OTHER
// component with ≥2 members yields one info finding.
func checkPartialPartition(state *simState, workspaceID string) []checkFinding {
	components := liveComponents(state)
	if len(components) < 2 {
		return nil
	}
	ref := 0
	for i := 1; i < len(components); i++ {
		if len(components[i]) > len(components[ref]) ||
			(len(components[i]) == len(components[ref]) &&
				strings.Join(components[i], "\x00") < strings.Join(components[ref], "\x00")) {
			ref = i
		}
	}
	var findings []checkFinding
	for i, members := range components {
		if i == ref || len(members) < 2 {
			continue
		}
		findings = append(findings, checkFinding{
			code:     domain.CheckPartialPartition,
			sortKey:  strings.Join(members, "\x00"),
			entityID: workspaceID,
			message:  fmt.Sprintf("component of %d devices is disconnected from the main component", len(members)),
			detail:   partitionDetail{Members: members},
		})
	}
	return findings
}

// checkUnconfiguredInterface implements CHK-005: every up interface of an
// up device without an IPv4 address yields one info finding.
func checkUnconfiguredInterface(ifaces []ifaceView) []checkFinding {
	var findings []checkFinding
	for _, v := range ifaces {
		if v.iface.Status != domain.EntityStatusUp || v.device.Status != domain.EntityStatusUp || v.iface.IPv4Addr != "" {
			continue
		}
		findings = append(findings, checkFinding{
			code:     domain.CheckUnconfiguredInterface,
			sortKey:  v.device.Code + "\x00" + v.iface.Name,
			entityID: v.iface.ID,
			message:  fmt.Sprintf("interface %s/%s has no IPv4 address", v.device.Code, v.iface.Name),
			detail: unconfiguredDetail{
				InterfaceID: v.iface.ID, DeviceID: v.device.ID,
				DeviceCode: v.device.Code, Name: v.iface.Name,
			},
		})
	}
	return findings
}

// ---- live-edge graph helpers (shared by CHK-003/CHK-004 and Path) ----

// liveLink reports whether a link counts as a live connection: link up,
// both interfaces up, both devices up — the §5 connectivity predicate.
func (s *simState) liveLink(l domain.SimLink) bool {
	if l.Status != domain.EntityStatusUp {
		return false
	}
	ifa, okA := s.ifaceByID[l.AInterfaceID]
	ifz, okZ := s.ifaceByID[l.ZInterfaceID]
	if !okA || !okZ || ifa.Status != domain.EntityStatusUp || ifz.Status != domain.EntityStatusUp {
		return false
	}
	devA, okA := s.deviceByID[ifa.DeviceID]
	devZ, okZ := s.deviceByID[ifz.DeviceID]
	return okA && okZ && devA.Status == domain.EntityStatusUp && devZ.Status == domain.EntityStatusUp
}

// liveLinkCounts returns, per device id, the number of live links and the
// total number of links touching the device's interfaces.
func liveLinkCounts(state *simState) (live, total map[string]int) {
	live = map[string]int{}
	total = map[string]int{}
	for _, l := range state.links {
		a, aOK := state.ifaceDevice[l.AInterfaceID]
		z, zOK := state.ifaceDevice[l.ZInterfaceID]
		if aOK {
			total[a]++
		}
		if zOK && z != a {
			total[z]++
		}
		if state.liveLink(l) {
			if aOK {
				live[a]++
			}
			if zOK && z != a {
				live[z]++
			}
		}
	}
	return live, total
}

// liveComponents returns the connected components of the up-device graph
// over live links: each component a sorted list of device codes, the
// component list sorted by member set (deterministic).
func liveComponents(state *simState) [][]string {
	adj := map[string]map[string]bool{}
	for _, l := range state.links {
		if !state.liveLink(l) {
			continue
		}
		a := state.ifaceDevice[l.AInterfaceID]
		z := state.ifaceDevice[l.ZInterfaceID]
		if adj[a] == nil {
			adj[a] = map[string]bool{}
		}
		if adj[z] == nil {
			adj[z] = map[string]bool{}
		}
		adj[a][z] = true
		adj[z][a] = true
	}

	visited := map[string]bool{}
	var components [][]string
	for _, d := range state.devices { // code order → deterministic seeding
		if d.Dev.Status != domain.EntityStatusUp || visited[d.Dev.ID] {
			continue
		}
		var members []string
		queue := []string{d.Dev.ID}
		visited[d.Dev.ID] = true
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			members = append(members, state.deviceByID[cur].Code)
			neighbors := make([]string, 0, len(adj[cur]))
			for nb := range adj[cur] {
				neighbors = append(neighbors, nb)
			}
			sort.Strings(neighbors) // deterministic traversal
			for _, nb := range neighbors {
				if !visited[nb] {
					visited[nb] = true
					queue = append(queue, nb)
				}
			}
		}
		sort.Strings(members)
		components = append(components, members)
	}
	sort.Slice(components, func(i, j int) bool {
		return strings.Join(components[i], "\x00") < strings.Join(components[j], "\x00")
	})
	return components
}

// ---- IPv4 network math (uint32, big-endian; §5) ----

// subnet is one masked CIDR network with its address range [lo, hi].
type subnet struct {
	net  uint32
	bits int
	lo   uint32
	hi   uint32
}

// subnetOf computes the masked network of addr/prefix.
func subnetOf(addr string, prefix int) (subnet, error) {
	parsed, err := netip.ParseAddr(addr)
	if err != nil || !parsed.Is4() || prefix < 0 || prefix > 32 {
		return subnet{}, fmt.Errorf("invalid IPv4 %s/%d", addr, prefix)
	}
	oct := parsed.As4()
	ip := uint32(oct[0])<<24 | uint32(oct[1])<<16 | uint32(oct[2])<<8 | uint32(oct[3])
	var mask uint32
	if prefix > 0 {
		mask = ^uint32(0) << (32 - prefix)
	}
	net := ip & mask
	return subnet{net: net, bits: prefix, lo: net, hi: net | ^mask}, nil
}

// classifyOverlap classifies the overlap of two subnets:
//
//	identical — same network and same prefix (e.g. 10.0.0.1/24 vs
//	            10.0.0.9/24, different hosts in one /24)
//	contains  — one range contains the other AND the smaller network's
//	            base equals the larger's base (aligned nesting, e.g.
//	            10.0.0.1/16 vs 10.0.0.9/24: both mask to 10.0.0.0 but the
//	            prefixes differ)
//	partial   — nested but offset from the container's base (e.g.
//	            10.0.0.1/24 vs 10.0.0.130/25)
//
// ok is false when the ranges are disjoint. Note that "identical" must
// compare the PREFIX as well: a /16 and a /24 over the same base mask to
// the same network number but are different networks (the /24 sits inside
// the /16 — ACC-P2-4's "contains" case).
func classifyOverlap(a, b subnet) (kind, netA, netB string, ok bool) {
	netA, netB = subnetText(a), subnetText(b)
	if a.lo > b.hi || b.lo > a.hi {
		return "", "", "", false // disjoint
	}
	if a.net == b.net && a.bits == b.bits {
		return "identical", netA, netB, true
	}
	// CIDR ranges nest or are disjoint: the wider one contains the
	// narrower one. "contains" requires alignment at the shared base.
	if a.lo <= b.lo && b.hi <= a.hi { // a contains b
		if b.lo == a.lo {
			return "contains", netA, netB, true
		}
		return "partial", netA, netB, true
	}
	// b contains a
	if a.lo == b.lo {
		return "contains", netA, netB, true
	}
	return "partial", netA, netB, true
}

// subnetText renders the network in CIDR notation ("10.0.0.0/24").
func subnetText(s subnet) string {
	return fmt.Sprintf("%d.%d.%d.%d/%d",
		byte(s.net>>24), byte(s.net>>16), byte(s.net>>8), byte(s.net), s.bits)
}

// ---- small helpers ----

// netRefOf renders an interface view as the CHK-002 detail reference.
func netRefOf(v ifaceView) netIfaceRef {
	return netIfaceRef{
		InterfaceID: v.iface.ID, DeviceID: v.device.ID,
		DeviceCode: v.device.Code, Name: v.iface.Name,
		IPv4Addr: v.iface.IPv4Addr, IPv4Prefix: v.iface.IPv4Prefix,
	}
}

// sortedKeysOf returns the keys of m in sorted order.
func sortedKeysOf[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// detailJSONOf marshals a typed detail payload; a nil detail yields "{}".
func detailJSONOf(detail any) string {
	if detail == nil {
		return "{}"
	}
	b, err := json.Marshal(detail)
	if err != nil {
		return "{}"
	}
	return string(b)
}
