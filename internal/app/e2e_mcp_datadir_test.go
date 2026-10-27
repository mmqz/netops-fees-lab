package app

// e2e_mcp_datadir_test.go — #97 regression suite: the MCP entry point must
// root the fee analysis layer at the SAME data dir the serve/fees channels
// use. Before the fix, `netopsfees mcp` built the composition without a
// DataDir, so fee_risks answered from a CWD-relative "analysis" tree —
// silently zero clues for real data, the shipped docker image included.
//
// Two pins, from cheap to deep:
//   - TestMCPCreatesDefaultDataDir (every build): the MCP composition must
//     default --data to ./data — observable as the data dir the app
//     composition creates next to the CWD.
//   - TestMCPFeesDataDirRoundTrip (duckdb build only): a REAL imported fee
//     batch must surface as non-zero unlinked clues through the real MCP
//     stdio handshake, and pointing --data at an empty tree must answer an
//     honest zero — the routing pin.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// mcpcsvFixture writes a two-row electricity CSV (auto-recognizable headers)
// and returns its path.
func mcpcsvFixture(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "electricity-2024-05.csv")
	body := "站址编码,电费(元),账期\n" +
		"HLJ-0001,100.00,2024-05\n" +
		"HLJ-0002,200.50,2024-05\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write csv fixture: %v", err)
	}
	return path
}

// TestMCPCreatesDefaultDataDir pins the composition default: `netopsfees mcp`
// resolves its data dir to ./data (the same default serve and the fees CLI
// use). Before #97 the MCP composition ran with an empty DataDir — no ./data
// next to the CWD, analysis rooted at the bare CWD-relative "analysis".
func TestMCPCreatesDefaultDataDir(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopsfees binary was not built (go tool unavailable)")
	}
	tmp := t.TempDir()
	cwd := filepath.Join(tmp, "cwd")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}
	dbDir := filepath.Join(tmp, "elsewhere")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatalf("mkdir dbdir: %v", err)
	}
	db := filepath.Join(dbDir, "netops.db")

	cmd := exec.Command(e2eBin, "mcp", "--db", db)
	cmd.Dir = cwd
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start mcp: %v", err)
	}
	// Immediate EOF: the MCP server exits when its stdin transport closes.
	_ = stdin.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("mcp did not exit on stdin EOF within 30s")
	case err := <-done:
		if err != nil {
			t.Fatalf("mcp exit: %v", err)
		}
	}

	info, err := os.Stat(filepath.Join(cwd, "data"))
	if err != nil || !info.IsDir() {
		t.Fatalf("mcp default data dir: ./data next to CWD must exist after the composition opens (got err %v) — #97 regression: the MCP entry must default --data like serve/fees", err)
	}
}

// duckdbE2EBin lazily builds the duckdb-tagged analysis binary (the only
// build whose fee_risks can answer queries; the default build reports
// ErrEngineNotBuilt — an honest refusal, useless for routing pins). Best
// effort: CGO-less or mingw-less environments skip the deep test.
var duckdbE2EBin struct {
	once sync.Once
	path string
}

func buildDuckdbE2E(t *testing.T) string {
	t.Helper()
	duckdbE2EBin.once.Do(func() {
		tmp, err := os.MkdirTemp("", "netopsfees-duckdb-e2e-*")
		if err != nil {
			return
		}
		bin := filepath.Join(tmp, "netopsfees-duckdb"+exeSuffix())
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
		defer cancel()
		build := exec.CommandContext(ctx, "go", "build", "-tags", "duckdb", "-o", bin, "./cmd/netopsfees")
		build.Dir = mustRepoRoot()
		build.Env = append(os.Environ(), "CGO_ENABLED=1")
		if _, err := build.CombinedOutput(); err != nil {
			os.RemoveAll(tmp)
			return // leave empty: the deep test skips
		}
		duckdbE2EBin.path = bin
	})
	if duckdbE2EBin.path == "" {
		t.Skip("duckdb analysis build unavailable (CGO toolchain missing or build failed) — the routing pin needs the duckdb-tagged binary")
	}
	return duckdbE2EBin.path
}

