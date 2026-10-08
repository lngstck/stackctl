// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

// Package publish makes apps reachable under their hostnames (see
// internal/public). A local reverse proxy terminates TLS and routes by
// hostname; nothing above this package should have to know how.
//
// State ownership is deliberately outside: a Publisher starts and stops
// things, and reports what it did. Recording that in state.yaml is the
// caller's job, under the caller's lock — saving it from in here would race
// the clone-and-commit that background install jobs use.
package publish

import (
	"log"
	"sort"

	"github.com/lngstck/stackctl/internal/config"
)

// Status values reported by Status and AuthStatus. They are the strings the
// UI renders as badges.
const (
	// StatusRunning: published and believed reachable.
	StatusRunning = "running"
	// StatusStopped: deliberately not published.
	StatusStopped = "stopped"
	// StatusError: meant to be published but currently broken. The publisher
	// keeps trying; the badge exists so the admin notices.
	StatusError = "error"
)

// App is one publishable unit, as far as publishing is concerned.
type App struct {
	// ID is the app id and also its subdomain label.
	ID string
	// LocalPort is the port the app listens on from the server's point of
	// view — the host side of the container's port mapping.
	LocalPort int
	// ContainerPort is the port inside the container. The proxy reaches the
	// container directly over the docker network and needs this one.
	ContainerPort int
}

// Publisher is the transport-independent way to publish this install.
//
// Implementations must be safe for concurrent use: the web UI calls Status
// while background work changes things underneath.
type Publisher interface {
	// Kind reports the transport (config.Transport*), for UI wording only.
	// Behaviour must never branch on it outside this package.
	Kind() string

	// Refresh rewrites the proxy configuration from the current settings,
	// keeping every route — for a changed certificate setup.
	Refresh() error

	// EnsureAuth publishes the local Dex if it is not published already.
	// This is not optional in any mode — without it there is no login.
	EnsureAuth() error
	// AuthStatus reports the Dex publication status.
	AuthStatus() string
	// StartAuth and StopAuth are the admin's manual controls for it.
	StartAuth() error
	StopAuth() error

	// StartAdmin publishes stackctl's own web UI at admin.{base_domain},
	// listening on localPort. Unlike the login this is off unless the admin
	// asks for it: it puts the control plane of the whole install — installs,
	// secrets, restores — behind one password at one more address.
	//
	// It does not change what stackctl listens on. The LAN port stays open,
	// so a route that does not work costs nothing but a wrong bookmark.
	StartAdmin(localPort int) error
	// StopAdmin withdraws it. Withdrawing something unpublished is not an error.
	StopAdmin() error
	// AdminStatus reports the publication status of the UI itself.
	AdminStatus() string

	// Enable publishes an app and returns the public host it now answers on.
	Enable(app App) (host string, err error)
	// Disable withdraws it. Disabling something that is not published is not
	// an error.
	Disable(appID string) error
	// Restore publishes everything that was published before a restart. It
	// reports problems to the log rather than failing: one broken app must
	// not stop the rest from coming back.
	Restore(apps []App)

	// Status reports one app's publication status.
	Status(appID string) string

	// StartMonitor launches background supervision, if the transport has any.
	StartMonitor()
	// Shutdown stops everything this publisher owns.
	Shutdown()
}

// AppsFrom builds the publish list from the containers that state.yaml marks
// as public. containerPort resolves an app's in-container port and may be nil
// when that information is not available.
//
// Callers pass a snapshot, never the live state — the returned slice is what
// a Publisher works from, and it must not alias a map another goroutine may
// be committing to.
// Bootstrap brings public access up from a saved state: the login first, then
// every app that was published before, then the UI itself if the admin asked
// for that, then background supervision.
//
// It exists so the two callers cannot drift. One runs at service start, the
// other the moment setup completes — and an install-time step that only one
// of them performs is a slow-acting bug: it works until the next restart, or
// only after one. Failures are logged rather than fatal; a server that cannot
// publish must still serve the UI that fixes it.
func Bootstrap(p Publisher, state *config.State, containerPort func(string) int, adminPort int) {
	if p == nil {
		return
	}
	if err := p.EnsureAuth(); err != nil {
		log.Printf("publish: login: %v", err)
	}
	p.Restore(AppsFrom(state, containerPort))
	if state != nil && state.AdminPublished {
		if err := p.StartAdmin(adminPort); err != nil {
			log.Printf("publish: admin UI: %v", err)
		}
	}
	p.StartMonitor()
}

func AppsFrom(state *config.State, containerPort func(string) int) []App {
	if state == nil {
		return nil
	}
	var apps []App
	for id, cs := range state.Containers {
		if cs == nil || !cs.PublicEnabled || len(cs.Ports) == 0 {
			continue
		}
		app := App{ID: id, LocalPort: cs.Ports[0]}
		if containerPort != nil {
			app.ContainerPort = containerPort(id)
		}
		apps = append(apps, app)
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].ID < apps[j].ID })
	return apps
}
