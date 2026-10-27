package api_test

// fees_years_test.go — the HTTP surface of the data-year discovery face
// (v0.20.3): GET /api/fees/years answers in EVERY build (the discovery is
// path-level — directory presence is metadata, not an engine query, so
// there is no engine_not_built fork here), ascending, and honestly empty
// as [] on a fresh install. The year pickers' data-first default keys off
// this endpoint: the old calendar default opened a 2026 dashboard against
// 2024 data.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/app"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

// seedYearPartition drops one dummy .parquet FILE into the writer's
// five-part hive layout under DataDir — path presence is all the
// discovery reads, file content never opens.
func seedYearPartition(t *testing.T, dataDir, feeType string, year, month int) {
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

// TestFeesYearsEndpointDiscovery pins the contract on a server whose own
// DataDir we hold: distinct ascending years across fee types, and a
// rolled-back year (directory alive, files gone) honestly absent.
func TestFeesYearsEndpointDiscovery(t *testing.T) {
	dir := testsupport.TempDir(t)
	a, err := app.New(app.Config{
		DBPath:  filepath.Join(dir, "netops.db"),
		DataDir: dir,
	})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	srv := newAPITestServerFromApp(t, a)

	seedYearPartition(t, dir, "electricity", 2024, 5)
	seedYearPartition(t, dir, "tower_lease", 2023, 12)
	// A file-less partition directory: every 2025 batch rolled back —
	// the directory survives, the year must not.
	rolled := filepath.Join(dir, "analysis", "fees", "fee_type=electricity", "year=2025", "month=01")
	if err := os.MkdirAll(rolled, 0o755); err != nil {
		t.Fatalf("MkdirAll rolled: %v", err)
	}

	resp, body := doRequest(t, srv, http.MethodGet, "/api/fees/years", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/fees/years: status = %d, want 200 (no engine gate on this face)\nbody: %s", resp.StatusCode, body)
	}
	var out struct {
		Years []int `json:"years"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode years: %v\n%s", err, body)
	}
	if want := []int{2023, 2024}; !reflect.DeepEqual(out.Years, want) {
		t.Errorf("years = %v, want %v (2025 rolled back, honestly absent)", out.Years, want)
	}
}

// TestFeesYearsEndpointFreshInstall pins the fresh-install answer: 200
// with [], never null — absence is a statement, and the JSON face says
// it as an empty list so every picker's data-first default can fall
// back to the calendar year without tripping over null.
func TestFeesYearsEndpointFreshInstall(t *testing.T) {
	srv := newAPITestServer(t)

	resp, body := doRequest(t, srv, http.MethodGet, "/api/fees/years", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/fees/years: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	if string(body) == `{"years":null}` || !json.Valid(body) {
		t.Fatalf("fresh-install body = %s, want the honest empty list []", body)
	}
	var out struct {
		Years []int `json:"years"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode years: %v\n%s", err, body)
	}
	if out.Years == nil || len(out.Years) != 0 {
		t.Errorf("years = %#v, want non-nil empty (the fresh-install answer)", out.Years)
	}
}
