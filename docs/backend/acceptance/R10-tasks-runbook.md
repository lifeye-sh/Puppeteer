# R10 第二阶段：SSH 任务调度

实现版本：`r10.2-preview`，schema 27，能力 revision 14。验收结果见下文；凭据轮换仍属 R10 未完成部分。

## 入口与使用

前端 `/scheduled-task`，需要 `task.manage` 权限。普通操作员只管理自己的任务和运行记录；管理员可查看所有记录并请求取消，但不能直接借用他人的任务身份触发或修改任务。

新建任务按“命令与资产”“调度与超时策略”分组，支持从系统/个人命令片段填入命令。保存的是当次命令文本，不会随片段后续修改而改变。

- 每个任务最多选择 20 台获授权 SSH 资产，按定义顺序执行；每台资产超时 1–600 秒。
- Cron 留空为仅手动执行；定时采用五段 cron，最短周期一分钟。手动运行与自动调度启停独立。
- IANA 时区和未来十次运行预览使用同一解析器。运行时间以 UTC 时刻去重；DST 跳过不存在的本地时间，重复本地时间按不同 UTC 时刻分别调度。
- 漏跑超过一分钟：可选择跳过，或把积压合并补执行一次。不会补发停机期间的所有命令。
- 同一任务存在 queued/running 运行时：新的手动触发返回冲突；定时触发生成 skipped 记录。
- 立即运行先展示目标数量与命令供确认。同一次确认框及网络重试复用同一请求 UUID。新确认框代表新的操作请求。
- 运行历史分页；结果弹窗自动更新状态，逐资产查看结果、原因、退出码和输出。输出在单台资产执行结束后出现，最多保留 64 KiB，截断有标记。

## 执行与恢复语义

F00 `internal/platform/jobs` 拥有 jobs、领取和租约；M12 拥有 schedules、scheduled_runs 和 scheduled_run_targets。应用层注册 `ssh.command` 处理器。未就绪的轮换、备份等处理器不注册，也不能通过 API 创建任务。

领取使用 `FOR UPDATE SKIP LOCKED`，随机 token 和 30 秒租约；每秒续租并检查取消。每实例两个执行 Worker。持久化执行意图前检查 token、租约、取消标志；旧 Worker 无权覆盖恢复后的结果。运行中的 job 过期后标为 unknown，绝不重新入队。

执行前和执行期间检查用户是否启用、`task.manage` 权限、资产授权、访问/动作策略、命令过滤、资产版本、凭据版本和 SSH 信任版本。后台执行不要求原浏览器会话保持登录。手动运行使用触发请求的来源 IP；定时使用创建或最近编辑定义时记录的来源 IP，作为该委托的策略上下文，不代表后台发生了新的用户登录。

复用 SSH、Agent、跳板、网关组、HTTP/SOCKS 代理的现有连接链路；密码或私钥仅通过 Vault 的 `scheduled_task` 内部用途提取。任务定义、执行快照、输出和运行状态是任务审计数据；通用操作日志只保存 ID/结果，不复制命令正文。

取消前尚未发送命令的资产标为 cancelled。命令已发送后的超时、取消、权限变化、连接丢失可能无法确认远端进程状态，标为 unknown，并停用该任务的自动调度。关闭 SSH 并不能保证远端脚本及其子进程已经停止。用户应先核对远端，再决定手动触发新运行或重新启用调度；系统不自动重试该运行。

批量中已成功的资产保持成功；确定失败与成功混合为 partial；任一资产结果不明时整次运行为 unknown。修改任务不改变已排队的命令快照；停用自动调度不取消已创建的运行。删除任务要求没有排队或运行中的任务，历史保留。

## API

- `GET/POST /api/tasks`、`PUT/DELETE /api/tasks/{id}`：列表/维护；更新与删除携带版本。
- `POST /api/tasks/preview`：`{cron, timezone}`，返回未来十个 UTC 时刻。
- `POST /api/tasks/{id}/trigger`：`{key: UUID, version}`，返回运行 ID。
- `GET /api/task-runs?scheduleId=&page=1`：历史。
- `GET /api/task-runs/{id}`：命令快照、逐资产结果与输出。
- `POST /api/task-runs/{id}/cancel`：持久化取消请求。

## 验收记录

- 第一批专项测试已通过：去重、双 Worker、输出截断、取消排队、超时、结果丢失、租约恢复与旧 Worker 拒写、并发调度、漏跑跳过、所有者隔离、委托人撤权、时区/DST。
- 前端 TypeScript 检查与 backend 构建通过。
- 后续新增运行中取消、部分失败、漏跑合并、重叠跳过验收；全量回归与浏览器实机验收尚待执行。
- 本地 Docker 引擎在镜像构建期间无响应，`buildx ls` 的 Linux Engine `/_ping` 超时。用户已授权重启 Docker Desktop，恢复后继续验收。

依赖使用固定 `github.com/robfig/cron/v3 v3.0.1`；容器 DNS 故障时从官方 Go Proxy 获取模块包，并使用官方 SumDB 的 go.sum 校验值校验。解析行为依据 [cron 官方文档](https://pkg.go.dev/github.com/robfig/cron/v3)。

## 最终验收更新（2026-10-29）

- Docker/WSL 已恢复，PostgreSQL、后端、图形服务和 R06 内网测试网关重新启动。
- 全量 `go test ./... -count=1 -timeout=12m` 及 `go vet ./...` 通过，application 包 283.565 秒。包含新增的运行中取消、部分失败、漏跑合并和重叠跳过测试。同名凭据的测试夹具冲突已修正。
- 镜像构建通过，已部署 r10.2-preview，实际 schema 27；编译和测试并发限制为 2。
- 浏览器任务页面可访问，新建表单分组显示，无页面横向溢出。
- 通过登录浏览器的真实 API 创建“R10 内网调度验收”，手动运行成功：输出 `/home/tester`、`R10_TASK_OK`，退出码 0。
- 一分钟 cron 于北京时间 13:04 自动创建并执行一次，成功。随后通过 API 关闭示例自动调度，保留任务定义和两条运行历史供前端查看。
- 真实任务使用 ssh-fixture 内网 SSH；其它连接链路复用现有实现，本次未逐条重新做实机验收。
- 日志：backend/.local/r10-jobs-full-tests.log、r10-jobs-recovery-build.log。前端编译证据：r10-jobs-ui.log。
- R10 凭据轮换仍待开发，本阶段完成不表示整轮完成。
