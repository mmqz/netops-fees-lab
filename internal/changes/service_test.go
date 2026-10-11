package changes

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/importer"
	"github.com/mmqz/netops-lab/internal/mapping"
	"github.com/mmqz/netops-lab/internal/repo"
	"github.com/xuri/excelize/v2"
)

// newSeededService opens a fresh database, imports a one-device workbook,
// re-imports the same device with a changed mgmt_ip + vendor (one pending
// proposal per field) and returns the changes service plus the repository.
// The two W005 proposals share the same resource.
func newSeededService(t *testing.T) (*Service, *repo.DB, domain.Resource) {
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
	imp := importer.New(db)
	imp.Now = func() time.Time { return time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC) }
	ctx := context.Background()

	deviceMapping := func() mapping.Mapping {
		return mapping.Mapping{Sheets: []mapping.SheetMapping{{
			Sheet: "设备清单", Action: mapping.ActionImport, ResourceType: domain.ResourceTypeDevice,
			KeyColumns: []string{"device_code"},
			ColumnMapping: map[string]string{
				"device_code": "设备编码", "device_name": "设备名称", "city": "城市",
				"vendor": "厂商", "model": "型号", "mgmt_ip": "管理IP", "status": "状态",
			},
		}}}
	}
	importOne := func(ip, vendor string) {
		t.Helper()
		f := excelize.NewFile()
		if err := f.SetSheetName("Sheet1", "设备清单"); err != nil {
			t.Fatalf("rename default sheet: %v", err)
		}
		rows := [][]string{
			{"设备编码", "设备名称", "城市", "厂商", "型号", "管理IP", "状态"},
			{"DEV-001", "核心路由1", "杭州", vendor, "NE40E", ip, "在网"},
		}
		for r, row := range rows {
			for c, v := range row {
				cell, err := excelize.CoordinatesToCellName(c+1, r+1)
				if err != nil {
					t.Fatalf("cell name: %v", err)
				}
				if err := f.SetCellStr("设备清单", cell, v); err != nil {
					t.Fatalf("set cell: %v", err)
				}
			}
		}
		path := filepath.Join(t.TempDir(), "devices.xlsx")
		if err := f.SaveAs(path); err != nil {
			t.Fatalf("save workbook: %v", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("close workbook: %v", err)
		}
		a, err := imp.Analyze(ctx, path)
		if err != nil {
			t.Fatalf("Analyze: %v", err)
		}
		if _, err := imp.Execute(ctx, a.BatchID, deviceMapping()); err != nil {
			t.Fatalf("Execute: %v", err)
		}
	}
	importOne("10.0.0.1", "华为")
	importOne("10.0.0.9", "中兴") // skipped row → mgmt_ip + vendor pending

	res, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, "DEV-001")
	if err != nil {
		t.Fatalf("GetByTypeKey: %v", err)
	}
	chs, total, err := db.AttributeChanges.List(ctx, repo.ChangeFilter{Status: domain.ChangeStatusPending})
	if err != nil || total != 2 || len(chs) != 2 {
		t.Fatalf("seeded pending changes = %d (total %d, err %v), want 2", len(chs), total, err)
	}
	return New(db), db, res
}

// TestApplyWritesAttributeAndFlipsStatus pins the successful apply: the new
// value lands in the resource attributes, updated_at refreshes, the
// proposal flips to applied with applied_at — atomically.
func TestApplyWritesAttributeAndFlipsStatus(t *testing.T) {
	svc, db, res := newSeededService(t)
	ctx := context.Background()
	svc.Now = func() time.Time { return time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC) }

	ipChange, _, err := db.AttributeChanges.List(ctx, repo.ChangeFilter{
		Status: domain.ChangeStatusPending, ResourceID: res.ID})
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	var target domain.AttributeChange
	for _, ch := range ipChange {
		if ch.Field == "mgmt_ip" {
			target = ch
		}
	}
	if target.ID == "" {
		t.Fatalf("no mgmt_ip proposal found")
	}

	item, err := svc.Apply(ctx, target.ID)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if item.Status != "applied" || item.Field != "mgmt_ip" || item.OldValue != "10.0.0.1" ||
		item.NewValue != "10.0.0.9" || item.AppliedAt == nil || !item.AppliedAt.Equal(time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("applied item = %+v, want applied mgmt_ip 10.0.0.1→10.0.0.9 at fixed ts", item)
	}
	if item.ResourceType != "device" || item.NaturalKey != "DEV-001" {
		t.Errorf("applied item join = %s/%s, want device/DEV-001", item.ResourceType, item.NaturalKey)
	}
	after, err := db.Resources.GetByID(ctx, res.ID)
	if err != nil || after.Attributes["mgmt_ip"] != "10.0.0.9" {
		t.Errorf("resource after apply = %v (err %v), want mgmt_ip 10.0.0.9", after, err)
	}
	if !after.UpdatedAt.After(res.UpdatedAt) {
		t.Errorf("updated_at %v must be refreshed (was %v)", after.UpdatedAt, res.UpdatedAt)
	}
	// A non-name field must NOT touch resources.name (mgmt_ip is not the
	// device NameField — the W9-D sync is scoped to the name field).
	if after.Name != "核心路由1" {
		t.Errorf("resource name after non-name-field apply = %q, want 核心路由1 (untouched)", after.Name)
	}

	// A second apply of the same (now applied) proposal → 409 conflict.
	if _, err := svc.Apply(ctx, target.ID); err == nil || !errors.Is(err, domain.ErrConflict) {
		t.Errorf("re-Apply = %v, want domain.ErrConflict", err)
	}
	// Apply of an unknown id → 404.
	if _, err := svc.Apply(ctx, "chg_nope"); err == nil || !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Apply unknown = %v, want domain.ErrNotFound", err)
	}
}

