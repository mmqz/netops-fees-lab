package testfixtures

import (
	"fmt"
	"net/netip"
	"strconv"
	"testing"
)

// deviceStatusEnum is the status enum shared by devices and circuits
// (PROJECT_BRIEF §6).
var deviceStatusEnum = map[string]bool{
	"在网": true,
	"停用": true,
	"在建": true,
	"退网": true,
}

// row returns the fixture row at 1-based Excel row number n
// (row 1 is the header, so n = slice index + 2).
func row(n int) int { return n - 2 }

func TestRowCountsMatchConstants(t *testing.T) {
	if len(DeviceRows) != DeviceTotal {
		t.Errorf("len(DeviceRows) = %d, want DeviceTotal = %d", len(DeviceRows), DeviceTotal)
	}
	if len(IPPoolRows) != IPPoolTotal {
		t.Errorf("len(IPPoolRows) = %d, want IPPoolTotal = %d", len(IPPoolRows), IPPoolTotal)
	}
	if len(CircuitRows) != CircuitTotal {
		t.Errorf("len(CircuitRows) = %d, want CircuitTotal = %d", len(CircuitRows), CircuitTotal)
	}
	if len(DeviceHeaders) != 7 || len(IPPoolHeaders) != 5 || len(CircuitHeaders) != 5 {
		t.Errorf("header counts = %d/%d/%d, want 7/5/5",
			len(DeviceHeaders), len(IPPoolHeaders), len(CircuitHeaders))
	}
}

func TestDeviceSeededRows(t *testing.T) {
	// The duplicate-key pair: same code, different name, first copy valid.
	first := DeviceRows[row(DeviceDupFirstRow)]
	second := DeviceRows[row(DeviceDupSecondRow)]
	if first.DeviceCode != DeviceDupCode || second.DeviceCode != DeviceDupCode {
		t.Errorf("dup pair codes = %q/%q, want both %q", first.DeviceCode, second.DeviceCode, DeviceDupCode)
	}
	if first.DeviceName == second.DeviceName {
		t.Errorf("dup pair must have different names, both %q", first.DeviceName)
	}
	if first.DeviceName == "" {
		t.Errorf("first DEV-012 copy must have a non-empty name")
	}

	// Missing 设备名称.
	if r := DeviceRows[row(DeviceMissingNameRow)]; r.DeviceName != "" {
		t.Errorf("row %d DeviceName = %q, want empty (E002 seed)", DeviceMissingNameRow, r.DeviceName)
	}

	// Invalid 管理IP.
	if r := DeviceRows[row(DeviceInvalidIPRow)]; r.MgmtIP != DeviceInvalidIP {
		t.Errorf("row %d MgmtIP = %q, want %q (E004 seed)", DeviceInvalidIPRow, r.MgmtIP, DeviceInvalidIP)
	}
	if _, err := netip.ParseAddr(DeviceInvalidIP); err == nil {
		t.Errorf("%q must not parse as an IPv4 address", DeviceInvalidIP)
	}

	// Unknown status.
	if r := DeviceRows[row(DeviceUnknownStatusRow)]; r.Status != DeviceUnknownStatus {
		t.Errorf("row %d Status = %q, want %q (W002 seed)", DeviceUnknownStatusRow, r.Status, DeviceUnknownStatus)
	}
	if deviceStatusEnum[DeviceUnknownStatus] {
		t.Errorf("%q must not be a known status", DeviceUnknownStatus)
	}

	// Shared 管理IP pair.
	a := DeviceRows[row(DeviceSharedIPRowA)]
	b := DeviceRows[row(DeviceSharedIPRowB)]
	if a.MgmtIP != DeviceSharedMgmtIP || b.MgmtIP != DeviceSharedMgmtIP {
		t.Errorf("E006 pair IPs = %q/%q, want both %q", a.MgmtIP, b.MgmtIP, DeviceSharedMgmtIP)
	}
	if a.DeviceCode == b.DeviceCode {
		t.Errorf("E006 pair must be two different devices, both %q", a.DeviceCode)
	}

	// The unlinked device.
	if r := DeviceRows[row(DeviceUnlinkedRow)]; r.DeviceCode != DeviceUnlinkedCode {
		t.Errorf("row %d DeviceCode = %q, want %q (I001 seed)", DeviceUnlinkedRow, r.DeviceCode, DeviceUnlinkedCode)
	}
}

