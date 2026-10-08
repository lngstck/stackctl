# Betriebsarten — wo Login und Apps erreichbar sind

Leitsatz: **Keine Schülerdaten verlassen das Schulnetz.** stackctl kennt deshalb
genau zwei Betriebsarten, und die übliche ist die, in der von außen niemand
herankommt.

Die Betriebsart wird im Einrichtungsassistenten gewählt. Dieses Dokument
erklärt, was dahintersteckt, was die Schule vorbereiten muss und woran man
erkennt, dass es funktioniert.

---

## Die Wahl in einem Satz

|  | Erreichbar | Vorzubereiten | Zertifikat |
|---|---|---|---|
| **Nur im Schulnetz** (Standard) | im Schulnetz | Wildcard-DNS auf die LAN-IP, deSEC-Konto, ein CNAME | ein Wildcard-Zertifikat über DNS-01 |
| **Direkter Betrieb** (erweitert) | aus dem Internet | Wildcard-DNS auf die öffentliche IP, Port 80/443 offen | je Adresse eins über HTTP-01 |

Gemeinsam ist beiden:

- **Eine Domain, alles darunter.** Die Schule wählt bei der Einrichtung eine
  Domain, z. B. `apps.gymnasium-musterstadt.de`. Daraus folgt fest:
  - `auth.<domain>`: die Anmeldung (lokaler Dex)
  - `<app>.<domain>`: jede App, z. B. `pylearn.<domain>`
  - `admin.<domain>`: diese Oberfläche, nur wenn eingeschaltet
- **Caddy** hält Port 80/443 auf dem Server, terminiert TLS und verteilt die
  Anfragen anhand des Hostnamens an die Apps. Er ist Pflichtdienst.

---

## 1. Nur im Schulnetz

Login und Apps sind im Schulnetz erreichbar, mit echtem Zertifikat — von
außen nicht.

**Warum das ohne eingehende Verbindungen funktioniert:**

- Das Zertifikat bindet an einen *Namen*, nicht an eine IP. Bei DNS-01
  verbindet sich die Zertifizierungsstelle nie mit dem Server; sie prüft nur
  einen TXT-Eintrag im DNS.
- Der Rückweg vom Anmeldedienst (moin.schule, Wobila …) ist ein
  **Browser-Redirect**. Der Anmeldedienst muss `auth.<domain>` nie selbst
  erreichen. Token-Tausch und Schlüsselabruf gehen vom Server **ausgehend**.
- Der Browser zeigt bei *Hostname → private IP* keinen Warndialog
  (gemessen mit Chrome; nur IP-Literale und `.local` lösen ihn aus).

### Vorbereitung