// TestApplyStaleConflict pins the StaleError path: applying the second
// proposal of a field whose value another apply already moved answers a
// typed StaleError (→ HTTP 409 attribute_conflict) and touches nothing.
func TestApplyStaleConflict(t *testing.T) {
	svc, db, res := newSeededService(t)
	ctx := context.Background()
	// seededBatchID is the real import batch of the seeded proposals (the
	// batch_id FK requires an existing row).
	seeded, _, err := db.AttributeChanges.List(ctx, repo.ChangeFilter{
		Status: domain.ChangeStatusPending, ResourceID: res.ID})
	if err != nil || len(seeded) != 2 {
		t.Fatalf("seeded proposals = %d (err %v), want 2", len(seeded), err)
	}
	seededBatchID := seeded[0].BatchID

	// Create a SECOND pending proposal on mgmt_ip with another new value:
	// the row was re-imported once more with yet another IP.
	// (10.0.0.1 → 10.0.0.9 pending; now also 10.0.0.1 → 10.0.0.10 pending.)
	err = db.Do(ctx, func(tx *repo.Tx) error {
		return tx.AttributeChanges.InsertBatch(ctx, []domain.AttributeChange{{
			ID: "chg_extra", ResourceID: res.ID, BatchID: seededBatchID,
			Field: "mgmt_ip", OldValue: "10.0.0.1", NewValue: "10.0.0.10",
			Status: domain.ChangeStatusPending, Sheet: "设备清单", RowNumber: 2,
			CreatedAt: time.Date(2026, 11, 3, 11, 0, 0, 0, time.UTC),
		}})
	})
	if err != nil {
		t.Fatalf("insert extra proposal: %v", err)
	}

	first, err := db.AttributeChanges.Get(ctx, "chg_extra")
	_ = first
	// Apply the ORIGINAL 10.0.0.9 proposal.
	pend, _, err := db.AttributeChanges.List(ctx, repo.ChangeFilter{
		Status: domain.ChangeStatusPending, ResourceID: res.ID})
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	var orig domain.AttributeChange
	for _, ch := range pend {
		if ch.NewValue == "10.0.0.9" {
			orig = ch
		}
	}
	if _, err := svc.Apply(ctx, orig.ID); err != nil {
		t.Fatalf("Apply first: %v", err)
	}

	// Now the extra proposal is stale: the current value is 10.0.0.9, its
	// old value is 10.0.0.1.
	var stale *StaleError
	_, err = svc.Apply(ctx, "chg_extra")
	if err == nil || !errors.As(err, &stale) {
		t.Fatalf("stale Apply = %v, want *changes.StaleError", err)
	}
	if stale.Field != "mgmt_ip" || stale.Current != "10.0.0.9" || stale.Old != "10.0.0.1" {
		t.Errorf("stale error = %+v, want mgmt_ip current 10.0.0.9 old 10.0.0.1", stale)
	}
	if !containsStr(stale.Error(), "10.0.0.9") || !containsStr(stale.Error(), "10.0.0.1") || !containsStr(stale.Error(), "mgmt_ip") {
		t.Errorf("stale message %q must name the field and both values", stale.Error())
	}
	// The stale proposal is still pending; the resource keeps 10.0.0.9.
	extra, err := db.AttributeChanges.Get(ctx, "chg_extra")
	if err != nil || extra.Status != domain.ChangeStatusPending {
		t.Errorf("extra after stale apply = %+v (err %v), want still pending", extra, err)
	}
	after, err := db.Resources.GetByID(ctx, res.ID)
	if err != nil || after.Attributes["mgmt_ip"] != "10.0.0.9" {
		t.Errorf("resource after stale apply = %v (err %v), want mgmt_ip 10.0.0.9 unchanged", after, err)
	}
}

