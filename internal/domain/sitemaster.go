package domain

// sitemaster.go — the site master-data fields of issue #38: the three
// structured lifecycle answers a cross-system reconcile needs to move
// from "列出差异" to "给出判断".
//
// The 实测 shape the issue was born from: 36% of the sites in the tower
// settlement sheet have NO electricity record — and the system could only
// LIST them (only_tower), never CLASSIFY them. The five business
// realities behind a "tower rent but no electricity" row:
//
//   1. 共享站——铁塔方提供电力          → 正常（无需处理）
//   2. 站址已退网——仍在被计费          → 风险（应追回）
//   3. 电费由他方承担                  → 正常
//   4. 电费台账漏登                    → 风险（应补记）
//   5. 两系统编码不一致                → 需映射
//
// Which of the five a row is cannot be read off the fee rows themselves —
// it rides on WHO the site is (nature / power supply / lifecycle). Those
// three answers are master data: they exist in the operator's systems
// (and even inside the tower sheet's own unmapped columns, see #39's
// raw-row read-back), they just never had a structured channel into this
// system. Attributes-JSON is free text (unqueryable, unverifiable); these
// are the geo_lat/geo_lng-precedent REAL columns: typed, CHECK-
// constrained, and joinable by the reconcile.
//
// Token discipline: the COLUMNS carry the English tokens (self_built /
// shared / leased, direct / transfer / none, active / retired /
// dismantled) — the API and reconcile vocabulary; the SHEET values the
// importer recognizes are the Chinese synonyms (自建/共享/租用, 直供/转供/
// 无, 在网/退网/已拆). The enum doctrine is the status field's own: an
// in-set value lives ONLY in the structured column (st.attributes skips
// KindEnum), an out-of-set value is a WARNING (RuleUnknownStatusValue)
// and lands nowhere — the row imports, the column stays NULL, never a
// row failure and never a silent guess.

// SiteNature is the 站址性质 answer: who owns the site.
type SiteNature string

const (
	SiteNatureSelfBuilt SiteNature = "self_built" // 自建
	SiteNatureShared    SiteNature = "shared"     // 共享
	SiteNatureLeased    SiteNature = "leased"     // 租用
)

// PowerMode is the 供电方式 answer: whether the site draws electricity
// through this operator's bill at all.
type PowerMode string

const (
	PowerModeDirect   PowerMode = "direct"   // 直供
	PowerModeTransfer PowerMode = "transfer" // 转供
	PowerModeNone     PowerMode = "none"     // 无（铁塔方供电/他方承担——本侧不应有电费）
)

// Lifecycle is the 生命周期 answer: whether the site should still be
// billed anything at all.
type Lifecycle string

const (
	LifecycleActive     Lifecycle = "active"     // 在网
	LifecycleRetired    Lifecycle = "retired"    // 退网
	LifecycleDismantled Lifecycle = "dismantled" // 已拆
)

// ParseSiteNatureValue normalizes one raw sheet value to the column token.
// ok=false means the value is outside the closed set — the caller warns
// and leaves the column NULL (never guesses).
func ParseSiteNatureValue(raw string) (SiteNature, bool) {
	switch raw {
	case "自建":
		return SiteNatureSelfBuilt, true
	case "共享":
		return SiteNatureShared, true
	case "租用":
		return SiteNatureLeased, true
	case "self_built":
		return SiteNatureSelfBuilt, true
	case "shared":
		return SiteNatureShared, true
	case "leased":
		return SiteNatureLeased, true
	}
	return "", false
}

// ParsePowerModeValue normalizes one raw sheet value to the column token.
func ParsePowerModeValue(raw string) (PowerMode, bool) {
	switch raw {
	case "直供":
		return PowerModeDirect, true
	case "转供":
		return PowerModeTransfer, true
	case "无":
		return PowerModeNone, true
	case "direct":
		return PowerModeDirect, true
	case "transfer":
		return PowerModeTransfer, true
	case "none":
		return PowerModeNone, true
	}
	return "", false
}

// ParseLifecycleValue normalizes one raw sheet value to the column token.
func ParseLifecycleValue(raw string) (Lifecycle, bool) {
	switch raw {
	case "在网":
		return LifecycleActive, true
	case "退网":
		return LifecycleRetired, true
	case "已拆":
		return LifecycleDismantled, true
	case "active":
		return LifecycleActive, true
	case "retired":
		return LifecycleRetired, true
	case "dismantled":
		return LifecycleDismantled, true
	}
	return "", false
}
