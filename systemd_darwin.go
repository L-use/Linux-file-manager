package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const systemdMarker = "filemanager.launchd"
const systemdUnit = "io.filemanager.server"

var systemdUnitPath = launchdPath()

func launchdPath() string {
	if os.Geteuid() == 0 {
		return "/Library/LaunchDaemons/" + systemdUnit + ".plist"
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library/LaunchAgents", systemdUnit+".plist")
}
func launchdDomain() string {
	if os.Geteuid() == 0 {
		return "system"
	}
	return "gui/" + strconv.Itoa(os.Getuid())
}
func launchctl(args ...string) error {
	c := exec.Command("/bin/launchctl", args...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
func systemdInstalled(appDir, executable string) (bool, error) {
	markerPath := filepath.Join(appDir, systemdMarker)
	info, err := os.Lstat(markerPath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, errors.New("invalid launchd marker")
	}
	info, err = os.Lstat(systemdUnitPath)
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, errors.New("invalid launchd service file")
	}
	marker, err := os.ReadFile(markerPath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(string(marker)) != systemdUnit {
		return false, errors.New("invalid launchd marker")
	}
	data, err := os.ReadFile(systemdUnitPath)
	if err != nil {
		return false, err
	}
	if !strings.Contains(string(data), "<!-- Managed by File Manager -->") || !strings.Contains(string(data), "<string>"+xmlEscape(executable)+"</string>") {
		return false, errors.New("launchd service belongs to another installation")
	}
	return true, nil
}
func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&apos;").Replace(s)
}
func systemctl(args ...string) error {
	if len(args) > 0 && args[0] == "stop" {
		if exec.Command("/bin/launchctl", "print", launchdDomain()+"/"+systemdUnit).Run() != nil {
			return nil
		}
		if err := launchctl("bootout", launchdDomain()+"/"+systemdUnit); err != nil {
			return err
		}
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		executable, err = filepath.EvalSymlinks(executable)
		if err != nil {
			return err
		}
		cfg, name, err := configFile(filepath.Dir(executable), "")
		if err != nil {
			return err
		}
		state, err := configPath(cfg.State, filepath.Dir(name))
		if err != nil {
			return err
		}
		deadline := time.Now().Add(12 * time.Second)
		for time.Now().Before(deadline) {
			if inspectDaemon(state) == daemonStopped {
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}
		return errors.New("launchd service did not stop within 12 seconds")
	}
	return nil
}
func startSystemdService(address string) error {
	probe, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("cannot listen on %s: %w", address, err)
	}
	probe.Close()
	target := launchdDomain() + "/" + systemdUnit
	if exec.Command("/bin/launchctl", "print", target).Run() != nil {
		if err := launchctl("bootstrap", launchdDomain(), systemdUnitPath); err != nil {
			return err
		}
	}
	if err := launchctl("kickstart", target); err != nil {
		return err
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if host == "" || host == "0.0.0.0" {
		address = net.JoinHostPort("127.0.0.1", port)
	} else if host == "::" {
		address = net.JoinHostPort("::1", port)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			fmt.Printf("filemanager started at http://%s (launchd)\n", address)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("launchd service did not become ready; check the server log in the state directory")

}
func unregisterSystemdService() error {
	if err := systemctl("stop"); err != nil {
		return err
	}
	return removeUninstallFile(systemdUnitPath)
}

func notifySystemdReady() error { return nil }
