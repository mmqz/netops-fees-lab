package importer

import (
	"context"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
	"github.com/mmqz/netops-lab/internal/testfixtures"
)

// refsByBatch partitions the entity's source refs by their batch id.
func refsByBatch(t *testing.T, refs []domain.SourceRef) map[string]domain.SourceRef {
	t.Helper()
	m := map[string]domain.SourceRef{}
	for _, ref := range refs {
		if _, dup := m[ref.BatchID]; dup {
			t.Fatalf("two source refs of batch %s for one entity (UNIQUE violated): %+v", ref.BatchID, refs)
		}
		m[ref.BatchID] = ref
	}
	return m
}

// TestSkipAppendsSourceRefVersion is the W7-D core contract (v0.5): a
// re-imported row that hits an existing key is STILL a pure skip at the row
// level (status, E001 rule code, counts, zero skip issues) — but it now
// appends exactly one SourceRef version row for the current batch, so the
// provenance shows every workbook that CONFIRMED the key, not just the one
// that created it. The devices workbook is the fixture: 27 resources each
// end up with 2 refs (one per batch), and the seeded E001 duplicate pair
// (DEV-012 rows 13+27) pins that the second in-batch copy NEVER appends —
// only row 13's skip records a version in run 2, row 27 fails E001 before
// the skip checkpoint.
func TestSkipAppendsSourceRefVersion(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()
	a1, s1 := importWorkbook(t, svc, "devices.xlsx")
	a2, s2 := importWorkbook(t, svc, "devices.xlsx")

	if s1.RowsImported != 27 || s2.RowsImported != 0 || s2.RowsSkipped != 27 || s2.RowsFailed != 3 {
		t.Fatalf("runs = %d and %d/%d/%d/%d, want 27 imported then 0/27/3 (row semantics unchanged)",
			s1.RowsImported, s2.RowsImported, s2.RowsSkipped, s2.RowsFailed, s2.RowsTotal)
	}

	resources, err := db.Resources.ListByBatch(ctx, a1.BatchID)
	if err != nil {
		t.Fatalf("list resources of batch 1: %v", err)
	}
	if len(resources) != 27 {
		t.Fatalf("resources of batch 1 = %d, want 27", len(resources))
	}
	for _, res := range resources {
		refs, err := db.SourceRefs.ListByEntity(ctx, domain.EntityKindResource, res.ID)
		if err != nil {
			t.Fatalf("ListByEntity(%s): %v", res.NaturalKey, err)
		}
		if len(refs) != 2 {
			t.Fatalf("resource %s has %d source refs, want 2 (one version per batch)", res.NaturalKey, len(refs))
		}
		// Version order: insertion order = creation batch first, skip batch second.
		if refs[0].BatchID != a1.BatchID || refs[1].BatchID != a2.BatchID {
			t.Errorf("ref order of %s = [%s, %s], want [batch1, batch2] (version order)",
				res.NaturalKey, refs[0].BatchID, refs[1].BatchID)
		}
		byBatch := refsByBatch(t, refs)
		r1, r2 := byBatch[a1.BatchID], byBatch[a2.BatchID]
		if r1.EntityID != res.ID || r2.EntityID != res.ID {
			t.Errorf("ref entity ids = %s/%s, want both %s", r1.EntityID, r2.EntityID, res.ID)
		}
		if r2.EntityKind != domain.EntityKindResource {
			t.Errorf("skip version entity kind = %s, want resource", r2.EntityKind)
		}
		// Same workbook re-imported: the skip version mirrors the creating
		// ref's workbook/sheet/row exactly (it is the same row re-confirmed).
		if r2.Workbook != "devices.xlsx" || r2.Sheet != testfixtures.DeviceSheetName {
			t.Errorf("skip version workbook/sheet = %s/%s, want devices.xlsx/%s",
				r2.Workbook, r2.Sheet, testfixtures.DeviceSheetName)
		}
		if r2.RowNumber != r1.RowNumber {
			t.Errorf("skip version row number = %d, want %d (same row)", r2.RowNumber, r1.RowNumber)
		}
	}

	// The version row of DEV-001 carries the raw re-imported row.
	dev1, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, "DEV-001")
	if err != nil {
		t.Fatalf("GetByTypeKey(DEV-001): %v", err)
	}
	refs, err := db.SourceRefs.ListByEntity(ctx, domain.EntityKindResource, dev1.ID)
	if err != nil {
		t.Fatalf("ListByEntity(DEV-001): %v", err)
	}
	if refs[1].Raw["设备编码"] != "DEV-001" {
		t.Errorf("skip version raw 设备编码 = %q, want DEV-001", refs[1].Raw["设备编码"])
	}

	// DEV-012: the E001 second copy (row 27) never appends a version — in
	// either run. Exactly 2 refs (row 13 imported in run 1, re-confirmed in
	// run 2), never 3.
	dev12, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, testfixtures.DeviceDupCode)
	if err != nil {
		t.Fatalf("GetByTypeKey(%s): %v", testfixtures.DeviceDupCode, err)
	}
	refs, err = db.SourceRefs.ListByEntity(ctx, domain.EntityKindResource, dev12.ID)
	if err != nil {
		t.Fatalf("ListByEntity(%s): %v", testfixtures.DeviceDupCode, err)
	}
	if len(refs) != 2 {
		t.Fatalf("DEV-012 has %d source refs, want 2 (the E001 duplicate row appends nothing)", len(refs))
	}

	// Skip rows record ZERO issues: batch 2's only issues are the three
	// deterministic E-row failures repeating (same as pre-W7-D semantics).
	if issues := batchIssues(t, db, a2.BatchID); len(issues) != 3 {
		t.Fatalf("issues of batch 2 = %d, want 3 (failed rows only; skips stay silent)", len(issues))
	}
	// Skipped row results keep the E001 rule code (semantics zero-change).
	rows, _, err := db.RowResults.List(ctx, repo.RowResultFilter{
		BatchID: a2.BatchID, Status: domain.RowStatusSkipped,
	})
	if err != nil || len(rows) != 27 {
		t.Fatalf("skipped row results of batch 2 = %d (err %v), want 27", len(rows), err)
	}
	for _, rr := range rows {
		if rr.RuleCode != domain.RuleDuplicateKey {
			t.Errorf("skipped row %d rule code = %q, want E001 (unchanged)", rr.RowNumber, rr.RuleCode)
		}
	}
}

