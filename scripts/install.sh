#!/usr/bin/env bash
# install.sh — One-line installer for stackctl.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/lngstck/stackctl/main/scripts/install.sh | sudo bash
#
# Eine bestimmte Version, z.B. einen Vorab-Stand, den "latest" nicht zeigt:
#   curl -fsSL .../install.sh | sudo STACKCTL_VERSION=v0.12.0-rc1 bash
#
# What it does:
#   1. Checks OS, architecture, curl, Docker
#   2. Creates system user "learningstack" (+ docker group)
#   3. Creates /opt/stackctl and /opt/learningstack
#   4. Downloads the stackctl binary from GitHub Releases and verifies it
#      against the release's SHA256SUMS
#   5. Installs systemd service
#   6. Starts stackctl and prints the setup link, including the one-time
#      setup code stackctl creates on its first start
#
# Requires: root, Ubuntu/Debian, Docker Engine with the Compose plugin
# (docker-ce + docker-compose-plugin). On a fresh Debian, curl and Docker
# are not there yet — README.md lists the commands.
set -euo pipefail

GITHUB_REPO="lngstck/stackctl"
INSTALL_DIR="/opt/stackctl"
DATA_DIR="/opt/learningstack"
SERVICE_FILE="/etc/systemd/system/stackctl.service"
AUTOUPDATE_SERVICE_FILE="/etc/systemd/system/stackctl-autoupdate.service"
AUTOUPDATE_TIMER_FILE="/etc/systemd/system/stackctl-autoupdate.timer"
SYMLINK="/usr/local/bin/stackctl"
SUDOERS_FILE="/etc/sudoers.d/stackctl"
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
command -v sha256sum >/dev/null 2>&1 || die "sha256sum fehlt (Paket coreutils)."
command -v docker >/dev/null 2>&1 || die "Docker ist nicht installiert. Bitte zuerst Docker installieren: https://docs.docker.com/engine/install/"

# stackctl ruft durchgehend 'docker compose ...' (v2-Plugin) auf. Das
# Debian/Ubuntu-Paket 'docker.io' und Snap-Docker bringen das Plugin nicht
# mit — ohne diesen Check laeuft die Installation durch und scheitert erst
# beim ersten App-Install mit einer kryptischen Meldung.
docker compose version >/dev/null 2>&1 || die "Docker-Compose-Plugin fehlt ('docker compose version' schlaegt fehl).
  stackctl braucht Docker Engine inkl. Compose-Plugin (nicht 'docker.io' oder Snap).
  Installation: https://docs.docker.com/engine/install/ (docker-ce + docker-compose-plugin)"

# 'docker --version' funktioniert auch ohne laufenden Daemon — erst
# 'docker info' beweist, dass der Daemon erreichbar ist.
docker info >/dev/null 2>&1 || die "Docker-Daemon ist nicht erreichbar ('docker info' schlaegt fehl).
  Starten: systemctl enable --now docker"

info "OS: ${ID:-unknown} | Arch: ${ARCH} (${GOARCH}) | Docker: $(docker --version | head -1) | Compose: $(docker compose version --short 2>/dev/null || echo '?')"

# Port 8090 belegt und stackctl laeuft nicht? Dann wuerde der Service nach
# der Installation im Crashloop landen. Nur Warnung, kein Abbruch — der
# Admin soll selbst entscheiden.
if command -v ss >/dev/null 2>&1 \
    && ss -tln 2>/dev/null | grep -Eq '[:.]8090[[:space:]]' \
    && ! systemctl is-active --quiet stackctl 2>/dev/null; then
    warn "Port 8090 ist bereits belegt (und stackctl laeuft nicht). Der Web-UI-Start wird vermutlich fehlschlagen."
fi

# --- Create user and group ------------------------------------------------
if ! id "$USER" &>/dev/null; then
    info "Erstelle System-Benutzer '$USER'..."
    useradd --system --create-home --home-dir /home/$USER --shell /usr/sbin/nologin "$USER"
    info "Benutzer '$USER' erstellt."
else
    info "Benutzer '$USER' existiert bereits."
fi

