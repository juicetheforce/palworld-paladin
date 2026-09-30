# Paladin — shared build + package step, sourced by release.sh and
# deploy-test.sh so test builds are packaged exactly like releases.
# (install.sh can't use this: it is curl-piped as a single file.)
#
# build_and_package <version> <outdir>
#   Builds the static linux/amd64 binary with <version> stamped in, then
#   writes <outdir>/paladin_<version>_linux_x86_64.tar.gz (containing just
#   `paladin`) and its .sha256. Prints the asset file name on stdout; all
#   progress goes to stderr. Run from the repo root.
build_and_package() {
  local version="$1" out="$2" asset
  mkdir -p "$out"
  echo "Building paladin $version (static, linux/amd64)…" >&2
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$out/paladin" ./cmd/paladin

  "$out/paladin" version | grep -Fq "$version" || { echo "version stamp failed" >&2; return 1; }

  asset="paladin_${version}_linux_x86_64.tar.gz"
  tar -czf "$out/$asset" -C "$out" paladin
  ( cd "$out" && sha256sum "$asset" > "$asset.sha256" && cat "$asset.sha256" >&2 )
  echo "$asset"
}
