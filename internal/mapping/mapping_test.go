package mapping

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/testfixtures"
)

// TestMappingSuggest is the table-driven suggestion-engine test
// (PROJECT_BRIEF §6 confidence ladder + type scoping).
func TestMappingSuggest(t *testing.T) {
	cases := []struct {
		name     string
		sheet    string
		header   string
		wantFld  string
		wantConf float64
	}{
		{"chinese synonym of device_code", testfixtures.DeviceSheetName, "设备编码", "device_code", 0.75},
		{"exact field name", testfixtures.DeviceSheetName, "device_code", "device_code", 1.0},
		{"normalized (case) field name", testfixtures.DeviceSheetName, "DEVICE_CODE", "device_code", 0.9},
		{"normalized (spaces) field name", testfixtures.DeviceSheetName, " device_code ", "device_code", 0.9},
		{"city synonym", testfixtures.DeviceSheetName, "城市", "city", 0.75},
		{"city synonym 2", testfixtures.DeviceSheetName, "地市", "city", 0.75},
		{"mgmt ip synonym", testfixtures.DeviceSheetName, "管理IP", "mgmt_ip", 0.75},
		{"mgmt ip english synonym", testfixtures.DeviceSheetName, "IP", "mgmt_ip", 0.75},
		{"english synonym code", testfixtures.DeviceSheetName, "code", "device_code", 0.75},
		{"unknown header has no suggestion", testfixtures.DeviceSheetName, "无关列", "", 0},
		{"other type's field is not suggested on typed sheet", testfixtures.DeviceSheetName, "网关", "", 0},
		{"cidr header matches field case-insensitively", testfixtures.IPPoolSheetName, "CIDR", "cidr", 0.9},
		{"vlan synonym", testfixtures.IPPoolSheetName, "VLAN", "vlan_id", 0.75},
		{"pool name synonym", testfixtures.IPPoolSheetName, "地址池名称", "pool_name", 0.75},
		{"pool name generic synonym", testfixtures.IPPoolSheetName, "池名称", "pool_name", 0.75},
		{"a-end synonym", testfixtures.CircuitSheetName, "A端设备", "a_end_device", 0.75},
		{"a-end short synonym", testfixtures.CircuitSheetName, "A端", "a_end_device", 0.75},
		{"z-end synonym", testfixtures.CircuitSheetName, "Z端设备", "z_end_device", 0.75},
		{"bandwidth synonym", testfixtures.CircuitSheetName, "带宽Mbps", "bandwidth_mbps", 0.75},
		{"bandwidth generic synonym", testfixtures.CircuitSheetName, "带宽", "bandwidth_mbps", 0.75},
		{"untyped sheet falls back to all types, exact", "杂项", "cidr", "cidr", 1.0},
		{"untyped sheet falls back to all types, synonym", "杂项", "名称", "device_name", 0.75},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Suggest(tc.sheet, []string{tc.header})
			if len(got) != 1 {
				t.Fatalf("Suggest returned %d suggestions, want 1", len(got))
			}
			if got[0].Field != tc.wantFld {
				t.Errorf("Suggest(%q, %q) field = %q, want %q", tc.sheet, tc.header, got[0].Field, tc.wantFld)
			}
			if got[0].Confidence != tc.wantConf {
				t.Errorf("Suggest(%q, %q) confidence = %v, want %v", tc.sheet, tc.header, got[0].Confidence, tc.wantConf)
			}
			if got[0].Header != tc.header {
				t.Errorf("Suggest echo header = %q, want %q", got[0].Header, tc.header)
			}
		})
	}

	// Whole-sheet suggestion of the real device workbook headers.
	got := Suggest(testfixtures.DeviceSheetName, testfixtures.DeviceHeaders)
	if len(got) != len(testfixtures.DeviceHeaders) {
		t.Fatalf("Suggest returned %d suggestions, want %d", len(got), len(testfixtures.DeviceHeaders))
	}
	want := []ColumnSuggestion{
		{"设备编码", "device_code", 0.75},
		{"设备名称", "device_name", 0.75},
		{"城市", "city", 0.75},
		{"厂商", "vendor", 0.75},
		{"型号", "model", 0.75},
		{"管理IP", "mgmt_ip", 0.75},
		{"状态", "status", 0.75},
	}
	for i, w := range want {
		if got[i] != (ColumnSuggestion{Header: w.Header, Field: w.Field, Confidence: w.Confidence}) {
			t.Errorf("device sheet suggestion[%d] = %+v, want %+v", i, got[i], w)
		}
	}
}

// TestSuggestType covers the sheet-name hints 设备→device, 地址池/池→ip_pool,
// 电路→circuit.
func TestSuggestType(t *testing.T) {
	cases := []struct {
		sheet string
		want  domain.ResourceType
		ok    bool
	}{
		{"设备清单", domain.ResourceTypeDevice, true},
		{"设备", domain.ResourceTypeDevice, true},
		{"地址池", domain.ResourceTypeIPPool, true},
		{"池", domain.ResourceTypeIPPool, true},
		{"IP地址池", domain.ResourceTypeIPPool, true},
		{"电路", domain.ResourceTypeCircuit, true},
		{"电路清单", domain.ResourceTypeCircuit, true},
		{"备注", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := SuggestType(tc.sheet)
		if got != tc.want || ok != tc.ok {
			t.Errorf("SuggestType(%q) = (%q, %v), want (%q, %v)", tc.sheet, got, ok, tc.want, tc.ok)
		}
	}
}

// deviceSheetMapping builds a complete, valid device sheet mapping.
func deviceSheetMapping() SheetMapping {
	return SheetMapping{
		Sheet:        testfixtures.DeviceSheetName,
		Action:       ActionImport,
		ResourceType: domain.ResourceTypeDevice,
		KeyColumns:   []string{"device_code"},
		ColumnMapping: map[string]string{
			"device_code": "设备编码",
			"device_name": "设备名称",
			"city":        "城市",
			"vendor":      "厂商",
			"model":       "型号",
			"mgmt_ip":     "管理IP",
			"status":      "状态",
		},
	}
}

// testHeaders is the header set of the three synthetic workbooks.
var testHeaders = map[string][]string{
	testfixtures.DeviceSheetName:  testfixtures.DeviceHeaders,
	testfixtures.IPPoolSheetName:  testfixtures.IPPoolHeaders,
	testfixtures.CircuitSheetName: testfixtures.CircuitHeaders,
}

// TestMappingValidate covers the mapping validation contract: happy paths,
// unknown sheet, missing natural key, unmapped required field, header not
// in sheet, invalid target field and structural errors.
func TestMappingValidate(t *testing.T) {
	ippoolMapping := SheetMapping{
		Sheet:        testfixtures.IPPoolSheetName,
		Action:       ActionImport,
		ResourceType: domain.ResourceTypeIPPool,
		KeyColumns:   []string{"pool_name"},
		ColumnMapping: map[string]string{
			"pool_name":       "地址池名称",
			"cidr":            "CIDR",
			"gateway":         "网关",
			"vlan_id":         "VLAN",
			"assigned_device": "归属设备",
		},
	}

	cases := []struct {
		name      string
		mutate    func(m *Mapping)
		wantErr   bool
		wantSheet string
		wantField string
	}{
		{
			name: "happy path device",
			mutate: func(m *Mapping) {
				m.Sheets = append(m.Sheets, deviceSheetMapping())
			},
		},
		{
			name: "happy path ip_pool without optional status",
			mutate: func(m *Mapping) {
				m.Sheets = append(m.Sheets, ippoolMapping)
			},
		},
		{
			name: "skip action needs nothing else",
			mutate: func(m *Mapping) {
				m.Sheets = append(m.Sheets, SheetMapping{Sheet: "随便什么表", Action: ActionSkip})
			},
		},
		{
			name: "unknown sheet",
			mutate: func(m *Mapping) {
				sm := deviceSheetMapping()
				sm.Sheet = "不存在的表"
				m.Sheets = append(m.Sheets, sm)
			},
			wantErr:   true,
			wantSheet: "不存在的表",
		},
		{
			name: "missing natural key mapping",
			mutate: func(m *Mapping) {
				sm := deviceSheetMapping()
				delete(sm.ColumnMapping, "device_code")
				m.Sheets = append(m.Sheets, sm)
			},
			wantErr:   true,
			wantSheet: testfixtures.DeviceSheetName,
			wantField: "device_code",
		},
		{
			name: "unmapped required field",
			mutate: func(m *Mapping) {
				sm := deviceSheetMapping()
				delete(sm.ColumnMapping, "device_name")
				m.Sheets = append(m.Sheets, sm)
			},
			wantErr:   true,
			wantSheet: testfixtures.DeviceSheetName,
			wantField: "device_name",
		},
		{
			name: "unmapped required ref field of circuit",
			mutate: func(m *Mapping) {
				sm := SheetMapping{
					Sheet:        testfixtures.CircuitSheetName,
					Action:       ActionImport,
					ResourceType: domain.ResourceTypeCircuit,
					KeyColumns:   []string{"circuit_code"},
					ColumnMapping: map[string]string{
						"circuit_code": "电路编号",
						"z_end_device": "Z端设备",
					},
				}
				m.Sheets = append(m.Sheets, sm)
			},
			wantErr:   true,
			wantSheet: testfixtures.CircuitSheetName,
			wantField: "a_end_device",
		},
		{
			name: "source header not in sheet",
			mutate: func(m *Mapping) {
				sm := deviceSheetMapping()
				sm.ColumnMapping["city"] = "城市名"
				m.Sheets = append(m.Sheets, sm)
			},
			wantErr:   true,
			wantSheet: testfixtures.DeviceSheetName,
			wantField: "city",
		},
		{
			name: "invalid target field",
			mutate: func(m *Mapping) {
				sm := deviceSheetMapping()
				sm.ColumnMapping["bogus"] = "城市"
				m.Sheets = append(m.Sheets, sm)
			},
			wantErr:   true,
			wantSheet: testfixtures.DeviceSheetName,
			wantField: "bogus",
		},
		{
			name: "unknown resource type",
			mutate: func(m *Mapping) {
				sm := deviceSheetMapping()
				sm.ResourceType = domain.ResourceType("router")
				m.Sheets = append(m.Sheets, sm)
			},
			wantErr:   true,
			wantSheet: testfixtures.DeviceSheetName,
			wantField: "router",
		},
		{
			name: "key columns must be the natural key",
			mutate: func(m *Mapping) {
				sm := deviceSheetMapping()
				sm.KeyColumns = []string{"device_name"}
				m.Sheets = append(m.Sheets, sm)
			},
			wantErr:   true,
			wantSheet: testfixtures.DeviceSheetName,
			wantField: "keyColumns",
		},
		{
			name: "invalid action",
			mutate: func(m *Mapping) {
				sm := deviceSheetMapping()
				sm.Action = "importall"
				m.Sheets = append(m.Sheets, sm)
			},
			wantErr:   true,
			wantSheet: testfixtures.DeviceSheetName,
		},
		{
			name: "duplicate sheet entries",
			mutate: func(m *Mapping) {
				m.Sheets = append(m.Sheets, deviceSheetMapping(), deviceSheetMapping())
			},
			wantErr:   true,
			wantSheet: testfixtures.DeviceSheetName,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := Mapping{}
			tc.mutate(&m)
			err := Validate(m, testHeaders)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("Validate: unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Validate: want error, got nil")
			}
			var fe *FieldError
			if !errors.As(err, &fe) {
				t.Fatalf("Validate error %v is not *FieldError", err)
			}
			if fe.Sheet != tc.wantSheet {
				t.Errorf("FieldError.Sheet = %q, want %q", fe.Sheet, tc.wantSheet)
			}
			if fe.Field != tc.wantField {
				t.Errorf("FieldError.Field = %q, want %q", fe.Field, tc.wantField)
			}
		})
	}

	// Empty mapping is valid (all sheets are skipped implicitly).
	if err := Validate(Mapping{}, testHeaders); err != nil {
		t.Errorf("Validate(empty) = %v, want nil", err)
	}
}

