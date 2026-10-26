package fees

// crosscheck_test.go — issue #12's pure core and channel discipline: the
// three-way classification of the merge, the alias redirect trail, the
// skeleton normalizer, the alias CRUD refusals, and the import-time
// data-volume guard (<20% 骤降线, floor 10).

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

func TestCodeSkeleton(t *testing.T) {
	cases := []struct{ in, want string }{
		{"HLJ-0001", "HLJ1"},         // hyphen noise + leading zero
		{"hlj0001", "HLJ1"},          // case + zeros
		{"HLJ_001", "HLJ1"},          // underscore
		{"0230101AB01", "230101AB1"}, // classic zero-padded dual-system code (both runs trimmed)
		{"230101AB01", "230101AB1"},  // the unpadded sibling lands on the SAME skeleton
		{"AB10", "AB10"},             // a trailing 10 keeps its 1 (no leading-zero merge)
		{"S000", "S"},                // an all-zero run vanishes — proposal grade, human confirms
		{"HLJ-1", "HLJ1"},
		{"0000", "0000"}, // all-zero keeps its literal skeleton
	}
	for _, c := range cases {
		if got := codeSkeleton(c.in); got != c.want {
			t.Errorf("codeSkeleton(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// the #12 实测 shape: same station, two codes, one skeleton.
	if codeSkeleton("HLJ-0001") != codeSkeleton("hlj001") {
		t.Fatal("the dual-system pair must share a skeleton")
	}
}

func TestCrossMergeThreeWay(t *testing.T) {
	elec := []TotalsRow{
		{SiteCode: "BOTH-1", Rows: 2, TotalCents: 100_000},
		{SiteCode: "ELEC-ONLY", Rows: 1, TotalCents: 50_000},
		{SiteCode: "ALIAS-E", Rows: 1, TotalCents: 20_000}, // redirects to BOTH-2
	}
	tower := []TotalsRow{
		{SiteCode: "BOTH-1", Rows: 1, TotalCents: 80_000},
		{SiteCode: "TOWER-ONLY", Rows: 3, TotalCents: 300_000},
		{SiteCode: "ALIAS-T", Rows: 1, TotalCents: 15_000}, // redirects to BOTH-2
	}
	res := crossMerge(2026, 8, elec, tower,
		map[string]string{"ALIAS-E": "BOTH-2"}, map[string]string{"ALIAS-T": "BOTH-2"})
	if res.Year != 2026 || res.Month != 8 {
		t.Fatalf("period = %d-%d", res.Year, res.Month)
	}
	if res.Both != 2 || res.OnlyElectricity != 1 || res.OnlyTower != 1 {
		t.Fatalf("counts = %+v", res)
	}
	if len(res.Rows) != 4 {
		t.Fatalf("rows = %d, want 4 canonical lines", len(res.Rows))
	}
	// the redirected pair merges onto BOTH-2 with the trail.
	var both2 *CrossRow
	for i := range res.Rows {
		if res.Rows[i].Canonical == "BOTH-2" {
			both2 = &res.Rows[i]
		}
	}
	if both2 == nil {
		t.Fatal("the alias merge target BOTH-2 is missing")
	}
	if both2.ElectricityCents != 20_000 || both2.TowerCents != 15_000 || both2.Class != string(CrossBoth) {
		t.Fatalf("merged line = %+v", *both2)
	}
	if both2.RedirectedFrom == "" {
		t.Fatal("the redirect trail must ride the line")
	}
	// deterministic anomaly-first order: only_electricity, then only_tower,
	// then both (ties by canonical).
	if res.Rows[0].Class != string(CrossOnlyElectricity) || res.Rows[1].Class != string(CrossOnlyTower) {
		t.Fatalf("order = %+v", res.Rows)
	}
	// same input, same output — byte-equal.
	again := crossMerge(2026, 8, elec, tower,
		map[string]string{"ALIAS-E": "BOTH-2"}, map[string]string{"ALIAS-T": "BOTH-2"})
	if len(again.Rows) != len(res.Rows) {
		t.Fatal("merge is not deterministic")
	}
	for i := range res.Rows {
		if res.Rows[i] != again.Rows[i] {
			t.Fatalf("merge row %d diverged: %+v vs %+v", i, res.Rows[i], again.Rows[i])
		}
	}
}

func TestServiceAliasChannel(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()

	a, err := svc.AddSiteCodeAlias(ctx, SiteCodeAliasInput{
		Canonical: "HLJ-0001", Alias: "HLJ001", Source: "tower_lease", Confidence: "rule",
	}, "cli-zhang")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if a.ID == "" || a.Confidence != "rule" || a.CreatedBy != "cli-zhang" {
		t.Fatalf("added = %+v", a)
	}
	// duplicate triple: honest 422 with the existing id.
	if _, err := svc.AddSiteCodeAlias(ctx, SiteCodeAliasInput{
		Canonical: "HLJ-0001", Alias: "HLJ001", Source: "tower_lease", Confidence: "manual",
	}, "cli"); err == nil || !errors.Is(err, ErrImportRefused) || !strings.Contains(err.Error(), a.ID) {
		t.Fatalf("duplicate refusal = %v", err)
	}
	// the refusal vocabulary.
	for _, bad := range []SiteCodeAliasInput{
		{Canonical: "", Alias: "x", Source: "tower_lease", Confidence: "manual"},
		{Canonical: "x", Alias: "x", Source: "tower_lease", Confidence: "manual"},
		{Canonical: "x", Alias: "y", Source: "groceries", Confidence: "manual"},
		{Canonical: "x", Alias: "y", Source: "tower_lease", Confidence: "bogus"},
	} {
		if _, err := svc.AddSiteCodeAlias(ctx, bad, "cli"); err == nil || !errors.Is(err, ErrImportRefused) {
			t.Errorf("refusal %+v: %v", bad, err)
		}
	}
	// no actor: no audit trail: no entry.
	if _, err := svc.AddSiteCodeAlias(ctx, SiteCodeAliasInput{
		Canonical: "x", Alias: "y", Source: "manual", Confidence: "manual"}, ""); err == nil {
		t.Fatal("a missing actor must refuse")
	}
	// manual source accepted (the pure-human cross-system mapping).
	if _, err := svc.AddSiteCodeAlias(ctx, SiteCodeAliasInput{
		Canonical: "x", Alias: "y", Source: "manual", Confidence: "manual"}, "cli"); err != nil {
		t.Fatalf("manual source: %v", err)
	}
	// list: registry order, read-open.
	got, err := svc.ListSiteCodeAliases(ctx)
	if err != nil || len(got) != 2 {
		t.Fatalf("list = %d rows, %v", len(got), err)
	}
	// remove: unknown id keeps ErrNotFound.
	if err := svc.RemoveSiteCodeAlias(ctx, "fca_nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("remove unknown = %v", err)
	}
	if err := svc.RemoveSiteCodeAlias(ctx, a.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	got, _ = svc.ListSiteCodeAliases(ctx)
	if len(got) != 1 {
		t.Fatalf("after remove = %d rows", len(got))
	}
}

func TestImportRowAnomalyGuard(t *testing.T) {
	svc, _, dir := newService(t)
	ctx := context.Background()

	// month 5: 20 rows (a healthy baseline over the floor).
	if _, err := svc.Import(ctx, ImportOptions{
		Path: writeRows(t, dir, "may.csv", 20, "2026-05"), FeeType: FeeTypeTowerLease, Year: 2026, Month: 5,
	}); err != nil {
		t.Fatalf("may import: %v", err)
	}
	// month 6: 2 rows — 10% of May, under the 20% 骤降线 → warning.
	sum, err := svc.Import(ctx, ImportOptions{
		Path: writeRows(t, dir, "jun.csv", 2, "2026-06"), FeeType: FeeTypeTowerLease, Year: 2026, Month: 6,
	})
	if err != nil {
		t.Fatalf("jun import: %v", err)
	}
	if len(sum.Warnings) == 0 || !containsAny(sum.Warnings, "骤降线") {
		t.Fatalf("the volume guard must fire: %+v", sum.Warnings)
	}
	// month 7: 15 rows — 75% of June is over any line, but the honest
	// baseline is month 6's 2 rows < floor → NO warning (no honest
	// baseline = no comparison).
	sum, err = svc.Import(ctx, ImportOptions{
		Path: writeRows(t, dir, "jul.csv", 15, "2026-07"), FeeType: FeeTypeTowerLease, Year: 2026, Month: 7,
	})
	if err != nil {
		t.Fatalf("jul import: %v", err)
	}
	for _, w := range sum.Warnings {
		if containsAny([]string{w}, "骤降线") {
			t.Fatalf("a 15-vs-2 month must not fire the guard (baseline under floor): %+v", sum.Warnings)
		}
	}
	// month 8: another fee type — a type-isolated guard (no warning: no
	// August electricity baseline at all → nothing to compare).
	sum, err = svc.Import(ctx, ImportOptions{
		Path: writeRows(t, dir, "aug-e.csv", 1, "2026-08"), FeeType: FeeTypeElectricity, Year: 2026, Month: 8,
	})
	if err != nil {
		t.Fatalf("aug import: %v", err)
	}
	for _, w := range sum.Warnings {
		if containsAny([]string{w}, "骤降线") {
			t.Fatalf("a type without a baseline must not fire: %+v", sum.Warnings)
		}
	}
}

func containsAny(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// writeRows writes an N-row electricity/tower CSV fixture.
func writeRows(t *testing.T, dir, name string, n int, period string) string {
	t.Helper()
	body := "站址编码,账期,电费(元)\n"
	for i := 0; i < n; i++ {
		body += "S" + strconv.Itoa(i) + "," + period + ",100.00\n"
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
