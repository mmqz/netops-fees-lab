//go:build duckdb

package fees

// invariants_duckdb_test.go — the #33 property face, part 2: the invariants
// that need the REAL analysis engine (the money conservation and the risk
// monotonicity), plus the honest scope ruling for the statistical
// scenarios.
//
//   I2  amount conservation — imported money is conserved across every
//       face: the per-site totals, the monthly series and the whole-year
//       aggregate all reproduce the fixture sum exactly, under growth
//       (more imports), under removal (rollback) and under the idempotent
//       re-import that must add nothing.
//   I5  clue monotonicity — with a STATIC resource universe and a growing
//       fee face, every structural scenario's clue set only GROWS: old
//       clues never disappear when data is added. Scope ruling (the
//       honest boundary): spike/outlier are POPULATION-DEPENDENT scenarios
//       (their thresholds are computed from the peer set) — adding data
//       can legitimately retire an outlier clue, so monotonicity is NOT
//       a true invariant there and the property deliberately excludes
//       them instead of asserting a falsehood.

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// engineFor hands the invariant tests the real engine + the analysis dir
// rooted at the service's data dir (the CLI's own wiring).
func engineFor(t *testing.T, dir string) (QueryEngine, string) {
	t.Helper()
	ctx := context.Background()
	engine := Query(ctx)
	if !engine.Available() {
		t.Fatal("the duckdb build must report Available")
	}
	return engine, filepath.Join(dir, "analysis")
}

// sumMonthlyTotalCents folds a monthly series to the year total.
func sumMonthlyTotalCents(rows []MonthlyTotalsRow) int64 {
	var sum int64
	for _, r := range rows {
		sum += r.TotalCents
	}
	return sum
}

// siteTotalsTotal sums the per-site totals answer to one number.
func siteTotalsTotal(rows []TotalsRow) (int64, int) {
	var sum int64
	var rowSum int
	for _, r := range rows {
		sum += r.TotalCents
		rowSum += r.Rows
	}
	return sum, rowSum
}

// TestAmountConservationFaces pins I2 on the real engine: money imported
// through the real pipeline is conserved across the query faces — the
// per-site totals, the monthly series and the year aggregate all
// reproduce the exact fixture cents, and they keep reproducing it under
// growth, under rollback and under the idempotent re-import.
func TestAmountConservationFaces(t *testing.T) {
	svc, _, dir := newService(t)
	ctx := context.Background()
	engine, analysis := engineFor(t, dir)

	// T05 = 100.00 + 250.00 = 35000 cents across two sites.
	sumA := importFixture(t, svc, dir, "may-a.csv", "站址编码,账期,电费(元)\nS-A,2024-05,100.00\n", 2024, 5)
	importFixture(t, svc, dir, "may-b.csv", "站址编码,账期,电费(元)\nS-B,2024-05,250.00\n", 2024, 5)
	const T05 = 35000

	sites, err := engine.Totals(ctx, analysis, FeeTypeElectricity, 2024, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if got, rows := siteTotalsTotal(sites); got != T05 || rows != 2 {
		t.Fatalf("site totals after May: %d cents / %d rows, want %d / 2", got, rows, T05)
	}

	// growth: June adds S-C 90.00 → the May face is UNTOUCHED and the year
	// grows by exactly the new money.
	sumC := importFixture(t, svc, dir, "jun.csv", "站址编码,账期,电费(元)\nS-C,2024-06,90.00\n", 2024, 6)
	const T06 = 9000
	monthly, err := engine.MonthlyTotals(ctx, analysis, FeeTypeElectricity, 2024)
	if err != nil {
		t.Fatal(err)
	}
	if len(monthly) != 2 || monthly[0].Month != 5 || monthly[0].TotalCents != T05 ||
		monthly[1].Month != 6 || monthly[1].TotalCents != T06 {
		t.Fatalf("monthly series after growth = %+v", monthly)
	}
	if got := sumMonthlyTotalCents(monthly); got != T05+T06 {
		t.Fatalf("year total after growth = %d, want %d", got, T05+T06)
	}

	// removal: rolling June back subtracts exactly its own money — the
	// May face is untouched, June honestly disappears (absence, never a
	// zero month — the #3 doctrine: no data month never appears).
	if _, err := svc.Rollback(ctx, sumC.BatchID); err != nil {
		t.Fatalf("rollback June: %v", err)
	}
	monthly, err = engine.MonthlyTotals(ctx, analysis, FeeTypeElectricity, 2024)
	if err != nil {
		t.Fatal(err)
	}
	if len(monthly) != 1 || monthly[0].Month != 5 || monthly[0].TotalCents != T05 {
		t.Fatalf("monthly series after rollback = %+v, want May=%d only", monthly, T05)
	}

	// idempotent re-import: the duplicate answer adds NOTHING — the year
	// total after re-importing the May file equals the pre-reimport
	// total (the sentinel answer is informational, zero new data).
	if _, err := importFixtureErr(svc, dir, "may-a.csv", "站址编码,账期,电费(元)\nS-A,2024-05,100.00\n", 2024, 5); err == nil {
		t.Fatal("re-import of a completed file must be the duplicate sentinel")
	}
	monthly, err = engine.MonthlyTotals(ctx, analysis, FeeTypeElectricity, 2024)
	if err != nil {
		t.Fatal(err)
	}
	if got := sumMonthlyTotalCents(monthly); got != T05 {
		t.Fatalf("year total after idempotent re-import = %d, want %d (re-import added money!)", got, T05)
	}
	_ = sumA
}

// clueKey identifies one clue across scenario runs (the monotonicity face
// compares SETS, not counts — a clue that changed its metric but kept its
// identity is still the same clue for this property).
func clueKey(c RiskClue) string {
	return c.SiteCode + "|" + c.RecordKey + "|" + strconv.Itoa(c.Year) + "|" + strconv.Itoa(c.Month)
}

// clueKeysOf runs one scenario and collects its clue identity set.
func clueKeysOf(t *testing.T, svc *Service, engine QueryEngine, sc RiskScenario) map[string]bool {
	t.Helper()
	res, err := svc.Risks(context.Background(), engine, sc, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024})
	if err != nil {
		t.Fatalf("scenario %s: %v", sc, err)
	}
	keys := make(map[string]bool, res.Total)
	for _, c := range res.Clues {
		if keys[clueKey(c)] {
			t.Fatalf("scenario %s: duplicate clue identity %s", sc, clueKey(c))
		}
		keys[clueKey(c)] = true
	}
	return keys
}

// TestRiskCluesMonotonicUnderFeeGrowth pins I5: with a static resource
// universe and a growing fee face, each structural scenario's clue set is
// a growing chain — layer k's clues are a SUBSET of layer k+1's. The
// statistical scenarios (spike, outlier) are excluded by the documented
// scope ruling: their thresholds are functions of the peer population, so
// adding peers can legitimately retire a clue there.
func TestRiskCluesMonotonicUnderFeeGrowth(t *testing.T) {
	svc, db, dir := newService(t)
	ctx := context.Background()
	engine, _ := engineFor(t, dir)

	// static resource universe: one retired site (link confirmed), the
	// retired set the retired-billing scenario reads.
	seedLinkResource(t, db, "res_retired", "RETIRED-1", "停用")
	_ = ctx

	structural := []RiskScenario{RiskRetiredBilling, RiskUnlinked, RiskDuplicateSettlement, RiskLedgerMismatch}

	// layer 0: one retired-billing clue + one unlinked clue.
	importFixture(t, svc, dir, "l0-retired.csv", "站址编码,账期,电费(元)\nRETIRED-1,2024-05,100.00\n", 2024, 5)
	importFixture(t, svc, dir, "l0-wild.csv", "站址编码,账期,电费(元)\nS-WILD-0,2024-05,80.00\n", 2024, 5)
	prev := map[RiskScenario]map[string]bool{}
	for _, sc := range structural {
		prev[sc] = clueKeysOf(t, svc, engine, sc)
	}
	if len(prev[RiskRetiredBilling]) != 1 || len(prev[RiskUnlinked]) != 1 {
		t.Fatalf("layer 0 baseline: retired=%v unlinked=%v", prev[RiskRetiredBilling], prev[RiskUnlinked])
	}

	// layer 1: MORE of the same shapes — new periods for both codes, a
	// fresh wild site, and a duplicate pair (which also unlinks).
	importFixture(t, svc, dir, "l1-retired.csv", "站址编码,账期,电费(元)\nRETIRED-1,2024-06,110.00\n", 2024, 6)
	importFixture(t, svc, dir, "l1-wild.csv", "站址编码,账期,电费(元)\nS-WILD-1,2024-06,70.00\nS-WILD-0,2024-06,81.00\n", 2024, 6)
	importFixture(t, svc, dir, "l1-dup-a.csv", "站址编码,账期,电费(元),备注\nS-DUP,2024-05,100.00,台账A\n", 2024, 5)
	importFixture(t, svc, dir, "l1-dup-b.csv", "站址编码,账期,电费(元),备注\nS-DUP,2024-05,100.00,台账B\n", 2024, 5)

	// layer 2: a mismatch pair (which is also a duplicate) + another
	// retired period.
	importFixture(t, svc, dir, "l2-mis-a.csv", "站址编码,账期,电费(元)\nS-LEDG,2024-06,200.00\n", 2024, 6)
	importFixture(t, svc, dir, "l2-mis-b.csv", "站址编码,账期,电费(元)\nS-LEDG,2024-06,250.00\n", 2024, 6)
	importFixture(t, svc, dir, "l2-retired.csv", "站址编码,账期,电费(元)\nRETIRED-1,2024-07,120.00\n", 2024, 7)

	// the monotonic chain: every layer-k clue survives into layer k+1.
	cur := map[RiskScenario]map[string]bool{}
	for _, sc := range structural {
		cur[sc] = clueKeysOf(t, svc, engine, sc)
		for k, ok := range prev[sc] {
			if ok && !cur[sc][k] {
				t.Fatalf("scenario %s: clue %q vanished when the fee face only GREW — monotonicity broken", sc, k)
			}
		}
	}
	// growth is real: the total structural clue population grew.
	total := 0
	for _, sc := range structural {
		total += len(cur[sc])
	}
	if total < 8 {
		t.Fatalf("expected the grown library to carry ≥8 structural clues, got %d", total)
	}
}

