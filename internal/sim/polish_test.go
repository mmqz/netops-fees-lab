package sim

// v0.2.1 polish tests of the engine: empty-ops rejection (AUD1-3),
// duplicate-pair create_link rejection + parallel-link checkpoint restore
// (AUD1-1), the full-predicate diff connectivity (AUD1-2, incl. the legacy
// snapshot back-compat) and the concurrent-writer stress of the storage
// hardening (AUD1-4).

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// ---- FX1-A / AUD1-3: empty ops must never create a 0-op changeset ----

// TestApplyOpsEmptyOpsRejected pins the engine boundary: DecodeOps (the
// HTTP/MCP/CLI entry) and ApplyOps (direct callers) both reject an empty ops
// array with domain.ErrInvalidOp BEFORE any changeset row is written — an
// applied 0-op changeset would brick changeset-tier undo (Undo("") targets
// it and fails "no op rows" while 409-blocking every older changeset).
func TestApplyOpsEmptyOpsRejected(t *testing.T) {
	svc, db, res := seededEngine(t)
	ctx := context.Background()
	ws := res.WorkspaceID
	hashBefore := mustHash(t, svc, ws)
	csBefore, err := db.ChangeSets.CountByWorkspace(ctx, ws)
	if err != nil {
		t.Fatalf("count changesets: %v", err)
	}

	// DecodeOps: the raw {"ops":[]} body of every external boundary.
	_, err = DecodeOps([]byte(`{"ops":[],"description":"x"}`))
	if !errors.Is(err, domain.ErrInvalidOp) {
		t.Fatalf("DecodeOps({\"ops\":[]}): err = %v, want domain.ErrInvalidOp", err)
	}
	if !containsSubstring(err.Error(), "ops must contain at least one operation") {
		t.Fatalf("DecodeOps error message = %q, want the \"ops must contain at least one operation\" wording", err.Error())
	}

	// ApplyOps: the engine entry for direct (already-typed) callers.
	_, err = svc.ApplyOps(ctx, ws, nil, "", true)
	if !errors.Is(err, domain.ErrInvalidOp) {
		t.Fatalf("ApplyOps(nil): err = %v, want domain.ErrInvalidOp", err)
	}
	if !containsSubstring(err.Error(), "ops must contain at least one operation") {
		t.Fatalf("ApplyOps error message = %q, want the shared wording", err.Error())
	}

	// Nothing was written: hash and changeset count unchanged.
	if hash := mustHash(t, svc, ws); hash != hashBefore {
		t.Fatalf("workspace hash changed on rejected empty ops: %s → %s", hashBefore, hash)
	}
	csAfter, err := db.ChangeSets.CountByWorkspace(ctx, ws)
	if err != nil {
		t.Fatalf("count changesets after: %v", err)
	}
	if csAfter != csBefore {
		t.Fatalf("changeset count %d → %d, want unchanged (no 0-op changeset row)", csBefore, csAfter)
	}
	// Undo still works on the (unchanged) changeset history: the trap of
	// AUD1-3 was that an empty-ops changeset would block everything — verify
	// the workspace still has no changesets to undo at all here (csBefore=0).
	if csBefore != 0 {
		t.Fatalf("freshly materialized workspace should have no changesets, has %d", csBefore)
	}
}

// ---- FX1-B / AUD1-1: duplicate-pair create_link + parallel-link restore ----

