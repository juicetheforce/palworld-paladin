#!/usr/bin/env bash
# Paladin — deploy the current working tree to a disposable test VM through
# the REAL installer, the way users run it (fresh install or update).
#
# Usage (on the laptop, from anywhere in the repo):
#   scripts/deploy-test.sh <host> [--fresh|--update]
#
#   <host>    an ssh destination listed in scripts/test-hosts (gitignored;
#             copy scripts/test-hosts.example). Anything else is refused:
#             that list is what keeps this away from real servers.
#   --fresh   refuse unless the VM has no Paladin yet (restored clean snapshot)
#   --update  refuse unless Paladin is already installed on the VM
#             (with neither, the installer decides, as it always does)
#
# Builds exactly like release.sh (shared scripts/lib/build.sh), stamped
# dev-<commit>[-dirty], then runs install.sh --local-archive on the VM.
# See docs/testing.md.
set -euo pipefail
trap 'echo "[deploy-test] FATAL: command failed at line $LINENO" >&2' ERR

die() { echo "[deploy-test] ERROR: $*" >&2; exit 1; }
say() { echo "[deploy-test] $*"; }

HOST="${1:-}"
MODE=""
[ -n "$HOST" ] && [ "${HOST#-}" = "$HOST" ] || die "usage: scripts/deploy-test.sh <host> [--fresh|--update]"
shift
while [ $# -gt 0 ]; do
  case "$1" in
    --fresh|--update) [ -z "$MODE" ] || die "pick one of --fresh or --update"; MODE="$1" ;;
    *) die "unknown flag: $1 (usage: scripts/deploy-test.sh <host> [--fresh|--update])" ;;
  esac; shift
done

cd "$(dirname "$0")/.."

# ---- guard: only hosts listed in scripts/test-hosts ----
HOSTS_FILE=scripts/test-hosts
[ -f "$HOSTS_FILE" ] || die "refusing: $HOSTS_FILE doesn't exist. Copy scripts/test-hosts.example and list your disposable test VMs in it."
if ! grep -v '^[[:space:]]*#' "$HOSTS_FILE" | tr -d '[:blank:]\r' | grep -Fxq -- "$HOST"; then
  die "refusing: '$HOST' is not in $HOSTS_FILE (the list of disposable test VMs). Add it there if it really is one."
fi

# ---- build + package, exactly like a release ----
STAMP="dev-$(git rev-parse --short HEAD)"
[ -z "$(git status --porcelain)" ] || STAMP="$STAMP-dirty"
OUT=$(mktemp -d)
trap 'rm -rf "$OUT"' EXIT
# shellcheck source=lib/build.sh
. scripts/lib/build.sh
ASSET=$(build_and_package "$STAMP" "$OUT")

# ---- optional fresh/update assertion ----
BIN=/usr/local/bin/paladin
if [ -n "$MODE" ]; then
  if ssh "$HOST" test -x "$BIN"; then present=1; else present=0; fi
  if [ "$MODE" = --fresh ] && [ "$present" = 1 ]; then
    die "--fresh: Paladin is already installed on $HOST ($BIN). Restore the clean snapshot first."
  fi
  if [ "$MODE" = --update ] && [ "$present" = 0 ]; then
    die "--update: Paladin isn't installed on $HOST yet. Run a --fresh deploy first (or restore the installed snapshot)."
  fi
fi

# ---- ship and run the real installer ----
say "Copying $ASSET, its .sha256 and install.sh to $HOST:/tmp …"
scp -q "$OUT/$ASSET" "$OUT/$ASSET.sha256" scripts/install.sh "$HOST:/tmp/"
say "Running the installer on $HOST (sudo will ask for your password there)…"
ssh -t "$HOST" sudo bash /tmp/install.sh --local-archive "/tmp/$ASSET"

# ---- summary ----
ADDR=$(ssh -G "$HOST" | awk '$1 == "hostname" {print $2; exit}')
echo
say "Host:   $HOST"
say "Build:  $STAMP"
say "Web UI: http://${ADDR:-$HOST}:8080   (default port; the installer's summary above is authoritative)"
