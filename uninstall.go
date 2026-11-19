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

func reportUninstallRemoval(name string) {
	fmt.Printf(cli("  Removed %q\n", "  已删除 %q\n"), name)
}

func reportUninstallResult(name string, err error) error {
	if err == nil {
		reportUninstallRemoval(name)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func removeUninstallTree(parent *os.File, name string) error {
	store := &fileStore{}
	info, err := store.metadata(parent, name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := store.checkRecursiveDelete(parent, name, info); err != nil {
			return err
		}
	}
	return store.unlinkReported(parent, name, info, filepath.Join(parent.Name(), name), reportUninstallRemoval)
}

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
	return reportUninstallResult(name, os.Remove(name))
}

func removeEmptyDirectory(name string) error {
	err := unix.Rmdir(name)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ENOTEMPTY) ||
		errors.Is(err, unix.EEXIST) {
		return nil
	}
	return reportUninstallResult(name, err)
}

func deleteProgramState(state *os.File) error {
	if err := removeUninstallTree(state, "trash"); err != nil {
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
			name != previewCacheLimitFile && name != updateModeFile && name != installationSeenFile && !uninstallTemporary(name, ".update-archive-", uploadPrefix+"setting-", ".credential-", ".fm-video-", ".preview-cache-limit-", ffmpegTempPrefix) {
			continue
		}
		if err := reportUninstallResult(filepath.Join(state.Name(), name), unix.Unlinkat(int(state.Fd()), name, 0)); err != nil {
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
		licensePaths = append(licensePaths, strings.Fields(goLicensePaths)...)
		store, err := openFiles(appDir, nil)
		if err != nil {
			return err
		}
		defer store.root.Close()
		for _, path := range append(licensePaths, "README.md", "LICENSE", "THIRD_PARTY.md", "FFMPEG-NOTICE.md", "FFMPEG-COPYING.GPLv2", "config.example.json") {
			dir, leaf, err := store.parent(path, false)
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.ELOOP) {
				continue
			}
			if err != nil {
				return err
			}
			err = unix.Unlinkat(int(dir.Fd()), leaf, 0)
			dir.Close()
			if err := reportUninstallResult(filepath.Join(appDir, path), err); err != nil {
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
				if err == nil {
					reportUninstallRemoval(filepath.Join(appDir, dir))
				} else if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, unix.ENOTDIR) &&
					!errors.Is(err, unix.ENOTEMPTY) && !errors.Is(err, unix.EEXIST) && !errors.Is(err, unix.EBUSY) {
					return err
				}
			}
		}
	}
	return nil
}

func uninstallTemporary(name string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func uninstallProgram(executable, configName string, state *os.File) error {
	name := filepath.Base(executable)
	if name != "filemanager" && name != "file-manager" {
		return errors.New(cli("Uninstall requires the original executable name.", "卸载时需要使用程序原始文件名。"))
	}
	appDir := filepath.Dir(executable)
	entries, err := os.ReadDir(appDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() && uninstallTemporary(entry.Name(), ".filemanager-language-", ".filemanager.", ".config.", ".install-setting.", uploadPrefix+"update-", uploadPrefix+"setting-") {
			if err := removeUninstallFile(filepath.Join(appDir, entry.Name())); err != nil {
				return err
			}
		}
	}
	if state != nil {
		if err := deleteProgramState(state); err != nil {
			return fmt.Errorf(cli("remove program state: %w", "清理程序状态失败：%w"), err)
		}
	}
	if err := removePortableArtifacts(appDir); err != nil {
		return err
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
	for _, name := range []string{"filemanager.launchd.log", registeredCommandFile, languageFile, portableMarker, systemdMarker, installationMarker} {
		if err := removeUninstallFile(filepath.Join(appDir, name)); err != nil {
			return err
		}
	}
	if state != nil {
		if err := reportUninstallResult(filepath.Join(state.Name(), lockFileName), unix.Unlinkat(int(state.Fd()), lockFileName, 0)); err != nil {
			return err
		}
		if err := removeEmptyDirectory(state.Name()); err != nil {
			return err
		}
		if _, err := os.Lstat(state.Name()); err == nil {
			fmt.Printf(cli("  Kept nonempty directory: %q\n", "  保留非空目录 %q\n"), state.Name())
		} else if errors.Is(err, fs.ErrNotExist) {
			for dir := filepath.Dir(state.Name()); dir != appDir && inside(appDir, dir); dir = filepath.Dir(dir) {
				if err := removeEmptyDirectory(dir); err != nil {
					return err
				}
			}
		}
	}
	if configName != "" {
		if err := removeUninstallFile(configName); err != nil {
			return err
		}
	}
	if err := removeUninstallFile(executable); err != nil {
		return err
	}
	if err := removeEmptyDirectory(appDir); err != nil {
		return err
	}
	if _, err := os.Lstat(appDir); err == nil {
		fmt.Printf(cli("  Kept directory containing other files: %q\n", "  保留包含其他文件的目录 %q\n"), appDir)
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
	return reportUninstallResult(alias, os.Remove(alias))
}
