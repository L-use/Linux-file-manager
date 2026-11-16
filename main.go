package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

//go:embed web
var assets embed.FS

func main() {
	var ready, inheritedLock *os.File
	if os.Getenv(daemonEnv) == "1" {
		ready, inheritedLock = daemonFiles()
	}
	if err := run(ready, inheritedLock); err != nil {
		if ready != nil {
			_, _ = fmt.Fprintln(ready, "ERR", err)
		}
		log.Fatal(err)
	}
}

func run(ready, inheritedLock *os.File) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return err
	}
	appDir := filepath.Dir(executable)
	args := os.Args[1:]
	hasLanguage := loadCommandLanguage(appDir)
	if override := os.Getenv(commandLanguageEnv); override == "zh" || override == "en" {
		commandLanguage, hasLanguage = override, true
	}
	if ready == nil && len(args) == 0 && filepath.Base(os.Args[0]) != "file-manager" {
		return managementMenu(executable, hasLanguage)
	}
	command := ""
	if len(args) > 0 {
		switch args[0] {
		case "start", "stop", "uninstall":
			command, args = args[0], args[1:]
		}
	}
	stopping := command == "stop" || command == "uninstall"
	managedSystemd, err := systemdInstalled(appDir, executable)
	if err != nil {
		return err
	}
	configFlag := flag.String("config", "", "JSON configuration file (default: config.json next to executable)")
	root := flag.String("root", "", "override the management root")
	listen := flag.String("listen", "", "override the address and port")
	state := flag.String("state-dir", "", "override the private state directory")
	var reset bool
	flag.BoolVar(&reset, "reset", false, "interactively set a new password without starting the server")
	flag.BoolVar(&reset, "reset-password", false, "alias for -reset")
	foreground := flag.Bool("foreground", false, "run in the foreground")
	ffmpeg := flag.String("ffmpeg", "", "FFmpeg executable for compatible video previews")
	secure := flag.Bool("secure-cookie", false, "require HTTPS for session cookies")
	if err := flag.CommandLine.Parse(args); err != nil {
		return err
	}
	if flag.NArg() != 0 || (stopping && reset) {
		return errors.New("usage: filemanager [start | stop | -reset] [options]")
	}

	c, configName, err := configFile(appDir, *configFlag)
	if err != nil {
		return err
	}
	base := ""
	if configName != "" {
		base = filepath.Dir(configName)
	}
	stateBase, rootBase, ffmpegBase := base, base, base
	if *state != "" {
		c.State, stateBase = *state, ""
	}
	if *root != "" {
		c.Root, rootBase = *root, ""
	}
	if *ffmpeg != "" {
		c.FFmpeg, ffmpegBase = *ffmpeg, ""
	}
	if *listen != "" {
		c.Listen = *listen
	}
	if *secure {
		c.Secure = true
	}
	if c.State == "" {
		return errors.New("config.state_dir must not be empty")
	}
	stateAbs, err := configPath(c.State, stateBase)
	if err != nil {
		return fmt.Errorf("resolve state directory: %w", err)
	}

	defaultSystemd := managedSystemd && len(args) == 0
	if command == "uninstall" && managedSystemd && !defaultSystemd {
		return errors.New(cli("Uninstall the systemd installation using its default configuration.", "卸载 systemd 安装时请使用默认配置。"))
	}
	if stopping && defaultSystemd {
		if err := systemctl("stop", systemdUnit); err != nil {
			return err
		}
	}

	var rootAbs string
	listenAddress := ""
	if !reset && !stopping {
		if c.Root == "" {
			return errors.New("config.root must name an existing management directory")
		}
		rootPath, err := configPath(c.Root, rootBase)
		if err != nil {
			return fmt.Errorf("resolve management root %q: %w", c.Root, err)
		}
		listenAddress, err = c.address()
		if err != nil {
			return err
		}
		if strings.ContainsRune(c.FFmpeg, filepath.Separator) {
			c.FFmpeg, err = configPath(c.FFmpeg, ffmpegBase)
			if err != nil {
				return fmt.Errorf("resolve FFmpeg: %w", err)
			}
		}
		rootAbs, err = canonical(rootPath)
		if err != nil {
			return fmt.Errorf("management root %q: %w", c.Root, err)
		}
		probe, err := os.Open(rootAbs)
		if err != nil {
			return fmt.Errorf("management root is unreadable: %w", err)
		}
		_, err = probe.ReadDir(1)
		probe.Close()
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("management root is not a readable directory: %w", err)
		}
		if inside(stateAbs, rootAbs) {
			return errors.New("management root must be outside the private state directory")
		}
	}
	if !stopping {
		if err := os.MkdirAll(stateAbs, 0700); err != nil {
			return fmt.Errorf("create state directory: %w", err)
		}
	}
	c.State, err = canonical(stateAbs)
	if stopping && errors.Is(err, fs.ErrNotExist) {
		if command == "uninstall" {
			if defaultSystemd {
				if err := unregisterSystemdService(); err != nil {
					return err
				}
			}
			return uninstallProgram(executable, configName, nil)
		}
		if defaultSystemd {
			fmt.Println(cli("filemanager stopped", "filemanager 已停止"))
		} else {
			fmt.Println(cli("filemanager is not running", "filemanager 未运行"))
		}
		return nil
	}
	if err != nil {
		return err
	}
	info, err := os.Stat(c.State)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("private state directory %q must have mode 0700", c.State)
	}
	if rootAbs != "" && inside(c.State, rootAbs) {
		return errors.New("management root must be outside the private state directory")
	}
	if c.State == appDir {
		return errors.New("state directory must be separate from the program directory")
	}

	stateDir, err := os.Open(c.State)
	if err != nil {
		return err
	}
	defer stateDir.Close()
	if command == "stop" {
		if defaultSystemd && inspectDaemon(c.State) == daemonStopped {
			fmt.Println(cli("filemanager stopped", "filemanager 已停止"))
			return nil
		}
		return stopServer(stateDir)
	}
	if command == "uninstall" {
		if err := stopServer(stateDir); err != nil {
			return err
		}
		lock, err := lockServer(stateDir)
		if err != nil {
			return err
		}
		defer lock.Close()
		if defaultSystemd {
			if err := unregisterSystemdService(); err != nil {
				return err
			}
		}
		return uninstallProgram(executable, configName, stateDir)
	}
	lock := inheritedLock
	if lock == nil {
		lock, err = lockServer(stateDir)
	} else {
		err = checkInheritedLock(stateDir, lock)
	}
	if errors.Is(err, errAlreadyRunning) {
		return errors.New(cli(errAlreadyRunning.Error(), "此状态目录已有 filemanager 正在运行或启动"))
	}
	if err != nil {
		return err
	}
	defer lock.Close()
	auth, err := newAuth(c.State, c.Secure, reset)
	if err != nil {
		return err
	}
	if reset {
		fmt.Println(cli("Administrator password updated", "管理员密码已更新"))
		return nil
	}
	if ready == nil && !*foreground && filepath.Base(os.Args[0]) != "file-manager" {
		if defaultSystemd {
			if err := lock.Close(); err != nil {
				return err
			}
			return startSystemdService(listenAddress)
		}
		return startBackground(executable, append([]string{"start"}, args...), stateDir, lock)
	}

	videoTool, err := videoExecutable(c.FFmpeg)
	if err != nil {
		return err
	}
	videos := newVideoPreviews(videoTool, c.State)
	defer videos.close()
	protected := []string{c.State, executable}
	if managedSystemd {
		protected = append(protected, systemdUnitPath)
	}
	if videoTool != "" {
		protected = append(protected, videoTool)
	}
	for _, name := range []string{"config.json", "config.example.json", "filemanager", "file-manager", "install.sh", languageFile, registeredCommandFile, portableMarker, systemdMarker, "README.md", "LICENSE", "THIRD_PARTY.md", "web", "vendor"} {
		internal := filepath.Join(appDir, name)
		if _, err := os.Stat(internal); err == nil {
			protected = append(protected, internal)
		}
	}
	if configName != "" {
		absolute, err := filepath.Abs(configName)
		if err != nil {
			return err
		}
		name, err := canonical(absolute)
		if err != nil {
			return err
		}
		protected = append(protected, absolute, name)
	}
	for _, internal := range protected {
		if inside(internal, rootAbs) {
			return errors.New("management root overlaps protected application files")
		}
	}

	files, err := openFiles(rootAbs, protected)
	if err != nil {
		return err
	}
	defer files.root.Close()
	trash, err := openTrash(c.State, files)
	if err != nil {
		return fmt.Errorf("open recycle bin: %w", err)
	}
	defer trash.dir.Close()
	web, err := fs.Sub(assets, "web")
	if err != nil {
		return err
	}
	server := &http.Server{Handler: routes(auth, files, trash, videos, web), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return fmt.Errorf(cli("cannot listen on %s: %w", "无法监听 %s：%w"), listenAddress, err)
	}
	defer listener.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	control, err := listenForStop(stateDir, stop)
	if err != nil {
		return err
	}
	defer control.Close()
	if err := notifySystemdReady(); err != nil {
		return fmt.Errorf("notify systemd: %w", err)
	}
	log.Printf("Listening on http://%s | root: %s | user: admin", listener.Addr(), rootAbs)
	if ready != nil {
		_, _ = fmt.Fprintln(ready, "OK", listener.Addr())
		_ = ready.Close()
	}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(deadline); err != nil {
			_ = server.Close()
		}
	}()
	err = server.Serve(listener)
	stop()
	<-shutdownDone
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}

func canonical(name string) (string, error) {
	p, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

func inside(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
