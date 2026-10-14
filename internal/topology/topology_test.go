package topology

import (
	"context"
	"encoding/json"
	"github.com/mmqz/netops-lab/internal/testsupport"
	"path/filepath"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/importer"
	"github.com/mmqz/netops-lab/internal/repo"
)

// testdataPath returns a committed test artifact relative to the repo root.
func testdataPath(name string) string {
	return filepath.Join("..", "..", "testdata", name)
}

// importAll runs the canonical import order (devices → ippools → circuits)
// with each workbook's suggested mapping and returns the populated DB.
func importAll(t *testing.T) (*repo.DB, *importer.Service) {
	t.Helper()
	db, err := repo.Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close: %v", err)
		}
	})
	svc := importer.New(db)
	ctx := context.Background()
	for _, file := range []string{"devices.xlsx", "ippools.xlsx", "circuits.xlsx"} {
		a, err := svc.Analyze(ctx, testdataPath(file))
		if err != nil {
			t.Fatalf("Analyze(%s): %v", file, err)
		}
		if _, err := svc.Execute(ctx, a.BatchID, a.SuggestedMapping); err != nil {
			t.Fatalf("Execute(%s): %v", file, err)
		}
	}
	return db, svc
}

// buildGraph loads all resources and relations of db and builds the graph.
func buildGraph(t *testing.T, db *repo.DB) *Graph {
	t.Helper()
	ctx := context.Background()
	resources, _, err := db.Resources.Search(ctx, repo.ResourceSearchFilter{})
	if err != nil {
		t.Fatalf("search resources: %v", err)
	}
	relations, err := db.Relations.ListAll(ctx)
	if err != nil {
		t.Fatalf("list relations: %v", err)
	}
	return Build(ctx, resources, relations)
}

// nodeCode returns the natural keys of nodes in order.
func nodeCodes(nodes []Node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.NaturalKey
	}
	return out
}

// edgeKeys returns "type/from→to" descriptions of edges in order.
func edgeKeys(g *Graph, edges []Edge) []string {
	out := make([]string, len(edges))
	for i, e := range edges {
		out[i] = string(e.Type) + "/" + g.keyOf[e.FromID] + "->" + g.keyOf[e.ToID]
	}
	return out
}

// TestNeighborsDepth1FromImportedFixtures pins the DEV-001 one-hop device
// neighborhood (network view): the linking circuit and its far device, the
// pool assigned to DEV-001, plus the resources attached to DEV-002 — in
// deterministic (type, natural_key) order.
func TestNeighborsDepth1FromImportedFixtures(t *testing.T) {
	db, _ := importAll(t)
	g := buildGraph(t, db)
	dev001, ok := g.NodeByKey("device", "DEV-001")
	if !ok {
		t.Fatalf("DEV-001 not in graph")
	}
	nodes, edges, found := g.Neighbors(dev001.ID, 1)
	if !found {
		t.Fatalf("Neighbors(DEV-001, 1): not found")
	}
	// circuit < device < ip_pool: CKT-001, CKT-002 (attached to DEV-002),
	// DEV-002 (one device link away), POOL-01, POOL-02.
	want := []string{"CKT-001", "CKT-002", "DEV-002", "POOL-01", "POOL-02"}
	got := nodeCodes(nodes)
	if len(got) != len(want) {
		t.Fatalf("depth-1 neighbor codes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("depth-1 neighbor codes = %v, want %v", got, want)
		}
	}
	// Subgraph edges among the visited set (CKT-002's z_end edge to DEV-003
	// is outside depth 1 and therefore cut).
	wantEdges := []string{
		"a_end/CKT-001->DEV-001",
		"a_end/CKT-002->DEV-002",
		"assigned_to/POOL-01->DEV-001",
		"assigned_to/POOL-02->DEV-002",
		"z_end/CKT-001->DEV-002",
	}
	gotEdges := edgeKeys(g, edges)
	if len(gotEdges) != len(wantEdges) {
		t.Fatalf("depth-1 edges = %v, want %v", gotEdges, wantEdges)
	}
	for i := range wantEdges {
		if gotEdges[i] != wantEdges[i] {
			t.Fatalf("depth-1 edges = %v, want %v (a_end < assigned_to < z_end)", gotEdges, wantEdges)
		}
	}
}

