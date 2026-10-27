package fees

// period_dup_test.go — issue #20's service-level pins: the same file under
// a different period is refused by default (both periods + batch id in the
// message), --allow-period-duplicate overrides with a durable audit trail,
// the multi-sheet channel is guarded workbook-wide, and verify-batches
// surfaces the three-face inconsistencies of pre-#20 history. #24's
// complete-field duplicate answers are pinned here too (one builder, both
// duplicate paths).
import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// plainCSVDup builds a period-column-less CSV (C档: manual period, nothing
// in the content contradicts any declaration).
func plainCSVDup(t *testing.T, dir, name string) string {
	t.Helper()
	return writeFeeCSVP(t, dir, name,
		"站址编码,电费(元)\nHLJ-01,100.00\nHLJ-02,200.00\n")
}

func TestCrossPeriodDuplicateRefusedByDefault(t *testing.T) {
	svc, dir := newFeeService(t)
	path := plainCSVDup(t, dir, "dup.csv")

	if _, err := svc.Import(context.Background(), ImportOptions{
		Path: path, FeeType: FeeTypeElectricity, Year: 2026, Month: 1,
	}); err != nil {
		t.Fatalf("first import: %v", err)
	}
	// Same bytes, different month: MUST refuse, naming both periods, the
	// existing batch and the override flag.
	_, err := svc.Import(context.Background(), ImportOptions{
		Path: path, FeeType: FeeTypeElectricity, Year: 2026, Month: 5,
	})
	if err == nil {
		t.Fatal("cross-period re-import must refuse by default")
	}
	for _, want := range []string{"2026-01", "2026-05", "--allow-period-duplicate"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must contain %q: %v", want, err)
		}
	}
	// The refusal is honest about the mechanism: nothing landed.
	list, _ := svc.List(context.Background(), FeeStatusCompleted)
	if len(list) != 1 {
		t.Fatalf("completed batches = %d, want 1 (the refusal wrote nothing)", len(list))
	}
}

func TestCrossPeriodDuplicateAllowOverridesWithTrail(t *testing.T) {
	svc, dir := newFeeService(t)
	path := plainCSVDup(t, dir, "dup.csv")
	if _, err := svc.Import(context.Background(), ImportOptions{
		Path: path, FeeType: FeeTypeElectricity, Year: 2026, Month: 1,
	}); err != nil {
		t.Fatalf("first import: %v", err)
	}
	sum, err := svc.Import(context.Background(), ImportOptions{
		Path: path, FeeType: FeeTypeElectricity, Year: 2026, Month: 5,
		AllowPeriodDuplicate: true,
	})
	if err != nil {
		t.Fatalf("override import: %v", err)
	}
	if sum.Status != "completed" {
		t.Fatalf("status = %q, want completed", sum.Status)
	}
	// The override rides the response warnings...
	var warned bool
	for _, w := range sum.Warnings {
		if strings.Contains(w, "跨账期重复覆盖") && strings.Contains(w, "2026-01") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("warnings = %v, want the cross-period override audit note", sum.Warnings)
	}
	// ...AND the durable batch row (period_basis names both batches).
	b, err := getBatchRow(t, svc, sum.BatchID)
	if err != nil {
		t.Fatalf("get batch: %v", err)
	}
	if !strings.Contains(b.PeriodBasis, "--allow-period-duplicate") || !strings.Contains(b.PeriodBasis, "2026-01") {
		t.Fatalf("batch periodBasis = %q, want the durable override trail", b.PeriodBasis)
	}
	// Both batches completed — the operator's explicit choice, fully audited.
	list, _ := svc.List(context.Background(), FeeStatusCompleted)
	if len(list) != 2 {
		t.Fatalf("completed batches = %d, want 2", len(list))
	}
}

func TestCrossPeriodDuplicateMultiSheetGuarded(t *testing.T) {
	svc, dir := newFeeService(t)
	// A one-sheet xlsx whose sheet name declares electricity; imported via
	// the MULTI-SHEET channel first, then the same channel under another
	// period must refuse (no mode conflict can fire — the cross-period
	// guard itself is under test, workbook-wide).
	path := writeLedgerXLSX(t, dir, "book.xlsx", []sheetSpec{
		{"电费", [][]string{
			{"站址编码", "账期", "电费(元)"},
			{"S1", "2026-01", "10.00"},
		}},
	})
	if _, err := svc.ImportSheets(context.Background(), SheetsImportOptions{
		Path: path, Year: 2026, Month: 1,
	}); err != nil {
		t.Fatalf("first multi-sheet import: %v", err)
	}
	_, err := svc.ImportSheets(context.Background(), SheetsImportOptions{
		Path: path, Year: 2026, Month: 5,
	})
	if err == nil {
		t.Fatal("multi-sheet cross-period re-import must refuse")
	}
	for _, want := range []string{"2026-01", "2026-05", "--allow-period-duplicate"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must contain %q: %v", want, err)
		}
	}
}

