// Tests drive the REAL MCP server (NewServer + Run) over os.Pipe stdio
// transports with the go-sdk client performing the full JSON-RPC MCP
// initialize handshake, then tools/list and tools/call. TestMCPToolRegistry
// is the §11 SEC-P2 acceptance test: exactly ten tools — resource_search,
// the four lab tools of PROJECT_BRIEF_P2 §8 and the five read-only analysis
// tools of v0.9 R4 — and nothing else (no SQL/shell/file/production tools,
// no workspace create/delete tool, no alert ack/clear or detection-run
// tool: the analysis surface is read-only). The
// lab tests materialize a workspace DIRECTLY through sim.Service on the test
// side (simulating the human/HTTP creation MCP cannot perform) and then
// exercise every tool through the real MCP client.
package mcpserver

import (
	"context"
	"encoding/json"
	"github.com/mmqz/netops-lab/internal/testsupport"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/importer"
	"github.com/mmqz/netops-lab/internal/repo"
	"github.com/mmqz/netops-lab/internal/services"
	"github.com/mmqz/netops-lab/internal/sim"
)

// importSeededWorkbooks opens a fresh database and imports the three seeded
// workbooks with their suggested mappings (canonical order), leaving the
// database in the post-import state the search tool operates on.
func importSeededWorkbooks(t *testing.T) *repo.DB {
	t.Helper()
	db, err := repo.Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close: %v", err)
		}
	})
	svc := importer.New(db)
	ctx := context.Background()
	for _, file := range []string{"devices.xlsx", "ippools.xlsx", "circuits.xlsx"} {
		path := filepath.Join("..", "..", "testdata", file)
		a, err := svc.Analyze(ctx, path)
		if err != nil {
			t.Fatalf("Analyze(%s): %v", file, err)
		}
		if _, err := svc.Execute(ctx, a.BatchID, a.SuggestedMapping); err != nil {
			t.Fatalf("Execute(%s): %v", file, err)
		}
	}
	return db
}

// startSession runs the real MCP server over two os.Pipe pairs (one per
// direction, exactly the stdio shape the CLI uses) and connects the go-sdk
// client to the other ends — Connect performs the MCP initialize handshake.
// The session is usable until the test ends; the server goroutine and the
// pipes are shut down by the cleanup function.
func startSession(t *testing.T, db *repo.DB) *mcp.ClientSession {
	t.Helper()

	// Client → server pipe and server → client pipe.
	srvRead, cliWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	cliRead, srvWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		// The real entry point the CLI uses — mcpserver.Run over the pipe
		// ends as its stdin/stdout. W9-D: the session consumes the single
		// service composition built from this test's db (the same shape
		// app.New hands to Run).
		done <- Run(ctx, services.New(db), srvRead, srvWrite)
	}()
	t.Cleanup(func() {
		cancel()
		// Closing the client-side ends unblocks the server's readers.
		_ = cliWrite.Close()
		_ = cliRead.Close()
		_ = srvRead.Close()
		_ = srvWrite.Close()
		<-done
	})

	client := mcp.NewClient(&mcp.Implementation{Name: "mcpserver-test-client", Version: domain.AppVersion}, nil)
	session, err := client.Connect(ctx, &mcp.IOTransport{Reader: cliRead, Writer: cliWrite}, nil)
	if err != nil {
		t.Fatalf("client.Connect (MCP initialize handshake): %v", err)
	}
	return session
}

// toolResultJSON extracts the tool's structured result as a decoded JSON
// object — from StructuredContent when present, otherwise from the JSON text
// content the SDK attaches for typed tool handlers.
func toolResultJSON(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	if res.StructuredContent != nil {
		if m, ok := res.StructuredContent.(map[string]any); ok {
			return m
		}
	}
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			var m map[string]any
			if err := json.Unmarshal([]byte(tc.Text), &m); err == nil {
				return m
			}
		}
	}
	t.Fatalf("tool result has no parsable JSON content: %+v", res)
	return nil
}

