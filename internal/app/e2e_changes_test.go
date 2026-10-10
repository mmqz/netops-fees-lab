package app

// TestCLIChangesFlow is the CLI smoke test of the attribute change
// confirmation channel (W4-D): import → re-import with a changed mgmt_ip →
// pending proposal (changes list) → apply (the resource attribute really
// changes) → a second proposal dismissed — all through the single-JSON
// stdout contract. The usage-error paths pin the exit code 2 taxonomy.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"
)

// writeDeviceWorkbookCLI writes the one-row device workbook with the given
// mgmt_ip and returns its path.
func writeDeviceWorkbookCLI(t *testing.T, dir, name, ip string) string {
	t.Helper()
	f := excelize.NewFile()
	if err := f.SetSheetName("Sheet1", "设备清单"); err != nil {
		t.Fatalf("rename sheet: %v", err)
	}
	rows := [][]string{
		{"设备编码", "设备名称", "城市", "厂商", "型号", "管理IP", "状态"},
		{"DEV-001", "核心路由1", "杭州", "华为", "NE40E", ip, "在网"},
	}
	for r, row := range rows {
		for c, v := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, r+1)
			if err != nil {
				t.Fatalf("cell name: %v", err)
			}
			if err := f.SetCellStr("设备清单", cell, v); err != nil {
				t.Fatalf("set cell: %v", err)
			}
		}
	}
	path := filepath.Join(dir, name)
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("save workbook: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close workbook: %v", err)
	}
	return path
}

// deviceMapJSON is the confirmed mapping of the one-sheet device workbook.
const deviceMapJSON = `{"sheets":[
	{"sheet":"设备清单","action":"import","resourceType":"device","keyColumns":["device_code"],
	 "columnMapping":{"device_code":"设备编码","device_name":"设备名称","city":"城市","vendor":"厂商","model":"型号","mgmt_ip":"管理IP","status":"状态"}}
]}`

