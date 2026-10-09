package sim

// ACC-P2-3 / ACC-P2-4 — the deterministic check catalog on crafted
// states: CHK-001 duplicate host address, CHK-002 subnet overlap kinds,
// CHK-003 isolated devices, CHK-005 unconfigured interfaces, baseline
// rows and the score formula.

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
)

// configureIface is a shorthand op for config_interface_ipv4.
func configureIface(id, addr string, prefix int) Op {
	return Op{Op: domain.OpConfigInterfaceIPv4, InterfaceID: id, IPv4Addr: strPtr(addr), IPv4Prefix: intPtr(prefix)}
}

// chkDetail mirrors the detail payloads of all five checks (the fields
// relevant to the assertions; every check uses a subset).
type chkDetail struct {
	Addr       string `json:"addr"`
	Interfaces []struct {
		InterfaceID string `json:"interfaceId"`
		DeviceID    string `json:"deviceId"`
		DeviceCode  string `json:"deviceCode"`
		Name        string `json:"name"`
	} `json:"interfaces"`
	AInterface struct {
		DeviceCode string `json:"deviceCode"`
		Name       string `json:"name"`
		IPv4Addr   string `json:"ipv4Addr"`
		IPv4Prefix int    `json:"ipv4Prefix"`
	} `json:"aInterface"`
	BInterface struct {
		DeviceCode string `json:"deviceCode"`
		Name       string `json:"name"`
		IPv4Addr   string `json:"ipv4Addr"`
		IPv4Prefix int    `json:"ipv4Prefix"`
	} `json:"bInterface"`
	Kind        string   `json:"kind"`
	NetworkA    string   `json:"networkA"`
	NetworkB    string   `json:"networkB"`
	InterfaceID string   `json:"interfaceId"`
	DeviceID    string   `json:"deviceId"`
	DeviceCode  string   `json:"deviceCode"`
	Code        string   `json:"code"`
	Name        string   `json:"name"`
	TotalLinks  int      `json:"totalLinks"`
	UpLinks     int      `json:"upLinks"`
	Members     []string `json:"members"`
}

