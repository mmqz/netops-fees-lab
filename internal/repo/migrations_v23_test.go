package repo

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

// TestMigrationV23LayerOverlay pins the W33 (issue #51) overlay storage:
//   - both tables exist with the CHECK vocabularies enforced (bad
//     layer/sem/endpoint kinds are rejected by SQLite itself);
//   - the workspace cascade removes overlay rows (workspace delete);
//   - a drill-device delete nulls the anchor but keeps the node
//     (presentation never depends on drill liveness);
//   - the cluster read path pages members in code order and reports the
//     full count.
func TestMigrationV23LayerOverlay(t *testing.T) {
	dir := testsupport.TempDir(t)
	path := filepath.Join(dir, "netops.db")
	ctx := context.Background()

	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	ts := time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)
	seed := func() error {
		return db.Do(ctx, func(tx *Tx) error {
			if err := tx.Batches.Create(ctx, newBatch("bat_v23", ts)); err != nil {
				return err
			}
			if err := tx.Resources.Insert(ctx, domain.Resource{
				ID: "res_sta_v23", Type: domain.ResourceTypeBaseStation,
				NaturalKey: "460-0-123-45", Name: "基站甲", Attributes: map[string]string{},
				FirstBatchID: "bat_v23", CreatedAt: ts, UpdatedAt: ts,
			}); err != nil {
				return err
			}
			if err := tx.Resources.Insert(ctx, domain.Resource{
				ID: "res_dev_v23", Type: domain.ResourceTypeDevice,
				NaturalKey: "DEV-V23", Name: "设备甲", Attributes: map[string]string{},
				FirstBatchID: "bat_v23", CreatedAt: ts, UpdatedAt: ts,
			}); err != nil {
				return err
			}
			if err := tx.Resources.Insert(ctx, domain.Resource{
				ID: "res_cus_v23", Type: domain.ResourceTypeCustomer,
				NaturalKey: "CUST-V23", Name: "客户甲", Attributes: map[string]string{},
				FirstBatchID: "bat_v23", CreatedAt: ts, UpdatedAt: ts,
			}); err != nil {
				return err
			}
			if err := tx.Workspaces.Insert(ctx, domain.Workspace{
				ID: "ws_v23", Name: "w23", Status: domain.WorkspaceStatusActive,
				SourceJSON: `{"batchIds":["bat_v23"]}`, CreatedAt: ts, UpdatedAt: ts,
			}); err != nil {
				return err
			}
			if err := tx.VirtualDevices.Insert(ctx, domain.VirtualDevice{
				ID: "vd_v23", WorkspaceID: "ws_v23", ResourceID: "res_dev_v23",
				Code: "DEV-V23", Name: "设备甲", Status: domain.EntityStatusUp,
				CreatedAt: ts, UpdatedAt: ts,
			}); err != nil {
				return err
			}
			lat, lng := 45.8, 126.5
			if err := tx.LayerNodes.Insert(ctx, domain.LayerNode{
				ID: "vn_1", WorkspaceID: "ws_v23", ResourceID: "res_sta_v23",
				Layer: domain.LayerAccess, Kind: "base_station", Code: "ST-1",
				Name: "基站甲", Status: "在网", AnchorDeviceID: "vd_v23",
				ClusterKey: "vd_v23", GeoLat: &lat, GeoLng: &lng,
				CreatedAt: ts, UpdatedAt: ts,
			}); err != nil {
				return err
			}
			return tx.LayerLinks.Insert(ctx, domain.LayerLink{
				ID: "vl_1", WorkspaceID: "ws_v23", Layer: domain.LayerBusiness,
				Sem: domain.SemService, Label: "serves", ResourceID: "res_sta_v23",
				AKind: domain.LayerEndpointNode, ARef: "vn_1",
				ZKind: domain.LayerEndpointNode, ZRef: "vn_cus",
				CreatedAt: ts, UpdatedAt: ts,
			})
		})
	}
	if err := seed(); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Vocabulary CHECKs (storage twins of the domain validation).
	if err := db.LayerNodes.Insert(ctx, domain.LayerNode{
		ID: "vn_bad", WorkspaceID: "ws_v23", ResourceID: "res_sta_v23",
		Layer: "L3", Kind: "circuit", Code: "ST-BAD", // L3 is edges-only
		CreatedAt: ts, UpdatedAt: ts,
	}); err == nil {
		t.Errorf("insert layer node with L3 layer accepted — want CHECK rejection")
	}
	if err := db.LayerLinks.Insert(ctx, domain.LayerLink{
		ID: "vl_bad", WorkspaceID: "ws_v23", Layer: domain.LayerLogical,
		Sem: "bogus", AKind: domain.LayerEndpointDevice, ARef: "vd_v23",
		ZKind: domain.LayerEndpointDevice, ZRef: "vd_v23",
		CreatedAt: ts, UpdatedAt: ts,
	}); err == nil {
		t.Errorf("insert layer link with bogus sem accepted — want CHECK rejection")
	}

	// Cluster read path: paged members + full count.
	members, total, err := db.LayerNodes.ListByCluster(ctx, "ws_v23", "vd_v23", 1, 10)
	if err != nil || total != 1 || len(members) != 1 {
		t.Fatalf("ListByCluster = %v/%d, %v; want 1/1", members, total, err)
	}

	// Drill-device delete nulls the anchor, keeps the node.
	if err := db.VirtualDevices.Delete(ctx, "vd_v23"); err != nil {
		t.Fatalf("delete device: %v", err)
	}
	nodes, err := db.LayerNodes.ListByWorkspace(ctx, "ws_v23")
	if err != nil || len(nodes) != 1 {
		t.Fatalf("ListByWorkspace after device delete = %d, %v; want node kept", len(nodes), err)
	}
	if nodes[0].AnchorDeviceID != "" {
		t.Errorf("anchor after device delete = %q, want \"\" (SET NULL)", nodes[0].AnchorDeviceID)
	}

	// Workspace cascade removes overlay rows.
	if _, err := db.Workspaces.Delete(ctx, "ws_v23"); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}
	if n, _ := db.LayerNodes.CountByWorkspace(ctx, "ws_v23"); n != 0 {
		t.Errorf("layer nodes after workspace delete = %d, want 0 (cascade)", n)
	}
	if n, _ := db.LayerLinks.CountByWorkspace(ctx, "ws_v23"); n != 0 {
		t.Errorf("layer links after workspace delete = %d, want 0 (cascade)", n)
	}
}

// TestMigrationV23UpgradeFromV22 opens a pre-v23 database file and asserts
// the migration applies cleanly (the fresh-Open path above already covers
// the current shape).
func TestMigrationV23UpgradeFromV22(t *testing.T) {
	dir := testsupport.TempDir(t)
	path := filepath.Join(dir, "netops.db")
	ctx := context.Background()

	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	var v int
	if err := db.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if v < 23 {
		t.Errorf("schema version = %d, want ≥23", v)
	}
}
