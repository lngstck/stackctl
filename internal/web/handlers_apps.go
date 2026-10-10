package web

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

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

// appSheet is everything the sheet of an installed app shows. The start
// screen renders one per app; /apps/{id} opens it.
type appSheet struct {
	ID          string
	Name        string
	ShortName   string // for the basic services: "Datenbank", "Anmeldung", …
	Description string
	Category    string
	Tile        appTile
	Running     bool
	IsInfra     bool
	IsMandatory bool

	Version        string
	UpdateTo       string
	UpdateBreaking bool
	AutoUpdate     bool

	OpenURL       string // where "Öffnen" leads; empty for background services
	Where         string // "school" | "internet"
	PublicEnabled bool
	PublicHost    string
	CanPublish    bool // the app has a web port the proxy can route to

	Port          int
	ServerDomain  string
	ContainerName string
	DataDir       string
	InstalledAt   string

	HasOIDC         bool
	OIDCClientID    string
	OIDCRedirectURI string

	AdminLogin    string
	AdminPassword string
	AdminNotes    template.HTML

	Homepage string
	Docs     string
}

// appSheet builds the sheet of an installed app from its state and the
// cached catalog definition.
func (s *Server) appSheet(id string, cs *config.ContainerState) appSheet {
	name := cs.Name
	if name == "" {
		name = id
	}
	port := 0
	if len(cs.Ports) > 0 {
		port = cs.Ports[0]
	}
	sh := appSheet{
		ID:            id,
		Name:          name,
		ShortName:     infraShortNames[id],
		Running:       appRunning(id),
		IsMandatory:   isMandatoryApp(id),
		Version:       cs.VersionInstalled,
		AutoUpdate:    !cs.AutoUpdateDisabled,
		Where:         "school",
		PublicEnabled: cs.PublicEnabled,
		PublicHost:    cs.PublicHost,
		Port:          port,
		ServerDomain:  s.cfg.School.ServerDomain,
		ContainerName: "ls-" + id,
		DataDir:       filepath.Join(paths.LearningstackDir(), id),
		InstalledAt:   formatDay(cs.InstalledAt),
	}
	if sh.ShortName == "" {
		sh.ShortName = name
	}

	def, err := catalog.LoadDefinition(id)
	if err == nil {
		sh.Description = def.Description
		sh.Category = def.Category
		if catalog.HasUpdate(cs.VersionInstalled, def.Version) {
			sh.UpdateTo = def.Version
			sh.UpdateBreaking = def.Breaking
		}
		if def.OIDC != nil {
			sh.HasOIDC = true
			sh.OIDCClientID = def.OIDC.ClientID
			sh.OIDCRedirectURI = dex.BuildRedirectURI(s.cfg, id, def.OIDC.RedirectPath, port, cs.PublicEnabled)
		}
		if def.Links != nil {
			sh.Homepage = def.Links.Homepage
			sh.Docs = def.Links.Docs
		}
		if def.AdminInfo != nil {
			sh.AdminLogin = expandAdminPlaceholders(def.AdminInfo.Login, s.cfg, id)
			sh.AdminPassword = expandAdminPlaceholders(def.AdminInfo.PasswordHint, s.cfg, id)
			sh.AdminNotes = linkifyAdminNotes(expandAdminPlaceholders(def.AdminInfo.Notes, s.cfg, id))
		}
	}
	sh.IsInfra = isInfrastructure(id, sh.Category)
	sh.CanPublish = !sh.IsMandatory && s.publishApp(id, cs).ContainerPort != 0

	if s.cfg.Public.Transport == config.TransportDirect && cs.PublicEnabled {
		sh.Where = "internet"
	}
	if !sh.IsInfra {
		switch {
		case cs.PublicEnabled && cs.PublicHost != "":
			sh.OpenURL = "https://" + cs.PublicHost
		case port > 0 && sh.ServerDomain != "":
			sh.OpenURL = fmt.Sprintf("http://%s:%d", sh.ServerDomain, port)
		}
	}
	sh.Tile = appTile{
		Face:     faceFor(id, name),
		Off:      !sh.Running,
		Update:   sh.UpdateTo != "",
		Internet: sh.Where == "internet",
	}
	return sh
}

// formatDay renders an RFC3339 timestamp as a German date.
func formatDay(rfc3339 string) string {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return rfc3339
	}
	return t.Local().Format("02.01.2006")
}

