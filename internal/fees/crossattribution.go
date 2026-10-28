package fees

// crossattribution.go — #38's 带判定的三分类: the attribution half of the
// cross-dataset reconcile. The three-way classification (only_electricity /
// only_tower / both) LISTED the differences; the attribution answers WHY
// each only_* row exists — from the site master data (站址性质/供电方式/
// 生命周期, the migration-v40 structured columns) plus the alias-proposal
// face of the code universe.
//
// The closed token set (the issue's five + the mirror of the shared
// case — a self-built site with no tower rent is the one reading the
// master data can CONFIRM for an only_electricity row; hiding it behind
// unknown would be its own dishonesty):
//
//	shared_no_power_needed   共享站且无供电        → only_tower 正常
//	self_built_no_tower_needed 自建站（无塔租）    → only_electricity 正常
//	retired_but_billed       已退网/已拆仍计费     → 风险（应追回）
//	power_should_exist       应供电但电费无记录    → 漏登风险（应补记）
//	code_mismatch            疑两系统编码不一致    → 需映射（提案已存在）
//	unknown                  缺站址主数据          → 无法判断（不是正常）
//
// Discipline (the issue's own 洗白 warning): a row WITHOUT master data is
// unknown and SAYS so — never a silent "normal" — and the envelope warns
// with the count so a wall of unknowns cannot dress up as an all-clear.
// Hard master data beats the heuristic proposals: a retired site's row
// attributes retired_but_billed even when a fuzzy proposal also exists
// (the proposal is a hint; the structured field is an answer).
//
// The decision core (attributeCrossRows) is a pure function — the five
// acceptance branches are table-pinned in crossattribution_test.go; the
// lookups (fetchSiteMaster over confirmed links, proposalCoverage over the
// engine's alias proposals) stay in the thin assembly half.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// The attribution tokens (the wire vocabulary; api.md documents them).
const (
	AttributionSharedNoPowerNeeded = "shared_no_power_needed"
	AttributionSelfBuiltNoTower    = "self_built_no_tower_needed"
	AttributionRetiredButBilled    = "retired_but_billed"
	AttributionPowerShouldExist    = "power_should_exist"
	AttributionCodeMismatch        = "code_mismatch"
	AttributionUnknown             = "unknown"
)

// siteMasterAnswer is one canonical code's master-data lookup: ok=false
// means no confirmed link to a resource (or the linked resource carries
// none of the three fields — same honest state, the fields are what the
// attribution reads).
type siteMasterAnswer struct {
	ok        bool
	nature    string
	power     string
	lifecycle string
}

// collectAttributionCodes answers the distinct codes the attribution
// needs master data for: every only_* row's canonical plus the original
// codes of its alias trails (a redirected row's confirmed link may hang
// off the ORIGINAL code, not the canonical).
func collectAttributionCodes(rows []CrossRow) []string {
	seen := map[string]bool{}
	var codes []string
	add := func(c string) {
		if c == "" || seen[c] {
			return
		}
		seen[c] = true
		codes = append(codes, c)
	}
	for _, r := range rows {
		if r.Class == string(CrossBoth) {
			continue // no difference to attribute
		}
		add(r.Canonical)
		for _, field := range strings.Fields(r.RedirectedFrom) {
			if i := strings.Index(field, "→"); i > 0 {
				add(field[:i])
			}
		}
	}
	return codes
}

// attributeCrossRows sets the Attribution of every only_* row and answers
// the count of rows that stayed unknown (the honest-missing face the
// envelope warns about). both rows carry no attribution (omitempty) —
// there is no difference to explain.
func attributeCrossRows(rows []CrossRow, master map[string]siteMasterAnswer, proposals map[string]bool) int {
	unknown := 0
	for i := range rows {
		r := &rows[i]
		switch r.Class {
		case string(CrossOnlyTower):
			r.Attribution = attributeOnlyTower(masterAnswerFor(master, *r), proposalCovers(proposals, *r))
		case string(CrossOnlyElectricity):
			r.Attribution = attributeOnlyElectricity(masterAnswerFor(master, *r), proposalCovers(proposals, *r))
		default:
			continue
		}
		if r.Attribution == AttributionUnknown {
			unknown++
		}
	}
	return unknown
}

// masterAnswerFor reads the row's master-data answer: the canonical's
// link first, then the alias trail's originals (the confirmed link of a
// redirected row hangs off the code that was linked, whichever spelling).
func masterAnswerFor(master map[string]siteMasterAnswer, r CrossRow) siteMasterAnswer {
	if a, ok := master[r.Canonical]; ok && a.ok {
		return a
	}
	for _, field := range strings.Fields(r.RedirectedFrom) {
		if i := strings.Index(field, "→"); i > 0 {
			if a, ok := master[field[:i]]; ok && a.ok {
				return a
			}
		}
	}
	return siteMasterAnswer{}
}

// proposalCovers reports whether any of the row's code spellings sits in
// the alias-proposal set (the graded matcher's pending exact/rule
// suggestion — a code_mismatch trace that is one human confirm away).
func proposalCovers(proposals map[string]bool, r CrossRow) bool {
	if proposals[r.Canonical] {
		return true
	}
	for _, field := range strings.Fields(r.RedirectedFrom) {
		if i := strings.Index(field, "→"); i > 0 {
			if proposals[field[:i]] {
				return true
			}
		}
	}
	return false
}

