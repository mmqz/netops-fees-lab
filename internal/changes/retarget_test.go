package changes

// The W13-D ref-retarget test family: a confirmed ref-field change
// (machine_room.site_id, circuit a_end/z_end) must retarget the derived
// relation inside the apply transaction — while a NON-ref field never
// touches relations, a ref field with no edge never creates one, an
// unresolvable new target refuses the apply with the typed 409 error, and
// a mid-flight retarget failure (the rename-merge UNIQUE corner) rolls the
// whole apply back. The machine-room seeding follows the real 双读法 story
// of the 机房局站 export: sites first, then the rooms whose optional 局站ID
// ref resolves against them and derives the belongs_to edge.

import (
	"context"
	"errors"
	"github.com/mmqz/netops-lab/internal/testsupport"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/importer"
	"github.com/mmqz/netops-lab/internal/mapping"
	"github.com/mmqz/netops-lab/internal/repo"
	"github.com/xuri/excelize/v2"
)

// writeSheetWorkbook writes one single-sheet workbook (header + rows) and
// returns its path.
func writeSheetWorkbook(t *testing.T, dir, name, sheet string, header []string, rows [][]string) string {
	t.Helper()
	f := excelize.NewFile()
	if err := f.SetSheetName("Sheet1", sheet); err != nil {
		t.Fatalf("rename default sheet: %v", err)
	}
	grid := append([][]string{header}, rows...)
	for r, row := range grid {
		for c, v := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, r+1)
			if err != nil {
				t.Fatalf("cell name: %v", err)
			}
			if err := f.SetCellStr(sheet, cell, v); err != nil {
				t.Fatalf("set cell: %v", err)
			}
		}
	}
	path := filepath.Join(dir, name)
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("save workbook: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close workbook: %v", err)
	}
	return path
}

// importWorkbook runs the analyze → execute pipeline with a fixed mapping.
func importWorkbook(t *testing.T, imp *importer.Service, ctx context.Context, path string, m mapping.Mapping) importer.Summary {
	t.Helper()
	a, err := imp.Analyze(ctx, path)
	if err != nil {
		t.Fatalf("Analyze %s: %v", path, err)
	}
	sum, err := imp.Execute(ctx, a.BatchID, m)
	if err != nil {
		t.Fatalf("Execute %s: %v", path, err)
	}
	return sum
}

// siteMapping / roomMapping are the confirmed mappings of the two readings.
var (
	siteMapping = mapping.Mapping{Sheets: []mapping.SheetMapping{{
		Sheet: "局站资源表", Action: mapping.ActionImport, ResourceType: domain.ResourceTypeSite,
		KeyColumns: []string{"site_id"},
		ColumnMapping: map[string]string{
			"site_id": "局站ID", "site_name": "局站名称", "city": "地市",
		},
	}}}
	roomMapping = mapping.Mapping{Sheets: []mapping.SheetMapping{{
		Sheet: "机房资源表", Action: mapping.ActionImport, ResourceType: domain.ResourceTypeMachineRoom,
		KeyColumns: []string{"machine_room_id"},
		ColumnMapping: map[string]string{
			"machine_room_id": "机房ID", "machine_room_name": "机房名称", "site_id": "局站ID",
		},
	}}}
)

// roomSiteFixture is the seeded state of the retarget test family.
type roomSiteFixture struct {
	svc        *Service
	db         *repo.DB
	imp        *importer.Service
	room       domain.Resource
	siteA      domain.Resource
	siteB      domain.Resource
	roomBatch  string // the batch that created the room — the edge's lineage anchor
	siteBBatch string // SITE-B lives in its OWN batch (deletable via rollback)
	ch         domain.AttributeChange
}

