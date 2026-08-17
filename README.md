# PocketNAS

把你的手机 / 电脑变成一台轻量个人 NAS：一个单二进制 Go 服务，内嵌 Web 前端，
提供文件管理、相册、Live Photo 播放与视频在线转码，可运行在 Linux、Windows、
Android（薄壳 APK）以及 Docker 中。

![screenshot placeholder](docs/screenshot.png)

## 功能

- **文件管理**：浏览、上传、下载、重命名、移动、新建目录、删除（所有操作限定在
  `-root` 存储根目录内），`.pocketnas` 元数据目录自动隐藏。
- **相册**：媒体扫描、缩略图、时间线画廊（`/api/gallery`、`/api/thumb/*`）。
- **Live Photo / Motion Photo**：支持四种格式——Pixel 旧版 MicroVideo、
  Pixel MotionPhoto、三星 MotionPhoto（XMP / `MotionPhoto_Data` 尾标记）、
  iOS Live Photo（同名 `.heic`/`.jpg` + `.mov` 配对），在线提取并播放内嵌视频。
- **视频转码**：按需 ffmpeg 转码，多分辨率（360p / 720p / 1080p），任务队列 +
  磁盘缓存 + 进度查询（`/api/video/*`）。需系统安装 ffmpeg。
- **多共享路径**：设置页可配置多个共享目录（名称 + 路径），客户端仅能访问
  共享内的文件；未配置时保持整根可见（兼容模式）。配置持久化于
  `<Root>/.pocketnas/settings.json`（`GET/PUT /api/settings/shares`，
  目录选择器由 `GET /api/system/browse` 支持）。
- **鉴权**：可选密码登录（token 认证，`-password` 开启；不设置则免登录）。
- **系统信息**：`/api/system/info` 返回版本、存储根、磁盘余量与 Go 版本。

## 快速开始

### Linux / Windows（预编译二进制）

从 Releases 下载对应平台的二进制：

```bash
# Linux
chmod +x pocket-nas-linux-amd64
./pocket-nas-linux-amd64 -root /path/to/storage -port 8080 -password yourpass

# Windows
pocket-nas-windows-amd64.exe -root D:\storage -port 8080 -password yourpass
```

打开 http://localhost:8080 即可使用。

参数：`-root`（必填，存储根目录）、`-addr`（默认 `0.0.0.0`）、
`-port`（默认 `8080`，被占用时自动递增，最多 +100）、`-password`（可选）。

### systemd（Linux 常驻服务）

发布包内附带 `pocket-nas.service` 模板（源文件见 `packaging/pocket-nas.service`）：

```bash
sudo install -m755 pocket-nas-linux-amd64 /usr/local/bin/pocket-nas
sudo install -m644 pocket-nas.service /etc/systemd/system/pocket-nas.service
sudo mkdir -p /srv/pocket-nas
sudo systemctl daemon-reload && sudo systemctl enable --now pocket-nas
```

### Docker

```bash
docker build -t pocket-nas .
docker run -d -p 8080:8080 -v /path/to/storage:/data pocket-nas
```

容器以 `-root /data` 启动，`EXPOSE 8080`，`VOLUME /data`。

### Android（APK 薄壳）

见下文「构建 Android APK」。APK 内嵌同一 Go 核心，WebView 加载本地服务。

## 开发构建

要求 Go 1.23+。

```bash
go build ./cmd/pocket-nas        # 本地构建
go test ./...                    # 运行测试
```

### 发布构建（三平台）

```bash
VERSION=v0.5.0 bash scripts/build-all.sh
```

产物输出到 `dist/`：

- `pocket-nas-linux-amd64`
- `pocket-nas-linux-arm64`
- `pocket-nas-windows-amd64.exe`
- `pocket-nas.service`（systemd 模板）
- `SHA256SUMS`

版本号通过 `-ldflags -X main.Version=... -X pocket-nas/internal/files.Version=...`
注入，可用 `./dist/pocket-nas-linux-amd64 -version` 或
`curl localhost:8080/api/system/info` 验证。

推送 `v*` tag 会触发 `.github/workflows/release.yml`：矩阵构建三平台二进制并
上传 Release assets，另一个 job 构建 Android APK 并上传。

### 构建 Android APK

要求 JDK 17、Android SDK（API 34）、Android NDK、Go + gomobile。

```bash
go install golang.org/x/mobile/cmd/gomobile@latest
gomobile init

# 1) 用 gomobile bind 生成 Go 核心 .aar
gomobile bind -target=android/arm64 -androidapi 26 \
  -o android/app/libs/pocketnas.aar ./mobile

# 2) 打包 APK
cd android && ./gradlew assembleDebug
# 产物：android/app/build/outputs/apk/debug/app-debug.apk
```

`android/` 为纯源码 Kotlin 薄壳（`com.pocketnas.app`，minSdk 26 / targetSdk 34）：
前台服务中运行 Go 核心，WebView 全屏加载 `http://127.0.0.1:8080`，默认存储根
`/storage/emulated/0`，含存储权限与电池优化白名单引导。

## 项目结构

```
cmd/pocket-nas      入口（flag 解析、-version）
internal/config     配置解析与校验
internal/server     HTTP 路由、鉴权中间件
internal/files      文件 API + /api/system/info
internal/media      相册扫描、缩略图
internal/livephoto  Live Photo / Motion Photo 解析与提取
internal/transcode  ffmpeg 多分辨率转码（队列 + 缓存）
mobile/             gomobile bind 入口（Android）
web/                embed 的静态前端
android/            Kotlin 薄壳（纯源码）
packaging/          systemd unit 模板
scripts/            构建与冒烟脚本
```

## 第三方依赖与协议

| 依赖 | 用途 | 协议 |
| --- | --- | --- |
| github.com/go-chi/chi/v5 | HTTP 路由 | MIT |
| github.com/disintegration/imaging | 缩略图/图像处理 | MIT |
| github.com/rwcarlsen/goexif | EXIF 解析 | BSD-2-Clause |
| golang.org/x/image | 图像编解码扩展 | BSD-3-Clause |
| modernc.org/sqlite | 纯 Go SQLite（媒体索引） | BSD-3-Clause |
| github.com/dustin/go-humanize（间接） | 人性化数字格式 | MIT |
| github.com/google/uuid（间接） | UUID | BSD-3-Clause |
| github.com/mattn/go-isatty（间接） | TTY 检测 | MIT |
| github.com/ncruces/go-strftime（间接） | 时间格式化 | MIT |
| github.com/remyoudompheng/bigfft（间接） | 大数乘法 | BSD-3-Clause |
| golang.org/x/sys（间接） | 系统调用 | BSD-3-Clause |
| modernc.org/libc / mathutil / memory（间接） | sqlite 运行时 | BSD-3-Clause |

PocketNAS 本体以 MIT License 发布。

## License

MIT
