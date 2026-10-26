package fees_test

// catchup_columns_test.go — issue #11's unit pins: the ISO date parser,
// the catch-up column recognition (purchase window + the two money
// faces), the import round-trip with the new optional columns, the
// --strict-rows gate over them, and the idempotency-key stability (the
// catch-up keys ride the import, never the identity).
import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/fees"
	"github.com/mmqz/netops-fees-lab/internal/repo"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

func TestParseDateISO(t *testing.T) {
	ok := []struct{ raw, want string }{
		{"2026-01-15", "2026-01-15"},
		{"2026-1-5", "2026-01-05"},
		{"2026.1.15", "2026-01-15"},
		{"2026/01/15", "2026-01-15"},
		{"2026年1月15日", "2026-01-15"},
		{"2026年01月15", "2026-01-15"},
		{" 2026-01-15 ", "2026-01-15"},
		{"2024-02-29", "2024-02-29"}, // leap
	}
	for _, c := range ok {
		got, err := fees.ParseDateISO(c.raw)
		if err != nil || got != c.want {
			t.Errorf("ParseDateISO(%q) = %q, %v; want %q, nil", c.raw, got, err, c.want)
		}
	}
	bad := []string{
		"",                 // empty
		"2026-13-01",       // month out of range
		"2026-00-10",       // month zero
		"2026-02-30",       // not a calendar date
		"2023-02-29",       // non-leap Feb 29
		"2026-01-00",       // day zero
		"2026-01-32",       // day overflow
		"1999-01-01",       // below the 2000 bound
		"2101-01-01",       // above the 2100 bound
		"2026-01",          // no day
		"2026",             // month+day missing
		"2026-01-1x",       // garbage day
		"15/01/2026",       // wrong order
		"2026-01-15-extra", // trailing garbage
	}
	for _, raw := range bad {
		if got, err := fees.ParseDateISO(raw); err == nil {
			t.Errorf("ParseDateISO(%q) = %q, want an error", raw, got)
		}
	}
}

func TestCatchUpFieldRecognition(t *testing.T) {
	// The 转供电 sheet's real header shapes.
	cols := fees.RecognizeLedgerFields([]string{
		"站址编码", "购电起始日", "购电截止日", "应收电信", "实收电信", "电费(元)",
	})
	want := map[string]string{
		"purchaseStart":   "购电起始日",
		"purchaseEnd":     "购电截止日",
		"receivableCents": "应收电信",
		"receivedCents":   "实收电信",
	}
	for field, header := range want {
		if cols[field] != header {
			t.Errorf("catch-up field %s = %q, want %q", field, cols[field], header)
		}
	}
	// Variants: date-suffixed and generic-money shapes.
	cols = fees.RecognizeLedgerFields([]string{"购电起始日期", "购电截止时间", "应收金额", "实收金额"})
	if cols["purchaseStart"] != "购电起始日期" || cols["purchaseEnd"] != "购电截止时间" {
		t.Errorf("date variants: %v", cols)
	}
	if cols["receivableCents"] != "应收金额" || cols["receivedCents"] != "实收金额" {
		t.Errorf("money variants: %v", cols)
	}
	// The #15 lesson: generic form-date headers must NEVER map to the
	// purchase window — a bare 起始日期/日期/填表日期 is form metadata, not
	// purchase evidence.
	cols = fees.RecognizeLedgerFields([]string{"站址编码", "起始日期", "截止日期", "日期", "填表日期"})
	for _, f := range []string{"purchaseStart", "purchaseEnd"} {
		if cols[f] != "" {
			t.Errorf("generic date header must not map to %s, got %q", f, cols[f])
		}
	}
}

