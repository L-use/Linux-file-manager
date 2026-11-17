package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

const previewCacheLimitFile = "preview-cache-limit"

const cacheMiB = int64(1 << 20)
const defaultCacheMiB = 1024
const minCacheMiB = 64
const maxCacheMiB = 4096

type cacheUsage struct {
	Bytes int64 `json:"bytes"`
	Count int   `json:"count"`
}

type previewCacheStats struct {
	LimitMiB   int        `json:"limitMiB"`
	Thumbnails cacheUsage `json:"thumbnails"`
	Videos     cacheUsage `json:"videos"`
}

type previewCacheEntry struct {
	key   any
	image thumbnail
	video *os.File
	size  int64
}

type previewCache struct {
	mu         sync.Mutex
	settings   string
	limitMiB   int
	bytes      int64
	generation uint64
	entries    []previewCacheEntry
}

func validCacheLimit(limit int) bool {
	return limit >= minCacheMiB && limit <= maxCacheMiB && limit%64 == 0
}

func newPreviewCache(state string) *previewCache {
	c := &previewCache{settings: filepath.Join(state, previewCacheLimitFile), limitMiB: defaultCacheMiB}
	if data, err := os.ReadFile(c.settings); err == nil {
		if limit, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && validCacheLimit(limit) {
			c.limitMiB = limit
		}
	}
	return c
}

func (c *previewCache) stats() previewCacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	stats := previewCacheStats{LimitMiB: c.limitMiB}
	for _, entry := range c.entries {
		usage := &stats.Thumbnails
		if entry.video != nil {
			usage = &stats.Videos
		}
		usage.Count++
		usage.Bytes += entry.size
	}
	return stats
}

func (c *previewCache) removeOldest() {
	entry := c.entries[0]
	if entry.video != nil {
		entry.video.Close()
	}
	c.bytes -= entry.size
	c.entries[0] = previewCacheEntry{}
	c.entries = c.entries[1:]
}

func (c *previewCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	for len(c.entries) > 0 {
		c.removeOldest()
	}
	c.entries = nil
}

func (c *previewCache) setLimit(limit int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	file, err := os.CreateTemp(filepath.Dir(c.settings), ".preview-cache-limit-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.WriteString(strconv.Itoa(limit) + "\n")
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(file.Name(), c.settings); err != nil {
		return err
	}
	c.limitMiB = limit
	for c.bytes > int64(limit)*cacheMiB {
		c.removeOldest()
	}
	return nil
}

func (c *previewCache) budget() (int64, uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return int64(c.limitMiB) * cacheMiB, c.generation
}

func (c *previewCache) put(entry previewCacheEntry, generation uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	limit := int64(c.limitMiB) * cacheMiB
	if generation != c.generation || entry.size > limit {
		return false
	}
	for c.bytes+entry.size > limit || len(c.entries) >= 512 {
		c.removeOldest()
	}
	c.entries = append(c.entries, entry)
	c.bytes += entry.size
	return true
}

// find is called with the mutex held. Recently used entries are retained first.
func (c *previewCache) find(key any) previewCacheEntry {
	for i, entry := range c.entries {
		if entry.key == key {
			copy(c.entries[i:], c.entries[i+1:])
			c.entries[len(c.entries)-1] = entry
			return entry
		}
	}
	return previewCacheEntry{}
}

func (c *previewCache) image(key thumbnailKey) thumbnail {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.find(key).image
}

func (c *previewCache) hasVideo(key videoKey) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.find(key).video != nil
}

func (c *previewCache) openVideo(key videoKey) (*os.File, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.find(key)
	if entry.video == nil {
		return nil, failure(404, "videoNotReady")
	}
	fd, err := unix.FcntlInt(entry.video.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), "compatible.mp4"), nil
}