// TestApplyOpsDuplicateLinkPair pins the engine strengthening: a second
// create_link between an already-linked interface pair fails with
// domain.ErrOpFailed ("link already exists between interfaces if-X and if-Y"),
// the whole changeset rolls back and is recorded as failed with 0 applied
// ops.
func TestApplyOpsDuplicateLinkPair(t *testing.T) {
	svc, db, res := seededEngine(t)
	ctx := context.Background()
	ws := res.WorkspaceID
	hashBefore := mustHash(t, svc, ws)

	// ln-1 connects if-1 (DEV-001/eth0) and if-2 (DEV-002/eth0).
	ln1, err := db.SimLinks.GetByID(ctx, "ln-1")
	if err != nil {
		t.Fatalf("GetByID(ln-1): %v", err)
	}
	pair := [2]string{ln1.AInterfaceID, ln1.ZInterfaceID}

	_, err = svc.ApplyOps(ctx, ws, []Op{{
		Op: domain.OpCreateLink, AInterfaceID: pair[0], ZInterfaceID: pair[1], Label: "PARALLEL",
	}}, "parallel link", false)
	if !errors.Is(err, domain.ErrOpFailed) {
		t.Fatalf("ApplyOps(duplicate pair): err = %v, want domain.ErrOpFailed", err)
	}
	var opErr *OpError
	if !errors.As(err, &opErr) {
		t.Fatalf("ApplyOps(duplicate pair): err = %T, want *sim.OpError", err)
	}
	wantMsg := fmt.Sprintf("link already exists between interfaces %s and %s", pair[0], pair[1])
	if opErr.Message != wantMsg {
		t.Fatalf("op message = %q, want %q", opErr.Message, wantMsg)
	}

	// State untouched: hash identical, exactly one link on the pair.
	if hash := mustHash(t, svc, ws); hash != hashBefore {
		t.Fatalf("workspace hash changed on the rejected duplicate link: %s → %s", hashBefore, hash)
	}
	touching, err := db.SimLinks.ListTouchingInterface(ctx, pair[0])
	if err != nil {
		t.Fatalf("ListTouchingInterface(%s): %v", pair[0], err)
	}
	if len(touching) != 1 {
		t.Fatalf("links touching %s = %d, want 1 (the rejected create rolled back)", pair[0], len(touching))
	}

	// The failed changeset was recorded: status failed, ops_applied 0, the
	// single op 'failed', no inverse stored.
	css, _, err := db.ChangeSets.ListByWorkspace(ctx, ws, 1, 0)
	if err != nil || len(css) != 1 {
		t.Fatalf("changeset list = %+v (%v), want the failed changeset", css, err)
	}
	failed := css[0]
	if failed.Status != domain.ChangeSetStatusFailed || failed.OpsApplied != 0 {
		t.Fatalf("failed changeset = %+v, want status failed / opsApplied 0", failed)
	}
	rows, err := db.ChangeSets.GetOps(ctx, failed.ID)
	if err != nil || len(rows) != 1 || rows[0].OpResult != domain.OpResultFailed {
		t.Fatalf("failed changeset ops = %+v (%v), want one 'failed' row", rows, err)
	}
	if rows[0].InverseJSON != "" {
		t.Fatalf("failed changeset must store no inverse, got %q", rows[0].InverseJSON)
	}

	// The same pair REVERSED is rejected too (A/Z swapped).
	_, err = svc.ApplyOps(ctx, ws, []Op{{
		Op: domain.OpCreateLink, AInterfaceID: pair[1], ZInterfaceID: pair[0], Label: "REVERSED",
	}}, "reversed pair", false)
	if !errors.Is(err, domain.ErrOpFailed) {
		t.Fatalf("ApplyOps(reversed duplicate pair): err = %v, want domain.ErrOpFailed", err)
	}
	if touchCountOf(t, db, pair[0]) != 1 {
		t.Fatalf("link count changed after the reversed duplicate was rejected")
	}
}

// insertParallelLink writes a second link between ln-1's interface pair
// directly through the repo — the LEGACY shape the engine now rejects at
// create_link but older databases (and direct SQL) can still hold. The
// checkpoint restore must handle it hash-exactly (AUD1-1 defense-in-depth).
func insertParallelLink(t *testing.T, svc *Service, ws string, ln domain.SimLink, label string) domain.SimLink {
	t.Helper()
	now := svc.clock()
	parallel := domain.SimLink{
		ID: "ln-900", WorkspaceID: ws,
		AInterfaceID: ln.AInterfaceID, ZInterfaceID: ln.ZInterfaceID,
		Status: domain.EntityStatusUp, Label: label,
		CreatedAt: now, UpdatedAt: now,
	}
	err := svc.db.Do(context.Background(), func(tx *repo.Tx) error {
		return tx.SimLinks.Insert(context.Background(), parallel)
	})
	if err != nil {
		t.Fatalf("insert parallel link: %v", err)
	}
	return parallel
}