// TestNeighborsDepth2FromImportedFixtures pins the DEV-001 two-hop device
// neighborhood: DEV-002 (1 device link) AND DEV-003 (2 device links) are
// both visible, plus every circuit and pool attached along the way (the
// task's S0-5 expectations).
func TestNeighborsDepth2FromImportedFixtures(t *testing.T) {
	db, _ := importAll(t)
	g := buildGraph(t, db)
	dev001, _ := g.NodeByKey("device", "DEV-001")
	nodes, edges, found := g.Neighbors(dev001.ID, 2)
	if !found {
		t.Fatalf("Neighbors(DEV-001, 2): not found")
	}
	got := nodeCodes(nodes)
	want := []string{"CKT-001", "CKT-002", "CKT-003", "DEV-002", "DEV-003", "POOL-01", "POOL-02", "POOL-03"}
	if len(got) != len(want) {
		t.Fatalf("depth-2 neighbor codes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("depth-2 neighbor codes = %v, want %v", got, want)
		}
	}
	// DEV-002 (1 hop) and DEV-003 (2 hops) must both be present.
	sawDEV002, sawDEV003 := false, false
	for _, code := range got {
		if code == "DEV-002" {
			sawDEV002 = true
		}
		if code == "DEV-003" {
			sawDEV003 = true
		}
	}
	if !sawDEV002 || !sawDEV003 {
		t.Fatalf("depth-2 neighborhood lacks DEV-002/DEV-003: %v", got)
	}

	// Eight edges among the visited set; deterministic order (type, from, to)
	// — CKT-003's z_end edge to DEV-004 is outside depth 2 and cut.
	wantEdges := []string{
		"a_end/CKT-001->DEV-001",
		"a_end/CKT-002->DEV-002",
		"a_end/CKT-003->DEV-003",
		"assigned_to/POOL-01->DEV-001",
		"assigned_to/POOL-02->DEV-002",
		"assigned_to/POOL-03->DEV-003",
		"z_end/CKT-001->DEV-002",
		"z_end/CKT-002->DEV-003",
	}
	gotEdges := edgeKeys(g, edges)
	if len(gotEdges) != len(wantEdges) {
		t.Fatalf("depth-2 edges = %v, want %v", gotEdges, wantEdges)
	}
	for i := range wantEdges {
		if gotEdges[i] != wantEdges[i] {
			t.Fatalf("depth-2 edges = %v, want %v", gotEdges, wantEdges)
		}
	}
}

// TestNeighborsUnknownID verifies the unknown-resource contract.
func TestNeighborsUnknownID(t *testing.T) {
	db, _ := importAll(t)
	g := buildGraph(t, db)
	if _, _, found := g.Neighbors("res_does_not_exist", 1); found {
		t.Fatalf("Neighbors(unknown): found = true, want false")
	}
}

// TestReachableChainPath pins the deterministic shortest path along the
// circuit chain: DEV-001 → DEV-002 → DEV-003 in exactly 2 hops.
func TestReachableChainPath(t *testing.T) {
	db, _ := importAll(t)
	g := buildGraph(t, db)
	from, _ := g.NodeByKey("device", "DEV-001")
	to, _ := g.NodeByKey("device", "DEV-003")

	reachable, hops, length := g.Reachable(from.ID, to.ID, 2)
	if !reachable {
		t.Fatalf("DEV-001 → DEV-003 (max 2): not reachable, want reachable")
	}
	if length != 2 {
		t.Errorf("pathLength = %d, want 2", length)
	}
	got := nodeCodes(hops)
	want := []string{"DEV-001", "DEV-002", "DEV-003"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("path = %v, want %v", got, want)
	}

	// 1 hop is not enough for DEV-001 → DEV-003.
	reachable, hops, length = g.Reachable(from.ID, to.ID, 1)
	if reachable || hops != nil || length != 0 {
		t.Errorf("DEV-001 → DEV-003 (max 1): reachable=%v hops=%v length=%d, want false/nil/0", reachable, hops, length)
	}
}

