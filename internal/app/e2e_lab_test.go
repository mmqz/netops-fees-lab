package app

// E2E-P2 — the repeatable CLI lab demo (PROJECT_BRIEF_P2 §11): fresh
// database → import the three workbooks → materialize the workspace →
// baseline checks → apply the fault-injection ops file (two identical
// addresses + one link down) → checks --run → path over the broken link →
// diff before/after → undo → verify recovery → checkpoint create/restore.
// Every step asserts the single-JSON-object stdout contract and its key
// fields; the binary is the one TestMain (e2e_test.go) built once.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCLILabFlow is E2E-P2.
func TestCLILabFlow(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopslab binary was not built (go tool unavailable)")
	}
	root := repoRoot(t)
	tmp := t.TempDir()
	db := filepath.Join(tmp, "netops.db")

	// ---- import the three committed workbooks (the §10 flow).
	for _, wb := range []string{"devices.xlsx", "ippools.xlsx", "circuits.xlsx"} {
		file := filepath.Join(root, "testdata", wb)
		analysis, code := runCLIJSON(t, "analyze "+wb, "analyze", "--file", file)
		if code != 0 {
			t.Fatalf("analyze %s: exit %d, want 0", wb, code)
		}
		suggested, err := json.Marshal(analysis["suggestedMapping"])
		if err != nil || string(suggested) == "null" {
			t.Fatalf("analyze %s: suggestedMapping missing", wb)
		}
		mapFile := filepath.Join(tmp, "map-"+wb+".json")
		if err := os.WriteFile(mapFile, suggested, 0o644); err != nil {
			t.Fatalf("write map: %v", err)
		}
		summary, code := runCLIJSON(t, "import "+wb, "import", "--file", file, "--map", mapFile, "--db", db)
		if code != 0 {
			t.Fatalf("import %s: exit %d, want 0", wb, code)
		}
		if status, _ := summary["status"].(string); status != "completed" {
			t.Fatalf("import %s: status = %v, want completed", wb, summary["status"])
		}
	}

	// ---- materialize: 27 devices / 40 interfaces / 20 links (seeded data).
	create, code := runCLIJSON(t, "lab workspace create", "lab", "workspace", "create",
		"--db", db, "--name", "演示", "--all-batches")
	if code != 0 {
		t.Fatalf("lab workspace create: exit %d, want 0", code)
	}
	ws, _ := create["workspaceId"].(string)
	if ws == "" {
		t.Fatalf("lab workspace create: workspaceId is empty: %v", create)
	}
	counts, _ := create["counts"].(map[string]any)
	for name, want := range map[string]int{"devices": 27, "interfaces": 40, "links": 20} {
		if got := int(counts[name].(float64)); got != want {
			t.Fatalf("lab workspace create: counts[%s] = %d, want %d", name, got, want)
		}
	}
	baselineHash, _ := create["workspaceHash"].(string)
	if len(baselineHash) != 64 {
		t.Fatalf("lab workspace create: workspaceHash = %q, want a 64-char sha256 hex", baselineHash)
	}
	if got, _ := create["name"].(string); got != "演示" {
		t.Fatalf("lab workspace create: name = %q, want 演示", got)
	}

	// ---- `lab checks` without --run on a workspace without runs: exit 1,
	// error code not_found.
	stdout, code := runCLI(t, "lab", "checks", "--db", db, "--workspace", ws)
	if code != 1 {
		t.Fatalf("lab checks without a run: exit %d, want 1 (stdout %s)", code, stdout)
	}
	var errOut map[string]any
	if err := json.Unmarshal([]byte(stdout), &errOut); err != nil {
		t.Fatalf("lab checks (no run): stdout is not one JSON object: %v\n%s", err, stdout)
	}
	errBody, _ := errOut["error"].(map[string]any)
	if errBody["code"] != "not_found" {
		t.Fatalf("lab checks (no run): error.code = %v, want not_found", errBody["code"])
	}

	// ---- baseline run (`lab checks --run`).
	baselineRun, code := runCLIJSON(t, "lab checks --run", "lab", "checks", "--db", db, "--workspace", ws, "--run")
	if code != 0 {
		t.Fatalf("lab checks --run: exit %d, want 0", code)
	}
	baselineRunID, _ := baselineRun["runId"].(string)
	if baselineRunID != "run-1" {
		t.Fatalf("baseline run id = %q, want run-1 (deterministic sequences)", baselineRunID)
	}
	if trigger, _ := baselineRun["trigger"].(string); trigger != "manual" {
		t.Fatalf("baseline run trigger = %q, want manual", trigger)
	}
	if hash, _ := baselineRun["workspaceHash"].(string); hash != baselineHash {
		t.Fatalf("baseline run hash = %q, want the materialize hash", hash)
	}

	// ---- the ops file: two interfaces configured with the SAME address
	// (CHK-001) plus the unique-path link down (ACC-P2-5 pair).
	opsFile := filepath.Join(tmp, "ops.json")
	opsJSON := `{
  "ops": [
    {"op": "config_interface_ipv4", "interfaceId": "if-1", "ipv4Addr": "10.0.0.1", "ipv4Prefix": 24},
    {"op": "config_interface_ipv4", "interfaceId": "if-2", "ipv4Addr": "10.0.0.1", "ipv4Prefix": 24},
    {"op": "set_link_status", "linkId": "ln-2", "status": "down"}
  ],
  "description": "故障注入演示"
}`
	if err := os.WriteFile(opsFile, []byte(opsJSON), 0o644); err != nil {
		t.Fatalf("write ops.json: %v", err)
	}
	apply, code := runCLIJSON(t, "lab apply", "lab", "apply", "--db", db, "--workspace", ws, "--ops", opsFile)
	if code != 0 {
		t.Fatalf("lab apply: exit %d, want 0", code)
	}
	if cs, _ := apply["changesetId"].(string); cs != "cs-1" {
		t.Fatalf("lab apply: changesetId = %v, want cs-1", apply["changesetId"])
	}
	if trigger, _ := apply["trigger"].(string); trigger != "user" {
		t.Fatalf("lab apply: trigger = %q, want user", trigger)
	}
	if got := int(apply["opsApplied"].(float64)); got != 3 {
		t.Fatalf("lab apply: opsApplied = %d, want 3", got)
	}
	applyChecks, _ := apply["checks"].(map[string]any)
	if applyChecks == nil {
		t.Fatalf("lab apply: auto-checks summary missing (runChecks defaults to true)")
	}
	faultRunID, _ := applyChecks["runId"].(string)
	if faultRunID != "run-2" {
		t.Fatalf("lab apply: auto run = %q, want run-2", faultRunID)
	}
	faultCounts, _ := applyChecks["counts"].(map[string]any)
	if got := int(faultCounts["error"].(float64)); got < 1 {
		t.Fatalf("lab apply: auto-run error findings = %d, want ≥ 1 (the duplicate address)", got)
	}

	// ---- `lab checks --run` after the fault: the CHK-001 finding is
	// visible with error ≥ 1.
	postRun, code := runCLIJSON(t, "lab checks --run (faulted)", "lab", "checks", "--db", db, "--workspace", ws, "--run")
	if code != 0 {
		t.Fatalf("lab checks --run (faulted): exit %d, want 0", code)
	}
	postCounts, _ := postRun["counts"].(map[string]any)
	if got := int(postCounts["error"].(float64)); got < 1 {
		t.Fatalf("lab checks --run (faulted): error findings = %d, want ≥ 1", got)
	}

	// ---- the downed link breaks the DEV-001 → DEV-003 path.
	path, code := runCLIJSON(t, "lab path (faulted)", "lab", "path", "--db", db, "--workspace", ws,
		"--from", "DEV-001", "--to", "DEV-003")
	if code != 0 {
		t.Fatalf("lab path (faulted): exit %d, want 0", code)
	}
	if reachable, _ := path["reachable"].(bool); reachable {
		t.Fatalf("lab path (faulted): reachable = true, want false (CKT-002 down cuts the unique path)")
	}

	// ---- diff run-1 → run-2: the pair is among the lost connectivity.
	diff, code := runCLIJSON(t, "lab diff", "lab", "diff", "--db", db, "--workspace", ws,
		"--from", baselineRunID, "--to", faultRunID)
	if code != 0 {
		t.Fatalf("lab diff: exit %d, want 0", code)
	}
	lost, _ := diff["connectivityLost"].([]any)
	foundPair := false
	for _, rawPair := range lost {
		pair, _ := rawPair.(map[string]any)
		if pair["fromDevice"] == "DEV-001" && pair["toDevice"] == "DEV-003" {
			foundPair = true
		}
	}
	if !foundPair {
		t.Fatalf("lab diff: connectivityLost misses the DEV-001→DEV-003 pair: %v", lost)
	}
	if delta := int(diff["scoreDelta"].(float64)); delta >= 0 {
		t.Fatalf("lab diff: scoreDelta = %d, want negative (faults injected)", delta)
	}
	added, _ := diff["findingsAdded"].([]any)
	sawCHK001 := false
	for _, rawFinding := range added {
		finding, _ := rawFinding.(map[string]any)
		if finding["checkCode"] == "CHK-001" {
			sawCHK001 = true
			if msg, _ := finding["message"].(string); !strings.Contains(msg, "duplicate host address") {
				t.Fatalf("lab diff: CHK-001 message = %q", msg)
			}
		}
	}
	if !sawCHK001 {
		t.Fatalf("lab diff: findingsAdded misses the CHK-001 duplicate-address finding")
	}

	// ---- undo (default target: the latest applied changeset) → the path is
	// back with identical hops.
	undo, code := runCLIJSON(t, "lab undo", "lab", "undo", "--db", db, "--workspace", ws)
	if code != 0 {
		t.Fatalf("lab undo: exit %d, want 0", code)
	}
	if reverted, _ := undo["reverted"].(string); reverted != "cs-1" {
		t.Fatalf("lab undo: reverted = %v, want cs-1", undo["reverted"])
	}
	if trigger, _ := undo["trigger"].(string); trigger != "undo" {
		t.Fatalf("lab undo: trigger = %q, want undo", trigger)
	}
	if got := int(undo["opsApplied"].(float64)); got != 3 {
		t.Fatalf("lab undo: opsApplied = %d, want 3", got)
	}
	if hash, _ := undo["workspaceHash"].(string); hash != baselineHash {
		t.Fatalf("lab undo: workspaceHash = %q, want the materialize baseline hash", hash)
	}
	if undo["checks"] == nil {
		t.Fatalf("lab undo: checks summary missing (runChecks defaults to true)")
	}
	path, code = runCLIJSON(t, "lab path (recovered)", "lab", "path", "--db", db, "--workspace", ws,
		"--from", "DEV-001", "--to", "DEV-003")
	if code != 0 {
		t.Fatalf("lab path (recovered): exit %d, want 0", code)
	}
	if reachable, _ := path["reachable"].(bool); !reachable {
		t.Fatalf("lab path (recovered): reachable = false, want true after undo")
	}
	hops, _ := path["hops"].([]any)
	wantCodes := []string{"DEV-001", "DEV-002", "DEV-003"}
	if len(hops) != len(wantCodes) {
		t.Fatalf("lab path (recovered): %d hops, want %d", len(hops), len(wantCodes))
	}
	for i, rawHop := range hops {
		hop, _ := rawHop.(map[string]any)
		if hop["code"] != wantCodes[i] {
			t.Fatalf("lab path (recovered): hop %d = %v, want %s", i, hop["code"], wantCodes[i])
		}
	}

	// ---- checkpoint create: hash equals the (restored) baseline state.
	cp, code := runCLIJSON(t, "lab checkpoint --create", "lab", "checkpoint", "--db", db,
		"--workspace", ws, "--create", "--label", "基线")
	if code != 0 {
		t.Fatalf("lab checkpoint --create: exit %d, want 0", code)
	}
	cpID, _ := cp["checkpointId"].(string)
	if cpID != "cp-1" {
		t.Fatalf("lab checkpoint --create: checkpointId = %v, want cp-1", cp["checkpointId"])
	}
	if label, _ := cp["label"].(string); label != "基线" {
		t.Fatalf("lab checkpoint --create: label = %q, want 基线", label)
	}
	if hash, _ := cp["workspaceHash"].(string); hash != baselineHash {
		t.Fatalf("lab checkpoint --create: workspaceHash = %q, want the baseline hash", hash)
	}

	// ---- break again, then restore: back at the checkpoint state.
	breakFile := filepath.Join(tmp, "break.json")
	breakJSON := `{"ops": [{"op": "set_link_status", "linkId": "ln-2", "status": "down"}], "description": "再破坏"}`
	if err := os.WriteFile(breakFile, []byte(breakJSON), 0o644); err != nil {
		t.Fatalf("write break.json: %v", err)
	}
	if _, code := runCLIJSON(t, "lab apply (break)", "lab", "apply", "--db", db, "--workspace", ws,
		"--ops", breakFile, "--no-checks"); code != 0 {
		t.Fatalf("lab apply (break): exit %d, want 0", code)
	}
	restore, code := runCLIJSON(t, "lab checkpoint --restore", "lab", "checkpoint", "--db", db,
		"--workspace", ws, "--restore", "--id", cpID)
	if code != 0 {
		t.Fatalf("lab checkpoint --restore: exit %d, want 0", code)
	}
	if cs, _ := restore["changesetId"].(string); cs == "" {
		t.Fatalf("lab checkpoint --restore: changesetId is empty, want the restore changeset")
	}
	if got := int(restore["opsApplied"].(float64)); got != 1 {
		t.Fatalf("lab checkpoint --restore: opsApplied = %d, want 1 (link back up)", got)
	}
	if hash, _ := restore["restoredToHash"].(string); hash != baselineHash {
		t.Fatalf("lab checkpoint --restore: restoredToHash = %q, want the checkpoint hash", hash)
	}
	if hash, _ := restore["workspaceHash"].(string); hash != baselineHash {
		t.Fatalf("lab checkpoint --restore: workspaceHash = %q, want the checkpoint hash (exact round-trip)", hash)
	}
	if restore["checks"] == nil {
		t.Fatalf("lab checkpoint --restore: checks summary missing")
	}
	// The path works again after the restore.
	path, code = runCLIJSON(t, "lab path (restored)", "lab", "path", "--db", db, "--workspace", ws,
		"--from", "DEV-001", "--to", "DEV-003")
	if code != 0 || !path["reachable"].(bool) {
		t.Fatalf("lab path (restored): reachable = %v (exit %d), want true", path["reachable"], code)
	}

	// ---- checkpoint list and the no-op restore.
	list, code := runCLIJSON(t, "lab checkpoint --list", "lab", "checkpoint", "--db", db,
		"--workspace", ws, "--list")
	if code != 0 {
		t.Fatalf("lab checkpoint --list: exit %d, want 0", code)
	}
	if total := int(list["total"].(float64)); total != 1 {
		t.Fatalf("lab checkpoint --list: total = %d, want 1", total)
	}
	noop, code := runCLIJSON(t, "lab checkpoint --restore (no-op)", "lab", "checkpoint", "--db", db,
		"--workspace", ws, "--restore", "--id", cpID)
	if code != 0 {
		t.Fatalf("lab checkpoint --restore (no-op): exit %d, want 0", code)
	}
	if cs, _ := noop["changesetId"].(string); cs != "" {
		t.Fatalf("lab checkpoint --restore (no-op): changesetId = %q, want empty", cs)
	}
	if note, _ := noop["note"].(string); note != "already at checkpoint state" {
		t.Fatalf("lab checkpoint --restore (no-op): note = %q, want the no-op note", note)
	}

	// ---- topology payload: 27 nodes / 20 edges at the checkpoint state.
	topo, code := runCLIJSON(t, "lab topology", "lab", "topology", "--db", db, "--workspace", ws)
	if code != 0 {
		t.Fatalf("lab topology: exit %d, want 0", code)
	}
	if nodes, _ := topo["nodes"].([]any); len(nodes) != 27 {
		t.Fatalf("lab topology: %d nodes, want 27", len(nodes))
	}
	if edges, _ := topo["edges"].([]any); len(edges) != 20 {
		t.Fatalf("lab topology: %d edges, want 20", len(edges))
	}
	if hash, _ := topo["workspaceHash"].(string); hash != baselineHash {
		t.Fatalf("lab topology: workspaceHash = %q, want the checkpoint hash", hash)
	}

	// ---- usage errors: exit 2.
	if _, code := runCLI(t, "lab"); code != 2 {
		t.Fatalf("lab without subcommand: exit %d, want 2", code)
	}
	if _, code := runCLI(t, "lab", "checkpoint", "--db", db, "--workspace", ws); code != 2 {
		t.Fatalf("lab checkpoint without a mode: exit %d, want 2", code)
	}
	if _, code := runCLI(t, "lab", "workspace", "delete", "--db", db); code != 2 {
		t.Fatalf("lab workspace delete: exit %d, want 2 (only create exists)", code)
	}
}
