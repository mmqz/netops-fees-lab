package repo

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

// TestMigrationV40SiteMasterColumns pins the #38 upgrade path: a genuine
// v39 database whose resources already carry 站址性质/供电方式/生命周期 as
// attribute text (the pre-#38 world) gains the three structured columns on
// open, with the Chinese values backfilled to the English tokens and
// out-of-set values honestly left NULL (master data missing, never a
// guess). Fresh databases answer NULL columns; the CHECK constraints
// refuse out-of-set writes.
func TestMigrationV40SiteMasterColumns(t *testing.T) {
	path := filepath.Join(testsupport.TempDir(t), "netops.db")

	// Seed at v39 through the API with the attribute text already present.
	db := openAtVersion(t, path, 39)
	ctx := context.Background()
	if err := db.Do(ctx, func(tx *Tx) error {
		return tx.Batches.Create(ctx, newBatch("b1", simTS))
	}); err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	seed := domain.Resource{
		ID: "res_v40a", Type: domain.ResourceTypeSite, NaturalKey: "SITE-40A", Name: "共享直供站",
		Attributes: map[string]string{
			"site_nature": "共享", "power_mode": "直供", "lifecycle": "在网",
		},
		FirstBatchID: "b1", CreatedAt: simTS, UpdatedAt: simTS,
	}
	if err := db.Resources.Insert(ctx, seed); err != nil {
		t.Fatalf("seed insert: %v", err)
	}
	partial := domain.Resource{
		ID: "res_v40b", Type: domain.ResourceTypeSite, NaturalKey: "SITE-40B", Name: "口径外站",
		Attributes: map[string]string{
			"site_nature": "铁塔站", // not in the closed set — stays NULL
			"lifecycle":   "退网",
		},
		FirstBatchID: "b1", CreatedAt: simTS, UpdatedAt: simTS,
	}
	if err := db.Resources.Insert(ctx, partial); err != nil {
		t.Fatalf("partial insert: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close at v39: %v", err)
	}

	// Upgrade: open with the FULL registry — v40 runs the backfill.
	up, err := Open(path)
	if err != nil {
		t.Fatalf("upgrade open: %v", err)
	}
	defer func() { _ = up.Close() }()

	got, err := up.Resources.GetByID(ctx, "res_v40a")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.SiteNature != "shared" || got.PowerMode != "direct" || got.Lifecycle != "active" {
		t.Fatalf("backfill = %q/%q/%q, want shared/direct/active", got.SiteNature, got.PowerMode, got.Lifecycle)
	}
	// The attributes text survives untouched (lossless source of truth).
	if got.Attributes["site_nature"] != "共享" {
		t.Fatalf("attributes were rewritten: %v", got.Attributes)
	}
	gotB, err := up.Resources.GetByID(ctx, "res_v40b")
	if err != nil {
		t.Fatalf("GetByID(v40b): %v", err)
	}
	if gotB.SiteNature != "" {
		t.Fatalf("out-of-set value must stay NULL, got %q (铁塔站 is not in the closed set)", gotB.SiteNature)
	}
	if gotB.Lifecycle != "retired" {
		t.Fatalf("recognized value backfilled = %q, want retired", gotB.Lifecycle)
	}

	// The CHECK constraints refuse out-of-set tokens.
	if _, err := up.sql.ExecContext(ctx,
		`UPDATE resources SET site_nature = 'bogus' WHERE id = 'res_v40a'`); err == nil {
		t.Fatalf("site_nature CHECK constraint did not refuse 'bogus'")
	}
	if _, err := up.sql.ExecContext(ctx,
		`UPDATE resources SET power_mode = 'mains' WHERE id = 'res_v40a'`); err == nil {
		t.Fatalf("power_mode CHECK constraint did not refuse 'mains'")
	}
	if _, err := up.sql.ExecContext(ctx,
		`UPDATE resources SET lifecycle = 'paused' WHERE id = 'res_v40a'`); err == nil {
		t.Fatalf("lifecycle CHECK constraint did not refuse 'paused'")
	}

	// A fresh database (no rows) opens clean — the additive columns exist.
	fresh, err := Open(filepath.Join(testsupport.TempDir(t), "fresh.db"))
	if err != nil {
		t.Fatalf("fresh open: %v", err)
	}
	defer func() { _ = fresh.Close() }()
}

// TestResourceInsertSiteMasterRoundTrip pins the repo round-trip: the
// three tokens land on the columns and read back (the 14-column insert
// shape), and a master-data-free resource keeps the legacy 9/11-column
// shapes (the pre-v20 byte-identity discipline).
func TestResourceInsertSiteMasterRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	if err := db.Do(ctx, func(tx *Tx) error {
		return tx.Batches.Create(ctx, newBatch("b1", simTS))
	}); err != nil {
		t.Fatalf("batch: %v", err)
	}
	withMaster := domain.Resource{
		ID: "res_m1", Type: domain.ResourceTypeBaseStation, NaturalKey: "BS-1", Name: "归因基板",
		Attributes:   map[string]string{"site_nature": "共享"},
		SiteNature:   "shared",
		PowerMode:    "none",
		Lifecycle:    "active",
		FirstBatchID: "b1", CreatedAt: simTS, UpdatedAt: simTS,
	}
	if err := db.Resources.Insert(ctx, withMaster); err != nil {
		t.Fatalf("insert with master: %v", err)
	}
	back, err := db.Resources.GetByID(ctx, "res_m1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if back.SiteNature != "shared" || back.PowerMode != "none" || back.Lifecycle != "active" {
		t.Fatalf("round trip = %q/%q/%q", back.SiteNature, back.PowerMode, back.Lifecycle)
	}
	plain := domain.Resource{
		ID: "res_m2", Type: domain.ResourceTypeBaseStation, NaturalKey: "BS-2", Name: "无主数据",
		FirstBatchID: "b1", CreatedAt: simTS, UpdatedAt: simTS,
	}
	if err := db.Resources.Insert(ctx, plain); err != nil {
		t.Fatalf("insert plain: %v", err)
	}
	back2, err := db.Resources.GetByID(ctx, "res_m2")
	if err != nil {
		t.Fatalf("GetByID plain: %v", err)
	}
	if back2.SiteNature != "" || back2.PowerMode != "" || back2.Lifecycle != "" {
		t.Fatalf("plain resource answered master data from nowhere: %+v", back2)
	}
}
