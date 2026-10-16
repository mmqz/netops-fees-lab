package domain

import (
	"fmt"
	"time"
)

// FlapThreshold and FlapSpan define the v0.12-R17 re-fire hold-down
// (告警抖动治理): a (rule, series) pair that has already completed
// FlapThreshold fire→clear cycles whose engine recovery clears (data-ts)
// fall within the trailing FlapSpan of EVIDENCE time before a would-be
// new fire is oscillating — the 4th fire of a hovering series is HELD
// (counted in RunResult.FlapHeld, listed with action="flap_held" and the
// deterministic would-be id), exactly like a maintenance-suppressed
// finding but derived from the pair's OWN history instead of an operator
// plan. The hold ages out: as cleared rows exit the trailing span the
// count drops below the threshold and the next sustained violation fires
// for real (a hold is bounded by FlapSpan of data time, never forever).
//
// Semantics deliberately narrow (all pinned in internal/detect/flap_test.go):
//   - only ENGINE recovery clears count (cleared_at from the data
//     timeline); a HUMAN clear writes the wall clock into cleared_at and
//     is NOT an oscillation of the series — manual decisions are not
//     flapping;
//   - only NEW fires are held; existing alerts keep their lifecycle and
//     recovery clears continue during a hold (good news is never
//     suppressed — the R15 doctrine);
//   - the three-edge state machine is untouched: the hold is a gate
//     BEFORE the machine, never an edge (ADR-0011 D3).
//
// Code constants, not per-rule columns: the closed-value pair is engine
// tuning shared by every rule (the comparator/window precedent — per-rule
// promotion stays a documented follow-up until a real workload asks for it).
const (
	FlapThreshold = 3                   // completed cycles within the span to hold the next fire
	FlapSpan      = 14 * 24 * time.Hour // trailing evidence-time window counted back from the would-be fire
)

// Comparator is the closed comparison enum of a detection rule (ADR-0011 D2,
// migration v18 CHECK): how a measured value relates to the rule threshold
// to count as one violating point. The rule's WINDOW then decides how many
// consecutive violating points constitute a finding (去抖).
type Comparator string

// The closed comparator set.
const (
	ComparatorGt  Comparator = "gt"  // value > threshold
	ComparatorLt  Comparator = "lt"  // value < threshold
	ComparatorGte Comparator = "gte" // value >= threshold
	ComparatorLte Comparator = "lte" // value <= threshold
)

// String implements fmt.Stringer.
func (c Comparator) String() string { return string(c) }

// IsValid reports whether c is one of the four closed-enum values.
func (c Comparator) IsValid() bool {
	switch c {
	case ComparatorGt, ComparatorLt, ComparatorGte, ComparatorLte:
		return true
	}
	return false
}

// ParseComparator converts s into a Comparator; unknown values are an error.
func ParseComparator(s string) (Comparator, error) {
	v := Comparator(s)
	if !v.IsValid() {
		return "", fmt.Errorf("domain: invalid comparator %q (gt/lt/gte/lte)", s)
	}
	return v, nil
}

// Matches reports whether value satisfies the comparator against threshold
// — the pure one-point verdict of the detection engine (ADR-0011 D2:
// 判定引擎为纯函数，无时钟依赖).
func (c Comparator) Matches(value, threshold float64) bool {
	switch c {
	case ComparatorGt:
		return value > threshold
	case ComparatorLt:
		return value < threshold
	case ComparatorGte:
		return value >= threshold
	case ComparatorLte:
		return value <= threshold
	}
	return false
}

// AlertCorrelationRole is the closed correlation verdict set of migration
// v25's CHECK: primary (根因嫌疑), secondary (疑似次生), ” (unmarked).
type AlertCorrelationRole string

// The closed correlation role set.
const (
	AlertCorrelationPrimary   AlertCorrelationRole = "primary"
	AlertCorrelationSecondary AlertCorrelationRole = "secondary"
)

// ParseCorrelationRole converts s into an AlertCorrelationRole; unknown
// values are an error (the API filter boundary).
func ParseCorrelationRole(s string) (AlertCorrelationRole, error) {
	if s == "" {
		return "", nil
	}
	v := AlertCorrelationRole(s)
	switch v {
	case AlertCorrelationPrimary, AlertCorrelationSecondary:
		return v, nil
	}
	return "", fmt.Errorf("domain: invalid correlation role %q (primary/secondary)", s)
}