// catalogData is the template context for apps.html.tmpl, the catalog:
// every app the catalog offers, on shelves by category. An app opens its
// sheet; for one not yet installed the sheet is the install form.
type catalogData struct {
	PageData
	Flash   flash
	Shelves []catalogShelf
	// CatalogMissing is true when the catalog index has not been synced yet.
	// It is a notice of its own, so it never displaces the flash.
	CatalogMissing bool
	Available      int
	Apps           []catalogApp // every app, for the sheets
}

type catalogShelf struct {
	Title string
	Apps  []catalogApp
}

// catalogApp is one app in the catalog with what its install sheet needs.
type catalogApp struct {
	ID          string
	Name        string
	Label       string // under the tile: short names for the basic services
	Category    string
	Description string
	Version     string
	Tile        appTile
	Installed   bool
	IsMandatory bool

	// Install form (only for apps not yet installed).
	Missing     []string // names of apps it needs first
	Prompts     []catalog.Prompt
	Secrets     []catalog.SecretSpec
	UsesAdminPw bool
	HasOIDC     bool
	AutoAddress bool // the app gets its address during install (autoPublish)
	Error       string
	Values      map[string]string
}

// appListEntry is one app in the catalog index merged with its state.
type appListEntry struct {
	ID          string
	Name        string
	Category    string
	Description string
	IsInstalled bool
	IsMandatory bool
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
	s.renderCatalog(w, r, "", nil)
}

// renderCatalog renders the catalog. With openApp it opens that app's
// sheet; form carries the values and error of a rejected install.
func (s *Server) renderCatalog(w http.ResponseWriter, r *http.Request, openApp string, form *catalogApp) {
	data := catalogData{
		PageData: s.pageData("apps"),
		Flash:    flashFrom(r),
	}
	data.Search = true

	// Without the index only the offer is missing, not what is installed.
	idx, err := catalog.LoadIndex()
	if err != nil {
		data.CatalogMissing = true
		idx = &catalog.Index{}
	}

	st := s.snapState()
	installed := make(map[string]bool, len(st.Containers))
	for _, id := range st.InstalledIDs() {
		installed[id] = true
	}

	var entries []appListEntry
	listed := make(map[string]bool, len(idx.Apps))
	for _, app := range idx.Apps {
		listed[app.ID] = true
		entries = append(entries, appListEntry{
			ID: app.ID, Name: app.Name, Category: app.Category, Description: app.Description,
			IsInstalled: installed[app.ID], IsMandatory: isMandatoryApp(app.ID),
		})
	}
	// Installed apps the index does not list — no index at all, or an app
	// that has since left the catalog — still belong on the page. The cached
	// definition fills in what state.yaml does not know.
	for _, id := range st.InstalledIDs() {
		if listed[id] {
			continue
		}
		e := appListEntry{ID: id, Name: st.Containers[id].Name, IsInstalled: true, IsMandatory: isMandatoryApp(id)}
		if def, err := catalog.LoadDefinition(id); err == nil {
			e.Category = def.Category
			e.Description = def.Description
			if e.Name == "" {
				e.Name = def.Name
			}
		}
		if e.Name == "" {
			e.Name = id
		}
		entries = append(entries, e)
	}
	pinMandatoryFirst(entries)

	shelves := map[string]*catalogShelf{}
	var order []string
	for _, e := range entries {
		app := s.catalogApp(e, installed)
		if form != nil && form.ID == app.ID {
			app.Error = form.Error
			app.Values = form.Values
		}
		if !app.Installed {
			data.Available++
		}
		data.Apps = append(data.Apps, app)

		title := categoryLabel(e.Category)
		if shelves[title] == nil {
			shelves[title] = &catalogShelf{Title: title}
			order = append(order, title)
		}
		shelves[title].Apps = append(shelves[title].Apps, app)
	}

	// Grundversorgung steht vorn, solange etwas davon fehlt — sonst hinten,
	// nach dem, wofür man den Katalog eigentlich öffnet.
	infraMissing := false
	for _, id := range mandatoryAppIDs() {
		if !installed[id] {
			infraMissing = true
		}
	}
	rank := func(t string) int {
		if t == categoryLabels["infrastructure"] {
			if infraMissing {
				return -1
			}
			return len(categoryOrder) + 1
		}
		for i, c := range categoryOrder {
			if c == t {
				return i
			}
		}
		return len(categoryOrder)
	}
	sort.SliceStable(order, func(i, j int) bool { return rank(order[i]) < rank(order[j]) })
	for _, t := range order {
		data.Shelves = append(data.Shelves, *shelves[t])
	}

	if openApp != "" {
		data.OpenSheet = "app-" + openApp
	}
	s.render(w, "apps.html.tmpl", data)
}

