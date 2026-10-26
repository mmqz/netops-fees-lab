package fees

// details.go — the manual-entry evidence tier of #16's completion line
// item 6: 系统提供「表单能力」，明细层永不参与系统表金额计算。The
// three mandates are validated here (关联键 + 金额 + 录入留痕 — every
// optional field is genuinely optional); categories carry the suggestion
// set (domain.FeeDetailSuggestedCategories) but accept local ones (#16
// explicitly over the v1 spec's closed enum). The reconcile answers the
// evidence-side sum against the system-table side (engine) — a HINT,
// never a block: 差额只提示，不阻断.

import (
	"context"
	"fmt"
	"strings"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/repo"
)

// DetailInput is the entry form: the three #16 mandates plus every
// optional field the local operator decides to fill (or not).
type DetailInput struct {
	LinkKey        string   `json:"linkKey"`
	Period         string   `json:"period"` // YYYYMM
	FeeType        string   `json:"feeType"`
	Category       string   `json:"category"`
	AmountYuan     *float64 `json:"amountYuan"`  // 元（录入面）；分是存储契约
	AmountCents    *int64   `json:"amountCents"` // 分（程序面）——两形态二选一
	OccurredOn     string   `json:"occurredOn"`
	ItemName       string   `json:"itemName"`
	Quantity       *float64 `json:"quantity"`
	QuantityUnit   string   `json:"quantityUnit"`
	UnitPriceCents *int64   `json:"unitPriceCents"`
	Reason         string   `json:"reason"`
	Location       string   `json:"location"`
	WorkOrderNo    string   `json:"workOrderNo"`
	InvoiceNo      string   `json:"invoiceNo"`
	Counterparty   string   `json:"counterparty"`
	SourceFile     string   `json:"sourceFile"`
	SourceRow      int      `json:"sourceRow"`
	Remark         string   `json:"remark"`
}

// AddDetail validates the three mandates and inserts one pending row.
// The actor IS the provenance (created_by) — the caller derives it from
// its channel (HTTP token tier / CLI --actor); a missing actor is a
// refusal, not a default: 人工数据必须有来源痕迹.
func (s *Service) AddDetail(ctx context.Context, in DetailInput, actor string) (domain.FeeDetail, error) {
	linkKey := strings.TrimSpace(in.LinkKey)
	if linkKey == "" {
		return domain.FeeDetail{}, refusef("linkKey 不能为空——不知道这笔钱属于谁（站址编码或工号），就没法用（#16 三强制之一）")
	}
	if len([]rune(linkKey)) > 128 {
		return domain.FeeDetail{}, refusef("linkKey 过长（最多 128 字）")
	}
	period := strings.TrimSpace(in.Period)
	if !validDetailPeriod(period) {
		return domain.FeeDetail{}, refusef("period 必须是 YYYYMM（202001–209912），收到 %q", period)
	}
	ft, err := ParseFeeType(in.FeeType)
	if err != nil {
		return domain.FeeDetail{}, refusef("feeType 必须是 %v 之一，收到 %q", FeeTypes(), in.FeeType)
	}
	category := strings.TrimSpace(in.Category)
	if category == "" {
		return domain.FeeDetail{}, refusef("category 不能为空——建议集 %v 或本地自建分类（#16：允许自建，不允许空）", domain.FeeDetailSuggestedCategories)
	}
	if len([]rune(category)) > 64 {
		return domain.FeeDetail{}, refusef("category 过长（最多 64 字）")
	}
	amount, err := detailAmountCents(in)
	if err != nil {
		return domain.FeeDetail{}, refusef("%v", err)
	}
	if actor == "" {
		return domain.FeeDetail{}, refusef("录入人（actor）不能为空——人工数据必须有来源痕迹（#16 三强制之一）")
	}
	d := domain.FeeDetail{
		ID: domain.NewID("fd"), LinkKey: linkKey, Period: period, FeeType: ft,
		Category: category, AmountCents: amount,
		OccurredOn: strings.TrimSpace(in.OccurredOn), ItemName: in.ItemName,
		Quantity: in.Quantity, QuantityUnit: in.QuantityUnit, UnitPriceCents: in.UnitPriceCents,
		Reason: in.Reason, Location: in.Location, WorkOrderNo: in.WorkOrderNo,
		InvoiceNo: in.InvoiceNo, Counterparty: in.Counterparty,
		SourceFile: in.SourceFile, SourceRow: in.SourceRow, Remark: in.Remark,
		Status: domain.FeeDetailStatusPending, CreatedBy: actor, CreatedAt: s.clock(),
	}
	if err := s.db.Do(ctx, func(tx *repo.Tx) error {
		// 铁律 5（同一笔明细不允许重复关联）: the entry-fingerprint
		// pre-check rides the SAME transaction — the refusal carries the
		// existing id so the operator can inspect instead of re-entering.
		dup, err := tx.FeeDetails.ExactDuplicate(ctx, d)
		if err != nil {
			return err
		}
		if dup != "" {
			return refusef("疑似重复明细——同键同类别同金额同项目名已存在 %s；若确为新发生费用请改项目名或备注以示区别", dup)
		}
		return tx.FeeDetails.Insert(ctx, d)
	}); err != nil {
		return domain.FeeDetail{}, err
	}
	return d, nil
}

