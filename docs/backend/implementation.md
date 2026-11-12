# 实施路线与验收

用户每轮操作验证按[前端迁移主计划](frontend-migration-plan.md)、[R00–R17执行卡](migration-rounds.md)及[记录模板](migration-acceptance-template.md)执行。已提供的双环境前端代理只解决接入配置，不表示后端业务已经完成。

当前模块开发顺序使用[模块开发规划](module-development-plan.md)中的 W0..W4，独立任务见[模块任务索引](module-backlog.md)。本文件保留架构级接口示例、协议验收和运维要求，旧 P0..P6 阶段作为对应关系参考。

使用文档补充的 36 项需求及 W0..W4 门槛见[修订说明](usage-docs-review.md)。下面的验收表是待实施测试要求，不表示已经执行过后端集成测试。

## 代码组织

建议在当前仓库新增 `backend/`，保留现有前端结构。以下为待实现的目录设计，本次没有创建空服务或宣称代码可运行。

```text
backend/
  go.mod
  cmd/
    server/          HTTP、WS 路由和业务服务
    connector/       terminal、graphics、rdp-native、web、db 运行角色
    worker/          异步任务与调度
    agent/           内网主动接入
    migrate/         数据库迁移入口
    nt/              运维 CLI，复用模块公开用例
    nt-client/       自有原生客户端，PKCE/HTTPS bridge
  native/rdp-worker/ 固定 FreeRDP 源码、C 插件与受控补丁
  internal/
    kernel/
    modules/
      identity/ authorization/ resource/ vault/ connectivity/
      session/ files/ audit/ webproxy/ database/ approval/
      automation/ pki/ notification/ operations/ backup/ ai/ system/
    application/bootstrap/
    application/workflows/
    application/readmodels/
    application/compatibility/
    platform/postgres/
    platform/cryptography/
    platform/objectstore/
    platform/jobs/
    platform/observability/
  api/openapi.yaml
  api/proto/
  migrations/manifest.json  模块内迁移的全局有序清单
  deploy/compose.yaml
  deploy/config.example.yaml
  tests/contract/
  tests/integration/
  tests/e2e/
```

每个业务模块内部使用 domain、service、repository、dto/adapter 的薄分层。不要把所有 struct 放进一个巨大 models 包，也不要让 Handler 直接更新数据库。公用包仅容纳明确共享的 ID、时间、Actor 和错误，不存放各业务之间的隐藏依赖。

## 核心应用接口草案

```go
type Authorizer interface {
    Evaluate(ctx context.Context, actor Actor, action Action,
        resource ResourceRef, env AccessContext) (Decision, error)
}

type SessionService interface {
    Create(ctx context.Context, actor Actor, in CreateSession) (ExportSession, error)
    Attach(ctx context.Context, actor Actor, in AttachSession) (SessionLease, error)
    Disconnect(ctx context.Context, actor Actor, sessionID ID, reason string) error
}

type SecretStore interface {
    PutVersion(ctx context.Context, actor Actor, in SecretWrite) (SecretVersion, error)
    Lease(ctx context.Context, service ServiceIdentity, in SecretLeaseRequest) (SecretLease, error)
}

type Connector interface {
    Reserve(ctx context.Context, in Reservation) (ReservationResult, error)
    Activate(ctx context.Context, in Activation) (ConnectionHandle, error)
    Close(ctx context.Context, sessionID ID, epoch uint64, reason string) error
}
```

类型为设计占位，未生成可编译的 Go 包。HTTP DTO 与领域类型分开：数据库 password_hash、secret_ref、node_id、lease_epoch 不通过通用 JSON marshal 暴露。业务返回领域错误，httpcompat 统一映射已有 code/status/字段。

## 领域事件与任务

