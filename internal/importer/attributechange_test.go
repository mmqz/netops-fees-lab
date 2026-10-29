package importer

import (
	"context"
	"testing"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/repo"
)

// pendingChanges lists the pending attribute changes of the whole database.
func pendingChanges(t *testing.T, db *repo.DB) []domain.AttributeChange {
	t.Helper()
	chs, total, err := db.AttributeChanges.List(context.Background(), repo.ChangeFilter{
		Status: domain.ChangeStatusPending,
	})
	if err != nil {
		t.Fatalf("list pending changes: %v", err)
	}
	if total != len(chs) {
		t.Fatalf("pending total %d != len %d", total, len(chs))
	}
	return chs
}

// w005Issues lists every W005 issue of the database.
func w005Issues(t *testing.T, db *repo.DB) []domain.Issue {
	t.Helper()
	issues, _, err := db.Issues.List(context.Background(), repo.IssueFilter{RuleCode: domain.RuleAttributeChange})
	if err != nil {
		t.Fatalf("list W005 issues: %v", err)
	}
	return issues
}

// changesOfField filters chs to one field, asserting a count of want.
func changesOfField(t *testing.T, chs []domain.AttributeChange, field string, want int) []domain.AttributeChange {
	t.Helper()
	var out []domain.AttributeChange
	for _, ch := range chs {
		if ch.Field == field {
			out = append(out, ch)
		}
	}
	if len(out) != want {
		t.Fatalf("changes of field %s = %d, want %d (%+v)", field, len(out), want, chs)
	}
	return out
}

