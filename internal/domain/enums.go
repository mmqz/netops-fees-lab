package domain

import "fmt"

// ResourceType enumerates the resource kinds of the unified graph. Values
// match the SQLite CHECK constraint of the resources table (PROJECT_BRIEF
// §5 + migrations v1/v6/v9/v10/v13, extended by v15's W21 扩容): the 8
// pre-W21 types plus the 25 new types of the data-model v1.0 §3 catalog —
// the 33-type closed set covering all seven layers of §10.2.
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
	ResourceTypePTPPort     ResourceType = "ptp_port"

	// W21 扩容（data-model v1.0 §3，migration v15）——L0 空间层.
	ResourceTypeRack    ResourceType = "rack"
	ResourceTypeDuct    ResourceType = "duct"
	ResourceTypeManhole ResourceType = "manhole"
	ResourceTypePole    ResourceType = "pole"
	// L1 物理层（设备）.
	ResourceTypeBoard ResourceType = "board"
	// L2 物理层（光缆）.
	ResourceTypeOpticalCable ResourceType = "optical_cable"
	ResourceTypeCableSegment ResourceType = "cable_segment"
	ResourceTypeFiber        ResourceType = "fiber"
	ResourceTypeODF          ResourceType = "odf"
	ResourceTypeODFTerminal  ResourceType = "odf_terminal"
	ResourceTypeSpliceBox    ResourceType = "splice_box"
	// L3 传输层（protection_group 本体归 L5 逻辑层，§10.2 层次表）.
	ResourceTypeOTNOch          ResourceType = "otn_och"
	ResourceTypeSDHTimeslot     ResourceType = "sdh_timeslot"
	ResourceTypeProtectionGroup ResourceType = "protection_group"
	// L5 逻辑层.
	ResourceTypeRing        ResourceType = "ring"
	ResourceTypeIGPDomain   ResourceType = "igp_domain"
	ResourceTypeVPNInstance ResourceType = "vpn_instance"
	// L6 业务层.
	ResourceTypeCustomer   ResourceType = "customer"
	ResourceTypeService    ResourceType = "service"
	ResourceTypeLeasedLine ResourceType = "leased_line"
	// L4 接入层.
	ResourceTypeBBU     ResourceType = "bbu"
	ResourceTypeRRU     ResourceType = "rru"
	ResourceTypeOLT     ResourceType = "olt"
	ResourceTypeONU     ResourceType = "onu"
	ResourceTypePONPort ResourceType = "pon_port"
)

// String implements fmt.Stringer.
func (t ResourceType) String() string { return string(t) }

// W21NewResourceTypes lists the 25 W21 扩容 types (data-model v1.0 §3) in
// design-doc catalog order — space → device → optical → transport →
// logic → business → access, mirroring §3.3's migration skeleton comments.
// The 8 pre-W21 types are not listed; callers distinguishing old vs new
// (e.g. B8 KeyNormalize) enumerate this slice.
var W21NewResourceTypes = []ResourceType{
	ResourceTypeRack, ResourceTypeDuct, ResourceTypeManhole, ResourceTypePole,
	ResourceTypeBoard,
	ResourceTypeOpticalCable, ResourceTypeCableSegment, ResourceTypeFiber,
	ResourceTypeODF, ResourceTypeODFTerminal, ResourceTypeSpliceBox,
	ResourceTypeOTNOch, ResourceTypeSDHTimeslot, ResourceTypeProtectionGroup,
	ResourceTypeRing, ResourceTypeIGPDomain, ResourceTypeVPNInstance,
	ResourceTypeCustomer, ResourceTypeService, ResourceTypeLeasedLine,
	ResourceTypeBBU, ResourceTypeRRU, ResourceTypeOLT, ResourceTypeONU, ResourceTypePONPort,
}

// IsValid reports whether t is a known resource type. The values must stay
// in sync with the resources table CHECK constraint (migrations v1 + v6 +
// v9 + v10 + v13 + v15: the 33-type closed set of data-model v1.0 §3.3).
// port, base_station and ptp_port resources carry no derived relations;
// site is a plain container; machine_room derives belongs_to relations to
// site (RelationTypeBelongsTo). ptp_port (v0.5 W8-D) is the per-model PTP
// port template row synthesized from ZTE VRP configuration scripts. The 25
// W21 types derive relations per their KindRef fields
// (mapping.RelationTypeFor, §4.2 关系目录).
func (t ResourceType) IsValid() bool {
	switch t {
	case ResourceTypeDevice, ResourceTypeIPPool, ResourceTypeCircuit, ResourceTypePort,
		ResourceTypeBaseStation, ResourceTypeSite, ResourceTypeMachineRoom, ResourceTypePTPPort:
		return true
	}
	for _, nt := range W21NewResourceTypes {
		if t == nt {
			return true
		}
	}
	return false
}

