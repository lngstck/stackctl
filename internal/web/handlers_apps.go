package web

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/lngstck/stackctl/internal/catalog"
	"github.com/lngstck/stackctl/internal/compose"
	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/dex"
	"github.com/lngstck/stackctl/internal/docker"
	"github.com/lngstck/stackctl/internal/envfile"
	"github.com/lngstck/stackctl/internal/install"
	"github.com/lngstck/stackctl/internal/lock"
	"github.com/lngstck/stackctl/internal/paths"
	"github.com/lngstck/stackctl/internal/public"
)

// appsData is the template context for apps.html.tmpl.
type appsData struct {
	PageData
	All       []appListEntry
	Installed []appListEntry
	Available []appListEntry
	Message   string
	IsError   bool
}

// appListEntry holds one entry in the app catalog list.
type appListEntry struct {
	ID              string
	Name            string
	Category        string
	Description     string
	Version         string
	IsInstalled     bool
	IsMandatory     bool
	Status          string // "running" | "stopped" | "unknown" | "" (not installed)
	UpdateAvailable bool
	UpdateTo        string
	UpdateBreaking  bool
}

// pinMandatoryFirst zieht noch nicht installierte Pflicht-Dienste an den
// Anfang der Liste — auf einem frischen System soll der Admin postgres und
// dex als Erstes sehen, nicht alphabetisch irgendwo im Katalog suchen.
func pinMandatoryFirst(entries []appListEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		pi := entries[i].IsMandatory && !entries[i].IsInstalled
		pj := entries[j].IsMandatory && !entries[j].IsInstalled
		return pi && !pj
	})
}

// appDetailData is the template context for app_detail.html.tmpl.
type appDetailData struct {
	PageData
	ID              string
	Name            string
	Category        string
	Description     string
	Version         string
	Status          string
	Port            int
	ServerDomain    string
	TunnelEnabled   bool
	TunnelSubdomain string
	ContainerName   string
	InstalledAt     string
	HasOIDC         bool
	OIDCClientID    string
	OIDCRedirectURI string
	Homepage        string
	Docs            string
	IsMandatory     bool
	AdminLogin         string
	AdminPassword      string
	AdminNotes         template.HTML
	UpdateAvailable    bool
	UpdateTo           string
	UpdateBreaking     bool
	AutoUpdateDisabled bool
}

// appInstallData is the template context for app_install.html.tmpl.
type appInstallData struct {
	PageData
	ID          string
	Name        string
	Category    string
	Description string
	Version     string
	HasOIDC     bool
	Prompts     []catalog.Prompt
	Secrets     []catalog.SecretSpec
	UsesAdminPw bool
	Error       string
	Values      map[string]string
}

// expandAdminPlaceholders replaces {school_slug}, {server_domain}, {app_id}
// und die {public_*}-Adressen in admin_info strings. Kept narrow on purpose —
// admin_info is rendered directly into the UI, so we don't want to pull in
// arbitrary .env values. Das Gegenstueck fuer post_install-Messages ist
// install.expandMessage; beide Listen zusammen halten.
func expandAdminPlaceholders(s string, cfg *config.Config, appID string) string {
	if s == "" {
		return ""
	}
	r := strings.NewReplacer(
		"{school_slug}", cfg.School.Slug,
		"{server_domain}", cfg.School.ServerDomain,
		"{app_id}", appID,
		"{public_base_domain}", public.BaseDomain(cfg),
		"{public_app_url}", public.AppURL(cfg, appID),
		"{public_auth_url}", public.AuthURL(cfg),
	)
	return r.Replace(s)
}

// urlPattern matches http(s)-URLs in admin_info notes. Trailing
// Satzzeichen bleiben draussen, damit "…/admin." nicht den Punkt mitnimmt.
var urlPattern = regexp.MustCompile(`https?://[^\s<>"]+`)

// linkifyAdminNotes escapes admin_info notes for HTML and wraps URLs in
// anchor tags. Everything is escaped first — the only markup in the result
// is the anchors we build ourselves, so catalog content can't inject HTML.
func linkifyAdminNotes(s string) template.HTML {
	if s == "" {
		return ""
	}
	var b strings.Builder
	last := 0
	for _, m := range urlPattern.FindAllStringIndex(s, -1) {
		b.WriteString(template.HTMLEscapeString(s[last:m[0]]))
		url := strings.TrimRight(s[m[0]:m[1]], ".,;:!?)")
		rest := s[m[0]:m[1]][len(url):]
		esc := template.HTMLEscapeString(url)
		b.WriteString(`<a href="` + esc + `" target="_blank" rel="noopener">` + esc + `</a>`)
		b.WriteString(template.HTMLEscapeString(rest))
		last = m[1]
	}
	b.WriteString(template.HTMLEscapeString(s[last:]))
	return template.HTML(b.String())
}