func TestCLIChangesFlow(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopslab binary was not built (go tool unavailable)")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "netops.db")
	mapFile := filepath.Join(tmp, "map.json")
	if err := os.WriteFile(mapFile, []byte(deviceMapJSON), 0o644); err != nil {
		t.Fatalf("write mapping: %v", err)
	}

	// Import 1: DEV-001 with mgmt_ip 10.0.0.1.
	file1 := writeDeviceWorkbookCLI(t, tmp, "devices-v1.xlsx", "10.0.0.1")
	if _, code := runCLIJSON(t, "import v1", "import", "--file", file1, "--map", mapFile, "--db", db); code != 0 {
		t.Fatalf("import v1: exit %d, want 0", code)
	}
	// Import 2: same device re-homed to 10.0.0.9 → skipped + pending change.
	file2 := writeDeviceWorkbookCLI(t, tmp, "devices-v2.xlsx", "10.0.0.9")
	if _, code := runCLIJSON(t, "import v2", "import", "--file", file2, "--map", mapFile, "--db", db); code != 0 {
		t.Fatalf("import v2: exit %d, want 0", code)
	}

	// changes list: one pending proposal with the full shape.
	listing, code := runCLIJSON(t, "changes list", "changes", "--db", db, "list", "--status", "pending")
	if code != 0 {
		t.Fatalf("changes list: exit %d, want 0", code)
	}
	items, _ := listing["items"].([]any)
	if total, _ := listing["total"].(float64); total != 1 || len(items) != 1 {
		t.Fatalf("changes list = %d items (total %v), want 1/1: %v", len(items), listing["total"], listing)
	}
	item, _ := items[0].(map[string]any)
	if item["field"] != "mgmt_ip" || item["oldValue"] != "10.0.0.1" ||
		item["newValue"] != "10.0.0.9" || item["status"] != "pending" ||
		item["resourceType"] != "device" || item["naturalKey"] != "DEV-001" {
		t.Fatalf("change item = %v, want device/DEV-001 mgmt_ip 10.0.0.1→10.0.0.9 pending", item)
	}
	changeID, _ := item["id"].(string)
	if changeID == "" {
		t.Fatalf("change item has no id: %v", item)
	}

	// changes apply: the resource's mgmt_ip changes for real.
	applied, code := runCLIJSON(t, "changes apply", "changes", "--db", db, "apply", "--id", changeID)
	if code != 0 {
		t.Fatalf("changes apply: exit %d, want 0", code)
	}
	if applied["status"] != "applied" || applied["field"] != "mgmt_ip" {
		t.Fatalf("applied item = %v, want applied mgmt_ip", applied)
	}
	resources, code := runCLIJSON(t, "query resources", "query", "--db", db, "resources", "--q", "DEV-001")
	if code != 0 {
		t.Fatalf("query resources: exit %d, want 0", code)
	}
	resItems, _ := resources["items"].([]any)
	if len(resItems) != 1 {
		t.Fatalf("query resources DEV-001 = %d items, want 1", len(resItems))
	}
	res, _ := resItems[0].(map[string]any)
	attrs, _ := res["attributes"].(map[string]any)
	if attrs["mgmt_ip"] != "10.0.0.9" {
		t.Fatalf("DEV-001 attributes after apply = %v, want mgmt_ip 10.0.0.9", attrs)
	}

	// Apply of the applied change again → exit 1 with the conflict code.
	stdout, code := runCLI(t, "changes", "--db", db, "apply", "--id", changeID)
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

	// A second proposal (yet another IP) is dismissed.
	file3 := writeDeviceWorkbookCLI(t, tmp, "devices-v3.xlsx", "10.0.0.10")
	if _, code := runCLIJSON(t, "import v3", "import", "--file", file3, "--map", mapFile, "--db", db); code != 0 {
		t.Fatalf("import v3: exit %d, want 0", code)
	}
	listing, code = runCLIJSON(t, "changes list 2", "changes", "--db", db, "list", "--status", "pending")
	if code != 0 {
		t.Fatalf("changes list 2: exit %d, want 0", code)
	}
	items, _ = listing["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("changes list 2 = %d items, want 1", len(items))
	}
	item, _ = items[0].(map[string]any)
	dismissID, _ := item["id"].(string)
	dismissed, code := runCLIJSON(t, "changes dismiss", "changes", "--db", db, "dismiss", "--id", dismissID)
	if code != 0 {
		t.Fatalf("changes dismiss: exit %d, want 0", code)
	}
	if dismissed["status"] != "dismissed" {
		t.Fatalf("dismissed item = %v, want dismissed", dismissed)
	}
	// The resource keeps the applied value (dismiss never writes).
	resources, _ = runCLIJSON(t, "query resources 2", "query", "--db", db, "resources", "--q", "DEV-001")
	resItems, _ = resources["items"].([]any)
	res, _ = resItems[0].(map[string]any)
	attrs, _ = res["attributes"].(map[string]any)
	if attrs["mgmt_ip"] != "10.0.0.9" {
		t.Fatalf("DEV-001 attributes after dismiss = %v, want mgmt_ip still 10.0.0.9", attrs)
	}

	// Usage errors: missing subcommand and a bogus status exit 2.
	for _, args := range [][]string{
		{"changes", "--db", db},
		{"changes", "--db", db, "list", "--status", "bogus"},
		{"changes", "--db", db, "apply"},
	} {
		stdout, code = runCLI(t, args...)
		if code != 2 {
			t.Errorf("%v: exit = %d, want 2 (stdout: %s)", args, code, stdout)
		}
	}
	// Unknown change id exits 1 with not_found.
	stdout, code = runCLI(t, "changes", "--db", db, "apply", "--id", "chg_nope")
	if code != 1 {
		t.Errorf("apply unknown: exit = %d, want 1 (stdout: %s)", code, stdout)
	}
}
