#!/usr/bin/env bash
# uninstall.sh — Removes stackctl (keeps data by default).
#
# Usage:
#   sudo bash uninstall.sh [--purge]
#
# --purge: also removes /opt/learningstack/ (container data).
# Without --purge, container data is preserved for re-installation.
set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BOLD='\033[1m'
NC='\033[0m'

info()  { echo -e "${GREEN}▸${NC} $*"; }
warn()  { echo -e "${YELLOW}▸${NC} $*"; }
die()   { echo -e "${RED}✗${NC} $*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "Bitte als root ausfuehren: sudo bash uninstall.sh"

PURGE=false
[ "${1:-}" = "--purge" ] && PURGE=true

# Stop and disable service.
if systemctl is-active stackctl &>/dev/null; then
    info "Stoppe stackctl..."
    systemctl stop stackctl
fi
if systemctl is-enabled stackctl &>/dev/null; then
    systemctl disable stackctl
fi

# Stop containers.
if [ -f /opt/stackctl/compose/docker-compose.yml ]; then
    info "Stoppe Container..."
    docker compose -f /opt/stackctl/compose/docker-compose.yml down 2>/dev/null || true
fi

# Remove service file.
rm -f /etc/systemd/system/stackctl.service
systemctl daemon-reload 2>/dev/null || true

# Remove symlink.
rm -f /usr/local/bin/stackctl

# Remove program directory.
info "Entferne /opt/stackctl/..."
rm -rf /opt/stackctl

# Purge data if requested.
if $PURGE; then
    warn "Entferne Container-Daten /opt/learningstack/..."
    rm -rf /opt/learningstack
else
    info "Container-Daten bleiben erhalten unter /opt/learningstack/"
    info "Zum vollstaendigen Entfernen: sudo bash uninstall.sh --purge"
fi

echo ""
echo -e "${BOLD}stackctl wurde deinstalliert.${NC}"
