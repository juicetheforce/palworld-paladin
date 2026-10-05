#!/usr/bin/env bash
# Paladin — install & update script
# https://github.com/juicetheforce/palworld-paladin
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/juicetheforce/palworld-paladin/main/scripts/install.sh | sudo bash
#   sudo ./install.sh [--check] [--local | --local-archive FILE] [--port N]
#
#   --check   Detect and report only. Changes NOTHING. Run this first.
#   --local   Install the ./paladin binary from the current directory
#             instead of downloading a GitHub release (for dev builds).
#   --local-archive FILE
#             Install from a release-format tarball on disk instead of
#             downloading one; verified against FILE.sha256 if it sits
#             beside it. Used by scripts/deploy-test.sh; works in both
#             install and update mode.
#   --port N  Web UI port (default 8080).
#
# Scenarios handled:
#   0. Paladin already installed          -> update binary + restart
#   1. Fresh box, no Palworld server      -> full install (server + Paladin)
#   2. Existing server, nothing managing  -> adopt (detect user/paths, author unit)
#   3. Existing server + rival supervisor -> stop/disable theirs (with consent), adopt
set -euo pipefail
trap 'echo "[paladin] FATAL: command failed at line $LINENO (rerunning the installer is safe)" >&2' ERR

REPO="juicetheforce/palworld-paladin"
PALWORLD_APP_ID=2394010
# sav_cli sidecar pin (DESIGN.md rev 18a) — upgraded deliberately, never "latest".
SAV_URL="https://github.com/zaigie/palworld-server-tool/releases/download/v0.12.2/pst_v0.12.2_linux_x86_64.tar.gz"
SAV_ARCHIVE_SHA="831c87f3df171a1d63ddeb4e231b5427ead262cbc6466e0ff196ff08a6b35304"
SAV_BIN_SHA="bff95acdaca261b1f3c1750b9f80bb6b1cfcbf830e9a1f42be49a8b56ce7b7d1"

BIN=/usr/local/bin/paladin
CONF_DIR=/etc/paladin
CONF="$CONF_DIR/config.json"
SUDOERS=/etc/sudoers.d/paladin
PALADIN_UNIT=paladin.service
SERVER_UNIT=palserver.service

CHECK_ONLY=0; LOCAL_BIN=0; LOCAL_ARCHIVE=""; WEB_PORT=8080
while [ $# -gt 0 ]; do
  case "$1" in
    --check) CHECK_ONLY=1 ;;
    --local) LOCAL_BIN=1 ;;
    --local-archive)
      LOCAL_ARCHIVE="${2:-}"
      [ -n "$LOCAL_ARCHIVE" ] || { echo "--local-archive needs a file path" >&2; exit 2; }
      shift ;;
    --port)  WEB_PORT="$2"; shift ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac; shift
done
if [ "$LOCAL_BIN" = 1 ] && [ -n "$LOCAL_ARCHIVE" ]; then
  echo "--local and --local-archive can't be combined (pick one binary source)" >&2; exit 2
fi

c_grn=$'\033[32m'; c_ylw=$'\033[33m'; c_red=$'\033[31m'; c_off=$'\033[0m'
say()  { echo "${c_grn}[paladin]${c_off} $*"; }
warn() { echo "${c_ylw}[paladin]${c_off} $*"; }
die()  { echo "${c_red}[paladin] ERROR:${c_off} $*" >&2; exit 1; }

# Random password, SIGPIPE-safe: a finite read from urandom FIRST, then
# filter. (tr reading urandom directly into head dies of SIGPIPE under
# pipefail — the classic.)
gen_pw() { head -c 512 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 20; }

