package fees

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// The SNAPSHOT LAYER (#60): every imported fee file is copied, byte for
// byte, to a content-addressed, immutable location under
// <dataDir>/fee-imports/. The path is the evidence anchor every analysis
// row points back at (source_file + source_row); the original file is
// never modified, and a re-import of the same content hits the same path
// (dedup by construction — the hash IS the name).
//
// Layout (the #60 sketch):
//
//      fee-imports/<fee_type>/<year>/<month>/<sha256>_<original-name>.<ext>
//
// The fee_type/year/month directory components come from the batch's
// DECLARED period (the operator's statement of what the file covers); a
// file whose rows span periods is still stored once under the declared
// period — the analysis layer partitions by each ROW's own normalized
// period, the snapshot layer remembers where the file was filed.

// SnapshotExt reports the snapshot extension of a fee file (lower-cased,
// without dot): csv / xlsx. Unknown extensions — including .xls, the
// pre-2007 binary format no supported reader can parse (#92) — are refused
// by the importer before the snapshot is taken.
func SnapshotExt(name string) (string, error) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	switch ext {
	case "csv", "xlsx":
		return ext, nil
	case "xls":
		return "", fmt.Errorf("fees: .xls 是 Excel 2003 二进制旧格式，不受支持——请用 Excel 另存为 .xlsx（或导出 .csv）后重试：%q", name)
	default:
		return "", fmt.Errorf("fees: 文件 %q 的扩展名不受支持（可用：csv / xlsx）", name)
	}
}

