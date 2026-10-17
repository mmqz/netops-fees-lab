package repo

import (
	"context"
	"github.com/mmqz/netops-lab/internal/testsupport"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
)

// withoutV19 temporarily removes migration v19 AND every later migration
// from the registry so the next Open builds a GENUINE v18 database (the W23
// head: detection tables live, no mapping templates). The registry is
// restored via t.Cleanup; the removed tail is returned for Phase 2's
// full-registry reopen (the withoutV18 pattern).
func withoutV19(t *testing.T) []Migration {
	t.Helper()
	restored := append([]Migration(nil), migrations...)
	filtered := make([]Migration, 0, len(migrations))
	var removed []Migration
	for _, m := range migrations {
		if m.Version <= 18 {
			filtered = append(filtered, m)
		} else {
			removed = append(removed, m)
		}
	}
	migrations = filtered
	t.Cleanup(func() { migrations = restored })
	return removed
}

// v19TS is the fixed timestamp of this file's seeds (no clock in the data
// path — the simrepo_test.go convention).
var v19TS = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// v19Template is the probe template document: a confirmed single-sheet
// device mapping (the §8 shape stored by the capture flow).
const v19TemplateMapping = `{"sheets":[` +
	`{"sheet":"设备清单","action":"import","resourceType":"device","keyColumns":["device_code"],` +
	`"columnMapping":{"device_code":"设备编码","device_name":"设备名称","city":"城市",` +
	`"vendor":"厂商","model":"型号","mgmt_ip":"管理IP","status":"状态"}}]}`

