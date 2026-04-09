# stackctl

Control plane für [learningstack](https://learningstack.online) — die selbst-gehostete Schul-IT-Plattform für deutsche Schulen.

> **Status:** Neubau in Go. Noch kein Code, nur Architektur. Siehe [`ARCHITECTURE.md`](ARCHITECTURE.md).

## Was ist das?

stackctl ist das einzige Tool, das ein Schul-Admin auf einem frischen Linux-Server installiert, um darauf Docker-basierte Anwendungen für den Unterricht zu betreiben. Es ersetzt "Ich muss wissen, was Docker, OIDC und docker-compose sind" durch eine Web-Oberfläche mit verständlichen Knöpfen.

- **Pflicht-Container**: PostgreSQL und [Dex](https://dexidp.io) (OIDC) werden automatisch eingerichtet.
- **Apps aus dem Katalog**: Langflow, Open-WebUI, Grafana, … ein Klick zur Installation.
- **Tunnel zur Außenwelt**: Apps wahlweise nur im Schul-LAN oder öffentlich unter `<app>.<schule>.learningstack.online`.
- **Single-Sign-On**: alle Apps authentifizieren gegen den lokalen Dex, der wiederum über den zentralen Dex mit [moin.schule](https://moin.schule) spricht.

## Installation

```bash
curl -fsSL https://raw.githubusercontent.com/lngstck/stackctl/main/scripts/install.sh | sudo bash
```

Danach `http://<server-ip>:8090` im Browser öffnen und dem Setup-Wizard folgen.

Voraussetzungen: Ubuntu 22.04+ oder Debian 12+, Docker 24+, ein eingehender Port 8090 im lokalen Netz.

## Entwicklung

Siehe [`ARCHITECTURE.md`](ARCHITECTURE.md) für die vollständige Beschreibung von Build-System, Deployment und Entwicklungs-Workflow.

```bash
# Lokal auf dem Mac
make dev              # startet stackctl web --dev auf :8090

# Linux-Devbox (learningstack-local)
make deploy-devbox    # cross-compile + rsync + systemctl restart
```

## Lizenz

AGPL-3.0-or-later. Siehe [`LICENSE`](LICENSE) und §19 in [`ARCHITECTURE.md`](ARCHITECTURE.md).
