package fees

import (
	"path"

	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/repo"
	"sort"
)

// Service orchestrates the fee import pipeline (#61): read → recognize →
// snapshot → normalize → partitioned Parquet → batch state, plus the
// rollback and GC companions. The data dir layout:
//
//	<dataDir>/fee-imports/…                the immutable snapshot layer
//	<dataDir>/analysis/fees/fee_type=…/…   the Parquet analysis table
//
// The service owns NO long-lived handles (each call opens what it needs);
// the SQLite writes run inside one DB.Do transaction each, mirroring the
// resource importer's discipline.
type Service struct {
	db  *repo.DB
	dir string // the data dir (snapshot + analysis roots live under it)

	// Now is the clock hook; tests may replace it for deterministic
	// timestamps. New installs time.Now; a nil hook falls back.
	Now func() time.Time

	// testMoveFault, when non-nil (set only via export_test.go — the
	// importer's afterSheet seam precedent), is consulted before every
	// file move of the rollback dance; a non-nil answer fails that exact
	// step. #107's regression tests inject IO faults deterministically
	// this way: a chmod'd read-only dir cannot place a fault BETWEEN two
	// renames of the same call (and would not bite when tests run as
	// root).
	testMoveFault func(op, path string) error
}

// NewService binds a service to db and the data dir.
func NewService(db *repo.DB, dataDir string) *Service {
	return &Service{db: db, dir: dataDir, Now: time.Now}
}

func (s *Service) clock() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) analysisDir() string {
	return filepath.Join(s.dir, "analysis")
}

// DataYears answers the years the analysis face actually carries rows for,
// ascending — the read-open face behind the year pickers' data-first
// default and the overview's honest year-empty banner (the old calendar
// default opened a 2026 dashboard against 2024 data: blank cards, a
// misleading month-validation error from the risk map's month=0, and the
// operator left to guess which year to type).
//
// The discovery is PATH-LEVEL, deliberately: the same glob the engines
// read with (feeTableGlob), parsed for its year= partition component —
// so it answers in EVERY build (directory presence is metadata, not an
// engine query — no 501), and it can never drift from what read_parquet
// would actually scan. An empty-after-rollback partition (directory
// alive, files gone) is honestly ABSENT — glob matches files, not
// directories. A glob error surfaces to the caller instead of reading
// as empty (#105's lesson: a "[" in the data dir must never be masked
// into "no data"). Stray paths that don't carry the writer's five-part
// partition shape are skipped, never fatal to the picker.
func (s *Service) DataYears() ([]int, error) {
	matches, err := filepath.Glob(feeTableGlob(s.analysisDir()))
	if err != nil {
		return nil, fmt.Errorf("fees: 数据年份发现: %w", err)
	}
	root := s.analysisDir()
	seen := make(map[int]bool, len(matches))
	for _, m := range matches {
		rel, rerr := filepath.Rel(root, m)
		if rerr != nil {
			continue
		}
		// fees/fee_type=X/year=Y/month=M/file.parquet — the year is the
		// third component; ToSlash first (#32's lesson: Linux never sees
		// the Windows separator Glob answers with there).
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) != 5 || !strings.HasPrefix(parts[2], "year=") {
			continue
		}
		y, perr := strconv.Atoi(strings.TrimPrefix(parts[2], "year="))
		if perr != nil {
			continue
		}
		seen[y] = true
	}
	// Non-nil empty slice: the fresh-install answer is "years: []", never
	// null — absence is a statement, and the JSON face says it as [].
	years := make([]int, 0, len(seen))
	for y := range seen {
		years = append(years, y)
	}
	sort.Ints(years)
	return years, nil
}

// ImportOptions is one import request: the file, the declared fee type
// and period (the operator's statement), and the confirmed core-column
// mapping (target field → source header). When Mapping is empty the
// recognizer's suggestion is used directly (the unattended path for
// well-formed files); a suggestion missing siteCode or amount refuses —
// the human mapping channel is the fallback, never a guess.
type ImportOptions struct {
	Path string
	// TrustDeclaredPeriod (#15): when true, a content-vs-declaration period
	// DISAGREEMENT does not refuse the import — the override is recorded
	// in the batch (period_source stays unverified, the message carries
	// both values). The confirmation channel of the B/A档 mismatch in
	// unattended CLI form; false (default) keeps the refusal.
	TrustDeclaredPeriod bool
	// StrictRows (#8): when true, an optional ledger column cell that is
	// PRESENT but unparseable (a hand-mangled number) fails the row with its
	// reason instead of silently degrading to nil. Default false keeps the
	// pre-#8 behavior byte-for-byte — the lenient import and the on-demand
	// scenario-9 rules remain the warning surface.
	StrictRows bool
	// AllowPeriodDuplicate (#20): when true, importing a file whose SHA256
	// already landed as a COMPLETED batch under a DIFFERENT period does not
	// refuse — the override is recorded on the batch (period_basis) and the
	// import warnings. Default false refuses with both periods + the batch
	// id: the same bytes under two periods is silent double-counting by
	// construction (the idempotency key includes the period, so the
	// per-key duplicate check cannot see it).
	AllowPeriodDuplicate bool
	// SourcePath is the ABSOLUTE path the import actually reads (issue #6):
	// recorded on the batch so 'fees verify-sources' can re-hash the file
	// later and notice tampering. Set by Service.Import from Path ('' =
	// not recorded; the HTTP channel passes the staged uploads path).
	SourcePath string
	// OriginalName is the USER's file name for the batch's display identity
	// and the evidence anchors (WorkbookName / sourceFile). The HTTP channel
	// stages uploads content-addressed (#88: <sha256>_<name>), so deriving
	// the name from the staged path would pollute both; the caller passes
	// the multipart header's name here. Empty = derive from Path (the CLI's
	// channel, where the path IS the user's file).
	OriginalName string
	FeeType      FeeType
	Year         int
	Month        int
	Mapping      map[string]string // optional; nil = auto
}

// ImportSummary is the outcome: batch identity, counts, the per-partition
// file manifest and the row-level import errors (bounded — the full error
// detail stays queryable via the failed-row evidence in the snapshot).
type ImportSummary struct {
	BatchID      string `json:"batchId"`
	Status       string `json:"status"`
	WorkbookName string `json:"workbookName"`
	SHA256       string `json:"sha256"`
	FeeType      string `json:"feeType"`
	FeeYear      int    `json:"feeYear"`
	FeeMonth     int    `json:"feeMonth"`
	// PeriodSource/PeriodBasis (#15): how this batch's period was
	// established (auto/unverified/manual) and the 识别依据 — the
	// operator-facing copy of the batch row's provenance fields.
	PeriodSource     string   `json:"periodSource"`
	PeriodBasis      string   `json:"periodBasis,omitempty"`
	RowsTotal        int      `json:"rowsTotal"`
	RowsImported     int      `json:"rowsImported"`
	RowsFailed       int      `json:"rowsFailed"`
	Files            []string `json:"files"`
	Errors           []string `json:"errors,omitempty"`
	SkippedDuplicate bool     `json:"skippedDuplicate"`
	ExistingBatch    string   `json:"existingBatch,omitempty"`
	// Warnings are non-blocking integrity notices of issue #6 (a writable
	// source file at import time — the read-only-directory advice rides
	// here; the import itself never refuses on them).
	Warnings []string `json:"warnings,omitempty"`
	// AmountColumn (#29): the header the import actually read the amount
	// from. On sheets carrying several same-family amount variants (the
	// tower settlement's 产品服务费合计 ×4) the operator must see WHICH 口径
	// landed without opening the mapping JSON. Empty when a legacy path
	// resolved no amount (never happens on fresh imports — amount mapping
	// is mandatory).
	AmountColumn string `json:"amountColumn,omitempty"`
}

// Import runs the full pipeline for one file the way it always worked
// (#125 keeps this channel byte-compatible): the operator DECLARES the
// fee type, the first sheet (xlsx) or the file itself (CSV) is the data.
// New since #125: the declared type is cross-checked against the sheet's
// own evidence (sheet name + headers) — a declared tower_lease on a sheet
// named 电费 refuses instead of polluting the type's ledger; and a file
// already imported in multi-sheet mode refuses (the two modes have
// different idempotency keys by construction — importing both would
// double-count the data silently, the exact pollution #125 closes).
//
// The crash-safety design lives in importSheet (both channels share it):
//
//  1. hash + read the file (nothing written yet);
//  2. pre-flight idempotency check (completed batch with the same key →
//     ErrDuplicateImport BEFORE anything lands);
//  3. snapshot (immutable, content-addressed — idempotent by naming);
//  4. normalize every row (failures collected and counted, never fatal:
//     a bad row skips, the snapshot keeps the evidence);
//  5. write the Parquet partitions STAGED, then renamed into place;
//  6. the batch row (created as failed in step 3's transaction) flips to
//     completed — THE commit point. A crash anywhere before step 6 leaves
//     at worst orphan Parquet files (GC's job) and a failed batch row
//     that never blocks a retry.
func (s *Service) Import(ctx context.Context, opts ImportOptions) (ImportSummary, error) {
	if _, err := ParseFeeType(string(opts.FeeType)); err != nil {
		return ImportSummary{}, err
	}
	if opts.Path == "" {
		return ImportSummary{}, fmt.Errorf("fees: 缺少导入文件路径")
	}
	if !validPeriod(opts.Year, opts.Month) {
		return ImportSummary{}, fmt.Errorf("fees: 账期 %04d-%02d 不在合法范围", opts.Year, opts.Month)
	}

	// 1. hash + read.
	sha, err := HashFile(opts.Path)
	if err != nil {
		return ImportSummary{}, err
	}
	// issue #6: record the absolute source path the import reads (survives
	// later CWD changes; '' rows are skipped by verify-sources, not guessed).
	if opts.SourcePath == "" {
		if abs, aerr := filepath.Abs(opts.Path); aerr == nil {
			opts.SourcePath = abs
		}
	}
	name := opts.OriginalName
	if name == "" {
		name = filepath.Base(opts.Path)
	}
	if _, err := SnapshotExt(name); err != nil {
		return ImportSummary{}, fmt.Errorf("%w: %v", ErrImportRefused, err)
	}
	warnings := sourceWritableWarnings(opts.Path)
	sheet, err := ReadFeeFileFirst(opts.Path)
	if err != nil {
		// #104: a bad file shape is the operator's to fix — 422 class, not a
		// server fault. The reader's texts are the CLI contract already.
		return ImportSummary{}, fmt.Errorf("%w: %v", ErrImportRefused, err)
	}

	// #125 plan C: the declared type must not contradict the sheet's own
	// evidence (a sheet named 电费 with an 电费(元) amount column declared
	// as tower_lease used to import the electricity rows INTO the tower
	// ledger — same file, re-typed, double-counted).
	if err := crossCheckDeclaredType(opts.FeeType, sheet); err != nil {
		return ImportSummary{}, err
	}

	// #125 mode guard: this IS the single-sheet channel, so the mode
	// being entered is single — the guard then refuses when the same
	// file has MULTI-sheet batches (sheet names in the key, invisible
	// to this channel's idempotency check).
	if err := s.refuseModeConflict(ctx, sha, name, singleSheetBatch); err != nil {
		return ImportSummary{}, err
	}

	// #20 cross-period guard: same bytes under another completed period is
	// double-counting the per-key check cannot see. The override (when
	// given) rides the warnings + the batch's period_basis.
	periodDupNote, err := s.refuseCrossPeriodDuplicate(ctx, sha, name, opts.Year, opts.Month, opts.AllowPeriodDuplicate)
	if err != nil {
		return ImportSummary{}, err
	}
	if periodDupNote != "" {
		warnings = append(warnings, periodDupNote)
	}

	// The single-sheet channel keeps the pre-v34 batch coordinate (sheet
	// '') so its idempotency keys survive the migration byte-for-byte;
	// the runtime cross-check above still USES the sheet's name.
	sum, err := s.importSheet(ctx, opts, sheet, sha, name, "")
	// #12 acceptance 4 — the data-volume guard rides the warnings
	// channel on the SUCCESS path only (a failed import says its own
	// thing): this month vs last month's completed rows, <20% = the
	// 骤降线. 只提示不阻断 — the operator keeps full agency. (The
	// multi-sheet channel is out of scope phase 1: its per-sheet rows
	// are not the type universe; an honest per-type aggregate lands
	// with its own pass later.)
	if err == nil {
		warnings = append(warnings, s.importRowAnomalyWarn(ctx, opts.FeeType, opts.Year, opts.Month, sum.RowsImported)...)
	}
	if sum.SkippedDuplicate {
		// #25: a duplicate answer carries ONLY the library record — the
		// current run wrote nothing, so the current-run warnings (source
		// writability, 骤降线) do not apply; periodBasis/periodSource fields
		// already carry the durable trail. Prepending the outer copy here is
		// what used to duplicate the source-writable advice byte-for-byte.
		return sum, err
	}
	sum.Warnings = append(warnings, sum.Warnings...)
	return sum, err
}

