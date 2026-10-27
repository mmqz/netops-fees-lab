package fees

// risks_test.go — the risk-scenario surface's build-independent tests:
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
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/repo"
)

// fakeRiskEngine is the canned fee side for the composition tests: it
// implements the whole QueryEngine surface with the two existing presets
// erroring (unused here) and the risk primitives returning scripted data.
type fakeRiskEngine struct {
	periodTotals []SitePeriodTotal
	siteTotals   []SiteTotal
	evidence     map[EvidenceKey][]EvidenceAnchor
	totals       []TotalsRow
	duplicates   []DuplicateRow
	customRules  []CustomRuleClue
	// #30: the spike exclusion breakdown + outlier excluded count the
	// advice tests script (nil/0 = the not-used answers stay).
	spikeExcluded   *SpikeExcluded
	outlierExcluded int
}

func (f *fakeRiskEngine) Available() bool { return true }

// MonthlyTotals is never exercised by the risk scenarios — the interface
// grew it for the #3 trend surface; the fake carries the honest
// not-built answer so the compile-time contract stays pinned.
func (f *fakeRiskEngine) MonthlyTotals(ctx context.Context, analysisDir string, feeType FeeType, year int) ([]MonthlyTotalsRow, error) {
	return nil, ErrEngineNotBuilt
}
func (f *fakeRiskEngine) Totals(ctx context.Context, analysisDir string, feeType FeeType, year, month, limit int) ([]TotalsRow, error) {
	if f.totals == nil {
		return nil, errors.New("not used")
	}
	return f.totals, nil
}
func (f *fakeRiskEngine) Duplicates(ctx context.Context, analysisDir string, feeType FeeType, year, month, limit int) ([]DuplicateRow, error) {
	if f.duplicates == nil {
		return nil, errors.New("not used")
	}
	return f.duplicates, nil
}
func (f *fakeRiskEngine) LedgerRows(ctx context.Context, analysisDir string, feeType FeeType, year, month int, site string, recordKey string, limit int) ([]LedgerRow, error) {
	return nil, nil // the composition tests never exercise the rows query
}

func (f *fakeRiskEngine) RiskLedgerConsistencyPreset(ctx context.Context, analysisDir string, q RiskQuery) ([]LedgerConsistencyClue, int, error) {
	return nil, 0, nil // scenario 9 has its own test engine surface
}

