// End-to-end HTTP API tests of the Phase 2 simulation workbench routes
// (PROJECT_BRIEF_P2 §7 — API-P2) over the real app wiring (internal/app)
// mounted with httptest.NewServer, against the three committed seeded
// workbooks (testdata/*.xlsx). The response structs re-declare the exact §7
// field names, so a field-name regression fails to compile.
//
// TestAPILabFullFlow walks the complete lab journey: workspace materialize →
// list/detail → topology/path → ops (auto-checks, malformed, semantic
// failure with atomic state) → checks run/list/get → run diff → changesets →
// undo (LIFO, already-reverted) → checkpoints (create/break/restore/no-op/
// missing file) → lab report (HTML + JSON) → workspace delete. The error
// paths of workspace creation and the 404 surface of every route are covered
// by TestAPILabWorkspaceErrors.

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mmqz/netops-lab/internal/app"
	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/testfixtures"
)

// newLabTestServer wires the full application against a throwaway database
// and upload directory (both under one temp dir) and serves it over
// httptest. The directory is returned — the checkpoint tests must reach the
// snapshot files under <dir>/checkpoints/<workspaceId>/.
func newLabTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	a, err := app.New(app.Config{
		DBPath:  filepath.Join(dir, "netops.db"),
		DataDir: dir,
	})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	srv := httptest.NewServer(a.Routes())
	t.Cleanup(func() {
		srv.Close()
		if err := a.Close(); err != nil {
			t.Errorf("app.Close: %v", err)
		}
	})
	return srv, dir
}

// importLabWorkbooks imports the three seeded workbooks (canonical order
// devices → ippools → circuits) through the §9 flow and returns their batch
// ids in import order.
func importLabWorkbooks(t *testing.T, srv *httptest.Server) (devices, pools, circuits string) {
	t.Helper()
	devices = uploadAndConfirm(t, srv, "devices.xlsx",
		sheetExpectation{
			name: testfixtures.DeviceSheetName, rowCount: 30, columns: len(testfixtures.DeviceHeaders),
			header: "设备编码", field: "device_code", confidence: 0.75, firstSample: "DEV-001",
		},
		[4]int{30, 27, 0, 3}, map[string]int{"error": 4, "warning": 1, "info": 27})
	pools = uploadAndConfirm(t, srv, "ippools.xlsx",
		sheetExpectation{
			name: testfixtures.IPPoolSheetName, rowCount: 20, columns: len(testfixtures.IPPoolHeaders),
			header: "CIDR", field: "cidr", confidence: 0.9, firstSample: "10.40.1.0/24",
		},
		[4]int{20, 16, 0, 4}, map[string]int{"error": 6, "warning": 1, "info": 12})
	circuits = uploadAndConfirm(t, srv, "circuits.xlsx",
		sheetExpectation{
			name: testfixtures.CircuitSheetName, rowCount: 25, columns: len(testfixtures.CircuitHeaders),
			header: "带宽Mbps", field: "bandwidth_mbps", confidence: 0.75, firstSample: "1000",
		},
		[4]int{25, 21, 0, 4}, map[string]int{"error": 6, "warning": 1, "info": 1})
	return devices, pools, circuits
}

// labJSON marshals v (request bodies).
func labJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	return b
}

// assertSevCounts compares the {error,warning,info} block exactly.
func assertSevCounts(t *testing.T, ctx string, got, want map[string]int) {
	t.Helper()
	for _, sev := range []string{"error", "warning", "info"} {
		if got[sev] != want[sev] {
			t.Errorf("%s: counts[%q] = %d, want %d (got %v)", ctx, sev, got[sev], want[sev], got)
		}
	}
}

// expectError asserts the status and the error-envelope code.
func expectError(t *testing.T, ctx string, resp *http.Response, body []byte, status int, code string) {
	t.Helper()
	if resp.StatusCode != status {
		t.Fatalf("%s: status = %d, want %d\nbody: %s", ctx, resp.StatusCode, status, body)
	}
	var er errorResp
	decodeJSON(t, ctx, body, &er)
	if er.Error.Code != code {
		t.Errorf("%s: error.code = %q, want %q (message %q)", ctx, er.Error.Code, code, er.Error.Message)
	}
}

// ---------------------------------------------------------------- §7 shapes —

// labMaterializeCounts mirrors the counts block of POST /api/workspaces.
type labMaterializeCounts struct {
	Devices    int `json:"devices"`
	Interfaces int `json:"interfaces"`
	Links      int `json:"links"`
}

// labMaterializeResp mirrors the 201 body of POST /api/workspaces.
type labMaterializeResp struct {
	WorkspaceID    string               `json:"workspaceId"`
	Name           string               `json:"name"`
	SourceBatchIDs []string             `json:"sourceBatchIds"`
	Counts         labMaterializeCounts `json:"counts"`
	WorkspaceHash  string               `json:"workspaceHash"`
}

// labWSItem mirrors one GET /api/workspaces items[] entry.
type labWSItem struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Description     string  `json:"description"`
	DeviceCount     int     `json:"deviceCount"`
	InterfaceCount  int     `json:"interfaceCount"`
	LinkCount       int     `json:"linkCount"`
	UpDeviceCount   int     `json:"upDeviceCount"`
	ChangeSetCount  int     `json:"changesetCount"`
	CheckpointCount int     `json:"checkpointCount"`
	LastRunID       *string `json:"lastRunId"`
	LastRunScore    *int    `json:"lastRunScore"`
	CreatedAt       string  `json:"createdAt"`
}

// labWSPage mirrors the GET /api/workspaces envelope.
type labWSPage struct {
	Items []labWSItem `json:"items"`
	pageEnvelope
}

// labDetailCounts mirrors the counts block of GET /api/workspaces/{id}.
type labDetailCounts struct {
	Devices      int `json:"devices"`
	UpDevices    int `json:"upDevices"`
	DownDevices  int `json:"downDevices"`
	Interfaces   int `json:"interfaces"`
	UpInterfaces int `json:"upInterfaces"`
	Links        int `json:"links"`
	UpLinks      int `json:"upLinks"`
	ChangeSets   int `json:"changesets"`
	Checkpoints  int `json:"checkpoints"`
}

// labLatestChangeset mirrors the latestChangeset block.
type labLatestChangeset struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Trigger     string `json:"trigger"`
	Status      string `json:"status"`
	OpsApplied  int    `json:"opsApplied"`
	CreatedAt   string `json:"createdAt"`
}

// labLatestRun mirrors the latestRun block.
type labLatestRun struct {
	RunID  string         `json:"runId"`
	Score  int            `json:"score"`
	Counts map[string]int `json:"counts"`
}

// labWSDetail mirrors GET /api/workspaces/{id}.
type labWSDetail struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	Description     string              `json:"description"`
	Status          string              `json:"status"`
	SourceBatchIDs  []string            `json:"sourceBatchIds"`
	Counts          labDetailCounts     `json:"counts"`
	WorkspaceHash   string              `json:"workspaceHash"`
	LatestChangeSet *labLatestChangeset `json:"latestChangeset"`
	LatestRun       *labLatestRun       `json:"latestRun"`
	CreatedAt       string              `json:"createdAt"`
	UpdatedAt       string              `json:"updatedAt"`
}

// labTopoIface mirrors one node interfaces[] entry.
type labTopoIface struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	IPv4Addr   string `json:"ipv4Addr"`
	IPv4Prefix int    `json:"ipv4Prefix"`
}

// labTopoNode mirrors one GET /api/workspaces/{id}/topology nodes[] entry.
type labTopoNode struct {
	ID         string         `json:"id"`
	Code       string         `json:"code"`
	Name       string         `json:"name"`
	Status     string         `json:"status"`
	Interfaces []labTopoIface `json:"interfaces"`
}

// labTopoEdge mirrors one edges[] entry (field names pinned by §7, incl.
// aDeviceId/aDeviceCode).
type labTopoEdge struct {
	ID           string `json:"id"`
	AInterfaceID string `json:"aInterfaceId"`
	ADeviceID    string `json:"aDeviceId"`
	ADeviceCode  string `json:"aDeviceCode"`
	ZInterfaceID string `json:"zInterfaceId"`
	ZDeviceID    string `json:"zDeviceId"`
	ZDeviceCode  string `json:"zDeviceCode"`
	Status       string `json:"status"`
	Label        string `json:"label"`
}