// importSheet runs steps 2-7 for ONE sheet's data. sheetKey is the
// idempotency/evidence sheet coordinate: "" for the single-sheet channel
// (CSV and pre-#125 compatibility) and the sheet's own name for the
// multi-sheet channel; the evidence's source_file gains a "#<sheet>"
// suffix exactly when sheetKey is non-empty (rows of different sheets
// share sheet-local row numbers — the anchor must name its sheet).
func (s *Service) importSheet(ctx context.Context, opts ImportOptions, sheet SheetData,
	sha, name, sheetKey string) (ImportSummary, error) {

	rows := sheet.Rows

	// 2. mapping resolution (explicit mapping wins; recognizer fills the
	// gaps; siteCode + amount are mandatory).
	mapping := opts.Mapping
	if mapping == nil {
		_, suggested := Recognize(sheet.Headers)
		mapping = suggested
	}
	// #29: the type-scoped amount table outranks the GENERIC recognizer —
	// but only when the operator did NOT hand an explicit mapping (an
	// explicit --map is the 口径 declaration that always wins). The tower
	// settlement detail carries several 产品服务费合计 variants plus
	// 罚责赠费合计; the generic contains-fallback used to grab the FIRST
	// header containing 费用 — on the real 157-column sheet that was an
	// all-zero variant column and 454 rows landed with totalCents 0 and
	// zero warnings. The scoped table matches the full normalized header
	// (exact, never contains), in 口径 priority: 出账费用（不含税） first.
	amountVariants := 0
	if opts.Mapping == nil {
		if h, ok, variants := RecognizeAmountForType(opts.FeeType, sheet.Headers); ok {
			mapping["amount"] = h
			amountVariants = variants
		}
	}
	// #5: the optional ledger columns join the same mapping (explicit wins;
	// the recognizer fills; nil rides). They resolve by header index next.
	for field, header := range RecognizeLedgerFields(sheet.Headers) {
		if mapping[field] == "" {
			mapping[field] = header
		}
	}
	if mapping["siteCode"] == "" || mapping["amount"] == "" {
		return ImportSummary{}, refusef("缺少必需列映射（siteCode / amount）——请提供映射 JSON 或修正表头；识别到的表头：%v", sheet.Headers)
	}

	// #15 three-tier period check: the declared period must not
	// contradict the sheet's own content evidence — a dedicated period
	// field (A档) mismatch REFUSES; an indirect clue (B档) refuses on
	// month disagreement unless the operator trusts the declaration;
	// no evidence (C档) proceeds marked manual. The verdict rides the
	// batch row (period_source + period_basis) — 跳过识别也留痕：the
	// override path records the disagreement verbatim.
	detection := DetectPeriod(sheet.Headers, rows)
	check := CheckPeriod(detection, opts.Year, opts.Month, opts.TrustDeclaredPeriod)
	if check.Refuse {
		return ImportSummary{}, refusef("%s", check.Message)
	}
	// The 综维绩效单 ruling (#15): its 日期 column is the FILING time
	// (measured: 7 sheets' dates span 2026.6–10 while every period is
	// 202609) — never period evidence. The date mapping is dropped so
	// the declared period governs every row; the drop is recorded in
	// the basis (the mapping the batch actually used).
	if detection.Kind == TablePerfSheet && mapping["date"] != "" {
		dropped := mapping["date"]
		delete(mapping, "date")
		check.Message += fmt.Sprintf("；表型判定为综维绩效单——「%s」列（填表时间）不参与账期解析，账期以申报为准", dropped)
	}

	canonical := FeeMapping{FeeType: opts.FeeType, Columns: mapping}.CanonicalMapping()

	// 3. pre-flight idempotency.
	existing, preflightErr := s.db.FeeBatches.CompletedByIdempotencyKey(ctx, sha, opts.FeeType, opts.Year, opts.Month, canonical, sheetKey)
	if preflightErr != nil && !errors.Is(preflightErr, domain.ErrNotFound) {
		// #104: a real DB failure used to read as "no duplicate" and the
		// import proceeded — the idempotency guarantee silently void.
		return ImportSummary{}, fmt.Errorf("fees: 幂等性预检: %w", preflightErr)
	}
	if preflightErr == nil {
		// #24: the duplicate answer carries the COMPLETE field set —
		// the batch row is the source of truth, not a hand-picked subset.
		return s.summaryFromBatch(ctx, existing, opts), &ErrDuplicateImport{BatchID: existing.ID}
	}

	// 4. snapshot.
	batchID := domain.NewID("fee")
	snapRel, err := WriteSnapshot(s.dir, opts.Path, opts.FeeType, opts.Year, opts.Month, sha, name)
	if err != nil {
		return ImportSummary{}, err
	}
	// batch row: created as failed (the retry-open default); flips to
	// completed at the commit point. A crash between here and the flip
	// leaves a failed row + (at worst) GC-able orphans.
	basis := check.Message
	// #20 audit trail: the cross-period override (when given) appends to
	// the batch's own basis — the durable record of what the refusal would
	// have carried. period_source stays what the three-tier check decided
	// (an override keeps it unverified; the trail is the point).
	if opts.AllowPeriodDuplicate {
		if note, gerr := s.refuseCrossPeriodDuplicate(ctx, sha, name, opts.Year, opts.Month, true); gerr == nil && note != "" {
			basis += "；" + note
		}
	}
	if err := s.db.Do(ctx, func(tx *repo.Tx) error {
		return tx.FeeBatches.Create(ctx, FeeBatch{
			ID: batchID, WorkbookName: name, SHA256: sha, Sheet: sheetKey, FeeType: opts.FeeType,
			FeeYear: opts.Year, FeeMonth: opts.Month, SnapshotPath: snapRel, Mapping: canonical,
			SourcePath:   opts.SourcePath,
			PeriodSource: check.Source, PeriodBasis: basis,
			Status: FeeStatusFailed, CreatedAt: s.clock(),
		})
	}); err != nil {
		return ImportSummary{}, fmt.Errorf("fees: 记录导入批次: %w", err)
	}

	// periodBasisNote carries #20's row/declaration disagreement summary
	// (filled after normalization; appended to the batch basis + warnings).
	var periodBasisNote string
	// 5. normalize.
	siteIdx := headerIndex(sheet.Headers, mapping["siteCode"])
	amountIdx := headerIndex(sheet.Headers, mapping["amount"])
	periodIdx := headerIndex(sheet.Headers, mapping["period"])
	dateIdx := headerIndex(sheet.Headers, mapping["date"])
	// #5: resolve the optional ledger indices once per file (absent = -1,
	// the column never read).
	ledgerIdx := make(map[string]int, len(LedgerFieldKeys()))
	for _, field := range LedgerFieldKeys() {
		ledgerIdx[field] = headerIndex(sheet.Headers, mapping[field])
	}
	if siteIdx < 0 || amountIdx < 0 {
		errMsg := fmt.Sprintf("映射列不在表头中：siteCode=%q amount=%q（表头：%v）", mapping["siteCode"], mapping["amount"], sheet.Headers)
		_ = s.db.FeeBatches.MarkFailed(ctx, batchID, len(rows), 0, errMsg)
		return ImportSummary{}, refusef("%s", errMsg)
	}

	sourceFile := SnapshotSourceFile(name, sha)
	if sheetKey != "" {
		// Multi-sheet evidence: rows of different sheets share sheet-local
		// numbers — the anchor names its sheet or it points at the wrong
		// ledger page.
		sourceFile += "#" + sheetKey
	}
	normalized := make([]NormalizedRow, 0, len(rows))
	var importErrs []*ImportError
	for _, row := range rows {
		nr, ierr := s.normalizeRow(row, siteIdx, amountIdx, periodIdx, dateIdx, ledgerIdx, opts, sourceFile, batchID)
		if ierr != nil {
			importErrs = append(importErrs, ierr)
			continue
		}
		normalized = append(normalized, nr)
	}
	if len(normalized) == 0 && len(rows) > 0 {
		// every row failed — record and report, but the batch row stays
		// failed with the errors (the operator fixes the file and retries)
		errs := renderImportErrors(importErrs)
		_ = s.db.FeeBatches.MarkFailed(ctx, batchID, len(rows), len(rows), firstOr(errs, "全部行规范化失败"))
		return ImportSummary{
			BatchID: batchID, Status: "failed", WorkbookName: name, SHA256: sha,
			FeeType: string(opts.FeeType), FeeYear: opts.Year, FeeMonth: opts.Month,
			RowsTotal: len(rows), RowsImported: 0, RowsFailed: len(rows), Errors: errs,
		}, refusef("%d 行全部规范化失败（首个：%s）", len(rows), importErrs[0].Error())
	}

	// 6. Parquet partitions (staged → atomic rename). #20: the partition is
	// the DECLARED period — the batch row, the snapshot dir and the analysis
	// partition must tell one period story. A row-local period that
	// disagrees (reachable via --period-trust-declared or an unrecognized
	// mapped period column) is counted and warned, never silently rehomed.
	var periodDisagree int
	for _, nr := range normalized {
		if nr.Year != opts.Year || nr.Month != opts.Month {
			periodDisagree++
		}
	}
	if periodDisagree > 0 {
		periodBasisNote = fmt.Sprintf(
			"%d 行的行内账期证据与申报账期 %04d-%02d 不一致——已按申报账期入分区（分区跟随申报是 #20 起的一致性铁律）",
			periodDisagree, opts.Year, opts.Month)
	}
	files, err := writeParquetPartitions(s.analysisDir(), opts.FeeType, batchID, opts.Year, opts.Month, normalized)
	if err != nil {
		_ = s.db.FeeBatches.MarkFailed(ctx, batchID, len(rows), 0, "Parquet 写入失败: "+err.Error())
		return ImportSummary{}, fmt.Errorf("fees: 批次 %s: %w", batchID, err)
	}

	// 7. commit point.
	if periodBasisNote != "" {
		// #20: the disagreement is durable on the batch row, not just the
		// response — appended before the completed flip, same db handle.
		if berr := s.db.Do(ctx, func(tx *repo.Tx) error {
			return tx.FeeBatches.AppendPeriodBasis(ctx, batchID, "；"+periodBasisNote)
		}); berr != nil {
			return ImportSummary{}, fmt.Errorf("fees: 批次 %s 账期留痕: %w", batchID, berr)
		}
	}
	now := s.clock()
	var errs []string
	if importErrs != nil {
		errs = renderImportErrors(importErrs)
	}
	if err := s.db.Do(ctx, func(tx *repo.Tx) error {
		return tx.FeeBatches.MarkCompleted(ctx, batchID, len(rows), len(normalized), len(importErrs), now)
	}); err != nil {
		// the idempotency race lost (another import of the same key
		// completed between our pre-flight and the commit): clean our
		// files, report the duplicate
		if err == repo.ErrFeeIdempotentDuplicate {
			_ = RemoveParquetFiles(filePaths(s.analysisDir(), files))
			_ = s.db.FeeBatches.MarkFailed(ctx, batchID, len(rows), 0, "幂等竞态：同键批次已先完成")
			existing, gerr := s.db.FeeBatches.CompletedByIdempotencyKey(ctx, sha, opts.FeeType, opts.Year, opts.Month, canonical, sheetKey)
			if gerr == nil {
				// #24: same complete-field discipline as the pre-flight branch —
				// one builder, no drift between the two duplicate paths.
				return s.summaryFromBatch(ctx, existing, opts), &ErrDuplicateImport{BatchID: existing.ID}
			}
		}
		return ImportSummary{}, fmt.Errorf("fees: 提交批次 %s: %w", batchID, err)
	}

	relFiles := make([]string, 0, len(files))
	for _, f := range files {
		relFiles = append(relFiles, f.relPath)
	}
	// #29 stop-the-bleed: a fresh batch whose EVERY imported row carries
	// amount 0 is almost certainly a wrong-column import (the 157-column
	// settlement shape — the recognizer grabbed an all-zero variant).
	// The batch still completes (rows, evidence and rollback all stand);
	// the warning names the shape so the 零金额 never masquerades as
	// "导好了". A legitimate all-zero month is possible only in the sense
	// that every row's own number is 0 — the text tells the operator to
	// check the MAPPING, never claims the data is wrong outright.
	var zeroAmountNote string
	if len(normalized) > 0 {
		allZero := true
		for _, nr := range normalized {
			if nr.AmountCents != 0 {
				allZero = false
				break
			}
		}
		if allZero {
			zeroAmountNote = fmt.Sprintf(
				"已导入 %d 行金额全部为 0——极可能是金额列未正确识别（该表共 %d 列）。这批数据无法参与任何金额分析；请核对金额列映射或用映射 JSON 指定金额列，必要时回滚本批次后重新导入",
				len(normalized), len(sheet.Headers))
		}
	}
	// #29 acceptance 5 — 多金额列口径声明: when the type-scoped table picked
	// ONE column out of a non-trivial same-stem family, the import says
	// which 口径 landed and how many siblings stayed behind. Only the
	// scoped-hit path counts variants (the generic fallback's choice is
	// not a 口径 declaration).
	var amountVariantNote string
	if amountVariants > 1 {
		amountVariantNote = fmt.Sprintf(
			"金额列按塔类结算口径取「%s」；表内共有 %d 个同族金额列（历史调增/待退回等变体）——如需其他口径请用映射 JSON 显式指定",
			mapping["amount"], amountVariants)
	}
	basisOut := check.Message
	if periodBasisNote != "" {
		basisOut += "；" + periodBasisNote
	}
	sumWarnings := warningsFor(periodBasisNote)
	if zeroAmountNote != "" {
		sumWarnings = append(sumWarnings, zeroAmountNote)
	}
	if amountVariantNote != "" {
		sumWarnings = append(sumWarnings, amountVariantNote)
	}
	return ImportSummary{
		BatchID: batchID, Status: "completed", WorkbookName: name, SHA256: sha,
		FeeType: string(opts.FeeType), FeeYear: opts.Year, FeeMonth: opts.Month,
		PeriodSource: check.Source, PeriodBasis: basisOut,
		RowsTotal: len(rows), RowsImported: len(normalized), RowsFailed: len(importErrs),
		Files: relFiles, Errors: errs,
		Warnings:     sumWarnings,
		AmountColumn: mapping["amount"],
	}, nil
}

