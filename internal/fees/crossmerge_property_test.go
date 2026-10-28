package fees

// crossmerge_property_test.go — the #33 I4 face: presence versus amount.
// #31's lesson as a PROPERTY, not examples: the cross-reconcile class is a
// ROW-COUNT decision (rows > 0 = present) and amounts never influence it —
// a zero amount with rows is still presence (zeroSide marks WHICH side's
// money column to audit), and an absent side with any amount is still
// absence. The generated four-tuples (elecRows, elecCents, towerRows,
// towerCents) pin that no generated amount combination can move a site
// across the class borders; the explicit quadrant matrix pins the issue's
// own decision table; the delta/counter identities pin the arithmetic.
//
// crossMerge is pure (the #3 display doctrine), so this whole face runs in
// the DEFAULT build — no engine involved.

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// TestCrossClassIsRowCountProperty pins I4's core: for 200 generated
// four-tuples the class equals the row-count decision table — amounts are
// never consulted. The generator is seeded (deterministic, reproducible)
// and deliberately generates the zero-amount shapes that #31 paid for.
func TestCrossClassIsRowCountProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(33)) // #33: the seeded generator
	for i := 0; i < 200; i++ {
		elecRows := rng.Intn(4)                     // 0..3 (0 = absent side)
		towerRows := rng.Intn(4)                    // 0..3
		elecCents := int64(rng.Intn(5)) * 12345     // 0 included — the #31 shape
		towerCents := int64(rng.Intn(5))*999 - 1000 // negatives included
		site := fmt.Sprintf("S-%03d", i)

		var elec, tower []TotalsRow
		if elecRows > 0 {
			elec = append(elec, TotalsRow{SiteCode: site, Rows: elecRows, TotalCents: elecCents})
		}
		if towerRows > 0 {
			tower = append(tower, TotalsRow{SiteCode: site, Rows: towerRows, TotalCents: towerCents})
		}

		res := crossMerge(2024, 5, elec, tower, nil, nil)
		if elecRows == 0 && towerRows == 0 {
			// the issue's own decision table, row 1: (0, 0) → the site NEVER
			// APPEARS — absence on both sides is absence from the table.
			if len(res.Rows) != 0 {
				t.Fatalf("case %d: both sides absent yet %d merged rows appeared", i, len(res.Rows))
			}
			continue
		}
		if len(res.Rows) != 1 {
			t.Fatalf("case %d (%d/%d rows): %d merged rows, want 1", i, elecRows, towerRows, len(res.Rows))
		}
		row := res.Rows[0]

		want := ""
		switch {
		case elecRows > 0 && towerRows > 0:
			want = string(CrossBoth)
		case elecRows > 0:
			want = string(CrossOnlyElectricity)
		default:
			want = string(CrossOnlyTower)
		}
		if row.Class != want {
			t.Fatalf("case %d: class %q, want %q — the class must follow ROW COUNTS ONLY (elec %d rows/%d cents, tower %d rows/%d cents)",
				i, row.Class, want, elecRows, elecCents, towerRows, towerCents)
		}
		// the money columns travel unharmed: delta is exactly the signed
		// difference of the PRESENT sides' money (an absent side
		// contributes zero — the generated cents of an absent side never
		// entered the input, so they must not enter the expectation).
		wantElec, wantTower := int64(0), int64(0)
		if elecRows > 0 {
			wantElec = elecCents
		}
		if towerRows > 0 {
			wantTower = towerCents
		}
		if row.DeltaCents != wantElec-wantTower {
			t.Fatalf("case %d: delta %d, want %d", i, row.DeltaCents, wantElec-wantTower)
		}
	}
}