// TestCheckDuplicateHostAddress is ACC-P2-3: two up interfaces with the
// same address on up devices ⇒ exactly one CHK-001 finding listing both
// (sorted by deviceCode, name); interface down or device down ⇒ no
// finding; the identical-address pair yields NO CHK-002 finding.
func TestCheckDuplicateHostAddress(t *testing.T) {
	svc, _, res := seededEngine(t)
	ws := res.WorkspaceID

	// Two up ifaces with the same address: DEV-001/eth0 (if-1) and
	// DEV-003/eth0 (if-4).
	applyOK(t, svc, ws, []Op{
		configureIface("if-1", "10.0.0.1", 24),
		configureIface("if-4", "10.0.0.1", 24),
	})
	_, results := mustRunChecks(t, svc, ws)
	dup := findings(results, domain.CheckDuplicateHostAddress)
	if len(dup) != 1 {
		t.Fatalf("CHK-001 findings = %d, want 1", len(dup))
	}
	f := dup[0]
	if f.Severity != domain.SeverityError || f.Passed {
		t.Fatalf("CHK-001 finding severity/passed = %v/%v, want error/false", f.Severity, f.Passed)
	}
	if f.EntityKind != "workspace" || f.EntityID != ws {
		t.Fatalf("CHK-001 entity = %q/%q, want workspace/%s", f.EntityKind, f.EntityID, ws)
	}
	if want := "duplicate host address 10.0.0.1 used by 2 interfaces"; f.Message != want {
		t.Fatalf("CHK-001 message = %q, want %q", f.Message, want)
	}
	var detail chkDetail
	if err := json.Unmarshal([]byte(f.DetailJSON), &detail); err != nil {
		t.Fatalf("CHK-001 detail %q: %v", f.DetailJSON, err)
	}
	if detail.Addr != "10.0.0.1" || len(detail.Interfaces) != 2 {
		t.Fatalf("CHK-001 detail = %+v, want addr 10.0.0.1 with 2 interfaces", detail)
	}
	// Detail sorted by (deviceCode, name).
	if detail.Interfaces[0].DeviceCode != "DEV-001" || detail.Interfaces[0].Name != "eth0" ||
		detail.Interfaces[1].DeviceCode != "DEV-003" || detail.Interfaces[1].Name != "eth0" {
		t.Fatalf("CHK-001 detail interfaces = %+v, want DEV-001/eth0 then DEV-003/eth0", detail.Interfaces)
	}
	if detail.Interfaces[0].InterfaceID != "if-1" || detail.Interfaces[0].DeviceID != "vd-1" ||
		detail.Interfaces[1].InterfaceID != "if-4" || detail.Interfaces[1].DeviceID != "vd-3" {
		t.Fatalf("CHK-001 detail ids = %+v", detail.Interfaces)
	}
	// The identical-addr pair yields NO CHK-002 finding for that pair.
	if overlaps := findings(results, domain.CheckSubnetOverlap); len(overlaps) != 0 {
		t.Fatalf("identical-addr pair produced %d CHK-002 findings, want 0 (CHK-001 owns them)", len(overlaps))
	}

	// Interface down ⇒ no finding.
	applyOK(t, svc, ws, []Op{{Op: domain.OpSetInterfaceStatus, InterfaceID: "if-4", Status: "down"}})
	_, results = mustRunChecks(t, svc, ws)
	if dup = findings(results, domain.CheckDuplicateHostAddress); len(dup) != 0 {
		t.Fatalf("CHK-001 with iface down = %d findings, want 0", len(dup))
	}
	if overlaps := findings(results, domain.CheckSubnetOverlap); len(overlaps) != 0 {
		t.Fatalf("CHK-002 with iface down = %d findings, want 0", len(overlaps))
	}

	// Device down ⇒ no finding (interface back up, device down).
	applyOK(t, svc, ws, []Op{
		{Op: domain.OpSetInterfaceStatus, InterfaceID: "if-4", Status: "up"},
		{Op: domain.OpSetDeviceStatus, DeviceID: "vd-3", Status: "down"},
	})
	_, results = mustRunChecks(t, svc, ws)
	if dup = findings(results, domain.CheckDuplicateHostAddress); len(dup) != 0 {
		t.Fatalf("CHK-001 with device down = %d findings, want 0", len(dup))
	}

	// Three interfaces, one finding listing all three (sorted).
	applyOK(t, svc, ws, []Op{
		{Op: domain.OpSetDeviceStatus, DeviceID: "vd-3", Status: "up"},
		configureIface("if-8", "10.0.0.1", 24), // DEV-005/eth0
	})
	_, results = mustRunChecks(t, svc, ws)
	dup = findings(results, domain.CheckDuplicateHostAddress)
	if len(dup) != 1 {
		t.Fatalf("CHK-001 with 3 same-addr ifaces = %d findings, want 1", len(dup))
	}
	if err := json.Unmarshal([]byte(dup[0].DetailJSON), &detail); err != nil {
		t.Fatalf("detail: %v", err)
	}
	if len(detail.Interfaces) != 3 || detail.Interfaces[2].DeviceCode != "DEV-005" {
		t.Fatalf("3-iface detail = %+v, want DEV-001, DEV-003, DEV-005", detail.Interfaces)
	}
	if want := "duplicate host address 10.0.0.1 used by 3 interfaces"; dup[0].Message != want {
		t.Fatalf("message = %q, want %q", dup[0].Message, want)
	}
	// Still no CHK-002 for any of the identical pairs.
	if overlaps := findings(results, domain.CheckSubnetOverlap); len(overlaps) != 0 {
		t.Fatalf("identical-addr pairs produced %d CHK-002 findings, want 0", len(overlaps))
	}
}

