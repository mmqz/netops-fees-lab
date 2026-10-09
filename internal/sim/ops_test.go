package sim

// Strict op validation matrix (PROJECT_BRIEF_P2 §4) and the external
// boundary rules: unknown op codes, engine-internal restore_* codes,
// set-but-unused fields, value formats. SEC-P2 (decode half): restore
// ops are rejected at DecodeOps AND ApplyOps.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
)

func TestValidateOp(t *testing.T) {
	valid := map[string]Op{
		domain.OpCreateDevice:        {Op: domain.OpCreateDevice, Code: "DEV-100", Name: "new device"},
		domain.OpSetDeviceStatus:     {Op: domain.OpSetDeviceStatus, DeviceID: "vd-1", Status: "down"},
		domain.OpDeleteDevice:        {Op: domain.OpDeleteDevice, DeviceID: "vd-1"},
		domain.OpCreateInterface:     {Op: domain.OpCreateInterface, DeviceID: "vd-1", Name: "eth0"},
		domain.OpSetInterfaceStatus:  {Op: domain.OpSetInterfaceStatus, InterfaceID: "if-1", Status: "up"},
		domain.OpConfigInterfaceIPv4: {Op: domain.OpConfigInterfaceIPv4, InterfaceID: "if-1", IPv4Addr: strPtr("10.0.0.1"), IPv4Prefix: intPtr(24)},
		domain.OpDeleteInterface:     {Op: domain.OpDeleteInterface, InterfaceID: "if-1"},
		domain.OpCreateLink:          {Op: domain.OpCreateLink, AInterfaceID: "if-1", ZInterfaceID: "if-2"},
		domain.OpSetLinkStatus:       {Op: domain.OpSetLinkStatus, LinkID: "ln-1", Status: "up"},
		domain.OpDeleteLink:          {Op: domain.OpDeleteLink, LinkID: "ln-1"},
	}
	if len(valid) != 10 {
		t.Fatalf("happy-path table has %d ops, want 10", len(valid))
	}
	for code, op := range valid {
		if err := ValidateOp(op); err != nil {
			t.Errorf("ValidateOp(%s) happy path: %v", code, err)
		}
	}

	// Optional-field variants of the create ops.
	if err := ValidateOp(Op{Op: domain.OpCreateInterface, DeviceID: "vd-1", Name: "eth9",
		Status: "down", IPv4Addr: strPtr("10.0.0.1"), IPv4Prefix: intPtr(32)}); err != nil {
		t.Errorf("create_interface with all optionals: %v", err)
	}
	if err := ValidateOp(Op{Op: domain.OpCreateInterface, DeviceID: "vd-1", Name: "eth9",
		Status: "up"}); err != nil {
		t.Errorf("create_interface with status only: %v", err)
	}
	if err := ValidateOp(Op{Op: domain.OpCreateLink, AInterfaceID: "if-1", ZInterfaceID: "if-2",
		Status: "down", Label: "CKT-9"}); err != nil {
		t.Errorf("create_link with all optionals: %v", err)
	}
	// Unconfigure variant: both null is valid for config_interface_ipv4.
	if err := ValidateOp(Op{Op: domain.OpConfigInterfaceIPv4, InterfaceID: "if-1"}); err != nil {
		t.Errorf("config_interface_ipv4 unconfigure (both null): %v", err)
	}
	// Prefix boundaries 0 and 32.
	for _, prefix := range []int{0, 32} {
		if err := ValidateOp(Op{Op: domain.OpConfigInterfaceIPv4, InterfaceID: "if-1",
			IPv4Addr: strPtr("10.0.0.1"), IPv4Prefix: intPtr(prefix)}); err != nil {
			t.Errorf("config_interface_ipv4 prefix %d: %v", prefix, err)
		}
	}
	// Interface name regex boundaries: 1 and 32 chars, all allowed chars.
	if err := ValidateOp(Op{Op: domain.OpCreateInterface, DeviceID: "vd-1", Name: "a"}); err != nil {
		t.Errorf("1-char interface name: %v", err)
	}
	if err := ValidateOp(Op{Op: domain.OpCreateInterface, DeviceID: "vd-1", Name: strings.Repeat("aA0._-", 5) + "aa"}); err != nil {
		t.Errorf("32-char interface name: %v", err)
	}

	tests := []struct {
		name string
		op   Op
	}{
		{"unknown op code", Op{Op: "explode_device", DeviceID: "vd-1"}},
		{"empty op code", Op{Code: "DEV-1", Name: "n"}},
		{"restore op rejected", Op{Op: domain.OpRestoreLink, LinkID: "ln-1"}},
		{"restore device rejected", Op{Op: domain.OpRestoreDevice, DeviceID: "vd-1"}},
		{"restore interface rejected", Op{Op: domain.OpRestoreInterface, InterfaceID: "if-1"}},
		// set-but-unused fields (one per representative op)
		{"create_device with deviceId", Op{Op: domain.OpCreateDevice, Code: "C", Name: "n", DeviceID: "vd-1"}},
		{"create_device with status", Op{Op: domain.OpCreateDevice, Code: "C", Name: "n", Status: "up"}},
		{"set_device_status with name", Op{Op: domain.OpSetDeviceStatus, DeviceID: "vd-1", Status: "up", Name: "x"}},
		{"set_device_status with code", Op{Op: domain.OpSetDeviceStatus, DeviceID: "vd-1", Status: "up", Code: "C"}},
		{"delete_device with interfaceId", Op{Op: domain.OpDeleteDevice, DeviceID: "vd-1", InterfaceID: "if-1"}},
		{"create_interface with linkId", Op{Op: domain.OpCreateInterface, DeviceID: "vd-1", Name: "eth0", LinkID: "ln-1"}},
		{"set_interface_status with deviceId", Op{Op: domain.OpSetInterfaceStatus, InterfaceID: "if-1", Status: "up", DeviceID: "vd-1"}},
		{"config_interface_ipv4 with status", Op{Op: domain.OpConfigInterfaceIPv4, InterfaceID: "if-1", Status: "up"}},
		{"delete_interface with label", Op{Op: domain.OpDeleteInterface, InterfaceID: "if-1", Label: "L"}},
		{"create_link with deviceId", Op{Op: domain.OpCreateLink, AInterfaceID: "if-1", ZInterfaceID: "if-2", DeviceID: "vd-1"}},
		{"set_link_status with name", Op{Op: domain.OpSetLinkStatus, LinkID: "ln-1", Status: "up", Name: "x"}},
		{"delete_link with code", Op{Op: domain.OpDeleteLink, LinkID: "ln-1", Code: "C"}},
		// missing required identifiers
		{"set_device_status empty deviceId", Op{Op: domain.OpSetDeviceStatus, Status: "up"}},
		{"delete_device empty deviceId", Op{Op: domain.OpDeleteDevice}},
		{"create_interface empty deviceId", Op{Op: domain.OpCreateInterface, Name: "eth0"}},
		{"set_interface_status empty interfaceId", Op{Op: domain.OpSetInterfaceStatus, Status: "down"}},
		{"delete_interface empty interfaceId", Op{Op: domain.OpDeleteInterface}},
		{"config_interface_ipv4 empty interfaceId", Op{Op: domain.OpConfigInterfaceIPv4, IPv4Addr: strPtr("10.0.0.1"), IPv4Prefix: intPtr(24)}},
		{"create_link empty aInterfaceId", Op{Op: domain.OpCreateLink, ZInterfaceID: "if-2"}},
		{"create_link empty zInterfaceId", Op{Op: domain.OpCreateLink, AInterfaceID: "if-1"}},
		{"set_link_status empty linkId", Op{Op: domain.OpSetLinkStatus, Status: "up"}},
		{"delete_link empty linkId", Op{Op: domain.OpDeleteLink}},
		// create_device required values
		{"create_device empty code", Op{Op: domain.OpCreateDevice, Name: "n"}},
		{"create_device empty name", Op{Op: domain.OpCreateDevice, Code: "C"}},
		// status enum
		{"set_device_status bad status", Op{Op: domain.OpSetDeviceStatus, DeviceID: "vd-1", Status: "paused"}},
		{"set_device_status empty status", Op{Op: domain.OpSetDeviceStatus, DeviceID: "vd-1"}},
		{"set_device_status uppercase status", Op{Op: domain.OpSetDeviceStatus, DeviceID: "vd-1", Status: "Up"}},
		{"set_interface_status bad status", Op{Op: domain.OpSetInterfaceStatus, InterfaceID: "if-1", Status: "disabled"}},
		{"set_link_status bad status", Op{Op: domain.OpSetLinkStatus, LinkID: "ln-1", Status: "up?"}},
		{"create_interface bad status", Op{Op: domain.OpCreateInterface, DeviceID: "vd-1", Name: "eth0", Status: "sideways"}},
		{"create_link bad status", Op{Op: domain.OpCreateLink, AInterfaceID: "if-1", ZInterfaceID: "if-2", Status: "Down"}},
		// interface name regex
		{"create_interface empty name", Op{Op: domain.OpCreateInterface, DeviceID: "vd-1"}},
		{"create_interface space in name", Op{Op: domain.OpCreateInterface, DeviceID: "vd-1", Name: "eth 0"}},
		{"create_interface slash in name", Op{Op: domain.OpCreateInterface, DeviceID: "vd-1", Name: "eth/0"}},
		{"create_interface 33-char name", Op{Op: domain.OpCreateInterface, DeviceID: "vd-1", Name: strings.Repeat("a", 33)}},
		{"create_interface non-ascii name", Op{Op: domain.OpCreateInterface, DeviceID: "vd-1", Name: "以太网0"}},
		// IPv4 rules
		{"addr without prefix", Op{Op: domain.OpConfigInterfaceIPv4, InterfaceID: "if-1", IPv4Addr: strPtr("10.0.0.1")}},
		{"prefix without addr", Op{Op: domain.OpConfigInterfaceIPv4, InterfaceID: "if-1", IPv4Prefix: intPtr(24)}},
		{"create_interface addr without prefix", Op{Op: domain.OpCreateInterface, DeviceID: "vd-1", Name: "eth0", IPv4Addr: strPtr("10.0.0.1")}},
		{"create_interface prefix without addr", Op{Op: domain.OpCreateInterface, DeviceID: "vd-1", Name: "eth0", IPv4Prefix: intPtr(24)}},
		{"invalid quad 10.0.0.300", Op{Op: domain.OpConfigInterfaceIPv4, InterfaceID: "if-1", IPv4Addr: strPtr("10.0.0.300"), IPv4Prefix: intPtr(24)}},
		{"invalid quad 10.0.0", Op{Op: domain.OpConfigInterfaceIPv4, InterfaceID: "if-1", IPv4Addr: strPtr("10.0.0"), IPv4Prefix: intPtr(24)}},
		{"invalid quad ipv6", Op{Op: domain.OpConfigInterfaceIPv4, InterfaceID: "if-1", IPv4Addr: strPtr("::1"), IPv4Prefix: intPtr(24)}},
		{"invalid quad empty", Op{Op: domain.OpConfigInterfaceIPv4, InterfaceID: "if-1", IPv4Addr: strPtr(""), IPv4Prefix: intPtr(24)}},
		{"prefix 33", Op{Op: domain.OpConfigInterfaceIPv4, InterfaceID: "if-1", IPv4Addr: strPtr("10.0.0.1"), IPv4Prefix: intPtr(33)}},
		{"prefix negative", Op{Op: domain.OpConfigInterfaceIPv4, InterfaceID: "if-1", IPv4Addr: strPtr("10.0.0.1"), IPv4Prefix: intPtr(-1)}},
	}
	for _, tt := range tests {
		if err := ValidateOp(tt.op); !errors.Is(err, domain.ErrInvalidOp) {
			t.Errorf("%s: ValidateOp err = %v, want domain.ErrInvalidOp", tt.name, err)
		}
	}
}

