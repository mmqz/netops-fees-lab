package fees

// crossattribution_test.go — the #38 acceptance 5 table: the attribution
// decision core pinned branch by branch (the five tokens of the issue plus
// the self_built mirror), the code collection (canonical + alias trail),
// and the redirect-trail master lookup. The e2e verticals live in
// crossattribution_duckdb_test.go (real engine + real links).

import (
	"reflect"
	"testing"
)

func onlyTowerRow(canonical string) CrossRow {
	return CrossRow{Canonical: canonical, Class: "only_tower"}
}

func onlyElecRow(canonical string) CrossRow {
	return CrossRow{Canonical: canonical, Class: "only_electricity"}
}

func master(nature, power, lifecycle string) siteMasterAnswer {
	return siteMasterAnswer{ok: nature != "" || power != "" || lifecycle != "",
		nature: nature, power: power, lifecycle: lifecycle}
}

func TestAttributeOnlyTowerFiveBranches(t *testing.T) {
	cases := []struct {
		name string
		m    siteMasterAnswer
		prop bool
		want string
	}{
		{"retired site still billed", master("", "", "retired"), false, AttributionRetiredButBilled},
		{"dismantled site still billed", master("", "", "dismantled"), false, AttributionRetiredButBilled},
		{"shared with no power supply", master("shared", "none", ""), false, AttributionSharedNoPowerNeeded},
		{"direct power should have a bill", master("", "direct", "active"), false, AttributionPowerShouldExist},
		{"transfer power should have a bill", master("leased", "transfer", ""), false, AttributionPowerShouldExist},
		{"alias proposal covers the code", siteMasterAnswer{}, true, AttributionCodeMismatch},
		{"no master data at all", siteMasterAnswer{}, false, AttributionUnknown},
		{"master data that answers nothing (self_built+direct power on a tower-only row? direct wins first)",
			master("self_built", "direct", ""), false, AttributionPowerShouldExist},
	}
	for _, tc := range cases {
		if got := attributeOnlyTower(tc.m, tc.prop); got != tc.want {
			t.Errorf("%s: attributeOnlyTower = %q, want %q", tc.name, got, tc.want)
		}
	}
	// The retired answer outranks a co-existing proposal (hard master data
	// beats the heuristic hint).
	if got := attributeOnlyTower(master("", "", "retired"), true); got != AttributionRetiredButBilled {
		t.Errorf("retired + proposal = %q, want retired_but_billed (hard data outranks hints)", got)
	}
	// A shared site WITH a direct supply is not the no-power reading.
	if got := attributeOnlyTower(master("shared", "direct", ""), false); got != AttributionPowerShouldExist {
		t.Errorf("shared+direct = %q, want power_should_exist (供电方式 beats 站址性质)", got)
	}
	// Incomplete master data (nature known, power unknown) never guesses
	// the shared_no_power reading.
	if got := attributeOnlyTower(master("shared", "", ""), false); got != AttributionUnknown {
		t.Errorf("shared with power unknown = %q, want unknown (never a silent normal)", got)
	}
}

func TestAttributeOnlyElectricityBranches(t *testing.T) {
	cases := []struct {
		name string
		m    siteMasterAnswer
		prop bool
		want string
	}{
		{"retired site still consuming", master("", "", "retired"), false, AttributionRetiredButBilled},
		{"self_built explains the missing tower rent", master("self_built", "", "active"), false, AttributionSelfBuiltNoTower},
		{"alias proposal covers the code", siteMasterAnswer{}, true, AttributionCodeMismatch},
		{"no master data", siteMasterAnswer{}, false, AttributionUnknown},
		{"shared/leased without tower rent stays unknown (塔租可能在别处结算)", master("shared", "direct", "active"), false, AttributionUnknown},
	}
	for _, tc := range cases {
		if got := attributeOnlyElectricity(tc.m, tc.prop); got != tc.want {
			t.Errorf("%s: attributeOnlyElectricity = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestAttributeCrossRowsEnvelope(t *testing.T) {
	rows := []CrossRow{
		{Canonical: "A", Class: "both"}, // no attribution (nothing to explain)
		onlyTowerRow("T-SHARED"), onlyTowerRow("T-NA"),
		onlyElecRow("E-RETIRED"), onlyElecRow("E-NA"),
	}
	masterMap := map[string]siteMasterAnswer{
		"T-SHARED":  master("shared", "none", "active"),
		"E-RETIRED": master("self_built", "direct", "retired"),
	}
	unknown := attributeCrossRows(rows, masterMap, nil)
	want := []string{
		"", AttributionSharedNoPowerNeeded, AttributionUnknown,
		AttributionRetiredButBilled, AttributionUnknown,
	}
	for i, w := range want {
		if rows[i].Attribution != w {
			t.Errorf("row %d (%s): attribution = %q, want %q", i, rows[i].Canonical, rows[i].Attribution, w)
		}
	}
	if unknown != 2 {
		t.Fatalf("unknown count = %d, want 2", unknown)
	}
}

func TestCollectAttributionCodesAndTrailLookup(t *testing.T) {
	rows := []CrossRow{
		{Canonical: "BOTH", Class: "both"},
		{Canonical: "E1", Class: "only_electricity", RedirectedFrom: "T9→E1"},
		{Canonical: "T2", Class: "only_tower"},
	}
	got := collectAttributionCodes(rows)
	if want := []string{"E1", "T9", "T2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("collectAttributionCodes = %v, want %v (both rows excluded, trail originals in)", got, want)
	}
	// The canonical has no link; the trail's original does.
	m := map[string]siteMasterAnswer{
		"T9": master("shared", "none", ""),
	}
	if a := masterAnswerFor(m, rows[1]); !a.ok || a.nature != "shared" {
		t.Fatalf("trail lookup = %+v, want T9's answer", a)
	}
	if a := masterAnswerFor(m, rows[2]); a.ok {
		t.Fatalf("unlinked row answered ok — want the empty (unknown) state")
	}
	// Proposal coverage through the trail too.
	if !proposalCovers(map[string]bool{"T9": true}, rows[1]) {
		t.Fatalf("proposal coverage must read the trail originals")
	}
}

// TestAttributionNeverWhitewashes pins the issue's own 洗白 warning: a
// reconcile whose EVERY only_* row is unknown must answer the count, not
// an implicit all-clear — the envelope's warnings carry it.
func TestAttributionNeverWhitewashes(t *testing.T) {
	rows := []CrossRow{onlyTowerRow("X1"), onlyTowerRow("X2"), onlyElecRow("Y1")}
	if unknown := attributeCrossRows(rows, nil, nil); unknown != 3 {
		t.Fatalf("unknown = %d, want 3", unknown)
	}
	for _, r := range rows {
		if r.Attribution != AttributionUnknown {
			t.Fatalf("row %s attribution = %q, want unknown — never a silent normal", r.Canonical, r.Attribution)
		}
	}
}
