// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import "net/http"

// viaHTTPS reports whether the browser reached stackctl over https. stackctl
// itself only speaks http; https ends at Caddy, which says so in
// X-Forwarded-Proto. Someone on the LAN can send that header to port 8090
// by hand, but all it gets them is a Secure cookie their own browser then
// refuses to send back.
func viaHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// securityHeaders sets what every response of the control plane carries.
//
//   - No framing: a page that embeds stackctl invisibly could make the admin
//     click "Entfernen" without seeing it (clickjacking).
//   - nosniff: uploaded backups and downloads stay what they claim to be.
//   - Referrer only within stackctl: links to an app or its docs do not
//     carry the admin address along.
//   - HSTS over https only: the browser then goes straight to https next
//     time, and the session cookie never travels over plain http. The admin
//     address is always served by Caddy with a certificate, so there is no
//     way back to http that this could block; 192.168.x.x:8090 is a
//     different host and unaffected.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "frame-ancestors 'none'; base-uri 'self'; object-src 'none'")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		if viaHTTPS(r) {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}
