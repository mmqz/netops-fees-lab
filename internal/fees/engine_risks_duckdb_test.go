//go:build duckdb

package fees

// engine_risks_duckdb_test.go — the #64 acceptance proof on the REAL
// engine: all seven scenarios over data imported through the real
// pipeline, with the determinism ruling pinned (scenario 4/5 exclude
// multi-batch (site, period) pairs and REPORT the exclusion — a re-run
// on dirty data returns byte-identical results).

import (
	"context"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/repo"
)

// importRiskFixture seeds the analysis table for the risk-scenario suite:
//
//   - S-SPIKE: 2024-05 = 100.00 → 2024-06 = 250.00 (rise ratio 1.5 → clue)
//   - S-FLAT:  2024-05 = 100.00 → 2024-06 = 105.00 (5% → silent)
//   - S-DUP:   two batches of 100.00 in BOTH 05 and 06 (duplicate-settlement
//     clue ×2; amount-equal so ledger-mismatch stays silent; multi-batch
//     so spike/outlier exclude it — 3 excluded pairs total)
//   - S-LEDG:  batch A 200.00 + batch B 250.00 in 2024-05 (mismatch
//     delta 5000 cents → clue; also a duplicates clue)
//   - S-O01..S-O12: 100.00 each in 2024-05 — the outlier peer set
//   - S-OUT:   1000.00 in 2024-05 (z ≈ 4.3 against the 14 peers → clue)
//   - S-WILD:  900.00 in 2024-06 (no resource at all — unlinked)
//
// Resource side (the scenario-1/6/7 compositions): RETIRED-SITE seeded 停用
// with a confirmed link bills on; ACTIVE-SITE 在网 with a link stays
// silent; PENDING-SITE has a pending proposal (human channel) and stays
// silent in unlinked. Scenario 7 (#114) reuses the pair from the metric
// face: RETIRED-SITE's resource carries NO metric points (fires), while
// ACTIVE-SITE's resource gets June points seeded in the scenario-7 block
// (stays silent) — the fee face says both bill, the measurement face
// only contradicts one.
func importRiskFixture(t *testing.T, svc *Service) {
	t.Helper()
	ctx := context.Background()
	// #20: the declared period owns the partition — a file's rows land in
	// the DECLARED month, so multi-month fixtures import as one file per
	// declared month (the real monthly-ledger shape: one file per month).
	files := []struct {
		name    string
		year, m int
		body    string
	}{
		{"spike-a.csv", 2024, 5, "站址编码,账期,电费(元)\nS-SPIKE,2024-05,100.00\nS-FLAT,2024-05,100.00\n"},
		{"spike-b.csv", 2024, 6, "站址编码,账期,电费(元)\nS-SPIKE,2024-06,250.00\nS-FLAT,2024-06,105.00\n"},
		{"dup-a-05.csv", 2024, 5, "站址编码,账期,电费(元),备注\nS-DUP,2024-05,100.00,台账A\n"},
		{"dup-a-06.csv", 2024, 6, "站址编码,账期,电费(元),备注\nS-DUP,2024-06,100.00,台账A\n"},
		{"dup-b-05.csv", 2024, 5, "站址编码,账期,电费(元),备注\nS-DUP,2024-05,100.00,台账B\n"},
		{"dup-b-06.csv", 2024, 6, "站址编码,账期,电费(元),备注\nS-DUP,2024-06,100.00,台账B\n"},
		{"ledg-a.csv", 2024, 5, "站址编码,账期,电费(元)\nS-LEDG,2024-05,200.00\n"},
		{"ledg-b.csv", 2024, 5, "站址编码,账期,电费(元)\nS-LEDG,2024-05,250.00\n"},
		{"peers.csv", 2024, 5, "站址编码,账期,电费(元)\nS-O01,2024-05,100.00\nS-O02,2024-05,100.00\nS-O03,2024-05,100.00\nS-O04,2024-05,100.00\nS-O05,2024-05,100.00\nS-O06,2024-05,100.00\nS-O07,2024-05,100.00\nS-O08,2024-05,100.00\nS-O09,2024-05,100.00\nS-O10,2024-05,100.00\nS-O11,2024-05,100.00\nS-O12,2024-05,100.00\n"},
		{"outlier.csv", 2024, 5, "站址编码,账期,电费(元)\nS-OUT,2024-05,1000.00\n"},
		{"wild.csv", 2024, 6, "站址编码,账期,电费(元)\nS-WILD,2024-06,900.00\nRETIRED-SITE,2024-06,777.00\nACTIVE-SITE,2024-06,555.00\nPENDING-SITE,2024-06,444.00\n"},
		// #105: the zero-base spike shape — ¥0.00 in 07 followed by ¥900 in
		// 08 is the STRONGEST month-over-month rise there is, but the ratio
		// is undefined on a zero base: it must appear in the EXCLUDED
		// envelope (it used to be invisible in both counters). Months 07/08
		// keep it out of the month-05 outlier stats.
		{"zero-base-07.csv", 2024, 7, "站址编码,账期,电费(元)\nS-ZERO,2024-07,0.00\n"},
		{"zero-base-08.csv", 2024, 8, "站址编码,账期,电费(元)\nS-ZERO,2024-08,900.00\n"},
	}
	for _, f := range files {
		p := filepath.Join(testsupport.TempDir(t), f.name)
		if err := os.WriteFile(p, []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Import(ctx, ImportOptions{Path: p, FeeType: FeeTypeElectricity, Year: f.year, Month: f.m}); err != nil {
			t.Fatalf("import %s: %v", f.name, err)
		}
	}
}

// TestRiskPresetsDuck runs all seven scenarios on the real engine and pins
// the #64 acceptance surface: clue sets, integer-cent metrics, explicit
// thresholds, the determinism exclusion, evidence anchors, and a re-run
// returning identical results.
func TestRiskPresetsDuck(t *testing.T) {
	svc, db, _ := newService(t)
	ctx := context.Background()
	importRiskFixture(t, svc)

	// resource side for the cross-layer scenarios
	seedLinkResource(t, db, "res_retired_site", "RETIRED-SITE", "停用")
	seedLinkResource(t, db, "res_active_site", "ACTIVE-SITE", "在网")
	// #118: the missing-billing mirror — June points, NO bills at all.
	seedLinkResource(t, db, "res_unbilled_site", "UNBILLED-SITE", "在网")
	if err := db.Do(ctx, func(tx *repo.Tx) error {
		return tx.FeeSiteLinks.InsertProposal(ctx, domain.FeeSiteLink{
			ID: "fsl_pending_site", FeeSiteCode: "PENDING-SITE", ResourceID: "res_active_site",
			ResourceType: domain.ResourceTypeSite, ResourceKey: "ACTIVE-SITE",
			Grade: domain.LinkGradeFuzzy, Status: domain.LinkStatusPending,
			Source: domain.LinkSourceAuto, CreatedAt: simTSLink,
		})
	}); err != nil {
		t.Fatal(err)
	}

	engine := Query(ctx)
	if !engine.Available() {
		t.Fatal("the duckdb build must report Available")
	}
	base := RiskQuery{FeeType: FeeTypeElectricity, Year: 2024}

	// ---- scenario 2: duplicate-settlement (whole year) ----
	res, err := svc.Risks(ctx, engine, RiskDuplicateSettlement, base)
	if err != nil {
		t.Fatalf("duplicate-settlement: %v", err)
	}
	// S-DUP×2 (05, 06) + S-LEDG×1 (05)
	if res.Total != 3 {
		t.Fatalf("duplicate-settlement total = %d, want 3: %+v", res.Total, res.Clues)
	}
	if res.Clues[0].SiteCode != "S-DUP" || res.Clues[0].Month != 5 ||
		res.Clues[0].MetricValue != 2 || res.Clues[0].MetricLabel != "batches" ||
		res.Clues[0].Threshold == nil || *res.Clues[0].Threshold != 1 {
		t.Fatalf("dup clue[0] = %+v", res.Clues[0])
	}
	// deterministic (import_batch, source_row) anchor order, integer cents
	if len(res.Clues[0].Evidence) != 2 || res.Clues[0].EvidenceTotal != 2 {
		t.Fatalf("dup clue[0] evidence = %+v (total %d)", res.Clues[0].Evidence, res.Clues[0].EvidenceTotal)
	}
	for _, a := range res.Clues[0].Evidence {
		if a.AmountCents != 10000 {
			t.Errorf("anchor amount = %d cents, want 10000", a.AmountCents)
		}
	}

	// ---- scenario 3: ledger-mismatch ----
	res, err = svc.Risks(ctx, engine, RiskLedgerMismatch, base)
	if err != nil {
		t.Fatalf("ledger-mismatch: %v", err)
	}
	// S-DUP is amount-equal (delta 0) → silent; S-LEDG delta 5000 fires.
	if res.Total != 1 || res.Clues[0].SiteCode != "S-LEDG" || res.Clues[0].MetricValue != 5000 {
		t.Fatalf("ledger-mismatch = %+v", res.Clues)
	}
	if res.Clues[0].MetricLabel != "deltaCents" || res.Clues[0].ThresholdLabel == "" {
		t.Errorf("mismatch labels = %q / %q", res.Clues[0].MetricLabel, res.Clues[0].ThresholdLabel)
	}
	// the two batches' anchors carry the two differing amounts
	if len(res.Clues[0].Evidence) != 2 ||
		res.Clues[0].Evidence[0].AmountCents+res.Clues[0].Evidence[1].AmountCents != 45000 {
		t.Errorf("mismatch anchors = %+v", res.Clues[0].Evidence)
	}

	// ---- scenario 4: spike (determinism ruling) ----
	res, err = svc.Risks(ctx, engine, RiskSpike, base)
	if err != nil {
		t.Fatalf("spike: %v", err)
	}
	// S-SPIKE 100→250 = 1.5 ✓; S-FLAT 5% ✗; S-WILD has no prior month (06
	// is its first) ✗; S-DUP excluded (multi-batch); S-LEDG excluded.
	if res.Total != 1 || res.Clues[0].SiteCode != "S-SPIKE" || res.Clues[0].Month != 6 {
		t.Fatalf("spike clues = %+v", res.Clues)
	}
	if got := res.Clues[0].MetricValue; got < 1.49 || got > 1.51 {
		t.Errorf("spike ratio = %v, want 1.5", got)
	}
	// S-DUP(05) + S-DUP(06) + S-LEDG(05) = 3 multi-batch pairs, plus the
	// #105 class: S-ZERO(08) whose previous billed month total is 0 — the
	// zero-base pair the ratio cannot judge but the envelope must count.
	if res.Excluded != 4 {
		t.Errorf("spike excluded = %d, want 4 (3 multi-batch + 1 zero-base, #105)", res.Excluded)
	}
	// single-month scope narrows the clue window
	resM, err := svc.Risks(ctx, engine, RiskSpike, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Month: 6})
	if err != nil || resM.Total != 1 || resM.Clues[0].SiteCode != "S-SPIKE" {
		t.Fatalf("spike month=6 = %+v (err %v)", resM.Clues, err)
	}
	// custom ratio above 1.5 → silent, but the honest envelope must survive
	// the empty clue set (#83: the old first-row capture fell back to 0
	// exactly here — misreporting "nothing was excluded" while the year
	// still holds 3 multi-batch pairs).
	resHi, err := svc.Risks(ctx, engine, RiskSpike, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Ratio: 1.6})
	if err != nil || resHi.Total != 0 {
		t.Fatalf("spike ratio=1.6 must be silent, got %+v (err %v)", resHi.Clues, err)
	}
	if resHi.Excluded != 4 {
		t.Errorf("spike ratio=1.6 excluded = %d, want 4 — the envelope must stay truthful on an empty clue set (#83) and count the zero-base class (#105)", resHi.Excluded)
	}

	// ---- scenario 5: outlier ----
	res, err = svc.Risks(ctx, engine, RiskOutlier, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
	if err != nil {
		t.Fatalf("outlier: %v", err)
	}
	if res.Total != 1 || res.Clues[0].SiteCode != "S-OUT" {
		t.Fatalf("outlier clues = %+v", res.Clues)
	}
	// 14 peers at 100.00 + one at 1000.00: mean 160, stddev_samp ≈ 232.4
	// → z = 840/232.4 ≈ 3.61 (the hand-checkable number — pinned loosely
	// to survive peer-set drift from the other scenarios' fixtures).
	if z := res.Clues[0].MetricValue; z < 3.5 || z > 3.7 {
		t.Errorf("S-OUT z = %v, want ≈ 3.61 against the 14 peers", z)
	}
	// the month=5 scope excludes only MAY's multi-batch pairs: S-DUP/05
	// + S-LEDG/05 = 2 (S-DUP/06 is June, outside this scope).
	if res.Clues[0].MetricLabel != "zScore" || res.Excluded != 2 {
		t.Errorf("outlier labels/excluded = %q / %d", res.Clues[0].MetricLabel, res.Excluded)
	}
	// a z bar above the max attainable → silent, envelope still truthful (#83)
	resZ, err := svc.Risks(ctx, engine, RiskOutlier, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Month: 5, ZScore: 5})
	if err != nil || resZ.Total != 0 {
		t.Fatalf("outlier z=5 must be silent, got %+v (err %v)", resZ.Clues, err)
	}
	if resZ.Excluded != 2 {
		t.Errorf("outlier z=5 excluded = %d, want 2 — the envelope must stay truthful on an empty clue set (#83)", resZ.Excluded)
	}
	// a min-sites bar above the period's peer count → silent (small samples)
	resN, err := svc.Risks(ctx, engine, RiskOutlier, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Month: 5, MinSites: 100})
	if err != nil || resN.Total != 0 {
		t.Fatalf("outlier minSites=100 must be silent, got %+v (err %v)", resN.Clues, err)
	}
	if resN.Excluded != 2 {
		t.Errorf("outlier minSites=100 excluded = %d, want 2 (#83)", resN.Excluded)
	}

	// ---- scenario 1: retired-billing (cross-layer) ----
	res, err = svc.Risks(ctx, engine, RiskRetiredBilling, base)
	if err != nil {
		t.Fatalf("retired-billing: %v", err)
	}
	// RETIRED-SITE (停用, confirmed link) fires; ACTIVE-SITE 在网 silent;
	// PENDING-SITE unlinked-but-pending is scenario 6's side note, not 1's.
	if res.Total != 1 || res.Clues[0].SiteCode != "RETIRED-SITE" || res.Clues[0].MetricValue != 77700 {
		t.Fatalf("retired-billing clues = %+v", res.Clues)
	}
	if res.Clues[0].EvidenceTotal != 1 || len(res.Clues[0].Evidence) != 1 || res.Clues[0].Evidence[0].AmountCents != 77700 {
		t.Errorf("retired-billing anchors = %+v", res.Clues[0].Evidence)
	}

	// ---- scenario 6: unlinked (cross-layer) ----
	res, err = svc.Risks(ctx, engine, RiskUnlinked, base)
	if err != nil {
		t.Fatalf("unlinked: %v", err)
	}
	// every fee code except RETIRED-SITE / ACTIVE-SITE (confirmed) and
	// PENDING-SITE (pending) fires; capped by Limit=5 → top five by total.
	if res.Total < 15 {
		t.Fatalf("unlinked total = %d, want the full uncovered set (>= 15)", res.Total)
	}
	for _, c := range res.Clues {
		if c.SiteCode == "RETIRED-SITE" || c.SiteCode == "ACTIVE-SITE" || c.SiteCode == "PENDING-SITE" {
			t.Errorf("unlinked must skip covered codes, found %s", c.SiteCode)
		}
	}

	// ---- scenario 7: idle-billing (cross-layer, #114) ----
	// The fee face bills BOTH linked sites in 2024-06; the metric face
	// only has records for ACTIVE-SITE — RETIRED-SITE's resource carries
	// zero points in the window, so exactly one clue fires.
	seedMetricPoints(t, db, "mbat_active_site", "res_active_site", "2024-06-10T00:00:00Z", "2024-06-20T00:00:00Z")
	res, err = svc.Risks(ctx, engine, RiskIdleBilling, base)
	if err != nil {
		t.Fatalf("idle-billing: %v", err)
	}
	if res.Total != 1 || res.Clues[0].SiteCode != "RETIRED-SITE" || res.Clues[0].MetricValue != 77700 || res.Clues[0].Month != 6 {
		t.Fatalf("idle-billing clues = %+v, want RETIRED-SITE 2024-06 77700", res.Clues)
	}
	if res.Clues[0].EvidenceTotal != 1 || len(res.Clues[0].Evidence) != 1 {
		t.Errorf("idle-billing anchors = %+v", res.Clues[0].Evidence)
	}
	// the month-6 window sees the June points: ACTIVE-SITE never fires
	for _, c := range res.Clues {
		if c.SiteCode == "ACTIVE-SITE" {
			t.Errorf("idle-billing must skip ACTIVE-SITE (June points exist), found %+v", c)
		}
	}
	// a single-month query on 2024-05: no May metric points anywhere, but
	// the fee face has no linked-site May rows either — honest empty.
	res, err = svc.Risks(ctx, engine, RiskIdleBilling, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
	if err != nil || res.Total != 0 {
		t.Fatalf("idle-billing month-5 = %+v (err %v), want the honest empty (no linked-site May bills)", res.Clues, err)
	}

	// ---- scenario 8: missing-billing (cross-layer, #118) ----
	// UNBILLED-SITE's resource carries June points while the fee face has
	// no rows for the code at all — the mirror asymmetry fires exactly
	// once. ACTIVE-SITE has June points too but IS billed (silent);
	// RETIRED-SITE is outside the universe (retired = not-billed is the
	// correct state) and pointness-free anyway; PENDING-SITE's link is not
	// confirmed (the human has not ruled yet).
	seedMetricPoints(t, db, "mbat_unbilled_site", "res_unbilled_site", "2024-06-10T00:00:00Z")
	res, err = svc.Risks(ctx, engine, RiskMissingBilling, base)
	if err != nil {
		t.Fatalf("missing-billing: %v", err)
	}
	if res.Total != 1 || res.Clues[0].SiteCode != "UNBILLED-SITE" || res.Clues[0].Month != 6 || res.Clues[0].MetricValue != 1 {
		t.Fatalf("missing-billing clues = %+v, want UNBILLED-SITE 2024-06 metricPoints=1", res.Clues)
	}
	if res.Clues[0].MetricLabel != "metricPoints" || res.Clues[0].Threshold != nil {
		t.Errorf("missing-billing clue shape = %+v, want metricPoints + nil threshold", res.Clues[0])
	}
	// the zero-row evidence shape: no fee rows exist behind the clue —
	// EvidenceTotal 0 and an EMPTY (never null, #93) anchor list.
	if res.Clues[0].EvidenceTotal != 0 || len(res.Clues[0].Evidence) != 0 {
		t.Errorf("missing-billing evidence = %+v (total %d), want the zero-row shape", res.Clues[0].Evidence, res.Clues[0].EvidenceTotal)
	}
	// single-month honest empty: no May points anywhere among the linked
	// resources — nothing to cross-check, the answer is an empty list.
	res, err = svc.Risks(ctx, engine, RiskMissingBilling, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
	if err != nil || res.Total != 0 {
		t.Fatalf("missing-billing month-5 = %+v (err %v), want the honest empty (no May points)", res.Clues, err)
	}

	// ---- #119: the human verification loop closes on the real engine ----
	// dismiss the missing-billing clue → the re-run suppresses it and
	// counts it in the envelope (verified stays visible; the default
	// build owns the semantics, this pins the composition end to end).
	if _, err := svc.DecideClue(ctx, domain.RiskClueDecision{
		Scenario: "missing-billing", FeeType: FeeTypeElectricity,
		SiteCode: "UNBILLED-SITE", Year: 2024, Month: 6,
		Status: domain.ClueDecisionDismissed, Note: "季度结算",
	}); err != nil {
		t.Fatalf("decide dismiss: %v", err)
	}
	res, err = svc.Risks(ctx, engine, RiskMissingBilling, base)
	if err != nil {
		t.Fatalf("missing-billing after dismiss: %v", err)
	}
	if res.Total != 0 || res.Dismissed != 1 {
		t.Fatalf("after dismiss = total %d / dismissed %d, want 0 / 1 — the clue must not re-appear", res.Total, res.Dismissed)
	}
	// IncludeDecided brings it back, marked.
	res, err = svc.Risks(ctx, engine, RiskMissingBilling, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, IncludeDecided: true})
	if err != nil {
		t.Fatalf("missing-billing include-decided: %v", err)
	}
	if res.Total != 1 || res.Clues[0].Decision != "dismissed" || res.Clues[0].DecidedAt == "" {
		t.Fatalf("include-decided = %+v, want the marked dismissed clue", res.Clues)
	}

	// ---- determinism: re-run everything, byte-identical clue sets ----
	for _, sc := range []RiskScenario{RiskDuplicateSettlement, RiskLedgerMismatch, RiskSpike, RiskOutlier, RiskMissingBilling} {
		q := base
		if sc == RiskOutlier {
			q.Month = 5
		}
		a, err := svc.Risks(ctx, engine, sc, q)
		if err != nil {
			t.Fatalf("re-run %s: %v", sc, err)
		}
		b, err := svc.Risks(ctx, engine, sc, q)
		if err != nil {
			t.Fatalf("re-run2 %s: %v", sc, err)
		}
		if len(a.Clues) != len(b.Clues) || a.Total != b.Total || a.Excluded != b.Excluded {
			t.Errorf("scenario %s not deterministic across runs", sc)
			continue
		}
		for i := range a.Clues {
			if a.Clues[i].MetricValue != b.Clues[i].MetricValue || a.Clues[i].SiteCode != b.Clues[i].SiteCode {
				t.Errorf("scenario %s clue[%d] drifts: %v vs %v", sc, i, a.Clues[i], b.Clues[i])
			}
		}
	}
}

