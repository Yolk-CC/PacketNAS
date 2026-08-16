# PocketNAS — M4 SPEC（视频流式播放 + 多分辨率转码）

> 在 M3 基础上叠加。既有契约不变。Go：`export PATH=/tmp/go/bin:$PATH GOPATH=/tmp/gopath GOPROXY=https://mirrors.aliyun.com/goproxy/,https://goproxy.cn,direct GOSUMDB=off`
> （Go 若丢失：tar -C /tmp -xzf /mnt/agents/output/.toolchain/go.tgz）

## 0. 原则

- 转码 = ffmpeg 子进程 + 内存队列 + 磁盘缓存；**原画永远直出**（已有 /api/media/file 满足）
- ffmpeg 缺失时系统降级为"仅原画"，其余功能不受影响
- 安卓发热考量：转码 worker 默认 1 个（可配环境变量 POCKETNAS_TRANSCODE_WORKERS）

## 1. 后端模块

```
internal/transcode/
├── manager.go     # 任务队列、worker、去重、状态机
├── ffmpeg.go      # 命令构造与执行、进度解析
├── cache.go       # 转码产物缓存 + LRU
└── *_test.go
```

### 1.1 转码档位（固定三档 + 原画）

| 档位 | scale | 视频码率 | 音频 | 容器 |
|---|---|---|---|---|
| 360p | -2:360 | 800k | aac 96k | mp4 |
| 720p | -2:720 | 2000k | aac 128k | mp4 |
| 1080p | -2:1080 | 4000k | aac 128k | mp4 |

命令模板（faststart 保证可流式边下边播）：
```
ffmpeg -y -i <in> -vf scale=-2:<h> -c:v libx264 -preset veryfast -b:v <rate>
       -c:a aac -b:a <arate> -movflags +faststart <out.tmp> && mv <out.tmp> <out>
```
- 源高度 ≤ 目标档（如 480p 源求 1080p）→ 跳过该档，回退到不超过源分辨率的最近档逻辑：直接按 720p 档处理但 scale 不放大（scale=-2:min(h,srcH) 用表达式 `scale=-2:'min(720,ih)'`）
- 源无音频流 → 去掉 -c:a 参数（ffprobe 检测）

### 1.2 缓存与状态机

- 产物：`.pocketnas/transcode/<sha256(path+mtime+res)>.mp4`
- 状态：`none → queued → running → done | failed`（内存 map + 持久化到 index.db 新表 transcode_jobs(path,res,status,output,updated_at)，重启后 running/queued 重置为 none 可重新触发）
- 同一 (path,res) 任务去重；LRU：`.pocketnas/transcode/` > 2GB 按 mtime 淘汰至 80%

### 1.3 API（鉴权组内）

```
GET /api/video/<path...>?res=original|360p|720p|1080p
```
- res=original（默认）→ 等同 /api/media/file（ServeContent + Range）
- 转码档：done → ServeContent 产物（Range）；queued/running → `202 {"status":"running","progress":42}`；none → 入队并返回 `202 {"status":"queued"}`；failed → 409 + 错误信封
```
GET /api/video/status/<path...>?res=720p  → {"status":"running","progress":42}
```
- progress：解析 ffmpeg `-progress pipe:1` 的 out_time_ms / 总时长（ffprobe duration）得百分比
- 非视频文件 → 400

### 1.4 gallery items 增强
`/api/gallery` 视频项增加 `"resolutions":["360p","720p","1080p","original"]`（依据源分辨率计算可用档：源高>360 才有360p... 简化：始终返回全部档位 + original，前端展示全部即可）。

## 2. 前端（gallery.js 灯箱视频播放增强）

- 灯箱中视频项：控制条上加清晰度选择器（原画/1080p/720p/360p）
- 默认：原画。切换转码档时：先 GET /api/video/...?res=720p 不带 Range——收到 202 则显示「转码中 xx%」覆盖层并每 2s 轮询 /api/video/status，done 后重新 fetch 播放
- 失败（409）→ toast「转码失败，已切回原画」并回退
- 记住：fetch blob 模式，与 M2/M3 一致

## 3. M4 DoD

1. build/vet/test -count=1 全绿（含旧测试）；GOOS=windows 编译通过
2. 单测：命令构造（有/无音频、源高度小于档位）；状态机去重；缓存键
3. 集成（scripts/smoke-m4.sh）：ffmpeg 生成 5s 1280x720 测试视频 →
   - GET ?res=360p 首次 → 202；轮询 status 至 done；再 GET → 200 video/mp4，ffprobe 验证高度=360、可 Range 206
   - ?res=original → 200 且字节与源一致
   - 同一任务并发两请求只转一次（检查 .pocketnas/transcode 只一个产物）
   - 非视频 ?res=720p → 400
4. 转码过程不阻塞其他 API（转码中请求 /api/files 正常 200）
