package mapping

// The 25 W21 扩容 type specs (data-model v1.0 §3.2, migration v15): one
// TypeSpec per new resource type, in §3.3's catalog order — space (rack,
// duct, manhole, pole) → device (board) → optical (optical_cable,
// cable_segment, fiber, odf, odf_terminal, splice_box) → transport
// (otn_och, sdh_timeslot, protection_group) → logic (ring, igp_domain,
// vpn_instance) → business (customer, service, leased_line) → access
// (bbu, rru, olt, onu, pon_port).
//
// Shared conventions of the whole family:
//
//   - Natural keys are code-keyed (rack_code, cable_code, …), so every
//     spec sets KeyNormalize true (§12-B8): trim + 全角→半角 + upper on
//     every key component before the join; leading zeros preserved ("1"
//     ≠ "01" — the anti-collapse rule).
//   - KindRef fields carry the ref columns whose resolution derives the
//     §4.2 relation (mapping.RelationTypeFor). Refs resolve by the
//     TARGET's natural key — composite keys (odf_terminal
//     "odf_code|terminal_no", fiber "cable_code|fiber_no", pon_port
//     "olt_code|port_no") therefore expect the joined key string in the
//     cell. E003 fires on unresolvable values (E003 is the import-ORDER
//     enforcer: containers first).
//   - Optional refs stay optional (unmapped column ⇒ no W001, no
//     relation — B4 zero auto-derivation); required refs are the ones the
//     §4.2 relation cannot exist without (board.device_code,
//     fiber.cable_code, …).
//   - Field lists are deliberately LEAN: §3.2's 关键字段 vocabulary plus
//     the ref columns — nothing more. No new quality rules; kinds stay
//     the honest ones (numeric-looking values with units/fractions —
//     波长, 分光比, 长度 — are KindString so E004 never rejects real
//     sheets; pure counts — 芯数, 孔数, 节点数 — are KindInt).

import (
	"strings"

	"github.com/mmqz/netops-lab/internal/domain"
)

// rackSpec is the 机架 schema (L0 空间层): rack_code keyed; machine_room_id
// is the optional located_in ref (rack → machine_room).
var rackSpec = TypeSpec{
	Type:         domain.ResourceTypeRack,
	Label:        "机架",
	Layer:        LayerSpace,
	Domain:       DomainSpace,
	SheetHint:    "机架",
	KeyNormalize: true,
	Fields: []FieldSpec{
		{Name: "rack_code", Label: "机架编码", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"机架编码", "机架编号", "机架ID", "机架代码"}},
		{Name: "machine_room_id", Label: "所属机房", Kind: KindRef, RefType: domain.ResourceTypeMachineRoom,
			Synonyms: []string{"所属机房", "机房ID", "机房编号", "所在机房"}},
		{Name: "column_no", Label: "列号", Kind: KindInt, Synonyms: []string{"列号", "列"}},
		{Name: "row_no", Label: "行号", Kind: KindInt, Synonyms: []string{"行号", "行"}},
	},
}

// ductSpec is the 管道 schema (L0): duct_code keyed. 起止局站 are PLAIN
// STRING columns (the §3.2 vocabulary has no duct ref columns in the W21
// ref-field contract — no relation derives from them; a future duct→site
// relation needs an explicit design round).
var ductSpec = TypeSpec{
	Type:         domain.ResourceTypeDuct,
	Label:        "管道",
	Layer:        LayerSpace,
	Domain:       DomainSpace,
	SheetHint:    "管道",
	KeyNormalize: true,
	Fields: []FieldSpec{
		{Name: "duct_code", Label: "管道编码", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"管道编码", "管道编号", "管道ID", "管段编码"}},
		{Name: "a_end_site", Label: "起点局站", Kind: KindString,
			Synonyms: []string{"起点局站", "A端局站", "起点", "起止局站A"}},
		{Name: "z_end_site", Label: "终点局站", Kind: KindString,
			Synonyms: []string{"终点局站", "Z端局站", "终点", "起止局站Z"}},
		{Name: "length_km", Label: "长度", Kind: KindString, // decimals + units in real sheets
			Synonyms: []string{"长度", "管道长度", "长度(公里)", "长度(米)"}},
		{Name: "hole_count", Label: "孔数", Kind: KindInt, Synonyms: []string{"孔数", "管孔数"}},
	},
}