// TestCheckSubnetOverlap is ACC-P2-4: the three overlap kinds plus the
// disjoint case, pair ordering a<b by (deviceCode, name) and messages
// carrying both networks.
func TestCheckSubnetOverlap(t *testing.T) {
	svc, _, res := seededEngine(t)
	ws := res.WorkspaceID

	// One changeset configuring ten interfaces (device codes ascend, so
	// the (deviceCode, name) pair order is the configuration order):
	//   DEV-001/eth0 10.0.0.1/24   vs DEV-002/eth0 10.0.0.9/24   → identical
	//   DEV-003/eth0 10.1.0.1/16   vs DEV-004/eth0 10.1.0.9/24   → contains
	//   (the /24 sits at the /16's base: aligned nesting)
	//   DEV-005/eth0 10.2.0.1/24   vs DEV-006/eth0 10.2.0.130/25 → partial
	//   DEV-007/eth0 10.3.0.1/24   vs DEV-008/eth0 10.9.0.1/24   → disjoint
	//   DEV-009/eth0 10.4.0.1/24   vs DEV-010/eth0 10.4.0.1/24   → identical
	//   address (CHK-001 territory, excluded from CHK-002)
	applyOK(t, svc, ws, []Op{
		configureIface("if-1", "10.0.0.1", 24),    // DEV-001/eth0
		configureIface("if-2", "10.0.0.9", 24),    // DEV-002/eth0
		configureIface("if-4", "10.1.0.1", 16),    // DEV-003/eth0
		configureIface("if-6", "10.1.0.9", 24),    // DEV-004/eth0, same base as the /16
		configureIface("if-8", "10.2.0.1", 24),    // DEV-005/eth0
		configureIface("if-10", "10.2.0.130", 25), // DEV-006/eth0
		configureIface("if-12", "10.3.0.1", 24),   // DEV-007/eth0
		configureIface("if-14", "10.9.0.1", 24),   // DEV-008/eth0
		configureIface("if-16", "10.4.0.1", 24),   // DEV-009/eth0
		configureIface("if-18", "10.4.0.1", 24),   // DEV-010/eth0
	})
	_, results := mustRunChecks(t, svc, ws)
	overlaps := findings(results, domain.CheckSubnetOverlap)
	if len(overlaps) != 3 {
		t.Fatalf("CHK-002 findings = %d, want 3 (identical, contains, partial; disjoint ⇒ none): %+v",
			len(overlaps), overlaps)
	}

	want := []struct {
		pair     string // "DEV-001/eth0|DEV-002/eth0"
		kind     string
		networkA string
		networkB string
		message  string
	}{
		{"DEV-001/eth0|DEV-002/eth0", "identical", "10.0.0.0/24", "10.0.0.0/24",
			"subnets 10.0.0.0/24 and 10.0.0.0/24 overlap (identical)"},
		{"DEV-003/eth0|DEV-004/eth0", "contains", "10.1.0.0/16", "10.1.0.0/24",
			"subnets 10.1.0.0/16 and 10.1.0.0/24 overlap (contains)"},
		{"DEV-005/eth0|DEV-006/eth0", "partial", "10.2.0.0/24", "10.2.0.128/25",
			"subnets 10.2.0.0/24 and 10.2.0.128/25 overlap (partial)"},
	}
	for i, w := range want {
		f := overlaps[i]
		var detail chkDetail
		if err := json.Unmarshal([]byte(f.DetailJSON), &detail); err != nil {
			t.Fatalf("CHK-002[%d] detail %q: %v", i, f.DetailJSON, err)
		}
		gotPair := detail.AInterface.DeviceCode + "/" + detail.AInterface.Name + "|" +
			detail.BInterface.DeviceCode + "/" + detail.BInterface.Name
		if gotPair != w.pair {
			t.Errorf("CHK-002[%d] pair = %q, want %q (a < b by deviceCode, name)", i, gotPair, w.pair)
		}
		if detail.Kind != w.kind {
			t.Errorf("CHK-002[%d] kind = %q, want %q", i, detail.Kind, w.kind)
		}
		if detail.NetworkA != w.networkA || detail.NetworkB != w.networkB {
			t.Errorf("CHK-002[%d] networks = %q/%q, want %q/%q", i, detail.NetworkA, detail.NetworkB, w.networkA, w.networkB)
		}
		if f.Message != w.message {
			t.Errorf("CHK-002[%d] message = %q, want %q", i, f.Message, w.message)
		}
		if f.Severity != domain.SeverityError || f.EntityKind != "workspace" || f.EntityID != ws {
			t.Errorf("CHK-002[%d] meta = %v/%q/%q, want error/workspace", i, f.Severity, f.EntityKind, f.EntityID)
		}
	}
	// The a-interface of every finding carries its configured address.
	if overlaps[0].DetailJSON == "" {
		t.Fatalf("detail json empty")
	}
	// One CHK-001 for the identical-address pair and none extra.
	if dup := findings(results, domain.CheckDuplicateHostAddress); len(dup) != 1 {
		t.Fatalf("CHK-001 findings = %d, want 1 (10.4.0.1 pair)", len(dup))
	}
	// No CHK-002 finding references the 10.4.0.1 pair (identical address).
	for _, f := range overlaps {
		var detail chkDetail
		_ = json.Unmarshal([]byte(f.DetailJSON), &detail)
		if detail.AInterface.IPv4Addr == "10.4.0.1" || detail.BInterface.IPv4Addr == "10.4.0.1" {
			t.Fatalf("identical-address pair leaked into CHK-002: %s", f.DetailJSON)
		}
	}

	// Disjoint networks ⇒ no finding for that pair (implied by len==3
	// above; the 10.3/10.9 pair appears in none of the findings).
	for _, f := range overlaps {
		var detail chkDetail
		_ = json.Unmarshal([]byte(f.DetailJSON), &detail)
		for _, side := range []struct{ code, name string }{
			{detail.AInterface.DeviceCode, detail.AInterface.Name},
			{detail.BInterface.DeviceCode, detail.BInterface.Name},
		} {
			if side.code == "DEV-007" || side.code == "DEV-008" {
				t.Fatalf("disjoint pair DEV-007/DEV-008 leaked into CHK-002: %s", f.DetailJSON)
			}
		}
	}
}