// attributeOnlyTower — a "tower rent but no electricity" row. The five
// business realities of the issue's §3.1, decided in the order of
// evidence strength:
//
//  1. lifecycle retired/dismantled → retired_but_billed (hard answer:
//     the site should not be billed AT ALL — a refund target);
//  2. shared + power none → shared_no_power_needed (the 铁塔方提供电力
//     reading: normal, no electricity bill expected on this side);
//  3. power direct/transfer → power_should_exist (this side SHOULD have
//     an electricity record — a 漏登 risk);
//  4. an alias proposal covers the code → code_mismatch (the missing
//     electricity twin exists under another spelling — build the map);
//  5. else → unknown (no master data, no hint: 无法判断, never "normal").
func attributeOnlyTower(m siteMasterAnswer, proposalCovered bool) string {
	if m.ok {
		switch {
		case m.lifecycle == "retired" || m.lifecycle == "dismantled":
			return AttributionRetiredButBilled
		case m.nature == "shared" && m.power == "none":
			return AttributionSharedNoPowerNeeded
		case m.power == "direct" || m.power == "transfer":
			return AttributionPowerShouldExist
		}
	}
	if proposalCovered {
		return AttributionCodeMismatch
	}
	return AttributionUnknown
}

// attributeOnlyElectricity — the mirror: "electricity but no tower rent".
func attributeOnlyElectricity(m siteMasterAnswer, proposalCovered bool) string {
	if m.ok {
		switch {
		case m.lifecycle == "retired" || m.lifecycle == "dismantled":
			return AttributionRetiredButBilled
		case m.nature == "self_built":
			return AttributionSelfBuiltNoTower
		}
	}
	if proposalCovered {
		return AttributionCodeMismatch
	}
	return AttributionUnknown
}

// fetchSiteMaster resolves the confirmed link + resource master data for
// each code (memoized per call; the retired-billing scenario's #85 memo
// pattern). A code without a confirmed link answers ok=false — that is a
// BUSINESS state, not an error. Real DB failures abort the report.
func (s *Service) fetchSiteMaster(ctx context.Context, codes []string) (map[string]siteMasterAnswer, error) {
	out := make(map[string]siteMasterAnswer, len(codes))
	for _, code := range codes {
		link, err := s.db.FeeSiteLinks.ConfirmedByCode(ctx, code)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				out[code] = siteMasterAnswer{}
				continue
			}
			return nil, fmt.Errorf("fees: 对账归因查关联 %s: %w", code, err)
		}
		res, rerr := s.db.Resources.GetByID(ctx, link.ResourceID)
		if rerr != nil {
			if errors.Is(rerr, domain.ErrNotFound) {
				out[code] = siteMasterAnswer{}
				continue
			}
			return nil, fmt.Errorf("fees: 对账归因查资源 %s: %w", link.ResourceID, rerr)
		}
		out[code] = siteMasterAnswer{
			ok:        res.SiteNature != "" || res.PowerMode != "" || res.Lifecycle != "",
			nature:    res.SiteNature,
			power:     res.PowerMode,
			lifecycle: res.Lifecycle,
		}
	}
	return out, nil
}

// proposalCoverage maps the alias-proposal face: every canonical and
// alias of every pending proposal. A code in this set has a graded-match
// twin in the other universe awaiting the human confirm channel — the
// code_mismatch trace.
func (s *Service) proposalCoverage(ctx context.Context, engine QueryEngine) (map[string]bool, error) {
	proposals, err := s.ProposeSiteCodeAliases(ctx, engine)
	if err != nil {
		// The proposal engine shares the reconcile's engine face; a real
		// failure is a report-level failure, not a per-row degradation.
		return nil, err
	}
	covered := make(map[string]bool, len(proposals)*2)
	for _, p := range proposals {
		covered[p.Canonical] = true
		covered[p.Alias] = true
	}
	return covered, nil
}

// attributionNoteAppendix is the note's #38 half: the token meanings plus
// the two readings the acceptance asks the note to keep apart — the
// attributed rows say WHY, the unknown rows say they CANNOT say.
const attributionNoteAppendix = "；归因口径（#38）：每行 only_tower / only_electricity 差异携带 attribution——shared_no_power_needed=共享站且无供电（正常）/ self_built_no_tower_needed=自建站无塔租（正常）/ retired_but_billed=已退网或已拆仍计费（风险，应追回）/ power_should_exist=应供电但电费无记录（漏登风险，应补记）/ code_mismatch=疑两系统编码不一致（存在映射提案，先人工确认映射）/ unknown=缺站址主数据（无已确认关联或资源未带站址性质/供电方式/生命周期），无法判断差异性质——不是正常。归因是主数据的结构化判读，仍非认定结论，请人工核实"

// attributionUnknownWarning names the honest-missing face with its count:
// a wall of unknowns must never dress up as "all clear" (#21 discipline).
func attributionUnknownWarning(unknown int) string {
	return fmt.Sprintf(
		"本账期 %d 行差异 attribution=unknown——缺少站址主数据，无法判断差异性质（不是正常）；请导入携带站址性质/供电方式/生命周期的站址资源表、建立关联后重跑对账",
		unknown)
}
