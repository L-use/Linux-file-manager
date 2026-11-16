package main

import (
	"os"
	"path/filepath"
)

func lifecycleSocketPath(dir *os.File, name string) string {
	return filepath.Join(dir.Name(), name)
}
