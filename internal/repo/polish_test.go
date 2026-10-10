package repo

// v0.2.1 polish tests (AUD-1 P2 batch): numeric link ordering, the
// MarkReverted status guard, DSN paths with URI-special characters, and the
// concurrent-writer stress of AUD1-4's transaction hardening.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
)

// TestSimLinkListNumericOrder pins the NUMERIC id order of
// SimLinkRepo.ListByWorkspace (AUD1-13): plain `ORDER BY id` is
// lexicographic and would answer ln-1, ln-10, ln-2.
func TestSimLinkListNumericOrder(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedWorkspace(t, db, "ws_num", "numeric", "")

	err := db.Do(ctx, func(tx *Tx) error {
		for _, d := range []string{"vd-1", "vd-2"} {
			if err := tx.VirtualDevices.Insert(ctx, newDevice(d, "ws_num", "DEV-"+d[3:], domain.EntityStatusUp)); err != nil {
				return err
			}
		}
		for _, i := range []string{"if-1-eth0", "if-1-eth1", "if-1-eth2", "if-1-eth3", "if-1-eth4", "if-1-eth5"} {
			if err := tx.SimInterfaces.Insert(ctx, newIface(i, "ws_num", "vd-1", "eth"+i[5:], domain.EntityStatusUp)); err != nil {
				return err
			}
		}
		for _, i := range []string{"if-2-eth0", "if-2-eth1", "if-2-eth2", "if-2-eth3", "if-2-eth4", "if-2-eth5"} {
			if err := tx.SimInterfaces.Insert(ctx, newIface(i, "ws_num", "vd-2", "eth"+i[5:], domain.EntityStatusUp)); err != nil {
				return err
			}
		}
		// ln-1, ln-2, ln-10: lexicographic order would put ln-10 second.
		for n, pair := range map[int][2]string{
			1:  {"if-1-eth0", "if-2-eth0"},
			2:  {"if-1-eth1", "if-2-eth1"},
			10: {"if-1-eth4", "if-2-eth4"},
		} {
			if err := tx.SimLinks.Insert(ctx, newLink(
				domain.FormatSimID(domain.SeqLink, n), "ws_num", pair[0], pair[1], domain.EntityStatusUp, "L")); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	links, err := db.SimLinks.ListByWorkspace(ctx, "ws_num")
	if err != nil {
		t.Fatalf("ListByWorkspace: %v", err)
	}
	want := []string{"ln-1", "ln-2", "ln-10"}
	if len(links) != len(want) {
		t.Fatalf("ListByWorkspace = %d links, want %d", len(links), len(want))
	}
	for i, id := range want {
		if links[i].ID != id {
			t.Fatalf("links[%d].ID = %q, want %q (numeric order, not lexicographic)", i, links[i].ID, id)
		}
	}
}

// TestMarkRevertedStatusGuard pins the applied → reverted transition guard
// (AUD1-8): marking an already-reverted or failed changeset answers
// domain.ErrConflict and never overwrites reverted_at; an unknown changeset
// still answers domain.ErrNotFound.
func TestMarkRevertedStatusGuard(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedWorkspace(t, db, "ws_guard", "guard", "")
	at := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)

	insert := func(id string, status domain.ChangeSetStatus) {
		t.Helper()
		if err := db.ChangeSets.Insert(ctx, domain.ChangeSet{
			ID: id, WorkspaceID: "ws_guard", Trigger: domain.ChangeSetTriggerUser,
			Status: status, OpsApplied: 0, CreatedAt: at,
		}, nil); err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	insert("cs-applied", domain.ChangeSetStatusApplied)
	insert("cs-reverted", domain.ChangeSetStatusReverted)
	insert("cs-failed", domain.ChangeSetStatusFailed)

	// Applied → reverted works and is recorded.
	if err := db.ChangeSets.MarkReverted(ctx, "cs-applied", at.Add(time.Hour)); err != nil {
		t.Fatalf("MarkReverted(applied): %v", err)
	}
	got, err := db.ChangeSets.GetByID(ctx, "cs-applied")
	if err != nil || got.Status != domain.ChangeSetStatusReverted {
		t.Fatalf("cs-applied after mark = %+v (%v), want reverted", got, err)
	}

	// Guard: an already-reverted changeset refuses re-marking.
	if err := db.ChangeSets.MarkReverted(ctx, "cs-applied", at.Add(2*time.Hour)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("MarkReverted(already reverted) = %v, want domain.ErrConflict", err)
	}
	got, _ = db.ChangeSets.GetByID(ctx, "cs-applied")
	if !got.RevertedAt.Equal(at.Add(time.Hour)) {
		t.Fatalf("reverted_at was overwritten: %v, want %v", got.RevertedAt, at.Add(time.Hour))
	}
	// Guard: a failed changeset is never marked reverted.
	if err := db.ChangeSets.MarkReverted(ctx, "cs-failed", at); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("MarkReverted(failed) = %v, want domain.ErrConflict", err)
	}
	if got, _ := db.ChangeSets.GetByID(ctx, "cs-reverted"); got.Status != domain.ChangeSetStatusReverted {
		t.Fatalf("pre-reverted row changed: %+v", got)
	}
	// Unknown id keeps the ErrNotFound semantics.
	if err := db.ChangeSets.MarkReverted(ctx, "cs-99", at); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("MarkReverted(unknown) = %v, want domain.ErrNotFound", err)
	}
}

// TestOpenPathWithSpecialCharacters pins the DSN path encoding (AUD1-7): a
// database path containing spaces, '?' and '#' opens, persists and re-opens
// (read-only) — without the encoding the '?' truncated the DSN and dropped
// every pragma.
func TestOpenPathWithSpecialCharacters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "scratch dir?with#hash")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(dir, "net ops#1.db")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	ctx := context.Background()
	if err := db.Workspaces.Insert(ctx, domain.Workspace{
		ID: "ws-special", Name: "special", Status: domain.WorkspaceStatusActive,
		SourceJSON: "{}", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	// WAL mode must be active even on the encoded path (the pragma survived).
	var mode string
	if err := db.sql.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal (DSN pragmas must survive the encoded path)", mode)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The file exists at the RAW path (SQLite percent-decoded the URI).
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database file missing at the raw path %q: %v", path, err)
	}

	// Read-only open (the checkpoint-snapshot reader) works on the same path.
	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("OpenReadOnly(%q): %v", path, err)
	}
	t.Cleanup(func() { _ = ro.Close() })
	if _, err := ro.Workspaces.GetByID(ctx, "ws-special"); err != nil {
		t.Fatalf("GetByID over the read-only handle: %v", err)
	}
}

