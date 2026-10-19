package app

// E2E of the fee analysis layer CLI (#58/#60/#61): import → idempotent
// duplicate → list → rollback → retry after rollback. The query presets
// are engine-covered (engine_duckdb_test.go, tagged); the default-build
// e2e proves the query command's unavailability message too.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFeeCSV(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCLIFeesFlow(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopslab binary was not built (go tool unavailable)")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "netops.db")

	src := writeFeeCSV(t, tmp, "may-bills.csv",
		"站址编码,账期,电费(元)\n0230101ABCDEFG01,2024-05,1234.56\n0230101ABCDEFG02,2024-05,654.00\n")

	// import: 2 rows, one partition file, completed.
	summary, code := runCLIJSON(t, "fees import", "fees", "--db", db, "--data", tmp,
		"import", "--file", src, "--type", "electricity", "--year", "2024", "--month", "5")
	if code != 0 {
		t.Fatalf("fees import: exit %d, want 0", code)
	}
	if summary["status"] != "completed" || summary["rowsImported"] != float64(2) || summary["rowsTotal"] != float64(2) {
		t.Fatalf("import summary = %v", summary)
	}
	files, _ := summary["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("files = %v, want one partition file", summary["files"])
	}
	batchID, _ := summary["batchId"].(string)

	// the snapshot exists under the declared period path.
	snap := filepath.Join(tmp, "fee-imports", "electricity", "2024", "05")
	entries, err := os.ReadDir(snap)
	if err != nil || len(entries) != 1 {
		t.Fatalf("snapshot entries = %v (err %v), want 1", entries, err)
	}

	// re-import: informational duplicate (exit 0, skippedDuplicate).
	dup, code := runCLIJSON(t, "fees re-import", "fees", "--db", db, "--data", tmp,
		"import", "--file", src, "--type", "electricity", "--year", "2024", "--month", "5")
	if code != 0 {
		t.Fatalf("duplicate import must exit 0 (informational), got %d: %v", code, dup)
	}
	if dup["skippedDuplicate"] != true || dup["batchId"] != batchID {
		t.Fatalf("duplicate summary = %v", dup)
	}

	// list: one completed batch with the label + period rendering.
	listing, code := runCLIJSON(t, "fees list", "fees", "--db", db, "--data", tmp, "list")
	if code != 0 {
		t.Fatalf("fees list: exit %d", code)
	}
	if listing["total"] != float64(1) {
		t.Fatalf("list total = %v", listing["total"])
	}
	items, _ := listing["items"].([]any)
	item, _ := items[0].(map[string]any)
	if item["feeTypeLabel"] != "电费" || item["period"] != "2024-05" || item["status"] != "completed" {
		t.Fatalf("list item = %v", item)
	}

	// rollback: parquet gone, snapshot kept, batch rolled_back.
	rb, code := runCLIJSON(t, "fees rollback", "fees", "--db", db, "--data", tmp,
		"rollback", "--batch", batchID)
	if code != 0 {
		t.Fatalf("fees rollback: exit %d", code)
	}
	if rb["status"] != "rolled_back" || rb["snapshotKept"] == "" {
		t.Fatalf("rollback result = %v", rb)
	}
	if _, err := os.Stat(filepath.Join(tmp, "analysis", "fees", "fee_type=electricity", "year=2024", "month=05")); !os.IsNotExist(err) {
		t.Fatalf("partition dir must be gone after rollback (err %v)", err)
	}
	if entries, err := os.ReadDir(snap); err != nil || len(entries) != 1 {
		t.Fatalf("snapshot must survive the rollback (err %v)", err)
	}

	// retry after rollback: the key is free again → completes.
	retry, code := runCLIJSON(t, "fees retry", "fees", "--db", db, "--data", tmp,
		"import", "--file", src, "--type", "electricity", "--year", "2024", "--month", "5")
	if code != 0 || retry["status"] != "completed" || retry["rowsImported"] != float64(2) {
		t.Fatalf("retry after rollback = %v (exit %d), want completed 2 rows", retry, code)
	}

	// usage errors: missing subcommand, bogus type, missing flags.
	for _, args := range [][]string{
		{"fees", "--db", db},
		{"fees", "--db", db, "import", "--file", src, "--type", "groceries", "--year", "2024", "--month", "5"},
		{"fees", "--db", db, "import", "--type", "electricity", "--year", "2024", "--month", "5"},
		{"fees", "--db", db, "list", "--status", "bogus"},
		{"fees", "--db", db, "rollback"},
		{"fees", "--db", db, "query"},
		{"fees", "--db", db, "bogus"},
	} {
		if _, code := runCLI(t, args...); code != 2 {
			t.Errorf("%v: exit = %d, want 2 (usage error)", args, code)
		}
	}

	// the query command on the DEFAULT build reports the engine state
	// honestly (exit 1, JSON error envelope naming the duckdb tag).
	stdout, code := runCLI(t, "fees", "--db", db, "--data", tmp, "query", "totals", "--type", "electricity", "--year", "2024")
	var errOut map[string]any
	if json.Unmarshal([]byte(stdout), &errOut) != nil {
		t.Fatalf("query stdout is not JSON: %s", stdout)
	}
	if _, ok := errOut["error"]; !ok {
		t.Fatalf("query stdout must be an error envelope: %s", stdout)
	}
	if code == 0 {
		t.Log("this e2e binary was built with -tags duckdb: the query succeeded")
	}
}

// TestCLIFeesRisksContract pins the six-scenario CLI surface on the
// default build (#64): usage errors exit 2, the missing --type/--year
// exit 2, and every valid scenario answers the honest engine-unavailable
// failure (exit 1) — the duckdb-tagged fees package tests cover the real
// outputs end to end.
func TestCLIFeesRisksContract(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopslab binary was not built (go tool unavailable)")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "netops.db")

	// usage errors: no scenario, bogus scenario, missing --type/--year.
	for _, args := range [][]string{
		{"fees", "--db", db, "--data", tmp, "risks"},
		{"fees", "--db", db, "--data", tmp, "risks", "bogus", "--type", "electricity", "--year", "2024"},
		{"fees", "--db", db, "--data", tmp, "risks", "spike"},
		{"fees", "--db", db, "--data", tmp, "risks", "spike", "--type", "electricity"},
		{"fees", "--db", db, "--data", tmp, "risks", "spike", "--type", "bogus", "--year", "2024"},
	} {
		_, code := runCLI(t, args...)
		if code != 2 {
			t.Errorf("%v: exit = %d, want 2 (usage)", args, code)
		}
	}

	// every valid scenario: the engine-unavailable failure carries the
	// duckdb build hint (exit 1, single JSON error object).
	for _, sc := range []string{"retired-billing", "duplicate-settlement", "ledger-mismatch", "spike", "outlier", "unlinked"} {
		stdout, code := runCLI(t, "fees", "--db", db, "--data", tmp,
			"risks", sc, "--type", "electricity", "--year", "2024")
		if code != 1 {
			t.Errorf("risks %s: exit = %d, want 1 (engine not built)\nstdout: %s", sc, code, stdout)
			continue
		}
		var errOut map[string]any
		if err := json.Unmarshal([]byte(stdout), &errOut); err != nil {
			t.Fatalf("risks %s: stdout not a JSON error object: %v\n%s", sc, err, stdout)
		}
		errBody, _ := errOut["error"].(map[string]any)
		if !strings.Contains(fmt.Sprint(errBody["message"]), "duckdb") {
			t.Errorf("risks %s error message must carry the duckdb hint: %v", sc, errBody["message"])
		}
	}
}