// TestMappingJSON round-trips the mapping model through its JSON form.
func TestMappingJSON(t *testing.T) {
	m := Mapping{Sheets: []SheetMapping{
		deviceSheetMapping(),
		{Sheet: "备注", Action: ActionSkip},
	}}
	data, err := Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	back, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !reflect.DeepEqual(m, back) {
		t.Errorf("round-trip mismatch:\ngot  %+v\nwant %+v", back, m)
	}
	if _, err := Parse([]byte("{not json")); err == nil {
		t.Error("Parse of invalid JSON must fail")
	}
	// The canonical §8 shape: keys are target fields, values are headers.
	var probe map[string]any
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	sheets := probe["sheets"].([]any)
	first := sheets[0].(map[string]any)
	if first["sheet"] != testfixtures.DeviceSheetName || first["action"] != ActionImport ||
		first["resourceType"] != domain.ResourceTypeDevice.String() {
		t.Errorf("unexpected sheet JSON: %v", first)
	}
	cm := first["columnMapping"].(map[string]any)
	if cm["device_code"] != "设备编码" {
		t.Errorf("columnMapping[device_code] = %v, want 设备编码", cm["device_code"])
	}
}

// TestSuggestMappingOfSyntheticSheets checks the prefilled mapping of the
// three synthetic workbooks: type detected, every column mapped, natural key
// as keyColumns, and the mapping passes Validate.
func TestSuggestMappingOfSyntheticSheets(t *testing.T) {
	m := SuggestMapping([]SheetInfo{
		{Name: testfixtures.DeviceSheetName, Headers: testfixtures.DeviceHeaders},
		{Name: testfixtures.IPPoolSheetName, Headers: testfixtures.IPPoolHeaders},
		{Name: testfixtures.CircuitSheetName, Headers: testfixtures.CircuitHeaders},
	})
	if len(m.Sheets) != 3 {
		t.Fatalf("SuggestMapping produced %d sheets, want 3", len(m.Sheets))
	}
	for i, wantType := range []domain.ResourceType{
		domain.ResourceTypeDevice, domain.ResourceTypeIPPool, domain.ResourceTypeCircuit,
	} {
		sm := m.Sheets[i]
		if sm.Action != ActionImport || sm.ResourceType != wantType {
			t.Errorf("sheet %d = %q/%q, want import/%q", i, sm.Action, sm.ResourceType, wantType)
		}
		spec, _ := SpecByType(wantType)
		key := spec.NaturalKeyNames()
		if !reflect.DeepEqual(sm.KeyColumns, key) {
			t.Errorf("sheet %d keyColumns = %v, want %v", i, sm.KeyColumns, key)
		}
		if _, ok := sm.ColumnMapping[key[0]]; !ok {
			t.Errorf("sheet %d does not map natural key %s", i, key[0])
		}
		for _, f := range spec.Fields {
			if f.Required {
				if _, ok := sm.ColumnMapping[f.Name]; !ok {
					t.Errorf("sheet %d: suggested mapping misses required field %s", i, f.Name)
				}
			}
		}
	}
	if err := Validate(m, testHeaders); err != nil {
		t.Errorf("suggested mapping does not validate: %v", err)
	}
	// Sheets without a type hint get action skip.
	m2 := SuggestMapping([]SheetInfo{{Name: "备注", Headers: []string{"A", "B"}}})
	if m2.Sheets[0].Action != ActionSkip || m2.Sheets[0].ColumnMapping != nil {
		t.Errorf("untyped sheet suggestion = %+v, want skip", m2.Sheets[0])
	}
}

// TestFieldKindCheckFormat covers the E004 parse-level checks.
func TestFieldKindCheckFormat(t *testing.T) {
	cases := []struct {
		kind  FieldKind
		value string
		ok    bool
	}{
		{KindIPv4, "10.10.10.5", true},
		{KindIPv4, "192.168.1.300", false},
		{KindIPv4, "10.20.0", false},
		{KindIPv4, "::1", false},
		{KindIPv4, "", false},
		{KindCIDR, "10.30.1.0/24", true},
		{KindCIDR, "10.30.1.128/25", true},
		{KindCIDR, "10.20.0/24", false},
		{KindCIDR, "10.40.1.0/33", false},
		{KindCIDR, "::/0", false},
		{KindInt, "100", true},
		{KindInt, "abc", false},
		{KindInt, "N/A", false},
		{KindString, "anything", true},
		{KindEnum, "未知", true},      // enums are not format-checked here (W002)
		{KindRef, "DEV-9999", true}, // refs are resolved against the DB (E003)
	}
	for _, tc := range cases {
		_, ok := tc.kind.CheckFormat(tc.value)
		if ok != tc.ok {
			t.Errorf("CheckFormat(%q, %q) ok = %v, want %v", tc.kind, tc.value, ok, tc.ok)
		}
	}
}