// labTopoResp mirrors GET /api/workspaces/{id}/topology.
type labTopoResp struct {
	WorkspaceID   string        `json:"workspaceId"`
	WorkspaceHash string        `json:"workspaceHash"`
	Nodes         []labTopoNode `json:"nodes"`
	Edges         []labTopoEdge `json:"edges"`
}

// labPathHop mirrors one hops[] entry of the path query.
type labPathHop struct {
	DeviceID string `json:"deviceId"`
	Code     string `json:"code"`
}

// labPathResp mirrors POST /api/workspaces/{id}/topology/path.
type labPathResp struct {
	Reachable  bool         `json:"reachable"`
	Hops       []labPathHop `json:"hops"`
	PathLength int          `json:"pathLength"`
}

// labChecksSummary mirrors the checks block embedded in apply/undo/restore
// responses.
type labChecksSummary struct {
	RunID  string         `json:"runId"`
	Counts map[string]int `json:"counts"`
	Score  int            `json:"score"`
}

// labApplyResp mirrors POST /api/workspaces/{id}/ops.
type labApplyResp struct {
	ChangeSetID   string            `json:"changesetId"`
	Trigger       string            `json:"trigger"`
	OpsApplied    int               `json:"opsApplied"`
	WorkspaceHash string            `json:"workspaceHash"`
	Checks        *labChecksSummary `json:"checks"`
}

// labRunResp mirrors POST /api/workspaces/{id}/checks/run.
type labRunResp struct {
	RunID         string         `json:"runId"`
	Trigger       string         `json:"trigger"`
	Counts        map[string]int `json:"counts"`
	Score         int            `json:"score"`
	WorkspaceHash string         `json:"workspaceHash"`
	DurationMs    int            `json:"durationMs"`
}

// labRunItem mirrors one GET /api/workspaces/{id}/checks items[] entry.
type labRunItem struct {
	RunID       string         `json:"runId"`
	ChangeSetID *string        `json:"changesetId"`
	Trigger     string         `json:"trigger"`
	StartedAt   string         `json:"startedAt"`
	Counts      map[string]int `json:"counts"`
	Score       int            `json:"score"`
}

// labRunPage mirrors the checks list envelope.
type labRunPage struct {
	Items []labRunItem `json:"items"`
	pageEnvelope
}

// labResultRow mirrors one results[] entry of the run detail.
type labResultRow struct {
	Seq        int             `json:"seq"`
	CheckCode  string          `json:"checkCode"`
	Severity   string          `json:"severity"`
	Passed     bool            `json:"passed"`
	EntityKind string          `json:"entityKind"`
	EntityID   string          `json:"entityId"`
	Message    string          `json:"message"`
	Detail     json.RawMessage `json:"detail"`
}

// labRunDetail mirrors GET /api/workspaces/{id}/checks/{runId}.
type labRunDetail struct {
	RunID       string         `json:"runId"`
	WorkspaceID string         `json:"workspaceId"`
	ChangeSetID *string        `json:"changesetId"`
	Trigger     string         `json:"trigger"`
	RuleVersion string         `json:"ruleVersion"`
	StartedAt   string         `json:"startedAt"`
	DurationMs  int            `json:"durationMs"`
	Score       int            `json:"score"`
	Counts      map[string]int `json:"counts"`
	Results     []labResultRow `json:"results"`
}

// labDiffRef mirrors one from/to ref of the diff.
type labDiffRef struct {
	RunID     string         `json:"runId"`
	StartedAt string         `json:"startedAt"`
	Score     int            `json:"score"`
	Counts    map[string]int `json:"counts"`
}

// labDiffFinding mirrors one findingsAdded/Removed entry.
type labDiffFinding struct {
	CheckCode string          `json:"checkCode"`
	Severity  string          `json:"severity"`
	Message   string          `json:"message"`
	Detail    json.RawMessage `json:"detail"`
}

// labPair mirrors one connectivityLost/Gained entry.
type labPair struct {
	FromDevice string `json:"fromDevice"`
	ToDevice   string `json:"toDevice"`
}

// labDiffResp mirrors GET /api/workspaces/{id}/checks/{runId}/diff.
type labDiffResp struct {
	From               labDiffRef       `json:"from"`
	To                 labDiffRef       `json:"to"`
	FindingsAdded      []labDiffFinding `json:"findingsAdded"`
	FindingsRemoved    []labDiffFinding `json:"findingsRemoved"`
	ConnectivityLost   []labPair        `json:"connectivityLost"`
	ConnectivityGained []labPair        `json:"connectivityGained"`
	ScoreDelta         int              `json:"scoreDelta"`
}

// labCSItem mirrors one GET /api/workspaces/{id}/changesets items[] entry.
type labCSItem struct {
	ID          string  `json:"id"`
	Description string  `json:"description"`
	Trigger     string  `json:"trigger"`
	Status      string  `json:"status"`
	OpsApplied  int     `json:"opsApplied"`
	CreatedAt   string  `json:"createdAt"`
	RevertedAt  *string `json:"revertedAt"`
}

// labCSPage mirrors the changesets list envelope.
type labCSPage struct {
	Items []labCSItem `json:"items"`
	pageEnvelope
}

// labUndoResp mirrors POST /api/workspaces/{id}/undo.
type labUndoResp struct {
	ChangeSetID   string            `json:"changesetId"`
	Trigger       string            `json:"trigger"`
	Reverted      string            `json:"reverted"`
	OpsApplied    int               `json:"opsApplied"`
	WorkspaceHash string            `json:"workspaceHash"`
	Checks        *labChecksSummary `json:"checks"`
}

// labCheckpoint mirrors POST /api/workspaces/{id}/checkpoints and one
// checkpoints list entry.
type labCheckpoint struct {
	CheckpointID  string `json:"checkpointId"`
	Label         string `json:"label"`
	WorkspaceHash string `json:"workspaceHash"`
	CreatedAt     string `json:"createdAt"`
}

// labCheckpointsResp mirrors GET /api/workspaces/{id}/checkpoints.
type labCheckpointsResp struct {
	Items []labCheckpoint `json:"items"`
	Total int             `json:"total"`
}

// labRestoreResp mirrors POST /api/workspaces/{id}/checkpoints/{cpId}/restore.
type labRestoreResp struct {
	CheckpointID   string            `json:"checkpointId"`
	Label          string            `json:"label"`
	ChangeSetID    *string           `json:"changesetId"`
	Note           string            `json:"note"`
	OpsApplied     int               `json:"opsApplied"`
	RestoredToHash string            `json:"restoredToHash"`
	WorkspaceHash  string            `json:"workspaceHash"`
	Checks         *labChecksSummary `json:"checks"`
}

// labDeleteResp mirrors DELETE /api/workspaces/{id}.
type labDeleteResp struct {
	Deleted bool `json:"deleted"`
	Removed struct {
		Devices     int64 `json:"devices"`
		Interfaces  int64 `json:"interfaces"`
		Links       int64 `json:"links"`
		ChangeSets  int64 `json:"changesets"`
		CheckRuns   int64 `json:"checkRuns"`
		Checkpoints int64 `json:"checkpoints"`
	} `json:"removed"`
}

// labReportResp mirrors GET /api/lab/report?format=json.
type labReportResp struct {
	GeneratedAt   string `json:"generatedAt"`
	EngineVersion string `json:"engineVersion"`
	Workspace     struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		WorkspaceHash string `json:"workspaceHash"`
		Counts        struct {
			Devices     int `json:"devices"`
			UpDevices   int `json:"upDevices"`
			DownDevices int `json:"downDevices"`
			Interfaces  int `json:"interfaces"`
			Links       int `json:"links"`
			UpLinks     int `json:"upLinks"`
			ChangeSets  int `json:"changesets"`
			Checkpoints int `json:"checkpoints"`
		} `json:"counts"`
	} `json:"workspace"`
	Score  *int `json:"score"`
	Checks struct {
		Counts  map[string]int `json:"counts"`
		Results []struct {
			CheckCode string          `json:"checkCode"`
			Severity  string          `json:"severity"`
			Message   string          `json:"message"`
			Detail    json.RawMessage `json:"detail"`
		} `json:"results"`
	} `json:"checks"`
	ChangeSets []struct {
		ID         string  `json:"id"`
		Trigger    string  `json:"trigger"`
		Status     string  `json:"status"`
		RevertedAt *string `json:"revertedAt"`
	} `json:"changesets"`
	Checkpoints []labCheckpoint `json:"checkpoints"`
	Disclaimer  string          `json:"disclaimer"`
}

// ---------------------------------------------------------------- the tests —