func TestCatchUpColumnsRoundTrip(t *testing.T) {
	dir := testsupport.TempDir(t)
	db, err := repo.Open(filepath.Join(dir, "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := fees.NewService(db, dir)

	rich := writeFeeCSV(t, dir, "transfer-supply.csv",
		"站址编码,购电起始日,购电截止日,应收电信,实收电信,电费(元),账期\n"+
			"HLJ-TS-01,2026.1.10,2026.4.15,34400,0,61500,2026-04\n"+
			"HLJ-TS-02,2026-04-01,2026-04-30,5000,5000,5000,2026-04\n")
	sum, err := svc.Import(t.Context(), fees.ImportOptions{Path: rich, FeeType: "electricity", Year: 2026, Month: 4})
	if err != nil || sum.Status != "completed" {
		t.Fatalf("catch-up rich import: %v (%+v)", err, sum)
	}
	if sum.RowsImported != 2 || sum.RowsFailed != 0 {
		t.Fatalf("rows = %d imported / %d failed, want 2/0 (%+v)", sum.RowsImported, sum.RowsFailed, sum)
	}
	// Idempotency: the catch-up keys ride the import, never the identity.
	_, err = svc.Import(t.Context(), fees.ImportOptions{Path: rich, FeeType: "electricity", Year: 2026, Month: 4})
	if err == nil {
		t.Fatal("same file re-import must stay idempotent-blocked (catch-up columns never move the key)")
	}
	// A file without the catch-up columns imports unchanged (pre-#11
	// byte-compat: no new warnings, no failed rows).
	plain := writeFeeCSV(t, dir, "plain11.csv",
		"站址编码,电费(元),账期\nHLJ-0009,20000,2026-05\n")
	sum2, err := svc.Import(t.Context(), fees.ImportOptions{Path: plain, FeeType: "electricity", Year: 2026, Month: 5})
	if err != nil || sum2.Status != "completed" || sum2.RowsFailed != 0 {
		t.Fatalf("plain import degraded: %v (%+v)", err, sum2)
	}
}

func TestCatchUpStrictRowsGate(t *testing.T) {
	dir := testsupport.TempDir(t)
	db, err := repo.Open(filepath.Join(dir, "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := fees.NewService(db, dir)

	bad := writeFeeCSV(t, dir, "bad-dates.csv",
		"站址编码,购电起始日,购电截止日,应收电信,实收电信,电费(元),账期\n"+
			"HLJ-TS-11,2026-01-10,2026-04-15,100,100,10000,2026-04\n"+
			"HLJ-TS-12,2026年13月1日,2026-04-15,100,100,10000,2026-04\n"+
			"HLJ-TS-13,2026-01-10,2026-04-15,10.555,100,10000,2026-04\n") // 3 fraction digits: NormalizeAmount refuses, never rounds
	sum, err := svc.Import(t.Context(), fees.ImportOptions{
		Path: bad, FeeType: "electricity", Year: 2026, Month: 4, StrictRows: true,
	})
	if err != nil {
		t.Fatalf("strict import call: %v", err)
	}
	if sum.RowsImported != 1 || sum.RowsFailed != 2 {
		t.Fatalf("strict gate: %d imported / %d failed, want 1/2 (bad date + bad amount each refuse their row)", sum.RowsImported, sum.RowsFailed)
	}
	joined := strings.Join(sum.Errors, "\n")
	if !strings.Contains(joined, "purchaseStart") || !strings.Contains(joined, "receivableCents") {
		t.Fatalf("strict errors must name the offending catch-up fields, got: %s", joined)
	}
	// Lenient mode: the same mangled cells stay nil and the rows import.
	dir2 := testsupport.TempDir(t)
	db2, err := repo.Open(filepath.Join(dir2, "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	svc2 := fees.NewService(db2, dir2)
	sum2, err := svc2.Import(t.Context(), fees.ImportOptions{Path: bad, FeeType: "electricity", Year: 2026, Month: 4})
	if err != nil || sum2.Status != "completed" || sum2.RowsImported != 3 {
		t.Fatalf("lenient import must take all 3 rows: %v (%+v)", err, sum2)
	}
}
