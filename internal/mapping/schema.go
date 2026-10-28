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

	"github.com/mmqz/netops-fees-lab/internal/domain"
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

// statusField builds the lifecycle-status field of the issue #45
// field-thickness round: before W30 only device/ip_pool/circuit/service
// carried one, so 98% of the demo resources imported with a NULL status and
// the resource browser rendered a wall of 未知. The field is optional in
// every spec that gets it (unmapped column → NULL status, no W002, no
// relation effect); the demo generator now supplies values, real sheets
// supply them when they carry the column. extra appends type-specific
// header synonyms (机房状态/局站状态/…) so future real exports auto-map.
func statusField(extra ...string) FieldSpec {
	f := FieldSpec{
		Name:     "status",
		Label:    "状态",
		Kind:     KindEnum,
		Enum:     StatusEnum,
		Synonyms: []string{"状态"},
	}
	f.Synonyms = append(f.Synonyms, extra...)
	return f
}

// #38 站址主数据 — the closed raw-value sets of the three structured
// master-data columns (migration v40): the SHEET spellings (Chinese)
// plus the API tokens (English), the exact closed sets the domain
// parsers (ParseSiteNatureValue & co.) accept. The enum list and the
// parser must stay in lockstep — site_master_test pins both spellings
// through a real import.
var (
	siteNatureEnum = []string{"自建", "共享", "租用", "self_built", "shared", "leased"}
	powerModeEnum  = []string{"直供", "转供", "无", "direct", "transfer", "none"}
	lifecycleEnum  = []string{"在网", "退网", "已拆", "active", "retired", "dismantled"}
)

// siteMasterField builds one of the three #38 master-data fields.
// KindEnum (not KindString) carries the W002 doctrine: an out-of-set
// value warns and the row still imports with the column NULL — never a
// row failure, never a guess (the status precedent); a RECOGNIZED value
// lives ONLY in the structured column — attributes() skips enum fields.
func siteMasterField(name, label string, enum []string, synonyms ...string) FieldSpec {
	return FieldSpec{Name: name, Label: label, Kind: KindEnum, Enum: enum, Synonyms: synonyms}
}

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
	// Label is the human-readable (Chinese) name of the TYPE itself —
	// e.g. "网元设备" for device, the same wording the frontend's
	// RESOURCE_TYPE_LABELS uses. GET /api/schema (W5-D) projects it so
	// schema consumers get one canonical label per type without
	// hand-copying a second table.
	Label string
	// Layer is the seven-layer model position of the type (§10.2, as
	// amended by §12-B5: access inserted as L4). Part of the single
	// source of truth for type metadata (issue #26): the traverse
	// endpoint's layers parameter and the §10.5 root-cause layer logic
	// read it from here — never from a hand-copied table. LayerName
	// gives the display name.
	Layer int
	// Domain is the professional-domain axis (§2.1 专业域投影: 只看传输/
	// 无线/IP). One of the Domain* constants; projected by /api/schema
	// so consumers can group types without a second registry.
	Domain string
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
	// KeyNormalize enables the B8 natural-key normalization (W21, §12-B8)
	// for this type: the importer normalizes EVERY natural-key FIELD value
	// before joining the key — trim + fullwidth→halfwidth + upper
	// (NormalizeNaturalKeyComponent). Leading zeros are preserved (never
	// padded or stripped): "1" and "01" stay two distinct identities, while
	// " gy-001", "gy-001" and "ＧＹ-００１" collapse to one. Default false —
	// the 8 pre-W21 types keep the raw-value keys (byte-identical imports,
	// B4 conservative migration); the 25 W21 code-keyed types set it true.
	KeyNormalize bool
	// RenameDetection enables the importer's suspected-rename detection
	// (W006, v0.5 W6-D) for this type. It is set ONLY for types whose
	// natural key carries a NAME component — ip_pool (pool_name),
	// base_station (the city|name fallback key) and port (the
	// device_name|port_name composite): a source-side rename changes
	// such a key, and the re-imported row would otherwise insert as a
	// brand-new resource while the old-key resource lingers forever
	// (identity split, nobody informed). Code-keyed types (device,
	// circuit, site, machine_room) keep this OFF on doctrine: their keys
	// are system-issued identifiers, and a changed code IS another
	// resource by definition — proposing a merge would be wrong (see
	// docs/natural-keys.md §2/§4).
	RenameDetection bool
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

