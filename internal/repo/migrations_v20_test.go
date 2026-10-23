package repo

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

// openAtVersion opens a GENUINE database whose schema stops at the given
// version (the migrations registry is temporarily capped — the same
// registry-filtering technique as withoutV14). Closing the DB restores the
// registry; the caller re-opens the file afterwards to exercise the
// remaining migrations (the upgrade path).
func openAtVersion(t *testing.T, path string, version int) *DB {
	t.Helper()
	restored := append([]Migration(nil), migrations...)
	filtered := make([]Migration, 0, len(migrations))
	for _, m := range migrations {
		if m.Version <= version {
			filtered = append(filtered, m)
		}
	}
	migrations = filtered
	// Restore the registry when THIS FUNCTION returns, not at test end: the
	// registry only matters during Open (the file keeps its version state),
	// and the caller must be able to re-open later with the FULL registry to
	// exercise the upgrade path — the whole point of the backfill test.
	defer func() { migrations = restored }()
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open at v%d: %v", version, err)
	}
	return db
}

// geoColumnExists reports whether the resources table declares the named
// column (pragma table_info).
func geoColumnExists(ctx context.Context, db *DB, column string) (bool, error) {
	rows, err := db.sql.QueryContext(ctx, `PRAGMA table_info(resources)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return false, err
		}
		if strings.EqualFold(name, column) {
			return true, nil
		}
	}
	return false, rows.Err()
}

// TestMigrationV20ResourceGeoColumns pins the geo promotion of issue #45:
// a FRESH database gets the geo_lat/geo_lng columns (NULL by default) and
// the bounding-box index; the importer-facing Insert/Get round-trip writes
// and reads the numeric columns (the attributes JSON keeps its string form
// — the dual representation is deliberate: attributes stay the lossless
// source of truth for re-export, the REAL columns are the query substrate).
func TestMigrationV20ResourceGeoColumns(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if ok, err := geoColumnExists(ctx, db, "geo_lat"); err != nil || !ok {
		t.Fatalf("resources lacks geo_lat after v20 (ok=%v err=%v)", ok, err)
	}
	if ok, err := geoColumnExists(ctx, db, "geo_lng"); err != nil || !ok {
		t.Fatalf("resources lacks geo_lng after v20 (ok=%v err=%v)", ok, err)
	}
	if ok, err := indexExists(ctx, db, "idx_resources_geo"); err != nil || !ok {
		t.Fatalf("idx_resources_geo missing after v20 (ok=%v err=%v)", ok, err)
	}

	seedBatch(t, db, "bat_v20_geo")
	lat, lng := 45.773, 126.636
	res := domain.Resource{
		ID: "res-geo-1", Type: domain.ResourceTypeDevice, NaturalKey: "DEV-GEO-1",
		Name: "geo round trip", Status: "在网", FirstBatchID: "bat_v20_geo",
		Attributes: map[string]string{"geo_lat": "45.773", "geo_lng": "126.636"},
		GeoLat:     &lat, GeoLng: &lng,
	}
	if err := db.Resources.Insert(ctx, res); err != nil {
		t.Fatalf("insert geo resource: %v", err)
	}
	got, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, "DEV-GEO-1")
	if err != nil {
		t.Fatalf("get geo resource: %v", err)
	}
	if got.GeoLat == nil || got.GeoLng == nil || *got.GeoLat != lat || *got.GeoLng != lng {
		t.Fatalf("geo round trip: got (%v,%v), want (%v,%v)", got.GeoLat, got.GeoLng, lat, lng)
	}

	// A resource without coordinates inserts with NULL geo — Get returns
	// nil pointers, not zero (zero would fabricate a null-island position).
	plain := domain.Resource{
		ID: "res-geo-2", Type: domain.ResourceTypeDevice, NaturalKey: "DEV-GEO-2",
		Name: "no coords", FirstBatchID: "bat_v20_geo", Attributes: map[string]string{},
	}
	if err := db.Resources.Insert(ctx, plain); err != nil {
		t.Fatalf("insert plain resource: %v", err)
	}
	got2, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, "DEV-GEO-2")
	if err != nil {
		t.Fatalf("get plain resource: %v", err)
	}
	if got2.GeoLat != nil || got2.GeoLng != nil {
		t.Fatalf("plain resource geo = (%v,%v), want (nil,nil)", got2.GeoLat, got2.GeoLng)
	}
}

// TestMigrationV20BackfillsExistingAttributes pins the upgrade path: a
// database whose rows carry geo only inside the attributes JSON (the
// pre-v20 shape of every real import) gets the REAL columns backfilled by
// the migration itself — json_extract re-derives them; rows without geo
// keys stay NULL. This is the honest upgrade contract: no re-import needed.
func TestMigrationV20BackfillsExistingAttributes(t *testing.T) {
	dir := testsupport.TempDir(t)
	dbPath := filepath.Join(dir, "netops.db")

	// Build a pre-v20 database (migrate to 19 only) and insert a row with
	// string geo attributes — the exact pre-v20 import shape.
	pre := openAtVersion(t, dbPath, 19)
	ctx := context.Background()
	if err := pre.Do(ctx, func(tx *Tx) error {
		if err := tx.Batches.Create(ctx, newBatch("bat_v20_back", simTS)); err != nil {
			return err
		}
		if err := tx.Resources.Insert(ctx, domain.Resource{
			ID: "res-back-1", Type: domain.ResourceTypeBaseStation, NaturalKey: "460-00-1-1",
			Name: "backfill station", Status: "在网", FirstBatchID: "bat_v20_back",
			Attributes: map[string]string{"geo_lat": "48.45402", "geo_lng": "125.11985"},
			CreatedAt:  simTS, UpdatedAt: simTS,
		}); err != nil {
			return err
		}
		return tx.Resources.Insert(ctx, domain.Resource{
			ID: "res-back-2", Type: domain.ResourceTypeDevice, NaturalKey: "DEV-BACK-2",
			Name: "no geo", FirstBatchID: "bat_v20_back",
			Attributes: map[string]string{"vendor": "华为"},
			CreatedAt:  simTS, UpdatedAt: simTS,
		})
	}); err != nil {
		t.Fatalf("seed pre-v20 resources: %v", err)
	}
	if err := pre.Close(); err != nil {
		t.Fatalf("close pre-v20 db: %v", err)
	}

	// Re-open: Open applies the remaining migrations (v20) — the backfill
	// must run inside that upgrade.
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("re-open at v20: %v", err)
	}
	defer func() { _ = db.Close() }()

	got, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeBaseStation, "460-00-1-1")
	if err != nil {
		t.Fatalf("get backfilled station: %v", err)
	}
	if got.GeoLat == nil || got.GeoLng == nil {
		t.Fatalf("backfill did not populate geo columns: (%v,%v)", got.GeoLat, got.GeoLng)
	}
	if *got.GeoLat != 48.45402 || *got.GeoLng != 125.11985 {
		t.Fatalf("backfilled geo = (%v,%v), want (48.45402,125.11985)", *got.GeoLat, *got.GeoLng)
	}
	got2, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, "DEV-BACK-2")
	if err != nil {
		t.Fatalf("get no-geo resource: %v", err)
	}
	if got2.GeoLat != nil || got2.GeoLng != nil {
		t.Fatalf("no-geo resource backfilled to (%v,%v), want nil", got2.GeoLat, got2.GeoLng)
	}
}

// TestMigrationV20Nearest pins the spatial query the columns enable:
// ResourceRepo.Nearest returns resources of one type ordered by true
// haversine distance from the query point (bounding-box prefilter in SQL,
// exact ranking in Go), bounded by limit, and does NOT fabricate results
// when nothing lies in the window.
func TestMigrationV20Nearest(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// Three stations around Harbin at increasing distance, plus one station
	// far away (outside the ~1.5° window) that must NOT shadow the nearest.
	seed := []domain.Resource{
		{ID: "res-n1", Type: domain.ResourceTypeBaseStation, NaturalKey: "near-1", Name: "near 1",
			Attributes: map[string]string{}, GeoLat: ptrFloat(45.80), GeoLng: ptrFloat(126.64)},
		{ID: "res-n2", Type: domain.ResourceTypeBaseStation, NaturalKey: "near-2", Name: "near 2",
			Attributes: map[string]string{}, GeoLat: ptrFloat(45.90), GeoLng: ptrFloat(126.70)},
		{ID: "res-n3", Type: domain.ResourceTypeBaseStation, NaturalKey: "near-3", Name: "near 3",
			Attributes: map[string]string{}, GeoLat: ptrFloat(45.76), GeoLng: ptrFloat(126.63)},
		{ID: "res-far", Type: domain.ResourceTypeBaseStation, NaturalKey: "far-1", Name: "far",
			Attributes: map[string]string{}, GeoLat: ptrFloat(52.03), GeoLng: ptrFloat(122.52)},
		{ID: "res-dev", Type: domain.ResourceTypeDevice, NaturalKey: "DEV-N", Name: "device near",
			Attributes: map[string]string{}, GeoLat: ptrFloat(45.75), GeoLng: ptrFloat(126.64)},
	}
	seedBatch(t, db, "bat_v20_near")
	for i := range seed {
		seed[i].FirstBatchID = "bat_v20_near"
		seed[i].CreatedAt, seed[i].UpdatedAt = simTS, simTS
		if err := db.Resources.Insert(ctx, seed[i]); err != nil {
			t.Fatalf("seed %s: %v", seed[i].ID, err)
		}
	}

	// Query at Harbin center: exact order by haversine, type-filtered,
	// limited. near-3 (45.76,126.63) is closest, then near-1, then near-2.
	got, err := db.Resources.Nearest(ctx, domain.ResourceTypeBaseStation, 45.75, 126.64, 2)
	if err != nil {
		t.Fatalf("Nearest: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Nearest returned %d, want 2 (limit)", len(got))
	}
	if got[0].NaturalKey != "near-3" || got[1].NaturalKey != "near-1" {
		t.Fatalf("Nearest order = [%s, %s], want [near-3, near-1] (haversine, not axis order)",
			got[0].NaturalKey, got[1].NaturalKey)
	}

	// Far point with nothing nearby: empty slice, no error, no fabrication.
	none, err := db.Resources.Nearest(ctx, domain.ResourceTypeDevice, 30.0, 120.0, 5)
	if err != nil {
		t.Fatalf("Nearest far point: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("Nearest far point returned %d results, want 0", len(none))
	}
}

func ptrFloat(v float64) *float64 { return &v }

// seedBatch creates the import batch the resources' first_batch_id FK
// requires (the v9-test seeding pattern).
func seedBatch(t *testing.T, db *DB, id string) {
	t.Helper()
	ctx := context.Background()
	err := db.Do(ctx, func(tx *Tx) error {
		return tx.Batches.Create(ctx, newBatch(id, simTS))
	})
	if err != nil {
		t.Fatalf("seed batch %s: %v", id, err)
	}
}
