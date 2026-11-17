package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

type config struct {
	Root   string `json:"root"`
	Host   string `json:"host"`
	Port   int    `json:"port"`
	Listen string `json:"listen"`
	State  string `json:"state_dir"`
	Secure bool   `json:"secure_cookie"`
	FFmpeg string `json:"ffmpeg"`
}

func configFile(appDir, specified string) (config, string, error) {
	c := config{Root: "~", Host: "127.0.0.1", Port: 8080, State: filepath.Join(appDir, "state")}
	name := specified
	if name == "" {
		name = filepath.Join(appDir, "config.json")
	}
	data, err := os.ReadFile(name)
	if specified == "" && errors.Is(err, fs.ErrNotExist) {
		return c, "", nil
	}
	if err != nil {
		return c, "", fmt.Errorf("read configuration %q: %w", name, err)
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, "", fmt.Errorf("invalid configuration %q: %w", name, err)
	}
	return c, name, nil
}

func configPath(value, base string) (string, error) {
	if value == "" {
		return "", nil
	}
	if strings.HasPrefix(value, "~") {
		account, subpath, _ := strings.Cut(value[1:], "/")
		var home string
		if account == "" {
			home = os.Getenv("HOME")
			if home == "" {
				record, err := user.LookupId(strconv.Itoa(os.Geteuid()))
				if err != nil {
					return "", fmt.Errorf("look up home directory for effective user: %w", err)
				}
				home = record.HomeDir
			}
		} else {
			record, err := user.Lookup(account)
			if err != nil {
				return "", err
			}
			home = record.HomeDir
		}
		return filepath.Join(home, subpath), nil
	}
	if !filepath.IsAbs(value) && base != "" {
		value = filepath.Join(base, value)
	}
	return filepath.Abs(value)
}

func (c config) address() (string, error) {
	if c.Listen != "" {
		return c.Listen, nil
	}
	if c.Port < 1 || c.Port > 65535 {
		return "", errors.New("config.port must be an integer from 1 to 65535")
	}
	return net.JoinHostPort(strings.TrimSuffix(strings.TrimPrefix(c.Host, "["), "]"), strconv.Itoa(c.Port)), nil
}
