package ptpconfig

import (
	"errors"
	"strings"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/mapping"
)

// realSampleLines is the byte-faithful shape of the real ZTE VRP material
// (samples/ptp-config-script.txt): the 6150 and 6130XG-S script blocks with
// trailing tabs, tab-suffix interface annotations and the block-external
// show notes. CRLF is applied by script().
var realSampleLines = []string{
	"6150配置脚本\t",
	"config ter\t",
	"interface gei-1/1/0/7\t",
	"  ptp enable\t",
	"  ptp pdelay-req-interval 0\t",
	"  ptp unsymmetrical-offset 0 negative\t",
	"$\t",
	"ptp\t",
	"free-mode\t",
	"    port interface gei-1/1/0/7 portnum 6\t",
	"      enable\t",
	"      manual-port-state master\t",
	"    $\t",
	"end\t",
	"write\t",
	"\t",
	"show ptp port-state all\t查看接口状态，接BBU口master为正常。",
	"\t",
	"6130XG-S配置脚本\t",
	"config ter\t",
	"ptp clock-mode boundary\t",
	"ptp state-algorithm alternate-bmc\t",
	"ptp priority1 128\t",
	"ptp priority2 128\t",
	"ptp local-priority 128\t",
	"ptp clock-class 187\t",
	"ptp clock-accuracy 254\t",
	"ptp domain-delay-mechanism e2e\t",
	"ptp announce-interval -3\t",
	"ptp announce-timeout 6\t",
	"ptp domain-num 24\t",
	"ptp utc-trace auto holdover 24\t",
	"ssmclock usessm\t",
	"ssmclock ssm-mode exp-ssm\t",
	"ssmclock sec-quality 11\t",
	"ssmclock wtr-time 5\t",
	"ssmclock config\t",
	"  synchronization-clock line gei_1/1 2 unframe auto-ssm 11\t",
	"!\t",
	"!\t",
	"interface gei_1/7\t互联口",
	"ptp enable\t",
	"ptp pdelay-req-interval 0\t",
	"ptp packet-rcv-mode forward\t",
	"exit\t",
	"interface gei_1/7.31\t",
	"ptp enable\t",
	"  ptp sync-interval -1\t",
	"  ptp delay-req-interval -1\t",
	"  ptp network-protocol ipv4\t",
	"  ptp transfer-mode multicast\t",
	"  ptp vlan-priority 7\t",
	"exit\t",
	"interface gei_1/1\tBBU",
	"  switchport access vlan 4003\t",
	"  ptp enable\t",
	"  ptp pdelay-req-interval 0\t",
	"  ptp packet-rcv-mode forward\t",
	"!\t",
	"ptp add vlan4003\t",
	"!\t",
	"vlan 4003\t",
	"  l2_switch enable\t",
	"  ptp enable\t",
	"  ptp port-state master\t",
	"  ptp sync-interval -4\t",
	"  ptp delay-req-interval -4\t",
	"  ptp network-protocol ieee802\t",
	"  ptp transfer-mode multicast\t",
	"!\t",
	"end\t",
	"write\t",
	"\t",
	"show ptp port-state\t",
}

// script joins lines with the given line ending.
func script(lines []string, eol string) []byte {
	return []byte(strings.Join(lines, eol))
}

