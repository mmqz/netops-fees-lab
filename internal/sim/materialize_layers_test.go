package sim

// materialize_layers_test.go — issue #51 (W33) acceptance tests: REAL port
// interfaces (attribution + spill + Phase 1 fallback), the L0/L3/L4/L5/L6
// overlay nodes and semantic edges, cluster aggregation in the read path,
// and the LayerMembers drill-down. The fixture is a miniature of the
// realdata shape: 3 stations, boards+ports, a corridor cable with an OTN
// channel, stations uplinked to devices, a customer with a service and a
// leased line, a ring and a PON subtree.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// layersFixture builds the miniature dataset in one completed batch and
// returns the entity lookup handles the assertions need.
type layersFixture struct {
	dev01, dev02, dev03 domain.Resource
	board11             domain.Resource
	stationA, stationB  domain.Resource
	customer            domain.Resource
	circuit             domain.Resource
	ring                domain.Resource
	olt                 domain.Resource
	onu1                domain.Resource
	ponPort             domain.Resource
}

func insertLayersFixture(t *testing.T, db *repo.DB) layersFixture {
	t.Helper()
	ctx := context.Background()
	finished := time.Now()
	if err := db.Batches.Create(ctx, domain.ImportBatch{
		ID: "b1", WorkbookName: "layers.xlsx", WorkbookSHA256: "sha",
		Status: domain.BatchStatusCompleted, Mapping: "{}", RuleVersion: "v1",
		StartedAt: finished, FinishedAt: &finished,
	}); err != nil {
		t.Fatalf("create batch: %v", err)
	}
	rel := func(from domain.Resource, typ domain.RelationType, to domain.Resource) {
		t.Helper()
		if err := db.Relations.Insert(ctx, domain.Relation{
			ID: uuid.NewString(), Type: typ, FromResourceID: from.ID, ToResourceID: to.ID,
			FirstBatchID: "b1", CreatedAt: finished,
		}); err != nil {
			t.Fatalf("insert relation %s→%s: %v", typ, to.NaturalKey, err)
		}
	}
	res := func(r domain.Resource) domain.Resource {
		t.Helper()
		return insertRes(t, db, r)
	}

	f := layersFixture{}
	// —— L1 devices at 3 stations ——
	f.dev01 = res(domain.Resource{
		Type: domain.ResourceTypeDevice, NaturalKey: "DEV-A-01", Name: "甲站传输设备01", Status: "在网",
		Attributes: map[string]string{"geo_lat": "45.80", "geo_lng": "126.53"},
	})
	f.dev02 = res(domain.Resource{
		Type: domain.ResourceTypeDevice, NaturalKey: "DEV-B-01", Name: "乙站传输设备01", Status: "在网",
		Attributes: map[string]string{"geo_lat": "46.06", "geo_lng": "125.96"},
	})
	f.dev03 = res(domain.Resource{
		Type: domain.ResourceTypeDevice, NaturalKey: "DEV-A-02", Name: "甲站传输设备02", Status: "在网",
		Attributes: map[string]string{"geo_lat": "45.80", "geo_lng": "126.53"},
	})

	// —— boards + ports: 2 boards on dev01, 3 real ports + attribution ——
	f.board11 = res(domain.Resource{
		Type: domain.ResourceTypeBoard, NaturalKey: "BRD-A01-01", Name: "板卡", Status: "在网",
		Attributes: map[string]string{},
	})
	res(domain.Resource{
		Type: domain.ResourceTypeBoard, NaturalKey: "BRD-A01-02", Name: "板卡", Status: "在网",
		Attributes: map[string]string{},
	})
	rel(f.board11, domain.RelationTypeInstalledIn, f.dev01)
	board2, _, _ := searchOne(t, db, domain.ResourceTypeBoard, "BRD-A01-02")
	rel(board2, domain.RelationTypeInstalledIn, f.dev01)
	// 3 real ports on dev01 (2 on board 1, 1 on board 2); one unattributed
	// port (no board ref) must be skipped honestly.
	for _, spec := range []struct {
		name, board string
		oper        string
	}{
		{"xgei-1/1/1", "BRD-A01-01", "up"},
		{"xgei-1/1/2", "BRD-A01-01", "up"},
		{"xgei-1/2/1", "BRD-A01-02", "down"},
	} {
		p := res(domain.Resource{
			Type: domain.ResourceTypePort, NaturalKey: "DEV-A-01|" + spec.name, Name: spec.name,
			Status: "在网",
			Attributes: map[string]string{
				"port_name": spec.name, "oper_status": spec.oper, "board_id": spec.board,
			},
		})
		if spec.board != "" {
			board := f.board11
			if spec.board != "BRD-A01-01" {
				board = board2
			}
			rel(p, domain.RelationTypeMountedOn, board)
		}
	}
	res(domain.Resource{
		Type: domain.ResourceTypePort, NaturalKey: "DEV-A-01|xgei-9/9/9", Name: "xgei-9/9/9",
		Status: "在网", Attributes: map[string]string{"port_name": "xgei-9/9/9"},
	}) // no board ref → skipped

	// —— L2 corridor: cable + segments + splice boxes (A→B hop) ——
	cable := res(domain.Resource{
		Type: domain.ResourceTypeOpticalCable, NaturalKey: "OC-L", Name: "甲乙光缆", Status: "在网",
		Attributes: map[string]string{},
	})
	rel(cable, domain.RelationTypeCableAEnd, stationSite(t, db, "A"))
	rel(cable, domain.RelationTypeCableZEnd, stationSite(t, db, "B"))
	rel(cable, domain.RelationTypeConnectsDevice, f.dev01)
	for _, box := range []struct {
		code   string
		latlng string
	}{{"SB-L-A", "45.80|126.53"}, {"SB-L-Z", "46.06|125.96"}} {
		lat, lng := box.latlng[:5], box.latlng[6:]
		insertRes(t, db, domain.Resource{
			Type: domain.ResourceTypeSpliceBox, NaturalKey: box.code, Name: box.code, Status: "在网",
			Attributes: map[string]string{"geo_lat": lat, "geo_lng": lng},
		})
	}
	seg := res(domain.Resource{
		Type: domain.ResourceTypeCableSegment, NaturalKey: "SEG-L-01", Name: "段", Status: "在网",
		Attributes: map[string]string{"cable_code": "OC-L", "segment_index": "1", "a_end_box": "SB-L-A", "z_end_box": "SB-L-Z"},
	})
	_ = seg

	// —— L3: circuit + otn_och over the cable ——
	f.circuit = res(domain.Resource{
		Type: domain.ResourceTypeCircuit, NaturalKey: "CIR-001", Name: "电路一", Status: "在网",
		Attributes: map[string]string{},
	})
	rel(f.circuit, domain.RelationTypeAEnd, f.dev01)
	rel(f.circuit, domain.RelationTypeZEnd, f.dev02)
	och := res(domain.Resource{
		Type: domain.ResourceTypeOTNOch, NaturalKey: "OCH-L-01", Name: "波道", Status: "在网",
		Attributes: map[string]string{},
	})
	rel(och, domain.RelationTypeRoutedOver, cable)
	rel(f.circuit, domain.RelationTypeCarriedBy, och)

	// —— L4: 3 base stations uplinked to dev01/dev02 + PON subtree ——
	f.stationA = res(domain.Resource{
		Type: domain.ResourceTypeBaseStation, NaturalKey: "460-0-1-1", Name: "甲基站", Status: "在网",
		Attributes: map[string]string{"geo_lat": "45.81", "geo_lng": "126.54"},
	})
	f.stationB = res(domain.Resource{
		Type: domain.ResourceTypeBaseStation, NaturalKey: "460-0-1-2", Name: "乙基站", Status: "在网",
		Attributes: map[string]string{"geo_lat": "46.05", "geo_lng": "125.97"},
	})
	res(domain.Resource{
		Type: domain.ResourceTypeBaseStation, NaturalKey: "460-0-1-3", Name: "甲基站2", Status: "停用",
		Attributes: map[string]string{"geo_lat": "45.82", "geo_lng": "126.55"},
	})
	rel(f.stationA, domain.RelationTypeUplinkTo, f.dev01)
	rel(f.stationB, domain.RelationTypeUplinkTo, f.dev02)
	station3, _, _ := searchOne(t, db, domain.ResourceTypeBaseStation, "460-0-1-3")
	rel(station3, domain.RelationTypeUplinkTo, f.dev01)

	// PON subtree: olt (in station A's machine room) + 2 pon ports + onu.
	mr := res(domain.Resource{
		Type: domain.ResourceTypeMachineRoom, NaturalKey: "MR-A", Name: "甲站机房", Status: "在网",
		Attributes: map[string]string{},
	})
	site := stationSite(t, db, "A")
	rel(mr, domain.RelationTypeBelongsTo, site)
	rel(f.dev01, domain.RelationTypeHousedIn, mr)
	// The B station's machine room anchors dev02 (och Z-end resolution).
	mrB := res(domain.Resource{
		Type: domain.ResourceTypeMachineRoom, NaturalKey: "MR-B", Name: "乙站机房", Status: "在网",
		Attributes: map[string]string{},
	})
	rel(mrB, domain.RelationTypeBelongsTo, stationSite(t, db, "B"))
	rel(f.dev02, domain.RelationTypeHousedIn, mrB)
	f.olt = res(domain.Resource{
		Type: domain.ResourceTypeOLT, NaturalKey: "OLT-A", Name: "甲站OLT", Status: "在网",
		Attributes: map[string]string{"machine_room_id": "MR-A"},
	})
	f.ponPort = res(domain.Resource{
		Type: domain.ResourceTypePONPort, NaturalKey: "OLT-A|0/1/1", Name: "PON口", Status: "在网",
		Attributes: map[string]string{"olt_code": "OLT-A", "port_no": "0/1/1"},
	})
	res(domain.Resource{
		Type: domain.ResourceTypePONPort, NaturalKey: "OLT-A|0/1/2", Name: "PON口", Status: "在网",
		Attributes: map[string]string{"olt_code": "OLT-A", "port_no": "0/1/2"},
	})
	f.onu1 = res(domain.Resource{
		Type: domain.ResourceTypeONU, NaturalKey: "ONU-A-01", Name: "商务楼宇1", Status: "在网",
		Attributes: map[string]string{"olt_code": "OLT-A", "connect_port": "OLT-A|0/1/1"},
	})
	rel(f.onu1, domain.RelationTypeConnectsONU, f.ponPort)

	// —— L5: ring membership + protection group ——
	f.ring = res(domain.Resource{
		Type: domain.ResourceTypeRing, NaturalKey: "RING-L", Name: "环", Status: "在网",
		Attributes: map[string]string{},
	})
	rel(f.dev01, domain.RelationTypeMemberOfRing, f.ring)
	rel(f.dev02, domain.RelationTypeMemberOfRing, f.ring)
	pg := res(domain.Resource{
		Type: domain.ResourceTypeProtectionGroup, NaturalKey: "PG-L", Name: "保护组", Status: "在网",
		Attributes: map[string]string{},
	})
	rel(pg, domain.RelationTypeProtects, f.circuit)
	// port in_vpn
	vpn := res(domain.Resource{
		Type: domain.ResourceTypeVPNInstance, NaturalKey: "L3VPN-L", Name: "VPN", Status: "在网",
		Attributes: map[string]string{},
	})
	port11, _, _ := searchOne(t, db, domain.ResourceTypePort, "DEV-A-01|xgei-1/1/1")
	rel(port11, domain.RelationTypeInVPN, vpn)

	// —— L6: customer + service + leased line ——
	f.customer = res(domain.Resource{
		Type: domain.ResourceTypeCustomer, NaturalKey: "CUST-L", Name: "演示客户", Status: "在网",
		Attributes: map[string]string{},
	})
	rel(f.dev01, domain.RelationTypeServes, f.customer)
	rel(f.stationA, domain.RelationTypeServes, f.customer)
	rel(f.stationB, domain.RelationTypeServes, f.customer)
	svc := res(domain.Resource{
		Type: domain.ResourceTypeService, NaturalKey: "SVC-L", Name: "专线业务", Status: "在网",
		Attributes: map[string]string{},
	})
	rel(svc, domain.RelationTypeSubscribedBy, f.customer)
	rel(svc, domain.RelationTypeDependsOn, f.dev02)
	rel(svc, domain.RelationTypeProvidedBy, f.circuit)
	ll := res(domain.Resource{
		Type: domain.ResourceTypeLeasedLine, NaturalKey: "LL-L", Name: "政企专线", Status: "在网",
		Attributes: map[string]string{},
	})
	rel(ll, domain.RelationTypeSubscribedBy, f.customer)

	// —— L0: one manhole (spatial anchor) ——
	insertRes(t, db, domain.Resource{
		Type: domain.ResourceTypeManhole, NaturalKey: "MH-L-01", Name: "人井", Status: "在网",
		Attributes: map[string]string{"geo_lat": "45.90", "geo_lng": "126.20"},
	})
	return f
}

