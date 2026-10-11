package exporter

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/mapping"
)

// sheetRowsOf opens the workbook bytes and returns the raw rows of one
// sheet (as excelize GetRows would).
func sheetRowsOf(t *testing.T, data []byte, sheet string) [][]string {
	t.Helper()
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("open exported workbook: %v", err)
	}
	defer func() { _ = f.Close() }()
	rows, err := f.GetRows(sheet)
	if err != nil {
		t.Fatalf("get rows of sheet %q: %v", sheet, err)
	}
	return rows
}

func TestSheetNameSuggestTypeRoundTrip(t *testing.T) {
	for _, spec := range mapping.Specs {
		name := SheetName(spec.Type)
		got, ok := mapping.SuggestType(name)
		if !ok || got != spec.Type {
			t.Errorf("SuggestType(%q) = %q,%v; want %q (every exported sheet name must suggest its own type)", name, got, ok, spec.Type)
		}
	}
}

func TestParseTypes(t *testing.T) {
	cases := []struct {
		raw     string
		want    []domain.ResourceType
		wantErr bool
	}{
		{raw: "", want: nil},
		{raw: "   ", want: nil},
		{raw: ",", want: nil},
		{raw: "device", want: []domain.ResourceType{domain.ResourceTypeDevice}},
		{raw: "device, ip_pool", want: []domain.ResourceType{domain.ResourceTypeDevice, domain.ResourceTypeIPPool}},
		{raw: "device,,port", want: []domain.ResourceType{domain.ResourceTypeDevice, domain.ResourceTypePort}},
		{raw: "device,device", want: []domain.ResourceType{domain.ResourceTypeDevice}},
		{raw: "bogus", wantErr: true},
		{raw: "device,bogus", wantErr: true},
	}
	for _, tc := range cases {
		got, err := ParseTypes(tc.raw)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseTypes(%q) = %v, want error", tc.raw, got)
			} else if !bytes.Contains([]byte(err.Error()), []byte("bogus")) {
				t.Errorf("ParseTypes(%q) error %q must name the offending type", tc.raw, err.Error())
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseTypes(%q): %v", tc.raw, err)
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("ParseTypes(%q) = %v, want %v", tc.raw, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("ParseTypes(%q)[%d] = %s, want %s", tc.raw, i, got[i], tc.want[i])
			}
		}
	}
}

