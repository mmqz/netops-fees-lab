package sim

// PERF-P2 — performance on the materialized 27-device workspace: the
// materialization itself, a 10-op changeset, a full check run and a path
// query must each stay under the CPU bound (4 CPU-seconds per step).
// History: the bounds were wall-milliseconds — 2000 calibrated idle,
// contention-scaled to 6000 after #82 observed 2.8× sibling-package
// inflation — but wall gates can never be load-immune (the fees fuzzy
// gate later measured 1.38 s idle vs 15.2 s loaded for byte-identical
// code on the same box), so the gates moved to CPU-seconds, which stay
// flat under core theft. The measured numbers are logged with the
// PERF-P2 prefix (wall + CPU) and go into the docs.

import (
	"context"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/testsupport"
)

// cpuBound gates one measured step on CPU-seconds instead of wall
// milliseconds (#82 second edition: the 6000 ms wall bounds still
// false-red when sibling test processes steal the cores — the fees
// fuzzy gate observed 1.38 s idle vs 15.2 s loaded for byte-identical
// code on the same box). CPU time is load-immune for this engine's
// work (in-process SQLite via modernc is CPU-bound SQL work; stolen
// cores inflate waiting, not computing) while order-of-magnitude
// regressions cost proportionally more CPU and stay caught. Wall
// milliseconds are still logged for the PERF-P2 docs lines.
func cpuBound(t *testing.T, step string, cpu time.Duration, wall time.Duration) {
	t.Helper()
	if cpu > 4*time.Second {
		t.Fatalf("%s cost %v CPU-seconds, bound is 4 (#82 load-immune re-calibration of the 6000 ms wall bound; wall was %v)", step, cpu, wall)
	}
}

func TestSimulationPerformance(t *testing.T) {
	svc, db := newEngine(t)
	batches := importWorkbooks(t, db)
	ctx := context.Background()

	// Materialize.
	cpuStart, err := testsupport.CPUTime()
	if err != nil {
		t.Fatalf("read CPU time: %v", err)
	}
	start := time.Now()
	res, err := svc.Materialize(ctx, "perf", "", batches)
	elapsedMaterialize := time.Since(start)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	cpuEnd, err := testsupport.CPUTime()
	if err != nil {
		t.Fatalf("read CPU time: %v", err)
	}
	cpuMaterialize := cpuEnd - cpuStart
	cpuBound(t, "Materialize", cpuMaterialize, elapsedMaterialize)
	t.Logf("PERF-P2 Materialize (27 devices / 40 interfaces / 20 links): %d ms wall, %v CPU", elapsedMaterialize.Milliseconds(), cpuMaterialize)

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
	cpuStart, err = testsupport.CPUTime()
	if err != nil {
		t.Fatalf("read CPU time: %v", err)
	}
	if _, err := svc.ApplyOps(ctx, res.WorkspaceID, ops, "perf: 10 ops", false); err != nil {
		t.Fatalf("ApplyOps(10): %v", err)
	}
	elapsedApply := time.Since(start)
	cpuEnd, err = testsupport.CPUTime()
	if err != nil {
		t.Fatalf("read CPU time: %v", err)
	}
	cpuBound(t, "ApplyOps of 10 ops", cpuEnd-cpuStart, elapsedApply)
	t.Logf("PERF-P2 ApplyOps (10 config ops, 1 transaction): %d ms wall, %v CPU", elapsedApply.Milliseconds(), cpuEnd-cpuStart)

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
	cpuStart, err = testsupport.CPUTime()
	if err != nil {
		t.Fatalf("read CPU time: %v", err)
	}
	run, err := svc.RunChecks(ctx, res.WorkspaceID, domain.CheckTriggerManual)
	if err != nil {
		t.Fatalf("RunChecks: %v", err)
	}
	elapsedChecks := time.Since(start)
	cpuEnd, err = testsupport.CPUTime()
	if err != nil {
		t.Fatalf("read CPU time: %v", err)
	}
	cpuBound(t, "RunChecks", cpuEnd-cpuStart, elapsedChecks)
	t.Logf("PERF-P2 RunChecks (%d result rows): %d ms wall, %v CPU", len(run.Results), elapsedChecks.Milliseconds(), cpuEnd-cpuStart)

	// Path query (DEV-002 is down from the previous step, so route the
	// query along the live segment DEV-003→DEV-014).
	start = time.Now()
	cpuStart, err = testsupport.CPUTime()
	if err != nil {
		t.Fatalf("read CPU time: %v", err)
	}
	p, err := svc.Path(ctx, res.WorkspaceID, "DEV-003", "DEV-014")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	elapsedPath := time.Since(start)
	cpuEnd, err = testsupport.CPUTime()
	if err != nil {
		t.Fatalf("read CPU time: %v", err)
	}
	cpuBound(t, "Path", cpuEnd-cpuStart, elapsedPath)
	if !p.Reachable {
		t.Fatalf("Path(DEV-003→DEV-014) must be reachable")
	}
	t.Logf("PERF-P2 Path (%d-hop BFS): %d ms wall, %v CPU", p.PathLength, elapsedPath.Milliseconds(), cpuEnd-cpuStart)

	// Topology + hash (the payload assembly) for completeness.
	start = time.Now()
	topo := mustTopology(t, svc, res.WorkspaceID)
	_ = topo
	t.Logf("PERF-P2 Topology + hash (27 nodes / 20 edges): %d ms", time.Since(start).Milliseconds())
}
