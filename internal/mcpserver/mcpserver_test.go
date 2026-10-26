// Tests drive the REAL MCP server (NewServer + Run) over os.Pipe stdio
// transports with the go-sdk client performing the full JSON-RPC MCP
// initialize handshake, then tools/list and tools/call. TestMCPToolRegistry
// is the §11 SEC-P2 acceptance test: exactly four tools — resource_search,
// fee_risks, fee_totals and fee_duplicates (the fees repo's AI face).
package mcpserver

import (
	"context"
	"encoding/json"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/fees"
	"github.com/mmqz/netops-fees-lab/internal/importer"
	"github.com/mmqz/netops-fees-lab/internal/repo"
	"github.com/mmqz/netops-fees-lab/internal/services"
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
		// app.New hands to Run). #58 stage 5: WithFees wires the fee
		// analysis service (a temp data dir — the fee_risks tool needs it).
		done <- Run(ctx, services.New(db, services.WithFees(testsupport.TempDir(t))), srvRead, srvWrite)
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
// EXACTLY the five contract tools — resource_search + fee_risks (v0.15,
// #58 stage 5) + fee_totals / fee_duplicates (#120) + fee_rules_create
// (#2 phase 1: the first WRITE tool — creation only, NewServer only) —
// and nothing else: no SQL, shell, file or production tools, and no
// enable/disable tool (the human channel stays HTTP/CLI). The COUNT is additionally
// pinned against docs/facts.md by TestMCPToolCountMatchesFactsFile (#110 —
// the facts-file gate), so the number quoted in prose can never drift from
// the registry silently.
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
	// The five contract tools of the fees repo, SORTED for comparison (the
	// closed registry; the order is unspecified). fee_rules_create joined in
	// #2 phase 1 — the first write tool, registered on NewServer ONLY (the
	// --read-only twin must never see it — TestMCPReadOnlyRegistryIsReadOnlyHintSet).
	want := []string{
		"fee_duplicates", "fee_risks", "fee_rules_create", "fee_totals", "resource_search",
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
	if len(names) != 5 {
		t.Fatalf("tools/list exposes %d tools (%v), want exactly the five contract tools %v", len(names), names, want)
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
	for _, forbidden := range []string{"alerts_ack", "alerts_clear", "detection_run", "metrics_ingest",
		"lab_apply_ops", "lab_run_checks", "graph_traverse", "graph_impact", "graph_context"} {
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

// TestMCPFeeRisks pins the fee_risks tool (#58 stage 5): the closed
// parameter validation (scenario/feeType/year) and — on default builds —
// the honest engine-not-built refusal. The duckdb-tagged fees package
// tests cover the real preset outputs; this pin holds the TOOL surface.
func TestMCPFeeRisks(t *testing.T) {
	db, err := repo.Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	session := startSession(t, db)

	// validation: bogus scenario / fee type / missing year (MCP tool
	// errors — IsError results, not protocol errors)
	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"scenario": "bogus", "feeType": "electricity", "year": 2024}, "场景"},
		{map[string]any{"scenario": "spike", "feeType": "bogus", "year": 2024}, "feeType"},
		{map[string]any{"scenario": "spike", "feeType": "electricity"}, "year"},
	} {
		got := callToolError(t, session, "fee_risks", tc.args)
		if !strings.Contains(got, tc.want) {
			t.Errorf("fee_risks(%v) error = %q, want it to mention %q", tc.args, got, tc.want)
		}
	}

	// default build: the honest engine-not-built refusal, every scenario.
	// On a -tags duckdb build the engine IS available, so the refusal half
	// cannot assert here — the call would run a real preset against the
	// empty temp analysis dir and come back with a DuckDB IO error instead.
	// Skip explicitly (never silently pass): the fees package's
	// duckdb-tagged tests own the engine-built behavior.
	if fees.Query(context.Background()).Available() {
		t.Skip("engine built into this binary (-tags duckdb) — the engine-not-built refusal half belongs to the default build; engine behavior is owned by the duckdb-tagged fees tests")
	}
	for _, sc := range []string{"retired-billing", "duplicate-settlement", "ledger-mismatch", "spike", "outlier", "unlinked", "idle-billing", "missing-billing"} {
		got := callToolError(t, session, "fee_risks", map[string]any{
			"scenario": sc, "feeType": "electricity", "year": 2024,
		})
		if !strings.Contains(got, "分析查询引擎未编译") {
			t.Errorf("fee_risks(%s) on default build error = %q, want the engine-not-built refusal", sc, got)
		}
	}

	// #120: the two query tools join the same refusal contract — and their
	// input validation fires BEFORE the engine gate (bad fee type / year /
	// month / limit refuse with their own texts).
	for _, tc := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{feeTotalsName, map[string]any{"feeType": "electricity", "year": 2024}, "分析查询引擎未编译"},
		{feeDuplicatesName, map[string]any{"feeType": "electricity", "year": 2024}, "分析查询引擎未编译"},
		{feeTotalsName, map[string]any{"feeType": "bogus", "year": 2024}, "未知费用类型"},
		// the SDK's schema validation refuses the missing required year
		// BEFORE the handler (its own vocabulary); the in-handler check
		// stays as defense in depth.
		{feeTotalsName, map[string]any{"feeType": "electricity"}, "year"},
		{feeTotalsName, map[string]any{"feeType": "electricity", "year": 1999}, "2000–2100"},
		{feeTotalsName, map[string]any{"feeType": "electricity", "year": 2024, "month": 13}, "1–12"},
		{feeDuplicatesName, map[string]any{"feeType": "electricity", "year": 2024, "limit": 0}, "limit 必须是"},
	} {
		got := callToolError(t, session, tc.tool, tc.args)
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s(%v) error = %q, want containing %q", tc.tool, tc.args, got, tc.want)
		}
	}
}
