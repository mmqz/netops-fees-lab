// Direct unit tests of the query service behaviors not already covered by
// the HTTP-level tests: the relations depth semantics (which nodes join the
// neighborhood at depth 1 vs 2), depth validation, unknown-id errors and the
// topology reachability check over the seeded fixture database.
package query

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/importer"
	"github.com/mmqz/netops-lab/internal/repo"
	"github.com/mmqz/netops-lab/internal/topology"
)

// newSeededService imports the three seeded workbooks (canonical order) into
// a fresh database and returns a Service over it.
func newSeededService(t *testing.T) (*Service, *repo.DB) {
	t.Helper()
	db, err := repo.Open(filepath.Join(t.TempDir(), "netops.db"))
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
		a, err := svc.Analyze(ctx, filepath.Join("..", "..", "testdata", file))
		if err != nil {
			t.Fatalf("Analyze(%s): %v", file, err)
		}
		if _, err := svc.Execute(ctx, a.BatchID, a.SuggestedMapping); err != nil {
			t.Fatalf("Execute(%s): %v", file, err)
		}
	}
	return New(db), db
}

// deviceID resolves a device's resource id by its natural key.
func deviceID(t *testing.T, db *repo.DB, code string) string {
	t.Helper()
	r, err := db.Resources.GetByTypeKey(context.Background(), domain.ResourceTypeDevice, code)
	if err != nil {
		t.Fatalf("GetByTypeKey(%s): %v", code, err)
	}
	return r.ID
}

// keysOf collects the natural keys of graph nodes.
func keysOf(nodes []topology.Node) map[string]bool {
	keys := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		keys[n.NaturalKey] = true
	}
	return keys
}

// TestRelationsDepth pins the depth semantics: entering a device costs one
// hop; circuits and pools hang off their anchor device for free. DEV-001's
// depth-1 neighborhood therefore holds DEV-002 plus CKT-001 and POOL-01;
// depth 2 adds DEV-003 with its circuit; depth 3 is refused.
func TestRelationsDepth(t *testing.T) {
	svc, db := newSeededService(t)
	ctx := context.Background()
	dev001 := deviceID(t, db, "DEV-001")

	// Depth validation.
	for _, bad := range []int{0, 3, -1} {
		if _, err := svc.Relations(ctx, dev001, bad); err == nil {
			t.Errorf("Relations(depth %d) succeeded, want an error", bad)
		}
	}

	// Unknown resource id → domain.ErrNotFound.
	if _, err := svc.Relations(ctx, "res_missing", 1); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Relations(unknown id) = %v, want domain.ErrNotFound", err)
	}

	r1, err := svc.Relations(ctx, dev001, 1)
	if err != nil {
		t.Fatalf("Relations depth 1: %v", err)
	}
	if r1.Center.NaturalKey != "DEV-001" || r1.Center.ID != dev001 {
		t.Errorf("depth 1: center = %+v, want DEV-001", r1.Center)
	}
	keys1 := keysOf(r1.Nodes)
	for _, want := range []string{"DEV-002", "CKT-001", "POOL-01"} {
		if !keys1[want] {
			t.Errorf("depth 1: %s missing from DEV-001 neighborhood: %v", want, keys1)
		}
	}
	if keys1["DEV-003"] {
		t.Errorf("depth 1: DEV-003 present (needs 2 device hops)")
	}
	if len(r1.Edges) == 0 {
		t.Error("depth 1: no edges in the subgraph")
	}
	// Every edge's endpoints must be nodes of the visited subgraph (center
	// included). DEV-002's own attachments (CKT-002, POOL-02) are free-hop
	// members of the depth-1 visited set.
	visited := map[string]bool{r1.Center.ID: true}
	for _, n := range r1.Nodes {
		visited[n.ID] = true
	}
	for _, e := range r1.Edges {
		if !visited[e.FromID] || !visited[e.ToID] {
			t.Errorf("depth 1: edge %+v reaches outside the neighborhood", e)
		}
	}

	r2, err := svc.Relations(ctx, dev001, 2)
	if err != nil {
		t.Fatalf("Relations depth 2: %v", err)
	}
	keys2 := keysOf(r2.Nodes)
	for _, want := range []string{"DEV-002", "DEV-003", "CKT-001", "CKT-002", "POOL-01", "POOL-02"} {
		if !keys2[want] {
			t.Errorf("depth 2: %s missing from DEV-001 neighborhood: %v", want, keys2)
		}
	}
	if keys2["DEV-004"] {
		t.Errorf("depth 2: DEV-004 present (needs 3 device hops)")
	}
	if len(r2.Nodes) <= len(r1.Nodes) || len(r2.Edges) <= len(r1.Edges) {
		t.Errorf("depth 2 did not grow the neighborhood: nodes %d→%d, edges %d→%d",
			len(r1.Nodes), len(r2.Nodes), len(r1.Edges), len(r2.Edges))
	}
}

