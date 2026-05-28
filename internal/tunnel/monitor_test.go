package tunnel

import (
	"testing"
	"time"
)

func TestComputeBackoff(t *testing.T) {
	cases := []struct {
		failures int
		want     time.Duration
	}{
		{0, baseBackoff},  // clamped to ≥1 → baseBackoff
		{1, baseBackoff},  // 5s
		{2, 10 * time.Second},
		{3, 20 * time.Second},
		{4, 40 * time.Second},
		{5, 80 * time.Second},
		{100, maxBackoff}, // far past cap → clamped
	}
	for _, c := range cases {
		got := computeBackoff(c.failures)
		if got != c.want {
			t.Errorf("computeBackoff(%d) = %s, want %s", c.failures, got, c.want)
		}
	}
}

func TestComputeBackoff_NeverExceedsMaxOrOverflows(t *testing.T) {
	// The shift can overflow for large exponents; ensure we always return a
	// sane, positive, capped value.
	for f := 0; f < 1000; f++ {
		d := computeBackoff(f)
		if d <= 0 {
			t.Fatalf("computeBackoff(%d) = %s (non-positive)", f, d)
		}
		if d > maxBackoff {
			t.Fatalf("computeBackoff(%d) = %s > maxBackoff %s", f, d, maxBackoff)
		}
	}
}

// aliveProc returns a tunnelProc whose done channel is open (process running).
func aliveProc(id string, failures int) *tunnelProc {
	return &tunnelProc{
		done:                make(chan struct{}),
		tunnelID:            id,
		consecutiveFailures: failures,
		startedAt:           time.Now(),
	}
}

// deadProc returns a tunnelProc whose done channel is closed (process exited).
func deadProc(id string, failures int) *tunnelProc {
	done := make(chan struct{})
	close(done)
	return &tunnelProc{
		done:                done,
		tunnelID:            id,
		consecutiveFailures: failures,
	}
}

func TestStatusLocked(t *testing.T) {
	m := &Manager{tunnels: map[string]*tunnelProc{}}

	// Not in map → stopped.
	if got := m.statusLocked("missing"); got != StatusStopped {
		t.Errorf("missing tunnel: got %q, want %q", got, StatusStopped)
	}

	// Alive, healthy → running.
	m.tunnels["healthy"] = aliveProc("healthy", 0)
	if got := m.statusLocked("healthy"); got != StatusRunning {
		t.Errorf("healthy tunnel: got %q, want %q", got, StatusRunning)
	}

	// Alive but flapping (failures ≥ threshold) → error, so the admin notices
	// even though the monitor keeps healing it.
	m.tunnels["flapping"] = aliveProc("flapping", errorThreshold)
	if got := m.statusLocked("flapping"); got != StatusError {
		t.Errorf("flapping tunnel: got %q, want %q", got, StatusError)
	}

	// Dead but still tracked → error (monitor retrying), NOT stopped.
	m.tunnels["dead"] = deadProc("dead", 3)
	if got := m.statusLocked("dead"); got != StatusError {
		t.Errorf("dead tunnel: got %q, want %q", got, StatusError)
	}
}

// TestCheckAndRestart_RespectsBackoffWindow verifies that a dead tunnel within
// its backoff window is left untouched (no respawn attempt, no counter bump).
// This is the fix for the original bug where every monitor tick re-counted the
// same dead process as a fresh failure.
func TestCheckAndRestart_RespectsBackoffWindow(t *testing.T) {
	m := &Manager{tunnels: map[string]*tunnelProc{}}

	// Dead proc whose next retry is comfortably in the future.
	p := deadProc("dex", 7)
	p.nextRetryAt = time.Now().Add(time.Hour)
	p.remoteHost = "auth.test.learningstack.online"
	p.localPort = 5556
	m.tunnels["dex"] = p

	m.checkAndRestart()

	// Still present, untouched: no respawn (which would have replaced it with
	// a fresh proc) and crucially the failure counter did NOT grow — proving
	// we no longer re-count the same dead process on every tick.
	got, ok := m.tunnels["dex"]
	if !ok {
		t.Fatal("dead tunnel was removed despite being within its backoff window")
	}
	if got.consecutiveFailures != 7 {
		t.Errorf("consecutiveFailures = %d, want 7 (must not increment within backoff window)", got.consecutiveFailures)
	}
}
