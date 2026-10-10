package web

import (
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/lngstck/stackctl/internal/backup"
	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/docker"
	"github.com/lngstck/stackctl/internal/public"
	"github.com/lngstck/stackctl/internal/publish"
)

// startData is the template context for dashboard.html.tmpl, the start
// screen: one sentence on the state, what needs the admin, a few figures,
// then the installed apps on shelves. Details live in one sheet per app.
// Everything comes from fast local sources — no network call.
type startData struct {
	PageData
	Headline string
	Summary  string
	Flash    flash
	Notices  []notice

	Sys        sysView
	LastBackup string // "vor 2 Tg." or "" without a backup
	Backups    int
	Mode       string // config.TransportLocal | config.TransportDirect
	BaseDomain string

	Shelves   []shelf
	Apps      []appSheet // shelf apps, for the list view
	HasApps   bool
	Infra     []appSheet
	InfraDown []string // names of basic services that are not running
	Sheets    []appSheet
	Internet  bool // at least one app is reachable from the internet
	Updates   bool
	Alerts    bool
}

// shelf is one category of installed apps on the start screen.
type shelf struct {
	Title string
	Apps  []appSheet
	Last  bool // carries the "App holen" tile
}

// flash is the short feedback after an action (?msg=…&err=1).
type flash struct {
	Text string
	Err  bool
}

func flashFrom(r *http.Request) flash {
	q := r.URL.Query()
	return flash{Text: q.Get("msg"), Err: q.Get("err") == "1"}
}

// dashIssue is something that needs the admin, as found by the checks
// below. The start screen turns it into a notice with exactly one action.
type dashIssue struct {
	Level       string // "danger" | "warning"
	Icon        string // sprite id
	Title       string
	Detail      string
	Action      string // in-app link
	ActionLabel string
	// AppID ties the issue to an installed app: the notice shows its tile
	// and its title opens the app's sheet.
	AppID string
	// PostTo makes the action a button that posts there (with CSRF token)
	// instead of a link, for one-click fixes such as starting an app.
	PostTo string
}

// notice is the rendering of a hint on the start screen (layout "notice").
type notice struct {
	Tone        string // bad | warn | info
	Icon        string
	Tile        *appTile
	Title       string
	Detail      string
	Sheet       string // dialog id the title (and action) opens
	Href        string
	PostTo      string
	ActionLabel string
	CSRFToken   string
}

// infraDisplayNames gibt den Pflicht-Diensten verständliche Namen für den
// Admin (statt nackter Container-IDs).
var infraDisplayNames = map[string]string{
	"postgres": "Datenbank (PostgreSQL)",
	"dex":      "Anmeldung (Dex)",
	"caddy":    "Reverse-Proxy (Caddy)",
}

// mandatoryAppIDs liefert die Pflicht-Dienste in sinnvoller
// Installations-Reihenfolge (Apps hängen von postgres ab, Logins von dex).
// Der Reverse-Proxy hält Port 80/443 und terminiert TLS — ohne ihn ist keine
// einzige Adresse erreichbar, auch der Login nicht.
func mandatoryAppIDs() []string {
	return []string{"postgres", "dex", "caddy"}
}

// isMandatoryApp meldet, ob die App ein Pflicht-Dienst ist. Einzige Quelle
// der Wahrheit dafür ist mandatoryAppIDs.
func isMandatoryApp(id string) bool {
	for _, m := range mandatoryAppIDs() {
		if m == id {
			return true
		}
	}
	return false
}

// missingInfraDetails erklärt pro Pflicht-Dienst, warum er installiert werden
// muss — der Admin auf einem frisch eingerichteten System kennt weder
// "postgres" noch "dex".
var missingInfraDetails = map[string]string{
	"postgres": "Fast alle Apps speichern ihre Daten darin. Bitte zuerst installieren.",
	"dex":      "Ohne sie meldet sich niemand in den Apps an.",
	"caddy":    "Er holt die Zertifikate und verteilt die Anfragen an die Apps. Ohne ihn ist keine Adresse erreichbar.",
}