// batchMode names the import channel for the mode-conflict guard: the two
// channels give the same file DIFFERENT idempotency keys by construction
// (sheet ” vs the sheet names), so neither can see the other through the
// per-key duplicate check — the guard looks at the file hash instead.
type batchMode string

const (
	singleSheetBatch batchMode = "single"
	multiSheetBatch  batchMode = "multi"
)

// SheetsImportOptions is one multi-sheet import request (#125): the file
// and the declared period. No fee type and no explicit mapping — each
// sheet's type comes from its own evidence (sheet name + header markers,
// RecognizeSheetType) and each sheet's mapping from its own header row
// (the columns differ per sheet; one operator-pinned mapping for the whole
// book would be wrong by construction).
type SheetsImportOptions struct {
	Path         string
	OriginalName string
	Year         int
	Month        int
	// SourcePath mirrors ImportOptions.SourcePath (issue #6).
	SourcePath string
	// StrictRows mirrors ImportOptions.StrictRows (#8).
	StrictRows bool
	// TrustDeclaredPeriod mirrors ImportOptions.TrustDeclaredPeriod (#15)
	// — the per-sheet period-check override, workbook-wide.
	TrustDeclaredPeriod bool
	// AllowPeriodDuplicate mirrors ImportOptions.AllowPeriodDuplicate
	// (#20) — workbook-wide, one declaration for every sheet batch.
	AllowPeriodDuplicate bool
}

// SheetBatchResult is one sheet's import outcome inside the multi-sheet
// envelope: the batch identity (sheet + feeType + batchId), the row
// accounting, and the idempotency skip marker — the shape the issue's
// acceptance pins (three sheets → three batches, each type correct).
type SheetBatchResult struct {
	Sheet            string   `json:"sheet"`
	FeeType          string   `json:"feeType"`
	BatchID          string   `json:"batchId"`
	Status           string   `json:"status"`
	PeriodSource     string   `json:"periodSource"`
	PeriodBasis      string   `json:"periodBasis,omitempty"`
	RowsTotal        int      `json:"rowsTotal"`
	RowsImported     int      `json:"rowsImported"`
	RowsFailed       int      `json:"rowsFailed"`
	Files            []string `json:"files,omitempty"`
	Errors           []string `json:"errors,omitempty"`
	SkippedDuplicate bool     `json:"skippedDuplicate"`
	ExistingBatch    string   `json:"existingBatch,omitempty"`
	Warnings         []string `json:"warnings,omitempty"`
}

// SheetsImportSummary is the multi-sheet envelope: the workbook identity,
// the per-sheet batch results in workbook order, and the cosmetic sheets
// that were skipped (named, never silently dropped — the full accounting).
type SheetsImportSummary struct {
	WorkbookName  string             `json:"workbookName"`
	SHA256        string             `json:"sha256"`
	Year          int                `json:"year"`
	Month         int                `json:"month"`
	Status        string             `json:"status"`
	Batches       []SheetBatchResult `json:"batches"`
	SkippedSheets []string           `json:"skippedSheets,omitempty"`
}

// ImportSheets runs the multi-sheet channel (#125): one workbook, one fee
// type per sheet — the real monthly ledger shape. Every sheet with data is
// classified from its own evidence and imported as its OWN batch (own
// feeType, own mapping, own evidence chain with "#<sheet>" anchors, own
// idempotency key). Nothing is silently dropped: cosmetic sheets are
// reported by name, and a sheet that cannot be classified REFUSES THE
// WHOLE IMPORT before any sheet lands (a partial multi-sheet import with
// a "completed" face would be the P0's silent-drop in a new costume).
//
// Refusals (ErrImportRefused, all listing every offending sheet):
//   - an unclassifiable sheet (no unique type evidence);
//   - a sheet whose mandatory columns (siteCode/amount) cannot be mapped;
//   - a CSV in multi-sheet mode (a CSV has one pseudo-sheet and no sheet
//     names — the type evidence the channel exists to read is absent;
//     the single-sheet channel with its declared type is the honest path);
//   - a file already imported through the single-sheet channel (mode
//     conflict — the double-count guard).
//
// Per-sheet outcomes are honest even mid-failure: an IO/server fault on
// sheet N leaves sheets 1..N-1 completed (their batches stand, each
// queryable and rollback-able), the faulting sheet's summary entry says
// failed, and the error comes back — the caller sees exactly what landed.
func (s *Service) ImportSheets(ctx context.Context, opts SheetsImportOptions) (SheetsImportSummary, error) {
	if opts.Path == "" {
		return SheetsImportSummary{}, fmt.Errorf("fees: 缺少导入文件路径")
	}
	if !validPeriod(opts.Year, opts.Month) {
		return SheetsImportSummary{}, fmt.Errorf("fees: 账期 %04d-%02d 不在合法范围", opts.Year, opts.Month)
	}

	sha, err := HashFile(opts.Path)
	if err != nil {
		return SheetsImportSummary{}, err
	}
	// issue #6: same recording discipline as the single-sheet channel.
	if opts.SourcePath == "" {
		if abs, aerr := filepath.Abs(opts.Path); aerr == nil {
			opts.SourcePath = abs
		}
	}
	name := opts.OriginalName
	if name == "" {
		name = filepath.Base(opts.Path)
	}
	if _, err := SnapshotExt(name); err != nil {
		return SheetsImportSummary{}, fmt.Errorf("%w: %v", ErrImportRefused, err)
	}
	warnings := sourceWritableWarnings(opts.Path)
	sheets, skipped, err := ReadFeeFileSheets(opts.Path)
	if err != nil {
		return SheetsImportSummary{}, fmt.Errorf("%w: %v", ErrImportRefused, err)
	}
	// The CSV pseudo-sheet has no name — the multi-sheet channel's whole
	// premise (sheet names as type declarations) is absent; refuse rather
	// than run a header-guess fallback the single-sheet channel already
	// does better with its declared type.
	if len(sheets) == 1 && sheets[0].Name == "" {
		return SheetsImportSummary{}, refusef("多 sheet 导入模式不支持 CSV（CSV 没有工作表名可判定费用类型）——请用单 sheet 模式显式声明 feeType")
	}

	// Mode guard: this IS the multi-sheet channel, so the mode being
	// entered is multi — the guard then refuses when the same file has
	// single-sheet batches (sheet '' keys, invisible to the per-sheet
	// idempotency check about to run).
	if err := s.refuseModeConflict(ctx, sha, name, multiSheetBatch); err != nil {
		return SheetsImportSummary{}, err
	}

	// #20 cross-period guard — workbook-wide (every sheet batch of this
	// import shares the declaration, so one guard covers them all).
	if _, err := s.refuseCrossPeriodDuplicate(ctx, sha, name, opts.Year, opts.Month, opts.AllowPeriodDuplicate); err != nil {
		return SheetsImportSummary{}, err
	}

	// Classification pre-pass: EVERY sheet must resolve (type + mandatory
	// columns) before ANY sheet imports — the all-or-nothing refusal that
	// makes "completed with missing data" impossible.
	type sheetPlan struct {
		sheet   SheetData
		feeType FeeType
	}
	plans := make([]sheetPlan, 0, len(sheets))
	var refusals []string
	for _, sd := range sheets {
		typ, _ := RecognizeSheetType(sd.Name, sd.Headers)
		if typ == "" {
			refusals = append(refusals, fmt.Sprintf("工作表 %q：无法从表名/表头唯一判定费用类型", sd.Name))
			continue
		}
		_, mapping := Recognize(sd.Headers)
		if mapping["siteCode"] == "" || mapping["amount"] == "" {
			refusals = append(refusals, fmt.Sprintf("工作表 %q（%s）：缺少必需列映射（siteCode=%q amount=%q，表头：%v）",
				sd.Name, typ, mapping["siteCode"], mapping["amount"], sd.Headers))
			continue
		}
		// #15 pre-flight period check — the same three-tier rule the
		// single-sheet channel runs inside importSheet, hoisted here so
		// a mismatching sheet refuses the WHOLE workbook before any
		// sheet lands (a period error on sheet 2 with sheet 1 already
		// committed would leave a half-period workbook, the exact
		// poisoning this issue exists to stop).
		check := CheckPeriod(DetectPeriod(sd.Headers, sd.Rows), opts.Year, opts.Month, opts.TrustDeclaredPeriod)
		if check.Refuse {
			refusals = append(refusals, fmt.Sprintf("工作表 %q（%s）：%s", sd.Name, typ, check.Message))
			continue
		}
		plans = append(plans, sheetPlan{sheet: sd, feeType: typ})
	}
	if len(refusals) > 0 {
		return SheetsImportSummary{}, refusef("多 sheet 导入被拒绝（先修复以下工作表，或从文件中移除无关工作表；本导入未写入任何数据）：%s", strings.Join(refusals, "；"))
	}

	out := SheetsImportSummary{
		WorkbookName: name, SHA256: sha, Year: opts.Year, Month: opts.Month,
		Status: "completed", SkippedSheets: skipped,
	}
	fatal := false
	for _, p := range plans {
		summary, err := s.importSheet(ctx, ImportOptions{
			Path: opts.Path, OriginalName: opts.OriginalName,
			FeeType: p.feeType, Year: opts.Year, Month: opts.Month,
			SourcePath:          opts.SourcePath,
			StrictRows:          opts.StrictRows,
			TrustDeclaredPeriod: opts.TrustDeclaredPeriod,
			// Mapping intentionally nil: each sheet's columns differ — the
			// recognizer runs per sheet inside importSheet.
		}, p.sheet, sha, name, p.sheet.Name)
		result := SheetBatchResult{
			Sheet: p.sheet.Name, FeeType: string(p.feeType),
			PeriodSource: summary.PeriodSource, PeriodBasis: summary.PeriodBasis,
			RowsTotal: summary.RowsTotal, RowsImported: summary.RowsImported,
			RowsFailed: summary.RowsFailed, Files: summary.Files, Errors: summary.Errors,
			SkippedDuplicate: summary.SkippedDuplicate, ExistingBatch: summary.ExistingBatch,
			// #25: a skipped-duplicate sheet wrote nothing — its warnings stay
			// empty (the library record rides periodBasis); a fresh sheet gets
			// the workbook-level notices (source writability of #6) prepended
			// to its own, the pre-#25 merge that used to emit the advice twice
			// for duplicates once ran unconditionally.
			Warnings: func() []string {
				if summary.SkippedDuplicate {
					return nil
				}
				return append(append([]string{}, warnings...), summary.Warnings...)
			}(),
		}
		if result.SkippedDuplicate && result.BatchID == "" {
			result.BatchID = summary.ExistingBatch
		} else {
			result.BatchID = summary.BatchID
		}
		switch {
		case err == nil && summary.Status == "completed":
			result.Status = "completed"
		case errors.As(err, new(*ErrDuplicateImport)):
			// the informational skip: the existing completed batch IS the answer
			result.Status = "completed"
		case summary.Status == "failed":
			// every row failed normalization — the sheet's batch row is the record
			result.Status = "failed"
			fatal = true
		default:
			// snapshot IO / DB / Parquet fault: the sheet did not land; earlier
			// sheets stand, this entry tells the caller exactly that
			result.Status = "failed"
			result.Errors = append(result.Errors, err.Error())
			fatal = true
		}
		out.Batches = append(out.Batches, result)
	}
	if fatal {
		out.Status = "partial"
		return out, refusef("多 sheet 导入部分失败（成功批次的批次号见 batches；失败原因见对应条目）")
	}
	return out, nil
}

