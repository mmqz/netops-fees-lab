package importer

// AnalyzeOffline is the CLI `analyze` command's whole job (PROJECT_BRIEF
// §10 "parse+suggest only, no DB write") and was the importer's only
// zero-covered production path: a silent regression in it (say, a
// mapping-suggestion change that panicked on an empty sheet) would only
// surface as a CLI crash in the field. The pins: a real committed
// workbook analyzes with suggestions AND no batch side effect, a
// missing file wraps the OS error, and garbage bytes wrap the workbook
// package's parse sentinel without creating anything.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalyzeOfflineRealWorkbook(t *testing.T) {
	a, err := AnalyzeOffline(testdataPath("devices.xlsx"), "devices.xlsx")
	if err != nil {
		t.Fatalf("AnalyzeOffline: %v", err)
	}
	if a.BatchID != "" {
		t.Fatalf("BatchID = %q, want empty — the offline path must not create any batch row", a.BatchID)
	}
	if len(a.Sheets) == 0 {
		t.Fatalf("sheets = 0, want the devices workbook's sheet analyzed")
	}
	if len(a.SuggestedMapping.Sheets) == 0 {
		t.Fatalf("suggested mapping empty, want the recognizer's suggestion for a real committed workbook")
	}
}

func TestAnalyzeOfflineMissingFile(t *testing.T) {
	_, err := AnalyzeOffline(filepath.Join(t.TempDir(), "nope.xlsx"), "nope.xlsx")
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want a wrapped os.ErrNotExist", err)
	}
}

func TestAnalyzeOfflineUnparseableBytes(t *testing.T) {
	garbage := filepath.Join(t.TempDir(), "garbage.xlsx")
	if err := os.WriteFile(garbage, []byte("not a zip, not a workbook"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := AnalyzeOffline(garbage, "garbage.xlsx")
	if err == nil {
		t.Fatalf("err = nil, want the workbook package's parse error surfaced")
	}
	if !strings.Contains(err.Error(), "analyze garbage.xlsx") {
		t.Fatalf("err = %v, want the workbook name in the wrapping context", err)
	}
}
