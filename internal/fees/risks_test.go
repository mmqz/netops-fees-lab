package fees

// risks_test.go — the six-scenario surface's build-independent tests:
// the closed scenario enum, the parameter normalization (documented
// defaults, never magic numbers), the honest 501 contract on default
// builds, and the two CROSS-LAYER compositions (retired-billing and
// unlinked) over a fake engine — the fee-side data is canned, the
// resource side runs against the real SQLite repo, so the composition
// logic (status filtering, link-state semantics, evidence plumbing) is
// pinned on every platform. The REAL SQL presets run in the duckdb-tagged
// engine test.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// fakeRiskEngine is the canned fee side for the composition tests: it
// implements the whole QueryEngine surface with the two existing presets
// erroring (unused here) and the risk primitives returning scripted data.
type fakeRiskEngine struct {
	periodTotals []SitePeriodTotal
	siteTotals   []SiteTotal
	evidence     map[EvidenceKey][]EvidenceAnchor
}

func (f *fakeRiskEngine) Available() bool { return true }
func (f *fakeRiskEngine) Totals(ctx context.Context, analysisDir string, feeType FeeType, year, month, limit int) ([]TotalsRow, error) {
	return nil, errors.New("not used")
}
func (f *fakeRiskEngine) Duplicates(ctx context.Context, analysisDir string, feeType FeeType, year, month, limit int) ([]DuplicateRow, error) {
	return nil, errors.New("not used")
}
func (f *fakeRiskEngine) RiskDuplicatesPreset(ctx context.Context, analysisDir string, q RiskQuery) ([]RiskClue, int, error) {
	return nil, 0, errors.New("not used")
}
func (f *fakeRiskEngine) RiskLedgerMismatchPreset(ctx context.Context, analysisDir string, q RiskQuery) ([]RiskClue, int, error) {
	return nil, 0, errors.New("not used")
}
func (f *fakeRiskEngine) RiskSpikePreset(ctx context.Context, analysisDir string, q RiskQuery) ([]RiskClue, int, error) {
	return nil, 0, errors.New("not used")
}
func (f *fakeRiskEngine) RiskOutlierPreset(ctx context.Context, analysisDir string, q RiskQuery) ([]RiskClue, int, error) {
	return nil, 0, errors.New("not used")
}
func (f *fakeRiskEngine) SitePeriodTotals(ctx context.Context, analysisDir string, feeType FeeType, year, month int) ([]SitePeriodTotal, error) {
	return f.periodTotals, nil
}
func (f *fakeRiskEngine) SiteTotalsAll(ctx context.Context, analysisDir string, feeType FeeType, year, month int) ([]SiteTotal, error) {
	return f.siteTotals, nil
}
func (f *fakeRiskEngine) EvidenceFor(ctx context.Context, analysisDir string, feeType FeeType, keys []EvidenceKey, maxPer int) (map[EvidenceKey][]EvidenceAnchor, error) {
	return f.evidence, nil
}

// TestParseRiskScenario pins the closed enum and its rejection contract.
func TestParseRiskScenario(t *testing.T) {
	if len(RiskScenarios()) != 6 {
		t.Fatalf("scenario count = %d, want 6", len(RiskScenarios()))
	}
	for _, s := range riskScenarios {
		got, err := ParseRiskScenario(string(s))
		if err != nil || got != s {
			t.Errorf("ParseRiskScenario(%q) = %v, %v", s, got, err)
		}
		if RiskScenarioLabels[s] == "" {
			t.Errorf("scenario %q has no Chinese label", s)
		}
	}
	if _, err := ParseRiskScenario("bogus"); err == nil {
		t.Error("ParseRiskScenario(bogus) must error")
	}
}

