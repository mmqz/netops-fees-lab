package sim

// Geo pipeline tests of the simulation engine: materialization parses the
// device resource geo attributes into VirtualDevice.GeoLat/GeoLng, the
// topology payload carries them as geoLat/geoLng (omitted when absent), the
// canonical workspace STATE HASH is unaffected by geo (red line: geo is
// presentation metadata, never part of the hash), and the delete→undo
// restore path preserves the position through the DeviceSnap capture.

import (
	"context"
	"encoding/json"
	"github.com/mmqz/netops-lab/internal/testsupport"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/importer"
	"github.com/mmqz/netops-lab/internal/repo"
	"github.com/xuri/excelize/v2"
)

// geoDeviceRow is one device row of the geo test workbooks.
type geoDeviceRow struct {
	code, name, status, combined string // combined = "lng,lat" Gaode order
}

// geoCircuitRow is one circuit row of the geo test workbooks.
type geoCircuitRow struct {
	code, aEnd, zEnd string
}

// writeGeoWorkbook writes the device sheet (+circuit sheet when circuits is
// non-empty) of the geo test fixture into dir and returns the path.
func writeGeoWorkbook(t *testing.T, dir, name string, devices []geoDeviceRow, circuits []geoCircuitRow) string {
	t.Helper()
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	if err := f.SetSheetName("Sheet1", "设备清单"); err != nil {
		t.Fatalf("SetSheetName: %v", err)
	}
	deviceHeaders := []string{"设备编码", "设备名称", "状态", "经纬度"}
	for c, h := range deviceHeaders {
		cell, err := excelize.CoordinatesToCellName(c+1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.SetCellStr("设备清单", cell, h); err != nil {
			t.Fatalf("SetCellStr(%s): %v", cell, err)
		}
	}
	for r, d := range devices {
		for c, v := range []string{d.code, d.name, d.status, d.combined} {
			cell, err := excelize.CoordinatesToCellName(c+1, r+2)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.SetCellStr("设备清单", cell, v); err != nil {
				t.Fatalf("SetCellStr(%s): %v", cell, err)
			}
		}
	}
	if len(cuitsheets(circuits)) > 0 {
		if _, err := f.NewSheet("电路"); err != nil {
			t.Fatalf("NewSheet(电路): %v", err)
		}
		circuitHeaders := []string{"电路编号", "A端设备", "Z端设备", "带宽Mbps", "状态"}
		for c, h := range circuitHeaders {
			cell, err := excelize.CoordinatesToCellName(c+1, 1)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.SetCellStr("电路", cell, h); err != nil {
				t.Fatalf("SetCellStr(%s): %v", cell, err)
			}
		}
		for r, c := range circuits {
			for i, v := range []string{c.code, c.aEnd, c.zEnd, "1000", "在网"} {
				cell, err := excelize.CoordinatesToCellName(i+1, r+2)
				if err != nil {
					t.Fatal(err)
				}
				if err := f.SetCellStr("电路", cell, v); err != nil {
					t.Fatalf("SetCellStr(%s): %v", cell, err)
				}
			}
		}
	}
	path := filepath.Join(dir, name)
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("SaveAs(%s): %v", path, err)
	}
	return path
}

// cuitsheets is a no-op placeholder keeping the sheet-creation guard simple
// (len() of the circuit slice decides whether the sheet is written).
func cuitsheets(circuits []geoCircuitRow) []geoCircuitRow { return circuits }

// importGeoWorkbook imports one geo fixture workbook (devices, and circuits
// when present — device workbook first so refs resolve) into db and returns
// the batch ids in import order.
func importGeoWorkbook(t *testing.T, db *repo.DB, devices []geoDeviceRow, circuits []geoCircuitRow) []string {
	t.Helper()
	ctx := context.Background()
	imp := importer.New(db)
	dir := testsupport.TempDir(t)
	var batchIDs []string
	files := []struct {
		path string
	}{
		{writeGeoWorkbook(t, dir, "geo-devices.xlsx", devices, nil)},
	}
	if len(circuits) > 0 {
		files = append(files, struct{ path string }{writeGeoWorkbook(t, dir, "geo-circuits.xlsx", devices, circuits)})
	}
	for i, f := range files {
		_ = i
		a, err := imp.Analyze(ctx, f.path)
		if err != nil {
			t.Fatalf("Analyze(%s): %v", f.path, err)
		}
		if _, err := imp.Execute(ctx, a.BatchID, a.SuggestedMapping); err != nil {
			t.Fatalf("Execute(%s): %v", f.path, err)
		}
		batchIDs = append(batchIDs, a.BatchID)
	}
	return batchIDs
}

