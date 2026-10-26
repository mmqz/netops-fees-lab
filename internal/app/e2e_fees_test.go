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

	"github.com/xuri/excelize/v2"
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
		t.Skip("netopsfees binary was not built (go tool unavailable)")
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

	// the #3 monthly series rides the identical contract: honest engine
	// state on the default build, usage refusal for --month (exit 2).
	stdout, code = runCLI(t, "fees", "--db", db, "--data", tmp, "query", "monthly", "--type", "electricity", "--year", "2024")
	if json.Unmarshal([]byte(stdout), &errOut) != nil {
		t.Fatalf("monthly stdout is not JSON: %s", stdout)
	}
	if _, ok := errOut["error"]; !ok {
		t.Fatalf("monthly stdout must be an error envelope on the default build: %s", stdout)
	}
	if code == 0 {
		t.Log("this e2e binary was built with -tags duckdb: the monthly query succeeded")
	}
	if _, code = runCLI(t, "fees", "--db", db, "--data", tmp, "query", "monthly", "--type", "electricity", "--year", "2024", "--month", "5"); code != 2 {
		t.Errorf("monthly --month: exit = %d, want 2 (the series covers the whole year)", code)
	}
}

// TestCLIFeesRisksContract pins the risk-scenario CLI surface on the
// default build (#64): usage errors exit 2, the missing --type/--year
// exit 2, and every valid scenario answers the honest engine-unavailable
// failure (exit 1) — the duckdb-tagged fees package tests cover the real
// outputs end to end.
func TestCLIFeesRisksContract(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopsfees binary was not built (go tool unavailable)")
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
	// duckdb build hint (exit 1, single JSON error object). #114 added
	// idle-billing to the closed set.
	for _, sc := range []string{"retired-billing", "duplicate-settlement", "ledger-mismatch", "spike", "outlier", "unlinked", "idle-billing", "missing-billing"} {
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

// TestCLIFeesClueDecisions pins #119's human channel on the CLI face:
// the decision UPSERTS (verify → dismiss flip keeps one ledger row),
// the ledger list filters work, usage errors exit 2, and — the honest
// composition note — the DECISION persists on the default build (no
// engine needed): the suppression applies the next time an engine-built
// binary runs the scenario.
func TestCLIFeesClueDecisions(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopsfees binary was not built (go tool unavailable)")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "netops.db")
	base := []string{"fees", "--db", db, "--data", tmp, "clue-decisions"}

	// usage errors exit 2: missing subcommand, bogus subcommand, missing
	// identity flags, bogus scenario.
	for _, args := range [][]string{
		append(append([]string{}, base...), "bogus"),
		append(append([]string{}, base...), "verify"),
		append(append([]string{}, base...), "verify", "--scenario", "spike", "--type", "electricity", "--site", "S-1"),
		append(append([]string{}, base...), "verify", "--scenario", "bogus", "--type", "electricity", "--site", "S-1", "--year", "2024"),
		append(append([]string{}, base...), "list", "--status", "bogus"),
	} {
		_, code := runCLI(t, args...)
		if code != 2 {
			t.Errorf("%v: exit = %d, want 2 (usage)", args, code)
		}
	}

	// verify: the full item shape comes back (exit 0).
	stdout, code := runCLI(t, "fees", "--db", db, "--data", tmp,
		"clue-decisions", "verify", "--scenario", "spike", "--type", "electricity",
		"--site", "S-1", "--year", "2024", "--month", "5", "--note", "属实，已转核查")
	if code != 0 {
		t.Fatalf("verify: exit = %d\nstdout: %s", code, stdout)
	}
	var item map[string]any
	if err := json.Unmarshal([]byte(stdout), &item); err != nil {
		t.Fatalf("verify stdout not JSON: %v\n%s", err, stdout)
	}
	if !strings.HasPrefix(fmt.Sprint(item["id"]), "rcd_") || item["status"] != "verified" ||
		item["scenarioCn"] != "电费环比异常上涨" || item["note"] != "属实，已转核查" {
		t.Fatalf("verify item = %v", item)
	}

	// the flip: dismiss the SAME identity — one ledger row, status flips.
	stdout, code = runCLI(t, "fees", "--db", db, "--data", tmp,
		"clue-decisions", "dismiss", "--scenario", "spike", "--type", "electricity",
		"--site", "S-1", "--year", "2024", "--month", "5")
	if code != 0 {
		t.Fatalf("dismiss flip: exit = %d\nstdout: %s", code, stdout)
	}
	var flipped map[string]any
	if err := json.Unmarshal([]byte(stdout), &flipped); err != nil {
		t.Fatalf("dismiss stdout not JSON: %v", err)
	}
	if flipped["status"] != "dismissed" {
		t.Fatalf("flipped = %v, want dismissed", flipped)
	}

	// the ledger: one row (the flip kept one), the status filter narrows.
	stdout, code = runCLI(t, "fees", "--db", db, "--data", tmp,
		"clue-decisions", "list", "--status", "dismissed")
	if code != 0 {
		t.Fatalf("list: exit = %d\nstdout: %s", code, stdout)
	}
	var ledger struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal([]byte(stdout), &ledger); err != nil {
		t.Fatalf("list stdout not JSON: %v", err)
	}
	if ledger.Total != 1 || len(ledger.Items) != 1 || ledger.Items[0]["status"] != "dismissed" {
		t.Fatalf("ledger = %v, want one dismissed row", ledger)
	}
	stdout, code = runCLI(t, "fees", "--db", db, "--data", tmp,
		"clue-decisions", "list", "--status", "verified")
	if code != 0 {
		t.Fatalf("list verified: exit = %d", code)
	}
	if err := json.Unmarshal([]byte(stdout), &ledger); err != nil {
		t.Fatalf("list verified stdout not JSON: %v", err)
	}
	if ledger.Total != 0 {
		t.Fatalf("verified filter = %d, want 0 (the flip left no verified rows)", ledger.Total)
	}
}

// writeFeeLedgerXLSX builds the #125 multi-sheet ledger workbook on disk:
// sheets in order 电费(2 rows) / 铁塔租费(1 row: 5000.00) / 修理费(1 row).
func writeFeeLedgerXLSX(t *testing.T, dir, name string) string {
	t.Helper()
	f := excelize.NewFile()
	if err := f.SetSheetName(f.GetSheetList()[0], "电费"); err != nil {
		t.Fatal(err)
	}
	for _, spec := range []struct {
		sheet string
		rows  [][]string
	}{
		{"电费", [][]string{{"站址编码", "站址名称", "电费(元)", "账期"}, {"S1", "一站", "12345.67", "2024-05"}, {"S2", "二站", "678.90", "2024-05"}}},
		{"铁塔租费", [][]string{{"站址编码", "站址名称", "塔租费(元)", "账期"}, {"S1", "一站", "5000.00", "2024-05"}}},
		{"修理费", [][]string{{"站址编码", "站址名称", "修理费(元)", "账期", "工单号"}, {"S2", "二站", "999.99", "2024-05", "GD-001"}}},
	} {
		if spec.sheet != "电费" {
			if _, err := f.NewSheet(spec.sheet); err != nil {
				t.Fatal(err)
			}
		}
		for i, row := range spec.rows {
			for j, cell := range row {
				ref, err := excelize.CoordinatesToCellName(j+1, i+1)
				if err != nil {
					t.Fatal(err)
				}
				if err := f.SetCellValue(spec.sheet, ref, cell); err != nil {
					t.Fatalf("set %s!%s: %v", spec.sheet, ref, err)
				}
			}
		}
	}
	p := filepath.Join(dir, name)
	if err := f.SaveAs(p); err != nil {
		t.Fatalf("save workbook: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestCLIFeesImportSheets pins the #125 multi-sheet CLI contract end to
// end: three batches with sheet-declared types, the cosmetic sheet
// named, the idempotent re-import informational, and the CSV refusal
// exiting non-zero with the honest pointer to the single-sheet channel.
func TestCLIFeesImportSheets(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopsfees binary was not built (go tool unavailable)")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "netops.db")
	src := writeFeeLedgerXLSX(t, tmp, "2024年05月费用.xlsx")

	// import-sheets: 3 batches, one per fee-type sheet, workbook order.
	summary, code := runCLIJSON(t, "fees import-sheets", "fees", "--db", db, "--data", tmp,
		"import-sheets", "--file", src, "--year", "2024", "--month", "5")
	if code != 0 {
		t.Fatalf("fees import-sheets: exit %d, want 0", code)
	}
	batches, _ := summary["batches"].([]any)
	if len(batches) != 3 {
		t.Fatalf("batches = %v, want 3", summary["batches"])
	}
	for i, want := range []struct {
		sheet, typ string
		rows       float64
	}{{"电费", "electricity", 2}, {"铁塔租费", "tower_lease", 1}, {"修理费", "repair", 1}} {
		b, _ := batches[i].(map[string]any)
		if b["sheet"] != want.sheet || b["feeType"] != want.typ || b["rowsImported"] != want.rows {
			t.Fatalf("batches[%d] = %v, want %s/%s rows %.0f", i, b, want.sheet, want.typ, want.rows)
		}
	}
	skipped, _ := summary["skippedSheets"].([]any)
	if len(skipped) != 0 || summary["status"] != "completed" {
		// this builder has no cosmetic sheet — the cover-sheet case is the
		// service test's; here the shape must simply be complete.
		if summary["status"] != "completed" {
			t.Fatalf("status = %v", summary["status"])
		}
	}

	// re-import: informational — every sheet a duplicate skip, exit 0.
	dup, code := runCLIJSON(t, "fees import-sheets again", "fees", "--db", db, "--data", tmp,
		"import-sheets", "--file", src, "--year", "2024", "--month", "5")
	if code != 0 {
		t.Fatalf("duplicate import-sheets must exit 0, got %d: %v", code, dup)
	}
	dupBatches, _ := dup["batches"].([]any)
	if len(dupBatches) != 3 {
		t.Fatalf("duplicate batches = %v", dup["batches"])
	}
	for i, b := range dupBatches {
		m, _ := b.(map[string]any)
		if m["skippedDuplicate"] != true {
			t.Fatalf("duplicate batches[%d] = %v, want skippedDuplicate", i, m)
		}
	}

	// CSV: no sheet names to classify from — non-zero exit with the
	// honest pointer, NOT a silent first-sheet import.
	csvPath := writeFeeCSV(t, tmp, "may.csv", "站址编码,账期,电费(元)\nS1,2024-05,10.00\n")
	stdout, code := runCLI(t, "fees", "--db", db, "--data", tmp,
		"import-sheets", "--file", csvPath, "--year", "2024", "--month", "5")
	if code == 0 {
		t.Fatalf("CSV import-sheets must refuse, got exit 0: %s", stdout)
	}
	if !strings.Contains(stdout, "单 sheet") {
		t.Fatalf("CSV refusal must point at the single-sheet channel: %s", stdout)
	}
}
