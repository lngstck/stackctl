package tunnel

import (
	"fmt"
	"log"
	"os/exec"
	"sync"
	"time"

	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/paths"
)

// Status constants returned by Manager.Status.
const (
	StatusRunning = "running"
	StatusStopped = "stopped"
	StatusError   = "error"
)

// DexTunnelID is the fixed tunnel ID for the always-on Dex tunnel.
const DexTunnelID = "_dex"

// tunnelProc tracks a single autossh/ssh child process.
type tunnelProc struct {
	cmd        *exec.Cmd
	done       chan struct{} // closed when cmd.Wait() returns
	tunnelID   string
	remoteHost string
	localPort  int
	startedAt  time.Time
	failures   []time.Time // for failure-rate tracking in the monitor
}

// Manager owns all running tunnel processes. It is safe for concurrent use.
type Manager struct {
	mu      sync.Mutex
	tunnels map[string]*tunnelProc
	cfg     *config.Config
	state   *config.State
	stopCh  chan struct{} // closed to signal the monitor goroutine to exit
}

// New creates a tunnel Manager. Call EnsureDexTunnel and StartMonitor after
// creation in the startup sequence.
func New(cfg *config.Config, state *config.State) *Manager {
	return &Manager{
		tunnels: make(map[string]*tunnelProc),
		cfg:     cfg,
		state:   state,
		stopCh:  make(chan struct{}),
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
	cmd := buildSSHCmd(remoteHost, localPort, m.cfg.Tunnel.SSHHost, m.cfg.Tunnel.SSHPort, keyPath)
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
		return StatusStopped
	default:
		// Check for excessive recent failures.
		recent := countRecentFailures(p.failures, 5*time.Minute)
		if recent > 5 {
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

// RootDomain is the public root under which every school's subdomains live.
// The `-R`-Remote-Host muss der volle FQDN sein, weil sish den angegebenen
// Host 1:1 als Routing-Key benutzt — wenn wir nur `auth.{slug}` angeben,
// registriert sich der Tunnel als `auth.{slug}`, aber eingehende Requests
// kommen mit Host-Header `auth.{slug}.learningstack.online` und finden
// keine passende Registrierung ("cannot find connection for host: …").
const RootDomain = "learningstack.online"

// EnsureDexTunnel starts the mandatory Dex tunnel if it is not already running.
// The Dex tunnel forwards auth.{slug}.learningstack.online:80 to localhost:5556.
func (m *Manager) EnsureDexTunnel() error {
	remoteHost := "auth." + m.cfg.School.Slug + "." + RootDomain
	return m.Start(DexTunnelID, remoteHost, 5556)
}

// RestoreAppTunnels restarts tunnels for all apps that had tunnel_enabled=true
// in state.yaml. Called once during startup.
func (m *Manager) RestoreAppTunnels() {
	for id, cs := range m.state.Containers {
		if cs.TunnelEnabled && len(cs.Ports) > 0 {
			remoteHost := id + "." + m.cfg.School.Slug + "." + RootDomain
			if err := m.Start(id, remoteHost, cs.Ports[0]); err != nil {
				log.Printf("tunnel: restore %s: %v", id, err)
			}
		}
	}
}

// EnableAppTunnel starts a tunnel for an installed app and updates state.
func (m *Manager) EnableAppTunnel(appID string) error {
	cs, ok := m.state.Containers[appID]
	if !ok {
		return fmt.Errorf("app %s not installed", appID)
	}
	if len(cs.Ports) == 0 {
		return fmt.Errorf("app %s has no ports", appID)
	}
	remoteHost := appID + "." + m.cfg.School.Slug + "." + RootDomain
	if err := m.Start(appID, remoteHost, cs.Ports[0]); err != nil {
		return err
	}
	cs.TunnelEnabled = true
	cs.TunnelSubdomain = remoteHost
	return m.state.Save()
}

// DisableAppTunnel stops an app's tunnel and clears the state flags.
func (m *Manager) DisableAppTunnel(appID string) error {
	cs, ok := m.state.Containers[appID]
	if !ok {
		return fmt.Errorf("app %s not installed", appID)
	}
	if err := m.Stop(appID); err != nil {
		return err
	}
	cs.TunnelEnabled = false
	cs.TunnelSubdomain = ""
	return m.state.Save()
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

func countRecentFailures(failures []time.Time, window time.Duration) int {
	cutoff := time.Now().Add(-window)
	n := 0
	for _, t := range failures {
		if t.After(cutoff) {
			n++
		}
	}
	return n
}
