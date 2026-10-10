package repo

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"modernc.org/sqlite"
)

// Path returns the database file path as passed to Open (or OpenReadOnly).
// The checkpoint machinery uses it to place snapshot files relative to the
// lab DB directory (PROJECT_BRIEF_P2 §4: <dbdir>/checkpoints/<ws>/<cp>.db).
func (d *DB) Path() string { return d.path }

// Backup writes a byte-consistent snapshot of the whole database to dstPath
// using the SQLite Online Backup API — never a raw file copy of the live
// database (PROJECT_BRIEF_P2 §1.6). It borrows one pooled connection, reaches
// the driver's unexported conn through (*sql.Conn).Raw and asserts the
// anonymous NewBackup interface; the backup then runs with a single Step(-1)
// loop and Finish() (P2-0 verified the API surface of modernc.org/sqlite
// v1.60.1). The parent directory of dstPath is created first. The source
// connection keeps serving reads and writes while the backup runs (that is
// the point of the online API).
func (d *DB) Backup(ctx context.Context, dstPath string) error {
	if dir := filepath.Dir(dstPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("repo: backup to %s: create directory %s: %w", dstPath, dir, err)
		}
	}
	conn, err := d.sql.Conn(ctx)
	if err != nil {
		return fmt.Errorf("repo: backup to %s: acquire connection: %w", dstPath, err)
	}
	defer conn.Close() // returns the conn to the pool, never fails the backup
	err = conn.Raw(func(dc any) error {
		driverConn, ok := dc.(interface {
			NewBackup(dstURI string) (*sqlite.Backup, error)
		})
		if !ok {
			return fmt.Errorf("driver connection %T does not implement NewBackup (modernc.org/sqlite Online Backup API)", dc)
		}
		bck, err := driverConn.NewBackup(dstPath)
		if err != nil {
			return fmt.Errorf("open backup destination: %w", err)
		}
		// Step(-1) copies all remaining pages; the loop guards the
		// "true, nil ⇒ more pages remain" contract of the API.
		for {
			more, err := bck.Step(-1)
			if err != nil {
				_ = bck.Finish()
				return fmt.Errorf("backup step: %w", err)
			}
			if !more {
				break
			}
		}
		if err := bck.Finish(); err != nil {
			return fmt.Errorf("backup finish: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("repo: backup to %s: %w", dstPath, err)
	}
	return nil
}

// OpenReadOnly opens an existing snapshot database in read-only mode and
// wires the same repositories as Open. It is the checkpoint-restore reader
// (PROJECT_BRIEF_P2 §4): the DSN carries mode=ro and busy_timeout only — no
// journal_mode pragma (the file is read-only) and NO migrations (snapshots
// are produced by Backup of an already-migrated database, so they are at the
// current schema version by construction). The connection is pinged eagerly
// so a missing or unreadable file fails HERE, not at the first query.
// Attempted writes fail at the SQLite level (mode=ro) — read methods behave
// exactly like on a writable DB.
func OpenReadOnly(path string) (*DB, error) {
	// dsnOf percent-encodes the path (AUD1-7): a snapshot directory
	// containing '?' or '#' must not truncate the DSN and drop mode=ro.
	dsn := dsnOf(path, "mode=ro&_pragma=busy_timeout(5000)")
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("repo: open read-only %s: %w", path, err)
	}
	// database/sql connects lazily; ping now so a missing file errors at open.
	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("repo: open read-only %s: connect: %w", path, err)
	}
	db := &DB{sql: sqlDB, path: path}
	wire(db, sqlDB)
	return db, nil
}
