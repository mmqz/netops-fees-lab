package app

// e2e_templates_test.go — the W24 CLI acceptance (§12-B7 方案 A, issue #28):
// the unattended import (`import --auto`), the template capture
// (`templates save`), the inventory (`templates list`) and the opt-out
// (`templates delete`), driven as the REAL binary through the §10
// single-JSON-object stdout contract. The refusal (renamed column) pins
// exit 1 + the no_matching_template error envelope naming the sheet.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/mmqz/netops-lab/internal/mapping"
)

// w24Sheet / w24Headers mirror the api-test fixture shape (the
// testfixtures device header row).
var (
	w24Sheet   = "设备清单"
	w24Headers = []string{"设备编码", "设备名称", "城市", "厂商", "型号", "管理IP", "状态"}
)

// w24Write builds ONE device-sheet workbook at path with the given rows.
func w24Write(t *testing.T, path string, headers []string, rows [][]string) string {
	t.Helper()
	f := excelize.NewFile()
	if err := f.SetSheetName("Sheet1", w24Sheet); err != nil {
		t.Fatalf("rename sheet: %v", err)
	}
	grid := append([][]string{headers}, rows...)
	for r, row := range grid {
		for c, v := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, r+1)
			if err != nil {
				t.Fatalf("cell name: %v", err)
			}
			if err := f.SetCellStr(w24Sheet, cell, v); err != nil {
				t.Fatalf("set cell: %v", err)
			}
		}
	}
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("save %s: %v", path, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
	return path
}

// w24Rows builds n valid device rows with the given code/IP prefix.
func w24Rows(prefix string, base, n int) [][]string {
	rows := make([][]string, n)
	for i := 0; i < n; i++ {
		rows[i] = []string{
			prefix + "-0" + itoaE2E(i), "设备-" + prefix + "-" + itoaE2E(i), "杭州", "华为", "NE40E",
			"10.80." + itoaE2E(base) + "." + itoaE2E(i+1), "在网",
		}
	}
	return rows
}

func itoaE2E(n int) string {
	return string(rune('0' + n))
}

