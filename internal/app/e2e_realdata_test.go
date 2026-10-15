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
        "github.com/mmqz/netops-lab/internal/testsupport"
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
// it; demo-real.sh uses the same list). W30: base_stations moved after
// devices (its uplink_device/serve_customer refs), boards before ports
// (mounted_on refs), protection_groups after circuits (protects refs).
var realDataOrder = []string{
        "sites", "machine_rooms", "customers", "vpn_instances",
        "rings", "igp_domains", "devices", "boards", "base_stations", "ports", "racks",
        "odf", "odf_terminals", "ducts", "manholes", "poles",
        "optical_cables", "cable_segments", "splice_boxes", "fibers",
        "otn_ochs", "sdh_timeslots", "circuits", "protection_groups", "ippools", "services",
        "leased_lines", "bbus", "rrus", "olts", "pon_ports", "onus",
}

// realDataExpected pins the totals the manifest records (workbooks.py).
// W30 (issues #40/#42/#45): circuits 10→32, protection_groups +4.
// W32 (issue #52): device placement unthrottled — 15 hardcoded sites →
// all 83 OSM stations (13 hubs ×3 + counties ×1~2) = 133 devices; ports/boards/
// racks/odf/odf_terminals scale with the device base; circuits 32→207 four
// families (83 adjacent + 60 long-haul + 52 prefecture mesh + 11 access).
var realDataExpected = map[string]int{
        "sites": 83, "machine_rooms": 83, "base_stations": 1198, "customers": 12,
        "vpn_instances": 3, "rings": 2, "igp_domains": 2, "devices": 133,
        "ports": 399, "boards": 266, "racks": 83, "odf": 83, "odf_terminals": 664,
        "ducts": 7, "manholes": 14, "poles": 7, "optical_cables": 7,
        "cable_segments": 23, "splice_boxes": 46, "fibers": 56, "otn_ochs": 14,
        "sdh_timeslots": 4, "circuits": 207, "protection_groups": 4, "ippools": 5,
        "services": 8, "leased_lines": 6, "bbus": 5, "rrus": 5, "olts": 5,
        "pon_ports": 20, "onus": 10,
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
        tmp := testsupport.TempDir(t)
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
        if totalImported != 3464 {
                t.Errorf("total imported = %d, want 3464", totalImported)
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
        // W30 pins (issues #40/#42/#45): the previously-missing edges are now
        // data-backed — serves/uplink_to/mounted_on/protects — and the circuit
        // families carry 32 links (workbench scale, #42).
        // W32 (#52): device-derived relation counts scale with 133 devices
        // (housed_in/installed_in/mounted_on/in_vpn/ring/igp) and 207 circuits
        // (a_end/z_end/carried_by); serves = 133 devices + 1198 stations.
        for _, want := range []struct {
                rel   string
                count float64
        }{
                {"belongs_to", 83}, {"segment_of", 23}, {"fiber_of", 56},
                {"fiber_terminates", 56}, {"housed_in", 133}, {"installed_in", 266},
                {"serves", 1331}, {"uplink_to", 1198}, {"mounted_on", 399}, {"in_vpn", 399},
                {"protects", 4}, {"a_end", 207}, {"z_end", 207}, {"carried_by", 207},
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
        if d, _ := counts["devices"].(float64); d != 133 {
                t.Errorf("workspace devices = %v, want 133", counts["devices"])
        }
        // Issue #49: links are now the PHYSICAL per-hop interconnections along
        // the 7 cable corridors' anchor chains. W32 (#52): devices now sit at
        // ALL 83 OSM stations, so every corridor waypoint is device-anchored
        // and the 7 corridors fold into 23 hops (4+3+4+4+3+2+3 — one hop per
        // segment; the intermediate collapse pattern of the 15-site era is
        // gone). The 207 business circuits stay in the unified resource graph
        // (traverse/impact) instead of device-direct arcs.
        if l, _ := counts["links"].(float64); l != 23 {
                t.Errorf("workspace links = %v, want 23 physical corridor hops (issue #49 × #52)", counts["links"])
        }
        // Issue #51 (W33): interfaces are now the REAL port resources
        // (399 ports attributed via port→board→device) plus the synthetic
        // spill (1: a junction device's real ports are exhausted by its 4
        // corridor hops) — the reverse-inference 2×links era is over.
        if i, _ := counts["interfaces"].(float64); i != 400 {
                t.Errorf("workspace interfaces = %v, want 400 (399 real ports + 1 synthetic spill)", counts["interfaces"])
        }
        // The layer overlay (issue #51): 1,286 nodes (1,198 base stations
        // + bbu/rru/olt/onu + rings/igp/vpn/pg/pools + customers/services/
        // leased lines + manholes/poles), 1,073 semantic edges, 17 source
        // resource types (acceptance F.1: ≥ 8, was 1).
        if n, _ := counts["layerNodes"].(float64); n != 1286 {
                t.Errorf("workspace layerNodes = %v, want 1286", counts["layerNodes"])
        }
        if l, _ := counts["layerLinks"].(float64); l != 1073 {
                t.Errorf("workspace layerLinks = %v, want 1073", counts["layerLinks"])
        }
        types, _ := counts["sourceTypes"].([]any)
        if len(types) < 8 {
                t.Errorf("sourceTypes = %v, want ≥8 distinct input types (issue #51 F.1)", types)
        }

        ts, killServe := realDataServe(t, db)
        defer killServe()

        // Issue #51: the topology payload carries the layer overlay — the
        // L4 base stations arrive aggregated (cluster nodes with member
        // counts), L3 logical + L6 business edges are present, and the
        // cluster drill-down endpoint pages the members.
        topo := realDataGet(t, ts, "/api/workspaces/"+wsOut["workspaceId"].(string)+"/topology")
        layers, _ := topo["layers"].(map[string]any)
        if layers == nil {
                t.Fatalf("topology payload lacks the layers section (issue #51)")
        }
        summary, _ := layers["summary"].(map[string]any)
        byLayer, _ := summary["byLayer"].(map[string]any)
        l4, _ := byLayer["L4"].(map[string]any)
        if l4 != nil {
                // OpenCelliD's HLJ coverage concentrates on the Harbin/Daqing
                // corridor (manifest discloses it): all 1,198 stations
                // uplink to 11 device sites — the honest crowd-sourced
                // shape, not a generator artifact.
                if clusters, _ := l4["clusters"].(float64); clusters != 11 {
                        t.Errorf("L4 clusters = %v, want 11 (OpenCelliD coverage shape)", clusters)
                }
                if clustered, _ := l4["clusteredNodes"].(float64); clustered != 1198 {
                        t.Errorf("L4 clusteredNodes = %v, want 1198 (all stations)", clustered)
                }
        } else {
                t.Errorf("layers summary lacks L4 (got %v)", byLayer)
        }
        l3, _ := byLayer["L3"].(map[string]any)
        if l3 == nil {
                t.Errorf("layers summary lacks L3 (logical circuits — got %v)", byLayer)
        } else if edges, _ := l3["edges"].(float64); edges < 200 {
                t.Errorf("L3 logical edges = %v, want ≥200 circuits (issue #52 families)", edges)
        }
        l6, _ := byLayer["L6"].(map[string]any)
        if l6 == nil {
                t.Errorf("layers summary lacks L6 (business layer — got %v)", byLayer)
        } else if nodes, _ := l6["nodes"].(float64); nodes != 26 {
                t.Errorf("L6 business nodes = %v, want 26 (12 customers + 8 services + 6 leased lines)", nodes)
        }
        // Cluster drill-down: page 1 of the first aggregate node's members.
        layerNodes, _ := layers["nodes"].([]any)
        var clusterAnchor string
        for _, raw := range layerNodes {
                n, _ := raw.(map[string]any)
                if kind, _ := n["kind"].(string); kind == "cluster" {
                        clusterAnchor, _ = n["anchorDeviceId"].(string)
                        if mc, _ := n["memberCount"].(float64); mc < 1 {
                                t.Errorf("cluster node memberCount = %v, want ≥1", mc)
                        }
                        break
                }
        }
        if clusterAnchor == "" {
                t.Fatalf("no cluster node in the layers payload (issue #51 aggregation)")
        }
        members := realDataGet(t, ts, "/api/workspaces/"+wsOut["workspaceId"].(string)+
                "/layer-members?cluster="+clusterAnchor+"&pageSize=5")
        if items, _ := members["items"].([]any); len(items) != 5 {
                t.Errorf("layer-members page returned %d items, want 5 (pageSize)", len(items))
        }
        if total, _ := members["total"].(float64); total < 1 {
                t.Errorf("layer-members total = %v, want ≥1", total)
        }

        // W30 (issue #45) acceptance: status coverage and the geo REAL columns
        // are visible through the API. The station fleet imports with lifecycle
        // status (在网 majority), and list items expose geoLat/geoLng (migration
        // v20) — the spatial substrate that used to be JSON-string-only.
        if st := realDataGet(t, ts, "/api/resources?type=base_station&status=%E5%9C%A8%E7%BD%91&pageSize=1"); st != nil {
                if total, _ := st["total"].(float64); total < 1100 {
                        t.Errorf("base_station status=在网 total = %v, want ≥1100 (issue #45 status coverage)", total)
                }
        }
        if list := realDataGet(t, ts, "/api/resources?type=base_station&pageSize=1"); list != nil {
                items, _ := list["items"].([]any)
                if len(items) == 0 {
                        t.Fatalf("base_station list: no items")
                }
                first, _ := items[0].(map[string]any)
                if first["geoLat"] == nil || first["geoLng"] == nil {
                        t.Errorf("base_station item lacks geoLat/geoLng (migration v20 columns, issue #45): %v", first)
                }
        }

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
