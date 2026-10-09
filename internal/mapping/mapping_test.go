package mapping

import (
	"encoding/json"
	"errors"
	"reflect"
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

// TestNameField checks the name-field derivation of the three schemas.
func TestNameField(t *testing.T) {
	for _, tc := range []struct {
		t    domain.ResourceType
		want string
	}{
		{domain.ResourceTypeDevice, "device_name"},
		{domain.ResourceTypeIPPool, "pool_name"},
		{domain.ResourceTypeCircuit, "circuit_code"},
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
