package app

// TestCLIRealDataImportChain is the v0.8.0 real-public-data acceptance test:
// the 31 committed workbooks under testdata/realdata/ (OSM sites + OpenCelliD
// base stations + real corridor cables + disclosed-simulated inventory
// details) import through the exact demo-real.sh path — analyze → extract
// suggestedMapping → import --map — in dependency order, with zero failed
// rows, the full cross-layer relation graph, and the business impact chain
// (optical cable down → customers/leased lines impacted) reachable via the
// HTTP API. The dataset is a committed deliverable; this test guards it
// against regenerating drift (counts pinned to the manifest).

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// realDataOrder is the ref-dependency import order (workbooks.py documents
// it; demo-real.sh uses the same list).
var realDataOrder = []string{
	"sites", "machine_rooms", "base_stations", "customers", "vpn_instances",
	"rings", "igp_domains", "devices", "ports", "boards", "racks",
	"odf", "odf_terminals", "ducts", "manholes", "poles",
	"optical_cables", "cable_segments", "splice_boxes", "fibers",
	"otn_ochs", "sdh_timeslots", "circuits", "ippools", "services",
	"leased_lines", "bbus", "rrus", "olts", "pon_ports", "onus",
}

// realDataExpected pins the totals the manifest records (workbooks.py).
var realDataExpected = map[string]int{
	"sites": 83, "machine_rooms": 83, "base_stations": 1198, "customers": 12,
	"vpn_instances": 3, "rings": 2, "igp_domains": 2, "devices": 30,
	"ports": 90, "boards": 60, "racks": 15, "odf": 15, "odf_terminals": 120,
	"ducts": 7, "manholes": 14, "poles": 7, "optical_cables": 7,
	"cable_segments": 23, "splice_boxes": 46, "fibers": 56, "otn_ochs": 14,
	"sdh_timeslots": 4, "circuits": 10, "ippools": 5, "services": 8,
	"leased_lines": 6, "bbus": 5, "rrus": 5, "olts": 5, "pon_ports": 20,
	"onus": 10,
}

