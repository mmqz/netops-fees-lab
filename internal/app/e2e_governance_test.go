package app

// W14-D CLI e2e: `netopslab query --db <path> governance` — the CLI twin of
// GET /api/governance/summary. The contract test walks the real §10 journey
// (analyze → import ×3 over the committed seeded workbooks) so the asserted
// aggregate is the REAL imported state (64 resources, 57 relations, the
// E13/W3/I1 issue mix whose by-rule top-10 happens to be the whole catalog),
// then pins the empty-database all-zero output and the usage error of an
// unknown flag.

import (
	"encoding/json"
	"github.com/mmqz/netops-lab/internal/testsupport"
	"testing"
)

// TestCLIGovernanceSummary pins the governance JSON contract: one JSON
// object on stdout, exit 0, every field of the aggregate matching the
// imported state of the three seeded workbooks.
func TestCLIGovernanceSummary(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopslab binary was not built (go tool unavailable)")
	}
	dir := testsupport.TempDir(t)
	db := dir + "/netops.db"

	// Seed the three committed workbooks through the CLI itself (the
	// suggested mapping flow of e2e_export_test.go).
	for n, file := range []string{"devices.xlsx", "ippools.xlsx", "circuits.xlsx"} {
		importSuggestedCLI(t, db, repoRoot(t)+"/testdata/"+file, n+1)
	}

	out, code := runCLIJSON(t, "query governance", "query", "--db", db, "governance")
	if code != 0 {
		t.Fatalf("query governance: exit %d, want 0", code)
	}

	// pending / decided: no confirmation work happened on this database.
	pending, ok := out["pending"].(map[string]any)
	if !ok {
		t.Fatalf("query governance: pending is %T, want an object: %v", out["pending"], out)
	}
	if pending["attributeChanges"] != float64(0) || pending["renameProposals"] != float64(0) {
		t.Errorf("pending = %v, want zeros", pending)
	}
	decided, ok := out["decided"].(map[string]any)
	if !ok {
		t.Fatalf("query governance: decided is %T, want an object", out["decided"])
	}
	if ac, ok := decided["attributeChanges"].(map[string]any); !ok ||
		ac["applied"] != float64(0) || ac["dismissed"] != float64(0) {
		t.Errorf("decided.attributeChanges = %v, want applied/dismissed zeros", decided["attributeChanges"])
	}
	if rp, ok := decided["renameProposals"].(map[string]any); !ok ||
		rp["applied"] != float64(0) || rp["rejected"] != float64(0) {
		t.Errorf("decided.renameProposals = %v, want applied/rejected zeros", decided["renameProposals"])
	}

	// issues: the E13/W3/I1 seed mix; byRule top-10 = the whole catalog in
	// (count DESC, code ASC) order: E004:4, E002:3, E001:2, E003:2, then the
	// six singletons alphabetically.
	issues, ok := out["issues"].(map[string]any)
	if !ok {
		t.Fatalf("query governance: issues is %T, want an object", out["issues"])
	}
	if sev, ok := issues["bySeverity"].(map[string]any); !ok ||
		sev["error"] != float64(13) || sev["warning"] != float64(3) || sev["info"] != float64(1) {
		t.Errorf("issues.bySeverity = %v, want error 13 / warning 3 / info 1", issues["bySeverity"])
	}
	byRule, ok := issues["byRule"].([]any)
	if !ok {
		t.Fatalf("query governance: issues.byRule is %T, want an array", issues["byRule"])
	}
	wantRule := []struct {
		rule  string
		count float64
	}{
		{"E004", 4}, {"E002", 3}, {"E001", 2}, {"E003", 2},
		{"E005", 1}, {"E006", 1}, {"I001", 1}, {"W001", 1}, {"W002", 1}, {"W003", 1},
	}
	if len(byRule) != len(wantRule) {
		t.Fatalf("issues.byRule length = %d, want %d (the whole catalog): %v", len(byRule), len(wantRule), byRule)
	}
	for i, want := range wantRule {
		entry, ok := byRule[i].(map[string]any)
		if !ok {
			t.Fatalf("issues.byRule[%d] is %T, want an object", i, byRule[i])
		}
		if entry["rule"] != want.rule || entry["count"] != want.count {
			t.Errorf("issues.byRule[%d] = %v, want %s:%d (count DESC, code ASC)", i, entry, want.rule, int(want.count))
		}
	}

	// inventory shapes: 64 resources (27/16/21), 57 relations (15/21/21).
	resources, ok := out["resources"].(map[string]any)
	if !ok {
		t.Fatalf("query governance: resources is %T, want an object", out["resources"])
	}
	if resources["total"] != float64(64) {
		t.Errorf("resources.total = %v, want 64", resources["total"])
	}
	if bt, ok := resources["byType"].(map[string]any); !ok ||
		bt["device"] != float64(27) || bt["ip_pool"] != float64(16) || bt["circuit"] != float64(21) {
		t.Errorf("resources.byType = %v, want device 27 / ip_pool 16 / circuit 21", resources["byType"])
	}
	relations, ok := out["relations"].(map[string]any)
	if !ok {
		t.Fatalf("query governance: relations is %T, want an object", out["relations"])
	}
	if relations["total"] != float64(57) {
		t.Errorf("relations.total = %v, want 57", relations["total"])
	}
	if bt, ok := relations["byType"].(map[string]any); !ok ||
		bt["assigned_to"] != float64(15) || bt["a_end"] != float64(21) || bt["z_end"] != float64(21) {
		t.Errorf("relations.byType = %v, want assigned_to 15 / a_end 21 / z_end 21", relations["byType"])
	}
	batches, ok := out["batches"].(map[string]any)
	if !ok {
		t.Fatalf("query governance: batches is %T, want an object", out["batches"])
	}
	if batches["total"] != float64(3) {
		t.Errorf("batches.total = %v, want 3", batches["total"])
	}
	if bs, ok := batches["byStatus"].(map[string]any); !ok || bs["completed"] != float64(3) {
		t.Errorf("batches.byStatus = %v, want completed 3", batches["byStatus"])
	}
	if _, ok := out["generatedAt"].(string); !ok {
		t.Errorf("generatedAt = %T, want an RFC3339 string", out["generatedAt"])
	}
}

