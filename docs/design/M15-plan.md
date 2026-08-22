# M15 — 审核问题整改计划

依据 docs/design/工程审核报告.md。

## M15a（Go 服务端，分支 m15a，仅改 internal/ 与必要测试）
- P0-1：Move 拒绝移动到自身/自身子目录（destAbs == srcAbs 或 HasPrefix(destAbs, srcAbs+sep) → 400）
- P0-2：缩略图缓存键加入 w×h 与源文件 mtime
- P1-3/4：zip.go WalkDir 回调内显式 Close（去掉 defer），错误记日志
- P1-5：faces service.go 锁内判 engine nil
- P1-6：faces handlers.go PUT models 恒真条件 → 仅 req.LibPath != "" 时覆盖；删死代码
- P1-7：对客户端返回通用错误文案，详细错误进日志（server.go:310/384、files/handlers.go、faces 503 reason）
- 每项配单测（Go 测试）

## M15b（Android，分支 m15b，仅改 android/ 与 client-android/）
- P1-8：ViewerActivity.onDestroy 兜底 releaseAll
- P1-9：ViewerActivity fetch 改流式（复用 DownloadHelper 思路），删整读内存
- P1-10：ViewerPagerAdapter 精确 removeCallbacks(runnable)，bind/回收时取消 pending runnable
- P1-11：共享单例 OkHttpClient（App/PlayerManager 注入），切服不再 new
- P1-12：NasService 启动加 starting 标志防双实例
- 纯逻辑改动配单测；assembleDebug + testDebugUnitTest 必须过

## M15c（工程基建，主 agent 直接做）
- 根 .gitignore 补 dist/、*.aar、.pocketnas/ 等；工作区删 dist/
- 删除 22 个已合并本地分支；GitHub 侧分支清理
- 新增 ci.yml：push/PR 跑 go test/vet + client 单测 + M11 smoke 转必选（如成本低）
- release.yml：tag 驱动统一注入 versionName/versionCode 到两个 APK
- dist 历史清理（filter-repo）属破坏性操作，最后单独确认

## 发布
合并 m15a+m15b → 构建验证 → tag v0.13.1 → CI 验证。
