// Command testdatagen writes the deterministic synthetic test workbooks and
// the expected import summary into an output directory (default
// "testdata").
//
// It produces exactly four files:
//
//	devices.xlsx     sheet 设备清单  (internal/testfixtures.DeviceRows)
//	ippools.xlsx     sheet 地址池    (internal/testfixtures.IPPoolRows)
//	circuits.xlsx    sheet 电路      (internal/testfixtures.CircuitRows)
//	expected-summary.json
//
// All row data comes from the fixed literals in internal/testfixtures —
// this command adds no data of its own, so the generated business data is
// byte-stable apart from xlsx container metadata. Header is Excel row 1,
// data starts at Excel row 2; row_number = slice index + 2 everywhere.
//
// The expected summary encodes the import outcome when the three workbooks
// are imported in order devices → ippools → circuits into a FRESH database
// (pool/circuit references resolve against previously imported devices).
// Semantics follow PROJECT_BRIEF §7: error-severity rules (E…) fail the
// row; warning (W…) and info (I…) rules leave the row imported. Set-level
// rules (E005/E006/I001) are counted once per finding and attributed to the
// workbook whose data creates or reveals them (E006 + I001 → devices,
// E005 → ippools).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/xuri/excelize/v2"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/testfixtures"
)

func main() {
	out := flag.String("out", "testdata", "output directory (created if missing)")
	flag.Parse()
	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "testdatagen:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	// Sanity-check the fixtures before writing anything, so a drifted
	// literal fails loudly here instead of silently downstream.
	if len(testfixtures.DeviceRows) != testfixtures.DeviceTotal ||
		len(testfixtures.IPPoolRows) != testfixtures.IPPoolTotal ||
		len(testfixtures.CircuitRows) != testfixtures.CircuitTotal {
		return fmt.Errorf("fixture count mismatch: devices=%d (want %d), ippools=%d (want %d), circuits=%d (want %d)",
			len(testfixtures.DeviceRows), testfixtures.DeviceTotal,
			len(testfixtures.IPPoolRows), testfixtures.IPPoolTotal,
			len(testfixtures.CircuitRows), testfixtures.CircuitTotal)
	}

	if err := writeWorkbook(filepath.Join(out, "devices.xlsx"), testfixtures.DeviceSheetName,
		testfixtures.DeviceHeaders, cellsOf(testfixtures.DeviceRows)); err != nil {
		return err
	}
	if err := writeWorkbook(filepath.Join(out, "ippools.xlsx"), testfixtures.IPPoolSheetName,
		testfixtures.IPPoolHeaders, cellsOf(testfixtures.IPPoolRows)); err != nil {
		return err
	}
	if err := writeWorkbook(filepath.Join(out, "circuits.xlsx"), testfixtures.CircuitSheetName,
		testfixtures.CircuitHeaders, cellsOf(testfixtures.CircuitRows)); err != nil {
		return err
	}
	if err := writeSummary(filepath.Join(out, "expected-summary.json")); err != nil {
		return err
	}
	fmt.Printf("testdatagen: wrote devices.xlsx, ippools.xlsx, circuits.xlsx, expected-summary.json to %s\n", out)
	return nil
}

// cellsOf flattens the typed fixture rows into raw string rows.
func cellsOf[T interface{ Cells() []string }](rows []T) [][]string {
	out := make([][]string, len(rows))
	for i, r := range rows {
		out[i] = r.Cells()
	}
	return out
}

// writeWorkbook writes one xlsx file containing a single sheet with the
// given header row and data rows. All cells are written as strings — the
// raw values exactly as the fixtures define them.
func writeWorkbook(path, sheet string, headers []string, rows [][]string) error {
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	if err := f.SetSheetName("Sheet1", sheet); err != nil {
		return fmt.Errorf("rename default sheet in %s: %w", path, err)
	}
	for c, h := range headers {
		cell, err := excelize.CoordinatesToCellName(c+1, 1)
		if err != nil {
			return err
		}
		if err := f.SetCellStr(sheet, cell, h); err != nil {
			return fmt.Errorf("write header %s in %s: %w", cell, path, err)
		}
	}
	for r, row := range rows {
		for c, v := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, r+2) // data starts at Excel row 2
			if err != nil {
				return err
			}
			if err := f.SetCellStr(sheet, cell, v); err != nil {
				return fmt.Errorf("write cell %s in %s: %w", cell, path, err)
			}
		}
	}
	// Fixed document properties keep the output as stable as the xlsx
	// container allows (no now()-based timestamps inside the metadata).
	props := excelize.DocProperties{
		Creator:        "netops-lab testdatagen",
		LastModifiedBy: "netops-lab testdatagen",
		Created:        "2025-01-01T00:00:00Z",
		Modified:       "2025-01-01T00:00:00Z",
	}
	if err := f.SetDocProps(&props); err != nil {
		return fmt.Errorf("set doc props of %s: %w", path, err)
	}
	if err := f.SaveAs(path); err != nil {
		return fmt.Errorf("save %s: %w", path, err)
	}
	return nil
}

