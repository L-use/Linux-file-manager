package main

import (
	"errors"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

func renameNoReplace(src int, old string, dst int, name string) error {
	return unix.RenameatxNp(src, old, dst, name, unix.RENAME_EXCL)
}

func openRegularAt(dir *os.File, name string, _ fs.FileInfo) (*os.File, error) {
	return openAt(dir, name, unix.O_RDONLY, 0)
}

func chmodAt(dir *os.File, name, expected string, permissions uint32) error {
	handle, err := openAt(dir, name, unix.O_EVTONLY, 0)
	if errors.Is(err, fs.ErrPermission) {
		// Run the no-follow operation relative to the pinned parent when the
		// entry has no read permissions and cannot be opened by macOS.
		info, err := descriptorLstat(dir, name)
		if err != nil {
			return err
		}
		mode, err := chmodMode(info, expected, permissions)
		if err != nil {
			return err
		}
		return unix.Fchmodat(int(dir.Fd()), name, mode, unix.AT_SYMLINK_NOFOLLOW)
	}
	if err != nil {
		return err
	}
	defer handle.Close()
	info, err := descriptorStat(handle)
	if err != nil {
		return err
	}
	mode, err := chmodMode(info, expected, permissions)
	if err != nil {
		return err
	}
	return unix.Fchmod(int(handle.Fd()), mode)
}
