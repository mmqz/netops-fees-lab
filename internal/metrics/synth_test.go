package metrics

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/importer"
	"github.com/mmqz/netops-lab/internal/repo"
	"github.com/mmqz/netops-lab/internal/workbook"
)

// readAllFiles returns dir's regular files (name → bytes), sorted by name
// — the byte-comparison substrate of the determinism gate.
func readAllFiles(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	out := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		out[e.Name()] = data
	}
	return out
}

// TestSynthDeterminism is the acceptance gate of the generator: the same
// seed → byte-identical output (every file), and a different seed →
// different bytes (the seed actually matters).
func TestSynthDeterminism(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	opts := SynthOptions{Out: dirA, Devices: 5, Ports: 2, Days: 4, Scenario: ScenarioDegrade, Seed: 42, Metrics: 6}
	if _, err := WriteSynthData(opts); err != nil {
		t.Fatalf("generate A: %v", err)
	}
	opts.Out = dirB
	if _, err := WriteSynthData(opts); err != nil {
		t.Fatalf("generate B: %v", err)
	}
	filesA := readAllFiles(t, dirA)
	filesB := readAllFiles(t, dirB)
	if len(filesA) == 0 {
		t.Fatalf("no files generated")
	}
	want := map[string]bool{
		"resources.xlsx": true, "README.md": true,
		"perf-day-01.xlsx": true, "perf-day-02.xlsx": true,
		"perf-day-03.xlsx": true, "perf-day-04.xlsx": true,
	}
	if len(filesA) != len(want) {
		t.Fatalf("generated files = %v, want exactly %v", filesA, want)
	}
	for name := range want {
		a, okA := filesA[name]
		b, okB := filesB[name]
		if !okA || !okB {
			t.Fatalf("file %s missing (A:%v B:%v)", name, okA, okB)
		}
		if !bytes.Equal(a, b) {
			t.Errorf("file %s differs between the two same-seed runs (determinism gate)", name)
		}
	}

	// A different seed must produce different bytes.
	dirC := t.TempDir()
	opts.Out = dirC
	opts.Seed = 43
	if _, err := WriteSynthData(opts); err != nil {
		t.Fatalf("generate C: %v", err)
	}
	filesC := readAllFiles(t, dirC)
	if bytes.Equal(filesA["perf-day-01.xlsx"], filesC["perf-day-01.xlsx"]) {
		t.Errorf("seed 42 and 43 produced identical perf-day-01.xlsx — the seed does nothing")
	}

	// The README states the synthetic origin and the exact regeneration
	// command (§12-A5 / the task's origin-statement requirement).
	readme := string(filesA["README.md"])
	for _, needle := range []string{"合成数据", "netopslab synthdata", "--seed 42", "scenario: degrade"} {
		if !bytes.Contains([]byte(readme), []byte(needle)) {
			t.Errorf("README.md misses %q", needle)
		}
	}
}