// TestMCPServerResourceSearch is the §12 S0-3 acceptance test: a full MCP
// round-trip (initialize → tools/list → tools/call) over pipe stdio against
// the real server, with the §10 result contract asserted on the wire JSON.
func TestMCPServerResourceSearch(t *testing.T) {
	db := importSeededWorkbooks(t)
	session := startSession(t, db)
	ctx := context.Background()

	// The tool answers with the §10 shape: total, items, applicationGenerated.
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "resource_search",
		Arguments: map[string]any{"q": "DEV-0", "limit": 10},
	})
	if err != nil {
		t.Fatalf("CallTool(resource_search): %v", err)
	}
	if res.IsError {
		t.Fatalf("CallTool(resource_search) answered a tool error: %+v", res.Content)
	}
	out := toolResultJSON(t, res)

	total, ok := out["total"].(float64)
	if !ok || total <= 0 {
		t.Fatalf("resource_search: total = %v, want > 0", out["total"])
	}
	if appGen, _ := out["applicationGenerated"].(bool); appGen {
		t.Errorf("resource_search: top-level applicationGenerated = true, want false")
	}
	items, ok := out["items"].([]any)
	if !ok || len(items) != 10 {
		t.Fatalf("resource_search: %d items (limit 10), want 10", len(items))
	}
	for i, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("resource_search: items[%d] is %T, want object", i, raw)
		}
		for _, field := range []string{"id", "type", "naturalKey", "name"} {
			v, _ := item[field].(string)
			if v == "" {
				t.Errorf("resource_search: items[%d].%s is empty: %v", i, field, item)
			}
		}
		src, ok := item["source"].(map[string]any)
		if !ok {
			t.Fatalf("resource_search: items[%d].source missing (applicationGenerated=%v): %v",
				i, item["applicationGenerated"], item)
		}
		for _, field := range []string{"workbook", "sheet"} {
			v, _ := src[field].(string)
			if v == "" {
				t.Errorf("resource_search: items[%d].source.%s is empty: %v", i, field, src)
			}
		}
		if row, _ := src["rowNumber"].(float64); row < 2 {
			t.Errorf("resource_search: items[%d].source.rowNumber = %v, want a 1-based Excel row (≥2)", i, src["rowNumber"])
		}
	}

	// A typed search narrows to the devices and keeps the wire ordering
	// (type, natural_key) — DEV-001 first.
	res, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "resource_search",
		Arguments: map[string]any{"q": "DEV-0", "type": "device", "limit": 30},
	})
	if err != nil {
		t.Fatalf("CallTool(resource_search, type=device): %v", err)
	}
	if res.IsError {
		t.Fatalf("CallTool(resource_search, type=device) answered a tool error")
	}
	out = toolResultJSON(t, res)
	if total, _ := out["total"].(float64); total != 27 {
		t.Errorf("resource_search type=device: total = %v, want 27", out["total"])
	}
	items, _ = out["items"].([]any)
	if len(items) != 27 {
		t.Fatalf("resource_search type=device: %d items, want 27", len(items))
	}
	first, _ := items[0].(map[string]any)
	if first["type"] != "device" || first["naturalKey"] != "DEV-001" {
		t.Errorf("resource_search type=device: items[0] = %v, want device/DEV-001", first)
	}

	// MCP tool error semantics: an invalid type argument is reported INSIDE
	// the tool result (IsError = true), not as a protocol-level error.
	res, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "resource_search",
		Arguments: map[string]any{"type": "bogus"},
	})
	if err != nil {
		t.Fatalf("CallTool(bogus type): protocol error: %v", err)
	}
	if !res.IsError {
		t.Errorf("CallTool(bogus type): IsError = false, want true (MCP tool error semantics)")
	}
}

// TestMCPToolRegistry is the §11 SEC-P2 acceptance test: tools/list exposes
// EXACTLY the five contract tools — resource_search + lab_apply_ops +
// lab_run_checks + lab_diff_undo + lab_checkpoint (PROJECT_BRIEF_P2 §8) —
// and nothing else: no SQL, shell, file or production tools, and no tool
// that could create or delete a workspace (§1.4).
func TestMCPToolRegistry(t *testing.T) {
	db, err := repo.Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	session := startSession(t, db)

	// The initialize handshake reports the server identity: name netops-lab,
	// version domain.AppVersion (v0.2.0 in Phase 2 — §8).
	if init := session.InitializeResult(); init == nil || init.ServerInfo == nil {
		t.Fatalf("initialize result missing server info: %+v", init)
	} else if init.ServerInfo.Name != ServerName || init.ServerInfo.Version != domain.AppVersion {
		t.Fatalf("server identity = %s %s, want %s %s", init.ServerInfo.Name, init.ServerInfo.Version, ServerName, domain.AppVersion)
	}

	list, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	// The ten contract tools, SORTED for comparison (§8 pins the set, the
	// registry order is unspecified).
	want := []string{
		"alerts_query", "graph_context", "graph_impact", "graph_traverse",
		"lab_apply_ops", "lab_checkpoint", "lab_diff_undo", "lab_run_checks",
		"metrics_query", "resource_search",
	}
	names := make([]string, 0, len(list.Tools))
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
		if tool.Description == "" {
			t.Errorf("tool %q has an empty description", tool.Name)
		}
		// W9-D: the resource_search description must enumerate every resource
		// type of the closed set — ptp_port joined the set in W8-D and the
		// enumeration lagged one round; this pin keeps the two in lockstep.
		if tool.Name == "resource_search" && !strings.Contains(tool.Description, "PTP端口") {
			t.Errorf("resource_search description = %q, want the type enumeration to include PTP端口", tool.Description)
		}
	}
	sort.Strings(names)
	if len(names) != 10 {
		t.Fatalf("tools/list exposes %d tools (%v), want exactly the ten contract tools %v", len(names), names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("tools/list (sorted) = %v, want %v — the tool surface is closed", names, want)
		}
	}

	// The closed tool list cannot even be called past an unknown name: the
	// server answers a protocol-level error (unknown tool), proving nothing
	// beyond the registry is callable.
	_, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "run_sql"})
	if err == nil {
		t.Errorf("CallTool(run_sql) succeeded — unknown tools must not be callable")
	} else if !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("CallTool(run_sql) error = %v, want an unknown-tool protocol error", err)
	}
	// The R4 analysis surface is read-only by construction: the alert
	// lifecycle edges (ack/clear) and the detection run are NOT tools —
	// they stay human/HTTP/CLI. Their absence is pinned here like the
	// workspace-create prohibition above.
	for _, forbidden := range []string{"alerts_ack", "alerts_clear", "detection_run", "metrics_ingest"} {
		_, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: forbidden})
		if err == nil {
			t.Errorf("CallTool(%s) succeeded — no such write tool may exist", forbidden)
		} else if !strings.Contains(err.Error(), "unknown tool") {
			t.Errorf("CallTool(%s) error = %v, want an unknown-tool protocol error", forbidden, err)
		}
	}
	// A workspace-create-shaped tool must not exist either (§1.4: MCP cannot
	// create or delete workspaces).
	_, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "lab_create_workspace"})
	if err == nil {
		t.Errorf("CallTool(lab_create_workspace) succeeded — no such tool may exist")
	} else if !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("CallTool(lab_create_workspace) error = %v, want an unknown-tool protocol error", err)
	}
}

