//go:build duckdb

package fees

// crossattribution_duckdb_test.go — the #38 acceptance on the REAL
// engine + REAL link channel: the full vertical of the attribution —
// resources carrying 站址性质/供电方式/生命周期 (imported through the mapping
// channel or seeded with the structured columns), the graded link scan
// + the human confirm, and the cross-reconcile answering the attributed
// three-way table with every branch of the closed token set live.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/repo"
)

func TestDuckCrossReconcileAttributionEndToEnd(t *testing.T) {
	svc, db, dir := newService(t)
	ctx := context.Background()
	e := newQueryEngine()

	// The fee universes: five shapes, five attributions.
	elecBody := "站址编码,账期,电费(元)\n" +
		"BOTH-1,2026-08,10000.00\n" +
		"ELEC-ONLY,2026-08,5000.00\n" + // self_built resource → 自建无塔租（正常）
		"RET-ONLY,2026-08,7000.00\n" + // retired resource → 已退网仍计费（风险）
		"HLJ-0002,2026-08,2000.00\n" + // twin of tower's hlj0002 → proposal pair
		"NA-ONLY,2026-08,100.00\n" // no master data, no proposal → unknown
	elecPath := filepath.Join(dir, "a-elec.csv")
	if err := os.WriteFile(elecPath, []byte(elecBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Import(ctx, ImportOptions{Path: elecPath, FeeType: FeeTypeElectricity, Year: 2026, Month: 8}); err != nil {
		t.Fatalf("elec import: %v", err)
	}
	towerBody := "站址编码,账期,费用(元)\n" +
		"BOTH-1,2026-08,8000.00\n" +
		"TOWER-ONLY,2026-08,30000.00\n" + // shared+none resource → 共享无供电（正常）
		"hlj0002,2026-08,2500.00\n" + // the unpadded twin → code_mismatch via proposal
		"T-NA,2026-08,400.00\n" // no link, no proposal → unknown
	towerPath := filepath.Join(dir, "a-tower.csv")
	if err := os.WriteFile(towerPath, []byte(towerBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Import(ctx, ImportOptions{Path: towerPath, FeeType: FeeTypeTowerLease, Year: 2026, Month: 8}); err != nil {
		t.Fatalf("tower import: %v", err)
	}

	// Resources with the structured master data (the #38 columns), linked
	// to the fee codes by the graded scan + the human confirm channel.
	seedMasterResource(t, db, "res_at1", "TOWER-ONLY", "共享无供电站", "shared", "none", "active")
	seedMasterResource(t, db, "res_at2", "ELEC-ONLY", "自建无塔租站", "self_built", "direct", "active")
	seedMasterResource(t, db, "res_at3", "RET-ONLY", "已退网站", "shared", "none", "retired")
	if _, err := svc.ScanLinks(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}
	// The scan AUTO-CONFIRMS its exact-grade finds (decideCode: exact and
	// rule land LinkStatusConfirmed/source=auto — only fuzzy proposals stay
	// pending for the human channel), so the three seeded resources' links
	// are analysis-visible straight after the scan; assert them directly.
	for _, code := range []string{"TOWER-ONLY", "ELEC-ONLY", "RET-ONLY"} {
		if _, err := db.FeeSiteLinks.ConfirmedByCode(ctx, code); err != nil {
			t.Fatalf("confirmed link for %s (exact scan find): %v", code, err)
		}
	}

	res, err := svc.CrossReconcile(ctx, e, 2026, 8)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got := map[string]string{}
	for _, r := range res.Rows {
		got[r.Canonical] = r.Attribution
	}
	want := map[string]string{
		"TOWER-ONLY": AttributionSharedNoPowerNeeded,
		"ELEC-ONLY":  AttributionSelfBuiltNoTower,
		"RET-ONLY":   AttributionRetiredButBilled,
		"hlj0002":    AttributionCodeMismatch,
		"NA-ONLY":    AttributionUnknown,
		"T-NA":       AttributionUnknown,
	}
	for code, w := range want {
		if got[code] != w {
			t.Errorf("row %s attribution = %q, want %q", code, got[code], w)
		}
	}
	if got["BOTH-1"] != "" {
		t.Errorf("both row carried attribution %q — the both class has nothing to explain", got["BOTH-1"])
	}
	if got["HLJ-0002"] != AttributionCodeMismatch {
		t.Errorf("the electricity twin of the proposal pair = %q, want code_mismatch too", got["HLJ-0002"])
	}

	// The unknown face warns with its count (never a silent all-clear).
	foundUnknownWarning := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "2 行差异 attribution=unknown") {
			foundUnknownWarning = true
		}
	}
	if !foundUnknownWarning {
		t.Fatalf("the 2 unknown rows must warn with the count, warnings = %v", res.Warnings)
	}
	// The note explains the two readings (已归因 vs 无法判断).
	if !strings.Contains(res.Note, "归因口径（#38）") || !strings.Contains(res.Note, "无法判断差异性质") {
		t.Fatalf("the note lost the attribution vocabulary: %q", res.Note)
	}
}

// seedMasterResource inserts one base_station resource carrying the #38
// structured master-data columns (the link seed pattern of link_test.go
// with the new columns filled).
func seedMasterResource(t *testing.T, db *repo.DB, id, key, name, nature, power, lifecycle string) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.Batches.Get(ctx, "bat_link"); err != nil {
		err := db.Do(ctx, func(tx *repo.Tx) error {
			return tx.Batches.Create(ctx, domain.ImportBatch{
				ID: "bat_link", WorkbookName: "b.xlsx", WorkbookSHA256: "linkseed",
				Status: domain.BatchStatusCompleted, Mapping: "{}", RuleVersion: domain.RuleVersion,
			})
		})
		if err != nil {
			t.Fatalf("seed batch: %v", err)
		}
	}
	err := db.Do(ctx, func(tx *repo.Tx) error {
		return tx.Resources.Insert(ctx, domain.Resource{
			ID: id, Type: domain.ResourceTypeBaseStation, NaturalKey: key, Name: name,
			Attributes:   map[string]string{},
			SiteNature:   nature,
			PowerMode:    power,
			Lifecycle:    lifecycle,
			FirstBatchID: "bat_link",
			CreatedAt:    simTSLink, UpdatedAt: simTSLink,
		})
	})
	if err != nil {
		t.Fatalf("seed resource %s: %v", key, err)
	}
}
