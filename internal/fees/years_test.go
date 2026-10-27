package fees

// years_test.go — the data-year discovery face (v0.20.3): the path-level
// primitive behind GET /api/fees/years and the year pickers' data-first
// default. The old calendar default opened a 2026 dashboard against 2024
// data (blank cards + the risk map's month=0 leaking a validation error);
// these tests pin the discovery's honesty contract on every axis the
// endpoint doc promises: presence follows FILES not directories (rollback
// honesty), a bad glob surfaces instead of masquerading as empty (#105
// lesson), and stray paths never crash the picker.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/repo"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

// seedPartitionFile drops one dummy .parquet FILE into the five-part hive
// partition layout the writer produces. Content is irrelevant — the
// discovery is path-level and never opens the files.
func seedPartitionFile(t *testing.T, dataDir, feeType string, year, month int) {
	t.Helper()
	p := filepath.Join(dataDir, "analysis", "fees",
		"fee_type="+feeType, fmt.Sprintf("year=%d", year), fmt.Sprintf("month=%02d", month),
		"batch-x.parquet")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// TestServiceDataYearsDiscovery pins the happy path: years across fee
// types and months collapse to the distinct ascending year list — the
// picker's default is max(years), whatever the clock says.
func TestServiceDataYearsDiscovery(t *testing.T) {
	svc, _, dir := newService(t)
	seedPartitionFile(t, dir, "electricity", 2024, 5)
	seedPartitionFile(t, dir, "tower_lease", 2024, 11)
	seedPartitionFile(t, dir, "electricity", 2023, 12)

	years, err := svc.DataYears()
	if err != nil {
		t.Fatalf("DataYears: %v", err)
	}
	if want := []int{2023, 2024}; !reflect.DeepEqual(years, want) {
		t.Errorf("DataYears() = %v, want %v", years, want)
	}
}

// TestServiceDataYearsEmptyIsHonestEmptyList pins the fresh-install
// answer: an analysis dir that doesn't even exist answers [], not null
// and not an error — absence is a statement the JSON face renders as [].
func TestServiceDataYearsEmptyIsHonestEmptyList(t *testing.T) {
	svc, _, _ := newService(t)

	years, err := svc.DataYears()
	if err != nil {
		t.Fatalf("DataYears on fresh install: %v", err)
	}
	if years == nil {
		t.Fatal("DataYears() = nil, want non-nil empty slice (JSON [] not null)")
	}
	if len(years) != 0 {
		t.Errorf("DataYears() = %v, want empty", years)
	}
}

// TestServiceDataYearsRolledBackYearAbsent pins the rollback honesty:
// a partition DIRECTORY that outlived its files (every batch of the year
// rolled back) is honestly ABSENT — presence follows files, and the
// picker never offers a year the analysis face cannot answer for.
func TestServiceDataYearsRolledBackYearAbsent(t *testing.T) {
	svc, _, dir := newService(t)
	seedPartitionFile(t, dir, "electricity", 2024, 5)
	empty := filepath.Join(dir, "analysis", "fees", "fee_type=electricity", "year=2025", "month=03")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	years, err := svc.DataYears()
	if err != nil {
		t.Fatalf("DataYears: %v", err)
	}
	if want := []int{2024}; !reflect.DeepEqual(years, want) {
		t.Errorf("DataYears() = %v, want %v (2025 dir has no files — rolled back)", years, want)
	}
}

// TestServiceDataYearsSkipsStrayPaths pins the defensiveness: paths that
// don't carry the writer's five-part partition shape (or carry an
// unparseable year component) are skipped, never fatal — a stray file
// must not take the year picker down with it.
func TestServiceDataYearsSkipsStrayPaths(t *testing.T) {
	svc, _, dir := newService(t)
	seedPartitionFile(t, dir, "electricity", 2024, 5)
	// A four-part path (no month component) — matches the glob, not the
	// writer's shape.
	stray := filepath.Join(dir, "analysis", "fees", "fee_type=electricity", "year=2027", "batch-y.parquet")
	if err := os.MkdirAll(filepath.Dir(stray), 0o755); err != nil {
		t.Fatalf("MkdirAll stray: %v", err)
	}
	if err := os.WriteFile(stray, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile stray: %v", err)
	}
	// An unparseable year component in an otherwise well-shaped path.
	badYear := filepath.Join(dir, "analysis", "fees", "fee_type=electricity", "year=abc", "month=03", "batch-z.parquet")
	if err := os.MkdirAll(filepath.Dir(badYear), 0o755); err != nil {
		t.Fatalf("MkdirAll badYear: %v", err)
	}
	if err := os.WriteFile(badYear, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile badYear: %v", err)
	}

	years, err := svc.DataYears()
	if err != nil {
		t.Fatalf("DataYears: %v", err)
	}
	if want := []int{2024}; !reflect.DeepEqual(years, want) {
		t.Errorf("DataYears() = %v, want %v (stray paths skipped, not fatal)", years, want)
	}
}

// TestServiceDataYearsBadGlobSurfaces pins the #105 lesson on the new
// face: a data dir whose name breaks the glob pattern answers an ERROR,
// never "no data" — masking a bad pattern as an empty dashboard is the
// exact failure this repository already fixed once on the query face.
func TestServiceDataYearsBadGlobSurfaces(t *testing.T) {
	broken := filepath.Join(testsupport.TempDir(t), "bad[dir")
	db, err := repo.Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := NewService(db, broken)

	_, err = svc.DataYears()
	if err == nil {
		t.Fatal("DataYears on bad-glob data dir: err = nil, want the glob error surfaced")
	}
	if !errors.Is(err, filepath.ErrBadPattern) {
		t.Errorf("DataYears error = %v, want the wrapped path.ErrBadPattern", err)
	}
}