// catalogApp builds one catalog entry. For an app not yet installed it loads
// the cached definition, which carries the install form.
func (s *Server) catalogApp(e appListEntry, installed map[string]bool) catalogApp {
	app := catalogApp{
		ID:          e.ID,
		Name:        e.Name,
		Category:    e.Category,
		Description: e.Description,
		Installed:   e.IsInstalled,
		IsMandatory: e.IsMandatory,
		Tile:        appTile{Face: faceFor(e.ID, e.Name)},
		Values:      map[string]string{},
	}
	app.Label = infraShortNames[e.ID]
	if app.Label == "" {
		app.Label = e.Name
	}
	if app.Installed {
		return app
	}
	def, err := catalog.LoadDefinition(e.ID)
	if err != nil {
		return app
	}
	app.Version = def.Version
	app.Prompts = def.Prompts
	app.Secrets = def.Secrets
	app.UsesAdminPw = usesAdminPassword(def)
	app.HasOIDC = def.OIDC != nil
	app.AutoAddress = s.cfg.Public.Transport == config.TransportLocal
	for _, dep := range catalog.MissingDependencies(def, installed) {
		app.Missing = append(app.Missing, appName(dep))
	}
	return app
}

// appName is the display name of an app from the cached catalog, its id
// when the catalog does not know it.
func appName(id string) string {
	if def, err := catalog.LoadDefinition(id); err == nil && def.Name != "" {
		return def.Name
	}
	return id
}

// handleAppDetail opens an installed app's sheet on the start screen. An app
// that is not installed has no sheet there; its place is the catalog.
func (s *Server) handleAppDetail(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("id")
	if _, ok := s.snapState().Containers[appID]; !ok {
		http.Redirect(w, r, "/apps", http.StatusSeeOther)
		return
	}
	s.renderStart(w, r, appID)
}

// handleAppInstallForm opens the install sheet in the catalog.
func (s *Server) handleAppInstallForm(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("id")
	if _, err := catalog.GetOrFetch(s.cfg.Catalog.URL, appID); err != nil {
		http.Redirect(w, r, "/apps?msg=App+nicht+gefunden&err=1", http.StatusSeeOther)
		return
	}
	if s.snapState().IsInstalled(appID) {
		http.Redirect(w, r, "/apps/"+appID, http.StatusSeeOther)
		return
	}
	s.renderCatalog(w, r, appID, nil)
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

	// Validate required prompts. A rejected form goes back to its sheet in
	// the catalog, with what was typed.
	for _, p := range def.Prompts {
		if p.Required && promptValues[p.Key] == "" {
			s.renderCatalog(w, r, appID, &catalogApp{
				ID:     appID,
				Error:  fmt.Sprintf("%s ist erforderlich.", p.Question),
				Values: promptValues,
			})
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
	if opErr == nil && result != nil && result.Success && job.Kind == "install" {
		if msg := s.autoPublish(working, job.AppID); msg != "" {
			result.Messages = append(result.Messages, msg)
		}
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

// autoPublish gives a freshly installed app its address when that address
// stays inside the school network. There it is no exposure — and an app that
// logs in through Dex cannot work without it, so asking for a second click
// would only be a trap. On a server directly on the internet publishing
// remains a deliberate decision.
//
// It records the result on working, the job's state clone, and returns a
// line for the job's messages when publishing failed. The app is installed
// either way; the address can be switched on later on its page.
func (s *Server) autoPublish(working *config.State, appID string) string {
	if s.publisher == nil || s.cfg.Public.Transport != config.TransportLocal || isMandatoryApp(appID) {
		return ""
	}
	cs := working.Containers[appID]
	if cs == nil || cs.PublicEnabled {
		return ""
	}
	app := s.publishApp(appID, cs)
	if app.ContainerPort == 0 {
		return "" // nothing to route to, e.g. a background service
	}
	host, err := s.publisher.Enable(app)
	if err != nil {
		log.Printf("web: auto-publish %s: %v", appID, err)
		return "⚠ Die Adresse der App konnte nicht eingerichtet werden. Auf der App-Seite lässt sie sich erneut einschalten."
	}
	cs.PublicEnabled = true
	cs.PublicHost = host
	return ""
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
	// The switch in the sheet reads "update automatically"; unchecked it
	// sends nothing.
	cs.AutoUpdateDisabled = r.FormValue("auto") != "on"
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

	http.Redirect(w, r, "/?msg="+url.QueryEscape(appName(appID)+" ist entfernt."), http.StatusSeeOther)
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
