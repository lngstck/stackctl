package web

import (
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/lngstck/stackctl/internal/catalog"
	"github.com/lngstck/stackctl/internal/compose"
	"github.com/lngstck/stackctl/internal/dex"
	"github.com/lngstck/stackctl/internal/docker"
	"github.com/lngstck/stackctl/internal/envfile"
	"github.com/lngstck/stackctl/internal/install"
	"github.com/lngstck/stackctl/internal/paths"
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
	ID          string
	Name        string
	Category    string
	Description string
	Version     string
	IsInstalled bool
	Status      string // "running" | "stopped" | "unknown" | "" (not installed)
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
	AdminPwEnv  string
	Error       string
	Values      map[string]string
}

// appInstallResultData is the template context for app_install_result.html.tmpl.
type appInstallResultData struct {
	PageData
	Result *install.Result
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

	for _, app := range idx.Apps {
		cs, installed := s.state.Containers[app.ID]
		entry := appListEntry{
			ID:          app.ID,
			Name:        app.Name,
			Category:    app.Category,
			Description: app.Description,
			Version:     "",
			IsInstalled: installed,
		}

		if installed {
			entry.Version = cs.VersionInstalled
			containerName := "ls-" + app.ID
			if docker.IsRunning(containerName) {
				entry.Status = "running"
			} else {
				entry.Status = "stopped"
			}
			data.Installed = append(data.Installed, entry)
		} else {
			entry.Status = ""
			data.Available = append(data.Available, entry)
		}
		data.All = append(data.All, entry)
	}

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

	cs, ok := s.state.Containers[appID]
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
		TunnelEnabled:   cs.TunnelEnabled,
		TunnelSubdomain: cs.TunnelSubdomain,
		ContainerName:   "ls-" + appID,
		InstalledAt:     cs.InstalledAt,
		IsMandatory:     appID == "postgres" || appID == "dex",
	}

	// Load definition for extra info (OIDC, links, category, description).
	def, err := catalog.LoadDefinition(appID)
	if err == nil {
		data.Category = def.Category
		data.Description = def.Description
		if def.OIDC != nil {
			data.HasOIDC = true
			data.OIDCClientID = def.OIDC.ClientID
			data.OIDCRedirectURI = dex.BuildRedirectURI(
				def.OIDC.RedirectPath, appID, s.cfg.School.Slug,
				s.cfg.School.ServerDomain, port, cs.TunnelEnabled,
			)
		}
		if def.Links != nil {
			data.Homepage = def.Links.Homepage
			data.Docs = def.Links.Docs
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
	idSlice := s.state.InstalledIDs()
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
		AdminPwEnv:  def.AdminPasswordEnv,
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
				AdminPwEnv:  def.AdminPasswordEnv,
				Error:       fmt.Sprintf("%s ist erforderlich.", p.Question),
				Values:      promptValues,
			}
			s.render(w, "app_install.html.tmpl", data)
			return
		}
	}

	// Load env file.
	env, err := envfile.Load(paths.EnvFile())
	if err != nil {
		env = envfile.New()
	}

	// Collect all installed definitions for compose regeneration.
	var allDefs []*catalog.Definition
	for id := range s.state.Containers {
		if d, err := catalog.LoadDefinition(id); err == nil {
			allDefs = append(allDefs, d)
		}
	}

	// Load existing dex clients.
	// TODO: load from dex config file. For now, reconstruct from state.
	var dexClients []dex.Client

	// Run install.
	result, updatedClients, installErr := install.Install(
		def, s.cfg, s.state, env, dexClients, allDefs, promptValues,
	)

	if installErr != nil {
		log.Printf("web: install %s: %v", appID, installErr)
	}

	// Refresh system-owned env keys (in case cfg changed since last save).
	envfile.ApplySystemEnv(env, s.cfg, "")

	// Save state + env regardless of partial success.
	if err := env.Save(paths.EnvFile()); err != nil {
		log.Printf("web: save env after install: %v", err)
	}
	if err := s.state.Save(); err != nil {
		log.Printf("web: save state after install: %v", err)
	}

	// Save dex config if clients were updated.
	if updatedClients != nil {
		if err := dex.SaveConfig(s.cfg, updatedClients); err != nil {
			log.Printf("web: save dex config after install: %v", err)
		}
	}

	s.render(w, "app_install_result.html.tmpl", appInstallResultData{
		PageData: s.pageData("apps"),
		Result:   result,
	})
}

func (s *Server) handleAppRemove(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("id")

	env, err := envfile.Load(paths.EnvFile())
	if err != nil {
		env = envfile.New()
	}

	var remainingDefs []*catalog.Definition
	for id := range s.state.Containers {
		if id != appID {
			if d, err := catalog.LoadDefinition(id); err == nil {
				remainingDefs = append(remainingDefs, d)
			}
		}
	}

	var dexClients []dex.Client
	_, removeErr := install.Remove(appID, s.cfg, s.state, env, dexClients, remainingDefs)

	if removeErr != nil {
		log.Printf("web: remove %s: %v", appID, removeErr)
		http.Redirect(w, r, fmt.Sprintf("/apps/%s?msg=Fehler+beim+Entfernen&err=1", appID), http.StatusSeeOther)
		return
	}

	envfile.ApplySystemEnv(env, s.cfg, "")

	if err := env.Save(paths.EnvFile()); err != nil {
		log.Printf("web: save env after remove: %v", err)
	}
	if err := s.state.Save(); err != nil {
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