// KeyComponentNames returns every field name that participates in the
// type's key computation: the strict natural-key fields plus the
// conditional fallback fields (KeyFallbackFields). The rename apply flow
// (renames service) uses it to decide which attributes must be re-pointed
// from the short-lived new resource onto the identity-keeping old resource:
// a confirmed rename rewrites the natural key, so a key-bearing attribute
// (base_station_name of a fallback-key row, device_name of a port) must not
// keep the old value — the key and its source attribute would contradict
// each other and every re-import of the renamed source would fire a W005
// diff on that field.
func (t TypeSpec) KeyComponentNames() []string {
	names := t.NaturalKeyNames()
	return append(names, t.KeyFallbackFields...)
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
	Label:     "网元设备",
	Layer:     LayerDevice,
	Domain:    DomainDevice,
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
		// W21 optional ref columns (data-model v1.0 §3): each derives its
		// relation on import (mapping.RelationTypeFor) but ONLY when the
		// sheet actually maps the column — the classic devices.xlsx (no
		// 机房/环/IGP域/客户 columns) imports byte-identically (B4: zero
		// auto-derivation from existing data; new-import-only columns).
		{Name: "machine_room_id", Label: "所属机房", Kind: KindRef, RefType: domain.ResourceTypeMachineRoom,
			Synonyms: []string{"所属机房", "机房ID", "机房编号", "所在机房"}},
		{Name: "ring_code", Label: "所属环", Kind: KindRef, RefType: domain.ResourceTypeRing,
			Synonyms: []string{"所属环", "环编码", "环编号", "环ID"}},
		{Name: "igp_domain", Label: "所属IGP域", Kind: KindRef, RefType: domain.ResourceTypeIGPDomain,
			Synonyms: []string{"所属IGP域", "IGP域", "IGP域编码", "域编码"}},
		{Name: "serve_customer", Label: "服务客户", Kind: KindRef, RefType: domain.ResourceTypeCustomer,
			Synonyms: []string{"服务客户", "服务客户编码", "所属客户"}},
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
	Type:            domain.ResourceTypeIPPool,
	Label:           "IP地址池",
	Layer:           LayerLogic, // §12-B5: 地址空间属逻辑层（assigned_to 为跨层边）
	Domain:          DomainLogic,
	SheetHint:       "池",  // substring match covers 地址池 and a bare 池
	RenameDetection: true, // pool_name key: a source rename splits identity (W006)
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
	Label:     "传输电路",
	Layer:     LayerTransport,
	Domain:    DomainTransport,
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
		// W21 optional ref columns: the 层次穿透 (carried_by) carriers —
		// circuit.carried_och → otn_och, circuit.carried_timeslot →
		// sdh_timeslot (both derive carried_by). Unmapped → the classic
		// circuit sheets import byte-identically (B4).
		{Name: "carried_och", Label: "承载波道", Kind: KindRef, RefType: domain.ResourceTypeOTNOch,
			Synonyms: []string{"承载波道", "波道编码", "OTN波道", "波道"}},
		{Name: "carried_timeslot", Label: "承载时隙", Kind: KindRef, RefType: domain.ResourceTypeSDHTimeslot,
			Synonyms: []string{"承载时隙", "时隙编码", "SDH时隙", "时隙"}},
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
	Label:             "设备端口",
	Layer:             LayerDevice,
	Domain:            DomainDevice,
	SheetHint:         "端口",
	NameFieldOverride: "port_name",
	RenameDetection:   true, // device_name|port_name composite key: renames split identity (W006)
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
		// W21 optional ref columns: board_id derives mounted_on (port →
		// board) when mapped; vpn_instance derives in_vpn (port →
		// vpn_instance). Unmapped → no W001, no relation (B4: existing
		// port imports byte-identical).
		{Name: "board_id", Label: "所属板卡", Kind: KindRef, RefType: domain.ResourceTypeBoard,
			Synonyms: []string{"所属板卡", "板卡ID", "板卡编号"}},
		{Name: "vpn_instance", Label: "所属VPN实例", Kind: KindRef, RefType: domain.ResourceTypeVPNInstance,
			Synonyms: []string{"所属VPN实例", "VPN实例", "VPN编码", "所属VPN"}},
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
	Label:             "基站",
	Layer:             LayerAccess,
	Domain:            DomainAccess,
	SheetHint:         "基站",
	NameFieldOverride: "base_station_name",
	KeyFallbackFields: []string{"city", "base_station_name"},
	RenameDetection:   true, // name-bearing fallback key: renames split identity (W006)
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
		// W21 optional ref columns: serve_customer derives serves
		// (base_station → customer); uplink_device derives uplink_to
		// (base_station → device) — the explicit replacement of the
		// 归属A TEXT columns, which derive NOTHING (§12-B4: no
		// name/IP inference; uplink_device is a NEW column only new
		// imports carry). Unmapped → the real station sheets import
		// byte-identically.
		{Name: "serve_customer", Label: "服务客户", Kind: KindRef, RefType: domain.ResourceTypeCustomer,
			Synonyms: []string{"服务客户", "服务客户编码", "所属客户"}},
		{Name: "uplink_device", Label: "上联设备", Kind: KindRef, RefType: domain.ResourceTypeDevice,
			Synonyms: []string{"上联设备", "上联设备编码", "上联网元"}},
		{Name: "description", Label: "描述", Kind: KindString, Synonyms: []string{"描述"}},
		{Name: "work_order", Label: "工单号", Kind: KindString,
			Synonyms: []string{"工单号", "工单编号", "工单"}},
		{Name: "remark", Label: "备注", Kind: KindString, Synonyms: []string{"备注"}},
		// W30 (issue #45) lifecycle status: the real station tables carry a
		// 状态 column; the demo generator supplies the enum values. Optional —
		// unmapped → NULL status (no W002).
		statusField("基站状态"),
		// W30 (issue #45) geo trio — the same columns/kinds as device: the
		// OpenCelliD real coordinates (realdata-core.json) land as REAL
		// columns via migration v20, making base_station the spatially
		// richest type (1,198 stations) instead of the 90-port exception.
		{Name: "geo_lat", Label: "纬度", Kind: KindLat,
			Synonyms: []string{"纬度", "lat", "LAT", "GPS纬度", "北纬"}},
		{Name: "geo_lng", Label: "经度", Kind: KindLng,
			Synonyms: []string{"经度", "lng", "lon", "LON", "GPS经度", "东经"}},
		{Name: "geo", Label: "经纬度", Kind: KindGeo,
			Synonyms: []string{"经纬度", "装机经纬度", "坐标", "GPS坐标", "位置"}},
		// #38 站址主数据三列 — the structured reconcile-attribution inputs
		// (site_nature/power_mode/lifecycle, migration v40): recognized
		// values project onto the REAL columns the cross-dataset reconcile
		// reads through the confirmed link; out-of-set values warn W002 and
		// stay NULL. Optional — the classic station sheets without these
		// columns import byte-identically.
		siteMasterField("site_nature", "站址性质", siteNatureEnum, "站址性质", "站址类型"),
		siteMasterField("power_mode", "供电方式", powerModeEnum, "供电方式", "供电"),
		siteMasterField("lifecycle", "生命周期", lifecycleEnum, "生命周期", "生命周期状态"),
	},
}

