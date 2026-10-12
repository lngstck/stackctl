# stackctl

Control plane für [learningstack](https://learningstack.online) — die selbst-gehostete Schul-IT-Plattform für deutsche Schulen.

> **Hinweis:** Dieses Projekt befindet sich in einer frühen Testphase. Es ist noch nicht für den produktiven Einsatz geeignet. APIs, Konfigurationsformate und Verhalten können sich jederzeit ändern.

## Was ist das?

stackctl ist das einzige Tool, das ein Schul-Admin auf einem frischen Linux-Server installiert, um darauf Docker-basierte Anwendungen für den Unterricht zu betreiben. Es ersetzt "Ich muss wissen, was Docker, OIDC und docker-compose sind" durch eine Web-Oberfläche mit verständlichen Knöpfen.

- **Grundversorgung**: Datenbank (PostgreSQL), Anmeldung ([Dex](https://dexidp.io)) und Webzugang ([Caddy](https://caddyserver.com), HTTPS) richtet stackctl direkt nach der Einrichtung ein.
- **Apps aus dem Katalog**: Open WebUI, PyLearn, Open Roberta Lab, Sponsorenlauf, … ein Klick zur Installation.
- **KI**: Ein KI-Dienst der Schule verbindet die Apps mit KI-Anbietern, etwa OpenAI oder einem Ollama im Schulnetz. Welche Modelle es gibt und mit welchem System-Prompt, legt die Schule in stackctl fest.
- **Nur im Schulnetz**: Login und Apps laufen unter der Domain der Schule mit echtem Zertifikat, erreichbar nur im Schulnetz. Keine Schülerdaten verlassen das Schulnetz. Direkter Betrieb am Internet ist als erweiterte Option möglich. Siehe [Betriebsarten](docs/betriebsarten.md).
- **Single-Sign-On**: Alle Apps melden sich über den lokalen Dex an. Bis der Anmeldedienst der Schule angebunden ist (z. B. moin.schule, Wobila), gibt es Testkonten für Lehrkräfte und Schüler:innen.

## Installation

Voraussetzungen: Debian 12+ oder Ubuntu 22.04+ (amd64 oder arm64), Docker Engine mit Compose-Plugin, Port 8090 im Schulnetz erreichbar. Der Server braucht eine feste Adresse im Schulnetz, im Router also eine feste IP-Zuweisung: Auf diese Adresse zeigen später Login und Apps.

Auf einem frischen Debian fehlt curl noch. Als root:

```bash
apt update && apt install -y curl ca-certificates
```

Docker installiert das Skript unten auf **Debian 13** selbst aus den Debian-Paketquellen. Auf Debian 12 und Ubuntu vorher Docker von Docker selbst holen, die Pakete dort haben kein Compose-Plugin:

```bash
curl -fsSL https://get.docker.com | sh
```

Dann stackctl installieren (ohne `sudo`, wenn du schon root bist):

```bash
curl -fsSL https://raw.githubusercontent.com/lngstck/stackctl/main/scripts/install.sh | sudo bash
```

Am Ende zeigt das Skript einen Link zur Einrichtung, z. B. `http://192.168.1.10:8090/setup?code=K7M4-Q2XP`. Er enthält den Einrichtungscode: Ohne ihn lässt sich die Einrichtung nicht abschließen, damit nicht irgendwer im Netz zuerst das Admin-Passwort setzt. Den Code neu anzeigen: `sudo stackctl setup-code`.

Eine bestimmte Version installieren, etwa einen Vorab-Stand:

```bash
curl -fsSL https://raw.githubusercontent.com/lngstck/stackctl/main/scripts/install.sh | sudo STACKCTL_VERSION=v0.12.0-rc4 bash
```

Der Assistent fragt nach der Domain der Schule und nach der Betriebsart. Für den Standard „nur im Schulnetz“ braucht es ein kostenloses Konto bei deSEC und zwei NS-Einträge beim DNS-Anbieter der Schule, die die Domain an deSEC übergeben — die [Betriebsarten](docs/betriebsarten.md) beschreiben, was wann nötig ist. Die Wahl fällt einmalig bei der Einrichtung.

Nach der Einrichtung installiert stackctl die Grundversorgung von selbst, das dauert ein paar Minuten. Im Schulnetz braucht das erste Zertifikat danach meist noch 5 bis 10 Minuten; „Zugang“ zeigt so lange „wird geholt“.

## Entwicklung

```bash
# Lokal auf dem Mac
make dev              # startet stackctl web --dev auf :8090
scripts/vorschau.sh   # Oberfläche mit Testdaten ohne Docker auf :8091

# Cross-compile für Linux
make build-all        # erzeugt dist/stackctl-linux-{amd64,arm64}
```

## Lizenz

[AGPL-3.0-or-later](LICENSE)
