# D05 / D06 配置、能力与许可证

M18 是唯一写入所有者，领域 owner 负责校验和应用；M01/M02 分别验证身份和权限。关联 U31/U32。完整属性定义在[机器可读目录](../system-property-catalog.json)，默认值是本项目选择，不是官方闭源生产默认值。

## 属性注册与兼容层

**最新增量（2026-11-11，schema73）：** 当前已部署31项运行配置，除下述历史批次外，IP锁定/总开关、浏览器Cookie、每人登录会话容量、品牌文字与Logo、七类日志保留/预览/保全均已实际接入；新增六项会话水印设置以新会话快照作用于网页终端、图形与观察，不写入录屏或原生客户端。登录容量默认关闭以保持升级行为；Logo必须引用服务端校验并重编码的PNG对象。后面的“尚未接入”属于各历史批次当时状态，最新证据见R16验收记录。权益提供器和其余属性仍不能当作整轮完成。

**2026-11-11 实施状态：** 下文是完整目标，尚未全部实现。已部署首批 PostgreSQL 新会话参数 `postgres-query-timeout-seconds`（1..20，默认10秒）、`postgres-result-max-rows`（1..100000，默认100000行）、`postgres-result-max-mib`（1..64，默认64MiB），均为非秘密整数。具备快照、If-Match、节点 ACK、失败重试和历史查询；新连接重新读库，旧连接保留快照。此实现通过 head 行锁及定时拉取同步，不使用 outbox，也不承担监听器切换。完整103键/秘密/许可/配额功能仍逐项待接入，证据见 [R16](../acceptance/R16.md)。

第二批接入 `login-session-duration`（1..525600 分钟，默认480）。所有浏览器身份登录和 MFA 升级共用签发出口，以当前修订生成绝对到期时间并记录版本；现有会话不回写。MFA 待验证/待绑定仍仅5分钟。Cookie MaxAge 从已签发会话的剩余有效期计算，不再次读取可能变化的配置，不做滑动续期。不改变原生 OAuth 令牌、数据库个人密码或 Web 代理独立会话的时限。schema62 在当前快照上追加配置版本，旧版本缺少该键时按“未纳入此版本”展示，不能改写历史；不支持直接换回仅识别三键的旧二进制。完整交付证据见 R16 验收记录。

第三批接入账户失败锁定三键：`login-lock-failed-duration`（1..1440分钟，默认15）、`login-lock-failed-times`（1..100次，默认5）、`login-lock-account-duration`（0..10080分钟，默认15）。统计时长的早期设计默认10，实际旧代码为15；本次保留15避免升级静默改变保护行为。密码登录、LDAP失败及管理员密码复核共用计数实现；不是所有身份提供方的远端失败都能计入。下一次失败读取当前版本，成功校验或无新失败达到统计时长清除/重置计数；锁定期内不续期，到期后的首次失败从1开始。时长0仅禁止产生新锁定，既有锁定仍需到期或管理员解锁。总开关、独立IP锁定仍未接入，验证码、限流及MFA尝试上限保留各自实现。此类策略只限制新的失败校验，不撤销现有已登录会话。

每个定义包含 `key,owner,wireType,internalType,proposedDefault,validation,effect,secret,access`。目录覆盖官方属性页103键；当前前端还有该页未列出的键（例如原生RDP与录屏转换），仍须纳入同一个运行注册表，不能误认为103就是全产品属性总数。下方给出本轮相关扩展键。

| 新增/前端已存在键 | 类型与本项目默认 | 校验与生效 |
|---|---|---|
| rdp-proxy-enabled | bool=false | D03 profile通过后方可开；listener |
| rdp-proxy-addr | hostport=0.0.0.0:3390 | 地址合法且无冲突；listener |
| rdp-proxy-public-addr | hostport=空 | 对外连接启用时必填，禁止把0.0.0.0发给客户端；new-session |
| rdp-proxy-ticket-ttl-seconds | int=300 | 60..3600；new-session |
| rdp-proxy-certificate-id | UUID=空 | 启用时证书匹配公开主机名且有私钥；listener |
| login-session-browser-close-logout | bool=false | 只改变Cookie持久性，不能保证浏览器恢复会话后一定退出；new-session |
| native-client-enabled（新） | bool=false | M01/M06客户端闭环通过；security-live |
| agent-auto-install-enabled（新） | bool=false | TUF仓库/可信root与维护窗已配置；next-job |
| backup-recipients（新） | X25519公钥列表=[] | 开启备份前至少一个合法age接收者；next-job |
| backup-format（新） | 只读 nt-backup/v1 | 不接受任意格式输入 |
| entitlements-provider（主机配置） | local | local/signed；切换需受控重启，不能通过普通用户API开启商业权益 |

