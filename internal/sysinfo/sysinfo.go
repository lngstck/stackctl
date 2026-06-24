// Copyright (C) 2026 learningstack contributors. Licensed under AGPL-3.0-or-later. See LICENSE.

// Package sysinfo reads coarse host resource usage (RAM, disk, CPU load) so the
// admin UI can show whether the machine still has headroom for another
// container. It is intentionally minimal and dependency-free: RAM and load come
// from /proc (Linux), disk from statfs. On a platform without /proc (e.g. a
// macOS dev box) the RAM/load fields are simply marked unavailable.
package sysinfo

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// Info is a snapshot of host resource usage. The *OK flags say whether a value
// could be read on this platform; callers should hide unavailable sections.
type Info struct {
	RAMOK        bool
	RAMTotal     uint64 // bytes
	RAMUsed      uint64 // bytes (total - available)
	RAMAvailable uint64 // bytes
	RAMPercent   int    // 0..100, used/total

	DiskOK      bool
	DiskPath    string
	DiskTotal   uint64 // bytes
	DiskUsed    uint64 // bytes
	DiskFree    uint64 // bytes available to non-root
	DiskPercent int    // 0..100, used/total

	CPUCount int

	LoadOK bool
	Load1  float64 // 1-minute load average
}

// Read gathers a fresh snapshot. diskPath selects the filesystem whose usage is
// reported (the container data root, e.g. /opt/learningstack).
func Read(diskPath string) Info {
	info := Info{CPUCount: runtime.NumCPU(), DiskPath: diskPath}
	readMem(&info)
	readDisk(&info, diskPath)
	readLoad(&info)
	return info
}

func readMem(info *Info) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return
	}
	defer f.Close()

	var total, avail uint64
	var haveTotal, haveAvail bool
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		v, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			total, haveTotal = v*1024, true // value is in kB
		case "MemAvailable:":
			avail, haveAvail = v*1024, true
		}
	}
	if !haveTotal || !haveAvail || total == 0 {
		return
	}
	info.RAMOK = true
	info.RAMTotal = total
	info.RAMAvailable = avail
	if avail <= total {
		info.RAMUsed = total - avail
	}
	info.RAMPercent = percent(info.RAMUsed, total)
}

func readDisk(info *Info, path string) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return
	}
	// Bsize is int64 on Linux and uint32 on Darwin; uint64() covers both.
	bs := uint64(st.Bsize)
	total := st.Blocks * bs
	if bs == 0 || total == 0 {
		return
	}
	info.DiskOK = true
	info.DiskTotal = total
	info.DiskFree = st.Bavail * bs            // usable by non-root
	info.DiskUsed = total - st.Bfree*bs       // includes root-reserved blocks
	info.DiskPercent = percent(info.DiskUsed, total)
}

func readLoad(info *Info) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return
	}
	if v, err := strconv.ParseFloat(fields[0], 64); err == nil {
		info.Load1 = v
		info.LoadOK = true
	}
}

// percent returns used/total as an integer 0..100 (clamped).
func percent(used, total uint64) int {
	if total == 0 {
		return 0
	}
	p := int(used * 100 / total)
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}