// TestSkipVersionW005DiffSkip pins the second skip form: the W005 diff-skip
// (same key, changed attribute) appends its source ref version exactly like
// the pure skip — the version row records what the CURRENT workbook said
// (raw new value), even though the attribute change is still a pending
// proposal awaiting human confirmation. Refs and pending proposals coexist.
func TestSkipVersionW005DiffSkip(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()

	b1, sum1 := importSheet(t, svc, [][]string{
		attrChangeHeaders,
		attrChangeRow("DEV-001", "核心路由1", "杭州", "华为", "NE40E", "10.0.0.1", "在网"),
		attrChangeRow("DEV-002", "核心路由2", "宁波", "中兴", "ZXR10", "10.0.1.1", "在网"),
	})
	if sum1.RowsImported != 2 {
		t.Fatalf("run 1 imported = %d, want 2", sum1.RowsImported)
	}
	_ = b1
	b2, sum2 := importSheet(t, svc, [][]string{
		attrChangeHeaders,
		attrChangeRow("DEV-001", "核心路由1", "杭州", "华为", "NE40E", "10.0.0.9", "在网"),
		attrChangeRow("DEV-002", "核心路由2", "宁波", "中兴", "ZXR10", "10.0.1.1", "在网"),
	})
	if sum2.RowsImported != 0 || sum2.RowsSkipped != 2 {
		t.Fatalf("run 2 = %d/%d, want 0 imported / 2 skipped (diff-skip)", sum2.RowsImported, sum2.RowsSkipped)
	}

	// Both rows append their version: the diff-skip (DEV-001) AND the pure
	// identical skip (DEV-002). Each version points at its own sheet row.
	for code, wantRow := range map[string]int{"DEV-001": 2, "DEV-002": 3} {
		dev, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, code)
		if err != nil {
			t.Fatalf("GetByTypeKey(%s): %v", code, err)
		}
		refs, err := db.SourceRefs.ListByEntity(ctx, domain.EntityKindResource, dev.ID)
		if err != nil {
			t.Fatalf("ListByEntity(%s): %v", code, err)
		}
		if len(refs) != 2 {
			t.Fatalf("%s has %d source refs, want 2 (diff-skip appends too)", code, len(refs))
		}
		byBatch := refsByBatch(t, refs)
		if byBatch[b2].RowNumber != wantRow || byBatch[b2].Sheet != "设备清单" {
			t.Errorf("%s skip version = sheet %q row %d, want 设备清单 row %d", code, byBatch[b2].Sheet, byBatch[b2].RowNumber, wantRow)
		}
	}

	// The diff-skip version records the NEW workbook value (10.0.0.9) as raw
	// evidence, while the pending proposal and the untouched attribute keep
	// their W005 semantics — versioning and diff proposal coexist.
	dev1, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, "DEV-001")
	if err != nil {
		t.Fatalf("GetByTypeKey(DEV-001): %v", err)
	}
	refs, err := db.SourceRefs.ListByEntity(ctx, domain.EntityKindResource, dev1.ID)
	if err != nil {
		t.Fatalf("ListByEntity(DEV-001): %v", err)
	}
	if refs[1].Raw["管理IP"] != "10.0.0.9" {
		t.Errorf("diff-skip version raw 管理IP = %q, want 10.0.0.9 (the new workbook value)", refs[1].Raw["管理IP"])
	}
	if chs := pendingChanges(t, db); len(chs) != 1 || chs[0].Field != "mgmt_ip" ||
		chs[0].NewValue != "10.0.0.9" || chs[0].BatchID != b2 {
		t.Fatalf("pending changes = %+v, want the single mgmt_ip 10.0.0.9 proposal of batch 2", chs)
	}
	if dev1.Attributes["mgmt_ip"] != "10.0.0.1" {
		t.Fatalf("DEV-001 mgmt_ip = %q, want 10.0.0.1 (skip never writes the resource)", dev1.Attributes["mgmt_ip"])
	}
	if n := len(w005Issues(t, db)); n != 1 {
		t.Fatalf("W005 issues = %d, want 1", n)
	}
}

