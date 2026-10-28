package fees

// crosscheck.go — issue #12: the cross-dataset reconcile (甲方核对乙方).
// The two systems name the same physical site differently; the alias
// bridge (migration v39) redirects each universe's rows onto the
// canonical code before the two totals presets are merged. The merge
// itself is a PURE function (same-input-same-output, table-testable);
// the scan PROPOSES (read-only — a human confirms by creating the alias
// through the audited channel, never auto-written); the import-time
// data-volume guard rides the existing warnings channel (只提示不阻断
// — the #12 实测 story: two tower months at 1 row vs 400+ neighbors).

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/repo"
)

// ---- alias channel ----

// SiteCodeAliasInput is the manual creation form (the 人工确认 channel).
type SiteCodeAliasInput struct {
	Canonical  string `json:"canonical"`
	Alias      string `json:"alias"`
	Source     string `json:"source"`
	Confidence string `json:"confidence"`
}

// AddSiteCodeAlias validates and inserts one mapping row. The actor is
// the acceptance-5 audit (谁建立); scan proposals land here too — the
// scan never writes, the human's confirm is this very call.
func (s *Service) AddSiteCodeAlias(ctx context.Context, in SiteCodeAliasInput, actor string) (domain.SiteCodeAlias, error) {
	canonical := strings.TrimSpace(in.Canonical)
	alias := strings.TrimSpace(in.Alias)
	if canonical == "" || alias == "" {
		return domain.SiteCodeAlias{}, refusef("canonical 与 alias 都不能为空——映射表的两端就是它的全部职责")
	}
	if canonical == alias {
		return domain.SiteCodeAlias{}, refusef("canonical 与 alias 相同（%q）——同一编码无需映射", canonical)
	}
	if len([]rune(canonical)) > 128 || len([]rune(alias)) > 128 {
		return domain.SiteCodeAlias{}, refusef("canonical/alias 过长（最多 128 字）")
	}
	var source domain.FeeType
	if in.Source == "manual" {
		source = domain.FeeType("manual")
	} else {
		ft, err := ParseFeeType(in.Source)
		if err != nil {
			return domain.SiteCodeAlias{}, refusef("source 必须是 %v 之一或 manual，收到 %q", append(FeeTypes(), "manual"), in.Source)
		}
		source = ft
	}
	if in.Confidence != "exact" && in.Confidence != "rule" && in.Confidence != "manual" {
		return domain.SiteCodeAlias{}, refusef("confidence 必须是 exact/rule/manual 之一，收到 %q", in.Confidence)
	}
	if actor == "" {
		return domain.SiteCodeAlias{}, refusef("建立人（actor）不能为空——映射关系可审计（#12 验收 5：谁在何时建立）")
	}
	a := domain.SiteCodeAlias{
		ID: domain.NewID("fca"), Canonical: canonical, Alias: alias,
		Source: source, Confidence: in.Confidence, CreatedBy: actor, CreatedAt: s.clock(),
	}
	if err := s.db.Do(ctx, func(tx *repo.Tx) error {
		if existing, err := tx.FeeSiteCodeAliases.Exists(ctx, canonical, alias, source); err != nil {
			return err
		} else if existing != "" {
			return refusef("映射已存在（%s）——canonical %q ↔ alias %q（source %s）；同一映射不重复建立", existing, canonical, alias, in.Source)
		}
		return tx.FeeSiteCodeAliases.Insert(ctx, a)
	}); err != nil {
		return domain.SiteCodeAlias{}, err
	}
	return a, nil
}

// ListSiteCodeAliases answers the whole registry (read-open — the bridge
// is evidence).
func (s *Service) ListSiteCodeAliases(ctx context.Context) ([]domain.SiteCodeAlias, error) {
	return s.db.FeeSiteCodeAliases.List(ctx)
}

// RemoveSiteCodeAlias is the admin-tier governance act (a wrong mapping
// must not poison the reconcile forever; removal is deliberate).
func (s *Service) RemoveSiteCodeAlias(ctx context.Context, id string) error {
	return s.db.Do(ctx, func(tx *repo.Tx) error {
		return tx.FeeSiteCodeAliases.Delete(ctx, id)
	})
}

// ---- the read-only scan (proposals, never writes) ----

