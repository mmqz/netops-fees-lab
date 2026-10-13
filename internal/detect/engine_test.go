package detect

import (
	"reflect"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
)

// rxRule is the canonical test rule: 接收光功率 ≤ -14 连续 3 点 → warning.
func rxRule() domain.DetectionRule {
	return domain.DetectionRule{
		RuleID:     "dr_test_rx",
		MetricKey:  "optical_power_rx_avg_dbm",
		Comparator: domain.ComparatorLte,
		Threshold:  -14.0,
		Window:     3,
		Severity:   domain.SeverityWarning,
		Enabled:    true,
		Label:      "接收光功率持续偏低",
	}
}

// ts builds the i-th daily test timestamp (ascending).
func ts(i int) string {
	days := []string{
		"2026-03-01T00:00:00Z", "2026-03-02T00:00:00Z", "2026-03-03T00:00:00Z",
		"2026-03-04T00:00:00Z", "2026-03-05T00:00:00Z", "2026-03-06T00:00:00Z",
		"2026-03-07T00:00:00Z", "2026-03-08T00:00:00Z", "2026-03-09T00:00:00Z",
	}
	return days[i]
}

// pt builds one ok-quality point.
func pt(i int, v float64) Point {
	return Point{TS: ts(i), Value: v, Quality: domain.PointQualityOk}
}

// TestEvaluateWindowSemantics pins the 去抖 semantics: only the LAST
// window consecutive violating points decide — window-1 violations never
// fire, a single non-violating tail point resets the run, and the window
// counts CONSECUTIVE violations (an early dip with later recovery is not
// a trailing violation).
func TestEvaluateWindowSemantics(t *testing.T) {
	rule := rxRule()

	// window-1 = 2 consecutive violations → no finding.
	points := []Point{pt(0, -8.0), pt(1, -14.5), pt(2, -15.0)}
	if _, ok := Evaluate(points, rule); ok {
		t.Errorf("2 of 3 window violations fired, want no finding")
	}

	// exactly window = 3 consecutive violations → finding, FirstSeen at
	// the run start, LastTS/LastValue at the evidence point.
	points = []Point{pt(0, -8.0), pt(1, -14.5), pt(2, -15.0), pt(3, -15.4)}
	f, ok := Evaluate(points, rule)
	if !ok {
		t.Fatalf("3 consecutive violations did not fire, want finding")
	}
	if f.FirstSeen != ts(1) {
		t.Errorf("FirstSeen = %q, want %q (first point of the trailing run)", f.FirstSeen, ts(1))
	}
	if f.LastTS != ts(3) || f.LastValue != -15.4 {
		t.Errorf("LastTS/LastValue = %q/%v, want %q/-15.4", f.LastTS, f.LastValue, ts(3))
	}
	if f.Violations != 3 {
		t.Errorf("Violations = %d, want 3", f.Violations)
	}

	// more than window: the run start moves to the run's first point.
	points = []Point{pt(0, -15.0), pt(1, -15.1), pt(2, -15.2), pt(3, -15.3), pt(4, -15.4)}
	f, ok = Evaluate(points, rule)
	if !ok || f.FirstSeen != ts(0) || f.Violations != 5 {
		t.Errorf("5-point run = ok %v FirstSeen %q Violations %d, want ok true / %q / 5", ok, f.FirstSeen, f.Violations, ts(0))
	}

	// a clean TAIL breaks the run even after 4 violations earlier.
	points = []Point{pt(0, -15.0), pt(1, -15.1), pt(2, -15.2), pt(3, -15.3), pt(4, -8.0)}
	if _, ok = Evaluate(points, rule); ok {
		t.Errorf("recovered tail fired, want no finding (recovery condition)")
	}

	// window=1: the single-point rule.
	single := rule
	single.Window = 1
	if _, ok = Evaluate([]Point{pt(0, -8.0), pt(1, -14.5)}, single); !ok {
		t.Errorf("window=1 with one violating point did not fire")
	}
	if _, ok = Evaluate([]Point{pt(0, -14.5), pt(1, -8.0)}, single); ok {
		t.Errorf("window=1 with clean last point fired, want no finding")
	}

	// fewer points than the window → no finding.
	if _, ok = Evaluate([]Point{pt(0, -15.0), pt(1, -15.1)}, rule); ok {
		t.Errorf("2 points against window 3 fired, want no finding")
	}
	// no points at all → no finding.
	if _, ok = Evaluate(nil, rule); ok {
		t.Errorf("empty points fired, want no finding")
	}
	// window < 1 (invalid rule) → no finding, never a panic.
	bad := rule
	bad.Window = 0
	if _, ok = Evaluate([]Point{pt(0, -15.0)}, bad); ok {
		t.Errorf("window=0 fired, want no finding")
	}
}