func TestVerifyBatchesCleanThenDirty(t *testing.T) {
	svc, dir := newFeeService(t)
	ctx := context.Background()
	path := plainCSVDup(t, dir, "clean.csv")
	if _, err := svc.Import(ctx, ImportOptions{
		Path: path, FeeType: FeeTypeElectricity, Year: 2026, Month: 1,
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	// Clean post-#20 world: one batch, all three faces agree.
	rep, err := svc.VerifyBatches(ctx)
	if err != nil {
		t.Fatalf("verify-batches: %v", err)
	}
	if rep.BatchesChecked != 1 || len(rep.Problems) != 0 {
		t.Fatalf("report = %+v, want 1 checked / 0 problems", rep)
	}

	// Dirty shapes: (a) the same file under a second period via the override;
	// (b) a planted foreign-partition file (the pre-#20 row-local layout).
	if _, err := svc.Import(ctx, ImportOptions{
		Path: path, FeeType: FeeTypeElectricity, Year: 2026, Month: 5,
		AllowPeriodDuplicate: true,
	}); err != nil {
		t.Fatalf("override import: %v", err)
	}
	analysis := filepath.Join(dir, "analysis")
	batches, _ := svc.List(ctx, FeeStatusCompleted)
	// plant: copy batch[0]'s file into a wrong partition (pre-#20 shape)
	live, err := BatchParquetFiles(analysis, batches[0].ID)
	if err != nil || len(live) != 1 {
		t.Fatalf("live files = %v (err %v)", live, err)
	}
	plant := plantForeignPartition(t, analysis, batches[0].ID, 2026, 9, live[0])
	_ = plant

	rep2, err := svc.VerifyBatches(ctx)
	if err != nil {
		t.Fatalf("verify-batches (dirty): %v", err)
	}
	if rep2.BatchesChecked != 2 {
		t.Fatalf("checked = %d, want 2", rep2.BatchesChecked)
	}
	var dup, part int
	for _, p := range rep2.Problems {
		switch p.Kind {
		case "duplicate-sha-period":
			dup++
		case "analysis-partition":
			part++
		}
	}
	if dup != 2 {
		t.Errorf("duplicate-sha-period problems = %d, want 2 (both family members named)", dup)
	}
	if part != 1 {
		t.Errorf("analysis-partition problems = %d, want 1 (the planted file)", part)
	}
}

// plantForeignPartition copies a batch file into a WRONG partition dir and
// returns the planted path (simulating pre-#20 row-local layout residue).
func plantForeignPartition(t *testing.T, analysis, batchID string, year, month int, src string) string {
	t.Helper()
	dst := filepath.Join(analysis, "fees", "fee_type=electricity",
		"year=2026", "month=09", "batch-"+batchID+".parquet")
	_ = year
	_ = month
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := readFileAll(t, src)
	writeFileAll(t, dst, body)
	return dst
}

// ---- #24: the two duplicate paths return the COMPLETE field set ----

func TestDuplicateAnswerFieldsComplete(t *testing.T) {
	svc, dir := newFeeService(t)
	ctx := context.Background()
	path := plainCSVDup(t, dir, "dup-fields.csv")

	first, err := svc.Import(ctx, ImportOptions{
		Path: path, FeeType: FeeTypeElectricity, Year: 2026, Month: 1,
	})
	if err != nil {
		t.Fatalf("first import: %v", err)
	}

	// Pre-flight duplicate hit.
	second, err := svc.Import(ctx, ImportOptions{
		Path: path, FeeType: FeeTypeElectricity, Year: 2026, Month: 1,
	})
	if err == nil {
		t.Fatal("duplicate import must answer ErrDuplicateImport")
	}
	if second.SkippedDuplicate != true || second.ExistingBatch != first.BatchID {
		t.Fatalf("summary = %+v, want skippedDuplicate + existingBatch", second)
	}
	// #24 acceptance: the field SET matches the normal path's — no more
	// hand-picked subsets. periodSource equals the DB row's value (non-empty
	// for #15-era batches), workbookName present, files[] recovered.
	raw, err := getBatchRow(t, svc, first.BatchID)
	if err != nil {
		t.Fatalf("get batch row: %v", err)
	}
	if second.WorkbookName == "" {
		t.Errorf("workbookName empty — the pre-flight branch used to drop it")
	}
	if second.PeriodSource != raw.PeriodSource || second.PeriodSource == "" {
		t.Errorf("periodSource = %q, want the DB row's non-empty %q", second.PeriodSource, raw.PeriodSource)
	}
	if second.PeriodBasis != raw.PeriodBasis || second.PeriodBasis == "" {
		t.Errorf("periodBasis = %q, want the DB row's non-empty %q", second.PeriodBasis, raw.PeriodBasis)
	}
	if len(second.Files) != 1 {
		t.Errorf("files = %v, want the recovered analysis manifest", second.Files)
	}
	if second.FeeType != string(FeeTypeElectricity) || second.FeeYear != 2026 || second.FeeMonth != 1 {
		t.Errorf("identity fields = %s/%d/%d, want electricity/2026/1", second.FeeType, second.FeeYear, second.FeeMonth)
	}

	// Race-path duplicate: another DB-level insert of the same key between
	// pre-flight and commit cannot be staged from the outside; the SAME
	// builder feeds both branches (summaryFromBatch), so the pre-flight
	// pins above carry the race branch by construction. The builder itself
	// is verified once more against the raw batch row (already read above):
	if second.BatchID != raw.ID || second.RowsImported != raw.RowsImported {
		t.Errorf("duplicate answer %+v vs batch row %s — one source of truth", second, raw.ID)
	}
}

// ---- helpers that keep this file self-contained ----

func readFileAll(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return b
}

func writeFileAll(t *testing.T, p string, b []byte) {
	t.Helper()
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

// getBatchRow reads one batch through the service's list (test helper).
func getBatchRow(t *testing.T, svc *Service, id string) (domain.FeeBatch, error) {
	t.Helper()
	batches, err := svc.List(context.Background(), FeeStatusCompleted)
	if err != nil {
		return domain.FeeBatch{}, err
	}
	for _, b := range batches {
		if b.ID == id {
			return b, nil
		}
	}
	return domain.FeeBatch{}, os.ErrNotExist
}

// assertNoDuplicateWarnings is #25's general guard: no byte-identical
// strings inside one warnings list — a duplicate entry means two sources
// appended unconditionally again.
func assertNoDuplicateWarnings(t *testing.T, ws []string) {
	t.Helper()
	seen := make(map[string]struct{}, len(ws))
	for _, w := range ws {
		if _, dup := seen[w]; dup {
			t.Fatalf("warnings carry a byte-identical duplicate: %q (all: %q)", w, ws)
		}
		seen[w] = struct{}{}
	}
}

// TestDuplicateWarningsNoDupes (#25 方案 B): a duplicate import writes
// NOTHING, so its answer carries only the library-recorded trail — the
// current-run notices (the #6 writable-source advice) used to be derived
// inside summaryFromBatch AND prepended again by the Import merge,
// landing the same string twice, byte for byte (1 warning on the fresh
// path, 2 identical on the duplicate path). Now: fresh import warns about
// the writable source; the duplicate answer's warnings stay empty (the
// durable record rides periodBasis/periodSource); the multi-sheet
// channel's skipped sheets are equally clean.
func TestDuplicateWarningsNoDupes(t *testing.T) {
	svc, dir := newFeeService(t)
	ctx := context.Background()
	path := plainCSVDup(t, dir, "dup-warnings.csv")

	first, err := svc.Import(ctx, ImportOptions{
		Path: path, FeeType: FeeTypeElectricity, Year: 2026, Month: 2,
	})
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	assertNoDuplicateWarnings(t, first.Warnings)
	if len(first.Warnings) == 0 {
		t.Fatal("fresh import of a writable (0644) source must carry the #6 advice — fixture drifted?")
	}

	second, err := svc.Import(ctx, ImportOptions{
		Path: path, FeeType: FeeTypeElectricity, Year: 2026, Month: 2,
	})
	if err == nil {
		t.Fatal("duplicate import must answer ErrDuplicateImport")
	}
	if second.SkippedDuplicate != true {
		t.Fatalf("summary = %+v, want skippedDuplicate", second)
	}
	if len(second.Warnings) != 0 {
		t.Fatalf("duplicate answer warnings = %q, want empty (#25 方案 B: only the library record — the current run wrote nothing)", second.Warnings)
	}

	// The multi-sheet channel's skipped sheets: the pre-#25 merge
	// prepended the workbook-level advice onto every sheet result,
	// duplicated for the skipped ones.
	xlsxPath := threeTypeLedger(t, t.TempDir())
	xopts := SheetsImportOptions{Path: xlsxPath, OriginalName: "2024年05月费用.xlsx", Year: 2024, Month: 5}
	if _, err := svc.ImportSheets(ctx, xopts); err != nil {
		t.Fatalf("first ImportSheets: %v", err)
	}
	sheetsSecond, err := svc.ImportSheets(ctx, xopts)
	if err != nil {
		t.Fatalf("re-import must be informational, got %v", err)
	}
	skipped := 0
	for _, b := range sheetsSecond.Batches {
		assertNoDuplicateWarnings(t, b.Warnings)
		if b.SkippedDuplicate {
			skipped++
			if len(b.Warnings) != 0 {
				t.Fatalf("skipped sheet %s warnings = %q, want empty (library record only)", b.Sheet, b.Warnings)
			}
		}
	}
	if skipped == 0 {
		t.Fatal("re-import must skip the already-imported sheets — fixture drifted?")
	}
}
