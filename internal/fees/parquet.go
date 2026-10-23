package fees

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/parquet-go/parquet-go"
)

// The ANALYSIS LAYER writer (#60): the hive-partitioned Parquet table.
//
//      <analysisDir>/fees/fee_type=electricity/year=2024/month=05/batch-<id>.parquet
//
// One file per (batch × row-period): a file spanning periods splits into
// its per-month partitions; re-imports of OTHER files coexist as their own
// batch files in the SAME partition (the "同键多行以批次血缘共存，不静默
// 合并" ruling of #60 — duplicate settlement detection QUERIES across
// them, never merges them). Partition columns (fee_type/year/month) live
// in the PATH only (hive convention — DuckDB's hive_partitioning reads
// them from the directory, so they must not also be file columns); the
// file carries the payload + evidence columns.
//
// Atomicity (#61: no half-written state): every file is written under
// <analysisDir>/fees/.staging/<batch>/… then RENAMED into place (atomic
// per file); the SQLite batch row's completed flip is the commit point.
// Files without a completed batch row are GC orphans by definition (the
// keep-set is exactly the completed batch ids — see GCAnalysis in
// service.go, wired to serve startup + `netopsfees gc` with an mtime
// milestone since #106).
//
// Deletion gets the same discipline (#107): Rollback used to unlink the
// batch's files one by one and flip the row only afterwards — an IO
// fault mid-loop left earlier files already deleted while the row still
// said completed (neither fully rolled back nor fully intact). The fix
// moves the delete set into a per-batch quarantine tree with reversible
// renames BEFORE the flip — see the rollback section at the bottom of
// this file and Service.Rollback.

// feeParquetRow is the on-disk schema of one analysis row. Column names
// are the query surface (DuckDB reads them verbatim); source_row is the
// 1-based row number in the ORIGINAL file — the snapshot layer's evidence
// coordinate. amount_cents is the money value as INTEGER CENTS (#65 P1-a):
// float64 sums drift across aggregation orders and poison reconciliation;
// int64 cents are exact. NOTE: batches imported by pre-#65 builds carry a
// float `amount` column instead — re-import them after upgrading (the
// snapshot layer keeps the originals; a re-import is the same file, same
// idempotency key… delete the old batch first: the key blocks a second
// import of the same file).
type feeParquetRow struct {
	SiteCode    string `parquet:"site_code"`
	AmountCents int64  `parquet:"amount_cents"`
	SourceFile  string `parquet:"source_file"`
	SourceRow   int32  `parquet:"source_row"`
	ImportBatch string `parquet:"import_batch"`
}

// partitionFile is the per-partition manifest entry: the live (relative)
// partition path and the staged file it commits from, plus the row count
// (the counts the batch row records must be verifiable against the
// files).
type partitionFile struct {
	relPath string
	staged  string
	rows    int
}

// writeParquetPartitions writes the normalized rows of one import batch
// into the analysis table. Rows are grouped by their own (year, month)
// period; each group lands as ONE file under the batch's staging dir,
// then all files are atomically renamed into their partition dirs. The
// fee_type partition component is the batch's declared type. Returns the
// per-file manifest (path + row count) in (year, month) order.
func writeParquetPartitions(analysisDir string, feeType FeeType, batchID string, rows []NormalizedRow) ([]partitionFile, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	byPeriod := make(map[[2]int][]feeParquetRow)
	for _, r := range rows {
		key := [2]int{r.Year, r.Month}
		byPeriod[key] = append(byPeriod[key], feeParquetRow{
			SiteCode:    r.SiteCode,
			AmountCents: r.AmountCents,
			SourceFile:  r.SourceFile,
			SourceRow:   int32(r.SourceRow),
			ImportBatch: r.ImportBatch,
		})
	}
	keys := make([][2]int, 0, len(byPeriod))
	for k := range byPeriod {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})

	staging := filepath.Join(analysisDir, "fees", ".staging", batchID)
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return nil, fmt.Errorf("fees: 创建 Parquet 暂存目录: %w", err)
	}

	files := make([]partitionFile, 0, len(keys))
	for _, k := range keys {
		year, month := k[0], k[1]
		// relPath is a LOGICAL path (path.Join: always '/') — it flows into
		// the batch manifest, the HTTP API's files[] and the CLI JSON
		// output, so it is a cross-platform contract like
		// SnapshotPath (#75); the disk join converts via
		// filepath.FromSlash below.
		rel := path.Join("fees",
			fmt.Sprintf("fee_type=%s", string(feeType)),
			fmt.Sprintf("year=%d", year),
			fmt.Sprintf("month=%02d", month),
			"batch-"+batchID+".parquet")
		staged := fmt.Sprintf("year=%d_month=%02d.parquet", year, month)
		if err := writeOneParquet(filepath.Join(staging, staged), byPeriod[k]); err != nil {
			return nil, err
		}
		files = append(files, partitionFile{relPath: rel, staged: staged, rows: len(byPeriod[k])})
	}
	// commit: atomic rename per file into the live partition dirs
	for _, f := range files {
		dst := filepath.Join(analysisDir, filepath.FromSlash(f.relPath)) // contract → OS path
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, fmt.Errorf("fees: 创建分区目录: %w", err)
		}
		if err := os.Rename(filepath.Join(staging, f.staged), dst); err != nil {
			return nil, fmt.Errorf("fees: 提交 Parquet 分区文件: %w", err)
		}
	}
	// best-effort staging cleanup (a crash before this leaves an empty
	// dir — the GC sweeps staging dirs of non-completed batches once
	// past the idle milestone, see service.go)
	_ = os.RemoveAll(staging)
	return files, nil
}

// writeOneParquet writes one staged Parquet file of the given rows.
func writeOneParquet(path string, rows []feeParquetRow) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("fees: 创建 Parquet 文件 %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	w := parquet.NewGenericWriter[feeParquetRow](f)
	if _, err := w.Write(rows); err != nil {
		return fmt.Errorf("fees: 写 Parquet 行: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("fees: 关闭 Parquet 写入器: %w", err)
	}
	return f.Close()
}

// BatchParquetFiles lists the live Parquet files of one batch (the
// rollback delete set): glob every fee_type partition tree for
// batch-<id>.parquet. The fees tree's working namespaces are skipped:
// .staging (mid-write staging — never carries live files) and .rollback
// (the #107 quarantine; a quarantined file is NOT live, and its delete
// slot is derived from the mirrored layout, so it must never leak into
// the delete set it would be moved into).
func BatchParquetFiles(analysisDir, batchID string) ([]string, error) {
	root := filepath.Join(analysisDir, "fees")
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint — missing trees are simply empty sets
		}
		if d.IsDir() {
			if d.Name() == ".staging" || d.Name() == ".rollback" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "batch-"+batchID+".parquet" {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("fees: 扫描批次 %s 的 Parquet 文件: %w", batchID, err)
	}
	return out, nil
}

// RemoveParquetFiles deletes the given files (the GC orphan sweep and
// the import's idempotency-race-lost cleanup — rollback itself does NOT
// unlink files directly anymore, it quarantines them, see the #107
// section below). Missing files are tolerated (idempotent — a crash
// mid-sweep retries cleanly). A file's partition directory is pruned
// when it becomes empty (an empty hive dir is a dead partition — nothing
// matches the glob, but the tree stays tidy); the walk stops at the
// first non-empty ancestor.
func RemoveParquetFiles(files []string) error {
	for _, f := range files {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("fees: 删除 Parquet 文件 %s: %w", f, err)
		}
		pruneEmptyDirs(filepath.Dir(f), 3)
	}
	return nil
}