func TestCLIRealDataImportChain(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopslab binary was not built (go tool unavailable)")
	}
	root := repoRoot(t)
	dataDir := filepath.Join(root, "testdata", "realdata")
	manifestPath := filepath.Join(dataDir, "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("testdata/realdata/manifest.json missing: %v (run scripts/realdata/{extract,workbooks}.py)", err)
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "netops.db")
	mapFile := filepath.Join(tmp, "map.json")

	totalImported, totalFailed := 0, 0
	for _, wb := range realDataOrder {
		file := filepath.Join(dataDir, wb+".xlsx")
		if _, err := os.Stat(file); err != nil {
			t.Fatalf("workbook %s.xlsx missing: %v", wb, err)
		}
		analysis, code := runCLIJSON(t, "analyze "+wb, "analyze", "--file", file)
		if code != 0 {
			t.Fatalf("analyze %s: exit %d", wb, code)
		}
		raw, err := json.Marshal(analysis["suggestedMapping"])
		if err != nil {
			t.Fatalf("marshal suggestedMapping %s: %v", wb, err)
		}
		if err := os.WriteFile(mapFile, raw, 0o644); err != nil {
			t.Fatalf("write mapping: %v", err)
		}
		imp, code := runCLIJSON(t, "import "+wb, "import", "--file", file, "--map", mapFile, "--db", db)
		if code != 0 {
			t.Fatalf("import %s: exit %d", wb, code)
		}
		sum, _ := imp["summary"].(map[string]any)
		imported, _ := sum["rowsImported"].(float64)
		failed, _ := sum["rowsFailed"].(float64)
		totalImported += int(imported)
		totalFailed += int(failed)
		if want := realDataExpected[wb]; int(imported) != want {
			t.Errorf("%s imported = %d, want %d (dataset drift: regenerate via scripts/realdata/)", wb, int(imported), want)
		}
	}
	if totalFailed != 0 {
		t.Errorf("total failed rows = %d, want 0", totalFailed)
	}
	if totalImported != 1965 {
		t.Errorf("total imported = %d, want 1965", totalImported)
	}

	// The relation graph built across layers (segment_of / fiber_of /
	// belongs_to / subscribed_by …) is the cross-layer query substrate.
	stats, code := runCLIJSON(t, "query stats", "query", "--db", db, "stats")
	if code != 0 {
		t.Fatalf("query stats: exit %d", code)
	}
	byType, _ := stats["byType"].(map[string]any)
	if byType["base_station"].(float64) != 1198 || byType["site"].(float64) != 83 {
		t.Errorf("stats byType base_station/site = %v/%v, want 1198/83", byType["base_station"], byType["site"])
	}
	byRel, _ := stats["byRelationType"].(map[string]any)
	for _, want := range []struct {
		rel   string
		count float64
	}{
		{"belongs_to", 83}, {"segment_of", 23}, {"fiber_of", 56},
		{"fiber_terminates", 56}, {"housed_in", 30}, {"installed_in", 60},
	} {
		if got, _ := byRel[want.rel].(float64); got != want.count {
			t.Errorf("relations %s = %v, want %v", want.rel, got, want.count)
		}
	}

	// Business impact chain over HTTP: the real Harbin–Daqing–Qiqihar cable
	// goes down → the impact closure reaches the business layer (customers,
	// leased lines, services) — the #16 acceptance property, now on the
	// real-data dataset.
	wsOut, code := runCLIJSON(t, "lab workspace", "lab", "workspace", "create",
		"--db", db, "--name", "realdata-e2e")
	if code != 0 {
		t.Fatalf("lab workspace create: exit %d", code)
	}
	counts, _ := wsOut["counts"].(map[string]any)
	if d, _ := counts["devices"].(float64); d != 30 {
		t.Errorf("workspace devices = %v, want 30", counts["devices"])
	}

	ts, killServe := realDataServe(t, db)
	defer killServe()

	// Resolve OC-HDQ (the Harbin–Daqing–Qiqihar trunk) and drive it down.
	ocID := realDataFindResourceID(t, ts, "OC-HDQ")
	realDataPost(t, ts, "/api/resources/"+ocID+"/run-state",
		`{"runState":"down","source":"manual"}`)
	impacted := realDataGet(t, ts, "/api/resources/"+ocID+"/impact?direction=up")
	nodeTypes := map[string]int{}
	items, _ := impacted["impacted"].([]any)
	for _, it := range items {
		node, _ := it.(map[string]any)
		n, _ := node["node"].(map[string]any)
		ty, _ := n["type"].(string)
		nodeTypes[ty]++
	}
	for _, want := range []string{"customer", "leased_line", "service", "circuit"} {
		if nodeTypes[want] == 0 {
			t.Errorf("impact up from OC-HDQ reaches no %s (got %v) — cross-layer chain broken", want, nodeTypes)
		}
	}
}

// realDataServe starts a real `serve` subprocess on an ephemeral port,
// waits for /api/health, and returns the base URL plus a kill func.
func realDataServe(t *testing.T, db string) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	base := "http://" + addr
	proc := exec.Command(e2eBin, "serve", "--db", db, "--addr", addr)
	if err := proc.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	for i := 0; i < 100; i++ {
		resp, err := http.Get(base + "/api/health")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return base, func() { _ = proc.Process.Kill() }
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = proc.Process.Kill()
	t.Fatalf("serve did not become healthy on %s", base)
	return base, func() {}
}

// realDataFindResourceID searches by type+q and returns the first item id.
func realDataFindResourceID(t *testing.T, base, q string) string {
	t.Helper()
	list := realDataGet(t, base, "/api/resources?type=optical_cable&q="+q)
	items, _ := list["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("resource search q=%s: no items", q)
	}
	first, _ := items[0].(map[string]any)
	id, _ := first["id"].(string)
	if id == "" {
		t.Fatalf("resource search q=%s: item without id: %v", q, first)
	}
	return id
}

// realDataGet performs GET and decodes the JSON body.
func realDataGet(t *testing.T, base, path string) map[string]any {
	t.Helper()
	resp, err := http.Get(base + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s: status %d", path, resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("GET %s: decode: %v", path, err)
	}
	return out
}

// realDataPost performs POST with a JSON body and expects 200.
func realDataPost(t *testing.T, base, path, body string) {
	t.Helper()
	resp, err := http.Post(base+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("POST %s: status %d", path, resp.StatusCode)
	}
}
