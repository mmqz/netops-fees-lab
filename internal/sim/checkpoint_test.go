package sim

// ACC-P2-7 — checkpoint round-trip through the SQLite Online Backup API:
// create captures the state hash and a snapshot file at the pinned relative
// path; break the state (address change, device flip, link delete, two
// device deletes with cascades); restore replays the difference as ONE
// checkpoint_restore changeset of forward ops and lands on the EXACT
// checkpoint hash; findings match the pre-break run by the id-churn-proof
// business keys of the diff machinery; a second restore is a no-op; delete
// workspace purges rows and the snapshot directory. Plus the determinism
// spot check and the error catalog.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// cpSetup runs the fixed pre-checkpoint script (materialize → baseline
// checks → configure an address → pre-break checks) and returns the engine,
// the repo handle, the workspace id, the hash AFTER the address
// configuration (the state the checkpoint captures) and the pre-break run
// results.
func cpSetup(t *testing.T) (*Service, *repo.DB, string, string, []domain.CheckResult) {
	t.Helper()
	svc, db, res := seededEngine(t)
	ws := res.WorkspaceID

	// Baseline run at materialize time (the "before" reference).
	mustRunChecks(t, svc, ws)
	// Configure an address — the state the checkpoint will capture.
	applyOK(t, svc, ws, []Op{{
		Op: domain.OpConfigInterfaceIPv4, InterfaceID: "if-1",
		IPv4Addr: strPtr("10.0.0.1"), IPv4Prefix: intPtr(24),
	}})
	// Pre-break run: computed on the exact state the checkpoint captures —
	// this is the run the post-restore findings must match.
	_, preBreakResults := mustRunChecks(t, svc, ws)
	return svc, db, ws, mustHash(t, svc, ws), preBreakResults
}

