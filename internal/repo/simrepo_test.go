package repo

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
)

// simTS is the fixed timestamp used by the simulation repo tests.
var simTS = time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)

// newWorkspace builds a minimal valid workspace for tests.
func newWorkspace(id, name, sourceJSON string) domain.Workspace {
	return domain.Workspace{
		ID:          id,
		Name:        name,
		Description: "测试工作台 " + name,
		Status:      domain.WorkspaceStatusActive,
		SourceJSON:  sourceJSON,
		CreatedAt:   simTS,
		UpdatedAt:   simTS,
	}
}

// newDevice builds a minimal valid virtual device for tests.
func newDevice(id, workspaceID, code string, status domain.EntityStatus) domain.VirtualDevice {
	return domain.VirtualDevice{
		ID:          id,
		WorkspaceID: workspaceID,
		Code:        code,
		Name:        "设备-" + code,
		Status:      status,
		CreatedAt:   simTS,
		UpdatedAt:   simTS,
	}
}

// newIface builds a minimal valid interface for tests. Unconfigured by
// default (addr "" / prefix -1).
func newIface(id, workspaceID, deviceID, name string, status domain.EntityStatus) domain.SimInterface {
	return domain.SimInterface{
		ID:          id,
		WorkspaceID: workspaceID,
		DeviceID:    deviceID,
		Name:        name,
		Status:      status,
		IPv4Addr:    "",
		IPv4Prefix:  -1,
		CreatedAt:   simTS,
		UpdatedAt:   simTS,
	}
}

// newLink builds a minimal valid link for tests.
func newLink(id, workspaceID, aIF, zIF string, status domain.EntityStatus, label string) domain.SimLink {
	return domain.SimLink{
		ID:           id,
		WorkspaceID:  workspaceID,
		AInterfaceID: aIF,
		ZInterfaceID: zIF,
		Status:       status,
		Label:        label,
		CreatedAt:    simTS,
		UpdatedAt:    simTS,
	}
}

// seedWorkspace creates one workspace (in its own transaction) and returns
// it.
func seedWorkspace(t *testing.T, db *DB, id, name, sourceJSON string) domain.Workspace {
	t.Helper()
	ctx := context.Background()
	w := newWorkspace(id, name, sourceJSON)
	err := db.Do(ctx, func(tx *Tx) error { return tx.Workspaces.Insert(ctx, w) })
	if err != nil {
		t.Fatalf("seed workspace %s: %v", id, err)
	}
	return w
}

