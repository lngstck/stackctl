// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package publish

import (
	"fmt"
	"log"
	"net"
	"strconv"
	"sync"

	"github.com/lngstck/stackctl/internal/caddy"
	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/dex"
	"github.com/lngstck/stackctl/internal/docker"
	"github.com/lngstck/stackctl/internal/public"
)

// dexContainerPort is the port the local Dex listens on inside its container.
// Fixed by the dex catalog definition's web.http setting.
const dexContainerPort = 5556

// Proxy serves every address through a local reverse proxy that holds
// 80/443, terminates TLS and routes by hostname. Nothing leaves the machine
// unencrypted, and no third party sees request contents. Both transports use
// it; they differ only in the certificate setup, which internal/caddy derives
// from the config.
//
// It keeps the routing table in memory and rewrites the whole Caddyfile on
// every change, in the same spirit as the generated Dex config: there is one
// writer and no partial edits. That also makes Restore trivially correct — it
// is the same code path as any other change, just with more routes at once.
type Proxy struct {
	cfg *config.Config

	// hostAddress reports the address containers reach this host on. It is a
	// field so tests can answer it without a docker daemon.
	hostAddress func() (string, error)

	mu     sync.Mutex
	routes map[string]caddy.Route // key: app id, authRouteKey or adminRouteKey
}

// authRouteKey and adminRouteKey identify the two routes that are not apps.
// Neither can collide with an app id, which is a validated slug and never
// starts with an underscore.
const (
	authRouteKey  = "_auth"
	adminRouteKey = "_admin"
)

// NewProxy wires a publisher onto the local reverse proxy.
func NewProxy(cfg *config.Config) *Proxy {
	return &Proxy{
		cfg:         cfg,
		hostAddress: docker.NetworkGateway,
		routes:      map[string]caddy.Route{},
	}
}

// Kind reports the transport the proxy serves, for UI wording.
func (d *Proxy) Kind() string { return d.cfg.Public.Transport }

// Refresh rewrites the proxy config from the current settings without
// changing any route — for a changed certificate setup.
func (d *Proxy) Refresh() error { return d.apply() }

// EnsureAuth publishes the local Dex under auth.{base_domain}.
func (d *Proxy) EnsureAuth() error {
	host := public.AuthHost(d.cfg)
	if host == "" {
		return fmt.Errorf("publish: install has no public address")
	}
	d.mu.Lock()
	d.routes[authRouteKey] = caddy.Route{
		Host:     host,
		Upstream: fmt.Sprintf("%s:%d", dex.DexContainerName, dexContainerPort),
	}
	d.mu.Unlock()
	return d.apply()
}

// AuthStatus reports whether the login is currently reachable.
func (d *Proxy) AuthStatus() string { return d.statusFor(authRouteKey) }

func (d *Proxy) StartAuth() error { return d.EnsureAuth() }

// StopAuth withdraws the Dex route. This breaks every login, which is why the
// UI asks before offering it — but the control has to exist, otherwise a
// broken route could only be cleared by editing files on the server.
func (d *Proxy) StopAuth() error { return d.remove(authRouteKey) }

// StartAdmin routes admin.{base_domain} to stackctl itself.
//
// The upstream is the host, not a container: stackctl runs under systemd. The
// proxy reaches it at the gateway address of the shared network, which is
// also why this keeps working only as long as stackctl listens on more than
// the loopback interface.
func (d *Proxy) StartAdmin(localPort int) error {
	host := public.AdminHost(d.cfg)
	if host == "" {
		return fmt.Errorf("publish: install has no public address")
	}
	if localPort <= 0 {
		return fmt.Errorf("publish: unknown stackctl port")
	}
	gateway, err := d.hostAddress()
	if err != nil {
		// The usual cause is that the proxy was never installed, so its
		// network does not exist. Saying that beats passing the daemon's
		// wording through to an admin who did not ask about networks.
		return fmt.Errorf("der Reverse-Proxy ist noch nicht eingerichtet — bitte zuerst Caddy installieren (%w)", err)
	}

	d.mu.Lock()
	previous, existed := d.routes[adminRouteKey]
	d.routes[adminRouteKey] = caddy.Route{
		Host:     host,
		Upstream: net.JoinHostPort(gateway, strconv.Itoa(localPort)),
	}
	d.mu.Unlock()

	if err := d.apply(); err != nil {
		d.mu.Lock()
		if existed {
			d.routes[adminRouteKey] = previous
		} else {
			delete(d.routes, adminRouteKey)
		}
		d.mu.Unlock()
		return err
	}
	return nil
}