// ListDetails answers the journal (newest first) under the filter.
func (s *Service) ListDetails(ctx context.Context, f domain.FeeDetailFilter) ([]domain.FeeDetail, error) {
	if f.FeeType != "" {
		if _, err := ParseFeeType(string(f.FeeType)); err != nil {
			return nil, refusef("feeType 必须是 %v 之一，收到 %q", FeeTypes(), string(f.FeeType))
		}
	}
	if f.Status != "" && !validDetailStatus(f.Status) {
		return nil, refusef("status 必须是 pending/confirmed/rejected/unmatched 之一，收到 %q", f.Status)
	}
	return s.db.FeeDetails.List(ctx, f)
}

// DetailByID answers one row (the status route's read-back).
func (s *Service) DetailByID(ctx context.Context, id string) (domain.FeeDetail, error) {
	return s.db.FeeDetails.GetByID(ctx, id)
}

// SetDetailStatus is the human verification channel (the spec §3.4 flow):
// confirmed / rejected (reason required) / unmatched — always with the
// verifier's provenance (铁律 3/7：谁核实、何时、什么原因).
func (s *Service) SetDetailStatus(ctx context.Context, id, status, actor, rejectReason string) error {
	if !validDetailStatus(status) {
		return refusef("status 必须是 confirmed/rejected/unmatched 之一，收到 %q", status)
	}
	if status == domain.FeeDetailStatusRejected && strings.TrimSpace(rejectReason) == "" {
		return refusef("驳回必须填原因（rejectReason）——状态流转留痕")
	}
	if actor == "" {
		return refusef("核实人（actor）不能为空——核实留痕（谁核实、何时）")
	}
	return s.db.Do(ctx, func(tx *repo.Tx) error {
		return tx.FeeDetails.SetStatus(ctx, id, status, actor, rejectReason, s.clock())
	})
}

// DetailReconcile is the 差额提示 (hint, never a block): the evidence-side
// sum (SQLite, always answerable) against the system-table side (the
// Parquet analysis layer — the ENGINE's to answer; a default build
// honestly reports engineCentsAvailable=false instead of inventing 0).
type DetailReconcile struct {
	LinkKey              string `json:"linkKey"`
	Period               string `json:"period"`
	FeeType              string `json:"feeType"`
	Rows                 int    `json:"rows"`
	DetailSumCents       int64  `json:"detailSumCents"`
	EngineCentsAvailable bool   `json:"engineCentsAvailable"`
	SystemCents          *int64 `json:"systemCents"` // nil when the engine is not built / the key has no system rows
	DeltaCents           *int64 `json:"deltaCents"`  // nil alongside systemCents
	Note                 string `json:"note"`
}

