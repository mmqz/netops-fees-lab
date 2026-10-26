package fees

// riskmap.go — issue #18: the site risk map's data face. The cloud builds
// the RENDERING capability only; the geo data itself comes from the user's
// own resource sheets (the #16 boundary applies verbatim — 经纬度数据由
// 本地资源表自带). The importer has split 经纬度 into the structured
// geo_lat/geo_lng attribute keys since migration v20, so the join is
// straight: fee clue sites → confirmed links → resource attributes.
//
// The color semantics (the issue's 目标形态): 红 = verified clues (the
// known-issues ledger — a human confirmed the anomaly), 黄 = pending
// clues (待核实 — the honest default), 绿 = linked sites with no clue.
// Dismissed clues count is reported but does not color: a dismissed clue
// is the human saying "not a problem" — the point goes back to green's
// population (pending-zero) unless other clues say otherwise.

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/repo"
)

// RiskMapPoint is one renderable site on the risk map.
type RiskMapPoint struct {
	SiteCode string `json:"siteCode"`
	// Lat/Lng are nil when the linked resource carries no coordinates —
	// such sites stay in the answer (the honest accounting: linkedCount vs
	// geoCount) but the frontend drops them from the canvas (issue
	// acceptance 2: no geo, no map).
	Lat *float64 `json:"lat"`
	Lng *float64 `json:"lng"`
	// Counts by human decision state (#119 vocabulary): Pending = nobody
	// looked yet, Verified = confirmed real, Dismissed = ruled out.
	ClueCount int `json:"clueCount"`
	Pending   int `json:"pending"`
	Verified  int `json:"verified"`
	Dismissed int `json:"dismissed"`
	// Scenarios is the distinct scenario set that produced the clues
	// (deterministic order — the scenario enum's order).
	Scenarios []string `json:"scenarios"`
	// ResourceKey names the linked resource (the popup's first line).
	ResourceKey string `json:"resourceKey"`
}

// RiskMapReport is the endpoint envelope: the renderable points (geo or
// not — the frontend filters), the honest coverage accounting, and the
// scenario list actually evaluated (the map IS the all-scenarios roll-up).
type RiskMapReport struct {
	FeeType string         `json:"feeType"`
	Year    int            `json:"year"`
	Month   int            `json:"month"`
	Points  []RiskMapPoint `json:"points"`
	// LinkedSites counts sites with a confirmed link; GeoSites the subset
	// with coordinates — linked-but-geoless sites are the map's honest gap,
	// reported instead of silently shrinking.
	LinkedSites int `json:"linkedSites"`
	GeoSites    int `json:"geoSites"`
}

// riskMapScenarios is the roll-up order (the scenario enum's own order —
// deterministic). custom-rules rides along: an enabled rule is as much a
// clue source as a preset.
var riskMapScenarios = []RiskScenario{
	RiskRetiredBilling, RiskDuplicateSettlement, RiskLedgerMismatch, RiskSpike,
	RiskOutlier, RiskUnlinked, RiskIdleBilling, RiskMissingBilling,
	RiskLedgerConsistency, RiskCustomRules,
}