// AllResourceTypes returns every valid resource type: the 8 pre-W21 types
// in canonical Specs order followed by the 25 W21 types in catalog order.
// The list is freshly built — callers may mutate it. mapping.Specs remains
// the authority for canonical ORDER; this helper is for closed-set
// enumeration in tests and validation.
func AllResourceTypes() []ResourceType {
	all := []ResourceType{
		ResourceTypeDevice, ResourceTypeIPPool, ResourceTypeCircuit, ResourceTypePort,
		ResourceTypeBaseStation, ResourceTypeSite, ResourceTypeMachineRoom, ResourceTypePTPPort,
	}
	return append(all, W21NewResourceTypes...)
}

// ParseResourceType converts s into a ResourceType.
func ParseResourceType(s string) (ResourceType, error) {
	t := ResourceType(s)
	if !t.IsValid() {
		return "", fmt.Errorf("domain: invalid resource type %q", s)
	}
	return t, nil
}

// RelationType enumerates the derived relations. Values must stay in sync
// with the relations table CHECK constraint (migrations v1 + v10 + v16):
// the 29-value closed set of data-model v1.0 §4.3 — the 4 pre-W21
// relations plus the 25 new W21 relations. belongs_to was added by
// migration v10; the other 25 by migration v16 (W21-D).
type RelationType string

// Relation types.
const (
	RelationTypeAssignedTo RelationType = "assigned_to" // ip_pool → device
	RelationTypeAEnd       RelationType = "a_end"       // circuit → device
	RelationTypeZEnd       RelationType = "z_end"       // circuit → device
	RelationTypeBelongsTo  RelationType = "belongs_to"  // machine_room → site（容器归属）

	// W21 扩容（data-model v1.0 §4.2，migration v16）——空间归属.
	RelationTypeLocatedIn   RelationType = "located_in"   // rack → machine_room
	RelationTypeHousedIn    RelationType = "housed_in"    // device → machine_room
	RelationTypeInstalledIn RelationType = "installed_in" // board → device
	RelationTypeMountedOn   RelationType = "mounted_on"   // port → board
	// 物理连接.
	RelationTypeCableAEnd       RelationType = "cable_a_end"      // optical_cable → site（A端）
	RelationTypeCableZEnd       RelationType = "cable_z_end"      // optical_cable → site（Z端）
	RelationTypeFiberOf         RelationType = "fiber_of"         // fiber → optical_cable（§4.2 carries_fiber 的 W21 方向勘误）
	RelationTypeFiberTerminates RelationType = "fiber_terminates" // fiber → odf_terminal
	RelationTypeSegmentOf       RelationType = "segment_of"       // cable_segment → optical_cable
	RelationTypeConnectsDevice  RelationType = "connects_device"  // optical_cable → device
	// 传输承载.
	RelationTypeCarriedBy  RelationType = "carried_by"  // circuit → otn_och | sdh_timeslot
	RelationTypeRoutedOver RelationType = "routed_over" // otn_och → optical_cable
	RelationTypeUsesFiber  RelationType = "uses_fiber"  // otn_och → fiber
	// 逻辑归属.
	RelationTypeMemberOfRing RelationType = "member_of_ring" // device → ring
	RelationTypeProtects     RelationType = "protects"       // protection_group → circuit
	RelationTypeInDomain     RelationType = "in_domain"      // device → igp_domain
	RelationTypeInVPN        RelationType = "in_vpn"         // port → vpn_instance
	// 业务服务.
	RelationTypeServes       RelationType = "serves"        // device | base_station → customer
	RelationTypeSubscribedBy RelationType = "subscribed_by" // service → customer
	RelationTypeProvidedBy   RelationType = "provided_by"   // service → circuit
	RelationTypeDependsOn    RelationType = "depends_on"    // service → device | circuit
	// 接入关联.
	RelationTypeUplinkTo    RelationType = "uplink_to"    // base_station → device
	RelationTypeHomeBBU     RelationType = "home_bbu"     // rru → bbu
	RelationTypeCovers      RelationType = "covers"       // rru → base_station
	RelationTypeConnectsONU RelationType = "connects_onu" // onu → pon_port
)

// String implements fmt.Stringer.
func (t RelationType) String() string { return string(t) }

