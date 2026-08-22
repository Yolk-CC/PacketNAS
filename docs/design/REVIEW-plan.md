# 工程审核计划（v0.13.0 基线）

目标：对 PocketNAS 全工程做架构 + 代码规范审核，输出分级问题清单与整改建议。

## 并行审核分工（3 个 reviewer subagent，只读）
1. **Go 服务端**：internal/ 全部模块（server、faces、db、auth、scanner 等）、cmd/、go.mod 依赖、web 静态资源嵌入；关注并发安全、错误处理、SQL 注入/路径穿越等安全、接口一致性。
2. **Android 两端**：android/（server APK + gomobile 绑定）与 client-android/；关注生命周期、协程泄漏、内存（Bitmap/ExoPlayer）、Manifest/权限、硬编码、ViewBinding/空安全。
3. **架构与工程规范**：整体分层、模块边界、API 契约一致性（server ↔ client ↔ web）、git 仓库卫生（提交体积、分支）、CI 工作流、文档与 SPEC 一致性、依赖许可证风险。

## 输出
- 每个 reviewer 输出：问题清单（P0 阻塞 / P1 建议尽快修 / P2 改进项），每条带文件:行号与理由。
- 主 agent 汇总为审核报告 docs/design/工程审核报告.md，给出整改优先级路线图。
