package main

import (
	"fmt"
	"io/fs"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

type descriptorInfo struct {
	name string
	stat unix.Stat_t
}

func (i descriptorInfo) Name() string { return i.name }
func (i descriptorInfo) Sys() any     { return i.stat }
func (i descriptorInfo) Size() int64  { return i.stat.Size }
func (i descriptorInfo) ModTime() time.Time {
	return time.Unix(int64(i.stat.Mtim.Sec), int64(i.stat.Mtim.Nsec))
}
func (i descriptorInfo) IsDir() bool { return i.Mode().IsDir() }
func (i descriptorInfo) Mode() fs.FileMode {
	mode := fs.FileMode(i.stat.Mode & 0777)
	switch i.stat.Mode & unix.S_IFMT {
	case unix.S_IFDIR:
		mode |= fs.ModeDir
	case unix.S_IFLNK:
		mode |= fs.ModeSymlink
	case unix.S_IFIFO:
		mode |= fs.ModeNamedPipe
	case unix.S_IFSOCK:
		mode |= fs.ModeSocket
	case unix.S_IFCHR:
		mode |= fs.ModeCharDevice | fs.ModeDevice
	case unix.S_IFBLK:
		mode |= fs.ModeDevice
	case unix.S_IFREG:
	default:
		mode |= fs.ModeIrregular
	}
	return mode
}
func descriptorLstat(dir *os.File, name string) (fs.FileInfo, error) {
	info := descriptorInfo{name: name}
	err := unix.Fstatat(int(dir.Fd()), name, &info.stat, unix.AT_SYMLINK_NOFOLLOW)
	return info, err
}
func descriptorStat(f *os.File) (fs.FileInfo, error) {
	info := descriptorInfo{name: f.Name()}
	err := unix.Fstat(int(f.Fd()), &info.stat)
	return info, err
}
func sameFile(first, second fs.FileInfo) bool {
	a := first.Sys().(unix.Stat_t)
	b := second.Sys().(unix.Stat_t)
	return a.Dev == b.Dev && a.Ino == b.Ino
}
func fileIdentity(info fs.FileInfo) string {
	s := info.Sys().(unix.Stat_t)
	return fmt.Sprintf("%x:%x:%x:%x", s.Dev, s.Ino, s.Ctim.Sec, s.Ctim.Nsec)
}
func statAbsolute(p string) (fs.FileInfo, error) {
	info := descriptorInfo{name: p}
	if err := unix.Stat(p, &info.stat); err != nil {
		return nil, err
	}
	return info, nil
}
