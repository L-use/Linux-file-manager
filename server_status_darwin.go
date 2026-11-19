package main

import (
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

func readServerStatus(root *os.File) serverStatus {
	var status serverStatus
	status.Memory = macMemory()
	var disk unix.Statfs_t
	if err := unix.Fstatfs(int(root.Fd()), &disk); err == nil && disk.Blocks >= disk.Bfree && disk.Bsize > 0 {
		used := (disk.Blocks - disk.Bfree) * uint64(disk.Bsize)
		status.Disk = &statusUsage{used, disk.Blocks * uint64(disk.Bsize)}
	}
	if boot, err := unix.SysctlTimeval("kern.boottime"); err == nil {
		uptime := time.Since(time.Unix(boot.Sec, int64(boot.Usec)*1000)).Seconds()
		status.Uptime = &uptime
	}
	return status
}

func readServerServices(_ *float64) *serviceStatus {
	data, err := exec.Command("/bin/ps", "-axo", "rss=,etime=,comm=").Output()
	if err != nil {
		return nil
	}
	result := &serviceStatus{Kind: "processes"}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		rss, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			continue
		}
		elapsed := f[1]
		days := 0.0
		if a, b, ok := strings.Cut(elapsed, "-"); ok {
			days, _ = strconv.ParseFloat(a, 64)
			elapsed = b
		}
		seconds := days * 86400
		parts := strings.Split(elapsed, ":")
		value := 0.0
		for _, part := range parts {
			n, _ := strconv.ParseFloat(part, 64)
			value = value*60 + n
		}
		seconds += value
		result.Items = append(result.Items, serverService{Name: strings.Join(f[2:], " "), Memory: rss * 1024, Uptime: seconds, Processes: 1})
	}
	result.Total = len(result.Items)
	sort.Slice(result.Items, func(i, j int) bool { return result.Items[i].Memory > result.Items[j].Memory })
	if len(result.Items) > 12 {
		result.Items = result.Items[:12]
	}
	return result
}

var macMemoryCache struct {
	sync.Mutex
	at    time.Time
	usage *statusUsage
}

func macMemory() *statusUsage {
	macMemoryCache.Lock()
	defer macMemoryCache.Unlock()
	if time.Since(macMemoryCache.at) < time.Second {
		return macMemoryCache.usage
	}
	macMemoryCache.at = time.Now()
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return nil
	}
	data, err := exec.Command("/usr/bin/vm_stat").Output()
	if err != nil {
		return nil
	}
	pageSize := uint64(os.Getpagesize())
	header := strings.Fields(strings.SplitN(string(data), "\n", 2)[0])
	for i, word := range header {
		if word == "of" && i+1 < len(header) {
			if parsed, err := strconv.ParseUint(header[i+1], 10, 64); err == nil {
				pageSize = parsed
			}
		}
	}
	var available uint64
	for _, line := range strings.Split(string(data), "\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if name == "Pages free" || name == "Pages inactive" || name == "Pages speculative" {
			pages, _ := strconv.ParseUint(strings.Trim(strings.TrimSpace(value), "."), 10, 64)
			available += pages * pageSize
		}
	}
	if available > total {
		available = total
	}
	macMemoryCache.usage = &statusUsage{total - available, total}
	return macMemoryCache.usage
}