# ---------- first-login messaging ----------
# Every line here is derived from what actually happened. A fresh-install
# test (2026-10-04) printed "use the setup token shown above" when no token
# had been shown, right under the game's REST password line, and the REST
# password got typed in as the Paladin login. See learnings.md.
#
# report_setup_token asks the installed binary for the first-run setup
# token and sets SETUP_STATE:
#   token        exit 0 + token on stdout: printed in a highlighted block
#   complete     exit 0 + empty stdout: an admin already exists
#   unsupported  exit 2: an older binary without the subcommand (its CLI
#                prints usage and exits 2 on unknown commands)
#   failed       anything else; SETUP_ERR holds the first stderr line
# It never fails the installer.
report_setup_token() {
  local tok rc errf
  errf=$(mktemp)
  if tok=$("$BIN" setup-token 2>"$errf"); then rc=0; else rc=$?; fi
  SETUP_ERR=$(head -n 1 "$errf" | sed 's/^ERROR: //'); rm -f "$errf"
  if [ "$rc" = 0 ] && [ -n "$tok" ]; then
    SETUP_STATE=token
    local hl=$'\033[1;33m'
    say "   ${hl}================= SETUP TOKEN =================${c_off}"
    say "   ${hl}   $tok${c_off}"
    say "   ${hl}===============================================${c_off}"
    say "   A one-time code, not a password: enter it in the Web UI,"
    say "   then create your Paladin admin password."
  elif [ "$rc" = 0 ]; then
    SETUP_STATE=complete
  elif [ "$rc" = 2 ]; then
    SETUP_STATE=unsupported
  else
    SETUP_STATE=failed
    warn "   Couldn't get the setup token: ${SETUP_ERR:-unknown error}"
  fi
}

# setup_next_step prints the closing instruction for SETUP_STATE. It must
# never refer to output that wasn't printed.
setup_next_step() {
  case "${SETUP_STATE:-}" in
    token)
      say " Open the Web UI, enter the setup token shown above, then create your"
      say " Paladin admin password." ;;
    complete)
      say " Open the Web UI and sign in with your Paladin admin password."
      say " Forgot it? Run: sudo paladin reset-password" ;;
    unsupported)
      local ver; ver=$("$BIN" version 2>/dev/null | awk '{print $2}' || true)
      warn " This Paladin build (${ver:-unknown}) predates setup tokens. If you haven't created"
      warn " your Paladin admin password yet, open the Web UI and do it now, before anyone"
      warn " else on your network can." ;;
    *)
      say " Get the setup token with: sudo paladin setup-token"
      say " then open the Web UI, enter it, and create your Paladin admin password." ;;
  esac
}

# print_summary: the end-of-install summary. Callers set ip and game_port.
print_summary() {
  echo
  say "======================================================"
  say " Paladin is installed and running."
  say "   Web UI:        http://${ip:-<this-host>}:$WEB_PORT"
  report_setup_token
  say "   Game server:   ${ip:-<this-host>}:${game_port:-8211}/udp"
  say "   Service user:  $SVC_USER"
  say "   Units:         $SERVER_UNIT, $PALADIN_UNIT"
  say "   Config:        $CONF"
  say "   Game server REST API password (not your Paladin login): stored in $CONF"
  say "   (and the game ini). Paladin uses it to talk to the game; you never type it."
  setup_next_step
  say " The UI listens on all interfaces so LAN devices can reach it: keep port $WEB_PORT"
  say " off the internet (firewall, no port-forward)."
  say "======================================================"
}

# Interactive prompts must read the terminal, not stdin (we may be piped).
ask() { # ask "Question" -> sets $REPLY_ANS to y/n
  local q="$1"
  if [ ! -t 0 ] && [ ! -e /dev/tty ]; then die "no TTY for confirmation: $q (run the script from a terminal)"; fi
  read -r -p "$q [y/N] " REPLY_ANS < /dev/tty
  case "$REPLY_ANS" in y|Y|yes|YES) REPLY_ANS=y ;; *) REPLY_ANS=n ;; esac
}

# Development builds report "dev" (plain `go build`) or "dev-<commit>[-dirty]"
# (scripts/deploy-test.sh). Their version string doesn't identify their
# contents, so they never count as "already up to date".
is_dev_version() {
  case "${1:-}" in ""|dev|dev-*|unknown) return 0 ;; *) return 1 ;; esac
}

