package tunnel

import (
	"log"
	"time"
)

const (
	// monitorInterval is how often the monitor checks tunnel liveness.
	monitorInterval = 10 * time.Second

	// baseBackoff / maxBackoff bound the exponential reconnect delay. A dead
	// tunnel is retried after baseBackoff, then 2×, 4×, … up to maxBackoff.
	// Crucially the monitor NEVER gives up — a school server tunnel must
	// self-heal after an outage (overnight suspend, sish restart, ISP blip),
	// not stay dead until someone SSHes in.
	baseBackoff = 5 * time.Second
	maxBackoff  = 5 * time.Minute

	// settleDuration is how long a respawned tunnel must stay up before we
	// consider it recovered and reset its failure counter. An ssh process is
	// briefly "alive" even while its reverse forward is about to fail
	// (ExitOnForwardFailure exits a moment later), so resetting on mere
	// process-liveness would defeat the backoff. Require real uptime.
	settleDuration = 60 * time.Second

	// errorThreshold is the consecutive-failure count at which Status reports
	// "error" for the UI. The monitor keeps retrying past this — it only
	// drives the status badge so the admin notices a flapping tunnel.
	errorThreshold = 5
)

// StartMonitor launches a background goroutine that checks all tunnels every
// monitorInterval. Dead tunnels are reconnected with exponential backoff and
// retried indefinitely. See the const block for the reliability rationale.
func (m *Manager) StartMonitor() {
	go m.monitorLoop()
}

func (m *Manager) monitorLoop() {
	ticker := time.NewTicker(monitorInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.checkAndRestart()
		}
	}
}

func (m *Manager) checkAndRestart() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for id, p := range m.tunnels {
		select {
		case <-p.done:
			// Dead. Wait out the backoff window before the next attempt —
			// this is also what keeps the log quiet (no per-tick spam).
			if !p.nextRetryAt.IsZero() && now.Before(p.nextRetryAt) {
				continue
			}

			p.consecutiveFailures++
			backoff := computeBackoff(p.consecutiveFailures)
			log.Printf("tunnel: %s down — reconnect attempt %d (next retry in %s if this fails)",
				id, p.consecutiveFailures, backoff)

			failures := p.consecutiveFailures
			remoteHost := p.remoteHost
			localPort := p.localPort

			delete(m.tunnels, id)
			if err := m.startLocked(id, remoteHost, localPort); err != nil {
				// Couldn't even spawn (e.g. ssh binary missing). Re-insert a
				// dead placeholder so we keep retrying on the backoff cadence
				// instead of silently dropping the tunnel from the map.
				log.Printf("tunnel: %s respawn failed: %v", id, err)
				m.insertDeadLocked(id, remoteHost, localPort, failures, now.Add(backoff))
				continue
			}
			// startLocked made a fresh proc with zeroed counters — carry the
			// failure count forward and arm the next retry deadline.
			if newP, ok := m.tunnels[id]; ok {
				newP.consecutiveFailures = failures
				newP.nextRetryAt = now.Add(backoff)
			}

		default:
			// Alive. Only declare recovery once it has stayed up past the
			// settle window, otherwise a flapping tunnel resets its backoff
			// every cycle and hammers sish.
			if p.consecutiveFailures > 0 && now.Sub(p.startedAt) >= settleDuration {
				log.Printf("tunnel: %s recovered (stable for %s)", id, settleDuration)
				p.consecutiveFailures = 0
				p.nextRetryAt = time.Time{}
			}
		}
	}
}

// computeBackoff returns baseBackoff·2^(failures-1), clamped to maxBackoff.
// Guards against shift overflow for large failure counts.
func computeBackoff(failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	shift := failures - 1
	if shift > 20 { // 5s<<20 ≫ maxBackoff already; avoid overflow
		return maxBackoff
	}
	d := baseBackoff << uint(shift)
	if d <= 0 || d > maxBackoff {
		return maxBackoff
	}
	return d
}

// insertDeadLocked re-inserts a tunnel entry whose done channel is already
// closed, so the next monitor pass retries it once nextRetryAt elapses. Used
// when a respawn fails to start at all. Caller must hold m.mu.
func (m *Manager) insertDeadLocked(id, remoteHost string, localPort, failures int, nextRetryAt time.Time) {
	done := make(chan struct{})
	close(done)
	m.tunnels[id] = &tunnelProc{
		done:                done,
		tunnelID:            id,
		remoteHost:          remoteHost,
		localPort:           localPort,
		consecutiveFailures: failures,
		nextRetryAt:         nextRetryAt,
	}
}