// AliasProposal is one scan candidate: two codes from different
// universes whose normalized skeletons match — created only by the
// human confirm channel (AddSiteCodeAlias).
type AliasProposal struct {
	Canonical string `json:"canonical"`
	Alias     string `json:"alias"`
	Source    string `json:"source"` // the alias's universe
	Why       string `json:"why"`
}

// ProposeSiteCodeAliases compares the two imported universes' site-code
// sets and proposes the same-skeleton pairs (uppercase alphanumerics
// equal, literals different — the 双系统编码不统一 shape of the 实测
// 3 站). Read-only: nothing lands without the human's confirm.
func (s *Service) ProposeSiteCodeAliases(ctx context.Context, engine QueryEngine) ([]AliasProposal, error) {
	if !engine.Available() {
		return nil, ErrEngineNotBuilt
	}
	elecCodes, err := s.universeCodes(ctx, engine, domain.FeeTypeElectricity)
	if err != nil {
		return nil, err
	}
	towerCodes, err := s.universeCodes(ctx, engine, domain.FeeTypeTowerLease)
	if err != nil {
		return nil, err
	}
	// the existing registry is honored: a pair already mapped is not
	// re-proposed.
	aliases, err := s.db.FeeSiteCodeAliases.List(ctx)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, a := range aliases {
		known[a.Canonical+"\x00"+a.Alias+"\x00"+string(a.Source)] = true
	}
	proposals := []AliasProposal{}
	towerSkeletons := map[string]string{} // skeleton -> code (first wins, conflicts skipped honestly)
	for _, c := range towerCodes {
		sk := codeSkeleton(c)
		if _, clash := towerSkeletons[sk]; !clash {
			towerSkeletons[sk] = c
		}
	}
	for _, e := range elecCodes {
		if c, ok := towerSkeletons[codeSkeleton(e)]; ok && c != e {
			if !known[e+"\x00"+c+"\x00"+string(domain.FeeTypeTowerLease)] {
				proposals = append(proposals, AliasProposal{
					Canonical: e, Alias: c, Source: string(domain.FeeTypeTowerLease),
					Why: fmt.Sprintf("两库编码骨架相同（%s ↔ %s）——疑为同一物理站的两套编码（#12 实测形态），人工确认后建立映射", e, c),
				})
			}
		}
	}
	return proposals, nil
}

// universeCodes answers the distinct site codes of one fee type's
// analysis layer (the whole CURRENT year — the universe is the
// universe; the proposal list is a UI aid, not a ledger, so the clock
// read here never touches the ledger's determinism).
func (s *Service) universeCodes(ctx context.Context, engine QueryEngine, ft domain.FeeType) ([]string, error) {
	rows, err := engine.Totals(ctx, s.analysisDir(), ft, s.clock().Year(), 0, 500)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.SiteCode)
	}
	return out, nil
}

