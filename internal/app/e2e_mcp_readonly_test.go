package app

// e2e_mcp_readonly_test.go — the real-process stdio smoke of `netopsfees
// mcp --read-only` (issue #10): a bare JSON-RPC initialize + tools/list
// over the process's actual stdin/stdout must answer EXACTLY the six
// read-only tools (the four pure queries + #36's two evidence tools),
// each carrying the explicit annotation set.

import (
        "bufio"
        "encoding/json"
        "github.com/mmqz/netops-fees-lab/internal/testsupport"
        "io"
        "os/exec"
        "path/filepath"
        "strings"
        "testing"
        "time"
)

// roSmokeMessage is the minimal JSON-RPC envelope of the smoke exchange.
type roSmokeMessage struct {
        JSONRPC string          `json:"jsonrpc"`
        ID      json.RawMessage `json:"id,omitempty"`
        Method  string          `json:"method,omitempty"`
        Result  json.RawMessage `json:"result,omitempty"`
}

// roToolsListResult is the tools/list payload shape.
type roToolsListResult struct {
        Tools []struct {
                Name        string `json:"name"`
                Annotations *struct {
                        ReadOnlyHint    bool  `json:"readOnlyHint"`
                        DestructiveHint *bool `json:"destructiveHint,omitempty"`
                        IdempotentHint  bool  `json:"idempotentHint"`
                        OpenWorldHint   *bool `json:"openWorldHint,omitempty"`
                } `json:"annotations,omitempty"`
        } `json:"tools"`
}

// TestMCPReadOnlyStdioSmoke drives the REAL binary with --read-only and
// asserts the wire: four tools, every one readOnlyHint=true with the
// explicit annotation shape.
func TestMCPReadOnlyStdioSmoke(t *testing.T) {
        if e2eBin == "" {
                t.Skip("netopsfees binary was not built (go tool unavailable)")
        }
        tmp := testsupport.TempDir(t)
        db := filepath.Join(tmp, "netops.db")

        cmd := exec.Command(e2eBin, "mcp", "--db", db, "--read-only")
        cmd.Dir = tmp
        stdin, err := cmd.StdinPipe()
        if err != nil {
                t.Fatalf("stdin pipe: %v", err)
        }
        stdout, err := cmd.StdoutPipe()
        if err != nil {
                t.Fatalf("stdout pipe: %v", err)
        }
        if err := cmd.Start(); err != nil {
                t.Fatalf("start mcp --read-only: %v", err)
        }
        defer func() {
                _ = stdin.Close()
                _ = cmd.Process.Kill()
                _ = cmd.Wait()
        }()

        go func() {
                _, _ = io.WriteString(stdin, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"readonly-smoke","version":"0"}}}`+"\n")
                _, _ = io.WriteString(stdin, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n")
                _, _ = io.WriteString(stdin, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`+"\n")
        }()

        reader := bufio.NewReader(stdout)
        var listing roToolsListResult
        deadline := time.Now().Add(30 * time.Second)
        for time.Now().Before(deadline) {
                line, err := reader.ReadString('\n')
                if err != nil {
                        t.Fatalf("read stdout line: %v", err)
                }
                if !strings.HasPrefix(strings.TrimSpace(line), "{") {
                        continue
                }
                var msg roSmokeMessage
                if err := json.Unmarshal([]byte(line), &msg); err != nil {
                        continue
                }
                if len(msg.ID) > 0 && strings.Contains(string(msg.ID), "2") && len(msg.Result) > 0 {
                        if err := json.Unmarshal(msg.Result, &listing); err != nil {
                                t.Fatalf("decode tools/list result: %v\n%s", err, line)
                        }
                        break
                }
        }

        if len(listing.Tools) != 6 {
                names := make([]string, 0, len(listing.Tools))
                for _, tool := range listing.Tools {
                        names = append(names, tool.Name)
                }
                t.Fatalf("mcp --read-only tools/list = %d tools (%v), want exactly the six read-only tools (4 pure queries + #36's two evidence tools)", len(listing.Tools), names)
        }
        for _, tool := range listing.Tools {
                if tool.Annotations == nil {
                        t.Errorf("tool %q carries no annotations on the wire", tool.Name)
                        continue
                }
                if !tool.Annotations.ReadOnlyHint {
                        t.Errorf("tool %q on the read-only server has readOnlyHint=false", tool.Name)
                }
                if tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint {
                        t.Errorf("tool %q destructiveHint must be explicitly false", tool.Name)
                }
                if !tool.Annotations.IdempotentHint {
                        t.Errorf("tool %q idempotentHint must be true", tool.Name)
                }
                if tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
                        t.Errorf("tool %q openWorldHint must be explicitly false", tool.Name)
                }
        }
}