// mcpJSONRPC runs `netopsfees mcp` over real stdio, speaks the raw MCP
// JSON-RPC handshake (initialize → WAIT for the response → initialized → the
// tools/call — the go-sdk rejects pipelined notifications: "initialized
// before initialize") and returns the decoded result object of the call.
func mcpJSONRPC(t *testing.T, bin, db, dataDir string, callArgs map[string]any) map[string]any {
	t.Helper()
	cmd := exec.Command(bin, "mcp", "--db", db, "--data", dataDir)
	cmd.Dir = t.TempDir() // nothing may depend on the CWD
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start mcp: %v", err)
	}
	defer func() {
		_ = stdin.Close()
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(120 * time.Second):
			_ = cmd.Process.Kill()
		}
	}()

	// Background reader: decode the newline-delimited JSON-RPC stream.
	type msg struct {
		obj map[string]any
		raw string
	}
	msgs := make(chan msg, 64)
	go func() {
		defer close(msgs)
		sc := bufio.NewScanner(stdoutPipe)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var obj map[string]any
			if err := json.Unmarshal([]byte(line), &obj); err != nil {
				continue
			}
			msgs <- msg{obj: obj, raw: line}
		}
	}()
	waitFor := func(id float64, op string) map[string]any {
		for {
			select {
			case m, ok := <-msgs:
				if !ok {
					t.Fatalf("%s: no response (id %d) — stream closed", op, int(id))
				}
				got, _ := m.obj["id"].(float64)
				if got != id {
					continue
				}
				if errObj, ok := m.obj["error"].(map[string]any); ok {
					t.Fatalf("%s answered a JSON-RPC error: %v", op, errObj)
				}
				result, _ := m.obj["result"].(map[string]any)
				if result == nil {
					t.Fatalf("%s response has no result: %s", op, m.raw)
				}
				return result
			case <-time.After(120 * time.Second):
				t.Fatalf("%s: no response within 120s", op)
			}
		}
	}

	if _, err := fmt.Fprintln(stdin, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"e2e","version":"0"}}}`); err != nil {
		t.Fatalf("write initialize: %v", err)
	}
	waitFor(1, "initialize")
	if _, err := fmt.Fprintln(stdin, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); err != nil {
		t.Fatalf("write initialized: %v", err)
	}
	call, _ := json.Marshal(map[string]any{"name": "fee_risks", "arguments": callArgs})
	if _, err := fmt.Fprintf(stdin, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":%s}`+"\n", call); err != nil {
		t.Fatalf("write tools/call: %v", err)
	}
	return waitFor(2, "fee_risks")
}

// TestMCPFeesDataDirRoundTrip — the deep routing pin (#97 acceptance): a fee
// batch imported through the CLI (into --data D) must surface as non-zero
// unlinked clues through a REAL `netopsfees mcp --data D` process; the same
// call against an EMPTY data dir must answer an honest zero. Before #97 the
// mcp entry had no --data at all and read a CWD-relative tree.
func TestMCPFeesDataDirRoundTrip(t *testing.T) {
	bin := buildDuckdbE2E(t)

	tmp := t.TempDir()
	db := filepath.Join(tmp, "netops.db")
	data := filepath.Join(tmp, "thedata")
	empty := filepath.Join(tmp, "emptydata")
	csv := mcpcsvFixture(t, tmp)

	if _, code := runCLIBin(t, bin, "fees", "--db", db, "--data", data, "import",
		"--file", csv, "--type", "electricity", "--year", "2024", "--month", "5"); code != 0 {
		t.Fatalf("fees import: exit %d, want 0", code)
	}
	if _, err := os.Stat(filepath.Join(data, "analysis")); err != nil {
		t.Fatalf("import must write the analysis tree under --data (err %v)", err)
	}

	result := mcpJSONRPC(t, bin, db, data, map[string]any{
		"scenario": "unlinked", "feeType": "electricity", "year": 2024, "month": 5, "limit": 10,
	})
	sc, _ := result["structuredContent"].(map[string]any)
	if sc == nil {
		t.Fatalf("fee_risks result carries no structuredContent: %v", result)
	}
	// #27: no resources are imported in this round trip — the empty-library
	// precheck answers 「未分析」 (0 clues + the reason in note AND warnings,
	// the same text on both channels) instead of the pre-#27 all-hit noise
	// ("both codes look unlinked" was vacuously true of an empty library).
	if total, _ := sc["total"].(float64); total != 0 {
		t.Fatalf("fee_risks unlinked total = %v, want 0 (empty resource library → unanalyzed) — structured %v", sc["total"], sc)
	}
	note, _ := sc["note"].(string)
	if !strings.Contains(note, "零资源计费未分析") {
		t.Fatalf("fee_risks unlinked note = %q, want the 未分析 explanation", note)
	}
	warnings, _ := sc["warnings"].([]any)
	if len(warnings) != 1 {
		t.Fatalf("fee_risks unlinked warnings = %v, want the same text riding the warnings channel", sc["warnings"])
	}
	if w, _ := warnings[0].(string); w != note {
		t.Fatalf("fee_risks unlinked warnings = %q, want the #21 channel contract (same text as note): %q", w, note)
	}

	// The control: same db, an empty analysis tree → the honest zero.
	result = mcpJSONRPC(t, bin, db, empty, map[string]any{
		"scenario": "unlinked", "feeType": "electricity", "year": 2024, "month": 5, "limit": 10,
	})
	sc, _ = result["structuredContent"].(map[string]any)
	if sc == nil {
		t.Fatalf("fee_risks control result carries no structuredContent: %v", result)
	}
	if total, _ := sc["total"].(float64); total != 0 {
		t.Fatalf("fee_risks against the empty data dir: total = %v, want 0 (honest empty)", sc["total"])
	}
}

// runCLIBin is runCLI against an explicit binary (the duckdb build).
func runCLIBin(t *testing.T, bin string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run %v: %v", args, err)
	}
	return stdout.String(), code
}
