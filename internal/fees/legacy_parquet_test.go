//go:build duckdb

package fees

// legacy_parquet_test.go — the pre-#11 generation fixture writer: a
// parquet file with the #5-era schema (record_key + the 11 ledger
// columns, NO purchase/receivable columns), hand-written so the schema
// guard tests run against the true legacy shape without needing a
// checked-in binary fixture.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/parquet-go/parquet-go"
)

// legacyFeeRow is the #5-era on-disk schema (post-#5, pre-#11).
type legacyFeeRow struct {
	SiteCode     string   `parquet:"site_code"`
	AmountCents  int64    `parquet:"amount_cents"`
	SourceFile   string   `parquet:"source_file"`
	SourceRow    int32    `parquet:"source_row"`
	ImportBatch  string   `parquet:"import_batch"`
	RecordKey    *string  `parquet:"record_key,optional"`
	MeterNo      *string  `parquet:"meter_no,optional"`
	ReadingStart *float64 `parquet:"reading_start,optional"`
	ReadingEnd   *float64 `parquet:"reading_end,optional"`
	MeterPrevEnd *float64 `parquet:"meter_prev_end,optional"`
	MeterSwapped *bool    `parquet:"meter_swapped,optional"`
	Quantity     *float64 `parquet:"quantity,optional"`
	UnitPrice    *float64 `parquet:"unit_price,optional"`
	ShareMobile  *float64 `parquet:"share_mobile,optional"`
	ShareUnicom  *float64 `parquet:"share_unicom,optional"`
	ShareTelecom *float64 `parquet:"share_telecom,optional"`
	ShareTower   *float64 `parquet:"share_tower,optional"`
}

// writeLegacyParquet lays down one electricity partition of the #5-era
// schema with a zero-quantity-billing row (so scenario 9 has something
// to find besides the guard proof) and a normal row.
func writeLegacyParquet(t *testing.T, dir string) {
	t.Helper()
	part := filepath.Join(dir, "analysis", "fees", "fee_type=electricity", "year=2026", "month=1")
	if err := os.MkdirAll(part, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(part, "legacy.snappy.parquet"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	w := parquet.NewGenericWriter[legacyFeeRow](f)
	rows := []legacyFeeRow{
		{SiteCode: "OLD-01", AmountCents: 800000, SourceFile: "legacy.csv", SourceRow: 1, ImportBatch: "legacy-b1",
			Quantity: ptrFloat(0)},
		{SiteCode: "OLD-02", AmountCents: 500000, SourceFile: "legacy.csv", SourceRow: 2, ImportBatch: "legacy-b1",
			Quantity: ptrFloat(120)},
	}
	if _, err := w.Write(rows); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func ptrFloat(v float64) *float64 { return &v }
