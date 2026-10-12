// End-to-end CLI tests: TestMain builds the real netopslab binary once
// (go build -o <tmp>/netopslab ./cmd/netopslab from the repo root); the
// TestCLIEndToEnd then drives it as a subprocess through the §10 journey —
// analyze (offline) → import ×3 → validate → report → query — asserting the
// single-JSON-object stdout contract, the seeded outcome counts and the exit
// codes (0 success, 1 failure, 2 usage). A separate test pins the graceful
// HTTP shutdown path of App.Run (§9).
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// exeSuffix is "" on POSIX and ".exe" on Windows: the e2e binary must
// carry the platform suffix or exec.Command cannot run it on Windows
// (issue #3: `executable file not found in %PATH%`).
func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// e2eBin is the netopslab binary TestMain builds; empty when the build is
// skipped (only when the go tool is unavailable).
var e2eBin string

// repoRoot is the repository root, derived from this package's directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	root, err := filepath.Abs(filepath.Join(dir, "..", ".."))
	if err != nil {
		t.Fatalf("abs repo root: %v", err)
	}
	return root
}

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "netopslab-e2e-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "mkdir temp: %v\n", err)
		os.Exit(1)
	}
	bin := filepath.Join(tmp, "netopslab"+exeSuffix())

	// Build the real binary once for all CLI e2e tests.
	build := exec.Command("go", "build", "-o", bin, "./cmd/netopslab")
	build.Dir = mustRepoRoot()
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building netopslab: %v\n%s", err, out)
		os.RemoveAll(tmp)
		os.Exit(1)
	}
	e2eBin = bin

	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

// mustRepoRoot is repoRoot without a *testing.T (TestMain context).
func mustRepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		panic("os.Getwd: " + err.Error())
	}
	root, err := filepath.Abs(filepath.Join(dir, "..", ".."))
	if err != nil {
		panic("abs repo root: " + err.Error())
	}
	return root
}

// runCLI executes the built binary and returns stdout plus the exit code.
// The §10 contract: exactly one JSON object on stdout for every command,
// structured logs on stderr only.
func runCLI(t *testing.T, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(e2eBin, args...)
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
	if stderr.Len() > 0 {
		t.Logf("%v stderr:\n%s", args, stderr.String())
	}
	return stdout.String(), code
}

// runCLIJSON runs the binary and decodes its stdout as the single JSON
// object the contract promises.
func runCLIJSON(t *testing.T, ctx string, args ...string) (map[string]any, int) {
	t.Helper()
	stdout, code := runCLI(t, args...)
	var out map[string]any
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("%s: stdout is not a single JSON object (exit %d): %v\nstdout: %s", ctx, code, err, stdout)
	}
	return out, code
}