// TestCheckFormatRanges covers the field-level integer range constraints of
// PROJECT_BRIEF §6 (vlan_id 1..4094, bandwidth_mbps > 0) that turn
// out-of-range values into E004 failures. Values are synthetic — the
// committed fixtures only seed parse failures, so no new seeds are needed.
func TestCheckFormatRanges(t *testing.T) {
	poolSpec, ok := SpecByType(domain.ResourceTypeIPPool)
	if !ok {
		t.Fatal("ip_pool spec missing")
	}
	vlan, ok := poolSpec.Field("vlan_id")
	if !ok {
		t.Fatal("vlan_id field missing")
	}
	circuitSpec, ok := SpecByType(domain.ResourceTypeCircuit)
	if !ok {
		t.Fatal("circuit spec missing")
	}
	bandwidth, ok := circuitSpec.Field("bandwidth_mbps")
	if !ok {
		t.Fatal("bandwidth_mbps field missing")
	}

	cases := []struct {
		field FieldSpec
		value string
		ok    bool
	}{
		{vlan, "4095", false},
		{vlan, "4094", true},
		{vlan, "0", false},
		{vlan, "1", true},
		{vlan, "-1", false},
		{vlan, "abc", false},
		{vlan, "", false}, // kind-level parse rejects empty typed values; the importer skips empties before checking
		{bandwidth, "0", false},
		{bandwidth, "-5", false},
		{bandwidth, "100", true},
		{bandwidth, "10000", true},
		{bandwidth, "N/A", false},
	}
	for _, tc := range cases {
		reason, ok := tc.field.CheckFormat(tc.value)
		if ok != tc.ok {
			t.Errorf("%s.CheckFormat(%q) ok = %v, want %v (reason %q)",
				tc.field.Name, tc.value, ok, tc.ok, reason)
		}
		if !ok && reason == "" {
			t.Errorf("%s.CheckFormat(%q) failed without a reason", tc.field.Name, tc.value)
		}
	}

	// Non-int fields ignore the range machinery: the string field of device
	// accepts any text and delegates only to the kind-level check.
	deviceSpec, _ := SpecByType(domain.ResourceTypeDevice)
	name, _ := deviceSpec.Field("device_name")
	if reason, ok := name.CheckFormat("VLAN 4095"); !ok || reason != "" {
		t.Errorf("device_name.CheckFormat must accept arbitrary text, got ok=%v reason=%q", ok, reason)
	}
}

// TestNameField checks the name-field derivation of the schemas (port pins
// its display name to port_name — device_name is the key's scope component).
func TestNameField(t *testing.T) {
	for _, tc := range []struct {
		t    domain.ResourceType
		want string
	}{
		{domain.ResourceTypeDevice, "device_name"},
		{domain.ResourceTypeIPPool, "pool_name"},
		{domain.ResourceTypeCircuit, "circuit_code"},
		{domain.ResourceTypePort, "port_name"},
	} {
		spec, ok := SpecByType(tc.t)
		if !ok {
			t.Fatalf("SpecByType(%s) missing", tc.t)
		}
		if got := spec.NameField(); got != tc.want {
			t.Errorf("NameField(%s) = %q, want %q", tc.t, got, tc.want)
		}
	}
}

// ---- geographic coordinate fields (geo pipeline) ----

// TestGeoFieldSpecs pins the three optional device geo fields: presence,
// kinds, labels, synonym ladders (so the suggestion engine picks up 经度/
// 纬度/经纬度 columns), and that NO other schema carries them (geo is a
// device-only concern).
func TestGeoFieldSpecs(t *testing.T) {
	spec, ok := SpecByType(domain.ResourceTypeDevice)
	if !ok {
		t.Fatal("device spec missing")
	}
	cases := []struct {
		name     string
		label    string
		kind     FieldKind
		synonyms []string
	}{
		{"geo_lat", "纬度", KindLat, []string{"纬度", "lat", "LAT", "GPS纬度", "北纬"}},
		{"geo_lng", "经度", KindLng, []string{"经度", "lng", "lon", "LON", "GPS经度", "东经"}},
		{"geo", "经纬度", KindGeo, []string{"经纬度", "装机经纬度", "坐标", "GPS坐标", "A端装机经纬度", "位置"}},
	}
	for _, tc := range cases {
		f, found := spec.Field(tc.name)
		if !found {
			t.Fatalf("device spec misses optional field %s", tc.name)
		}
		if f.Label != tc.label || f.Kind != tc.kind {
			t.Errorf("%s = label %q kind %q, want %q/%q", tc.name, f.Label, f.Kind, tc.label, tc.kind)
		}
		if f.Required || f.NaturalKey {
			t.Errorf("%s must be optional and non-key (Required=%v NaturalKey=%v)", tc.name, f.Required, f.NaturalKey)
		}
		if !reflect.DeepEqual(f.Synonyms, tc.synonyms) {
			t.Errorf("%s synonyms = %v, want %v", tc.name, f.Synonyms, tc.synonyms)
		}
	}
	// The geo fields sit AFTER status, keep every required field in front and
	// do not disturb the natural key.
	if f, _ := spec.Field("status"); f.Kind != KindEnum {
		t.Errorf("status field must stay the enum before the geo block, got %q", f.Kind)
	}
	for _, other := range []domain.ResourceType{domain.ResourceTypeIPPool, domain.ResourceTypeCircuit} {
		s, _ := SpecByType(other)
		for _, name := range []string{"geo_lat", "geo_lng", "geo"} {
			if _, found := s.Field(name); found {
				t.Errorf("%s spec must not carry the device-only field %s", other, name)
			}
		}
	}
	// The synonym ladder reaches the suggestion engine: 经度/纬度/经纬度
	// headers of a 设备 sheet suggest the geo fields at synonym confidence.
	sug := Suggest("设备", []string{"经度", "纬度", "经纬度", "位置"})
	want := []string{"geo_lng", "geo_lat", "geo", "geo"}
	for i, s := range sug {
		if s.Field != want[i] || s.Confidence != ConfidenceSynonym {
			t.Errorf("Suggest(设备, %q) = %s@%v, want %s@%v", sug[i].Header, s.Field, s.Confidence, want[i], ConfidenceSynonym)
		}
	}
}

// TestGeoFieldKindCheckFormat covers the E004 parse/range checks of the
// three geo kinds, including the Chinese failure reasons.
func TestGeoFieldKindCheckFormat(t *testing.T) {
	cases := []struct {
		kind   FieldKind
		value  string
		ok     bool
		reason string // expected reason prefix when !ok
	}{
		{KindLat, "45.773", true, ""},
		{KindLat, "-90", true, ""},
		{KindLat, "90", true, ""},
		{KindLat, "90.0001", false, "不是合法的纬度（-90 到 90 的数字）"},
		{KindLat, "-90.5", false, "不是合法的纬度（-90 到 90 的数字）"},
		{KindLat, "abc", false, "不是合法的纬度（-90 到 90 的数字）"},
		{KindLng, "126.636", true, ""},
		{KindLng, "-180", true, ""},
		{KindLng, "180", true, ""},
		{KindLng, "180.1", false, "不是合法的经度（-180 到 180 的数字）"},
		{KindLng, "N/A", false, "不是合法的经度（-180 到 180 的数字）"},
		{KindGeo, "126.63,45.75", true, ""},
		{KindGeo, "45.75,126.63", true, ""},
		{KindGeo, "126.63，45.75", true, ""}, // Chinese comma
		{KindGeo, "126.63 45.75", true, ""}, // space separator
		{KindGeo, "126.63；45.75", true, ""}, // Chinese semicolon
		{KindGeo, "-74.0,40.7", true, ""},   // foreign coordinates pass
		{KindGeo, "126.63,45.75,0", false, "经纬度应为两个数字（如 126.63,45.75）"},
		{KindGeo, "126.63", false, "经纬度应为两个数字（如 126.63,45.75）"},
		{KindGeo, "abc,45.75", false, "经纬度应为两个数字（如 126.63,45.75）"},
		{KindGeo, "100,100", false, "不是合法的经纬度"}, // both outside [-90,90]
		{KindGeo, "200,45", false, "不是合法的经纬度"},  // 200 can be neither lat nor lng
		{KindGeo, "", false, "经纬度应为两个数字（如 126.63,45.75）"},
	}
	for _, tc := range cases {
		reason, ok := tc.kind.CheckFormat(tc.value)
		if ok != tc.ok {
			t.Errorf("CheckFormat(%q, %q) ok = %v, want %v (reason %q)", tc.kind, tc.value, ok, tc.ok, reason)
			continue
		}
		if !ok && !strings.HasPrefix(reason, tc.reason) {
			t.Errorf("CheckFormat(%q, %q) reason = %q, want prefix %q", tc.kind, tc.value, reason, tc.reason)
		}
	}
	// Field-level CheckFormat (range machinery wrapper) delegates to the kind
	// check for the float fields — IntMin/IntMax stay untouched.
	spec, _ := SpecByType(domain.ResourceTypeDevice)
	lat, _ := spec.Field("geo_lat")
	if _, ok := lat.CheckFormat("45.773"); !ok {
		t.Errorf("geo_lat.CheckFormat(45.773) must pass")
	}
	if reason, ok := lat.CheckFormat("91"); ok || reason == "" {
		t.Errorf("geo_lat.CheckFormat(91) = ok %v reason %q, want a failed check with reason", ok, reason)
	}
}