// stationSite lazily creates (or returns) the site resource of a station.
func stationSite(t *testing.T, db *repo.DB, code string) domain.Resource {
	t.Helper()
	key := "ST-" + code
	res, found, err := searchOne(t, db, domain.ResourceTypeSite, key)
	if err != nil {
		t.Fatalf("search site %s: %v", key, err)
	}
	if found {
		return res
	}
	return insertRes(t, db, domain.Resource{
		Type: domain.ResourceTypeSite, NaturalKey: key, Name: key + "局站", Status: "在网",
		Attributes: map[string]string{"geo_lat": "45.80", "geo_lng": "126.53"},
	})
}

// searchOne finds one resource by type + natural key.
func searchOne(t *testing.T, db *repo.DB, rt domain.ResourceType, naturalKey string) (domain.Resource, bool, error) {
	t.Helper()
	list, _, err := db.Resources.Search(context.Background(), repo.ResourceSearchFilter{Type: rt})
	if err != nil {
		return domain.Resource{}, false, err
	}
	for _, r := range list {
		if r.NaturalKey == naturalKey {
			return r, true, nil
		}
	}
	return domain.Resource{}, false, nil
}

// TestMaterializeRealPortInterfaces pins the #51 interface path: real
// ports become interfaces NAMED by the port's own name, attributed via
// port→board→device; the unattributable port is skipped; link endpoints
// bind real interfaces first and spill to synthetic only on exhaustion.
func TestMaterializeRealPortInterfaces(t *testing.T) {
	svc, db := newEngine(t)
	ctx := context.Background()
	f := insertLayersFixture(t, db)
	_ = f

	res, err := svc.Materialize(ctx, "layers-lab", "", []string{"b1"})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	topo := mustTopology(t, svc, res.WorkspaceID)

	// dev01: 3 real ports (xgei-1/1/1, xgei-1/1/2 up; xgei-1/2/1 down) + 0
	// synthetic (the single OC-L hop needs one endpoint, taken from the
	// real pool) → 3 interfaces, NO eth<n>.
	nodeA := findNode(t, topo, "DEV-A-01")
	if len(nodeA.Interfaces) != 3 {
		t.Fatalf("DEV-A-01 interfaces = %d, want 3 (real ports only)", len(nodeA.Interfaces))
	}
	iface := findIface(t, nodeA, "xgei-1/2/1")
	if iface.Status != "down" {
		t.Errorf("xgei-1/2/1 status = %s, want down (oper_status=down)", iface.Status)
	}
	for _, i := range nodeA.Interfaces {
		if strings.HasPrefix(i.Name, "eth") {
			t.Errorf("DEV-A-01 has synthetic interface %s — real ports must cover the link", i.Name)
		}
	}
	// dev02 (no ports at all): the hop's other endpoint → 1 synthetic eth0.
	nodeB := findNode(t, topo, "DEV-B-01")
	if len(nodeB.Interfaces) != 1 || nodeB.Interfaces[0].Name != "eth0" {
		t.Errorf("DEV-B-01 interfaces = %+v, want [eth0] (synthetic fallback)", nodeB.Interfaces)
	}
	// The unattributed port must NOT appear anywhere.
	for _, n := range topo.Nodes {
		for _, i := range n.Interfaces {
			if i.Name == "xgei-9/9/9" {
				t.Errorf("unattributed port xgei-9/9/9 materialized — must be skipped")
			}
		}
	}
	// Counts: 3 devices, 1 corridor hop, 3+1 interfaces.
	if res.Counts.Devices != 3 || res.Counts.Links != 1 || res.Counts.Interfaces != 4 {
		t.Fatalf("counts = %+v, want devices 3 / links 1 / interfaces 4", res.Counts)
	}
}

