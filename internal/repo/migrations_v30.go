package repo

import "sort"

// migrationV30 adds the fee-site linkage table (schema version 30, #58
// stage 3 / issue #63): the graded association between fee analysis rows'
// site codes and the resource layer's site-like resources.
//
// The three grades (#63's ruling):
//
//	exact — fee code == the resource's natural key, byte-for-byte
//	rule   — equality under a NAMED, explainable normalization rule
//	         (recorded per link for audit)
//	fuzzy  — a similarity CANDIDATE only: status pending until a human
//	         confirms (the W005/W006 doctrine — identity links never
//	         merge automatically)
//
// Lifecycle: exact/rule links are created CONFIRMED with source=auto (the
// matching is deterministic and reversible); fuzzy links are created
// PENDING (proposals). A human confirms (pending → confirmed,
// source=human) or dismisses (pending → dismissed, audit kept). At most
// ONE confirmed link per fee_site_code (partial unique index) — one fee
// site code designates one physical site; a resource may carry many fee
// codes (different code systems), which the resource-side index serves.
//
// resource_id ON DELETE CASCADE: the resource vanishing (rollback of its
// import batch) removes the link with it — the audited entity is gone.
// fee-side there is no FK by design: the fee codes come from the Parquet
// layer, not a SQLite row; a fee batch rollback leaves the code string
// untouched (the linkage is a code-space fact, not a batch fact).
var migrationV30 = Migration{
	Version: 30,
	Name:    "fee_site_links",
	SQL: `
CREATE TABLE fee_site_links (
  id TEXT PRIMARY KEY,                          -- fsl_<uuid>
  fee_site_code TEXT NOT NULL,                  -- 费用侧站址编码（代码空间事实，无费用侧 FK）
  resource_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
  resource_type TEXT NOT NULL,
  resource_key TEXT NOT NULL,                   -- 建链时命中的资源键（审计）
  grade TEXT NOT NULL CHECK (grade IN ('exact','rule','fuzzy')),
  rule TEXT,                                    -- rule 级所用规则名；其它级 NULL
  similarity REAL,                              -- fuzzy 级相似度（审计）；其它级 NULL
  status TEXT NOT NULL CHECK (status IN ('confirmed','pending','dismissed')),
  source TEXT NOT NULL CHECK (source IN ('auto','human')),
  batch_id TEXT,                                -- 产出本提案的费用批次（fuzzy 溯源；可为 NULL）
  created_at TEXT NOT NULL,
  decided_at TEXT
);
CREATE INDEX idx_fee_site_links_code ON fee_site_links(fee_site_code);
CREATE INDEX idx_fee_site_links_resource ON fee_site_links(resource_id);
CREATE INDEX idx_fee_site_links_status ON fee_site_links(status);
-- 一个费用站址编码至多一条 confirmed 关联（部分唯一：pending/dismissed 不占位）
CREATE UNIQUE INDEX idx_fee_site_links_live ON fee_site_links(fee_site_code) WHERE status = 'confirmed';
`,
}

// init appends v30 to the ordered migration list and re-sorts by version.
func init() {
	migrations = append(migrations, migrationV30)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
}