// TestEveryClueCarriesThresholdLabel (#23's contract): whatever the
// scenario, a clue's thresholdLabel is never empty — a clue whose verdict
// condition is invisible is unexplainable, and "可解释" is the product
// doctrine. The four no-numeric-threshold scenarios (unlinked /
// retired-billing / idle-billing / missing-billing) state their verdict
// conditions as the label; the numeric ones keep their existing labels.
// A future scenario that forgets the label fails HERE, not as an empty
// 括号 in the UI.
func TestEveryClueCarriesThresholdLabel(t *testing.T) {
	svc, db, _ := newService(t)
	ctx := context.Background()
	importRiskFixture(t, svc)
	seedLinkResource(t, db, "res_retired_site", "RETIRED-SITE", "停用")
	seedLinkResource(t, db, "res_active_site", "ACTIVE-SITE", "在网")
	seedLinkResource(t, db, "res_unbilled_site", "UNBILLED-SITE", "在网")
	if err := db.Do(ctx, func(tx *repo.Tx) error {
		return tx.FeeSiteLinks.InsertProposal(ctx, domain.FeeSiteLink{
			ID: "fsl_pending_lbl", FeeSiteCode: "PENDING-SITE", ResourceID: "res_active_site",
			ResourceType: domain.ResourceTypeSite, ResourceKey: "ACTIVE-SITE",
			Grade: domain.LinkGradeFuzzy, Status: domain.LinkStatusPending,
			Source: domain.LinkSourceAuto, CreatedAt: simTSLink,
		})
	}); err != nil {
		t.Fatal(err)
	}
	engine := Query(ctx)
	if !engine.Available() {
		t.Fatal("the duckdb build must report Available")
	}
	scenarios := []RiskScenario{
		RiskRetiredBilling, RiskDuplicateSettlement, RiskLedgerMismatch, RiskSpike,
		RiskOutlier, RiskUnlinked, RiskIdleBilling, RiskMissingBilling, RiskLedgerConsistency,
	}
	checked := 0
	for _, sc := range scenarios {
		res, err := svc.Risks(ctx, engine, sc, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024})
		if err != nil {
			t.Fatalf("%s: %v", sc, err)
		}
		for _, c := range res.Clues {
			checked++
			if strings.TrimSpace(c.ThresholdLabel) == "" {
				t.Errorf("%s clue %s %04d-%02d: thresholdLabel is EMPTY — every clue must carry its verdict condition (#23)", sc, c.SiteCode, c.Year, c.Month)
			}
		}
	}
	if checked == 0 {
		t.Fatal("fixture must produce at least one clue for the contract to mean anything")
	}
	t.Logf("thresholdLabel contract verified on %d clues across %d scenarios", checked, len(scenarios))
}