// sourceWritableWarnings is the issue #6 import-time notice: the system
// itself never writes source files (os.Open -> snapshot copy), but a
// WRITABLE source is one careless "fix the number in Excel" away from a
// broken evidence chain, and until now nothing even noticed. The notice
// is non-blocking (the operator keeps full agency).
//
// Cross-platform truth (#14): Go's os.Stat maps the Windows read-only
// attribute (FILE_ATTRIBUTE_READONLY) into the mode bits — a normal file
// reports 0666 there, `attrib +R` reports 0444 — so the single `&0o200`
// probe is truthful on every platform. The former windows short-circuit
// (#14's root cause) assumed the write bit was meaningless on Windows;
// in fact that mapping is exactly what makes the shared check correct,
// and skipping it left Windows users without the advice while the tests
// kept asserting it — the platform assumptions of impl and tests diverged.
func sourceWritableWarnings(path string) []string {
	info, err := os.Stat(path)
	if err != nil {
		return nil // unreadable = the import will surface the real error
	}
	if info.Mode().Perm()&0o200 == 0 {
		return nil // read-only source: the advised state, nothing to say
	}
	return []string{
		"源文件当前可写——建议将原始台账目录设为只读（chmod a-w / chattr +i；Windows attrib +R），防止事后改动破坏证据链；本系统对源文件只读，但无法阻止外部程序改动（issue #6）",
	}
}

// SourceVerifyStatus is the per-batch verdict of fees verify-sources.
type SourceVerifyStatus string

const (
	SourceMatched SourceVerifyStatus = "match"   // file hash == import-time hash
	SourceChanged SourceVerifyStatus = "changed" // file exists, hash differs — the source was modified after import
	SourceMissing SourceVerifyStatus = "missing" // file no longer exists
	SourceUnknown SourceVerifyStatus = "unknown" // no recorded source path (pre-v35 / staged-then-GC'd rows) — skipped, never guessed
)

// SourceVerifyItem is one batch's verdict with the evidence to act on.
type SourceVerifyItem struct {
	BatchID        string             `json:"batchId"`
	WorkbookName   string             `json:"workbookName"`
	Sheet          string             `json:"sheet,omitempty"`
	Status         SourceVerifyStatus `json:"status"`
	SourcePath     string             `json:"sourcePath,omitempty"`
	RecordedSHA    string             `json:"recordedSha256"`
	CurrentSHA     string             `json:"currentSha256,omitempty"`
	SnapshotIntact bool               `json:"snapshotIntact"`
	Note           string             `json:"note,omitempty"`
}

// SourceVerifyReport is the single-JSON envelope of fees verify-sources.
type SourceVerifyReport struct {
	Checked int                `json:"checked"`
	Match   int                `json:"match"`
	Changed int                `json:"changed"`
	Missing int                `json:"missing"`
	Unknown int                `json:"unknown"`
	Results []SourceVerifyItem `json:"results"`
}

// VerifyBatchProblem is one inconsistency `fees verify-batches` (#20 方案 C)
// reports. Kind is one of: "snapshot-period" (the snapshot directory's
// year/month disagrees with fee_batches), "analysis-partition" (a Parquet
// partition of the batch disagrees with fee_batches), "analysis-missing"
// (a completed batch has no Parquet file at all — rolled-back-elsewhere or
// GC'd), "duplicate-sha-period" (the same file hash lives under more than
// one period — the #20 core shape); and since #32 the DATA-VALIDITY face
// with the #33 DQ ids and severities riding the new Check/Severity fields:
//
//	DQ1 "zero-amount" (high)   — every row of the batch carries amount 0,
//	                              the #29 wrong-column import shape: 454
//	                              rows, totalCents all 0, zero warnings.
//	DQ2 "empty-site-code" (high) — every row's site code blank; the
//	                              import guard refuses blanks row-by-row,
//	                              so a live hit is a writer regression.
//	DQ3 "single-site-code" (high) — rows > 1 but ALL one site: the issue's
//	                              literal distinct==0 is unreachable here
//	                              (site_code is never NULL in this schema —
//	                              the all-NULL shape collapses into DQ2's
//	                              blank fingerprint); the useful
//	                              distinctness signal our data can carry
//	                              is whole-batch-one-site (a paste-error
//	                              fingerprint), implemented instead.
//	DQ4 "row-count-cliff" (medium) — same fee type, adjacent periods with
//	                              data: this period imported < 50% of the
//	                              previous period's rows. The #12 import
//	                              guard warns at the door against last
//	                              year; this is the ledger-level audit
//	                              across all history (both builds — pure
//	                              DB face).
//	DQ5 "all-negative-amount" (high) / "majority-negative-amount" (medium)
//	                              — the minus side's share: 100% high,
//	                              > 30% (and not 100%) medium.
//	DQ6 has NO verify-side kind BY DESIGN: the multi-candidate amount
//	    column is an import-time face (#29's amountColumn + variants
//	    warnings answer it when the evidence — the headers — is in
//	    hand); re-parsing snapshots per verify run would re-do the import
//	    face with less context. The mapping is documented, not faked.
type VerifyBatchProblem struct {
	Kind     string `json:"kind"`
	BatchID  string `json:"batchId"`
	Detail   string `json:"detail"`
	Check    string `json:"check,omitempty"`    // #33: the DQ face id on data problems
	Severity string `json:"severity,omitempty"` // #33: high/medium on data problems
}

// VerifyBatchesReport is the verify-batches envelope: every completed batch
// checked against its snapshot path, its analysis partitions and the file's
// other-period duplicates. Clean = zero problems (the counts still tell the
// story). Read-only: the report NEVER mutates or refuses anything —
// historical pollution is surfaced, the operator decides (roll back the bad
// batch, re-import, or accept with eyes open).
//
// #32: the problems split into two tiers — PeriodProblems (the #20
// three-face period consistency, unchanged) and DataProblems (the
// data-validity checks — an all-zero-amount batch used to sail through
// verify-batches as problems: [], and a clean audit on garbage data is
// worse than no audit: it hands the operator a false all-clear). Problems
// keeps the merged list for the pre-#32 consumers (CLI exit code, HTTP
// readers). DataChecksSkipped=true names the default-build shape honestly:
// without the duckdb engine the data face CANNOT be checked, and a silent
// skip would be the exact false-security this tool must never hand out.
type VerifyBatchesReport struct {
	BatchesChecked    int                  `json:"batchesChecked"`
	Problems          []VerifyBatchProblem `json:"problems"`
	PeriodProblems    []VerifyBatchProblem `json:"periodProblems"`
	DataProblems      []VerifyBatchProblem `json:"dataProblems"`
	DataChecksSkipped bool                 `json:"dataChecksSkipped,omitempty"`
}

// batchDataTarget is the #32 data face's worklist entry: one completed
// batch's live parquet files (the same set the partition walk saw).
type batchDataTarget struct {
	id    string
	files []string
}