// TestCheckpointRestoreParallelLinks pins the multiplicity-aware restore
// (AUD1-1): a workspace holding a PARALLEL link (same interface pair) —
// crafted by direct repo inserts because the engine now rejects the op —
// restores hash-exactly in BOTH directions: the surplus link is deleted
// (current has more than the snapshot), and a deleted parallel link is
// re-created (the snapshot has more than the current state).
func TestCheckpointRestoreParallelLinks(t *testing.T) {
	svc, db, res := seededEngine(t)
	ctx := context.Background()
	ws := res.WorkspaceID

	ln1, err := db.SimLinks.GetByID(ctx, "ln-1")
	if err != nil {
		t.Fatalf("GetByID(ln-1): %v", err)
	}

	// ---- direction A: CURRENT has the surplus parallel link ----
	baseHash := mustHash(t, svc, ws)
	cpA, err := svc.CreateCheckpoint(ctx, ws, "before-parallel")
	if err != nil {
		t.Fatalf("CreateCheckpoint(A): %v", err)
	}
	insertParallelLink(t, svc, ws, ln1, "PARALLEL")
	if hash := mustHash(t, svc, ws); hash == baseHash {
		t.Fatalf("the parallel link must change the state hash")
	}
	restoredA, err := svc.RestoreCheckpoint(ctx, ws, cpA.CheckpointID, false)
	if err != nil {
		t.Fatalf("RestoreCheckpoint(surplus parallel): %v", err)
	}
	if restoredA.WorkspaceHash != cpA.WorkspaceHash {
		t.Fatalf("restore A: workspace hash %s != checkpoint hash %s (surplus link must be deleted)",
			restoredA.WorkspaceHash, cpA.WorkspaceHash)
	}
	if touchCountOf(t, db, ln1.AInterfaceID) != 1 {
		t.Fatalf("restore A: links on the pair = %d, want 1 (the surplus link was deleted)",
			touchCountOf(t, db, ln1.AInterfaceID))
	}

	// ---- direction B: SNAPSHOT has the parallel link, current lost it ----
	// A second parallel link with the SAME label as ln-1 — the pure
	// multiplicity case (identical restore-diff key, count 2 vs 1).
	insertParallelLink(t, svc, ws, ln1, ln1.Label)
	hashWithParallel := mustHash(t, svc, ws)
	cpB, err := svc.CreateCheckpoint(ctx, ws, "with-parallel")
	if err != nil {
		t.Fatalf("CreateCheckpoint(B): %v", err)
	}
	if cpB.WorkspaceHash != hashWithParallel {
		t.Fatalf("checkpoint B hash = %s, want the parallel-state hash %s", cpB.WorkspaceHash, hashWithParallel)
	}
	// Delete BOTH links of the pair down to zero through the engine, then
	// restore: two creates must rebuild the multiplicity (one of them
	// duplicating the pair — only legal on the restore path).
	if _, err := svc.ApplyOps(ctx, ws, []Op{
		{Op: domain.OpDeleteLink, LinkID: "ln-1"},
		{Op: domain.OpDeleteLink, LinkID: "ln-900"},
	}, "drop the pair", false); err != nil {
		t.Fatalf("ApplyOps(delete pair): %v", err)
	}
	if touchCountOf(t, db, ln1.AInterfaceID) != 0 {
		t.Fatalf("pair must be empty before the restore")
	}
	restoredB, err := svc.RestoreCheckpoint(ctx, ws, cpB.CheckpointID, false)
	if err != nil {
		t.Fatalf("RestoreCheckpoint(deficit parallel): %v", err)
	}
	if restoredB.WorkspaceHash != hashWithParallel {
		t.Fatalf("restore B: workspace hash %s != checkpoint hash %s (the parallel multiplicity must be rebuilt)",
			restoredB.WorkspaceHash, hashWithParallel)
	}
	if got := touchCountOf(t, db, ln1.AInterfaceID); got != 2 {
		t.Fatalf("restore B: links on the pair = %d, want 2 (both occurrences rebuilt)", got)
	}
}

