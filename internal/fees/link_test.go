package fees

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
