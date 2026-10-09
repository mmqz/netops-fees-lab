package sim

// ACC-P2-1 — workspace materialization: counts, deterministic port
// allocation, status mapping, resource lineage, cross-database
// determinism, and every §6 error path.

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// TestWorkspaceMaterialize covers ACC-P2-1 end to end on the seeded
// 3-batch dataset: 27 devices, 20 links (21 imported circuits minus the
// W003 self-loop), 40 interfaces, deterministic eth<n> port allocation,
// status mapping, lineage and the §6 error cases.
func TestWorkspaceMaterialize(t *testing.T) {
	svc, db, res := seededEngine(t)
	ctx := context.Background()

	if res.Counts.Devices != 27 || res.Counts.Links != 20 || res.Counts.Interfaces != 40 {
		t.Fatalf("materialize counts = %+v, want devices 27 links 20 interfaces 40", res.Counts)
	}
	if res.Name != "lab" || res.WorkspaceID == "" || res.WorkspaceHash == "" {
		t.Fatalf("materialize result incomplete: %+v", res)
	}

	topo := mustTopology(t, svc, res.WorkspaceID)
	if len(topo.Nodes) != 27 || len(topo.Edges) != 20 {
		t.Fatalf("topology: %d nodes / %d edges, want 27 / 20", len(topo.Nodes), len(topo.Edges))
	}
	if topo.WorkspaceHash != res.WorkspaceHash {
		t.Fatalf("topology hash %s != materialize hash %s", topo.WorkspaceHash, res.WorkspaceHash)
	}

	// ---- concrete port allocation (§6) ----
	// CKT-001 (DEV-001→DEV-002) runs first: DEV-001/eth0, DEV-002/eth0.
	// CKT-002 (DEV-002→DEV-003) then takes DEV-002's lowest free port eth1.
	edge1 := findEdgeByLabel(t, topo, "CKT-001")
	if edge1.ADeviceCode != "DEV-001" || edge1.ADeviceID != "vd-1" || edge1.ZDeviceCode != "DEV-002" || edge1.ZDeviceID != "vd-2" {
		t.Fatalf("CKT-001 edge = %+v, want DEV-001(vd-1) → DEV-002(vd-2)", edge1)
	}
	edge2 := findEdgeByLabel(t, topo, "CKT-002")
	if edge2.ADeviceCode != "DEV-002" || edge2.ZDeviceCode != "DEV-003" {
		t.Fatalf("CKT-002 edge = %+v, want DEV-002 → DEV-003", edge2)
	}
	dev2 := findNode(t, topo, "DEV-002") // chain-interior device: ≥2 circuits
	if len(dev2.Interfaces) != 2 || dev2.Interfaces[0].Name != "eth0" || dev2.Interfaces[1].Name != "eth1" {
		t.Fatalf("DEV-002 interfaces = %+v, want [eth0 eth1]", dev2.Interfaces)
	}
	if edge1.ZInterfaceID != dev2.Interfaces[0].ID {
		t.Fatalf("CKT-001 lands on %s, want DEV-002/eth0 (%s)", edge1.ZInterfaceID, dev2.Interfaces[0].ID)
	}
	if edge2.AInterfaceID != dev2.Interfaces[1].ID {
		t.Fatalf("CKT-002 lands on %s, want DEV-002/eth1 (%s)", edge2.AInterfaceID, dev2.Interfaces[1].ID)
	}
	dev1 := findNode(t, topo, "DEV-001") // single-circuit device: exactly eth0
	if len(dev1.Interfaces) != 1 || dev1.Interfaces[0].Name != "eth0" {
		t.Fatalf("DEV-001 interfaces = %+v, want exactly [eth0]", dev1.Interfaces)
	}
	dev21 := findNode(t, topo, "DEV-021")
	if len(dev21.Interfaces) != 1 || dev21.Interfaces[0].Name != "eth0" {
		t.Fatalf("DEV-021 interfaces = %+v, want exactly [eth0]", dev21.Interfaces)
	}

	// All interfaces start up and unconfigured.
	for _, n := range topo.Nodes {
		for _, i := range n.Interfaces {
			if i.Status != "up" || i.IPv4Addr != "" || i.IPv4Prefix != -1 {
				t.Fatalf("interface %s/%s = %+v, want up/unconfigured", n.Code, i.Name, i)
			}
		}
	}

	// ---- status mapping (§6: 在网 → up, everything else → down) ----
	statuses := map[string]string{}
	for _, n := range topo.Nodes {
		statuses[n.Code] = n.Status
	}
	for _, code := range []string{"DEV-001", "DEV-002", "DEV-017", "DEV-022", "DEV-030"} {
		if statuses[code] != "up" {
			t.Errorf("device %s status = %q, want up (在网)", code, statuses[code])
		}
	}
	for _, code := range []string{"DEV-015", "DEV-016", "DEV-019", "DEV-025"} {
		if statuses[code] != "down" {
			t.Errorf("device %s status = %q, want down (退网/停用/在建/未知)", code, statuses[code])
		}
	}
	upDevices, downDevices := 0, 0
	for _, s := range statuses {
		if s == "up" {
			upDevices++
		} else {
			downDevices++
		}
	}
	if upDevices != 23 || downDevices != 4 {
		t.Fatalf("status split = %d up / %d down, want 23 / 4", upDevices, downDevices)
	}

	// ---- resource_id + source_json lineage ----
	d1, err := db.VirtualDevices.GetByCode(ctx, res.WorkspaceID, "DEV-001")
	if err != nil {
		t.Fatalf("GetByCode(DEV-001): %v", err)
	}
	if d1.ResourceID == "" {
		t.Fatalf("device DEV-001 has no resource lineage")
	}
	if srcRes, err := db.Resources.GetByID(ctx, d1.ResourceID); err != nil || srcRes.NaturalKey != "DEV-001" {
		t.Fatalf("device lineage resource: %v (%+v)", err, srcRes)
	}
	ln1, err := db.SimLinks.GetByID(ctx, edge1.ID)
	if err != nil {
		t.Fatalf("GetByID(%s): %v", edge1.ID, err)
	}
	if ln1.ResourceID == "" {
		t.Fatalf("link %s has no resource lineage", edge1.ID)
	}
	if linkRes, err := db.Resources.GetByID(ctx, ln1.ResourceID); err != nil || linkRes.NaturalKey != "CKT-001" {
		t.Fatalf("link lineage resource: %v (%+v)", err, linkRes)
	}
	ws, err := db.Workspaces.GetByID(ctx, res.WorkspaceID)
	if err != nil {
		t.Fatalf("GetByID(workspace): %v", err)
	}
	src, err := ws.Source()
	if err != nil {
		t.Fatalf("workspace source: %v", err)
	}
	if !reflect.DeepEqual(src.BatchIDs, res.SourceBatchIDs) {
		t.Fatalf("source_json batchIds %v != result %v", src.BatchIDs, res.SourceBatchIDs)
	}
	if len(src.BatchIDs) != 3 {
		t.Fatalf("source batch ids = %v, want the 3 imported batches", src.BatchIDs)
	}

	// Materialization is NOT a changeset.
	if csCount, err := db.ChangeSets.CountByWorkspace(ctx, res.WorkspaceID); err != nil || csCount != 0 {
		t.Fatalf("materialize recorded %d changesets, want 0 (materialization is not a changeset): %v", csCount, err)
	}

	// ---- §6 error paths ----
	if _, err := svc.Materialize(ctx, "lab", "", nil); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate name: err = %v, want ErrConflict", err)
	}
	batches := importBatches(t, db)
	if _, err := svc.Materialize(ctx, "no-devices", "", []string{batches[1]}); !errors.Is(err, domain.ErrNoSourceResources) {
		t.Fatalf("ippool-only scope: err = %v, want ErrNoSourceResources", err)
	}
	if _, err := svc.Materialize(ctx, "unknown-batch", "", []string{"b-999"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown batch: err = %v, want ErrNotFound", err)
	}

	// ---- explicit batch subset: devices + circuits only ----
	sub, err := svc.Materialize(ctx, "subset", "", []string{batches[0], batches[2]})
	if err != nil {
		t.Fatalf("Materialize(subset): %v", err)
	}
	if sub.Counts.Devices != 27 || sub.Counts.Links != 20 || sub.Counts.Interfaces != 40 {
		t.Fatalf("subset counts = %+v, want 27/20/40 (ippools must not influence)", sub.Counts)
	}
	if sub.WorkspaceHash != res.WorkspaceHash {
		t.Fatalf("subset hash %s != full-scope hash %s (ippools must not influence the topology)", sub.WorkspaceHash, res.WorkspaceHash)
	}

	// ---- determinism: two fresh databases ----
	svc2, db2 := newEngine(t)
	importWorkbooks(t, db2)
	res2, err := svc2.Materialize(ctx, "lab", "", nil)
	if err != nil {
		t.Fatalf("Materialize(db2): %v", err)
	}
	topo2 := mustTopology(t, svc2, res2.WorkspaceID)
	if !reflect.DeepEqual(topo.Nodes, topo2.Nodes) || !reflect.DeepEqual(topo.Edges, topo2.Edges) {
		t.Fatalf("topology differs across two fresh databases")
	}
	if topo.WorkspaceID == topo2.WorkspaceID {
		t.Fatalf("two materializations must yield two distinct workspace ids")
	}
	if res.WorkspaceHash != res2.WorkspaceHash {
		t.Fatalf("workspace hash differs across two fresh databases: %s vs %s", res.WorkspaceHash, res2.WorkspaceHash)
	}
	// Identical topology JSON modulo the workspace uuid.
	j1, err := json.Marshal(topo)
	if err != nil {
		t.Fatalf("marshal topo: %v", err)
	}
	j2, err := json.Marshal(topo2)
	if err != nil {
		t.Fatalf("marshal topo2: %v", err)
	}
	norm1 := strings.ReplaceAll(string(j1), topo.WorkspaceID, "<ws>")
	norm2 := strings.ReplaceAll(string(j2), topo2.WorkspaceID, "<ws>")
	if norm1 != norm2 {
		t.Fatalf("topology JSON differs across two fresh databases (modulo workspace id)")
	}
}

