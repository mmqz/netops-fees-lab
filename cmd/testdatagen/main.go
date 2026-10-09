// Command testdatagen writes the deterministic synthetic test workbooks and
// the expected import summary into an output directory (default
// "testdata"). The generator logic lives in internal/testfixtures
// (WriteAll) and is shared with `netopslab testdata`, so both commands
// produce byte-identical artifacts.
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
	"flag"
	"fmt"
	"os"

	"github.com/mmqz/netops-lab/internal/testfixtures"
)

func main() {
	out := flag.String("out", "testdata", "output directory (created if missing)")
	flag.Parse()
	if err := testfixtures.WriteAll(*out); err != nil {
		fmt.Fprintln(os.Stderr, "testdatagen:", err)
		os.Exit(1)
	}
	fmt.Printf("testdatagen: wrote %s to %s\n", "devices.xlsx, ippools.xlsx, circuits.xlsx, expected-summary.json", *out)
}
