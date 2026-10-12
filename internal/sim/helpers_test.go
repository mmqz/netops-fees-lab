package sim

// Shared test scaffolding of the simulation engine suite: fresh databases
// seeded with the three committed testdata workbooks (TASK_PROTOCOL §4),
// the materialized 27-device workspace derived from them, and lookup
// helpers over the topology payload. The seeding mirrors
// internal/importer/importer_test.go (Analyze + Execute with the suggested
// mapping; import order devices → ippools → circuits).

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/importer"
	"github.com/mmqz/netops-lab/internal/repo"
)

// testdataPath returns the path of a committed testdata artifact.
func testdataPath(name string) string {
	return filepath.Join("..", "..", "testdata", name)
}

// tempDirT is t.TempDir with a Windows-tolerant cleanup (issue #5, same
// rationale as internal/repo/repo_test.go: sqlite handle release lags
// db.Close on Windows; retry RemoveAll briefly, log instead of fail).
func tempDirT(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "netops-sim-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() {
		var last error
		for i := 0; i < 25; i++ {
			if last = os.RemoveAll(dir); last == nil {
				return
			}
			time.Sleep(40 * time.Millisecond)
		}
		t.Logf("tempdir cleanup: RemoveAll(%s) failed after ~1s of retries: %v (Windows 句柄释放延迟，忽略)", dir, last)
	})
	return dir
}

// newEngine opens a fresh database and returns a sim Service bound to it.
func newEngine(t *testing.T) (*Service, *repo.DB) {
	t.Helper()
	db, err := repo.Open(filepath.Join(tempDirT(t), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close: %v", err)
		}
	})
	return New(db), db
}

// importOrder is the canonical import order of the three workbooks.
var importOrder = []string{"devices.xlsx", "ippools.xlsx", "circuits.xlsx"}

// importWorkbooks imports the three committed testdata workbooks (devices →
// ippools → circuits, suggested mappings) into db and returns the batch ids
// in import order. The seeded expectations are pinned by
// testdata/expected-summary.json: 27 / 16 / 21 imported rows.
func importWorkbooks(t *testing.T, db *repo.DB) []string {
	t.Helper()
	ctx := context.Background()
	imp := importer.New(db)
	batchIDs := make([]string, 0, len(importOrder))
	wantImported := map[string]int{"devices.xlsx": 27, "ippools.xlsx": 16, "circuits.xlsx": 21}
	for _, file := range importOrder {
		a, err := imp.Analyze(ctx, testdataPath(file))
		if err != nil {
			t.Fatalf("Analyze(%s): %v", file, err)
		}
		if len(a.Sheets) != 1 {
			t.Fatalf("Analyze(%s): got %d sheets, want 1", file, len(a.Sheets))
		}
		sum, err := imp.Execute(ctx, a.BatchID, a.SuggestedMapping)
		if err != nil {
			t.Fatalf("Execute(%s): %v", file, err)
		}
		if sum.RowsImported != wantImported[file] {
			t.Fatalf("Execute(%s): imported %d rows, want %d", file, sum.RowsImported, wantImported[file])
		}
		batchIDs = append(batchIDs, a.BatchID)
	}
	return batchIDs
}

// seededEngine is the standard fixture: a fresh database with the three
// workbooks imported and ONE materialized workspace named name (default
// "lab"). It returns the engine and the materialization result.
func seededEngine(t *testing.T) (*Service, *repo.DB, MaterializeResult) {
	t.Helper()
	svc, db := newEngine(t)
	batchIDs := importWorkbooks(t, db)
	res, err := svc.Materialize(context.Background(), "lab", "", batchIDs)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	return svc, db, res
}

// findNode returns the topology node with the given device code.
func findNode(t *testing.T, topo Topology, code string) TopologyNode {
	t.Helper()
	for _, n := range topo.Nodes {
		if n.Code == code {
			return n
		}
	}
	t.Fatalf("topology: no node with code %s", code)
	return TopologyNode{}
}

// findIface returns the node interface with the given name.
func findIface(t *testing.T, node TopologyNode, name string) TopologyInterface {
	t.Helper()
	for _, i := range node.Interfaces {
		if i.Name == name {
			return i
		}
	}
	t.Fatalf("node %s: no interface %s", node.Code, name)
	return TopologyInterface{}
}

// findEdgeByLabel returns the unique topology edge with the given label.
func findEdgeByLabel(t *testing.T, topo Topology, label string) TopologyEdge {
	t.Helper()
	var found []TopologyEdge
	for _, e := range topo.Edges {
		if e.Label == label {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("topology: %d edges with label %s, want 1", len(found), label)
	}
	return found[0]
}

// mustTopology reads the workspace topology or fails the test.
func mustTopology(t *testing.T, svc *Service, workspaceID string) Topology {
	t.Helper()
	topo, err := svc.Topology(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("Topology: %v", err)
	}
	return topo
}

// mustHash reads the workspace state hash or fails the test.
func mustHash(t *testing.T, svc *Service, workspaceID string) string {
	t.Helper()
	hash, err := svc.WorkspaceHash(context.Background(), workspaceID)
	if err != nil {
		t.Fatalf("WorkspaceHash: %v", err)
	}
	return hash
}

// mustRunChecks runs a manual check run and returns the run with its
// results.
func mustRunChecks(t *testing.T, svc *Service, workspaceID string) (domain.CheckRun, []domain.CheckResult) {
	t.Helper()
	res, err := svc.RunChecks(context.Background(), workspaceID, domain.CheckTriggerManual)
	if err != nil {
		t.Fatalf("RunChecks: %v", err)
	}
	run, results, err := svc.GetRun(context.Background(), workspaceID, res.RunID)
	if err != nil {
		t.Fatalf("GetRun(%s): %v", res.RunID, err)
	}
	return run, results
}

// applyOK applies ops successfully (no auto-checks) and returns the result.
func applyOK(t *testing.T, svc *Service, workspaceID string, ops []Op) ApplyResult {
	t.Helper()
	res, err := svc.ApplyOps(context.Background(), workspaceID, ops, "", false)
	if err != nil {
		t.Fatalf("ApplyOps: %v", err)
	}
	return res
}

// findings filters a run's result rows down to the findings of one check
// code (passed=0), in stored order.
func findings(results []domain.CheckResult, checkCode string) []domain.CheckResult {
	var out []domain.CheckResult
	for _, r := range results {
		if !r.Passed && r.CheckCode == checkCode {
			out = append(out, r)
		}
	}
	return out
}

// countCode counts result rows of one check code (findings and baselines).
func countCode(results []domain.CheckResult, checkCode string, passed bool) int {
	n := 0
	for _, r := range results {
		if r.CheckCode == checkCode && r.Passed == passed {
			n++
		}
	}
	return n
}

// strPtr / intPtr build op pointer fields.
func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }
