package repo

import "sort"

// migrationV37 adds the configurable risk-rule tables (schema version 37,
// issue #2 phase 1): the L1 rule set — {metric, aggregation, dimension,
// comparator, threshold, window} six-tuples that an AI can WRITE (create
// only) and a human can enable/tune in the open. The doctrine is the
// alert-rules precedent: "Rules are CONFIG DATA, not code" — a new
// judgment no longer needs a Go release.
//
//	fee_risk_rules      the rule rows; enabled=0 by default (the
//	                    conservative observation state — a fresh rule
//	                    fires nothing until a human enables it)
//	fee_risk_rule_audit the action ledger (created/enabled/disabled),
//	                    actor + JSON changes — 阈值变更留痕 red line
//
// Red lines carried by the CHECK enums (the engine re-validates before
// building SQL): the comparator/metric/aggregation/dimension sets are
// CLOSED, thresholds are numeric — a rule's text NEVER reaches SQL
// (枚举白名单 + 参数绑定，规则内容绝不拼进 SQL).
var migrationV37 = Migration{
	Version: 37,
	Name:    "fee risk rules (L1 configurable)",
	SQL: `
CREATE TABLE fee_risk_rules (
  id TEXT PRIMARY KEY,                          -- rule_<uuid>
  name TEXT NOT NULL UNIQUE,                    -- 人类可读名（线索溯源展示）
  fee_type TEXT NOT NULL CHECK (fee_type IN ('electricity','tower_lease','repair','maintenance')),
  metric TEXT NOT NULL CHECK (metric IN ('amount','rows')),
  aggregation TEXT NOT NULL CHECK (aggregation IN ('sum','avg','max','count')),
  dimension TEXT NOT NULL CHECK (dimension IN ('site_month')),
  comparator TEXT NOT NULL CHECK (comparator IN ('gt','gte','lt','lte','mom_ratio')),
  threshold REAL NOT NULL CHECK (threshold >= 0),
  window_months INTEGER NOT NULL DEFAULT 1 CHECK (window_months BETWEEN 1 AND 12),
  severity TEXT NOT NULL CHECK (severity IN ('high','medium','low')),
  description TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0,1)),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX idx_fee_risk_rules_enabled ON fee_risk_rules(enabled, fee_type);

CREATE TABLE fee_risk_rule_audit (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  rule_id TEXT NOT NULL,
  action TEXT NOT NULL CHECK (action IN ('created','enabled','disabled')),
  actor TEXT NOT NULL DEFAULT '',
  changes TEXT NOT NULL DEFAULT '',             -- JSON：{"field":{"from":x,"to":y}}
  at TEXT NOT NULL
);
CREATE INDEX idx_fee_risk_rule_audit_rule ON fee_risk_rule_audit(rule_id, at);
`,
}

// init appends v37 to the ordered migration list and re-sorts by version,
// following the registration hook pattern established by migrations_v4.go.
func init() {
	migrations = append(migrations, migrationV37)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
}