HTTP `/api/admin/properties` 的已有请求/返回结构沿用前端契约；string是官方文档wire描述，实际UI需要的bool/number由compat adapter转成原生JSON值，不能对字符串 `"false"` 直接做真值判断。枚举/数值解析只允许完整值，无效返回422且整批不修改；未知键拒绝。迁移阶段旧键通过显式alias映射，写入只存规范键。

本项目新增 schema 和状态端点：GET `/api/settings/schema`（字段类型、范围、readonly、default、effect）、GET `/api/settings/revisions/{id}`（requested/effective/node ACK/errors）。兼容PUT响应体保持不变，返回 `ETag` 和 `X-Settings-Revision`；管理前端补 `If-Match`，过期返回409。老客户端未带 If-Match 时服务端串行化应用并记录 legacy_write，不能虚称其具有乐观并发保护。

秘密读取返回掩码并带 hasValue；旧表单掩码原样提交表示保持，空值默认保持，明确clear操作单独字段且二次认证。新API更新采用 `{set:{...},clearSecrets:[...]}`，不把普通值 `"*****"` 存成密码。普通get/list不得输出私钥，ssh-server-private-key-exists及version为只读；前端表单若回传这些字段则兼容适配只允许忽略未变化的只读投影，试图修改拒绝。

## 生效方式和跨字段校验

统一 effect 枚举：`live`（展示类立即读取）、`security-live`（下一次鉴权立即读取并撤销不合规租约）、`new-session`（只影响新建连接，旧会话保留快照）、`next-job`（下一次任务）、`listener`（prepare新监听/证书后原子切换，旧连接drain）、`readonly`。不是所有属性都能无中断热更新。

事务：锁 settings_revision → 合并批次 → 所有领域校验 → 写 typed值/secret_ref/revision + outbox。配置状态 `requested → applying → effective / failed`；节点按 revision 幂等准备并ACK，不能乱序覆盖。30秒未ACK告警，旧revision节点停止接收受影响新连接。listener变更先试绑端口，失败继续旧listener并显示requested与effective差异；管理员可重试或提交补偿revision，不能隐式改历史。

收紧安全策略不等待所有节点：授权器从提交时按新限制拒绝新请求，旧租约5秒复核/15秒上限终止；失联节点隔离。放宽策略直到所需节点应用成功才开放。已生效revision保留值快照和变更审计，但不记录秘密明文。

强制跨字段规则：

- 邮件开启需要host/port/from；mail-use-ssl=true使用隐式TLS，false使用强制STARTTLS，生产不降级明文。skip-verify只允许显式开发profile。
- LDAP开启需ldaps或强制StartTLS、DN/filter/mapping；禁用密码登录前至少一项已测试外部身份/Passkey及离线应急入口可用，防止锁死管理员。
- 推荐密码策略至少12字符、弱密码名单检查，允许长口令；customize按JSON字段执行。复杂度中的字符分类定义为字母/数字/其他三类，前端1..3与服务端一致。
- access-mfa-expires-at是本项目规定的分钟时长兼容键，内部名mfaFreshness；不得当Unix时间戳。已由 AssetAccessEntrySetting.tsx 确认分钟单位，新内部API显式以秒表示并只换算一次。
- Passkey RP域名与allowed origins精确一致且HTTPS，localhost仅开发；OIDC issuer固定HTTPS、redirect精确匹配。provider默认部门必须存在，不能隐式赋管理角色。
- SSH forwarding开启还必须有精确host:port白名单，私钥需可解析且只用于服务端身份；设置导入不覆盖既存Host Key。
- 保留天数-1代表无限，0拒绝（避免误删全量）；改短先给dry-run计数，清理任务按新revision执行，legal hold永不绕过。录屏强制要求由资产策略决定，全局关录屏与强制策略冲突时拒绝新连接。
- ip-extractor 非direct时仅接受经过显式trusted proxy的头；XFF从右向左剥离可信代理。自定义header遵循HTTP token语法且禁止Authorization/Cookie等敏感头。
- LLM开启必须有允许的endpoint/model与预算；私有服务端点按管理员出站白名单，禁止未授权 metadata 地址。temperature范围0..2仅是兼容上限，具体模型不支持时不发送而不是报已应用。

## 自有能力提供器和配额

