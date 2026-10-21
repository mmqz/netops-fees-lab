package fees

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
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

// ImportOptions is one import request: the file, the declared fee type
// and period (the operator's statement), and the confirmed core-column
// mapping (target field → source header). When Mapping is empty the
// recognizer's suggestion is used directly (the unattended path for
// well-formed files); a suggestion missing siteCode or amount refuses —
// the human mapping channel is the fallback, never a guess.
type ImportOptions struct {
	Path string
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
	BatchID          string   `json:"batchId"`
	Status           string   `json:"status"`
	WorkbookName     string   `json:"workbookName"`
	SHA256           string   `json:"sha256"`
	FeeType          string   `json:"feeType"`
	FeeYear          int      `json:"feeYear"`
	FeeMonth         int      `json:"feeMonth"`
	RowsTotal        int      `json:"rowsTotal"`
	RowsImported     int      `json:"rowsImported"`
	RowsFailed       int      `json:"rowsFailed"`
	Files            []string `json:"files"`
	Errors           []string `json:"errors,omitempty"`
	SkippedDuplicate bool     `json:"skippedDuplicate"`
	ExistingBatch    string   `json:"existingBatch,omitempty"`
}

// Import runs the full pipeline for one file. The order of operations is
// the crash-safety design:
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
	name := opts.OriginalName
	if name == "" {
		name = filepath.Base(opts.Path)
	}
	if _, err := SnapshotExt(name); err != nil {
		return ImportSummary{}, fmt.Errorf("%w: %v", ErrImportRefused, err)
	}
	headers, rows, err := ReadFeeFile(opts.Path)
	if err != nil {
		// #104: a bad file shape is the operator's to fix — 422 class, not a
		// server fault. The reader's texts are the CLI contract already.
		return ImportSummary{}, fmt.Errorf("%w: %v", ErrImportRefused, err)
	}

	// 2. mapping resolution (explicit mapping wins; recognizer fills the
	// gaps; siteCode + amount are mandatory).
	mapping := opts.Mapping
	if mapping == nil {
		_, suggested := Recognize(headers)
		mapping = suggested
	}
	if mapping["siteCode"] == "" || mapping["amount"] == "" {
		return ImportSummary{}, refusef("缺少必需列映射（siteCode / amount）——请提供映射 JSON 或修正表头；识别到的表头：%v", headers)
	}
	canonical := FeeMapping{FeeType: opts.FeeType, Columns: mapping}.CanonicalMapping()

	// 3. pre-flight idempotency.
	existing, preflightErr := s.db.FeeBatches.CompletedByIdempotencyKey(ctx, sha, opts.FeeType, opts.Year, opts.Month, canonical)
	if preflightErr != nil && !errors.Is(preflightErr, domain.ErrNotFound) {
		// #104: a real DB failure used to read as "no duplicate" and the
		// import proceeded — the idempotency guarantee silently void.
		return ImportSummary{}, fmt.Errorf("fees: 幂等性预检: %w", preflightErr)
	}
	if preflightErr == nil {
		return ImportSummary{
			BatchID: existing.ID, Status: "completed", WorkbookName: existing.WorkbookName,
			SHA256: sha, FeeType: string(opts.FeeType), FeeYear: opts.Year, FeeMonth: opts.Month,
			RowsTotal: existing.RowsTotal, RowsImported: existing.RowsImported, RowsFailed: existing.RowsFailed,
			SkippedDuplicate: true, ExistingBatch: existing.ID,
		}, &ErrDuplicateImport{BatchID: existing.ID}
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
	if err := s.db.Do(ctx, func(tx *repo.Tx) error {
		return tx.FeeBatches.Create(ctx, FeeBatch{
			ID: batchID, WorkbookName: name, SHA256: sha, FeeType: opts.FeeType,
			FeeYear: opts.Year, FeeMonth: opts.Month, SnapshotPath: snapRel, Mapping: canonical,
			Status: FeeStatusFailed, CreatedAt: s.clock(),
		})
	}); err != nil {
		return ImportSummary{}, fmt.Errorf("fees: 记录导入批次: %w", err)
	}

	// 5. normalize.
	siteIdx := headerIndex(headers, mapping["siteCode"])
	amountIdx := headerIndex(headers, mapping["amount"])
	periodIdx := headerIndex(headers, mapping["period"])
	dateIdx := headerIndex(headers, mapping["date"])
	if siteIdx < 0 || amountIdx < 0 {
		errMsg := fmt.Sprintf("映射列不在表头中：siteCode=%q amount=%q（表头：%v）", mapping["siteCode"], mapping["amount"], headers)
		_ = s.db.FeeBatches.MarkFailed(ctx, batchID, len(rows), 0, errMsg)
		return ImportSummary{}, refusef("%s", errMsg)
	}

	sourceFile := SnapshotSourceFile(name, sha)
	normalized := make([]NormalizedRow, 0, len(rows))
	var importErrs []*ImportError
	for _, row := range rows {
		nr, ierr := s.normalizeRow(row, siteIdx, amountIdx, periodIdx, dateIdx, opts, sourceFile, batchID)
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

	// 6. Parquet partitions (staged → atomic rename).
	files, err := writeParquetPartitions(s.analysisDir(), opts.FeeType, batchID, normalized)
	if err != nil {
		_ = s.db.FeeBatches.MarkFailed(ctx, batchID, len(rows), 0, "Parquet 写入失败: "+err.Error())
		return ImportSummary{}, fmt.Errorf("fees: 批次 %s: %w", batchID, err)
	}

	// 7. commit point.
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
			existing, gerr := s.db.FeeBatches.CompletedByIdempotencyKey(ctx, sha, opts.FeeType, opts.Year, opts.Month, canonical)
			if gerr == nil {
				return ImportSummary{
					BatchID: existing.ID, Status: "completed", SHA256: sha,
					FeeType: string(opts.FeeType), FeeYear: opts.Year, FeeMonth: opts.Month,
					RowsTotal: existing.RowsTotal, RowsImported: existing.RowsImported,
					RowsFailed: existing.RowsFailed, SkippedDuplicate: true, ExistingBatch: existing.ID,
				}, &ErrDuplicateImport{BatchID: existing.ID}
			}
		}
		return ImportSummary{}, fmt.Errorf("fees: 提交批次 %s: %w", batchID, err)
	}

	relFiles := make([]string, 0, len(files))
	for _, f := range files {
		relFiles = append(relFiles, f.relPath)
	}
	return ImportSummary{
		BatchID: batchID, Status: "completed", WorkbookName: name, SHA256: sha,
		FeeType: string(opts.FeeType), FeeYear: opts.Year, FeeMonth: opts.Month,
		RowsTotal: len(rows), RowsImported: len(normalized), RowsFailed: len(importErrs),
		Files: relFiles, Errors: errs,
	}, nil
}

// normalizeRow converts one source row into a NormalizedRow. Period
// resolution order: the row's own period column → the row's date column →
// the batch's declared period. Amount and site code are mandatory; the
// period column, when mapped and non-empty, MUST parse (a garbage period
// is a row failure, not a silent fallback — the partition grain is the
// whole point).
func (s *Service) normalizeRow(row SourceRow, siteIdx, amountIdx, periodIdx, dateIdx int, opts ImportOptions, sourceFile, batchID string) (NormalizedRow, *ImportError) {
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
	return NormalizedRow{
		SiteCode: code, AmountCents: amountCents, Year: year, Month: month,
		SourceFile: sourceFile, SourceRow: row.Number, ImportBatch: batchID,
	}, nil
}

// List returns the fee batches (newest first), optionally filtered by
// status ("" = all).
func (s *Service) List(ctx context.Context, status FeeStatus) ([]FeeBatch, error) {
	return s.db.FeeBatches.List(ctx, status)
}

// Rollback reverses a completed fee batch: the Parquet files are deleted
// (the analysis rows disappear), the snapshot STAYS (evidence chain — the
// same asymmetry as the resource importer's batch rollback keeping the
// workbook), and the batch row flips to rolled_back (terminal audit).
// Idempotent: rolling back a rolled-back batch answers ErrConflict.
func (s *Service) Rollback(ctx context.Context, id string) (FeeBatch, error) {
	b, err := s.db.FeeBatches.GetByID(ctx, id)
	if err != nil {
		return FeeBatch{}, fmt.Errorf("fees: 回滚批次 %s: %w", id, err)
	}
	if b.Status != FeeStatusCompleted {
		return FeeBatch{}, fmt.Errorf("fees: 回滚批次 %s: 状态 %s，仅 completed 可回滚: %w", id, b.Status, domain.ErrConflict)
	}
	files, err := BatchParquetFiles(s.analysisDir(), id)
	if err != nil {
		return FeeBatch{}, fmt.Errorf("fees: 回滚批次 %s: %w", id, err)
	}
	if err := RemoveParquetFiles(files); err != nil {
		return FeeBatch{}, fmt.Errorf("fees: 回滚批次 %s: %w", id, err)
	}
	if err := s.db.Do(ctx, func(tx *repo.Tx) error {
		return tx.FeeBatches.MarkRolledBack(ctx, id)
	}); err != nil {
		return FeeBatch{}, fmt.Errorf("fees: 回滚批次 %s: %w", id, err)
	}
	return s.db.FeeBatches.GetByID(ctx, id)
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

// GCAnalysis removes the analysis layer's orphans: Parquet files whose
// batch-id is not a completed batch (crash leftovers, rolled-back
// relicts), and stale staging dirs. Returns the counts. The keep-set is
// exactly the completed batch ids — the conservative direction is KEEP
// (an unaccounted file is reported, not deleted, when its batch row is
// missing entirely… no: a file whose batch row vanished IS an orphan by
// definition — the row is the commit record. Delete.)
func (s *Service) GCAnalysis(ctx context.Context) (deletedFiles, deletedStaging int, err error) {
	live, err := s.db.FeeBatches.ListParquetLiveBatchIDs(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("fees: GC: %w", err)
	}
	root := filepath.Join(s.analysisDir(), "fees")
	var doomed []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil //nolint — unreadable trees are skipped, GC is best-effort
		}
		if d.Name() == ".staging" {
			return nil
		}
		name := d.Name()
		if len(name) > 6 && name[:6] == "batch-" && filepath.Ext(name) == ".parquet" {
			id := strings.TrimSuffix(strings.TrimPrefix(name, "batch-"), ".parquet")
			if _, keep := live[id]; !keep {
				doomed = append(doomed, path)
			}
		}
		return nil
	})
	if err != nil {
		return 0, 0, fmt.Errorf("fees: GC 扫描: %w", err)
	}
	if err := RemoveParquetFiles(doomed); err != nil {
		return 0, 0, fmt.Errorf("fees: GC: %w", err)
	}
	// stale staging dirs: every staging dir whose batch is not live
	staging := filepath.Join(root, ".staging")
	entries, derr := os.ReadDir(staging)
	if derr == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if _, keep := live[e.Name()]; !keep {
				if os.RemoveAll(filepath.Join(staging, e.Name())) == nil {
					deletedStaging++
				}
			}
		}
	}
	return len(doomed), deletedStaging, nil
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
