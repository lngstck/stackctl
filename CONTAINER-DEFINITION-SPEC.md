# Container Definition Specification v3.0

**Gueltig fuer:** stackctl >= 0.2.0 (Go-Neubau)

Diese Datei ist der massgebliche Standard fuer Container-Definitionen im
learningstack-Katalog. Definitionen die nicht dieser Spec entsprechen werden
von stackctl abgelehnt.

---

## 1. Katalogserver-Struktur

```
catalog.learningstack.online/
├── catalog.yaml                ← App-Index
├── containers/
│   ├── postgres.yaml
│   ├── dex.yaml
│   ├── open-webui.yaml
│   └── ...
└── binaries/
    ├── langflow/
    │   └── itbox_llm.py
    └── ...
```

## 2. Ordnerstruktur auf dem Schulserver

```
/opt/stackctl/                  ← stackctl Programm
  └── config/
      ├── config.yaml
      ├── state.yaml
      ├── dex-config.yaml       ← von stackctl generiert
      ├── catalog.yaml          ← gecacht
      └── catalog/
          ├── postgres.yaml
          └── ...

/opt/learningstack/             ← Container-Daten (getrennt vom Programm)
  └── <app_id>/
      ├── data/                 ← persistente Daten
      └── config/               ← Config-Dateien
```

Alle `host`-Pfade in Definitionen muessen unter `/opt/learningstack/<id>/` liegen.

## 3. .env-Datei

Pfad: `/opt/stackctl/compose/.env` (Permissions: 0640)

```bash
# learningstack environment – managed by stackctl

# === global ===
SCHOOL_NAME=Gymnasium Phoenix
SCHOOL_SLUG=phoenix
SERVER_DOMAIN=192.168.1.10
DEX_AUTH_URL=https://auth.phoenix.learningstack.online
STACKCTL_ADMIN_PASSWORD=Xy7!kj2...
LLM_ENDPOINT=https://llm.learningstack.online/v1
LLM_API_KEY=ls_abc123...

# === postgres ===
POSTGRES_PASSWORD=Pq9$rr...

# === open-webui ===
OPENWEBUI_OIDC_SECRET=a1b2c3...
```

System-Variablen (immer vorhanden, von stackctl verwaltet):

| Variable | Beschreibung |
|---|---|
| `SCHOOL_NAME` | Anzeigename der Schule |
| `SCHOOL_SLUG` | URL-sichere Kurzform |
| `SERVER_DOMAIN` | LAN-IP oder Hostname |
| `DEX_AUTH_URL` | Immer `https://auth.{slug}.learningstack.online` |
| `STACKCTL_ADMIN_PASSWORD` | Klartext-Passwort, referenzierbar von Apps |

---

## 4. Container-Definition — Vollstaendiges Schema

### Metadaten (Pflicht)

```yaml
id: open-webui                     # Eindeutig, [a-z0-9][a-z0-9-]*, kein -- erlaubt
name: Open WebUI                   # Anzeigename
version: "1.0"                     # SemVer der Definition
description: Chat-Oberflaeche fuer LLMs
category: education                # education | infrastructure | games | (erweiterbar)
```

Kategorien werden als String gelesen — neue Kategorien koennen jederzeit
eingefuehrt werden ohne Code-Aenderung.

```yaml
links:                             # Optional
  homepage: https://openwebui.com
  docs: https://docs.openwebui.com
```

### Image (Pflicht)

```yaml
image:
  name: ghcr.io/open-webui/open-webui
  tag: main
```

### Ports (Pflicht, mind. 1)

```yaml
ports:
  - host: 8310                     # Muss stackweit eindeutig sein
    container: 8080
    description: Web UI
    bind: "0.0.0.0"               # Optional; default "0.0.0.0"
                                   # "127.0.0.1" fuer nur-lokal
```

Keine festen Port-Ranges. stackctl prueft auf Konflikte mit bereits
installierten Apps.

### Volumes (optional)

```yaml
volumes:
  - host: /opt/learningstack/open-webui/data
    container: /app/backend/data
    readonly: false                # Optional; default false
```

### Config-Dateien (optional)

Werden vor Container-Start auf dem Host geschrieben. Unterstuetzen
Template-Substitution: `${VAR}` wird aus der .env aufgeloest.

```yaml
configs:
  - path: /opt/learningstack/open-webui/config.json
    content: |
      {
        "school": "${SCHOOL_NAME}",
        "auth_url": "${DEX_AUTH_URL}"
      }
    mode: "0640"                   # Optional; default "0644"
```

Alle System-Variablen und App-Secrets sind als `${VAR}` verfuegbar.

### Statische Umgebungsvariablen (optional)

`${KEY}` wird zur Laufzeit von docker-compose aus der .env aufgeloest.

