package app

// TestCLIRenamesFlow is the CLI smoke test of the rename confirmation
// channel (W6-D): import → renamed re-import → pending proposal (renames
// list) → apply (the old resource keeps its id and takes the new key, the
// new resource is gone) → the same renamed sheet again is a pure skip (no
// new proposal, no W005) — all through the single-JSON stdout contract.
// TestCLIRenamesReject covers the reject side in its own database.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"
)

// baseStationRowCLI builds one real-shaped 无线基站表 data row.
func baseStationRowCLI(name, stationIP string) []string {
	return []string{"宏基站", name, "", "是", "4.28.2.3", "环1", "链1", "枢纽楼A4", "gei_1/11",
		"12.17.10.126", stationIP, "BBU接入", "202366", ""}
}

// writeStationWorkbookCLI writes the one-row base_station workbook and
// returns its path.
func writeStationWorkbookCLI(t *testing.T, dir, name, file, stationIP string) string {
	t.Helper()
	f := excelize.NewFile()
	if err := f.SetSheetName("Sheet1", "无线基站表"); err != nil {
		t.Fatalf("rename sheet: %v", err)
	}
	rows := [][]string{
		{"基站类型", "无线基站名称", "基站编码", "电联共享", "归属A IP", "环", "链", "归属A名称", "归属A端口", "网关IP", "基站IP", "描述", "工单号", "备注"},
		baseStationRowCLI(name, stationIP),
	}
	for r, row := range rows {
		for c, v := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, r+1)
			if err != nil {
				t.Fatalf("cell name: %v", err)
			}
			if err := f.SetCellStr("无线基站表", cell, v); err != nil {
				t.Fatalf("set cell: %v", err)
			}
		}
	}
	path := filepath.Join(dir, file)
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("save workbook: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close workbook: %v", err)
	}
	return path
}

// stationMapJSON is the confirmed mapping of the one-sheet base_station
// workbook.
const stationMapJSON = `{"sheets":[
	{"sheet":"无线基站表","action":"import","resourceType":"base_station","keyColumns":["base_station_code"],
	 "columnMapping":{"base_station_type":"基站类型","base_station_name":"无线基站名称","base_station_code":"基站编码","sharing":"电联共享","uplink_a_ip":"归属A IP","ring":"环","chain":"链","uplink_a_name":"归属A名称","uplink_a_port":"归属A端口","gateway_ip":"网关IP","base_station_ip":"基站IP","description":"描述","work_order":"工单号","remark":"备注"}}
]}`

func TestCLIRenamesFlow(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopslab binary was not built (go tool unavailable)")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "netops.db")
	mapFile := filepath.Join(tmp, "map.json")
	if err := os.WriteFile(mapFile, []byte(stationMapJSON), 0o644); err != nil {
		t.Fatalf("write mapping: %v", err)
	}

	// Import 1: 动力机房2.
	orig := writeStationWorkbookCLI(t, tmp, "动力机房2", "stations-v1.xlsx", "172.7.10.125/30")
	if _, code := runCLIJSON(t, "import v1", "import", "--file", orig, "--map", mapFile, "--db", db); code != 0 {
		t.Fatalf("import v1: exit %d, want 0", code)
	}
	// Import 2: the renamed sheet (same attributes, new name/key).
	renamed := writeStationWorkbookCLI(t, tmp, "动力机房2新", "stations-v2.xlsx", "172.7.10.125/30")
	if _, code := runCLIJSON(t, "import v2", "import", "--file", renamed, "--map", mapFile, "--db", db); code != 0 {
		t.Fatalf("import v2: exit %d, want 0", code)
	}

	// renames list: one pending proposal with the full shape.
	listing, code := runCLIJSON(t, "renames list", "renames", "--db", db, "list", "--status", "pending")
	if code != 0 {
		t.Fatalf("renames list: exit %d, want 0", code)
	}
	items, _ := listing["items"].([]any)
	if total, _ := listing["total"].(float64); total != 1 || len(items) != 1 {
		t.Fatalf("renames list = %d items (total %v), want 1/1: %v", len(items), listing["total"], listing)
	}
	item, _ := items[0].(map[string]any)
	if item["status"] != "pending" || item["resourceType"] != "base_station" ||
		item["oldKey"] != "动力机房2" || item["newKey"] != "动力机房2新" ||
		item["naturalKey"] != "动力机房2" || item["newResourceId"] == "" || item["resourceId"] == "" {
		t.Fatalf("rename item = %v, want pending base_station 动力机房2→动力机房2新", item)
	}
	matched, _ := item["matchedFields"].([]any)
	if len(matched) == 0 {
		t.Fatalf("matched fields = %v, want the non-empty equal fields", item["matchedFields"])
	}
	proposalID, _ := item["id"].(string)
	oldID, _ := item["resourceId"].(string)

	// renames apply: the old resource keeps its id and takes the new key;
	// the new resource is gone.
	applied, code := runCLIJSON(t, "renames apply", "renames", "--db", db, "apply", "--id", proposalID)
	if code != 0 {
		t.Fatalf("renames apply: exit %d, want 0", code)
	}
	if applied["status"] != "applied" || applied["newKey"] != "动力机房2新" || applied["decidedAt"] == nil {
		t.Fatalf("applied item = %v, want applied 动力机房2→动力机房2新 with decidedAt", applied)
	}
	resources, code := runCLIJSON(t, "query resources", "query", "--db", db, "resources", "--type", "base_station")
	if code != 0 {
		t.Fatalf("query resources: exit %d, want 0", code)
	}
	resItems, _ := resources["items"].([]any)
	if len(resItems) != 1 {
		t.Fatalf("base_station resources after apply = %d, want 1 (merged)", len(resItems))
	}
	res, _ := resItems[0].(map[string]any)
	if res["id"] != oldID || res["naturalKey"] != "动力机房2新" {
		t.Fatalf("merged resource = %v, want the OLD id %s with key 动力机房2新", res, oldID)
	}

	// Re-apply of the applied proposal → exit 1 with the conflict code.
	stdout, code := runCLI(t, "renames", "--db", db, "apply", "--id", proposalID)
	if code != 1 {
		t.Errorf("re-apply: exit = %d, want 1 (stdout: %s)", code, stdout)
	}
	var errOut map[string]any
	if err := json.Unmarshal([]byte(stdout), &errOut); err != nil {
		t.Fatalf("re-apply: stdout is not a JSON error object: %v\n%s", err, stdout)
	}
	errBody, _ := errOut["error"].(map[string]any)
	if errBody["code"] != "conflict" {
		t.Errorf("re-apply error.code = %v, want conflict", errBody["code"])
	}
	// Unknown id exits 1 as well.
	stdout, code = runCLI(t, "renames", "--db", db, "apply", "--id", "rp_nope")
	if code != 1 {
		t.Errorf("apply unknown: exit = %d, want 1 (stdout: %s)", code, stdout)
	}

	// Import 3: the SAME renamed sheet again — the new key exists with
	// equal attributes: a pure skip (0 imported, no new proposal, no W005).
	summary, code := runCLIJSON(t, "import v3", "import", "--file", renamed, "--map", mapFile, "--db", db)
	if code != 0 {
		t.Fatalf("import v3: exit %d, want 0", code)
	}
	sm, _ := summary["summary"].(map[string]any)
	if sm["rowsImported"] != float64(0) || sm["rowsSkipped"] != float64(1) {
		t.Fatalf("import v3 summary = %v, want 0 imported / 1 skipped (idempotent)", summary)
	}
	if gotIssues, _ := summary["issues"].(map[string]any); gotIssues["warning"] != float64(0) {
		t.Errorf("import v3 issues = %v, want zero warnings (pure skip; the apply must have synced the key-bearing attribute so no W005 fires)", gotIssues)
	}
	listing, code = runCLIJSON(t, "renames list 2", "renames", "--db", db, "list")
	if code != 0 {
		t.Fatalf("renames list 2: exit %d, want 0", code)
	}
	if total, _ := listing["total"].(float64); total != 1 {
		t.Fatalf("renames list 2 total = %v, want 1 (the applied proposal, no new ones)", listing["total"])
	}

	// Usage errors: missing subcommand, bogus status/type, missing --id.
	for _, args := range [][]string{
		{"renames", "--db", db},
		{"renames", "--db", db, "list", "--status", "bogus"},
		{"renames", "--db", db, "list", "--type", "vlan"},
		{"renames", "--db", db, "apply"},
		{"renames", "--db", db, "reject"},
	} {
		stdout, code = runCLI(t, args...)
		if code != 2 {
			t.Errorf("%v: exit = %d, want 2 (stdout: %s)", args, code, stdout)
		}
	}

}

