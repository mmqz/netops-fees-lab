package fees

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// seedSiteResource inserts one site-like resource for the matcher tests
// (the backing batch row is created once per test).
func seedSiteResource(t *testing.T, db *repo.DB, id, typ, key, name string) {
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
			ID: id, Type: domain.ResourceType(typ), NaturalKey: key, Name: name,
			Attributes: map[string]string{}, FirstBatchID: "bat_link",
			CreatedAt: simTSLink, UpdatedAt: simTSLink,
		})
	})
	if err != nil {
		t.Fatalf("seed resource %s: %v", id, err)
	}
}

var simTSLink = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

// TestLinkScanGrades pins the three-grade matcher over a real Parquet
// import: exact (byte), rule (fold / zero_pad, named), fuzzy (threshold
// + capped candidates, pending proposals), unlinked (the zero-resource
// billing signal's raw material).
func TestLinkScanGrades(t *testing.T) {
	svc, db, dir := newService(t)
	ctx := context.Background()

	// resources: exact hit, fold hit (full-width), zero-pad hit, one
	// near-miss for fuzzy, one far resource.
	// Discrete short codes: the five grades' probes must not fall within
	// each other's 0.8 similarity ball (near-identical 18-digit codes
	// would all be fuzzy candidates of each other — realistic but noisy
	// for pinning grade semantics).
	seedSiteResource(t, db, "res_site_exact", "site", "SITE-001", "局站A")
	seedSiteResource(t, db, "res_site_fold", "site", "SITE-002", "局站B")
	seedSiteResource(t, db, "res_mr_pad", "machine_room", "0003001", "机房C")
	seedSiteResource(t, db, "res_site_fuzzy", "site", "SITE-004", "局站D")
	seedSiteResource(t, db, "res_site_far", "site", "ZZZ999", "局站E")

	// fee codes: exact / fold (full-width + hyphen) / zero-pad / fuzzy
	// near / unlinked.
	src := writeCSV(t, dir, "codes.csv", `站址编码,账期,电费(元)
SITE-001,2024-05,1.00
ＳＩＴＥ－００２,2024-05,2.00
3001,2024-05,3.00
SITE-004X,2024-05,4.00
ZZZ-NOSUCH,2024-05,5.00
`)
	if _, err := svc.Import(ctx, ImportOptions{Path: src, FeeType: FeeTypeElectricity, Year: 2024, Month: 5}); err != nil {
		t.Fatalf("import: %v", err)
	}

	result, err := svc.ScanLinks(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if result.Codes != 5 {
		t.Fatalf("codes = %d, want 5", result.Codes)
	}
	if result.ExactLinks != 1 || result.RuleLinks != 2 {
		t.Fatalf("exact/rule = %d/%d, want 1/2 (fold + zero_pad)", result.ExactLinks, result.RuleLinks)
	}
	if result.FuzzyProposed != 1 {
		t.Fatalf("fuzzy proposed = %d, want 1 (the near-miss site)", result.FuzzyProposed)
	}
	if result.Unlinked != 1 {
		t.Fatalf("unlinked = %d, want 1 (NOSUCHCODE99999)", result.Unlinked)
	}

	// the exact link: confirmed auto, byte-equal key.
	exact, err := db.FeeSiteLinks.ConfirmedByCode(ctx, "SITE-001")
	if err != nil || exact.ResourceID != "res_site_exact" || exact.Grade != domain.LinkGradeExact ||
		exact.Status != domain.LinkStatusConfirmed || exact.Source != domain.LinkSourceAuto {
		t.Fatalf("exact link = %+v (err %v)", exact, err)
	}
	// the fold link: rule recorded, points at SITE-002.
	fold, err := db.FeeSiteLinks.ConfirmedByCode(ctx, "ＳＩＴＥ－００２")
	if err != nil || fold.ResourceID != "res_site_fold" || fold.Grade != domain.LinkGradeRule || fold.Rule != "fold" {
		t.Fatalf("fold link = %+v (err %v)", fold, err)
	}
	// the zero-pad link.
	pad, err := db.FeeSiteLinks.ConfirmedByCode(ctx, "3001")
	if err != nil || pad.ResourceID != "res_mr_pad" || pad.Grade != domain.LinkGradeRule || pad.Rule != "zero_pad" {
		t.Fatalf("pad link = %+v (err %v)", pad, err)
	}
	// the fuzzy proposal: pending, similarity recorded, best candidate.
	pending, err := db.FeeSiteLinks.List(ctx, repo.FeeLinkFilter{Status: domain.LinkStatusPending})
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending = %v (err %v), want 1 proposal", pending, err)
	}
	p := pending[0]
	if p.FeeSiteCode != "SITE-004X" || p.ResourceID != "res_site_fuzzy" ||
		p.Grade != domain.LinkGradeFuzzy || p.Similarity < 0.8 {
		t.Fatalf("proposal = %+v, want code SITE-004X → res_site_fuzzy fuzzy ≥0.8", p)
	}

	// idempotent re-scan: same auto verdicts, no duplicate proposals
	// (the existing pending pair is skipped — FuzzyProposed counts only
	// FRESH proposals).
	result2, err := svc.ScanLinks(ctx)
	if err != nil {
		t.Fatalf("re-scan: %v", err)
	}
	if result2.ExactLinks != 1 || result2.RuleLinks != 2 || result2.Unlinked != 1 || result2.FuzzyProposed != 0 {
		t.Fatalf("re-scan = %+v, want exact 1 / rule 2 / unlinked 1 / fresh fuzzy 0", result2)
	}
	if links, _ := db.FeeSiteLinks.List(ctx, repo.FeeLinkFilter{}); len(links) != 4 {
		t.Fatalf("total links after re-scan = %d, want 4 (auto links replaced, proposals not duplicated)", len(links))
	}
}

