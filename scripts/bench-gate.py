#!/usr/bin/env python3
"""bench-gate.py — the #33 benchmark regression gate (告警不阻断).

Compares two `go test -bench` outputs (ns/op per benchmark) and warns on
regressions beyond the threshold. Exit code stays 0 on regressions (the
issue's stage-3 ask: warn, never block) unless --strict is given.

Usage:
    python3 scripts/bench-gate.py --new new.txt --old testdata/bench/baseline.txt [--threshold 0.20] [--strict]

Honesty notes:
  - ns/op across DIFFERENT machines is noise, not signal — compare runs
    from the same host family (the nightly workflow compares consecutive
    nightly runs, both on ubuntu-latest; locally compare against a
    baseline regenerated on your machine).
  - a benchmark missing from either side is reported as UNPAIRED (never
    silently skipped); -2/-8 cpu-suffixes are normalized away.
"""
import argparse
import re
import sys

LINE = re.compile(r"^(Benchmark[A-Za-z0-9_]+?)(?:-\d+)?\s+\d+\s+([0-9.]+) ns/op")


def parse(path):
    out = {}
    unpaired = []
    for ln in open(path, encoding="utf-8", errors="replace"):
        m = LINE.match(ln)
        if m:
            name = m.group(1)
            # keep the FIRST occurrence: go prints one result line per benchmark
            out.setdefault(name, float(m.group(2)))
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--new", required=True, help="the fresh `go test -bench` output")
    ap.add_argument("--old", required=True, help="the baseline `go test -bench` output")
    ap.add_argument("--threshold", type=float, default=0.20, help="regression bar (0.20 = 20%%)")
    ap.add_argument("--strict", action="store_true", help="exit 1 on regressions instead of warning")
    args = ap.parse_args()

    new = parse(args.new)
    old = parse(args.old)
    if not new:
        print("bench-gate: no benchmark lines found in --new", file=sys.stderr)
        return 2
    if not old:
        print("bench-gate: no benchmark lines found in --old", file=sys.stderr)
        return 2

    regressions = 0
    for name in sorted(new):
        base = old.get(name)
        if base is None:
            print(f"UNPAIRED  {name}: present in new, absent from old (baseline refresh due?)")
            continue
        delta = (new[name] - base) / base
        if delta > args.threshold:
            regressions += 1
            print(f"REGRESSED {name}: {base:.0f} -> {new[name]:.0f} ns/op (+{delta:.0%} over the {args.threshold:.0%} bar)")
        elif delta < -args.threshold:
            print(f"improved  {name}: {base:.0f} -> {new[name]:.0f} ns/op ({delta:.0%} — enjoy, refresh the baseline)")
    for name in sorted(old):
        if name not in new:
            print(f"UNPAIRED  {name}: present in old, absent from new (benchmark deleted?)")

    if regressions:
        print(f"bench-gate: {regressions} regression(s) over the {args.threshold:.0%} bar", file=sys.stderr)
        return 1 if args.strict else 0
    print(f"bench-gate: {len(new)} benchmarks compared, none over the {args.threshold:.0%} bar")
    return 0


if __name__ == "__main__":
    sys.exit(main())
