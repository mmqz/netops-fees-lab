package repo

import "sort"

// migrationV34 adds the sheet coordinate to fee_batches and folds it into
// the idempotency key (schema version 34, #125): a real monthly ledger is
// ONE workbook with ONE fee type per sheet (电费/铁塔租费/修理费), so the
// multi-sheet import channel creates one batch per sheet — and two sheets
// of the SAME type in one file would collide on the old
// (sha256, fee_type, year, month, mapping) key even though they carry
// different rows. The sheet name (empty for CSV and for every batch
// recorded before this migration) becomes part of the key, making the
// batch's identity as granular as its data actually is.
//
// ALTER TABLE + DROP INDEX + CREATE INDEX only — no table rebuild, no data
// moves (the same non-rebuild shape as v31); every pre-existing completed
// batch keeps its key verbatim (sheet defaults to ”), so idempotency of
// historical single-sheet imports is preserved byte-for-byte.
var migrationV34 = Migration{
	Version: 34,
	Name:    "fee_batches sheet coordinate",
	SQL: `
ALTER TABLE fee_batches ADD COLUMN sheet TEXT NOT NULL DEFAULT '';
DROP INDEX idx_fee_batches_idem;
-- 幂等键：同一 (文件哈希, sheet, 费用类型, 账期, 映射) 至多一个 completed
-- 批次；failed/rolled_back 不占键（重试通道永远开放）。sheet 为空 =
-- CSV 或 v34 前的单 sheet 批次。
CREATE UNIQUE INDEX idx_fee_batches_idem
  ON fee_batches(sha256, sheet, fee_type, fee_year, fee_month, mapping)
  WHERE status = 'completed';
`,
}

// init appends v34 to the ordered migration list and re-sorts by version,
// following the registration hook pattern established by migrations_v4.go.
func init() {
	migrations = append(migrations, migrationV34)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
}