```yaml
environment:
  - key: OAUTH_CLIENT_ID
    value: open-webui
  - key: OAUTH_CLIENT_SECRET
    value: "${OPENWEBUI_OIDC_SECRET}"
  - key: OPENID_PROVIDER_URL
    value: "${DEX_AUTH_URL}"
```

### Secrets (optional)

Automatisch generiert, in der .env gespeichert. Nie interaktiv abfragen —
das ist Aufgabe von `prompts`.

```yaml
secrets:
  - key: OPENWEBUI_OIDC_SECRET
    generate: secret               # secret   -> 40-char hex
  - key: SOME_PASSWORD
    generate: password             # password -> 20 Zeichen, Buchstaben+Ziffern+Sonderzeichen
  - key: SOME_API_KEY
    generate: api_key              # api_key  -> "{prefix}_{token_hex(16)}"
    prefix: ls                     # Nur bei api_key
```

### Globale Variablen (optional)

Geteilt zwischen Apps. Werden einmalig gesetzt beim ersten App das sie
braucht. Landen im `=== global ===` Block der .env.

```yaml
global_env:
  - key: LLM_ENDPOINT
    description: OpenAI-kompatibler LLM-Endpunkt
    required: false                # true -> Install blockiert wenn nicht gesetzt
    default: https://llm.learningstack.online/v1
  - key: LLM_API_KEY
    description: API-Key fuer LLM-Proxy
    required: false
```

### Prompts (optional)

Interaktive Abfragen beim Install. Antworten landen in der .env (App-Sektion).

```yaml
prompts:
  - key: ADMIN_EMAIL
    question: E-Mail-Adresse des Admin-Accounts
    required: true
    validate: email                # email | int | url | (leer = beliebig)
    hint: "Wird fuer den ersten Login benoetigt"
  - key: WORKER_COUNT
    question: Anzahl Worker-Prozesse
    default: "1"
    validate: int
  - key: GAME_MODE
    question: Spielmodus
    options: [survival, creative, adventure]
    default: creative
```

### Abhaengigkeiten (optional)

```yaml
depends_on:
  - postgres
  - dex
```

stackctl prueft vor dem Install und blockiert bei fehlenden Dependencies.

### OIDC (optional)

```yaml
oidc:
  client_id: open-webui
  redirect_path: /oauth/oidc/callback
```

stackctl registriert automatisch einen OIDC-Client bei Dex und baut die
vollstaendige `redirect_uri`:

- **Getunnelt:** `https://{app_id}.{slug}.learningstack.online{redirect_path}`
- **Lokal:** `http://{server_domain}:{port}{redirect_path}`

Das generierte OIDC-Secret wird als `{APP_ID_UPPER}_OIDC_SECRET` in der
.env gespeichert.

### Admin-Passwort (optional)

```yaml
admin_password_env: WEBUI_SECRET_KEY
```

stackctl injiziert den Wert von `STACKCTL_ADMIN_PASSWORD` unter diesem
Environment-Key in den Container. So teilen sich alle Apps dasselbe
Admin-Passwort.

### Binaries (optional)

Dateien die vor Container-Start heruntergeladen werden.

```yaml
binaries:
  # Catalog-relativ
  - source: binaries/langflow/itbox_llm.py
    destination: /opt/learningstack/langflow/components/itbox_llm.py
    mode: "0644"                   # Optional; default "0644"

  # Externe URL
  - source: https://example.com/releases/v1.0/flow.json
    destination: /opt/learningstack/langflow/flows/default.json
```

Beginnt `source` mit `http://` oder `https://` wird direkt von der URL
heruntergeladen. Sonst wird der Pfad relativ zum Katalogserver aufgeloest.

### Scripts (optional)

Werden nach Container-Start ausgefuehrt.

```yaml
scripts:
  post_install:
    - type: docker-exec            # Im Container ausfuehren
      container: ls-postgres       # container_name (immer "ls-{app_id}")
      wait: healthy                # healthy | started | <sekunden>
      command: |
        psql -U postgres -c "CREATE DATABASE openwebui;"
    - type: host                   # Auf dem Server ausfuehren
      command: |
        chmod -R 755 /opt/learningstack/langflow/components
```

`wait`-Werte:
- `healthy` — wartet auf Docker Healthcheck
- `started` — wartet bis Container laeuft
- `30` (Ganzzahl) — wartet N Sekunden
- Nicht angegeben — sofort ausfuehren

Bei Fehler: automatisch Retry (max 10x, 3s Pause).

### Post-Install-Anzeige (optional)

```yaml
post_install:
  messages:
    - "Open WebUI: http://{server_domain}:8310"
    - "Admin-Login: {ADMIN_EMAIL}"
  secrets_to_show:
    - key: OPENWEBUI_OIDC_SECRET
      label: OIDC-Secret
```

