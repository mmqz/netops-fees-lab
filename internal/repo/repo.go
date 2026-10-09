// Package repo is the SQLite storage layer of NetOps Lab — the ONLY package
// that speaks SQL (PROJECT_BRIEF §1.6). It opens the database, applies
// ordered idempotent migrations, and exposes typed repositories for every
// aggregate (batches, resources, relations, source refs, row results,
// issues; Phase 2 adds workspaces, sequences, virtual devices, interfaces,
// links, changesets, check runs, checkpoints). Domain structs flow in and
// out; no SQL leaks beyond this package.
//
// All mutations that must be atomic go through DB.Do, which runs a function
// inside one transaction and rolls back on error or panic.
package repo

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registers driver name "sqlite"
)

// DBTX is the minimal query interface shared by *sql.DB (autocommit mode)
// and *sql.Tx (transaction mode). All repositories accept it so the same
// code paths serve both.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// DB is a handle to an opened SQLite database with attached repositories.
// The repo fields run each statement in autocommit mode; use Do for
// multi-statement atomicity.
type DB struct {
	sql *sql.DB
	// path is the database file path as passed to Open/OpenReadOnly;
	// Path exposes it (checkpoint snapshot files live relative to it).
	path string

	Batches    *BatchRepo
	Resources  *ResourceRepo
	Relations  *RelationRepo
	SourceRefs *SourceRefRepo
	RowResults *RowResultRepo
	Issues     *IssueRepo

	// Phase 2 simulation workbench aggregates (DDL v5).
	Workspaces     *WorkspaceRepo
	Sequences      *SequenceRepo
	VirtualDevices *VirtualDeviceRepo
	SimInterfaces  *SimInterfaceRepo
	SimLinks       *SimLinkRepo
	ChangeSets     *ChangeSetRepo
	CheckRuns      *CheckRunRepo
	Checkpoints    *CheckpointRepo
}

// Open opens (or creates) the SQLite database at path, applies all pending
// migrations and returns a ready-to-use DB. The parent directory of path
// must exist. The DSN carries the pragmas mandated by PROJECT_BRIEF §5
// (busy_timeout=5000, journal_mode=WAL, foreign_keys=ON — the latter is
// per-connection, hence repeated on every pooled connection).
func Open(path string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", path)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("repo: open %s: %w", path, err)
	}
	db := &DB{sql: sqlDB, path: path}
	if err := db.applyMigrations(context.Background()); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	wire(db, sqlDB)
	return db, nil
}

// wire attaches the repository set of q (autocommit mode) to db. Open and
// OpenReadOnly (backup.go) share it so both handles expose the same surface.
func wire(db *DB, q DBTX) {
	db.Batches = &BatchRepo{q: q}
	db.Resources = &ResourceRepo{q: q}
	db.Relations = &RelationRepo{q: q}
	db.SourceRefs = &SourceRefRepo{q: q}
	db.RowResults = &RowResultRepo{q: q}
	db.Issues = &IssueRepo{q: q}
	db.Workspaces = &WorkspaceRepo{q: q}
	db.Sequences = &SequenceRepo{q: q}
	db.VirtualDevices = &VirtualDeviceRepo{q: q}
	db.SimInterfaces = &SimInterfaceRepo{q: q}
	db.SimLinks = &SimLinkRepo{q: q}
	db.ChangeSets = &ChangeSetRepo{q: q}
	db.CheckRuns = &CheckRunRepo{q: q}
	db.Checkpoints = &CheckpointRepo{q: q}
}

// Close closes the underlying database handle.
func (d *DB) Close() error {
	return d.sql.Close()
}

// Tx is a transaction-scoped view of the repositories. Every repo call on
// a Tx joins the surrounding transaction; the transaction is committed or
// rolled back by DB.Do — callers never manage BEGIN/COMMIT themselves.
type Tx struct {
	Batches    *BatchRepo
	Resources  *ResourceRepo
	Relations  *RelationRepo
	SourceRefs *SourceRefRepo
	RowResults *RowResultRepo
	Issues     *IssueRepo

	// Phase 2 simulation workbench aggregates (DDL v5).
	Workspaces     *WorkspaceRepo
	Sequences      *SequenceRepo
	VirtualDevices *VirtualDeviceRepo
	SimInterfaces  *SimInterfaceRepo
	SimLinks       *SimLinkRepo
	ChangeSets     *ChangeSetRepo
	CheckRuns      *CheckRunRepo
	Checkpoints    *CheckpointRepo
}

// Do runs fn inside a single transaction. If fn returns an error, the
// transaction is rolled back and the error is returned unchanged. If fn
// panics, the transaction is rolled back and the panic is re-raised. On
// success the transaction is committed. This is the Stage 0 atomicity
// primitive that guarantees no partial writes.
func (d *DB) Do(ctx context.Context, fn func(tx *Tx) error) (err error) {
	sqlTx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("repo: begin transaction: %w", err)
	}
	tx := &Tx{
		Batches:        &BatchRepo{q: sqlTx},
		Resources:      &ResourceRepo{q: sqlTx},
		Relations:      &RelationRepo{q: sqlTx},
		SourceRefs:     &SourceRefRepo{q: sqlTx},
		RowResults:     &RowResultRepo{q: sqlTx},
		Issues:         &IssueRepo{q: sqlTx},
		Workspaces:     &WorkspaceRepo{q: sqlTx},
		Sequences:      &SequenceRepo{q: sqlTx},
		VirtualDevices: &VirtualDeviceRepo{q: sqlTx},
		SimInterfaces:  &SimInterfaceRepo{q: sqlTx},
		SimLinks:       &SimLinkRepo{q: sqlTx},
		ChangeSets:     &ChangeSetRepo{q: sqlTx},
		CheckRuns:      &CheckRunRepo{q: sqlTx},
		Checkpoints:    &CheckpointRepo{q: sqlTx},
	}
	defer func() {
		if p := recover(); p != nil {
			_ = sqlTx.Rollback()
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		if rbErr := sqlTx.Rollback(); rbErr != nil {
			return fmt.Errorf("repo: rollback after error %q failed: %w", err.Error(), rbErr)
		}
		return err
	}
	if err := sqlTx.Commit(); err != nil {
		return fmt.Errorf("repo: commit transaction: %w", err)
	}
	return nil
}
