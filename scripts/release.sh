#!/usr/bin/env bash
# Paladin — release artifact builder
# Usage: ./scripts/release.sh v0.1.0
#
# Produces: dist-release/paladin_<tag>_linux_x86_64.tar.gz (+ .sha256)
# containing the statically-built binary with the version stamped in —
# the exact asset name scripts/install.sh downloads. The committed web
# dist is embedded via go:embed, so no Node toolchain is needed here.
#
# Runs on the laptop (or anywhere with Go): the binary is static
# linux/amd64 and -trimpath, so it's the same wherever it's built. The
# /release command calls this, then tags and publishes the GitHub release.
set -euo pipefail
TAG="${1:-}"
[ -n "$TAG" ] || { echo "usage: $0 vX.Y.Z" >&2; exit 2; }
case "$TAG" in v[0-9]*) ;; *) echo "tag must look like v0.1.0" >&2; exit 2 ;; esac
cd "$(dirname "$0")/.."

# A release must be exactly a commit. Building from a dirty tree is how
# v0.3.0 shipped a frontend whose source was never committed.
if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
  echo "refusing: the working tree is dirty, so the release wouldn't match any commit." >&2
  echo "Commit or stash first (git status shows what)." >&2
  exit 1
fi

# shellcheck source=lib/build.sh
. scripts/lib/build.sh
out=dist-release
asset=$(build_and_package "$TAG" "$out")

echo
echo "Done: $out/$asset"
echo "Next: /release tags the commit, publishes the GitHub release with both"
echo "files from $out/, and checks the published assets from the outside."