// TestParseRealSampleGolden pins the parsed structure of the real material
// field by field (golden test of the import channel's source of truth).
func TestParseRealSampleGolden(t *testing.T) {
	sections, err := Parse(script(realSampleLines, "\r\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(sections) != 2 {
		t.Fatalf("sections = %d, want 2", len(sections))
	}

	s1 := sections[0]
	if s1.Model != "6150" || s1.TitleLine != 1 {
		t.Errorf("section 1 = %q (line %d), want 6150 (line 1)", s1.Model, s1.TitleLine)
	}
	if len(s1.Globals) != 1 || s1.Globals["free_mode"] != "true" {
		t.Errorf("section 6150 globals = %v, want only free_mode=true", s1.Globals)
	}
	if len(s1.Ports) != 1 {
		t.Fatalf("section 6150 ports = %d, want 1", len(s1.Ports))
	}
	p := s1.Ports[0]
	if p.PortName != "gei-1/1/0/7" || p.Line != 3 {
		t.Errorf("6150 port = %q (line %d), want gei-1/1/0/7 (line 3)", p.PortName, p.Line)
	}
	if p.Annotation != "" {
		t.Errorf("6150 port annotation = %q, want empty", p.Annotation)
	}
	want := map[string]string{
		"portnum":              "6",
		"port_state":           "master",
		"pdelay_req_interval":  "0",
		"unsymmetrical_offset": "0 negative",
	}
	if len(p.Params) != len(want) {
		t.Fatalf("6150 port params = %v, want %v", p.Params, want)
	}
	for k, v := range want {
		if p.Params[k] != v {
			t.Errorf("6150 port param %s = %q, want %q", k, p.Params[k], v)
		}
	}

	s2 := sections[1]
	if s2.Model != "6130XG-S" || s2.TitleLine != 19 {
		t.Errorf("section 2 = %q (line %d), want 6130XG-S (line 19)", s2.Model, s2.TitleLine)
	}
	wantGlobals := map[string]string{
		"clock_mode":        "boundary",
		"state_algorithm":   "alternate-bmc",
		"priority1":         "128",
		"priority2":         "128",
		"local_priority":    "128",
		"clock_class":       "187",
		"clock_accuracy":    "254",
		"delay_mechanism":   "e2e",
		"announce_interval": "-3",
		"announce_timeout":  "6",
		"domain_num":        "24",
		"utc_trace":         "auto holdover 24",
		"ssm_mode":          "exp-ssm",
		"ssm_sec_quality":   "11",
		"ssm_wtr_time":      "5",
	}
	if len(s2.Globals) != len(wantGlobals) {
		t.Fatalf("6130XG-S globals = %v, want %v", s2.Globals, wantGlobals)
	}
	for k, v := range wantGlobals {
		if s2.Globals[k] != v {
			t.Errorf("6130XG-S global %s = %q, want %q", k, s2.Globals[k], v)
		}
	}
	if len(s2.Ports) != 3 {
		t.Fatalf("6130XG-S ports = %d, want 3", len(s2.Ports))
	}
	type wantPort struct {
		name, ann string
		line      int
		params    map[string]string
	}
	for i, w := range []wantPort{
		{name: "gei_1/7", ann: "互联口", line: 41, params: map[string]string{"pdelay_req_interval": "0"}},
		{name: "gei_1/7.31", ann: "", line: 46, params: map[string]string{}},
		{name: "gei_1/1", ann: "BBU", line: 54, params: map[string]string{"pdelay_req_interval": "0"}},
	} {
		got := s2.Ports[i]
		if got.PortName != w.name || got.Annotation != w.ann || got.Line != w.line {
			t.Errorf("6130XG-S port[%d] = %q ann=%q line=%d, want %q ann=%q line=%d",
				i, got.PortName, got.Annotation, got.Line, w.name, w.ann, w.line)
		}
		if len(got.Params) != len(w.params) {
			t.Fatalf("6130XG-S port %s params = %v, want %v", w.name, got.Params, w.params)
		}
		for k, v := range w.params {
			if got.Params[k] != v {
				t.Errorf("6130XG-S port %s param %s = %q, want %q", w.name, k, got.Params[k], v)
			}
		}
	}
}

// TestParseLFAndBOMEquivalence pins that CRLF, LF and a UTF-8 BOM produce
// identical results (the same line numbering).
func TestParseLFAndBOMEquivalence(t *testing.T) {
	want, err := Parse(script(realSampleLines, "\r\n"))
	if err != nil {
		t.Fatalf("Parse CRLF: %v", err)
	}
	lf, err := Parse(script(realSampleLines, "\n"))
	if err != nil {
		t.Fatalf("Parse LF: %v", err)
	}
	if len(lf) != len(want) || lf[0].TitleLine != want[0].TitleLine || lf[1].TitleLine != want[1].TitleLine {
		t.Fatalf("LF sections differ: %+v vs %+v", lf, want)
	}
	bom, err := Parse(append([]byte{0xEF, 0xBB, 0xBF}, script(realSampleLines, "\r\n")...))
	if err != nil {
		t.Fatalf("Parse BOM+CRLF: %v", err)
	}
	if len(bom) != len(want) || bom[1].Ports[2].Line != want[1].Ports[2].Line {
		t.Fatalf("BOM sections differ: %+v vs %+v", bom, want)
	}
}

// TestParseMixedDocumentIgnoresOutsideMaterial replicates the FULL raw
// operator document (Excel-style rows before/after, numbered 6120H recipe
// blocks, show notes): only the two model-titled blocks parse.
func TestParseMixedDocumentIgnoresOutsideMaterial(t *testing.T) {
	lines := append([]string{
		"类型      地址      基站名称",
		"IP地址    7.43.0.2        手动挡应急车",
		"",
		"级别      业务类型    组网方式    备注",
	}, realSampleLines...)
	lines = append(lines,
		"",
		"一、6120H/6180H配置脚本       ",
		"config ter      ",
		"ssm     ",
		"  usessm        ",
		"  synchronization-clock line xgei-1/1/0/1       互联端口",
		"exit    ",
		"ptp     ",
		"  announce-timeout 6    ",
		"  domain 24     ",
		"  free-mode     ",
		"    port interface xgei-1/1/0/1.31 portnum 2    互联端口,编号2开始使用，累计顺延使用，不能重复",
		"      enable    ",
		"    $   ",
		"$       ",
		"2、开通基站脚本        ",
		"ptp     ",
		"  free-mode     ",
		"    port interface xgei-1/1/0/2 portnum 5       接BBU口",
		"    $   ",
		"$       ",
		"3、闭环脚本： ",
		"config ter      ",
		"end",
		"地市      区县      设备名称    IP地址    机房ID",
		"大兴      呼区      源1      4.8.14.8",
	)
	sections, err := Parse(script(lines, "\r\n"))
	if err != nil {
		t.Fatalf("Parse mixed document: %v", err)
	}
	if len(sections) != 2 {
		t.Fatalf("sections = %d, want exactly 2 (6150 + 6130XG-S)", len(sections))
	}
	if sections[0].Model != "6150" || sections[1].Model != "6130XG-S" {
		t.Fatalf("models = %q/%q, want 6150/6130XG-S", sections[0].Model, sections[1].Model)
	}
	if len(sections[1].Ports) != 3 {
		t.Fatalf("6130XG-S ports = %d, want 3 (recipe interfaces must not leak in)", len(sections[1].Ports))
	}
}

// TestParseLineNumberedErrors pins the strictness contract: every malformed
// in-section line fails with a line number, and errors wrap ErrSyntax.
func TestParseLineNumberedErrors(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{
			name:  "unknown command",
			lines: []string{"6150配置脚本", "config ter", "reboot now"},
			want:  "第 3 行",
		},
		{
			name:  "global ptp subcommand inside interface block",
			lines: []string{"6150配置脚本", "config ter", "interface gei_1/7", "ptp domain-num 24", "exit", "write"},
			want:  "第 4 行",
		},
		{
			name:  "port interface outside free-mode",
			lines: []string{"6150配置脚本", "config ter", "port interface gei_1/7 portnum 6", "$", "write"},
			want:  "第 3 行",
		},
		{
			name:  "non-integer portnum",
			lines: []string{"6150配置脚本", "config ter", "ptp", "free-mode", "port interface gei_1/7 portnum six", "$", "$", "write"},
			want:  "第 5 行",
		},
		{
			name:  "non-integer global priority",
			lines: []string{"6130XG-S配置脚本", "config ter", "ptp priority1 high", "write"},
			want:  "第 3 行",
		},
		{
			name:  "duplicate model block",
			lines: []string{"6150配置脚本", "config ter", "write", "6150配置脚本", "config ter", "write"},
			want:  "第 4 行",
		},
		{
			name:  "conflicting param redefinition",
			lines: []string{"6150配置脚本", "config ter", "interface gei_1/7", "ptp pdelay-req-interval 0", "exit", "interface gei_1/7", "ptp pdelay-req-interval 4", "exit", "write"},
			want:  "第 7 行",
		},
		{
			name:  "interface inside interface block",
			lines: []string{"6150配置脚本", "config ter", "interface gei_1/7", "interface gei_1/8", "exit", "write"},
			want:  "第 4 行",
		},
		{
			name:  "ssm block form unsupported",
			lines: []string{"6150配置脚本", "config ter", "ssm", "usessm", "exit", "write"},
			want:  "第 3 行",
		},
		{
			name:  "manual-port-state outside port block",
			lines: []string{"6150配置脚本", "config ter", "manual-port-state master", "write"},
			want:  "第 3 行",
		},
	}
	for _, tc := range cases {
		_, err := Parse(script(tc.lines, "\r\n"))
		if err == nil {
			t.Errorf("%s: Parse succeeded, want error", tc.name)
			continue
		}
		if !errors.Is(err, ErrSyntax) {
			t.Errorf("%s: error %v does not wrap ErrSyntax", tc.name, err)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not name %q", tc.name, err.Error(), tc.want)
		}
	}
}

