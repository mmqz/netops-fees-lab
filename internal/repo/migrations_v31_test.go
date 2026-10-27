package repo

import (
	"context"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
	"path/filepath"
	"strings"
	"testing"
)

// withoutV31 temporarily unregisters v31 from the migration registry so a
// test can build a genuine v30 database first (the withoutV12 pattern).
func withoutV31(t *testing.T) {
	t.Helper()
	restored := append([]Migration(nil), migrations...)
	filtered := make([]Migration, 0, len(migrations))
	for _, m := range migrations {
		if m.Version <= 30 {
			filtered = append(filtered, m)
		}
	}
	migrations = filtered
	t.Cleanup(func() { migrations = restored })
}

// TestMigrationV31NormalizesFeeSnapshotPaths pins the #75 legacy rewrite:
// a v30 database carrying Windows-written backslash snapshot paths is
// normalized to the logical ('/') contract on reopen, slash-shaped rows
// pass through byte-for-byte, the version advances to 31, and a second
// reopen (idempotency) changes nothing.
func TestMigrationV31NormalizesFeeSnapshotPaths(t *testing.T) {
	dir := testsupport.TempDir(t)
	path := filepath.Join(dir, "netops.db")

	// Phase 1: a genuine v30 database with legacy backslash rows plus a
	// clean slash row. The backslash rows are inserted via raw SQL — the
	// Go write side already emits the logical form and could not produce
	// them.
	withoutV31(t)
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open at v30: %v", err)
	}
	ctx := context.Background()
	seed := `INSERT INTO fee_batches
(id, workbook_name, sha256, fee_type, fee_year, fee_month, snapshot_path, mapping, status, rows_total, rows_imported, rows_failed, error, created_at, completed_at)
VALUES
('fee_win', 'bills.csv', 'aa01', 'electricity', 2024, 5, 'fee-imports\electricity\2024\05\aa01_bills.csv', '{}', 'completed', 3, 3, 0, NULL, '2026-10-19T12:00:00Z', '2026-10-19T12:00:00Z'),
('fee_posix', 'bills.csv', 'bb02', 'electricity', 2024, 6, 'fee-imports/electricity/2024/06/bb02_bills.csv', '{}', 'completed', 2, 2, 0, NULL, '2026-10-19T12:00:00Z', '2026-10-19T12:00:00Z')`
	if _, err := db.sql.ExecContext(ctx, seed); err != nil {
		t.Fatalf("seed legacy rows: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close at v30: %v", err)
	}

	// Phase 2: restore the registry and reopen — v31 applies.
	migrations = append(append([]Migration(nil), migrations...), migrationV31)
	db, err = Open(path)
	if err != nil {
		t.Fatalf("reopen with v31: %v", err)
	}
	defer func() { _ = db.Close() }()

	var version int
	if err := db.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("max version: %v", err)
	}
	if version != 31 {
		t.Fatalf("MAX(version) = %d, want 31", version)
	}

	for _, c := range []struct{ id, want string }{
		{"fee_win", "fee-imports/electricity/2024/05/aa01_bills.csv"},
		{"fee_posix", "fee-imports/electricity/2024/06/bb02_bills.csv"},
	} {
		var got string
		if err := db.sql.QueryRowContext(ctx, `SELECT snapshot_path FROM fee_batches WHERE id = ?`, c.id).Scan(&got); err != nil {
			t.Fatalf("read %s: %v", c.id, err)
		}
		if got != c.want {
			t.Errorf("snapshot_path of %s = %q, want %q", c.id, got, c.want)
		}
		if strings.Contains(got, `\`) {
			t.Errorf("snapshot_path of %s = %q still contains a backslash", c.id, got)
		}
	}

	// Phase 3: idempotency — a second reopen (the migration re-checks as
	// applied) leaves the rows byte-identical.
	if err := db.Close(); err != nil {
		t.Fatalf("close after v31: %v", err)
	}
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second reopen: %v", err)
	}
	defer func() { _ = db2.Close() }()
	var got string
	if err := db2.sql.QueryRowContext(ctx, `SELECT snapshot_path FROM fee_batches WHERE id = 'fee_win'`).Scan(&got); err != nil {
		t.Fatalf("reread fee_win: %v", err)
	}
	if got != "fee-imports/electricity/2024/05/aa01_bills.csv" {
		t.Errorf("snapshot_path after second reopen = %q, want the normalized form (idempotent)", got)
	}
}
