package metrics

// predict.go — ADR-0011 D4 (P4), the last unpaid promise of that ADR: the
// minimal honest trend forecast. The ruling is narrow ON PURPOSE and this
// file stays inside it:
//
//   - base = least-squares linear trend over the series's DAILY AVG
//     aggregation (slope / intercept / residual SE / slope SE — the four
//     numbers every stats 101 course names, nothing more);
//   - "N days to threshold": project along the catalog's Direction to the
//     matching ADVISORY bound (higher_is_better → min_value, falling is
//     the degradation; lower_is_better → max_value, rising is);
//     neutral direction, a bound that is unset, a slope that moves AWAY
//     from the bound, or a zero slope all answer days = nil — an honest
//     blank beats a fabricated number;
//   - already beyond the bound → 0 days (that is detection's business
//     today, not something a forecast gets to "predict" retroactively);
//   - 80% prediction band (normal approximation, z = 1.282; valid from
//     n = 14 up);
//   - FEWER THAN 14 DAILY POINTS REFUSES (D4's own words: 数据不足时上
//     预测是自欺) — a 422 with the honest count, never a shrunk fit;
//   - the note 恒为「外推参考，非承诺」— the panel shows it, the API
//     returns it, the MCP tool carries it. No learning models, no
//     seasonality, no promises: v1 is a ruler on a scatter plot, and it
//     says so out loud.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
)

// forecastMinPoints is the D4 minimum: 14 daily averages before any fit
// is allowed to answer. Below it the refusal is a 422-class error, not
// a degraded forecast.
const forecastMinPoints = 14

// forecastZ80 is the normal-approximation z for a two-sided 80% band.
const forecastZ80 = 1.282

// ErrForecastNotEnoughData answers a series with fewer than
// forecastMinPoints daily points. The honest count rides along.
var ErrForecastNotEnoughData = errors.New("metrics: 预测数据不足")

// ThresholdBound names which advisory bound the projection chases.
type ThresholdBound string

const (
	ThresholdAtMin ThresholdBound = "min" // higher_is_better: falling to min is the degradation
	ThresholdAtMax ThresholdBound = "max" // lower_is_better: rising to max is the degradation
)

// ThresholdInfo describes the bound a non-nil daysToThreshold refers to.
type ThresholdInfo struct {
	Bound     ThresholdBound         `json:"bound"`
	Value     float64                `json:"value"`
	Direction domain.MetricDirection `json:"direction"`
}

// ProjectionPoint is one extrapolated day: the fitted value with its 80%
// prediction band.
type ProjectionPoint struct {
	Day    string  `json:"day"` // YYYY-MM-DD
	Value  float64 `json:"value"`
	CiLow  float64 `json:"ciLow"`
	CiHigh float64 `json:"ciHigh"`
}

// ForecastResult is the D4 v1 contract.
type ForecastResult struct {
	ResourceID string  `json:"resourceId"`
	MetricKey  string  `json:"metricKey"`
	Label      string  `json:"label"`
	Unit       string  `json:"unit"`
	Points     int     `json:"points"`   // n of daily averages the fit rode
	FirstDay   string  `json:"firstDay"` // YYYY-MM-DD of x=0
	LastDay    string  `json:"lastDay"`  // YYYY-MM-DD of x=n-1
	Slope      float64 `json:"slopePerDay"`
	Intercept  float64 `json:"intercept"`
	SlopeSE    float64 `json:"slopeSe"`
	ResidualSE float64 `json:"residualSe"`
	// DaysToThreshold: whole days from the last observed day until the
	// fitted line crosses the degradation bound. nil = not determinable
	// (neutral direction / unbound / slope away / slope zero); 0 = the
	// last observed value is already at/beyond the bound.
	DaysToThreshold *int              `json:"daysToThreshold"`
	Threshold       *ThresholdInfo    `json:"threshold,omitempty"`
	Projections     []ProjectionPoint `json:"projections"`
	Note            string            `json:"note"`
}

// ForecastNote is the D4-mandated disclaimer every consumer must carry.
const ForecastNote = "外推参考，非承诺——线性趋势基于历史日均值，未含季节性/突发/变更，越远越不可信"

