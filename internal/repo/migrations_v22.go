package repo

// migrationV22 adds the hot-path read index the R14 audit found missing
// (schema version 22, v0.11-R14): idx_alerts_state_fired (state, fired_at).
//
// The alerts page's default queue (WHERE a.state = 'firing' ORDER BY
// a.fired_at DESC), the lifecycle filters and the v0.11 stats GROUP BY
// state all filter state against the v18 (rule_id, series_id, state)
// index — state is its THIRD column, so a state-only filter cannot use
// it; the listing walked a full scan of the joined set. state first
// matches the filter, fired_at second rides the ORDER BY.
//
// The audit's OTHER candidate — an (series_id, ts) index on metrics —
// turned out to be already covered: the v14 table is WITHOUT ROWID with
// PRIMARY KEY (series_id, ts), and EXPLAIN QUERY PLAN shows the
// series-scoped window read already SEARCHes the primary key. Adding a
// redundant index would only tax every ingest write; it is deliberately
// NOT created (the finding is pinned in the migration test).
//
// Purely additive: one CREATE INDEX, no rebuild — the migrations_v19/v21
// style. EXPLAIN QUERY PLAN pins the usage in migrations_v22_test.
var migrationV22 = Migration{
	Version: 22,
	Name:    "r14_alerts_state_fired_index",
	SQL: `
CREATE INDEX idx_alerts_state_fired ON alerts(state, fired_at);
`,
}

func init() {
	migrations = append(migrations, migrationV22)
}
