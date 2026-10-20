package metrics

import (
	"context"
	"github.com/mmqz/netops-lab/internal/testsupport"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/importer"
	"github.com/mmqz/netops-lab/internal/repo"
)

// fixedClock is the deterministic bookkeeping clock of the metric service
// (batch started_at/finished_at only — ts values come from the data).
var fixedClock = func() time.Time {
	return time.Date(2026, 12, 1, 8, 0, 0, 0, time.UTC)
}

// newMetricService opens a fresh database and returns the metric Service
// bound to it (deterministic clock).
func newMetricService(t *testing.T) (*Service, *repo.DB) {
	t.Helper()
	db, err := repo.Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close: %v", err)
		}
	})
	s := New(db)
	s.Now = fixedClock
	return s, db
}

// writeWorkbook writes one xlsx workbook with a single named sheet and
// returns its path (the importer test helper, in miniature).
func writeWorkbook(t *testing.T, dir, name, sheet string, rows [][]string) string {
	t.Helper()
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	if sheet != "Sheet1" {
		if err := f.SetSheetName("Sheet1", sheet); err != nil {
			t.Fatalf("rename default sheet: %v", err)
		}
	}
	for r, row := range rows {
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

// seedPortInventory imports a small device+port inventory through the REAL
// resource import channel (the resolution lookup then matches exactly the
// natural-key shapes the production pipeline produces) and returns the
// port resource id of device/port.
func seedPortInventory(t *testing.T, db *repo.DB, device, ip, port string) string {
	t.Helper()
	dir := testsupport.TempDir(t)
	path := filepath.Join(dir, "resources.xlsx")
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	if err := f.SetSheetName("Sheet1", "网元设备"); err != nil {
		t.Fatalf("name device sheet: %v", err)
	}
	if _, err := f.NewSheet("端口资源表"); err != nil {
		t.Fatalf("create port sheet: %v", err)
	}
	devRows := [][]string{
		{"设备编码", "设备名称", "厂商", "型号", "管理IP", "状态"},
		{device, device, "测试厂商", "T-1", ip, "在用"},
	}
	portRows := [][]string{
		{"设备名称", "端口名称", "设备IP", "端口类型", "带宽Mbps"},
		{device, port, ip, "GE", "10000"},
	}
	for sheet, rows := range map[string][][]string{"网元设备": devRows, "端口资源表": portRows} {
		for r, row := range rows {
			for c, v := range row {
				cell, err := excelize.CoordinatesToCellName(c+1, r+1)
				if err != nil {
					t.Fatalf("cell name: %v", err)
				}
				if err := f.SetCellStr(sheet, cell, v); err != nil {
					t.Fatalf("write %s!%s: %v", sheet, cell, err)
				}
			}
		}
	}
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("save resources workbook: %v", err)
	}

	imp := importer.New(db)
	ctx := context.Background()
	a, err := imp.Analyze(ctx, path)
	if err != nil {
		t.Fatalf("analyze resources workbook: %v", err)
	}
	if _, err := imp.Execute(ctx, a.BatchID, a.SuggestedMapping); err != nil {
		t.Fatalf("execute resources workbook: %v", err)
	}
	r, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypePort, device+"|"+port)
	if err != nil {
		t.Fatalf("load seeded port: %v", err)
	}
	return r.ID
}

// perfRow builds one perf-sheet data row with the canonical headers.
func perfRow(date, device, ip, port, rxPower string) []string {
	return []string{date, device, ip, port, rxPower, "-2.10", "0.02", "0.01",
		"35.2", "30.1", "850.3", "400.2", "0", "1", "42.3", "3.30", "35.1", "-10.40"}
}

// perfHeadersRow is the header row matching perfRow (canonical set).
var perfHeadersRow = []string{"日期", "设备名称", "设备IP", "端口名",
	"接收光功率均值", "发送光功率均值", "接收丢包率", "发送丢包率",
	"接收带宽利用率", "发送带宽利用率", "接收速率", "发送速率",
	"接收错误包数", "发送错误包数", "光模块温度均值", "供电电压", "偏置电流", "接收光功率谷值"}

// rxMapping maps the canonical headers to the optical RX metric (the
// minimal two-metric mapping of the ingest tests).
func rxMapping() IngestMapping {
	return IngestMapping{
		TSField:      "日期",
		ResourceType: "port",
		ResolveBy:    []string{"device_name", "port_name"},
		Metrics:      map[string]string{"optical_power_rx_avg_dbm": "接收光功率均值"},
	}
}

