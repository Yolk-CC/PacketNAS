# PocketNAS — M2 SPEC（媒体索引 + 缩略图 + 相册页）

> 在 M1 基础上叠加。M1 的所有 API/代码契约不变。单一事实来源：本文档 + SPEC.md。
> Go 工具：`export PATH=$HOME/go/bin:$PATH GOPATH=$HOME/gopath`

## 0. 新增依赖（go get 即可，代理 goproxy.cn 已配）

- `modernc.org/sqlite`（纯 Go SQLite，禁 CGO）
- `github.com/rwcarlsen/goexif`（EXIF）
- `github.com/disintegration/imaging`（缩略图缩放）
- 系统 ffmpeg/ffprobe 已安装（视频元数据/抽帧，用 os/exec 调用，禁止 cgo 绑定）

## 1. 新增模块

```
internal/media/
├── db.go          # SQLite 打开/建表/迁移；WAL 模式
├── scanner.go     # 全量+增量扫描；EXIF/ffprobe 元数据提取
├── thumb.go       # 缩略图生成 + LRU 磁盘缓存
└── handlers.go    # 相册/缩略图 API
internal/media/*_test.go
web/static/gallery.html  + gallery.js（相册页，灯箱）
```

## 2. 数据库契约（internal/media）

文件路径：`<Root>/.pocketnas/index.db`（`.pocketnas` 目录同时存放缩略图缓存 `thumb/`，扫描时跳过该目录）。

```sql
CREATE TABLE IF NOT EXISTS media_index (
    id INTEGER PRIMARY KEY,
    path TEXT UNIQUE,          -- 相对 root，"/DCIM/a.jpg"
    name TEXT, mime_type TEXT, size INTEGER, modified_time INTEGER,
    taken_time INTEGER,        -- EXIF DateTimeOriginal 优先，mtime 兜底（秒）
    width INTEGER, height INTEGER,
    duration INTEGER,          -- 视频毫秒，图片 0
    thumbnail_path TEXT,       -- 相对 .pocketnas/thumb/ 的文件名
    created_at INTEGER
);
CREATE INDEX IF NOT EXISTS idx_taken ON media_index(taken_time DESC);
```

```go
type Store struct{ /* *sql.DB */ }
func Open(root string) (*Store, error)             // 自动建目录/建表
func (s *Store) Upsert(m Media) error
func (s *Store) DeleteMissing(seen map[string]bool) error  // 增量扫描清理已删文件
func (s *Store) Page(offset, limit int, typ string) ([]Media, int, error) // typ: all|image|video；返回总数
```

## 3. 扫描器

```go
type Scanner struct{ /* store, root */ }
func (sc *Scanner) Full(ctx context.Context, progress chan<- int) error
func (sc *Scanner) Incremental(ctx context.Context) error  // 按 mtime>db记录 决定重提取
```
- 遍历 root（跳过 `.pocketnas`、隐藏目录），识别扩展名：图片 jpg/jpeg/png/gif/webp/heic/heif/bmp；视频 mp4/mkv/mov/webm/avi/m4v。
- 图片尺寸：JPEG/PNG/GIF/WebP 用 stdlib 读头（`image.DecodeConfig`，WebP 用 `golang.org/x/image/webp`——加入依赖）；EXIF 时间用 goexif，失败回退 mtime。HEIC 跳过尺寸/EXIF（记 0，taken=mtime）。
- 视频元数据：`ffprobe -v quiet -print_format json -show_format -show_streams <file>`，解析 width/height/duration；失败记 0 不中断。
- 扫描并发：worker pool = 4；全程可 ctx 取消。
- 服务启动时：后台 goroutine 自动跑 Incremental；`/api/gallery` 在扫描中也可访问（返回已索引部分）。

## 4. 缩略图

- API：`GET /api/thumb/<path...>?w=300&h=300`（w/h 可选，默认 300，上限 1024）
- 生成：
  - 普通图片：imaging 打开 → Fit 缩放 → JPEG q85 存 `.pocketnas/thumb/<sha256(path)>.jpg`
  - HEIC：`ffmpeg -i in -frames:v 1 -vf scale=w:h:force_original_aspect_ratio=decrease out.jpg`
  - 视频：`ffmpeg -ss 1 -i in -frames:v 1 -vf scale=... out.jpg`（首秒帧）
- 缓存命中直接 `http.ServeFile`；生成失败返回 302 到内置占位 SVG `/static/placeholder.svg`（前端 agent 提供此文件）。
- LRU 淘汰：启动时检查 `.pocketnas/thumb/` 总大小 >500MB 则按 mtime 最旧删除至 80% 容量。
- 并发限制：缩略图生成信号量 = 2，防 OOM。

## 5. 相册 API

### GET /api/gallery?offset=0&limit=200&type=all|image|video
```json
{"total":1234,"items":[{"path":"/DCIM/a.jpg","name":"a.jpg","mimeType":"image/jpeg",
 "takenTime":1723692622,"width":4000,"height":3000,"duration":0,
 "thumbUrl":"/api/thumb/DCIM/a.jpg?w=300&h=300"}]}
```
按 taken_time DESC。鉴权沿用 M1 中间件（挂在 /api 下即可）。

### GET /api/gallery/scan  → `{"scanning":true,"indexed":123}` （扫描状态查询）

### GET /api/media/file/<path...>  原图/原视频流式访问（http.ServeContent，支持 Range）——灯箱看原图、M3/M4 播放都靠它。路径安全复用 files.Service 的 resolve 逻辑（可将 resolve 提升为导出函数 `files.Resolve(root, rel)` 供 media 包复用，重构 M1 代码时保持原行为与测试全绿）。

## 6. 前端（gallery.html + gallery.js + 共用 style.css）

- 入口：文件管理器页(index.html)顶部加 Tab 切换「文件 | 相册」→ gallery.html。
- 相册页：正方形网格（CSS grid，`aspect-ratio:1`，`object-fit:cover`），按 takenTime 倒序；滚动到底自动加载下一页（limit=200）；顶部显示「共 N 项 · 索引中…/已完成」（轮询 /api/gallery/scan）。
- 灯箱：点击缩略图全屏黑色遮罩，显示 `/api/media/file/<path>` 原图；支持：←/→ 键与左右滑动切换、Esc/点击背景关闭、双指/滚轮缩放（可简单实现：双击放大还原 + 按钮）。视频项在灯箱中用 `<video controls>` 播放。
- token 复用 localStorage pocketnas_token；图片/视频 URL 需要鉴权 → 用 fetch blob 转 objectURL 的方式加载（M1 已知限制，可接受）。
- 设计延续 M1 风格（浅色暖调、圆角）；灯箱为深色全屏。

## 7. M2 DoD

1. `go build ./... && go vet ./... && go test ./...` 全绿；`GOOS=windows go build` 通过（ffprobe 调用处注意 PATH 查找，Windows 下 ffprobe.exe）。
2. 单测：sqlite Store 增删查分页；扫描器对构造的测试目录（含 jpg 带 EXIF/无 EXIF、png、假 mp4）正确入库；taken_time EXIF 优先。
3. 集成（scripts/smoke-m2.sh 扩展或新脚本）：启动服务 → 等扫描完成 → /api/gallery 返回正确排序与分页 → /api/thumb 对 jpg 返回 image/jpeg 且尺寸 ≤300 → 视频（用 ffmpeg 现场生成 2s 测试 mp4）能出缩略图 → /api/media/file Range 206。
4. 扫描 1000 文件（脚本生成）< 60s。
