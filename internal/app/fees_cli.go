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
	"strings"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/fees"
	"github.com/mmqz/netops-lab/internal/repo"
)

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
	return "", "", "", nil, &usageError{msg: "缺少 fees 子命令（import / list / rollback / query / link-scan / links）"}
}

// cmdFees implements `netopslab fees --db <path> --data <dir> import|list|rollback|query …`.
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
	case "list":
		return cmdFeesList(ctx, svc, subArgs, stdout, stderr)
	case "rollback":
		return cmdFeesRollback(ctx, svc, subArgs, stdout, stderr)
	case "query":
		return cmdFeesQuery(ctx, svc, dataDir, subArgs, stdout, stderr)
	case "link-scan":
		return cmdFeesLinkScan(ctx, svc, subArgs, stdout, stderr)
	case "links":
		return cmdFeesLinks(ctx, svc, subArgs, stdout, stderr)
	case "risks":
		return cmdFeesRisks(ctx, svc, dataDir, subArgs, stdout, stderr)
	default:
		return &usageError{msg: fmt.Sprintf("fees 子命令必须是 import、list、rollback、query、link-scan、links 或 risks，收到 %q", sub)}
	}
}

// cmdFeesRisks implements `fees risks <scenario>` — the six risk-scenario
// presets of #64 (#58 stage 4): retired-billing / duplicate-settlement /
// ledger-mismatch / spike / outlier / unlinked. Every threshold flag maps
// to a RiskQuery field with a documented default; the output is the
// single-JSON RiskResult envelope (clues carry row-level evidence
// anchors pointing back into the snapshot layer).
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
	})
	if err != nil {
		return err
	}
	return writeJSON(stdout, result)
}

// cmdFeesImport implements `fees import --file --type --year --month [--map <json>]`.
func cmdFeesImport(ctx context.Context, svc *fees.Service, args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("fees import")
	file := fs.String("file", "", "费用文件路径（.csv / .xlsx / .xls），必填")
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

// cmdFeesQuery implements `fees query totals|duplicates --type --year [--month] [--limit]`.
func cmdFeesQuery(ctx context.Context, svc *fees.Service, dataDir string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return &usageError{msg: "fees query 需要 totals 或 duplicates 子命令"}
	}
	preset, rest := args[0], args[1:]
	fs, usage := newFlagSet("fees query " + preset)
	feeType := fs.String("type", "", "费用类型，必填")
	year := fs.Int("year", 0, "年份（必填）")
	month := fs.Int("month", 0, "月份（可选：缺省整年）")
	limit := fs.Int("limit", 20, "totals 返回条数上限（默认 20）")
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
	case "duplicates":
		rows, err := engine.Duplicates(ctx, analysis, t, *year, *month)
		if err != nil {
			return err
		}
		return writeJSON(stdout, map[string]any{
			"preset": "duplicates", "feeType": string(t), "year": *year, "month": *month,
			"items": rows, "total": len(rows),
		})
	default:
		return &usageError{msg: fmt.Sprintf("fees query 预设必须是 totals 或 duplicates，收到 %q", preset)}
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
