package metrics

// Audit §2.4 robustness pin (v0.8.1 deep-dive round): the committed
// perf-day sheets carry TEXT metric cells ('-8.00' as a string), while real
// NMS exports typically carry NUMERIC cells. The ingest path must accept
// both — the workbook reader renders numeric cells as their formatted
// string, which the row-level ParseFloat accepts. This test builds the
// perf sheet with REAL numeric cells (excelize SetCellValue, not
// SetCellStr) and drives the full ingest: a green result pins the
// both-formats robustness the audit asked to confirm.

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

// TestIngestNumericCells writes the canonical perf sheet with NUMERIC
// cells for every metric column (only the identity/date columns stay
// strings) and asserts the row lands as one point with the exact float.
func TestIngestNumericCells(t *testing.T) {
	s, db := newMetricService(t)
	ctx := context.Background()
	portID := seedPortInventory(t, db, "DEV-N", "10.0.0.9", "GE-0/0/9")

	dir := testsupport.TempDir(t)
	path := filepath.Join(dir, "perf-numeric.xlsx")
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	if err := f.SetSheetName("Sheet1", "端口性能"); err != nil {
		t.Fatalf("rename sheet: %v", err)
	}
	// Header row: strings (as every real export carries them).
	for c, h := range perfHeadersRow {
		cell, err := excelize.CoordinatesToCellName(c+1, 1)
		if err != nil {
			t.Fatalf("cell name: %v", err)
		}
		if err := f.SetCellStr("端口性能", cell, h); err != nil {
			t.Fatalf("write header %s: %v", cell, err)
		}
	}
	// Data row: identity columns as strings, EVERY metric column as a real
	// float cell (the audit's "real NMS export" shape).
	row := perfRow("2026-01-01", "DEV-N", "10.0.0.9", "GE-0/0/9", "-8.50")
	for c, v := range row {
		cell, err := excelize.CoordinatesToCellName(c+1, 2)
		if err != nil {
			t.Fatalf("cell name: %v", err)
		}
		if c < 4 {
			if err := f.SetCellStr("端口性能", cell, v); err != nil {
				t.Fatalf("write %s: %v", cell, err)
			}
		} else {
			// Numeric cell: parse the canonical string form and write the
			// NUMBER (what openpyxl/excelize do when a real export lands).
			num, err := parseStrictFloat(v)
			if err != nil {
				t.Fatalf("fixture value %q: %v", v, err)
			}
			if err := f.SetCellValue("端口性能", cell, num); err != nil {
				t.Fatalf("write numeric %s: %v", cell, err)
			}
		}
	}
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	sum, err := s.Ingest(ctx, path, rxMapping(), "perf-numeric.xlsx")
	if err != nil {
		t.Fatalf("ingest numeric cells: %v", err)
	}
	if sum.RowsImported != 1 || sum.RowsFailed != 0 || sum.PointsWritten != 1 {
		t.Fatalf("summary = %+v, want imported=1 failed=0 points=1", sum)
	}
	if sum.Status != domain.BatchStatusCompleted {
		t.Fatalf("batch status = %s, want completed", sum.Status)
	}

	// The point value round-trips the exact float (-8.5).
	series, err := db.Metrics.SeriesOfResource(ctx, portID, nil)
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	if len(series) != 1 {
		t.Fatalf("series count = %d, want 1", len(series))
	}
	points, err := db.Metrics.PointsOfSeries(ctx, series[0].Series.SeriesID, "", "")
	if err != nil {
		t.Fatalf("points: %v", err)
	}
	if len(points) != 1 || points[0].Value != -8.5 {
		t.Fatalf("point = %+v, want one point at -8.5", points)
	}
}

// parseStrictFloat parses the canonical perf-sheet numeric strings (the
// fixture builder's own values) — test-side helper only.
func parseStrictFloat(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
}