// TestMaterializeLayerOverlay pins the overlay shape end to end: node
// kinds per layer, the L3 logical edges (circuit + och), the L4 cluster
// aggregation (2 stations on DEV-A-01, 1 on DEV-B-01), the L5 group
// edges, the L6 business edges, and the read-path payloads.
func TestMaterializeLayerOverlay(t *testing.T) {
	svc, db := newEngine(t)
	ctx := context.Background()
	f := insertLayersFixture(t, db)

	res, err := svc.Materialize(ctx, "layers-lab", "", []string{"b1"})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	topo := mustTopology(t, svc, res.WorkspaceID)
	if topo.Layers == nil {
		t.Fatalf("topology payload lacks layers (issue #51)")
	}

	// —— nodes: one aggregate per anchor device + the PON subtree + L5/L6 ——
	var clusters, olts, onus, customers, services, lls, rings, manholes int
	var clusterA *LayerNodePayload
	for i, n := range topo.Layers.Nodes {
		switch n.Kind {
		case "cluster":
			clusters++
			if n.AnchorDeviceID != "" {
				if dev, ok := lookupDev(t, topo, n.AnchorDeviceID); ok && dev.Code == "DEV-A-01" {
					clusterA = &topo.Layers.Nodes[i]
				}
			}
		case "olt":
			olts++
		case "onu":
			onus++
		case "customer":
			customers++
		case "service":
			services++
		case "leased_line":
			lls++
		case "ring":
			rings++
		case "manhole":
			manholes++
		case "base_station":
			t.Errorf("individual base_station node in the payload — must be aggregated")
		}
	}
	if clusters != 2 || olts != 1 || onus != 1 || customers != 1 || services != 1 || lls != 1 || rings != 1 || manholes != 1 {
		t.Fatalf("layer node kinds: clusters=%d olt=%d onu=%d customer=%d service=%d ll=%d ring=%d manhole=%d",
			clusters, olts, onus, customers, services, lls, rings, manholes)
	}
	if clusterA == nil {
		t.Fatalf("no cluster node for DEV-A-01 (2 stations must aggregate there)")
	}
	if clusterA.MemberCount != 2 || len(clusterA.Sample) != 2 || !clusterA.Aggregated {
		t.Errorf("cluster A = %+v, want 2 members + sample + aggregated", clusterA)
	}
	if clusterA.ID != "agg-"+clusterA.AnchorDeviceID {
		t.Errorf("cluster id = %s, want agg-<anchor>", clusterA.ID)
	}
	// OLT detail carries its PON ports.
	var oltPayload *LayerNodePayload
	for i, n := range topo.Layers.Nodes {
		if n.Kind == "olt" {
			oltPayload = &topo.Layers.Nodes[i]
		}
	}
	if oltPayload == nil || oltPayload.Detail == nil {
		t.Fatalf("olt node without detail (ponPorts)")
	}
	var detail struct {
		PonPorts []string `json:"ponPorts"`
	}
	if err := json.Unmarshal(oltPayload.Detail, &detail); err != nil {
		t.Fatalf("unmarshal olt detail: %v", err)
	}
	if len(detail.PonPorts) != 2 || detail.PonPorts[0] != "0/1/1" {
		t.Errorf("olt ponPorts = %v, want [0/1/1 0/1/2]", detail.PonPorts)
	}
	// Customer detail: station serves count (2 stations serve CUST-L).
	var custPayload *LayerNodePayload
	for i, n := range topo.Layers.Nodes {
		if n.Kind == "customer" {
			custPayload = &topo.Layers.Nodes[i]
		}
	}
	if custPayload == nil || custPayload.Detail == nil {
		t.Fatalf("customer node without detail (stationServes)")
	}
	var custDetail struct {
		StationServes int `json:"stationServes"`
	}
	if err := json.Unmarshal(custPayload.Detail, &custDetail); err != nil || custDetail.StationServes != 2 {
		t.Errorf("customer stationServes = %+v (err %v), want 2", custDetail, err)
	}

	// —— edges: L3 circuit + och, L4 onu→olt, L5 ring/in_vpn/protects,
	// L6 serves/subscribed_by/depends_on/provided_by ——
	var l3Circuit, l3Och, l4Onu, l5Ring, l5VPN, l5PG, l6Serves, l6Sub, l6Dep, l6Prov int
	for _, e := range topo.Layers.Edges {
		switch {
		case e.Layer == "L3" && e.Label == "CIR-001":
			l3Circuit++
			if e.Sem != "logical" || e.ACode != "DEV-A-01" || e.ZCode != "DEV-B-01" {
				t.Errorf("circuit edge = %+v, want logical DEV-A-01↔DEV-B-01", e)
			}
		case e.Layer == "L3" && e.Label == "OCH-L-01":
			l3Och++
		case e.Layer == "L4":
			l4Onu++
			if e.AKind != "node" || e.ZKind != "node" || !strings.Contains(e.Label, "OLT-A") {
				t.Errorf("onu edge = %+v, want node→node OLT-A|…", e)
			}
		case e.Layer == "L5" && e.Label == "member_of_ring":
			l5Ring++
		case e.Layer == "L5" && e.Label == "in_vpn":
			l5VPN++
		case e.Layer == "L5" && strings.HasPrefix(e.Label, "保护 "):
			l5PG++
		case e.Layer == "L6" && e.Label == "serves":
			l6Serves++
		case e.Layer == "L6" && e.Label == "subscribed_by":
			l6Sub++
		case e.Layer == "L6" && e.Label == "depends_on":
			l6Dep++
		case e.Layer == "L6" && strings.HasPrefix(e.Label, "提供电路 "):
			l6Prov++
		}
	}
	if l3Circuit != 1 || l3Och != 1 || l4Onu != 1 || l5Ring != 2 || l5VPN != 1 || l5PG != 1 ||
		l6Serves != 1 || l6Sub != 2 || l6Dep != 1 || l6Prov != 1 {
		t.Fatalf("layer edges: cir=%d och=%d onu=%d ring=%d vpn=%d pg=%d serves=%d sub=%d dep=%d prov=%d",
			l3Circuit, l3Och, l4Onu, l5Ring, l5VPN, l5PG, l6Serves, l6Sub, l6Dep, l6Prov)
	}
	// The in_vpn edge points at a real interface id that exists in the
	// drill state.
	for _, e := range topo.Layers.Edges {
		if e.Label == "in_vpn" && e.AKind == "interface" {
			found := false
			for _, n := range topo.Nodes {
				for _, i := range n.Interfaces {
					if i.ID == e.ARef {
						found = true
					}
				}
			}
			if !found {
				t.Errorf("in_vpn edge points at %s — no such drill interface", e.ARef)
			}
		}
	}

	// —— summary ——
	if s := topo.Layers.Summary.ByLayer["L4"]; s.Clusters != 2 || s.ClusteredNodes != 3 {
		t.Errorf("L4 summary = %+v, want 2 clusters / 3 clustered stations", s)
	}
	if s := topo.Layers.Summary.ByLayer["L3"]; s.Edges != 2 {
		t.Errorf("L3 summary = %+v, want 2 logical edges", s)
	}
	_ = f
}

