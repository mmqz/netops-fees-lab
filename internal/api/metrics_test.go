package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/metrics"
)

// This file pins the HTTP contracts of the W20 metric endpoints (design
// §7.3 first three + the ingest channel's analyze/imports/batches/
// rollback). The whole user journey runs against a live httptest server:
// synth resources import → metric analyze → metric import (upsert) →
// keys/resource-metrics/anomalies reads → rollback. Nothing is mocked —
// the same SQLite + handlers a real `serve` uses.

// metricKeyItem mirrors one entry of GET /api/metrics/keys (the handler
// struct stays unexported in package api — this is the wire-shape mirror,
// api_test style).
type metricKeyItem struct {
	MetricKey string   `json:"metricKey"`
	Label     string   `json:"label"`
	Unit      string   `json:"unit"`
	ValueKind string   `json:"valueKind"`
	MinValue  *float64 `json:"minValue"`
	MaxValue  *float64 `json:"maxValue"`
	Direction string   `json:"direction"`
	CreatedAt string   `json:"createdAt"`
}

// metricKeysBody mirrors GET /api/metrics/keys.
type metricKeysBody struct {
	Items []metricKeyItem `json:"items"`
	Total int             `json:"total"`
}

// importResourcesWorkbook walks resources.xlsx (the synth generator's
// resource sheet pair) through the EXISTING resource import flow —
// upload → confirm with the suggested mapping → completed — and returns
// the import summary so callers can assert counts.
func importResourcesWorkbook(t *testing.T, srv *httptest.Server, path string) importSummary {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	resp, body := uploadFile(t, srv, filepath.Base(path), raw)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("resource upload: status = %d, want 202\nbody: %s", resp.StatusCode, body)
	}
	var a importAnalysis
	decodeJSON(t, "resource upload", body, &a)
	resp, body = doRequest(t, srv, http.MethodPost, "/api/imports/"+a.BatchID+"/mapping", a.SuggestedMapping, "application/json")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("resource confirm: status = %d, want 202\nbody: %s", resp.StatusCode, body)
	}
	var sum importSummary
	decodeJSON(t, "resource confirm", body, &sum)
	return sum
}

