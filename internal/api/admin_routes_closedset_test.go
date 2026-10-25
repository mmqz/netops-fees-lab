package api

// admin_routes_closedset_test.go — the closed-set pin of issue #9 (v0.18):
// the Admin classification lives in exactly these seven routes of the
// single route table; anything else added to the table defaults to the
// declarer tier. The facts gate (facts_gate_test.go) pins route COUNTS;
// this pins the PRIVILEGE class — a new admin route must touch this test
// and be a deliberate act.
import "testing"

func TestAdminRouteClosedSet(t *testing.T) {
	s := &Server{}
	want := map[string]bool{
		"DELETE /api/batches/{id}":                true,
		"DELETE /api/mapping-templates/{id}":      true,
		"POST /api/metrics/batches/{id}/rollback": true,
		"POST /api/fees/batches/{id}/rollback":    true,
		"POST /api/fees/risk-clue-decisions":      true,
		"POST /api/fees/links/{id}/confirm":       true,
		"POST /api/fees/links/{id}/dismiss":       true,
	}
	got := map[string]bool{}
	for _, r := range s.apiRoutes() {
		if r.Admin {
			key := r.Method + " " + r.Pattern
			got[key] = true
			if !want[key] {
				t.Errorf("route %q is Admin-flagged but not in the closed set — extend this test deliberately", key)
			}
		}
	}
	for key := range want {
		if !got[key] {
			t.Errorf("route %q must be Admin-flagged (issue #9 closed set) but is not", key)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("Admin route count = %d, want %d", len(got), len(want))
	}
}
