# 后端业务服务

本文件定义逻辑服务。除连接、后台任务和 Agent 外，首版服务均在 `nt-server` 进程内运行。表名是拟定逻辑模型，见 [数据模型](data-model.md)；路由采用当前前端命名，不依据通用 CRUD 基类自动开放写权限。

开发单元现已重组为[18 个功能模块](module-development-plan.md)。本文件保留详细业务规则，下表的服务名称和关联数据表示业务概念，不再作为代码包或表写入所有权的定义；具体归属以[模块任务索引](module-backlog.md)为准。

## 服务总表

| 服务概念 | 前端入口与接口族 | 主要职责 | 关联数据 |
|---|---|---|---|
| Bootstrap | `/setup`、初始化用户接口 | 单次初始化、版本状态 | instance_state |
| Identity | account、users、departments、roles | 登录、用户、部门、角色、MFA、API Key | users、roles、departments、auth_sessions、认证凭据 |
| Federation | oidc、wechat-work、LDAP、oidc-clients | 外部身份接入与作为身份提供方 | identity_providers、external_identities、oauth_* |
| Authorization | access-policy、authorised-*、strategies、command-filters | 菜单操作、资源授权、访问策略、会话能力 | grants、policy_*、strategies、command_filter_* |
| AssetCatalog | assets、database-assets、websites、groups | 资源、分组、标签、批量编辑、导入 | resource_registry、assets、websites、database_assets、resource_groups |
| Secret | credentials、各 decrypted 接口 | 凭据版本、加解密、显式查看 | credentials、secret_versions、secret_access_events |
| Connectivity | ssh-gateways、agent-gateways、gateway-groups、network-proxies | 跳板、内网隧道、链路选择和拨号 | gateways、gateway_*、network_proxies |
| Session | portal/sessions、access/terminal、access/graphics | 会话预约、连接、分享、终止、监控 | sessions、session_participants、session_shares |
| Filesystem | access/filesystem、storages、snippets | SFTP/共享盘、上传、编辑、脚本片段 | filesystem_mounts、upload_tasks、storages、snippets |
| Audit | sessions、session-commands、*-logs | 命令/文件/登录/操作/SQL/网站审计和录像 | audit_*、recordings、recording_segments |
| WebsiteProxy | portal/website、http-proxy、website-temp-allow | 网站访问认证、反向代理与临时授权 | websites、website_temp_allows、website_access_sessions |
| DatabaseProxy | database-assets、db-proxy 设置、sql-log | 数据库认证、协议代理、语句控制 | db_connections、database_sql_logs |
| Approval | access-requests、db-work-orders | 临时访问审批、SQL 审核与执行 | access_requests、approval_scopes、db_work_orders、执行记录 |
| Automation | scheduled-tasks、credential-rotation | 调度、远程脚本、凭据轮换 | schedules、job_runs、rotation_* |
| PKI | certificates、dns-providers、客户端证书 | TLS、ACME、SSH/客户端证书签发 | certificates、certificate_orders、dns_providers |
| Notification | notifications、mail/sms 设置 | 事件规则、个人绑定、投递与重试 | notification_* |
| Operations | properties、backup、geodata、monitoring、tools | 配置、备份恢复、监控、工具、IP 集 | settings、backup_*、monitor_alerts、ip_sets、geodata_versions |
| AI | ai、admin/ai、ai-preferences | 对话、模型、工具审批和执行 | ai_* |
| Capability | branding、license、logo | 产品能力、品牌、资源额度 | entitlements、branding、logo_assets |

## 初始化与身份认证

### 初始化

`instance_state` 只有一个有效初始化状态。提交 setup 时取得事务锁，确认仍未初始化，然后原子创建首个超级管理员、基础角色、默认设置和审计。重复或并发请求只能一个成功。初始化完成后，setup 写接口永久关闭，恢复管理员使用单独的本地维护流程。