# --local-archive: verify (if a .sha256 sits beside it) and unpack the
# tarball BEFORE anything on the machine changes. Sets STAGED_BIN and
# ARCHIVE_VERSION. The hash is compared directly rather than via
# `sha256sum -c`, which would check whatever file name the .sha256 names.
stage_local_archive() { # $1=tarball
  local archive="$1" want got
  [ -f "$archive" ] || die "--local-archive: no such file: $archive"
  if [ -f "$archive.sha256" ]; then
    want=$(awk '{print $1; exit}' "$archive.sha256")
    got=$(sha256sum "$archive" | awk '{print $1}')
    [ -n "$want" ] && [ "$want" = "$got" ] || die "--local-archive: checksum mismatch for $archive (expected ${want:-nothing}, got $got)"
    say "Verified $(basename "$archive") against its .sha256."
  else
    warn "--local-archive: no $(basename "$archive").sha256 beside it — NOT verified."
  fi
  STAGE_DIR=$(mktemp -d)
  tar -xzf "$archive" -C "$STAGE_DIR" paladin 2>/dev/null || die "--local-archive: $archive has no 'paladin' binary inside"
  STAGED_BIN="$STAGE_DIR/paladin"
  [ -x "$STAGED_BIN" ] || die "--local-archive: 'paladin' in $archive is not executable"
  ARCHIVE_VERSION=$("$STAGED_BIN" version 2>/dev/null | awk '{print $2}' || true)
  [ -n "$ARCHIVE_VERSION" ] || die "--local-archive: the binary in $archive did not report a version"
}

# Sourced with PALADIN_INSTALL_LIB_ONLY=1 (by scripts/test-install-archive.sh):
# stop here, with the helpers above defined, so they can be tested on a
# laptop without root. Normal runs never set this.
if [ "${PALADIN_INSTALL_LIB_ONLY:-0}" = 1 ]; then return 0 2>/dev/null || exit 0; fi

# ---------- preflight ----------
[ "$(id -u)" = 0 ] || die "must run as root (sudo)"
[ "$(uname -m)" = x86_64 ] || die "x86_64 only (found $(uname -m))"
command -v systemctl >/dev/null || die "systemd is required"
command -v curl >/dev/null || die "curl is required"
command -v ss >/dev/null || die "ss (iproute2) is required"
[ -z "$LOCAL_ARCHIVE" ] || stage_local_archive "$LOCAL_ARCHIVE"

# ---------- scenario 0: update an existing install ----------
if [ -x "$BIN" ] && [ -f "$CONF" ]; then
  say "Existing Paladin installation detected — update mode."
  installed=$("$BIN" version 2>/dev/null | awk '{print $2}' || echo unknown)
  if [ -n "$LOCAL_ARCHIVE" ]; then
    latest="$ARCHIVE_VERSION"
    say "Installed: $installed   Local archive: $latest"
  else
    latest=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep -oP '"tag_name":\s*"\K[^"]+' || true)
    [ -n "$latest" ] || die "could not determine latest release (GitHub API). Try again later."
    say "Installed: $installed   Latest: $latest"
  fi
  # --check stays read-only, so it never prints (or creates) a setup token.
  # Dev builds never count as up to date (see is_dev_version).
  if [ "$installed" = "$latest" ] && ! is_dev_version "$latest"; then
    say "Already up to date. Nothing to do."
    [ "$CHECK_ONLY" = 1 ] || { report_setup_token; setup_next_step; }
    exit 0
  fi
  [ "$CHECK_ONLY" = 1 ] && { say "--check: would update $installed -> $latest and restart $PALADIN_UNIT."; exit 0; }
  case "$installed" in dev|dev-*)
    is_dev_version "$latest" || warn "Installed is a development build ($installed); this replaces it with release $latest." ;;
  esac
  ask "Update Paladin $installed -> $latest and restart the web service? (The game server is NOT touched.)"
  [ "$REPLY_ANS" = y ] || { say "Aborted."; report_setup_token; setup_next_step; exit 0; }
  if [ -n "$LOCAL_ARCHIVE" ]; then
    install -m 0755 "$STAGED_BIN" "$BIN"; rm -rf "$STAGE_DIR"
  else
    tmp=$(mktemp -d)
    curl -fsSL -o "$tmp/paladin.tar.gz" \
      "https://github.com/$REPO/releases/download/$latest/paladin_${latest}_linux_x86_64.tar.gz"
    tar -xzf "$tmp/paladin.tar.gz" -C "$tmp" paladin
    install -m 0755 "$tmp/paladin" "$BIN"; rm -rf "$tmp"
  fi
  systemctl restart "$PALADIN_UNIT"
  say "Updated to $latest and restarted. Done."
  report_setup_token; setup_next_step
  exit 0
