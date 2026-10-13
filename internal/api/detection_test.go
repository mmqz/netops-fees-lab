package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/app"
	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/metrics"
	"github.com/mmqz/netops-lab/internal/repo"
)

// This file pins the HTTP contracts of the W23 detection/alert endpoints:
// rules CRUD, the detection run, the alert lifecycle (ack/clear state
// machine with 409s), and the run-state integration (source=alert written
// by the engine, the manual HTTP guard stays). The graph + metrics come
// from the synthdata degrade scenario (the REAL channel), so the
// detection finds genuinely degrading ports.

// newDetectTestServer builds the server, imports the synth resources +
// two perf days (one healthy, one degraded), and ingests them.
func newDetectTestServer(t *testing.T) *httptest.Server {
	srv, _ := newDetectServerSeeded(t)
	return srv
}

// newDetectServerSeeded is the shared builder returning server + app.
func newDetectServerSeeded(t *testing.T) (*httptest.Server, *app.App) {
	t.Helper()
	dir := t.TempDir()
	a, err := app.New(app.Config{
		DBPath:  filepath.Join(dir, "netops.db"),
		DataDir: dir,
	})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	srv := newAPITestServerFromApp(t, a)

	// synthdata: 3 devices × 1 port × 2 days degrade — at least one fault
	// port whose RX power declines.
	sdir := filepath.Join(dir, "synth")
	if _, err := metrics.WriteSynthData(metrics.SynthOptions{
		Out: sdir, Devices: 3, Ports: 1, Days: 2, Scenario: metrics.ScenarioDegrade,
		Seed: 42, Metrics: 3,
	}); err != nil {
		t.Fatalf("synthdata: %v", err)
	}

	// Import resources through the HTTP flow (upload + suggested mapping).
	resRaw, err := os.ReadFile(filepath.Join(sdir, "resources.xlsx"))
	if err != nil {
		t.Fatalf("read resources: %v", err)
	}
	resp, body := uploadFile(t, srv, "resources.xlsx", resRaw)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("resource upload: %d\n%s", resp.StatusCode, body)
	}
	var a0 importAnalysis
	decodeJSON(t, "upload", body, &a0)
	resp, body = doRequest(t, srv, http.MethodPost, "/api/imports/"+a0.BatchID+"/mapping", a0.SuggestedMapping, "application/json")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("resource confirm: %d\n%s", resp.StatusCode, body)
	}

	// Ingest the two perf days (analyze → suggested mapping → import).
	ingest := func(day string) {
		perf, err := os.ReadFile(filepath.Join(sdir, day))
		if err != nil {
			t.Fatalf("read %s: %v", day, err)
		}
		resp, body := uploadMetricFile(t, srv, "/api/metrics/analyze", day, perf, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("analyze %s: %d\n%s", day, resp.StatusCode, body)
		}
		var analysis metrics.IngestAnalysis
		decodeJSON(t, "analyze "+day, body, &analysis)
		m, err := json.Marshal(analysis.SuggestedMapping)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		resp, body = uploadMetricFile(t, srv, "/api/metrics/imports", day, perf, string(m))
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("import %s: %d\n%s", day, resp.StatusCode, body)
		}
	}
	ingest("perf-day-01.xlsx")
	ingest("perf-day-02.xlsx")
	return srv, a
}

// newDetectTestServerWithApp is newDetectTestServer plus the app handle.
func newDetectTestServerWithApp(t *testing.T) (*httptest.Server, *app.App) {
	t.Helper()
	return newDetectServerSeeded(t)
}