// TestCheckpointRestore is ACC-P2-7.
func TestCheckpointRestore(t *testing.T) {
	svc, db, ws, cpHash, preBreakResults := cpSetup(t)
	ctx := context.Background()

	// ---- create cp-1 (label). The marker row stores the snapshot path
	// RELATIVE to the DB directory; the file exists at the pinned location.
	cp1, err := svc.CreateCheckpoint(ctx, ws, "基线")
	if err != nil {
		t.Fatalf("CreateCheckpoint: %v", err)
	}
	if cp1.CheckpointID != "cp-1" || cp1.Label != "基线" {
		t.Fatalf("cp1 = %+v, want cp-1 / 基线", cp1)
	}
	if cp1.WorkspaceHash != cpHash {
		t.Fatalf("cp1 hash = %s, want the current state hash %s", cp1.WorkspaceHash, cpHash)
	}
	if cp1.CreatedAt.IsZero() {
		t.Fatalf("cp1.CreatedAt is zero")
	}
	row, err := db.Checkpoints.GetByID(ctx, "cp-1")
	if err != nil {
		t.Fatalf("checkpoint row: %v", err)
	}
	wantRel := filepath.Join("checkpoints", ws, "cp-1.db")
	if row.FilePath != wantRel {
		t.Fatalf("checkpoint file_path = %q, want the relative path %q", row.FilePath, wantRel)
	}
	snapshot := filepath.Join(filepath.Dir(db.Path()), row.FilePath)
	if _, err := os.Stat(snapshot); err != nil {
		t.Fatalf("snapshot file %s missing: %v", snapshot, err)
	}
	// The snapshot opens read-only and carries the checkpointed workspace.
	snapDB, err := repo.OpenReadOnly(snapshot)
	if err != nil {
		t.Fatalf("OpenReadOnly(snapshot): %v", err)
	}
	if _, err := snapDB.Workspaces.GetByID(ctx, ws); err != nil {
		t.Fatalf("snapshot must contain the workspace: %v", err)
	}
	_ = snapDB.Close()

	// ---- break the state (separate changesets): the address changes, a
	// device goes down, a link is deleted, and two devices are deleted with
	// their cascades — DEV-010 (up, CKT-009/010 → ln-9/ln-10) and DEV-015
	// (down, CKT-014/015 → ln-14/ln-15; exercise create_device +
	// set_device_status with the predicted id on the restore path).
	applyOK(t, svc, ws, []Op{{
		Op: domain.OpConfigInterfaceIPv4, InterfaceID: "if-1",
		IPv4Addr: strPtr("10.0.0.9"), IPv4Prefix: intPtr(24),
	}})
	applyOK(t, svc, ws, []Op{{Op: domain.OpSetDeviceStatus, DeviceID: "vd-5", Status: "down"}})
	applyOK(t, svc, ws, []Op{{Op: domain.OpDeleteLink, LinkID: "ln-3"}})
	applyOK(t, svc, ws, []Op{{Op: domain.OpDeleteDevice, DeviceID: "vd-10"}})
	applyOK(t, svc, ws, []Op{{Op: domain.OpDeleteDevice, DeviceID: "vd-15"}})
	brokenHash := mustHash(t, svc, ws)
	if brokenHash == cpHash {
		t.Fatalf("the break phase must change the state")
	}

	// Second checkpoint AFTER the break: proves list ordering (newest first).
	cp2, err := svc.CreateCheckpoint(ctx, ws, "broken")
	if err != nil {
		t.Fatalf("CreateCheckpoint(broken): %v", err)
	}
	if cp2.CheckpointID != "cp-2" {
		t.Fatalf("cp2 = %+v, want cp-2 (monotonic sequence)", cp2)
	}
	if cp2.WorkspaceHash != brokenHash {
		t.Fatalf("cp2 hash = %s, want the broken state hash %s", cp2.WorkspaceHash, brokenHash)
	}

	// ---- restore cp-1: one checkpoint_restore changeset of forward ops.
	res, err := svc.RestoreCheckpoint(ctx, ws, "cp-1", true)
	if err != nil {
		t.Fatalf("RestoreCheckpoint(cp-1): %v", err)
	}
	if res.ChangeSetID == "" || res.OpsApplied == 0 {
		t.Fatalf("restore = %+v, want a real changeset with ops", res)
	}
	if res.RestoredToHash != cpHash || res.WorkspaceHash != cpHash {
		t.Fatalf("restore hashes = restoredTo %s / workspace %s, want the checkpoint hash %s",
			res.RestoredToHash, res.WorkspaceHash, cpHash)
	}
	if got := mustHash(t, svc, ws); got != cpHash {
		t.Fatalf("current hash after restore = %s, want the checkpoint hash %s (exact state round-trip)", got, cpHash)
	}
	if res.Note != "" {
		t.Fatalf("a real restore must carry no note, got %q", res.Note)
	}
	if res.Checks == nil {
		t.Fatalf("runChecks=true must attach the auto-check summary")
	}
	// The restore changeset itself: trigger checkpoint_restore, forward ops
	// only (no restore_* payloads), description "restore checkpoint cp-1 …".
	cs, err := db.ChangeSets.GetByID(ctx, res.ChangeSetID)
	if err != nil {
		t.Fatalf("restore changeset %s: %v", res.ChangeSetID, err)
	}
	if cs.Trigger != domain.ChangeSetTriggerCheckpointRestore {
		t.Fatalf("restore changeset trigger = %q, want checkpoint_restore", cs.Trigger)
	}
	wantDesc := "restore checkpoint cp-1 \"基线\""
	if cs.Description != wantDesc {
		t.Fatalf("restore changeset description = %q, want %q", cs.Description, wantDesc)
	}
	opRows, err := db.ChangeSets.GetOps(ctx, res.ChangeSetID)
	if err != nil {
		t.Fatalf("GetOps(%s): %v", res.ChangeSetID, err)
	}
	if len(opRows) != res.OpsApplied {
		t.Fatalf("restore changeset has %d op rows, opsApplied says %d", len(opRows), res.OpsApplied)
	}
	counts := map[string]int{}
	for _, r := range opRows {
		if domain.IsRestoreOp(r.OpCode) {
			t.Fatalf("restore changeset op %d is the engine-internal %q — forward ops only", r.Seq, r.OpCode)
		}
		counts[r.OpCode]++
	}
	// Plan shape: 2 devices + 4 interfaces + 5 links re-created (ln-3, ln-9,
	// ln-10, ln-14, ln-15), 2 set_device_status (DEV-005 back up — it went
	// down in the break phase — and DEV-015 back down via the predicted id),
	// 1 config_interface_ipv4 (if-1 back to 10.0.0.1/24); nothing else
	// differs.
	wantCounts := map[string]int{
		domain.OpCreateDevice:        2,
		domain.OpCreateInterface:     4,
		domain.OpCreateLink:          5,
		domain.OpSetDeviceStatus:     2,
		domain.OpConfigInterfaceIPv4: 1,
	}
	if !reflectEqualCounts(counts, wantCounts) {
		t.Fatalf("restore op plan = %v, want %v (14 ops)", counts, wantCounts)
	}
	if len(opRows) != 14 {
		t.Fatalf("restore changeset ops = %d, want 14", len(opRows))
	}
	// The recreated entities: DEV-010 up, DEV-015 down (set via the
	// predicted id — create_device has no status field), address restored.
	dev5, err := db.VirtualDevices.GetByID(ctx, "vd-5")
	if err != nil || dev5.Status != domain.EntityStatusUp {
		t.Fatalf("vd-5 after restore = %+v (err %v), want up (break-phase flip reverted)", dev5, err)
	}
	dev10, err := db.VirtualDevices.GetByCode(ctx, ws, "DEV-010")
	if err != nil || dev10.Status != domain.EntityStatusUp {
		t.Fatalf("DEV-010 after restore = %+v (err %v), want up", dev10, err)
	}
	dev15, err := db.VirtualDevices.GetByCode(ctx, ws, "DEV-015")
	if err != nil || dev15.Status != domain.EntityStatusDown {
		t.Fatalf("DEV-015 after restore = %+v (err %v), want down (created then set down)", dev15, err)
	}
	iface, err := db.SimInterfaces.GetByID(ctx, "if-1")
	if err != nil || iface.IPv4Addr != "10.0.0.1" || iface.IPv4Prefix != 24 {
		t.Fatalf("if-1 after restore = %+v (err %v), want 10.0.0.1/24", iface, err)
	}

	// ---- findings match the pre-break run. The comparison uses the
	// id-churn-proof business keys of the diff machinery: re-created
	// entities carry fresh ids, so raw detail JSON would differ while the
	// findings themselves (addr / device code / members / device+iface
	// names) are identical — exactly the matching §7 prescribes.
	postRun, postResults := mustRunChecks(t, svc, ws)
	preRunScore := scoreOfFindings(preBreakResults)
	if postRun.Score != preRunScore {
		t.Fatalf("post-restore score = %d, want the pre-break score %d", postRun.Score, preRunScore)
	}
	if !equalStrings(findingBusinessKeys(preBreakResults), findingBusinessKeys(postResults)) {
		t.Fatalf("findings after restore differ from the pre-break run:\npre:  %v\npost: %v",
			findingBusinessKeys(preBreakResults), findingBusinessKeys(postResults))
	}

	// ---- restore cp-1 AGAIN: no-op (already at that state).
	csCount, err := db.ChangeSets.CountByWorkspace(ctx, ws)
	if err != nil {
		t.Fatalf("count changesets: %v", err)
	}
	noop, err := svc.RestoreCheckpoint(ctx, ws, "cp-1", false)
	if err != nil {
		t.Fatalf("RestoreCheckpoint(cp-1) again: %v", err)
	}
	if noop.ChangeSetID != "" || noop.OpsApplied != 0 {
		t.Fatalf("second restore = %+v, want the no-op result", noop)
	}
	if noop.Note != "already at checkpoint state" {
		t.Fatalf("no-op note = %q, want %q", noop.Note, "already at checkpoint state")
	}
	if noop.WorkspaceHash != cpHash || noop.RestoredToHash != cpHash {
		t.Fatalf("no-op hashes = %+v", noop)
	}
	if got, _ := db.ChangeSets.CountByWorkspace(ctx, ws); got != csCount {
		t.Fatalf("no-op restore must not create a changeset: %d → %d", csCount, got)
	}

	// ---- list: newest first (cp-2, cp-1).
	list, err := svc.ListCheckpoints(ctx, ws)
	if err != nil {
		t.Fatalf("ListCheckpoints: %v", err)
	}
	if len(list) != 2 || list[0].ID != "cp-2" || list[1].ID != "cp-1" {
		t.Fatalf("checkpoint list = %+v, want [cp-2 cp-1]", list)
	}
	if list[0].Label != "broken" || list[1].Label != "基线" {
		t.Fatalf("checkpoint labels = [%s %s]", list[0].Label, list[1].Label)
	}

	// ---- error catalog.
	if _, err := svc.CreateCheckpoint(ctx, ws, ""); !errors.Is(err, domain.ErrInvalidOp) {
		t.Fatalf("empty label: err = %v, want domain.ErrInvalidOp", err)
	}
	if _, err := svc.CreateCheckpoint(ctx, "ws-none", "x"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown workspace: err = %v, want domain.ErrNotFound", err)
	}
	if _, err := svc.RestoreCheckpoint(ctx, ws, "cp-99", false); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown checkpoint: err = %v, want domain.ErrNotFound", err)
	}
	if _, err := svc.ListCheckpoints(ctx, "ws-none"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("ListCheckpoints unknown workspace: err = %v, want domain.ErrNotFound", err)
	}
	// Missing snapshot file: conflict whose message contains the pinned
	// substring (P2-T4 maps it to 409 checkpoint_missing_file).
	cp2Row, err := db.Checkpoints.GetByID(ctx, "cp-2")
	if err != nil {
		t.Fatalf("GetByID(cp-2): %v", err)
	}
	cp2Path := filepath.Join(filepath.Dir(db.Path()), cp2Row.FilePath)
	if err := os.Remove(cp2Path); err != nil {
		t.Fatalf("remove snapshot: %v", err)
	}
	_, err = svc.RestoreCheckpoint(ctx, ws, "cp-2", false)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("missing snapshot: err = %v, want domain.ErrConflict", err)
	}
	if !containsSubstring(err.Error(), "checkpoint file missing") {
		t.Fatalf("missing snapshot error must mention \"checkpoint file missing\": %q", err.Error())
	}

	// ---- delete workspace: rows gone, snapshot directory best-effort gone.
	delCounts, err := svc.DeleteWorkspace(ctx, ws)
	if err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}
	if delCounts["virtual_devices"] != 27 || delCounts["interfaces"] != 40 || delCounts["links"] != 20 {
		t.Fatalf("delete counts devices/interfaces/links = %d/%d/%d, want 27/40/20",
			delCounts["virtual_devices"], delCounts["interfaces"], delCounts["links"])
	}
	if delCounts["changesets"] == 0 || delCounts["check_runs"] == 0 || delCounts["checkpoints"] != 2 {
		t.Fatalf("delete counts cs/runs/cps = %d/%d/%d, want >0/>0/2",
			delCounts["changesets"], delCounts["check_runs"], delCounts["checkpoints"])
	}
	if _, err := db.Workspaces.GetByID(ctx, ws); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("workspace row must be gone: %v", err)
	}
	devCounts, err := db.VirtualDevices.CountByWorkspace(ctx, ws)
	if err != nil {
		t.Fatalf("count devices after delete: %v", err)
	}
	if total := devCounts[domain.EntityStatusUp] + devCounts[domain.EntityStatusDown]; total != 0 {
		t.Fatalf("virtual_devices rows after delete = %d, want 0", total)
	}
	if runs, err := db.CheckRuns.CountByWorkspace(ctx, ws); err != nil || runs != 0 {
		t.Fatalf("check_runs rows after delete = %d (err %v), want 0", runs, err)
	}
	cpDir := filepath.Join(filepath.Dir(db.Path()), "checkpoints", ws)
	if _, err := os.Stat(cpDir); !os.IsNotExist(err) {
		t.Fatalf("checkpoint directory %s must be removed, stat err = %v", cpDir, err)
	}
	if _, err := svc.DeleteWorkspace(ctx, ws); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("double delete: err = %v, want domain.ErrNotFound", err)
	}
}