// simTableCount counts the rows of a simulation table directly.
func simTableCount(t *testing.T, db *DB, table string) int {
	t.Helper()
	var n int
	if err := db.sql.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM `+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// ---------- migrations ----------

// TestMigrationV5UpgradesV4Database verifies that a database left at schema
// v4 is migrated to the CURRENT head (v5 simulation tables + the v6 geo
// columns, since Open always applies every pending version) by Open, and
// that the result is idempotent.
func TestMigrationV5UpgradesV4Database(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "netops.db")
	ctx := context.Background()

	db1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	// Rewind to the pre-v5 state: drop the v5 tables (children first) and
	// the bookkeeping row, emulating a database created by DDL v1–v4.
	for _, stmt := range []string{
		`DROP TABLE checkpoints`,
		`DROP TABLE check_results`,
		`DROP TABLE check_runs`,
		`DROP TABLE changeset_ops`,
		`DROP TABLE changesets`,
		`DROP TABLE links`,
		`DROP TABLE interfaces`,
		`DROP TABLE virtual_devices`,
		`DROP TABLE sequences`,
		`DROP TABLE workspaces`,
		`DELETE FROM schema_migrations WHERE version = 5`,
	} {
		if _, err := db1.sql.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("rewind %q: %v", stmt, err)
		}
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close after rewind: %v", err)
	}

	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open (v4 → current): %v", err)
	}
	defer db2.Close()
	var maxV, count int
	if err := db2.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&maxV); err != nil {
		t.Fatalf("max version: %v", err)
	}
	if maxV != migrations[len(migrations)-1].Version {
		t.Errorf("max schema version after upgrade = %d, want %d", maxV, migrations[len(migrations)-1].Version)
	}
	if err := db2.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if count != len(migrations) {
		t.Errorf("schema_migrations rows = %d, want %d", count, len(migrations))
	}
	// The v5 tables must be usable: insert one workspace end to end.
	w := seedWorkspace(t, db2, "ws_mig", "migration-check", `{"batchIds":[]}`)
	if _, err := db2.Workspaces.GetByName(ctx, w.Name); err != nil {
		t.Fatalf("workspace not usable after v5 upgrade: %v", err)
	}

	// Reopening again must be a no-op (idempotent migration).
	db3, err := Open(path)
	if err != nil {
		t.Fatalf("third Open (idempotency): %v", err)
	}
	defer db3.Close()
	if err := db3.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&maxV); err != nil {
		t.Fatalf("max version after reopen: %v", err)
	}
	if maxV != migrations[len(migrations)-1].Version {
		t.Errorf("max schema version after reopen = %d, want %d", maxV, migrations[len(migrations)-1].Version)
	}
}

// TestMigrationV6UpgradesV5Database verifies the geo column migration: a
// database left at schema v5 (virtual_devices WITHOUT geo_lat/geo_lng, with
// live rows) is migrated to v6 by Open — the two REAL columns are added,
// existing rows read back unchanged with NULL positions, and reopening is
// idempotent (a second ADD COLUMN would fail on the duplicate column, so a
// clean reopen proves the bookkeeping works).
func TestMigrationV6UpgradesV5Database(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "netops.db")
	ctx := context.Background()

	db1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	// Seed one workspace + device so the upgrade runs against live data.
	seedWorkspace(t, db1, "ws_v6", "geo-upgrade", "")
	if err := db1.Do(ctx, func(tx *Tx) error {
		return tx.VirtualDevices.Insert(ctx, newDevice("vd-1", "ws_v6", "DEV-V6", domain.EntityStatusUp))
	}); err != nil {
		t.Fatalf("seed device: %v", err)
	}
	// Rewind to the pre-v6 state: strip the geo columns and the v6
	// bookkeeping row, emulating a database created by DDL v1–v5.
	for _, stmt := range []string{
		`ALTER TABLE virtual_devices DROP COLUMN geo_lat`,
		`ALTER TABLE virtual_devices DROP COLUMN geo_lng`,
		`DELETE FROM schema_migrations WHERE version = 6`,
	} {
		if _, err := db1.sql.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("rewind %q: %v", stmt, err)
		}
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close after rewind: %v", err)
	}

	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open (v5 → v6 → … → v9): %v", err)
	}
	defer db2.Close()
	var maxV int
	if err := db2.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&maxV); err != nil {
		t.Fatalf("max version: %v", err)
	}
	if maxV != 9 {
		t.Errorf("max schema version after upgrade = %d, want 9", maxV)
	}
	// Old data is readable and position-less (NULL → nil pointers).
	d, err := db2.VirtualDevices.GetByCode(ctx, "ws_v6", "DEV-V6")
	if err != nil {
		t.Fatalf("GetByCode after upgrade: %v", err)
	}
	if d.GeoLat != nil || d.GeoLng != nil {
		t.Errorf("device after upgrade = lat %v / lng %v, want nil/nil (old rows stay position-less)", d.GeoLat, d.GeoLng)
	}
	// The new columns are usable: writing a position round-trips.
	d2 := newDevice("vd-2", "ws_v6", "DEV-GEO", domain.EntityStatusUp)
	lat, lng := 46.59, 125.103
	d2.GeoLat, d2.GeoLng = &lat, &lng
	if err := db2.Do(ctx, func(tx *Tx) error { return tx.VirtualDevices.Insert(ctx, d2) }); err != nil {
		t.Fatalf("insert geo device after upgrade: %v", err)
	}
	got, err := db2.VirtualDevices.GetByCode(ctx, "ws_v6", "DEV-GEO")
	if err != nil {
		t.Fatalf("GetByCode(DEV-GEO): %v", err)
	}
	if got.GeoLat == nil || *got.GeoLat != lat || got.GeoLng == nil || *got.GeoLng != lng {
		t.Errorf("geo round-trip after upgrade = %v/%v, want %v/%v", got.GeoLat, got.GeoLng, lat, lng)
	}

	// v7 (port resource type): the rebuilt resources CHECK accepts 'port'.
	// Before v7 the same INSERT failed with a CHECK constraint error — the
	// regression the migration exists to fix. The rebuild also preserved the
	// FK graph: first_batch_id still enforces its reference.
	batchID := "bat_v7_port"
	if err := db2.Do(ctx, func(tx *Tx) error {
		if err := tx.Batches.Create(ctx, newBatch(batchID, simTS)); err != nil {
			return err
		}
		return tx.Resources.Insert(ctx, domain.Resource{
			Type: domain.ResourceTypePort, NaturalKey: "B2|bvi2.1008",
			Name: "bvi2.1008", Attributes: map[string]string{"device_ip": "12.1.53.24", "vendor": "中兴"},
			FirstBatchID: batchID, CreatedAt: simTS, UpdatedAt: simTS,
		})
	}); err != nil {
		t.Fatalf("insert port resource after v8 key migration: %v", err)
	}
	if _, err := db2.Resources.GetByTypeKey(ctx, domain.ResourceTypePort, "B2|bvi2.1008"); err != nil {
		t.Fatalf("GetByTypeKey(port) after v8 key migration: %v", err)
	}

	// Reopening again must be a no-op (idempotent migration).
	db3, err := Open(path)
	if err != nil {
		t.Fatalf("third Open (idempotency): %v", err)
	}
	defer db3.Close()
	if err := db3.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&maxV); err != nil {
		t.Fatalf("max version: %v", err)
	}
	if maxV != 9 {
		t.Errorf("max schema version after reopen = %d, want 9", maxV)
	}
}

// ---------- workspaces ----------

func TestWorkspaceRepoCRUD(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	w := seedWorkspace(t, db, "ws_1", "华东实验网", `{"batchIds":["bat_1","bat_2"]}`)

	got, err := db.Workspaces.GetByID(ctx, "ws_1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "华东实验网" || got.Status != domain.WorkspaceStatusActive ||
		got.Description != w.Description || got.CreatedAt != simTS || got.UpdatedAt != simTS {
		t.Errorf("workspace round-trip mismatch: %+v", got)
	}
	src, err := got.Source()
	if err != nil {
		t.Fatalf("Source(): %v", err)
	}
	if len(src.BatchIDs) != 2 || src.BatchIDs[0] != "bat_1" || src.BatchIDs[1] != "bat_2" {
		t.Errorf("Source() = %+v, want [bat_1 bat_2]", src)
	}

	byName, err := db.Workspaces.GetByName(ctx, "华东实验网")
	if err != nil || byName.ID != "ws_1" {
		t.Errorf("GetByName = %+v, %v; want ws_1", byName, err)
	}
	if _, err := db.Workspaces.GetByName(ctx, "不存在"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetByName(unknown) = %v, want ErrNotFound", err)
	}
	if _, err := db.Workspaces.GetByID(ctx, "ws_x"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetByID(unknown) = %v, want ErrNotFound", err)
	}

	// Duplicate name → ErrConflict (UNIQUE(name)).
	dup := newWorkspace("ws_2", "华东实验网", "")
	if err := db.Workspaces.Insert(ctx, dup); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("duplicate name insert = %v, want ErrConflict", err)
	}
	// Invalid status is rejected before touching SQL.
	bad := newWorkspace("ws_3", "x", "")
	bad.Status = "paused"
	if err := db.Workspaces.Insert(ctx, bad); err == nil {
		t.Error("insert with invalid status succeeded, want error")
	}

	// List with pagination: 3 workspaces, page 2 of size 2 → 1 item.
	seedWorkspace(t, db, "ws_4", "华南实验网", "")
	w5 := newWorkspace("ws_5", "西南实验网", "")
	w5.CreatedAt = simTS.Add(time.Hour)
	w5.UpdatedAt = simTS.Add(time.Hour)
	if err := db.Do(ctx, func(tx *Tx) error { return tx.Workspaces.Insert(ctx, w5) }); err != nil {
		t.Fatalf("insert ws_5: %v", err)
	}
	items, total, err := db.Workspaces.List(ctx, 2, 2)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 3 || len(items) != 1 || items[0].ID != "ws_1" {
		t.Errorf("List page 2 = %d items (%+v), total %d; want 1 item (ws_1), total 3", len(items), items, total)
	}
	items, total, err = db.Workspaces.List(ctx, 1, 0) // pageSize <= 0 → unlimited
	if err != nil || total != 3 || len(items) != 3 {
		t.Errorf("List unlimited = %d items, total %d, err %v; want 3/3", len(items), total, err)
	}
	if items[0].ID != "ws_5" { // newest first
		t.Errorf("List order = %+v, want ws_5 first", items)
	}

	// UpdateTimestamps stamps updated_at.
	before, _ := db.Workspaces.GetByID(ctx, "ws_1")
	if err := db.Workspaces.UpdateTimestamps(ctx, "ws_1"); err != nil {
		t.Fatalf("UpdateTimestamps: %v", err)
	}
	after, _ := db.Workspaces.GetByID(ctx, "ws_1")
	if after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Error("UpdateTimestamps did not change updated_at")
	}
	if err := db.Workspaces.UpdateTimestamps(ctx, "ws_x"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("UpdateTimestamps(unknown) = %v, want ErrNotFound", err)
	}
}

func TestWorkspaceDeleteCascades(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// One workspace with the full entity tree, plus a second workspace that
	// must survive the delete.
	err := db.Do(ctx, func(tx *Tx) error {
		if err := tx.Workspaces.Insert(ctx, newWorkspace("ws_del", "待删除", `{"batchIds":["bat_1"]}`)); err != nil {
			return err
		}
		if err := tx.Workspaces.Insert(ctx, newWorkspace("ws_keep", "保留", "")); err != nil {
			return err
		}
		for i, code := range []string{"DEV-A", "DEV-B", "DEV-C"} {
			status := domain.EntityStatusUp
			if code == "DEV-C" {
				status = domain.EntityStatusDown
			}
			n, err := tx.Sequences.Allocate(ctx, domain.SeqDevice)
			if err != nil {
				return err
			}
			if err := tx.VirtualDevices.Insert(ctx,
				newDevice(domain.FormatSimID(domain.SeqDevice, n), "ws_del", code, status)); err != nil {
				return err
			}
			_ = i
		}
		ifaces := [][2]string{{"vd-1", "eth0"}, {"vd-1", "eth1"}, {"vd-2", "eth0"}, {"vd-3", "eth0"}}
		for _, pair := range ifaces {
			n, err := tx.Sequences.Allocate(ctx, domain.SeqInterface)
			if err != nil {
				return err
			}
			if err := tx.SimInterfaces.Insert(ctx,
				newIface(domain.FormatSimID(domain.SeqInterface, n), "ws_del", pair[0], pair[1], domain.EntityStatusUp)); err != nil {
				return err
			}
		}
		for _, pair := range [][2]string{{"if-1", "if-3"}, {"if-2", "if-3"}} {
			n, err := tx.Sequences.Allocate(ctx, domain.SeqLink)
			if err != nil {
				return err
			}
			if err := tx.SimLinks.Insert(ctx,
				newLink(domain.FormatSimID(domain.SeqLink, n), "ws_del", pair[0], pair[1], domain.EntityStatusUp, "CKT-1")); err != nil {
				return err
			}
		}
		for i := 1; i <= 2; i++ {
			n, err := tx.Sequences.Allocate(ctx, domain.SeqChangeSet)
			if err != nil {
				return err
			}
			cs := domain.ChangeSet{
				ID: domain.FormatSimID(domain.SeqChangeSet, n), WorkspaceID: "ws_del",
				Description: "cs", Trigger: domain.ChangeSetTriggerUser,
				Status: domain.ChangeSetStatusApplied, OpsApplied: 1, CreatedAt: simTS,
			}
			if err := tx.ChangeSets.Insert(ctx, cs, []domain.ChangeSetOp{{
				ChangeSetID: cs.ID, Seq: 1, OpCode: domain.OpCreateLink,
				OpJSON: `{"op":"create_link"}`, InverseJSON: `{"op":"delete_link"}`,
				OpResult: domain.OpResultApplied,
			}}); err != nil {
				return err
			}
		}
		for i := 1; i <= 2; i++ {
			n, err := tx.Sequences.Allocate(ctx, domain.SeqCheckRun)
			if err != nil {
				return err
			}
			run := domain.CheckRun{
				ID: domain.FormatSimID(domain.SeqCheckRun, n), WorkspaceID: "ws_del",
				Trigger: domain.CheckTriggerManual, RuleVersion: domain.SimRuleVersion,
				StartedAt: simTS, DurationMs: 5, Score: 100, TopologyJSON: "[]",
			}
			if err := tx.CheckRuns.Insert(ctx, run, []domain.CheckResult{{
				RunID: run.ID, Seq: 1, CheckCode: domain.CheckIsolatedDevice,
				Severity: domain.SeverityInfo, Passed: true, Message: "ok", DetailJSON: "{}",
			}}); err != nil {
				return err
			}
		}
		for i := 1; i <= 2; i++ {
			n, err := tx.Sequences.Allocate(ctx, domain.SeqCheckpoint)
			if err != nil {
				return err
			}
			if err := tx.Checkpoints.Insert(ctx, domain.Checkpoint{
				ID: domain.FormatSimID(domain.SeqCheckpoint, n), WorkspaceID: "ws_del",
				Label: "cp", FilePath: "checkpoints/ws_del/cp.db", WorkspaceHash: "h",
				CreatedAt: simTS,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	counts, err := db.Workspaces.Delete(ctx, "ws_del")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	wantCounts := map[string]int64{
		"virtual_devices": 3,
		"interfaces":      4,
		"links":           2,
		"changesets":      2,
		"check_runs":      2,
		"checkpoints":     2,
	}
	if len(counts) != len(wantCounts) {
		t.Fatalf("Delete returned %d keys, want %d: %v", len(counts), len(wantCounts), counts)
	}
	for table, want := range wantCounts {
		if got := counts[table]; got != want {
			t.Errorf("Delete counts[%s] = %d, want %d", table, got, want)
		}
	}

	// Every sim table of the deleted workspace (incl. the cascade-only
	// children) is empty; ws_keep is the only workspace left. The global
	// `sequences` counters are deliberately NOT workspace-scoped and survive.
	for _, table := range []string{
		"virtual_devices", "interfaces", "links",
		"changesets", "changeset_ops", "check_runs", "check_results", "checkpoints",
	} {
		if n := simTableCount(t, db, table); n != 0 {
			t.Errorf("table %s has %d rows after workspace delete, want 0", table, n)
		}
	}
	if n := simTableCount(t, db, "workspaces"); n != 1 {
		t.Errorf("workspaces has %d rows after delete, want 1 (ws_keep)", n)
	}

	// The second workspace survived.
	if _, err := db.Workspaces.GetByID(ctx, "ws_keep"); err != nil {
		t.Errorf("ws_keep did not survive: %v", err)
	}

	// Unknown workspace → ErrNotFound.
	if _, err := db.Workspaces.Delete(ctx, "ws_del"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Delete(unknown) = %v, want ErrNotFound", err)
	}
}

// TestSequenceGlobalMultiWorkspace pins the orchestrator-amended DDL v5
// contract (PROJECT_BRIEF_P2 §3, "ID allocation"): ids are allocated from
// GLOBAL per-kind sequences, so `id TEXT PRIMARY KEY` stays valid when
// multiple workspaces coexist in one database. Two workspaces materializing
// the same device codes must both succeed with distinct globally-unique
// ids — the second workspace gets structurally identical topology with
// fresh ids (documented behavior, not a collision).
func TestSequenceGlobalMultiWorkspace(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	seedWorkspace(t, db, "ws_a", "工作台A", "")
	err := db.Do(ctx, func(tx *Tx) error {
		return tx.VirtualDevices.Insert(ctx, newDevice("vd-1", "ws_a", "DEV-001", domain.EntityStatusUp))
	})
	if err != nil {
		t.Fatalf("first workspace vd-1: %v", err)
	}

	seedWorkspace(t, db, "ws_b", "工作台B", "")
	err = db.Do(ctx, func(tx *Tx) error {
		return tx.VirtualDevices.Insert(ctx, newDevice("vd-2", "ws_b", "DEV-001", domain.EntityStatusUp))
	})
	if err != nil {
		t.Fatalf("second workspace vd-2: %v", err)
	}

	for _, ws := range []string{"ws_a", "ws_b"} {
		dev, err := db.VirtualDevices.GetByCode(ctx, ws, "DEV-001")
		if err != nil {
			t.Fatalf("GetByCode(%s): %v", ws, err)
		}
		if dev.WorkspaceID != ws {
			t.Errorf("DEV-001 of %s has workspace %s", ws, dev.WorkspaceID)
		}
	}
	a, _ := db.VirtualDevices.GetByCode(ctx, "ws_a", "DEV-001")
	b, _ := db.VirtualDevices.GetByCode(ctx, "ws_b", "DEV-001")
	if a.ID == b.ID {
		t.Errorf("same device id %s across workspaces", a.ID)
	}
}

// ---------- sequences ----------

func TestSequenceAllocate(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedWorkspace(t, db, "ws_seq", "序列", "")

	// Monotonic allocation + kind independence, plus the real usage
	// pattern: allocate inside Do together with the device insert.
	err := db.Do(ctx, func(tx *Tx) error {
		for i, want := range []int{1, 2, 3} {
			n, err := tx.Sequences.Allocate(ctx, domain.SeqDevice)
			if err != nil {
				return err
			}
			if n != want {
				return fmt.Errorf("vd allocation %d = %d, want %d", i, n, want)
			}
		}
		n, err := tx.Sequences.Allocate(ctx, domain.SeqInterface)
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("first if allocation = %d, want 1", n)
		}
		// Consume the next vd id for a real insert.
		n, err = tx.Sequences.Allocate(ctx, domain.SeqDevice)
		if err != nil {
			return err
		}
		if n != 4 {
			return fmt.Errorf("vd allocation after if = %d, want 4", n)
		}
		return tx.VirtualDevices.Insert(ctx,
			newDevice(domain.FormatSimID(domain.SeqDevice, n), "ws_seq", "DEV-001", domain.EntityStatusUp))
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}

	dev, err := db.VirtualDevices.GetByCode(ctx, "ws_seq", "DEV-001")
	if err != nil || dev.ID != "vd-4" {
		t.Errorf("device after Do = %+v, %v; want id vd-4", dev, err)
	}

	for kind, want := range map[string]int{
		domain.SeqDevice: 4, domain.SeqInterface: 1, domain.SeqLink: 0,
	} {
		got, err := db.Sequences.Value(ctx, kind)
		if err != nil || got != want {
			t.Errorf("Value(%s) = %d, %v; want %d", kind, got, err, want)
		}
	}

	// A rolled-back transaction also rolls back its sequence increments
	// (no burned ids for failed changesets).
	_ = db.Do(ctx, func(tx *Tx) error {
		if _, err := tx.Sequences.Allocate(ctx, domain.SeqDevice); err != nil {
			return err
		}
		return errors.New("injected failure")
	})
	if got, _ := db.Sequences.Value(ctx, domain.SeqDevice); got != 4 {
		t.Errorf("vd sequence after rollback = %d, want 4 (increment rolled back)", got)
	}

	// Invalid kind is rejected before touching SQL.
	if _, err := db.Sequences.Allocate(ctx, "xx"); err == nil {
		t.Error("Allocate(invalid kind) succeeded, want error")
	}
	// An unallocated kind reads as 0.
	if got, err := db.Sequences.Value(ctx, domain.SeqCheckpoint); err != nil || got != 0 {
		t.Errorf("Value(unallocated kind) = %d, %v; want 0", got, err)
	}
}

// seedResource creates one import batch plus a device resource so lineage
// references (virtual_devices.resource_id, links.resource_id) have a valid
// FK target.
func seedResource(t *testing.T, db *DB, batchID, resourceID, naturalKey string) {
	t.Helper()
	ctx := context.Background()
	err := db.Do(ctx, func(tx *Tx) error {
		if err := tx.Batches.Create(ctx, newBatch(batchID, simTS)); err != nil {
			return err
		}
		res := newResource(resourceID, naturalKey, simTS)
		res.FirstBatchID = batchID
		return tx.Resources.Insert(ctx, res)
	})
	if err != nil {
		t.Fatalf("seed resource %s: %v", resourceID, err)
	}
}

// ---------- virtual devices ----------

func TestVirtualDeviceRepo(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedWorkspace(t, db, "ws_dev", "设备", "")
	seedResource(t, db, "bat_dev", "res_1", "DEV-LINK")

	err := db.Do(ctx, func(tx *Tx) error {
		for _, code := range []string{"DEV-C", "DEV-A", "DEV-B"} { // deliberately unsorted
			if err := tx.VirtualDevices.Insert(ctx,
				newDevice("vd-"+code[4:], "ws_dev", code, domain.EntityStatusUp)); err != nil {
				return err
			}
		}
		// One device with a resource lineage + one down device.
		if err := tx.VirtualDevices.Insert(ctx, func() domain.VirtualDevice {
			d := newDevice("vd-L", "ws_dev", "DEV-LINK", domain.EntityStatusDown)
			d.ResourceID = "res_1"
			return d
		}()); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed devices: %v", err)
	}

	// ListByWorkspace is sorted by code.
	devs, err := db.VirtualDevices.ListByWorkspace(ctx, "ws_dev")
	if err != nil {
		t.Fatalf("ListByWorkspace: %v", err)
	}
	wantCodes := []string{"DEV-A", "DEV-B", "DEV-C", "DEV-LINK"}
	if len(devs) != len(wantCodes) {
		t.Fatalf("devices = %d, want %d", len(devs), len(wantCodes))
	}
	for i, code := range wantCodes {
		if devs[i].Code != code {
			t.Errorf("devices[%d].Code = %q, want %q", i, devs[i].Code, code)
		}
	}

	// ResourceID round-trip: "" → NULL → "", "res_1" stays.
	if devs[3].ResourceID != "res_1" || devs[0].ResourceID != "" {
		t.Errorf("ResourceID round-trip: %+v / %+v", devs[0], devs[3])
	}

	// GetByCode + GetByID.
	got, err := db.VirtualDevices.GetByCode(ctx, "ws_dev", "DEV-B")
	if err != nil || got.ID != "vd-B" {
		t.Errorf("GetByCode = %+v, %v; want id vd-B", got, err)
	}
	if got.Name != "设备-DEV-B" || got.Status != domain.EntityStatusUp || got.WorkspaceID != "ws_dev" {
		t.Errorf("GetByCode round-trip mismatch: %+v", got)
	}
	if _, err := db.VirtualDevices.GetByCode(ctx, "ws_dev", "DEV-X"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetByCode(unknown) = %v, want ErrNotFound", err)
	}
	if _, err := db.VirtualDevices.GetByID(ctx, "vd-A"); err != nil {
		t.Errorf("GetByID: %v", err)
	}
	if _, err := db.VirtualDevices.GetByID(ctx, "vd-X"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetByID(unknown) = %v, want ErrNotFound", err)
	}

	// Duplicate (workspace, code) → ErrConflict.
	err = db.VirtualDevices.Insert(ctx, newDevice("vd-dup", "ws_dev", "DEV-A", domain.EntityStatusUp))
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("duplicate code insert = %v, want ErrConflict", err)
	}
	// FK: unknown workspace.
	if err := db.VirtualDevices.Insert(ctx, newDevice("vd-fk", "ws_missing", "DEV-FK", domain.EntityStatusUp)); err == nil {
		t.Error("insert into unknown workspace succeeded, want FK error")
	}
	// Invalid status is rejected before SQL.
	if err := db.VirtualDevices.Insert(ctx, newDevice("vd-bad", "ws_dev", "DEV-BAD", "paused")); err == nil {
		t.Error("insert with invalid status succeeded, want error")
	}

	// CountByWorkspace: up 3, down 1.
	counts, err := db.VirtualDevices.CountByWorkspace(ctx, "ws_dev")
	if err != nil {
		t.Fatalf("CountByWorkspace: %v", err)
	}
	if counts[domain.EntityStatusUp] != 3 || counts[domain.EntityStatusDown] != 1 {
		t.Errorf("CountByWorkspace = %v, want up=3 down=1", counts)
	}

	// UpdateStatus mutates status AND stamps updated_at (regardless of the
	// clock sitting before or after the fixed test timestamp).
	if err := db.VirtualDevices.UpdateStatus(ctx, "vd-A", domain.EntityStatusDown); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, _ = db.VirtualDevices.GetByID(ctx, "vd-A")
	if got.Status != domain.EntityStatusDown {
		t.Errorf("status after UpdateStatus = %q, want down", got.Status)
	}
	if got.UpdatedAt.Equal(got.CreatedAt) {
		t.Errorf("updated_at %v unchanged by UpdateStatus", got.UpdatedAt)
	}
	if err := db.VirtualDevices.UpdateStatus(ctx, "vd-X", domain.EntityStatusUp); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("UpdateStatus(unknown) = %v, want ErrNotFound", err)
	}
	if err := db.VirtualDevices.UpdateStatus(ctx, "vd-A", "paused"); err == nil {
		t.Error("UpdateStatus(invalid) succeeded, want error")
	}

	// Touch only stamps updated_at.
	before, _ := db.VirtualDevices.GetByID(ctx, "vd-B")
	if err := db.VirtualDevices.Touch(ctx, "vd-B"); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	after, _ := db.VirtualDevices.GetByID(ctx, "vd-B")
	if after.Status != before.Status || after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("Touch changed more than updated_at: %+v → %+v", before, after)
	}
	if err := db.VirtualDevices.Touch(ctx, "vd-X"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Touch(unknown) = %v, want ErrNotFound", err)
	}

	// Delete removes the device.
	if err := db.VirtualDevices.Delete(ctx, "vd-C"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := db.VirtualDevices.GetByID(ctx, "vd-C"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetByID after Delete = %v, want ErrNotFound", err)
	}
	if err := db.VirtualDevices.Delete(ctx, "vd-C"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Delete(unknown) = %v, want ErrNotFound", err)
	}
}

// ---------- interfaces ----------

func TestSimInterfaceRepo(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedWorkspace(t, db, "ws_if", "接口", "")

	err := db.Do(ctx, func(tx *Tx) error {
		if err := tx.VirtualDevices.Insert(ctx, newDevice("vd-1", "ws_if", "DEV-1", domain.EntityStatusUp)); err != nil {
			return err
		}
		if err := tx.VirtualDevices.Insert(ctx, newDevice("vd-2", "ws_if", "DEV-2", domain.EntityStatusUp)); err != nil {
			return err
		}
		// Deliberately unsorted names across two devices.
		for _, pair := range [][2]string{
			{"vd-2", "eth0"}, {"vd-1", "eth1"}, {"vd-1", "eth0"}, {"vd-2", "eth1"},
		} {
			if err := tx.SimInterfaces.Insert(ctx,
				newIface("if-"+pair[0][3:]+"-"+pair[1], "ws_if", pair[0], pair[1], domain.EntityStatusUp)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed interfaces: %v", err)
	}

	// ListByWorkspace sorted by (device_id, name).
	ifaces, err := db.SimInterfaces.ListByWorkspace(ctx, "ws_if")
	if err != nil {
		t.Fatalf("ListByWorkspace: %v", err)
	}
	want := [][2]string{
		{"vd-1", "eth0"}, {"vd-1", "eth1"}, {"vd-2", "eth0"}, {"vd-2", "eth1"},
	}
	if len(ifaces) != len(want) {
		t.Fatalf("interfaces = %d, want %d", len(ifaces), len(want))
	}
	for i, pair := range want {
		if ifaces[i].DeviceID != pair[0] || ifaces[i].Name != pair[1] {
			t.Errorf("interfaces[%d] = %s/%s, want %s/%s", i, ifaces[i].DeviceID, ifaces[i].Name, pair[0], pair[1])
		}
	}

	// ListByDevice sorted by name.
	dev1, err := db.SimInterfaces.ListByDevice(ctx, "vd-1")
	if err != nil || len(dev1) != 2 || dev1[0].Name != "eth0" || dev1[1].Name != "eth1" {
		t.Errorf("ListByDevice(vd-1) = %+v, %v", dev1, err)
	}

	// Unconfigured NULL round-trip: "" / -1.
	got, err := db.SimInterfaces.GetByID(ctx, "if-1-eth0")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.IPv4Addr != "" || got.IPv4Prefix != -1 {
		t.Errorf("unconfigured interface = %q/%d, want \"\"/-1", got.IPv4Addr, got.IPv4Prefix)
	}

	// UpdateIPv4 round-trips: configure → read → unconfigure → read.
	if err := db.SimInterfaces.UpdateIPv4(ctx, "if-1-eth0", "10.0.0.1", 24); err != nil {
		t.Fatalf("UpdateIPv4: %v", err)
	}
	got, _ = db.SimInterfaces.GetByID(ctx, "if-1-eth0")
	if got.IPv4Addr != "10.0.0.1" || got.IPv4Prefix != 24 {
		t.Errorf("configured interface = %q/%d, want 10.0.0.1/24", got.IPv4Addr, got.IPv4Prefix)
	}
	if got.UpdatedAt.Equal(got.CreatedAt) {
		t.Errorf("updated_at %v unchanged by UpdateIPv4", got.UpdatedAt)
	}
	if err := db.SimInterfaces.UpdateIPv4(ctx, "if-1-eth0", "", -1); err != nil {
		t.Fatalf("UpdateIPv4(unconfigure): %v", err)
	}
	got, _ = db.SimInterfaces.GetByID(ctx, "if-1-eth0")
	if got.IPv4Addr != "" || got.IPv4Prefix != -1 {
		t.Errorf("unconfigured again = %q/%d, want \"\"/-1", got.IPv4Addr, got.IPv4Prefix)
	}

	// Malformed combinations are rejected before SQL.
	for _, c := range []struct {
		addr   string
		prefix int
	}{
		{"10.0.0.1", 33},
		{"10.0.0.1", -1},
	} {
		if err := db.SimInterfaces.UpdateIPv4(ctx, "if-1-eth0", c.addr, c.prefix); err == nil {
			t.Errorf("UpdateIPv4(%q,%d) succeeded, want error", c.addr, c.prefix)
		}
	}
	if err := db.SimInterfaces.UpdateIPv4(ctx, "if-X", "10.0.0.1", 24); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("UpdateIPv4(unknown) = %v, want ErrNotFound", err)
	}

	// Duplicate (workspace, device, name) → ErrConflict.
	err = db.SimInterfaces.Insert(ctx, newIface("if-dup", "ws_if", "vd-1", "eth0", domain.EntityStatusUp))
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("duplicate name insert = %v, want ErrConflict", err)
	}
	// FK: bogus device.
	if err := db.SimInterfaces.Insert(ctx, newIface("if-fk", "ws_if", "vd-99", "eth9", domain.EntityStatusUp)); err == nil {
		t.Error("insert with bogus device_id succeeded, want FK error")
	}

	// UpdateStatus.
	if err := db.SimInterfaces.UpdateStatus(ctx, "if-1-eth0", domain.EntityStatusDown); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, _ = db.SimInterfaces.GetByID(ctx, "if-1-eth0")
	if got.Status != domain.EntityStatusDown {
		t.Errorf("status after UpdateStatus = %q, want down", got.Status)
	}
	if err := db.SimInterfaces.UpdateStatus(ctx, "if-X", domain.EntityStatusUp); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("UpdateStatus(unknown) = %v, want ErrNotFound", err)
	}

	// CountByWorkspace: up 3, down 1.
	counts, err := db.SimInterfaces.CountByWorkspace(ctx, "ws_if")
	if err != nil {
		t.Fatalf("CountByWorkspace: %v", err)
	}
	if counts[domain.EntityStatusUp] != 3 || counts[domain.EntityStatusDown] != 1 {
		t.Errorf("CountByWorkspace = %v, want up=3 down=1", counts)
	}

	// Touch + Delete.
	if err := db.SimInterfaces.Touch(ctx, "if-2-eth1"); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	if err := db.SimInterfaces.Delete(ctx, "if-2-eth1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := db.SimInterfaces.GetByID(ctx, "if-2-eth1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetByID after Delete = %v, want ErrNotFound", err)
	}

	// Insert with configured address round-trips the prefix.
	cfg := newIface("if-cfg", "ws_if", "vd-2", "eth9", domain.EntityStatusUp)
	cfg.IPv4Addr, cfg.IPv4Prefix = "192.168.1.1", 16
	if err := db.SimInterfaces.Insert(ctx, cfg); err != nil {
		t.Fatalf("insert configured: %v", err)
	}
	got, _ = db.SimInterfaces.GetByID(ctx, "if-cfg")
	if got.IPv4Addr != "192.168.1.1" || got.IPv4Prefix != 16 {
		t.Errorf("configured insert round-trip = %q/%d", got.IPv4Addr, got.IPv4Prefix)
	}
}

// ---------- links ----------

func TestSimLinkRepo(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedWorkspace(t, db, "ws_ln", "链路", "")

	err := db.Do(ctx, func(tx *Tx) error {
		for _, d := range []string{"vd-1", "vd-2", "vd-3"} {
			if err := tx.VirtualDevices.Insert(ctx, newDevice(d, "ws_ln", "DEV-"+d[3:], domain.EntityStatusUp)); err != nil {
				return err
			}
		}
		for _, pair := range [][2]string{
			{"vd-1", "eth0"}, {"vd-1", "eth1"}, {"vd-2", "eth0"}, {"vd-2", "eth1"}, {"vd-3", "eth0"},
		} {
			if err := tx.SimInterfaces.Insert(ctx,
				newIface("if-"+pair[0][3:]+"-"+pair[1], "ws_ln", pair[0], pair[1], domain.EntityStatusUp)); err != nil {
				return err
			}
		}
		// ln-1..ln-3 in creation order; ln-2 is DOWN.
		if err := tx.SimLinks.Insert(ctx, newLink("ln-1", "ws_ln", "if-1-eth0", "if-2-eth0", domain.EntityStatusUp, "CKT-001")); err != nil {
			return err
		}
		if err := tx.SimLinks.Insert(ctx, newLink("ln-2", "ws_ln", "if-1-eth1", "if-2-eth1", domain.EntityStatusDown, "CKT-002")); err != nil {
			return err
		}
		return tx.SimLinks.Insert(ctx, newLink("ln-3", "ws_ln", "if-2-eth1", "if-3-eth0", domain.EntityStatusUp, "CKT-003"))
	})
	if err != nil {
		t.Fatalf("seed links: %v", err)
	}

	// ListByWorkspace sorted by id ascending.
	links, err := db.SimLinks.ListByWorkspace(ctx, "ws_ln")
	if err != nil || len(links) != 3 {
		t.Fatalf("ListByWorkspace = %d links, %v; want 3", len(links), err)
	}
	for i, wantID := range []string{"ln-1", "ln-2", "ln-3"} {
		if links[i].ID != wantID {
			t.Errorf("links[%d].ID = %q, want %q", i, links[i].ID, wantID)
		}
	}
	if links[0].Label != "CKT-001" || links[1].Status != domain.EntityStatusDown ||
		links[0].AInterfaceID != "if-1-eth0" || links[0].ZInterfaceID != "if-2-eth0" {
		t.Errorf("link round-trip mismatch: %+v %+v", links[0], links[1])
	}

	// GetByID.
	got, err := db.SimLinks.GetByID(ctx, "ln-2")
	if err != nil || got.Label != "CKT-002" {
		t.Errorf("GetByID(ln-2) = %+v, %v", got, err)
	}
	if _, err := db.SimLinks.GetByID(ctx, "ln-9"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetByID(unknown) = %v, want ErrNotFound", err)
	}

	// ListTouchingInterface: A end, Z end and multiple links.
	touching, err := db.SimLinks.ListTouchingInterface(ctx, "if-2-eth1")
	if err != nil || len(touching) != 2 || touching[0].ID != "ln-2" || touching[1].ID != "ln-3" {
		t.Errorf("ListTouchingInterface(if-2-eth1) = %+v, %v; want ln-2, ln-3", touching, err)
	}
	touching, err = db.SimLinks.ListTouchingInterface(ctx, "if-1-eth0")
	if err != nil || len(touching) != 1 || touching[0].ID != "ln-1" {
		t.Errorf("ListTouchingInterface(if-1-eth0) = %+v, %v; want ln-1", touching, err)
	}
	if touching, err = db.SimLinks.ListTouchingInterface(ctx, "if-none"); err != nil || len(touching) != 0 {
		t.Errorf("ListTouchingInterface(unknown) = %+v, %v; want none", touching, err)
	}

	// Self loop is rejected by the DDL CHECK constraint.
	if err := db.SimLinks.Insert(ctx, newLink("ln-self", "ws_ln", "if-1-eth0", "if-1-eth0", domain.EntityStatusUp, "SELF")); err == nil {
		t.Error("self-loop link insert succeeded, want CHECK constraint error")
	}
	// FK: bogus endpoint.
	if err := db.SimLinks.Insert(ctx, newLink("ln-fk", "ws_ln", "if-1-eth0", "if-99-eth0", domain.EntityStatusUp, "FK")); err == nil {
		t.Error("link with bogus endpoint succeeded, want FK error")
	}
	// Invalid status rejected before SQL.
	if err := db.SimLinks.Insert(ctx, newLink("ln-bad", "ws_ln", "if-1-eth0", "if-2-eth0", "paused", "BAD")); err == nil {
		t.Error("link with invalid status succeeded, want error")
	}

	// CountByWorkspace: up 2, down 1.
	counts, err := db.SimLinks.CountByWorkspace(ctx, "ws_ln")
	if err != nil || counts[domain.EntityStatusUp] != 2 || counts[domain.EntityStatusDown] != 1 {
		t.Errorf("CountByWorkspace = %v, want up=2 down=1", counts)
	}

	// UpdateStatus stamps updated_at.
	if err := db.SimLinks.UpdateStatus(ctx, "ln-1", domain.EntityStatusDown); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, _ = db.SimLinks.GetByID(ctx, "ln-1")
	if got.Status != domain.EntityStatusDown || got.UpdatedAt.Equal(got.CreatedAt) {
		t.Errorf("link after UpdateStatus = %+v, want down + fresh updated_at", got)
	}
	if err := db.SimLinks.UpdateStatus(ctx, "ln-9", domain.EntityStatusUp); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("UpdateStatus(unknown) = %v, want ErrNotFound", err)
	}

	// Touch + Delete.
	if err := db.SimLinks.Touch(ctx, "ln-3"); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	if err := db.SimLinks.Delete(ctx, "ln-3"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := db.SimLinks.GetByID(ctx, "ln-3"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetByID after Delete = %v, want ErrNotFound", err)
	}
}

// ---------- changesets ----------

func TestChangeSetRepo(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedWorkspace(t, db, "ws_cs", "变更集", "")

	// cs-1: applied user changeset with 3 ops (mixed inverse JSON, one
	// empty).
	cs1 := domain.ChangeSet{
		ID: "cs-1", WorkspaceID: "ws_cs", Description: "建拓扑",
		Trigger: domain.ChangeSetTriggerUser, Status: domain.ChangeSetStatusApplied,
		OpsApplied: 3, CreatedAt: simTS,
	}
	ops1 := []domain.ChangeSetOp{
		{ChangeSetID: "cs-1", Seq: 1, OpCode: domain.OpCreateDevice,
			OpJSON:      `{"op":"create_device","code":"DEV-1","name":"n"}`,
			InverseJSON: `{"op":"delete_device","deviceId":"vd-1"}`, OpResult: domain.OpResultApplied},
		{ChangeSetID: "cs-1", Seq: 2, OpCode: domain.OpSetDeviceStatus,
			OpJSON:      `{"op":"set_device_status","deviceId":"vd-1","status":"down"}`,
			InverseJSON: "", OpResult: domain.OpResultApplied},
		{ChangeSetID: "cs-1", Seq: 3, OpCode: domain.OpDeleteLink,
			OpJSON:      `{"op":"delete_link","linkId":"ln-1"}`,
			InverseJSON: `{"op":"restore_link","link":{}}`, OpResult: domain.OpResultApplied},
	}
	if err := db.ChangeSets.Insert(ctx, cs1, ops1); err != nil {
		t.Fatalf("Insert cs-1: %v", err)
	}

	got, err := db.ChangeSets.GetByID(ctx, "cs-1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Description != "建拓扑" || got.Trigger != domain.ChangeSetTriggerUser ||
		got.Status != domain.ChangeSetStatusApplied || got.OpsApplied != 3 ||
		got.CreatedAt != simTS || got.Reverted() {
		t.Errorf("changeset round-trip mismatch: %+v", got)
	}

	gotOps, err := db.ChangeSets.GetOps(ctx, "cs-1")
	if err != nil {
		t.Fatalf("GetOps: %v", err)
	}
	if len(gotOps) != 3 {
		t.Fatalf("GetOps = %d ops, want 3", len(gotOps))
	}
	for i, want := range []struct {
		seq     int
		code    string
		inverse string
	}{
		{1, domain.OpCreateDevice, `{"op":"delete_device","deviceId":"vd-1"}`},
		{2, domain.OpSetDeviceStatus, ""},
		{3, domain.OpDeleteLink, `{"op":"restore_link","link":{}}`},
	} {
		if gotOps[i].Seq != want.seq || gotOps[i].OpCode != want.code || gotOps[i].InverseJSON != want.inverse ||
			gotOps[i].OpResult != domain.OpResultApplied || gotOps[i].ChangeSetID != "cs-1" {
			t.Errorf("ops[%d] = %+v, want seq=%d code=%s inverse=%q", i, gotOps[i], want.seq, want.code, want.inverse)
		}
	}
	// GetOps of an unknown changeset is empty, not an error.
	if gotOps, err = db.ChangeSets.GetOps(ctx, "cs-99"); err != nil || len(gotOps) != 0 {
		t.Errorf("GetOps(unknown) = %+v, %v; want empty", gotOps, err)
	}

	// cs-2: a failed changeset (ops not_attempted / failed, no inverses).
	cs2 := domain.ChangeSet{
		ID: "cs-2", WorkspaceID: "ws_cs", Description: "失败",
		Trigger: domain.ChangeSetTriggerUser, Status: domain.ChangeSetStatusFailed,
		OpsApplied: 0, CreatedAt: simTS,
	}
	ops2 := []domain.ChangeSetOp{
		{ChangeSetID: "cs-2", Seq: 1, OpCode: domain.OpSetLinkStatus, OpJSON: `{"op":"set_link_status"}`, OpResult: domain.OpResultFailed},
		{ChangeSetID: "cs-2", Seq: 2, OpCode: domain.OpCreateLink, OpJSON: `{"op":"create_link"}`, OpResult: domain.OpResultNotAttempted},
	}
	if err := db.ChangeSets.Insert(ctx, cs2, ops2); err != nil {
		t.Fatalf("Insert cs-2: %v", err)
	}

	// cs-3..cs-12: bare rows to prove numeric id ordering (lexicographic
	// "cs-10" would sort before "cs-9").
	for n := 3; n <= 12; n++ {
		cs := domain.ChangeSet{
			ID: domain.FormatSimID(domain.SeqChangeSet, n), WorkspaceID: "ws_cs",
			Description: fmt.Sprintf("cs-%d", n), Trigger: domain.ChangeSetTriggerUser,
			Status: domain.ChangeSetStatusApplied, OpsApplied: 0, CreatedAt: simTS,
		}
		if err := db.ChangeSets.Insert(ctx, cs, nil); err != nil {
			t.Fatalf("Insert %s: %v", cs.ID, err)
		}
	}

	// ListByWorkspace: desc by numeric id, with pagination.
	items, total, err := db.ChangeSets.ListByWorkspace(ctx, "ws_cs", 1, 0)
	if err != nil || total != 12 || len(items) != 12 {
		t.Fatalf("ListByWorkspace = %d items, total %d, %v; want 12/12", len(items), total, err)
	}
	for i, want := range []string{"cs-12", "cs-11", "cs-10", "cs-9", "cs-8", "cs-7"} {
		if items[i].ID != want {
			t.Errorf("items[%d].ID = %q, want %q", i, items[i].ID, want)
		}
	}
	items, total, err = db.ChangeSets.ListByWorkspace(ctx, "ws_cs", 2, 5)
	if err != nil || total != 12 || len(items) != 5 || items[0].ID != "cs-7" || items[4].ID != "cs-3" {
		t.Errorf("ListByWorkspace page 2 = %+v, total %d; want cs-7..cs-3", items, total)
	}

	// LatestApplied skips failed and reverted changesets.
	latest, err := db.ChangeSets.LatestApplied(ctx, "ws_cs")
	if err != nil || latest.ID != "cs-12" {
		t.Errorf("LatestApplied = %+v, %v; want cs-12", latest, err)
	}
	// cs-2 (failed) never becomes latest; revert cs-12 → cs-11.
	revertedAt := simTS.Add(2 * time.Hour)
	if err := db.ChangeSets.MarkReverted(ctx, "cs-12", revertedAt); err != nil {
		t.Fatalf("MarkReverted: %v", err)
	}
	got, _ = db.ChangeSets.GetByID(ctx, "cs-12")
	if got.Status != domain.ChangeSetStatusReverted || !got.Reverted() || got.RevertedAt != revertedAt {
		t.Errorf("cs-12 after MarkReverted = %+v, want reverted at %v", got, revertedAt)
	}
	latest, err = db.ChangeSets.LatestApplied(ctx, "ws_cs")
	if err != nil || latest.ID != "cs-11" {
		t.Errorf("LatestApplied after revert = %+v, %v; want cs-11", latest, err)
	}
	if err := db.ChangeSets.MarkReverted(ctx, "cs-99", revertedAt); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("MarkReverted(unknown) = %v, want ErrNotFound", err)
	}

	// CountByWorkspace.
	n, err := db.ChangeSets.CountByWorkspace(ctx, "ws_cs")
	if err != nil || n != 12 {
		t.Errorf("CountByWorkspace = %d, %v; want 12", n, err)
	}
	if _, err := db.ChangeSets.GetByID(ctx, "cs-99"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetByID(unknown) = %v, want ErrNotFound", err)
	}
}

func TestChangeSetLatestAppliedEmpty(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedWorkspace(t, db, "ws_empty", "空", "")

	if _, err := db.ChangeSets.LatestApplied(ctx, "ws_empty"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("LatestApplied(empty workspace) = %v, want ErrNotFound", err)
	}

	// A workspace with only failed changesets has no latest applied either.
	seedWorkspace(t, db, "ws_failed", "只有失败", "")
	cs := domain.ChangeSet{
		ID: "cs-1", WorkspaceID: "ws_failed", Trigger: domain.ChangeSetTriggerUser,
		Status: domain.ChangeSetStatusFailed, CreatedAt: simTS,
	}
	if err := db.ChangeSets.Insert(ctx, cs, nil); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if _, err := db.ChangeSets.LatestApplied(ctx, "ws_failed"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("LatestApplied(only failed) = %v, want ErrNotFound", err)
	}
}

func TestChangeSetInsertValidation(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedWorkspace(t, db, "ws_val", "校验", "")

	// Invalid trigger/status are rejected before SQL.
	cs := domain.ChangeSet{ID: "cs-1", WorkspaceID: "ws_val", Trigger: "cron", Status: domain.ChangeSetStatusApplied, CreatedAt: simTS}
	if err := db.ChangeSets.Insert(ctx, cs, nil); err == nil {
		t.Error("insert with invalid trigger succeeded, want error")
	}
	cs = domain.ChangeSet{ID: "cs-1", WorkspaceID: "ws_val", Trigger: domain.ChangeSetTriggerUser, Status: "pending", CreatedAt: simTS}
	if err := db.ChangeSets.Insert(ctx, cs, nil); err == nil {
		t.Error("insert with invalid status succeeded, want error")
	}
	// Op result mismatch + wrong ChangeSetID are rejected.
	cs = domain.ChangeSet{ID: "cs-1", WorkspaceID: "ws_val", Trigger: domain.ChangeSetTriggerUser, Status: domain.ChangeSetStatusApplied, CreatedAt: simTS}
	if err := db.ChangeSets.Insert(ctx, cs, []domain.ChangeSetOp{{
		ChangeSetID: "cs-other", Seq: 1, OpCode: domain.OpCreateLink, OpJSON: "{}", OpResult: domain.OpResultApplied,
	}}); err == nil {
		t.Error("op with foreign ChangeSetID succeeded, want error")
	}
	if err := db.ChangeSets.Insert(ctx, cs, []domain.ChangeSetOp{{
		ChangeSetID: "cs-1", Seq: 1, OpCode: domain.OpCreateLink, OpJSON: "{}", OpResult: "skipped",
	}}); err == nil {
		t.Error("op with invalid result succeeded, want error")
	}
	// Duplicate changeset id → ErrConflict; duplicate (changeset, seq) on a
	// second insert also surfaces as ErrConflict.
	if err := db.ChangeSets.Insert(ctx, cs, nil); err != nil {
		t.Fatalf("insert cs-1: %v", err)
	}
	if err := db.ChangeSets.Insert(ctx, cs, nil); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("duplicate changeset insert = %v, want ErrConflict", err)
	}
	// Unknown workspace fails on the FK.
	csForeign := domain.ChangeSet{ID: "cs-2", WorkspaceID: "ws_missing", Trigger: domain.ChangeSetTriggerUser, Status: domain.ChangeSetStatusApplied, CreatedAt: simTS}
	if err := db.ChangeSets.Insert(ctx, csForeign, nil); err == nil {
		t.Error("changeset into unknown workspace succeeded, want FK error")
	}
	// Simulated atomicity: an ops failure inside Do leaves no changeset row.
	err := db.Do(ctx, func(tx *Tx) error {
		cs3 := domain.ChangeSet{ID: "cs-3", WorkspaceID: "ws_val", Trigger: domain.ChangeSetTriggerUser, Status: domain.ChangeSetStatusApplied, CreatedAt: simTS}
		return tx.ChangeSets.Insert(ctx, cs3, []domain.ChangeSetOp{
			{ChangeSetID: "cs-3", Seq: 1, OpCode: domain.OpCreateLink, OpJSON: "{}", OpResult: domain.OpResultApplied},
			{ChangeSetID: "cs-3", Seq: 1, OpCode: domain.OpCreateLink, OpJSON: "{}", OpResult: domain.OpResultApplied}, // dup seq
		})
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("duplicate seq inside Do = %v, want ErrConflict", err)
	}
	if _, err := db.ChangeSets.GetByID(ctx, "cs-3"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("cs-3 persisted after rolled-back Do = %v, want ErrNotFound", err)
	}
}

// ---------- check runs ----------

func TestCheckRunRepo(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedWorkspace(t, db, "ws_run", "检查", "")

	// run-1: 2 findings + 5 baselines, not tied to a changeset.
	run1 := domain.CheckRun{
		ID: "run-1", WorkspaceID: "ws_run", ChangesetID: "",
		Trigger: domain.CheckTriggerManual, RuleVersion: domain.SimRuleVersion,
		StartedAt: simTS, DurationMs: 12, Score: 89, TopologyJSON: `{"links":[]}`,
	}
	results1 := []domain.CheckResult{
		{RunID: "run-1", Seq: 1, CheckCode: domain.CheckDuplicateHostAddress, Severity: domain.SeverityError,
			Passed: false, EntityKind: "workspace", Message: "duplicate host address 10.0.0.1 used by 2 interfaces",
			DetailJSON: `{"addr":"10.0.0.1","interfaces":[]}`},
		{RunID: "run-1", Seq: 2, CheckCode: domain.CheckIsolatedDevice, Severity: domain.SeverityWarning,
			Passed: false, EntityKind: "device", EntityID: "vd-5", Message: "device DEV-005 is isolated",
			DetailJSON: `{"deviceId":"vd-5"}`},
	}
	for i, code := range domain.CheckCodes { // 5 baselines
		results1 = append(results1, domain.CheckResult{
			RunID: "run-1", Seq: 3 + i, CheckCode: code, Severity: domain.SeverityInfo,
			Passed: true, Message: "ok", DetailJSON: "{}",
		})
	}
	if err := db.CheckRuns.Insert(ctx, run1, results1); err != nil {
		t.Fatalf("Insert run-1: %v", err)
	}

	got, err := db.CheckRuns.GetByID(ctx, "run-1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ChangesetID != "" || got.Trigger != domain.CheckTriggerManual || got.RuleVersion != domain.SimRuleVersion ||
		got.DurationMs != 12 || got.Score != 89 || got.TopologyJSON != `{"links":[]}` || got.StartedAt != simTS {
		t.Errorf("check run round-trip mismatch: %+v", got)
	}

	gotResults, err := db.CheckRuns.GetResults(ctx, "run-1")
	if err != nil {
		t.Fatalf("GetResults: %v", err)
	}
	if len(gotResults) != 7 {
		t.Fatalf("GetResults = %d rows, want 7", len(gotResults))
	}
	for i := 1; i <= 7; i++ {
		if gotResults[i-1].Seq != i {
			t.Errorf("results[%d].Seq = %d, want %d (seq order)", i-1, gotResults[i-1].Seq, i)
		}
	}
	r1 := gotResults[0]
	if r1.Severity != domain.SeverityError || r1.Passed || r1.EntityKind != "workspace" || r1.EntityID != "" ||
		r1.Message != "duplicate host address 10.0.0.1 used by 2 interfaces" || r1.DetailJSON != `{"addr":"10.0.0.1","interfaces":[]}` {
		t.Errorf("finding row mismatch: %+v", r1)
	}
	r6 := gotResults[5]
	if !r6.Passed || r6.EntityKind != "" || r6.EntityID != "" || r6.Message != "ok" {
		t.Errorf("baseline row mismatch: %+v", r6)
	}
	// GetResults of an unknown run is empty, not an error.
	if gotResults, err = db.CheckRuns.GetResults(ctx, "run-99"); err != nil || len(gotResults) != 0 {
		t.Errorf("GetResults(unknown) = %+v, %v; want empty", gotResults, err)
	}

	// run-2..run-11: auto_after_ops runs tied to a changeset, proving
	// numeric desc ordering ("run-10" > "run-9"). The changeset row must
	// exist for the check_runs FK.
	if err := db.ChangeSets.Insert(ctx, domain.ChangeSet{
		ID: "cs-1", WorkspaceID: "ws_run", Trigger: domain.ChangeSetTriggerUser,
		Status: domain.ChangeSetStatusApplied, OpsApplied: 0, CreatedAt: simTS,
	}, nil); err != nil {
		t.Fatalf("Insert cs-1: %v", err)
	}
	for n := 2; n <= 11; n++ {
		run := domain.CheckRun{
			ID: domain.FormatSimID(domain.SeqCheckRun, n), WorkspaceID: "ws_run", ChangesetID: "cs-1",
			Trigger: domain.CheckTriggerAutoAfterOps, RuleVersion: domain.SimRuleVersion,
			StartedAt: simTS, DurationMs: 1, Score: 100, TopologyJSON: "[]",
		}
		if err := db.CheckRuns.Insert(ctx, run, nil); err != nil {
			t.Fatalf("Insert %s: %v", run.ID, err)
		}
	}

	items, total, err := db.CheckRuns.ListByWorkspace(ctx, "ws_run", 1, 0)
	if err != nil || total != 11 || len(items) != 11 {
		t.Fatalf("ListByWorkspace = %d items, total %d, %v; want 11/11", len(items), total, err)
	}
	if items[0].ID != "run-11" || items[1].ID != "run-10" || items[2].ID != "run-9" || items[10].ID != "run-1" {
		t.Errorf("ListByWorkspace order = %v …, want run-11 run-10 run-9 … run-1", idsOfRuns(items))
	}
	items, total, err = db.CheckRuns.ListByWorkspace(ctx, "ws_run", 2, 4)
	if err != nil || total != 11 || len(items) != 4 || items[0].ID != "run-7" {
		t.Errorf("ListByWorkspace page 2 = %v, total %d; want run-7…", idsOfRuns(items), total)
	}

	// ChangesetID round-trip through NULL.
	got, _ = db.CheckRuns.GetByID(ctx, "run-2")
	if got.ChangesetID != "cs-1" || got.Trigger != domain.CheckTriggerAutoAfterOps {
		t.Errorf("run-2 = %+v, want changeset cs-1 / auto_after_ops", got)
	}

	// Latest + CountByWorkspace.
	latest, err := db.CheckRuns.Latest(ctx, "ws_run")
	if err != nil || latest.ID != "run-11" {
		t.Errorf("Latest = %+v, %v; want run-11", latest, err)
	}
	n, err := db.CheckRuns.CountByWorkspace(ctx, "ws_run")
	if err != nil || n != 11 {
		t.Errorf("CountByWorkspace = %d, %v; want 11", n, err)
	}
	if _, err := db.CheckRuns.GetByID(ctx, "run-99"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetByID(unknown) = %v, want ErrNotFound", err)
	}
	seedWorkspace(t, db, "ws_run2", "检查2", "")
	if _, err := db.CheckRuns.Latest(ctx, "ws_run2"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Latest(empty workspace) = %v, want ErrNotFound", err)
	}
}

// idsOfRuns extracts the ids of check runs for failure messages.
func idsOfRuns(runs []domain.CheckRun) []string {
	out := make([]string, len(runs))
	for i, r := range runs {
		out[i] = r.ID
	}
	return out
}

func TestCheckRunInsertValidation(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedWorkspace(t, db, "ws_runval", "校验", "")

	run := domain.CheckRun{ID: "run-1", WorkspaceID: "ws_runval", Trigger: "cron", StartedAt: simTS}
	if err := db.CheckRuns.Insert(ctx, run, nil); err == nil {
		t.Error("insert with invalid trigger succeeded, want error")
	}
	run = domain.CheckRun{ID: "run-1", WorkspaceID: "ws_runval", Trigger: domain.CheckTriggerAPI, StartedAt: simTS}
	if err := db.CheckRuns.Insert(ctx, run, []domain.CheckResult{{
		RunID: "run-1", Seq: 1, CheckCode: "CHK-XXX", Severity: "fatal", Message: "m",
	}}); err == nil {
		t.Error("insert with invalid severity succeeded, want error")
	}
	if err := db.CheckRuns.Insert(ctx, run, []domain.CheckResult{{
		RunID: "run-other", Seq: 1, CheckCode: "CHK-001", Severity: domain.SeverityError, Message: "m",
	}}); err == nil {
		t.Error("result with foreign RunID succeeded, want error")
	}
	// Unknown workspace fails on the FK.
	run = domain.CheckRun{ID: "run-2", WorkspaceID: "ws_missing", Trigger: domain.CheckTriggerAPI, StartedAt: simTS}
	if err := db.CheckRuns.Insert(ctx, run, nil); err == nil {
		t.Error("run into unknown workspace succeeded, want FK error")
	}
	// Unknown changeset (when set) fails on the FK.
	run = domain.CheckRun{ID: "run-3", WorkspaceID: "ws_runval", ChangesetID: "cs-99", Trigger: domain.CheckTriggerAPI, StartedAt: simTS}
	if err := db.CheckRuns.Insert(ctx, run, nil); err == nil {
		t.Error("run tied to unknown changeset succeeded, want FK error")
	}
}

// ---------- checkpoints ----------

func TestCheckpointRepo(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedWorkspace(t, db, "ws_cp", "检查点", "")

	cp1 := domain.Checkpoint{
		ID: "cp-1", WorkspaceID: "ws_cp", Label: "配置地址前",
		FilePath: "checkpoints/ws_cp/cp-1.db", WorkspaceHash: "hash-1", CreatedAt: simTS,
	}
	if err := db.Checkpoints.Insert(ctx, cp1); err != nil {
		t.Fatalf("Insert cp-1: %v", err)
	}

	got, err := db.Checkpoints.GetByID(ctx, "cp-1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Label != "配置地址前" || got.FilePath != "checkpoints/ws_cp/cp-1.db" ||
		got.WorkspaceHash != "hash-1" || got.CreatedAt != simTS {
		t.Errorf("checkpoint round-trip mismatch: %+v", got)
	}
	if _, err := db.Checkpoints.GetByID(ctx, "cp-9"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetByID(unknown) = %v, want ErrNotFound", err)
	}

	// cp-2..cp-11 for numeric desc ordering ("cp-10" > "cp-9").
	for n := 2; n <= 11; n++ {
		cp := domain.Checkpoint{
			ID: domain.FormatSimID(domain.SeqCheckpoint, n), WorkspaceID: "ws_cp",
			Label: fmt.Sprintf("检查点%d", n), FilePath: fmt.Sprintf("checkpoints/ws_cp/cp-%d.db", n),
			WorkspaceHash: "hash", CreatedAt: simTS,
		}
		if err := db.Checkpoints.Insert(ctx, cp); err != nil {
			t.Fatalf("Insert %s: %v", cp.ID, err)
		}
	}

	items, err := db.Checkpoints.ListByWorkspace(ctx, "ws_cp")
	if err != nil || len(items) != 11 {
		t.Fatalf("ListByWorkspace = %d items, %v; want 11", len(items), err)
	}
	if items[0].ID != "cp-11" || items[1].ID != "cp-10" || items[2].ID != "cp-9" || items[10].ID != "cp-1" {
		t.Errorf("ListByWorkspace order = %v, want cp-11 cp-10 cp-9 … cp-1", idsOfCheckpoints(items))
	}

	n, err := db.Checkpoints.CountByWorkspace(ctx, "ws_cp")
	if err != nil || n != 11 {
		t.Errorf("CountByWorkspace = %d, %v; want 11", n, err)
	}

	// Delete + 404 paths.
	if err := db.Checkpoints.Delete(ctx, "cp-11"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := db.Checkpoints.GetByID(ctx, "cp-11"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetByID after Delete = %v, want ErrNotFound", err)
	}
	if err := db.Checkpoints.Delete(ctx, "cp-11"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Delete(unknown) = %v, want ErrNotFound", err)
	}
	// Duplicate id → ErrConflict.
	if err := db.Checkpoints.Insert(ctx, cp1); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("duplicate checkpoint insert = %v, want ErrConflict", err)
	}
	// Unknown workspace fails on the FK.
	cpForeign := domain.Checkpoint{ID: "cp-20", WorkspaceID: "ws_missing", Label: "x", FilePath: "p", WorkspaceHash: "h"}
	if err := db.Checkpoints.Insert(ctx, cpForeign); err == nil {
		t.Error("checkpoint into unknown workspace succeeded, want FK error")
	}
}

// idsOfCheckpoints extracts the ids of checkpoints for failure messages.
func idsOfCheckpoints(cps []domain.Checkpoint) []string {
	out := make([]string, len(cps))
	for i, cp := range cps {
		out[i] = cp.ID
	}
	return out
}