# Ensure group exists and user is in docker group. Ohne Gruppe 'docker'
# startet der Dienst gar nicht (SupplementaryGroups=docker in der Unit) —
# das soll hier auffallen, nicht erst im Journal.
getent group docker >/dev/null || die "Gruppe 'docker' fehlt. stackctl braucht Docker Engine (docker-ce), kein rootless Docker."
getent group "$GROUP" >/dev/null || groupadd "$GROUP"
usermod -aG docker "$USER"
usermod -g "$GROUP" "$USER"

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
# Ohne STACKCTL_VERSION die neueste Veroeffentlichung. Vorab-Staende
# (Pre-Releases) zaehlen fuer GitHub nicht als "latest" und lassen sich nur
# gezielt installieren.
if [ -n "${STACKCTL_VERSION:-}" ]; then
    RELEASE_URL="https://github.com/${GITHUB_REPO}/releases/download/${STACKCTL_VERSION}"
    info "Lade stackctl ${STACKCTL_VERSION} herunter..."
else
    RELEASE_URL="https://github.com/${GITHUB_REPO}/releases/latest/download"
    info "Lade die neueste stackctl-Version herunter..."
fi
ASSET_NAME="stackctl-linux-${GOARCH}"

# Erst in eine Temp-Datei laden, dann atomar nach ${INSTALL_DIR}/stackctl
# umbenennen. Direktes Schreiben auf ein laufendes Binary scheitert auf
# Linux mit ETXTBSY (Text file busy). `mv` ueber das laufende Binary ist
# safe: der Kernel haengt den neuen Inode unter den Namen, der laufende
# Prozess behaelt seinen alten Inode bis zum Restart.
TMP_BIN="${INSTALL_DIR}/stackctl.new"
TMP_SUMS="${INSTALL_DIR}/SHA256SUMS.new"
rm -f "$TMP_BIN" "$TMP_SUMS"
if ! curl -fsSL --retry 3 -o "$TMP_BIN" "${RELEASE_URL}/${ASSET_NAME}"; then
    rm -f "$TMP_BIN"
    die "Download fehlgeschlagen: ${RELEASE_URL}/${ASSET_NAME}"
fi

# Pruefsumme wie beim Self-Update (internal/update): ohne SHA256SUMS oder
# bei Abweichung wird nichts installiert. Die TLS-Verbindung zu GitHub ist
# damit nicht die einzige Vertrauenskette.
if ! curl -fsSL --retry 3 -o "$TMP_SUMS" "${RELEASE_URL}/SHA256SUMS"; then
    rm -f "$TMP_BIN" "$TMP_SUMS"
    die "Pruefsummen-Datei SHA256SUMS fehlt im Release — Abbruch aus Sicherheitsgruenden."
fi
EXPECTED_SUM=$(awk -v f="$ASSET_NAME" '$2 == f || $2 == "*"f { print $1 }' "$TMP_SUMS")
ACTUAL_SUM=$(sha256sum "$TMP_BIN" | awk '{ print $1 }')
rm -f "$TMP_SUMS"
if [ -z "$EXPECTED_SUM" ] || [ "$EXPECTED_SUM" != "$ACTUAL_SUM" ]; then
    rm -f "$TMP_BIN"
    die "Pruefsumme stimmt nicht (erwartet ${EXPECTED_SUM:-?}, erhalten ${ACTUAL_SUM}) — nichts installiert."
fi
info "Pruefsumme stimmt."

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
# WICHTIG: Diese Unit-Datei MUSS mit systemd/stackctl.service im Repo
# synchron bleiben. Restart=always + RestartSec=1 ist Voraussetzung fuer
# den Self-Update-Mechanismus aus Issue #10 (Prozess macht os.Exit(0),
# systemd bringt ihn mit dem neuen Binary wieder hoch). Vorgaenger-
# Strategie war Restart=on-failure + sudo systemctl restart — das wuerde
# auf Exit 0 NICHT neu starten, der Service bliebe tot.
# StartLimitBurst gehoert auf Unit-Ebene (nicht in [Service]).
cat > "$SERVICE_FILE" << 'UNIT'
[Unit]
Description=stackctl – learningstack control plane
After=docker.service network-online.target
Wants=network-online.target
Requires=docker.service
StartLimitBurst=3
StartLimitIntervalSec=60

