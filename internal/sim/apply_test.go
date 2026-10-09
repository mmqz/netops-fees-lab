package sim

// ACC-P2-2 — atomicity of failed changesets, the §4 apply algorithm
// (one transaction, failed-changeset recording in a second transaction),
// auto-checks after ops, and the semantic failure catalog. Plus the
// forward/inverse round-trip of the ten forward ops.

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
)

// TestApplyOpsAtomicity is ACC-P2-2: [ok set_device_status, ok
// set_link_status, set_link_status on ln-99] ⇒ OpError wrapping
// domain.ErrOpFailed with Index 2; the workspace hash is unchanged; the
// failed changeset is persisted with ops_applied 0, the failing op
// "failed", the others "not_attempted", inverse_json empty for every row;
// no state changed anywhere.
func TestApplyOpsAtomicity(t *testing.T) {
	svc, db, res := seededEngine(t)
	ctx := context.Background()
	ws := res.WorkspaceID

	hashBefore := mustHash(t, svc, ws)
	topoBefore := mustTopology(t, svc, ws)

	ops := []Op{
		{Op: domain.OpSetDeviceStatus, DeviceID: "vd-1", Status: "down"},
		{Op: domain.OpSetLinkStatus, LinkID: "ln-1", Status: "down"},
		{Op: domain.OpSetLinkStatus, LinkID: "ln-99", Status: "down"},
	}
	_, err := svc.ApplyOps(ctx, ws, ops, "boom", true)
	if err == nil {
		t.Fatalf("ApplyOps with a bad op: want error, got nil")
	}
	if !errors.Is(err, domain.ErrOpFailed) {
		t.Fatalf("err = %v, want domain.ErrOpFailed", err)
	}
	var opErr *OpError
	if !errors.As(err, &opErr) {
		t.Fatalf("err = %v, want *OpError", err)
	}
	if opErr.Index != 2 || opErr.Code != domain.OpSetLinkStatus {
		t.Fatalf("OpError = %+v, want Index 2 Code set_link_status", opErr)
	}
	if want := "op 2 (set_link_status): link not found: ln-99"; !stringsContains(err.Error(), want) {
		t.Fatalf("err text %q must contain %q", err.Error(), want)
	}
	if !stringsContains(err.Error(), "[changeset cs-1 recorded as failed]") {
		t.Fatalf("err text %q must name the failed changeset", err.Error())
	}

	// Hash and topology untouched.
	if hashAfter := mustHash(t, svc, ws); hashAfter != hashBefore {
		t.Fatalf("workspace hash changed: %s → %s", hashBefore, hashAfter)
	}
	topoAfter := mustTopology(t, svc, ws)
	if !reflect.DeepEqual(topoBefore.Nodes, topoAfter.Nodes) || !reflect.DeepEqual(topoBefore.Edges, topoAfter.Edges) {
		t.Fatalf("topology changed after a failed changeset")
	}

	// The failed changeset is recorded per §4 step 2.
	changesets, total, err := db.ChangeSets.ListByWorkspace(ctx, ws, 1, 0)
	if err != nil || total != 1 || len(changesets) != 1 {
		t.Fatalf("changesets: %d rows (err %v), want exactly the failed one", total, err)
	}
	cs := changesets[0]
	if cs.ID != "cs-1" || cs.Status != domain.ChangeSetStatusFailed || cs.OpsApplied != 0 {
		t.Fatalf("failed changeset = %+v, want cs-1/failed/ops_applied 0", cs)
	}
	if cs.Description != "boom" || cs.Trigger != domain.ChangeSetTriggerUser {
		t.Fatalf("failed changeset meta = %q/%q, want boom/user", cs.Description, cs.Trigger)
	}
	rows, err := db.ChangeSets.GetOps(ctx, cs.ID)
	if err != nil {
		t.Fatalf("GetOps(%s): %v", cs.ID, err)
	}
	if len(rows) != 3 {
		t.Fatalf("failed changeset has %d op rows, want 3", len(rows))
	}
	for i, row := range rows {
		if row.Seq != i+1 {
			t.Errorf("op row %d has seq %d, want %d", i, row.Seq, i+1)
		}
		if row.InverseJSON != "" {
			t.Errorf("op row %d: inverse_json = %q, want empty (failed changesets never store inverses)", i, row.InverseJSON)
		}
		wantResult := domain.OpResultNotAttempted
		if i == 2 {
			wantResult = domain.OpResultFailed
		}
		if row.OpResult != wantResult {
			t.Errorf("op row %d result = %q, want %q", i, row.OpResult, wantResult)
		}
		if row.OpCode != ops[i].Op {
			t.Errorf("op row %d code = %q, want %q", i, row.OpCode, ops[i].Op)
		}
		// op_json carries the submitted payload of EVERY op — including the
		// ones after the failure (computed before the transaction).
		if row.OpJSON == "" {
			t.Errorf("op row %d has empty op_json, want the submitted payload", i)
		}
		var stored Op
		if err := json.Unmarshal([]byte(row.OpJSON), &stored); err != nil {
			t.Errorf("op row %d op_json = %q: %v", i, row.OpJSON, err)
		} else if stored.Op != ops[i].Op || stored.LinkID != ops[i].LinkID {
			t.Errorf("op row %d op_json = %+v, want the submitted op %+v", i, stored, ops[i])
		}
	}

	// No state change anywhere: device/link statuses all original.
	if d, err := db.VirtualDevices.GetByID(ctx, "vd-1"); err != nil || d.Status != domain.EntityStatusUp {
		t.Fatalf("device vd-1 = %+v (err %v), want still up", d, err)
	}
	if l, err := db.SimLinks.GetByID(ctx, "ln-1"); err != nil || l.Status != domain.EntityStatusUp {
		t.Fatalf("link ln-1 = %+v (err %v), want still up", l, err)
	}
	// No check run was created (auto-checks only fire on applied changesets).
	if runCount, err := db.CheckRuns.CountByWorkspace(ctx, ws); err != nil || runCount != 0 {
		t.Fatalf("check runs after failed changeset = %d, want 0: %v", runCount, err)
	}
	// Sequences: the rolled-back allocation is discarded, so the failed
	// changeset still got cs-1 (deterministic id, no burned ids).
	if n, err := db.Sequences.Value(ctx, domain.SeqChangeSet); err != nil || n != 1 {
		t.Fatalf("changeset sequence = %d, want 1: %v", n, err)
	}
}