// newRoomSiteFixture seeds SITE-A and SITE-B (two separate imports, so
// SITE-B's batch can be rolled back without touching SITE-A), the machine
// room RM-001 with site_id = firstSiteID ("" = the W001-orphan path:
// empty optional ref, no edge) and re-imports the same room with site_id =
// secondSiteID — one pending W005 proposal.
func newRoomSiteFixture(t *testing.T, firstSiteID, secondSiteID string) *roomSiteFixture {
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
	imp := importer.New(db)
	imp.Now = func() time.Time { return time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC) }
	ctx := context.Background()
	dir := testsupport.TempDir(t)

	siteAWB := writeSheetWorkbook(t, dir, "site-a.xlsx", "局站资源表",
		[]string{"局站ID", "局站名称", "地市"},
		[][]string{{"SITE-A", "站A", "杭州"}})
	importWorkbook(t, imp, ctx, siteAWB, siteMapping)
	siteBWB := writeSheetWorkbook(t, dir, "site-b.xlsx", "局站资源表",
		[]string{"局站ID", "局站名称", "地市"},
		[][]string{{"SITE-B", "站B", "宁波"}})
	siteBBatch := importWorkbook(t, imp, ctx, siteBWB, siteMapping).BatchID

	roomWB := writeSheetWorkbook(t, dir, "room-v1.xlsx", "机房资源表",
		[]string{"机房ID", "机房名称", "局站ID"},
		[][]string{{"RM-001", "机房一", firstSiteID}})
	roomBatch := importWorkbook(t, imp, ctx, roomWB, roomMapping).BatchID

	roomV2 := writeSheetWorkbook(t, dir, "room-v2.xlsx", "机房资源表",
		[]string{"机房ID", "机房名称", "局站ID"},
		[][]string{{"RM-001", "机房一", secondSiteID}})
	importWorkbook(t, imp, ctx, roomV2, roomMapping)

	room, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeMachineRoom, "RM-001")
	if err != nil {
		t.Fatalf("GetByTypeKey RM-001: %v", err)
	}
	siteA, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeSite, "SITE-A")
	if err != nil {
		t.Fatalf("GetByTypeKey SITE-A: %v", err)
	}
	siteB, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeSite, "SITE-B")
	if err != nil {
		t.Fatalf("GetByTypeKey SITE-B: %v", err)
	}
	chs, total, err := db.AttributeChanges.List(ctx, repo.ChangeFilter{
		Status: domain.ChangeStatusPending, ResourceID: room.ID})
	if err != nil || total != 1 || len(chs) != 1 {
		t.Fatalf("seeded pending changes = %d (total %d, err %v), want exactly the site_id proposal", len(chs), total, err)
	}
	svc := New(db)
	svc.Now = func() time.Time { return time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) }
	return &roomSiteFixture{
		svc: svc, db: db, imp: imp, room: room, siteA: siteA, siteB: siteB,
		roomBatch: roomBatch, siteBBatch: siteBBatch, ch: chs[0],
	}
}

// belongsToOf returns the room's belongs_to relation (from = room), or the
// zero Relation when none exists.
func belongsToOf(t *testing.T, db *repo.DB, roomID string) domain.Relation {
	t.Helper()
	rels, err := db.Relations.ListByResource(context.Background(), roomID)
	if err != nil {
		t.Fatalf("ListByResource %s: %v", roomID, err)
	}
	for _, rel := range rels {
		if rel.FromResourceID == roomID && rel.Type == domain.RelationTypeBelongsTo {
			return rel
		}
	}
	return domain.Relation{}
}

// TestApplyRefFieldRetargetsRelation pins the happy path ①: a confirmed
// machine_room.site_id change writes the attribute AND retargets the
// belongs_to edge to the new site's resource id, inside one transaction;
// the response carries relationRetargeted=true, the LIST preview showed
// the same predicate before the apply (the web hint), the edge's lineage
// (first_batch_id) stays anchored at the room's creating import, and the
// second apply answers the unchanged 409 conflict.
func TestApplyRefFieldRetargetsRelation(t *testing.T) {
	f := newRoomSiteFixture(t, "SITE-A", "SITE-B")
	svc, db, room, siteB := f.svc, f.db, f.room, f.siteB
	ctx := context.Background()

	// The LIST preview (the predicate behind the drawer's ref-field hint).
	list, err := svc.List(ctx, Filter{Status: domain.ChangeStatusPending})
	if err != nil || len(list.Items) != 1 {
		t.Fatalf("List pending = %d items (err %v), want 1", len(list.Items), err)
	}
	if !list.Items[0].RelationRetargeted {
		t.Errorf("list preview relationRetargeted = false, want true (ref field with an edge)")
	}
	if f.ch.Field != "site_id" || f.ch.OldValue != "SITE-A" || f.ch.NewValue != "SITE-B" {
		t.Fatalf("proposal = %+v, want site_id SITE-A→SITE-B", f.ch)
	}

	item, err := svc.Apply(ctx, f.ch.ID)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !item.RelationRetargeted {
		t.Errorf("applied item relationRetargeted = false, want true (the edge moved)")
	}
	if item.Status != "applied" || item.Field != "site_id" {
		t.Errorf("applied item = %+v, want applied site_id", item)
	}
	after, err := db.Resources.GetByID(ctx, room.ID)
	if err != nil || after.Attributes["site_id"] != "SITE-B" {
		t.Errorf("resource after apply = %v (err %v), want site_id SITE-B", after, err)
	}
	// The edge followed the new target — same from, same type, new to.
	edge := belongsToOf(t, db, room.ID)
	if edge.ID == "" {
		t.Fatal("belongs_to edge vanished after the apply")
	}
	if edge.ToResourceID != siteB.ID || edge.FromResourceID != room.ID || edge.Type != domain.RelationTypeBelongsTo {
		t.Errorf("edge after retarget = %+v, want room --belongs_to--> SITE-B(%s)", edge, siteB.ID)
	}
	// Lineage untouched: the edge keeps its creating import batch (the
	// workbook evidence), not the apply.
	if edge.FirstBatchID != f.roomBatch {
		t.Errorf("edge first_batch_id = %q, want %q (retargeting keeps the creation lineage)", edge.FirstBatchID, f.roomBatch)
	}

	// The second apply of the now-applied proposal → the unchanged conflict.
	if _, err := svc.Apply(ctx, f.ch.ID); err == nil || !errors.Is(err, domain.ErrConflict) {
		t.Errorf("re-Apply = %v, want domain.ErrConflict", err)
	}
}