// TestLayerMembersPagination pins the cluster drill-down: members page in
// code order with the full total.
func TestLayerMembersPagination(t *testing.T) {
	svc, db := newEngine(t)
	ctx := context.Background()
	insertLayersFixture(t, db)
	res, err := svc.Materialize(ctx, "layers-lab", "", []string{"b1"})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	topo := mustTopology(t, svc, res.WorkspaceID)
	var anchor string
	for _, n := range topo.Layers.Nodes {
		if n.Kind == "cluster" && n.MemberCount == 2 {
			anchor = n.AnchorDeviceID
		}
	}
	if anchor == "" {
		t.Fatalf("no 2-member cluster to drill into")
	}
	members, total, err := svc.LayerMembers(ctx, res.WorkspaceID, anchor, 1, 1)
	if err != nil || total != 2 || len(members) != 1 {
		t.Fatalf("LayerMembers page1 = %d items / total %d (err %v), want 1/2", len(members), total, err)
	}
	if members[0].Kind != "base_station" || members[0].GeoLat == nil {
		t.Errorf("member[0] = %+v, want base_station with geo", members[0])
	}
	members2, total2, err := svc.LayerMembers(ctx, res.WorkspaceID, anchor, 2, 1)
	if err != nil || total2 != 2 || len(members2) != 1 {
		t.Fatalf("LayerMembers page2 = %d items / total %d (err %v), want 1/2", len(members2), total2, err)
	}
	if members2[0].Code <= members[0].Code {
		t.Errorf("members not in code order: %s then %s", members[0].Code, members2[0].Code)
	}
	// Unknown cluster: empty page, zero total (not an error).
	members3, total3, err := svc.LayerMembers(ctx, res.WorkspaceID, "vd-999", 1, 20)
	if err != nil || total3 != 0 || len(members3) != 0 {
		t.Errorf("unknown cluster = %d/%d (err %v), want 0/0 nil", len(members3), total3, err)
	}
	// Unknown workspace: ErrNotFound.
	if _, _, err := svc.LayerMembers(ctx, "no-such-ws", anchor, 1, 20); err == nil {
		t.Errorf("unknown workspace accepted")
	}
}