// stringsContains avoids importing strings for a single call site family.
func stringsContains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestApplyOpsSemanticFailures walks the catalog of op_failed cases
// (§4): every one rolls back, records a failed changeset and returns
// domain.ErrOpFailed.
func TestApplyOpsSemanticFailures(t *testing.T) {
	svc, db, res := seededEngine(t)
	ctx := context.Background()
	ws := res.WorkspaceID

	// Second workspace in the same database: its ids must NOT be usable
	// from the first workspace (cross-workspace references behave as
	// not-found).
	other, err := svc.Materialize(ctx, "other", "", nil)
	if err != nil {
		t.Fatalf("Materialize(other): %v", err)
	}
	otherDev, err := db.VirtualDevices.GetByCode(ctx, other.WorkspaceID, "DEV-001")
	if err != nil {
		t.Fatalf("GetByCode(other/DEV-001): %v", err)
	}
	// A second interface on DEV-001 for the self-loop cases.
	applyOK(t, svc, ws, []Op{{Op: domain.OpCreateInterface, DeviceID: "vd-1", Name: "eth9"}})

	tests := []struct {
		name  string
		ops   []Op
		index int    // expected failing index
		code  string // expected op code of the failing op
	}{
		{
			name:  "unknown device",
			ops:   []Op{{Op: domain.OpSetDeviceStatus, DeviceID: "vd-99", Status: "down"}},
			index: 0, code: domain.OpSetDeviceStatus,
		},
		{
			name:  "unknown interface",
			ops:   []Op{{Op: domain.OpConfigInterfaceIPv4, InterfaceID: "if-99", IPv4Addr: strPtr("10.0.0.1"), IPv4Prefix: intPtr(24)}},
			index: 0, code: domain.OpConfigInterfaceIPv4,
		},
		{
			name:  "unknown link",
			ops:   []Op{{Op: domain.OpDeleteLink, LinkID: "ln-99"}},
			index: 0, code: domain.OpDeleteLink,
		},
		{
			name:  "duplicate device code",
			ops:   []Op{{Op: domain.OpCreateDevice, Code: "DEV-001", Name: "clone"}},
			index: 0, code: domain.OpCreateDevice,
		},
		{
			name:  "duplicate interface name on device",
			ops:   []Op{{Op: domain.OpCreateInterface, DeviceID: "vd-1", Name: "eth0"}},
			index: 0, code: domain.OpCreateInterface,
		},
		{
			name:  "create_link self loop (both interfaces on one device)",
			ops:   []Op{{Op: domain.OpCreateLink, AInterfaceID: "if-1", ZInterfaceID: "if-41"}},
			index: 0, code: domain.OpCreateLink,
		},
		{
			name:  "create_link same interface twice",
			ops:   []Op{{Op: domain.OpCreateLink, AInterfaceID: "if-1", ZInterfaceID: "if-1"}},
			index: 0, code: domain.OpCreateLink,
		},
		{
			name:  "cross-workspace device id",
			ops:   []Op{{Op: domain.OpSetDeviceStatus, DeviceID: otherDev.ID, Status: "down"}},
			index: 0, code: domain.OpSetDeviceStatus,
		},
		{
			name: "failure after two applied ops rolls both back",
			ops: []Op{
				{Op: domain.OpSetDeviceStatus, DeviceID: "vd-2", Status: "down"},
				{Op: domain.OpSetLinkStatus, LinkID: "ln-2", Status: "down"},
				{Op: domain.OpDeleteDevice, DeviceID: "vd-99"},
			},
			index: 2, code: domain.OpDeleteDevice,
		},
	}
	hashBaseline := mustHash(t, svc, ws)
	for _, tt := range tests {
		_, err := svc.ApplyOps(ctx, ws, tt.ops, "fault: "+tt.name, false)
		if !errors.Is(err, domain.ErrOpFailed) {
			t.Errorf("%s: err = %v, want domain.ErrOpFailed", tt.name, err)
			continue
		}
		var opErr *OpError
		if !errors.As(err, &opErr) || opErr.Index != tt.index || opErr.Code != tt.code {
			t.Errorf("%s: OpError = %+v, want Index %d Code %s", tt.name, opErr, tt.index, tt.code)
		}
		if hash := mustHash(t, svc, ws); hash != hashBaseline {
			t.Errorf("%s: workspace hash drifted after a rolled-back changeset", tt.name)
		}
	}
	// Every semantic failure is recorded as a failed changeset (plus the
	// one applied changeset of the setup step).
	changesets, _, err := db.ChangeSets.ListByWorkspace(ctx, ws, 1, 0)
	if err != nil {
		t.Fatalf("list changesets: %v", err)
	}
	failed := 0
	for _, cs := range changesets {
		if cs.Status == domain.ChangeSetStatusFailed {
			failed++
		}
	}
	if failed != len(tests) || len(changesets) != len(tests)+1 {
		t.Fatalf("changesets: %d total / %d failed, want %d total / %d failed (setup changeset applied)",
			len(changesets), failed, len(tests)+1, len(tests))
	}
	// And none of them ran checks.
	if runs, err := db.CheckRuns.CountByWorkspace(ctx, ws); err != nil || runs != 0 {
		t.Fatalf("check runs = %d, want 0: %v", runs, err)
	}
}