// siteSpec is the 局站 (site) schema (v0.4, W3-D): one row of the real 机房局站
// export (samples/telecom-site-machine-room.xlsx, sheet family 机房局站表).
// The natural key is site_id (局站ID, a 12-digit numeric code, globally
// unique — R4 ruling, natural-keys.md §4). The ID field is KindString, NOT a
// numeric kind: the codes are system-issued digit strings whose leading zeros
// are significant identity (an int column/parse would strip them, splitting
// identities); there is no arithmetic on the code, so text is the honest kind.
// site is a plain container (no ref fields, no derived relations): nothing
// points FROM a site in this schema family. site_name (局站名称) is the
// display name (the first *_name field, so the default NameField heuristic
// needs no override); device_name is the nickname of a device AT the site —
// a plain attribute, deliberately NOT a device ref: the device schema's key
// is device_code and this sheet carries only free-text names (unresolvable,
// same rationale as port's device_ip). ip_address is a mutable attribute
// (KindIPv4 → E004 on malformed values), never identity.
var siteSpec = TypeSpec{
	Type:      domain.ResourceTypeSite,
	Label:     "局站",
	Layer:     LayerSpace,
	Domain:    DomainSpace,
	SheetHint: "局站",
	Fields: []FieldSpec{
		{Name: "site_id", Label: "局站ID", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"局站ID", "局站编号", "局站编码", "局站代码"}},
		{Name: "site_name", Label: "局站名称", Required: true, Kind: KindString,
			Synonyms: []string{"局站名称", "局站名", "名称"}},
		{Name: "city", Label: "地市", Kind: KindString, Synonyms: []string{"地市", "城市", "归属地"}},
		{Name: "district", Label: "区县", Kind: KindString, Synonyms: []string{"区县", "县区"}},
		{Name: "device_name", Label: "设备名称", Kind: KindString,
			Synonyms: []string{"设备名称", "网元名称", "设备名"}},
		{Name: "ip_address", Label: "IP地址", Kind: KindIPv4,
			Synonyms: []string{"IP地址", "IP", "设备IP"}},
		// W30 (issue #45): lifecycle status — optional, unmapped → NULL.
		statusField("局站状态"),
		// #38 站址主数据三列（同 base_station 的口径，迁移 v40）。
		siteMasterField("site_nature", "站址性质", siteNatureEnum, "站址性质", "站址类型"),
		siteMasterField("power_mode", "供电方式", powerModeEnum, "供电方式", "供电"),
		siteMasterField("lifecycle", "生命周期", lifecycleEnum, "生命周期", "生命周期状态"),
	},
}

