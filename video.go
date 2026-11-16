package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const maxVideoPreview = 512 << 20

type videoKey struct{ path, version string }

type videoPreviews struct {
	executable string
	state      string
	converting chan struct{}
	mu         sync.Mutex
	files      map[videoKey]*os.File
	order      []videoKey
}

func newVideoPreviews(executable, state string) *videoPreviews {
	return &videoPreviews{executable: executable, state: state, converting: make(chan struct{}, 1), files: make(map[videoKey]*os.File)}
}

func (v *videoPreviews) close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, file := range v.files {
		file.Close()
	}
}

func (v *videoPreviews) cached(key videoKey) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.files[key] != nil
}

func (v *videoPreviews) convert(ctx context.Context, key videoKey, input *os.File) error {
	if v.executable == "" {
		return failure(503, "videoToolMissing")
	}
	if v.cached(key) {
		return nil
	}
	select {
	case v.converting <- struct{}{}:
		defer func() { <-v.converting }()
	default:
		return failure(503, "videoBusy")
	}
	if v.cached(key) {
		return nil
	}

	output, err := os.CreateTemp(v.state, ".fm-video-")
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			output.Close()
		}
	}()
	// Hold the temporary result by descriptor so it leaves no file after a crash.
	if err := os.Remove(output.Name()); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, v.executable,
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-max_alloc", "134217728",
		"-protocol_whitelist", "fd", "-format_whitelist", "mov,matroska,webm,avi,asf,mpeg,mpegts,mpegvideo,flv,ogg,rm",
		"-max_pixels", "40000000", "-threads", "2", "-fd", "3", "-i", "fd:",
		"-map", "0:V:0", "-map", "0:a:0?", "-map_metadata", "-1", "-map_chapters", "-1",
		"-filter_threads", "2", "-vf", "scale=w='min(1920,iw)':h='min(1080,ih)':force_original_aspect_ratio=decrease:force_divisible_by=2",
		"-c:v", "libx264", "-preset", "veryfast", "-pix_fmt", "yuv420p", "-threads", "2",
		"-c:a", "aac", "-ac", "2", "-fs", strconv.Itoa(maxVideoPreview), "-fd", "4", "-f", "mp4", "fd:")
	command.ExtraFiles = []*os.File{input, output}
	if err := command.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return failure(422, "videoLimit")
		}
		return failure(422, "videoConvertFailed")
	}
	info, err := output.Stat()
	if err != nil {
		return err
	}
	if info.Size() == 0 || info.Size() >= maxVideoPreview {
		return failure(422, "videoLimit")
	}
	latest, err := descriptorStat(input)
	if err != nil {
		return err
	}
	if version(latest) != key.version {
		return failure(409, "changed")
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.order) == 2 {
		old := v.order[0]
		v.files[old].Close()
		delete(v.files, old)
		v.order = v.order[1:]
	}
	v.files[key] = output
	v.order = append(v.order, key)
	keep = true
	return nil
}

func (v *videoPreviews) serve(w http.ResponseWriter, r *http.Request, key videoKey, modified time.Time) error {
	v.mu.Lock()
	file := v.files[key]
	var fd int
	var err error
	if file != nil {
		fd, err = unix.FcntlInt(file.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	}
	v.mu.Unlock()
	if file == nil {
		return failure(404, "videoNotReady")
	}
	if err != nil {
		return err
	}
	preview := os.NewFile(uintptr(fd), "compatible.mp4")
	defer preview.Close()
	info, err := preview.Stat()
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	http.ServeContent(w, r, "compatible.mp4", modified, io.NewSectionReader(preview, 0, info.Size()))
	return nil
}

func videoExecutable(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if strings.ContainsRune(path, filepath.Separator) {
		var err error
		path, err = filepath.Abs(path)
		if err != nil {
			return "", err
		}
	}
	file, err := exec.LookPath(path)
	if err != nil {
		return "", fmt.Errorf("FFmpeg executable: %w", err)
	}
	return canonical(file)
}

func sourceMediaType(file *os.File, name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if ext != ".ts" {
		return mediaType(ext)
	}
	// Keep TypeScript previews for text files with the same extension.
	var prefix [640]byte
	n, _ := file.ReadAt(prefix[:], 0)
	for _, framing := range [][2]int{{0, 188}, {4, 192}, {0, 204}} {
		first, step := framing[0], framing[1]
		if n > first+2*step && prefix[first] == 0x47 && prefix[first+step] == 0x47 && prefix[first+2*step] == 0x47 {
			return "video/mp2t"
		}
	}
	return ""
}