// countPoints runs SELECT COUNT(*) FROM metrics.
func countPoints(t *testing.T, db *repo.DB) int {
	t.Helper()
	n, err := db.Metrics.CountMetricPoints(context.Background())
	if err != nil {
		t.Fatalf("count points: %v", err)
	}
	return n
}

// TestIngestResolvesBothWays pins the two resolution paths against the
// real resource inventory: (a) 设备名称+端口名 → the port natural key
// directly; (b) 设备IP+端口名 → the device by exact mgmt_ip match → its
// name → the port key. Both must land on the SAME series of the SAME port
// resource.
func TestIngestResolvesBothWays(t *testing.T) {
	s, db := newMetricService(t)
	ctx := context.Background()
	portID := seedPortInventory(t, db, "DEV-A", "10.0.0.1", "GE-0/0/1")

	dir := testsupport.TempDir(t)
	// Path (a): device name + port name.
	fileA := writeWorkbook(t, dir, "perf-a.xlsx", "端口性能", [][]string{
		perfHeadersRow,
		perfRow("2026-01-01", "DEV-A", "10.0.0.1", "GE-0/0/1", "-8.50"),
	})
	mA := rxMapping()
	sumA, err := s.Ingest(ctx, fileA, mA, "perf-a.xlsx")
	if err != nil {
		t.Fatalf("ingest (name path): %v", err)
	}
	if sumA.RowsTotal != 1 || sumA.RowsImported != 1 || sumA.RowsFailed != 0 || sumA.PointsWritten != 1 {
		t.Fatalf("ingest (name path) summary = %+v", sumA)
	}
	if sumA.Status != domain.BatchStatusCompleted {
		t.Fatalf("batch status = %s, want completed", sumA.Status)
	}

	// Path (b): device IP + port name.
	fileB := writeWorkbook(t, dir, "perf-b.xlsx", "端口性能", [][]string{
		perfHeadersRow,
		perfRow("2026-01-02", "whatever-display-name", "10.0.0.1", "GE-0/0/1", "-8.30"),
	})
	mB := rxMapping()
	mB.ResolveBy = []string{"device_ip", "port_name"}
	sumB, err := s.Ingest(ctx, fileB, mB, "perf-b.xlsx")
	if err != nil {
		t.Fatalf("ingest (ip path): %v", err)
	}
	if sumB.RowsImported != 1 || sumB.PointsWritten != 1 {
		t.Fatalf("ingest (ip path) summary = %+v", sumB)
	}

	// Both landed on the SAME series (one series row, two points, in ts order).
	series, err := db.Metrics.SeriesOfResource(ctx, portID, nil)
	if err != nil {
		t.Fatalf("series of port: %v", err)
	}
	if len(series) != 1 || series[0].Series.MetricKey != "optical_power_rx_avg_dbm" {
		t.Fatalf("series = %+v, want exactly the rx power series", series)
	}
	points, err := db.Metrics.PointsOfSeries(ctx, series[0].Series.SeriesID, "", "")
	if err != nil {
		t.Fatalf("points: %v", err)
	}
	if len(points) != 2 || points[0].TS != "2026-01-01T00:00:00Z" || points[1].TS != "2026-01-02T00:00:00Z" {
		t.Fatalf("points = %+v, want the two ordered daily points", points)
	}
	// Series metadata: first/last seen are data-derived (MIN/MAX ts).
	if series[0].Series.FirstSeenAt != "2026-01-01T00:00:00Z" || series[0].Series.LastSeenAt != "2026-01-02T00:00:00Z" {
		t.Errorf("series bounds = %q..%q, want data-derived 2026-01-01..2026-01-02",
			series[0].Series.FirstSeenAt, series[0].Series.LastSeenAt)
	}
}

