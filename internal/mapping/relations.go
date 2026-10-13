package mapping

import (
	"fmt"

	"github.com/mmqz/netops-lab/internal/domain"
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
// FromTypes lists every resource type allowed on the from side, ToType the
// single allowed to-side type. The importer's derived edges (RelationTypeFor)
// stay within this contract; TestRelationRegistryConsistency pins it.
type RelationSpec struct {
	// Type is the relation type (assigned_to, a_end, …).
	Type domain.RelationType
	// Label is the human-readable (Chinese) name.
	Label string
	// FromTypes lists the valid from-side resource types (upper layer /
	// dependent). Non-empty.
	FromTypes []domain.ResourceType
	// ToType is the valid to-side resource type (lower layer / bearer).
	ToType domain.ResourceType
	// CrossLayer reports whether the two ends sit in different layers
	// (§10.2: 同层边 vs 跨层边). Computed FROM the layer registry and
	// pinned by test — do not hand-edit the semantics.
	CrossLayer bool
	// Note is the one-line direction semantics (Chinese), same wording
	// as the §10.2 table so the doc and code can be diffed by eye.
	Note string
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
		ToType:     domain.ResourceTypeDevice,
		CrossLayer: true, // L5 逻辑 → L1 设备
		Note:       "from=地址池（逻辑资源），to=设备；上行走影响面",
	},
	{
		Type:       domain.RelationTypeAEnd,
		Label:      "电路A端设备",
		FromTypes:  []domain.ResourceType{domain.ResourceTypeCircuit},
		ToType:     domain.ResourceTypeDevice,
		CrossLayer: true, // L3 传输 → L1 设备
		Note:       "from=电路，to=设备；电路端接设备（A端）",
	},
	{
		Type:       domain.RelationTypeZEnd,
		Label:      "电路Z端设备",
		FromTypes:  []domain.ResourceType{domain.ResourceTypeCircuit},
		ToType:     domain.ResourceTypeDevice,
		CrossLayer: true, // L3 传输 → L1 设备
		Note:       "from=电路，to=设备；电路端接设备（Z端）",
	},
	{
		Type:       domain.RelationTypeBelongsTo,
		Label:      "机房属局站",
		FromTypes:  []domain.ResourceType{domain.ResourceTypeMachineRoom},
		ToType:     domain.ResourceTypeSite,
		CrossLayer: false, // L0 空间 → L0 空间（容器归属，同层）
		Note:       "from=机房，to=局站；层级容器归属（v10 落地语义）",
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
