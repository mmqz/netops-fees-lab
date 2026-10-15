package api

// detection.go — the W23 alert detection HTTP surface (ADR-0011 D2/D3,
// design data-model-v1.md §12-B2, issue #23): the detection rule CRUD, the
// engine run endpoint and the alert lifecycle endpoints (list / ack /
// clear). The handlers are thin projections over the SAME engine instance
// the CLI uses (services composition root) and the repo layer — no second
// detection path exists. The MCP tool surface stays frozen at the five
// read-only tools (ADR-0011 D5): AI reads alerts via HTTP.

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"time"

	"github.com/mmqz/netops-lab/internal/detect"
	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/metrics"
	"github.com/mmqz/netops-lab/internal/repo"
)

// ruleItem is one detection rule of the list/create/patch responses.
type ruleItem struct {
	RuleID     string  `json:"ruleId"`
	MetricKey  string  `json:"metricKey"`
	Comparator string  `json:"comparator"`
	Threshold  float64 `json:"threshold"`
	Window     int     `json:"window"`
	Severity   string  `json:"severity"`
	Enabled    bool    `json:"enabled"`
	Label      string  `json:"label"`
	CreatedAt  string  `json:"createdAt"`
	UpdatedAt  string  `json:"updatedAt"`
}

// ruleItemOf projects a domain rule.
func ruleItemOf(r domain.DetectionRule) ruleItem {
	return ruleItem{
		RuleID: r.RuleID, MetricKey: r.MetricKey, Comparator: r.Comparator.String(),
		Threshold: r.Threshold, Window: r.Window, Severity: r.Severity.String(),
		Enabled: r.Enabled, Label: r.Label, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

// ruleRequest is the POST /api/detection/rules body: ruleId is optional
// (auto-generated when absent), everything else is validated against the
// closed catalogs.
type ruleRequest struct {
	RuleID     string   `json:"ruleId"`
	MetricKey  string   `json:"metricKey"`
	Comparator string   `json:"comparator"`
	Threshold  *float64 `json:"threshold"`
	Window     *int     `json:"window"`
	Severity   string   `json:"severity"`
	Label      string   `json:"label"`
	Enabled    *bool    `json:"enabled"`
}

// maxRuleWindow bounds the rule window (1 day … 1 year of consecutive
// points — the debounce cannot be unbounded config).
const maxRuleWindow = 365

// handleDetectionRulesList implements GET /api/detection/rules: every rule
// (enabled and disabled), rule_id order — deterministic. The LAZY default
// seed runs first when the table is empty (data, not schema: a user who
// deleted rules never gets them resurrected).
func (s *Server) handleDetectionRulesList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.db.DetectionRules.SeedDefaultDetectionRules(ctx); err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	rules, err := s.db.DetectionRules.ListRules(ctx, false)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	items := make([]ruleItem, 0, len(rules))
	for _, rule := range rules {
		items = append(items, ruleItemOf(rule))
	}
	respondJSON(w, http.StatusOK, struct {
		Items []ruleItem `json:"items"`
		Total int        `json:"total"`
	}{Items: items, Total: len(items)})
}

// validateRuleFields validates the shared create fields. ruleID may be
// empty (auto-generation); the caller's now stamps the bookkeeping
// timestamps (config edits are bookkeeping, not a data path — the W22
// manual-channel precedent).
func validateRuleFields(w http.ResponseWriter, req ruleRequest, ruleID string) (domain.DetectionRule, bool) {
	if _, ok := metrics.SpecForKey(req.MetricKey); !ok {
		respondErrorf(w, http.StatusBadRequest, "bad_request",
			"metricKey %q 不在封闭指标目录中（GET /api/metrics/keys 查看全部键）", req.MetricKey)
		return domain.DetectionRule{}, false
	}
	comparator, err := domain.ParseComparator(req.Comparator)
	if err != nil {
		respondErrorf(w, http.StatusBadRequest, "bad_request", "comparator %q 不是 gt/lt/gte/lte", req.Comparator)
		return domain.DetectionRule{}, false
	}
	if req.Threshold == nil {
		respondErrorf(w, http.StatusBadRequest, "bad_request", "threshold 必填（数值）")
		return domain.DetectionRule{}, false
	}
	if math.IsNaN(*req.Threshold) || math.IsInf(*req.Threshold, 0) {
		respondErrorf(w, http.StatusBadRequest, "bad_request", "threshold 必须是有限数值")
		return domain.DetectionRule{}, false
	}
	window := 1
	if req.Window != nil {
		if *req.Window < 1 || *req.Window > maxRuleWindow {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "window 必须在 1..%d（连续越限点数，去抖）", maxRuleWindow)
			return domain.DetectionRule{}, false
		}
		window = *req.Window
	}
	severity, err := domain.ParseSeverity(req.Severity)
	if err != nil {
		respondErrorf(w, http.StatusBadRequest, "bad_request", "severity %q 不是 error/warning/info", req.Severity)
		return domain.DetectionRule{}, false
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	return domain.DetectionRule{
		RuleID: ruleID, MetricKey: req.MetricKey, Comparator: comparator,
		Threshold: *req.Threshold, Window: window, Severity: severity,
		Enabled: enabled, Label: req.Label,
	}, true
}

// handleDetectionRulesCreate implements POST /api/detection/rules: 201 with
// the stored rule; 400 on any closed-catalog violation; 409 on a duplicate
// rule_id. The ruleId is auto-generated (dr_<uuid>) when absent.
func (s *Server) handleDetectionRulesCreate(w http.ResponseWriter, r *http.Request) {
	var req ruleRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		respondErrorf(w, http.StatusBadRequest, "bad_request", "请求体不是有效的 JSON：%v", err)
		return
	}
	ruleID := req.RuleID
	if ruleID == "" {
		ruleID = domain.NewID("dr")
	}
	rule, ok := validateRuleFields(w, req, ruleID)
	if !ok {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	rule.CreatedAt, rule.UpdatedAt = now, now

	if err := s.db.DetectionRules.InsertRule(r.Context(), rule); err != nil {
		s.respondErrorFor(w, r, err, "duplicate_rule_id")
		return
	}
	respondJSON(w, http.StatusCreated, ruleItemOf(rule))
}

// rulePatchRequest is the PATCH /api/detection/rules/{id} body: ONLY the
// mutable fields. metricKey (and ruleId) are the rule's IDENTITY and
// immutable — a change answers 400 with the honest reason (a rule on
// another metric is a NEW rule).
type rulePatchRequest struct {
	MetricKey  *string  `json:"metricKey"`
	RuleID     *string  `json:"ruleId"`
	Comparator *string  `json:"comparator"`
	Threshold  *float64 `json:"threshold"`
	Window     *int     `json:"window"`
	Severity   *string  `json:"severity"`
	Enabled    *bool    `json:"enabled"`
	Label      *string  `json:"label"`
}

// handleDetectionRulesPatch implements PATCH /api/detection/rules/{id}:
// enabled / threshold / label / window / comparator / severity are mutable
// (rules are config data, ADR-0011 D2); metricKey is immutable (400).
// 404 unknown rule; the edit stamps updated_at with the server clock
// (config bookkeeping).
func (s *Server) handleDetectionRulesPatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rule, err := s.db.DetectionRules.GetRuleByID(r.Context(), id)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	var req rulePatchRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		respondErrorf(w, http.StatusBadRequest, "bad_request", "请求体不是有效的 JSON：%v", err)
		return
	}
	// Identity fields are immutable.
	if req.MetricKey != nil && *req.MetricKey != rule.MetricKey {
		respondErrorf(w, http.StatusBadRequest, "bad_request",
			"metricKey 不可修改（规则的身份字段）：当前 %s，换指标请新建规则并禁用本条", rule.MetricKey)
		return
	}
	if req.RuleID != nil && *req.RuleID != rule.RuleID {
		respondErrorf(w, http.StatusBadRequest, "bad_request", "ruleId 不可修改（规则的主键）")
		return
	}
	if req.Comparator != nil {
		comparator, err := domain.ParseComparator(*req.Comparator)
		if err != nil {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "comparator %q 不是 gt/lt/gte/lte", *req.Comparator)
			return
		}
		rule.Comparator = comparator
	}
	if req.Threshold != nil {
		if math.IsNaN(*req.Threshold) || math.IsInf(*req.Threshold, 0) {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "threshold 必须是有限数值")
			return
		}
		rule.Threshold = *req.Threshold
	}
	if req.Window != nil {
		if *req.Window < 1 || *req.Window > maxRuleWindow {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "window 必须在 1..%d", maxRuleWindow)
			return
		}
		rule.Window = *req.Window
	}
	if req.Severity != nil {
		severity, err := domain.ParseSeverity(*req.Severity)
		if err != nil {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "severity %q 不是 error/warning/info", *req.Severity)
			return
		}
		rule.Severity = severity
	}
	if req.Enabled != nil {
		rule.Enabled = *req.Enabled
	}
	if req.Label != nil {
		rule.Label = *req.Label
	}
	rule.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	if err := s.db.DetectionRules.UpdateRule(r.Context(), rule); err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, ruleItemOf(rule))
}

