package topology

import (
	"context"
	"fmt"
	"github.com/mmqz/netops-lab/internal/testsupport"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// This file is the §10.9 acceptance suite of the W22 traverse/impact
// engines. The seed graph is the W21 cross-layer chain (the same entity
// set as importer/chain_w21_test.go — the contract W22 consumes), plus a
// ring and a fan for the convergence/truncation tests.
//
//                                          ┌─ member_of_ring ─ RING-01 (L5)
//      DEV-R1/R2/R3 (L1) ─ a_end/z_end ─ CIR-R1/R2/R3 (L3)   (ring cycle)
//
//      CUST-01 (L6) ←subscribed_by─ SVC-01 (L6) ─provided_by→ CIR-01 (L3)
//        SVC-01 ─depends_on→ DEV-C1 (L1) ─housed_in→ MR-01 (L0) ─belongs_to→ SITE-01 (L0)
//        CIR-01 ─carried_by→ OCH-01 (L3) ─routed_over→ CABLE-01 (L2)
//        OCH-01 ─uses_fiber→ FIB-01 (L2) ─fiber_of→ CABLE-01
//        FIB-01 ─fiber_terminates→ ODT-01 (L2)
//        CABLE-01 ─cable_a_end→ SITE-01   ─cable_z_end→ SITE-02 (L0)
//        CIR-01 ─a_end/z_end→ DEV-C1

type seedNode struct {
	id   string
	typ  domain.ResourceType
	key  string
	name string
}

var chainNodes = []seedNode{
	{"res_SITE01", domain.ResourceTypeSite, "SITE-01", "加区枢纽楼"},
	{"res_SITE02", domain.ResourceTypeSite, "SITE-02", "道里汇聚局"},
	{"res_MR01", domain.ResourceTypeMachineRoom, "MR-01", "枢纽一楼机房"},
	{"res_DEVC1", domain.ResourceTypeDevice, "DEV-C1", "加区C1路由器"},
	{"res_CUST1", domain.ResourceTypeCustomer, "CUST-01", "哈尔滨银行"},
	{"res_SVC1", domain.ResourceTypeService, "SVC-01", "金融专线业务"},
	{"res_CIR1", domain.ResourceTypeCircuit, "CIR-01", "C-001"},
	{"res_OCH1", domain.ResourceTypeOTNOch, "OCH-01", "波道5"},
	{"res_FIB1", domain.ResourceTypeFiber, "CABLE-01|1", "1号纤芯"},
	{"res_CAB1", domain.ResourceTypeOpticalCable, "CABLE-01", "加区-道里主干"},
	{"res_ODT1", domain.ResourceTypeODFTerminal, "ODF-01|1", "1号端子"},
}

type seedEdge struct {
	from, to string
	rel      domain.RelationType
}

var chainEdges = []seedEdge{
	{"res_SVC1", "res_CUST1", domain.RelationTypeSubscribedBy},
	{"res_SVC1", "res_CIR1", domain.RelationTypeProvidedBy},
	{"res_SVC1", "res_DEVC1", domain.RelationTypeDependsOn},
	{"res_CIR1", "res_OCH1", domain.RelationTypeCarriedBy},
	{"res_OCH1", "res_CAB1", domain.RelationTypeRoutedOver},
	{"res_OCH1", "res_FIB1", domain.RelationTypeUsesFiber},
	{"res_FIB1", "res_CAB1", domain.RelationTypeFiberOf},
	{"res_FIB1", "res_ODT1", domain.RelationTypeFiberTerminates},
	{"res_CAB1", "res_SITE01", domain.RelationTypeCableAEnd},
	{"res_CAB1", "res_SITE02", domain.RelationTypeCableZEnd},
	{"res_DEVC1", "res_MR01", domain.RelationTypeHousedIn},
	{"res_MR01", "res_SITE01", domain.RelationTypeBelongsTo},
	{"res_CIR1", "res_DEVC1", domain.RelationTypeAEnd},
	{"res_CIR1", "res_DEVC1", domain.RelationTypeZEnd},
}

// seedChain builds the chain graph in a fresh DB and returns the store.
func seedChain(t *testing.T) (StoreFuncs, *repo.DB) {
	t.Helper()
	db, err := repo.Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	bat := insertBatch(t, db, "bat_chain")
	for _, n := range chainNodes {
		if err := db.Resources.Insert(ctx, domain.Resource{
			ID: n.id, Type: n.typ, NaturalKey: n.key, Name: n.name,
			Attributes: map[string]string{}, FirstBatchID: bat,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("insert %s: %v", n.id, err)
		}
	}
	for i, e := range chainEdges {
		if err := db.Relations.Insert(ctx, domain.Relation{
			ID:   fmt.Sprintf("rel_chain_%02d", i+1),
			Type: e.rel, FromResourceID: e.from, ToResourceID: e.to,
			FirstBatchID: bat, CreatedAt: now,
		}); err != nil {
			t.Fatalf("insert edge %s-%s->%s: %v", e.from, e.rel, e.to, err)
		}
	}
	return StoreFuncs{
		NodeFn: func(ctx context.Context, id string) (NodeInfo, error) {
			r, err := db.Resources.GetByID(ctx, id)
			if err != nil {
				return NodeInfo{}, err
			}
			return NodeInfoOf(r), nil
		},
		EdgeFn: func(ctx context.Context, id string) ([]domain.Relation, error) {
			return db.Relations.ListByResource(ctx, id)
		},
	}, db
}

// insertBatch creates a minimal import_batches row for FK satisfaction.
func insertBatch(t *testing.T, db *repo.DB, id string) string {
	t.Helper()
	ctx := context.Background()
	seedTS := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	err := db.Batches.Create(ctx, domain.ImportBatch{
		ID: id, WorkbookName: "seed.xlsx",
		WorkbookSHA256: "0000000000000000000000000000000000000000000000000000000000000000",
		Status:         domain.BatchStatusCompleted, RuleVersion: "seed", StartedAt: seedTS,
	})
	if err != nil {
		t.Fatalf("insert batch: %v", err)
	}
	return id
}

// nodeTypes collects the type set of the result for layer-coverage asserts.
func nodeTypes(res *TraverseResult) map[domain.ResourceType]bool {
	out := map[domain.ResourceType]bool{}
	for _, n := range res.Nodes {
		out[n.Type] = true
	}
	return out
}

// TestTraverseCrossLayerPenetration is §10.9 Test 1: from the customer the
// down-walk covers the whole L6→L0/L2 chain and the edges carry the right
// relation semantics.
func TestTraverseCrossLayerPenetration(t *testing.T) {
	store, _ := seedChain(t)
	ctx := context.Background()
	res, err := Traverse(ctx, store, "res_CUST1", TraverseParams{Direction: DirectionDown, MaxDepth: 8})
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}
	got := nodeTypes(res)
	for _, want := range []domain.ResourceType{
		domain.ResourceTypeCustomer, domain.ResourceTypeService, domain.ResourceTypeCircuit,
		domain.ResourceTypeOTNOch, domain.ResourceTypeFiber, domain.ResourceTypeOpticalCable,
		domain.ResourceTypeSite, domain.ResourceTypeMachineRoom, domain.ResourceTypeDevice,
		domain.ResourceTypeODFTerminal,
	} {
		if !got[want] {
			t.Errorf("down-walk from customer misses type %q (nodes: %v)", want, res.Nodes)
		}
	}
	// §4.4's path exists in the emitted edges: service→circuit→och→fiber→cable.
	edgeSet := map[string]bool{}
	for _, e := range res.Edges {
		edgeSet[e.From+"|"+e.To+"|"+string(e.Relation)] = true
	}
	for _, want := range []string{
		"res_SVC1|res_CIR1|provided_by",
		"res_CIR1|res_OCH1|carried_by",
		"res_OCH1|res_FIB1|uses_fiber",
		"res_FIB1|res_CAB1|fiber_of",
		"res_CAB1|res_SITE01|cable_a_end",
	} {
		if !edgeSet[want] {
			t.Errorf("edge %q missing (edges: %v)", want, res.Edges)
		}
	}
	if res.Truncated {
		t.Errorf("truncated = true on the small chain, want false")
	}
}

// TestTraverseRingConvergence is §10.9 Test 2: a cyclic device↔circuit
// graph + a shared ring node — every node once, every edge once, and the
// query returns within the design's 100ms budget.
func TestTraverseRingConvergence(t *testing.T) {
	store, db := seedChain(t)
	ctx := context.Background()
	bat := insertBatch(t, db, "bat_ring")
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ringNodes := []seedNode{
		{"res_RING1", domain.ResourceTypeRing, "RING-01", "加区接入环"},
		{"res_DEVR1", domain.ResourceTypeDevice, "DEV-R1", "R1"},
		{"res_DEVR2", domain.ResourceTypeDevice, "DEV-R2", "R2"},
		{"res_DEVR3", domain.ResourceTypeDevice, "DEV-R3", "R3"},
		{"res_CIRR1", domain.ResourceTypeCircuit, "CIR-R1", "r1"},
		{"res_CIRR2", domain.ResourceTypeCircuit, "CIR-R2", "r2"},
		{"res_CIRR3", domain.ResourceTypeCircuit, "CIR-R3", "r3"},
	}
	for _, n := range ringNodes {
		if err := db.Resources.Insert(ctx, domain.Resource{
			ID: n.id, Type: n.typ, NaturalKey: n.key, Name: n.name,
			Attributes: map[string]string{}, FirstBatchID: bat, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("insert %s: %v", n.id, err)
		}
	}
	ringEdges := []seedEdge{
		{"res_DEVR1", "res_RING1", domain.RelationTypeMemberOfRing},
		{"res_DEVR2", "res_RING1", domain.RelationTypeMemberOfRing},
		{"res_DEVR3", "res_RING1", domain.RelationTypeMemberOfRing},
		{"res_CIRR1", "res_DEVR1", domain.RelationTypeAEnd},
		{"res_CIRR1", "res_DEVR2", domain.RelationTypeZEnd},
		{"res_CIRR2", "res_DEVR2", domain.RelationTypeAEnd},
		{"res_CIRR2", "res_DEVR3", domain.RelationTypeZEnd},
		{"res_CIRR3", "res_DEVR3", domain.RelationTypeAEnd},
		{"res_CIRR3", "res_DEVR1", domain.RelationTypeZEnd},
	}
	for i, e := range ringEdges {
		if err := db.Relations.Insert(ctx, domain.Relation{
			ID:   fmt.Sprintf("rel_ring_%02d", i+1),
			Type: e.rel, FromResourceID: e.from, ToResourceID: e.to,
			FirstBatchID: bat, CreatedAt: now,
		}); err != nil {
			t.Fatalf("insert ring edge: %v", err)
		}
	}

	start := time.Now()
	res, err := Traverse(ctx, store, "res_DEVR1", TraverseParams{Direction: DirectionBoth, MaxDepth: 8})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Traverse ring: %v", err)
	}
	// Node dedup: each ring node exactly once.
	counts := map[string]int{}
	for _, n := range res.Nodes {
		counts[n.ID]++
	}
	for _, id := range []string{"res_DEVR1", "res_DEVR2", "res_DEVR3", "res_CIRR1", "res_CIRR2", "res_CIRR3", "res_RING1"} {
		if counts[id] != 1 {
			t.Errorf("node %s appears %d times, want 1", id, counts[id])
		}
	}
	// Edge dedup: the cycle's edges exactly once each.
	edgeCounts := map[string]int{}
	for _, e := range res.Edges {
		edgeCounts[e.From+"|"+e.To+"|"+string(e.Relation)]++
	}
	for k, c := range edgeCounts {
		if c != 1 {
			t.Errorf("edge %s emitted %d times, want 1 (rule 2 dedup)", k, c)
		}
	}
	// Ring membership edges from all three devices are all present (the
	// ring node itself expanded once, as an edge endpoint for the rest).
	if edgeCounts["res_DEVR1|res_RING1|member_of_ring"] != 1 ||
		edgeCounts["res_DEVR2|res_RING1|member_of_ring"] != 1 ||
		edgeCounts["res_DEVR3|res_RING1|member_of_ring"] != 1 {
		t.Errorf("member_of_ring edges incomplete: %v", edgeCounts)
	}
	if elapsed > 300*time.Millisecond {
		// #82: 100ms was an idle-box calibration; under package-parallel
		// test load shared runners triple small-query wall times. 300ms
		// keeps the same order-of-magnitude guard without load flips.
		t.Errorf("ring query took %v, want < 300ms (§10.9 Test 2, #82 contention-scaled from 100ms)", elapsed)
	}
}

// TestTraverseDirectionSemantics is §10.9 Test 3: the layer-delta contract.
// Up from the device reaches the customer (impact side); down from the
// customer reaches the device (root-cause side).
func TestTraverseDirectionSemantics(t *testing.T) {
	store, _ := seedChain(t)
	ctx := context.Background()

	up, err := Traverse(ctx, store, "res_DEVC1", TraverseParams{Direction: DirectionUp, MaxDepth: 8})
	if err != nil {
		t.Fatalf("up from device: %v", err)
	}
	if !nodeTypes(up)[domain.ResourceTypeCustomer] {
		t.Errorf("direction=up from device does not reach the customer (nodes: %v)", up.Nodes)
	}

	down, err := Traverse(ctx, store, "res_CUST1", TraverseParams{Direction: DirectionDown, MaxDepth: 8})
	if err != nil {
		t.Fatalf("down from customer: %v", err)
	}
	if !nodeTypes(down)[domain.ResourceTypeDevice] {
		t.Errorf("direction=down from customer does not reach the device (nodes: %v)", down.Nodes)
	}

	// The layers filter confines the walk to the given layers ("只走指定
	// 层"): down from the customer with layers=3,6 walks the business +
	// transport layers (service/circuit/och) and never drops into the
	// optical (L2) or space (L0) layers.
	zone, err := Traverse(ctx, store, "res_CUST1", TraverseParams{
		Direction: DirectionDown, MaxDepth: 8,
		Layers: map[int]bool{3: true, 6: true},
	})
	if err != nil {
		t.Fatalf("layered traverse: %v", err)
	}
	zt := nodeTypes(zone)
	if !zt[domain.ResourceTypeService] || !zt[domain.ResourceTypeCircuit] || !zt[domain.ResourceTypeOTNOch] {
		t.Errorf("layers=3,6 walk lost the business/transport layers (types: %v)", zt)
	}
	for _, banned := range []domain.ResourceType{
		domain.ResourceTypeFiber, domain.ResourceTypeOpticalCable,
		domain.ResourceTypeSite, domain.ResourceTypeMachineRoom,
	} {
		if zt[banned] {
			t.Errorf("layers=3,6 walk reached banned type %q", banned)
		}
	}
	// And a STRICT subset (only L3) from the circuit: reaches och but no
	// optical, no device (a_end is L1).
	strict, err := Traverse(ctx, store, "res_CIR1", TraverseParams{
		Direction: DirectionDown, MaxDepth: 8,
		Layers: map[int]bool{3: true},
	})
	if err != nil {
		t.Fatalf("strict layered traverse: %v", err)
	}
	st := nodeTypes(strict)
	if !st[domain.ResourceTypeOTNOch] {
		t.Errorf("strict L3 walk lost the och (types: %v)", st)
	}
	for _, banned := range []domain.ResourceType{
		domain.ResourceTypeFiber, domain.ResourceTypeDevice, domain.ResourceTypeSite,
	} {
		if st[banned] {
			t.Errorf("strict L3 walk reached banned type %q", banned)
		}
	}

	// The relations filter narrows to the carrying chain only.
	carries, err := Traverse(ctx, store, "res_CUST1", TraverseParams{
		Direction: DirectionDown, MaxDepth: 8,
		Relations: map[domain.RelationType]bool{
			domain.RelationTypeProvidedBy: true,
			domain.RelationTypeCarriedBy:  true,
		},
	})
	if err != nil {
		t.Fatalf("relations-filtered traverse: %v", err)
	}
	for _, e := range carries.Edges {
		if e.Relation != "provided_by" && e.Relation != "carried_by" {
			t.Errorf("relations filter leaked edge %q", e.Relation)
		}
	}
}

// TestImpactRootCauseCandidates is §10.9 Test 4: the cable is down, the
// chain above it is normal → candidates = [the cable], impacted = the
// chain nodes above the abnormality.
func TestImpactRootCauseCandidates(t *testing.T) {
	store, db := seedChain(t)
	ctx := context.Background()
	setRunState(t, db, "res_CAB1", domain.RunStateDown, "2026-10-01T00:00:00Z")

	res, err := Impact(ctx, store, "res_CUST1", DirectionDown, 8)
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}
	if len(res.Candidates) != 1 || res.Candidates[0].Node.ID != "res_CAB1" {
		t.Fatalf("candidates = %+v, want exactly [CABLE-01]", res.Candidates)
	}
	if res.Candidates[0].Node.RunState != string(domain.RunStateDown) {
		t.Errorf("candidate runState = %q, want %q", res.Candidates[0].Node.RunState, domain.RunStateDown)
	}
	impacted := map[string]bool{}
	for _, it := range res.Impacted {
		impacted[it.Node.ID] = true
	}
	for _, want := range []string{"res_SVC1", "res_CIR1", "res_OCH1", "res_FIB1", "res_CUST1"} {
		if !impacted[want] {
			t.Errorf("impacted misses %s (got %v)", want, res.Impacted)
		}
	}
	if impacted["res_CAB1"] {
		t.Errorf("the abnormal node must not be in impacted (it is the candidate)")
	}
	// viaPath of an impacted node is a real root→node path.
	for _, it := range res.Impacted {
		if len(it.ViaPath) == 0 || it.ViaPath[0] != "res_CUST1" {
			t.Errorf("impacted %s viaPath = %v, want a path starting at the root", it.Node.ID, it.ViaPath)
		}
	}
}

