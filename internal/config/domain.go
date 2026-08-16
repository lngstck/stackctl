package config

import (
	"errors"
	"fmt"
	"strings"
)

// BaseDomainMaxLen bounds the base domain. Every public hostname is
// "{label}.{base_domain}", and a DNS name may not exceed 253 characters —
// leaving room for the longest app id keeps us clear of that limit.
const BaseDomainMaxLen = 200

// ValidateBaseDomain enforces the rules for public.base_domain.
//
// The admin types this by hand in the setup wizard, so the checks target the
// mistakes people actually make: pasting a full URL, pasting the wildcard
// record they just created at their DNS provider, or adding a trailing path
// or port. A wrong base domain is expensive — it lands in the Dex issuer, in
// every OIDC redirect URI, and in the registration package the operator acts
// on — so it is worth rejecting early and precisely.
func ValidateBaseDomain(domain string) error {
	if domain == "" {
		return errors.New("must not be empty")
	}
	if len(domain) > BaseDomainMaxLen {
		return fmt.Errorf("length %d exceeds %d", len(domain), BaseDomainMaxLen)
	}
	if strings.Contains(domain, "://") {
		return errors.New("must be a bare domain, without https://")
	}
	if strings.ContainsAny(domain, "/?#") {
		return errors.New("must be a bare domain, without a path")
	}
	if strings.Contains(domain, "*") {
		return errors.New("must be the domain itself, not a wildcard like *.example.org")
	}
	if strings.Contains(domain, ":") {
		return errors.New("must not carry a port")
	}
	if domain != strings.ToLower(domain) {
		return errors.New("must be lowercase")
	}
	if strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return errors.New("must not start or end with a dot")
	}

	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return errors.New("must contain at least one dot, e.g. example.org")
	}
	for _, label := range labels {
		if err := validateDomainLabel(label); err != nil {
			return err
		}
	}
	return nil
}

// validateDomainLabel checks one dot-separated part against the LDH rule
// (letters, digits, hyphen; no leading or trailing hyphen).
func validateDomainLabel(label string) error {
	if label == "" {
		return errors.New("must not contain an empty part (two dots in a row)")
	}
	if len(label) > 63 {
		return fmt.Errorf("part %q is longer than 63 characters", label)
	}
	if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
		return fmt.Errorf("part %q must not start or end with a hyphen", label)
	}
	for i := 0; i < len(label); i++ {
		c := label[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '-':
		default:
			return fmt.Errorf("part %q contains invalid character %q", label, c)
		}
	}
	return nil
}
