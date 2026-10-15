package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/app"
	"github.com/mmqz/netops-lab/internal/detect"
	"github.com/mmqz/netops-lab/internal/testsupport"
)

// TestDetectionRunHistory pins the v0.11-R11 run-history contract end to
// end: every POST /api/detection/run leaves a receipt (the response
// envelope carries the SAME runId the listing shows), the listing is
// newest-first with the closed source set, and pagination works.
func TestDetectionRunHistory(t *testing.T) {
	srv := newDetectTestServer(t)

	// Empty history: the contract's zero shape.
	resp, body := doRequest(t, srv, http.MethodGet, "/api/detection/runs?page=1&pageSize=20", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("empty runs list: %d\n%s", resp.StatusCode, body)
	}
	var empty struct {
		Items    []json.RawMessage `json:"items"`
		Total    int               `json:"total"`
		Page     int               `json:"page"`
		PageSize int               `json:"pageSize"`
	}
	decodeJSON(t, "empty runs", body, &empty)
	if empty.Total != 0 || len(empty.Items) != 0 || empty.Page != 1 {
		t.Errorf("empty runs = %+v, want total 0 / nil items / page 1", empty)
	}

	// Two sweeps (a windowed one and a default one): each response
	// carries the receipt block.
	first := postRun(t, srv, `{"from":"2020-01-01T00:00:00Z"}`)
	second := postRun(t, srv, `{}`)
	if first.RunID == second.RunID {
		t.Errorf("two runs share runId %q, want unique receipts", first.RunID)
	}
	if first.Source != "api" || second.Source != "api" {
		t.Errorf("run envelope source = %q / %q, want api/api", first.Source, second.Source)
	}
	if first.StartedAt == "" || first.FinishedAt == "" || first.DurationMS < 0 {
		t.Errorf("run envelope bracket = %q..%q (%dms), want populated", first.StartedAt, first.FinishedAt, first.DurationMS)
	}
	if first.From != "2020-01-01T00:00:00Z" || second.From != "" {
		t.Errorf("run envelope window echo = %q / %q, want the bounds", first.From, second.From)
	}

	// The listing: newest first, the SAME runIds, the counts echo.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/detection/runs?page=1&pageSize=1", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("runs list: %d\n%s", resp.StatusCode, body)
	}
	var listed struct {
		Items []struct {
			RunID         string `json:"runId"`
			Source        string `json:"source"`
			Findings      int    `json:"findings"`
			AlertsCreated int    `json:"alertsCreated"`
			Error         string `json:"error"`
		} `json:"items"`
		Total    int `json:"total"`
		PageSize int `json:"pageSize"`
	}
	decodeJSON(t, "runs list", body, &listed)
	if listed.Total != 2 || len(listed.Items) != 1 || listed.PageSize != 1 {
		t.Fatalf("runs list = %+v, want total 2 / 1 item / pageSize 1", listed)
	}
	if listed.Items[0].RunID != second.RunID {
		t.Errorf("newest run = %s, want %s (newest first)", listed.Items[0].RunID, second.RunID)
	}
	if listed.Items[0].Source != "api" || listed.Items[0].Error != "" {
		t.Errorf("listed run = %+v, want api / no error", listed.Items[0])
	}
	if listed.Items[0].Findings != second.Findings {
		t.Errorf("listed findings = %d, envelope findings = %d, want the echo", listed.Items[0].Findings, second.Findings)
	}
}

// postRun fires one detection sweep and decodes the receipt block.
func postRun(t *testing.T, srv *httptest.Server, reqBody string) runEnvelope {
	t.Helper()
	resp, body := doRequest(t, srv, http.MethodPost, "/api/detection/run", []byte(reqBody), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detection run %s: %d\n%s", reqBody, resp.StatusCode, body)
	}
	var env runEnvelope
	decodeJSON(t, "detection run", body, &env)
	return env
}