// TestDoConcurrentWorkloads exercises the transaction hardening of AUD1-4
// (BEGIN IMMEDIATE via _txlock, bounded busy retry in Do): several goroutines
// run mixed read→write workloads — Do transactions that first read state and
// then write (exactly the runChecksWith/Materialize shape that used to fail
// with SQLITE_BUSY on the deferred read→write upgrade) — plus plain Do
// writes, and everything must complete without a single error. NO -race (2
// cores, database-heavy; TASK_PROTOCOL §5).
func TestDoConcurrentWorkloads(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedWorkspace(t, db, "ws-conc", "concurrent", "")
	// Seed the sequence rows the workload allocates from (Allocate seeds
	// idempotently with a conflict retry, but do it up front to keep the
	// racing goroutines out of the seeding INSERT).
	if err := db.Do(ctx, func(tx *Tx) error {
		_, err := tx.Sequences.Allocate(ctx, domain.SeqLink)
		if err != nil {
			return err
		}
		_, err = tx.Sequences.Allocate(ctx, domain.SeqDevice)
		return err
	}); err != nil {
		t.Fatalf("seed sequences: %v", err)
	}

	const goroutines = 8
	const iterations = 10

	var wg sync.WaitGroup
	errs := make(chan error, goroutines*iterations*2)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				// Read→write upgrade shape: read state first (the typed repo
				// read), then a write (the sequence UPDATE) — the exact
				// pattern that hit SQLITE_BUSY with deferred BEGIN.
				var n int
				if err := db.Do(ctx, func(tx *Tx) error {
					existing, err := tx.SimLinks.ListByWorkspace(ctx, "ws-conc")
					if err != nil {
						return err
					}
					if len(existing) < 0 {
						t.Errorf("impossible link count")
					}
					v, err := tx.Sequences.Allocate(ctx, domain.SeqLink)
					if err != nil {
						return err
					}
					n = v
					return nil
				}); err != nil {
					errs <- err
					return
				}
				_ = n
				// Plain write transaction.
				if err := db.Do(ctx, func(tx *Tx) error {
					_, err := tx.Sequences.Allocate(ctx, domain.SeqDevice)
					return err
				}); err != nil {
					errs <- err
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent workload error (no SQLITE_BUSY should escape Do): %v", err)
	}
}

// TestDoRollbackFailureKeepsOriginalError pins the Do() error semantics of
// AUD1-6: when the rollback itself fails, the ORIGINAL error stays the
// wrapped cause (errors.Is keeps resolving domain sentinels — the API keeps
// answering 404/409/422 instead of 500) while the rollback failure is only
// quoted. Deterministic trigger: canceling the transaction's context makes
// database/sql's watcher roll the tx back on its own, so the explicit
// Rollback in Do answers ErrTxDone.
func TestDoRollbackFailureKeepsOriginalError(t *testing.T) {
	db := newTestDB(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	original := fmt.Errorf("load entity: %w", domain.ErrNotFound)
	err := db.Do(ctx, func(tx *Tx) error {
		cancel()
		// Give database/sql's context watcher the time to roll the
		// transaction back (it fires on ctx.Done; 100ms is ample on a
		// 2-core box, and the assertion below tolerates the winner race).
		time.Sleep(100 * time.Millisecond)
		return original
	})
	if err == nil {
		t.Fatalf("Do: err = nil, want the original error (with a failed rollback note)")
	}
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("errors.Is(err, domain.ErrNotFound) = false — the original sentinel must survive the failed rollback: %v", err)
	}
	if !strings.Contains(err.Error(), "load entity") {
		t.Fatalf("Do error = %q, want the original error text preserved", err.Error())
	}
	if !strings.Contains(err.Error(), "rollback also failed") {
		t.Errorf("Do error = %q, want the rollback failure quoted (v0.2.0 lost the original error entirely here)", err.Error())
	}
}
