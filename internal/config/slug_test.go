package config

import "testing"

func TestValidateSlugOK(t *testing.T) {
	valid := []string{
		"phoenix",
		"gym1",
		"a1b",
		"school-2026",
		"abc-def-ghi",
		"x12",
		"abcdefghij0123456789abcdefghij", // 30 chars
	}
	for _, s := range valid {
		if err := ValidateSlug(s); err != nil {
			t.Errorf("ValidateSlug(%q) unexpected error: %v", s, err)
		}
	}
}

func TestValidateSlugErrors(t *testing.T) {
	cases := map[string]string{
		"":                                "empty",
		"ab":                              "too short",
		"abcdefghij0123456789abcdefghij1": "too long",
		"1school":                         "starts with digit",
		"-school":                         "starts with hyphen",
		"School":                          "uppercase",
		"school_name":                     "underscore",
		"school.name":                     "dot",
		"school ":                         "space",
		"school-":                         "trailing hyphen",
		"foo--bar":                        "consecutive hyphens",
		"foo-":                            "trailing hyphen short",
	}
	for slug, label := range cases {
		if err := ValidateSlug(slug); err == nil {
			t.Errorf("ValidateSlug(%q) expected error (%s), got nil", slug, label)
		}
	}
}
