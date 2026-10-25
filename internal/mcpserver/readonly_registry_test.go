package mcpserver

// readonly_registry_test.go — issue #10: the --read-only variant registers
// EXACTLY the tools whose annotations claim readOnlyHint=true on the full
// server. Today both faces are the same four tools (the fee repo has no
// write-side MCP tool); the invariant is what protects the future — a
// write tool added to NewServer without a read-only decision lands OUTSIDE
// this set and the test tells the truth.

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmqz/netops-fees-lab/internal/repo"
	"github.com/mmqz/netops-fees-lab/internal/services"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

// startReadOnlySession is startSession's --read-only twin: the real
// RunReadOnly entry over pipe stdio with the same composition shape.
func startReadOnlySession(t *testing.T, db *repo.DB) *mcp.ClientSession {
	t.Helper()
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
		done <- RunReadOnly(ctx, services.New(db, services.WithFees(testsupport.TempDir(t))), srvRead, srvWrite)
	}()
	t.Cleanup(func() {
		cancel()
		_ = cliWrite.Close()
		_ = cliRead.Close()
		_ = srvRead.Close()
		_ = srvWrite.Close()
		<-done
	})
	client := mcp.NewClient(&mcp.Implementation{Name: "mcpserver-readonly-test", Version: "test"}, nil)
	session, err := client.Connect(ctx, &mcp.IOTransport{Reader: cliRead, Writer: cliWrite}, nil)
	if err != nil {
		t.Fatalf("client.Connect (read-only initialize): %v", err)
	}
	return session
}

// TestMCPReadOnlyRegistryIsReadOnlyHintSet pins the invariant: the
// read-only registry == the full server's readOnlyHint=true set, name by
// name (sorted).
func TestMCPReadOnlyRegistryIsReadOnlyHintSet(t *testing.T) {
	db, err := repo.Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	full, err := startSession(t, db).ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("full ListTools: %v", err)
	}
	readOnlyAnnotated := []string{}
	for _, tool := range full.Tools {
		if tool.Annotations != nil && tool.Annotations.ReadOnlyHint {
			readOnlyAnnotated = append(readOnlyAnnotated, tool.Name)
		}
	}
	sort.Strings(readOnlyAnnotated)

	ro, err := startReadOnlySession(t, db).ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("read-only ListTools: %v", err)
	}
	roNames := []string{}
	for _, tool := range ro.Tools {
		roNames = append(roNames, tool.Name)
	}
	sort.Strings(roNames)

	if len(roNames) == 0 {
		t.Fatal("read-only registry is empty — the variant must expose the pure queries")
	}
	if len(roNames) != len(readOnlyAnnotated) {
		t.Fatalf("read-only registry (%v) != full-server readOnlyHint=true set (%v)", roNames, readOnlyAnnotated)
	}
	for i := range roNames {
		if roNames[i] != readOnlyAnnotated[i] {
			t.Fatalf("read-only registry (%v) != full-server readOnlyHint=true set (%v)", roNames, readOnlyAnnotated)
		}
	}
}