// manholeSpec is the 人井 schema (L0): manhole_code keyed; duct_code a
// plain string (所属管道 vocabulary, no ref contract); 经纬度 as the
// canonical geo triple.
var manholeSpec = TypeSpec{
	Type:         domain.ResourceTypeManhole,
	Label:        "人井",
	Layer:        LayerSpace,
	Domain:       DomainSpace,
	SheetHint:    "人井",
	KeyNormalize: true,
	Fields: []FieldSpec{
		{Name: "manhole_code", Label: "人井编码", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"人井编码", "人井编号", "人井ID", "井盖编号"}},
		{Name: "duct_code", Label: "所属管道", Kind: KindString,
			Synonyms: []string{"所属管道", "管道编码", "管道编号"}},
		{Name: "geo_lat", Label: "纬度", Kind: KindLat, Synonyms: []string{"纬度", "lat", "GPS纬度"}},
		{Name: "geo_lng", Label: "经度", Kind: KindLng, Synonyms: []string{"经度", "lng", "GPS经度"}},
		{Name: "geo", Label: "经纬度", Kind: KindGeo, Synonyms: []string{"经纬度", "坐标", "GPS坐标"}},
	},
}

// poleSpec is the 杆路 schema (L0): pole_code keyed; 起止 plain strings.
var poleSpec = TypeSpec{
	Type:         domain.ResourceTypePole,
	Label:        "杆路",
	Layer:        LayerSpace,
	Domain:       DomainSpace,
	SheetHint:    "杆路",
	KeyNormalize: true,
	Fields: []FieldSpec{
		{Name: "pole_code", Label: "杆路编码", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"杆路编码", "杆路编号", "杆路ID", "杆号编码"}},
		{Name: "a_end", Label: "起点", Kind: KindString, Synonyms: []string{"起点", "起止A", "A端"}},
		{Name: "z_end", Label: "终点", Kind: KindString, Synonyms: []string{"终点", "起止Z", "Z端"}},
		{Name: "pole_no", Label: "杆号", Kind: KindInt, Synonyms: []string{"杆号", "杆塔号"}},
	},
}

// boardSpec is the 板卡 schema (L1): board_id keyed; device_code the
// REQUIRED installed_in ref (board → device; a boardless-device sheet
// cannot exist by §3.2's vocabulary).
var boardSpec = TypeSpec{
	Type:         domain.ResourceTypeBoard,
	Label:        "板卡",
	Layer:        LayerDevice,
	Domain:       DomainDevice,
	SheetHint:    "板卡",
	KeyNormalize: true,
	Fields: []FieldSpec{
		{Name: "board_id", Label: "板卡ID", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"板卡ID", "板卡编号", "板卡编码", "板卡标识"}},
		{Name: "device_code", Label: "所属设备", Required: true, Kind: KindRef, RefType: domain.ResourceTypeDevice,
			Synonyms: []string{"所属设备", "设备编码", "设备编号", "网元编码"}},
		{Name: "slot_no", Label: "槽位号", Kind: KindInt, Synonyms: []string{"槽位号", "槽位"}},
		{Name: "board_model", Label: "板卡型号", Kind: KindString, Synonyms: []string{"板卡型号", "型号"}},
		{Name: "board_type", Label: "板卡类型", Kind: KindString, Synonyms: []string{"板卡类型", "类型", "板卡类别"}},
	},
}

// opticalCableSpec is the 光缆 schema (L2): cable_code keyed; a_end_site /
// z_end_site the REQUIRED cable_a_end/cable_z_end refs; connect_device the
// OPTIONAL connects_device ref (配线光缆 only).
var opticalCableSpec = TypeSpec{
	Type:         domain.ResourceTypeOpticalCable,
	Label:        "光缆",
	Layer:        LayerOptical,
	Domain:       DomainOptical,
	SheetHint:    "光缆",
	KeyNormalize: true,
	Fields: []FieldSpec{
		{Name: "cable_code", Label: "光缆编码", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"光缆编码", "光缆编号", "光缆ID", "光缆标识"}},
		{Name: "cable_name", Label: "光缆名称", Required: true, Kind: KindString,
			Synonyms: []string{"光缆名称", "光缆名", "名称"}},
		{Name: "a_end_site", Label: "A端局站", Required: true, Kind: KindRef, RefType: domain.ResourceTypeSite,
			Synonyms: []string{"A端局站", "A端站点", "起点局站"}},
		{Name: "z_end_site", Label: "Z端局站", Required: true, Kind: KindRef, RefType: domain.ResourceTypeSite,
			Synonyms: []string{"Z端局站", "Z端站点", "终点局站"}},
		{Name: "core_count", Label: "芯数", Kind: KindInt, Synonyms: []string{"芯数", "纤芯总数", "光纤芯数"}},
		{Name: "length_km", Label: "长度", Kind: KindString, // decimals + units in real sheets
			Synonyms: []string{"长度", "光缆长度", "长度(公里)", "长度(米)"}},
		{Name: "connect_device", Label: "连接设备", Kind: KindRef, RefType: domain.ResourceTypeDevice,
			Synonyms: []string{"连接设备", "所连设备", "对端设备"}},
	},
}

