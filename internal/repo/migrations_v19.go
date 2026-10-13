package repo

import "sort"

// migrationV19 adds the mapping template store of the unattended import
// channel (schema version 19, W24 — design data-model-v1.md §12-B7 方案 A,
// issue #28):
//
//   - mapping_templates: ONE row per confirmed per-sheet mapping. The
//     identity is UNIQUE (header_fingerprint, resource_type): a
//     re-confirmation of the same sheet shape UPSERTS mapping_json and
//     confirmed_at (idempotent capture), never a second row. The
//     fingerprint is the sha256 of the canonical sheet signature (sheet
//     name + trimmed/fullwidth-folded/lowercased headers IN SHEET ORDER —
//     internal/mapping.Fingerprint), so a column rename, addition, removal
//     or permutation is a different fingerprint: a changed workbook must be
//     re-confirmed by a human, never silently auto-imported with a stale
//     mapping (表头变更感知).
//
//   - resource_type is the validation anchor of the stored mapping; it is
//     deliberately a free TEXT column constrained by the application layer
//     (mapping.Validate + the 33-type Specs registry), NOT a CHECK — the
//     W15+ resource-type set is closed in CODE (the registry is the single
//     fact source; a SQL CHECK would fork it — §12-B5's anti-drift rule).
//
//   - confirmed_batch is the AUDIT pointer to the import batch whose
//     confirmation produced the row. It is deliberately a plain TEXT column
//     with NO foreign key: rollback of that batch must NOT delete the
//     template (a template is a confirmed human artifact, not batch data —
//     the same asymmetric lifecycle as applied attribute-change/rename
//     decisions). An FK with any ON DELETE action would either break the
//     rollback or silently destroy the confirmed artifact; no action at all
//     would make the rollback FAIL on the dangling reference. Plain TEXT is
//     the honest choice, documented in §12 W24 处置.
//
//   - mapping_json is the confirmed single-sheet Mapping document
//     ({"sheets":[one SheetMapping]}). See domain.MappingTemplate.
//
// The migration is purely additive — one plain CREATE TABLE plus the UNIQUE
// lookup index, no rebuild, no data movement, foreign_keys enforcement stays
// ON (the migrations_v18 style). No default templates are seeded: templates
// are USER DATA born from a human confirmation, not schema — the empty table
// until the first capture is the correct day-one state.
//
// Regression note: before this migration any template insert fails with
// "no such table"; after it the template repo round-trips (pinned by
// TestMigrationV19AddsMappingTemplates).
var migrationV19 = Migration{
	Version: 19,
	Name:    "w24_mapping_templates",
	SQL: `
CREATE TABLE mapping_templates (
  template_id        TEXT PRIMARY KEY,
  source_name        TEXT NOT NULL,              -- human label (workbook name origin)
  header_fingerprint TEXT NOT NULL,              -- sha256 hex of the canonical sheet signature
  resource_type      TEXT NOT NULL,              -- the template's target type (validation anchor)
  mapping_json       TEXT NOT NULL,              -- the CONFIRMED mapping (Mapping struct JSON)
  sheet_name         TEXT NOT NULL,
  confirmed_at       TEXT NOT NULL,
  confirmed_batch    TEXT                        -- the batch whose confirmation produced it (audit)
);
CREATE UNIQUE INDEX idx_mapping_templates_fp ON mapping_templates(header_fingerprint, resource_type);
`,
}

// init appends v19 to the ordered migration list (which migrations.go,
// migrations_v4.go … migrations_v18.go own) and re-sorts the list by
// version, following the registration hook pattern of migrations_v4.go.
func init() {
	migrations = append(migrations, migrationV19)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
}
