package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"

	"github.com/lngstck/stackctl/internal/catalog"
	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/preflight"
	"github.com/lngstck/stackctl/internal/public"
	"github.com/lngstck/stackctl/internal/publish"
)

// publicData is the template context for public.html.tmpl.
type publicData struct {
	PageData
	// Mode and its label describe how this install is reached. The page has
	// to say it out loud: half of what follows — an SSH key, a connection
	// test, the very word "tunnel" — applies to one transport and not the
	// other.
	Mode       string
	ModeLabel  string
	IsRelay    bool
	BaseDomain string

	SSHPubKey  string
	KeyExists  bool
	SSHHost    string
	SSHPort    int
	AuthStatus string // "running" | "stopped" | "error"
	AuthHost   string
	// AdminHost/AdminStatus describe stackctl's own UI. It is published only
	// on request, so unlike the login it is usually "stopped".
	AdminHost   string
	AdminStatus string
	// AdminLocalAddr is where this UI answers on the LAN. It is shown next to
	// the public address because that pair is the whole point: publishing
	// adds a way in, it does not move one.
	AdminLocalAddr string
	Apps           []publicAppEntry
	TestResult     string // "" | "ok" | error message
	TestDone       bool
	// CanTest is true when the current publisher can check its own transport.
	CanTest bool
}

// publicAppEntry is one row in the app publication table.
type publicAppEntry struct {
	ID           string
	Name         string
	Port         int
	Published    bool
	PublicHost   string
	PublishState string // "running" | "stopped" | "error"
	HasOIDC      bool
}

func (s *Server) handlePublic(w http.ResponseWriter, r *http.Request) {
	mode := preflight.Mode(s.cfg)
	data := publicData{
		PageData:   s.pageData("public"),
		Mode:       mode,
		ModeLabel:  preflight.ModeLabel(mode),
		IsRelay:    s.cfg.Public.Transport != config.TransportDirect,
		BaseDomain: public.BaseDomain(s.cfg),
		AuthHost:   public.AuthHost(s.cfg),
		AuthStatus: publish.StatusStopped,

		AdminHost:      public.AdminHost(s.cfg),
		AdminStatus:    publish.StatusStopped,
		AdminLocalAddr: s.adminLocalAddr(),
	}

	if s.publisher != nil {
		data.AuthStatus = s.publisher.AuthStatus()
		data.AdminStatus = s.publisher.AdminStatus()
		_, data.CanTest = s.publisher.(publish.ConnectivityTester)

		// The SSH identity block only exists for transports that dial a
		// remote endpoint. A server that publishes itself has no relay key.
		if id, ok := s.publisher.(publish.RelayIdentity); ok {
			data.SSHHost, data.SSHPort = id.Endpoint()
			if pub, err := id.PublicKey(); err == nil {
				data.SSHPubKey = pub
				data.KeyExists = true
			}
		}
	}

	// App publication.
	for id, cs := range s.snapState().Containers {
		if isMandatoryApp(s.cfg, id) {
			continue // infrastructure, not shown in the app list
		}
		port := 0
		if len(cs.Ports) > 0 {
			port = cs.Ports[0]
		}
		entry := publicAppEntry{
			ID:           id,
			Name:         cs.Name,
			Port:         port,
			Published:    cs.PublicEnabled,
			PublicHost:   cs.PublicHost,
			PublishState: publish.StatusStopped,
		}
		if s.publisher != nil {
			entry.PublishState = s.publisher.Status(id)
		}

		// Check if app has OIDC.
		if def, err := catalog.LoadDefinition(id); err == nil && def.OIDC != nil {
			entry.HasOIDC = true
		}

		data.Apps = append(data.Apps, entry)
	}

	// Flash message from test.
	if r.URL.Query().Get("test") != "" {
		data.TestDone = true
		data.TestResult = r.URL.Query().Get("test")
	}

	s.render(w, "public.html.tmpl", data)
}

