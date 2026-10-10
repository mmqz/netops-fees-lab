package sim

// ACC-P2-5 — connectivity fault injection and recovery on the seeded
// chain topology: a 2-hop path with specific hops, a link-down fault on
// the unique path link, the resulting CHK-004 component finding, and
// identical hops after recovery. Plus the Path basics of §5.

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
)

// TestPathBasics covers the §5 connectivity query contract: BFS over
// live edges only, ascending device-code neighbor order, deterministic
// first path, self-path, unknown codes.
func TestPathBasics(t *testing.T) {
	svc, _, res := seededEngine(t)
	ctx := context.Background()
	ws := res.WorkspaceID

	// Known 2-hop path: DEV-001 → DEV-002 → DEV-003 (chain of CKT-001/002).
	p, err := svc.Path(ctx, ws, "DEV-001", "DEV-003")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if !p.Reachable || p.PathLength != 2 {
		t.Fatalf("path = %+v, want reachable / length 2", p)
	}
	wantHops := []PathHop{
		{DeviceID: "vd-1", Code: "DEV-001"},
		{DeviceID: "vd-2", Code: "DEV-002"},
		{DeviceID: "vd-3", Code: "DEV-003"},
	}
	if !reflect.DeepEqual(p.Hops, wantHops) {
		t.Fatalf("hops = %+v, want %+v", p.Hops, wantHops)
	}

	// 13-hop path along the live chain: the down devices DEV-015/016/019
	// cut the chain, so DEV-014 is the farthest reachable from DEV-001 and
	// DEV-021 is NOT.
	p, err = svc.Path(ctx, ws, "DEV-001", "DEV-014")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if !p.Reachable || p.PathLength != 13 || len(p.Hops) != 14 {
		t.Fatalf("path to DEV-014 = %+v, want reachable / 13 hops", p)
	}
	p, err = svc.Path(ctx, ws, "DEV-001", "DEV-021")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if p.Reachable {
		t.Fatalf("DEV-021 must be unreachable (down devices DEV-015/016/019 cut the chain): %+v", p)
	}

	// Same code ⇒ self path.
	p, err = svc.Path(ctx, ws, "DEV-005", "DEV-005")
	if err != nil {
		t.Fatalf("Path(self): %v", err)
	}
	if !p.Reachable || p.PathLength != 0 || len(p.Hops) != 1 || p.Hops[0].Code != "DEV-005" {
		t.Fatalf("self path = %+v, want reachable/0/[DEV-005]", p)
	}

	// Unreachable: DEV-022 has no circuit edges at all.
	p, err = svc.Path(ctx, ws, "DEV-001", "DEV-022")
	if err != nil {
		t.Fatalf("Path(unreachable): %v", err)
	}
	if p.Reachable || p.PathLength != 0 || len(p.Hops) != 0 {
		t.Fatalf("unreachable path = %+v, want reachable=false with EMPTY hops ([] on the wire, never null)", p)
	}

	// Unknown codes ⇒ ErrNotFound.
	if _, err := svc.Path(ctx, ws, "DEV-001", "DEV-999"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Path(unknown to): err = %v, want ErrNotFound", err)
	}
	if _, err := svc.Path(ctx, ws, "DEV-999", "DEV-001"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Path(unknown from): err = %v, want ErrNotFound", err)
	}

	// Live-edge predicate: a DOWN device breaks the path even though its
	// links are up (DEV-002 down ⇒ DEV-003 unreachable from DEV-001).
	applyOK(t, svc, ws, []Op{{Op: domain.OpSetDeviceStatus, DeviceID: "vd-2", Status: "down"}})
	p, err = svc.Path(ctx, ws, "DEV-001", "DEV-003")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if p.Reachable {
		t.Fatalf("path through a down device must be unreachable: %+v", p)
	}
	// ... and a DOWN INTERFACE breaks it too.
	applyOK(t, svc, ws, []Op{
		{Op: domain.OpSetDeviceStatus, DeviceID: "vd-2", Status: "up"},
		{Op: domain.OpSetInterfaceStatus, InterfaceID: "if-3", Status: "down"}, // DEV-002/eth1 (CKT-002 a-end)
	})
	p, err = svc.Path(ctx, ws, "DEV-001", "DEV-003")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if p.Reachable {
		t.Fatalf("path through a down interface must be unreachable: %+v", p)
	}
	applyOK(t, svc, ws, []Op{{Op: domain.OpSetInterfaceStatus, InterfaceID: "if-3", Status: "up"}})
	p, err = svc.Path(ctx, ws, "DEV-001", "DEV-003")
	if err != nil || !p.Reachable || !reflect.DeepEqual(p.Hops, wantHops) {
		t.Fatalf("recovered path = %+v (err %v), want the original hops", p, err)
	}

	// Deterministic first path with ascending neighbor order: from
	// DEV-002 the BFS first visits DEV-001 (lowest code) — so the path
	// DEV-003→DEV-001 is [DEV-003, DEV-002, DEV-001], and a query from
	// DEV-004 to DEV-001 walks DEV-004→DEV-003→DEV-002→DEV-001.
	p, err = svc.Path(ctx, ws, "DEV-004", "DEV-001")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	wantHops4 := []PathHop{
		{DeviceID: "vd-4", Code: "DEV-004"},
		{DeviceID: "vd-3", Code: "DEV-003"},
		{DeviceID: "vd-2", Code: "DEV-002"},
		{DeviceID: "vd-1", Code: "DEV-001"},
	}
	if !reflect.DeepEqual(p.Hops, wantHops4) {
		t.Fatalf("hops = %+v, want %+v (BFS with ascending neighbor codes)", p.Hops, wantHops4)
	}
}