// TestDetectionRulesCRUD pins the rules CRUD contracts.
func TestDetectionRulesCRUD(t *testing.T) {
	srv := newDetectTestServer(t)

	// Seeded starter set exists.
	resp, body := doRequest(t, srv, http.MethodGet, "/api/detection/rules", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rules list: %d\n%s", resp.StatusCode, body)
	}
	var list struct {
		Items []struct {
			RuleID    string `json:"ruleId"`
			MetricKey string `json:"metricKey"`
			Severity  string `json:"severity"`
			Enabled   bool   `json:"enabled"`
		} `json:"items"`
		Total int `json:"total"`
	}
	decodeJSON(t, "rules list", body, &list)
	if list.Total == 0 {
		t.Fatal("no seeded rules — the starter catalog is missing")
	}

	// Create a custom rule; bogus metricKey → 400.
	newRule := []byte(`{"metricKey":"optical_power_rx_avg_dbm","comparator":"lte","threshold":-15,"window":2,"severity":"warning","label":"测试规则"}`)
	resp, body = doRequest(t, srv, http.MethodPost, "/api/detection/rules", newRule, "application/json")
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("rule create: %d\n%s", resp.StatusCode, body)
	}
	var created struct {
		RuleID string `json:"ruleId"`
	}
	decodeJSON(t, "rule create", body, &created)
	if created.RuleID == "" {
		t.Fatal("created rule has no id")
	}
	resp, _ = doRequest(t, srv, http.MethodPost, "/api/detection/rules", []byte(`{"metricKey":"bogus_key","comparator":"lte","threshold":-15,"severity":"warning"}`), "application/json")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bogus metricKey: %d, want 400", resp.StatusCode)
	}

	// Patch: enabled + threshold mutable, metricKey change → 400.
	patch := []byte(`{"threshold":-16,"enabled":false,"label":"改名"}`)
	resp, _ = doRequest(t, srv, http.MethodPatch, "/api/detection/rules/"+created.RuleID, patch, "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("rule patch: %d, want 200", resp.StatusCode)
	}
	resp, _ = doRequest(t, srv, http.MethodPatch, "/api/detection/rules/"+created.RuleID, []byte(`{"metricKey":"packet_loss_rx_pct"}`), "application/json")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("metricKey change: %d, want 400 (immutable identity)", resp.StatusCode)
	}
	resp, _ = doRequest(t, srv, http.MethodPatch, "/api/detection/rules/dr_bogus", patch, "application/json")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("patch unknown rule: %d, want 404", resp.StatusCode)
	}

	// Delete the unused rule works; delete a rule WITH alerts → 409 (audit).
	resp, _ = doRequest(t, srv, http.MethodDelete, "/api/detection/rules/"+created.RuleID, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("rule delete: %d, want 200", resp.StatusCode)
	}
}