// TestMigrationV19AddsMappingTemplates pins the unattended import's template
// store (§12-B7 方案 A, issue #28):
//
//   - on a genuine v18 database (v19 temporarily unregistered) any template
//     insert fails with "no such table" — the regression the migration
//     exists to fix;
//   - re-registering v19 and reopening the SAME file applies it: the table
//     exists, the UNIQUE (header_fingerprint, resource_type) identity
//     enforces one row per sheet-shape (upsert updates, never duplicates),
//     the repo round-trips (upsert / find / list / delete),
//     confirmed_batch is a plain audit column with NO FK (a batch rollback
//     cannot touch the template — the asymmetric lifecycle), the populated
//     v18 data survived byte-identically, and MAX(version) == 19;
//   - a third Open is a bookkeeping no-op (idempotency);
//   - the migration seeds NOTHING: templates are user data born from a
//     human confirmation — the table is empty after a genuine upgrade.
func TestMigrationV19AddsMappingTemplates(t *testing.T) {
	dir := testsupport.TempDir(t)
	path := filepath.Join(dir, "netops.db")
	ctx := context.Background()

	// Phase 1: genuine v18 database with populated import data.
	tail := withoutV19(t)
	db1, err := Open(path)
	if err != nil {
		t.Fatalf("open at v18: %v", err)
	}
	if err := db1.Batches.Create(ctx, newBatch("bat_v19", v19TS)); err != nil {
		t.Fatalf("seed v18 batch: %v", err)
	}
	if err := db1.Do(ctx, func(tx *Tx) error {
		return tx.Resources.Insert(ctx, domain.Resource{
			ID: "res_v19", Type: domain.ResourceTypeDevice, NaturalKey: "DEV-V19",
			Name: "v19设备", Attributes: map[string]string{}, FirstBatchID: "bat_v19",
			CreatedAt: v19TS, UpdatedAt: v19TS,
		})
	}); err != nil {
		t.Fatalf("seed v18 resource: %v", err)
	}
	// Pre-migration regression: the table does not exist.
	if _, err := db1.sql.ExecContext(ctx,
		`INSERT INTO mapping_templates
(template_id, source_name, header_fingerprint, resource_type, mapping_json, sheet_name, confirmed_at, confirmed_batch)
VALUES ('tpl_t','devices.xlsx','fp1','device','{}','设备清单','2026-01-01T00:00:00Z','bat_v19')`); err == nil || !strings.Contains(err.Error(), "no such table") {
		t.Errorf("insert mapping_templates at v18 = %v, want \"no such table\" error", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close v18 db: %v", err)
	}

	// Phase 2: restore the full registry and reopen — v19 applies.
	migrations = append(append([]Migration(nil), migrations...), tail...)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open (v18 → v19): %v", err)
	}
	defer db2.Close()

	// The UNIQUE business identity is (fingerprint, resource_type): a raw
	// second row of the same identity is rejected.
	if _, err := db2.Templates.UpsertTemplate(ctx, domain.MappingTemplate{
		TemplateID: "tpl_a", SourceName: "devices.xlsx", HeaderFingerprint: "fp_v19",
		ResourceType: domain.ResourceTypeDevice, MappingJSON: v19TemplateMapping,
		SheetName: "设备清单", ConfirmedAt: v19TS, ConfirmedBatch: "bat_v19",
	}); err != nil {
		t.Fatalf("upsert template: %v", err)
	}
	if _, err := db2.sql.ExecContext(ctx,
		`INSERT INTO mapping_templates
(template_id, source_name, header_fingerprint, resource_type, mapping_json, sheet_name, confirmed_at, confirmed_batch)
VALUES ('tpl_b','other.xlsx','fp_v19','device','{}','设备清单','2026-01-02T00:00:00Z','bat_x')`); err == nil || !strings.Contains(err.Error(), "UNIQUE") {
		t.Errorf("duplicate (fingerprint, resource_type) insert = %v, want UNIQUE constraint error", err)
	}
	// The SAME fingerprint with a DIFFERENT resource type is a legal second
	// identity (the same sheet shape confirmed for another type).
	if _, err := db2.Templates.UpsertTemplate(ctx, domain.MappingTemplate{
		TemplateID: "tpl_c", SourceName: "other.xlsx", HeaderFingerprint: "fp_v19",
		ResourceType: domain.ResourceTypePort, MappingJSON: "{}",
		SheetName: "设备清单", ConfirmedAt: v19TS.Add(24 * time.Hour), ConfirmedBatch: "bat_v19",
	}); err != nil {
		t.Fatalf("upsert same-fingerprint other-type template: %v", err)
	}
	// FindByFingerprint tie-break: both rows share fp_v19; the port row was
	// confirmed later, so it wins (confirmed_at DESC).
	got, err := db2.Templates.FindByFingerprint(ctx, "fp_v19")
	if err != nil || got.TemplateID != "tpl_c" {
		t.Errorf("FindByFingerprint = %+v (err %v), want the newest tie-break tpl_c", got, err)
	}

	// The repo round-trip: re-confirmation UPSERTS in place (no new row, the
	// original template_id survives, mapping_json + confirmed_at replaced).
	re := domain.MappingTemplate{
		TemplateID: "tpl_a2", SourceName: "devices-v2.xlsx", HeaderFingerprint: "fp_v19",
		ResourceType: domain.ResourceTypeDevice, MappingJSON: v19TemplateMapping,
		SheetName: "设备清单", ConfirmedAt: v19TS.Add(48 * time.Hour), ConfirmedBatch: "bat_v19",
	}
	created, err := db2.Templates.UpsertTemplate(ctx, re)
	if err != nil || created {
		t.Errorf("re-confirm upsert = created %v err %v, want created=false (idempotent update)", created, err)
	}
	after, err := db2.Templates.GetTemplate(ctx, "tpl_a")
	if err != nil || after.MappingJSON != v19TemplateMapping || !after.ConfirmedAt.Equal(re.ConfirmedAt) ||
		after.SourceName != "devices-v2.xlsx" {
		t.Errorf("template after re-confirm = %+v (err %v), want updated in place under the original id", after, err)
	}
	// FindByFingerprint now returns the UPDATED device row (still newest
	// overall: 48h > 24h).
	got, err = db2.Templates.FindByFingerprint(ctx, "fp_v19")
	if err != nil || got.TemplateID != "tpl_a" || got.SourceName != "devices-v2.xlsx" {
		t.Errorf("FindByFingerprint after re-confirm = %+v (err %v), want the updated tpl_a", got, err)
	}

	// Unknown fingerprint answers ErrNotFound.
	if _, err := db2.Templates.FindByFingerprint(ctx, "no_such_fp"); err == nil || !strings.Contains(err.Error(), domain.ErrNotFound.Error()) {
		t.Errorf("FindByFingerprint unknown = %v, want ErrNotFound", err)
	}

	// The listing: newest-first, deterministic, with the total.
	templates, total, err := db2.Templates.ListTemplates(ctx, 0, 0)
	if err != nil || total != 2 || len(templates) != 2 {
		t.Fatalf("ListTemplates = %d rows (total %d, err %v), want 2", len(templates), total, err)
	}
	if templates[0].TemplateID != "tpl_a" || templates[1].TemplateID != "tpl_c" {
		t.Errorf("ListTemplates order = [%s, %s], want newest-first [tpl_a, tpl_c]",
			templates[0].TemplateID, templates[1].TemplateID)
	}
	if templates[0].ConfirmedBatch != "bat_v19" || templates[0].SheetName != "设备清单" {
		t.Errorf("round-tripped template fields = %+v, want confirmed_batch + sheet_name preserved", templates[0])
	}

	// Delete round-trip; unknown id answers ErrNotFound.
	if err := db2.Templates.DeleteTemplate(ctx, "tpl_c"); err != nil {
		t.Fatalf("DeleteTemplate: %v", err)
	}
	if err := db2.Templates.DeleteTemplate(ctx, "tpl_c"); err == nil || !strings.Contains(err.Error(), domain.ErrNotFound.Error()) {
		t.Errorf("double DeleteTemplate = %v, want ErrNotFound", err)
	}

	// Populated v18 data survived byte-identically.
	res, err := db2.Resources.GetByID(ctx, "res_v19")
	if err != nil || res.NaturalKey != "DEV-V19" || !res.UpdatedAt.Equal(v19TS) {
		t.Errorf("resource after v19 = %+v (err %v), want byte-identical survival", res, err)
	}
	bat, err := db2.Batches.Get(ctx, "bat_v19")
	if err != nil || bat.Status != domain.BatchStatusAnalyzing || !bat.StartedAt.Equal(v19TS) {
		t.Errorf("batch after v19 = %+v (err %v), want byte-identical survival", bat, err)
	}

	// Rollback asymmetry at the storage layer: deleting the batch's data
	// and the batch row itself (what a rollback does) never touches the
	// template — confirmed_batch is plain TEXT with NO FK, so nothing
	// cascades and nothing blocks.
	if _, err := db2.sql.ExecContext(ctx, `DELETE FROM resources WHERE id = 'res_v19'`); err != nil {
		t.Fatalf("delete batch resource: %v", err)
	}
	if _, err := db2.sql.ExecContext(ctx, `DELETE FROM import_batches WHERE id = 'bat_v19'`); err != nil {
		t.Fatalf("delete creating batch: %v", err)
	}
	if _, err := db2.Templates.GetTemplate(ctx, "tpl_a"); err != nil {
		t.Errorf("template after creating-batch deletion = %v, want survival (no FK, audit column)", err)
	}

	// The migration seeds nothing (templates are user data).
	var n int
	if err := db2.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM mapping_templates`).Scan(&n); err != nil {
		t.Fatalf("count templates: %v", err)
	}
	if n != 1 { // tpl_a only — everything the TEST created; zero seeded rows
		t.Errorf("templates = %d, want 1 (only the test's row — the migration seeds nothing)", n)
	}

	// MAX(version) follows the registry head.
	var maxV int
	if err := db2.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&maxV); err != nil {
		t.Fatalf("max version: %v", err)
	}
	if maxV != 28 {
		t.Errorf("max schema version after upgrade = %d, want 28", maxV)
	}

	// Phase 3: reopening is a bookkeeping no-op (a second CREATE TABLE
	// would fail on the duplicate table, so a clean reopen proves the
	// bookkeeping).
	db3, err := Open(path)
	if err != nil {
		t.Fatalf("third Open (idempotency): %v", err)
	}
	defer db3.Close()
	if err := db3.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&maxV); err != nil {
		t.Fatalf("max version after reopen: %v", err)
	}
	if maxV != 28 {
		t.Errorf("max schema version after reopen = %d, want 28", maxV)
	}
	if _, err := db3.Templates.GetTemplate(ctx, "tpl_a"); err != nil {
		t.Errorf("GetTemplate after reopen: %v", err)
	}
}
