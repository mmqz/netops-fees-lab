// Package mapping defines the target resource schemas of PROJECT_BRIEF §6
// (field names, kinds, required/natural-key flags, enums, synonyms), the
// deterministic column-suggestion engine (§6 synonym ladder: exact 1.0,
// normalized 0.9, synonym 0.75, none 0), and the confirmed-mapping model of
// §8 with validation against real workbook headers.
//
// The package is pure data plus pure functions: it depends only on
// internal/domain and the standard library, so the importer, the HTTP API
// and tests can all share the same schema definitions.
package mapping

import (
	"net/netip"
	"strconv"
	"strings"

	"github.com/mmqz/netops-lab/internal/domain"
)

// FieldKind is the value type of a target field (PROJECT_BRIEF §6).
type FieldKind string

// Field kinds.
const (
	KindString FieldKind = "string" // trimmed text
	KindIPv4   FieldKind = "ipv4"   // dotted quad, each octet 0-255
	KindCIDR   FieldKind = "cidr"   // a.b.c.d/len, len 0-32
	KindInt    FieldKind = "int"    // base-10 integer
	KindEnum   FieldKind = "enum"   // one of a fixed value set
	KindRef    FieldKind = "ref"    // natural key of another resource type
)

// CheckFormat reports whether value is well-formed for kind k. When it is
// not, reason describes the failure (it feeds the E004 issue evidence and
// message). Enum fields are NOT checked here — an out-of-enum status is the
// warning W002, not the error E004 (PROJECT_BRIEF §7); ref fields are
// resolved against the database (E003).
//
// Integer range constraints (vlan_id 1..4094, bandwidth_mbps > 0) are left
// to the rule engine refinement — the parse-level check only rejects
// non-numeric values (E004), which matches the seeded test data.
func (k FieldKind) CheckFormat(value string) (reason string, ok bool) {
	switch k {
	case KindIPv4:
		addr, err := netip.ParseAddr(value)
		if err != nil || !addr.Is4() {
			return "不是合法的 IPv4 点分十进制地址（每段 0-255）", false
		}
	case KindCIDR:
		p, err := netip.ParsePrefix(value)
		if err != nil || !p.Addr().Is4() {
			return "不是合法的 CIDR（应为 a.b.c.d/前缀长度，长度 0-32）", false
		}
	case KindInt:
		if _, err := strconv.Atoi(value); err != nil {
			return "不是十进制整数", false
		}
	case KindString, KindEnum, KindRef:
		// No format constraint beyond trimming.
	}
	return "", true
}

// StatusEnum is the shared status enum of §6 (在网/停用/在建/退网). Matching
// is exact and case-sensitive.
var StatusEnum = []string{"在网", "停用", "在建", "退网"}

// FieldSpec describes one target field of a resource schema (§6).
type FieldSpec struct {
	// Name is the canonical field name (the mapping target key).
	Name string
	// Label is the human-readable (Chinese) label used by the UI.
	Label string
	// Required fields must be mapped and non-empty (E002 otherwise).
	Required bool
	// NaturalKey marks the unique-per-type key field.
	NaturalKey bool
	// Kind is the value type.
	Kind FieldKind
	// RefType is the referenced resource type; set when Kind == KindRef.
	RefType domain.ResourceType
	// Enum holds the allowed values; set when Kind == KindEnum.
	Enum []string
	// Synonyms are the alternative header texts suggesting this field.
	Synonyms []string
}

// EnumContains reports whether v is one of the field's enum values.
func (f FieldSpec) EnumContains(v string) bool {
	for _, e := range f.Enum {
		if v == e {
			return true
		}
	}
	return false
}

// TypeSpec describes one resource type schema (§6).
type TypeSpec struct {
	// Type is the resource type.
	Type domain.ResourceType
	// SheetHint is a sheet-name substring that suggests this type
	// (设备→device, 池→ip_pool [matches 地址池 too], 电路→circuit).
	SheetHint string
	// Fields lists the target fields in canonical order.
	Fields []FieldSpec
}

// Field returns the field spec with the given name.
func (t TypeSpec) Field(name string) (FieldSpec, bool) {
	for _, f := range t.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return FieldSpec{}, false
}

// NaturalKeyField returns the natural key field of the type. Every §6 type
// has exactly one.
func (t TypeSpec) NaturalKeyField() (FieldSpec, bool) {
	for _, f := range t.Fields {
		if f.NaturalKey {
			return f, true
		}
	}
	return FieldSpec{}, false
}

// NaturalKeyNames returns the names of the natural key fields.
func (t TypeSpec) NaturalKeyNames() []string {
	var names []string
	for _, f := range t.Fields {
		if f.NaturalKey {
			names = append(names, f.Name)
		}
	}
	return names
}