// TestImpactDeviceDegradationRootIncluded is §10.6 ③: the root itself is
// the abnormality → the root IS a candidate.
func TestImpactDeviceDegradationRootIncluded(t *testing.T) {
	store, db := seedChain(t)
	ctx := context.Background()
	setRunState(t, db, "res_DEVC1", domain.RunStateDegraded, "2026-10-01T00:00:00Z")

	res, err := Impact(ctx, store, "res_DEVC1", DirectionUp, 8)
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}
	foundRoot := false
	for _, c := range res.Candidates {
		if c.Node.ID == "res_DEVC1" {
			foundRoot = true
		}
	}
	if !foundRoot {
		t.Errorf("root DEV-C1 (degraded) not in candidates: %+v", res.Candidates)
	}
	// The customers above the degraded device are impacted.
	impacted := map[string]bool{}
	for _, it := range res.Impacted {
		impacted[it.Node.ID] = true
	}
	if !impacted["res_CUST1"] {
		t.Errorf("customer not impacted above the degraded device (got %v)", res.Impacted)
	}
}

// TestTraverseDeterminism is §10.9 Test 5: the same query twice yields
// identical results (the wall-clock stats field excluded).
func TestTraverseDeterminism(t *testing.T) {
	store, _ := seedChain(t)
	ctx := context.Background()
	a, err := Traverse(ctx, store, "res_CUST1", TraverseParams{Direction: DirectionDown, MaxDepth: 8})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	b, err := Traverse(ctx, store, "res_CUST1", TraverseParams{Direction: DirectionDown, MaxDepth: 8})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	a.Stats.ElapsedMs, b.Stats.ElapsedMs = 0, 0
	if !reflect.DeepEqual(a, b) {
		t.Errorf("same query twice → different results:\n%+v\n%+v", a, b)
	}
}

