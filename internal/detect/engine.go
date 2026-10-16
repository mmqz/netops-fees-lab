// Package detect implements the alert detection engine of NetOps Lab (W23,
// migration v18 — ADR-0011 D2/D3, design data-model-v1.md §12-B2, issue
// #23): the RULES side of the "same shape, different table" pair whose
// query cousin is internal/metrics Anomalies (§5.5 Q2 trend scan — both
// read the same metric tables; Anomalies answers "which series moved",
// detection answers "which series are OUT OF BOUNDS and who is degraded
// because of it").
//
// The engine follows the project's determinism doctrine end to end:
//
//   - the CORE is a PURE FUNCTION — Evaluate(points, rule) has no DB, no
//     clock, no ambient state; every timestamp comes from the data. Same
//     points + same rule → same finding, fully unit-testable;
//   - the DB parts (load rules, load series, upsert alerts, write
//     run-states) are thin wrappers over internal/repo;
//   - one Detect run is ONE transaction (DB.Do): alert upserts, clears and
//     run-state writes commit atomically or not at all — idempotent per
//     run (re-running over unchanged data is a no-op);
//   - alert ids are DERIVED (sha256 of rule/series/first_seen), not random:
//     same starting state + same data + same rules → byte-identical alert
//     rows and result envelope.
//
// Alert lifecycle (ADR-0011 D3): EXACTLY THREE edges — firing→acked (human
// HTTP ack), firing→cleared (engine recovery or human clear), acked→cleared
// (same). No auto-escalation, no timeout auto-close. A (rule, series) pair
// re-firing after a cleared alert creates a NEW alert row (fresh
// first_seen). Already-acked alerts never re-fire while acked — the human
// holds the floor.
//
// Maintenance windows (v0.12-R15) sit BEFORE the state machine, not inside
// it: a NEW fire whose evidence ts falls inside [starts_at, ends_at) on a
// scoped resource never enters the machine (no alert row, no state write);
// windows never mutate existing alerts. The alert table's shape and the
// three-edge doctrine are untouched — suppression is a gate, not an edge.
package detect

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// Point is one measurement point as the pure core sees it: ts from the
// data (normalized RFC3339 UTC), the value, and the recorded quality.
type Point struct {
	TS      string
	Value   float64
	Quality domain.PointQuality
}

// SuppressedWindow is the pure-function projection of one maintenance
// window row (v0.12-R15): only what the suppression verdict needs — the
// half-open [StartsAfter, EndsAfter) interval (both bounds from the DATA
// timeline, pre-parsed) and the resource scope (nil = global). Loading
// the rows and parsing the RFC3339 bounds is the repo/DB concern; the
// verdict below stays clock-free and DB-free.
type SuppressedWindow struct {
	StartsAfter time.Time
	EndsBefore  time.Time
	ResourceIDs map[string]bool // nil = the global scope (every resource)
	Label       string
}

// Suppresses reports whether a finding on resourceID with evidence
// timestamp lastTS falls under this window: lastTS ∈ [StartsAfter,
// EndsBefore) AND the resource is in scope. The interval is half-open on
// purpose — a fire exactly at EndsBefore belongs to the world AFTER the
// plan, a fire exactly at StartsAfter belongs to the plan. Two windows
// overlapping the same ts/resource is fine: the first match wins in the
// engine's loop, the verdict (suppressed) is the same.
func (w SuppressedWindow) Suppresses(resourceID, lastTS string) bool {
	if w.ResourceIDs != nil && !w.ResourceIDs[resourceID] {
		return false
	}
	ts, err := time.Parse(time.RFC3339, lastTS)
	if err != nil {
		return false // engine ts strings are RFC3339 by invariant; a parse miss never suppresses
	}
	return !ts.Before(w.StartsAfter) && ts.Before(w.EndsBefore)
}