func TestBuildShape(t *testing.T) {
	resources := []domain.Resource{
		{
			ID: "res_d2", Type: domain.ResourceTypeDevice, NaturalKey: "DEV-002",
			Name: "核心路由2", Status: "停用",
			Attributes: map[string]string{"device_name": "核心路由2", "city": "杭州", "mgmt_ip": "10.0.0.2"},
		},
		{
			ID: "res_d1", Type: domain.ResourceTypeDevice, NaturalKey: "DEV-001",
			Name: "核心路由1", Status: "在网",
			Attributes: map[string]string{"device_name": "核心路由1", "city": "杭州", "vendor": "华为", "mgmt_ip": "10.0.0.1"},
		},
		{
			ID: "res_p1", Type: domain.ResourceTypePort, NaturalKey: "DEV-001|gei-1/1/0/7",
			Name:       "gei-1/1/0/7",
			Attributes: map[string]string{"device_ip": "10.0.0.1", "port_type": "GE", "bandwidth_mbps": "1000"},
		},
	}
	data, summary, err := BuildBytes(resources, nil)
	if err != nil {
		t.Fatalf("BuildBytes: %v", err)
	}

	// Summary: two sheets, rows in Specs order (device before port).
	if summary.TotalRows != 3 {
		t.Errorf("summary.TotalRows = %d, want 3", summary.TotalRows)
	}
	if summary.Types["device"] != 2 || summary.Types["port"] != 1 {
		t.Errorf("summary.Types = %v, want device:2 port:1", summary.Types)
	}

	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = f.Close() }()
	if got := f.GetSheetList(); len(got) != 2 || got[0] != SheetName(domain.ResourceTypeDevice) || got[1] != SheetName(domain.ResourceTypePort) {
		t.Fatalf("sheet list = %v, want [%s %s] (Specs order, empty types absent)", got, SheetName(domain.ResourceTypeDevice), SheetName(domain.ResourceTypePort))
	}

	// Device sheet: headers = field Labels in spec order; rows sorted by
	// natural key; cells from attributes / status.
	spec, _ := mapping.SpecByType(domain.ResourceTypeDevice)
	rows := sheetRowsOf(t, data, SheetName(domain.ResourceTypeDevice))
	wantHeaders := make([]string, len(spec.Fields))
	for i, field := range spec.Fields {
		wantHeaders[i] = field.Label
	}
	if len(rows) != 3 {
		t.Fatalf("device sheet rows = %d, want 3 (header + 2 data)", len(rows))
	}
	for i, h := range rows[0] {
		if i < len(wantHeaders) && h != wantHeaders[i] {
			t.Errorf("device header[%d] = %q, want %q", i, h, wantHeaders[i])
		}
	}
	if rows[1][0] != "DEV-001" || rows[2][0] != "DEV-002" {
		t.Errorf("device rows not sorted by natural key: %q, %q", rows[1][0], rows[2][0])
	}
	// DEV-001 row: key column backfill, attribute cells, status cell.
	if rows[1][1] != "核心路由1" || rows[1][2] != "杭州" || rows[1][3] != "华为" {
		t.Errorf("DEV-001 attribute cells = %v", rows[1])
	}
	statusIdx := -1
	for i, field := range spec.Fields {
		if field.Name == "status" {
			statusIdx = i
		}
	}
	if rows[1][statusIdx] != "在网" || rows[2][statusIdx] != "停用" {
		t.Errorf("status cells = %q / %q, want 在网 / 停用", rows[1][statusIdx], rows[2][statusIdx])
	}

	// Port sheet: composite key backfilled into both key columns.
	portSpec, _ := mapping.SpecByType(domain.ResourceTypePort)
	portRows := sheetRowsOf(t, data, SheetName(domain.ResourceTypePort))
	if len(portRows) != 2 || len(portRows[1]) < 3 {
		t.Fatalf("port sheet rows = %v", portRows)
	}
	deviceNameIdx, portNameIdx := -1, -1
	for i, field := range portSpec.Fields {
		switch field.Name {
		case "device_name":
			deviceNameIdx = i
		case "port_name":
			portNameIdx = i
		}
	}
	if portRows[1][deviceNameIdx] != "DEV-001" || portRows[1][portNameIdx] != "gei-1/1/0/7" {
		t.Errorf("port key cells = %q / %q, want DEV-001 / gei-1/1/0/7", portRows[1][deviceNameIdx], portRows[1][portNameIdx])
	}
}

