package main

import (
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func readServerStatus(root *os.File) serverStatus {
	var status serverStatus
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

func readServerServices(_ *float64) *serviceStatus { return nil }
