package app

// TestCLIExportRoundTrip is the W12-D core acceptance test (回源幂等): a
// database seeded with EVERY resource type (single, composite and
// conditional/degraded natural keys; ref columns and their derived
// relations) AND with attribute state that only became current through a
// W005 attribute-change apply and a W006 rename apply is exported via the
// real CLI (`netopslab export`), the exported workbook is re-imported into
// a FRESH database with nothing but its suggested mapping, and the two
// databases are compared for equivalence: identical resource sets (type,
// natural key, name, status, attributes) and identical relation sets
// (type + both endpoint keys — the relations are re-DERIVED from the ref
// columns, the ids are irrelevant by construction).
//
// TestCLIExportUsage pins the usage contract (exit 2 on a missing --out,
// an unknown --types value and a positional argument) and the empty-database
// export (exit 0, zero rows, the placeholder-only workbook).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/mmqz/netops-lab/internal/repo"
)

// importSuggestedCLI runs the CLI's own §10 flow for one file: offline
// analyze → write the suggested mapping to <tmp>/map-<n>.json → import.
// It returns the import summary JSON.
func importSuggestedCLI(t *testing.T, db, file string, n int) map[string]any {
	t.Helper()
	analysis, code := runCLIJSON(t, "analyze "+filepath.Base(file), "analyze", "--file", file)
	if code != 0 {
		t.Fatalf("analyze %s: exit %d, want 0", file, code)
	}
	suggested, err := json.Marshal(analysis["suggestedMapping"])
	if err != nil || string(suggested) == "null" {
		t.Fatalf("analyze %s: suggestedMapping unusable: %v", file, err)
	}
	mapFile := filepath.Join(filepath.Dir(db), fmt.Sprintf("map-%d.json", n))
	if err := os.WriteFile(mapFile, suggested, 0o644); err != nil {
		t.Fatalf("write mapping: %v", err)
	}
	summary, code := runCLIJSON(t, "import "+filepath.Base(file), "import", "--file", file, "--map", mapFile, "--db", db)
	if code != 0 {
		t.Fatalf("import %s: exit %d, want 0\nsummary: %v", file, code, summary)
	}
	return summary
}

// importMappedCLI imports one file with a fixed mapping JSON string.
func importMappedCLI(t *testing.T, db, file, mapping, label string) map[string]any {
	t.Helper()
	mapFile := filepath.Join(filepath.Dir(db), label+".map.json")
	if err := os.WriteFile(mapFile, []byte(mapping), 0o644); err != nil {
		t.Fatalf("write mapping %s: %v", label, err)
	}
	summary, code := runCLIJSON(t, "import "+label, "import", "--file", file, "--map", mapFile, "--db", db)
	if code != 0 {
		t.Fatalf("import %s: exit %d, want 0\nsummary: %v", label, code, summary)
	}
	return summary
}

// machineRoomMapJSON is the second reading of the 机房局站 sample sheet:
// the machine_room pass (the site pass used the suggested mapping).
const machineRoomMapJSON = `{"sheets":[
	{"sheet":"机房局站表","action":"import","resourceType":"machine_room","keyColumns":["machine_room_id"],
	 "columnMapping":{"machine_room_id":"机房ID","machine_room_name":"机房名称","site_id":"局站ID","city":"地市","district":"区县","device_name":"设备名称","ip_address":"IP地址"}}
]}`

// portSampleMapJSON imports the port sample's "Sheet1" (no type hint in
// the name → the suggested mapping skips it; the header-A columns are
// mapped by hand, mirroring importer_test's portMapping).
const portSampleMapJSON = `{"sheets":[
	{"sheet":"Sheet1","action":"import","resourceType":"port","keyColumns":["device_name","port_name"],
	 "columnMapping":{"device_name":"设备名称","port_name":"端口","device_ip":"设备ip","port_desc":"端口描述","port_type":"端口类型","bandwidth_mbps":"端口带宽","city":"地市","vendor":"厂家","model":"型号","record_date":"日期"}}
]}`

// (deviceMapJSON of e2e_changes_test.go is the confirmed mapping of the
// 设备清单 one-row workbook; the W005 seed below reuses it.)