// TestSkipVersionInBatchDuplicateNotAppended pins the E001 checkpoint guard:
// the second same-key row of ONE batch fails E001 and returns BEFORE the
// idempotency check, so it never appends a skip version — in a fresh batch
// (insert + E001 failure) and in a re-import (skip + E001 failure). This is
// what keeps the skip-path insert from ever tripping the
// UNIQUE (entity_kind, entity_id, batch_id) constraint.
func TestSkipVersionInBatchDuplicateNotAppended(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()
	sheet := [][]string{
		attrChangeHeaders,
		attrChangeRow("DEV-001", "核心路由1", "杭州", "华为", "NE40E", "10.0.0.1", "在网"),
		attrChangeRow("DEV-001", "同名副本", "杭州", "华为", "NE40E", "10.0.0.2", "在网"),
	}

	// Run 1: row 2 imported (1 ref), row 3 E001-failed (0 refs from it).
	b1, sum1 := importSheet(t, svc, sheet)
	if sum1.RowsImported != 1 || sum1.RowsFailed != 1 {
		t.Fatalf("run 1 = %d imported / %d failed, want 1/1", sum1.RowsImported, sum1.RowsFailed)
	}
	dev1, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, "DEV-001")
	if err != nil {
		t.Fatalf("GetByTypeKey(DEV-001): %v", err)
	}
	refs, err := db.SourceRefs.ListByEntity(ctx, domain.EntityKindResource, dev1.ID)
	if err != nil || len(refs) != 1 || refs[0].BatchID != b1 {
		t.Fatalf("refs after run 1 = %+v (err %v), want exactly 1 of batch 1 (E001 row appends nothing)", refs, err)
	}

	// Run 2: row 2 skipped (appends its version), row 3 E001-failed again —
	// exactly ONE version of batch 2, never two.
	b2, sum2 := importSheet(t, svc, sheet)
	if sum2.RowsSkipped != 1 || sum2.RowsFailed != 1 {
		t.Fatalf("run 2 = %d skipped / %d failed, want 1/1", sum2.RowsSkipped, sum2.RowsFailed)
	}
	refs, err = db.SourceRefs.ListByEntity(ctx, domain.EntityKindResource, dev1.ID)
	if err != nil || len(refs) != 2 || refs[1].BatchID != b2 {
		t.Fatalf("refs after run 2 = %+v (err %v), want exactly 2: batch 1 + ONE batch 2 version", refs, err)
	}
}

// TestRollbackRemovesSkipVersions pins the batch-scoped lifecycle: rolling
// back the re-import batch deletes its skip versions with it — via the
// rollback's explicit DeleteByBatch (the DDL's batch_id ON DELETE CASCADE
// would deliver the same outcome on batch-row deletion). The creating
// batch's refs survive untouched.
func TestRollbackRemovesSkipVersions(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()
	a1, _ := importWorkbook(t, svc, "devices.xlsx")
	a2, _ := importWorkbook(t, svc, "devices.xlsx")

	if n, err := db.SourceRefs.CountByBatch(ctx, a2.BatchID); err != nil || n != 27 {
		t.Fatalf("source refs of batch 2 = %d (err %v), want 27 (one per skipped resource)", n, err)
	}
	if err := svc.Rollback(ctx, a2.BatchID); err != nil {
		t.Fatalf("Rollback(batch 2): %v", err)
	}
	if n, err := db.SourceRefs.CountByBatch(ctx, a2.BatchID); err != nil || n != 0 {
		t.Fatalf("source refs of batch 2 after rollback = %d (err %v), want 0", n, err)
	}
	resources, err := db.Resources.ListByBatch(ctx, a1.BatchID)
	if err != nil {
		t.Fatalf("list resources of batch 1: %v", err)
	}
	for _, res := range resources {
		refs, err := db.SourceRefs.ListByEntity(ctx, domain.EntityKindResource, res.ID)
		if err != nil {
			t.Fatalf("ListByEntity(%s): %v", res.NaturalKey, err)
		}
		if len(refs) != 1 || refs[0].BatchID != a1.BatchID {
			t.Fatalf("resource %s refs after rollback = %+v, want exactly 1 of batch 1 (versions die with their batch)",
				res.NaturalKey, refs)
		}
	}
	// The resources themselves are untouched by the skip-batch rollback.
	if resCounts, err := db.Resources.CountByType(ctx); err != nil || resCounts[domain.ResourceTypeDevice] != 27 {
		t.Fatalf("devices after rollback = %v (err %v), want 27", resCounts, err)
	}
}
