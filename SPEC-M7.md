# SPEC-M7 — 多共享路径 + 设置界面

目标：用户可在设置页配置多个共享路径（name + path），客户端只能访问共享目录。
无共享配置时保持现状（单根模式，向后兼容）。

## 1. 配置存储（internal/settings）

- 文件：`<Root>/.pocketnas/settings.json`（0600），与 index.db 同目录。
- 结构：`{"shares":[{"name":"照片","path":"/storage/emulated/0/DCIM"}, ...]}`
- API：
  - `Load(root string) (*Store, error)`：不存在则返回空 Store（shares=nil → 兼容模式）。
  - `(*Store).Shares() []Share`（拷贝）
  - `(*Store).SetShares([]Share) error`：校验（name 非空、唯一、不含 `/` `\` 且 != "." / ".." / ".pocketnas"；path 存在且为目录，存 ResolveRoot 规范化后的绝对路径），原子写（tmp+rename）。
- Share JSON：`{"name":..., "path":...}`。

## 2. files.Service 改造

- `New(root)` 保持；新增 `(*Service).SetShares(shares []settings.Share)`（启动时 + PUT 设置后调用）。
- **共享模式**（len(shares)>0）：
  - `List("/", typ)`：返回各共享的伪目录项（Name=share.Name, IsDir=true, Mime="inode/directory", Size=0, ModTime=对应目录 mtime）。
  - resolve(rel)：第一段 = share name（URL 解码后），在 shares 中查找；找不到 → `not_found`。其余段沿用现有防穿越逻辑（Clean/..拒绝/EvalSymlinks/前缀校验，根变为 share.Path）。
  - `relPath(abs)`：输出 `shareName/sub/path`。
  - 上传/重命名/移动/删除/mkdir 全部走同一 resolve，天然受限；跨共享 move 允许（copyTree+删，同现有跨设备逻辑）。
- **兼容模式**（无 shares）：行为与现状完全一致。
- `Shares() []settings.Share` 供 media 扫描器使用。

## 3. 新 HTTP 端点（均在 auth 组内）

- `GET /api/settings/shares` → `{"shares":[...], "legacy":bool}`（legacy=true 表示未配置共享、整根暴露）。
- `PUT /api/settings/shares`，body `{"shares":[{name,path},...]}`：校验失败 → 400 `{"error":{"code":"invalid_share","message":...}}`；成功 → 保存 + `svc.SetShares` + 触发相册重扫描，返回新列表。允许传空数组回到兼容模式。
- `GET /api/system/browse?path=<abs>`：列出任意绝对路径下的**仅目录**项（{Name,Path}，按名排序，隐藏 . 开头），供目录选择器；path 省略 = 系统根（`/` 或 Windows 盘符列表）。非目录 → 400。

## 4. 相册/扫描器

- media 初始化改为接收 `func() []string`（当前共享根列表；兼容模式返回 [root]）。
- 扫描器遍历所有共享根；media_index.path 存**虚拟路径**（共享模式 `shareName/sub`，兼容模式同现状）。
- PUT shares 后异步触发一次全量重扫（可增量：删除已不存在共享的索引行）。

## 5. 前端（web/static）

- 新增 `settings.html` + `settings.js`，首页/相册页顶部导航加"设置"入口。
- 设置页：
  - 显示当前模式（未配置共享时提示"当前共享整个根目录"）。
  - 共享列表：名称、路径、删除按钮；"添加共享" = 名称输入 + 目录选择器（基于 /api/system/browse 的模态目录浏览器，逐层进入/返回上级/选择当前目录）。
  - 保存按钮 → PUT；成功后提示。
- `app.js` 文件列表适配：`/` 下展示共享伪目录（后端已返回，前端可能无需改动——验证 breadcrumbs、上传目标、下载 zip 在共享模式下正常即可）。

## 6. Android

- mobile.Start 签名不变；默认兼容模式（整根）。用户通过 Web 设置页配置共享。
- APK 无需改动（settings.json 位于 Root/.pocketnas）。

## 7. 验收

- `go test ./...` 全绿；新增测试：settings Load/SetShares 校验、共享模式 resolve（含越权/`..`/不存在共享）、List("/")、browse 端点。
- 冒烟脚本 `scripts/smoke-m7.sh`：起服务 → PUT 两个共享 → GET /api/files?path=/ 只见两个共享名 → 访问共享外路径 404/403 → PUT 空数组恢复兼容模式。