// NameField returns the field whose value becomes the resource Name: the
// first field whose name ends in "_name", else the natural key field. For
// the §6 schemas this yields device_name / pool_name / circuit_code.
func (t TypeSpec) NameField() string {
	for _, f := range t.Fields {
		if strings.HasSuffix(f.Name, "_name") {
			return f.Name
		}
	}
	if f, ok := t.NaturalKeyField(); ok {
		return f.Name
	}
	return ""
}

// deviceSpec is the device schema of §6.
var deviceSpec = TypeSpec{
	Type:      domain.ResourceTypeDevice,
	SheetHint: "设备",
	Fields: []FieldSpec{
		{Name: "device_code", Label: "设备编码", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"设备编码", "设备编号", "网元编码", "编码", "code"}},
		{Name: "device_name", Label: "设备名称", Required: true, Kind: KindString,
			Synonyms: []string{"设备名称", "网元名称", "名称"}},
		{Name: "city", Label: "城市", Kind: KindString, Synonyms: []string{"城市", "地市", "归属地"}},
		{Name: "vendor", Label: "厂商", Kind: KindString, Synonyms: []string{"厂商", "供应商"}},
		{Name: "model", Label: "型号", Kind: KindString, Synonyms: []string{"型号", "设备型号"}},
		{Name: "mgmt_ip", Label: "管理IP", Kind: KindIPv4, Synonyms: []string{"管理IP", "管理地址", "IP地址", "IP"}},
		{Name: "status", Label: "状态", Kind: KindEnum, Enum: StatusEnum,
			Synonyms: []string{"状态", "设备状态", "运行状态"}},
	},
}

// ipPoolSpec is the ip_pool schema of §6.
var ipPoolSpec = TypeSpec{
	Type:      domain.ResourceTypeIPPool,
	SheetHint: "池", // substring match covers 地址池 and a bare 池
	Fields: []FieldSpec{
		{Name: "pool_name", Label: "地址池名称", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"地址池名称", "池名称", "名称"}},
		{Name: "cidr", Label: "CIDR", Required: true, Kind: KindCIDR,
			Synonyms: []string{"CIDR", "网段", "地址段", "子网"}},
		{Name: "gateway", Label: "网关", Kind: KindIPv4, Synonyms: []string{"网关", "网关地址"}},
		{Name: "vlan_id", Label: "VLAN", Kind: KindInt, Synonyms: []string{"VLAN", "VLAN_ID", "VLAN编号"}},
		{Name: "assigned_device", Label: "归属设备", Kind: KindRef, RefType: domain.ResourceTypeDevice,
			Synonyms: []string{"归属设备", "所属设备", "绑定设备"}},
		{Name: "status", Label: "状态", Kind: KindEnum, Enum: StatusEnum,
			Synonyms: []string{"状态", "设备状态", "运行状态"}},
	},
}

// circuitSpec is the circuit schema of §6.
var circuitSpec = TypeSpec{
	Type:      domain.ResourceTypeCircuit,
	SheetHint: "电路",
	Fields: []FieldSpec{
		{Name: "circuit_code", Label: "电路编号", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"电路编号", "电路编码", "电路ID"}},
		{Name: "a_end_device", Label: "A端设备", Required: true, Kind: KindRef, RefType: domain.ResourceTypeDevice,
			Synonyms: []string{"A端设备", "A端", "本端设备"}},
		{Name: "z_end_device", Label: "Z端设备", Required: true, Kind: KindRef, RefType: domain.ResourceTypeDevice,
			Synonyms: []string{"Z端设备", "Z端", "对端设备"}},
		{Name: "bandwidth_mbps", Label: "带宽Mbps", Kind: KindInt,
			Synonyms: []string{"带宽Mbps", "带宽(Mbps)", "带宽", "速率"}},
		{Name: "status", Label: "状态", Kind: KindEnum, Enum: StatusEnum,
			Synonyms: []string{"状态", "设备状态", "运行状态"}},
	},
}

// Specs lists the three resource type schemas in canonical order (device,
// ip_pool, circuit). The order is the deterministic tie-breaker of
// SuggestType and of the suggestion fallback for untyped sheets. Treat the
// list as read-only.
var Specs = []TypeSpec{deviceSpec, ipPoolSpec, circuitSpec}

// SpecByType returns the schema of t; ok is false for unknown types.
func SpecByType(t domain.ResourceType) (TypeSpec, bool) {
	for _, s := range Specs {
		if s.Type == t {
			return s, true
		}
	}
	return TypeSpec{}, false
}
