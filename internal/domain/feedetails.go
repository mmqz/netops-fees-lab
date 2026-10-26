package domain

import "time"

// FeeDetail is one manually entered detail row of the evidence tier
// (migration v38, #16's completion line item 6): the layer that explains
// WHERE a system-table amount went — corroboration, never authority.
// The three #16 mandates are the NOT NULL spine: the link key (whose
// money — a site code or a staff code), the amount (integer cents,
// negative = write-off) and the provenance (created_by/created_at —
// 人工数据必须有来源痕迹). Everything else is optional by design:
// 系统提供「表单能力」，不规定必须填哪些可选字段.
type FeeDetail struct {
	ID             string
	LinkKey        string // 站址编码或工号（这笔钱属于谁）
	Period         string // YYYYMM（哪个月）
	FeeType        FeeType
	Category       string // 建议集 + 本地自建（#16：不强制闭集，只强制非空）
	AmountCents    int64  // 整数分契约（#65 P1-a）；负数 = 冲销
	OccurredOn     string
	ItemName       string
	Quantity       *float64
	QuantityUnit   string
	UnitPriceCents *int64
	Reason         string
	Location       string
	WorkOrderNo    string
	InvoiceNo      string
	Counterparty   string
	SourceFile     string
	SourceRow      int
	Remark         string
	Status         string // pending | confirmed | rejected | unmatched
	CreatedBy      string
	CreatedAt      time.Time
	VerifiedBy     string
	VerifiedAt     *time.Time
	RejectReason   string
}

// The fee-detail status flow (the spec §3.4 human channel): entries land
// pending; a reviewer confirms or rejects (with a reason); unmatched is a
// human marking, the system never flips a status silently.
const (
	FeeDetailStatusPending   = "pending"
	FeeDetailStatusConfirmed = "confirmed"
	FeeDetailStatusRejected  = "rejected"
	FeeDetailStatusUnmatched = "unmatched"
)

// FeeDetailDetailCategories are the TWELVE suggested categories from the
// real-data induction (docs/fee-detail-schema.md §3.2) — a SUGGESTION
// SET, not a closed enum: #16 rules "系统提供「分类建议」，但允许本地
// 自建分类". The UI pre-fills these; the backend accepts any non-empty
// category.
var FeeDetailSuggestedCategories = []string{
	"划转纤芯绩效", "计件工资", "加班餐补", "住宿费", "低值易耗品",
	"维修工器具", "油费", "车辆维修", "资质培训", "线路改迁", "绩效分配", "冲销",
}

// FeeDetailFilter is the list query shape: every field optional (empty =
// no constraint), the link triple narrows to one key's story.
type FeeDetailFilter struct {
	LinkKey  string
	Period   string
	FeeType  FeeType
	Status   string
	Category string
}