// handlePublicHealth answers the live status cards.
//
// It is a separate request on purpose: these checks talk to DNS, open a TLS
// connection and fetch a URL, which together can take seconds. Doing that
// inline would make the page slow exactly when something is wrong — the one
// time an admin needs it to come up.
func (s *Server) handlePublicHealth(w http.ResponseWriter, r *http.Request) {
	checks := preflight.NewProber().Live(r.Context(), preflight.LiveInput{
		Mode:         preflight.Mode(s.cfg),
		BaseDomain:   public.BaseDomain(s.cfg),
		RelaySSHHost: s.cfg.Public.Relay.SSHHost,
		AuthHost:     public.AuthHost(s.cfg),
	})

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"summary": preflight.Worst(checks),
		"checks":  checks,
	}); err != nil {
		log.Printf("web: encode public health: %v", err)
	}
}

// handleAuthPublishStart (re)publishes Dex. Previously the Dex tunnel had no
// manual control — once the monitor gave up on it, the only recovery was
// restarting the whole stackctl service.
func (s *Server) handleAuthPublishStart(w http.ResponseWriter, r *http.Request) {
	if s.publisher == nil {
		http.Redirect(w, r, "/public?test=Kein+Publisher+verfuegbar", http.StatusSeeOther)
		return
	}
	if err := s.publisher.StartAuth(); err != nil {
		http.Redirect(w, r, "/public?test="+fmt.Sprintf("Start des Login-Zugangs fehlgeschlagen: %v", err), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/public", http.StatusSeeOther)
}

// handleAuthPublishStop withdraws Dex. It stays down until started again or
// until stackctl restarts (EnsureAuth runs at startup).
func (s *Server) handleAuthPublishStop(w http.ResponseWriter, r *http.Request) {
	if s.publisher == nil {
		http.Redirect(w, r, "/public?test=Kein+Publisher+verfuegbar", http.StatusSeeOther)
		return
	}
	if err := s.publisher.StopAuth(); err != nil {
		http.Redirect(w, r, "/public?test="+fmt.Sprintf("Stop des Login-Zugangs fehlgeschlagen: %v", err), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/public", http.StatusSeeOther)
}

// adminLocalAddr renders the LAN address of this UI, e.g.
// "192.168.1.10:8090". The port is the one this process actually listens on,
// not the default — telling an admin a port that is not in use would send
// them looking for a network fault that does not exist.
func (s *Server) adminLocalAddr() string {
	host := s.cfg.School.ServerDomain
	if host == "" {
		host = "<server-ip>"
	}
	if s.listenPort <= 0 {
		return host
	}
	return net.JoinHostPort(host, strconv.Itoa(s.listenPort))
}

// handleAdminPublishStart puts stackctl's own UI on the public address.
//
// This is the one publish action that changes what an attacker can reach: the
// control plane installs containers, reads secrets and restores backups, and
// it is guarded by a single password with no second factor. It is therefore
// off by default and switched on deliberately, never as a side effect.
//
// What it does not do is close the LAN port. Rebinding stackctl to the
// loopback interface is the other half of this feature and deliberately not
// in it: a route that turns out not to work would then be indistinguishable
// from a locked-out server, and the fix would need SSH. Two ways in is the
// point until the route has proven itself.
func (s *Server) handleAdminPublishStart(w http.ResponseWriter, r *http.Request) {
	if s.publisher == nil {
		http.Redirect(w, r, "/public?test=Kein+Publisher+verfuegbar", http.StatusSeeOther)
		return
	}
	if err := s.publisher.StartAdmin(s.listenPort); err != nil {
		log.Printf("web: publish admin UI: %v", err)
		http.Redirect(w, r, "/public?test="+url.QueryEscape(
			fmt.Sprintf("Oeffentlicher Zugang zur Verwaltung fehlgeschlagen: %v", err)), http.StatusSeeOther)
		return
	}

	working := s.snapState()
	working.AdminPublished = true
	if err := s.commitState(working); err != nil {
		log.Printf("web: save state after publishing admin UI: %v", err)
	}
	http.Redirect(w, r, "/public", http.StatusSeeOther)
}

// handleAdminPublishStop takes the UI back off the public address. The LAN
// port was never closed, so this cannot strand the admin.
func (s *Server) handleAdminPublishStop(w http.ResponseWriter, r *http.Request) {
	if s.publisher == nil {
		http.Redirect(w, r, "/public?test=Kein+Publisher+verfuegbar", http.StatusSeeOther)
		return
	}
	if err := s.publisher.StopAdmin(); err != nil {
		log.Printf("web: unpublish admin UI: %v", err)
		http.Redirect(w, r, "/public?test="+url.QueryEscape(
			fmt.Sprintf("Zuruecknehmen fehlgeschlagen: %v", err)), http.StatusSeeOther)
		return
	}

	working := s.snapState()
	working.AdminPublished = false
	if err := s.commitState(working); err != nil {
		log.Printf("web: save state after unpublishing admin UI: %v", err)
	}
	http.Redirect(w, r, "/public", http.StatusSeeOther)
}

func (s *Server) handlePublicTest(w http.ResponseWriter, r *http.Request) {
	tester, ok := s.publisher.(publish.ConnectivityTester)
	if !ok {
		http.Redirect(w, r, "/public?test=Fuer+diese+Betriebsart+gibt+es+keinen+Verbindungstest", http.StatusSeeOther)
		return
	}
	if err := tester.TestTransport(); err != nil {
		http.Redirect(w, r, "/public?test="+fmt.Sprintf("Fehler: %v", err), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/public?test=ok", http.StatusSeeOther)
}

// handleAppPublishEnable publishes an app and records the result.
//
// The publisher does the publishing and reports the host it bound; writing
// that into state.yaml happens here, on a snapshot, committed under the
// server's lock. The route holds the op-lock (authPostLocked), so this cannot
// interleave with a background install job's commit.
func (s *Server) handleAppPublishEnable(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("id")
	if s.publisher == nil {
		http.Redirect(w, r, "/apps/"+appID+"?msg=Kein+Publisher+verfuegbar&err=1", http.StatusSeeOther)
		return
	}

	working := s.snapState()
	cs, ok := working.Containers[appID]
	if !ok {
		http.Redirect(w, r, "/apps?msg=Nicht+installiert&err=1", http.StatusSeeOther)
		return
	}

	host, err := s.publisher.Enable(s.publishApp(appID, cs))
	if err != nil {
		log.Printf("web: publish %s: %v", appID, err)
		http.Redirect(w, r, "/apps/"+appID+"?msg=Oeffentlicher+Zugang+fehlgeschlagen&err=1", http.StatusSeeOther)
		return
	}

	cs.PublicEnabled = true
	cs.PublicHost = host
	if err := s.commitState(working); err != nil {
		log.Printf("web: save state after publish %s: %v", appID, err)
	}
	http.Redirect(w, r, "/apps/"+appID, http.StatusSeeOther)
}

// handleAppPublishDisable withdraws an app from the internet.
func (s *Server) handleAppPublishDisable(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("id")
	if s.publisher == nil {
		http.Redirect(w, r, "/apps/"+appID+"?msg=Kein+Publisher+verfuegbar&err=1", http.StatusSeeOther)
		return
	}

	working := s.snapState()
	cs, ok := working.Containers[appID]
	if !ok {
		http.Redirect(w, r, "/apps?msg=Nicht+installiert&err=1", http.StatusSeeOther)
		return
	}

	if err := s.publisher.Disable(appID); err != nil {
		log.Printf("web: unpublish %s: %v", appID, err)
		http.Redirect(w, r, "/apps/"+appID+"?msg=Zugang+konnte+nicht+beendet+werden&err=1", http.StatusSeeOther)
		return
	}

	cs.PublicEnabled = false
	cs.PublicHost = ""
	if err := s.commitState(working); err != nil {
		log.Printf("web: save state after unpublish %s: %v", appID, err)
	}
	http.Redirect(w, r, "/apps/"+appID, http.StatusSeeOther)
}

// publishApp builds the publish.App for an installed container. The container
// port comes from the catalog definition — a relay ignores it, a local proxy
// routes to it over the docker network. A missing definition is not fatal:
// the app is still publishable, the proxy just has less to work with.
func (s *Server) publishApp(appID string, cs *config.ContainerState) publish.App {
	app := publish.App{ID: appID}
	if len(cs.Ports) > 0 {
		app.LocalPort = cs.Ports[0]
	}
	if def, err := catalog.LoadDefinition(appID); err == nil && len(def.Ports) > 0 {
		app.ContainerPort = def.Ports[0].Container
	}
	return app
}