// missingInfraIssues liefert Hinweise für Pflicht-Dienste, die noch gar
// nicht installiert sind. Ohne sie landet ein frisch eingerichteter Admin
// auf einem leeren Start und erfährt erst beim Installieren einer App von
// den Abhängigkeiten.
func missingInfraIssues(st *config.State) []dashIssue {
	var issues []dashIssue
	for _, id := range mandatoryAppIDs() {
		if _, installed := st.Containers[id]; installed {
			continue
		}
		issues = append(issues, dashIssue{
			Level:       "danger",
			Icon:        "plus",
			Title:       infraDisplayNames[id] + " noch nicht installiert",
			Detail:      missingInfraDetails[id],
			Action:      "/apps/" + id + "/install",
			ActionLabel: "Jetzt installieren",
		})
	}
	return issues
}

// accountIssues meldet, wenn Dex läuft, sich aber niemand anmelden kann:
// Ohne angebundenen Anmeldedienst sind die Testkonten der einzige Weg hinein.
func accountIssues(st *config.State, cfg *config.Config) []dashIssue {
	if !st.IsInstalled("dex") || len(cfg.Auth.TestAccounts) > 0 {
		return nil
	}
	return []dashIssue{{
		Level:       "warning",
		Icon:        "user",
		Title:       "Noch kann sich niemand anmelden",
		Detail:      "Es ist kein Anmeldedienst der Schule angebunden und es gibt noch keine Testkonten.",
		Action:      "/settings#konten",
		ActionLabel: "Konto anlegen",
	}}
}

// dnsSyncIssues meldet, wenn stackctl den Wildcard-Eintrag bei deSEC nicht
// setzen konnte. Ohne ihn ist keine Adresse erreichbar, auch der Login nicht.
func (s *Server) dnsSyncIssues() []dashIssue {
	msg := s.dnsSync.get()
	if msg == "" {
		return nil
	}
	return []dashIssue{{
		Level:       "danger",
		Icon:        "globe",
		Title:       "DNS-Eintrag bei deSEC fehlt",
		Detail:      msg,
		Action:      "/public",
		ActionLabel: "Zugang prüfen",
	}}
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	s.renderStart(w, r, "")
}

// renderStart renders the start screen, optionally with one app's sheet
// open (/apps/{id} lands here).
func (s *Server) renderStart(w http.ResponseWriter, r *http.Request, openApp string) {
	data := s.buildStart(r)
	if openApp != "" {
		data.OpenSheet = "app-" + openApp
	}
	s.render(w, "dashboard.html.tmpl", data)
}

