package main

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUploadPublishesOnlyCompleteFiles(t *testing.T) {
	root := t.TempDir()
	store, err := openFiles(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.root.Close()

	const name = "data.txt"
	const original = "original bytes"
	if err := store.upload(name, "", strings.NewReader(original), int64(len(original))); err != nil {
		t.Fatal(err)
	}
	entry, err := store.stat(name)
	if err != nil {
		t.Fatal(err)
	}
	var conflict *problem
	err = store.upload(name, "", strings.NewReader("replacement"), -1)
	if !errors.As(err, &conflict) || conflict.code != "conflict" || conflict.version != entry.Version {
		t.Fatalf("expected current version on conflict, got %v", err)
	}
	body := io.MultiReader(strings.NewReader("partial"), interruptedReader{})
	if err := store.upload(name, entry.Version, body, 4096); err == nil {
		t.Fatal("interrupted overwrite succeeded")
	}
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil || string(data) != original {
		t.Fatalf("interrupted overwrite altered file: %q, %v", data, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != name {
		t.Fatalf("temporary upload remained: %v, %v", entries, err)
	}
}

type interruptedReader struct{}

func (interruptedReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestFileAccessRejectsAliasesAndStaleMutations(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	private := filepath.Join(root, "private")
	if err := os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(private, "credentials.json")
	if err := os.WriteFile(secret, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(secret, filepath.Join(root, "alias.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	store, err := openFiles(root, []string{private})
	if err != nil {
		t.Fatal(err)
	}
	defer store.root.Close()

	items, err := store.list("")
	if err != nil || len(items) != 1 || items[0].Kind != "symlink" {
		t.Fatalf("private paths or aliases appeared in listing: %v, %v", items, err)
	}
	if _, err := store.dir("outside"); err == nil {
		t.Fatal("followed a symlink")
	}
	if err := store.mkdir("outside/new", false); err == nil {
		t.Fatal("wrote through a symlink")
	}
	if _, _, err := store.regular("alias.txt"); err == nil {
		t.Fatal("opened an alias to a protected file")
	}
	if err := store.move("outside", "new-name", "outdated", ""); err == nil {
		t.Fatal("moved a file using a stale version")
	}
	if _, err := os.Stat(filepath.Join(outside, "new")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("unexpected outside directory state: %v", err)
	}
}

func TestSaveTextRequiresLatestVersionAndPreservesMode(t *testing.T) {
	root := t.TempDir()
	name := filepath.Join(root, "test.sh")
	if err := os.WriteFile(name, []byte(utf8BOM+"original\r\n"), 0750); err != nil {
		t.Fatal(err)
	}
	store, err := openFiles(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.root.Close()

	f, info, err := store.regular("test.sh")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := readText(f, info)
	f.Close()
	if err != nil || snapshot.Text != "original\r\n" || !snapshot.BOM {
		t.Fatalf("unexpected text preview: %#v, %v", snapshot, err)
	}
	replacement := []byte(utf8BOM + "edited\r\n")
	if err := store.saveText("test.sh", snapshot.Version, replacement); err != nil {
		t.Fatal(err)
	}
	if err := store.saveText("test.sh", snapshot.Version, []byte("stale")); err == nil {
		t.Fatal("saved over a newer revision")
	}
	data, err := os.ReadFile(name)
	if err != nil || string(data) != string(replacement) {
		t.Fatalf("incorrect edited file: %q, %v", data, err)
	}
	result, err := os.Stat(name)
	if err != nil || result.Mode().Perm() != 0750 {
		t.Fatalf("editor changed permissions: %v, %v", result, err)
	}
}

func TestMkdirCanReuseExistingDirectoryOnly(t *testing.T) {
	root := t.TempDir()
	store, err := openFiles(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.root.Close()

	if err := store.mkdir("folder", false); err != nil {
		t.Fatal(err)
	}
	if err := store.mkdir("folder", false); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("manual mkdir accepted a duplicate: %v", err)
	}
	if err := store.mkdir("folder", true); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("folder", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := store.mkdir("link", true); err == nil {
		t.Fatal("folder upload accepted a symlink as a destination")
	}
}

func TestChmodTargetsCurrentFileOrFolder(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "note.txt")
	folder := filepath.Join(root, "folder")
	child := filepath.Join(folder, "child.txt")
	if err := os.Mkdir(folder, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(folder, 0750|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{file, child} {
		if err := os.WriteFile(name, []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(file, filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	store, err := openFiles(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.root.Close()

	original, err := store.stat("note.txt")
	if err != nil || original.Permissions != "600" {
		t.Fatalf("initial mode: %v, %v", original, err)
	}
	if err := store.chmod("note.txt", original.Version, "000"); err != nil {
		t.Fatal(err)
	}
	if err := store.chmod("note.txt", original.Version, "777"); err == nil {
		t.Fatal("allowed stale version")
	}
	locked, err := store.stat("note.txt")
	if err != nil || locked.Permissions != "000" {
		t.Fatalf("locked mode: %v, %v", locked, err)
	}
	if err := store.chmod("note.txt", locked.Version, "640"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "original" {
		t.Fatalf("chmod altered file contents: %q, %v", data, err)
	}

	dir, err := store.stat("folder")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.chmod("folder", dir.Version, "0755"); err != nil {
		t.Fatal(err)
	}
	dirInfo, err := os.Stat(folder)
	if err != nil || dirInfo.Mode()&(os.ModeSticky|os.ModePerm) != os.ModeSticky|0755 {
		t.Fatalf("folder mode: %v, %v", dirInfo, err)
	}
	childInfo, err := os.Stat(child)
	if err != nil || childInfo.Mode().Perm() != 0600 {
		t.Fatalf("chmod altered child permissions: %v, %v", childInfo, err)
	}

	alias, err := store.stat("alias")
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range [][3]string{{"alias", alias.Version, "777"}, {"../outside", original.Version, "777"}, {"", dir.Version, "755"}, {"note.txt", locked.Version, "889"}} {
		if err := store.chmod(request[0], request[1], request[2]); err == nil {
			t.Fatalf("allowed invalid request: %v", request)
		}
	}
}
