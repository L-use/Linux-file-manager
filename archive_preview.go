package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/bodgit/sevenzip"
	"github.com/nwaples/rardecode/v2"
)

const archivePreviewBytes = 512 << 20
const archiveScanBytes = 128 << 20

type archivePreviewEntry struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Dir      bool   `json:"dir"`
	Download *int   `json:"download,omitempty"`
}

type archivePreview struct {
	Entries []archivePreviewEntry `json:"entries"`
	Limited bool                  `json:"limited"`
}

func archiveError(err error) error {
	if err != nil {
		return failure(415, "archiveReadError")
	}
	return nil
}

func (s *fileStore) previewArchive(ctx context.Context, name string, limit int) (archivePreview, error) {
	out := archivePreview{Entries: make([]archivePreviewEntry, 0)}
	f, info, err := s.regular(name)
	if err != nil {
		return out, err
	}
	defer f.Close()
	if info.Size() > archivePreviewBytes {
		return out, failure(413, "archivePreviewLimit")
	}
	appendEntry := func(e archivePreviewEntry) bool {
		if len(out.Entries) >= limit {
			out.Limited = true
			return false
		}
		out.Entries = append(out.Entries, e)
		return true
	}
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		archive, err := zip.NewReader(f, info.Size())
		if err != nil {
			return out, archiveError(err)
		}
		for i, member := range archive.File {
			e := archivePreviewEntry{Name: member.Name, Size: int64(member.UncompressedSize64), Dir: member.FileInfo().IsDir()}
			if member.Mode().IsRegular() && member.Flags&1 == 0 && member.UncompressedSize64 <= archivePreviewBytes {
				index := i
				e.Download = &index
			}
			if !appendEntry(e) {
				break
			}
		}
	case strings.HasSuffix(lower, ".7z"):
		archive, err := sevenzip.NewReader(f, info.Size())
		if err != nil {
			return out, archiveError(err)
		}
		for _, member := range archive.File {
			if !appendEntry(archivePreviewEntry{Name: member.Name, Size: int64(member.UncompressedSize), Dir: member.FileInfo().IsDir()}) {
				break
			}
		}
	case strings.HasSuffix(lower, ".rar"):
		input := &io.LimitedReader{R: contextImageReader{ctx, f}, N: archiveScanBytes}
		reader, err := rardecode.NewReader(input, rardecode.MaxDictionarySize(32<<20))
		if err != nil {
			return out, archiveError(err)
		}
		for {
			header, err := reader.Next()
			if input.N == 0 {
				out.Limited = true
				break
			}
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return out, archiveError(err)
			}
			if !appendEntry(archivePreviewEntry{Name: header.Name, Size: header.UnPackedSize, Dir: header.IsDir}) {
				break
			}
		}
	case strings.HasSuffix(lower, ".tar"), strings.HasSuffix(lower, ".tgz"), strings.HasSuffix(lower, ".tar.gz"):
		var input io.Reader = contextImageReader{ctx, f}
		if !strings.HasSuffix(lower, ".tar") {
			compressed, err := gzip.NewReader(input)
			if err != nil {
				return out, archiveError(err)
			}
			defer compressed.Close()
			input = compressed
		}
		bounded := &io.LimitedReader{R: input, N: archiveScanBytes}
		reader := tar.NewReader(bounded)
		for {
			header, err := reader.Next()
			if bounded.N == 0 {
				out.Limited = true
				break
			}
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return out, archiveError(err)
			}
			if !appendEntry(archivePreviewEntry{Name: header.Name, Size: header.Size, Dir: header.Typeflag == tar.TypeDir}) {
				break
			}
		}
	default:
		return out, failure(415, "unsupported")
	}
	return out, ctx.Err()
}

func (s *fileStore) downloadArchiveMember(w http.ResponseWriter, r *http.Request, name string, index int, expected string) error {
	if !strings.EqualFold(path.Ext(name), ".zip") || expected == "" {
		return failure(400, "badRequest")
	}
	f, info, err := s.regular(name)
	if err != nil {
		return err
	}
	defer f.Close()
	if !matches(info, expected) {
		return failure(409, "changed")
	}
	if info.Size() > archivePreviewBytes {
		return failure(413, "archivePreviewLimit")
	}
	archive, err := zip.NewReader(f, info.Size())
	if err != nil {
		return archiveError(err)
	}
	if index < 0 || index >= len(archive.File) {
		return failure(404, "missing")
	}
	member := archive.File[index]
	if !member.Mode().IsRegular() || member.Flags&1 != 0 || member.UncompressedSize64 > archivePreviewBytes {
		return failure(415, "unsupported")
	}
	input, err := member.Open()
	if err != nil {
		return archiveError(err)
	}
	defer input.Close()
	filename := path.Base(strings.ReplaceAll(member.Name, "\\", "/"))
	if !validName(filename) {
		return failure(400, "invalidPath")
	}
	setDownloadHeaders(w, filename)
	w.Header().Set("Content-Length", strconv.FormatUint(member.UncompressedSize64, 10))
	_, _ = io.Copy(w, io.LimitReader(input, int64(member.UncompressedSize64)))
	return nil
}
