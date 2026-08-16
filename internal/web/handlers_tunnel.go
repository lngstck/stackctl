package web

import (
	"fmt"
	"log"
	"net/http"

	"github.com/lngstck/stackctl/internal/catalog"
	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/public"
	"github.com/lngstck/stackctl/internal/publish"
)

// tunnelData is the template context for tunnel.html.tmpl.
type tunnelData struct {
	PageData
	SSHPubKey       string
	KeyExists       bool
	SSHHost         string
	SSHPort         int
	DexTunnelStatus string // "running" | "stopped" | "error"
	DexSubdomain    string
	Apps            []tunnelAppEntry
	TestResult      string // "" | "ok" | error message
	TestDone        bool
	// CanTest is true when the current publisher can check its own transport.
	CanTest bool
}

// tunnelAppEntry is one row in the app tunnel table.
type tunnelAppEntry struct {
	ID              string
	Name            string
	Port            int
	TunnelEnabled   bool
	TunnelSubdomain string
	TunnelStatus    string // "running" | "stopped" | "error"
	HasOIDC         bool
}

func (s *Server) handleTunnel(w http.ResponseWriter, r *http.Request) {
	data := tunnelData{
		PageData:        s.pageData("tunnel"),
		DexSubdomain:    public.AuthHost(s.cfg),
		DexTunnelStatus: publish.StatusStopped,
	}

	if s.publisher != nil {
		data.DexTunnelStatus = s.publisher.AuthStatus()
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
		if id == "postgres" || id == "dex" {
			continue // infrastructure, not shown in app tunnel list
		}
		port := 0
		if len(cs.Ports) > 0 {
			port = cs.Ports[0]
		}
		entry := tunnelAppEntry{
			ID:              id,
			Name:            cs.Name,
			Port:            port,
			TunnelEnabled:   cs.PublicEnabled,
			TunnelSubdomain: cs.PublicHost,
			TunnelStatus:    publish.StatusStopped,
		}
		if s.publisher != nil {
			entry.TunnelStatus = s.publisher.Status(id)
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

	s.render(w, "tunnel.html.tmpl", data)
}

// handleDexTunnelStart (re)publishes Dex. Previously the Dex tunnel had no
// manual control — once the monitor gave up on it, the only recovery was
// restarting the whole stackctl service.
func (s *Server) handleDexTunnelStart(w http.ResponseWriter, r *http.Request) {
	if s.publisher == nil {
		http.Redirect(w, r, "/tunnel?test=Kein+Publisher+verfuegbar", http.StatusSeeOther)
		return
	}
	if err := s.publisher.StartAuth(); err != nil {
		http.Redirect(w, r, "/tunnel?test="+fmt.Sprintf("Start des Login-Zugangs fehlgeschlagen: %v", err), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/tunnel", http.StatusSeeOther)
}

// handleDexTunnelStop withdraws Dex. It stays down until started again or
// until stackctl restarts (EnsureAuth runs at startup).
func (s *Server) handleDexTunnelStop(w http.ResponseWriter, r *http.Request) {
	if s.publisher == nil {
		http.Redirect(w, r, "/tunnel?test=Kein+Publisher+verfuegbar", http.StatusSeeOther)
		return
	}
	if err := s.publisher.StopAuth(); err != nil {
		http.Redirect(w, r, "/tunnel?test="+fmt.Sprintf("Stop des Login-Zugangs fehlgeschlagen: %v", err), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/tunnel", http.StatusSeeOther)
}

func (s *Server) handleTunnelTest(w http.ResponseWriter, r *http.Request) {
	tester, ok := s.publisher.(publish.ConnectivityTester)
	if !ok {
		http.Redirect(w, r, "/tunnel?test=Fuer+diese+Betriebsart+gibt+es+keinen+Verbindungstest", http.StatusSeeOther)
		return
	}
	if err := tester.TestTransport(); err != nil {
		http.Redirect(w, r, "/tunnel?test="+fmt.Sprintf("Fehler: %v", err), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/tunnel?test=ok", http.StatusSeeOther)
}

// handleAppTunnelEnable publishes an app and records the result.
//
// The publisher does the publishing and reports the host it bound; writing
// that into state.yaml happens here, on a snapshot, committed under the
// server's lock. The route holds the op-lock (authPostLocked), so this cannot
// interleave with a background install job's commit.
func (s *Server) handleAppTunnelEnable(w http.ResponseWriter, r *http.Request) {
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

// handleAppTunnelDisable withdraws an app from the internet.
func (s *Server) handleAppTunnelDisable(w http.ResponseWriter, r *http.Request) {
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