// TestReachableNotReachableAcrossComponents: DEV-025 lives in the
// {POOL-10, DEV-025} component (its only relation is the pool assignment),
// which the circuit chain never touches — DEV-001 cannot reach it within 2
// hops (nor at all).
func TestReachableNotReachableAcrossComponents(t *testing.T) {
	db, _ := importAll(t)
	g := buildGraph(t, db)
	from, _ := g.NodeByKey("device", "DEV-001")
	to, _ := g.NodeByKey("device", "DEV-025")

	reachable, hops, length := g.Reachable(from.ID, to.ID, 2)
	if reachable || hops != nil || length != 0 {
		t.Fatalf("DEV-001 → DEV-025 (max 2): reachable=%v hops=%v length=%d, want false/nil/0", reachable, hops, length)
	}
	// Even a generous bound stays false: separate connected component.
	if reachable, _, _ := g.Reachable(from.ID, to.ID, 64); reachable {
		t.Fatalf("DEV-001 → DEV-025 (max 64): reachable, want false (separate component)")
	}
}

// TestReachableChainEndAndSelf: the chain end and the from == to case.
func TestReachableChainEndAndSelf(t *testing.T) {
	db, _ := importAll(t)
	g := buildGraph(t, db)
	from, _ := g.NodeByKey("device", "DEV-001")
	end, _ := g.NodeByKey("device", "DEV-021")

	// DEV-001…DEV-021 form a 20-edge chain.
	reachable, _, length := g.Reachable(from.ID, end.ID, 20)
	if !reachable || length != 20 {
		t.Fatalf("DEV-001 → DEV-021 (max 20): reachable=%v length=%d, want true/20", reachable, length)
	}
	// One hop short fails.
	if reachable, _, _ := g.Reachable(from.ID, end.ID, 19); reachable {
		t.Fatalf("DEV-001 → DEV-021 (max 19): reachable, want false")
	}

	// from == to: reachable with a single-node, zero-edge path.
	selfReachable, selfHops, selfLength := g.Reachable(from.ID, from.ID, 2)
	if !selfReachable || selfLength != 0 || len(selfHops) != 1 || selfHops[0].NaturalKey != "DEV-001" {
		t.Fatalf("DEV-001 → DEV-001: reachable=%v hops=%v length=%d, want true/[DEV-001]/0", selfReachable, selfHops, selfLength)
	}
}

// TestNeighborsDeterministicOrdering: two independently built graphs of the
// same database produce byte-identical neighbor subgraph JSON.
func TestNeighborsDeterministicOrdering(t *testing.T) {
	db, _ := importAll(t)
	g1 := buildGraph(t, db)
	g2 := buildGraph(t, db)
	dev001, _ := g1.NodeByKey("device", "DEV-001")

	n1, e1, _ := g1.Neighbors(dev001.ID, 2)
	n2, e2, _ := g2.Neighbors(dev001.ID, 2)
	b1, err := json.Marshal(struct {
		Nodes []Node `json:"nodes"`
		Edges []Edge `json:"edges"`
	}{n1, e1})
	if err != nil {
		t.Fatalf("marshal g1: %v", err)
	}
	b2, err := json.Marshal(struct {
		Nodes []Node `json:"nodes"`
		Edges []Edge `json:"edges"`
	}{n2, e2})
	if err != nil {
		t.Fatalf("marshal g2: %v", err)
	}
	if string(b1) != string(b2) {
		t.Fatalf("neighbor subgraphs differ across identical builds:\n%s\n%s", b1, b2)
	}
}

// TestBuildSkipsDanglingRelations guards the defensive branch of Build:
// relations whose endpoints are not part of the resource list are dropped.
func TestBuildSkipsDanglingRelations(t *testing.T) {
	db, _ := importAll(t)
	ctx := context.Background()
	resources, _, err := db.Resources.Search(ctx, repo.ResourceSearchFilter{})
	if err != nil {
		t.Fatalf("search resources: %v", err)
	}
	relations, err := db.Relations.ListAll(ctx)
	if err != nil {
		t.Fatalf("list relations: %v", err)
	}
	relations = append(relations, domain.Relation{
		ID: "rel_ghost", Type: domain.RelationTypeAEnd,
		FromResourceID: "res_missing_a", ToResourceID: "res_missing_b",
	})

	g := Build(ctx, resources, relations)
	if len(g.edges) != len(relations)-1 {
		t.Fatalf("graph edges = %d, want %d (dangling relation dropped)", len(g.edges), len(relations)-1)
	}
	if _, _, found := g.Neighbors("res_missing_a", 1); found {
		t.Fatalf("dangling endpoint must not be a graph node")
	}
}