func TestDeviceCodeUniqueness(t *testing.T) {
	// Exactly one code (the seeded DEV-012) appears twice; every other code
	// appears exactly once, so 30 rows carry 29 distinct codes.
	counts := map[string]int{}
	for _, r := range DeviceRows {
		counts[r.DeviceCode]++
	}
	dups := 0
	for code, n := range counts {
		switch n {
		case 2:
			dups++
			if code != DeviceDupCode {
				t.Errorf("unexpected duplicated device code %q", code)
			}
		case 1:
		default:
			t.Errorf("device code %q appears %d times, want 1 or 2", code, n)
		}
	}
	if dups != 1 {
		t.Errorf("number of duplicated device codes = %d, want exactly 1", dups)
	}
	if len(counts) != DeviceTotal-1 {
		t.Errorf("distinct device codes = %d, want %d", len(counts), DeviceTotal-1)
	}
}

func TestDeviceMgmtIPUniqueness(t *testing.T) {
	counts := map[string]int{}
	for _, r := range DeviceRows {
		if r.MgmtIP == "" {
			t.Errorf("device %s has an empty 管理IP, want a literal value in every row", r.DeviceCode)
		}
		counts[r.MgmtIP]++
	}
	dups := 0
	for ip, n := range counts {
		switch n {
		case 2:
			dups++
			if ip != DeviceSharedMgmtIP {
				t.Errorf("unexpected duplicated 管理IP %q", ip)
			}
		case 1:
		default:
			t.Errorf("管理IP %q appears %d times, want 1 or 2", ip, n)
		}
	}
	if dups != 1 {
		t.Errorf("number of duplicated 管理IPs = %d, want exactly 1 (the E006 pair)", dups)
	}

	// Every clean IP parses as a valid IPv4 dotted quad (the E004 seed and
	// the not-imported duplicate copy are the only exceptions).
	for _, r := range DeviceRows {
		if r.MgmtIP == DeviceInvalidIP {
			continue
		}
		if _, err := netip.ParseAddr(r.MgmtIP); err != nil {
			t.Errorf("device %s 管理IP %q does not parse as an IPv4 address: %v", r.DeviceCode, r.MgmtIP, err)
		}
	}
}

func TestDeviceFieldDomains(t *testing.T) {
	cities := map[string]bool{"杭州": true, "宁波": true, "嘉兴": true, "温州": true, "上海": true}
	vendors := map[string]bool{"华为": true, "中兴": true, "烽火": true}
	models := map[string]bool{"NE40E": true, "ATN910C": true, "ZXCTN6100": true}
	for i, r := range DeviceRows {
		if !cities[r.City] {
			t.Errorf("row %d (%s): city %q is outside the realistic domain", i+2, r.DeviceCode, r.City)
		}
		if !vendors[r.Vendor] {
			t.Errorf("row %d (%s): vendor %q is outside the realistic domain", i+2, r.DeviceCode, r.Vendor)
		}
		if !models[r.Model] {
			t.Errorf("row %d (%s): model %q is outside the realistic domain", i+2, r.DeviceCode, r.Model)
		}
		// Status must be a known enum value, except the W002 seed.
		if r.Status != DeviceUnknownStatus && !deviceStatusEnum[r.Status] {
			t.Errorf("row %d (%s): unexpected status %q", i+2, r.DeviceCode, r.Status)
		}
		// Every non-seed row must have a name.
		if r.DeviceName == "" && i != row(DeviceMissingNameRow) {
			t.Errorf("row %d (%s): empty 设备名称 outside the E002 seed", i+2, r.DeviceCode)
		}
	}
}