func TestDecodeOps(t *testing.T) {
	// Valid ops array round-trips, with request-envelope fields ignored.
	raw := `{"ops":[
                {"op":"create_device","code":"DEV-100","name":"n"},
                {"op":"config_interface_ipv4","interfaceId":"if-1","ipv4Addr":"10.0.0.1","ipv4Prefix":24},
                {"op":"set_link_status","linkId":"ln-1","status":"down"}
        ],"description":"story","runChecks":false}`
	ops, err := DecodeOps([]byte(raw))
	if err != nil {
		t.Fatalf("DecodeOps: %v", err)
	}
	if len(ops) != 3 {
		t.Fatalf("decoded %d ops, want 3", len(ops))
	}
	want := []Op{
		{Op: domain.OpCreateDevice, Code: "DEV-100", Name: "n"},
		{Op: domain.OpConfigInterfaceIPv4, InterfaceID: "if-1", IPv4Addr: strPtr("10.0.0.1"), IPv4Prefix: intPtr(24)},
		{Op: domain.OpSetLinkStatus, LinkID: "ln-1", Status: "down"},
	}
	for i, w := range want {
		if ops[i].Op != w.Op || ops[i].Code != w.Code || ops[i].Name != w.Name ||
			ops[i].InterfaceID != w.InterfaceID || ops[i].LinkID != w.LinkID || ops[i].Status != w.Status {
			t.Errorf("op %d = %+v, want %+v", i, ops[i], w)
		}
		if (ops[i].IPv4Addr == nil) != (w.IPv4Addr == nil) || (ops[i].IPv4Prefix == nil) != (w.IPv4Prefix == nil) {
			t.Errorf("op %d pointer fields = %v/%v, want %v/%v", i, ops[i].IPv4Addr, ops[i].IPv4Prefix, w.IPv4Addr, w.IPv4Prefix)
		}
	}
	// Null unconfigure round-trips as nil pointers.
	ops, err = DecodeOps([]byte(`{"ops":[{"op":"config_interface_ipv4","interfaceId":"if-1","ipv4Addr":null,"ipv4Prefix":null}]}`))
	if err != nil {
		t.Fatalf("DecodeOps(null addr): %v", err)
	}
	if ops[0].IPv4Addr != nil || ops[0].IPv4Prefix != nil {
		t.Fatalf("null addr decoded to %v/%v, want nil/nil", ops[0].IPv4Addr, ops[0].IPv4Prefix)
	}

	bad := []struct {
		name string
		raw  string
	}{
		{"garbage", `not json at all`},
		{"top-level array", `["create_device"]`},
		{"missing ops key", `{"description":"d"}`},
		{"ops not an array", `{"ops":{"op":"create_device"}}`},
		{"op item not an object", `{"ops":["create_device"]}`},
		{"op item is a number", `{"ops":[42]}`},
		{"unknown op field name", `{"ops":[{"op":"create_device","code":"C","name":"N","bogus":1}]}`},
		{"unknown op code", `{"ops":[{"op":"explode","deviceId":"vd-1"}]}`},
		{"missing op code", `{"ops":[{"deviceId":"vd-1"}]}`},
		{"wrong field type", `{"ops":[{"op":"create_interface","deviceId":7,"name":"eth0"}]}`},
		{"status wrong type", `{"ops":[{"op":"set_device_status","deviceId":"vd-1","status":true}]}`},
	}
	for _, tt := range bad {
		if _, err := DecodeOps([]byte(tt.raw)); !errors.Is(err, domain.ErrInvalidOp) {
			t.Errorf("%s: DecodeOps err = %v, want domain.ErrInvalidOp", tt.name, err)
		}
	}
}

