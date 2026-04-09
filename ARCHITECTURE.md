# stackctl — Architektur (v2, Neubau)

> Status: **Entwurf, noch kein Code.** Dieses Dokument beschreibt, was stackctl ist, was es tut, wie es gebaut wird und wo was lebt. Alles, was hier steht, soll vor dem ersten `go build` diskutiert und freigegeben sein.

---

## 1. Vision in zwei Sätzen

stackctl ist das einzige Tool, das ein Schul-Admin auf einem frisch aufgesetzten Linux-Server installiert, um darauf Docker-basierte Anwendungen für den Unterricht zu betreiben. Es ersetzt "Ich muss wissen, was Docker, OIDC und docker-compose sind" durch eine Web-Oberfläche mit verständlichen Knöpfen.

## 2. Zielnutzer

**Der Schul-Admin.** Typisches Profil:
- hat Linux schonmal benutzt, aber kein tägliches Werkzeug
- kennt Docker dem Namen nach, hat selten selbst ein Compose-File geschrieben
- versteht OIDC nicht, will es auch nicht verstehen müssen
- hat Angst, Daten kaputtzumachen
- liest Fehlermeldungen sorgfältig, aber nur wenn sie in Klartext sind

**Design-Konsequenz:** Die Web-UI ist die Haupt-Schnittstelle. Die CLI ist Rettungswerkzeug. Jede sichtbare Aktion muss reversibel oder eindeutig als "Achtung, das geht ins Internet" markiert sein. Defaults sind immer lokal und sicher.

## 3. Was stackctl **nicht** ist

- Kein User-Portal. Der Endnutzer (Lehrkraft, Schüler:in) loggt sich **nicht** bei stackctl ein. Ein späterer, separater Container übernimmt das User-Dashboard auf Port 80.
- Kein OIDC-Provider. Das ist Dex. stackctl konfiguriert Dex nur.
- Kein Tunnel-Server. Das ist sish auf `learningstack.online`. stackctl startet nur `autossh`-Clients.
- Kein Container-Registry-Hoster. Das ist `registry.learningstack.online`.
- Kein Multi-Server-Orchestrator. Ein stackctl gehört zu genau einem Server.

## 4. System-Kontext

learningstack verteilt seine zentralen Aufgaben **bewusst auf zwei getrennte Server**. Siehe §4.1 für die Sicherheitsbegründung.

```
                   ┌─────────────────────────────────────────────┐
                   │ GitHub (lngstck org)                        │
                   │   lngstck/stackctl   → Code + Releases      │
                   │   lngstck/catalog    → Container-Defs       │
                   └───────────┬─────────────────────────────────┘
                               │ curl install.sh; stackctl self-update
                               ▼
┌───────────────────────────────────────────────────────────────────┐
│  Schulserver (Ubuntu/Debian, ein Server pro Schule)               │
│                                                                   │
│  ┌─────────────────────┐   manages   ┌─────────────────────────┐  │
│  │  stackctl (Go)      │ ──────────► │  Docker-Container       │  │
│  │  systemd service    │             │  ls-postgres            │  │
│  │  Web-UI :8090       │             │  ls-dex   ──────────┐   │  │
│  │  CLI /usr/local/bin │             │  ls-langflow ...    │   │  │
│  └─────────┬───────────┘             └─────────────────────┼───┘  │
│            │                                               │      │
│            │ autossh (immer für Dex; optional pro App)     │      │
└────────────┼───────────────────────────────────────────────┼──────┘
             │                                               │
             │ Reverse-SSH-Tunnel                            │ OIDC-Upstream
             ▼                                               │ (HTTPS, direkt)
┌────────────────────────────────────────┐    ┌─────────────▼──────────────────┐
│  System 1 — learningstack.online       │    │  System 2 — auth.learningstack │
│  (Netzwerk-Vermittler)                 │    │  .online (Identity-System)     │
│                                        │    │                                │
│  sish      → SSH-Reverse-Tunnel-Router │    │  zentraler Dex                 │
│  nginx     → TLS-Terminierung, vhosts  │    │  (tools/central-dex/)          │
│  acme.sh   → Wildcard-Certs (IONOS)    │    │  - storage: memory (kein PII)  │
│  catalog   → statisches nginx          │    │  - moin.schule Upstream        │
│  registry  → Docker Registry v2        │    │  - staticClients via 'schulen' │
│                                        │    │                                │
│  tools/register-tunnel (Operator)      │    │  schulen (Operator-CLI)        │
└────────────────────────────────────────┘    └────────────────────────────────┘
```

**Kommunikation zwischen System 1 und System 2:** keine direkte. Der Schul-Dex spricht System 2 **direkt** über das Internet an (HTTPS zu `auth.learningstack.online`), nicht über sish. Der einzige Traffic-Fluss durch sish ist Browser-→-Schule.

### 4.1 Warum zwei Server

- **Sicherheits-Kompartmentierung:** System 2 hält die "Identity-Kronjuwelen" (Dex-Signing-Key, Upstream-Client-Secret zu moin.schule, alle Schul-Client-Secrets). System 1 ist ein öffentlich exponierter SSH-/HTTP-Router mit großer Angriffsfläche. Ein Compromise von System 1 darf nicht automatisch System 2 gefährden.
- **Angriffsflächen-Asymmetrie:** sish nimmt beliebige SSH-Verbindungen vom Internet an, Dex antwortet nur auf wenige, wohldefinierte OIDC-Endpoints. Die Zero-Day-Wahrscheinlichkeit ist bei sish höher.
- **Compliance:** BSI-Grundschutz und typische Datenschutz-Prüfungen erwarten getrennte Schutzklassen für "Authentifizierung" und "Netzwerk-Vermittlung".
- **Operationaler Overhead:** vernachlässigbar — zwei kleine VMs statt einer, unabhängige Update-Zyklen.

### 4.2 Betreiber-Workflow für Neu-Registrierungen

Wenn eine neue Schule stackctl installieren will, landet bei dir (Betreiber) eine age-verschlüsselte E-Mail (siehe §11.0). Du entschlüsselst, führst **zwei** Skripte auf **zwei** Systemen aus:

**Auf System 1 (`learningstack.online`):**
```bash
./tools/register-tunnel phoenix phoenix.pub
# Macht: sish-Key, acme.sh Wildcard-Cert *.phoenix.learningstack.online,
#        nginx-vhost aus Template, systemctl reload nginx
```