// VerifyBatches is #20 方案 C plus the #32 data-validity face: the
// batch row, the snapshot directory and the analysis partitions each
// record a period — they must agree; a same-SHA family spanning several
// periods is the cross-period duplicate the import guard now refuses at
// the door (pre-guard history shows up here). Since #32 every completed
// batch's Parquet ALSO gets a money-health aggregate — all-zero amounts
// (the #29 wrong-column import sailed through as problems: []), blank
// site codes, all-negative amounts. Read-only as ever: the operator
// decides. The engine param names the data face's dependency — default
// builds honestly set DataChecksSkipped instead of pretending.
func (s *Service) VerifyBatches(ctx context.Context, engine QueryEngine) (VerifyBatchesReport, error) {
	batches, err := s.db.FeeBatches.List(ctx, "")
	if err != nil {
		return VerifyBatchesReport{}, fmt.Errorf("fees: 读取费用批次: %w", err)
	}
	report := VerifyBatchesReport{
		Problems:       []VerifyBatchProblem{},
		PeriodProblems: []VerifyBatchProblem{},
		DataProblems:   []VerifyBatchProblem{},
	}

	// sha -> set of distinct periods (completed batches only — a failed
	// attempt blocks nothing and double-counts nothing).
	type periodKey struct{ y, m int }
	shaPeriods := make(map[string]map[periodKey]string) // sha -> period -> batch id
	var dataTargets []batchDataTarget
	// #33 DQ4: per-fee-type period -> imported rows accumulator (the
	// ledger-level adjacent-period cliff is a pure DB face — both builds).
	type periodRowTotal struct {
		y, m, rows int
		firstID    string
	}
	cliffAcc := make(map[FeeType][]periodRowTotal)

	for _, b := range batches {
		if b.Status != FeeStatusCompleted {
			continue
		}
		report.BatchesChecked++

		// 1. snapshot path vs batch row: fee-imports/<type>/<year>/<mm>/<sha>_<name>
		if parts := strings.Split(b.SnapshotPath, "/"); len(parts) >= 4 {
			if y, err1 := strconv.Atoi(parts[2]); err1 == nil {
				if m, err2 := strconv.Atoi(parts[3]); err2 == nil {
					if y != b.FeeYear || m != b.FeeMonth {
						report.PeriodProblems = append(report.PeriodProblems, VerifyBatchProblem{
							Kind:    "snapshot-period",
							BatchID: b.ID,
							Detail:  fmt.Sprintf("批次账期 %04d-%02d 与快照目录 %s（%04d-%02d）不一致", b.FeeYear, b.FeeMonth, b.SnapshotPath, y, m),
						})
					}
				}
			}
		}

		// 2. analysis partitions vs batch row: every live parquet file of the
		// batch must sit under year=<row>/month=<row>. The pre-#20 layout
		// (row-local partitions) is exactly what this check surfaces.
		files, ferr := BatchParquetFiles(s.analysisDir(), b.ID)
		if ferr != nil {
			report.PeriodProblems = append(report.PeriodProblems, VerifyBatchProblem{
				Kind:    "analysis-partition",
				BatchID: b.ID,
				Detail:  "扫描分析层失败: " + ferr.Error(),
			})
		} else if len(files) == 0 {
			report.PeriodProblems = append(report.PeriodProblems, VerifyBatchProblem{
				Kind:    "analysis-missing",
				BatchID: b.ID,
				Detail:  "completed 批次在分析层无 Parquet 文件（已回滚/被 GC？）",
			})
		} else {
			for _, f := range files {
				if !s.partitionMatches(f, b.FeeYear, b.FeeMonth) {
					report.PeriodProblems = append(report.PeriodProblems, VerifyBatchProblem{
						Kind:    "analysis-partition",
						BatchID: b.ID,
						Detail:  fmt.Sprintf("批次账期 %04d-%02d 与分析层分区 %s 不一致", b.FeeYear, b.FeeMonth, relPathOf(s.analysisDir(), f)),
					})
				}
			}
			// queue the batch for the #32 money-health pass; a refused
			// queue entry is NAMED as its own data problem, never skipped.
			if terr := s.queueDataTarget(b.ID, files); terr != nil {
				report.DataProblems = append(report.DataProblems, VerifyBatchProblem{
					Kind:    "data-check-refused",
					BatchID: b.ID,
					Detail:  "数据有效性检查未运行: " + terr.Error(),
				})
			} else {
				dataTargets = append(dataTargets, batchDataTarget{id: b.ID, files: files})
			}
		}

		// 3. accumulate the sha-period family
		if shaPeriods[b.SHA256] == nil {
			shaPeriods[b.SHA256] = make(map[periodKey]string)
		}
		shaPeriods[b.SHA256][periodKey{b.FeeYear, b.FeeMonth}] = b.ID

		// 3b. accumulate the DQ4 period face (adjacent-period row
		// totals per fee type; multiple batches of one period sum —
		// a period's size is its ledger, not any one file).
		acc := cliffAcc[b.FeeType]
		found := false
		for i := range acc {
			if acc[i].y == b.FeeYear && acc[i].m == b.FeeMonth {
				acc[i].rows += b.RowsImported
				found = true
				break
			}
		}
		if !found {
			acc = append(acc, periodRowTotal{y: b.FeeYear, m: b.FeeMonth, rows: b.RowsImported, firstID: b.ID})
		}
		cliffAcc[b.FeeType] = acc
	}

	// 4. same sha under several periods — the #20 shape; name every member.
	for sha, periods := range shaPeriods {
		if len(periods) < 2 {
			continue
		}
		members := make([]string, 0, len(periods))
		for pk, id := range periods {
			members = append(members, fmt.Sprintf("%04d-%02d（批次 %s）", pk.y, pk.m, id))
		}
		sort.Strings(members)
		for pk, id := range periods {
			report.PeriodProblems = append(report.PeriodProblems, VerifyBatchProblem{
				Kind:    "duplicate-sha-period",
				BatchID: id,
				Detail:  fmt.Sprintf("文件（SHA256 前 12 位 %s）同时存在于多个账期：%s ——同文件多账期会把数据重复入账", sha[:12], strings.Join(members, "、")),
			})
			_ = pk
		}
	}

	// 4b. the #33 DQ4 row-count cliff: per fee type, order the periods
	// that carry data and compare each against its immediate
	// predecessor — importing under half the previous period's rows
	// earns a medium warning naming both ledgers. A gap month is NOT
	// a cliff (a missing import is the missing-billing scenarios'
	// story, not this check's); the comparison is strictly
	// adjacent-periods-with-data.
	for ft, acc := range cliffAcc {
		sort.Slice(acc, func(i, j int) bool {
			if acc[i].y != acc[j].y {
				return acc[i].y < acc[j].y
			}
			return acc[i].m < acc[j].m
		})
		for i := 1; i < len(acc); i++ {
			if acc[i-1].rows <= 0 || acc[i].rows >= acc[i-1].rows/2 {
				continue
			}
			report.DataProblems = append(report.DataProblems, VerifyBatchProblem{
				Kind:    "row-count-cliff",
				BatchID: acc[i].firstID,
				Detail: fmt.Sprintf("%s 账期 %04d-%02d 仅导入 %d 行，不足上一有数据账期 %04d-%02d（%d 行）的一半——本月表不全或上期口径变化，请人工核对",
					FeeTypeLabel(ft), acc[i].y, acc[i].m, acc[i].rows, acc[i-1].y, acc[i-1].m, acc[i-1].rows),
				Check:    "DQ4",
				Severity: "medium",
			})
		}
	}

	// 5. the #32 data-validity face: only the real engine can aggregate
	// Parquet. The default build sets the honest skip flag — a silent skip
	// would hand out exactly the false all-clear this tool must never
	// produce (the issue's own verdict: 「检查通过」比「没检查」更危险).
	if engine != nil && engine.Available() {
		probs, derr := s.verifyBatchDataHealth(ctx, dataTargets)
		if derr != nil {
			return VerifyBatchesReport{}, derr
		}
		report.DataProblems = append(report.DataProblems, probs...)
	} else {
		report.DataChecksSkipped = true
	}
	report.Problems = append(report.Problems, report.PeriodProblems...)
	report.Problems = append(report.Problems, report.DataProblems...)
	return report, nil
}

// queueDataTarget validates one batch's file set before it may reach the
// #32 query face: ids are re-checked against the strict whitelist and any
// path carrying a quote/backslash byte is refused (the SQL text carries
// ONLY system-generated paths — the same discipline as feeTableGlob; a
// refused entry is named in the report, never silently skipped).
func (s *Service) queueDataTarget(batchID string, files []string) error {
	if !safeBatchID(batchID) {
		return fmt.Errorf("批次号 %q 不在白名单字符集（[A-Za-z0-9_-]）内", batchID)
	}
	for _, f := range files {
		// Windows-path lesson (the CI run that failed on windows-latest):
		// the analysis paths are OS-native and backslash-separated there —
		// the raw byte check refused EVERY batch on Windows and the two
		// verify tests went red exactly there, green on Linux. Normalize
		// with ToSlash FIRST (the query face does the same), then check:
		// a quote byte would be the only way out of a SQL string literal,
		// and a backslash SURVIVING ToSlash means data corruption, not a
		// path separator.
		p := filepath.ToSlash(f)
		if strings.Contains(p, "'") || strings.Contains(p, "\\") {
			return fmt.Errorf("分析层路径 %s 含引号/反斜杠字节，拒绝进入查询文本", relPathOf(s.analysisDir(), f))
		}
	}
	return nil
}

// safeBatchID is the #32 SQL face's whitelist: ids are system-generated
// (fee_<uuid>), and the guard keeps a future writer's regression from
// ever reaching the query text.
func safeBatchID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// partitionMatches checks one analysis-layer file path against the expected
// (year, month): the hive path .../year=YYYY/month=MM/batch-<id>.parquet.
func (s *Service) partitionMatches(absPath string, year, month int) bool {
	parts := strings.Split(filepath.ToSlash(absPath), "/")
	wantY := fmt.Sprintf("year=%d", year)
	wantM := fmt.Sprintf("month=%02d", month)
	var haveY, haveM string
	for _, p := range parts {
		switch {
		case strings.HasPrefix(p, "year="):
			haveY = p
		case strings.HasPrefix(p, "month="):
			haveM = p
		}
	}
	return haveY == wantY && haveM == wantM
}

// VerifySources re-hashes every fee batch's recorded source file against
// the hash captured at import time (issue #6 方案 A2): the three honest
// verdicts are match / changed / missing; batches without a recorded path
// are counted as unknown and skipped (never guessed). The snapshot side
// is checked too — the content-addressed copy must still exist and still
// hash to the recorded value, so a "changed" verdict always has the
// untampered evidence to fall back on.
func (s *Service) VerifySources(ctx context.Context) (SourceVerifyReport, error) {
	batches, err := s.db.FeeBatches.List(ctx, "")
	if err != nil {
		return SourceVerifyReport{}, fmt.Errorf("fees: 读取费用批次: %w", err)
	}
	report := SourceVerifyReport{Results: []SourceVerifyItem{}}
	for _, b := range batches {
		item := SourceVerifyItem{
			BatchID: b.ID, WorkbookName: b.WorkbookName, Sheet: b.Sheet,
			RecordedSHA: b.SHA256,
		}
		// Snapshot integrity: the evidence copy must still exist and still
		// carry the recorded content (the one thing verify-sources CAN
		// guarantee even for unknown-source rows).
		snapPath := filepath.Join(s.dir, b.SnapshotPath)
		if snapSHA, err := HashFile(snapPath); err == nil && snapSHA == b.SHA256 {
			item.SnapshotIntact = true
		}
		if b.SourcePath == "" {
			item.Status = SourceUnknown
			item.Note = "批次未记录源文件路径（v35 前批次或 HTTP 暂存通道）——跳过源核对"
		} else if _, err := os.Stat(b.SourcePath); err != nil {
			item.Status = SourceMissing
			item.SourcePath = b.SourcePath
			item.Note = "源文件已不存在——快照仍在（可恢复），但无法再核对源侧"
		} else if cur, err := HashFile(b.SourcePath); err != nil {
			item.Status = SourceMissing
			item.SourcePath = b.SourcePath
			item.Note = "源文件无法读取：" + err.Error()
		} else if cur == b.SHA256 {
			item.Status = SourceMatched
			item.SourcePath = b.SourcePath
			item.CurrentSHA = cur
		} else {
			item.Status = SourceChanged
			item.SourcePath = b.SourcePath
			item.CurrentSHA = cur
			item.Note = "源文件内容与导入时不一致——源侧已被改动；本批结论以快照为准并需人工核实改动来源"
		}
		report.Checked++
		switch item.Status {
		case SourceMatched:
			report.Match++
		case SourceChanged:
			report.Changed++
		case SourceMissing:
			report.Missing++
		case SourceUnknown:
			report.Unknown++
		}
		report.Results = append(report.Results, item)
	}
	return report, nil
}

// crossCheckDeclaredType refuses a declared fee type that CONTRADICTS the
// sheet's own evidence (#125 plan C): the classifier needs score ≥ 2 — a
// sheet-name hit alone or ≥2 header marker hits — to overrule the
// operator; agreeing or silent evidence never refuses (the operator's
// declaration always wins when the file says nothing).
func crossCheckDeclaredType(declared FeeType, sheet SheetData) error {
	recognized, score := RecognizeSheetType(sheet.Name, sheet.Headers)
	if recognized == "" || recognized == declared || score < 2 {
		return nil
	}
	return refusef("声明的费用类型 %s 与工作表自身证据矛盾（工作表 %q 的表名/表头指向 %s）——同一文件换类型重导会把 %s 的数据计入 %s 账本；请修正 feeType 或改用多 sheet 导入模式",
		declared, sheet.Name, recognized, recognized, declared)
}

// refuseModeConflict refuses an import whose file was already imported
// through the OTHER channel (#125): the two channels key the same data
// differently (sheet ” vs sheet names), so both landing would double-
// count rows the per-key idempotency check cannot see. mode is the
// channel being ENTERED; the guard looks for completed batches of the
// OTHER channel's shape. A refused operator has the honest paths: roll
// back the old batches, or keep using the channel that already imported.
func (s *Service) refuseModeConflict(ctx context.Context, sha, name string, mode batchMode) error {
	completed, err := s.db.FeeBatches.CompletedBySHA(ctx, sha)
	if err != nil {
		// #104 discipline: a failed guard check must not read as "no
		// conflict" — the guarantee would be silently void.
		return fmt.Errorf("fees: 导入模式冲突预检: %w", err)
	}
	for _, b := range completed {
		conflict := false
		switch mode {
		case singleSheetBatch:
			conflict = b.Sheet != "" // multi-sheet batches exist for this file
		case multiSheetBatch:
			conflict = b.Sheet == "" // single-sheet batches exist for this file
		}
		if !conflict {
			continue
		}
		other := "多 sheet"
		if mode == multiSheetBatch {
			other = "单 sheet"
		}
		return refusef("文件 %s 曾按%s模式导入过（批次 %s，类型 %s，%d-%02d）——两种模式的幂等键不同，重复经两种模式导入会把同一数据重复入账；请先回滚旧批次，或继续沿用原模式",
			name, other, b.ID, b.FeeType, b.FeeYear, b.FeeMonth)
	}
	return nil
}

