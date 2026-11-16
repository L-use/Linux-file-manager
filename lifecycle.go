package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const lockFileName = "server.lock"
const controlFileName = "server.sock"
const logFileName = "filemanager.log"
const daemonEnv = "FILEMANAGER_INTERNAL_DAEMON"

var errAlreadyRunning = errors.New("filemanager is already running or starting for this state directory")

// On Linux, use the private directory's descriptor for binding the socket and
// looking it up. The resulting sockaddr fits even when the installation path is long.
func controlAddress(dir *os.File) string {
	return lifecycleSocketPath(dir, controlFileName)
}

func privateStateFile(dir *os.File, name string, flags int) (*os.File, error) {
	file, err := openAt(dir, name, flags, 0600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		file.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%s must be a private regular file", name)
	}
	return file, nil
}

func lockServer(dir *os.File) (*os.File, error) {
	lock, err := privateStateFile(dir, lockFileName, unix.O_RDWR|unix.O_CREAT)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, errAlreadyRunning
		}
		return nil, err
	}
	return lock, nil
}

func listenForStop(dir *os.File, stop func()) (net.Listener, error) {
	info, err := descriptorLstat(dir, controlFileName)
	if err == nil {
		if info.Mode()&fs.ModeSocket == 0 {
			return nil, errors.New("server.sock exists and is not a Unix socket")
		}
		if err := unix.Unlinkat(int(dir.Fd()), controlFileName, 0); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	listener, err := net.Listen("unix", controlAddress(dir))
	if err != nil {
		return nil, fmt.Errorf("open stop socket: %w", err)
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			request := make([]byte, 1)
			_, err = io.ReadFull(conn, request)
			if err == nil {
				switch request[0] {
				case 'P':
					_, _ = io.WriteString(conn, "OK\n")
				case 'S':
					_, _ = io.WriteString(conn, "OK\n")
					conn.Close()
					stop()
					return
				}
			}
			conn.Close()
		}
	}()
	return listener, nil
}

type daemonStatus uint8

const (
	daemonUnknown daemonStatus = iota
	daemonStopped
	daemonPending
	daemonActive
)

func inspectDaemon(statePath string) daemonStatus {
	fd, err := unix.Open(statePath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return daemonStopped
	}
	if err != nil {
		return daemonUnknown
	}
	dir := os.NewFile(uintptr(fd), statePath)
	defer dir.Close()
	lock, err := privateStateFile(dir, lockFileName, unix.O_RDONLY)
	if errors.Is(err, fs.ErrNotExist) {
		return daemonStopped
	}
	if err != nil {
		return daemonUnknown
	}
	err = unix.Flock(int(lock.Fd()), unix.LOCK_SH|unix.LOCK_NB)
	lock.Close()
	if err == nil {
		return daemonStopped
	}
	if !errors.Is(err, unix.EWOULDBLOCK) {
		return daemonUnknown
	}
	const timeout = 250 * time.Millisecond
	conn, err := net.DialTimeout("unix", controlAddress(dir), timeout)
	if err != nil {
		return daemonPending
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := io.WriteString(conn, "P"); err != nil {
		return daemonPending
	}
	var reply [3]byte
	_, err = io.ReadFull(conn, reply[:])
	// Older processes close the connection for an unknown request. Their
	// open control socket and held lock still identify a running instance.
	if errors.Is(err, io.EOF) || (err == nil && string(reply[:]) == "OK\n") {
		return daemonActive
	}
	return daemonPending
}

func stopServer(dir *os.File) error {
	lock, err := lockServer(dir)
	if err == nil {
		lock.Close()
		fmt.Println(cli("filemanager is not running", "filemanager 未运行"))
		return nil
	}
	if !errors.Is(err, errAlreadyRunning) {
		return err
	}
	conn, err := net.DialTimeout("unix", controlAddress(dir), time.Second)
	if err != nil {
		return errors.New(cli("filemanager is starting or its stop socket is unavailable; try again shortly", "filemanager 正在启动或暂时无法停止，请稍后重试"))
	}
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	_, err = io.WriteString(conn, "S")
	if err == nil {
		var reply [3]byte
		if _, err = io.ReadFull(conn, reply[:]); err == nil && string(reply[:]) != "OK\n" {
			err = errors.New("invalid stop response")
		}
	}
	conn.Close()
	if err != nil {
		return fmt.Errorf("stop filemanager: %w", err)
	}
	for deadline := time.Now().Add(7 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		lock, err := lockServer(dir)
		if err == nil {
			lock.Close()
			fmt.Println(cli("filemanager stopped", "filemanager 已停止"))
			return nil
		}
		if !errors.Is(err, errAlreadyRunning) {
			return err
		}
	}
	return errors.New(cli("filemanager has not finished stopping", "filemanager 尚未完全停止"))
}

func startBackground(executable string, args []string, state, lock *os.File) error {
	logFile, err := privateStateFile(state, logFileName, unix.O_CREAT|unix.O_WRONLY|unix.O_APPEND)
	if err != nil {
		return fmt.Errorf("open service log: %w", err)
	}
	defer logFile.Close()
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	defer reader.Close()
	cmd := exec.Command(executable, args...)
	cmd.Env = append(os.Environ(), daemonEnv+"=1")
	cmd.ExtraFiles = []*os.File{writer, lock}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	err = cmd.Start()
	writer.Close()
	if err != nil {
		return err
	}
	_ = reader.SetReadDeadline(time.Now().Add(15 * time.Second))
	line, err := bufio.NewReader(reader).ReadString('\n')
	if err == nil && strings.HasPrefix(line, "OK ") {
		_ = cmd.Process.Release()
		fmt.Printf(cli("filemanager started at http://%s\nLog: %s\n", "filemanager 已启动，监听 http://%s\n日志：%s\n"), strings.TrimSpace(strings.TrimPrefix(line, "OK ")), filepath.Join(state.Name(), logFileName))
		return nil
	}
	if err != nil {
		_ = cmd.Process.Kill()
	}
	_ = cmd.Wait()
	if strings.HasPrefix(line, "ERR ") {
		return errors.New(strings.TrimSpace(strings.TrimPrefix(line, "ERR ")))
	}
	return fmt.Errorf("background startup failed; see %s", filepath.Join(state.Name(), logFileName))
}

func daemonFiles() (*os.File, *os.File) {
	const readyFD, lockFD = 3, 4
	unix.CloseOnExec(readyFD)
	unix.CloseOnExec(lockFD)
	return os.NewFile(readyFD, "startup response"), os.NewFile(lockFD, "inherited lock")
}

func checkInheritedLock(dir, lock *os.File) error {
	expected, err := descriptorLstat(dir, lockFileName)
	if err != nil {
		return err
	}
	actual, err := descriptorStat(lock)
	if err != nil {
		return err
	}
	if !actual.Mode().IsRegular() || !sameFile(expected, actual) {
		return errors.New("state directory changed during background startup")
	}
	return nil
}
