package repo

// detection.go — the W23 alert detection storage (migration v18, ADR-0011
// D2/D3, issue #23): the detection_rules config table and the alerts state
// machine. All SQL of the detection channel lives here; the pure-function
// engine (internal/detect) works over these typed methods.
//
// rule_version deliberately does NOT exist on either table (同构不同表,
// ADR-0011 D2: detection rules and the E/W/I import catalog share severity
// semantics but never a version anchor — mixing them would lock import
// regression tests and detection-rule evolution together).
//
// The DEFAULT rule set is defined here (DefaultDetectionRules) and seeded
// lazily — INSERT OR IGNORE by rule_id, only when the table is EMPTY — by
// SeedDefaultDetectionRules, invoked from the detection engine run and the
// rules-list HTTP handler. Why not in the migration: rules are USER DATA,
// not schema — a user who deletes dr_opt_rx_low must not get it back on the
// next schema replay, and a future rule-edit round must not need a v19.
// (The metric catalog of v14 is closed-registry static data — a different
// animal, seeded in the migration on purpose.)

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/mmqz/netops-lab/internal/domain"
)

// defaultRulesSeedTS is the FIXED created_at/updated_at of the default rule
// rows — the v14 catalog precedent: seeded rows are static data, the seed
// must stay deterministic (no clock in the data path).
const defaultRulesSeedTS = "2026-01-01T00:00:00Z"

// DetectionRuleRepo persists the detection rule config rows.
type DetectionRuleRepo struct{ q DBTX }

// AlertRepo persists the alert state machine rows.
type AlertRepo struct{ q DBTX }

// DefaultDetectionRules is the closed catalog's starter set (the §5.2
// domain-knowledge thresholds — 接收光功率 -14/-25 dBm、丢包 1%、带宽
// 90%、模块温度 70℃): five rules over four metric keys that make detection
// usable on day one. DIRECTION is NOT duplicated here — it lives in
// metric_keys.direction (§12-B2 衔接点 ②).
var DefaultDetectionRules = []domain.DetectionRule{
	{RuleID: "dr_opt_rx_low", MetricKey: "optical_power_rx_avg_dbm", Comparator: domain.ComparatorLte,
		Threshold: -14.0, Window: 3, Severity: domain.SeverityWarning, Enabled: true,
		Label: "接收光功率持续偏低", CreatedAt: defaultRulesSeedTS, UpdatedAt: defaultRulesSeedTS},
	{RuleID: "dr_opt_rx_floor", MetricKey: "optical_power_rx_avg_dbm", Comparator: domain.ComparatorLte,
		Threshold: -25.0, Window: 1, Severity: domain.SeverityError, Enabled: true,
		Label: "接收光功率跌至谷底", CreatedAt: defaultRulesSeedTS, UpdatedAt: defaultRulesSeedTS},
	{RuleID: "dr_loss_rx", MetricKey: "packet_loss_rx_pct", Comparator: domain.ComparatorGte,
		Threshold: 1.0, Window: 5, Severity: domain.SeverityWarning, Enabled: true,
		Label: "接收丢包率持续超限", CreatedAt: defaultRulesSeedTS, UpdatedAt: defaultRulesSeedTS},
	{RuleID: "dr_util_rx", MetricKey: "bandwidth_util_rx_pct", Comparator: domain.ComparatorGte,
		Threshold: 90.0, Window: 3, Severity: domain.SeverityWarning, Enabled: true,
		Label: "接收带宽利用率持续高企", CreatedAt: defaultRulesSeedTS, UpdatedAt: defaultRulesSeedTS},
	{RuleID: "dr_temp", MetricKey: "module_temp_avg_c", Comparator: domain.ComparatorGte,
		Threshold: 70.0, Window: 3, Severity: domain.SeverityWarning, Enabled: true,
		Label: "光模块温度偏高", CreatedAt: defaultRulesSeedTS, UpdatedAt: defaultRulesSeedTS},
}

// SeedDefaultDetectionRules inserts DefaultDetectionRules (INSERT OR IGNORE
// by rule_id) ONLY when the detection_rules table is completely empty — the
// lazy day-one seeding of the engine/rules-list. A user who deletes or edits
// rules never gets them resurrected: the guard is "table empty", not "row
// missing". Call inside the caller's transaction for atomicity with the
// surrounding run.
func (r *DetectionRuleRepo) SeedDefaultDetectionRules(ctx context.Context) error {
	var n int
	if err := r.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM detection_rules`).Scan(&n); err != nil {
		return fmt.Errorf("repo: seed detection rules: count: %w", err)
	}
	if n > 0 {
		return nil
	}
	for _, rule := range DefaultDetectionRules {
		if _, err := r.q.ExecContext(ctx,
			`INSERT OR IGNORE INTO detection_rules
  (rule_id, metric_key, comparator, threshold, window, severity, enabled, label, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?)`,
			rule.RuleID, rule.MetricKey, rule.Comparator.String(), rule.Threshold, rule.Window,
			rule.Severity.String(), boolInt(rule.Enabled), rule.Label, rule.CreatedAt, rule.UpdatedAt); err != nil {
			return fmt.Errorf("repo: seed detection rule %s: %w", rule.RuleID, err)
		}
	}
	return nil
}

// boolInt maps a bool to the 0/1 INTEGER storage form.
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ruleColumns is the canonical SELECT column list of detection_rules.
const ruleColumns = `rule_id, metric_key, comparator, threshold, window, severity, enabled, label, created_at, updated_at`

// scanRule scans one detection_rules row into a domain.DetectionRule.
func scanRule(s scanner) (domain.DetectionRule, error) {
	var (
		rule       domain.DetectionRule
		comparator string
		severity   string
		enabled    int
	)
	if err := s.Scan(&rule.RuleID, &rule.MetricKey, &comparator, &rule.Threshold, &rule.Window,
		&severity, &enabled, &rule.Label, &rule.CreatedAt, &rule.UpdatedAt); err != nil {
		return domain.DetectionRule{}, err
	}
	if _, err := domain.ParseComparator(comparator); err != nil {
		return domain.DetectionRule{}, fmt.Errorf("repo: rule %s: %w", rule.RuleID, err)
	}
	if _, err := domain.ParseSeverity(severity); err != nil {
		return domain.DetectionRule{}, fmt.Errorf("repo: rule %s: %w", rule.RuleID, err)
	}
	rule.Comparator = domain.Comparator(comparator)
	rule.Severity = domain.Severity(severity)
	rule.Enabled = enabled != 0
	return rule, nil
}

// ListRules returns the rules ordered by rule_id (deterministic listing
// contract). enabledOnly restricts to enabled rules (the engine's load).
func (r *DetectionRuleRepo) ListRules(ctx context.Context, enabledOnly bool) ([]domain.DetectionRule, error) {
	query := `SELECT ` + ruleColumns + ` FROM detection_rules`
	if enabledOnly {
		query += ` WHERE enabled = 1`
	}
	query += ` ORDER BY rule_id`
	rows, err := r.q.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("repo: list detection rules: %w", err)
	}
	defer rows.Close()
	out := make([]domain.DetectionRule, 0, 8)
	for rows.Next() {
		rule, err := scanRule(rows)
		if err != nil {
			return nil, fmt.Errorf("repo: scan detection rule: %w", err)
		}
		out = append(out, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: list detection rules: %w", err)
	}
	return out, nil
}

// GetRuleByID loads one rule; unknown id answers domain.ErrNotFound.
func (r *DetectionRuleRepo) GetRuleByID(ctx context.Context, ruleID string) (domain.DetectionRule, error) {
	var rule domain.DetectionRule
	row := r.q.QueryRowContext(ctx, `SELECT `+ruleColumns+` FROM detection_rules WHERE rule_id = ?`, ruleID)
	rule, err := scanRule(row)
	if err != nil {
		return domain.DetectionRule{}, notFound("detection rule", ruleID, err)
	}
	return rule, nil
}

// InsertRule stores a new rule. A duplicate rule_id answers
// domain.ErrConflict (the caller decides 409); an unknown metric_key
// violates the FK (validation belongs to the service/API layer — the CHECK
// and FK are the backstop).
func (r *DetectionRuleRepo) InsertRule(ctx context.Context, rule domain.DetectionRule) error {
	_, err := r.q.ExecContext(ctx,
		`INSERT INTO detection_rules
  (rule_id, metric_key, comparator, threshold, window, severity, enabled, label, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?)`,
		rule.RuleID, rule.MetricKey, rule.Comparator.String(), rule.Threshold, rule.Window,
		rule.Severity.String(), boolInt(rule.Enabled), rule.Label, rule.CreatedAt, rule.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("repo: insert detection rule %s: %w", rule.RuleID, domain.ErrConflict)
		}
		return fmt.Errorf("repo: insert detection rule %s: %w", rule.RuleID, err)
	}
	return nil
}

// UpdateRule persists the mutable fields of rule (everything except the
// identity — metric_key immutability is an API-layer contract, the repo
// writes what it is given). updated_at is the caller's stamp (config
// bookkeeping). Unknown id answers domain.ErrNotFound.
func (r *DetectionRuleRepo) UpdateRule(ctx context.Context, rule domain.DetectionRule) error {
	res, err := r.q.ExecContext(ctx,
		`UPDATE detection_rules
SET metric_key = ?, comparator = ?, threshold = ?, window = ?, severity = ?, enabled = ?, label = ?, updated_at = ?
WHERE rule_id = ?`,
		rule.MetricKey, rule.Comparator.String(), rule.Threshold, rule.Window, rule.Severity.String(),
		boolInt(rule.Enabled), rule.Label, rule.UpdatedAt, rule.RuleID)
	if err != nil {
		return fmt.Errorf("repo: update detection rule %s: %w", rule.RuleID, err)
	}
	return requireUpdated(res, "detection rule", rule.RuleID)
}

// DeleteRule removes a rule — REFUSED (domain.ErrConflict) while any alert
// references it: alerts are the audit trail, deleting the rule that
// produced them would orphan the history (blocked-delete, ADR-0011-silent
// point ruled in §12 W23 处置). Call inside one transaction so the
// count-then-delete pair is atomic; the table FK is the backstop.
func (r *DetectionRuleRepo) DeleteRule(ctx context.Context, ruleID string) error {
	var n int
	if err := r.q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM alerts WHERE rule_id = ?`, ruleID).Scan(&n); err != nil {
		return fmt.Errorf("repo: delete detection rule %s: count alerts: %w", ruleID, err)
	}
	if n > 0 {
		return fmt.Errorf("repo: delete detection rule %s: %d alerts reference it (audit trail) — clear or keep them first: %w",
			ruleID, n, domain.ErrConflict)
	}
	res, err := r.q.ExecContext(ctx, `DELETE FROM detection_rules WHERE rule_id = ?`, ruleID)
	if err != nil {
		return fmt.Errorf("repo: delete detection rule %s: %w", ruleID, err)
	}
	return requireUpdated(res, "detection rule", ruleID)
}