// runEnvelope is the POST /api/detection/run receipt block (the v0.10
// result fields are asserted elsewhere; this pins the v0.11 additions).
type runEnvelope struct {
	RunID      string `json:"runId"`
	Source     string `json:"source"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
	DurationMS int64  `json:"durationMs"`
	From       string `json:"from"`
	Findings   int    `json:"findings"`
}

// TestAlertsStatsEndpoint pins GET /api/alerts/stats: the closed
// zero-filled shape on an empty database, and state totals consistent
// with the /api/alerts listing totals after a real sweep.
func TestAlertsStatsEndpoint(t *testing.T) {
	srv := newDetectTestServer(t)

	// The seeded scenario needs a window-1 tripwire rule to fire on the
	// 2-day degrade curve (the starter set needs ≥3 days — the
	// TestDetectionRunAndAlerts precedent).
	sensitive := []byte(`{"metricKey":"optical_power_rx_avg_dbm","comparator":"lte","threshold":-8.1,"window":1,"severity":"warning","label":"统计测试触发"}`)
	resp, body := doRequest(t, srv, http.MethodPost, "/api/detection/rules", sensitive, "application/json")
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("custom rule create: %d\n%s", resp.StatusCode, body)
	}

	// No alerts yet: all zeros, but EVERY closed key present.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/alerts/stats", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stats: %d\n%s", resp.StatusCode, body)
	}
	var stats struct {
		Total            int            `json:"total"`
		ByState          map[string]int `json:"byState"`
		ActiveBySeverity map[string]int `json:"activeBySeverity"`
	}
	decodeJSON(t, "stats", body, &stats)
	if stats.Total != 0 {
		t.Errorf("empty stats total = %d, want 0", stats.Total)
	}
	for _, state := range []string{"firing", "acked", "cleared"} {
		if _, ok := stats.ByState[state]; !ok {
			t.Errorf("ByState missing closed key %q: %v", state, stats.ByState)
		}
	}
	for _, sev := range []string{"error", "warning", "info"} {
		if _, ok := stats.ActiveBySeverity[sev]; !ok {
			t.Errorf("ActiveBySeverity missing closed key %q: %v", sev, stats.ActiveBySeverity)
		}
	}

	// Run the sweep, ack one, clear one — the state totals must agree
	// with the listing's totals (the dashboard reads one, the queue the
	// other; they must never disagree).
	postRun(t, srv, `{}`)
	resp, body = doRequest(t, srv, http.MethodGet, "/api/alerts?state=firing&page=1&pageSize=50", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("firing list: %d\n%s", resp.StatusCode, body)
	}
	var firing struct {
		Items []struct {
			AlertID string `json:"alertId"`
		} `json:"items"`
		Total int `json:"total"`
	}
	decodeJSON(t, "firing list", body, &firing)
	if firing.Total == 0 {
		t.Fatal("sweep produced no firing alerts — seeded scenario regressed")
	}
	// Ack the first, clear the second (if present).
	if len(firing.Items) > 0 {
		doRequest(t, srv, http.MethodPost, "/api/alerts/"+firing.Items[0].AlertID+"/ack", []byte(`{"ackedBy":"stats-tester"}`), "application/json")
	}
	if len(firing.Items) > 1 {
		doRequest(t, srv, http.MethodPost, "/api/alerts/"+firing.Items[1].AlertID+"/clear", nil, "")
	}

	resp, body = doRequest(t, srv, http.MethodGet, "/api/alerts/stats", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stats after lifecycle: %d\n%s", resp.StatusCode, body)
	}
	decodeJSON(t, "stats after lifecycle", body, &stats)
	if stats.Total != firing.Total {
		t.Errorf("stats total = %d, want the listing total %d", stats.Total, firing.Total)
	}
	if stats.ByState["acked"] != 1 {
		t.Errorf("ByState[acked] = %d, want 1", stats.ByState["acked"])
	}
	wantCleared := 0
	if len(firing.Items) > 1 {
		wantCleared = 1
	}
	if stats.ByState["cleared"] != wantCleared {
		t.Errorf("ByState[cleared] = %d, want %d", stats.ByState["cleared"], wantCleared)
	}
	if stats.ByState["firing"] != firing.Total-1-wantCleared {
		t.Errorf("ByState[firing] = %d, want %d", stats.ByState["firing"], firing.Total-1-wantCleared)
	}
	// Active = firing + acked; the acked one is warning-severity
	// (the degrade scenario's starter rules are warning/error mix —
	// assert only that active > 0 and total invariant holds).
	active := stats.ByState["firing"] + stats.ByState["acked"]
	if active == 0 {
		t.Error("active count = 0, want > 0 after the sweep")
	}
	sum := stats.ByState["firing"] + stats.ByState["acked"] + stats.ByState["cleared"]
	if sum != stats.Total {
		t.Errorf("state sum %d != total %d", sum, stats.Total)
	}
}

// TestHealthSchedulerBlock pins the scheduler's health projection: the
// block is ALWAYS present (closed shape), reports disabled when the
// patrol is off, and reports enabled/interval/nextRunAt when wired.
func TestHealthSchedulerBlock(t *testing.T) {
	srv := newDetectTestServer(t)

	// Default: the patrol is off — the block exists and says so.
	resp, body := doRequest(t, srv, http.MethodGet, "/api/health", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health: %d", resp.StatusCode)
	}
	var health struct {
		Scheduler struct {
			Enabled         bool   `json:"enabled"`
			IntervalSeconds int    `json:"intervalSeconds"`
			Running         bool   `json:"running"`
			Rounds          int    `json:"rounds"`
			LastRunAt       string `json:"lastRunAt"`
			NextRunAt       string `json:"nextRunAt"`
		} `json:"scheduler"`
	}
	decodeJSON(t, "health", body, &health)
	if health.Scheduler.Enabled || health.Scheduler.IntervalSeconds != 0 || health.Scheduler.NextRunAt != "" {
		t.Errorf("health scheduler block = %+v, want the disabled snapshot", health.Scheduler)
	}

	// With the patrol wired (30s — no round fires during the test, the
	// status projection is what is pinned).
	dir := testsupport.TempDir(t)
	a, err := app.New(app.Config{
		DBPath:  filepath.Join(dir, "netops.db"),
		DataDir: dir,
	})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	a.API.Schedule = detect.NewScheduler(30*time.Second, a.DB, a.Detect, nil, a.Log)
	srv2 := newAPITestServerFromApp(t, a)

	resp, body = doRequest(t, srv2, http.MethodGet, "/api/health", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health with scheduler: %d", resp.StatusCode)
	}
	decodeJSON(t, "health with scheduler", body, &health)
	if !health.Scheduler.Enabled || health.Scheduler.IntervalSeconds != 30 {
		t.Errorf("health scheduler block = %+v, want enabled/30s", health.Scheduler)
	}
	if health.Scheduler.Running || health.Scheduler.Rounds != 0 || health.Scheduler.LastRunAt != "" {
		t.Errorf("health scheduler block = %+v, want idle (Run never started)", health.Scheduler)
	}
}
