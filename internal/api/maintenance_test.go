package api_test

import (
	"net/http"
	"testing"
)

// This file pins the maintenance-window HTTP contracts (v0.12-R15):
// the window CRUD (validation, UTC normalization, scope honesty) and the
// END-TO-END suppression story through the real detection run endpoint —
// a window covering the degrade evidence holds the fires, deleting it
// releases them, and the run receipts record the suppressed counts.

// TestMaintenanceWindowsCRUD pins the CRUD contracts of
// /api/maintenance/windows.
func TestMaintenanceWindowsCRUD(t *testing.T) {
	srv := newDetectTestServer(t)

	// Empty list first.
	resp, body := doRequest(t, srv, http.MethodGet, "/api/maintenance/windows", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("windows list: %d\n%s", resp.StatusCode, body)
	}
	var list struct {
		Items []struct {
			WindowID    string   `json:"windowId"`
			Label       string   `json:"label"`
			StartsAt    string   `json:"startsAt"`
			EndsAt      string   `json:"endsAt"`
			ResourceIDs []string `json:"resourceIds"`
		} `json:"items"`
		Total int `json:"total"`
	}
	decodeJSON(t, "windows list", body, &list)
	if list.Total != 0 || list.Items == nil {
		t.Fatalf("empty list = total %d items %v, want 0 / []", list.Total, list.Items)
	}

	// Create with a non-UTC offset: the bounds come back normalized to Z.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/maintenance/windows",
		[]byte(`{"label":"哈大齐割接","startsAt":"2026-01-02T08:00:00+08:00","endsAt":"2026-01-03T08:00:00+08:00","note":"计划内光缆割接"}`), "application/json")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("window create: %d\n%s", resp.StatusCode, body)
	}
	var created struct {
		WindowID    string   `json:"windowId"`
		StartsAt    string   `json:"startsAt"`
		EndsAt      string   `json:"endsAt"`
		ResourceIDs []string `json:"resourceIds"`
	}
	decodeJSON(t, "window create", body, &created)
	if created.WindowID == "" || created.StartsAt != "2026-01-02T00:00:00Z" || created.EndsAt != "2026-01-03T00:00:00Z" {
		t.Errorf("created window = %v, want UTC-normalized bounds", created)
	}
	// The global scope is an explicit empty array, never null.
	if created.ResourceIDs == nil || len(created.ResourceIDs) != 0 {
		t.Errorf("global scope resourceIds = %v, want []", created.ResourceIDs)
	}

	// Validation contracts: each 400 names its field.
	badCases := []struct {
		name string
		body string
	}{
		{"missing label", `{"startsAt":"2026-01-02T00:00:00Z","endsAt":"2026-01-03T00:00:00Z"}`},
		{"bad startsAt", `{"label":"x","startsAt":"2026-01-02","endsAt":"2026-01-03T00:00:00Z"}`},
		{"ends before starts", `{"label":"x","startsAt":"2026-01-03T00:00:00Z","endsAt":"2026-01-02T00:00:00Z"}`},
		{"equal bounds", `{"label":"x","startsAt":"2026-01-02T00:00:00Z","endsAt":"2026-01-02T00:00:00Z"}`},
		{"unknown resource", `{"label":"x","startsAt":"2026-01-02T00:00:00Z","endsAt":"2026-01-03T00:00:00Z","resourceIds":["res_bogus"]}`},
		{"malformed json", `{not-json`},
	}
	for _, tc := range badCases {
		resp, _ = doRequest(t, srv, http.MethodPost, "/api/maintenance/windows", []byte(tc.body), "application/json")
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", tc.name, resp.StatusCode)
		}
	}

	// A scoped window round-trips its sorted, deduplicated ids.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/maintenance/windows",
		[]byte(`{"label":"端口维护","startsAt":"2026-02-01T00:00:00Z","endsAt":"2026-02-02T00:00:00Z"}`), "application/json")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("scoped window create: %d\n%s", resp.StatusCode, body)
	}
	// The scoped ids reference a REAL imported resource: read one from the
	// resources list first (scope honesty — a typo'd id is a 400, a valid
	// id round-trips).
	resp, body = doRequest(t, srv, http.MethodGet, "/api/resources?pageSize=1", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resources list: %d\n%s", resp.StatusCode, body)
	}
	var res struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	decodeJSON(t, "resources list", body, &res)
	if len(res.Items) == 0 {
		t.Fatal("no resources to scope a window to")
	}
	scopedBody := `{"label":"端口维护","startsAt":"2026-02-01T00:00:00Z","endsAt":"2026-02-02T00:00:00Z","resourceIds":["` + res.Items[0].ID + `","` + res.Items[0].ID + `"]}`
	resp, body = doRequest(t, srv, http.MethodPost, "/api/maintenance/windows", []byte(scopedBody), "application/json")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("scoped window create: %d\n%s", resp.StatusCode, body)
	}
	decodeJSON(t, "scoped window", body, &created)
	if len(created.ResourceIDs) != 1 || created.ResourceIDs[0] != res.Items[0].ID {
		t.Errorf("scoped ids = %v, want [%s] deduplicated", created.ResourceIDs, res.Items[0].ID)
	}

	// The listing is newest-started first.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/maintenance/windows", nil, "")
	decodeJSON(t, "windows relist", body, &list)
	if list.Total != 3 {
		t.Fatalf("windows total = %d, want 3", list.Total)
	}
	if list.Items[0].StartsAt != "2026-02-01T00:00:00Z" {
		t.Errorf("newest window first = %s, want the Feb 1 window", list.Items[0].StartsAt)
	}

	// Delete: 200 + honest 404 for the unknown id.
	resp, _ = doRequest(t, srv, http.MethodDelete, "/api/maintenance/windows/"+created.WindowID, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("window delete: %d, want 200", resp.StatusCode)
	}
	resp, _ = doRequest(t, srv, http.MethodDelete, "/api/maintenance/windows/mw_bogus", nil, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("delete unknown window: %d, want 404", resp.StatusCode)
	}
}