// HashFile reads the file and returns its hex SHA-256 (streamed — fee
// files can be hundreds of MB; no full in-memory copy needed for hashing).
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("fees: 打开文件 %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("fees: 计算文件哈希 %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// SnapshotPath builds the content-addressed snapshot destination (relative
// to the data dir) for a file of the given hash, name and declared period.
// The base name carries BOTH the hash and the original name — the
// evidence reader sees the original filename without opening the batch
// record, and the hash prefix keeps names unique.
//
// The return value is a LOGICAL path (path.Join: always '/') — it is a
// cross-platform data contract, not a local filesystem path: it is
// persisted to fee_batches.snapshot_path, returned by the HTTP API
// (snapshotPath) and printed by the CLI (#75). A database and data dir
// copied from Windows to Linux (or back) must resolve identically, and
// the API must not change shape with the server's OS. Consumers that
// touch the disk convert once via filepath.FromSlash at the join point
// (WriteSnapshot below); filepath.Join here would leak the platform
// separator into the contract.
func SnapshotPath(feeType FeeType, year, month int, sha256, originalName string) string {
	return path.Join("fee-imports", string(feeType),
		fmt.Sprintf("%d", year), fmt.Sprintf("%02d", month),
		sha256+"_"+originalName)
}

// WriteSnapshot copies the source file to its content-addressed
// destination under dataDir. Same content → same destination: an existing
// file is verified (size + hash prefix already match by construction, so a
// byte-identical short-circuit is enough) and the copy is skipped — the
// snapshot layer is idempotent by naming. The copy lands via a temp file +
// atomic rename, so a crash never leaves a torn snapshot. The temp name is
// UNIQUE per call (#106): two concurrent imports of the same file both
// miss the Stat fast-path and would otherwise share one O_TRUNC temp —
// interleaved truncate+copy can leave zero holes that no writer comes
// back to fill, and the "atomic" rename would then commit the torn file
// under the evidence hash's own name. (This is the byte-level sibling of
// the HTTP channels' staging primitive — internal/api storeUpload, #112
// deduplicated that side of the family; it streams from a source file
// rather than an in-memory buffer, so the implementations stay separate
// by necessity, the temp-name + rename doctrine identical.)
func WriteSnapshot(dataDir, srcPath string, feeType FeeType, year, month int, sha256, originalName string) (string, error) {
	rel := SnapshotPath(feeType, year, month, sha256, originalName)
	dst := filepath.Join(dataDir, filepath.FromSlash(rel)) // contract → OS path at the disk boundary
	if _, err := os.Stat(dst); err == nil {
		return rel, nil // already snapshotted (identical content by naming)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", fmt.Errorf("fees: 创建快照目录: %w", err)
	}
	in, err := os.Open(srcPath)
	if err != nil {
		return "", fmt.Errorf("fees: 打开源文件 %s: %w", srcPath, err)
	}
	defer func() { _ = in.Close() }()

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".snap-*")
	if err != nil {
		return "", fmt.Errorf("fees: 创建快照临时文件: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("fees: 写快照: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("fees: 关闭快照: %w", err)
	}
	// CreateTemp opens 0600; snapshots sit next to files this layer
	// writes as 0644, and the operator may read them outside the app.
	if err := os.Chmod(tmpName, 0o644); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("fees: 写快照: %w", err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		_ = os.Remove(tmpName)
		// #124 (the storeUpload sibling race): on Windows, rename over a
		// target another request is reading (the evidence read-back of a
		// concurrent same-content import) answers ACCESS_DENIED. The
		// content-addressed snapshot name means the winner landed the
		// identical bytes — the fast path at the top of this function
		// already trusts exactly that shape, so the loser accepts the
		// winner's file instead of failing the whole import over a race
		// the design itself created.
		if _, statErr := os.Stat(dst); statErr == nil {
			return rel, nil
		}
		return "", fmt.Errorf("fees: 提交快照: %w", err)
	}
	return rel, nil
}

// SnapshotSourceFile is the analysis row's source_file value: the snapshot
// base name (hash + original name) — the analysis row points at the exact
// immutable bytes, not at the operator's local path.
func SnapshotSourceFile(originalName, sha256 string) string {
	return sha256 + "_" + originalName
}

// ---- evidence read-back (#86) ----

// RowEvidence is one snapshot row read back: the header row's trimmed cells
// plus the requested row's raw (untrimmed) cells.
type RowEvidence struct {
	Header []string
	Cells  []string
}

// ReadSnapshotRow streams the snapshot at path up to the requested row and
// returns it (#86): the third tier of the evidence chain — the conclusion
// cites an anchor (source_file + source_row), the anchor resolves HERE to
// the original text. The row number is the SOURCE FILE's own numbering
// (row 1 = the header; blank rows keep their numbers) — exactly the
// numbering readCSV/readXLSX stamp onto every SourceRow, i.e. the
// source_row value every clue anchor and import-error detail carries.
// A row past the end of the file answers an error wrapping
// domain.ErrNotFound (a 404 at the API).
//
// The read is streaming and bounded by the target row: a 100k-row
// snapshot's row 42 costs 42 records, never the whole file.
func ReadSnapshotRow(path string, row int) (RowEvidence, error) {
	if row < 1 {
		return RowEvidence{}, fmt.Errorf("fees: 行号必须 ≥ 1（源文件自身编号，表头 = 1）")
	}
	ext, err := SnapshotExt(path)
	if err != nil {
		return RowEvidence{}, err
	}
	switch ext {
	case "csv":
		return readSnapshotRowCSV(path, row)
	default: // "xlsx" — the only other SnapshotExt output
		return readSnapshotRowXLSX(path, row)
	}
}

func readSnapshotRowCSV(path string, row int) (RowEvidence, error) {
	f, err := os.Open(path)
	if err != nil {
		return RowEvidence{}, fmt.Errorf("fees: 打开快照 %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	r := csv.NewReader(f)
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	var ev RowEvidence
	recNo := 0
	for {
		rec, rerr := r.Read()
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return RowEvidence{}, fmt.Errorf("fees: 读取快照 %s 第 %d 行: %w", path, recNo+1, rerr)
		}
		recNo++
		if ev.Header == nil && !isBlankRow(rec) {
			ev.Header = trimAll(rec)
		}
		if recNo == row {
			ev.Cells = rec
			return ev, nil
		}
	}
	return RowEvidence{}, fmt.Errorf("%w: fees: 快照 %s 共 %d 行，第 %d 行不存在",
		domain.ErrNotFound, filepath.Base(path), recNo, row)
}

func readSnapshotRowXLSX(path string, row int) (RowEvidence, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return RowEvidence{}, fmt.Errorf("fees: 打开快照 %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return RowEvidence{}, fmt.Errorf("fees: 快照 %s 没有工作表", path)
	}
	it, err := f.Rows(sheets[0])
	if err != nil {
		return RowEvidence{}, fmt.Errorf("fees: 读取快照工作表 %s: %w", sheets[0], err)
	}
	defer func() { _ = it.Close() }()
	var ev RowEvidence
	rowNum := 0
	for it.Next() {
		rowNum++
		cells, cerr := it.Columns()
		if cerr != nil {
			return RowEvidence{}, fmt.Errorf("fees: 读取快照 %s 第 %d 行: %w", path, rowNum, cerr)
		}
		strs := make([]string, len(cells))
		copy(strs, cells)
		if ev.Header == nil && !isBlankRow(strs) {
			ev.Header = trimAll(strs)
		}
		if rowNum == row {
			ev.Cells = strs
			return ev, nil
		}
	}
	if ierr := it.Error(); ierr != nil {
		return RowEvidence{}, fmt.Errorf("fees: 读取快照 %s: %w", path, ierr)
	}
	return RowEvidence{}, fmt.Errorf("%w: fees: 快照 %s 共 %d 行，第 %d 行不存在",
		domain.ErrNotFound, filepath.Base(path), rowNum, row)
}
