package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/app"
	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// This file pins the HTTP contracts of the W22 round: run-state endpoints
// (POST set / GET history), the traverse/impact endpoints (parameter
// validation, 404, determinism) and the context assembly endpoint. The
// graph is seeded DIRECTLY through the app's repositories (the same chain
// as the engine-level suite in internal/topology/traverse_test.go — this
// file walks the same entity set through HTTP).

// newSeededAPITestServer builds the httptest server AND returns the app
// (for direct repo seeding — the importer path is already covered by the
// importer/api suites; here we test the QUERY side).
func newSeededAPITestServer(t *testing.T) (*httptest.Server, *app.App) {
	t.Helper()
	dir := t.TempDir()
	a, err := app.New(app.Config{
		DBPath:  filepath.Join(dir, "netops.db"),
		DataDir: dir,
	})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	srv := newAPITestServerFromApp(t, a)
	seedGraphDirect(t, a.DB)
	return srv, a
}

// newAPITestServerFromApp mounts an existing app's routes on an httptest
// server (the newAPITestServer body, parameterized — that helper creates
// its own app; seeding needs OUR instance).
func newAPITestServerFromApp(t *testing.T, a *app.App) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(a.Routes())
	t.Cleanup(srv.Close)
	return srv
}

// seedGraphDirect inserts the chain graph + one abnormal run-state.
func seedGraphDirect(t *testing.T, db *repo.DB) {
	t.Helper()
	ctx := context.Background()
	bat := "bat_w22api"
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := db.Batches.Create(ctx, domain.ImportBatch{
		ID: bat, WorkbookName: "seed.xlsx",
		WorkbookSHA256: "0000000000000000000000000000000000000000000000000000000000000000",
		Status:         domain.BatchStatusCompleted, RuleVersion: "seed", StartedAt: now,
	}); err != nil {
		t.Fatalf("batch: %v", err)
	}
	nodes := []domain.Resource{
		{ID: "res_SITE01", Type: domain.ResourceTypeSite, NaturalKey: "SITE-01", Name: "加区枢纽楼"},
		{ID: "res_MR01", Type: domain.ResourceTypeMachineRoom, NaturalKey: "MR-01", Name: "枢纽机房"},
		{ID: "res_DEVC1", Type: domain.ResourceTypeDevice, NaturalKey: "DEV-C1", Name: "C1路由器"},
		{ID: "res_CUST1", Type: domain.ResourceTypeCustomer, NaturalKey: "CUST-01", Name: "哈尔滨银行"},
		{ID: "res_SVC1", Type: domain.ResourceTypeService, NaturalKey: "SVC-01", Name: "金融专线"},
		{ID: "res_CIR1", Type: domain.ResourceTypeCircuit, NaturalKey: "CIR-01", Name: "C-001"},
		{ID: "res_OCH1", Type: domain.ResourceTypeOTNOch, NaturalKey: "OCH-01", Name: "波道5"},
		{ID: "res_FIB1", Type: domain.ResourceTypeFiber, NaturalKey: "CABLE-01|1", Name: "1号纤芯"},
		{ID: "res_CAB1", Type: domain.ResourceTypeOpticalCable, NaturalKey: "CABLE-01", Name: "主干光缆"},
	}
	for i := range nodes {
		nodes[i].Attributes = map[string]string{}
		nodes[i].FirstBatchID = bat
		nodes[i].CreatedAt, nodes[i].UpdatedAt = now, now
		if err := db.Resources.Insert(ctx, nodes[i]); err != nil {
			t.Fatalf("insert %s: %v", nodes[i].ID, err)
		}
	}
	edges := []domain.Relation{
		{ID: "rel01", Type: domain.RelationTypeSubscribedBy, FromResourceID: "res_SVC1", ToResourceID: "res_CUST1"},
		{ID: "rel02", Type: domain.RelationTypeProvidedBy, FromResourceID: "res_SVC1", ToResourceID: "res_CIR1"},
		{ID: "rel03", Type: domain.RelationTypeCarriedBy, FromResourceID: "res_CIR1", ToResourceID: "res_OCH1"},
		{ID: "rel04", Type: domain.RelationTypeRoutedOver, FromResourceID: "res_OCH1", ToResourceID: "res_CAB1"},
		{ID: "rel05", Type: domain.RelationTypeUsesFiber, FromResourceID: "res_OCH1", ToResourceID: "res_FIB1"},
		{ID: "rel06", Type: domain.RelationTypeFiberOf, FromResourceID: "res_FIB1", ToResourceID: "res_CAB1"},
		{ID: "rel07", Type: domain.RelationTypeCableAEnd, FromResourceID: "res_CAB1", ToResourceID: "res_SITE01"},
		{ID: "rel08", Type: domain.RelationTypeHousedIn, FromResourceID: "res_DEVC1", ToResourceID: "res_MR01"},
		{ID: "rel09", Type: domain.RelationTypeBelongsTo, FromResourceID: "res_MR01", ToResourceID: "res_SITE01"},
		{ID: "rel10", Type: domain.RelationTypeAEnd, FromResourceID: "res_CIR1", ToResourceID: "res_DEVC1"},
	}
	for i := range edges {
		edges[i].Attributes = map[string]string{}
		edges[i].FirstBatchID = bat
		edges[i].CreatedAt = now
		if err := db.Relations.Insert(ctx, edges[i]); err != nil {
			t.Fatalf("insert %s: %v", edges[i].ID, err)
		}
	}
}