// writeOneRowDeviceWorkbook writes the DEV-001 workbook with the given
// mgmt_ip (the testdata row's other values kept identical, so the re-import
// records exactly ONE attribute diff).
func writeOneRowDeviceWorkbook(t *testing.T, dir, file, mgmtIP string) string {
	t.Helper()
	f := excelize.NewFile()
	if err := f.SetSheetName("Sheet1", "设备清单"); err != nil {
		t.Fatalf("rename sheet: %v", err)
	}
	rows := [][]string{
		{"设备编码", "设备名称", "城市", "厂商", "型号", "管理IP", "状态"},
		{"DEV-001", "杭州-核心路由器-001", "杭州", "华为", "NE40E", mgmtIP, "在网"},
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
	path := filepath.Join(dir, file)
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("save workbook: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close workbook: %v", err)
	}
	return path
}

// resourceSignature is the equivalence key of one resource: everything the
// export/import round trip must preserve (ids, batch ids and timestamps
// are deliberately NOT compared — they are database internals).
type resourceSignature struct {
	Type       string
	NaturalKey string
	Name       string
	Status     string
	Attributes map[string]string
}

// relationSignature is the equivalence key of one relation: its type and
// both endpoint resource keys (the ids differ between the databases; the
// from/to resources are resolved by (type, natural key)).
type relationSignature struct {
	Type    string
	FromKey string
	ToKey   string
}

