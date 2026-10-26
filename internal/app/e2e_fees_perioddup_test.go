package app

// e2e_fees_perioddup_test.go — issue #20's CLI end-to-end: the cross-period
// duplicate refusal (both periods + batch id + override flag in the text),
// the --allow-period-duplicate override with its audit trail, and the
// verify-batches report (exit discipline of verify-sources' family).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIFeesCrossPeriodDuplicateAndVerify(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopsfees binary was not built (go tool unavailable)")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "netops.db")
	src := filepath.Join(tmp, "dup.csv")
	if err := os.WriteFile(src, []byte("站址编码,电费(元)\nHLJ-01,100.00\nHLJ-02,200.00\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. first import: 2026-01, clean.
	out, code := runCLI(t, "fees", "--db", db, "--data", tmp,
		"import", "--file", src, "--type", "electricity", "--year", "2026", "--month", "1")
	if code != 0 {
		t.Fatalf("first import exit %d: %s", code, out)
	}

	// 2. same file, other month: REFUSED, text carries both periods + the flag.
	out, code = runCLI(t, "fees", "--db", db, "--data", tmp,
		"import", "--file", src, "--type", "electricity", "--year", "2026", "--month", "5")
	if code == 0 {
		t.Fatalf("cross-period import must refuse, stdout: %s", out)
	}
	for _, want := range []string{"2026-01", "2026-05", "--allow-period-duplicate"} {
		if !strings.Contains(out, want) {
			t.Errorf("refusal must contain %q: %s", want, out)
		}
	}

	// 3. the override proceeds and the summary carries the audit warning.
	out, code = runCLI(t, "fees", "--db", db, "--data", tmp,
		"import", "--file", src, "--type", "electricity", "--year", "2026", "--month", "5",
		"--allow-period-duplicate")
	if code != 0 {
		t.Fatalf("override import exit %d: %s", code, out)
	}
	var sum struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &sum); err != nil {
		t.Fatalf("decode summary: %v\n%s", err, out)
	}
	var audited bool
	for _, w := range sum.Warnings {
		if strings.Contains(w, "跨账期重复覆盖") {
			audited = true
		}
	}
	if !audited {
		t.Fatalf("warnings = %v, want the override audit note", sum.Warnings)
	}

	// 4. verify-batches: the same-sha family surfaces, exit non-zero.
	out, code = runCLI(t, "fees", "--db", db, "--data", tmp,
		"verify-batches")
	if code == 0 {
		t.Fatalf("dirty batches must exit non-zero, stdout: %s", out)
	}
	var rep struct {
		BatchesChecked int `json:"batchesChecked"`
		Problems       []struct {
			Kind    string `json:"kind"`
			BatchID string `json:"batchId"`
			Detail  string `json:"detail"`
		} `json:"problems"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("decode report: %v\n%s", err, out)
	}
	if rep.BatchesChecked != 2 {
		t.Errorf("batchesChecked = %d, want 2", rep.BatchesChecked)
	}
	var dupKinds int
	for _, p := range rep.Problems {
		if p.Kind == "duplicate-sha-period" {
			dupKinds++
			if !strings.Contains(p.Detail, "2026-01") || !strings.Contains(p.Detail, "2026-05") {
				t.Errorf("duplicate problem must name both periods: %s", p.Detail)
			}
		}
	}
	if dupKinds != 2 {
		t.Errorf("duplicate-sha-period problems = %d, want 2 (both members named)", dupKinds)
	}

	// 5. usage discipline: stray arguments refuse.
	out, code = runCLI(t, "fees", "--db", db, "--data", tmp,
		"verify-batches", "unexpected")
	if code == 0 {
		t.Fatalf("stray argument must exit non-zero, stdout: %s", out)
	}
}
