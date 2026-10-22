package app

// e2e_metrics_cli_test.go — TestCLIMetricsBatchLifecycle pins #115: the
// metric channel finally has its CLI rollback. The journey runs the REAL
// artifacts end to end — synthdata (resources + perf day) → resource
// import → ingest (suggest + execute) → metrics list (the batch visible,
// same JSON shape as the HTTP face) → rollback (points deleted, series
// pruned, status flipped) → list again (rolled_back persists) — plus the
// full error taxonomy: re-rollback exit 1 conflict, unknown id exit 1
// not_found, and the usage set exit 2.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmqz/netops-lab/internal/metrics"
)

func TestCLIMetricsBatchLifecycle(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopslab binary was not built (go tool unavailable)")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "netops.db")

	// ① synthdata: 2 devices × 1 port, one perf day, 3 metric columns.
	if _, err := metrics.WriteSynthData(metrics.SynthOptions{
		Out: tmp, Devices: 2, Ports: 1, Days: 1, Scenario: metrics.ScenarioNormal, Seed: 42, Metrics: 3,
	}); err != nil {
		t.Fatalf("synthdata: %v", err)
	}

	// ② resource import via the analyze → import CLI pair (suggested
	// mapping written to a file, the standard unattended shape).
	analysisOut, code := runCLI(t, "analyze", "--file", filepath.Join(tmp, "resources.xlsx"))
	if code != 0 {
		t.Fatalf("analyze: exit %d\nstdout: %s", code, analysisOut)
	}
	var analysis map[string]any
	if err := json.Unmarshal([]byte(analysisOut), &analysis); err != nil {
		t.Fatalf("analyze: stdout is not JSON: %v\n%s", err, analysisOut)
	}
	suggested, err := json.Marshal(analysis["suggestedMapping"])
	if err != nil {
		t.Fatalf("marshal suggestedMapping: %v", err)
	}
	mapFile := filepath.Join(tmp, "resmap.json")
	if err := os.WriteFile(mapFile, suggested, 0o644); err != nil {
		t.Fatalf("write mapping: %v", err)
	}
	impOut, code := runCLI(t, "import", "--file", filepath.Join(tmp, "resources.xlsx"), "--map", mapFile, "--db", db)
	if code != 0 {
		t.Fatalf("resource import: exit %d\nstdout: %s\nmap: %s", code, impOut, suggested)
	}

	// ③ ingest --suggest → mapping file → ingest (one-shot batch).
	sugOut, code := runCLI(t, "ingest", "--file", filepath.Join(tmp, "perf-day-01.xlsx"), "--suggest")
	if code != 0 {
		t.Fatalf("ingest --suggest: exit %d\nstdout: %s", code, sugOut)
	}
	var suggest map[string]any
	if err := json.Unmarshal([]byte(sugOut), &suggest); err != nil {
		t.Fatalf("ingest --suggest: stdout is not JSON: %v\n%s", err, sugOut)
	}
	perfMap, err := json.Marshal(suggest["suggestedMapping"])
	if err != nil {
		t.Fatalf("marshal perf suggestedMapping: %v", err)
	}
	perfMapFile := filepath.Join(tmp, "perfmap.json")
	if err := os.WriteFile(perfMapFile, perfMap, 0o644); err != nil {
		t.Fatalf("write perf mapping: %v", err)
	}
	ingested, code := runCLIJSON(t, "ingest exec", "ingest", "--file", filepath.Join(tmp, "perf-day-01.xlsx"),
		"--map", perfMapFile, "--db", db)
	if code != 0 {
		t.Fatalf("ingest: exit %d (%v)", code, ingested)
	}
	batchID, _ := ingested["batchId"].(string)
	if batchID == "" || ingested["status"] != "completed" {
		t.Fatalf("ingest envelope = %v, want a completed batch", ingested)
	}
	pointsWritten, _ := ingested["pointsWritten"].(float64)
	if pointsWritten == 0 {
		t.Fatalf("ingest pointsWritten = %v, want > 0 (2 ports × 3 metrics)", ingested["pointsWritten"])
	}

	// ④ metrics list: the batch is visible, HTTP-same shape.
	listing, code := runCLIJSON(t, "metrics list", "metrics", "--db", db, "list")
	if code != 0 {
		t.Fatalf("metrics list: exit %d", code)
	}
	items, _ := listing["items"].([]any)
	if listing["total"].(float64) != 1 || len(items) != 1 {
		t.Fatalf("metrics list = %v, want one batch", listing)
	}
	item, _ := items[0].(map[string]any)
	if item["id"] != batchID || item["status"] != "completed" || item["workbookName"] != "perf-day-01.xlsx" {
		t.Fatalf("metrics list item = %v, want the completed perf batch", item)
	}
	// pagination bounds are usage errors
	for _, args := range [][]string{
		{"metrics", "--db", db, "list", "--page", "0"},
		{"metrics", "--db", db, "list", "--page-size", "101"},
	} {
		if stdout, code := runCLI(t, args...); code != 2 {
			t.Errorf("%v: exit = %d, want 2 (stdout: %s)", args, code, stdout)
		}
	}

	// ⑤ rollback: points deleted, series pruned, status flipped.
	rolled, code := runCLIJSON(t, "metrics rollback", "metrics", "--db", db, "rollback", "--batch", batchID)
	if code != 0 {
		t.Fatalf("metrics rollback: exit %d (%v)", code, rolled)
	}
	if rolled["batchId"] != batchID || rolled["status"] != "rolled_back" {
		t.Fatalf("rollback result = %v, want rolled_back", rolled)
	}
	if rolled["deletedPoints"].(float64) != pointsWritten || rolled["prunedSeries"].(float64) == 0 {
		t.Fatalf("rollback result = %v, want deletedPoints = %v and prunedSeries > 0", rolled, pointsWritten)
	}

	// ⑥ the rolled_back status persists in the list; re-rollback is a
	// conflict (exit 1, the HTTP 409's CLI mirror).
	listing, code = runCLIJSON(t, "metrics list 2", "metrics", "--db", db, "list")
	if code != 0 {
		t.Fatalf("metrics list 2: exit %d", code)
	}
	items, _ = listing["items"].([]any)
	item, _ = items[0].(map[string]any)
	if item["status"] != "rolled_back" {
		t.Fatalf("post-rollback item = %v, want status rolled_back", item)
	}
	stdout, code := runCLI(t, "metrics", "--db", db, "rollback", "--batch", batchID)
	if code != 1 {
		t.Errorf("re-rollback: exit = %d, want 1 (stdout: %s)", code, stdout)
	}
	var errOut map[string]any
	if err := json.Unmarshal([]byte(stdout), &errOut); err != nil {
		t.Fatalf("re-rollback: stdout is not a JSON error object: %v\n%s", err, stdout)
	}
	errBody, _ := errOut["error"].(map[string]any)
	if errBody["code"] != "conflict" {
		t.Errorf("re-rollback error.code = %v, want conflict", errBody["code"])
	}
	// unknown batch id → not_found, exit 1
	stdout, code = runCLI(t, "metrics", "--db", db, "rollback", "--batch", "mbat_nope")
	if code != 1 {
		t.Errorf("rollback unknown: exit = %d, want 1 (stdout: %s)", code, stdout)
	}
	if err := json.Unmarshal([]byte(stdout), &errOut); err != nil {
		t.Fatalf("rollback unknown: stdout is not JSON: %v\n%s", err, stdout)
	}
	errBody, _ = errOut["error"].(map[string]any)
	if errBody["code"] != "not_found" {
		t.Errorf("rollback unknown error.code = %v, want not_found", errBody["code"])
	}

	// ⑦ the usage set: missing subcommand, unknown subcommand, missing
	// --batch, positional garbage.
	for _, args := range [][]string{
		{"metrics", "--db", db},
		{"metrics", "--db", db, "__nope__"},
		{"metrics", "--db", db, "rollback"},
		{"metrics", "--db", db, "rollback", "--batch", batchID, "extra"},
		{"metrics", "--db", db, "list", "extra"},
	} {
		stdout, code = runCLI(t, args...)
		if code != 2 {
			t.Errorf("%v: exit = %d, want 2 (stdout: %s)", args, code, stdout)
		}
	}
	// the missing-subcommand usage names BOTH subcommands (the derived
	// closed set — #108's doctrine carried to the new dispatcher).
	usageOut, code := runCLI(t, "metrics", "--db", db)
	if code != 2 {
		t.Errorf("metrics (no subcommand): exit = %d, want 2 (stdout: %s)", code, usageOut)
	}
	if !strings.Contains(usageOut, "list") || !strings.Contains(usageOut, "rollback") {
		t.Errorf("usage text must name both metrics subcommands: %s", usageOut)
	}
}
