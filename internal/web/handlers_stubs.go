package web

import (
	"log"
	"net/http"

	"github.com/lngstck/stackctl/internal/envfile"
	"github.com/lngstck/stackctl/internal/paths"
	"github.com/lngstck/stackctl/internal/public"
	"github.com/lngstck/stackctl/internal/secrets"
	"github.com/lngstck/stackctl/internal/update"
)

// settingsData is the template context for settings.html.tmpl. Seit Issue
// #4 enthaelt die Seite den ehemaligen /system-Bereich (stackctl-Version,
// Self-Update, Katalog-Sync) als eigenen Tab — daher die zusaetzlichen
// Felder aus dem alten systemData.
type settingsData struct {
	PageData
	SchoolName   string
	SchoolSlug   string
	ServerDomain string
	ContactEmail string
	// PublicBaseDomain is the parent of every public hostname of this
	// install — no longer derivable from the slug, so the UI reads it.
	PublicBaseDomain string
	DexAuthURL       string
	AutoUpdate   bool
	Error        string
	Message      string

	// System-Tab (ehemals /system).
	CurrentVersion  string
	LatestVersion   string
	UpdateAvailable bool
	CheckError      string
	UpdateResult    string
	UpdateError     string
	SyncResult      string
	SyncError       string

	// SystemTabActive steuert, welcher Tab beim Laden offen ist. Nach einer
	// System-Tab-Aktion (Update / Katalog-Sync) redirecten die Handler nach
	// /settings — ohne dieses Flag wuerde <ot-tabs> immer auf "Allgemein"
	// (erster Tab) starten und der Nutzer landet gefuehlt am falschen Ort.
	SystemTabActive bool

	// Restarting schaltet das Auto-Reload-Script frei (nach Self-Update, wenn
	// der Dienst gerade durch systemd neu gestartet wird).
	Restarting bool

	// Sys ist die System-Auslastung (RAM/Platte/Last) fuer den System-Tab.
	Sys sysView
}

func (s *Server) settingsData(msg, errMsg string) settingsData {
	return settingsData{
		PageData:       s.pageData("settings"),
		SchoolName:     s.cfg.School.Name,
		SchoolSlug:     s.cfg.School.Slug,
		ServerDomain:   s.cfg.School.ServerDomain,
		ContactEmail:     s.cfg.School.ContactEmail,
		PublicBaseDomain: public.BaseDomain(s.cfg),
		DexAuthURL:       public.AuthURL(s.cfg),
		AutoUpdate:     s.cfg.AutoUpdate.Enabled,
		Message:        msg,
		Error:          errMsg,
		CurrentVersion: update.CurrentVersion(),
		Sys:            buildSysView(),
	}
}

// withSystemFlash liest die System-Tab-Flash-Messages aus der Query und
// fuehrt den Update-Check aus. Wird vom GET-Handler genutzt, damit der
// POST-Handler keine eigene Render-Logik braucht — er redirected einfach
// mit Query-Params nach /settings.
func (s *Server) withSystemFlash(data settingsData, r *http.Request) settingsData {
	// Update-Check (non-blocking — Fehler werden im UI gezeigt).
	if result, err := update.Check(); err != nil {
		data.CheckError = err.Error()
	} else {
		data.LatestVersion = result.LatestVersion
		data.UpdateAvailable = result.UpdateAvailable
	}

	// Flash-Messages aus POST-Handlern.
	q := r.URL.Query()
	if msg := q.Get("sync"); msg != "" {
		if msg == "ok" {
			data.SyncResult = "Katalog erfolgreich synchronisiert."
		} else {
			data.SyncError = msg
		}
	}
	if msg := q.Get("update"); msg != "" {
		if q.Get("err") == "1" {
			data.UpdateError = msg
		} else {
			data.UpdateResult = msg
		}
	}
	// Jede System-Flash bedeutet: die Aktion kam aus dem System-Tab — also
	// genau dort wieder aufmachen statt zurueck auf "Allgemein" zu springen.
	if data.SyncResult != "" || data.SyncError != "" || data.UpdateResult != "" || data.UpdateError != "" {
		data.SystemTabActive = true
	}
	if q.Get("restarting") == "1" {
		data.Restarting = true
	}
	return data
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	data := s.withSystemFlash(s.settingsData("", ""), r)
	s.render(w, "settings.html.tmpl", data)
}

func (s *Server) handleSettingsPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.render(w, "settings.html.tmpl", s.withSystemFlash(s.settingsData("", "Ungueltige Formulardaten."), r))
		return
	}

	schoolName := r.FormValue("school_name")
	serverDomain := r.FormValue("server_domain")
	contactEmail := r.FormValue("contact_email")
	password := r.FormValue("password")
	passwordConfirm := r.FormValue("password_confirm")

	if schoolName == "" {
		s.render(w, "settings.html.tmpl", s.withSystemFlash(s.settingsData("", "Schulname ist erforderlich."), r))
		return
	}

	var newPassword string
	if password != "" || passwordConfirm != "" {
		if password != passwordConfirm {
			s.render(w, "settings.html.tmpl", s.withSystemFlash(s.settingsData("", "Passwoerter stimmen nicht ueberein."), r))
			return
		}
		if len(password) < 8 {
			s.render(w, "settings.html.tmpl", s.withSystemFlash(s.settingsData("", "Passwort muss mindestens 8 Zeichen lang sein."), r))
			return
		}
		hash, err := secrets.HashPassword(password)
		if err != nil {
			s.render(w, "settings.html.tmpl", s.withSystemFlash(s.settingsData("", "Passwort-Hash fehlgeschlagen."), r))
			return
		}
		s.cfg.Admin.PasswordHash = hash
		newPassword = password
	}

	s.cfg.School.Name = schoolName
	s.cfg.School.ServerDomain = serverDomain
	s.cfg.School.ContactEmail = contactEmail
	s.cfg.AutoUpdate.Enabled = r.FormValue("auto_update") == "on"

	if err := s.cfg.Save(); err != nil {
		log.Printf("web: save config from settings: %v", err)
		s.render(w, "settings.html.tmpl", s.withSystemFlash(s.settingsData("", "Konfiguration konnte nicht gespeichert werden."), r))
		return
	}

	// Propagate to .env.
	env, err := envfile.Load(paths.EnvFile())
	if err != nil {
		env = envfile.New()
	}
	envfile.ApplySystemEnv(env, s.cfg, newPassword)
	if err := env.Save(paths.EnvFile()); err != nil {
		log.Printf("web: save env from settings: %v", err)
	}

	// If the password changed, invalidate existing sessions so the new hash
	// has to be used. (Other admins logged in elsewhere get booted; this is
	// the right behavior for a single-admin tool.)
	if newPassword != "" {
		s.sessions.destroy()
	}

	s.render(w, "settings.html.tmpl", s.withSystemFlash(s.settingsData("Einstellungen gespeichert.", ""), r))
}
