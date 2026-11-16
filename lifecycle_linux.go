package main

import (
	"fmt"
	"os"
)

func lifecycleSocketPath(dir *os.File, name string) string {
	return fmt.Sprintf("/proc/self/fd/%d/%s", dir.Fd(), name)
}