// codeSkeleton normalizes a site code to its comparison skeleton:
// uppercase, only letters and digits kept (前缀/连字符/下划线等全部噪声
// 剥掉), and the LEADING ZEROS OF EACH DIGIT RUN stripped — zero-padding
// is the classic dual-system divergence ("HLJ-0001" vs "hlj001" is the
// 实测 shape; "AB01" vs "AB10" stays distinct because 10 carries no
// leading zero). A run of only zeros vanishes ("S000" ≡ "S") — the scan
// is a proposal pass, the human confirms.
func codeSkeleton(code string) string {
	var b strings.Builder
	inRun, runStarted := false, false
	for _, r := range strings.ToUpper(code) {
		switch {
		case unicode.IsLetter(r):
			b.WriteRune(r)
			inRun, runStarted = false, false
		case unicode.IsDigit(r):
			if !inRun {
				inRun, runStarted = true, false
			}
			if r == '0' && !runStarted {
				continue // the run's leading zeros
			}
			runStarted = true
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return strings.ToUpper(code) // an all-noise code keeps its literal
	}
	return b.String()
}

// ---- the cross-dataset reconcile (pure merge + composition) ----

// CrossClass is the three-way presence classification of the #12
// acceptance 3: both / only the electricity ledger / only the tower
// detail.
type CrossClass string

const (
	CrossBoth            CrossClass = "both"
	CrossOnlyElectricity CrossClass = "only_electricity"
	CrossOnlyTower       CrossClass = "only_tower"
)

// CrossRow is one canonical site's reconcile line.
type CrossRow struct {
	Canonical        string `json:"canonical"`
	Class            string `json:"class"`
	ElectricityCents int64  `json:"electricityCents"`
	TowerCents       int64  `json:"towerCents"`
	DeltaCents       int64  `json:"deltaCents"`
	ElecRows         int    `json:"elecRows"`
	TowerRows        int    `json:"towerRows"`
	// ZeroSide (#31): "electricity" or "tower" when BOTH sides carry rows
	// but one side's total is 0 — a presence-with-zero-cents row, distinct
	// from the class-mates whose zero side carries NO rows at all. The
	// classify itself is row-count-based (both vs only-either); this marker
	// tells the operator WHICH side to suspect for the amount, so a
	// wrong-column import (#29) reads as "钱列可疑" instead of "编码映射可疑"
	// — the two lead to completely different audits.
	ZeroSide       string `json:"zeroSide,omitempty"`
	RedirectedFrom string `json:"redirectedFrom,omitempty"` // the alias trail of this line
	// Attribution (#38) is the row's WHY — the master-data judgment the
	// reconcile reads off the confirmed link's resource (站址性质/供电方式/
	// 生命周期, migration v40) plus the alias-proposal face. Closed token set
	// (crossattribution.go owns the decision order): shared_no_power_needed /
	// self_built_no_tower_needed / retired_but_billed / power_should_exist /
	// code_mismatch / unknown. both rows carry none (omitempty — no
	// difference to explain); unknown means master data is MISSING and says
	// so — never a silent "normal" (the issue's own 洗白 warning).
	Attribution string `json:"attribution,omitempty"`
}

// CrossReconcileResult is the envelope: the merged table (anomaly-first
// order), the class counts and the doctrine note. Warnings (#31) carries
// the side-level health notices — a side whose every present site totals
// 0 (or a side with no rows at all) makes the DELTA column meaningless
// and the envelope must say so, the #21 channel discipline.
type CrossReconcileResult struct {
	Year            int        `json:"year"`
	Month           int        `json:"month"`
	Rows            []CrossRow `json:"rows"`
	OnlyElectricity int        `json:"onlyElectricity"`
	OnlyTower       int        `json:"onlyTower"`
	Both            int        `json:"both"`
	AliasesApplied  int        `json:"aliasesApplied"`
	Note            string     `json:"note"`
	Warnings        []string   `json:"warnings,omitempty"`
}

// crossMerge is the PURE reconcile: two per-site totals presets + the two
// universes' alias maps → one canonical table. Same input, same output
// (table-testable, the #3 display doctrine).
func crossMerge(year, month int, elec, tower []TotalsRow, elecAliases, towerAliases map[string]string) CrossReconcileResult {
	type agg struct {
		elecCents, towerCents int64
		elecRows, towerRows   int
		redirects             []string
	}
	byCanonical := map[string]*agg{}
	apply := func(rows []TotalsRow, aliases map[string]string, elecSide bool) {
		for _, r := range rows {
			key := r.SiteCode
			if canonical, ok := aliases[r.SiteCode]; ok {
				key = canonical
			}
			a := byCanonical[key]
			if a == nil {
				a = &agg{}
				byCanonical[key] = a
			}
			if elecSide {
				a.elecCents += r.TotalCents
				a.elecRows += r.Rows
			} else {
				a.towerCents += r.TotalCents
				a.towerRows += r.Rows
			}
			if key != r.SiteCode {
				a.redirects = append(a.redirects, r.SiteCode+"→"+key)
			}
		}
	}
	apply(elec, elecAliases, true)
	apply(tower, towerAliases, false)

	res := CrossReconcileResult{Year: year, Month: month, Rows: []CrossRow{}}
	for canonical, a := range byCanonical {
		row := CrossRow{
			Canonical: canonical, ElectricityCents: a.elecCents, TowerCents: a.towerCents,
			DeltaCents: a.elecCents - a.towerCents, ElecRows: a.elecRows, TowerRows: a.towerRows,
		}
		if len(a.redirects) > 0 {
			row.RedirectedFrom = strings.Join(a.redirects, " ")
		}
		switch {
		case a.elecRows > 0 && a.towerRows > 0:
			row.Class = string(CrossBoth)
			// #31: rows on both sides but one side totals 0 — a
			// presence-with-zero-cents row. NOT reclassified (the site
			// IS both systems'), but marked: the operator audits the
			// AMOUNT column of the zero side, never the code mapping.
			if a.elecCents == 0 && a.towerCents == 0 {
				row.ZeroSide = "electricity+tower"
			} else if a.elecCents == 0 {
				row.ZeroSide = "electricity"
			} else if a.towerCents == 0 {
				row.ZeroSide = "tower"
			}
			res.Both++
		case a.elecRows > 0:
			row.Class = string(CrossOnlyElectricity)
			res.OnlyElectricity++
		default:
			row.Class = string(CrossOnlyTower)
			res.OnlyTower++
		}
		res.Rows = append(res.Rows, row)
	}
	// deterministic order: anomaly-first (only_electricity — the
	// hunting target), then only_tower, then both; ties by canonical.
	sortCrossRows(res.Rows)
	return res
}

func sortCrossRows(rows []CrossRow) {
	clsRank := map[string]int{string(CrossOnlyElectricity): 0, string(CrossOnlyTower): 1, string(CrossBoth): 2}
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0; j-- {
			a, b := rows[j-1], rows[j]
			ra, rb := clsRank[a.Class], clsRank[b.Class]
			swap := ra > rb || (ra == rb && a.Canonical > b.Canonical)
			if !swap {
				break
			}
			rows[j-1], rows[j] = b, a
		}
	}
}