// TestRunStateEndpoints pins the run-state HTTP contracts.
func TestRunStateEndpoints(t *testing.T) {
	srv, _ := newSeededAPITestServer(t)

	// Set with explicit ts (deterministic data path).
	body := []byte(`{"runState":"down","source":"manual","ts":"2026-10-01T08:00:00Z"}`)
	resp, respBody := doRequest(t, srv, http.MethodPost, "/api/resources/res_CAB1/run-state", body, "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set run-state: %d\n%s", resp.StatusCode, respBody)
	}
	var snap struct {
		RunState   string `json:"runState"`
		RunStateAt string `json:"runStateAt"`
		Source     string `json:"source"`
	}
	decodeJSON(t, "set run-state", respBody, &snap)
	if snap.RunState != "down" || snap.Source != "manual" {
		t.Fatalf("snapshot = %+v, want down/manual", snap)
	}

	// History window.
	resp, respBody = doRequest(t, srv, http.MethodGet, "/api/resources/res_CAB1/run-state/history", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("history: %d\n%s", resp.StatusCode, respBody)
	}
	var hist struct {
		Items []struct {
			TS       string `json:"ts"`
			RunState string `json:"runState"`
			Source   string `json:"source"`
		} `json:"items"`
		Total int `json:"total"`
	}
	decodeJSON(t, "history", respBody, &hist)
	if hist.Total != 1 || hist.Items[0].RunState != "down" {
		t.Fatalf("history = %+v, want one down observation", hist)
	}

	// Error taxonomy: invalid enum 400, reserved source 400 (honest W23
	// reservation message), unknown resource 404, ledger status untouched.
	resp, _ = doRequest(t, srv, http.MethodPost, "/api/resources/res_CAB1/run-state", []byte(`{"runState":"bogus"}`), "application/json")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bogus runState: %d, want 400", resp.StatusCode)
	}
	resp, respBody = doRequest(t, srv, http.MethodPost, "/api/resources/res_CAB1/run-state", []byte(`{"runState":"up","source":"alert"}`), "application/json")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("reserved source alert: %d, want 400\n%s", resp.StatusCode, respBody)
	}
	resp, _ = doRequest(t, srv, http.MethodPost, "/api/resources/res_bogus/run-state", []byte(`{"runState":"up"}`), "application/json")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown resource run-state: %d, want 404", resp.StatusCode)
	}

	// Stats carries the run-state counts.
	resp, respBody = doRequest(t, srv, http.MethodGet, "/api/stats", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stats: %d", resp.StatusCode)
	}
	var stats map[string]any
	decodeJSON(t, "stats", respBody, &stats)
	runStates, ok := stats["byRunState"].(map[string]any)
	if !ok {
		t.Fatalf("stats has no byRunState block: %v", stats)
	}
	if runStates["down"] != float64(1) {
		t.Errorf("stats.byRunState.down = %v, want 1", runStates["down"])
	}
	// The no-observation resources are counted under "null" (NULL snapshot
	// ≠ explicit unknown — the §12-B1 distinction survives aggregation).
	if runStates["null"] != float64(8) {
		t.Errorf("stats.byRunState.null = %v, want 8 (unobserved resources)", runStates["null"])
	}
}

// TestTraverseEndpointContract pins the traverse HTTP contract.
func TestTraverseEndpointContract(t *testing.T) {
	srv, _ := newSeededAPITestServer(t)

	// The default down-walk from the customer covers the chain.
	resp, body := doRequest(t, srv, http.MethodGet, "/api/resources/res_CUST1/traverse", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("traverse: %d\n%s", resp.StatusCode, body)
	}
	var res struct {
		Root struct {
			ID    string `json:"id"`
			Layer int    `json:"layer"`
		} `json:"root"`
		Nodes []struct {
			ID    string `json:"id"`
			Layer int    `json:"layer"`
		} `json:"nodes"`
		Truncated bool `json:"truncated"`
	}
	decodeJSON(t, "traverse", body, &res)
	if res.Root.Layer != 6 {
		t.Errorf("root layer = %d, want 6 (customer)", res.Root.Layer)
	}
	sawOptical := false
	for _, n := range res.Nodes {
		if n.ID == "res_CAB1" {
			sawOptical = true
		}
	}
	if !sawOptical {
		t.Errorf("traverse from customer missed the optical cable (nodes: %v)", res.Nodes)
	}

	// Determinism: same query twice → identical JSON modulo stats.elapsedMs.
	resp, body2 := doRequest(t, srv, http.MethodGet, "/api/resources/res_CUST1/traverse", nil, "")
	decodeJSON(t, "traverse 2", body2, &res)
	var m1, m2 map[string]any
	_ = json.Unmarshal(body, &m1)
	_ = json.Unmarshal(body2, &m2)
	clearStats(m1)
	clearStats(m2)
	b1, _ := json.Marshal(m1)
	b2, _ := json.Marshal(m2)
	if string(b1) != string(b2) {
		t.Errorf("same traverse twice → different JSON")
	}

	// Validation: 400 family + 404.
	for _, tc := range []struct {
		path string
	}{
		{"/api/resources/res_CUST1/traverse?direction=sideways"},
		{"/api/resources/res_CUST1/traverse?layers=9"},
		{"/api/resources/res_CUST1/traverse?relations=bogus_rel"},
		{"/api/resources/res_CUST1/traverse?maxDepth=99"},
		{"/api/resources/res_CUST1/traverse?maxNodes=0"},
		{"/api/resources/res_CUST1/traverse?pathOnly=maybe"},
	} {
		resp, _ = doRequest(t, srv, http.MethodGet, tc.path, nil, "")
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", tc.path, resp.StatusCode)
		}
	}
	resp, _ = doRequest(t, srv, http.MethodGet, "/api/resources/res_bogus/traverse", nil, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("traverse unknown root: %d, want 404", resp.StatusCode)
	}
}