// TestApplyOpsInvalidPayloadRecordsNothing: payload validation fails
// BEFORE any transaction — no changeset row at all (§7 invalid_request vs
// op_failed).
func TestApplyOpsInvalidPayloadRecordsNothing(t *testing.T) {
	svc, db, res := seededEngine(t)
	ctx := context.Background()
	if _, err := svc.ApplyOps(ctx, res.WorkspaceID, []Op{
		{Op: domain.OpSetDeviceStatus, DeviceID: "vd-1", Status: "sideways"},
	}, "", false); !errors.Is(err, domain.ErrInvalidOp) {
		t.Fatalf("err = %v, want domain.ErrInvalidOp", err)
	}
	if cs, err := db.ChangeSets.CountByWorkspace(ctx, res.WorkspaceID); err != nil || cs != 0 {
		t.Fatalf("changesets = %d, want 0: %v", cs, err)
	}
}

// TestApplyOpsAutoChecks pins §4 step 3: runChecks=true attaches a
// run with trigger auto_after_ops and changeset_id = the changeset;
// runChecks=false records no run at all.
func TestApplyOpsAutoChecks(t *testing.T) {
	svc, db, res := seededEngine(t)
	ctx := context.Background()
	ws := res.WorkspaceID

	withChecks, err := svc.ApplyOps(ctx, ws, []Op{
		{Op: domain.OpSetDeviceStatus, DeviceID: "vd-2", Status: "down"},
	}, "with checks", true)
	if err != nil {
		t.Fatalf("ApplyOps(runChecks=true): %v", err)
	}
	if withChecks.Checks == nil {
		t.Fatalf("runChecks=true must embed a RunSummary")
	}
	run, results, err := svc.GetRun(ctx, ws, withChecks.Checks.RunID)
	if err != nil {
		t.Fatalf("GetRun(%s): %v", withChecks.Checks.RunID, err)
	}
	if run.ChangesetID != withChecks.ChangeSetID {
		t.Fatalf("auto run changeset_id = %q, want %q", run.ChangesetID, withChecks.ChangeSetID)
	}
	if run.Trigger != domain.CheckTriggerAutoAfterOps {
		t.Fatalf("auto run trigger = %q, want auto_after_ops", run.Trigger)
	}
	if run.RuleVersion != domain.SimRuleVersion {
		t.Fatalf("auto run rule version = %q, want %q", run.RuleVersion, domain.SimRuleVersion)
	}
	if len(results) == 0 {
		t.Fatalf("auto run has no result rows")
	}
	if withChecks.Checks.Score != run.Score {
		t.Fatalf("embedded score %d != run score %d", withChecks.Checks.Score, run.Score)
	}
	// WorkspaceHash of the result equals the hash the run was computed on.
	if withChecks.WorkspaceHash != mustHash(t, svc, ws) {
		t.Fatalf("ApplyResult.WorkspaceHash %s != current hash", withChecks.WorkspaceHash)
	}

	runsBefore, err := db.CheckRuns.CountByWorkspace(ctx, ws)
	if err != nil {
		t.Fatalf("count runs: %v", err)
	}
	withoutChecks, err := svc.ApplyOps(ctx, ws, []Op{
		{Op: domain.OpSetDeviceStatus, DeviceID: "vd-2", Status: "up"},
	}, "no checks", false)
	if err != nil {
		t.Fatalf("ApplyOps(runChecks=false): %v", err)
	}
	if withoutChecks.Checks != nil {
		t.Fatalf("runChecks=false must leave Checks nil, got %+v", withoutChecks.Checks)
	}
	runsAfter, err := db.CheckRuns.CountByWorkspace(ctx, ws)
	if err != nil || runsAfter != runsBefore {
		t.Fatalf("check runs = %d, want unchanged %d: %v", runsAfter, runsBefore, err)
	}
	if withoutChecks.WorkspaceHash != mustHash(t, svc, ws) {
		t.Fatalf("ApplyResult.WorkspaceHash must still be computed without checks")
	}
}

