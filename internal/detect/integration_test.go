package detect

// integration_test.go — the §7.4 daily-inspection loop's detection half,
// end-to-end on the W20 synthdata degrade scenario: the REAL import
// channel (resources.xlsx), the REAL metric ingest channel
// (perf-day-*.xlsx), then Detect → exactly the degrading ports' series
// get firing alerts (the deterministic fault subset of the generator) →
// severity mapping to run_state (warning→degraded) → the recovery import
// (values back in bounds, value-semantics upsert) → re-Detect → cleared +
// run_state up. The AI loop of §10.6 starts from exactly this state.

import (
	"context"
	"fmt"
	"github.com/mmqz/netops-lab/internal/testsupport"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/importer"
	"github.com/mmqz/netops-lab/internal/metrics"
	"github.com/mmqz/netops-lab/internal/repo"
	"github.com/xuri/excelize/v2"
)

// synthIntegrationOptions is the degrade-scenario dataset: 10 devices × 2
// ports × 60 days. The degrade curve (base -8.0 dBm, -0.15 dBm/day on the
// deterministic fault subset) crosses the dr_opt_rx_low threshold (-14.0)
// at day 41, so the last ~20 days violate — comfortably past the window-3
// debounce; the floor rule (-25) is never reached (the decline bottoms out
// at ~-16.9 dBm).
const (
	synthDevices = 10
	synthPorts   = 2
	synthDays    = 60
	synthSeed    = int64(42)
)

// synthIntegrationMapping maps the canonical perf headers: RX power (the
// degrading metric) plus loss and temperature (in-bounds controls — their
// rules must NOT fire).
func synthIntegrationMapping() metrics.IngestMapping {
	return metrics.IngestMapping{
		TSField:      "日期",
		ResourceType: "port",
		ResolveBy:    []string{"device_name", "port_name"},
		Metrics: map[string]string{
			"optical_power_rx_avg_dbm": "接收光功率均值",
			"packet_loss_rx_pct":       "接收丢包率",
			"module_temp_avg_c":        "光模块温度均值",
		},
	}
}

