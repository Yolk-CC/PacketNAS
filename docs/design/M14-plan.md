# M14 — Client Android 改版计划

目标：将 PocketNAS Client 视觉与结构统一到 M13 服务端设计语言。

## 范围
1. 第四 tab「我的」：服务器管理（已保存服务器列表、添加/删除、当前连接、LAN 发现入口）迁入；关于/版本信息。
2. 查看器详情面板：底部上滑详情（文件名、时间、尺寸、EXIF——来自 /api/media 元数据）。
3. 视觉统一：暖色系设计令牌（浅色 #FAF8F5 底 / #B26F3F 主色；暗色暖灰），圆角卡片、统一字体层级、Tab 图标。
4. 底层未就绪功能留位不做（自动备份开关、回收站等仅入口或不做）。

## 阶段
- Stage 1：coder subagent 在 m14 分支实现（worktree 或 clone 到 $HOME）。
- Stage 2：主 agent 审查 + gradle 构建验证 + 合并 + 推 GitHub + tag v0.13.0。
- Stage 3：CI 验证 release 产物，报告用户。

## 参考
- docs/design/M13-server-web-design.md（设计令牌）
- PocketNAS_功能盘点与结构.md（IA 定义）