// TestExcludedDominanceWarns (#21): a zero-clue answer with a dominant
// excluded count is the CANNOT-ANALYZE shape, not the all-clear shape —
// the envelope must say so in BOTH the structured warnings (for UI alert
// bars) and the note (for CLI/MCP readers). The fixture: one site, one
// declared period, TWO batches — the (site, period) pair spans batches,
// so the outlier preset excludes it and nothing participates.
func TestExcludedDominanceWarns(t *testing.T) {
	svc, _, dir := newService(t)
	ctx := context.Background()
	for _, f := range []struct{ name, body string }{
		{"a.csv", "站址编码,账期,电费(元)\nS1,2024-05,100.00\n"},
		{"b.csv", "站址编码,账期,电费(元)\nS1,2024-05,300.00\n"},
	} {
		p := filepath.Join(dir, f.name)
		if err := os.WriteFile(p, []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Import(ctx, ImportOptions{Path: p, FeeType: FeeTypeElectricity, Year: 2024, Month: 5}); err != nil {
			t.Fatalf("import %s: %v", f.name, err)
		}
	}
	engine := Query(ctx)
	if !engine.Available() {
		t.Fatal("the duckdb build must report Available")
	}
	res, err := svc.Risks(ctx, engine, RiskOutlier, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
	if err != nil {
		t.Fatalf("outlier: %v", err)
	}
	if res.Total != 0 || res.Excluded == 0 {
		t.Fatalf("fixture shape: total=%d excluded=%d, want 0 clues with a positive excluded", res.Total, res.Excluded)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("zero clues + excluded > 0 must carry a warning (#21: cannot-analyze must not dress up as all-clear)")
	}
	for _, want := range []string{"0 条线索", "排除", "verify-batches"} {
		if !strings.Contains(res.Warnings[0], want) {
			t.Errorf("warning must contain %q: %q", want, res.Warnings[0])
		}
	}
	if !strings.Contains(res.Note, "【注意】") {
		t.Errorf("note must carry the same advice (CLI/MCP readers read the note): %q", res.Note)
	}

	// The clean shape: no exclusions, zero clues → NO warning (the neutral
	// "analyzed, all clear" must not be polluted by false alarms).
	clean, err := svc.Risks(ctx, engine, RiskOutlier, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Month: 7})
	if err != nil {
		t.Fatalf("outlier clean: %v", err)
	}
	if clean.Total != 0 || clean.Excluded != 0 {
		t.Fatalf("clean shape assumption: total=%d excluded=%d", clean.Total, clean.Excluded)
	}
	if len(clean.Warnings) != 0 {
		t.Fatalf("clean zero-clue answer must not warn, got %v", clean.Warnings)
	}
}