// summaryFromBatch (#24) rebuilds a COMPLETE ImportSummary from an
// existing batch row — the two duplicate branches (pre-flight hit and
// commit-race hit) used to hand-build partial structs, so the same
// duplicate was reported with a different field set depending on which
// internal path hit (periodSource empty, workbookName missing, files
// and warnings silently absent). The batch row is the single source:
// every identity field comes back exactly as recorded; files[] is
// recovered from the analysis layer (the batch row does not store the
// manifest); the caller layers SkippedDuplicate/ExistingBatch on top.
func (s *Service) summaryFromBatch(ctx context.Context, existing domain.FeeBatch, opts ImportOptions) ImportSummary {
	sum := ImportSummary{
		BatchID: existing.ID, Status: string(existing.Status),
		WorkbookName: existing.WorkbookName, SHA256: existing.SHA256,
		FeeType: string(existing.FeeType), FeeYear: existing.FeeYear, FeeMonth: existing.FeeMonth,
		PeriodSource: existing.PeriodSource, PeriodBasis: existing.PeriodBasis,
		RowsTotal: existing.RowsTotal, RowsImported: existing.RowsImported, RowsFailed: existing.RowsFailed,
		SkippedDuplicate: true, ExistingBatch: existing.ID,
	}
	// files[]: the analysis layer is where the batch's data lives — the
	// honest manifest for a duplicate answer (legacy pre-#65 batches may
	// legitimately have none left after a rollback).
	if rels, err := BatchParquetFiles(s.analysisDir(), existing.ID); err == nil {
		for _, p := range rels {
			sum.Files = append(sum.Files, relPathOf(s.analysisDir(), p))
		}
	}
	// warnings (#25 方案 B): a duplicate import writes NOTHING — the
	// current-run notices (source writability of #6, the 骤降线 of #12)
	// describe an import event that did not happen, and re-deriving
	// them here is exactly what used to land byte-identical duplicates
	// when the callers prepended their own copy. The durable record
	// rides the periodBasis/periodSource fields already; the warnings
	// list of a duplicate answer stays empty — only the library-recorded
	// trail, never fresh advice about a file nobody re-read.
	return sum
}

// warningsFor renders the import-sheet-level warning list for the normal
// path's summary: #20's row/declaration disagreement note when present,
// nil otherwise (the outer Import appends its own warnings — file-level
// notices and the cross-period override — onto this list).
func warningsFor(note string) []string {
	if note == "" {
		return nil
	}
	return []string{note}
}

// refuseCrossPeriodDuplicate is #20's guard A: the per-key idempotency
// check includes the period in its key, so the SAME file re-imported under
// a different --year/--month sails past it and doubles the data silently.
// The SHA256 is the strong invariant the key lacks — same bytes, other
// period, completed batch => refuse (both periods + the batch id + the
// override flag in the message). With allow set the refusal degrades to the
// audit warning string (the caller records it on the batch and the summary).
func (s *Service) refuseCrossPeriodDuplicate(ctx context.Context, sha, name string, year, month int, allow bool) (string, error) {
	completed, err := s.db.FeeBatches.CompletedBySHA(ctx, sha)
	if err != nil {
		// #104 discipline: a failed guard check must not read as "no
		// conflict" — the guarantee would be silently void.
		return "", fmt.Errorf("fees: 跨账期重复预检: %w", err)
	}
	// Same-period batches are NOT this guard's business: the per-key
	// idempotency check answers them informationally (the duplicate import
	// that re-asks an already-imported period must keep getting "skipped:
	// batch X" even when the file also has foreign-period history — refusing
	// here would mask the idempotent answer with a cross-period error and
	// leave the operator thinking their period was wrong).
	samePeriod := false
	var foreign *domain.FeeBatch
	for i := range completed {
		b := completed[i]
		if b.FeeYear == year && b.FeeMonth == month {
			samePeriod = true
			continue
		}
		if foreign == nil {
			foreign = &completed[i]
		}
	}
	if foreign == nil || samePeriod {
		// no foreign-period history — or the declared period already holds
		// this file's batch: the per-key idempotency check is about to
		// answer informationally, and THAT answer (skipped: batch X) is the
		// honest one for a re-ask of an already-imported period.
		return "", nil
	}
	b := *foreign
	// multi-sheet siblings share the file hash by construction, but a
	// sibling always shares the workbook's ONE declared period — a
	// different-period hit is never a sibling of this import.
	if allow {
		return fmt.Sprintf("跨账期重复覆盖：文件已作为 %04d-%02d 导入（批次 %s，类型 %s）——本次以 --allow-period-duplicate 显式按申报账期 %04d-%02d 导入，事实留痕于批次", b.FeeYear, b.FeeMonth, b.ID, b.FeeType, year, month), nil
	}
	return "", refusef("文件 %s（SHA256 前 12 位 %s）已作为账期 %04d-%02d 导入过（批次 %s，类型 %s），与本次申报账期 %04d-%02d 不同——同一文件按另一账期再导入会把同一数据重复入账且账期口径互相矛盾；若确属补录/重分类，请显式覆盖（CLI：--allow-period-duplicate；HTTP 表单：allowPeriodDuplicate=true）", name, sha[:12], b.FeeYear, b.FeeMonth, b.ID, b.FeeType, year, month)
}

// normalizeRow converts one source row into a NormalizedRow. Period
// resolution order: the row's own period column → the row's date column →
// the batch's declared period. Amount and site code are mandatory; the
// period column, when mapped and non-empty, MUST parse (a garbage period
// is a row failure, not a silent fallback — the partition grain is the
// whole point).
func (s *Service) normalizeRow(row SourceRow, siteIdx, amountIdx, periodIdx, dateIdx int, ledgerIdx map[string]int, opts ImportOptions, sourceFile, batchID string) (NormalizedRow, *ImportError) {
	code := NormalizeSiteCode(cellAt(row, siteIdx))
	if code == "" {
		return NormalizedRow{}, &ImportError{Row: row.Number, Field: "siteCode", Raw: cellAt(row, siteIdx), Reason: "站址编码为空"}
	}
	amountRaw := cellAt(row, amountIdx)
	amountCents, err := NormalizeAmount(amountRaw)
	if err != nil {
		return NormalizedRow{}, &ImportError{Row: row.Number, Field: "amount", Raw: amountRaw, Reason: err.Error()}
	}
	year, month := opts.Year, opts.Month
	if periodIdx >= 0 {
		if raw := cellAt(row, periodIdx); raw != "" {
			y, m, perr := ParsePeriod(raw)
			if perr != nil {
				return NormalizedRow{}, &ImportError{Row: row.Number, Field: "period", Raw: raw, Reason: perr.Error()}
			}
			year, month = y, m
		}
	}
	if dateIdx >= 0 {
		if raw := cellAt(row, dateIdx); raw != "" {
			y, m, derr := ParseDatePeriod(raw)
			if derr != nil {
				return NormalizedRow{}, &ImportError{Row: row.Number, Field: "date", Raw: raw, Reason: derr.Error()}
			}
			year, month = y, m
		}
	}
	if !validPeriod(year, month) {
		return NormalizedRow{}, &ImportError{Row: row.Number, Field: "period", Raw: fmt.Sprintf("%04d-%02d", year, month), Reason: "账期越界"}
	}
	out := NormalizedRow{
		SiteCode: code, AmountCents: amountCents, Year: year, Month: month,
		SourceFile: sourceFile, SourceRow: row.Number, ImportBatch: batchID,
	}
	// #5: the optional ledger columns parse LENIENTLY by default — absent
	// or empty cell = nil, unparseable numeric = nil (the row's CORE
	// identity and amount already passed the strict gate; a bad optional
	// cell must not sink the row's money record). The strict mode of #8
	// (--strict-rows) turns unparseable optionals into row errors.
	if idx := ledgerIdx["recordKey"]; idx >= 0 {
		if v := strings.TrimSpace(cellAt(row, idx)); v != "" {
			out.RecordKey = &v
		}
	}
	if idx := ledgerIdx["meterNo"]; idx >= 0 {
		if v := cellAt(row, idx); v != "" {
			out.MeterNo = &v
		}
	}
	for field, dst := range map[string]**float64{
		"readingStart": &out.ReadingStart, "readingEnd": &out.ReadingEnd,
		"meterPrevEnd": &out.MeterPrevEnd, "quantity": &out.Quantity,
		"unitPrice": &out.UnitPrice, "shareMobile": &out.ShareMobile,
		"shareUnicom": &out.ShareUnicom, "shareTelecom": &out.ShareTelecom,
		"shareTower": &out.ShareTower,
	} {
		idx := ledgerIdx[field]
		if idx < 0 {
			continue
		}
		raw := cellAt(row, idx)
		v, ok := parseOptionalFloat(raw)
		if !ok {
			// #8 --strict-rows: a cell the file DID write but no number can be
			// read from is a hand-mangled value — fail the row with the reason
			// instead of importing a money record whose evidence columns lie.
			if opts.StrictRows && strings.TrimSpace(raw) != "" {
				return NormalizedRow{}, &ImportError{
					Row: row.Number, Field: field, Raw: raw,
					Reason: fmt.Sprintf("台账列 %s 的值 %q 无法解析为数字（--strict-rows：该行拒绝导入）", field, raw),
				}
			}
			continue
		}
		*dst = &v
	}
	if idx := ledgerIdx["meterSwapped"]; idx >= 0 {
		if v, ok := parseOptionalBool(cellAt(row, idx)); ok {
			out.MeterSwapped = &v
		}
	}
	// #11's transfer-supply catch-up columns: same lenient/strict
	// discipline as the #5 floats — absent/empty = nil (the sheet
	// carried no such column), a written-but-unparseable cell stays
	// nil in lenient mode and fails the row under --strict-rows (#8).
	for field, dst := range map[string]**string{
		"purchaseStart": &out.PurchaseStart, "purchaseEnd": &out.PurchaseEnd,
	} {
		idx := ledgerIdx[field]
		if idx < 0 {
			continue
		}
		raw := cellAt(row, idx)
		if strings.TrimSpace(raw) == "" {
			continue
		}
		v, derr := ParseDateISO(raw)
		if derr != nil {
			if opts.StrictRows {
				return NormalizedRow{}, &ImportError{
					Row: row.Number, Field: field, Raw: raw,
					Reason: fmt.Sprintf("台账列 %s 的值 %q 无法解析为日期（--strict-rows：该行拒绝导入）", field, raw),
				}
			}
			continue
		}
		*dst = &v
	}
	for field, dst := range map[string]**int64{
		"receivableCents": &out.ReceivableCents, "receivedCents": &out.ReceivedCents,
	} {
		idx := ledgerIdx[field]
		if idx < 0 {
			continue
		}
		raw := cellAt(row, idx)
		if strings.TrimSpace(raw) == "" {
			continue
		}
		v, aerr := NormalizeAmount(raw)
		if aerr != nil {
			if opts.StrictRows {
				return NormalizedRow{}, &ImportError{
					Row: row.Number, Field: field, Raw: raw,
					Reason: fmt.Sprintf("台账列 %s 的值 %q 无法解析为金额（--strict-rows：该行拒绝导入）", field, raw),
				}
			}
			continue
		}
		*dst = &v
	}
	return out, nil
}

// parseOptionalFloat is the lenient numeric parse of the optional ledger
// columns: empty or unparseable = absent (nil). 括号负数与千分位逗号 follow
// NormalizeAmount's tolerant forms via the same parse helper.
func parseOptionalFloat(raw string) (float64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	s := strings.NewReplacer(",", "", "，", "", " ", "", "　", "").Replace(raw)
	s = strings.NewReplacer("．", ".", "－", "-").Replace(s)
	// accounting negative: a full paren wrap means minus
	neg := false
	if len(s) >= 3 {
		if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
			neg, s = true, s[1:len(s)-1]
		} else if strings.HasPrefix(s, "（") && strings.HasSuffix(s, "）") {
			neg, s = true, s[len("（"):len(s)-len("）")]
		}
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '０' && r <= '９':
			b.WriteRune(r - '０' + '0')
		default:
			b.WriteRune(r)
		}
	}
	v, err := strconv.ParseFloat(b.String(), 64)
	if err != nil {
		return 0, false
	}
	if neg {
		v = -v
	}
	return v, true
}

