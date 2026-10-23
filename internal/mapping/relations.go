package mapping

import (
	"fmt"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// Layer constants for the seven-layer model of docs/design/data-model-v1.md
// §10.2 (as amended by §12 disposition B5: the access layer is inserted as
// L4, shifting logic→L5 and business→L6, and ip_pool is assigned to the
// logic layer). Layers make "same layer / cross layer" programmable — the
// traverse endpoint (§10.3 `layers` parameter) and the root-cause layer
// logic (§10.5) read them from here, never from a hand-copied table.
const (
	// LayerSpace is L0: 局站/机房/机架/管道/人井/杆路.
	LayerSpace = 0
	// LayerDevice is L1: 网元设备/板卡/端口/PTP端口.
	LayerDevice = 1
	// LayerOptical is L2: 光缆/光缆段/纤芯/ODF/ODF端子/接头盒.
	LayerOptical = 2
	// LayerTransport is L3: 电路/OTN波道/SDH时隙.
	LayerTransport = 3
	// LayerAccess is L4: 基站/BBU/RRU/OLT/ONU/PON口 (§12-B5 amendment).
	LayerAccess = 4
	// LayerLogic is L5: 环网/IGP域/VPN实例/保护组/IP地址池.
	LayerLogic = 5
	// LayerBusiness is L6: 客户/业务实例/专线.
	LayerBusiness = 6
)

// LayerName returns the canonical (Chinese) layer name. Unknown layers
// return the numeric form so the error is visible instead of silently
// empty.
func LayerName(layer int) string {
	switch layer {
	case LayerSpace:
		return "空间层"
	case LayerDevice:
		return "物理层（设备）"
	case LayerOptical:
		return "物理层（光缆）"
	case LayerTransport:
		return "传输层"
	case LayerAccess:
		return "接入层"
	case LayerLogic:
		return "逻辑层"
	case LayerBusiness:
		return "业务层"
	default:
		return fmt.Sprintf("L%d", layer)
	}
}

// Domain constants: the professional-domain projection axis (§2.1 "专业域
// 投影" — 只看传输/无线/IP). One value per professional domain; the web
// filter chips and /api/schema consumers read them from the registry.
const (
	DomainSpace     = "space"     // 空间/机房
	DomainDevice    = "device"    // 网元设备
	DomainOptical   = "optical"   // 光缆网
	DomainTransport = "transport" // 传输
	DomainAccess    = "access"    // 无线/PON 接入
	DomainLogic     = "logic"     // 逻辑/地址
	DomainService   = "service"   // 客户业务
)

// RelationSpec is the registry entry of one relation type: the direction
// semantics of §10.2 (the "key deliverable" table — every relation MUST be
// registered here, unregistered relations are unusable by traversal).
//
// Direction rule (§10.2): from is the 依赖方/上层/被承载方, to is the
// 被依赖方/下层/承载方. Walking from→to = down (root-cause direction);
// walking to→from = up (impact/blame direction).
//
// FromTypes lists every resource type allowed on the from side, ToTypes
// every allowed to-side type (W21 extended single → slice: carried_by ends
// at otn_och OR sdh_timeslot, depends_on at device OR circuit, serves
// starts from device OR base_station — the three multi-end relations of
// §4.2). The importer's derived edges (RelationTypeFor) stay within this
// contract; TestRelationRegistryConsistency pins it.
type RelationSpec struct {
	// Type is the relation type (assigned_to, a_end, …).
	Type domain.RelationType
	// Label is the human-readable (Chinese) name.
	Label string
	// FromTypes lists the valid from-side resource types (upper layer /
	// dependent). Non-empty.
	FromTypes []domain.ResourceType
	// ToTypes lists the valid to-side resource types (lower layer /
	// bearer). Non-empty (the W19 single ToType became a slice in W21).
	ToTypes []domain.ResourceType
	// CrossLayer reports whether the two ends sit in different layers
	// (§10.2: 同层边 vs 跨层边). Computed FROM the layer registry and
	// pinned by test — do not hand-edit the semantics. With multi-end
	// relations the flag is the UNANIMOUS verdict over every
	// from×to layer pair (see TestRelationRegistryConsistency).
	CrossLayer bool
	// Note is the one-line direction semantics (Chinese), same wording
	// as the §10.2 table so the doc and code can be diffed by eye.
	Note string
}

// ToType returns the single to-side type when the relation has exactly one
// (the common case), mirroring the pre-W21 single-valued field. Callers
// that must handle multi-end relations read ToTypes directly.
func (s RelationSpec) ToType() domain.ResourceType {
	if len(s.ToTypes) != 1 {
		return ""
	}
	return s.ToTypes[0]
}

// AllowsFrom reports whether rt may appear on the from side of s.
func (s RelationSpec) AllowsFrom(rt domain.ResourceType) bool {
	for _, t := range s.FromTypes {
		if t == rt {
			return true
		}
	}
	return false
}

// AllowsTo reports whether rt may appear on the to side of s.
func (s RelationSpec) AllowsTo(rt domain.ResourceType) bool {
	for _, t := range s.ToTypes {
		if t == rt {
			return true
		}
	}
	return false
}

// relationSpecs is the ordered relation registry. §12 disposition A1
// (issue #17): belongs_to 与 housed_in 不合并 — belongs_to 保持 v10 语义
// (容器归属, 同层), housed_in (W21) 表达物理位置. Merging into a single
// located_in was rejected: it would rewrite v10 rows (breaking change, cf.
// §10.10) and touch 6 packages / 8 test files for zero query gain.
var relationSpecs = []RelationSpec{
	{
		Type:       domain.RelationTypeAssignedTo,
		Label:      "地址池分配设备",
		FromTypes:  []domain.ResourceType{domain.ResourceTypeIPPool},
		ToTypes:    []domain.ResourceType{domain.ResourceTypeDevice},
		CrossLayer: true, // L5 逻辑 → L1 设备
		Note:       "from=地址池（逻辑资源），to=设备；上行走影响面",
	},
	{
		Type:       domain.RelationTypeAEnd,
		Label:      "电路A端设备",
		FromTypes:  []domain.ResourceType{domain.ResourceTypeCircuit},
		ToTypes:    []domain.ResourceType{domain.ResourceTypeDevice},
		CrossLayer: true, // L3 传输 → L1 设备
		Note:       "from=电路，to=设备；电路端接设备（A端）",
	},
	{
		Type:       domain.RelationTypeZEnd,
		Label:      "电路Z端设备",
		FromTypes:  []domain.ResourceType{domain.ResourceTypeCircuit},
		ToTypes:    []domain.ResourceType{domain.ResourceTypeDevice},
		CrossLayer: true, // L3 传输 → L1 设备
		Note:       "from=电路，to=设备；电路端接设备（Z端）",
	},
	{
		Type:       domain.RelationTypeBelongsTo,
		Label:      "机房属局站",
		FromTypes:  []domain.ResourceType{domain.ResourceTypeMachineRoom},
		ToTypes:    []domain.ResourceType{domain.ResourceTypeSite},
		CrossLayer: false, // L0 空间 → L0 空间（容器归属，同层）
		Note:       "from=机房，to=局站；层级容器归属（v10 落地语义）",
	},

	// ---- W21 扩容：25 条新关系（data-model v1.0 §4.2；注册序 = §4.3 CHECK 序）----
	// CrossLayer 全部由层次注册表计算（TestRelationRegistryConsistency 钉死），
	// 注释给出层差依据。§10.2 方向表两处层差标注（carried_by/fiber_terminates
	// 标「跨层」）与层次表（两端同属 L3/L2）不一致——以层次表计算结果为准，
	// 勘误记入 §12「W21 落地勘误与处置」。

	// 空间归属（§4.2 空间归属组）.
	{
		Type:       domain.RelationTypeLocatedIn,
		Label:      "机架在机房",
		FromTypes:  []domain.ResourceType{domain.ResourceTypeRack},
		ToTypes:    []domain.ResourceType{domain.ResourceTypeMachineRoom},
		CrossLayer: false, // L0 空间 → L0 空间（容器归属，同层）
		Note:       "from=机架，to=机房；机架在机房（容器归属）",
	},
	{
		Type:       domain.RelationTypeHousedIn,
		Label:      "设备在机房",
		FromTypes:  []domain.ResourceType{domain.ResourceTypeDevice},
		ToTypes:    []domain.ResourceType{domain.ResourceTypeMachineRoom},
		CrossLayer: true, // L1 设备 → L0 空间
		Note:       "from=设备，to=机房；设备物理位置（与 belongs_to 容器归属分立，ADR-0014）",
	},
	{
		Type:       domain.RelationTypeInstalledIn,
		Label:      "板卡在设备",
		FromTypes:  []domain.ResourceType{domain.ResourceTypeBoard},
		ToTypes:    []domain.ResourceType{domain.ResourceTypeDevice},
		CrossLayer: false, // L1 设备 → L1 设备（成员→容器，同层）
		Note:       "from=板卡，to=设备；板卡插在设备（成员→容器）",
	},
	{
		Type:       domain.RelationTypeMountedOn,
		Label:      "端口在板卡",
		FromTypes:  []domain.ResourceType{domain.ResourceTypePort},
		ToTypes:    []domain.ResourceType{domain.ResourceTypeBoard},
		CrossLayer: false, // L1 设备 → L1 设备（成员→容器，同层）
		Note:       "from=端口，to=板卡；端口在板卡上（成员→容器）",
	},
	// 物理连接（§4.2 物理连接组）.
	{
		Type:       domain.RelationTypeCableAEnd,
		Label:      "光缆A端局站",
		FromTypes:  []domain.ResourceType{domain.ResourceTypeOpticalCable},
		ToTypes:    []domain.ResourceType{domain.ResourceTypeSite},
		CrossLayer: true, // L2 光缆 → L0 空间
		Note:       "from=光缆，to=局站；光缆A端落局站",
	},
	{
		Type:       domain.RelationTypeCableZEnd,
		Label:      "光缆Z端局站",
		FromTypes:  []domain.ResourceType{domain.ResourceTypeOpticalCable},
		ToTypes:    []domain.ResourceType{domain.ResourceTypeSite},
		CrossLayer: true, // L2 光缆 → L0 空间
		Note:       "from=光缆，to=局站；光缆Z端落局站",
	},
	{
		Type:       domain.RelationTypeFiberOf,
		Label:      "纤芯属光缆",
		FromTypes:  []domain.ResourceType{domain.ResourceTypeFiber},
		ToTypes:    []domain.ResourceType{domain.ResourceTypeOpticalCable},
		CrossLayer: false, // L2 光缆 → L2 光缆（成员→容器，同层）
		Note:       "from=纤芯，to=光缆；纤芯属光缆（§4.2 carries_fiber 的 W21 方向勘误：成员→容器）",
	},
	{
		Type:       domain.RelationTypeFiberTerminates,
		Label:      "纤芯成端于ODF端子",
		FromTypes:  []domain.ResourceType{domain.ResourceTypeFiber},
		ToTypes:    []domain.ResourceType{domain.ResourceTypeODFTerminal},
		CrossLayer: false, // L2 光缆 → L2 光缆（层次表计算为同层；§10.2 表标跨层系勘误）
		Note:       "from=纤芯，to=ODF端子；纤芯成端于ODF端子",
	},
	{
		Type:       domain.RelationTypeSegmentOf,
		Label:      "光缆段属光缆",
		FromTypes:  []domain.ResourceType{domain.ResourceTypeCableSegment},
		ToTypes:    []domain.ResourceType{domain.ResourceTypeOpticalCable},
		CrossLayer: false, // L2 光缆 → L2 光缆（成员→容器，同层）
		Note:       "from=光缆段，to=光缆；光缆段属光缆（成员→容器）",
	},
	{
		Type:       domain.RelationTypeConnectsDevice,
		Label:      "光缆连接设备",
		FromTypes:  []domain.ResourceType{domain.ResourceTypeOpticalCable},
		ToTypes:    []domain.ResourceType{domain.ResourceTypeDevice},
		CrossLayer: true, // L2 光缆 → L1 设备（两套物理资源之间的跳转）
		Note:       "from=光缆，to=设备；光缆连接设备（配线光缆）",
	},
	// 传输承载（§4.2 传输承载组——层次穿透的关键）.
	{
		Type:       domain.RelationTypeCarriedBy,
		Label:      "电路由波道/时隙承载",
		FromTypes:  []domain.ResourceType{domain.ResourceTypeCircuit},
		ToTypes:    []domain.ResourceType{domain.ResourceTypeOTNOch, domain.ResourceTypeSDHTimeslot},
		CrossLayer: false, // L3 传输 → L3 传输（层次表计算为同层；§10.2 表标跨层系勘误）
		Note:       "from=电路，to=波道/时隙；电路由波道或时隙承载（多目标端）",
	},
	{
		Type:       domain.RelationTypeRoutedOver,
		Label:      "波道路由经过光缆",
		FromTypes:  []domain.ResourceType{domain.ResourceTypeOTNOch},
		ToTypes:    []domain.ResourceType{domain.ResourceTypeOpticalCable},
		CrossLayer: true, // L3 传输 → L2 光缆
		Note:       "from=波道，to=光缆；波道路由经过光缆",
	},
	{
		Type:       domain.RelationTypeUsesFiber,
		Label:      "波道占用纤芯",
		FromTypes:  []domain.ResourceType{domain.ResourceTypeOTNOch},
		ToTypes:    []domain.ResourceType{domain.ResourceTypeFiber},
		CrossLayer: true, // L3 传输 → L2 光缆
		Note:       "from=波道，to=纤芯；波道占用纤芯",
	},
	// 逻辑归属（§4.2 逻辑归属组）.
	{
		Type:       domain.RelationTypeMemberOfRing,
		Label:      "设备属某环",
		FromTypes:  []domain.ResourceType{domain.ResourceTypeDevice},
		ToTypes:    []domain.ResourceType{domain.ResourceTypeRing},
		CrossLayer: true, // L1 设备 → L5 逻辑
		Note:       "from=设备，to=环；设备属某环",
	},
	{
		Type:       domain.RelationTypeProtects,
		Label:      "保护组保护电路",
		FromTypes:  []domain.ResourceType{domain.ResourceTypeProtectionGroup},
		ToTypes:    []domain.ResourceType{domain.ResourceTypeCircuit},
		CrossLayer: true, // L5 逻辑 → L3 传输
		Note:       "from=保护组，to=电路；保护组保护电路",
	},
	{
		Type:       domain.RelationTypeInDomain,
		Label:      "设备属IGP域",
		FromTypes:  []domain.ResourceType{domain.ResourceTypeDevice},
		ToTypes:    []domain.ResourceType{domain.ResourceTypeIGPDomain},
		CrossLayer: true, // L1 设备 → L5 逻辑
		Note:       "from=设备，to=IGP域；设备属IGP域",
	},
	{
		Type:       domain.RelationTypeInVPN,
		Label:      "端口属VPN实例",
		FromTypes:  []domain.ResourceType{domain.ResourceTypePort},
		ToTypes:    []domain.ResourceType{domain.ResourceTypeVPNInstance},
		CrossLayer: true, // L1 设备 → L5 逻辑
		Note:       "from=端口，to=VPN实例；端口属VPN实例",
	},
	// 业务服务（§4.2 业务服务组——业务影响分析）.
	{
		Type:       domain.RelationTypeServes,
		Label:      "设备/基站服务客户",
		FromTypes:  []domain.ResourceType{domain.ResourceTypeDevice, domain.ResourceTypeBaseStation},
		ToTypes:    []domain.ResourceType{domain.ResourceTypeCustomer},
		CrossLayer: true, // L1 设备 / L4 接入 → L6 业务（多起点端，层差一律非零）
		Note:       "from=设备/基站，to=客户；设备/基站服务客户（多起点端）",
	},
	{
		Type:  domain.RelationTypeSubscribedBy,
		Label: "业务/专线属客户",
		FromTypes: []domain.ResourceType{
			domain.ResourceTypeService,
			// W25（#16 验收补齐）：专线同样属客户（真实专线表携「客户名称」列），
			// 同一语义同一关系——按客户聚合 service 与 leased_line 都可回答。
			domain.ResourceTypeLeasedLine,
		},
		ToTypes:    []domain.ResourceType{domain.ResourceTypeCustomer},
		CrossLayer: false, // L6 业务/专线 → L6 业务（同层）
		Note:       "from=业务/专线，to=客户；业务/专线属于客户（W25 起含专线）",
	},
	{
		Type:       domain.RelationTypeProvidedBy,
		Label:      "业务由电路提供",
		FromTypes:  []domain.ResourceType{domain.ResourceTypeService},
		ToTypes:    []domain.ResourceType{domain.ResourceTypeCircuit},
		CrossLayer: true, // L6 业务 → L3 传输
		Note:       "from=业务，to=电路；业务由电路提供",
	},
	{
		Type:       domain.RelationTypeDependsOn,
		Label:      "业务依赖资源",
		FromTypes:  []domain.ResourceType{domain.ResourceTypeService},
		ToTypes:    []domain.ResourceType{domain.ResourceTypeDevice, domain.ResourceTypeCircuit},
		CrossLayer: true, // L6 业务 → L1 设备 / L3 传输（多目标端，层差一律非零）
		Note:       "from=业务，to=设备/电路；业务依赖资源（多目标端）",
	},
	// 接入关联（§4.2 接入关联组）.
	{
		Type:       domain.RelationTypeUplinkTo,
		Label:      "基站上联设备",
		FromTypes:  []domain.ResourceType{domain.ResourceTypeBaseStation},
		ToTypes:    []domain.ResourceType{domain.ResourceTypeDevice},
		CrossLayer: true, // L4 接入 → L1 设备
		Note:       "from=基站，to=设备；基站上联设备（替代「归属A」文本匹配，仅新导入显式列）",
	},
	{
		Type:       domain.RelationTypeHomeBBU,
		Label:      "RRU归属BBU",
		FromTypes:  []domain.ResourceType{domain.ResourceTypeRRU},
		ToTypes:    []domain.ResourceType{domain.ResourceTypeBBU},
		CrossLayer: false, // L4 接入 → L4 接入（同层）
		Note:       "from=RRU，to=BBU；RRU归属BBU",
	},
	{
		Type:       domain.RelationTypeCovers,
		Label:      "RRU覆盖基站",
		FromTypes:  []domain.ResourceType{domain.ResourceTypeRRU},
		ToTypes:    []domain.ResourceType{domain.ResourceTypeBaseStation},
		CrossLayer: false, // L4 接入 → L4 接入（同层）
		Note:       "from=RRU，to=基站；RRU覆盖基站",
	},
	{
		Type:       domain.RelationTypeConnectsONU,
		Label:      "ONU接PON口",
		FromTypes:  []domain.ResourceType{domain.ResourceTypeONU},
		ToTypes:    []domain.ResourceType{domain.ResourceTypePONPort},
		CrossLayer: false, // L4 接入 → L4 接入（同层）
		Note:       "from=ONU，to=PON口；ONU接PON口",
	},
}

// RelationSpecs returns the relation registry in canonical order. The
// result is the shared slice — treat it as read-only.
func RelationSpecs() []RelationSpec {
	return relationSpecs
}

// SpecForRelation returns the registry entry of a relation type.
func SpecForRelation(rt domain.RelationType) (RelationSpec, bool) {
	for _, s := range relationSpecs {
		if s.Type == rt {
			return s, true
		}
	}
	return RelationSpec{}, false
}

// LayerOf returns the layer of a resource type from the Specs registry.
// Unknown types return an error (callers must not guess layers — §12-B5:
// the layer table is code, not documentation).
func LayerOf(rt domain.ResourceType) (int, error) {
	for _, spec := range Specs {
		if spec.Type == rt {
			return spec.Layer, nil
		}
	}
	return -1, fmt.Errorf("mapping: no TypeSpec for resource type %q", rt)
}

// MustLayerOf is LayerOf with a panic on unknown type — for use in tests
// and registry self-checks only.
func MustLayerOf(rt domain.ResourceType) int {
	l, err := LayerOf(rt)
	if err != nil {
		panic(err)
	}
	return l
}
