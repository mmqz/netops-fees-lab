package mcpserver

// analyticstools.go — #36's second and third MCP batches: the four
// read-only tools that complete the AI analysis loop the issue drew —
// 跑场景 → 看证据 → 查关联 → 对账 → 看批次 → 自检. The first batch
// (evidencetools.go) handed over the 证据/关联 entry points; these four
// expose the remaining read faces that already lived on CLI/HTTP:
//
//      fee_cross_reconcile  P1  the 跨数据集对账表 WITH #38 attribution —
//                              the "电费和塔租对不上是因为什么" answer
//      fee_details_list     P1  the 明细补录层 journal — 人工佐证的查证面
//      fee_batches_list    P2  the import batch face — "数据来源是否可靠"
//      fee_verify_batches  P2  the 数据自检 — batch/period/data health
//
// All four are readOnlyHint=true (both server faces, --read-only included),
// all four answer the SAME service methods their HTTP twins call (one
// contract, two transports — the output shapes mirror the HTTP JSON), and
// every answer carries its note (口径随行). Governance is the surface's
// own: limit default 20 cap 50 where a list can grow unbounded; the
// reconcile's rows ride the anomaly-first order so the cap keeps the
// strongest signal, with rowsTotal + the full attribution histogram
// carrying what the cap trimmed (a capped answer never pretends to be
// the whole table).
//
// Engine faces: fee_cross_reconcile and fee_verify_batches' data half
// need the engine — the reconcile passes the honest engine-not-built
// refusal through verbatim on default builds (the fee_risks ruling), and
// verify-batches answers on ANY build with dataChecksSkipped=true naming
// the not-run half honestly (the CLI twin's own discipline). The two
// list tools are pure DB faces — they work on every build.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/fees"
	"github.com/mmqz/netops-fees-lab/internal/services"
)

const (
	feeCrossReconcileName = "fee_cross_reconcile"
	feeDetailsListName    = "fee_details_list"
	feeBatchesListName    = "fee_batches_list"
	feeVerifyBatchesName  = "fee_verify_batches"

	feeCrossReconcileNote = "对账表是待核实差异而非认定结论：only_electricity=疑似自建站或编码不一致 / only_tower=多为共享站（正常）或电费在别处 / both=可进一步核对；每行 only_* 差异的 attribution 是主数据（站址性质/供电方式/生命周期）的结构化判读——归因仍非认定结论，请人工核实；attribution=unknown 表示缺站址主数据、无法判断差异性质（不是正常）"
	feeDetailsListNote    = "明细补录层是佐证而非权威（#16 教义）：人工录入的解释性条目，金额为整数分（负数=冲销），用于解释系统台账金额去向、供人工核对，不参与风险计算与对账合并；status=pending/confirmed/rejected/unmatched 是人工裁定通道的痕迹"
	feeBatchesListNote    = "批次列表是数据来源面：每批次=一次导入（快照+分析行+行计数），status=completed/failed/rolled_back；同一文件重复导入会被幂等键拒为重复批次——判断数据是否可靠/是否重复导入时先看此面；批次 id 亦即线索证据锚点的 importBatch，可用 fee_evidence 的原始行模式回读快照"
	feeVerifyBatchesNote  = "verify-batches 是数据自检面（#20/#32）：批次/快照/分析分区三面一致性与账期一致性检查 + 已完成批次的金额健康聚合（金额全零/关键列空/全负）；默认构建未装配 duckdb 引擎时 dataChecksSkipped=true（数据面如实未跑，非静默跳过）；问题清单是待核实线索，非账目错误认定"
)

