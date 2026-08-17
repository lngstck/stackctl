// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package publish

import (
	"fmt"
	"log"

	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/paths"
	"github.com/lngstck/stackctl/internal/public"
	"github.com/lngstck/stackctl/internal/tunnel"
)

// Relay publishes through SSH reverse tunnels to a sish endpoint. The server
// may sit behind NAT; TLS terminates at the relay, which therefore sees
// request contents in the clear.
//
// Whether that endpoint is operator-run or school-run makes no difference
// here — it is the same tunnel, and the difference lives in config.public.
type Relay struct {
	mgr *tunnel.Manager
}

// NewRelay wires a relay publisher onto the tunnel manager.
func NewRelay(cfg *config.Config) *Relay {
	return &Relay{mgr: tunnel.New(cfg)}
}

// Manager exposes the underlying tunnel manager for the parts of the UI that
// are inherently relay-specific (the SSH key block on the public-access
// page). Everything else must go through the Publisher interface.
func (r *Relay) Manager() *tunnel.Manager { return r.mgr }

func (r *Relay) Kind() string { return KindRelay }

// EnsureAuth brings up the mandatory Dex tunnel. It also makes sure the SSH
// key exists — without it every tunnel start would fail with a confusing
// "no such file" from ssh.
func (r *Relay) EnsureAuth() error {
	if err := tunnel.EnsureKey(); err != nil {
		return err
	}
	return r.mgr.EnsureDexTunnel()
}

func (r *Relay) AuthStatus() string { return r.mgr.Status(tunnel.DexTunnelID) }
func (r *Relay) StartAuth() error   { return r.mgr.StartDexTunnel() }
func (r *Relay) StopAuth() error    { return r.mgr.StopDexTunnel() }

// StartAdmin forwards admin.{base_domain} to the port stackctl listens on.
// Same machinery as any app tunnel — the local end just happens to be
// stackctl itself rather than a container.
func (r *Relay) StartAdmin(localPort int) error {
	if localPort <= 0 {
		return fmt.Errorf("publish: unknown stackctl port")
	}
	remoteHost := public.AdminHost(r.mgr.Config())
	if remoteHost == "" {
		return fmt.Errorf("publish: install has no public address")
	}
	return r.mgr.Start(tunnel.AdminTunnelID, remoteHost, localPort)
}

func (r *Relay) StopAdmin() error { return r.mgr.Stop(tunnel.AdminTunnelID) }

func (r *Relay) AdminStatus() string { return r.mgr.Status(tunnel.AdminTunnelID) }

func (r *Relay) Enable(app App) (string, error) {
	return r.mgr.StartApp(app.ID, app.LocalPort)
}

func (r *Relay) Disable(appID string) error { return r.mgr.StopApp(appID) }

func (r *Relay) Restore(apps []App) {
	for _, a := range apps {
		if _, err := r.mgr.StartApp(a.ID, a.LocalPort); err != nil {
			log.Printf("publish: restore %s: %v", a.ID, err)
		}
	}
}

func (r *Relay) Status(appID string) string { return r.mgr.Status(appID) }

func (r *Relay) StartMonitor() { r.mgr.StartMonitor() }
func (r *Relay) Shutdown()     { r.mgr.Shutdown() }

// TestTransport performs an SSH handshake against the relay, which proves key,
// network path and authorisation in one go.
func (r *Relay) TestTransport() error {
	host, port := r.Endpoint()
	return tunnel.TestConnection(host, port, paths.TunnelKeyFile())
}

// PublicKey returns the SSH key the operator authorises on the relay.
func (r *Relay) PublicKey() (string, error) { return tunnel.PublicKey() }

// Endpoint returns the relay this install dials.
func (r *Relay) Endpoint() (string, int) {
	cfg := r.mgr.Config()
	return cfg.Public.Relay.SSHHost, cfg.Public.Relay.SSHPort
}
