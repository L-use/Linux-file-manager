package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type trashClearResult struct {
	Removed int `json:"removed"`
	Failed  int `json:"failed"`
}

const trashPayload = "payload"
const trashMetadata = "metadata.json"

type trashEntry struct {
	ID      string `json:"id"`
	Root    string `json:"root"`
	Path    string `json:"path"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Deleted int64  `json:"deleted"`
}

type trashStore struct {
	dir   *os.File
	files *fileStore
}

func openTrash(state string, files *fileStore) (*trashStore, error) {
	parent, err := os.Open(state)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	err = unix.Mkdirat(int(parent.Fd()), "trash", 0700)
	if err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, err
	}
	dir, err := openAt(parent, "trash", unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	info, err := dir.Stat()
	if err != nil || info.Mode().Perm()&0077 != 0 {
		dir.Close()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("trash directory must be private")
	}
	return &trashStore{dir: dir, files: files}, nil
}

func (t *trashStore) removeRecord(dir *os.File, id string) {
	_ = unix.Unlinkat(int(dir.Fd()), trashMetadata, 0)
	_ = unix.Unlinkat(int(t.dir.Fd()), id, unix.AT_REMOVEDIR)
}

func (t *trashStore) put(name, expected string) (trashEntry, error) {
	t.files.mu.Lock()
	defer t.files.mu.Unlock()

	var result trashEntry
	source, base, err := t.files.parent(name, true)
	if err != nil {
		return result, err
	}
	defer source.Close()
	info, err := t.files.metadata(source, base)
	if err != nil {
		return result, err
	}
	if !matches(info, expected) {
		return result, failure(409, "changed")
	}

	id := "item-" + rand.Text()
	if err := unix.Mkdirat(int(t.dir.Fd()), id, 0700); err != nil {
		return result, err
	}
	dir, err := openAt(t.dir, id, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		_ = unix.Unlinkat(int(t.dir.Fd()), id, unix.AT_REMOVEDIR)
		return result, err
	}
	defer dir.Close()
	moved := false
	defer func() {
		if !moved {
			t.removeRecord(dir, id)
		}
	}()

	record := trashEntry{ID: id, Root: t.files.absolute, Path: name, Name: path.Base(name),
		Kind: toEntry(name, info).Kind, Deleted: time.Now().UnixMilli()}
	manifest, err := openAt(dir, trashMetadata, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL, 0600)
	if err != nil {
		return result, err
	}
	err = json.NewEncoder(manifest).Encode(record)
	if err == nil {
		err = manifest.Sync()
	}
	closeErr := manifest.Close()
	if err != nil {
		return result, err
	}
	if closeErr != nil {
		return result, closeErr
	}
	latest, err := t.files.metadata(source, base)
	if err != nil {
		return result, err
	}
	if !matches(latest, expected) {
		return result, failure(409, "changed")
	}
	err = renameNoReplace(int(source.Fd()), base, int(dir.Fd()), trashPayload)
	if errors.Is(err, unix.EXDEV) {
		return result, failure(409, "trashCrossDevice")
	}
	if err != nil {
		return result, err
	}
	moved = true
	return record, nil
}

func (t *trashStore) open(id string) (*os.File, trashEntry, error) {
	var record trashEntry
	if !validName(id) || !strings.HasPrefix(id, "item-") {
		return nil, record, failure(400, "invalidPath")
	}
	dir, err := openAt(t.dir, id, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, record, err
	}
	f, err := openAt(dir, trashMetadata, unix.O_RDONLY, 0)
	if err == nil {
		err = json.NewDecoder(io.LimitReader(f, 16<<10)).Decode(&record)
		f.Close()
	}
	if err == nil && (record.ID != id || record.Root != t.files.absolute) {
		err = fs.ErrNotExist
	}
	if err == nil {
		_, err = t.files.metadata(dir, trashPayload)
	}
	if err != nil {
		dir.Close()
		return nil, trashEntry{}, err
	}
	return dir, record, nil
}

func (t *trashStore) list() ([]trashEntry, error) {
	t.files.mu.Lock()
	defer t.files.mu.Unlock()
	dir, err := openAt(t.dir, ".", unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	names, err := dir.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	result := make([]trashEntry, 0, len(names))
	for _, id := range names {
		recordDir, record, err := t.open(id)
		if err == nil {
			recordDir.Close()
			result = append(result, record)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Deleted > result[j].Deleted })
	return result, nil
}

func (t *trashStore) restore(id string) error {
	t.files.mu.Lock()
	defer t.files.mu.Unlock()
	dir, record, err := t.open(id)
	if err != nil {
		return err
	}
	defer dir.Close()
	target, base, err := t.files.parent(record.Path, true)
	if errors.Is(err, fs.ErrNotExist) {
		return failure(409, "trashParentMissing")
	}
	if err != nil {
		return err
	}
	defer target.Close()
	err = renameNoReplace(int(dir.Fd()), trashPayload, int(target.Fd()), base)
	if errors.Is(err, unix.EXDEV) {
		return failure(409, "trashCrossDevice")
	}
	if err != nil {
		return err
	}
	t.removeRecord(dir, id)
	return nil
}

func (t *trashStore) purge(id string) error {
	t.files.mu.Lock()
	defer t.files.mu.Unlock()
	dir, _, err := t.open(id)
	if err != nil {
		return err
	}
	defer dir.Close()
	info, err := t.files.metadata(dir, trashPayload)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := t.files.checkRecursiveDelete(dir, trashPayload, info); err != nil {
			return err
		}
	}
	if err := t.files.unlink(dir, trashPayload, info); err != nil {
		return err
	}
	t.removeRecord(dir, id)
	return nil
}

func (t *trashStore) clear(ctx context.Context) (trashClearResult, error) {
	var result trashClearResult
	items, err := t.list()
	if err != nil {
		return result, err
	}
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := t.purge(item.ID); err == nil {
			result.Removed++
		} else if !errors.Is(err, fs.ErrNotExist) {
			result.Failed++
		}
	}
	return result, nil
}