// TestApplyOpsConfigUnconfigure verifies the both-null unconfigure
// semantics of config_interface_ipv4 through real state.
func TestApplyOpsConfigUnconfigure(t *testing.T) {
	svc, db, res := seededEngine(t)
	ctx := context.Background()
	ws := res.WorkspaceID

	applyOK(t, svc, ws, []Op{{
		Op: domain.OpConfigInterfaceIPv4, InterfaceID: "if-1",
		IPv4Addr: strPtr("10.0.0.1"), IPv4Prefix: intPtr(24),
	}})
	i, err := db.SimInterfaces.GetByID(ctx, "if-1")
	if err != nil || i.IPv4Addr != "10.0.0.1" || i.IPv4Prefix != 24 {
		t.Fatalf("after configure: %+v (err %v)", i, err)
	}
	applyOK(t, svc, ws, []Op{{Op: domain.OpConfigInterfaceIPv4, InterfaceID: "if-1"}})
	i, err = db.SimInterfaces.GetByID(ctx, "if-1")
	if err != nil || i.IPv4Addr != "" || i.IPv4Prefix != -1 {
		t.Fatalf("after unconfigure: %+v (err %v), want NULL/NULL", i, err)
	}
}

// TestApplyOpsUnknownWorkspace: every engine entry point fails cleanly on
// an unknown workspace.
func TestApplyOpsUnknownWorkspace(t *testing.T) {
	svc, _, _ := seededEngine(t)
	ctx := context.Background()
	if _, err := svc.ApplyOps(ctx, "ws-none", []Op{{Op: domain.OpCreateDevice, Code: "C", Name: "n"}}, "", false); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("ApplyOps unknown workspace: %v", err)
	}
	if _, err := svc.RunChecks(ctx, "ws-none", domain.CheckTriggerManual); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("RunChecks unknown workspace: %v", err)
	}
	if _, err := svc.Topology(ctx, "ws-none"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Topology unknown workspace: %v", err)
	}
	if _, err := svc.WorkspaceHash(ctx, "ws-none"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("WorkspaceHash unknown workspace: %v", err)
	}
	if _, err := svc.Path(ctx, "ws-none", "DEV-001", "DEV-003"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Path unknown workspace: %v", err)
	}
	if _, _, err := svc.ListRuns(ctx, "ws-none", 1, 10); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("ListRuns unknown workspace: %v", err)
	}
	if _, _, err := svc.GetRun(ctx, "ws-none", "run-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetRun unknown workspace: %v", err)
	}
	if _, _, err := svc.LatestRun(ctx, "ws-none"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("LatestRun unknown workspace: %v", err)
	}
}

// TestRunChecksInvalidTrigger guards the trigger enum at the engine's
// public surface.
func TestRunChecksInvalidTrigger(t *testing.T) {
	svc, _, res := seededEngine(t)
	if _, err := svc.RunChecks(context.Background(), res.WorkspaceID, domain.CheckTrigger("bogus")); !errors.Is(err, domain.ErrInvalidOp) {
		t.Fatalf("RunChecks bogus trigger: err = %v, want domain.ErrInvalidOp", err)
	}
}
