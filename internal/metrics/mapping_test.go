package metrics

import (
	"errors"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
)

// perfHeaders is the canonical synthetic perf-sheet header row (the same
// set the synthdata generator emits).
var perfHeaders = []string{"日期", "设备名称", "设备IP", "端口名",
	"接收光功率均值", "发送光功率均值", "接收丢包率", "发送丢包率",
	"接收带宽利用率", "发送带宽利用率", "接收速率", "发送速率",
	"接收错误包数", "发送错误包数", "光模块温度均值", "供电电压", "偏置电流", "接收光功率谷值"}

// TestSuggestCanonicalHeaders: the canonical performance headers suggest
// the complete mapping — name+port resolution, the date column, and every
// metric column matched by its catalog synonym.
func TestSuggestCanonicalHeaders(t *testing.T) {
	m, warnings := Suggest("端口性能", perfHeaders)
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none for canonical headers", warnings)
	}
	if m.TSField != "日期" {
		t.Errorf("tsField = %q, want 日期", m.TSField)
	}
	if m.ResourceType != domain.ResourceTypePort.String() {
		t.Errorf("resourceType = %q, want port", m.ResourceType)
	}
	if len(m.ResolveBy) != 2 || m.ResolveBy[0] != "device_name" || m.ResolveBy[1] != "port_name" {
		t.Errorf("resolveBy = %v, want [device_name port_name]", m.ResolveBy)
	}
	if len(m.Metrics) != 14 {
		t.Errorf("suggested metrics = %d, want 14 (all catalog synonyms matched)", len(m.Metrics))
	}
	if m.Metrics["optical_power_rx_avg_dbm"] != "接收光功率均值" {
		t.Errorf("metrics[optical_power_rx_avg_dbm] = %q, want 接收光功率均值", m.Metrics["optical_power_rx_avg_dbm"])
	}
	if err := m.Validate(perfHeaders); err != nil {
		t.Errorf("suggested mapping does not validate: %v", err)
	}
}

// TestSuggestIPResolution: without the 设备名称 column the suggestion
// falls back to the 设备IP+端口名 resolution path.
func TestSuggestIPResolution(t *testing.T) {
	headers := []string{"日期", "设备IP", "端口名", "接收光功率均值"}
	m, _ := Suggest("端口性能", headers)
	if len(m.ResolveBy) != 2 || m.ResolveBy[0] != "device_ip" || m.ResolveBy[1] != "port_name" {
		t.Errorf("resolveBy = %v, want [device_ip port_name]", m.ResolveBy)
	}
}

// TestSuggestForeignHeaders: headers nothing recognizes produce warnings
// (the honest suggestion), not a crash.
func TestSuggestForeignHeaders(t *testing.T) {
	m, warnings := Suggest("任意", []string{"foo", "bar"})
	if len(m.Metrics) != 0 || m.TSField != "" || len(m.ResolveBy) != 0 {
		t.Errorf("foreign headers suggested %+v, want empty mapping", m)
	}
	if len(warnings) == 0 {
		t.Errorf("foreign headers produced no warnings, want the honest warnings")
	}
}