func (s *Server) buildStart(r *http.Request) startData {
	st := s.snapState()
	data := startData{
		PageData:   s.pageData("dashboard"),
		Flash:      flashFrom(r),
		Sys:        buildSysView(),
		Mode:       s.cfg.Public.Transport,
		BaseDomain: public.BaseDomain(s.cfg),
	}
	data.Search = true

	// Die Prüfungen in fester Reihenfolge: Fehlende Pflicht-Dienste zuerst —
	// auf einem frisch eingerichteten System das Erste, was der Admin tun
	// muss.
	var issues []dashIssue
	issues = append(issues, missingInfraIssues(st)...)
	issues = append(issues, s.dnsSyncIssues()...)
	issues = append(issues, accountIssues(st, s.cfg)...)

	// Die Adresse des Logins — die Lebensader für OIDC. Liegt sie, kann sich
	// niemand mehr mit dem Schulkonto anmelden.
	if s.publisher != nil {
		if status := s.publisher.AuthStatus(); status != publish.StatusRunning {
			issues = append(issues, dashIssue{
				Level:       "danger",
				Icon:        "key",
				Title:       "Anmeldung nicht erreichbar",
				Detail:      "Der Login wird gerade nicht ausgeliefert, niemand kommt in die Apps. Läuft der Reverse-Proxy?",
				Action:      "/public",
				ActionLabel: "Zugang prüfen",
			})
		}
	}

	// Pro installierter App: läuft sie, ist ihre Adresse da, gibt es ein
	// Update? InstalledIDs ist sortiert, damit die Hinweise nicht bei jedem
	// Laden die Plätze tauschen.
	var updates []notice
	for _, id := range st.InstalledIDs() {
		cs := st.Containers[id]
		sheet := s.appSheet(id, cs)

		if !sheet.Running {
			title := sheet.Name + " läuft nicht"
			detail := "Die App ist installiert, aber ausgeschaltet."
			level := "warning"
			if infraName, isInfra := infraDisplayNames[id]; isInfra {
				title = infraName + " läuft nicht"
				detail = "Apps, die diesen Dienst brauchen, funktionieren gerade nicht."
				level = "danger"
			}
			issues = append(issues, dashIssue{
				Level: level, Icon: "power", Title: title, Detail: detail,
				AppID: id, PostTo: "/apps/" + id + "/start", ActionLabel: "Einschalten",
			})
		}

		// Adresse eingeschaltet, Route läuft aber nicht (nur echte Apps).
		if cs.PublicEnabled && !isMandatoryApp(id) && s.publisher != nil {
			if status := s.publisher.Status(id); status != publish.StatusRunning {
				issues = append(issues, dashIssue{
					Level:       "warning",
					Icon:        "globe",
					Title:       "Adresse von " + sheet.Name + " nicht erreichbar",
					Detail:      "Die Adresse ist eingeschaltet, wird aber gerade nicht ausgeliefert. Läuft der Reverse-Proxy?",
					AppID:       id,
					Action:      "/public",
					ActionLabel: "Zugang prüfen",
				})
				sheet.Tile.Alert = true
			}
		}

		if sheet.UpdateTo != "" {
			data.Updates = true
			n := notice{
				Tone:      "warn",
				Tile:      &appTile{Face: sheet.Tile.Face, Size: "m"},
				Title:     fmt.Sprintf("%s %s ist da", sheet.Name, sheet.UpdateTo),
				Sheet:     "app-" + id,
				CSRFToken: data.CSRFToken,
			}
			if sheet.UpdateBreaking {
				n.Detail = "Größeres Update. Es läuft nicht automatisch, vorher sichern."
				n.ActionLabel = "Ansehen"
			} else {
				n.Detail = "Daten und Einstellungen bleiben erhalten."
				n.PostTo = "/apps/" + id + "/update"
				n.ActionLabel = "Aktualisieren"
			}
			updates = append(updates, n)
		}

		data.Sheets = append(data.Sheets, sheet)
		if sheet.IsInfra {
			data.Infra = append(data.Infra, sheet)
			if !sheet.Running {
				data.InfraDown = append(data.InfraDown, sheet.ShortName)
			}
			continue
		}
		data.Apps = append(data.Apps, sheet)
		if sheet.Where == "internet" {
			data.Internet = true
		}
		if sheet.Tile.Alert {
			data.Alerts = true
		}
	}

	for _, is := range issues {
		data.Notices = append(data.Notices, noticeFor(is, data.Sheets, data.CSRFToken))
	}
	// Laufende Vorgänge: wer gerade eine Installation angestoßen hat und
	// zurück auf Start geht, findet den Weg zum Fortschritt.
	for _, a := range s.jobs.recent(maxRetainedJobs) {
		if a.Done {
			continue
		}
		data.Notices = append(data.Notices, notice{
			Tone: "info", Icon: "refresh", Title: a.Title + " …",
			Detail: "Läuft gerade.", Href: "/jobs/" + a.ID, ActionLabel: "Fortschritt ansehen",
		})
	}
	needs := len(data.Notices)
	data.Notices = append(data.Notices, updates...)

	data.Shelves = shelvesFor(data.Apps)
	data.HasApps = len(data.Apps) > 0
	data.Headline, data.Summary = startSentence(needs, data.Apps, len(updates))

	if infos, err := backup.List(); err != nil {
		log.Printf("web: list backups: %v", err)
	} else if len(infos) > 0 {
		data.Backups = len(infos)
		if t, err := time.Parse(time.RFC3339, infos[0].CreatedAt); err == nil {
			data.LastBackup = humanAgo(t)
		}
	}
	return data
}

