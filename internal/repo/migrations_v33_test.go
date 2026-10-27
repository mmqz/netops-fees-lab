package repo

import (
	"context"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
	"path/filepath"
	"strings"
	"testing"
)

// withoutV33 temporarily unregisters v33 from the migration registry so a
// test can build a genuine v32 database first (the withoutV31 pattern).
func withoutV33(t *testing.T) {
	t.Helper()
	restored := append([]Migration(nil), migrations...)
	filtered := make([]Migration, 0, len(migrations))
	for _, m := range migrations {
		if m.Version <= 32 {
			filtered = append(filtered, m)
		}
	}
	migrations = filtered
	t.Cleanup(func() { migrations = restored })
}

// TestMigrationV33AddsRiskClueDecisions pins the v33 upgrade: a genuine
// v32 database (the table absent — every access errors) upgrades to the
// decision table on reopen, the version advances to 33, the CHECK
// guards reject out-of-enum scenario/fee_type/status values, the UNIQUE
// identity holds, and a second reopen changes nothing (idempotency).
func TestMigrationV33AddsRiskClueDecisions(t *testing.T) {
	dir := testsupport.TempDir(t)
	path := filepath.Join(dir, "netops.db")

	// Phase 1: a genuine v32 database — the table does not exist yet.
	withoutV33(t)
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open at v32: %v", err)
	}
	ctx := context.Background()
	if _, err := db.sql.ExecContext(ctx, `SELECT COUNT(*) FROM risk_clue_decisions`); err == nil {
		db.Close()
		t.Fatal("risk_clue_decisions already exists at v32 (registry leak)")
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close at v32: %v", err)
	}

	// Phase 2: restore the registry (cleanup holds the full list) and
	// reopen — v33 applies.
	restored := append([]Migration(nil), migrations...) // the withoutV33-filtered list
	migrations = append(restored, migrationV33)
	db, err = Open(path)
	if err != nil {
		t.Fatalf("reopen with v33: %v", err)
	}
	defer func() { _ = db.Close() }()

	var version int
	if err := db.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("max version: %v", err)
	}
	if version != 33 {
		t.Fatalf("MAX(version) = %d, want 33 (this test reopens with v33 only — v34 is deliberately not yet in the registry)", version)
	}

	// the CHECK guards are the last line (the service validates first —
	// these pin the schema's own closed sets).
	for _, bad := range []string{
		`INSERT INTO risk_clue_decisions (id, scenario, fee_type, site_code, year, month, status, note, decided_at)
		 VALUES ('rcd_x', 'bogus-scenario', 'electricity', 'S-1', 2024, 5, 'verified', '', '2026-10-23T00:00:00Z')`,
		`INSERT INTO risk_clue_decisions (id, scenario, fee_type, site_code, year, month, status, note, decided_at)
		 VALUES ('rcd_x', 'spike', 'bogus-type', 'S-1', 2024, 5, 'verified', '', '2026-10-23T00:00:00Z')`,
		`INSERT INTO risk_clue_decisions (id, scenario, fee_type, site_code, year, month, status, note, decided_at)
		 VALUES ('rcd_x', 'spike', 'electricity', 'S-1', 2024, 5, 'bogus-status', '', '2026-10-23T00:00:00Z')`,
		`INSERT INTO risk_clue_decisions (id, scenario, fee_type, site_code, year, month, status, note, decided_at)
		 VALUES ('rcd_x', 'spike', 'electricity', 'S-1', 1999, 5, 'verified', '', '2026-10-23T00:00:00Z')`,
		`INSERT INTO risk_clue_decisions (id, scenario, fee_type, site_code, year, month, status, note, decided_at)
		 VALUES ('rcd_x', 'spike', 'electricity', 'S-1', 2024, 13, 'verified', '', '2026-10-23T00:00:00Z')`,
	} {
		if _, err := db.sql.ExecContext(ctx, bad); err == nil {
			t.Errorf("out-of-enum/range insert must fail: %s", strings.Split(bad, "\n")[0])
		}
	}

	// the UNIQUE identity: the same (scenario, fee_type, site, period)
	// admits exactly one row.
	ins := `INSERT INTO risk_clue_decisions (id, scenario, fee_type, site_code, year, month, status, note, decided_at)
		VALUES ('rcd_a', 'spike', 'electricity', 'S-1', 2024, 5, 'verified', '', '2026-10-23T00:00:00Z')`
	if _, err := db.sql.ExecContext(ctx, ins); err != nil {
		t.Fatalf("seed first decision: %v", err)
	}
	dup := strings.Replace(ins, "'rcd_a'", "'rcd_b'", 1)
	if _, err := db.sql.ExecContext(ctx, dup); err == nil {
		t.Error("duplicate clue identity must fail the UNIQUE constraint")
	}

	// Phase 3: reopen — idempotent, version stays 33.
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen (idempotency): %v", err)
	}
	defer func() { _ = db2.Close() }()
	if err := db2.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("max version (reopen): %v", err)
	}
	if version != 33 {
		t.Fatalf("MAX(version) after reopen = %d, want 33 (idempotent)", version)
	}
}
