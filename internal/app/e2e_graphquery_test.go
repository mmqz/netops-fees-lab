package app

// Issue #41 CLI e2e: `netopslab query --db <path> traverse | impact |
// context | nearest` — the CLI twins of the v0.7.0 graph-query endpoints.
// Two properties are pinned:
//
//  1. Cross-channel equivalence: the CLI document is JSON-equal to the HTTP
//     document for the same resource and parameters (both channels share
//     the composition root's ONE store binding and ONE context assembly —
//     this test is the guard that the sharing never silently forks).
//
//  2. The §10 contract: stdout carries exactly one JSON object, exit 0;
//     unknown flags / bad values exit 2 with a usage error.
//
// The seeded database is the three committed workbooks (64 resources, 57
// relations) — enough graph for a meaningful up-walk from a circuit; the
// nearest channel needs coordinates, so it runs on the realdata station
// sheet when available (skipped otherwise: the classic workbooks carry no
// geo).

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/mmqz/netops-lab/internal/testsupport"
)

// TestCLIGraphQueryChannels drives traverse/impact/context through BOTH
// channels over the same seeded database and asserts document equality.
func TestCLIGraphQueryChannels(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopslab binary was not built (go tool unavailable)")
	}
	dir := testsupport.TempDir(t)
	db := dir + "/netops.db"

	for n, file := range []string{"devices.xlsx", "ippools.xlsx", "circuits.xlsx"} {
		importSuggestedCLI(t, db, repoRoot(t)+"/testdata/"+file, n+1)
	}

	// A circuit's id: resolved once through query resources (CKT-001).
	out, code := runCLIJSON(t, "query resources", "query", "--db", db, "resources", "--q", "CKT-001")
	if code != 0 {
		t.Fatalf("query resources: exit %d", code)
	}
	items, _ := out["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("no circuit CKT-001 in the seeded database")
	}
	first, _ := items[0].(map[string]any)
	cirID, _ := first["id"].(string)
	if cirID == "" {
		t.Fatalf("circuit item without id: %v", first)
	}

	// —— traverse: CLI vs HTTP, same parameters (direction=down from the
	// circuit reaches its a_end/z_end devices — the layer-delta rule: up
	// would climb toward services/customers, which the classic workbooks
	// do not carry). ——
	cliTraverse, code := runCLIJSON(t, "query traverse", "query", "--db", db,
		"traverse", "--id", cirID, "--direction", "down")
	if code != 0 {
		t.Fatalf("query traverse: exit %d", code)
	}
	ts, killServe := graphChannelServe(t, db)
	defer killServe()
	httpTraverse := graphChannelGet(t, ts, "/api/resources/"+cirID+"/traverse?direction=down")
	assertJSONEqual(t, "traverse", cliTraverse, httpTraverse)

	// The down-walk from a circuit must reach its two endpoint devices —
	// the document is not just well-formed, it is the right graph.
	nodes, _ := cliTraverse["nodes"].([]any)
	if len(nodes) < 3 {
		t.Errorf("traverse down from a circuit visited %d nodes, want ≥3 (circuit + 2 devices)", len(nodes))
	}

	// —— impact: CLI vs HTTP. No run_state is set, so impacted/candidates
	// are empty — which is exactly the audit §2.6 contract: [] not null. ——
	cliImpact, code := runCLIJSON(t, "query impact", "query", "--db", db, "impact", "--id", cirID)
	if code != 0 {
		t.Fatalf("query impact: exit %d", code)
	}
	httpImpact := graphChannelGet(t, ts, "/api/resources/"+cirID+"/impact")
	assertJSONEqual(t, "impact", cliImpact, httpImpact)
	if v, ok := cliImpact["impacted"].([]any); !ok || v == nil {
		t.Errorf("impact.impacted = %v, want a JSON array (audit §2.6: [] not null)", cliImpact["impacted"])
	}
	if v, ok := cliImpact["candidates"].([]any); !ok || v == nil {
		t.Errorf("impact.candidates = %v, want a JSON array (audit §2.6)", cliImpact["candidates"])
	}

	// —— context: CLI vs HTTP. The classic workbooks carry no metrics, so
	// metrics/neighbors are empty arrays — the §2.6 contract again. ——
	cliContext, code := runCLIJSON(t, "query context", "query", "--db", db, "context", "--id", cirID)
	if code != 0 {
		t.Fatalf("query context: exit %d", code)
	}
	httpContext := graphChannelGet(t, ts, "/api/resources/"+cirID+"/context")
	assertJSONEqual(t, "context", cliContext, httpContext)
	for _, field := range []string{"metrics", "recentAnomalies"} {
		if v, ok := cliContext[field].([]any); !ok || v == nil {
			t.Errorf("context.%s = %v, want a JSON array (audit §2.6)", field, cliContext[field])
		}
	}
	neighbors, _ := cliContext["neighbors"].(map[string]any)
	if neighbors == nil {
		t.Fatalf("context.neighbors missing: %v", cliContext["neighbors"])
	}
	if v, ok := neighbors["nodes"].([]any); !ok || v == nil {
		t.Errorf("context.neighbors.nodes = %v, want a JSON array", neighbors["nodes"])
	}

	// —— usage errors: the §10 discipline (exit 2, usage message). ——
	for _, bad := range [][]string{
		{"traverse"}, // missing --id
		{"traverse", "--id", cirID, "--direction", "x"},  // bad direction
		{"traverse", "--id", cirID, "--layers", "9"},     // bad layer
		{"impact", "--id", cirID, "--direction", "both"}, // impact has no both
		{"impact", "--id", cirID, "--max-depth", "9"},    // depth cap
		{"context", "--id", cirID, "--depth", "3"},       // depth 1|2 only
		{"context", "--id", cirID, "--metrics", "x"},     // latest|none only
		{"nearest"}, // missing --type
		{"nearest", "--type", "device", "--lat", "91", "--lng", "0"}, // lat range
		{"graph-query-does-not-exist"},                               // unknown subcommand
	} {
		if _, code := runCLIJSON(t, "bad flags", append([]string{"query", "--db", db}, bad...)...); code != 2 {
			t.Errorf("query %v: exit %d, want 2 (usage error)", bad, code)
		}
	}
}