// TestEvaluateComparatorMatrix pins all four comparators at their boundary
// values (the classic off-by-one family).
func TestEvaluateComparatorMatrix(t *testing.T) {
	cases := []struct {
		comparator domain.Comparator
		value      float64
		threshold  float64
		wantMatch  bool
	}{
		{domain.ComparatorGt, 10.0, 10.0, false},
		{domain.ComparatorGt, 10.1, 10.0, true},
		{domain.ComparatorLt, 10.0, 10.0, false},
		{domain.ComparatorLt, 9.9, 10.0, true},
		{domain.ComparatorGte, 10.0, 10.0, true},
		{domain.ComparatorGte, 9.9, 10.0, false},
		{domain.ComparatorLte, 10.0, 10.0, true},
		{domain.ComparatorLte, 10.1, 10.0, false},
	}
	for _, tc := range cases {
		rule := domain.DetectionRule{
			RuleID: "dr_c", Comparator: tc.comparator, Threshold: tc.threshold,
			Window: 1, Severity: domain.SeverityWarning,
		}
		if got := rule.Comparator.Matches(tc.value, tc.threshold); got != tc.wantMatch {
			t.Errorf("%s value %v threshold %v = %v, want %v", tc.comparator, tc.value, tc.threshold, got, tc.wantMatch)
		}
		_, ok := Evaluate([]Point{{TS: ts(0), Value: tc.value, Quality: domain.PointQualityOk}}, rule)
		if ok != tc.wantMatch {
			t.Errorf("%s Evaluate = %v, want %v", tc.comparator, ok, tc.wantMatch)
		}
	}
}

// TestEvaluateSuspectQualityStillEvaluates pins the documented quality
// policy: suspect points are recorded values and evaluate normally (the
// advisory bounds filter nothing, §5.3); missing points are gap markers —
// they break the consecutive run and never count as violating.
func TestEvaluateSuspectQualityStillEvaluates(t *testing.T) {
	rule := rxRule()

	// All three suspect → still fires.
	points := []Point{
		{TS: ts(0), Value: -14.5, Quality: domain.PointQualitySuspect},
		{TS: ts(1), Value: -15.0, Quality: domain.PointQualitySuspect},
		{TS: ts(2), Value: -15.4, Quality: domain.PointQualitySuspect},
	}
	f, ok := Evaluate(points, rule)
	if !ok || f.FirstSeen != ts(0) {
		t.Errorf("suspect-quality run = ok %v FirstSeen %q, want ok true / %q", ok, f.FirstSeen, ts(0))
	}

	// A missing point in the middle of the trailing run breaks it: only
	// the points AFTER the gap count (3 of them → still fires, run
	// restarts after the gap).
	points = []Point{
		{TS: ts(0), Value: -15.0, Quality: domain.PointQualityOk},
		{TS: ts(1), Value: -15.1, Quality: domain.PointQualityMissing},
		{TS: ts(2), Value: -15.2, Quality: domain.PointQualityOk},
		{TS: ts(3), Value: -15.3, Quality: domain.PointQualityOk},
		{TS: ts(4), Value: -15.4, Quality: domain.PointQualityOk},
	}
	f, ok = Evaluate(points, rule)
	if !ok || f.FirstSeen != ts(2) {
		t.Errorf("run with gap = ok %v FirstSeen %q, want ok true / %q (gap breaks the run, restart at the gap's successor)", ok, f.FirstSeen, ts(2))
	}

	// The gap leaves only 2 consecutive violations → no finding.
	points = []Point{
		{TS: ts(0), Value: -15.0, Quality: domain.PointQualityMissing},
		{TS: ts(1), Value: -15.1, Quality: domain.PointQualityOk},
		{TS: ts(2), Value: -15.2, Quality: domain.PointQualityOk},
	}
	if _, ok = Evaluate(points, rule); ok {
		t.Errorf("gap-broken run of 2 fired, want no finding")
	}
}

// TestEvaluateDeterministic pins the pure-function determinism gate: the
// same inputs always produce the identical finding (call twice, compare
// deep-equal — no clock, no ambient state).
func TestEvaluateDeterministic(t *testing.T) {
	rule := rxRule()
	points := []Point{pt(0, -8.0), pt(1, -14.5), pt(2, -15.0), pt(3, -15.4), pt(4, -15.2)}
	f1, ok1 := Evaluate(points, rule)
	f2, ok2 := Evaluate(points, rule)
	if ok1 != ok2 {
		t.Fatalf("ok flipped between identical calls: %v vs %v", ok1, ok2)
	}
	if !reflect.DeepEqual(f1, f2) {
		t.Errorf("identical inputs → different findings: %+v vs %+v", f1, f2)
	}
}
