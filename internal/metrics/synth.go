package metrics

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

// SynthOptions parameterize the synthetic data generator (CLI
// `netopsfees synthdata`). Defaults are small demo scale (20 devices × 2
// ports, 30 days, scenario normal, seed 42) — the volume runs raise
// --devices/--ports/--metrics explicitly.
type SynthOptions struct {
	// Out is the output directory (created when missing; the default is
	// NOT testdata/ — the caller passes --out, per §12-A5 the demo
	// placeholder later points at testdata/placeholder/).
	Out string
	// Devices is the number of synthetic devices.
	Devices int
	// Ports is the number of ports per device (default 2; volume runs use
	// 20).
	Ports int
	// Days is the number of daily performance workbooks.
	Days int
	// Scenario is normal | degrade | outage.
	Scenario string
	// Seed drives every derived value: same seed → byte-identical output.
	Seed int64
	// Metrics is the number of metric columns to emit (registry order,
	// first N); 0 = the whole closed registry.
	Metrics int
}

// SynthSummary is the JSON envelope of `netopsfees synthdata`.
type SynthSummary struct {
	Out      string   `json:"out"`
	Files    []string `json:"files"`
	Devices  int      `json:"devices"`
	Ports    int      `json:"portsPerDevice"`
	Days     int      `json:"days"`
	Scenario string   `json:"scenario"`
	Seed     int64    `json:"seed"`
	Metrics  int      `json:"metricColumns"`
}

// synth scenario names.
const (
	ScenarioNormal  = "normal"
	ScenarioDegrade = "degrade"
	ScenarioOutage  = "outage"
)

// ValidScenario reports whether s is a known scenario.
func ValidScenario(s string) bool {
	switch s {
	case ScenarioNormal, ScenarioDegrade, ScenarioOutage:
		return true
	}
	return false
}

// The canonical sheet names of the generated workbooks. resources.xlsx
// uses the exporter-pinned names so the EXISTING import channel suggests
// the right types with zero manual mapping (mapping.SuggestType sheet-name
// ladder); the perf workbooks use one metric-flavored sheet per day.
const (
	synthDeviceSheet = "网元设备"  // SuggestType → device
	synthPortSheet   = "端口资源表" // SuggestType → port
	synthPerfSheet   = "端口性能"  // metric channel picks it by synonym match
)

// synthResourceHeaders are the column sets of resources.xlsx (field Labels
// of the two specs, exact-importable by the existing channel).
var (
	synthDeviceHeaders = []string{"设备编码", "设备名称", "厂商", "型号", "管理IP", "状态"}
	synthPortHeaders   = []string{"设备名称", "端口名称", "设备IP", "端口类型", "带宽Mbps"}
)

// synthPerfHeaders builds the perf workbook headers: the resolution and
// time columns plus the first n registry metric synonyms.
func synthPerfHeaders(n int) []string {
	if n <= 0 || n > len(MetricKeys) {
		n = len(MetricKeys)
	}
	headers := make([]string, 0, 4+n)
	headers = append(headers, "日期", "设备名称", "设备IP", "端口名")
	for i := 0; i < n; i++ {
		// The first synonym of every registry key IS the canonical real
		// export header (接收光功率均值, 接收丢包率, …).
		headers = append(headers, MetricKeys[i].Synonyms[0])
	}
	return headers
}

// synthPort is one generated port row identity.
type synthPort struct {
	deviceIdx  int
	deviceName string
	deviceIP   string
	portName   string
}

// synthBase is the normal-curve anchor of one metric: base value, jitter
// amplitude and the value's string precision.
type synthBase struct {
	base   float64
	jitter float64
	prec   int
}

// synthBases anchors every registry metric's normal curve (all inside the
// catalog plausibility bounds — the acceptance rule).
var synthBases = []synthBase{
	{base: -8.0, jitter: 0.1, prec: 2},   // optical_power_rx_avg_dbm（抖动收敛到 ±0.1：劣化日降 0.15 必须显著大于噪声）
	{base: -2.0, jitter: 0.1, prec: 2},   // optical_power_tx_avg_dbm
	{base: 0.02, jitter: 0.02, prec: 2},  // packet_loss_rx_pct（非负钳位）
	{base: 0.01, jitter: 0.01, prec: 2},  // packet_loss_tx_pct（非负钳位）
	{base: 35.0, jitter: 3.0, prec: 1},   // bandwidth_util_rx_pct
	{base: 30.0, jitter: 3.0, prec: 1},   // bandwidth_util_tx_pct
	{base: 850.0, jitter: 50.0, prec: 1}, // rx_rate_mbps
	{base: 400.0, jitter: 40.0, prec: 1}, // tx_rate_mbps
	{base: 0.0, jitter: 2.0, prec: 0},    // errors_rx_count（非负钳位）
	{base: 0.0, jitter: 2.0, prec: 0},    // errors_tx_count（非负钳位）
	{base: 42.0, jitter: 1.5, prec: 1},   // module_temp_avg_c
	{base: 3.3, jitter: 0.02, prec: 2},   // supply_voltage_v
	{base: 35.0, jitter: 1.0, prec: 1},   // bias_current_ma
	{base: -10.0, jitter: 0.1, prec: 2},  // optical_power_rx_min_dbm
}