// importBatches lists the database's import batches in import order
// (Batches.List is newest-first).
func importBatches(t *testing.T, db *repo.DB) []string {
	t.Helper()
	list, _, err := db.Batches.List(context.Background(), -1, 0) // limit<=0 → unlimited
	if err != nil {
		t.Fatalf("list batches: %v", err)
	}
	ids := make([]string, 0, len(list))
	for i := len(list) - 1; i >= 0; i-- {
		ids = append(ids, list[i].ID)
	}
	return ids
}

// TestMaterializeAtomicity proves §6's single-transaction rule by failing
// the materialization after the device writes via the engine's
// afterWrite hook (engine-internal, mirroring the importer's afterSheet):
// nothing persists — no workspace, no devices, and the sequence
// increments of the rolled-back transaction are discarded.
func TestMaterializeAtomicity(t *testing.T) {
	svc, db := newEngine(t)
	batches := importWorkbooks(t, db)
	svc.afterWrite = func(stage string) error {
		if stage == "links" {
			return errors.New("injected failure after devices")
		}
		return nil
	}
	ctx := context.Background()
	if _, err := svc.Materialize(ctx, "broken", "", batches); err == nil {
		t.Fatalf("materialize with injected failure: want error, got nil")
	}
	svc.afterWrite = nil

	if list, total, err := db.Workspaces.List(ctx, 1, 0); err != nil || total != 0 || len(list) != 0 {
		t.Fatalf("workspaces after rollback: %d rows (err %v)", total, err)
	}
	if devs, err := db.VirtualDevices.ListByWorkspace(ctx, brokenWorkspaceID); err != nil || len(devs) != 0 {
		t.Fatalf("virtual devices after rollback: %d rows (err %v)", len(devs), err)
	}
	if n, err := db.Sequences.Value(ctx, domain.SeqDevice); err != nil || n != 0 {
		t.Fatalf("device sequence after rollback = %d, want 0 (allocation rolled back): %v", n, err)
	}

	// The same name is free again — a retry without the hook succeeds.
	res, err := svc.Materialize(ctx, "broken", "", batches)
	if err != nil {
		t.Fatalf("Materialize retry: %v", err)
	}
	if res.Counts.Devices != 27 {
		t.Fatalf("retry counts = %+v, want 27 devices", res.Counts)
	}
}

