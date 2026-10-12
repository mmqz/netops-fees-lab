package app

// Issue #6 / #8 e2e regression tests (CLI §10 stdout contract):
//
//   - #6: `report --format json|html --out <file>` must print exactly one
//     JSON summary object on stdout (used to exit 0 with silent stdout —
//     indistinguishable from a failure in scripts).
//   - #8: `lab workspace create` output carries the db field (the absolute
//     path the workspace lives in) so it can be diffed against the serve
//     binding.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// importFixtures imports the three committed testdata workbooks with their
// suggested mappings — the minimal §10 journey the issue #6/#8 e2e tests
// need (64 resources, matching the seeded counts of TestCLIEndToEnd).
func importFixtures(t *testing.T, db string) {
	t.Helper()
	root := repoRoot(t)
	tmp := t.TempDir()
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
}

// TestCLIReportOutStdoutSummary walks the §10 journey minimally (one import)
// and pins the stdout summary of report --out for BOTH formats.
func TestCLIReportOutStdoutSummary(t *testing.T) {
	tmp := t.TempDir()
	db := filepath.Join(tmp, "netops.db")

	importFixtures(t, db)

	for _, format := range []string{"json", "html"} {
		out := filepath.Join(tmp, "report."+format)
		stdout, code := runCLI(t, "report", "--db", db, "--format", format, "--out", out)
		if code != 0 {
			t.Fatalf("report --format %s --out: exit %d, want 0\nstdout: %s", format, code, stdout)
		}
		// §10: stdout carries EXACTLY one JSON object — parseable, non-empty,
		// and the file exists.
		var summary struct {
			File             string         `json:"file"`
			Format           string         `json:"format"`
			Scope            string         `json:"scope"`
			TotalResources   int            `json:"totalResources"`
			QualityScore     int            `json:"qualityScore"`
			IssuesBySeverity map[string]int `json:"issuesBySeverity"`
		}
		if err := json.Unmarshal([]byte(stdout), &summary); err != nil {
			t.Fatalf("report --format %s --out: stdout is not one JSON object: %v\nstdout: %s", format, err, stdout)
		}
		if summary.File != out || summary.Format != format {
			t.Errorf("report %s summary: file/format = %q/%q, want %q/%q", format, summary.File, summary.Format, out, format)
		}
		if summary.Scope != "global" {
			t.Errorf("report %s summary: scope = %q, want global", format, summary.Scope)
		}
		if summary.TotalResources != 64 {
			t.Errorf("report %s summary: totalResources = %d, want 64", format, summary.TotalResources)
		}
		if len(summary.IssuesBySeverity) != 3 {
			t.Errorf("report %s summary: issuesBySeverity = %v, want all three severities", format, summary.IssuesBySeverity)
		}
		if info, err := os.Stat(out); err != nil || info.Size() == 0 {
			t.Errorf("report %s artifact: %v (size %d), want a non-empty file at %s", format, err, info.Size(), out)
		}
	}
}

// TestCLILabCreateCarriesDB pins the issue #8 output field: the workspace
// create JSON answers db — the absolute path of the database the workspace
// was materialized into.
func TestCLILabCreateCarriesDB(t *testing.T) {
	tmp := t.TempDir()
	db := filepath.Join(tmp, "netops.db")

	importFixtures(t, db)

	stdout, code := runCLI(t, "lab", "workspace", "create",
		"--db", db, "--name", "跨库演示", "--all-batches")
	if code != 0 {
		t.Fatalf("lab workspace create: exit %d, want 0\nstdout: %s", code, stdout)
	}
	var create struct {
		WorkspaceID string `json:"workspaceId"`
		DB          string `json:"db"`
	}
	if err := json.Unmarshal([]byte(stdout), &create); err != nil {
		t.Fatalf("lab workspace create: stdout is not one JSON object: %v\nstdout: %s", err, stdout)
	}
	if create.WorkspaceID == "" {
		t.Fatalf("lab workspace create: workspaceId is empty: %s", stdout)
	}
	if abs, err := filepath.Abs(db); err != nil || create.DB != abs {
		t.Errorf("lab workspace create: db = %q, want the absolute path %q", create.DB, abs)
	}
}
