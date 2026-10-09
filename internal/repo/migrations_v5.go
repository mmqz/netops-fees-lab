package repo

import "sort"

// migrationV5 creates the Phase 2 simulation workbench tables
// (PROJECT_BRIEF_P2 §3, DDL v5 — verbatim): workspaces, global per-kind id
// sequences, virtual devices, interfaces, links, changesets with per-op
// rows, check runs with results, and checkpoints. Go structs deliberately
// avoid the bare name "Interface" (domain.SimInterface) because of the
// standard library clash; the SQL table keeps the pinned name `interfaces`.
var migrationV5 = Migration{
	Version: 5,
	Name:    "simulation_tables",
	SQL: `
CREATE TABLE workspaces (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','archived')),
  source_json TEXT NOT NULL DEFAULT '{}',     -- {"batchIds":[…]} lineage
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE sequences (
  kind TEXT NOT NULL CHECK (kind IN ('vd','if','ln','cs','run','cp')),
  last_value INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (kind)
);

CREATE TABLE virtual_devices (
  id TEXT PRIMARY KEY,                        -- 'vd-<n>' (global sequence)
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  resource_id TEXT REFERENCES resources(id) ON DELETE SET NULL,
  code TEXT NOT NULL,                         -- device_code from the resource
  name TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'up' CHECK (status IN ('up','down')),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE (workspace_id, code)
);
CREATE INDEX idx_virtual_devices_ws ON virtual_devices(workspace_id);

CREATE TABLE interfaces (
  id TEXT PRIMARY KEY,                        -- 'if-<n>'
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  device_id TEXT NOT NULL REFERENCES virtual_devices(id) ON DELETE CASCADE,
  name TEXT NOT NULL,                         -- 'eth0', 'eth1', …
  status TEXT NOT NULL DEFAULT 'up' CHECK (status IN ('up','down')),
  ipv4_addr TEXT,                             -- dotted quad, normalized; NULL = unconfigured
  ipv4_prefix INTEGER,                       -- 0..32; NULL when addr NULL
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE (workspace_id, device_id, name)
);
CREATE INDEX idx_interfaces_ws ON interfaces(workspace_id);
CREATE INDEX idx_interfaces_device ON interfaces(device_id);

CREATE TABLE links (
  id TEXT PRIMARY KEY,                        -- 'ln-<n>'
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  a_interface_id TEXT NOT NULL REFERENCES interfaces(id) ON DELETE CASCADE,
  z_interface_id TEXT NOT NULL REFERENCES interfaces(id) ON DELETE CASCADE,
  status TEXT NOT NULL DEFAULT 'up' CHECK (status IN ('up','down')),
  label TEXT NOT NULL DEFAULT '',             -- circuit_code when derived
  resource_id TEXT REFERENCES resources(id) ON DELETE SET NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  CHECK (a_interface_id <> z_interface_id)
);
CREATE INDEX idx_links_ws ON links(workspace_id);

CREATE TABLE changesets (
  id TEXT PRIMARY KEY,                        -- 'cs-<n>'
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  description TEXT NOT NULL DEFAULT '',
  trigger TEXT NOT NULL CHECK (trigger IN ('user','undo','checkpoint_restore')),
  status TEXT NOT NULL DEFAULT 'applied' CHECK (status IN ('applied','reverted','failed')),
  ops_applied INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  reverted_at TEXT
);
CREATE INDEX idx_changesets_ws ON changesets(workspace_id, id);

CREATE TABLE changeset_ops (
  changeset_id TEXT NOT NULL REFERENCES changesets(id) ON DELETE CASCADE,
  seq INTEGER NOT NULL,                       -- 1-based, application order
  op_code TEXT NOT NULL,
  op_json TEXT NOT NULL,                      -- forward op, typed schema
  inverse_json TEXT,                          -- inverse op; NULL in failed changesets
  result TEXT NOT NULL CHECK (result IN ('applied','failed','not_attempted')),
  PRIMARY KEY (changeset_id, seq)
);

CREATE TABLE check_runs (
  id TEXT PRIMARY KEY,                        -- 'run-<n>'
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  changeset_id TEXT REFERENCES changesets(id) ON DELETE SET NULL,
  trigger TEXT NOT NULL CHECK (trigger IN ('manual','api','mcp','auto_after_ops')),
  rule_version TEXT NOT NULL,
  started_at TEXT NOT NULL,
  duration_ms INTEGER NOT NULL,
  score INTEGER NOT NULL,
  topology_json TEXT NOT NULL                 -- canonical edge snapshot (see §8)
);
CREATE INDEX idx_check_runs_ws ON check_runs(workspace_id, id);

CREATE TABLE check_results (
  run_id TEXT NOT NULL REFERENCES check_runs(id) ON DELETE CASCADE,
  seq INTEGER NOT NULL,
  check_code TEXT NOT NULL,                   -- CHK-001…CHK-005
  severity TEXT NOT NULL CHECK (severity IN ('error','warning','info')),
  passed INTEGER NOT NULL,                    -- 1 = check clean, 0 = finding
  entity_kind TEXT,                           -- 'workspace'|'device'|'interface'
  entity_id TEXT,
  message TEXT NOT NULL,                      -- English, deterministic
  detail_json TEXT NOT NULL,                  -- typed per-check detail
  PRIMARY KEY (run_id, seq)
);

CREATE TABLE checkpoints (
  id TEXT PRIMARY KEY,                        -- 'cp-<n>'
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  label TEXT NOT NULL,
  file_path TEXT NOT NULL,                    -- relative to the lab DB directory
  workspace_hash TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX idx_checkpoints_ws ON checkpoints(workspace_id, id);
`,
}

// init appends v5 to the ordered migration list (which migrations.go and
// migrations_v4.go own) and re-sorts by version, following the registration
// hook pattern established by migrations_v4.go.
func init() {
	migrations = append(migrations, migrationV5)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
}