// cableSegmentSpec is the 光缆段 schema (L2): segment_code keyed;
// cable_code the REQUIRED segment_of ref (segment → cable).
var cableSegmentSpec = TypeSpec{
	Type:         domain.ResourceTypeCableSegment,
	Label:        "光缆段",
	Layer:        LayerOptical,
	Domain:       DomainOptical,
	SheetHint:    "光缆段",
	KeyNormalize: true,
	Fields: []FieldSpec{
		{Name: "segment_code", Label: "光缆段编码", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"光缆段编码", "光缆段编号", "段编码", "段编号"}},
		{Name: "cable_code", Label: "所属光缆", Required: true, Kind: KindRef, RefType: domain.ResourceTypeOpticalCable,
			Synonyms: []string{"所属光缆", "光缆编码", "光缆编号"}},
		{Name: "segment_index", Label: "段序", Kind: KindInt, Synonyms: []string{"段序", "段序号", "段号"}},
		{Name: "a_end_box", Label: "A端接头盒", Kind: KindString, Synonyms: []string{"A端接头盒", "A端接头"}},
		{Name: "z_end_box", Label: "Z端接头盒", Kind: KindString, Synonyms: []string{"Z端接头盒", "Z端接头"}},
	},
}

// fiberSpec is the 纤芯 schema (L2): COMPOSITE key cable_code|fiber_no
// (§3.2, the real telecom fiber sheet vocabulary). cable_code is BOTH a
// natural-key component and the REQUIRED fiber_of ref (fiber → optical_
// cable — the W21 direction correction of §4.2's carries_fiber: member →
// container like belongs_to/segment_of). fiber_no stays KindString: the
// B8 anti-collapse rule (1 ≠ 01) requires zero-preserving identity.
// terminate_odf is the OPTIONAL fiber_terminates ref resolving by the
// odf_terminal natural key string "odf_code|terminal_no".
var fiberSpec = TypeSpec{
	Type:         domain.ResourceTypeFiber,
	Label:        "纤芯",
	Layer:        LayerOptical,
	Domain:       DomainOptical,
	SheetHint:    "纤芯",
	KeyNormalize: true,
	Fields: []FieldSpec{
		{Name: "cable_code", Label: "光缆编码", Required: true, NaturalKey: true, Kind: KindRef,
			RefType:  domain.ResourceTypeOpticalCable,
			Synonyms: []string{"光缆编码", "光缆编号", "光缆名称", "所属光缆"}},
		{Name: "fiber_no", Label: "纤芯号", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"纤芯号", "纤芯编号", "芯号"}},
		{Name: "terminate_odf", Label: "成端ODF端子", Kind: KindRef, RefType: domain.ResourceTypeODFTerminal,
			Synonyms: []string{"成端ODF端子", "成端端子", "对端落地点", "成端ODF"}},
		{Name: "fiber_count", Label: "纤芯总数", Kind: KindInt, Synonyms: []string{"纤芯总数", "芯数"}},
		{Name: "usage", Label: "使用情况", Kind: KindString, Synonyms: []string{"使用情况", "占用情况", "状态"}},
		{Name: "usage_direction", Label: "使用局向", Kind: KindString, Synonyms: []string{"使用局向", "局向"}},
		{Name: "tx_rx", Label: "收发关系", Kind: KindString, Synonyms: []string{"收发关系", "收/发"}},
	},
}

// odfSpec is the 光配线架 schema (L2): odf_code keyed. 所属机房 stays a
// plain string (no W21 ref contract for odf → machine_room).
var odfSpec = TypeSpec{
	Type:         domain.ResourceTypeODF,
	Label:        "光配线架",
	Layer:        LayerOptical,
	Domain:       DomainOptical,
	SheetHint:    "ODF",
	KeyNormalize: true,
	Fields: []FieldSpec{
		{Name: "odf_code", Label: "ODF编码", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"ODF编码", "ODF编号", "ODF_ID", "光配线架编码"}},
		{Name: "odf_name", Label: "ODF名称", Required: true, Kind: KindString,
			Synonyms: []string{"ODF名称", "光配线架名称", "名称"}},
		{Name: "machine_room_id", Label: "所属机房", Kind: KindString,
			Synonyms: []string{"所属机房", "机房ID", "机房编号", "所在机房"}},
		{Name: "capacity", Label: "容量", Kind: KindInt, Synonyms: []string{"容量", "芯容量", "端口容量"}},
	},
}

