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
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, errors.New("invalid filemanager.systemd marker")
	}
	unit, err := os.ReadFile(systemdUnitPath)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	escaped := strings.NewReplacer("\\", "\\\\", `"`, `\"`, "%", "%%").Replace(executable)
	expectedExec := `ExecStart="` + escaped + `" -foreground`
	content := "\n" + string(unit) + "\n"
	if !strings.HasPrefix(string(unit), systemdUnitHeader+"\n") ||
		!strings.Contains(content, "\n"+expectedExec+"\n") {
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

func unregisterSystemdService() error {
	if err := systemctl("disable", "--now", systemdUnit); err != nil {
		return fmt.Errorf(cli("disable systemd service: %w", "停止并禁用 systemd 服务失败：%w"), err)
	}
	if err := os.Remove(systemdUnitPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return systemctl("daemon-reload")
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