// workbookSummary is the expected outcome of importing one workbook.
type workbookSummary struct {
	File         string `json:"file"`
	Sheet        string `json:"sheet"`
	Rows         int    `json:"rows"`
	RowsImported int    `json:"rowsImported"`
	RowsSkipped  int    `json:"rowsSkipped"`
	RowsFailed   int    `json:"rowsFailed"`
	// Expected maps rule_code → number of issues attributable to this
	// workbook. Every catalog rule is listed, including the zeros.
	Expected map[string]int `json:"expected"`
}

// summary is the shape of testdata/expected-summary.json.
type summary struct {
	Devices  workbookSummary `json:"devices"`
	IPPools  workbookSummary `json:"ippools"`
	Circuits workbookSummary `json:"circuits"`
}

func writeSummary(path string) error {
	s := summary{
		// Devices: 30 rows. Failures (E ⇒ row failed): the second DEV-012
		// copy (E001), the row with the empty 设备名称 (E002) and the row
		// with 管理IP 192.168.1.300 (E004). W002 (status 未知) is a warning
		// ⇒ row imported. E006 (10.10.10.5 pair) and I001 (DEV-030 without
		// any relation) are set-level findings on data from this workbook.
		Devices: workbookSummary{
			File:         "devices.xlsx",
			Sheet:        testfixtures.DeviceSheetName,
			Rows:         testfixtures.DeviceTotal,
			RowsImported: 27,
			RowsSkipped:  0,
			RowsFailed:   3,
			Expected: map[string]int{
				domain.RuleDuplicateKey:         1, // E001
				domain.RuleMissingRequiredField: 1, // E002
				domain.RuleInvalidReference:     0, // E003
				domain.RuleInvalidFormat:        1, // E004
				domain.RuleSubnetOverlap:        0, // E005
				domain.RuleDuplicateAddress:     1, // E006 (set)
				domain.RuleOrphanRecord:         0, // W001
				domain.RuleUnknownStatusValue:   1, // W002 (imported)
				domain.RuleCircuitSelfLoop:      0, // W003
				domain.RuleUnlinkedDevice:       1, // I001 (set)
			},
		},
		// IP pools: 20 rows. Failures: empty CIDR (E002), CIDR 10.20.0/24
		// (E004), VLAN abc (E004), 归属设备 DEV-9999 (E003). The empty
		// 归属设备 row is the W001 orphan and stays imported; the
		// overlapping 10.30.1.0/24 + 10.30.1.128/25 pair is the set-level
		// E005 finding, both rows imported.
		IPPools: workbookSummary{
			File:         "ippools.xlsx",
			Sheet:        testfixtures.IPPoolSheetName,
			Rows:         testfixtures.IPPoolTotal,
			RowsImported: 16,
			RowsSkipped:  0,
			RowsFailed:   4,
			Expected: map[string]int{
				domain.RuleDuplicateKey:         0, // E001
				domain.RuleMissingRequiredField: 1, // E002
				domain.RuleInvalidReference:     1, // E003
				domain.RuleInvalidFormat:        2, // E004 (bad CIDR + bad VLAN)
				domain.RuleSubnetOverlap:        1, // E005 (set)
				domain.RuleDuplicateAddress:     0, // E006
				domain.RuleOrphanRecord:         1, // W001 (imported)
				domain.RuleUnknownStatusValue:   0, // W002
				domain.RuleCircuitSelfLoop:      0, // W003
				domain.RuleUnlinkedDevice:       0, // I001
			},
		},
		// Circuits: 25 rows. Failures: the second CKT-018 copy (E001), the
		// row with the empty Z端设备 (E002), the row with A端设备 DEV-8888
		// (E003) and the row with 带宽Mbps N/A (E004). The A端==Z端==DEV-005
		// row is the W003 self-loop and stays imported.
		Circuits: workbookSummary{
			File:         "circuits.xlsx",
			Sheet:        testfixtures.CircuitSheetName,
			Rows:         testfixtures.CircuitTotal,
			RowsImported: 21,
			RowsSkipped:  0,
			RowsFailed:   4,
			Expected: map[string]int{
				domain.RuleDuplicateKey:         1, // E001
				domain.RuleMissingRequiredField: 1, // E002
				domain.RuleInvalidReference:     1, // E003
				domain.RuleInvalidFormat:        1, // E004
				domain.RuleSubnetOverlap:        0, // E005
				domain.RuleDuplicateAddress:     0, // E006
				domain.RuleOrphanRecord:         0, // W001
				domain.RuleUnknownStatusValue:   0, // W002
				domain.RuleCircuitSelfLoop:      1, // W003 (imported)
				domain.RuleUnlinkedDevice:       0, // I001
			},
		},
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal expected summary: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
