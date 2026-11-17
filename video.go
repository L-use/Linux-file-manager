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
	"time"
)

const maxVideoPreview = 512 << 20

type videoKey struct{ path, version string }

type videoPreviews struct {
	executable string
	state      string
	converting chan struct{}
	cache      *previewCache
}

func newVideoPreviews(executable, state string) *videoPreviews {
	return &videoPreviews{executable: executable, state: state, converting: make(chan struct{}, 1), cache: newPreviewCache(state)}
}

func (v *videoPreviews) convert(ctx context.Context, key videoKey, input *os.File, audio bool) error {
	if v.executable == "" {
		return failure(503, "videoToolMissing")
	}
	if v.cache.hasVideo(key) {
		return nil
	}
	select {
	case v.converting <- struct{}{}:
		defer func() { <-v.converting }()
	default:
		return failure(503, "videoBusy")
	}
	if v.cache.hasVideo(key) {
		return nil
	}

	budget, generation := v.cache.budget()
	maxSize := min(budget, maxVideoPreview)
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
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-max_alloc", "134217728",
		"-protocol_whitelist", "fd", "-format_whitelist", "mov,matroska,webm,avi,asf,mpeg,mpegts,mpegvideo,flv,ogg,rm,mp3,wav,flac,aac",
		"-max_pixels", "40000000", "-threads", "2", "-fd", "3", "-i", "fd:"}
	if audio {
		args = append(args, "-map", "0:a:0", "-vn")
	} else {
		args = append(args, "-map", "0:V:0", "-map", "0:a:0?", "-filter_threads", "2",
			"-vf", "scale=w='min(1920,iw)':h='min(1080,ih)':force_original_aspect_ratio=decrease:force_divisible_by=2",
			"-c:v", "libx264", "-preset", "veryfast", "-pix_fmt", "yuv420p", "-threads", "2")
	}
	args = append(args, "-map_metadata", "-1", "-map_chapters", "-1")
	format := "mp4"
	if audio {
		format = "mp3"
		args = append(args, "-c:a", "libmp3lame", "-q:a", "4", "-ac", "2")
	} else {
		args = append(args, "-c:a", "aac", "-ac", "2")
	}
	args = append(args, "-fs", strconv.FormatInt(maxSize, 10), "-fd", "4", "-f", format, "fd:")
	command := exec.CommandContext(ctx, v.executable, args...)
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
	if info.Size() == 0 || info.Size() >= maxSize {
		return failure(422, "videoLimit")
	}
	latest, err := descriptorStat(input)
	if err != nil {
		return err
	}
	if version(latest) != key.version {
		return failure(409, "changed")
	}

	if !v.cache.put(previewCacheEntry{key: key, video: output, size: info.Size()}, generation) {
		return failure(409, "videoNotReady")
	}
	keep = true
	return nil
}

func (v *videoPreviews) serve(w http.ResponseWriter, r *http.Request, key videoKey, modified time.Time, audio bool) error {
	preview, err := v.cache.openVideo(key)
	if err != nil {
		return err
	}
	defer preview.Close()
	info, err := preview.Stat()
	if err != nil {
		return err
	}
	mime, name := "video/mp4", "compatible.mp4"
	if audio {
		mime, name = "audio/mpeg", "compatible.mp3"
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	http.ServeContent(w, r, name, modified, io.NewSectionReader(preview, 0, info.Size()))
	return nil
}

func videoExecutable(path string) (string, error) {
	if path == "" {
		executable, err := exec.LookPath("ffmpeg")
		if err != nil {
			return "", nil
		}
		return canonical(executable)
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

func (v *videoPreviews) poster(s *fileStore, w http.ResponseWriter, r *http.Request) error {
	if v.executable == "" {
		return failure(503, "videoToolMissing")
	}
	name := r.URL.Query().Get("path")
	f, info, err := s.regular(name)
	if err != nil {
		return err
	}
	defer f.Close()
	if !strings.HasPrefix(sourceMediaType(f, name), "video/") {
		return failure(415, "unsupported")
	}
	key := thumbnailKey{name, version(info), -1}
	if cached := v.cache.image(key); cached.data != nil {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(cached.data)
		return nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	select {
	case v.converting <- struct{}{}:
		defer func() { <-v.converting }()
	case <-ctx.Done():
		return failure(503, "videoBusy")
	}
	if cached := v.cache.image(key); cached.data != nil {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(cached.data)
		return nil
	}
	_, generation := v.cache.budget()
	command := exec.CommandContext(ctx, v.executable, "-hide_banner", "-loglevel", "error", "-nostdin",
		"-max_alloc", "67108864", "-protocol_whitelist", "fd,pipe", "-format_whitelist", "mov,matroska,webm,avi,asf,mpeg,mpegts,mpegvideo,flv,ogg,rm",
		"-max_pixels", "40000000", "-threads", "1", "-fd", "3", "-i", "fd:", "-map", "0:V:0",
		"-filter_threads", "1", "-vf", "scale=240:240:force_original_aspect_ratio=decrease", "-frames:v", "1", "-fs", "262144", "-f", "image2pipe", "-c:v", "mjpeg", "pipe:1")
	command.ExtraFiles = []*os.File{f}
	data, err := command.Output()
	if err != nil || len(data) == 0 || len(data) > 256<<10 {
		return failure(415, "mediaError")
	}
	latest, err := descriptorStat(f)
	if err != nil {
		return err
	}
	if !matches(latest, key.version) {
		return failure(409, "changed")
	}
	v.cache.put(previewCacheEntry{key: key, image: thumbnail{data: data}, size: int64(len(data))}, generation)
	w.Header().Set("Content-Type", "image/jpeg")
	_, _ = w.Write(data)
	return nil
}