// TestIsolatedAndUnconfigured pins CHK-003 and CHK-005 on the freshly
// materialized workspace: five never-referenced/isolated devices, 34
// unconfigured interfaces on up entities (40 interfaces minus the 6 on
// the down devices DEV-015/016/019), plus the 5 passed baseline rows.
func TestIsolatedAndUnconfigured(t *testing.T) {
	svc, _, res := seededEngine(t)
	ws := res.WorkspaceID

	run, results := mustRunChecks(t, svc, ws)

	// CHK-003: the seeded devices without circuit edges are isolated —
	// entity is the device.
	isolated := findings(results, domain.CheckIsolatedDevice)
	wantCodes := []string{"DEV-022", "DEV-026", "DEV-027", "DEV-028", "DEV-030"}
	if len(isolated) != len(wantCodes) {
		t.Fatalf("CHK-003 findings = %d, want %d", len(isolated), len(wantCodes))
	}
	for i, code := range wantCodes {
		f := isolated[i]
		if f.Severity != domain.SeverityWarning || f.Passed {
			t.Fatalf("CHK-003[%d] = %v/%v, want warning/false", i, f.Severity, f.Passed)
		}
		if f.EntityKind != "device" {
			t.Fatalf("CHK-003[%d] entity kind = %q, want device", i, f.EntityKind)
		}
		var detail chkDetail
		if err := json.Unmarshal([]byte(f.DetailJSON), &detail); err != nil {
			t.Fatalf("CHK-003 detail: %v", err)
		}
		if detail.Code != code || detail.TotalLinks != 0 || detail.UpLinks != 0 {
			t.Fatalf("CHK-003[%d] detail = %+v, want code %s totalLinks 0 upLinks 0", i, detail, code)
		}
		if want := "device " + code + " is isolated (no up links)"; f.Message != want {
			t.Fatalf("CHK-003[%d] message = %q, want %q", i, f.Message, want)
		}
		if f.EntityID == "" {
			t.Fatalf("CHK-003[%d] entity id empty", i)
		}
	}

	// CHK-005: 34 unconfigured interfaces (40 minus the 6 interfaces on
	// the down devices DEV-015/DEV-016/DEV-019 — the check requires the
	// device to be up). All 40 materialized interfaces are unconfigured.
	unconf := findings(results, domain.CheckUnconfiguredInterface)
	if len(unconf) != 34 {
		t.Fatalf("CHK-005 findings = %d, want 34", len(unconf))
	}
	// Sorted by (deviceCode, name); no finding on a down device.
	for _, f := range unconf {
		var detail chkDetail
		if err := json.Unmarshal([]byte(f.DetailJSON), &detail); err != nil {
			t.Fatalf("CHK-005 detail: %v", err)
		}
		if f.EntityKind != "interface" {
			t.Fatalf("CHK-005 entity kind = %q, want interface", f.EntityKind)
		}
		if detail.DeviceCode == "DEV-015" || detail.DeviceCode == "DEV-016" || detail.DeviceCode == "DEV-019" {
			t.Fatalf("CHK-005 finding on a DOWN device: %+v", detail)
		}
		if want := "interface " + detail.DeviceCode + "/" + detail.Name + " has no IPv4 address"; f.Message != want {
			t.Fatalf("CHK-005 message = %q, want %q", f.Message, want)
		}
	}
	// First finding = DEV-001/eth0 (canonical order).
	var first chkDetail
	if err := json.Unmarshal([]byte(unconf[0].DetailJSON), &first); err != nil {
		t.Fatalf("detail: %v", err)
	}
	if first.DeviceCode != "DEV-001" || first.Name != "eth0" || first.InterfaceID != "if-1" {
		t.Fatalf("first CHK-005 finding = %+v, want DEV-001/eth0 (if-1)", first)
	}

	// CHK-004 on the fresh workspace: the two 2-member components cut off
	// by the down devices DEV-015/DEV-016/DEV-019.
	parts := findings(results, domain.CheckPartialPartition)
	if len(parts) != 2 {
		t.Fatalf("CHK-004 findings = %d, want 2", len(parts))
	}
	for i, want := range [][]string{{"DEV-017", "DEV-018"}, {"DEV-020", "DEV-021"}} {
		var detail chkDetail
		if err := json.Unmarshal([]byte(parts[i].DetailJSON), &detail); err != nil {
			t.Fatalf("CHK-004 detail: %v", err)
		}
		if !reflect.DeepEqual(detail.Members, want) {
			t.Fatalf("CHK-004[%d] members = %v, want %v", i, detail.Members, want)
		}
		if parts[i].Severity != domain.SeverityInfo {
			t.Fatalf("CHK-004[%d] severity = %v, want info", i, parts[i].Severity)
		}
		if want := "component of 2 devices is disconnected from the main component"; parts[i].Message != want {
			t.Fatalf("CHK-004[%d] message = %q, want %q", i, parts[i].Message, want)
		}
	}

	// Findings canonical order: (check_code, entity_key) — CHK-003 block
	// precedes CHK-004 block precedes CHK-005 block; inside a block the
	// keys ascend.
	var prevCode, prevKey string
	for _, f := range results {
		if f.Passed {
			continue
		}
		key := f.EntityID
		switch f.CheckCode {
		case domain.CheckIsolatedDevice:
			var d chkDetail
			_ = json.Unmarshal([]byte(f.DetailJSON), &d)
			key = d.Code
		case domain.CheckPartialPartition:
			var d chkDetail
			_ = json.Unmarshal([]byte(f.DetailJSON), &d)
			key = d.Code // members joined
			for _, m := range d.Members {
				key += m
			}
		case domain.CheckUnconfiguredInterface:
			var d chkDetail
			_ = json.Unmarshal([]byte(f.DetailJSON), &d)
			key = d.DeviceCode + "/" + d.Name
		}
		if prevCode != "" && (prevCode > f.CheckCode || (prevCode == f.CheckCode && prevKey > key)) {
			t.Fatalf("findings out of canonical order at %s: prev %s/%s ≥ %s/%s", f.CheckCode, prevCode, prevKey, f.CheckCode, key)
		}
		prevCode, prevKey = f.CheckCode, key
	}

	// 5 passed baseline rows appended AFTER the findings, in CheckCodes
	// order, message "ok".
	totalFindings := len(isolated) + len(parts) + len(unconf)
	if len(results) != totalFindings+5 {
		t.Fatalf("run has %d rows, want %d findings + 5 baselines", len(results), totalFindings)
	}
	for i, code := range domain.CheckCodes {
		row := results[totalFindings+i]
		if row.Seq != totalFindings+i+1 || !row.Passed || row.Message != "ok" || row.DetailJSON != "{}" {
			t.Fatalf("baseline row %d = %+v, want seq %d passed ok/{}", i, row, totalFindings+i+1)
		}
		if row.CheckCode != code {
			t.Fatalf("baseline row %d code = %q, want %q (CheckCodes order)", i, row.CheckCode, code)
		}
	}
	// Run-level counts and score: 0 errors, 5 warnings, 36 infos ⇒ 95.
	if run.Score != 95 {
		t.Fatalf("fresh workspace score = %d, want 95 (100 − 5·0 − 1·5)", run.Score)
	}
	if run.RuleVersion != domain.SimRuleVersion {
		t.Fatalf("rule version = %q, want %q", run.RuleVersion, domain.SimRuleVersion)
	}
}