// TestCLIEndToEnd is the §12 E2E acceptance test: the repeatable CLI demo —
// testdata import → validate → report → query with JSON summary assertions.
func TestCLIEndToEnd(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopslab binary was not built (go tool unavailable)")
	}
	root := repoRoot(t)
	tmp := t.TempDir()
	db := filepath.Join(tmp, "netops.db")

	workbooks := []struct {
		file   string
		rows   [4]int // total / imported / skipped / failed
		issues map[string]int
	}{
		{"devices.xlsx", [4]int{30, 27, 0, 3}, map[string]int{"error": 4, "warning": 1, "info": 27}},
		{"ippools.xlsx", [4]int{20, 16, 0, 4}, map[string]int{"error": 6, "warning": 1, "info": 12}},
		{"circuits.xlsx", [4]int{25, 21, 0, 4}, map[string]int{"error": 6, "warning": 1, "info": 1}},
	}

	// analyze (offline) + import for each workbook. The mapping file is the
	// suggested mapping the offline analysis prints — the CLI's own §10 flow.
	for i, wb := range workbooks {
		file := filepath.Join(root, "testdata", wb.file)

		// Offline analyze: same shape as POST /api/imports, batchId empty,
		// exit 0, and NO database write (the DB does not even exist yet on
		// the first iteration).
		analysis, code := runCLIJSON(t, "analyze "+wb.file, "analyze", "--file", file)
		if code != 0 {
			t.Fatalf("analyze %s: exit %d, want 0", wb.file, code)
		}
		if got, _ := analysis["batchId"].(string); got != "" {
			t.Errorf("analyze %s: batchId = %q, want empty (offline, no batch)", wb.file, got)
		}
		if got, _ := analysis["workbookName"].(string); got != wb.file {
			t.Errorf("analyze %s: workbookName = %v", wb.file, analysis["workbookName"])
		}
		suggested, err := json.Marshal(analysis["suggestedMapping"])
		if err != nil {
			t.Fatalf("analyze %s: marshal suggestedMapping: %v", wb.file, err)
		}
		if string(suggested) == "null" {
			t.Fatalf("analyze %s: suggestedMapping is null", wb.file)
		}
		mapFile := filepath.Join(tmp, fmt.Sprintf("map-%d.json", i))
		if err := os.WriteFile(mapFile, suggested, 0o644); err != nil {
			t.Fatalf("write %s: %v", mapFile, err)
		}

		// import: analyze + execute + automatic set-level validation.
		summary, code := runCLIJSON(t, "import "+wb.file, "import", "--file", file, "--map", mapFile, "--db", db)
		if code != 0 {
			t.Fatalf("import %s: exit %d, want 0", wb.file, code)
		}
		if got, _ := summary["batchId"].(string); got == "" {
			t.Errorf("import %s: batchId is empty", wb.file)
		}
		if got, _ := summary["status"].(string); got != "completed" {
			t.Errorf("import %s: status = %v", wb.file, summary["status"])
		}
		gotRows := [4]int{}
		if sm, ok := summary["summary"].(map[string]any); ok {
			gotRows = [4]int{
				int(sm["rowsTotal"].(float64)),
				int(sm["rowsImported"].(float64)),
				int(sm["rowsSkipped"].(float64)),
				int(sm["rowsFailed"].(float64)),
			}
		} else {
			t.Fatalf("import %s: summary missing: %v", wb.file, summary)
		}
		if gotRows != wb.rows {
			t.Errorf("import %s: summary rows = %+v, want %+v", wb.file, gotRows, wb.rows)
		}
		gotIssues, _ := summary["issues"].(map[string]any)
		for _, sev := range []string{"error", "warning", "info"} {
			if got := int(gotIssues[sev].(float64)); got != wb.issues[sev] {
				t.Errorf("import %s: issues[%s] = %d, want %d", wb.file, sev, got, wb.issues[sev])
			}
		}
	}

	// validate: recomputes the set-level rules and scores the whole database.
	validate, code := runCLIJSON(t, "validate", "validate", "--db", db)
	if code != 0 {
		t.Fatalf("validate: exit %d, want 0", code)
	}
	if got, _ := validate["score"].(float64); got != 32 {
		t.Errorf("validate: score = %v, want 32", validate["score"])
	}
	issues, _ := validate["issues"].(map[string]any)
	for sev, want := range map[string]int{"error": 13, "warning": 3, "info": 1} {
		if got := int(issues[sev].(float64)); got != want {
			t.Errorf("validate: issues[%s] = %d, want %d", sev, got, want)
		}
	}

	// report --format json --out: the file carries the full quality report.
	reportFile := filepath.Join(tmp, "report.json")
	stdout, code := runCLI(t, "report", "--db", db, "--format", "json", "--out", reportFile)
	if code != 0 {
		t.Fatalf("report: exit %d, want 0\nstdout: %s", code, stdout)
	}
	raw, err := os.ReadFile(reportFile)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report struct {
		Scope   string `json:"scope"`
		Summary struct {
			TotalResources   int            `json:"totalResources"`
			ByType           map[string]int `json:"byType"`
			IssuesBySeverity map[string]int `json:"issuesBySeverity"`
			QualityScore     int            `json:"qualityScore"`
			Batches          []struct {
				Workbook     string `json:"workbook"`
				RowsTotal    int    `json:"rowsTotal"`
				RowsImported int    `json:"rowsImported"`
				RowsSkipped  int    `json:"rowsSkipped"`
				RowsFailed   int    `json:"rowsFailed"`
			} `json:"batches"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("report json: %v\n%s", err, raw)
	}
	if report.Summary.TotalResources != 64 {
		t.Errorf("report: totalResources = %d, want 64", report.Summary.TotalResources)
	}
	for typ, want := range map[string]int{"device": 27, "ip_pool": 16, "circuit": 21} {
		if report.Summary.ByType[typ] != want {
			t.Errorf("report: byType[%s] = %d, want %d", typ, report.Summary.ByType[typ], want)
		}
	}
	if report.Summary.QualityScore != 32 {
		t.Errorf("report: qualityScore = %d, want 32", report.Summary.QualityScore)
	}
	for sev, want := range map[string]int{"error": 13, "warning": 3, "info": 1} {
		if report.Summary.IssuesBySeverity[sev] != want {
			t.Errorf("report: issuesBySeverity[%s] = %d, want %d", sev, report.Summary.IssuesBySeverity[sev], want)
		}
	}
	if len(report.Summary.Batches) != 3 {
		t.Fatalf("report: %d batches, want 3", len(report.Summary.Batches))
	}
	for _, b := range report.Summary.Batches {
		var want [4]int
		switch b.Workbook {
		case "devices.xlsx":
			want = [4]int{30, 27, 0, 3}
		case "ippools.xlsx":
			want = [4]int{20, 16, 0, 4}
		case "circuits.xlsx":
			want = [4]int{25, 21, 0, 4}
		default:
			t.Errorf("report: unexpected workbook %q", b.Workbook)
			continue
		}
		if got := [4]int{b.RowsTotal, b.RowsImported, b.RowsSkipped, b.RowsFailed}; got != want {
			t.Errorf("report: batch %s rows = %+v, want %+v", b.Workbook, got, want)
		}
	}

	// query stats: the CLI's read-side summary.
	stats, code := runCLIJSON(t, "query stats", "query", "--db", db, "stats")
	if code != 0 {
		t.Fatalf("query stats: exit %d, want 0", code)
	}
	if got, _ := stats["totalResources"].(float64); got != 64 {
		t.Errorf("query stats: totalResources = %v, want 64", stats["totalResources"])
	}
	byType, _ := stats["byType"].(map[string]any)
	for typ, want := range map[string]int{"device": 27, "ip_pool": 16, "circuit": 21} {
		if got := int(byType[typ].(float64)); got != want {
			t.Errorf("query stats: byType[%s] = %d, want %d", typ, got, want)
		}
	}
	sev, _ := stats["issuesBySeverity"].(map[string]any)
	for s, want := range map[string]int{"error": 13, "warning": 3, "info": 1} {
		if got := int(sev[s].(float64)); got != want {
			t.Errorf("query stats: issuesBySeverity[%s] = %d, want %d", s, got, want)
		}
	}

	// query resources: paginated search over the CLI surface.
	resources, code := runCLIJSON(t, "query resources", "query", "--db", db, "resources", "--q", "DEV-001", "--type", "device")
	if code != 0 {
		t.Fatalf("query resources: exit %d, want 0", code)
	}
	if got, _ := resources["total"].(float64); got != 1 {
		t.Errorf("query resources: total = %v, want 1", resources["total"])
	}
	if items, _ := resources["items"].([]any); len(items) != 1 {
		t.Errorf("query resources: %d items, want 1", len(items))
	}

	// query relations: the 2-hop neighborhood of DEV-001 in JSON.
	devicePage, _ := resources["items"].([]any)
	dev001, _ := devicePage[0].(map[string]any)
	devID, _ := dev001["id"].(string)
	relations, code := runCLIJSON(t, "query relations", "query", "--db", db, "relations", "--id", devID, "--depth", "2")
	if code != 0 {
		t.Fatalf("query relations: exit %d, want 0", code)
	}
	nodes, _ := relations["nodes"].([]any)
	sawDEV003 := false
	for _, rawNode := range nodes {
		node, _ := rawNode.(map[string]any)
		if node["naturalKey"] == "DEV-003" {
			sawDEV003 = true
		}
	}
	if !sawDEV003 {
		t.Errorf("query relations depth 2: DEV-003 missing from DEV-001 neighborhood (%d nodes)", len(nodes))
	}

	// Failure case: importing a non-xlsx file exits 1 with a not_xlsx error.
	stdout, code = runCLI(t, "import", "--file",
		filepath.Join(root, "testdata", "expected-summary.json"), "--map",
		filepath.Join(tmp, "map-0.json"), "--db", db)
	if code != 1 {
		t.Errorf("import non-xlsx: exit = %d, want 1 (stdout: %s)", code, stdout)
	}
	var errOut map[string]any
	if err := json.Unmarshal([]byte(stdout), &errOut); err != nil {
		t.Fatalf("import non-xlsx: stdout is not a JSON error object: %v\n%s", err, stdout)
	}
	errBody, _ := errOut["error"].(map[string]any)
	if errBody["code"] != "not_xlsx" {
		t.Errorf("import non-xlsx: error.code = %v, want not_xlsx", errBody["code"])
	}

	// Usage error: unknown subcommand exits 2.
	_, code = runCLI(t, "frobnicate")
	if code != 2 {
		t.Errorf("frobnicate: exit = %d, want 2 (usage)", code)
	}

	// testdata: regenerating the deterministic workbooks into a fresh dir.
	outDir := filepath.Join(tmp, "testdata-out")
	td, code := runCLIJSON(t, "testdata", "testdata", "--out", outDir)
	if code != 0 {
		t.Fatalf("testdata: exit %d, want 0", code)
	}
	if got, _ := td["out"].(string); got != outDir {
		t.Errorf("testdata: out = %v", td["out"])
	}
	for _, name := range []string{"devices.xlsx", "ippools.xlsx", "circuits.xlsx", "expected-summary.json"} {
		if _, err := os.Stat(filepath.Join(outDir, name)); err != nil {
			t.Errorf("testdata: %s not generated: %v", name, err)
		}
	}
}

// TestAppRunGracefulShutdown pins the §9 shutdown contract: cancelling the
// Run context (the SIGINT/SIGTERM stand-in) drains the HTTP server and Run
// returns nil within the shutdown timeout.
func TestAppRunGracefulShutdown(t *testing.T) {
	dir := t.TempDir()
	a, err := New(Config{
		Addr:    "127.0.0.1:0",
		DBPath:  filepath.Join(dir, "netops.db"),
		DataDir: dir,
	})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	// Give the listener a moment to bind, then "send" SIGTERM.
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run: %v, want nil on graceful shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s of cancellation")
	}
}
