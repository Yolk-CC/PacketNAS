# PocketNAS — M3 SPEC（Live Photo / Motion Photo）

> 在 M2 基础上叠加。M1/M2 契约不变。Go 工具：`export PATH=/tmp/go/bin:$PATH GOPATH=/tmp/gopath`
> （注意：$HOME 会被环境周期清空，Go 装在 /tmp/go，安装包缓存在 /mnt/agents/output/.toolchain/go.tgz）

## 0. 背景与格式要点（实现依据，务必遵守）

Motion Photo = 一个 JPEG/HEIC 文件，头部 XMP 含标记，**文件尾部拼接了一个完整 MP4**。

| 格式 | XMP 标记 | 视频定位 |
|---|---|---|
| Pixel 旧版 MicroVideo | `GCamera:MicroVideo=1` + `GCamera:MicroVideoOffset` | Offset = 从**文件尾**向前数的字节数 |
| Pixel 新版 MotionPhoto | `GCamera:MotionPhoto=1` + `GCamera:MotionPhotoPresentationTimestampUs`，**通常无偏移** | ftyp 扫描 |
| Samsung 旧版 | XMP `samsung:MotionPhoto=1` 或 JPEG 尾部固定标记 `MotionPhoto_Data` | 搜索尾部 `MotionPhoto_Data` 标记，其后即 MP4 |
| Samsung 新版 | 同 Pixel ftyp 扫描 | ftyp 扫描 |
| iOS Live Photo | 同目录同名 `.heic`/`.jpg` + `.mov` 配对 | 独立 .mov 文件 |

**ftyp 扫描算法**：从文件尾向前（或直接全文）扫描 MP4 box 签名：4 字节 big-endian 长度 + `ftyp`，且后随 brand 为 `isom|iso2|mp41|mp42|avc1|qt  ` 之一。取**最后一个**合法 ftyp 出现位置作为视频起点（JPEG 数据内不会含该签名，尾部 MP4 的 ftyp 是最后一个）。验证：从该位置解析 box 链，若首个 box size 合法（>8 且不超出文件尾）则采纳。

## 1. 后端模块

```
internal/livephoto/
├── parse.go        # XMP 提取 + 格式识别 + 视频偏移定位（纯函数，可单测）
├── extract.go      # 按需提取 MP4 片段到缓存 + HTTP handler
└── parse_test.go   # 用合成样本测试
```

### 1.1 解析器契约

```go
type Info struct {
    Type        string // "pixel" | "pixel_legacy" | "samsung" | "ios" | "none"
    VideoOffset int64  // 从文件头算起的视频起点（iOS 为 0）
    VideoLength int64  // iOS 为 companion 文件大小
    Companion   string // iOS: .mov 相对路径；其他 ""
}
func Parse(path string, data []byte) Info  
// data = 文件头前 128KB（XMP 必在 APP1，128KB 足够）。
// 需要全文扫描 ftyp 时，函数内部自行 os.Open 读取（调用方只传头部）。
// 无法识别 → Info{Type:"none"}
```

### 1.2 扫描器集成（改 internal/media）
- DB 迁移：`ALTER TABLE media_index ADD COLUMN is_live_photo INTEGER DEFAULT 0` 等 5 列（live_type TEXT, companion_path TEXT, video_offset INTEGER, video_length INTEGER）。`Open` 时用 `PRAGMA table_info` 检测列是否存在再 ALTER（兼容 M2 旧库）。
- 扫描图片时：读头部 128KB 调 `livephoto.Parse`，结果入库。
- iOS 配对：扫描完成后（Full/Incremental 末尾）跑一遍配对：对库里所有 .heic/.jpg 检查同目录同名 .mov 存在 → 更新 is_live_photo/live_type=ios/companion_path；同名优先，多个候选取 mtime 差 <5s 者。
- `/api/gallery` 的 items 增加字段：`"isLivePhoto":true,"liveType":"pixel"`。

### 1.3 提取 API
`GET /api/livephoto/<path...>`
- 非 Live Photo → 404。iOS → 直接 ServeContent companion .mov。
- 内嵌型：按 offset/length 从原文件截取，写入 `.pocketnas/livecache/<sha256(path+mtime)>.mp4` 缓存，ServeContent 提供（支持 Range，前端 video 需要）。
- 路径安全：复用 files.Resolve。

## 2. 前端（gallery.js 扩展）

- 网格：Live Photo 缩略图右上角加「LIVE」角标（CSS 胶囊样式）
- 灯箱内交互：
  - PC：鼠标悬停 1.5s 后开始播放视频层（`<video muted loop>` 叠在原图上，淡入）；移出停止并恢复静态图
  - 移动端：长按 1.5s 播放，松手停止
  - 视频源：`/api/livephoto/<path>`（fetch blob → objectURL，与 M2 相同模式）
  - 播放中显示静音图标，点击切换 `video.muted`
- 文件管理器页(index.html)：Live Photo 文件名旁加小角标（依赖 /api/files 返回？——不，M1 files API 无此字段；改为：仅相册页展示角标，文件页不做）

## 3. 测试样本（测试内合成，不依赖真机）

测试辅助函数合成：
1. **pixel_legacy**：JPEG(任意小图) + MP4(ffmpeg 生成 1s) 拼接；APP1 XMP 含 `GCamera:MicroVideo=1` `GCamera:MicroVideoOffset=<mp4长度>`
2. **pixel 新版**：拼接同上，XMP 只含 `GCamera:MotionPhoto=1`（无 offset）→ 必须走 ftyp 扫描
3. **samsung 旧版**：拼接 + 尾部标记方式（MP4 前加 `MotionPhoto_Data` 标记的版本：JPEG + MP4，XMP 含 samsung 标记，用 ftyp 兜底也可）——两种能解析即合格
4. **ios**：a.heic（任意字节伪造扩展名即可，解析只认配对逻辑）+ a.mov（真 mp4）
5. 普通 jpg → none

## 4. M3 DoD

1. build/vet/test 全绿（含 M1/M2 旧测试，-count=1）；GOOS=windows 编译通过
2. Parse 单测覆盖上表 5 种情形 + 损坏 XMP + 截断文件
3. 集成：扫描含合成 Motion Photo 的目录 → /api/gallery 正确标记 → /api/livephoto 返回 200 video/mp4 且字节与嵌入 MP4 完全一致 → Range 206 → 非 live 文件 404
4. 前端 node --check 通过；fetch 路径与契约一致
