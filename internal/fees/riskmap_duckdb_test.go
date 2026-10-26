//go:build duckdb

package fees

// riskmap_duckdb_test.go — issue #18's data-face pins: the all-scenarios
// roll-up per site, the confirmed-link coordinate join (migration v20's
// structured geo_lat/geo_lng keys), the honest coverage accounting
// (linkedSites vs geoSites), and the deterministic point order. The
// rendering capability is the cloud's; the geo data is the user's own
// resource sheets (#16 boundary) — the fixture emulates exactly that.
import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/repo"
)

// seedGeoResource seeds one site resource carrying geo_lat/geo_lng
// attributes (the v20 split keys) + a confirmed link.
func seedGeoResource(t *testing.T, db *repo.DB, resID, key, status string, lat, lng string) {
	t.Helper()
	seedLinkResource(t, db, resID, key, status)
	// resourceCoords reads the structured keys — patch them onto the seeded row.
	if err := db.Resources.UpdateAttributes(context.Background(), resID, map[string]string{
		"geo_lat": lat, "geo_lng": lng,
	}, simTSLink); err != nil {
		t.Fatalf("patch geo attrs %s: %v", resID, err)
	}
}

func TestRiskMapAggregation(t *testing.T) {
	svc, db, dir := newService(t)
	ctx := context.Background()

	// Two sites, two batches each (duplicate-settlement shape → clues);
	// GEO-1 carries coordinates, GEO-2 does not (the honest gap).
	for _, f := range []struct{ name, body string }{
		{"a.csv", "站址编码,账期,电费(元)\nGEO-1,2024-05,100.00\nGEO-2,2024-05,50.00\n"},
		{"b.csv", "站址编码,账期,电费(元)\nGEO-1,2024-05,300.00\nGEO-2,2024-05,60.00\n"},
	} {
		p := filepath.Join(dir, f.name)
		if err := writeFileForMap(t, p, f.body); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Import(ctx, ImportOptions{Path: p, FeeType: FeeTypeElectricity, Year: 2024, Month: 5}); err != nil {
			t.Fatalf("import %s: %v", f.name, err)
		}
	}
	seedGeoResource(t, db, "res_geo1", "GEO-1", "在网", "45.75", "126.63")
	seedLinkResource(t, db, "res_geo2", "GEO-2", "在网")

	engine := Query(ctx)
	if !engine.Available() {
		t.Fatal("the duckdb build must report Available")
	}
	report, err := svc.RiskMap(ctx, engine, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
	if err != nil {
		t.Fatalf("RiskMap: %v", err)
	}

	// Coverage accounting: 2 linked sites, 1 with coordinates.
	if report.LinkedSites != 2 || report.GeoSites != 1 {
		t.Fatalf("coverage = %d/%d, want 2 linked / 1 geo", report.LinkedSites, report.GeoSites)
	}
	if len(report.Points) != 2 {
		t.Fatalf("points = %d, want 2", len(report.Points))
	}
	// Deterministic order (site code asc).
	if report.Points[0].SiteCode != "GEO-1" || report.Points[1].SiteCode != "GEO-2" {
		t.Fatalf("order = %s,%s — deterministic site-code order required", report.Points[0].SiteCode, report.Points[1].SiteCode)
	}
	p1, p2 := report.Points[0], report.Points[1]
	// GEO-1: duplicate-settlement clues (2 batches), coordinates attached.
	if p1.Lat == nil || p1.Lng == nil || *p1.Lat != 45.75 || *p1.Lng != 126.63 {
		t.Fatalf("GEO-1 coords = %v/%v, want 45.75/126.63", p1.Lat, p1.Lng)
	}
	if p1.ClueCount == 0 || p1.Pending != p1.ClueCount {
		t.Fatalf("GEO-1 counts = %+v — fresh clues are pending", p1)
	}
	if len(p1.Scenarios) == 0 {
		t.Fatalf("GEO-1 scenarios empty: %+v", p1)
	}
	if p1.ResourceKey != "GEO-1" {
		t.Fatalf("GEO-1 resourceKey = %q", p1.ResourceKey)
	}
	// GEO-2: linked but geoless — the honest null, NOT a guessed zero.
	if p2.Lat != nil || p2.Lng != nil {
		t.Fatalf("GEO-2 coords = %v/%v, want nil (no geo in the resource sheet)", p2.Lat, p2.Lng)
	}

	// Determinism: a re-run answers the identical report.
	again, err := svc.RiskMap(ctx, engine, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
	if err != nil {
		t.Fatalf("RiskMap re-run: %v", err)
	}
	if len(again.Points) != len(report.Points) || again.Points[0].SiteCode != report.Points[0].SiteCode ||
		again.GeoSites != report.GeoSites {
		t.Fatalf("re-run drifted: %+v vs %+v", again, report)
	}
}

func TestRiskMapDefaultsTo501WithoutEngine(t *testing.T) {
	svc, _, _ := newService(t)
	engine := Query(context.Background())
	if engine.Available() {
		t.Skip("this pin needs a default (non-duckdb) build")
	}
	if _, err := svc.RiskMap(context.Background(), engine, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024}); !errIs501(err) {
		t.Fatalf("default build = %v, want ErrEngineNotBuilt", err)
	}
}

func errIs501(err error) bool {
	return err == ErrEngineNotBuilt
}

func writeFileForMap(t *testing.T, path, body string) error {
	t.Helper()
	return os.WriteFile(path, []byte(body), 0o644)
}