// machineRoomSpec is the 机房 (machine room) schema (v0.4, W3-D): the second
// reading of the SAME 机房局站 export — one physical sheet, two resource
// types (先按局站类型导入落 site，再按机房类型导入同一 sheet). The natural key
// is machine_room_id (机房ID, a 17-digit numeric code, globally unique — R4
// ruling, natural-keys.md §4). Like site_id it is KindString on purpose:
// digit-string codes keep their leading zeros as identity and take no
// arithmetic, so a numeric kind would only invite coercion damage. site_id
// (局站ID) is a KindRef to site and is OPTIONAL: when the sites are not
// imported (yet), the column can stay unmapped and the rooms import as
// relation-less orphans (W001 per the generic optional-ref semantics); when
// it IS mapped, the existing E003 semantics pin the import ORDER — the
// referenced site must already exist (earlier batch or an earlier sheet of
// the same batch), otherwise the row fails E003 — and each resolved ref
// derives one machine_room --belongs_to--> site relation. device_name is the
// nickname of a device IN the room — a plain attribute, NOT a device ref
// (the device key is device_code; this sheet's free-text names cannot
// resolve, so no relation is invented from them). ip_address is a mutable
// attribute (KindIPv4). NameFieldOverride pins the display name to
// machine_room_name — explicit like port/base_station, so a future field
// insertion before it can never hijack the "first *_name" heuristic.
var machineRoomSpec = TypeSpec{
	Type:              domain.ResourceTypeMachineRoom,
	Label:             "机房",
	Layer:             LayerSpace,
	Domain:            DomainSpace,
	SheetHint:         "机房",
	NameFieldOverride: "machine_room_name",
	Fields: []FieldSpec{
		{Name: "machine_room_id", Label: "机房ID", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"机房ID", "机房编号", "机房编码", "机房代码"}},
		{Name: "machine_room_name", Label: "机房名称", Required: true, Kind: KindString,
			Synonyms: []string{"机房名称", "机房名"}},
		{Name: "site_id", Label: "局站ID", Kind: KindRef, RefType: domain.ResourceTypeSite,
			Synonyms: []string{"局站ID", "局站编号", "局站编码"}},
		{Name: "city", Label: "地市", Kind: KindString, Synonyms: []string{"地市", "城市", "归属地"}},
		{Name: "district", Label: "区县", Kind: KindString, Synonyms: []string{"区县", "县区"}},
		{Name: "device_name", Label: "设备名称", Kind: KindString,
			Synonyms: []string{"设备名称", "网元名称", "设备名"}},
		{Name: "ip_address", Label: "IP地址", Kind: KindIPv4,
			Synonyms: []string{"IP地址", "IP", "设备IP"}},
		// W30 (issue #45): lifecycle status — optional, unmapped → NULL.
		statusField("机房状态"),
		// #38 站址主数据三列（同 base_station 的口径，迁移 v40）。
		siteMasterField("site_nature", "站址性质", siteNatureEnum, "站址性质", "站址类型"),
		siteMasterField("power_mode", "供电方式", powerModeEnum, "供电方式", "供电"),
		siteMasterField("lifecycle", "生命周期", lifecycleEnum, "生命周期", "生命周期状态"),
	},
}