// usesAdminPassword reports whether any of the app's environment values
// references ${ADMIN_PASSWORD}. Used to show a UX hint on the install page.
func usesAdminPassword(def *catalog.Definition) bool {
	for _, e := range def.Environment {
		if strings.Contains(e.Value, "${ADMIN_PASSWORD}") {
			return true
		}
	}
	return false
}

func (s *Server) handleApps(w http.ResponseWriter, r *http.Request) {
	data := appsData{
		PageData: s.pageData("apps"),
	}

	// Load catalog index.
	idx, err := catalog.LoadIndex()
	if err != nil {
		data.Message = "Katalog nicht geladen. Bitte zuerst synchronisieren."
		data.IsError = true
		s.render(w, "apps.html.tmpl", data)
		return
	}

	st := s.snapState()
	for _, app := range idx.Apps {
		cs, installed := st.Containers[app.ID]
		entry := appListEntry{
			ID:          app.ID,
			Name:        app.Name,
			Category:    app.Category,
			Description: app.Description,
			Version:     "",
			IsInstalled: installed,
			IsMandatory: isMandatoryApp(s.cfg, app.ID),
		}

		if installed {
			entry.Version = cs.VersionInstalled
			containerName := "ls-" + app.ID
			if docker.IsRunning(containerName) {
				entry.Status = "running"
			} else {
				entry.Status = "stopped"
			}
			// Update-Verfuegbarkeit aus gecachter Definition ableiten.
			if def, err := catalog.LoadDefinition(app.ID); err == nil {
				if catalog.HasUpdate(cs.VersionInstalled, def.Version) {
					entry.UpdateAvailable = true
					entry.UpdateTo = def.Version
					entry.UpdateBreaking = def.Breaking
				}
			}
			data.Installed = append(data.Installed, entry)
		} else {
			entry.Status = ""
			data.Available = append(data.Available, entry)
		}
		data.All = append(data.All, entry)
	}

	pinMandatoryFirst(data.All)
	pinMandatoryFirst(data.Available)

	if msg := r.URL.Query().Get("msg"); msg != "" {
		data.Message = msg
		data.IsError = r.URL.Query().Get("err") == "1"
	}

	s.render(w, "apps.html.tmpl", data)
}

func (s *Server) handleAppDetail(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("id")
	if appID == "" {
		http.Redirect(w, r, "/apps", http.StatusSeeOther)
		return
	}

	cs, ok := s.snapState().Containers[appID]
	if !ok {
		http.Redirect(w, r, "/apps", http.StatusSeeOther)
		return
	}

	status := "stopped"
	if docker.IsRunning("ls-" + appID) {
		status = "running"
	}

	port := 0
	if len(cs.Ports) > 0 {
		port = cs.Ports[0]
	}

	data := appDetailData{
		PageData:        s.pageData("apps"),
		ID:              appID,
		Name:            cs.Name,
		Version:         cs.VersionInstalled,
		Status:          status,
		Port:            port,
		ServerDomain:    s.cfg.School.ServerDomain,
		TunnelEnabled:   cs.PublicEnabled,
		TunnelSubdomain: cs.PublicHost,
		ContainerName:   "ls-" + appID,
		InstalledAt:     cs.InstalledAt,
		IsMandatory:        isMandatoryApp(s.cfg, appID),
		AutoUpdateDisabled: cs.AutoUpdateDisabled,
	}

	// Load definition for extra info (OIDC, links, category, description).
	def, err := catalog.LoadDefinition(appID)
	if err == nil {
		data.Category = def.Category
		data.Description = def.Description
		if catalog.HasUpdate(cs.VersionInstalled, def.Version) {
			data.UpdateAvailable = true
			data.UpdateTo = def.Version
			data.UpdateBreaking = def.Breaking
		}
		if def.OIDC != nil {
			data.HasOIDC = true
			data.OIDCClientID = def.OIDC.ClientID
			data.OIDCRedirectURI = dex.BuildRedirectURI(
				s.cfg, appID, def.OIDC.RedirectPath, port, cs.PublicEnabled,
			)
		}
		if def.Links != nil {
			data.Homepage = def.Links.Homepage
			data.Docs = def.Links.Docs
		}
		if def.AdminInfo != nil {
			data.AdminLogin = expandAdminPlaceholders(def.AdminInfo.Login, s.cfg, appID)
			data.AdminPassword = expandAdminPlaceholders(def.AdminInfo.PasswordHint, s.cfg, appID)
			data.AdminNotes = linkifyAdminNotes(expandAdminPlaceholders(def.AdminInfo.Notes, s.cfg, appID))
		}
	}

	s.render(w, "app_detail.html.tmpl", data)
}

