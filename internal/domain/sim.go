package domain

import (
	"errors"
	"fmt"
)

// Simulation op codes (PROJECT_BRIEF_P2 §4). Forward ops form the closed
// external boundary — the only codes accepted from HTTP, MCP and CLI
// requests. Restore ops are engine-internal inverses of the delete ops and
// must be rejected at every external boundary.
const (
	// Forward ops.
	OpCreateDevice        = "create_device"         // {code, name}
	OpSetDeviceStatus     = "set_device_status"     // {deviceId, status}
	OpDeleteDevice        = "delete_device"         // {deviceId}
	OpCreateInterface     = "create_interface"      // {deviceId, name, status?, ipv4Addr?, ipv4Prefix?}
	OpSetInterfaceStatus  = "set_interface_status"  // {interfaceId, status}
	OpConfigInterfaceIPv4 = "config_interface_ipv4" // {interfaceId, ipv4Addr, ipv4Prefix}
	OpDeleteInterface     = "delete_interface"      // {interfaceId}
	OpCreateLink          = "create_link"           // {aInterfaceId, zInterfaceId, status?, label?}
	OpSetLinkStatus       = "set_link_status"       // {linkId, status}
	OpDeleteLink          = "delete_link"           // {linkId}
)

// Internal restore ops (inverse-only; never accepted from external
// boundaries).
const (
	OpRestoreDevice    = "restore_device"    // captured cascade: device + interfaces + touching links
	OpRestoreInterface = "restore_interface" // interface + its links
	OpRestoreLink      = "restore_link"      // link row
)

// forwardOps is the closed set of externally acceptable op codes.
var forwardOps = map[string]struct{}{
	OpCreateDevice:        {},
	OpSetDeviceStatus:     {},
	OpDeleteDevice:        {},
	OpCreateInterface:     {},
	OpSetInterfaceStatus:  {},
	OpConfigInterfaceIPv4: {},
	OpDeleteInterface:     {},
	OpCreateLink:          {},
	OpSetLinkStatus:       {},
	OpDeleteLink:          {},
}

// restoreOps is the closed set of engine-internal restore op codes.
var restoreOps = map[string]struct{}{
	OpRestoreDevice:    {},
	OpRestoreInterface: {},
	OpRestoreLink:      {},
}

// IsForwardOp reports whether code is one of the ten forward op codes that
// external boundaries (HTTP, MCP, CLI) may accept.
func IsForwardOp(code string) bool {
	_, ok := forwardOps[code]
	return ok
}

// IsRestoreOp reports whether code is one of the three engine-internal
// restore op codes (inverses of the delete ops). Request decoding must
// reject these with domain.ErrInvalidOp.
func IsRestoreOp(code string) bool {
	_, ok := restoreOps[code]
	return ok
}

// Simulation check codes (PROJECT_BRIEF_P2 §5, rule_version 1.0.0).
const (
	CheckDuplicateHostAddress  = "CHK-001" // error: duplicate IPv4 host address on up entities
	CheckSubnetOverlap         = "CHK-002" // error: overlapping configured subnets
	CheckIsolatedDevice        = "CHK-003" // warning: up device without up links
	CheckPartialPartition      = "CHK-004" // info: up devices split into components
	CheckUnconfiguredInterface = "CHK-005" // info: up interface without IPv4 address
)

// SimRuleVersion is the version of the simulation check catalog
// (PROJECT_BRIEF_P2 §5). Identical workspace state plus this version must
// produce identical check results.
const SimRuleVersion = "1.0.0"

// CheckCodes lists all simulation check codes in catalog order (CHK-001…
// CHK-005) for deterministic iteration.
var CheckCodes = []string{
	CheckDuplicateHostAddress,
	CheckSubnetOverlap,
	CheckIsolatedDevice,
	CheckPartialPartition,
	CheckUnconfiguredInterface,
}

// Per-workspace sequence kinds and the resulting id formats (PROJECT_BRIEF_P2
// §3): vd-<n> devices, if-<n> interfaces, ln-<n> links, cs-<n> changesets,
// run-<n> check runs, cp-<n> checkpoints.
const (
	SeqDevice     = "vd"
	SeqInterface  = "if"
	SeqLink       = "ln"
	SeqChangeSet  = "cs"
	SeqCheckRun   = "run"
	SeqCheckpoint = "cp"
)

// FormatSimID renders a simulation entity id of the form "<kind>-<n>", for
// example FormatSimID(SeqDevice, 3) = "vd-3".
func FormatSimID(kind string, n int) string {
	return fmt.Sprintf("%s-%d", kind, n)
}

// Phase 2 sentinel errors, alongside ErrNotFound/ErrConflict of domain.go.
// Wrap pattern: callers wrap with fmt.Errorf("…: %w", err) so that
// errors.Is works through every layer.
var (
	// ErrInvalidOp is returned when an op code is unknown, belongs to the
	// engine-internal restore set, or its payload is malformed.
	ErrInvalidOp = errors.New("domain: invalid operation")
	// ErrNotLatest is returned when a changeset cannot be reverted because
	// it is not the latest applied changeset of its workspace (undo is
	// LIFO, PROJECT_BRIEF_P2 §4).
	ErrNotLatest = errors.New("domain: changeset is not the latest applied")
	// ErrNoSourceResources is returned by workspace materialization when
	// the selected import batches contain no device resources to build a
	// workspace from (PROJECT_BRIEF_P2 §6: "no_source_resources").
	ErrNoSourceResources = errors.New("domain: no importable device resources in the selected batches")
	// ErrOpFailed is returned when a well-formed op fails to APPLY
	// (unknown entity, duplicate code, self loop …): the whole changeset
	// rolled back and was recorded as failed. The engine wraps it in an
	// *OpError carrying the failing op; the API layer maps it to 422
	// op_failed (PROJECT_BRIEF_P2 §4/§7).
	ErrOpFailed = errors.New("domain: operation failed")
	// ErrCheckpointMissing is returned by checkpoint restore when the
	// checkpoint snapshot file is gone from disk (deleted or moved after
	// the checkpoint was created). It wraps ErrConflict — a restore
	// against a vanished snapshot is a state conflict — while giving the
	// HTTP layer its dedicated 409 checkpoint_missing_file code
	// (PROJECT_BRIEF_P2 §7).
	ErrCheckpointMissing = fmt.Errorf("domain: checkpoint snapshot file is gone from disk: %w", ErrConflict)
)
