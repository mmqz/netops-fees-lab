package importer

// W006 suspected-rename detection tests (v0.5 W6-D, natural-keys.md §6 gap
// #3): the two-phase detection on the not-exists insert branch of
// name-keyed types, the five proposal conditions (each pinned by a negative
// case), the bijection requirement, the hollow-match silence, the
// incremental-import false-positive face (declared, not fixed), the
// idempotent re-import, the relation retarget through the renames service
// apply (an ip_pool rename carries a real assigned_to edge — the one e2e
// relation scenario the seven types have for a rename candidate), and the
// rollback asymmetry.

import (
	"context"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/mapping"
	"github.com/mmqz/netops-lab/internal/renames"
	"github.com/mmqz/netops-lab/internal/repo"
)

// renameRow builds one 无线基站表 data row (the REAL 14-column shape of
// samples/telecom-base-station.xlsx, code empty — the fallback key is the
// bare station name).
func renameRow(name, uplinkIP, stationIP string) []string {
	return []string{"宏基站", name, "", "是", uplinkIP, "环1", "链1", "枢纽楼A4", "gei_1/11",
		"12.17.10.126", stationIP, "BBU接入", "202366", ""}
}

// importRenameWorkbook analyzes + executes one single-sheet base_station
// workbook (real 14-column shape) and returns the batch id + summary.
func importRenameWorkbook(t *testing.T, svc *Service, rows [][]string) (string, Summary) {
	t.Helper()
	ctx := context.Background()
	path := writeMultiSheetWorkbook(t, []sheetSpec{{name: baseStationSheetName, rows: rows}})
	a, err := svc.Analyze(ctx, path)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	sum, err := svc.Execute(ctx, a.BatchID, baseStationMapping(baseStationSheetName, false))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return a.BatchID, sum
}

// pendingRenames lists the pending rename proposals of the whole database.
func pendingRenames(t *testing.T, db *repo.DB) []domain.RenameProposal {
	t.Helper()
	rps, total, err := db.RenameProposals.List(context.Background(), repo.RenameFilter{
		Status: domain.RenameStatusPending,
	})
	if err != nil {
		t.Fatalf("list pending renames: %v", err)
	}
	if total != len(rps) {
		t.Fatalf("pending total %d != len %d", total, len(rps))
	}
	return rps
}

// w006Issues lists every W006 issue of the database.
func w006Issues(t *testing.T, db *repo.DB) []domain.Issue {
	t.Helper()
	issues, _, err := db.Issues.List(context.Background(), repo.IssueFilter{RuleCode: domain.RuleSuspectedRename})
	if err != nil {
		t.Fatalf("list W006 issues: %v", err)
	}
	return issues
}