**Auf System 2 (`auth.learningstack.online`):**
```bash
./schulen add phoenix \
    --client-id phoenix \
    --client-secret <aus-dem-Paket> \
    --redirect-uri https://auth.phoenix.learningstack.online/callback
# Das `schulen`-Tool akzeptiert neu die drei Flags (--client-id,
# --client-secret, --redirect-uri). Ohne Flags würfelt es wie bisher selbst.
```

Beide Skripte sind idempotent. Einmal gelaufen, ist die Schule freigeschaltet.

**Phase 2 (später):** ein kleines Meta-Script `registriere-schule`, das per SSH auf beide Server zugreift und beides in einem Schritt erledigt. Für Phase 1 manuell — du willst ja explizit hinschauen, wer reinkommt.

## 5. Tech-Stack

- **Sprache:** Go (mindestens 1.22)
- **Web:** `net/http` (stdlib), `html/template` (stdlib), `//go:embed` für Templates und Assets
- **YAML:** `gopkg.in/yaml.v3`
- **Passwort-Hashing:** `golang.org/x/crypto/bcrypt` (dieselbe Library, die auch Dex nutzt)
- **Verschlüsselung (Registrierungs-Paket):** `filippo.io/age` — modernes X25519+ChaCha20-Poly1305, ASCII-armored, Multi-Recipient-fähig
- **Sonst:** stdlib
- **Keine** Web-Frameworks, ORMs, DI-Container, Asset-Pipelines
- **CSS/JS:** [oat.ink](https://oat.ink) – `oat.min.css` + `oat.min.js` werden ins Binary eingebettet
- **Persistenz:** YAML-Dateien für Config und State. Keine interne Datenbank in stackctl selbst.

Gesamte externe Dep-Liste: **drei** Go-Module (`yaml.v3`, `crypto/bcrypt`, `age`).
**Lizenz:** AGPL-3.0 (siehe §19).

## 6. Repo-Layout (Source Tree)

```
stackctl/                        (= dieses Verzeichnis, wird Repo lngstck/stackctl)
├── ARCHITECTURE.md              ← dieses Dokument
├── README.md                    ← Nutzersicht: Installation + erste Schritte
├── LICENSE
├── Makefile                     ← build, dev, deploy-devbox, release
├── go.mod
├── go.sum
│
├── cmd/
│   └── stackctl/
│       └── main.go              ← CLI-Entrypoint, argparse, Dispatch
│
├── internal/                    ← gesamte Business-Logik
│   ├── paths/                   ← STACKCTL_DIR, LEARNINGSTACK_DIR, Pfad-Helfer
│   ├── config/                  ← config.yaml + state.yaml Laden/Speichern
│   ├── envfile/                 ← .env Parser/Writer (mit Sektionen)
│   ├── secrets/                 ← Passwörter, Secrets, bcrypt-Wrapper
│   ├── catalog/                 ← Catalog-Sync, App-Definitionen, Versionscheck
│   ├── docker/                  ← dünner Wrapper um `docker` CLI
│   ├── compose/                 ← docker-compose.yml Generator
│   ├── dex/                     ← dex-config.yaml schreiben, OIDC-Client-Verwaltung
│   ├── postgres/                ← DB + User anlegen für Apps
│   ├── tunnel/                  ← autossh-Prozesse, SSH-Key, Monitoring
│   ├── update/                  ← Self-Update via GitHub Releases API
│   ├── install/                 ← App-Install-Flow (orchestriert alle obigen)
│   └── web/                     ← HTTP-Server
│       ├── server.go            ← Routing, Middleware (Auth, RateLimit)
│       ├── sessions.go          ← In-Memory Session-Store
│       ├── handlers_*.go        ← pro Seitengruppe eine Datei
│       ├── templates/           ← *.html.tmpl, via go:embed
│       │   ├── layout.html.tmpl
│       │   ├── setup.html.tmpl
│       │   ├── login.html.tmpl
│       │   ├── dashboard.html.tmpl
│       │   ├── apps.html.tmpl
│       │   ├── app_install.html.tmpl
│       │   ├── settings.html.tmpl
│       │   ├── tunnel.html.tmpl
│       │   └── system.html.tmpl
│       └── static/              ← via go:embed
│           ├── oat.min.css
│           ├── oat.min.js
│           ├── stackctl.css     ← eigene Ergänzungen
│           └── stackctl.js
│
├── scripts/
│   ├── install.sh               ← Ein-Klick-Installer (curl | sudo bash)
│   └── uninstall.sh
│
├── systemd/
│   └── stackctl.service         ← systemd-Unit-Template
│
└── docs/
    ├── catalog-spec.md          ← Schema der Container-Definitionen (TBD)
    ├── dex-integration.md       ← wie stackctl Dex konfiguriert
    └── dev.md                   ← Entwicklungs-Workflow (Mac ↔ Linux)
```

**Go-Modul-Pfad:** `github.com/lngstck/stackctl`

## 7. Runtime-Layout (auf dem Zielserver)

Zwei Dateibäume, bewusst getrennt: **Programm** vs. **Daten**.

```
/opt/stackctl/                       ← alles, was stackctl gehört
├── stackctl                         ← das Go-Binary (ersetzt beim Self-Update)
├── stackctl.version                 ← aktuelle Version als Klartext
├── config/
│   ├── config.yaml                  ← Schuleinstellungen, Admin-Passwort-Hash
│   ├── state.yaml                   ← installierte Apps, Ports, Tunnel-Status
│   ├── dex-config.yaml              ← von stackctl generiert, von ls-dex gelesen
│   ├── tunnel_key                   ← ed25519 (600, nur learningstack-User)
│   ├── tunnel_key.pub
│   └── catalog/                     ← gecachter Katalog
│       ├── catalog.yaml
│       └── containers/
│           ├── postgres.yaml
│           ├── dex.yaml
│           └── langflow.yaml
├── compose/
│   ├── docker-compose.yml           ← generiert; "do not edit"
│   └── .env                         ← 640, learningstack:docker
└── logs/                            ← optional, falls systemd journal nicht reicht

/opt/learningstack/                  ← alles, was Container gehört
├── postgres/data/
├── dex/data/
├── langflow/data/
└── open-webui/data/

/usr/local/bin/stackctl              ← symlink → /opt/stackctl/stackctl
/etc/systemd/system/stackctl.service ← systemd-Unit
```

**Besitzer:**
- System-User: `learningstack` (angelegt durch `install.sh`)
- Gruppe: `learningstack` + Mitglied in `docker`
- `/opt/stackctl/` gehört `learningstack:learningstack`
- `/opt/learningstack/` gehört `learningstack:learningstack` (root darf natürlich auch reinschauen)
- systemd-Service läuft als `User=learningstack`, `Group=learningstack`, `SupplementaryGroups=docker`

**Entwicklung:** `STACKCTL_DIR` und `LEARNINGSTACK_DIR` als Env-Vars umbiegbar, z.B. auf `~/dev-stackctl/` auf der Linux-Devbox.

## 8. systemd-Service

```ini
# /etc/systemd/system/stackctl.service
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
# Self-update: stackctl darf sich selbst stoppen/starten via systemctl
AmbientCapabilities=
# Kein NoNewPrivileges weil docker-cli das manchmal braucht

[Install]
WantedBy=multi-user.target
```

Web-UI erreichbar unter `http://{server-ip}:8090`. Port 80 bleibt frei für späteres User-Portal.

## 9. CLI-Kommandos

Minimaler Satz. Alles was über Web-UI geht, ist in der CLI **nicht** verfügbar.

| Befehl | Beschreibung |
|---|---|
| `stackctl version` | Versionsinfo |
| `stackctl web [--host H] [--port P] [--dev]` | Web-UI starten (normalerweise via systemd) |
| `stackctl setup` | Interaktiver Erst-Setup (wird vom installer aufgerufen, kann auch manuell) |
| `stackctl status` | Kurzübersicht für die Shell |
| `stackctl reset-password` | Admin-Passwort neu setzen (Recovery) |
| `stackctl repair` | Konfiguration prüfen und docker-compose.yml neu generieren |
| `stackctl self-update [--check]` | Neue Version von GitHub ziehen und installieren |

`--dev` bei `web`: lädt Templates und Assets vom Dateisystem statt aus dem Embed-FS, für Live-Reload während UI-Iteration.

## 10. Web-UI – Seiten und Routen

**Auth-Modell:** Eine Admin-Session (In-Memory-Token in Cookie `stackctl_session`). Rate-Limit bei Login (5 Fehlversuche pro IP, 60s Lockout). Keine User, keine Rollen — stackctl hat exakt einen Admin.

**Setup-Lifecycle:** stackctl hat drei Zustände, die in `config.yaml` als `setup_state` persistiert werden. Routen sind entsprechend gegated:

- `needs_setup` — Werksfrisch. Alle Nicht-Setup-Routen leiten auf `/setup` um.
- `awaiting_registration` — Setup-Wizard ausgefüllt, Registrierungs-Paket erzeugt. Alle Nicht-Registrierungs-Routen leiten auf `/setup/register` um.
- `ready` — Vom Betreiber freigeschaltet, normaler Betrieb. Setup-/Register-Routen leiten auf `/` um.

| Methode | Pfad | Zustand | Auth | Zweck |
|---|---|---|---|---|
| GET | `/setup` | needs_setup | – | Setup-Wizard |
| POST | `/setup` | needs_setup | – | Wizard speichern, erzeugt Registrierungs-Paket, wechselt zu `awaiting_registration` |
| GET | `/setup/register` | awaiting_registration | – | Zeigt age-Paket + mailto-Link + Poll-Status |
| GET | `/setup/register/download` | awaiting_registration | – | Lädt `.age`-Datei herunter |
| GET | `/setup/status` | awaiting_registration | – | JSON-Poll: prüft, ob zentrale Registrierung durch ist; wechselt bei Erfolg zu `ready` |
| POST | `/setup/register/retry` | awaiting_registration | – | Prüft manuell und erzeugt Paket ggf. neu |
| GET | `/login` | ready | – | Login-Seite |
| POST | `/login` | ready | – | Passwort prüfen, Session setzen |
| GET | `/logout` | ready | yes | Session löschen |
| GET | `/` | ready | yes | Dashboard: App-Karten, Status, Update-Hinweise |
| GET | `/apps` | ready | yes | Katalog mit Tabs: Alle / Installiert / Verfügbar |
| GET | `/apps/{id}` | ready | yes | App-Detail: Status, Logs-Link, Env-Werte, Entfernen |
| GET | `/apps/{id}/install` | ready | yes | Install-Formular (Prompts aus Definition) |
| POST | `/apps/{id}/install` | ready | yes | Installieren |
| POST | `/apps/{id}/remove` | ready | yes | Entfernen (Daten bleiben) |
| POST | `/apps/{id}/start` | ready | yes | Container starten |
| POST | `/apps/{id}/stop` | ready | yes | Container stoppen |
| POST | `/apps/{id}/update` | ready | yes | Container-Image aktualisieren |
| POST | `/apps/{id}/tunnel/enable` | ready | yes | App-Tunnel aktivieren |
| POST | `/apps/{id}/tunnel/disable` | ready | yes | App-Tunnel deaktivieren |
| GET | `/settings` | ready | yes | Allgemeine Einstellungen (Schulname, LLM-Keys, global_env) |
| POST | `/settings` | ready | yes | Einstellungen speichern |
| POST | `/settings/password` | ready | yes | Admin-Passwort ändern (altes + neues) |
| GET | `/tunnel` | ready | yes | Tunnel-Übersicht: SSH-Key, Verbindungstest, App-Liste (Dex-Tunnel ist hier nur Status, kein Toggle) |
| POST | `/tunnel/test` | ready | yes | SSH-Handshake zu sish testen |
| GET | `/system` | ready | yes | stackctl-Version, Self-Update, Logs, Repair |
| POST | `/system/update` | ready | yes | Self-Update auslösen |
| POST | `/system/catalog/sync` | ready | yes | Katalog neu laden |
| GET | `/static/*` | jeder | – | Statische Assets (aus embed, oder `--dev` vom FS) |
| GET | `/healthz` | jeder | – | Liveness für systemd/monitoring |

**Entfernte Routen gegenüber v1:**
- Kein `/settings/dex` — der Dex-Upstream-Typ ist in Phase 1 fest: zentraler Dex → moin.schule. Kein Switching zur Laufzeit.
- Kein `/tunnel/dex/enable|disable` — der Dex-Tunnel läuft **immer**, unbedingt, automatisch. Kein Admin-Schalter.
- Kein `/tunnel/keygen` — der SSH-Key wird beim Setup einmalig erzeugt und ist Teil des Registrierungs-Pakets. Wird er verloren, ist das ein Recovery-Fall via CLI.

**Kein `/portal`, kein OIDC-Callback für stackctl selbst.** Die stackctl-UI ist admin-only mit Passwort, ohne Dex-Kopplung (sonst hat man ein Henne/Ei-Problem: Dex muss laufen, damit stackctl funktioniert, aber stackctl verwaltet Dex).

## 10.1 Visuelle Richtung

Sidebar-Layout mit oat, an die Landing Page angelehnt:

- Linke Sidebar: **Dashboard · Apps · Einstellungen · Tunnel · System**
- Topnav: Wordmark "learningstack" links, Schulname + Admin-Dropdown rechts
- Zentrale Farbe: oat-Primary (wie Landing)
- Jede App-Karte zeigt: Name, Status-Dot (grün/gelb/rot/grau), Kurzbeschreibung, Action-Buttons
- "Gefährliche" Aktionen (Tunnel aktivieren, Entfernen) haben Confirm-Dialoge

Wir iterieren die UI gemeinsam im Browser, sobald das Skelett steht.

## 11. Haupt-Workflows

### 11.0 Registrierungs-Flow (Operator-Sicht)

Eine neue Schule installiert stackctl. Weil der lokale Dex sofort mit dem zentralen Dex sprechen muss und ein Wildcard-Tunnel für `*.{slug}.learningstack.online` aktiv sein soll, ist einmalig ein kleiner Handshake zum Betreiber nötig. Ziel: Admin macht keine Copy-Paste-Fehler, keine Credentials werden im Klartext durch unsichere Kanäle geschleppt, alles ist ein Einbahn-Paket.

**Was der Schul-Admin sieht:**

1. Füllt den Setup-Wizard aus (Schulname, Slug, Server-IP, Admin-Passwort, Kontakt-Email).
2. stackctl erzeugt **alles selbst**:
   - ed25519 SSH-Key für sish-Tunnel (`tunnel_key`, `tunnel_key.pub`)
   - Client-ID (= Slug) und Client-Secret für den zentralen Dex (40 hex chars)
   - Admin-Passwort-Hash (bcrypt)
   - interne Secrets für postgres etc.
3. stackctl packt ein **Registrierungs-Paket** (`registration-{slug}.age`) mit:
   ```yaml
   slug: phoenix
   school_name: "Gymnasium Phoenix"
   contact_email: "admin@gym-phoenix.de"
   created_at: "2026-04-09T12:34:56Z"
   stackctl_version: "v0.1.0"
   server_domain: "93.184.216.34"
   ssh_public_key: "ssh-ed25519 AAAA... stackctl@phoenix"
   dex_client_id: "phoenix"
   dex_client_secret: "a1b2c3d4e5..."
   dex_redirect_uri: "https://auth.phoenix.learningstack.online/callback"
   ```
   Das Paket wird mit dem **öffentlichen age-Key des Betreibers** verschlüsselt (hardgecodet im stackctl-Binary, pro Release austauschbar).
4. stackctl wechselt in `setup_state = awaiting_registration` und zeigt auf `/setup/register`:
   - Einen **mailto:**-Link mit Empfänger `registrierung@learningstack.online`, Betreff `stackctl registrierung {slug}`, leerem Body und dem Paket als Anhang — oder alternativ zum Download (wenn das Mailclient-Attachment nicht klappt).
   - Einen klaren Text: "Schick diese Datei an registrierung@learningstack.online. Wir richten deinen Tunnel und den OIDC-Zugang ein und melden uns zurück."
   - Einen Poll-Indikator, der alle 30s `/setup/status` abfragt.
5. Sobald der zentrale Dex die Schule kennt **und** der sish-Tunnel freigeschaltet ist, wechselt stackctl auf `ready`, installiert die Pflicht-Container (postgres + dex), startet den Dex-Tunnel permanent, und leitet auf `/login`.

**Was der Betreiber macht:** siehe §4.2. Zwei Skripte auf zwei Servern, beide idempotent. Danach kann der Admin sich einloggen.

**Warum age-verschlüsselt per E-Mail?** Einbahn-Transport, Signatur durch die Empfänger-Only-Entschlüsselung, keine zusätzliche Infrastruktur (kein Signal-Bot, kein Webhook), stabil gegen Mailclient-Fehler. Das Paket enthält nichts Geheimes, was der Admin selbst nicht ohnehin kennt — aber es ist trotzdem verschlüsselt, weil die Ausgangs-Mailbox potentiell schwach ist.

**Wie stackctl Poll-Erfolg erkennt:**
- Dex-Tunnel-Test: `curl -I https://auth.{slug}.learningstack.online/.well-known/openid-configuration` → 200 = Tunnel steht, DNS/TLS/nginx/sish alle bereit.
- OIDC-Client-Test: discovery-URL wird geladen; clientcredentials zum zentralen Dex (`auth.learningstack.online/token`) erzeugen keinen `invalid_client` → Client ist eingetragen.
- Erst wenn **beides** durchgeht, wechselt der State. Vorher bleibt der Admin auf `/setup/register` mit freundlichem "Wir warten noch auf die Freischaltung" und einem "Was wird gerade geprüft?"-Dropdown für Troubleshooting.

**Entwickler-Abkürzung:** CLI-Flag `stackctl setup --skip-registration` schreibt das Paket in `/tmp/registration-{slug}.age`, wechselt aber **nicht** den State. Für die Devbox nimmt man stattdessen `STACKCTL_SKIP_REGISTRATION=1` + gefakten `catalog.url` + vordefinierte Secrets.

### 11.1 Erstinstallation (Admin-Sicht)

```
Admin auf frischem Ubuntu:
  curl -fsSL https://raw.githubusercontent.com/lngstck/stackctl/main/scripts/install.sh | sudo bash

install.sh:
  1. Prüft: OS (Ubuntu/Debian), Architektur (amd64/arm64), curl, docker
  2. Legt Benutzer `learningstack` an (falls fehlt), Gruppe `learningstack`,
     fügt zu `docker` hinzu
  3. mkdir /opt/stackctl /opt/learningstack, chown learningstack:learningstack
  4. Lädt neueste Release von GitHub:
       https://github.com/lngstck/stackctl/releases/latest/download/stackctl-linux-{arch}
  5. Schreibt /opt/stackctl/stackctl, chmod +x
  6. Symlinkt /usr/local/bin/stackctl → /opt/stackctl/stackctl
  7. Schreibt /etc/systemd/system/stackctl.service
  8. systemctl daemon-reload && systemctl enable --now stackctl
  9. Gibt aus: "stackctl läuft unter http://<server-ip>:8090 – öffne diese URL im Browser"

Admin im Browser (http://server-ip:8090), setup_state = needs_setup:
  → /setup (Wizard, eine Seite)
      1. Schulname + Slug (auto, editierbar, Validierung a-z0-9-, 3..30 Zeichen)
      2. Kontakt-E-Mail (für Rückfragen)
      3. Server-Domain/IP (auto-erkannt, editierbar)
      4. Admin-Passwort (2x)
      5. LLM-Defaults (optional, kann später in Einstellungen)
  → POST /setup:
      - bcrypt-Hash schreiben
      - ed25519-Keypair erzeugen
      - Dex-Client-Secret würfeln
      - Registrierungs-Paket packen, mit Betreiber-Pubkey age-verschlüsseln
      - config.yaml schreiben, setup_state = awaiting_registration
  → Redirect /setup/register:
      - zeigt mailto-Link mit Anhang + Download-Button
      - zeigt "Status: warten auf Freischaltung …" mit Polling
      - erklärt was passiert (inkl. Kontakt-Fallback)

(Betreiber erhält Mail, führt register-tunnel + schulen add aus — §4.2)

  → Polling sieht Dex-Tunnel + OIDC-Client live → setup_state = ready
      - postgres + dex werden automatisch installiert (als pflichtige Apps im Katalog)
      - Dex-Tunnel wird als Dauer-Prozess gestartet
      - Redirect auf /login
```

### 11.2 App installieren

```
Admin klickt auf /apps → "Installieren" bei z.B. Langflow:

stackctl:
  1. Lädt Definition (cache oder von catalog-URL)
  2. Prüft depends_on: sind postgres + dex da? → ja
  3. Zeigt Prompt-Formular (nur was die Definition fragt)
  4. Nach Submit:
      a. secrets: alle auto-generieren (X_DB_PASSWORD, X_OIDC_SECRET, ...)
      b. global_env-Defaults einfüllen (LLM_KEYS etc.)
      c. Wenn depends_on postgres: DB + User anlegen
      d. Wenn oidc-Block: Client in dex-config.yaml eintragen, docker restart ls-dex
      e. data-Verzeichnisse unter /opt/learningstack/{id}/ anlegen
      f. configs[] als Dateien schreiben
      g. docker-compose.yml neu generieren (alle installierten Apps)
      h. docker compose up -d ls-{id}
      i. post_install scripts/messages
      j. state.yaml + .env speichern
  5. Ergebnisseite: Link zur App, ggf. auto-generierte Secrets (copy to clipboard)
```

### 11.3 Dex-Tunnel (immer an)

Der Dex-Tunnel ist **nicht optional**. `DEX_AUTH_URL = https://auth.{slug}.learningstack.online` ist eine Konstante, die stackctl beim Setup berechnet und danach nie wieder anfasst. Er wird auf drei Wegen gesichert:

1. **Beim Wechsel nach `ready`:** stackctl startet den autossh-Prozess und trägt ihn in `state.yaml` unter dem fixen Key `_dex` ein.
2. **Beim systemd-Start von stackctl (`web`-Kommando):** vor `ListenAndServe` wird `tunnel.EnsureDexTunnel()` aufgerufen, der den autossh-Prozess anlegt (falls nicht schon da).
3. **Monitor-Goroutine:** prüft alle 30s den Prozess-Status. Stirbt autossh, wird es neu gestartet. Stirbt es mehrmals hintereinander (>5 Fehlschläge in 5 Minuten), meldet stackctl einen **roten Hinweis** auf der Tunnel-Seite mit Debug-Output.

In der Tunnel-UI wird der Dex-Tunnel als erste Zeile angezeigt — **ohne** Ein-/Ausschalter, nur mit Status-Dot und "Testen"-Button.

### 11.4 Tunnel für App aktivieren

```
Admin klickt auf App-Karte → Tunnel-Toggle → ON:

stackctl:
  1. start autossh: -R {app_id}.{slug}:80:localhost:{port} tunnel@sish.learningstack.online
  2. OIDC: redirect_uri wurde beim Install schon als public URL eingetragen
     → Dex-Config ändert sich NICHT
  3. state.yaml: containers[{id}].tunnel_enabled=true, tunnel_subdomain gesetzt
  4. UI: zeigt öffentliche URL
```

**Wichtig:** Eine OIDC-App funktioniert ohne Tunnel nicht (redirect_uri zeigt auf public URL). stackctl warnt das bei Install explizit: "Diese App nutzt OIDC – du musst sie nach der Installation öffentlich tunneln, damit der Login funktioniert." Beim Deaktivieren des Tunnels kommt ein Confirm-Dialog mit genau diesem Hinweis.

### 11.5 Self-Update stackctl

```
Admin klickt /system/update (oder: CLI `sudo stackctl self-update`)

stackctl:
  1. GET https://api.github.com/repos/lngstck/stackctl/releases/latest
  2. Vergleicht tag mit /opt/stackctl/stackctl.version
  3. Wenn neuer:
      a. Lädt stackctl-linux-{arch} nach /opt/stackctl/stackctl.new
      b. chmod +x, prüft: stackctl.new version → ausführbar?
      c. mv stackctl.new stackctl (atomic auf gleichem FS)
      d. echo "{new_version}" > stackctl.version
      e. systemctl restart stackctl
  4. Web-UI kommt nach 2-3 Sekunden neu hoch
```

systemd's `Restart=on-failure` fängt den kurzen Ausfall ab. Sessions sind weg (in-memory), Admin muss neu einloggen.

### 11.6 Passwort-Reset per CLI (Recovery)

```
Admin hat sich ausgesperrt → SSH auf den Server:
  sudo stackctl reset-password
  → fragt neues Passwort 2x
  → schreibt neuen Hash in config.yaml
  → schreibt neues STACKCTL_ADMIN_PASSWORD in .env
  → docker compose up -d (restart von Containern mit admin_password_env)
  → Done.
```

## 12. Konfigurationsdateien im Detail

### config.yaml

```yaml
version: 2
setup_state: "ready"                   # needs_setup | awaiting_registration | ready
school:
  name: "Gymnasium Phoenix"
  slug: "phoenix"
  server_domain: "192.168.1.10"        # oder Hostname
  contact_email: "admin@gym-phoenix.de"
catalog:
  url: "https://raw.githubusercontent.com/lngstck/catalog/main"
  # oder: "https://catalog.learningstack.online"
admin:
  password_hash: "$2a$10$..."          # bcrypt
dex:
  # Upstream ist fest "moin_schule via zentralem Dex". Kein Switching.
  client_id: "phoenix"                 # = school.slug, für auth.learningstack.online
  client_secret: "<40 hex>"            # vom Setup erzeugt, im Registrierungs-Paket
  auth_url: "https://auth.phoenix.learningstack.online"  # immutable nach setup
registration:
  state_entered_at: "2026-04-09T12:34:56Z"  # für UI "wartet seit ..."
  package_path: "config/registration-phoenix.age"
  operator_pubkey_fingerprint: "age1xj..."  # zum Anzeigen, nicht sicherheitsrelevant
global_env:
  LLM_ENDPOINT: "https://llm.learningstack.online/v1"
  LLM_API_KEY: ""
  LLM_DEFAULT_MODEL: "gpt-4o-mini"
tunnel:
  ssh_host: "sish.learningstack.online"
  ssh_port: 22
```

`setup_state` ist die einzige Quelle der Wahrheit für die Zustands-Maschine aus §10. Der Übergang `needs_setup → awaiting_registration` geschieht in `POST /setup`, der Übergang `awaiting_registration → ready` in einem erfolgreichen `/setup/status`-Poll.

### state.yaml

```yaml
version: "2.0"
containers:
  postgres:
    id: postgres
    name: PostgreSQL
    version_installed: "16.2"
    ports: [8100]
    env_keys: ["POSTGRES_PASSWORD"]
    installed_at: "2026-04-09T12:34:00Z"
    tunnel_enabled: false
  dex:
    id: dex
    name: Dex
    version_installed: "2.45.1"
    ports: [5556]
    env_keys: ["DEX_UPSTREAM_CLIENT_ID", "DEX_UPSTREAM_CLIENT_SECRET"]
    installed_at: "2026-04-09T12:34:30Z"
    tunnel_enabled: false
ports:
  8100: postgres
  5556: dex
```

### .env (mit Sektionen, aus build_env_sections generiert)

```env
# === global ===
SCHOOL_NAME=Gymnasium Phoenix
SCHOOL_SLUG=phoenix
SERVER_DOMAIN=192.168.1.10
DEX_AUTH_URL=https://auth.phoenix.learningstack.online
STACKCTL_ADMIN_PASSWORD=<klartext>
LLM_ENDPOINT=https://llm.learningstack.online/v1
LLM_API_KEY=sk-...
LLM_DEFAULT_MODEL=gpt-4o-mini

# === postgres ===
POSTGRES_PASSWORD=<auto>

# === dex ===
DEX_UPSTREAM_CLIENT_ID=phoenix
DEX_UPSTREAM_CLIENT_SECRET=<40 hex>

# === langflow ===
LANGFLOW_DB_PASSWORD=<auto>
LANGFLOW_OIDC_SECRET=<auto>
```

`DEX_AUTH_URL` ist ab Setup konstant die öffentliche URL. Apps, die Dex intern übers Docker-Netz ansprechen könnten (`http://ls-dex:5556`), tun das **nicht** — der OIDC-Issuer muss Browser- und Container-seitig identisch sein, sonst scheitert die JWT-Validierung.

Permissions: `640`, `learningstack:learningstack`. Root und der stackctl-User können lesen; niemand sonst.

### dex-config.yaml

Wird von stackctl voll generiert. Format siehe Memory `project_central_dex_moinschule.md` (insbesondere `claimMapping` Singular).

## 13. Catalog-Schema

Das Catalog-Schema bekommt ein eigenes Dokument unter `docs/catalog-spec.md`. Der alte Spec aus dem Mutter-CLAUDE.md ist weitgehend okay, wir werden aber:
- `role`-Felder komplett entfernen
- `admin_password_env` verbindlich dokumentieren
- `version` der App-Definition (SemVer) als **Quelle der Wahrheit** für Update-Erkennung festlegen
- moin.schule claim conventions hineinziehen (`sub`, `email`=sub, `groups`)

Das alte `./catalog/` im Projekt-Root wird ersetzt — wahrscheinlich durch ein eigenes Repo `lngstck/catalog`, hosted via GitHub Pages oder weiterhin `catalog.learningstack.online`. Das ist aber **nicht Teil dieses Architektur-Dokuments**, sondern folgt eigenständig.

## 14. Build, Release, Distribution

### Build (lokal auf Mac)

```makefile
# Makefile-Auszug
VERSION := $(shell git describe --tags --always --dirty)
LDFLAGS := -s -w -X main.version=$(VERSION)

build-linux-amd64:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/stackctl-linux-amd64 ./cmd/stackctl

build-linux-arm64:
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/stackctl-linux-arm64 ./cmd/stackctl

build-all: build-linux-amd64 build-linux-arm64

dev:
	go run ./cmd/stackctl web --dev --host 127.0.0.1 --port 8090
```

### Release (Mac → GitHub)

Über GitHub Actions, getriggert von Git-Tag `vX.Y.Z`:
1. `go test ./...`
2. `make build-all`
3. `gh release create vX.Y.Z dist/stackctl-linux-amd64 dist/stackctl-linux-arm64`

Manuell als Fallback: `gh release create vX.Y.Z ...` vom Mac aus.

### Distribution

- `install.sh` liegt im Repo-Root, wird via `curl | sudo bash` aus `main` gezogen.
- Binaries liegen als Release-Assets.
- Self-Update sucht `releases/latest` via GitHub API (unauthentifiziert, 60 req/h reicht).

## 15. Entwicklungs-Workflow

**Setup:**
- Code lebt unter `~/Developer/learningstack/stackctl/` auf dem Mac, Git-tracked.
- Linux-Devbox (Hostname: **TBD**, Benutzer `learningstack`).
- `~/.ssh/config` auf dem Mac hat einen Eintrag `learningstack-local` → `192.168.1.161` (User `learningstack`), sodass `ssh learningstack-local` "einfach funktioniert". SSH-Key liegt in 1Password.

**Alltag – Go-Code-Änderung:**
```makefile
deploy-devbox:
	GOOS=linux GOARCH=amd64 go build -o dist/stackctl-dev ./cmd/stackctl
	rsync -avz dist/stackctl-dev learningstack-local:/tmp/stackctl
	ssh learningstack-local 'sudo mv /tmp/stackctl /opt/stackctl/stackctl && sudo systemctl restart stackctl'
```
Dauer: ~3 Sekunden.

**Alltag – nur Template/CSS-Änderung:**
Wenn stackctl im `--dev`-Modus läuft (Templates vom FS), reicht ein `rsync` der Template-Datei, kein Rebuild, kein Restart. Für schnelle UI-Iteration kann man das Template-Verzeichnis auch per `sshfs` mounten.

**Browser-Zugriff vom Mac:**
```
ssh -L 8090:localhost:8090 learningstack-local
```
Dann im Mac-Browser `http://localhost:8090`.

**Entwicklungsdaten:** Auf der Devbox via Env-Vars
```
STACKCTL_DIR=/home/learningstack/dev-stackctl
LEARNINGSTACK_DIR=/home/learningstack/dev-learningstack
```
damit nichts an der späteren Produktions-Struktur ankratzt.

## 16. Sicherheits-Notizen

- **stackctl-Web-UI** bindet per Default an `0.0.0.0:8090` – das ist für die Schule gewollt (Admin will von seinem Windows-Rechner im LAN drauf). **Kein TLS** in der Erstversion, weil Schul-LANs HTTP zulassen. TLS kommt später, wenn nginx davorsteht.
- **Admin-Passwort**: bcrypt, cost 10. Login-Rate-Limit pro IP.
- **Sessions**: In-Memory, Cookie `HttpOnly; SameSite=Lax; Secure` nur wenn TLS. Lifetime z.B. 12 Stunden.
- **XSS**: alle Template-Renders via `html/template` (auto-escape). Kein String-Concat in HTML.
- **CSRF**: Token in jedem Formular, gegen Session validiert.
- **Command-Injection**: alle `docker`-Aufrufe via `exec.Command` mit Args-Array, **nie** `sh -c`.
- **File-Permissions**:
  - `.env` → 640 learningstack:learningstack
  - `tunnel_key` → 600 learningstack:learningstack
  - `config.yaml` → 640 learningstack:learningstack
- **Secrets im Web-UI**: Secrets werden bei Install **einmal** angezeigt, danach nur noch maskiert. Expliziter "Secret anzeigen"-Button mit Passwort-Rückfrage.

### 16.1 Registrierungs-Paket (age-verschlüsselt)

- **Format:** `filippo.io/age`, ASCII-armored, Content-Type YAML. Dateiname `registration-{slug}.age`.
- **Empfänger-Key:** Der öffentliche age-Key des Betreibers ist als Konstante im stackctl-Binary eingebettet (`internal/setup/operator_key.go`). Er wird nie über das Netz nachgeladen — wer das Binary austauscht, tauscht auch den Empfänger.
- **Key-Rotation:** Bei Rotation wird eine neue stackctl-Version gereleast mit neuem Key. Alte stackctl-Installationen verschlüsseln gegen den alten Key, den der Betreiber weiter entschlüsseln kann, bis die Alten via Self-Update migriert sind. Der Empfänger kann **mehrere** Keys gleichzeitig gültig haben.
- **Inhalt:** nichts, was nur der Server kennt, außer dem frischen SSH-Pubkey und dem frischen Client-Secret. Der Private-SSH-Key und alle weiteren Secrets bleiben auf der Schul-Maschine.
- **Zweckbindung:** Das Paket wird nur einmal erzeugt und lokal in `config/` abgelegt. Bei `POST /setup/register/retry` wird es identisch neu ausgegeben (kein Key-Neuwürfeln, sonst müsste der Betreiber alles neu eintragen).
- **Bedrohungsmodell:**
  - Mail-Provider kann Mail lesen → sieht nur age-Ciphertext.
  - Operator-Postfach kompromittiert → Angreifer kann neue Schulen eintragen, aber keine bestehenden übernehmen (bestehende Schulen haben schon ihre Client-Secrets).
  - Schul-Admin-Maschine kompromittiert → Angreifer kennt alle Secrets der Schule sowieso (er hat root).
  - Betreiber-age-Private-Key verloren → betreiberseitiges Recovery, neue Keys für alle rotieren. Dokumentiert in `docs/operator-runbook.md`.
- **CSRF / Replay:** irrelevant, weil das Paket von außen reinkommt und der Betreiber manuell prüft, bevor er `register-tunnel` und `schulen add` ausführt. Der Betreiber sieht `school_name`, `contact_email`, `created_at` im Klartext nach Entschlüsselung.

### 16.2 Tunnel-Sicherheit

- `DEX_AUTH_URL` ist immer `https://...` — der Browser bekommt echtes TLS via nginx auf System 1. Innerhalb des Tunnels ist es HTTP auf localhost, was für diesen Hop okay ist.
- Der ed25519-Key ist an den Host gebunden (`from="..."` in den authorized_keys auf sish würde gehen, ist aber nicht verpflichtend; Key-only auth reicht).
- sish auf System 1 trennt Schulen per Subdomain-Namespace. Eine Schule kann nicht auf die Subdomain einer anderen Schule forwarden, solange sish das Key-zu-Subdomain-Mapping respektiert — das ist beim Onboarding via `register-tunnel` festzulegen.

## 17. Phase 1 Scope vs. Phase 2

### Phase 1 (MVP – dieser Neubau)

- ✅ Install-Skript + Self-Update über GitHub
- ✅ Web-UI mit Dashboard, Apps, Settings, Tunnel, System
- ✅ Setup-Wizard + Registrierungs-Flow (age-Paket, Polling)
- ✅ Pflicht-Container postgres + dex automatisch beim Setup
- ✅ Dex **immer** getunnelt (fester `DEX_AUTH_URL`)
- ✅ App-Install mit Prompts, Secrets, OIDC-Registrierung
- ✅ Tunnel an/aus pro App
- ✅ Container-Updates via Katalog-Versionsvergleich
- ✅ Passwort-Reset per CLI + Web
- ✅ CLI: version, web, setup, status, reset-password, repair, self-update
- ✅ Betreiber-Tooling: `tools/register-tunnel` (System 1), `schulen add --client-id/-secret` (System 2)

### Phase 2 (später, nicht in diesem Build)

- Backup/Restore (`stackctl backup` → tar.gz; Postgres-Dumps separat)
- Ein-Schritt-Meta-Skript `registriere-schule` (SSH zu beiden Systemen)
- User-Portal-Container (separates Projekt, Port 80)
- Mehrere Admin-User mit Rollen
- Audit-Log der Admin-Aktionen
- HTTPS direkt in stackctl (oder via Caddy/nginx als Reverse Proxy vor :8090)
- Health-Checks + Monitoring-Hooks
- Alternative Dex-Upstreams (Wobila, static für Test-Schulen)

## 18. Bestätigte Entscheidungen

Diese Punkte waren früher offene Fragen und sind jetzt fixiert:

1. **Linux-Devbox:** SSH-Alias `learningstack-local` (→ `192.168.1.161`, User `learningstack`). Default-Target für `deploy-devbox` im Makefile.
2. **Catalog-Host:** eigenes GitHub-Repo `lngstck/catalog` (via raw.githubusercontent.com oder GitHub Pages). `catalog.learningstack.online` bleibt als Alias nutzbar, ist aber nicht Quelle.
3. **Port 8090** für die stackctl-Web-UI. Port 80 bleibt dem späteren User-Portal vorbehalten.
4. **Dex immer getunnelt.** Der Schul-Dex ist in Phase 1 permanent öffentlich unter `https://auth.{slug}.learningstack.online`. Kein lokaler Modus, kein Toggle. Der Tunnel wird beim Setup aufgezogen und von stackctl aktiv überwacht.
5. **Upstream-Typ fest:** zentraler Dex → moin.schule. Kein Static-Password-Fallback, kein Wobila in Phase 1. Andere Upstreams folgen in Phase 2.
6. **Credential-Generierung:** stackctl erzeugt SSH-Key, Dex-Client-Secret, Admin-Passwort-Hash und alle App-Secrets **selbst**. Kein Copy-Paste durch den Admin.
7. **Credential-Transport:** age-verschlüsseltes Paket an `registrierung@learningstack.online`, Einbahn-Flow. Siehe §11.0 und §16.1.
8. **Lizenz:** AGPL-3.0. Siehe §19.
9. **SCHOOL_SLUG-Regeln:** `[a-z0-9-]`, 3..30 Zeichen, muss mit Buchstaben beginnen, keine doppelten Bindestriche, kein führender/schließender Bindestrich. Hart validiert beim Setup, weil der Slug in Subdomains und DB-Namen landet.
10. **GitHub Actions:** `.github/workflows/release.yml` wird erst beim ersten echten Release angelegt. Der erste v0.1.0 wird manuell vom Mac aus geschnitten.

### Wirklich noch offen

- **age-Pubkey des Betreibers:** der konkrete Key muss erzeugt und in `internal/setup/operator_key.go` eingebettet werden, bevor der erste Release rausgeht. TODO beim ersten Build.

---

## 19. Lizenz

**AGPL-3.0-or-later.**

### Warum AGPL

- learningstack ist ein Projekt für öffentliche Schulen — Steuergelder, Lehrkräfte-Zeit, Schülerdaten. Es ist richtig, dass Verbesserungen an dieser Software, die als Dienst betrieben wird, der Allgemeinheit zurückfließen.
- AGPL schließt im Gegensatz zu GPL auch die "SaaS-Lücke": wer stackctl nimmt, leicht modifiziert, und kommerziell als "Schul-IT-Komplettpaket" hostet, muss seine Änderungen unter AGPL weitergeben. Das schützt das Projekt davor, dass ein Anbieter das ganze Ökosystem inhouse forkt und die Community aushungert.
- Für die Zielgruppe (Schulträger, Lehrkräfte, IT-Betreuer) ist AGPL unproblematisch: sie betreiben stackctl on-prem, sie verkaufen keinen SaaS-Dienst, sie haben keinen Lizenzkonflikt.
- Der Konflikt-Fall "AGPL vs. proprietäre Kunden" existiert bei uns nicht, weil es keine proprietären Kunden gibt.

### Was AGPL praktisch bedeutet

- Jeder darf stackctl nutzen, verändern und weitergeben.
- Wer eine modifizierte Version auf einem Server betreibt, über die Nutzer interagieren (direkt oder indirekt übers Netz), muss den Quellcode **dieser modifizierten Version** den Nutzern zugänglich machen.
- Abhängigkeiten müssen AGPL-kompatibel sein:
  - `gopkg.in/yaml.v3` — Apache-2.0 ✓
  - `golang.org/x/crypto/bcrypt` — BSD-3-Clause ✓
  - `filippo.io/age` — BSD-3-Clause ✓
  - `oat.ink` (CSS/JS) — MIT ✓ (laut oat-Website)
- Copyright-Header in jeder Go-Datei: `// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.`

### `LICENSE`-Datei

Wird im Repo-Root als Volltext AGPL-3.0 hinterlegt. Die kanonische Version ist <https://www.gnu.org/licenses/agpl-3.0.txt>.

### Contributor-Policy

Keine CLA für Phase 1. Contributions gelten per default als unter AGPL-3.0-or-later beigesteuert (inbound = outbound), dokumentiert im `CONTRIBUTING.md` sobald es das gibt.

---

## Anhang A: Initiale Task-Reihenfolge nach Freigabe dieses Dokuments

(Nur zur Orientierung — wird erst nach Freigabe angefasst.)

1. Go-Modul, Makefile, CI-Gerüst, README-Stub
2. `internal/paths` + `internal/config` + `internal/envfile` + `internal/secrets` (Fundament)
3. `internal/docker` + `internal/compose` (docker-compose generieren)
4. `internal/catalog` (Katalog laden)
5. `internal/dex` + `internal/postgres` (Pflicht-Container-Verwaltung)
6. `internal/install` (App-Install-Flow orchestrieren)
7. `internal/web` Skelett: Layout, Login, Setup, Dashboard
8. Web-UI: Apps-Seite, Install-Flow, App-Detail
9. `internal/tunnel` + Tunnel-UI
10. `internal/update` + System-Seite
11. `scripts/install.sh` + systemd-Unit
12. Erste v0.1.0 Release auf GitHub
13. Install auf Devbox, Iteration an der UI