// TestDismissKeepsAuditTrail pins the dismiss flow: the resource is never
// touched, the proposal flips to dismissed with a NULL applied_at.
func TestDismissKeepsAuditTrail(t *testing.T) {
	svc, db, res := newSeededService(t)
	ctx := context.Background()

	pend, _, err := db.AttributeChanges.List(ctx, repo.ChangeFilter{
		Status: domain.ChangeStatusPending, ResourceID: res.ID})
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	var target domain.AttributeChange
	for _, ch := range pend {
		if ch.Field == "vendor" {
			target = ch
		}
	}
	item, err := svc.Dismiss(ctx, target.ID)
	if err != nil {
		t.Fatalf("Dismiss: %v", err)
	}
	if item.Status != "dismissed" || item.AppliedAt != nil || item.Field != "vendor" {
		t.Errorf("dismissed item = %+v, want dismissed vendor with NULL applied_at", item)
	}
	after, err := db.Resources.GetByID(ctx, res.ID)
	if err != nil || after.Attributes["vendor"] != "华为" || !after.UpdatedAt.Equal(res.UpdatedAt) {
		t.Errorf("resource after dismiss = %v (err %v), want untouched vendor 华为", after, err)
	}
	// Dismissing again → conflict; unknown id → not found.
	if _, err := svc.Dismiss(ctx, target.ID); err == nil || !errors.Is(err, domain.ErrConflict) {
		t.Errorf("re-Dismiss = %v, want domain.ErrConflict", err)
	}
	if _, err := svc.Dismiss(ctx, "chg_nope"); err == nil || !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Dismiss unknown = %v, want domain.ErrNotFound", err)
	}
}

