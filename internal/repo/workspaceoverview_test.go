package repo_test

// workspaceoverview_test.go — #102 equivalence pins: every batched read of
// workspaceoverview.go must answer EXACTLY what the per-item queries it
// replaced answered, on a REAL materialized world (the seeded workbooks →
// one sim workspace) plus an empty workspace. The handlers' wire shapes
// are covered by the api package's tests; the repo contract pinned here is
// shape parity: a workspace with content and a workspace without must both
// come out identical under the two shapes.

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/importer"
	"github.com/mmqz/netops-lab/internal/repo"
	"github.com/mmqz/netops-lab/internal/sim"
	"github.com/mmqz/netops-lab/internal/testsupport"
)

// seededMaterialized opens a fresh database, imports the three seeded
// workbooks and materializes one sim workspace from them (the same journey
// the mcpserver lab tests run, minus the MCP wire).
func seededMaterialized(t *testing.T) (*repo.DB, string) {
	t.Helper()
	db, err := repo.Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close: %v", err)
		}
	})
	ctx := context.Background()
	imp := importer.New(db)
	for _, file := range []string{"devices.xlsx", "ippools.xlsx", "circuits.xlsx"} {
		a, err := imp.Analyze(ctx, filepath.Join("..", "..", "testdata", file))
		if err != nil {
			t.Fatalf("Analyze(%s): %v", file, err)
		}
		if _, err := imp.Execute(ctx, a.BatchID, a.SuggestedMapping); err != nil {
			t.Fatalf("Execute(%s): %v", file, err)
		}
	}
	res, err := sim.New(db).Materialize(ctx, "overview-equiv", "equivalence fixture", nil)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	return db, res.WorkspaceID
}

func TestWorkspaceOverviewEquivalence(t *testing.T) {
	ctx := context.Background()
	db, richID := seededMaterialized(t)

	// The empty twin: a workspace row with nothing behind it (the
	// zero-counts / no-run semantics the per-item path carried).
	empty := domain.Workspace{Name: "empty", Description: "no content", Status: domain.WorkspaceStatusActive}
	if err := db.Do(ctx, func(tx *repo.Tx) error {
		return tx.Workspaces.Insert(ctx, empty)
	}); err != nil {
		t.Fatalf("insert empty workspace: %v", err)
	}
	workspaces, _, err := db.Workspaces.List(ctx, 1, 50)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(workspaces) != 2 {
		t.Fatalf("workspaces = %d, want 2", len(workspaces))
	}
	ids := []string{workspaces[0].ID, workspaces[1].ID}
	if ids[0] == ids[1] {
		t.Fatal("the two workspaces share an id")
	}

	summaries, err := db.Workspaces.CountSummariesOfWorkspaces(ctx, ids)
	if err != nil {
		t.Fatalf("CountSummariesOfWorkspaces: %v", err)
	}
	latests, err := db.CheckRuns.LatestByWorkspaces(ctx, ids)
	if err != nil {
		t.Fatalf("LatestByWorkspaces: %v", err)
	}

	for _, ws := range workspaces {
		sum := summaries[ws.ID]
		// The three status maps: per-item CountByWorkspace parity.
		for _, pair := range []struct {
			table   string
			batched map[domain.EntityStatus]int
			point   func() (map[domain.EntityStatus]int, error)
		}{
			{"virtual_devices", sum.DeviceStatus, func() (map[domain.EntityStatus]int, error) { return db.VirtualDevices.CountByWorkspace(ctx, ws.ID) }},
			{"interfaces", sum.InterfaceStatus, func() (map[domain.EntityStatus]int, error) { return db.SimInterfaces.CountByWorkspace(ctx, ws.ID) }},
			{"links", sum.LinkStatus, func() (map[domain.EntityStatus]int, error) { return db.SimLinks.CountByWorkspace(ctx, ws.ID) }},
		} {
			point, err := pair.point()
			if err != nil {
				t.Fatalf("CountByWorkspace(%s): %v", pair.table, err)
			}
			if len(point) != len(pair.batched) {
				t.Errorf("ws %s %s: batched map %v ≠ point map %v (size differs)", ws.ID, pair.table, pair.batched, point)
				continue
			}
			for st, n := range point {
				if pair.batched[st] != n {
					t.Errorf("ws %s %s[%v]: batched %d ≠ point %d", ws.ID, pair.table, st, pair.batched[st], n)
				}
			}
		}
		// The four plain counts.
		if got, err := db.ChangeSets.CountByWorkspace(ctx, ws.ID); err != nil || got != sum.ChangeSets {
			t.Errorf("ws %s changesets: batched %d (err %v), point %d", ws.ID, sum.ChangeSets, err, got)
		}
		if got, err := db.Checkpoints.CountByWorkspace(ctx, ws.ID); err != nil || got != sum.Checkpoints {
			t.Errorf("ws %s checkpoints: batched %d (err %v), point %d", ws.ID, sum.Checkpoints, err, got)
		}
		if got, err := db.LayerNodes.CountByWorkspace(ctx, ws.ID); err != nil || got != sum.LayerNodes {
			t.Errorf("ws %s layer nodes: batched %d (err %v), point %d", ws.ID, sum.LayerNodes, err, got)
		}
		if got, err := db.LayerLinks.CountByWorkspace(ctx, ws.ID); err != nil || got != sum.LayerLinks {
			t.Errorf("ws %s layer links: batched %d (err %v), point %d", ws.ID, sum.LayerLinks, err, got)
		}
		// The latest run: per-item Latest (ErrNotFound = absent).
		run, err := db.CheckRuns.Latest(ctx, ws.ID)
		if err == nil {
			if batched, ok := latests[ws.ID]; !ok || batched.ID != run.ID || batched.Score != run.Score {
				t.Errorf("ws %s latest run: batched %+v, point %s/%d", ws.ID, batched, run.ID, run.Score)
			}
		} else {
			if _, ok := latests[ws.ID]; ok {
				t.Errorf("ws %s: batched latest exists but point Latest answered %v", ws.ID, err)
			}
		}
	}

	// The rich workspace genuinely carries content (the equivalence above is
	// only meaningful when the fixtures are non-zero).
	rich := summaries[richID]
	if len(rich.DeviceStatus) == 0 || rich.ChangeSets < 0 || rich.LayerNodes == 0 {
		t.Fatalf("the materialized workspace's summary is unexpectedly thin: %+v — equivalence would be vacuous", rich)
	}
	// The empty workspace is all-zero / absent.
	emptyWS := summaries[workspaces[0].ID]
	if workspaces[0].ID == richID {
		emptyWS = summaries[workspaces[1].ID]
	}
	if len(emptyWS.DeviceStatus) != 0 || emptyWS.ChangeSets != 0 || emptyWS.LayerNodes != 0 {
		t.Errorf("the empty workspace's summary = %+v, want all-zero", emptyWS)
	}

	// Empty input: no database touch, empty maps.
	if m, err := db.Workspaces.CountSummariesOfWorkspaces(ctx, nil); err != nil || len(m) != 0 {
		t.Errorf("empty ids: summaries = %v (err %v), want empty", m, err)
	}
	if m, err := db.CheckRuns.LatestByWorkspaces(ctx, nil); err != nil || len(m) != 0 {
		t.Errorf("empty ids: latests = %v (err %v), want empty", m, err)
	}
	if m, err := db.CheckRuns.SeverityCountsByRuns(ctx, nil); err != nil || len(m) != 0 {
		t.Errorf("empty ids: sev counts = %v (err %v), want empty", m, err)
	}
}
