#!/usr/bin/env bash
# install.sh — One-line installer for stackctl.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/lngstck/stackctl/main/scripts/install.sh | sudo bash
#
# What it does (see ARCHITECTURE.md §11.1):
#   1. Checks OS, architecture, curl, Docker
#   2. Creates system user "learningstack" (+ docker group)
#   3. Creates /opt/stackctl and /opt/learningstack
#   4. Downloads latest stackctl binary from GitHub Releases
#   5. Installs systemd service
#   6. Starts stackctl
#
# Requires: root, Ubuntu/Debian, Docker already installed.
set -euo pipefail

GITHUB_REPO="lngstck/stackctl"
INSTALL_DIR="/opt/stackctl"
DATA_DIR="/opt/learningstack"
SERVICE_FILE="/etc/systemd/system/stackctl.service"
AUTOUPDATE_SERVICE_FILE="/etc/systemd/system/stackctl-autoupdate.service"
AUTOUPDATE_TIMER_FILE="/etc/systemd/system/stackctl-autoupdate.timer"
SYMLINK="/usr/local/bin/stackctl"
USER="learningstack"
GROUP="learningstack"

# --- Colors ---------------------------------------------------------------
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BOLD='\033[1m'
NC='\033[0m'

info()  { echo -e "${GREEN}▸${NC} $*"; }
warn()  { echo -e "${YELLOW}▸${NC} $*"; }
error() { echo -e "${RED}✗${NC} $*" >&2; }
die()   { error "$@"; exit 1; }

# --- Pre-flight checks ----------------------------------------------------
[ "$(id -u)" -eq 0 ] || die "Bitte als root ausfuehren: sudo bash install.sh"

# OS check.
if [ -f /etc/os-release ]; then
    . /etc/os-release
    case "$ID" in
        ubuntu|debian) ;;
        *) warn "Ungetestetes OS: $ID. Fortfahren auf eigene Gefahr." ;;
    esac
else
    warn "/etc/os-release nicht gefunden. Fortfahren auf eigene Gefahr."
fi

# Architecture.
ARCH="$(uname -m)"
case "$ARCH" in
    x86_64)  GOARCH="amd64" ;;
    aarch64) GOARCH="arm64" ;;
    *)       die "Nicht unterstuetzte Architektur: $ARCH (nur amd64/arm64)" ;;
esac

# Required tools.
command -v curl  >/dev/null 2>&1 || die "curl ist nicht installiert. Bitte installieren: apt install curl"
command -v docker >/dev/null 2>&1 || die "Docker ist nicht installiert. Bitte zuerst Docker installieren: https://docs.docker.com/engine/install/"

info "OS: ${ID:-unknown} | Arch: ${ARCH} (${GOARCH}) | Docker: $(docker --version | head -1)"

# --- Create user and group ------------------------------------------------
if ! id "$USER" &>/dev/null; then
    info "Erstelle System-Benutzer '$USER'..."
    useradd --system --create-home --home-dir /home/$USER --shell /usr/sbin/nologin "$USER"
    info "Benutzer '$USER' erstellt."
else
    info "Benutzer '$USER' existiert bereits."
fi

# Ensure group exists and user is in docker group.
getent group "$GROUP" >/dev/null || groupadd "$GROUP"
usermod -aG docker "$USER" 2>/dev/null || true
usermod -g "$GROUP" "$USER" 2>/dev/null || true

# --- Create directories ---------------------------------------------------
info "Erstelle Verzeichnisse..."
mkdir -p "$INSTALL_DIR"/{config,compose}
mkdir -p "$DATA_DIR"
chown -R "$USER:$GROUP" "$INSTALL_DIR"
# WICHTIG: NICHT chown -R auf $DATA_DIR. Unter /opt/learningstack/ leben
# Container-Daten mit Container-spezifischen UIDs (postgres 999, dex 1001,
# grafana 472, ...). Ein rekursiver chown auf learningstack:learningstack
# bricht Postgres-/Dex-Datenverzeichnisse bei jedem erneuten install.sh-
# Lauf — Postmaster-Forks scheitern dann mit "permission denied" auf
# global/pg_filenode.map. stackctl chownt die App-Daten beim Install via
# Throwaway-Container auf die richtigen UIDs (paths.EnsureDir + owner:
# aus der Katalog-Definition). Hier nur das Top-Level fuer stackctl
# beschreibbar machen, damit der Service neue App-Verzeichnisse anlegen
# kann.
chown "$USER:$GROUP" "$DATA_DIR"

# --- Download binary ------------------------------------------------------
info "Lade stackctl herunter..."
ASSET_NAME="stackctl-linux-${GOARCH}"
DOWNLOAD_URL="https://github.com/${GITHUB_REPO}/releases/latest/download/${ASSET_NAME}"

