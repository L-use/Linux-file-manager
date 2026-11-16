package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const portableMarker = "filemanager.portable"

func removeUninstallFile(name string) error {
	info, err := os.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("expected a file at %s", name)
	}
	return os.Remove(name)
}

func removeEmptyDirectory(name string) error {
	err := unix.Rmdir(name)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ENOTEMPTY) ||
		errors.Is(err, unix.EEXIST) || errors.Is(err, unix.EBUSY) {
		return nil
	}
	return err
}

func deleteProgramState(state *os.File) error {
	store := &fileStore{}
	info, err := store.metadata(state, "trash")
	if err == nil {
		if info.IsDir() {
			if err := store.checkRecursiveDelete(state, "trash", info); err != nil {
				return err
			}
		}
		if err := store.unlink(state, "trash", info); err != nil {
			return err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	handle, err := openAt(state, ".", unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return err
	}
	names, err := handle.Readdirnames(-1)
	handle.Close()
	if err != nil {
		return err
	}
	for _, name := range names {
		if name != "credentials.json" && name != logFileName && name != controlFileName &&
			!strings.HasPrefix(name, ".credential-") && !strings.HasPrefix(name, ".fm-video-") {
			continue
		}
		if err := unix.Unlinkat(int(state.Fd()), name, 0); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

func removePortableArtifacts(appDir string) error {
	if info, err := os.Lstat(filepath.Join(appDir, portableMarker)); err == nil && info.Mode().IsRegular() {
		licensePaths, err := fs.Glob(assets, "web/vendor/*/LICENSE*")
		if err != nil {
			return err
		}
		licensePaths = append(licensePaths, "vendor/golang.org/x/sys/LICENSE", "vendor/golang.org/x/term/LICENSE")
		store, err := openFiles(appDir, nil)
		if err != nil {
			return err
		}
		defer store.root.Close()
		for _, path := range append(licensePaths, "README.md", "LICENSE", "THIRD_PARTY.md", "config.example.json") {
			dir, leaf, err := store.parent(path, false)
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.ELOOP) {
				continue
			}
			if err != nil {
				return err
			}
			err = unix.Unlinkat(int(dir.Fd()), leaf, 0)
			dir.Close()
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
		// Remove the license directories from deepest to shallowest; keep any
		// directory that contains files added by the administrator.
		for _, license := range licensePaths {
			for dir := parentName(license); dir != ""; dir = parentName(dir) {
				parent, child, err := store.parent(dir, false)
				if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.ELOOP) {
					continue
				}
				if err != nil {
					return err
				}
				err = unix.Unlinkat(int(parent.Fd()), child, unix.AT_REMOVEDIR)
				parent.Close()
				if err != nil && !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, unix.ENOTDIR) &&
					!errors.Is(err, unix.ENOTEMPTY) && !errors.Is(err, unix.EEXIST) && !errors.Is(err, unix.EBUSY) {
					return err
				}
			}
		}
	}
	return nil
}

func uninstallProgram(executable, configName string, state *os.File) error {
	name := filepath.Base(executable)
	if name != "filemanager" && name != "file-manager" {
		return errors.New(cli("Uninstall requires the original executable name.", "卸载时需要使用程序原始文件名。"))
	}
	appDir := filepath.Dir(executable)
	if state != nil {
		if err := deleteProgramState(state); err != nil {
			return fmt.Errorf(cli("remove program state: %w", "清理程序状态失败：%w"), err)
		}
	}
	if err := removePortableArtifacts(appDir); err != nil {
		return err
	}
	if configName != "" {
		if err := removeUninstallFile(configName); err != nil {
			return err
		}
	}
	aliases := []string{filepath.Join(appDir, "filemanager"), filepath.Join(appDir, "file-manager")}
	if raw, err := os.ReadFile(filepath.Join(appDir, registeredCommandFile)); err == nil {
		registered := string(raw)
		if filepath.Base(registered) == "filemanager" {
			aliases = append(aliases, registered)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if external, err := exec.LookPath("filemanager"); err == nil {
		aliases = append(aliases, external)
	}
	for _, alias := range aliases {
		if err := removeCommandAlias(alias, executable); err != nil {
			return err
		}
	}
	for _, name := range []string{registeredCommandFile, languageFile, portableMarker, systemdMarker} {
		if err := removeUninstallFile(filepath.Join(appDir, name)); err != nil {
			return err
		}
	}
	if err := os.Remove(executable); err != nil {
		return err
	}
	if state != nil {
		if err := unix.Unlinkat(int(state.Fd()), lockFileName, 0); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := removeEmptyDirectory(state.Name()); err != nil {
			return err
		}
	}
	if err := removeEmptyDirectory(appDir); err != nil {
		return err
	}
	fmt.Println(cli("Uninstallation complete.", "卸载完成。"))
	return nil
}

// Only remove aliases which still point directly to this executable.
func removeCommandAlias(alias, executable string) error {
	if !filepath.IsAbs(alias) || alias == executable {
		return nil
	}
	info, err := os.Lstat(alias)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		return nil
	}
	target, err := os.Readlink(alias)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(alias), target)
	}
	if filepath.Clean(target) != executable {
		return nil
	}
	return os.Remove(alias)
}
