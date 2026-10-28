package domain

import "time"

// Resource is a normalized network entity (device, ip_pool or circuit)
// imported from a workbook row. Attributes holds the normalized schema
// fields of PROJECT_BRIEF §6 as strings (empty values are omitted by the
// importer). The (Type, NaturalKey) pair is unique.
//
// RunState / RunStateAt are the W22 operational-state SNAPSHOT columns
// (migration v17, §12-B1 方案 C): the projection of the newest
// resource_status observation. They are READ on every scan and written ONLY
// by ResourceStatusRepo.UpsertStatus — the importer never sets them (Insert
// does not include the columns; new resources are NULL = unknown), and the
// ledger fields (Status/Attributes/UpdatedAt) never move on a run-state
// write: 台账状态与运行状态彻底分立.
type Resource struct {
	ID           string
	Type         ResourceType
	NaturalKey   string
	Name         string
	Attributes   map[string]string
	Status       string // free text, empty means unknown
	FirstBatchID string
	CreatedAt    time.Time
	UpdatedAt    time.Time

	// RunState is the snapshot of the newest run-state observation; empty
	// means NULL = no observation at all (distinct from "unknown", which is
	// an explicitly recorded state).
	RunState string
	// RunStateAt is the ts of the observation the snapshot projects; the
	// zero time means no observation.
	RunStateAt time.Time

	// GeoLat/GeoLng are the REAL-column projection of the geo_lat/geo_lng
	// attributes (migration v20, issue #45). nil means "no coordinates"
	// — never 0/0, which would fabricate a null-island position. The
	// attributes JSON keeps its string form as the lossless source of
	// truth; these pointers exist so SQL can range-scan and rank without
	// parsing JSON per row (see ResourceRepo.Nearest).
	GeoLat *float64
	GeoLng *float64

	// SiteNature / PowerMode / Lifecycle are the #38 structured
	// master-data columns (migration v40): the three answers the
	// cross-dataset reconcile needs to ATTRIBUTE an only_tower /
	// only_electricity row instead of merely listing it — 站址性质
	// (self_built/shared/leased), 供电方式 (direct/transfer/none),
	// 生命周期 (active/retired/dismantled). Empty string means NULL =
	// master data missing, the reconcile's honest "unknown" (never a
	// silent "normal"). The v20 geo trio is the precedent: REAL columns
	// beside the attributes JSON, because free text cannot be
	// CHECK-constrained or joined. The importer projects RECOGNIZED
	// sheet values here (ParseSiteNatureValue & co.); an out-of-set
	// value warns W002 and leaves the column NULL — never a row
	// failure, never a guess (the status-field doctrine).
	SiteNature string
	PowerMode  string
	Lifecycle  string
}

// Relation is a typed edge between two resources. Current closed set:
// assigned_to (ip_pool → device), a_end and z_end (circuit → device),
// belongs_to (machine_room → site, v0.4 W3-D); ptp_port (v0.5 W8-D)
// derives no relations — the config scripts carry model-level template
// identity, no device-instance identity.
type Relation struct {
	ID             string
	Type           RelationType
	FromResourceID string
	ToResourceID   string
	Attributes     map[string]string
	FirstBatchID   string
	CreatedAt      time.Time
}

// SourceRef records the origin of a resource or relation: the workbook,
// sheet and 1-based Excel row that produced it. Raw holds the original row
// as {header: value} pairs.
type SourceRef struct {
	ID         string
	EntityKind EntityKind
	EntityID   string
	BatchID    string
	Workbook   string
	Sheet      string
	RowNumber  int // 1-based Excel row; row 1 is the header row
	Raw        map[string]string
}

// ImportBatch is one import run of a single workbook. Status follows the
// state machine analyzing → awaiting_mapping → importing →
// completed | failed, with rolled_back as the post-terminal rollback state.
type ImportBatch struct {
	ID             string
	WorkbookName   string
	WorkbookSHA256 string
	Status         BatchStatus
	Mapping        string // confirmed mapping JSON (PROJECT_BRIEF §8)
	RuleVersion    string
	RowsTotal      int
	RowsImported   int
	RowsSkipped    int
	RowsFailed     int
	Error          string
	StartedAt      time.Time
	FinishedAt     *time.Time
}

