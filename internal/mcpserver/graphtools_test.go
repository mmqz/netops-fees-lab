// graphtools_test.go — the v0.9 R4 acceptance tests: full MCP round-trips
// (initialize → tools/call) for the five read-only analysis tools.
//
// TestMCPGraphToolsRoundTrip drives graph_traverse / graph_impact /
// graph_context over the classic seeded workbooks (circuit → devices
// relations), pinning the wire shapes (§2.6: empty collections are arrays,
// never null) and the error vocabulary (unknown id, bad direction).
//
// TestMCPAlertsMetricsRoundTrip drives alerts_query / metrics_query over a
// REAL detection journey: import the real telecom port sample, ingest a
// 5-day declining optical-power series (the -8 → -24 dBm degradation
// story), run the detection engine, then the AI's read path — alerts_query
// finds the firing alert with its resource projection, metrics_query
// returns the curve as evidence. This is the root-cause loop the agent
// competition story needs, end to end through the actual MCP wire.
package mcpserver

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/mmqz/netops-lab/internal/detect"
	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/metrics"
	"github.com/mmqz/netops-lab/internal/repo"
	"github.com/mmqz/netops-lab/internal/services"
)

// TestMCPGraphToolsRoundTrip — traverse/impact/context over the seeded
// relation graph.
func TestMCPGraphToolsRoundTrip(t *testing.T) {
	db := importSeededWorkbooks(t)
	session := startSession(t, db)

	// Resolve a circuit through resource_search (the agent's discovery
	// path, not a test shortcut).
	searchDoc := callTool(t, session, "resource_search", map[string]any{"q": "CKT-001", "type": "circuit", "limit": 1})
	items, _ := searchDoc["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("resource_search(CKT-001): no items")
	}
	item, _ := items[0].(map[string]any)
	cirID, _ := item["id"].(string)
	if cirID == "" {
		t.Fatalf("resource_search item has no id: %+v", item)
	}

	// —— graph_traverse down：电路 → 两端设备（layer-delta 规则） ——
	doc := callTool(t, session, "graph_traverse", map[string]any{
		"resourceId": cirID,
		"direction":  "down",
	})
	if root, _ := doc["root"].(map[string]any); root == nil || root["type"] != "circuit" {
		t.Fatalf("graph_traverse root = %+v, want the circuit", doc["root"])
	}
	nodes, _ := doc["nodes"].([]any)
	if len(nodes) < 3 {
		t.Errorf("graph_traverse down from a circuit visited %d nodes, want ≥3 (circuit + 2 devices)", len(nodes))
	}
	if _, ok := doc["edges"].([]any); !ok {
		t.Errorf("graph_traverse edges = %T, want a JSON array (§2.6)", doc["edges"])
	}

	// —— graph_impact down：无 run_state 的经典库——候选/受影响为空数组
	// （§2.6 契约：[] 非 null），文档信封完整 ——
	imp := callTool(t, session, "graph_impact", map[string]any{
		"resourceId": cirID,
		"direction":  "down",
	})
	for _, field := range []string{"impacted", "candidates"} {
		v, ok := imp[field].([]any)
		if !ok {
			t.Errorf("graph_impact.%s = %T, want a JSON array (§2.6: [] not null)", field, imp[field])
		} else if len(v) != 0 {
			t.Errorf("graph_impact.%s has %d items on a run-state-free database, want 0", field, len(v))
		}
	}

	// —— graph_context：资源详情 + 邻接 + 指标块（经典库无序列 → metrics
	// 为空数组；neighbors 有 a/z 端设备） ——
	ctxDoc := callTool(t, session, "graph_context", map[string]any{"resourceId": cirID})
	if ctxDoc["naturalKey"] != "CKT-001" {
		t.Errorf("graph_context naturalKey = %v, want CKT-001 (the flattened resource detail)", ctxDoc["naturalKey"])
	}
	nb, _ := ctxDoc["neighbors"].(map[string]any)
	nbNodes, _ := nb["nodes"].([]any)
	if len(nbNodes) < 2 {
		t.Errorf("graph_context neighbors.nodes = %d, want ≥2 (the a/z devices)", len(nbNodes))
	}

	// —— 错误词汇表：未知资源 / 非法方向（MCP 工具错误语义） ——
	callToolError(t, session, "graph_traverse", map[string]any{"resourceId": "res_does-not-exist"})
	callToolError(t, session, "graph_traverse", map[string]any{"resourceId": cirID, "direction": "sideways"})
	callToolError(t, session, "graph_impact", map[string]any{"resourceId": cirID, "direction": "both"})
}