// ---------------------------------------------------------------- lab tools —

// materializeLabWorkspace materializes the canonical 27-device workspace from
// the seeded import batches DIRECTLY through sim.Service on the test side —
// the human/HTTP creation step MCP cannot perform (§1.4) — and pins the
// deterministic materialization counts.
func materializeLabWorkspace(t *testing.T, db *repo.DB) sim.MaterializeResult {
	t.Helper()
	svc := sim.New(db)
	res, err := svc.Materialize(context.Background(), "mcp-lab", "materialized test-side for MCP tests", nil)
	if err != nil {
		t.Fatalf("sim.Materialize: %v", err)
	}
	if res.Counts.Devices != 27 || res.Counts.Interfaces != 40 || res.Counts.Links != 20 {
		t.Fatalf("materialize counts = %d/%d/%d, want 27 devices / 40 interfaces / 20 links (seeded batches)",
			res.Counts.Devices, res.Counts.Interfaces, res.Counts.Links)
	}
	if len(res.WorkspaceHash) != 64 {
		t.Fatalf("materialize workspaceHash %q is not a sha256 hex string", res.WorkspaceHash)
	}
	return res
}

// callTool invokes a tool and returns its decoded structured result, failing
// on protocol errors and tool errors alike.
func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): protocol error: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("CallTool(%s) answered a tool error: %s", name, toolErrorText(t, res))
	}
	return toolResultJSON(t, res)
}

// callToolError invokes a tool that MUST answer an MCP tool error (IsError =
// true) and returns the error message text.
func callToolError(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): protocol error: %v", name, err)
	}
	if !res.IsError {
		t.Fatalf("CallTool(%s): IsError = false, want a tool error (arguments %v)", name, args)
	}
	return toolErrorText(t, res)
}

// toolErrorText extracts the message text of a tool-error result.
func toolErrorText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	t.Fatalf("tool error result has no text content: %+v", res)
	return ""
}

// num returns out[key] as a float64 (JSON numbers decode as float64), failing
// when the field is missing or not a number.
func num(t *testing.T, ctx string, out map[string]any, key string) float64 {
	t.Helper()
	v, ok := out[key].(float64)
	if !ok {
		t.Fatalf("%s: %s = %#v, want a number", ctx, key, out[key])
	}
	return v
}

// str returns out[key] as a string, failing when the field is missing.
func str(t *testing.T, ctx string, out map[string]any, key string) string {
	t.Helper()
	v, ok := out[key].(string)
	if !ok {
		t.Fatalf("%s: %s = %#v, want a string", ctx, key, out[key])
	}
	return v
}

// obj returns out[key] as a nested object (nil-safe for absence checks).
func obj(out map[string]any, key string) map[string]any {
	m, _ := out[key].(map[string]any)
	return m
}

// arr returns out[key] as a nested array (nil when absent/not an array).
func arr(out map[string]any, key string) []any {
	a, _ := out[key].([]any)
	return a
}

// labFaultOps is the canonical 3-op fault of the lab journey: duplicate
// address on if-1/if-4 (CHK-001) + ln-2 down (connectivity loss).
var labFaultOps = []any{
	map[string]any{"op": "config_interface_ipv4", "interfaceId": "if-1", "ipv4Addr": "10.0.0.1", "ipv4Prefix": 24},
	map[string]any{"op": "config_interface_ipv4", "interfaceId": "if-4", "ipv4Addr": "10.0.0.1", "ipv4Prefix": 24},
	map[string]any{"op": "set_link_status", "linkId": "ln-2", "status": "down"},
}