// pruneEmptyDirs removes dir and its (up to levels) ancestors while they
// are empty; any error (non-empty, concurrent creation) silently stops
// the walk — pruning is hygiene, not correctness.
func pruneEmptyDirs(dir string, levels int) {
	for i := 0; i < levels; i++ {
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// ---- the #107 rollback quarantine ----
//
// Deletion cannot be transactional the way a SQLite row can: a batch's
// Parquet files live in several partition dirs and os.Remove is one
// syscall per file. The pre-#107 rollback looped RemoveParquetFiles and
// aborted at the first error — the files before the fault were already
// UNLINKED on disk while the batch row still said completed: a
// "completed" batch with some of its analysis rows silently missing,
// exactly the audit corruption #107 reports.
//
// The fix keeps every step of the delete reversible until the commit:
// the delete set is MATERIALIZED first (rollbackPlan), then every file
// is RENAMED into a per-batch quarantine tree that mirrors its live
// layout,
//
//      <analysisDir>/fees/.rollback/<batchID>/<fees-relative path>
//
// A rename is metadata-only and same-filesystem by construction: when it
// fails the file is exactly where it was — a failed quarantine step
// leaves nothing behind. Only after the LAST file moved does the caller
// flip the batch row to rolled_back (the single commit point, the same
// shape as the import's step-7); any failure before the flip UNDOES the
// moves, so a failed rollback leaves the batch completed with every row
// intact. The quarantine is invisible to every reader — the query glob
// is exactly three dirs deep (fees/*/*/*/*.parquet), the mirrored
// layout sits five levels below fees/ — and GCAnalysis sweeps stale
// quarantine dirs like stale staging ones (keep-set first: a completed
// batch's quarantine is torn-attempt evidence, adoptable by the next
// Rollback call, never swept).

// quarantineDirName is the fees-subtree namespace of the rollback
// quarantine trees (the delete-side twin of .staging).
const quarantineDirName = ".rollback"

// rollbackPlan is one materialized delete: the live partition file and
// the quarantine slot it moves to. The whole batch's plan is built
// BEFORE any move runs (#107: the delete set is explicit and complete
// when execution starts — no half-discovered delete lists mid-dance).
type rollbackPlan struct {
	live       string
	quarantine string
}

// rollbackQuarantineRoot is the per-batch quarantine tree.
func rollbackQuarantineRoot(analysisDir, batchID string) string {
	return filepath.Join(analysisDir, "fees", quarantineDirName, batchID)
}

// buildRollbackPlan materializes the delete plan: every live file of
// the batch mapped to its mirrored quarantine slot. files are the live
// paths exactly as BatchParquetFiles returned them (fees-rooted).
func buildRollbackPlan(analysisDir, batchID string, files []string) []rollbackPlan {
	feesRoot := filepath.Join(analysisDir, "fees")
	plan := make([]rollbackPlan, 0, len(files))
	for _, f := range files {
		rel := strings.TrimPrefix(f, feesRoot+string(filepath.Separator))
		plan = append(plan, rollbackPlan{
			live:       f,
			quarantine: filepath.Join(rollbackQuarantineRoot(analysisDir, batchID), rel),
		})
	}
	return plan
}

// quarantinedFiles lists a batch's quarantined files with their live
// paths derived back from the mirrored layout — the adoption input when
// a previous attempt was torn by a crash (row still completed, some
// files already quarantined). A missing quarantine tree is the normal
// case and answers an empty plan.
func quarantinedFiles(analysisDir, batchID string) ([]rollbackPlan, error) {
	root := rollbackQuarantineRoot(analysisDir, batchID)
	feesRoot := filepath.Join(analysisDir, "fees")
	var out []rollbackPlan
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// no quarantine at all — the normal entry point
			if os.IsNotExist(err) && path == root {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(path, root+string(filepath.Separator))
		out = append(out, rollbackPlan{live: filepath.Join(feesRoot, rel), quarantine: path})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("fees: 扫描批次 %s 的回滚隔离文件: %w", batchID, err)
	}
	return out, nil
}

// quarantineBackoff is the small bounded backoff between quarantine-rename
// retries (see renameWithRetry). Total worst added latency is 125ms — an
// operator rollback tolerates that; a FAILING rollback does not get better
// by failing faster.
var quarantineBackoff = []time.Duration{25 * time.Millisecond, 100 * time.Millisecond}

// renameWithRetrySeam is os.Rename as a var so tests can inject the
// Windows-only transient (a sharing violation no Linux box can produce
// natively — see TestQuarantineFileRidesTransientRename).
var renameWithRetrySeam = os.Rename

// renameWithRetry renames src→dst with a bounded retry for the Windows
// sharing-violation window (#124's class, rollback edition): on POSIX,
// renaming a file some reader has open Just Works (the reader keeps the
// old inode); on Windows the same rename answers ERROR_SHARING_VIOLATION /
// ACCESS_DENIED while a concurrent fee query's DuckDB read_parquet handle
// holds the live file open. Those queries are millisecond-scale, so a
// couple of tiny-backoff retries rides out the overlap. Rename's other
// failure classes (real faults: permissions, disk state) do not improve
// with retries and pass through after the bounded attempts — the caller's
// abort/undo semantics (#107) then handle them exactly as before. ENOENT
// fails fast with no retry burn: a vanished source is a semantic answer
// (the idempotence dance treats it as success), never a transient.
func renameWithRetry(rename func(string, string) error, backoff []time.Duration, src, dst string) error {
	var err error
	for attempt := 0; ; attempt++ {
		if err = rename(src, dst); err == nil || os.IsNotExist(err) {
			return err
		}
		if attempt >= len(backoff) {
			return err
		}
		time.Sleep(backoff[attempt])
	}
}

// quarantineFile executes one plan step (live → quarantine). A missing
// source is tolerated — the file is already gone, which is where the
// delete wants it (the idempotence RemoveParquetFiles always had); any
// other failure aborts the dance before anything is truly deleted.
func quarantineFile(p rollbackPlan) error {
	if err := os.MkdirAll(filepath.Dir(p.quarantine), 0o755); err != nil {
		return fmt.Errorf("fees: 创建回滚隔离目录: %w", err)
	}
	// renameWithRetry (not the raw os.Rename): on Windows a concurrent
	// fee query can hold the live file open for the milliseconds this
	// dance overlaps with it — the #124 storeUpload race, seen from the
	// rollback side. (The reverse move, restoreQuarantinedFile, renames
	// INTO a slot no query can glob — the quarantine tree sits outside
	// the read_parquet pattern — and the commit rename in
	// writeParquetFiles targets a fresh batch-<id> path, so neither
	// shares this window; both keep their plain os.Rename.)
	if err := renameWithRetry(renameWithRetrySeam, quarantineBackoff, p.live, p.quarantine); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("fees: 隔离 Parquet 文件 %s: %w", p.live, err)
	}
	return nil
}

// restoreQuarantinedFile is quarantineFile's inverse (quarantine →
// live): one undo step. A missing source is a successful undo — the
// slot was already restored or swept elsewhere, and an undo that has
// nothing left to do is exactly what "nothing stays deleted" wants.
func restoreQuarantinedFile(p rollbackPlan) error {
	if err := os.MkdirAll(filepath.Dir(p.live), 0o755); err != nil {
		return fmt.Errorf("fees: 重建分区目录: %w", err)
	}
	if err := os.Rename(p.quarantine, p.live); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("fees: 恢复隔离文件到 %s: %w", p.live, err)
	}
	return nil
}
