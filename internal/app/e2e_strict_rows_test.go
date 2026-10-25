package app

// e2e_strict_rows_test.go — issue #8 at the real process boundary: the
// CLI flag reaches the pipeline and the refusal names the cell.
import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestE2EStrictRowsFlag(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopsfees binary was not built (go tool unavailable)")
	}
	tmp := t.TempDir()
	path := filepath.Join(tmp, "mixed.csv")
	body := "站址编码,实际电量,电费(元),账期\n" +
		"HLJ-0001,130,11050,2026-01\n" +
		"HLJ-0002,约一百,20000,2026-01\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(tmp, "netops.db")

	// Default (lenient): both rows in.
	out, code := runCLI(t, "fees", "--db", db, "--data", tmp,
		"import", "--file", path, "--type", "electricity", "--year", "2026", "--month", "1")
	if code != 0 {
		t.Fatalf("lenient exit = %d:\n%s", code, out)
	}
	var sum struct {
		RowsImported int `json:"rowsImported"`
	}
	if err := json.Unmarshal([]byte(out), &sum); err != nil || sum.RowsImported != 2 {
		t.Fatalf("lenient summary: %v (%+v)", err, sum)
	}

	// --strict-rows: THE ROW refuses with its reason (the batch completes
	// with the good row — row-level semantics of #8, not a file refusal).
	db2 := filepath.Join(tmp, "strict.db")
	out2, code := runCLI(t, "fees", "--db", db2, "--data", tmp,
		"import", "--file", path, "--type", "electricity", "--year", "2026", "--month", "1",
		"--strict-rows")
	if code != 0 {
		t.Fatalf("strict row refusal completes the batch (exit 0):\n%s", out2)
	}
	var sum2 struct {
		RowsImported int      `json:"rowsImported"`
		RowsFailed   int      `json:"rowsFailed"`
		Errors       []string `json:"errors"`
	}
	if err := json.Unmarshal([]byte(out2), &sum2); err != nil {
		t.Fatalf("strict stdout must stay the single JSON envelope: %v\n%s", err, out2)
	}
	if sum2.RowsImported != 1 || sum2.RowsFailed != 1 {
		t.Fatalf("strict outcome = %+v, want 1 imported 1 refused", sum2)
	}
	found := false
	for _, e := range sum2.Errors {
		if containsSub(e, "约一百") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the refusal must name the raw value, got %v", sum2.Errors)
	}
}

func containsSub(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