func (s *Server) handleAppInstallForm(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("id")

	def, err := catalog.GetOrFetch(s.cfg.Catalog.URL, appID)
	if err != nil {
		http.Redirect(w, r, "/apps?msg=App+nicht+gefunden&err=1", http.StatusSeeOther)
		return
	}

	// Check dependencies.
	idSlice := s.snapState().InstalledIDs()
	installedIDs := make(map[string]bool, len(idSlice))
	for _, id := range idSlice {
		installedIDs[id] = true
	}
	missing := catalog.MissingDependencies(def, installedIDs)
	if len(missing) > 0 {
		msg := fmt.Sprintf("Fehlende Abhaengigkeiten: %s", strings.Join(missing, ", "))
		http.Redirect(w, r, "/apps?msg="+msg+"&err=1", http.StatusSeeOther)
		return
	}

	data := appInstallData{
		PageData:    s.pageData("apps"),
		ID:          def.ID,
		Name:        def.Name,
		Category:    def.Category,
		Description: def.Description,
		Version:     def.Version,
		HasOIDC:     def.OIDC != nil,
		Prompts:     def.Prompts,
		Secrets:     def.Secrets,
		UsesAdminPw: usesAdminPassword(def),
		Values:      make(map[string]string),
	}

	s.render(w, "app_install.html.tmpl", data)
}

