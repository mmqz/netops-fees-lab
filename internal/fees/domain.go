// Package fees implements the fee-data analysis layer of NetOps Lab
// (#58/#60/#61, the stage-1/2 vertical slice of the fee risk-analysis
// module): the immutable snapshot layer, the normalized Parquet partition
// table and the SQLite state layer of fee imports.
//
// Architecture (the two-layer ruling of the 2026-10-18 discussion):
//
//	状态层 SQLite —— fee_batches 状态机、幂等键、回滚记账（migration v29）
//	快照层 文件系统 —— 内容寻址的原始文件快照（证据链，永不修改）
//	分析层 Parquet —— fee_type/year/month hive 分区大表（DuckDB 查询引擎）
//
// The write path is PURE GO (parquet-go): CSV/Excel → normalize →
// partitioned Parquet, no CGO, runs on every platform the main binary
// builds on. The QUERY engine is DuckDB (go-duckdb, CGO) behind the
// `duckdb` build tag — default builds answer ErrEngineNotBuilt (see
// engine.go); release builds for linux (docker) compile with -tags
// duckdb. The 2026-10-18 validation (#59) measured this path: hive
// partition pruning ×2.4, ad-hoc joins ×2.5–×53 vs SQLite.
//
// The shared record types (FeeType / FeeStatus / FeeBatch) live in
// internal/domain — repo needs them and must never import a service
// package; this file re-exports them as aliases so the fee world has one
// import surface.
package fees

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// The shared domain types (see internal/domain/fees.go).
type (
	// FeeType is the closed enum of the fee domains (hive partition
	// values).
	FeeType = domain.FeeType
	// FeeStatus is the fee-batch state machine.
	FeeStatus = domain.FeeStatus
	// FeeBatch is one fee import's state-layer record.
	FeeBatch = domain.FeeBatch
)

// The fee type values (aliases of the domain constants).
const (
	FeeTypeElectricity = domain.FeeTypeElectricity
	FeeTypeTowerLease  = domain.FeeTypeTowerLease
	FeeTypeRepair      = domain.FeeTypeRepair
	FeeTypeMaintenance = domain.FeeTypeMaintenance

	FeeStatusCompleted  = domain.FeeStatusCompleted
	FeeStatusFailed     = domain.FeeStatusFailed
	FeeStatusRolledBack = domain.FeeStatusRolledBack
)

// ParseFeeType validates a raw token against the closed enum.
func ParseFeeType(raw string) (FeeType, error) { return domain.ParseFeeType(raw) }

// FeeTypes lists every fee type value (CLI usage text).
func FeeTypes() []string { return domain.FeeTypes() }

// FeeTypeLabel is the Chinese display name of a fee type.
func FeeTypeLabel(t FeeType) string { return domain.FeeTypeLabel(t) }

// ErrDuplicateImport reports the idempotency skip: a completed batch with
// the same (sha256, fee_type, year, month, mapping) key already exists.
// The error carries the existing batch id — the CLI surfaces it as
// informational (exit 0 with the summary), never as a failure.
type ErrDuplicateImport struct {
	BatchID string
}

func (e *ErrDuplicateImport) Error() string {
	return fmt.Sprintf("fees: 同一文件（同哈希/类型/账期/映射）已于批次 %s 完成导入——零新数据（幂等跳过）", e.BatchID)
}

// ErrImportRefused marks the CLIENT-side refusals of the import channel
// (#104): bad file shape, unreadable format, missing mapping — problems the
// operator fixes in the FILE, not server faults. The HTTP face answers these
// with 422 and the message text; everything else (snapshot IO, DB, Parquet)
// is a server fault the transport classifies as 5xx with the detail logged,
// never shipped.
var ErrImportRefused = errors.New("fees: 导入被拒绝")

// refusef builds one ErrImportRefused-wrapped refusal (the message is the
// honest, operator-facing text — headers and mapping names, no filesystem
// paths beyond the caller's own file name).
func refusef(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrImportRefused, fmt.Sprintf(format, args...))
}

// ErrEngineNotBuilt reports that the DuckDB query engine is not compiled
// into this binary (release builds use -tags duckdb; CGO required). The
// import/write path is unaffected — only queries answer this.
var ErrEngineNotBuilt = errors.New("fees: 分析查询引擎未编译进本二进制（需要 -tags duckdb 与 CGO 构建；导入/快照/Parquet 写入不受影响）。Windows 原生构建暂不可用（go-duckdb 链接的 rstrtmgr 库 mingw-w64 未提供）——请用 Docker 镜像（docker build -t netops-lab:local . ，已含 duckdb 引擎）或 WSL2 内 CGO_ENABLED=1 go build -tags duckdb 获得 Linux 查询构建；详见 docs/fees-layer.md §5")

// FeeMapping is the confirmed column mapping of one fee file: target
// field → source header. siteCode and amount are mandatory; the period
// can come from the period column, the date column, or the declared
// year+month.
type FeeMapping struct {
	FeeType FeeType           `json:"feeType"`
	Columns map[string]string `json:"columns"`
}

// CanonicalMapping renders the mapping with stable key order (the
// idempotency key's mapping component — a reordered JSON must not produce
// a second identity).
func (m FeeMapping) CanonicalMapping() string {
	keys := make([]string, 0, len(m.Columns))
	for k := range m.Columns {
		keys = append(keys, k)
	}
	// insertion sort: tiny N, no extra import surface
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	var b strings.Builder
	b.WriteString("{\"feeType\":\"")
	b.WriteString(string(m.FeeType))
	b.WriteString("\",\"columns\":{")
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`"` + k + `":"` + m.Columns[k] + `"`)
	}
	b.WriteString("}}")
	return b.String()
}

// NormalizedRow is one analysis-layer row: the identity key parts of every
// risk scenario (site × period × amount) plus the row-level evidence
// chain. AmountCents is the money value as INTEGER CENTS (#65 P1-a) — the
// reconciliation-grade contract: parse, storage, aggregation and JSON all
// carry int64 cents, never a float. Year/month are ALSO the hive partition
// values (path, not file columns). Extra source columns stay in the
// snapshot — the evidence principle: the analysis table carries the
// standard core, the snapshot carries the full original row.
type NormalizedRow struct {
	SiteCode    string
	AmountCents int64
	Year        int
	Month       int
	SourceFile  string
	SourceRow   int
	ImportBatch string
}

// ImportError is a row-level normalization failure (bad amount, unparseable
// period): the row is skipped and counted, never silently mangled.
type ImportError struct {
	Row    int
	Field  string
	Raw    string
	Reason string
}

func (e *ImportError) Error() string {
	return fmt.Sprintf("行 %d 字段 %s 值 %q 无法规范化：%s", e.Row, e.Field, e.Raw, e.Reason)
}
