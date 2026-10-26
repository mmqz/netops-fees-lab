package app

// e2e_period_test.go — issue #15's CLI end-to-end: the period-probe
// subcommand answers the content detection, a mismatching declaration
// refuses with both values, the --period-trust-declared override lands
// recorded, and the honest C档 path (no period evidence) proceeds
// marked manual.

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIFeesPeriodFlow(t *testing.T) {
	if e2eBin == "" {
		t.Skip("netopsfees binary was not built (go tool unavailable)")
	}
	tmp := t.TempDir()
	db := filepath.Join(tmp, "netops.db")

	// The 综维工资单 shape: 业务账期 says 2026-08.
	payroll := writeFeeCSV(t, tmp, "payroll-08.csv",
		"站址编码,员工工资上传批次,业务账期,1.8其他,金额\nHLJ-01,B-01,202608,5.00,10000\n")

	// period-probe: read-only, no declaration, nothing written.
	probe, code := runCLIJSON(t, "fees period-probe", "fees", "--db", db, "--data", tmp,
		"period-probe", "--file", payroll)
	if code != 0 {
		t.Fatalf("period-probe: exit %d", code)
	}
	if probe["kind"] != "payroll" || probe["confidence"] != "strong" {
		t.Fatalf("probe = %v", probe)
	}
	if probe["year"] != float64(2026) || probe["month"] != float64(8) {
		t.Fatalf("probe period = %v/%v, want 2026/8", probe["year"], probe["month"])
	}

	// import with a MISMATCHING declaration (2026-09): refused, both
	// values in the message, exit non-zero.
	out, code := runCLI(t, "fees", "--db", db, "--data", tmp,
		"import", "--file", payroll, "--type", "maintenance", "--year", "2026", "--month", "9")
	if code == 0 {
		t.Fatalf("mismatch import must exit non-zero, stdout: %s", out)
	}
	if !strings.Contains(out, "2026-08") || !strings.Contains(out, "2026-09") {
		t.Fatalf("mismatch refusal must carry both values: %s", out)
	}

	// import with the correct declaration: lands with periodSource=auto.
	sum, code := runCLIJSON(t, "fees import auto", "fees", "--db", db, "--data", tmp,
		"import", "--file", payroll, "--type", "maintenance", "--year", "2026", "--month", "8")
	if code != 0 {
		t.Fatalf("correct import: exit %d", code)
	}
	if sum["periodSource"] != "auto" {
		t.Fatalf("periodSource = %v, want auto", sum["periodSource"])
	}
	if basis, _ := sum["periodBasis"].(string); !strings.Contains(basis, "业务账期") {
		t.Fatalf("periodBasis = %v, want the evidence", sum["periodBasis"])
	}

	// A different file with a mismatching period: the override flag lands
	// it recorded (unverified + 人工裁决).
	payroll2 := writeFeeCSV(t, tmp, "payroll-10.csv",
		"站址编码,员工工资上传批次,业务账期,1.8其他,金额\nHLJ-02,B-02,202610,5.00,20000\n")
	sum, code = runCLIJSON(t, "fees import override", "fees", "--db", db, "--data", tmp,
		"import", "--file", payroll2, "--type", "maintenance", "--year", "2026", "--month", "11",
		"--period-trust-declared")
	if code != 0 {
		t.Fatalf("override import: exit %d", code)
	}
	if sum["periodSource"] != "unverified" {
		t.Fatalf("override periodSource = %v, want unverified", sum["periodSource"])
	}
	if basis, _ := sum["periodBasis"].(string); !strings.Contains(basis, "人工裁决") {
		t.Fatalf("override periodBasis = %v, want the recorded override", sum["periodBasis"])
	}

	// C档: a plain file with no period evidence lands marked manual.
	plain := writeFeeCSV(t, tmp, "plain.csv", "站址编码,电费(元)\nHLJ-03,10000\n")
	sum, code = runCLIJSON(t, "fees import manual", "fees", "--db", db, "--data", tmp,
		"import", "--file", plain, "--type", "electricity", "--year", "2026", "--month", "9")
	if code != 0 {
		t.Fatalf("C档 import: exit %d", code)
	}
	if sum["periodSource"] != "manual" {
		t.Fatalf("C档 periodSource = %v, want manual", sum["periodSource"])
	}
}
