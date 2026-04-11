package tunnel

import (
	"log"
	"time"
)

const (
	monitorInterval = 30 * time.Second
	failureWindow   = 5 * time.Minute
	maxFailures     = 5
)

// StartMonitor launches a background goroutine that checks all tunnels every
// 30 seconds. Dead processes are restarted automatically. If a tunnel fails
// more than 5 times within 5 minutes, it is marked as "error" and no longer
// restarted until manually stopped and re-started.
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

	for id, p := range m.tunnels {
		select {
		case <-p.done:
			// Process has exited — record failure and maybe restart.
			p.failures = append(p.failures, time.Now())
			p.failures = pruneOld(p.failures, failureWindow)

			if len(p.failures) > maxFailures {
				log.Printf("tunnel: %s failed %d times in %v — marked as error",
					id, len(p.failures), failureWindow)
				continue // don't restart, leave in map so status shows "error"
			}

			log.Printf("tunnel: %s died, restarting (failure %d/%d)",
				id, len(p.failures), maxFailures)

			// Keep failure history across restarts.
			failures := p.failures
			remoteHost := p.remoteHost
			localPort := p.localPort

			delete(m.tunnels, id)
			if err := m.startLocked(id, remoteHost, localPort); err != nil {
				log.Printf("tunnel: restart %s: %v", id, err)
			} else if newP, ok := m.tunnels[id]; ok {
				newP.failures = failures
			}
		default:
			// Still running — nothing to do.
		}
	}
}

// pruneOld removes entries older than the window from a time slice.
func pruneOld(times []time.Time, window time.Duration) []time.Time {
	cutoff := time.Now().Add(-window)
	out := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			out = append(out, t)
		}
	}
	return out
}
