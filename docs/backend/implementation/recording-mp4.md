# MP4 后台转换

访问记录中的“生成 MP4”提交持久化任务，HTTP 请求结束或浏览器关闭不影响转换。返回访问记录后重新查询服务器进度，完成后显示 MP4 大小和下载链接。支持取消、失败重试；排队任务立即取消，正在运行的任务通过租约心跳取消。

## 服务与数据

- 数据库迁移 031：`recording_exports` 保存会话、任务、进度、重试次数、文件大小和 SHA-256；`jobs` 使用 `recording.mp4` 类型。
- Go Worker 受维护模式统一管理，领取任务后每 5 秒续租。源录屏先校验完整性，在数据库事务中生成一致的 JSONL 快照。
- 独立 `mp4-renderer` 服务运行 Chromium、xterm/Guacamole 回放和 FFmpeg，生成 H.264、1280×720、20 fps、yuv420p MP4。按录屏时间取帧，不依赖浏览器实时录制。
- 转换服务只加入 Docker 内部网络，不发布宿主端口；独立随机 Bearer 令牌、非 root、只读根文件系统、删除全部 capabilities。禁止页面外部请求和 Service Worker。临时目录 768 MiB，内存 2 GiB，CPU 2 核，PID 上限 160。
- 成品存储在服务端 `NT_MP4_DIRECTORY`。完成时检查任务租约、取消状态，原子发布并保存摘要。下载检查原有会话权限和摘要，不公开存储路径。

## API

| 方法 | 地址 | 用途 |
|---|---|---|
| GET | `/api/sessions/{id}/recording/mp4` | 状态、可用性、进度、大小 |
| POST | 同上 | 创建任务，重复提交复用正在执行或已完成的任务 |
| DELETE | 同上 | 取消排队或执行中的任务 |
| GET | `/api/sessions/{id}/recording/mp4/download` | 校验后下载 MP4，支持 HTTP Range |

会话本人或具有 `audit.read` 的用户才能访问。提交、取消、完成和下载均写入审计事件。

## 限制与恢复

每实例单 Worker 顺序执行，最多 8 个排队/运行任务；原始录屏最多 100 MiB、2 小时，输出最多 256 MiB，单任务最多 4 小时。服务正常退出/维护时重新排队；租约丢失最多自动尝试 3 次，从头转换，不是帧级断点续传。失败后可手动重新生成。

成品缓存保留 7 天，并以 1 GiB 为清理阈值；达到阈值时优先清理旧成品。原始录屏不受此缓存清理影响。访问记录被保留策略删除时取消相关任务、级联删除导出元数据，清理孤立文件。源录屏仍在时可重新生成过期缓存。

MP4 是派生成品，不进入 R11 备份归档。恢复时清理导出元数据；支持从 schema 30/31 归档迁移到 31。业务录屏仍按 R11 原有备份范围处理。

## 部署

`scripts/start-backend.ps1` 自动生成本地 `NT_MP4_TOKEN`，启动转换容器并配置 API。独立部署需设置 `NT_MP4_URL`、至少 32 字符的 `NT_MP4_TOKEN`、可写的 `NT_MP4_DIRECTORY`。令牌只保存在私有配置中，不暴露给前端。

验收命令：`node --test test.mjs` 在 renderer 镜像内验证实际终端和图形编码；Go `TestMP4DurableJobPermissionsDedupeDownloadAndRetry` 验证权限、队列、下载、校验、恢复和清理；设置 `NT_MP4_TEST_URL`、`NT_MP4_TEST_TOKEN` 后执行 `TestMP4RealRendererAfterClientDisconnect` 验证真实服务端转换链路。