// alertColumns is the canonical SELECT column list of alerts.
const alertColumns = `alert_id, rule_id, series_id, fired_at, cleared_at, acked_at, acked_by, state, last_value, first_seen, correlation_role, correlated_alert_id, correlated_resource_id`

// scanAlert scans one alerts row into a domain.Alert.
func scanAlert(s scanner) (domain.Alert, error) {
	var (
		a            domain.Alert
		state        string
		cleared      sql.NullString
		acked        sql.NullString
		ackedBy      sql.NullString
		lastVal      sql.NullFloat64
		firstTS      sql.NullString
		corrRole     sql.NullString
		corrAlert    sql.NullString
		corrResource sql.NullString
	)
	if err := s.Scan(&a.AlertID, &a.RuleID, &a.SeriesID, &a.FiredAt, &cleared, &acked, &ackedBy,
		&state, &lastVal, &firstTS, &corrRole, &corrAlert, &corrResource); err != nil {
		return domain.Alert{}, err
	}
	if _, err := domain.ParseAlertState(state); err != nil {
		return domain.Alert{}, fmt.Errorf("repo: alert %s: %w", a.AlertID, err)
	}
	if corrRole.String != "" {
		if _, err := domain.ParseCorrelationRole(corrRole.String); err != nil {
			return domain.Alert{}, fmt.Errorf("repo: alert %s: %w", a.AlertID, err)
		}
	}
	a.State = domain.AlertState(state)
	a.ClearedAt = stringFromNull(cleared)
	a.AckedAt = stringFromNull(acked)
	a.AckedBy = stringFromNull(ackedBy)
	a.FirstSeen = stringFromNull(firstTS)
	a.CorrelationRole = corrRole.String
	a.CorrelatedAlertID = corrAlert.String
	a.CorrelatedResourceID = corrResource.String
	if lastVal.Valid {
		a.LastValue = lastVal.Float64
	}
	return a, nil
}