// TestIngestIdempotency is the A2/#18 acceptance gate (ADR-0014 decision
// 2): the same workbook ingested 3× leaves SELECT COUNT(*) FROM metrics
// unchanged; a re-import of the same (series, ts) with a DIFFERENT value
// overwrites (last wins, metrics.batch_id names the last writer).
func TestIngestIdempotency(t *testing.T) {
	s, db := newMetricService(t)
	ctx := context.Background()
	seedPortInventory(t, db, "DEV-B", "10.0.0.2", "GE-0/0/1")

	dir := testsupport.TempDir(t)
	file := writeWorkbook(t, dir, "perf.xlsx", "端口性能", [][]string{
		perfHeadersRow,
		perfRow("2026-01-01", "DEV-B", "10.0.0.2", "GE-0/0/1", "-8.50"),
		perfRow("2026-01-02", "DEV-B", "10.0.0.2", "GE-0/0/1", "-8.42"),
	})
	m := rxMapping()

	first, err := s.Ingest(ctx, file, m, "perf.xlsx")
	if err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	afterFirst := countPoints(t, db)
	if afterFirst != 2 {
		t.Fatalf("points after first ingest = %d, want 2", afterFirst)
	}
	for i := 0; i < 2; i++ { // two more identical runs
		if _, err := s.Ingest(ctx, file, m, "perf.xlsx"); err != nil {
			t.Fatalf("re-ingest #%d: %v", i+2, err)
		}
		if got := countPoints(t, db); got != afterFirst {
			t.Fatalf("points after re-ingest #%d = %d, want unchanged %d (A2/#18: 同数据连导行数不变)", i+2, got, afterFirst)
		}
	}
	// Series rows also stay unique (one series per (resource, metric)).
	if n, err := db.Metrics.CountMetricSeries(ctx); err != nil || n != 1 {
		t.Fatalf("series rows = %d (err %v), want 1", n, err)
	}
	// Three batches recorded (audit trail), first one no longer owns the
	// points (the last writer does).
	batches, total, err := s.ListBatches(ctx, 10, 0)
	if err != nil || total != 3 || len(batches) != 3 {
		t.Fatalf("metric batches = %d/%d (err %v), want 3/3", len(batches), total, err)
	}
	owned, err := db.Metrics.CountPointsByBatch(ctx, first.BatchID)
	if err != nil {
		t.Fatalf("count points of first batch: %v", err)
	}
	if owned != 0 {
		t.Fatalf("points owned by the FIRST batch after re-ingests = %d, want 0 (last writer owns them)", owned)
	}

	// Same (series, ts), different value → last wins.
	fileV2 := writeWorkbook(t, dir, "perf-v2.xlsx", "端口性能", [][]string{
		perfHeadersRow,
		perfRow("2026-01-01", "DEV-B", "10.0.0.2", "GE-0/0/1", "-9.99"),
	})
	v2, err := s.Ingest(ctx, fileV2, m, "perf-v2.xlsx")
	if err != nil {
		t.Fatalf("value-overwrite ingest: %v", err)
	}
	if got := countPoints(t, db); got != afterFirst {
		t.Fatalf("points after value overwrite = %d, want unchanged %d (覆盖而非追加)", got, afterFirst)
	}
	series, err := db.Metrics.SeriesOfResource(ctx, mustPortID(t, db, "DEV-B", "GE-0/0/1"), nil)
	if err != nil || len(series) != 1 {
		t.Fatalf("series: %v (err %v)", series, err)
	}
	points, err := db.Metrics.PointsOfSeries(ctx, series[0].Series.SeriesID, "", "")
	if err != nil {
		t.Fatalf("points: %v", err)
	}
	var val float64
	for _, p := range points {
		if p.TS == "2026-01-01T00:00:00Z" {
			val = p.Value
			if p.BatchID != v2.BatchID {
				t.Errorf("overwritten point batch_id = %s, want the LAST writer %s", p.BatchID, v2.BatchID)
			}
		}
	}
	if val != -9.99 {
		t.Fatalf("value at 2026-01-01 after overwrite = %v, want -9.99 (末次胜出)", val)
	}
}

// mustPortID resolves a port resource id for the assertions.
func mustPortID(t *testing.T, db *repo.DB, device, port string) string {
	t.Helper()
	r, err := db.Resources.GetByTypeKey(context.Background(), domain.ResourceTypePort, device+"|"+port)
	if err != nil {
		t.Fatalf("load port %s|%s: %v", device, port, err)
	}
	return r.ID
}