// odfTerminalSpec is the ODF端子 schema (L2): COMPOSITE key
// odf_code|terminal_no (§3.2). Both components KindString — terminal
// numbers carry significant leading zeros in real exports. fiber_code
// (对应纤芯) is a plain string: the fiber's own key is composite and the
// sheet vocabulary is free text (no W21 ref contract).
var odfTerminalSpec = TypeSpec{
	Type:         domain.ResourceTypeODFTerminal,
	Label:        "ODF端子",
	Layer:        LayerOptical,
	Domain:       DomainOptical,
	SheetHint:    "端子",
	KeyNormalize: true,
	Fields: []FieldSpec{
		{Name: "odf_code", Label: "ODF编码", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"ODF编码", "ODF编号", "所属ODF"}},
		{Name: "terminal_no", Label: "端子号", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"端子号", "端子编号", "端口号"}},
		{Name: "fiber_code", Label: "对应纤芯", Kind: KindString,
			Synonyms: []string{"对应纤芯", "对应纤芯编号", "纤芯"}},
	},
}

// spliceBoxSpec is the 接头盒 schema (L2): splice_box_code keyed;
// capacity/used_cores counts; geo triple for the field position.
var spliceBoxSpec = TypeSpec{
	Type:         domain.ResourceTypeSpliceBox,
	Label:        "接头盒",
	Layer:        LayerOptical,
	Domain:       DomainOptical,
	SheetHint:    "接头盒",
	KeyNormalize: true,
	Fields: []FieldSpec{
		{Name: "splice_box_code", Label: "接头盒编码", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"接头盒编码", "接头盒编号", "接头盒ID"}},
		{Name: "capacity", Label: "容量", Kind: KindInt, Synonyms: []string{"容量", "芯容量"}},
		{Name: "used_cores", Label: "已用芯数", Kind: KindInt, Synonyms: []string{"已用芯数", "已用纤芯数"}},
		{Name: "geo_lat", Label: "纬度", Kind: KindLat, Synonyms: []string{"纬度", "lat", "GPS纬度"}},
		{Name: "geo_lng", Label: "经度", Kind: KindLng, Synonyms: []string{"经度", "lng", "GPS经度"}},
		{Name: "geo", Label: "经纬度", Kind: KindGeo, Synonyms: []string{"经纬度", "坐标", "GPS坐标"}},
	},
}

// otnOchSpec is the OTN波道 schema (L3): och_code keyed. route_cable /
// use_fiber are the OPTIONAL routed_over/uses_fiber refs (波道 → 光缆 /
// 纤芯; use_fiber resolves by the fiber composite key
// "cable_code|fiber_no"). 波长/速率 KindString: real values carry units
// ("1550.12nm", "100G") an int kind would E004.
var otnOchSpec = TypeSpec{
	Type:         domain.ResourceTypeOTNOch,
	Label:        "OTN波道",
	Layer:        LayerTransport,
	Domain:       DomainTransport,
	SheetHint:    "波道",
	KeyNormalize: true,
	Fields: []FieldSpec{
		{Name: "och_code", Label: "波道编码", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"波道编码", "波道编号", "波道ID", "OTN波道编号"}},
		{Name: "wave_no", Label: "波道号", Kind: KindInt, Synonyms: []string{"波道号", "波道序号"}},
		{Name: "wavelength", Label: "波长", Kind: KindString, Synonyms: []string{"波长", "波长(nm)"}},
		{Name: "rate", Label: "速率", Kind: KindString, Synonyms: []string{"速率", "波道速率", "速率等级"}},
		{Name: "route_cable", Label: "路由光缆", Kind: KindRef, RefType: domain.ResourceTypeOpticalCable,
			Synonyms: []string{"路由光缆", "承载光缆", "经过光缆", "所经光缆"}},
		{Name: "use_fiber", Label: "占用纤芯", Kind: KindRef, RefType: domain.ResourceTypeFiber,
			Synonyms: []string{"占用纤芯", "使用纤芯", "所用纤芯"}},
	},
}

