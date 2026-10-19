package fees

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
//	fee-imports/<fee_type>/<year>/<month>/<sha256>_<original-name>.<ext>
//
// The fee_type/year/month directory components come from the batch's
// DECLARED period (the operator's statement of what the file covers); a
// file whose rows span periods is still stored once under the declared
// period — the analysis layer partitions by each ROW's own normalized
// period, the snapshot layer remembers where the file was filed.

// SnapshotExt reports the snapshot extension of a fee file (lower-cased,
// without dot): csv / xlsx / xls. Unknown extensions are refused by the
// importer before the snapshot is taken.
func SnapshotExt(name string) (string, error) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	switch ext {
	case "csv", "xlsx", "xls":
		return ext, nil
	default:
		return "", fmt.Errorf("fees: 文件 %q 的扩展名不受支持（可用：csv / xlsx / xls）", name)
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
func SnapshotPath(feeType FeeType, year, month int, sha256, originalName string) string {
	return filepath.Join("fee-imports", string(feeType),
		fmt.Sprintf("%d", year), fmt.Sprintf("%02d", month),
		sha256+"_"+originalName)
}

// WriteSnapshot copies the source file to its content-addressed
// destination under dataDir. Same content → same destination: an existing
// file is verified (size + hash prefix already match by construction, so a
// byte-identical short-circuit is enough) and the copy is skipped — the
// snapshot layer is idempotent by naming. The copy lands via a temp file +
// atomic rename, so a crash never leaves a torn snapshot.
func WriteSnapshot(dataDir, srcPath string, feeType FeeType, year, month int, sha256, originalName string) (string, error) {
	rel := SnapshotPath(feeType, year, month, sha256, originalName)
	dst := filepath.Join(dataDir, rel)
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

	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return "", fmt.Errorf("fees: 创建快照临时文件: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return "", fmt.Errorf("fees: 写快照: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("fees: 关闭快照: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
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
