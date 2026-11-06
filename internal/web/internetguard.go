// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import (
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/public"
	"github.com/lngstck/stackctl/internal/publish"
)

// The control plane speaks plain HTTP on its own port. In the school network
// that port is only reachable from inside, which is the whole idea. On a
// server that hangs on the internet itself (a VPS, a VM with a public
// address) the same port would put the setup code and the admin password on
// the internet in clear text, and the login form in front of everyone.
//
// internetGuard closes that gap in stackctl itself rather than relying on a
// firewall nobody set up: requests from public addresses get a page that
// explains the way in instead of the UI. Everything that is not the public
// internet still gets through —
//
//   - loopback: an SSH tunnel (ssh -L 8090:localhost:8090 …), the way in on
//     a VPS;
//   - private, link-local and shared (CGNAT) addresses: the server's own
//     network, a VPN such as WireGuard or Tailscale, and Caddy, which reaches
//     this port from the docker network when admin.<domain> is switched on.
//
// The check uses the TCP peer, never X-Forwarded-For: a header is whatever
// the client wants it to be.
//
// When it is active:
//
//   - before setup, always. The mode is not chosen yet, and the setup code
//     and the new admin password are exactly what must not cross the
//     internet in clear. Checking the server's own address instead would
//     miss cloud VMs behind 1:1 NAT (AWS, Azure, Google), which only see a
//     private address on their interface. In the school network the setup
//     link from install.sh carries the server's private IPv4, so the admin
//     comes from a private address and nothing changes there.
//   - after setup, in the direct mode only. In the school-network mode the
//     guard is never active, whatever the addresses look like: schools with
//     public IPv4 ranges inside (BelWü, for one) keep reaching the plain
//     port exactly as before.

// cgnat is the shared address space of carrier-grade NAT (RFC 6598), also
// used by overlay networks such as Tailscale. The internet cannot send from it.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// publicAddr reports whether a is an address on the public internet.
func publicAddr(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() {
		return true
	}
	return !(a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() ||
		a.IsUnspecified() || cgnat.Contains(a))
}

// fromInternet reports whether the request's TCP peer is a public address.
// An address that does not parse counts as public: the guard fails closed.
func fromInternet(r *http.Request) bool {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return true
	}
	return publicAddr(ap.Addr())
}

// guardsPlainPort reports whether the plain port must not answer the
// internet right now; see the comment at the top of this file.
func (s *Server) guardsPlainPort() bool {
	if s.cfg.SetupState == config.SetupStateNeedsSetup {
		return true
	}
	return s.cfg.Public.Transport == config.TransportDirect
}

// internetGuard wraps the whole UI; see the comment at the top of this file.
// Static assets stay reachable so the explanation page has its styles; they
// are the same for every install and hold nothing secret.
func (s *Server) internetGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		static := (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
			strings.HasPrefix(r.URL.Path, "/static/")
		if static || !s.guardsPlainPort() || !fromInternet(r) {
			next.ServeHTTP(w, r)
			return
		}
		s.render(&statusWriter{ResponseWriter: w, code: http.StatusForbidden}, "remote.html.tmpl", s.remoteData(r))
	})
}

// remoteData is what the explanation page needs to show the way in.
type remoteData struct {
	Setup      bool   // setup is still to be done
	Port       int    // the port this UI listens on
	ServerHost string // the address the browser used, for the ssh line
	LocalURL   string // the same page through the tunnel
	AdminURL   string // https://admin.<domain>, if it is switched on and up
}

func (s *Server) remoteData(r *http.Request) remoteData {
	port := s.listenPort
	if port <= 0 {
		port = 8090
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		host = "<server>"
	}

	// Through the tunnel, the same page: the setup link from install.sh then
	// only needs a click once the tunnel is up. The code in it is the one the
	// browser just sent.
	target := "/"
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/") && !strings.HasPrefix(r.URL.Path, "//") {
		target = r.URL.Path
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
	}

	d := remoteData{
		Setup:      s.cfg.SetupState == config.SetupStateNeedsSetup,
		Port:       port,
		ServerHost: host,
		LocalURL:   "http://localhost:" + strconv.Itoa(port) + target,
	}
	if !d.Setup && s.publisher != nil && s.publisher.AdminStatus() == publish.StatusRunning {
		if admin := public.AdminHost(s.cfg); admin != "" {
			d.AdminURL = "https://" + admin
		}
	}
	return d
}

// statusWriter sends code instead of the implicit 200 of the first Write,
// so a page can go through the normal render path with another status.
type statusWriter struct {
	http.ResponseWriter
	code  int
	wrote bool
}

func (w *statusWriter) WriteHeader(code int) {
	if w.wrote {
		return
	}
	w.wrote = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(w.code)
	}
	return w.ResponseWriter.Write(b)
}
