// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

// Package desec talks to the deSEC DNS API (desec.io).
//
// In the school-network mode the school hands its domain to deSEC with two NS
// records at its own provider. From then on deSEC answers for it, Caddy
// writes the ACME challenge there with the school's token, and stackctl keeps
// the one record everything else depends on: *.{base_domain} pointing at this
// server. Nothing else in the zone is touched.
package desec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL is deSEC's API root.
const DefaultBaseURL = "https://desec.io/api/v1"

// Nameservers are deSEC's nameservers — what the school's provider has to
// list as NS records for the domain it hands over.
var Nameservers = []string{"ns1.desec.io", "ns2.desec.org"}

// ErrTokenRejected means deSEC does not accept the token.
var ErrTokenRejected = errors.New("deSEC lehnt den Token ab")

// ErrNoZone means none of the token's domains covers the name.
var ErrNoZone = errors.New("keine passende Domain bei deSEC")

// Client calls the API with one token.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// New returns a client for the real API.
func New(token string) *Client {
	return &Client{
		BaseURL: DefaultBaseURL,
		Token:   token,
		HTTP:    &http.Client{Timeout: 15 * time.Second},
	}
}

// Zones lists the domains the token can see.
func (c *Client) Zones(ctx context.Context) ([]string, error) {
	var domains []struct {
		Name string `json:"name"`
	}
	if err := c.do(ctx, http.MethodGet, "/domains/", nil, &domains); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(domains))
	for _, d := range domains {
		names = append(names, d.Name)
	}
	return names, nil
}

// ZoneFor returns the zone among zones that holds name: the name itself or
// its closest parent. A school may hand over sl.schule.de alone or all of
// schule.de; either way the record ends up in the right zone.
func ZoneFor(zones []string, name string) (string, bool) {
	best := ""
	for _, z := range zones {
		if (name == z || strings.HasSuffix(name, "."+z)) && len(z) > len(best) {
			best = z
		}
	}
	return best, best != ""
}

// EnsureWildcard makes *.{base} point at ip and nothing else. It reads first
// and writes only on a difference: every write at deSEC re-signs the zone and
// counts against a rate limit, and stackctl calls this on every start.
// changed reports whether a write happened.
func (c *Client) EnsureWildcard(ctx context.Context, base string, ip net.IP) (changed bool, err error) {
	rrType := "A"
	if ip.To4() == nil {
		rrType = "AAAA"
	}
	want := ip.String()

	zones, err := c.Zones(ctx)
	if err != nil {
		return false, err
	}
	zone, ok := ZoneFor(zones, base)
	if !ok {
		return false, fmt.Errorf("%w für %s", ErrNoZone, base)
	}
	subname := "*"
	if base != zone {
		subname = "*." + strings.TrimSuffix(base, "."+zone)
	}

	current, err := c.records(ctx, zone, subname, rrType)
	if err != nil {
		return false, err
	}
	if len(current) == 1 && net.ParseIP(current[0]).Equal(ip) {
		return false, nil
	}

	ttl, err := c.minimumTTL(ctx, zone)
	if err != nil {
		return false, err
	}
	body := []map[string]any{{
		"subname": subname,
		"type":    rrType,
		"ttl":     ttl,
		"records": []string{want},
	}}
	if err := c.do(ctx, http.MethodPut, "/domains/"+url.PathEscape(zone)+"/rrsets/", body, nil); err != nil {
		return false, err
	}
	return true, nil
}

// records returns the values of one RRset, or nil if it does not exist.
func (c *Client) records(ctx context.Context, zone, subname, rrType string) ([]string, error) {
	var rrsets []struct {
		Subname string   `json:"subname"`
		Type    string   `json:"type"`
		Records []string `json:"records"`
	}
	q := url.Values{"subname": {subname}}
	if err := c.do(ctx, http.MethodGet, "/domains/"+url.PathEscape(zone)+"/rrsets/?"+q.Encode(), nil, &rrsets); err != nil {
		return nil, err
	}
	for _, rr := range rrsets {
		if rr.Subname == subname && rr.Type == rrType {
			return rr.Records, nil
		}
	}
	return nil, nil
}

// minimumTTL is the smallest TTL deSEC accepts in the zone. Asking beats
// guessing: a lower value is rejected outright.
func (c *Client) minimumTTL(ctx context.Context, zone string) (int, error) {
	var d struct {
		MinimumTTL int `json:"minimum_ttl"`
	}
	if err := c.do(ctx, http.MethodGet, "/domains/"+url.PathEscape(zone)+"/", nil, &d); err != nil {
		return 0, err
	}
	if d.MinimumTTL <= 0 {
		return 3600, nil
	}
	return d.MinimumTTL, nil
}

// do sends one request and decodes the answer into out, if given.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Token "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrTokenRejected
	case resp.StatusCode == http.StatusForbidden:
		// The token is valid but may not write here — a token restricted
		// by a policy, for instance.
		return fmt.Errorf("deSEC verweigert den Zugriff (HTTP 403): Darf der Token in diese Domain schreiben?")
	case resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("deSEC bremst gerade (zu viele Anfragen), bitte später erneut versuchen")
	case resp.StatusCode >= 300:
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("deSEC: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out); err != nil {
		return fmt.Errorf("deSEC: Antwort unlesbar: %w", err)
	}
	return nil
}
