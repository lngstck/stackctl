package config

import "testing"

func TestValidateBaseDomain(t *testing.T) {
	valid := []string{
		"phoenix.learningstack.online",
		"gym-phoenix.de",
		"ls.gym-phoenix.de",
		"lernen.schule.stadt-wolfsburg.de",
		"a1.example.org",
	}
	for _, d := range valid {
		if err := ValidateBaseDomain(d); err != nil {
			t.Errorf("ValidateBaseDomain(%q) = %v, want nil", d, err)
		}
	}

	// The rejected cases are the ones an admin actually types: a pasted URL,
	// the wildcard record they just created, a host with a port, a trailing
	// path. Each has to fail before it reaches the Dex issuer.
	invalid := []struct {
		domain string
		reason string
	}{
		{"", "empty"},
		{"https://ls.gym-phoenix.de", "pasted URL"},
		{"*.gym-phoenix.de", "wildcard record"},
		{"ls.gym-phoenix.de/apps", "trailing path"},
		{"ls.gym-phoenix.de:8443", "port"},
		{"LS.Gym-Phoenix.de", "uppercase"},
		{"localhost", "single label"},
		{".gym-phoenix.de", "leading dot"},
		{"gym-phoenix.de.", "trailing dot"},
		{"ls..gym-phoenix.de", "empty label"},
		{"-ls.gym-phoenix.de", "label starts with hyphen"},
		{"ls-.gym-phoenix.de", "label ends with hyphen"},
		{"ls.gym_phoenix.de", "underscore"},
		{"ls.gym phoenix.de", "space"},
	}
	for _, tc := range invalid {
		if err := ValidateBaseDomain(tc.domain); err == nil {
			t.Errorf("ValidateBaseDomain(%q) = nil, want an error (%s)", tc.domain, tc.reason)
		}
	}
}
