// Package testfixtures holds the deterministic synthetic test data of
// NetOps Lab (TASK_PROTOCOL §4). Every row is a fixed literal — there is NO
// randomness anywhere, so re-running cmd/testdatagen always produces the
// same business data.
//
// The three row slices (DeviceRows, IPPoolRows, CircuitRows) feed both the
// Excel workbook generator (cmd/testdatagen writes them into
// testdata/*.xlsx, one sheet per file, header row 1, data from row 2) and
// the tests that assert the seeded data-quality issues.
//
// Field names correspond 1:1 to the workbook headers (see the *Header*
// constants and the *Cells method of each row type); values are the raw
// strings exactly as they will appear in the Excel cells, BEFORE any
// normalization.
//
// Import order contract: devices → ippools → circuits into a FRESH
// database. Pool/circuit device references resolve against the devices
// imported by the earlier batch.
//
// Seeded issue map (rule codes as defined in internal/domain):
//
//	devices  E001:1 (dup DEV-012, second copy), E002:1 (empty name),
//	         E004:1 (mgmt IP 192.168.1.300), W002:1 (status 未知, imported),
//	         E006:1 (set: DEV-026/DEV-027 share 10.10.10.5, both imported),
//	         I001:1 (set: DEV-030 has no relations)
//	         → 30 rows: 27 imported, 0 skipped, 3 failed
//	ippools  E002:1 (empty CIDR), E004:2 (CIDR 10.20.0/24, VLAN abc),
//	         E003:1 (DEV-9999), W001:1 (empty 归属设备, imported),
//	         E005:1 (set: 10.30.1.0/24 overlaps 10.30.1.128/25, both imported)
//	         → 20 rows: 16 imported, 0 skipped, 4 failed
//	circuits E001:1 (dup CKT-018, second copy), E002:1 (empty Z端设备),
//	         E003:1 (DEV-8888), E004:1 (bandwidth N/A),
//	         W003:1 (A==Z DEV-005, imported)
//	         → 25 rows: 21 imported, 0 skipped, 4 failed
//
// Only error-severity rules (E…) fail a row; warning/info rules (W…/I…)
// leave the row imported (PROJECT_BRIEF §7).
package testfixtures

// Sheet names of the three generated workbooks.
const (
	DeviceSheetName  = "设备清单"
	IPPoolSheetName  = "地址池"
	CircuitSheetName = "电路"
)

// DeviceHeaders is the exact header row (order included) of the 设备清单
// sheet.
var DeviceHeaders = []string{"设备编码", "设备名称", "城市", "厂商", "型号", "管理IP", "状态"}

// IPPoolHeaders is the exact header row of the 地址池 sheet.
var IPPoolHeaders = []string{"地址池名称", "CIDR", "网关", "VLAN", "归属设备"}

// CircuitHeaders is the exact header row of the 电路 sheet.
var CircuitHeaders = []string{"电路编号", "A端设备", "Z端设备", "带宽Mbps", "状态"}

// Row counts. These must match the len() of the slices below; the
// testfixtures tests assert it.
const (
	DeviceTotal  = 30
	IPPoolTotal  = 20
	CircuitTotal = 25
)

// Seeded values used by the data-quality rules (rule codes in comments).
const (
	DeviceDupCode        = "DEV-012"        // E001: second row with this code fails
	DeviceInvalidIP      = "192.168.1.300"  // E004: not a dotted quad
	DeviceUnknownStatus  = "未知"             // W002: not in the status enum
	DeviceSharedMgmtIP   = "10.10.10.5"     // E006: two devices share this IP
	DeviceUnlinkedCode   = "DEV-030"        // I001: no circuit or pool references it
	IPPoolInvalidCIDR    = "10.20.0/24"     // E004: malformed CIDR
	IPPoolOverlapCIDRA   = "10.30.1.0/24"   // E005: overlaps …
	IPPoolOverlapCIDRB   = "10.30.1.128/25" // E005: … this /25 inside the /24
	IPPoolInvalidVLAN    = "abc"            // E004: not an int
	IPPoolUnknownDevice  = "DEV-9999"       // E003: no such device
	CircuitDupCode       = "CKT-018"        // E001: second row with this code fails
	CircuitUnknownDevice = "DEV-8888"       // E003: no such device
	CircuitBadBandwidth  = "N/A"            // E004: not an int
	CircuitSelfLoopDev   = "DEV-005"        // W003: a_end == z_end
)