// Finding is one rule/series violation: the last Window consecutive points
// all satisfied comparator(threshold). FirstSeen is the ts of the FIRST
// point of that trailing violating run; LastTS/LastValue are the newest
// evaluated point (the evidence that completed the window).
type Finding struct {
	RuleID     string
	SeriesID   string
	ResourceID string
	MetricKey  string
	Severity   domain.Severity
	FirstSeen  string
	LastTS     string
	LastValue  float64
	Violations int
}

// Evaluate is the PURE-FUNCTION core (ADR-0011 D2: 判定引擎为纯函数):
// points must be ordered ascending by ts (the repo read path guarantees
// it). A series VIOLATES the rule when the last Window consecutive points
// ALL satisfy comparator(threshold) — Window=1 is the single-point rule.
//
// Quality policy (documented disposition): points with quality='suspect'
// still evaluate — they are recorded values, the advisory bounds filter
// nothing (§5.3); quality='missing' is a gap marker, not a value — it
// BREAKS the consecutive run (never counts as violating). No clock, no DB:
// fully deterministic.
func Evaluate(points []Point, rule domain.DetectionRule) (Finding, bool) {
	if len(points) == 0 || rule.Window < 1 {
		return Finding{}, false
	}
	// Walk backwards over the trailing consecutive violating run.
	run := 0
	for i := len(points) - 1; i >= 0; i-- {
		p := points[i]
		if p.Quality == domain.PointQualityMissing {
			break // a known gap breaks the consecutive run
		}
		if !rule.Comparator.Matches(p.Value, rule.Threshold) {
			break // first non-violating point terminates the run
		}
		run++
	}
	if run < rule.Window {
		return Finding{}, false
	}
	last := points[len(points)-1]
	return Finding{
		RuleID:     rule.RuleID,
		FirstSeen:  points[len(points)-run].TS,
		LastTS:     last.TS,
		LastValue:  last.Value,
		Violations: run,
	}, true
}

// Options parameterize one Detect run: From/To are optional RFC3339
// bounds of the evaluation window ("" = unbounded on that side) — the
// daily-inspection sweep scans ALL series of every enabled rule's metric
// key inside [from, to].
type Options struct {
	From string
	To   string
}

// AlertSummary is one alert mutation of a run (created or cleared) — the
// run envelope's per-alert evidence line. v0.12-R15: action="suppressed"
// lines describe findings held back by a maintenance window — they have
// NO alert row, so State is "" and AlertID carries the DETERMINISTIC
// would-be id (same identity the fire gets after the window ends when the
// violation run extends back across the boundary — correlatable).
type AlertSummary struct {
	AlertID    string  `json:"alertId"`
	RuleID     string  `json:"ruleId"`
	SeriesID   string  `json:"seriesId"`
	ResourceID string  `json:"resourceId"`
	MetricKey  string  `json:"metricKey"`
	Severity   string  `json:"severity"`
	Action     string  `json:"action"` // created | cleared | suppressed
	State      string  `json:"state"`  // the state AFTER the run ("" for suppressed lines)
	FirstSeen  string  `json:"firstSeen"`
	FiredAt    string  `json:"firedAt"`
	ClearedAt  string  `json:"clearedAt"`
	LastValue  float64 `json:"lastValue"`
}

// RunResult is the response envelope of POST /api/detection/run and the
// CLI `netopslab detect` output. Findings counts ALL violations of the run
// (including series whose alert already exists); AlertsCreated /
// AlertsCleared / RunStatesWritten count the run's MUTATIONS; Suppressed
// counts the findings held back by maintenance windows (a subset of
// Findings — the alarm the NOC intentionally chose not to receive);
// Alerts lists the created/cleared/suppressed lines (ordered rule_id,
// series_id). EvaluatedAt is the run's evaluation timestamp FROM THE DATA
// (the newest ts of any evaluated point; "" when no data was in scope).
type RunResult struct {
	Findings         int            `json:"findings"`
	AlertsCreated    int            `json:"alertsCreated"`
	AlertsCleared    int            `json:"alertsCleared"`
	RunStatesWritten int            `json:"runStatesWritten"`
	Suppressed       int            `json:"suppressed"`
	EvaluatedAt      string         `json:"evaluatedAt"`
	Alerts           []AlertSummary `json:"alerts"`
}

// Engine is the stateless detection engine — Detect is a pure run over
// (database state, rules, window). One shared instance is wired into the
// composition root (services.New); HTTP and CLI call the same method, no
// bypass.
type Engine struct{}

// New returns the engine (stateless; the constructor exists for the
// composition-root convention).
func New() *Engine { return &Engine{} }

// Detect runs one detection sweep over db:
//
//  1. lazily seed the default rules when the table is empty (repo side,
//     INSERT OR IGNORE — data, not schema);
//  2. load every ENABLED rule (rule_id order) and every series of its
//     metric_key (series_id order);
//  3. evaluate each series's points in [opts.From, opts.To] through the
//     pure core;
//  4. sync the alert state machine (create firing, clear recovered,
//     refresh firing evidence — acked alerts are never re-fired);
//  5. map active (firing|acked) error/warning alerts to run-states via
//     repo.ResourceStatus.UpsertStatus with source=alert (error→down,
//     warning→degraded, error wins) and write the recovery 'up' for
//     resources whose alert-sourced state exists but that have no active
//     error/warning alert anymore;
//  6. everything commits in ONE transaction.
//
// Determinism: same database state + same rules + same window → identical
// result and identical stored rows. A re-run over unchanged data reports
// 0/0/0 mutations (idempotent per run).
func (e *Engine) Detect(ctx context.Context, db *repo.DB, opts Options) (RunResult, error) {
	var res RunResult
	err := db.Do(ctx, func(tx *repo.Tx) error {
		if err := tx.DetectionRules.SeedDefaultDetectionRules(ctx); err != nil {
			return err
		}
		// Maintenance windows (v0.12-R15): loaded INSIDE the same
		// transaction as the alert sync — the suppression verdict and
		// the alert writes see one consistent window set (a window
		// created mid-sweep cannot half-apply).
		supWindows, err := loadSuppressionWindows(ctx, tx)
		if err != nil {
			return err
		}
		rules, err := tx.DetectionRules.ListRules(ctx, true)
		if err != nil {
			return err
		}

		evalTS := "" // the run's evaluation timestamp: newest evaluated data point
		summaries := make([]AlertSummary, 0, 8)

		for _, rule := range rules {
			series, err := tx.Metrics.SeriesOfKey(ctx, rule.MetricKey)
			if err != nil {
				return err
			}
			findings := make([]Finding, 0, len(series))
			// lastTSBySeries: every series that had DATA in the window — the
			// recovery-evidence set (a series with no points in scope is NOT
			// recovered: absence of observations is not health, it is silence).
			lastTSBySeries := make(map[string]string, len(series))
			for _, se := range series {
				points, err := tx.Metrics.PointsOfSeries(ctx, se.SeriesID, opts.From, opts.To)
				if err != nil {
					return err
				}
				if len(points) == 0 {
					continue
				}
				pts := make([]Point, len(points))
				for i, p := range points {
					pts[i] = Point{TS: p.TS, Value: p.Value, Quality: p.Quality}
				}
				lastTSBySeries[se.SeriesID] = pts[len(pts)-1].TS
				if pts[len(pts)-1].TS > evalTS {
					evalTS = pts[len(pts)-1].TS
				}
				if f, ok := Evaluate(pts, rule); ok {
					f.SeriesID = se.SeriesID
					f.ResourceID = se.ResourceID
					f.MetricKey = se.MetricKey
					f.Severity = rule.Severity
					findings = append(findings, f)
				}
			}
			res.Findings += len(findings)

			// --- alert sync (per rule, idempotent per run) ---
			active, err := tx.Alerts.ListActiveAlertsByRule(ctx, rule.RuleID)
			if err != nil {
				return err
			}
			activeBySeries := make(map[string]domain.Alert, len(active))
			for _, a := range active {
				activeBySeries[a.SeriesID] = a
			}
			findingBySeries := make(map[string]Finding, len(findings))
			for _, f := range findings {
				findingBySeries[f.SeriesID] = f
			}

			// 1. findings → create (no non-cleared alert) or refresh evidence
			//    (firing only — an acked alert's evidence is frozen at the
			//    human's floor; acked alerts never re-fire).
			//
			//    v0.12-R15: a NEW fire whose evidence ts falls inside a
			//    maintenance window covering the resource is SUPPRESSED —
			//    no alert row, counted, listed with action="suppressed"
			//    and the deterministic would-be id. Existing alerts keep
			//    their lifecycle (evidence refresh included): the window
			//    holds NEW noise, it does not rewrite history.
			for _, f := range findings {
				existing, has := activeBySeries[f.SeriesID]
				if has {
					if existing.State == domain.AlertStateFiring {
						if err := tx.Alerts.UpdateFiringEvidence(ctx, existing.AlertID, f.LastTS, f.LastValue); err != nil {
							return err
						}
					}
					continue
				}
				if matchSuppressionWindow(supWindows, f.ResourceID, f.LastTS) != nil {
					res.Suppressed++
					summaries = append(summaries, AlertSummary{
						AlertID:    NewAlertID(rule.RuleID, f.SeriesID, f.FirstSeen),
						RuleID:     rule.RuleID,
						SeriesID:   f.SeriesID,
						ResourceID: f.ResourceID,
						MetricKey:  f.MetricKey,
						Severity:   rule.Severity.String(),
						Action:     "suppressed",
						State:      "",
						FirstSeen:  f.FirstSeen,
						FiredAt:    f.LastTS,
						LastValue:  f.LastValue,
					})
					continue
				}
				a := domain.Alert{
					AlertID:   NewAlertID(rule.RuleID, f.SeriesID, f.FirstSeen),
					RuleID:    rule.RuleID,
					SeriesID:  f.SeriesID,
					FiredAt:   f.LastTS,
					State:     domain.AlertStateFiring,
					LastValue: f.LastValue,
					FirstSeen: f.FirstSeen,
				}
				created, err := tx.Alerts.InsertFiringAlert(ctx, a)
				if err != nil {
					return err
				}
				if created {
					res.AlertsCreated++
					summaries = append(summaries, alertSummary(a, f, "created"))
				}
				// created=false: the (rule, series, first_seen) identity
				// already exists as a CLEARED row — a manual clear of a
				// still-violating series stands until a LATER violation
				// window (fresh first_seen) fires a fresh alert. Honest
				// suppression, documented in api.md.
			}

			// 2. active alerts whose series no longer violate → clear
			//    (firing→cleared AND acked→cleared, the same recovery edge).
			for _, a := range active {
				if _, violating := findingBySeries[a.SeriesID]; violating {
					continue
				}
				lastTS, hasData := lastTSBySeries[a.SeriesID]
				if !hasData {
					continue // no data in scope ≠ recovered — hold the alert
				}
				if err := tx.Alerts.ClearAlert(ctx, a.AlertID, lastTS); err != nil {
					return err
				}
				res.AlertsCleared++
				summaries = append(summaries, AlertSummary{
					AlertID:   a.AlertID,
					RuleID:    a.RuleID,
					SeriesID:  a.SeriesID,
					MetricKey: rule.MetricKey,
					Severity:  rule.Severity.String(),
					Action:    "cleared",
					State:     domain.AlertStateCleared.String(),
					FirstSeen: a.FirstSeen,
					FiredAt:   a.FiredAt,
					ClearedAt: lastTS,
					LastValue: a.LastValue,
				})
			}
		}

		// --- run-state integration (§12-B2 衔接点 ③) ---
		runStates, err := writeRunStates(ctx, tx, evalTS)
		if err != nil {
			return err
		}
		res.RunStatesWritten = runStates

		// Deterministic order of the summary: (ruleId, seriesId, alertId).
		sort.Slice(summaries, func(i, j int) bool {
			if summaries[i].RuleID != summaries[j].RuleID {
				return summaries[i].RuleID < summaries[j].RuleID
			}
			if summaries[i].SeriesID != summaries[j].SeriesID {
				return summaries[i].SeriesID < summaries[j].SeriesID
			}
			return summaries[i].AlertID < summaries[j].AlertID
		})
		res.Alerts = summaries
		res.EvaluatedAt = evalTS
		return nil
	})
	if err != nil {
		return RunResult{}, err
	}
	return res, nil
}

// loadSuppressionWindows reads the maintenance-window rows and projects
// them into the pure SuppressedWindow form (parsed bounds, scope set).
// A row whose bounds no longer parse is a HARD error — the API boundary
// validated them on write, so an unparseable row means direct DB tampering;
// failing the sweep (and recording the failure in detection_runs) is the
// honest outcome, not silently ignoring a window that may be suppressing
// alarms the operator believes are active.
func loadSuppressionWindows(ctx context.Context, tx *repo.Tx) ([]SuppressedWindow, error) {
	rows, err := tx.Maintenance.ListWindows(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SuppressedWindow, 0, len(rows))
	for _, row := range rows {
		start, end, err := domain.ParseMaintenanceWindowTimes(row.StartsAt, row.EndsAt)
		if err != nil {
			return nil, fmt.Errorf("detect: maintenance window %s (%s): %w", row.WindowID, row.Label, err)
		}
		w := SuppressedWindow{StartsAfter: start, EndsBefore: end, Label: row.Label}
		if len(row.ResourceIDs) > 0 {
			scope := make(map[string]bool, len(row.ResourceIDs))
			for _, id := range row.ResourceIDs {
				scope[id] = true
			}
			w.ResourceIDs = scope
		}
		out = append(out, w)
	}
	return out, nil
}