// TestApplyNonRefFieldNeverTouchesRelations pins ②: a non-ref attribute
// change (device mgmt_ip) never touches relations — relationRetargeted is
// false on both the apply response and the list preview, and the resource
// participates in zero relations.
func TestApplyNonRefFieldNeverTouchesRelations(t *testing.T) {
	svc, db, res := newSeededService(t)
	ctx := context.Background()

	list, err := svc.List(ctx, Filter{Status: domain.ChangeStatusPending})
	if err != nil || len(list.Items) != 2 {
		t.Fatalf("List pending = %d items (err %v), want 2", len(list.Items), err)
	}
	for _, it := range list.Items {
		if it.RelationRetargeted {
			t.Errorf("list preview of non-ref field %s: relationRetargeted = true, want false", it.Field)
		}
	}

	var targetID string
	for _, it := range list.Items {
		if it.Field == "mgmt_ip" {
			targetID = it.ID
		}
	}
	item, err := svc.Apply(ctx, targetID)
	if err != nil {
		t.Fatalf("Apply mgmt_ip: %v", err)
	}
	if item.RelationRetargeted {
		t.Errorf("applied non-ref item relationRetargeted = true, want false")
	}
	rels, err := db.Relations.ListByResource(ctx, res.ID)
	if err != nil || len(rels) != 0 {
		t.Errorf("relations of DEV-001 after non-ref apply = %d (err %v), want 0", len(rels), err)
	}
}

// TestApplyRefFieldWithoutEdgeNeverCreates pins ③: the W001-orphan path —
// the room was imported with an EMPTY optional site_id (no belongs_to
// edge); confirming the "fill it in" proposal writes the attribute but
// never creates the edge (the provenance invariant: an edge needs workbook
// evidence, the apply has none), relationRetargeted stays false and the
// relation count is unchanged.
func TestApplyRefFieldWithoutEdgeNeverCreates(t *testing.T) {
	f := newRoomSiteFixture(t, "", "SITE-B")
	svc, db, room, ch := f.svc, f.db, f.room, f.ch
	ctx := context.Background()

	if ch.OldValue != "" {
		t.Fatalf("orphan proposal old value = %q, want \"\" (empty at import)", ch.OldValue)
	}
	list, err := svc.List(ctx, Filter{Status: domain.ChangeStatusPending})
	if err != nil || len(list.Items) != 1 || list.Items[0].RelationRetargeted {
		t.Fatalf("list preview = %+v (err %v), want relationRetargeted=false for the no-edge ref field", list.Items, err)
	}

	item, err := svc.Apply(ctx, ch.ID)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if item.RelationRetargeted {
		t.Errorf("no-edge ref apply relationRetargeted = true, want false")
	}
	after, err := db.Resources.GetByID(ctx, room.ID)
	if err != nil || after.Attributes["site_id"] != "SITE-B" {
		t.Errorf("resource after apply = %v (err %v), want site_id SITE-B (attribute-only)", after, err)
	}
	if edge := belongsToOf(t, db, room.ID); edge.ID != "" {
		t.Errorf("an edge was created by the apply: %+v — apply never creates relations", edge)
	}
	counts, err := db.Relations.CountByType(ctx)
	if err != nil {
		t.Fatalf("CountByType: %v", err)
	}
	if counts[domain.RelationTypeBelongsTo] != 0 {
		t.Errorf("belongs_to count after the no-edge apply = %d, want 0 (never created)", counts[domain.RelationTypeBelongsTo])
	}
}

