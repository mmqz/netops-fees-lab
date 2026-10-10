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
	// NameFieldOverride pins the field whose value becomes the resource
	// Name when the default NameField heuristic (first *_name field)
	// picks the wrong one: port carries BOTH device_name and port_name,
	// and the port's own name (port_name) is the display name —
	// device_name is the scope component of the natural key, not the
	// port's name. Empty = the heuristic applies unchanged.
	NameFieldOverride string
	// KeyFallbackFields names the fields building the natural key when a
	// PRIMARY key field's value is empty (conditional natural key,
	// v0.4; referenced by field name, order = join order). While every
	// primary key value is non-empty, the key is the classic join of the
	// natural-key fields (spec order, '|'). When ANY is empty, the key
	// is rebuilt from these fields' values in the declared order,
	// SKIPPING empty values (an unmapped/empty scope field degrades the
	// key to the remaining components — e.g. a base-station sheet without
	// a 地市 column degrades to the bare station name), and the importer
	// records a W004 warning for the identity-key degradation. The first
	// user is base_station (base_station_code primary, city +
	// base_station_name fallback); empty = the classic unconditional
	// key. Every referenced field must exist in Fields (pinned by
	// TestBaseStationSpec validation of all Specs).
	KeyFallbackFields []string
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

// NaturalKeyField returns the first natural key field of the type. §6 types
// have exactly one, port has two (device_name + port_name) — prefer
// NaturalKeyFields for composite-aware code.
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
// NameFieldOverride when set, else the first field whose name ends in
// "_name", else the natural key field. For the §6 schemas this yields
// device_name / pool_name / circuit_code; port overrides to port_name
// (device_name is the key's scope component, not the port's own name).
func (t TypeSpec) NameField() string {
	if t.NameFieldOverride != "" {
		return t.NameFieldOverride
	}
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

// portSpec is the device-port schema (v0.3.1; natural key rebased in v0.4
// development, migration v8): the telecom port workbooks of the real-network
// samples (端口流量监控表 / 端口资源表). The natural key is COMPOSITE —
// device_name + port_name — because a port name repeats across devices; the
// importer joins the key values with '|' in natural-key (spec field) order
// (single-key types are byte-identical to the classic behaviour). The device
// side of the key is the device NAME, not its IP: a re-IPped device must keep
// its ports one resource (IP is a mutable attribute, R4's P1 finding).
// device_ip deliberately stays an ipv4 VALUE column (required, not a device
// ref, not a key): the port workbook is a self-contained export whose device
// rows may not exist (yet) in the resource base, and E003 on a missing device
// would reject the whole port sheet. NameFieldOverride pins the display name
// to port_name — device_name is the key's scope, not the port's own name.
var portSpec = TypeSpec{
	Type:              domain.ResourceTypePort,
	SheetHint:         "端口",
	NameFieldOverride: "port_name",
	Fields: []FieldSpec{
		{Name: "device_name", Label: "设备名称", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"设备名称", "网元名称", "设备名"}},
		{Name: "port_name", Label: "端口名称", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"端口名称", "端口名", "端口"}},
		{Name: "device_ip", Label: "设备IP", Required: true, Kind: KindIPv4,
			Synonyms: []string{"设备IP地址", "设备ip", "设备IP", "网元IP"}},
		{Name: "port_desc", Label: "端口描述", Kind: KindString, Synonyms: []string{"端口描述"}},
		{Name: "port_type", Label: "端口类型", Kind: KindString, Synonyms: []string{"端口类型"}},
		{Name: "bandwidth_mbps", Label: "带宽Mbps", Kind: KindInt, // no IntMin: real sheets carry 0
			Synonyms: []string{"端口带宽", "端口速率(M)", "端口速率", "速率", "带宽Mbps"}},
		{Name: "admin_status", Label: "管理状态", Kind: KindString, Synonyms: []string{"管理状态"}},
		{Name: "oper_status", Label: "运行状态", Kind: KindString, Synonyms: []string{"运行状态"}},
		{Name: "device_type", Label: "设备类型", Kind: KindString, Synonyms: []string{"设备类型"}},
		{Name: "city", Label: "地市", Kind: KindString, Synonyms: []string{"地市", "城市"}},
		{Name: "district", Label: "区县", Kind: KindString, Synonyms: []string{"区县", "县区"}},
		{Name: "vendor", Label: "厂家", Kind: KindString, Synonyms: []string{"厂家", "厂商"}},
		{Name: "model", Label: "型号", Kind: KindString, Synonyms: []string{"型号", "设备型号"}},
		{Name: "record_date", Label: "日期", Kind: KindString, Synonyms: []string{"日期", "采集日期"}},
		// Geographic position (optional): same columns/kinds as device, so a
		// base-station port sheet can carry 经纬度 the same way.
		{Name: "geo_lat", Label: "纬度", Kind: KindLat,
			Synonyms: []string{"纬度", "lat", "LAT", "GPS纬度", "北纬"}},
		{Name: "geo_lng", Label: "经度", Kind: KindLng,
			Synonyms: []string{"经度", "lng", "lon", "LON", "GPS经度", "东经"}},
		{Name: "geo", Label: "经纬度", Kind: KindGeo,
			Synonyms: []string{"经纬度", "装机经纬度", "坐标", "GPS坐标", "位置"}},
	},
}