// TestSplitGeoLatLng covers the China-bounds heuristic of the combined
// 经纬度 split: both component orders, all separators, foreign coordinates
// and the swap rule for pairs whose second component cannot be a latitude.
func TestSplitGeoLatLng(t *testing.T) {
	cases := []struct {
		value   string
		lat     float64
		lng     float64
		ok      bool
		comment string
	}{
		{"126.63,45.75", 45.75, 126.63, true, "Gaode/Baidu (经度,纬度) order — 哈尔滨"},
		{"45.75,126.63", 45.75, 126.63, true, "(纬度,经度) order — same site"},
		{"126.636，45.773", 45.773, 126.636, true, "Chinese comma"},
		{"126.61 45.721", 45.721, 126.61, true, "space separator"},
		{"124.114;50.424", 50.424, 124.114, true, "semicolon separator"},
		{"124.114；50.424", 50.424, 124.114, true, "Chinese semicolon"},
		{"122.536,52.972", 52.972, 122.536, true, "漠河 — lat beyond 54: China-lng window still first"},
		{"-74.0,40.7", 40.7, -74.0, true, "New York as (lng,lat) — foreign, default interpretation"},
		{"40.7,-74.0", -74.0, 40.7, true, "ambiguous pair (both in both windows): default lng=f1"},
		{"45,150", 45, 150, true, "lat<90/lng>90: swapped interpretation (Alaska-ish)"},
		{"-50,100", -50, 100, true, "southern latitude + lng>90: swapped interpretation"},
		{"126.63,abc", 0, 0, false, "unparseable second component"},
		{"abc", 0, 0, false, "not a pair"},
		{"", 0, 0, false, "empty"},
	}
	for _, tc := range cases {
		lat, lng, ok := SplitGeoLatLng(tc.value)
		if ok != tc.ok {
			t.Errorf("SplitGeoLatLng(%q) ok = %v, want %v (%s)", tc.value, ok, tc.ok, tc.comment)
			continue
		}
		if !ok {
			continue
		}
		if lat != tc.lat || lng != tc.lng {
			t.Errorf("SplitGeoLatLng(%q) = (%v, %v), want (%v, %v) — %s", tc.value, lat, lng, tc.lat, tc.lng, tc.comment)
		}
		if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
			t.Errorf("SplitGeoLatLng(%q) produced out-of-range lat/lng (%v, %v)", tc.value, lat, lng)
		}
	}
	// The formatting half: plain decimal strings, no exponent, no padding.
	if got := FormatGeoFloat(45.773); got != "45.773" {
		t.Errorf("FormatGeoFloat(45.773) = %q, want 45.773", got)
	}
	if got := FormatGeoFloat(126.6); got != "126.6" {
		t.Errorf("FormatGeoFloat(126.6) = %q, want 126.6", got)
	}
	if got := FormatGeoFloat(-50); got != "-50" {
		t.Errorf("FormatGeoFloat(-50) = %q, want -50", got)
	}
}

// ---- port resource type (schema v7) ----

// portSheetName is a sheet name whose 端口 hint suggests the port type.
const portSheetName = "端口资源表"

// portHeadersA are the leading columns of the real telecom sheet's header
// row A (samples/telecom-port-multi-header.xlsx).
var portHeadersA = []string{"地市", "区县", "设备名称", "设备ip", "端口", "端口ip", "端口带宽", "端口类型", "端口描述", "厂家", "型号", "日期"}

// portSheetMapping builds a complete, valid port sheet mapping: composite
// natural key (device_name, port_name) and the real header row A columns.
func portSheetMapping() SheetMapping {
	return SheetMapping{
		Sheet:        portSheetName,
		Action:       ActionImport,
		ResourceType: domain.ResourceTypePort,
		KeyColumns:   []string{"device_name", "port_name"},
		ColumnMapping: map[string]string{
			"device_name":    "设备名称",
			"port_name":      "端口",
			"device_ip":      "设备ip",
			"port_desc":      "端口描述",
			"port_type":      "端口类型",
			"bandwidth_mbps": "端口带宽",
			"city":           "地市",
			"vendor":         "厂家",
			"model":          "型号",
			"record_date":    "日期",
		},
	}
}

// TestPortSuggest covers the suggestion ladder on a typed 端口 sheet: every
// confidence tier (exact 1.0, normalized 0.9, synonym 0.75) plus the real
// header row A columns and the traffic-metric columns that must yield no
// suggestion.
func TestPortSuggest(t *testing.T) {
	cases := []struct {
		name     string
		sheet    string
		header   string
		wantFld  string
		wantConf float64
	}{
		{"exact field name", portSheetName, "device_ip", "device_ip", 1.0},
		{"normalized case field name", portSheetName, "DEVICE_IP", "device_ip", 0.9},
		{"normalized spaces field name", portSheetName, " port_name ", "port_name", 0.9},
		{"header A device name", portSheetName, "设备名称", "device_name", 0.75},
		{"header B device name", portSheetName, "网元名称", "device_name", 0.75},
		{"short device name synonym", portSheetName, "设备名", "device_name", 0.75},
		{"header A device ip", portSheetName, "设备ip", "device_ip", 0.75},
		{"header B device ip", portSheetName, "设备IP地址", "device_ip", 0.75},
		{"header A port", portSheetName, "端口", "port_name", 0.75},
		{"header B port name", portSheetName, "端口名称", "port_name", 0.75},
		{"header A bandwidth", portSheetName, "端口带宽", "bandwidth_mbps", 0.75},
		{"header B bandwidth", portSheetName, "端口速率(M)", "bandwidth_mbps", 0.75},
		{"header A port type", portSheetName, "端口类型", "port_type", 0.75},
		{"header A port desc", portSheetName, "端口描述", "port_desc", 0.75},
		{"header A vendor", portSheetName, "厂家", "vendor", 0.75},
		{"header A model", portSheetName, "型号", "model", 0.75},
		{"header A city", portSheetName, "地市", "city", 0.75},
		{"header A date", portSheetName, "日期", "record_date", 0.75},
		{"traffic metric column has no suggestion", portSheetName, "接收速率ipv6", "", 0},
		{"device-only field not suggested on port sheet", portSheetName, "设备编码", "", 0},
		{"untyped sheet falls through to port field", "Sheet1", "端口名称", "port_name", 0.75},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Suggest(tc.sheet, []string{tc.header})
			if len(got) != 1 {
				t.Fatalf("Suggest returned %d suggestions, want 1", len(got))
			}
			if got[0].Field != tc.wantFld || got[0].Confidence != tc.wantConf {
				t.Errorf("Suggest(%q, %q) = %q/%v, want %q/%v",
					tc.sheet, tc.header, got[0].Field, got[0].Confidence, tc.wantFld, tc.wantConf)
			}
		})
	}
}

// TestSuggestTypePort pins the port sheet hint and the Specs order rule:
// 端口 suggests port unless an earlier hint (设备) matches first.
func TestSuggestTypePort(t *testing.T) {
	cases := []struct {
		sheet string
		want  domain.ResourceType
		ok    bool
	}{
		{portSheetName, domain.ResourceTypePort, true},
		{"端口", domain.ResourceTypePort, true},
		{"端口流量监控表", domain.ResourceTypePort, true},
		{"设备端口表", domain.ResourceTypeDevice, true}, // device hint wins (Specs order)
		{"Sheet1", "", false},                      // real sheet name: no hint
	}
	for _, tc := range cases {
		got, ok := SuggestType(tc.sheet)
		if got != tc.want || ok != tc.ok {
			t.Errorf("SuggestType(%q) = (%q, %v), want (%q, %v)", tc.sheet, got, ok, tc.want, tc.ok)
		}
	}
}

