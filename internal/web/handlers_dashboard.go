package web

import (
	"net/http"

	"github.com/lngstck/stackctl/internal/docker"
)

// dashboardData is the template context for dashboard.html.tmpl.
type dashboardData struct {
	PageData
	Apps []appCardData
}

// appCardData holds everything needed to render one app card.
type appCardData struct {
	ID              string
	Name            string
	Description     string
	Category        string
	Status          string // "running" | "stopped" | "unknown"
	Port            int
	Version         string
	TunnelEnabled   bool
	TunnelSubdomain string
	IsMandatory     bool // dex, postgres — no remove button
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	data := dashboardData{
		PageData: s.pageData("dashboard"),
	}

	for _, cs := range s.state.Containers {
		status := "unknown"
		containerName := "ls-" + cs.ID
		if docker.IsRunning(containerName) {
			status = "running"
		} else {
			status = "stopped"
		}

		port := 0
		if len(cs.Ports) > 0 {
			port = cs.Ports[0]
		}

		mandatory := cs.ID == "postgres" || cs.ID == "dex"

		data.Apps = append(data.Apps, appCardData{
			ID:              cs.ID,
			Name:            cs.Name,
			Description:     "", // Would come from cached definition.
			Category:        "", // Would come from cached definition.
			Status:          status,
			Port:            port,
			Version:         cs.VersionInstalled,
			TunnelEnabled:   cs.TunnelEnabled,
			TunnelSubdomain: cs.TunnelSubdomain,
			IsMandatory:     mandatory,
		})
	}

	s.render(w, "dashboard.html.tmpl", data)
}