// TestRestoreRejectedAtBoundary is the decode half of SEC-P2: restore_*
// op codes are engine-internal and rejected at every external boundary —
// DecodeOps AND ApplyOps (never decoded into typed restore payloads, never
// applied as forward ops).
func TestRestoreRejectedAtBoundary(t *testing.T) {
	svc, _, res := seededEngine(t)
	ctx := context.Background()

	restoreBodies := map[string]string{
		domain.OpRestoreDevice:    `{"op":"restore_device","device":{"id":"vd-1","code":"DEV-001","name":"n","status":"up"},"interfaces":[],"links":[]}`,
		domain.OpRestoreInterface: `{"op":"restore_interface","interface":{"id":"if-1","deviceId":"vd-1","name":"eth0","status":"up"},"links":[]}`,
		domain.OpRestoreLink:      `{"op":"restore_link","link":{"id":"ln-1","aInterfaceId":"if-1","zInterfaceId":"if-2","status":"up","label":""}}`,
	}
	for code, body := range restoreBodies {
		if _, err := DecodeOps([]byte(body)); !errors.Is(err, domain.ErrInvalidOp) {
			t.Errorf("DecodeOps(%s): err = %v, want domain.ErrInvalidOp (restore ops are engine-internal)", code, err)
		}
	}
	// The ops array with a restore op among forward ops is rejected too.
	if _, err := DecodeOps([]byte(`{"ops":[{"op":"set_link_status","linkId":"ln-1","status":"down"},{"op":"restore_link","link":{"id":"ln-1"}}]}`)); !errors.Is(err, domain.ErrInvalidOp) {
		t.Errorf("DecodeOps(mixed restore): err = %v, want domain.ErrInvalidOp", err)
	}

	// ApplyOps refuses a restore op submitted as a plain Op struct.
	for code, op := range map[string]Op{
		domain.OpRestoreDevice:    {Op: domain.OpRestoreDevice, DeviceID: "vd-1"},
		domain.OpRestoreInterface: {Op: domain.OpRestoreInterface, InterfaceID: "if-1"},
		domain.OpRestoreLink:      {Op: domain.OpRestoreLink, LinkID: "ln-1"},
	} {
		if _, err := svc.ApplyOps(ctx, res.WorkspaceID, []Op{op}, "", false); !errors.Is(err, domain.ErrInvalidOp) {
			t.Errorf("ApplyOps(%s): err = %v, want domain.ErrInvalidOp", code, err)
		}
	}
	// A restore op smuggled AFTER a valid op still fails the whole request
	// before any transaction (no changeset recorded).
	before := mustHash(t, svc, res.WorkspaceID)
	if _, err := svc.ApplyOps(ctx, res.WorkspaceID, []Op{
		{Op: domain.OpSetDeviceStatus, DeviceID: "vd-1", Status: "down"},
		{Op: domain.OpRestoreLink, LinkID: "ln-1"},
	}, "", false); !errors.Is(err, domain.ErrInvalidOp) {
		t.Errorf("ApplyOps(valid+restore): err = %v, want domain.ErrInvalidOp", err)
	}
	if mustHash(t, svc, res.WorkspaceID) != before {
		t.Fatalf("state changed by a rejected restore submission")
	}
	if cs, err := svc.db.ChangeSets.CountByWorkspace(ctx, res.WorkspaceID); err != nil || cs != 0 {
		t.Fatalf("changesets recorded = %d, want 0 (invalid_request happens before any transaction): %v", cs, err)
	}
}

