// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package sysinfo

import "testing"

func TestPercent(t *testing.T) {
	cases := []struct {
		used, total uint64
		want        int
	}{
		{0, 0, 0},
		{0, 100, 0},
		{50, 100, 50},
		{100, 100, 100},
		{200, 100, 100}, // clamped
	}
	for _, c := range cases {
		if got := percent(c.used, c.total); got != c.want {
			t.Errorf("percent(%d, %d) = %d, want %d", c.used, c.total, got, c.want)
		}
	}
}

// TestReadDoesNotPanicAndReportsCPU is platform-agnostic: RAM/load may be
// unavailable off Linux, but CPU count is always set and Read must never panic.
func TestReadDoesNotPanicAndReportsCPU(t *testing.T) {
	info := Read("/")
	if info.CPUCount < 1 {
		t.Errorf("CPUCount = %d, want >= 1", info.CPUCount)
	}
	if info.RAMOK && (info.RAMPercent < 0 || info.RAMPercent > 100) {
		t.Errorf("RAMPercent out of range: %d", info.RAMPercent)
	}
	if info.DiskOK && (info.DiskPercent < 0 || info.DiskPercent > 100) {
		t.Errorf("DiskPercent out of range: %d", info.DiskPercent)
	}
}