// TestRiskQueryDefaults pins the documented defaults (#64: no magic
// numbers — the zero values select the documented constants).
func TestRiskQueryDefaults(t *testing.T) {
	q := RiskQuery{}.normalized()
	if q.Ratio != DefaultSpikeRatio || q.ZScore != DefaultOutlierZ || q.MinSites != DefaultMinSites {
		t.Errorf("defaults = ratio %v z %v minSites %v", q.Ratio, q.ZScore, q.MinSites)
	}
	if q.Limit != 100 || q.MaxEvidence != DefaultMaxEvidence {
		t.Errorf("defaults = limit %d maxEvidence %d", q.Limit, q.MaxEvidence)
	}
	if len(q.Statuses) != 2 || q.Statuses[0] != "停用" || q.Statuses[1] != "退网" {
		t.Errorf("default retired statuses = %v", q.Statuses)
	}
	// clamps
	q = RiskQuery{Limit: 5000, MaxEvidence: 500, Ratio: -1}.normalized()
	if q.Limit != 1000 || q.MaxEvidence != 50 || q.Ratio != DefaultSpikeRatio {
		t.Errorf("clamped = %+v", q)
	}
}

// TestRisksHonestContract: on the default build every scenario answers
// ErrEngineNotBuilt — the #64 acceptance item (engine not compiled → 501,
// uniformly across all six).
func TestRisksHonestContract(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	engine := Query(ctx) // the std stub on default builds
	if engine.Available() {
		t.Skip("the duckdb build exercises the real engine elsewhere")
	}
	for _, sc := range riskScenarios {
		_, err := svc.Risks(ctx, engine, sc, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
		if !errors.Is(err, ErrEngineNotBuilt) {
			t.Errorf("scenario %s on default build = %v, want ErrEngineNotBuilt", sc, err)
		}
	}
	// parameter validation still precedes the engine check (a bad fee
	// type is a 400, not a 501).
	if _, err := svc.Risks(ctx, engine, RiskSpike, RiskQuery{FeeType: FeeType("bogus"), Year: 2024}); err == nil || errors.Is(err, ErrEngineNotBuilt) {
		t.Errorf("bad fee type must be a validation error, got %v", err)
	}
	if _, err := svc.Risks(ctx, engine, RiskScenario("bogus"), RiskQuery{FeeType: FeeTypeElectricity, Year: 2024}); err == nil || errors.Is(err, ErrEngineNotBuilt) {
		t.Errorf("bad scenario must be a validation error, got %v", err)
	}
}

// seedLinkResource seeds one site resource + one confirmed link for the
// composition tests.
func seedLinkResource(t *testing.T, db *repo.DB, resID, key, status string) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.Batches.Get(ctx, "bat_risks"); err != nil {
		if err := db.Do(ctx, func(tx *repo.Tx) error {
			return tx.Batches.Create(ctx, domain.ImportBatch{
				ID: "bat_risks", WorkbookName: "r.xlsx", WorkbookSHA256: "riskseed",
				Status: domain.BatchStatusCompleted, Mapping: "{}", RuleVersion: domain.RuleVersion,
			})
		}); err != nil {
			t.Fatalf("seed batch: %v", err)
		}
	}
	if err := db.Do(ctx, func(tx *repo.Tx) error {
		return tx.Resources.Insert(ctx, domain.Resource{
			ID: resID, Type: domain.ResourceTypeSite, NaturalKey: key, Name: key,
			Status: status, Attributes: map[string]string{}, FirstBatchID: "bat_risks",
			CreatedAt: simTSLink, UpdatedAt: simTSLink,
		})
	}); err != nil {
		t.Fatalf("seed resource %s: %v", resID, err)
	}
	if err := db.Do(ctx, func(tx *repo.Tx) error {
		return tx.FeeSiteLinks.InsertProposal(ctx, domain.FeeSiteLink{
			ID: "fsl_" + resID, FeeSiteCode: key, ResourceID: resID,
			ResourceType: domain.ResourceTypeSite, ResourceKey: key,
			Grade: domain.LinkGradeExact, Status: domain.LinkStatusConfirmed,
			Source: domain.LinkSourceAuto, CreatedAt: simTSLink,
		})
	}); err != nil {
		t.Fatalf("seed link %s: %v", resID, err)
	}
}