func TestIPPoolSeededRows(t *testing.T) {
	if r := IPPoolRows[row(IPPoolMissingCIDRRow)]; r.CIDR != "" {
		t.Errorf("row %d CIDR = %q, want empty (E002 seed)", IPPoolMissingCIDRRow, r.CIDR)
	}
	if r := IPPoolRows[row(IPPoolBadCIDRRow)]; r.CIDR != IPPoolInvalidCIDR {
		t.Errorf("row %d CIDR = %q, want %q (E004 seed)", IPPoolBadCIDRRow, r.CIDR, IPPoolInvalidCIDR)
	}
	if _, err := netip.ParsePrefix(IPPoolInvalidCIDR); err == nil {
		t.Errorf("%q must not parse as a CIDR", IPPoolInvalidCIDR)
	}
	if r := IPPoolRows[row(IPPoolBadVLANRow)]; r.VLAN != IPPoolInvalidVLAN {
		t.Errorf("row %d VLAN = %q, want %q (E004 seed)", IPPoolBadVLANRow, r.VLAN, IPPoolInvalidVLAN)
	}
	if r := IPPoolRows[row(IPPoolOrphanRefRow)]; r.AssignedDevice != "" {
		t.Errorf("row %d 归属设备 = %q, want empty (W001 seed)", IPPoolOrphanRefRow, r.AssignedDevice)
	}
	if r := IPPoolRows[row(IPPoolUnknownRefRow)]; r.AssignedDevice != IPPoolUnknownDevice {
		t.Errorf("row %d 归属设备 = %q, want %q (E003 seed)", IPPoolUnknownRefRow, r.AssignedDevice, IPPoolUnknownDevice)
	}
	if a, b := IPPoolRows[row(IPPoolOverlapRowA)].CIDR, IPPoolRows[row(IPPoolOverlapRowB)].CIDR; a != IPPoolOverlapCIDRA || b != IPPoolOverlapCIDRB {
		t.Errorf("E005 pair CIDRs = %q/%q, want %q/%q", a, b, IPPoolOverlapCIDRA, IPPoolOverlapCIDRB)
	}
}

func TestIPPoolCleanInvariants(t *testing.T) {
	// Pool names are unique and every row has one.
	names := map[string]bool{}
	for i, r := range IPPoolRows {
		if r.PoolName == "" {
			t.Errorf("row %d has an empty 地址池名称", i+2)
		}
		if names[r.PoolName] {
			t.Errorf("duplicated 地址池名称 %q", r.PoolName)
		}
		names[r.PoolName] = true
	}

	// VLANs parse as ints in 1..4094, except the E004 seed.
	for i, r := range IPPoolRows {
		if r.VLAN == IPPoolInvalidVLAN {
			continue
		}
		v, err := strconv.Atoi(r.VLAN)
		if err != nil {
			t.Errorf("row %d VLAN %q does not parse as an int: %v", i+2, r.VLAN, err)
			continue
		}
		if v < 1 || v > 4094 {
			t.Errorf("row %d VLAN = %d, outside 1..4094", i+2, v)
		}
	}

	// Gateways parse as IPv4 and are pairwise distinct.
	gateways := map[string]bool{}
	for i, r := range IPPoolRows {
		if r.Gateway == "" {
			t.Errorf("row %d has an empty 网关", i+2)
		}
		addr, err := netip.ParseAddr(r.Gateway)
		if err != nil {
			t.Errorf("row %d 网关 %q does not parse as an IPv4 address: %v", i+2, r.Gateway, err)
			continue
		}
		if gateways[r.Gateway] {
			t.Errorf("duplicated 网关 %q (would trigger E006)", r.Gateway)
		}
		gateways[r.Gateway] = true
		_ = addr
	}

	// The only overlapping CIDR pair among rows with a parseable CIDR is
	// the seeded E005 pair.
	type pc struct {
		row    int
		prefix netip.Prefix
	}
	var prefixes []pc
	for i, r := range IPPoolRows {
		if r.CIDR == "" || r.CIDR == IPPoolInvalidCIDR {
			continue
		}
		p, err := netip.ParsePrefix(r.CIDR)
		if err != nil {
			t.Errorf("row %d CIDR %q does not parse: %v", i+2, r.CIDR, err)
			continue
		}
		prefixes = append(prefixes, pc{i + 2, p.Masked()})
	}
	for i := 0; i < len(prefixes); i++ {
		for j := i + 1; j < len(prefixes); j++ {
			if !prefixes[i].prefix.Overlaps(prefixes[j].prefix) {
				continue
			}
			seeded := (prefixes[i].row == IPPoolOverlapRowA && prefixes[j].row == IPPoolOverlapRowB) ||
				(prefixes[i].row == IPPoolOverlapRowB && prefixes[j].row == IPPoolOverlapRowA)
			if !seeded {
				t.Errorf("unexpected CIDR overlap between rows %d and %d (%s / %s)",
					prefixes[i].row, prefixes[j].row, prefixes[i].prefix, prefixes[j].prefix)
			}
		}
	}

	// Every 归属设备 other than the seeds resolves to a device code that
	// exists in the device sheet.
	deviceCodes := map[string]bool{}
	for _, r := range DeviceRows {
		deviceCodes[r.DeviceCode] = true
	}
	for i, r := range IPPoolRows {
		switch r.AssignedDevice {
		case "", IPPoolUnknownDevice:
			continue
		}
		if !deviceCodes[r.AssignedDevice] {
			t.Errorf("row %d 归属设备 %q does not exist in DeviceRows", i+2, r.AssignedDevice)
		}
	}
}