// TestApplyRefTargetMissing pins ④: the ref-target guard. A proposal's new
// value always resolved at import time (an unresolvable ref fails E003
// BEFORE the skip/diff path — the import channel can never record a
// dangling target), so the REACHABLE trigger of this guard is "the target
// resource vanished after the proposal was recorded" — here the natural
// journey: SITE-B is rolled back after the proposal exists. The apply
// answers the typed RefTargetMissingError (the HTTP layer's 409
// ref_target_missing) with the field, the target key and the referenced
// type in the message; the proposal stays pending and BOTH the resource
// and its relation are untouched.
func TestApplyRefTargetMissing(t *testing.T) {
	f := newRoomSiteFixture(t, "SITE-A", "SITE-B")
	svc, db, room, siteA, ch := f.svc, f.db, f.room, f.siteA, f.ch
	ctx := context.Background()

	// The target site is rolled back AFTER the proposal was recorded.
	// The rollback is legal: no relation references SITE-B (the room's
	// edge points at SITE-A), and the proposal belongs to the room's
	// re-import batch, not to the rolled-back site batch — both survival
	// properties are the same ones the W005/W006 flows already rely on.
	if err := f.imp.Rollback(ctx, f.siteBBatch); err != nil {
		t.Fatalf("Rollback site-B batch: %v", err)
	}

	_, err := svc.Apply(ctx, ch.ID)
	var missing *RefTargetMissingError
	if err == nil || !errors.As(err, &missing) {
		t.Fatalf("Apply with vanished target = %v, want *changes.RefTargetMissingError", err)
	}
	if missing.Field != "site_id" || missing.NewValue != "SITE-B" || missing.RefType != domain.ResourceTypeSite {
		t.Errorf("RefTargetMissingError = %+v, want site_id SITE-B site", missing)
	}
	for _, want := range []string{"site_id", "SITE-B", "site"} {
		if !strings.Contains(missing.Error(), want) {
			t.Errorf("message %q must contain %q", missing.Error(), want)
		}
	}
	// Zero changes: proposal still pending…
	after, err := db.AttributeChanges.Get(ctx, ch.ID)
	if err != nil || after.Status != domain.ChangeStatusPending {
		t.Errorf("proposal after refusal = %+v (err %v), want still pending", after, err)
	}
	// …resource attribute untouched…
	res, err := db.Resources.GetByID(ctx, room.ID)
	if err != nil || res.Attributes["site_id"] != "SITE-A" {
		t.Errorf("resource after refusal = %v (err %v), want site_id still SITE-A", res, err)
	}
	// …and the belongs_to edge still points at SITE-A.
	edge := belongsToOf(t, db, room.ID)
	if edge.ID == "" || edge.ToResourceID != siteA.ID {
		t.Errorf("edge after refusal = %+v, want still room --belongs_to--> SITE-A(%s)", edge, siteA.ID)
	}
}