func (s *Server) handleAppInstallPost(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("id")

	def, err := catalog.GetOrFetch(s.cfg.Catalog.URL, appID)
	if err != nil {
		http.Redirect(w, r, "/apps?msg=App+nicht+gefunden&err=1", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Ungueltige Formulardaten", http.StatusBadRequest)
		return
	}

	// Collect prompt values.
	promptValues := make(map[string]string)
	for _, p := range def.Prompts {
		promptValues[p.Key] = r.FormValue(p.Key)
	}

	// Validate required prompts.
	for _, p := range def.Prompts {
		if p.Required && promptValues[p.Key] == "" {
			data := appInstallData{
				PageData:    s.pageData("apps"),
				ID:          def.ID,
				Name:        def.Name,
				Category:    def.Category,
				Description: def.Description,
				Version:     def.Version,
				HasOIDC:     def.OIDC != nil,
				Prompts:     def.Prompts,
				Secrets:     def.Secrets,
				UsesAdminPw: usesAdminPassword(def),
				Error:       fmt.Sprintf("%s ist erforderlich.", p.Question),
				Values:      promptValues,
			}
			s.render(w, "app_install.html.tmpl", data)
			return
		}
	}

	// Long-running work runs asynchronously behind the op-lock so the browser
	// gets a live progress view (issue #1) instead of a multi-minute blocking
	// request. The handle is released by the worker goroutine.
	h, ok := s.tryLock(w, r)
	if !ok {
		return
	}
	job := s.jobs.create("install", appID, "Installiere "+def.Name, "/apps/"+appID)
	go s.runAppJob(h, job, func(working *config.State, env *envfile.File, allDefs []*catalog.Definition, dexClients []dex.Client) (*install.Result, []dex.Client, error) {
		return install.Install(def, s.cfg, working, env, dexClients, allDefs, promptValues, job)
	})
	http.Redirect(w, r, "/jobs/"+job.ID, http.StatusSeeOther)
}

func (s *Server) handleAppUpdate(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("id")

	if !s.snapState().IsInstalled(appID) {
		http.Redirect(w, r, "/apps?msg=Nicht+installiert&err=1", http.StatusSeeOther)
		return
	}

	// Force-refresh: catalog.FetchDefinition always overwrites the local cache.
	def, err := catalog.FetchDefinition(s.cfg.Catalog.URL, appID)
	if err != nil {
		log.Printf("web: fetch %s for update: %v", appID, err)
		http.Redirect(w, r, fmt.Sprintf("/apps/%s?msg=Katalog-Abruf+fehlgeschlagen&err=1", appID), http.StatusSeeOther)
		return
	}

	h, ok := s.tryLock(w, r)
	if !ok {
		return
	}
	job := s.jobs.create("update", appID, "Aktualisiere "+def.Name, "/apps/"+appID)
	go s.runAppJob(h, job, func(working *config.State, env *envfile.File, allDefs []*catalog.Definition, dexClients []dex.Client) (*install.Result, []dex.Client, error) {
		return install.Update(def, s.cfg, working, env, dexClients, allDefs, job)
	})
	http.Redirect(w, r, "/jobs/"+job.ID, http.StatusSeeOther)
}

// runAppJob is the shared worker body for install and update jobs. It loads a
// fresh env, snapshots state into a private clone, reconstructs the OIDC client
// list, runs the supplied operation (which reports progress to the job), then
// persists env, dex config and the mutated state clone — all off the request
// goroutine. It always releases the op-lock and finishes the job.
func (s *Server) runAppJob(
	h *lock.Handle,
	job *Job,
	op func(working *config.State, env *envfile.File, allDefs []*catalog.Definition, dexClients []dex.Client) (*install.Result, []dex.Client, error),
) {
	defer h.Release()

	env, err := envfile.Load(paths.EnvFile())
	if err != nil {
		env = envfile.New()
	}

	working := s.snapState()
	var allDefs []*catalog.Definition
	for id := range working.Containers {
		if d, err := catalog.LoadDefinition(id); err == nil {
			allDefs = append(allDefs, d)
		}
	}
	dexClients := install.ReconstructDexClients(allDefs, env, s.cfg)

	result, updatedClients, opErr := op(working, env, allDefs, dexClients)
	if opErr != nil {
		log.Printf("web: job %s (%s): %v", job.ID, job.Kind, opErr)
	}

	// Persist env (incl. system keys), the mutated state clone, and — if the
	// op touched OIDC — the dex config. On failure the clone is unchanged for
	// installs (state is only written on success), so committing is harmless.
	envfile.ApplySystemEnv(env, s.cfg, "")
	if err := env.Save(paths.EnvFile()); err != nil {
		log.Printf("web: job %s: save env: %v", job.ID, err)
	}
	if err := s.commitState(working); err != nil {
		log.Printf("web: job %s: commit state: %v", job.ID, err)
	}
	if updatedClients != nil {
		if err := dex.SaveConfig(s.cfg, updatedClients); err != nil {
			log.Printf("web: job %s: save dex config: %v", job.ID, err)
		}
	}

	if result != nil {
		job.setResult(result.SecretsToShow, result.Messages)
	}
	success := result != nil && result.Success
	errMsg := ""
	switch {
	case opErr != nil:
		errMsg = opErr.Error()
	case result != nil && result.Error != "":
		errMsg = result.Error
	}
	job.finish(success, errMsg)
}

func (s *Server) handleAppAutoUpdateToggle(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("id")
	working := s.snapState()
	cs, ok := working.Containers[appID]
	if !ok {
		http.Redirect(w, r, "/apps?msg=Nicht+installiert&err=1", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Ungueltige Formulardaten", http.StatusBadRequest)
		return
	}
	cs.AutoUpdateDisabled = r.FormValue("disabled") == "on"
	if err := s.commitState(working); err != nil {
		log.Printf("web: save state after autoupdate toggle: %v", err)
	}
	http.Redirect(w, r, fmt.Sprintf("/apps/%s", appID), http.StatusSeeOther)
}

func (s *Server) handleAppRemove(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("id")

	env, err := envfile.Load(paths.EnvFile())
	if err != nil {
		env = envfile.New()
	}

	working := s.snapState()
	var remainingDefs []*catalog.Definition
	for id := range working.Containers {
		if id != appID {
			if d, err := catalog.LoadDefinition(id); err == nil {
				remainingDefs = append(remainingDefs, d)
			}
		}
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Ungueltige Formulardaten", http.StatusBadRequest)
		return
	}
	wipeData := r.FormValue("wipe_data") == "on"

	// Dex-Clients der verbleibenden Apps rekonstruieren; Remove entfernt daraus
	// nur den eigenen Client und schreibt die Config mit dem Rest neu.
	dexClients := install.ReconstructDexClients(remainingDefs, env, s.cfg)
	_, removeErr := install.Remove(appID, s.cfg, working, env, dexClients, remainingDefs, wipeData)

	if removeErr != nil {
		log.Printf("web: remove %s: %v", appID, removeErr)
		http.Redirect(w, r, fmt.Sprintf("/apps/%s?msg=Fehler+beim+Entfernen&err=1", appID), http.StatusSeeOther)
		return
	}

	envfile.ApplySystemEnv(env, s.cfg, "")

	if err := env.Save(paths.EnvFile()); err != nil {
		log.Printf("web: save env after remove: %v", err)
	}
	if err := s.commitState(working); err != nil {
		log.Printf("web: save state after remove: %v", err)
	}

	http.Redirect(w, r, "/apps?msg="+appID+"+entfernt", http.StatusSeeOther)
}

func (s *Server) handleAppStart(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("id")
	svcName := compose.ServiceName(appID)
	code, out := docker.ComposeUp(paths.ComposeFile(), svcName)
	if code != 0 {
		log.Printf("web: start %s: %s", appID, out)
	}
	referrer := r.Header.Get("Referer")
	if referrer == "" {
		referrer = "/"
	}
	http.Redirect(w, r, referrer, http.StatusSeeOther)
}

func (s *Server) handleAppStop(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("id")
	svcName := compose.ServiceName(appID)
	docker.ComposeStop(paths.ComposeFile(), svcName)
	referrer := r.Header.Get("Referer")
	if referrer == "" {
		referrer = "/"
	}
	http.Redirect(w, r, referrer, http.StatusSeeOther)
}