fi

# ---------- detection engine ----------
say "Detecting the current state of this machine…"

DOCKERIZED=0; SRV_PID=""; SRV_USER=""; SRV_HOME=""; INSTALL_DIR=""; SRV_UNIT_EXISTING=""; SRV_LAUNCH=""; SRV_PPID=""; SRV_PARENT_SUPERVISOR=""; SRV_PARENT_DESC=""
if command -v docker >/dev/null; then
  # grep reads all input (no -q) so docker never dies of SIGPIPE under pipefail.
  if [ -n "$(docker ps --format '{{.Image}} {{.Names}}' 2>/dev/null | grep -i palworld || true)" ]; then
    DOCKERIZED=1
  fi
fi

# Note: pgrep -x is useless here (kernel comm names truncate at 15 chars);
# match the full command line instead.
SRV_PID=$(pgrep -f 'PalServer-Linux-Shipping' | head -1 || true)
if [ -n "$SRV_PID" ]; then
  SRV_USER=$(ps -o user= -p "$SRV_PID" | tr -d ' ')
  exe=$(readlink -f "/proc/$SRV_PID/exe")               # …/Pal/Binaries/Linux/PalServer-Linux-Shipping
  INSTALL_DIR=$(dirname "$(dirname "$(dirname "$(dirname "$exe")")")")
  SRV_HOME=$(getent passwd "$SRV_USER" | cut -d: -f6)
  SRV_UNIT_EXISTING=$(ps -o unit= -p "$SRV_PID" | tr -d ' ')
  case "$SRV_UNIT_EXISTING" in ""|-|user*.slice|session*.scope) SRV_UNIT_EXISTING="" ;; esac
  if [ -z "$SRV_UNIT_EXISTING" ]; then
    SRV_PPID=$(ps -o ppid= -p "$SRV_PID" | tr -d ' ')
    pcomm=$(ps -o comm= -p "$SRV_PPID" 2>/dev/null | tr -d ' ' || true)
    pargs=$(ps -o args= -p "$SRV_PPID" 2>/dev/null || true)
    SRV_LAUNCH="${pcomm:-unknown}"   # screen / tmux / bash / palworld-admin …
    # Known process-supervisors relaunch the server if only the child dies.
    if echo "$pargs $pcomm" | grep -qiE 'palworld[-_]?admin|palworld[-_]?server[-_]?manager|pst([^a-z]|$)'; then
      SRV_PARENT_SUPERVISOR="$SRV_PPID"
      SRV_PARENT_DESC="$pcomm ($pargs)"
    fi
  fi
fi

PORT_USER=$(ss -ltnpH "sport = :$WEB_PORT" 2>/dev/null | grep -oP 'users:\(\("\K[^"]+' | head -1 || true)
RIVALS=$(systemctl list-units --type=service --all --no-legend 2>/dev/null \
  | awk '{print $1}' \
  | grep -Ei '^(pst|palpanel|palworld-server-tool|palworld[-_]?admin|palworld-server-manager)\.service' \
  | grep -Fv "$SERVER_UNIT" | grep -Fv "$PALADIN_UNIT" \
  | { [ -n "$SRV_UNIT_EXISTING" ] && grep -Fv "$SRV_UNIT_EXISTING" || cat; } || true)

