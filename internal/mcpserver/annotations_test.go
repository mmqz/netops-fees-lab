package mcpserver

// annotations_test.go — issue #10: every tool must carry the MCP-standard
// annotation set and the annotations must MATCH THE BEHAVIOR CONTRACT of
// annotations.go. A live initialize + tools/list walks the real registry;
// a tool with missing/lying annotations fails here.

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmqz/netops-fees-lab/internal/repo"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

// TestMCPToolAnnotationsMatchBehavior pins annotation ↔ tool-name against
// the contract table of annotations.go: all four tools are pure queries
// (readOnly=true, destructive explicitly false, idempotent=true,
// openWorld explicitly false), and NO annotation field is left to the
// SDK defaults (a default is an unspoken claim).
func TestMCPToolAnnotationsMatchBehavior(t *testing.T) {
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
	if len(list.Tools) == 0 {
		t.Fatal("tools/list is empty — the annotation walk needs the real registry")
	}
	for _, tool := range list.Tools {
		if tool.Annotations == nil {
			t.Errorf("tool %q carries no annotations — issue #10: the four standard hints must be set explicitly", tool.Name)
			continue
		}
		if !tool.Annotations.ReadOnlyHint {
			t.Errorf("tool %q: readOnlyHint = false, want true (the whole fee surface is pure query)", tool.Name)
		}
		if tool.Annotations.DestructiveHint == nil {
			t.Errorf("tool %q: destructiveHint unset (SDK default) — must be explicitly false", tool.Name)
		} else if *tool.Annotations.DestructiveHint {
			t.Errorf("tool %q: destructiveHint = true, want false", tool.Name)
		}
		if !tool.Annotations.IdempotentHint {
			t.Errorf("tool %q: idempotentHint = false, want true (re-running a pure query has no extra effect)", tool.Name)
		}
		if tool.Annotations.OpenWorldHint == nil {
			t.Errorf("tool %q: openWorldHint unset (SDK default) — must be explicitly false", tool.Name)
		} else if *tool.Annotations.OpenWorldHint {
			t.Errorf("tool %q: openWorldHint = true, want false (closed local world)", tool.Name)
		}
	}
}