// geoFixture is the canonical 3-device/2-circuit geo lab: 哈尔滨 → 大庆 →
// 加格达奇, combined 经纬度 column in Gaode (经度,纬度) order.
var geoFixture = struct {
	devices  []geoDeviceRow
	circuits []geoCircuitRow
}{
	devices: []geoDeviceRow{
		{"DEV-A", "枢纽楼A", "在网", "126.6,45.7"},
		{"DEV-B", "汇聚B", "在网", "125.1,46.5"},
		{"DEV-C", "汇聚C", "在网", "124.1,50.4"},
	},
	circuits: []geoCircuitRow{
		{"CKT-1", "DEV-A", "DEV-B"},
		{"CKT-2", "DEV-B", "DEV-C"},
	},
}

// TestMaterializeGeoFlow covers the full geo path: import (split) → resource
// attributes → materialize → VirtualDevice.GeoLat/GeoLng → topology nodes
// with geoLat/geoLng, omitted from the JSON when absent.
func TestMaterializeGeoFlow(t *testing.T) {
	svc, db := newEngine(t)
	batches := importGeoWorkbook(t, db, geoFixture.devices, geoFixture.circuits)
	ctx := context.Background()
	res, err := svc.Materialize(ctx, "geo-lab", "坐标为演示近似值", batches)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if res.Counts.Devices != 3 || res.Counts.Links != 2 || res.Counts.Interfaces != 4 {
		t.Fatalf("materialize counts = %+v, want 3 devices / 2 links / 4 interfaces", res.Counts)
	}

	topo := mustTopology(t, svc, res.WorkspaceID)
	want := map[string][2]float64{
		"DEV-A": {45.7, 126.6},
		"DEV-B": {46.5, 125.1},
		"DEV-C": {50.4, 124.1},
	}
	for _, n := range topo.Nodes {
		w, ok := want[n.Code]
		if !ok {
			t.Fatalf("unexpected node %s", n.Code)
		}
		if n.GeoLat == nil || n.GeoLng == nil || *n.GeoLat != w[0] || *n.GeoLng != w[1] {
			t.Errorf("node %s geo = %v/%v, want %v/%v", n.Code, n.GeoLat, n.GeoLng, w[0], w[1])
			continue
		}
	}

	// Repo-level round-trip: the position lives on the device row.
	d, err := db.VirtualDevices.GetByCode(ctx, res.WorkspaceID, "DEV-A")
	if err != nil {
		t.Fatalf("GetByCode(DEV-A): %v", err)
	}
	if d.GeoLat == nil || *d.GeoLat != 45.7 || d.GeoLng == nil || *d.GeoLng != 126.6 {
		t.Errorf("repo device DEV-A geo = %v/%v, want 45.7/126.6", d.GeoLat, d.GeoLng)
	}

	// Nodes WITHOUT a position omit the keys entirely (omitempty): import a
	// device set without geo columns and materialize a second workspace.
	noGeoDevices := []geoDeviceRow{
		{"DEV-X", "无坐标站", "在网", ""},
		{"DEV-Y", "无坐标站2", "在网", ""},
	}
	batches2 := importGeoWorkbook(t, db, noGeoDevices, []geoCircuitRow{{"CKT-9", "DEV-X", "DEV-Y"}})
	res2, err := svc.Materialize(ctx, "no-geo-lab", "", batches2)
	if err != nil {
		t.Fatalf("Materialize(no-geo): %v", err)
	}
	topo2 := mustTopology(t, svc, res2.WorkspaceID)
	for _, n := range topo2.Nodes {
		if n.GeoLat != nil || n.GeoLng != nil {
			t.Errorf("node %s without geo columns must have nil position, got %v/%v", n.Code, n.GeoLat, n.GeoLng)
		}
	}
	raw, err := json.Marshal(topo2.Nodes)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(raw) == "" || containsGeoKey(string(raw)) {
		t.Errorf("topology JSON of position-less nodes must not contain geoLat/geoLng keys: %s", raw)
	}
	rawGeo, err := json.Marshal(topo.Nodes)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !containsGeoKey(string(rawGeo)) {
		t.Errorf("topology JSON of positioned nodes must contain geoLat/geoLng keys: %s", rawGeo)
	}
}

