// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUsageLevel(t *testing.T) {
	cases := map[int]string{0: "ok", 74: "ok", 75: "warn", 89: "warn", 90: "crit", 100: "crit"}
	for pct, want := range cases {
		if got := usageLevel(pct); got != want {
			t.Errorf("usageLevel(%d) = %q, want %q", pct, got, want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[uint64]string{
		512:                   "512 B",
		2048:                  "2.0 KiB",
		3 * 1024 * 1024:       "3.0 MiB",
		8 * 1024 * 1024 * 1024: "8.0 GiB",
	}
	for n, want := range cases {
		if got := humanBytes(n); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

// TestRenderSettingsSystemUsage renders the settings page with a populated
// sysView, exercising template execution incl. the `width:{{.Sys.RAMPercent}}%`
// CSS interpolation (html/template must accept the int in a style attribute).
func TestRenderSettingsSystemUsage(t *testing.T) {
	s := &Server{}
	if err := s.loadTemplates(); err != nil {
		t.Fatalf("loadTemplates: %v", err)
	}
	data := settingsData{
		PageData:        PageData{NavActive: "settings", CSRFToken: "tok"},
		SystemTabActive: true,
		Sys: sysView{
			Available:   true,
			RAMOK:       true,
			RAMUsed:     "3.2 GiB",
			RAMTotal:    "8.0 GiB",
			RAMFree:     "4.8 GiB",
			RAMPercent:  40,
			RAMLevel:    "ok",
			DiskOK:      true,
			DiskPath:    "/opt/learningstack",
			DiskUsed:    "92.0 GiB",
			DiskTotal:   "100.0 GiB",
			DiskFree:    "8.0 GiB",
			DiskPercent: 92,
			DiskLevel:   "crit",
			CPUCount:    4,
			LoadOK:      true,
			Load1:       "0.42",
		},
	}
	rec := httptest.NewRecorder()
	s.render(rec, "settings.html.tmpl", data)
	if rec.Code != 200 {
		t.Fatalf("render status = %d; body:\n%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"System-Auslastung",
		"Arbeitsspeicher",
		"3.2 GiB / 8.0 GiB · 40%",
		"width:40%",
		"Speicherplatz",
		"width:92%",
		"crit",
		"4 CPU-Kerne",
		"0.42",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("settings page missing %q", want)
		}
	}
}