// TestSynthFilesFeedsBothChannels is the integration gate: the generated
// resources.xlsx imports through the EXISTING resource channel (suggested
// mapping, zero manual work), the perf-day files ingest through the metric
// channel with the SUGGESTED mapping, and the anomaly scan then surfaces
// the degrade-scenario ports — the whole W20 story in one test.
func TestSynthFilesFeedsBothChannels(t *testing.T) {
	dir := t.TempDir()
	db, err := repo.Open(filepath.Join(t.TempDir(), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := WriteSynthData(SynthOptions{
		Out: dir, Devices: 10, Ports: 2, Days: 8, Scenario: ScenarioDegrade, Seed: 42, Metrics: 8,
	}); err != nil {
		t.Fatalf("synthdata: %v", err)
	}
	ctx := context.Background()

	// ① resources.xlsx through the existing import channel.
	imp := importer.New(db)
	a, err := imp.Analyze(ctx, filepath.Join(dir, "resources.xlsx"))
	if err != nil {
		t.Fatalf("analyze resources.xlsx: %v", err)
	}
	sum, err := imp.Execute(ctx, a.BatchID, a.SuggestedMapping)
	if err != nil {
		t.Fatalf("execute resources.xlsx: %v", err)
	}
	if sum.RowsImported != 10+20 { // devices + ports
		t.Fatalf("resources import = %+v, want 30 rows imported (10 devices + 20 ports)", sum)
	}

	// ② perf-day-01 through the metric channel with the SUGGESTED mapping.
	s := New(db)
	s.Now = fixedClock
	an, err := AnalyzeOffline(filepath.Join(dir, "perf-day-01.xlsx"), "perf-day-01.xlsx")
	if err != nil {
		t.Fatalf("analyze perf-day-01: %v", err)
	}
	if len(an.SuggestedMapping.Metrics) != 8 {
		t.Fatalf("suggested metrics = %d, want 8", len(an.SuggestedMapping.Metrics))
	}
	if an.SuggestedMapping.TSField != "日期" || len(an.SuggestedMapping.ResolveBy) != 2 {
		t.Fatalf("suggested mapping = %+v", an.SuggestedMapping)
	}
	if is, err := s.Ingest(ctx, filepath.Join(dir, "perf-day-01.xlsx"), an.SuggestedMapping, "perf-day-01.xlsx"); err != nil {
		t.Fatalf("ingest perf-day-01: %v", err)
	} else if is.RowsImported != 20 || is.PointsWritten != 20*8 {
		t.Fatalf("ingest summary = %+v, want 20 rows / 160 points", is)
	}

	// ③ all 8 days, then the anomaly scan: the degrade subset (deterministic
	// ~10% of 20 ports ≈ 2 ports) declines 0.15 dBm/day × 8 days = 1.05 —
	// beyond the 1.0 default threshold.
	for d := 2; d <= 8; d++ {
		name := fmt.Sprintf("perf-day-%02d.xlsx", d)
		if _, err := s.Ingest(ctx, filepath.Join(dir, name), an.SuggestedMapping, name); err != nil {
			t.Fatalf("ingest %s: %v", name, err)
		}
	}
	items, err := s.Anomalies(ctx, "optical_power_rx_avg_dbm", "", "", 0, "")
	if err != nil {
		t.Fatalf("anomalies: %v", err)
	}
	if len(items) == 0 {
		t.Fatalf("no anomalies surfaced for the degrade scenario — the acceptance story is broken")
	}
	for _, it := range items {
		if !it.Degrading || it.Delta >= 0 {
			t.Errorf("anomaly %+v: want degrading with negative delta", it)
		}
	}
}

// TestSynthValuesRespectBounds: every emitted value of every perf file
// stays inside its catalog plausibility bounds (the generator's own
// acceptance rule).
func TestSynthValuesRespectBounds(t *testing.T) {
	dir := t.TempDir()
	const days, devices, ports = 10, 12, 3
	for _, scenario := range []string{ScenarioNormal, ScenarioDegrade, ScenarioOutage} {
		sub := filepath.Join(dir, scenario)
		if _, err := WriteSynthData(SynthOptions{
			Out: sub, Devices: devices, Ports: ports, Days: days, Scenario: scenario, Seed: 42,
		}); err != nil {
			t.Fatalf("synthdata %s: %v", scenario, err)
		}
		for d := 1; d <= days; d++ {
			wb, err := workbook.Read(filepath.Join(sub, fmt.Sprintf("perf-day-%02d.xlsx", d)))
			if err != nil {
				t.Fatalf("read perf-day-%02d (%s): %v", d, scenario, err)
			}
			sheet := pickSheet(wb)
			if sheet == nil {
				t.Fatalf("perf-day-%02d (%s): no sheet", d, scenario)
			}
			for _, row := range sheet.Rows {
				for _, spec := range MetricKeys {
					header := spec.Synonyms[0]
					raw := row.Values[header]
					if raw == "" {
						t.Fatalf("%s perf-day-%02d: empty cell for %s", scenario, d, header)
					}
					var v float64
					if _, err := fmt.Sscanf(raw, "%g", &v); err != nil {
						t.Fatalf("%s perf-day-%02d: %s=%q unparseable: %v", scenario, d, header, raw, err)
					}
					if !spec.InBounds(v) {
						t.Fatalf("%s perf-day-%02d: %s=%v outside bounds [%v,%v]", scenario, d, header, v, spec.MinValue, spec.MaxValue)
					}
				}
			}
		}
	}
}

// TestSynthDegradeCurveShape: in the degrade scenario the fault subset's
// RX power declines monotonically ~0.15/day while the healthy ports stay
// flat — the deterministic subset is stable across the window.
func TestSynthDegradeCurveShape(t *testing.T) {
	dir := t.TempDir()
	const devices, ports, days = 20, 1, 6
	if _, err := WriteSynthData(SynthOptions{
		Out: dir, Devices: devices, Ports: ports, Days: days, Scenario: ScenarioDegrade, Seed: 7,
	}); err != nil {
		t.Fatalf("synthdata: %v", err)
	}
	opts := SynthOptions{Devices: devices, Ports: ports, Days: days, Scenario: ScenarioDegrade, Seed: 7}
	portUniverse := synthPorts(opts)

	faults, healthy := 0, 0
	firstDay := map[string]float64{}
	lastDay := map[string]float64{}
	for d := 1; d <= days; d++ {
		wb, err := workbook.Read(filepath.Join(dir, fmt.Sprintf("perf-day-%02d.xlsx", d)))
		if err != nil {
			t.Fatalf("read perf-day-%02d: %v", d, err)
		}
		sheet := pickSheet(wb)
		for _, row := range sheet.Rows {
			key := row.Values["设备名称"] + "|" + row.Values["端口名"]
			var v float64
			if _, err := fmt.Sscanf(row.Values["接收光功率均值"], "%g", &v); err != nil {
				t.Fatalf("parse rx: %v", err)
			}
			if d == 1 {
				firstDay[key] = v
			}
			lastDay[key] = v
		}
	}
	for _, p := range portUniverse {
		key := p.deviceName + "|" + p.portName
		delta := lastDay[key] - firstDay[key]
		if faultPort(opts, p) {
			faults++
			// -0.15/day × (days-1) with jitter ≤ 0.3 each side: the
			// decline must dominate (≤ -0.5 over 5 days).
			if delta > -0.5 {
				t.Errorf("fault port %s delta = %v, want a clear decline", key, delta)
			}
		} else {
			healthy++
			if delta < -0.7 || delta > 0.7 {
				t.Errorf("healthy port %s delta = %v, want flat (jitter only)", key, delta)
			}
		}
	}
	if faults == 0 || faults > len(portUniverse)/5 {
		t.Errorf("fault subset = %d of %d ports, want ~10%% (1..2 of 20)", faults, len(portUniverse))
	}
	_ = healthy
	_ = excelize.CoordinatesToCellName // keep excelize meaningful if unused elsewhere
	_ = domain.ResourceTypePort
}
