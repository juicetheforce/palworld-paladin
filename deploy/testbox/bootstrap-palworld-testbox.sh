#!/usr/bin/env bash
# =============================================================================
# bootstrap-palworld-testbox.sh
#
# Stands up a Palworld 1.0 dedicated server on a FRESH Ubuntu server install.
# Part of the palworld-paladin project (deploy/testbox/): this box is the
# development target for Paladin's adopt flow and the verification bench for
# data/palworld-settings.json. It intentionally creates a plain "foreign"
# systemd unit — the exact situation Paladin's adopt flow must detect.
#
# Usage:   sudo ./bootstrap-palworld-testbox.sh
# Target:  Ubuntu 22.04 / 24.04, amd64, ~16 GB RAM recommended, ~20 GB disk.
# Rerun-safe: skips what already exists, revalidates the game files.
# v1.1: fixed silent pipefail exit in password gen; SteamCMD retry loop.
# =============================================================================
set -euo pipefail

SERVICE_USER="palworld"
STEAMCMD_DIR="/home/${SERVICE_USER}/steamcmd"
INSTALL_DIR="/home/${SERVICE_USER}/palserver"
APP_ID="2394010"
UNIT_NAME="palserver.service"
CONFIG_DIR="${INSTALL_DIR}/Pal/Saved/Config/LinuxServer"
CONFIG_FILE="${CONFIG_DIR}/PalWorldSettings.ini"
CRED_FILE="/home/${SERVICE_USER}/palserver-credentials.txt"

log()  { echo -e "\n\033[1;32m==>\033[0m $*"; }
warn() { echo -e "\033[1;33mWARN:\033[0m $*"; }
die()  { echo -e "\033[1;31mERROR:\033[0m $*" >&2; exit 1; }

# ---- pre-flight -------------------------------------------------------------
[[ $EUID -eq 0 ]] || die "Run with sudo: sudo $0"
[[ "$(uname -m)" == "x86_64" ]] || die "Palworld's server is amd64-only."
command -v apt-get >/dev/null || die "This script targets Ubuntu/apt."

TOTAL_MB=$(awk '/MemTotal/ {print int($2/1024)}' /proc/meminfo)
if (( TOTAL_MB < 14000 )); then
  warn "Only ${TOTAL_MB} MB RAM detected. Pocketpair recommends 16 GB even"
  warn "for light use, and memory climbs with uptime. Proceeding anyway."
fi

FREE_GB=$(df -BG --output=avail /home | tail -1 | tr -dc '0-9')
(( FREE_GB >= 20 )) || warn "Only ${FREE_GB} GB free on /home; ~20 GB advised."

# ---- dependencies -----------------------------------------------------------
log "Installing dependencies (curl, lib32gcc-s1 for SteamCMD)"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq curl tar openssl lib32gcc-s1 >/dev/null

# ---- service account --------------------------------------------------------
if id "${SERVICE_USER}" &>/dev/null; then
  log "User '${SERVICE_USER}' already exists — reusing it"
else
  log "Creating service account '${SERVICE_USER}' (no password, not sudo)"
  useradd --system --create-home --shell /bin/bash "${SERVICE_USER}"
fi
passwd -l "${SERVICE_USER}" >/dev/null   # ensure the password stays locked

run_as() { runuser -u "${SERVICE_USER}" -- bash -c "$*"; }

# ---- SteamCMD (tarball method: no license debconf dance) --------------------
if [[ -x "${STEAMCMD_DIR}/steamcmd.sh" ]]; then
  log "SteamCMD already installed"
else
  log "Installing SteamCMD to ${STEAMCMD_DIR}"
  run_as "mkdir -p '${STEAMCMD_DIR}' && cd '${STEAMCMD_DIR}' && \
    curl -sSL https://steamcdn-a.akamaihd.net/client/installer/steamcmd_linux.tar.gz | tar zx"
fi

# ---- game server ------------------------------------------------------------
log "Installing/validating Palworld dedicated server (app ${APP_ID}) — this downloads several GB"
for ATTEMPT in 1 2 3; do
  run_as "'${STEAMCMD_DIR}/steamcmd.sh' +force_install_dir '${INSTALL_DIR}' \
    +login anonymous +app_update ${APP_ID} validate +quit" || true
  [[ -x "${INSTALL_DIR}/PalServer.sh" ]] && break
  warn "SteamCMD attempt ${ATTEMPT} didn't produce PalServer.sh (its first run after a self-update often fails spuriously); retrying..."
  sleep 3
done
[[ -x "${INSTALL_DIR}/PalServer.sh" ]] || die "PalServer.sh missing after 3 SteamCMD attempts — see output above."

# Well-known requirement: the server needs steamclient.so in ~/.steam/sdk64
log "Applying the sdk64 steamclient.so fix"
run_as "mkdir -p ~/.steam/sdk64 && \
  cp -f '${STEAMCMD_DIR}/linux32/../linux64/steamclient.so' ~/.steam/sdk64/steamclient.so 2>/dev/null || \
  cp -f '${STEAMCMD_DIR}/linux64/steamclient.so' ~/.steam/sdk64/steamclient.so"