// The synthetic window starts at a FIXED date — the data path never reads
// the clock (determinism gate).
var synthStartDate = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// curve constants of the two fault scenarios (optical RX only, per the
// design's degrade acceptance).
const (
	degradePerDay = -0.15 // dBm/day on optical_power_rx_avg_dbm
	outageFloor   = -28.0 // dBm hard drop (inside the -30..5 bounds)
)

// WriteSynthData generates the synthetic dataset of o into o.Out:
//
//   - resources.xlsx — a device sheet + a port sheet importable by the
//     EXISTING import channel (deterministic ids DEV-S{seed}-{n}, IPs
//     10.{seed%250}.{n/250+1}.{n%250+1});
//   - perf-day-01.xlsx … perf-day-{DD}.xlsx — one workbook per day, one row
//     per port, columns 日期/设备名称/设备IP/端口名 + the first
//     o.Metrics catalog metric columns under their canonical Chinese
//     headers;
//   - README.md — synthetic origin statement + the exact regeneration
//     command (§12-A5: no third-party license constraints, but the origin
//     is stated where the data lives).
//
// DETERMINISM IS THE ACCEPTANCE GATE: the same seed yields byte-identical
// files (excelize writes fixed zip timestamps — the exporter's pinned
// precedent). Curves: normal = stable base + small hash-derived jitter;
// degrade = a deterministic ~10% port subset whose
// optical_power_rx_avg_dbm declines degradePerDay per day across the
// window; outage = a deterministic subset at outageFloor from mid-window.
// Every emitted value respects the catalog plausibility bounds.
func WriteSynthData(o SynthOptions) (SynthSummary, error) {
	switch o.Scenario {
	case ScenarioNormal, ScenarioDegrade, ScenarioOutage:
	default:
		return SynthSummary{}, fmt.Errorf("metrics: synthdata: unknown scenario %q (normal|degrade|outage)", o.Scenario)
	}
	if o.Devices <= 0 {
		o.Devices = 20
	}
	if o.Ports <= 0 {
		o.Ports = 2
	}
	if o.Days <= 0 {
		o.Days = 30
	}
	if o.Metrics <= 0 || o.Metrics > len(MetricKeys) {
		o.Metrics = len(MetricKeys)
	}
	if o.Out == "" {
		return SynthSummary{}, fmt.Errorf("metrics: synthdata: --out 目录必填")
	}
	if err := os.MkdirAll(o.Out, 0o755); err != nil {
		return SynthSummary{}, fmt.Errorf("metrics: synthdata: create %s: %w", o.Out, err)
	}

	ports := synthPorts(o)
	files := make([]string, 0, o.Days+2)

	// resources.xlsx (the existing import channel consumes it): the device
	// sheet is the renamed default sheet, the port sheet is added after it
	// (device first — the E003 round-trip ordering).
	resPath := filepath.Join(o.Out, "resources.xlsx")
	if err := writeResourcesWorkbook(resPath, o, ports); err != nil {
		return SynthSummary{}, err
	}
	files = append(files, "resources.xlsx")

	// perf-day-DD.xlsx — one per day.
	for d := 1; d <= o.Days; d++ {
		name := fmt.Sprintf("perf-day-%02d.xlsx", d)
		path := filepath.Join(o.Out, name)
		rows := synthPerfRows(o, ports, d)
		if err := writeSheetWorkbook(path, synthPerfSheet, synthPerfHeaders(o.Metrics), rows); err != nil {
			return SynthSummary{}, err
		}
		files = append(files, name)
	}

	// README.md — synthetic origin + regeneration command.
	if err := writeSynthREADME(o, files); err != nil {
		return SynthSummary{}, err
	}
	files = append(files, "README.md")

	return SynthSummary{
		Out:      o.Out,
		Files:    files,
		Devices:  o.Devices,
		Ports:    o.Ports,
		Days:     o.Days,
		Scenario: o.Scenario,
		Seed:     o.Seed,
		Metrics:  o.Metrics,
	}, nil
}