// TestApplyCircuitEndsRetargetIndependently pins ⑤: a circuit's a_end and
// z_end are two ref fields of ONE resource with two distinct derived
// relation types — each apply retargets only its own edge, the other edge
// is untouched, both report relationRetargeted=true.
func TestApplyCircuitEndsRetargetIndependently(t *testing.T) {
	db, err := repo.Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close: %v", err)
		}
	})
	imp := importer.New(db)
	imp.Now = func() time.Time { return time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC) }
	ctx := context.Background()
	dir := testsupport.TempDir(t)

	devices := writeSheetWorkbook(t, dir, "devices.xlsx", "设备清单",
		[]string{"设备编码", "设备名称"},
		[][]string{
			{"DEV-001", "设备一"}, {"DEV-002", "设备二"},
			{"DEV-003", "设备三"}, {"DEV-004", "设备四"},
		})
	devMapping := mapping.Mapping{Sheets: []mapping.SheetMapping{{
		Sheet: "设备清单", Action: mapping.ActionImport, ResourceType: domain.ResourceTypeDevice,
		KeyColumns:    []string{"device_code"},
		ColumnMapping: map[string]string{"device_code": "设备编码", "device_name": "设备名称"},
	}}}
	importWorkbook(t, imp, ctx, devices, devMapping)

	cktMapping := mapping.Mapping{Sheets: []mapping.SheetMapping{{
		Sheet: "电路清单", Action: mapping.ActionImport, ResourceType: domain.ResourceTypeCircuit,
		KeyColumns: []string{"circuit_code"},
		ColumnMapping: map[string]string{
			"circuit_code": "电路编号", "a_end_device": "A端设备", "z_end_device": "Z端设备",
		},
	}}}
	cktV1 := writeSheetWorkbook(t, dir, "circuit-v1.xlsx", "电路清单",
		[]string{"电路编号", "A端设备", "Z端设备"},
		[][]string{{"CKT-001", "DEV-001", "DEV-002"}})
	importWorkbook(t, imp, ctx, cktV1, cktMapping)
	cktV2 := writeSheetWorkbook(t, dir, "circuit-v2.xlsx", "电路清单",
		[]string{"电路编号", "A端设备", "Z端设备"},
		[][]string{{"CKT-001", "DEV-003", "DEV-004"}})
	importWorkbook(t, imp, ctx, cktV2, cktMapping)

	circuit, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeCircuit, "CKT-001")
	if err != nil {
		t.Fatalf("GetByTypeKey CKT-001: %v", err)
	}
	chs, total, err := db.AttributeChanges.List(ctx, repo.ChangeFilter{
		Status: domain.ChangeStatusPending, ResourceID: circuit.ID})
	if err != nil || total != 2 || len(chs) != 2 {
		t.Fatalf("pending changes = %d (total %d, err %v), want a_end + z_end proposals", len(chs), total, err)
	}
	var aCh, zCh domain.AttributeChange
	for _, ch := range chs {
		switch ch.Field {
		case "a_end_device":
			aCh = ch
		case "z_end_device":
			zCh = ch
		}
	}
	if aCh.ID == "" || zCh.ID == "" {
		t.Fatalf("proposals = %+v, want one a_end_device and one z_end_device", chs)
	}
	dev := func(key string) string {
		res, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, key)
		if err != nil {
			t.Fatalf("GetByTypeKey %s: %v", key, err)
		}
		return res.ID
	}
	svc := New(db)
	svc.Now = func() time.Time { return time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) }

	// List preview (W15-D, closing the W13-Q P3-2 observation): both circuit-end
	// proposals are ref fields whose derived edges exist, so the real-time
	// predicate behind relationRetargeted recomputes true for each end
	// independently — BEFORE any apply has run.
	lr, err := svc.List(ctx, Filter{Status: domain.ChangeStatusPending, ResourceID: circuit.ID})
	if err != nil {
		t.Fatalf("List preview: %v", err)
	}
	if lr.Total != 2 || len(lr.Items) != 2 {
		t.Fatalf("List preview = %d items (total %d), want both circuit-end proposals", len(lr.Items), lr.Total)
	}
	for _, it := range lr.Items {
		if !it.RelationRetargeted {
			t.Errorf("List preview %s (%s) relationRetargeted = false, want true (ref field with an existing derived edge)", it.ID, it.Field)
		}
	}

	// Apply the a_end proposal: only the a_end edge moves.
	item, err := svc.Apply(ctx, aCh.ID)
	if err != nil {
		t.Fatalf("Apply a_end: %v", err)
	}
	if !item.RelationRetargeted {
		t.Errorf("a_end apply relationRetargeted = false, want true")
	}
	rels, err := db.Relations.ListByResource(ctx, circuit.ID)
	if err != nil {
		t.Fatalf("ListByResource: %v", err)
	}
	for _, rel := range rels {
		switch rel.Type {
		case domain.RelationTypeAEnd:
			if rel.ToResourceID != dev("DEV-003") {
				t.Errorf("a_end after first apply points at %s, want DEV-003", rel.ToResourceID)
			}
		case domain.RelationTypeZEnd:
			if rel.ToResourceID != dev("DEV-002") {
				t.Errorf("z_end after a_end apply points at %s, want DEV-002 (untouched)", rel.ToResourceID)
			}
		}
	}

	// Apply the z_end proposal: only the z_end edge moves.
	item, err = svc.Apply(ctx, zCh.ID)
	if err != nil {
		t.Fatalf("Apply z_end: %v", err)
	}
	if !item.RelationRetargeted {
		t.Errorf("z_end apply relationRetargeted = false, want true")
	}
	rels, err = db.Relations.ListByResource(ctx, circuit.ID)
	if err != nil {
		t.Fatalf("ListByResource 2: %v", err)
	}
	for _, rel := range rels {
		switch rel.Type {
		case domain.RelationTypeAEnd:
			if rel.ToResourceID != dev("DEV-003") {
				t.Errorf("a_end after z_end apply points at %s, want DEV-003 (untouched)", rel.ToResourceID)
			}
		case domain.RelationTypeZEnd:
			if rel.ToResourceID != dev("DEV-004") {
				t.Errorf("z_end after second apply points at %s, want DEV-004", rel.ToResourceID)
			}
		}
	}
	// Both fields applied on one resource: the per-field stale guard let
	// them through independently.
	circuitAfter, err := db.Resources.GetByID(ctx, circuit.ID)
	if err != nil {
		t.Fatalf("GetByID circuit: %v", err)
	}
	if circuitAfter.Attributes["a_end_device"] != "DEV-003" || circuitAfter.Attributes["z_end_device"] != "DEV-004" {
		t.Errorf("circuit attributes after both applies = %v, want DEV-003/DEV-004", circuitAfter.Attributes)
	}
}

