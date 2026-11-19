package main

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

// Prepare on disk before sending headers: unreadable or changing sources must
// return an error instead of publishing a seemingly successful, broken ZIP.
func (s *fileStore) folderArchive(ctx context.Context, state string, names []string) (*os.File, string, error) {
	if len(names) == 0 || len(names) > 5000 {
		return nil, "", failure(400, "badRequest")
	}
	selected := make([]string, 0, len(names))
	for _, name := range names {
		if err := s.checked(name, false); err != nil {
			return nil, "", err
		}
		covered := false
		for _, other := range names {
			if other != name && (other == "" || strings.HasPrefix(name, other+"/")) {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		duplicate := false
		for _, other := range selected {
			if other == name {
				duplicate = true
			}
		}
		if !duplicate {
			selected = append(selected, name)
		}
	}
	out, err := os.CreateTemp(state, ".fm-zip-")
	if err != nil {
		return nil, "", err
	}
	if err = os.Remove(out.Name()); err != nil {
		out.Close()
		return nil, "", err
	}
	keep := false
	defer func() {
		if !keep {
			out.Close()
		}
	}()
	writer := zip.NewWriter(out)
	scanning := true
	var total, done int64
	snapshots := map[string]string{}
	advance := func(n int) {
		done += int64(n)
		reported := done
		if reported >= total {
			reported = total - 1
		}
		reportOperation(ctx, "zipPreparing", reported, total)
	}
	var appendPath func(string, string, int) error
	appendPath = func(name, target string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if depth > 256 || strings.Contains(target, "\\") {
			return failure(400, "invalidPath")
		}
		var info fs.FileInfo
		if name == "" {
			info, err = descriptorStat(s.root)
		} else {
			dir, base, e := s.parent(name, false)
			if e != nil {
				return e
			}
			info, e = s.metadata(dir, base)
			dir.Close()
			if e != nil {
				return e
			}
		}
		if info == nil {
			return err
		}
		if scanning {
			if len(snapshots) >= 100000 {
				return failure(413, "zipTooMany")
			}
			snapshots[name] = version(info)
		} else if !matches(info, snapshots[name]) {
			return failure(409, "changed")
		}
		header, e := zip.FileInfoHeader(info)
		if e != nil {
			return e
		}
		header.Name = target
		if info.IsDir() {
			header.Name = strings.TrimSuffix(target, "/") + "/"
			if scanning {
				total++
			} else {
				if _, e = writer.CreateHeader(header); e != nil {
					return e
				}
				advance(1)
			}
			dir, e := s.dir(name)
			if e != nil {
				return e
			}
			children, e := dir.Readdirnames(-1)
			dir.Close()
			if e != nil {
				return e
			}
			for _, child := range children {
				childPath := path.Join(name, child)
				if !validName(child) {
					continue
				}
				if s.checked(childPath, false) != nil {
					continue
				}
				e = appendPath(childPath, path.Join(target, child), depth+1)
				var p *problem
				if errors.As(e, &p) && p.code == "protected" {
					continue
				}
				if e != nil {
					return e
				}
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return failure(415, "zipSpecial")
		}
		if scanning {
			total += max(info.Size(), 1)
			return nil
		}
		input, latest, e := s.regular(name)
		if e != nil {
			return e
		}
		defer input.Close()
		if !matches(latest, version(info)) {
			return failure(409, "changed")
		}
		header.Method = zip.Deflate
		destination, e := writer.CreateHeader(header)
		if e != nil {
			return e
		}
		if _, e = io.Copy(progressWriter{destination, advance}, contextImageReader{ctx, input}); e != nil {
			return e
		}
		if info.Size() == 0 {
			advance(1)
		}
		after, e := descriptorStat(input)
		if e != nil {
			return e
		}
		if !matches(after, version(info)) {
			return failure(409, "changed")
		}
		return nil
	}
	for _, name := range selected {
		if err = appendPath(name, name, 0); err != nil {
			return nil, "", err
		}
	}
	total++
	scanning = false
	reportOperation(ctx, "zipPreparing", 0, total)
	archiveName := "files.zip"
	for _, name := range selected {
		target := name
		if len(selected) == 1 {
			target = path.Base(name)
			if name == "" {
				target = path.Base(s.absolute)
				if target == "/" || target == "." {
					target = "server-files"
				}
			}
			archiveName = target + ".zip"
		}
		if err = appendPath(name, target, 0); err != nil {
			return nil, "", err
		}
	}
	if err = writer.Close(); err != nil {
		return nil, "", err
	}
	reportOperation(ctx, "zipPreparing", total, total)
	if _, err = out.Seek(0, io.SeekStart); err != nil {
		return nil, "", err
	}
	keep = true
	return out, archiveName, nil
}

func registerFolderDownloads(register func(string, bool, func(http.ResponseWriter, *http.Request) error), s *fileStore, state string) {
	gate := make(chan struct{}, 1)
	type readyArchive struct {
		file *os.File
		name string
	}
	var mu sync.Mutex
	pending := map[string]readyArchive{}
	register("POST /api/download-zip", true, func(w http.ResponseWriter, r *http.Request) error {
		var input struct{ Paths []string }
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			return failure(400, "badRequest")
		}
		if decoder.Decode(new(any)) != io.EOF {
			return failure(400, "badRequest")
		}
		return operationResponse(w, r, func(ctx context.Context) (any, error) {
			select {
			case gate <- struct{}{}:
				defer func() { <-gate }()
			default:
				return nil, failure(503, "zipBusy")
			}
			reportOperation(ctx, "zipPreparing", 0, 1)
			file, name, err := s.folderArchive(ctx, state, input.Paths)
			if err != nil {
				return nil, err
			}
			tokenBytes := make([]byte, 24)
			if _, err = rand.Read(tokenBytes); err != nil {
				file.Close()
				return nil, err
			}
			token := hex.EncodeToString(tokenBytes)
			mu.Lock()
			if len(pending) >= 4 {
				mu.Unlock()
				file.Close()
				return nil, failure(503, "zipBusy")
			}
			pending[token] = readyArchive{file, name}
			mu.Unlock()
			time.AfterFunc(5*time.Minute, func() {
				mu.Lock()
				defer mu.Unlock()
				if item, ok := pending[token]; ok {
					delete(pending, token)
					item.file.Close()
				}
			})
			reportOperation(ctx, "zipPreparing", 1, 1)
			return map[string]string{"ticket": token, "name": name}, nil
		})
	})
	register("GET /api/download-zip", true, func(w http.ResponseWriter, r *http.Request) error {
		var out *os.File
		var name string
		if token := r.URL.Query().Get("ticket"); token != "" {
			mu.Lock()
			item, ok := pending[token]
			if ok {
				delete(pending, token)
			}
			mu.Unlock()
			if !ok {
				return failure(404, "missing")
			}
			out, name = item.file, item.name
		} else {
			select {
			case gate <- struct{}{}:
			default:
				return failure(503, "zipBusy")
			}
			var err error
			out, name, err = s.folderArchive(r.Context(), state, r.URL.Query()["path"])
			<-gate
			if err != nil {
				return err
			}
		}
		defer out.Close()
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		http.ServeContent(w, r, name, time.Time{}, out)
		return nil
	})
}
