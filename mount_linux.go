package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// Device numbers alone cannot distinguish a bind-mounted directory.
func differentDirectoryMount(parent, child *os.File) bool {
	var first, second unix.Statx_t
	return unix.Statx(int(parent.Fd()), "", unix.AT_EMPTY_PATH, unix.STATX_MNT_ID, &first) == nil &&
		unix.Statx(int(child.Fd()), "", unix.AT_EMPTY_PATH, unix.STATX_MNT_ID, &second) == nil &&
		first.Mask&unix.STATX_MNT_ID != 0 && second.Mask&unix.STATX_MNT_ID != 0 && first.Mnt_id != second.Mnt_id
}
