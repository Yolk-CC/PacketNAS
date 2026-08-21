# SPEC-M9 — pocketnas-client 原生 Android 相册（骨架）

全新原生 App，位置 `client-android/`（与 android/ 并列）。不含人脸（M11）、不含文件夹 tab（M10）。
本里程碑目标：**能连服务器、像主流相册一样刷时间线、看图、播视频和 Live Photo**。

## 1. 技术栈与工程

- Kotlin + View 体系（RecyclerView/ViewPager2），minSdk 26 / targetSdk 34，Gradle Kotlin DSL
- 依赖（全部 Apache/MIT）：Coil 2.x（图片）、ExoPlayer Media3（视频）、PhotoView 2.3（缩放）、OkHttp（API）、Moshi 或 kotlinx-serialization（JSON）
- applicationId `com.pocketnas.client`，应用名 "PocketNAS"
- 包结构：`data/api`（API 客户端）、`data/model`、`ui/server`（连接页）、`ui/timeline`、`ui/viewer`、`player`
- CI：release.yml 增加 client APK job（产物 `pocketnas-client-debug.apk`）；与 server job 共享 setup 步骤

## 2. 连接服务器（ui/server）

- 首启进入连接页：手动输入 `host:port` + 密码（可选）；"扫描局域网"按钮：向 255.255.255.255:45777 发 UDP `POCKETNAS_DISCOVER`，2 秒窗口收集 `POCKETNAS_HERE|name|port|apiLevel` 回复，列表选择
- 连接验证：GET /api/system/info（校验 apiLevel>=2）；有密码时 POST /api/auth/login 取 token
- 保存多服务器列表（SharedPreferences/Room 轻实现），可切换、可删除；token 按服务器记忆
- 全部 API 请求带 `X-Auth-Token`

## 3. 相册时间线（ui/timeline）——主界面

- 数据源：`GET /api/gallery?offset=&limit=&type=all`（现有契约，照片+视频混合，服务端已按时间倒序）
- UI：网格 3 列（可双指捏合切 3/4/5 列），**按日期分组带粘性日期头**（yyyy-MM-dd 或本地化"今天/昨天"），右侧快速拖动条（fast scroller，拖动时显示年月气泡）
- 缩略图：`GET /api/thumb/<path>`（path 为服务端返回的虚拟路径，逐段 encode），Coil 加载 + 内存/磁盘缓存，网格复用滚动流畅（预取、placeholder）
- 视频项右下角时长角标；Live Photo 项（服务端 `livePhoto:true` 字段，若无则 M9 先用 media/file 类型推断，缺字段就跳过角标）加"动态"角标
- 下拉刷新触发 `GET /api/gallery/scan` 后重载；顶部显示服务器名
- 增量分页滚动加载（offset/limit）

## 4. 查看器（ui/viewer）

- 点击进入，ViewPager2 左右滑动切换
- 图片：PhotoView 双指缩放/双击放大；加载原图 `GET /api/media/file/<path>`（先显缩略图再渐进替换）
- 视频：ExoPlayer 播放 `/api/media/file/<path>`（Range 支持服务端已有）；长按可选转码档位调 `/api/video/<path>?res=720p`（M9 可只做直放，转码留 M12）
- Live Photo：进入时显示照片，长按照片播放动态部分 `GET /api/livephoto/<path>`（ExoPlayer 小窗覆盖播放），与 iOS 相册交互一致
- 顶部：返回、文件名、拍摄时间（gallery 项已含）；底部操作：分享（系统 share sheet，用 FileProvider 缓存后分享）、下载到本机相册（MediaStore 插入）、删除（DELETE /api/files）
- 沉浸式：点击切换系统栏显隐

## 5. 边界与质量

- 空态/加载态/错误态（服务器不可达、401 重新输密码）
- 大图 OOM 防护：Coil 自动 downscale；原图加载限定屏幕分辨率 2 倍
- 横竖屏切换不丢状态（ViewModel）
- 深色模式跟随系统
- 自测：`./gradlew :app:assembleDebug` 本地无法跑（无 SDK）——保证静态正确，CI 验证；关键逻辑（日期分组、发现协议解析、API 客户端）写成纯 Kotlin 单元测试（JUnit，Robolectric 可选，能跑 `./gradlew testDebugUnitTest` 更好，不强制）

## 6. 验收（CI 为准）

- CI client job 绿，产出 pocketnas-client-debug.apk
- 代码评审点：API 契约全部使用现有服务端端点；不引入 GPL 依赖；包名/产物名与 CI 一致
