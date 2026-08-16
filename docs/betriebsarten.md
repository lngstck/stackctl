# Betriebsarten — wie die Apps ins Internet kommen

Ein Schulserver steht selten so im Netz, wie es die Anleitung gern hätte. Mal
hängt er hinter einem Anschluss ohne feste IP, mal soll er unbedingt unter der
Domain der Schule erreichbar sein, mal steht er als VM im Rechenzentrum und
könnte den Verkehr problemlos selbst annehmen. stackctl bildet diese drei Fälle
als **Betriebsarten** ab.

Die Betriebsart wird im Einrichtungsassistenten gewählt. Dieses Dokument
erklärt, was dahintersteckt, was die Schule jeweils vorbereiten muss und woran
man erkennt, dass es funktioniert.

---

## Die Wahl in einem Satz

|  | Adresse der Apps | Vorzubereiten | Wer stellt die Zertifikate |
|---|---|---|---|
| **Adresse des Betreibers** | `app.schulkuerzel.learningstack.online` | nichts | Betreiber |
| **Eigene Domain über den Betreiber** | `app.ihre-domain.de` | Wildcard-DNS auf den Relay | Betreiber |
| **Direkter Betrieb** | `app.ihre-domain.de` | Wildcard-DNS hierher + Port 80/443 | dieser Server (Let's Encrypt) |

Intern sind das keine drei Sonderfälle, sondern zwei unabhängige Angaben:
**wie** der Verkehr ankommt (`transport`: über den Relay oder direkt) und
**unter welcher Domain** (`base_domain`). Die dritte Zeile der Tabelle
unterscheidet sich von der zweiten nur im Transport, die zweite von der ersten
nur in der Domain.

---

## 1. Adresse des Betreibers

Der Server baut eine ausgehende SSH-Verbindung zum Relay des Betreibers auf und
bekommt von dort seinen Verkehr zugestellt. Nach außen sichtbar ist nur der
Relay.

- **Der Server braucht keine öffentliche IP und keine Portfreigabe.** Das ist der
  Grund, warum diese Betriebsart hinter jedem Schulanschluss funktioniert, auch
  hinter DS-Lite oder wechselnden IP-Adressen.
- DNS, TLS-Zertifikate und deren Erneuerung liegen vollständig beim Betreiber.
- Die Adresse enthält das Schulkürzel: `pylearn.gymnasium-musterstadt.learningstack.online`.

Zu prüfen ist hier nichts — der Assistent zeigt in dieser Betriebsart nur den
Hinweis, dass der Betreiber das übernimmt.

**Passend, wenn** der Server im Schulnetz steht und niemand Lust auf DNS hat.

## 2. Eigene Domain über den Betreiber

Technisch dasselbe wie oben — derselbe Tunnel, dieselbe Mechanik —, aber die
Apps liegen unter der Domain der Schule.

Der entscheidende Punkt, an dem diese Betriebsart erfahrungsgemäß scheitert:

> Der Wildcard-Eintrag muss auf den **Relay des Betreibers** zeigen, **nicht auf
> diesen Server.**

Der Verkehr kommt am Relay an und wird von dort durch den Tunnel gereicht. Ein
Wildcard, der auf die Schul-IP zeigt, löst sauber auf und funktioniert trotzdem
nie. Der Assistent prüft deshalb nicht nur, *ob* der Eintrag auflöst, sondern
ob er auf den Relay zeigt.

Zusätzlich muss der Betreiber die fremde Domain am Relay freigeben (siehe
"Grenzen" unten) — das lässt sich nicht allein von der Schule aus erledigen.

**Passend, wenn** die Adresse nach Schule aussehen soll, der Anschluss aber
keinen direkten Betrieb hergibt.

## 3. Direkter Betrieb

Der Server nimmt den Verkehr selbst entgegen. stackctl installiert dafür einen
Reverse-Proxy (Caddy) als Pflichtdienst, der Port 80 und 443 hält, TLS
terminiert und anhand des Hostnamens an die richtige App weiterleitet. Die
Zertifikate holt und erneuert Caddy selbst über Let's Encrypt (HTTP-01).

Dauerhafte Voraussetzungen — nicht nur bei der Einrichtung:

- `*.ihre-domain.de` zeigt im DNS auf die öffentliche IP dieses Servers.
- **Port 80 ist aus dem Internet erreichbar.** Auch nach der Einrichtung: über
  Port 80 läuft die ACME-Challenge. Wird er später dichtgemacht, scheitert die
  Erneuerung still, bis das Zertifikat abläuft — dann steht alles.
- **Port 443 ist aus dem Internet erreichbar.** Darüber läuft der eigentliche
  Zugriff.
- Auf dem Server läuft kein anderer Webserver auf 80/443. Ein von der
  Distribution mitgelieferter Apache oder nginx muss weg oder umziehen.

Kein Tunnel, kein SSH-Key, keine Abhängigkeit vom Relay im laufenden Betrieb.
Der Betreiber wird nur einmal für den zentralen Login gebraucht.

**Passend, wenn** der Server als VM mit fester IP im Rechenzentrum steht.

---

## Warum die Wahl früh fällt — und bleibt

Die öffentliche Adresse ist kein Anzeigename. Sie steckt

- im OIDC-Issuer des lokalen Dex (Browser und Container müssen denselben sehen),
- in der Redirect-URI, die beim zentralen Dex des Betreibers registriert ist,
- in der Konfiguration jeder installierten App.

Ein Wechsel der Betriebsart ist deshalb kein Umschalten, sondern ein Umzug: alle
drei müssen gleichzeitig mitziehen. stackctl unterstützt das derzeit **nicht** —
die Betriebsart wird bei der Einrichtung einmal entschieden. Wer sie doch ändern
muss, setzt neu auf.

Ein Sonderfall ist harmlos: Wer sich zwischen den beiden **Relay**-Varianten
bewegt, ändert nur die Domain, nicht den Transport. Auch das erfordert aber eine
neue Registrierung beim Betreiber, weil die Redirect-URI dort hinterlegt ist.

---

## Wildcard-DNS einrichten

Für Betriebsart 2 und 3 braucht es genau einen Eintrag in der Zone der
Schuldomain:

```
*.ls.gymnasium-musterstadt.de.   A   203.0.113.10
```

- **Ziel:** bei Betriebsart 2 die Adresse des Relays, bei Betriebsart 3 die
  öffentliche IP dieses Servers.
- **Wildcard, kein Einzeleintrag.** Jede App bekommt ihre eigene Subdomain, und
  zwar erst dann, wenn sie installiert wird. Ein einzelner Eintrag für `auth.`
  lässt den Login funktionieren und jede später veröffentlichte App ins Leere
  laufen — deshalb prüft der Assistent mit einem zufälligen Namen, den niemand
  von Hand angelegt haben kann.
- **Eigene Subdomain empfohlen.** `*.ls.schule.de` statt `*.schule.de` lässt die
  vorhandene Website in Ruhe. Der Assistent akzeptiert beides.
- Frisch angelegte Einträge brauchen je nach Anbieter einige Minuten. Die Prüfung
  lässt sich beliebig oft wiederholen.

Ein IPv6-Anschluss braucht zusätzlich einen `AAAA`-Wildcard. Die Prüfung
akzeptiert beide Rekordtypen.

---

## Was die Prüfung im Assistenten aussagt

Der Knopf *Voraussetzungen prüfen* fragt nur ab, was von außen sichtbar ist. Er
liefert drei Sorten Ergebnis, und der Unterschied zwischen den letzten beiden
ist wichtig:

| | Bedeutung |
|---|---|
| **grün** | bestätigt in Ordnung |
| **gelb** | **nicht bestätigbar** — nicht dasselbe wie kaputt |
| **rot** | bestätigt kaputt |

Gelb ist der ehrliche Zustand für alles, was der Server über sich selbst nicht
sicher weiß. Hinter NAT kennt er seine eigene öffentliche IP nicht, kann den
Wildcard-Eintrag also nicht mit sich selbst abgleichen. Ohne Rechte auf Port 80
heißt "unbekannt" eben nicht "belegt". In beiden Fällen wäre eine rote Meldung
schlicht gelogen.

**Keine Prüfung blockiert die Einrichtung.** DNS propagiert nach eigenem
Zeitplan, und ein Setup, das sich hinter einem noch nicht sichtbaren Eintrag
verriegelt, hilft niemandem. Die Prüfung ist eine Auskunft, keine Schranke.

---

## Nach der Einrichtung: die Seite *Öffentlicher Zugang*

Unter `/public` steht dieselbe Frage noch einmal, aber im Präsens: Der Assistent
beantwortet *"kann das funktionieren?"*, diese Seite *"funktioniert es noch?"*.
DNS wird nachträglich editiert, Zertifikate laufen ab, ein Relay-Endpunkt zieht
um.

Die Karte **Zustand** prüft bei jedem Aufruf der Seite (und auf Knopfdruck):

- **Erreichbarkeit** — dieselbe Anfrage, die auch ein Browser stellen würde, an
  die Login-Adresse. Sie ist die eine Adresse, die jede Installation hat.
- **Zertifikat** — gelesen aus dem TLS-Handshake, nicht aus einer Datei auf der
  Platte. Entscheidend ist, was tatsächlich ausgeliefert wird; im Relay-Betrieb
  liegt das Zertifikat ohnehin beim Betreiber. Gewarnt wird ab **14 Tagen
  Restlaufzeit**: Caddy erneuert rund 30 Tage vorher, wer also unter 14 Tagen
  landet, hat kein baldiges Problem, sondern bereits ein bestehendes.
- **Wildcard-DNS** — außer bei der Betriebsart des Betreibers, wo die Schule
  daran nichts kaputtmachen kann.

Ist der Endpunkt unten, warnt die Zertifikatskarte nur, statt einen zweiten
Fehler zu melden: eine Ursache, ein Alarm.

Im Relay-Betrieb zeigt die Seite zusätzlich den SSH-Key und einen
Verbindungstest zum Relay.

---

## Adressen in den Apps

Keine App und keine Katalog-Definition baut eine öffentliche Adresse selbst
zusammen. stackctl schreibt sie beim Installieren **und bei jedem Update** in die
`.env`:

```
PYLEARN_PUBLIC_URL=https://pylearn.ls.gymnasium-musterstadt.de
PYLEARN_OIDC_REDIRECT_URI=https://pylearn.ls.gymnasium-musterstadt.de/auth/callback
```

Die Redirect-URI muss zeichengenau mit der übereinstimmen, die bei Dex
registriert ist. Solange es dafür zwei Quellen gab — eine berechnete und eine in
der Katalog-Definition ausgeschriebene —, stimmten sie genau so lange überein,
wie jede Schule unter der Domain des Betreibers lag. Jetzt gibt es eine Quelle,
und eine Definition, die ihre Redirect-URI weiterhin selbst zusammensetzt, wird
bei der Installation mit einer Fehlermeldung abgewiesen, die beide Adressen
nennt.

---

## Störungssuche

| Symptom | Wahrscheinliche Ursache | Was hilft |
|---|---|---|
| Login zeigt "cannot find connection for host" | Der Tunnel ist nicht (mehr) verbunden | `/public` → Verbindungstest; Dienst-Log ansehen |
| Alles außer `auth.` ist unerreichbar | Einzelner DNS-Eintrag statt Wildcard | Wildcard-Eintrag anlegen |
| DNS löst auf, es kommt trotzdem nichts an | Betriebsart 2: Wildcard zeigt auf den Schulserver statt auf den Relay | Ziel des Eintrags korrigieren |
| Zertifikat läuft ab, Erneuerung schlägt fehl | Betriebsart 3: Port 80 nachträglich zugemacht | Port 80 wieder öffnen |
| Caddy startet nicht | Port 80/443 durch Apache/nginx belegt | Anderen Webserver stoppen und deaktivieren |
| Login schlägt nach Umzug fehl | Redirect-URI beim Betreiber zeigt noch auf die alte Adresse | Neu registrieren |

Nach einer Wiederherstellung aus dem Backup gilt für den direkten Betrieb
zusätzlich: `/opt/learningstack/caddy/data` enthält Zertifikate und
ACME-Konto. Fehlt das Verzeichnis, holt Caddy alles neu — Let's Encrypt erlaubt
davon fünf pro Woche und Domain.

---

## Grenzen

- **Betriebsart nach der Einrichtung wechseln** geht nicht (siehe oben).
- **Eigene Domain am Relay** setzt eine Freigabe auf Betreiberseite voraus. Der
  Relay prüft beim Verbinden per DNS, ob der Tunnel den angefragten Namen führen
  darf; für fremde Domains ist dieser Ablauf noch nicht automatisiert.
- **Die stackctl-Oberfläche selbst** bleibt in allen Betriebsarten auf Port 8090
  im lokalen Netz. Sie liegt bewusst nicht hinter dem öffentlichen Zugang — wäre
  sie es, würde ein Fehler in der Adresskonfiguration genau das Werkzeug
  aussperren, mit dem man ihn beheben müsste.
- **Alle übrigen Container** binden auf `127.0.0.1`. Nach außen sichtbar wird
  eine App ausschließlich über den öffentlichen Zugang, nie durch einen offenen
  Port.
