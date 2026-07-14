// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package tunnel

import (
	"net/http"
	"os/exec"
	"testing"
	"time"
)

// resp builds a minimal http.Response for signature classification tests.
func resp(status int, contentLength int64, contentType string) *http.Response {
	h := http.Header{}
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	return &http.Response{StatusCode: status, ContentLength: contentLength, Header: h}
}

func TestIsEdgeUnrouted(t *testing.T) {
	cases := []struct {
		name string
		r    *http.Response
		want bool
	}{
		// Empirisch verifizierte sish-v2.23.0-Signatur fuer ungebundene
		// Hosts: 404, Content-Length 0, kein Content-Type.
		{"sish edge 404", resp(404, 0, ""), true},
		{"Go stdlib 404 (App lebt)", resp(404, 19, "text/plain; charset=utf-8"), false},
		{"leere 404 MIT Content-Type (App lebt)", resp(404, 0, "text/html"), false},
		{"200 OK", resp(200, 1381, "application/json"), false},
		{"302 Redirect", resp(302, 0, ""), false},
		{"502 sish→App down (Tunnel routet!)", resp(502, 0, ""), false},
	}
	for _, c := range cases {
		if got := isEdgeUnrouted(c.r); got != c.want {
			t.Errorf("%s: isEdgeUnrouted = %v, want %v", c.name, got, c.want)
		}
	}
}

// settledProc starts a real throwaway process (sleep) so the routing check
// exercises genuine process/done-channel semantics, and backdates startedAt
// past the settle window.
func settledProc(t *testing.T, id string) *tunnelProc {
	t.Helper()
	cmd := exec.Command("sleep", "300")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start dummy process: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
	})
	return &tunnelProc{
		cmd:        cmd,
		done:       done,
		tunnelID:   id,
		remoteHost: id + ".test." + RootDomain,
		startedAt:  time.Now().Add(-2 * settleDuration),
	}
}

// Drei aufeinanderfolgende "unrouted"-Probes muessen den Prozess beenden
// (ohne ihn aus der Map zu entfernen — der Reconnect-Pfad uebernimmt);
// zwischendurch ein routingOK setzt den Zaehler zurueck.
func TestCheckRouting_ForcesRebindAfterThreshold(t *testing.T) {
	verdict := routingUnrouted
	m := &Manager{
		tunnels: map[string]*tunnelProc{},
		probe:   func(string) routingResult { return verdict },
	}
	p := settledProc(t, "dex")
	m.tunnels["dex"] = p

	// Zwei Fehlschlaege: Prozess lebt noch.
	m.checkRouting()
	m.checkRouting()
	select {
	case <-p.done:
		t.Fatal("process killed before reaching the threshold")
	default:
	}
	if p.routingFailures != 2 {
		t.Fatalf("routingFailures = %d, want 2", p.routingFailures)
	}

	// Ein OK dazwischen setzt zurueck.
	verdict = routingOK
	m.checkRouting()
	if p.routingFailures != 0 {
		t.Fatalf("routingFailures nach OK = %d, want 0", p.routingFailures)
	}

	// Dreimal unrouted → SIGTERM, done schliesst sich, Eintrag bleibt.
	verdict = routingUnrouted
	m.checkRouting()
	m.checkRouting()
	m.checkRouting()

	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		t.Fatal("process was not terminated after reaching the threshold")
	}
	if _, ok := m.tunnels["dex"]; !ok {
		t.Fatal("tunnel entry removed from map — reconnect path would never respawn it")
	}
}

// routingUnknown (Netzfehler/Timeout) darf weder zaehlen noch zuruecksetzen.
func TestCheckRouting_UnknownIsNeutral(t *testing.T) {
	m := &Manager{
		tunnels: map[string]*tunnelProc{},
		probe:   func(string) routingResult { return routingUnknown },
	}
	p := settledProc(t, "app")
	p.routingFailures = 2
	m.tunnels["app"] = p

	m.checkRouting()
	if p.routingFailures != 2 {
		t.Fatalf("routingFailures = %d, want unveraendert 2", p.routingFailures)
	}
}

// Frisch gestartete Tunnel (innerhalb settleDuration) werden nicht geprobt —
// das wuerde die DNS-Propagation eines Erst-Binds racen.
func TestCheckRouting_SkipsUnsettledTunnels(t *testing.T) {
	probed := 0
	m := &Manager{
		tunnels: map[string]*tunnelProc{},
		probe: func(string) routingResult {
			probed++
			return routingUnrouted
		},
	}
	p := settledProc(t, "fresh")
	p.startedAt = time.Now() // eben erst gestartet
	m.tunnels["fresh"] = p

	m.checkRouting()
	if probed != 0 {
		t.Fatalf("frischer Tunnel wurde geprobt (%d mal)", probed)
	}
}
