package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/term"
)

const languageFile = "filemanager.language"
const registeredCommandFile = "filemanager.command"
const commandLanguageEnv = "FILEMANAGER_COMMAND_LANGUAGE"

var commandLanguage = "en"

func cli(english, chinese string) string {
	if commandLanguage == "zh" {
		return chinese
	}
	return english
}

func loadCommandLanguage(appDir string) bool {
	data, err := os.ReadFile(filepath.Join(appDir, languageFile))
	if err != nil {
		return false
	}
	switch strings.TrimSpace(string(data)) {
	case "zh":
		commandLanguage = "zh"
	case "en":
		commandLanguage = "en"
	default:
		return false
	}
	return true
}

func saveCommandLanguage(appDir, language string) error {
	temporary, err := os.CreateTemp(appDir, ".filemanager-language-")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	_, err = fmt.Fprintln(temporary, language)
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporary.Name(), filepath.Join(appDir, languageFile))
}

func readMenuChoice(input *bufio.Reader, prompt string) (string, bool) {
	fmt.Print(prompt)
	value, err := input.ReadString('\n')
	return strings.TrimSpace(value), err == nil || value != ""
}

func menuStyle(color, value string) string {
	if _, disabled := os.LookupEnv("NO_COLOR"); disabled || os.Getenv("TERM") == "dumb" || !term.IsTerminal(int(os.Stdout.Fd())) {
		return value
	}
	return "\033[" + color + "m" + value + "\033[0m"
}

func menuServiceStatus(appDir string) daemonStatus {
	config, name, err := configFile(appDir, "")
	if err != nil || config.State == "" {
		return daemonUnknown
	}
	path, err := configPath(config.State, filepath.Dir(name))
	if err != nil {
		return daemonUnknown
	}
	return inspectDaemon(path)
}

func showManagementMenu(appDir string) {
	const rule = "────────────────────────────────────────────"
	var badge, detail, color string
	switch menuServiceStatus(appDir) {
	case daemonActive:
		badge, color = "● ACTIVE", "1;32"
		detail = cli("Running", "已启动")
	case daemonStopped:
		badge, color = "● STOP", "1;31"
		detail = cli("Stopped", "已停止")
	case daemonPending:
		badge, color = "● WAIT", "1;33"
		detail = cli("Waiting for service", "正在等待服务响应")
	default:
		badge, color = "● UNKNOWN", "1;33"
		detail = cli("Status unavailable", "无法读取状态")
	}
	fmt.Printf("\n  %s\n  %s\n", menuStyle("1;36", cli("filemanager  /  Management", "filemanager  /  程序管理")), menuStyle("2", rule))
	fmt.Printf("  %s  %s\n  %s\n", cli("Status", "状态"), menuStyle(color, badge+"  "+detail), menuStyle("2", rule))
	for _, option := range []struct {
		number, color, label string
	}{
		{"1", "32", cli("Start service", "启动服务")},
		{"2", "31", cli("Stop service", "停止服务")},
		{"3", "36", cli("Change administrator password", "修改管理员密码")},
		{"4", "36", cli("Change menu language", "修改菜单语言")},
		{"5", "31", cli("Uninstall program", "卸载程序")},
		{"0", "2", cli("Exit menu", "退出菜单")},
	} {
		fmt.Printf("  %s  %s\n", menuStyle(option.color, option.number+"."), option.label)
	}
	fmt.Printf("  %s\n", menuStyle("2", rule))
}

func selectMenuLanguage(input *bufio.Reader, appDir string) bool {
	for {
		fmt.Printf("\n  %s\n  1. 中文 (Chinese)\n  2. English\n\n", menuStyle("1;36", cli("Select language", "选择语言")))
		choice, read := readMenuChoice(input, cli("  Select language [2]: ", "  选择语言 [2]："))
		if !read {
			return false
		}
		var language string
		switch strings.ToLower(choice) {
		case "1", "zh", "中文":
			language = "zh"
		case "", "2", "en", "english":
			language = "en"
		default:
			fmt.Println(cli("Enter 1 or 2.", "请输入 1 或 2。"))
			continue
		}
		commandLanguage = language
		if err := saveCommandLanguage(appDir, language); err != nil {
			fmt.Printf(cli("Language applies to this session only: %v\n", "语言仅在本次会话生效：%v\n"), err)
		}
		return true
	}
}

func runMenuCommand(executable, argument string) error {
	command := exec.Command(executable, argument)
	command.Env = append(os.Environ(), commandLanguageEnv+"="+commandLanguage)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	return command.Run()
}

func managementMenu(executable string, hasLanguage bool) error {
	appDir := filepath.Dir(executable)
	input := bufio.NewReader(os.Stdin)
	if !hasLanguage && !selectMenuLanguage(input, appDir) {
		return nil
	}
	for {
		showManagementMenu(appDir)
		choice, read := readMenuChoice(input, cli("  Select an option [0–5]: ", "  请选择 [0–5]："))
		if !read || choice == "0" {
			return nil
		}
		switch choice {
		case "1":
			_ = runMenuCommand(executable, "start")
		case "2":
			_ = runMenuCommand(executable, "stop")
		case "3":
			if err := runMenuCommand(executable, "-reset"); err != nil {
				fmt.Println(cli("If the service is running, choose Stop before changing its password.", "如果服务正在运行，请先选择停止，再修改密码。"))
			}
		case "4":
			if !selectMenuLanguage(input, appDir) {
				return nil
			}
		case "5":
			answer, ok := readMenuChoice(input, cli(
				"Completely uninstall filemanager? [y/N]: ",
				"确认彻底卸载 filemanager？[y/N]："))
			if !ok {
				return nil
			}
			if strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes") {
				if err := runMenuCommand(executable, "uninstall"); err == nil {
					return nil
				}
			}
		default:
			fmt.Println(cli("Invalid option.", "选项无效。"))
		}
	}
}