| 事件 | 同事务写入方 | 消费者与效果 |
|---|---|---|
| UserDisabled / AuthSessionRevoked | Identity | Session 断连、OAuth 吊销、缓存失效 |
| GrantCreated / GrantRevoked | Authorization / Approval | 门户缓存刷新、连接复核、通知 |
| ResourceChanged | AssetCatalog | revision 发布、拨号计划更新、连接复核 |
| SessionRequested / SessionClosed | Session | 预约清理、录屏完成、指标 |
| RecordingFinalized | Audit/Session | 上传、转换、完整性检查 |
| AccessRequestSubmitted / Reviewed | Approval | 选择有资格的审批人、通知申请人 |
| WorkOrderApproved | Approval | 创建唯一执行任务，不直接在 HTTP Handler 执行 SQL |
| CredentialRotationRequested | Automation | 候选密钥、远程更新、验证、补偿 |
| CertificateRenewalDue | PKI/Scheduler | ACME 订单、签发日志、热更新 |
| SettingsChanged | Operations | 节点加载和 ACK，失败告警 |
| MonitorAlertChanged | Operations | 去重通知与恢复提醒 |
| AIToolApproved | AI | 校验当前权限后执行唯一 executionId |

事件 envelope 包含 event_id、aggregate_id、aggregate_version、type、schema_version、occurred_at、actor_ref、trace_id 和脱敏 payload。消费者幂等记录和业务效果在本地事务提交；外部副作用采用独立状态机。事件不携带明文密钥、大文件或录屏内容。

任务隔离队列：interactive-support、recording-upload、recording-convert、notification、backup、credential-rotation、certificate、scheduled-exec、ai-turn。高 CPU 转码不得与登录/交互连接共享无界 goroutine 池；每种任务明确并发、超时、重试、取消及 unknown 处理。

## 原阶段与系统级验收参考

每阶段以实际前端操作闭环验收，不以“接口返回 200”完成。以下是依赖顺序，未根据团队人数承诺工期。

| 阶段 | 交付内容 | 完成门槛 |
|---|---|---|
| P0 契约冻结与底座 | OpenAPI、错误码、数据库迁移、配置/密钥、Actor、中间件、审计、outbox/jobs | 前端启动所需 setup/branding/license/login-status 可响应；核心安全模型和 DTO 用例冻结 |
| P1 身份与资源 | 本地登录、MFA、用户/部门/角色、资产/分组/凭据、授权、门户 | 两个不同权限用户看到不同资产；无权 API 和对象 ID 请求被拒绝；秘密不会泄露 |
| P2 SSH 核心闭环 | 会话预约、xterm、SFTP、上传/下载、录像、在线终止、历史回放 | 真实 SSH 资产可连接；节点故障/撤权/磁盘满行为正确；回放可复现输出 |
| P3 图形与网站 | Guacamole RDP/VNC、图形录屏/共享、网站认证反代、证书 | Windows/Linux 桌面和网站真实连接；剪贴板/只读/文件权限在服务端有效 |
| P4 内网与治理 | 自有 Agent、链路/网关组、访问审批、身份访问策略、IP 集、通知、调度、轮换、备份 | 跨内网访问、临时授权到期关闭、轮换故障恢复、完整备份恢复演练通过 |
| P5 数据库与联合身份 | MySQL/PG 原生代理、SQL 工单、OIDC/LDAP/企业微信、身份提供方、API Key/Agent API | JDBC/CLI/GUI 客户端验证；prepared statement 审计；SQL unknown 不重做；OIDC 互通 |
| P6 AI 与规模化 | AI 对话/审批/工具、配额、恢复、HA、负载与混沌测试 | 工具权限不能超过用户；断线/重试无重复命令；滚动升级行为可预测 |
| 专项原生 RDP | FreeRDP 侧车、票据、通道策略、录像与客户端矩阵（D03） | 原生 mstsc 等真实测试完成才启用，不能被“图形网页已通”替代 |

原生协议按 [D01–D07 实施决策](implementation-decisions.md) 的任务前置执行，W0 锁依赖和契约，W3/W4 完成真实链路，不提前对用户开启未完成能力。P4 是一组相互可独立交付的模块，不表示需要一次上线全部功能。

## 契约和业务验收