// TestRisksRetiredBillingComposition pins scenario 1's cross-layer rule:
// only (site, period) pairs whose confirmed link points at a resource in
// the retired set fire; in-service links, unlinked codes and vanished
// resources stay silent. Evidence anchors flow through finishClues.
func TestRisksRetiredBillingComposition(t *testing.T) {
	svc, db, _ := newService(t)
	ctx := context.Background()
	seedLinkResource(t, db, "res_active", "ACTIVE-1", "在网")
	seedLinkResource(t, db, "res_retired", "RETIRED-1", "停用")
	seedLinkResource(t, db, "res_offline", "OFFLINE-1", "退网")

	engine := &fakeRiskEngine{
		periodTotals: []SitePeriodTotal{
			{SiteCode: "ACTIVE-1", Year: 2024, Month: 5, Rows: 1, TotalCents: 10000},
			{SiteCode: "RETIRED-1", Year: 2024, Month: 5, Rows: 2, TotalCents: 25000},
			{SiteCode: "RETIRED-1", Year: 2024, Month: 6, Rows: 1, TotalCents: 12000},
			{SiteCode: "OFFLINE-1", Year: 2024, Month: 5, Rows: 1, TotalCents: 5000},
			{SiteCode: "NOLINK-1", Year: 2024, Month: 5, Rows: 1, TotalCents: 9000},
		},
		evidence: map[EvidenceKey][]EvidenceAnchor{
			{SiteCode: "RETIRED-1", Year: 2024, Month: 5}: {
				{SourceFile: "ret.csv", SourceRow: 2, ImportBatch: "fee_b1", AmountCents: 10000},
				{SourceFile: "ret.csv", SourceRow: 3, ImportBatch: "fee_b1", AmountCents: 15000},
			},
		},
	}
	result, err := svc.Risks(ctx, engine, RiskRetiredBilling, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
	if err != nil {
		t.Fatalf("Risks(retired-billing): %v", err)
	}
	if result.Total != 3 {
		t.Fatalf("clue total = %d, want 3 (停用×2 + 退网×1)", result.Total)
	}
	// deterministic order: metric desc → RETIRED-1/05 (25000), RETIRED-1/06 (12000), OFFLINE-1/05 (5000)
	want := []struct {
		site   string
		month  int
		metric float64
	}{{"RETIRED-1", 5, 25000}, {"RETIRED-1", 6, 12000}, {"OFFLINE-1", 5, 5000}}
	for i, w := range want {
		c := result.Clues[i]
		if c.SiteCode != w.site || c.Month != w.month || c.MetricValue != w.metric {
			t.Errorf("clue[%d] = %s/%d metric %v, want %s/%d %v", i, c.SiteCode, c.Month, c.MetricValue, w.site, w.month, w.metric)
		}
	}
	// evidence anchors attached to the top clue, none fabricated for others
	if len(result.Clues[0].Evidence) != 2 || result.Clues[0].Evidence[0].SourceFile != "ret.csv" {
		t.Errorf("top clue evidence = %+v", result.Clues[0].Evidence)
	}
	// #93: a clue without canned evidence stays anchor-less — as an EMPTY
	// slice, never nil (the arrays-are-never-null discipline; the frontend
	// row expander maps over it without guards).
	if result.Clues[2].Evidence == nil || len(result.Clues[2].Evidence) != 0 {
		t.Errorf("clue without canned evidence must carry an empty anchor list, got %+v", result.Clues[2].Evidence)
	}
	// a custom status set narrows the fire set
	result, err = svc.Risks(ctx, engine, RiskRetiredBilling, RiskQuery{
		FeeType: FeeTypeElectricity, Year: 2024, Month: 5, Statuses: []string{"停用"},
	})
	if err != nil || result.Total != 2 {
		t.Fatalf("custom statuses [停用]: total %d err %v, want 2 (RETIRED-1 ×2)", result.Total, err)
	}
}

// TestRisksUnlinkedComposition pins scenario 6's link-state rule: codes
// covered by a confirmed link OR a pending proposal stay silent (the
// pending pair is the human channel's business); a dismissed proposal
// counts as no match and the code fires.
func TestRisksUnlinkedComposition(t *testing.T) {
	svc, db, _ := newService(t)
	ctx := context.Background()
	seedLinkResource(t, db, "res_covered", "COVERED-1", "在网")
	// a pending proposal on PENDING-1
	if err := db.Do(ctx, func(tx *repo.Tx) error {
		return tx.FeeSiteLinks.InsertProposal(ctx, domain.FeeSiteLink{
			ID: "fsl_p1", FeeSiteCode: "PENDING-1", ResourceID: "res_covered",
			ResourceType: domain.ResourceTypeSite, ResourceKey: "COVERED-1",
			Grade: domain.LinkGradeFuzzy, Status: domain.LinkStatusPending,
			Source: domain.LinkSourceAuto, CreatedAt: simTSLink,
		})
	}); err != nil {
		t.Fatal(err)
	}
	// a dismissed proposal on DISMISSED-1 (the human said no match)
	if err := db.Do(ctx, func(tx *repo.Tx) error {
		return tx.FeeSiteLinks.InsertProposal(ctx, domain.FeeSiteLink{
			ID: "fsl_d1", FeeSiteCode: "DISMISSED-1", ResourceID: "res_covered",
			ResourceType: domain.ResourceTypeSite, ResourceKey: "COVERED-1",
			Grade: domain.LinkGradeFuzzy, Status: domain.LinkStatusDismissed,
			Source: domain.LinkSourceAuto, CreatedAt: simTSLink,
		})
	}); err != nil {
		t.Fatal(err)
	}
	engine := &fakeRiskEngine{
		siteTotals: []SiteTotal{
			{SiteCode: "COVERED-1", Rows: 1, TotalCents: 10000},
			{SiteCode: "PENDING-1", Rows: 1, TotalCents: 20000},
			{SiteCode: "DISMISSED-1", Rows: 3, TotalCents: 70000},
			{SiteCode: "WILD-1", Rows: 1, TotalCents: 30000},
		},
	}
	result, err := svc.Risks(ctx, engine, RiskUnlinked, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Month: 0})
	if err != nil {
		t.Fatalf("Risks(unlinked): %v", err)
	}
	// DISMISSED-1 (70000) and WILD-1 (30000) fire; COVERED-1 and PENDING-1 stay silent.
	if result.Total != 2 || result.Clues[0].SiteCode != "DISMISSED-1" || result.Clues[1].SiteCode != "WILD-1" {
		t.Fatalf("unlinked clues = %+v", result.Clues)
	}
	if result.Clues[0].EvidenceTotal != 3 {
		t.Errorf("DISMISSED-1 evidenceTotal = %d, want 3", result.Clues[0].EvidenceTotal)
	}
}

