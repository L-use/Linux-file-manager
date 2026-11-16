package main

import (
	"archive/zip"
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestZIPExtractPublishesCompleteSafeTree(t *testing.T) {
	root := t.TempDir()
	store, err := openFiles(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.root.Close()
	writeZip := func(name, member string, mode fs.FileMode) string {
		t.Helper()
		var contents bytes.Buffer
		w := zip.NewWriter(&contents)
		header := &zip.FileHeader{Name: member, Method: zip.Deflate}
		header.SetMode(mode)
		part, err := w.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte("release")); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), contents.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		entry, err := store.stat(name)
		if err != nil {
			t.Fatal(err)
		}
		return entry.Version
	}
	version := writeZip("good.zip", "nested/program", 0755)
	if err := store.extractZip(context.Background(), "good.zip", "bundle", version); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "bundle", "nested", "program")
	body, err := os.ReadFile(file)
	if err != nil || string(body) != "release" {
		t.Fatalf("extracted file = %q, %v", body, err)
	}
	if info, err := os.Stat(file); err != nil || info.Mode().Perm()&0100 == 0 {
		t.Fatalf("execute bit missing: %v, %v", info, err)
	}
	if err := store.extractZip(context.Background(), "good.zip", "bundle", version); err == nil {
		t.Fatal("replaced existing destination")
	}
	for _, member := range []string{"../outside", "/absolute", "nested/../escape", "folder\\escape"} {
		version := writeZip("bad.zip", member, 0644)
		if err := store.extractZip(context.Background(), "bad.zip", "rejected", version); err == nil {
			t.Fatalf("%q accepted", member)
		}
	}
	version = writeZip("link.zip", "shortcut", fs.ModeSymlink|0777)
	if err := store.extractZip(context.Background(), "link.zip", "rejected", version); err == nil {
		t.Fatal("symlink accepted")
	}
	items, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Name() == "rejected" || strings.HasPrefix(item.Name(), uploadPrefix) {
			t.Fatalf("left incomplete output %q", item.Name())
		}
	}
}
