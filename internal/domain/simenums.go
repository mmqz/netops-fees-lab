package domain

import "fmt"

// EntityStatus is the up/down state of a simulated device, interface or
// link. Values match the CHECK constraints of the DDL v5 tables.
type EntityStatus string

// Entity statuses.
const (
	EntityStatusUp   EntityStatus = "up"
	EntityStatusDown EntityStatus = "down"
)

// String implements fmt.Stringer.
func (s EntityStatus) String() string { return string(s) }

// IsValid reports whether s is a known entity status.
func (s EntityStatus) IsValid() bool {
	switch s {
	case EntityStatusUp, EntityStatusDown:
		return true
	}
	return false
}

// ParseEntityStatus converts s into an EntityStatus.
func ParseEntityStatus(s string) (EntityStatus, error) {
	v := EntityStatus(s)
	if !v.IsValid() {
		return "", fmt.Errorf("domain: invalid entity status %q", s)
	}
	return v, nil
}

// WorkspaceStatus enumerates the lifecycle states of a workspace. Only one
// active workspace is the supported mode, but archiving is part of the
// schema contract.
type WorkspaceStatus string

// Workspace statuses.
const (
	WorkspaceStatusActive   WorkspaceStatus = "active"
	WorkspaceStatusArchived WorkspaceStatus = "archived"
)

// String implements fmt.Stringer.
func (s WorkspaceStatus) String() string { return string(s) }

// IsValid reports whether s is a known workspace status.
func (s WorkspaceStatus) IsValid() bool {
	switch s {
	case WorkspaceStatusActive, WorkspaceStatusArchived:
		return true
	}
	return false
}

// ParseWorkspaceStatus converts s into a WorkspaceStatus.
func ParseWorkspaceStatus(s string) (WorkspaceStatus, error) {
	v := WorkspaceStatus(s)
	if !v.IsValid() {
		return "", fmt.Errorf("domain: invalid workspace status %q", s)
	}
	return v, nil
}

// ChangeSetTrigger enumerates how a changeset came to exist: direct user
// request, undo of another changeset, or a checkpoint restore replayed as
// forward ops (PROJECT_BRIEF_P2 §4).
type ChangeSetTrigger string

// Changeset triggers.
const (
	ChangeSetTriggerUser              ChangeSetTrigger = "user"
	ChangeSetTriggerUndo              ChangeSetTrigger = "undo"
	ChangeSetTriggerCheckpointRestore ChangeSetTrigger = "checkpoint_restore"
)

// String implements fmt.Stringer.
func (t ChangeSetTrigger) String() string { return string(t) }

// IsValid reports whether t is a known changeset trigger.
func (t ChangeSetTrigger) IsValid() bool {
	switch t {
	case ChangeSetTriggerUser, ChangeSetTriggerUndo, ChangeSetTriggerCheckpointRestore:
		return true
	}
	return false
}

// ParseChangeSetTrigger converts s into a ChangeSetTrigger.
func ParseChangeSetTrigger(s string) (ChangeSetTrigger, error) {
	v := ChangeSetTrigger(s)
	if !v.IsValid() {
		return "", fmt.Errorf("domain: invalid changeset trigger %q", s)
	}
	return v, nil
}

// ChangeSetStatus enumerates the terminal states of a changeset: applied
// (state was mutated), reverted (an undo changeset reversed it) or failed
// (nothing was applied — the transaction rolled back).
type ChangeSetStatus string

// Changeset statuses.
const (
	ChangeSetStatusApplied  ChangeSetStatus = "applied"
	ChangeSetStatusReverted ChangeSetStatus = "reverted"
	ChangeSetStatusFailed   ChangeSetStatus = "failed"
)

// String implements fmt.Stringer.
func (s ChangeSetStatus) String() string { return string(s) }

// IsValid reports whether s is a known changeset status.
func (s ChangeSetStatus) IsValid() bool {
	switch s {
	case ChangeSetStatusApplied, ChangeSetStatusReverted, ChangeSetStatusFailed:
		return true
	}
	return false
}

// ParseChangeSetStatus converts s into a ChangeSetStatus.
func ParseChangeSetStatus(s string) (ChangeSetStatus, error) {
	v := ChangeSetStatus(s)
	if !v.IsValid() {
		return "", fmt.Errorf("domain: invalid changeset status %q", s)
	}
	return v, nil
}

// CheckTrigger enumerates what started a check run: an explicit human action
// (manual), the HTTP API, an MCP tool call, or the automatic run right after
// a successfully applied changeset (PROJECT_BRIEF_P2 §4 step 3).
type CheckTrigger string

// Check run triggers.
const (
	CheckTriggerManual       CheckTrigger = "manual"
	CheckTriggerAPI          CheckTrigger = "api"
	CheckTriggerMCP          CheckTrigger = "mcp"
	CheckTriggerAutoAfterOps CheckTrigger = "auto_after_ops"
)

// String implements fmt.Stringer.
func (t CheckTrigger) String() string { return string(t) }

// IsValid reports whether t is a known check trigger.
func (t CheckTrigger) IsValid() bool {
	switch t {
	case CheckTriggerManual, CheckTriggerAPI, CheckTriggerMCP, CheckTriggerAutoAfterOps:
		return true
	}
	return false
}

// ParseCheckTrigger converts s into a CheckTrigger.
func ParseCheckTrigger(s string) (CheckTrigger, error) {
	v := CheckTrigger(s)
	if !v.IsValid() {
		return "", fmt.Errorf("domain: invalid check trigger %q", s)
	}
	return v, nil
}

// OpResult is the per-op outcome recorded in changeset_ops. Applied
// changesets record every op as applied; failed changesets record exactly
// one op as failed and all others as not_attempted (PROJECT_BRIEF_P2 §4).
type OpResult string

// Operation results.
const (
	OpResultApplied      OpResult = "applied"
	OpResultFailed       OpResult = "failed"
	OpResultNotAttempted OpResult = "not_attempted"
)

// String implements fmt.Stringer.
func (r OpResult) String() string { return string(r) }

// IsValid reports whether r is a known op result.
func (r OpResult) IsValid() bool {
	switch r {
	case OpResultApplied, OpResultFailed, OpResultNotAttempted:
		return true
	}
	return false
}

// ParseOpResult converts s into an OpResult.
func ParseOpResult(s string) (OpResult, error) {
	v := OpResult(s)
	if !v.IsValid() {
		return "", fmt.Errorf("domain: invalid op result %q", s)
	}
	return v, nil
}
