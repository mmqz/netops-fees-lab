package sim

// Engine-level contracts not covered by the acceptance flows: the Now
// clock hook (deterministic timestamps) and the diff's id-churn-proof
// matching keys.

import (
	"context"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
)

// TestClockHook pins that every recorded timestamp flows through the
// Service.Now hook (deterministic tests need a fixed clock).
func TestClockHook(t *testing.T) {
	svc, _, res := seededEngine(t)
	fixed := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	svc.Now = func() time.Time { return fixed }
	ctx := context.Background()

	applyRes, err := svc.ApplyOps(ctx, res.WorkspaceID, []Op{
		{Op: domain.OpSetDeviceStatus, DeviceID: "vd-1", Status: "down"},
	}, "clocked", true)
	if err != nil {
		t.Fatalf("ApplyOps: %v", err)
	}
	cs, err := svc.db.ChangeSets.GetByID(ctx, applyRes.ChangeSetID)
	if err != nil {
		t.Fatalf("GetByID(%s): %v", applyRes.ChangeSetID, err)
	}
	if !cs.CreatedAt.Equal(fixed) {
		t.Fatalf("changeset created_at = %v, want the Now hook value %v", cs.CreatedAt, fixed)
	}
	// The auto-check run's started_at honors the hook as well.
	run, _, err := svc.GetRun(ctx, res.WorkspaceID, applyRes.Checks.RunID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if !run.StartedAt.Equal(fixed) {
		t.Fatalf("run started_at = %v, want the Now hook value %v", run.StartedAt, fixed)
	}
	// Rows the engine INSERTS carry the hook value too (the repo's
	// UpdateStatus/UpdateIPv4 stamp updated_at with their own wall clock —
	// a repo-layer surface property, documented in the worklog; the state
	// hash and every canonical output are timestamp-free, so determinism
	// is unaffected).
	created, err := svc.ApplyOps(ctx, res.WorkspaceID, []Op{
		{Op: domain.OpCreateDevice, Code: "CLOCKED-1", Name: "c"},
	}, "clocked create", false)
	if err != nil {
		t.Fatalf("ApplyOps(create): %v", err)
	}
	d, err := svc.db.VirtualDevices.GetByCode(ctx, res.WorkspaceID, "CLOCKED-1")
	if err != nil {
		t.Fatalf("GetByCode(CLOCKED-1): %v", err)
	}
	_ = created
	if !d.CreatedAt.Equal(fixed) {
		t.Fatalf("device created_at = %v, want the Now hook value %v", d.CreatedAt, fixed)
	}
}

// TestDiffMatchingKeys pins the per-check business keys of the diff
// (id-churn-proof): CHK-001 addr, CHK-002 the sorted endpoint pair,
// CHK-003 device code, CHK-004 sorted member codes, CHK-005
// deviceCode/name.
func TestDiffMatchingKeys(t *testing.T) {
	equal := func(code, a, b string) bool {
		ka := keyOfFinding(domain.CheckResult{CheckCode: code, DetailJSON: a})
		kb := keyOfFinding(domain.CheckResult{CheckCode: code, DetailJSON: b})
		return ka == kb
	}
	different := func(code, a, b string) bool { return !equal(code, a, b) }

	// CHK-001: the address is the key.
	if !equal(domain.CheckDuplicateHostAddress,
		`{"addr":"10.0.0.1","interfaces":[{"interfaceId":"if-1"},{"interfaceId":"if-2"}]}`,
		`{"addr":"10.0.0.1","interfaces":[{"interfaceId":"if-9"},{"interfaceId":"if-8"}]}`) {
		t.Fatalf("CHK-001 keys must match on addr regardless of interface ids")
	}
	if !different(domain.CheckDuplicateHostAddress, `{"addr":"10.0.0.1"}`, `{"addr":"10.0.0.2"}`) {
		t.Fatalf("CHK-001 keys must differ by addr")
	}

	// CHK-002: the sorted (deviceCode/name) endpoint pair — order of
	// a/b does not matter, ids do not matter.
	pairAB := `{"aInterface":{"interfaceId":"if-2","deviceCode":"DEV-002","name":"eth0"},
                    "bInterface":{"interfaceId":"if-1","deviceCode":"DEV-001","name":"eth1"},
                    "kind":"partial","networkA":"x","networkB":"y"}`
	pairBA := `{"aInterface":{"interfaceId":"if-7","deviceCode":"DEV-001","name":"eth1"},
                    "bInterface":{"interfaceId":"if-4","deviceCode":"DEV-002","name":"eth0"},
                    "kind":"identical","networkA":"z","networkB":"w"}`
	if !equal(domain.CheckSubnetOverlap, pairAB, pairBA) {
		t.Fatalf("CHK-002 keys must match on the sorted endpoint pair regardless of a/b order")
	}
	if !different(domain.CheckSubnetOverlap, pairAB,
		`{"aInterface":{"deviceCode":"DEV-002","name":"eth0"},"bInterface":{"deviceCode":"DEV-001","name":"eth0"}}`) {
		t.Fatalf("CHK-002 keys must differ when an endpoint changes")
	}

	// CHK-003: the device code.
	if !equal(domain.CheckIsolatedDevice,
		`{"deviceId":"vd-5","code":"DEV-005","totalLinks":0,"upLinks":0}`,
		`{"deviceId":"vd-99","code":"DEV-005","totalLinks":2,"upLinks":0}`) {
		t.Fatalf("CHK-003 keys must match on device code")
	}
	if !different(domain.CheckIsolatedDevice, `{"code":"DEV-005"}`, `{"code":"DEV-006"}`) {
		t.Fatalf("CHK-003 keys must differ by code")
	}

	// CHK-004: the sorted member set.
	if !equal(domain.CheckPartialPartition, `{"members":["DEV-020","DEV-001"]}`, `{"members":["DEV-001","DEV-020"]}`) {
		t.Fatalf("CHK-004 keys must match on the sorted member set")
	}
	if !different(domain.CheckPartialPartition, `{"members":["DEV-001","DEV-002"]}`, `{"members":["DEV-001","DEV-003"]}`) {
		t.Fatalf("CHK-004 keys must differ by member set")
	}

	// CHK-005: deviceCode/name.
	if !equal(domain.CheckUnconfiguredInterface,
		`{"interfaceId":"if-3","deviceId":"vd-1","deviceCode":"DEV-001","name":"eth0"}`,
		`{"interfaceId":"if-77","deviceId":"vd-9","deviceCode":"DEV-001","name":"eth0"}`) {
		t.Fatalf("CHK-005 keys must match on deviceCode/name")
	}
	if !different(domain.CheckUnconfiguredInterface,
		`{"deviceCode":"DEV-001","name":"eth0"}`, `{"deviceCode":"DEV-001","name":"eth1"}`) {
		t.Fatalf("CHK-005 keys must differ by name")
	}
}