| 领域 | 必须覆盖的用例 |
|---|---|
| 初始化 | 50 个并发 setup 请求只能生成一个初始管理员；初始化后接口关闭 |
| 登录 | 密码、验证码过期、MFA 中间态、重放、锁定、会话固定攻击、登出撤销；LDAP source 不混淆 |
| 权限 | 两用户 IDOR、部门继承变化、角色变更、列表过滤、分享越权、匿名下载、Key scope 收缩 |
| Cookie/WS | 跨 Origin HTTP/WS 被拒绝；合法同源正常；伪造 X-Forwarded-For 不改变策略判断 |
| 凭据 | 普通 DTO/日志/备份配置无明文；decrypted 需要权限和二次认证；密钥轮换后可读旧数据 |
| 审批 | 双击 approve、approve/cancel 并发、范围变更、到期、重复 grant、自批被拒绝 |
| SSH | host key 变化、密码/私钥/交互认证、中文/emoji、resize、断线、Telnet 协商、ZMODEM 与只读 |
| 文件 | 路径遍历、符号链接、特殊字符、配额、超大文件、取消竞态、覆盖冲突、会话关闭后再访问 |
| 图形 | RDP/VNC、分辨率、键鼠、剪贴板、共享晚加入、只读服务端过滤、录像回放 |
| 网站 | Cookie 隔离、Host/SNI、上游 TLS、WebSocket、公开策略、临时 IP、DNS 重绑定、响应改写上限 |
| 数据库 | MySQL text/prepare、多语句、PG extended/pipeline/COPY、事务回滚、取消、函数写入绕过、未知提交 |
| 审计 | DB 故障、本地 spool 满、片段中断、对象上传重试、校验失败、Range/回放授权、保留清理 |
| 调度 | 双调度器同时触发、Worker 失去租约、过期 owner 回写、misfire、时区/DST、取消 |
| 轮换 | 目标修改成功后掉线、部分资产成功、旧 key 删除失败、新 key 登录验证失败 |
| OIDC/Passkey | nonce/state/PKCE、回调 URI、码重放、refresh 重用、issuer 混淆、passkey RP/origin |
| AI | duplicate clientMessageId、重复批准、参数变化、撤权后执行、模型重试、WS 重连、输出注入、费用上限 |
| 恢复 | 从备份重建实例、根密钥恢复、对象清单一致、旧凭据/票据撤销、历史任务不重做 |

HTTP 契约测试校验状态码、Content-Type、字段和无 body 行为；WS/SSE 测试校验帧而非只检查握手。用真实 PostgreSQL 进行事务/锁测试，不能仅用内存 mock。协议测试使用隔离的测试资产，不向生产资产执行验证命令。

## 初始非功能目标

这些是待压测的验收目标，不是当前系统实测容量：

- 控制面普通读取在约定数据集与并发下 p95 < 300 ms；写入 p95 < 500 ms，排除外部认证和远程握手。
- 初始基准场景：10,000 个资产、1,000 个用户、200 个 SSH 并发和 20 个图形会话；图形分辨率、帧变化、网络时延需固定记录。
- 撤权正常控制通道目标 5 秒内生效；控制分区时按 15 秒租约上限终止，压测中验证时钟/抖动余量。
- 已 ACK 的业务事务 RPO 取决于 PostgreSQL 同步复制配置；不能用应用代码承诺零丢失。
- 备份策略目标 RPO ≤ 15 分钟、RTO ≤ 60 分钟，要求 WAL/PITR、对象与密钥备份及恢复演练共同支持；仅每日 pg_dump 不满足此目标。
- 保持至少 30% 的测得资源余量作为起始部署策略，再依据峰值和故障转移测量调整。

容量预算公式：

```text
录屏日容量 = Σ(协议并发数 × 实测平均录屏字节每秒 × 日活跃秒数)
总容量 = 日容量 × 保留天数 × 副本系数 + 转码副本 + spool + 备份
节点内存 = 进程基础开销 + Σ(各类会话常驻内存 + 有界缓冲)
链路带宽 = 输入输出 + 内部转发 + 录屏上传 + 网关封装开销
```

不能用屏幕分辨率直接推算录屏大小，也不能认为所有 RDP 会话负载相同。先在真实窗口活动和网络条件下测量，再给出 CPU/RAM/磁盘配置。

## 监控与运维

