package importer

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/mapping"
	"github.com/mmqz/netops-lab/internal/repo"
)

// TestW21CrossLayerChainFixture is the W21 acceptance test: the full
// L6→L2 business-to-physical chain built through REAL imports (in-test
// workbooks, custom confirmed mappings — no testdata/ changes), asserting
// every derived relation of the new W21 catalog:
//
//	customer CUST-01
//	  ↑ subscribed_by            (service SVC-01)
//	service SVC-01 ─ provided_by → circuit CIR-01
//	                           └─ depends_on → device DEV-C1
//	circuit CIR-01 ─ carried_by → otn_och OCH-01
//	otn_och OCH-01 ─ routed_over → optical_cable CABLE-01
//	               └ uses_fiber → fiber CABLE-01|1
//	fiber CABLE-01|1 ─ fiber_of → optical_cable CABLE-01
//	                 └ fiber_terminates → odf_terminal ODF-01|1
//	optical_cable CABLE-01 ─ cable_a_end → site SITE-01
//	                       └ cable_z_end → site SITE-02
//	machine_room MR-01 ─ belongs_to → site SITE-01   (existing v10 edge)
//	device DEV-C1 ─ housed_in → machine_room MR-01   (new W21 edge)
//
// This is the graph the W22 traverse/impact endpoints will walk — the
// fixture doubles as their seed data contract.
func TestW21CrossLayerChainFixture(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()

	// importTyped runs one synthetic single-sheet workbook through
	// analyze → execute with the given explicit mapping (the wizard's
	// confirmed-mapping path; suggestion is not under test here).
	importTyped := func(sheet string, headers []string, rows [][]string, rt domain.ResourceType, keyCols []string, colMap map[string]string) Summary {
		t.Helper()
		all := append([][]string{headers}, rows...)
		path := writeWorkbookFile(t, sheet, all)
		a, err := svc.Analyze(ctx, path)
		if err != nil {
			t.Fatalf("Analyze(%s): %v", sheet, err)
		}
		m := mapping.Mapping{Sheets: []mapping.SheetMapping{{
			Sheet:         sheet,
			Action:        mapping.ActionImport,
			ResourceType:  rt,
			KeyColumns:    keyCols,
			ColumnMapping: colMap,
		}}}
		sum, err := svc.Execute(ctx, a.BatchID, m)
		if err != nil {
			t.Fatalf("Execute(%s): %v", sheet, err)
		}
		return sum
	}

	// ① sites (L0).
	siteHeaders := []string{"局站ID", "局站名称"}
	importTyped("局站资源表", siteHeaders,
		[][]string{{"SITE-01", "加区枢纽楼"}, {"SITE-02", "道里汇聚局"}},
		domain.ResourceTypeSite, []string{"site_id"},
		map[string]string{"site_id": "局站ID", "site_name": "局站名称"})

	// ② machine_room (L0) → belongs_to SITE-01.
	mrHeaders := []string{"机房ID", "机房名称", "局站ID"}
	importTyped("机房资源表", mrHeaders,
		[][]string{{"MR-01", "枢纽一楼机房", "SITE-01"}},
		domain.ResourceTypeMachineRoom, []string{"machine_room_id"},
		map[string]string{"machine_room_id": "机房ID", "machine_room_name": "机房名称", "site_id": "局站ID"})

	// ③ device (L1) → housed_in MR-01 (the new optional ref column).
	devHeaders := []string{"设备编码", "设备名称", "厂商", "机房ID"}
	importTyped("网元设备", devHeaders,
		[][]string{{"DEV-C1", "加区C1路由器", "合成厂商", "MR-01"}},
		domain.ResourceTypeDevice, []string{"device_code"},
		map[string]string{"device_code": "设备编码", "device_name": "设备名称", "vendor": "厂商", "machine_room_id": "机房ID"})

	// ④ customer (L6).
	custHeaders := []string{"客户编码", "客户名称"}
	importTyped("客户资源表", custHeaders,
		[][]string{{"CUST-01", "哈尔滨银行"}},
		domain.ResourceTypeCustomer, []string{"customer_code"},
		map[string]string{"customer_code": "客户编码", "customer_name": "客户名称"})

	// ⑤ odf_terminal (L2) — composite key ODF-01|1.
	otHeaders := []string{"ODF编码", "端子号"}
	importTyped("端子资源表", otHeaders,
		[][]string{{"ODF-01", "1"}},
		domain.ResourceTypeODFTerminal, []string{"odf_code", "terminal_no"},
		map[string]string{"odf_code": "ODF编码", "terminal_no": "端子号"})

	// ⑥ optical_cable (L2) → cable_a_end SITE-01 / cable_z_end SITE-02.
	// Row 2 carries the B8 normalization variant " cable-01 " (trim+upper
	// → CABLE-01) → E001 duplicate within the batch: the anti-collapse
	// gate (format variants are ONE identity; 1 vs 01 would stay two).
	cableHeaders := []string{"光缆编码", "光缆名称", "A端局站", "Z端局站", "芯数"}
	cableSum := importTyped("光缆资源表", cableHeaders,
		[][]string{{"CABLE-01", "加区-道里主干光缆", "SITE-01", "SITE-02", "48"}, {" cable-01 ", "重复编码行", "SITE-01", "SITE-02", "48"}},
		domain.ResourceTypeOpticalCable, []string{"cable_code"},
		map[string]string{"cable_code": "光缆编码", "cable_name": "光缆名称", "a_end_site": "A端局站", "z_end_site": "Z端局站", "core_count": "芯数"})
	if cableSum.RowsImported != 1 || cableSum.RowsFailed != 1 {
		t.Fatalf("cable import: imported=%d failed=%d, want 1/1 (B8 variant collapses onto CABLE-01 → E001)", cableSum.RowsImported, cableSum.RowsFailed)
	}

	// ⑦ fiber (L2) → fiber_of CABLE-01 + fiber_terminates ODF-01|1.
	fiberHeaders := []string{"光缆编码", "纤芯号", "成端ODF端子", "使用情况"}
	importTyped("纤芯资源表", fiberHeaders,
		[][]string{{"CABLE-01", "1", "ODF-01|1", "在用"}},
		domain.ResourceTypeFiber, []string{"cable_code", "fiber_no"},
		map[string]string{"cable_code": "光缆编码", "fiber_no": "纤芯号", "terminate_odf": "成端ODF端子", "usage": "使用情况"})

	// ⑧ otn_och (L3) → routed_over CABLE-01 + uses_fiber CABLE-01|1.
	ochHeaders := []string{"波道编码", "波道号", "路由光缆", "占用纤芯"}
	importTyped("波道资源表", ochHeaders,
		[][]string{{"OCH-01", "5", "CABLE-01", "CABLE-01|1"}},
		domain.ResourceTypeOTNOch, []string{"och_code"},
		map[string]string{"och_code": "波道编码", "wave_no": "波道号", "route_cable": "路由光缆", "use_fiber": "占用纤芯"})

	// ⑨ circuit (L3) → carried_by OCH-01.
	cirHeaders := []string{"电路编号", "A端设备", "Z端设备", "承载波道"}
	cirSum := importTyped("传输电路", cirHeaders,
		[][]string{{"CIR-01", "DEV-C1", "DEV-C1", "OCH-01"}},
		domain.ResourceTypeCircuit, []string{"circuit_code"},
		map[string]string{"circuit_code": "电路编号", "a_end_device": "A端设备", "z_end_device": "Z端设备", "carried_och": "承载波道"})
	_ = cirSum

	// ⑩ service (L6) → subscribed_by CUST-01 + provided_by CIR-01 + depends_on DEV-C1.
	svcHeaders := []string{"业务编码", "业务类型", "客户编码", "提供电路", "依赖设备"}
	importTyped("业务实例表", svcHeaders,
		[][]string{{"SVC-01", "金融专线", "CUST-01", "CIR-01", "DEV-C1"}},
		domain.ResourceTypeService, []string{"service_code"},
		map[string]string{"service_code": "业务编码", "service_type": "业务类型", "customer_code": "客户编码", "provide_circuit": "提供电路", "depend_device": "依赖设备"})

	// ① leased_line (L6, W25 #16 验收补齐) → subscribed_by CUST-01.
	// 真实专线表的「客户名称」列现在是 ref：按客户自然键解析出边。
	llHeaders := []string{"专线编号", "专线名称", "客户名称", "协议带宽(Mbps)"}
	importTyped("专线资源表", llHeaders,
		[][]string{{"LL-01", "哈尔滨银行总部专线", "CUST-01", "200"}},
		domain.ResourceTypeLeasedLine, []string{"line_code"},
		map[string]string{"line_code": "专线编号", "line_name": "专线名称", "customer_code": "客户名称", "bandwidth_mbps": "协议带宽(Mbps)"})

	// ── Relation assertions ──────────────────────────────────────────────
	counts, err := db.Relations.CountByType(ctx)
	if err != nil {
		t.Fatalf("CountByType: %v", err)
	}
	want := map[domain.RelationType]int{
		domain.RelationTypeBelongsTo:       1, // MR-01 → SITE-01
		domain.RelationTypeHousedIn:        1, // DEV-C1 → MR-01
		domain.RelationTypeCableAEnd:       1, // CABLE-01 → SITE-01
		domain.RelationTypeCableZEnd:       1, // CABLE-01 → SITE-02
		domain.RelationTypeFiberOf:         1, // CABLE-01|1 → CABLE-01
		domain.RelationTypeFiberTerminates: 1, // CABLE-01|1 → ODF-01|1
		domain.RelationTypeRoutedOver:      1, // OCH-01 → CABLE-01
		domain.RelationTypeUsesFiber:       1, // OCH-01 → CABLE-01|1
		domain.RelationTypeCarriedBy:       1, // CIR-01 → OCH-01
		domain.RelationTypeSubscribedBy:    2, // SVC-01 → CUST-01 + LL-01 → CUST-01（W25）
		domain.RelationTypeProvidedBy:      1, // SVC-01 → CIR-01
		domain.RelationTypeDependsOn:       1, // SVC-01 → DEV-C1
		domain.RelationTypeAEnd:            1, // CIR-01 → DEV-C1 (circuit sheet)
		domain.RelationTypeZEnd:            1, // CIR-01 → DEV-C1
	}
	for rt, n := range want {
		if counts[rt] != n {
			t.Errorf("relation %s: count = %d, want %d (all counts: %v)", rt, counts[rt], n, counts)
		}
	}
	total := 0
	for _, n := range counts {
		total += n
	}
	if total != 15 {
		t.Errorf("total relations = %d, want 15 (counts: %v)", total, counts)
	}

	// Edge identity spot-checks: the chain's key hops, by natural key.
	edge := func(fromType, fromKey, relation, toType, toKey string) {
		t.Helper()
		fromID, err := db.Resources.GetByTypeKey(ctx, domain.ResourceType(fromType), fromKey)
		if err != nil {
			t.Fatalf("resource %s/%s: %v", fromType, fromKey, err)
		}
		toID, err := db.Resources.GetByTypeKey(ctx, domain.ResourceType(toType), toKey)
		if err != nil {
			t.Fatalf("resource %s/%s: %v", toType, toKey, err)
		}
		rels, err := db.Relations.ListByResource(ctx, fromID.ID)
		if err != nil {
			t.Fatalf("ListByResource(%s): %v", fromKey, err)
		}
		for _, r := range rels {
			if r.Type == domain.RelationType(relation) && r.FromResourceID == fromID.ID && r.ToResourceID == toID.ID {
				return // found
			}
		}
		t.Errorf("edge %s --%s--> %s not found", fromKey, relation, toKey)
	}
	edge("service", "SVC-01", "subscribed_by", "customer", "CUST-01")
	edge("leased_line", "LL-01", "subscribed_by", "customer", "CUST-01") // W25
	edge("service", "SVC-01", "provided_by", "circuit", "CIR-01")
	edge("service", "SVC-01", "depends_on", "device", "DEV-C1")
	edge("circuit", "CIR-01", "carried_by", "otn_och", "OCH-01")
	edge("otn_och", "OCH-01", "routed_over", "optical_cable", "CABLE-01")
	edge("otn_och", "OCH-01", "uses_fiber", "fiber", "CABLE-01|1")
	edge("fiber", "CABLE-01|1", "fiber_of", "optical_cable", "CABLE-01")
	edge("fiber", "CABLE-01|1", "fiber_terminates", "odf_terminal", "ODF-01|1")
	edge("optical_cable", "CABLE-01", "cable_a_end", "site", "SITE-01")
	edge("optical_cable", "CABLE-01", "cable_z_end", "site", "SITE-02")
	edge("device", "DEV-C1", "housed_in", "machine_room", "MR-01")
	edge("machine_room", "MR-01", "belongs_to", "site", "SITE-01")

	// ── Idempotency: re-import the circuit sheet ────────────────────────
	path := writeWorkbookFile(t, "传输电路", append([][]string{cirHeaders}, [][]string{{"CIR-01", "DEV-C1", "DEV-C1", "OCH-01"}}...))
	a, err := svc.Analyze(ctx, path)
	if err != nil {
		t.Fatalf("re-analyze: %v", err)
	}
	m := mapping.Mapping{Sheets: []mapping.SheetMapping{{
		Sheet: "传输电路", Action: mapping.ActionImport, ResourceType: domain.ResourceTypeCircuit,
		KeyColumns:    []string{"circuit_code"},
		ColumnMapping: map[string]string{"circuit_code": "电路编号", "a_end_device": "A端设备", "z_end_device": "Z端设备", "carried_och": "承载波道"},
	}}}
	sum, err := svc.Execute(ctx, a.BatchID, m)
	if err != nil {
		t.Fatalf("re-execute: %v", err)
	}
	if sum.RowsImported != 0 || sum.RowsSkipped != 1 {
		t.Errorf("re-import: imported=%d skipped=%d, want 0/1", sum.RowsImported, sum.RowsSkipped)
	}
	counts2, err := db.Relations.CountByType(ctx)
	if err != nil {
		t.Fatalf("CountByType after re-import: %v", err)
	}
	if counts2[domain.RelationTypeCarriedBy] != 1 || counts2[domain.RelationTypeAEnd] != 1 {
		t.Errorf("re-import changed relations: carried_by=%d a_end=%d, want 1/1", counts2[domain.RelationTypeCarriedBy], counts2[domain.RelationTypeAEnd])
	}

	// ── B4 guard: the existing devices.xlsx baseline is untouched ──────
	// (covered exhaustively by the legacy suite; one spot pin here: the
	// chain imports above never invented relations for existing types'
	// text columns — OLT/BBU machine/base_station columns are plain
	// strings, zero derivation).
	relTotal, err := db.Relations.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(relTotal) != 15 {
		t.Errorf("ListAll = %d relations, want 15", len(relTotal))
	}
	_ = filepath.Join
	_ = repo.Open
}
