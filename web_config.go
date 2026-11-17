package main

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type webConfig struct {
	mu         sync.Mutex
	path       string
	activeRoot string
	overridden bool
	protected  []string
}

type rootSetting struct {
	Root     string `json:"root"`
	Active   string `json:"active"`
	Writable bool   `json:"writable"`
}

func (c *webConfig) read() (config, error) {
	specified := c.path
	if filepath.Base(c.path) == "config.json" {
		specified = ""
	}
	settings, _, err := configFile(filepath.Dir(c.path), specified)
	return settings, err
}

func (c *webConfig) get() (rootSetting, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.setting()
}

func (c *webConfig) setting() (rootSetting, error) {
	cfg, err := c.read()
	return rootSetting{Root: cfg.Root, Active: c.activeRoot, Writable: !c.overridden}, err
}

func (c *webConfig) save(value string) (rootSetting, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.overridden {
		return rootSetting{}, failure(409, "rootOverride")
	}
	if value == "" || (!strings.HasPrefix(value, "~") && !filepath.IsAbs(value)) || strings.ContainsAny(value, "\x00\r\n") {
		return rootSetting{}, failure(400, "rootInvalid")
	}
	root, err := configPath(value, filepath.Dir(c.path))
	if err != nil {
		return rootSetting{}, failure(400, "rootInvalid")
	}
	root, err = canonical(root)
	if err != nil {
		return rootSetting{}, err
	}
	for _, name := range c.protected {
		if inside(name, root) {
			return rootSetting{}, failure(403, "protected")
		}
	}
	probe, err := os.Open(root)
	if err != nil {
		return rootSetting{}, err
	}
	info, err := probe.Stat()
	if err != nil || !info.IsDir() {
		probe.Close()
		if err != nil {
			return rootSetting{}, err
		}
		return rootSetting{}, failure(400, "rootInvalid")
	}
	_, err = probe.ReadDir(1)
	probe.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return rootSetting{}, err
	}

	data, err := os.ReadFile(c.path)
	settings := make(map[string]json.RawMessage)
	mode := fs.FileMode(0600)
	if err == nil {
		info, err := os.Stat(c.path)
		if err != nil {
			return rootSetting{}, err
		}
		mode = info.Mode().Perm()
		if err := json.Unmarshal(data, &settings); err != nil {
			return rootSetting{}, err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return rootSetting{}, err
	}
	if settings == nil {
		settings = make(map[string]json.RawMessage)
	}
	settings["root"], _ = json.Marshal(value)
	output, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return rootSetting{}, err
	}
	if err := writePrivateSetting(c.path, append(output, '\n'), mode); err != nil {
		return rootSetting{}, err
	}
	return c.setting()
}

func writePrivateSetting(name string, data []byte, mode fs.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(name), uploadPrefix+"setting-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), name)
}