// ptpPortSpec is the PTP 端口 schema (v0.5, W8-D): one row per PTP-configured
// interface of a device-model configuration script (the ptpconfig parser
// synthesizes the virtual sheet; samples/ptp-config-script.txt is the real
// ZTE VRP material). The natural key is COMPOSITE — device_model +
// port_name — mirroring the port precedent: a port/interface name repeats
// across models, and the model scopes it. Both components are NAME-class
// values (the model title of the script block and the interface name), so
// the key is name-class and RenameDetection is ON (W006: an interface
// renamed in a later revision of the script splits identity — the proposal
// flow lets a human confirm the merge; see docs/natural-keys.md §4).
// device_model is the MODEL-level template identity, not a device
// instance: the scripts carry no device codes/IPs, so no ref field and no
// derived relation exist — an instance-level association would need
// identity data the scripts do not carry (honest declaration, §18 of
// import-and-validation.md). Global PTP clock parameters (clock mode,
// priorities, domain, SSM …) of the model's block are flattened onto every
// row of that model — the flat table is the honest materialization of "one
// script block, N interface rows, one global section".
// NameFieldOverride pins the display name to port_name (device_model is
// the key's scope component, not the port's own name).
var ptpPortSpec = TypeSpec{
	Type:              domain.ResourceTypePTPPort,
	Label:             "PTP端口",
	Layer:             LayerDevice,
	Domain:            DomainDevice,
	SheetHint:         "PTP", // matches the synthesized virtual sheet "PTP配置"
	NameFieldOverride: "port_name",
	RenameDetection:   true, // device_model|port_name composite key: renames split identity (W006)
	Fields: []FieldSpec{
		{Name: "device_model", Label: "设备型号", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"设备型号"}},
		{Name: "port_name", Label: "接口名称", Required: true, NaturalKey: true, Kind: KindString,
			Synonyms: []string{"接口名称", "接口", "端口名称"}},
		{Name: "annotation", Label: "注释", Kind: KindString, Synonyms: []string{"注释"}},
		{Name: "portnum", Label: "端口号", Kind: KindInt, Synonyms: []string{"端口号"}},
		{Name: "port_state", Label: "端口状态", Kind: KindString,
			Synonyms: []string{"端口状态"}},
		{Name: "free_mode", Label: "自由模式", Kind: KindString,
			Synonyms: []string{"自由模式"}},
		{Name: "pdelay_req_interval", Label: "延迟请求间隔", Kind: KindInt,
			Synonyms: []string{"延迟请求间隔"}},
		{Name: "unsymmetrical_offset", Label: "非对称偏移", Kind: KindString,
			// string on purpose: the real value carries a textual suffix
			// ("0 negative"), so a numeric kind would E004 it.
			Synonyms: []string{"非对称偏移"}},
		{Name: "delay_mechanism", Label: "延迟机制", Kind: KindEnum,
			Enum: []string{"e2e", "p2p"}, Synonyms: []string{"延迟机制"}},
		{Name: "domain_num", Label: "域号", Kind: KindInt, Synonyms: []string{"域号"}},
		{Name: "clock_mode", Label: "时钟模式", Kind: KindString, Synonyms: []string{"时钟模式"}},
		{Name: "state_algorithm", Label: "状态算法", Kind: KindString, Synonyms: []string{"状态算法"}},
		{Name: "priority1", Label: "优先级1", Kind: KindInt, Synonyms: []string{"优先级1"}},
		{Name: "priority2", Label: "优先级2", Kind: KindInt, Synonyms: []string{"优先级2"}},
		{Name: "local_priority", Label: "本地优先级", Kind: KindInt, Synonyms: []string{"本地优先级"}},
		{Name: "clock_class", Label: "时钟等级", Kind: KindInt, Synonyms: []string{"时钟等级"}},
		{Name: "clock_accuracy", Label: "时钟精度", Kind: KindInt, Synonyms: []string{"时钟精度"}},
		{Name: "announce_interval", Label: "通告间隔", Kind: KindInt, Synonyms: []string{"通告间隔"}},
		{Name: "announce_timeout", Label: "通告超时", Kind: KindInt, Synonyms: []string{"通告超时"}},
		{Name: "utc_trace", Label: "UTC追踪", Kind: KindString,
			// string on purpose: the real value is a compound ("auto holdover 24").
			Synonyms: []string{"UTC追踪"}},
		{Name: "ssm_mode", Label: "SSM模式", Kind: KindString, Synonyms: []string{"SSM模式"}},
		{Name: "ssm_sec_quality", Label: "SSM质量等级", Kind: KindInt, Synonyms: []string{"SSM质量等级"}},
		{Name: "ssm_wtr_time", Label: "SSM等待恢复时间", Kind: KindInt, Synonyms: []string{"SSM等待恢复时间"}},
	},
}