// handleDetectionRulesDelete implements DELETE /api/detection/rules/{id}:
// deleting a rule that has alerts is BLOCKED (409 conflict — alerts are
// the audit trail, orphaning them would erase the history's meaning);
// an alert-free rule deletes cleanly (200 {deleted:true}); 404 unknown.
// Disabling (enabled=false) is the soft alternative: the rule stops
// evaluating but its alerts and history stay.
func (s *Server) handleDetectionRulesDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.db.DetectionRules.DeleteRule(r.Context(), id); err != nil {
		s.respondErrorFor(w, r, err, "rule_has_alerts")
		return
	}
	respondJSON(w, http.StatusOK, struct {
		RuleID  string `json:"ruleId"`
		Deleted bool   `json:"deleted"`
	}{RuleID: id, Deleted: true})
}

// detectRunRequest is the POST /api/detection/run body: optional RFC3339
// window bounds ("" / absent = unbounded on that side).
type detectRunRequest struct {
	From *string `json:"from"`
	To   *string `json:"to"`
}

// handleDetectionRun implements POST /api/detection/run: ONE engine sweep
// over the whole database (the daily-inspection detection half) — seeds
// default rules when the table is empty, evaluates every enabled rule's
// metric_key series in [from, to], syncs the alert state machine and
// writes the alert-sourced run-states, all in one transaction. Deterministic
// given database state + rules + window; a re-run over unchanged data
// reports zero mutations. 200 with the run envelope; 400 on malformed
// bounds.
func (s *Server) handleDetectionRun(w http.ResponseWriter, r *http.Request) {
	var req detectRunRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		respondErrorf(w, http.StatusBadRequest, "bad_request", "请求体不是有效的 JSON：%v", err)
		return
	}
	opts := detect.Options{}
	if req.From != nil && *req.From != "" {
		t, err := parseRFC3339(*req.From)
		if err != nil {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "from 参数 %s", err.Error())
			return
		}
		opts.From = t.Format(time.RFC3339)
	}
	if req.To != nil && *req.To != "" {
		t, err := parseRFC3339(*req.To)
		if err != nil {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "to 参数 %s", err.Error())
			return
		}
		opts.To = t.Format(time.RFC3339)
	}
	res, err := s.detect.Detect(r.Context(), s.db, opts)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	// Notification side path (v0.10-R8): the run summary is delivered to
	// the configured webhook AFTER the 200 is computed, on a background
	// context — the delivery never blocks the response and never fails
	// the run (notify logs the failure at warn level itself). The nil
	// receiver is a no-op (channel off).
	if s.Webhook != nil {
		resCopy := res
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = s.Webhook.NotifyDetectionRun(ctx, resCopy)
		}()
	}
	respondJSON(w, http.StatusOK, res)
}