// brokenWorkspaceID is a non-existent workspace id used to prove the
// rollback left no devices behind (ListByWorkspace of an unknown
// workspace returns empty).
const brokenWorkspaceID = "ws-does-not-exist"

// TestMaterializeNameRequired pins the empty-name guard.
func TestMaterializeNameRequired(t *testing.T) {
	svc, _ := newEngine(t)
	if _, err := svc.Materialize(context.Background(), "", "", nil); !errors.Is(err, domain.ErrInvalidOp) {
		t.Fatalf("empty name: err = %v, want ErrInvalidOp", err)
	}
}

// TestMaterializeNoBatches pins ErrNoSourceResources on an empty database
// (the default scope selects every completed batch — there are none).
func TestMaterializeNoBatches(t *testing.T) {
	svc, _ := newEngine(t)
	if _, err := svc.Materialize(context.Background(), "empty", "", nil); !errors.Is(err, domain.ErrNoSourceResources) {
		t.Fatalf("no batches: err = %v, want ErrNoSourceResources", err)
	}
}

// TestMaterializeCircuitOrderPins the natural_key-ascending circuit
// processing order through the interface ids: CKT-001's two interfaces
// are if-1/if-2, CKT-002's are if-3/if-4, … (a-end first, z-end second).
func TestMaterializeCircuitOrder(t *testing.T) {
	svc, _, res := seededEngine(t)
	topo := mustTopology(t, svc, res.WorkspaceID)
	edges := map[string]TopologyEdge{}
	for _, e := range topo.Edges {
		edges[e.Label] = e
	}
	if len(edges) != 20 {
		t.Fatalf("got %d edges, want 20", len(edges))
	}
	// Circuit numbers CKT-001..CKT-020 exist; the self-loop CKT-021 must
	// NOT have been materialized.
	if _, ok := edges["CKT-021"]; ok {
		t.Fatalf("self-referencing circuit CKT-021 must be skipped")
	}
	ids := make([]string, 0, len(topo.Edges))
	for _, e := range topo.Edges {
		ids = append(ids, e.ID)
	}
	sortedCopy := append([]string(nil), ids...)
	sort.Strings(sortedCopy)
	// ln-1..ln-20 in numeric id order = creation order = natural_key order.
	want := make([]string, 0, 20)
	for i := 1; i <= 20; i++ {
		want = append(want, domain.FormatSimID(domain.SeqLink, i))
	}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("edge ids = %v, want %v (numeric ascending = circuit natural_key order)", ids, want)
	}
	// First circuit's interfaces: a-end then z-end.
	e1 := edges["CKT-001"]
	if e1.AInterfaceID != domain.FormatSimID(domain.SeqInterface, 1) || e1.ZInterfaceID != domain.FormatSimID(domain.SeqInterface, 2) {
		t.Fatalf("CKT-001 interfaces = %s/%s, want if-1/if-2 (a-end first)", e1.AInterfaceID, e1.ZInterfaceID)
	}
}
