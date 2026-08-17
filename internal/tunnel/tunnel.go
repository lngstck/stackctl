package tunnel

import (
	"fmt"
	"log"
	"os/exec"
	"sync"
	"time"

	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/paths"
	"github.com/lngstck/stackctl/internal/public"
)

// Status constants returned by Manager.Status.
const (
	StatusRunning = "running"
	StatusStopped = "stopped"
	StatusError   = "error"
)

// DexTunnelID is the fixed tunnel ID for the always-on Dex tunnel.
const DexTunnelID = "_dex"

// AdminTunnelID is the fixed tunnel ID for stackctl's own web UI. Like
// DexTunnelID it starts with an underscore, which no app id can.
const AdminTunnelID = "_admin"

// tunnelProc tracks a single autossh/ssh child process.
type tunnelProc struct {
	cmd        *exec.Cmd
	done       chan struct{} // closed when cmd.Wait() returns
	tunnelID   string
	remoteHost string
	localPort  int
	startedAt  time.Time

	// Reconnect bookkeeping owned by the monitor (monitor.go). consecutive
	// Failures counts deaths since the last recovery; nextRetryAt gates the
	// backoff so we don't retry (or log) every tick. Both are zero on a
	// freshly started, healthy tunnel.
	consecutiveFailures int
	nextRetryAt         time.Time

	// routingFailures counts consecutive "sish does not route this host"
	// probes (routing.go). At routingFailThreshold the monitor force-kills
	// the process so the regular reconnect path rebinds it.
	routingFailures int
}

// Manager owns all running tunnel processes. It is safe for concurrent use.
type Manager struct {
	mu      sync.Mutex
	tunnels map[string]*tunnelProc
	cfg     *config.Config
	stopCh  chan struct{} // closed to signal the monitor goroutine to exit

	// probe checks whether a public tunnel host is actually routed by sish
	// (routing.go). A field so tests can substitute a fake.
	probe func(host string) routingResult
}

// New creates a tunnel Manager. Call EnsureDexTunnel and StartMonitor after
// creation in the startup sequence.
//
// The Manager deliberately holds no state.State pointer. It used to, and
// mutated plus saved state.yaml from inside Enable/Disable — outside the
// server's stateMu, and racing the clone-and-commit that background jobs use.
// Ownership of state now sits entirely with the caller (see internal/publish).
func New(cfg *config.Config) *Manager {
	return &Manager{
		tunnels: make(map[string]*tunnelProc),
		cfg:     cfg,
		stopCh:  make(chan struct{}),
		probe:   probePublicURL,
	}
}

// Start launches a tunnel process. If a tunnel with the same ID is already
// running, Start is a no-op and returns nil.
func (m *Manager) Start(tunnelID, remoteHost string, localPort int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startLocked(tunnelID, remoteHost, localPort)
}

func (m *Manager) startLocked(tunnelID, remoteHost string, localPort int) error {
	if p, ok := m.tunnels[tunnelID]; ok {
		select {
		case <-p.done:
			// Process died — clean up and re-create below.
		default:
			return nil // still running
		}
	}

	keyPath := paths.TunnelKeyFile()
	cmd := buildSSHCmd(remoteHost, localPort, m.cfg.Public.Relay.SSHHost, m.cfg.Public.Relay.SSHPort, keyPath)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start tunnel %s: %w", tunnelID, err)
	}

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	m.tunnels[tunnelID] = &tunnelProc{
		cmd:        cmd,
		done:       done,
		tunnelID:   tunnelID,
		remoteHost: remoteHost,
		localPort:  localPort,
		startedAt:  time.Now(),
	}
	log.Printf("tunnel: started %s → %s:80 ← localhost:%d", tunnelID, remoteHost, localPort)
	return nil
}

// Stop terminates a running tunnel. Returns nil if the tunnel is not running.
func (m *Manager) Stop(tunnelID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.tunnels[tunnelID]
	if !ok {
		return nil
	}
	return m.killProc(p)
}

// killProc terminates the process and removes it from the map. Caller must
// hold m.mu.
func (m *Manager) killProc(p *tunnelProc) error {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
	}
	delete(m.tunnels, p.tunnelID)
	log.Printf("tunnel: stopped %s", p.tunnelID)
	return nil
}

// Status returns the status of a single tunnel.
func (m *Manager) Status(tunnelID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked(tunnelID)
}

func (m *Manager) statusLocked(tunnelID string) string {
	p, ok := m.tunnels[tunnelID]
	if !ok {
		return StatusStopped
	}
	select {
	case <-p.done:
		// Dead but still tracked → the monitor is retrying it with backoff.
		// Surface as "error" rather than "stopped" so the admin can tell the
		// difference between "I turned it off" and "it broke".
		return StatusError
	default:
		if p.consecutiveFailures >= errorThreshold {
			// Alive this instant but flapping — keep the badge red so the
			// admin notices, even though the monitor keeps healing it.
			return StatusError
		}
		return StatusRunning
	}
}

// AllStatuses returns a snapshot of tunnel statuses keyed by tunnel ID.
func (m *Manager) AllStatuses() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]string, len(m.tunnels))
	for id := range m.tunnels {
		out[id] = m.statusLocked(id)
	}
	return out
}

// EnsureDexTunnel starts the mandatory Dex tunnel if it is not already
// running. It forwards auth.{base_domain}:80 to localhost:5556.
//
// The `-R` remote host is always the full FQDN from internal/public, because
// sish uses the requested name verbatim as its routing key — see the note on
// public.Host.
func (m *Manager) EnsureDexTunnel() error {
	remoteHost := public.AuthHost(m.cfg)
	return m.Start(DexTunnelID, remoteHost, 5556)
}

// StartDexTunnel (re)starts the Dex tunnel from the UI. Identical to
// EnsureDexTunnel, but named for the explicit manual-control intent. If the
// tunnel is currently in the error state (dead but tracked), Start detects the
// closed done-channel and respawns a fresh process with reset backoff.
func (m *Manager) StartDexTunnel() error {
	return m.EnsureDexTunnel()
}

// StopDexTunnel stops the Dex tunnel from the UI. Because Stop removes it from
// the tunnel map, the monitor will not resurrect it — it stays down until the
// admin starts it again or stackctl restarts (which re-runs EnsureDexTunnel).
func (m *Manager) StopDexTunnel() error {
	return m.Stop(DexTunnelID)
}

// Config returns the config this manager was built with, so relay-specific
// UI code can read the endpoint without a second copy of the pointer.
func (m *Manager) Config() *config.Config { return m.cfg }

// StartApp opens a tunnel for one app and returns the public host it is
// bound to. It does not touch state.yaml — the caller records the result.
func (m *Manager) StartApp(appID string, localPort int) (string, error) {
	if localPort == 0 {
		return "", fmt.Errorf("app %s has no port to forward", appID)
	}
	remoteHost := public.AppHost(m.cfg, appID)
	if remoteHost == "" {
		return "", fmt.Errorf("app %s: install has no public address", appID)
	}
	if err := m.Start(appID, remoteHost, localPort); err != nil {
		return "", err
	}
	return remoteHost, nil
}

// StopApp closes an app's tunnel. Stopping an unknown tunnel is not an error.
func (m *Manager) StopApp(appID string) error {
	return m.Stop(appID)
}

// Shutdown stops all tunnels and signals the monitor goroutine to exit.
func (m *Manager) Shutdown() {
	close(m.stopCh)
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.tunnels {
		m.killProc(p)
	}
}