// RiskMap aggregates every scenario's clues per site and joins the
// confirmed-link resource coordinates. Read-only; the engine absence
// answers 501 the same way Risks does (the honest contract).
func (s *Service) RiskMap(ctx context.Context, engine QueryEngine, q RiskQuery) (RiskMapReport, error) {
	if _, err := ParseFeeType(string(q.FeeType)); err != nil {
		return RiskMapReport{}, err
	}
	if !validQueryPeriod(q.Year, q.Month) {
		return RiskMapReport{}, fmt.Errorf("fees: 账期 %04d-%02d 不在合法范围", q.Year, q.Month)
	}
	if !engine.Available() {
		return RiskMapReport{}, ErrEngineNotBuilt
	}
	q = q.normalized()

	type siteAgg struct {
		point    RiskMapPoint
		scenSeen map[string]struct{}
	}
	bySite := make(map[string]*siteAgg)
	get := func(code string) *siteAgg {
		a, ok := bySite[code]
		if !ok {
			a = &siteAgg{point: RiskMapPoint{SiteCode: code}, scenSeen: map[string]struct{}{}}
			bySite[code] = a
		}
		return a
	}

	for _, sc := range riskMapScenarios {
		res, err := s.Risks(ctx, engine, sc, q)
		if err != nil {
			// A scenario that cannot run on this data shape answers its own
			// error; the roll-up reports it rather than half-silencing.
			return RiskMapReport{}, fmt.Errorf("fees: 风险地图聚合 %s: %w", sc, err)
		}
		for _, c := range res.Clues {
			a := get(c.SiteCode)
			a.point.ClueCount++
			switch domain.ClueDecisionStatus(c.Decision) {
			case domain.ClueDecisionVerified:
				a.point.Verified++
			case domain.ClueDecisionDismissed:
				a.point.Dismissed++
			default:
				a.point.Pending++
			}
			a.scenSeen[string(sc)] = struct{}{}
		}
	}

	// The GREEN population needs the confirmed-link universe too (sites
	// with no clue at all still deserve a dot) — one list call, memoized
	// per-code lookups follow the #85 discipline.
	links, err := s.db.FeeSiteLinks.List(ctx, repo.FeeLinkFilter{Status: domain.LinkStatusConfirmed})
	if err != nil {
		return RiskMapReport{}, fmt.Errorf("fees: 风险地图列关联: %w", err)
	}
	type resGeo struct {
		ok  bool
		key string
		lat *float64
		lng *float64
	}
	resMemo := make(map[string]resGeo, len(links))
	for _, link := range links {
		a := get(link.FeeSiteCode)
		if _, seen := resMemo[link.ResourceID]; !seen {
			rg := resGeo{}
			if res, rerr := s.db.Resources.GetByID(ctx, link.ResourceID); rerr == nil {
				rg.ok = true
				rg.key = res.NaturalKey
				rg.lat, rg.lng = resourceCoords(res)
			}
			resMemo[link.ResourceID] = rg
		}
		rg := resMemo[link.ResourceID]
		if rg.ok {
			a.point.ResourceKey = rg.key
			a.point.Lat = rg.lat
			a.point.Lng = rg.lng
		}
	}

	report := RiskMapReport{FeeType: string(q.FeeType), Year: q.Year, Month: q.Month, Points: []RiskMapPoint{}}
	for _, a := range bySite {
		scens := make([]string, 0, len(a.scenSeen))
		for sc := range a.scenSeen {
			scens = append(scens, sc)
		}
		sort.Strings(scens)
		a.point.Scenarios = scens
		if a.point.ResourceKey != "" {
			report.LinkedSites++
			if a.point.Lat != nil {
				report.GeoSites++
			}
		}
		report.Points = append(report.Points, a.point)
	}
	// deterministic order: site code asc — same data, same answer (#18
	// acceptance 4).
	sort.Slice(report.Points, func(i, j int) bool {
		return report.Points[i].SiteCode < report.Points[j].SiteCode
	})
	return report, nil
}

// resourceCoords reads the structured geo_lat/geo_lng attribute keys
// (migration v20 splits the combined 经纬度 column at import). Absent or
// unparseable values answer nil — never a guessed zero.
func resourceCoords(res domain.Resource) (*float64, *float64) {
	latS, latOK := res.Attributes["geo_lat"]
	lngS, lngOK := res.Attributes["geo_lng"]
	if !latOK || !lngOK {
		return nil, nil
	}
	lat, err1 := strconv.ParseFloat(trimAttrNum(latS), 64)
	lng, err2 := strconv.ParseFloat(trimAttrNum(lngS), 64)
	if err1 != nil || err2 != nil {
		return nil, nil
	}
	return &lat, &lng
}

// trimAttrNum strips the unit suffixes resource sheets sometimes carry
// (北纬/东经 markers, trailing commas); the importer's own split keeps
// plain numbers, but the map must not break on a hand-mended sheet.
func trimAttrNum(s string) string {
	for _, cut := range []string{"北纬", "东经", "°", "度"} {
		s = strings.ReplaceAll(s, cut, "")
	}
	return strings.TrimSpace(s)
}