func TestCircuitSeededRows(t *testing.T) {
	first := CircuitRows[row(CircuitDupFirstRow)]
	second := CircuitRows[row(CircuitDupSecondRow)]
	if first.CircuitCode != CircuitDupCode || second.CircuitCode != CircuitDupCode {
		t.Errorf("dup pair codes = %q/%q, want both %q", first.CircuitCode, second.CircuitCode, CircuitDupCode)
	}
	if first.BandwidthMbps == second.BandwidthMbps {
		t.Errorf("dup circuit pair should differ beyond the code (both bandwidth %q)", first.BandwidthMbps)
	}

	if r := CircuitRows[row(CircuitMissingZRow)]; r.ZEndDevice != "" {
		t.Errorf("row %d Z端设备 = %q, want empty (E002 seed)", CircuitMissingZRow, r.ZEndDevice)
	}
	if r := CircuitRows[row(CircuitUnknownRefRow)]; r.AEndDevice != CircuitUnknownDevice {
		t.Errorf("row %d A端设备 = %q, want %q (E003 seed)", CircuitUnknownRefRow, r.AEndDevice, CircuitUnknownDevice)
	}
	if r := CircuitRows[row(CircuitBadBandwidthRow)]; r.BandwidthMbps != CircuitBadBandwidth {
		t.Errorf("row %d 带宽Mbps = %q, want %q (E004 seed)", CircuitBadBandwidthRow, r.BandwidthMbps, CircuitBadBandwidth)
	}
	if _, err := strconv.Atoi(CircuitBadBandwidth); err == nil {
		t.Errorf("%q must not parse as an int", CircuitBadBandwidth)
	}
	r := CircuitRows[row(CircuitSelfLoopRow)]
	if r.AEndDevice != CircuitSelfLoopDev || r.ZEndDevice != CircuitSelfLoopDev {
		t.Errorf("row %d A/Z端 = %q/%q, want both %q (W003 seed)", CircuitSelfLoopRow, r.AEndDevice, r.ZEndDevice, CircuitSelfLoopDev)
	}
}

// TestCircuitChain verifies the 20 clean chain rows: CKT-001..CKT-020
// connect DEV-001→DEV-002, DEV-002→DEV-003, …, DEV-020→DEV-021, giving the
// known 2-hop path DEV-001→DEV-002→DEV-003 and 20-hop reachability of
// DEV-021 from DEV-001.
func TestCircuitChain(t *testing.T) {
	if CircuitChainLen != CircuitChainLast-CircuitChainFirst+1 {
		t.Fatalf("CircuitChainLen = %d, inconsistent with first/last %d/%d",
			CircuitChainLen, CircuitChainFirst, CircuitChainLast)
	}
	for n := CircuitChainFirst; n <= CircuitChainLast; n++ {
		r := CircuitRows[row(n+1)] // CKT-00n lives at Excel row n+1
		wantCode := fmt.Sprintf("CKT-%03d", n)
		if r.CircuitCode != wantCode {
			t.Errorf("chain row %d: 电路编号 = %q, want %q", n+1, r.CircuitCode, wantCode)
		}
		wantA := fmt.Sprintf("DEV-%03d", n)
		wantZ := fmt.Sprintf("DEV-%03d", n+1)
		if r.AEndDevice != wantA || r.ZEndDevice != wantZ {
			t.Errorf("chain row %d: A/Z端 = %q/%q, want %q/%q", n+1, r.AEndDevice, r.ZEndDevice, wantA, wantZ)
		}
	}

	// The chain device codes all exist in the device sheet.
	for n := DeviceChainStart; n <= DeviceChainEnd; n++ {
		if FindDeviceRow(fmt.Sprintf("DEV-%03d", n)) == 0 {
			t.Errorf("chain device DEV-%03d is missing from DeviceRows", n)
		}
	}
}