// TestPortCompositeKeyValidate pins the composite natural key of the port
// type: keyColumns must be EXACTLY [device_name port_name] — a single key, a
// swapped order or source-header names are all rejected — and the valid
// mapping passes with the required key fields mapped.
func TestPortCompositeKeyValidate(t *testing.T) {
	headers := map[string][]string{}
	for k, v := range testHeaders {
		headers[k] = v
	}
	headers[portSheetName] = portHeadersA

	cases := []struct {
		name      string
		mutate    func(m *Mapping)
		wantErr   bool
		wantField string
	}{
		{
			name: "happy path with composite key",
			mutate: func(m *Mapping) {
				m.Sheets = append(m.Sheets, portSheetMapping())
			},
		},
		{
			name: "single key column is rejected",
			mutate: func(m *Mapping) {
				sm := portSheetMapping()
				sm.KeyColumns = []string{"device_name"}
				m.Sheets = append(m.Sheets, sm)
			},
			wantErr:   true,
			wantField: "keyColumns",
		},
		{
			name: "swapped key order is rejected",
			mutate: func(m *Mapping) {
				sm := portSheetMapping()
				sm.KeyColumns = []string{"port_name", "device_name"}
				m.Sheets = append(m.Sheets, sm)
			},
			wantErr:   true,
			wantField: "keyColumns",
		},
		{
			name: "source headers as key columns are rejected",
			mutate: func(m *Mapping) {
				sm := portSheetMapping()
				sm.KeyColumns = []string{"设备名称", "端口"}
				m.Sheets = append(m.Sheets, sm)
			},
			wantErr:   true,
			wantField: "keyColumns",
		},
		{
			name: "unmapped required key field device_name",
			mutate: func(m *Mapping) {
				sm := portSheetMapping()
				delete(sm.ColumnMapping, "device_name")
				m.Sheets = append(m.Sheets, sm)
			},
			wantErr:   true,
			wantField: "device_name",
		},
		{
			name: "unmapped required value field device_ip",
			mutate: func(m *Mapping) {
				sm := portSheetMapping()
				delete(sm.ColumnMapping, "device_ip")
				m.Sheets = append(m.Sheets, sm)
			},
			wantErr:   true,
			wantField: "device_ip",
		},
		{
			name: "unmapped required key field port_name",
			mutate: func(m *Mapping) {
				sm := portSheetMapping()
				delete(sm.ColumnMapping, "port_name")
				m.Sheets = append(m.Sheets, sm)
			},
			wantErr:   true,
			wantField: "port_name",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := Mapping{}
			tc.mutate(&m)
			err := Validate(m, headers)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("Validate: unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Validate: want error, got nil")
			}
			var fe *FieldError
			if !errors.As(err, &fe) {
				t.Fatalf("Validate error %v is not *FieldError", err)
			}
			if fe.Field != tc.wantField {
				t.Errorf("FieldError.Field = %q, want %q", fe.Field, tc.wantField)
			}
		})
	}
}

// ---- base_station (conditional natural key, v0.4 W2-D) ----

// baseStationSheetName is a sheet name whose 基站 hint suggests the
// base_station type (the real sheet family: 无线基站…表).
const baseStationSheetName = "无线基站表"

// baseStationHeaders are the REAL column layout of the base-station export
// (samples/telecom-base-station.xlsx, owner-provided session paste): 14
// business columns, NO 地市 column.
var baseStationHeaders = []string{"基站类型", "无线基站名称", "基站编码", "电联共享", "归属A IP", "环", "链", "归属A名称", "归属A端口", "网关IP", "基站IP", "描述", "工单号", "备注"}

// baseStationHeadersWithCity adds the 地市 scope column a future sheet may
// carry (the fallback key then becomes 地市|无线基站名称 instead of the bare
// name).
var baseStationHeadersWithCity = append([]string{"地市"}, baseStationHeaders...)

// baseStationSheetMapping builds a complete, valid base_station sheet
// mapping against the real column layout: the PRIMARY key column is
// base_station_code (the conditional fallback is spec-driven, not a mapping
// input), the required field base_station_name is mapped.
func baseStationSheetMapping() SheetMapping {
	return SheetMapping{
		Sheet:        baseStationSheetName,
		Action:       ActionImport,
		ResourceType: domain.ResourceTypeBaseStation,
		KeyColumns:   []string{"base_station_code"},
		ColumnMapping: map[string]string{
			"base_station_type": "基站类型",
			"base_station_name": "无线基站名称",
			"base_station_code": "基站编码",
			"sharing":           "电联共享",
			"uplink_a_ip":       "归属A IP",
			"ring":              "环",
			"chain":             "链",
			"uplink_a_name":     "归属A名称",
			"uplink_a_port":     "归属A端口",
			"gateway_ip":        "网关IP",
			"base_station_ip":   "基站IP",
			"description":       "描述",
			"work_order":        "工单号",
			"remark":            "备注",
		},
	}
}

// TestBaseStationSpec pins the base_station schema: the conditional natural
// key (optional code primary + [city, base_station_name] fallback), the
// display-name override, the Kinds of the three IP columns (base_station_ip
// is a string — the real values carry /30 masks — while uplink_a_ip and
// gateway_ip stay strict IPv4), and — for EVERY spec of the registry — that
// KeyFallbackFields reference existing fields (a typo in a fallback field
// name would silently degrade every key of the type; this is the load-bearing
// validation the importer cannot perform at runtime).
func TestBaseStationSpec(t *testing.T) {
	spec, ok := SpecByType(domain.ResourceTypeBaseStation)
	if !ok {
		t.Fatal("base_station spec missing from Specs")
	}
	if spec.Type != domain.ResourceTypeBaseStation || spec.SheetHint != "基站" {
		t.Errorf("spec identity = %s/%q, want base_station/基站", spec.Type, spec.SheetHint)
	}
	if got := spec.NameField(); got != "base_station_name" {
		t.Errorf("NameField(base_station) = %q, want base_station_name (override)", got)
	}
	if !reflect.DeepEqual(spec.KeyFallbackFields, []string{"city", "base_station_name"}) {
		t.Errorf("KeyFallbackFields = %v, want [city base_station_name]", spec.KeyFallbackFields)
	}
	if got := spec.NaturalKeyNames(); !reflect.DeepEqual(got, []string{"base_station_code"}) {
		t.Errorf("NaturalKeyNames = %v, want [base_station_code]", got)
	}
	if f, _ := spec.Field("base_station_code"); f.Required {
		t.Error("base_station_code must NOT be Required (the real sheet's code column is empty; empty code degrades the key, W004)")
	}
	if f, _ := spec.Field("base_station_name"); !f.Required || f.NaturalKey {
		t.Error("base_station_name must be Required and non-key (the E002 anchor and fallback component)")
	}
	for _, tc := range []struct {
		field string
		want  FieldKind
	}{
		{"base_station_ip", KindString}, // real values carry /30 masks — NOT KindIPv4
		{"uplink_a_ip", KindIPv4},
		{"gateway_ip", KindIPv4},
	} {
		f, found := spec.Field(tc.field)
		if !found {
			t.Fatalf("spec misses field %s", tc.field)
		}
		if f.Kind != tc.want {
			t.Errorf("%s kind = %q, want %q", tc.field, f.Kind, tc.want)
		}
	}
	// No ref fields: the station sheet is a self-contained export, like port.
	for _, f := range spec.Fields {
		if f.Kind == KindRef {
			t.Errorf("base_station must not carry ref fields (found %s)", f.Name)
		}
	}
	// Registry-wide KeyFallbackFields validation: every referenced field
	// must exist in the same spec (order is the fallback join order).
	for _, s := range Specs {
		for _, name := range s.KeyFallbackFields {
			if _, found := s.Field(name); !found {
				t.Errorf("spec %s: KeyFallbackFields references unknown field %q", s.Type, name)
			}
		}
	}
}

