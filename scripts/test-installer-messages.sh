#!/usr/bin/env bash
# Paladin — laptop check of the installer's first-login messaging.
#
# Regression: a fresh-install VM test (2026-10-04, main's installer + the
# v0.3.0 binary) printed "use the setup token shown above" when no token had
# been shown, right under "REST password: stored in /etc/paladin/config.json",
# and the game's REST password got typed in as the Paladin login. The summary
# must only describe what actually happened.
#
# Sources install.sh with PALADIN_INSTALL_LIB_ONLY=1 and runs the real
# print_summary against stub `paladin` binaries. No root, no network.
#
# Usage: scripts/test-installer-messages.sh   (from anywhere in the repo)
set -uo pipefail
cd "$(dirname "$0")/.."

fails=0
pass() { echo "PASS  $*"; }
fail() { echo "FAIL  $*"; fails=$((fails + 1)); }

T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT

# stub <name> <setup-token body>: a fake binary. `version` reports v9.9.9;
# `setup-token` runs the given body.
stub() {
  cat > "$T/$1" <<EOF
#!/usr/bin/env bash
case "\$1" in
  version) echo "paladin v9.9.9" ;;
  setup-token) $2 ;;
  *) echo "usage" >&2; exit 2 ;;
esac
EOF
  chmod +x "$T/$1"
}
stub token       'echo 0123456789abcdef0123456789abcdef; echo "Enter this token…" >&2'
stub complete    'echo "Setup is already complete: an admin account exists." >&2'
# v0.3.0's CLI: unknown subcommand → usage on stderr, exit 2.
stub unsupported 'echo "paladin v0.3.0 — usage…" >&2; exit 2'
stub failed      'echo "ERROR: open /home/palworld/paladin-config/setup-token: permission denied" >&2; exit 1'

set --
PALADIN_INSTALL_LIB_ONLY=1 . scripts/install.sh
set +e; trap - ERR; set -uo pipefail
trap 'rm -rf "$T"' EXIT

# Variables print_summary reads (normally set by the installer run).
ip=192.0.2.10; game_port=8211; WEB_PORT=8080; SVC_USER=palworld
SERVER_UNIT=palserver.service; PALADIN_UNIT=paladin.service; CONF=/etc/paladin/config.json

summary() { BIN="$T/$1"; print_summary 2>&1 | sed 's/\x1b\[[0-9;]*m//g'; }
has()  { case "$1" in *"$2"*) return 0 ;; *) return 1 ;; esac; }

for c in token complete unsupported failed; do
  out=$(summary "$c")
  # Every case: the REST line must say it's not the Paladin login.
  has "$out" "not your Paladin login" && pass "$c: REST line says it's not the Paladin login" \
    || fail "$c: REST line not relabelled"
  has "$out" "REST password: stored" && fail "$c: old ambiguous REST line still printed"
  # "shown above" only when a token really was shown.
  if [ "$c" = token ]; then
    has "$out" "0123456789abcdef0123456789abcdef" && has "$out" "SETUP TOKEN" \
      && pass "token: highlighted token block printed" || fail "token: token block missing"
    has "$out" "shown above" && pass "token: says 'shown above'" || fail "token: missing 'shown above'"
    # The block must sit right under the Web UI line.
    next=$(printf '%s\n' "$out" | grep -A1 'Web UI:' | tail -1)
    has "$next" "SETUP TOKEN" && pass "token: block directly under the Web UI URL" \
      || fail "token: block not under the Web UI URL (next line: $next)"
  else
    has "$out" "shown above" && fail "$c: says 'shown above' with no token shown" \
      || pass "$c: never says 'shown above'"
    has "$out" "SETUP TOKEN" && fail "$c: printed a token block"
  fi
done

out=$(summary complete)
has "$out" "sign in with your Paladin admin password" && pass "complete: tells you to sign in" \
  || fail "complete: no sign-in instruction"

out=$(summary unsupported)
has "$out" "predates setup tokens" && has "$out" "v9.9.9" && pass "unsupported: says the build predates setup tokens" \
  || fail "unsupported: wrong message"

out=$(summary failed)
has "$out" "permission denied" && has "$out" "sudo paladin setup-token" \
  && pass "failed: shows the error and the command to get the token" || fail "failed: wrong message"

# Terminology: the token is never called a password.
for c in token failed; do
  out=$(summary "$c")
  printf '%s\n' "$out" | grep -i 'token' | grep -qi 'token password\|one[- ]time password' \
    && fail "$c: token called a password" || pass "$c: token never called a password"
done

echo
if [ "$fails" = 0 ]; then echo "All installer messaging checks passed."; else echo "$fails check(s) FAILED."; exit 1; fi
