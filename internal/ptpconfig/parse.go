// Package ptpconfig parses ZTE VRP-style PTP configuration scripts (the
// fourth real-telecom material modality, v0.5 W8-D) into structured
// sections: one section per 「<模型名>配置脚本」 title block, carrying the
// model's global PTP clock parameters and its per-interface port entries.
//
// The parser is PURE (bytes in, data out — it imports no other internal
// package, so the workbook package can depend on it without a cycle) and
// STRICT: inside a recognized script block every line must match the
// documented ZTE VRP grammar, otherwise Parse fails with a line-numbered
// error wrapping ErrSyntax. Material OUTSIDE recognized blocks (Excel-style
// pasted rows, usage notes like `show ptp port-state all …`, numbered
// recipe blocks without a proper model title) is ignored — the real
// operator material is a MIXED document, and script blocks carry their own
// title boundary.
//
// Recognition rules (documented contract, see docs/import-and-validation.md
// §18):
//
//   - a SECTION TITLE is a line whose trimmed form is 「<model>配置脚本」
//     (an optional trailing ：/: is allowed) where <model> matches
//     [A-Za-z0-9][A-Za-z0-9_-]* — e.g. 「6150配置脚本」→ model "6150",
//     「6130XG-S配置脚本」→ model "6130XG-S". Numbered recipe headers
//     (「一、6120H/6180H配置脚本」, 「2、开通基站脚本」) do NOT match and
//     stay outside-material.
//   - a section runs from its title to the next title, the `write`
//     command, or EOF; `end` closes the config-mode nesting.
//   - parameter keys in Globals/Params are the canonical field names of
//     the mapping package's ptp_port spec (cross-pinned by
//     TestParseKeysMatchPTPPortSpec).
//   - RECOGNIZED-BUT-NOT-MATERIALIZED commands (parsed without error, no
//     field emitted — the task's field set is the closed contract):
//     interface-level ptp enable / packet-rcv-mode / sync-interval /
//     delay-req-interval / network-protocol / transfer-mode /
//     vlan-priority / port-state / switchport access vlan, the ssmclock
//     config sub-block (synchronization-clock line …), ssmclock usessm,
//     `ptp add vlan<N>`, free-mode port `enable`, and whole `vlan <N>`
//     container blocks (l2_switch / vlan-level ptp parameters). The flat
//     ptp_port table covers interface-level and free-mode port-level
//     parameters only.
package ptpconfig

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ErrSyntax is the sentinel wrapped by every Parse failure. The HTTP layer
// maps it to 422 script_parse_error (the message carries the line number).
var ErrSyntax = errors.New("PTP 配置脚本解析失败")

// ScriptSection is one model's script block: the model name, the global
// PTP clock parameters of the block (flattened onto every port row by the
// sheet synthesis) and the interface-level port entries.
type ScriptSection struct {
	// Model is the model name from the title line (「6150配置脚本」→"6150").
	Model string
	// TitleLine is the 1-based line number of the title line.
	TitleLine int
	// Globals maps ptp_port field name → value for block-global parameters
	// (ptp clock-mode …, ssmclock …, free_mode).
	Globals map[string]string
	// Ports lists the interface-level entries in first-appearance order.
	Ports []PortEntry
}

// PortEntry is one PTP-configured interface of a section. An interface
// block (`interface <name>`) and a free-mode port block
// (`port interface <name> portnum <n>`) referencing the same name merge
// into ONE entry (the 6150 shape: gei-1/1/0/7 appears in both).
type PortEntry struct {
	// PortName is the interface token verbatim (both real forms occur:
	// "gei-1/1/0/7" hyphen-style and "gei_1/7" underscore-style).
	PortName string
	// Annotation is the trailing tab/space comment of the interface line
	// (e.g. "互联口", "BBU") — whitespace-trimmed, internal spacing kept.
	Annotation string
	// Line is the 1-based line number of the line that first created the
	// entry (the interface line, or the free-mode port line).
	Line int
	// Params maps ptp_port field name → value for port-level parameters
	// (portnum, port_state, pdelay_req_interval, unsymmetrical_offset).
	Params map[string]string
}

// intParams are the parameter keys validated as base-10 integers at parse
// time (a non-numeric value is a malformed SCRIPT, not a row-level import
// issue — the virtual sheet is synthesized, so E004 row evidence would be
// meaningless). Enum values (delay_mechanism) deliberately stay
// unvalidated here: out-of-enum values ride the importer's W002 machinery.
var intParams = map[string]bool{
	"portnum":             true,
	"pdelay_req_interval": true,
	"priority1":           true,
	"priority2":           true,
	"local_priority":      true,
	"clock_class":         true,
	"clock_accuracy":      true,
	"announce_interval":   true,
	"announce_timeout":    true,
	"domain_num":          true,
	"ssm_sec_quality":     true,
	"ssm_wtr_time":        true,
}

