// PTP configuration-script channel tests (W8-D): the .txt/.cfg sniffing
// branch of Read, the virtual sheet synthesis (single "PTP配置" sheet,
// ptp_port spec headers, script line numbers as row numbers), and the
// zero-regression guard of the xlsx path.
package workbook

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/mapping"
	"github.com/mmqz/netops-lab/internal/ptpconfig"
)

// sampleScript is the byte shape of samples/ptp-config-script.txt (the
// real ZTE VRP material: both model blocks, trailing tabs, tab-suffix
// annotations, CRLF).
var sampleScript = []byte("6150配置脚本\t\r\n" +
	"config ter\t\r\n" +
	"interface gei-1/1/0/7\t\r\n" +
	"  ptp enable\t\r\n" +
	"  ptp pdelay-req-interval 0\t\r\n" +
	"  ptp unsymmetrical-offset 0 negative\t\r\n" +
	"$\t\r\n" +
	"ptp\t\r\n" +
	"free-mode\t\r\n" +
	"    port interface gei-1/1/0/7 portnum 6\t\r\n" +
	"      enable\t\r\n" +
	"      manual-port-state master\t\r\n" +
	"    $\t\r\n" +
	"end\t\r\n" +
	"write\t\r\n" +
	"\t\r\n" +
	"show ptp port-state all\t查看接口状态，接BBU口master为正常。\r\n" +
	"\t\r\n" +
	"6130XG-S配置脚本\t\r\n" +
	"config ter\t\r\n" +
	"ptp clock-mode boundary\t\r\n" +
	"ptp domain-num 24\t\r\n" +
	"ssmclock ssm-mode exp-ssm\t\r\n" +
	"interface gei_1/7\t互联口\r\n" +
	"ptp pdelay-req-interval 0\t\r\n" +
	"exit\t\r\n" +
	"interface gei_1/1\tBBU\r\n" +
	"ptp pdelay-req-interval 0\t\r\n" +
	"exit\t\r\n" +
	"end\t\r\n" +
	"write\t\r\n")