// TestSuggestTypeBaseStation pins the 基站 sheet hint and the Specs order
// rule (earlier hints win).
func TestSuggestTypeBaseStation(t *testing.T) {
	cases := []struct {
		sheet string
		want  domain.ResourceType
		ok    bool
	}{
		{baseStationSheetName, domain.ResourceTypeBaseStation, true},
		{"基站", domain.ResourceTypeBaseStation, true},
		{"无线基站资源表", domain.ResourceTypeBaseStation, true},
		{"Sheet1", "", false},
	}
	for _, tc := range cases {
		got, ok := SuggestType(tc.sheet)
		if got != tc.want || ok != tc.ok {
			t.Errorf("SuggestType(%q) = (%q, %v), want (%q, %v)", tc.sheet, got, ok, tc.want, tc.ok)
		}
	}
}

// TestBaseStationSuggest covers the suggestion ladder on a typed 基站 sheet:
// the real column layout maps onto the base_station fields at synonym
// confidence, and a sheet WITH the 地市 column also suggests city (the
// reserved scope field).
func TestBaseStationSuggest(t *testing.T) {
	// Every real header maps to a field at synonym confidence.
	sug := Suggest(baseStationSheetName, baseStationHeaders)
	want := []string{
		"base_station_type", "base_station_name", "base_station_code", "sharing",
		"uplink_a_ip", "ring", "chain", "uplink_a_name", "uplink_a_port",
		"gateway_ip", "base_station_ip", "description", "work_order", "remark",
	}
	if len(sug) != len(want) {
		t.Fatalf("Suggest returned %d suggestions, want %d", len(sug), len(want))
	}
	for i, w := range want {
		if sug[i].Field != w || sug[i].Confidence != ConfidenceSynonym {
			t.Errorf("suggestion[%d] = %s@%v, want %s@%v", i, sug[i].Field, sug[i].Confidence, w, ConfidenceSynonym)
		}
	}
	// Exact / normalized tiers and the reserved scope column.
	cases := []struct {
		name     string
		sheet    string
		header   string
		wantFld  string
		wantConf float64
	}{
		{"exact field name", baseStationSheetName, "base_station_code", "base_station_code", 1.0},
		{"normalized case field name", baseStationSheetName, "BASE_STATION_NAME", "base_station_name", 0.9},
		{"city scope column", "基站表-含地市", "地市", "city", 0.75},
		{"city alt synonym", "基站表-含地市", "城市", "city", 0.75},
		{"device-only field not suggested", baseStationSheetName, "管理IP", "", 0},
		{"port field not suggested", baseStationSheetName, "端口名称", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Suggest(tc.sheet, []string{tc.header})
			if len(got) != 1 {
				t.Fatalf("Suggest returned %d suggestions, want 1", len(got))
			}
			if got[0].Field != tc.wantFld || got[0].Confidence != tc.wantConf {
				t.Errorf("Suggest(%q, %q) = %q/%v, want %q/%v",
					tc.sheet, tc.header, got[0].Field, got[0].Confidence, tc.wantFld, tc.wantConf)
			}
		})
	}
}

// TestSuggestMappingBaseStation pins the prefill: a 基站 sheet gets action
// import, keyColumns = the primary natural key [base_station_code], every
// real column mapped — and the suggested mapping VALIDATES as-is (the
// required field is mapped by the synonym ladder; the optional code column
// is mapped too, so real empty-code rows reach the W004 path).
func TestSuggestMappingBaseStation(t *testing.T) {
	m := SuggestMapping([]SheetInfo{{Name: baseStationSheetName, Headers: baseStationHeaders}})
	if len(m.Sheets) != 1 {
		t.Fatalf("SuggestMapping returned %d sheets, want 1", len(m.Sheets))
	}
	sm := m.Sheets[0]
	if sm.Action != ActionImport || sm.ResourceType != domain.ResourceTypeBaseStation {
		t.Fatalf("suggested sheet = %s/%s, want import/base_station", sm.Action, sm.ResourceType)
	}
	if !reflect.DeepEqual(sm.KeyColumns, []string{"base_station_code"}) {
		t.Errorf("keyColumns = %v, want [base_station_code] (the primary key, not the fallback)", sm.KeyColumns)
	}
	if len(sm.ColumnMapping) != len(baseStationHeaders) {
		t.Errorf("columnMapping has %d entries, want %d (every real column)", len(sm.ColumnMapping), len(baseStationHeaders))
	}
	if err := Validate(m, map[string][]string{baseStationSheetName: baseStationHeaders}); err != nil {
		t.Errorf("suggested mapping must validate as-is: %v", err)
	}
}

// TestBaseStationKeyValidate pins the mapping contract of the conditional
// key: keyColumns stays the PRIMARY natural key [base_station_code] (the
// fallback is spec-driven, never a mapping input), the optional code column
// may be left unmapped (real sheets carry an empty code column), the
// required name column must be mapped, and city maps when the sheet carries
// the scope column.
func TestBaseStationKeyValidate(t *testing.T) {
	headers := map[string][]string{
		baseStationSheetName: baseStationHeaders,
		"基站表-含地市":            baseStationHeadersWithCity,
	}
	for k, v := range testHeaders {
		headers[k] = v
	}

	cases := []struct {
		name      string
		mutate    func(m *Mapping)
		wantErr   bool
		wantField string
	}{
		{
			name: "happy path with primary key column",
			mutate: func(m *Mapping) {
				m.Sheets = append(m.Sheets, baseStationSheetMapping())
			},
		},
		{
			name: "fallback fields as key columns are rejected",
			mutate: func(m *Mapping) {
				sm := baseStationSheetMapping()
				sm.KeyColumns = []string{"city", "base_station_name"}
				m.Sheets = append(m.Sheets, sm)
			},
			wantErr:   true,
			wantField: "keyColumns",
		},
		{
			name: "code column left unmapped is valid (optional, real empty-code sheets)",
			mutate: func(m *Mapping) {
				sm := baseStationSheetMapping()
				delete(sm.ColumnMapping, "base_station_code")
				m.Sheets = append(m.Sheets, sm)
			},
		},
		{
			name: "unmapped required name field is rejected",
			mutate: func(m *Mapping) {
				sm := baseStationSheetMapping()
				delete(sm.ColumnMapping, "base_station_name")
				m.Sheets = append(m.Sheets, sm)
			},
			wantErr:   true,
			wantField: "base_station_name",
		},
		{
			name: "city scope column maps on a sheet that carries it",
			mutate: func(m *Mapping) {
				sm := baseStationSheetMapping()
				sm.Sheet = "基站表-含地市"
				sm.ColumnMapping["city"] = "地市"
				m.Sheets = append(m.Sheets, sm)
			},
		},
		{
			name: "unknown target field is rejected",
			mutate: func(m *Mapping) {
				sm := baseStationSheetMapping()
				sm.ColumnMapping["lng"] = "环"
				m.Sheets = append(m.Sheets, sm)
			},
			wantErr:   true,
			wantField: "lng",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := Mapping{}
			tc.mutate(&m)
			err := Validate(m, headers)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("Validate: unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Validate: want error, got nil")
			}
			var fe *FieldError
			if !errors.As(err, &fe) {
				t.Fatalf("Validate error %v is not *FieldError", err)
			}
			if fe.Field != tc.wantField {
				t.Errorf("FieldError.Field = %q, want %q", fe.Field, tc.wantField)
			}
		})
	}
}

// ---- site / machine_room (the 机房局站 export, v0.4 W3-D) ----

// siteMachineRoomSheetName is the REAL sheet name family of the owner's
// 机房局站 export (samples/telecom-site-machine-room.xlsx): the name carries
// BOTH SheetHints (机房 and 局站), so SuggestType resolves to whichever spec
// is registered first — site, per the container-first import story.
const siteMachineRoomSheetName = "机房局站表"

// machineRoomSheetName carries only the 机房 hint (the second pass of the
// same story, or a machine-room-only export).
const machineRoomSheetName = "机房表"

// siteMachineRoomHeaders is the REAL 8-column layout of the 机房局站 export
// (owner session paste): 地市/区县/设备名称/IP地址/机房ID/机房名称/局站ID/局站名称.
var siteMachineRoomHeaders = []string{"地市", "区县", "设备名称", "IP地址", "机房ID", "机房名称", "局站ID", "局站名称"}