// IsValid reports whether t is a known relation type.
func (t RelationType) IsValid() bool {
	switch t {
	case RelationTypeAssignedTo, RelationTypeAEnd, RelationTypeZEnd, RelationTypeBelongsTo:
		return true
	}
	for _, nt := range W21NewRelationTypes {
		if t == nt {
			return true
		}
	}
	return false
}

// W21NewRelationTypes lists the 25 W21 扩容 relation types (data-model
// v1.0 §4.2) in registry order: 空间归属 → 物理连接 → 传输承载 → 逻辑
// 归属 → 业务服务 → 接入关联. The 4 pre-W21 relations are not listed.
var W21NewRelationTypes = []RelationType{
	RelationTypeLocatedIn, RelationTypeHousedIn, RelationTypeInstalledIn, RelationTypeMountedOn,
	RelationTypeCableAEnd, RelationTypeCableZEnd, RelationTypeFiberOf, RelationTypeFiberTerminates,
	RelationTypeSegmentOf, RelationTypeConnectsDevice,
	RelationTypeCarriedBy, RelationTypeRoutedOver, RelationTypeUsesFiber,
	RelationTypeMemberOfRing, RelationTypeProtects, RelationTypeInDomain, RelationTypeInVPN,
	RelationTypeServes, RelationTypeSubscribedBy, RelationTypeProvidedBy, RelationTypeDependsOn,
	RelationTypeUplinkTo, RelationTypeHomeBBU, RelationTypeCovers, RelationTypeConnectsONU,
}

// AllRelationTypes returns every valid relation type: the 4 pre-W21
// relations followed by the 25 W21 relations. Freshly built for closed-set
// enumeration; mapping.RelationSpecs() remains the ORDER authority.
func AllRelationTypes() []RelationType {
	all := []RelationType{RelationTypeAssignedTo, RelationTypeAEnd, RelationTypeZEnd, RelationTypeBelongsTo}
	return append(all, W21NewRelationTypes...)
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

// ChangeStatus enumerates the lifecycle states of a resource attribute
// change proposal (resource_attribute_changes.status, migration v11).
// pending is the only operable state (apply/dismiss both refuse anything
// else); applied and dismissed are terminal — an applied change has written
// its new value into the resource attributes, a dismissed one was
// deliberately ignored. Both survive a batch rollback as the human-decision
// audit trail (pending proposals of the rolled-back batch are deleted).
type ChangeStatus string

// Change statuses.
const (
	ChangeStatusPending   ChangeStatus = "pending"
	ChangeStatusApplied   ChangeStatus = "applied"
	ChangeStatusDismissed ChangeStatus = "dismissed"
)

// String implements fmt.Stringer.
func (c ChangeStatus) String() string { return string(c) }

// IsValid reports whether c is a known change status. The values must stay
// in sync with the resource_attribute_changes CHECK constraint
// (migrations v11).
func (c ChangeStatus) IsValid() bool {
	switch c {
	case ChangeStatusPending, ChangeStatusApplied, ChangeStatusDismissed:
		return true
	default:
		return false
	}
}

// ParseChangeStatus converts s into a ChangeStatus.
func ParseChangeStatus(s string) (ChangeStatus, error) {
	v := ChangeStatus(s)
	if !v.IsValid() {
		return "", fmt.Errorf("domain: invalid change status %q", s)
	}
	return v, nil
}

// RenameStatus enumerates the lifecycle states of a rename proposal
// (rename_proposals.status, migration v12). pending is the only operable
// state (apply/reject both refuse anything else); applied and rejected are
// terminal — an applied proposal has merged the two resources (the new
// resource is deleted, the old one carries the new key), a rejected one
// left both resources independent (the import's default outcome). Both
// survive a batch rollback as the human-decision audit trail (pending
// proposals of the rolled-back batch are deleted).
type RenameStatus string

// Rename statuses.
const (
	RenameStatusPending  RenameStatus = "pending"
	RenameStatusApplied  RenameStatus = "applied"
	RenameStatusRejected RenameStatus = "rejected"
)

// String implements fmt.Stringer.
func (r RenameStatus) String() string { return string(r) }

// IsValid reports whether r is a known rename status. The values must stay
// in sync with the rename_proposals CHECK constraint (migrations v12).
func (r RenameStatus) IsValid() bool {
	switch r {
	case RenameStatusPending, RenameStatusApplied, RenameStatusRejected:
		return true
	default:
		return false
	}
}

// ParseRenameStatus converts s into a RenameStatus.
func ParseRenameStatus(s string) (RenameStatus, error) {
	v := RenameStatus(s)
	if !v.IsValid() {
		return "", fmt.Errorf("domain: invalid rename status %q", s)
	}
	return v, nil
}
