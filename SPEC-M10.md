# SPEC-M10 — client 文件夹浏览 tab

在 client-android 增加"文件"tab，按文件夹浏览服务器共享目录（API 全部为现有契约）。

## 1. 结构
- MainActivity 改为底部三 tab（Material BottomNavigationView）：相册（现有 TimelineActivity 内容迁入 Fragment 或保持 Activity 跳转而 Main 仅作入口——选改动最小方案：保持 TimelineActivity 为主页，顶部 toolbar 增加"文件"入口图标 + 文件页内可返回；或引入单 Activity + 2 Fragment。**决定：用单 Activity + ViewPager2/Fragment 底部 tab**，把 TimelineActivity 重构为 TimelineFragment；若重构风险大则退回入口图标方案，报告中说明选择）
- 文件 tab = FilesFragment

## 2. FilesFragment
- 数据源：`GET /api/files?path=<p>&type=all`（共享模式下 "/" 返回共享伪目录，沿用 M7 契约）
- UI：RecyclerView 列表（目录📁/图片🖼️/视频🎬/文件📄 图标、名称、大小、修改时间），顶部面包屑（可点击逐级回退，根显示"共享"）
- 点击目录进入；点击图片/视频 → 跳现有查看器（仅传当前目录媒体列表）
- 长按多选：下载（批量 zip：`GET /api/download/<path>?archive=zip` 或逐个下载到 Download 目录）、删除（DELETE /api/files，确认弹窗）
- 新建文件夹（POST /api/mkdir）、上传（系统文件选择器，multipart POST /api/upload?path=）、重命名（POST /api/rename）——操作按钮放 toolbar/浮动按钮，沿用 Web UI 的 API 契约（以 internal/files/handlers.go 实际代码为准）
- 下拉刷新；空态/错误态

## 3. 验收
- CI client job 绿（assembleDebug + testDebugUnitTest）
- 面包屑/路径编码（中文、空格）正确；多选态操作可用
- 纯逻辑（路径栈、选择集管理）写单测