// TestDegradeScenarioDailyInspectionLoop runs the full loop.
func TestDegradeScenarioDailyInspectionLoop(t *testing.T) {
	dir := testsupport.TempDir(t)
	out := filepath.Join(dir, "synth")
	sum, err := metrics.WriteSynthData(metrics.SynthOptions{
		Out: out, Devices: synthDevices, Ports: synthPorts, Days: synthDays,
		Scenario: "degrade", Seed: synthSeed,
	})
	if err != nil {
		t.Fatalf("WriteSynthData: %v", err)
	}
	if len(sum.Files) != synthDays+2 {
		t.Fatalf("synth files = %d, want %d", len(sum.Files), synthDays+2)
	}

	db := newDetectDB(t)
	ctx := context.Background()

	// ① The REAL resource import channel consumes resources.xlsx.
	imp := importer.New(db)
	a, err := imp.Analyze(ctx, filepath.Join(out, "resources.xlsx"))
	if err != nil {
		t.Fatalf("analyze resources workbook: %v", err)
	}
	if _, err := imp.Execute(ctx, a.BatchID, a.SuggestedMapping); err != nil {
		t.Fatalf("execute resources workbook: %v", err)
	}
	wantPorts := synthDevices * synthPorts
	ports, err := db.Resources.ListByType(ctx, domain.ResourceTypePort)
	if err != nil || len(ports) != wantPorts {
		t.Fatalf("imported ports = %d (err %v), want %d", len(ports), err, wantPorts)
	}

	// ② The REAL metric ingest channel consumes the 60 daily perf
	// workbooks (value-semantics upsert — the daily import routine).
	svc := metrics.New(db)
	m := synthIntegrationMapping()
	for d := 1; d <= synthDays; d++ {
		path := filepath.Join(out, fmt.Sprintf("perf-day-%02d.xlsx", d))
		ingest, err := svc.Ingest(ctx, path, m, filepath.Base(path))
		if err != nil {
			t.Fatalf("ingest %s: %v", path, err)
		}
		if ingest.RowsFailed > 0 {
			t.Fatalf("ingest %s failed %d rows", path, ingest.RowsFailed)
		}
	}

	// The EXPECTED fault set, computed from the data itself (independent
	// of the engine under test): every RX series whose LAST 3 points are
	// all ≤ -14.0 (the seeded rule's judgement, stated in plain Go).
	series, err := db.Metrics.SeriesOfKey(ctx, "optical_power_rx_avg_dbm")
	if err != nil {
		t.Fatalf("SeriesOfKey: %v", err)
	}
	if len(series) != wantPorts {
		t.Fatalf("rx series = %d, want %d", len(series), wantPorts)
	}
	expected := make(map[string]bool) // series_id → faulting
	for _, se := range series {
		points, err := db.Metrics.PointsOfSeries(ctx, se.SeriesID, "", "")
		if err != nil {
			t.Fatalf("PointsOfSeries: %v", err)
		}
		if len(points) < 3 {
			continue
		}
		last3 := points[len(points)-3:]
		faulting := true
		for _, p := range last3 {
			if p.Value > -14.0 {
				faulting = false
				break
			}
		}
		if faulting {
			expected[se.SeriesID] = true
		}
	}
	if len(expected) == 0 {
		t.Fatalf("the degrade scenario produced no faulting series — the fixture is broken (seed %d)", synthSeed)
	}

	// ③ Detect: the lazy seed installs the default rules, the sweep
	// fires exactly the expected subset (deterministic subset!).
	engine := New()
	res, err := engine.Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	t.Logf("FAULTSUBSET: %d faulting of %d series; created=%d findings=%d runStates=%d evaluatedAt=%s", len(expected), wantPorts, res.AlertsCreated, res.Findings, res.RunStatesWritten, res.EvaluatedAt)
	if res.AlertsCreated != len(expected) {
		t.Fatalf("alerts created = %d, want exactly the %d faulting series", res.AlertsCreated, len(expected))
	}
	// Controls: the loss and temperature rules fired nothing (in-bounds).
	for _, s := range res.Alerts {
		if s.RuleID != "dr_opt_rx_low" {
			t.Errorf("unexpected alert from rule %s — the in-bounds controls fired", s.RuleID)
		}
		if s.Severity != domain.SeverityWarning.String() {
			t.Errorf("alert severity = %s, want warning (the floor rule must stay silent)", s.Severity)
		}
	}

	rows, _, err := db.Alerts.ListAlerts(ctx, repo.AlertFilter{State: "firing"})
	if err != nil {
		t.Fatalf("ListAlerts(firing): %v", err)
	}
	got := make(map[string]bool, len(rows))
	for _, row := range rows {
		got[row.SeriesID] = true
		if row.MetricKey != "optical_power_rx_avg_dbm" {
			t.Errorf("alert on metric %s, want optical_power_rx_avg_dbm", row.MetricKey)
		}
		if row.ResourceType != "port" {
			t.Errorf("alert resource type = %s, want port (A3: series hang on port resources)", row.ResourceType)
		}
	}
	if len(got) != len(expected) {
		t.Fatalf("firing alerts = %d, want %d", len(got), len(expected))
	}
	for sid := range expected {
		if !got[sid] {
			t.Errorf("faulting series %s got NO alert", sid)
		}
	}
	for sid := range got {
		if !expected[sid] {
			t.Errorf("healthy series %s got an alert (false positive)", sid)
		}
	}

	// ④ Severity → run-state mapping: exactly the faulting ports'
	// resources are degraded (warning), nothing else moved.
	counts, nulls, err := db.ResourceStatus.CountByRunState(ctx)
	if err != nil {
		t.Fatalf("CountByRunState: %v", err)
	}
	if counts[domain.RunStateDegraded] != len(expected) {
		t.Errorf("degraded resources = %d, want %d (warning → degraded)", counts[domain.RunStateDegraded], len(expected))
	}
	if counts[domain.RunStateUp] != 0 || counts[domain.RunStateDown] != 0 {
		t.Errorf("run-state leakage: up=%d down=%d, want 0/0", counts[domain.RunStateUp], counts[domain.RunStateDown])
	}
	if nulls != synthDevices+wantPorts-len(expected) {
		t.Errorf("unobserved resources = %d, want %d (devices + healthy ports)", nulls, synthDevices+wantPorts-len(expected))
	}

	// ⑤ The recovery import: the faulting ports' last three days are
	// re-imported back in bounds (the value-semantics upsert of the
	// metric channel — same (series, ts), healthy values).
	faultResourceIDs := make([]string, 0, len(expected))
	for _, se := range series {
		if expected[se.SeriesID] {
			faultResourceIDs = append(faultResourceIDs, se.ResourceID)
		}
	}
	sort.Strings(faultResourceIDs)
	recoveryRows := make([][]string, 0, len(faultResourceIDs)*3)
	lastDay := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, synthDays-1)
	for _, id := range faultResourceIDs {
		res, err := db.Resources.GetByID(ctx, id)
		if err != nil {
			t.Fatalf("GetByID(%s): %v", id, err)
		}
		device, port, ok := splitPortKey(res.NaturalKey)
		if !ok {
			t.Fatalf("port natural key %q does not split", res.NaturalKey)
		}
		for back := 2; back >= 0; back-- {
			day := lastDay.AddDate(0, 0, -back).Format("2006-01-02")
			recoveryRows = append(recoveryRows, []string{
				day, device, port, "-8.50", "0.02", "42.10",
			})
		}
	}
	recoveryPath := writePerfWorkbook(t, dir, "recovery.xlsx", recoveryRows)
	if _, err := svc.Ingest(ctx, recoveryPath, m, "recovery.xlsx"); err != nil {
		t.Fatalf("ingest recovery workbook: %v", err)
	}

	// ⑥ Re-Detect: the faulting alerts clear (engine recovery, data-ts),
	// the run-states go up — the daily-inspection loop closes.
	res2, err := engine.Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("re-Detect: %v", err)
	}
	if res2.Findings != 0 || res2.AlertsCleared != len(expected) {
		t.Errorf("re-Detect = %d findings / %d cleared, want 0 / %d", res2.Findings, res2.AlertsCleared, len(expected))
	}
	firing, _, err := db.Alerts.ListAlerts(ctx, repo.AlertFilter{State: "firing"})
	if err != nil || len(firing) != 0 {
		t.Errorf("firing alerts after recovery = %d (err %v), want 0", len(firing), err)
	}
	cleared, _, err := db.Alerts.ListAlerts(ctx, repo.AlertFilter{State: "cleared"})
	if err != nil || len(cleared) != len(expected) {
		t.Errorf("cleared alerts after recovery = %d (err %v), want %d", len(cleared), err, len(expected))
	}
	counts, nulls, err = db.ResourceStatus.CountByRunState(ctx)
	if err != nil {
		t.Fatalf("CountByRunState after recovery: %v", err)
	}
	if counts[domain.RunStateUp] != len(expected) {
		t.Errorf("up resources after recovery = %d, want %d", counts[domain.RunStateUp], len(expected))
	}
	if counts[domain.RunStateDegraded] != 0 || counts[domain.RunStateDown] != 0 {
		t.Errorf("run-state leakage after recovery: degraded=%d down=%d", counts[domain.RunStateDegraded], counts[domain.RunStateDown])
	}
	if nulls != synthDevices+wantPorts-len(expected) {
		t.Errorf("unobserved resources after recovery = %d, want %d (the healthy set never moved)", nulls, synthDevices+wantPorts-len(expected))
	}
}

