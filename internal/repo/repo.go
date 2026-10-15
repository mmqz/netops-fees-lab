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
	"errors"
	"fmt"
	"net/url"
	"time"

	"modernc.org/sqlite"             // pure-Go SQLite driver, registers driver name "sqlite"
	sqlite3 "modernc.org/sqlite/lib" // SQLITE_* result codes
)

// beginBusyAttempts bounds the retry of a transaction BEGIN that returned
// SQLITE_BUSY (AUD1-4): with _txlock=immediate every BEGIN takes the write
// lock up front, so the busy_timeout already absorbed the common contention;
// the retry below is the last line of defense for the rare case where a
// writer held the lock for the whole timeout window.
//
// Issue #39 (audit-v0.8.0 §2.2): three attempts with 50/100/150ms backoff
// (~300ms extra) proved too small a budget under whole-suite load — the
// Windows audit saw SQLITE_BUSY escape when parallel test binaries starved
// a write-lock holder past busy_timeout(5s)+300ms. Six attempts with
// exponential backoff (50ms … 1.6s, ~3.1s extra) keep the worst-case begin
// budget around 6×5s+3s ≈ 33s, which absorbs the load spikes of a machine
// running the full test suite concurrently while still failing fast enough
// for a genuinely stuck writer. Multi-instance concurrency boundary: a
// single process is fully supported; multiple processes sharing one
// database file are supported up to this begin budget (WAL +
// busy_timeout + bounded begin retry) — sustained write-lock hold times
// beyond it surface as SQLITE_BUSY errors, never as corruption.
const beginBusyAttempts = 6

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

	// AttributeChanges stores the attribute change proposals recorded on
	// the importer's duplicate-key skip path (migration v11, W4-D).
	AttributeChanges *AttributeChangeRepo

	// RenameProposals stores the suspected-rename proposals recorded by the
	// importer's not-exists insert path of name-keyed types (migration v12,
	// W6-D).
	RenameProposals *RenameProposalRepo

	// Phase 2 simulation workbench aggregates (DDL v5).
	Workspaces     *WorkspaceRepo
	Sequences      *SequenceRepo
	VirtualDevices *VirtualDeviceRepo
	SimInterfaces  *SimInterfaceRepo
	SimLinks       *SimLinkRepo
	ChangeSets     *ChangeSetRepo
	CheckRuns      *CheckRunRepo
	Checkpoints    *CheckpointRepo

	// Timeseries metric channel (migration v14, W20): the separate
	// metric batch family and the three metric tables.
	MetricBatches *MetricBatchRepo
	Metrics       *MetricRepo

	// Run-state model (migration v17, W22 — §12-B1 方案 C): the
	// resource_status history + the resources snapshot columns.
	ResourceStatus *ResourceStatusRepo

	// Alert detection model (migration v18, W23 — ADR-0011 D2/D3): the
	// detection_rules config table and the alerts state machine.
	DetectionRules *DetectionRuleRepo
	Alerts         *AlertRepo

	// Detection run history (migration v21, v0.11-R11): one receipt row
	// per detection sweep — api / cli / scheduler all land here.
	DetectionRuns *DetectionRunRepo

	// Mapping templates (migration v19, W24 — §12-B7 方案 A): the confirmed
	// per-sheet mapping store of the unattended import channel.
	Templates *TemplateRepo
}

