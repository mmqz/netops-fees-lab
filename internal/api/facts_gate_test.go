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
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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

// TestAPIMDocHeadingsMatchRouteTable pins the PROSE half of the HTTP
// contract (#116): api.md calls itself "契约基线", but nothing tied its
// method headings to the route table — three routes (openapi.json,
// alerts/stats, layer-members) shipped without a section, the same
// #66/#110 drift shape that kept the MCP tool count lying. The gate
// parses every "##/###/#### <METHOD> <path>" heading of api.md and
// compares the set against apiRoutes() BOTH WAYS: a route added without
// a doc section fails, a doc section naming a dead route fails too.
// Exemptions: none — api.md documents exactly the live table (query
// strings and " — title" suffixes in headings are stripped by the
// parsing; the doc is free to group nothing, every route stands alone).
func TestAPIMDocHeadingsMatchRouteTable(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "api.md"))
	if err != nil {
		t.Skipf("docs/api.md not readable (package tested outside the repo tree): %v", err)
	}

	// parse the method headings: ^#{2,4} METHOD /path(…)— api.md uses ##
	// and ### levels for route sections; deeper levels and prose lists
	// never open a line with a bare METHOD + /-path the regex captures.
	doc := make(map[string]bool)
	for _, line := range strings.Split(string(raw), "\n") {
		m := apiDocHeading.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		doc[m[1]+" "+m[2]] = true
	}

	db, err := repo.Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := New(slog.Default(), services.New(db, services.WithFees(testsupport.TempDir(t))), testsupport.TempDir(t))

	routes := s.apiRoutes()
	missing := make([]string, 0, 4)
	for _, r := range routes {
		key := r.Method + " " + r.Pattern
		if !doc[key] {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		t.Errorf("api.md is missing sections for %d live route(s): %v — the prose contract must document every route in the same change that adds it (api.md 契约基线)", len(missing), missing)
	}
	dead := make([]string, 0, 4)
	for key := range doc {
		live := false
		for _, r := range routes {
			if r.Method+" "+r.Pattern == key {
				live = true
				break
			}
		}
		if !live {
			dead = append(dead, key)
		}
	}
	if len(dead) > 0 {
		sort.Strings(dead)
		t.Errorf("api.md documents %d route(s) that no longer exist: %v — stale sections lie to consumers exactly like the #66 tool-count drift", len(dead), dead)
	}
}

// apiDocHeading matches one api.md route section heading, capturing the
// METHOD and the bare path (query strings are contract detail, not
// identity; the " — title" suffix is prose). \x60 is a backtick — the
// heading path never contains one, but the character class must stop there.
var apiDocHeading = regexp.MustCompile("^#{2,4} (GET|POST|PUT|DELETE|PATCH) ([^ ?\\x60—]+)")