// InsertFiringAlert stores one NEW firing alert. INSERT OR IGNORE makes it
// idempotent on BOTH keys — the PRIMARY KEY alert_id and the UNIQUE
// (rule_id, series_id, first_seen): a finding whose first_seen collides
// with an existing (cleared) row is suppressed, not resurrected (the
// honest corner: a manual clear of a still-violating series followed by a
// run over the SAME window keeps the clear; a later violation window has a
// fresh first_seen and fires a fresh alert). Returns created=false when the
// row was already there.
func (r *AlertRepo) InsertFiringAlert(ctx context.Context, a domain.Alert) (bool, error) {
	res, err := r.q.ExecContext(ctx,
		`INSERT OR IGNORE INTO alerts
  (alert_id, rule_id, series_id, fired_at, cleared_at, acked_at, acked_by, state, last_value, first_seen)
VALUES (?,?,?,?,NULL,NULL,NULL,'firing',?,?)`,
		a.AlertID, a.RuleID, a.SeriesID, a.FiredAt, a.LastValue, nullString(a.FirstSeen))
	if err != nil {
		return false, fmt.Errorf("repo: insert alert of %s/%s: %w", a.RuleID, a.SeriesID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("repo: insert alert of %s/%s: rows affected: %w", a.RuleID, a.SeriesID, err)
	}
	return n == 1, nil
}

// UpdateFiringEvidence refreshes last_value (and fired_at) of a FIRING
// alert whose violation persists — evidence freshness, first_seen stays
// immutable. Only firing alerts move (an acked alert's evidence is frozen
// at the human's floor). Unknown/moved-on id answers domain.ErrNotFound.
func (r *AlertRepo) UpdateFiringEvidence(ctx context.Context, alertID, firedAt string, lastValue float64) error {
	res, err := r.q.ExecContext(ctx,
		`UPDATE alerts SET fired_at = ?, last_value = ? WHERE alert_id = ? AND state = 'firing'`,
		firedAt, lastValue, alertID)
	if err != nil {
		return fmt.Errorf("repo: update alert evidence %s: %w", alertID, err)
	}
	return requireUpdated(res, "alert", alertID)
}

// UpdateAlertCorrelation sets the correlation verdict of one alert
// (migration v25, v0.12-R16). The verdict is ENGINE-managed observation
// state — every detect run rewrites it for the active error/warning alerts
// (idempotent content-wise: same graph + same alerts → same verdict), and
// cleared alerts simply stop being rewritten (their last verdict is the
// audit truth). The CHECK constraint on correlation_role is the backstop
// of the domain parse.
func (r *AlertRepo) UpdateAlertCorrelation(ctx context.Context, alertID, role, correlatedAlertID, correlatedResourceID string) error {
	_, err := r.q.ExecContext(ctx,
		`UPDATE alerts SET correlation_role = ?, correlated_alert_id = ?, correlated_resource_id = ? WHERE alert_id = ?`,
		role, correlatedAlertID, correlatedResourceID, alertID)
	if err != nil {
		return fmt.Errorf("repo: update alert correlation %s: %w", alertID, err)
	}
	return nil
}

// ClearAlert moves a firing OR acked alert to cleared (the engine recovery
// edge AND the human override share this one transition; ADR-0011 D3 has
// exactly three edges and no auto-re-open). Already-cleared answers
// domain.ErrConflict (the API maps it to 409); unknown id answers
// domain.ErrNotFound.
func (r *AlertRepo) ClearAlert(ctx context.Context, alertID, clearedAt string) error {
	res, err := r.q.ExecContext(ctx,
		`UPDATE alerts SET state = 'cleared', cleared_at = ? WHERE alert_id = ? AND state IN ('firing','acked')`,
		clearedAt, alertID)
	if err != nil {
		return fmt.Errorf("repo: clear alert %s: %w", alertID, err)
	}
	if err := requireUpdated(res, "alert", alertID); err != nil {
		// Distinguish "not found" from "found but already cleared".
		if _, getErr := r.GetAlertByID(ctx, alertID); getErr == nil {
			return fmt.Errorf("repo: clear alert %s: already cleared: %w", alertID, domain.ErrConflict)
		}
		return err
	}
	return nil
}

// AckAlert moves a FIRING alert to acked (the human edge — the only ack
// writer is the HTTP ack endpoint). Non-firing states answer
// domain.ErrConflict; unknown id domain.ErrNotFound.
func (r *AlertRepo) AckAlert(ctx context.Context, alertID, ackedAt, ackedBy string) error {
	res, err := r.q.ExecContext(ctx,
		`UPDATE alerts SET state = 'acked', acked_at = ?, acked_by = ? WHERE alert_id = ? AND state = 'firing'`,
		ackedAt, ackedBy, alertID)
	if err != nil {
		return fmt.Errorf("repo: ack alert %s: %w", alertID, err)
	}
	if err := requireUpdated(res, "alert", alertID); err != nil {
		if _, getErr := r.GetAlertByID(ctx, alertID); getErr == nil {
			return fmt.Errorf("repo: ack alert %s: not firing: %w", alertID, domain.ErrConflict)
		}
		return err
	}
	return nil
}

// GetAlertByID loads one alert; unknown id answers domain.ErrNotFound.
func (r *AlertRepo) GetAlertByID(ctx context.Context, alertID string) (domain.Alert, error) {
	row := r.q.QueryRowContext(ctx, `SELECT `+alertColumns+` FROM alerts WHERE alert_id = ?`, alertID)
	a, err := scanAlert(row)
	if err != nil {
		return domain.Alert{}, notFound("alert", alertID, err)
	}
	return a, nil
}

// ListActiveAlertsByRule returns the NON-CLEARED (firing|acked) alerts of
// one rule ordered by series_id — the engine's per-rule sync input.
func (r *AlertRepo) ListActiveAlertsByRule(ctx context.Context, ruleID string) ([]domain.Alert, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+alertColumns+` FROM alerts WHERE rule_id = ? AND state IN ('firing','acked') ORDER BY series_id`,
		ruleID)
	if err != nil {
		return nil, fmt.Errorf("repo: list active alerts of rule %s: %w", ruleID, err)
	}
	defer rows.Close()
	out := make([]domain.Alert, 0, 8)
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, fmt.Errorf("repo: scan alert: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: list active alerts of rule %s: %w", ruleID, err)
	}
	return out, nil
}