// TestMCPLabApplyOps drives lab_apply_ops through the real MCP client: the
// 3-op fault applies atomically as cs-1 with the auto-check summary (CHK-001
// error, score < 100), restore_* ops are rejected by the engine's decoder
// (§1.8), unknown op codes yield tool errors, and an unknown workspace fails
// with the not_found vocabulary shared with the HTTP layer. runChecks=false
// renders checks as null on the wire (§8).
func TestMCPLabApplyOps(t *testing.T) {
	db := importSeededWorkbooks(t)
	ws := materializeLabWorkspace(t, db)
	session := startSession(t, db)

	// The 3-op fault applies as ONE changeset with the auto-check summary.
	out := callTool(t, session, "lab_apply_ops", map[string]any{
		"workspaceId": ws.WorkspaceID,
		"ops":         labFaultOps,
		"description": "fault injection via MCP",
	})
	if got := str(t, "apply", out, "changesetId"); got != "cs-1" {
		t.Errorf("apply: changesetId = %q, want cs-1 (first changeset of the workspace)", got)
	}
	if got := str(t, "apply", out, "trigger"); got != "user" {
		t.Errorf("apply: trigger = %q, want user", got)
	}
	if got := num(t, "apply", out, "opsApplied"); got != 3 {
		t.Errorf("apply: opsApplied = %v, want 3", got)
	}
	if got := str(t, "apply", out, "workspaceHash"); len(got) != 64 {
		t.Errorf("apply: workspaceHash = %q, want a 64-hex sha256", got)
	}
	checks := obj(out, "checks")
	if checks == nil {
		t.Fatalf("apply: checks missing (auto-checks default true): %v", out)
	}
	if got := str(t, "apply checks", checks, "runId"); got != "run-1" {
		t.Errorf("apply: checks.runId = %q, want run-1", got)
	}
	counts := obj(checks, "counts")
	if counts == nil {
		t.Fatalf("apply: checks.counts missing: %v", checks)
	}
	if got := num(t, "apply checks", counts, "error"); got < 1 {
		t.Errorf("apply: checks.counts.error = %v, want >= 1 (duplicate host address CHK-001)", got)
	}
	if got := num(t, "apply checks", checks, "score"); got >= 100 {
		t.Errorf("apply: checks.score = %v, want < 100 (findings present)", got)
	}

	// restore_* ops are engine-internal and rejected inside the tool (§1.8):
	// the typed ops round-trip through sim.DecodeOps, which refuses the code.
	msg := callToolError(t, session, "lab_apply_ops", map[string]any{
		"workspaceId": ws.WorkspaceID,
		"ops":         []any{map[string]any{"op": "restore_link"}},
	})
	if !strings.Contains(msg, "not a forward op") {
		t.Errorf("apply restore_link: error message = %q, want the engine's not-a-forward-op rejection", msg)
	}
	// The full restore payload (with its engine-internal snapshot field) is
	// rejected as well — by the generated input schema before the handler.
	callToolError(t, session, "lab_apply_ops", map[string]any{
		"workspaceId": ws.WorkspaceID,
		"ops": []any{map[string]any{
			"op":   "restore_link",
			"link": map[string]any{"id": "ln-2", "aInterfaceId": "if-3", "zInterfaceId": "if-5", "status": "up", "label": ""},
		}},
	})

	// Unknown op codes are tool errors through the same decoder path (§8).
	msg = callToolError(t, session, "lab_apply_ops", map[string]any{
		"workspaceId": ws.WorkspaceID,
		"ops":         []any{map[string]any{"op": "bogus_op"}},
	})
	if !strings.Contains(msg, "not a forward op") {
		t.Errorf("apply bogus_op: error message = %q, want the engine's not-a-forward-op rejection", msg)
	}

	// FX1-A (AUD1-3): an EMPTY ops array is a tool error — the typed ops
	// round-trip through sim.DecodeOps, which rejects {"ops":[]} with
	// invalid_request BEFORE any changeset row exists (an applied 0-op
	// changeset would permanently brick changeset-tier undo). A null ops
	// field is a tool error as well (input schema or the same decoder
	// gate, depending on the SDK's null handling).
	msg = callToolError(t, session, "lab_apply_ops", map[string]any{
		"workspaceId": ws.WorkspaceID,
		"ops":         []any{},
		"description": "must be rejected",
	})
	if !strings.Contains(msg, "invalid_request") {
		t.Errorf("apply empty ops: error message = %q, want the invalid_request code prefix", msg)
	}
	if !strings.Contains(msg, "ops must contain at least one operation") {
		t.Errorf("apply empty ops: error message = %q, want the engine's empty-ops rejection text", msg)
	}
	callToolError(t, session, "lab_apply_ops", map[string]any{
		"workspaceId": ws.WorkspaceID,
		"ops":         nil,
	})

	// Unknown workspace: the §7 not_found vocabulary.
	msg = callToolError(t, session, "lab_apply_ops", map[string]any{
		"workspaceId": "no-such-workspace",
		"ops":         labFaultOps,
	})
	if !strings.Contains(msg, "not_found") {
		t.Errorf("apply unknown workspace: error message = %q, want the not_found code prefix", msg)
	}

	// runChecks=false: checks renders as null on the wire (§8), opsApplied 1.
	out = callTool(t, session, "lab_apply_ops", map[string]any{
		"workspaceId": ws.WorkspaceID,
		"ops": []any{
			map[string]any{"op": "config_interface_ipv4", "interfaceId": "if-2", "ipv4Addr": "10.9.0.1", "ipv4Prefix": 24},
		},
		"runChecks": false,
	})
	if got := str(t, "apply no-checks", out, "changesetId"); got != "cs-2" {
		t.Errorf("apply no-checks: changesetId = %q, want cs-2", got)
	}
	if got := num(t, "apply no-checks", out, "opsApplied"); got != 1 {
		t.Errorf("apply no-checks: opsApplied = %v, want 1", got)
	}
	if v, present := out["checks"]; !present || v != nil {
		t.Errorf("apply no-checks: checks = %#v (present=%v), want JSON null", v, present)
	}
}