1. **deSEC-Konto** (kostenlos, [desec.io](https://desec.io)): dort eine Domain
   wie `meine-schule.dedyn.io` anlegen und einen API-Token erzeugen. deSEC
   hat die DNS-API, die der Anbieter der Schuldomain meist nicht hat.
2. **Zwei Einträge beim DNS-Anbieter der Schuldomain:**

   ```
   *.apps.gymnasium-musterstadt.de.                 A      192.168.10.5
   _acme-challenge.apps.gymnasium-musterstadt.de.   CNAME  _acme-challenge.meine-schule.dedyn.io.
   ```

   - Der Wildcard zeigt auf die **LAN-Adresse** dieses Servers.
   - Der CNAME lenkt die Zertifikatsprüfung zu deSEC um. Den TXT-Eintrag
     dort schreibt Caddy selbst, mit dem Token.
   - Manche Anbieter nehmen Einträge mit Unterstrich nur über den Import
     einer Zonendatei an.
3. Token und CNAME-Ziel im Assistenten eintragen. Der Assistent zeigt die
   beiden Einträge passend zur eingegebenen Domain an.

Verwaltet deSEC die Schuldomain selbst, entfällt der CNAME: dann das Feld
„Ziel bei deSEC“ leer lassen.

### DNS-Rebind-Schutz

Viele Router und DNS-Server im Schulnetz (FRITZ!Box, pfSense/OPNsense mit
Unbound, manche Firewalls) verwerfen Antworten, in denen ein öffentlicher
Name auf eine private Adresse zeigt. Dann löst die Domain im Internet auf,
im Schulnetz aber nicht.

- Der Assistent erkennt das: Er fragt zusätzlich einen öffentlichen
  DNS-Server (Quad9) nach einem Zufallsnamen unter der Domain.
- Abhilfe: im Router eine Ausnahme für die Domain eintragen, bei einer
  FRITZ!Box unter *Heimnetz → Netzwerk → Netzwerkeinstellungen*.
- Das betrifft auch den Server selbst: Die Apps prüfen die Anmeldung über
  `auth.<domain>` und müssen den Namen genauso auflösen.

### Zugang von außen

Gehört nicht zu stackctl und wird nicht verbaut. Wer ihn braucht, richtet ihn
selbst ein (VPN oder eigener Tunnel). Dann muss `auth.<domain>` mit nach außen,
sonst scheitert jeder Login von außen.

---

## 2. Direkter Betrieb (erweitert)

Der Server nimmt den Verkehr aus dem Internet selbst entgegen. Caddy holt und
erneuert die Zertifikate über Let's Encrypt (HTTP-01).

Dauerhafte Voraussetzungen — nicht nur bei der Einrichtung:

- `*.<domain>` zeigt im DNS auf die öffentliche IP dieses Servers.
- **Port 80 ist aus dem Internet erreichbar.** Darüber läuft die
  ACME-Challenge. Wird er später dichtgemacht, scheitert die Erneuerung still,
  bis das Zertifikat abläuft.
- **Port 443 ist aus dem Internet erreichbar.**
- Kein anderer Webserver auf 80/443 (mitgelieferter Apache oder nginx).

Alle übrigen Container binden in dieser Betriebsart auf `127.0.0.1`: Nach
außen sichtbar wird eine App ausschließlich über Caddy, nie über einen
offenen Port. Eine App bekommt ihre Adresse erst, wenn die Admin sie
einschaltet.

**Passend, wenn** der Server als VM mit fester IP im Rechenzentrum steht und
die Apps bewusst aus dem Internet erreichbar sein sollen.

---

## Warum die Wahl früh fällt — und bleibt

Die Domain ist kein Anzeigename. Sie steckt

- im OIDC-Issuer des lokalen Dex (Browser und Container müssen denselben sehen),
- in der Redirect-URI, die beim Anmeldedienst der Schule eingetragen ist,
- in der Konfiguration jeder installierten App.

Ein Wechsel ist deshalb ein Umzug, kein Umschalten. stackctl unterstützt ihn
derzeit **nicht**; wer die Domain ändern muss, setzt neu auf.

---

## Was die Prüfung im Assistenten aussagt

Der Knopf *Voraussetzungen prüfen* fragt ab, was von hier aus sichtbar ist:

| | Bedeutung |
|---|---|
| **grün** | bestätigt in Ordnung |
| **gelb** | **nicht bestätigbar** — nicht dasselbe wie kaputt |
| **rot** | bestätigt kaputt |

Geprüft wird im Schulnetz-Betrieb: Wildcard (mit Zufallsnamen, damit ein
einzelner Eintrag für `auth.` nicht als Wildcard durchgeht), Ziel des
Wildcards, Rebind-Schutz, CNAME für `_acme-challenge`, deSEC-Token (eine
Anfrage an desec.io) und ob Port 80/443 frei sind.

**Keine Prüfung blockiert die Einrichtung** — außer einem fehlenden
deSEC-Token im Schulnetz-Betrieb: ohne ihn kann es nie ein Zertifikat geben.
DNS-Einträge dürfen dagegen nach der Einrichtung entstehen.

---

## Nach der Einrichtung: die Seite *Zugang*

Unter `/public` steht dieselbe Frage im Präsens: Der Assistent beantwortet
*„kann das funktionieren?“*, diese Seite *„funktioniert es noch?“*.

- **Erreichbarkeit** — dieselbe Anfrage, die ein Browser stellen würde, an
  die Login-Adresse.
- **Zertifikat** — gelesen aus dem TLS-Handshake. Gewarnt wird ab **14 Tagen
  Restlaufzeit**: Caddy erneuert rund 30 Tage vorher, wer darunter landet,
  hat ein bestehendes Problem.
- **DNS** — Wildcard, Ziel und im Schulnetz-Betrieb der CNAME.

Token und Ziel bei deSEC lassen sich in den *Einstellungen* ändern.

---

## Adressen in den Apps

Keine App baut eine Adresse selbst zusammen. stackctl schreibt sie beim
Installieren **und bei jedem Update** in die `.env`:

```
PYLEARN_PUBLIC_URL=https://pylearn.apps.gymnasium-musterstadt.de
PYLEARN_OIDC_REDIRECT_URI=https://pylearn.apps.gymnasium-musterstadt.de/auth/callback
```

Eine Definition, die ihre Redirect-URI selbst zusammensetzt, wird bei der
Installation mit einer Fehlermeldung abgewiesen, die beide Adressen nennt.

Im Schulnetz-Betrieb bekommt jede neu installierte App ihre Adresse sofort.

---

## Störungssuche

| Symptom | Wahrscheinliche Ursache | Was hilft |
|---|---|---|
| Im Schulnetz löst `auth.<domain>` nicht auf, im Mobilfunk schon | DNS-Rebind-Schutz im Router | Ausnahme für die Domain eintragen |
| Alles außer `auth.` ist unerreichbar | Einzelner DNS-Eintrag statt Wildcard | Wildcard-Eintrag anlegen |
| Zertifikatsfehler im Schulnetz-Betrieb | Token ungültig oder CNAME fehlt/falsch | Seite *Zugang*, Einstellungen → Zertifikat |
| Zertifikat läuft ab (direkter Betrieb) | Port 80 nachträglich zugemacht | Port 80 wieder öffnen |
| Caddy startet nicht | Port 80/443 durch Apache/nginx belegt | anderen Webserver stoppen |
| „Unbekannte Adresse“ | App nicht installiert oder Adresse abgeschaltet | App-Seite → Adresse einschalten |

Nach einer Wiederherstellung aus dem Backup: `/opt/learningstack/caddy/data`
enthält Zertifikate und ACME-Konto. Fehlt das Verzeichnis, holt Caddy alles
neu — Let's Encrypt erlaubt davon fünf pro Woche und Domain.

---

## Grenzen

- **Domain oder Betriebsart nach der Einrichtung wechseln** geht nicht.
- **Nur deSEC** als DNS-API für DNS-01. Andere Anbieter brauchen ein anderes
  Caddy-Modul.
- **Die stackctl-Oberfläche** ist immer auf Port 8090 im Schulnetz
  erreichbar. Unter `admin.<domain>` lässt sie sich zusätzlich einschalten;
  im direkten Betrieb heißt das: aus dem Internet. Das ist standardmäßig aus
  und will überlegt sein — die Oberfläche installiert Apps, zeigt Passwörter
  und spielt Backups zurück, geschützt nur durch das Admin-Passwort.
