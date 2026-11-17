package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const systemdMarker = "filemanager.systemd"
const systemdUnit = "filemanager.service"
const systemdUnitPath = "/etc/systemd/system/" + systemdUnit
const systemdUnitHeader = "# Managed by Linux File Manager"

func systemdInstalled(appDir, executable string) (bool, error) {
	info, err := os.Lstat(filepath.Join(appDir, systemdMarker))
	hasMarker := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if hasMarker && !info.Mode().IsRegular() {
		return false, errors.New("invalid filemanager.systemd marker")
	}
	unitInfo, err := os.Lstat(systemdUnitPath)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !unitInfo.Mode().IsRegular() {
		if !hasMarker {
			return false, nil
		}
		return false, errors.New("filemanager.service is not a regular unit file")
	}
	unit, err := os.ReadFile(systemdUnitPath)
	if err != nil {
		return false, err
	}
	escaped := strings.NewReplacer("\\", "\\\\", `"`, `\"`, "%", "%%").Replace(executable)
	expectedExec := `ExecStart="` + escaped + `" -foreground`
	content := "\n" + string(unit) + "\n"
	if !strings.HasPrefix(string(unit), systemdUnitHeader+"\n") ||
		!strings.Contains(content, "\n"+expectedExec+"\n") {
		if !hasMarker {
			return false, nil
		}
		return false, errors.New(cli("filemanager.service belongs to a different installation", "filemanager.service 属于另一套安装"))
	}
	return true, nil
}

func systemctl(args ...string) error {
	command := exec.Command("systemctl", args...)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	return command.Run()
}

func startSystemdService(address string) error {
	if err := systemctl("start", systemdUnit); err != nil {
		return fmt.Errorf(cli("service start failed; check journalctl -u filemanager.service: %w",
			"服务启动失败，请用 journalctl -u filemanager.service 查看日志：%w"), err)
	}
	if err := systemctl("enable", systemdUnit); err != nil {
		return fmt.Errorf(cli("service started, but enabling startup at boot failed: %w",
			"服务已启动，但设置开机启动失败：%w"), err)
	}
	fmt.Printf(cli("filemanager started at http://%s\nLogs: journalctl -u filemanager.service\n",
		"filemanager 已启动，监听 http://%s\n日志：journalctl -u filemanager.service\n"), address)
	return nil
}

func ownedSystemdLinks(directories []string) ([]string, error) {
	var links []string
	for _, directory := range directories {
		err := filepath.WalkDir(directory, func(name string, entry fs.DirEntry, err error) error {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if entry.Type()&fs.ModeSymlink == 0 {
				return nil
			}
			target, err := os.Readlink(name)
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(name), target)
			}
			if filepath.Clean(target) == systemdUnitPath {
				links = append(links, name)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return links, nil
}

func unregisterSystemdService() error {
	directories := []string{filepath.Dir(systemdUnitPath), "/run/systemd/system"}
	links, err := ownedSystemdLinks(directories)
	if err != nil {
		return err
	}
	if err := systemctl("disable", "--now", systemdUnit); err != nil {
		return fmt.Errorf(cli("disable systemd service: %w", "停止并禁用 systemd 服务失败：%w"), err)
	}
	for _, link := range links {
		if _, err := os.Lstat(link); errors.Is(err, fs.ErrNotExist) {
			reportUninstallRemoval(link)
		}
	}
	// Some custom links may survive systemctl disable. Recheck ownership before removing them.
	links, err = ownedSystemdLinks(directories)
	if err != nil {
		return err
	}
	for _, link := range links {
		if err := removeUninstallFile(link); err != nil {
			return err
		}
	}
	for _, directory := range directories {
		if err := removeSystemdOverrides(directory); err != nil {
			return err
		}
	}
	if err := removeUninstallFile(systemdUnitPath); err != nil {
		return err
	}
	return systemctl("daemon-reload")
}

func removeSystemdOverrides(directory string) error {
	parent, err := os.Open(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer parent.Close()
	return removeUninstallTree(parent, systemdUnit+".d")
}

func notifySystemdReady() error {
	address := os.Getenv("NOTIFY_SOCKET")
	if address == "" {
		return nil
	}
	_ = os.Unsetenv("NOTIFY_SOCKET")
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: address, Net: "unixgram"})
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
	_, err = conn.Write([]byte("READY=1"))
	return err
}
