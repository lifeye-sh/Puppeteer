# R10 第一阶段：命令片段

2026-10-29，已部署 `r10.1-preview`，数据库 schema 26，能力 revision 13。
本阶段完成命令片段；不代表 R10 的任务调度和凭据轮换已完成。

## 前端验收入口

- `/snippet`：系统命令片段。所有登录用户可查看；拥有 `snippet.manage` 权限的用户可新建、编辑、删除。
- `/x-snippet`：当前用户的个人片段。其他用户（包括管理员的个人接口）不能读写。
- SSH 终端右侧“命令片段”：搜索、预览、插入命令编辑框。保持原始多行内容，不发送终端字节，不触发执行请求。
- 用户检查命令后点击“执行命令”，复用现有会话权限、动作策略、命令过滤、审计和退出码处理。独立执行不继承交互终端当前目录，沿用 10 秒超时。
- Telnet / RDP 不提供此 SSH 执行入口。

系统中保留 `R10 内网诊断示例`，内容为 `pwd` 和输出 `R10_SNIPPET_OK` 的 printf 命令，可在内网测试资产上验证。

## 服务与数据

- `internal/modules/automation/snippets.go`：M12 片段存储，按所有者过滤后分页，每页 30 条；名称/说明不区分大小写搜索，SQL 参数化。
- `GET /api/snippets?scope=mine|system|all&page=1&search=...`：默认个人与系统合集。
- `POST /api/snippets`、`PUT/DELETE /api/snippets/{id}`：个人维护；删除携带 `?version=N`。
- `POST /api/admin/snippets`、`PUT/DELETE /api/admin/snippets/{id}`：系统维护，独立权限。
- 新增迁移 `026_snippets.sql`。系统所有者为 NULL；客户端不能通过请求字段变更所有者或将个人片段发布为系统片段。
- 修改/删除使用乐观锁；数据变更和操作审计在同一事务提交。操作日志只保存片段 ID / 类别，不复制命令正文。
- 内容限制为 4096 UTF-8 字节，不允许 NUL 和空白正文。片段为明文命令模板，不是凭据保险库。

## 已验证

- `go test ./internal/application -run TestR10 -count=1 -timeout=3m`：通过，6.614 秒。
- `go vet ./...`：通过。
- TypeScript 检查、Vite backend 构建：通过。
- API 集成测试：个人隔离、普通用户不能维护系统片段、跨个人/系统接口不能修改归属、两次并发编辑仅一次成功、旧版本删除拒绝、审计不泄露正文、UTF-8 字节限制、非法页码、未登录拒绝、分页和搜索。
- 浏览器：从系统片段页面新建多行示例，保存成功；管理员连接内网 `ssh-fixture:2222`，通过工具箱插入示例，草稿逐字符保持一致。
- 插入之后：浏览器执行接口调用数 0，数据库匹配示例的命令记录数 0。
- 手动点击执行之后：接口调用数 1，命令审计 `succeeded / exit_code=0 / explicit-exec`，前端显示成功且草稿清空。
- 部署前活跃会话数 0；保留 schema 结构快照 `backend/.local/pre-r10-schema.sql`（不是完整数据备份）。

日志保存在忽略目录 `backend/.local/r10-snippets-{tests,ui,build}.log`。

## R10 剩余开发

1. 手动/cron 调度、时区及下一次运行预览、重叠和漏跑策略。
2. Worker 领取租约、调度去重、取消/超时、日志和逐资产结果、执行前重新授权。
3. 密码/密钥轮换候选版本、逐资产验证、部分失败处理、结果不明的只读核对与人工恢复。
4. 对应前端任务/轮换页面及内网故障注入验收。

`automation.jobs` 仍为 `not_implemented`。只新增 `automation.snippets=available`，不会把未完成能力展示为已完成。