// synthPorts builds the port universe: device i (0-based) has name/code
// DEV-S{seed}-{i+1:04d}, IP 10.{seed%250}.{i/250+1}.{i%250+1}, and
// o.Ports ports GE-0/0/{p+1}.
func synthPorts(o SynthOptions) []synthPort {
	out := make([]synthPort, 0, o.Devices*o.Ports)
	for i := 0; i < o.Devices; i++ {
		name := fmt.Sprintf("DEV-S%d-%04d", o.Seed, i+1)
		ip := fmt.Sprintf("10.%d.%d.%d", o.Seed%250, i/250+1, i%250+1)
		for p := 0; p < o.Ports; p++ {
			out = append(out, synthPort{
				deviceIdx:  i,
				deviceName: name,
				deviceIP:   ip,
				portName:   fmt.Sprintf("GE-0/0/%d", p+1),
			})
		}
	}
	return out
}

// synthDeviceRows builds the device sheet rows of resources.xlsx (code,
// name, vendor, model, mgmt IP, status — all strings, import-isomorphic).
func synthDeviceRows(o SynthOptions) [][]string {
	rows := make([][]string, 0, o.Devices)
	for i := 0; i < o.Devices; i++ {
		name := fmt.Sprintf("DEV-S%d-%04d", o.Seed, i+1)
		ip := fmt.Sprintf("10.%d.%d.%d", o.Seed%250, i/250+1, i%250+1)
		rows = append(rows, []string{name, name, "合成厂商", "SYNTH-1000", ip, "在用"})
	}
	return rows
}

// synthPortRows builds the port sheet rows of resources.xlsx.
func synthPortRows(o SynthOptions, ports []synthPort) [][]string {
	rows := make([][]string, 0, len(ports))
	for _, p := range ports {
		rows = append(rows, []string{p.deviceName, p.portName, p.deviceIP, "GE", "10000"})
	}
	return rows
}

// synthPerfRows builds the data rows of perf-day-{d}.xlsx: one row per
// port, in port-universe order (deterministic), with the day's date and
// the metric values from the scenario curves.
func synthPerfRows(o SynthOptions, ports []synthPort, day int) [][]string {
	date := synthStartDate.AddDate(0, 0, day-1).Format("2006-01-02")
	headers := synthPerfHeaders(o.Metrics)
	rows := make([][]string, 0, len(ports))
	for _, p := range ports {
		row := make([]string, 0, len(headers))
		row = append(row, date, p.deviceName, p.deviceIP, p.portName)
		for mi := 0; mi < o.Metrics; mi++ {
			row = append(row, synthValue(o, p, mi, day))
		}
		rows = append(rows, row)
	}
	return rows
}

// synthValue computes the day's value of metric mi for port p under the
// scenario: normal = base + hash-derived jitter; degrade = RX avg power
// declines degradePerDay per day for the deterministic ~10% subset;
// outage = the deterministic subset sits at outageFloor from mid-window.
// Everything stays inside the catalog bounds.
func synthValue(o SynthOptions, p synthPort, mi, day int) string {
	b := synthBases[mi]
	v := b.base + jitter(o.Seed, p.deviceIdx, p.portName, mi, day, b.jitter)

	if MetricKeys[mi].Key == "optical_power_rx_avg_dbm" {
		switch o.Scenario {
		case ScenarioDegrade:
			if faultPort(o, p) {
				v += degradePerDay * float64(day-1)
			}
		case ScenarioOutage:
			if faultPort(o, p) && day > o.Days/2 {
				v = outageFloor
			}
		}
	}
	// Clamp into the catalog's advisory bounds by construction (the
	// bounds are advisory for INGEST, but the generator's acceptance rule
	// is stricter: every emitted value respects them — counters/ratios
	// never go negative, curves never escape their plausible range).
	if MetricKeys[mi].MinValue != nil && v < *MetricKeys[mi].MinValue {
		v = *MetricKeys[mi].MinValue
	}
	if MetricKeys[mi].MaxValue != nil && v > *MetricKeys[mi].MaxValue {
		v = *MetricKeys[mi].MaxValue
	}
	return strconv.FormatFloat(v, 'f', b.prec, 64)
}

// faultPort selects the deterministic fault subset (~10% of the port
// universe, chosen purely by the seed hash — the same subset across the
// whole window).
func faultPort(o SynthOptions, p synthPort) bool {
	h := mix64(uint64(o.Seed)) ^ mix64(uint64(p.deviceIdx)) ^ mix64(uint64(hashStr(p.portName)))
	return h%10 == 0
}

// jitter derives a deterministic small offset in [-amp, +amp] from the
// (seed, device, port, metric, day) tuple — same inputs, same value, no
// clock, no global RNG state. NOTE: the modulo runs on the uint64 hash
// (NOT its int64 conversion) so the range is symmetric [0, 2000] →
// [-amp, +amp]; int64(h)%2001 would go negative for high-bit hashes and
// skew the range to [-3*amp, +amp] (the bug the curve-shape test caught).
func jitter(seed int64, deviceIdx int, portName string, mi, day int, amp float64) float64 {
	h := mix64(uint64(seed)) ^ mix64(uint64(deviceIdx)) ^ mix64(uint64(hashStr(portName))) ^
		mix64(uint64(mi)) ^ mix64(uint64(day))
	// map to [-amp, +amp]: 2000 steps of amp/1000.
	steps := float64(h%2001) - 1000.0
	return steps * amp / 1000.0
}