// TestIngestSuspectQuality: a value outside the catalog's advisory bounds
// is recorded with quality='suspect' (never rejected — bounds are
// advisory, §5.3 quality column).
func TestIngestSuspectQuality(t *testing.T) {
	s, db := newMetricService(t)
	ctx := context.Background()
	portID := seedPortInventory(t, db, "DEV-C", "10.0.0.3", "GE-0/0/1")

	dir := testsupport.TempDir(t)
	file := writeWorkbook(t, dir, "perf.xlsx", "端口性能", [][]string{
		perfHeadersRow,
		perfRow("2026-01-01", "DEV-C", "10.0.0.3", "GE-0/0/1", "-77.0"), // below -30 bound
	})
	sum, err := s.Ingest(ctx, file, rxMapping(), "perf.xlsx")
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if sum.RowsImported != 1 || sum.RowsFailed != 0 {
		t.Fatalf("summary = %+v, want the row imported (suspect, not rejected)", sum)
	}
	points, err := db.Metrics.PointsOfSeries(ctx, mustSeriesID(t, db, portID), "", "")
	if err != nil || len(points) != 1 {
		t.Fatalf("points: %v (err %v)", points, err)
	}
	if points[0].Quality != domain.PointQualitySuspect {
		t.Fatalf("quality = %q, want suspect", points[0].Quality)
	}
	if points[0].Value != -77.0 {
		t.Fatalf("value = %v, want -77.0 (recorded, not rejected)", points[0].Value)
	}
}

// mustSeriesID returns the single series id of a port resource.
func mustSeriesID(t *testing.T, db *repo.DB, portID string) string {
	t.Helper()
	series, err := db.Metrics.SeriesOfResource(context.Background(), portID, nil)
	if err != nil || len(series) != 1 {
		t.Fatalf("series of %s: %v (err %v)", portID, series, err)
	}
	return series[0].Series.SeriesID
}

// TestIngestFailedRows: unresolvable port, unparseable date and
// non-numeric metric value each fail their row (counted, never fatal);
// the batch still completes.
func TestIngestFailedRows(t *testing.T) {
	s, db := newMetricService(t)
	ctx := context.Background()
	seedPortInventory(t, db, "DEV-D", "10.0.0.4", "GE-0/0/1")

	dir := testsupport.TempDir(t)
	file := writeWorkbook(t, dir, "perf.xlsx", "端口性能", [][]string{
		perfHeadersRow,
		perfRow("2026-01-01", "DEV-D", "10.0.0.4", "GE-0/0/1", "-8.50"), // ok
		perfRow("2026-01-01", "DEV-X", "10.0.0.4", "GE-0/0/1", "-8.50"), // unresolvable port
		perfRow("not-a-date", "DEV-D", "10.0.0.4", "GE-0/0/1", "-8.50"), // bad date
		perfRow("2026-01-02", "DEV-D", "10.0.0.4", "GE-0/0/1", "N/A"),   // non-numeric value
		perfRow("", "DEV-D", "10.0.0.4", "GE-0/0/1", "-8.50"),           // empty date
	})
	sum, err := s.Ingest(ctx, file, rxMapping(), "perf.xlsx")
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if sum.Status != domain.BatchStatusCompleted {
		t.Fatalf("status = %s, want completed (failed rows are counted, not fatal)", sum.Status)
	}
	if sum.RowsTotal != 5 || sum.RowsImported != 1 || sum.RowsFailed != 4 {
		t.Fatalf("summary = %+v, want 5 total / 1 imported / 4 failed", sum)
	}
	if got := countPoints(t, db); got != 1 {
		t.Fatalf("points = %d, want 1 (only the good row's point)", got)
	}
}

// TestIngestEmptyMetricCell: an empty metric cell produces NO point for
// that metric (real exports carry blanks) and does not fail the row.
func TestIngestEmptyMetricCell(t *testing.T) {
	s, db := newMetricService(t)
	ctx := context.Background()
	portID := seedPortInventory(t, db, "DEV-E", "10.0.0.5", "GE-0/0/1")

	dir := testsupport.TempDir(t)
	row := perfRow("2026-01-01", "DEV-E", "10.0.0.5", "GE-0/0/1", "")
	file := writeWorkbook(t, dir, "perf.xlsx", "端口性能", [][]string{perfHeadersRow, row})
	sum, err := s.Ingest(ctx, file, rxMapping(), "perf.xlsx")
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if sum.RowsImported != 1 || sum.RowsFailed != 0 || sum.PointsWritten != 0 {
		t.Fatalf("summary = %+v, want row imported with zero points", sum)
	}
	if got := countPoints(t, db); got != 0 {
		t.Fatalf("points = %d, want 0", got)
	}
	_ = portID
}

