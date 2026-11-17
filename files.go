package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const uploadPrefix = ".fm-upload-"

type deniedPath struct {
	absolute string
	info     fs.FileInfo
}

type fileStore struct {
	root     *os.File
	absolute string
	denied   []deniedPath
	mu       sync.Mutex
}

type entry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Kind        string `json:"kind"`
	Size        int64  `json:"size"`
	Modified    int64  `json:"modified"`
	Version     string `json:"version"`
	Permissions string `json:"permissions"`
}

func openFiles(root string, excludes []string) (*fileStore, error) {
	f, err := os.Open(root)
	if err != nil {
		return nil, err
	}
	s := &fileStore{root: f, absolute: root}
	for _, p := range excludes {
		info, _ := statAbsolute(p)
		s.denied = append(s.denied, deniedPath{p, info})
		if info != nil && info.IsDir() {
			credentials, _ := statAbsolute(filepath.Join(p, "credentials.json"))
			if credentials != nil {
				s.denied = append(s.denied, deniedPath{filepath.Join(p, "credentials.json"), credentials})
			}
		}
	}
	rootInfo, err := descriptorStat(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	if !s.visible(rootInfo) {
		f.Close()
		return nil, errors.New("management root is a protected directory")
	}
	return s, nil
}

func validName(name string) bool {
	return name != "" && name != "." && name != ".." && len(name) <= 255 && utf8.ValidString(name) && !strings.ContainsAny(name, "/\x00") && !strings.HasPrefix(name, uploadPrefix)
}

func (s *fileStore) checked(name string, modifying bool) error {
	if name != "" {
		for _, piece := range strings.Split(name, "/") {
			if !validName(piece) {
				return failure(400, "invalidPath")
			}
		}
	} else if modifying {
		return failure(403, "protected")
	}
	full := filepath.Join(s.absolute, name)
	for _, d := range s.denied {
		if inside(d.absolute, full) || (modifying && inside(full, d.absolute)) {
			return failure(403, "protected")
		}
	}
	return nil
}

func (s *fileStore) visible(info fs.FileInfo) bool {
	for _, d := range s.denied {
		if d.info != nil && sameFile(info, d.info) {
			return false
		}
	}
	return true
}

func openAt(directory *os.File, name string, flags int, mode uint32) (*os.File, error) {
	fd, err := unix.Openat(int(directory.Fd()), name, flags|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, mode)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

func (s *fileStore) dir(name string) (*os.File, error) {
	if err := s.checked(name, false); err != nil {
		return nil, err
	}
	current, err := openAt(s.root, ".", unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil || name == "" {
		return current, err
	}
	for _, part := range strings.Split(name, "/") {
		next, err := openAt(current, part, unix.O_RDONLY|unix.O_DIRECTORY, 0)
		current.Close()
		if err != nil {
			return nil, err
		}
		info, err := descriptorStat(next)
		if err != nil || !s.visible(info) {
			next.Close()
			if err != nil {
				return nil, err
			}
			return nil, failure(403, "protected")
		}
		current = next
	}
	return current, nil
}

func parentName(name string) string {
	dir := path.Dir(name)
	if dir == "." {
		return ""
	}
	return dir
}

func (s *fileStore) parent(name string, modifying bool) (*os.File, string, error) {
	if err := s.checked(name, modifying); err != nil {
		return nil, "", err
	}
	f, err := s.dir(parentName(name))
	return f, path.Base(name), err
}

func (s *fileStore) metadata(dir *os.File, name string) (fs.FileInfo, error) {
	// Inspect the final component relative to its open parent.
	info, err := descriptorLstat(dir, name)
	if err != nil {
		return nil, err
	}
	if !s.visible(info) {
		return nil, failure(403, "protected")
	}
	return info, nil
}

func (s *fileStore) list(name string) ([]entry, error) {
	dir, err := s.dir(name)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	names, err := dir.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	result := make([]entry, 0, len(names))
	for _, item := range names {
		full := path.Join(name, item)
		if !validName(item) || s.checked(full, false) != nil {
			continue
		}
		info, err := s.metadata(dir, item)
		if err != nil {
			continue
		}
		result = append(result, toEntry(full, info))
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := result[i], result[j]
		if (left.Kind == "directory") != (right.Kind == "directory") {
			return left.Kind == "directory"
		}
		return strings.ToLower(left.Name) < strings.ToLower(right.Name)
	})
	return result, nil
}

func toEntry(full string, info fs.FileInfo) entry {
	kind := "special"
	switch {
	case info.IsDir():
		kind = "directory"
	case info.Mode().IsRegular():
		kind = "file"
	case info.Mode()&os.ModeSymlink != 0:
		kind = "symlink"
	}
	return entry{
		Name: path.Base(full), Path: full, Kind: kind, Size: info.Size(),
		Modified: info.ModTime().UnixMilli(), Version: version(info),
		Permissions: fmt.Sprintf("%03o", info.Mode().Perm()),
	}
}

func (s *fileStore) stat(name string) (entry, error) {
	dir, base, err := s.parent(name, false)
	if err != nil {
		return entry{}, err
	}
	defer dir.Close()
	info, err := s.metadata(dir, base)
	if err != nil {
		return entry{}, err
	}
	return toEntry(name, info), nil
}

func (s *fileStore) regular(name string) (*os.File, fs.FileInfo, error) {
	dir, base, err := s.parent(name, false)
	if err != nil {
		return nil, nil, err
	}
	defer dir.Close()
	info, err := s.metadata(dir, base)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, failure(415, "special")
	}
	f, err := openRegularAt(dir, base, info)
	if err != nil {
		return nil, nil, err
	}
	opened, err := descriptorStat(f)
	if err != nil || !opened.Mode().IsRegular() || !sameFile(info, opened) || !s.visible(opened) {
		f.Close()
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, failure(409, "changed")
	}
	return f, opened, nil
}

func (s *fileStore) chmod(name, expected, permissions string) error {
	if len(permissions) == 4 {
		permissions = strings.TrimPrefix(permissions, "0")
	}
	if len(permissions) != 3 {
		return failure(400, "invalidPermissions")
	}
	mode, err := strconv.ParseUint(permissions, 8, 9)
	if err != nil {
		return failure(400, "invalidPermissions")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, base, err := s.parent(name, true)
	if err != nil {
		return err
	}
	defer dir.Close()
	info, err := s.metadata(dir, base)
	if err != nil {
		return err
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return failure(415, "special")
	}
	if !matches(info, expected) {
		return failure(409, "changed")
	}
	return chmodAt(dir, base, expected, uint32(mode))
}

func chmodMode(info fs.FileInfo, expected string, permissions uint32) (uint32, error) {
	if !matches(info, expected) {
		return 0, failure(409, "changed")
	}
	stat := info.Sys().(unix.Stat_t)
	return uint32(stat.Mode)&07000 | permissions, nil
}

func (s *fileStore) mkdir(name string, existOK bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, base, err := s.parent(name, true)
	if err != nil {
		return err
	}
	defer dir.Close()
	err = unix.Mkdirat(int(dir.Fd()), base, 0755)
	if !existOK || !errors.Is(err, fs.ErrExist) {
		return err
	}
	info, err := s.metadata(dir, base)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return failure(409, "conflict")
	}
	return nil
}

func matches(info fs.FileInfo, expected string) bool {
	return expected != "" && version(info) == expected
}

func (s *fileStore) target(dir *os.File, base, overwrite string) error {
	info, err := s.metadata(dir, base)
	if errors.Is(err, fs.ErrNotExist) {
		if overwrite != "" {
			return failure(409, "changed")
		}
		return nil
	}
	if err != nil {
		return err
	}
	if overwrite == "" {
		return &problem{status: 409, code: "conflict", version: version(info), replaceable: info.Mode().IsRegular()}
	}
	if !info.Mode().IsRegular() {
		return failure(409, "cannotReplace")
	}
	if !matches(info, overwrite) {
		return failure(409, "changed")
	}
	return nil
}

func (s *fileStore) move(source, destination, expected, overwrite string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	from, base, err := s.parent(source, true)
	if err != nil {
		return err
	}
	defer from.Close()
	info, err := s.metadata(from, base)
	if err != nil {
		return err
	}
	if !matches(info, expected) {
		return failure(409, "changed")
	}
	if source == destination {
		return nil
	}
	if strings.HasPrefix(destination, source+"/") {
		return failure(400, "recursiveMove")
	}
	to, newBase, err := s.parent(destination, true)
	if err != nil {
		return err
	}
	defer to.Close()
	if err := s.target(to, newBase, overwrite); err != nil {
		return err
	}
	if overwrite == "" {
		err = renameNoReplace(int(from.Fd()), base, int(to.Fd()), newBase)
		if errors.Is(err, unix.EEXIST) {
			if conflict := s.target(to, newBase, ""); conflict != nil {
				return conflict
			}
		}
	} else {
		if !info.Mode().IsRegular() {
			return failure(409, "cannotReplace")
		}
		err = unix.Renameat(int(from.Fd()), base, int(to.Fd()), newBase)
	}
	return err
}

func (s *fileStore) upload(name, overwrite string, body io.Reader, size int64) error {
	return s.writeFile(name, overwrite, body, size, nil)
}

func (s *fileStore) writeFile(name, overwrite string, body io.Reader, size int64, original fs.FileInfo) error {
	dir, base, err := s.parent(name, true)
	if err != nil {
		return err
	}
	defer dir.Close()
	checkTarget := func() error {
		if err := s.target(dir, base, overwrite); err != nil {
			return err
		}
		if original != nil {
			return unix.Faccessat(int(dir.Fd()), base, unix.W_OK, unix.AT_EACCESS|unix.AT_SYMLINK_NOFOLLOW)
		}
		return nil
	}
	s.mu.Lock()
	err = checkTarget()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	var random [16]byte
	rand.Read(random[:])
	temp := uploadPrefix + hex.EncodeToString(random[:])
	file, err := openAt(dir, temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL, 0600)
	if err != nil {
		return err
	}
	fd := int(dir.Fd())
	defer unix.Unlinkat(fd, temp, 0)
	written, err := io.Copy(file, body)
	if err != nil || (size >= 0 && size != written) {
		file.Close()
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			return err
		}
		return failure(400, "interrupted")
	}
	if original != nil {
		err = preserveFileMode(file, original)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// A concurrent move may have changed which directory the path names.
	fresh, _, err := s.parent(name, true)
	if err != nil {
		return err
	}
	defer fresh.Close()
	before, err := descriptorStat(dir)
	if err != nil {
		return err
	}
	after, err := descriptorStat(fresh)
	if err != nil {
		return err
	}
	if !sameFile(before, after) {
		return failure(409, "changed")
	}
	if err := checkTarget(); err != nil {
		return err
	}
	if overwrite != "" {
		return unix.Renameat(fd, temp, fd, base)
	}
	err = renameNoReplace(fd, temp, fd, base)
	if errors.Is(err, unix.EEXIST) {
		if conflict := s.target(dir, base, ""); conflict != nil {
			return conflict
		}
	}
	return err
}

func preserveFileMode(temp *os.File, original fs.FileInfo) error {
	info, err := descriptorStat(temp)
	if err != nil {
		return err
	}
	before := original.Sys().(unix.Stat_t)
	after := info.Sys().(unix.Stat_t)
	if before.Uid != after.Uid || before.Gid != after.Gid {
		if err := temp.Chown(int(before.Uid), int(before.Gid)); err != nil {
			return err
		}
	}
	return temp.Chmod(original.Mode().Perm())
}

// Check descendants before removing any of them, including nested mount points.
func (s *fileStore) checkRecursiveDelete(parent *os.File, name string, expected fs.FileInfo) error {
	dir, err := openAt(parent, name, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return err
	}
	defer dir.Close()
	info, err := descriptorStat(dir)
	if err != nil {
		return err
	}
	if !sameFile(expected, info) || !s.visible(info) {
		return failure(409, "changed")
	}
	parentInfo, err := descriptorStat(parent)
	if err != nil {
		return err
	}
	if parentInfo.Sys().(unix.Stat_t).Dev != info.Sys().(unix.Stat_t).Dev || differentDirectoryMount(parent, dir) {
		return failure(409, "busy")
	}
	if err := unix.Faccessat(int(dir.Fd()), ".", unix.W_OK|unix.X_OK, unix.AT_EACCESS); err != nil {
		return err
	}
	items, err := dir.Readdirnames(-1)
	if err != nil {
		return err
	}
	for _, item := range items {
		if strings.HasPrefix(item, uploadPrefix) {
			return failure(409, "busy")
		}
		child, err := s.metadata(dir, item)
		if err != nil {
			return err
		}
		if child.IsDir() {
			if err := s.checkRecursiveDelete(dir, item, child); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *fileStore) unlink(parent *os.File, name string, info fs.FileInfo) error {
	return s.unlinkReported(parent, name, info, "", nil)
}

func (s *fileStore) unlinkReported(parent *os.File, name string, info fs.FileInfo, location string, report func(string)) error {
	fd := int(parent.Fd())
	if !info.IsDir() {
		err := unix.Unlinkat(fd, name, 0)
		if err == nil && report != nil {
			report(location)
		}
		return err
	}
	dir, err := openAt(parent, name, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return err
	}
	defer dir.Close()
	opened, err := descriptorStat(dir)
	if err != nil {
		return err
	}
	if !sameFile(info, opened) || !s.visible(opened) {
		return failure(409, "changed")
	}
	if differentDirectoryMount(parent, dir) {
		return failure(409, "busy")
	}
	items, err := dir.Readdirnames(-1)
	if err != nil {
		return err
	}
	for _, item := range items {
		if strings.HasPrefix(item, uploadPrefix) {
			return failure(409, "busy")
		}
		child, err := s.metadata(dir, item)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := s.unlinkReported(dir, item, child, filepath.Join(location, item), report); err != nil {
			return err
		}
	}
	current, err := s.metadata(parent, name)
	if err != nil {
		return err
	}
	if !sameFile(info, current) {
		return failure(409, "changed")
	}
	err = unix.Unlinkat(fd, name, unix.AT_REMOVEDIR)
	if err == nil && report != nil {
		report(location)
	}
	return err
}

func version(info fs.FileInfo) string {
	return fmt.Sprintf("%s:%x:%x:%x", fileIdentity(info), info.ModTime().UnixNano(), info.Size(), info.Mode())
}