// TestMCPAlertsMetricsRoundTrip — the agent's root-cause loop over a real
// detection journey (import sample ports → ingest degradation → detect →
// read alerts → read the metric curve).
func TestMCPAlertsMetricsRoundTrip(t *testing.T) {
	db := importSeededWorkbooks(t)
	ctx := context.Background()

	// 1) 导入真实电信端口样例（2 ports：B2|bvi2.1008 等）。
	svc := services.New(db)
	importerSvc := svc.Importer
	a, err := importerSvc.Analyze(ctx, filepath.Join("..", "..", "samples", "telecom-port-multi-header.xlsx"))
	if err != nil {
		t.Fatalf("Analyze(port sample): %v", err)
	}
	if _, err := importerSvc.Execute(ctx, a.BatchID, a.SuggestedMapping); err != nil {
		t.Fatalf("Execute(port sample): %v", err)
	}

	// 2) 构建 5 日劣化工作簿（-8 → -24 dBm，触发 dr_opt_rx_low：
	//    lte -14 窗口 3——第 3/4/5 日连续越限）并摄入。
	dir := t.TempDir()
	perfPath := filepath.Join(dir, "perf.xlsx")
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	sheet := f.GetSheetName(0)
	rows := [][]string{
		{"日期", "设备名称", "端口名称", "接收光功率均值"},
		{"2026-10-01", "B2", "bvi2.1008", "-8.1"},
		{"2026-10-02", "B2", "bvi2.1008", "-10.2"},
		{"2026-10-03", "B2", "bvi2.1008", "-14.5"},
		{"2026-10-04", "B2", "bvi2.1008", "-20.7"},
		{"2026-10-05", "B2", "bvi2.1008", "-24.0"},
	}
	for r, row := range rows {
		for c, v := range row {
			cell, _ := excelize.CoordinatesToCellName(c+1, r+1)
			if err := f.SetCellValue(sheet, cell, v); err != nil {
				t.Fatalf("SetCellValue: %v", err)
			}
		}
	}
	if err := f.SaveAs(perfPath); err != nil {
		t.Fatalf("SaveAs: %v", err)
	}
	ingestMap := metrics.IngestMapping{
		TSField:      "日期",
		ResourceType: "port",
		ResolveBy:    []string{"device_name", "port_name"},
		Metrics:      map[string]string{"optical_power_rx_avg_dbm": "接收光功率均值"},
	}
	if _, err := svc.Metrics.Ingest(ctx, perfPath, ingestMap, "perf.xlsx"); err != nil {
		t.Fatalf("Metrics.Ingest: %v", err)
	}

	// 3) 运行检测（种子规则 + 引擎——与 CLI detect 同一实例）。
	if _, err := svc.Detect.Detect(ctx, db, detect.Options{}); err != nil {
		t.Fatalf("Detect: %v", err)
	}

	// 4) AI 读路径：alerts_query 找到 firing 告警（含资源投影）。
	session := startSession(t, db)
	alertDoc := callTool(t, session, "alerts_query", map[string]any{"state": "firing"})
	alerts, _ := alertDoc["items"].([]any)
	if len(alerts) == 0 {
		t.Fatalf("alerts_query(firing): no alerts — the degradation journey produced none")
	}
	first, _ := alerts[0].(map[string]any)
	if first["state"] != "firing" {
		t.Errorf("alerts_query item state = %v, want firing", first["state"])
	}
	if first["metricKey"] != "optical_power_rx_avg_dbm" {
		t.Errorf("alerts_query metricKey = %v, want optical_power_rx_avg_dbm", first["metricKey"])
	}
	resource, _ := first["resource"].(map[string]any)
	if resource["id"] == "" || resource["type"] != "port" {
		t.Errorf("alerts_query resource projection = %+v, want the port resource", resource)
	}
	resID, _ := resource["id"].(string)

	// 5) AI 读路径：metrics_query 取该资源的劣化曲线（证据）。
	metDoc := callTool(t, session, "metrics_query", map[string]any{"resourceId": resID})
	series, _ := metDoc["series"].([]any)
	if len(series) == 0 {
		t.Fatalf("metrics_query: no series for the alerted resource")
	}
	sr, _ := series[0].(map[string]any)
	points, _ := sr["points"].([]any)
	if len(points) != 5 {
		t.Errorf("metrics_query points = %d, want 5 (the ingested degradation days)", len(points))
	}
	last, _ := points[len(points)-1].(map[string]any)
	if last["value"].(float64) != -24.0 {
		t.Errorf("metrics_query last value = %v, want -24", last["value"])
	}

	// 6) 错误词汇表：非法 state / 未知指标键。
	callToolError(t, session, "alerts_query", map[string]any{"state": "exploded"})
	callToolError(t, session, "metrics_query", map[string]any{"resourceId": resID, "keys": "not_a_metric"})
}