// TestTopologyCheck covers the reachability check: the seeded 2-hop path
// DEV-001→DEV-003, non-reachability beyond the hop budget, the trivial
// self-path and the unknown-device error mapping to domain.ErrNotFound.
func TestTopologyCheck(t *testing.T) {
	svc, _ := newSeededService(t)
	ctx := context.Background()

	res, err := svc.TopologyCheck(ctx, "DEV-001", "DEV-003", 2)
	if err != nil {
		t.Fatalf("TopologyCheck DEV-001→DEV-003: %v", err)
	}
	if !res.Reachable || res.PathLength != 2 || len(res.Hops) != 3 {
		t.Fatalf("DEV-001→DEV-003: reachable=%v pathLength=%d hops=%d, want true/2/3",
			res.Reachable, res.PathLength, len(res.Hops))
	}
	wantPath := []string{"DEV-001", "DEV-002", "DEV-003"}
	for i, hop := range res.Hops {
		if hop.DeviceCode != wantPath[i] {
			t.Errorf("hops[%d] = %s, want %s", i, hop.DeviceCode, wantPath[i])
		}
	}

	// DEV-021 sits 20 hops down the chain: reachable with a large budget.
	res, err = svc.TopologyCheck(ctx, "DEV-001", "DEV-021", 20)
	if err != nil || !res.Reachable || res.PathLength != 20 {
		t.Errorf("DEV-001→DEV-021 maxHops=20: reachable=%v pathLength=%d err=%v, want true/20",
			res.Reachable, res.PathLength, err)
	}

	// DEV-025 has no circuit edge: not reachable within 2 hops.
	res, err = svc.TopologyCheck(ctx, "DEV-001", "DEV-025", 2)
	if err != nil {
		t.Fatalf("TopologyCheck DEV-001→DEV-025: %v", err)
	}
	if res.Reachable || res.PathLength != 0 || len(res.Hops) != 0 {
		t.Errorf("DEV-001→DEV-025: %+v, want unreachable with empty hops", res)
	}

	// Same device: trivially reachable with a single-node path.
	res, err = svc.TopologyCheck(ctx, "DEV-001", "DEV-001", 2)
	if err != nil || !res.Reachable || res.PathLength != 0 || len(res.Hops) != 1 {
		t.Errorf("DEV-001→DEV-001: %+v err=%v, want reachable with 1 hop node", res, err)
	}

	// Unknown device codes → domain.ErrNotFound (the API maps this to 404).
	for _, pair := range [][2]string{{"DEV-001", "DEV-9999"}, {"DEV-9999", "DEV-001"}} {
		if _, err := svc.TopologyCheck(ctx, pair[0], pair[1], 2); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("TopologyCheck(%s→%s) = %v, want domain.ErrNotFound", pair[0], pair[1], err)
		}
	}
}

// TestResourceDetailErrors pins the unknown-id error of the detail view.
func TestResourceDetailErrors(t *testing.T) {
	svc, _ := newSeededService(t)
	if _, err := svc.ResourceDetail(context.Background(), "res_missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ResourceDetail(unknown) = %v, want domain.ErrNotFound", err)
	}
}
