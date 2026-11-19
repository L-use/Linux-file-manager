<img width="1586" height="992" alt="image" src="https://github.com/user-attachments/assets/7207adcb-ba48-4856-b690-426d8da0e397" /><img width="1586" height="992" alt="image" src="https://github.com/user-attachments/assets/11a4061f-1a35-4bde-b57c-3a1871b517f5" />






# Linux File Manager

[English](#english) · [简体中文](#简体中文)

## English

Linux File Manager lets you browse and manage existing Linux server directories from your browser. The Go executable embeds the web interface, fonts, icons, document readers, and PDF worker, so the application can run offline.

### Quick online installation

Run this on the Linux server. The default system installation uses root privileges, installs to `/usr/local/filemanager`, and listens on port 8080. The default managed directory is the home directory of the user who starts the installer.

```sh
curl -fsSL https://github.com/L-use/Linux-file-manager/releases/latest/download/install.sh -o install.sh && sh install.sh --start
```

1. The installer first prompts in English. Enter `1` for Chinese or `2` for English. English is the default.
2. Choose where to install the program, or press Enter to use the default path.
3. The installer verifies the archive and registers the command. On systems running systemd, it also registers `filemanager.service`. At first startup, follow the prompts to set the `admin` password.

System installation puts the command in `/usr/local/bin` and enables startup at boot after the first successful systemd start. For an installation without sudo, add `--user`. That installs to `~/.local/share/filemanager`, or `$XDG_DATA_HOME/filemanager` when set, with the command in `~/.local/bin`.

| Option | Purpose |
| --- | --- |
| `--root PATH` | Set the directory managed through the browser. |
| `--user` | Install or upgrade for the current user. |
| `--install-dir PATH` | Set the program and configuration directory and skip the path prompt. |
| `--port PORT` | Set the initial port. The default is 8080. |
| `--lang zh\|en` | Select the installer and menu language and skip the language prompt. |
| `--start` | Start after installing. During an upgrade, stop the existing instance first. |

The installer uses `curl`, `tar`, and either `sha256sum` or `shasum`. Run it again to upgrade while keeping the existing configuration and state. Without a terminal, the installer defaults to English and the default installation directory.

To pin a release, download `install.sh` from `releases/download/VERSION/install.sh` and pass the same version with `--tag`. Maintainers should upload the script, both online `.tar.gz` archives, both offline ZIPs, and `SHA256SUMS` to the same GitHub Release.

### Management menu and first startup

- **Online installation**: run `filemanager` as root, `sudo filemanager` when managing a system installation as a regular user, or `filemanager` for a per-user installation.
- **Offline use**: download a ZIP from [GitHub Releases](https://github.com/L-use/Linux-file-manager/releases/latest), extract it, and run `./filemanager` from the extracted directory. Select amd64 for Linux `x86_64`, or arm64 for `aarch64` and `arm64`. If extraction loses the executable bit, run `chmod +x ./filemanager`.

The menu refreshes the service status each time it opens. Green **ACTIVE** means running and red **STOP** means stopped.

| Option | Action |
| --- | --- |
| `1` | Start the service. |
| `2` | Stop the service. |
| `3` | Change the administrator password. Stop the service first. |
| `4` | Change the terminal menu language. |
| `5` | Uninstall the program, configuration, credentials, preview caches, and recycle bin. Unregister the systemd service created by the installer. |
| `0` | Exit the menu. |

1. Edit `config.json` next to the executable and set `root` to the directory you want to manage. The default is the service user's home directory. You can specify `"/"`, `"~ubuntu"`, or the absolute path to an existing directory.
2. Open the menu and choose `1`. Set the password for `admin` by entering it twice. It must be at least 12 characters. The terminal hides your input.
3. When the menu shows **ACTIVE**, open `http://SERVER_IP:8080` in your browser and sign in as `admin`. Use your configured port if it differs.

The service runs in the background and requires the standard Linux `/proc` mount. Use `journalctl -u filemanager.service` for systemd logs. The standalone service writes to `state/filemanager.log` by default. The uninstaller lists the files, links, and directories it removes. It preserves managed files, unrelated files in shared directories, and the system's shared journal.

### Configuration and service commands

The program reads `config.json` beside the executable. Relative paths are resolved from the directory containing that configuration file.

```json
{
  "root": "/srv/files",
  "host": "0.0.0.0",
  "port": 8080,
  "state_dir": "./state",
  "secure_cookie": false,
  "ffmpeg": "bundled"
}
```

Edit `port` and restart the service to change ports. The legacy `listen` field overrides `host` and `port`. The command line supports `--config`, `--root`, `--listen`, `--state-dir`, `--secure-cookie`, and `--ffmpeg`. Use the same custom configuration or state-directory flags when stopping or resetting the password. The state directory is created automatically and must be private to the service user.

For automation, use `filemanager start`, `filemanager stop`, or `filemanager -reset`. For an offline installation, prefix the command with `./`. Use `sudo systemctl status filemanager.service` to inspect a system installation. For an offline installation with your own systemd unit, set the entry point to `filemanager -foreground`. The compatibility entry point `./file-manager` also runs in the foreground.

### Video compatibility

The Linux amd64 and arm64 release packages embed FFmpeg. The default `"ffmpeg": "bundled"` selects the embedded copy. When a bundled build starts with an empty or missing FFmpeg setting, it records `"ffmpeg": "bundled"` in the configuration. If you prefer a different build, set `"ffmpeg": "/usr/bin/ffmpeg"` or pass `--ffmpeg /usr/bin/ffmpeg`. That build needs the `fd` protocol and the AAC, libx264, and libmp3lame encoders.

The browser plays supported files at their original quality. For formats it cannot decode, including some MOV, MKV, AVI, WMV, MPEG, TS, FLV, and 3GP files, the server attempts to create a seekable H.264/AAC MP4 preview up to 1080p. It handles one conversion at a time, with a ten-minute timeout and an output limit of 512 MiB or the configured cache limit, whichever is lower. Downloads always provide the original file.

### Public HTTPS

For public access, bind the application to `127.0.0.1:8080` and serve HTTPS through a reverse proxy such as Caddy or Nginx. Set `"secure_cookie": true` in the configuration. With Caddy and a domain pointing to the server, use a Caddyfile entry like this.

```caddyfile
files.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Configure a certificate and allow incoming connections to the HTTPS port. The proxy must pass through Range requests and allow the upload sizes and durations you need. You can turn off request buffering to stream uploads directly. Sign in at `https://files.example.com`. Keep `secure_cookie` set to `false` for direct HTTP access on a local network.

### Upgrading

Use Check for updates under Settings → Updates to check manually, receive update reminders, or automatically install the latest stable release. The version list lets you install compatible historical releases. Manually installing a historical release switches automatic installation to manual. In-app installation requires a versioned Linux release, server access to GitHub, and write access to the program directory. It verifies the archive checksum and restarts the service. The Settings gear lets you change the managed directory in `config.json`. Select Restart service beside the directory setting to apply it. The listening port is configured separately in the file.

1. Stop the existing service. If the previous version runs in the foreground, press Ctrl+C in its terminal.
2. Extract the new ZIP to a temporary directory. Replace the installed `filemanager` executable and run `chmod +x filemanager`. Keep your existing `config.json` and `state/`. If you previously configured the root and port with flags, you can copy the new `config.json` and put those settings in it.
3. From the original directory, run `./filemanager` and choose Start, or run `filemanager start` for an online installation. Sign back in through your browser.

### Features and limits

- **Browsing and selection** · Directories load on demand and Refresh picks up changes made through SSH. Sort each folder by name, size, or modification time. The browser remembers your sorting and theme preferences. Large Icons view shows image thumbnails. Drag from a tile to select multiple items, then move them to the recycle bin together.
- **Search and details** · The search field filters loaded nodes. Search subfolders scans the selected directory recursively, up to 50,000 entries and 150 matches. Open Details from a context menu to see the path, size, disk usage, timestamps, owner, permissions, and inode. Recursive folder counts skip symbolic links and flag partial results.
- **Uploads and downloads** · Upload whole folders, including empty subfolders, by selecting or dragging them into Explorer. Existing directories merge, and replacements require confirmation. The upload queue shows bytes, percentage, and speed. Failed or unfinished items can be retried on the same page. Downloads use the browser's progress display.
- **ZIP extraction and recycling** · Extract ZIP from the context menu into a new folder next to the archive. Existing paths, unsafe paths, links, and special files are rejected. The limit is 20,000 entries and 16 GiB per archive. Recycled items can be restored to their original paths, with conflicts reported before overwriting. The recycle bin and the files being removed must be on the same filesystem. Emptying the recycle bin permanently deletes its entries for the current managed root.
- **Permissions and file operations** · Right-click, use the row menu, or press Shift+F10 for file actions. Set owner, group, and other permissions using presets, individual permission bits, or octal values such as `0755`. Folder permissions apply to the selected directory itself. The Hide setting only affects the current browser's Explorer and search, and the eye button reveals hidden folders.
- **Image and video previews** · Large JPEG and PNG files initially use a smaller preview when available. Zooming in, switching to 100%, or entering fullscreen loads the original image. HEIC and HEIF previews decode the primary image. Images above 50 megapixels or 96 MiB may need to be downloaded. Images and videos support pan and zoom. The video timeline shows buffered ranges in gray and played progress in blue.
- **Fullscreen and editing** · Fullscreen is available for image, video, code, PDF, and Office previews. Press Esc to exit. Use Up and Down in the normal preview, or Left and Right in fullscreen, to switch between files in the current directory. Images and videos open at the largest size that preserves the whole frame and its aspect ratio. Select Fill screen to crop the edges and cover the display. Text previews read the first 512 KiB or 12,000 lines of UTF-8. Fully displayed text, code, and Markdown can be edited. DOCX editing supports listed body paragraphs, headings, and table-cell text while preserving images and document structure. Some complex paragraphs are omitted from the editor. XLSX editing supports existing plain text, numbers, and booleans. Existing formulas and workbook structure are preserved. Formula cells, protected sheets, and cells absent from the original file are read-only.
- **Saving and document limits** · Use Save, Ctrl+S, or Cmd+S. Closing or switching files prompts for unsaved edits. Browser reload or tab closure triggers the browser prompt. Changes made by other processes are checked at save time, and conflicts leave your draft intact. DOCX and spreadsheet preview inputs are limited to 48 MiB, with decompression limits for Office files. Tables render in segments, up to 20,000 rows and 512 columns per sheet. PDF and CSV previews are read-only. Text and CSV support UTF-8, UTF-16, and GB18030 with a manual encoding selector. Non-UTF-8 text is read-only. Older Office formats can be downloaded.
- **Live status and preview cache** · The status bar shows memory, disk usage for the managed filesystem, uptime, and request latency. Hover for up to 12 running services or processes, with memory RSS and uptime. The panel refreshes every 100 ms in the active tab. Settings shows cache sizes and item counts. The shared preview cache has a persistent limit of 64 MiB to 4 GiB, with a default of 1 GiB.
- **Filesystem boundaries** · Access is limited by the configured root and the service user's Linux permissions. Traversal through symbolic links is blocked, while a link itself can be renamed, moved, or deleted. Application files and private state paths are protected. Replacement is limited to regular files. Moving uses atomic filesystem renames and fails across filesystems. Concurrent changes or system errors during recursive removal may require checking the remaining files from the command line.

- **Additional previews** · MP3, M4A, AAC, WAV, FLAC, and OGG/Opus play with speed controls. ZIP, TAR, TAR.GZ, 7z, and RAR can be browsed without extraction. ZIP members can be downloaded individually. PPTX supports page-by-page viewing of text and embedded images. SVG supports image and source views, TIFF shows the first image, and DNG shows its embedded JPEG when present. Large Icons shows real video frames and content thumbnails where the file can be decoded.
- **Compatibility** · Bundled FFmpeg supplies audio and video conversion, fallback video posters, and additional TIFF decoders. PPTX complex layouts and animations may differ. Encrypted archives and DNG files without a readable embedded preview can still be downloaded.

### Build from source

Install Go 1.25 or newer, `tar`, `zip`, and `sh` on the build machine. Go modules and web assets are vendored. For a full Linux release, extract `ffmpeg-source.tar.gz` into an FFmpeg source directory and build the bundled binaries with Docker.

```sh
go test -mod=vendor ./...
./build-ffmpeg.sh ./ffmpeg-source ./ffmpeg-dist
FFMPEG_SOURCE_DIR="$PWD/ffmpeg-source" ./build.sh
```

The generated archives, installer, FFmpeg corresponding source bundle, and SHA-256 checksums are in `dist/`. Local Linux packages include versioned filenames such as `file-manager-linux-amd64-v27.0.2.zip`. To build just the executable for the local platform, run `go build -mod=vendor -o filemanager .`.

### License

The source is licensed under the [MIT License](LICENSE). Bundled components retain their own licenses and attributions. See [THIRD_PARTY.md](THIRD_PARTY.md).

## 简体中文

在浏览器中登录，浏览和管理 Linux 服务器现有目录。Go 程序内嵌前端、字体、图标、文档解析器和 PDF worker，可以在隔离网络中运行。

### 在线快速安装

在 Linux 服务器终端运行。默认通过 sudo 系统安装到 `/usr/local/filemanager`，管理发起安装的用户家目录，端口为 8080。

```sh
curl -fsSL https://github.com/L-use/Linux-file-manager/releases/latest/download/install.sh -o install.sh && sh install.sh --start
```

| 步骤 | 操作 |
| --- | --- |
| 选择语言 | 安装器先用英文询问。输入 `1` 选中文，输入 `2` 或直接回车选英文。 |
| 选择安装位置 | 此处填写程序的存放位置。输入可写路径，或回车使用默认位置。 |
| 安装和启动 | 脚本校验发布包并注册命令。系统安装使用 root 权限。首次运行时按提示设置 `admin` 密码，程序随即启动。支持 systemd 的服务器会注册 `filemanager.service`。 |

系统安装默认写入 `/usr/local/filemanager`，命令放在 `/usr/local/bin`。在 systemd 服务器上，首次成功启动后会启用开机运行。普通用户使用 `--user` 可免 sudo 安装，默认使用 `~/.local/share/filemanager`。设置了 `XDG_DATA_HOME` 时，改用其下的 `filemanager` 目录。个人安装的命令放在 `~/.local/bin`。

#### 常用安装选项

| 选项 | 用途 |
| --- | --- |
| `--root 路径` | 设置网页管理的目录。 |
| `--user` | 以当前普通用户安装和运行。已有普通用户安装用此选项升级。 |
| `--install-dir 路径` | 指定程序和配置的存放位置，跳过路径询问。 |
| `--port 端口` | 指定首次安装的端口。默认 8080。 |
| `--lang zh\|en` | 指定安装与菜单语言，跳过语言询问。 |
| `--start` | 安装后直接启动。升级时会先停止已安装的实例。 |

在线安装使用系统的 `curl`、`tar` 以及 `sha256sum` 或 `shasum`，省去预先安装解压工具的步骤。再次运行脚本可升级，已有配置和状态会保留。无终端时默认使用英文和默认安装位置。

发布到 GitHub 时，将 `install.sh`、两个在线安装用的 `.tar.gz`、两个离线 ZIP 和 `SHA256SUMS` 上传到同一个 Release。固定版本时，下载地址改用 `releases/download/版本标签/install.sh`，同时传入相同的 `--tag`。

### 使用程序

#### 打开管理菜单

- **在线安装后**，root 运行 `filemanager`。普通用户管理系统安装时运行 `sudo filemanager`，个人安装直接运行 `filemanager`。
- **离线使用**，从 [GitHub Releases](https://github.com/L-use/Linux-file-manager/releases/latest) 下载对应架构的 ZIP，解压后在程序目录运行 `./filemanager`。Linux `x86_64` 选择 amd64，`aarch64` 或 `arm64` 选择 arm64。如执行权限丢失，运行 `chmod +x ./filemanager`。

菜单顶部显示服务状态。绿色 **ACTIVE** 表示已启动，红色 **STOP** 表示已停止。每次回到菜单都会重新读取状态。

| 选项 | 操作 |
| --- | --- |
| `1` | 启动服务 |
| `2` | 停止服务 |
| `3` | 修改管理员密码。请先停止服务。 |
| `4` | 切换终端菜单语言 |
| `5` | 彻底卸载。确认后停止服务，清理程序、配置、密码记录、预览缓存和回收站，注销安装器创建的 systemd 服务。受管理的文件保留。 |
| `0` | 退出菜单 |

#### 首次启动与登录

1. 编辑程序旁边的 `config.json`，将 `root` 设为要管理的目录。默认是启动用户的家目录。也可以填写 `"/"`、`"~ubuntu"` 或已有目录的绝对路径。
2. 打开菜单，选择 `1`。为 `admin` 输入两遍密码，至少 12 个字符。终端会隐藏输入。
3. 菜单显示 **ACTIVE** 后，在浏览器打开 `http://服务器IP:8080`，用 `admin` 和刚设置的密码登录。若修改了端口，请使用对应端口。

程序会在后台运行。systemd 安装用 `journalctl -u filemanager.service` 查看日志，独立后台运行的日志在 `state/filemanager.log`。程序会自动读取可执行文件旁的 `config.json`，配置中的相对路径以配置文件所在目录为准。发布版需要 Linux 的标准 `/proc` 挂载。

卸载时会逐条列出已删除的文件、链接和目录，结束后显示「卸载完成」。如果安装目录或自定义状态目录里还有其他文件，卸载会保留这些文件及其目录。操作系统的共享 systemd 日志按系统的日志策略保留。

```json
{
  "root": "/srv/files",
  "host": "0.0.0.0",
  "port": 8080,
  "state_dir": "./state",
  "secure_cookie": false,
  "ffmpeg": "bundled"
}
```

自动化可用 `filemanager start`、`filemanager stop` 和 `filemanager -reset` 直接执行操作。离线使用时加上 `./`。

### 其他启动选项

修改端口只需编辑 `config.json` 的 `port`，再停止并重新启动。旧配置的 `listen` 字段仍支持，设置后会覆盖 `host` 和 `port`。命令行仍支持 `--config`、`--root`、`--listen`、`--state-dir`、`--secure-cookie` 和 `--ffmpeg`。使用自定义配置或状态目录启动时，停止或重设密码也要传同样的选项。状态目录会自动创建，已有状态目录应由服务用户持有且仅对该用户开放。

在线系统安装会自动注册 systemd 服务。可运行 `sudo systemctl status filemanager.service` 查看状态，使用管理菜单可启动、停止或卸载。离线版自行注册服务时，入口为 `filemanager -foreground`。包内兼容入口 `./file-manager` 保留原前台行为。

### 兼容视频预览

Linux amd64 和 arm64 发布包已内置 FFmpeg，默认配置 `"ffmpeg": "bundled"` 使用内置版本，旧配置为空或缺少此项时，带内置 FFmpeg 的程序会在启动时补写 `"ffmpeg": "bundled"`。需要指定其他构建时，可设置 `"ffmpeg": "/usr/bin/ffmpeg"` 或传入 `--ffmpeg /usr/bin/ffmpeg`。自选构建需支持 `fd` 协议和 AAC、libx264、libmp3lame 编码器。

浏览器能解码的视频直接播放原文件。对浏览器无法解码的 MOV、MKV、AVI、WMV、MPEG、TS、FLV、3GP 等文件，程序尝试生成 H.264/AAC MP4 预览。兼容预览最高 1080p，生成后可跳转。每次运行最多同时转换一个视频，单次转换上限为 10 分钟，输出上限为 512 MiB 与所设置缓存容量中的较小值。下载始终提供原文件。

### 公网 HTTPS

公网使用时，将程序监听地址设为 `127.0.0.1:8080`，以 Caddy、Nginx 等反向代理提供 HTTPS。在 JSON 设置 `"secure_cookie": true`。例如，已经部署 Caddy 且有可解析到服务器的域名时，可以使用下面的 Caddyfile 配置。

```caddyfile
files.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

为域名配置证书并允许访问 HTTPS 端口。代理须保留浏览器的 Range 请求，允许所需上传大小和时间。若代理会缓冲请求内容，可关闭请求缓冲以便服务器直接接收上传流。通过 `https://files.example.com` 登录。局域网直接通过 HTTP 访问时，保持 `secure_cookie` 为 `false`。

### 更新程序

「设置 → 软件更新」中的「检查更新」可选择手动检查、有新版本时提醒或自动安装最新正式版。版本列表可以选择安装与服务器架构兼容的历史版本。手动安装历史版本时，自动安装会切换为手动。程序内更新需要使用带版本号的 Linux 发布包，服务器能访问 GitHub，运行账号能写入程序目录。安装会先校验发布包，然后重启服务。通过齿轮「设置」可修改 `config.json` 的管理目录，保存后点击旁边的「重启服务」即可生效。监听端口仍在配置文件中单独设置。

1. 停止旧服务。旧版以前台方式运行时，到其终端按 Ctrl+C。
2. 将新 ZIP 解压到临时目录，把新的 `filemanager` 可执行文件复制到原安装目录并执行 `chmod +x filemanager`。保留已有的 `config.json` 和 `state/`。如果原来靠命令行指定管理目录和端口，可复制新版 `config.json` 到原目录，并把旧命令的设置填进去。
3. 在原目录执行 `./filemanager` 并选择启动，或直接运行 `filemanager start`。浏览器重新登录。

### 使用和限制

- 在「设置 → 外观与语言」中切换白昼和黑夜。主题选择保存在当前浏览器。
- 大图标视图显示图片缩略图，可从文件卡片拖动框选并批量移入回收站。视频进度条以灰色显示已缓冲范围、蓝色显示已播放进度。设置页可查看、清理预览缓存，并将容量上限设为 64 MiB 至 4 GiB，默认 1 GiB，重启后保留。

- 右键「移至回收站」后可立即撤销，也可从左下角「回收站」恢复。恢复只使用原路径，遇到同名条目会提示冲突。永久删除在回收站中单独确认。回收站保存在状态目录下，状态目录与待删除文件必须位于同一文件系统。跨文件系统时，移入回收站会失败并保留原件。
- 搜索框可筛选已加载的节点。搜索框右侧的「搜索子目录」可在选中目录内递归按名称搜索，并定位到结果。一次最多搜索 50,000 个条目、返回 150 条匹配结果，超限时会提示。
- 上传队列显示当前文件的已上传字节、实际百分比和传输速度。同名文件可逐个处理或选择「全部跳过」。失败和未完成的项目可在当前页面重试。下载进度由浏览器显示。
- 通过资源管理器工具栏的「排序」，可在每一级目录内按名称、文件大小或修改日期排序。大小和日期支持升序与降序，文件夹排在文件前面。按大小排序时，文件夹按名称排列。浏览器记住最近一次排序选择。
- 从右键或行末菜单打开「详情」，可以查看路径、大小、磁盘占用、时间、所有者、Linux 权限和 Inode。文件系统提供创建时间时才显示该项。「统计文件夹内容」会递归统计可访问的普通文件与子目录，不跟随符号链接。达到扫描上限或遇到无法访问的内容时会标记为部分结果。
- 从 ZIP 的右键或行末菜单选择「解压 ZIP」，可在同一目录下解压到新文件夹。同名路径会提示冲突。压缩包内的越界路径、链接和特殊文件会被拒绝。每次最多解压 20,000 项和 16 GiB。
- 回收站的「清空回收站」会在确认后永久删除当前管理根目录的回收站项目。部分项目删除失败时会显示结果。
- 底栏显示服务器内存、管理根目录所在文件系统的使用率、系统运行时间和网络请求耗时。悬停时展开实时面板，移开即关闭。面板在前台每 100 毫秒刷新，最多展示 12 条占用内存最高的可见服务及其 RSS 和运行时长。系统无法识别 systemd 服务时显示可见进程。
- 大 JPEG、PNG 默认尝试先显示轻量的窗口预览。放大、切到 100% 或进入全屏时加载原图。无法生成窗口预览时会直接加载原图。

- 目录按需加载。搜索已加载的名称，刷新可同步通过 SSH 等方式进行的改动。右键、行末菜单或 Shift+F10 可打开文件操作。使用「更改权限」可为文件所有者、同组用户及其他用户选择「可编辑」「只读」或「无权限」。展开「高级设置」可逐项勾选读、写、执行权限，或输入八进制权限（如 `755` 或 `0755`）。文件夹权限设置作用于选中的文件夹本身。文件夹的隐藏开关只影响当前浏览器的目录树和搜索显示。通过资源管理器底部的眼睛按钮可重新显示隐藏文件夹。选中目录后上传或新建文件夹，选中文件时以文件所在目录为目标。使用「上传文件夹」可保留文件夹名称和子目录结构。桌面上可将文件夹拖到资源管理器，连同空子目录一起上传。同名目录会合并，替换同名文件前会询问。取消后，已上传完成的项目会保留。
- 预览保留原始图片和视频数据。滚轮以指针位置为中心缩放，拖动可平移。触屏支持双指缩放。HEIC 和 HEIF 可预览主图，浏览器解码后仍可使用原有缩放工具。超过 50 百万像素或 96 MiB 的图片可能需要下载查看。点击预览顶部的全屏按钮可让图片、视频、代码、PDF、文档和表格占满屏幕，按 Esc 退出全屏。普通预览用上下方向键切换同一目录的文件，全屏用左右方向键。图片和视频进入全屏时默认等比最大显示完整画面，也可手动选择「铺满屏幕」。浏览器无法进入原生全屏时会显示提示。
- 文本预览读取前 512 KiB、最多 12,000 行的 UTF-8 内容。完整显示的代码、文本和 Markdown 可在线编辑。Word DOCX 可编辑所列正文、标题和表格单元格的文字，保留文档结构和图片，包含域、修订或其他复杂内容的段落不会列入编辑器。使用「保存」或 Ctrl+S（macOS 为 Cmd+S）写回原路径。关闭预览或切换文件时，如有未保存修改，可选择保存并继续、放弃或取消。关闭浏览器标签页或刷新网页时，由浏览器弹出离开提示。保存时会检查文件是否已被其他进程更新，冲突时保留编辑区的草稿。
- XLSX 可通过「编辑」修改已有普通单元格的文字、数字和布尔值，保存时保留公式和原有工作簿结构，打开 Excel 时重新计算。网页预览使用文件内的公式缓存结果。公式、受保护工作表和未存储在原文件中的空白格不能在线编辑。关闭预览或切换文件时使用相同的保存提示。
- 文本和 CSV 支持 UTF-8、UTF-16 和 GB18030 预览及手动切换编码。非 UTF-8 文本为只读。DOCX 和表格预览的源文件上限是 48 MiB，并限制 Office 解压大小。表格按段渲染，每张表最多显示前 20,000 行和 512 列。DOCX 预览的复杂排版可能变化。PDF 和 CSV 提供只读预览，旧 Office 格式可下载原件。
- Web 文件访问受根目录和服务进程权限约束。目录遍历拒绝符号链接，可对链接条目本身执行重命名、移动或删除。程序文件和私有状态路径受保护。进程以普通用户运行时仍受 Linux 权限约束。
- 覆盖只适用于普通文件。移动使用文件系统原子的重命名，同一根内跨文件系统的移动会提示失败。与其他 Linux 进程共享目录时，程序在变更前检查文件版本。Linux 可在检查后继续并发修改目录。从回收站永久删除目录前会核查可访问性，递归删除过程中如有外部变更或系统错误，可能需要通过命令行检查剩余内容。

- **新增预览格式** · MP3、M4A、AAC、WAV、FLAC 和 OGG/Opus 支持播放与倍速。ZIP、TAR、TAR.GZ、7z、RAR 可直接查看条目，其中 ZIP 支持单个成员下载。PPTX 可逐页查看文字和图片。SVG 可切换图片与源码，TIFF 显示首张图像，DNG 显示内嵌 JPEG。大图标模式会为可解码的文件显示内容缩略图，并用实际视频帧作为视频封面。
- **兼容性** · 随包 FFmpeg 提供音视频兼容转换、后备视频封面和部分 TIFF 的解码。PPTX 的复杂排版和动画可能有差异。加密压缩包和没有可读内嵌预览的 DNG 可下载原文件。

### 从源码构建

在构建机安装 Go 1.25 或更新版本、`tar`、`zip` 和 `sh`。源树内有 Go 模块和网页组件。要构建完整 Linux 发布包，先解压 `ffmpeg-source.tar.gz`，然后用 Docker 构建两个架构的 FFmpeg。

```sh
go test -mod=vendor ./...
./build-ffmpeg.sh ./ffmpeg-source ./ffmpeg-dist
FFMPEG_SOURCE_DIR="$PWD/ffmpeg-source" ./build.sh
```

两个离线 ZIP、两个在线安装包、源码 ZIP、安装脚本、FFmpeg 对应源码包和 SHA-256 校验清单位于 `dist/`。本地 Linux 安装包另有带版本号的文件名，例如 `file-manager-linux-amd64-v27.0.2.zip`。本地单独构建可用 `go build -mod=vendor -o filemanager .`。第三方信息见 `THIRD_PARTY.md`。

### 许可证

项目源码采用 [MIT](LICENSE) 许可证。随包发布的第三方组件保留各自的许可证与署名，见 [THIRD_PARTY.md](THIRD_PARTY.md)。
