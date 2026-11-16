package main

import (
	"context"
	"errors"
	"io"
	"path"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type searchResult struct {
	Matches []entry `json:"matches"`
	Limited bool    `json:"limited"`
}

func (s *fileStore) search(ctx context.Context, directory, term string) (searchResult, error) {
	res := searchResult{Matches: []entry{}}
	term = strings.ToLower(strings.TrimSpace(term))
	if term == "" || len(term) > 1024 {
		return res, failure(400, "badRequest")
	}
	cutoff := time.Now().Add(3 * time.Second)
	const maxScanned = 50000
	const maxMatches = 150
	queue := []string{directory}
	scanned := 0
	for i := 0; i < len(queue); i++ {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if time.Now().After(cutoff) {
			res.Limited = true
			return res, nil
		}
		dir, err := s.dir(queue[i])
		if err != nil {
			if i == 0 {
				return res, err
			}
			continue
		}
		for {
			if err := ctx.Err(); err != nil {
				dir.Close()
				return res, err
			}
			items, readErr := dir.Readdirnames(128)
			if readErr != nil && !errors.Is(readErr, io.EOF) && i == 0 {
				dir.Close()
				return res, readErr
			}
			for _, name := range items {
				if scanned >= maxScanned || len(res.Matches) >= maxMatches || time.Now().After(cutoff) {
					dir.Close()
					res.Limited = true
					return res, nil
				}
				scanned++
				full := path.Join(queue[i], name)
				if !validName(name) || s.checked(full, false) != nil {
					continue
				}
				info, err := s.metadata(dir, name)
				if err != nil {
					continue
				}
				// Skip directory entries we cannot traverse and never follow symlinks.
				if info.IsDir() && unix.Faccessat(int(dir.Fd()), name, unix.R_OK|unix.X_OK, unix.AT_EACCESS|unix.AT_SYMLINK_NOFOLLOW) == nil {
					queue = append(queue, full)
				}
				if strings.Contains(strings.ToLower(name), term) {
					res.Matches = append(res.Matches, toEntry(full, info))
				}
			}
			if readErr != nil || len(items) == 0 {
				break
			}
		}
		dir.Close()
	}
	return res, nil
}
