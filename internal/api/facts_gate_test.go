package api

// facts_gate_test.go — the #110 drift gates for the HTTP route table: the
// REAL route counts must equal the numbers declared in docs/facts.md, the
// machine-readable fact source. The route table (routes.go) already
// single-sources the mux and the OpenAPI document
// (TestOpenAPISpecMatchesLiveServer walks the pair), so the gate only has
// to keep the DOCUMENTED totals honest: a route added without bumping the
// facts file fails here instead of leaving fees-layer.md ("fees tag 共 N
// 条路由") and README quietly wrong.

import (
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/mmqz/netops-lab/internal/repo"
	"github.com/mmqz/netops-lab/internal/services"
	"github.com/mmqz/netops-lab/internal/testsupport"
)

func TestAPIRouteCountsMatchFactsFile(t *testing.T) {
	wantTotal, ok := testsupport.FactsFileInt(t, "api-routes")
	wantFees, okFees := testsupport.FactsFileInt(t, "api-fees-routes")
	if !ok || !okFees {
		t.Skipf("docs/facts.md not readable (package tested outside the repo tree)")
	}

	// The same composition the tests use everywhere: a real Server over a
	// throwaway database with the fee service wired (apiRoutes is a method
	// on Server, so the gate asserts against the very table the mux and
	// the OpenAPI document are generated from).
	db, err := repo.Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := New(slog.Default(), services.New(db, services.WithFees(testsupport.TempDir(t))), testsupport.TempDir(t))

	routes := s.apiRoutes()
	fees := 0
	for _, r := range routes {
		if r.Tag == "fees" {
			fees++
		}
	}
	if len(routes) != wantTotal {
		t.Fatalf("route table holds %d routes, docs/facts.md declares api-routes = %d — a route added or removed must update the facts file in the same change (see docs/facts.md §4)", len(routes), wantTotal)
	}
	if fees != wantFees {
		t.Fatalf("route table holds %d fees-tag routes, docs/facts.md declares api-fees-routes = %d — a fees route added or removed must update the facts file in the same change (see docs/facts.md §4)", fees, wantFees)
	}
}