// touchCountOf counts the links touching ifaceID through the repo handle.
func touchCountOf(t *testing.T, db *repo.DB, ifaceID string) int {
	t.Helper()
	links, err := db.SimLinks.ListTouchingInterface(context.Background(), ifaceID)
	if err != nil {
		t.Fatalf("ListTouchingInterface(%s): %v", ifaceID, err)
	}
	return len(links)
}

// ---- FX1-C / AUD1-2: full-predicate diff connectivity ----

// TestRunDiffEntityStatusFaults pins the v0.2.1 diff semantics: the
// connectivityLost/Gained pairs are computed with the FULL §5 predicate
// (link up AND both interfaces up AND both devices up), so
// set_device_status / set_interface_status faults surface as lost pairs —
// exactly like link faults — and undo restores them. The seeded baseline
// live components are {DEV-001..DEV-014}, {DEV-017, DEV-018} and
// {DEV-020, DEV-021} (down devices DEV-015/016/019 cut the chain).
func TestRunDiffEntityStatusFaults(t *testing.T) {
	svc, _, res := seededEngine(t)
	ctx := context.Background()
	ws := res.WorkspaceID
	run1, _ := mustRunChecks(t, svc, ws)

	// ---- device fault: DEV-005 (vd-5) down ----
	applyOK(t, svc, ws, []Op{{Op: domain.OpSetDeviceStatus, DeviceID: "vd-5", Status: "down"}})
	run2, _ := mustRunChecks(t, svc, ws)
	diff, err := svc.Diff(ctx, ws, run1.ID, run2.ID)
	if err != nil {
		t.Fatalf("Diff(device fault): %v", err)
	}
	if len(diff.ConnectivityLost) == 0 {
		t.Fatalf("device-down fault must lose connectivity pairs (was the empty connectivityLost of AUD1-2)")
	}
	if len(diff.ConnectivityGained) != 0 {
		t.Fatalf("connectivityGained = %d, want 0", len(diff.ConnectivityGained))
	}
	mustContainPair(t, diff.ConnectivityLost, "DEV-001", "DEV-005")
	mustContainPair(t, diff.ConnectivityLost, "DEV-005", "DEV-014")
	if len(diff.ConnectivityLost) != 49 {
		t.Fatalf("device fault connectivityLost = %d pairs, want 49: %+v", len(diff.ConnectivityLost), diff.ConnectivityLost)
	}

	// Undo restores the pairs.
	if _, err := svc.Undo(ctx, ws, "", false); err != nil {
		t.Fatalf("Undo(device fault): %v", err)
	}
	run3, _ := mustRunChecks(t, svc, ws)
	diff, err = svc.Diff(ctx, ws, run2.ID, run3.ID)
	if err != nil {
		t.Fatalf("Diff(device recovery): %v", err)
	}
	if len(diff.ConnectivityLost) != 0 || len(diff.ConnectivityGained) != 49 {
		t.Fatalf("device recovery lost/gained = %d/%d, want 0/49", len(diff.ConnectivityLost), len(diff.ConnectivityGained))
	}
	mustContainPair(t, diff.ConnectivityGained, "DEV-001", "DEV-005")

	// ---- interface fault: DEV-005/eth0 (if-8) down cuts only the D4—D5
	// edge (DEV-005 stays reachable via DEV-006) — 4×10 cross pairs lost.
	applyOK(t, svc, ws, []Op{{Op: domain.OpSetInterfaceStatus, InterfaceID: "if-8", Status: "down"}})
	run4, _ := mustRunChecks(t, svc, ws)
	diff, err = svc.Diff(ctx, ws, run3.ID, run4.ID)
	if err != nil {
		t.Fatalf("Diff(interface fault): %v", err)
	}
	if len(diff.ConnectivityLost) != 40 {
		t.Fatalf("interface fault connectivityLost = %d pairs, want 40: %+v", len(diff.ConnectivityLost), diff.ConnectivityLost)
	}
	mustContainPair(t, diff.ConnectivityLost, "DEV-001", "DEV-005")
	// DEV-005—DEV-006 stays connected through the live eth1 edge.
	mustNotContainPair(t, diff.ConnectivityLost, "DEV-005", "DEV-006")

	// Undo restores it as well.
	if _, err := svc.Undo(ctx, ws, "", false); err != nil {
		t.Fatalf("Undo(interface fault): %v", err)
	}
	run5, _ := mustRunChecks(t, svc, ws)
	diff, err = svc.Diff(ctx, ws, run4.ID, run5.ID)
	if err != nil {
		t.Fatalf("Diff(interface recovery): %v", err)
	}
	if len(diff.ConnectivityGained) != 40 {
		t.Fatalf("interface recovery connectivityGained = %d, want 40", len(diff.ConnectivityGained))
	}
	mustContainPair(t, diff.ConnectivityGained, "DEV-001", "DEV-005")
}

