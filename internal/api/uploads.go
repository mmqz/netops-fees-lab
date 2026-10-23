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
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// uploadTargetMatches reports whether the content-addressed target path
// already holds exactly the bytes being staged — the Windows-race
// fallback (#124). On POSIX, rename-over-an-open-file replaces it
// atomically; on Windows the same rename answers ERROR_ACCESS_DENIED
// while any other handle (the analyze/ingest read-back of the request
// that WON the race) is open. The content-addressed name means the
// winner staged the IDENTICAL bytes, so losing that race is success,
// not a 500: the size gate is cheap, the full hash re-read closes the
// torn-debris corner (a crash-left file at the target name with a
// matching size but wrong bytes must never pass as "already staged").
func uploadTargetMatches(path string, size int64, wantSHA [sha256.Size]byte) bool {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() != size {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	return bytes.Equal(h.Sum(nil), wantSHA[:])
}

// storeUpload stages data on disk under the content-addressed name
// <sha256>_<name> inside dir (created when missing) and returns the final
// path. Identical content replaces itself safely: concurrent same-content
// submissions each write their own temp and RENAME over the target —
// atomic per file on POSIX, so the racers replace each other with whole
// identical files and no reader ever observes a partial one. On Windows
// (#124) rename-over-an-open-target answers ACCESS_DENIED instead; the
// fallback at the rename site accepts the racing winner's byte-identical
// file (see uploadTargetMatches) so the race resolves to the idempotent
// success the content-addressed name already promised. (Both the
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
		// #124: on Windows, rename-over-a-target-that-another-
		// request-is-reading answers ACCESS_DENIED — the POSIX
		// "rename replaces atomically" assumption this staging
		// design rode does not hold there. In the concurrent
		// same-content race the winner already landed the exact
		// bytes under this content-addressed name: accepting the
		// winner's file IS the idempotent semantics the name
		// promises (and it never answers a lying 500 to the
		// operator for a race the design itself created).
		if uploadTargetMatches(path, int64(len(data)), sum) {
			_ = os.Remove(tmp.Name())
			return path, nil
		}
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("保存上传文件: %w", err)
	}
	return path, nil
}