// sectionTitleRe matches a trimmed section title line; the capture is the
// model-name candidate (「一、6120H/6180H」 fails the model check below).
var sectionTitleRe = regexp.MustCompile(`^(.+?)配置脚本[:：]?$`)

// modelNameRe constrains the model-name candidate of a title: a plain
// token of alphanumerics, hyphens and underscores (6150, 6130XG-S).
var modelNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// interfaceNameRe constrains an interface token: no whitespace (the
// trailing tab-comment is the annotation), alphanumerics plus . - _ /.
// Both real forms match: gei-1/1/0/7, gei_1/7.31, xgei-1/1/0/1.
var interfaceNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.\-/]*$`)

// ScriptSheetName is the name of the virtual sheet the workbook package
// synthesizes from the parsed sections.
const ScriptSheetName = "PTP配置"

// IsScriptText reports whether data carries the PTP script fingerprint
// used for content sniffing: both the `config ter` opener and a `ptp`
// token appear (case-insensitive substring match). The workbook package
// gates on this BEFORE calling Parse, so plain text tables and prose with
// an incidental "ptp" mention do not enter the script channel (they fail
// Parse's zero-section check instead, or the xlsx path).
func IsScriptText(data []byte) bool {
	lower := strings.ToLower(string(data))
	return strings.Contains(lower, "config ter") && strings.Contains(lower, "ptp")
}

// errAt builds a line-numbered parse error.
func errAt(line int, format string, args ...any) error {
	return fmt.Errorf("%w：第 %d 行：%s", ErrSyntax, line, fmt.Sprintf(format, args...))
}

// splitLines normalizes line endings (CRLF and lone CR → LF), strips a
// UTF-8 BOM and splits. Line numbers are 1-based over the result.
func splitLines(data []byte) []string {
	if bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}) {
		data = data[3:]
	}
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Split(s, "\n")
}

// tailAfterTokens returns the substring of line after skipping k
// whitespace-delimited tokens and any whitespace run that follows — the
// honest raw tail (annotation or multi-word value) with internal spacing
// preserved. line must be the trimmed command line.
func tailAfterTokens(line string, k int) string {
	i, n := 0, len(line)
	for ; k > 0; k-- {
		for i < n && isSpace(line[i]) {
			i++
		}
		for i < n && !isSpace(line[i]) {
			i++
		}
	}
	for i < n && isSpace(line[i]) {
		i++
	}
	return line[i:]
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' }

// context names of the nesting stack (config-mode is the empty stack).
const (
	ctxInterface = "interface"
	ctxPTP       = "ptp"
	ctxFreeMode  = "free-mode"
	ctxPort      = "port"
	ctxSSMClock  = "ssmclock"
	ctxVLAN      = "vlan"
)

// frame is one nesting level: the context kind plus, for contexts that
// carry a port entry (interface / free-mode port), the INDEX of that entry
// in sectionBuilder.ports — the merge case (an interface block re-entering
// an already-known name) must target the original entry, not whatever
// entry happens to be last.
type frame struct {
	ctx     string
	portIdx int // -1 when the context has no port entry
}

// sectionBuilder accumulates one script block.
type sectionBuilder struct {
	model     string
	titleLine int
	globals   map[string]string
	ports     []PortEntry
	byName    map[string]int // port name → index into ports
	stack     []frame        // nesting contexts, innermost last
}

func (b *sectionBuilder) ctx() string {
	if len(b.stack) == 0 {
		return ""
	}
	return b.stack[len(b.stack)-1].ctx
}

func (b *sectionBuilder) pushCtx(ctx string) {
	b.stack = append(b.stack, frame{ctx: ctx, portIdx: -1})
}

func (b *sectionBuilder) pushPortCtx(ctx string, portIdx int) {
	b.stack = append(b.stack, frame{ctx: ctx, portIdx: portIdx})
}

func (b *sectionBuilder) pop() {
	if len(b.stack) > 0 {
		b.stack = b.stack[:len(b.stack)-1]
	}
}

// setGlobal records a global parameter; a conflicting redefinition is an
// error (identical repetition is idempotent).
func (b *sectionBuilder) setGlobal(key, val string, line int) error {
	if old, ok := b.globals[key]; ok && old != val {
		return errAt(line, "全局参数 %s 重复定义且取值不同（%q → %q）", key, old, val)
	}
	b.globals[key] = val
	return nil
}

// entry returns the index of the port entry for name, creating the entry
// (line = the creating line) when absent; annotation merges only into an
// empty slot.
func (b *sectionBuilder) entry(name, annotation string, line int) (int, error) {
	if i, ok := b.byName[name]; ok {
		e := &b.ports[i]
		if annotation != "" && e.Annotation != "" && e.Annotation != annotation {
			return 0, errAt(line, "接口 %s 的注释重复定义且不一致（%q → %q）", name, e.Annotation, annotation)
		}
		if e.Annotation == "" {
			e.Annotation = annotation
		}
		return i, nil
	}
	b.ports = append(b.ports, PortEntry{
		PortName: name, Annotation: annotation, Line: line, Params: map[string]string{},
	})
	idx := len(b.ports) - 1
	b.byName[name] = idx
	return idx, nil
}

// setPortParam records a port-level parameter; conflicts error like globals.
func (e *PortEntry) setPortParam(key, val string, line int) error {
	if old, ok := e.Params[key]; ok && old != val {
		return errAt(line, "接口 %s 参数 %s 重复定义且取值不同（%q → %q）", e.PortName, key, old, val)
	}
	e.Params[key] = val
	return nil
}

// validateInt checks a recorded value against the int parameter set.
func validateInt(key, val string, line int) error {
	if !intParams[key] {
		return nil
	}
	if _, err := strconv.Atoi(val); err != nil {
		return errAt(line, "参数 %s 的值 %q 不是十进制整数", key, val)
	}
	return nil
}

// Parse parses a ZTE VRP PTP configuration script into its model sections.
// Every failure wraps ErrSyntax and, except for the whole-file
// "no sections" case, names the offending 1-based line number.
func Parse(data []byte) ([]ScriptSection, error) {
	lines := splitLines(data)
	var sections []ScriptSection
	seen := map[string]int{} // model → title line of first occurrence
	var b *sectionBuilder

	finalize := func() {
		if b != nil {
			sections = append(sections, ScriptSection{
				Model:     b.model,
				TitleLine: b.titleLine,
				Globals:   b.globals,
				Ports:     b.ports,
			})
			b = nil
		}
	}

	for i, raw := range lines {
		lineNo := i + 1
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if m := sectionTitleRe.FindStringSubmatch(trimmed); m != nil && modelNameRe.MatchString(m[1]) {
			if first, dup := seen[m[1]]; dup {
				return nil, errAt(lineNo, "模型 %q 的脚本块重复（首次出现于第 %d 行）", m[1], first)
			}
			finalize()
			seen[m[1]] = lineNo
			b = &sectionBuilder{
				model:     m[1],
				titleLine: lineNo,
				globals:   map[string]string{},
				byName:    map[string]int{},
			}
			continue
		}
		if b == nil {
			continue // outside-section material (rows, notes, recipes)
		}
		if trimmed == "write" {
			finalize()
			continue
		}
		if err := b.command(trimmed, lineNo); err != nil {
			return nil, err
		}
	}
	finalize()

	if len(sections) == 0 {
		return nil, fmt.Errorf("%w：未识别到任何「模型名配置脚本」标题行（标题格式如「6150配置脚本」）", ErrSyntax)
	}
	return sections, nil
}

// command classifies one in-section line against the ZTE VRP grammar.
func (b *sectionBuilder) command(trimmed string, line int) error {
	f := strings.Fields(trimmed)
	switch f[0] {
	case "config":
		if len(f) == 2 && (f[1] == "ter" || f[1] == "terminal") {
			if b.ctx() != "" {
				return errAt(line, "config ter 出现在块内部（应在全局配置模式）")
			}
			return nil // section opener, no state
		}
	case "end":
		b.stack = nil // closes the whole config-mode nesting
		return nil
	case "exit":
		b.pop()
		return nil
	case "!", "$":
		b.pop() // block terminator / separator
		return nil
	case "interface":
		if b.ctx() != "" {
			return errAt(line, "interface 出现在块内部（缺少 exit/!/$ 结束上一块）")
		}
		if len(f) < 2 || !interfaceNameRe.MatchString(f[1]) {
			return errAt(line, "interface 后缺少合法接口名（形如 gei-1/1/0/7 或 gei_1/7）")
		}
		idx, err := b.entry(f[1], tailAfterTokens(trimmed, 2), line)
		if err != nil {
			return err
		}
		b.pushPortCtx(ctxInterface, idx)
		return nil
	case "ptp":
		return b.ptpCommand(trimmed, f, line)
	case "ssmclock":
		return b.ssmclockCommand(f, line)
	case "ssm":
		return errAt(line, "无法识别的命令 %q（ssm 块形态不在本通道支持范围）", trimmed)
	case "vlan":
		if len(f) == 2 {
			if _, err := strconv.Atoi(f[1]); err != nil {
				return errAt(line, "vlan 后应为数字编号，收到 %q", f[1])
			}
			if b.ctx() != "" {
				return errAt(line, "vlan 出现在块内部（缺少 exit/! 结束上一块）")
			}
			b.pushCtx(ctxVLAN)
			return nil
		}
	case "port":
		if len(f) >= 2 && f[1] == "interface" {
			if b.ctx() != ctxFreeMode {
				return errAt(line, "port interface 只能出现在 free-mode 块内")
			}
			if len(f) < 5 || f[3] != "portnum" {
				return errAt(line, "port interface 行缺少 portnum 参数（形如 port interface gei-1/1/0/7 portnum 6）")
			}
			if !interfaceNameRe.MatchString(f[2]) {
				return errAt(line, "port interface 后缺少合法接口名，收到 %q", f[2])
			}
			idx, err := b.entry(f[2], tailAfterTokens(trimmed, 5), line)
			if err != nil {
				return err
			}
			if err := b.ports[idx].setPortParam("portnum", f[4], line); err != nil {
				return err
			}
			if err := validateInt("portnum", f[4], line); err != nil {
				return err
			}
			b.pushPortCtx(ctxPort, idx)
			return nil
		}
	case "free-mode":
		if b.ctx() != ctxPTP {
			return errAt(line, "free-mode 只能出现在 ptp 块内")
		}
		b.pushCtx(ctxFreeMode)
		return b.setGlobal("free_mode", "true", line)
	case "enable":
		if b.ctx() == ctxPort {
			return nil // free-mode port enable — recognized, not materialized
		}
	case "manual-port-state":
		if b.ctx() == ctxPort {
			if len(f) != 2 {
				return errAt(line, "manual-port-state 需要恰一个状态值（如 master）")
			}
			return b.setPortParamCurrent(f[1], "port_state", line)
		}
	case "network-protocol":
		if b.ctx() == ctxPort || b.ctx() == ctxVLAN {
			return nil // recognized, not materialized
		}
	case "synchronization-clock":
		if b.ctx() == ctxSSMClock {
			return nil // line-clock source — recognized, not materialized
		}
	case "l2_switch":
		if b.ctx() == ctxVLAN && len(f) == 2 && f[1] == "enable" {
			return nil // recognized, not materialized
		}
	case "switchport":
		if b.ctx() == ctxInterface && len(f) == 4 && f[1] == "access" && f[2] == "vlan" {
			if _, err := strconv.Atoi(f[3]); err != nil {
				return errAt(line, "switchport access vlan 后应为数字编号，收到 %q", f[3])
			}
			return nil // recognized, not materialized
		}
	}
	return errAt(line, "无法识别的命令 %q", trimmed)
}

// setPortParamCurrent records a parameter on the port entry of the
// INNERMOST context frame (the interface / free-mode port under parse) —
// the frame carries the entry INDEX, so the re-entered-interface merge
// case targets the original entry.
func (b *sectionBuilder) setPortParamCurrent(val, key string, line int) error {
	if len(b.stack) == 0 || b.stack[len(b.stack)-1].portIdx < 0 {
		return errAt(line, "参数 %s 出现时当前没有接口条目", key)
	}
	e := &b.ports[b.stack[len(b.stack)-1].portIdx]
	if err := e.setPortParam(key, val, line); err != nil {
		return err
	}
	return validateInt(key, val, line)
}

// ptpCommand dispatches `ptp …` lines: the bare `ptp` opens the ptp block;
// `ptp <param>` lines are global (config mode) or interface-level
// (interface/vlan context) parameters.
func (b *sectionBuilder) ptpCommand(trimmed string, f []string, line int) error {
	if len(f) == 1 {
		if b.ctx() != "" {
			return errAt(line, "ptp 块出现在块内部（缺少结束符）")
		}
		b.pushCtx(ctxPTP)
		return nil
	}
	switch b.ctx() {
	case "": // global config-mode parameters
		return b.ptpGlobal(trimmed, f, line)
	case ctxInterface, ctxVLAN: // interface-level parameters
		return b.ptpInterface(trimmed, f, line)
	default:
		return errAt(line, "ptp 子命令 %q 出现在 %s 块内部", f[1], b.ctx())
	}
}

// ptpGlobal handles the block-global ptp parameters of the 6130XG-S shape.
func (b *sectionBuilder) ptpGlobal(trimmed string, f []string, line int) error {
	var key, val string
	switch f[1] {
	case "clock-mode":
		if len(f) != 3 {
			return errAt(line, "ptp clock-mode 需要恰一个值")
		}
		key, val = "clock_mode", f[2]
	case "state-algorithm":
		if len(f) != 3 {
			return errAt(line, "ptp state-algorithm 需要恰一个值")
		}
		key, val = "state_algorithm", f[2]
	case "priority1", "priority2", "local-priority",
		"clock-class", "clock-accuracy", "announce-interval", "announce-timeout":
		if len(f) != 3 {
			return errAt(line, "ptp %s 需要恰一个整数值", f[1])
		}
		key, val = strings.ReplaceAll(f[1], "-", "_"), f[2]
	case "domain-num":
		if len(f) != 3 {
			return errAt(line, "ptp domain-num 需要恰一个整数值")
		}
		key, val = "domain_num", f[2]
	case "domain-delay-mechanism":
		if len(f) != 3 {
			return errAt(line, "ptp domain-delay-mechanism 需要恰一个值")
		}
		key, val = "delay_mechanism", f[2]
	case "utc-trace":
		val = tailAfterTokens(trimmed, 2) // compound value, e.g. "auto holdover 24"
		if val == "" {
			return errAt(line, "ptp utc-trace 缺少值")
		}
		key = "utc_trace"
	case "add":
		// `ptp add vlan4003` — recognized, not materialized.
		if len(f) == 3 && strings.HasPrefix(f[2], "vlan") {
			return nil
		}
		return errAt(line, "无法识别的 ptp 子命令组合 %q", strings.Join(f[1:], " "))
	default:
		return errAt(line, "无法识别的全局 ptp 子命令 %q", f[1])
	}
	if err := b.setGlobal(key, val, line); err != nil {
		return err
	}
	return validateInt(key, val, line)
}

// ptpInterface handles the interface-level ptp subcommands.
func (b *sectionBuilder) ptpInterface(trimmed string, f []string, line int) error {
	switch f[1] {
	case "enable", "packet-rcv-mode", "sync-interval", "delay-req-interval",
		"network-protocol", "transfer-mode", "vlan-priority", "port-state":
		return nil // recognized, not materialized (closed field set)
	case "pdelay-req-interval":
		if len(f) != 3 {
			return errAt(line, "ptp pdelay-req-interval 需要恰一个整数值")
		}
		return b.setPortParamCurrent(f[2], "pdelay_req_interval", line)
	case "unsymmetrical-offset":
		if len(f) < 3 {
			return errAt(line, "ptp unsymmetrical-offset 缺少值")
		}
		// compound value ("0 negative") — string field by design
		return b.setPortParamCurrent(tailAfterTokens(trimmed, 2), "unsymmetrical_offset", line)
	default:
		return errAt(line, "无法识别的接口级 ptp 子命令 %q", f[1])
	}
}

// ssmclockCommand handles `ssmclock …` lines.
func (b *sectionBuilder) ssmclockCommand(f []string, line int) error {
	if b.ctx() != "" && b.ctx() != ctxSSMClock {
		return errAt(line, "ssmclock 子命令出现在 %s 块内部", b.ctx())
	}
	switch {
	case len(f) == 2 && f[1] == "config":
		b.pushCtx(ctxSSMClock)
		return nil
	case len(f) == 2 && f[1] == "usessm":
		return nil // recognized, not materialized
	case len(f) == 3 && f[1] == "ssm-mode":
		return b.setGlobal("ssm_mode", f[2], line)
	case len(f) == 3 && f[1] == "sec-quality":
		if err := b.setGlobal("ssm_sec_quality", f[2], line); err != nil {
			return err
		}
		return validateInt("ssm_sec_quality", f[2], line)
	case len(f) == 3 && f[1] == "wtr-time":
		if err := b.setGlobal("ssm_wtr_time", f[2], line); err != nil {
			return err
		}
		return validateInt("ssm_wtr_time", f[2], line)
	default:
		return errAt(line, "无法识别的 ssmclock 子命令组合 %q", strings.Join(f[1:], " "))
	}
}
