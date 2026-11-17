package main

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"

	"golang.org/x/sys/unix"
)

type problem struct {
	status      int
	code        string
	version     string
	replaceable bool
}

func (e *problem) Error() string               { return e.code }
func failure(status int, code string) *problem { return &problem{status: status, code: code} }

func report(w http.ResponseWriter, err error) {
	var p *problem
	if !errors.As(err, &p) {
		switch {
		case errors.Is(err, fs.ErrNotExist):
			p = failure(404, "missing")
		case errors.Is(err, fs.ErrPermission), errors.Is(err, unix.EROFS):
			p = failure(403, "permission")
		case errors.Is(err, unix.ELOOP), errors.Is(err, unix.ENOTDIR):
			p = failure(403, "boundary")
		case errors.Is(err, unix.EXDEV):
			p = failure(409, "crossDevice")
		case errors.Is(err, unix.ENOTEMPTY), errors.Is(err, fs.ErrExist):
			p = failure(409, "conflict")
		case errors.Is(err, unix.ENOSPC), errors.Is(err, unix.EDQUOT):
			p = failure(507, "space")
		case errors.Is(err, unix.EBUSY):
			p = failure(409, "busy")
		case errors.Is(err, unix.EINVAL):
			p = failure(400, "invalidPath")
		default:
			p = failure(500, "io")
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-FM-Error", p.code)
	w.WriteHeader(p.status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": p.code, "version": p.version, "replaceable": p.replaceable})
}

func respond(w http.ResponseWriter, v any) error {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
	return nil
}

func parseJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	if err := d.Decode(dst); err != nil {
		return failure(400, "badRequest")
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return failure(400, "badRequest")
	}
	return nil
}

func officeEdit[T any](save func(string, string, []T) error) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, officeEditLimit))
		var changes []T
		if err := decoder.Decode(&changes); err != nil {
			var limit *http.MaxBytesError
			if errors.As(err, &limit) {
				return failure(413, "editLimit")
			}
			return failure(400, "badRequest")
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return failure(400, "badRequest")
		}
		query := r.URL.Query()
		if err := save(query.Get("path"), query.Get("version"), changes); err != nil {
			return err
		}
		return respond(w, map[string]bool{"ok": true})
	}
}