func TestBuildKeyColumnBackfill(t *testing.T) {
	cases := []struct {
		name     string
		resource domain.Resource
		// field name → expected cell value (nil → expect "")
		want map[string]string
	}{
		{
			name: "port composite key via Name suffix",
			resource: domain.Resource{
				Type: domain.ResourceTypePort, NaturalKey: "NE-A|gei-1/1/0/7",
				Name:       "gei-1/1/0/7",
				Attributes: map[string]string{"device_ip": "10.1.1.1"},
			},
			want: map[string]string{"device_name": "NE-A", "port_name": "gei-1/1/0/7"},
		},
		{
			name: "port key with | inside the device component (Name suffix is exact)",
			resource: domain.Resource{
				Type: domain.ResourceTypePort, NaturalKey: "A|B|gei-1/1/0/7",
				Name: "gei-1/1/0/7",
			},
			want: map[string]string{"device_name": "A|B", "port_name": "gei-1/1/0/7"},
		},
		{
			name: "port key with | inside the port component",
			resource: domain.Resource{
				Type: domain.ResourceTypePort, NaturalKey: "NE-A|sub|port",
				Name: "sub|port",
			},
			want: map[string]string{"device_name": "NE-A", "port_name": "sub|port"},
		},
		{
			name: "device single key with | survives unsplit",
			resource: domain.Resource{
				Type: domain.ResourceTypeDevice, NaturalKey: "DEV|001", Name: "x",
			},
			want: map[string]string{"device_code": "DEV|001"},
		},
		{
			name: "base_station primary key (code) is written verbatim",
			resource: domain.Resource{
				Type: domain.ResourceTypeBaseStation, NaturalKey: "B123456", Name: "站A",
				Attributes: map[string]string{"base_station_name": "站A", "city": "哈尔滨"},
			},
			want: map[string]string{"base_station_code": "B123456", "city": "哈尔滨", "base_station_name": "站A"},
		},
		{
			name: "base_station degraded key (bare name) leaves the code column empty",
			resource: domain.Resource{
				Type: domain.ResourceTypeBaseStation, NaturalKey: "站A", Name: "站A",
				Attributes: map[string]string{"base_station_name": "站A"},
			},
			want: map[string]string{"base_station_code": "", "base_station_name": "站A", "city": ""},
		},
		{
			name: "base_station degraded key with city re-derives city|name",
			resource: domain.Resource{
				Type: domain.ResourceTypeBaseStation, NaturalKey: "哈尔滨|站A", Name: "站A",
				Attributes: map[string]string{"base_station_name": "站A", "city": "哈尔滨"},
			},
			want: map[string]string{"base_station_code": "", "base_station_name": "站A", "city": "哈尔滨"},
		},
		{
			name: "base_station degraded key that drifted (city added later) falls back to the primary path",
			resource: domain.Resource{
				// Key is still the bare name, but a W005-applied city makes the
				// fallback join differ from the key: the primary path writes the
				// key verbatim so the round trip reconstructs the SAME key.
				Type: domain.ResourceTypeBaseStation, NaturalKey: "站A", Name: "站A",
				Attributes: map[string]string{"base_station_name": "站A", "city": "哈尔滨"},
			},
			want: map[string]string{"base_station_code": "站A", "base_station_name": "站A", "city": "哈尔滨"},
		},
	}
	for _, tc := range cases {
		spec, ok := mapping.SpecByType(tc.resource.Type)
		if !ok {
			t.Fatalf("%s: no spec for %s", tc.name, tc.resource.Type)
		}
		got := rowValues(spec, tc.resource)
		for field, want := range tc.want {
			if got[field] != want {
				t.Errorf("%s: %s = %q, want %q", tc.name, field, got[field], want)
			}
		}
	}
}