// Forecast runs the D4 v1 forecast for one resource × metric key.
// windowDays (clamped to 1..90, default 30) is how many days past the
// last observed day the projections array carries.
func (s *Service) Forecast(ctx context.Context, resourceID, metricKey string, windowDays int) (ForecastResult, error) {
	spec, known := SpecForKey(metricKey)
	if !known {
		return ForecastResult{}, fmt.Errorf("metrics: 未知指标键 %q（封闭目录：%s）", metricKey, joinKeys())
	}
	if windowDays <= 0 {
		windowDays = 30
	}
	if windowDays > 90 {
		windowDays = 90
	}
	series, err := s.db.Metrics.SeriesOfResource(ctx, resourceID, []string{metricKey})
	if err != nil {
		return ForecastResult{}, err
	}
	if len(series) == 0 {
		return ForecastResult{}, fmt.Errorf("%w：该资源没有指标 %s 的序列", ErrForecastNotEnoughData, metricKey)
	}
	daily, err := s.db.Metrics.DailyPointsOfSeries(ctx, series[0].Series.SeriesID, "", "", AggDailyAvg)
	if err != nil {
		return ForecastResult{}, err
	}
	if len(daily) < forecastMinPoints {
		return ForecastResult{}, fmt.Errorf("%w：日聚合点 %d 个 < 14（至少 14 天日均值才配得上趋势）", ErrForecastNotEnoughData, len(daily))
	}

	// The fit: x = day index 0..n-1 (day spacing is the calendar day of
	// the aggregated point, gaps included — a missing day shifts x, the
	// slope stays per CALENDAR day which is the operator's question).
	n := float64(len(daily))
	var sx, sy float64
	for i, p := range daily {
		sx += float64(i)
		sy += p.Value
	}
	xBar, yBar := sx/n, sy/n
	var sxx, sxy, syy float64
	for i, p := range daily {
		x, y := float64(i)-xBar, p.Value-yBar
		sxx += x * x
		sxy += x * y
		syy += y * y
	}
	slope := sxy / sxx
	intercept := yBar - slope*xBar
	// residual SE with n-2 dof (slope + intercept spent two).
	var sse float64
	for i, p := range daily {
		e := p.Value - (intercept + slope*float64(i))
		sse += e * e
	}
	residualSE := math.Sqrt(sse / (n - 2))
	slopeSE := 0.0
	if sxx > 0 {
		slopeSE = residualSE / math.Sqrt(sxx)
	}

	out := ForecastResult{
		ResourceID: resourceID, MetricKey: metricKey,
		Label: spec.Label, Unit: spec.Unit,
		Points: len(daily), FirstDay: daily[0].Day, LastDay: daily[len(daily)-1].Day,
		Slope: slope, Intercept: intercept, SlopeSE: slopeSE, ResidualSE: residualSE,
		Note: ForecastNote,
	}

	// The threshold projection (direction semantics per D4).
	out.DaysToThreshold, out.Threshold = projectToThreshold(
		spec, daily[len(daily)-1].Value, len(daily), intercept, slope)

	// The projection band, windowDays past the last observed day.
	lastX := n - 1
	lastDay, err := time.Parse("2006-01-02", daily[len(daily)-1].Day)
	if err != nil {
		return ForecastResult{}, fmt.Errorf("metrics: 日聚合日期 %q 不合法: %w", daily[len(daily)-1].Day, err)
	}
	out.Projections = make([]ProjectionPoint, 0, windowDays)
	for d := 0; d <= windowDays; d++ {
		x := lastX + float64(d)
		y := intercept + slope*x
		half := forecastZ80 * residualSE * math.Sqrt(1+1/n+((x-xBar)*(x-xBar))/sxx)
		out.Projections = append(out.Projections, ProjectionPoint{
			Day:    lastDay.AddDate(0, 0, d).Format("2006-01-02"),
			Value:  y,
			CiLow:  y - half,
			CiHigh: y + half,
		})
	}
	return out, nil
}

// projectToThreshold maps the catalog's Direction to the advisory bound
// the degradation runs toward, then solves intercept + slope·x = bound
// for the whole days from the LAST OBSERVED day. The four nil answers
// are all honest blanks: neutral direction, unbound side, slope moving
// away from the bound, zero slope (never arrives).
func projectToThreshold(spec MetricKeySpec, lastValue float64, nDays int, intercept, slope float64) (*int, *ThresholdInfo) {
	var bound *float64
	var which ThresholdBound
	switch spec.Direction {
	case domain.MetricDirectionHigherIsBetter:
		bound, which = spec.MinValue, ThresholdAtMin
	case domain.MetricDirectionLowerIsBetter:
		bound, which = spec.MaxValue, ThresholdAtMax
	default:
		return nil, nil
	}
	if bound == nil {
		return nil, nil
	}
	info := &ThresholdInfo{Bound: which, Value: *bound, Direction: spec.Direction}
	// Already at/beyond the bound: detection's business today — the
	// forecast refuses to "predict" the present.
	if (which == ThresholdAtMin && lastValue <= *bound) || (which == ThresholdAtMax && lastValue >= *bound) {
		zero := 0
		return &zero, info
	}
	if slope == 0 {
		return nil, info
	}
	xCross := (*bound - intercept) / slope
	days := xCross - float64(nDays-1)
	// Moving away from the bound (or the crossing already passed without
	// the last point being beyond — an inconsistent fit): no answer.
	if days < 0 {
		return nil, info
	}
	// Ceil with an epsilon guard: binary floats render -10-0.8·i as
	// 11.000000000000012-day crossings, and a raw Ceil would answer 12 —
	// a whole day the physics never promised. Sub-day slack collapses
	// honestly (11.2 still answers 12).
	whole := int(math.Ceil(days - 1e-9))
	return &whole, info
}

func joinKeys() string { return strings.Join(KeySet(), ", ") }