[Service]
Type=simple
User=learningstack
Group=learningstack
SupplementaryGroups=docker
ExecStart=/opt/stackctl/stackctl web --host 0.0.0.0 --port 8090
Restart=always
RestartSec=1

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
# zur selben Sekunde den Katalog (GitHub) und ghcr.io abfragen.
OnCalendar=*-*-* 03:00:00
RandomizedDelaySec=3600
Persistent=true

[Install]
WantedBy=timers.target
UNIT

# --- sudoers snippet removal ---------------------------------------------
# Frueher (Issue #6) brauchte Self-Update ein NOPASSWD-sudo fuer
# `systemctl restart stackctl`. Seit Issue #10 ist der Mechanismus auf
# kontrollierten Self-Exit + Restart=always umgestellt — der Snippet ist
# obsolet. Auf Re-Install bestehender Hosts wird er hier entfernt.
if [ -f "$SUDOERS_FILE" ]; then
    info "Entferne obsoletes sudoers-Snippet (Self-Update braucht es nicht mehr)..."
    rm -f "$SUDOERS_FILE"
fi

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

# --- Setup link -----------------------------------------------------------
# stackctl legt den Einrichtungscode beim ersten Start an. Ohne ihn laesst
# sich die Einrichtung nicht abschliessen — sonst koennte jede Person im
# Netz, die zuerst auf Port 8090 kommt, das Admin-Passwort setzen.
SETUP_CODE_FILE="${INSTALL_DIR}/config/setup-code"
SETUP_DONE=false
grep -q '^setup_state: ready' "${INSTALL_DIR}/config/config.yaml" 2>/dev/null && SETUP_DONE=true
SETUP_LINK=""
if ! $SETUP_DONE; then
    for _ in $(seq 1 20); do
        [ -s "$SETUP_CODE_FILE" ] && break
        sleep 1
    done
    if [ -s "$SETUP_CODE_FILE" ]; then
        SETUP_LINK="http://${SERVER_IP}:8090/setup?code=$(tr -d '[:space:]' < "$SETUP_CODE_FILE")"
    fi
fi

# Laeuft der Dienst gar nicht, nuetzt der Link nichts. Dann lieber gleich
# zeigen, woran es liegt, als die Admin ins Leere zu schicken.
sleep 2
if ! systemctl is-active --quiet stackctl; then
    error "stackctl laeuft nicht. Die letzten Zeilen aus dem Journal:"
    journalctl -u stackctl -n 20 --no-pager >&2 || true
    die "Installation abgebrochen. Nach einer Korrektur install.sh einfach erneut ausfuehren."
fi

# --- Done -----------------------------------------------------------------
echo ""
echo -e "${BOLD}════════════════════════════════════════════════════${NC}"
echo -e "${BOLD}  stackctl ist installiert und laeuft!${NC}"
echo ""
if [ -n "$SETUP_LINK" ]; then
    echo -e "  Einrichtung im Browser starten:"
    echo -e "  ${GREEN}▸${NC} ${BOLD}${SETUP_LINK}${NC}"
    echo ""
    echo -e "  Der Link enthaelt den Einrichtungscode. Nur an die Person"
    echo -e "  weitergeben, die einrichtet. Neu anzeigen: sudo stackctl setup-code"
elif $SETUP_DONE; then
    echo -e "  ${GREEN}▸${NC} Web-UI:  ${BOLD}http://${SERVER_IP}:8090${NC}"
else
    warn "Der Einrichtungscode ist noch nicht da. Gleich noch einmal versuchen:"
    echo -e "    sudo stackctl setup-code"
    echo -e "  Einrichtung dann unter ${BOLD}http://${SERVER_IP}:8090/setup${NC}"
fi
echo ""
echo -e "  ${GREEN}▸${NC} Status:  systemctl status stackctl"
echo -e "  ${GREEN}▸${NC} Logs:    journalctl -u stackctl -f"
echo -e "${BOLD}════════════════════════════════════════════════════${NC}"
