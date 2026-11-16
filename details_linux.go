package main

import (
	"time"

	"golang.org/x/sys/unix"
)

func detailBirthTime(dir int, name string, original unix.Stat_t) *int64 {
	var stat unix.Statx_t
	const fields = unix.STATX_BTIME | unix.STATX_INO | unix.STATX_CTIME
	if err := unix.Statx(dir, name, unix.AT_SYMLINK_NOFOLLOW|unix.AT_NO_AUTOMOUNT, fields, &stat); err != nil ||
		stat.Mask&fields != fields || stat.Ino != original.Ino ||
		unix.Mkdev(stat.Dev_major, stat.Dev_minor) != uint64(original.Dev) ||
		stat.Ctime.Sec != int64(original.Ctim.Sec) || stat.Ctime.Nsec != uint32(original.Ctim.Nsec) {
		return nil
	}
	value := time.Unix(stat.Btime.Sec, int64(stat.Btime.Nsec)).UnixMilli()
	return &value
}