// TestIngestDateForms: every accepted date form normalizes to the same
// daily point (excel serial 45658 = 2024-12-31… actually pinned below) —
// different spellings of the same day converge on one (series, ts).
func TestIngestDateForms(t *testing.T) {
	s, db := newMetricService(t)
	ctx := context.Background()
	seedPortInventory(t, db, "DEV-F", "10.0.0.6", "GE-0/0/1")
	dir := testsupport.TempDir(t)
	file := writeWorkbook(t, dir, "perf.xlsx", "端口性能", [][]string{
		perfHeadersRow,
		perfRow("2026-01-15", "DEV-F", "10.0.0.6", "GE-0/0/1", "-8.10"),
		perfRow("2026/1/15", "DEV-F", "10.0.0.6", "GE-0/0/1", "-8.20"),
		perfRow("2026-01-15 08:30:00", "DEV-F", "10.0.0.6", "GE-0/0/1", "-8.30"),
	})
	if _, err := s.Ingest(ctx, file, rxMapping(), "perf.xlsx"); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if got := countPoints(t, db); got != 1 {
		t.Fatalf("points = %d, want 1 (three spellings of the same day converge)", got)
	}
	series, err := db.Metrics.SeriesOfResource(ctx, mustPortID(t, db, "DEV-F", "GE-0/0/1"), nil)
	if err != nil || len(series) != 1 {
		t.Fatalf("series: %v (err %v)", series, err)
	}
	points, err := db.Metrics.PointsOfSeries(ctx, series[0].Series.SeriesID, "", "")
	if err != nil || len(points) != 1 {
		t.Fatalf("points: %v", points)
	}
	if points[0].TS != "2026-01-15T00:00:00Z" {
		t.Fatalf("ts = %q, want 2026-01-15T00:00:00Z", points[0].TS)
	}
	if points[0].Value != -8.30 {
		t.Fatalf("value = %v, want the LAST write -8.30", points[0].Value)
	}
}

// TestIngestRejectsBadMapping: a mapping that does not fit the workbook
// fails BEFORE any batch is created (no orphan batch rows).
func TestIngestRejectsBadMapping(t *testing.T) {
	s, db := newMetricService(t)
	ctx := context.Background()
	dir := testsupport.TempDir(t)
	file := writeWorkbook(t, dir, "perf.xlsx", "端口性能", [][]string{
		perfHeadersRow,
		perfRow("2026-01-01", "DEV-G", "10.0.0.7", "GE-0/0/1", "-8.50"),
	})
	bad := rxMapping()
	bad.Metrics = map[string]string{"no_such_metric": "接收光功率均值"}
	if _, err := s.Ingest(ctx, file, bad, "perf.xlsx"); err == nil {
		t.Fatalf("ingest with unknown metric key accepted, want error")
	}
	batches, total, err := s.ListBatches(ctx, 10, 0)
	if err != nil || total != 0 || len(batches) != 0 {
		t.Fatalf("metric batches after rejected mapping = %d (err %v), want 0 (nothing recorded)", total, err)
	}
	if got := countPoints(t, db); got != 0 {
		t.Fatalf("points = %d, want 0", got)
	}
}

