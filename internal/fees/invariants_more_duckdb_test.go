//go:build duckdb

package fees

// invariants_more_duckdb_test.go — the #33 I2 supplement faces: the
// honest-absence contract of the monthly series (#3: months without data
// NEVER appear, the display layer draws its own blanks) and the per-site
// conservation split (each site's rows carry exactly their own cents).

import (
	"context"
	"testing"
)

// TestMonthlyTotalsHonestAbsence pins the #3 absence doctrine as an
// invariant: a year with data in May and July answers exactly those
// months — June (no data) NEVER appears as a zero row. A fabricated zero
// month would read as "audited, nothing happened" instead of "no ledger".
func TestMonthlyTotalsHonestAbsence(t *testing.T) {
	svc, _, dir := newService(t)
	engine, analysis := engineFor(t, dir)

	importFixture(t, svc, dir, "may.csv", "站址编码,账期,电费(元)\nS-A,2024-05,100.00\n", 2024, 5)
	importFixture(t, svc, dir, "jul.csv", "站址编码,账期,电费(元)\nS-A,2024-07,300.00\n", 2024, 7)

	rows, err := engine.MonthlyTotals(context.Background(), analysis, FeeTypeElectricity, 2024)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("monthly series = %+v, want exactly May and July", rows)
	}
	if rows[0].Month != 5 || rows[0].TotalCents != 10000 {
		t.Errorf("May = %+v", rows[0])
	}
	if rows[1].Month != 7 || rows[1].TotalCents != 30000 {
		t.Errorf("July = %+v", rows[1])
	}
	for _, r := range rows {
		if r.Month == 6 {
			t.Fatal("June appeared with no data — the honest-absence doctrine is broken")
		}
	}
}

// TestAmountConservationPerSite pins I2's per-site face: every site's
// totals row carries exactly the cents that site's rows carried in the
// fixture — money is conserved per owner, not just in aggregate (an
// aggregate-conserving bug that swapped two sites' money would fail here).
func TestAmountConservationPerSite(t *testing.T) {
	svc, _, dir := newService(t)
	engine, analysis := engineFor(t, dir)

	// three sites, three distinct amounts.
	importFixture(t, svc, dir, "sites.csv",
		"站址编码,账期,电费(元)\nS-A,2024-05,100.00\nS-B,2024-05,201.50\nS-C,2024-05,7.25\n", 2024, 5)

	rows, err := engine.Totals(context.Background(), analysis, FeeTypeElectricity, 2024, 5, 20)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{"S-A": 10000, "S-B": 20150, "S-C": 725}
	if len(rows) != len(want) {
		t.Fatalf("totals rows = %+v", rows)
	}
	for _, r := range rows {
		if want[r.SiteCode] != r.TotalCents {
			t.Errorf("site %s total = %d cents, want %d", r.SiteCode, r.TotalCents, want[r.SiteCode])
		}
	}
}