// TestCLITemplatesUnattended walks the unattended journey through the real
// binary: day 1 the ordinary flow (analyze → import --map → templates
// save); day 2 the SAME-header workbook imports with --auto and ZERO
// mapping files (exit 0 + the summary envelope); a renamed-column workbook
// REFUSES (exit 1, no_matching_template naming the sheet — the batch stays
// awaiting_mapping); templates list/delete round-trip; --auto and --map are
// mutually exclusive (exit 2).
func TestCLITemplatesUnattended(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopslab binary was not built (go tool unavailable)")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "netops.db")

	// Day 1: the ordinary flow. The mapping file is the suggested mapping
	// the offline analysis prints (the CLI's own §10 flow).
	day1 := w24Write(t, filepath.Join(tmp, "day1.xlsx"), w24Headers, w24Rows("D1", 1, 4))
	analysis, code := runCLIJSON(t, "analyze day1", "analyze", "--file", day1)
	if code != 0 {
		t.Fatalf("analyze day1: exit %d, want 0", code)
	}
	suggested, err := json.Marshal(analysis["suggestedMapping"])
	if err != nil || string(suggested) == "null" {
		t.Fatalf("analyze day1: suggestedMapping = %s (err %v)", suggested, err)
	}
	mapFile := filepath.Join(tmp, "map-day1.json")
	if err := os.WriteFile(mapFile, suggested, 0o644); err != nil {
		t.Fatalf("write map: %v", err)
	}
	summary, code := runCLIJSON(t, "import day1", "import", "--file", day1, "--map", mapFile, "--db", db)
	if code != 0 {
		t.Fatalf("import day1: exit %d, want 0", code)
	}
	day1Batch, _ := summary["batchId"].(string)
	if day1Batch == "" || summary["status"] != "completed" {
		t.Fatalf("import day1: batchId/status = %v/%v", day1Batch, summary["status"])
	}

	// The one-click save: capture the confirmed mapping of the completed batch.
	saved, code := runCLIJSON(t, "templates save", "templates", "save", "--db", db, "--batch", day1Batch, "--name", "机房日更模板")
	if code != 0 {
		t.Fatalf("templates save: exit %d, want 0", code)
	}
	items, _ := saved["items"].([]any)
	if n, _ := saved["total"].(float64); n != 1 || len(items) != 1 {
		t.Fatalf("templates save = %d items (total %v), want 1", len(items), saved["total"])
	}
	item, _ := items[0].(map[string]any)
	tplID, _ := item["templateId"].(string)
	fp, _ := item["headerFingerprint"].(string)
	if tplID == "" || fp != mapping.Fingerprint(w24Sheet, w24Headers) {
		t.Errorf("saved template id/fingerprint = %q/%q, want the canonical sheet fingerprint %s",
			tplID, fp, mapping.Fingerprint(w24Sheet, w24Headers))
	}
	if item["sourceName"] != "机房日更模板" || item["confirmedBatch"] != day1Batch {
		t.Errorf("saved template sourceName/confirmedBatch = %v/%v, want the explicit label and the batch", item["sourceName"], item["confirmedBatch"])
	}

	// Day 2: same headers, fresh rows — the UNATTENDED import. No --map at
	// all: the mapping comes from the stored template.
	rows := append(append([][]string(nil), w24Rows("D1", 1, 4)...), w24Rows("D2", 2, 3)...)
	day2 := w24Write(t, filepath.Join(tmp, "day2.xlsx"), w24Headers, rows)
	auto, code := runCLIJSON(t, "import --auto day2", "import", "--file", day2, "--db", db, "--auto")
	if code != 0 {
		t.Fatalf("import --auto day2: exit %d, want 0 (all import sheets matched)", code)
	}
	if auto["status"] != "completed" {
		t.Fatalf("import --auto day2: status = %v, want completed", auto["status"])
	}
	sm, _ := auto["summary"].(map[string]any)
	if sm == nil || int(sm["rowsTotal"].(float64)) != 7 || int(sm["rowsImported"].(float64)) != 3 ||
		int(sm["rowsSkipped"].(float64)) != 4 || int(sm["rowsFailed"].(float64)) != 0 {
		t.Fatalf("import --auto day2 summary = %v, want 7 total / 3 imported / 4 skipped / 0 failed", sm)
	}

	// The refusal: ONE RENAMED column → exit 1, the no_matching_template
	// envelope naming the sheet (表头变更感知); the batch stays
	// awaiting_mapping for the human --map flow.
	renamedHeaders := append([]string(nil), w24Headers...)
	renamedHeaders[1] = "设备名称（新）"
	renamed := w24Write(t, filepath.Join(tmp, "renamed.xlsx"), renamedHeaders, w24Rows("D3", 3, 2))
	refusedJSON, code := runCLI(t, "import", "--file", renamed, "--db", db, "--auto")
	if code != 1 {
		t.Fatalf("import --auto renamed: exit %d, want 1 (refusal)", code)
	}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(refusedJSON), &envelope); err != nil {
		t.Fatalf("refused stdout is not the error envelope: %v\nstdout: %s", err, refusedJSON)
	}
	if envelope.Error.Code != "no_matching_template" {
		t.Errorf("refusal code = %q, want no_matching_template", envelope.Error.Code)
	}
	if !strings.Contains(envelope.Error.Message, w24Sheet) {
		t.Errorf("refusal message = %q, want it to name the sheet %q", envelope.Error.Message, w24Sheet)
	}

	// templates list: the inventory with the stored mapping document.
	listed, code := runCLIJSON(t, "templates list", "templates", "list", "--db", db)
	if code != 0 {
		t.Fatalf("templates list: exit %d, want 0", code)
	}
	listItems, _ := listed["items"].([]any)
	if n, _ := listed["total"].(float64); n != 1 || len(listItems) != 1 {
		t.Fatalf("templates list = %d items (total %v), want 1", len(listItems), listed["total"])
	}
	listItem, _ := listItems[0].(map[string]any)
	if doc, _ := listItem["mapping"].(map[string]any); doc == nil {
		t.Errorf("templates list mapping = %v, want the decoded single-sheet document", listItem["mapping"])
	}

	// templates delete: round-trip, then not_found on repeat.
	deleted, code := runCLIJSON(t, "templates delete", "templates", "delete", "--db", db, "--id", tplID)
	if code != 0 || deleted["deleted"] != true {
		t.Fatalf("templates delete = %v (exit %d), want {deleted:true} exit 0", deleted, code)
	}
	if _, code := runCLIJSON(t, "templates delete repeat", "templates", "delete", "--db", db, "--id", tplID); code != 1 {
		t.Fatalf("templates delete repeat: exit %d, want 1 (not_found)", code)
	}

	// With the template gone, --auto falls back to refusing honestly.
	if _, code := runCLIJSON(t, "import --auto after delete", "import", "--file", day2, "--db", db, "--auto"); code != 1 {
		t.Fatalf("import --auto after template delete: exit %d, want 1 (no template → no unattended import)", code)
	}

	// --auto and --map are mutually exclusive (usage error, exit 2).
	if _, code := runCLIJSON(t, "import auto+map", "import", "--file", day2, "--db", db, "--auto", "--map", mapFile); code != 2 {
		t.Fatalf("import --auto --map: exit %d, want 2 (mutually exclusive)", code)
	}
}