// TestImportRenameDetectionPositive is the W006 end-to-end contract on the
// real base_station shape: a renamed station (old key vanished from the
// source, every non-key attribute equal, unique candidate, unique new row)
// imports as a NEW resource and gets one pending proposal + one W006
// warning with the full evidence; the same-named duplicate row (the sheet's
// E001 seed) keeps failing E001; re-importing the renamed sheet is a pure
// skip — zero new issues, zero new proposals, the first proposal intact.
func TestImportRenameDetectionPositive(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()

	// Run 1: the real-shaped sheet (the TestImportBaseStationConditionalKey
	// rows — 动力机房2 twice, a coded row, an E002 row, an E004 row).
	_, sum1 := importRenameWorkbook(t, svc, [][]string{
		baseStationRealHeaders,
		renameRow("动力机房2", "4.28.2.3", "172.7.10.125/30"),
		[]string{"宏基站", "会展中心B1", "BTS-HLJ-0001", "否", "4.28.1.10", "环1", "链2", "枢纽楼A4", "gei_1/12", "12.17.10.126", "172.7.11.8/30", "BBU接入", "202367", ""},
		renameRow("动力机房2", "4.28.2.4", "172.7.10.126/30"),
		[]string{"宏基站", "", "", "是", "4.28.2.5", "环1", "链1", "枢纽楼A4", "gei_1/11", "12.17.10.126", "172.7.10.127/30", "", "", ""},
		[]string{"宏基站", "学府路2", "", "是", "999.28.2.6", "环2", "链1", "枢纽楼A4", "gei_1/13", "12.17.10.126", "172.7.10.128/30", "", "202368", ""},
	})
	if sum1.RowsImported != 2 || sum1.RowsFailed != 3 {
		t.Fatalf("run 1 summary = %+v, want 2 imported / 3 failed", sum1)
	}
	if rps := pendingRenames(t, db); len(rps) != 0 {
		t.Fatalf("pending renames after first import = %d, want 0", len(rps))
	}

	// Run 2: BOTH rows named 动力机房2 are renamed (a rename must make the
	// old key vanish from the source — condition 2; the sheet has two such
	// rows, the second being the E001 seed).
	b2, sum2 := importRenameWorkbook(t, svc, [][]string{
		baseStationRealHeaders,
		renameRow("动力机房2新", "4.28.2.3", "172.7.10.125/30"),
		[]string{"宏基站", "会展中心B1", "BTS-HLJ-0001", "否", "4.28.1.10", "环1", "链2", "枢纽楼A4", "gei_1/12", "12.17.10.126", "172.7.11.8/30", "BBU接入", "202367", ""},
		renameRow("动力机房2新", "4.28.2.4", "172.7.10.126/30"),
		[]string{"宏基站", "", "", "是", "4.28.2.5", "环1", "链1", "枢纽楼A4", "gei_1/11", "12.17.10.126", "172.7.10.127/30", "", "", ""},
		[]string{"宏基站", "学府路2", "", "是", "999.28.2.6", "环2", "链1", "枢纽楼A4", "gei_1/13", "12.17.10.126", "172.7.10.128/30", "", "202368", ""},
	})
	// Row 2 imports as the new resource; row 3 skips (identical); row 4
	// fails E001 (in-batch duplicate of row 2's new key); rows 5/6 fail as
	// before.
	if sum2.RowsImported != 1 || sum2.RowsSkipped != 1 || sum2.RowsFailed != 3 {
		t.Fatalf("run 2 summary = %+v, want 1 imported / 1 skipped / 3 failed", sum2)
	}
	if sum2.IssuesBySeverity[domain.SeverityWarning] != 2 {
		t.Fatalf("run 2 warnings = %d, want 2 (W004 of the imported row + W006)", sum2.IssuesBySeverity[domain.SeverityWarning])
	}

	// One pending proposal with every field pinned.
	rps := pendingRenames(t, db)
	if len(rps) != 1 {
		t.Fatalf("pending renames after rename import = %d, want 1", len(rps))
	}
	rp := rps[0]
	old, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeBaseStation, "动力机房2")
	if err != nil {
		t.Fatalf("GetByTypeKey(动力机房2): %v", err)
	}
	newRes, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeBaseStation, "动力机房2新")
	if err != nil {
		t.Fatalf("GetByTypeKey(动力机房2新): %v", err)
	}
	if rp.ResourceID != old.ID || rp.NewResourceID != newRes.ID || rp.BatchID != b2 ||
		rp.OldKey != "动力机房2" || rp.NewKey != "动力机房2新" ||
		rp.Status != domain.RenameStatusPending || rp.Sheet != baseStationSheetName || rp.RowNumber != 2 ||
		rp.DecidedAt != nil {
		t.Fatalf("proposal = %+v, want old→new of batch %s sheet %s row 2", rp, b2, baseStationSheetName)
	}
	if len(rp.MatchedFields) == 0 {
		t.Fatalf("proposal matched fields = %v, want the non-empty equal fields", rp.MatchedFields)
	}
	for _, f := range rp.MatchedFields {
		if f == "base_station_name" || f == "city" || f == "base_station_code" {
			t.Errorf("matched fields must exclude key/fallback components, got %v", rp.MatchedFields)
		}
	}

	// One W006 issue on the NEW resource with its source ref.
	issues := w006Issues(t, db)
	if len(issues) != 1 {
		t.Fatalf("W006 issues after rename import = %d, want 1", len(issues))
	}
	iss := issues[0]
	if iss.Severity != domain.SeverityWarning || iss.EntityKind != domain.EntityKindResource ||
		iss.EntityID != newRes.ID || iss.BatchID == nil || *iss.BatchID != b2 {
		t.Fatalf("W006 issue = %+v, want warning on the new resource of batch %s", iss, b2)
	}
	if iss.SourceRefID == nil {
		t.Fatalf("W006 issue must carry the imported row's source ref, got %+v", iss)
	}
	srcRef, err := db.SourceRefs.GetByID(ctx, *iss.SourceRefID)
	if err != nil || srcRef.EntityID != newRes.ID || srcRef.BatchID != b2 ||
		srcRef.Sheet != baseStationSheetName || srcRef.RowNumber != 2 {
		t.Fatalf("W006 source ref = %+v (err %v), want the new resource's own ref", srcRef, err)
	}
	for key, want := range map[string]string{
		"type": "base_station", "old_key": "动力机房2", "new_key": "动力机房2新",
		"resource_id": old.ID, "new_resource_id": newRes.ID,
		"sheet": baseStationSheetName, "row_number": "2",
	} {
		if iss.Evidence[key] != want {
			t.Errorf("W006 evidence[%s] = %q, want %q", key, iss.Evidence[key], want)
		}
	}

	// Run 3: the SAME renamed sheet again — pure skip (the new key exists,
	// attributes equal → no W005 either), zero new issues, zero new
	// proposals, the first proposal intact.
	b3, sum3 := importRenameWorkbook(t, svc, [][]string{
		baseStationRealHeaders,
		renameRow("动力机房2新", "4.28.2.3", "172.7.10.125/30"),
		[]string{"宏基站", "会展中心B1", "BTS-HLJ-0001", "否", "4.28.1.10", "环1", "链2", "枢纽楼A4", "gei_1/12", "12.17.10.126", "172.7.11.8/30", "BBU接入", "202367", ""},
		renameRow("动力机房2新", "4.28.2.4", "172.7.10.126/30"),
		[]string{"宏基站", "", "", "是", "4.28.2.5", "环1", "链1", "枢纽楼A4", "gei_1/11", "12.17.10.126", "172.7.10.127/30", "", "", ""},
		[]string{"宏基站", "学府路2", "", "是", "999.28.2.6", "环2", "链1", "枢纽楼A4", "gei_1/13", "12.17.10.126", "172.7.10.128/30", "", "202368", ""},
	})
	_ = b3
	if sum3.RowsImported != 0 || sum3.RowsSkipped != 2 || sum3.RowsFailed != 3 {
		t.Fatalf("run 3 summary = %+v, want 0 imported / 2 skipped / 3 failed", sum3)
	}
	if sum3.IssuesBySeverity[domain.SeverityWarning] != 0 {
		t.Fatalf("run 3 warnings = %d, want 0 (idempotent: no W004/W005/W006)", sum3.IssuesBySeverity[domain.SeverityWarning])
	}
	if rps = pendingRenames(t, db); len(rps) != 1 || rps[0].ID != rp.ID {
		t.Fatalf("pending renames after re-import = %+v, want the unchanged first proposal", rps)
	}
	if n := len(w006Issues(t, db)); n != 1 {
		t.Fatalf("W006 issues after re-import = %d, want 1 (not re-recorded)", n)
	}
	// And no W005 fired either (attributes are equal on the skip branch).
	if n := len(w005Issues(t, db)); n != 0 {
		t.Fatalf("W005 issues after re-import = %d, want 0", n)
	}
}