// splitPortKey splits "device|port" natural keys.
func splitPortKey(key string) (device, port string, ok bool) {
	i := strings.Index(key, "|")
	if i <= 0 || i == len(key)-1 {
		return "", "", false
	}
	return key[:i], key[i+1:], true
}

// recoveryHeaders are the recovery workbook's columns: the resolution +
// time columns plus the three mapped metric headers.
var recoveryHeaders = []string{"日期", "设备名称", "端口名", "接收光功率均值", "接收丢包率", "光模块温度均值"}

// writePerfWorkbook writes one 端口性能-flavored workbook with the given
// rows (the metric ingest channel picks it by its synonym header).
func writePerfWorkbook(t *testing.T, dir, name string, rows [][]string) string {
	t.Helper()
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	const sheet = "端口性能"
	if err := f.SetSheetName("Sheet1", sheet); err != nil {
		t.Fatalf("rename sheet: %v", err)
	}
	all := append([][]string{recoveryHeaders}, rows...)
	for r, row := range all {
		for c, v := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, r+1)
			if err != nil {
				t.Fatalf("cell name: %v", err)
			}
			if err := f.SetCellStr(sheet, cell, v); err != nil {
				t.Fatalf("write %s: %v", cell, err)
			}
		}
	}
	path := filepath.Join(dir, name)
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("save %s: %v", path, err)
	}
	return path
}
