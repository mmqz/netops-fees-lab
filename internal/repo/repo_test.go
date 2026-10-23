package repo

import (
	"context"
	"errors"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
	"path/filepath"
	"testing"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// newBatch builds a minimal valid import batch for tests.
func newBatch(id string, started time.Time) domain.ImportBatch {
	return domain.ImportBatch{
		ID:             id,
		WorkbookName:   "devices.xlsx",
		WorkbookSHA256: "abc123",
		Status:         domain.BatchStatusAnalyzing,
		Mapping:        "{}",
		RuleVersion:    domain.RuleVersion,
		StartedAt:      started,
	}
}

// newResource builds a minimal valid device resource for tests.
func newResource(id, key string, createdAt time.Time) domain.Resource {
	return domain.Resource{
		ID:           id,
		Type:         domain.ResourceTypeDevice,
		NaturalKey:   key,
		Name:         "设备-" + key,
		Attributes:   map[string]string{"city": "杭州", "vendor": "华为"},
		Status:       "在网",
		FirstBatchID: "bat_1",
		CreatedAt:    createdAt,
		UpdatedAt:    createdAt,
	}
}

func TestOpenAppliesMigrationsIdempotently(t *testing.T) {
	dir := testsupport.TempDir(t)
	path := filepath.Join(dir, "netops.db")

	db1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	ctx := context.Background()
	b := newBatch("bat_1", time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC))
	if err := db1.Batches.Create(ctx, b); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Second Open on the same file must skip applied migrations and keep data.
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer db2.Close()

	var count int
	if err := db2.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if count != len(migrations) {
		t.Errorf("schema_migrations rows = %d, want %d", count, len(migrations))
	}
	got, err := db2.Batches.Get(ctx, "bat_1")
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if got.ID != "bat_1" || got.Status != domain.BatchStatusAnalyzing {
		t.Errorf("batch after reopen = %+v, want bat_1/analyzing", got)
	}
}

func TestDoCommits(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	ts := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)

	err := db.Do(ctx, func(tx *Tx) error {
		if err := tx.Batches.Create(ctx, newBatch("bat_1", ts)); err != nil {
			return err
		}
		return tx.Resources.Insert(ctx, newResource("res_1", "DEV-001", ts))
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}

	if _, err := db.Batches.Get(ctx, "bat_1"); err != nil {
		t.Errorf("batch not persisted after commit: %v", err)
	}
	res, err := db.Resources.GetByID(ctx, "res_1")
	if err != nil {
		t.Errorf("resource not persisted after commit: %v", err)
	}
	if res.NaturalKey != "DEV-001" || res.Attributes["city"] != "杭州" {
		t.Errorf("resource roundtrip mismatch: %+v", res)
	}
}

func TestDoRollsBackOnError(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	ts := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	sentinel := errors.New("injected failure")

	err := db.Do(ctx, func(tx *Tx) error {
		if err := tx.Batches.Create(ctx, newBatch("bat_1", ts)); err != nil {
			return err
		}
		if err := tx.Resources.Insert(ctx, newResource("res_1", "DEV-001", ts)); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Do returned %v, want the injected error", err)
	}

	// Nothing may be persisted — the Stage 0 atomicity guarantee (S0-6).
	if _, err := db.Batches.Get(ctx, "bat_1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("batch persisted after rollback: %v", err)
	}
	if _, err := db.Resources.GetByID(ctx, "res_1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("resource persisted after rollback: %v", err)
	}
	var n int
	if err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM resources`).Scan(&n); err != nil {
		t.Fatalf("count resources: %v", err)
	}
	if n != 0 {
		t.Errorf("resources count = %d, want 0", n)
	}
}

func TestDoRollsBackOnPanic(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	ts := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)

	defer func() {
		if p := recover(); p == nil {
			t.Fatal("panic must propagate out of Do")
		}
		var n int
		if err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM resources`).Scan(&n); err != nil {
			t.Fatalf("count resources: %v", err)
		}
		if n != 0 {
			t.Errorf("resources count after panic = %d, want 0", n)
		}
	}()
	_ = db.Do(ctx, func(tx *Tx) error {
		if err := tx.Batches.Create(ctx, newBatch("bat_1", ts)); err != nil {
			return err
		}
		if err := tx.Resources.Insert(ctx, newResource("res_1", "DEV-001", ts)); err != nil {
			return err
		}
		panic("boom")
	})
}