// sdhTimeslotSpec is the SDH时隙 schema (L3): timeslot_code keyed.
// vc_level KindString (VC4/VC12 are alphanumeric); timeslot_no int.
var sdhTimeslotSpec = TypeSpec{
	Type:         domain.ResourceTypeSDHTimeslot,
	Label:        "SDH时隙",
	Layer:        LayerTransport,
	Domain:       DomainTransport,
	SheetHint:    "时隙",
	KeyNormalize: true,
	Fields: []FieldSpec{
		{Name: "timeslot_code", Label: "时隙编码", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"时隙编码", "时隙编号", "时隙ID", "SDH时隙编号"}},
		{Name: "vc_level", Label: "VC级别", Kind: KindString, Synonyms: []string{"VC级别", "VC等级", "VC类型"}},
		{Name: "timeslot_no", Label: "时隙号", Kind: KindInt, Synonyms: []string{"时隙号", "时隙序号"}},
		{Name: "capacity_mbps", Label: "容量", Kind: KindInt, Synonyms: []string{"容量", "容量(Mbps)", "带宽"}},
	},
}

// protectionGroupSpec is the 保护组 schema (L5 逻辑层 per §10.2's layer
// table): pg_code keyed; protected_circuit the REQUIRED protects ref
// (protection_group → circuit). protect_type KindString ("1+1", "1:1",
// "环网" — arithmetic-looking tokens must not hit an int parse).
var protectionGroupSpec = TypeSpec{
	Type:         domain.ResourceTypeProtectionGroup,
	Label:        "保护组",
	Layer:        LayerLogic,
	Domain:       DomainLogic,
	SheetHint:    "保护",
	KeyNormalize: true,
	Fields: []FieldSpec{
		{Name: "pg_code", Label: "保护组编码", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"保护组编码", "保护组编号", "保护组ID"}},
		{Name: "protect_type", Label: "保护类型", Kind: KindString, Synonyms: []string{"保护类型", "保护方式"}},
		{Name: "protected_circuit", Label: "保护电路", Required: true, Kind: KindRef, RefType: domain.ResourceTypeCircuit,
			Synonyms: []string{"保护电路", "被保护电路", "保护电路编号"}},
		{Name: "work_path", Label: "工作路径", Kind: KindString, Synonyms: []string{"工作路径", "工作路由"}},
		{Name: "protect_path", Label: "保护路径", Kind: KindString, Synonyms: []string{"保护路径", "保护路由"}},
	},
}

// ringSpec is the 环网 schema (L5): ring_code keyed. 环/链 stays an
// ATTRIBUTE (B4: 环/链 not promoted to graph structure in W21 — devices
// reach rings via device.ring_code refs, member_of_ring).
var ringSpec = TypeSpec{
	Type:         domain.ResourceTypeRing,
	Label:        "环网",
	Layer:        LayerLogic,
	Domain:       DomainLogic,
	SheetHint:    "环网",
	KeyNormalize: true,
	Fields: []FieldSpec{
		{Name: "ring_code", Label: "环编码", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"环编码", "环编号", "环ID"}},
		{Name: "ring_name", Label: "环名称", Required: true, Kind: KindString,
			Synonyms: []string{"环名称", "环名", "名称"}},
		{Name: "ring_type", Label: "环类型", Kind: KindString, Synonyms: []string{"环类型", "环路类型"}},
		{Name: "node_count", Label: "节点数", Kind: KindInt, Synonyms: []string{"节点数", "环节点数"}},
	},
}

// igpDomainSpec is the IGP域 schema (L5): domain_code keyed. area_no is
// KindString on purpose: OSPF area IDs may be dotted decimals ("0.0.0.1").
var igpDomainSpec = TypeSpec{
	Type:         domain.ResourceTypeIGPDomain,
	Label:        "IGP域",
	Layer:        LayerLogic,
	Domain:       DomainLogic,
	SheetHint:    "IGP",
	KeyNormalize: true,
	Fields: []FieldSpec{
		{Name: "domain_code", Label: "域编码", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"域编码", "IGP域编码", "域编号", "IGP域编号"}},
		{Name: "domain_name", Label: "域名称", Required: true, Kind: KindString,
			Synonyms: []string{"域名称", "IGP域名称", "名称"}},
		{Name: "process_no", Label: "进程号", Kind: KindInt, Synonyms: []string{"进程号", "进程编号"}},
		{Name: "area_no", Label: "区域号", Kind: KindString, Synonyms: []string{"区域号", "Area", "区域ID"}},
		{Name: "protocol", Label: "协议", Kind: KindString, Synonyms: []string{"协议", "路由协议"}},
	},
}

