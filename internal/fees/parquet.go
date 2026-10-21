package fees

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"

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
// service.go, wired to serve startup + `netopslab gc` with an mtime
// milestone since #106).

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
// batch-<id>.parquet.
func BatchParquetFiles(analysisDir, batchID string) ([]string, error) {
	root := filepath.Join(analysisDir, "fees")
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint — missing trees are simply empty sets
		}
		if d.IsDir() {
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

// RemoveParquetFiles deletes the given files (rollback). Missing files
// are tolerated (idempotent rollback — a crash mid-rollback retries
// cleanly). A file's partition directory is pruned when it becomes empty
// (an empty hive dir is a dead partition — nothing matches the glob, but
// the tree stays tidy); the walk stops at the first non-empty ancestor.
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
