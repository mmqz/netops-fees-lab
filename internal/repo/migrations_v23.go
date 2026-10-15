package repo

// migrationV23 creates the layer overlay tables (schema version 23, issue
// #51 / W33): virtual_nodes + virtual_layer_links — the read-only canvas
// projection of the unified resource graph, scoped to a workspace's
// lineage batches.
//
//   - layer is CHECKed against the overlay vocabulary (L0/L4/L5/L6 for
//     nodes — L3 entities are EDGES — and L3/L4/L5/L6 for links). L1/L2
//     are the drill entities themselves and never appear here.
//   - anchor_device_id is the cluster/layout anchor (uplink device of a
//     base station, station device of an OLT, depended device of a
//     service); ON DELETE SET NULL keeps the node when a drill device is
//     deleted — the overlay is presentation, never a drill dependency.
//   - sem is CHECKed against logical|service|access|group; endpoint kinds
//     against device|interface|node.
//   - resource_id lineage: ON DELETE SET NULL for nodes (rows survive a
//     resource purge as presentation ghosts), NOT NULL for links' lineages
//     is NOT enforced — a derived edge (aggregate uplink) may have none.
//
// The overlay is deliberately NOT part of the workspace state hash and
// never mutated by ops — see internal/domain/layers.go for the boundary.
//
// Purely additive: two CREATE TABLE + three CREATE INDEX, no rebuild —
// the migrations_v19/v21 style. A re-run is a no-op (the runner's version
// bookkeeping guards it).
var migrationV23 = Migration{
	Version: 23,
	Name:    "w33_layer_overlay_nodes_links",
	SQL: `
-- The overlay entities need two new per-kind sequences (vn/vl); the v5
-- sequences table CHECK is widened by the classic _new/rename rebuild
-- (the migrations_v10/v13/v15 pattern; the table holds one row per kind,
-- so the copy is trivial).
CREATE TABLE sequences_v23 (
  kind TEXT NOT NULL CHECK (kind IN ('vd','if','ln','cs','run','cp','vn','vl')),
  last_value INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (kind)
);
INSERT INTO sequences_v23 (kind, last_value) SELECT kind, last_value FROM sequences;
DROP TABLE sequences;
ALTER TABLE sequences_v23 RENAME TO sequences;
CREATE TABLE virtual_nodes (
  id                TEXT PRIMARY KEY,
  workspace_id      TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  resource_id       TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
  layer             TEXT NOT NULL CHECK (layer IN ('L0','L4','L5','L6')),
  kind              TEXT NOT NULL,
  code              TEXT NOT NULL,
  name              TEXT NOT NULL DEFAULT '',
  status            TEXT NOT NULL DEFAULT '',
  anchor_device_id  TEXT REFERENCES virtual_devices(id) ON DELETE SET NULL,
  cluster_key       TEXT NOT NULL DEFAULT '',
  geo_lat           REAL,
  geo_lng           REAL,
  detail_json       TEXT NOT NULL DEFAULT '{}',
  created_at        TEXT NOT NULL,
  updated_at        TEXT NOT NULL,
  UNIQUE (workspace_id, code)
);
CREATE INDEX idx_virtual_nodes_ws ON virtual_nodes (workspace_id, layer, kind);
CREATE TABLE virtual_layer_links (
  id            TEXT PRIMARY KEY,
  workspace_id  TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  layer         TEXT NOT NULL CHECK (layer IN ('L3','L4','L5','L6')),
  sem           TEXT NOT NULL CHECK (sem IN ('logical','service','access','group')),
  label         TEXT NOT NULL DEFAULT '',
  resource_id   TEXT REFERENCES resources(id) ON DELETE SET NULL,
  a_kind        TEXT NOT NULL CHECK (a_kind IN ('device','interface','node')),
  a_ref         TEXT NOT NULL,
  z_kind        TEXT NOT NULL CHECK (z_kind IN ('device','interface','node')),
  z_ref         TEXT NOT NULL,
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);
CREATE INDEX idx_virtual_layer_links_ws ON virtual_layer_links (workspace_id, layer, sem);
CREATE INDEX idx_virtual_nodes_cluster ON virtual_nodes (workspace_id, cluster_key);
`,
}

func init() {
	migrations = append(migrations, migrationV23)
}