func TestCircuitCleanInvariants(t *testing.T) {
	// Circuit codes: exactly one code appears twice (the E001 seed).
	counts := map[string]int{}
	for _, r := range CircuitRows {
		if r.CircuitCode == "" {
			t.Errorf("circuit row with empty 电路编号")
		}
		counts[r.CircuitCode]++
	}
	dups := 0
	for code, n := range counts {
		switch n {
		case 2:
			dups++
			if code != CircuitDupCode {
				t.Errorf("unexpected duplicated circuit code %q", code)
			}
		case 1:
		default:
			t.Errorf("circuit code %q appears %d times", code, n)
		}
	}
	if dups != 1 {
		t.Errorf("number of duplicated circuit codes = %d, want exactly 1", dups)
	}

	// Bandwidth parses as an int within 100..10000, except the E004 seed.
	for i, r := range CircuitRows {
		if r.BandwidthMbps == CircuitBadBandwidth {
			continue
		}
		bw, err := strconv.Atoi(r.BandwidthMbps)
		if err != nil {
			t.Errorf("row %d 带宽Mbps %q does not parse as an int: %v", i+2, r.BandwidthMbps, err)
			continue
		}
		if bw < 100 || bw > 10000 {
			t.Errorf("row %d 带宽Mbps = %d, outside 100..10000", i+2, bw)
		}
	}

	// Statuses are valid enum values (circuits carry no status seed).
	for i, r := range CircuitRows {
		if !deviceStatusEnum[r.Status] {
			t.Errorf("row %d (%s): unexpected status %q", i+2, r.CircuitCode, r.Status)
		}
	}

	// Every referenced device (A and Z) exists in the device sheet, except
	// the E003 seed; every A端 is present, except the E003 seed row.
	deviceCodes := map[string]bool{}
	for _, r := range DeviceRows {
		deviceCodes[r.DeviceCode] = true
	}
	for i, r := range CircuitRows {
		for _, ref := range []string{r.AEndDevice, r.ZEndDevice} {
			if ref == "" || ref == CircuitUnknownDevice {
				continue
			}
			if !deviceCodes[ref] {
				t.Errorf("row %d (%s): referenced device %q is not in DeviceRows", i+2, r.CircuitCode, ref)
			}
		}
	}
}

// TestUnlinkedDeviceIsolation pins the I001 seed: DEV-030 is referenced by
// no pool and no circuit, and it is the only IMPORTED device code with that
// property. The three error-seeded device rows (second DEV-012 copy, empty
// name, invalid IP) are excluded because error rows never reach the
// database, so the set-level I001 rule cannot see them.
func TestUnlinkedDeviceIsolation(t *testing.T) {
	failedRows := map[int]bool{
		row(DeviceDupSecondRow):   true,
		row(DeviceMissingNameRow): true,
		row(DeviceInvalidIPRow):   true,
	}
	referenced := map[string]bool{}
	for _, r := range IPPoolRows {
		if r.AssignedDevice != "" {
			referenced[r.AssignedDevice] = true
		}
	}
	for _, r := range CircuitRows {
		if r.AEndDevice != "" {
			referenced[r.AEndDevice] = true
		}
		if r.ZEndDevice != "" {
			referenced[r.ZEndDevice] = true
		}
	}

	var unlinked []string
	seen := map[string]bool{}
	for i, r := range DeviceRows {
		if failedRows[i] || seen[r.DeviceCode] {
			continue // count the imported DEV-012 copy only once
		}
		seen[r.DeviceCode] = true
		if !referenced[r.DeviceCode] {
			unlinked = append(unlinked, r.DeviceCode)
		}
	}
	if len(unlinked) != 1 || unlinked[0] != DeviceUnlinkedCode {
		t.Errorf("unreferenced imported device codes = %v, want exactly [%s]", unlinked, DeviceUnlinkedCode)
	}
}

