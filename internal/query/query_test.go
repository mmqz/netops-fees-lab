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
	"time"

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

// TestSearchResourcesWithSource pins the W9-D batched provenance resolution
// the MCP resource_search tool now consumes: imported resources resolve
// their CREATING source (FirstBatchID match), a resource whose recorded refs
// never match FirstBatchID falls back to the earliest ref (rowid version
// order), a resource without any ref is application-generated, and the
// limit/offset + q/type filters page exactly like the repo search underneath.
func TestSearchResourcesWithSource(t *testing.T) {
	svc, db := newSeededService(t)
	ctx := context.Background()

	// The seeded database: 27 devices + 16 pools + 21 circuits, all imported
	// → every item carries its creating source.
	items, total, err := svc.SearchResourcesWithSource(ctx, SourceSearchFilter{Limit: 100})
	if err != nil {
		t.Fatalf("SearchResourcesWithSource: %v", err)
	}
	if total != 64 || len(items) != 64 {
		t.Fatalf("search all = %d items (total %d), want 64/64", len(items), total)
	}
	workbookByType := map[string]string{
		"device": "devices.xlsx", "ip_pool": "ippools.xlsx", "circuit": "circuits.xlsx",
	}
	for _, it := range items {
		if it.Source == nil || it.ApplicationGenerated {
			t.Errorf("imported resource %s has no resolved source (applicationGenerated=%v)", it.NaturalKey, it.ApplicationGenerated)
			continue
		}
		if want := workbookByType[it.Type]; it.Source.Workbook != want {
			t.Errorf("source of %s = %s, want the creating workbook %s", it.NaturalKey, it.Source.Workbook, want)
		}
		if it.Source.RowNumber < 2 {
			t.Errorf("source of %s has rowNumber %d, want a 1-based Excel row (≥2)", it.NaturalKey, it.Source.RowNumber)
		}
	}

	// Type + limit/offset: repo ordering (resource_type, natural_key) makes
	// DEV-001 the first device; the total ignores the window.
	items, total, err = svc.SearchResourcesWithSource(ctx, SourceSearchFilter{Type: domain.ResourceTypeDevice, Limit: 5})
	if err != nil || total != 27 || len(items) != 5 {
		t.Fatalf("typed search = %d items (total %d, err %v), want 5/27", len(items), total, err)
	}
	if items[0].NaturalKey != "DEV-001" || items[0].Type != "device" {
		t.Errorf("typed search items[0] = %s/%s, want DEV-001/device", items[0].NaturalKey, items[0].Type)
	}
	if items[0].Source == nil || items[0].Source.Sheet == "" {
		t.Errorf("typed search items[0].source = %+v, want the resolved creating ref", items[0].Source)
	}
	items, total, err = svc.SearchResourcesWithSource(ctx, SourceSearchFilter{Type: domain.ResourceTypeDevice, Limit: 100, Offset: 25})
	if err != nil || total != 27 || len(items) != 2 {
		t.Fatalf("offset search = %d items (total %d, err %v), want the last 2 of 27", len(items), total, err)
	}

	// q narrows like the repo contains match (DEV-001 also appears in the
	// circuits' a_end attributes, so the typed query isolates the device).
	items, total, err = svc.SearchResourcesWithSource(ctx, SourceSearchFilter{Type: domain.ResourceTypeDevice, Q: "DEV-001"})
	if err != nil || total != 1 || len(items) != 1 || items[0].NaturalKey != "DEV-001" {
		t.Fatalf("q search = %d items (total %d, err %v), want exactly DEV-001", len(items), total, err)
	}
	if items[0].Source == nil || items[0].Source.Workbook != "devices.xlsx" {
		t.Errorf("q search source = %+v, want the devices.xlsx creating ref", items[0].Source)
	}

	// Fallback + application-generated semantics, built directly on the repo
	// so the states are unambiguous:
	//   res_fb  — refs exist but none matches FirstBatchID → earliest ref;
	//   res_gen — no refs at all → applicationGenerated.
	ts := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	if err := db.Batches.Create(ctx, domain.ImportBatch{
		ID: "bat_fb", WorkbookName: "b.xlsx", WorkbookSHA256: "abc123",
		Status: domain.BatchStatusCompleted, Mapping: "{}", RuleVersion: domain.RuleVersion, StartedAt: ts,
	}); err != nil {
		t.Fatalf("Create bat_fb: %v", err)
	}
	if err := db.Batches.Create(ctx, domain.ImportBatch{
		ID: "bat_v1", WorkbookName: "b.xlsx", WorkbookSHA256: "abc123",
		Status: domain.BatchStatusCompleted, Mapping: "{}", RuleVersion: domain.RuleVersion, StartedAt: ts,
	}); err != nil {
		t.Fatalf("Create bat_v1: %v", err)
	}
	if err := db.Batches.Create(ctx, domain.ImportBatch{
		ID: "bat_gen", WorkbookName: "b.xlsx", WorkbookSHA256: "abc123",
		Status: domain.BatchStatusCompleted, Mapping: "{}", RuleVersion: domain.RuleVersion, StartedAt: ts,
	}); err != nil {
		t.Fatalf("Create bat_gen: %v", err)
	}
	fbRes := domain.Resource{
		ID: "res_fb", Type: domain.ResourceTypeDevice, NaturalKey: "DEV-FB", Name: "DEV-FB",
		FirstBatchID: "bat_fb", CreatedAt: ts, UpdatedAt: ts, Attributes: map[string]string{},
	}
	genRes := domain.Resource{
		ID: "res_gen", Type: domain.ResourceTypeDevice, NaturalKey: "DEV-GEN", Name: "DEV-GEN",
		FirstBatchID: "bat_gen", CreatedAt: ts, UpdatedAt: ts, Attributes: map[string]string{},
	}
	for _, res := range []domain.Resource{fbRes, genRes} {
		if err := db.Resources.Insert(ctx, res); err != nil {
			t.Fatalf("Insert %s: %v", res.ID, err)
		}
	}
	// res_fb's refs live under OTHER batches (its creating batch recorded
	// nothing) — the earliest (rowid) ref wins the fallback.
	if err := db.Batches.Create(ctx, domain.ImportBatch{
		ID: "bat_fb2", WorkbookName: "b.xlsx", WorkbookSHA256: "abc123",
		Status: domain.BatchStatusCompleted, Mapping: "{}", RuleVersion: domain.RuleVersion, StartedAt: ts,
	}); err != nil {
		t.Fatalf("Create bat_fb2: %v", err)
	}
	for _, tc := range []struct{ id, batch, workbook string }{
		{"src_fb1", "bat_v1", "first.xlsx"}, {"src_fb2", "bat_fb2", "second.xlsx"},
	} {
		if err := db.SourceRefs.Insert(ctx, domain.SourceRef{
			ID: tc.id, EntityKind: domain.EntityKindResource, EntityID: "res_fb", BatchID: tc.batch,
			Workbook: tc.workbook, Sheet: "设备清单", RowNumber: 3, Raw: map[string]string{"设备编码": "DEV-FB"},
		}); err != nil {
			t.Fatalf("Insert %s: %v", tc.id, err)
		}
	}

	items, total, err = svc.SearchResourcesWithSource(ctx, SourceSearchFilter{Q: "DEV-FB"})
	if err != nil || total != 1 || len(items) != 1 {
		t.Fatalf("fallback search = %d items (total %d, err %v), want DEV-FB alone", len(items), total, err)
	}
	if items[0].Source == nil || items[0].Source.Workbook != "first.xlsx" {
		t.Errorf("fallback source = %+v, want the EARLIEST ref (first.xlsx)", items[0].Source)
	}
	if items[0].ApplicationGenerated {
		t.Error("fallback item must not be applicationGenerated (it has refs)")
	}

	items, total, err = svc.SearchResourcesWithSource(ctx, SourceSearchFilter{Q: "DEV-GEN"})
	if err != nil || total != 1 || len(items) != 1 {
		t.Fatalf("appgen search = %d items (total %d, err %v), want DEV-GEN alone", len(items), total, err)
	}
	if items[0].Source != nil || !items[0].ApplicationGenerated {
		t.Errorf("ref-less resource = source %+v applicationGenerated %v, want nil/true", items[0].Source, items[0].ApplicationGenerated)
	}

	// An invalid type is refused (the MCP layer validates first with its own
	// wire-contract text; this guard mirrors SearchResources).
	if _, _, err := svc.SearchResourcesWithSource(ctx, SourceSearchFilter{Type: domain.ResourceType("vlan")}); err == nil {
		t.Error("invalid type succeeded, want an error")
	}
}