echo
say "---------- detection report ----------"
if [ "$DOCKERIZED" = 1 ]; then warn "Docker Palworld container detected."; fi
if [ -n "$SRV_PID" ]; then
  say "Palworld server RUNNING  pid=$SRV_PID user=$SRV_USER"
  say "  install dir: $INSTALL_DIR"
  if [ -n "$SRV_UNIT_EXISTING" ]; then say "  supervised by systemd unit: $SRV_UNIT_EXISTING"
  elif [ -n "$SRV_PARENT_SUPERVISOR" ]; then warn "  supervised by a PROCESS manager: $SRV_PARENT_DESC (pid $SRV_PARENT_SUPERVISOR) — it will be stopped during adoption"
  else warn "  not systemd-managed (launch parent: ${SRV_LAUNCH:-unknown})"; fi
else
  say "No running Palworld server found -> fresh-install scenario."
fi
[ -n "$PORT_USER" ] && warn "Port $WEB_PORT is in use by: $PORT_USER"
[ -n "$RIVALS" ] && warn "Possible rival supervisor unit(s): $RIVALS"
say "--------------------------------------"
echo
if [ "$CHECK_ONLY" = 1 ]; then
  say "--check complete. No changes were made."
  exit 0
fi

[ "$DOCKERIZED" = 1 ] && die "This server runs in Docker. Container servers are configured via their compose file and supervised by the Docker daemon — Paladin manages systemd/bare-metal servers and will not fight the Docker daemon. (See the project README.)"
[ -n "$PORT_USER" ] && die "Port $WEB_PORT is occupied by '$PORT_USER'. Stop it or rerun with --port <other>."

# ---------- helpers used by both branches ----------
write_server_unit() { # $1=user $2=install_dir
  cat > "/etc/systemd/system/$SERVER_UNIT" <<UNIT
[Unit]
Description=Palworld dedicated server (managed by Paladin)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$1
WorkingDirectory=$2
ExecStart=$2/PalServer.sh -enable-gamedata-api
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
UNIT
}

write_paladin_unit() { # $1=user
  cat > "/etc/systemd/system/$PALADIN_UNIT" <<UNIT
[Unit]
Description=Paladin — Palworld server admin panel
After=network-online.target $SERVER_UNIT
Wants=network-online.target

[Service]
Type=simple
User=$1
ExecStart=$BIN serve --config $CONF
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
UNIT
}

write_sudoers() { # $1=user — exactly the supervise layer's contract, nothing more
  {
    echo "# Paladin scoped grant: service control of $SERVER_UNIT only."
    for verb in start stop restart kill show is-active status; do
      echo "$1 ALL=(root) NOPASSWD: /usr/bin/systemctl $verb $SERVER_UNIT"
    done
    # sudoers matches arguments EXACTLY: the force-kill escalation invokes
    # 'kill -s SIGKILL <unit>', which the bare 'kill' line does not cover
    # (first discovered when a real world outlived the stop grace window).
    echo "$1 ALL=(root) NOPASSWD: /usr/bin/systemctl kill -s SIGKILL $SERVER_UNIT"
  } > "$SUDOERS"
  chmod 0440 "$SUDOERS"
  visudo -cf "$SUDOERS" >/dev/null || die "generated sudoers failed validation"
}

fetch_sav_cli() { # $1=user $2=home
  local tools="$2/paladin-tools" tmp; tmp=$(mktemp -d)
  say "Fetching pinned sav_cli sidecar (PST v0.12.2)…"
  curl -fsSL -o "$tmp/pst.tar.gz" "$SAV_URL"
  echo "$SAV_ARCHIVE_SHA  $tmp/pst.tar.gz" | sha256sum -c - >/dev/null || die "sav_cli archive checksum mismatch"
  tar -xzf "$tmp/pst.tar.gz" -C "$tmp" sav_cli NOTICE
  echo "$SAV_BIN_SHA  $tmp/sav_cli" | sha256sum -c - >/dev/null || die "sav_cli binary checksum mismatch"
  mkdir -p "$tools"; mv "$tmp/sav_cli" "$tmp/NOTICE" "$tools/"
  chmod 0755 "$tools/sav_cli"; chown -R "$1:$1" "$tools"; rm -rf "$tmp"
}

