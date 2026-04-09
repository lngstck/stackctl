package config

import (
	"errors"
	"fmt"
	"strings"
)

// SlugMinLen and SlugMaxLen bound the admissible length of a school slug.
// The slug ends up in Subdomains, DB names, and Dex client IDs, so the
// rules here are intentionally strict (ARCHITECTURE.md §18 Punkt 9).
const (
	SlugMinLen = 3
	SlugMaxLen = 30
)

// ValidateSlug enforces the SCHOOL_SLUG rules:
//   - length 3..30
//   - must start with a lowercase letter
//   - allowed chars: [a-z0-9-]
//   - no leading or trailing hyphen
//   - no consecutive hyphens
func ValidateSlug(slug string) error {
	if slug == "" {
		return errors.New("slug must not be empty")
	}
	if n := len(slug); n < SlugMinLen || n > SlugMaxLen {
		return fmt.Errorf("slug length %d outside %d..%d", n, SlugMinLen, SlugMaxLen)
	}
	first := slug[0]
	if first < 'a' || first > 'z' {
		return errors.New("slug must start with a lowercase letter a-z")
	}
	for i := 0; i < len(slug); i++ {
		c := slug[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '-':
		default:
			return fmt.Errorf("slug contains invalid character %q", c)
		}
	}
	if strings.HasSuffix(slug, "-") {
		return errors.New("slug must not end with a hyphen")
	}
	if strings.Contains(slug, "--") {
		return errors.New("slug must not contain consecutive hyphens")
	}
	return nil
}
