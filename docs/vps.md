# learningstack auf einem Server im Internet (VPS)

Für den Fall, dass learningstack nicht im Schulnetz steht, sondern auf einem
gemieteten Server mit eigener öffentlicher IP. Login und Apps sind dann aus
dem Internet erreichbar, die Verwaltung (stackctl) nicht.

> **Datenschutz:** Die Daten liegen dann beim Hoster, nicht im Schulnetz. Zum
> Ausprobieren mit Testkonten ist das kein Problem. Für echte Schülerdaten
> braucht die Schule einen Vertrag zur Auftragsverarbeitung (AVV) mit dem
> Hoster.

## Was du brauchst

- **Server:** Debian 13, **amd64** (x86). Auf ARM-Servern laufen mehrere Apps
  nicht. Richtwert: 2 CPU-Kerne, 4 GB RAM, 40 GB Platte.
- **SSH-Zugang** als root oder mit sudo.
- **Eine Domain oder Subdomain**, bei der du DNS-Einträge setzen kannst, z. B.
  `ls.gymnasium-musterstadt.de`. Darunter liegen später `auth.…` und die Apps.

## 1. DNS-Eintrag setzen

Beim DNS-Anbieter der Domain einen Wildcard-Eintrag auf die IPv4 des Servers:

```
*.ls.gymnasium-musterstadt.de.   A   203.0.113.5
```

- **Nur A, kein AAAA.** Der Webzugang lauscht nur auf IPv4. Mit AAAA-Eintrag
  scheitern Zertifikate und Aufrufe über IPv6.
- Prüfen: `dig +short test.ls.gymnasium-musterstadt.de` zeigt die IP.
- Im direkten Betrieb setzt stackctl keine DNS-Einträge, das bleibt bei dir.

## 2. Firewall

Hat der Hoster eine Firewall, dort öffnen:

| Port | Wofür |
|---|---|
| 22 | SSH |
| 80 | Zertifikate von Let's Encrypt, **dauerhaft** offen lassen |
| 443 | Login und Apps |

Port 8090 (Verwaltung) muss nicht offen sein: Aus dem Internet nimmt stackctl
dort ohnehin nichts an.

## 3. Installieren

Auf dem Server als root:

```bash
apt update && apt install -y curl ca-certificates
```

```bash
curl -fsSL https://raw.githubusercontent.com/lngstck/stackctl/main/scripts/install.sh | STACKCTL_VERSION=v0.12.0-rc6 bash
```

- Die Version muss angegeben sein. Ohne sie kommt eine alte Version.
- Docker holt das Skript auf Debian 13 selbst.
- Am Ende zeigt es zwei Dinge: den **Befehl für den SSH-Tunnel** und den
  **Einrichtungslink** mit `localhost`.

## 4. Einrichten über den SSH-Tunnel

1. **Auf deinem eigenen Rechner** ein Terminal öffnen und offen lassen:
   ```bash
   ssh -L 8090:localhost:8090 root@203.0.113.5
   ```
2. Den Einrichtungslink aus der Ausgabe öffnen:
   `http://localhost:8090/setup?code=…`
3. Im Assistenten:
   - Betriebsart **Direkter Betrieb**
   - Domain `ls.gymnasium-musterstadt.de` (ohne `*.` und ohne `https://`)
   - eine E-Mail-Adresse, an die Let's Encrypt Zertifikats-Warnungen schickt
4. *Voraussetzungen prüfen*: Gelb bei Port 80/443 heißt nur „nicht prüfbar“
   und ist kein Fehler.
5. Danach richtet stackctl die Grundversorgung ein (Datenbank, Anmeldung,
   Webzugang). Die Zertifikate holt der Webzugang je Adresse selbst, meist
   innerhalb von ein, zwei Minuten.

## 5. Apps freischalten

- App im Katalog installieren.
- Unter **Zugang** bei der App auf **Einschalten**. Erst dann ist sie unter
  `https://<app>.ls.gymnasium-musterstadt.de` erreichbar.
- Testkonten für Lehrkräfte und Schüler:innen: **Einstellungen → Anmeldung**.
  Die Anmeldeseite ist aus dem Internet erreichbar, also nur starke
  Passwörter vergeben.

## Verwaltung später

- Immer über den Tunnel: `ssh -L 8090:localhost:8090 root@<IP>`, dann
  `http://localhost:8090`.
- Wer `http://<IP>:8090` aus dem Internet aufruft, sieht nur eine Seite mit
  genau diesem Tunnel-Befehl, keine Anmeldung.
- Unter **Zugang** lässt sich `https://admin.<domain>` einschalten: dann
  verschlüsselt ohne Tunnel, aber aus dem ganzen Internet erreichbar und nur
  durch das Admin-Passwort geschützt. Für einen Test besser aus lassen.

## Wenn etwas klemmt

| Symptom | Ursache | Was hilft |
|---|---|---|
| Erklärseite statt Verwaltung | Aufruf über die IP statt über den Tunnel | Tunnel öffnen, `http://localhost:8090` |
| Kein Zertifikat, Browser warnt | DNS zeigt noch nicht auf den Server, oder Port 80 zu | `dig` prüfen, Firewall prüfen, dann unter *Zugang* nachsehen |
| „Unbekannte Adresse“ | App-Adresse nicht eingeschaltet | *Zugang* → bei der App *Einschalten* |
| Caddy startet nicht | Ein anderer Webserver belegt 80/443 | `apache2`/`nginx` stoppen und deaktivieren |

Hintergrund zu beiden Betriebsarten: [Betriebsarten](betriebsarten.md).
