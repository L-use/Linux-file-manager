package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestTrashRestoresWithoutOverwritingAndPurgesSeparately(t *testing.T) {
	root := t.TempDir()
	state := t.TempDir()
	store, err := openFiles(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.root.Close()
	name := filepath.Join(root, "folder")
	if err := os.Mkdir(name, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(name, "child.txt"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := store.stat("folder")
	if err != nil {
		t.Fatal(err)
	}
	recycle, err := openTrash(state, store)
	if err != nil {
		t.Fatal(err)
	}
	item, err := recycle.put("folder", info.Version)
	recycle.dir.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(name); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("deleted folder still present: %v", err)
	}

	recycle, err = openTrash(state, store)
	if err != nil {
		t.Fatal(err)
	}
	defer recycle.dir.Close()
	items, err := recycle.list()
	if err != nil || len(items) != 1 || items[0].ID != item.ID {
		t.Fatalf("failed to reopen trash: %#v, %v", items, err)
	}
	if err := os.WriteFile(name, []byte("occupied"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := recycle.restore(item.ID); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("restore did not reject the conflict: %v", err)
	}
	got, err := os.ReadFile(name)
	if err != nil || string(got) != "occupied" {
		t.Fatalf("restore touched an existing target: %q, %v", got, err)
	}
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if err := recycle.restore(item.ID); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(filepath.Join(name, "child.txt"))
	if err != nil || string(got) != "original" {
		t.Fatalf("failed to restore child: %q, %v", got, err)
	}

	info, err = store.stat("folder")
	if err != nil {
		t.Fatal(err)
	}
	item, err = recycle.put("folder", info.Version)
	if err != nil {
		t.Fatal(err)
	}
	if err := recycle.purge(item.ID); err != nil {
		t.Fatal(err)
	}
	if err := recycle.restore(item.ID); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("purged item can still be restored: %v", err)
	}
}

func TestRecursiveSearchHonorsScopeAndProtectedPaths(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	for _, folder := range []string{"scope/nested", "elsewhere", "scope/private"} {
		if err := os.MkdirAll(filepath.Join(root, folder), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"scope/nested/项目-atlas.txt", "elsewhere/atlas.txt", "scope/private/atlas-secret.txt"} {
		if err := os.WriteFile(filepath.Join(root, file), []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "atlas-external.txt"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "scope", "external")); err != nil {
		t.Fatal(err)
	}
	store, err := openFiles(root, []string{filepath.Join(root, "scope", "private")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.root.Close()
	results, err := store.search(context.Background(), "scope", "ATLAS")
	if err != nil || len(results.Matches) != 1 || results.Matches[0].Path != "scope/nested/项目-atlas.txt" || results.Limited {
		t.Fatalf("unexpected matches: %#v, %v", results, err)
	}
}

func TestThumbnailReportsOriginalOrientation(t *testing.T) {
	var raw bytes.Buffer
	if err := jpeg.Encode(&raw, image.NewRGBA(image.Rect(0, 0, 920, 610)), nil); err != nil {
		t.Fatal(err)
	}
	// EXIF orientation 6 rotates the encoded image 90 degrees clockwise.
	exif := []byte{'E', 'x', 'i', 'f', 0, 0, 'I', 'I', 42, 0, 8, 0, 0, 0, 1, 0,
		0x12, 1, 3, 0, 1, 0, 0, 0, 6, 0, 0, 0, 0, 0, 0, 0}
	source := append(append([]byte{0xff, 0xd8, 0xff, 0xe1, 0, byte(len(exif) + 2)}, exif...), raw.Bytes()[2:]...)
	result, err := makeThumbnail(context.Background(), bytes.NewReader(source), false, 800)
	if err != nil {
		t.Fatal(err)
	}
	if result.width != 610 || result.height != 920 {
		t.Fatalf("original orientation: %d × %d", result.width, result.height)
	}
	dim, err := jpeg.DecodeConfig(bytes.NewReader(result.data))
	if err != nil || dim.Width != 530 || dim.Height != 800 {
		t.Fatalf("thumbnail dimensions: %#v, %v", dim, err)
	}
}
