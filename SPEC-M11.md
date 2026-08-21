# SPEC-M11 — 服务端人脸识别 V1（可迁移 + 多模型）

目标：服务端自动识别人脸、聚类成人物、可命名；数据可导出导入迁移；模型可替换（标准 ONNX）。

## 1. 模型与引擎（internal/faces/engine.go）

- **模型文件**：不打包进二进制，放 `<Root>/.pocketnas/models/`：
  - 检测模型（默认 SCRFD `det_10g.onnx`，InsightFace buffalo 系列）
  - 特征模型（默认 `w600k_r50.onnx`，输出 512 维向量）
  - 模型由用户从设置页下载（内置下载 URL 常量，放 github release/直链）或手动放入目录；设置页可切换同目录下任意 .onnx
- **推理后端**：`github.com/yalue/onnxruntime_go`（purego 动态加载 .so，不破坏交叉编译；构建保持无 CGO）。onnxruntime 原生库同样放 models 目录或系统路径：
  - Linux: libonnxruntime.so；Windows: onnxruntime.dll；Android: 从 app nativeLibraryDir 传入路径（mobile.Start 增加可选参数，缺省跳过）
  - 库不存在/加载失败 → 人脸功能整体优雅降级：API 返回 `{"available":false,"reason":...}`，其余功能不受影响
- **Engine 接口**（多模型兼容的核心抽象）：
  ```go
  type Engine interface {
      Detect(img image.Image) ([]Face, error)      // Face{Box, Landmarks[5], Score}
      Embed(img image.Image, f Face) ([]float32, error) // 对齐裁剪+特征，维度随模型
  }
  ```
  ONNX 实现按"输入名/输出名/预处理参数"配置化（内置 buffalo_l/buffalo_s/MobileFaceNet 三档 profile），新增模型=加 profile。
- 图像解码复用现有 media 包的解码/缩略图逻辑（含 EXIF 旋转校正——必须对齐，否则框偏）。

## 2. 数据（internal/faces/store.go）

- SQLite `<Root>/.pocketnas/faces.db`：
  - `faces(id, file_hash TEXT, box_json, embedding BLOB, person_id, cluster_id)`
  - `persons(id, name TEXT, cover_face_id, created_at)`
  - `file_hash` = 媒体文件 sha256（与缩略图缓存同键，保证可迁移）
- 识别队列：相册扫描完成后（或 /api/gallery/scan 触发后）异步对未识别图片入队；worker=1（手机 CPU 友好），进度可查
- 聚类：embedding 余弦相似度 ≥0.5（profile 可调）增量归入已有 cluster，否则新建；cluster 聚合为 person（未命名 person = cluster）
- 命名/合并：修改 persons 表 + 回写 faces.person_id

## 3. API（auth 组内，/api/faces 命名空间）

- `GET /api/faces/status` → `{available, reason?, model:{det,rec}, queue:{pending,done}, persons, facesTotal}`
- `POST /api/faces/models/download` → 后台下载默认模型+运行库到 models 目录（进度轮询 status）
- `PUT /api/faces/models` → 切换模型 profile `{detModel, recModel}`（重切模型且维度变化→标记需重建索引）
- `POST /api/faces/scan` → 触发/继续识别队列
- `GET /api/faces/persons` → `[{id,name?,faceCount,coverUrl}]`（coverUrl 调下方 face crop 端点）
- `GET /api/faces/persons/<id>/photos` → 该人物全部媒体（复用 gallery 项格式）
- `PUT /api/faces/persons/<id>` `{name}` 命名；`POST /api/faces/persons/merge` `{from,to}`
- `GET /api/faces/crop/<faceId>` → 人脸小图（按 box 从原图裁剪，缓存）
- **迁移**：
  - `GET /api/faces/export` → 单个 JSON：`{version:1, modelRec, dims, persons:[...], faces:[{fileHash,box,embedding,personId}]}`（gzip）
  - `POST /api/faces/import` → 导入后按 fileHash 匹配现有媒体：命中=直接挂人物，未命中=保留记录待文件出现后生效；**不重新识别**
- 模型/库未就绪时以上端点（除 status/download）返回 503 `{"error":{"code":"faces_unavailable"}}`

## 4. Web 设置页 + 人物页（web/static）

- 设置页加"人脸识别"卡片：状态（可用/不可用+原因）、模型选择与下载按钮、识别进度条、"开始识别"按钮、导出/导入按钮
- 新 people.html：人物网格（封面+名字+照片数），点入看该人物全部照片（复用 gallery 网格样式），未命名人物可命名、两两合并
- 导航加"人物"tab

## 5. Android

- mobile.Start 增加 onnxLibPath 参数（从 context.nativeLibraryDir 传入）；android/ 打包 onnxruntime-mobile 的 .so（CI 下载 onnxruntime-android aar 解出 jni/arm64-v8a/libonnxruntime.so 放入 aar 的 jni 目录或 app jniLibs）
- 若打包复杂度超预期：Android 端允许 faces_unavailable 降级，先在桌面端验证全链路，Android 打包列为已知遗留（报告中说明）

## 6. 验收

- 单测：store CRUD、聚类逻辑（用合成向量）、导出导入往返、API 降级行为
- 集成冒烟 scripts/smoke-m11.sh：用 ONNX 测试小模型（CI 下载 buffalo_s）对 3 张含人脸测试图（2 张同一人）识别→聚类为 2 人物→命名→导出→重建库导入→人物关系保留
- CI 增加人脸模型下载缓存 + smoke-m11 执行（仅 linux job）
- go test ./... 全绿

## 7. 明确不做
- 不做实时识别、不做视频人脸、不打包模型进二进制
