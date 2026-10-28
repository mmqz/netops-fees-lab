package fees

// crossmerge_more_test.go — the #33 I4 supplement: the pure-merge faces
// the generated property does not spell out — alias redirection
// (merging may redirect codes, never money), the anomaly-first display
// order (#3's doctrine as an identity), and the empty-inputs contract.

import (
	"strings"
	"testing"
)

// TestCrossMergeAliasRedirectPreservesMoney pins the alias face: a code
// under an alias map merges into its canonical row, the redirect trail is
// recorded, and the money conservation of the whole table is untouched —
// redirecting a code moves its cents, it never duplicates or loses them.
func TestCrossMergeAliasRedirectPreservesMoney(t *testing.T) {
	elec := []TotalsRow{{SiteCode: "S-RAW", Rows: 1, TotalCents: 10000}}
	tower := []TotalsRow{{SiteCode: "S-CANON", Rows: 2, TotalCents: 5000}}
	aliases := map[string]string{"S-RAW": "S-CANON"}

	res := crossMerge(2024, 5, elec, tower, aliases, map[string]string{})
	if len(res.Rows) != 1 {
		t.Fatalf("alias must merge the two sides into one row, got %d", len(res.Rows))
	}
	row := res.Rows[0]
	if row.Canonical != "S-CANON" || row.ElectricityCents != 10000 || row.TowerCents != 5000 {
		t.Fatalf("merged row = %+v", row)
	}
	if row.RedirectedFrom == "" || !strings.Contains(row.RedirectedFrom, "S-RAW") {
		t.Fatalf("redirect trail missing: %q", row.RedirectedFrom)
	}
	// AliasesApplied is the envelope's counter, filled by CrossReconcile
	// (the caller that owns the alias maps) — the pure merge's invariant
	// here is the redirect trail + the money, asserted above.
	if row.ElectricityCents+row.TowerCents != 15000 {
		t.Errorf("money moved in the merge: %+v", row)
	}
}

// TestCrossMergeSortAnomalyFirstOrder pins the display doctrine as an
// identity: only_electricity rows lead (the hunting target), then
// only_tower, then both; ties order by canonical code — deterministic on
// every input.
func TestCrossMergeSortAnomalyFirstOrder(t *testing.T) {
	elec := []TotalsRow{
		{SiteCode: "S-Z", Rows: 1, TotalCents: 1},
		{SiteCode: "S-A", Rows: 1, TotalCents: 1},
	}
	tower := []TotalsRow{
		{SiteCode: "S-B", Rows: 1, TotalCents: 1},
		{SiteCode: "S-Y", Rows: 1, TotalCents: 1},
		{SiteCode: "S-W", Rows: 1, TotalCents: 1},
	}
	res := crossMerge(2024, 5, elec, tower, nil, nil)
	var classes []string
	for _, r := range res.Rows {
		classes = append(classes, r.Class)
	}
	want := []string{
		"only_electricity", "only_electricity",
		"only_tower", "only_tower", "only_tower",
	}
	if len(classes) != len(want) {
		t.Fatalf("row count = %d, want %d (%v)", len(classes), len(want), classes)
	}
	for i := range want {
		if classes[i] != want[i] {
			t.Fatalf("order broken at %d: %v", i, classes)
		}
	}
	if res.Rows[0].Canonical != "S-A" || res.Rows[1].Canonical != "S-Z" {
		t.Errorf("ties must order by canonical: %v %v", res.Rows[0].Canonical, res.Rows[1].Canonical)
	}
}

// TestCrossMergeEmptySidesAnswerEmptyTable pins the empty contract: two
// absent sides answer an EMPTY (non-nil, JSON-serializable) table, never a
// nil slice and never a fabricated row — the honest-absence doctrine.
func TestCrossMergeEmptySidesAnswerEmptyTable(t *testing.T) {
	res := crossMerge(2024, 5, nil, nil, nil, nil)
	if res.Rows == nil {
		t.Fatal("empty inputs must answer an EMPTY table, not a nil slice (JSON contract)")
	}
	if len(res.Rows) != 0 {
		t.Fatalf("empty inputs produced %d rows", len(res.Rows))
	}
	if res.Both+res.OnlyElectricity+res.OnlyTower != 0 {
		t.Error("counters must stay zero on empty inputs")
	}
}