Platzhalter in `messages`: `{server_domain}`, `{port}` (erster Port),
`{KEY}` (beliebiger .env-Key).

`secrets_to_show`: Werte werden einmalig mit Kopier-Button angezeigt.

---

## 5. catalog.yaml — Index-Format

```yaml
version: "1.0"
apps:
  - id: postgres
    name: PostgreSQL
    category: infrastructure
    description: Zentrale Datenbank fuer alle Apps
  - id: dex
    name: Dex (OIDC)
    category: infrastructure
    description: OIDC-Provider (Proxy zu moin.schule)
  - id: open-webui
    name: Open WebUI
    category: education
    description: Chat-Oberflaeche fuer LLMs

global_env_schema:                 # Informativ; Apps sind die Quelle der Wahrheit
  - key: LLM_ENDPOINT
    description: OpenAI-kompatibler LLM-Endpunkt
    required: false
    default: https://llm.learningstack.online/v1
  - key: LLM_API_KEY
    description: API-Key fuer LLM-Proxy
    required: false
```

---

## 6. Install-Flow

```
[Installieren] geklickt
       |
1. Dependency-Check
   -> fehlend? -> Fehler, abbrechen
       |
2. Prompts vorhanden?
   -> ja:  Formular anzeigen
   -> nein: weiter
       |
3. [Jetzt installieren] bestaetigt
       |
4. Sequentieller Ablauf:
   1) Secrets generieren
   2) Binaries herunterladen (catalog + externe URLs)
   3) Verzeichnisse anlegen, Config-Dateien schreiben (mit Template-Substitution)
   4) PostgreSQL-DB anlegen (falls depends_on: postgres)
   5) OIDC-Client bei Dex registrieren (falls oidc: vorhanden)
   6) docker-compose.yml neu generieren
   7) Container starten
   8) Post-Install-Scripts ausfuehren
   9) State aktualisieren
       |
5. Ergebnis:
   -> post_install.messages anzeigen
   -> secrets_to_show mit [Kopieren]-Button
```

---

## 7. Validierungsregeln

| Feld | Regel |
|---|---|
| `id` | Pflicht, `[a-z0-9][a-z0-9-]*`, kein `--`, stackweit eindeutig |
| `name` | Pflicht, nicht leer |
| `version` | Pflicht, nicht leer |
| `description` | Pflicht, nicht leer |
| `category` | Pflicht, nicht leer |
| `image.name` | Pflicht |
| `image.tag` | Pflicht |
| `ports` | Mind. 1 Eintrag; `host` + `container` Pflicht; `host` stackweit eindeutig |
| `volumes[].host` | Muss mit `/opt/learningstack/<id>/` beginnen |
| `configs[].path` | Muss mit `/opt/learningstack/<id>/` beginnen |
| `binaries[].destination` | Muss mit `/opt/learningstack/<id>/` beginnen |
| `scripts[].type` | Nur `docker-exec` oder `host` |
| `scripts[].wait` | Nur `healthy`, `started`, oder positive Ganzzahl |
| `prompts[].validate` | Nur `email`, `int`, `url`, oder leer |
| `oidc.redirect_path` | Muss mit `/` beginnen |

---

## 8. Vollstaendiges Beispiel: open-webui.yaml

```yaml
id: open-webui
name: Open WebUI
version: "1.0"
description: Chat-Oberflaeche fuer LLMs
category: education

links:
  homepage: https://openwebui.com
  docs: https://docs.openwebui.com

image:
  name: ghcr.io/open-webui/open-webui
  tag: main

ports:
  - host: 8310
    container: 8080
    description: Web UI

volumes:
  - host: /opt/learningstack/open-webui/data
    container: /app/backend/data

depends_on:
  - dex

oidc:
  client_id: open-webui
  redirect_path: /oauth/oidc/callback

admin_password_env: WEBUI_SECRET_KEY

environment:
  - key: OAUTH_CLIENT_ID
    value: open-webui
  - key: OAUTH_CLIENT_SECRET
    value: "${OPENWEBUI_OIDC_SECRET}"
  - key: OPENID_PROVIDER_URL
    value: "${DEX_AUTH_URL}"
  - key: OAUTH_PROVIDER_NAME
    value: "Schullogin"
  - key: ENABLE_OAUTH_SIGNUP
    value: "true"

secrets:
  - key: OPENWEBUI_OIDC_SECRET
    generate: secret

global_env:
  - key: LLM_ENDPOINT
    description: OpenAI-kompatibler LLM-Endpunkt
    required: false
    default: https://llm.learningstack.online/v1
  - key: LLM_API_KEY
    description: API-Key fuer LLM-Proxy
    required: false

post_install:
  messages:
    - "Open WebUI: http://{server_domain}:8310"
  secrets_to_show:
    - key: OPENWEBUI_OIDC_SECRET
      label: OIDC-Secret
```