未初始化的受保护 API 按前端习惯返回 HTTP 418；setup 状态、品牌、必要登录入口保持可访问。初始化接口限制来自管理网络或安装时配置的引导口令，避免新实例被首个任意访客接管。

### 登录与会话

本地密码登录：验证码证明校验 → 账号及来源策略 → 密码校验 → MFA 中间态 → 正式登录会话。LDAP 登录只在 `source=ldap` 且 provider 启用时触发，不把本地失败的密码转发给 LDAP。失败反馈对外统一，对审计记录可区分原因。

密码采用带独立随机 salt 的 Argon2id 哈希；具体资源参数通过部署设备基准测试确定，并保存在哈希编码中。LDAP bind 使用 TLS，转义过滤器，使用稳定外部 subject 映射账号，不用可更改的显示名做身份主键。

`auth_sessions` 保存随机 token 的摘要、用户、MFA 阶段、空闲与绝对截止时间、撤销时间、来源信息。Cookie 命名为独立实现自己的名称，HTTPS 下使用 Secure、HttpOnly、SameSite=Lax、Path=/。本地 HTTP 开发允许显式 dev 配置，不能把 insecure 模式带到生产。MFA 完成和权限提升后轮换会话 ID。

前端 LoginResult 有 `token`，但登录页面不再用它设置认证头；兼容层可返回空字符串，认证由 Set-Cookie 完成。未完成 MFA 的会话只能访问 MFA/logout/status 白名单。验证码、短信验证码、WebAuthn challenge 都要绑定用途、浏览器会话、截止时间和尝试次数。

敏感操作使用 `X-Security-Token` 对应的短时二次认证证明，绑定用户与登录会话、authentication strength 和允许用途；证明默认 5 分钟有效。现有签发接口不一定提供目标操作，因此兼容期无法强绑定请求正文：先实施“会话 + 用途类别”约束，高风险接口后续增加资源/正文摘要绑定并同步改前端。

### 管理与机器身份

- 禁用用户、重置密码、移除关键认证因子时撤销登录会话及相关能力，发布连接断开事件。
- API Key 随机生成，只在创建响应显示一次，数据库存摘要和可辨识前缀；列表的 `token` 返回掩码。接受当前 Python 客户端使用的 `X-Auth-Token`。
- Key 权限等于用户当前权限与 Key scope 的交集；不因 type=api 获得超级管理员能力。不允许 Key 代替 MFA 直接查看密码或重新签发身份凭据。
- 用户 SSH 公钥保存指纹与公钥；私钥不上传。用户客户端证书记录序列号、身份、用途、吊销与有效期。
- 超级管理员可管理系统配置，不默认拥有所有资产的远程操作权。紧急访问使用独立、显式、可审计的 break-glass 能力。

### 外部身份与 OIDC 提供方

外部登录采用 provider ID + issuer + subject 唯一绑定。邮箱相同不自动合并账号。绑定已有账号要求重新认证；撤销绑定时检查用户是否仍有可用登录方式。