func TestBuildDeterministicBytes(t *testing.T) {
	resources := sampleResourcesFixtures()
	a, _, err := BuildBytes(resources, nil)
	if err != nil {
		t.Fatalf("BuildBytes #1: %v", err)
	}
	b, _, err := BuildBytes(resources, nil)
	if err != nil {
		t.Fatalf("BuildBytes #2: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("same input twice must produce byte-identical xlsx output")
	}
	// Input order must not matter (rows are sorted, sheets follow Specs).
	shuffled := []domain.Resource{resources[2], resources[0], resources[3], resources[1]}
	c, _, err := BuildBytes(shuffled, nil)
	if err != nil {
		t.Fatalf("BuildBytes shuffled: %v", err)
	}
	if !bytes.Equal(a, c) {
		t.Fatal("shuffled input must produce byte-identical xlsx output (order-insensitive determinism)")
	}
}

func TestBuildEmpty(t *testing.T) {
	data, summary, err := BuildBytes(nil, nil)
	if err != nil {
		t.Fatalf("BuildBytes empty: %v", err)
	}
	if summary.TotalRows != 0 || len(summary.Types) != 0 {
		t.Errorf("empty export summary = %+v, want zero", summary)
	}
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("empty export must be a readable xlsx: %v", err)
	}
	defer func() { _ = f.Close() }()
	if got := f.GetSheetList(); len(got) != 1 || got[0] != emptySheetName {
		t.Errorf("empty export sheets = %v, want the single placeholder %q", got, emptySheetName)
	}
	// A types filter selecting nothing behaves like the empty export.
	data2, _, err := BuildBytes(nil, []domain.ResourceType{domain.ResourceTypeSite})
	if err != nil {
		t.Fatalf("BuildBytes filtered-empty: %v", err)
	}
	f2, err := excelize.OpenReader(bytes.NewReader(data2))
	if err != nil {
		t.Fatalf("filtered-empty export must be a readable xlsx: %v", err)
	}
	defer func() { _ = f2.Close() }()
	if got := f2.GetSheetList(); len(got) != 1 || got[0] != emptySheetName {
		t.Errorf("filtered-empty export sheets = %v, want the single placeholder", got)
	}
}

func TestBuildTypesFilter(t *testing.T) {
	resources := []domain.Resource{
		{ID: "res_d1", Type: domain.ResourceTypeDevice, NaturalKey: "DEV-001", Name: "n1"},
		{ID: "res_i1", Type: domain.ResourceTypeIPPool, NaturalKey: "POOL-A", Name: "p1",
			Attributes: map[string]string{"cidr": "10.0.0.0/24", "assigned_device": "DEV-001"}},
	}
	data, summary, err := BuildBytes(resources, []domain.ResourceType{domain.ResourceTypeIPPool})
	if err != nil {
		t.Fatalf("BuildBytes filtered: %v", err)
	}
	if summary.Types["device"] != 0 || summary.Types["ip_pool"] != 1 || summary.TotalRows != 1 {
		t.Errorf("filtered summary = %+v, want ip_pool:1 only", summary)
	}
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = f.Close() }()
	if got := f.GetSheetList(); len(got) != 1 || got[0] != SheetName(domain.ResourceTypeIPPool) {
		t.Errorf("filtered sheets = %v, want only %s", got, SheetName(domain.ResourceTypeIPPool))
	}
	// The ref column value comes from the attribute snapshot.
	poolSpec, _ := mapping.SpecByType(domain.ResourceTypeIPPool)
	rows := sheetRowsOf(t, data, SheetName(domain.ResourceTypeIPPool))
	refIdx, cidrIdx := -1, -1
	for i, field := range poolSpec.Fields {
		switch field.Name {
		case "assigned_device":
			refIdx = i
		case "cidr":
			cidrIdx = i
		}
	}
	if rows[1][refIdx] != "DEV-001" || rows[1][cidrIdx] != "10.0.0.0/24" {
		t.Errorf("ip_pool cells: assigned_device = %q cidr = %q", rows[1][refIdx], rows[1][cidrIdx])
	}
}

