package sim

// Issue #49 — physical-layer-first link materialization: cable corridor
// anchor chains fold into per-hop device links; business circuits stop
// being drawn as end-to-end device-direct arcs when corridors exist.

import (
	"context"
	"strconv"

	"github.com/google/uuid"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// geoSpec is one station: name, coordinates, and whether devices live
// there. Harbin → Zhaodong (no devices) → Daqing mirrors the real
// TRK-HDQ corridor shape at miniature scale.
type geoSpec struct {
	name        string
	lng, lat    float64
	withDevices bool
}

// insertRes is a thin wrapper over db.Resources.Insert for the fixture
// (the importer generates uuid resource ids; the fixture mirrors that).
func insertRes(t *testing.T, db *repo.DB, res domain.Resource) domain.Resource {
	t.Helper()
	res.ID = uuid.NewString()
	res.FirstBatchID = "b1"
	res.CreatedAt = time.Now()
	res.UpdatedAt = res.CreatedAt
	if err := db.Resources.Insert(context.Background(), res); err != nil {
		t.Fatalf("insert resource %s: %v", res.NaturalKey, err)
	}
	return res
}

// TestMaterializePhysicalCorridorHops pins the corridor folding: OC-A
// runs Harbin → Zhaodong (no devices) → Daqing, so its two segments
// collapse into ONE hop DEV-HARBIN-01 ↔ DEV-DAQING-01; OC-B runs
// Daqing → Qiqihar directly. The circuit CIR-100 (Harbin-02 ↔
// Qiqihar-02) must NOT produce a link — physical corridors take over.
func TestMaterializePhysicalCorridorHops(t *testing.T) {
	svc, db := newEngine(t)
	ctx := context.Background()

	// The completed batch that scopes every fixture resource.
	finished := time.Now()
	if err := db.Batches.Create(ctx, domain.ImportBatch{
		ID: "b1", WorkbookName: "corridors.xlsx", WorkbookSHA256: "sha1",
		Status: domain.BatchStatusCompleted, Mapping: "{}", RuleVersion: "v1",
		StartedAt: finished, FinishedAt: &finished,
	}); err != nil {
		t.Fatalf("create batch: %v", err)
	}

	// Devices at three stations (-01/-02 each); Zhaodong has none.
	stations := []geoSpec{
		{"HARBIN", 126.53, 45.80, true},
		{"ZHAODONG", 125.96, 46.06, false},
		{"DAQING", 125.10, 46.59, true},
		{"QIQIHAR", 123.95, 47.35, true},
	}
	deviceOf := map[string]domain.Resource{} // station → -01 resource
	for _, st := range stations {
		if !st.withDevices {
			continue
		}
		for _, suffix := range []string{"01", "02"} {
			code := "DEV-" + st.name + "-" + suffix
			insertRes(t, db, domain.Resource{
				Type: domain.ResourceTypeDevice, NaturalKey: code, Name: code,
				Status: "在网",
				Attributes: map[string]string{
					"device_name": code,
					"geo_lat":     ftoa(st.lat),
					"geo_lng":     ftoa(st.lng),
				},
			})
		}
		res, _, err := db.Resources.Search(ctx, repo.ResourceSearchFilter{Type: domain.ResourceTypeDevice})
		if err != nil {
			t.Fatalf("search devices: %v", err)
		}
		for _, r := range res {
			if r.NaturalKey == "DEV-"+st.name+"-01" {
				deviceOf[st.name] = r
			}
		}
	}

	// Optical cables OC-A (Harbin→Daqing via Zhaodong) and OC-B
	// (Daqing→Qiqihar).
	cableA := insertRes(t, db, domain.Resource{
		Type: domain.ResourceTypeOpticalCable, NaturalKey: "OC-A", Name: "哈大走廊",
		Status: "在网", Attributes: map[string]string{"cable_name": "哈大走廊"},
	})
	cableB := insertRes(t, db, domain.Resource{
		Type: domain.ResourceTypeOpticalCable, NaturalKey: "OC-B", Name: "大齐走廊",
		Status: "在网", Attributes: map[string]string{"cable_name": "大齐走廊"},
	})

	// Splice boxes anchor the corridor: segment endpoints at each station
	// (Zhaodong's boxes exist and carry geo, but no device resolves there —
	// they collapse into the nearest station's device chain run).
	spliceBoxes := []struct {
		code string
		st   geoSpec
	}{
		{"SB-A-01-A", stations[0]}, {"SB-A-01-Z", stations[1]}, // seg1 Harbin→Zhaodong
		{"SB-A-02-A", stations[1]}, {"SB-A-02-Z", stations[2]}, // seg2 Zhaodong→Daqing
		{"SB-B-01-A", stations[2]}, {"SB-B-01-Z", stations[3]}, // OC-B seg1 Daqing→Qiqihar
	}
	for _, sb := range spliceBoxes {
		insertRes(t, db, domain.Resource{
			Type: domain.ResourceTypeSpliceBox, NaturalKey: sb.code, Name: sb.code,
			Status: "在网",
			Attributes: map[string]string{
				"geo_lat": ftoa(sb.st.lat),
				"geo_lng": ftoa(sb.st.lng),
			},
		})
	}

	// Cable segments (the corridor chain, ordered by index).
	segs := []struct {
		code, cable string
		idx         string
		aBox, zBox  string
	}{
		{"SEG-A-01", "OC-A", "1", "SB-A-01-A", "SB-A-01-Z"},
		{"SEG-A-02", "OC-A", "2", "SB-A-02-A", "SB-A-02-Z"},
		{"SEG-B-01", "OC-B", "1", "SB-B-01-A", "SB-B-01-Z"},
	}
	for _, sg := range segs {
		insertRes(t, db, domain.Resource{
			Type: domain.ResourceTypeCableSegment, NaturalKey: sg.code, Name: sg.code,
			Status: "在网",
			Attributes: map[string]string{
				"cable_code":    sg.cable,
				"segment_index": sg.idx,
				"a_end_box":     sg.aBox,
				"z_end_box":     sg.zBox,
			},
		})
	}

	// A business circuit whose endpoints are the -02 devices — with
	// physical corridors in scope it must not become a link.
	cir := insertRes(t, db, domain.Resource{
		Type: domain.ResourceTypeCircuit, NaturalKey: "CIR-100", Name: "CIR-100",
		Status: "在网", Attributes: map[string]string{},
	})
	harb02 := mustResource(t, db, "DEV-HARBIN-02")
	qiqi02 := mustResource(t, db, "DEV-QIQIHAR-02")
	for _, rel := range []domain.Relation{
		{ID: uuid.NewString(), Type: domain.RelationTypeAEnd, FromResourceID: cir.ID, ToResourceID: harb02.ID, FirstBatchID: "b1", CreatedAt: finished},
		{ID: uuid.NewString(), Type: domain.RelationTypeZEnd, FromResourceID: cir.ID, ToResourceID: qiqi02.ID, FirstBatchID: "b1", CreatedAt: finished},
	} {
		if err := db.Relations.Insert(ctx, rel); err != nil {
			t.Fatalf("insert relation: %v", err)
		}
	}

	// Materialize.
	res, err := svc.Materialize(ctx, "corridor-lab", "", []string{"b1"})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	// Counts: 6 devices, 2 physical hops (OC-A folded, OC-B direct), 4
	// interfaces — the circuit contributed nothing.
	if res.Counts.Devices != 6 || res.Counts.Links != 2 || res.Counts.Interfaces != 4 {
		t.Fatalf("counts = %+v, want devices 6 links 2 interfaces 4 (circuit suppressed)", res.Counts)
	}

	topo := mustTopology(t, svc, res.WorkspaceID)
	if len(topo.Nodes) != 6 || len(topo.Edges) != 2 {
		t.Fatalf("topology: %d nodes / %d edges, want 6 / 2", len(topo.Nodes), len(topo.Edges))
	}

	// Edge 1: OC-A — Harbin-01 ↔ Daqing-01 (the -01 devices win the
	// nearest-device tie by code; Zhaodong's run collapses).
	eA := findEdgeByLabel(t, topo, "OC-A")
	if eA.ADeviceCode != "DEV-HARBIN-01" && eA.ZDeviceCode != "DEV-DAQING-01" &&
		!(eA.ADeviceCode == "DEV-DAQING-01" && eA.ZDeviceCode == "DEV-HARBIN-01") {
		t.Fatalf("OC-A edge = %s ↔ %s, want DEV-HARBIN-01 ↔ DEV-DAQING-01", eA.ADeviceCode, eA.ZDeviceCode)
	}
	// Edge 2: OC-B — Daqing-01 ↔ Qiqihar-01.
	eB := findEdgeByLabel(t, topo, "OC-B")
	if eB.ADeviceCode != "DEV-DAQING-01" && eB.ZDeviceCode != "DEV-QIQIHAR-01" &&
		!(eB.ADeviceCode == "DEV-QIQIHAR-01" && eB.ZDeviceCode == "DEV-DAQING-01") {
		t.Fatalf("OC-B edge = %s ↔ %s, want DEV-DAQING-01 ↔ DEV-QIQIHAR-01", eB.ADeviceCode, eB.ZDeviceCode)
	}

	// Link lineage: ResourceID = the optical_cable resource.
	links, err := db.SimLinks.ListByWorkspace(ctx, res.WorkspaceID)
	if err != nil {
		t.Fatalf("ListByWorkspace: %v", err)
	}
	for _, l := range links {
		want := cableA.ID
		if l.Label == "OC-B" {
			want = cableB.ID
		}
		if l.ResourceID != want {
			t.Errorf("link %s ResourceID = %s, want %s", l.Label, l.ResourceID, want)
		}
	}
	// Daqing-01 is on both hops: eth0 + eth1; Harbin/Qiqihar -01s: eth0.
	daqingNode := findNode(t, topo, "DEV-DAQING-01")
	if len(daqingNode.Interfaces) != 2 {
		t.Errorf("DEV-DAQING-01 interfaces = %d, want 2 (both hops land here)", len(daqingNode.Interfaces))
	}
	harbNode := findNode(t, topo, "DEV-HARBIN-01")
	if len(harbNode.Interfaces) != 1 || harbNode.Interfaces[0].Name != "eth0" {
		t.Errorf("DEV-HARBIN-01 interfaces = %+v, want [eth0]", harbNode.Interfaces)
	}
	// The -02 devices have no links at all (only the suppressed circuit
	// referenced them).
	harb02Node := findNode(t, topo, "DEV-HARBIN-02")
	if len(harb02Node.Interfaces) != 0 {
		t.Errorf("DEV-HARBIN-02 interfaces = %d, want 0 (circuit not materialized)", len(harb02Node.Interfaces))
	}
}

// TestMaterializePhysicalFallbackKeepsCircuits pins the fallback: with no
// cable segments in scope, the legacy circuit behavior is byte-identical
// (the Phase 1 contract — 27 devices / 20 links on the seeded fixtures is
// already pinned by TestWorkspaceMaterialize; here the mechanism is
// pinned on a minimal device+circuit-only dataset).
func TestMaterializePhysicalFallbackKeepsCircuits(t *testing.T) {
	svc, db := newEngine(t)
	ctx := context.Background()

	finished := time.Now()
	if err := db.Batches.Create(ctx, domain.ImportBatch{
		ID: "b1", WorkbookName: "p1.xlsx", WorkbookSHA256: "sha1",
		Status: domain.BatchStatusCompleted, Mapping: "{}", RuleVersion: "v1",
		StartedAt: finished, FinishedAt: &finished,
	}); err != nil {
		t.Fatalf("create batch: %v", err)
	}

	// Two devices, one circuit — no segments, no boxes, no cables.
	devA := insertRes(t, db, domain.Resource{
		Type: domain.ResourceTypeDevice, NaturalKey: "DEV-A-01", Name: "DEV-A-01",
		Status: "在网", Attributes: map[string]string{"device_name": "DEV-A-01"},
	})
	devZ := insertRes(t, db, domain.Resource{
		Type: domain.ResourceTypeDevice, NaturalKey: "DEV-Z-01", Name: "DEV-Z-01",
		Status: "在网", Attributes: map[string]string{"device_name": "DEV-Z-01"},
	})
	cir := insertRes(t, db, domain.Resource{
		Type: domain.ResourceTypeCircuit, NaturalKey: "CIR-1", Name: "CIR-1",
		Status: "在网", Attributes: map[string]string{},
	})
	for _, rel := range []domain.Relation{
		{ID: uuid.NewString(), Type: domain.RelationTypeAEnd, FromResourceID: cir.ID, ToResourceID: devA.ID, FirstBatchID: "b1", CreatedAt: finished},
		{ID: uuid.NewString(), Type: domain.RelationTypeZEnd, FromResourceID: cir.ID, ToResourceID: devZ.ID, FirstBatchID: "b1", CreatedAt: finished},
	} {
		if err := db.Relations.Insert(ctx, rel); err != nil {
			t.Fatalf("insert relation: %v", err)
		}
	}

	res, err := svc.Materialize(ctx, "p1-lab", "", []string{"b1"})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if res.Counts.Devices != 2 || res.Counts.Links != 1 || res.Counts.Interfaces != 2 {
		t.Fatalf("counts = %+v, want devices 2 links 1 interfaces 2 (circuit fallback)", res.Counts)
	}
	links, err := db.SimLinks.ListByWorkspace(ctx, res.WorkspaceID)
	if err != nil {
		t.Fatalf("ListByWorkspace: %v", err)
	}
	if len(links) != 1 || links[0].ResourceID != cir.ID {
		t.Fatalf("fallback link lineage = %+v, want the single CIR-1 link", links)
	}
}

// mustResource fetches one resource by natural key.
func mustResource(t *testing.T, db *repo.DB, naturalKey string) domain.Resource {
	t.Helper()
	list, _, err := db.Resources.Search(context.Background(), repo.ResourceSearchFilter{Type: domain.ResourceTypeDevice})
	if err != nil {
		t.Fatalf("search devices: %v", err)
	}
	for _, r := range list {
		if r.NaturalKey == naturalKey {
			return r
		}
	}
	t.Fatalf("resource %s not found", naturalKey)
	return domain.Resource{}
}

// mustDevice reads one virtual device by code.
func mustDevice(t *testing.T, svc *Service, workspaceID, code string) TopologyNode {
	t.Helper()
	return findNode(t, mustTopology(t, svc, workspaceID), code)
}

// ftoa formats a float without %.17g noise (the importer writes plain
// decimal strings too).
func ftoa(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