// parseOptionalBool reads the 是否换表-style flag: 是/true/1/y = true,
// 否/false/0/n = false, anything else = absent.
func parseOptionalBool(raw string) (bool, bool) {
	raw = strings.TrimSpace(raw)
	switch raw {
	case "是", "true", "TRUE", "True", "1", "Y", "y":
		return true, true
	case "否", "false", "FALSE", "False", "0", "N", "n", "":
		return false, raw != ""
	}
	return false, false
}

// List returns the fee batches (newest first), optionally filtered by
// status ("" = all).
func (s *Service) List(ctx context.Context, status FeeStatus) ([]FeeBatch, error) {
	return s.db.FeeBatches.List(ctx, status)
}

// The rollback file-dance step names — the testMoveFault seam's op
// values (quarantine = live partition file → quarantine slot, restore =
// the inverse, i.e. an undo or torn-attempt adoption step).
const (
	moveQuarantine = "quarantine"
	moveRestore    = "restore"
)

// moveFault consults the test seam before one rollback file move; nil in
// production.
func (s *Service) moveFault(op, path string) error {
	if s.testMoveFault != nil {
		return s.testMoveFault(op, path)
	}
	return nil
}

// Rollback reverses a completed fee batch ATOMICALLY (#107): the delete
// set is materialized first (every partition file of the batch), each
// file is then RENAMED into the per-batch quarantine (a reversible,
// metadata-only move — parquet.go's #107 section), and only after the
// last file moved does the batch row flip to rolled_back — one commit
// point, the same shape as the import's step-7. The snapshot STAYS
// (evidence chain — the same asymmetry as the resource importer's batch
// rollback keeping the workbook).
//
// Failure semantics (the issue's acceptance, in order):
//
//   - any fault before the flip → the quarantine is UNDONE and the
//     batch keeps completed with EVERY row intact: a rollback either
//     fully happens or fully does not ("失败时不落任何删");
//   - a fault that even blocks the undo (a second IO fault) → the row
//     is marked failed with the quarantine's whereabouts in the error
//     text and never left lying as completed/rolled_back: 'failed' is the
//     existing honest marker (the partial idempotency index covers
//     completed rows only, so it also frees the key — the snapshot-backed
//     re-import restores the rows); GCAnalysis sweeps the quarantined
//     remains past the milestone.
//
// A previous attempt torn by a crash (files already quarantined, row
// still completed) is adopted first: the quarantined files are restored
// so this attempt re-plans from the full live set. Idempotent at the
// edges: rolling back a rolled-back batch answers ErrConflict; a
// missing file inside the plan is tolerated (already gone = where the
// delete wants it).
func (s *Service) Rollback(ctx context.Context, id string) (FeeBatch, error) {
	b, err := s.db.FeeBatches.GetByID(ctx, id)
	if err != nil {
		return FeeBatch{}, fmt.Errorf("fees: 回滚批次 %s: %w", id, err)
	}
	if b.Status != FeeStatusCompleted {
		return FeeBatch{}, fmt.Errorf("fees: 回滚批次 %s: 状态 %s，仅 completed 可回滚: %w", id, b.Status, domain.ErrConflict)
	}

	// adopt a torn attempt first: a crash mid-dance leaves files in the
	// quarantine with the row still completed; restoring them re-derives
	// the full live set before this attempt materializes its own plan.
	torn, terr := quarantinedFiles(s.analysisDir(), id)
	if terr != nil {
		return FeeBatch{}, fmt.Errorf("fees: 回滚批次 %s: %w", id, terr)
	}
	for _, p := range torn {
		if ferr := s.moveFault(moveRestore, p.live); ferr != nil {
			// rows would stay quarantined under a completed row — the
			// exact lie #107 closes; record the torn state honestly.
			s.markRollbackTorn(ctx, b, ferr)
			return FeeBatch{}, fmt.Errorf("fees: 回滚批次 %s: 恢复中断的隔离文件 %s: %w", id, p.live, ferr)
		}
		if rerr := restoreQuarantinedFile(p); rerr != nil {
			s.markRollbackTorn(ctx, b, rerr)
			return FeeBatch{}, fmt.Errorf("fees: 回滚批次 %s: %w", id, rerr)
		}
	}
	if len(torn) > 0 {
		_ = dropQuarantineTree(rollbackQuarantineRoot(s.analysisDir(), id))
	}

	// materialize the WHOLE delete plan before touching any file.
	files, err := BatchParquetFiles(s.analysisDir(), id)
	if err != nil {
		return FeeBatch{}, fmt.Errorf("fees: 回滚批次 %s: %w", id, err)
	}
	plan := buildRollbackPlan(s.analysisDir(), id, files)

	// execute: quarantine every file, each move reversible. Any fault
	// aborts into a full undo.
	for _, p := range plan {
		if ferr := s.moveFault(moveQuarantine, p.live); ferr != nil {
			return FeeBatch{}, s.abortRollback(ctx, b, plan, fmt.Errorf("fees: 隔离 Parquet 文件 %s: %w", p.live, ferr))
		}
		if qerr := quarantineFile(p); qerr != nil {
			return FeeBatch{}, s.abortRollback(ctx, b, plan, qerr)
		}
	}

	// the commit point: the flip is what turns the quarantine into a
	// deletion (the same single commit point as the import's).
	if err := s.db.Do(ctx, func(tx *repo.Tx) error {
		return tx.FeeBatches.MarkRolledBack(ctx, id)
	}); err != nil {
		// a concurrent winner may have flipped first; its cleanup then
		// took the quarantined files and the undo steps below no-op on
		// ENOENT. A file we do put back under a rolled_back row is
		// reclaimed by GCAnalysis past the milestone (not keep-set).
		return FeeBatch{}, s.abortRollback(ctx, b, plan, fmt.Errorf("fees: 提交回滚状态: %w", err))
	}

	// after the commit the quarantined rows are dead: drop the quarantine
	// tree and prune the emptied partition dirs (the hygiene
	// RemoveParquetFiles' rollback callers always had). The drop is
	// STILL best-effort at heart — a crash mid-drop leaves it to
	// GCAnalysis's .rollback sweep — but #17's Windows red (AV scanners
	// hold transient handles on freshly written files, the main repo's
	// env-fsync-antivirus precedent) gets a bounded retry first: three
	// attempts, 50ms apart, then the GC guarantee stands as designed.
	_ = dropQuarantineTree(rollbackQuarantineRoot(s.analysisDir(), id))
	for _, p := range plan {
		pruneEmptyDirs(filepath.Dir(p.live), 3)
	}
	return s.db.FeeBatches.GetByID(ctx, id)
}

// abortRollback undoes a half-executed dance — #107's core: every
// quarantined file goes back to its live slot, so a failed rollback
// leaves the batch completed with ALL rows intact (nothing deleted). If
// the undo itself hits a fault, the row is marked failed via
// markRollbackTorn instead of left lying as completed-with-rows-missing.
// The undo covers the whole plan (never-quarantined slots answer ENOENT
// and no-op), so it is idempotent against any abort point.
func (s *Service) abortRollback(ctx context.Context, b FeeBatch, plan []rollbackPlan, cause error) error {
	id := b.ID
	for _, p := range plan {
		if ferr := s.moveFault(moveRestore, p.live); ferr != nil {
			s.markRollbackTorn(ctx, b, ferr)
			return fmt.Errorf("fees: 回滚批次 %s: %w（恢复已隔离文件失败，批次已标记 failed，隔离区保留待 GC 清扫）: %v", id, cause, ferr)
		}
		if rerr := restoreQuarantinedFile(p); rerr != nil {
			s.markRollbackTorn(ctx, b, rerr)
			return fmt.Errorf("fees: 回滚批次 %s: %w（恢复已隔离文件 %s 失败，批次已标记 failed，隔离区保留待 GC 清扫）: %v", id, cause, p.live, rerr)
		}
	}
	_ = os.RemoveAll(rollbackQuarantineRoot(s.analysisDir(), id))
	return fmt.Errorf("fees: 回滚批次 %s: %w（已恢复全部分析行，批次保持 completed——回滚未发生）", id, cause)
}

// markRollbackTorn records a rollback that stopped halfway with files
// stranded in the quarantine: the row must NOT stay completed — part of
// its rows are out of the query surface. MarkFailed is the existing
// honest marker (adding a dedicated rollback_failed state would need a
// repo method — out of this fix's file boundary): the import accounting
// passes through unchanged (rows_total/rows_failed as they were),
// completed_at stays as the audit of the once-good import, and 'failed'
// frees the idempotency key so re-importing the snapshot's file restores
// the analysis rows (the snapshot survives every rollback by design).
// Best-effort: if even this write fails (DB down on top of the IO
// fault), the already-returned error told the operator everything.
func (s *Service) markRollbackTorn(ctx context.Context, b FeeBatch, cause error) {
	stranded, _ := quarantinedFiles(s.analysisDir(), b.ID)
	root := rollbackQuarantineRoot(s.analysisDir(), b.ID)
	where := make([]string, 0, len(stranded))
	for _, p := range stranded {
		where = append(where, strings.TrimPrefix(p.quarantine, root+string(filepath.Separator)))
	}
	detail := "无"
	if len(where) > 0 {
		detail = strings.Join(where, ", ")
	}
	_ = s.db.FeeBatches.MarkFailed(ctx, b.ID, b.RowsTotal, b.RowsFailed,
		fmt.Sprintf("回滚中断（%s）：分析行已不完整；滞留隔离区待 GC 清扫：%s；快照保留，可重导恢复", cause, detail))
}

// EvidenceRow is the API-facing read-back of one source row from a batch's
// immutable snapshot (#86) — the third tier of the evidence chain: a clue or
// import-error cites an anchor (sourceFile + sourceRow), and THIS resolves
// the anchor to the original text without any filesystem access by the
// operator or AI.
type EvidenceRow struct {
	BatchID      string   `json:"batchId"`
	SourceFile   string   `json:"sourceFile"`
	OriginalName string   `json:"originalName"`
	FeeType      string   `json:"feeType"`
	Year         int      `json:"year"`
	Month        int      `json:"month"`
	Row          int      `json:"row"`
	Header       []string `json:"header"`
	Cells        []string `json:"cells"`
}

// EvidenceRow reads one row of a batch's snapshot back (#86). row is the
// source file's own numbering (header = 1) — exactly the source_row anchors
// every clue and import-error detail carries, so an operator (or the MCP
// fee_risks consumer) can go from anchor to original text in one call. The
// snapshot survives rollback by design, so evidence stays readable for
// rolled-back batches too. Works on every build (pure file read, no engine).
func (s *Service) EvidenceRow(ctx context.Context, batchID string, row int) (EvidenceRow, error) {
	b, err := s.db.FeeBatches.GetByID(ctx, batchID)
	if err != nil {
		return EvidenceRow{}, fmt.Errorf("fees: 批次 %s: %w", batchID, err)
	}
	if row < 1 {
		return EvidenceRow{}, fmt.Errorf("fees: 行号必须 ≥ 1（源文件自身编号，表头 = 1）")
	}
	if b.SnapshotPath == "" {
		return EvidenceRow{}, fmt.Errorf("fees: 批次 %s 没有快照路径: %w", batchID, domain.ErrNotFound)
	}
	ev, err := ReadSnapshotRow(filepath.Join(s.dir, filepath.FromSlash(b.SnapshotPath)), row)
	if err != nil {
		return EvidenceRow{}, err
	}
	// Arrays are never null (the repo discipline): a header row read back
	// before the header exists (a blank row 1), or a blank target row,
	// still answers [] arrays.
	if ev.Header == nil {
		ev.Header = []string{}
	}
	if ev.Cells == nil {
		ev.Cells = []string{}
	}
	return EvidenceRow{
		BatchID:      b.ID,
		SourceFile:   SnapshotSourceFile(b.WorkbookName, b.SHA256),
		OriginalName: b.WorkbookName,
		FeeType:      string(b.FeeType),
		Year:         b.FeeYear,
		Month:        b.FeeMonth,
		Row:          row,
		Header:       ev.Header,
		Cells:        ev.Cells,
	}, nil
}

