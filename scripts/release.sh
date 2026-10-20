#!/usr/bin/env bash
# release.sh — parameterized GitHub Release publisher (#74: the one-shot
# w29_release.sh with TAG hardcoded to v0.8.0 was a root cause of the
# second release-channel gap — nine versions shipped tags without
# Releases because publishing was not repeatable).
#
# Usage:
#   scripts/release.sh TAG NAME BODY_FILE [--with-duckdb] [--binary TAG_REF]
#
#     TAG           the release tag (v0.15.2); created if missing, pointing
#                   at HEAD (or TAG_REF when given)
#     NAME          the release title
#     BODY_FILE     the release body (markdown file)
#     --with-duckdb also build & upload the linux-amd64 duckdb binary
#                   (CGO + -tags duckdb; the fee-analysis engine build —
#                   linux/WSL2/macOS only)
#     --binary TAG_REF
#                   build the linux binary from TAG_REF (an existing tag /
#                   commit) instead of HEAD — used to backfill assets onto
#                   an existing release (e.g. the empty v0.9.0 shell)
#
# Assets per release:
#   netops-lab-<TAG>-source.tar.gz        git archive of the tag
#   netops-lab-<TAG>-linux-amd64.tar.gz   bin/netopslab (default build)
#   netops-lab-<TAG>-linux-amd64-duckdb.tar.gz  (with --with-duckdb)
#
# PAT: read at runtime from ~/.git-credentials (600, never committed).
# The frontend MUST be built first (make web) — the Go build embeds
# whatever is in internal/webui/dist.
set -euo pipefail

REPO="mmqz/netops-lab"
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

TAG="${1:?usage: release.sh TAG NAME BODY_FILE [--with-duckdb] [--binary TAG_REF]}"
NAME="${2:?release name}"
BODY_FILE="${3:?release body file}"
WITH_DUCKDB=no
BIN_REF=""
shift 3 || true
while [ $# -gt 0 ]; do
  case "$1" in
    --with-duckdb) WITH_DUCKDB=yes ;;
    --binary) BIN_REF="${2:?--binary needs a tag/commit}"; shift ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
  shift
done

REL="$(mktemp -d /tmp/netops-rel.XXXXXX)"
trap 'rm -rf "$REL"' EXIT

TOKEN=$(python3 -c "print(open('/home/z/.git-credentials').read().strip().split(':')[2].split('@')[0])")
AUTH=(-H "Authorization: token $TOKEN" -H "Accept: application/vnd.github+json")

# 1. the tag must exist (create at HEAD / BIN_REF when missing).
if ! git rev-parse -q --verify "refs/tags/$TAG" >/dev/null; then
  REF="${BIN_REF:-HEAD}"
  git tag "$TAG" "$REF"
  git push origin "$TAG"
  echo "tagged $TAG at $REF and pushed"
else
  echo "tag $TAG already exists"
fi

# 2. the linux binary: from BIN_REF when backfilling, else from HEAD.
BUILD_REF="${BIN_REF:-HEAD}"
echo "building default binary from $BUILD_REF …"
git archive --format=tar "--prefix=netops-lab-build/" "$BUILD_REF" | (mkdir -p "$REL/build" && tar -x -C "$REL/build")
# The build needs the webui dist of THAT tree — dist is gitignored, so build
# it from the extracted tree with the current toolchain (dist content is a
# deterministic function of web/ + node_modules, not of the host dist).
(
  cd "$REL/build/netops-lab-build"
  cp -r "$REPO_ROOT/web/node_modules" web/node_modules 2>/dev/null || (cd web && npm ci --no-audit --no-fund --silent)
  (cd web && npx tsc -b && npm run build >/dev/null 2>&1)
  go build -o "$REL/netopslab" ./cmd/netopslab
  if [ "$WITH_DUCKDB" = yes ]; then
    CGO_ENABLED=1 go build -tags duckdb -o "$REL/netopslab-duckdb" ./cmd/netopslab
  fi
)

# 3. source tarball from the tagged tree.
git archive --format=tar.gz --prefix="netops-lab-$TAG/" -o "$REL/netops-lab-$TAG-source.tar.gz" "$TAG"

# 4. binary tarballs.
BIN="$REL/netops-lab-$TAG-linux-amd64.tar.gz"
tar -czf "$BIN" -C "$REL" --transform "s|^netopslab|netopslab|" netopslab
ASSETS=("netops-lab-$TAG-source.tar.gz" "netops-lab-$TAG-linux-amd64.tar.gz")
if [ "$WITH_DUCKDB" = yes ]; then
  DDB="$REL/netops-lab-$TAG-linux-amd64-duckdb.tar.gz"
  tar -czf "$DDB" -C "$REL" --transform "s|^netopslab-duckdb|netopslab-duckdb|" netopslab-duckdb
  ASSETS+=("netops-lab-$TAG-linux-amd64-duckdb.tar.gz")
fi

# 5. find-or-create the release, then upload every asset. An existing
# release (e.g. the empty v0.9.0 shell) is REUSED — its id receives the
# missing assets.
python3 - "$TAG" "$NAME" "$BODY_FILE" > "$REL/body.json" << 'PY'
import json, sys
tag, name, body_file = sys.argv[1], sys.argv[2], sys.argv[3]
body = open(body_file, encoding='utf-8').read()
print(json.dumps({"tag_name": tag, "name": name, "body": body, "draft": False, "prerelease": False}))
PY

ID=$(curl -s "${AUTH[@]}" "https://api.github.com/repos/$REPO/releases/tags/$TAG" \
  | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('id','') if d.get('id') else '')")
if [ -z "$ID" ]; then
  ID=$(curl -s "${AUTH[@]}" -H "Content-Type: application/json" --data @"$REL/body.json" \
    "https://api.github.com/repos/$REPO/releases" \
    | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('id','')) if d.get('id') else sys.exit(f\"release create failed: {d.get('message')} {d.get('errors')}\")")
fi
if [ -z "$ID" ]; then
  echo "could not resolve a release id for $TAG" >&2
  exit 1
fi
echo "release id=$ID"

for f in "${ASSETS[@]}"; do
  curl -s "${AUTH[@]}" -H "Content-Type: application/octet-stream" \
    --data-binary "@$REL/$f" \
    "https://uploads.github.com/repos/$REPO/releases/$ID/assets?name=$f" \
    | python3 -c "import json,sys; d=json.load(sys.stdin); print('uploaded:', d.get('name'), d.get('state'), d.get('size'), d.get('message',''))"
done
echo "done: https://github.com/$REPO/releases/tag/$TAG"