// TestLayerOverlayDeterminism pins the overlay determinism across fresh
// databases (modulo workspace + import uuids).
func TestLayerOverlayDeterminism(t *testing.T) {
	ctx := context.Background()
	svc1, db1 := newEngine(t)
	insertLayersFixture(t, db1)
	res1, err := svc1.Materialize(ctx, "d1", "", []string{"b1"})
	if err != nil {
		t.Fatalf("Materialize 1: %v", err)
	}
	svc2, db2 := newEngine(t)
	insertLayersFixture(t, db2)
	res2, err := svc2.Materialize(ctx, "d2", "", []string{"b1"})
	if err != nil {
		t.Fatalf("Materialize 2: %v", err)
	}
	if res1.Counts.LayerNodes != res2.Counts.LayerNodes || res1.Counts.LayerLinks != res2.Counts.LayerLinks {
		t.Fatalf("overlay counts differ: %+v vs %+v", res1.Counts, res2.Counts)
	}
	j1 := marshalLayers(t, svc1, res1.WorkspaceID)
	j2 := marshalLayers(t, svc2, res2.WorkspaceID)
	if replaceResourceUUIDs(j1) != replaceResourceUUIDs(j2) {
		t.Fatalf("layer payload differs across fresh databases (modulo import uuids)")
	}
}