// containsGeoKey reports whether the JSON text carries a geoLat/geoLng key
// (the `"geoLat":` probe is sufficient for the omitempty assertions here).
func containsGeoKey(s string) bool {
	return strings.Contains(s, `"geoLat":`) || strings.Contains(s, `"geoLng":`)
}

// TestWorkspaceHashIgnoresGeo is the RED-LINE regression: the canonical
// workspace state hash must be identical with and without device positions.
// Both layers are proven: (a) end to end — two fresh databases, identical
// device/circuit rows, one workbook with 经纬度 columns and one without,
// materialize both → identical hashes; (b) unit level — the same loaded
// simState with GeoLat/GeoLng set vs cleared yields the identical
// hashValue().
func TestWorkspaceHashIgnoresGeo(t *testing.T) {
	// (a) End to end: identical business data, geo columns present vs absent.
	svcGeo, dbGeo := newEngine(t)
	batchesGeo := importGeoWorkbook(t, dbGeo, geoFixture.devices, geoFixture.circuits)
	resGeo, err := svcGeo.Materialize(context.Background(), "geo", "", batchesGeo)
	if err != nil {
		t.Fatalf("Materialize(geo): %v", err)
	}

	svcPlain, dbPlain := newEngine(t)
	// Same rows but the combined column empty → no geo attributes at all.
	plainDevices := make([]geoDeviceRow, len(geoFixture.devices))
	for i, d := range geoFixture.devices {
		plainDevices[i] = geoDeviceRow{code: d.code, name: d.name, status: d.status}
	}
	batchesPlain := importGeoWorkbook(t, dbPlain, plainDevices, geoFixture.circuits)
	resPlain, err := svcPlain.Materialize(context.Background(), "plain", "", batchesPlain)
	if err != nil {
		t.Fatalf("Materialize(plain): %v", err)
	}
	if resGeo.WorkspaceHash != resPlain.WorkspaceHash {
		t.Fatalf("workspace hash WITH geo %s != hash WITHOUT geo %s — geo must never enter the state hash",
			resGeo.WorkspaceHash, resPlain.WorkspaceHash)
	}

	// The topology payloads DO differ (that is the point of geo) — but only
	// in the position fields: same node count, same codes.
	topoGeo := mustTopology(t, svcGeo, resGeo.WorkspaceID)
	topoPlain := mustTopology(t, svcPlain, resPlain.WorkspaceID)
	if len(topoGeo.Nodes) != len(topoPlain.Nodes) || len(topoGeo.Edges) != len(topoPlain.Edges) {
		t.Fatalf("topology shapes differ: %d/%d vs %d/%d", len(topoGeo.Nodes), len(topoGeo.Edges), len(topoPlain.Nodes), len(topoPlain.Edges))
	}
	for i := range topoGeo.Nodes {
		if topoGeo.Nodes[i].Code != topoPlain.Nodes[i].Code {
			t.Fatalf("node %d code mismatch: %s vs %s", i, topoGeo.Nodes[i].Code, topoPlain.Nodes[i].Code)
		}
	}

	// (b) Unit level: mutate the loaded state's geo in place — the hash
	// value must stay byte-identical.
	state, err := loadState(context.Background(), storeOfDB(dbGeo), resGeo.WorkspaceID)
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}
	hashBefore, err := state.hashValue()
	if err != nil {
		t.Fatalf("hashValue(before): %v", err)
	}
	for i := range state.devices {
		state.devices[i].Dev.GeoLat = nil
		state.devices[i].Dev.GeoLng = nil
	}
	hashAfter, err := state.hashValue()
	if err != nil {
		t.Fatalf("hashValue(after): %v", err)
	}
	if hashBefore != hashAfter {
		t.Fatalf("hashValue changed when geo was cleared: %s → %s — hashDevice must not include geo", hashBefore, hashAfter)
	}
	// And the raw hash JSON carries no geo keys at all.
	if containsGeoKey(hashBefore) {
		t.Errorf("canonical hash JSON contains geo keys: %s", hashBefore)
	}
}

