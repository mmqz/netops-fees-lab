// Tests drive the REAL MCP server (NewServer + Run) over os.Pipe stdio
// transports with the go-sdk client performing the full JSON-RPC MCP
// initialize handshake, then tools/list and tools/call. TestMCPToolRegistry
// is the §12 SEC acceptance test: exactly one tool, resource_search, and
// nothing else — no SQL/shell/file/production tools can ever be registered.
package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/importer"
	"github.com/mmqz/netops-lab/internal/repo"
)

// importSeededWorkbooks opens a fresh database and imports the three seeded
// workbooks with their suggested mappings (canonical order), leaving the
// database in the post-import state the search tool operates on.
func importSeededWorkbooks(t *testing.T) *repo.DB {
	t.Helper()
	db, err := repo.Open(filepath.Join(t.TempDir(), "netops.db"))
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
		// ends as its stdin/stdout.
		done <- Run(ctx, db, srvRead, srvWrite)
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

// TestMCPToolRegistry is the §12 SEC acceptance test: tools/list exposes
// EXACTLY one tool named resource_search — no SQL, shell, file or production
// tools are registered (PROJECT_BRIEF §1 non-negotiable).
func TestMCPToolRegistry(t *testing.T) {
	db, err := repo.Open(filepath.Join(t.TempDir(), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	session := startSession(t, db)
	list, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(list.Tools) != 1 {
		names := make([]string, 0, len(list.Tools))
		for _, tool := range list.Tools {
			names = append(names, tool.Name)
		}
		t.Fatalf("tools/list exposes %d tools (%v), want exactly [resource_search]", len(list.Tools), names)
	}
	if list.Tools[0].Name != "resource_search" {
		t.Fatalf("tools/list tool name = %q, want resource_search", list.Tools[0].Name)
	}
	if list.Tools[0].Description == "" {
		t.Errorf("resource_search tool has an empty description")
	}

	// The pinned tool cannot even be called with an unknown name: the server
	// answers a protocol-level error (unknown tool), proving nothing beyond
	// the registry is callable.
	_, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "run_sql"})
	if err == nil {
		t.Errorf("CallTool(run_sql) succeeded — unknown tools must not be callable")
	} else if !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("CallTool(run_sql) error = %v, want an unknown-tool protocol error", err)
	}
}
