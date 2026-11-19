package main

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const ffmpegTempPrefix = ".fm-ffmpeg-"

func prepareFFmpeg(configured string, state *os.File) (string, func(), error) {
	if configured == "bundled" {
		configured = ""
	}
	if configured != "" || len(bundledFFmpeg) == 0 {
		path, err := videoExecutable(configured)
		return path, func() {}, err
	}

	info, err := descriptorStat(state)
	if err != nil {
		return "", nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Sys().(unix.Stat_t).Uid != uint32(os.Geteuid()) {
		return "", nil, errors.New("bundled FFmpeg requires a private state directory owned by the service user")
	}
	// Resolve through the held directory descriptor, even if a parent path is renamed.
	directory := lifecycleSocketPath(state, "")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", nil, err
	}
	// The caller already holds the service lock. Remove files left by unclean exits.
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ffmpegTempPrefix) && entry.Type().IsRegular() {
			name := filepath.Join(directory, entry.Name())
			if err := os.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return "", nil, err
			}
		}
	}

	input, err := gzip.NewReader(bytes.NewReader(bundledFFmpeg))
	if err != nil {
		return "", nil, fmt.Errorf("open bundled FFmpeg: %w", err)
	}
	defer input.Close()

	file, err := os.CreateTemp(directory, ffmpegTempPrefix)
	if err != nil {
		return "", nil, fmt.Errorf("prepare bundled FFmpeg: %w", err)
	}
	defer file.Close()
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	keep := false
	defer func() {
		if !keep {
			cleanup()
		}
	}()
	if _, err := io.Copy(file, input); err != nil {
		return "", nil, fmt.Errorf("extract bundled FFmpeg: %w", err)
	}
	if err := file.Chmod(0700); err != nil {
		return "", nil, err
	}
	if err := file.Close(); err != nil {
		return "", nil, err
	}
	keep = true
	return path, cleanup, nil
}
