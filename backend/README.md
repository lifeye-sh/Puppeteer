# Puppeteer Go 后端

当前状态见 [轮次记录](../docs/backend/migration-rounds.md)。下列各批次说明为历史交付记录；生产部署按 R17 全新安装规划进行。

R17 已提供核心、图形/MP4及协议入口的独立部署预览和生产启动保护，见[部署步骤](../deploy/production/README.md)。本页历史记录中的 development-only 限制不再代表当前代码；R17 的真实证书、容量、恢复演练和最终上线签收仍未完成。

R11 已交付加密备份、每日计划与保留、S3/SFTP/WebDAV 复制、离线隔离恢复、双服务切换、回退与继续、旧认证和任务隔离、独立恢复记录及 pgBackRest 演练。入口为 `/system-event` 或系统设置的备份标签；详见 [验收记录](../docs/backend/acceptance/R11.md) 和 [运维步骤](../docs/backend/implementation/backup-restore.md)。用户验收待确认，当前仍为 development-only 部署。

已交付 R01 初始化、登录、能力工作台，以及 R02 用户、部门、角色查看权限、验证码、账号锁定、TOTP、恢复码、会话撤销和审计。R03 新增 SSH 资产目录、分组标签、加密凭据、资源授权、门户与 JSON 导入导出。使用独立 PostgreSQL；未实现能力返回 501，前端不转发到旧后端。R04 新增真实 SSH、主机信任、会话租约和访问审计。当前 r11-preview，schemaVersion=30，capabilitiesRevision=17，contractVersion=1；005 迁移补充免安装终端提交审计，r04.3 增加访问记录中的录屏回放；006 迁移补充 R05 文件模块与录屏摘要。

R06 已交付 Agent 注册/轮换/吊销、WSS/yamux 拨号、最多四级 SSH 跳板、网关组故障切换、HTTP/HTTPS/SOCKS5/SOCKS5H 代理、真实指标与链路诊断，以及 TUF 受管更新/失败回滚。开发验证完成，用户验收待确认。见 [部署说明](deploy/README.md) 与 [R06 验收记录](../docs/backend/acceptance/R06.md)。

## 启动与测试

需要 Docker Desktop Linux Engine、PowerShell 7、Node/npm 和前端依赖；无需宿主机 Go。首次拉取镜像及 Go 模块需要联网。在仓库根目录运行：

```powershell
pwsh -NoProfile -File scripts/start-backend.ps1
npm run dev:backend
# PostgreSQL 集成测试，每个用例独立 schema
pwsh -NoProfile -File scripts/start-backend.ps1 -TestOnly
# 可重复创建验收数据，不覆盖已有账号密码或权限
pwsh -NoProfile -File scripts/seed-r02.ps1 -Operator accept-admin
```

打开 <http://backend.localhost:5174>。空库进入初始化，R01/R02 数据自动升级；旧环境仍使用 dev:legacy 和 legacy.localhost:5173。账号 3–64 位 ASCII 字母、数字或 _.-，首字符字母/数字，统一小写；密码至少 12 个字符、最多 1024 UTF-8 字节。

Compose 项目继续使用 nt-backend-r01，以保留数据卷和实例 ID；名称不代表运行版本。API 仅映射 127.0.0.1:8089，PostgreSQL 不映射宿主端口。脚本保存 backend/.local/compose.env 的随机数据库密码和 两个独立的 32 字节密钥 NT_MFA_KEY 与 NT_VAULT_KEY。必须一起保留该文件与数据卷；登记密钥指纹后，更换密钥会拒绝启动，不能直接重生成密钥处理丢失问题。

Vite 禁止通过普通路径、raw 参数和文件系统路径读取 backend/.local/、.playwright-cli/。这些目录被 Git 忽略，凭据只能在本机打开。

```powershell
docker compose --env-file backend/.local/compose.env -f backend/compose.yaml exec server /app/nt status
docker compose --env-file backend/.local/compose.env -f backend/compose.yaml logs --tail 50 server
docker compose --env-file backend/.local/compose.env -f backend/compose.yaml restart server
docker compose --env-file backend/.local/compose.env -f backend/compose.yaml stop
# 本机运维恢复 MFA，仍需知道账号密码
docker compose --env-file backend/.local/compose.env -f backend/compose.yaml exec server /app/nt reset-mfa --user accept-admin --operator local-maintainer --reason "lost authenticator device"
```