// TestLinkHumanConfirmation walks the human channel: confirm a fuzzy
// proposal (source flips human) → the auto scan NEVER overwrites it →
// dismiss leaves audit and frees the code for a fresh candidate.
func TestLinkHumanConfirmation(t *testing.T) {
	svc, db, dir := newService(t)
	ctx := context.Background()

	seedSiteResource(t, db, "res_f1", "site", "23100120000018077", "局站D")
	seedSiteResource(t, db, "res_f2", "site", "23100120000018044", "局站D真身")
	src := writeCSV(t, dir, "f.csv", "站址编码,账期,电费(元)\n2310012000001804X,2024-05,1.00\n")
	if _, err := svc.Import(ctx, ImportOptions{Path: src, FeeType: FeeTypeElectricity, Year: 2024, Month: 5}); err != nil {
		t.Fatalf("import: %v", err)
	}
	if _, err := svc.ScanLinks(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}
	pending, err := db.FeeSiteLinks.List(ctx, repo.FeeLinkFilter{Status: domain.LinkStatusPending})
	if err != nil || len(pending) == 0 {
		t.Fatalf("pending = %v (err %v), want fuzzy proposals", pending, err)
	}

	// confirm the FIRST pending proposal → confirmed human.
	target := pending[0]
	l, err := svc.ConfirmLink(ctx, target.ID)
	if err != nil || l.Status != domain.LinkStatusConfirmed || l.Source != domain.LinkSourceHuman || l.DecidedAt == nil {
		t.Fatalf("confirm = %+v (err %v)", l, err)
	}
	// re-confirm → conflict.
	if _, err := svc.ConfirmLink(ctx, target.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("re-confirm err = %v, want ErrConflict", err)
	}
	// a second confirmed link on the same code → conflict (one code, one site).
	other := pending[len(pending)-1]
	if other.ID != target.ID {
		if _, err := svc.ConfirmLink(ctx, other.ID); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("second confirm on the same code err = %v, want ErrConflict", err)
		}
	}

	// the auto scan respects the human decision: re-scan keeps it.
	if _, err := svc.ScanLinks(ctx); err != nil {
		t.Fatalf("re-scan: %v", err)
	}
	kept, err := db.FeeSiteLinks.ConfirmedByCode(ctx, target.FeeSiteCode)
	if err != nil || kept.ID != target.ID || kept.Source != domain.LinkSourceHuman {
		t.Fatalf("human link after re-scan = %+v (err %v) — the scan must not overwrite it", kept, err)
	}
	// #69 (found by the r19 fees regression): a CONFIRMED pair never
	// re-proposes either — the fuzzy step must skip every decided
	// (code, resource) pair. Before the fix, the re-scan planted a fresh
	// pending row for the very pair the human just confirmed (and
	// ConfirmLink 409s on it, so the duplicate would strand forever).
	afterPending, err := db.FeeSiteLinks.List(ctx, repo.FeeLinkFilter{Status: domain.LinkStatusPending})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range afterPending {
		if p.FeeSiteCode == target.FeeSiteCode && p.ResourceID == target.ResourceID {
			t.Fatalf("re-scan re-proposed the confirmed pair %s → %s — PairDecided must cover confirmed rows",
				p.FeeSiteCode, p.ResourceID)
		}
	}

	// dismiss journey: dismiss a fresh pending proposal → dismissed,
	// audit kept, code free.
	fresh := writeCSV(t, dir, "f2.csv", "站址编码,账期,电费(元)\n231099XX0000180ZZ,2024-05,2.00\n")
	_ = fresh
	// (plant a pending proposal directly — the dismiss semantics are the
	// target here, not the matcher again)
	err = db.Do(ctx, func(tx *repo.Tx) error {
		return tx.FeeSiteLinks.InsertProposal(ctx, domain.FeeSiteLink{
			ID: "fsl_dismiss", FeeSiteCode: "231099XX0000180ZZ", ResourceID: "res_f2",
			ResourceType: domain.ResourceTypeSite, ResourceKey: "23100120000018044",
			Grade: domain.LinkGradeFuzzy, Similarity: 0.9,
			Status: domain.LinkStatusPending, Source: domain.LinkSourceAuto, CreatedAt: simTSLink,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	d, err := svc.DismissLink(ctx, "fsl_dismiss")
	if err != nil || d.Status != domain.LinkStatusDismissed || d.DecidedAt == nil {
		t.Fatalf("dismiss = %+v (err %v)", d, err)
	}
	// re-dismiss → conflict (terminal state, the renames doctrine).
	if _, err := svc.DismissLink(ctx, "fsl_dismiss"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("re-dismiss err = %v, want ErrConflict", err)
	}
	// the code is free: the dismissed pair does not block a fresh
	// proposal on the same code.
	err = db.Do(ctx, func(tx *repo.Tx) error {
		return tx.FeeSiteLinks.InsertProposal(ctx, domain.FeeSiteLink{
			ID: "fsl_fresh", FeeSiteCode: "231099XX0000180ZZ", ResourceID: "res_f1",
			ResourceType: domain.ResourceTypeSite, ResourceKey: "23100120000018077",
			Grade: domain.LinkGradeFuzzy, Similarity: 0.85,
			Status: domain.LinkStatusPending, Source: domain.LinkSourceAuto, CreatedAt: simTSLink,
		})
	})
	if err != nil {
		t.Fatalf("a fresh proposal on a dismissed code must be insertable: %v", err)
	}
}

// TestLinkScanRollbackDropout: a rolled-back fee batch's files vanish →
// its codes drop out of the scan universe → the auto link disappears on
// the next scan (the code-space fact follows the live data).
func TestLinkScanRollbackDropout(t *testing.T) {
	svc, db, dir := newService(t)
	ctx := context.Background()
	seedSiteResource(t, db, "res_rb", "site", "RB001", "局站RB")
	src := writeCSV(t, dir, "rb.csv", "站址编码,账期,电费(元)\nRB001,2024-05,1.00\n")
	sum, err := svc.Import(ctx, ImportOptions{Path: src, FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if _, err := svc.ScanLinks(ctx); err != nil {
		t.Fatal(err)
	}
	if l, err := db.FeeSiteLinks.ConfirmedByCode(ctx, "RB001"); err != nil || l.ResourceID != "res_rb" {
		t.Fatalf("link before rollback = %+v (err %v)", l, err)
	}
	if _, err := svc.Rollback(ctx, sum.BatchID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ScanLinks(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.FeeSiteLinks.ConfirmedByCode(ctx, "RB001"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("the auto link must drop with the rolled-back data, got %v", err)
	}
	// the parquet tree is pruned (no files → no codes).
	if _, err := os.Stat(filepath.Join(dir, "analysis", "fees")); !os.IsNotExist(err) {
		t.Logf("analysis tree state after full rollback: %v", err)
	}
}

// TestSimilarityAtLeastEquivalence pins the #65 P1-b fast path's central
// property: similarityAtLeast accepts EXACTLY the pairs the naive
// Similarity >= threshold comparison accepts, and returns the SAME
// similarity value — the length pre-filter, the banded DP and the early
// abort are lossless cuts, never behavior changes. The corpus mixes
// realistic telecom shapes (15-18 digit codes, Chinese name keys) with
// adversarial boundaries (equal lengths, one-off lengths, threshold-edge
// distances, empty keys).
func TestSimilarityAtLeastEquivalence(t *testing.T) {
	corpus := []string{
		"", "0123456789012345", "012345678901234A", "0123456789ABCDEF",
		"2301010ABCDE00001", "2301010abcde00001", "２３０１ＡＢＣ", "2301",
		"动力机房2", "动力机房2新", "学府路", "学府路2", "SITE-001", "site-001",
		"12345", "123456", "1234567", "aaaaaaaaaaaaaaaa", "aaaaaaaaaaaaaaaaa",
		"汉", "汉语", "语汉", "01234567890123456", "01234567890123",
	}
	for _, a := range corpus {
		for _, b := range corpus {
			naive := Similarity(a, b)
			wantOK := naive >= FUZZY_THRESHOLD
			got, ok := similarityAtLeast(a, b, FUZZY_THRESHOLD)
			if ok != wantOK {
				t.Errorf("similarityAtLeast(%q, %q) ok = %v, naive %v → %v", a, b, ok, naive, wantOK)
				continue
			}
			if ok && got != naive {
				t.Errorf("similarityAtLeast(%q, %q) = %v, want the naive value %v", a, b, got, naive)
			}
		}
	}
}

// TestFuzzyScanPerformance pins the #65 acceptance bound: a 10,000-code ×
// 1,000-candidate topFuzzy sweep finishes under the budget on this
// class of machine (the naive path extrapolated to 15.5s+ at the same
// shape; the pre-filter + banded DP brings it under a second). The
// generated corpus is deliberately adversarial for the length cut: all
// candidates share the codes' length class, so only the DP-side cuts
// (band + early abort) can deliver the speedup.
func TestFuzzyScanPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("performance smoke skipped in -short mode")
	}
	if raceEnabled {
		t.Skip("wall-time bounds are meaningless under the race detector's instrumentation")
	}
	const nCodes, nCands = 10_000, 1_000
	cands := make([]resourceCandidate, nCands)
	for i := range cands {
		cands[i] = resourceCandidate{id: "res_" + strconv.Itoa(i), key: codeOf(i, 18)}
	}
	// The scan's real shape (#65 P1-b): the decide phase runs a worker
	// pool over the codes — this test mirrors it exactly so the measured
	// wall time is the time a ScanLinks caller actually waits.
	start := time.Now()
	proposed := make([]bool, nCodes)
	var (
		wg   sync.WaitGroup
		next atomic.Int32
	)
	workers := runtime.GOMAXPROCS(0)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= nCodes {
					return
				}
				proposed[i] = len(topFuzzy(cands, codeOf(i*7+3, 18))) > 0
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	n := 0
	for _, p := range proposed {
		if p {
			n++
		}
	}
	if elapsed > 2*time.Second {
		t.Fatalf("10k×1k parallel fuzzy sweep took %v, want < 2s (issue #65 acceptance; naive extrapolation was 15.5s)", elapsed)
	}
	t.Logf("10k codes × 1k same-length-class candidates (parallel, %d workers): %v, %d codes had proposals", workers, elapsed, n)
	if n == 0 {
		t.Fatalf("no proposals at all — the corpus must contain near-matches for the sweep to be meaningful")
	}
}

// codeOf renders a deterministic 18-character site code of n (digits +
// one letter block): codes of nearby n differ in a few characters — the
// realistic near-miss density of telecom site-code universes.
func codeOf(n int, length int) string {
	const alphabet = "0123456789ABCDEFGH"
	b := make([]byte, length)
	for i := 0; i < length; i++ {
		b[i] = alphabet[uint(n+i*3+((n>>uint(i%8))%7))%uint(len(alphabet))]
	}
	// force a shared prefix (realistic regional prefixes defeat no cut —
	// the band does the work)
	b[0], b[1], b[2] = '2', '3', '0'
	return string(b)
}

// TestSweepAutoNotInRepo pins the SQL-pushed dropout sweep's semantics
// directly (#65 P1-b): auto links of dropped codes die (confirmed AND
// pending), human-confirmed links and dismissed proposals are decisions
// and survive, the >400-code chunking boundary behaves identically, and
// an empty keep-set sweeps every auto non-dismissed link.
func TestSweepAutoNotInRepo(t *testing.T) {
	svc, db, _ := newService(t)
	ctx := context.Background()
	seedSiteResource(t, db, "res_x", "site", "X-KEY", "局站X")
	seedLink := func(id, code string, status domain.LinkStatus, source domain.LinkSource) {
		t.Helper()
		if err := db.Do(ctx, func(tx *repo.Tx) error {
			return tx.FeeSiteLinks.InsertProposal(ctx, domain.FeeSiteLink{
				ID: id, FeeSiteCode: code, ResourceID: "res_x", ResourceType: domain.ResourceTypeSite,
				ResourceKey: code, Grade: domain.LinkGradeFuzzy, Status: status, Source: source,
				CreatedAt: simTSLink,
			})
		}); err != nil {
			t.Fatalf("seed link %s: %v", id, err)
		}
	}
	seedLink("fsl_auto_kept", "KEPT1", domain.LinkStatusConfirmed, domain.LinkSourceAuto)
	seedLink("fsl_auto_dropped", "GONE1", domain.LinkStatusConfirmed, domain.LinkSourceAuto)
	seedLink("fsl_auto_pending_dropped", "GONE2", domain.LinkStatusPending, domain.LinkSourceAuto)
	seedLink("fsl_human_dropped", "GONE3", domain.LinkStatusConfirmed, domain.LinkSourceHuman)
	seedLink("fsl_dismissed_dropped", "GONE4", domain.LinkStatusDismissed, domain.LinkSourceAuto)
	// 500 dropped codes: crosses the 400-parameter chunk boundary.
	for i := 0; i < 500; i++ {
		seedLink(domain.NewID("fsl"), fmt.Sprintf("BULK%04d", i), domain.LinkStatusConfirmed, domain.LinkSourceAuto)
	}

	keep := []string{"KEPT1"}
	for i := 0; i < 400; i++ { // pad the keep-set across the chunk boundary too
		keep = append(keep, fmt.Sprintf("PAD%04d", i))
	}
	var n int64
	if err := db.Do(ctx, func(tx *repo.Tx) error {
		var err error
		n, err = tx.FeeSiteLinks.DeleteAutoNotIn(ctx, keep)
		return err
	}); err != nil {
		t.Fatalf("DeleteAutoNotIn: %v", err)
	}
	if n != 502 { // 500 bulk + GONE1 + GONE2; the human-confirmed and dismissed rows are decisions and stay
		t.Fatalf("DeleteAutoNotIn deleted %d, want 502 (auto confirmed+pending of dropped codes only)", n)
	}
	for _, tc := range []struct {
		id       string
		wantGone bool
	}{
		{"fsl_auto_kept", false},
		{"fsl_auto_dropped", true},
		{"fsl_auto_pending_dropped", true},
		{"fsl_human_dropped", false}, // a decision outlives the data
		{"fsl_dismissed_dropped", false},
	} {
		_, err := db.FeeSiteLinks.GetByID(ctx, tc.id)
		if tc.wantGone && !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("link %s after sweep = %v, want gone", tc.id, err)
		}
		if !tc.wantGone && err != nil {
			t.Errorf("link %s must survive the sweep: %v", tc.id, err)
		}
	}

	// empty keep-set: everything auto non-dismissed goes, decisions stay.
	if err := db.Do(ctx, func(tx *repo.Tx) error {
		var err error
		n, err = tx.FeeSiteLinks.DeleteAutoNotIn(ctx, nil)
		return err
	}); err != nil {
		t.Fatalf("DeleteAutoNotIn(empty): %v", err)
	}
	if n != 1 { // only fsl_auto_kept remains auto+live
		t.Fatalf("empty-keep sweep deleted %d, want 1", n)
	}
	if _, err := db.FeeSiteLinks.GetByID(ctx, "fsl_human_dropped"); err != nil {
		t.Errorf("human link must survive the empty-universe sweep: %v", err)
	}
	_ = svc // the service handle is part of the fixture contract; the sweep is repo-level
}