// TestCLIGovernanceEmpty pins the empty-database contract: exit 0 with the
// all-zero structure (fixed map keys at 0, empty dynamic maps, byRule []).
func TestCLIGovernanceEmpty(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopslab binary was not built (go tool unavailable)")
	}
	db := testsupport.TempDir(t) + "/netops.db"

	out, code := runCLIJSON(t, "query governance empty", "query", "--db", db, "governance")
	if code != 0 {
		t.Fatalf("query governance on empty db: exit %d, want 0", code)
	}
	if pending := out["pending"].(map[string]any); pending["attributeChanges"] != float64(0) || pending["renameProposals"] != float64(0) {
		t.Errorf("empty pending = %v, want zeros", pending)
	}
	decided := out["decided"].(map[string]any)
	if ac := decided["attributeChanges"].(map[string]any); ac["applied"] != float64(0) || ac["dismissed"] != float64(0) {
		t.Errorf("empty decided.attributeChanges = %v, want zeros", ac)
	}
	if rp := decided["renameProposals"].(map[string]any); rp["applied"] != float64(0) || rp["rejected"] != float64(0) {
		t.Errorf("empty decided.renameProposals = %v, want zeros", rp)
	}
	issues := out["issues"].(map[string]any)
	if sev := issues["bySeverity"].(map[string]any); sev["error"] != float64(0) || sev["warning"] != float64(0) || sev["info"] != float64(0) {
		t.Errorf("empty issues.bySeverity = %v, want the three zeros", sev)
	}
	if byRule, ok := issues["byRule"].([]any); !ok || len(byRule) != 0 {
		t.Errorf("empty issues.byRule = %#v, want []", issues["byRule"])
	}
	if resources := out["resources"].(map[string]any); resources["total"] != float64(0) {
		t.Errorf("empty resources.total = %v, want 0", resources["total"])
	}
	if relations := out["relations"].(map[string]any); relations["total"] != float64(0) {
		t.Errorf("empty relations.total = %v, want 0", relations["total"])
	}
	if batches := out["batches"].(map[string]any); batches["total"] != float64(0) {
		t.Errorf("empty batches.total = %v, want 0", batches["total"])
	}
}

// TestCLIGovernanceUsage pins the exit-2 usage errors of the new
// sub-subcommand: an unknown flag and a positional argument are both usage
// errors (the query-level --db precedes the sub-subcommand — the family
// contract shared with query stats/relations).
func TestCLIGovernanceUsage(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopslab binary was not built (go tool unavailable)")
	}
	db := testsupport.TempDir(t) + "/netops.db"

	for _, args := range [][]string{
		{"query", "--db", db, "governance", "--bogus"},
		{"query", "--db", db, "governance", "extra"},
	} {
		stdout, code := runCLI(t, args...)
		if code != 2 {
			t.Fatalf("%v: exit %d, want 2 (usage error)\nstdout: %s", args, code, stdout)
		}
		var e map[string]any
		if err := json.Unmarshal([]byte(stdout), &e); err != nil {
			t.Fatalf("%v: stdout is not JSON: %s", args, stdout)
		}
		if errObj, _ := e["error"].(map[string]any); errObj["code"] != "usage_error" {
			t.Errorf("%v: error.code = %v, want usage_error", args, errObj["code"])
		}
	}
}
