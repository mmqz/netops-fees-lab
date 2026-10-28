package fees

// verify_dq_test.go — the #33 DQ4 row-count cliff on the pure DB face (it
// runs in BOTH builds: the cliff compares batch rows across adjacent
// periods, never the Parquet content — only the money-shape checks need
// the engine). The default build's dataChecksSkipped envelope still says
// the ENGINE face is not run; DQ4 rides the same DataProblems list because
// it IS a data problem, and the honest envelope keeps working: a std
// operator sees the cliff findings plus the skip flag naming what was not
// checked.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// importCSV writes and imports one electricity CSV, failing loudly (the
// DQ fixtures' shared seeder — untagged so both builds see it).
func importCSV(t *testing.T, svc *Service, dir, name, body string, year, month int) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Import(context.Background(), ImportOptions{
		Path: p, FeeType: FeeTypeElectricity, Year: year, Month: month,
	}); err != nil {
		t.Fatalf("import %s: %v", name, err)
	}
}

// numberedRows renders n numbered data rows for the cliff fixtures (the
// row VOLUME is the point — a cliff needs real counts on both sides).
func numberedRows(sitePrefix string, period string, n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "%s%02d,%s,1.00\n", sitePrefix, i, period)
	}
	return b.String()
}

// TestVerifyDQ4RowCountCliff pins the adjacent-period cliff: importing
// under half the previous period-with-data's rows answers row-count-cliff
// with check DQ4 / severity medium; the healthy sibling stays silent; and
// only the actual cliff fires.
func TestVerifyDQ4RowCountCliff(t *testing.T) {
	svc, _, dir := newService(t)

	// 2026-01: 10 rows (the healthy baseline).
	importCSV(t, svc, dir, "jan.csv", "站址编码,账期,电费(元)\n"+numberedRows("S-J", "2026-01", 10), 2026, 1)
	// 2026-02: 3 rows — under half of 10 → the cliff fires.
	importCSV(t, svc, dir, "feb-thin.csv", "站址编码,账期,电费(元)\n"+numberedRows("S-F", "2026-02", 3), 2026, 2)
	// 2026-03: 8 rows — comfortably over half of 3 → silent.
	importCSV(t, svc, dir, "mar.csv", "站址编码,账期,电费(元)\n"+numberedRows("S-M", "2026-03", 8), 2026, 3)

	rep, err := svc.VerifyBatches(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var cliff *VerifyBatchProblem
	for i := range rep.DataProblems {
		if rep.DataProblems[i].Kind == "row-count-cliff" {
			cliff = &rep.DataProblems[i]
			break
		}
	}
	if cliff == nil {
		t.Fatalf("dataProblems = %+v, want the DQ4 row-count-cliff finding", rep.DataProblems)
	}
	if cliff.Check != "DQ4" || cliff.Severity != "medium" {
		t.Fatalf("DQ4 labeling: %+v", *cliff)
	}
	if !strings.Contains(cliff.Detail, "3 行") || !strings.Contains(cliff.Detail, "10 行") {
		t.Errorf("DQ4 detail = %q (want both periods' row counts)", cliff.Detail)
	}
	// exactly one cliff: Jan→Feb fires, Feb→Mar (3→8) does not.
	cliffs := 0
	for _, d := range rep.DataProblems {
		if d.Kind == "row-count-cliff" {
			cliffs++
		}
	}
	if cliffs != 1 {
		t.Fatalf("cliff findings = %d, want exactly 1 (Jan→Feb only)", cliffs)
	}
}

// TestVerifyDQ4FirstPeriodNeverFires pins the boundary: the first period of
// a fee type has no predecessor and never fires — the cliff is strictly an
// ADJACIENT-periods-with-data comparison (a missing import is the
// missing-billing scenarios' story, not this check's).
func TestVerifyDQ4FirstPeriodNeverFires(t *testing.T) {
	svc, _, dir := newService(t)
	// a single one-row period: no predecessor, no cliff, no noise.
	importCSV(t, svc, dir, "only.csv", "站址编码,账期,电费(元)\nS-X,2026-01,5.00\n", 2026, 1)

	rep, err := svc.VerifyBatches(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.DataProblems) != 0 {
		t.Fatalf("dataProblems = %+v, want clean (the first period has no predecessor)", rep.DataProblems)
	}
}

// TestVerifyDQ4SumsSamePeriodBatches pins the period-grain rule: several
// batches of ONE period are the period's ledger together — the cliff
// compares PERIOD totals, never a single file against the previous month
// (two half-size files of one month are one normal month, not a cliff).
func TestVerifyDQ4SumsSamePeriodBatches(t *testing.T) {
	svc, _, dir := newService(t)

	// 2026-01: one 10-row file.
	importCSV(t, svc, dir, "jan.csv", "站址编码,账期,电费(元)\n"+numberedRows("S-J", "2026-01", 10), 2026, 1)
	// 2026-02: TWO files of 3+3 rows = 6 rows period total — over half of
	// 10, so no cliff even though each file alone is under it.
	importCSV(t, svc, dir, "feb-a.csv", "站址编码,账期,电费(元)\n"+numberedRows("S-F", "2026-02", 3), 2026, 2)
	importCSV(t, svc, dir, "feb-b.csv", "站址编码,账期,电费(元)\n"+numberedRows("S-G", "2026-02", 3), 2026, 2)

	rep, err := svc.VerifyBatches(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range rep.DataProblems {
		if d.Kind == "row-count-cliff" {
			t.Fatalf("period-grain rule broken: %v — two same-period files must sum to the period before comparing", d.Detail)
		}
	}
}

// TestVerifyDQ4GapPeriodComparesNearestWithData pins the gap ruling: a
// month with no import is never a SUBJECT (the missing-billing scenarios
// own that story), but the period after the gap compares against the
// NEAREST PERIOD WITH DATA — importing under half of it still fires.
func TestVerifyDQ4GapPeriodComparesNearestWithData(t *testing.T) {
	svc, _, dir := newService(t)

	// January: 10 rows. February: nothing. March: 3 rows.
	importCSV(t, svc, dir, "jan.csv", "站址编码,账期,电费(元)\n"+numberedRows("S-J", "2026-01", 10), 2026, 1)
	importCSV(t, svc, dir, "mar.csv", "站址编码,账期,电费(元)\n"+numberedRows("S-M", "2026-03", 3), 2026, 3)

	rep, err := svc.VerifyBatches(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range rep.DataProblems {
		if d.Kind == "row-count-cliff" {
			found = true
			if !strings.Contains(d.Detail, "2026-03") || !strings.Contains(d.Detail, "2026-01") {
				t.Errorf("the gap comparison must name the nearestWithData period: %q", d.Detail)
			}
		}
	}
	if !found {
		t.Fatal("March's 3-vs-10 cliff must fire across the empty February")
	}
}
