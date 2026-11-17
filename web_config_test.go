package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWebDirectorySettingChangesOnlyRoot(t *testing.T) {
	dir, err := canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.json")
	oldRoot, newRoot := filepath.Join(dir, "old"), filepath.Join(dir, "new")
	for _, root := range []string{oldRoot, newRoot} {
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
	}
	start := map[string]any{"root": oldRoot, "port": 9085, "state_dir": "./state", "extra": "keep"}
	contents, err := json.Marshal(start)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, contents, 0600); err != nil {
		t.Fatal(err)
	}
	settings := &webConfig{path: configPath, activeRoot: oldRoot, protected: []string{oldRoot}}
	if _, err := settings.save(oldRoot); err == nil {
		t.Fatal("accepted protected directory")
	}
	value, err := settings.save(newRoot)
	if err != nil || value.Root != newRoot || value.Active != oldRoot {
		t.Fatalf("save returned %+v, %v", value, err)
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result["port"] != float64(9085) || result["state_dir"] != "./state" || result["extra"] != "keep" {
		t.Fatalf("other configuration changed: %#v", result)
	}
}