func TestFindRowHelpers(t *testing.T) {
	if got := FindDeviceRow(DeviceDupCode); got != DeviceDupFirstRow {
		t.Errorf("FindDeviceRow(%q) = %d, want %d (first copy)", DeviceDupCode, got, DeviceDupFirstRow)
	}
	if got := FindDeviceRow("DEV-001"); got != 2 {
		t.Errorf("FindDeviceRow(DEV-001) = %d, want 2", got)
	}
	if got := FindDeviceRow("DEV-9999"); got != 0 {
		t.Errorf("FindDeviceRow(DEV-9999) = %d, want 0", got)
	}
	if got := FindIPPoolRow("POOL-01"); got != 2 {
		t.Errorf("FindIPPoolRow(POOL-01) = %d, want 2", got)
	}
	if got := FindIPPoolRow("POOL-99"); got != 0 {
		t.Errorf("FindIPPoolRow(POOL-99) = %d, want 0", got)
	}
	if got := FindCircuitRow(CircuitDupCode); got != CircuitDupFirstRow {
		t.Errorf("FindCircuitRow(%q) = %d, want %d (first copy)", CircuitDupCode, got, CircuitDupFirstRow)
	}
	if got := FindCircuitRow("CKT-025"); got != 0 {
		t.Errorf("FindCircuitRow(CKT-025) = %d, want 0 (displaced by the dup)", got)
	}
}

// TestSeedRowLocatorsInRange guards against off-by-two drift between the
// *Row constants and the slice literals.
func TestSeedRowLocatorsInRange(t *testing.T) {
	check := func(name string, n int, max int) {
		if n < 2 || n > max+1 {
			t.Errorf("%s = %d, out of range 2..%d", name, n, max+1)
		}
	}
	check("DeviceDupFirstRow", DeviceDupFirstRow, len(DeviceRows))
	check("DeviceDupSecondRow", DeviceDupSecondRow, len(DeviceRows))
	check("DeviceInvalidIPRow", DeviceInvalidIPRow, len(DeviceRows))
	check("DeviceMissingNameRow", DeviceMissingNameRow, len(DeviceRows))
	check("DeviceUnknownStatusRow", DeviceUnknownStatusRow, len(DeviceRows))
	check("DeviceSharedIPRowA", DeviceSharedIPRowA, len(DeviceRows))
	check("DeviceSharedIPRowB", DeviceSharedIPRowB, len(DeviceRows))
	check("DeviceUnlinkedRow", DeviceUnlinkedRow, len(DeviceRows))
	check("IPPoolOrphanRefRow", IPPoolOrphanRefRow, len(IPPoolRows))
	check("IPPoolOverlapRowA", IPPoolOverlapRowA, len(IPPoolRows))
	check("IPPoolOverlapRowB", IPPoolOverlapRowB, len(IPPoolRows))
	check("IPPoolMissingCIDRRow", IPPoolMissingCIDRRow, len(IPPoolRows))
	check("IPPoolBadCIDRRow", IPPoolBadCIDRRow, len(IPPoolRows))
	check("IPPoolBadVLANRow", IPPoolBadVLANRow, len(IPPoolRows))
	check("IPPoolUnknownRefRow", IPPoolUnknownRefRow, len(IPPoolRows))
	check("CircuitDupFirstRow", CircuitDupFirstRow, len(CircuitRows))
	check("CircuitDupSecondRow", CircuitDupSecondRow, len(CircuitRows))
	check("CircuitSelfLoopRow", CircuitSelfLoopRow, len(CircuitRows))
	check("CircuitMissingZRow", CircuitMissingZRow, len(CircuitRows))
	check("CircuitUnknownRefRow", CircuitUnknownRefRow, len(CircuitRows))
	check("CircuitBadBandwidthRow", CircuitBadBandwidthRow, len(CircuitRows))
}