// Open opens (or creates) the SQLite database at path, applies all pending
// migrations and returns a ready-to-use DB. The parent directory of path
// must exist. The DSN carries the pragmas mandated by PROJECT_BRIEF §5
// (busy_timeout=5000, journal_mode=WAL, foreign_keys=ON — the latter is
// per-connection, hence repeated on every pooled connection).
//
// Two storage-layer decisions harden the DSN beyond the pragmas (AUD1-4):
//
//   - the path is percent-encoded as a file: URI path component, so a path
//     containing '?' or '#' cannot truncate the DSN or drop the pragmas
//     (SQLite decodes the escapes when it opens the file), and
//   - every transaction begins IMMEDIATE (_txlock=immediate, honored by
//     modernc.org/sqlite v1.60.1): a BEGIN that takes the write lock up
//     front cannot fail with SQLITE_BUSY on the deferred read→write
//     upgrade, which used to lose check runs when concurrent HTTP requests
//     raced (the pool stays unbounded — readers never block in WAL mode).
func Open(path string) (*DB, error) {
	sqlDB, err := sql.Open("sqlite", dsnOf(path,
		"_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"))
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
	db.AttributeChanges = &AttributeChangeRepo{q: q}
	db.RenameProposals = &RenameProposalRepo{q: q}
	db.Workspaces = &WorkspaceRepo{q: q}
	db.Sequences = &SequenceRepo{q: q}
	db.VirtualDevices = &VirtualDeviceRepo{q: q}
	db.SimInterfaces = &SimInterfaceRepo{q: q}
	db.SimLinks = &SimLinkRepo{q: q}
	db.ChangeSets = &ChangeSetRepo{q: q}
	db.CheckRuns = &CheckRunRepo{q: q}
	db.Checkpoints = &CheckpointRepo{q: q}
	db.MetricBatches = &MetricBatchRepo{q: q}
	db.Metrics = &MetricRepo{q: q}
	db.ResourceStatus = &ResourceStatusRepo{q: q}
	db.DetectionRules = &DetectionRuleRepo{q: q}
	db.Alerts = &AlertRepo{q: q}
	db.DetectionRuns = &DetectionRunRepo{q: q}
	db.Templates = &TemplateRepo{q: q}
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

	// AttributeChanges stores the attribute change proposals recorded on
	// the importer's duplicate-key skip path (migration v11, W4-D).
	AttributeChanges *AttributeChangeRepo

	// RenameProposals stores the suspected-rename proposals recorded by the
	// importer's not-exists insert path of name-keyed types (migration v12,
	// W6-D).
	RenameProposals *RenameProposalRepo

	// Phase 2 simulation workbench aggregates (DDL v5).
	Workspaces     *WorkspaceRepo
	Sequences      *SequenceRepo
	VirtualDevices *VirtualDeviceRepo
	SimInterfaces  *SimInterfaceRepo
	SimLinks       *SimLinkRepo
	ChangeSets     *ChangeSetRepo
	CheckRuns      *CheckRunRepo
	Checkpoints    *CheckpointRepo

	// Timeseries metric channel (migration v14, W20).
	MetricBatches *MetricBatchRepo
	Metrics       *MetricRepo

	// Run-state model (migration v17, W22).
	ResourceStatus *ResourceStatusRepo

	// Alert detection model (migration v18, W23).
	DetectionRules *DetectionRuleRepo
	Alerts         *AlertRepo

	// Detection run history (migration v21, v0.11-R11).
	DetectionRuns *DetectionRunRepo

	// Mapping templates (migration v19, W24).
	Templates *TemplateRepo
}

// dsnOf builds the SQLite DSN of path: a file: URI with the path component
// percent-encoded (spaces, '?', '#', '%' and every other URI-special byte)
// followed by the given query parameters. For plain ASCII paths the encoding
// is the identity, so existing DSNs are byte-identical; a path with a literal
// '?' or '#' would otherwise truncate the DSN and silently drop every pragma
// (SQLite percent-decodes the path when it opens the file — modernc.org/sqlite
// passes file: names to sqlite3_open_v2 whole with SQLITE_OPEN_URI).
func dsnOf(path, query string) string {
	return "file:" + (&url.URL{Path: path}).EscapedPath() + "?" + query
}

// isBusyError reports whether err is an SQLITE_BUSY result of the modernc
// driver — including extended busy codes such as SQLITE_BUSY_SNAPSHOT, whose
// primary code (rc & 0xff) is still SQLITE_BUSY.
func isBusyError(err error) bool {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code()&0xff == sqlite3.SQLITE_BUSY
	}
	return false
}