// TestExportedHeadersSuggestBack pins the suggestion-engine round trip of
// the exported header row: every field Label maps back to its OWN field.
// The measured confidences are the synonym tier 0.75 for all Labels EXCEPT
// ip_pool's "CIDR" (0.9 — the normalized tier, because lowercase("CIDR")
// equals the field name "cidr"). NOT 1.0 anywhere: the exact tier only
// fires on the English field names. These are the honest, measured values;
// the SuggestMapping built from an exported sheet is complete (every spec
// field mapped), which is what makes the unattended re-import possible.
func TestExportedHeadersSuggestBack(t *testing.T) {
	for _, spec := range mapping.Specs {
		name := SheetName(spec.Type)
		headers := make([]string, len(spec.Fields))
		for i, field := range spec.Fields {
			headers[i] = field.Label
		}
		sug := mapping.Suggest(name, headers)
		for i, field := range spec.Fields {
			if sug[i].Field != field.Name {
				t.Errorf("%s: Label %q suggested %q, want its own field %s", name, field.Label, sug[i].Field, field.Name)
			}
			wantConf := mapping.ConfidenceSynonym
			if strings.EqualFold(field.Label, field.Name) {
				wantConf = mapping.ConfidenceNormalized // only "CIDR" today
			}
			if sug[i].Confidence != wantConf {
				t.Errorf("%s: Label %q confidence = %v, want the measured tier %v",
					name, field.Label, sug[i].Confidence, wantConf)
			}
		}
		// The full suggested mapping of the sheet must be importable:
		// action import, the type, keyColumns, and EVERY field mapped.
		sm := mapping.SuggestMapping([]mapping.SheetInfo{{Name: name, Headers: headers}}).Sheets[0]
		if sm.Action != mapping.ActionImport || sm.ResourceType != spec.Type {
			t.Fatalf("%s: SuggestMapping action/type = %q/%q", name, sm.Action, sm.ResourceType)
		}
		if len(sm.ColumnMapping) != len(spec.Fields) {
			t.Errorf("%s: SuggestMapping maps %d/%d fields", name, len(sm.ColumnMapping), len(spec.Fields))
		}
		if want := spec.NaturalKeyNames(); len(sm.KeyColumns) != len(want) {
			t.Errorf("%s: SuggestMapping keyColumns = %v, want %v", name, sm.KeyColumns, want)
		}
	}
}

func TestWriteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "export.xlsx")
	resources := []domain.Resource{
		{ID: "res_d1", Type: domain.ResourceTypeDevice, NaturalKey: "DEV-001", Name: "n1", Status: "在网",
			Attributes: map[string]string{"device_name": "n1"}},
	}
	summary, err := WriteFile(path, resources, nil)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if summary.TotalRows != 1 {
		t.Errorf("summary.TotalRows = %d, want 1", summary.TotalRows)
	}
	if data, err := os.ReadFile(path); err != nil || len(data) == 0 {
		t.Fatalf("export file unreadable: %v", err)
	}
	// Overwrite semantics: exporting again to the same path succeeds and
	// the content reflects the new resource set.
	summary2, err := WriteFile(path, resources[:0], nil)
	if err != nil {
		t.Fatalf("WriteFile overwrite: %v", err)
	}
	if summary2.TotalRows != 0 {
		t.Errorf("overwrite summary.TotalRows = %d, want 0", summary2.TotalRows)
	}
	f, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("open overwritten export: %v", err)
	}
	defer func() { _ = f.Close() }()
	if got := f.GetSheetList(); len(got) != 1 || got[0] != emptySheetName {
		t.Errorf("overwritten export sheets = %v, want the placeholder", got)
	}
}

// sampleResourcesFixtures returns four resources of three types in a
// NON-sorted order (the determinism tests rely on the input order not
// being the output order).
func sampleResourcesFixtures() []domain.Resource {
	return []domain.Resource{
		{ID: "res_d2", Type: domain.ResourceTypeDevice, NaturalKey: "DEV-002", Name: "n2", Status: "停用",
			Attributes: map[string]string{"device_name": "n2"}},
		{ID: "res_d1", Type: domain.ResourceTypeDevice, NaturalKey: "DEV-001", Name: "n1", Status: "在网",
			Attributes: map[string]string{"device_name": "n1"}},
		{ID: "res_p1", Type: domain.ResourceTypePort, NaturalKey: "DEV-001|gei-1/1/0/7", Name: "gei-1/1/0/7",
			Attributes: map[string]string{"device_ip": "10.0.0.1"}},
		{ID: "res_s1", Type: domain.ResourceTypeSite, NaturalKey: "123456789012", Name: "局站甲",
			Attributes: map[string]string{"site_name": "局站甲", "city": "哈尔滨"}},
	}
}
