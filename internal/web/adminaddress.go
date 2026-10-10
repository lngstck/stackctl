package web

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/lngstck/stackctl/internal/caddy"
	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/public"
	"github.com/lngstck/stackctl/internal/publish"
)

// autoPublishAdmin gives stackctl's own UI its address as soon as the proxy is
// installed — in the school network only. There the address resolves to a
// private IP and adds no way in from outside; what it adds is TLS, so the
// admin password no longer crosses the LAN in plain HTTP. On a server on the
// internet publishing the UI remains a deliberate decision
// (handleAdminPublishStart). The LAN port stays open either way.
//
// It records the result on working, the job's state clone, and returns a
// line for the job's messages.
func (s *Server) autoPublishAdmin(working *config.State, appID string) string {
	if appID != "caddy" || s.publisher == nil || working.AdminPublished ||
		s.cfg.Public.Transport != config.TransportLocal {
		return ""
	}
	// The proxy has only just started. Until its admin endpoint answers it
	// rejects a reload, and the new route would sit in the file unused.
	if err := waitForProxy(); err != nil {
		log.Printf("web: auto-publish admin UI: proxy not ready: %v", err)
		return "⚠ Die verschlüsselte Adresse der Verwaltung ließ sich noch nicht einrichten. Unter „Zugang“ lässt sie sich einschalten."
	}
	if err := s.publisher.StartAdmin(s.listenPort); err != nil {
		log.Printf("web: auto-publish admin UI: %v", err)
		return "⚠ Die verschlüsselte Adresse der Verwaltung ließ sich nicht einrichten. Unter „Zugang“ lässt sie sich einschalten."
	}
	working.AdminPublished = true
	return fmt.Sprintf("Die Verwaltung ist jetzt auch verschlüsselt erreichbar: https://%s – das Zertifikat kann beim ersten Mal einige Minuten dauern.", public.AdminHost(s.cfg))
}

// waitForProxy waits until the freshly started proxy accepts its config. A
// variable so tests need no docker.
var waitForProxy = func() error {
	var err error
	for range 20 {
		if err = caddy.Reload(); err == nil {
			return nil
		}
		time.Sleep(time.Second)
	}
	return err
}

// secureAdminURL is the encrypted address of this UI when the request came in
// over the plain LAN port although that address is up — for a hint on the
// login page and the dashboard. Empty otherwise.
func (s *Server) secureAdminURL(r *http.Request) string {
	if s.publisher == nil || s.cfg.Public.Transport != config.TransportLocal {
		return ""
	}
	host := public.AdminHost(s.cfg)
	if host == "" || r.Host == host || s.publisher.AdminStatus() != publish.StatusRunning {
		return ""
	}
	return "https://" + host
}

// adminAddressIssues points from the plain LAN port to the encrypted address.
func (s *Server) adminAddressIssues(r *http.Request) []dashIssue {
	url := s.secureAdminURL(r)
	if url == "" {
		return nil
	}
	return []dashIssue{{
		Level:       "info",
		Icon:        "🔒",
		Title:       "Verwaltung verschlüsselt aufrufen",
		Detail:      "Über " + url + " geht das Passwort verschlüsselt durchs Schulnetz.",
		Action:      url,
		ActionLabel: "Wechseln",
	}}
}