指标至少包括 active sessions、握手失败率与延迟、连接等待队列、租约续约失败、WS 背压、spool 剩余空间与最老片段、上传/转换失败、job queue age、数据库池等待、授权拒绝率、AI token/cost、备份最近成功和恢复演练时间。

指标 label 禁止放 userId/sessionId/完整 URL 等高基数字段；详细定位使用 trace_id 和受控日志。readiness 检查所需数据库/配置/密钥，liveness 只判断进程是否可继续运行，避免外部存储短故障导致重启风暴。

必备 runbook：数据库不可用、对象存储不可用、录屏磁盘接近满、connector 故障、Agent 证书吊销、凭据轮换不确定、SQL unknown、密钥遗失/恢复、证书续签失败、版本回滚。每个 runbook 明确“停止新连接”“保留证据”“人工核对”与恢复条件。

## 本次设计交付与后续边界

本次新增架构、业务服务、逻辑数据模型、协议兼容和实施验收文档，以及接口证据提取脚本。没有部署后端、改变前端行为或调用真实资产；前一轮前端测试通过不能被当作新后端的验证结果。

开始编码时首先完成 W0 和 W1，随后交付 W2 的 SSH 垂直闭环。后续按 W3/W4 的模块任务卡扩展，仍以真实连接、权限和审计验证作为发布条件。

## 使用文档补充的集成验收包

| 用例包 | 关联需求 | 必须验证的结果 |
|---|---|---|
| 管理员与普通用户首次接入 | U01–U06 | 分组授权动态加入/移除、权限过期、逐动作拒绝、凭证引用删除受阻 |
| 身份恢复与紧急运维 | U24/U25/U30 | 恢复码/TOTP 并发只消费一次；清除 Passkey 后旧凭据失败；UI/API/CLI 配置一致 |
| SSH 原生入口 | U04/U09/U10 | 名称/别名歧义拒绝、Host Key 重启稳定、转发禁用不影响登录、禁止目标转发被审计 |
| Agent 全生命周期 | U07/U08 | 注册/重连/吊销、TLS/Header/代理、include/exclude、Windows 磁盘、更新校验与回滚 |
| 原生 RDP 与 Termark | U11–U13 | 双消费者票据竞争、TTL 边界、错误重连、HTTPS-only 链路、两段 NLA、禁用能力拒绝 |
| 文件与配额 | U14 | 浏览器上传与后端落盘分阶段；取消/重试不越权；共享/默认存储隔离；驱动器写入不能绕过配额 |
| 审计查询与保留 | U15/U16 | 无 session 的失败访问可查；日志串联；禁用访问日志的范围明确；无录像显示原因 |
| 网站与真实 IP | U17–U20 | 唯一域名、恶意回跳拒绝、匿名/登录分支、续期开关、可信代理链、防伪 XFF、响应压缩/流式边界 |
| MySQL 与工单 | U21–U23/U36 | 用户专属密码、元数据查询、预处理、多语句、只读绕过、审批后自动执行、unknown 不重做、通知失败不重复 SQL |
| OIDC 与 mTLS | U26/U27 | Discovery/JWKS/issuer 一致、客户端密钥轮换与撤销；无证书/吊销/到期/错误 CA；证书默认继承及站点覆盖 |
| 备份与升级 | U28/U29 | 格式/校验/版本拒绝、CA 与秘密可恢复、对象独立备份、PG 主版本迁移与回退、安全备份失败不继续覆盖 |
| 配置与产品能力 | U31/U32 | 103 键逐域 schema；未知键拒绝、秘密掩码、配置生效反馈；实现/许可/权限取交集 |
| 监控、片段、AI | U33–U35 | 数字状态码不误注销用户；指标来源区分；片段插入不自动执行；系统与资产双重 AI 门控 |

自有客户端、Agent、RDP 和归档按照 [实施决策](implementation-decisions.md) 完成独立交付。官方 Termark 专有协议、旧 Agent、.ntbak 兼容使用真实 fixture 和外部契约，状态为 external_contract_required；不阻塞自有链路，也不能用 mock 冒充兼容。当前仅完成设计注册表验证，未执行后端实机测试。
