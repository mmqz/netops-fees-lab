package repo

import "sort"

// migrationV39 adds the site-code alias table (schema version 39, issue
// #12): the bridge between the two systems' site-code universes — the
// electricity ledger and the tower settlement detail name the SAME
// physical site differently (实测 3 个站「同名不同码」), and the
// cross-dataset reconcile needs the canonical↔alias bridge to see it.
//
//	fee_site_code_aliases  append-only mapping rows: canonical (主编码)
//	                       + alias (另一系统的编码或站名) + source
//	                       (哪个费用域的宇宙) + confidence (exact/rule/
//	                       manual — the graded doctrine of fee_site_links)
//	                       + created_by/created_at (issue acceptance 5:
//	                       映射关系可审计——谁在何时建立).
//
// No update channel (the rules precedent): a wrong alias is REMOVED
// (admin tier) and re-created — the removal itself stays out of the
// table but the operator's re-creation carries the fresh provenance.
var migrationV39 = Migration{
	Version: 39,
	Name:    "site code aliases (#12 cross-dataset reconcile bridge)",
	SQL: `
CREATE TABLE fee_site_code_aliases (
  id TEXT PRIMARY KEY,                          -- fca_<uuid>
  canonical TEXT NOT NULL,                     -- 主编码（对账归并的目标键）
  alias TEXT NOT NULL,                         -- 别名（另一系统的编码或站名）
  source TEXT NOT NULL CHECK (source IN ('electricity','tower_lease','repair','maintenance','manual')),
  confidence TEXT NOT NULL CHECK (confidence IN ('exact','rule','manual')),
  created_by TEXT NOT NULL,                    -- 审计：谁建立（acceptance 5）
  created_at TEXT NOT NULL,                    -- 审计：何时建立
  UNIQUE (canonical, alias, source)
);
CREATE INDEX idx_fee_site_code_aliases_canonical ON fee_site_code_aliases(canonical);
CREATE INDEX idx_fee_site_code_aliases_alias ON fee_site_code_aliases(alias);
`,
}

func init() {
	migrations = append(migrations, migrationV39)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
}