// ReconcileDetails computes the delta story of one link triple.
func (s *Service) ReconcileDetails(ctx context.Context, engine QueryEngine, linkKey, period, feeType string) (DetailReconcile, error) {
	linkKey = strings.TrimSpace(linkKey)
	if linkKey == "" {
		return DetailReconcile{}, refusef("linkKey 不能为空")
	}
	if !validDetailPeriod(period) {
		return DetailReconcile{}, refusef("period 必须是 YYYYMM（202001–209912），收到 %q", period)
	}
	ft, err := ParseFeeType(feeType)
	if err != nil {
		return DetailReconcile{}, refusef("feeType 必须是 %v 之一，收到 %q", FeeTypes(), feeType)
	}
	sum, rows, err := s.db.FeeDetails.SumCents(ctx, linkKey, period, ft)
	if err != nil {
		return DetailReconcile{}, err
	}
	out := DetailReconcile{
		LinkKey: linkKey, Period: period, FeeType: string(ft),
		Rows: rows, DetailSumCents: sum,
	}
	if engine.Available() {
		// The system side: the per-site totals preset filtered to this
		// key (month scope). A key absent from the analysis layer keeps
		// systemCents nil — 明细有、系统无 is the spec's warning shape,
		// not a zero.
		year, month := detailPeriodSplit(period)
		totals, err := engine.Totals(ctx, s.analysisDir(), ft, year, month, 500)
		if err != nil {
			return DetailReconcile{}, err
		}
		for _, t := range totals {
			if t.SiteCode == linkKey {
				sys := t.TotalCents
				delta := sum - sys
				out.SystemCents = &sys
				out.DeltaCents = &delta
				break
			}
		}
		out.EngineCentsAvailable = true
		if out.SystemCents == nil {
			out.Note = "系统表（分析层）中该键无行——明细有、系统无：请人工核对关联键是否写错（只提示，不阻断）"
		} else if *out.DeltaCents != 0 {
			out.Note = fmt.Sprintf("明细合计与系统表金额差 %d 分——差额只提示不阻断：可能明细待补，也可能系统表待解释（明细永不覆盖系统表）", *out.DeltaCents)
		} else {
			out.Note = "明细合计与系统表金额一致"
		}
	} else {
		out.Note = "本二进制未编译分析引擎（默认构建）——明细侧合计可答，系统表侧缺席：engine_not_built，差额暂不可比（不造零）"
	}
	return out, nil
}

// ---- small pure helpers ----

func validDetailPeriod(p string) bool {
	if len(p) != 6 {
		return false
	}
	year, month := detailPeriodSplit(p)
	return year >= 2020 && year <= 2099 && month >= 1 && month <= 12
}

func detailPeriodSplit(p string) (year, month int) {
	fmt.Sscanf(p, "%4d%2d", &year, &month)
	return year, month
}

func validDetailStatus(s string) bool {
	return s == domain.FeeDetailStatusPending || s == domain.FeeDetailStatusConfirmed ||
		s == domain.FeeDetailStatusRejected || s == domain.FeeDetailStatusUnmatched
}

// detailAmountCents accepts either the yuan form (the human form, two
// decimal places, float-to-int via rounding — NEVER truncation) or the
// cents form (the program form, exact); both set = must agree; neither =
// refusal. The integer-cents contract (#65 P1-a) starts at the door.
func detailAmountCents(in DetailInput) (int64, error) {
	if in.AmountCents != nil && in.AmountYuan != nil {
		if *in.AmountCents != yuanToCents(*in.AmountYuan) {
			return 0, fmt.Errorf("amountYuan 与 amountCents 同时给出但不相等（%v 元 ≠ %d 分）", *in.AmountYuan, *in.AmountCents)
		}
		return *in.AmountCents, nil
	}
	if in.AmountCents != nil {
		return *in.AmountCents, nil
	}
	if in.AmountYuan != nil {
		return yuanToCents(*in.AmountYuan), nil
	}
	return 0, fmt.Errorf("amountYuan 或 amountCents 必须给一个——金额是 #16 三强制之一")
}

func yuanToCents(yuan float64) int64 {
	cents := yuan * 100
	if cents < 0 {
		return int64(cents - 0.5)
	}
	return int64(cents + 0.5)
}
