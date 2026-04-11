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
chown -R "$USER:$GROUP" "$DATA_DIR"

# --- Download binary ------------------------------------------------------
info "Lade stackctl herunter..."
ASSET_NAME="stackctl-linux-${GOARCH}"
DOWNLOAD_URL="https://github.com/${GITHUB_REPO}/releases/latest/download/${ASSET_NAME}"

HTTP_CODE=$(curl -fsSL -w "%{http_code}" -o "${INSTALL_DIR}/stackctl" "$DOWNLOAD_URL" 2>/dev/null || true)
if [ ! -f "${INSTALL_DIR}/stackctl" ] || [ "${HTTP_CODE}" != "200" ]; then
    die "Download fehlgeschlagen (HTTP ${HTTP_CODE}). URL: ${DOWNLOAD_URL}"
fi

chmod 755 "${INSTALL_DIR}/stackctl"
chown "$USER:$GROUP" "${INSTALL_DIR}/stackctl"

# Verify binary works.
VERSION=$("${INSTALL_DIR}/stackctl" version 2>/dev/null || echo "")
if [ -z "$VERSION" ]; then
    die "Heruntergeladenes Binary ist nicht ausfuehrbar."
fi
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

systemctl daemon-reload
systemctl enable stackctl
systemctl start stackctl

info "stackctl-Service gestartet."

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