// Specs lists the resource type schemas in canonical order: the 8 pre-W21
// types (device, ip_pool, circuit, port, base_station, site, machine_room,
// ptp_port) followed by the 25 W21 扩容 types in §3 catalog order (see
// schema_w21.go). The order is the deterministic tie-breaker of SuggestType
// and of the suggestion fallback for untyped sheets. site is registered
// BEFORE machine_room (container first, mirroring the import story
// 先局站后机房): a sheet named 机房局站表 matches BOTH SheetHints (机房 and
// 局站) and SuggestType therefore suggests site — the suggestion is only a
// prefill, the mapping confirmation page lets the user switch to
// machine_room for the second pass. The same generic-before-specific rule
// orders optical_cable BEFORE cable_segment (a sheet named 光缆段表
// suggests optical_cable first, the user switches for the segment pass).
// ptp_port's SheetHint "PTP" only matters for the synthesized virtual
// sheet ("PTP配置"); an xlsx sheet whose name carries 端口/机房/… suggests
// those types first by order. Treat the list as read-only.
var Specs = append([]TypeSpec{deviceSpec, ipPoolSpec, circuitSpec, portSpec, baseStationSpec, siteSpec, machineRoomSpec, ptpPortSpec}, w21Specs...)

// SpecByType returns the schema of t; ok is false for unknown types.
func SpecByType(t domain.ResourceType) (TypeSpec, bool) {
	for _, s := range Specs {
		if s.Type == t {
			return s, true
		}
	}
	return TypeSpec{}, false
}