// dbSignatures loads the resource and relation signatures of a database.
func dbSignatures(t *testing.T, dbPath string) ([]resourceSignature, []relationSignature) {
	t.Helper()
	db, err := repo.Open(dbPath)
	if err != nil {
		t.Fatalf("repo.Open %s: %v", dbPath, err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()

	resources, _, err := db.Resources.Search(ctx, repo.ResourceSearchFilter{})
	if err != nil {
		t.Fatalf("load resources of %s: %v", dbPath, err)
	}
	resSigs := make([]resourceSignature, len(resources))
	keyByID := make(map[string]string, len(resources))
	for i, r := range resources {
		attrs := r.Attributes
		if attrs == nil {
			attrs = map[string]string{}
		}
		resSigs[i] = resourceSignature{
			Type:       r.Type.String(),
			NaturalKey: r.NaturalKey,
			Name:       r.Name,
			Status:     r.Status,
			Attributes: attrs,
		}
		keyByID[r.ID] = r.Type.String() + "\x00" + r.NaturalKey
	}

	relations, err := db.Relations.ListAll(ctx)
	if err != nil {
		t.Fatalf("load relations of %s: %v", dbPath, err)
	}
	relSigs := make([]relationSignature, len(relations))
	for i, rel := range relations {
		relSigs[i] = relationSignature{
			Type:    rel.Type.String(),
			FromKey: keyByID[rel.FromResourceID],
			ToKey:   keyByID[rel.ToResourceID],
		}
	}
	sort.Slice(relSigs, func(i, j int) bool {
		return relSigs[i].Type+"\x00"+relSigs[i].FromKey+"\x00"+relSigs[i].ToKey <
			relSigs[j].Type+"\x00"+relSigs[j].FromKey+"\x00"+relSigs[j].ToKey
	})
	return resSigs, relSigs
}

func TestCLIExportRoundTrip(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopslab binary was not built (go tool unavailable)")
	}
	root := repoRoot(t)
	tmp := t.TempDir()
	db1 := filepath.Join(tmp, "seed.db")
	samples := filepath.Join(root, "samples")

	// ---- seed: every resource type ----
	// testdata trio (device/ip_pool/circuit — single keys, ref columns,
	// derived relations). Pinned row outcomes from TestCLIEndToEnd.
	importSuggestedCLI(t, db1, filepath.Join(root, "testdata", "devices.xlsx"), 1)
	importSuggestedCLI(t, db1, filepath.Join(root, "testdata", "ippools.xlsx"), 2)
	importSuggestedCLI(t, db1, filepath.Join(root, "testdata", "circuits.xlsx"), 3)
	// base_station sample: BOTH sheets import as base_station — the bare
	// name sheet yields degraded keys (bare name) and the 含地市 sheet
	// yields city|name degraded keys, plus one PRIMARY (code) key row.
	importSuggestedCLI(t, db1, filepath.Join(samples, "telecom-base-station.xlsx"), 4)
	// site/machine-room sample, double reading: first the suggested
	// (site) pass, then the machine_room pass with the fixed mapping —
	// the rooms' 局站ID refs resolve against the sites of batch one.
	importSuggestedCLI(t, db1, filepath.Join(samples, "telecom-site-machine-room.xlsx"), 5)
	importMappedCLI(t, db1, filepath.Join(samples, "telecom-site-machine-room.xlsx"), machineRoomMapJSON, "machine-room")
	// port sample (composite key device_name|port_name, Sheet1 needs the
	// fixed mapping) and the PTP script channel (ptp_port).
	importMappedCLI(t, db1, filepath.Join(samples, "telecom-port-multi-header.xlsx"), portSampleMapJSON, "port")
	importSuggestedCLI(t, db1, filepath.Join(samples, "ptp-config-script.txt"), 6)

	// ---- W005: an attribute change applied by a human ----
	// DEV-001 re-imported with a changed mgmt_ip records one pending
	// change; the apply makes the new value the CURRENT state — the
	// export must carry the applied value, not any source workbook's.
	diffWB := writeOneRowDeviceWorkbook(t, tmp, "dev-diff.xlsx", "10.10.1.99")
	importMappedCLI(t, db1, diffWB, deviceMapJSON, "device-diff")
	listing, code := runCLIJSON(t, "changes list", "changes", "--db", db1, "list", "--status", "pending")
	if code != 0 {
		t.Fatalf("changes list: exit %d, want 0", code)
	}
	items, _ := listing["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("pending changes = %d, want 1 (DEV-001 mgmt_ip): %v", len(items), listing)
	}
	item, _ := items[0].(map[string]any)
	changeID, _ := item["id"].(string)
	if _, code := runCLIJSON(t, "changes apply", "changes", "--db", db1, "apply", "--id", changeID); code != 0 {
		t.Fatalf("changes apply: exit %d, want 0", code)
	}

	// ---- W006: a rename applied by a human ----
	// 实验站甲 imported, then the renamed 实验站乙 (same attributes) →
	// pending proposal → apply: the identity keeper takes the NEW key and
	// the key-component attribute is synced. The export must carry the
	// post-apply state (new degraded key, synced name attribute).
	stationA := writeStationWorkbookCLI(t, tmp, "实验站甲", "station-a.xlsx", "172.7.90.1/30")
	stationB := writeStationWorkbookCLI(t, tmp, "实验站乙", "station-b.xlsx", "172.7.90.1/30")
	importMappedCLI(t, db1, stationA, stationMapJSON, "station-a")
	importMappedCLI(t, db1, stationB, stationMapJSON, "station-b")
	rlist, code := runCLIJSON(t, "renames list", "renames", "--db", db1, "list", "--status", "pending")
	if code != 0 {
		t.Fatalf("renames list: exit %d, want 0", code)
	}
	ritems, _ := rlist["items"].([]any)
	if len(ritems) != 1 {
		t.Fatalf("pending renames = %d, want 1 (实验站甲→实验站乙): %v", len(ritems), rlist)
	}
	ritem, _ := ritems[0].(map[string]any)
	renameID, _ := ritem["id"].(string)
	if _, code := runCLIJSON(t, "renames apply", "renames", "--db", db1, "apply", "--id", renameID); code != 0 {
		t.Fatalf("renames apply: exit %d, want 0", code)
	}

	// ---- export: JSON contract + per-type counts ----
	exportFile := filepath.Join(tmp, "netops-resources.xlsx")
	exported, code := runCLIJSON(t, "export", "export", "--db", db1, "--out", exportFile)
	if code != 0 {
		t.Fatalf("export: exit %d, want 0", code)
	}
	if got, _ := exported["file"].(string); got != exportFile {
		t.Errorf("export: file = %q, want %q", got, exportFile)
	}
	totalRows := int(exported["totalRows"].(float64))
	types, _ := exported["types"].(map[string]any)
	if totalRows == 0 || len(types) == 0 {
		t.Fatalf("export: totalRows/types = %v/%v, want the full inventory", totalRows, types)
	}
	// Every exported type count must equal the source database's count.
	seedRes, _ := dbSignatures(t, db1)
	counts := map[string]int{}
	for _, r := range seedRes {
		counts[r.Type]++
	}
	if len(types) != len(counts) {
		t.Errorf("export: %d types (%v), want %d (%v)", len(types), types, len(counts), counts)
	}
	for typ, n := range counts {
		if got := int(types[typ].(float64)); got != n {
			t.Errorf("export: types[%s] = %d, want %d", typ, got, n)
		}
	}
	if totalRows != len(seedRes) {
		t.Errorf("export: totalRows = %d, want %d (resource count)", totalRows, len(seedRes))
	}

	// ---- round trip: analyze the export, import it into a FRESH db ----
	db2 := filepath.Join(tmp, "roundtrip.db")
	analysis, code := runCLIJSON(t, "analyze export", "analyze", "--file", exportFile)
	if code != 0 {
		t.Fatalf("analyze export: exit %d, want 0", code)
	}
	// Every sheet of the export must be suggested for import with the
	// right type, in mapping.Specs order.
	wantTypes := []string{"device", "ip_pool", "circuit", "port", "base_station", "site", "machine_room", "ptp_port"}
	sheets, _ := analysis["sheets"].([]any)
	gotTypes := []string{}
	for _, s := range sheets {
		sm, _ := s.(map[string]any)
		if sm["suggestedType"] != nil {
			gotTypes = append(gotTypes, sm["suggestedType"].(string))
		}
	}
	if len(gotTypes) != len(counts) {
		t.Fatalf("analyze export: %d import-suggested sheets (%v), want %d", len(gotTypes), gotTypes, len(counts))
	}
	// The suggested types (in sheet order) are the Specs-ordered subset of
	// the seeded types.
	wantSubset := []string{}
	for _, typ := range wantTypes {
		if _, ok := counts[typ]; ok {
			wantSubset = append(wantSubset, typ)
		}
	}
	for i, typ := range wantSubset {
		if gotTypes[i] != typ {
			t.Errorf("analyze export: sheet %d suggested type = %q, want %q (Specs order)", i, gotTypes[i], typ)
		}
	}
	suggested, err := json.Marshal(analysis["suggestedMapping"])
	if err != nil || string(suggested) == "null" {
		t.Fatalf("analyze export: suggestedMapping unusable: %v", err)
	}
	mapFile := filepath.Join(tmp, "roundtrip.map.json")
	if err := os.WriteFile(mapFile, suggested, 0o644); err != nil {
		t.Fatalf("write round-trip mapping: %v", err)
	}
	summary, code := runCLIJSON(t, "import export", "import", "--file", exportFile, "--map", mapFile, "--db", db2)
	if code != 0 {
		t.Fatalf("import export: exit %d, want 0\nsummary: %v", code, summary)
	}
	sm, _ := summary["summary"].(map[string]any)
	if got := int(sm["rowsImported"].(float64)); got != totalRows {
		t.Errorf("round-trip import: rowsImported = %d, want %d (every exported row imports)", got, totalRows)
	}
	if got := int(sm["rowsFailed"].(float64)); got != 0 {
		t.Errorf("round-trip import: rowsFailed = %d, want 0", got)
	}
	if got := int(sm["rowsSkipped"].(float64)); got != 0 {
		t.Errorf("round-trip import: rowsSkipped = %d, want 0 (fresh database)", got)
	}

	// ---- equivalence: resource sets and relation sets ----
	roundRes, roundRel := dbSignatures(t, db2)
	_, seedRel := dbSignatures(t, db1)
	if len(roundRes) != len(seedRes) {
		t.Fatalf("round-trip resources = %d, want %d", len(roundRes), len(seedRes))
	}
	for i := range seedRes {
		a, b := seedRes[i], roundRes[i]
		if a.Type != b.Type || a.NaturalKey != b.NaturalKey || a.Name != b.Name || a.Status != b.Status {
			t.Errorf("resource %d mismatch: seed %s/%s/%s/%s vs round-trip %s/%s/%s/%s",
				i, a.Type, a.NaturalKey, a.Name, a.Status, b.Type, b.NaturalKey, b.Name, b.Status)
		}
		if len(a.Attributes) != len(b.Attributes) {
			t.Errorf("resource %s/%s: attribute counts %d vs %d\nseed: %v\nround: %v",
				a.Type, a.NaturalKey, len(a.Attributes), len(b.Attributes), a.Attributes, b.Attributes)
			continue
		}
		for k, v := range a.Attributes {
			if b.Attributes[k] != v {
				t.Errorf("resource %s/%s: attribute %s = %q, want %q", a.Type, a.NaturalKey, k, b.Attributes[k], v)
			}
		}
	}
	if len(roundRel) != len(seedRel) {
		t.Fatalf("round-trip relations = %d, want %d\nseed: %v\nround: %v", len(roundRel), len(seedRel), seedRel, roundRel)
	}
	for i := range seedRel {
		if seedRel[i] != roundRel[i] {
			t.Errorf("relation %d mismatch: seed %+v vs round-trip %+v (ref columns must re-derive the same edges)",
				i, seedRel[i], roundRel[i])
		}
	}

	// The applied W005 value is the exported (and round-tripped) value.
	for _, r := range roundRes {
		if r.Type == "device" && r.NaturalKey == "DEV-001" {
			if r.Attributes["mgmt_ip"] != "10.10.1.99" {
				t.Errorf("DEV-001 mgmt_ip after round trip = %q, want the W005-applied 10.10.1.99 (current state, not the source workbook's 10.10.1.1)", r.Attributes["mgmt_ip"])
			}
		}
	}
	// The applied W006 rename survived: the station's key is the NEW name.
	found := false
	for _, r := range roundRes {
		if r.Type == "base_station" && r.NaturalKey == "实验站乙" {
			found = true
			if r.Attributes["base_station_name"] != "实验站乙" {
				t.Errorf("实验站乙 base_station_name = %q, want the synced key-component value", r.Attributes["base_station_name"])
			}
		}
	}
	if !found {
		t.Error("the W006-applied station 实验站乙 is missing after the round trip (identity merge state must export)")
	}
}

func TestCLIExportUsage(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopslab binary was not built (go tool unavailable)")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "netops.db")
	out := filepath.Join(tmp, "export.xlsx")

	// Missing --out: usage error, exit 2, JSON error object on stdout.
	stdout, code := runCLI(t, "export", "--db", db)
	if code != 2 {
		t.Errorf("export without --out: exit %d, want 2", code)
	}
	var e map[string]any
	if err := json.Unmarshal([]byte(stdout), &e); err != nil {
		t.Fatalf("export without --out: stdout is not JSON: %s", stdout)
	}
	if errObj, _ := e["error"].(map[string]any); errObj["code"] != "usage_error" {
		t.Errorf("export without --out: error.code = %v, want usage_error", errObj["code"])
	}

	// Unknown --types value: usage error naming the value.
	stdout, code = runCLI(t, "export", "--db", db, "--out", out, "--types", "device,bogus")
	if code != 2 {
		t.Errorf("export --types device,bogus: exit %d, want 2", code)
	}
	if !strings.Contains(stdout, "bogus") {
		t.Errorf("export --types device,bogus: message must name the offending type: %s", stdout)
	}

	// Positional argument: usage error.
	if _, code = runCLI(t, "export", "--db", db, "--out", out, "stray"); code != 2 {
		t.Errorf("export with positional arg: exit %d, want 2", code)
	}

	// Empty database: SUCCESS with zero rows — the placeholder-only
	// workbook (the agreed empty-export shape).
	res, code := runCLIJSON(t, "export empty", "export", "--db", db, "--out", out)
	if code != 0 {
		t.Fatalf("export empty db: exit %d, want 0", code)
	}
	if got, _ := res["totalRows"].(float64); got != 0 {
		t.Errorf("export empty db: totalRows = %v, want 0", res["totalRows"])
	}
	if types, _ := res["types"].(map[string]any); len(types) != 0 {
		t.Errorf("export empty db: types = %v, want empty", types)
	}
	f, err := excelize.OpenFile(out)
	if err != nil {
		t.Fatalf("open empty export: %v", err)
	}
	if got := f.GetSheetList(); len(got) != 1 || got[0] != "资源导出" {
		t.Errorf("empty export sheets = %v, want the placeholder [资源导出]", got)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close workbook: %v", err)
	}
}
