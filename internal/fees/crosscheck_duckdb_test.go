//go:build duckdb

package fees

// crosscheck_duckdb_test.go — the #12 acceptance on the REAL engine: the
// scan proposes the same-skeleton dual-system pair, the human channel
// creates the alias, and the cross-reconcile merges the two universes
// onto the canonical code with the three-way classification.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDuckCrossReconcileEndToEnd(t *testing.T) {
	svc, _, dir := newService(t)
	ctx := context.Background()
	e := newQueryEngine()

	// electricity universe: BOTH-1 + ELEC-ONLY + the padded HLJ-0001.
	elecBody := "站址编码,账期,电费(元)\nBOTH-1,2026-08,10000.00\nELEC-ONLY,2026-08,5000.00\nHLJ-0001,2026-08,2000.00\n"
	elecPath := filepath.Join(dir, "x-elec.csv")
	if err := os.WriteFile(elecPath, []byte(elecBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Import(ctx, ImportOptions{Path: elecPath, FeeType: FeeTypeElectricity, Year: 2026, Month: 8}); err != nil {
		t.Fatalf("elec import: %v", err)
	}
	// tower universe: BOTH-1 + TOWER-ONLY + the unpadded hlj001 sibling
	// of HLJ-0001 (the 实测 dual-code shape).
	towerBody := "站址编码,账期,费用(元)\nBOTH-1,2026-08,8000.00\nTOWER-ONLY,2026-08,30000.00\nhlj001,2026-08,2500.00\n"
	towerPath := filepath.Join(dir, "x-tower.csv")
	if err := os.WriteFile(towerPath, []byte(towerBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Import(ctx, ImportOptions{Path: towerPath, FeeType: FeeTypeTowerLease, Year: 2026, Month: 8}); err != nil {
		t.Fatalf("tower import: %v", err)
	}

	// the scan proposes exactly the padded/unpadded pair.
	proposals, err := svc.ProposeSiteCodeAliases(ctx, e)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	found := false
	for _, p := range proposals {
		if p.Canonical == "HLJ-0001" && p.Alias == "hlj001" && p.Source == "tower_lease" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the dual-code pair must be proposed: %+v", proposals)
	}

	// the pre-alias reconcile: three classes, HLJ-0001 and hlj001 apart
	// (both only_electricity + only_tower instead of one merged both).
	pre, err := svc.CrossReconcile(ctx, e, 2026, 8)
	if err != nil {
		t.Fatalf("pre reconcile: %v", err)
	}
	if pre.OnlyElectricity != 2 || pre.OnlyTower != 2 || pre.Both != 1 {
		t.Fatalf("pre-alias counts = %+v", pre)
	}

	// the human confirm channel.
	if _, err := svc.AddSiteCodeAlias(ctx, SiteCodeAliasInput{
		Canonical: "HLJ-0001", Alias: "hlj001", Source: "tower_lease", Confidence: "rule",
	}, "duck-test"); err != nil {
		t.Fatalf("alias add: %v", err)
	}

	// the post-alias reconcile: the pair merges onto HLJ-0001 (both
	// class), counts 1/1/2, and the trail rides the line.
	post, err := svc.CrossReconcile(ctx, e, 2026, 8)
	if err != nil {
		t.Fatalf("post reconcile: %v", err)
	}
	if post.Both != 2 || post.OnlyElectricity != 1 || post.OnlyTower != 1 || post.AliasesApplied != 1 {
		t.Fatalf("post-alias counts = %+v", post)
	}
	var merged *CrossRow
	for i := range post.Rows {
		if post.Rows[i].Canonical == "HLJ-0001" {
			merged = &post.Rows[i]
		}
	}
	if merged == nil {
		t.Fatal("the merged HLJ-0001 line is missing")
	}
	if merged.Class != string(CrossBoth) || merged.ElectricityCents != 200_000 ||
		merged.TowerCents != 250_000 || merged.DeltaCents != -50_000 {
		t.Fatalf("merged line = %+v", *merged)
	}
	if merged.RedirectedFrom == "" {
		t.Fatal("the alias trail must ride the merged line")
	}
}