// TestApplyRefRetargetRollsBackOnCollision pins ⑥ — the same-transaction
// proof by injection: a second belongs_to edge of the same (from, type) is
// legal (rename-merge residue), so the retarget UPDATE collides with the
// UNIQUE (from, to, type) constraint AFTER the attribute write already
// happened. The whole apply must roll back: error out, the proposal stays
// pending, the attribute keeps its old value and BOTH edges keep their
// original targets — zero residue.
func TestApplyRefRetargetRollsBackOnCollision(t *testing.T) {
	f := newRoomSiteFixture(t, "SITE-A", "SITE-B")
	svc, db, room, siteA, siteB, roomBatch, ch := f.svc, f.db, f.room, f.siteA, f.siteB, f.roomBatch, f.ch
	ctx := context.Background()

	// The injected second edge (the rename-merge corner): room already owns
	// belongs_to → SITE-A; add belongs_to → SITE-B. FirstBatchID must
	// reference an existing batch — the room's creating import.
	if err := db.Do(ctx, func(tx *repo.Tx) error {
		return tx.Relations.Insert(ctx, domain.Relation{
			ID: "rel_injected", Type: domain.RelationTypeBelongsTo,
			FromResourceID: room.ID, ToResourceID: siteB.ID,
			FirstBatchID: roomBatch, CreatedAt: time.Date(2026, 11, 10, 11, 0, 0, 0, time.UTC),
		})
	}); err != nil {
		t.Fatalf("inject second edge: %v", err)
	}

	_, err := svc.Apply(ctx, ch.ID)
	if err == nil || !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Fatalf("colliding Apply = %v, want the UNIQUE constraint failure (rename-merge corner)", err)
	}
	// Zero residue — the whole apply rolled back.
	after, err := db.AttributeChanges.Get(ctx, ch.ID)
	if err != nil || after.Status != domain.ChangeStatusPending {
		t.Errorf("proposal after rolled-back apply = %+v (err %v), want still pending", after, err)
	}
	res, err := db.Resources.GetByID(ctx, room.ID)
	if err != nil || res.Attributes["site_id"] != "SITE-A" {
		t.Errorf("resource after rolled-back apply = %v (err %v), want site_id still SITE-A", res, err)
	}
	rels, err := db.Relations.ListByResource(ctx, room.ID)
	if err != nil || len(rels) != 2 {
		t.Fatalf("relations after rolled-back apply = %d (err %v), want 2 (both kept)", len(rels), err)
	}
	for _, rel := range rels {
		if rel.Type != domain.RelationTypeBelongsTo || rel.FromResourceID != room.ID {
			t.Errorf("unexpected relation %+v", rel)
			continue
		}
		if rel.ToResourceID != siteA.ID && rel.ToResourceID != siteB.ID {
			t.Errorf("edge drifted to %s — the aborted statement must leave both edges unchanged", rel.ToResourceID)
		}
	}
}