// TestListFiltersAndPagination pins the list envelope: status/resource/batch
// filters, limit/offset windows, the chronological order and the
// resource-side join.
func TestListFiltersAndPagination(t *testing.T) {
	svc, db, res := newSeededService(t)
	ctx := context.Background()

	// All statuses: 2 pending.
	all, err := svc.List(ctx, Filter{})
	if err != nil || all.Total != 2 || len(all.Items) != 2 {
		t.Fatalf("List all = %d items (total %d, err %v), want 2", len(all.Items), all.Total, err)
	}
	if all.Items[0].Field != "vendor" || all.Items[1].Field != "mgmt_ip" {
		// Insertion order within one row = spec field order: vendor precedes
		// mgmt_ip in the device schema.
		t.Errorf("List order = %s, %s, want vendor then mgmt_ip", all.Items[0].Field, all.Items[1].Field)
	}
	if all.Items[0].ResourceType != "device" || all.Items[0].NaturalKey != "DEV-001" ||
		all.Items[0].BatchID == "" || all.Items[0].Sheet != "设备清单" || all.Items[0].RowNumber != 2 {
		t.Errorf("joined item = %+v, want device/DEV-001 join with batch and origin", all.Items[0])
	}

	// Status filter.
	pending, err := svc.List(ctx, Filter{Status: domain.ChangeStatusPending})
	if err != nil || pending.Total != 2 {
		t.Errorf("List pending = total %d (err %v), want 2", pending.Total, err)
	}
	// Resource filter (a bogus id matches nothing).
	other, err := svc.List(ctx, Filter{ResourceID: "res_nope"})
	if err != nil || other.Total != 0 || len(other.Items) != 0 {
		t.Errorf("List by unknown resource = total %d (err %v), want 0", other.Total, err)
	}
	// Batch filter via the seeded proposals' own batch.
	batch := all.Items[0].BatchID
	byBatch, err := svc.List(ctx, Filter{BatchID: batch})
	if err != nil || byBatch.Total != 2 {
		t.Errorf("List by batch = total %d (err %v), want 2", byBatch.Total, err)
	}

	// Pagination: window of 1, both offsets; limit clamps and defaults.
	w1, err := svc.List(ctx, Filter{Limit: 1, Offset: 0})
	if err != nil || w1.Total != 2 || len(w1.Items) != 1 || w1.Items[0].Field != "vendor" {
		t.Errorf("List limit=1 = %+v (err %v), want vendor", w1.Items, err)
	}
	w2, err := svc.List(ctx, Filter{Limit: 1, Offset: 1})
	if err != nil || w2.Total != 2 || len(w2.Items) != 1 || w2.Items[0].Field != "mgmt_ip" {
		t.Errorf("List limit=1 offset=1 = %+v (err %v), want mgmt_ip", w2.Items, err)
	}
	// The service window defaults are exercised via Filter zero values
	// (limit 0 → 20) and negative offsets clamp to 0.
	neg, err := svc.List(ctx, Filter{Offset: -5})
	if err != nil || neg.Total != 2 || len(neg.Items) != 2 {
		t.Errorf("List offset=-5 = %d items (err %v), want 2 (clamped)", len(neg.Items), err)
	}

	// A proposal whose resource vanished keeps the literal ids (rollback
	// cascade scenario).
	if err := db.Do(ctx, func(tx *repo.Tx) error {
		_, err := tx.Resources.DeleteByBatch(ctx, res.FirstBatchID)
		return err
	}); err != nil {
		t.Fatalf("delete resource batch: %v", err)
	}
	post, err := svc.List(ctx, Filter{Status: domain.ChangeStatusPending})
	if err != nil || post.Total != 0 {
		t.Fatalf("List pending after resource cascade = total %d (err %v), want 0", post.Total, err)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestApplyNameFieldSyncsResourceName pins the W9-D name sync of the SAME
// semantic family the rename apply got: a confirmed change on the type's
// display-name field (device_name here) rewrites resources.name too — the
// name is the projection of that attribute, and before W9-D an applied
// rename of the name attribute left the resource list showing the old
// display name forever.
func TestApplyNameFieldSyncsResourceName(t *testing.T) {
	db, err := repo.Open(filepath.Join(t.TempDir(), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close: %v", err)
		}
	})
	imp := importer.New(db)
	imp.Now = func() time.Time { return time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC) }
	ctx := context.Background()

	deviceMapping := mapping.Mapping{Sheets: []mapping.SheetMapping{{
		Sheet: "设备清单", Action: mapping.ActionImport, ResourceType: domain.ResourceTypeDevice,
		KeyColumns: []string{"device_code"},
		ColumnMapping: map[string]string{
			"device_code": "设备编码", "device_name": "设备名称", "city": "城市",
			"vendor": "厂商", "model": "型号", "mgmt_ip": "管理IP", "status": "状态",
		},
	}}}
	importNamed := func(name string) {
		t.Helper()
		f := excelize.NewFile()
		if err := f.SetSheetName("Sheet1", "设备清单"); err != nil {
			t.Fatalf("rename default sheet: %v", err)
		}
		rows := [][]string{
			{"设备编码", "设备名称", "城市", "厂商", "型号", "管理IP", "状态"},
			{"DEV-001", name, "杭州", "华为", "NE40E", "10.0.0.1", "在网"},
		}
		for r, row := range rows {
			for c, v := range row {
				cell, err := excelize.CoordinatesToCellName(c+1, r+1)
				if err != nil {
					t.Fatalf("cell name: %v", err)
				}
				if err := f.SetCellStr("设备清单", cell, v); err != nil {
					t.Fatalf("set cell: %v", err)
				}
			}
		}
		path := filepath.Join(t.TempDir(), "devices.xlsx")
		if err := f.SaveAs(path); err != nil {
			t.Fatalf("save workbook: %v", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("close workbook: %v", err)
		}
		a, err := imp.Analyze(ctx, path)
		if err != nil {
			t.Fatalf("Analyze: %v", err)
		}
		if _, err := imp.Execute(ctx, a.BatchID, deviceMapping); err != nil {
			t.Fatalf("Execute: %v", err)
		}
	}
	importNamed("核心路由1")
	importNamed("核心路由1改") // same key, new device_name → skip + pending W005

	res, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, "DEV-001")
	if err != nil {
		t.Fatalf("GetByTypeKey: %v", err)
	}
	if res.Name != "核心路由1" {
		t.Fatalf("seeded resource name = %q, want 核心路由1 (the name is the device_name projection)", res.Name)
	}
	chs, total, err := db.AttributeChanges.List(ctx, repo.ChangeFilter{Status: domain.ChangeStatusPending})
	if err != nil || total != 1 || len(chs) != 1 {
		t.Fatalf("pending changes = %d (total %d, err %v), want exactly the device_name proposal", len(chs), total, err)
	}
	if chs[0].Field != "device_name" || chs[0].OldValue != "核心路由1" || chs[0].NewValue != "核心路由1改" {
		t.Fatalf("pending proposal = %+v, want device_name 核心路由1→核心路由1改", chs[0])
	}

	svc := New(db)
	svc.Now = func() time.Time { return time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC) }
	item, err := svc.Apply(ctx, chs[0].ID)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if item.Field != "device_name" || item.Status != "applied" {
		t.Fatalf("applied item = %+v, want the applied device_name proposal", item)
	}

	after, err := db.Resources.GetByID(ctx, res.ID)
	if err != nil {
		t.Fatalf("GetByID after apply: %v", err)
	}
	if after.Attributes["device_name"] != "核心路由1改" {
		t.Errorf("attributes after apply = %v, want device_name 核心路由1改", after.Attributes)
	}
	if after.Name != "核心路由1改" {
		t.Errorf("resource NAME after a name-field apply = %q, want 核心路由1改 (the W9-D sync)", after.Name)
	}
}
