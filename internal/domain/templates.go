package domain

import "time"

// MappingTemplate is one confirmed per-sheet mapping template of the
// unattended import channel (W24, migration v19 — data-model-v1.md §12-B7
// 方案 A, issue #28). It stores the mapping a human ONCE confirmed for a
// sheet whose header row produced HeaderFingerprint, so future workbooks of
// the same shape can import with ZERO human interaction (the「每天导入即可」
// goal of §9.2).
//
// Identity is (HeaderFingerprint, ResourceType) — UNIQUE in the schema: a
// re-confirmation of the same sheet shape UPSERTS the stored mapping and
// stamps a fresh ConfirmedAt (idempotent capture), never a second row. The
// fingerprint covers sheet name AND the normalized header sequence IN SHEET
// ORDER (internal/mapping.Fingerprint), so any column permutation, rename,
// addition or removal is a different fingerprint: a changed workbook is
// honestly re-confirmation, never a silent mismatched auto-import
// (表头变更感知).
//
// MappingJSON is a single-sheet Mapping document ({"sheets":[one entry]} —
// the exact §8 SheetMapping the batch confirmed for this sheet); the
// unattended path re-applies it verbatim, and Execute's ordinary
// mapping.Validate re-checks it against the actual headers before any write.
//
// ConfirmedBatch is the audit pointer to the import batch whose confirmation
// produced the template. Rollback of that batch does NOT delete the template:
// a template is a confirmed ARTIFACT (a human decision), not batch data —
// the same asymmetric lifecycle as applied attribute-change / rename
// decisions (the batch rows disappear, the human decision survives).
type MappingTemplate struct {
	// TemplateID is the row id, "tpl_" prefixed.
	TemplateID string
	// SourceName is the human label of the template (the workbook name of
	// the originating capture, overridable at capture time).
	SourceName string
	// HeaderFingerprint is the sha256 hex of the canonical sheet signature
	// (sheet name + normalized headers, see mapping.Fingerprint).
	HeaderFingerprint string
	// ResourceType is the template's target type — the validation anchor
	// re-checked by mapping.Validate on every unattended execution.
	ResourceType ResourceType
	// MappingJSON is the confirmed single-sheet Mapping document.
	MappingJSON string
	// SheetName is the worksheet name the fingerprint was computed over.
	SheetName string
	// ConfirmedAt is the (re-)confirmation time.
	ConfirmedAt time.Time
	// ConfirmedBatch is the creating batch id; "" is never stored (capture
	// requires a completed batch) but scans defensively tolerate it.
	ConfirmedBatch string
}
