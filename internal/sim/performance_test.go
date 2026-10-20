package sim

// PERF-P2 — performance on the materialized 27-device workspace: the
// materialization itself, a 10-op changeset, a full check run and a path
// query must each stay under the CI-safe bound. The bounds are 6000 ms —
// three times the originally calibrated 2000 ms (#82): wall-clock gates
// are calibrated on idle single-package runs but execute under
// full-package-parallel load (go test runs up to GOMAXPROCS packages at
// once on shared runners), and the sibling test gates measured up to
// 2.8x slower under that contention on a 2-core box. The bounds still
// guard the order-of-magnitude class of regressions they were written
// for. The measured numbers are logged with the PERF-P2 prefix and go
// into the docs.

import (
	"context"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
)

func TestSimulationPerformance(t *testing.T) {
	svc, db := newEngine(t)
	batches := importWorkbooks(t, db)
	ctx := context.Background()

	// Materialize.
	start := time.Now()
	res, err := svc.Materialize(ctx, "perf", "", batches)
	elapsedMaterialize := time.Since(start)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if ms := elapsedMaterialize.Milliseconds(); ms >= 6000 {
		t.Fatalf("Materialize took %d ms, bound is 6000 ms (#82 contention-scaled from 2000)", ms)
	}
	t.Logf("PERF-P2 Materialize (27 devices / 40 interfaces / 20 links): %d ms", elapsedMaterialize.Milliseconds())

	// A second materialization into the same database (fresh ids) to
	// measure the read+write path on warm caches as well.
	start = time.Now()
	if _, err := svc.Materialize(ctx, "perf-2", "", batches); err != nil {
		t.Fatalf("Materialize(2): %v", err)
	}
	t.Logf("PERF-P2 Materialize #2 (same batches, warm cache): %d ms", time.Since(start).Milliseconds())

	// ApplyOps of 10 ops (no auto-checks — the check run is measured
	// separately below).
	ops := make([]Op, 0, 10)
	for i := 1; i <= 10; i++ {
		ops = append(ops, configureIface(
			domain.FormatSimID(domain.SeqInterface, i),
			"10.7."+itoa(i)+".1", 24))
	}
	start = time.Now()
	if _, err := svc.ApplyOps(ctx, res.WorkspaceID, ops, "perf: 10 ops", false); err != nil {
		t.Fatalf("ApplyOps(10): %v", err)
	}
	elapsedApply := time.Since(start)
	if ms := elapsedApply.Milliseconds(); ms >= 6000 {
		t.Fatalf("ApplyOps of 10 ops took %d ms, bound is 6000 ms (#82 contention-scaled from 2000)", ms)
	}
	t.Logf("PERF-P2 ApplyOps (10 config ops, 1 transaction): %d ms", elapsedApply.Milliseconds())

	// ApplyOps including the auto-check run (the full §4 pipeline).
	start = time.Now()
	if _, err := svc.ApplyOps(ctx, res.WorkspaceID, []Op{
		{Op: domain.OpSetDeviceStatus, DeviceID: "vd-2", Status: "down"},
	}, "perf: 1 op + auto checks", true); err != nil {
		t.Fatalf("ApplyOps(+checks): %v", err)
	}
	t.Logf("PERF-P2 ApplyOps (1 op + auto-check run): %d ms", time.Since(start).Milliseconds())

	// Full check run.
	start = time.Now()
	run, err := svc.RunChecks(ctx, res.WorkspaceID, domain.CheckTriggerManual)
	if err != nil {
		t.Fatalf("RunChecks: %v", err)
	}
	elapsedChecks := time.Since(start)
	if ms := elapsedChecks.Milliseconds(); ms >= 6000 {
		t.Fatalf("RunChecks took %d ms, bound is 6000 ms (#82 contention-scaled from 2000)", ms)
	}
	t.Logf("PERF-P2 RunChecks (%d result rows): %d ms", len(run.Results), elapsedChecks.Milliseconds())

	// Path query (DEV-002 is down from the previous step, so route the
	// query along the live segment DEV-003→DEV-014).
	start = time.Now()
	p, err := svc.Path(ctx, res.WorkspaceID, "DEV-003", "DEV-014")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	elapsedPath := time.Since(start)
	if ms := elapsedPath.Milliseconds(); ms >= 6000 {
		t.Fatalf("Path took %d ms, bound is 6000 ms (#82 contention-scaled from 2000)", ms)
	}
	if !p.Reachable {
		t.Fatalf("Path(DEV-003→DEV-014) must be reachable")
	}
	t.Logf("PERF-P2 Path (%d-hop BFS): %d ms", p.PathLength, elapsedPath.Milliseconds())

	// Topology + hash (the payload assembly) for completeness.
	start = time.Now()
	topo := mustTopology(t, svc, res.WorkspaceID)
	_ = topo
	t.Logf("PERF-P2 Topology + hash (27 nodes / 20 edges): %d ms", time.Since(start).Milliseconds())
}
