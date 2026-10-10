package testfixtures

// Geographic demo fixtures of the 黑龙江 real-network twin demo
// (geo pipeline, schema v6). Device codes, names, models, IPs and circuit
// labels reuse the identifiers of the operator's real export; the
// COORDINATES are demo approximations of the real sites (rounded to ~3
// decimals, public knowledge level) — good enough for a geographic topology
// view, never for site engineering. This note is mirrored in
// docs/data-model.md and scripts/demo-geo-lab.sh.
//
// Topology: a 哈尔滨(126.6,45.7) → 大庆 → 齐齐哈尔 → 加格达奇 → 塔河 →
// 漠河(122.5,52.9) north-south trunk (10G between aggregations, 1G to the
// tail base stations), plus the 地震局 10M long-haul spur
// MSTHL1060715/-Z (哈尔滨南岗 → 大庆 → 大兴安岭种子园) and the 动力机房2
// base-station tail GD-202366 (工单 202366). HLB-MH-BS1 is 停用 so the
// materialized workspace demonstrates a down device on the map.

import (
	"fmt"
	"path/filepath"
)

// GeoDeviceSheetName is the sheet name of geodevices.xlsx.
const GeoDeviceSheetName = "设备"

// GeoCircuitSheetName is the sheet name of geocircuits.xlsx.
const GeoCircuitSheetName = "电路"

// GeoDeviceHeaders is the exact header row (order included) of the 设备
// sheet of geodevices.xlsx — the §6 device header plus the two split geo
// columns 经度/纬度 (geo_lng/geo_lat).
var GeoDeviceHeaders = []string{"设备编码", "设备名称", "城市", "厂商", "型号", "管理IP", "状态", "经度", "纬度"}

// GeoCircuitHeaders is the exact header row of the 电路 sheet of
// geocircuits.xlsx (identical to the synthetic circuits workbook).
var GeoCircuitHeaders = []string{"电路编号", "A端设备", "Z端设备", "带宽Mbps", "状态"}

// Row counts (asserted by WriteGeo).
const (
	GeoDeviceTotal  = 10
	GeoCircuitTotal = 10
)

// GeoDeviceRow is one row of the geodevices.xlsx 设备 sheet.
type GeoDeviceRow struct {
	DeviceCode string // 设备编码
	DeviceName string // 设备名称
	City       string // 城市
	Vendor     string // 厂商
	Model      string // 型号
	MgmtIP     string // 管理IP
	Status     string // 状态
	Lng        string // 经度 (demo approximation)
	Lat        string // 纬度 (demo approximation)
}

// Cells returns the row values in header (column) order.
func (r GeoDeviceRow) Cells() []string {
	return []string{r.DeviceCode, r.DeviceName, r.City, r.Vendor, r.Model, r.MgmtIP, r.Status, r.Lng, r.Lat}
}

// GeoCircuitRow is one row of the geocircuits.xlsx 电路 sheet.
type GeoCircuitRow struct {
	CircuitCode   string // 电路编号
	AEndDevice    string // A端设备
	ZEndDevice    string // Z端设备
	BandwidthMbps string // 带宽Mbps
	Status        string // 状态
}

// Cells returns the row values in header (column) order.
func (r GeoCircuitRow) Cells() []string {
	return []string{r.CircuitCode, r.AEndDevice, r.ZEndDevice, r.BandwidthMbps, r.Status}
}

// GeoDeviceRows is the full 设备 sheet (10 rows, import order = sheet
// order). Coordinates are demo approximations of the real sites.
var GeoDeviceRows = []GeoDeviceRow{
	{"HLB-A4", "枢纽楼A4", "哈尔滨", "中兴", "ZXR-6180H", "4.28.2.3", "在网", "126.636", "45.773"},       // 省会枢纽
	{"HLB-DLZS2", "动力机房2", "哈尔滨", "中兴", "ZXR-6150", "172.7.10.125", "在网", "126.610", "45.721"}, // 动环监控基站，归属枢纽楼A4 gei_1/11
	{"HLB-ZYXY1", "职业学院1号1", "哈尔滨", "中兴", "ZXR-6150", "4.8.12.14", "在网", "126.638", "45.748"},  // 地震局专线A端
	{"HLB-DQ-AGG", "大庆汇聚", "大庆", "中兴", "ZXR-6120H", "4.8.10.1", "在网", "125.103", "46.590"},
	{"HLB-QQH-AGG", "齐齐哈尔汇聚", "齐齐哈尔", "中兴", "ZXR-6120H", "4.8.10.5", "在网", "123.918", "47.354"},
	{"HLB-JGDC-AGG", "加格达奇汇聚", "大兴安岭", "中兴", "ZXR-6130XG-S", "4.8.10.9", "在网", "124.114", "50.424"},
	{"HLB-ZZY1", "种子园A1", "大兴安岭", "中兴", "ZXR-6150", "4.8.12.55", "在网", "124.120", "51.980"},  // 专线Z端·大兴
	{"HLB-HZ-Y1", "源1", "大兴安岭", "中兴", "ZXR-6130XG-S", "4.8.14.8", "在网", "123.596", "51.937"}, // 呼中区局站
	{"HLB-TH-BS1", "塔河基站1", "大兴安岭", "中兴", "ZXR-6150", "172.7.11.129", "在网", "124.700", "52.383"},
	{"HLB-MH-BS1", "漠河基站1", "漠河", "中兴", "ZXR-6150", "172.7.11.130", "停用", "122.536", "52.972"}, // 停机 → 物化后 down
}

// GeoCircuitRows is the full 电路 sheet (10 rows). Circuit labels reuse the
// real 专线编号 where available (MSTHL1060715, GD-202366); the CKT-* labels
// are demo stand-ins for the trunk segments.
var GeoCircuitRows = []GeoCircuitRow{
	{"MSTHL1060715", "HLB-ZYXY1", "HLB-DQ-AGG", "10", "在网"},   // 地震局专线A段：哈尔滨南岗→大庆
	{"MSTHL1060715-Z", "HLB-DQ-AGG", "HLB-ZZY1", "10", "在网"},  // 地震局专线Z段：大庆→大兴安岭种子园（跨省北向长专线）
	{"CKT-HRB-DQ-10G", "HLB-A4", "HLB-DQ-AGG", "10000", "在网"}, // 哈大 10G 干线
	{"CKT-DQ-QQH-10G", "HLB-DQ-AGG", "HLB-QQH-AGG", "10000", "在网"},
	{"CKT-QQH-JGDC-10G", "HLB-QQH-AGG", "HLB-JGDC-AGG", "10000", "在网"},
	{"CKT-JGDC-ZZY-1G", "HLB-JGDC-AGG", "HLB-ZZY1", "1000", "在网"},
	{"CKT-JGDC-HZ-1G", "HLB-JGDC-AGG", "HLB-HZ-Y1", "1000", "在网"}, // 加格达奇→呼中源1
	{"CKT-JGDC-TH-1G", "HLB-JGDC-AGG", "HLB-TH-BS1", "1000", "在网"},
	{"CKT-TH-MH-1G", "HLB-TH-BS1", "HLB-MH-BS1", "1000", "在网"}, // 北向末梢
	{"GD-202366", "HLB-A4", "HLB-DLZS2", "100", "在网"},          // 动力机房2基站归属电路，工单202366
}

// GeoGeneratedFiles are the artifacts WriteGeo produces, in write order.
var GeoGeneratedFiles = []string{"geodevices.xlsx", "geocircuits.xlsx"}

// WriteGeo writes the two geographic demo workbooks (geodevices.xlsx +
// geocircuits.xlsx) into out (created when missing). It reuses the same
// deterministic workbook writer as WriteAll — fixed literals, fixed doc
// properties, no timestamps — and deliberately does NOT touch the four
// canonical WriteAll artifacts, which stay byte-identical.
//
// Import order contract: geodevices.xlsx → geocircuits.xlsx into a FRESH
// database (circuit a_end/z_end references resolve against the devices of
// the earlier batch). All 10 device rows import clean (管理IP distinct,
// statuses within the enum); all 10 circuit rows import clean.
func WriteGeo(out string) error {
	if len(GeoDeviceRows) != GeoDeviceTotal || len(GeoCircuitRows) != GeoCircuitTotal {
		return fmt.Errorf("geo fixture count mismatch: devices=%d (want %d), circuits=%d (want %d)",
			len(GeoDeviceRows), GeoDeviceTotal, len(GeoCircuitRows), GeoCircuitTotal)
	}
	if err := writeWorkbook(filepath.Join(out, "geodevices.xlsx"), GeoDeviceSheetName,
		GeoDeviceHeaders, cellsOf(GeoDeviceRows)); err != nil {
		return err
	}
	if err := writeWorkbook(filepath.Join(out, "geocircuits.xlsx"), GeoCircuitSheetName,
		GeoCircuitHeaders, cellsOf(GeoCircuitRows)); err != nil {
		return err
	}
	return nil
}