恢复 CLI 的操作者来自拥有容器执行权限的人声明，审计为 mfa.cli_reset，不是网页认证身份；没有 HTTP 后门。CLI 不重置密码。浏览器管理员可修改其他账号密码。当前没有忘记管理员密码找回功能。

## 模块与数据

| 目录 | 职责与表所有权 |
|---|---|
| internal/platform/postgres、migrations | F00 连接、共享事务、编号迁移、校验和与 advisory lock；migration_history |
| internal/modules/identity | M01 users、auth_sessions、departments、user_departments、login_locks、auth_challenges、mfa_factors、mfa_recovery_codes |
| internal/modules/authorization | M02 permissions、roles、user_roles、role_permissions、grants；按请求查询授权 |
| internal/modules/resource | M03 resource_registry、resource_groups、assets、resource_tags；邻接树 + 递归查询 |
| internal/modules/vault | M04 credentials、secret_versions、secret_access_events；不可变秘密版本 |
| internal/modules/system | M18 instance_state、settings；初始化、实例及密钥指纹 |
| internal/modules/audit | M08 operation_logs、login_logs、access_logs、recordings、recording_chunks、session_commands；访问与录屏证据 |
| internal/modules/connectivity | M05 ssh_known_hosts；解析目标、主机密钥信任与 SSH 握手 |
| internal/modules/session | M06 sessions；预约、票据、状态和租约 |
| internal/application | HTTP 适配、跨模块事务、鉴权与本地运维用例 |
| cmd/server、cmd/nt | HTTP 进程与运维 CLI |

R01–R06 实际表结构以 migrations/001–017 为准，全量设计的 124 张并未全部实现。部门使用邻接关系、递归 CTE 和串行写入防环，未创建 department_closure。不得更改已应用的 001_r01.sql、002_r02.sql 、003_r03.sql 或 004_r04.sql，后续新增编号迁移；runner 验证历史校验和。

## 权限、登录与 MFA

十项可分配权限：user.read、department.read、role.read、lock.read、audit.read、settings.read、asset.read、credential.read、grant.read、session.read。菜单、页面、API 对齐；每个请求检查当前权限。identity.manage 仅由系统管理员获得，不能通过角色授予；委派写权限未实现；R03 资源授权按下节说明生效。

用户、部门、角色更新需要 version，冲突返回 409。被引用部门/角色不可删除；最后一个启用管理员不可禁用、降级或删除。用户软删除；任何用户编辑撤销其全部会话。角色权限从后续请求生效，前端每 5 秒刷新并清理相关缓存。

密码 Argon2id（64 MiB、3 次、并行度 1、随机盐）；256-bit 随机会话仅以 host-only、HttpOnly、SameSite=Lax Cookie 传递，数据库存 SHA-256 摘要。完整会话 8 小时，MFA 临时态 5 分钟；业务 API 只接受完整会话。校验到期、撤销、账号启用状态与 auth_epoch。

所有写请求校验 Origin；普通 JSON 限 16 KiB，凭据写入限 64 KiB，资产导入限 1 MiB；拒绝未知字段。密码计算并发最多 4 个，单源 IP 每分钟最多 20 次。五次坏密码锁账号 15 分钟，状态持久化，管理员可解锁。Vite 下 IP 是代理地址；当前不信任任意 X-Forwarded-For，生产可信代理/分布式限流留待部署阶段。

