// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

// Package public builds the public hostnames and URLs of an install.
//
// Until ConfigVersion 3 the address "{sub}.{slug}.learningstack.online" was
// assembled inline in a dozen places, which quietly made the operator's root
// domain a constant of the codebase: a school could not be reached under its
// own domain, and a server that sits directly on the internet had no way to
// say so. Everything that needs a public address now goes through here, so
// config.public — transport plus base_domain — is the only place the answer
// lives.
//
// Nothing in this package knows how traffic actually arrives. A relay tunnel
// and a local reverse proxy produce the same hostnames; only the machinery
// behind them differs.
package public

import "github.com/lngstck/stackctl/internal/config"

// AuthSubdomain is the label the local Dex answers on. It is fixed: the
// OIDC issuer has to match between browser and container, and the central
// Dex holds a redirect URI derived from it.
const AuthSubdomain = "auth"

// AdminSubdomain is the label stackctl's own web UI answers on when the admin
// publishes it. Unlike the login it is off by default — see the handlers in
// internal/web for why that is a decision and not a default.
//
// It is a reserved label rather than a configurable one: an app whose id is
// "admin" would otherwise take the address of the control plane, and the
// collision would only show up once both are published.
const AdminSubdomain = "admin"

// BaseDomain returns the parent domain of every public hostname.
//
// The fallback covers a config whose address has not been written yet — a
// half-finished setup, or a v2 file that has been upgraded in memory but not
// saved. Every v2 install was an operator-relay install, so deriving the
// address from the slug reproduces exactly what that build would have built.
func BaseDomain(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	if cfg.Public.BaseDomain != "" {
		return cfg.Public.BaseDomain
	}
	if cfg.School.Slug != "" {
		return config.RelayBaseDomain(cfg.School.Slug)
	}
	return ""
}

// Host returns the fully qualified hostname for a subdomain label, or "" if
// the install has no address yet.
//
// Callers must always use this FQDN and never a bare label. sish stores a
// forwarding registration under the literally requested name while incoming
// requests carry the FQDN in their Host header, so a short name registers
// happily and then 404s with "cannot find connection for host: …". See
// ARCHITECTURE.md §16.4.
func Host(cfg *config.Config, sub string) string {
	base := BaseDomain(cfg)
	if base == "" {
		return ""
	}
	if sub == "" {
		return base
	}
	return sub + "." + base
}

// URL returns the https:// URL for a subdomain label, or "" if the install
// has no address yet. Public traffic is always TLS — at the relay edge or at
// the local proxy, depending on transport.
func URL(cfg *config.Config, sub string) string {
	host := Host(cfg, sub)
	if host == "" {
		return ""
	}
	return "https://" + host
}

// AuthHost returns the hostname of the local Dex.
func AuthHost(cfg *config.Config) string { return Host(cfg, AuthSubdomain) }

// AuthURL returns the OIDC issuer URL of the local Dex. This is always the
// public URL, never a local one: the issuer must match between the browser
// and the container that mints the tokens.
func AuthURL(cfg *config.Config) string { return URL(cfg, AuthSubdomain) }

// AdminHost returns the hostname stackctl's own UI answers on once published.
func AdminHost(cfg *config.Config) string { return Host(cfg, AdminSubdomain) }

// AdminURL returns the public URL of stackctl's own UI once published.
func AdminURL(cfg *config.Config) string { return URL(cfg, AdminSubdomain) }

// AppHost returns the public hostname of an installed app.
func AppHost(cfg *config.Config, appID string) string { return Host(cfg, appID) }

// AppURL returns the public URL of an installed app.
func AppURL(cfg *config.Config, appID string) string { return URL(cfg, appID) }