// writeScript writes data to a file of the given name and returns its path.
func writeScript(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestReadScriptSynthesizesVirtualSheet pins the virtual-sheet contract:
// one sheet named PTP配置, headers = ptp_port spec Labels in canonical
// order, 3 rows (1 + 2 ports), row numbers = script line numbers, values
// = globals overlaid by port params, SHA256 of the file bytes.
func TestReadScriptSynthesizesVirtualSheet(t *testing.T) {
	path := writeScript(t, "ptp-config-script.txt", sampleScript)
	wb, err := Read(path)
	if err != nil {
		t.Fatalf("Read script: %v", err)
	}
	if wb.Name != "ptp-config-script.txt" {
		t.Errorf("Name = %q", wb.Name)
	}
	if len(wb.Sheets) != 1 {
		t.Fatalf("sheets = %d, want 1", len(wb.Sheets))
	}
	sh := wb.Sheets[0]
	if sh.Name != ptpconfig.ScriptSheetName {
		t.Errorf("sheet name = %q, want %q", sh.Name, ptpconfig.ScriptSheetName)
	}
	spec, _ := mapping.SpecByType(domain.ResourceTypePTPPort)
	if len(sh.Headers) != len(spec.Fields) {
		t.Fatalf("headers = %d, want %d (ptp_port field count)", len(sh.Headers), len(spec.Fields))
	}
	for i, f := range spec.Fields {
		if sh.Headers[i] != f.Label {
			t.Errorf("header[%d] = %q, want spec Label %q", i, sh.Headers[i], f.Label)
		}
	}
	if sh.DataRows != 3 || len(sh.Rows) != 3 {
		t.Fatalf("rows = %d/%d, want 3/3 (6150 one + 6130XG-S two)", sh.DataRows, len(sh.Rows))
	}
	// Row 1: the 6150 port (script line 3).
	r1 := sh.Rows[0]
	if r1.Number != 3 {
		t.Errorf("row 1 number = %d, want 3 (script line of interface gei-1/1/0/7)", r1.Number)
	}
	for h, want := range map[string]string{
		"设备型号": "6150", "接口名称": "gei-1/1/0/7", "注释": "",
		"端口号": "6", "端口状态": "master", "自由模式": "true",
		"延迟请求间隔": "0", "非对称偏移": "0 negative",
		"时钟模式": "", "域号": "",
	} {
		if got := r1.Values[h]; got != want {
			t.Errorf("row1 %s = %q, want %q", h, got, want)
		}
	}
	// Row 2: the 6130XG-S gei_1/7 (script line 24, annotation 互联口, globals).
	r2 := sh.Rows[1]
	if r2.Number != 24 {
		t.Errorf("row 2 number = %d, want 24", r2.Number)
	}
	for h, want := range map[string]string{
		"设备型号": "6130XG-S", "接口名称": "gei_1/7", "注释": "互联口",
		"延迟请求间隔": "0", "时钟模式": "boundary", "域号": "24",
		"SSM模式": "exp-ssm", "端口号": "", "端口状态": "",
	} {
		if got := r2.Values[h]; got != want {
			t.Errorf("row2 %s = %q, want %q", h, got, want)
		}
	}
	// Row 3: gei_1/1 BBU (script line 27).
	r3 := sh.Rows[2]
	if r3.Number != 27 || r3.Values["接口名称"] != "gei_1/1" || r3.Values["注释"] != "BBU" {
		t.Errorf("row 3 = line %d name %q ann %q, want 27/gei_1/1/BBU", r3.Number, r3.Values["接口名称"], r3.Values["注释"])
	}
	if wb.SHA256 == "" || len(wb.SHA256) != 64 {
		t.Errorf("SHA256 = %q, want a 64-hex digest", wb.SHA256)
	}
}

// TestReadScriptSuggestsPTPPort pins the suggestion wiring: the sheet name
// suggests ptp_port, every column matches a field at synonym confidence,
// and SuggestMapping prefills the full import mapping with the composite
// key columns.
func TestReadScriptSuggestsPTPPort(t *testing.T) {
	path := writeScript(t, "script.cfg", sampleScript)
	wb, err := Read(path)
	if err != nil {
		t.Fatalf("Read script: %v", err)
	}
	sh := &wb.Sheets[0]
	if rt, ok := mapping.SuggestType(sh.Name); !ok || rt != domain.ResourceTypePTPPort {
		t.Fatalf("SuggestType(%q) = %q,%v, want ptp_port", sh.Name, rt, ok)
	}
	sug := mapping.Suggest(sh.Name, sh.Headers)
	mapped := 0
	for _, s := range sug {
		if s.Field != "" {
			mapped++
			if s.Confidence != mapping.ConfidenceSynonym {
				t.Errorf("column %q suggested %q at %v, want synonym confidence %v", s.Header, s.Field, s.Confidence, mapping.ConfidenceSynonym)
			}
		}
	}
	if mapped != len(sh.Headers) {
		t.Errorf("suggested columns = %d, want %d (every header must match a field)", mapped, len(sh.Headers))
	}
	m := mapping.SuggestMapping([]mapping.SheetInfo{{Name: sh.Name, Headers: sh.Headers}})
	if len(m.Sheets) != 1 || m.Sheets[0].Action != mapping.ActionImport ||
		m.Sheets[0].ResourceType != domain.ResourceTypePTPPort {
		t.Fatalf("SuggestMapping = %+v, want one import sheet of ptp_port", m)
	}
	keys := m.Sheets[0].KeyColumns
	if len(keys) != 2 || keys[0] != "device_model" || keys[1] != "port_name" {
		t.Errorf("keyColumns = %v, want [device_model port_name]", keys)
	}
	if len(m.Sheets[0].ColumnMapping) != len(sh.Headers) {
		t.Errorf("prefilled columnMapping = %d entries, want %d", len(m.Sheets[0].ColumnMapping), len(sh.Headers))
	}
}

// TestReadScriptUnsupportedExtensionContent pins the error contract of the
// script channel's negative paths.
func TestReadScriptUnsupportedExtensionContent(t *testing.T) {
	// .txt without the fingerprint → ErrUnsupportedScript.
	p1 := writeScript(t, "plain.txt", []byte("类型\t地址\nIP地址\t7.43.0.2\n"))
	if _, err := Read(p1); !errors.Is(err, ErrUnsupportedScript) {
		t.Errorf("Read(plain.txt) error = %v, want wrapped ErrUnsupportedScript", err)
	}
	// .cfg with sniff-passing but malformed content → ptpconfig.ErrSyntax.
	p2 := writeScript(t, "broken.cfg", []byte("config ter\n6150配置脚本\nptp bogus-subcommand\nwrite\n"))
	if _, err := Read(p2); !errors.Is(err, ptpconfig.ErrSyntax) {
		t.Errorf("Read(broken.cfg) error = %v, want wrapped ptpconfig.ErrSyntax", err)
	}
	// Sections without any interface row → ErrEmptyWorkbook.
	p3 := writeScript(t, "globals-only.txt",
		[]byte("6130XG-S配置脚本\r\nconfig ter\r\nptp domain-num 24\r\nwrite\r\n"))
	if _, err := Read(p3); !errors.Is(err, ErrEmptyWorkbook) {
		t.Errorf("Read(globals-only.txt) error = %v, want wrapped ErrEmptyWorkbook", err)
	}
	// A sniff-passing text without any model title → ptpconfig.ErrSyntax.
	p4 := writeScript(t, "no-title.txt", []byte("config ter\nptp enable\nwrite\n"))
	if _, err := Read(p4); !errors.Is(err, ptpconfig.ErrSyntax) {
		t.Errorf("Read(no-title.txt) error = %v, want wrapped ptpconfig.ErrSyntax", err)
	}
	// Script content under a NON-script extension keeps the xlsx error
	// semantics (extension AND content gate the channel).
	p5 := writeScript(t, "script.xlsx", sampleScript)
	if _, err := Read(p5); !errors.Is(err, ErrNotXLSX) {
		t.Errorf("Read(script.xlsx) error = %v, want wrapped ErrNotXLSX (xlsx path untouched)", err)
	}
	// Plain text under .xlsx also stays on the xlsx path (zero regression).
	p6 := writeScript(t, "notes.xlsx", []byte("hello world"))
	if _, err := Read(p6); !errors.Is(err, ErrNotXLSX) {
		t.Errorf("Read(notes.xlsx) error = %v, want wrapped ErrNotXLSX", err)
	}
}

// TestReadScriptRowLimit pins the MaxRowsPerSheet guard of the synthesis
// (the zip-bomb analogue of the script channel). setLimits restores the
// production defaults via t.Cleanup.
func TestReadScriptRowLimit(t *testing.T) {
	setLimits(t, 2, 1_000_000)                      // rows limit lowered for this test
	path := writeScript(t, "big.txt", sampleScript) // synthesizes 3 rows
	if _, err := Read(path); !errors.Is(err, ErrWorkbookTooLarge) {
		t.Errorf("Read(big.txt) error = %v, want wrapped ErrWorkbookTooLarge", err)
	}
}
