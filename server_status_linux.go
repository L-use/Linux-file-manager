package main

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func readServerStatus(root *os.File) serverStatus {
	var status serverStatus
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		var total, available uint64
		var availableFound bool
		for _, line := range strings.Split(string(data), "\n") {
			values := strings.Fields(line)
			if len(values) < 2 {
				continue
			}
			amount, err := strconv.ParseUint(values[1], 10, 54)
			if err != nil {
				continue
			}
			switch values[0] {
			case "MemTotal:":
				total = amount * 1024
			case "MemAvailable:":
				available, availableFound = amount*1024, true
			}
		}
		if total > 0 && availableFound && available <= total {
			status.Memory = &statusUsage{total - available, total}
		}
	}
	var disk unix.Statfs_t
	if err := unix.Fstatfs(int(root.Fd()), &disk); err == nil && disk.Blocks >= disk.Bfree && disk.Bsize > 0 {
		used := (disk.Blocks - disk.Bfree) * uint64(disk.Bsize)
		status.Disk = &statusUsage{used, disk.Blocks * uint64(disk.Bsize)}
	}
	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		values := strings.Fields(string(data))
		if len(values) > 0 {
			uptime, err := strconv.ParseFloat(values[0], 64)
			if err == nil && uptime >= 0 {
				status.Uptime = &uptime
			}
		}
	}
	return status
}

func serviceUnit(pidDir string) (string, string) {
	data, err := os.ReadFile(filepath.Join(pidDir, "cgroup"))
	if err != nil {
		return "", ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		_, slice, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		controller, location, ok := strings.Cut(slice, ":")
		if !ok || (controller != "" && controller != "name=systemd") {
			continue
		}
		for len(location) > 0 {
			i := strings.LastIndexByte(location, '/')
			if i < 0 {
				break
			}
			name := location[i+1:]
			if strings.HasSuffix(name, ".service") {
				return location, name
			}
			location = location[:i]
		}
	}
	return "", ""
}

func processStatus(pidDir string, uptime float64) (serverService, bool) {
	data, err := os.ReadFile(filepath.Join(pidDir, "stat"))
	if err != nil {
		return serverService{}, false
	}
	raw := string(data)
	open, close := strings.IndexByte(raw, '('), strings.LastIndexByte(raw, ')')
	if open < 0 || close <= open {
		return serverService{}, false
	}
	fields := strings.Fields(raw[close+1:])
	if len(fields) < 22 || fields[0] == "Z" || fields[0] == "X" {
		return serverService{}, false
	}
	start, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return serverService{}, false
	}
	rss, err := strconv.ParseInt(fields[21], 10, 64)
	if err != nil {
		return serverService{}, false
	}
	// Linux exposes /proc starttime in USER_HZ ticks on amd64 and arm64.
	return serverService{
		Name: raw[open+1 : close], Processes: 1,
		Memory: uint64(max(0, rss)) * uint64(os.Getpagesize()),
		Uptime: max(0, uptime-float64(start)/100),
	}, true
}

func readServerServices(uptime *float64) *serviceStatus {
	if uptime == nil {
		return nil
	}
	dirs, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	units := make(map[string]serverService)
	var processes []serverService
	for _, entry := range dirs {
		pid := entry.Name()
		if !entry.IsDir() {
			continue
		}
		if _, err := strconv.ParseUint(pid, 10, 32); err != nil {
			continue
		}
		dir := filepath.Join("/proc", pid)
		process, ok := processStatus(dir, *uptime)
		if !ok {
			continue
		}
		id, name := serviceUnit(dir)
		if id == "" {
			process.Name += " (" + pid + ")"
			processes = append(processes, process)
			continue
		}
		unit := units[id]
		unit.Name = name
		unit.Memory += process.Memory
		unit.Processes++
		unit.Uptime = max(unit.Uptime, process.Uptime)
		units[id] = unit
	}
	kind := "services"
	items := make([]serverService, 0, len(units))
	for _, unit := range units {
		items = append(items, unit)
	}
	if len(items) == 0 && len(processes) > 0 {
		kind = "processes"
		items = processes
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Memory != items[j].Memory {
			return items[i].Memory > items[j].Memory
		}
		return items[i].Name < items[j].Name
	})
	return &serviceStatus{Kind: kind, Items: items[:min(50, len(items))], Total: len(items)}
}
