package app

// TestCLIChangesRefRetargetFlow is the CLI e2e of the W13-D ref-field
// retarget (the machine-room site_id journey): two site imports (SITE-B in
// its own batch) → room imported with site_id = SITE-A (belongs_to edge) →
// re-import with SITE-B → one pending proposal whose LIST entry carries
// relationRetargeted=true (the preview) → the target site's batch is rolled
// back after the proposal exists → CLI apply answers exit 1 +
// ref_target_missing with ZERO changes (the attribute and the edge keep
// SITE-A; the proposal stays pending) → the site is re-imported → apply
// succeeds with relationRetargeted=true, the attribute AND the edge follow
// SITE-B → the re-apply keeps the plain conflict code.

import (
	"context"
	"encoding/json"
	"github.com/mmqz/netops-lab/internal/testsupport"
	"os"
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"
)

// writeSheetWorkbookCLI writes a one-sheet workbook (header + rows) and
// returns its path.
func writeSheetWorkbookCLI(t *testing.T, dir, name, sheet string, header []string, rows [][]string) string {
	t.Helper()
	f := excelize.NewFile()
	if err := f.SetSheetName("Sheet1", sheet); err != nil {
		t.Fatalf("rename default sheet: %v", err)
	}
	grid := append([][]string{header}, rows...)
	for r, row := range grid {
		for c, v := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, r+1)
			if err != nil {
				t.Fatalf("cell name: %v", err)
			}
			if err := f.SetCellStr(sheet, cell, v); err != nil {
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

// siteMapCLI / roomMapCLI are the confirmed mappings of the two readings
// (site pass + machine_room pass) for the CLI e2e workbooks.
const siteMapCLI = `{"sheets":[
	{"sheet":"局站资源表","action":"import","resourceType":"site","keyColumns":["site_id"],
	 "columnMapping":{"site_id":"局站ID","site_name":"局站名称","city":"地市"}}
]}`

const roomMapCLI = `{"sheets":[
	{"sheet":"机房资源表","action":"import","resourceType":"machine_room","keyColumns":["machine_room_id"],
	 "columnMapping":{"machine_room_id":"机房ID","machine_room_name":"机房名称","site_id":"局站ID"}}
]}`

func TestCLIChangesRefRetargetFlow(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopslab binary was not built (go tool unavailable)")
	}
	tmp := testsupport.TempDir(t)
	db := filepath.Join(tmp, "netops.db")
	siteMap := filepath.Join(tmp, "site.map.json")
	roomMap := filepath.Join(tmp, "room.map.json")
	if err := os.WriteFile(siteMap, []byte(siteMapCLI), 0o644); err != nil {
		t.Fatalf("write site mapping: %v", err)
	}
	if err := os.WriteFile(roomMap, []byte(roomMapCLI), 0o644); err != nil {
		t.Fatalf("write room mapping: %v", err)
	}

	// Imports: SITE-A, SITE-B (its own batch), room v1 (site_id=SITE-A),
	// room v2 (site_id=SITE-B — skip + one pending proposal).
	importCLI := func(file, mapFile string) map[string]any {
		t.Helper()
		summary, code := runCLIJSON(t, "import "+filepath.Base(file), "import", "--file", file, "--map", mapFile, "--db", db)
		if code != 0 {
			t.Fatalf("import %s: exit %d, want 0\nsummary: %v", file, code, summary)
		}
		return summary
	}
	siteAWB := writeSheetWorkbookCLI(t, tmp, "site-a.xlsx", "局站资源表",
		[]string{"局站ID", "局站名称", "地市"}, [][]string{{"SITE-A", "站A", "杭州"}})
	siteBWB := writeSheetWorkbookCLI(t, tmp, "site-b.xlsx", "局站资源表",
		[]string{"局站ID", "局站名称", "地市"}, [][]string{{"SITE-B", "站B", "宁波"}})
	roomV1 := writeSheetWorkbookCLI(t, tmp, "room-v1.xlsx", "机房资源表",
		[]string{"机房ID", "机房名称", "局站ID"}, [][]string{{"RM-001", "机房一", "SITE-A"}})
	roomV2 := writeSheetWorkbookCLI(t, tmp, "room-v2.xlsx", "机房资源表",
		[]string{"机房ID", "机房名称", "局站ID"}, [][]string{{"RM-001", "机房一", "SITE-B"}})
	importCLI(siteAWB, siteMap)
	siteBSummary := importCLI(siteBWB, siteMap)
	importCLI(roomV1, roomMap)
	importCLI(roomV2, roomMap)
	siteBBatch, _ := siteBSummary["batchId"].(string)
	if siteBBatch == "" {
		t.Fatalf("site-B import summary has no batchId: %v", siteBSummary)
	}

	// The pending proposal with the relationRetargeted PREVIEW.
	listing, code := runCLIJSON(t, "changes list", "changes", "--db", db, "list", "--status", "pending")
	if code != 0 {
		t.Fatalf("changes list: exit %d, want 0", code)
	}
	items, _ := listing["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("changes list = %d items, want 1: %v", len(items), listing)
	}
	item, _ := items[0].(map[string]any)
	if item["field"] != "site_id" || item["oldValue"] != "SITE-A" || item["newValue"] != "SITE-B" {
		t.Fatalf("change item = %v, want site_id SITE-A→SITE-B", item)
	}
	if retargeted, _ := item["relationRetargeted"].(bool); !retargeted {
		t.Fatalf("list preview relationRetargeted = %v, want true", item["relationRetargeted"])
	}
	changeID, _ := item["id"].(string)

	// The room resource id + a belongs_to endpoint-key probe via query CLI.
	resources, code := runCLIJSON(t, "query resources", "query", "--db", db, "resources", "--q", "RM-001")
	if code != 0 {
		t.Fatalf("query resources: exit %d, want 0", code)
	}
	resItems, _ := resources["items"].([]any)
	if len(resItems) != 1 {
		t.Fatalf("query resources RM-001 = %d items, want 1", len(resItems))
	}
	room, _ := resItems[0].(map[string]any)
	roomID, _ := room["id"].(string)
	belongsToKey := func() string {
		rels, code := runCLIJSON(t, "query relations", "query", "--db", db, "relations", "--id", roomID)
		if code != 0 {
			t.Fatalf("query relations: exit %d, want 0", code)
		}
		edges, _ := rels["edges"].([]any)
		for _, e := range edges {
			edge, _ := e.(map[string]any)
			if edge["relationType"] == "belongs_to" && edge["fromId"] == roomID {
				toID, _ := edge["toId"].(string)
				nodes, _ := rels["nodes"].([]any)
				for _, n := range nodes {
					node, _ := n.(map[string]any)
					if node["id"] == toID {
						key, _ := node["naturalKey"].(string)
						return key
					}
				}
			}
		}
		return ""
	}
	if got := belongsToKey(); got != "SITE-A" {
		t.Fatalf("belongs_to edge before apply points at %q, want SITE-A", got)
	}

	// Roll back the SITE-B batch after the proposal exists — the reachable
	// trigger of the ref-target guard (no relation references SITE-B; the
	// proposal belongs to the room's re-import batch). The rollback runs
	// through the real importer service on the same database (the engine
	// behind the HTTP DELETE /api/batches/{id} route).
	a, err := New(Config{DBPath: db})
	if err != nil {
		t.Fatalf("open app for rollback: %v", err)
	}
	if err := a.Importer.Rollback(context.Background(), siteBBatch); err != nil {
		t.Fatalf("rollback site-B batch: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("close rollback app: %v", err)
	}

	// Apply with the vanished target → exit 1 + ref_target_missing, and
	// zero changes (attribute keeps SITE-A, edge keeps SITE-A, proposal
	// still pending).
	stdout, code := runCLI(t, "changes", "--db", db, "apply", "--id", changeID)
	if code != 1 {
		t.Fatalf("apply with vanished target: exit = %d, want 1 (stdout: %s)", code, stdout)
	}
	var errOut map[string]any
	if err := json.Unmarshal([]byte(stdout), &errOut); err != nil {
		t.Fatalf("apply refusal: stdout is not a JSON error object: %v\n%s", err, stdout)
	}
	errBody, _ := errOut["error"].(map[string]any)
	if errBody["code"] != "ref_target_missing" {
		t.Fatalf("apply refusal error.code = %v, want ref_target_missing (stdout: %s)", errBody["code"], stdout)
	}
	resources, code = runCLIJSON(t, "query resources after refusal", "query", "--db", db, "resources", "--q", "RM-001")
	if code != 0 {
		t.Fatalf("query resources after refusal: exit %d, want 0", code)
	}
	resItems, _ = resources["items"].([]any)
	room, _ = resItems[0].(map[string]any)
	attrs, _ := room["attributes"].(map[string]any)
	if attrs["site_id"] != "SITE-A" {
		t.Fatalf("room after refused apply = %v, want site_id still SITE-A (zero changes)", attrs)
	}
	if got := belongsToKey(); got != "SITE-A" {
		t.Fatalf("edge after refused apply points at %q, want still SITE-A (zero changes)", got)
	}
	listing, code = runCLIJSON(t, "changes list after refusal", "changes", "--db", db, "list", "--status", "pending")
	if code != 0 {
		t.Fatalf("changes list after refusal: exit %d, want 0", code)
	}
	if total, _ := listing["total"].(float64); total != 1 {
		t.Fatalf("pending after refused apply = %v, want 1 (still pending)", listing["total"])
	}

	// Re-import the site and apply: the attribute AND the edge follow
	// SITE-B, relationRetargeted=true on the apply response.
	importCLI(siteBWB, siteMap)
	applied, code := runCLIJSON(t, "changes apply", "changes", "--db", db, "apply", "--id", changeID)
	if code != 0 {
		t.Fatalf("changes apply: exit %d, want 0", code)
	}
	if applied["status"] != "applied" || applied["field"] != "site_id" {
		t.Fatalf("applied item = %v, want applied site_id", applied)
	}
	if retargeted, _ := applied["relationRetargeted"].(bool); !retargeted {
		t.Fatalf("applied item relationRetargeted = %v, want true (the edge moved)", applied["relationRetargeted"])
	}
	resources, code = runCLIJSON(t, "query resources after apply", "query", "--db", db, "resources", "--q", "RM-001")
	if code != 0 {
		t.Fatalf("query resources after apply: exit %d, want 0", code)
	}
	resItems, _ = resources["items"].([]any)
	room, _ = resItems[0].(map[string]any)
	attrs, _ = room["attributes"].(map[string]any)
	if attrs["site_id"] != "SITE-B" {
		t.Fatalf("room after apply = %v, want site_id SITE-B", attrs)
	}
	if got := belongsToKey(); got != "SITE-B" {
		t.Fatalf("belongs_to edge after apply points at %q, want SITE-B", got)
	}

	// The re-apply keeps the plain conflict code.
	stdout, code = runCLI(t, "changes", "--db", db, "apply", "--id", changeID)
	if code != 1 {
		t.Fatalf("re-apply: exit = %d, want 1 (stdout: %s)", code, stdout)
	}
	if err := json.Unmarshal([]byte(stdout), &errOut); err != nil {
		t.Fatalf("re-apply: stdout is not a JSON error object: %v\n%s", err, stdout)
	}
	errBody, _ = errOut["error"].(map[string]any)
	if errBody["code"] != "conflict" {
		t.Fatalf("re-apply error.code = %v, want conflict", errBody["code"])
	}
}
