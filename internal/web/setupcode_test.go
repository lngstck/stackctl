// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/lngstck/stackctl/internal/config"
	"github.com/lngstck/stackctl/internal/paths"
)

// testSetupCode is the code test servers are set up with.
const testSetupCode = "ABCD-EFGH"

func TestNewSetupCodeShape(t *testing.T) {
	re := regexp.MustCompile(`^[` + setupCodeAlphabet + `]{4}-[` + setupCodeAlphabet + `]{4}$`)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		code, err := newSetupCode()
		if err != nil {
			t.Fatal(err)
		}
		if !re.MatchString(code) {
			t.Fatalf("code %q does not look like XXXX-XXXX from the alphabet", code)
		}
		seen[code] = true
	}
	if len(seen) < 50 {
		t.Errorf("only %d distinct codes out of 50", len(seen))
	}
}

// The code is created once and survives a restart, so the link install.sh
// printed keeps working.
func TestEnsureSetupCodeIsStable(t *testing.T) {
	t.Setenv(paths.EnvStackctlDir, t.TempDir())

	first, err := ensureSetupCode()
	if err != nil {
		t.Fatal(err)
	}
	second, err := ensureSetupCode()
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || first != second {
		t.Errorf("codes %q and %q, want the same non-empty code", first, second)
	}
	info, err := os.Stat(paths.SetupCodeFile())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o007 != 0 {
		t.Errorf("setup code file mode %v is readable by others", info.Mode().Perm())
	}
}

// Typed codes match regardless of case, dashes and pasted blanks; anything
// else does not, and an empty stored code matches nothing.
func TestValidSetupCode(t *testing.T) {
	s := &Server{setupCode: testSetupCode}
	for _, ok := range []string{"ABCD-EFGH", "abcd-efgh", "ABCDEFGH", " abcd efgh\n"} {
		if !s.validSetupCode(ok) {
			t.Errorf("%q should match", ok)
		}
	}
	for _, bad := range []string{"", "ABCD-EFGX", "ABCD-EFG", "ABCD-EFGHI"} {
		if s.validSetupCode(bad) {
			t.Errorf("%q should not match", bad)
		}
	}
	if (&Server{}).validSetupCode("") {
		t.Error("without a stored code nothing may match")
	}
}

// Without the code, setup does not happen — whatever else the form says.
func TestSetupRejectsWrongCode(t *testing.T) {
	for _, code := range []string{"", "ZZZZ-ZZZZ"} {
		t.Run("code="+code, func(t *testing.T) {
			s, _ := testServerWithPublisher(t, &fakePublisher{})
			s.cfg = config.Default()
			if err := s.loadTemplates(); err != nil {
				t.Fatalf("loadTemplates: %v", err)
			}
			form := localSetupForm()
			form.Set("setup_code", code)

			rec := postSetup(t, s, form)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Einrichtungscode stimmt nicht") {
				t.Errorf("status = %d, want the form with a code error", rec.Code)
			}
			if s.cfg.SetupState != config.SetupStateNeedsSetup {
				t.Errorf("SetupState = %q, want needs_setup", s.cfg.SetupState)
			}
			if _, err := os.Stat(paths.ConfigFile()); !errors.Is(err, os.ErrNotExist) {
				t.Error("config.yaml must not be written without the code")
			}
		})
	}
}

// After setup the code is gone, on disk and in memory.
func TestSetupRemovesCode(t *testing.T) {
	s, _ := testServerWithPublisher(t, &fakePublisher{})
	s.cfg = config.Default()
	if err := s.loadTemplates(); err != nil {
		t.Fatalf("loadTemplates: %v", err)
	}
	if err := os.MkdirAll(paths.ConfigDir(), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SetupCodeFile(), []byte(testSetupCode+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	if rec := postSetup(t, s, localSetupForm()); rec.Code != http.StatusSeeOther {
		t.Fatalf("setup failed: %d\n%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(paths.SetupCodeFile()); !errors.Is(err, os.ErrNotExist) {
		t.Error("setup code file should be deleted after setup")
	}
	if s.setupCode != "" {
		t.Error("setup code should be cleared in memory")
	}
}

// The prerequisite check is reachable without a login; the code keeps it
// from being an open probe — and from being a way to guess the code.
func TestSetupPreflightRequiresCode(t *testing.T) {
	s := &Server{
		cfg:       &config.Config{SetupState: config.SetupStateNeedsSetup},
		setupCode: testSetupCode,
		limiter:   newRateLimiter(),
	}
	for i := 0; i < maxLoginAttempts; i++ {
		rec := httptest.NewRecorder()
		s.handleSetupPreflight(rec, httptest.NewRequest("GET", "/setup/preflight?setup_code=WRONG&mode=direct&base_domain=x.de", nil))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("attempt %d: status = %d, want 403", i+1, rec.Code)
		}
	}
	// Locked out now: even the right code is refused for a while.
	rec := httptest.NewRecorder()
	s.handleSetupPreflight(rec, httptest.NewRequest("GET", "/setup/preflight?setup_code="+testSetupCode+"&mode=direct&base_domain=*.x.de", nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("status after lockout = %d, want 403", rec.Code)
	}
}

// The link from install.sh fills in the code.
func TestSetupPagePrefillsCode(t *testing.T) {
	s, _ := testServerWithPublisher(t, &fakePublisher{})
	s.cfg = config.Default()
	if err := s.loadTemplates(); err != nil {
		t.Fatalf("loadTemplates: %v", err)
	}
	rec := httptest.NewRecorder()
	s.handleSetup(rec, httptest.NewRequest("GET", "/setup?code=ABCD-EFGH", nil))
	if !strings.Contains(rec.Body.String(), `value="ABCD-EFGH"`) {
		t.Error("setup form should carry the code from the link")
	}
}
