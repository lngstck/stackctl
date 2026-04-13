# stackctl

Control plane für [learningstack](https://learningstack.online) — die selbst-gehostete Schul-IT-Plattform für deutsche Schulen.

> **Hinweis:** Dieses Projekt befindet sich in einer frühen Testphase. Es ist noch nicht für den produktiven Einsatz geeignet. APIs, Konfigurationsformate und Verhalten können sich jederzeit ändern.

## Was ist das?

stackctl ist das einzige Tool, das ein Schul-Admin auf einem frischen Linux-Server installiert, um darauf Docker-basierte Anwendungen für den Unterricht zu betreiben. Es ersetzt "Ich muss wissen, was Docker, OIDC und docker-compose sind" durch eine Web-Oberfläche mit verständlichen Knöpfen.

- **Pflicht-Container**: PostgreSQL und [Dex](https://dexidp.io) (OIDC) werden automatisch eingerichtet.
- **Apps aus dem Katalog**: Open-WebUI, Langflow, … ein Klick zur Installation.
- **Tunnel zur Außenwelt**: Apps wahlweise nur im Schul-LAN oder öffentlich unter `<app>.<schule>.learningstack.online`.
- **Single-Sign-On**: Alle Apps authentifizieren gegen den lokalen Dex, der über einen zentralen OIDC-Proxy mit schulischen Identity-Anbietern verbunden wird.

## Installation

```bash
curl -fsSL https://raw.githubusercontent.com/lngstck/stackctl/main/scripts/install.sh | sudo bash
```

Danach `http://<server-ip>:8090` im Browser öffnen und dem Setup-Wizard folgen.

Voraussetzungen: Ubuntu 22.04+ oder Debian 12+, Docker 24+, ein eingehender Port 8090 im lokalen Netz.

## Entwicklung

```bash
# Lokal auf dem Mac
make dev              # startet stackctl web --dev auf :8090

# Cross-compile für Linux
make build-all        # erzeugt dist/stackctl-linux-{amd64,arm64}
```

## Lizenz

[AGPL-3.0-or-later](LICENSE)
