#!/usr/bin/env python3
"""extract-doc-dockerfile.py — the #37 "文档即测试" bridge.

CI extracts the Dockerfile example OUT OF docs/fees-layer.md §5.2 (the
artifact's official consumption path) and actually builds + runs it against
the binary the duckdb job just produced. The doc is the single source of
truth: if the example regresses (a wrong base image like the #35
bookworm-slim, a missing GLIBC floor note), THIS extraction fails and CI
goes red — instead of the user's `docker run` failing with
`GLIBC_2.38 not found` after a green CI run.

Assertions (exit 1 on violation, message on stderr):
  1. exactly ONE ```dockerfile fenced block exists in the doc;
  2. the block carries a FROM line;
  3. the doc states the artifact's GLIBC floor (#35 acceptance 4 — the
     requirement note is as much a contract as the image tag itself).

Output: the Dockerfile text on stdout (CI redirects it into the build
context); the extracted base image goes to stderr for the log.
"""
import re
import sys

DOC_GLIBC_FLOOR = "GLIBC ≥ 2.38"


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: extract-doc-dockerfile.py <docs/fees-layer.md>", file=sys.stderr)
        return 1
    text = open(sys.argv[1], encoding="utf-8").read()

    blocks = re.findall(r"```dockerfile\n(.*?)```", text, flags=re.S)
    if len(blocks) != 1:
        print(
            f"extract-doc-dockerfile: expected exactly 1 ```dockerfile block in "
            f"{sys.argv[1]}, found {len(blocks)} — the CI step builds THE documented "
            f"example; two blocks would make 'the doc' ambiguous",
            file=sys.stderr,
        )
        return 1

    dockerfile = blocks[0]
    from_lines = [ln for ln in dockerfile.splitlines() if ln.strip().upper().startswith("FROM ")]
    if len(from_lines) != 1:
        print(
            f"extract-doc-dockerfile: the dockerfile block must carry exactly one FROM "
            f"line, found {len(from_lines)}",
            file=sys.stderr,
        )
        return 1

    if DOC_GLIBC_FLOOR not in text:
        print(
            "extract-doc-dockerfile: the doc no longer states the artifact's GLIBC "
            f"floor ({DOC_GLIBC_FLOOR}) — #35 acceptance 4 pins the requirement note "
            "to the doc; restore it (docs/fees-layer.md §5.2)",
            file=sys.stderr,
        )
        return 1

    print(f"extract-doc-dockerfile: base image = {from_lines[0].split()[1]}", file=sys.stderr)
    sys.stdout.write(dockerfile)
    return 0


if __name__ == "__main__":
    sys.exit(main())
