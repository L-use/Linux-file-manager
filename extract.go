package main

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"golang.org/x/sys/unix"
)

const maxExtractEntries = 20000
const maxExtractBytes = 16 << 30

func (s *fileStore) extractZip(ctx context.Context, name, destination, expected string) error {
	if !strings.EqualFold(path.Ext(name), ".zip") || !validName(destination) {
		return failure(400, "invalidPath")
	}
	target := path.Join(parentName(name), destination)
	parent, base, err := s.parent(target, true)
	if err != nil {
		return err
	}
	defer parent.Close()
	parentFD := int(parent.Fd())
	if err := s.target(parent, base, ""); err != nil {
		return err
	}

	source, original, err := s.regular(name)
	if err != nil {
		return err
	}
	defer source.Close()
	if !matches(original, expected) {
		return failure(409, "changed")
	}
	archive, err := zip.NewReader(source, original.Size())
	if archive == nil || (err != nil && !errors.Is(err, zip.ErrInsecurePath)) {
		return failure(415, "zipInvalid")
	}
	if len(archive.File) > maxExtractEntries {
		return failure(413, "zipLimit")
	}
	var expanded uint64
	entryNames := make(map[string]bool)
	for _, member := range archive.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		folder := member.Mode().IsDir()
		if !folder && !member.Mode().IsRegular() {
			return failure(415, "zipUnsafe")
		}
		memberPath := strings.TrimSuffix(member.Name, "/")
		pieces := strings.Split(memberPath, "/")
		if len(pieces) > 64 || strings.Contains(memberPath, "\\") || strings.Contains(pieces[0], ":") {
			return failure(415, "zipUnsafe")
		}
		for _, piece := range pieces {
			if !validName(piece) {
				return failure(415, "zipUnsafe")
			}
		}
		if err := s.checked(path.Join(target, memberPath), true); err != nil {
			return err
		}
		if entryNames[memberPath] {
			return failure(415, "zipUnsafe")
		}
		entryNames[memberPath] = true
		if member.UncompressedSize64 > maxExtractBytes-expanded {
			return failure(413, "zipLimit")
		}
		expanded += member.UncompressedSize64
	}
	temp := uploadPrefix + rand.Text()
	if err := unix.Mkdirat(parentFD, temp, 0700); err != nil {
		return err
	}
	staging, err := openAt(parent, temp, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		_ = unix.Unlinkat(parentFD, temp, unix.AT_REMOVEDIR)
		return err
	}
	defer staging.Close()
	initial, err := descriptorStat(staging)
	if err != nil {
		_ = unix.Unlinkat(parentFD, temp, unix.AT_REMOVEDIR)
		return err
	}
	defer func() { _ = s.unlink(parent, temp, initial) }()
	var extracted int64
	update := func(n int) {
		extracted += int64(n)
		reportOperation(ctx, "zipExtractingProgress", extracted, int64(expanded))
	}
	update(0)
	for index, member := range archive.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := extractZipMember(ctx, staging, member, update); err != nil {
			return err
		}
		if expanded == 0 {
			reportOperation(ctx, "zipExtractingProgress", int64(index+1), int64(len(archive.File)))
		}
	}
	reportOperation(ctx, "zipFinishing", 0, 0)
	if latest, err := descriptorStat(source); err != nil || !matches(latest, expected) {
		if err != nil {
			return err
		}
		return failure(409, "changed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	fresh, freshBase, err := s.parent(target, true)
	if err != nil {
		return err
	}
	defer fresh.Close()
	before, err := descriptorStat(parent)
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
	latest, err := s.metadata(parent, path.Base(name))
	if err != nil {
		return err
	}
	if !matches(latest, expected) {
		return failure(409, "changed")
	}
	if err := renameNoReplace(parentFD, temp, int(fresh.Fd()), freshBase); errors.Is(err, fs.ErrExist) {
		return failure(409, "conflict")
	} else {
		return err
	}
}

type extractionReader struct {
	ctx context.Context
	io.Reader
}

func (r extractionReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.Reader.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		err = failure(415, "zipInvalid")
	}
	return n, err
}

func extractZipMember(ctx context.Context, root *os.File, member *zip.File, progress func(int)) error {
	pieces := strings.Split(strings.TrimSuffix(member.Name, "/"), "/")
	dirs := pieces[:len(pieces)-1]
	folder := member.Mode().IsDir()
	if folder {
		dirs = pieces
	}
	current, err := openAt(root, ".", unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return err
	}
	for _, piece := range dirs {
		if err := unix.Mkdirat(int(current.Fd()), piece, 0755); err != nil && !errors.Is(err, fs.ErrExist) {
			current.Close()
			return err
		}
		next, err := openAt(current, piece, unix.O_RDONLY|unix.O_DIRECTORY, 0)
		current.Close()
		if err != nil {
			return err
		}
		current = next
	}
	defer current.Close()
	if folder {
		return nil
	}
	input, err := member.Open()
	if err != nil {
		return failure(415, "zipInvalid")
	}
	defer input.Close()
	mode := 0600 | uint32(member.Mode().Perm())&0755
	output, err := openAt(current, pieces[len(pieces)-1], unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL, mode)
	if err != nil {
		return err
	}
	length, err := io.Copy(progressWriter{output, progress}, io.LimitReader(extractionReader{ctx, input}, int64(member.UncompressedSize64)+1))
	closeErr := output.Close()
	if err != nil {
		return err
	}
	if uint64(length) != member.UncompressedSize64 {
		return failure(415, "zipInvalid")
	}
	return closeErr
}