// TestScore pins the score formula max(0, 100 − 5E − 1W) on real runs:
// 95 on the fresh workspace, 90 after one duplicate-address error, and
// the 0 floor after 20 errors.
func TestScore(t *testing.T) {
	svc, _, res := seededEngine(t)
	ws := res.WorkspaceID

	run, _ := mustRunChecks(t, svc, ws)
	if run.Score != 95 {
		t.Fatalf("baseline score = %d, want 95", run.Score)
	}

	applyOK(t, svc, ws, []Op{
		configureIface("if-1", "10.0.0.1", 24),
		configureIface("if-4", "10.0.0.1", 24),
	})
	run, _ = mustRunChecks(t, svc, ws)
	if run.Score != 90 {
		t.Fatalf("score after 1 error = %d, want 90 (100 − 5·1 − 1·5)", run.Score)
	}

	// Floor: bring the three down chain devices up (6 more live
	// interfaces) and configure all 40 interfaces with 20 duplicated
	// addresses (disjoint /24s, so no CHK-002) — 20 errors alone put the
	// formula below zero.
	ops := []Op{
		{Op: domain.OpSetDeviceStatus, DeviceID: "vd-15", Status: "up"},
		{Op: domain.OpSetDeviceStatus, DeviceID: "vd-16", Status: "up"},
		{Op: domain.OpSetDeviceStatus, DeviceID: "vd-19", Status: "up"},
	}
	for i := 1; i <= 40; i++ {
		ops = append(ops, configureIface(
			domain.FormatSimID(domain.SeqInterface, i),
			"10.5."+itoa((i+1)/2)+".1", 24)) // pair k shares 10.5.k.1
	}
	applyOK(t, svc, ws, ops)
	run, results := mustRunChecks(t, svc, ws)
	if dup := findings(results, domain.CheckDuplicateHostAddress); len(dup) != 20 {
		t.Fatalf("dup findings = %d, want 20", len(dup))
	}
	if run.Score != 0 {
		t.Fatalf("score with 20 errors = %d, want 0 (floor)", run.Score)
	}
}