**schema70 集成状态（2026-11-11）：** local/signed 验证已接入服务启动配置、管理员复核导入、追加式许可历史、状态面板，以及用户/三类资产/主机与数据库会话准入。100并发 × 三类额度、错实例/过期/零配额/版本冲突、续租越过旧到期点的竞争测试通过。149个应用测试入口的完整分组回归、其余Go包、go vet和29项前端测试通过，已部署schema70/revision45并完成local面板验收；业务实例保持 local 默认不限配额。部署文件、公钥、接口与实际受控范围见 [操作说明](../acceptance/R16-entitlements-runbook.md)。该范围不等于全产品功能权益已覆盖。现有依赖为 go-jose/v4 v4.1.4，使用[固定版本官方文档](https://pkg.go.dev/github.com/go-jose/go-jose/v4@v4.1.4)。

已实现的格式为compact JWS，header必须仅含alg=EdDSA、kid、typ=nt-license+jws；kid只查只读公钥副本，拒绝jku/jwk和重复字段。payload必须包含下文全部字段，iss=next-terminal-local-issuer，aud/product=next-terminal-backend，UUID实例与licenseId，schemaVersion=1。配额对象强制users/resources/sessions，null表示无限，数值0..1000000000；features为精确布尔字典。重复键、错误大小写、未知字段、超长文本、非法时刻均拒绝。快照每次检查签名/实例/有效期，过期不会返回有效功能；local空部署配置仅允许已实现能力。单测与go vet通过；测试镜像未启用CGO，未运行race检查。

最新范围：AI于revision47，任务、通知、文件写入与独立RDP共享盘进程于revision48接入；资源默认值于revision49接入。剩余范围：不能仅给 features 填键便宣称对应入口已受控。全量配置适配、监听热切换、多节点配置一致性验收与 DBeaver/全站业务按钮矩阵仍需继续。管理员登录、审计读取和恢复入口在上述许可失效后保持可用。

定义 `CapabilityProvider.Snapshot(instanceID,now)` 返回 `{source,revision,features,quotas,expiresAt}`。local模式从只读部署配置获取授权范围，默认允许已实现的核心模块；signed模式验证本项目签发的许可证。任何模式都不能令尚未实现的协议入口变为可用。

最终能力 `implemented ∩ entitlement ∩ configured ∩ testedProfile`，具体操作再经过M02权限/策略判断。前端增加implemented、available、disabledReason，旧license字段由compat adapter投影；返回明确原因 `not_implemented, not_configured, not_entitled, expired, quota_exceeded`。quota无限在内部用null，不用Infinity写JSON；各旧字段的数值/字符串表示按当前TS读取点单独适配并契约测试。

自有商业许可采用compact JWS，固定alg=EdDSA，kid只查内置公钥集合；payload包含 `iss,aud,licenseId,product,instanceId,iat,nbf,exp,features,quotas,schemaVersion`。库选go-jose/v4，[官方实现](https://github.com/go-jose/go-jose)。拒绝none/算法混淆、未知kid、错产品/实例、重复JSON字段、超长payload、负配额及未来schema；允许时钟偏差60秒。实例ID是初始化时生成的持久UUID，不能用可变MAC作为唯一身份。

私钥仅存在发行端离线签发环境，服务端只含公钥。导入许可证二次认证、审计摘要和revision；到期后拒绝新增受限资源/新会话，既有会话按既定授权租约和产品到期策略结束，仍允许管理员登录、查看历史、导出与恢复。首版无离线宽限；断网不影响已验证且未到期的离线许可。纯离线无法可靠证明系统时钟未回拨，也无法实时撤销，这是商业模式边界，不能靠本地时间戳宣称防篡改。

资源数/用户数配额在同一PG事务锁定额度行再计数创建；会话配额使用M06 reservation状态计数并带TTL，关闭/失败释放，超时清理带epoch，避免双释放。额度降低时保留现有资源但禁止净新增，超过并发额度先阻断新预约，不任意删除业务数据。

测试：所有属性合法/非法/边界、单位转换、只读回传、秘密掩码、空值clear、跨字段失败全回滚；并发revision、节点重启/失联/ACK乱序/端口占用；license篡改/错实例/错alg/到期、实现与许可交集、配额100并发竞争。这些测试随领域实现执行，本轮仅检查设计注册表完整性。


### 配置历史与设置工作区（已实现，revision46）

平台设置七分区替代原单页堆叠。GET /settings/revisions 以 before/limit 倒序游标返回元数据，limit为1..50；GET /settings/revisions/{id}/diff 的 from 缺省取实际前版、0为空基准。只比较 PropertySchema 注册的23项非秘密字段，未知历史值排除。两接口需settings.read，写入继续要求identity.manage与If-Match。前端只提交所选配置组；保留周期仍使用独立预览机制。历史版本只读，不提供未经校验的快照覆盖。未来接入秘密适配器必须设计脱敏差异，不能直接扩展当前值投影。


### AI 执行权益（revision47）

共享数据库准入回调增加 ai.assistant；服务层Create/Begin/Enqueue/Provider/StillEnabled/Propose/Decide(approve)/ClaimExecution执行检查，后台派发与工具实际适配器也复核。包级模型协议函数仍为内部协议实现，应用生产调用统一经过Service.Generate/GenerateStream/Models的许可封装。外部请求500ms复核、检查2s超时，终态隐藏撤销答案但不抹除已知用量。工作区、配置、历史与取消不受许可关闭影响，原有身份授权继续生效。用量未知的外部调用不虚报零费用。此增量不覆盖文件模块独立入口或任务/通知权益。