// CrossReconcile runs the merge for one period (issue #12 acceptance
// 2/3; #31 re-worked the input face).
func (s *Service) CrossReconcile(ctx context.Context, engine QueryEngine, year, month int) (CrossReconcileResult, error) {
	if !validQueryPeriod(year, month) {
		return CrossReconcileResult{}, fmt.Errorf("fees: 账期 %04d-%02d 不在合法范围", year, month)
	}
	if !engine.Available() {
		return CrossReconcileResult{}, ErrEngineNotBuilt
	}
	// #31: the UNIVERSE primitive, not the display Totals. The old input
	// (Totals, top-500 by total DESC) truncated the zero-amount tail of a
	// 500+ site side — a tower sheet whose amounts all landed 0 (the #29
	// wrong-column import) lost its 0-cent sites past the 500th row and
	// every electricity-present site classified only_electricity: presence
	// read as absence, the delta column degenerated to the electricity
	// re-pricing, and the envelope said NOTHING. The universe answers
	// EVERY site that has rows, 0 cents included.
	elec, err := engine.CrossUniverseTotals(ctx, s.analysisDir(), domain.FeeTypeElectricity, year, month)
	if err != nil {
		return CrossReconcileResult{}, err
	}
	tower, err := engine.CrossUniverseTotals(ctx, s.analysisDir(), domain.FeeTypeTowerLease, year, month)
	if err != nil {
		return CrossReconcileResult{}, err
	}
	elecAliases, err := s.db.FeeSiteCodeAliases.AliasMap(ctx, domain.FeeTypeElectricity)
	if err != nil {
		return CrossReconcileResult{}, err
	}
	towerAliases, err := s.db.FeeSiteCodeAliases.AliasMap(ctx, domain.FeeTypeTowerLease)
	if err != nil {
		return CrossReconcileResult{}, err
	}
	applied := len(elecAliases) + len(towerAliases)
	res := crossMerge(year, month, elec, tower, elecAliases, towerAliases)
	res.AliasesApplied = applied
	// #38: attribute every only_* row — the structured master data through
	// the confirmed link (hard answers) plus the alias-proposal face (hints);
	// a row with neither answers unknown and the envelope warns with the
	// count (never a silent all-clear). Hard master data outranks hints: a
	// retired site's row attributes retired_but_billed even when a fuzzy
	// proposal also exists.
	master, err := s.fetchSiteMaster(ctx, collectAttributionCodes(res.Rows))
	if err != nil {
		return CrossReconcileResult{}, err
	}
	proposals, err := s.proposalCoverage(ctx, engine)
	if err != nil {
		return CrossReconcileResult{}, err
	}
	unknown := attributeCrossRows(res.Rows, master, proposals)
	res.Note = "对账表是待核实差异而非认定结论：只在电费台账=疑似自建站或编码不一致；只在塔表=多为共享站（正常）或电费在别处；差额方向=电费−塔租；双方都有但一侧金额为 0（zeroSide 标记）=先查该侧金额列是否导入正确，不是编码映射问题。映射经人工确认审计通道建立。" + attributionNoteAppendix
	res.Warnings = crossSideHealthWarnings(res)
	if unknown > 0 {
		res.Warnings = append(res.Warnings, attributionUnknownWarning(unknown))
	}
	return res, nil
}