// TestImportRenameDetectionNegativeConditions pins each proposal condition
// with its own negative: (1) code-keyed types never propose; (2) an old key
// still present in the sheet disqualifies its resource; (3) an attribute
// difference disqualifies the match; (4) a broken bijection (two candidates
// or two rows) stays silent; (5) a hollow match (all match fields empty on
// both sides) stays silent.
func TestImportRenameDetectionNegativeConditions(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()

	// ---- (2) old key still in the sheet: the candidate's key must have
	// vanished from the source. ----
	importRenameWorkbook(t, svc, [][]string{
		baseStationRealHeaders,
		renameRow("站A", "4.28.2.3", "172.7.10.125/30"),
		renameRow("站B", "4.28.2.9", "172.7.10.200/30"),
	})
	// The sheet keeps 站B while 站A is renamed: 站B's key is in the universe,
	// so it is no candidate; 站A's key vanished and its rename fires.
	importRenameWorkbook(t, svc, [][]string{
		baseStationRealHeaders,
		renameRow("站A新", "4.28.2.3", "172.7.10.125/30"),
		renameRow("站B", "4.28.2.9", "172.7.10.200/30"),
	})
	rps := pendingRenames(t, db)
	if len(rps) != 1 || rps[0].OldKey != "站A" {
		t.Fatalf("(2) proposals = %+v, want exactly the 站A rename (站B still in sheet)", rps)
	}

	// ---- (3) attribute difference: the renamed row changed 基站IP — no
	// match, no proposal, the row still imports. ----
	svc2, db2 := newService(t)
	importRenameWorkbook(t, svc2, [][]string{
		baseStationRealHeaders,
		renameRow("站A", "4.28.2.3", "172.7.10.125/30"),
	})
	_, sum := importRenameWorkbook(t, svc2, [][]string{
		baseStationRealHeaders,
		renameRow("站A新", "4.28.2.3", "172.7.10.999/30"),
	})
	if sum.RowsImported != 1 {
		t.Fatalf("(3) run 2 summary = %+v, want the row imported", sum)
	}
	if rps := pendingRenames(t, db2); len(rps) != 0 {
		t.Fatalf("(3) proposals = %d, want 0 (attribute differs)", len(rps))
	}
	if n := len(w006Issues(t, db2)); n != 0 {
		t.Fatalf("(3) W006 issues = %d, want 0", n)
	}

	// ---- (4a) two candidates match one row: ambiguous, silence. ----
	svc3, db3 := newService(t)
	importRenameWorkbook(t, svc3, [][]string{
		baseStationRealHeaders,
		renameRow("站A", "4.28.2.3", "172.7.10.125/30"),
		renameRow("站A复制", "4.28.2.3", "172.7.10.125/30"),
	})
	// A third sheet renames the row again — now BOTH old resources (站A and
	// 站A复制, keys gone, attributes identical) match the single new row.
	importRenameWorkbook(t, svc3, [][]string{
		baseStationRealHeaders,
		renameRow("站A新", "4.28.2.3", "172.7.10.125/30"),
	})
	if rps := pendingRenames(t, db3); len(rps) != 0 {
		t.Fatalf("(4a) proposals = %d, want 0 (two candidates — ambiguous)", len(rps))
	}
	if n := len(w006Issues(t, db3)); n != 0 {
		t.Fatalf("(4a) W006 issues = %d, want 0", n)
	}

	// ---- (4b) two rows match one candidate: ambiguous, silence. ----
	svc4, db4 := newService(t)
	importRenameWorkbook(t, svc4, [][]string{
		baseStationRealHeaders,
		renameRow("站A", "4.28.2.3", "172.7.10.125/30"),
	})
	// One sheet carries TWO differently-named twins of 站A (e.g. an
	// accidental split export): both new rows match the one candidate.
	importRenameWorkbook(t, svc4, [][]string{
		baseStationRealHeaders,
		renameRow("站A新", "4.28.2.3", "172.7.10.125/30"),
		renameRow("站A又新", "4.28.2.3", "172.7.10.125/30"),
	})
	if rps := pendingRenames(t, db4); len(rps) != 0 {
		t.Fatalf("(4b) proposals = %d, want 0 (candidate matched twice — ambiguous)", len(rps))
	}
	if n := len(w006Issues(t, db4)); n != 0 {
		t.Fatalf("(4b) W006 issues = %d, want 0", n)
	}

	// ---- (5) hollow match: a name-only mapping leaves every match field
	// empty on both sides — no proposal. ----
	svc5, db5 := newService(t)
	nameOnly := mapping.Mapping{Sheets: []mapping.SheetMapping{{
		Sheet: baseStationSheetName, Action: mapping.ActionImport,
		ResourceType: domain.ResourceTypeBaseStation, KeyColumns: []string{"base_station_code"},
		ColumnMapping: map[string]string{"base_station_name": "无线基站名称"},
	}}}
	for _, name := range []string{"站A", "站A新"} {
		path := writeMultiSheetWorkbook(t, []sheetSpec{{name: baseStationSheetName, rows: [][]string{
			{"基站类型", "无线基站名称", "基站编码"}, {"宏基站", name, ""},
		}}})
		a, err := svc5.Analyze(ctx, path)
		if err != nil {
			t.Fatalf("(5) Analyalyze: %v", err)
		}
		if _, err := svc5.Execute(ctx, a.BatchID, nameOnly); err != nil {
			t.Fatalf("(5) Execute: %v", err)
		}
	}
	if rps := pendingRenames(t, db5); len(rps) != 0 {
		t.Fatalf("(5) proposals = %d, want 0 (hollow match — all match fields empty)", len(rps))
	}
	if n := len(w006Issues(t, db5)); n != 0 {
		t.Fatalf("(5) W006 issues = %d, want 0", n)
	}

	// ---- (1) code-keyed type: machine_room changes its 机房ID (a whole
	// new identity by doctrine) with every other attribute equal — no
	// proposal, both resources independent. ----
	svc6, db6 := newService(t)
	header := siteMachineRoomHeaders
	for _, roomID := range []string{"23100120000018049", "23100120000018050"} {
		row := []string{"大兴", "呼区", "源1", "4.8.14.8", roomID, "呼", "231001824048", "源"}
		path := writeMultiSheetWorkbook(t, []sheetSpec{{name: siteMachineRoomSheetName, rows: [][]string{header, row}}})
		a, err := svc6.Analyze(ctx, path)
		if err != nil {
			t.Fatalf("(1) Analyalyze: %v", err)
		}
		if _, err := svc6.Execute(ctx, a.BatchID, machineRoomReadingMapping(siteMachineRoomSheetName, false)); err != nil {
			t.Fatalf("(1) Execute: %v", err)
		}
	}
	if rps := pendingRenames(t, db6); len(rps) != 0 {
		t.Fatalf("(1) proposals = %d, want 0 (machine_room is code-keyed — rename detection off)", len(rps))
	}
	if n := len(w006Issues(t, db6)); n != 0 {
		t.Fatalf("(1) W006 issues = %d, want 0", n)
	}
}