// gcOrphanGrace is how long an unaccounted Parquet file or staging dir
// must have been idle before the sweep may delete it (#106-C). The two
// trigger placements (serve startup before the listener, the manual
// `netopsfees gc` subcommand) share no process with an in-flight HTTP
// import — but the fees CLI is a separate process that MAY be importing
// while serve starts: a batch's Parquet files are renamed into their
// live partitions BEFORE the batch row flips to completed, so a graceless
// sweep could delete a just-renamed file of a losing or in-flight batch
// and silently lose rows. The mtime milestone keeps anything freshly
// written on the KEEP side; real orphans (crash debris) are older than
// the window by the time anyone sweeps, so the grace costs collection
// latency only for debris that crashed within the last hour.
const gcOrphanGrace = time.Hour

// GCAnalysis removes the analysis layer's orphans: Parquet files whose
// batch-id is not a completed batch (crash leftovers, rolled-back
// relicts), and stale working dirs of the fees tree — the #106 staging
// trees and the #107 rollback quarantines (a quarantine of a
// non-completed batch is a rolled-back batch's leftover or a torn
// rollback's stranded remains). Returns the counts; deletedStaging
// covers both working-dir namespaces (the CLI's analysisStaging field).
// The keep-set is exactly the completed batch ids — the conservative
// direction is KEEP: an unaccounted FRESH file is a possible in-flight
// import of another process, and the milestone keeps it until the next
// sweep — #106. A completed batch's working dirs are never debris
// either: its staging tree is mid-write, and a torn rollback's
// quarantine (row still completed) is adoption evidence the next
// Rollback call restores before proceeding.
func (s *Service) GCAnalysis(ctx context.Context) (deletedFiles, deletedStaging int, err error) {
	live, err := s.db.FeeBatches.ListParquetLiveBatchIDs(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("fees: GC: %w", err)
	}
	root := filepath.Join(s.analysisDir(), "fees")
	var doomed []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil //nolint — unreadable trees are skipped, GC is best-effort
		}
		if d.IsDir() {
			// The working subtrees belong to the dedicated sweeps below:
			// staging files are mid-write by definition and the #107
			// quarantine is judged at DIR grain (a torn attempt's tree is
			// all-or-nothing; a completed batch's tree is adoption
			// evidence — a file-level delete would strip it and litter
			// empty mirror dirs), so the walk must not even look at
			// either (pre-#106 the ".staging" name check here was dead
			// code — directories returned one line above).
			if d.Name() == ".staging" || d.Name() == quarantineDirName {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if len(name) > 6 && name[:6] == "batch-" && filepath.Ext(name) == ".parquet" {
			id := strings.TrimSuffix(strings.TrimPrefix(name, "batch-"), ".parquet")
			if _, keep := live[id]; keep {
				return nil
			}
			if !entryIdle(d, gcOrphanGrace) {
				return nil // fresh write: possibly another process mid-import
			}
			doomed = append(doomed, path)
		}
		return nil
	})
	if err != nil {
		return 0, 0, fmt.Errorf("fees: GC 扫描: %w", err)
	}
	if err := RemoveParquetFiles(doomed); err != nil {
		return 0, 0, fmt.Errorf("fees: GC: %w", err)
	}
	// stale working dirs — the #106 staging trees and the #107 rollback
	// quarantines, judged by the same rules: every per-batch dir whose
	// batch is not completed AND that has been idle past the grace
	// window is removed whole (an in-flight import's staging dir / a
	// freshly torn rollback's quarantine is seconds old; real debris is
	// not). The keep-set always wins over the milestone.
	deletedStaging += s.sweepIdleBatchDirs(filepath.Join(root, ".staging"), live)
	deletedStaging += s.sweepIdleBatchDirs(filepath.Join(root, quarantineDirName), live)
	return len(doomed), deletedStaging, nil
}

// sweepIdleBatchDirs sweeps one working-dir namespace of the fees tree
// (.staging, the #107 .rollback quarantine): every per-batch dir whose
// batch is not in the live keep-set and has been idle past the grace
// window is removed whole. An unreadable namespace reads as empty (GC
// is best-effort); the milestone keeps fresh dirs (possibly another
// process mid-import, possibly a rollback torn moments ago).
func (s *Service) sweepIdleBatchDirs(root string, live map[string]struct{}) (removed int) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, keep := live[e.Name()]; keep {
			continue
		}
		if !entryIdle(e, gcOrphanGrace) {
			continue
		}
		if os.RemoveAll(filepath.Join(root, e.Name())) == nil {
			removed++
		}
	}
	return removed
}

// entryIdle reports whether the entry was last touched at least grace ago.
// An unreadable mtime reads as fresh (the conservative side of every GC
// judgment here is KEEP).
func entryIdle(d os.DirEntry, grace time.Duration) bool {
	info, err := d.Info()
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) >= grace
}

// helpers

func renderImportErrors(errs []*ImportError) []string {
	const cap = 20 // bounded: the full detail lives in the snapshot rows
	out := make([]string, 0, min(len(errs), cap))
	for i, e := range errs {
		if i == cap {
			out = append(out, fmt.Sprintf("…另有 %d 个行错误", len(errs)-cap))
			break
		}
		out = append(out, e.Error())
	}
	return out
}

func firstOr(ss []string, def string) string {
	if len(ss) == 0 {
		return def
	}
	return ss[0]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// relPathOf converts an analysis-layer ABSOLUTE file path into the
// logical forward-slash relative form the summary's files[] carries (the
// cross-platform contract of #75 — the disk path joins the OS separator,
// the contract path never does).
func relPathOf(analysisDir, abs string) string {
	rel, err := filepath.Rel(analysisDir, abs)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	return path.Join(filepath.ToSlash(rel))
}

func filePaths(base string, files []partitionFile) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		// relPath is the logical ('/') contract; the disk join converts
		// once at the boundary (#75).
		out = append(out, filepath.Join(base, filepath.FromSlash(f.relPath)))
	}
	return out
}

// ListLinks returns the fee-site links (newest first) with the
// repo-level filters.
func (s *Service) ListLinks(ctx context.Context, f repo.FeeLinkFilter) ([]domain.FeeSiteLink, error) {
	return s.db.FeeSiteLinks.List(ctx, f)
}

// ConfirmLink human-confirms a pending fuzzy proposal (pending →
// confirmed, source human). The partial unique index fires when another
// confirmed link already holds the code — ErrConflict semantics.
func (s *Service) ConfirmLink(ctx context.Context, id string) (domain.FeeSiteLink, error) {
	now := s.clock()
	if err := s.db.Do(ctx, func(tx *repo.Tx) error {
		return tx.FeeSiteLinks.Confirm(ctx, id, now)
	}); err != nil {
		return domain.FeeSiteLink{}, fmt.Errorf("fees: 确认关联 %s: %w", id, err)
	}
	return s.db.FeeSiteLinks.GetByID(ctx, id)
}

// DismissLink human-dismisses a pending fuzzy proposal (audit kept).
func (s *Service) DismissLink(ctx context.Context, id string) (domain.FeeSiteLink, error) {
	now := s.clock()
	if err := s.db.Do(ctx, func(tx *repo.Tx) error {
		return tx.FeeSiteLinks.Dismiss(ctx, id, now)
	}); err != nil {
		return domain.FeeSiteLink{}, fmt.Errorf("fees: 驳回关联 %s: %w", id, err)
	}
	return s.db.FeeSiteLinks.GetByID(ctx, id)
}

// QueryTotals runs the per-site totals preset against the service's own
// analysis dir (#120): the MCP face's entry into the aggregate half of
// the fee domain. The dir knowledge stays in ONE place — the HTTP face
// joins dataDir/analysis itself (unchanged), the MCP face goes through
// here and never learns a path. Validation mirrors Risks: the closed
// fee-type enum, the period bounds, then the honest engine gate.
func (s *Service) QueryTotals(ctx context.Context, engine QueryEngine, feeType FeeType, year, month, limit int) ([]TotalsRow, error) {
	if _, err := ParseFeeType(string(feeType)); err != nil {
		return nil, err
	}
	if !validQueryPeriod(year, month) {
		return nil, fmt.Errorf("fees: 账期 %04d-%02d 不在合法范围", year, month)
	}
	if limit < 1 {
		return nil, fmt.Errorf("fees: limit 必须是 ≥ 1 的整数，收到 %d", limit)
	}
	if !engine.Available() {
		return nil, ErrEngineNotBuilt
	}
	return engine.Totals(ctx, s.analysisDir(), feeType, year, month, limit)
}

// QueryDuplicates runs the cross-batch duplicate-settlement preset the
// same way (#120) — candidates with per-batch counters, never payment-
// duplicate verdicts (the same 账单存在 ≠ 认定结论 doctrine).
func (s *Service) QueryDuplicates(ctx context.Context, engine QueryEngine, feeType FeeType, year, month, limit int) ([]DuplicateRow, error) {
	if _, err := ParseFeeType(string(feeType)); err != nil {
		return nil, err
	}
	if !validQueryPeriod(year, month) {
		return nil, fmt.Errorf("fees: 账期 %04d-%02d 不在合法范围", year, month)
	}
	if limit < 1 {
		return nil, fmt.Errorf("fees: limit 必须是 ≥ 1 的整数，收到 %d", limit)
	}
	if !engine.Available() {
		return nil, ErrEngineNotBuilt
	}
	return engine.Duplicates(ctx, s.analysisDir(), feeType, year, month, limit)
}

// The clue-decision ledger's pagination bounds — PROJECT_BRIEF §9 (limit
// defaults to 20, clamps at 100), the same contract as the attribute
// change list.
const (
	clueDecisionDefaultPageSize = 20
	clueDecisionMaxPageSize     = 100
)

// DecideClue records one human decision on a computed risk clue (#119,
// migration v33) — verify (confirmed real, stays visible as the
// known-issues ledger) or dismiss (ruled out, suppressed from the
// default output and counted in the envelope). Re-deciding flips the
// status and refreshes decided_at (the human may change their mind as
// evidence evolves); the clue identity must be a scenario the registry
// knows + a valid fee type + a legal period — the same validation the
// Risks entry point applies. Human channel only: the MCP face gains no
// write tool (the attribute-changes/links governance).
func (s *Service) DecideClue(ctx context.Context, d domain.RiskClueDecision) (domain.RiskClueDecision, error) {
	if _, err := ParseRiskScenario(d.Scenario); err != nil {
		return domain.RiskClueDecision{}, err
	}
	if _, err := ParseFeeType(string(d.FeeType)); err != nil {
		return domain.RiskClueDecision{}, err
	}
	if _, err := domain.ParseClueDecisionStatus(string(d.Status)); err != nil {
		return domain.RiskClueDecision{}, err
	}
	if d.SiteCode == "" {
		return domain.RiskClueDecision{}, fmt.Errorf("fees: 线索裁定缺站址编码")
	}
	if !validQueryPeriod(d.Year, d.Month) {
		return domain.RiskClueDecision{}, fmt.Errorf("fees: 账期 %04d-%02d 不在合法范围", d.Year, d.Month)
	}
	d.DecidedAt = s.clock()
	if err := s.db.Do(ctx, func(tx *repo.Tx) error {
		saved, err := tx.RiskClueDecisions.Decide(ctx, d)
		if err == nil {
			d = saved
		}
		return err
	}); err != nil {
		return domain.RiskClueDecision{}, fmt.Errorf("fees: 线索裁定 %s/%s/%s: %w", d.Scenario, d.FeeType, d.SiteCode, err)
	}
	return d, nil
}

// ListClueDecisions reads the decision ledger (newest first) — the audit
// view of the human verification loop. Pagination follows the §9 bounds.
func (s *Service) ListClueDecisions(ctx context.Context, f repo.ClueDecisionFilter) ([]domain.RiskClueDecision, int, error) {
	if f.Limit <= 0 {
		f.Limit = clueDecisionDefaultPageSize
	}
	if f.Limit > clueDecisionMaxPageSize {
		f.Limit = clueDecisionMaxPageSize
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	if f.Scenario != "" {
		if _, err := ParseRiskScenario(f.Scenario); err != nil {
			return nil, 0, err
		}
	}
	if f.Status != "" {
		if _, err := domain.ParseClueDecisionStatus(f.Status); err != nil {
			return nil, 0, err
		}
	}
	if f.FeeType != "" {
		if _, err := ParseFeeType(f.FeeType); err != nil {
			return nil, 0, err
		}
	}
	items, err := s.db.RiskClueDecisions.List(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.db.RiskClueDecisions.Count(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}