// TestSiteMachineRoomSpec pins both schemas of the 机房局站 family: the ID
// fields are KindString (digit-string codes — a numeric kind would strip
// leading zeros and split identities), the optional site ref of machine_room
// (KindRef → site, the belongs_to source), the IPv4 value column, the
// display-name overrides, and the Specs REGISTRATION ORDER (site before
// machine_room — the deterministic tie-breaker of SuggestType on a sheet
// named 机房局站表).
func TestSiteMachineRoomSpec(t *testing.T) {
	site, ok := SpecByType(domain.ResourceTypeSite)
	if !ok {
		t.Fatal("site spec missing from Specs")
	}
	if site.Type != domain.ResourceTypeSite || site.SheetHint != "局站" {
		t.Errorf("site identity = %s/%q, want site/局站", site.Type, site.SheetHint)
	}
	if got := site.NameField(); got != "site_name" {
		t.Errorf("NameField(site) = %q, want site_name (first *_name heuristic)", got)
	}
	if got := site.NaturalKeyNames(); !reflect.DeepEqual(got, []string{"site_id"}) {
		t.Errorf("site NaturalKeyNames = %v, want [site_id]", got)
	}
	if f, _ := site.Field("site_id"); !f.Required || !f.NaturalKey || f.Kind != KindString {
		t.Errorf("site_id = Required%v NaturalKey%v Kind%v, want all true/true/string (12-digit code, leading zeros are identity)", f.Required, f.NaturalKey, f.Kind)
	}
	if f, _ := site.Field("site_name"); !f.Required || f.NaturalKey {
		t.Errorf("site_name = Required%v NaturalKey%v, want true/false", f.Required, f.NaturalKey)
	}
	if f, _ := site.Field("ip_address"); f.Kind != KindIPv4 {
		t.Errorf("site ip_address kind = %q, want ipv4 (mutable attribute, E004 on bad values)", f.Kind)
	}
	for _, f := range site.Fields {
		if f.Kind == KindRef {
			t.Errorf("site must not carry ref fields (plain container, found %s)", f.Name)
		}
	}
	if len(site.KeyFallbackFields) != 0 {
		t.Errorf("site KeyFallbackFields = %v, want none (unconditional ID key)", site.KeyFallbackFields)
	}

	mr, ok := SpecByType(domain.ResourceTypeMachineRoom)
	if !ok {
		t.Fatal("machine_room spec missing from Specs")
	}
	if mr.Type != domain.ResourceTypeMachineRoom || mr.SheetHint != "机房" {
		t.Errorf("machine_room identity = %s/%q, want machine_room/机房", mr.Type, mr.SheetHint)
	}
	if got := mr.NameField(); got != "machine_room_name" {
		t.Errorf("NameField(machine_room) = %q, want machine_room_name (override)", got)
	}
	if got := mr.NaturalKeyNames(); !reflect.DeepEqual(got, []string{"machine_room_id"}) {
		t.Errorf("machine_room NaturalKeyNames = %v, want [machine_room_id]", got)
	}
	if f, _ := mr.Field("machine_room_id"); !f.Required || !f.NaturalKey || f.Kind != KindString {
		t.Errorf("machine_room_id = Required%v NaturalKey%v Kind%v, want true/true/string (17-digit code, leading zeros are identity)", f.Required, f.NaturalKey, f.Kind)
	}
	if f, _ := mr.Field("machine_room_name"); !f.Required || f.NaturalKey {
		t.Errorf("machine_room_name = Required%v NaturalKey%v, want true/false", f.Required, f.NaturalKey)
	}
	// The container ref: optional (unmapped column = relation-less import),
	// typed to site, and the source of the belongs_to relation.
	siteRef, found := mr.Field("site_id")
	if !found {
		t.Fatal("machine_room spec misses the site_id ref field")
	}
	if siteRef.Kind != KindRef || siteRef.RefType != domain.ResourceTypeSite {
		t.Errorf("site_id = Kind%v RefType%v, want ref/site", siteRef.Kind, siteRef.RefType)
	}
	if siteRef.Required {
		t.Error("site_id must NOT be Required (sites not imported yet ⇒ the column can stay unmapped)")
	}
	if f, _ := mr.Field("ip_address"); f.Kind != KindIPv4 {
		t.Errorf("machine_room ip_address kind = %q, want ipv4", f.Kind)
	}
	// device_name stays a plain string attribute in both specs (device keys
	// are device_code; free-text nicknames resolve to nothing).
	for _, spec := range []TypeSpec{site, mr} {
		f, _ := spec.Field("device_name")
		if f.Kind != KindString || f.RefType != "" {
			t.Errorf("%s device_name = Kind%v RefType%q, want plain string attribute", spec.Type, f.Kind, f.RefType)
		}
	}

	// Registration order: site BEFORE machine_room — the container-first
	// ruling SuggestType relies on for the 机房局站表 double-hint sheet.
	var siteIdx, mrIdx = -1, -1
	for i, s := range Specs {
		switch s.Type {
		case domain.ResourceTypeSite:
			siteIdx = i
		case domain.ResourceTypeMachineRoom:
			mrIdx = i
		}
	}
	if siteIdx == -1 || mrIdx == -1 || siteIdx > mrIdx {
		t.Errorf("Specs order = site@%d machine_room@%d, want site registered first", siteIdx, mrIdx)
	}
}

// TestSuggestTypeSiteMachineRoom pins the hint resolution, including the
// double-hint sheet 机房局站表 (both 机房 and 局站 match): the FIRST
// registered spec wins — site — which is the documented prefill for the
// first pass of the import story; the mapping confirmation page still lets
// the user switch the type (the suggestion is only a prefill).
func TestSuggestTypeSiteMachineRoom(t *testing.T) {
	cases := []struct {
		sheet string
		want  domain.ResourceType
		ok    bool
	}{
		{siteMachineRoomSheetName, domain.ResourceTypeSite, true}, // double hint → first registered (site)
		{"局站表", domain.ResourceTypeSite, true},
		{"局站", domain.ResourceTypeSite, true},
		{"机房表", domain.ResourceTypeMachineRoom, true},
		{"机房", domain.ResourceTypeMachineRoom, true},
		{"动力机房清单", domain.ResourceTypeMachineRoom, true},
		{"Sheet1", "", false},
	}
	for _, tc := range cases {
		got, ok := SuggestType(tc.sheet)
		if got != tc.want || ok != tc.ok {
			t.Errorf("SuggestType(%q) = (%q, %v), want (%q, %v)", tc.sheet, got, ok, tc.want, tc.ok)
		}
	}
}

// TestSiteMachineRoomSuggest covers the suggestion ladder on the real
// 8-column layout: the site-typed 机房局站表 suggests the 6 site-relevant
// columns (the 机房 columns have no site-field synonym), while the
// machine_room-typed sheet suggests ALL 8 including the 局站ID → site_id ref.
func TestSiteMachineRoomSuggest(t *testing.T) {
	// Site reading of the double-hint sheet: 6 of 8 columns suggested at
	// synonym confidence; 机房ID/机房名称 have no candidate in the site spec.
	sug := Suggest(siteMachineRoomSheetName, siteMachineRoomHeaders)
	if len(sug) != len(siteMachineRoomHeaders) {
		t.Fatalf("Suggest returned %d suggestions, want %d", len(sug), len(siteMachineRoomHeaders))
	}
	want := []string{"city", "district", "device_name", "ip_address", "", "", "site_id", "site_name"}
	for i, w := range want {
		if sug[i].Field != w {
			t.Errorf("site suggestion[%d] (%q) = %q, want %q", i, siteMachineRoomHeaders[i], sug[i].Field, w)
		}
		if w != "" && sug[i].Confidence != ConfidenceSynonym {
			t.Errorf("site suggestion[%d] confidence = %v, want %v", i, sug[i].Confidence, ConfidenceSynonym)
		}
	}

	// Machine-room reading (机房-only sheet name): 7 of 8 columns map,
	// including the 局站ID → site_id ref field; 局站名称 has NO machine_room
	// target (the site's name lives on the site resource — the column is
	// simply not mappable in this reading).
	sugMr := Suggest(machineRoomSheetName, siteMachineRoomHeaders)
	wantMr := []string{"city", "district", "device_name", "ip_address", "machine_room_id", "machine_room_name", "site_id", ""}
	for i, w := range wantMr {
		if sugMr[i].Field != w {
			t.Errorf("machine_room suggestion[%d] (%q) = %q, want %q", i, siteMachineRoomHeaders[i], sugMr[i].Field, w)
		}
	}

	// Confidence tiers on the typed sheet.
	cases := []struct {
		name     string
		sheet    string
		header   string
		wantFld  string
		wantConf float64
	}{
		{"exact field name", siteMachineRoomSheetName, "site_id", "site_id", 1.0},
		{"normalized case field name", siteMachineRoomSheetName, "SITE_NAME", "site_name", 0.9},
		{"district alt synonym", siteMachineRoomSheetName, "县区", "district", 0.75},
		{"device-only column not suggested on site sheet", siteMachineRoomSheetName, "管理IP", "", 0},
		{"base-station column not suggested on site sheet", siteMachineRoomSheetName, "基站编码", "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Suggest(tc.sheet, []string{tc.header})
			if len(got) != 1 {
				t.Fatalf("Suggest returned %d suggestions, want 1", len(got))
			}
			if got[0].Field != tc.wantFld || got[0].Confidence != tc.wantConf {
				t.Errorf("Suggest(%q, %q) = %q/%v, want %q/%v",
					tc.sheet, tc.header, got[0].Field, got[0].Confidence, tc.wantFld, tc.wantConf)
			}
		})
	}
}