# Erst in eine Temp-Datei laden, dann atomar nach ${INSTALL_DIR}/stackctl
# umbenennen. Direktes Schreiben auf ein laufendes Binary scheitert auf
# Linux mit ETXTBSY (Text file busy) — curl meldet das aber nicht ueber
# HTTP_CODE, das wuerde stumm bleiben. `mv` ueber das laufende Binary ist
# safe: der Kernel haengt den neuen Inode unter den Namen, der laufende
# Prozess behaelt seinen alten Inode bis zum Restart.
TMP_BIN="${INSTALL_DIR}/stackctl.new"
rm -f "$TMP_BIN"
HTTP_CODE=$(curl -fSL -w "%{http_code}" -o "$TMP_BIN" "$DOWNLOAD_URL" 2>&1 | tail -1 || true)
if [ ! -s "$TMP_BIN" ] || [ "${HTTP_CODE}" != "200" ]; then
    rm -f "$TMP_BIN"
    die "Download fehlgeschlagen (HTTP ${HTTP_CODE}). URL: ${DOWNLOAD_URL}"
fi

chmod 755 "$TMP_BIN"
chown "$USER:$GROUP" "$TMP_BIN"

# Verify binary works BEFORE replacing the running one.
VERSION=$("$TMP_BIN" version 2>/dev/null || echo "")
if [ -z "$VERSION" ]; then
    rm -f "$TMP_BIN"
    die "Heruntergeladenes Binary ist nicht ausfuehrbar."
fi

mv -f "$TMP_BIN" "${INSTALL_DIR}/stackctl"
info "Installiert: ${VERSION}"

# Write version file.
echo "$VERSION" | sed 's/^stackctl //' > "${INSTALL_DIR}/stackctl.version"
chown "$USER:$GROUP" "${INSTALL_DIR}/stackctl.version"

# --- Symlink --------------------------------------------------------------
ln -sf "${INSTALL_DIR}/stackctl" "$SYMLINK"
info "Symlink: ${SYMLINK} -> ${INSTALL_DIR}/stackctl"

# --- systemd service ------------------------------------------------------
info "Installiere systemd-Service..."
cat > "$SERVICE_FILE" << 'UNIT'
[Unit]
Description=stackctl – learningstack control plane
After=docker.service network-online.target
Wants=network-online.target
Requires=docker.service

[Service]
Type=simple
User=learningstack
Group=learningstack
SupplementaryGroups=docker
ExecStart=/opt/stackctl/stackctl web --host 0.0.0.0 --port 8090
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
UNIT

info "Installiere systemd-Auto-Update-Timer..."
cat > "$AUTOUPDATE_SERVICE_FILE" << 'UNIT'
[Unit]
Description=stackctl nightly app auto-update
After=docker.service network-online.target stackctl.service
Wants=network-online.target
Requires=docker.service

[Service]
Type=oneshot
User=learningstack
Group=learningstack
SupplementaryGroups=docker
ExecStart=/opt/stackctl/stackctl autoupdate
UNIT

cat > "$AUTOUPDATE_TIMER_FILE" << 'UNIT'
[Unit]
Description=stackctl nightly app auto-update (timer)

[Timer]
# 03:00 nominal mit bis zu einer Stunde Jitter, damit nicht alle Schulen
# zur selben Sekunde catalog.learningstack.online + registry hammern.
OnCalendar=*-*-* 03:00:00
RandomizedDelaySec=3600
Persistent=true

[Install]
WantedBy=timers.target
UNIT

systemctl daemon-reload
systemctl enable stackctl
# restart statt start: bei einem Upgrade laeuft der Service schon, sonst
# wuerde der alte Prozess mit dem alten Inode weiterlaufen und das neu
# installierte Binary erst beim naechsten Neustart aktiv werden.
systemctl restart stackctl
# Timer ist immer aktiv; der eigentliche Auto-Update-Lauf prueft den
# globalen Schalter aus config.yaml (auto_update.enabled) und exit-ed
# ohne Aenderungen, wenn der Admin ihn nicht eingeschaltet hat.
systemctl enable --now stackctl-autoupdate.timer

info "stackctl-Service gestartet."
info "Auto-Update-Timer aktiviert (laeuft naechtlich 03:00 +/- 1h)."

# --- Detect server IP -----------------------------------------------------
SERVER_IP=$(ip -4 route get 8.8.8.8 2>/dev/null | grep -oP 'src \K[\d.]+' || hostname -I | awk '{print $1}' || echo "localhost")

# --- Done -----------------------------------------------------------------
echo ""
echo -e "${BOLD}════════════════════════════════════════════════════${NC}"
echo -e "${BOLD}  stackctl ist installiert und laeuft!${NC}"
echo ""
echo -e "  ${GREEN}▸${NC} Web-UI:  ${BOLD}http://${SERVER_IP}:8090${NC}"
echo -e "  ${GREEN}▸${NC} Status:  systemctl status stackctl"
echo -e "  ${GREEN}▸${NC} Logs:    journalctl -u stackctl -f"
echo ""
echo -e "  Oeffne die Web-UI im Browser, um das Setup zu starten."
echo -e "${BOLD}════════════════════════════════════════════════════${NC}"