// TestMCPInputBoundsRoundTrip — #99/#100: the input face gets honest.
//
// graph_impact's schema used to claim down as the default while the code
// ran up — an AI omitting direction got impact collection while its own
// schema promised root-cause tracing. Pin: (a) the tools/list schema
// advertises up as the default, (b) omitting direction behaves EXACTLY like
// up on a world where the two directions diverge (one device run_state=down
// via the resource-STATUS channel the engine reads).
//
// resource_search/alerts_query used to accept limit=0 as "unlimited" (the
// repo maps limit<=0 to -1 = all rows) while the schema promised
// "缺省 20，最大 100" — now an explicit 0/negative answers a validation
// error.
//
// metrics_query used to advertise "ISO 日期" and pass raw strings into the
// RFC3339 lexicographic window (a date-only `to` silently excluded the
// final day) — now the HTTP twin's RFC3339 validation applies.
func TestMCPInputBoundsRoundTrip(t *testing.T) {
	db := importSeededWorkbooks(t)
	session := startSession(t, db)

	// the world gets its asymmetry through the resource-STATUS channel —
	// the run_state the graph impact engine actually reads (the lab's
	// set_link_status writes the workspace-scoped sim world, invisible to
	// graph_impact): one device of the seeded circuit world goes down, so
	// impact DOWN from the circuit finds the abnormal device while impact UP
	// finds none — the two directions genuinely diverge.
	searchDoc := callTool(t, session, "resource_search", map[string]any{"q": "", "type": "device", "limit": 5})
	items, _ := searchDoc["items"].([]any)
	if len(items) == 0 {
		t.Fatal("resource_search(device): no items in the seeded world")
	}
	dev, _ := items[0].(map[string]any)
	devID, _ := dev["id"].(string)
	if devID == "" {
		t.Fatalf("device item has no id: %+v", dev)
	}
	if err := db.Do(context.Background(), func(tx *repo.Tx) error {
		return tx.ResourceStatus.UpsertStatus(context.Background(), domain.ResourceStatus{
			ResourceID: devID, TS: time.Now().UTC(),
			RunState: domain.RunStateDown, Source: domain.StatusSourceManual,
		})
	}); err != nil {
		t.Fatalf("upsert run_state down: %v", err)
	}
	// resolve the circuit (the device's parent in the seeded world)
	cirDoc := callTool(t, session, "resource_search", map[string]any{"q": "CKT-001", "type": "circuit", "limit": 1})
	cirItems, _ := cirDoc["items"].([]any)
	if len(cirItems) == 0 {
		t.Fatal("resource_search(CKT-001): no circuit")
	}
	cir, _ := cirItems[0].(map[string]any)
	cirID, _ := cir["id"].(string)

	// (a) the tools/list schema tells the truth about the default.
	toolsList, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range toolsList.Tools {
		if tool.Name != "graph_impact" {
			continue
		}
		// From the client the input schema arrives as its default JSON
		// marshaling (map[string]any).
		schema, _ := tool.InputSchema.(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		direction, _ := props["direction"].(map[string]any)
		if direction == nil {
			t.Fatalf("graph_impact schema has no direction property: %v", schema)
		}
		desc, _ := direction["description"].(string)
		if !strings.Contains(desc, "影响面收集（从异常源向上汇总业务受损面，默认）") {
			t.Fatalf("graph_impact schema direction description = %q, want up documented as the default (#99)", desc)
		}
		if strings.Contains(desc, "根因追查（从疑点向下找异常根因，默认）") {
			t.Fatalf("graph_impact schema still claims down as the default (#99): %q", desc)
		}
	}

	// (b) the default is UP, pinned where the directions diverge: the DOWN
	// walk finds the abnormal device as a candidate, the UP walk finds
	// none — and omitting direction must equal the UP answer byte for byte.
	omitted := callTool(t, session, "graph_impact", map[string]any{"resourceId": cirID})
	up := callTool(t, session, "graph_impact", map[string]any{"resourceId": cirID, "direction": "up"})
	if !reflect.DeepEqual(omitted, up) {
		t.Fatal("graph_impact: omitting direction ≠ explicit up — the default drifted (#99)")
	}
	down := callTool(t, session, "graph_impact", map[string]any{"resourceId": cirID, "direction": "down"})
	if reflect.DeepEqual(up, down) {
		t.Fatalf("graph_impact up/down did not diverge on the down-device world — the pin is vacuous (up=%v down=%v)", up, down)
	}
	if cands := arr(down, "candidates"); len(cands) == 0 {
		t.Errorf("graph_impact down: candidates = 0, want the abnormal device (setup failed): %v", down)
	}

	// (c) limit=0 and negative limits answer validation errors (#100), not
	// the silent unlimited dump (repo limit<=0 → -1).
	for _, bad := range []float64{0, -1, -100} {
		if msg := callToolError(t, session, "resource_search", map[string]any{"q": "", "limit": bad}); !strings.Contains(msg, "≥ 1") {
			t.Errorf("resource_search limit=%d error = %q, want the ≥ 1 validation error", int(bad), msg)
		}
		if msg := callToolError(t, session, "alerts_query", map[string]any{"limit": bad}); !strings.Contains(msg, "≥ 1") {
			t.Errorf("alerts_query limit=%d error = %q, want the ≥ 1 validation error", int(bad), msg)
		}
	}
	// a legal limit still works (and clamps at the cap).
	doc := callTool(t, session, "resource_search", map[string]any{"q": "CKT", "limit": 1})
	if items := arr(doc, "items"); len(items) > 1 {
		t.Errorf("resource_search limit=1 returned %d items, want ≤ 1", len(items))
	}

	// (d) metrics_query from/to: RFC3339 only — the HTTP twin's contract.
	// Garbage and date-only strings answer tool errors instead of silently
	// wrong windows.
	for _, badWindow := range []map[string]any{
		{"resourceId": "res_x", "from": "banana"},
		{"resourceId": "res_x", "to": "2024-05-01"},
		{"resourceId": "res_x", "from": "2024-05-01", "to": "2024-06-01"},
		{"resourceId": "res_x", "from": "2024-05-01T00:00:00Z", "to": "banana"},
	} {
		msg := callToolError(t, session, "metrics_query", badWindow)
		if !strings.Contains(msg, "RFC3339") {
			t.Errorf("metrics_query %v error = %q, want the RFC3339 validation error", badWindow, msg)
		}
	}
	// a valid RFC3339 window parses cleanly (an unknown resource answers
	// the empty-series envelope, not an error — metrics_query has no
	// not-found vocabulary; the point is the WINDOW validated).
	validDoc := callTool(t, session, "metrics_query", map[string]any{
		"resourceId": "res_does-not-exist", "from": "2024-05-01T00:00:00Z", "to": "2024-06-01T00:00:00Z",
	})
	if items := arr(validDoc, "series"); len(items) != 0 {
		t.Errorf("metrics_query unknown resource: series = %d, want 0", len(items))
	}
}