// TestSuggestMappingSiteMachineRoom pins the prefill of both readings: the
// double-hint sheet suggests the site import (keyColumns [site_id], both
// required site fields mapped, validates as-is), the 机房-only sheet
// suggests the machine_room import (keyColumns [machine_room_id], the
// 局站ID → site_id ref prefilled, validates as-is — the ref is optional but
// the prefill maps it because the column exists).
func TestSuggestMappingSiteMachineRoom(t *testing.T) {
	m := SuggestMapping([]SheetInfo{{Name: siteMachineRoomSheetName, Headers: siteMachineRoomHeaders}})
	if len(m.Sheets) != 1 {
		t.Fatalf("SuggestMapping returned %d sheets, want 1", len(m.Sheets))
	}
	sm := m.Sheets[0]
	if sm.Action != ActionImport || sm.ResourceType != domain.ResourceTypeSite {
		t.Fatalf("suggested sheet = %s/%s, want import/site", sm.Action, sm.ResourceType)
	}
	if !reflect.DeepEqual(sm.KeyColumns, []string{"site_id"}) {
		t.Errorf("site keyColumns = %v, want [site_id]", sm.KeyColumns)
	}
	if len(sm.ColumnMapping) != 6 {
		t.Errorf("site columnMapping has %d entries, want 6 (机房 columns excluded)", len(sm.ColumnMapping))
	}
	if err := Validate(m, map[string][]string{siteMachineRoomSheetName: siteMachineRoomHeaders}); err != nil {
		t.Errorf("suggested site mapping must validate as-is: %v", err)
	}

	mMr := SuggestMapping([]SheetInfo{{Name: machineRoomSheetName, Headers: siteMachineRoomHeaders}})
	if len(mMr.Sheets) != 1 {
		t.Fatalf("SuggestMapping returned %d sheets, want 1", len(mMr.Sheets))
	}
	smMr := mMr.Sheets[0]
	if smMr.Action != ActionImport || smMr.ResourceType != domain.ResourceTypeMachineRoom {
		t.Fatalf("suggested sheet = %s/%s, want import/machine_room", smMr.Action, smMr.ResourceType)
	}
	if !reflect.DeepEqual(smMr.KeyColumns, []string{"machine_room_id"}) {
		t.Errorf("machine_room keyColumns = %v, want [machine_room_id]", smMr.KeyColumns)
	}
	if smMr.ColumnMapping["site_id"] != "局站ID" {
		t.Errorf("machine_room prefill site_id ← %q, want 局站ID (the ref column)", smMr.ColumnMapping["site_id"])
	}
	if err := Validate(mMr, map[string][]string{machineRoomSheetName: siteMachineRoomHeaders}); err != nil {
		t.Errorf("suggested machine_room mapping must validate as-is: %v", err)
	}
}

// siteSheetMapping builds a complete, valid site mapping of the real layout.
func siteSheetMapping(sheet string) SheetMapping {
	return SheetMapping{
		Sheet:        sheet,
		Action:       ActionImport,
		ResourceType: domain.ResourceTypeSite,
		KeyColumns:   []string{"site_id"},
		ColumnMapping: map[string]string{
			"site_id":     "局站ID",
			"site_name":   "局站名称",
			"city":        "地市",
			"district":    "区县",
			"device_name": "设备名称",
			"ip_address":  "IP地址",
		},
	}
}

// machineRoomSheetMapping builds a machine_room mapping of the real layout;
// withSiteRef adds the optional 局站ID → site_id ref (the belongs_to source).
func machineRoomSheetMapping(sheet string, withSiteRef bool) SheetMapping {
	cm := map[string]string{
		"machine_room_id":   "机房ID",
		"machine_room_name": "机房名称",
		"city":              "地市",
		"district":          "区县",
		"device_name":       "设备名称",
		"ip_address":        "IP地址",
	}
	if withSiteRef {
		cm["site_id"] = "局站ID"
	}
	return SheetMapping{
		Sheet:         sheet,
		Action:        ActionImport,
		ResourceType:  domain.ResourceTypeMachineRoom,
		KeyColumns:    []string{"machine_room_id"},
		ColumnMapping: cm,
	}
}

// TestSiteMachineRoomValidate pins the mapping contract of both types:
// keyColumns are the single ID natural keys, the optional site_id ref column
// may stay unmapped (relation-less import), required ID/name columns must be
// mapped, and unknown target fields / missing source headers are rejected.
func TestSiteMachineRoomValidate(t *testing.T) {
	headers := map[string][]string{
		siteMachineRoomSheetName: siteMachineRoomHeaders,
		machineRoomSheetName:     siteMachineRoomHeaders,
	}
	for k, v := range testHeaders {
		headers[k] = v
	}

	cases := []struct {
		name      string
		mutate    func(m *Mapping)
		wantErr   bool
		wantField string
	}{
		{
			name: "site happy path",
			mutate: func(m *Mapping) {
				m.Sheets = append(m.Sheets, siteSheetMapping(siteMachineRoomSheetName))
			},
		},
		{
			name: "machine_room with site ref (the belongs_to path)",
			mutate: func(m *Mapping) {
				m.Sheets = append(m.Sheets, machineRoomSheetMapping(machineRoomSheetName, true))
			},
		},
		{
			name: "machine_room without site ref (optional, relation-less)",
			mutate: func(m *Mapping) {
				m.Sheets = append(m.Sheets, machineRoomSheetMapping(machineRoomSheetName, false))
			},
		},
		{
			name: "site keyColumns on machine_room mapping rejected",
			mutate: func(m *Mapping) {
				sm := machineRoomSheetMapping(machineRoomSheetName, true)
				sm.KeyColumns = []string{"site_id"}
				m.Sheets = append(m.Sheets, sm)
			},
			wantErr:   true,
			wantField: "keyColumns",
		},
		{
			name: "unmapped required machine_room_id rejected",
			mutate: func(m *Mapping) {
				sm := machineRoomSheetMapping(machineRoomSheetName, true)
				delete(sm.ColumnMapping, "machine_room_id")
				m.Sheets = append(m.Sheets, sm)
			},
			wantErr:   true,
			wantField: "machine_room_id",
		},
		{
			name: "unmapped required machine_room_name rejected",
			mutate: func(m *Mapping) {
				sm := machineRoomSheetMapping(machineRoomSheetName, true)
				delete(sm.ColumnMapping, "machine_room_name")
				m.Sheets = append(m.Sheets, sm)
			},
			wantErr:   true,
			wantField: "machine_room_name",
		},
		{
			name: "unmapped required site_name rejected",
			mutate: func(m *Mapping) {
				sm := siteSheetMapping(siteMachineRoomSheetName)
				delete(sm.ColumnMapping, "site_name")
				m.Sheets = append(m.Sheets, sm)
			},
			wantErr:   true,
			wantField: "site_name",
		},
		{
			name: "unknown target field rejected",
			mutate: func(m *Mapping) {
				sm := siteSheetMapping(siteMachineRoomSheetName)
				sm.ColumnMapping["vendor"] = "设备名称"
				m.Sheets = append(m.Sheets, sm)
			},
			wantErr:   true,
			wantField: "vendor",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := Mapping{}
			tc.mutate(&m)
			err := Validate(m, headers)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("Validate: unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Validate: want error, got nil")
			}
			var fe *FieldError
			if !errors.As(err, &fe) {
				t.Fatalf("Validate error %v is not *FieldError", err)
			}
			if fe.Field != tc.wantField {
				t.Errorf("FieldError.Field = %q, want %q", fe.Field, tc.wantField)
			}
		})
	}
}