// TestMCPLabRunChecks drives lab_run_checks: the findings-only compact
// payload (no passed rows), counts consistent with the results length, the
// deterministic baseline score, and the not_found tool error.
func TestMCPLabRunChecks(t *testing.T) {
	db := importSeededWorkbooks(t)
	ws := materializeLabWorkspace(t, db)
	session := startSession(t, db)

	out := callTool(t, session, "lab_run_checks", map[string]any{"workspaceId": ws.WorkspaceID})
	if got := str(t, "run", out, "runId"); got != "run-1" {
		t.Errorf("run: runId = %q, want run-1 (first run of the workspace)", got)
	}
	if got := str(t, "run", out, "workspaceHash"); len(got) != 64 {
		t.Errorf("run: workspaceHash = %q, want a 64-hex sha256", got)
	}
	results := arr(out, "results")
	if results == nil {
		t.Fatalf("run: results missing: %v", out)
	}
	counts := obj(out, "counts")
	if counts == nil {
		t.Fatalf("run: counts missing: %v", out)
	}
	var total float64
	for _, sev := range []string{"error", "warning", "info"} {
		v, ok := counts[sev].(float64)
		if !ok {
			t.Fatalf("run: counts.%s = %#v, want a number", sev, counts[sev])
		}
		total += v
	}
	if total != float64(len(results)) {
		t.Errorf("run: counts sum %v != len(results) %d (counts must count the findings rows)", total, len(results))
	}
	// The deterministic baseline of the seeded 27-device workspace.
	if total != 41 { // 0 error + 5 warning + 36 info findings
		t.Errorf("run: counts sum = %v, want 41 findings (baseline of the seeded topology)", total)
	}
	for i, raw := range results {
		row, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("run: results[%d] is %T, want object", i, raw)
		}
		if passed, _ := row["passed"].(bool); passed {
			t.Errorf("run: results[%d].passed = true — findings only, no baseline rows may appear: %v", i, row)
		}
		for _, field := range []string{"checkCode", "severity", "message"} {
			if v, _ := row[field].(string); v == "" {
				t.Errorf("run: results[%d].%s is empty: %v", i, field, row)
			}
		}
		if kind, _ := row["entityKind"].(string); kind != "workspace" && kind != "device" && kind != "interface" {
			t.Errorf("run: results[%d].entityKind = %q, want workspace/device/interface", i, kind)
		}
		if id, _ := row["entityId"].(string); id == "" {
			t.Errorf("run: results[%d].entityId is empty: %v", i, row)
		}
	}
	if got := num(t, "run", out, "score"); got != 95 { // 100 − 5·0 − 1·5 (info free)
		t.Errorf("run: score = %v, want 95 (deterministic baseline)", got)
	}

	// Unknown workspace: tool error, not_found vocabulary.
	msg := callToolError(t, session, "lab_run_checks", map[string]any{"workspaceId": "no-such-workspace"})
	if !strings.Contains(msg, "not_found") {
		t.Errorf("run unknown workspace: error message = %q, want the not_found code prefix", msg)
	}
}

