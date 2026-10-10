package domain

import "time"

// Resource is a normalized network entity (device, ip_pool or circuit)
// imported from a workbook row. Attributes holds the normalized schema
// fields of PROJECT_BRIEF §6 as strings (empty values are omitted by the
// importer). The (Type, NaturalKey) pair is unique.
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
}

// Relation is a typed edge between two resources. Current closed set:
// assigned_to (ip_pool → device), a_end and z_end (circuit → device),
// belongs_to (machine_room → site, v0.4 W3-D).
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
