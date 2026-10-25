package app

// e2e_convert_test.go — the real-binary pins of issue #7:
//   - convert answers the single-JSON CLI contract (exit 0);
//   - a batch with a corrupt .xls prints the SAME single JSON (per-file
//     reasons inside) and exits 1 — never silent, never a second envelope;
//   - the converted CSV is consumed by `fees import` end to end (the
//     acceptance pin: conversion is the missing shell BEFORE the pipeline,
//     not a parallel one).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConvertCLIContract drives the real binary on the committed fixture.
func TestConvertCLIContract(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopsfees binary was not built (go tool unavailable)")
	}
	tmp := t.TempDir()
	fixture := repoFixturePath(t, "ledger-2003-format.xls")

	out, code := runCLI(t, "convert", "--in", fixture, "--out", filepath.Join(tmp, "out"))
	if code != 0 {
		t.Fatalf("convert exit = %d, want 0:\n%s", code, out)
	}
	var res struct {
		Converted int `json:"converted"`
		Failed    int `json:"failed"`
		Files     []struct {
			Status string   `json:"status"`
			Output []string `json:"outputs"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("stdout is not the single JSON contract: %v\n%s", err, out)
	}
	if res.Converted != 1 || res.Failed != 0 {
		t.Fatalf("result = converted %d failed %d, want 1/0:\n%s", res.Converted, res.Failed, out)
	}
	xlsx := filepath.Join(tmp, "out", "ledger-2003-format.xlsx")
	if _, err := os.Stat(xlsx); err != nil {
		t.Fatalf("converted xlsx missing: %v", err)
	}
	if len(res.Files) == 0 || len(res.Files[0].Output) == 0 || res.Files[0].Output[0] != xlsx {
		t.Fatalf("output paths not reported / not original-named:\n%s", out)
	}
}

// TestConvertCLICorruptBatchExitsOne: one good + one corrupt .xls in a
// directory — the JSON reports both per-file, exit reflects the failure.
func TestConvertCLICorruptBatchExitsOne(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopsfees binary was not built (go tool unavailable)")
	}
	tmp := t.TempDir()
	in := filepath.Join(tmp, "in")
	if err := os.MkdirAll(in, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture := repoFixturePath(t, "ledger-2003-format.xls")
	if err := os.WriteFile(filepath.Join(in, "good.xls"), mustRead(t, fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in, "bad.xls"), []byte("not an OLE file"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := runCLI(t, "convert", "--in", in, "--out", filepath.Join(tmp, "out"))
	if code == 0 {
		t.Fatalf("batch with a corrupt file must exit non-zero:\n%s", out)
	}
	if strings.Count(strings.TrimSpace(out), "{") < 1 || strings.Contains(out, "usage_error") {
		t.Fatalf("stdout must stay the single result JSON (no second envelope):\n%s", out)
	}
	var res struct {
		Converted int `json:"converted"`
		Failed    int `json:"failed"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("single-JSON contract broken: %v\n%s", err, out)
	}
	if res.Converted != 1 || res.Failed != 1 {
		t.Fatalf("converted/failed = %d/%d, want 1/1:\n%s", res.Converted, res.Failed, out)
	}
}

// TestConvertOutputFeedsFeesImport is the consumability pin: the CSV the
// converter produced for the 转供电 sheet imports through the REAL fees
// pipeline with zero failures.
func TestConvertOutputFeedsFeesImport(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopsfees binary was not built (go tool unavailable)")
	}
	tmp := t.TempDir()
	fixture := repoFixturePath(t, "ledger-2003-format.xls")
	if _, code := runCLI(t, "convert", "--in", fixture, "--out", tmp, "--format", "csv"); code != 0 {
		t.Fatalf("convert csv: exit %d", code)
	}
	csv := filepath.Join(tmp, "ledger-2003-format", "转供电.csv")
	if _, err := os.Stat(csv); err != nil {
		t.Fatalf("converted sheet csv missing: %v", err)
	}
	db := filepath.Join(tmp, "netops.db")
	out, code := runCLI(t, "fees", "--db", db, "--data", tmp,
		"import", "--file", csv, "--type", "electricity", "--year", "2026", "--month", "1")
	if code != 0 {
		t.Fatalf("fees import of converted csv exit = %d:\n%s", code, out)
	}
	var imp struct {
		Status       string `json:"status"`
		RowsTotal    int    `json:"rowsTotal"`
		RowsImported int    `json:"rowsImported"`
		RowsFailed   int    `json:"rowsFailed"`
	}
	if err := json.Unmarshal([]byte(out), &imp); err != nil {
		t.Fatalf("fees import stdout not JSON: %v\n%s", err, out)
	}
	if imp.Status != "completed" || imp.RowsTotal != 2 || imp.RowsImported != 2 || imp.RowsFailed != 0 {
		t.Fatalf("import = %+v, want completed 2/2/0:\n%s", imp, out)
	}
}

// repoFixturePath resolves a testdata path relative to the app package.
func repoFixturePath(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root, err := filepath.Abs(filepath.Join(dir, "..", ".."))
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	p := filepath.Join(root, "testdata", name)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("fixture missing: %v", err)
	}
	return p
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return b
}