// Seed row locators: 1-based Excel row numbers (row 1 is the header row,
// data starts at row 2; row number = slice index + 2).
const (
	// Devices.
	DeviceDupFirstRow      = 13 // DEV-012 first (valid) copy — imported
	DeviceDupSecondRow     = 27 // DEV-012 second copy (different name) → E001
	DeviceInvalidIPRow     = 24 // DEV-023, 管理IP 192.168.1.300 → E004
	DeviceMissingNameRow   = 25 // DEV-024, empty 设备名称 → E002
	DeviceUnknownStatusRow = 26 // DEV-025, status 未知 → W002 (imported)
	DeviceSharedIPRowA     = 28 // DEV-026, 管理IP 10.10.10.5 → E006 pair
	DeviceSharedIPRowB     = 29 // DEV-027, 管理IP 10.10.10.5 → E006 pair
	DeviceUnlinkedRow      = 31 // DEV-030 → I001 (no relations)

	// IP pools.
	IPPoolOrphanRefRow   = 15 // POOL-14, empty 归属设备 → W001 (imported)
	IPPoolOverlapRowA    = 16 // POOL-15, 10.30.1.0/24 → E005 pair
	IPPoolOverlapRowB    = 17 // POOL-16, 10.30.1.128/25 → E005 pair
	IPPoolMissingCIDRRow = 18 // POOL-17, empty CIDR → E002
	IPPoolBadCIDRRow     = 19 // POOL-18, 10.20.0/24 → E004
	IPPoolBadVLANRow     = 20 // POOL-19, VLAN abc → E004
	IPPoolUnknownRefRow  = 21 // POOL-20, DEV-9999 → E003

	// Circuits.
	CircuitDupFirstRow     = 19 // CKT-018 first (valid) copy — imported
	CircuitDupSecondRow    = 26 // CKT-018 second copy → E001
	CircuitSelfLoopRow     = 22 // CKT-021, A端==Z端==DEV-005 → W003 (imported)
	CircuitMissingZRow     = 23 // CKT-022, empty Z端设备 → E002
	CircuitUnknownRefRow   = 24 // CKT-023, A端 DEV-8888 → E003
	CircuitBadBandwidthRow = 25 // CKT-024, 带宽Mbps N/A → E004
)

// Topology seeds. CKT-001..CKT-020 form the chain DEV-001→DEV-002→…→DEV-021
// (CKT-00n: A端 DEV-00n, Z端 DEV-00(n+1)), so DEV-001→DEV-002→DEV-003 is a
// known 2-hop path, DEV-021 is reachable from DEV-001 in 20 hops, and
// DEV-022..DEV-029 carry no circuit edges (unreachable from DEV-001).
const (
	CircuitChainLen   = 20 // number of chain rows (CKT-001..CKT-020)
	CircuitChainFirst = 1  // first chain circuit number
	CircuitChainLast  = 20 // last chain circuit number
	DeviceChainStart  = 1  // chain starts at DEV-001
	DeviceChainEnd    = 21 // chain ends at DEV-021
)

// DeviceRow is one row of the 设备清单 sheet. Fields map 1:1 onto
// DeviceHeaders; Cells returns them in sheet column order.
type DeviceRow struct {
	DeviceCode string // 设备编码
	DeviceName string // 设备名称
	City       string // 城市
	Vendor     string // 厂商
	Model      string // 型号
	MgmtIP     string // 管理IP
	Status     string // 状态
}

// Cells returns the row values in header (column) order.
func (r DeviceRow) Cells() []string {
	return []string{r.DeviceCode, r.DeviceName, r.City, r.Vendor, r.Model, r.MgmtIP, r.Status}
}

// IPPoolRow is one row of the 地址池 sheet.
type IPPoolRow struct {
	PoolName       string // 地址池名称
	CIDR           string // CIDR
	Gateway        string // 网关
	VLAN           string // VLAN
	AssignedDevice string // 归属设备
}

// Cells returns the row values in header (column) order.
func (r IPPoolRow) Cells() []string {
	return []string{r.PoolName, r.CIDR, r.Gateway, r.VLAN, r.AssignedDevice}
}

