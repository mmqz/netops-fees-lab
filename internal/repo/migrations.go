package repo

import (
	"context"
	"fmt"
	"time"
)

// Migration is one ordered schema change. SQL may contain multiple
// statements; they are executed as one script inside a single transaction
// together with the bookkeeping insert into schema_migrations.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// migrations is the ordered, append-only list of schema versions. DDL is
// verbatim from PROJECT_BRIEF §5 (SQLite Schema DDL v1).
var migrations = []Migration{
	{
		Version: 1,
		Name:    "core_tables",
		SQL: `
CREATE TABLE import_batches (
  id TEXT PRIMARY KEY,
  workbook_name TEXT NOT NULL,
  workbook_sha256 TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('analyzing','awaiting_mapping','importing','completed','failed','rolled_back')),
  mapping TEXT NOT NULL DEFAULT '{}',          -- confirmed mapping JSON (see §8)
  rule_version TEXT NOT NULL,
  rows_total INTEGER NOT NULL DEFAULT 0,
  rows_imported INTEGER NOT NULL DEFAULT 0,
  rows_skipped INTEGER NOT NULL DEFAULT 0,
  rows_failed INTEGER NOT NULL DEFAULT 0,
  error TEXT,
  started_at TEXT NOT NULL,
  finished_at TEXT
);

CREATE TABLE resources (
  id TEXT PRIMARY KEY,
  resource_type TEXT NOT NULL CHECK (resource_type IN ('device','ip_pool','circuit')),
  natural_key TEXT NOT NULL,                   -- device_code | pool_name | circuit_code
  name TEXT NOT NULL,
  attributes TEXT NOT NULL DEFAULT '{}',       -- normalized JSON, keys = field names of §6
  status TEXT,
  first_batch_id TEXT NOT NULL REFERENCES import_batches(id),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE (resource_type, natural_key)
);

CREATE TABLE relations (
  id TEXT PRIMARY KEY,
  relation_type TEXT NOT NULL CHECK (relation_type IN ('assigned_to','a_end','z_end')),
  from_resource_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
  to_resource_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
  attributes TEXT NOT NULL DEFAULT '{}',
  first_batch_id TEXT NOT NULL REFERENCES import_batches(id),
  created_at TEXT NOT NULL,
  UNIQUE (from_resource_id, to_resource_id, relation_type)
);

CREATE TABLE source_refs (
  id TEXT PRIMARY KEY,
  entity_kind TEXT NOT NULL CHECK (entity_kind IN ('resource','relation')),
  entity_id TEXT NOT NULL,
  batch_id TEXT NOT NULL REFERENCES import_batches(id) ON DELETE CASCADE,
  workbook TEXT NOT NULL,
  sheet TEXT NOT NULL,
  row_number INTEGER NOT NULL,
  raw TEXT NOT NULL DEFAULT '{}',              -- original row as {header: value}
  UNIQUE (entity_kind, entity_id, batch_id)
);
CREATE INDEX idx_source_refs_entity ON source_refs(entity_kind, entity_id);
`,
	},
	{
		Version: 2,
		Name:    "import_row_results",
		SQL: `
CREATE TABLE import_row_results (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  batch_id TEXT NOT NULL REFERENCES import_batches(id) ON DELETE CASCADE,
  sheet TEXT NOT NULL,
  row_number INTEGER NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('imported','skipped','failed')),
  entity_id TEXT,
  rule_code TEXT,                              -- set for failed/skipped rows
  message TEXT,
  UNIQUE (batch_id, sheet, row_number)
);
`,
	},
	{
		Version: 3,
		Name:    "issues",
		SQL: `
CREATE TABLE issues (
  id TEXT PRIMARY KEY,
  batch_id TEXT,                               -- NULL ⇒ set-level (cross-batch) issue
  rule_code TEXT NOT NULL,
  rule_version TEXT NOT NULL,
  severity TEXT NOT NULL CHECK (severity IN ('error','warning','info')),
  entity_kind TEXT,
  entity_id TEXT,
  source_ref_id TEXT,                          -- NULL ⇒ application-generated
  message TEXT NOT NULL,
  evidence TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);
CREATE INDEX idx_issues_batch ON issues(batch_id);
CREATE INDEX idx_issues_rule ON issues(rule_code);
`,
	},
}

// applyMigrations creates the schema_migrations bookkeeping table and
// applies every not-yet-applied migration in order, each in its own
// transaction. Already-applied versions are skipped, so calling Open
// repeatedly on the same file is idempotent.
func (d *DB) applyMigrations(ctx context.Context) error {
	if _, err := d.sql.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  applied_at TEXT NOT NULL
)`); err != nil {
		return fmt.Errorf("repo: create schema_migrations: %w", err)
	}
	applied := make(map[int]struct{})
	rows, err := d.sql.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("repo: read schema_migrations: %w", err)
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return fmt.Errorf("repo: scan schema_migrations: %w", err)
		}
		applied[v] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("repo: close schema_migrations rows: %w", err)
	}
	for _, m := range migrations {
		if _, done := applied[m.Version]; done {
			continue
		}
		if err := d.applyMigration(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

// applyMigration runs one migration and records it in schema_migrations
// inside the same transaction.
func (d *DB) applyMigration(ctx context.Context, m Migration) error {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("repo: migration %d (%s): begin: %w", m.Version, m.Name, err)
	}
	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("repo: migration %d (%s): exec: %w", m.Version, m.Name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?,?,?)`,
		m.Version, m.Name, formatTime(time.Now().UTC()),
	); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("repo: migration %d (%s): record: %w", m.Version, m.Name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("repo: migration %d (%s): commit: %w", m.Version, m.Name, err)
	}
	return nil
}