// crossSideHealthWarnings answers the #31 envelope-level notices: a side
// whose EVERY present site totals 0 carries no money information — the
// delta column degenerates to the OTHER side's full amounts and every
// class reading is unreliable; a side with NO rows at all is the
// not-imported shape (its only_either mates are un-auditable by
// construction). Both shapes get their warning; the healthy table stays
// silent (a warning on good data is its own noise).
func crossSideHealthWarnings(res CrossReconcileResult) []string {
	type sideStats struct {
		present, nonZero int
	}
	elec, tower := sideStats{}, sideStats{}
	for _, r := range res.Rows {
		if r.ElecRows > 0 {
			elec.present++
			if r.ElectricityCents != 0 {
				elec.nonZero++
			}
		}
		if r.TowerRows > 0 {
			tower.present++
			if r.TowerCents != 0 {
				tower.nonZero++
			}
		}
	}
	var warnings []string
	if elec.present > 0 && elec.nonZero == 0 {
		warnings = append(warnings, fmt.Sprintf(
			"电费侧有记录的 %d 站金额全部为 0（0 个非零）——差额=塔租全额不具参考价值；金额全零极可能是金额列未正确识别（#29 形态），请核对导入映射后重导",
			elec.present))
	}
	if tower.present > 0 && tower.nonZero == 0 {
		warnings = append(warnings, fmt.Sprintf(
			"塔租侧有记录的 %d 站金额全部为 0（0 个非零）——差额=电费全额不具参考价值；金额全零极可能是金额列未正确识别（#29 形态），请核对导入映射后重导",
			tower.present))
	}
	if elec.present == 0 {
		warnings = append(warnings, "电费侧该账期无任何记录——对账前提不成立，only_tower 分类不具排查意义（电费台账未导入该账期？）")
	}
	if tower.present == 0 {
		warnings = append(warnings, "塔租侧该账期无任何记录——对账前提不成立，only_electricity 分类不具排查意义（塔类详单未导入该账期？）")
	}
	return warnings
}

// ---- the import-time data-volume guard (#12 acceptance 4) ----

// rowsAnomalyThreshold: this month < 20% of last month AND last month ≥
// 10 rows (the floor keeps single-site ledgers out of the noise).
const (
	rowsAnomalyRatio = 0.2
	rowsAnomalyFloor = 10
)

// importRowAnomalyWarn answers the data-volume warning of one fresh
// import (#12: 塔表 9 个月中 2 个月只有 1 行 vs 邻月 400+——百万级缺口的
// 目视形态). nil when the guard has nothing to say.
func (s *Service) importRowAnomalyWarn(ctx context.Context, feeType domain.FeeType, year, month, rowsImported int) []string {
	prevY, prevM := year, month-1
	if prevM == 0 {
		prevM = 12
		prevY--
	}
	if prevY < 2000 {
		return nil
	}
	prevRows, err := s.db.FeeBatches.RowsImportedOfPeriod(ctx, feeType, prevY, prevM)
	if err != nil || prevRows < rowsAnomalyFloor {
		return nil // no honest baseline = no comparison to make
	}
	if float64(rowsImported) < float64(prevRows)*rowsAnomalyRatio {
		pct := float64(rowsImported) / float64(prevRows) * 100
		return []string{fmt.Sprintf(
			"本月导入 %d 行，仅为上月（%d 行）的 %.1f%%——低于 20%% 骤降线：请确认源文件是否完整（#12 实测形态：塔表个别月份仅 1 行 vs 邻月 400+，按邻月估算可能少算百万元级）",
			rowsImported, prevRows, pct)}
	}
	return nil
}
