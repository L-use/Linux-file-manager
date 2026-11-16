#!/bin/sh
set -eu

lang=
message() {
  if [ "$lang" = zh ]; then printf '%s' "$2"; else printf '%s' "$1"; fi
}
say() { message "$1" "$2"; printf '\n'; }
paint() {
  if [ -t 1 ] && [ "${NO_COLOR+x}" != x ] && [ "${TERM:-}" != dumb ]; then
    printf '\033[%sm%s\033[0m' "$1" "$2"
  else
    printf '%s' "$2"
  fi
}
rule='────────────────────────────────────────────'
heading() {
  printf '\n  '
  paint '1;36' "$(message "$1" "$2")"
  printf '\n  %s\n' "$rule"
}
step() {
  printf '\n  '
  paint '1;36' "$(message "$1" "$2")"
  printf '\n'
}
fail() { say "$@" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || fail "Required command: $1" "缺少命令：$1"; }
ask() {
  if [ "$terminal" = stdin ]; then
    printf '%s' "$1" >&2
    IFS= read -r answer
  else
    printf '%s' "$1" >/dev/tty
    IFS= read -r answer </dev/tty
  fi
}
writable_directory() (
  case "$1" in /*) ancestor=$1 ;; *) ancestor="$PWD/$1" ;; esac
  until [ -e "$ancestor" ] || [ -L "$ancestor" ]; do
    parent=$(dirname -- "$ancestor")
    [ "$parent" != "$ancestor" ] || exit 1
    ancestor=$parent
  done
  [ -d "$ancestor" ] && [ -w "$ancestor" ] && [ -x "$ancestor" ]
)
usage() {
  cat <<'HELP'
Usage:
  sh install.sh --repo OWNER/REPO [--tag TAG] [options]

Options:
  --lang zh|en        Installer and management menu language
  --user              Install for the current user without sudo
  --install-dir DIR   Program and config location (skip the online prompt)
  --bin-dir DIR       Directory for the filemanager command
  --root DIR          Initial managed directory (default: ~)
  --port PORT         Initial HTTP port (default: 8080)
  --start             Start after installation, replacing a running instance
  -h, --help          Show usage

System defaults: /usr/local/filemanager and /usr/local/bin.
User defaults: ~/.local/share/filemanager and ~/.local/bin.
Existing config.json and state are kept. On upgrade, edit config.json to
change the root or port. The GitHub repository defaults to
L-use/Linux-file-manager and can be overridden with FILEMANAGER_REPO.
HELP
}

repo=${FILEMANAGER_REPO:-L-use/Linux-file-manager}
tag=
install_dir=
bin_dir=
managed_root='~'
port=8080
custom_config=false
start=false
user_install=false
while [ "$#" -gt 0 ]; do
  option=$1
  case "$option" in
    --start) start=true; shift; continue ;;
    --user) user_install=true; shift; continue ;;
    -h|--help) usage; exit 0 ;;
    --repo|--tag|--lang|--install-dir|--bin-dir|--root|--port)
      [ "$#" -gt 1 ] && [ -n "$2" ] || fail "$option requires a value" "$option 需要指定值"
      case "$option" in
        --repo) repo=$2 ;;
        --tag) tag=$2 ;;
        --lang) lang=$2 ;;
        --install-dir) install_dir=$2 ;;
        --bin-dir) bin_dir=$2 ;;
        --root) managed_root=$2; custom_config=true ;;
        --port) port=$2; custom_config=true ;;
      esac
      shift 2 ;;
    *) fail "Unknown option: $option" "未知选项：$option" ;;
  esac
done

terminal=
if [ -t 0 ]; then
  terminal=stdin
elif ( : </dev/tty ) 2>/dev/null; then
  terminal=tty
fi
if [ -z "$lang" ] && [ -n "$terminal" ]; then
  heading 'filemanager  /  Online setup' 'filemanager  /  Online setup'
  printf '  1. Chinese (中文)\n  2. English\n\n'
  while :; do
    ask '  Select language [2]: ' || fail "Installation cancelled." "已取消安装。"
    case "$answer" in
      1|zh) lang=zh; break ;;
      ''|2|en) lang=en; break ;;
      *) printf '%s\n' 'Enter 1 or 2.' >&2 ;;
    esac
  done
fi
lang=${lang:-en}
case "$lang" in zh|en) ;; *) fail "Use --lang zh or --lang en." "请使用 --lang zh 或 --lang en。" ;; esac

[ "$(uname -s)" = Linux ] || fail "This installer is for Linux." "此安装脚本仅支持 Linux。"
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) fail "Unsupported architecture: $(uname -m)" "不支持的架构：$(uname -m)" ;;
esac
case "$port" in
  ''|0*|*[!0-9]*) fail "Port must be an integer from 1 to 65535." "端口必须是 1 到 65535 的整数。" ;;
esac
[ "${#port}" -le 5 ] && [ "$port" -le 65535 ] || fail "Port must be an integer from 1 to 65535." "端口必须是 1 到 65535 的整数。"
case "$managed_root" in
  /*|'~'*) ;;
  *) fail "--root must be an absolute path or begin with ~." "--root 必须是绝对路径或以 ~ 开头。" ;;
esac
case "$managed_root" in
  *'
'*) fail "--root cannot contain a newline." "--root 不能包含换行。" ;;
esac
if printf '%s\n' "$managed_root" | LC_ALL=C grep -q '[[:cntrl:]]'; then
  fail "--root cannot contain control characters." "--root 不能包含控制字符。"
fi

if [ "$(id -u)" -ne 0 ] && [ "$user_install" = false ]; then
  command -v sudo >/dev/null 2>&1 ||
    fail "System installation requires sudo. Run as root or add --user." "系统安装需要 sudo。请用 root 运行，或加上 --user。"
  case "$0" in /*) script=$0 ;; *) script="$PWD/$0" ;; esac
  [ -f "$script" ] || fail "Download install.sh to a file before running it." "请先将 install.sh 下载为文件后再运行。"
  case "$managed_root" in
    '~') managed_root=$HOME ;;
    '~/'*) managed_root="$HOME/${managed_root#'~/'}" ;;
  esac
  set -- --lang "$lang" --repo "$repo" --root "$managed_root" --port "$port"
  [ -z "$tag" ] || set -- "$@" --tag "$tag"
  [ -z "$install_dir" ] || set -- "$@" --install-dir "$install_dir"
  [ -z "$bin_dir" ] || set -- "$@" --bin-dir "$bin_dir"
  [ "$start" != true ] || set -- "$@" --start
  say "Using sudo for system installation." "将通过 sudo 进行系统安装。"
  exec sudo -- sh "$script" "$@"
fi

menu_prefix=
if [ "$(id -u)" -eq 0 ]; then
  [ "$user_install" = false ] ||
    fail "Run --user as the intended user without sudo." "使用 --user 时，请以目标用户直接运行。"
  default_install_dir=/usr/local/filemanager
  bin_dir=${bin_dir:-/usr/local/bin}
  if [ -n "${SUDO_USER:-}" ] && [ "$SUDO_USER" != root ]; then
    menu_prefix='sudo '
    if [ "$managed_root" = '~' ] && command -v getent >/dev/null 2>&1; then
      caller_home=$(getent passwd "$SUDO_USER" | cut -d: -f6)
      if [ -n "$caller_home" ]; then
        managed_root=$caller_home
        custom_config=true
      fi
    fi
  fi
else
  default_install_dir="${XDG_DATA_HOME:-$HOME/.local/share}/filemanager"
  bin_dir=${bin_dir:-$HOME/.local/bin}
fi

asset="file-manager-linux-$arch"
original_umask=$(umask)
umask 077
work=$(mktemp -d "${TMPDIR:-/tmp}/filemanager-install.XXXXXXXX")
staged_program=
staged_config=
staged_setting=
staged_unit=
cleanup() {
  [ -z "$staged_program" ] || rm -f -- "$staged_program"
  [ -z "$staged_config" ] || rm -f -- "$staged_config"
  [ -z "$staged_setting" ] || rm -f -- "$staged_setting"
  [ -z "$staged_unit" ] || rm -f -- "$staged_unit"
  rm -rf -- "$work"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

need tar
need curl
printf '%s\n' "$repo" | LC_ALL=C grep -Eq '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$' ||
  fail "Use --repo OWNER/REPO." "请通过 --repo OWNER/REPO 指定 GitHub 仓库。"
if [ -n "$tag" ]; then
  printf '%s\n' "$tag" | LC_ALL=C grep -Eq '^[A-Za-z0-9_.-]+$' ||
    fail "Unsupported release tag." "发布标签格式无效。"
  release="https://github.com/$repo/releases/download/$tag"
else
  release="https://github.com/$repo/releases/latest/download"
fi
heading 'filemanager  /  Installation' 'filemanager  /  在线安装'
step '1/3  Installation directory' '1/3  安装位置'
choose_dir=false
if [ -z "$install_dir" ] && [ -n "$terminal" ]; then
  choose_dir=true
fi
while :; do
  if [ "$choose_dir" = true ]; then
    ask "$(message "  Install directory [$default_install_dir]: " "  安装目录 [$default_install_dir]：")" ||
      fail "Installation cancelled." "已取消安装。"
    install_dir=$answer
  fi
  install_dir=${install_dir:-$default_install_dir}
  case "$install_dir" in
    '~') install_dir=$HOME ;;
    '~/'*) install_dir="$HOME/${install_dir#'~/'}" ;;
  esac
  if writable_directory "$install_dir"; then
    break
  fi
  if [ "$choose_dir" != true ]; then
    fail "Cannot write to the install directory: $install_dir" "安装目录不可写：$install_dir"
  fi
  say "  Cannot write to $install_dir. Choose a writable directory or accept the default." \
      "  无法写入 $install_dir。请选择可写目录，或按回车使用默认位置。"
done
writable_directory "$bin_dir" ||
  fail "Cannot write to the command directory: $bin_dir" "命令目录不可写：$bin_dir"
say "  Directory  $install_dir" "  安装目录  $install_dir"
archive_name="$asset.tar.gz"
archive="$work/$archive_name"
checksums="$work/SHA256SUMS"
step '2/3  Download and verify' '2/3  下载与校验'
say "  Downloading the $arch release…" "  正在下载 $arch 发布包…"
curl --proto '=https' --proto-redir '=https' -fsSL -- "$release/SHA256SUMS" > "$checksums" ||
  fail "Could not download SHA256SUMS." "无法下载 SHA256SUMS 校验清单。"
curl --proto '=https' --proto-redir '=https' -fsSL -- "$release/$archive_name" > "$archive" ||
  fail "Could not download $archive_name." "无法下载 $archive_name。"

expected=$(awk -v file="$archive_name" '$2 == file || $2 == "*" file {print $1}' "$checksums")
[ "${#expected}" -eq 64 ] ||
  fail "Expected one SHA-256 entry for $archive_name." "$archive_name 应有且仅有一条 SHA-256 校验记录。"
case "$expected" in *[!0-9a-fA-F]*) fail "Invalid SHA-256 in manifest." "校验清单中的 SHA-256 无效。" ;; esac
expected=$(printf '%s' "$expected" | tr 'A-F' 'a-f')
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum < "$archive")
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 < "$archive")
else
  fail "Required command: sha256sum or shasum." "需要 sha256sum 或 shasum 命令。"
fi
[ "${actual%% *}" = "$expected" ] ||
  fail "SHA-256 verification failed for $archive_name." "$archive_name 的 SHA-256 校验失败。"
say '  SHA-256 verified' '  SHA-256 校验通过'
# Stream the two known members to fixed destinations.
tar -xOzf "$archive" "$asset/filemanager" > "$work/filemanager" ||
  fail "Missing executable for $arch." "发布包缺少 $arch 可执行文件。"
tar -xOzf "$archive" "$asset/config.json" > "$work/config.json" ||
  fail "Missing initial config.json." "发布包缺少初始 config.json。"

# The first 20 ELF bytes identify a 64-bit little-endian Linux release and its CPU.
header=$(od -An -v -t u1 -N20 "$work/filemanager")
set -- $header
[ "$#" -eq 20 ] && [ "$1 $2 $3 $4 $5 $6" = "127 69 76 70 2 1" ] || fail "Invalid release executable." "发布包中的可执行文件无效。"
case "$arch:${19}:${20}" in
  amd64:62:0|arm64:183:0) ;;
  *) fail "Release executable does not match $arch." "可执行文件与 $arch 架构不匹配。" ;;
esac

step '3/3  Install program' '3/3  安装程序'
mkdir -p -- "$install_dir" "$bin_dir" ||
  fail "Could not create the installation directories." "无法创建安装目录或命令目录。"
install_dir=$(CDPATH='' cd -- "$install_dir" && pwd -P)
bin_dir=$(CDPATH='' cd -- "$bin_dir" && pwd -P)
program="$install_dir/filemanager"
config="$install_dir/config.json"
link="$bin_dir/filemanager"
register_service=false
if [ "$(id -u)" -eq 0 ] && [ -d /run/systemd/system ] && command -v systemctl >/dev/null 2>&1; then
  # A root service must not execute a binary replaceable by a normal user.
  directory=$install_dir
  while :; do
    owner=$(stat -c %u -- "$directory")
    mode=$(stat -c %a -- "$directory")
    if [ "$owner" != 0 ] || [ "$((0$mode & 022))" -ne 0 ]; then
      fail "A root service requires a root-owned installation path without group or world write access: $directory" \
           "系统服务需要由 root 所有、且同组和其他用户不可写的安装路径：$directory"
    fi
    [ "$directory" != / ] || break
    directory=$(dirname -- "$directory")
  done
  case "$program" in
    *'
'*) fail "The systemd program path contains a newline." "systemd 的程序路径不能包含换行。" ;;
  esac
  if printf '%s' "$program" | LC_ALL=C grep -q '[[:cntrl:]]'; then
    fail "The systemd program path contains control characters." "systemd 的程序路径不能包含控制字符。"
  fi
  service_path=/etc/systemd/system/filemanager.service
  systemd_program=$(printf '%s' "$program" | sed 's/[\\"]/\\&/g; s/%/%%/g')
  exec_line="ExecStart=\"$systemd_program\" -foreground"
  if [ -e "$service_path" ] || [ -L "$service_path" ]; then
    [ ! -L "$service_path" ] &&
      grep -Fxq '# Managed by Linux File Manager' "$service_path" &&
      grep -Fxq "$exec_line" "$service_path" ||
      fail "filemanager.service belongs to a different installation." "filemanager.service 属于另一套安装。"
  fi
  register_service=true
fi
if [ "$link" != "$program" ] && { [ -e "$link" ] || [ -L "$link" ]; }; then
  [ -L "$link" ] && [ "$(readlink "$link")" = "$program" ] ||
    fail "Command already exists at $link." "命令已存在：$link"
fi
[ ! -L "$program" ] && { [ ! -e "$program" ] || [ -f "$program" ]; } ||
  fail "Installation destination is not a regular file: $program" "安装目标不是普通文件：$program"
[ ! -e "$config" ] && [ ! -L "$config" ] || [ -f "$config" ] ||
  fail "Configuration is not a regular file: $config" "配置不是普通文件：$config"

staged_program=$(mktemp "$install_dir/.filemanager.XXXXXXXX")
cp -- "$work/filemanager" "$staged_program"
chmod 0755 "$staged_program"
if [ ! -f "$config" ]; then
  staged_config=$(mktemp "$install_dir/.config.XXXXXXXX")
  if [ "$custom_config" = true ]; then
    root_json=$(printf '%s' "$managed_root" | sed 's/[\\"]/\\&/g')
    printf '{\n  "root": "%s",\n  "host": "0.0.0.0",\n  "port": %s,\n  "state_dir": "./state",\n  "secure_cookie": false,\n  "ffmpeg": ""\n}\n' "$root_json" "$port" > "$staged_config"
  else
    cat "$work/config.json" > "$staged_config"
  fi
  ln -- "$staged_config" "$config"
elif [ "$custom_config" = true ]; then
  say "Keeping existing root and port. Edit $config to change them." "已保留原目录和端口设置。修改请编辑 $config。"
fi
if [ "$start" = true ] && [ -x "$program" ]; then
  FILEMANAGER_COMMAND_LANGUAGE="$lang" "$program" stop
fi
mv -f -- "$staged_program" "$program"
staged_program=
if [ "$link" != "$program" ] && [ ! -L "$link" ]; then
  ln -s -- "$program" "$link"
fi

write_setting() {
  [ ! -d "$install_dir/$1" ] || fail "Cannot write $install_dir/$1." "无法写入 $install_dir/$1。"
  staged_setting=$(mktemp "$install_dir/.install-setting.XXXXXXXX")
  printf '%s' "$2" > "$staged_setting"
  mv -f -- "$staged_setting" "$install_dir/$1"
  staged_setting=
}
write_setting filemanager.language "$lang"
write_setting filemanager.command "$link"
if [ "$register_service" = true ]; then
  staged_unit=$(mktemp /etc/systemd/system/.filemanager.XXXXXXXX)
  cat > "$staged_unit" <<EOF
# Managed by Linux File Manager
[Unit]
Description=Linux File Manager
After=network.target

[Service]
Type=notify
NotifyAccess=main
$exec_line
Restart=on-failure
RestartSec=3s
TimeoutStartSec=20s
TimeoutStopSec=12s

[Install]
WantedBy=multi-user.target
EOF
  chmod 0644 "$staged_unit"
  mv -f -- "$staged_unit" "$service_path"
  staged_unit=
  systemctl daemon-reload
  write_setting filemanager.systemd filemanager.service
  say '  Registered systemd service filemanager.service' '  已注册 systemd 服务 filemanager.service'
fi

printf '\n  %s\n  ' "$rule"
paint '1;32' "$(message 'Installation complete' '安装完成')"
printf '\n'
say "  Command        $link" "  命令位置  $link"
say "  Configuration  $config" "  配置文件  $config"
case ":${PATH:-}:" in
  *":$bin_dir:"*) say "  Open menu      ${menu_prefix}filemanager" "  打开菜单  ${menu_prefix}filemanager" ;;
  *) say "  Open menu      $menu_prefix$program" "  打开菜单  $menu_prefix$program"
     say "  Add to PATH    $bin_dir" "  添加到 PATH  $bin_dir" ;;
esac
printf '  %s\n' "$rule"
if [ "$start" = true ]; then
  umask "$original_umask"
  if [ "$terminal" = tty ]; then
    "$program" start </dev/tty
  else
    "$program" start
  fi
fi