// TestImportAttributeChangeDiff is the W005 end-to-end contract test of the
// update-on-diff import mode (v0.4 W4-D): a re-imported row whose natural
// key hits an existing resource but whose attribute values differ is STILL
// skipped (row semantics unchanged, E001 rule code kept), while every
// differing field lands as a pending change proposal plus one W005 warning
// issue. Idempotency: repeating a diff that is already pending records
// nothing (no change, no issue); a diff whose new value changed again adds
// a second pending proposal; the rollback deletes the batch's pending
// proposals while applied ones survive.
func TestImportAttributeChangeDiff(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()

	// Deterministic, strictly increasing clock: the multi-pending queue
	// order (created_at, rowid) must be stable in this test; a real
	// import's same-second ties are broken by rowid (insertion order).
	base := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	run := 0
	svc.Now = func() time.Time { run++; return base.Add(time.Duration(run) * time.Minute) }

	// ---- Run 1: import two devices (mgmt_ip 10.0.0.1 / 10.0.1.1). ----
	b1, sum1 := importSheet(t, svc, [][]string{
		attrChangeHeaders,
		attrChangeRow("DEV-001", "核心路由1", "杭州", "华为", "NE40E", "10.0.0.1", "在网"),
		attrChangeRow("DEV-002", "核心路由2", "宁波", "中兴", "ZXR10", "10.0.1.1", "在网"),
	})
	if sum1.Status != domain.BatchStatusCompleted || sum1.RowsImported != 2 || sum1.RowsSkipped != 0 {
		t.Fatalf("run 1 summary = %+v, want 2 imported / 0 skipped", sum1)
	}
	if chs := pendingChanges(t, db); len(chs) != 0 {
		t.Fatalf("pending changes after first import = %d, want 0", len(chs))
	}
	dev1, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, "DEV-001")
	if err != nil {
		t.Fatalf("GetByTypeKey(DEV-001): %v", err)
	}

	// ---- Run 2: DEV-001 re-homed to 10.0.0.9; DEV-002 identical. ----
	b2, sum2 := importSheet(t, svc, [][]string{
		attrChangeHeaders,
		attrChangeRow("DEV-001", "核心路由1", "杭州", "华为", "NE40E", "10.0.0.9", "在网"),
		attrChangeRow("DEV-002", "核心路由2", "宁波", "中兴", "ZXR10", "10.0.1.1", "在网"),
	})
	_ = b1
	if sum2.RowsImported != 0 || sum2.RowsSkipped != 2 || sum2.RowsFailed != 0 {
		t.Fatalf("run 2 summary = %+v, want 0 imported / 2 skipped / 0 failed", sum2)
	}
	// The row result keeps the classic duplicate-import semantics.
	rows2, _, err := db.RowResults.List(ctx, repo.RowResultFilter{BatchID: b2})
	if err != nil || len(rows2) != 2 {
		t.Fatalf("run 2 row results = %d (err %v), want 2", len(rows2), err)
	}
	for _, rr := range rows2 {
		if rr.Status != domain.RowStatusSkipped || rr.EntityID != dev1.ID && rr.EntityID == "" {
			t.Errorf("run 2 row %+v: status/entity mismatch, want skipped with entity", rr)
		}
		if rr.Status == domain.RowStatusSkipped && rr.RuleCode != domain.RuleDuplicateKey {
			t.Errorf("run 2 skipped row rule code = %q, want E001 (unchanged semantics)", rr.RuleCode)
		}
	}

	// One pending change: field/old/new/batch/sheet/row all pinned.
	chs := changesOfField(t, pendingChanges(t, db), "mgmt_ip", 1)
	ch := chs[0]
	if ch.ResourceID != dev1.ID || ch.OldValue != "10.0.0.1" || ch.NewValue != "10.0.0.9" ||
		ch.BatchID != b2 || ch.Sheet != "设备清单" || ch.RowNumber != 2 || ch.AppliedAt != nil {
		t.Fatalf("pending change = %+v, want DEV-001 mgmt_ip 10.0.0.1→10.0.0.9 of batch %s sheet 设备清单 row 2", ch, b2)
	}

	// One W005 issue with the full evidence.
	w005 := w005Issues(t, db)
	if len(w005) != 1 {
		t.Fatalf("W005 issues after run 2 = %d, want 1", len(w005))
	}
	iss := w005[0]
	if iss.Severity != domain.SeverityWarning || iss.EntityKind != domain.EntityKindResource ||
		iss.EntityID != dev1.ID || iss.BatchID == nil || *iss.BatchID != b2 {
		t.Fatalf("W005 issue = %+v, want warning on resource DEV-001 of batch %s", iss, b2)
	}
	for key, want := range map[string]string{
		"type": "device", "natural_key": "DEV-001", "resource_id": dev1.ID,
		"fields": "mgmt_ip", "change_count": "1", "sheet": "设备清单", "row_number": "2",
	} {
		if iss.Evidence[key] != want {
			t.Errorf("W005 evidence[%s] = %q, want %q", key, iss.Evidence[key], want)
		}
	}

	// The resource itself is untouched (skip never writes).
	if dev, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, "DEV-001"); err != nil ||
		dev.Attributes["mgmt_ip"] != "10.0.0.1" || !dev.UpdatedAt.Equal(dev1.UpdatedAt) {
		t.Errorf("DEV-001 after diff import = %v (err %v), want untouched mgmt_ip 10.0.0.1", dev, err)
	}

	// ---- Run 3: identical re-import of run 2 → zero new records/issues. ----
	_, sum3 := importSheet(t, svc, [][]string{
		attrChangeHeaders,
		attrChangeRow("DEV-001", "核心路由1", "杭州", "华为", "NE40E", "10.0.0.9", "在网"),
		attrChangeRow("DEV-002", "核心路由2", "宁波", "中兴", "ZXR10", "10.0.1.1", "在网"),
	})
	if sum3.RowsSkipped != 2 {
		t.Fatalf("run 3 summary = %+v, want 2 skipped", sum3)
	}
	if chs := pendingChanges(t, db); len(chs) != 1 {
		t.Fatalf("pending changes after run 3 = %d, want 1 (same diff deduplicated)", len(chs))
	}
	if n := len(w005Issues(t, db)); n != 1 {
		t.Fatalf("W005 issues after run 3 = %d, want 1 (deduplicated)", n)
	}

	// ---- Run 4: multi-field diff + empty new value. ----
	// vendor and model change (new pending), mgmt_ip repeats the pending
	// 10.0.0.9 (deduplicated), city is EMPTY (never recorded — the
	// "empty never overwrites" import semantics).
	b4, sum4 := importSheet(t, svc, [][]string{
		attrChangeHeaders,
		attrChangeRow("DEV-001", "核心路由1", "", "中兴", "NE9000", "10.0.0.9", "在网"),
		attrChangeRow("DEV-002", "核心路由2", "宁波", "中兴", "ZXR10", "10.0.1.1", "在网"),
	})
	if sum4.RowsSkipped != 2 {
		t.Fatalf("run 4 summary = %+v, want 2 skipped", sum4)
	}
	chs = pendingChanges(t, db)
	changesOfField(t, chs, "mgmt_ip", 1) // still only the 10.0.0.9 proposal
	changesOfField(t, chs, "vendor", 1)
	changesOfField(t, chs, "model", 1)
	if len(chs) != 3 {
		t.Fatalf("pending changes after run 4 = %d, want 3 (mgmt_ip + vendor + model)", len(chs))
	}
	vendorCh := changesOfField(t, chs, "vendor", 1)[0]
	if vendorCh.OldValue != "华为" || vendorCh.NewValue != "中兴" || vendorCh.BatchID != b4 {
		t.Errorf("vendor change = %+v, want 华为→中兴 of batch %s", vendorCh, b4)
	}
	if cityChs := changesOfField(t, chs, "city", 0); cityChs != nil {
		t.Errorf("city changes = %v, want none (empty new value)", cityChs)
	}
	if n := len(w005Issues(t, db)); n != 2 {
		t.Fatalf("W005 issues after run 4 = %d, want 2 (one per diff row)", n)
	}

	// ---- Run 5: the value changed AGAIN while pending → a second
	// proposal on the same field coexists (created_at order). ----
	_, sum5 := importSheet(t, svc, [][]string{
		attrChangeHeaders,
		attrChangeRow("DEV-001", "核心路由1", "杭州", "中兴", "NE9000", "10.0.0.10", "在网"),
		attrChangeRow("DEV-002", "核心路由2", "宁波", "中兴", "ZXR10", "10.0.1.1", "在网"),
	})
	if sum5.RowsSkipped != 2 {
		t.Fatalf("run 5 summary = %+v, want 2 skipped", sum5)
	}
	all5 := pendingChanges(t, db)
	if len(all5) != 4 {
		t.Fatalf("pending changes after run 5 = %d, want 4 (mgmt_ip x2 + vendor + model; same-value diffs deduplicated)", len(all5))
	}
	ipChs := changesOfField(t, all5, "mgmt_ip", 2)
	if ipChs[0].NewValue != "10.0.0.9" || ipChs[1].NewValue != "10.0.0.10" {
		t.Errorf("mgmt_ip pendings = %v, %v, want 10.0.0.9 then 10.0.0.10 (created_at order)", ipChs[0].NewValue, ipChs[1].NewValue)
	}

	// ---- Rollback asymmetry: pending dies with its batch, applied
	// survives. Apply the vendor proposal of run 4 at the repo level (the
	// full HTTP apply path is pinned by the api/changes tests), then roll
	// back run 4. ----
	if err := db.Do(ctx, func(tx *repo.Tx) error {
		return tx.AttributeChanges.MarkApplied(ctx, vendorCh.ID, simTSImporter)
	}); err != nil {
		t.Fatalf("MarkApplied(vendor): %v", err)
	}
	if err := svc.Rollback(ctx, b4); err != nil {
		t.Fatalf("Rollback(%s): %v", b4, err)
	}
	chs = pendingChanges(t, db)
	// Run 4's pendings (model) are gone; run 2's mgmt_ip(10.0.0.9) and run
	// 5's mgmt_ip(10.0.0.10) survive (other batches).
	if len(chs) != 2 {
		t.Fatalf("pending changes after rollback of run 4 = %d, want 2 (model deleted, two mgmt_ip kept)", len(chs))
	}
	changesOfField(t, chs, "model", 0)
	// The applied vendor change survived its batch's rollback.
	appliedVendor, err := db.AttributeChanges.Get(ctx, vendorCh.ID)
	if err != nil || appliedVendor.Status != domain.ChangeStatusApplied || appliedVendor.AppliedAt == nil {
		t.Errorf("applied vendor change after rollback = %+v (err %v), want applied (audit trail survives)", appliedVendor, err)
	}
}