// TestImportRenameIncrementalFalsePositive pins the DECLARED false-positive
// face of the detection: an incremental (partial) sheet that renames one
// station while its twin stays in the database but NOT in the sheet still
// proposes the rename — the key universe is per import run, and "old key
// absent from this source" is indistinguishable from "renamed in a delta
// sheet" without cross-batch source bookkeeping (SourceRef versioning is
// the follow-up work, see natural-keys.md §6 #4). The human sees the
// proposal and can reject it.
func TestImportRenameIncrementalFalsePositive(t *testing.T) {
	svc, db := newService(t)

	// Full sheet: 站A + 站B.
	importRenameWorkbook(t, svc, [][]string{
		baseStationRealHeaders,
		renameRow("站A", "4.28.2.3", "172.7.10.125/30"),
		renameRow("站B", "4.28.2.9", "172.7.10.200/30"),
	})
	// Incremental sheet: only the renamed 站A row. 站B is absent from the
	// sheet but its resource lives on — the detection cannot tell this from
	// a rename and proposes.
	importRenameWorkbook(t, svc, [][]string{
		baseStationRealHeaders,
		renameRow("站A新", "4.28.2.3", "172.7.10.125/30"),
	})
	rps := pendingRenames(t, db)
	if len(rps) != 1 || rps[0].OldKey != "站A" {
		t.Fatalf("incremental proposals = %+v, want the 站A rename (declared false-positive face)", rps)
	}
}