OIDC 提供方需实现 discovery、JWKS、authorize、token、userinfo、撤销及用户同意记录；这些协议入口并非仅靠现有 SPA API 就完整。仅实现授权码流程并要求 PKCE；校验 state、nonce、issuer、audience 和精确 redirect URI，刷新令牌轮换与重用检测。RSA/EC 签名私钥进入 SecretStore，JWKS 只发布公钥，轮换时保留旧公钥到旧令牌过期。[OIDC Core](https://openid.net/specs/openid-connect-core-1_0.html) 与 [OAuth 安全最佳实践](https://www.rfc-editor.org/rfc/rfc9700.html) 是实现和互通测试的依据。

WebAuthn 校验 challenge、RP ID、origin、签名、用户验证要求和凭据所属关系；计数器变化结合备份型 passkey 属性评估，不能简单把所有未增长情况判为克隆。[WebAuthn 规范](https://www.w3.org/TR/webauthn-3/) 定义验证流程。

## 授权与访问策略

### 四层判定

```text
Allow = 身份有效
     AND 操作权限匹配
     AND 资源授权有效
     AND 接入策略通过
     AND 所需会话能力允许
     AND 产品能力启用
```

没有资源对象的管理查询只应用相应层，但所有列表仍限定可见范围。`GET /admin/...` 不是“登录即可访问”，菜单隐藏也不是鉴权。API 路由注册为标准 route ID 与 action；兼容输出 `permissions[{method,path}]`，内部不直接匹配用户提供的 URL 字符串。

`grants` 关联用户或部门、资产/网站/数据库/分组、有效时间、策略与命令过滤器。部门继承明确为包含祖先部门授予的权限，移动部门必须重新计算；禁止环。分组授权按请求时有效后代资源计算，资产移动可能改变权限，应产生审计与连接复核事件。

### 明确定义的合并规则

以下为新后端决策，前端类型不足以证明原后端使用相同规则：

1. 至少一个未过期且未撤销的资源授权匹配，才可访问资源；没有授权默认拒绝。
2. 所有匹配的显式限制按最严格结果合并：文件等布尔能力做 AND，空闲/期限取最短，命令拒绝任一命中即拒绝。空策略代表系统默认值，不能解释成全开。
3. 同一访问策略组内，启用且未过期规则按 priority 升序、ID 稳定排序，首个匹配规则决定该组结果。规则中不同维度 AND，同一维度多值 OR。
4. 白名单组无规则匹配则拒绝，黑名单组无规则匹配则允许；用户和部门绑定多个组时所有组均通过才放行。
5. 不绑定访问策略组时不增加 IP/地域限制，但仍执行全局登录限制和资源授权。地域数据不可用且策略要求地域时拒绝，不默认为国内或本地。
6. 能力不能因分享或 AI 执行扩大；监控参与者只读，服务端过滤键盘、鼠标、resize、文件和剪贴板输入。

规则评估返回 decision、reason_code、matched_rule_ids、policy_revision、expires_at，保存审计证据。IP 采用可信代理链提取，规范化 IPv4-mapped IPv6；IP 集在发布时编译成 CIDR 索引。时段以实例 IANA 时区计算，夏令时使用时间库，不用 UTC 固定偏移。

命令过滤对受控 exec/批处理是明确可执行边界；交互 PTY 中 shell 编辑、解释器、别名、转义、vim shell escape 和 ZMODEM 会使字符串规则不可靠。要求强限制的资源必须用受控 exec、禁用任意解释器与文件上传，或配合目标系统受限账号；不能把 PTY 正则过滤标称为沙箱。

## 资源与凭据

AssetCatalog 统一资源 ID 注册表，但分别保存主机、网站和数据库的强类型字段。前端 `attrs` 可扩展，后端按协议白名单校验；禁止把任意 attrs 直接拼接为系统命令或 guacd 参数。分组树采用 parent + closure，排序值兼容现有字符串 sort，移动/重排在短事务中完成。

导入先解析和验证，返回行级错误；执行时按批次事务并生成导入报告。导出只输出调用者可见的数据，默认排除明文凭据。批量编辑严格使用 changes 白名单和资源逐项授权，不能把一个成功资源的权限应用到整批。

SecretStore 采用信封加密：每个秘密版本用独立数据密钥加密，主密钥/KMS 包装数据密钥，AAD 包含资源、字段与版本。主密钥不存放在同一数据库备份中。API 普通查询不返回可用密码，显式 decrypted 接口要求独立权限、二次认证并记录查看行为。

修改秘密必须区分“缺失字段保持原值”“明确清空”“替换新值”。兼容 DTO 中的空字符串和 `-` 不是通用的清空指令；字段注册表逐项定义转换。普通表单不得把掩码写回真实秘密。

删除凭据、网关、代理时检查关联引用，返回 HTTP 409 和前端读取的 `assetNames/gatewayNames/websiteNames/databaseAssetNames/...`。外键是并发删除的最终保护，不能只做一次提前计数。

## 网关与内网 Agent

统一 `DialPlan` 描述 direct、gateway chain、HTTP CONNECT/SOCKS5 proxy。链路编译先展开资产配置和分组默认值，校验类型、存在性、循环与最大跳数（初值 8），再生成不可变 revision。任何连接复用都按目标/用户/授权/凭据版本隔离。

GatewayGroup 支持前端的 priority、latency、random。选择只发生在新会话，探活失败触发节点熔断，不把进行中的 SSH 连接无声切换到另一跳板。

Agent 按 [D02](implementation/native-client-agent.md) 使用一次性注册令牌换取专用设备 API Key，mTLS 为追加认证；不假定官方二进制互通。WSS/yamux 通道按 device/epoch 绑定，拨号请求受目标允许列表和15秒会话租约约束。限制流数、缓冲、带宽、目标网络和 WOL 广播范围；删除 Agent 吊销凭据和已有隧道。

旧前端下载的是 `nt-tunnel-*`，其私有线协议在本仓库中不可见。本方案新增自己的 `nt-agent` 协议与构建产物，并通过下载接口和注册命令适配前端；不能假设能直接驱动旧二进制。

## 远程会话与文件服务

内部会话状态：`requested → reserved → connecting → connected → closing → closed`；预约失败进入 failed，节点丢失进入 interrupted。前端 session.status 为 string，实际 UI 使用的值需要 DTO 映射，不能直接假设所有内部状态都被前端识别。

SSH 校验目标 host key；初次信任必须由资产管理流程登记指纹或受控批准，不能默认忽略。连接尝试有总超时和每跳超时。SSH 原生入口使用同一授权服务，严格管理 SFTP、exec、PTY、TCP forwarding 请求。[Go SSH 包](https://pkg.go.dev/golang.org/x/crypto/ssh) 提供协议基础，业务授权仍由本系统实现。

图形会话由 connector 校验后桥接 guacd。Guacamole 消息中的键盘、鼠标、剪贴板和文件指令都需要服务端能力过滤；不能依赖前端按钮 disabled。图形共享需维护独立参与者连接与可恢复显示状态，不能简单把建立连接后的字节流复制给晚加入者。由 guacd 共享能力或协议状态重建实现，作为专项验收。

分享链接是短期 capability：随机令牌仅存 hash，绑定会话、创建者、期限、只读/可操作上限、最大参与人数和撤销 revision。当前前端传 `passcode`，其语义需按表单确认；服务端不能把用户输入的短口令直接作为不可猜测令牌。分享 URL 使用 Referrer-Policy，日志过滤 token 参数。

`filesystemId` 是服务端登记的文件系统挂载，不允许任意指定节点目录。支持会话 SFTP、受管理共享存储和图形会话驱动目录；创建挂载时保存 owner、session/resource、根目录、策略和连接节点。

文件服务执行 realpath/根目录边界检查、路径规范化、符号链接限制与每次操作授权。SFTP realpath 仍存在远程 TOCTOU，严格隔离还需目标端 chroot/权限；不能承诺靠字符串路径检查防止所有越界。编辑采用限长读取、编码检查和可选内容哈希冲突校验，避免静默覆盖多人编辑。

上传状态按前端定义：`created → receiving → transmitting → success`，旁路终态 error/cancelled。请求体为 multipart `file`；客户端上传完成只表示传到服务器。当前前端收到 HTTP 2xx 就判成功，因此兼容接口必须等目标落盘完成才返回成功；若改为 202 后台上传，必须同时修改前端。用临时文件、大小与摘要检查、同目录 rename 完成提交；预留磁盘配额，取消和超时清理临时文件，不删除最终文件。

ZMODEM/SFTP/图形驱动是不同传输通道，都须纳入策略。禁用上传时若 PTY 仍允许任意二进制输入，不能宣称实现了绝对的数据上传阻断。

## 审计与录像

审计拆分为登录、管理操作、终端命令、文件操作、数据库 SQL、网站访问和系统事件。共同字段包含 actor、resource、trace_id、来源、动作、结果、时间和策略 revision。命令输出、SQL 参数和文件名可能敏感，设访问权限、截断和保留策略，不写密码、令牌或请求中的秘密。

录屏流水线：打开持久 spool → 按序追加片段及时间索引 → 生成 manifest/校验和 → 上传存储 → 校验上传 → 更新可播放状态 → 按保留策略删除本地副本。文件不能只写在容器临时层。每个片段键按 session/recording/sequence 唯一，上传可幂等重试。

终端回放兼容 asciinema 使用的格式；Guacamole 回放保存其协议流并提供播放器可读取的响应；视频转换使用隔离的转码 Worker，限制 CPU/内存/时间。原始 recording、video、上传状态和转换状态分别保存，避免“已关闭会话”等同“录像可播放”。下载/Range 请求和监控连接每次校验审计权限。下载 URL 不暴露本地绝对路径。

普通管理员的 clear 操作只能触发被授权范围内的保留/删除任务。生产可设最低保留期和法律保留标记；哈希链可检测部分篡改，但不能防止同权限攻击者重写整条链，需要独立存储策略或外部签名锚点。

## 网站代理与数据库代理

网站代理根据规范化 Host/SNI 匹配资源，执行登录/公开访问口令/IP/地域/时段/临时授权检查，再按 DialPlan 转发。公开访问不等于无条件放行。临时 IP 允许绑定站点、申请者和到期时间，明确 NAT 下会共享同一出口地址；autoRenew 关闭时不续期；开启时按滑动 TTL 续期，并受本方案新增的绝对期限约束。

管理控制台与目标站点 origin 分离，禁止向上游传控制台 Cookie、X-Auth-Token、X-Security-Token。目标地址来自授权资产，用户不能通过 query 改目标；外部 URL 拉取、favicon、IP 集同步、模型地址等非资产场景有独立 egress 策略。资产允许私网是产品必要能力，不能用一刀切禁止私网替代授权。对 DNS 解析结果、重定向后的目标和实际拨号地址统一校验。

转发支持请求/响应流、WebSocket、上游 TLS/SNI、Host override 和超时。响应改写只在许可内容类型和大小下解压、修改、重新计算 Content-Length/ETag；不能对视频、大文件或无限流全量缓冲。

数据库代理先认证平台身份，再验证资产授权并使用目标凭据建立后端连接。原生 MySQL 客户端用户名兼容 `username@asset_name`（官方使用文档），登录秘密为个人中心签发的数据库专用密码，不直接把平台主密码转给目标库。连接池禁止跨平台用户或跨事务复用状态。

MySQL 需处理握手、TLS、认证插件、文本查询、prepare/execute、结果集、多语句和取消边界；PostgreSQL 需处理 startup、TLS/SCRAM、simple query、Parse/Bind/Execute/Sync、COPY、错误恢复、事务状态及取消。相关协议依据：[MySQL Command Phase](https://dev.mysql.com/doc/dev/mysql-server/latest/page_protocol_command_phase.html)、[PostgreSQL Message Flow](https://www.postgresql.org/docs/current/protocol-flow.html)。

SQL 分类应使用对应方言解析器，并结合目标最小权限账号。仅匹配字符串 SELECT 无法保证只读，函数、CTE、存储过程、prepared statement 都必须纳入限制。无法判定的语句在受限模式拒绝；自由模式按策略审计。审计记录耗时、行数、数据库、SQL 摘要和结果，默认不存完整结果集。

## 审批与外部执行

### 临时访问

申请只允许 scope 内资源；审批人必须拥有该申请范围的批准权限，默认不允许自批。审批事务锁定请求，确认 pending，限定开始时间/时长/策略上限，插入带 request_id 的临时 grant，更新请求并写 outbox；任何一步失败整体回滚。

前端状态保持 pending、approved、rejected、cancelled、closed。授权到期由 grant 的时间条件实时拒绝，后台任务只做归档和通知，不依赖定时任务准时运行保证安全。approved 在有效期结束后仍是审批结果；列表显示过期时间。closed 用于已有授权等系统结束条件，`existing_authorization` 保持前端可翻译原因码。取消 approved 必须同事务撤销其 grant 并通知断连。

### SQL 工单

前端已有 pending、approved、rejected、executed、failed、unknown。approve 接口没有独立 execute 按钮，因此设计为“审批通过后入执行队列”；内部执行阶段放在 `work_order_executions`，外部运行中仍显示 approved。

审批时冻结资产、数据库、SQL 原文摘要和目标凭据身份；执行前重新校验审批、资产和操作者权限。普通工单默认单语句，禁止绕过目标只读/DDL 策略。结果保存 rowsAffected、errorMessage 和执行证据。

远程数据库提交与平台状态保存不是一个事务。提交后断网或 Worker 崩溃无法证明是否生效时必须 unknown，禁止自动重试。只有能确认远端事务未提交，或业务自带可查询幂等键，才允许安全重试。unknown 需要人工核对目标数据库，当前 UI 没有核对按钮，后续新增受审计的 reconcile 接口及界面。

## 调度、轮换、证书、通知与运维

调度器只负责将唯一 `(schedule_id, scheduled_for)` 写入任务；Worker 负责执行。cron 和实例时区明确，next-ten-runs 使用同一解析器；默认错过一次不补无限历史，禁止同任务重叠。远程脚本在资产上运行，通过参数化 exec/上传脚本实施，平台主机不拼接 shell。

凭据轮换使用 `prepared → applying → verifying → activated`，失败进入 rollback_pending/manual_reconcile。先生成并加密保存候选版本，再更新目标，重新登录验证，最后切换 active_version。共享凭据对多个资产更新可能部分成功，应逐资产记录成功版本，不用一个 bool 代表整组原子成功。保留旧密钥/回滚条件明确，删除旧 SSH key 只在新 key 验证后进行。

PKI 管理证书有效期、默认项唯一性、私钥匹配、自动续签与 DNS provider。ACME 订单和 challenge 关联 job，有速率限制与退避；DNS API Key 只在受限 Worker 可解密。TLS 证书替换通过原子配置 revision 更新，失败时保留有效旧证书；客户端证书吊销要能影响现存认证连接。

通知从 outbox 消费，规则计算渠道/个人收件者、quietMinutes、个人偏好和审批资格；“接收待审批提醒”不赋予审批权限。唯一 `(event_id, rule_id, channel, recipient)` 防重复创建，投递记录 attempts 和 next_retry_at。发送成功与状态提交之间仍可能重复，供应商支持时使用幂等键，否则承认至少一次语义。

M18 的配置注册表定义每个 key 的类型、默认值、秘密属性、校验器、影响服务和是否重启。系统设置 PUT 只能更新允许键；配置发布 revision，节点确认加载，失败保留旧快照。监控区分 liveness/readiness，磁盘/队列/连接/录屏失败产生有恢复阈值的告警；确认告警与解决告警是不同状态。

备份包含数据库快照、必要配置、对象清单、版本和加密元数据；录屏是否包含必须在 includes/excludes 明示。根密钥单独托管并测试可恢复性。恢复先验证版本/校验和、进入维护模式、排空并终止连接、建立恢复前备份，再在隔离数据库恢复验证后切换。恢复后吊销旧登录、票据和未完成远程任务，不能把过去任务重新执行。

IP 集同步和 GeoData 更新以临时文件下载、大小/内容/摘要验证、原子替换和版本回滚完成。ping/tcping 有目标、次数、时长限制，不允许命令注入；SSE 断开立即取消探测。存储卷和共享盘有容量预留及回收账本，usedSize 不能只依赖可丢失的内存计数。

## AI 服务

AI 会话、用户消息、轮次、事件、工具执行和审批决定分别持久化。WebSocket 只是订阅/提交通道，断开不重发已接受命令；前端 `clientMessageId` 用作去重键。一个会话同一时间仅一个活动轮次，排队消息保存顺序，send_now 触发明确的轮次调度。

工具执行状态保持 reviewing、awaiting_approval、running、succeeded、failed、rejected、cancelled、timed_out、needs_revision。工具执行 ID 全局唯一；用户批准必须绑定 executionId、工具参数摘要、目标资源、主体和期限。参数或文件 diff 改变则重新审批。

所有工具调用进入同一 Asset/Session/Filesystem/Approval 服务，使用用户委托权限，不给 AI 一个数据库超级用户或不受限 shell。hostMentions 只用于选择候选资产，后端仍检查访问权。提示词、主机描述、命令输出和模型建议均不是授权来源。

confirmMode 的 auto/auto_review/balanced/always 是交互规则，不影响资源鉴权与硬性禁令。自动审查超时或失败不能自动放行。命令运行独立 exec 通道，设超时、输出上限、取消信号和审计。模型重试只能重试生成；未知结果的外部工具操作不得自动重做。

模型配置秘密加密，模型请求日志脱敏，按用户/实例设置并发、token、成本上限；只发送必要且被授权的上下文。rewind/reset 只改变对话视图或分支，绝不能声称撤销已执行的系统操作。AI reasoning 字段只展示供应商明确提供的可公开摘要，不自行构造隐藏推理内容。

## 产品能力与品牌

`/api/license` 提供当前前端可消费的 type/oem/source 适配；独立后端应增加明确的 capabilities 作为长期契约，前端逐步从版本名称切到功能标志。未实现功能必须同时关闭界面和服务端入口，不能把企业版字段设为 true 就宣称完成。

官方许可证的签名和服务端验证逻辑不在本仓库中。本实现只定义自己的授权配置或对接合法提供的验证服务，不模拟官方签发与认证。品牌、logo、版本和定价链接来自自己的产品配置。

## 官方使用文档补充后的流程边界

详细规则与来源以 [使用文档修订说明](usage-docs-review.md) 和模块卡 U01–U36 为准。

- M01 的数据库密码是 MySQL 协议身份材料，区别于平台登录密码和普通 API Token；M04 只保护可恢复的 verifier/秘密材料，不决定用户认证规则。
- M06 原生 RDP 的一次性票据解析后即消耗；目标建连失败不恢复旧票据。浏览器图形、原生 RDP、Termark 通过能力矩阵区分监控、主动断开、文件与录像，通用会话接口必须处理不支持状态。
- M07 的浏览器 RDP 文件流是平台存储中转。默认存储按操作者选择，共享存储明确授权；目标 Windows 本地复制只能通过桌面录像观察，不能生成虚构 SFTP 操作日志。
- M09 的主机名唯一约束覆盖所有网站，并排除管理 SelfDomain。公开规则、登录用户资源授权和临时 IP 授权各有明确分支；autoRenew 是可关闭的滑动续期，不是永久授权。
- M16 的基础备份包含数据库与 CA，录屏及驱动盘有独立备份/恢复任务。官方 .ntbak 导入能力待格式验证；自有归档带明确格式版本，不能仅靠扩展名判断兼容。
- nt CLI 作为 application adapter 复用 M01/M02/M13/M15/M18 用例，不拥有业务表；应急恢复模式的 OS 身份校验、操作原因、结果与后续会话撤销必须可审计。
