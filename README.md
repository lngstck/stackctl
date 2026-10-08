# stackctl

Control plane für [learningstack](https://learningstack.online) — die selbst-gehostete Schul-IT-Plattform für deutsche Schulen.

> **Hinweis:** Dieses Projekt befindet sich in einer frühen Testphase. Es ist noch nicht für den produktiven Einsatz geeignet. APIs, Konfigurationsformate und Verhalten können sich jederzeit ändern.

## Was ist das?

stackctl ist das einzige Tool, das ein Schul-Admin auf einem frischen Linux-Server installiert, um darauf Docker-basierte Anwendungen für den Unterricht zu betreiben. Es ersetzt "Ich muss wissen, was Docker, OIDC und docker-compose sind" durch eine Web-Oberfläche mit verständlichen Knöpfen.

- **Pflicht-Container**: PostgreSQL, [Dex](https://dexidp.io) (OIDC) und [Caddy](https://caddyserver.com) (HTTPS) werden automatisch eingerichtet.
- **Apps aus dem Katalog**: Open WebUI, PyLearn, Sponsorenlauf, … ein Klick zur Installation.
- **Nur im Schulnetz**: Login und Apps laufen unter der Domain der Schule mit echtem Zertifikat, erreichbar nur im Schulnetz. Keine Schülerdaten verlassen das Schulnetz. Direkter Betrieb am Internet ist als erweiterte Option möglich. Siehe [Betriebsarten](docs/betriebsarten.md).
- **Single-Sign-On**: Alle Apps authentifizieren gegen den lokalen Dex. Er spricht direkt mit dem Anmeldedienst der Schule (z. B. moin.schule, Wobila), kein Dritter dazwischen.

## Installation

```bash
curl -fsSL https://raw.githubusercontent.com/lngstck/stackctl/main/scripts/install.sh | sudo bash
```

Am Ende zeigt das Skript einen Link zur Einrichtung, z. B. `http://192.168.1.10:8090/setup?code=K7M4-Q2XP`. Er enthält den Einrichtungscode: Ohne ihn lässt sich die Einrichtung nicht abschließen, damit nicht irgendwer im Netz zuerst das Admin-Passwort setzt. Den Code neu anzeigen: `sudo stackctl setup-code`.

Eine bestimmte Version installieren, etwa einen Vorab-Stand:

```bash
curl -fsSL https://raw.githubusercontent.com/lngstck/stackctl/main/scripts/install.sh | sudo STACKCTL_VERSION=v0.12.0-rc1 bash
```

Voraussetzungen: Ubuntu 22.04+ oder Debian 12+, Docker 24+ mit Compose-Plugin (`docker-ce` + `docker-compose-plugin`, nicht das Paket `docker.io`), ein eingehender Port 8090 im lokalen Netz.

Der Assistent fragt nach der Domain der Schule und nach der Betriebsart. Für den Standard „nur im Schulnetz“ braucht es zwei DNS-Einträge und ein kostenloses Konto bei deSEC für das Zertifikat — die [Betriebsarten](docs/betriebsarten.md) beschreiben, was wann nötig ist. Die Wahl fällt einmalig bei der Einrichtung.

## Entwicklung

```bash
# Lokal auf dem Mac
make dev              # startet stackctl web --dev auf :8090

# Cross-compile für Linux
make build-all        # erzeugt dist/stackctl-linux-{amd64,arm64}
```

## Lizenz

[AGPL-3.0-or-later](LICENSE)
