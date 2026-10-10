package sim

// §3/§7 payload shapes of the read paths: the canonical state hash
// (exact JSON), the topology payload (field names, sorts), the run edge
// snapshot, and the run listing/pagination/get contracts.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mmqz/netops-lab/internal/domain"
)

// shapeWorkspace inserts a workspace row directly (the engine has no
// workspace-create besides Materialize; a hand-made row is legitimate
// test state for pure shape checks).
func shapeWorkspace(t *testing.T, svc *Service, id, name string) string {
	t.Helper()
	if id == "" {
		id = uuid.NewString()
	}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	if err := svc.db.Workspaces.Insert(context.Background(), domain.Workspace{
		ID: id, Name: name, Status: domain.WorkspaceStatusActive,
		SourceJSON: "{}", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("insert workspace %s: %v", name, err)
	}
	return id
}

// TestWorkspaceHashShape pins the exact canonical state JSON of §3 on a
// small crafted workspace: devices sorted by code, interfaces by name,
// links by (aDevice, aInterface, zDevice, zInterface, label), unconfigured
// addresses as null, field order exactly as pinned.
func TestWorkspaceHashShape(t *testing.T) {
	svc, _ := newEngine(t)
	ctx := context.Background()
	ws := shapeWorkspace(t, svc, "", "shape")

	applyOK(t, svc, ws, []Op{
		{Op: domain.OpCreateDevice, Code: "B", Name: "b"}, // code order != creation order
		{Op: domain.OpCreateDevice, Code: "A", Name: "a"},
		{Op: domain.OpCreateInterface, DeviceID: "vd-2", Name: "eth0"}, // A/eth0 = if-1
		{Op: domain.OpCreateInterface, DeviceID: "vd-1", Name: "eth0"}, // B/eth0 = if-2
		{Op: domain.OpConfigInterfaceIPv4, InterfaceID: "if-1", IPv4Addr: strPtr("10.0.0.1"), IPv4Prefix: intPtr(24)},
		{Op: domain.OpCreateLink, AInterfaceID: "if-1", ZInterfaceID: "if-2", Label: "L"},
	})

	// The pinned canonical state JSON (field order and nulls exactly).
	wantJSON := `{"devices":[` +
		`{"code":"A","name":"a","status":"up","interfaces":[` +
		`{"name":"eth0","status":"up","ipv4Addr":"10.0.0.1","ipv4Prefix":24}]},` +
		`{"code":"B","name":"b","status":"up","interfaces":[` +
		`{"name":"eth0","status":"up","ipv4Addr":null,"ipv4Prefix":null}]}],` +
		`"links":[{"aDevice":"A","aInterface":"eth0","zDevice":"B","zInterface":"eth0","status":"up","label":"L"}]}`
	sum := sha256.Sum256([]byte(wantJSON))
	wantHash := hex.EncodeToString(sum[:])
	if hash := mustHash(t, svc, ws); hash != wantHash {
		t.Fatalf("workspace hash = %s, want sha256 of the pinned canonical JSON (%s)", hash, wantHash)
	}

	// The run's topology_json edge snapshot: same six fields plus the four
	// endpoint-status fields of the v0.2.1 full-predicate extension, ALL
	// links, canonical sort.
	run, err := svc.RunChecks(ctx, ws, domain.CheckTriggerManual)
	if err != nil {
		t.Fatalf("RunChecks: %v", err)
	}
	stored, _, err := svc.GetRun(ctx, ws, run.RunID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	wantTopo := `[{"aDevice":"A","aInterface":"eth0","zDevice":"B","zInterface":"eth0","status":"up","label":"L","aDeviceStatus":"up","aInterfaceStatus":"up","zDeviceStatus":"up","zInterfaceStatus":"up"}]`
	if stored.TopologyJSON != wantTopo {
		t.Fatalf("topology_json = %s, want %s", stored.TopologyJSON, wantTopo)
	}

	// A down link still appears in the snapshot (ALL links incl. down) with
	// its endpoint statuses.
	applyOK(t, svc, ws, []Op{{Op: domain.OpSetLinkStatus, LinkID: "ln-1", Status: "down"}})
	run, err = svc.RunChecks(ctx, ws, domain.CheckTriggerManual)
	if err != nil {
		t.Fatalf("RunChecks: %v", err)
	}
	stored, _, err = svc.GetRun(ctx, ws, run.RunID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	wantTopo = `[{"aDevice":"A","aInterface":"eth0","zDevice":"B","zInterface":"eth0","status":"down","label":"L","aDeviceStatus":"up","aInterfaceStatus":"up","zDeviceStatus":"up","zInterfaceStatus":"up"}]`
	if stored.TopologyJSON != wantTopo {
		t.Fatalf("topology_json with a down link = %s, want %s", stored.TopologyJSON, wantTopo)
	}
}

// TestTopologyPayloadShape pins the §7 topology payload: exact JSON field
// names (nodes, edges, interfaces), nodes sorted by code, interfaces by
// name, edges by id.
func TestTopologyPayloadShape(t *testing.T) {
	svc, _ := newEngine(t)
	ws := shapeWorkspace(t, svc, "", "shape2")

	applyOK(t, svc, ws, []Op{
		{Op: domain.OpCreateDevice, Code: "B", Name: "b"},
		{Op: domain.OpCreateDevice, Code: "A", Name: "a"},
		{Op: domain.OpCreateInterface, DeviceID: "vd-2", Name: "eth1"},                     // A: eth1 first …
		{Op: domain.OpCreateInterface, DeviceID: "vd-2", Name: "eth0"},                     // … eth0 second (name sort!)
		{Op: domain.OpCreateInterface, DeviceID: "vd-1", Name: "eth0"},                     // B/eth0
		{Op: domain.OpCreateLink, AInterfaceID: "if-3", ZInterfaceID: "if-1", Label: "L1"}, // ln-1
		{Op: domain.OpCreateLink, AInterfaceID: "if-2", ZInterfaceID: "if-3", Label: "L2"}, // ln-2
	})
	topo := mustTopology(t, svc, ws)

	// Nodes sorted by code; interfaces sorted by name.
	if len(topo.Nodes) != 2 || topo.Nodes[0].Code != "A" || topo.Nodes[1].Code != "B" {
		t.Fatalf("nodes = %v, want A then B (code sort)", topo.Nodes)
	}
	nodeA := topo.Nodes[0]
	if len(nodeA.Interfaces) != 2 || nodeA.Interfaces[0].Name != "eth0" || nodeA.Interfaces[1].Name != "eth1" {
		t.Fatalf("A interfaces = %v, want eth0 then eth1 (name sort)", nodeA.Interfaces)
	}
	// Exact JSON of one node (field names and order).
	nodeJSON, err := json.Marshal(nodeA)
	if err != nil {
		t.Fatalf("marshal node: %v", err)
	}
	wantNode := `{"id":"vd-2","code":"A","name":"a","status":"up","interfaces":[` +
		`{"id":"if-2","name":"eth0","status":"up","ipv4Addr":"","ipv4Prefix":-1},` +
		`{"id":"if-1","name":"eth1","status":"up","ipv4Addr":"","ipv4Prefix":-1}]}`
	if string(nodeJSON) != wantNode {
		t.Fatalf("node JSON = %s, want %s", nodeJSON, wantNode)
	}
	// Edges sorted by numeric id (ln-1 before ln-2).
	if len(topo.Edges) != 2 || topo.Edges[0].ID != "ln-1" || topo.Edges[1].ID != "ln-2" {
		t.Fatalf("edges = %v, want ln-1 then ln-2 (numeric id sort)", topo.Edges)
	}
	edgeJSON, err := json.Marshal(topo.Edges[0])
	if err != nil {
		t.Fatalf("marshal edge: %v", err)
	}
	wantEdge := `{"id":"ln-1","aInterfaceId":"if-3","aDeviceId":"vd-1","aDeviceCode":"B",` +
		`"zInterfaceId":"if-1","zDeviceId":"vd-2","zDeviceCode":"A","status":"up","label":"L1"}`
	if string(edgeJSON) != wantEdge {
		t.Fatalf("edge JSON = %s, want %s", edgeJSON, wantEdge)
	}
	// Top-level payload field names.
	topoJSON, err := json.Marshal(topo)
	if err != nil {
		t.Fatalf("marshal topo: %v", err)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(topoJSON, &probe); err != nil {
		t.Fatalf("unmarshal topo: %v", err)
	}
	for _, key := range []string{"workspaceId", "workspaceHash", "nodes", "edges"} {
		if _, ok := probe[key]; !ok {
			t.Fatalf("topology payload misses key %q: %v", key, probe)
		}
	}
}

// TestTopologyEmptyWorkspace: an empty workspace yields empty (not null)
// node/edge arrays and the hash of the empty canonical state.
func TestTopologyEmptyWorkspace(t *testing.T) {
	svc, _ := newEngine(t)
	ctx := context.Background()
	ws := shapeWorkspace(t, svc, "", "empty")

	topo := mustTopology(t, svc, ws)
	if topo.Nodes == nil || len(topo.Nodes) != 0 {
		t.Fatalf("nodes = %+v, want empty non-nil", topo.Nodes)
	}
	if topo.Edges == nil || len(topo.Edges) != 0 {
		t.Fatalf("edges = %+v, want empty non-nil", topo.Edges)
	}
	wantJSON := `{"devices":[],"links":[]}`
	sum := sha256.Sum256([]byte(wantJSON))
	if hash := mustHash(t, svc, ws); hash != hex.EncodeToString(sum[:]) {
		t.Fatalf("empty workspace hash = %s, want sha256 of %s", hash, wantJSON)
	}
	// A check run on the empty workspace works (5 baselines, score 100).
	result, err := svc.RunChecks(ctx, ws, domain.CheckTriggerManual)
	if err != nil {
		t.Fatalf("RunChecks(empty): %v", err)
	}
	if len(result.Results) != 5 || result.Results[0].Message != "ok" {
		t.Fatalf("empty workspace results = %d rows, want 5 baselines", len(result.Results))
	}
	if result.Score != 100 {
		t.Fatalf("empty workspace score = %d, want 100", result.Score)
	}
	if stored, _, err := svc.GetRun(ctx, ws, result.RunID); err != nil || stored.TopologyJSON != "[]" {
		t.Fatalf("empty topology_json = %q (err %v), want []", stored.TopologyJSON, err)
	}
}

// TestRunListings covers ListRuns pagination + desc order, GetRun with
// findings-first row order, and LatestRun.
func TestRunListings(t *testing.T) {
	svc, _, res := seededEngine(t)
	ctx := context.Background()
	ws := res.WorkspaceID

	// Three runs with different triggers; a finding-bearing run in the
	// middle to assert the row order.
	applyOK(t, svc, ws, []Op{configureIface("if-1", "10.0.0.1", 24), configureIface("if-4", "10.0.0.1", 24)})
	triggers := []domain.CheckTrigger{
		domain.CheckTriggerManual,
		domain.CheckTriggerAPI,
		domain.CheckTriggerMCP,
	}
	for _, tr := range triggers {
		if _, err := svc.RunChecks(ctx, ws, tr); err != nil {
			t.Fatalf("RunChecks(%s): %v", tr, err)
		}
	}

	runs, total, err := svc.ListRuns(ctx, ws, 1, 2)
	if err != nil {
		t.Fatalf("ListRuns page 1: %v", err)
	}
	if total != 3 || len(runs) != 2 || runs[0].ID != "run-3" || runs[1].ID != "run-2" {
		t.Fatalf("ListRuns page 1 = %v (total %d), want [run-3 run-2] desc", runs, total)
	}
	runs, total, err = svc.ListRuns(ctx, ws, 2, 2)
	if err != nil || total != 3 || len(runs) != 1 || runs[0].ID != "run-1" {
		t.Fatalf("ListRuns page 2 = %v (total %d), want [run-1]", runs, total)
	}
	// Unlimited page.
	runs, total, err = svc.ListRuns(ctx, ws, 1, 0)
	if err != nil || total != 3 || len(runs) != 3 {
		t.Fatalf("ListRuns unlimited = %d rows (total %d), want 3", len(runs), total)
	}

	// GetRun: findings first (seq ascending), then the 5 baselines.
	run, results, err := svc.GetRun(ctx, ws, "run-1")
	if err != nil {
		t.Fatalf("GetRun(run-1): %v", err)
	}
	if run.ID != "run-1" || run.Trigger != domain.CheckTriggerManual || run.RuleVersion != domain.SimRuleVersion {
		t.Fatalf("run-1 = %+v", run)
	}
	if run.ChangesetID != "" {
		t.Fatalf("manual run changeset_id = %q, want empty (not tied to a changeset)", run.ChangesetID)
	}
	dups := findings(results, domain.CheckDuplicateHostAddress)
	if len(dups) != 1 {
		t.Fatalf("run-1 CHK-001 findings = %d, want 1", len(dups))
	}
	prevSeq := 0
	passedSeen := false
	for _, r := range results {
		if r.Seq <= prevSeq {
			t.Fatalf("result rows out of seq order: %d after %d", r.Seq, prevSeq)
		}
		prevSeq = r.Seq
		if r.Passed {
			passedSeen = true
		} else if passedSeen {
			t.Fatalf("finding row after baseline rows (findings must come first)")
		}
	}

	// LatestRun = run-3.
	latest, _, err := svc.LatestRun(ctx, ws)
	if err != nil || latest.ID != "run-3" {
		t.Fatalf("LatestRun = %+v (err %v), want run-3", latest, err)
	}

	// Unknown run.
	if _, _, err := svc.GetRun(ctx, ws, "run-99"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetRun(unknown): err = %v, want ErrNotFound", err)
	}
	// A run of ANOTHER workspace behaves like unknown.
	other, err := svc.Materialize(ctx, "listings-other", "", nil)
	if err != nil {
		t.Fatalf("Materialize(other): %v", err)
	}
	otherRun, err := svc.RunChecks(ctx, other.WorkspaceID, domain.CheckTriggerManual)
	if err != nil {
		t.Fatalf("RunChecks(other): %v", err)
	}
	if otherRun.RunID != "run-4" {
		t.Fatalf("other run id = %s, want run-4 (global monotonic sequence)", otherRun.RunID)
	}
	if _, _, err := svc.GetRun(ctx, ws, "run-4"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetRun(cross-workspace): err = %v, want ErrNotFound", err)
	}
	// LatestRun on a workspace without runs.
	emptyWS := shapeWorkspace(t, svc, "", "no-runs")
	if _, _, err := svc.LatestRun(ctx, emptyWS); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("LatestRun(no runs): err = %v, want ErrNotFound", err)
	}

	// RunResult JSON summary shape (Results excluded from JSON).
	run3, results3, err := svc.GetRun(ctx, ws, "run-3")
	if err != nil {
		t.Fatalf("GetRun(run-3): %v", err)
	}
	rr := RunResult{RunID: run3.ID, Trigger: run3.Trigger.String(), Counts: countSevs(results3), Score: run3.Score}
	b, err := json.Marshal(rr)
	if err != nil {
		t.Fatalf("marshal RunResult: %v", err)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(b, &probe); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"runId", "trigger", "counts", "score", "workspaceHash", "durationMs"} {
		if _, ok := probe[key]; !ok {
			t.Fatalf("RunResult JSON misses key %q: %s", key, b)
		}
	}
	if _, ok := probe["results"]; ok {
		t.Fatalf("RunResult JSON must not carry the results array: %s", b)
	}
	if !reflect.DeepEqual(rr.Counts, countSevs(results3)) {
		t.Fatalf("counts drift")
	}
}

// countSevs tallies the finding severities of a result set.
func countSevs(results []domain.CheckResult) SevCounts {
	var c SevCounts
	for _, r := range results {
		if r.Passed {
			continue
		}
		switch r.Severity {
		case domain.SeverityError:
			c.Error++
		case domain.SeverityWarning:
			c.Warning++
		case domain.SeverityInfo:
			c.Info++
		}
	}
	return c
}
