package app

// The CLI side of the fee analysis layer (#58/#60/#61): import (snapshot +
// Parquet + batch), list, rollback and the two query presets. All outputs
// follow the single-JSON-object stdout contract; usage errors exit 2; the
// idempotent duplicate import is INFORMATIONAL (exit 0, the summary
// carries skippedDuplicate), never a failure — the same severity doctrine
// as the resource importer's duplicate skip.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/fees"
	"github.com/mmqz/netops-fees-lab/internal/repo"
)

// feesSubcommands lists the fees dispatcher's cases; both usage surfaces
// (missing and unknown subcommand) derive from it. #108 found both strings
// stale — `evidence` and `risks` were missing from one and both
// respectively — so the list is now joined, never hand-copied, and the
// closed-set test (usage_closset_test.go) pins it against the switch via
// go/ast, in both directions.
var feesSubcommands = []string{
	"import", "import-sheets", "period-probe", "list", "rollback", "evidence", "query", "link-scan", "links", "risks", "rules", "clue-decisions", "details", "aliases", "cross-reconcile", "verify-sources", "verify-batches",
}

// splitFeeArgs parses the leading --db / --data flags of the fees command
// and splits off the sub-command (splitSubArgs's sibling — the fee
// pipeline is rooted at a data dir, not only a database).
func splitFeeArgs(args []string) (dbPath, dataDir, sub string, subArgs []string, err error) {
	dbPath = "./data/netops.db"
	dataDir = "./data"
	i := 0
	for i < len(args) {
		arg := args[i]
		switch {
		case arg == "--db" || arg == "-db":
			if i+1 >= len(args) {
				return "", "", "", nil, &usageError{msg: "--db 需要一个路径参数"}
			}
			dbPath = args[i+1]
			i += 2
		case strings.HasPrefix(arg, "--db="):
			dbPath = strings.TrimPrefix(arg, "--db=")
			i++
		case arg == "--data" || arg == "-data":
			if i+1 >= len(args) {
				return "", "", "", nil, &usageError{msg: "--data 需要一个目录参数"}
			}
			dataDir = args[i+1]
			i += 2
		case strings.HasPrefix(arg, "--data="):
			dataDir = strings.TrimPrefix(arg, "--data=")
			i++
		case strings.HasPrefix(arg, "-"):
			return "", "", "", nil, &usageError{msg: fmt.Sprintf("fees 不支持参数 %q（仅 --db / --data）", arg)}
		default:
			return dbPath, dataDir, arg, args[i+1:], nil
		}
	}
	return "", "", "", nil, &usageError{msg: "缺少 fees 子命令（" + strings.Join(feesSubcommands, " / ") + "）"}
}

// cmdFees implements `netopsfees fees --db <path> --data <dir> import|list|rollback|query …`.
func cmdFees(args []string, stdout, stderr io.Writer) error {
	dbPath, dataDir, sub, subArgs, err := splitFeeArgs(args)
	if err != nil {
		return err
	}
	a, err := openApp(dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = a.Close() }()
	svc := fees.NewService(a.DB, dataDir)
	ctx := context.Background()

	switch sub {
	case "import":
		return cmdFeesImport(ctx, svc, subArgs, stdout, stderr)
	case "import-sheets":
		return cmdFeesImportSheets(ctx, svc, subArgs, stdout, stderr)
	case "period-probe":
		return cmdFeesPeriodProbe(subArgs, stdout, stderr)
	case "list":
		return cmdFeesList(ctx, svc, subArgs, stdout, stderr)
	case "rollback":
		return cmdFeesRollback(ctx, svc, subArgs, stdout, stderr)
	case "evidence":
		return cmdFeesEvidence(ctx, svc, subArgs, stdout, stderr)
	case "query":
		return cmdFeesQuery(ctx, svc, dataDir, subArgs, stdout, stderr)
	case "link-scan":
		return cmdFeesLinkScan(ctx, svc, subArgs, stdout, stderr)
	case "links":
		return cmdFeesLinks(ctx, svc, subArgs, stdout, stderr)
	case "risks":
		return cmdFeesRisks(ctx, svc, dataDir, subArgs, stdout, stderr)
	case "rules":
		return cmdFeesRules(ctx, svc, subArgs, stdout, stderr)
	case "clue-decisions":
		return cmdFeesClueDecisions(ctx, svc, subArgs, stdout, stderr)
	case "details":
		return cmdFeesDetails(ctx, svc, subArgs, stdout, stderr)
	case "aliases":
		return cmdFeesAliases(ctx, svc, dataDir, subArgs, stdout, stderr)
	case "cross-reconcile":
		return cmdFeesCrossReconcile(ctx, svc, subArgs, stdout, stderr)
	case "verify-sources":
		return cmdFeesVerifySources(ctx, svc, subArgs, stdout, stderr)
	case "verify-batches":
		return cmdFeesVerifyBatches(ctx, svc, subArgs, stdout, stderr)
	default:
		return &usageError{msg: fmt.Sprintf("fees 子命令必须是 %s，收到 %q", commandEnum(feesSubcommands), sub)}
	}
}