// findingBusinessKeys renders a run's findings as the sorted id-churn-proof
// keys of the §7 diff (severity + check code + business key) — the form two
// runs of the same logical state must agree on even when entity ids differ.
func findingBusinessKeys(results []domain.CheckResult) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		if r.Passed {
			continue
		}
		out = append(out, r.Severity.String()+" "+keyOfFinding(r))
	}
	sort.Strings(out)
	return out
}

// scoreOfFindings recomputes the §5 score of a result row set.
func scoreOfFindings(results []domain.CheckResult) int {
	score := 100
	for _, r := range results {
		if r.Passed {
			continue
		}
		switch r.Severity {
		case domain.SeverityError:
			score -= 5
		case domain.SeverityWarning:
			score -= 1
		}
	}
	if score < 0 {
		return 0
	}
	return score
}

// reflectEqualCounts compares two op-code count maps.
func reflectEqualCounts(got, want map[string]int) bool {
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}

// equalStrings compares two string slices for exact equality.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// containsSubstring reports whether substr is inside s.
func containsSubstring(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestCheckpointDeterminismSpot: two fresh databases, identical states ⇒
// identical checkpoint hash fields (the cheap determinism add of this task).
func TestCheckpointDeterminismSpot(t *testing.T) {
	var hashes [2]string
	for i := 0; i < 2; i++ {
		svc, _, ws, cpHash, _ := cpSetup(t)
		ctx := context.Background()
		cp, err := svc.CreateCheckpoint(ctx, ws, "same")
		if err != nil {
			t.Fatalf("CreateCheckpoint #%d: %v", i, err)
		}
		if cp.WorkspaceHash != cpHash {
			t.Fatalf("checkpoint #%d hash %s != state hash %s", i, cp.WorkspaceHash, cpHash)
		}
		hashes[i] = cp.WorkspaceHash
	}
	if hashes[0] != hashes[1] {
		t.Fatalf("identical states must checkpoint to identical hashes: %s vs %s", hashes[0], hashes[1])
	}
}

// TestRestoreCheckpointUnknownWorkspace guards the entry points.
func TestRestoreCheckpointUnknownWorkspace(t *testing.T) {
	svc, _, _ := seededEngine(t)
	ctx := context.Background()
	if _, err := svc.RestoreCheckpoint(ctx, "ws-none", "cp-1", true); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("RestoreCheckpoint unknown workspace: %v", err)
	}
}
