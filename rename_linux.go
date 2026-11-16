package main

import (
	"fmt"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

func renameNoReplace(src int, old string, dst int, name string) error {
	return unix.Renameat2(src, old, dst, name, unix.RENAME_NOREPLACE)
}

func openRegularAt(dir *os.File, name string, expected fs.FileInfo) (*os.File, error) {
	handle, err := openAt(dir, name, unix.O_PATH, 0)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	info, err := descriptorStat(handle)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || !sameFile(info, expected) {
		return nil, failure(409, "changed")
	}
	return os.Open(fmt.Sprintf("/proc/self/fd/%d", handle.Fd()))
}

// O_PATH pins the entry even when its permission bits currently deny reads.
func chmodAt(dir *os.File, name, expected string, permissions uint32) error {
	handle, err := openAt(dir, name, unix.O_PATH, 0)
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
	return unix.Chmod(fmt.Sprintf("/proc/self/fd/%d", handle.Fd()), mode)
}
