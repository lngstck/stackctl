// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

package web

import (
	"fmt"
	"strings"

	"github.com/lngstck/stackctl/internal/paths"
	"github.com/lngstck/stackctl/internal/sysinfo"
)

// sysView is the display-ready system-usage block for the settings System tab.
// Bytes are pre-formatted and each meter carries a level (ok/warn/crit) that
// the template maps to a colour — a quick "is there room for another container".
type sysView struct {
	RAMOK      bool
	RAMUsed    string
	RAMTotal   string
	RAMFree    string
	RAMPercent int
	RAMLevel   string

	DiskOK      bool
	DiskPath    string
	DiskUsed    string
	DiskTotal   string
	DiskFree    string
	DiskPercent int
	DiskLevel   string

	// Short forms for the start screen: "139" of "240 GB", "5,2" of "16 GB".
	RAMUsedShort, RAMTotalShort, RAMFreeShort    string
	DiskUsedShort, DiskTotalShort, DiskFreeShort string

	CPUCount int
	LoadOK   bool
	Load1    string

	// Available is false only when neither RAM nor disk could be read.
	Available bool
}

func buildSysView() sysView {
	info := sysinfo.Read(paths.LearningstackDir())
	v := sysView{
		RAMOK:       info.RAMOK,
		RAMPercent:  info.RAMPercent,
		RAMLevel:    usageLevel(info.RAMPercent),
		DiskOK:      info.DiskOK,
		DiskPath:    info.DiskPath,
		DiskPercent: info.DiskPercent,
		DiskLevel:   usageLevel(info.DiskPercent),
		CPUCount:    info.CPUCount,
		LoadOK:      info.LoadOK,
		Available:   info.RAMOK || info.DiskOK,
	}
	if info.RAMOK {
		v.RAMUsed = humanBytes(info.RAMUsed)
		v.RAMTotal = humanBytes(info.RAMTotal)
		v.RAMFree = humanBytes(info.RAMAvailable)
		v.RAMUsedShort = gigabytes(info.RAMUsed)
		v.RAMTotalShort = gigabytes(info.RAMTotal) + " GB"
		v.RAMFreeShort = gigabytes(info.RAMAvailable) + " GB"
	}
	if info.DiskOK {
		v.DiskUsed = humanBytes(info.DiskUsed)
		v.DiskTotal = humanBytes(info.DiskTotal)
		v.DiskFree = humanBytes(info.DiskFree)
		v.DiskUsedShort = gigabytes(info.DiskUsed)
		v.DiskTotalShort = gigabytes(info.DiskTotal) + " GB"
		v.DiskFreeShort = gigabytes(info.DiskFree) + " GB"
	}
	if info.LoadOK {
		v.Load1 = fmt.Sprintf("%.2f", info.Load1)
	}
	return v
}

// usageLevel maps a percentage to a traffic-light level for colouring.
func usageLevel(pct int) string {
	switch {
	case pct >= 90:
		return "crit"
	case pct >= 75:
		return "warn"
	default:
		return "ok"
	}
}

// humanBytes formats a byte count as a short human-readable string (GiB-based).
func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for n/div >= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// gigabytes formats a byte count in decimal gigabytes, the unit disks are
// sold in, German style: one decimal below ten ("5,2"), whole numbers above.
func gigabytes(n uint64) string {
	gb := float64(n) / 1e9
	if gb < 10 {
		return strings.Replace(fmt.Sprintf("%.1f", gb), ".", ",", 1)
	}
	return fmt.Sprintf("%.0f", gb)
}
