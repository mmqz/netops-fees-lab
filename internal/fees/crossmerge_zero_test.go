package fees

// crossmerge_zero_test.go — issue #31's pure-core regression net: the
// presence-vs-absence discipline. A site with rows on both sides but 0
// cents on one is NOT the same as a site missing from that side — the
// classify stays row-count-based, the zero side gets named per row, and
// the envelope-level health warnings catch the degenerate shapes (a side
// whose every present site totals 0 — the delta column degenerates to
// the other side's full amounts; a side with no rows at all — the
// reconcile premise is absent). The duckdb-build half
// (crosscheck_duckdb_test.go) proves the universe primitive keeps the
// zero-amount tail past the old top-500 cutoff.

import (
	"strings"
	"testing"
)

// TestCrossMergeZeroSideMarker pins the per-row marker: both-sides-present
// rows with one side's total at 0 keep class both and carry zeroSide; the
// double-zero shape carries both names; only-either rows never do (a
// missing side is absence, not zero presence — the two must stay
// distinguishable, the issue's acceptance 1 and 3).
func TestCrossMergeZeroSideMarker(t *testing.T) {
	elec := []TotalsRow{
		{SiteCode: "B1", Rows: 2, TotalCents: 100_000},
		{SiteCode: "B2", Rows: 1, TotalCents: 50_000},
		{SiteCode: "ZZ", Rows: 1, TotalCents: 0}, // both sides zero
		{SiteCode: "E-ONLY", Rows: 1, TotalCents: 10_000},
	}
	tower := []TotalsRow{
		{SiteCode: "B1", Rows: 1, TotalCents: 0},      // zero tower cents
		{SiteCode: "B2", Rows: 1, TotalCents: 70_000}, // healthy both
		{SiteCode: "ZZ", Rows: 1, TotalCents: 0},
		{SiteCode: "T-ONLY", Rows: 2, TotalCents: 30_000},
	}
	res := crossMerge(2026, 9, elec, tower, nil, nil)
	got := map[string]CrossRow{}
	for _, r := range res.Rows {
		got[r.Canonical] = r
	}
	if r := got["B1"]; r.Class != string(CrossBoth) || r.ZeroSide != "tower" {
		t.Errorf("B1 = %+v, want class both + zeroSide tower", r)
	}
	if r := got["B2"]; r.Class != string(CrossBoth) || r.ZeroSide != "" {
		t.Errorf("B2 = %+v, want healthy both with NO zeroSide", r)
	}
	if r := got["ZZ"]; r.Class != string(CrossBoth) || r.ZeroSide != "electricity+tower" {
		t.Errorf("ZZ = %+v, want the double-zero marker", r)
	}
	if r := got["E-ONLY"]; r.Class != string(CrossOnlyElectricity) || r.ZeroSide != "" {
		t.Errorf("E-ONLY = %+v, want absence without a zeroSide marker", r)
	}
	if r := got["T-ONLY"]; r.Class != string(CrossOnlyTower) || r.ZeroSide != "" {
		t.Errorf("T-ONLY = %+v, want absence without a zeroSide marker", r)
	}
}

// TestCrossSideHealthWarnings pins the envelope notices: the all-zero
// side (delta degenerates to the other side's full amounts — the
// reported shape), the absent side (premise missing), and the healthy
// silence (a warning on good data is its own noise).
func TestCrossSideHealthWarnings(t *testing.T) {
	// shape 1: tower side all zeros, electricity healthy — the #29
	// mirror-image reconcile. The warning names the side and the #29
	// suspect, NOT the code mapping.
	res := CrossReconcileResult{Rows: []CrossRow{
		{Canonical: "A", Class: string(CrossBoth), ElecRows: 1, ElectricityCents: 110_000, TowerRows: 4, TowerCents: 0},
		{Canonical: "B", Class: string(CrossOnlyTower), TowerRows: 1, TowerCents: 0},
	}}
	w := crossSideHealthWarnings(res)
	if len(w) != 1 {
		t.Fatalf("warnings = %v, want exactly the tower-side zero warning", w)
	}
	if !strings.Contains(w[0], "塔租侧有记录的 2 站金额全部为 0") || !strings.Contains(w[0], "金额列") {
		t.Errorf("tower zero warning = %q", w[0])
	}

	// shape 2: electricity side entirely absent — the premise warning (the
	// tower side itself is healthy: 1 present site, 1 non-zero → silent).
	res2 := CrossReconcileResult{Rows: []CrossRow{
		{Canonical: "B", Class: string(CrossOnlyTower), TowerRows: 1, TowerCents: 5_000},
	}}
	w2 := crossSideHealthWarnings(res2)
	if len(w2) != 1 {
		t.Fatalf("warnings = %v, want the single absent-side premise notice", w2)
	}
	if !strings.Contains(w2[0], "电费侧该账期无任何记录") {
		t.Errorf("absent-side warning = %q", w2[0])
	}

	// shape 3: healthy two-sided table — silence.
	res3 := CrossReconcileResult{Rows: []CrossRow{
		{Canonical: "A", Class: string(CrossBoth), ElecRows: 1, ElectricityCents: 10_000, TowerRows: 1, TowerCents: 8_000},
	}}
	if w3 := crossSideHealthWarnings(res3); len(w3) != 0 {
		t.Errorf("healthy table must not warn, got %v", w3)
	}

	// shape 4: a zero-amount MINORITY stays envelope-silent (the per-row
	// zeroSide marker is that row's own evidence; a side-level warning
	// would flood once a single site legitimately nets zero).
	res4 := CrossReconcileResult{Rows: []CrossRow{
		{Canonical: "A", Class: string(CrossBoth), ElecRows: 1, ElectricityCents: 10_000, TowerRows: 1, TowerCents: 9_000},
		{Canonical: "Z", Class: string(CrossBoth), ElecRows: 1, ElectricityCents: 5_000, TowerRows: 1, TowerCents: 0},
	}}
	if w4 := crossSideHealthWarnings(res4); len(w4) != 0 {
		t.Errorf("a zero-amount minority must not flood the envelope, got %v", w4)
	}
}
