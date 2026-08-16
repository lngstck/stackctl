package web

import (
	"fmt"
	"net/http"
	"time"

	"github.com/lngstck/stackctl/internal/catalog"
	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/docker"
	"github.com/lngstck/stackctl/internal/publish"
)

// dashboardData is the template context for dashboard.html.tmpl. Das Dashboard
// ist bewusst KEINE App-Liste mehr (das ist /apps), sondern eine
// Statusübersicht: Was läuft nicht? Was muss aktualisiert werden? Wie steht
// das System da? Alle Felder werden aus vorhandenen, schnellen Read-Quellen
// aggregiert — kein Netz-Call (stackctl-Selbstupdate-Check bleibt im
// System-Tab, weil er einen GitHub-Roundtrip braucht).
type dashboardData struct {
	PageData
	HasApps     bool
	AppsTotal   int
	AppsRunning int
	Issues      []dashIssue
	Updates     []dashUpdate
	Sys         sysView
	Activity    []dashActivity
}

// dashIssue ist eine Zeile im "Handlungsbedarf"-Bereich.
type dashIssue struct {
	Level       string // "danger" | "warning"
	Icon        string
	Title       string
	Detail      string
	Action      string // In-App-Link
	ActionLabel string
}

// dashUpdate ist eine verfügbare App-Aktualisierung (aus dem lokalen
// Katalog-Cache, ohne Netz).
type dashUpdate struct {
	ID       string
	Name     string
	From     string
	To       string
	Breaking bool
}

// dashActivity ist eine Zeile in "Letzte Aktivität" (flüchtige In-RAM-Jobs).
type dashActivity struct {
	Title string
	State string // status-dot Modifier: ok | warn | danger
	Ago   string
}

// infraDisplayNames gibt den Pflicht-Diensten verständliche Namen für den
// Admin (statt nackter Container-IDs).
var infraDisplayNames = map[string]string{
	"postgres": "Datenbank (PostgreSQL)",
	"dex":      "Anmeldung (Dex)",
}

// mandatoryAppIDs sind die Pflicht-Dienste in sinnvoller
// Installations-Reihenfolge (Apps hängen von postgres ab, Logins von dex).
var mandatoryAppIDs = []string{"postgres", "dex"}

// isMandatoryApp meldet, ob die App ein Pflicht-Dienst ist. Einzige Quelle
// der Wahrheit dafür ist infraDisplayNames.
func isMandatoryApp(id string) bool {
	_, ok := infraDisplayNames[id]
	return ok
}

// missingInfraDetails erklärt pro Pflicht-Dienst, warum er installiert werden
// muss — der Admin auf einem frisch freigeschalteten System kennt weder
// "postgres" noch "dex".
var missingInfraDetails = map[string]string{
	"postgres": "Pflicht-Dienst — fast alle Apps brauchen die Datenbank. Bitte zuerst installieren.",
	"dex":      "Pflicht-Dienst — ohne ihn funktioniert kein Login über moin.schule.",
}

// missingInfraIssues liefert Handlungsbedarf-Karten für Pflicht-Dienste, die
// noch gar nicht installiert sind. Ohne diesen Hinweis landet ein frisch
// freigeschalteter Admin auf einem leeren Dashboard und erfährt erst beim
// Installieren einer App von den Abhängigkeiten.
func missingInfraIssues(st *config.State) []dashIssue {
	var issues []dashIssue
	for _, id := range mandatoryAppIDs {
		if _, installed := st.Containers[id]; installed {
			continue
		}
		issues = append(issues, dashIssue{
			Level:       "danger",
			Icon:        "⚠",
			Title:       infraDisplayNames[id] + " noch nicht installiert",
			Detail:      missingInfraDetails[id],
			Action:      "/apps/" + id + "/install",
			ActionLabel: "Jetzt installieren",
		})
	}
	return issues
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	st := s.snapState()
	data := dashboardData{
		PageData: s.pageData("dashboard"),
		Sys:      buildSysView(),
	}

	// 0) Fehlende Pflicht-Dienste — auf einem frisch freigeschalteten System
	//    das Erste, was der Admin tun muss. Steht deshalb ganz oben.
	data.Issues = append(data.Issues, missingInfraIssues(st)...)

	// 1) Dex-Tunnel — die Lebensader für den OIDC-Login. Liegt er, kann sich
	//    niemand mehr über moin.schule anmelden → höchste Priorität.
	if s.publisher != nil {
		if status := s.publisher.AuthStatus(); status != publish.StatusRunning {
			data.Issues = append(data.Issues, dashIssue{
				Level:       "danger",
				Icon:        "⚠",
				Title:       "Anmeldung nicht erreichbar",
				Detail:      "Der Dex-Tunnel ist nicht aktiv — Logins über moin.schule funktionieren nicht.",
				Action:      "/tunnel",
				ActionLabel: "Tunnel prüfen",
			})
		}
	}

	// 2) Pro installierter App: Health (läuft?), Tunnel-Status, Update.
	for id, cs := range st.Containers {
		data.AppsTotal++
		running := docker.IsRunning("ls-" + id)
		if running {
			data.AppsRunning++
		}
		name := cs.Name
		if name == "" {
			name = id
		}

		if !running {
			if infraName, isInfra := infraDisplayNames[id]; isInfra {
				data.Issues = append(data.Issues, dashIssue{
					Level:       "danger",
					Icon:        "⚠",
					Title:       infraName + " läuft nicht",
					Detail:      "Ein Pflicht-Dienst ist gestoppt — abhängige Apps funktionieren ohne ihn nicht.",
					Action:      "/apps",
					ActionLabel: "Zu den Apps",
				})
			} else {
				data.Issues = append(data.Issues, dashIssue{
					Level:       "warning",
					Icon:        "●",
					Title:       name + " läuft nicht",
					Detail:      "Die App ist installiert, aber der Container ist gestoppt.",
					Action:      "/apps/" + id,
					ActionLabel: "Öffnen",
				})
			}
		}

		// Tunnel aktiviert, läuft aber nicht (nur echte Apps, keine Infra).
		if cs.PublicEnabled && id != "dex" && id != "postgres" && s.publisher != nil {
			if status := s.publisher.Status(id); status != publish.StatusRunning {
				data.Issues = append(data.Issues, dashIssue{
					Level:       "warning",
					Icon:        "●",
					Title:       "Externer Zugang für " + name + " inaktiv",
					Detail:      "Der Tunnel ist aktiviert, läuft aber gerade nicht.",
					Action:      "/tunnel",
					ActionLabel: "Tunnel prüfen",
				})
			}
		}

		// Update-Verfügbarkeit aus dem gecachten Katalog (lokal, kein Netz).
		if def, err := catalog.LoadDefinition(id); err == nil {
			if catalog.HasUpdate(cs.VersionInstalled, def.Version) {
				data.Updates = append(data.Updates, dashUpdate{
					ID:       id,
					Name:     name,
					From:     cs.VersionInstalled,
					To:       def.Version,
					Breaking: def.Breaking,
				})
			}
		}
	}
	data.HasApps = data.AppsTotal > 0

	// 3) Letzte Aktivität — flüchtige In-RAM-Jobs (Install/Update/Selfupdate).
	for _, a := range s.jobs.recent(5) {
		data.Activity = append(data.Activity, dashActivity{
			Title: a.Title,
			State: a.dotState(),
			Ago:   humanAgo(a.When),
		})
	}

	s.render(w, "dashboard.html.tmpl", data)
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
