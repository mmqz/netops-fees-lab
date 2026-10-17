package domain

// maintenance.go — the maintenance-window model (v0.12-R15, migration v24):
// a PLANNED time range during which NEW alert fires on the scoped resources
// are SUPPRESSED instead of delivered. The NOC rationale: scheduled work
// (割接、升级、更换板卡) produces expected threshold crossings; an alarm that
// the operator already knows about is noise, and noise trains operators to
// ignore alarms.
//
// Suppression semantics (deliberately narrow, honest, and deterministic —
// see internal/detect/engine.go for the engine-side contract):
//
//   - only NEW fires are held: a finding whose would-be alert does not exist
//     yet is suppressed; an alert that ALREADY fired keeps its lifecycle
//     (the human holds the floor — ack/clear as usual);
//   - DUAL time basis (issue #55, v0.12.1): a finding is suppressed when
//     EITHER clock puts it inside [StartsAt, EndsAt) —
//     (a) the DATA timeline: the finding's evidence timestamp (LastTS)
//     falls in the window (replayable, deterministic);
//     (b) the OPERATIONAL timeline: the sweep's execution moment (the
//     wall clock injected into detect.Options by the recorder) falls in
//     the window. Windows are human plans written against the wall clock
//     ("tonight 22:00 → tomorrow 02:00"); basis (b) keeps a plan
//     protecting a replay of HISTORICAL data whose evidence ts sits days
//     away from the plan — before v0.12.1 that combination silently
//     suppressed nothing. Which basis matched rides the suppressed line
//     (suppressionBasis) and a silent-failure note explains the runs
//     where wall-active windows held back nothing (RunResult.
//     SuppressionNote). The resource scope applies to BOTH bases;
//   - suppressed findings are NOT alert rows: they are counted (RunResult.
//     Suppressed, detection_runs.suppressed) and listed in the run envelope
//     with action="suppressed" (the alertId line carries the DETERMINISTIC
//     would-be id, so when the window ends and the still-violating series
//     finally fires, the NOC can correlate the two lines);
//   - run-states are untouched by suppressed findings (a resource under
//     planned maintenance is not "down" — that is the whole point);
//   - recovery continues during a window: an active alert whose series
//     stopped violating is cleared normally (recovery is good news, even
//     mid-maintenance).
//
// Windows are CONFIG DATA (the detection_rules precedent): id is
// mw_<uuid> (bookkeeping identity, not the deterministic data path),
// timestamps are RFC3339 normalized to UTC on write, and the scope is a
// JSON array of resource ids ("" = the global scope). No PATCH: a window
// is a plan, editing one is delete + recreate (recorded honestly in the
// audit trail of the API log).

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// MaxMaintenanceScopeResources bounds the explicit resource scope of one
// window. The scope is a hand-maintained allowlist; beyond this size the
// operator should use the global scope (or per-resource tooling), not a
// thousand-row JSON array.
const MaxMaintenanceScopeResources = 1000

// MaintenanceWindow is one row of maintenance_windows (migration v24).
type MaintenanceWindow struct {
	WindowID    string
	Label       string
	StartsAt    string // RFC3339 UTC
	EndsAt      string // RFC3339 UTC (exclusive bound: [StartsAt, EndsAt))
	ResourceIDs []string
	Note        string
	CreatedAt   string // RFC3339, bookkeeping
}

// ParseMaintenanceWindowTimes parses both window bounds. Both must be valid
// RFC3339 timestamps; EndsAt must be strictly AFTER StartsAt (a zero-length
// or inverted window can never suppress anything and is rejected at the API
// boundary, not silently ignored).
func ParseMaintenanceWindowTimes(startsAt, endsAt string) (time.Time, time.Time, error) {
	start, err := time.Parse(time.RFC3339, startsAt)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("domain: startsAt %q 不是合法 RFC3339 时间", startsAt)
	}
	end, err := time.Parse(time.RFC3339, endsAt)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("domain: endsAt %q 不是合法 RFC3339 时间", endsAt)
	}
	if !end.After(start) {
		return time.Time{}, time.Time{}, fmt.Errorf("domain: endsAt 必须晚于 startsAt（收到 %s → %s）", startsAt, endsAt)
	}
	return start, end, nil
}

// NormalizeMaintenanceScope returns the canonical scope slice: sorted,
// deduplicated, nil for the empty scope. The SAME normalization the
// storage encoding applies — the API create response echoes the stored
// representation, not the raw request.
func NormalizeMaintenanceScope(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(ids))
	uniq := ids[:0]
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}
	sort.Strings(uniq)
	out := make([]string, len(uniq))
	copy(out, uniq)
	return out
}

// EncodeMaintenanceScope serializes the resource scope into the storage
// representation: a JSON array, or "" for the global scope (empty list).
// The representation is stable (sorted, deduplicated) so equal scopes
// encode byte-identically — the project's determinism doctrine applied to
// config data.
func EncodeMaintenanceScope(ids []string) string {
	ids = NormalizeMaintenanceScope(ids)
	if ids == nil {
		return ""
	}
	b, err := json.Marshal(ids)
	if err != nil {
		// []string cannot fail to marshal; the honest branch stays.
		return ""
	}
	return string(b)
}

// DecodeMaintenanceScope parses the storage representation back into the
// id slice ("" and "{}" both mean the global scope). A malformed array is
// an error — the repo layer never writes one, so this is a backstop.
func DecodeMaintenanceScope(s string) ([]string, error) {
	if s == "" {
		return nil, nil
	}
	var ids []string
	if err := json.Unmarshal([]byte(s), &ids); err != nil {
		return nil, fmt.Errorf("domain: 维护窗口 scope 不是合法 JSON 数组: %w", err)
	}
	return ids, nil
}
