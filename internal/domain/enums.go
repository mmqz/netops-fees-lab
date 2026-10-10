package domain

import "fmt"

// ResourceType enumerates the resource kinds of Phase 1. Values match the
// SQLite CHECK constraint of the resources table (PROJECT_BRIEF §5).
type ResourceType string

// Resource types.
const (
	ResourceTypeDevice      ResourceType = "device"
	ResourceTypeIPPool      ResourceType = "ip_pool"
	ResourceTypeCircuit     ResourceType = "circuit"
	ResourceTypePort        ResourceType = "port"
	ResourceTypeBaseStation ResourceType = "base_station"
	ResourceTypeSite        ResourceType = "site"
	ResourceTypeMachineRoom ResourceType = "machine_room"
)

// String implements fmt.Stringer.
func (t ResourceType) String() string { return string(t) }

// IsValid reports whether t is a known resource type. The values must stay
// in sync with the resources table CHECK constraint (migrations v1 + v6 +
// v9 + v10: 'device', 'ip_pool', 'circuit', 'port', 'base_station', 'site',
// 'machine_room'). port and base_station resources carry no derived
// relations; site is a plain container; machine_room derives belongs_to
// relations to site (RelationTypeBelongsTo).
func (t ResourceType) IsValid() bool {
	switch t {
	case ResourceTypeDevice, ResourceTypeIPPool, ResourceTypeCircuit, ResourceTypePort,
		ResourceTypeBaseStation, ResourceTypeSite, ResourceTypeMachineRoom:
		return true
	}
	return false
}

// ParseResourceType converts s into a ResourceType.
func ParseResourceType(s string) (ResourceType, error) {
	t := ResourceType(s)
	if !t.IsValid() {
		return "", fmt.Errorf("domain: invalid resource type %q", s)
	}
	return t, nil
}

// RelationType enumerates the derived relations (PROJECT_BRIEF §6;
// belongs_to added by migration v10). Values must stay in sync with the
// relations table CHECK constraint.
type RelationType string

// Relation types.
const (
	RelationTypeAssignedTo RelationType = "assigned_to" // ip_pool → device
	RelationTypeAEnd       RelationType = "a_end"       // circuit → device
	RelationTypeZEnd       RelationType = "z_end"       // circuit → device
	RelationTypeBelongsTo  RelationType = "belongs_to"  // machine_room → site（容器归属）
)

// String implements fmt.Stringer.
func (t RelationType) String() string { return string(t) }

// IsValid reports whether t is a known relation type.
func (t RelationType) IsValid() bool {
	switch t {
	case RelationTypeAssignedTo, RelationTypeAEnd, RelationTypeZEnd, RelationTypeBelongsTo:
		return true
	}
	return false
}

// ParseRelationType converts s into a RelationType.
func ParseRelationType(s string) (RelationType, error) {
	t := RelationType(s)
	if !t.IsValid() {
		return "", fmt.Errorf("domain: invalid relation type %q", s)
	}
	return t, nil
}

// BatchStatus enumerates the import batch state machine states
// (PROJECT_BRIEF §8).
type BatchStatus string

// Batch statuses.
const (
	BatchStatusAnalyzing       BatchStatus = "analyzing"
	BatchStatusAwaitingMapping BatchStatus = "awaiting_mapping"
	BatchStatusImporting       BatchStatus = "importing"
	BatchStatusCompleted       BatchStatus = "completed"
	BatchStatusFailed          BatchStatus = "failed"
	BatchStatusRolledBack      BatchStatus = "rolled_back"
)

// String implements fmt.Stringer.
func (s BatchStatus) String() string { return string(s) }

// IsValid reports whether s is a known batch status.
func (s BatchStatus) IsValid() bool {
	switch s {
	case BatchStatusAnalyzing, BatchStatusAwaitingMapping, BatchStatusImporting,
		BatchStatusCompleted, BatchStatusFailed, BatchStatusRolledBack:
		return true
	}
	return false
}

// IsTerminal reports whether the import pipeline can no longer leave this
// state except via rollback. finished_at is stamped when a batch first
// enters a terminal state.
func (s BatchStatus) IsTerminal() bool {
	switch s {
	case BatchStatusCompleted, BatchStatusFailed, BatchStatusRolledBack:
		return true
	}
	return false
}

// ParseBatchStatus converts s into a BatchStatus.
func ParseBatchStatus(s string) (BatchStatus, error) {
	v := BatchStatus(s)
	if !v.IsValid() {
		return "", fmt.Errorf("domain: invalid batch status %q", s)
	}
	return v, nil
}

// RowStatus enumerates the per-row import outcomes (PROJECT_BRIEF §8).
type RowStatus string

// Row statuses.
const (
	RowStatusImported RowStatus = "imported"
	RowStatusSkipped  RowStatus = "skipped"
	RowStatusFailed   RowStatus = "failed"
)

// String implements fmt.Stringer.
func (s RowStatus) String() string { return string(s) }

// IsValid reports whether s is a known row status.
func (s RowStatus) IsValid() bool {
	switch s {
	case RowStatusImported, RowStatusSkipped, RowStatusFailed:
		return true
	}
	return false
}

// ParseRowStatus converts s into a RowStatus.
func ParseRowStatus(s string) (RowStatus, error) {
	v := RowStatus(s)
	if !v.IsValid() {
		return "", fmt.Errorf("domain: invalid row status %q", s)
	}
	return v, nil
}

// Severity enumerates issue severities (PROJECT_BRIEF §7).
type Severity string

// Issue severities.
const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

// String implements fmt.Stringer.
func (s Severity) String() string { return string(s) }

// IsValid reports whether s is a known severity.
func (s Severity) IsValid() bool {
	switch s {
	case SeverityError, SeverityWarning, SeverityInfo:
		return true
	}
	return false
}

// ParseSeverity converts s into a Severity.
func ParseSeverity(s string) (Severity, error) {
	v := Severity(s)
	if !v.IsValid() {
		return "", fmt.Errorf("domain: invalid severity %q", s)
	}
	return v, nil
}

// EntityKind distinguishes resources from relations for source refs and
// issues. Values match the SQLite CHECK constraint of source_refs.
type EntityKind string

// Entity kinds.
const (
	EntityKindResource EntityKind = "resource"
	EntityKindRelation EntityKind = "relation"
)

// String implements fmt.Stringer.
func (k EntityKind) String() string { return string(k) }

// IsValid reports whether k is a known entity kind.
func (k EntityKind) IsValid() bool {
	switch k {
	case EntityKindResource, EntityKindRelation:
		return true
	}
	return false
}

// ParseEntityKind converts s into an EntityKind.
func ParseEntityKind(s string) (EntityKind, error) {
	v := EntityKind(s)
	if !v.IsValid() {
		return "", fmt.Errorf("domain: invalid entity kind %q", s)
	}
	return v, nil
}
