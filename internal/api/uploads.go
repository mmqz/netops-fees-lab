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
//
// #126 closed the residual Windows window #124's first fix left open
// (still 4/5 failing): the "accept the winner's file" fallback could
// itself lose a transient race — an os.Open/os.Stat of the target while
// ANOTHER racer's MoveFileEx replace is mid-flight answers a sharing
// violation, the probe reads as "no winner", and the request answers a
// lying 500 for a race the design itself created. The staging of one
// content-addressed path is now serialized in-process by a striped
// per-path mutex (same-process concurrency is the only scenario a serve
// process has) and the residual cross-process window carries a bounded
// rename-or-probe retry with backoff — the same tolerance doctrine
// internal/fees' rollback rename adopted (e8c69d4).

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// uploadStripeCount sizes the striped lock table: staging of two different
// content-addressed paths may proceed in parallel; only exact-path racers
// (the same bytes, by construction of the name) serialize. 64 stripes keep
// the collision probability between unrelated uploads negligible while the
// table stays a fixed allocation.
const uploadStripeCount = 64

var uploadStripes [uploadStripeCount]sync.Mutex

// uploadStripe returns the stripe that serializes the staging of one
// content-addressed target path.
func uploadStripe(path string) *sync.Mutex {
	sum := sha256.Sum256([]byte(path))
	return &uploadStripes[sum[0]%uploadStripeCount]
}

// storeUploadRetryBudget bounds the rename-or-probe retry of the residual
// cross-process staging race (#126): a foreign process's MoveFileEx or open
// handle on the target is transient by nature — the budget outlasts any
// realistic sharing window by orders of magnitude while keeping a truly
// wedged filesystem (read-only volume and the like) answering in seconds.
const storeUploadRetryBudget = 2 * time.Second

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
// submissions each write their own ".tmp-*" temp and RENAME over the
// target — atomic per file on POSIX, so the racers replace each other with
// whole identical files and no reader ever observes a partial one. On
// Windows (#124/#126) rename-over-an-open-target answers ACCESS_DENIED
// instead, and the PROBE of the winner's file can itself transiently fail
// while yet another racer's replace is mid-flight — so the staging of one
// target path runs under a striped in-process mutex, starts with the
// idempotent fast path (the winner's bytes already landed — accepting
// them IS the semantics the content-addressed name promises), and keeps a
// bounded rename-or-probe retry for the cross-process window. (Both the
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

	// The staging of one content-addressed path is serialized in-process
	// (#126): same-path racers are always staging IDENTICAL bytes (the
	// name is the hash), so the loser's work under the stripe reduces to
	// the idempotent fast path — no second rename, no probe racing a
	// concurrent MoveFileEx, no sharing-violation window at all.
	mu := uploadStripe(path)
	mu.Lock()
	defer mu.Unlock()

	// Fast path: the winner already landed the exact bytes (same-process
	// racer under the stripe, or an earlier identical submission) —
	// accepting them is the idempotency the content-addressed name
	// promises; the full-hash probe never passes torn crash debris.
	if uploadTargetMatches(path, int64(len(data)), sum) {
		_ = os.Remove(tmp.Name())
		return path, nil
	}

	// Fresh target (or torn debris an earlier crash left): rename into
	// place. A cross-process racer may transiently hold the target —
	// retry the rename and the winner-probe within a bounded budget
	// before answering a real error.
	deadline := time.Now().Add(storeUploadRetryBudget)
	backoff := time.Millisecond
	for {
		if err := os.Rename(tmp.Name(), path); err == nil {
			return path, nil
		} else if uploadTargetMatches(path, int64(len(data)), sum) {
			// #124: on Windows, rename-over-a-target-that-another-
			// request-is-reading answers ACCESS_DENIED — the POSIX
			// "rename replaces atomically" assumption this staging
			// design rode does not hold there. In the concurrent
			// same-content race the winner already landed the exact
			// bytes under this content-addressed name: accepting the
			// winner's file IS the idempotent semantics the name
			// promises (and it never answers a lying 500 to the
			// operator for a race the design itself created).
			_ = os.Remove(tmp.Name())
			return path, nil
		} else if time.Now().After(deadline) {
			_ = os.Remove(tmp.Name())
			return "", fmt.Errorf("保存上传文件: %w", err)
		}
		time.Sleep(backoff)
		if backoff *= 2; backoff > 50*time.Millisecond {
			backoff = 50 * time.Millisecond
		}
	}
}