// noticeFor turns an issue into a notice. An issue about an installed app
// shows that app's tile and opens its sheet.
func noticeFor(is dashIssue, sheets []appSheet, csrf string) notice {
	n := notice{
		Tone:        "warn",
		Icon:        is.Icon,
		Title:       is.Title,
		Detail:      is.Detail,
		Href:        is.Action,
		PostTo:      is.PostTo,
		ActionLabel: is.ActionLabel,
		CSRFToken:   csrf,
	}
	if is.Level == "danger" {
		n.Tone = "bad"
	}
	if n.Icon == "" {
		n.Icon = "alert"
	}
	for _, sh := range sheets {
		if is.AppID != "" && sh.ID == is.AppID {
			n.Tile = &appTile{Face: sh.Tile.Face, Size: "m"}
			n.Sheet = "app-" + sh.ID
		}
	}
	return n
}

// startSentence answers "Is everything fine?" in one sentence, with the
// figures behind it in a second line.
func startSentence(needs int, apps []appSheet, updates int) (string, string) {
	var headline string
	switch {
	case needs == 1:
		headline = "Eine Sache braucht dich"
	case needs > 1:
		headline = fmt.Sprintf("%d Dinge brauchen dich", needs)
	case len(apps) == 0:
		headline = "Bereit für die erste App"
	default:
		headline = "Alles läuft"
	}

	var parts []string
	up := 0
	var off []string
	for _, a := range apps {
		if a.Running {
			up++
		} else {
			off = append(off, a.Name)
		}
	}
	switch {
	case len(apps) == 0:
		parts = append(parts, "Noch keine Apps installiert")
	case up == len(apps) && len(apps) == 1:
		parts = append(parts, "Die App läuft")
	case up == len(apps):
		parts = append(parts, fmt.Sprintf("Alle %d Apps laufen", len(apps)))
	default:
		parts = append(parts, fmt.Sprintf("%d von %d Apps laufen", up, len(apps)))
	}
	switch updates {
	case 0:
	case 1:
		parts = append(parts, "1 Update")
	default:
		parts = append(parts, fmt.Sprintf("%d Updates", updates))
	}
	return headline, strings.Join(parts, " · ")
}

// shelvesFor groups the apps by category, in a fixed order. The last shelf
// carries the tile that leads to the catalog.
func shelvesFor(apps []appSheet) []shelf {
	byTitle := map[string]*shelf{}
	var titles []string
	for _, a := range apps {
		t := categoryLabel(a.Category)
		if byTitle[t] == nil {
			byTitle[t] = &shelf{Title: t}
			titles = append(titles, t)
		}
		byTitle[t].Apps = append(byTitle[t].Apps, a)
	}
	rank := func(t string) int {
		for i, c := range categoryOrder {
			if c == t {
				return i
			}
		}
		return len(categoryOrder)
	}
	sort.SliceStable(titles, func(i, j int) bool {
		ri, rj := rank(titles[i]), rank(titles[j])
		if ri != rj {
			return ri < rj
		}
		return titles[i] < titles[j]
	})
	out := make([]shelf, 0, len(titles))
	for _, t := range titles {
		sh := byTitle[t]
		sort.SliceStable(sh.Apps, func(i, j int) bool {
			return strings.ToLower(sh.Apps[i].Name) < strings.ToLower(sh.Apps[j].Name)
		})
		out = append(out, *sh)
	}
	if len(out) > 0 {
		out[len(out)-1].Last = true
	}
	return out
}

// humanAgo formatiert einen Zeitpunkt als grobe deutsche Relativzeit.
func humanAgo(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "gerade eben"
	case d < time.Hour:
		return fmt.Sprintf("vor %d Min.", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("vor %d Std.", int(d.Hours()))
	default:
		return fmt.Sprintf("vor %d Tg.", int(d.Hours()/24))
	}
}

// appRunning reports whether an app's container is up. A variable so tests
// can answer without Docker.
var appRunning = func(id string) bool { return docker.IsRunning("ls-" + id) }