// TestSnapshotReachablePairsLegacyFormat pins the back-compat of the
// full-predicate extension: a v0.2.0 six-field snapshot row (no endpoint
// statuses) is read with missing = up, so legacy diffs behave exactly as
// before; a v0.2.1 row with a down endpoint kills the edge.
func TestSnapshotReachablePairsLegacyFormat(t *testing.T) {
	legacyUp := `[{"aDevice":"A","aInterface":"eth0","zDevice":"B","zInterface":"eth0","status":"up","label":"L"}]`
	legacyDown := `[{"aDevice":"A","aInterface":"eth0","zDevice":"B","zInterface":"eth0","status":"down","label":"L"}]`
	newUp := `[{"aDevice":"A","aInterface":"eth0","zDevice":"B","zInterface":"eth0","status":"up","label":"L","aDeviceStatus":"up","aInterfaceStatus":"up","zDeviceStatus":"up","zInterfaceStatus":"up"}]`
	newADown := `[{"aDevice":"A","aInterface":"eth0","zDevice":"B","zInterface":"eth0","status":"up","label":"L","aDeviceStatus":"down","aInterfaceStatus":"up","zDeviceStatus":"up","zInterfaceStatus":"up"}]`
	newIfaceDown := `[{"aDevice":"A","aInterface":"eth0","zDevice":"B","zInterface":"eth0","status":"up","label":"L","aDeviceStatus":"up","aInterfaceStatus":"down","zDeviceStatus":"up","zInterfaceStatus":"up"}]`

	cases := []struct {
		name string
		raw  string
		want int // number of reachable pairs
	}{
		{"legacy link up (no statuses)", legacyUp, 1},
		{"legacy link down", legacyDown, 0},
		{"new format all up", newUp, 1},
		{"new format a-device down", newADown, 0},
		{"new format a-interface down", newIfaceDown, 0},
	}
	for _, tc := range cases {
		pairs, err := snapshotReachablePairs(tc.raw)
		if err != nil {
			t.Fatalf("%s: snapshotReachablePairs: %v", tc.name, err)
		}
		if len(pairs) != tc.want {
			t.Fatalf("%s: reachable pairs = %d, want %d", tc.name, len(pairs), tc.want)
		}
	}

	// Legacy rows parse back with nil endpoint statuses (missing = up).
	links, err := parseTopoLinks(legacyUp)
	if err != nil || len(links) != 1 {
		t.Fatalf("parseTopoLinks(legacy): %v / %d rows", err, len(links))
	}
	if links[0].ADeviceStatus != nil || links[0].AInterfaceStatus != nil ||
		links[0].ZDeviceStatus != nil || links[0].ZInterfaceStatus != nil {
		t.Fatalf("legacy row statuses = %v/%v/%v/%v, want all nil", links[0].ADeviceStatus,
			links[0].AInterfaceStatus, links[0].ZDeviceStatus, links[0].ZInterfaceStatus)
	}
	// New rows parse back with the stored statuses.
	links, err = parseTopoLinks(newADown)
	if err != nil || len(links) != 1 {
		t.Fatalf("parseTopoLinks(new): %v / %d rows", err, len(links))
	}
	if links[0].ADeviceStatus == nil || *links[0].ADeviceStatus != "down" {
		t.Fatalf("new row aDeviceStatus = %v, want \"down\"", links[0].ADeviceStatus)
	}
}