// ActiveAlertRef is one firing/acked alert joined with the rule's severity
// and the series's resource — the run-state mapping input (which resources
// are down/degraded because of alerts).
type ActiveAlertRef struct {
	domain.Alert
	Severity   domain.Severity
	ResourceID string
}

// ListActiveAlertRefs returns EVERY firing/acked alert of the database
// (all rules — a disabled rule's firing alert still holds the resource
// down until cleared) joined with rule severity and series resource_id,
// ordered by (rule_id, series_id, alert_id) — deterministic.
func (r *AlertRepo) ListActiveAlertRefs(ctx context.Context) ([]ActiveAlertRef, error) {
	rows, err := r.q.QueryContext(ctx, `
SELECT a.alert_id, a.rule_id, a.series_id, a.fired_at, a.cleared_at, a.acked_at, a.acked_by, a.state, a.last_value, a.first_seen,
       dr.severity, s.resource_id
FROM alerts a
JOIN detection_rules dr ON dr.rule_id = a.rule_id
JOIN metric_series s ON s.series_id = a.series_id
WHERE a.state IN ('firing','acked')
ORDER BY a.rule_id, a.series_id, a.alert_id`)
	if err != nil {
		return nil, fmt.Errorf("repo: list active alert refs: %w", err)
	}
	defer rows.Close()
	out := make([]ActiveAlertRef, 0, 8)
	for rows.Next() {
		var (
			ref      ActiveAlertRef
			state    string
			severity string
			cleared  sql.NullString
			acked    sql.NullString
			ackedBy  sql.NullString
			lastVal  sql.NullFloat64
			firstTS  sql.NullString
		)
		if err := rows.Scan(&ref.AlertID, &ref.RuleID, &ref.SeriesID, &ref.FiredAt, &cleared, &acked, &ackedBy,
			&state, &lastVal, &firstTS, &severity, &ref.ResourceID); err != nil {
			return nil, fmt.Errorf("repo: scan active alert ref: %w", err)
		}
		ref.State = domain.AlertState(state)
		ref.ClearedAt = stringFromNull(cleared)
		ref.AckedAt = stringFromNull(acked)
		ref.AckedBy = stringFromNull(ackedBy)
		ref.FirstSeen = stringFromNull(firstTS)
		if lastVal.Valid {
			ref.LastValue = lastVal.Float64
		}
		if _, err := domain.ParseAlertState(state); err != nil {
			return nil, fmt.Errorf("repo: active alert ref %s: %w", ref.AlertID, err)
		}
		if _, err := domain.ParseSeverity(severity); err != nil {
			return nil, fmt.Errorf("repo: active alert ref %s rule %s: %w", ref.AlertID, ref.RuleID, err)
		}
		ref.Severity = domain.Severity(severity)
		out = append(out, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: list active alert refs: %w", err)
	}
	return out, nil
}

// AlertFilter parameterizes ListAlerts: closed-enum filters validated by
// the API layer ("" = no filter); pagination limit/offset (0 = the full
// set — the engine and tests use unpaginated reads).
type AlertFilter struct {
	State      string
	Severity   string // matches the RULE's severity (JOIN detection_rules)
	ResourceID string
	Role       string // correlation verdict filter: primary | secondary ("" = all)
	Limit      int
	Offset     int
}

// AlertRow is one alerts listing row: the alert itself plus the joined rule
// fields (severity, comparator, threshold, window, label) and the series →
// resource projection (the AI needs the resource, not the bare series_id).
type AlertRow struct {
	domain.Alert
	MetricKey    string
	Comparator   domain.Comparator
	Threshold    float64
	Window       int
	Severity     domain.Severity
	RuleLabel    string
	ResourceID   string
	ResourceType string
	NaturalKey   string
	ResourceName string
}

// ListAlerts returns the alerts (newest fired_at first, alert_id ASC as the
// deterministic tie-break) joined with rule + series + resource, with the
// closed-enum filters and pagination. The second return is the TOTAL count
// of the unpaginated filter set (the envelope's total).
func (r *AlertRepo) ListAlerts(ctx context.Context, f AlertFilter) ([]AlertRow, int, error) {
	where := []string{"1=1"}
	args := make([]any, 0, 8)
	if f.State != "" {
		where = append(where, "a.state = ?")
		args = append(args, f.State)
	}
	if f.Severity != "" {
		where = append(where, "dr.severity = ?")
		args = append(args, f.Severity)
	}
	if f.ResourceID != "" {
		where = append(where, "s.resource_id = ?")
		args = append(args, f.ResourceID)
	}
	if f.Role != "" {
		// Correlation verdict filter (v0.12-R16): the engine-managed 根因/
		// 次生 marks. Validated upstream (domain parse) — the WHERE is a
		// closed-enum equality, never user text.
		where = append(where, "a.correlation_role = ?")
		args = append(args, f.Role)
	}
	whereSQL := strings.Join(where, " AND ")

	var total int
	if err := r.q.QueryRowContext(ctx, `
SELECT COUNT(*) FROM alerts a
JOIN detection_rules dr ON dr.rule_id = a.rule_id
JOIN metric_series s ON s.series_id = a.series_id
WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("repo: count alerts: %w", err)
	}

	query := `
SELECT a.alert_id, a.rule_id, a.series_id, a.fired_at, a.cleared_at, a.acked_at, a.acked_by, a.state, a.last_value, a.first_seen,
       a.correlation_role, a.correlated_alert_id, a.correlated_resource_id,
       dr.metric_key, dr.comparator, dr.threshold, dr.window, dr.severity, dr.label,
       s.resource_id, res.resource_type, res.natural_key, res.name
FROM alerts a
JOIN detection_rules dr ON dr.rule_id = a.rule_id
JOIN metric_series s ON s.series_id = a.series_id
JOIN resources res ON res.id = s.resource_id
WHERE ` + whereSQL + `
ORDER BY a.fired_at DESC, a.alert_id ASC`
	limit, offset := paging(f.Limit, f.Offset)
	if limit >= 0 {
		query += fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
	}

	rows, err := r.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("repo: list alerts: %w", err)
	}
	defer rows.Close()
	out := make([]AlertRow, 0, 16)
	for rows.Next() {
		var (
			row        AlertRow
			state      string
			comparator string
			severity   string
			cleared    sql.NullString
			acked      sql.NullString
			ackedBy    sql.NullString
			lastVal    sql.NullFloat64
			firstTS    sql.NullString
			corrRole   sql.NullString
			corrAlert  sql.NullString
			corrRes    sql.NullString
		)
		if err := rows.Scan(&row.AlertID, &row.RuleID, &row.SeriesID, &row.FiredAt, &cleared, &acked, &ackedBy,
			&state, &lastVal, &firstTS, &corrRole, &corrAlert, &corrRes,
			&row.MetricKey, &comparator, &row.Threshold, &row.Window, &severity, &row.RuleLabel,
			&row.ResourceID, &row.ResourceType, &row.NaturalKey, &row.ResourceName); err != nil {
			return nil, 0, fmt.Errorf("repo: scan alert row: %w", err)
		}
		if _, err := domain.ParseAlertState(state); err != nil {
			return nil, 0, fmt.Errorf("repo: alert row %s: %w", row.AlertID, err)
		}
		if _, err := domain.ParseComparator(comparator); err != nil {
			return nil, 0, fmt.Errorf("repo: alert row %s rule %s: %w", row.AlertID, row.RuleID, err)
		}
		if _, err := domain.ParseSeverity(severity); err != nil {
			return nil, 0, fmt.Errorf("repo: alert row %s rule %s: %w", row.AlertID, row.RuleID, err)
		}
		if corrRole.String != "" {
			if _, err := domain.ParseCorrelationRole(corrRole.String); err != nil {
				return nil, 0, fmt.Errorf("repo: alert row %s: %w", row.AlertID, err)
			}
		}
		row.State = domain.AlertState(state)
		row.Comparator = domain.Comparator(comparator)
		row.Severity = domain.Severity(severity)
		row.ClearedAt = stringFromNull(cleared)
		row.AckedAt = stringFromNull(acked)
		row.AckedBy = stringFromNull(ackedBy)
		row.FirstSeen = stringFromNull(firstTS)
		row.CorrelationRole = corrRole.String
		row.CorrelatedAlertID = corrAlert.String
		row.CorrelatedResourceID = corrRes.String
		if lastVal.Valid {
			row.LastValue = lastVal.Float64
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("repo: list alerts: %w", err)
	}
	return out, total, nil
}
