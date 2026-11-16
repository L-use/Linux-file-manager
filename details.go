package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/user"
	"path"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

type itemDetails struct {
	entry
	Mode      string `json:"mode"`
	Symbolic  string `json:"symbolic"`
	Subtype   string `json:"subtype"`
	UID       uint32 `json:"uid"`
	GID       uint32 `json:"gid"`
	Owner     string `json:"owner,omitempty"`
	Group     string `json:"group,omitempty"`
	Inode     string `json:"inode"`
	Links     uint64 `json:"links"`
	Allocated int64  `json:"allocated"`
	Accessed  int64  `json:"accessed"`
	Changed   int64  `json:"changed"`
	Created   *int64 `json:"created,omitempty"`
}

func describeMode(info fs.FileInfo, stat unix.Stat_t) (string, string) {
	rwx := []byte(info.Mode().Perm().String())
	subtype := ""
	switch stat.Mode & unix.S_IFMT {
	case unix.S_IFDIR:
		rwx[0] = 'd'
	case unix.S_IFLNK:
		rwx[0] = 'l'
	case unix.S_IFIFO:
		rwx[0], subtype = 'p', "fifo"
	case unix.S_IFSOCK:
		rwx[0], subtype = 's', "socket"
	case unix.S_IFCHR:
		rwx[0], subtype = 'c', "charDevice"
	case unix.S_IFBLK:
		rwx[0], subtype = 'b', "blockDevice"
	}
	for _, flag := range []struct {
		mask   uint32
		index  int
		letter byte
	}{{unix.S_ISUID, 3, 's'}, {unix.S_ISGID, 6, 's'}, {unix.S_ISVTX, 9, 't'}} {
		if uint32(stat.Mode)&flag.mask != 0 {
			if rwx[flag.index] == 'x' {
				rwx[flag.index] = flag.letter
			} else {
				rwx[flag.index] = flag.letter - ('a' - 'A')
			}
		}
	}
	return string(rwx), subtype
}

func (s *fileStore) details(name string) (itemDetails, error) {
	dir, base, err := s.parent(name, false)
	if err != nil {
		return itemDetails{}, err
	}
	defer dir.Close()
	info, err := s.metadata(dir, base)
	if err != nil {
		return itemDetails{}, err
	}
	stat := info.Sys().(unix.Stat_t)
	symbolic, subtype := describeMode(info, stat)
	d := itemDetails{
		entry: toEntry(name, info),
		Mode:  fmt.Sprintf("%04o", stat.Mode&07777), Symbolic: symbolic, Subtype: subtype,
		UID: stat.Uid, GID: stat.Gid, Links: uint64(stat.Nlink),
		Inode: strconv.FormatUint(stat.Ino, 10), Allocated: int64(stat.Blocks) * 512,
		Accessed: time.Unix(int64(stat.Atim.Sec), int64(stat.Atim.Nsec)).UnixMilli(),
		Changed:  time.Unix(int64(stat.Ctim.Sec), int64(stat.Ctim.Nsec)).UnixMilli(),
		Created:  detailBirthTime(int(dir.Fd()), base, stat),
	}
	if name == "" {
		d.Name = s.absolute
	}
	if owner, err := user.LookupId(strconv.FormatUint(uint64(d.UID), 10)); err == nil {
		d.Owner = owner.Username
	}
	if group, err := user.LookupGroupId(strconv.FormatUint(uint64(d.GID), 10)); err == nil {
		d.Group = group.Name
	}
	return d, nil
}

type folderContents struct {
	Bytes   int64 `json:"bytes"`
	Files   int   `json:"files"`
	Folders int   `json:"folders"`
	Partial bool  `json:"partial"`
}

func (s *fileStore) folderContents(ctx context.Context, name string) (folderContents, error) {
	var result folderContents
	const maxEntries = 50000
	cutoff := time.Now().Add(3 * time.Second)
	queue := []string{name}
	type identity struct{ device, inode uint64 }
	seen := make(map[identity]bool)
	scanned := 0
	for i := 0; i < len(queue); i++ {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if time.Now().After(cutoff) {
			result.Partial = true
			break
		}
		dir, err := s.dir(queue[i])
		if err != nil {
			if i == 0 {
				return result, err
			}
			result.Partial = true
			continue
		}
		info, err := descriptorStat(dir)
		if err != nil {
			dir.Close()
			if i == 0 {
				return result, err
			}
			result.Partial = true
			continue
		}
		stat := info.Sys().(unix.Stat_t)
		key := identity{uint64(stat.Dev), stat.Ino}
		if seen[key] {
			dir.Close()
			result.Partial = true
			continue
		}
		seen[key] = true
		for {
			if err := ctx.Err(); err != nil {
				dir.Close()
				return result, err
			}
			names, readErr := dir.Readdirnames(128)
			for _, child := range names {
				if scanned == maxEntries || time.Now().After(cutoff) {
					dir.Close()
					result.Partial = true
					return result, nil
				}
				scanned++
				full := path.Join(queue[i], child)
				if !validName(child) || s.checked(full, false) != nil {
					continue
				}
				info, err := s.metadata(dir, child)
				if err != nil {
					var blocked *problem
					if !errors.As(err, &blocked) || blocked.code != "protected" {
						result.Partial = true
					}
					continue
				}
				if info.IsDir() {
					queue = append(queue, full)
					result.Folders++
				} else if info.Mode().IsRegular() {
					result.Files++
					result.Bytes += info.Size()
				}
			}
			if readErr != nil {
				if !errors.Is(readErr, io.EOF) {
					if i == 0 {
						dir.Close()
						return result, readErr
					}
					result.Partial = true
				}
				break
			}
		}
		dir.Close()
	}
	return result, nil
}
