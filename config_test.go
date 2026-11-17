package main

import (
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