// RowResult is the accounting record for a single data row of a batch.
// RowNumber is the real 1-based Excel row number (row 1 is the header).
// RowResults are persisted with an autoincrement id which is populated on
// read and ignored on insert.
type RowResult struct {
	ID        int64
	BatchID   string
	Sheet     string
	RowNumber int
	Status    RowStatus
	EntityID  string
	RuleCode  string
	Message   string
}

// Issue is a data-quality finding produced by the validation rules. A nil
// BatchID marks a set-level (cross-batch) issue. A nil SourceRefID marks an
// application-generated issue; row-phase failures store their origin in
// Evidence ("sheet" and "row_number") instead, because the failed row has
// no entity and therefore no source_ref row.
type Issue struct {
	ID          string
	BatchID     *string
	RuleCode    string
	RuleVersion string
	Severity    Severity
	EntityKind  EntityKind // zero value means "no entity"
	EntityID    string     // empty means "no entity"
	SourceRefID *string
	Message     string
	Evidence    map[string]string
	CreatedAt   time.Time
}

// AttributeChange is one pending attribute change proposal recorded by the
// importer when a row whose natural key matches an existing resource carries
// attribute values that differ from the stored ones (W005, v0.4 W4-D). The
// row itself is still skipped — the change only takes effect when a human
// applies it via the HTTP API / CLI (never via MCP); dismissed proposals are
// kept as an audit trail. OldValue is the stored attribute value at record
// time (empty when the resource had no value for Field — attributes only
// store non-empty values); NewValue is always non-empty (empty new values
// record nothing, the existing "empty never overwrites" import semantics).
// Pending proposals of a batch are deleted by that batch's rollback;
// applied/dismissed ones survive it (the human decision is decoupled from
// the import-batch lifecycle) — unless their resource itself is deleted by
// the rollback, in which case the ON DELETE CASCADE removes them with it.
type AttributeChange struct {
	ID         string
	ResourceID string
	BatchID    string
	Field      string
	OldValue   string
	NewValue   string
	Status     ChangeStatus
	Sheet      string
	RowNumber  int // 1-based Excel row; row 1 is the header row
	CreatedAt  time.Time
	AppliedAt  *time.Time // set when Status == ChangeStatusApplied
}

// RenameProposal is one pending rename proposal recorded by the importer's
// suspected-rename detection (W006, v0.5 W6-D) when a row of a
// name-keyed type parses to a natural key that does NOT exist yet while an
// existing resource of the same type carries the same non-key attribute
// values and its own key has vanished from the source sheet (the
// "key changed, data unchanged" twin of W005's "key same, data changed").
// The row still imports as a brand-new resource — the identity merge only
// happens when a human applies the proposal (HTTP API / CLI, never MCP).
//
// ResourceID is the OLD resource (the identity keeper): its proxy id res_x,
// relations and SourceRefs survive the apply unchanged — only natural_key
// is rewritten. NewResourceID is the resource created by the detection
// batch; it is deleted on apply (a plain TEXT column, deliberately NOT a
// foreign key: after an apply the referenced row is gone and the id must
// stay as the audit trail). MatchedFields lists the non-key, non-ref,
// non-computed fields whose normalized values were equal on both sides (the
// evidence of the "same asset" hypothesis); at least one non-empty field
// participated or the importer would not have proposed anything.
//
// Pending proposals of a batch are deleted by that batch's rollback (the
// new resource disappears with the batch too, so a dangling proposal would
// be meaningless); applied/rejected proposals survive it as the human-
// decision audit trail — the same asymmetric lifecycle as W005.
type RenameProposal struct {
	ID            string
	ResourceID    string
	NewResourceID string
	BatchID       string
	ResourceType  ResourceType
	OldKey        string
	NewKey        string
	MatchedFields []string
	Status        RenameStatus
	Sheet         string
	RowNumber     int // 1-based Excel row; row 1 is the header row
	CreatedAt     time.Time
	DecidedAt     *time.Time // set when Status == RenameStatusApplied or RenameStatusRejected
}