// CircuitRow is one row of the 电路 sheet.
type CircuitRow struct {
	CircuitCode   string // 电路编号
	AEndDevice    string // A端设备
	ZEndDevice    string // Z端设备
	BandwidthMbps string // 带宽Mbps
	Status        string // 状态
}

// Cells returns the row values in header (column) order.
func (r CircuitRow) Cells() []string {
	return []string{r.CircuitCode, r.AEndDevice, r.ZEndDevice, r.BandwidthMbps, r.Status}
}

// DeviceRows is the full 设备清单 sheet (30 rows). Layout notes:
//
//   - rows 0-21: DEV-001..DEV-022, clean (row 22=DEV-023 has the bad IP,
//     row 23=DEV-024 the empty name, row 24=DEV-025 the unknown status);
//   - row 25: the second DEV-012 copy (E001 — displaces a DEV-029 row, so
//     DEV-029 never appears in the sheet);
//   - rows 26-27: the 10.10.10.5 pair (E006);
//   - row 28: DEV-028, clean;
//   - row 29: DEV-030 — referenced by nothing (I001).
//
// Cities rotate 杭州/宁波/嘉兴/温州/上海, vendors 华为/中兴/烽火, models
// NE40E/ATN910C/ZXCTN6100 (role in the name follows the model). Management
// IPs are 10.10.<n>.<n> per device (n = code number) except the seeds.
var DeviceRows = []DeviceRow{
	{"DEV-001", "杭州-核心路由器-001", "杭州", "华为", "NE40E", "10.10.1.1", "在网"},
	{"DEV-002", "宁波-接入路由器-002", "宁波", "中兴", "ATN910C", "10.10.2.2", "在网"},
	{"DEV-003", "嘉兴-传输设备-003", "嘉兴", "烽火", "ZXCTN6100", "10.10.3.3", "在网"},
	{"DEV-004", "温州-核心路由器-004", "温州", "华为", "NE40E", "10.10.4.4", "在网"},
	{"DEV-005", "上海-接入路由器-005", "上海", "中兴", "ATN910C", "10.10.5.5", "在网"},
	{"DEV-006", "杭州-传输设备-006", "杭州", "烽火", "ZXCTN6100", "10.10.6.6", "在网"},
	{"DEV-007", "宁波-核心路由器-007", "宁波", "华为", "NE40E", "10.10.7.7", "在网"},
	{"DEV-008", "嘉兴-接入路由器-008", "嘉兴", "中兴", "ATN910C", "10.10.8.8", "在网"},
	{"DEV-009", "温州-传输设备-009", "温州", "烽火", "ZXCTN6100", "10.10.9.9", "在网"},
	{"DEV-010", "上海-核心路由器-010", "上海", "华为", "NE40E", "10.10.10.10", "在网"},
	{"DEV-011", "杭州-接入路由器-011", "杭州", "中兴", "ATN910C", "10.10.11.11", "在网"},
	{"DEV-012", "宁波-传输设备-012", "宁波", "烽火", "ZXCTN6100", "10.10.12.12", "在网"}, // E001 first copy (row 13)
	{"DEV-013", "嘉兴-核心路由器-013", "嘉兴", "华为", "NE40E", "10.10.13.13", "在网"},
	{"DEV-014", "温州-接入路由器-014", "温州", "中兴", "ATN910C", "10.10.14.14", "在网"},
	{"DEV-015", "上海-传输设备-015", "上海", "烽火", "ZXCTN6100", "10.10.15.15", "退网"},
	{"DEV-016", "杭州-核心路由器-016", "杭州", "华为", "NE40E", "10.10.16.16", "停用"},
	{"DEV-017", "宁波-接入路由器-017", "宁波", "中兴", "ATN910C", "10.10.17.17", "在网"},
	{"DEV-018", "嘉兴-传输设备-018", "嘉兴", "烽火", "ZXCTN6100", "10.10.18.18", "在网"},
	{"DEV-019", "温州-核心路由器-019", "温州", "华为", "NE40E", "10.10.19.19", "在建"},
	{"DEV-020", "杭州-接入路由器-020", "杭州", "中兴", "ATN910C", "10.10.20.20", "在网"},
	{"DEV-021", "宁波-传输设备-021", "宁波", "烽火", "ZXCTN6100", "10.10.21.21", "在网"},
	{"DEV-022", "嘉兴-核心路由器-022", "嘉兴", "华为", "NE40E", "10.10.22.22", "在网"},
	{"DEV-023", "上海-接入路由器-023", "上海", "中兴", "ATN910C", DeviceInvalidIP, "在网"},            // E004 (row 24)
	{"DEV-024", "", "杭州", "烽火", "ZXCTN6100", "10.10.24.24", "在网"},                        // E002 empty name (row 25)
	{"DEV-025", "宁波-核心路由器-025", "宁波", "华为", "NE40E", "10.10.25.25", DeviceUnknownStatus}, // W002 (row 26)
	{DeviceDupCode, "宁波-传输设备-012副本", "宁波", "烽火", "ZXCTN6100", "10.10.32.12", "在网"},       // E001 second copy (row 27); distinct IP so fixture-level 管理IP uniqueness stays intact (row never reaches the DB)
	{"DEV-026", "杭州-接入路由器-026", "杭州", "中兴", "ATN910C", DeviceSharedMgmtIP, "在网"},         // E006 pair (row 28)
	{"DEV-027", "宁波-传输设备-027", "宁波", "烽火", "ZXCTN6100", DeviceSharedMgmtIP, "在网"},        // E006 pair (row 29)
	{"DEV-028", "嘉兴-核心路由器-028", "嘉兴", "华为", "NE40E", "10.10.28.28", "在网"},
	{DeviceUnlinkedCode, "上海-传输设备-030", "上海", "烽火", "ZXCTN6100", "10.10.30.30", "在网"}, // I001 unlinked (row 31)
}