func (f *fakeRiskEngine) RiskDuplicatesPreset(ctx context.Context, analysisDir string, q RiskQuery) ([]RiskClue, int, error) {
	return nil, 0, errors.New("not used")
}
func (f *fakeRiskEngine) RiskLedgerMismatchPreset(ctx context.Context, analysisDir string, q RiskQuery) ([]RiskClue, int, error) {
	return nil, 0, errors.New("not used")
}
func (f *fakeRiskEngine) RiskSpikePreset(ctx context.Context, analysisDir string, q RiskQuery) ([]RiskClue, SpikeExcluded, error) {
	if f.spikeExcluded == nil {
		return nil, SpikeExcluded{}, errors.New("not used")
	}
	return nil, *f.spikeExcluded, nil
}
func (f *fakeRiskEngine) RiskOutlierPreset(ctx context.Context, analysisDir string, q RiskQuery) ([]RiskClue, int, error) {
	return nil, f.outlierExcluded, nil // 0 = the honest empty the note test exercises
}
func (f *fakeRiskEngine) RiskCustomRulesPreset(ctx context.Context, analysisDir string, q RiskQuery, rules []domain.FeeRiskRule) ([]CustomRuleClue, error) {
	return f.customRules, nil // #2 phase 1: the scripted findings of the composition test
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

// CrossUniverseTotals (#31): the reconcile surface's universe primitive —
// the risk composition tests never call it; the honest not-used answer
// keeps the compile-time contract pinned like every other unused face.
func (f *fakeRiskEngine) CrossUniverseTotals(ctx context.Context, analysisDir string, feeType FeeType, year, month int) ([]TotalsRow, error) {
	return nil, errors.New("not used")
}

// TestParseRiskScenario pins the closed enum and its rejection contract.
func TestParseRiskScenario(t *testing.T) {
	if len(RiskScenarios()) != 10 {
		t.Fatalf("scenario count = %d, want 10", len(RiskScenarios()))
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

// TestRiskOutlierLabelAndNote pins #117: the scenario-5 label claims
// what the preset actually computes (月度金额, NOT 单位成本 — the old
// label promised per-unit normalization the SQL never had), and the
// envelope note carries the honest scale caveat so a human reading the
// clue knows big sites skew high and must be judged against their size.
func TestRiskOutlierLabelAndNote(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	res, err := svc.Risks(ctx, &fakeRiskEngine{}, RiskOutlier, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
	if err != nil {
		t.Fatalf("Risks(outlier, honest empty): %v", err)
	}
	if res.ScenarioCn != "同类站月度金额离群" {
		t.Errorf("scenarioCn = %q, want 同类站月度金额离群 (#117: the label must not claim unit cost)", res.ScenarioCn)
	}
	for _, want := range []string{"月度总额而非单位成本", "规模大的站天然偏高", "统计线索，非认定结论"} {
		if !strings.Contains(res.Note, want) {
			t.Errorf("note missing %q: %s", want, res.Note)
		}
	}
}

// seedMetricPoints seeds one metric series + its points on a resource
// via a real metric batch (the metrics.batch_id FK needs the parent
// row). The key is the catalog's first registered one — the migration
// seed guarantees it exists.
func seedMetricPoints(t *testing.T, db *repo.DB, batchID, resourceID string, ts ...string) {
	t.Helper()
	ctx := context.Background()
	if err := db.MetricBatches.Create(ctx, domain.MetricBatch{
		ID: batchID, WorkbookName: "perf.xlsx", WorkbookSHA256: "sha" + batchID,
		Status: domain.BatchStatusCompleted, Mapping: "{}", StartedAt: simTSLink,
	}); err != nil {
		t.Fatalf("seed metric batch %s: %v", batchID, err)
	}
	seriesID, _, err := db.Metrics.FindOrCreateSeries(ctx, resourceID, "optical_power_rx_avg_dbm", nil, ts[0], ts[len(ts)-1])
	if err != nil {
		t.Fatalf("seed series %s: %v", resourceID, err)
	}
	pts := make([]domain.MetricPoint, 0, len(ts))
	for _, s := range ts {
		pts = append(pts, domain.MetricPoint{
			SeriesID: seriesID, TS: s, Value: -18.5,
			Quality: domain.PointQualityOk, BatchID: batchID,
		})
	}
	if err := db.Metrics.InsertPoints(ctx, pts); err != nil {
		t.Fatalf("seed points %s: %v", resourceID, err)
	}
}

// TestRisksIdleBillingComposition pins scenario 7's cross-layer rule
// (#114): a clue fires when the linked resource carries ZERO metric
// points inside the billed period's window — points outside the window
// do not save it, points inside any series silence it, an unlinked
// code never reaches the metric face (unlinked's business), and the
// whole-year scope windows each (site, period) row on its own month.
func TestRisksIdleBillingComposition(t *testing.T) {
	svc, db, _ := newService(t)
	ctx := context.Background()
	seedLinkResource(t, db, "res_alive", "ALIVE-1", "在网")     // points in May
	seedLinkResource(t, db, "res_dead", "DEAD-1", "在网")       // no points at all
	seedLinkResource(t, db, "res_shifted", "SHIFTED-1", "在网") // points only in April
	// NOLINK-1 has fees but no link — never measured here
	seedMetricPoints(t, db, "mbat_alive", "res_alive", "2024-05-01T00:00:00Z", "2024-05-15T00:00:00Z")
	seedMetricPoints(t, db, "mbat_shifted", "res_shifted", "2024-04-10T00:00:00Z")

	engine := &fakeRiskEngine{
		periodTotals: []SitePeriodTotal{
			{SiteCode: "ALIVE-1", Year: 2024, Month: 5, Rows: 1, TotalCents: 15000},
			{SiteCode: "DEAD-1", Year: 2024, Month: 5, Rows: 2, TotalCents: 80000},
			{SiteCode: "SHIFTED-1", Year: 2024, Month: 5, Rows: 1, TotalCents: 60000}, // April points don't cover May
			{SiteCode: "NOLINK-1", Year: 2024, Month: 5, Rows: 1, TotalCents: 99000},  // unlinked's business
		},
	}
	result, err := svc.Risks(ctx, engine, RiskIdleBilling, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
	if err != nil {
		t.Fatalf("Risks(idle-billing): %v", err)
	}
	// metric desc: DEAD-1 (80000) before SHIFTED-1 (60000); ALIVE-1 has
	// May points, NOLINK-1 has no confirmed link — both stay silent.
	if result.Total != 2 {
		t.Fatalf("clue total = %d, want 2 (DEAD-1 + SHIFTED-1): %+v", result.Total, result.Clues)
	}
	if result.Clues[0].SiteCode != "DEAD-1" || result.Clues[1].SiteCode != "SHIFTED-1" {
		t.Fatalf("clue order = %s, %s, want DEAD-1, SHIFTED-1", result.Clues[0].SiteCode, result.Clues[1].SiteCode)
	}
	if result.Clues[0].EvidenceTotal != 2 {
		t.Errorf("DEAD-1 evidenceTotal = %d, want 2", result.Clues[0].EvidenceTotal)
	}
	if result.Clues[0].Evidence == nil || len(result.Clues[0].Evidence) != 0 {
		t.Errorf("#93: anchor-less clue must carry an empty list, got %+v", result.Clues[0].Evidence)
	}
	if result.Note == "" || result.ScenarioCn != "有费无量" {
		t.Errorf("envelope = %q / %q, want a note + 有费无量", result.Note, result.ScenarioCn)
	}

	// the same resource's points silence the SAME period but not another
	// month of the same year-scope query: ALIVE-1 May silent, June (no
	// points, no May rows) fires only if the fee face has a June row.
	engine.periodTotals = append(engine.periodTotals, SitePeriodTotal{SiteCode: "ALIVE-1", Year: 2024, Month: 6, Rows: 1, TotalCents: 7000})
	result, err = svc.Risks(ctx, engine, RiskIdleBilling, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Month: 0})
	if err != nil {
		t.Fatalf("Risks(idle-billing, whole-year): %v", err)
	}
	var aliveMay, aliveJune int
	for _, c := range result.Clues {
		if c.SiteCode != "ALIVE-1" {
			continue
		}
		if c.Month == 5 {
			aliveMay++
		}
		if c.Month == 6 {
			aliveJune++
		}
	}
	if aliveMay != 0 || aliveJune != 1 {
		t.Fatalf("ALIVE-1 clues: May %d (want 0, has points), June %d (want 1, no points)", aliveMay, aliveJune)
	}
}

// TestPeriodWindowBounds pins the window math: the calendar month and
// the whole-year shape, month 0 spanning Jan 1 through Dec 31.
func TestPeriodWindowBounds(t *testing.T) {
	for _, tc := range []struct {
		y, m     int
		from, to string
	}{
		{2024, 5, "2024-05-01T00:00:00Z", "2024-05-31T23:59:59Z"},
		{2024, 2, "2024-02-01T00:00:00Z", "2024-02-29T23:59:59Z"}, // leap year
		{2023, 12, "2023-12-01T00:00:00Z", "2023-12-31T23:59:59Z"},
		{2024, 0, "2024-01-01T00:00:00Z", "2024-12-31T23:59:59Z"},
	} {
		from, to := periodWindow(tc.y, tc.m)
		if from != tc.from || to != tc.to {
			t.Errorf("periodWindow(%d, %d) = (%q, %q), want (%q, %q)", tc.y, tc.m, from, to, tc.from, tc.to)
		}
	}
}

// TestRisksMissingBillingComposition pins scenario 8's cross-layer rule
// (#118), the mirror of scenario 7: a clue fires when a CONFIRMED-linked,
// non-retired resource carries metric points in a month whose fee side
// holds no record of the queried type. Billed site-months stay silent,
// retired-status resources stay silent (not billing them is correct),
// link rows whose resource vanished stay silent (rollback cascade is a
// business state), pointless months stay silent, and the whole-year
// scope walks each month on its own window.
func TestRisksMissingBillingComposition(t *testing.T) {
	svc, db, _ := newService(t)
	ctx := context.Background()
	seedLinkResource(t, db, "res_unbilled", "UNBILLED-1", "在网")           // May points, no bill → fires
	seedLinkResource(t, db, "res_billed", "BILLED-1", "在网")               // May points, billed → silent
	seedLinkResource(t, db, "res_retired_alive", "RETIRED-ALIVE-1", "停用") // May points, no bill → suppressed (retired)
	seedLinkResource(t, db, "res_pointless", "POINTLESS-1", "在网")         // no points, no bill → silent
	// NOTE the vanished-resource shape (link row without its resource)
	// cannot be seeded: fee_site_links.resource_id carries a real FK, and
	// deleting a resource cascades its links away — the ErrNotFound branch
	// in risksMissingBilling is defensive symmetry with scenario 7, kept
	// for the same reason (a future FK-shape change must not abort the
	// report), but is unreachable through today's schema.
	seedMetricPoints(t, db, "mbat_unbilled", "res_unbilled", "2024-05-01T00:00:00Z", "2024-05-15T00:00:00Z")
	seedMetricPoints(t, db, "mbat_billed", "res_billed", "2024-05-03T00:00:00Z")
	seedMetricPoints(t, db, "mbat_retired", "res_retired_alive", "2024-05-07T00:00:00Z")

	engine := &fakeRiskEngine{
		periodTotals: []SitePeriodTotal{
			{SiteCode: "BILLED-1", Year: 2024, Month: 5, Rows: 1, TotalCents: 15000},
		},
	}
	result, err := svc.Risks(ctx, engine, RiskMissingBilling, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
	if err != nil {
		t.Fatalf("Risks(missing-billing): %v", err)
	}
	if result.Total != 1 {
		t.Fatalf("clue total = %d, want 1 (UNBILLED-1 only): %+v", result.Total, result.Clues)
	}
	c := result.Clues[0]
	if c.SiteCode != "UNBILLED-1" || c.Month != 5 || c.MetricValue != 2 || c.MetricLabel != "metricPoints" {
		t.Fatalf("clue = %+v, want UNBILLED-1 2024-05 metricPoints=2", c)
	}
	if c.Threshold != nil {
		t.Errorf("clue threshold = %v, want nil (points>0 is the deterministic signal)", c.Threshold)
	}
	if c.EvidenceTotal != 0 {
		t.Errorf("evidenceTotal = %d, want 0 (zero fee rows IS the finding)", c.EvidenceTotal)
	}
	if c.Evidence == nil || len(c.Evidence) != 0 {
		t.Errorf("#93: the anchor-less clue must carry an empty list, got %+v", c.Evidence)
	}
	if c.Note == "" || result.Note == "" || result.ScenarioCn != "有量无费" {
		t.Errorf("envelope = %q / note %q / %q, want the caveat notes + 有量无费", c.Note, result.Note, result.ScenarioCn)
	}

	// the whole-year scope walks each month on its own window: add June
	// points for the unbilled resource — May (2 points) then June (1)
	// both fire, sorted metric-desc; every other shape stays silent.
	seedMetricPoints(t, db, "mbat_unbilled6", "res_unbilled", "2024-06-05T00:00:00Z")
	result, err = svc.Risks(ctx, engine, RiskMissingBilling, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Month: 0})
	if err != nil {
		t.Fatalf("Risks(missing-billing, whole-year): %v", err)
	}
	if result.Total != 2 || result.Clues[0].Month != 5 || result.Clues[1].Month != 6 {
		t.Fatalf("whole-year clues = %+v, want May(2 pts) then June(1 pt) for UNBILLED-1", result.Clues)
	}
	for _, clue := range result.Clues {
		if clue.SiteCode != "UNBILLED-1" {
			t.Errorf("whole-year clue site = %s, want UNBILLED-1 only", clue.SiteCode)
		}
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
	tmp := testsupport.TempDir(t)
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
	bad := filepath.Join(testsupport.TempDir(t), "analysis[bad")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if analysisEmpty(bad) {
		t.Error("malformed glob (dir contains '['): analysisEmpty must answer false so the real query surfaces the error (#105)")
	}
}

// TestRiskClueDecisionSemantics pins #119's suppression contract on a
// real repo + fake engine: a dismissed clue never re-appears in the
// default output (counted in Dismissed, never silently dropped — #83),
// a verified clue STAYS visible and marked (the known-issues ledger),
// IncludeDecided returns the full marked set, re-deciding flips the
// status, and the decision is scoped per (scenario, feeType) — a
// dismissed missing-billing clue says nothing about the same site's
// retired-billing clue or its tower-lease scope.
func TestRiskClueDecisionSemantics(t *testing.T) {
	svc, db, _ := newService(t)
	ctx := context.Background()
	seedLinkResource(t, db, "res_unb_a", "UNBILLED-A", "在网")
	seedLinkResource(t, db, "res_unb_b", "UNBILLED-B", "在网")
	seedMetricPoints(t, db, "mbat_unb_a", "res_unb_a", "2024-05-02T00:00:00Z")
	seedMetricPoints(t, db, "mbat_unb_b", "res_unb_b", "2024-05-04T00:00:00Z", "2024-05-09T00:00:00Z")

	// #27: the fee face must EXIST (some site bills) — a completely empty
	// fee face is the all-hit noise shape the missing-billing precheck now
	// names as 「未分析」 instead of emitting every alive resource. The
	// decision-semantics vehicle therefore bills GATE-BILLED-1 while the
	// two UNBILLED sites stay bill-less: the per-site signal shape.
	engine := &fakeRiskEngine{
		periodTotals: []SitePeriodTotal{{SiteCode: "GATE-BILLED-1", Year: 2024, Month: 5, Rows: 1, TotalCents: 12000}},
	}
	q := RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Month: 5}

	res, err := svc.Risks(ctx, engine, RiskMissingBilling, q)
	if err != nil {
		t.Fatalf("risks (no decisions): %v", err)
	}
	if res.Total != 2 || res.Dismissed != 0 {
		t.Fatalf("initial run = total %d / dismissed %d, want 2 / 0: %+v", res.Total, res.Dismissed, res.Clues)
	}

	// dismiss UNBILLED-A's clue (the human channel).
	if _, err := svc.DecideClue(ctx, domain.RiskClueDecision{
		Scenario: "missing-billing", FeeType: domain.FeeTypeElectricity,
		SiteCode: "UNBILLED-A", Year: 2024, Month: 5,
		Status: domain.ClueDecisionDismissed, Note: "季度结算，不是漏收",
	}); err != nil {
		t.Fatalf("decide dismiss: %v", err)
	}
	res, err = svc.Risks(ctx, engine, RiskMissingBilling, q)
	if err != nil {
		t.Fatalf("risks (one dismissed): %v", err)
	}
	if res.Total != 1 || res.Dismissed != 1 || res.Clues[0].SiteCode != "UNBILLED-B" {
		t.Fatalf("after dismiss = total %d / dismissed %d, want 1 / 1 with UNBILLED-B: %+v", res.Total, res.Dismissed, res.Clues)
	}

	// verify UNBILLED-B's clue: stays VISIBLE and marked.
	if _, err := svc.DecideClue(ctx, domain.RiskClueDecision{
		Scenario: "missing-billing", FeeType: domain.FeeTypeElectricity,
		SiteCode: "UNBILLED-B", Year: 2024, Month: 5,
		Status: domain.ClueDecisionVerified, Note: "属实，已转资损核查",
	}); err != nil {
		t.Fatalf("decide verify: %v", err)
	}
	res, err = svc.Risks(ctx, engine, RiskMissingBilling, q)
	if err != nil {
		t.Fatalf("risks (one verified): %v", err)
	}
	if res.Total != 1 || res.Clues[0].SiteCode != "UNBILLED-B" ||
		res.Clues[0].Decision != "verified" || res.Clues[0].DecidedAt == "" {
		t.Fatalf("verified clue = %+v, want UNBILLED-B marked verified with decidedAt", res.Clues[0])
	}

	// IncludeDecided: the full marked set — A dismissed, B verified.
	q.IncludeDecided = true
	res, err = svc.Risks(ctx, engine, RiskMissingBilling, q)
	if err != nil {
		t.Fatalf("risks (include decided): %v", err)
	}
	if res.Total != 2 || res.Dismissed != 1 {
		t.Fatalf("include-decided run = total %d / dismissed %d, want 2 / 1", res.Total, res.Dismissed)
	}
	bySite := map[string]RiskClue{}
	for _, c := range res.Clues {
		bySite[c.SiteCode] = c
	}
	if bySite["UNBILLED-A"].Decision != "dismissed" || bySite["UNBILLED-B"].Decision != "verified" {
		t.Fatalf("marks = A:%q B:%q, want dismissed/verified", bySite["UNBILLED-A"].Decision, bySite["UNBILLED-B"].Decision)
	}
	q.IncludeDecided = false

	// the decision is SCOPED: the same site's clue under another scenario
	// is untouched (the missing-billing dismissal says nothing about
	// retired-billing), and another fee type's missing-billing scope is a
	// different clue too.
	res, err = svc.Risks(ctx, engine, RiskMissingBilling, RiskQuery{FeeType: FeeTypeTowerLease, Year: 2024, Month: 5})
	if err != nil {
		t.Fatalf("risks (tower_lease scope): %v", err)
	}
	if res.Total != 2 || res.Dismissed != 0 {
		t.Fatalf("tower_lease scope = total %d / dismissed %d, want 2 / 0 — decisions do not leak across fee types", res.Total, res.Dismissed)
	}

	// re-decide flips: UNBILLED-A verified → visible again.
	if _, err := svc.DecideClue(ctx, domain.RiskClueDecision{
		Scenario: "missing-billing", FeeType: domain.FeeTypeElectricity,
		SiteCode: "UNBILLED-A", Year: 2024, Month: 5,
		Status: domain.ClueDecisionVerified,
	}); err != nil {
		t.Fatalf("re-decide verify: %v", err)
	}
	res, err = svc.Risks(ctx, engine, RiskMissingBilling, q)
	if err != nil {
		t.Fatalf("risks (after flip): %v", err)
	}
	if res.Total != 2 || res.Dismissed != 0 {
		t.Fatalf("after flip = total %d / dismissed %d, want 2 / 0 (both verified)", res.Total, res.Dismissed)
	}

	// the ledger view: two decisions (the flip kept ONE row for A) —
	// newest first, filters narrow.
	items, total, err := svc.ListClueDecisions(ctx, repo.ClueDecisionFilter{Status: string(domain.ClueDecisionVerified)})
	if err != nil || total != 2 || len(items) != 2 {
		t.Fatalf("ledger = %d items / total %d (err %v), want 2 verified", len(items), total, err)
	}
}

// TestDecideClueValidation pins the human channel's input contract: the
// closed enums (scenario / feeType / status) and the period bounds
// refuse before any write, exactly like the Risks entry point.
func TestDecideClueValidation(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	base := domain.RiskClueDecision{
		Scenario: "spike", FeeType: domain.FeeTypeElectricity,
		SiteCode: "S-1", Year: 2024, Month: 5, Status: domain.ClueDecisionVerified,
	}
	for _, bad := range []struct {
		mutate func(*domain.RiskClueDecision)
		want   string
	}{
		{func(d *domain.RiskClueDecision) { d.Scenario = "bogus" }, "未知风险场景"},
		{func(d *domain.RiskClueDecision) { d.FeeType = domain.FeeType("bogus") }, "未知费用类型"},
		{func(d *domain.RiskClueDecision) { d.Status = domain.ClueDecisionStatus("bogus") }, "未知线索裁定"},
		{func(d *domain.RiskClueDecision) { d.SiteCode = "" }, "缺站址编码"},
		{func(d *domain.RiskClueDecision) { d.Year = 1999 }, "不在合法范围"},
		{func(d *domain.RiskClueDecision) { d.Month = 13 }, "不在合法范围"},
	} {
		d := base
		bad.mutate(&d)
		_, err := svc.DecideClue(ctx, d)
		if err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Errorf("decide(%v) error = %v, want containing %q", d, err, bad.want)
		}
	}
	// the ledger filters validate the same way.
	if _, _, err := svc.ListClueDecisions(ctx, repo.ClueDecisionFilter{Scenario: "bogus"}); err == nil || !strings.Contains(err.Error(), "未知风险场景") {
		t.Errorf("list(bogus scenario) error = %v, want the closed-set refusal", err)
	}
	if _, _, err := svc.ListClueDecisions(ctx, repo.ClueDecisionFilter{Status: "bogus"}); err == nil || !strings.Contains(err.Error(), "未知线索裁定") {
		t.Errorf("list(bogus status) error = %v, want the closed-set refusal", err)
	}
	// the pagination clamps: limit 0 → default 20, limit 1000 → 100.
	items, _, err := svc.ListClueDecisions(ctx, repo.ClueDecisionFilter{Limit: 1000})
	if err != nil || len(items) != 0 {
		t.Fatalf("clamped list err = %v items = %d (want empty, fresh ledger)", err, len(items))
	}
}

// TestQueryPresetsViaService pins #120's service wrappers: validation
// (closed fee type, period bounds, positive limit) precedes the engine
// gate; the unavailable engine answers the honest refusal; the available
// one returns the rows through the service's own analysis dir.
func TestQueryPresetsViaService(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	engine := &fakeRiskEngine{
		totals:     []TotalsRow{{SiteCode: "S-1", Rows: 3, TotalCents: 123456}},
		duplicates: []DuplicateRow{{SiteCode: "S-2", Year: 2024, Month: 5, Batches: 2, Rows: 2, TotalCents: 20000}},
	}

	// validation precedes the engine gate (the Risks entry shape).
	for _, tc := range []struct {
		call   string
		callFn func() error
		want   string
	}{
		{"totals bogus type", func() error {
			_, err := svc.QueryTotals(ctx, engine, FeeType("bogus"), 2024, 5, 20)
			return err
		}, "未知费用类型"},
		{"duplicates bogus type", func() error {
			_, err := svc.QueryDuplicates(ctx, engine, FeeType("bogus"), 2024, 5, 20)
			return err
		}, "未知费用类型"},
		{"totals bad year", func() error {
			_, err := svc.QueryTotals(ctx, engine, FeeTypeElectricity, 1999, 0, 20)
			return err
		}, "不在合法范围"},
		{"totals bad month", func() error {
			_, err := svc.QueryTotals(ctx, engine, FeeTypeElectricity, 2024, 13, 20)
			return err
		}, "不在合法范围"},
		{"duplicates zero limit", func() error {
			_, err := svc.QueryDuplicates(ctx, engine, FeeTypeElectricity, 2024, 5, 0)
			return err
		}, "limit 必须是"},
	} {
		if err := tc.callFn(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want containing %q", tc.call, err, tc.want)
		}
	}

	// the unavailable engine refuses honestly, validation already passed
	// (the default build's shape; the duckdb-tagged tests own the real
	// engine).
	_, err := svc.QueryTotals(ctx, unavailableEngine{}, FeeTypeElectricity, 2024, 5, 20)
	if !errors.Is(err, ErrEngineNotBuilt) {
		t.Fatalf("totals on unavailable engine = %v, want ErrEngineNotBuilt", err)
	}
	_, err = svc.QueryDuplicates(ctx, unavailableEngine{}, FeeTypeElectricity, 2024, 5, 20)
	if !errors.Is(err, ErrEngineNotBuilt) {
		t.Fatalf("duplicates on unavailable engine = %v, want ErrEngineNotBuilt", err)
	}

	// the available engine returns the rows.
	rows, err := svc.QueryTotals(ctx, engine, FeeTypeElectricity, 2024, 5, 20)
	if err != nil || len(rows) != 1 || rows[0].SiteCode != "S-1" || rows[0].TotalCents != 123456 {
		t.Fatalf("QueryTotals = %+v (err %v)", rows, err)
	}
	dups, err := svc.QueryDuplicates(ctx, engine, FeeTypeElectricity, 2024, 5, 20)
	if err != nil || len(dups) != 1 || dups[0].Batches != 2 {
		t.Fatalf("QueryDuplicates = %+v (err %v)", dups, err)
	}
}

// unavailableEngine implements the QueryEngine face with Available()
// false — the default build's shape (the duckdb-tagged tests own the
// real engine).
type unavailableEngine struct{}

func (unavailableEngine) Available() bool { return false }
func (unavailableEngine) MonthlyTotals(ctx context.Context, analysisDir string, feeType FeeType, year int) ([]MonthlyTotalsRow, error) {
	return nil, errors.New("unreachable")
}
func (unavailableEngine) Totals(ctx context.Context, analysisDir string, feeType FeeType, year, month, limit int) ([]TotalsRow, error) {
	return nil, errors.New("unreachable")
}
func (unavailableEngine) Duplicates(ctx context.Context, analysisDir string, feeType FeeType, year, month, limit int) ([]DuplicateRow, error) {
	return nil, errors.New("unreachable")
}
func (unavailableEngine) LedgerRows(ctx context.Context, analysisDir string, feeType FeeType, year, month int, site string, recordKey string, limit int) ([]LedgerRow, error) {
	return nil, errors.New("unreachable")
}
func (unavailableEngine) RiskLedgerConsistencyPreset(ctx context.Context, analysisDir string, q RiskQuery) ([]LedgerConsistencyClue, int, error) {
	return nil, 0, errors.New("unreachable")
}
func (unavailableEngine) RiskDuplicatesPreset(ctx context.Context, analysisDir string, q RiskQuery) ([]RiskClue, int, error) {
	return nil, 0, errors.New("unreachable")
}
func (unavailableEngine) RiskLedgerMismatchPreset(ctx context.Context, analysisDir string, q RiskQuery) ([]RiskClue, int, error) {
	return nil, 0, errors.New("unreachable")
}
func (unavailableEngine) RiskSpikePreset(ctx context.Context, analysisDir string, q RiskQuery) ([]RiskClue, SpikeExcluded, error) {
	return nil, SpikeExcluded{}, errors.New("unreachable")
}
func (unavailableEngine) RiskOutlierPreset(ctx context.Context, analysisDir string, q RiskQuery) ([]RiskClue, int, error) {
	return nil, 0, errors.New("unreachable")
}
func (unavailableEngine) RiskCustomRulesPreset(ctx context.Context, analysisDir string, q RiskQuery, rules []domain.FeeRiskRule) ([]CustomRuleClue, error) {
	return nil, errors.New("unreachable")
}
func (unavailableEngine) SitePeriodTotals(ctx context.Context, analysisDir string, feeType FeeType, year, month int) ([]SitePeriodTotal, error) {
	return nil, errors.New("unreachable")
}
func (unavailableEngine) SiteTotalsAll(ctx context.Context, analysisDir string, feeType FeeType, year, month int) ([]SiteTotal, error) {
	return nil, errors.New("unreachable")
}
func (unavailableEngine) EvidenceFor(ctx context.Context, analysisDir string, feeType FeeType, keys []EvidenceKey, maxPer int) (map[EvidenceKey][]EvidenceAnchor, error) {
	return nil, errors.New("unreachable")
}

// CrossUniverseTotals (#31): the unavailable engine's face-filling stub.
func (unavailableEngine) CrossUniverseTotals(ctx context.Context, analysisDir string, feeType FeeType, year, month int) ([]TotalsRow, error) {
	return nil, errors.New("unreachable")
}

// TestRisksUnanalyzedPrechecks (#27): the empty-cross-face prechecks.
// An empty resource library, zero confirmed links, an entirely empty
// metrics face, or an empty fee face for the queried scope makes the
// cross-layer scenarios answer 「未分析」 — zero clues + the reason + the
// next step, riding BOTH the note (CLI/MCP consumers) and warnings (the
// #21 UI alert channel) — instead of an all-hit noise flood (unlinked's
// 200+ rows) or a vacuous all-clear. Partial faces keep the normal path
// (pinned by the four composition tests above: seeded resources, links,
// points and fee rows all keep their clue sets).
func TestRisksUnanalyzedPrechecks(t *testing.T) {
	ctx := context.Background()
	// The fee face exists (rows on both primitives) so the gates fire on
	// the CROSS face being empty, never on a missing fee face (except
	// missing-billing's own gate 3, which needs the fee face empty).
	feeFace := &fakeRiskEngine{
		siteTotals:   []SiteTotal{{SiteCode: "WILD-1", Rows: 2, TotalCents: 30000}},
		periodTotals: []SitePeriodTotal{{SiteCode: "WILD-1", Year: 2024, Month: 5, Rows: 2, TotalCents: 30000}},
	}
	assertUnanalyzed := func(t *testing.T, res RiskResult, wantFrag string) {
		t.Helper()
		if res.Total != 0 || len(res.Clues) != 0 {
			t.Fatalf("clues = %d (%+v), want the unanalyzed zero-clue answer", res.Total, res.Clues)
		}
		if !strings.Contains(res.Note, wantFrag) {
			t.Errorf("note missing %q: %s", wantFrag, res.Note)
		}
		if len(res.Warnings) != 1 || res.Warnings[0] != res.Note {
			t.Errorf("warnings = %+v, want the #21 channel carrying the same text as the note", res.Warnings)
		}
	}

	// 1. unlinked on a completely empty resource library: the pre-#27
	// answer was the WHOLE fee face as clues — the noise flood.
	svc, _, _ := newService(t)
	res, err := svc.Risks(ctx, feeFace, RiskUnlinked, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
	if err != nil {
		t.Fatalf("Risks(unlinked, empty library): %v", err)
	}
	assertUnanalyzed(t, res, "零资源计费未分析：资源库为空")

	// 2. retired-billing with zero confirmed links (nothing seeded at
	// all): vacuous universe named instead of an all-clear.
	svc2, _, _ := newService(t)
	res, err = svc2.Risks(ctx, feeFace, RiskRetiredBilling, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
	if err != nil {
		t.Fatalf("Risks(retired-billing, no links): %v", err)
	}
	assertUnanalyzed(t, res, "停用计费未分析：无任何已确认关联")

	// 3. idle-billing: confirmed link + fee rows exist, but the metrics
	// face is COMPLETELY empty — every linked billed site would fire at
	// once (the mirror flood).
	svc3, db3, _ := newService(t)
	seedLinkResource(t, db3, "res_gate_idle", "GATE-IDLE-1", "在网")
	res, err = svc3.Risks(ctx, feeFace, RiskIdleBilling, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
	if err != nil {
		t.Fatalf("Risks(idle-billing, empty metrics): %v", err)
	}
	assertUnanalyzed(t, res, "零量计费未分析：指标采集面为空")

	// 4a. missing-billing with no confirmed links at all.
	svc4, _, _ := newService(t)
	res, err = svc4.Risks(ctx, feeFace, RiskMissingBilling, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
	if err != nil {
		t.Fatalf("Risks(missing-billing, no links): %v", err)
	}
	assertUnanalyzed(t, res, "有量无费未分析：无任何已确认关联")

	// 4b. missing-billing: links exist, metrics face entirely empty.
	svc5, db5, _ := newService(t)
	seedLinkResource(t, db5, "res_gate_mb", "GATE-MB-1", "在网")
	res, err = svc5.Risks(ctx, feeFace, RiskMissingBilling, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
	if err != nil {
		t.Fatalf("Risks(missing-billing, empty metrics): %v", err)
	}
	assertUnanalyzed(t, res, "有量无费未分析：指标采集面为空")

	// 4c. missing-billing: links + points exist, but the fee face for the
	// queried type/period is EMPTY (the fake returns no rows) — every
	// alive resource would fire at once.
	svc6, db6, _ := newService(t)
	seedLinkResource(t, db6, "res_gate_fee", "GATE-FEE-1", "在网")
	seedMetricPoints(t, db6, "mbat_gate_fee", "res_gate_fee", "2024-05-05T00:00:00Z")
	res, err = svc6.Risks(ctx, &fakeRiskEngine{}, RiskMissingBilling, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
	if err != nil {
		t.Fatalf("Risks(missing-billing, empty fee face): %v", err)
	}
	assertUnanalyzed(t, res, "有量无费未分析：费用侧该类型/账期无任何计费记录")
}
