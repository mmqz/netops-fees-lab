package sim

// ACC-P2-6 — the before/after run diff: findings matched by per-check
// business keys, connectivity recomputed from the runs' stored
// topology_json snapshots, scoreDelta, and the error paths.

import (
	"context"
	"errors"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
)

// TestRunDiff is ACC-P2-6: a fault changeset (link down + duplicate
// address) adds a CHK-004 component finding and a CHK-001 error, loses
// the pair's connectivity and drops the score; recovery removes them
// again. Unknown and cross-workspace runs yield ErrNotFound; the same
// run twice yields ErrConflict (the API maps that to 400).
func TestRunDiff(t *testing.T) {
	svc, _, res := seededEngine(t)
	ctx := context.Background()
	ws := res.WorkspaceID

	run1, _ := mustRunChecks(t, svc, ws) // baseline (score 95)

	// Fault: CKT-002 (the DEV-002→DEV-003 chain link) down + a duplicate
	// host address on DEV-001/eth0 and DEV-003/eth0.
	if _, err := svc.ApplyOps(ctx, ws, []Op{
		{Op: domain.OpSetLinkStatus, LinkID: "ln-2", Status: "down"},
		configureIface("if-1", "10.0.0.1", 24),
		configureIface("if-4", "10.0.0.1", 24),
	}, "fault", true); err != nil {
		t.Fatalf("ApplyOps(fault): %v", err)
	}
	_, latest := mustRunChecks(t, svc, ws) // auto-run exists; take a manual one for clarity
	if len(latest) == 0 {
		t.Fatalf("no results")
	}
	run2, results2 := mustRunChecks(t, svc, ws)

	// Recovery: link up + both addresses unconfigured.
	if _, err := svc.ApplyOps(ctx, ws, []Op{
		{Op: domain.OpSetLinkStatus, LinkID: "ln-2", Status: "up"},
		{Op: domain.OpConfigInterfaceIPv4, InterfaceID: "if-1"},
		{Op: domain.OpConfigInterfaceIPv4, InterfaceID: "if-4"},
	}, "recover", true); err != nil {
		t.Fatalf("ApplyOps(recover): %v", err)
	}
	run3, _ := mustRunChecks(t, svc, ws)
	if run3.ID == run2.ID || run2.ID == run1.ID {
		t.Fatalf("expected three distinct runs, got %s/%s/%s", run1.ID, run2.ID, run3.ID)
	}
	_ = results2

	// ---- Diff(baseline → after-fault) ----
	diff, err := svc.Diff(ctx, ws, run1.ID, run2.ID)
	if err != nil {
		t.Fatalf("Diff(baseline, after-fault): %v", err)
	}
	if diff.From.RunID != run1.ID || diff.To.RunID != run2.ID {
		t.Fatalf("diff run refs = %s→%s, want %s→%s", diff.From.RunID, diff.To.RunID, run1.ID, run2.ID)
	}
	if diff.From.Score != 95 || diff.To.Score != 90 {
		t.Fatalf("diff run scores = %d→%d, want 95→90", diff.From.Score, diff.To.Score)
	}
	// findingsAdded: the CHK-004 component {DEV-001, DEV-002} and the
	// CHK-001 duplicate address (id-churn-proof matching keys).
	if len(diff.FindingsAdded) != 2 {
		t.Fatalf("findingsAdded = %d, want 2 (CHK-001 + CHK-004): %+v", len(diff.FindingsAdded), diff.FindingsAdded)
	}
	addedCodes := map[string]int{}
	for _, f := range diff.FindingsAdded {
		addedCodes[f.CheckCode]++
	}
	if addedCodes[domain.CheckDuplicateHostAddress] != 1 || addedCodes[domain.CheckPartialPartition] != 1 {
		t.Fatalf("findingsAdded codes = %v, want one CHK-001 and one CHK-004", addedCodes)
	}
	// findingsRemoved: the two now-configured interfaces' CHK-005 rows.
	if len(diff.FindingsRemoved) != 2 {
		t.Fatalf("findingsRemoved = %d, want 2 (the two CHK-005 rows): %+v", len(diff.FindingsRemoved), diff.FindingsRemoved)
	}
	for _, f := range diff.FindingsRemoved {
		if f.CheckCode != domain.CheckUnconfiguredInterface {
			t.Fatalf("findingsRemoved contains %s, want only CHK-005", f.CheckCode)
		}
	}
	// connectivityLost: the CKT-002 cut splits the live universe. With the
	// full §5 predicate (v0.2.1, AUD1-2) the baseline live components are
	// {DEV-001..DEV-014} (the down devices DEV-015/016/019 cut the chain
	// beyond), plus {DEV-017, DEV-018} and {DEV-020, DEV-021}; after the
	// fault the big component splits into {DEV-001, DEV-002} vs
	// {DEV-003..DEV-014} — 2×12 = 24 pairs lost, including the 2-hop pair.
	if len(diff.ConnectivityLost) != 24 {
		t.Fatalf("connectivityLost = %d pairs, want 24: %+v", len(diff.ConnectivityLost), diff.ConnectivityLost)
	}
	if len(diff.ConnectivityGained) != 0 {
		t.Fatalf("connectivityGained = %d, want 0", len(diff.ConnectivityGained))
	}
	lostPair := PairRef{FromDevice: "DEV-001", ToDevice: "DEV-003"}
	seen := false
	for _, pair := range diff.ConnectivityLost {
		if pair == lostPair {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("connectivityLost misses the pair %+v: %+v", lostPair, diff.ConnectivityLost)
	}
	// scoreDelta < 0 (95 → 90).
	if diff.ScoreDelta != -5 {
		t.Fatalf("scoreDelta = %d, want −5", diff.ScoreDelta)
	}

	// ---- Diff(after-fault → recovered) ----
	diff, err = svc.Diff(ctx, ws, run2.ID, run3.ID)
	if err != nil {
		t.Fatalf("Diff(after-fault, recovered): %v", err)
	}
	if len(diff.FindingsRemoved) != 2 {
		t.Fatalf("findingsRemoved = %d, want 2 (CHK-001 + CHK-004): %+v", len(diff.FindingsRemoved), diff.FindingsRemoved)
	}
	if len(diff.FindingsAdded) != 2 {
		t.Fatalf("findingsAdded = %d, want 2 (the two CHK-005 rows back): %+v", len(diff.FindingsAdded), diff.FindingsAdded)
	}
	if len(diff.ConnectivityGained) != 24 {
		t.Fatalf("connectivityGained = %d pairs, want 24: %+v", len(diff.ConnectivityGained), diff.ConnectivityGained)
	}
	seen = false
	for _, pair := range diff.ConnectivityGained {
		if pair == lostPair {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("connectivityGained misses the pair %+v", lostPair)
	}
	if diff.ScoreDelta != 5 {
		t.Fatalf("scoreDelta = %d, want +5", diff.ScoreDelta)
	}

	// ---- error paths ----
	if _, err := svc.Diff(ctx, ws, run1.ID, "run-999"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Diff unknown run: err = %v, want ErrNotFound", err)
	}
	// Same run twice ⇒ domain.ErrConflict (the API layer maps it to 400).
	if _, err := svc.Diff(ctx, ws, run1.ID, run1.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("Diff same run twice: err = %v, want ErrConflict", err)
	}
	// Cross-workspace run behaves like an unknown run of this workspace.
	other, err := svc.Materialize(ctx, "cross-ws", "", nil)
	if err != nil {
		t.Fatalf("Materialize(other): %v", err)
	}
	otherRun, err := svc.RunChecks(ctx, other.WorkspaceID, domain.CheckTriggerManual)
	if err != nil {
		t.Fatalf("RunChecks(other): %v", err)
	}
	if _, err := svc.Diff(ctx, ws, run1.ID, otherRun.RunID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Diff cross-workspace: err = %v, want ErrNotFound", err)
	}
	// Unknown workspace.
	if _, err := svc.Diff(ctx, "ws-none", run1.ID, run2.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Diff unknown workspace: err = %v, want ErrNotFound", err)
	}
}
