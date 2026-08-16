# PocketNAS — M5/M6 SPEC（安卓壳 + 三平台打包发布）

> 基线：master（M1–M4 已合并）。Go：`export PATH=/tmp/go/bin:$PATH GOPATH=/tmp/gopath GOPROXY=https://mirrors.aliyun.com/goproxy/,https://goproxy.cn,direct GOSUMDB=off`

## M5 — Android 壳

### 目标
APK = Kotlin 薄壳 + Go 核心（gomobile bind 生成 .aar）。核心已在 M1–M4 完成，本里程碑只做胶水。

### 后端侧（mobile binding）
新增 `mobile/mobile.go`：
```go
// Package mobile 供 gomobile bind 生成 Android 绑定
package mobile

// Start 启动 PocketNAS 服务。root 为存储根目录（如 /storage/emulated/0），
// password 可为空。返回实际监听地址（如 "http://0.0.0.0:8080"）。
func Start(root, password string, port int) string
func Stop()
```
- 内部调用 server.Start（需要 server 包支持非阻塞启动 + Stop；若 M1 的 Start 是阻塞式的，加一个 `server.StartAsync(cfg) (addr string, stop func(), err error)` 包装，保持 M1 行为与测试不变）
- `gomobile bind -target=android/arm64 -androidapi 26` 若本机无 NDK 则跳过实际构建，只保证 `go build ./mobile/` 通过（普通 GOOS=linux 可编译即可，gomobile 兼容由 CI 验证）

### 安卓侧（android/ 目录，纯源码交付）
```
android/
├── settings.gradle / build.gradle（根）
├── app/build.gradle           # applicationId com.pocketnas.app, minSdk 26, targetSdk 34, 依赖 aar
├── app/libs/                  # pocketnas.aar 放置处（.gitkeep）
├── app/src/main/AndroidManifest.xml
└── app/src/main/java/com/pocketnas/app/
    ├── MainActivity.kt        # WebView 全屏加载 http://127.0.0.1:<port>；启动 NasService；权限引导
    ├── NasService.kt          # 前台服务：通知栏常驻（"PocketNAS 运行中 · 点击打开"），
    │                          #   onCreate 起 Go 核心(mobile.Start)，onDestroy 调 Stop
    └── PermissionHelper.kt    # Android 11+ MANAGE_EXTERNAL_STORAGE 申请与降级
                               #   （READ_MEDIA_IMAGES/VIDEO），电池优化白名单引导
```
- 不要求本地编译 APK（无 Android SDK），但代码必须完整、可直接被 Android Studio / CI 打开构建
- 存储根目录默认 `/storage/emulated/0`，端口 8080
- WebView 设置：domStorageEnabled、允许文件访问、处理文件选择（上传需要 onShowFileChooser）

## M6 — 打包与发布

### scripts/build-all.sh
交叉编译发布产物到 dist/：
- `pocket-nas-linux-amd64`、`pocket-nas-linux-arm64`
- `pocket-nas-windows-amd64.exe`
- 每个含版本号注入（-ldflags "-X main.Version=v0.5.0"——需要 main.go 支持 Version 变量，若无则顺手加上，保持测试绿）
- 输出 SHA256SUMS

### 附带产物
- `dist/pocket-nas.service`（systemd unit 模板）
- `Dockerfile`（多阶段：golang 构建 + alpine 运行，EXPOSE 8080，VOLUME /data）
- `.github/workflows/release.yml`：tag 触发 → 矩阵构建三平台 + 上传 release assets；Android job 用 android-actions/setup-android + gomobile 构建 APK（允许此 job 只写不验）
- `README.md`：三平台使用说明、APK 构建说明、功能列表、截图占位、协议说明

### DoD
1. build-all.sh 在 Linux 实跑成功，dist/ 产物齐全且 linux 版可运行（起服务 curl /api/system/info 显示注入版本号）
2. go test -count=1 ./... 保持全绿；GOOS=windows/arm64 编译通过
3. android/ 源码完整性自查清单（文件逐个存在、Kotlin 语法人工核对）
4. README 无虚构内容（功能以已实现为准）