// TestAPILabFullFlow walks the complete §7 lab journey (API-P2).
func TestAPILabFullFlow(t *testing.T) {
	srv, dir := newLabTestServer(t)
	devicesBatch, poolsBatch, circuitsBatch := importLabWorkbooks(t, srv)

	// ---- POST /api/workspaces: 201 + deterministic materialization.
	resp, body := doRequest(t, srv, http.MethodPost, "/api/workspaces",
		labJSON(t, map[string]any{"name": "lab-e2e", "description": "API-P2 end-to-end"}),
		"application/json")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create workspace: status = %d, want 201\nbody: %s", resp.StatusCode, body)
	}
	var created labMaterializeResp
	decodeJSON(t, "create workspace", body, &created)
	if created.WorkspaceID == "" || created.Name != "lab-e2e" {
		t.Fatalf("create workspace: %+v", created)
	}
	if created.Counts.Devices != 27 || created.Counts.Interfaces != 40 || created.Counts.Links != 20 {
		t.Fatalf("create workspace counts = %+v, want devices 27 interfaces 40 links 20", created.Counts)
	}
	if len(created.WorkspaceHash) != 64 {
		t.Fatalf("create workspace hash length = %d, want 64", len(created.WorkspaceHash))
	}
	if len(created.SourceBatchIDs) != 3 || created.SourceBatchIDs[0] != devicesBatch ||
		created.SourceBatchIDs[1] != poolsBatch || created.SourceBatchIDs[2] != circuitsBatch {
		t.Fatalf("create workspace sourceBatchIds = %v, want the three batches in import order",
			created.SourceBatchIDs)
	}
	ws := created.WorkspaceID
	wsPath := "/api/workspaces/" + ws

	// ---- GET /api/workspaces: field set incl. lastRunId null.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/workspaces", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list workspaces: status = %d, want 200", resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("list workspaces: cache-control = %q, want no-store (inherited middleware)", cc)
	}
	var list labWSPage
	decodeJSON(t, "list workspaces", body, &list)
	if list.Total != 1 || list.Page != 1 || list.PageSize != 20 || len(list.Items) != 1 {
		t.Fatalf("list workspaces envelope = %+v, want total 1 / page 1 / pageSize 20 / 1 item", list)
	}
	item := list.Items[0]
	if item.ID != ws || item.Name != "lab-e2e" || item.Description != "API-P2 end-to-end" {
		t.Fatalf("list workspaces item = %+v", item)
	}
	if item.DeviceCount != 27 || item.InterfaceCount != 40 || item.LinkCount != 20 || item.UpDeviceCount != 23 {
		t.Fatalf("list workspaces counts = %+v, want 27/40/20 devices/interfaces/links + 23 up devices", item)
	}
	if item.ChangeSetCount != 0 || item.CheckpointCount != 0 {
		t.Fatalf("list workspaces changeset/checkpoint counts = %d/%d, want 0/0",
			item.ChangeSetCount, item.CheckpointCount)
	}
	if item.LastRunID != nil || item.LastRunScore != nil {
		t.Fatalf("list workspaces lastRun = %v/%v, want null before the first run", item.LastRunID, item.LastRunScore)
	}
	if item.CreatedAt == "" {
		t.Fatalf("list workspaces createdAt is empty")
	}

	// ---- GET /api/workspaces/{id}: detail with up/down splits.
	resp, body = doRequest(t, srv, http.MethodGet, wsPath, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("workspace detail: status = %d, want 200", resp.StatusCode)
	}
	var detail labWSDetail
	decodeJSON(t, "workspace detail", body, &detail)
	if detail.ID != ws || detail.Name != "lab-e2e" || detail.Status != "active" {
		t.Fatalf("workspace detail meta = %+v", detail)
	}
	if len(detail.SourceBatchIDs) != 3 || detail.SourceBatchIDs[0] != devicesBatch {
		t.Fatalf("workspace detail sourceBatchIds = %v", detail.SourceBatchIDs)
	}
	if got := detail.Counts; got.Devices != 27 || got.UpDevices != 23 || got.DownDevices != 4 ||
		got.Interfaces != 40 || got.UpInterfaces != 40 || got.Links != 20 || got.UpLinks != 20 ||
		got.ChangeSets != 0 || got.Checkpoints != 0 {
		t.Fatalf("workspace detail counts = %+v, want 27/23/4/40/40/20/20/0/0", got)
	}
	if detail.WorkspaceHash != created.WorkspaceHash {
		t.Fatalf("workspace detail hash = %s, want the materialization hash %s",
			detail.WorkspaceHash, created.WorkspaceHash)
	}
	if detail.LatestChangeSet != nil || detail.LatestRun != nil {
		t.Fatalf("workspace detail latestChangeset/latestRun = %v/%v, want null/null",
			detail.LatestChangeSet, detail.LatestRun)
	}
	if detail.CreatedAt == "" || detail.UpdatedAt == "" {
		t.Fatalf("workspace detail timestamps are empty")
	}

	// ---- GET /api/workspaces/{id}/topology: nodes/edges with the exact §7
	// field names and sorting (nodes by code, interfaces by name, edges by
	// numeric id).
	resp, body = doRequest(t, srv, http.MethodGet, wsPath+"/topology", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("topology: status = %d, want 200", resp.StatusCode)
	}
	var topo labTopoResp
	decodeJSON(t, "topology", body, &topo)
	if topo.WorkspaceID != ws || topo.WorkspaceHash != created.WorkspaceHash {
		t.Fatalf("topology meta = %s/%s", topo.WorkspaceID, topo.WorkspaceHash)
	}
	if len(topo.Nodes) != 27 || len(topo.Edges) != 20 {
		t.Fatalf("topology has %d nodes / %d edges, want 27/20", len(topo.Nodes), len(topo.Edges))
	}
	for i := 1; i < len(topo.Nodes); i++ { // nodes sorted by code
		if topo.Nodes[i-1].Code >= topo.Nodes[i].Code {
			t.Fatalf("topology nodes not code-sorted at %d: %s >= %s", i, topo.Nodes[i-1].Code, topo.Nodes[i].Code)
		}
	}
	first := topo.Nodes[0]
	if first.Code != "DEV-001" || first.ID != "vd-1" || first.Status != "up" || first.Name == "" {
		t.Fatalf("topology nodes[0] = %+v, want DEV-001/vd-1/up", first)
	}
	for i := 1; i < len(first.Interfaces); i++ { // interfaces sorted by name
		if first.Interfaces[i-1].Name >= first.Interfaces[i].Name {
			t.Fatalf("node DEV-001 interfaces not name-sorted at %d", i)
		}
	}
	iface0 := first.Interfaces[0]
	if iface0.ID != "if-1" || iface0.Name != "eth0" || iface0.Status != "up" ||
		iface0.IPv4Addr != "" || iface0.IPv4Prefix != -1 {
		t.Fatalf("topology nodes[0].interfaces[0] = %+v, want if-1/eth0/up/\"\"/-1 (unconfigured)", iface0)
	}
	if topo.Nodes[26].Code != "DEV-030" { // DEV-001..DEV-030 minus the 3 failed import rows
		t.Fatalf("topology nodes[26].code = %s, want DEV-030", topo.Nodes[26].Code)
	}
	// Edges ln-1..ln-20 in numeric order; ln-1 is CKT-001 (DEV-001 ↔ DEV-002).
	for i, wantID := 0, "ln-1"; i < len(topo.Edges); i, wantID = i+1, nextSimID(wantID) {
		if topo.Edges[i].ID != wantID {
			t.Fatalf("topology edges[%d].id = %s, want %s (numeric order)", i, topo.Edges[i].ID, wantID)
		}
	}
	edge0 := topo.Edges[0]
	if edge0.AInterfaceID != "if-1" || edge0.ADeviceID != "vd-1" || edge0.ADeviceCode != "DEV-001" ||
		edge0.ZInterfaceID != "if-2" || edge0.ZDeviceID != "vd-2" || edge0.ZDeviceCode != "DEV-002" ||
		edge0.Status != "up" || edge0.Label != "CKT-001" {
		t.Fatalf("topology edges[0] = %+v, want CKT-001 with aDeviceId/aDeviceCode vd-1/DEV-001 and z vd-2/DEV-002", edge0)
	}

	// ---- POST /api/workspaces/{id}/topology/path: known 2-hop pair, self,
	// unknown.
	resp, body = doRequest(t, srv, http.MethodPost, wsPath+"/topology/path",
		labJSON(t, map[string]string{"fromDevice": "DEV-001", "toDevice": "DEV-003"}), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("path DEV-001→DEV-003: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	var path labPathResp
	decodeJSON(t, "path DEV-001→DEV-003", body, &path)
	if !path.Reachable || path.PathLength != 2 || len(path.Hops) != 3 {
		t.Fatalf("path DEV-001→DEV-003 = %+v, want reachable/2/3 hops", path)
	}
	for i, code := range []string{"DEV-001", "DEV-002", "DEV-003"} {
		if path.Hops[i].Code != code || path.Hops[i].DeviceID == "" {
			t.Fatalf("path DEV-001→DEV-003 hops[%d] = %+v, want %s", i, path.Hops[i], code)
		}
	}
	resp, body = doRequest(t, srv, http.MethodPost, wsPath+"/topology/path",
		labJSON(t, map[string]string{"fromDevice": "DEV-005", "toDevice": "DEV-005"}), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("self path: status = %d, want 200", resp.StatusCode)
	}
	decodeJSON(t, "self path", body, &path)
	if !path.Reachable || path.PathLength != 0 || len(path.Hops) != 1 || path.Hops[0].Code != "DEV-005" {
		t.Fatalf("self path = %+v, want reachable/0/1 hop", path)
	}
	resp, body = doRequest(t, srv, http.MethodPost, wsPath+"/topology/path",
		labJSON(t, map[string]string{"fromDevice": "DEV-001", "toDevice": "DEV-999"}), "application/json")
	expectError(t, "path unknown device", resp, body, http.StatusNotFound, "not_found")
	resp, body = doRequest(t, srv, http.MethodPost, wsPath+"/topology/path",
		[]byte(`{"fromDevice":"DEV-001"}`), "application/json")
	expectError(t, "path missing toDevice", resp, body, http.StatusBadRequest, "invalid_request")

	// ---- POST /api/workspaces/{id}/checks/run: baseline (95 = 100 − 5·5).
	resp, body = doRequest(t, srv, http.MethodPost, wsPath+"/checks/run", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("checks run: status = %d, want 200", resp.StatusCode)
	}
	var run1 labRunResp
	decodeJSON(t, "checks run", body, &run1)
	if run1.RunID != "run-1" || run1.Trigger != "manual" {
		t.Fatalf("checks run = %+v, want run-1/manual", run1)
	}
	assertSevCounts(t, "checks run", run1.Counts, map[string]int{"error": 0, "warning": 5, "info": 36})
	if run1.Score != 95 {
		t.Fatalf("checks run score = %d, want 95", run1.Score)
	}
	if run1.WorkspaceHash != created.WorkspaceHash || run1.DurationMs < 0 {
		t.Fatalf("checks run hash/duration = %s/%d", run1.WorkspaceHash, run1.DurationMs)
	}

	// ---- the list now reports lastRunId/lastRunScore.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/workspaces", nil, "")
	decodeJSON(t, "list workspaces after run", body, &list)
	if list.Items[0].LastRunID == nil || *list.Items[0].LastRunID != "run-1" ||
		list.Items[0].LastRunScore == nil || *list.Items[0].LastRunScore != 95 {
		t.Fatalf("list lastRun = %v/%v, want run-1/95", list.Items[0].LastRunID, list.Items[0].LastRunScore)
	}

	// ---- POST ops: the 3-op fault (two duplicate addresses + link down) —
	// auto-checks attached to the changeset.
	opsFault := `{"ops":[
                {"op":"config_interface_ipv4","interfaceId":"if-1","ipv4Addr":"10.0.0.1","ipv4Prefix":24},
                {"op":"config_interface_ipv4","interfaceId":"if-4","ipv4Addr":"10.0.0.1","ipv4Prefix":24},
                {"op":"set_link_status","linkId":"ln-2","status":"down"}
        ],"description":"fault injection"}`
	resp, body = doRequest(t, srv, http.MethodPost, wsPath+"/ops", []byte(opsFault), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ops fault: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	var applied labApplyResp
	decodeJSON(t, "ops fault", body, &applied)
	if applied.ChangeSetID != "cs-1" || applied.Trigger != "user" || applied.OpsApplied != 3 {
		t.Fatalf("ops fault = %+v, want cs-1/user/3", applied)
	}
	if applied.WorkspaceHash == "" || applied.WorkspaceHash == created.WorkspaceHash {
		t.Fatalf("ops fault hash = %s, want a changed hash", applied.WorkspaceHash)
	}
	if applied.Checks == nil {
		t.Fatalf("ops fault: checks summary missing")
	}
	if applied.Checks.RunID != "run-2" || applied.Checks.Score != 90 {
		t.Fatalf("ops fault checks = %+v, want run-2/90", applied.Checks)
	}
	assertSevCounts(t, "ops fault checks", applied.Checks.Counts, map[string]int{"error": 1, "warning": 5, "info": 35})

	// ---- GET run detail: findings first, then the 5 baselines; detail is a
	// parsed JSON object.
	resp, body = doRequest(t, srv, http.MethodGet, wsPath+"/checks/run-2", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("run-2 detail: status = %d, want 200", resp.StatusCode)
	}
	var runDetail labRunDetail
	decodeJSON(t, "run-2 detail", body, &runDetail)
	if runDetail.RunID != "run-2" || runDetail.WorkspaceID != ws {
		t.Fatalf("run-2 detail meta = %s/%s", runDetail.RunID, runDetail.WorkspaceID)
	}
	if runDetail.ChangeSetID == nil || *runDetail.ChangeSetID != "cs-1" {
		t.Fatalf("run-2 detail changesetId = %v, want cs-1 (auto_after_ops)", runDetail.ChangeSetID)
	}
	if runDetail.Trigger != "auto_after_ops" || runDetail.RuleVersion != "1.0.0" {
		t.Fatalf("run-2 detail trigger/ruleVersion = %s/%s", runDetail.Trigger, runDetail.RuleVersion)
	}
	assertSevCounts(t, "run-2 detail", runDetail.Counts, map[string]int{"error": 1, "warning": 5, "info": 35})
	if runDetail.Score != 90 {
		t.Fatalf("run-2 detail score = %d, want 90", runDetail.Score)
	}
	if len(runDetail.Results) != 46 { // 41 findings + 5 baselines
		t.Fatalf("run-2 has %d results, want 46 (41 findings + 5 baselines)", len(runDetail.Results))
	}
	if runDetail.Results[0].Passed || runDetail.Results[0].CheckCode != "CHK-001" ||
		runDetail.Results[0].Severity != "error" || runDetail.Results[0].Seq != 1 {
		t.Fatalf("run-2 results[0] = %+v, want the CHK-001 finding at seq 1", runDetail.Results[0])
	}
	if runDetail.Results[40].Passed || !runDetail.Results[41].Passed {
		t.Fatalf("findings must precede baselines: results[40].Passed=%v (want false), results[41].Passed=%v (want true)",
			runDetail.Results[40].Passed, runDetail.Results[41].Passed)
	}
	// The CHK-001 finding detail is a JSON OBJECT (not a string).
	var dupDetail struct {
		Addr       string `json:"addr"`
		Interfaces []struct {
			DeviceCode string `json:"deviceCode"`
			Name       string `json:"name"`
		} `json:"interfaces"`
	}
	if err := json.Unmarshal(runDetail.Results[0].Detail, &dupDetail); err != nil {
		t.Fatalf("CHK-001 detail is not a JSON object: %v (%s)", err, runDetail.Results[0].Detail)
	}
	if dupDetail.Addr != "10.0.0.1" || len(dupDetail.Interfaces) != 2 ||
		dupDetail.Interfaces[0].DeviceCode != "DEV-001" || dupDetail.Interfaces[1].DeviceCode != "DEV-003" {
		t.Fatalf("CHK-001 detail = %+v, want addr 10.0.0.1 with DEV-001 and DEV-003", dupDetail)
	}
	// The 5 baselines at the tail, in catalog order, message "ok".
	for i, code := range []string{"CHK-001", "CHK-002", "CHK-003", "CHK-004", "CHK-005"} {
		row := runDetail.Results[41+i]
		if !row.Passed || row.CheckCode != code || row.Message != "ok" {
			t.Fatalf("run-2 baseline row %d = %+v, want passed %s ok", i, row, code)
		}
	}

	// ---- ops malformed: unknown code, restore op, bad value, bad type,
	// broken JSON, missing ops, EMPTY and NULL ops arrays — all 400
	// invalid_request, no state change.
	for name, opsBody := range map[string]string{
		"unknown op code": `{"ops":[{"op":"explode","deviceId":"vd-1"}]}`,
		"restore op":      `{"ops":[{"op":"restore_link","link":{"id":"ln-1"}}]}`,
		"bad status":      `{"ops":[{"op":"set_link_status","linkId":"ln-1","status":"sideways"}]}`,
		"bad type":        `{"ops":[{"op":"create_interface","deviceId":"vd-1","name":"eth9","ipv4Addr":"10.9.9.9","ipv4Prefix":"24"}]}`,
		"field typo":      `{"ops":[{"op":"set_link_status","linId":"ln-1","status":"down"}]}`,
		"broken JSON":     `{`,
		"missing ops":     `{"description":"no ops"}`,
		"empty ops":       `{"ops":[]}`,
		"null ops":        `{"ops":null}`,
	} {
		resp, body = doRequest(t, srv, http.MethodPost, wsPath+"/ops", []byte(opsBody), "application/json")
		expectError(t, "ops malformed: "+name, resp, body, http.StatusBadRequest, "invalid_request")
		// FX1-E (AUD2-3): the 400 envelope must carry a NON-EMPTY
		// message — v0.2.0's simErrorMessages had no invalid_request
		// entry and apply-op 400s answered "" (the UI rendered a
		// generic 请求失败).
		var erMsg errorResp
		decodeJSON(t, "ops malformed: "+name, body, &erMsg)
		if strings.TrimSpace(erMsg.Error.Message) == "" {
			t.Errorf("ops malformed: %s: error.message is empty, want the engine's rejection text", name)
		}
	}
	// FX1-A (AUD1-3) wire proof: the empty-ops rejection names the exact
	// reason, and none of the 400s above wrote a changeset row — the
	// changesets list below pins the total at exactly cs-1 (applied) +
	// cs-2 (failed, from the semantic-failure case that follows).
	resp, body = doRequest(t, srv, http.MethodPost, wsPath+"/ops", []byte(`{"ops":[]}`), "application/json")
	expectError(t, "ops empty array (replay)", resp, body, http.StatusBadRequest, "invalid_request")
	var erEmpty errorResp
	decodeJSON(t, "ops empty array (replay)", body, &erEmpty)
	if !strings.Contains(erEmpty.Error.Message, "ops must contain at least one operation") {
		t.Errorf("ops empty array: error.message = %q, want the at-least-one-operation wording", erEmpty.Error.Message)
	}

	// ---- ops semantic failure: bogus link id ⇒ 422 op_failed with the §7
	// message format; state untouched (hash unchanged).
	opsBad := `{"ops":[
                {"op":"set_device_status","deviceId":"vd-5","status":"down"},
                {"op":"set_link_status","linkId":"ln-99","status":"down"}
        ]}`
	resp, body = doRequest(t, srv, http.MethodPost, wsPath+"/ops", []byte(opsBad), "application/json")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("ops semantic failure: status = %d, want 422\nbody: %s", resp.StatusCode, body)
	}
	var er errorResp
	decodeJSON(t, "ops semantic failure", body, &er)
	if er.Error.Code != "op_failed" {
		t.Fatalf("ops semantic failure code = %q, want op_failed", er.Error.Code)
	}
	wantMsg := "op 1 (set_link_status): link not found: ln-99 [changeset cs-2 recorded as failed]"
	if er.Error.Message != wantMsg {
		t.Fatalf("ops semantic failure message = %q, want %q", er.Error.Message, wantMsg)
	}
	resp, body = doRequest(t, srv, http.MethodGet, wsPath+"/topology", nil, "")
	decodeJSON(t, "topology after failed ops", body, &topo)
	if topo.WorkspaceHash != applied.WorkspaceHash {
		t.Fatalf("topology hash after failed ops = %s, want the unchanged %s (atomic rollback)",
			topo.WorkspaceHash, applied.WorkspaceHash)
	}

	// ---- checks list: desc by run id, changesetId null for the manual run.
	resp, body = doRequest(t, srv, http.MethodGet, wsPath+"/checks", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("checks list: status = %d, want 200", resp.StatusCode)
	}
	var runs labRunPage
	decodeJSON(t, "checks list", body, &runs)
	if runs.Total != 2 || len(runs.Items) != 2 {
		t.Fatalf("checks list total = %d (%d items), want 2/2", runs.Total, len(runs.Items))
	}
	if runs.Items[0].RunID != "run-2" || runs.Items[1].RunID != "run-1" {
		t.Fatalf("checks list order = [%s %s], want [run-2 run-1] (desc)", runs.Items[0].RunID, runs.Items[1].RunID)
	}
	if runs.Items[0].ChangeSetID == nil || *runs.Items[0].ChangeSetID != "cs-1" ||
		runs.Items[0].Trigger != "auto_after_ops" {
		t.Fatalf("checks list items[0] = %+v, want run-2 tied to cs-1 (auto_after_ops)", runs.Items[0])
	}
	if runs.Items[1].ChangeSetID != nil || runs.Items[1].Trigger != "manual" || runs.Items[1].Score != 95 {
		t.Fatalf("checks list items[1] = %+v, want run-1 manual with changesetId null", runs.Items[1])
	}

	// ---- a second workspace (devices only) + a run for cross-workspace 404s.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/workspaces",
		labJSON(t, map[string]any{"name": "lab-other", "sourceBatchIds": []string{devicesBatch}}),
		"application/json")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create second workspace: status = %d, want 201\nbody: %s", resp.StatusCode, body)
	}
	var other labMaterializeResp
	decodeJSON(t, "create second workspace", body, &other)
	if other.Counts.Devices != 27 || other.Counts.Links != 0 || other.Counts.Interfaces != 0 {
		t.Fatalf("second workspace counts = %+v, want 27 devices / 0 links (devices-only scope)", other.Counts)
	}
	// The report of a workspace WITHOUT a check run: score null, no findings.
	resp, body = doRequest(t, srv, http.MethodGet,
		"/api/lab/report?workspaceId="+other.WorkspaceID+"&format=json", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second workspace report: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	var otherReport labReportResp
	decodeJSON(t, "second workspace report", body, &otherReport)
	if otherReport.Score != nil || len(otherReport.Checks.Results) != 0 ||
		len(otherReport.ChangeSets) != 0 || len(otherReport.Checkpoints) != 0 {
		t.Fatalf("second workspace report = score %v with %d findings, want null/0 (no run yet)",
			otherReport.Score, len(otherReport.Checks.Results))
	}
	if otherReport.Workspace.Counts.Devices != 27 || otherReport.Workspace.Counts.Links != 0 {
		t.Fatalf("second workspace report counts = %+v, want 27 devices/0 links", otherReport.Workspace.Counts)
	}
	resp, body = doRequest(t, srv, http.MethodPost, "/api/workspaces/"+other.WorkspaceID+"/checks/run", nil, "")
	decodeJSON(t, "second workspace run", body, &run1)
	if run1.RunID != "run-3" {
		t.Fatalf("second workspace run = %s, want run-3 (global sequence)", run1.RunID)
	}
	resp, body = doRequest(t, srv, http.MethodGet, wsPath+"/checks/run-3", nil, "")
	expectError(t, "cross-workspace run", resp, body, http.StatusNotFound, "not_found")

	// ---- diff: baseline → fault.
	resp, body = doRequest(t, srv, http.MethodGet, wsPath+"/checks/run-1/diff?compareTo=run-2", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("diff: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	var diff labDiffResp
	decodeJSON(t, "diff", body, &diff)
	if diff.From.RunID != "run-1" || diff.To.RunID != "run-2" {
		t.Fatalf("diff refs = %s→%s, want run-1→run-2", diff.From.RunID, diff.To.RunID)
	}
	if diff.From.Score != 95 || diff.To.Score != 90 || diff.ScoreDelta != -5 {
		t.Fatalf("diff scores = %d→%d (delta %d), want 95→90 (−5)", diff.From.Score, diff.To.Score, diff.ScoreDelta)
	}
	if len(diff.FindingsAdded) != 2 || len(diff.FindingsRemoved) != 2 {
		t.Fatalf("diff findings added/removed = %d/%d, want 2/2", len(diff.FindingsAdded), len(diff.FindingsRemoved))
	}
	added := map[string]bool{}
	for _, f := range diff.FindingsAdded {
		added[f.CheckCode] = true
		if f.Severity == "" || f.Message == "" || len(f.Detail) == 0 {
			t.Fatalf("diff finding %+v misses severity/message/detail", f)
		}
	}
	if !added["CHK-001"] || !added["CHK-004"] {
		t.Fatalf("diff findingsAdded codes = %v, want CHK-001 + CHK-004", added)
	}
	for _, f := range diff.FindingsRemoved {
		if f.CheckCode != "CHK-005" {
			t.Fatalf("diff findingsRemoved contains %s, want only CHK-005", f.CheckCode)
		}
	}
	// The finding entries carry exactly the four §7 fields.
	var findingKeys map[string]json.RawMessage
	if err := json.Unmarshal(mustMarshal(t, diff.FindingsAdded[0]), &findingKeys); err != nil {
		t.Fatalf("re-marshal findingsAdded[0]: %v", err)
	}
	if len(findingKeys) != 4 {
		t.Fatalf("diff finding keys = %v, want exactly {checkCode,severity,message,detail}", findingKeys)
	}
	for _, key := range []string{"checkCode", "severity", "message", "detail"} {
		if _, ok := findingKeys[key]; !ok {
			t.Fatalf("diff finding misses key %q (has %v)", key, findingKeys)
		}
	}
	// Full §5 predicate (v0.2.1, AUD1-2): the baseline live components are
	// {DEV-001..DEV-014} (+ {DEV-017,DEV-018} and {DEV-020,DEV-021} behind
	// the down devices DEV-015/016/019); the ln-2 fault splits the big
	// component into {DEV-001,DEV-002} vs {DEV-003..DEV-014} — 24 lost
	// pairs (previously 38 under the link-only predicate).
	if len(diff.ConnectivityLost) != 24 || len(diff.ConnectivityGained) != 0 {
		t.Fatalf("diff connectivity lost/gained = %d/%d, want 24/0", len(diff.ConnectivityLost), len(diff.ConnectivityGained))
	}
	if diff.ConnectivityGained == nil { // must render as [], not null
		t.Fatalf("diff connectivityGained is null, want an empty array")
	}
	lostPair := false
	for _, pair := range diff.ConnectivityLost {
		if pair.FromDevice == "DEV-001" && pair.ToDevice == "DEV-003" {
			lostPair = true
		}
	}
	if !lostPair {
		t.Fatalf("diff connectivityLost misses the pair DEV-001→DEV-003")
	}

	// diff error paths: same run (400 per §7), missing compareTo (400),
	// unknown run (404), cross-workspace (404).
	resp, body = doRequest(t, srv, http.MethodGet, wsPath+"/checks/run-1/diff?compareTo=run-1", nil, "")
	expectError(t, "diff same run", resp, body, http.StatusBadRequest, "invalid_request")
	resp, body = doRequest(t, srv, http.MethodGet, wsPath+"/checks/run-1/diff", nil, "")
	expectError(t, "diff missing compareTo", resp, body, http.StatusBadRequest, "invalid_request")
	resp, body = doRequest(t, srv, http.MethodGet, wsPath+"/checks/run-1/diff?compareTo=run-99", nil, "")
	expectError(t, "diff unknown run", resp, body, http.StatusNotFound, "not_found")
	resp, body = doRequest(t, srv, http.MethodGet, wsPath+"/checks/run-1/diff?compareTo=run-3", nil, "")
	expectError(t, "diff cross-workspace run", resp, body, http.StatusNotFound, "not_found")

	// ---- changesets list: desc, statuses of applied + failed.
	resp, body = doRequest(t, srv, http.MethodGet, wsPath+"/changesets", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("changesets list: status = %d, want 200", resp.StatusCode)
	}
	var css labCSPage
	decodeJSON(t, "changesets list", body, &css)
	if css.Total != 2 || len(css.Items) != 2 {
		t.Fatalf("changesets list total = %d (%d items), want 2/2", css.Total, len(css.Items))
	}
	if css.Items[0].ID != "cs-2" || css.Items[0].Status != "failed" || css.Items[0].OpsApplied != 0 {
		t.Fatalf("changesets list items[0] = %+v, want cs-2 failed/0 ops", css.Items[0])
	}
	if css.Items[1].ID != "cs-1" || css.Items[1].Trigger != "user" || css.Items[1].Status != "applied" ||
		css.Items[1].OpsApplied != 3 || css.Items[1].RevertedAt != nil {
		t.Fatalf("changesets list items[1] = %+v, want cs-1 user/applied/3/null revertedAt", css.Items[1])
	}

	// ---- undo: LIFO, already-reverted, unknown; default target.
	opsTouch := `{"ops":[{"op":"set_device_status","deviceId":"vd-5","status":"down"}],"description":"touch","runChecks":false}`
	resp, body = doRequest(t, srv, http.MethodPost, wsPath+"/ops", []byte(opsTouch), "application/json")
	decodeJSON(t, "ops touch", body, &applied)
	if applied.ChangeSetID != "cs-3" || applied.OpsApplied != 1 || applied.Checks != nil {
		t.Fatalf("ops touch = %+v, want cs-3/1 op/no checks (runChecks=false)", applied)
	}
	resp, body = doRequest(t, srv, http.MethodPost, wsPath+"/undo",
		labJSON(t, map[string]string{"changesetId": "cs-1"}), "application/json")
	expectError(t, "undo not latest", resp, body, http.StatusConflict, "not_latest")
	resp, body = doRequest(t, srv, http.MethodPost, wsPath+"/undo", []byte(`{}`), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("undo default target: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	var undone labUndoResp
	decodeJSON(t, "undo default target", body, &undone)
	if undone.ChangeSetID != "cs-4" || undone.Trigger != "undo" || undone.Reverted != "cs-3" || undone.OpsApplied != 1 {
		t.Fatalf("undo default target = %+v, want cs-4/undo/reverted cs-3/1 op", undone)
	}
	if undone.Checks == nil || undone.Checks.RunID != "run-4" {
		t.Fatalf("undo checks = %+v, want run-4 (auto-checks after undo)", undone.Checks)
	}
	resp, body = doRequest(t, srv, http.MethodPost, wsPath+"/undo",
		labJSON(t, map[string]string{"changesetId": "cs-3"}), "application/json")
	expectError(t, "undo already reverted", resp, body, http.StatusConflict, "conflict")
	// cs-1 is the newest still-effective forward change now — undoing it
	// returns the workspace EXACTLY to the materialization baseline.
	resp, body = doRequest(t, srv, http.MethodPost, wsPath+"/undo",
		labJSON(t, map[string]string{"changesetId": "cs-1"}), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("undo cs-1: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	decodeJSON(t, "undo cs-1", body, &undone)
	if undone.ChangeSetID != "cs-5" || undone.Reverted != "cs-1" || undone.OpsApplied != 3 {
		t.Fatalf("undo cs-1 = %+v, want cs-5/reverted cs-1/3 ops", undone)
	}
	if undone.WorkspaceHash != created.WorkspaceHash {
		t.Fatalf("undo cs-1 hash = %s, want the materialization baseline %s (full round trip)",
			undone.WorkspaceHash, created.WorkspaceHash)
	}
	resp, body = doRequest(t, srv, http.MethodPost, wsPath+"/undo",
		labJSON(t, map[string]string{"changesetId": "cs-1"}), "application/json")
	expectError(t, "undo already reverted (cs-1)", resp, body, http.StatusConflict, "conflict")
	resp, body = doRequest(t, srv, http.MethodPost, wsPath+"/undo",
		labJSON(t, map[string]string{"changesetId": "cs-99"}), "application/json")
	expectError(t, "undo unknown changeset", resp, body, http.StatusNotFound, "not_found")

	// ---- checkpoints: create (empty label 400), list, restore after
	// breaking changes, no-op repeat, missing file 409.
	resp, body = doRequest(t, srv, http.MethodPost, wsPath+"/checkpoints",
		labJSON(t, map[string]string{"label": "基线"}), "application/json")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("checkpoint create: status = %d, want 201\nbody: %s", resp.StatusCode, body)
	}
	var cp labCheckpoint
	decodeJSON(t, "checkpoint create", body, &cp)
	if cp.CheckpointID != "cp-1" || cp.Label != "基线" || cp.WorkspaceHash != created.WorkspaceHash || cp.CreatedAt == "" {
		t.Fatalf("checkpoint create = %+v, want cp-1 at the baseline hash", cp)
	}
	resp, body = doRequest(t, srv, http.MethodPost, wsPath+"/checkpoints", []byte(`{"label":""}`), "application/json")
	expectError(t, "checkpoint empty label", resp, body, http.StatusBadRequest, "invalid_request")
	resp, body = doRequest(t, srv, http.MethodGet, wsPath+"/checkpoints", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("checkpoints list: status = %d, want 200", resp.StatusCode)
	}
	var cps labCheckpointsResp
	decodeJSON(t, "checkpoints list", body, &cps)
	if cps.Total != 1 || len(cps.Items) != 1 || cps.Items[0].CheckpointID != "cp-1" || cps.Items[0].Label != "基线" {
		t.Fatalf("checkpoints list = %+v, want [cp-1 基线]", cps)
	}

	// Break the state (status flips + a delete) without running checks.
	opsBreak := `{"ops":[
                {"op":"set_link_status","linkId":"ln-3","status":"down"},
                {"op":"set_device_status","deviceId":"vd-6","status":"down"},
                {"op":"delete_link","linkId":"ln-4"}
        ],"runChecks":false}`
	resp, body = doRequest(t, srv, http.MethodPost, wsPath+"/ops", []byte(opsBreak), "application/json")
	decodeJSON(t, "ops break", body, &applied)
	if applied.ChangeSetID != "cs-6" || applied.OpsApplied != 3 {
		t.Fatalf("ops break = %+v, want cs-6/3 ops", applied)
	}

	// Restore: a real changeset (non-null changesetId) returning to the
	// checkpoint hash, with auto-checks.
	resp, body = doRequest(t, srv, http.MethodPost, wsPath+"/checkpoints/cp-1/restore", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("checkpoint restore: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	var restored labRestoreResp
	decodeJSON(t, "checkpoint restore", body, &restored)
	if restored.CheckpointID != "cp-1" || restored.Label != "基线" {
		t.Fatalf("checkpoint restore meta = %+v", restored)
	}
	if restored.ChangeSetID == nil || *restored.ChangeSetID != "cs-7" || restored.OpsApplied == 0 {
		t.Fatalf("checkpoint restore = %+v, want changesetId cs-7 (non-null) with ops applied", restored)
	}
	if restored.RestoredToHash != created.WorkspaceHash || restored.WorkspaceHash != created.WorkspaceHash {
		t.Fatalf("checkpoint restore hashes = %s→%s, want the baseline %s both ways",
			restored.RestoredToHash, restored.WorkspaceHash, created.WorkspaceHash)
	}
	if restored.Checks == nil || restored.Checks.RunID != "run-6" {
		t.Fatalf("checkpoint restore checks = %+v, want run-6", restored.Checks)
	}

	// Repeat restore: no-op with changesetId null.
	resp, body = doRequest(t, srv, http.MethodPost, wsPath+"/checkpoints/cp-1/restore", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("checkpoint repeat restore: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	decodeJSON(t, "checkpoint repeat restore", body, &restored)
	if restored.ChangeSetID != nil || restored.OpsApplied != 0 || restored.Note != "already at checkpoint state" {
		t.Fatalf("checkpoint repeat restore = %+v, want changesetId null/0 ops/note", restored)
	}

	// Missing snapshot file on disk ⇒ 409 checkpoint_missing_file.
	snapshot := filepath.Join(dir, "checkpoints", ws, "cp-1.db")
	if err := os.Remove(snapshot); err != nil {
		t.Fatalf("remove snapshot %s: %v", snapshot, err)
	}
	resp, body = doRequest(t, srv, http.MethodPost, wsPath+"/checkpoints/cp-1/restore", nil, "")
	expectError(t, "checkpoint missing file", resp, body, http.StatusConflict, "checkpoint_missing_file")
	resp, body = doRequest(t, srv, http.MethodPost, wsPath+"/checkpoints/cp-99/restore", nil, "")
	expectError(t, "checkpoint unknown", resp, body, http.StatusNotFound, "not_found")

	// ---- changesets list after undo/restore: triggers and statuses.
	resp, body = doRequest(t, srv, http.MethodGet, wsPath+"/changesets", nil, "")
	decodeJSON(t, "changesets list final", body, &css)
	if css.Total != 7 || len(css.Items) != 7 {
		t.Fatalf("changesets final total = %d (%d items), want 7/7", css.Total, len(css.Items))
	}
	if css.Items[0].ID != "cs-7" || css.Items[0].Trigger != "checkpoint_restore" || css.Items[0].Status != "applied" {
		t.Fatalf("changesets final items[0] = %+v, want cs-7 checkpoint_restore/applied", css.Items[0])
	}
	byID := map[string]labCSItem{}
	for _, cs := range css.Items {
		byID[cs.ID] = cs
	}
	if byID["cs-5"].Trigger != "undo" || byID["cs-5"].Status != "applied" {
		t.Fatalf("changesets final cs-5 = %+v, want undo/applied", byID["cs-5"])
	}
	if byID["cs-1"].Status != "reverted" || byID["cs-1"].RevertedAt == nil {
		t.Fatalf("changesets final cs-1 = %+v, want reverted with revertedAt set", byID["cs-1"])
	}
	if byID["cs-2"].Status != "failed" {
		t.Fatalf("changesets final cs-2 = %+v, want failed", byID["cs-2"])
	}

	// ---- lab report JSON.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/lab/report?workspaceId="+ws+"&format=json", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("lab report json: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	var report labReportResp
	decodeJSON(t, "lab report json", body, &report)
	if report.GeneratedAt == "" || report.EngineVersion != domain.AppVersion {
		t.Fatalf("lab report meta = %s/%s, want a timestamp and engineVersion %s",
			report.GeneratedAt, report.EngineVersion, domain.AppVersion)
	}
	if report.Workspace.ID != ws || report.Workspace.Name != "lab-e2e" ||
		report.Workspace.WorkspaceHash != created.WorkspaceHash {
		t.Fatalf("lab report workspace = %+v", report.Workspace)
	}
	if rc := report.Workspace.Counts; rc.Devices != 27 || rc.UpDevices != 23 || rc.DownDevices != 4 ||
		rc.Interfaces != 40 || rc.Links != 20 || rc.UpLinks != 20 || rc.ChangeSets != 7 || rc.Checkpoints != 1 {
		t.Fatalf("lab report workspace counts = %+v, want 27/23/4/40/20/20/7/1", rc)
	}
	if report.Score == nil || *report.Score != 95 {
		t.Fatalf("lab report score = %v, want 95 (restored baseline state, latest run run-6)", report.Score)
	}
	assertSevCounts(t, "lab report checks", report.Checks.Counts, map[string]int{"error": 0, "warning": 5, "info": 36})
	if len(report.Checks.Results) != 41 {
		t.Fatalf("lab report findings = %d, want 41 (findings only)", len(report.Checks.Results))
	}
	if len(report.ChangeSets) != 7 || report.ChangeSets[0].ID != "cs-7" ||
		report.ChangeSets[0].Trigger != "checkpoint_restore" {
		t.Fatalf("lab report changesets = %d entries, want 7 newest-first starting at cs-7", len(report.ChangeSets))
	}
	if len(report.Checkpoints) != 1 || report.Checkpoints[0].CheckpointID != "cp-1" {
		t.Fatalf("lab report checkpoints = %+v, want [cp-1]", report.Checkpoints)
	}
	if report.Disclaimer != "本报告基于合成测试数据生成，仅用于工程方法验证，不代表真实网络运行情况。" {
		t.Fatalf("lab report disclaimer = %q", report.Disclaimer)
	}

	// ---- lab report HTML (default format).
	resp, body = doRequest(t, srv, http.MethodGet, "/api/lab/report?workspaceId="+ws, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("lab report html: status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("lab report html content-type = %q, want text/html; charset=utf-8", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("lab report html: cache-control = %q, want no-store", cc)
	}
	html := string(body)
	for _, want := range []string{
		"免责声明",
		"lab-e2e",
		"设备列表",
		"DEV-001",
		"用户操作",
		"撤销",
		"检查点恢复",
		"已应用",
		"已撤销",
		"失败",
		"本报告基于合成测试数据生成，仅用于工程方法验证，不代表真实网络运行情况。",
		"Synthetic data — engineering-method validation only.",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("lab report html misses %q", want)
		}
	}

	// ---- pagination clamps (pageSize 500 → 100) and invalid page.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/workspaces?pageSize=500", nil, "")
	decodeJSON(t, "workspaces clamp", body, &list)
	if list.PageSize != 100 {
		t.Fatalf("workspaces pageSize clamp = %d, want 100", list.PageSize)
	}
	resp, body = doRequest(t, srv, http.MethodGet, wsPath+"/changesets?pageSize=500", nil, "")
	decodeJSON(t, "changesets clamp", body, &css)
	if css.PageSize != 100 {
		t.Fatalf("changesets pageSize clamp = %d, want 100", css.PageSize)
	}
	resp, body = doRequest(t, srv, http.MethodGet, wsPath+"/checks?pageSize=500", nil, "")
	decodeJSON(t, "checks clamp", body, &runs)
	if runs.PageSize != 100 {
		t.Fatalf("checks pageSize clamp = %d, want 100", runs.PageSize)
	}
	resp, body = doRequest(t, srv, http.MethodGet, "/api/workspaces?page=0", nil, "")
	expectError(t, "workspaces page=0", resp, body, http.StatusBadRequest, "bad_request")

	// ---- workspace delete: removed counts + 404 afterwards.
	resp, body = doRequest(t, srv, http.MethodDelete, wsPath, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("workspace delete: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	var deleted labDeleteResp
	decodeJSON(t, "workspace delete", body, &deleted)
	if !deleted.Deleted {
		t.Fatalf("workspace delete deleted = false")
	}
	if deleted.Removed.Devices != 27 || deleted.Removed.Interfaces != 40 || deleted.Removed.Links != 20 ||
		deleted.Removed.ChangeSets != 7 || deleted.Removed.CheckRuns != 5 || deleted.Removed.Checkpoints != 1 {
		t.Fatalf("workspace delete removed = %+v, want devices 27/interfaces 40/links 20/changesets 7/checkRuns 5/checkpoints 1",
			deleted.Removed)
	}
	resp, body = doRequest(t, srv, http.MethodGet, wsPath, nil, "")
	expectError(t, "workspace after delete", resp, body, http.StatusNotFound, "not_found")
	resp, body = doRequest(t, srv, http.MethodGet, "/api/lab/report?workspaceId="+ws, nil, "")
	expectError(t, "lab report after delete", resp, body, http.StatusNotFound, "not_found")

	// ---- unknown /api route still answers the JSON 404 envelope.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/lab/nope", nil, "")
	expectError(t, "unknown lab api route", resp, body, http.StatusNotFound, "not_found")
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("unknown lab api route content-type = %q, want JSON", ct)
	}
}

// TestAPILabWorkspaceErrors covers the creation error surface (§6/§7) and
// the 404s of every route for an unknown workspace id.
func TestAPILabWorkspaceErrors(t *testing.T) {
	srv, _ := newLabTestServer(t)
	devicesBatch, _, circuitsBatch := importLabWorkbooks(t, srv)

	// Duplicate name ⇒ 409 conflict.
	resp, body := doRequest(t, srv, http.MethodPost, "/api/workspaces",
		labJSON(t, map[string]string{"name": "dup"}), "application/json")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create workspace: status = %d, want 201\nbody: %s", resp.StatusCode, body)
	}
	resp, body = doRequest(t, srv, http.MethodPost, "/api/workspaces",
		labJSON(t, map[string]string{"name": "dup"}), "application/json")
	expectError(t, "duplicate workspace name", resp, body, http.StatusConflict, "conflict")

	// Empty name / broken JSON ⇒ 400 invalid_request.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/workspaces",
		labJSON(t, map[string]string{"name": ""}), "application/json")
	expectError(t, "empty workspace name", resp, body, http.StatusBadRequest, "invalid_request")
	resp, body = doRequest(t, srv, http.MethodPost, "/api/workspaces", []byte(`{"name":`), "application/json")
	expectError(t, "broken workspace JSON", resp, body, http.StatusBadRequest, "invalid_request")

	// Unknown batch id ⇒ 404 not_found.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/workspaces",
		labJSON(t, map[string]any{"name": "x", "sourceBatchIds": []string{"batch-nope"}}), "application/json")
	expectError(t, "unknown batch id", resp, body, http.StatusNotFound, "not_found")

	// Selection without device resources ⇒ 422 no_source_resources (a
	// circuits-only scope has no devices).
	resp, body = doRequest(t, srv, http.MethodPost, "/api/workspaces",
		labJSON(t, map[string]any{"name": "circuits-only", "sourceBatchIds": []string{circuitsBatch}}),
		"application/json")
	expectError(t, "no source resources", resp, body, http.StatusUnprocessableEntity, "no_source_resources")
	_ = devicesBatch

	// Lab report parameter surface.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/lab/report?workspaceId=dup&format=xml", nil, "")
	expectError(t, "lab report bad format", resp, body, http.StatusBadRequest, "invalid_request")
	resp, body = doRequest(t, srv, http.MethodGet, "/api/lab/report?format=json", nil, "")
	expectError(t, "lab report missing workspaceId", resp, body, http.StatusBadRequest, "invalid_request")
	resp, body = doRequest(t, srv, http.MethodGet, "/api/lab/report?workspaceId=ws-none", nil, "")
	expectError(t, "lab report unknown workspace", resp, body, http.StatusNotFound, "not_found")

	// 404 for every route on an unknown workspace id.
	unknown := "/api/workspaces/ws-does-not-exist"
	resp, body = doRequest(t, srv, http.MethodGet, unknown, nil, "")
	expectError(t, "unknown ws detail", resp, body, http.StatusNotFound, "not_found")
	resp, body = doRequest(t, srv, http.MethodDelete, unknown, nil, "")
	expectError(t, "unknown ws delete", resp, body, http.StatusNotFound, "not_found")
	resp, body = doRequest(t, srv, http.MethodGet, unknown+"/topology", nil, "")
	expectError(t, "unknown ws topology", resp, body, http.StatusNotFound, "not_found")
	resp, body = doRequest(t, srv, http.MethodPost, unknown+"/topology/path",
		labJSON(t, map[string]string{"fromDevice": "DEV-001", "toDevice": "DEV-002"}), "application/json")
	expectError(t, "unknown ws path", resp, body, http.StatusNotFound, "not_found")
	resp, body = doRequest(t, srv, http.MethodPost, unknown+"/ops",
		[]byte(`{"ops":[{"op":"set_device_status","deviceId":"vd-1","status":"down"}]}`), "application/json")
	expectError(t, "unknown ws ops", resp, body, http.StatusNotFound, "not_found")
	resp, body = doRequest(t, srv, http.MethodPost, unknown+"/checks/run", nil, "")
	expectError(t, "unknown ws checks run", resp, body, http.StatusNotFound, "not_found")
	resp, body = doRequest(t, srv, http.MethodGet, unknown+"/checks", nil, "")
	expectError(t, "unknown ws checks list", resp, body, http.StatusNotFound, "not_found")
	resp, body = doRequest(t, srv, http.MethodGet, unknown+"/checks/run-1", nil, "")
	expectError(t, "unknown ws run detail", resp, body, http.StatusNotFound, "not_found")
	resp, body = doRequest(t, srv, http.MethodGet, unknown+"/checks/run-1/diff?compareTo=run-2", nil, "")
	expectError(t, "unknown ws diff", resp, body, http.StatusNotFound, "not_found")
	resp, body = doRequest(t, srv, http.MethodGet, unknown+"/changesets", nil, "")
	expectError(t, "unknown ws changesets", resp, body, http.StatusNotFound, "not_found")
	resp, body = doRequest(t, srv, http.MethodPost, unknown+"/undo", nil, "")
	expectError(t, "unknown ws undo", resp, body, http.StatusNotFound, "not_found")
	resp, body = doRequest(t, srv, http.MethodPost, unknown+"/checkpoints",
		labJSON(t, map[string]string{"label": "x"}), "application/json")
	expectError(t, "unknown ws checkpoint create", resp, body, http.StatusNotFound, "not_found")
	resp, body = doRequest(t, srv, http.MethodGet, unknown+"/checkpoints", nil, "")
	expectError(t, "unknown ws checkpoints list", resp, body, http.StatusNotFound, "not_found")
	resp, body = doRequest(t, srv, http.MethodPost, unknown+"/checkpoints/cp-1/restore", nil, "")
	expectError(t, "unknown ws checkpoint restore", resp, body, http.StatusNotFound, "not_found")
}

// nextSimID increments the numeric tail of a "<kind>-<n>" id
// (nextSimID("ln-1") = "ln-2").
func nextSimID(id string) string {
	i := strings.LastIndexByte(id, '-')
	n, _ := strconv.Atoi(id[i+1:])
	return id[:i+1] + strconv.Itoa(n+1)
}

// mustMarshal re-marshals a decoded §7 mirror struct so its exact JSON key
// set can be checked.
func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	return b
}