// AlertState is the closed alert lifecycle enum of ADR-0011 D3 (migration
// v18 CHECK): firing | acked | cleared. The state machine has EXACTLY THREE
// edges — firing→acked (human, POST /api/alerts/{id}/ack), firing→cleared
// (engine recovery OR human clear) and acked→cleared (engine recovery OR
// human clear). NO auto-escalation, NO timeout auto-close (ADR-0011 D3:
// 本地单机系统，状态机每加一条自动边就多一个不可复现的「告警自己消失了」
// 工单来源).
type AlertState string

// The closed alert state set.
const (
	AlertStateFiring  AlertState = "firing"
	AlertStateAcked   AlertState = "acked"
	AlertStateCleared AlertState = "cleared"
)

// String implements fmt.Stringer.
func (s AlertState) String() string { return string(s) }

// IsValid reports whether s is one of the three closed-enum values.
func (s AlertState) IsValid() bool {
	switch s {
	case AlertStateFiring, AlertStateAcked, AlertStateCleared:
		return true
	}
	return false
}

// ParseAlertState converts s into an AlertState; unknown values are an error.
func ParseAlertState(s string) (AlertState, error) {
	v := AlertState(s)
	if !v.IsValid() {
		return "", fmt.Errorf("domain: invalid alert state %q (firing/acked/cleared)", s)
	}
	return v, nil
}

// DetectionRule is one row of the detection_rules table (migration v18,
// ADR-0011 D2 verbatim + label/created_at/updated_at housekeeping columns):
// 「运行指标的时序越限」判定标准，如 packet_loss_rx_pct 连续 5 点 ≥ 1.0 →
// warning. Rules are CONFIG DATA, not code; they share severity semantics
// with the E/W/I catalog but deliberately NOT rule_version (同构不同表 —
// mixing the two would lock import regression tests and detection-rule
// evolution together, ADR-0011 D2). metric_key references the closed
// metric catalog (the rule's violation DIRECTION reuses metric_keys.direction
// for analysis, not a duplicate field — §12-B2 衔接点 ②).
type DetectionRule struct {
	RuleID     string
	MetricKey  string
	Comparator Comparator
	Threshold  float64
	Window     int
	Severity   Severity
	Enabled    bool
	Label      string
	CreatedAt  string // RFC3339, fixed seed timestamp for defaults / clock for edits (config bookkeeping)
	UpdatedAt  string // RFC3339
}

// Alert is one row of the alerts table (migration v18, ADR-0011 D3 verbatim
// + last_value/first_seen evidence columns; v25 adds the correlation
// observation columns). A (rule, series) pair re-firing after a cleared
// alert creates a NEW row (fresh first_seen — the UNIQUE
// (rule_id, series_id, first_seen) identity), never a zombie re-open.
//
//   - fired_at: the ts of the newest evidence point when the alert fired
//     (data-derived, deterministic);
//   - first_seen: the ts of the FIRST violating point of the firing window
//     run (data-derived);
//   - last_value: the offending point's value (the evidence);
//   - cleared_at / acked_at / acked_by: the lifecycle bookkeeping columns
//     (engine clears are data-ts; human ack/clear default to the server
//     clock — bookkeeping, not data path);
//   - CorrelationRole/CorrelatedAlertID/CorrelatedResourceID (v0.12-R16,
//     migration v25): the engine-computed 根因/次生 verdict — 'primary'
//     (根因嫌疑), 'secondary' (疑似次生, with the nearest alerting cause
//     linked), ” (unmarked). NOT lifecycle state: the three-edge state
//     machine is untouched; every detect run recomputes the roles of the
//     ACTIVE error/warning alerts, and a cleared alert keeps its last
//     verdict as audit truth.
type Alert struct {
	AlertID   string
	RuleID    string
	SeriesID  string
	FiredAt   string
	ClearedAt string // "" = not cleared
	AckedAt   string // "" = not acked
	AckedBy   string // "" = not acked
	State     AlertState
	LastValue float64
	FirstSeen string

	CorrelationRole      string // "" | primary | secondary
	CorrelatedAlertID    string // the nearest alerting cause (secondary only)
	CorrelatedResourceID string
}
