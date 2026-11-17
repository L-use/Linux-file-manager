package main

import (
	"os"
	"testing"
)

func TestPreviewCacheLimitAndClear(t *testing.T) {
	state := t.TempDir()
	cache := newPreviewCache(state)
	defer cache.clear()
	_, generation := cache.budget()
	imageKey := thumbnailKey{name: "image.jpg"}
	videoKey := videoKey{path: "video.mp4"}
	video, err := os.CreateTemp(state, "video-")
	if err != nil {
		t.Fatal(err)
	}
	defer video.Close()
	if !cache.put(previewCacheEntry{key: videoKey, video: video, size: 48 * cacheMiB}, generation) ||
		!cache.put(previewCacheEntry{key: imageKey, image: thumbnail{data: []byte{1}}, size: 48 * cacheMiB}, generation) {
		t.Fatal("initial cache rejected entries")
	}
	if err := cache.setLimit(64); err != nil {
		t.Fatal(err)
	}
	stats := cache.stats()
	if stats.Thumbnails.Count != 1 || stats.Videos.Count != 0 || stats.Thumbnails.Bytes != 48*cacheMiB {
		t.Fatalf("unexpected stats after eviction: %+v", stats)
	}
	if newPreviewCache(state).stats().LimitMiB != 64 {
		t.Fatal("saved limit did not survive cache reconstruction")
	}
	cache.clear()
	if cache.put(previewCacheEntry{key: imageKey, size: 1}, generation) || cache.stats().Thumbnails.Count != 0 {
		t.Fatal("a conversion started before clear repopulated the cache")
	}
}
