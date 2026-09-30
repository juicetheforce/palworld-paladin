#!/usr/bin/env bash
# Paladin — release artifact builder
# Usage: ./scripts/release.sh v0.1.0
#
# Produces: dist-release/paladin_<tag>_linux_x86_64.tar.gz (+ .sha256)
# containing the statically-built binary with the version stamped in —
# the exact asset name scripts/install.sh downloads. The committed web
# dist is embedded via go:embed, so no Node toolchain is needed here.
set -euo pipefail
TAG="${1:-}"
[ -n "$TAG" ] || { echo "usage: $0 vX.Y.Z" >&2; exit 2; }
case "$TAG" in v[0-9]*) ;; *) echo "tag must look like v0.1.0" >&2; exit 2 ;; esac
cd "$(dirname "$0")/.."

if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
  echo "WARNING: working tree is dirty — the release will not match a clean checkout." >&2
fi

# shellcheck source=lib/build.sh
. scripts/lib/build.sh
out=dist-release
asset=$(build_and_package "$TAG" "$out")

echo
echo "Done: $out/$asset"
echo "Suggested release notes (paste into the GitHub release body):"
echo "-----------------------------------------------------------"
cat <<NOTES
## Paladin $TAG

- <what changed and why>

**Install or update (same command):**
\`\`\`
curl -fsSL https://raw.githubusercontent.com/juicetheforce/palworld-paladin/main/scripts/install.sh | sudo bash
\`\`\`
NOTES
echo "-----------------------------------------------------------"
echo "Next steps:"
echo "  git tag $TAG && git push origin $TAG"
echo "  Create a GitHub release for $TAG and upload BOTH files from $out/,"
echo "  or with the gh CLI:  gh release create $TAG $out/$asset $out/$asset.sha256 --title \"Paladin $TAG\""