// TestParseNoSections pins the zero-section whole-file error (a sniff-passing
// text without a model title).
func TestParseNoSections(t *testing.T) {
	_, err := Parse([]byte("config ter\nptp enable\nwrite\n"))
	if err == nil || !errors.Is(err, ErrSyntax) {
		t.Fatalf("error = %v, want ErrSyntax", err)
	}
	if !strings.Contains(err.Error(), "配置脚本") {
		t.Errorf("error %q should explain the title format", err.Error())
	}
}

// TestParseMergeAndIdempotentRedefinition covers the 6150 merge shape
// variants: interface + free-mode port on the same name merge; a section
// without a trailing write ends at EOF; identical repetition across two
// interface blocks is idempotent (conflicts error — see the error table).
func TestParseMergeAndIdempotentRedefinition(t *testing.T) {
	lines := []string{
		"6150配置脚本",
		"config ter",
		"interface gei-1/1/0/7",
		"ptp pdelay-req-interval 0",
		"exit",
		"ptp",
		"free-mode",
		"port interface gei-1/1/0/7 portnum 6",
		"enable",
		"manual-port-state master",
		"$",
		"$",
	}
	sections, err := Parse(script(lines, "\r\n"))
	if err != nil {
		t.Fatalf("Parse (no trailing write): %v", err)
	}
	if len(sections) != 1 || len(sections[0].Ports) != 1 {
		t.Fatalf("want 1 section with 1 merged port, got %+v", sections)
	}
	p := sections[0].Ports[0]
	for k, v := range map[string]string{"portnum": "6", "port_state": "master", "pdelay_req_interval": "0"} {
		if p.Params[k] != v {
			t.Errorf("merged param %s = %q, want %q", k, p.Params[k], v)
		}
	}

	// Idempotent identical repetition across two interface blocks.
	sections, err = Parse(script([]string{
		"6150配置脚本", "config ter",
		"interface gei_1/7", "ptp pdelay-req-interval 0", "exit",
		"interface gei_1/7", "ptp pdelay-req-interval 0", "exit",
		"write",
	}, "\n"))
	if err != nil {
		t.Fatalf("Parse idempotent repetition: %v", err)
	}
	if len(sections[0].Ports) != 1 {
		t.Fatalf("ports = %d, want 1 (same interface merges)", len(sections[0].Ports))
	}
}