// TestCrossQuadrantMatrix pins the issue's own decision table explicitly —
// presence (rows) and amount (cents) are INDEPENDENT axes; every amount
// combination inside a row-count quadrant yields the same class, and the
// zeroSide marker names the zero-money side(s) without ever reclassifying.
func TestCrossQuadrantMatrix(t *testing.T) {
	cases := []struct {
		name               string
		elecRows, towerRow int
		elecC, towerC      int64
		wantClass          string
		wantZeroSide       string
	}{
		{"both non-zero money", 2, 3, 12000, 34000, string(CrossBoth), ""},
		{"both sides zero money", 2, 3, 0, 0, string(CrossBoth), "electricity+tower"},
		{"elec side zero money", 2, 3, 0, 34000, string(CrossBoth), "electricity"},
		{"tower side zero money", 2, 3, 12000, 0, string(CrossBoth), "tower"},
		{"only-elec with zero money", 2, 0, 0, 0, string(CrossOnlyElectricity), ""},
		{"only-elec with money", 2, 0, 12000, 0, string(CrossOnlyElectricity), ""},
		{"only-tower with zero money", 0, 4, 0, 0, string(CrossOnlyTower), ""},
		{"only-tower negative money", 0, 4, 0, -9900, string(CrossOnlyTower), ""},
	}
	for _, c := range cases {
		var elec, tower []TotalsRow
		if c.elecRows > 0 {
			elec = append(elec, TotalsRow{SiteCode: "Q", Rows: c.elecRows, TotalCents: c.elecC})
		}
		if c.towerRow > 0 {
			tower = append(tower, TotalsRow{SiteCode: "Q", Rows: c.towerRow, TotalCents: c.towerC})
		}
		res := crossMerge(2024, 5, elec, tower, nil, nil)
		if len(res.Rows) != 1 {
			t.Fatalf("%s: %d rows, want 1", c.name, len(res.Rows))
		}
		row := res.Rows[0]
		if row.Class != c.wantClass || row.ZeroSide != c.wantZeroSide {
			t.Fatalf("%s: class %q zeroSide %q, want %q/%q",
				c.name, row.Class, row.ZeroSide, c.wantClass, c.wantZeroSide)
		}
	}
}

// TestCrossCounterIdentity pins the envelope's arithmetic identity: the
// three class counters sum to the merged row count for ANY generated
// input — a lost or double-counted class would break the identity.
func TestCrossCounterIdentity(t *testing.T) {
	rng := rand.New(rand.NewSource(331))
	for i := 0; i < 100; i++ {
		n := 1 + rng.Intn(12)
		var elec, tower []TotalsRow
		for s := 0; s < n; s++ {
			site := fmt.Sprintf("S-%02d", s)
			if rng.Intn(2) == 1 {
				elec = append(elec, TotalsRow{SiteCode: site, Rows: 1 + rng.Intn(3), TotalCents: int64(rng.Intn(100000))})
			}
			if rng.Intn(2) == 1 {
				tower = append(tower, TotalsRow{SiteCode: site, Rows: 1 + rng.Intn(3), TotalCents: int64(rng.Intn(100000))})
			}
		}
		res := crossMerge(2024, 5, elec, tower, nil, nil)
		if res.Both+res.OnlyElectricity+res.OnlyTower != len(res.Rows) {
			t.Fatalf("case %d: counters %d+%d+%d != %d rows",
				i, res.Both, res.OnlyElectricity, res.OnlyTower, len(res.Rows))
		}
		// the merged table carries every cent exactly once: summing the
		// per-side money across the merged rows reproduces the per-side
		// input money — merging may redirect codes, never money.
		var elecSum, towerSum int64
		for _, r := range res.Rows {
			elecSum += r.ElectricityCents
			towerSum += r.TowerCents
		}
		var wantElec, wantTower int64
		for _, r := range elec {
			wantElec += r.TotalCents
		}
		for _, r := range tower {
			wantTower += r.TotalCents
		}
		if elecSum != wantElec || towerSum != wantTower {
			t.Fatalf("case %d: money moved in the merge: elec %d→%d, tower %d→%d",
				i, wantElec, elecSum, wantTower, towerSum)
		}
	}
}

// TestCrossMergeDeterministic pins the #3 display doctrine on the merge:
// same input, same output — byte-for-byte — across repeated invocations.
func TestCrossMergeDeterministic(t *testing.T) {
	elec := []TotalsRow{
		{SiteCode: "S-B", Rows: 2, TotalCents: 20000},
		{SiteCode: "S-A", Rows: 1, TotalCents: 10000},
	}
	tower := []TotalsRow{
		{SiteCode: "S-A", Rows: 3, TotalCents: 0},
		{SiteCode: "S-C", Rows: 1, TotalCents: -500},
	}
	first := crossMerge(2024, 5, elec, tower, nil, nil)
	for i := 0; i < 5; i++ {
		again := crossMerge(2024, 5, elec, tower, nil, nil)
		if !reflect.DeepEqual(first, again) {
			t.Fatalf("re-run %d diverged:\n first  %+v\n again  %+v", i, first, again)
		}
	}
}
