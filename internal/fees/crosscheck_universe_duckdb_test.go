//go:build duckdb

package fees

// crosscheck_universe_duckdb_test.go — issue #31's acceptance on the real
// engine: the universe primitive must keep the zero-amount tail. The
// production shape: a 500+ site tower sheet whose amounts ALL landed 0
// (the #29 wrong-column import); the old display-Totals input ranked
// total DESC and capped at 500, parking the zero-total sites past the
// cutoff — a site with 4 tower rows and 0 cents then classified
// only_electricity (presence read as absence) and the delta column
// degenerated to the electricity re-pricing with no envelope notice.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDuckCrossReconcileZeroAmountUniverse imports a 520-site all-zero
// tower month plus 2 electricity sites and proves the three #31 fixes
// together: every zero-total tower site stays in the merge (both /
// only_tower, never only_electricity-by-truncation), the both-rows rows
// carry zeroSide=tower, and the envelope warns that the tower side's
// money face is dead.
func TestDuckCrossReconcileZeroAmountUniverse(t *testing.T) {
	svc, _, dir := newService(t)
	ctx := context.Background()
	e := newQueryEngine()

	// tower universe: 520 sites, every amount 0 — the #29 import shape
	// (the import itself warns all-zero; that warning is tower_amount
	// territory, asserted there — here the point is the RECONCILE's
	// behavior on the degraded data).
	var b strings.Builder
	b.WriteString("站址编码,账期,费用(元)\n")
	for i := 1; i <= 520; i++ {
		fmt.Fprintf(&b, "T-%04d,2026-09,0\n", i)
	}
	towerPath := filepath.Join(dir, "u-tower.csv")
	if err := os.WriteFile(towerPath, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Import(ctx, ImportOptions{Path: towerPath, FeeType: FeeTypeTowerLease, Year: 2026, Month: 9}); err != nil {
		t.Fatalf("tower import: %v", err)
	}
	// electricity universe: 2 healthy sites that ALSO exist in the tower
	// universe (T-0001 / T-0002) plus one electricity-only site.
	elecBody := "站址编码,账期,电费(元)\nT-0001,2026-09,1105.63\nT-0002,2026-09,2200.00\nE-ONLY,2026-09,300.00\n"
	elecPath := filepath.Join(dir, "u-elec.csv")
	if err := os.WriteFile(elecPath, []byte(elecBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Import(ctx, ImportOptions{Path: elecPath, FeeType: FeeTypeElectricity, Year: 2026, Month: 9}); err != nil {
		t.Fatalf("elec import: %v", err)
	}

	res, err := svc.CrossReconcile(ctx, e, 2026, 9)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	// the universe: 520 tower sites + 1 electricity-only site = 521 lines.
	if len(res.Rows) != 521 {
		t.Fatalf("rows = %d, want 521 (the zero-amount tail must survive)", len(res.Rows))
	}
	// the shape that used to classify only_electricity by truncation: rows
	// on both sides, tower side at 0 cents.
	if res.Both != 2 {
		t.Fatalf("both = %d, want 2 (T-0001/T-0002 present on both sides)", res.Both)
	}
	if res.OnlyElectricity != 1 {
		t.Fatalf("onlyElectricity = %d, want 1 (E-ONLY alone — zero-tower sites must NOT read as absence)", res.OnlyElectricity)
	}
	if res.OnlyTower != 518 {
		t.Fatalf("onlyTower = %d, want 518", res.OnlyTower)
	}
	for _, r := range res.Rows {
		if r.Canonical == "T-0001" || r.Canonical == "T-0002" {
			if r.Class != string(CrossBoth) || r.ZeroSide != "tower" || r.TowerRows != 1 {
				t.Fatalf("%s = %+v, want both + zeroSide tower + tower rows present", r.Canonical, r)
			}
		}
	}
	// the envelope health notice: the tower side's money face is dead.
	var towerWarn bool
	for _, w := range res.Warnings {
		if strings.Contains(w, "塔租侧有记录的 520 站金额全部为 0") && strings.Contains(w, "不具参考价值") {
			towerWarn = true
		}
	}
	if !towerWarn {
		t.Errorf("warnings = %v, want the tower-side all-zero delta warning", res.Warnings)
	}
}

// TestDuckCrossUniverseTotalsNoLimit is the primitive's own acceptance:
// 520 sites all answer — no top-N cutoff, site_code ascending — while the
// display Totals keeps its cap (the two primitives carry different
// contracts on purpose).
func TestDuckCrossUniverseTotalsNoLimit(t *testing.T) {
	svc, _, dir := newService(t)
	ctx := context.Background()
	e := newQueryEngine()
	var b strings.Builder
	b.WriteString("站址编码,账期,费用(元)\n")
	for i := 1; i <= 520; i++ {
		fmt.Fprintf(&b, "T-%04d,2026-09,0\n", i)
	}
	p := filepath.Join(dir, "u2-tower.csv")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Import(ctx, ImportOptions{Path: p, FeeType: FeeTypeTowerLease, Year: 2026, Month: 9}); err != nil {
		t.Fatalf("import: %v", err)
	}
	rows, err := e.CrossUniverseTotals(ctx, svc.analysisDir(), FeeTypeTowerLease, 2026, 9)
	if err != nil {
		t.Fatalf("universe totals: %v", err)
	}
	if len(rows) != 520 {
		t.Fatalf("universe rows = %d, want 520 (no cutoff)", len(rows))
	}
	for i := 1; i < len(rows); i++ {
		if rows[i-1].SiteCode > rows[i].SiteCode {
			t.Fatalf("universe must answer site_code ascending, got %s before %s", rows[i-1].SiteCode, rows[i].SiteCode)
		}
	}
	// the display primitive keeps its cap — different contract, same data.
	disp, err := e.Totals(ctx, svc.analysisDir(), FeeTypeTowerLease, 2026, 9, 500)
	if err != nil {
		t.Fatalf("display totals: %v", err)
	}
	if len(disp) != 500 {
		t.Fatalf("display totals = %d rows, want its 500 cap (the old reconcile input)", len(disp))
	}
}