// IPPoolRows is the full 地址池 sheet (20 rows). Rows 0-12 are clean and
// reference imported devices (POOL-09..POOL-13 give the otherwise
// circuit-less devices DEV-022/025/026/027/028 their assigned_to relation
// so that DEV-030 stays the only unlinked device). Row 13 is the W001
// orphan, rows 14-15 the E005 overlap pair, rows 16-19 the four error
// rows. Clean CIDRs are distinct 10.40.x.0/24 networks; gateways are the
// first address of each pool and never collide with device IPs (10.10.x.y).
var IPPoolRows = []IPPoolRow{
	{"POOL-01", "10.40.1.0/24", "10.40.1.1", "100", "DEV-001"},
	{"POOL-02", "10.40.2.0/24", "10.40.2.1", "110", "DEV-002"},
	{"POOL-03", "10.40.3.0/24", "10.40.3.1", "120", "DEV-003"},
	{"POOL-04", "10.40.4.0/24", "10.40.4.1", "130", "DEV-004"},
	{"POOL-05", "10.40.5.0/24", "10.40.5.1", "140", "DEV-005"},
	{"POOL-06", "10.40.6.0/24", "10.40.6.1", "150", "DEV-006"},
	{"POOL-07", "10.40.7.0/24", "10.40.7.1", "160", "DEV-007"},
	{"POOL-08", "10.40.8.0/24", "10.40.8.1", "170", "DEV-008"},
	{"POOL-09", "10.40.9.0/24", "10.40.9.1", "180", "DEV-022"},
	{"POOL-10", "10.40.10.0/24", "10.40.10.1", "190", "DEV-025"},
	{"POOL-11", "10.40.11.0/24", "10.40.11.1", "200", "DEV-026"},
	{"POOL-12", "10.40.12.0/24", "10.40.12.1", "210", "DEV-027"},
	{"POOL-13", "10.40.13.0/24", "10.40.13.1", "220", "DEV-028"},
	{"POOL-14", "10.40.14.0/24", "10.40.14.1", "230", ""},                    // W001 orphan (row 15)
	{"POOL-15", IPPoolOverlapCIDRA, "10.30.1.1", "240", "DEV-011"},           // E005 pair (row 16)
	{"POOL-16", IPPoolOverlapCIDRB, "10.30.1.129", "250", "DEV-012"},         // E005 pair (row 17)
	{"POOL-17", "", "10.40.17.1", "260", "DEV-013"},                          // E002 empty CIDR (row 18)
	{"POOL-18", IPPoolInvalidCIDR, "10.40.18.1", "270", "DEV-014"},           // E004 bad CIDR (row 19)
	{"POOL-19", "10.40.19.0/24", "10.40.19.1", IPPoolInvalidVLAN, "DEV-015"}, // E004 bad VLAN (row 20)
	{"POOL-20", "10.40.20.0/24", "10.40.20.1", "280", IPPoolUnknownDevice},   // E003 unknown ref (row 21)
}

