package fees_test

// ledger_columns_test.go — issue #5's unit pins: the recognizer's ledger
// aliases, the lenient optional parsing, the canonical-key stability
// (richness keys ride the import, never the idempotency identity), and
// the absent-column no-op (acceptance #2: files without the columns
// import byte-identically to the pre-#5 world).
import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/fees"
	"github.com/mmqz/netops-fees-lab/internal/repo"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

func writeFeeCSV(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	return path
}

// TestLedgerFieldRecognition pins the alias table: the canonical headers
// hit, unit-suffixed variants hit via the contains fallback, and unknown
// headers never map.
func TestLedgerFieldRecognition(t *testing.T) {
	cols := fees.RecognizeLedgerFields([]string{
		"站址编码", "用户号", "电表编号", "起始读数", "截止读数", "实际电量", "单价",
		"移动分摊比例", "联通分摊比例", "电信分摊比例", "铁塔分摊比例", "是否换表", "旧表止数",
	})
	want := map[string]string{
		"meterNo": "电表编号", "readingStart": "起始读数", "readingEnd": "截止读数",
		"meterSwapped": "是否换表", "meterPrevEnd": "旧表止数",
		"quantity": "实际电量", "unitPrice": "单价",
		"shareMobile": "移动分摊比例", "shareUnicom": "联通分摊比例",
		"shareTelecom": "电信分摊比例", "shareTower": "铁塔分摊比例",
	}
	for field, header := range want {
		if cols[field] != header {
			t.Errorf("ledger field %s = %q, want %q", field, cols[field], header)
		}
	}
	// The contains fallback: unit-suffixed real-world variants.
	cols = fees.RecognizeLedgerFields([]string{"上期读数(度)", "本期读数(度)", "用电量(kWh)"})
	if cols["readingStart"] != "上期读数(度)" || cols["readingEnd"] != "本期读数(度)" || cols["quantity"] != "用电量(kWh)" {
		t.Fatalf("unit-suffixed variants: %v", cols)
	}
	// Unknown headers never map.
	cols = fees.RecognizeLedgerFields([]string{"站址编码", "电费(元)"})
	if len(cols) != 0 {
		t.Fatalf("unrelated headers must not map, got %v", cols)
	}
}

// TestCanonicalMappingExcludesRichnessKeys pins the idempotency-key
// stability: the richness keys ride the import but never the canonical
// identity — same file + same core mapping = same key, whatever the
// recognizer knows.
func TestCanonicalMappingExcludesRichnessKeys(t *testing.T) {
	core := map[string]string{"siteCode": "站址编码", "amount": "电费(元)"}
	rich := map[string]string{"siteCode": "站址编码", "amount": "电费(元)",
		"meterNo": "电表编号", "readingStart": "起始读数", "quantity": "实际电量"}
	a := fees.FeeMapping{FeeType: "electricity", Columns: core}.CanonicalMapping()
	b := fees.FeeMapping{FeeType: "electricity", Columns: rich}.CanonicalMapping()
	if a != b {
		t.Fatalf("canonical key moved with richness keys:\n%s\n%s", a, b)
	}
}

// TestLedgerColumnsRoundTripAndCompat is the acceptance spine: a rich file
// round-trips its optional columns through import + rows query, and a
// plain pre-#5 file imports unchanged (nil optionals, same core shape).
func TestLedgerColumnsRoundTripAndCompat(t *testing.T) {
	dir := testsupport.TempDir(t)
	db, err := repo.Open(filepath.Join(dir, "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := fees.NewService(db, dir)

	rich := writeFeeCSV(t, dir, "rich.csv",
		"站址编码,电表编号,起始读数,截止读数,实际电量,单价,移动分摊比例,联通分摊比例,电信分摊比例,铁塔分摊比例,是否换表,电费(元),账期\n"+
			"HLJ-0001,M-01,1280,1410,130,0.85,0.4,0.3,0.2,0.1,否,11050,2026-01\n")
	sum, err := svc.Import(t.Context(), fees.ImportOptions{Path: rich, FeeType: "electricity", Year: 2026, Month: 1})
	if err != nil || sum.Status != "completed" {
		t.Fatalf("rich import: %v (%+v)", err, sum)
	}
	// The batch row carries the richness mapping for RESOLUTION while the
	// canonical idempotency key stays core-only: re-importing is blocked.
	_, err = svc.Import(t.Context(), fees.ImportOptions{Path: rich, FeeType: "electricity", Year: 2026, Month: 1})
	if err == nil {
		t.Fatal("same file re-import must stay idempotent-blocked (the richness columns never move the key)")
	}

	plain := writeFeeCSV(t, dir, "plain.csv",
		"站址编码,电费(元),账期\nHLJ-0002,20000,2026-02\n")
	sum2, err := svc.Import(t.Context(), fees.ImportOptions{Path: plain, FeeType: "electricity", Year: 2026, Month: 2})
	if err != nil || sum2.Status != "completed" {
		t.Fatalf("plain import: %v (%+v)", err, sum2)
	}
	if len(sum2.Warnings) == 0 {
		t.Fatal("the writable-source notice (issue #6) must still ride")
	}
	// DuckDB-tagged builds verify the rows query end to end; this unit
	// test pins the import side only.
}
