package app

// e2e_verify_sources_test.go — issue #6 at the real process boundary: the
// import records the source path, verify-sources answers match, a source
// edit flips to changed AND exits non-zero (single JSON preserved), the
// deleted source answers missing, and the snapshot stays intact through
// it all.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeLedgerCSV(t *testing.T, dir, name, site, amount string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	body := "站址编码,电费(元),账期\n" + site + "," + amount + ",2026-01\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	return path
}

func runVerify(t *testing.T, db, dataDir string) (map[string]any, int) {
	t.Helper()
	out, code := runCLI(t, "fees", "--db", db, "--data", dataDir, "verify-sources")
	var rep map[string]any
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("verify-sources stdout is not the single JSON: %v\n%s", err, out)
	}
	return rep, code
}

func TestE2EVerifySources(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopsfees binary was not built (go tool unavailable)")
	}
	dir := t.TempDir()
	src := writeLedgerCSV(t, dir, "01月台账.csv", "HLJ-0001", "100.00")
	db := filepath.Join(dir, "netops.db")

	// 1. Import — the writable-source warning rides the summary JSON.
	out, code := runCLI(t, "fees", "--db", db, "--data", dir,
		"import", "--file", src, "--type", "electricity", "--year", "2026", "--month", "1")
	if code != 0 {
		t.Fatalf("import exit = %d:\n%s", code, out)
	}
	var imp struct {
		BatchID  string   `json:"batchId"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &imp); err != nil {
		t.Fatalf("import stdout: %v\n%s", err, out)
	}
	if len(imp.Warnings) == 0 {
		t.Fatalf("a 0o644 source must carry the read-only advice:\n%s", out)
	}

	// 2. Fresh verify: match, exit 0.
	rep, code := runVerify(t, db, dir)
	if code != 0 {
		t.Fatalf("fresh verify exit = %d", code)
	}
	if rep["match"].(float64) != 1 || rep["changed"].(float64) != 0 {
		t.Fatalf("fresh verify = %v", rep)
	}

	// 3. Tamper the source: changed + non-zero exit.
	if err := os.WriteFile(src, []byte("tampered after import"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, code = runVerify(t, db, dir)
	if code == 0 {
		t.Fatalf("changed source must exit non-zero:\n%v", rep)
	}
	if rep["changed"].(float64) != 1 {
		t.Fatalf("tampered verify = %v", rep)
	}

	// 4. Delete the source: missing, still explicit, snapshot intact.
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	rep, _ = runVerify(t, db, dir)
	if rep["missing"].(float64) != 1 {
		t.Fatalf("deleted verify = %v", rep)
	}
	results := rep["results"].([]any)
	first := results[0].(map[string]any)
	if first["snapshotIntact"] != true {
		t.Fatalf("snapshot must stay intact (the evidence fallback):\n%v", first)
	}
}