// alertItem is one alert of the list / ack / clear responses: the alert
// itself + the joined rule fields + the resource block (the AI needs the
// resource, not the bare series_id).
type alertItem struct {
	AlertID    string  `json:"alertId"`
	RuleID     string  `json:"ruleId"`
	RuleLabel  string  `json:"ruleLabel"`
	MetricKey  string  `json:"metricKey"`
	Comparator string  `json:"comparator"`
	Threshold  float64 `json:"threshold"`
	Window     int     `json:"window"`
	Severity   string  `json:"severity"`
	SeriesID   string  `json:"seriesId"`
	State      string  `json:"state"`
	FiredAt    string  `json:"firedAt"`
	FirstSeen  string  `json:"firstSeen"`
	ClearedAt  string  `json:"clearedAt"`
	AckedAt    string  `json:"ackedAt"`
	AckedBy    string  `json:"ackedBy"`
	LastValue  float64 `json:"lastValue"`
	Resource   struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		NaturalKey string `json:"naturalKey"`
		Name       string `json:"name"`
	} `json:"resource"`
}

// alertItemOf projects one repo.AlertRow.
func alertItemOf(row repo.AlertRow) alertItem {
	var item alertItem
	item.AlertID = row.AlertID
	item.RuleID = row.RuleID
	item.RuleLabel = row.RuleLabel
	item.MetricKey = row.MetricKey
	item.Comparator = row.Comparator.String()
	item.Threshold = row.Threshold
	item.Window = row.Window
	item.Severity = row.Severity.String()
	item.SeriesID = row.SeriesID
	item.State = row.State.String()
	item.FiredAt = row.FiredAt
	item.FirstSeen = row.FirstSeen
	item.ClearedAt = row.ClearedAt
	item.AckedAt = row.AckedAt
	item.AckedBy = row.AckedBy
	item.LastValue = row.LastValue
	item.Resource.ID = row.ResourceID
	item.Resource.Type = row.ResourceType
	item.Resource.NaturalKey = row.NaturalKey
	item.Resource.Name = row.ResourceName
	return item
}

