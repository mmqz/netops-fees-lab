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
	"fmt"
	"math"
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
	KindLat    FieldKind = "lat"    // latitude, float in [-90, 90]
	KindLng    FieldKind = "lng"    // longitude, float in [-180, 180]
	KindGeo    FieldKind = "geo"    // combined "num1,num2" (either order)
)

// CheckFormat reports whether value is well-formed for kind k. When it is
// not, reason describes the failure (it feeds the E004 issue evidence and
// message). Enum fields are NOT checked here — an out-of-enum status is the
// warning W002, not the error E004 (PROJECT_BRIEF §7); ref fields are
// resolved against the database (E003). Integer RANGE constraints
// (vlan_id 1..4094, bandwidth_mbps > 0) are field-specific, so they live on
// FieldSpec.CheckFormat, which wraps this parse-level check.
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
	case KindLat:
		v, err := parseFloatStrict(value)
		if err != nil || v < -90 || v > 90 {
			return "不是合法的纬度（-90 到 90 的数字）", false
		}
	case KindLng:
		v, err := parseFloatStrict(value)
		if err != nil || v < -180 || v > 180 {
			return "不是合法的经度（-180 到 180 的数字）", false
		}
	case KindGeo:
		f1, f2, err := parseGeoPair(value)
		if err != nil {
			return "经纬度应为两个数字（如 126.63,45.75）", false
		}
		// Both components must be plausible coordinates (within
		// [-180,180]) and at least one must be a valid latitude
		// (within [-90,90]) — otherwise no ordering of the pair can
		// form a (lat, lng) assignment. Values failing this check
		// are E004 invalid_format.
		if f1 < -180 || f1 > 180 || f2 < -180 || f2 > 180 ||
			(f1 < -90 || f1 > 90) && (f2 < -90 || f2 > 90) {
			return "不是合法的经纬度（纬度 -90 到 90，经度 -180 到 180）", false
		}
	case KindString, KindEnum, KindRef:
		// No format constraint beyond trimming.
	}
	return "", true
}

// parseFloatStrict parses a decimal float, rejecting NaN/Inf and the Go
// syntax ParseFloat would accept but a spreadsheet column should not (hex
// forms, underscores). strconv.ParseFloat already rejects hex and
// underscores; NaN/Inf parse successfully, so they are rejected here.
func parseFloatStrict(value string) (float64, error) {
	if value == "" {
		return 0, strconv.ErrSyntax
	}
	v, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, err
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, strconv.ErrSyntax
	}
	return v, nil
}

// geoPairSeparators are the separators accepted inside a combined 经纬度
// value: half-width and full-width comma, semicolon and spaces (any run of
// them, mixed allowed).
const geoPairSeparators = ",，；; \t"

// parseGeoPair splits a combined coordinate value into exactly two floats.
// Separators: comma, Chinese comma, semicolon, whitespace (any mix). Values
// such as "126.63,45.75", "126.63，45.75", "126.63 45.75" all parse.
func parseGeoPair(value string) (float64, float64, error) {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return strings.ContainsRune(geoPairSeparators, r)
	})
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("geo: want two components, got %d", len(parts))
	}
	f1, err := parseFloatStrict(parts[0])
	if err != nil {
		return 0, 0, err
	}
	f2, err := parseFloatStrict(parts[1])
	if err != nil {
		return 0, 0, err
	}
	return f1, f2, nil
}

// China longitude/latitude bounds of the combined 经纬度 split heuristic
// (continental China plus a margin): longitude [73,136], latitude [3,54].
// The two windows are disjoint, so at most one ordering of a pair can match
// both — that is what makes the heuristic deterministic.
const (
	geoChinaLngMin = 73.0
	geoChinaLngMax = 136.0
	geoChinaLatMin = 3.0
	geoChinaLatMax = 54.0
)

