# M07 文件与共享存储

为获准会话和共享存储提供受控文件操作。

状态：全量设计任务说明，模块未整体完成；阶段实现以[逐轮验收记录](../migration-rounds.md)为准。业务分组：资源与连接。首次交付批次：W2。目录：`backend/internal/modules/files`。

## 开发范围

- 文件系统挂载、目录列表、创建、删除、重命名和权限修改
- SFTP、共享存储、图形会话驱动目录适配
- 上传任务、下载、批量下载、文本编辑与冲突检测
- 容量、预留空间、ACL 与临时文件回收

## 本模块不负责

- 不管理脚本片段，脚本属于 M12
- 底层对象存储驱动属于 F00
- 不能利用 sessionId 访问任意平台磁盘路径

## 交付切片

- 首次交付：会话 SFTP、上传下载、编辑与容量控制。
- 后续交付：W3 共享存储、图形驱动目录与批量文件能力。
- 完整范围以本卡功能清单为准，首次批次不代表所有扩展功能已经完成。

## 数据所有权

- `filesystem_mounts`
- `upload_tasks`
- `storages`
- `storage_acl`

仅本模块的 Repository 可以更改这些表。跨模块操作通过公开用例接口；共享事务由 application/workflows 编排，不直接传出数据库连接。

## 对外契约

- Filesystem.Mount/List/Mutate
- Upload.Create/Transfer/Cancel
- FileAccess.Check
- Storage.Reserve/Release

这些是待实现的端口草案，不是已完成的 Go API。请求类型、错误、幂等键和授权前置条件必须在编码前补齐。

## 事件

- FileOperationCompleted
- UploadCompleted
- UploadFailed
- StorageQuotaExceeded

事件 payload 仅包含必要 ID、revision、状态与脱敏信息。消费者不得把事件中的旧授权作为当前授权。

## 开发前置与集成

- [M02 权限与访问策略](M02-authorization.md)
- [M06 远程会话](M06-session.md)

前置表示开发顺序；运行时通过端口和应用层注入协作模块，不表示可绕过身份/权限检查。身份与权限同批次集成验收。涉及的跨模块流程见[模块开发规划](../module-development-plan.md)。

## 前端契约证据

- [src/api/filesystem-api.ts](../../../src/api/filesystem-api.ts)
- [src/api/storage-api.ts](../../../src/api/storage-api.ts)
- [src/pages/access/upload/upload-manager.ts](../../../src/pages/access/upload/upload-manager.ts)

同一个前端 API 文件可能聚合多个模块，不据文件名称判断所有权；每条路由的所有者在 OpenAPI operationId 与模块注册表中固定。

## 使用文档补充需求

来源与版本差异见[文档核对说明](../usage-docs-review.md)。下列仍是待开发要求。

### U14 两类文件系统与上传状态

- 阶段：W2/W3；协作：M02、M06、M08、M18。
- 功能要求：SSH 使用目标 SFTP；浏览器 RDP 使用共享存储或操作者默认存储。上传区分准备、浏览器上传、后端传输、成功/失败，提供取消和可安全重试。
- 验收条件：99% 浏览器上传不算成功；新驱动配置需新会话；并发上传和映射盘写入均执行字节配额；编辑/目录上传/批量下载单列能力门控。
- 来源：[文件管理](https://www.next-terminal.com/zh/docs/usage/file-management.html)、[Windows / RDP 资产](https://www.next-terminal.com/zh/docs/usage/rdp.html)、[SSH 资产](https://www.next-terminal.com/zh/docs/usage/ssh.html)。

协作项：U05（主责 M02）、U12（主责 M06）、U15（主责 M08）、U28（主责 M16）、U31（主责 M18）、U32（主责 M18）。

## 已确定的实施规范

先读[实施决策与开发门槛](../implementation-decisions.md)。本模块参与的接口、状态机、依赖和实机验收：

- [implementation/native-rdp.md](../implementation/native-rdp.md)
- [implementation/backup-restore.md](../implementation/backup-restore.md)
- [implementation/settings-entitlements.md](../implementation/settings-entitlements.md)

## 验收条件

- 路径和符号链接边界经过测试
- HTTP 上传成功意味着目标落盘成功
- 取消与完成竞态不会删除最终文件
- 所有通道的上传/下载能力与会话策略一致

同时满足[统一交付标准](../module-development-plan.md#统一交付标准)。

---

由 `node scripts/backend-module-docs.mjs` 根据 `module-catalog.json` 、usage-requirements.json、usage-docs-sources.json 和脚本中的交付切片生成。修改功能、表归属、端口和验收项应先修改目录 JSON，再重新生成。
