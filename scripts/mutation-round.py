#!/usr/bin/env python3
"""mutation-round.py — issue #33's targeted mutation round: inject the four
named defects (the issue §7.3's own list), run the scoped tests that own
each behavior, and assert the suite CATCHES every mutant. A surviving
mutant means the tests only run, they do not verify.

Mutants (all named by the issue):
  M1 risks.go      threshold mutation — the outlier bar moves so far out
                   the scenario can never fire (the >= → > family).
  M2 crosscheck.go classification boundary — a single-row elec site stops
                   counting as present (rows > 0 becomes rows > 1).
  M3 recognize.go synonym deletion — the tower scope loses its first-tier
                   caliber entry.
  M4 repo.go       commit → rollback (the transaction mutation).

Usage: python3 scripts/mutation-round.py [--quick]
"""
import os
import subprocess
import sys

ROOT = "/home/z/my-project/netops-fees-lab"
PATH = os.environ.get("PATH", "")
if "/home/z/.local/go/bin" not in PATH:
    PATH = "/home/z/.local/go/bin:" + PATH
ENV = {**os.environ, "PATH": PATH}

MUTANTS = [
    {
        "id": "M1",
        "file": "internal/fees/risks.go",
        "old": "\tDefaultOutlierZ = 3.0",
        "new": "\tDefaultOutlierZ = 3000.0",
        "tests": [
            ["go", "test", "-count=1", "-tags", "duckdb", "-run",
             "TestRiskPresetsDuck|TestExcludedDominanceWarn", "./internal/fees/"],
        ],
    },
    {
        "id": "M2",
        "file": "internal/fees/crosscheck.go",
        "old": "\t\tcase a.elecRows > 0:",
        "new": "\t\tcase a.elecRows > 1:",
        "tests": [
            ["go", "test", "-count=1", "-run", "TestCross", "./internal/fees/"],
        ],
    },
    {
        "id": "M3",
        "file": "internal/fees/recognize.go",
        "old": '                "产品服务费合计(出账费用)(不含税)",\n',
        "new": "",
        "tests": [
            ["go", "test", "-count=1", "-run",
             "TestRecognizeAmountForType|TestHeaderMatchKeyFolds|TestTower", "./internal/fees/"],
        ],
    },
    {
        "id": "M4",
        "file": "internal/repo/repo.go",
        "old": "\tif err := sqlTx.Commit(); err != nil {",
        "new": "\tif err := sqlTx.Rollback(); err != nil {",
        "tests": [
            ["go", "test", "-count=1", "-run",
             "TestImportPipelineAndIdempotency|TestVerifyBatches", "./internal/fees/"],
        ],
    },
]

survived, caught = [], []
for m in MUTANTS:
    full = os.path.join(ROOT, m["file"])
    src = open(full).read()
    if m["old"] not in src:
        print(f"{m['id']}: MUTATION POINT GONE — the source drifted, update the harness")
        sys.exit(2)
    open(full, "w").write(src.replace(m["old"], m["new"], 1))
    try:
        mutant_caught = False
        for cmd in m["tests"]:
            p = subprocess.run(cmd, cwd=ROOT, env=ENV, capture_output=True, text=True,
                               timeout=600)
            if p.returncode != 0:
                mutant_caught = True
                detail = (p.stdout + p.stderr).strip().splitlines()
                first_fail = next((l for l in detail if "--- FAIL" in l), "(no FAIL line)")
                print(f"{m['id']}: CAUGHT by tests — {first_fail.strip()}")
                break
        if not mutant_caught:
            survived.append(m["id"])
            print(f"{m['id']}: SURVIVED — the scoped tests did NOT fail (tests verify nothing!)")
    finally:
        open(full, "w").write(src)
        # sanity: the restored file must still build-test green
        p = subprocess.run(["go", "test", "-count=1", "-run", m["id"] + "_restore_sanity", "./internal/fees/"],
                           cwd=ROOT, env=ENV, capture_output=True, text=True, timeout=300)
        if p.returncode != 0:
            print(f"{m['id']}: RESTORE BROKE THE TREE — {p.stdout[:400]}")
            sys.exit(2)

total = len(MUTANTS)
rate = len(survived) / total * 100
print(f"\nmutation round: {total} mutants, {len(survived)} survived — 存活率 {rate:.0f}%")
print(f"issue bar: core packages < 20%")
if survived:
    print("SURVIVED: " + ", ".join(survived))
    sys.exit(1)
print("every named mutant was caught by its owning tests — the suite verifies, not just runs")