// CircuitRows is the full 电路 sheet (25 rows). Rows 0-19 are the chain
// CKT-001..CKT-020 (A端 DEV-00n → Z端 DEV-00n+1, bandwidth rotating
// 1000/10000/500/2000/100 Mbps); row 20 is the W003 self-loop on DEV-005;
// rows 21-24 are the E002/E003/E004 rows and the second CKT-018 copy (E001,
// displacing CKT-025). Only 在网/停用 statuses are used, all within the
// device status enum.
var CircuitRows = []CircuitRow{
	{"CKT-001", "DEV-001", "DEV-002", "1000", "在网"},
	{"CKT-002", "DEV-002", "DEV-003", "10000", "在网"},
	{"CKT-003", "DEV-003", "DEV-004", "500", "在网"},
	{"CKT-004", "DEV-004", "DEV-005", "2000", "在网"},
	{"CKT-005", "DEV-005", "DEV-006", "100", "在网"},
	{"CKT-006", "DEV-006", "DEV-007", "1000", "在网"},
	{"CKT-007", "DEV-007", "DEV-008", "10000", "停用"},
	{"CKT-008", "DEV-008", "DEV-009", "500", "在网"},
	{"CKT-009", "DEV-009", "DEV-010", "2000", "在网"},
	{"CKT-010", "DEV-010", "DEV-011", "100", "在网"},
	{"CKT-011", "DEV-011", "DEV-012", "1000", "在网"},
	{"CKT-012", "DEV-012", "DEV-013", "10000", "在网"},
	{"CKT-013", "DEV-013", "DEV-014", "500", "在网"},
	{"CKT-014", "DEV-014", "DEV-015", "2000", "在网"},
	{"CKT-015", "DEV-015", "DEV-016", "100", "在网"},
	{"CKT-016", "DEV-016", "DEV-017", "1000", "在网"},
	{"CKT-017", "DEV-017", "DEV-018", "10000", "在网"},
	{"CKT-018", "DEV-018", "DEV-019", "500", "在网"}, // E001 first copy (row 19)
	{"CKT-019", "DEV-019", "DEV-020", "2000", "在网"},
	{"CKT-020", "DEV-020", "DEV-021", "100", "在网"},
	{"CKT-021", CircuitSelfLoopDev, CircuitSelfLoopDev, "1000", "在网"}, // W003 self-loop (row 22)
	{"CKT-022", "DEV-006", "", "2000", "在网"},                          // E002 empty Z端 (row 23)
	{"CKT-023", CircuitUnknownDevice, "DEV-007", "500", "在网"},         // E003 unknown A端 (row 24)
	{"CKT-024", "DEV-008", "DEV-009", CircuitBadBandwidth, "在网"},      // E004 bad bandwidth (row 25)
	{CircuitDupCode, "DEV-018", "DEV-019", "1000", "在网"},              // E001 second copy (row 26)
}

// FindDeviceRow returns the 1-based Excel row number of the FIRST DeviceRow
// whose 设备编码 equals code, or 0 when there is no such row. (For the
// duplicated DEV-012 use DeviceDupFirstRow / DeviceDupSecondRow.)
func FindDeviceRow(code string) int {
	for i, r := range DeviceRows {
		if r.DeviceCode == code {
			return i + 2
		}
	}
	return 0
}

// FindIPPoolRow returns the 1-based Excel row number of the IPPoolRow whose
// 地址池名称 equals name, or 0 when there is no such row.
func FindIPPoolRow(name string) int {
	for i, r := range IPPoolRows {
		if r.PoolName == name {
			return i + 2
		}
	}
	return 0
}

// FindCircuitRow returns the 1-based Excel row number of the FIRST
// CircuitRow whose 电路编号 equals code, or 0 when there is no such row.
// (For the duplicated CKT-018 use CircuitDupFirstRow / CircuitDupSecondRow.)
func FindCircuitRow(code string) int {
	for i, r := range CircuitRows {
		if r.CircuitCode == code {
			return i + 2
		}
	}
	return 0
}