// TestMCPLabDiffUndo drives both modes of lab_diff_undo: diff (fault injected
// ⇒ connectivityLost non-empty, negative scoreDelta, findings added) and undo
// (reverts the fault changeset, hash exactly back to the baseline), plus the
// guidance error for mode=diff with a missing toRunId.
func TestMCPLabDiffUndo(t *testing.T) {
	db := importSeededWorkbooks(t)
	ws := materializeLabWorkspace(t, db)
	session := startSession(t, db)

	// Pre-fault baseline run test-side through the engine (run-1, trigger
	// manual — the human's run MCP only reads back through diff).
	baseline, err := sim.New(db).RunChecks(context.Background(), ws.WorkspaceID, domain.CheckTriggerManual)
	if err != nil {
		t.Fatalf("sim.RunChecks baseline: %v", err)
	}
	if baseline.RunID != "run-1" {
		t.Fatalf("baseline run id = %q, want run-1", baseline.RunID)
	}

	// The fault changeset via MCP (cs-1, auto run-2).
	applied := callTool(t, session, "lab_apply_ops", map[string]any{
		"workspaceId": ws.WorkspaceID,
		"ops":         labFaultOps,
	})
	if got := str(t, "apply", applied, "changesetId"); got != "cs-1" {
		t.Fatalf("apply: changesetId = %q, want cs-1", got)
	}
	faultChecks := obj(applied, "checks")
	if faultChecks == nil {
		t.Fatalf("apply: checks missing")
	}
	faultRunID := str(t, "apply checks", faultChecks, "runId")

	// mode=diff: run-1 → run-2 (the fault).
	diff := callTool(t, session, "lab_diff_undo", map[string]any{
		"workspaceId": ws.WorkspaceID,
		"mode":        "diff",
		"fromRunId":   baseline.RunID,
		"toRunId":     faultRunID,
	})
	if got := str(t, "diff", diff, "mode"); got != "diff" {
		t.Errorf("diff: mode = %q, want diff", got)
	}
	if from := obj(diff, "from"); str(t, "diff from", from, "runId") != baseline.RunID {
		t.Errorf("diff: from.runId = %v, want %s", obj(diff, "from"), baseline.RunID)
	}
	if to := obj(diff, "to"); str(t, "diff to", to, "runId") != faultRunID {
		t.Errorf("diff: to.runId = %v, want %s", obj(diff, "to"), faultRunID)
	}
	lost := arr(diff, "connectivityLost")
	if len(lost) == 0 {
		t.Fatalf("diff: connectivityLost is empty — ln-2 down must break pairs: %v", diff)
	}
	for i, raw := range lost {
		pair, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("diff: connectivityLost[%d] is %T, want object", i, raw)
		}
		if from, _ := pair["fromDevice"].(string); from == "" {
			t.Errorf("diff: connectivityLost[%d].fromDevice empty: %v", i, pair)
		}
		if to, _ := pair["toDevice"].(string); to == "" {
			t.Errorf("diff: connectivityLost[%d].toDevice empty: %v", i, pair)
		}
	}
	if gained := arr(diff, "connectivityGained"); gained == nil {
		t.Errorf("diff: connectivityGained missing — empty lists render as [] not null: %v", diff)
	}
	if delta := num(t, "diff", diff, "scoreDelta"); delta >= 0 {
		t.Errorf("diff: scoreDelta = %v, want negative (fault lowers the score)", delta)
	}
	added := arr(diff, "findingsAdded")
	if len(added) == 0 {
		t.Fatalf("diff: findingsAdded is empty — the fault adds CHK-001/CHK-004: %v", diff)
	}
	sawCHK001 := false
	for _, raw := range added {
		f, _ := raw.(map[string]any)
		if f["checkCode"] == "CHK-001" {
			sawCHK001 = true
			if _, ok := f["detail"].(map[string]any); !ok {
				t.Errorf("diff: CHK-001 finding detail = %#v, want the parsed detail object", f["detail"])
			}
		}
	}
	if !sawCHK001 {
		t.Errorf("diff: findingsAdded contains no CHK-001 (duplicate host address): %v", added)
	}
	// The undo section must stay absent in diff mode (documented shape).
	if _, present := diff["reverted"]; present {
		t.Errorf("diff: undo field %q present in diff mode — sections are mode-gated", "reverted")
	}

	// mode=diff with a missing toRunId: the pinned guidance message.
	msg := callToolError(t, session, "lab_diff_undo", map[string]any{
		"workspaceId": ws.WorkspaceID,
		"mode":        "diff",
		"fromRunId":   baseline.RunID,
	})
	if !strings.Contains(msg, "fromRunId and toRunId are required for mode=diff") {
		t.Errorf("diff missing toRunId: error message = %q, want the fromRunId/toRunId guidance", msg)
	}
	// mode=undo with correlating diff fields: tool error (§8).
	msg = callToolError(t, session, "lab_diff_undo", map[string]any{
		"workspaceId": ws.WorkspaceID,
		"mode":        "undo",
		"fromRunId":   baseline.RunID,
	})
	if !strings.Contains(msg, "not valid for mode=undo") {
		t.Errorf("undo with fromRunId: error message = %q, want the not-valid-for-undo guidance", msg)
	}
	// Unknown mode: tool error.
	msg = callToolError(t, session, "lab_diff_undo", map[string]any{
		"workspaceId": ws.WorkspaceID,
		"mode":        "bogus",
	})
	if !strings.Contains(msg, "invalid_request") {
		t.Errorf("unknown mode: error message = %q, want invalid_request", msg)
	}

	// mode=undo (default target = the fault changeset): reverted cs-1, the
	// workspace hash EXACTLY back to the materialization baseline, checks
	// re-run.
	undo := callTool(t, session, "lab_diff_undo", map[string]any{
		"workspaceId": ws.WorkspaceID,
		"mode":        "undo",
	})
	if got := str(t, "undo", undo, "mode"); got != "undo" {
		t.Errorf("undo: mode = %q, want undo", got)
	}
	if got := str(t, "undo", undo, "reverted"); got != "cs-1" {
		t.Errorf("undo: reverted = %q, want cs-1 (the fault changeset)", got)
	}
	if got := str(t, "undo", undo, "changesetId"); got != "cs-2" {
		t.Errorf("undo: changesetId = %q, want cs-2 (the new undo changeset)", got)
	}
	if got := str(t, "undo", undo, "trigger"); got != "undo" {
		t.Errorf("undo: trigger = %q, want undo", got)
	}
	if got := num(t, "undo", undo, "opsApplied"); got != 3 {
		t.Errorf("undo: opsApplied = %v, want 3 (all three fault ops reversed)", got)
	}
	if got := str(t, "undo", undo, "workspaceHash"); got != ws.WorkspaceHash {
		t.Errorf("undo: workspaceHash = %q, want EXACTLY the baseline hash %q (full recovery)", got, ws.WorkspaceHash)
	}
	if checks := obj(undo, "checks"); checks == nil {
		t.Errorf("undo: checks missing — undo runs the auto-checks")
	}
	// The diff section must stay absent in undo mode (documented shape).
	if _, present := undo["scoreDelta"]; present {
		t.Errorf("undo: diff field %q present in undo mode — sections are mode-gated", "scoreDelta")
	}

	// Unknown workspace: tool error, not_found vocabulary.
	msg = callToolError(t, session, "lab_diff_undo", map[string]any{
		"workspaceId": "no-such-workspace",
		"mode":        "undo",
	})
	if !strings.Contains(msg, "not_found") {
		t.Errorf("undo unknown workspace: error message = %q, want the not_found code prefix", msg)
	}
}

