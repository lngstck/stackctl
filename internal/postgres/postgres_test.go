package postgres

import "testing"

func TestSanitizeIdent(t *testing.T) {
	cases := map[string]string{
		"langflow":   "langflow",
		"open-webui": "open_webui",
		"my.app":     "myapp",
		"MY-APP":     "my_app",  // lowercased + hyphen → underscore
		"---":        "___",  // hyphens → underscores
		"a1b":        "a1b",
	}
	for input, want := range cases {
		got := sanitizeIdent(input)
		if got != want {
			t.Errorf("sanitizeIdent(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSanitizeIdentEmpty(t *testing.T) {
	if got := sanitizeIdent("!!!"); got != "unknown" {
		t.Errorf("all-invalid input = %q, want unknown", got)
	}
}

func TestDBPasswordEnvKey(t *testing.T) {
	cases := map[string]string{
		"langflow":   "LANGFLOW_DB_PASSWORD",
		"open-webui": "OPEN_WEBUI_DB_PASSWORD",
		"postgres":   "POSTGRES_DB_PASSWORD",
	}
	for appID, want := range cases {
		got := DBPasswordEnvKey(appID)
		if got != want {
			t.Errorf("DBPasswordEnvKey(%q) = %q, want %q", appID, got, want)
		}
	}
}

func TestEscapeSingleQuote(t *testing.T) {
	if got := escapeSingleQuote("it's a test"); got != "it''s a test" {
		t.Errorf("escape = %q", got)
	}
	if got := escapeSingleQuote("no quotes"); got != "no quotes" {
		t.Errorf("escape = %q", got)
	}
}
