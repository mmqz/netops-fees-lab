package api_test

import (
	"net/http"
	"testing"
)

// This file pins the HTTP wire contract of the alert correlation verdict
// (v0.12-R16, migration v25): the list items carry the three correlation
// fields ("" = unmarked — always present, never omitted), the role filter
// selects by the closed-enum verdict, and a bogus role is a 400. The
// cross-layer VERDICT computation itself is pinned in internal/detect
// (TestDetectCorrelationPrimarySecondary) and internal/repo
// (TestMigrationV25AddsAlertCorrelation) — the synthdata here (all
// device-layer ports, lateral) legitimately produces unmarked alerts,
// which pins the lateral guard on the real channel too.
func TestAlertsCorrelationContract(t *testing.T) {
	srv := newDetectTestServer(t)

	// Fire some alerts first (the 2-day degrade tripwire).
	sensitive := []byte(`{"metricKey":"optical_power_rx_avg_dbm","comparator":"lte","threshold":-8.1,"window":1,"severity":"warning","label":"两天劣化触发"}`)
	resp, body := doRequest(t, srv, http.MethodPost, "/api/detection/rules", sensitive, "application/json")
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("rule create: %d\n%s", resp.StatusCode, body)
	}
	resp, body = doRequest(t, srv, http.MethodPost, "/api/detection/run", []byte(`{}`), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detection run: %d\n%s", resp.StatusCode, body)
	}

	// The list items ALWAYS carry the correlation fields (never omitted,
	// never null — the strict-client contract).
	resp, body = doRequest(t, srv, http.MethodGet, "/api/alerts?state=firing&pageSize=5", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("alerts list: %d\n%s", resp.StatusCode, body)
	}
	var alerts struct {
		Items []struct {
			AlertID              string `json:"alertId"`
			CorrelationRole      string `json:"correlationRole"`
			CorrelatedAlertID    string `json:"correlatedAlertId"`
			CorrelatedResourceID string `json:"correlatedResourceId"`
		} `json:"items"`
		Total int `json:"total"`
	}
	decodeJSON(t, "alerts list", body, &alerts)
	if alerts.Total == 0 {
		t.Fatal("no firing alerts after the degrade run")
	}
	for _, it := range alerts.Items {
		// Same-layer (lateral) alerting resources never mark each other —
		// the sibling guard: every synthdata alert is unmarked.
		if it.CorrelationRole != "" || it.CorrelatedAlertID != "" || it.CorrelatedResourceID != "" {
			t.Errorf("lateral alert %s = %q/%q/%q, want unmarked (sibling guard)",
				it.AlertID, it.CorrelationRole, it.CorrelatedAlertID, it.CorrelatedResourceID)
		}
	}

	// The role filter: valid closed-enum values answer 200 (empty — no
	// marked alerts in this dataset); a bogus value is a 400.
	for _, role := range []string{"primary", "secondary"} {
		resp, body = doRequest(t, srv, http.MethodGet, "/api/alerts?state=firing&role="+role, nil, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("role=%s list: %d\n%s", role, resp.StatusCode, body)
		}
		var byRole struct {
			Total int `json:"total"`
		}
		decodeJSON(t, "role list", body, &byRole)
		if byRole.Total != 0 {
			t.Errorf("role=%s total = %d, want 0 (all synth alerts are lateral/unmarked)", role, byRole.Total)
		}
	}
	resp, _ = doRequest(t, srv, http.MethodGet, "/api/alerts?role=bogus", nil, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bogus role: %d, want 400", resp.StatusCode)
	}

	// The run envelope's created lines carry the correlation role echo
	// ("" here — lateral). Re-run detection: 0 mutations, no created lines.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/detection/run", []byte(`{}`), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("re-run: %d\n%s", resp.StatusCode, body)
	}
	var rerun struct {
		AlertsCreated int `json:"alertsCreated"`
		Alerts        []struct {
			Action          string `json:"action"`
			CorrelationRole string `json:"correlationRole"`
		} `json:"alerts"`
	}
	decodeJSON(t, "re-run", body, &rerun)
	if rerun.AlertsCreated != 0 || len(rerun.Alerts) != 0 {
		t.Fatalf("re-run = created %d lines %d, want 0/0 (idempotent)", rerun.AlertsCreated, len(rerun.Alerts))
	}
}