// clearStats strips the wall-clock field for determinism comparison.
func clearStats(m map[string]any) {
	if s, ok := m["stats"].(map[string]any); ok {
		delete(s, "elapsedMs")
	}
}

// TestImpactEndpointContract pins the impact HTTP contract: the cable is
// down (seeded via HTTP in TestRunStateEndpoints? NO — fresh server, seed
// here) → candidates from the customer down-walk = the cable.
func TestImpactEndpointContract(t *testing.T) {
	srv, _ := newSeededAPITestServer(t)
	// The cable is down.
	resp, body := doRequest(t, srv, http.MethodPost, "/api/resources/res_CAB1/run-state",
		[]byte(`{"runState":"down","ts":"2026-10-01T08:00:00Z"}`), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("seed run-state: %d\n%s", resp.StatusCode, body)
	}

	resp, body = doRequest(t, srv, http.MethodGet, "/api/resources/res_CUST1/impact?direction=down", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("impact: %d\n%s", resp.StatusCode, body)
	}
	var res struct {
		Candidates []struct {
			Node struct {
				ID       string `json:"id"`
				RunState string `json:"runState"`
			} `json:"node"`
			Reason string `json:"reason"`
		} `json:"candidates"`
		Impacted []struct {
			Node struct {
				ID string `json:"id"`
			} `json:"node"`
		} `json:"impacted"`
	}
	decodeJSON(t, "impact", body, &res)
	if len(res.Candidates) != 1 || res.Candidates[0].Node.ID != "res_CAB1" {
		t.Fatalf("candidates = %+v, want [res_CAB1]", res.Candidates)
	}
	if len(res.Impacted) == 0 {
		t.Fatalf("impacted empty — the chain above the cable must be impacted")
	}

	// 400 on bad direction; 404 on unknown root; default direction is up.
	resp, _ = doRequest(t, srv, http.MethodGet, "/api/resources/res_CUST1/impact?direction=sideways", nil, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("impact bad direction: %d, want 400", resp.StatusCode)
	}
	resp, _ = doRequest(t, srv, http.MethodGet, "/api/resources/res_bogus/impact", nil, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("impact unknown root: %d, want 404", resp.StatusCode)
	}
}

// TestContextEndpoint pins the AI one-shot context assembly (§7.3):
// resource detail + neighbors + latest metrics shape.
func TestContextEndpoint(t *testing.T) {
	srv, _ := newSeededAPITestServer(t)
	resp, body := doRequest(t, srv, http.MethodGet, "/api/resources/res_SVC1/context", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("context: %d\n%s", resp.StatusCode, body)
	}
	// The context document is FLAT: the resource detail fields (id, type,
	// naturalKey, name, status, runState, attributes, sourceRefs) live at
	// the top level alongside the context blocks (§7.5 单次拿够 — no
	// nested resource envelope to unwrap).
	var ctxDoc map[string]any
	decodeJSON(t, "context", body, &ctxDoc)
	for _, key := range []string{"id", "type", "naturalKey", "name", "neighbors", "metrics", "recentAnomalies"} {
		if _, ok := ctxDoc[key]; !ok {
			t.Errorf("context misses %q (keys: %v)", key, ctxDoc)
		}
	}
	if ctxDoc["id"] != "res_SVC1" || ctxDoc["type"] != "service" {
		t.Errorf("context resource identity = %v/%v, want res_SVC1/service", ctxDoc["id"], ctxDoc["type"])
	}
	// 404 for unknown resource.
	resp, _ = doRequest(t, srv, http.MethodGet, "/api/resources/res_bogus/context", nil, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("context unknown: %d, want 404", resp.StatusCode)
	}
	_ = fmt.Sprintf
}