// mustContainPair asserts the unordered pair is in the list.
func mustContainPair(t *testing.T, pairs []PairRef, a, b string) {
	t.Helper()
	for _, p := range pairs {
		if (p.FromDevice == a && p.ToDevice == b) || (p.FromDevice == b && p.ToDevice == a) {
			return
		}
	}
	t.Fatalf("pair %s↔%s missing from %+v", a, b, pairs)
}

// mustNotContainPair asserts the unordered pair is NOT in the list.
func mustNotContainPair(t *testing.T, pairs []PairRef, a, b string) {
	t.Helper()
	for _, p := range pairs {
		if (p.FromDevice == a && p.ToDevice == b) || (p.FromDevice == b && p.ToDevice == a) {
			t.Fatalf("pair %s↔%s must not be in %+v", a, b, pairs)
		}
	}
}

// ---- FX1-D / AUD1-4: concurrent engine workload ----

// TestConcurrentEngineWorkload runs the exact race of AUD1-4 at the engine
// level: 8 goroutines alternate ApplyOps (read→write inside one Do), manual
// RunChecks (loadState read → sequence UPDATE — the deferred-BEGIN upgrade
// victim) and Topology reads on ONE workspace. Every call must return nil —
// with the unbounded pool + deferred BEGIN of v0.2.0 this lost runs to
// SQLITE_BUSY. The per-goroutine device ends UP again, so the final state
// hash must equal the materialization baseline and the final checks run
// must reproduce the baseline score. NO -race (TASK_PROTOCOL §5).
func TestConcurrentEngineWorkload(t *testing.T) {
	svc, _, res := seededEngine(t)
	ctx := context.Background()
	ws := res.WorkspaceID
	baselineHash := mustHash(t, svc, ws)
	baseline, _ := mustRunChecks(t, svc, ws)

	const workers = 8
	const iterations = 10

	var wg sync.WaitGroup
	errCh := make(chan error, workers*iterations*3)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			deviceID := fmt.Sprintf("vd-%d", w+1) // DEV-001..DEV-008, all up at baseline
			for i := 0; i < iterations; i++ {
				ops := []Op{
					{Op: domain.OpSetDeviceStatus, DeviceID: deviceID, Status: "down"},
					{Op: domain.OpSetDeviceStatus, DeviceID: deviceID, Status: "up"},
				}
				if _, err := svc.ApplyOps(ctx, ws, ops, fmt.Sprintf("stress %d/%d", w, i), false); err != nil {
					errCh <- fmt.Errorf("worker %d apply: %w", w, err)
					return
				}
				if _, err := svc.RunChecks(ctx, ws, domain.CheckTriggerManual); err != nil {
					errCh <- fmt.Errorf("worker %d checks: %w", w, err)
					return
				}
				if _, err := svc.Topology(ctx, ws); err != nil {
					errCh <- fmt.Errorf("worker %d topology: %w", w, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent engine workload error (no SQLITE_BUSY should escape): %v", err)
	}

	// Deterministic final state: every touched device ends UP → the
	// materialization baseline hash, and the score is reproducible.
	if hash := mustHash(t, svc, ws); hash != baselineHash {
		t.Fatalf("final workspace hash = %s, want the materialization baseline %s", hash, baselineHash)
	}
	final, _ := mustRunChecks(t, svc, ws)
	if final.Score != baseline.Score {
		t.Fatalf("final checks score = %d, want the baseline score %d (identical state)", final.Score, baseline.Score)
	}
}

// ---- AUD1-9 (completed by FX-1b): recorded marker hash vs snapshot ----

// TestCheckpointRestoreSnapshotIntegrity pins the marker verification: the
// checkpoint's recorded workspace hash must equal the state hash of the
// snapshot file at restore time. A snapshot modified on disk after creation
// (tampering, corruption) makes the restore REFUSE with domain.ErrConflict
// BEFORE any changeset is applied — the live state and the changeset history
// stay untouched, instead of silently restoring to a state the marker never
// described. The marker itself is computed from the snapshot at creation
// (CreateCheckpoint), so intact snapshots always pass.
func TestCheckpointRestoreSnapshotIntegrity(t *testing.T) {
	svc, db, res := seededEngine(t)
	ctx := context.Background()
	ws := res.WorkspaceID
	hashBefore := mustHash(t, svc, ws)
	csBefore, err := db.ChangeSets.CountByWorkspace(ctx, ws)
	if err != nil {
		t.Fatalf("count changesets: %v", err)
	}

	cp, err := svc.CreateCheckpoint(ctx, ws, "integrity")
	if err != nil {
		t.Fatalf("CreateCheckpoint: %v", err)
	}
	if cp.WorkspaceHash != hashBefore {
		t.Fatalf("marker hash = %s, want the live materialization hash %s (snapshot equals live state in single-writer mode)", cp.WorkspaceHash, hashBefore)
	}

	// Tamper the snapshot file OUT OF BAND: a writable open of the snapshot
	// database (test-only direct SQL surface — the engine always opens
	// snapshots read-only) inserting a parallel link row. The snapshot state
	// hash now differs from the recorded marker.
	ln1, err := db.SimLinks.GetByID(ctx, "ln-1")
	if err != nil {
		t.Fatalf("GetByID(ln-1): %v", err)
	}
	snapPath := filepath.Join(filepath.Dir(db.Path()), "checkpoints", ws, cp.CheckpointID+".db")
	tamper, err := repo.Open(snapPath)
	if err != nil {
		t.Fatalf("repo.Open(snapshot, writable): %v", err)
	}
	now := svc.clock()
	err = tamper.Do(ctx, func(tx *repo.Tx) error {
		return tx.SimLinks.Insert(ctx, domain.SimLink{
			ID: "ln-900", WorkspaceID: ws,
			AInterfaceID: ln1.AInterfaceID, ZInterfaceID: ln1.ZInterfaceID,
			Status: domain.EntityStatusUp, Label: "TAMPERED",
			CreatedAt: now, UpdatedAt: now,
		})
	})
	if err != nil {
		t.Fatalf("tamper insert: %v", err)
	}
	if err := tamper.Close(); err != nil {
		t.Fatalf("tamper close: %v", err)
	}

	_, err = svc.RestoreCheckpoint(ctx, ws, cp.CheckpointID, false)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("RestoreCheckpoint(tampered snapshot): err = %v, want domain.ErrConflict", err)
	}
	if !containsSubstring(err.Error(), "recorded checkpoint hash") {
		t.Fatalf("restore error = %q, want the recorded-checkpoint-hash wording", err.Error())
	}

	// Nothing was applied: hash and changeset history unchanged, and the
	// tampered row never leaked into the live workspace.
	if hash := mustHash(t, svc, ws); hash != hashBefore {
		t.Fatalf("workspace hash changed on the refused restore: %s -> %s", hashBefore, hash)
	}
	csAfter, err := db.ChangeSets.CountByWorkspace(ctx, ws)
	if err != nil {
		t.Fatalf("count changesets after: %v", err)
	}
	if csAfter != csBefore {
		t.Fatalf("changeset count %d -> %d, want unchanged (the refused restore created no changeset)", csBefore, csAfter)
	}
	if got := touchCountOf(t, db, ln1.AInterfaceID); got != 1 {
		t.Fatalf("links on the tampered pair = %d, want 1 (untouched live state)", got)
	}

	// An INTACT checkpoint on the same workspace still restores cleanly
	// after the refusal (the marker check does not poison the path).
	cp2, err := svc.CreateCheckpoint(ctx, ws, "intact")
	if err != nil {
		t.Fatalf("CreateCheckpoint(intact): %v", err)
	}
	applyOK(t, svc, ws, []Op{{Op: domain.OpSetLinkStatus, LinkID: "ln-2", Status: "down"}})
	restored, err := svc.RestoreCheckpoint(ctx, ws, cp2.CheckpointID, false)
	if err != nil {
		t.Fatalf("RestoreCheckpoint(intact): %v", err)
	}
	if restored.WorkspaceHash != cp2.WorkspaceHash {
		t.Fatalf("intact restore: workspace hash %s != marker hash %s", restored.WorkspaceHash, cp2.WorkspaceHash)
	}
}
