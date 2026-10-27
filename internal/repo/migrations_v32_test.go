package repo

import (
	"context"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
	"path/filepath"
	"strings"
	"testing"
)

// withoutV32 temporarily unregisters v32 from the migration registry so a
// test can build a genuine v31 database first (the withoutV31 pattern).
func withoutV32(t *testing.T) {
	t.Helper()
	restored := append([]Migration(nil), migrations...)
	filtered := make([]Migration, 0, len(migrations))
	for _, m := range migrations {
		if m.Version <= 31 {
			filtered = append(filtered, m)
		}
	}
	migrations = filtered
	t.Cleanup(func() { migrations = restored })
}

// TestMigrationV32NormalizesCheckpointPaths pins the #80 legacy rewrite:
// a v31 database carrying Windows-written backslash checkpoint paths is
// normalized to the logical ('/') contract on reopen, slash-shaped rows
// pass through byte-for-byte, the version advances to 32, and a second
// reopen (idempotency) changes nothing. The legacy rows are inserted via
// raw SQL because the Go write side already emits the logical form and
// could not produce them — the same evidence shape as v31's test.
func TestMigrationV32NormalizesCheckpointPaths(t *testing.T) {
	dir := testsupport.TempDir(t)
	path := filepath.Join(dir, "netops.db")

	// Phase 1: a genuine v31 database with one workspace, one legacy
	// backslash marker row (a Windows-written database) and one clean
	// slash row (a POSIX-written database).
	withoutV32(t)
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open at v31: %v", err)
	}
	ctx := context.Background()
	seed := `INSERT INTO workspaces (id, name, description, status, source_json, created_at, updated_at)
VALUES ('ws-legacy', 'legacy', '', 'active', '{}', '2026-10-20T12:00:00Z', '2026-10-20T12:00:00Z');
INSERT INTO checkpoints (id, workspace_id, label, file_path, workspace_hash, created_at)
VALUES
('cp-1', 'ws-legacy', 'windows-written', 'checkpoints\ws-legacy\cp-1.db', 'dead01', '2026-10-20T12:00:00Z'),
('cp-2', 'ws-legacy', 'posix-written', 'checkpoints/ws-legacy/cp-2.db', 'dead02', '2026-10-20T12:00:00Z')`
	if _, err := db.sql.ExecContext(ctx, seed); err != nil {
		t.Fatalf("seed legacy rows: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close at v31: %v", err)
	}

	// Phase 2: restore the registry and reopen — v32 applies.
	migrations = append(append([]Migration(nil), migrations...), migrationV32)
	db, err = Open(path)
	if err != nil {
		t.Fatalf("reopen with v32: %v", err)
	}
	defer func() { _ = db.Close() }()

	var version int
	if err := db.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("max version: %v", err)
	}
	if version != 32 {
		t.Fatalf("MAX(version) = %d, want 32", version)
	}

	for _, c := range []struct{ id, want string }{
		{"cp-1", "checkpoints/ws-legacy/cp-1.db"},
		{"cp-2", "checkpoints/ws-legacy/cp-2.db"},
	} {
		var got string
		if err := db.sql.QueryRowContext(ctx, `SELECT file_path FROM checkpoints WHERE id = ?`, c.id).Scan(&got); err != nil {
			t.Fatalf("read %s: %v", c.id, err)
		}
		if got != c.want {
			t.Errorf("file_path of %s = %q, want %q", c.id, got, c.want)
		}
		if strings.Contains(got, `\`) {
			t.Errorf("file_path of %s = %q still contains a backslash", c.id, got)
		}
	}

	// Phase 3: idempotency — a second reopen (the migration re-checks as
	// applied) leaves the rows byte-identical.
	if err := db.Close(); err != nil {
		t.Fatalf("close after v32: %v", err)
	}
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second reopen: %v", err)
	}
	defer func() { _ = db2.Close() }()
	var got string
	if err := db2.sql.QueryRowContext(ctx, `SELECT file_path FROM checkpoints WHERE id = 'cp-1'`).Scan(&got); err != nil {
		t.Fatalf("reread cp-1: %v", err)
	}
	if got != "checkpoints/ws-legacy/cp-1.db" {
		t.Errorf("file_path after second reopen = %q, want the normalized form (idempotent)", got)
	}
}