// simTSImporter is a fixed timestamp for repo-level status flips in this
// test file.
var simTSImporter = time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)

// TestImportAttributeChangeDiffRollbackWithOwnResource pins the cascade
// side of the rollback asymmetry: a batch that CREATED the resource is
// rolled back → the resource disappears → its pending proposals vanish via
// the ON DELETE CASCADE even when the pending-delete-by-batch statement
// already removed the rest.
func TestImportAttributeChangeDiffRollbackWithOwnResource(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()

	b1, sum1 := importSheet(t, svc, [][]string{
		attrChangeHeaders,
		attrChangeRow("DEV-100", "设备100", "杭州", "华为", "S", "10.1.0.1", "在网"),
	})
	if sum1.RowsImported != 1 {
		t.Fatalf("run 1 summary = %+v, want 1 imported", sum1)
	}
	// A second DIFFERENT device joins in batch 2, and DEV-100 also gets a
	// pending proposal from batch 2.
	b2, sum2 := importSheet(t, svc, [][]string{
		attrChangeHeaders,
		attrChangeRow("DEV-100", "设备100", "杭州", "华为", "S", "10.1.0.2", "在网"),
		attrChangeRow("DEV-200", "设备200", "杭州", "华为", "S", "10.1.2.1", "在网"),
	})
	if sum2.RowsImported != 1 || sum2.RowsSkipped != 1 {
		t.Fatalf("run 2 summary = %+v, want 1 imported / 1 skipped", sum2)
	}
	if chs := pendingChanges(t, db); len(chs) != 1 || chs[0].BatchID != b2 {
		t.Fatalf("pending changes after run 2 = %+v, want 1 of batch 2", chs)
	}

	// Rollback batch 2: its pending proposal is deleted by the explicit
	// delete; DEV-200 (created by batch 2) disappears and nothing dangles.
	if err := svc.Rollback(ctx, b2); err != nil {
		t.Fatalf("Rollback(%s): %v", b2, err)
	}
	if chs := pendingChanges(t, db); len(chs) != 0 {
		t.Fatalf("pending changes after rollback = %d, want 0", len(chs))
	}
	if _, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, "DEV-200"); err == nil {
		t.Errorf("DEV-200 after rollback still exists, want deleted")
	}
	if _, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, "DEV-100"); err != nil {
		t.Errorf("DEV-100 (batch %s) must survive: %v", b1, err)
	}
}