// uploadMetricFile posts one multipart upload to path (endpoint) with the
// given file field, plus an optional mapping form field.
func uploadMetricFile(t *testing.T, srv *httptest.Server, endpoint, filename string, content []byte, mapping string) (*http.Response, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if mapping != "" {
		if err := mw.WriteField("mapping", mapping); err != nil {
			t.Fatalf("WriteField mapping: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+endpoint, &buf)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read response: %v", err)
	}
	return resp, body.Bytes()
}

// TestMetricEndpointsContract is the W20 HTTP journey gate.
func TestMetricEndpointsContract(t *testing.T) {
	srv := newAPITestServer(t)

	// ① keys: the closed catalog, registry order, complete fields.
	resp, body := doRequest(t, srv, http.MethodGet, "/api/metrics/keys", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("keys: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	var keysBody metricKeysBody
	decodeJSON(t, "keys", body, &keysBody)
	if keysBody.Total != len(metrics.MetricKeys) || len(keysBody.Items) != len(metrics.MetricKeys) {
		t.Fatalf("keys: total = %d, want %d (registry size)", keysBody.Total, len(metrics.MetricKeys))
	}
	for i, item := range keysBody.Items {
		if item.MetricKey != metrics.MetricKeys[i].Key {
			t.Errorf("keys[%d] = %q, want %q (registry order)", i, item.MetricKey, metrics.MetricKeys[i].Key)
		}
	}
	if keysBody.Items[0].MetricKey != "optical_power_rx_avg_dbm" {
		t.Errorf("keys[0] = %q, want optical_power_rx_avg_dbm", keysBody.Items[0].MetricKey)
	}
	if keysBody.Items[0].Unit != "dBm" || keysBody.Items[0].Direction == "" || keysBody.Items[0].ValueKind == "" {
		t.Errorf("keys[0] fields incomplete: %+v", keysBody.Items[0])
	}

	// ② synth resources + perf days (small, deterministic).
	dir := testsupport.TempDir(t)
	if _, err := metrics.WriteSynthData(metrics.SynthOptions{
		Out: dir, Devices: 2, Ports: 1, Days: 2, Scenario: metrics.ScenarioNormal, Seed: 42, Metrics: 3,
	}); err != nil {
		t.Fatalf("synthdata: %v", err)
	}
	resSum := importResourcesWorkbook(t, srv, filepath.Join(dir, "resources.xlsx"))
	if resSum.Summary.RowsImported != 4 { // 2 devices + 2 ports
		t.Fatalf("resource import: rowsImported = %d, want 4\nsummary: %+v", resSum.Summary.RowsImported, resSum)
	}

	// ③ analyze: stateless suggestion, canonical headers resolved.
	perf, err := os.ReadFile(filepath.Join(dir, "perf-day-01.xlsx"))
	if err != nil {
		t.Fatalf("read perf: %v", err)
	}
	resp, body = uploadMetricFile(t, srv, "/api/metrics/analyze", "perf-day-01.xlsx", perf, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("analyze: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	var analysis metrics.IngestAnalysis
	decodeJSON(t, "analyze", body, &analysis)
	if analysis.RowCount != 2 {
		t.Errorf("analyze: rowCount = %d, want 2", analysis.RowCount)
	}
	if analysis.SuggestedMapping.TSField == "" || len(analysis.SuggestedMapping.Metrics) == 0 {
		t.Errorf("analyze: suggested mapping incomplete: %+v", analysis.SuggestedMapping)
	}
	mappingJSON, err := json.Marshal(analysis.SuggestedMapping)
	if err != nil {
		t.Fatalf("marshal mapping: %v", err)
	}

	// ④ import: 202 envelope + row accounting; error taxonomy on the way.
	resp, body = uploadMetricFile(t, srv, "/api/metrics/imports", "perf-day-01.xlsx", perf, string(mappingJSON))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("metric import: status = %d, want 202\nbody: %s", resp.StatusCode, body)
	}
	var imp struct {
		BatchID       string `json:"batchId"`
		Status        string `json:"status"`
		RowsTotal     int    `json:"rowsTotal"`
		RowsImported  int    `json:"rowsImported"`
		RowsFailed    int    `json:"rowsFailed"`
		PointsWritten int    `json:"pointsWritten"`
	}
	decodeJSON(t, "metric import", body, &imp)
	if imp.BatchID == "" || imp.Status != "completed" || imp.RowsTotal != 2 || imp.RowsImported != 2 || imp.RowsFailed != 0 {
		t.Fatalf("metric import envelope: %+v", imp)
	}

	// missing mapping field → 400
	resp, _ = uploadMetricFile(t, srv, "/api/metrics/imports", "perf-day-01.xlsx", perf, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("import without mapping: status = %d, want 400", resp.StatusCode)
	}
	// unknown metric key in mapping → 400
	badMapping := `{"tsField":"日期","resourceType":"port","resolveBy":["device_name","port_name"],"metrics":{"bogus_key":"接收光功率均值"}}`
	resp, body = uploadMetricFile(t, srv, "/api/metrics/imports", "perf-day-01.xlsx", perf, badMapping)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("import with bogus metric key: status = %d, want 400\nbody: %s", resp.StatusCode, body)
	}

	// ⑤ second day import (upsert grows the series, not duplicates).
	perf2, err := os.ReadFile(filepath.Join(dir, "perf-day-02.xlsx"))
	if err != nil {
		t.Fatalf("read perf2: %v", err)
	}
	resp, body = uploadMetricFile(t, srv, "/api/metrics/imports", "perf-day-02.xlsx", perf2, string(mappingJSON))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("metric import day2: status = %d, want 202\nbody: %s", resp.StatusCode, body)
	}
	var imp2 struct {
		BatchID string `json:"batchId"`
	}
	decodeJSON(t, "metric import day2", body, &imp2)

	// ⑥ resource metrics: series with 2 points, daily_agg aggregates.
	// The resource id is discovered via the resource list (first port).
	resp, body = doRequest(t, srv, http.MethodGet, "/api/resources?type=port&pageSize=10", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resource list: %d\n%s", resp.StatusCode, body)
	}
	var list struct {
		Items []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"items"`
	}
	decodeJSON(t, "resource list", body, &list)
	if len(list.Items) == 0 {
		t.Fatal("no port resources found")
	}
	portID := list.Items[0].ID
	resp, body = doRequest(t, srv, http.MethodGet, "/api/resources/"+portID+"/metrics", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resource metrics: %d\n%s", resp.StatusCode, body)
	}
	var seriesBody struct {
		ResourceID string `json:"resourceId"`
		Series     []struct {
			SeriesID  string `json:"seriesId"`
			MetricKey string `json:"metricKey"`
			Unit      string `json:"unit"`
			Points    []struct {
				Ts    string  `json:"ts"`
				Value float64 `json:"value"`
			} `json:"points"`
		} `json:"series"`
	}
	decodeJSON(t, "resource metrics", body, &seriesBody)
	if len(seriesBody.Series) != 3 { // metrics=3 in the synth options
		t.Fatalf("resource metrics: %d series, want 3", len(seriesBody.Series))
	}
	for _, s := range seriesBody.Series {
		if len(s.Points) != 2 {
			t.Errorf("series %s: %d points, want 2 (two days, upsert)", s.MetricKey, len(s.Points))
		}
	}
	// keys filter + unknown key → 400; bogus resource → 404; bad agg → 400.
	resp, _ = doRequest(t, srv, http.MethodGet, "/api/resources/"+portID+"/metrics?keys=bogus_key", nil, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("metrics keys=bogus: %d, want 400", resp.StatusCode)
	}
	resp, _ = doRequest(t, srv, http.MethodGet, "/api/resources/res_bogus/metrics", nil, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("metrics bogus resource: %d, want 404", resp.StatusCode)
	}
	resp, _ = doRequest(t, srv, http.MethodGet, "/api/resources/"+portID+"/metrics?agg=weekly", nil, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("metrics agg=weekly: %d, want 400", resp.StatusCode)
	}
	// daily_agg: one bucket per day per series.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/resources/"+portID+"/metrics?agg=daily_avg", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("daily_avg: %d\n%s", resp.StatusCode, body)
	}
	decodeJSON(t, "daily_avg", body, &seriesBody)
	if len(seriesBody.Series) == 0 || len(seriesBody.Series[0].Points) != 2 {
		t.Fatalf("daily_avg: buckets = %v, want 2 days", seriesBody.Series)
	}

	// ⑦ anomalies: contract + direction-aware degrading flag. In the
	// normal scenario with threshold tiny enough, series surface with
	// near-zero delta; degrading=false (stable values).
	resp, body = doRequest(t, srv, http.MethodGet, "/api/metrics/anomalies?metricKey=optical_power_rx_avg_dbm&threshold=0.0001", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("anomalies: %d\n%s", resp.StatusCode, body)
	}
	var anom struct {
		Items []struct {
			ResourceID string  `json:"resourceId"`
			MetricKey  string  `json:"metricKey"`
			N          int     `json:"n"`
			Delta      float64 `json:"delta"`
			Degrading  bool    `json:"degrading"`
		} `json:"items"`
		Total     int    `json:"total"`
		MetricKey string `json:"metricKey"`
	}
	decodeJSON(t, "anomalies", body, &anom)
	if anom.MetricKey != "optical_power_rx_avg_dbm" {
		t.Errorf("anomalies metricKey echo = %q", anom.MetricKey)
	}
	for _, it := range anom.Items {
		if it.N != 2 {
			t.Errorf("anomaly %s: n = %d, want 2", it.ResourceID, it.N)
		}
	}
	// bogus metricKey / negative threshold → 400.
	resp, _ = doRequest(t, srv, http.MethodGet, "/api/metrics/anomalies?metricKey=bogus", nil, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("anomalies bogus key: %d, want 400", resp.StatusCode)
	}
	resp, _ = doRequest(t, srv, http.MethodGet, "/api/metrics/anomalies?threshold=-1", nil, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("anomalies negative threshold: %d, want 400", resp.StatusCode)
	}

	// ⑧ batches list + rollback: the day-2 batch rolls back, series keep
	// day-1 points only (upsert semantics does NOT resurrect them).
	resp, body = doRequest(t, srv, http.MethodGet, "/api/metrics/batches", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("batches: %d\n%s", resp.StatusCode, body)
	}
	var batches struct {
		Items []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"items"`
		Total int `json:"total"`
	}
	decodeJSON(t, "batches", body, &batches)
	if batches.Total != 2 {
		t.Fatalf("batches total = %d, want 2", batches.Total)
	}
	resp, body = doRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/metrics/batches/%s/rollback", imp2.BatchID), nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rollback: %d\n%s", resp.StatusCode, body)
	}
	_, body = doRequest(t, srv, http.MethodGet, "/api/resources/"+portID+"/metrics", nil, "")
	decodeJSON(t, "after rollback", body, &seriesBody)
	for _, s := range seriesBody.Series {
		if len(s.Points) != 1 {
			t.Errorf("after rollback: series %s has %d points, want 1 (day-1 only)", s.MetricKey, len(s.Points))
		}
	}
	// unknown batch → 404; rolled-back batch re-rollback → 409 (state machine).
	resp, _ = doRequest(t, srv, http.MethodPost, "/api/metrics/batches/bat_bogus/rollback", nil, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("rollback bogus batch: %d, want 404", resp.StatusCode)
	}
	resp, _ = doRequest(t, srv, http.MethodPost, fmt.Sprintf("/api/metrics/batches/%s/rollback", imp2.BatchID), nil, "")
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("re-rollback: %d, want 409", resp.StatusCode)
	}
}

