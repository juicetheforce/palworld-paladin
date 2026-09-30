#!/usr/bin/env bash
# Paladin — laptop check for install.sh --local-archive (no root, no network).
#
# Builds a real release-format archive with scripts/lib/build.sh, then
# sources install.sh with PALADIN_INSTALL_LIB_ONLY=1 (which stops right
# after its helper functions) and exercises the real stage_local_archive
# and is_dev_version. Each case runs in a subshell because the installer's
# die() exits.
#
# Usage: scripts/test-install-archive.sh   (from anywhere in the repo)
set -uo pipefail
cd "$(dirname "$0")/.."

fails=0
pass() { echo "PASS  $*"; }
fail() { echo "FAIL  $*"; fails=$((fails + 1)); }

T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT

# shellcheck source=lib/build.sh
. scripts/lib/build.sh
ASSET=$(build_and_package dev-test "$T/good" 2>/dev/null) || { echo "FAIL  could not build the test archive"; exit 1; }
GOOD="$T/good/$ASSET"

# Load the installer's helpers only. install.sh parses "$@" and sets
# -euo pipefail + an ERR trap when sourced; clear args first, relax after.
set --
PALADIN_INSTALL_LIB_ONLY=1 . scripts/install.sh
set +e; trap - ERR; set -uo pipefail
trap 'rm -rf "$T"' EXIT

# 1. Good archive with its .sha256: verified, unpacked, version read.
out=$( ( stage_local_archive "$GOOD" >/dev/null 2>&1
         [ -x "$STAGED_BIN" ] && echo "$ARCHIVE_VERSION" ) )
[ "$out" = dev-test ] && pass "good archive + .sha256 → staged, version dev-test" \
  || fail "good archive + .sha256 (got '$out')"

# 2. No .sha256 beside it: still installs, with a NOT verified warning.
mkdir -p "$T/nosha" && cp "$GOOD" "$T/nosha/"
msg=$( ( stage_local_archive "$T/nosha/$ASSET" && echo "ok:$ARCHIVE_VERSION" ) 2>&1 )
case "$msg" in *"NOT verified"*"ok:dev-test"*) pass "no .sha256 → installs with a warning" ;;
  *) fail "no .sha256 (got: $msg)" ;; esac

# 3. Tampered .sha256: refused.
mkdir -p "$T/bad" && cp "$GOOD" "$T/bad/"
echo "0000000000000000000000000000000000000000000000000000000000000000  $ASSET" > "$T/bad/$ASSET.sha256"
if ( stage_local_archive "$T/bad/$ASSET" ) >/dev/null 2>&1; then fail "tampered .sha256 was accepted"
else pass "tampered .sha256 → refused"; fi

# 4. A .sha256 that names a DIFFERENT file must not vouch for this one.
mkdir -p "$T/other" && cp "$GOOD" "$T/other/"
( cd "$T/other" && echo "decoy" > decoy.tar.gz && sha256sum decoy.tar.gz > "$ASSET.sha256" )
if ( stage_local_archive "$T/other/$ASSET" ) >/dev/null 2>&1; then fail ".sha256 for another file was accepted"
else pass ".sha256 naming another file → refused"; fi

# 5. Tarball without a paladin binary: refused.
mkdir -p "$T/empty" && echo x > "$T/empty/README" && tar -czf "$T/empty/x.tar.gz" -C "$T/empty" README
if ( stage_local_archive "$T/empty/x.tar.gz" ) >/dev/null 2>&1; then fail "archive without paladin was accepted"
else pass "archive without paladin → refused"; fi

# 6. Missing file: refused.
if ( stage_local_archive "$T/nope.tar.gz" ) >/dev/null 2>&1; then fail "missing archive was accepted"
else pass "missing archive → refused"; fi

# 7. Dev-version detection used by update mode.
ok=1
for v in dev dev-abc1234 dev-abc1234-dirty unknown ""; do is_dev_version "$v" || { ok=0; echo "      '$v' should be dev"; }; done
for v in v0.3.0 v0.1.9 development; do ! is_dev_version "$v" || { ok=0; echo "      '$v' should not be dev"; }; done
[ "$ok" = 1 ] && pass "is_dev_version" || fail "is_dev_version"

# 8. --local and --local-archive together: rejected before anything runs.
# (Asserts the message: a bare non-zero exit could just be the root check.)
msg=$(bash scripts/install.sh --local --local-archive "$GOOD" 2>&1)
case "$msg" in *"can't be combined"*) pass "--local + --local-archive → rejected" ;;
  *) fail "--local + --local-archive (got: $msg)" ;; esac

echo
if [ "$fails" = 0 ]; then echo "All --local-archive checks passed."; else echo "$fails check(s) FAILED."; exit 1; fi
