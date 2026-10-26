package repo

// migrations_v37_test.go — issue #2 phase 1: the configurable risk-rule
// tables. The v37 CHECK enums are part of the 不可注入 red line (a rule
// row can never carry a foreign token), so the test drives the CHECKs
// through the REAL repo (Create), and the audit ledger through the same
// Do-transaction shape the service uses. The chain reaches v37 from
// scratch (newTestDB) — the max-version pins live in the per-version
// tests and the facts gate.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

func TestMigrationV37FeeRiskRules(t *testing.T) {
	db := newTestDB(t) // migrates to head (v37) from an empty file
	ctx := context.Background()
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

	rule := domain.FeeRiskRule{
		ID: "rule_v37", Name: "电费单月超阈值", FeeType: domain.FeeTypeElectricity,
		Metric: "amount", Aggregation: "sum", Dimension: "site_month",
		Comparator: "gt", Threshold: 500000, WindowMonths: 1,
		Severity: "high", Description: "单站单月电费超 5000 元",
		CreatedAt: now, UpdatedAt: now,
	}

	// The wire shape round-trips: Create forces enabled=0 (the observation
	// default), the six-tuple comes back verbatim.
	if err := db.FeeRiskRules.Create(ctx, rule); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := db.FeeRiskRules.GetByID(ctx, "rule_v37")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Enabled {
		t.Fatal("a fresh rule must land DISABLED (观察态) — Create hardcodes enabled=0")
	}
	if got.Name != rule.Name || got.Metric != "amount" || got.Aggregation != "sum" ||
		got.Comparator != "gt" || got.Threshold != 500000 || got.Severity != "high" {
		t.Fatalf("six-tuple roundtrip = %+v", got)
	}

	// The name UNIQUE: a second rule with the same name refuses.
	dup := rule
	dup.ID = "rule_v37_b"
	if err := db.FeeRiskRules.Create(ctx, dup); err == nil {
		t.Fatal("duplicate rule name must refuse (UNIQUE)")
	}

	// The CHECK enums: every foreign token refuses at the DB layer (the
	// service and the engine re-validate — belt and braces, but the CHECK
	// is the floor that makes a bypassed service impossible).
	bad := []domain.FeeRiskRule{
		{ID: "bad_metric", FeeType: domain.FeeTypeElectricity, Metric: "amount_cents'; DROP TABLE x; --", Aggregation: "sum", Dimension: "site_month", Comparator: "gt", Severity: "high"},
		{ID: "bad_agg", FeeType: domain.FeeTypeElectricity, Metric: "amount", Aggregation: "median", Dimension: "site_month", Comparator: "gt", Severity: "high"},
		{ID: "bad_dim", FeeType: domain.FeeTypeElectricity, Metric: "amount", Aggregation: "sum", Dimension: "site_year", Comparator: "gt", Severity: "high"},
		{ID: "bad_comp", FeeType: domain.FeeTypeElectricity, Metric: "amount", Aggregation: "sum", Dimension: "site_month", Comparator: " REGEXP 'x'", Severity: "high"},
		{ID: "bad_sev", FeeType: domain.FeeTypeElectricity, Metric: "amount", Aggregation: "sum", Dimension: "site_month", Comparator: "gt", Severity: "catastrophic"},
		{ID: "bad_fee", FeeType: domain.FeeType("rent"), Metric: "amount", Aggregation: "sum", Dimension: "site_month", Comparator: "gt", Severity: "high"},
	}
	for i, b := range bad {
		b.CreatedAt, b.UpdatedAt = now, now
		if err := db.FeeRiskRules.Create(ctx, b); err == nil {
			t.Fatalf("case %d (%s): CHECK enum must refuse %q", i, b.ID, b.Metric)
		}
	}

	// A negative threshold refuses (CHECK threshold >= 0).
	neg := rule
	neg.ID, neg.Name, neg.Threshold = "rule_v37_neg", "负阈值", -1
	if err := db.FeeRiskRules.Create(ctx, neg); err == nil {
		t.Fatal("negative threshold must refuse (CHECK threshold >= 0)")
	}

	// Window bounds: 0 and 13 refuse (CHECK BETWEEN 1 AND 12).
	for _, w := range []int{0, 13} {
		r := rule
		r.ID = "rule_v37_w"
		r.Name = "窗口越界"
		r.WindowMonths = w
		if err := db.FeeRiskRules.Create(ctx, r); err == nil {
			t.Fatalf("window %d must refuse", w)
		}
	}

	// SetEnabled + the audit ledger in ONE transaction (the service's
	// Do-shape): flip on, flip off, read the ledger back newest-first.
	if err := db.Do(ctx, func(tx *Tx) error {
		if err := tx.FeeRiskRules.SetEnabled(ctx, "rule_v37", true); err != nil {
			return err
		}
		return tx.FeeRiskRules.InsertAudit(ctx, "rule_v37", "enabled", "admin", `{"enabled":{"from":0,"to":1}}`)
	}); err != nil {
		t.Fatalf("enable + audit tx: %v", err)
	}
	if err := db.Do(ctx, func(tx *Tx) error {
		if err := tx.FeeRiskRules.SetEnabled(ctx, "rule_v37", false); err != nil {
			return err
		}
		return tx.FeeRiskRules.InsertAudit(ctx, "rule_v37", "disabled", "admin", `{"enabled":{"from":1,"to":0}}`)
	}); err != nil {
		t.Fatalf("disable + audit tx: %v", err)
	}
	got, err = db.FeeRiskRules.GetByID(ctx, "rule_v37")
	if err != nil || got.Enabled {
		t.Fatalf("final state must be disabled: %+v, %v", got, err)
	}
	entries, err := db.FeeRiskRules.AuditFor(ctx, "rule_v37")
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("audit rows = %d, want 2", len(entries))
	}
	// newest first
	if entries[0].Action != "disabled" || entries[1].Action != "enabled" {
		t.Fatalf("audit order = %s, %s — want disabled (newest) then enabled", entries[0].Action, entries[1].Action)
	}
	if entries[0].Actor != "admin" || !strings.Contains(entries[0].Changes, `"to":0`) {
		t.Fatalf("audit row = %+v — actor and changes must round-trip", entries[0])
	}

	// SetEnabled on an unknown id answers ErrNotFound (the 404 contract).
	err = db.FeeRiskRules.SetEnabled(ctx, "rule_missing", true)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown id must wrap ErrNotFound, got %v", err)
	}

	// List: two rules (one enabled) → the full list sees both, the
	// enabled-only list sees one.
	other := rule
	other.ID, other.Name, other.FeeType = "rule_v37_c", "铁塔月租突增", domain.FeeTypeTowerLease
	other.Comparator, other.WindowMonths = "mom_ratio", 3
	if err := db.FeeRiskRules.Create(ctx, other); err != nil {
		t.Fatalf("create other: %v", err)
	}
	if err := db.Do(ctx, func(tx *Tx) error {
		return tx.FeeRiskRules.SetEnabled(ctx, "rule_v37_c", true)
	}); err != nil {
		t.Fatalf("enable other: %v", err)
	}
	all, err := db.FeeRiskRules.List(ctx, false)
	if err != nil || len(all) != 2 {
		t.Fatalf("list all = %d (%v), want 2", len(all), err)
	}
	enabled, err := db.FeeRiskRules.List(ctx, true)
	if err != nil || len(enabled) != 1 || enabled[0].ID != "rule_v37_c" {
		t.Fatalf("list enabled = %+v (%v), want rule_v37_c only", enabled, err)
	}

	// GetByID unknown → ErrNotFound.
	if _, err := db.FeeRiskRules.GetByID(ctx, "rule_missing"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown GetByID must wrap ErrNotFound, got %v", err)
	}
}
