package repo

import "sort"

// migrationV38 adds the fee detail layer (schema version 38, #16's
// completion line item 6): the manual-entry evidence tier that explains
// WHERE a system-table amount went — never replacing it. The system table
// (wage/ledger imports) stays the authority on amounts; details are
// corroboration, and the delta between the two tiers is a HINT, never a
// block (差额只提示，不阻断).
//
//	fee_detail_lines  one manually entered detail row; the three
//	                  #16 mandates are NOT NULL here: the link key
//	                  (whose money — site code or staff code), the
//	                  amount (integer cents, negative = write-off)
//	                  and the provenance (created_by/created_at —
//	                  人工数据必须有来源痕迹).
//
// Categories are a SUGGESTION SET, not a closed enum — #16 over the v1
// spec: "系统提供「分类建议」，但允许本地自建分类". The backend only
// requires non-empty; the UI offers the twelve defaults from
// docs/fee-detail-schema.md §3.2 as suggestions. Status flow is the
// three-state human channel (pending → confirmed/rejected) with the
// audit trail columns; the system never flips a status silently.
var migrationV38 = Migration{
	Version: 38,
	Name:    "fee detail layer (manual entry evidence tier)",
	SQL: `
CREATE TABLE fee_detail_lines (
  id TEXT PRIMARY KEY,                           -- fd_<uuid>
  link_key TEXT NOT NULL,                        -- 关联键：站址编码或工号（这笔钱属于谁）
  period TEXT NOT NULL,                         -- YYYYMM（哪个月）
  fee_type TEXT NOT NULL CHECK (fee_type IN ('electricity','tower_lease','repair','maintenance')),
  category TEXT NOT NULL,                       -- 建议集 + 本地自建（#16：不强制闭集）
  amount_cents INTEGER NOT NULL,                 -- 整数分契约（#65 P1-a）；负数 = 冲销
  occurred_on TEXT,                             -- YYYY-MM-DD，可空
  item_name TEXT,                               -- 原始项目名（照抄原始表）
  quantity REAL,
  quantity_unit TEXT,
  unit_price_cents INTEGER,
  reason TEXT,
  location TEXT,
  work_order_no TEXT,
  invoice_no TEXT,
  counterparty TEXT,
  source_file TEXT,                             -- 来源文件名（追溯）
  source_row INTEGER,                           -- 原表行号（追溯）
  remark TEXT,
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','confirmed','rejected','unmatched')),
  created_by TEXT NOT NULL,                     -- 录入留痕（#16 三强制之一）
  created_at TEXT NOT NULL,
  verified_by TEXT,
  verified_at TEXT,
  reject_reason TEXT
);
CREATE INDEX idx_fee_detail_lines_link ON fee_detail_lines(link_key, period, fee_type);
CREATE INDEX idx_fee_detail_lines_status ON fee_detail_lines(status);
`,
}

// init appends v38 to the ordered migration list (the registration hook
// pattern every migration file carries).
func init() {
	migrations = append(migrations, migrationV38)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
}