// TestDecodeOpsUnknownFieldsIsStrict doubles the DisallowUnknownFields
// guard with a typo ("linId").
func TestDecodeOpsUnknownFieldsIsStrict(t *testing.T) {
	if _, err := DecodeOps([]byte(`{"ops":[{"op":"set_link_status","linId":"ln-1","status":"down"}]}`)); !errors.Is(err, domain.ErrInvalidOp) {
		t.Fatalf("typo field: err = %v, want domain.ErrInvalidOp", err)
	}
}

// TestOpErrorMessage pins the OpError text shape used by the §7 op_failed
// message: "op 2 (set_link_status): …".
func TestOpErrorMessage(t *testing.T) {
	e := &OpError{Index: 2, Code: domain.OpSetLinkStatus, Message: "link not found: ln-99"}
	want := "op 2 (set_link_status): link not found: ln-99"
	if e.Error() != want {
		t.Fatalf("OpError.Error() = %q, want %q", e.Error(), want)
	}
	if !errors.Is(e, domain.ErrOpFailed) {
		t.Fatalf("OpError must wrap domain.ErrOpFailed")
	}
	if err := fmt.Errorf("sim: %w [changeset cs-1 recorded as failed]", e); !errors.Is(err, domain.ErrOpFailed) {
		t.Fatalf("wrapped OpError must still satisfy errors.Is(err, domain.ErrOpFailed)")
	}
}
