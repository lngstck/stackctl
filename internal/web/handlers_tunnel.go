package web

import (
	"fmt"
	"net/http"

	"github.com/lngstck/stackctl/internal/catalog"
	"github.com/lngstck/stackctl/internal/paths"
	"github.com/lngstck/stackctl/internal/tunnel"
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
		PageData: s.pageData("tunnel"),
		SSHHost:  s.cfg.Tunnel.SSHHost,
		SSHPort:  s.cfg.Tunnel.SSHPort,
	}

	// SSH key.
	if pub, err := tunnel.PublicKey(); err == nil {
		data.SSHPubKey = pub
		data.KeyExists = true
	}

	// Dex tunnel status.
	data.DexSubdomain = "auth." + s.cfg.School.Slug + ".learningstack.online"
	if s.tunnelMgr != nil {
		data.DexTunnelStatus = s.tunnelMgr.Status(tunnel.DexTunnelID)
	} else {
		data.DexTunnelStatus = "stopped"
	}

	// App tunnels.
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
			TunnelEnabled:   cs.TunnelEnabled,
			TunnelSubdomain: cs.TunnelSubdomain,
		}
		if s.tunnelMgr != nil {
			entry.TunnelStatus = s.tunnelMgr.Status(id)
		} else {
			entry.TunnelStatus = "stopped"
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

// handleDexTunnelStart (re)starts the Dex tunnel. Previously the Dex tunnel
// had no manual control — once the monitor gave up on it, the only recovery
// was restarting the whole stackctl service. Now it gets the same start/stop
// treatment as app tunnels.
func (s *Server) handleDexTunnelStart(w http.ResponseWriter, r *http.Request) {
	if s.tunnelMgr == nil {
		http.Redirect(w, r, "/tunnel?test=Tunnel-Manager+nicht+verfuegbar", http.StatusSeeOther)
		return
	}
	if err := s.tunnelMgr.StartDexTunnel(); err != nil {
		http.Redirect(w, r, "/tunnel?test="+fmt.Sprintf("Dex-Tunnel-Start fehlgeschlagen: %v", err), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/tunnel", http.StatusSeeOther)
}

// handleDexTunnelStop stops the Dex tunnel. It stays down until manually
// started again or until stackctl restarts (EnsureDexTunnel runs at startup).
func (s *Server) handleDexTunnelStop(w http.ResponseWriter, r *http.Request) {
	if s.tunnelMgr == nil {
		http.Redirect(w, r, "/tunnel?test=Tunnel-Manager+nicht+verfuegbar", http.StatusSeeOther)
		return
	}
	if err := s.tunnelMgr.StopDexTunnel(); err != nil {
		http.Redirect(w, r, "/tunnel?test="+fmt.Sprintf("Dex-Tunnel-Stop fehlgeschlagen: %v", err), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/tunnel", http.StatusSeeOther)
}

func (s *Server) handleTunnelTest(w http.ResponseWriter, r *http.Request) {
	keyPath := paths.TunnelKeyFile()
	err := tunnel.TestConnection(s.cfg.Tunnel.SSHHost, s.cfg.Tunnel.SSHPort, keyPath)
	if err != nil {
		http.Redirect(w, r, "/tunnel?test="+fmt.Sprintf("Fehler: %v", err), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/tunnel?test=ok", http.StatusSeeOther)
}

func (s *Server) handleAppTunnelEnable(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("id")
	if s.tunnelMgr == nil {
		http.Redirect(w, r, "/apps/"+appID+"?msg=Tunnel-Manager+nicht+verfuegbar&err=1", http.StatusSeeOther)
		return
	}
	if err := s.tunnelMgr.EnableAppTunnel(appID); err != nil {
		http.Redirect(w, r, "/apps/"+appID+"?msg=Tunnel+Fehler&err=1", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/apps/"+appID, http.StatusSeeOther)
}

func (s *Server) handleAppTunnelDisable(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("id")
	if s.tunnelMgr == nil {
		http.Redirect(w, r, "/apps/"+appID+"?msg=Tunnel-Manager+nicht+verfuegbar&err=1", http.StatusSeeOther)
		return
	}
	if err := s.tunnelMgr.DisableAppTunnel(appID); err != nil {
		http.Redirect(w, r, "/apps/"+appID+"?msg=Tunnel+Fehler&err=1", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/apps/"+appID, http.StatusSeeOther)
}
