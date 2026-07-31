#!/usr/bin/env bash
# Paladin — uninstall script
# https://github.com/juicetheforce/palworld-paladin
#
# Usage:
#   sudo ./uninstall.sh              # remove Paladin ONLY; the game server keeps running
#   sudo ./uninstall.sh --everything # also remove server supervision (unit); world data
#                                    # deletion is a separate, explicit confirmation
#
# Default philosophy: Paladin leaves the way it came. The game server it
# supervised keeps running under its systemd unit — removing the admin
# panel must never take the game down with it.
set -euo pipefail

BIN=/usr/local/bin/paladin
CONF_DIR=/etc/paladin
CONF="$CONF_DIR/config.json"
SUDOERS=/etc/sudoers.d/paladin
PALADIN_UNIT=paladin.service
SERVER_UNIT=palserver.service

c_grn=$'\033[32m'; c_ylw=$'\033[33m'; c_red=$'\033[31m'; c_off=$'\033[0m'
say()  { echo "${c_grn}[paladin]${c_off} $*"; }
warn() { echo "${c_ylw}[paladin]${c_off} $*"; }
die()  { echo "${c_red}[paladin] ERROR:${c_off} $*" >&2; exit 1; }
ask() {
  read -r -p "$1 [y/N] " REPLY_ANS < /dev/tty
  case "$REPLY_ANS" in y|Y|yes|YES) REPLY_ANS=y ;; *) REPLY_ANS=n ;; esac
}

EVERYTHING=0
[ "${1:-}" = "--everything" ] && EVERYTHING=1
[ "$(id -u)" = 0 ] || die "must run as root (sudo)"

# Read what the installer recorded, so we remove exactly what we own.
DATA_DIR=""; INSTALL_DIR=""
if [ -f "$CONF" ]; then
  DATA_DIR=$(grep -oP '"data_dir":\s*"\K[^"]*' "$CONF" || true)
  INSTALL_DIR=$(grep -oP '"install_dir":\s*"\K[^"]*' "$CONF" || true)
fi

say "This will remove: $PALADIN_UNIT, $BIN, $CONF_DIR, $SUDOERS"
[ -n "$DATA_DIR" ] && say "Paladin data in $DATA_DIR (backups/journal/config/logs) will be KEPT — it is yours."
if [ "$EVERYTHING" = 1 ]; then
  warn "--everything: server supervision ($SERVER_UNIT) will ALSO be removed."
else
  say "The game server ($SERVER_UNIT) keeps running. Use --everything to remove it too."
fi
ask "Proceed?"
[ "$REPLY_ANS" = y ] || { say "Aborted. Nothing was changed."; exit 0; }

# ---- Paladin itself ----
if systemctl list-unit-files --no-legend 2>/dev/null | grep -q "^$PALADIN_UNIT"; then
  systemctl disable --now "$PALADIN_UNIT" 2>/dev/null || true
  rm -f "/etc/systemd/system/$PALADIN_UNIT"
fi
rm -f "$BIN" "$SUDOERS"
rm -rf "$CONF_DIR"
# The sidecar was fetched by the installer; it goes too (NOTICE included).
[ -n "$DATA_DIR" ] && rm -rf "$DATA_DIR/paladin-tools"
systemctl daemon-reload
say "Paladin removed."

# ---- optionally, the server's supervision ----
if [ "$EVERYTHING" = 1 ]; then
  if systemctl list-unit-files --no-legend 2>/dev/null | grep -q "^$SERVER_UNIT"; then
    warn "Stopping and removing $SERVER_UNIT (players will be disconnected)…"
    systemctl disable --now "$SERVER_UNIT" 2>/dev/null || true
    rm -f "/etc/systemd/system/$SERVER_UNIT"
    systemctl daemon-reload
    say "Server supervision removed. Server files are untouched."
  else
    warn "$SERVER_UNIT not present; nothing to remove."
  fi

  if [ -n "$INSTALL_DIR" ] && [ -d "$INSTALL_DIR" ]; then
    echo
    warn "The server install (including WORLD SAVES) is at: $INSTALL_DIR"
    warn "Deleting it is IRREVERSIBLE. Paladin backups under $DATA_DIR/paladin-backups"
    warn "would survive, but the live world would be gone."
    read -r -p "Type DELETE to erase the server directory, anything else to keep it: " confirm < /dev/tty
    if [ "$confirm" = "DELETE" ]; then
      rm -rf "$INSTALL_DIR"
      say "Server directory deleted."
    else
      say "Server files kept at $INSTALL_DIR."
    fi
  fi
fi

echo
say "Uninstall complete."
[ -n "$DATA_DIR" ] && say "Kept (yours to delete whenever): $DATA_DIR/paladin-backups, $DATA_DIR/paladin-journal, $DATA_DIR/paladin-config, $DATA_DIR/paladin-logs"