// vpnInstanceSpec is the VPN实例 schema (L5): vpn_code keyed; RD/RT plain
// strings (import/export targets are structured values, not int/enum);
// customer a plain string (no W21 ref contract — service.subscribed_by is
// the graph edge for customer linkage).
var vpnInstanceSpec = TypeSpec{
	Type:         domain.ResourceTypeVPNInstance,
	Label:        "VPN实例",
	Layer:        LayerLogic,
	Domain:       DomainLogic,
	SheetHint:    "VPN",
	KeyNormalize: true,
	Fields: []FieldSpec{
		{Name: "vpn_code", Label: "VPN编码", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"VPN编码", "VPN编号", "VPN实例编号", "VPN实例ID"}},
		{Name: "vpn_name", Label: "VPN名称", Required: true, Kind: KindString,
			Synonyms: []string{"VPN名称", "VPN实例名称", "名称"}},
		{Name: "rd", Label: "RD", Kind: KindString, Synonyms: []string{"RD", "RD值", "路由标识"}},
		{Name: "rt", Label: "RT", Kind: KindString, Synonyms: []string{"RT", "RT值", "路由目标"}},
		{Name: "customer", Label: "客户", Kind: KindString, Synonyms: []string{"客户", "客户名称", "所属客户"}},
	},
}

// customerSpec is the 客户 schema (L6): customer_code keyed — the natural
// key the serves/subscribed_by refs resolve against.
var customerSpec = TypeSpec{
	Type:         domain.ResourceTypeCustomer,
	Label:        "客户",
	Layer:        LayerBusiness,
	Domain:       DomainService,
	SheetHint:    "客户",
	KeyNormalize: true,
	Fields: []FieldSpec{
		{Name: "customer_code", Label: "客户编码", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"客户编码", "客户编号", "客户ID", "客户标识"}},
		{Name: "customer_name", Label: "客户名称", Required: true, Kind: KindString,
			Synonyms: []string{"客户名称", "客户名", "名称"}},
		{Name: "customer_type", Label: "客户类型", Kind: KindString, Synonyms: []string{"客户类型", "客户类别"}},
		{Name: "contact", Label: "联系人", Kind: KindString, Synonyms: []string{"联系人", "联系方式"}},
	},
}

// serviceSpec is the 业务实例 schema (L6): service_code keyed;
// customer_code the REQUIRED subscribed_by ref, provide_circuit the
// REQUIRED provided_by ref, depend_device/depend_circuit the OPTIONAL
// depends_on refs (multi-target §4.2 relation).
var serviceSpec = TypeSpec{
	Type:         domain.ResourceTypeService,
	Label:        "业务实例",
	Layer:        LayerBusiness,
	Domain:       DomainService,
	SheetHint:    "业务",
	KeyNormalize: true,
	Fields: []FieldSpec{
		{Name: "service_code", Label: "业务编码", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"业务编码", "业务编号", "业务ID", "业务标识"}},
		{Name: "service_type", Label: "业务类型", Kind: KindString, Synonyms: []string{"业务类型", "业务类别"}},
		{Name: "customer_code", Label: "客户编码", Required: true, Kind: KindRef, RefType: domain.ResourceTypeCustomer,
			Synonyms: []string{"客户编码", "客户编号", "所属客户", "客户"}},
		{Name: "provide_circuit", Label: "提供电路", Required: true, Kind: KindRef, RefType: domain.ResourceTypeCircuit,
			Synonyms: []string{"提供电路", "承载电路", "业务电路", "电路编号"}},
		{Name: "depend_device", Label: "依赖设备", Kind: KindRef, RefType: domain.ResourceTypeDevice,
			Synonyms: []string{"依赖设备", "关联设备", "业务设备"}},
		{Name: "depend_circuit", Label: "依赖电路", Kind: KindRef, RefType: domain.ResourceTypeCircuit,
			Synonyms: []string{"依赖电路", "关联电路", "业务依赖电路"}},
		{Name: "bandwidth_mbps", Label: "带宽Mbps", Kind: KindInt, Synonyms: []string{"带宽Mbps", "带宽(Mbps)", "带宽"}},
		{Name: "status", Label: "状态", Kind: KindEnum, Enum: StatusEnum,
			Synonyms: []string{"状态", "业务状态", "运行状态"}},
	},
}