// TestImportAttributeChangeConditionalType spot-checks the diff mechanism
// on the machine_room type — a conditional-key schema with a ref column
// (site_id): the mechanism is spec-driven and type-agnostic; the ref field
// participates in the diff only through its attributes() value.
func TestImportAttributeChangeConditionalType(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()

	header := siteMachineRoomHeaders
	realRow := []string{"大兴", "呼区", "源1", "4.8.14.8", "23100120000018049", "呼", "231001824048", "源"}
	path := writeMultiSheetWorkbook(t, []sheetSpec{
		{name: siteMachineRoomSheetName, rows: [][]string{header, realRow}},
	})

	// Phase A: the site reading.
	a1, err := svc.Analyze(ctx, path)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if _, err := svc.Execute(ctx, a1.BatchID, siteReadingMapping(siteMachineRoomSheetName)); err != nil {
		t.Fatalf("Execute site reading: %v", err)
	}

	// Phase B: the machine_room reading (creates the room with belongs_to).
	a2, err := svc.Analyze(ctx, path)
	if err != nil {
		t.Fatalf("Analyze 2: %v", err)
	}
	if _, err := svc.Execute(ctx, a2.BatchID, machineRoomReadingMapping(siteMachineRoomSheetName, true)); err != nil {
		t.Fatalf("Execute machine_room reading: %v", err)
	}
	room, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeMachineRoom, "23100120000018049")
	if err != nil {
		t.Fatalf("GetByTypeKey(machine_room): %v", err)
	}
	if room.Attributes["ip_address"] != "4.8.14.8" || room.Attributes["site_id"] != "231001824048" {
		t.Fatalf("room attrs = %v, want ip 4.8.14.8 + site ref", room.Attributes)
	}

	// Phase C: same workbook with a changed IP and 机房名称 — the room row
	// is skipped and both differing attribute fields become pending
	// proposals (机制类型无关). The site_id ref VALUE is unchanged → no diff
	// on it (apply would only rewrite the attributes snapshot, never the
	// derived relation — declared in docs/import-and-validation.md).
	changedRow := []string{"大兴", "呼区", "源1", "4.8.14.99", "23100120000018049", "呼新", "231001824048", "源"}
	path2 := writeMultiSheetWorkbook(t, []sheetSpec{
		{name: siteMachineRoomSheetName, rows: [][]string{header, changedRow}},
	})
	a3, err := svc.Analyze(ctx, path2)
	if err != nil {
		t.Fatalf("Analyze 3: %v", err)
	}
	sum3, err := svc.Execute(ctx, a3.BatchID, machineRoomReadingMapping(siteMachineRoomSheetName, true))
	if err != nil {
		t.Fatalf("Execute machine_room reading 3: %v", err)
	}
	if sum3.RowsImported != 0 || sum3.RowsSkipped != 1 {
		t.Fatalf("run 3 summary = %+v, want 0 imported / 1 skipped", sum3)
	}
	chs := pendingChanges(t, db)
	if len(chs) != 2 {
		t.Fatalf("pending changes after machine_room diff = %d, want 2 (ip_address + machine_room_name)", len(chs))
	}
	for _, ch := range chs {
		if ch.ResourceID != room.ID || ch.BatchID != a3.BatchID || ch.Sheet != siteMachineRoomSheetName {
			t.Errorf("change = %+v, want room %s of batch %s sheet %s", ch, room.ID, a3.BatchID, siteMachineRoomSheetName)
		}
		switch ch.Field {
		case "ip_address":
			if ch.OldValue != "4.8.14.8" || ch.NewValue != "4.8.14.99" {
				t.Errorf("ip_address change = %q→%q, want 4.8.14.8→4.8.14.99", ch.OldValue, ch.NewValue)
			}
		case "machine_room_name":
			if ch.OldValue != "呼" || ch.NewValue != "呼新" {
				t.Errorf("machine_room_name change = %q→%q, want 呼→呼新", ch.OldValue, ch.NewValue)
			}
		default:
			t.Errorf("unexpected diff field %q (site_id ref must not diff when unchanged)", ch.Field)
		}
	}
	// The room resource itself is untouched; the belongs_to relation count
	// is unchanged (skip never touches relations).
	if got, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeMachineRoom, "23100120000018049"); err != nil ||
		got.Attributes["ip_address"] != "4.8.14.8" || got.Name != "呼" {
		t.Errorf("room after diff import = %v (err %v), want untouched", got, err)
	}
	if n, _ := db.Relations.CountByType(ctx); n[domain.RelationTypeBelongsTo] != 1 {
		t.Errorf("belongs_to relations after diff import = %d, want 1 (unchanged)", n[domain.RelationTypeBelongsTo])
	}
	if n := len(w005Issues(t, db)); n != 1 {
		t.Fatalf("W005 issues after machine_room diff = %d, want 1", n)
	}
}
