//go:build !duckdb

package fees

// The std stub's honest-refusal contract, pinned at the ENGINE boundary
// (#64 acceptance): every query preset answers ErrEngineNotBuilt so no
// caller — service layer, cross-layer composition, or future wiring —
// can silently get an empty result that reads as "no findings" from a
// build that never ran the analysis. The service-level Risks() path has
// its own 501 pin (risks_test.go); this file pins the methods
// themselves so a direct preset call cannot drift.
// The same file carries the small zero-covered helpers: normalizeLimit
// (the preset clamp), the two operator-facing Error() texts, and
// AtoiOr (the flag-parsing default).

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestStdEngineEveryMethodRefuses(t *testing.T) {
	var e stdQueryEngine = newQueryEngine().(stdQueryEngine)
	if e.Available() {
		t.Fatalf("std engine Available() = true, want false (the duckdb build is the only real one)")
	}
	ctx := context.Background()
	q := RiskQuery{FeeType: FeeTypeElectricity, Year: 2026, Month: 10}
	calls := []struct {
		name string
		call func() error
	}{
		{"Totals", func() error { _, err := e.Totals(ctx, "dir", FeeTypeElectricity, 2026, 10, 100); return err }},
		{"Duplicates", func() error { _, err := e.Duplicates(ctx, "dir", FeeTypeElectricity, 2026, 10, 100); return err }},
		{"RiskDuplicatesPreset", func() error { _, _, err := e.RiskDuplicatesPreset(ctx, "dir", q); return err }},
		{"RiskLedgerMismatchPreset", func() error { _, _, err := e.RiskLedgerMismatchPreset(ctx, "dir", q); return err }},
		{"RiskSpikePreset", func() error { _, _, err := e.RiskSpikePreset(ctx, "dir", q); return err }},
		{"RiskOutlierPreset", func() error { _, _, err := e.RiskOutlierPreset(ctx, "dir", q); return err }},
		{"SitePeriodTotals", func() error { _, err := e.SitePeriodTotals(ctx, "dir", FeeTypeElectricity, 2026, 10); return err }},
		{"SiteTotalsAll", func() error { _, err := e.SiteTotalsAll(ctx, "dir", FeeTypeElectricity, 2026, 10); return err }},
		{"EvidenceFor", func() error { _, err := e.EvidenceFor(ctx, "dir", FeeTypeElectricity, nil, 10); return err }},
	}
	for _, c := range calls {
		if err := c.call(); !errors.Is(err, ErrEngineNotBuilt) {
			t.Errorf("%s on std build = %v, want ErrEngineNotBuilt (the honest 501, never a fake empty)", c.name, err)
		}
	}
}

func TestNormalizeLimitClamps(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, 100}, {-5, 100}, // no/invalid limit → the documented default
		{1, 1}, {100, 100}, {999, 999}, // in-range passthrough
		{1000, 1000}, {1001, 1000}, {100000, 1000}, // over-bound clamp
	}
	for _, c := range cases {
		if got := normalizeLimit(c.in); got != c.want {
			t.Errorf("normalizeLimit(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestImportErrorTexts(t *testing.T) {
	dup := &ErrDuplicateImport{BatchID: "fee_abc123"}
	msg := dup.Error()
	if !contains(msg, "fee_abc123") || !contains(msg, "幂等") {
		t.Errorf("ErrDuplicateImport.Error() = %q, want the batch id and the idempotent-skip wording", msg)
	}
	ie := &ImportError{Row: 42, Field: "金额", Raw: "abc", Reason: "不是数字"}
	msg2 := ie.Error()
	for _, want := range []string{"42", "金额", "abc", "不是数字"} {
		if !contains(msg2, want) {
			t.Errorf("ImportError.Error() = %q, want it to contain %q (the operator fixes the FILE — the text must localize the cell)", msg2, want)
		}
	}
}

func TestAtoiOr(t *testing.T) {
	cases := []struct {
		in   string
		def  int
		want int
	}{
		{"", 7, 7},               // empty flag → default
		{"2026", 2000, 2026},     // good value
		{"  2026  ", 2000, 2026}, // surrounding spaces tolerated
		{"20x6", 2000, 2000},     // unparseable → default, never an error path
		{"-3", 1, -3},            // negatives parse (validation is elsewhere)
	}
	for _, c := range cases {
		if got := AtoiOr(c.in, c.def); got != c.want {
			t.Errorf("AtoiOr(%q, %d) = %d, want %d", c.in, c.def, got, c.want)
		}
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
