package mcpserver

// facts_gate_test.go — the #110 drift gate for the MCP tool count: the
// REAL registry count (full MCP initialize handshake over pipe stdio,
// tools/list) must equal the number declared in docs/facts.md, the
// machine-readable fact source. TestMCPToolRegistry still pins the exact
// NAMES of the closed set; this gate pins the COUNT against the
// documented fact so a tool added or removed without touching the facts
// file fails loudly here instead of lying in every doc that quotes the
// number ("exactly five" survived three additions; "exactly ten" survived
// fee_risks — the TestAppVersionMatchesChangelogHead discipline applied
// to counts).

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmqz/netops-lab/internal/repo"
	"github.com/mmqz/netops-lab/internal/testsupport"
)

func TestMCPToolCountMatchesFactsFile(t *testing.T) {
	want, ok := testsupport.FactsFileInt(t, "mcp-tools")
	if !ok {
		t.Skipf("docs/facts.md not readable (package tested outside the repo tree)")
	}
	db, err := repo.Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	session := startSession(t, db)
	list, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(list.Tools) != want {
		t.Fatalf("tools/list exposes %d tools, docs/facts.md declares %d — a tool added or removed must update the facts file in the same change (see docs/facts.md §4)", len(list.Tools), want)
	}
}
