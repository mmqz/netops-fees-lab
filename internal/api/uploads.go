package api

// uploads.go — the ONE staging primitive every HTTP upload channel uses to
// land user bytes on disk. Three channels ride it (#112 deduplicated the
// family into this one place — before it, the resource, metrics and fee
// channels each grew their own copy of the same write): the resource
// workbook channel (imports.go), the metrics channel (metrics.go) and the
// fee staging channel (fees.go stageFeeUpload, whose #106-A fix is the
// pattern this helper extracted). The byte-level sibling outside this
// package is internal/fees WriteSnapshot's ".snap-*" copy + rename (it
// streams from a source file rather than an in-memory buffer, so it
// cannot share this helper — same doctrine, same per-call temp name).
//
// The invariant every caller needs: the content-addressed target name
// (<sha256>_<original name>) must never be observable with partial bytes.
// The analyze/ingest steps RE-READ the stored path and record its hash as
// the batch's evidence SHA256; a reader that catches the file mid-write
// records a hash that does not match the bytes actually imported — the
// evidence hash must never be able to lie (#106-A fixed it for the fee
// channel, #112 for the resource and metrics channels: same race, same
// shape, same fix, now one implementation).

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// storeUpload stages data on disk under the content-addressed name
// <sha256>_<name> inside dir (created when missing) and returns the final
// path. Identical content replaces itself safely: concurrent same-content
// submissions each write their own temp and RENAME over the target —
// rename is atomic per file, so the racers replace each other with whole
// identical files and no reader ever observes a partial one. (Both the
// resource and metrics channels used to write in place with O_TRUNC: one
// request's truncate+write interleaved with the other request's read-back,
// so the analyze/ingest step could hash a torn file and record a lying
// evidence SHA256, and the GC's base-name matching could then "prove" a
// file the recorded hash never described.)
//
// The ".tmp-*" temp name sits INSIDE dir (rename cannot cross
// filesystems) and is unique per request. An interrupted temp never
// enters any reference set — workbook_path records only the final
// content-addressed name — so the GC sweep over the uploads area
// (internal/app/gc.go) collects never-referenced entries, interrupted
// temps included, as orphan crash debris: the same doctrine the fee
// staging area already had (#106-B). Failure at any step removes the
// temp; the target name is only ever created by a completed write (which
// also self-heals a torn file an earlier crash may have left at the
// target name).
func storeUpload(dir string, data []byte, name string) (string, error) {
	sum := sha256.Sum256(data)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建上传目录: %w", err)
	}
	// Unique per request: concurrent submissions of the same content each
	// stage their own copy and never share write state.
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return "", fmt.Errorf("创建上传临时文件: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("保存上传文件: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("保存上传文件: %w", err)
	}
	// CreateTemp opens 0600; every data file this tree writes is 0644.
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("保存上传文件: %w", err)
	}
	path := filepath.Join(dir, hex.EncodeToString(sum[:])+"_"+name)
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("保存上传文件: %w", err)
	}
	return path, nil
}