// TestStatisticalScenariosExcludedFromMonotonicity documents the scope
// ruling with the counterexample that justifies it: adding peer data
// legitimately retires an outlier clue — monotonicity asserted over
// spike/outlier would be a FALSE property, so the #33 face excludes them
// (asserted here by demonstrating the retirement, so the exclusion is
// evidence-backed, not hand-waved).
func TestStatisticalScenariosExcludedFromMonotonicity(t *testing.T) {
	svc, _, dir := newService(t)
	ctx := context.Background()
	engine, _ := engineFor(t, dir)

	// one 1000.00 outlier against 12 peers of 100.00: z ≈ 4.3 → the
	// clue fires (the same shape the risk fixture pins).
	var peers strings.Builder
	peers.WriteString("站址编码,账期,电费(元)\n")
	for i := 1; i <= 12; i++ {
		fmt.Fprintf(&peers, "S-O%02d,2024-05,100.00\n", i)
	}
	importFixture(t, svc, dir, "peers.csv", peers.String(), 2024, 5)
	importFixture(t, svc, dir, "out.csv", "站址编码,账期,电费(元)\nS-OUT,2024-05,1000.00\n", 2024, 5)

	before, err := svc.Risks(ctx, engine, RiskOutlier, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
	if err != nil {
		t.Fatal(err)
	}
	if before.Total != 1 || before.Clues[0].SiteCode != "S-OUT" {
		t.Fatalf("outlier baseline = %+v, want the S-OUT clue", before.Clues)
	}

	// the counterexample: the fee face only GREW (three more 1000.00
	// peers), and the S-OUT clue legitimately retires — the population
	// moved, its thresholds moved with it. Monotonicity would fail HERE,
	// which is exactly why the property's scope excludes the statistical
	// scenarios.
	importFixture(t, svc, dir, "more.csv",
		"站址编码,账期,电费(元)\nS-X1,2024-05,1000.00\nS-X2,2024-05,1000.00\nS-X3,2024-05,1000.00\n", 2024, 5)
	after, err := svc.Risks(ctx, engine, RiskOutlier, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
	if err != nil {
		t.Fatal(err)
	}
	if after.Total != 0 {
		t.Fatalf("the retirement counterexample did not retire: %+v — pick peer sizes that actually move the z-score", after.Clues)
	}
}