// SplitGeoLatLng splits a combined 经纬度 value (either component order)
// into its latitude and longitude using the China-bounds heuristic:
//
//   - f1 in the China longitude window [73,136] AND f2 in the China
//     latitude window [3,54] → (lng, lat) order — the Gaode/Baidu
//     convention, e.g. "126.63,45.75" (哈尔滨);
//   - f1 in [3,54] AND f2 in [73,136] → (lat, lng) order, e.g.
//     "45.75,126.63";
//   - otherwise (foreign or ambiguous coordinates — no ordering matches
//     the China windows): keep whichever interpretation is in-range for
//     both slots. lng=f1/lat=f2 is the default (Gaode convention); it is
//     swapped to lat=f1/lng=f2 only when f2 cannot be a latitude while f1
//     can. Foreign coordinates are accepted — the heuristic never fails
//     on out-of-China values.
//
// ok is false only when the value does not parse as two floats at all; the
// caller then leaves the value un-split so the E004 format check reports
// it. Callers that already ran the KindGeo format check get in-range
// results by construction.
func SplitGeoLatLng(value string) (lat, lng float64, ok bool) {
	f1, f2, err := parseGeoPair(value)
	if err != nil {
		return 0, 0, false
	}
	if f1 >= geoChinaLngMin && f1 <= geoChinaLngMax && f2 >= geoChinaLatMin && f2 <= geoChinaLatMax {
		return f2, f1, true // (经度, 纬度) order — Gaode/Baidu convention
	}
	if f1 >= geoChinaLatMin && f1 <= geoChinaLatMax && f2 >= geoChinaLngMin && f2 <= geoChinaLngMax {
		return f1, f2, true // (纬度, 经度) order
	}
	// Neither ordering matches the China windows. Validity of each
	// interpretation: lat slot must be within [-90,90], lng slot within
	// [-180,180].
	latFirst := (f1 >= -90 && f1 <= 90) && (f2 >= -180 && f2 <= 180) // lat=f1, lng=f2
	lngFirst := (f1 >= -180 && f1 <= 180) && (f2 >= -90 && f2 <= 90) // lng=f1, lat=f2
	if latFirst && !lngFirst {
		return f1, f2, true // f2 cannot be a latitude; f1 must be it
	}
	// Default (covers lngFirst-only and both-valid): lng=f1, lat=f2.
	return f2, f1, true
}

// FormatGeoFloat renders a split coordinate as the plain decimal attribute
// string (strconv.FormatFloat 'f', -1 precision — no exponent, no trailing
// zeros): 45.773 stays "45.773", 126.6 stays "126.6".
func FormatGeoFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// CheckFormat reports whether value is well-formed for this field: it
// first runs the kind-level parse check (FieldKind.CheckFormat), then the
// field's integer range constraint — vlan_id must be within 1..4094 and
// bandwidth_mbps must be positive (PROJECT_BRIEF §6). An out-of-range
// value is an E004 invalid_format failure, exactly like a parse failure. An
// empty value is never range-checked here (required-ness is a separate
// concern).
func (f FieldSpec) CheckFormat(value string) (reason string, ok bool) {
	if reason, ok := f.Kind.CheckFormat(value); !ok {
		return reason, false
	}
	if f.Kind != KindInt || value == "" || (f.IntMin == 0 && f.IntMax == 0) {
		return "", true
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		// Unreachable: the kind-level check already rejected non-integers.
		return "不是十进制整数", false
	}
	switch {
	case f.IntMin > 0 && f.IntMax > 0:
		if n < f.IntMin || n > f.IntMax {
			return fmt.Sprintf("超出取值范围（%d-%d）", f.IntMin, f.IntMax), false
		}
	case f.IntMin > 0:
		if n < f.IntMin {
			return fmt.Sprintf("必须为不小于 %d 的整数", f.IntMin), false
		}
	case f.IntMax > 0:
		if n > f.IntMax {
			return fmt.Sprintf("必须为不大于 %d 的整数", f.IntMax), false
		}
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
	// IntMin is the inclusive lower bound of an int field's range
	// (PROJECT_BRIEF §6: vlan_id ≥ 1, bandwidth_mbps ≥ 1). Zero means
	// "no lower bound"; the pair (0, 0) disables the range check.
	IntMin int
	// IntMax is the inclusive upper bound of an int field's range
	// (PROJECT_BRIEF §6: vlan_id ≤ 4094). Zero means "no upper bound".
	IntMax int
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
		// Geographic position (optional, presentation metadata —
		// deliberately NOT part of the simulation state hash).
		// geo_lat/geo_lng are the canonical split columns; geo is
		// the combined "num1,num2" column (either order) the
		// importer splits via the China-bounds heuristic.
		{Name: "geo_lat", Label: "纬度", Kind: KindLat,
			Synonyms: []string{"纬度", "lat", "LAT", "GPS纬度", "北纬"}},
		{Name: "geo_lng", Label: "经度", Kind: KindLng,
			Synonyms: []string{"经度", "lng", "lon", "LON", "GPS经度", "东经"}},
		{Name: "geo", Label: "经纬度", Kind: KindGeo,
			Synonyms: []string{"经纬度", "装机经纬度", "坐标", "GPS坐标", "A端装机经纬度", "位置"}},
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
		{Name: "vlan_id", Label: "VLAN", Kind: KindInt, IntMin: 1, IntMax: 4094,
			Synonyms: []string{"VLAN", "VLAN_ID", "VLAN编号"}},
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
		{Name: "bandwidth_mbps", Label: "带宽Mbps", Kind: KindInt, IntMin: 1,
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
