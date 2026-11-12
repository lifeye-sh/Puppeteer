# 访问记录、录屏下载和保存策略

2026-10-29，版本 r10.4-preview，数据库 schema 29。

## 前端入口

- `/access-log`：显示操作时长、录屏大小和“下载录屏”，移除结束原因列及详情中的结束原因文本。
- `/system-status` → “访问记录保留策略”：默认 31 天，管理员可设为 1–3650 天；保存采用 revision 检查，避免覆盖其他管理员刚更新的配置。

操作时长从实际 `connectedAt` 计算至 `closedAt`，在线会话显示至当前时间。它是连接持续时间，包含空闲时间，不是按键活跃时间；未连接成功的会话显示“未连接”。

录屏大小是服务端保存的原始事件数据字节数。下载为 `.ntrec.jsonl` 原始录屏归档，包含时间戳、事件类型及 Base64 数据，归档大小与原始数据大小不同，页面提供提示。支持 SSH/Telnet 终端和 RDP/VNC 图形事件；不是转码后的 MP4。

## 下载契约

`GET /api/sessions/{id}/recording/download`：当前用户自己的会话，或具有 `audit.read` 权限的用户可下载。只有会话列表权限不允许下载他人录屏。

- 录制中、空录屏和完整性校验失败返回 409；未认证 401，越权 403，记录已清理 404。
- 下载前校验既有录屏完整性；逐页输出，不将全部录屏载入服务器内存。
- Content-Type 为 `application/x-ndjson`，Content-Disposition 为 attachment，Cache-Control 为 no-store。
- 第一行 `type=header`、`archiveVersion=1`，包含协议、原始格式、事件数量和原始字节数；事件行保留 sequence、elapsedMs、kind、payload；最后一行 `type=end`。payload 为 Base64，保留原始二进制数据和拆分的 UTF-8 字节。
- 前端检查归档结束标记后再保存；完整下载记录 `recording.downloaded` 操作审计。
- 下载持有录屏读取锁，清理取得同一录屏排他锁后才删除块，避免导出中途删块。

## 自动清理

每分钟最多处理 25 条超期已结束会话，单批事务超时 20 秒，失败整体回滚，后续继续尝试。按数据库中的会话结束时间起算保存天数；配置行共享锁保证单次清理使用一致的保存策略。多实例通过行锁和 SKIP LOCKED 避免重复处理。

清理实际删除 `access_logs`、`recordings`、`recording_chunks`、`session_commands`，随后从访问列表、详情及录屏接口隐藏该会话。为独立文件操作和分享审计的外键完整性保留技术 session 引用，并清空会话认证哈希、IP 和结束原因；独立文件审计、系统操作审计不随访问记录策略删除。在线会话不清理。

缩短天数会在后续清理批次删除超期数据；调大天数不能恢复已删除内容。配置页明确提示先下载需要保留的录屏。

接口：`GET /api/admin/access-retention` 需要 settings.read；`PUT /api/admin/access-retention` 需要 identity.manage，请求 `{days,revision}`，成功 204，值不合法 422，版本冲突 409。配置变更产生 `access.retention.updated` 审计。

## 自动验证

- `TestRecordingReplayPaginationPermissionsAndStates`：130 个事件的完整归档、二进制内容一致性、响应头、下载权限、损坏录屏拦截及原回放回归。
- `TestAccessRetentionSettingsAndCleanup`：默认 31 天、非法天数、权限与版本冲突、32 天过期记录删除、30 天记录保留、在线会话保留、改为 7 天后执行清理、导出锁与清理冲突回滚。
- 日志：`backend/.local/access-retention-tests.log`、`access-retention-full-tests.log`、`access-retention-ui.log`、`access-retention-build.log`。

## 部署后验收（2026-10-30）

- r10.4-preview / schema 29 已部署，后端和数据库健康。
- 全量 Go 测试及 go vet 通过；应用集成测试耗时 316.275 秒。TypeScript 检查与 Vite 构建通过（存在既有大包提示）。
- 管理员登录后，访问记录页面确认显示操作时长、录屏大小、下载按钮，未显示结束原因。
- 实际下载返回 200、attachment 和 application/x-ndjson；样本 2 个事件全部导出，结束标记完整。页面下载按钮已点击，服务端产生成功下载审计。
- 配置页保存修改成功并产生新 revision；验收后恢复 31 天，重新加载页面确认输入值为 31。

## MP4 下载（2026-10-30）

访问记录的下载按钮已改为“下载 MP4”。浏览器读取通过服务端完整性校验的原始录屏，在隔离画布上还原终端或 Guacamole 图形输出，再使用浏览器 MP4 编码器生成视频；不连接远端。原始 JSONL 接口继续作为受权限保护的数据源。

转换输出固定 1280×720，保持图形比例，按原始时长编码；有进度及取消操作，离开页面取消并释放画布、播放器、媒体轨道和临时 URL。页面进入后台则停止并提示重试，避免后台节流造成静默丢帧。浏览器不支持 MP4 时明确报错，不将 WebM 改扩展名。单次最长 2 小时、生成文件最多 256 MiB，无音轨。

验证：真实 SSH 录屏已生成 MP4；图形协议测试录屏生成 9018 字节 MP4，经浏览器视频解码确认 1280×720、时长 1.505 秒。TypeScript 检查通过。下载视频体积与列表中的原始录屏存储大小不同。

### 真实 RDP 页面转换回归

2026-10-30：在访问记录中点击真实 RDP 会话的“下载 MP4”，先取消，再从同一按钮重试成功。生成 MP4 5,190,113 字节，浏览器解码确认 1280×720、44.436467 秒。抽取第 5 秒画面可解码且具有 734 种采样颜色，排除纯空白帧。转换完成后临时渲染容器数量为 0，页面进度提示已清除。测试使用临时 Blob 观察钩子读取文件元信息，已还原浏览器 URL 方法并清除临时 Blob 引用。
