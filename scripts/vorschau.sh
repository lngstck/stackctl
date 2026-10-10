#!/usr/bin/env bash
# vorschau.sh — stackctl mit Testdaten im Browser ansehen, ohne Docker.
#
# Usage (aus dem Repo):
#   scripts/vorschau.sh [katalog-verzeichnis]    # Standard: ../catalog
#
# Legt unter dist/vorschau/ eine eingerichtete Testschule mit ein paar Apps,
# Testkonten und KI-Einstellungen an und startet stackctl im Dev-Modus auf
# http://127.0.0.1:8091 (Templates und CSS laden bei jedem Aufruf neu).
# Ein kleines Ersatz-docker meldet die Container als laufend, nur Handheld
# als aus. Jeder Start setzt die Daten zurück.
set -euo pipefail

cd "$(dirname "$0")/.."
CATALOG="${1:-../catalog}"
D="$PWD/dist/vorschau"

for tool in htpasswd openssl go; do
  command -v "$tool" >/dev/null || { echo "Fehlt: $tool" >&2; exit 1; }
done
[ -f "$CATALOG/catalog.yaml" ] || { echo "Kein Katalog unter $CATALOG (lngstck/catalog auschecken)" >&2; exit 1; }

rm -rf "$D"
mkdir -p "$D/bin" "$D/stackctl/config/catalog/containers" "$D/stackctl/backups" "$D/learningstack/llmd/config"

# Ersatz für docker: nur die Frage "läuft der Container?" bekommt eine Antwort.
cat > "$D/bin/docker" <<'SH'
#!/bin/sh
if [ "$1" = inspect ]; then
  for last; do :; done
  case "$last" in ls-handheld) echo false ;; ls-*) echo true ;; *) exit 1 ;; esac
  exit 0
fi
exit 1
SH
chmod +x "$D/bin/docker"

PW=$(openssl rand -hex 8)
echo "$PW" > "$D/ADMIN-PASSWORT.txt"
hash() { htpasswd -nbBC 10 x "$1" | cut -d: -f2; }

cat > "$D/stackctl/config/config.yaml" <<YAML
version: 4
setup_state: ready
school:
  name: Testschule Musterstadt
  slug: testschule
  server_domain: 192.168.178.20
  contact_email: it@testschule.de
catalog:
  url: https://raw.githubusercontent.com/lngstck/catalog/main
admin:
  password_hash: '$(hash "$PW")'
public:
  transport: local
  base_domain: test1.learningstack.online
auth:
  test_accounts:
    - id: 3f2a9c1d0e8b4a7f
      name: Frau Müller
      role: lehrkraft
      login: mueller@test1.learningstack.online
      password_hash: '$(hash testtest)'
    - id: 9b1c7e2d4f6a8c0e
      name: Mia S.
      role: schueler
      login: mia@test1.learningstack.online
      password_hash: '$(hash testtest)'
auto_update:
  enabled: true
YAML

cp "$CATALOG/catalog.yaml" "$D/stackctl/config/catalog/"
cp "$CATALOG"/containers/*.yaml "$D/stackctl/config/catalog/containers/"

app() { # id name version port public host
  cat >> "$D/stackctl/config/state.yaml" <<YAML
  $1:
    id: $1
    name: $2
    version_installed: "$3"
    ports: [$4]
    env_keys: []
    installed_at: "2026-10-09T18:20:00Z"
    public_enabled: $5
    public_host: "$6"
YAML
}
printf 'version: "3.0"\nports: {}\ncontainers:\n' > "$D/stackctl/config/state.yaml"
app postgres PostgreSQL 1.3 8100 false ""
app dex "Dex (OIDC)" 1.3 5556 true auth.test1.learningstack.online
app caddy "Reverse-Proxy (Caddy)" 1.1 80 false ""
app llmd LLM-Gateway 0.2.0 8320 false ""
app open-webui "Open WebUI" 1.6 8310 true chat.test1.learningstack.online
app sponsorenlauf Sponsorenlauf 0.5.0 8340 true sponsorenlauf.test1.learningstack.online
app handheld Handheld 0.1.0 8350 false ""

cat > "$D/learningstack/llmd/config/config.yaml" <<'YAML'
version: "2"
providers:
  - id: openai
    kind: openai
    base_url: https://api.openai.com
    api_key: sk-nur-vorschau
  - id: ollama
    kind: openai
    base_url: http://192.168.178.30:11434
personas:
  - id: tutor
    provider: openai
    upstream_id: gpt-4.1-mini
    prompt: Du bist ein geduldiger Tutor. Gib Hinweise statt Lösungen.
  - id: chat
    provider: openai
    upstream_id: gpt-4.1
  - id: lokal
api_keys:
  - id: open-webui
    prefix: sk-ls-3f9a
    hash: x
    allowed_personas: []
YAML

echo "Vorschau: http://127.0.0.1:8091  Admin-Passwort: dist/vorschau/ADMIN-PASSWORT.txt"
PATH="$D/bin:$PATH" STACKCTL_DIR="$D/stackctl" LEARNINGSTACK_DIR="$D/learningstack" \
  exec go run ./cmd/stackctl web --dev --host 127.0.0.1 --port 8091
