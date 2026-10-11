package repo

import "sort"

// migrationV12 adds the rename proposal table (schema version 12, W6-D):
// the landing spot of the R4-ruled "name change = identity-change event"
// semantics (natural-keys.md §2). When an imported row of a NAME-keyed type
// (ip_pool, base_station, port — see mapping.TypeSpec.RenameDetection)
// parses to a natural key that does not exist in the database yet, while an
// existing resource of the same type carries identical non-key attribute
// values and its own key has vanished from the source sheet, the importer
// records one PENDING proposal here plus a W006 issue — the row itself still
// imports as a brand-new resource (the safe default; nothing is merged
// automatically). A human then applies (or rejects) the proposal via the
// HTTP API / CLI; applying retargets the new resource's relations to the old
// resource, deletes the new resource, and rewrites the old resource's
// natural_key — all inside one transaction.
//
// The migration is a pure CREATE TABLE (+ indexes), modeled on migration v11:
// no table is rebuilt, no data moves, foreign_keys enforcement can stay ON.
// resource_id (the OLD resource, the identity keeper) carries ON DELETE
// CASCADE so deleting a resource removes its rename history with it —
// including the pending proposals of batches that merely SUGGESTED a rename
// against it. new_resource_id is deliberately a plain TEXT column with NO
// foreign key: apply deletes the very row it points at, and the id must
// survive as the audit trail. batch_id deliberately has NO cascade (same
// ruling as v11): the rollback deletes only the batch's PENDING proposals
// explicitly (applied/rejected stay as the human-decision audit trail, see
// rollback.go and docs/import-and-validation.md §16).
var migrationV12 = Migration{
	Version: 12,
	Name:    "rename_proposals",
	SQL: `
CREATE TABLE rename_proposals (
  id TEXT PRIMARY KEY,                          -- rp_<uuid>
  resource_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,  -- 旧资源（身份保留方）
  new_resource_id TEXT NOT NULL,                -- 新资源 id，纯文本留档（apply 后该行已删，无 FK）
  batch_id TEXT NOT NULL REFERENCES import_batches(id),  -- 无 cascade，同 v11 batch_id 口径
  resource_type TEXT NOT NULL,
  old_key TEXT NOT NULL,
  new_key TEXT NOT NULL,
  matched_fields TEXT NOT NULL DEFAULT '[]',    -- JSON 数组：参与匹配且相等的非键字段名
  status TEXT NOT NULL CHECK (status IN ('pending','applied','rejected')),
  sheet TEXT NOT NULL,
  row_number INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  decided_at TEXT
);
CREATE INDEX idx_rename_proposals_status ON rename_proposals(status);
CREATE INDEX idx_rename_proposals_resource ON rename_proposals(resource_id);
CREATE INDEX idx_rename_proposals_batch ON rename_proposals(batch_id);
`,
}

// init appends v12 to the ordered migration list (which migrations.go,
// migrations_v4.go … migrations_v11.go own) and re-sorts the list by version,
// following the registration hook pattern established by migrations_v4.go
// and reused by v11.
func init() {
	migrations = append(migrations, migrationV12)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
}