// leasedLineSpec is the 专线 schema (L6): line_code keyed. Field set from
// the real 23-column 专线 export (§3.2's note): 专线编号/集团STN编号/
// 装机地址/协议带宽. Address fields stay plain strings (locations, not
// site refs — no W21 ref contract).
var leasedLineSpec = TypeSpec{
	Type:         domain.ResourceTypeLeasedLine,
	Label:        "专线",
	Layer:        LayerBusiness,
	Domain:       DomainService,
	SheetHint:    "专线",
	KeyNormalize: true,
	Fields: []FieldSpec{
		{Name: "line_code", Label: "专线编码", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"专线编码", "专线编号", "专线ID", "互联网专线编号"}},
		{Name: "line_name", Label: "专线名称", Kind: KindString, Synonyms: []string{"专线名称", "专线名", "名称"}},
		{Name: "stn_no", Label: "集团STN编号", Kind: KindString, Synonyms: []string{"集团STN编号", "STN编号", "STN"}},
		{Name: "a_end_address", Label: "A端装机地址", Kind: KindString, Synonyms: []string{"A端装机地址", "A端地址"}},
		{Name: "z_end_address", Label: "Z端装机地址", Kind: KindString, Synonyms: []string{"Z端装机地址", "Z端地址"}},
		{Name: "bandwidth_mbps", Label: "协议带宽", Kind: KindInt, Synonyms: []string{"协议带宽", "带宽", "带宽(Mbps)"}},
	},
}

// bbuSpec is the BBU schema (L4): bbu_code keyed; base_station_code a
// plain string (所属基站 vocabulary, no W21 ref contract — rru reaches
// bbu via home_bbu, the station linkage stays textual here).
var bbuSpec = TypeSpec{
	Type:         domain.ResourceTypeBBU,
	Label:        "BBU",
	Layer:        LayerAccess,
	Domain:       DomainAccess,
	SheetHint:    "BBU",
	KeyNormalize: true,
	Fields: []FieldSpec{
		{Name: "bbu_code", Label: "BBU编码", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"BBU编码", "BBU编号", "BBU_ID", "BBU标识"}},
		{Name: "base_station_code", Label: "所属基站", Kind: KindString,
			Synonyms: []string{"所属基站", "基站编码", "基站编号"}},
		{Name: "model", Label: "设备型号", Kind: KindString, Synonyms: []string{"设备型号", "型号"}},
	},
}

// rruSpec is the RRU schema (L4): rru_code keyed; home_bbu the REQUIRED
// home_bbu ref (rru → bbu); cover_station the OPTIONAL covers ref (rru →
// base_station, resolves by the station's FINAL natural key — the
// conditional-key semantics make code and 地市|名称 fallback keys both
// valid targets).
var rruSpec = TypeSpec{
	Type:         domain.ResourceTypeRRU,
	Label:        "RRU",
	Layer:        LayerAccess,
	Domain:       DomainAccess,
	SheetHint:    "RRU",
	KeyNormalize: true,
	Fields: []FieldSpec{
		{Name: "rru_code", Label: "RRU编码", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"RRU编码", "RRU编号", "RRU_ID", "RRU标识"}},
		{Name: "home_bbu", Label: "归属BBU", Required: true, Kind: KindRef, RefType: domain.ResourceTypeBBU,
			Synonyms: []string{"归属BBU", "所属BBU", "BBU编码", "BBU编号"}},
		{Name: "cover_station", Label: "覆盖基站", Kind: KindRef, RefType: domain.ResourceTypeBaseStation,
			Synonyms: []string{"覆盖基站", "覆盖小区", "所属基站", "基站编码"}},
	},
}

// oltSpec is the OLT schema (L4): olt_code keyed; machine_room_id a plain
// string (no W21 ref contract); pon_port_count the PON口数 count.
var oltSpec = TypeSpec{
	Type:         domain.ResourceTypeOLT,
	Label:        "OLT",
	Layer:        LayerAccess,
	Domain:       DomainAccess,
	SheetHint:    "OLT",
	KeyNormalize: true,
	Fields: []FieldSpec{
		{Name: "olt_code", Label: "OLT编码", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"OLT编码", "OLT编号", "OLT_ID", "OLT标识"}},
		{Name: "machine_room_id", Label: "所属机房", Kind: KindString,
			Synonyms: []string{"所属机房", "机房ID", "机房编号", "所在机房"}},
		{Name: "model", Label: "型号", Kind: KindString, Synonyms: []string{"型号", "设备型号"}},
		{Name: "pon_port_count", Label: "PON口数", Kind: KindInt, Synonyms: []string{"PON口数", "PON端口数"}},
	},
}