func routes(a *authStore, s *fileStore, trash *trashStore, videos *videoPreviews, web fs.FS) http.Handler {
	mux := http.NewServeMux()
	type action func(http.ResponseWriter, *http.Request) error
	register := func(pattern string, private bool, handler action) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if private && !a.hasSession(r) {
				report(w, failure(401, "unauthenticated"))
				return
			}
			if r.Method == "POST" || r.Method == "PUT" {
				// A custom header restricts mutations to same-origin JavaScript.
				if r.Header.Get("X-FM-Request") != "1" || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
					report(w, failure(403, "permission"))
					return
				}
			}
			if err := handler(w, r); err != nil {
				report(w, err)
			}
		})
	}
	register("POST /api/login", false, a.login)
	register("POST /api/logout", true, a.logout)
	register("GET /api/session", true, func(w http.ResponseWriter, r *http.Request) error {
		return respond(w, map[string]string{"user": "admin", "root": s.absolute})
	})
	monitor := &serverMonitor{}
	register("GET /api/server-status", true, func(w http.ResponseWriter, r *http.Request) error {
		return respond(w, monitor.sample(s.root, r.URL.Query().Get("services") == "1"))
	})
	register("GET /api/list", true, func(w http.ResponseWriter, r *http.Request) error {
		values, err := s.list(r.URL.Query().Get("path"))
		if err != nil {
			return err
		}
		return respond(w, values)
	})
	register("GET /api/search", true, func(w http.ResponseWriter, r *http.Request) error {
		q := r.URL.Query()
		result, err := s.search(r.Context(), q.Get("path"), q.Get("q"))
		if err != nil {
			return err
		}
		return respond(w, result)
	})
	register("GET /api/trash", true, func(w http.ResponseWriter, r *http.Request) error {
		entries, err := trash.list()
		if err != nil {
			return err
		}
		return respond(w, entries)
	})
	register("POST /api/trash/clear", true, func(w http.ResponseWriter, r *http.Request) error {
		result, err := trash.clear(r.Context())
		if err != nil {
			return err
		}
		return respond(w, result)
	})
	for action, operation := range map[string]func(string) error{"restore": trash.restore, "purge": trash.purge} {
		register("POST /api/trash/"+action, true, func(w http.ResponseWriter, r *http.Request) error {
			var input struct{ ID string }
			if err := parseJSON(w, r, &input); err != nil {
				return err
			}
			if err := operation(input.ID); err != nil {
				return err
			}
			return respond(w, map[string]bool{"ok": true})
		})
	}
	register("GET /api/stat", true, func(w http.ResponseWriter, r *http.Request) error {
		name := r.URL.Query().Get("path")
		if name == "" {
			return failure(400, "invalidPath")
		}
		value, err := s.stat(name)
		if err != nil {
			return err
		}
		return respond(w, value)
	})
	register("GET /api/details", true, func(w http.ResponseWriter, r *http.Request) error {
		value, err := s.details(r.URL.Query().Get("path"))
		if err != nil {
			return err
		}
		return respond(w, value)
	})
	register("GET /api/folder-contents", true, func(w http.ResponseWriter, r *http.Request) error {
		value, err := s.folderContents(r.Context(), r.URL.Query().Get("path"))
		if err != nil {
			return err
		}
		return respond(w, value)
	})
	derivatives := newThumbnails(videos.cache)
	register("GET /api/cache", true, func(w http.ResponseWriter, r *http.Request) error {
		return respond(w, videos.cache.stats())
	})
	register("PUT /api/cache", true, func(w http.ResponseWriter, r *http.Request) error {
		var settings struct {
			LimitMiB int `json:"limitMiB"`
		}
		if err := parseJSON(w, r, &settings); err != nil {
			return err
		}
		if !validCacheLimit(settings.LimitMiB) {
			return failure(400, "badRequest")
		}
		if err := videos.cache.setLimit(settings.LimitMiB); err != nil {
			return err
		}
		return respond(w, videos.cache.stats())
	})
	register("POST /api/cache/clear", true, func(w http.ResponseWriter, r *http.Request) error {
		videos.cache.clear()
		return respond(w, videos.cache.stats())
	})
	register("GET /api/thumbnail", true, func(w http.ResponseWriter, r *http.Request) error {
		return derivatives.serve(s, w, r)
	})
	register("GET /api/file", true, func(w http.ResponseWriter, r *http.Request) error {
		f, info, err := s.regular(r.URL.Query().Get("path"))
		if err != nil {
			return err
		}
		defer f.Close()
		name := path.Base(r.URL.Query().Get("path"))
		contentType := sourceMediaType(f, name)
		disposition := "inline"
		if r.URL.Query().Get("download") == "1" || contentType == "" {
			disposition = "attachment"
			contentType = "application/octet-stream"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": name}))
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		http.ServeContent(w, r, name, info.ModTime(), f)
		return nil
	})
	for _, method := range []string{"POST", "GET"} {
		register(method+" /api/compatible-video", true, func(w http.ResponseWriter, r *http.Request) error {
			name := r.URL.Query().Get("path")
			f, info, err := s.regular(name)
			if err != nil {
				return err
			}
			defer f.Close()
			if !strings.HasPrefix(sourceMediaType(f, name), "video/") {
				return failure(415, "unsupported")
			}
			key := videoKey{name, version(info)}
			if r.Method != "POST" {
				return videos.serve(w, r, key, info.ModTime())
			}
			if err := videos.convert(r.Context(), key, f); err != nil {
				return err
			}
			return respond(w, map[string]bool{"ok": true})
		})
	}
	register("GET /api/text", true, func(w http.ResponseWriter, r *http.Request) error {
		f, info, err := s.regular(r.URL.Query().Get("path"))
		if err != nil {
			return err
		}
		defer f.Close()
		preview, err := readText(f, info)
		if err != nil {
			return err
		}
		return respond(w, preview)
	})
	register("PUT /api/text", true, func(w http.ResponseWriter, r *http.Request) error {
		bytes, err := io.ReadAll(http.MaxBytesReader(w, r.Body, textByteLimit))
		if err != nil {
			var limit *http.MaxBytesError
			if errors.As(err, &limit) {
				return failure(413, "editLimit")
			}
			return err
		}
		query := r.URL.Query()
		if err := s.saveText(query.Get("path"), query.Get("version"), bytes); err != nil {
			return err
		}
		return respond(w, map[string]bool{"ok": true})
	})
	register("GET /api/word", true, func(w http.ResponseWriter, r *http.Request) error {
		preview, err := s.wordPreview(r.URL.Query().Get("path"))
		if err != nil {
			return err
		}
		return respond(w, preview)
	})
	register("PUT /api/word", true, officeEdit(s.saveWord))
	register("PUT /api/sheet", true, officeEdit(s.saveSheet))
	register("POST /api/action", true, func(w http.ResponseWriter, r *http.Request) error {
		var input struct {
			Action, Path, Destination, Version, Overwrite, Permissions string
			ExistOK                                                    bool
		}
		if err := parseJSON(w, r, &input); err != nil {
			return err
		}
		var err error
		switch input.Action {
		case "mkdir":
			err = s.mkdir(input.Path, input.ExistOK)
		case "move":
			err = s.move(input.Path, input.Destination, input.Version, input.Overwrite)
		case "delete":
			var item trashEntry
			item, err = trash.put(input.Path, input.Version)
			if err == nil {
				return respond(w, item)
			}
		case "chmod":
			err = s.chmod(input.Path, input.Version, input.Permissions)
		case "extractZip":
			err = s.extractZip(r.Context(), input.Path, input.Destination, input.Version)
		default:
			return failure(400, "badRequest")
		}
		if err != nil {
			return err
		}
		return respond(w, map[string]bool{"ok": true})
	})
	register("PUT /api/upload", true, func(w http.ResponseWriter, r *http.Request) error {
		q := r.URL.Query()
		if err := s.upload(q.Get("path"), q.Get("overwrite"), r.Body, r.ContentLength); err != nil {
			return err
		}
		return respond(w, map[string]bool{"ok": true})
	})
	static := http.FileServerFS(web)
	mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && !strings.HasPrefix(r.URL.Path, "/vendor/") && r.URL.Path != "/app.js" && r.URL.Path != "/style.css" && r.URL.Path != "/preview.js" && r.URL.Path != "/icons.js" && r.URL.Path != "/i18n.js" && r.URL.Path != "/heif-worker.js" && r.URL.Path != "/night.css" && r.URL.Path != "/theme.js" && r.URL.Path != "/server-status.js" {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/") && r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, ".mjs") {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		}
		if strings.HasSuffix(r.URL.Path, ".wasm") {
			w.Header().Set("Content-Type", "application/wasm")
		}
		w.Header().Set("Cache-Control", "no-cache")
		static.ServeHTTP(w, r)
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; media-src 'self' blob:; worker-src 'self' blob:; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		mux.ServeHTTP(w, r)
	})
}

func mediaType(ext string) string {
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".avif":
		return "image/avif"
	case ".bmp":
		return "image/bmp"
	case ".heic", ".heics":
		return "image/heic"
	case ".heif", ".hif":
		return "image/heif"
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".ogv":
		return "video/ogg"
	case ".mov":
		return "video/quicktime"
	case ".mkv":
		return "video/x-matroska"
	case ".avi", ".divx":
		return "video/x-msvideo"
	case ".wmv", ".asf":
		return "video/x-ms-wmv"
	case ".mpeg", ".mpg", ".mpe", ".vob":
		return "video/mpeg"
	case ".mts", ".m2ts":
		return "video/mp2t"
	case ".flv", ".f4v":
		return "video/x-flv"
	case ".3gp", ".3g2":
		return "video/3gpp"
	case ".rm", ".rmvb":
		return "video/vnd.rn-realvideo"
	}
	return ""
}