// TestSplitCommaTrimsAndDrops pins #105-B: the comma-list parser used to
// keep items verbatim, so "停用, 退网" (a manual or URL-encoded space) carried
// " 退网" into the free-text status match — silently narrowing
// retired-billing while the note displayed the spacey form. Every item is
// now trimmed (half/full-width spaces), empties dropped.
func TestSplitCommaTrimsAndDrops(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"停用,退网", []string{"停用", "退网"}},
		{"停用, 退网", []string{"停用", "退网"}},     // the bug shape
		{"停用，退网", []string{"停用", "退网"}},      // full-width comma
		{" 停用 ,  退网 ", []string{"停用", "退网"}}, // spaces everywhere
		{"停用,　退网", []string{"停用", "退网"}},     // full-width space
		{",停用,,退网,", []string{"停用", "退网"}},   // empties dropped
		{"", nil},
		{",,,", nil},
	} {
		got := SplitComma(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("SplitComma(%q) = %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("SplitComma(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}

// TestAnalysisEmptySurfacesGlobErrors pins #105-C: a malformed glob (a "["
// in the data dir path) used to be swallowed as "no matches == empty" —
// the entire query surface answered honest-empty and MASKED the problem
// the doc comment promised to surface. A glob error now reads as
// NON-empty, so the real query runs and surfaces the underlying error.
func TestAnalysisEmptySurfacesGlobErrors(t *testing.T) {
	tmp := t.TempDir()
	// empty dir → honestly empty
	if !analysisEmpty(tmp) {
		t.Error("empty analysis dir: want analysisEmpty == true")
	}
	// a parquet file at the glob's depth (fees/<type>/<year>/<month>/) → non-empty
	deep := filepath.Join(tmp, "fees", "fee_type=electricity", "year=2024", "month=05")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "part-0.parquet"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if analysisEmpty(tmp) {
		t.Error("dir with a parquet file: want analysisEmpty == false")
	}
	// a "[" in the dir makes the glob pattern malformed → the error must
	// read as NON-empty (the query surfaces it), never as honest-empty
	bad := filepath.Join(t.TempDir(), "analysis[bad")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if analysisEmpty(bad) {
		t.Error("malformed glob (dir contains '['): analysisEmpty must answer false so the real query surfaces the error (#105)")
	}
}
