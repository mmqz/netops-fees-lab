package mapping

// Issue #4 regression tests: SuggestTypeFromHeaders — sheet names like
// Sheet1 / 导出结果 / 查询结果 (the default names of NMS/GIS exports) carry
// no SheetHint, and the name-only SuggestType used to skip the whole sheet
// ("imported 0 rows" with no warning). The header ROW now infers the type
// when the complete natural key and a majority of the required fields are
// recognizable.

import (
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
)

// portSheet1Headers is the issue's repro shape: a 端口 export with the
// port natural key (设备名称 + 端口名称) but no sheet-name hint.
var portSheet1Headers = []string{"设备名称", "端口名称", "设备IP", "端口描述", "端口类型", "带宽Mbps", "管理状态", "运行状态"}

// deviceSheet1Headers carries the device natural key 设备编码.
var deviceSheet1Headers = []string{"设备编码", "设备名称", "管理IP", "设备状态", "厂商", "型号", "地市"}

func TestSuggestTypeFromHeaders(t *testing.T) {
	cases := []struct {
		name    string
		headers []string
		want    domain.ResourceType
		wantOK  bool
	}{
		{"port sheet (issue repro)", portSheet1Headers, domain.ResourceTypePort, true},
		{"device sheet", deviceSheet1Headers, domain.ResourceTypeDevice, true},
		{"empty headers", nil, "", false},
		{"unrecognizable headers", []string{"序号", "备注", "录入人"}, "", false},
	}
	for _, tc := range cases {
		got, ok := SuggestTypeFromHeaders(tc.headers)
		if ok != tc.wantOK || got != tc.want {
			t.Errorf("%s: SuggestTypeFromHeaders = (%q, %v), want (%q, %v)",
				tc.name, got, ok, tc.want, tc.wantOK)
		}
	}
}

// TestSuggestTypeFromHeadersKeyGuard pins the conservative guard: a sheet
// with plenty of port columns but WITHOUT the port natural key (neither
// 设备名称 nor 端口名称) must not qualify — importing without identity would
// be worse than skipping.
func TestSuggestTypeFromHeadersKeyGuard(t *testing.T) {
	headers := []string{"端口描述", "端口类型", "带宽Mbps", "管理状态", "运行状态", "厂家", "型号"}
	if got, ok := SuggestTypeFromHeaders(headers); ok {
		t.Errorf("SuggestTypeFromHeaders(no natural key) = (%q, true), want no suggestion", got)
	}
}

// TestSuggestTypeFromHeadersPrefersPortOverDevice pins the discrimination
// rule: 设备名称+端口名称+设备IP matches port's key completely while
// device's key (设备编码) is absent — device is disqualified, port wins even
// though several device fields (设备名称/设备IP/厂家/型号/地市) also match.
func TestSuggestTypeFromHeadersPrefersPortOverDevice(t *testing.T) {
	got, ok := SuggestTypeFromHeaders(portSheet1Headers)
	if !ok || got != domain.ResourceTypePort {
		t.Fatalf("SuggestTypeFromHeaders = (%q, %v), want (port, true)", got, ok)
	}
}

// TestSuggestMappingSheet1NoLongerSkipped pins the end-to-end suggestion:
// SuggestMapping on a Sheet1-named port sheet now answers action import with
// the port mapping pre-filled (was: action skip, rowsTotal 0).
func TestSuggestMappingSheet1NoLongerSkipped(t *testing.T) {
	sheets := []SheetInfo{
		{Name: "Sheet1", Headers: portSheet1Headers},
		{Name: "说明", Headers: []string{"字段", "说明"}},
	}
	m := SuggestMapping(sheets)
	if len(m.Sheets) != 2 {
		t.Fatalf("SuggestMapping: %d sheets, want 2", len(m.Sheets))
	}
	sm := m.Sheets[0]
	if sm.Sheet != "Sheet1" || sm.Action != ActionImport || sm.ResourceType != domain.ResourceTypePort {
		t.Fatalf("Sheet1 mapping = %+v, want action import / port", sm)
	}
	if len(sm.KeyColumns) != 2 {
		t.Errorf("Sheet1 keyColumns = %v, want the port composite key (device_name, port_name)", sm.KeyColumns)
	}
	if sm.ColumnMapping["port_name"] != "端口名称" {
		t.Errorf("Sheet1 columnMapping[port_name] = %q, want 端口名称", sm.ColumnMapping["port_name"])
	}
	if m.Sheets[1].Action != ActionSkip {
		t.Errorf("说明 sheet action = %q, want skip (headers carry no type signal)", m.Sheets[1].Action)
	}
}

// TestSuggestUsesHeaderInferredCandidates pins that column suggestions for
// a name-less sheet restrict to the header-inferred type's fields (a
// 端口名称 column of the Sheet1 port sheet suggests port_name, not a device
// field).
func TestSuggestUsesHeaderInferredCandidates(t *testing.T) {
	sug := Suggest("查询结果", portSheet1Headers)
	byHeader := map[string]ColumnSuggestion{}
	for _, s := range sug {
		byHeader[s.Header] = s
	}
	if got := byHeader["端口名称"].Field; got != "port_name" {
		t.Errorf("Suggest(端口名称) = %q, want port_name", got)
	}
	if got := byHeader["设备IP"].Field; got != "device_ip" {
		t.Errorf("Suggest(设备IP) = %q, want device_ip", got)
	}
}