// hashStr is a stable string hash (FNV-1a 64).
func hashStr(s string) uint64 {
	var h uint64 = 14695981039346656037
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

// mix64 is splitmix64's finalizer — a cheap deterministic avalanche.
func mix64(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}

// writeSheetWorkbook writes one xlsx file with a single named sheet
// (headers row + string rows) — the testfixtures writeWorkbook shape.
func writeSheetWorkbook(path, sheet string, headers []string, rows [][]string) error {
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	if err := f.SetSheetName("Sheet1", sheet); err != nil {
		return fmt.Errorf("metrics: synthdata: rename default sheet in %s: %w", path, err)
	}
	if err := writeSheetCells(f, sheet, headers, rows); err != nil {
		return fmt.Errorf("metrics: synthdata: write %s: %w", path, err)
	}
	return writeXLSXFile(f, path)
}

// writeResourcesWorkbook writes resources.xlsx with the two sheets
// (device first — the E003 round-trip ordering of the existing channel).
func writeResourcesWorkbook(path string, o SynthOptions, ports []synthPort) error {
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	if err := f.SetSheetName("Sheet1", synthDeviceSheet); err != nil {
		return fmt.Errorf("metrics: synthdata: name device sheet: %w", err)
	}
	if err := writeSheetCells(f, synthDeviceSheet, synthDeviceHeaders, synthDeviceRows(o)); err != nil {
		return err
	}
	if _, err := f.NewSheet(synthPortSheet); err != nil {
		return fmt.Errorf("metrics: synthdata: create port sheet: %w", err)
	}
	if err := writeSheetCells(f, synthPortSheet, synthPortHeaders, synthPortRows(o, ports)); err != nil {
		return err
	}
	return writeXLSXFile(f, path)
}

// writeSheetCells writes the header row and data rows as strings.
func writeSheetCells(f *excelize.File, sheet string, headers []string, rows [][]string) error {
	for c, h := range headers {
		cell, err := excelize.CoordinatesToCellName(c+1, 1)
		if err != nil {
			return err
		}
		if err := f.SetCellStr(sheet, cell, h); err != nil {
			return fmt.Errorf("write header %s: %w", cell, err)
		}
	}
	for r, row := range rows {
		for c, v := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, r+2)
			if err != nil {
				return err
			}
			if err := f.SetCellStr(sheet, cell, v); err != nil {
				return fmt.Errorf("write cell %s: %w", cell, err)
			}
		}
	}
	return nil
}

// writeXLSXFile persists f to path, truncating an existing file.
func writeXLSXFile(f *excelize.File, path string) error {
	if err := f.SaveAs(path); err != nil {
		return fmt.Errorf("metrics: synthdata: save %s: %w", path, err)
	}
	return nil
}

// writeSynthREADME writes the origin statement + regeneration command.
func writeSynthREADME(o SynthOptions, files []string) error {
	var sb strings.Builder
	sb.WriteString("# 合成数据（synthdata）\n\n")
	sb.WriteString("本目录全部文件由 NetOps Lab 合成数据生成器产出（`netopsfees synthdata`），\n")
	sb.WriteString("非真实网络数据、非任何第三方数据源——无第三方许可约束（设计 §8/§12-A5）。\n")
	sb.WriteString("生成参数：\n\n")
	fmt.Fprintf(&sb, "- devices: %d\n- ports/device: %d\n- days: %d\n- scenario: %s\n- seed: %d\n- metric columns: %d\n\n",
		o.Devices, o.Ports, o.Days, o.Scenario, o.Seed, o.Metrics)
	sb.WriteString("再生成命令（同种子 → 字节级相同输出；README 所在目录即数据目录，故 --out 用 `.`）：\n\n")
	fmt.Fprintf(&sb, "    netopsfees synthdata --out . --devices %d --ports %d --days %d --scenario %s --seed %d --metrics %d\n\n",
		o.Devices, o.Ports, o.Days, o.Scenario, o.Seed, o.Metrics)
	sb.WriteString("文件清单：\n\n")
	for _, name := range files {
		fmt.Fprintf(&sb, "- %s\n", name)
	}
	if err := os.WriteFile(filepath.Join(o.Out, "README.md"), []byte(sb.String()), 0o644); err != nil {
		return fmt.Errorf("metrics: synthdata: write README: %w", err)
	}
	return nil
}