// TestAPIMetricForecast pins the #123 HTTP contract: the D4 trend
// forecast over the series's daily averages — shape, the honest
// refusals (422 <14 days / no series), the closed-catalog 400, the
// disclaimer that never leaves the payload.
func TestAPIMetricForecast(t *testing.T) {
	srv := newAPITestServer(t)
	dir := testsupport.TempDir(t)
	if _, err := metrics.WriteSynthData(metrics.SynthOptions{
		Out: dir, Devices: 1, Ports: 1, Days: 20, Scenario: metrics.ScenarioNormal, Seed: 42, Metrics: 3,
	}); err != nil {
		t.Fatalf("synthdata: %v", err)
	}
	importResourcesWorkbook(t, srv, filepath.Join(dir, "resources.xlsx"))

	// import all 20 perf days through the real analyze+import cycle.
	for d := 1; d <= 20; d++ {
		name := fmt.Sprintf("perf-day-%02d.xlsx", d)
		perf, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		resp, body := uploadMetricFile(t, srv, "/api/metrics/analyze", name, perf, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("analyze %s: %d\n%s", name, resp.StatusCode, body)
		}
		var analysis metrics.IngestAnalysis
		decodeJSON(t, "analyze "+name, body, &analysis)
		mappingJSON, err := json.Marshal(analysis.SuggestedMapping)
		if err != nil {
			t.Fatal(err)
		}
		resp, body = uploadMetricFile(t, srv, "/api/metrics/imports", name, perf, string(mappingJSON))
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("import %s: %d\n%s", name, resp.StatusCode, body)
		}
	}

	// discover the port id and the device id (the device carries no
	// series — metrics hang on ports — so it answers the 422 class).
	var portID, deviceID string
	resp, body := doRequest(t, srv, http.MethodGet, "/api/resources?pageSize=10", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resource list: %d\n%s", resp.StatusCode, body)
	}
	var list struct {
		Items []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"items"`
	}
	decodeJSON(t, "resource list", body, &list)
	for _, it := range list.Items {
		switch it.Type {
		case "port":
			portID = it.ID
		case "device":
			deviceID = it.ID
		}
	}
	if portID == "" || deviceID == "" {
		t.Fatalf("need one port and one device, got %q/%q", portID, deviceID)
	}

	// the forecast: 200, 20 daily points, 31 projections (last observed
	// day + the default 30 ahead), the D4 disclaimer always aboard.
	resp, body = doRequest(t, srv, http.MethodGet,
		"/api/metrics/forecast?resourceId="+portID+"&metricKey=optical_power_rx_avg_dbm", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("forecast: %d\n%s", resp.StatusCode, body)
	}
	var fc struct {
		ResourceID      string  `json:"resourceId"`
		MetricKey       string  `json:"metricKey"`
		Points          int     `json:"points"`
		Slope           float64 `json:"slopePerDay"`
		DaysToThreshold *int    `json:"daysToThreshold"`
		Projections     []struct {
			Day    string  `json:"day"`
			Value  float64 `json:"value"`
			CiLow  float64 `json:"ciLow"`
			CiHigh float64 `json:"ciHigh"`
		} `json:"projections"`
		Note string `json:"note"`
	}
	decodeJSON(t, "forecast", body, &fc)
	if fc.ResourceID != portID || fc.MetricKey != "optical_power_rx_avg_dbm" || fc.Points != 20 {
		t.Fatalf("forecast identity = %s/%s points %d, want the port 20-point series", fc.ResourceID, fc.MetricKey, fc.Points)
	}
	if len(fc.Projections) != 31 {
		t.Fatalf("projections = %d, want 31 (windowDays default 30 + the last observed day)", len(fc.Projections))
	}
	if fc.Note != metrics.ForecastNote {
		t.Fatalf("note = %q, want the D4 disclaimer", fc.Note)
	}
	if fc.Projections[0].CiLow > fc.Projections[0].Value || fc.Projections[0].CiHigh < fc.Projections[0].Value {
		t.Fatalf("projection[0] band = [%.3f, %.3f] around %.3f — the band must straddle the fit", fc.Projections[0].CiLow, fc.Projections[0].CiHigh, fc.Projections[0].Value)
	}

	// windowDays narrows the projection horizon.
	resp, body = doRequest(t, srv, http.MethodGet,
		"/api/metrics/forecast?resourceId="+portID+"&metricKey=optical_power_rx_avg_dbm&windowDays=7", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("forecast windowDays=7: %d\n%s", resp.StatusCode, body)
	}
	decodeJSON(t, "forecast w7", body, &fc)
	if len(fc.Projections) != 8 {
		t.Fatalf("windowDays=7 projections = %d, want 8", len(fc.Projections))
	}

	// the device carries no series → the honest 422, not an empty 200.
	resp, body = doRequest(t, srv, http.MethodGet,
		"/api/metrics/forecast?resourceId="+deviceID+"&metricKey=optical_power_rx_avg_dbm", nil, "")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("device forecast: %d, want 422\n%s", resp.StatusCode, body)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeJSON(t, "device forecast", body, &env)
	if env.Error.Code != "forecast_not_enough_data" {
		t.Fatalf("device forecast code = %q, want forecast_not_enough_data", env.Error.Code)
	}

	// the closed-catalog and parameter 400s.
	for _, bad := range []string{
		"/api/metrics/forecast?metricKey=optical_power_rx_avg_dbm",
		"/api/metrics/forecast?resourceId=" + portID,
		"/api/metrics/forecast?resourceId=" + portID + "&metricKey=bogus_key",
		"/api/metrics/forecast?resourceId=" + portID + "&metricKey=optical_power_rx_avg_dbm&windowDays=0",
		"/api/metrics/forecast?resourceId=" + portID + "&metricKey=optical_power_rx_avg_dbm&windowDays=91",
	} {
		resp, body = doRequest(t, srv, http.MethodGet, bad, nil, "")
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("GET %s: %d, want 400\n%s", bad, resp.StatusCode, body)
		}
	}

	// unknown resource → 404 (mapServiceError), never the data 422.
	resp, body = doRequest(t, srv, http.MethodGet,
		"/api/metrics/forecast?resourceId=res_nosuch&metricKey=optical_power_rx_avg_dbm", nil, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown resource forecast: %d, want 404\n%s", resp.StatusCode, body)
	}
}