# ---- configuration ----------------------------------------------------------
if [[ -f "${CONFIG_FILE}" ]]; then
  log "Config already exists at ${CONFIG_FILE} — leaving it untouched"
  warn "If you need REST enabled/credentials, edit it manually or delete it and rerun."
else
  log "Seeding config from DefaultPalWorldSettings.ini"
  [[ -f "${INSTALL_DIR}/DefaultPalWorldSettings.ini" ]] || die "DefaultPalWorldSettings.ini not found in ${INSTALL_DIR}"
  run_as "mkdir -p '${CONFIG_DIR}' && cp '${INSTALL_DIR}/DefaultPalWorldSettings.ini' '${CONFIG_FILE}'"

  # NOTE: do NOT generate this with `tr </dev/urandom | head` — under
  # `set -o pipefail` the SIGPIPE from head silently kills the script.
  ADMIN_PW=$(openssl rand -hex 12)

  log "Enabling REST API and setting AdminPassword (single-line-safe edits)"
  edit_key() {  # edit_key <pattern> <replacement> <label>
    if grep -q "$1" "${CONFIG_FILE}"; then
      sed -i "s/$1/$2/" "${CONFIG_FILE}"
    else
      warn "Key pattern not found for $3 — the 1.0 template may differ; set it manually."
    fi
  }
  edit_key 'AdminPassword=""'        "AdminPassword=\"${ADMIN_PW}\""  "AdminPassword"
  edit_key 'RESTAPIEnabled=False'    'RESTAPIEnabled=True'            "RESTAPIEnabled"
  edit_key 'ServerName="[^"]*"'      'ServerName="Paladin Test Box"'  "ServerName"
  chown "${SERVICE_USER}:${SERVICE_USER}" "${CONFIG_FILE}"

  # sanity: OptionSettings must still be exactly one line (the classic trap)
  OPT_LINES=$(grep -c '^OptionSettings=' "${CONFIG_FILE}" || true)
  [[ "${OPT_LINES}" == "1" ]] || die "Config damaged: expected exactly one OptionSettings line, found ${OPT_LINES}."

  install -m 600 -o "${SERVICE_USER}" -g "${SERVICE_USER}" /dev/null "${CRED_FILE}"
  cat > "${CRED_FILE}" <<EOF
Palworld test server credentials (generated $(date -Is))
AdminPassword: ${ADMIN_PW}
REST API:      http://127.0.0.1:8212  (HTTP Basic, user 'admin')
Test with:     curl -u admin:${ADMIN_PW} http://127.0.0.1:8212/v1/api/info
EOF
fi

# ---- systemd unit (deliberately plain — Paladin will adopt this later) ------
if [[ -f "/etc/systemd/system/${UNIT_NAME}" ]]; then
  log "Unit ${UNIT_NAME} already exists — leaving it"
else
  log "Writing systemd unit ${UNIT_NAME}"
  cat > "/etc/systemd/system/${UNIT_NAME}" <<EOF
# Plain Palworld unit for the Paladin test box.
# Intentionally NOT Paladin-managed: this is the "foreign supervisor" that
# Paladin's adopt flow (DESIGN.md §7.4) must detect and take over.
[Unit]
Description=Palworld dedicated server (testbox)
After=network.target

[Service]
Type=simple
User=${SERVICE_USER}
WorkingDirectory=${INSTALL_DIR}
ExecStart=${INSTALL_DIR}/PalServer.sh
Restart=on-failure
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
fi

log "Enabling and starting the server"
systemctl enable --now "${UNIT_NAME}"

# ---- wait for REST readiness ------------------------------------------------
log "Waiting for REST API readiness (up to 120s — first boot generates the world)"
ADMIN_PW="${ADMIN_PW:-$(grep -oP 'AdminPassword: \K.*' "${CRED_FILE}" 2>/dev/null || true)}"
READY=0
for _ in $(seq 1 60); do
  if curl -sf -u "admin:${ADMIN_PW}" http://127.0.0.1:8212/v1/api/info >/dev/null 2>&1; then
    READY=1; break
  fi
  sleep 2
done

echo
echo "============================================================"
if [[ "${READY}" == "1" ]]; then
  echo " Palworld test server is UP and the REST API is answering."
else
  echo " Server started but REST didn't answer within 120s."
  echo " Check: journalctl -u ${UNIT_NAME} -e"
fi
echo "------------------------------------------------------------"
echo " Install dir : ${INSTALL_DIR}"
echo " Config      : ${CONFIG_FILE}"
echo " Credentials : ${CRED_FILE}   (admin password inside)"
echo " Unit        : systemctl status ${UNIT_NAME}"
echo " Game port   : 8211/udp   (open in firewall for players:"
echo "               sudo ufw allow 8211/udp — NOT needed for local testing)"
echo " REST API    : 127.0.0.1:8212 — keep it local; never expose it."
echo "============================================================"