// baseStationSpec is the wireless base-station schema (v0.4, W2-D): the real
// 无线基站 export (samples/telecom-base-station.xlsx). The natural key is
// CONDITIONAL (R4 ruling, natural-keys.md §4): the primary key field
// base_station_code (基站编码, system-issued, globally unique) is OPTIONAL —
// the real sheet's code column exists but the values are empty — so the spec
// declares KeyFallbackFields [city, base_station_name]: when the code is
// empty the key degrades to 地市|无线基站名称 (empty values skipped: the real
// sheet carries NO 地市 column, so its key is the bare station name) and the
// importer records W004 (身份键退化). E002 required applies to
// base_station_name only; base_station_code/city stay optional. The base
// station IP is a KindString VALUE column, deliberately NOT KindIPv4: real
// cells carry a subnet mask suffix (172.7.10.125/30) and the address is a
// mutable attribute, never identity (E004 would reject the masked form).
// uplink_a_ip/gateway_ip are real IPv4 columns and stay KindIPv4. No ref
// fields: the station sheet is a self-contained export (same rationale as
// port's device_ip), so base_station derives no relations. NameFieldOverride
// pins the display name to base_station_name (the code is the key, the
// human name is the station's name).
var baseStationSpec = TypeSpec{
	Type:              domain.ResourceTypeBaseStation,
	SheetHint:         "基站",
	NameFieldOverride: "base_station_name",
	KeyFallbackFields: []string{"city", "base_station_name"},
	Fields: []FieldSpec{
		{Name: "base_station_type", Label: "基站类型", Kind: KindString,
			Synonyms: []string{"基站类型", "基站类别"}},
		{Name: "base_station_name", Label: "无线基站名称", Required: true, Kind: KindString,
			Synonyms: []string{"无线基站名称", "基站名称", "基站名", "名称"}},
		{Name: "base_station_code", Label: "基站编码", NaturalKey: true, Kind: KindString,
			Synonyms: []string{"基站编码", "基站编号", "基站ID", "基站代码"}},
		// 地市 is NOT in the real sheet's column set — the synonym
		// reserves the scope field for future sheets that carry it
		// (the fallback key then becomes 地市|名称 instead of bare
		// 名称). An unmapped column simply yields an empty value.
		{Name: "city", Label: "地市", Kind: KindString,
			Synonyms: []string{"地市", "城市", "归属地"}},
		{Name: "sharing", Label: "电联共享", Kind: KindString,
			Synonyms: []string{"电联共享", "共享", "电信联通共享"}},
		{Name: "uplink_a_ip", Label: "归属A IP", Kind: KindIPv4,
			Synonyms: []string{"归属A IP", "归属A IP地址", "归属A_ip"}},
		{Name: "ring", Label: "环", Kind: KindString, Synonyms: []string{"环", "所属环"}},
		{Name: "chain", Label: "链", Kind: KindString, Synonyms: []string{"链", "所属链"}},
		{Name: "uplink_a_name", Label: "归属A名称", Kind: KindString,
			Synonyms: []string{"归属A名称", "归属A 名称", "上联A名称"}},
		{Name: "uplink_a_port", Label: "归属A端口", Kind: KindString,
			Synonyms: []string{"归属A端口", "归属A 端口", "上联A端口"}},
		{Name: "gateway_ip", Label: "网关IP", Kind: KindIPv4,
			Synonyms: []string{"网关IP", "网关ip", "网关地址"}},
		{Name: "base_station_ip", Label: "基站IP", Kind: KindString,
			Synonyms: []string{"基站IP", "基站ip", "基站IP地址"}},
		{Name: "description", Label: "描述", Kind: KindString, Synonyms: []string{"描述"}},
		{Name: "work_order", Label: "工单号", Kind: KindString,
			Synonyms: []string{"工单号", "工单编号", "工单"}},
		{Name: "remark", Label: "备注", Kind: KindString, Synonyms: []string{"备注"}},
	},
}

// Specs lists the resource type schemas in canonical order (device,
// ip_pool, circuit, port, base_station). The order is the deterministic
// tie-breaker of SuggestType and of the suggestion fallback for untyped
// sheets. Treat the list as read-only.
var Specs = []TypeSpec{deviceSpec, ipPoolSpec, circuitSpec, portSpec, baseStationSpec}

// SpecByType returns the schema of t; ok is false for unknown types.
func SpecByType(t domain.ResourceType) (TypeSpec, bool) {
	for _, s := range Specs {
		if s.Type == t {
			return s, true
		}
	}
	return TypeSpec{}, false
}