// TestImportRenameApplyRetargetsRelations walks the one E2E relation
// scenario the seven types offer for a rename candidate: an ip_pool rename
// carries the pool's assigned_to edge. Import POOL-1 (assigned to DEV-001),
// re-import the sheet with the pool renamed (same cidr/gateway/vlan) → new
// resource + edge + W006 + proposal; apply through the renames service →
// the edge is retargeted to the OLD pool id, the new resource is gone, the
// old pool carries the new key.
func TestImportRenameApplyRetargetsRelations(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()

	// A device for the assigned_to edge.
	devPath := writeWorkbookFile(t, "设备清单", [][]string{
		attrChangeHeaders,
		attrChangeRow("DEV-001", "核心路由1", "杭州", "华为", "NE40E", "10.0.0.1", "在网"),
	})
	a, err := svc.Analyze(ctx, devPath)
	if err != nil {
		t.Fatalf("Analyze device: %v", err)
	}
	if _, err := svc.Execute(ctx, a.BatchID, attrChangeMapping("设备清单")); err != nil {
		t.Fatalf("Execute device: %v", err)
	}

	poolHeaders := []string{"地址池名称", "CIDR", "网关", "VLAN", "归属设备", "状态"}
	poolMapping := mapping.Mapping{Sheets: []mapping.SheetMapping{{
		Sheet: "地址池", Action: mapping.ActionImport,
		ResourceType: domain.ResourceTypeIPPool, KeyColumns: []string{"pool_name"},
		ColumnMapping: map[string]string{
			"pool_name": "地址池名称", "cidr": "CIDR", "gateway": "网关",
			"vlan_id": "VLAN", "assigned_device": "归属设备", "status": "状态",
		},
	}}}
	importPools := func(name string) Summary {
		t.Helper()
		path := writeMultiSheetWorkbook(t, []sheetSpec{{name: "地址池", rows: [][]string{
			poolHeaders, {name, "10.30.1.0/24", "10.30.1.1", "100", "DEV-001", "在网"},
		}}})
		a, err := svc.Analyze(ctx, path)
		if err != nil {
			t.Fatalf("Analyze pools: %v", err)
		}
		sum, err := svc.Execute(ctx, a.BatchID, poolMapping)
		if err != nil {
			t.Fatalf("Execute pools: %v", err)
		}
		return sum
	}

	if sum := importPools("POOL-1"); sum.RowsImported != 1 {
		t.Fatalf("pool import 1 = %+v, want 1 imported", sum)
	}
	old, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeIPPool, "POOL-1")
	if err != nil {
		t.Fatalf("GetByTypeKey(POOL-1): %v", err)
	}
	// The first import's edge: POOL-1 --assigned_to--> DEV-001.
	edges, err := db.Relations.ListByResource(ctx, old.ID)
	if err != nil || len(edges) != 1 || edges[0].Type != domain.RelationTypeAssignedTo || edges[0].ToResourceID == "" {
		t.Fatalf("POOL-1 edges = %+v (err %v), want one assigned_to", edges, err)
	}

	// The renamed pool: same cidr/gateway/vlan/device, new name.
	if sum := importPools("POOL-1新"); sum.RowsImported != 1 {
		t.Fatalf("pool import 2 = %+v, want 1 imported (the new key)", sum)
	}
	rps := pendingRenames(t, db)
	if len(rps) != 1 {
		t.Fatalf("pending renames after pool rename = %d, want 1", len(rps))
	}
	if rps[0].OldKey != "POOL-1" || rps[0].NewKey != "POOL-1新" || rps[0].ResourceType != domain.ResourceTypeIPPool {
		t.Fatalf("pool proposal = %+v, want POOL-1→POOL-1新 of ip_pool", rps[0])
	}
	if got, _ := db.Relations.CountByType(ctx); got[domain.RelationTypeAssignedTo] != 2 {
		t.Fatalf("assigned_to after pool rename = %d, want 2 (old + new pool edges)", got[domain.RelationTypeAssignedTo])
	}

	// Apply: the new pool's edge moves to the old pool id.
	svcRenames := renames.New(db)
	if _, err := svcRenames.Apply(ctx, rps[0].ID); err != nil {
		t.Fatalf("renames apply: %v", err)
	}
	if _, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeIPPool, "POOL-1新"); err != nil {
		t.Fatalf("old resource must now own the key POOL-1新: %v", err)
	}
	if _, err := db.Resources.GetByID(ctx, rps[0].NewResourceID); err == nil {
		t.Fatalf("new pool resource must be deleted by the apply")
	}
	after, err := db.Relations.ListByResource(ctx, old.ID)
	if err != nil || len(after) != 1 || after[0].Type != domain.RelationTypeAssignedTo {
		t.Fatalf("old pool edges after apply = %+v (err %v), want the retargeted assigned_to", after, err)
	}
	if n, _ := db.Relations.CountByType(ctx); n[domain.RelationTypeAssignedTo] != 1 {
		t.Fatalf("assigned_to after apply = %d, want 1 (the retargeted edge)", n[domain.RelationTypeAssignedTo])
	}
	// The old pool id is unchanged (identity keeper).
	if res, err := db.Resources.GetByID(ctx, old.ID); err != nil || res.NaturalKey != "POOL-1新" {
		t.Fatalf("old pool after apply = %+v (err %v), want id %s with key POOL-1新", res, err, old.ID)
	}
}

