package repo

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// TestRiskClueDecisionRepo pins the decision repo's contract: the UPSERT
// inserts-then-flips on the clue identity, GetByKey round-trips, the
// batch read serves finishClues' join (absent keys simply missing from
// the map), and List/Count honor the ledger filters and the decided_at
// DESC order.
func TestRiskClueDecisionRepo(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "netops.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	r := db.RiskClueDecisions

	base := domain.RiskClueDecision{
		Scenario: "spike", FeeType: domain.FeeTypeElectricity,
		SiteCode: "S-1", Year: 2024, Month: 5,
		Status: domain.ClueDecisionVerified, Note: "首次核实：属实",
	}
	if _, err := r.Decide(ctx, base); err != nil {
		t.Fatalf("decide: %v", err)
	}

	got, err := r.GetByKey(ctx, base.Scenario, base.FeeType, base.SiteCode, base.Year, base.Month)
	if err != nil {
		t.Fatalf("getByKey: %v", err)
	}
	if got.ID == "" || got.Status != domain.ClueDecisionVerified || got.Note != "首次核实：属实" {
		t.Fatalf("round-trip = %+v", got)
	}
	if got.ID != "rcd_"+got.ID[len("rcd_"):] || len(got.ID) <= len("rcd_") {
		t.Fatalf("id = %q, want the rcd_ prefix", got.ID)
	}

	// the flip: re-deciding the SAME identity flips status + refreshes
	// decided_at, keeps ONE row.
	flip := base
	flip.Status = domain.ClueDecisionDismissed
	flip.Note = "复议：合同+账单双台账，双份合法"
	// explicit clocks: decided_at is RFC3339 SECOND-granular, so two
	// same-second decisions tie and fall to the random-uuid id DESC
	// tiebreak — the ledger order between same-second rows is not a
	// contract. The test pins the ORDERED part (distinct seconds).
	flip.DecidedAt = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	flipped, err := r.Decide(ctx, flip)
	if err != nil {
		t.Fatalf("flip: %v", err)
	}
	if flipped.Status != domain.ClueDecisionDismissed || flipped.Note != "复议：合同+账单双台账，双份合法" {
		t.Fatalf("flipped = %+v", flipped)
	}
	n, err := r.Count(ctx, ClueDecisionFilter{})
	if err != nil || n != 1 {
		t.Fatalf("count after flip = %d (err %v), want 1 — the identity holds one row", n, err)
	}

	// a second, different decision (different scenario = different clue).
	other := domain.RiskClueDecision{
		Scenario: "missing-billing", FeeType: domain.FeeTypeTowerLease,
		SiteCode: "S-2", Year: 2024, Month: 0,
		Status: domain.ClueDecisionVerified,
		// one second AFTER the flip — the ledger's newest-first order is
		// deterministic exactly across distinct seconds.
		DecidedAt: time.Date(2026, 10, 23, 12, 0, 1, 0, time.UTC),
	}
	if _, err := r.Decide(ctx, other); err != nil {
		t.Fatalf("decide other: %v", err)
	}

	// the batch read: present keys map in, absent keys stay absent —
	// never fabricated.
	keys := []ClueKey{
		{Scenario: "spike", FeeType: domain.FeeTypeElectricity, SiteCode: "S-1", Year: 2024, Month: 5},
		{Scenario: "missing-billing", FeeType: domain.FeeTypeTowerLease, SiteCode: "S-2", Year: 2024, Month: 0},
		{Scenario: "unlinked", FeeType: domain.FeeTypeElectricity, SiteCode: "S-9", Year: 2024, Month: 5},
	}
	batch, err := r.ListForKeys(ctx, keys)
	if err != nil {
		t.Fatalf("listForKeys: %v", err)
	}
	if len(batch) != 2 {
		t.Fatalf("batch = %d entries, want 2 (the absent key never appears)", len(batch))
	}
	if d := batch[keys[0]]; d.Status != domain.ClueDecisionDismissed {
		t.Errorf("batch[spike/S-1] = %+v, want the flipped dismissed", d)
	}
	if _, ok := batch[keys[2]]; ok {
		t.Error("the absent key must be missing from the map, never fabricated")
	}
	// duplicate keys are deduplicated (a clue list may repeat identities).
	batch2, err := r.ListForKeys(ctx, append(keys, keys[0], keys[1]))
	if err != nil || len(batch2) != 2 {
		t.Fatalf("dedup batch = %d (err %v), want 2", len(batch2), err)
	}

	// the ledger list: newest first; the filters narrow it.
	all, err := r.List(ctx, ClueDecisionFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 || all[0].SiteCode != "S-2" {
		t.Fatalf("list order = %v, want newest (S-2) first", all)
	}
	onlySpike, err := r.List(ctx, ClueDecisionFilter{Scenario: "spike"})
	if err != nil || len(onlySpike) != 1 || onlySpike[0].SiteCode != "S-1" {
		t.Fatalf("scenario filter = %+v (err %v)", onlySpike, err)
	}
	onlyVerified, err := r.List(ctx, ClueDecisionFilter{Status: string(domain.ClueDecisionVerified)})
	if err != nil || len(onlyVerified) != 1 || onlyVerified[0].SiteCode != "S-2" {
		t.Fatalf("status filter = %+v (err %v)", onlyVerified, err)
	}
	n, err = r.Count(ctx, ClueDecisionFilter{Status: string(domain.ClueDecisionDismissed)})
	if err != nil || n != 1 {
		t.Fatalf("dismissed count = %d (err %v), want 1", n, err)
	}

	// the identity fields are guarded — a decision without them refuses.
	if _, err := r.Decide(ctx, domain.RiskClueDecision{Year: 2024}); err == nil {
		t.Error("identity-less decide must refuse")
	}
}
