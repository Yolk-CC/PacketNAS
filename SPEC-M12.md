# SPEC-M12 — client 人物 tab

在 client-android 增加"人物"tab，对接 M11 服务端 /api/faces/*（以 internal/faces/handlers.go 实际代码为准，先读！）。

## 1. 结构
- 底部 tab 变为：相册 / 人物 / 文件（设置占位可保持 Toast）
- PeopleFragment：人物网格（2-3 列，圆形人脸封面 + 姓名/未命名 + 照片数）
- PersonPhotosActivity：某人物的全部照片（复用时间线网格样式 3 列），点击进现有 viewer（传该列表）

## 2. 数据源（读服务端代码确认契约）
- `GET /api/faces/status` → available 判断；不可用时显示空态（原因 + 提示"请到服务器 Web 设置页下载模型并开始识别"）
- `GET /api/faces/persons` → [{id, name?, faceCount, coverUrl}]（coverUrl 或 coverFaceId 以实际为准）
- 封面图：走 status 里给的 crop 端点（如 /api/faces/crop/<faceId>），Coil 加载（自动带 token）
- `GET /api/faces/persons/<id>/photos` → gallery 项格式，直接映射 MediaItem
- `PUT /api/faces/persons/<id>` {name} 命名（长按或右上角"命名"）
- `POST /api/faces/persons/merge` {from,to}：人物网格进入选择模式（长按进入），多选两个后出现"合并"按钮，方向：合并到先选中的（或弹窗让选目标，选实现简单的）
- 下拉刷新（重新拉 persons）

## 3. 交互细节
- 未命名人物显示"人物 N"样式占位名
- 命名对话框：EditText 弹窗
- person photos 页标题=人物名，空态处理
- 全部沿用现有 ApiClient/Coil token 注入/错误 toast 模式

## 4. 测试
- ApiClientTest 增加 faces 端点契约用例（MockWebServer）
- 纯逻辑（如合并选择集）尽量复用 SelectionSet

## 5. 验收
- CI client job 绿（assembleDebug + testDebugUnitTest）
- 不碰 server 端文件