// TestValidateMapping pins the typed validation errors (HTTP 400):
// unknown resource type, bad resolveBy, unresolvable resolver column,
// empty/missing tsField, unknown metric key, missing source column,
// double-booked columns — and that a valid mapping passes.
func TestValidateMapping(t *testing.T) {
	valid := IngestMapping{
		TSField:      "日期",
		ResourceType: "port",
		ResolveBy:    []string{"device_name", "port_name"},
		Metrics:      map[string]string{"optical_power_rx_avg_dbm": "接收光功率均值"},
	}
	if err := valid.Validate(perfHeaders); err != nil {
		t.Fatalf("valid mapping rejected: %v", err)
	}

	headersNoIP := []string{"日期", "设备名称", "端口名", "接收光功率均值"}
	cases := []struct {
		name string
		hdrs []string
		m    IngestMapping
		want string
	}{
		{
			name: "bad resource type",
			m:    IngestMapping{TSField: "日期", ResourceType: "device", ResolveBy: ResolveByNameIP, Metrics: valid.Metrics},
			want: "resourceType",
		},
		{
			name: "bad resolveBy",
			m:    IngestMapping{TSField: "日期", ResourceType: "port", ResolveBy: []string{"device_ip", "device_name"}, Metrics: valid.Metrics},
			want: "resolveBy",
		},
		{
			name: "resolver column missing",
			hdrs: headersNoIP,
			m:    IngestMapping{TSField: "日期", ResourceType: "port", ResolveBy: []string{"device_ip", "port_name"}, Metrics: valid.Metrics},
			want: "resolveBy", // headersNoIP carries 设备名称, not 设备IP
		},
		{
			name: "empty tsField",
			m:    IngestMapping{ResourceType: "port", ResolveBy: ResolveByNameIP, Metrics: valid.Metrics},
			want: "tsField",
		},
		{
			name: "tsField not a header",
			m:    IngestMapping{TSField: "时间", ResourceType: "port", ResolveBy: ResolveByNameIP, Metrics: valid.Metrics},
			want: "tsField",
		},
		{
			name: "unknown metric key",
			m:    IngestMapping{TSField: "日期", ResourceType: "port", ResolveBy: ResolveByNameIP, Metrics: map[string]string{"no_such_key": "接收光功率均值"}},
			want: "metrics",
		},
		{
			name: "metric source column missing",
			m:    IngestMapping{TSField: "日期", ResourceType: "port", ResolveBy: ResolveByNameIP, Metrics: map[string]string{"optical_power_rx_avg_dbm": "不存在列"}},
			want: "metrics",
		},
		{
			name: "empty metrics",
			m:    IngestMapping{TSField: "日期", ResourceType: "port", ResolveBy: ResolveByNameIP, Metrics: map[string]string{}},
			want: "metrics",
		},
		{
			name: "double-booked column (ts == resolver)",
			m:    IngestMapping{TSField: "端口名", ResourceType: "port", ResolveBy: ResolveByNameIP, Metrics: valid.Metrics},
			want: "resolveBy",
		},
		{
			name: "double-booked column (ts == metric source)",
			m:    IngestMapping{TSField: "接收光功率均值", ResourceType: "port", ResolveBy: ResolveByNameIP, Metrics: valid.Metrics},
			want: "metrics",
		},
	}
	for _, tc := range cases {
		hdrs := perfHeaders
		if tc.hdrs != nil {
			hdrs = tc.hdrs
		}
		err := tc.m.Validate(hdrs)
		if err == nil {
			t.Errorf("%s: accepted, want rejection", tc.name)
			continue
		}
		var fieldErr *FieldError
		if !errors.As(err, &fieldErr) || fieldErr.Field != tc.want {
			t.Errorf("%s: error = %v (Field=%q), want FieldError with Field %q", tc.name, err, fieldErrField(err), tc.want)
		}
	}
}

// TestMappingJSONRoundTrip: the mapping JSON shape is the storage + wire
// contract ({tsField, resourceType, resolveBy, metrics}).
func TestMappingJSONRoundTrip(t *testing.T) {
	raw := `{"tsField":"日期","resourceType":"port","resolveBy":["device_name","port_name"],"metrics":{"optical_power_rx_avg_dbm":"接收光功率均值","packet_loss_rx_pct":"接收丢包率"}}`
	m, err := ParseMapping([]byte(raw))
	if err != nil {
		t.Fatalf("ParseMapping: %v", err)
	}
	if m.TSField != "日期" || len(m.ResolveBy) != 2 || len(m.Metrics) != 2 {
		t.Fatalf("parsed mapping = %+v", m)
	}
	out, err := MarshalMapping(m)
	if err != nil {
		t.Fatalf("MarshalMapping: %v", err)
	}
	m2, err := ParseMapping(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if m2.TSField != m.TSField || len(m2.Metrics) != len(m.Metrics) {
		t.Errorf("round trip drifted: %+v → %+v", m, m2)
	}
}

// TestParseTS pins the accepted date forms (design D2: ts from data) and
// the refusals.
func TestParseTS(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"2026-01-15", "2026-01-15T00:00:00Z"},
		{" 2026-01-15 ", "2026-01-15T00:00:00Z"},
		{"2026/1/5", "2026-01-05T00:00:00Z"},
		{"2026/01/05", "2026-01-05T00:00:00Z"},
		{"2026-01-15 14:30:00", "2026-01-15T00:00:00Z"}, // daily granularity
		{"2026-01-15T14:30:00Z", "2026-01-15T00:00:00Z"},
		{"45123", "2023-07-16T00:00:00Z"}, // excel serial (epoch 1899-12-30 + 45123d)
		{"1", "1900-01-01T00:00:00Z"},     // serial 1, leap-bug table
		{"61", "1900-03-01T00:00:00Z"},    // serial 61 = post-fake-leap
		{"60", "1900-02-28T00:00:00Z"},    // the fake 1900-02-29 clamps to 02-28
	}
	for _, tc := range cases {
		got, err := ParseTS(tc.raw)
		if err != nil {
			t.Errorf("ParseTS(%q): %v", tc.raw, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseTS(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
	for _, bad := range []string{"", "   ", "not-a-date", "2026-13-45", "0", "-5", "2026-01", "15/01/2026"} {
		if _, err := ParseTS(bad); err == nil {
			t.Errorf("ParseTS(%q) accepted, want error", bad)
		}
	}
}

// fieldErrField extracts the Field of a *FieldError ("" when not one).
func fieldErrField(err error) string {
	var fieldErr *FieldError
	if errors.As(err, &fieldErr) {
		return fieldErr.Field
	}
	return ""
}