// addAnalyticsTools registers #36's second+third batch tools (read-only,
// both faces: NewServer and the --read-only variant).
func addAnalyticsTools(server *mcp.Server, svc *services.Services) {
	if svc.Fees == nil {
		return // the fee service is not wired (no data dir) — no tools
	}
	mcp.AddTool(server, &mcp.Tool{
		Name: feeCrossReconcileName,
		Description: "只读·跨数据集对账查询（#36 第二批）：电费台账与塔类详单按主编码归并的对账表（与 CLI cross-reconcile / HTTP GET /api/fees/cross-reconcile 同一契约）。" +
			"每行携带三类分拣（only_electricity/only_tower/both）、金额差额与 #38 归因 attribution（shared_no_power_needed=共享站无供电（正常）/ retired_but_billed=已退网仍计费（风险）/ power_should_exist=应供电但电费无记录（漏登风险）/ code_mismatch=疑编码不一致（有映射提案）/ self_built_no_tower_needed=自建站无塔租（正常）/ unknown=缺站址主数据无法判断）。" +
			"输出含全量三类计数与归因直方图（attributionCounts）；rows 按异常优先排序、limit 缺省 20 上限 50，截断时 rowsTotal 与直方图仍为全量口径。默认构建未编译分析引擎时如实报错（501 同源拒答）。",
		Annotations: readOnlyHints(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in feeCrossReconcileInput) (*mcp.CallToolResult, feeCrossReconcileOutput, error) {
		return feeCrossReconcile(ctx, svc, in)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: feeDetailsListName,
		Description: "只读·费用明细补录层查询（#36 第二批）：人工录入的解释性条目（这笔钱属于谁/哪个月/多少分/哪类），与 HTTP GET /api/fees/details 同一契约——" +
			"全部过滤项可选：linkKey（站址编码或工号）/ period（YYYYMM）/ feeType / status（pending/confirmed/rejected/unmatched）/ category。返回最新在前条目与 total；limit 缺省 20 上限 50。明细是佐证而非权威，供人工核对金额去向。",
		Annotations: readOnlyHints(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in feeDetailsListInput) (*mcp.CallToolResult, feeDetailsListOutput, error) {
		return feeDetailsList(ctx, svc, in)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: feeBatchesListName,
		Description: "只读·费用导入批次查询（#36 第三批）：与 HTTP GET /api/fees/batches 同一契约——每批次=一次导入（工作簿/费用类型/账期/状态/行计数/时间戳），最新在前。" +
			"status 可过滤（completed/failed/rolled_back）；limit 缺省 20 上限 50。批次 id 即线索锚点的 importBatch（fee_evidence 原始行模式的入口）；同文件重复导入在此面呈重复/拒识形态——判断数据来源可靠性的第一面。",
		Annotations: readOnlyHints(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in feeBatchesListInput) (*mcp.CallToolResult, feeBatchesListOutput, error) {
		return feeBatchesList(ctx, svc, in)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: feeVerifyBatchesName,
		Description: "只读·费用数据自检（#36 第三批）：与 CLI fees verify-batches 同一契约——批次表/快照路径/分析分区三面一致性 + 账期一致性（同 SHA 多账期族）+ 已完成批次的金额健康聚合（金额全零/关键列空/全负）。" +
			"任何构建可用：默认构建 dataChecksSkipped=true（数据面如实未跑，账期面不受影响）。问题清单是待核实线索而非账目错误认定；非零问题数即应人工核查。",
		Annotations: readOnlyHints(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in feeVerifyBatchesInput) (*mcp.CallToolResult, feeVerifyBatchesOutput, error) {
		return feeVerifyBatches(ctx, svc)
	})
}

// governedLimit answers the input limit under the surface's own bound
// (default 20, cap 50, >= 1 — feetools.go's governance).
func governedLimit(in *int) (int, error) {
	if in == nil {
		return 20, nil
	}
	if *in < 1 {
		return 0, fmt.Errorf("limit 必须是 ≥ 1 的整数，收到 %d", *in)
	}
	if *in > 50 {
		return 50, nil
	}
	return *in, nil
}

// ---- fee_cross_reconcile ----

type feeCrossReconcileInput struct {
	Year  *int `json:"year" jsonschema:"账期年份（2000–2100，必填）"`
	Month *int `json:"month,omitempty" jsonschema:"账期月份（可选 0–12，0=整年；缺省 0）"`
	Limit *int `json:"limit,omitempty" jsonschema:"返回行数上限（行按异常优先排序：只在电费→只在塔表→双方），缺省 20，最大 50；计数与归因直方图始终为全量口径"`
}

type feeCrossReconcileOutput struct {
	Year            int `json:"year"`
	Month           int `json:"month"`
	OnlyElectricity int `json:"onlyElectricity"`
	OnlyTower       int `json:"onlyTower"`
	Both            int `json:"both"`
	AliasesApplied  int `json:"aliasesApplied"`
	// AttributionCounts is the FULL-table histogram (the cap trims rows,
	// never the counts): the issue's own answer shape — "only_tower 115
	// 条，其中 92 条共享站无供电（正常）、18 条已退网仍计费（风险）、
	// 5 条无法判断".
	AttributionCounts map[string]int  `json:"attributionCounts"`
	Rows              []fees.CrossRow `json:"rows"`
	RowsTotal         int             `json:"rowsTotal"`
	Note              string          `json:"note"`
	Warnings          []string        `json:"warnings"`
}

func feeCrossReconcile(ctx context.Context, svc *services.Services, in feeCrossReconcileInput) (*mcp.CallToolResult, feeCrossReconcileOutput, error) {
	if in.Year == nil {
		return nil, feeCrossReconcileOutput{}, fmt.Errorf("year 必填（2000–2100）")
	}
	year := *in.Year
	month := 0
	if in.Month != nil {
		month = *in.Month
	}
	// the parameter discipline fires BEFORE the engine check (the
	// 400-before-501 doctrine of the HTTP twin).
	if year < 2000 || year > 2100 || month < 0 || month > 12 {
		return nil, feeCrossReconcileOutput{}, fmt.Errorf("year 必须在 2000–2100；month 0–12（0=整年），收到 %d/%d", year, month)
	}
	limit, err := governedLimit(in.Limit)
	if err != nil {
		return nil, feeCrossReconcileOutput{}, err
	}
	engine := fees.Query(ctx)
	res, err := svc.Fees.CrossReconcile(ctx, engine, year, month)
	if err != nil {
		// the fee_risks ruling (#103): the engine-not-built refusal is a
		// CONTRACT answer and passes through verbatim; real engine failures
		// carry filesystem detail — stderr gets it, the tool error stays
		// generic.
		if errors.Is(err, fees.ErrEngineNotBuilt) {
			return nil, feeCrossReconcileOutput{}, err
		}
		slog.Error("fee_cross_reconcile engine failure", "year", year, "month", month, "error", err.Error())
		return nil, feeCrossReconcileOutput{}, fmt.Errorf("跨数据集对账查询失败（引擎内部错误，详情见服务端日志）")
	}
	counts := make(map[string]int, 6)
	for _, r := range res.Rows {
		if r.Class == string(fees.CrossBoth) {
			continue // nothing to attribute on a both row
		}
		counts[r.Attribution]++
	}
	rows := res.Rows
	if len(rows) > limit {
		rows = rows[:limit]
	}
	warnings := res.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	note := feeCrossReconcileNote + "；" + res.Note
	if len(res.Rows) > limit {
		note = fmt.Sprintf("rows 按异常优先排序截取前 %d 行（全表 %d 行，见 rowsTotal）；三类计数与 attributionCounts 为全量口径；", limit, len(res.Rows)) + note
	}
	return nil, feeCrossReconcileOutput{
		Year:              res.Year,
		Month:             res.Month,
		OnlyElectricity:   res.OnlyElectricity,
		OnlyTower:         res.OnlyTower,
		Both:              res.Both,
		AliasesApplied:    res.AliasesApplied,
		AttributionCounts: counts,
		Rows:              rows, // never nil: crossMerge seeds []
		RowsTotal:         len(res.Rows),
		Note:              note,
		Warnings:          warnings,
	}, nil
}

// ---- fee_details_list ----

type feeDetailsListInput struct {
	LinkKey  string `json:"linkKey,omitempty" jsonschema:"过滤·站址编码或工号（这笔钱属于谁）"`
	Period   string `json:"period,omitempty" jsonschema:"过滤·账期（YYYYMM）"`
	FeeType  string `json:"feeType,omitempty" jsonschema:"过滤·费用类型：electricity（电费）/ tower_lease（铁塔租费）/ repair（修理费）/ maintenance（综维费）"`
	Status   string `json:"status,omitempty" jsonschema:"过滤·状态：pending（待核）/ confirmed（已核）/ rejected（已驳）/ unmatched（不匹配）"`
	Category string `json:"category,omitempty" jsonschema:"过滤·分类（建议集 + 本地自建，如 油费/车辆维修）"`
	Limit    *int   `json:"limit,omitempty" jsonschema:"返回条数上限，缺省 20，最大 50；必须 ≥ 1"`
}

type feeDetailItemWire struct {
	ID             string   `json:"id"`
	LinkKey        string   `json:"linkKey"`
	Period         string   `json:"period"`
	FeeType        string   `json:"feeType"`
	Category       string   `json:"category"`
	AmountCents    int64    `json:"amountCents"`
	OccurredOn     string   `json:"occurredOn,omitempty"`
	ItemName       string   `json:"itemName,omitempty"`
	Quantity       *float64 `json:"quantity,omitempty"`
	QuantityUnit   string   `json:"quantityUnit,omitempty"`
	UnitPriceCents *int64   `json:"unitPriceCents,omitempty"`
	Reason         string   `json:"reason,omitempty"`
	Location       string   `json:"location,omitempty"`
	WorkOrderNo    string   `json:"workOrderNo,omitempty"`
	InvoiceNo      string   `json:"invoiceNo,omitempty"`
	Counterparty   string   `json:"counterparty,omitempty"`
	SourceFile     string   `json:"sourceFile,omitempty"`
	SourceRow      int      `json:"sourceRow,omitempty"`
	Remark         string   `json:"remark,omitempty"`
	Status         string   `json:"status"`
	CreatedBy      string   `json:"createdBy"`
	CreatedAt      string   `json:"createdAt"`
	VerifiedBy     string   `json:"verifiedBy,omitempty"`
	VerifiedAt     string   `json:"verifiedAt,omitempty"`
	RejectReason   string   `json:"rejectReason,omitempty"`
}

type feeDetailsListOutput struct {
	Items []feeDetailItemWire `json:"items"`
	Total int                 `json:"total"`
	Note  string              `json:"note"`
}

func feeDetailsList(ctx context.Context, svc *services.Services, in feeDetailsListInput) (*mcp.CallToolResult, feeDetailsListOutput, error) {
	filter := domain.FeeDetailFilter{
		LinkKey:  in.LinkKey,
		Period:   in.Period,
		Status:   in.Status,
		Category: in.Category,
	}
	if in.FeeType != "" {
		ft, err := fees.ParseFeeType(in.FeeType)
		if err != nil {
			return nil, feeDetailsListOutput{}, fmt.Errorf("feeType %s", err)
		}
		filter.FeeType = ft
	}
	switch in.Status {
	case "", domain.FeeDetailStatusPending, domain.FeeDetailStatusConfirmed,
		domain.FeeDetailStatusRejected, domain.FeeDetailStatusUnmatched:
	default:
		return nil, feeDetailsListOutput{}, fmt.Errorf(
			"status 必须是 pending / confirmed / rejected / unmatched，收到 %q", in.Status)
	}
	limit, err := governedLimit(in.Limit)
	if err != nil {
		return nil, feeDetailsListOutput{}, err
	}
	details, err := svc.Fees.ListDetails(ctx, filter)
	if err != nil {
		slog.Error("fee_details_list failure", "linkKey", in.LinkKey, "error", err.Error())
		return nil, feeDetailsListOutput{}, fmt.Errorf("明细补录层查询失败（数据库错误，详情见服务端日志）")
	}
	total := len(details)
	if len(details) > limit {
		details = details[:limit]
	}
	items := make([]feeDetailItemWire, 0, len(details))
	for _, d := range details {
		it := feeDetailItemWire{
			ID: d.ID, LinkKey: d.LinkKey, Period: d.Period, FeeType: string(d.FeeType),
			Category: d.Category, AmountCents: d.AmountCents, OccurredOn: d.OccurredOn,
			ItemName: d.ItemName, Quantity: d.Quantity, QuantityUnit: d.QuantityUnit,
			UnitPriceCents: d.UnitPriceCents, Reason: d.Reason, Location: d.Location,
			WorkOrderNo: d.WorkOrderNo, InvoiceNo: d.InvoiceNo, Counterparty: d.Counterparty,
			SourceFile: d.SourceFile, SourceRow: d.SourceRow, Remark: d.Remark,
			Status: d.Status, CreatedBy: d.CreatedBy, CreatedAt: d.CreatedAt.UTC().Format(time.RFC3339),
			VerifiedBy: d.VerifiedBy,
		}
		if d.VerifiedAt != nil {
			it.VerifiedAt = d.VerifiedAt.UTC().Format(time.RFC3339)
		}
		it.RejectReason = d.RejectReason
		items = append(items, it)
	}
	note := feeDetailsListNote
	if total > limit {
		note = fmt.Sprintf("条目最新在前，截取前 %d 条（全量 %d 条见 total）；", limit, total) + note
	}
	return nil, feeDetailsListOutput{
		Items: items, // never nil (#93)
		Total: total,
		Note:  note,
	}, nil
}

// ---- fee_batches_list ----

type feeBatchesListInput struct {
	Status string `json:"status,omitempty" jsonschema:"过滤·批次状态：completed（完成）/ failed（失败）/ rolled_back（已回滚）"`
	Limit  *int   `json:"limit,omitempty" jsonschema:"返回条数上限，缺省 20，最大 50；必须 ≥ 1"`
}

type feeBatchItemWire struct {
	ID           string `json:"id"`
	WorkbookName string `json:"workbookName"`
	FeeType      string `json:"feeType"`
	Period       string `json:"period"`
	Status       string `json:"status"`
	RowsTotal    int    `json:"rowsTotal"`
	RowsImported int    `json:"rowsImported"`
	RowsFailed   int    `json:"rowsFailed"`
	PeriodSource string `json:"periodSource,omitempty"`
	PeriodBasis  string `json:"periodBasis,omitempty"`
	SnapshotPath string `json:"snapshotPath,omitempty"`
	CreatedAt    string `json:"createdAt"`
	CompletedAt  string `json:"completedAt,omitempty"`
}

type feeBatchesListOutput struct {
	Items []feeBatchItemWire `json:"items"`
	Total int                `json:"total"`
	Note  string             `json:"note"`
}

func feeBatchesList(ctx context.Context, svc *services.Services, in feeBatchesListInput) (*mcp.CallToolResult, feeBatchesListOutput, error) {
	var status domain.FeeStatus
	if in.Status != "" {
		switch domain.FeeStatus(in.Status) {
		case domain.FeeStatusCompleted, domain.FeeStatusFailed, domain.FeeStatusRolledBack:
			status = domain.FeeStatus(in.Status)
		default:
			return nil, feeBatchesListOutput{}, fmt.Errorf(
				"status 必须是 completed / failed / rolled_back，收到 %q", in.Status)
		}
	}
	limit, err := governedLimit(in.Limit)
	if err != nil {
		return nil, feeBatchesListOutput{}, err
	}
	batches, err := svc.Fees.List(ctx, status)
	if err != nil {
		slog.Error("fee_batches_list failure", "status", in.Status, "error", err.Error())
		return nil, feeBatchesListOutput{}, fmt.Errorf("批次查询失败（数据库错误，详情见服务端日志）")
	}
	total := len(batches)
	if len(batches) > limit {
		batches = batches[:limit]
	}
	items := make([]feeBatchItemWire, 0, len(batches))
	for _, b := range batches {
		it := feeBatchItemWire{
			ID: b.ID, WorkbookName: b.WorkbookName, FeeType: string(b.FeeType),
			Period: fees.PeriodLabel(b.FeeYear, b.FeeMonth), Status: string(b.Status),
			RowsTotal: b.RowsTotal, RowsImported: b.RowsImported, RowsFailed: b.RowsFailed,
			PeriodSource: b.PeriodSource, PeriodBasis: b.PeriodBasis,
			SnapshotPath: b.SnapshotPath,
			CreatedAt:    b.CreatedAt.UTC().Format(time.RFC3339),
		}
		if b.CompletedAt != nil {
			it.CompletedAt = b.CompletedAt.UTC().Format(time.RFC3339)
		}
		items = append(items, it)
	}
	note := feeBatchesListNote
	if total > limit {
		note = fmt.Sprintf("批次最新在前，截取前 %d 条（全量 %d 条见 total）；", limit, total) + note
	}
	return nil, feeBatchesListOutput{
		Items: items, // never nil (#93)
		Total: total,
		Note:  note,
	}, nil
}

// ---- fee_verify_batches ----

type feeVerifyBatchesInput struct{}

type feeVerifyBatchesOutput struct {
	fees.VerifyBatchesReport
	Note string `json:"note"`
}

func feeVerifyBatches(ctx context.Context, svc *services.Services) (*mcp.CallToolResult, feeVerifyBatchesOutput, error) {
	report, err := svc.Fees.VerifyBatches(ctx, fees.Query(ctx))
	if err != nil {
		slog.Error("fee_verify_batches failure", "error", err.Error())
		return nil, feeVerifyBatchesOutput{}, fmt.Errorf("费用数据自检失败（数据库或引擎错误，详情见服务端日志）")
	}
	if report.Problems == nil {
		report.Problems = []fees.VerifyBatchProblem{}
	}
	if report.PeriodProblems == nil {
		report.PeriodProblems = []fees.VerifyBatchProblem{}
	}
	if report.DataProblems == nil {
		report.DataProblems = []fees.VerifyBatchProblem{}
	}
	note := feeVerifyBatchesNote
	if report.DataChecksSkipped {
		note = "默认构建未装配 duckdb 引擎——数据有效性检查（金额全零/关键列空/全负）未运行（dataChecksSkipped=true），账期一致性检查结果不受影响；" + note
	}
	return nil, feeVerifyBatchesOutput{
		VerifyBatchesReport: report,
		Note:                note,
	}, nil
}