// TestScoreFormulaUnit pins the formula exactly on a hand-built state:
// 3 errors and 8 warnings ⇒ 77; 21 errors ⇒ 0 (floor).
func TestScoreFormulaUnit(t *testing.T) {
	build := func(errors int) *simState {
		s := &simState{
			workspaceID: "ws-test",
			ifaceDevice: map[string]string{},
			deviceByID:  map[string]domain.VirtualDevice{},
			ifaceByID:   map[string]domain.SimInterface{},
		}
		devCount := 2 * errors // two interfaces per duplicated address
		for i := 0; i < devCount; i++ {
			code := "D"
			d := domain.VirtualDevice{
				ID: "vd-" + itoa(i+1), WorkspaceID: "ws-test", Code: code,
				Name: "d" + itoa(i+1), Status: domain.EntityStatusUp,
			}
			s.deviceByID[d.ID] = d
			iface := domain.SimInterface{
				ID: "if-" + itoa(i+1), WorkspaceID: "ws-test", DeviceID: d.ID,
				Name: "eth0", Status: domain.EntityStatusUp,
				IPv4Addr: "10." + itoa(i/2+1) + ".0.1", IPv4Prefix: 24,
			}
			s.ifaceByID[iface.ID] = iface
			s.ifaceDevice[iface.ID] = d.ID
			s.devices = append(s.devices, stateDevice{Dev: d, Interfaces: []domain.SimInterface{iface}})
		}
		return s
	}

	// 3 duplicated addresses ⇒ 3 CHK-001 errors; every device is isolated
	// (no links) ⇒ 2·3 = 6 CHK-003 warnings; 6 CHK-005 infos.
	_, counts, score := computeChecks(build(3), "ws-test")
	if counts.Error != 3 || counts.Warning != 6 {
		t.Fatalf("counts = %+v, want 3 errors / 6 warnings", counts)
	}
	if score != 100-15-6 {
		t.Fatalf("score = %d, want %d", score, 100-15-6)
	}

	// 21 errors ⇒ floor 0 regardless of the 42 warnings.
	_, counts, score = computeChecks(build(21), "ws-test")
	if counts.Error != 21 {
		t.Fatalf("errors = %d, want 21", counts.Error)
	}
	if score != 0 {
		t.Fatalf("score = %d, want 0 (floor)", score)
	}
}

// itoa avoids strconv in test bodies.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := ""
	for i > 0 {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
	}
	return digits
}