// TestConnectivityFaultInjection is ACC-P2-5: baseline 2-hop path with
// specific hops; set_link_status down on the unique path link ⇒ path
// unreachable + a CHK-004 component finding (with member detail) while
// CHK-003 stays sane; set_link_status back up ⇒ path reachable with
// IDENTICAL hops; Diff(baseline, recovered) shows the CHK-004 removed.
func TestConnectivityFaultInjection(t *testing.T) {
	svc, _, res := seededEngine(t)
	ctx := context.Background()
	ws := res.WorkspaceID

	// The pair DEV-001→DEV-003 and its unique path via DEV-002. DEV-001
	// has exactly one edge (CKT-001), and the only DEV-002→DEV-003 edge
	// is CKT-002 — verify the uniqueness before injecting.
	topo := mustTopology(t, svc, ws)
	deg := map[string]int{}
	for _, e := range topo.Edges {
		deg[e.ADeviceCode]++
		deg[e.ZDeviceCode]++
	}
	if deg["DEV-001"] != 1 {
		t.Fatalf("DEV-001 has %d edges, want 1 (unique-path precondition)", deg["DEV-001"])
	}
	fault := findEdgeByLabel(t, topo, "CKT-002")
	if fault.ADeviceCode != "DEV-002" || fault.ZDeviceCode != "DEV-003" {
		t.Fatalf("CKT-002 = %+v, want DEV-002→DEV-003", fault)
	}

	baseline, err := svc.Path(ctx, ws, "DEV-001", "DEV-003")
	if err != nil {
		t.Fatalf("Path baseline: %v", err)
	}
	wantHops := []PathHop{
		{DeviceID: "vd-1", Code: "DEV-001"},
		{DeviceID: "vd-2", Code: "DEV-002"},
		{DeviceID: "vd-3", Code: "DEV-003"},
	}
	if !reflect.DeepEqual(baseline.Hops, wantHops) {
		t.Fatalf("baseline hops = %+v, want %+v", baseline.Hops, wantHops)
	}
	baselineRun, baselineResults := mustRunChecks(t, svc, ws)
	if parts := findings(baselineResults, domain.CheckPartialPartition); len(parts) != 2 {
		t.Fatalf("baseline CHK-004 findings = %d, want 2 (the two down-device cut-offs)", len(parts))
	}

	// ---- fault: bring the unique path link down ----
	if _, err := svc.ApplyOps(ctx, ws, []Op{
		{Op: domain.OpSetLinkStatus, LinkID: fault.ID, Status: "down"},
	}, "fault: CKT-002 down", true); err != nil {
		t.Fatalf("ApplyOps(fault): %v", err)
	}
	p, err := svc.Path(ctx, ws, "DEV-001", "DEV-003")
	if err != nil {
		t.Fatalf("Path after fault: %v", err)
	}
	if p.Reachable {
		t.Fatalf("path must be unreachable after the unique link went down: %+v", p)
	}

	faultRun, faultResults := mustRunChecks(t, svc, ws)
	parts := findings(faultResults, domain.CheckPartialPartition)
	if len(parts) != 3 {
		t.Fatalf("CHK-004 after fault = %d findings, want 3 (component {DEV-001, DEV-002} added)", len(parts))
	}
	found := false
	for _, f := range parts {
		var detail chkDetail
		if err := json.Unmarshal([]byte(f.DetailJSON), &detail); err != nil {
			t.Fatalf("CHK-004 detail: %v", err)
		}
		if reflect.DeepEqual(detail.Members, []string{"DEV-001", "DEV-002"}) {
			found = true
			if want := "component of 2 devices is disconnected from the main component"; f.Message != want {
				t.Fatalf("CHK-004 message = %q, want %q", f.Message, want)
			}
		}
	}
	if !found {
		t.Fatalf("no CHK-004 finding with members [DEV-001 DEV-002]: %+v", parts)
	}
	// CHK-003 stays sane: still the five isolated devices, DEV-001 and
	// DEV-002 are NOT isolated (CKT-001 is still up).
	if isolated := findings(faultResults, domain.CheckIsolatedDevice); len(isolated) != 5 {
		t.Fatalf("CHK-003 after fault = %d findings, want 5 (unchanged)", len(isolated))
	}
	// The reference component is the largest ({DEV-003..DEV-014}); the
	// findings only cover the OTHER ≥2-member components.
	for _, f := range parts {
		var detail chkDetail
		_ = json.Unmarshal([]byte(f.DetailJSON), &detail)
		if len(detail.Members) != 2 {
			t.Fatalf("CHK-004 finding with %d members, want 2: %+v", len(detail.Members), detail)
		}
	}

	// ---- recovery: link back up ----
	if _, err := svc.ApplyOps(ctx, ws, []Op{
		{Op: domain.OpSetLinkStatus, LinkID: fault.ID, Status: "up"},
	}, "recover: CKT-002 up", true); err != nil {
		t.Fatalf("ApplyOps(recover): %v", err)
	}
	p, err = svc.Path(ctx, ws, "DEV-001", "DEV-003")
	if err != nil {
		t.Fatalf("Path after recovery: %v", err)
	}
	if !p.Reachable || !reflect.DeepEqual(p.Hops, wantHops) {
		t.Fatalf("recovered hops = %+v, want IDENTICAL to baseline %+v", p.Hops, wantHops)
	}
	recoveredRun, _ := mustRunChecks(t, svc, ws)

	// Diff(baseline, recovered): the CHK-004 finding is gone again.
	diff, err := svc.Diff(ctx, ws, baselineRun.ID, recoveredRun.ID)
	if err != nil {
		t.Fatalf("Diff(baseline, recovered): %v", err)
	}
	if len(diff.FindingsAdded) != 0 || len(diff.FindingsRemoved) != 0 {
		t.Fatalf("recovered state equals baseline: diff = %+v", diff)
	}
	// Diff(baseline, fault): the CHK-004 was added (in the fault run).
	diff, err = svc.Diff(ctx, ws, baselineRun.ID, faultRun.ID)
	if err != nil {
		t.Fatalf("Diff(baseline, fault): %v", err)
	}
	if len(diff.FindingsAdded) != 1 || diff.FindingsAdded[0].CheckCode != domain.CheckPartialPartition {
		t.Fatalf("diff findingsAdded = %+v, want the CHK-004", diff.FindingsAdded)
	}
	// ... and removing it again.
	diff, err = svc.Diff(ctx, ws, faultRun.ID, recoveredRun.ID)
	if err != nil {
		t.Fatalf("Diff(fault, recovered): %v", err)
	}
	if len(diff.FindingsRemoved) != 1 || diff.FindingsRemoved[0].CheckCode != domain.CheckPartialPartition {
		t.Fatalf("diff findingsRemoved = %+v, want the CHK-004", diff.FindingsRemoved)
	}
	var detail chkDetail
	if err := json.Unmarshal([]byte(diff.FindingsRemoved[0].DetailJSON), &detail); err != nil {
		t.Fatalf("detail: %v", err)
	}
	if !reflect.DeepEqual(detail.Members, []string{"DEV-001", "DEV-002"}) {
		t.Fatalf("removed CHK-004 members = %v, want [DEV-001 DEV-002]", detail.Members)
	}
}
