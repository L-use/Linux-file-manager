package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFolderArchivePreservesEmptyUnicodeAndOverlap(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(root, "资料", "空目录"), 0755)
	os.WriteFile(filepath.Join(root, "资料", "说明.txt"), []byte("内容"), 0644)
	store, err := openFiles(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.root.Close()
	archive, name, err := store.folderArchive(context.Background(), state, []string{"资料", "资料/说明.txt", "资料"})
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	if name != "资料.zip" {
		t.Fatal(name)
	}
	stat, _ := archive.Stat()
	reader, err := zip.NewReader(archive, stat.Size())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, file := range reader.File {
		r, e := file.Open()
		if e != nil {
			t.Fatal(e)
		}
		body, _ := io.ReadAll(r)
		r.Close()
		got[file.Name] = string(body)
	}
	if len(got) != 3 || got["资料/说明.txt"] != "内容" {
		t.Fatal(got)
	}
	if _, ok := got["资料/空目录/"]; !ok {
		t.Fatal("empty folder missing")
	}
	files, _ := os.ReadDir(state)
	if len(files) != 0 {
		t.Fatal("temporary ZIP has a persistent path")
	}
}
func TestFolderArchiveRejectsEscapeSpecialAndCancellation(t *testing.T) {
	root, state, outside := t.TempDir(), t.TempDir(), t.TempDir()
	os.Symlink(outside, filepath.Join(root, "link"))
	store, _ := openFiles(root, nil)
	defer store.root.Close()
	for _, name := range []string{"../escape", "link"} {
		file, _, err := store.folderArchive(context.Background(), state, []string{name})
		if err == nil {
			file.Close()
			t.Fatalf("accepted %s", name)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if file, _, err := store.folderArchive(ctx, state, []string{""}); err == nil {
		file.Close()
		t.Fatal("ignored cancellation")
	}
}
func TestFavoritesPersistAndAreSeparatedByRoot(t *testing.T) {
	state, root := t.TempDir(), t.TempDir()
	os.Mkdir(filepath.Join(root, "saved"), 0755)
	store, _ := openFiles(root, nil)
	defer store.root.Close()
	setup := func(s *fileStore) map[string]func(http.ResponseWriter, *http.Request) error {
		handlers := map[string]func(http.ResponseWriter, *http.Request) error{}
		registerFavorites(func(pattern string, private bool, fn func(http.ResponseWriter, *http.Request) error) {
			if !private {
				t.Fatal("public favorites")
			}
			handlers[pattern] = fn
		}, s, state)
		return handlers
	}
	call := func(h map[string]func(http.ResponseWriter, *http.Request) error, method, body string) []string {
		r := httptest.NewRequest(method, "/api/favorites", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		if err := h[method+" /api/favorites"](w, r); err != nil {
			t.Fatal(err)
		}
		var result []string
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	h := setup(store)
	if got := call(h, "POST", `{"path":"saved"}`); len(got) != 1 {
		t.Fatal(got)
	}
	if got := call(setup(store), "GET", ""); len(got) != 1 || got[0] != "saved" {
		t.Fatal("not persisted", got)
	}
	other, _ := openFiles(t.TempDir(), nil)
	defer other.root.Close()
	if got := call(setup(other), "GET", ""); len(got) != 0 {
		t.Fatal("cross-root favorites", got)
	}
	os.Remove(filepath.Join(root, "saved"))
	if got := call(h, "POST", `{"path":"saved","remove":true}`); len(got) != 0 {
		t.Fatal("cannot remove missing favorite", got)
	}
}
func TestGlobalSearchFindsUnloadedNestedFile(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "one", "two"), 0755)
	os.WriteFile(filepath.Join(root, "one", "two", "find-me.txt"), []byte("ok"), 0644)
	store, _ := openFiles(root, nil)
	defer store.root.Close()
	result, err := store.search(context.Background(), "", "find-me")
	if err != nil || len(result.Matches) != 1 || result.Matches[0].Path != "one/two/find-me.txt" {
		t.Fatalf("%+v %v", result, err)
	}
}