// cmdFeesRisks implements `fees risks <scenario>` — the risk-scenario
// presets of #64 (#58 stage 4) plus #114's scenario 7: retired-billing /
// duplicate-settlement / ledger-mismatch / spike / outlier / unlinked /
// idle-billing. Every threshold flag maps to a RiskQuery field with a
// documented default; the output is the single-JSON RiskResult envelope
// (clues carry row-level evidence anchors pointing back into the
// snapshot layer).
func cmdFeesRisks(ctx context.Context, svc *fees.Service, dataDir string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &usageError{msg: "fees risks 需要场景参数：" + strings.Join(fees.RiskScenarios(), " / ")}
	}
	scenario, rest := args[0], args[1:]
	fs, usage := newFlagSet("fees risks " + scenario)
	feeType := fs.String("type", "", "费用类型："+strings.Join(fees.FeeTypes(), " / ")+"，必填")
	year := fs.Int("year", 0, "年份（必填）")
	month := fs.Int("month", 0, "月份（可选：缺省整年各月）")
	status := fs.String("status", fees.DefaultRetiredStatuses, "retired-billing：视为退用/停用的资源状态（逗号分隔；默认 停用,退网）")
	diffCents := fs.Int64("diff-cents", 0, "ledger-mismatch：各批次合计差额阈值（分；默认 0 = 任何差异）")
	ratio := fs.Float64("ratio", fees.DefaultSpikeRatio, "spike：环比涨幅阈值（默认 0.30 = 30%）")
	z := fs.Float64("z", fees.DefaultOutlierZ, "outlier：|z-score| 阈值（默认 3.0）")
	minSites := fs.Int("min-sites", fees.DefaultMinSites, "outlier：同期最小同类站数（默认 8，不足则该期不出线索）")
	limit := fs.Int("limit", 100, "线索条数上限（默认 100，最大 1000）")
	maxEvidence := fs.Int("max-evidence", fees.DefaultMaxEvidence, "每条线索的证据锚点上限（默认 10，最大 50）")
	includeDecided := fs.Bool("include-decided", false, "含已驳回线索（审计视图；缺省隐藏已驳回、信封 dismissed 计数）")
	if err := fs.Parse(rest); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *feeType == "" || *year == 0 {
		return &usageError{msg: "fees risks 需要 --type 与 --year", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "fees risks 不接受额外位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	sc, err := fees.ParseRiskScenario(scenario)
	if err != nil {
		return &usageError{msg: err.Error(), usage: usage.String()}
	}
	t, err := fees.ParseFeeType(*feeType)
	if err != nil {
		return &usageError{msg: err.Error(), usage: usage.String()}
	}
	engine := fees.Query(ctx)
	if !engine.Available() {
		return fmt.Errorf("fees: 本二进制未编译分析查询引擎（%w）——发布构建使用 -tags duckdb（CGO）", fees.ErrEngineNotBuilt)
	}
	result, err := svc.Risks(ctx, engine, sc, fees.RiskQuery{
		FeeType: t, Year: *year, Month: *month,
		Statuses: fees.SplitComma(*status), DiffCents: *diffCents,
		Ratio: *ratio, ZScore: *z, MinSites: *minSites,
		Limit: *limit, MaxEvidence: *maxEvidence,
		IncludeDecided: *includeDecided,
	})
	if err != nil {
		return err
	}
	return writeJSON(stdout, result)
}

// cmdFeesImport implements `fees import --file --type --year --month [--map <json>]`.
func cmdFeesImport(ctx context.Context, svc *fees.Service, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("fees import")
	// #92/#108: .xls is REFUSED (Excel 2003 binary; the reader and the
	// snapshot layer both reject it with a dedicated message) — the help
	// must not claim it. Accepted: .csv / .xlsx only.
	file := fs.String("file", "", "费用文件路径（.csv / .xlsx；.xls 为 Excel 2003 二进制旧格式不受支持，可用 netopsfees convert 批量保真转换，issue #7），必填")
	strictRows := fs.Bool("strict-rows", false, "严格行校验（#8，默认关闭）：台账可选列（读数/用量/单价/分摊）中无法解析为数字的非空单元格使该行导入失败并列明原因；关闭时按宽松口径导入（坏值降级为空，行不拦），确定性规则仍可经 risks ledger-consistency 随时核查")
	trustPeriod := fs.Bool("period-trust-declared", false, "人工确认账期（#15，默认关闭）：文件内容识别的账期与 --year/--month 申报不一致时默认拒绝导入；加本标志则采用申报值继续导入，不一致事实留痕于批次（period_source=unverified）")
	allowPeriodDup := fs.Bool("allow-period-duplicate", false, "跨账期重复覆盖（#20，默认关闭）：同一文件（SHA256 相同）已按另一账期导入过时默认拒绝——本标志显式确认按本次申报账期再导入，覆盖事实留痕于批次 period_basis 与导入 warnings")
	feeType := fs.String("type", "", "费用类型："+strings.Join(fees.FeeTypes(), " / ")+"，必填")
	year := fs.Int("year", 0, "文件声明的账期年份（2000–2100），必填")
	month := fs.Int("month", 0, "文件声明的账期月份（1–12），必填")
	mapFile := fs.String("map", "", "列映射 JSON 文件路径（可选：{\"columns\":{\"siteCode\":\"站址编码\",\"amount\":\"电费(元)\",\"period\":\"账期\"}}；缺省自动识别）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *file == "" || *feeType == "" || *year == 0 || *month == 0 {
		return &usageError{msg: "fees import 需要 --file、--type、--year、--month", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "fees import 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	t, err := fees.ParseFeeType(*feeType)
	if err != nil {
		return &usageError{msg: err.Error(), usage: usage.String()}
	}
	var mapping map[string]string
	if *mapFile != "" {
		raw, err := os.ReadFile(*mapFile)
		if err != nil {
			return fmt.Errorf("读取映射文件 %s: %w", *mapFile, err)
		}
		var m struct {
			Columns map[string]string `json:"columns"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			return fmt.Errorf("映射文件 %s 不是合法 JSON: %w", *mapFile, err)
		}
		mapping = m.Columns
	}
	summary, err := svc.Import(ctx, fees.ImportOptions{
		Path: *file, FeeType: t, Year: *year, Month: *month, Mapping: mapping,
		StrictRows: *strictRows, TrustDeclaredPeriod: *trustPeriod,
		AllowPeriodDuplicate: *allowPeriodDup,
	})
	if err != nil {
		var dup *fees.ErrDuplicateImport
		if !errors.As(err, &dup) {
			return err
		}
		// the idempotent duplicate is informational: exit 0, the summary
		// carries skippedDuplicate + the existing batch id.
	}
	return writeJSON(stdout, summary)
}

// cmdFeesImportSheets implements `fees import-sheets` (#125): the
// multi-sheet channel — one workbook, one fee type per sheet, types
// recognized from each sheet's own evidence (name + header markers).
// No --type flag: the whole point is that the ledger's sheet names ARE
// the type declarations. Refusals (CSV / an unclassifiable sheet / a
// cross-channel re-import) exit non-zero with the honest text naming
// every offending sheet; the idempotent re-import is informational
// (exit 0, every batch entry skippedDuplicate).
func cmdFeesImportSheets(ctx context.Context, svc *fees.Service, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("fees import-sheets")
	file := fs.String("file", "", "多 sheet 台账工作簿路径（.xlsx；每 sheet 一种费用类型，类型按表名/表头自动判定），必填")
	strictRows := fs.Bool("strict-rows", false, "严格行校验（#8，默认关闭）：同 fees import")
	trustPeriod := fs.Bool("period-trust-declared", false, "人工确认账期（#15，默认关闭）：同 fees import——任一 sheet 的内容账期与申报不一致时默认拒绝整本导入，加本标志采用申报值继续并留痕")
	allowPeriodDup := fs.Bool("allow-period-duplicate", false, "跨账期重复覆盖（#20，默认关闭）：同 fees import——同一文件（SHA256 相同）已按另一账期导入过时默认拒绝，本标志显式确认并留痕")
	year := fs.Int("year", 0, "文件声明的账期年份（2000–2100），必填")
	month := fs.Int("month", 0, "文件声明的账期月份（1–12），必填")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *file == "" || *year == 0 || *month == 0 {
		return &usageError{msg: "fees import-sheets 需要 --file、--year、--month", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "fees import-sheets 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	summary, err := svc.ImportSheets(ctx, fees.SheetsImportOptions{
		Path: *file, OriginalName: filepath.Base(*file), Year: *year, Month: *month,
		StrictRows: *strictRows, TrustDeclaredPeriod: *trustPeriod,
		AllowPeriodDuplicate: *allowPeriodDup,
	})
	if err != nil {
		return err
	}
	return writeJSON(stdout, summary)
}

// cmdFeesPeriodProbe implements `fees period-probe --file` (#15): the
// read-only content probe — table-kind classification + three-tier
// period detection over the FIRST sheet (the single-sheet channel's
// data face), no declaration needed, nothing written. The JSON answer
// is the same PeriodDetection the import pipeline records; the UI's
// pre-import confirmation panel and the CLI operator share it.
func cmdFeesPeriodProbe(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("fees period-probe")
	file := fs.String("file", "", "费用文件路径（.csv / .xlsx），必填")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *file == "" {
		return &usageError{msg: "fees period-probe 需要 --file", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "fees period-probe 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	sheet, err := fees.ReadFeeFileFirst(*file)
	if err != nil {
		return err
	}
	det := fees.DetectPeriod(sheet.Headers, sheet.Rows)
	return writeJSON(stdout, det)
}

// cmdFeesList implements `fees list [--status]`.
func cmdFeesList(ctx context.Context, svc *fees.Service, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("fees list")
	status := fs.String("status", "", "状态过滤：completed / failed / rolled_back（缺省全部）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "fees list 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	var st domain.FeeStatus
	if *status != "" {
		switch domain.FeeStatus(*status) {
		case domain.FeeStatusCompleted, domain.FeeStatusFailed, domain.FeeStatusRolledBack:
			st = domain.FeeStatus(*status)
		default:
			return &usageError{msg: fmt.Sprintf("--status 必须是 completed/failed/rolled_back，收到 %q", *status), usage: usage.String()}
		}
	}
	batches, err := svc.List(ctx, st)
	if err != nil {
		return err
	}
	items := make([]feesListBatch, 0, len(batches))
	for _, b := range batches {
		items = append(items, feesListBatch{
			ID: b.ID, WorkbookName: b.WorkbookName, FeeType: string(b.FeeType),
			FeeTypeLabel: fees.FeeTypeLabel(b.FeeType), Period: fees.PeriodLabel(b.FeeYear, b.FeeMonth),
			Status: string(b.Status), RowsTotal: b.RowsTotal, RowsImported: b.RowsImported,
			RowsFailed: b.RowsFailed, SnapshotPath: b.SnapshotPath,
		})
	}
	return writeJSON(stdout, feesListResult{Items: items, Total: len(items)})
}

// feesListBatch is the CLI list item shape.
type feesListBatch struct {
	ID           string `json:"id"`
	WorkbookName string `json:"workbookName"`
	FeeType      string `json:"feeType"`
	FeeTypeLabel string `json:"feeTypeLabel"`
	Period       string `json:"period"`
	Status       string `json:"status"`
	RowsTotal    int    `json:"rowsTotal"`
	RowsImported int    `json:"rowsImported"`
	RowsFailed   int    `json:"rowsFailed"`
	SnapshotPath string `json:"snapshotPath"`
}

type feesListResult struct {
	Items []feesListBatch `json:"items"`
	Total int             `json:"total"`
}

// cmdFeesRollback implements `fees rollback --batch <id>`.
func cmdFeesRollback(ctx context.Context, svc *fees.Service, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("fees rollback")
	batch := fs.String("batch", "", "要回滚的 fee 批次 id，必填")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *batch == "" {
		return &usageError{msg: "fees rollback 需要 --batch", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "fees rollback 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	b, err := svc.Rollback(ctx, *batch)
	if err != nil {
		return err
	}
	return writeJSON(stdout, map[string]any{
		"id": b.ID, "status": string(b.Status), "period": fees.PeriodLabel(b.FeeYear, b.FeeMonth),
		"feeType": string(b.FeeType), "snapshotKept": b.SnapshotPath,
	})
}

// cmdFeesEvidence implements `fees evidence --batch <id> --row <n>`: the CLI
// twin of GET /api/fees/batches/{id}/evidence (#86) — resolves a clue's
// (sourceFile, sourceRow) anchor to the snapshot's original row.
func cmdFeesEvidence(ctx context.Context, svc *fees.Service, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("fees evidence")
	batch := fs.String("batch", "", "费用批次 id，必填")
	row := fs.Int("row", 0, "源文件行号（源文件自身编号，表头 = 1），必填")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *batch == "" || *row < 1 {
		return &usageError{msg: "fees evidence 需要 --batch 与 --row（行号 ≥ 1，表头 = 1）", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "fees evidence 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	ev, err := svc.EvidenceRow(ctx, *batch, *row)
	if err != nil {
		return err
	}
	return writeJSON(stdout, map[string]any{
		"batchId": ev.BatchID, "sourceFile": ev.SourceFile, "originalName": ev.OriginalName,
		"feeType": ev.FeeType, "year": ev.Year, "month": ev.Month, "row": ev.Row,
		"header": ev.Header, "cells": ev.Cells,
	})
}

// cmdFeesQuery implements `fees query totals|monthly|duplicates|rows --type --year [--month] [--limit]`.
func cmdFeesQuery(ctx context.Context, svc *fees.Service, dataDir string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &usageError{msg: "fees query 需要 totals、monthly、duplicates 或 rows 子命令"}
	}
	preset, rest := args[0], args[1:]
	fs, usage := newFlagSet("fees query " + preset)
	feeType := fs.String("type", "", "费用类型，必填")
	year := fs.Int("year", 0, "年份（必填）")
	month := fs.Int("month", 0, "月份（可选：缺省整年）")
	limit := fs.Int("limit", 20, "返回条数上限（默认 20；totals/duplicates/rows 均生效）")
	site := fs.String("site", "", "rows 预设的站址过滤（可选；缺省全部）")
	rk := fs.String("record-key", "", "rows 预设的记录键过滤（#4：订单号/电表编号/工单号；可选）")
	if err := fs.Parse(rest); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *feeType == "" || *year == 0 {
		return &usageError{msg: "fees query 需要 --type 与 --year", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "fees query 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	t, err := fees.ParseFeeType(*feeType)
	if err != nil {
		return &usageError{msg: err.Error(), usage: usage.String()}
	}
	if preset == "monthly" && *month > 0 {
		// parameter validation precedes the engine state — the same
		// doctrine as the HTTP layer's 400-before-501.
		return &usageError{msg: "fees query monthly 不接受 --month——序列覆盖全年逐月", usage: usage.String()}
	}
	engine := fees.Query(ctx)
	if !engine.Available() {
		return fmt.Errorf("fees: 本二进制未编译分析查询引擎（%w）——发布构建使用 -tags duckdb（CGO）", fees.ErrEngineNotBuilt)
	}
	analysis := filepath.Join(dataDir, "analysis")
	switch preset {
	case "totals":
		rows, err := engine.Totals(ctx, analysis, t, *year, *month, *limit)
		if err != nil {
			return err
		}
		return writeJSON(stdout, map[string]any{
			"preset": "totals", "feeType": string(t), "year": *year, "month": *month,
			"items": rows, "total": len(rows),
		})
	case "monthly":
		// #3: the whole-year monthly trend series (no --month, no --limit —
		// twelve rows are the honest ceiling; the --month refusal fires
		// before the engine check below).
		rows, err := engine.MonthlyTotals(ctx, analysis, t, *year)
		if err != nil {
			return err
		}
		return writeJSON(stdout, map[string]any{
			"preset": "monthly", "feeType": string(t), "year": *year,
			"items": rows, "total": len(rows),
		})
	case "rows":
		// #5: the optional ledger columns' query surface — the raw analysis
		// rows with meter/reading/quantity/price/share values.
		rows, err := engine.LedgerRows(ctx, analysis, t, *year, *month, *site, *rk, *limit)
		if err != nil {
			return err
		}
		return writeJSON(stdout, map[string]any{
			"preset": "rows", "feeType": string(t), "year": *year, "month": *month,
			"items": rows, "total": len(rows),
		})
	case "duplicates":
		rows, err := engine.Duplicates(ctx, analysis, t, *year, *month, *limit)
		if err != nil {
			return err
		}
		return writeJSON(stdout, map[string]any{
			"preset": "duplicates", "feeType": string(t), "year": *year, "month": *month,
			"items": rows, "total": len(rows),
		})
	default:
		return &usageError{msg: fmt.Sprintf("fees query 预设必须是 totals、monthly、duplicates 或 rows，收到 %q", preset)}
	}
}

// cmdFeesLinkScan implements `fees link-scan`: run the graded matcher
// (exact → rule → fuzzy proposals) over the live Parquet code universe.
func cmdFeesLinkScan(ctx context.Context, svc *fees.Service, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("fees link-scan")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "fees link-scan 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	result, err := svc.ScanLinks(ctx)
	if err != nil {
		return err
	}
	return writeJSON(stdout, result)
}

// cmdFeesLinks implements `fees links list|confirm|dismiss` — the human
// confirmation channel of the fuzzy proposals (auto exact/rule links are
// visible via list; their lifecycle is the scan's, not a decision's).
func cmdFeesLinks(ctx context.Context, svc *fees.Service, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &usageError{msg: "fees links 需要 list、confirm 或 dismiss 子命令"}
	}
	sub, subArgs := args[0], args[1:]
	switch sub {
	case "list":
		return cmdFeesLinksList(ctx, svc, subArgs, stdout, stderr)
	case "confirm":
		return cmdFeesLinksDecide(ctx, svc, "confirm", subArgs, stdout, stderr)
	case "dismiss":
		return cmdFeesLinksDecide(ctx, svc, "dismiss", subArgs, stdout, stderr)
	default:
		return &usageError{msg: fmt.Sprintf("fees links 子命令必须是 list、confirm 或 dismiss，收到 %q", sub)}
	}
}

// cmdFeesLinksList implements `fees links list [--status] [--grade] [--code]`.
func cmdFeesLinksList(ctx context.Context, svc *fees.Service, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("fees links list")
	status := fs.String("status", "", "状态过滤：confirmed / pending / dismissed（缺省全部）")
	grade := fs.String("grade", "", "级别过滤：exact / rule / fuzzy（缺省全部）")
	code := fs.String("code", "", "按费用站址编码过滤")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "fees links list 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	filter := repo.FeeLinkFilter{Code: *code}
	if *status != "" {
		switch domain.LinkStatus(*status) {
		case domain.LinkStatusConfirmed, domain.LinkStatusPending, domain.LinkStatusDismissed:
			filter.Status = domain.LinkStatus(*status)
		default:
			return &usageError{msg: fmt.Sprintf("--status 必须是 confirmed/pending/dismissed，收到 %q", *status), usage: usage.String()}
		}
	}
	if *grade != "" {
		switch domain.LinkGrade(*grade) {
		case domain.LinkGradeExact, domain.LinkGradeRule, domain.LinkGradeFuzzy:
			filter.Grade = domain.LinkGrade(*grade)
		default:
			return &usageError{msg: fmt.Sprintf("--grade 必须是 exact/rule/fuzzy，收到 %q", *grade), usage: usage.String()}
		}
	}
	links, err := svc.ListLinks(ctx, filter)
	if err != nil {
		return err
	}
	items := make([]feeLinkItem, 0, len(links))
	for _, l := range links {
		items = append(items, feeLinkItem{
			ID: l.ID, FeeSiteCode: l.FeeSiteCode, ResourceID: l.ResourceID,
			ResourceType: string(l.ResourceType), ResourceKey: l.ResourceKey,
			Grade: string(l.Grade), Rule: l.Rule, Similarity: l.Similarity,
			Status: string(l.Status), Source: string(l.Source), CreatedAt: l.CreatedAt,
			DecidedAt: l.DecidedAt,
		})
	}
	return writeJSON(stdout, feeLinksResult{Items: items, Total: len(items)})
}

// feeLinkItem is the CLI link item shape.
type feeLinkItem struct {
	ID           string     `json:"id"`
	FeeSiteCode  string     `json:"feeSiteCode"`
	ResourceID   string     `json:"resourceId"`
	ResourceType string     `json:"resourceType"`
	ResourceKey  string     `json:"resourceKey"`
	Grade        string     `json:"grade"`
	Rule         string     `json:"rule,omitempty"`
	Similarity   float64    `json:"similarity,omitempty"`
	Status       string     `json:"status"`
	Source       string     `json:"source"`
	CreatedAt    time.Time  `json:"createdAt"`
	DecidedAt    *time.Time `json:"decidedAt"`
}

type feeLinksResult struct {
	Items []feeLinkItem `json:"items"`
	Total int           `json:"total"`
}

// cmdFeesLinksDecide implements `fees links confirm|dismiss --id`.
func cmdFeesLinksDecide(ctx context.Context, svc *fees.Service, op string, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("fees links " + op)
	id := fs.String("id", "", "链接提案 id，必填")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *id == "" {
		return &usageError{msg: "fees links " + op + " 需要 --id", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "fees links " + op + " 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	var l domain.FeeSiteLink
	var err error
	if op == "confirm" {
		l, err = svc.ConfirmLink(ctx, *id)
	} else {
		l, err = svc.DismissLink(ctx, *id)
	}
	if err != nil {
		return err
	}
	return writeJSON(stdout, feeLinkItem{
		ID: l.ID, FeeSiteCode: l.FeeSiteCode, ResourceID: l.ResourceID,
		ResourceType: string(l.ResourceType), ResourceKey: l.ResourceKey,
		Grade: string(l.Grade), Rule: l.Rule, Similarity: l.Similarity,
		Status: string(l.Status), Source: string(l.Source), CreatedAt: l.CreatedAt,
		DecidedAt: l.DecidedAt,
	})
}

// cmdFeesClueDecisions implements `fees clue-decisions list|verify|dismiss`
// (#119) — the human channel of the risk-clue verification loop. verify:
// the clue is real, stays visible as the known-issues ledger; dismiss:
// ruled out, suppressed from the default risk output and counted in the
// envelope. Re-deciding flips the status (UPSERT); the decision survives
// batch rollback (pure audit, no FK — the W005/W006 doctrine).
func cmdFeesClueDecisions(ctx context.Context, svc *fees.Service, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &usageError{msg: "fees clue-decisions 需要 list、verify 或 dismiss 子命令"}
	}
	sub, subArgs := args[0], args[1:]
	switch sub {
	case "list":
		return cmdFeesClueDecisionsList(ctx, svc, subArgs, stdout, stderr)
	case "verify", "dismiss":
		return cmdFeesClueDecisionsDecide(ctx, svc, sub, subArgs, stdout, stderr)
	default:
		return &usageError{msg: fmt.Sprintf("fees clue-decisions 子命令必须是 list、verify 或 dismiss，收到 %q", sub)}
	}
}

// feeClueDecisionItem is the CLI ledger item shape (mirrors the HTTP one).
type feeClueDecisionItem struct {
	ID         string `json:"id"`
	Scenario   string `json:"scenario"`
	ScenarioCn string `json:"scenarioCn"`
	FeeType    string `json:"feeType"`
	SiteCode   string `json:"siteCode"`
	Year       int    `json:"year"`
	Month      int    `json:"month"`
	Status     string `json:"status"`
	Note       string `json:"note,omitempty"`
	DecidedAt  string `json:"decidedAt"`
}

// cmdFeesClueDecisionsList implements `fees clue-decisions list
// [--scenario] [--fee-type] [--status] [--site] [--year] [--limit]`.
func cmdFeesClueDecisionsList(ctx context.Context, svc *fees.Service, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("fees clue-decisions list")
	scenario := fs.String("scenario", "", "按风险场景过滤："+strings.Join(fees.RiskScenarios(), " / "))
	feeType := fs.String("fee-type", "", "按费用类型过滤："+strings.Join(fees.FeeTypes(), " / "))
	status := fs.String("status", "", "按裁定状态过滤：verified / dismissed（缺省全部）")
	site := fs.String("site", "", "按站址编码过滤")
	year := fs.Int("year", 0, "按年份过滤（2000-2100）")
	limit := fs.Int("limit", 20, "页大小（默认 20，最大 100）")
	offset := fs.Int("offset", 0, "偏移（缺省 0）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "fees clue-decisions list 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	if *scenario != "" {
		if _, err := fees.ParseRiskScenario(*scenario); err != nil {
			return &usageError{msg: err.Error(), usage: usage.String()}
		}
	}
	if *feeType != "" {
		if _, err := fees.ParseFeeType(*feeType); err != nil {
			return &usageError{msg: err.Error(), usage: usage.String()}
		}
	}
	if *status != "" {
		if _, err := domain.ParseClueDecisionStatus(*status); err != nil {
			return &usageError{msg: err.Error(), usage: usage.String()}
		}
	}
	f := repo.ClueDecisionFilter{
		Scenario: *scenario, FeeType: *feeType, Status: *status,
		SiteCode: *site, Year: *year, Limit: *limit, Offset: *offset,
	}
	items, total, err := svc.ListClueDecisions(ctx, f)
	if err != nil {
		return err
	}
	out := make([]feeClueDecisionItem, 0, len(items))
	for _, d := range items {
		out = append(out, feeClueDecisionItem{
			ID: d.ID, Scenario: d.Scenario,
			ScenarioCn: fees.RiskScenarioLabels[fees.RiskScenario(d.Scenario)],
			FeeType:    string(d.FeeType), SiteCode: d.SiteCode,
			Year: d.Year, Month: d.Month, Status: string(d.Status),
			Note: d.Note, DecidedAt: d.DecidedAt.Format(time.RFC3339),
		})
	}
	return writeJSON(stdout, struct {
		Items  []feeClueDecisionItem `json:"items"`
		Total  int                   `json:"total"`
		Limit  int                   `json:"limit"`
		Offset int                   `json:"offset"`
	}{Items: out, Total: total, Limit: f.Limit, Offset: f.Offset})
}

// cmdFeesClueDecisionsDecide implements `fees clue-decisions verify|dismiss
// --scenario --type --site --year [--month] [--note]`.
func cmdFeesClueDecisionsDecide(ctx context.Context, svc *fees.Service, op string, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("fees clue-decisions " + op)
	scenario := fs.String("scenario", "", "风险场景，必填："+strings.Join(fees.RiskScenarios(), " / "))
	feeType := fs.String("type", "", "费用类型，必填："+strings.Join(fees.FeeTypes(), " / "))
	site := fs.String("site", "", "站址编码，必填")
	year := fs.Int("year", 0, "年份，必填（2000-2100）")
	month := fs.Int("month", 0, "月份（0 = 整年口径线索，缺省 0）")
	note := fs.String("note", "", "裁定理由（可选，审计留档）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if *scenario == "" || *feeType == "" || *site == "" || *year == 0 {
		return &usageError{msg: "fees clue-decisions " + op + " 需要 --scenario、--type、--site、--year", usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "fees clue-decisions " + op + " 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	// closed-set validation answers usage errors (exit 2) — the same
	// shape as cmdFeesRisks; the service re-validates as defense in depth.
	if _, err := fees.ParseRiskScenario(*scenario); err != nil {
		return &usageError{msg: err.Error(), usage: usage.String()}
	}
	if _, err := fees.ParseFeeType(*feeType); err != nil {
		return &usageError{msg: err.Error(), usage: usage.String()}
	}
	status := domain.ClueDecisionVerified
	if op == "dismiss" {
		status = domain.ClueDecisionDismissed
	}
	d, err := svc.DecideClue(ctx, domain.RiskClueDecision{
		Scenario: *scenario, FeeType: domain.FeeType(*feeType), SiteCode: *site,
		Year: *year, Month: *month, Status: status, Note: *note,
	})
	if err != nil {
		return err
	}
	return writeJSON(stdout, feeClueDecisionItem{
		ID: d.ID, Scenario: d.Scenario,
		ScenarioCn: fees.RiskScenarioLabels[fees.RiskScenario(d.Scenario)],
		FeeType:    string(d.FeeType), SiteCode: d.SiteCode,
		Year: d.Year, Month: d.Month, Status: string(d.Status),
		Note: d.Note, DecidedAt: d.DecidedAt.Format(time.RFC3339),
	})
}

// cmdFeesVerifySources implements `fees verify-sources` (issue #6 方案 A2):
// re-hash every fee batch's recorded source file against its import-time
// hash — match / changed / missing, plus unknown for pre-v35 rows (skipped,
// never guessed). The single-JSON report carries per-batch evidence; a
// non-empty "changed" count exits non-zero (the operator's terminal can
// see it) while stdout stays the one envelope (errAlreadyPrinted).
func cmdFeesVerifySources(ctx context.Context, svc *fees.Service, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("fees verify-sources")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "verify-sources 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	report, err := svc.VerifySources(ctx)
	if err != nil {
		return err
	}
	enc, jerr := json.MarshalIndent(report, "", "  ")
	if jerr != nil {
		return jerr
	}
	if _, err := stdout.Write(append(enc, '\n')); err != nil {
		return err
	}
	if report.Changed > 0 {
		return fmt.Errorf("%d 个批次的源文件在导入后被改动（明细见 stdout JSON）: %w", report.Changed, errAlreadyPrinted)
	}
	return nil
}

// cmdFeesVerifyBatches implements `fees verify-batches` (#20 方案 C + #32
// data face): the three-face period consistency audit — fee_batches vs
// the snapshot path vs the analysis partitions, plus the same-SHA-
// multi-period family report, plus every completed batch's money-health
// aggregate (all-zero amounts / blank site codes / all-negative).
// Read-only: nothing is mutated, nothing is refused; a non-empty problem
// count exits non-zero so the operator's terminal sees it (stdout keeps
// the one JSON envelope, errAlreadyPrinted discipline). The default
// build answers with dataChecksSkipped: true — the period face still
// runs, the data face is honestly named as not-run, never silently
// skipped.
func cmdFeesVerifyBatches(ctx context.Context, svc *fees.Service, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("fees verify-batches")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "verify-batches 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	report, err := svc.VerifyBatches(ctx, fees.Query(ctx))
	if err != nil {
		return err
	}
	enc, jerr := json.MarshalIndent(report, "", "  ")
	if jerr != nil {
		return jerr
	}
	if _, err := stdout.Write(append(enc, '\n')); err != nil {
		return err
	}
	if report.DataChecksSkipped {
		fmt.Fprintln(stderr, "注意：默认构建未装配 duckdb 引擎——数据有效性检查（金额全零/关键列空/全负）未运行；账期一致性检查结果不受影响")
	}
	if len(report.Problems) > 0 {
		return fmt.Errorf("发现 %d 处问题（账期 %d + 数据有效性 %d，明细见 stdout JSON）: %w",
			len(report.Problems), len(report.PeriodProblems), len(report.DataProblems), errAlreadyPrinted)
	}
	return nil
}

// ---- fees rules (#2 phase 1): the L1 configurable risk-rule management —
// the {metric, aggregation, dimension, comparator, threshold, window}
// six-tuples. Channel doctrine: create is the AI/operator channel (the
// rule lands DISABLED — the conservative observation state); enable/disable
// is the human channel (CLI here, admin-tier HTTP there; the MCP surface
// gains create only); every mutation lands its audit row (阈值变更留痕 —
// phase 1 has no update channel, so a threshold change IS disable-old +
// create-new, both audited). ----

// cmdFeesRules implements `fees rules list|create|enable|disable|audit`.
func cmdFeesRules(ctx context.Context, svc *fees.Service, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &usageError{msg: "fees rules 需要 list、create、enable、disable 或 audit 子命令"}
	}
	sub, subArgs := args[0], args[1:]
	switch sub {
	case "list":
		return cmdFeesRulesList(ctx, svc, subArgs, stdout, stderr)
	case "create":
		return cmdFeesRulesCreate(ctx, svc, subArgs, stdout, stderr)
	case "enable", "disable":
		return cmdFeesRulesSetEnabled(ctx, svc, sub == "enable", subArgs, stdout, stderr)
	case "audit":
		return cmdFeesRulesAudit(ctx, svc, subArgs, stdout, stderr)
	default:
		return &usageError{msg: fmt.Sprintf("fees rules 子命令必须是 list、create、enable、disable 或 audit，收到 %q", sub)}
	}
}

// feeRuleItem is the CLI rule shape (mirrors the HTTP one; the six-tuple
// travels whole — a rule is evidence, never a black box).
type feeRuleItem struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	FeeType      string  `json:"feeType"`
	Metric       string  `json:"metric"`
	Aggregation  string  `json:"aggregation"`
	Dimension    string  `json:"dimension"`
	Comparator   string  `json:"comparator"`
	Threshold    float64 `json:"threshold"`
	WindowMonths int     `json:"windowMonths"`
	Severity     string  `json:"severity"`
	Description  string  `json:"description,omitempty"`
	Enabled      bool    `json:"enabled"`
	CreatedAt    string  `json:"createdAt"`
	UpdatedAt    string  `json:"updatedAt"`
}

func feeRuleItemOf(r domain.FeeRiskRule) feeRuleItem {
	return feeRuleItem{
		ID: r.ID, Name: r.Name, FeeType: string(r.FeeType), Metric: r.Metric,
		Aggregation: r.Aggregation, Dimension: r.Dimension, Comparator: r.Comparator,
		Threshold: r.Threshold, WindowMonths: r.WindowMonths, Severity: r.Severity,
		Description: r.Description, Enabled: r.Enabled,
		CreatedAt: r.CreatedAt.Format(time.RFC3339), UpdatedAt: r.UpdatedAt.Format(time.RFC3339),
	}
}

// cmdFeesRulesList implements `fees rules list [--enabled]`.
func cmdFeesRulesList(ctx context.Context, svc *fees.Service, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("fees rules list")
	enabled := fs.Bool("enabled", false, "仅列出已启用规则（缺省全部——观察态停用规则也可见）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "fees rules list 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	rules, err := svc.ListRules(ctx, *enabled)
	if err != nil {
		return err
	}
	out := make([]feeRuleItem, 0, len(rules))
	for _, r := range rules {
		out = append(out, feeRuleItemOf(r))
	}
	return writeJSON(stdout, struct {
		Items   []feeRuleItem `json:"items"`
		Total   int           `json:"total"`
		Enabled *bool         `json:"enabledOnly"`
	}{Items: out, Total: len(out), Enabled: enabled})
}

// cmdFeesRulesCreate implements `fees rules create --name --type --metric
// --agg --dimension --comparator --threshold [--window] --severity
// [--description] [--actor]` — the rule lands DISABLED (观察态); a human
// enables it after review.
func cmdFeesRulesCreate(ctx context.Context, svc *fees.Service, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("fees rules create")
	name := fs.String("name", "", "规则名，必填（线索溯源展示，唯一）")
	feeType := fs.String("type", "", "费用类型，必填："+strings.Join(fees.FeeTypes(), " / "))
	metric := fs.String("metric", "", "度量，必填：amount（金额）/ rows（行数）")
	agg := fs.String("agg", "", "聚合，必填：sum / avg / max / count（rows 度量固定 count）")
	dimension := fs.String("dimension", "site_month", "维度，本期固定 site_month（站址×账期）")
	comparator := fs.String("comparator", "", "算子，必填：gt / gte / lt / lte / mom_ratio（环比涨幅比）")
	threshold := fs.Float64("threshold", 0, "阈值，必填（金额类=分、行数类=行数、环比类=涨幅比如 0.30 表示 +30%）；≥ 0")
	window := fs.Int("window", 1, "环比基期回看月数（1-12，仅 mom_ratio 使用，缺省 1）")
	severity := fs.String("severity", "", "严重度，必填：high / medium / low")
	desc := fs.String("description", "", "规则口径描述（可选，≤500 字，随线索展示）")
	actor := fs.String("actor", "cli", "审计留痕的操作者标识（缺省 cli；HTTP 面按令牌层级，MCP 面固定 mcp）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "fees rules create 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	if *name == "" || *feeType == "" || *metric == "" || *agg == "" || *comparator == "" || *severity == "" {
		return &usageError{msg: "fees rules create 需要 --name、--type、--metric、--agg、--comparator、--severity", usage: usage.String()}
	}
	rule, err := svc.CreateRule(ctx, fees.RuleInput{
		Name: *name, FeeType: *feeType, Metric: *metric, Aggregation: *agg,
		Dimension: *dimension, Comparator: *comparator, Threshold: *threshold,
		WindowMonths: *window, Severity: *severity, Description: *desc,
	}, *actor)
	if err != nil {
		return err
	}
	return writeJSON(stdout, feeRuleItemOf(rule))
}

// cmdFeesRulesSetEnabled implements `fees rules enable|disable --id
// [--actor]` — the HUMAN channel (a fresh rule fires nothing until this
// flip); every flip lands its audit row.
func cmdFeesRulesSetEnabled(ctx context.Context, svc *fees.Service, enabled bool, args []string, stdout, stderr io.Writer) error {
	op := "disable"
	if enabled {
		op = "enable"
	}
	fs, usage := newFlagSet("fees rules " + op)
	id := fs.String("id", "", "规则 ID，必填（fees rules list 查看）")
	actor := fs.String("actor", "cli", "审计留痕的操作者标识（缺省 cli）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "fees rules " + op + " 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	if *id == "" {
		return &usageError{msg: "fees rules " + op + " 需要 --id", usage: usage.String()}
	}
	if err := svc.SetRuleEnabled(ctx, *id, enabled, *actor); err != nil {
		return err
	}
	rule, err := svc.RuleByID(ctx, *id)
	if err != nil {
		return err
	}
	return writeJSON(stdout, feeRuleItemOf(rule))
}

// cmdFeesRulesAudit implements `fees rules audit --id` — the action
// ledger of one rule (created/enabled/disabled, newest first): the
// 阈值变更留痕 red line made visible.
func cmdFeesRulesAudit(ctx context.Context, svc *fees.Service, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("fees rules audit")
	id := fs.String("id", "", "规则 ID，必填（fees rules list 查看）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "fees rules audit 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	if *id == "" {
		return &usageError{msg: "fees rules audit 需要 --id", usage: usage.String()}
	}
	entries, err := svc.RuleAudit(ctx, *id)
	if err != nil {
		return err
	}
	out := make([]feeRuleAuditItem, 0, len(entries))
	for _, a := range entries {
		out = append(out, feeRuleAuditItem{
			ID: a.ID, RuleID: a.RuleID, Action: a.Action, Actor: a.Actor,
			Changes: a.Changes, At: a.At.Format(time.RFC3339),
		})
	}
	return writeJSON(stdout, struct {
		RuleID string             `json:"ruleId"`
		Items  []feeRuleAuditItem `json:"items"`
		Total  int                `json:"total"`
	}{RuleID: *id, Items: out, Total: len(out)})
}

// feeRuleAuditItem is the CLI audit-entry shape (mirrors the HTTP one).
type feeRuleAuditItem struct {
	ID      int64  `json:"id"`
	RuleID  string `json:"ruleId"`
	Action  string `json:"action"`
	Actor   string `json:"actor"`
	Changes string `json:"changes,omitempty"`
	At      string `json:"at"`
}

// ---- 明细层 CLI（#16 完成线 6：fees details add/list/status/reconcile） ----

// cmdFeesDetails dispatches `fees details <add|list|status|reconcile>`.
func cmdFeesDetails(ctx context.Context, svc *fees.Service, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &usageError{msg: "fees details 需要 add、list、status 或 reconcile 子命令"}
	}
	sub, subArgs := args[0], args[1:]
	switch sub {
	case "add":
		return cmdFeesDetailsAdd(ctx, svc, subArgs, stdout, stderr)
	case "list":
		return cmdFeesDetailsList(ctx, svc, subArgs, stdout, stderr)
	case "status":
		return cmdFeesDetailsStatus(ctx, svc, subArgs, stdout, stderr)
	case "reconcile":
		return cmdFeesDetailsReconcile(ctx, svc, subArgs, stdout, stderr)
	default:
		return &usageError{msg: fmt.Sprintf("fees details 子命令必须是 add、list、status 或 reconcile，收到 %q", sub)}
	}
}

// feeDetailWire is the CLI detail shape (mirrors the HTTP camelCase
// contract — the two channels answer the same envelope).
type feeDetailWire struct {
	ID           string `json:"id"`
	LinkKey      string `json:"linkKey"`
	Period       string `json:"period"`
	FeeType      string `json:"feeType"`
	Category     string `json:"category"`
	AmountCents  int64  `json:"amountCents"`
	ItemName     string `json:"itemName,omitempty"`
	Status       string `json:"status"`
	CreatedBy    string `json:"createdBy"`
	CreatedAt    string `json:"createdAt"`
	VerifiedBy   string `json:"verifiedBy,omitempty"`
	RejectReason string `json:"rejectReason,omitempty"`
}

func feeDetailWireOf(d domain.FeeDetail) feeDetailWire {
	w := feeDetailWire{
		ID: d.ID, LinkKey: d.LinkKey, Period: d.Period, FeeType: string(d.FeeType),
		Category: d.Category, AmountCents: d.AmountCents, ItemName: d.ItemName,
		Status: d.Status, CreatedBy: d.CreatedBy, CreatedAt: d.CreatedAt.Format(time.RFC3339),
		VerifiedBy: d.VerifiedBy, RejectReason: d.RejectReason,
	}
	return w
}

// cmdFeesDetailsAdd implements `fees details add --link-key --period
// --type --category --amount-yuan [--item-name ...] [--actor]` — the
// three #16 mandates are flags, the optional fields are flags too.
func cmdFeesDetailsAdd(ctx context.Context, svc *fees.Service, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("fees details add")
	linkKey := fs.String("link-key", "", "关联键：站址编码或工号（这笔钱属于谁——#16 三强制之一）")
	period := fs.String("period", "", "账期 YYYYMM（哪个月——#16 三强制之一）")
	feeType := fs.String("type", "", "费用类型 "+strings.Join(fees.FeeTypes(), "/"))
	category := fs.String("category", "", "分类（建议集或本地自建，非空即可）")
	amountYuan := fs.String("amount-yuan", "", "金额（元，两位小数；负数=冲销——#16 三强制之一）")
	itemName := fs.String("item-name", "", "原始项目名（可选，照抄原始表）")
	reason := fs.String("reason", "", "事由（可选）")
	sourceFile := fs.String("source-file", "", "来源文件名（可选，追溯）")
	actor := fs.String("actor", "", "录入人（必填——人工数据必须有来源痕迹）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "fees details add 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	if *amountYuan == "" {
		return &usageError{msg: "fees details add 需要 --amount-yuan（金额是三强制之一）", usage: usage.String()}
	}
	yuan, err := strconv.ParseFloat(*amountYuan, 64)
	if err != nil {
		return &usageError{msg: "--amount-yuan 必须是数字：" + err.Error(), usage: usage.String()}
	}
	detail, err := svc.AddDetail(ctx, fees.DetailInput{
		LinkKey: *linkKey, Period: *period, FeeType: *feeType, Category: *category,
		AmountYuan: &yuan, ItemName: *itemName, Reason: *reason, SourceFile: *sourceFile,
	}, *actor)
	if err != nil {
		return err
	}
	return writeJSON(stdout, feeDetailWireOf(detail))
}

// cmdFeesDetailsList implements `fees details list [--link-key --period
// --type --status --category]`.
func cmdFeesDetailsList(ctx context.Context, svc *fees.Service, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("fees details list")
	linkKey := fs.String("link-key", "", "按关联键过滤（站址编码或工号）")
	period := fs.String("period", "", "按账期过滤 YYYYMM")
	feeType := fs.String("type", "", "按费用类型过滤 "+strings.Join(fees.FeeTypes(), "/"))
	status := fs.String("status", "", "按状态过滤 pending/confirmed/rejected/unmatched")
	category := fs.String("category", "", "按分类过滤")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "fees details list 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	details, err := svc.ListDetails(ctx, domain.FeeDetailFilter{
		LinkKey: *linkKey, Period: *period, FeeType: domain.FeeType(*feeType),
		Status: *status, Category: *category,
	})
	if err != nil {
		return err
	}
	out := make([]feeDetailWire, 0, len(details))
	for _, d := range details {
		out = append(out, feeDetailWireOf(d))
	}
	return writeJSON(stdout, struct {
		Items []feeDetailWire `json:"items"`
		Total int             `json:"total"`
	}{Items: out, Total: len(out)})
}

// cmdFeesDetailsStatus implements `fees details status --id --to
// confirmed|rejected|unmatched [--reason] [--actor]` — the human
// verification channel with provenance.
func cmdFeesDetailsStatus(ctx context.Context, svc *fees.Service, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("fees details status")
	id := fs.String("id", "", "明细 id（fd_…）")
	to := fs.String("to", "", "目标状态 confirmed/rejected/unmatched")
	reason := fs.String("reason", "", "驳回原因（rejected 时必填）")
	actor := fs.String("actor", "", "核实人（必填——核实留痕）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "fees details status 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	if err := svc.SetDetailStatus(ctx, *id, *to, *actor, *reason); err != nil {
		return err
	}
	detail, err := svc.DetailByID(ctx, *id)
	if err != nil {
		return err
	}
	return writeJSON(stdout, feeDetailWireOf(detail))
}

// cmdFeesDetailsReconcile implements `fees details reconcile --link-key
// --period --type` — the 差额提示 (hint, never a block); the default
// build answers the detail side with the honest engine-absent note.
func cmdFeesDetailsReconcile(ctx context.Context, svc *fees.Service, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("fees details reconcile")
	linkKey := fs.String("link-key", "", "关联键（站址编码或工号）")
	period := fs.String("period", "", "账期 YYYYMM")
	feeType := fs.String("type", "", "费用类型 "+strings.Join(fees.FeeTypes(), "/"))
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "fees details reconcile 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	rec, err := svc.ReconcileDetails(ctx, fees.Query(ctx), *linkKey, *period, *feeType)
	if err != nil {
		return err
	}
	return writeJSON(stdout, rec)
}

// ---- 跨数据集对账 CLI（#12：fees aliases + fees cross-reconcile） ----

// cmdFeesAliases dispatches `fees aliases <add|list|remove|scan>`.
func cmdFeesAliases(ctx context.Context, svc *fees.Service, dataDir string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &usageError{msg: "fees aliases 需要 add、list、remove 或 scan 子命令"}
	}
	sub, subArgs := args[0], args[1:]
	switch sub {
	case "add":
		return cmdFeesAliasesAdd(ctx, svc, subArgs, stdout, stderr)
	case "list":
		return cmdFeesAliasesList(ctx, svc, subArgs, stdout, stderr)
	case "remove":
		return cmdFeesAliasesRemove(ctx, svc, subArgs, stdout, stderr)
	case "scan":
		return cmdFeesAliasesScan(ctx, svc, dataDir, subArgs, stdout, stderr)
	default:
		return &usageError{msg: fmt.Sprintf("fees aliases 子命令必须是 add、list、remove 或 scan，收到 %q", sub)}
	}
}

func cmdFeesAliasesAdd(ctx context.Context, svc *fees.Service, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("fees aliases add")
	canonical := fs.String("canonical", "", "主编码（对账归并目标键）")
	alias := fs.String("alias", "", "别名（另一系统的编码或站名）")
	source := fs.String("source", "", "来源系统 "+strings.Join(append(fees.FeeTypes(), "manual"), "/"))
	confidence := fs.String("confidence", "manual", "置信 exact/rule/manual（人工确认缺省 manual）")
	actor := fs.String("actor", "", "建立人（必填——映射可审计）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "fees aliases add 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	a, err := svc.AddSiteCodeAlias(ctx, fees.SiteCodeAliasInput{
		Canonical: *canonical, Alias: *alias, Source: *source, Confidence: *confidence,
	}, *actor)
	if err != nil {
		return err
	}
	return writeJSON(stdout, struct {
		ID         string `json:"id"`
		Canonical  string `json:"canonical"`
		Alias      string `json:"alias"`
		Source     string `json:"source"`
		Confidence string `json:"confidence"`
		CreatedBy  string `json:"createdBy"`
	}{ID: a.ID, Canonical: a.Canonical, Alias: a.Alias, Source: string(a.Source),
		Confidence: a.Confidence, CreatedBy: a.CreatedBy})
}

func cmdFeesAliasesList(ctx context.Context, svc *fees.Service, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("fees aliases list")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "fees aliases list 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	aliases, err := svc.ListSiteCodeAliases(ctx)
	if err != nil {
		return err
	}
	type wireItem struct {
		ID         string `json:"id"`
		Canonical  string `json:"canonical"`
		Alias      string `json:"alias"`
		Source     string `json:"source"`
		Confidence string `json:"confidence"`
		CreatedBy  string `json:"createdBy"`
		CreatedAt  string `json:"createdAt"`
	}
	out := make([]wireItem, 0, len(aliases))
	for _, a := range aliases {
		out = append(out, wireItem{ID: a.ID, Canonical: a.Canonical, Alias: a.Alias,
			Source: string(a.Source), Confidence: a.Confidence, CreatedBy: a.CreatedBy,
			CreatedAt: a.CreatedAt.Format(time.RFC3339)})
	}
	return writeJSON(stdout, struct {
		Items []wireItem `json:"items"`
		Total int        `json:"total"`
	}{Items: out, Total: len(out)})
}

func cmdFeesAliasesRemove(ctx context.Context, svc *fees.Service, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("fees aliases remove")
	id := fs.String("id", "", "映射 id（fca_…）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "fees aliases remove 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	if err := svc.RemoveSiteCodeAlias(ctx, *id); err != nil {
		return err
	}
	return writeJSON(stdout, map[string]any{"removed": *id})
}

func cmdFeesAliasesScan(ctx context.Context, svc *fees.Service, dataDir string, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("fees aliases scan")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "fees aliases scan 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	proposals, err := svc.ProposeSiteCodeAliases(ctx, fees.Query(ctx))
	if err != nil {
		return err
	}
	return writeJSON(stdout, struct {
		Items []fees.AliasProposal `json:"items"`
		Total int                  `json:"total"`
		Note  string               `json:"note"`
	}{Items: proposals, Total: len(proposals),
		Note: "扫描只提议不落库——确认用 fees aliases add（--actor 留痕）"})
}

// cmdFeesCrossReconcile implements `fees cross-reconcile --year [--month]`
// — the cross-dataset table (#12 acceptance 2/3).
func cmdFeesCrossReconcile(ctx context.Context, svc *fees.Service, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("fees cross-reconcile")
	year := fs.Int("year", 0, "对账年份")
	month := fs.Int("month", 0, "对账月份（缺省整年）")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "fees cross-reconcile 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	if *year < 2000 || *year > 2100 {
		return &usageError{msg: "--year 必须在 2000–2100", usage: usage.String()}
	}
	res, err := svc.CrossReconcile(ctx, fees.Query(ctx), *year, *month)
	if err != nil {
		return err
	}
	return writeJSON(stdout, res)
}
