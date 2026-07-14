// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

// End-to-end routing check for tunnels. A tunnel process can be alive while
// sish is not actually routing its public host: since sish runs with
// --verify-dns, a transient DNS failure during the bind makes sish silently
// fall back to a dead name (it appends the root domain to the requested
// FQDN). autossh keeps the connection open and never notices — the only way
// to detect this is to probe the public URL from the outside and force a
// reconnect (fresh bind, fresh _sish TXT lookup) when the edge reports the
// host as unbound.
package tunnel

import (
	"net/http"
	"time"
)

const (
	// routingCheckInterval is how often settled tunnels are probed end-to-end.
	routingCheckInterval = 60 * time.Second

	// routingFailThreshold is the number of consecutive "unrouted" probes
	// before the tunnel process is force-reconnected. Combined with the
	// interval this tolerates ~2 minutes of flaky probing before acting.
	routingFailThreshold = 3

	// routingProbeTimeout bounds a single probe; the monitor loop must not
	// hang on a slow edge.
	routingProbeTimeout = 5 * time.Second
)

// routingResult classifies one probe of a tunnel's public URL.
type routingResult int

const (
	// routingOK: any reply that can only come from a bound tunnel (the app
	// answered, or at least sish proxied to it).
	routingOK routingResult = iota

	// routingUnrouted: the sish edge itself answered "no binding for this
	// host" — the tunnel is connected but publicly dead.
	routingUnrouted

	// routingUnknown: network error, timeout, DNS failure — no evidence
	// either way. Never counted: an offline school must not trigger
	// reconnect churn; autossh owns plain connectivity problems.
	routingUnknown
)

// probePublicURL probes https://{host}/ and classifies the answer. Redirects
// are not followed — a 3xx from the app is proof of routing all by itself.
func probePublicURL(host string) routingResult {
	client := &http.Client{
		Timeout: routingProbeTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get("https://" + host + "/")
	if err != nil {
		return routingUnknown
	}
	resp.Body.Close()
	if isEdgeUnrouted(resp) {
		return routingUnrouted
	}
	return routingOK
}

// isEdgeUnrouted reports whether the response matches sish's signature for
// "no tunnel bound for this host": gin's AbortWithError(404) sends status
// 404 with Content-Length: 0 and no Content-Type header (the error text only
// goes to sish's log). Apps behind a healthy tunnel practically always send
// a body or at least a Content-Type on "/" — e.g. Go's stdlib 404 is
// "404 page not found" with text/plain. Verified empirically against sish
// v2.23.0.
func isEdgeUnrouted(resp *http.Response) bool {
	return resp.StatusCode == http.StatusNotFound &&
		resp.ContentLength == 0 &&
		resp.Header.Get("Content-Type") == ""
}