// TestDeleteDeviceUndoPreservesGeo proves the delete→undo round-trip keeps
// the device position: delete_device captures the full VirtualDevice row
// (including geo) into the DeviceSnap of the restore_device inverse, and
// undo re-inserts it with the position intact. The state hash returns to
// the baseline exactly (geo never influenced it either way).
func TestDeleteDeviceUndoPreservesGeo(t *testing.T) {
	svc, db := newEngine(t)
	batches := importGeoWorkbook(t, db, geoFixture.devices, geoFixture.circuits)
	ctx := context.Background()
	res, err := svc.Materialize(ctx, "undo-geo", "", batches)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	ws := res.WorkspaceID
	hashBaseline := mustHash(t, svc, ws)

	// Delete DEV-A (has geo 45.7/126.6); its interface + link cascade.
	applyOK(t, svc, ws, []Op{{Op: domain.OpDeleteDevice, DeviceID: "vd-1"}})
	if _, err := db.VirtualDevices.GetByID(ctx, "vd-1"); err == nil {
		t.Fatalf("device vd-1 must be gone after delete_device")
	}
	if mustHash(t, svc, ws) == hashBaseline {
		t.Fatalf("the delete must have changed the state hash")
	}

	// The captured inverse carries the position (DeviceSnap.geoLat/geoLng).
	csList, _, err := db.ChangeSets.ListByWorkspace(ctx, ws, 1, 0)
	if err != nil || len(csList) == 0 {
		t.Fatalf("changesets: %v (%d)", err, len(csList))
	}
	rows, err := db.ChangeSets.GetOps(ctx, csList[0].ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("GetOps: %v (%d rows)", err, len(rows))
	}
	inv := decodeInverse(t, rows[0].InverseJSON)
	if inv.restoreDevice == nil {
		t.Fatalf("delete_device inverse = %q, want restore_device", rows[0].InverseJSON)
	}
	if inv.restoreDevice.Device.GeoLat == nil || *inv.restoreDevice.Device.GeoLat != 45.7 ||
		inv.restoreDevice.Device.GeoLng == nil || *inv.restoreDevice.Device.GeoLng != 126.6 {
		t.Fatalf("DeviceSnap geo = %v/%v, want 45.7/126.6 — the capture must keep the position",
			inv.restoreDevice.Device.GeoLat, inv.restoreDevice.Device.GeoLng)
	}
	// The snap JSON uses camelCase keys like every restore payload.
	var rawMap map[string]any
	if err := json.Unmarshal([]byte(rows[0].InverseJSON), &rawMap); err != nil {
		t.Fatalf("inverse json: %v", err)
	}
	dev, _ := rawMap["device"].(map[string]any)
	if dev == nil {
		t.Fatalf("restore_device json has no device object: %v", rawMap)
	}
	for _, key := range []string{"geoLat", "geoLng"} {
		if _, ok := dev[key]; !ok {
			t.Errorf("restore_device json device misses camelCase key %q: %v", key, dev)
		}
	}

	// Undo: the device (and its position) comes back; hash returns exactly.
	undoRes, err := svc.Undo(ctx, ws, "", false)
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if undoRes.WorkspaceHash != hashBaseline {
		t.Fatalf("post-undo hash %s != baseline %s", undoRes.WorkspaceHash, hashBaseline)
	}
	d, err := db.VirtualDevices.GetByID(ctx, "vd-1")
	if err != nil {
		t.Fatalf("device vd-1 not restored: %v", err)
	}
	if d.GeoLat == nil || *d.GeoLat != 45.7 || d.GeoLng == nil || *d.GeoLng != 126.6 {
		t.Fatalf("restored device geo = %v/%v, want 45.7/126.6", d.GeoLat, d.GeoLng)
	}

	// The restored device still shows its position in the topology payload.
	topo := mustTopology(t, svc, ws)
	node := findNode(t, topo, "DEV-A")
	if node.GeoLat == nil || *node.GeoLat != 45.7 || node.GeoLng == nil || *node.GeoLng != 126.6 {
		t.Fatalf("topology node DEV-A after undo = %v/%v, want 45.7/126.6", node.GeoLat, node.GeoLng)
	}
}