// TestParseKeysMatchPTPPortSpec cross-pins the parser's parameter keys
// against the mapping package's ptp_port spec field names — the workbook
// synthesis maps keys to columns by field name.
func TestParseKeysMatchPTPPortSpec(t *testing.T) {
	spec, ok := mapping.SpecByType(domain.ResourceTypePTPPort)
	if !ok {
		t.Fatal("ptp_port spec missing from mapping.Specs")
	}
	valid := map[string]bool{}
	for _, f := range spec.Fields {
		valid[f.Name] = true
	}
	sections, err := Parse(script(realSampleLines, "\r\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	for _, s := range sections {
		for k := range s.Globals {
			if !valid[k] {
				t.Errorf("global key %q is not a ptp_port field", k)
			}
		}
		for _, p := range s.Ports {
			for k := range p.Params {
				if !valid[k] {
					t.Errorf("port %s param key %q is not a ptp_port field", p.PortName, k)
				}
			}
		}
	}
	// intParams must mirror the spec's KindInt fields that the parser emits.
	for k := range intParams {
		f, ok := spec.Field(k)
		if !ok || f.Kind != mapping.KindInt {
			t.Errorf("intParams key %q is not a KindInt field of the spec", k)
		}
	}
}

// TestIsScriptText pins the sniffing fingerprint.
func TestIsScriptText(t *testing.T) {
	pos := [][]byte{
		script(realSampleLines, "\r\n"),
		[]byte("CONFIG TERMINAL\nPTP stuff"),
	}
	for _, d := range pos {
		if !IsScriptText(d) {
			t.Errorf("IsScriptText(%q…) = false, want true", d[:20])
		}
	}
	neg := [][]byte{
		[]byte("类型\t地址\t基站名称\nIP地址\t7.43.0.2"),
		[]byte("config terminal\ninterface gei_1/7\nexit"),
		[]byte(""),
	}
	for _, d := range neg {
		if IsScriptText(d) {
			t.Errorf("IsScriptText(%q) = true, want false", d)
		}
	}
}

// TestScriptSheetName pins the virtual sheet name constant used by the
// workbook synthesis (SuggestType must map it to ptp_port).
func TestScriptSheetName(t *testing.T) {
	if ScriptSheetName != "PTP配置" {
		t.Fatalf("ScriptSheetName = %q, want PTP配置", ScriptSheetName)
	}
	if rt, ok := mapping.SuggestType(ScriptSheetName); !ok || rt != domain.ResourceTypePTPPort {
		t.Fatalf("SuggestType(PTP配置) = %q,%v, want ptp_port", rt, ok)
	}
}