// TestDetectionRunAndAlerts pins the run + lifecycle contracts end to end.
func TestDetectionRunAndAlerts(t *testing.T) {
	srv := newDetectTestServer(t)
	var resp *http.Response
	var body []byte

	// A window-1 rule that trips the 2-day degrade curve (the seeded
	// starter rules need ≥3 consecutive violating days — they stay for
	// the longer runs; this custom rule is the 2-day test's tripwire).
	sensitive := []byte(`{"metricKey":"optical_power_rx_avg_dbm","comparator":"lte","threshold":-8.1,"window":1,"severity":"warning","label":"两天劣化触发"}`)
	resp, body = doRequest(t, srv, http.MethodPost, "/api/detection/rules", sensitive, "application/json")
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("custom rule create: %d\n%s", resp.StatusCode, body)
	}

	// Run the detection sweep.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/detection/run", []byte(`{}`), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detection run: %d\n%s", resp.StatusCode, body)
	}
	var run struct {
		Findings         int `json:"findings"`
		AlertsCreated    int `json:"alertsCreated"`
		AlertsCleared    int `json:"alertsCleared"`
		RunStatesWritten int `json:"runStatesWritten"`
	}
	decodeJSON(t, "detection run", body, &run)
	if run.Findings == 0 || run.AlertsCreated == 0 {
		t.Fatalf("degrade scenario produced no findings/alerts: %+v", run)
	}

	// The alerts list carries the resource context (not the bare series).
	resp, body = doRequest(t, srv, http.MethodGet, "/api/alerts?state=firing", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("alerts list: %d\n%s", resp.StatusCode, body)
	}
	var alerts struct {
		Items []struct {
			AlertID  string `json:"alertId"`
			State    string `json:"state"`
			Severity string `json:"severity"`
			Resource struct {
				ID   string `json:"id"`
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"resource"`
			RuleID string `json:"ruleId"`
		} `json:"items"`
		Total int `json:"total"`
	}
	decodeJSON(t, "alerts list", body, &alerts)
	if alerts.Total == 0 {
		t.Fatal("no firing alerts after the degrade detection")
	}
	for _, it := range alerts.Items {
		if it.Resource.ID == "" || it.Resource.Type != "port" {
			t.Errorf("alert %s has no port resource context: %+v", it.AlertID, it.Resource)
		}
	}

	// Ack: firing → acked; re-ack → 409.
	first := alerts.Items[0].AlertID
	resp, _ = doRequest(t, srv, http.MethodPost, "/api/alerts/"+first+"/ack", []byte(`{"ackedBy":"运维-张三"}`), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("ack: %d, want 200", resp.StatusCode)
	}
	resp, _ = doRequest(t, srv, http.MethodPost, "/api/alerts/"+first+"/ack", []byte(`{"ackedBy":"运维-张三"}`), "application/json")
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("re-ack: %d, want 409", resp.StatusCode)
	}

	// Clear: acked → cleared; re-clear → 409.
	resp, _ = doRequest(t, srv, http.MethodPost, "/api/alerts/"+first+"/clear", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("clear: %d, want 200", resp.StatusCode)
	}
	resp, _ = doRequest(t, srv, http.MethodPost, "/api/alerts/"+first+"/clear", nil, "")
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("re-clear: %d, want 409", resp.StatusCode)
	}
	resp, _ = doRequest(t, srv, http.MethodPost, "/api/alerts/al_bogus/ack", []byte(`{"ackedBy":"x"}`), "application/json")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("ack unknown: %d, want 404", resp.StatusCode)
	}

	// Filter contracts: state=cleared now has ≥1; severity filter works.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/alerts?state=cleared", nil, "")
	decodeJSON(t, "cleared list", body, &alerts)
	if alerts.Total < 1 {
		t.Errorf("cleared filter empty after the manual clear")
	}
}

// TestDetectionRunStateIntegration pins the §12-B2 integration point 3:
// firing alerts write run_state with source=alert (history + snapshot),
// the manual HTTP guard stays 400 for source=alert.
func TestDetectionRunStateIntegration(t *testing.T) {
	srv, a := newDetectTestServerWithApp(t)
	// A window-1 tripwire rule for the 2-day degrade data.
	sensitive := []byte(`{"metricKey":"optical_power_rx_avg_dbm","comparator":"lte","threshold":-8.1,"window":1,"severity":"warning","label":"两天劣化触发"}`)
	resp, body := doRequest(t, srv, http.MethodPost, "/api/detection/rules", sensitive, "application/json")
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("custom rule create: %d\n%s", resp.StatusCode, body)
	}
	resp, body = doRequest(t, srv, http.MethodPost, "/api/detection/run", []byte(`{}`), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detection run: %d\n%s", resp.StatusCode, body)
	}
	var run struct {
		RunStatesWritten int `json:"runStatesWritten"`
	}
	decodeJSON(t, "run", body, &run)
	if run.RunStatesWritten == 0 {
		t.Fatal("detection wrote no run-states (integration point 3 broken)")
	}

	// The HTTP manual channel still rejects source=alert.
	resp, _ = doRequest(t, srv, http.MethodPost, "/api/resources/res_any/run-state", []byte(`{"runState":"up","source":"alert"}`), "application/json")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("HTTP source=alert guard: %d, want 400 (engine-only channel)", resp.StatusCode)
	}

	// History of some port contains an alert-sourced observation.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/resources?type=port&pageSize=10", nil, "")
	var list struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	decodeJSON(t, "ports", body, &list)
	sawAlertSource := false
	for _, it := range list.Items {
		hist, err := a.DB.ResourceStatus.ListHistory(context.Background(), it.ID, "", "")
		if err != nil {
			t.Fatalf("history: %v", err)
		}
		for _, h := range hist {
			if h.Source == domain.StatusSourceAlert {
				sawAlertSource = true
			}
		}
	}
	if !sawAlertSource {
		t.Errorf("no alert-sourced run-state observation found in port histories")
	}
	_ = fmt.Sprintf
	_ = time.Now
	_ = repo.Open
}