// TestRollbackSharedSeries pins the metric rollback asymmetry: rolling
// back a batch deletes the points it CURRENTLY owns; a series still
// holding other batches' points survives; only emptied series are pruned;
// non-completed batches refuse with a conflict; unknown batch → not
// found.
func TestRollbackSharedSeries(t *testing.T) {
	s, db := newMetricService(t)
	ctx := context.Background()
	portID := seedPortInventory(t, db, "DEV-H", "10.0.0.8", "GE-0/0/1")
	dir := testsupport.TempDir(t)

	// Batch 1: two days.
	file1 := writeWorkbook(t, dir, "perf-1.xlsx", "端口性能", [][]string{
		perfHeadersRow,
		perfRow("2026-01-01", "DEV-H", "10.0.0.8", "GE-0/0/1", "-8.50"),
		perfRow("2026-01-02", "DEV-H", "10.0.0.8", "GE-0/0/1", "-8.40"),
	})
	b1, err := s.Ingest(ctx, file1, rxMapping(), "perf-1.xlsx")
	if err != nil {
		t.Fatalf("ingest 1: %v", err)
	}
	// Batch 2: day 2 again (overwrites batch 1's point) + day 3.
	file2 := writeWorkbook(t, dir, "perf-2.xlsx", "端口性能", [][]string{
		perfHeadersRow,
		perfRow("2026-01-02", "DEV-H", "10.0.0.8", "GE-0/0/1", "-8.45"),
		perfRow("2026-01-03", "DEV-H", "10.0.0.8", "GE-0/0/1", "-8.35"),
	})
	b2, err := s.Ingest(ctx, file2, rxMapping(), "perf-2.xlsx")
	if err != nil {
		t.Fatalf("ingest 2: %v", err)
	}
	if got := countPoints(t, db); got != 3 {
		t.Fatalf("points = %d, want 3", got)
	}

	// Point ownership after both ingests: 01-01 → batch 1 (never
	// overwritten), 01-02 → batch 2 (REPLACE moved the lineage), 01-03 →
	// batch 2.
	if owned, _ := db.Metrics.CountPointsByBatch(ctx, b1.BatchID); owned != 1 {
		t.Fatalf("points owned by batch 1 = %d, want 1 (01-01)", owned)
	}
	if owned, _ := db.Metrics.CountPointsByBatch(ctx, b2.BatchID); owned != 2 {
		t.Fatalf("points owned by batch 2 = %d, want 2 (01-02, 01-03)", owned)
	}

	// Roll back the LATER batch first: it owns 01-02 and 01-03 → both
	// deleted; the series SURVIVES with batch 1's 01-01 point (the shared
	// series is kept — completed decisions stay auditable).
	res, err := s.Rollback(ctx, b2.BatchID)
	if err != nil {
		t.Fatalf("rollback batch 2: %v", err)
	}
	if res.DeletedPoints != 2 {
		t.Errorf("deleted points = %d, want 2 (01-02 and 01-03)", res.DeletedPoints)
	}
	if res.PrunedSeries != 0 {
		t.Errorf("pruned series = %d, want 0 (01-01 of batch 1 survives)", res.PrunedSeries)
	}
	if got := countPoints(t, db); got != 1 {
		t.Errorf("points after rollback 2 = %d, want 1", got)
	}
	points, err := db.Metrics.PointsOfSeries(ctx, mustSeriesID(t, db, portID), "", "")
	if err != nil || len(points) != 1 || points[0].TS != "2026-01-01T00:00:00Z" || points[0].BatchID != b1.BatchID {
		t.Fatalf("points after rollback 2 = %+v, want batch 1's 01-01 point", points)
	}

	// Roll back batch 1 too: its last point goes, the now-empty series is
	// pruned — nothing dangles.
	res1, err := s.Rollback(ctx, b1.BatchID)
	if err != nil {
		t.Fatalf("rollback batch 1: %v", err)
	}
	if res1.DeletedPoints != 1 || res1.PrunedSeries != 1 {
		t.Errorf("rollback 1 result = %+v, want 1 point + 1 pruned series", res1)
	}
	if got := countPoints(t, db); got != 0 {
		t.Errorf("points after both rollbacks = %d, want 0", got)
	}

	// A second rollback of the already-rolled-back batch refuses (409).
	if _, err := s.Rollback(ctx, b2.BatchID); err == nil {
		t.Errorf("rollback of rolled_back batch accepted, want conflict")
	}

	// Unknown batch → not found.
	if _, err := s.Rollback(ctx, "mbat_nope"); err == nil {
		t.Errorf("rollback of unknown batch accepted, want not found")
	}
}

// TestRollbackPrunesEmptySeries: when a batch owns the last points of a
// series, the rollback deletes them AND prunes the now-empty series row.
func TestRollbackPrunesEmptySeries(t *testing.T) {
	s, db := newMetricService(t)
	ctx := context.Background()
	seedPortInventory(t, db, "DEV-I", "10.0.0.9", "GE-0/0/1")
	dir := testsupport.TempDir(t)
	file := writeWorkbook(t, dir, "perf.xlsx", "端口性能", [][]string{
		perfHeadersRow,
		perfRow("2026-01-01", "DEV-I", "10.0.0.9", "GE-0/0/1", "-8.50"),
	})
	b, err := s.Ingest(ctx, file, rxMapping(), "perf.xlsx")
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	res, err := s.Rollback(ctx, b.BatchID)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if res.DeletedPoints != 1 || res.PrunedSeries != 1 {
		t.Fatalf("rollback result = %+v, want 1 point deleted + 1 series pruned", res)
	}
	if got := countPoints(t, db); got != 0 {
		t.Fatalf("points after rollback = %d, want 0", got)
	}
	if n, err := db.Metrics.CountMetricSeries(ctx); err != nil || n != 0 {
		t.Fatalf("series after rollback = %d (err %v), want 0", n, err)
	}
	// Batch row stays as the audit trail with rolled_back status.
	got, err := db.MetricBatches.Get(ctx, b.BatchID)
	if err != nil || got.Status != domain.BatchStatusRolledBack {
		t.Fatalf("batch after rollback = %+v (err %v), want rolled_back", got, err)
	}
}

