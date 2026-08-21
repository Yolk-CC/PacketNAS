# SPEC-M8 — Server 独立化 + 局域网发现 + Client 预备 API

背景：拆分为 pocketnas-server（本里程碑）与 pocketnas-client（M9 新建原生 App）。
决策：人脸识别在服务端、人脸数据以 sha256 为键可导出导入、模型为可替换 ONNX（本里程碑只需在 API 设计中预留 /api/faces 命名空间，不实现）。

## 1. Go 服务端

- `GET /api/system/info` 响应增加字段：`serverName`（默认主机名，可配置）、`apiLevel`（整数，当前=2，供 client 做能力协商）。
- 配置：Config 增加 `-name` flag（默认 os.Hostname），Android 端 mobile.Start 传 "PocketNAS"。
- **局域网发现**：启动时在 UDP 45777 监听广播；收到文本 `POCKETNAS_DISCOVER`（v1 协议）即回复 `POCKETNAS_HERE|<serverName>|<port>|<apiLevel>` 到来源地址。独立 goroutine，失败仅记日志不影响主服务。关闭时随 server 退出。
- 以上全部向后兼容；补单测（info 含新字段；发现协议可用 127.0.0.1 发 UDP 包自测）。

## 2. Android 包 → pocketnas-server

- applicationId 改为 `com.pocketnas.server`，应用名 "PocketNAS Server"。
- 主界面保留现有 WebView 管理台（它是 server 端管理入口），但顶部增加一个原生状态条：显示服务运行状态 + 局域网地址（如 http://192.168.1.5:8080）+ 点击复制。实现尽量薄（Kotlin，Service 回调或轮询 /api/system/info 均可，选改动最小的）。
- minSdk/targetSdk 不变。

## 3. CI

- release.yml 的 Android job 产物重命名上传为 `pocketnas-server-debug.apk`。
- 其余 job 不变。

## 4. 文档

- README 增加"架构"小节：server/client 分离说明、发现协议一句话、API level 说明。

## 5. 验收

- `go test ./...` 全绿；冒烟：启动后 `GET /api/system/info` 含 serverName/apiLevel；用 `printf POCKETNAS_DISCOVER | nc -u -w1 127.0.0.1 45777`（或 python socket）能收到 `POCKETNAS_HERE|...` 回复。
- Android 部分本环境无法编译（无 SDK），保证 Kotlin 代码静态可读、`applicationId` 与 CI 引用一致即可；CI 会验证编译。