// TestImportRenameRollbackAsymmetry pins the rollback delete-chain member:
// the detection batch's PENDING proposals die with it (the new resource
// disappears too), while applied/rejected proposals survive as the
// human-decision audit trail.
func TestImportRenameRollbackAsymmetry(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()

	// Batch 1: the original station.
	importRenameWorkbook(t, svc, [][]string{
		baseStationRealHeaders,
		renameRow("站A", "4.28.2.3", "172.7.10.125/30"),
	})
	// Batch 2: the rename — one pending proposal, then APPLY it (the old
	// resource takes the new key; the twin is deleted).
	b2, _ := importRenameWorkbook(t, svc, [][]string{
		baseStationRealHeaders,
		renameRow("站A新", "4.28.2.3", "172.7.10.125/30"),
	})
	rps := pendingRenames(t, db)
	if len(rps) != 1 || rps[0].BatchID != b2 {
		t.Fatalf("pending renames after rename import = %+v, want one of batch %s", rps, b2)
	}
	appliedID := rps[0].ID
	svcRenames := renames.New(db)
	if _, err := svcRenames.Apply(ctx, appliedID); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Batch 3: yet another rename — one fresh pending proposal.
	b3, _ := importRenameWorkbook(t, svc, [][]string{
		baseStationRealHeaders,
		renameRow("站A新新", "4.28.2.3", "172.7.10.125/30"),
	})
	rps = pendingRenames(t, db)
	if len(rps) != 1 || rps[0].BatchID != b3 {
		t.Fatalf("pending renames after second rename = %+v, want one of batch %s", rps, b3)
	}

	// Roll back the second rename batch (b3): its PENDING proposal dies (the
	// new resource disappears with the batch — a dangling proposal would be
	// meaningless); the APPLIED proposal of b2 survives.
	if err := svc.Rollback(ctx, b3); err != nil {
		t.Fatalf("Rollback(%s): %v", b3, err)
	}
	rps = pendingRenames(t, db)
	if len(rps) != 0 {
		t.Fatalf("pending renames after rollback = %+v, want 0", rps)
	}
	if rp, err := db.RenameProposals.Get(ctx, appliedID); err != nil || rp.Status != domain.RenameStatusApplied {
		t.Fatalf("applied proposal after foreign-batch rollback = %+v (err %v), want surviving audit trail", rp, err)
	}
	// The b3 new resource disappeared with its batch; the old resource
	// (identity keeper, from b1) still carries the applied key.
	if _, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeBaseStation, "站A新新"); err == nil {
		t.Fatalf("站A新新 (batch %s) must be gone after rollback", b3)
	}
	if _, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeBaseStation, "站A新"); err != nil {
		t.Fatalf("站A新 must survive (the applied old resource): %v", err)
	}

	// Rolling back b2 — the applied proposal's OWN batch — keeps the
	// decision (the human audit trail is decoupled from the batch
	// lifecycle; the merge's writes went to b1's resource, not b2's).
	if err := svc.Rollback(ctx, b2); err != nil {
		t.Fatalf("Rollback(%s): %v", b2, err)
	}
	if rp, err := db.RenameProposals.Get(ctx, appliedID); err != nil || rp.Status != domain.RenameStatusApplied {
		t.Fatalf("applied proposal after own-batch rollback = %+v (err %v), want applied (audit trail survives)", rp, err)
	}
	if _, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeBaseStation, "站A新"); err != nil {
		t.Fatalf("站A新 must survive its own rename batch's rollback (the resource belongs to b1): %v", err)
	}
}