func marshalLayers(t *testing.T, svc *Service, workspaceID string) string {
	t.Helper()
	topo := mustTopology(t, svc, workspaceID)
	if topo.Layers == nil {
		t.Fatalf("no layers")
	}
	b, err := json.Marshal(topo.Layers)
	if err != nil {
		t.Fatalf("marshal layers: %v", err)
	}
	return string(b)
}

func lookupDev(t *testing.T, topo Topology, id string) (TopologyNode, bool) {
	t.Helper()
	for _, n := range topo.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return TopologyNode{}, false
}

// TestLayerOverlayPureEdgeContract pins the #53-A data contract: a pure-edge
// layer (L3 transmission: nodes=0, edges>0 — both endpoints on canvas
// devices) is a usable layer. The frontend layer toggle judges "has data"
// by nodes>0 || edges>0 (TopologyCanvas.tsx #53-A fix: the old "nodes>0"
// check permanently disabled the L3 switch and hid 207 circuits). This test
// keeps the payload side honest so that judgement stays sufficient:
//  1. every layer listed in the summary is usable (nodes>0 || edges>0);
//  2. every layer edge endpoint resolves to a renderable canvas entity
//     (device / interface-of-device / layer node) and no device self-loop
//     (the frontend drops self-loops, which would inflate edge counts);
//  3. every overlay node's anchor device exists (the frontend derives the
//     access-uplink edge from anchorDeviceId).
func TestLayerOverlayPureEdgeContract(t *testing.T) {
	ctx := context.Background()
	svc, db := newEngine(t)
	insertLayersFixture(t, db)

	res, err := svc.Materialize(ctx, "layers-contract", "", []string{"b1"})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	topo := mustTopology(t, svc, res.WorkspaceID)
	if topo.Layers == nil {
		t.Fatalf("topology payload lacks layers (issue #51)")
	}

	// (1) L3 is the canonical pure-edge layer: no nodes of its own, its
	// edges hang between devices already on the canvas. The shape is load
	// bearing — if materialization ever starts emitting L3 nodes, the
	// frontend fix must be revisited, and if L3 loses its edges the layer
	// must be disabled again (nodes=0 && edges=0).
	l3 := topo.Layers.Summary.ByLayer["L3"]
	if l3.Nodes != 0 {
		t.Errorf("L3 nodes = %d, want 0 (pure-edge layer shape pinned by #53)", l3.Nodes)
	}
	if l3.Edges <= 0 {
		t.Fatalf("L3 edges = %d, want > 0 (transmission circuits must exist)", l3.Edges)
	}
	for layer, s := range topo.Layers.Summary.ByLayer {
		if s.Nodes <= 0 && s.Edges <= 0 {
			t.Errorf("layer %s in summary with nodes=%d edges=%d — listed layers must be usable (nodes>0 || edges>0)", layer, s.Nodes, s.Edges)
		}
	}

	// (2) edge endpoints resolve to canvas-renderable entities.
	deviceIDs := make(map[string]bool)
	ifaceIDs := make(map[string]bool)
	for _, n := range topo.Nodes {
		deviceIDs[n.ID] = true
		for _, i := range n.Interfaces {
			ifaceIDs[i.ID] = true
		}
	}
	layerNodeIDs := make(map[string]bool)
	for _, n := range topo.Layers.Nodes {
		layerNodeIDs[n.ID] = true
	}
	resolves := func(kind, ref string) bool {
		switch kind {
		case domain.LayerEndpointDevice:
			return deviceIDs[ref]
		case domain.LayerEndpointInterface:
			return ifaceIDs[ref]
		case domain.LayerEndpointNode:
			return layerNodeIDs[ref]
		}
		return false
	}
	for _, e := range topo.Layers.Edges {
		if !resolves(e.AKind, e.ARef) {
			t.Errorf("edge %s A endpoint %s:%s does not resolve to a canvas entity", e.ID, e.AKind, e.ARef)
		}
		if !resolves(e.ZKind, e.ZRef) {
			t.Errorf("edge %s Z endpoint %s:%s does not resolve to a canvas entity", e.ID, e.ZKind, e.ZRef)
		}
		if e.AKind == domain.LayerEndpointDevice && e.ZKind == domain.LayerEndpointDevice && e.ARef == e.ZRef {
			t.Errorf("edge %s is a device self-loop — the frontend drops it, inflating summary edge counts", e.ID)
		}
	}

	// (3) anchors resolve to devices (derived access-uplink edges).
	for _, n := range topo.Layers.Nodes {
		if n.AnchorDeviceID != "" && !deviceIDs[n.AnchorDeviceID] {
			t.Errorf("node %s (%s) anchors at %s — no such device on the canvas", n.ID, n.Kind, n.AnchorDeviceID)
		}
	}
}