// beginTx starts a transaction, retrying a bounded number of times when the
// BEGIN returns SQLITE_BUSY (the modernc driver applies the DSN's
// _txlock=immediate, so a busy BEGIN means another pooled connection held
// the write lock for the whole busy_timeout window).
func (d *DB) beginTx(ctx context.Context) (*sql.Tx, error) {
	var lastErr error
	for attempt := 0; attempt < beginBusyAttempts; attempt++ {
		sqlTx, err := d.sql.BeginTx(ctx, nil)
		if err == nil {
			return sqlTx, nil
		}
		lastErr = err
		if !isBusyError(err) {
			return nil, fmt.Errorf("repo: begin transaction: %w", err)
		}
		// Exponential backoff (issue #39): 50, 100, 200, 400, 800ms
		// between attempts — no sleep after the final one.
		if attempt < beginBusyAttempts-1 {
			backoff := time.Duration(50<<attempt) * time.Millisecond
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("repo: begin transaction: %w", ctx.Err())
			case <-time.After(backoff):
			}
		}
	}
	return nil, fmt.Errorf("repo: begin transaction (SQLITE_BUSY, %d attempts): %w", beginBusyAttempts, lastErr)
}

// Do runs fn inside a single transaction. If fn returns an error, the
// transaction is rolled back and the error is returned unchanged — even
// when the rollback itself fails, the ORIGINAL error is preserved as the
// wrapped cause so sentinel checks (domain.ErrNotFound, ErrOpFailed, …)
// keep working. If fn panics, the transaction is rolled back and the panic
// is re-raised. On success the transaction is committed. This is the Stage 0
// atomicity primitive that guarantees no partial writes.
func (d *DB) Do(ctx context.Context, fn func(tx *Tx) error) (err error) {
	sqlTx, err := d.beginTx(ctx)
	if err != nil {
		return err
	}
	tx := &Tx{
		Batches:          &BatchRepo{q: sqlTx},
		Resources:        &ResourceRepo{q: sqlTx},
		Relations:        &RelationRepo{q: sqlTx},
		SourceRefs:       &SourceRefRepo{q: sqlTx},
		RowResults:       &RowResultRepo{q: sqlTx},
		Issues:           &IssueRepo{q: sqlTx},
		AttributeChanges: &AttributeChangeRepo{q: sqlTx},
		RenameProposals:  &RenameProposalRepo{q: sqlTx},
		Workspaces:       &WorkspaceRepo{q: sqlTx},
		Sequences:        &SequenceRepo{q: sqlTx},
		VirtualDevices:   &VirtualDeviceRepo{q: sqlTx},
		SimInterfaces:    &SimInterfaceRepo{q: sqlTx},
		SimLinks:         &SimLinkRepo{q: sqlTx},
		ChangeSets:       &ChangeSetRepo{q: sqlTx},
		CheckRuns:        &CheckRunRepo{q: sqlTx},
		Checkpoints:      &CheckpointRepo{q: sqlTx},
		MetricBatches:    &MetricBatchRepo{q: sqlTx},
		Metrics:          &MetricRepo{q: sqlTx},
		ResourceStatus:   &ResourceStatusRepo{q: sqlTx},
		DetectionRules:   &DetectionRuleRepo{q: sqlTx},
		Alerts:           &AlertRepo{q: sqlTx},
		DetectionRuns:    &DetectionRunRepo{q: sqlTx},
		Templates:        &TemplateRepo{q: sqlTx},
	}
	defer func() {
		if p := recover(); p != nil {
			_ = sqlTx.Rollback()
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		if rbErr := sqlTx.Rollback(); rbErr != nil {
			// The rollback failing must not mask the original error: keep it
			// wrapped (sentinels survive) and quote the rollback failure.
			return fmt.Errorf("repo: %w (rollback also failed: %v)", err, rbErr)
		}
		return err
	}
	if err := sqlTx.Commit(); err != nil {
		return fmt.Errorf("repo: commit transaction: %w", err)
	}
	return nil
}