// TestDetectionRunSuppressedByWindow pins the END-TO-END story through the
// real HTTP channel: a global window covering the degrade evidence holds
// every NEW fire (created=0, suppressed>0, no alert rows), the run
// receipt records it, and deleting the window releases the same fires on
// the next sweep.
func TestDetectionRunSuppressedByWindow(t *testing.T) {
	srv := newDetectTestServer(t)

	// The window-1 tripwire of the degrade scenario (the seeded starter
	// rules need more consecutive days than this 2-day dataset has).
	sensitive := []byte(`{"metricKey":"optical_power_rx_avg_dbm","comparator":"lte","threshold":-8.1,"window":1,"severity":"warning","label":"两天劣化触发"}`)
	resp, body := doRequest(t, srv, http.MethodPost, "/api/detection/rules", sensitive, "application/json")
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("custom rule create: %d\n%s", resp.StatusCode, body)
	}

	// The window covers the SECOND day's evidence (2026-01-02, the degrade
	// day) globally — every would-be fire of this run is held.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/maintenance/windows",
		[]byte(`{"label":"全网割接演练","startsAt":"2026-01-02T00:00:00Z","endsAt":"2026-01-03T00:00:00Z"}`), "application/json")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("window create: %d\n%s", resp.StatusCode, body)
	}
	var win struct {
		WindowID string `json:"windowId"`
	}
	decodeJSON(t, "window create", body, &win)

	// The sweep: violations found, fires held.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/detection/run", []byte(`{}`), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detection run: %d\n%s", resp.StatusCode, body)
	}
	var run struct {
		Findings      int `json:"findings"`
		AlertsCreated int `json:"alertsCreated"`
		Suppressed    int `json:"suppressed"`
		Alerts        []struct {
			AlertID string `json:"alertId"`
			Action  string `json:"action"`
			State   string `json:"state"`
		} `json:"alerts"`
	}
	decodeJSON(t, "suppressed run", body, &run)
	if run.Findings == 0 || run.AlertsCreated != 0 || run.Suppressed == 0 {
		t.Fatalf("suppressed run = findings %d / created %d / suppressed %d, want >0 / 0 / >0", run.Findings, run.AlertsCreated, run.Suppressed)
	}
	if run.Findings < run.Suppressed {
		t.Errorf("findings %d < suppressed %d (suppressed is a subset)", run.Findings, run.Suppressed)
	}
	for _, line := range run.Alerts {
		if line.Action == "suppressed" && (line.State != "" || line.AlertID == "") {
			t.Errorf("suppressed line = %+v, want empty state + the would-be id", line)
		}
	}

	// No alert rows exist — the queue is empty.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/alerts?state=firing", nil, "")
	var alerts struct {
		Total int `json:"total"`
	}
	decodeJSON(t, "alerts after suppressed run", body, &alerts)
	if alerts.Total != 0 {
		t.Fatalf("firing alerts after suppression = %d, want 0", alerts.Total)
	}

	// The run receipt records the suppressed count (auditable history).
	resp, body = doRequest(t, srv, http.MethodGet, "/api/detection/runs?pageSize=1", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("runs list: %d\n%s", resp.StatusCode, body)
	}
	var runs struct {
		Items []struct {
			Suppressed int `json:"suppressed"`
		} `json:"items"`
	}
	decodeJSON(t, "runs list", body, &runs)
	if len(runs.Items) != 1 || runs.Items[0].Suppressed != run.Suppressed {
		t.Errorf("run receipt suppressed = %+v, want %d", runs.Items, run.Suppressed)
	}

	// Delete the window → the next sweep releases the held fires.
	resp, _ = doRequest(t, srv, http.MethodDelete, "/api/maintenance/windows/"+win.WindowID, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("window delete: %d, want 200", resp.StatusCode)
	}
	resp, body = doRequest(t, srv, http.MethodPost, "/api/detection/run", []byte(`{}`), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detection run 2: %d\n%s", resp.StatusCode, body)
	}
	decodeJSON(t, "released run", body, &run)
	if run.AlertsCreated == 0 || run.Suppressed != 0 {
		t.Fatalf("released run = created %d / suppressed %d, want >0 / 0", run.AlertsCreated, run.Suppressed)
	}

	// The queue now carries the released alerts.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/alerts?state=firing", nil, "")
	decodeJSON(t, "alerts after release", body, &alerts)
	if alerts.Total == 0 {
		t.Fatal("no firing alerts after the window release")
	}
}