// TestCLIRenamesReject covers the reject side in its own database: the
// proposal flips to rejected, both resources stay independent.
func TestCLIRenamesReject(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopslab binary was not built (go tool unavailable)")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "netops.db")
	mapFile := filepath.Join(tmp, "map.json")
	if err := os.WriteFile(mapFile, []byte(stationMapJSON), 0o644); err != nil {
		t.Fatalf("write mapping: %v", err)
	}

	orig := writeStationWorkbookCLI(t, tmp, "学府路2", "stations-v1.xlsx", "172.7.20.1/30")
	if _, code := runCLIJSON(t, "import v1", "import", "--file", orig, "--map", mapFile, "--db", db); code != 0 {
		t.Fatalf("import v1: exit %d, want 0", code)
	}
	renamed := writeStationWorkbookCLI(t, tmp, "学府路2新", "stations-v2.xlsx", "172.7.20.1/30")
	if _, code := runCLIJSON(t, "import v2", "import", "--file", renamed, "--map", mapFile, "--db", db); code != 0 {
		t.Fatalf("import v2: exit %d, want 0", code)
	}
	listing, code := runCLIJSON(t, "renames list", "renames", "--db", db, "list", "--status", "pending")
	if code != 0 {
		t.Fatalf("renames list: exit %d, want 0", code)
	}
	items, _ := listing["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("renames list = %d items, want 1", len(items))
	}
	item, _ := items[0].(map[string]any)
	proposalID, _ := item["id"].(string)

	rejected, code := runCLIJSON(t, "renames reject", "renames", "--db", db, "reject", "--id", proposalID)
	if code != 0 {
		t.Fatalf("renames reject: exit %d, want 0", code)
	}
	if rejected["status"] != "rejected" {
		t.Fatalf("rejected item = %v, want rejected", rejected)
	}
	// Both resources coexist (independent identities).
	resources, code := runCLIJSON(t, "query resources", "query", "--db", db, "resources", "--type", "base_station")
	if code != 0 {
		t.Fatalf("query resources: exit %d, want 0", code)
	}
	resItems, _ := resources["items"].([]any)
	if len(resItems) != 2 {
		t.Fatalf("base_station resources after reject = %d, want 2 (independent)", len(resItems))
	}
	// Re-reject → exit 1 conflict.
	stdout, code := runCLI(t, "renames", "--db", db, "reject", "--id", proposalID)
	if code != 1 {
		t.Errorf("re-reject: exit = %d, want 1 (stdout: %s)", code, stdout)
	}
}