func (d *Proxy) StopAdmin() error { return d.remove(adminRouteKey) }

func (d *Proxy) AdminStatus() string { return d.statusFor(adminRouteKey) }

// Enable adds an app's route and reloads the proxy.
func (d *Proxy) Enable(app App) (string, error) {
	host := public.AppHost(d.cfg, app.ID)
	if host == "" {
		return "", fmt.Errorf("publish %s: install has no public address", app.ID)
	}
	port := app.ContainerPort
	if port == 0 {
		// Without the in-container port there is nothing to proxy to. The
		// proxy sits on the container network, not on the host, and
		// guessing would produce a route that silently 502s.
		return "", fmt.Errorf("publish %s: unknown container port — is the catalog definition cached?", app.ID)
	}

	d.mu.Lock()
	previous, existed := d.routes[app.ID]
	d.routes[app.ID] = caddy.Route{Host: host, Upstream: caddy.Upstream(app.ID, port)}
	d.mu.Unlock()

	if err := d.apply(); err != nil {
		// Put the table back the way it was, so it keeps describing what the
		// proxy is actually serving.
		d.mu.Lock()
		if existed {
			d.routes[app.ID] = previous
		} else {
			delete(d.routes, app.ID)
		}
		d.mu.Unlock()
		return "", err
	}
	return host, nil
}

func (d *Proxy) Disable(appID string) error { return d.remove(appID) }

func (d *Proxy) Restore(apps []App) {
	for _, a := range apps {
		if _, err := d.Enable(a); err != nil {
			log.Printf("publish: restore %s: %v", a.ID, err)
		}
	}
}

func (d *Proxy) Status(appID string) string { return d.statusFor(appID) }

// StartMonitor is a no-op for now. There is no process to supervise: the proxy is a container with restart: unless-stopped, and it
// renews certificates itself. The checks worth adding here — certificate
// expiry, DNS drift, an end-to-end probe — belong with the health cards in
// the public-access UI and land with them.
func (d *Proxy) StartMonitor() {}

// Shutdown leaves the proxy running. stackctl restarting must not take every
// published app offline with it — the proxy's lifecycle belongs to Docker,
// exactly like the apps it serves.
func (d *Proxy) Shutdown() {}

// statusFor reports one route's status. A route the proxy is not running for
// is an error rather than "stopped": the admin asked for it to be published,
// and it is not.
func (d *Proxy) statusFor(key string) string {
	d.mu.Lock()
	_, ok := d.routes[key]
	d.mu.Unlock()
	if !ok {
		return StatusStopped
	}
	if !caddy.IsRunning() {
		return StatusError
	}
	return StatusRunning
}

// remove drops a route and reloads. Removing an unknown route is a no-op.
func (d *Proxy) remove(key string) error {
	d.mu.Lock()
	_, ok := d.routes[key]
	if ok {
		delete(d.routes, key)
	}
	d.mu.Unlock()
	if !ok {
		return nil
	}
	return d.apply()
}

// apply renders the current table and hands it to the proxy.
func (d *Proxy) apply() error {
	d.mu.Lock()
	routes := make([]caddy.Route, 0, len(d.routes))
	for _, r := range d.routes {
		routes = append(routes, r)
	}
	d.mu.Unlock()

	_, err := caddy.Apply(d.cfg, routes)
	return err
}