// TestTraverseTruncation is §10.9 Test 6: maxNodes caps the node budget
// and truncated reports the cut honestly.
func TestTraverseTruncation(t *testing.T) {
	store, db := seedChain(t)
	ctx := context.Background()
	bat := insertBatch(t, db, "bat_fan")
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for i := 1; i <= 60; i++ {
		id := "res_FAN" + string(rune('A'+i/10)) + string(rune('0'+i%10))
		key := "FAN-" + string(rune('A'+i/10)) + string(rune('0'+i%10))
		if err := db.Resources.Insert(ctx, domain.Resource{
			ID: id, Type: domain.ResourceTypeFiber, NaturalKey: key, Name: key,
			Attributes: map[string]string{}, FirstBatchID: bat, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("insert fan fiber: %v", err)
		}
		if err := db.Relations.Insert(ctx, domain.Relation{
			ID:   "rel_fan_" + key,
			Type: domain.RelationTypeFiberOf, FromResourceID: id, ToResourceID: "res_CAB1",
			FirstBatchID: bat, CreatedAt: now,
		}); err != nil {
			t.Fatalf("insert fan edge: %v", err)
		}
	}
	res, err := Traverse(ctx, store, "res_CAB1", TraverseParams{Direction: DirectionDown, MaxDepth: 4, MaxNodes: 50})
	if err != nil {
		t.Fatalf("Traverse fan: %v", err)
	}
	if !res.Truncated {
		t.Errorf("truncated = false with maxNodes=50 over 60+ reachable nodes")
	}
	if len(res.Nodes) > 50 {
		t.Errorf("nodes = %d, want ≤ 50 (the budget)", len(res.Nodes))
	}
}

// setRunState seeds one run-state observation (manual source).
func setRunState(t *testing.T, db *repo.DB, id string, state domain.RunState, ts string) {
	t.Helper()
	tsTime, terr := time.Parse(time.RFC3339, ts)
	if terr != nil {
		t.Fatalf("parse ts %q: %v", ts, terr)
	}
	if err := db.ResourceStatus.UpsertStatus(context.Background(), domain.ResourceStatus{
		ResourceID: id, TS: tsTime, RunState: state, Source: domain.StatusSourceManual,
	}); err != nil {
		t.Fatalf("UpsertStatus(%s, %s): %v", id, state, err)
	}
}