install_paladin_binary() {
  if [ -n "$LOCAL_ARCHIVE" ]; then
    install -m 0755 "$STAGED_BIN" "$BIN"; rm -rf "$STAGE_DIR"
    say "Installed Paladin $ARCHIVE_VERSION from $LOCAL_ARCHIVE."
    return
  fi
  if [ "$LOCAL_BIN" = 1 ]; then
    [ -x ./paladin ] || die "--local: no executable ./paladin in $(pwd)"
    install -m 0755 ./paladin "$BIN"; say "Installed local ./paladin build."
    return
  fi
  local latest tmp
  latest=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep -oP '"tag_name":\s*"\K[^"]+' || true)
  [ -n "$latest" ] || die "no GitHub release found for $REPO. Build locally and rerun with --local."
  tmp=$(mktemp -d)
  curl -fsSL -o "$tmp/paladin.tar.gz" \
    "https://github.com/$REPO/releases/download/$latest/paladin_${latest}_linux_x86_64.tar.gz"
  tar -xzf "$tmp/paladin.tar.gz" -C "$tmp" paladin
  install -m 0755 "$tmp/paladin" "$BIN"; rm -rf "$tmp"
  say "Installed Paladin $latest."
}

read_ini_value() { # $1=ini $2=key -> quoted or bare value from OptionSettings
  grep -oP "$2=\"?\K[^\",)]*" "$1" 2>/dev/null | head -1 || true
}

write_config() { # $1=install_dir $2=data_dir $3=admin_pw
  mkdir -p "$CONF_DIR"
  cat > "$CONF" <<JSON
{
  "listen": "0.0.0.0:$WEB_PORT",
  "server_unit": "$SERVER_UNIT",
  "install_dir": "$1",
  "data_dir": "$2",
  "admin_password": "$3",
  "sav_cli": "$2/paladin-tools/sav_cli"
}
JSON
  chmod 0640 "$CONF"; chown "root:$SVC_USER" "$CONF"
}

# ---------- branch: fresh install ----------
fresh_install() {
  SVC_USER=palworld
  say "Fresh install: creating service account, SteamCMD, and the server."
  ask "Proceed with a full new Palworld server install as user '$SVC_USER'?"
  [ "$REPLY_ANS" = y ] || { say "Aborted."; exit 0; }

  # SteamCMD is a 32-bit binary: the one real dependency is 32-bit glibc.
  # Branch on the package manager for the two big families; refuse honestly
  # elsewhere rather than guessing at a distro's multilib ceremony.
  if command -v apt-get >/dev/null; then
    say "Installing packages (lib32gcc-s1, tar) via apt…"
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -qq && apt-get install -y -qq lib32gcc-s1 tar >/dev/null
  elif command -v dnf >/dev/null; then
    say "Installing packages (32-bit glibc, tar) via dnf…"
    dnf install -y -q glibc.i686 libstdc++.i686 tar >/dev/null
  else
    die "No apt or dnf found. Fresh installs need 32-bit glibc for SteamCMD — install it with your distro's package manager (Arch: enable multilib and install lib32-glibc; immutable distros: layer glibc.i686), then rerun this installer. Adopting an ALREADY-RUNNING server does not need this step."
  fi

  id "$SVC_USER" >/dev/null 2>&1 || useradd -r -m -d "/home/$SVC_USER" -s /usr/sbin/nologin "$SVC_USER"
  SRV_HOME="/home/$SVC_USER"; INSTALL_DIR="$SRV_HOME/palserver"

  say "Installing SteamCMD…"
  sudo -u "$SVC_USER" mkdir -p "$SRV_HOME/steamcmd"
  curl -fsSL https://steamcdn-a.akamaihd.net/client/installer/steamcmd_linux.tar.gz \
    | sudo -u "$SVC_USER" tar -xz -C "$SRV_HOME/steamcmd"

  say "Installing the Palworld dedicated server (app $PALWORLD_APP_ID) — this downloads several GB…"
  # SteamCMD's first run after self-bootstrap notoriously fails with
  # "Missing configuration"; a retry succeeds. It can also exit 0 on
  # failure, so success is judged by the server actually existing.
  installed=0
  for attempt in 1 2 3; do
    if sudo -u "$SVC_USER" "$SRV_HOME/steamcmd/steamcmd.sh" +force_install_dir "$INSTALL_DIR" \
        +login anonymous +app_update "$PALWORLD_APP_ID" validate +quit \
        && [ -x "$INSTALL_DIR/PalServer.sh" ]; then
      installed=1; break
    fi
    warn "SteamCMD attempt $attempt did not produce a server (its first run often fails) — retrying…"
    sleep 3
  done
  [ "$installed" = 1 ] || die "SteamCMD could not install the server after 3 attempts. This installer is safe to rerun — check network/disk and run it again."

  # First-boot config: run once briefly? No — author the ini directly.
  local ini_dir="$INSTALL_DIR/Pal/Saved/Config/LinuxServer"
  ADMIN_PW=$(gen_pw)
  sudo -u "$SVC_USER" mkdir -p "$ini_dir"
  cat > "$ini_dir/PalWorldSettings.ini" <<INI
[/Script/Pal.PalGameWorldSettings]
OptionSettings=(ServerName="Palworld Server (Paladin)",AdminPassword="$ADMIN_PW",RESTAPIEnabled=True,RESTAPIPort=8212,RCONEnabled=False)
INI
  chown "$SVC_USER:$SVC_USER" "$ini_dir/PalWorldSettings.ini"
  printf 'AdminPassword: %s\n' "$ADMIN_PW" > "$SRV_HOME/palserver-credentials.txt"
  chown "$SVC_USER:$SVC_USER" "$SRV_HOME/palserver-credentials.txt"; chmod 0600 "$SRV_HOME/palserver-credentials.txt"

  write_server_unit "$SVC_USER" "$INSTALL_DIR"
  systemctl daemon-reload && systemctl enable --now "$SERVER_UNIT"
  say "Server starting under systemd."
}