// TestMCPLabCheckpoint drives all three actions of lab_checkpoint: create
// (label required, hash of the current state), list (items + total),
// restore (opsApplied ≥ 1, restoredToHash = the checkpoint hash, checks
// re-run) and the correlating-field tool errors.
func TestMCPLabCheckpoint(t *testing.T) {
	db := importSeededWorkbooks(t)
	ws := materializeLabWorkspace(t, db)
	session := startSession(t, db)

	// Empty list (AUD1-11): a workspace with zero checkpoints renders
	// items: [] and total: 0 — the §8 list contract holds for the empty
	// case too (v0.2.0's omitempty dropped both fields and answered a
	// bare {"action":"list"}).
	empty := callTool(t, session, "lab_checkpoint", map[string]any{
		"workspaceId": ws.WorkspaceID,
		"action":      "list",
	})
	if got := str(t, "empty list", empty, "action"); got != "list" {
		t.Errorf("empty list: action = %q, want list", got)
	}
	if got := num(t, "empty list", empty, "total"); got != 0 {
		t.Errorf("empty list: total = %v, want 0", got)
	}
	if items, present := empty["items"]; !present || items == nil {
		t.Errorf("empty list: items = %#v (present=%v), want a present EMPTY array (never null/absent)", items, present)
	} else if arr, ok := items.([]any); !ok || len(arr) != 0 {
		t.Errorf("empty list: items = %#v, want an empty array", items)
	}

	// create: no checks run for a create (§8 — checks only on restore).
	created := callTool(t, session, "lab_checkpoint", map[string]any{
		"workspaceId": ws.WorkspaceID,
		"action":      "create",
		"label":       "baseline",
	})
	if got := str(t, "create", created, "action"); got != "create" {
		t.Errorf("create: action = %q, want create", got)
	}
	if got := str(t, "create", created, "checkpointId"); got != "cp-1" {
		t.Errorf("create: checkpointId = %q, want cp-1", got)
	}
	if got := str(t, "create", created, "label"); got != "baseline" {
		t.Errorf("create: label = %q, want baseline", got)
	}
	if got := str(t, "create", created, "workspaceHash"); got != ws.WorkspaceHash {
		t.Errorf("create: workspaceHash = %q, want the materialization baseline hash %q", got, ws.WorkspaceHash)
	}
	if got := str(t, "create", created, "createdAt"); got == "" {
		t.Errorf("create: createdAt is empty")
	}
	if _, present := created["checks"]; present {
		t.Errorf("create: checks present — a create runs no checks: %v", created)
	}

	// list: the checkpoint appears with total.
	listed := callTool(t, session, "lab_checkpoint", map[string]any{
		"workspaceId": ws.WorkspaceID,
		"action":      "list",
	})
	if got := str(t, "list", listed, "action"); got != "list" {
		t.Errorf("list: action = %q, want list", got)
	}
	if got := num(t, "list", listed, "total"); got != 1 {
		t.Errorf("list: total = %v, want 1", got)
	}
	items := arr(listed, "items")
	if len(items) != 1 {
		t.Fatalf("list: %d items, want 1: %v", len(items), listed)
	}
	item, _ := items[0].(map[string]any)
	if item["checkpointId"] != "cp-1" || item["label"] != "baseline" {
		t.Errorf("list: items[0] = %v, want cp-1/baseline", item)
	}
	if item["workspaceHash"] != ws.WorkspaceHash {
		t.Errorf("list: items[0].workspaceHash = %v, want the baseline hash", item["workspaceHash"])
	}

	// Break the state through MCP ops (cs-1: two links down).
	broken := callTool(t, session, "lab_apply_ops", map[string]any{
		"workspaceId": ws.WorkspaceID,
		"ops": []any{
			map[string]any{"op": "set_link_status", "linkId": "ln-3", "status": "down"},
			map[string]any{"op": "set_link_status", "linkId": "ln-5", "status": "down"},
		},
	})
	if got := str(t, "break", broken, "changesetId"); got != "cs-1" {
		t.Fatalf("break: changesetId = %q, want cs-1", got)
	}

	// restore: the state diff becomes one forward-op changeset; the hash
	// returns to the checkpoint state and checks re-run.
	restored := callTool(t, session, "lab_checkpoint", map[string]any{
		"workspaceId":  ws.WorkspaceID,
		"action":       "restore",
		"checkpointId": "cp-1",
	})
	if got := str(t, "restore", restored, "action"); got != "restore" {
		t.Errorf("restore: action = %q, want restore", got)
	}
	if got := str(t, "restore", restored, "checkpointId"); got != "cp-1" {
		t.Errorf("restore: checkpointId = %q, want cp-1", got)
	}
	if got := str(t, "restore", restored, "label"); got != "baseline" {
		t.Errorf("restore: label = %q, want baseline", got)
	}
	if got := num(t, "restore", restored, "opsApplied"); got < 1 {
		t.Errorf("restore: opsApplied = %v, want >= 1 (the two broken links must be repaired)", got)
	}
	if got := str(t, "restore", restored, "restoredToHash"); got != ws.WorkspaceHash {
		t.Errorf("restore: restoredToHash = %q, want the checkpoint (baseline) hash %q", got, ws.WorkspaceHash)
	}
	if got := str(t, "restore", restored, "workspaceHash"); got != ws.WorkspaceHash {
		t.Errorf("restore: workspaceHash = %q, want the state back at the baseline hash %q", got, ws.WorkspaceHash)
	}
	if got := str(t, "restore", restored, "changesetId"); got != "cs-2" {
		t.Errorf("restore: changesetId = %q, want cs-2 (the restore changeset)", got)
	}
	if checks := obj(restored, "checks"); checks == nil {
		t.Errorf("restore: checks missing — a restore runs the checks")
	}

	// restore without checkpointId: the correlating-field tool error.
	msg := callToolError(t, session, "lab_checkpoint", map[string]any{
		"workspaceId": ws.WorkspaceID,
		"action":      "restore",
	})
	if !strings.Contains(msg, "checkpointId is required for action=restore") {
		t.Errorf("restore without id: error message = %q, want the checkpointId-required guidance", msg)
	}
	// unknown checkpoint id: not_found vocabulary.
	msg = callToolError(t, session, "lab_checkpoint", map[string]any{
		"workspaceId":  ws.WorkspaceID,
		"action":       "restore",
		"checkpointId": "cp-99",
	})
	if !strings.Contains(msg, "not_found") {
		t.Errorf("restore unknown cp: error message = %q, want the not_found code prefix", msg)
	}
	// create without label: tool error (the engine's own guard).
	msg = callToolError(t, session, "lab_checkpoint", map[string]any{
		"workspaceId": ws.WorkspaceID,
		"action":      "create",
	})
	if !strings.Contains(msg, "label") {
		t.Errorf("create without label: error message = %q, want the label-required rejection", msg)
	}
	// unknown action: tool error.
	msg = callToolError(t, session, "lab_checkpoint", map[string]any{
		"workspaceId": ws.WorkspaceID,
		"action":      "destroy",
	})
	if !strings.Contains(msg, "invalid_request") {
		t.Errorf("unknown action: error message = %q, want invalid_request", msg)
	}
	// unknown workspace: not_found vocabulary.
	msg = callToolError(t, session, "lab_checkpoint", map[string]any{
		"workspaceId": "no-such-workspace",
		"action":      "list",
	})
	if !strings.Contains(msg, "not_found") {
		t.Errorf("list unknown workspace: error message = %q, want the not_found code prefix", msg)
	}
}
