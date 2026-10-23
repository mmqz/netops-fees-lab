package repo

import "sort"

// migrationV33 adds the risk-clue decision table (schema version 33,
// #119): the landing spot of the risk clues' human-verification loop.
// A computed RiskClue has no row of its own — every risk run recomputes
// the clue set from the Parquet analysis layer — so until v33 a human
// investigation left NO trace: dismissed-as-noise clues reappeared on
// every run, and confirmed-real anomalies were indistinguishable from
// never-looked-at ones. This table records the decision ONLY, keyed by
// the clue's identity (scenario + fee type + site code + period).
//
// Two states, both human-only (the MCP face gains no write tool — the
// same governance that keeps attribute-change apply and link confirm
// behind HTTP/CLI):
//
//	verified  — the human CONFIRMED the clue is a real anomaly; the
//	             clue stays visible (marked) as the known-issues ledger
//	dismissed — the human ruled the clue out; the default risk output
//	             suppresses it and counts it in the envelope (the #69
//	             PairDecided doctrine: a decided clue stays decided; the
//	             count is reported, never silently dropped)
//
// UNIQUE (scenario, fee_type, site_code, year, month) — one decision per
// clue identity; re-deciding flips the status and refreshes decided_at
// (UPSERT: the human may change their mind as evidence evolves; the row
// keeps the LATEST decision, the note column carries the reason).
//
// No foreign keys by design: site codes come from the fee files (a code
// space fact, not a resources row — the same ruling as fee_site_links'
// fee-side); scenario/fee_type are closed enums guarded by CHECK; the
// row is pure audit — batch rollback never touches it (the W005/W006
// doctrine: applied/dismissed human decisions survive as the audit
// trail). month keeps the 0..12 range because unlinked clues may carry
// the whole-year scope's month 0.
//
// The migration is a pure CREATE TABLE (+ indexes), modeled on v30: no
// rebuild, no data moves, foreign_keys enforcement stays ON.
var migrationV33 = Migration{
	Version: 33,
	Name:    "risk_clue_decisions",
	SQL: `
CREATE TABLE risk_clue_decisions (
  id TEXT PRIMARY KEY,                          -- rcd_<uuid>
  scenario TEXT NOT NULL CHECK (scenario IN ('retired-billing','duplicate-settlement','ledger-mismatch','spike','outlier','unlinked','idle-billing','missing-billing')),
  fee_type TEXT NOT NULL CHECK (fee_type IN ('electricity','tower_lease','repair','maintenance')),
  site_code TEXT NOT NULL,                      -- 费用侧站址编码（代码空间事实，无 FK——同 v30 裁决）
  year INTEGER NOT NULL CHECK (year >= 2000 AND year <= 2100),
  month INTEGER NOT NULL CHECK (month >= 0 AND month <= 12),
  status TEXT NOT NULL CHECK (status IN ('verified','dismissed')),
  note TEXT NOT NULL DEFAULT '',                -- 人工裁定理由（可选）
  decided_at TEXT NOT NULL,                     -- RFC3339 UTC
  UNIQUE (scenario, fee_type, site_code, year, month)
);
CREATE INDEX idx_risk_clue_decisions_status ON risk_clue_decisions(status);
`,
}

// init appends v33 to the ordered migration list (which migrations.go …
// migrations_v32.go own) and re-sorts by version — the registration hook
// pattern every migration file since v4 follows.
func init() {
	migrations = append(migrations, migrationV33)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
}