// TestCLINearestRealGeo drives `query nearest` on the realdata station
// sheet (OpenCelliD coordinates in the v20 REAL columns, issue #45): the
// Harbin-center query must rank 哈尔滨市传输设备01's site… no — the nearest
// BASE STATIONS by haversine: the closest stations to the Harbin reference
// point, with geoLat/geoLng present on every item.
func TestCLINearestRealGeo(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopslab binary was not built (go tool unavailable)")
	}
	root := repoRoot(t)
	stations := root + "/testdata/realdata/base_stations.xlsx"
	if !fileExistsForTest(t, stations) {
		t.Skip("realdata station sheet not present (run scripts/realdata/workbooks.py)")
	}
	dir := testsupport.TempDir(t)
	db := dir + "/netops.db"
	// Ref prerequisites of the station sheet (uplink_device → device,
	// serve_customer → customer; devices → sites/machine_rooms/rings/igp):
	// the realdata import order's prefix, exactly as demo-real.sh runs it.
	rd := root + "/testdata/realdata/"
	for i, wb := range []string{"sites.xlsx", "machine_rooms.xlsx", "customers.xlsx",
		"vpn_instances.xlsx", "rings.xlsx", "igp_domains.xlsx", "devices.xlsx"} {
		importSuggestedCLI(t, db, rd+wb, i+1)
	}
	importSuggestedCLI(t, db, stations, 8)

	// Harbin city center (45.80, 126.53): the station sheet's 672 Harbin
	// stations must dominate the nearest-5.
	out, code := runCLIJSON(t, "query nearest", "query", "--db", db,
		"nearest", "--type", "base_station", "--lat", "45.80", "--lng", "126.53", "--limit", "5")
	if code != 0 {
		t.Fatalf("query nearest: exit %d", code)
	}
	items, _ := out["items"].([]any)
	if len(items) != 5 {
		t.Fatalf("nearest returned %d items, want 5", len(items))
	}
	for i, raw := range items {
		it, _ := raw.(map[string]any)
		if it == nil {
			t.Fatalf("nearest item %d is not an object", i)
		}
		if it["geoLat"] == nil || it["geoLng"] == nil {
			t.Errorf("nearest item %d lacks geoLat/geoLng (v20 columns): %v", i, it)
		}
		if ty, _ := it["type"].(string); ty != "base_station" {
			t.Errorf("nearest item %d type = %q, want base_station", i, ty)
		}
	}
	// The nearest station is within a few km of the query point: the
	// natural key is the OpenCelliD CGI, so assert the lat/lng deltas
	// instead of names.
	first, _ := items[0].(map[string]any)
	lat, _ := first["geoLat"].(float64)
	lng, _ := first["geoLng"].(float64)
	if d := lat - 45.80; d < -0.5 || d > 0.5 {
		t.Errorf("nearest station geoLat = %v (Δ=%v), want within ±0.5° of the query", lat, d)
	}
	if d := lng - 126.53; d < -0.5 || d > 0.5 {
		t.Errorf("nearest station geoLng = %v (Δ=%v), want within ±0.5° of the query", lng, d)
	}
}

// —— helpers ——

// graphChannelServe starts a serve subprocess and returns its base URL.
func graphChannelServe(t *testing.T, db string) (string, func()) {
	t.Helper()
	return realDataServe(t, db)
}

// graphChannelGet performs GET and decodes the JSON body.
func graphChannelGet(t *testing.T, base, path string) map[string]any {
	t.Helper()
	return realDataGet(t, base, path)
}

// assertJSONEqual compares two decoded documents by canonical
// re-marshalling (map key order is not part of the contract; sorted-key
// JSON is).
func assertJSONEqual(t *testing.T, channel string, got, want map[string]any) {
	t.Helper()
	g, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("%s: marshal CLI doc: %v", channel, err)
	}
	w, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("%s: marshal HTTP doc: %v", channel, err)
	}
	// Canonicalize both (json.Marshal of map[string]any is already
	// key-sorted).
	if string(g) != string(w) {
		t.Errorf("%s: CLI and HTTP documents differ:\n  CLI : %s\n  HTTP: %s", channel, clipFor(g), clipFor(w))
	}
}

func clipFor(b []byte) []byte {
	if len(b) > 600 {
		return b[:600]
	}
	return b
}

func fileExistsForTest(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	return err == nil
}
