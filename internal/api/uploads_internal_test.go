package api

// The #124 fallback predicate: when rename-over-target fails the way it
// does on Windows (ERROR_ACCESS_DENIED while another request reads the
// file the concurrent same-content submission landed), storeUpload must
// decide whether the target already holds the staged bytes. The decision
// is pure and gets its own unit pin — the trigger itself is Windows file
// semantics these Linux tests can only simulate at the predicate level.

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestUploadTargetMatches(t *testing.T) {
	dir := t.TempDir()
	data := []byte("the exact staged bytes, 逐字节一致")
	sum := sha256.Sum256(data)

	target := filepath.Join(dir, hex.EncodeToString(sum[:])+"_账本.xlsx")

	// 1. missing target — the honest race loser with no winner yet.
	if uploadTargetMatches(target, int64(len(data)), sum) {
		t.Fatal("missing target must not match")
	}

	// 2. identical bytes at the target — the #124 winner's file: the
	// loser accepts it (content-addressed idempotency, not a 500).
	if err := os.WriteFile(target, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if !uploadTargetMatches(target, int64(len(data)), sum) {
		t.Fatal("identical target must match — the concurrent winner's file is the answer")
	}

	// 3. same size, wrong bytes — torn debris must NEVER pass as
	// "already staged" (the evidence hash must not be able to lie).
	wrong := make([]byte, len(data))
	for i := range wrong {
		wrong[i] = byte('x')
	}
	if err := os.WriteFile(target, wrong, 0o644); err != nil {
		t.Fatal(err)
	}
	if uploadTargetMatches(target, int64(len(data)), sum) {
		t.Fatal("same-size wrong-bytes target must not match (torn debris)")
	}

	// 4. wrong size — cheap gate refuses before any hashing.
	if err := os.WriteFile(target, data[:len(data)/2], 0o644); err != nil {
		t.Fatal(err)
	}
	if uploadTargetMatches(target, int64(len(data)), sum) {
		t.Fatal("short target must not match")
	}

	// 5. target is a directory — IsRegular refuses the shape.
	_ = os.Remove(target)
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if uploadTargetMatches(target, int64(len(data)), sum) {
		t.Fatal("a directory at the target name must not match")
	}
}

// TestStatusRecorderFlushAndUnwrap pins the two zero-covered plumbing
// methods of statusRecorder: Flush must forward to the wrapped writer's
// http.Flusher (or no-op safely when absent), and Unwrap must expose the
// wrapped writer so http.ResponseController can probe its capabilities —
// the embedded-interface shortcut alone does not provide Unwrap, and a
// streaming endpoint (SSE-style progress) silently degrades without it.
func TestStatusRecorderFlushAndUnwrap(t *testing.T) {
	// httptest.ResponseRecorder implements http.Flusher.
	rec := httptest.NewRecorder()
	sr := &statusRecorder{ResponseWriter: rec}
	sr.WriteHeader(200)

	f, ok := interface{}(sr).(http.Flusher)
	if !ok {
		t.Fatalf("statusRecorder does not implement http.Flusher")
	}
	f.Flush() // must not panic; forwards to the recorder

	if got := sr.Unwrap(); got != rec {
		t.Fatalf("Unwrap() = %v, want the wrapped ResponseWriter", got)
	}

	// A bare writer without Flusher: Flush must no-op, not panic.
	sr2 := &statusRecorder{ResponseWriter: bareWriter{w: rec}}
	sr2.Flush()
}

type bareWriter struct{ w io.Writer }

func (b bareWriter) Header() http.Header         { return http.Header{} }
func (b bareWriter) Write(p []byte) (int, error) { return b.w.Write(p) }
func (b bareWriter) WriteHeader(int)             {}
