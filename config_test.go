package main

import (
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"testing"
)

func TestConfigPathResolvesHomeWithoutHOME(t *testing.T) {
	t.Setenv("HOME", "")
	account, err := user.LookupId(strconv.Itoa(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"~", "~/files"} {
		got, err := configPath(path, t.TempDir())
		want := filepath.Join(account.HomeDir, path[1:])
		if err != nil || got != want {
			t.Errorf("configPath(%q) = %q, %v; want %q", path, got, err, want)
		}
	}
}

func TestRecordBundledFFmpegPreservesConfiguration(t *testing.T) {
	name := filepath.Join(t.TempDir(), "config.json")
	for _, original := range []string{`{"port":9070,"extra":true}`, `{"port":9070,"ffmpeg":"","extra":true}`, `{"port":9070,"ffmpeg":"bundled","extra":true}`, `{"port":9070,"ffmpeg":"/opt/ffmpeg","extra":true}`} {
		if err := os.WriteFile(name, []byte(original), 0600); err != nil {
			t.Fatal(err)
		}
		if err := recordBundledFFmpeg(name); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		var result map[string]json.RawMessage
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		want := `"bundled"`
		if original == `{"port":9070,"ffmpeg":"/opt/ffmpeg","extra":true}` {
			want = `"/opt/ffmpeg"`
		}
		if string(result["ffmpeg"]) != want || string(result["port"]) != "9070" || string(result["extra"]) != "true" {
			t.Fatalf("unexpected migrated configuration: %s", data)
		}
	}
}