TOTP 遵循 [RFC 6238](https://www.rfc-editor.org/rfc/rfc6238)：SHA1、6 位、30 秒、±1 步容差，已消费时间步不可重放。AES-256-GCM 加密密钥，用户 ID 为 AAD。绑定需重新验证密码；成功返回十个 128-bit 恢复码，仅显示一次，数据库只存绑定用户的 SHA-256 摘要。

恢复码原子消费，并发最多一个成功；撤销旧会话，只签发重新绑定临时会话，重绑后才可访问业务。重绑替换旧恢复码。管理员重置需自身密码、自身 TOTP（若已绑定）和原因；目标下次登录强制重新绑定。临时认证五次错误会撤销会话。流程参考 [OWASP MFA Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Multifactor_Authentication_Cheat_Sheet.html)。

登录使用真实 PNG 滑块，数据库保存答案；挑战及证明有效 120 秒，绑定 HttpOnly nt_captcha Cookie，一次性使用。它验证完整挑战链路，不具备生产级机器识别对抗能力。

## 契约、边界与回退

直接 JSON，无 data 包装，204 无响应体；错误为 {code:0,reason,message,requestId}。400 坏密码/无效证明，401 未认证，403 无权限，409 冲突，415 非 JSON，418 未初始化，422 字段无效，429 锁定/限流，501 未实现，503 依赖异常。73 个 HTTP 方法见 [OpenAPI](api/openapi.yaml)。三个只读配置：system-name、login-session-duration=480、login-captcha-enabled=true。

R01 升级前备份在 backend/.local/r01-before-r02.dump。只支持前向迁移；回退需停止新 API，把备份恢复到另建数据库并运行 R01 版本，不可对现有 R02 数据执行降级 SQL 或删除共享卷。备份不包含 R02 后新增数据；本轮未演练整库回退，灾备专项未验收。

仅供本机开发验收，NT_ENV=development 之外拒绝启动。HTTP Cookie 未设 Secure，Compose 私网数据库使用 sslmode=disable；生产 TLS、密钥轮换、分布式限流、审计防篡改和灾备尚未交付。SSH 远程终端与录屏回放已在 R04 交付，SFTP 已在 R05 交付，详见验收记录。

操作清单见 [R04 验收记录](../docs/backend/acceptance/R04.md)，后续见[迁移执行卡](../docs/backend/migration-rounds.md)。开发自测和用户验收分别记录。

## R03 资产、凭据与授权操作

前端入口：/asset、/credential、/authorised-asset、/x-asset。前三者要求对应查看权限，修改需要系统管理员；个人门户对所有已完整登录用户开放，只返回授权且启用的资产。普通用户无需资产管理权限。

资产本轮支持 SSH、IP/域名、端口、唯一别名、启用状态、共享凭据、说明、连接超时 1–300 秒和最多 20 个标签。分组为全局唯一名称的树；同事务防环、引用约束和乐观版本；暂不创建 resource_group_closure。组授权动态包含子组，部门授权只匹配用户直接所属部门。支持开始/到期时间、编辑和撤销；列表、详情、计数、筛选项均按相同授权范围限制。删除资产保留 registry 墓碑及审计/授权历史，不能复用旧 ID。

凭据支持密码、带 Passphrase 的 SSH 私钥及服务端生成 Ed25519 私钥。私钥解析使用 [Go SSH API](https://pkg.go.dev/golang.org/x/crypto/ssh#ParsePrivateKeyWithPassphrase)。生成密钥不会自动安装到目标。API 只返回元数据、公钥和引用数量；更新时不传 secret 则保留秘密，勾选更新秘密才产生新版本，旧密文不覆盖。仍有资产引用时删除返回 409。网关尚未交付，因此当前引用检查覆盖资产，后续接入网关时必须同时扩展检查。

每个秘密版本生成独立随机 DEK，使用 AES-256-GCM 加密；DEK 再以 NT_VAULT_KEY 包装，AAD 绑定凭据 ID 和版本。nonce 随密文保存。KEK ID 当前为 local-v1；生产 KMS、密钥轮换、短期机器租约与远程轮换尚未实现。NT_VAULT_KEY 指纹写入实例表，不匹配拒绝启动。备份必须同时保存数据库与本机 compose.env；不得重新生成密钥来处理丢失。

查看秘密必须完整管理员会话、自身密码、已绑定的 TOTP 新动态口令；未绑定 MFA 返回 403。恢复码不能用于解密。成功查看与 secret_access_events/operation_logs 同事务，响应 no-store，关闭弹窗清除显示；审计无明文。实现依据 [Go AES/GCM](https://pkg.go.dev/crypto/aes#NewCipher)。

导入使用 JSON {items:[资产对象]}，每批 1–100 行且 <=1 MiB，每行独立事务和审计；不覆盖现有资产，错误行返回行号且不留下半条数据。导出有当前过滤条件，最多 10000 行；超过则拒绝而非截断。导出清空 id/version、保留分组/凭据引用，不携带秘密。当前模板适用于同实例，导入前修改重复名称和别名；跨实例需映射引用 ID。导出文件也可能包含主机地址，应按资产清单管理。

TCP 检查只接受已保存、启用资产，由管理员触发；解析一次后连接选定 IP，3 秒超时、最多 4 并发及单源 IP 每分钟 20 次，阻止未指定/多播/链路本地地址。堡垒机场景允许管理员指定私网和回环地址；不接受 URL、重定向或普通用户探测。只报告 TCP 可达，绝不宣称 SSH 服务、主机密钥或凭据验证成功。

```powershell
# 需先已有 accept-admin、operator 等 R02 账号；只新增缺失示例，不覆盖已编辑资产/凭据
# 保留的示例为 TEST-NET 地址，不能当作真实 SSH 连通验收
docker compose --env-file backend/.local/compose.env -f backend/compose.yaml exec server /app/nt seed-r03 --operator accept-admin
```

当前示例：R03 SSH A=192.0.2.10:22、R03 SSH B=192.0.2.11:22；共用 R03 示例共享凭据，分组 R03 验收资产，operator 仅获 A。请编辑为你有权访问的真实主机并配置正确凭据。operator 的 R02 MFA 重绑状态保持不变。

R03 升级前备份为 backend/.local/r02-before-r03.dump。保留 R02 对应代码/镜像和密钥后，可在另建数据库恢复此备份并启动 R02；不能直接对当前库降级，也不应覆盖 R03 后新增数据。本轮未演练整库回退。


## R04 SSH 与审计

从 /x-asset 或 /asset 在新标签页打开 /terminal?assetId=...；/access 是同一终端入口。/online-session 查看在线连接；/access-log、/exec-command-log 查看访问历史、终端提交和显式执行命令。命令列表使用服务端分页，可访问 100 条以前的记录。权限、限制和可直接测试的本地主机见 [R04 验收](../docs/backend/acceptance/R04.md)。

管理员首次连接前必须在资产页探测、通过可信渠道核对并保存主机公钥；未知/变化密钥拒绝连接。SSH 只读取保存的资产目标，凭据经 M04 内部 SSHAuth 端口解密并记录 ssh_session 用途，不返回给前端。M06 只拥有 sessions；M08 独立持有访问和录屏记录。设计表名 asset_access_logs/recording_segments/audit_commands 对应阶段物理表 access_logs/recording_chunks/session_commands，映射已登记模块目录。

WS 用现有单字符类型帧：1 输入、2 cols,rows、3 附着票据/连接成功、6 base64 输出、0 错误、4 结束、9 心跳。Cookie+Origin+一次性票据三项检查；票据绑定网站登录，不出现在 URL。30秒预约、10秒租约、每2秒复核；授权、身份、资产、凭据或主机信任变化关闭旧会话。单进程最多16条、每用户4条、单会话最长4小时；明确只读模式在服务端禁止输入/resize/exec。HTTP Shutdown 单独排空 hijacked WS 后再关闭数据库。

每个输出片段先入库，再发浏览器；写失败/配额达到64MiB则中断，录屏标记 interrupted。记录时间偏移、原始字节与尺寸，r04.3 提供回放。显式 exec 保存命令、状态与退出码；最多10秒/64KiB，独立通道不继承交互环境，超时关闭SSH且不自动重放。

录屏接口 `GET /api/sessions/{id}/recording?after=0`：每页最多 64 个事件，按 sequence 递增，以 next 继续到 total。payload 为 base64 原始字节（含跨分片 UTF-8），种类 output / exec-output / resize，resize 内容为 cols,rows。会话本人或 audit.read 可读取，每页重新鉴权；录制中返回 409，首屏访问记 recording.viewed 操作审计，响应 no-store。已存录屏无需补采集。

前端在访问列表及详情提供“录屏回放”，支持播放/暂停、0.5/1/2/4/8 倍速、进度跳转、回到开头；初始 80×24，按事件还原尺寸，向后跳转从头重建终端。独立只读 xterm，无 SSH/WS/命令调用，阻止 OSC 52 写剪贴板。播放器先分批加载并校验序号与字节数，上限 64 MiB / 20 万事件，超出时明确报错；不加载原始按键。进度终点为最后一个录屏事件，不包含末尾空闲时间；中断录屏标注部分内容。暂未实现文件导出、对象存储与保留清理。

r04.2 的免安装审计在服务端处理 WS 输入：每次 Enter（CR/LF；CRLF 合并）先事务写入提交占位记录，再将本帧转发到 SSH。只从随后 2 秒内对应的可见 PTY 回显识别文本；支持常见单行编辑、回显的历史回调和补全，标记 source=terminal-input、state=observed，退出码为 null。没有可靠回显则保持 unrecognized、command 为空，前端显示原因。不写原始按键，不把不回显的密码作为命令保存。输入在内存中短暂用于回显比对，不能宣称内存或全部终端输出无秘密；显式输入到命令行并回显的秘密仍属于命令和录屏内容。

识别器不是 Shell 解释器：自定义/多行提示符、换行折行、复杂编辑、括号多行粘贴、全屏程序、应用交互、延迟回显可能无法识别；命令执行和退出码不由回显证明。Ctrl-C 取消的行不计为提交；特殊编辑器的非 Enter 执行操作及脚本内部命令不保证捕获。括号粘贴在结束后的 Enter 记为一次提交，复杂多行文本保留未识别记录。需更强语义和防绕过保证时必须增加主机审计，参见 [SSH 通道协议](https://www.rfc-editor.org/rfc/rfc4254.html) 和 [Bash 执行钩子语义](https://www.gnu.org/s/bash/manual/bash.html)。

单会话命令/提交总量上限 10000，每帧最多 100 次提交，最多 200 条待回显；单条识别文本最多 4096 UTF-8 字节，屏幕行缓冲最多 8192 字符。达到提交限额或写审计失败会停止终端、标记 command_audit_failed，故障帧不转发；已写入的占位记录不保证对应输入实际到达远端。GET /api/sessions/{id}/commands?current=1&pageSize=20 返回 {items,total}，无 current 时保留旧数组响应（最新 100 条）。只允许本人或 audit.read；旧会话不补造命令。

当前连接进程仍与 API 同部署；多节点归属、独立网关/Worker、录像对象存储和保留策略随后交付。实现采用固定 golang.org/x/crypto/ssh v0.58.0 与 gorilla/websocket v1.5.3；WS 使用单读/串行写、帧大小和读写超时边界。接口依据 [Go SSH](https://pkg.go.dev/golang.org/x/crypto/ssh) 与 [Gorilla WebSocket](https://pkg.go.dev/github.com/gorilla/websocket) 文档。

本地 OpenSSH fixture 启动：`pwsh -NoProfile -File scripts/start-r04-fixture.ps1 -Operator accept-admin`。该命令只新增缺失资产/凭据/授权；公钥写入 .local，私钥仍在 Vault；控制台显示主机指纹供前端核对。主机密钥在 ssh_hostkeys 卷；不映射宿主端口。不要删除卷后盲目重新信任。

R04 前备份：backend/.local/r03-before-r04.dump。回退需另建数据库并恢复备份、匹配 R03 镜像/代码及原密钥；不原地删除迁移，不覆盖新写入。未演练完整回退。

## R05 文件操作与录屏校验

r05.2 在 SSH 终端右侧文件夹提供 SFTP 文件管理：目录浏览、上传、下载、UTF-8 文本编辑、新建目录、重命名和删除空目录/文件。单文件限 16 MiB，文本编辑限 256 KiB；仅存活会话本人可操作，只读会话禁止写入。文件审计入口为 `/filesystem-log`。实现使用 [pkg/sftp v1.13.12](https://pkg.go.dev/github.com/pkg/sftp)，本地 fixture 已启用 SFTP 子系统。

上传经过持久分片、远端临时文件、SHA256 回读校验和重命名提交；取消释放暂存，网络不确定结果不会自动重试。远端根目录固定为 SFTP 服务暴露的系统根目录 `/`，遵循登录用户的操作系统权限并拒绝符号链接，但严格目录隔离仍需目标端 chroot。录屏关闭后尝试封段，首次回放校验内容摘要；访问记录中的保留预览只统计，不删除。测试入口、恢复边界和验收证据见 [R05 验收](../docs/backend/acceptance/R05.md)。升级前备份为 `backend/.local/r04-before-r05.dump`；未演练整库回退。


## R06 控制通道开发预览

Agent 网关 `/agent-gateway` 提供注册令牌、Linux amd64 程序下载、启动说明、真实控制通道状态和设备撤销。程序入口 `cmd/nt-agent`，使用 WSS/yamux；r06.2-preview 已支持资产选择 Agent 并通过受限拨号与租约访问 SSH；尚无资源指标、自动升级或 Windows 安装支持。完整范围及测试见 [R06 阶段记录](../docs/backend/acceptance/R06.md)。

SSH 跳板 `/ssh-gateway`：最多 4 级 SSH 转发、逐级主机密钥核验、版本冲突保护、停用或凭据变更关闭会话。资产连接路径可选直连、Agent 或 SSH 跳板，后两者互斥，失败不回退。此预览保留停用配置供审计，不提供物理删除。多级跳板已加入；网关组、代理等完整 R06 范围仍在开发。

R06.6 新增资产选择安装 Agent：在 Agent 网关选择资产，由后台 SSH 安装并确认上线。见 [安装验收说明](../docs/backend/acceptance/R06-asset-installer.md)。

R07 已增加 Telnet 与 RDP/VNC 图形连接、只读策略、访问记录图形回放及只读观察/分享；个人存储及 RDP 共享盘见下文，整轮其余范围保持开发中。见 [R07 开发与测试记录](../docs/backend/acceptance/R07.md)。

图形服务配置：`NT_GUACD_ADDR=guacd:4822`、`NT_GUACD_RELAY_HOST=server`。Compose 私有 guacd 不映射宿主端口；后端建立仅接受 guacd 对端 IP 的单次临时中继，经原资产直连/Agent/跳板/网关组/代理路径拨号。其他部署必须将中继限定在可信私网，禁止公网暴露；此实现面向单受信 guacd 实例，尚未提供跨不可信网络的 guacd 双向 TLS。RDP 使用 NLA；资产表单保存时自动探测远端 TLS 证书并固定 SHA-256 指纹，也可点击“获取 / 更新远端证书”预览主体、签发者和有效期，无需手填。地址或连接路径变化时重新探测；已有连接不会自动替换固定证书。探测失败则本次保存失败，实际连接仍启用证书校验；VNC 原生认证及传输能力取决于目标，跨网络请走已有 SSH/Agent 路由。

R07.3 提供在线会话只读观察与指定用户分享：`/session-observer`。观察读取已提交录屏前缀，约 1 秒刷新，不创建远端控制连接。接收者须已登录并具备资产授权；分享最长 30 分钟、一次领取，观察绑定当前登录，撤销/撤权在下次读取生效。源会话结束后停止；每会话最多 8 个活跃观察者、256 次加入和 64 次分享。该阶段尚未实现共享存储，后续 r07.5 已加入个人存储及 RDP 共享盘。


r07.5 增加 RDP 悬浮工具箱、双向纯文本剪贴板（16 KiB，传输内容不单独进入录屏）和组合键。个人文件按用户隔离、默认 5 GiB，用户管理可调整配额；文件块存入 PostgreSQL，备份须包含 storage_nodes/storage_chunks/user_storage。单文件上传最多 1 GiB，最多 10000 个目录/文件节点；降低配额不删除文件，超过配额禁止新增字节。用户于 2026-10-29 明确授权后，已启用 RDP 共享盘 MyZone。Compose 图形容器使用 /dev/fuse 和 SYS_ADMIN，只有挂载守护进程以 root 运行；guacd/协议进程均以 UID 1000 运行且不继承数据库凭据。无宿主目录挂载、无新增宿主端口。详情见 R07 验收记录最新章节。

共享盘配置：`NT_RDP_DRIVE_ENABLED=true`；驱动目录固定 `/mnt/nt-rdp/<session UUID>`，后端选择路径，FUSE 按会话解析到所属用户根目录。Windows“此电脑”显示“Guacamole RDP 上的 MyZone”，也可访问 `\\tsclient\MyZone`。只读会话可读但禁止写入、删除、截断、重命名；剪贴板纯文本最多 16 KiB，只读禁止发送，观察者不接收剪贴板通道。权限为平台固定策略，暂未提供独立的每用户剪贴板策略表单。文件系统提供普通文件和目录，不提供符号链接、硬链接或完整 NTFS ACL/锁语义。持久数据仅在 PostgreSQL 中，备份必须覆盖上述三张表。

停用共享盘：将服务端 `NT_RDP_DRIVE_ENABLED` 设为 false 并重新创建服务，新会话不再映射驱动。若还要取消容器挂载权限，须同时将 guacd 服务恢复为官方镜像并去掉自定义入口、设备和 SYS_ADMIN；不要在挂载进程仍启用时仅删除设备配置。


## R08 / R09 已交付

高级访问策略、临时审批与自动撤权、SMTP/企业微信通知，以及独立 HTTPS 网站代理、登录回跳、临时 IP、WebSocket、证书与 ACME DNS 自动续签、mTLS 已实现并部署。当前版本 `r09.0-preview`，schema 25，能力版本 12。

按用户要求，通知、DNS、ACME 和上游服务使用内网模拟环境。前端入口、测试账号位置、验收证据与运行边界见 [R08/R09 操作与验收手册](../docs/backend/acceptance/R08-R09-runbook.md)。上文 R07 分阶段说明保留为历史记录。