// matchSuppressionWindow returns the FIRST window suppressing the
// (resource, ts) pair, or nil. Order follows the listing (starts_at DESC)
// — with overlapping windows the verdict (suppressed) is identical either
// way; the returned window's label is evidence for the envelope line.
func matchSuppressionWindow(windows []SuppressedWindow, resourceID, lastTS string) *SuppressedWindow {
	for i := range windows {
		if windows[i].Suppresses(resourceID, lastTS) {
			return &windows[i]
		}
	}
	return nil
}

// writeRunStates maps the alert table to run-state observations (source=
// alert — the engine becomes a REAL writer of the W22 model; the HTTP
// manual channel keeps rejecting source=alert, so there is no API hole):
//
//   - ≥1 FIRING severity=error alert on the resource → down;
//   - else ≥1 FIRING severity=warning alert → degraded (error wins);
//   - NO firing AND NO acked error/warning alert anymore, but the resource
//     has an alert-sourced history row → up (the recovery rule);
//   - acked-only resources hold their last written state — neither
//     re-down nor up: the human acknowledged a condition that is not yet
//     observed recovered (clear it, engine or manual, to free the floor);
//   - info-severity alerts never move run-state (an FYI is not a fault);
//   - evalTS == "" (nothing was evaluated this run) → no writes at all.
//
// Every write goes through ResourceStatusRepo.UpsertStatus — the SAME
// history + snapshot double-write path as the manual channel; the ts is
// always evalTS (data-derived). Last-write-wins per ts ordering is the
// documented co-existence semantics: a manual 'up' set AFTER an
// alert-sourced 'down' is not overridden by the recovery write unless a
// NEWER detection run happens (UpsertStatus moves the snapshot only for
// ts >= the projected one).
func writeRunStates(ctx context.Context, tx *repo.Tx, evalTS string) (int, error) {
	if evalTS == "" {
		return 0, nil
	}
	refs, err := tx.Alerts.ListActiveAlertRefs(ctx)
	if err != nil {
		return 0, err
	}
	held := make(map[string]bool) // resources held non-up by an active error/warning alert
	downSet := make(map[string]bool)
	degradedSet := make(map[string]bool)
	var down, degraded []string
	for _, ref := range refs {
		if ref.Severity != domain.SeverityError && ref.Severity != domain.SeverityWarning {
			continue
		}
		held[ref.ResourceID] = true
		if ref.State != domain.AlertStateFiring {
			continue // acked holds the last written state — no new write
		}
		if ref.Severity == domain.SeverityError {
			if !downSet[ref.ResourceID] {
				downSet[ref.ResourceID] = true
				down = append(down, ref.ResourceID)
			}
		}
	}
	for _, ref := range refs {
		if ref.State != domain.AlertStateFiring || ref.Severity != domain.SeverityWarning {
			continue
		}
		if downSet[ref.ResourceID] {
			continue // error wins over warning
		}
		if !degradedSet[ref.ResourceID] {
			degradedSet[ref.ResourceID] = true
			degraded = append(degraded, ref.ResourceID)
		}
	}

	ts := mustParseTS(evalTS)
	written := 0
	for _, id := range down {
		if err := tx.ResourceStatus.UpsertStatus(ctx, domain.ResourceStatus{
			ResourceID: id, TS: ts, RunState: domain.RunStateDown, Source: domain.StatusSourceAlert,
		}); err != nil {
			return written, err
		}
		written++
	}
	for _, id := range degraded {
		if err := tx.ResourceStatus.UpsertStatus(ctx, domain.ResourceStatus{
			ResourceID: id, TS: ts, RunState: domain.RunStateDegraded, Source: domain.StatusSourceAlert,
		}); err != nil {
			return written, err
		}
		written++
	}

	// Recovery pass: resources with an alert-sourced history row whose
	// error/warning alerts are all inactive.
	everAlerted, err := tx.ResourceStatus.ResourceIDsBySource(ctx, domain.StatusSourceAlert)
	if err != nil {
		return written, err
	}
	for _, id := range everAlerted {
		if held[id] {
			continue
		}
		if err := tx.ResourceStatus.UpsertStatus(ctx, domain.ResourceStatus{
			ResourceID: id, TS: ts, RunState: domain.RunStateUp, Source: domain.StatusSourceAlert,
		}); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

// NewAlertID derives the DETERMINISTIC alert id from the alert's identity
// (rule, series, first_seen): "al_" + 16 bytes of sha256 hex. The same
// starting state + data + rules therefore produce byte-identical alert
// rows (random ids would make re-runs diverge); a re-fire after clear has
// a fresh first_seen and therefore a fresh id.
func NewAlertID(ruleID, seriesID, firstSeen string) string {
	sum := sha256.Sum256([]byte(ruleID + "\x00" + seriesID + "\x00" + firstSeen))
	return "al_" + hex.EncodeToString(sum[:16])
}

// mustParseTS converts a stored RFC3339 string back to time.Time — the
// engine only ever passes ts values that were read FROM the metric tables
// (already normalized RFC3339), so a parse failure is a programming error.
func mustParseTS(ts string) time.Time {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		panic(fmt.Sprintf("detect: evaluation ts %q is not RFC3339 (data path invariant broken): %v", ts, err))
	}
	return t
}

// alertSummary builds one created-alert summary line.
func alertSummary(a domain.Alert, f Finding, action string) AlertSummary {
	return AlertSummary{
		AlertID:    a.AlertID,
		RuleID:     a.RuleID,
		SeriesID:   a.SeriesID,
		ResourceID: f.ResourceID,
		MetricKey:  f.MetricKey,
		Severity:   f.Severity.String(),
		Action:     action,
		State:      a.State.String(),
		FirstSeen:  a.FirstSeen,
		FiredAt:    a.FiredAt,
		LastValue:  a.LastValue,
	}
}