// handleAlertsList implements GET /api/alerts?state=&severity=&resource_id=
// &page=&pageSize= — the alert listing with the rule + series → resource
// JOINs, newest fired_at first (alert_id ASC tie-break), page/pageSize
// pagination of the shared contract. state ∈ firing|acked|cleared,
// severity ∈ error|warning|info (both 400 on anything else; "" = no
// filter). 200 {items, total, page, pageSize}.
func (s *Server) handleAlertsList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	state := q.Get("state")
	if state != "" {
		if _, err := domain.ParseAlertState(state); err != nil {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "state %q 不是 firing/acked/cleared", state)
			return
		}
	}
	severity := q.Get("severity")
	if severity != "" {
		if _, err := domain.ParseSeverity(severity); err != nil {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "severity %q 不是 error/warning/info", severity)
			return
		}
	}
	page, pageSize, ok := parsePagination(w, r)
	if !ok {
		return
	}
	rows, total, err := s.db.Alerts.ListAlerts(r.Context(), repo.AlertFilter{
		State: state, Severity: severity, ResourceID: q.Get("resource_id"),
		Limit: pageSize, Offset: (page - 1) * pageSize,
	})
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	items := make([]alertItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, alertItemOf(row))
	}
	respondJSON(w, http.StatusOK, struct {
		Items    []alertItem `json:"items"`
		Total    int         `json:"total"`
		Page     int         `json:"page"`
		PageSize int         `json:"pageSize"`
	}{Items: items, Total: total, Page: page, PageSize: pageSize})
}

// ackRequest is the POST /api/alerts/{id}/ack body.
type ackRequest struct {
	AckedBy string  `json:"ackedBy"`
	TS      *string `json:"ts"`
}

// handleAlertAck implements POST /api/alerts/{id}/ack — the ONLY human
// ack edge (firing→acked; anything else 409). ackedBy is required (the
// human identity — audit trail). ts defaults to the server clock
// (bookkeeping, the manual-channel precedent); an explicit RFC3339 ts is
// accepted for reproducible history. 200 with the updated alert.
func (s *Server) handleAlertAck(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req ackRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		respondErrorf(w, http.StatusBadRequest, "bad_request", "请求体不是有效的 JSON：%v", err)
		return
	}
	if req.AckedBy == "" {
		respondErrorf(w, http.StatusBadRequest, "bad_request", "ackedBy 必填（确认人，告警审计字段）")
		return
	}
	ts := time.Now().UTC().Format(time.RFC3339)
	if req.TS != nil && *req.TS != "" {
		t, err := parseRFC3339(*req.TS)
		if err != nil {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "ts 参数 %s", err.Error())
			return
		}
		ts = t.Format(time.RFC3339)
	}
	if err := s.db.Alerts.AckAlert(r.Context(), id, ts, req.AckedBy); err != nil {
		s.respondErrorFor(w, r, err, "not_firing")
		return
	}
	respondJSON(w, http.StatusOK, alertLifecycleResponse{
		AlertID: id, State: domain.AlertStateAcked.String(), AckedAt: ts, AckedBy: req.AckedBy,
	})
}

// clearRequest is the POST /api/alerts/{id}/clear body (optional ts).
type clearRequest struct {
	TS *string `json:"ts"`
}

// handleAlertClear implements POST /api/alerts/{id}/clear — the HUMAN
// override edge (firing|acked→cleared; already cleared 409). The engine
// recovery edge shares the same repo transition with a data ts. ts
// defaults to the server clock; explicit RFC3339 accepted. 200 with the
// updated alert. NO delete endpoint exists: alerts are the audit trail.
func (s *Server) handleAlertClear(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// Clear is a no-argument action: an EMPTY body is valid (ts optional)
	// — only malformed non-empty JSON is a 400.
	var req clearRequest
	if raw, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20)); len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "请求体不是有效的 JSON：%v", err)
			return
		}
	}
	ts := time.Now().UTC().Format(time.RFC3339)
	if req.TS != nil && *req.TS != "" {
		t, err := parseRFC3339(*req.TS)
		if err != nil {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "ts 参数 %s", err.Error())
			return
		}
		ts = t.Format(time.RFC3339)
	}
	if err := s.db.Alerts.ClearAlert(r.Context(), id, ts); err != nil {
		s.respondErrorFor(w, r, err, "already_cleared")
		return
	}
	respondJSON(w, http.StatusOK, alertLifecycleResponse{
		AlertID: id, State: domain.AlertStateCleared.String(), ClearedAt: ts,
	})
}

// alertLifecycleResponse is the ack/clear response: the alert id, the new
// state and the transition stamp.
type alertLifecycleResponse struct {
	AlertID   string `json:"alertId"`
	State     string `json:"state"`
	AckedAt   string `json:"ackedAt,omitempty"`
	AckedBy   string `json:"ackedBy,omitempty"`
	ClearedAt string `json:"clearedAt,omitempty"`
}