# ---------- branch: adopt (scenarios 2 & 3) ----------
adopt_install() {
  SVC_USER="$SRV_USER"
  say "Adopting the detected server (user=$SVC_USER, dir=$INSTALL_DIR)."
  ask "Manage THIS server with Paladin? Its current launcher will be replaced with a systemd unit."
  [ "$REPLY_ANS" = y ] || { say "Aborted."; exit 0; }

  # Rival supervisors: consent, then stop+disable.
  if [ -n "$RIVALS" ]; then
    for u in $RIVALS; do
      ask "Stop and disable rival supervisor '$u'?"
      if [ "$REPLY_ANS" = y ]; then systemctl disable --now "$u" || warn "could not disable $u — continuing"; fi
    done
  fi

  local ini="$INSTALL_DIR/Pal/Saved/Config/LinuxServer/PalWorldSettings.ini"
  [ -f "$ini" ] || die "expected ini not found: $ini"
  ADMIN_PW=$(read_ini_value "$ini" AdminPassword)
  local rest_on; rest_on=$(read_ini_value "$ini" RESTAPIEnabled)
  if [ -z "$ADMIN_PW" ] || [ "${rest_on,,}" != "true" ]; then
    warn "REST API is not fully configured in the ini (enabled='$rest_on', password $( [ -n "$ADMIN_PW" ] && echo set || echo missing ))."
    ask "Enable the REST API (and set an admin password if missing) in PalWorldSettings.ini? The server restarts as part of adoption anyway."
    [ "$REPLY_ANS" = y ] || die "Paladin requires the REST API. Aborting without changes."
    [ -n "$ADMIN_PW" ] || ADMIN_PW=$(gen_pw)
    cp -a "$ini" "$ini.paladin-pre-adopt"
    python3 - "$ini" "$ADMIN_PW" <<'PY'
import re, sys
path, pw = sys.argv[1], sys.argv[2]
s = open(path).read()
def setkv(s, k, v):
    if re.search(k + r'=', s): return re.sub(k + r'=("?)[^",)]*\1', f'{k}={v}', s, count=1)
    return s.replace('OptionSettings=(', f'OptionSettings=({k}={v},', 1)
s = setkv(s, 'RESTAPIEnabled', 'True')
s = setkv(s, 'RESTAPIPort', '8212')
s = setkv(s, 'AdminPassword', f'"{pw}"')
open(path, 'w').write(s)
PY
    say "ini updated (pre-adopt copy at $ini.paladin-pre-adopt)."
  fi

  # Stop the current launcher.
  if [ -n "$SRV_UNIT_EXISTING" ]; then
    say "Stopping and disabling existing unit $SRV_UNIT_EXISTING…"
    systemctl disable --now "$SRV_UNIT_EXISTING"
    # Remove the superseded unit file so nothing can hand-start the old
    # launcher later (consent to replace it was given above).
    if [ "$SRV_UNIT_EXISTING" != "$SERVER_UNIT" ] && [ -f "/etc/systemd/system/$SRV_UNIT_EXISTING" ]; then
      rm -f "/etc/systemd/system/$SRV_UNIT_EXISTING"
      systemctl daemon-reload
      say "Removed superseded unit file $SRV_UNIT_EXISTING."
    fi
  elif [ -n "$SRV_PID" ]; then
    if [ -n "$SRV_PARENT_SUPERVISOR" ]; then
      ask "Stop the managing process '$SRV_LAUNCH' (pid $SRV_PARENT_SUPERVISOR)? It relaunches the server if only the server is killed, so it must go first."
      [ "$REPLY_ANS" = y ] || die "Cannot adopt while another manager supervises the server. Aborting without changes."
      kill "$SRV_PARENT_SUPERVISOR" || true
      for i in $(seq 1 15); do kill -0 "$SRV_PARENT_SUPERVISOR" 2>/dev/null || break; sleep 1; done
      kill -0 "$SRV_PARENT_SUPERVISOR" 2>/dev/null && { warn "manager still alive; SIGKILL"; kill -9 "$SRV_PARENT_SUPERVISOR" || true; sleep 1; }
    fi
    warn "Stopping the server process (players will be disconnected)…"
    kill "$SRV_PID" 2>/dev/null || true
    for i in $(seq 1 30); do kill -0 "$SRV_PID" 2>/dev/null || break; sleep 1; done
    kill -0 "$SRV_PID" 2>/dev/null && { warn "still alive; SIGKILL"; kill -9 "$SRV_PID" || true; sleep 2; }
    # Fight detector: if something respawned the server, a manager we did
    # not identify is still alive — stop rather than wrestle it.
    sleep 3
    if pgrep -f 'PalServer-Linux-Shipping' >/dev/null; then
      die "The server was restarted by something we did not stop (pid $(pgrep -f 'PalServer-Linux-Shipping' | head -1)). Identify and stop the remaining manager, then rerun."
    fi
  fi

  # Guarantee updates work post-adopt: whatever the old manager used for
  # steamcmd, make sure one exists where Paladin's update runner looks.
  if [ ! -x "$SRV_HOME/steamcmd/steamcmd.sh" ] && ! command -v steamcmd >/dev/null; then
    say "Installing SteamCMD for the server-update feature…"
    sudo -u "$SVC_USER" mkdir -p "$SRV_HOME/steamcmd"
    curl -fsSL https://steamcdn-a.akamaihd.net/client/installer/steamcmd_linux.tar.gz \
      | sudo -u "$SVC_USER" tar -xz -C "$SRV_HOME/steamcmd"
  fi

  write_server_unit "$SVC_USER" "$INSTALL_DIR"
  systemctl daemon-reload && systemctl enable --now "$SERVER_UNIT"
  say "Server restarted under Paladin's systemd unit."
}

# ---------- run the chosen branch ----------
if [ -z "$SRV_PID" ]; then fresh_install; else adopt_install; fi

# ---------- common tail ----------
write_sudoers "$SVC_USER"
fetch_sav_cli "$SVC_USER" "$SRV_HOME"
install_paladin_binary
write_config "$INSTALL_DIR" "$SRV_HOME" "$ADMIN_PW"
write_paladin_unit "$SVC_USER"
systemctl daemon-reload && systemctl enable --now "$PALADIN_UNIT"

# ---------- summary ----------
ip=$(hostname -I 2>/dev/null | awk '{print $1}')
game_port=$(read_ini_value "$INSTALL_DIR/Pal/Saved/Config/LinuxServer/PalWorldSettings.ini" PublicPort)
print_summary