// TestServiceAnomalies is the §5.5 Q2 scan at service level: a declining
// optical-power series surfaces with a negative delta and
// degrading=true; a stable series stays below the threshold; a rising
// loss-rate series (lower_is_better) is degrading with a positive delta.
func TestServiceAnomalies(t *testing.T) {
	s, db := newMetricService(t)
	ctx := context.Background()
	seedPortInventory(t, db, "DEV-J1", "10.0.1.1", "GE-0/0/1")
	seedPortInventory(t, db, "DEV-J2", "10.0.1.2", "GE-0/0/1")

	dir := testsupport.TempDir(t)
	// DEV-J1: optical power declines 0.5/day for 6 days (delta -2.5).
	rows := [][]string{perfHeadersRow}
	for d := 1; d <= 6; d++ {
		rx := -8.0 - 0.5*float64(d-1)
		rows = append(rows, perfRow(
			"2026-01-"+pad2(d), "DEV-J1", "10.0.1.1", "GE-0/0/1", fmt2(rx)))
	}
	// DEV-J2: stable (jitter far below threshold).
	for d := 1; d <= 6; d++ {
		rows = append(rows, perfRow(
			"2026-01-"+pad2(d), "DEV-J2", "10.0.1.2", "GE-0/0/1", "-8.01"))
	}
	file := writeWorkbook(t, dir, "perf.xlsx", "端口性能", rows)
	m := rxMapping()
	m.Metrics["packet_loss_rx_pct"] = "接收丢包率"
	if _, err := s.Ingest(ctx, file, m, "perf.xlsx"); err != nil {
		t.Fatalf("ingest: %v", err)

	}

	items, err := s.Anomalies(ctx, "", "optical_power_rx_avg_dbm", "", "", 1.0, "")
	if err != nil {
		t.Fatalf("anomalies: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("anomalies = %+v, want exactly the declining port", items)
	}
	it := items[0]
	// The port resource's display name is port_name (the spec's
	// NameFieldOverride) — the anomaly carries the RESOURCE's name, and
	// the resource is the port, not the device.
	if it.ResourceName != "GE-0/0/1" || it.ResourceType != "port" {
		t.Errorf("anomaly resource = %s/%s, want GE-0/0/1/port", it.ResourceName, it.ResourceType)
	}
	if it.N != 6 || it.FirstValue != -8.0 || it.LastValue != -10.5 {
		t.Errorf("anomaly series stats = n %d, first %v, last %v", it.N, it.FirstValue, it.LastValue)
	}
	if it.Delta != -2.5 {
		t.Errorf("delta = %v, want -2.5", it.Delta)
	}
	if !it.Degrading {
		t.Errorf("degrading = false, want true (higher_is_better declining)")
	}
	if it.Unit != "dBm" || it.Direction != "higher_is_better" {
		t.Errorf("unit/direction = %q/%q", it.Unit, it.Direction)
	}

	// The declining direction filter keeps it; the rising filter drops it.
	if items, _ := s.Anomalies(ctx, "", "optical_power_rx_avg_dbm", "", "", 1.0, "declining"); len(items) != 1 {
		t.Errorf("direction=declining items = %d, want 1", len(items))
	}
	if items, _ := s.Anomalies(ctx, "", "optical_power_rx_avg_dbm", "", "", 1.0, "rising"); len(items) != 0 {
		t.Errorf("direction=rising items = %d, want 0", len(items))
	}
	// Window bound: scanning from 01-04 shrinks the delta to -1.0 (first
	// of window -9.5, last -10.5 — still >= the 1.0 threshold).
	if items, _ := s.Anomalies(ctx, "", "optical_power_rx_avg_dbm", "2026-01-04T00:00:00Z", "", 1.0, ""); len(items) != 1 {
		t.Errorf("windowed items = %d, want 1", len(items))
	}
	// A lower_is_better rising trend (packet loss creeping up) is
	// degrading with a POSITIVE delta.
	lossRows := [][]string{perfHeadersRow}
	for d := 1; d <= 6; d++ {
		loss := 0.02 + 0.4*float64(d-1)
		lossRows = append(lossRows, perfRow(
			"2026-01-"+pad2(d), "DEV-J2", "10.0.1.2", "GE-0/0/1", "-8.01"))
		lossRows[len(lossRows)-1][6] = fmt2(loss) // 接收丢包率 column
	}
	file2 := writeWorkbook(t, dir, "perf-loss.xlsx", "端口性能", lossRows)
	if _, err := s.Ingest(ctx, file2, m, "perf-loss.xlsx"); err != nil {
		t.Fatalf("ingest loss: %v", err)
	}
	lossItems, err := s.Anomalies(ctx, "", "packet_loss_rx_pct", "", "", 1.0, "")
	if err != nil {
		t.Fatalf("anomalies (loss): %v", err)
	}
	if len(lossItems) != 1 || !lossItems[0].Degrading || lossItems[0].Delta <= 0 {
		t.Fatalf("loss anomalies = %+v, want one degrading item with positive delta", lossItems)
	}
}

// pad2 renders d as a 2-digit string.
func pad2(d int) string {
	if d < 10 {
		return "0" + string(rune('0'+d))
	}
	return string(rune('0'+d/10)) + string(rune('0'+d%10))
}

// fmt2 renders v with 2 decimals.
func fmt2(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}

// TestServiceResourceSeries covers the resource-metrics query service:
// raw + daily aggregation + the keys filter + bounds + error taxonomy.
func TestServiceResourceSeries(t *testing.T) {
	s, db := newMetricService(t)
	ctx := context.Background()
	portID := seedPortInventory(t, db, "DEV-K", "10.0.2.1", "GE-0/0/1")

	dir := testsupport.TempDir(t)
	rows := [][]string{perfHeadersRow}
	for d := 1; d <= 4; d++ {
		rx := -8.0 - 0.1*float64(d-1)
		rows = append(rows, perfRow("2026-01-"+pad2(d), "DEV-K", "10.0.2.1", "GE-0/0/1", fmt2(rx)))
	}
	m := rxMapping()
	m.Metrics["packet_loss_rx_pct"] = "接收丢包率"
	file := writeWorkbook(t, dir, "perf.xlsx", "端口性能", rows)
	if _, err := s.Ingest(ctx, file, m, "perf.xlsx"); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	// Raw: both series ordered by metric_key, points in ts order.
	rawSeries, err := s.ResourceSeries(ctx, portID, nil, "", "", AggRaw)
	if err != nil {
		t.Fatalf("raw series: %v", err)
	}
	if len(rawSeries) != 2 {
		t.Fatalf("series = %d, want 2", len(rawSeries))
	}
	if rawSeries[0].MetricKey != "optical_power_rx_avg_dbm" || rawSeries[1].MetricKey != "packet_loss_rx_pct" {
		t.Fatalf("series order = %s, %s (want metric_key order)", rawSeries[0].MetricKey, rawSeries[1].MetricKey)
	}
	if len(rawSeries[0].Points) != 4 || rawSeries[0].Points[0].TS != "2026-01-01T00:00:00Z" {
		t.Fatalf("raw points = %+v", rawSeries[0].Points)
	}
	if rawSeries[0].Unit != "dBm" {
		t.Errorf("unit = %q, want dBm", rawSeries[0].Unit)
	}

	// Keys filter.
	only, err := s.ResourceSeries(ctx, portID, []string{"packet_loss_rx_pct"}, "", "", AggRaw)
	if err != nil || len(only) != 1 || only[0].MetricKey != "packet_loss_rx_pct" {
		t.Fatalf("filtered series = %+v (err %v)", only, err)
	}

	// Daily aggregation: two points per day (raw daily data) → 4 daily
	// points of the same value; injecting a mid-day second point exercises
	// the AVG arithmetic below in the API test. Here: AVG of one point per
	// day = the point itself.
	daily, err := s.ResourceSeries(ctx, portID, []string{"optical_power_rx_avg_dbm"}, "", "", AggDailyAvg)
	if err != nil || len(daily) != 1 || len(daily[0].Points) != 4 {
		t.Fatalf("daily series = %+v (err %v)", daily, err)
	}
	if daily[0].Points[0].TS != "2026-01-01T00:00:00Z" || daily[0].Points[0].Value != -8.0 {
		t.Fatalf("daily point = %+v", daily[0].Points[0])
	}

	// Time bounds: [from=01-02, to=01-03] keeps 2 raw points.
	bounded, err := s.ResourceSeries(ctx, portID, []string{"optical_power_rx_avg_dbm"},
		"2026-01-02T00:00:00Z", "2026-01-03T00:00:00Z", AggRaw)
	if err != nil || len(bounded[0].Points) != 2 {
		t.Fatalf("bounded points = %+v (err %v)", bounded, err)
	}

	// Bad agg kind → typed error.
	if _, err := s.ResourceSeries(ctx, portID, nil, "", "", AggKind("bogus")); err == nil {
		t.Errorf("bogus agg accepted, want error")
	}
}