// RelationTypeFor maps a ref field of a resource type to the relation type
// the importer derives from it (PROJECT_BRIEF §6 + the v0.4 extension):
// ip_pool.assigned_device → assigned_to, circuit.a_end_device → a_end,
// circuit.z_end_device → z_end, machine_room.site_id → belongs_to (the
// container attribution of a machine room to its site; the ref itself
// resolves through the same E003 path as any other ref, so the import
// order 先局站后机房 is enforced by E003, not by this switch). Any other
// combination returns "" — no relation is derived from it.
//
// This is the SINGLE ref-field → relation-type derivation mapping of the
// codebase (v0.6.0 W13-D moved it here from the importer's private
// relationTypeFor so a second consumer could exist without a second copy):
// the importer calls it when creating the derived edge at import time, and
func RelationTypeFor(rt domain.ResourceType, field string) domain.RelationType {
	switch {
	case rt == domain.ResourceTypeIPPool && field == "assigned_device":
		return domain.RelationTypeAssignedTo
	case rt == domain.ResourceTypeCircuit && field == "a_end_device":
		return domain.RelationTypeAEnd
	case rt == domain.ResourceTypeCircuit && field == "z_end_device":
		return domain.RelationTypeZEnd
	case rt == domain.ResourceTypeMachineRoom && field == "site_id":
		return domain.RelationTypeBelongsTo

	// ---- W21 扩容（data-model v1.0 §4.2）：27 个新 ref 字段 → 25 种新关系 ----
	// 空间归属.
	case rt == domain.ResourceTypeRack && field == "machine_room_id":
		return domain.RelationTypeLocatedIn
	case rt == domain.ResourceTypeDevice && field == "machine_room_id":
		return domain.RelationTypeHousedIn
	case rt == domain.ResourceTypeBoard && field == "device_code":
		return domain.RelationTypeInstalledIn
	case rt == domain.ResourceTypePort && field == "board_id":
		return domain.RelationTypeMountedOn
	// 物理连接.
	case rt == domain.ResourceTypeOpticalCable && field == "a_end_site":
		return domain.RelationTypeCableAEnd
	case rt == domain.ResourceTypeOpticalCable && field == "z_end_site":
		return domain.RelationTypeCableZEnd
	case rt == domain.ResourceTypeOpticalCable && field == "connect_device":
		return domain.RelationTypeConnectsDevice
	case rt == domain.ResourceTypeCableSegment && field == "cable_code":
		return domain.RelationTypeSegmentOf
	case rt == domain.ResourceTypeFiber && field == "cable_code":
		return domain.RelationTypeFiberOf
	case rt == domain.ResourceTypeFiber && field == "terminate_odf":
		return domain.RelationTypeFiberTerminates
	// 传输承载.
	case rt == domain.ResourceTypeCircuit && field == "carried_och":
		return domain.RelationTypeCarriedBy
	case rt == domain.ResourceTypeCircuit && field == "carried_timeslot":
		return domain.RelationTypeCarriedBy
	case rt == domain.ResourceTypeOTNOch && field == "route_cable":
		return domain.RelationTypeRoutedOver
	case rt == domain.ResourceTypeOTNOch && field == "use_fiber":
		return domain.RelationTypeUsesFiber
	// 逻辑归属.
	case rt == domain.ResourceTypeDevice && field == "ring_code":
		return domain.RelationTypeMemberOfRing
	case rt == domain.ResourceTypeProtectionGroup && field == "protected_circuit":
		return domain.RelationTypeProtects
	case rt == domain.ResourceTypeDevice && field == "igp_domain":
		return domain.RelationTypeInDomain
	case rt == domain.ResourceTypePort && field == "vpn_instance":
		return domain.RelationTypeInVPN
	// 业务服务.
	case rt == domain.ResourceTypeBaseStation && field == "serve_customer":
		return domain.RelationTypeServes
	case rt == domain.ResourceTypeDevice && field == "serve_customer":
		return domain.RelationTypeServes
	case rt == domain.ResourceTypeService && field == "customer_code":
		return domain.RelationTypeSubscribedBy
	// W25（#16 验收补齐）：专线表「客户名称」列 → 客户引用，与 service 同
	// 语义的 subscribed_by 边（注册表 FromTypes 已同步扩展）。
	case rt == domain.ResourceTypeLeasedLine && field == "customer_code":
		return domain.RelationTypeSubscribedBy
	case rt == domain.ResourceTypeService && field == "provide_circuit":
		return domain.RelationTypeProvidedBy
	case rt == domain.ResourceTypeService && field == "depend_device":
		return domain.RelationTypeDependsOn
	case rt == domain.ResourceTypeService && field == "depend_circuit":
		return domain.RelationTypeDependsOn
	// 接入关联.
	case rt == domain.ResourceTypeBaseStation && field == "uplink_device":
		return domain.RelationTypeUplinkTo
	case rt == domain.ResourceTypeRRU && field == "home_bbu":
		return domain.RelationTypeHomeBBU
	case rt == domain.ResourceTypeRRU && field == "cover_station":
		return domain.RelationTypeCovers
	case rt == domain.ResourceTypeONU && field == "connect_port":
		return domain.RelationTypeConnectsONU
	default:
		return domain.RelationType("")
	}
}