// onuSpec is the ONU schema (L4): onu_code keyed; olt_code a plain string
// (所属OLT vocabulary); connect_port the REQUIRED connects_onu ref (onu →
// pon_port, resolves by the composite key "olt_code|port_no").
var onuSpec = TypeSpec{
	Type:         domain.ResourceTypeONU,
	Label:        "ONU",
	Layer:        LayerAccess,
	Domain:       DomainAccess,
	SheetHint:    "ONU",
	KeyNormalize: true,
	Fields: []FieldSpec{
		{Name: "onu_code", Label: "ONU编码", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"ONU编码", "ONU编号", "ONU_ID", "ONU标识"}},
		{Name: "olt_code", Label: "所属OLT", Kind: KindString,
			Synonyms: []string{"所属OLT", "OLT编码", "OLT编号"}},
		{Name: "connect_port", Label: "接入PON口", Required: true, Kind: KindRef, RefType: domain.ResourceTypePONPort,
			Synonyms: []string{"接入PON口", "PON口", "所属PON口", "PON端口"}},
		{Name: "user", Label: "用户", Kind: KindString, Synonyms: []string{"用户", "用户名称", "用户标识"}},
	},
}

// ponPortSpec is the PON口 schema (L4): COMPOSITE key olt_code|port_no
// (§3.2); both components KindString (zero-preserving identity). port_no
// deliberately NOT an int kind: the B8 anti-collapse rule (1 ≠ 01) binds
// here too. split_ratio KindString ("1:64").
var ponPortSpec = TypeSpec{
	Type:         domain.ResourceTypePONPort,
	Label:        "PON口",
	Layer:        LayerAccess,
	Domain:       DomainAccess,
	SheetHint:    "PON",
	KeyNormalize: true,
	Fields: []FieldSpec{
		{Name: "olt_code", Label: "OLT编码", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"OLT编码", "OLT编号", "所属OLT"}},
		{Name: "port_no", Label: "端口号", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"端口号", "PON端口号", "PON口号"}},
		{Name: "split_ratio", Label: "分光比", Kind: KindString, Synonyms: []string{"分光比", "分光比例"}},
	},
}

// w21Specs lists the 25 new specs in §3 catalog order (appended after the
// 8 pre-W21 specs in the Specs registry).
var w21Specs = []TypeSpec{
	rackSpec, ductSpec, manholeSpec, poleSpec,
	boardSpec,
	// ORDER MATTERS for SuggestType (first substring match wins): the
	// specific hint must precede its generic prefix — cable_segment
	// ("光缆段") before optical_cable ("光缆"), odf_terminal ("端子")
	// before odf ("ODF") — otherwise a "光缆段资源表" sheet would suggest
	// optical_cable. TestSheetNameSuggestTypeRoundTrip pins this.
	cableSegmentSpec, opticalCableSpec, fiberSpec, odfTerminalSpec, odfSpec, spliceBoxSpec,
	otnOchSpec, sdhTimeslotSpec, protectionGroupSpec,
	ringSpec, igpDomainSpec, vpnInstanceSpec,
	customerSpec, serviceSpec, leasedLineSpec,
	bbuSpec, rruSpec, oltSpec, onuSpec, ponPortSpec,
}

// NormalizeNaturalKeyComponent applies the §12-B8 code-key normalization
// to ONE natural-key field value: trim, fullwidth→halfwidth (Ｕ+FF01..FF5E
// plus the ideographic space U+3000), then upper-case. Leading zeros are
// preserved — nothing pads or strips digits, so "01" stays "01" (the
// anti-collapse rule: 1 and 01 are two distinct identities, never one).
// Chinese characters pass through unchanged (ToUpper is a no-op for
// them); the halfwidth fold also maps the fullwidth '｜'/'：' separators
// real exports carry onto the ASCII '|'/':' of joined composite keys.
func NormalizeNaturalKeyComponent(s string) string {
	return strings.ToUpper(foldFullwidth(strings.TrimSpace(s)))
}

// foldFullwidth maps fullwidth ASCII-range runes to their halfwidth
// counterparts (U+FF01 '！' … U+FF5E '～' → '!'…'~') and the ideographic
// space U+3000 to a plain space. Other runes (incl. CJK) are untouched.
func foldFullwidth(s string) string {
	changed := false
	for _, r := range s {
		if r == '　' || (r >= '！' && r <= '～') {
			changed = true
			break
		}
	}
	if !changed {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '　':
			b.WriteByte(' ')
		case r >= '！' && r <= '～':
			b.WriteRune(r - '！' + '!')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
