# D01 / D02 原生客户端与 Agent 实现契约

主责：M06 原生客户端、M05 Agent；M01 身份、M02 授权、M04 秘密、M13 证书、M15 指标协作。关联 U07–U13。以下均为新系统 v1 设计。

## 当前受控 TCP / HTTPS 转发 profile（R14 第五、六批）

已部署原生 SSH 网关的独立 `direct-tcpip` 模式，Windows OpenSSH `-N -L` 完成实机验收。HTTPS bridge 已支持显式 `entry=ssh-forward`，`nt-client forward` 以浏览器 PKCE 授权连接；普通 SSH 预约继续禁止转发，幂等键不能切换模式。下文完整客户端、系统密钥库和外部适配仍是设计目标。

- 管理接口：`GET/PUT /api/admin/native-forward`，要求 `identity.manage`；修改复核密码/TOTP，携带 `version`。默认关闭，最多 100 条精确 `{assetId,host,port}` 规则；`addresses` 只接受服务端解析结果。
- 启用规则固定 DNS IP，拨号不再解析。后端直接访问目标，不经过资产跳板/Agent；资产授权同时要求执行、上传、下载允许，命令过滤或只读不得通过不透明流量绕过。
- 每连接 8 通道、每用户每分钟 20 次建立尝试；继承原生会话限额和 4 小时上限，首通道 60 秒、拨号 5 秒、写阻塞 15 秒。配置或授权变化断开活动连接，客户端需重新连接。
- `GET /api/native/ssh-launch/{id}` 附加当前用户可用的 `forwardTargets`。专用命令固定回环监听并覆盖普通配置的 `ClearAllForwardings yes`。
- `GET /api/sessions/{id}/forward-streams?current=1` 分页返回目标/IP/状态/双向流量/时间；同会话明细权限，不录制 TCP 内容或生成 MP4。审计随访问记录保留策略清理；隔离恢复停用转发并封存未结束通道。
- `-R`、Agent/X11、SFTP、与 shell/exec 混用均拒绝。本批不据此声明其他专有客户端、动态代理或原生 RDP 的兼容性。

详细证据和前端操作见 [R14 第五批验收](../acceptance/R14.md)。

## 客户端身份和资产目录

### 已实现：登录设备管理（2026-11-02）

- “设备登录”是一次浏览器授权生成的 OAuth family，不是硬件证明。每次新授权独立登记，刷新仍属于原记录。CLI 默认在退出时注销；Windows 的显式记住登录与刷新复用见下节。跨次重新授权归并与持久安装标识尚未实现。
- `GET /api/account/native-devices?page=1`：仅浏览器登录本人，固定每页20条，返回 `items,total,page,pageSize`。每项包含 id、name、os、arch、clientVersion、sourceIp、registeredAt、可选 lastSeenAt、expiresAt、revokedAt、scopes 和 active/revoked/expired/invalid 状态。状态核对当前用户/应用/同意版本、授权范围、允许用户及全局认证 epoch；不把授权状态称为在线。
- `DELETE /api/account/native-devices/{id}`：当前平台密码、启用时的 TOTP 复核；只撤销本人的指定 family，访问与刷新令牌失效，同时标记关联 SSH/转发/RDP 预约终止。活动连接使用原有租约复核断开，其他设备不受影响。重复撤销幂等，审计不重复。
- 原生授权码交换接受可选 `device_name,device_os,device_arch,client_version`，仅作展示，来源 IP 由服务端记录；返回 `device_id`。`nt-client --device-name` 可指定显示名称，OS/架构自动上报；刷新不接受更换设备信息。名称长度、控制字符及标识符边界均校验。
- 原生客户端再次同意授权保留既有有效 consent 的版本和已同意范围，但新 family 只取得本次请求范围；不会使其他设备退出，也不会扩大旧 token 的 scope。“已授权应用”中撤销仍使所有相关设备失效。第三方 OAuth 同意语义不变。
- 迁移054登记已有原生 family；迁移055避免把升级时间伪造成历史使用时间。lastSeenAt 按实际令牌使用/续租记录，最多每分钟更新一次（签发/刷新实时记录）；历史未知值展示“尚无使用记录”。令牌清理删除 family 时级联清理设备行，恢复隔离撤销 family 后设备失效。
- 前端：个人信息 → 个人访问凭据 → 登录设备。完整测试与部署边界见 [R14 验收](../acceptance/R14.md)。

### Windows 显式记住登录（CLI 0.2.0-r14）

- `login --remember` 经浏览器授权申请 `assets:read offline_access`；连接权限需额外指定 `--allow-connect`。已保存登录时拒绝静默覆盖；更换账号或扩大范围先 `logout`。每个 Windows 用户、每个传入 HTTPS 平台地址保存一条记录，使用相同地址复用，不合并域名别名。
- Windows 使用 `CredReadW/CredWriteW/CredDeleteW` 的 GENERIC 凭据，LOCAL_MACHINE 持久性限定在当前用户、本机的后续登录会话，不使用企业漫游。只保存刷新令牌、平台地址、授权范围、设备 ID 和刷新阶段；不保存访问令牌或目标密码，不提供文件回退。实现依据 [CREDENTIALW](https://learn.microsoft.com/en-us/windows/win32/api/wincred/ns-wincred-credentialw)、[CredReadW](https://learn.microsoft.com/en-us/windows/win32/api/wincred/nf-wincred-credreadw) 和 [CredWriteW](https://learn.microsoft.com/en-us/windows/win32/api/wincred/nf-wincred-credwritew)。同一 Windows 用户下的进程并非相互隔离的安全主体。
- 同一 SID、平台地址使用全局命名 mutex，覆盖多个进程及 Windows 登录会话；Go 固定持锁 OS 线程，等待最多15秒。一次事务包含读取、写入 pending 标记、调用刷新接口、保存新令牌。崩溃或响应丢失后保留 pending，后续只尝试撤销旧授权，不重试已消费刷新令牌；确认撤销后要求重新浏览器登录。不能确认撤销时保持阻断并明确提示。
- `--use-login` 每次命令先轮换一次令牌，核对设备、scope、有效期及新旧令牌，使用完不撤销整族。连接仍通过后端租约持续校验；没有连接 scope 时拒绝而不自动扩大授权。
- `logout` 先清理本机凭据，再撤销远端授权；网络失败会明确提示远端撤销未确认，应在个人账号的“登录设备”撤销。保存失败不返回可用登录，会尝试撤销刚签发的授权。机器在首次授权成功、写入凭据前崩溃时仍可能留下远端授权，需通过设备列表撤销，最长受服务端7天寿命限制。
- 从 CLI 0.3.0-r14 开始，Linux 支持下节的 Secret Service；macOS 仍仅编译并保留临时浏览器登录，记住登录明确返回不支持。尚无安装器、自动更新或多账号 profile。

Windows PowerShell 使用示例（先在 OIDC 应用客户端的服务端设置中启用原生登录开关，保持 TLS 信任校验；自签 CA 可用 `--ca` 提供 PEM）：

```powershell
$client = 'E:\AIAgents\codex\Next-Terminal\backend\.local\nt-client-remember-windows-amd64.exe'
& $client --version
& $client login --server https://backend.localhost:5174 --remember --allow-connect --device-name '我的工作电脑'
& $client assets --server https://backend.localhost:5174 --use-login
& $client connect --server https://backend.localhost:5174 --asset '<资产 UUID>' --use-login
& $client logout --server https://backend.localhost:5174
```

当前为独立 exe：复制到可信目录即可运行，不注册服务或自启动。移除前执行 `logout`，确认远端撤销；无法联网时在个人设备列表撤销，再删除 exe。现有界面入口“我的账号与 MFA → 个人访问凭据 → 登录设备”可核对登录、最近使用及撤销状态。

### Linux Secret Service（CLI 0.3.0-r14）

- 直接使用 `github.com/godbus/dbus/v5 v5.2.4` 调用 Secret Service，不执行 shell 或 `secret-tool`，令牌不进入 argv。只接受显式的本机 Unix session bus 地址，核对 socket 对端 UID，拒绝 TCP、自动启动地址、地址回退链及 setuid 运行。固定服务的 unique owner，连接和认证最多5秒，方法调用最多5秒。
- 要求桌面已有默认 collection 且已解锁；缺少服务、collection、锁定或交互 prompt 均失败，不自动创建或解锁密钥库。默认 collection 搜索本应用/profile/平台摘要属性，只允许一个匹配项，重复项拒绝。JSON 秘密最多2560字节；GNOME Keyring 返回的 `text/plain` 与 `application/octet-stream` 均可接受，内容仍须通过凭据记录校验。
- 使用规范的 `plain` session 算法，经本机、同用户的 Unix D-Bus 通道传输；这不是应用层加密。持久存储保护由 Secret Service 提供，不能防御同一系统用户下的恶意进程。参考 [Secret Service API](https://specifications.freedesktop.org/secret-service/latest/) 与 [plain 算法](https://specifications.freedesktop.org/secret-service/latest/ch07s02.html)。
- `/run/user/<uid>` 必须归当前用户且为0700；其中 `next-terminal-native` 目录同样要求0700。每个平台摘要使用0600的空 lock 文件，拒绝符号链接、非普通文件、其他所有者及硬链接；`flock` 串行化整个刷新事务，等待最多15秒。文件只含锁、不含令牌；使用期间不删除 lock 文件，以免不同进程锁住不同 inode。非 systemd 桌面如果没有此受保护运行目录，当前版本会拒绝记住登录。
- 复用 Windows 同一套明确授权、pending 恢复、单次刷新、远端撤权和注销语义。真实 GNOME Keyring 50 的隔离容器已测；KWallet、桌面交互解锁及不同桌面登录会话尚未实机验收。仅在同机浏览器完成 loopback 登录，不能把回调地址直接搬到另一台电脑。

```sh
chmod +x ./nt-client-keyring-linux-amd64
./nt-client-keyring-linux-amd64 login --server https://platform.example --remember --allow-connect --device-name 'Linux 工作站'
./nt-client-keyring-linux-amd64 assets --server https://platform.example --use-login
./nt-client-keyring-linux-amd64 logout --server https://platform.example
```

验收复现：Windows 工作区运行 `scripts/test-native-keyring.ps1`（首次构建 fixture 可加 `-BuildFixture`）。测试容器使用非 root 用户、独立 D-Bus、临时 keyring 和禁外网网络，只挂载测试程序及启动脚本，不挂载宿主密钥库。锁定测试在清除测试秘密后执行，结束销毁空的锁定 collection；没有声称客户端可自动解锁。

macOS 后续使用 Security.framework 的 [SecItemAdd](https://developer.apple.com/documentation/security/1401659-secitemadd) / [SecItemCopyMatching](https://developer.apple.com/documentation/security/secitemcopymatching(_:_:)) 等接口；仍需原生编译、Keychain 权限提示/拒绝、锁定、并发、签名及升级身份变化的真实 macOS 验收，交叉编译不能替代。

提供 `cmd/nt-client`（Go，Windows/Linux/macOS），与运维 `cmd/nt` 区分。桌面通过系统浏览器执行 OAuth Authorization Code + PKCE S256；loopback 回调固定 `http://127.0.0.1:{ephemeral-port}/callback`，端口可变，其余 URI 严格匹配。客户端无内置 client secret，校验 state，回调后立即关闭监听，code 60 秒且单次消费。流程按 [RFC 8252](https://www.rfc-editor.org/rfc/rfc8252) 实现，复用 M01 oauth_* 表和服务，不自造密码交换协议。

自有客户端固定 client_id=`nt-native-v1`，注册最小 scope `assets:read sessions:connect`。访问令牌 5 分钟，refresh 7 天且每次轮换，重用撤销整个 family；用户禁用/退出设备同步撤销。refresh 存 Windows Credential Manager/macOS Keychain/Linux Secret Service，无系统密钥库时要求每次登录，不落明文文件。无浏览器主机首版可以通过已登录桌面启动 bridge；设备授权模式作为 RFC 8628 扩展，不把现有 Web authorizeId 冒充标准 device_code。

OAuth 路由复用 `/api/oidc/server/authorize` 和 `/api/oidc/server/token` 对应的 M01 服务；内置客户端可用不依赖对第三方开放 OIDC Server 的开关。外部客户端注册、授权页面必须受 OIDC Server 开关约束。discovery/issuer 仍只有一套固定规范，不根据任意请求 Host 生成。

| 新接口 | 入参/返回 | 规则 |
|---|---|---|
| GET `/api/native/v1/assets?cursor=&limit=100` | `{items:[{id,name,alias,protocol,entries,revision}],nextCursor}` | 每次查询当前授权；不返回目标密码、网关凭据、无需暴露的内网地址 |
| POST `/api/native/v1/connect` | `{assetId,entry,clientVersion,idempotencyKey}` → `{sessionId,bridgeTicket,expiresAt,bridgeUrl,capabilities}` | M02 决策、资产 MFA、审批和入口能力校验；M06 预约；不满足返回结构化错误 |
| GET `/api/native/v1/bridge` Upgrade | Authorization: Bearer bridgeTicket；子协议 `nt-bridge.v1` | 消费成功后才建立代理连接；一次一会话，不接收客户端任意 host:port |
| DELETE `/api/native/v1/sessions/{id}` | 无 body，204 | owner 或管理权限；幂等关闭 |

错误码：`mfa_required, approval_required, policy_denied, entry_unsupported, ticket_expired, ticket_consumed, node_unavailable, quota_exceeded`。403 表示授权/策略拒绝，409 表示状态或重复消费，410 表示到期，503 表示临时不可用；返回 `{code,message,requestId}`，不泄露目标秘密。

bridgeTicket 使用 32 字节 CSPRNG，数据库仅存 HMAC-SHA256 摘要，kind=`native_bridge`、主体/资源/会话/入口/auth_epoch、60 秒 TTL。`UPDATE client_tickets ... WHERE consumed_at IS NULL AND expires_at > now() RETURNING ...` 原子消费，在同一应用事务复核授权并绑定预约。目标连接失败仍不恢复票据；客户端显式发起新 connect。重试同一幂等键返回同一预约状态，不重复产生可消费凭证；丢失票据需取消旧预约再新建。访问令牌、bridgeTicket、RDP ticket 三者不能互换。

WS 建立后使用单会话二进制字节流，终端 EOF/关闭使用受控控制帧或 WS Close；bridge 两端不支持可靠 TCP 半关闭时，明确关闭整个会话。要求半关闭的协议通过下述 Agent 记录封装。禁止压缩 WS（避免额外内存/敏感流混合），每连接有界缓冲，写阻塞 15 秒关闭并记录。不得在 URL query 中携带 bearer。

## 真实原生客户端落地

原生 SSH 先完成既有 M06 SSH 代理，用户名 `platformUser:assetAlias`，平台公钥或允许的交互认证；目标凭据由 M04 注入。CLI 的 HTTPS bridge 终点必须是此 SSH 代理或原生 RDP 代理，不能直接转发到目标端口绕过审计。bridge 采用绑定会话的内部认证上下文：代理只接受来自受认证桥接服务的 session lease，校验主体/资产/epoch；外部原生入口仍要求完整独立认证。

Termark 1.7.0 起公开 `termark://connect?host=...&port=22&username=...`。因此前端可 URL 编码平台 SSH 代理地址、2022 端口和上述用户名，唤起 Termark 后按普通 SSH 登录；禁止把 password/token 放入 URI。这是已知可开发的入口，仍需实机回归。[Termark 官方更新记录](https://www.termark.app/changelog)

仅允许出站 HTTPS 的环境：`nt-client connect --asset <id> --entry ssh` 监听随机 `127.0.0.1` 端口，单次接受连接，经 WSS 接入代理；显示只包含本地地址的 SSH/Termark 命令。监听默认 60 秒无人连接自动关闭，禁止绑定 0.0.0.0；本地进程争抢以短时单次 bridge 和客户端确认缓解，不将 loopback 当身份边界。RDP 同理桥接到 D03 并用一次性 NTICKET；.rdp 文件包含的票据仅短时有效，0600/用户 ACL，启动后清理。磁盘缓存、命令行及日志不得保存目标密码。

前置 HTTP Basic/custom Header/HTTP CONNECT/SOCKS5 使用客户端 transport 配置；域名级绑定敏感 Header，重定向默认拒绝以免泄露，保留服务端 TLS 校验，mTLS 客户端证书可配置。禁用普通 HTTP 的公网连接；本地开发 HTTP 必须显式 `--allow-insecure-local` 且仅回环。

## Agent 注册、身份与生命周期

自有 Agent 与官方 Agent 使用不同 protocolVersion。首版采用“单次注册令牌 → 专用设备 API Key”，匹配文档中的 API Key 操作模型；节点证书是可追加 mTLS 认证，不假定旧二进制可用。

1. 管理员创建 `agent_registration_tokens`：32 字节随机值、10 分钟、max_uses=1、允许网段/端口。只显示一次。
2. Agent POST `/api/agent/v1/enroll`：`{registrationToken,name,os,arch,version}`。事务原子消费，返回 `{agentId,deviceKey,endpoint,networkScope,credentialVersion}`。deviceKey 32 字节随机，只存摘要；设备文件 Linux0600/Windows 当前服务账户 ACL。安装器不将凭据放命令行。
3. WSS `/api/agent/v1/connect` 使用 Authorization Bearer deviceKey，子协议 `nt-agent.v1`。设备凭据仅能连接自己的通道，不能调用用户 API；按 agentId、来源限速。M05 校验后分配连接 epoch，旧连接停止接收新流并关闭，避免双 owner。
4. 在线轮换由旧凭据认证的 rotate 请求产生新版本；新通道成功确认后吊销旧版本，最多 5 分钟交叠；遗失凭据通过管理员重新注册。吊销立即关通道，数据库故障不得接受新连接。
5. 心跳 30 秒，90 秒判离线；活跃会话另受 15 秒授权租约约束，不能等 90 秒才撤权。重连 1–30 秒指数退避加随机抖动；TCP 会话不可跨重连透明恢复，向 M06 返回 interrupted。

数据模型：M05 `agent_gateway_configs` 增加 `credential_hash,credential_version,previous_credential_hash,previous_expires_at,connection_epoch,protocol_version`；原 cert_serial 可空。摘要密钥由 M04 管理，数据库不存可直接复用的设备凭据。注册失败/重复消费/吊销有 M08 审计。

## WSS 上的多路复用协议

采用 `coder/websocket.NetConn(ctx, ws, MessageBinary)` 上运行 yamux。Agent 是 yamux client，接入节点是 server；Agent 创建第一个流作为控制流，服务端创建拨号流。WS 仅做传输，不依赖其消息边界；yamux 上的应用消息显式分帧。库能力依据 [NetConn](https://pkg.go.dev/github.com/coder/websocket#NetConn) 和 [yamux 规范](https://github.com/hashicorp/yamux/blob/master/spec.md)；以下消息是本项目自有协议。

控制消息格式：4 字节大端长度 + UTF-8 JSON，最大 64 KiB；`{v:1,type,id,epoch,payload}`。类型 `hello,hello_ack,heartbeat,heartbeat_ack,metrics,lease_update,close_session,drain,error`；未知必需类型关闭连接，未知可选 capability 不启用。hello 协商版本/OS/能力，服务端返回生效网络范围与窗口。metric 单批最多 1,000 点，默认每 5 秒，满队列丢弃旧指标并计数，不阻塞拨号。

每个拨号流先发送同格式 JSON：`{v:1,type:"dial",requestId,sessionId,epoch,host,port,deadlineUnixMs,leaseExpiresAt,policyRevision}`；对端回复 `{type:"dial_result",requestId,ok,errorCode,resolvedIp}`。只接受控制面有当前 session lease 的拨号；用户不可自行构造内部 dial。Agent 本地按下发 allowlist 校验 hostname、解析后的所有候选 IP 和端口，实际连接固定被校验的 IP，禁止校验后重新 DNS 解析；metadata/link-local/loopback 默认拒绝，明确授权的本地服务另列精确例外。

拨号成功后记录格式 `type:u8 | length:u32BE | payload`，DATA=1（≤64 KiB），FIN=2（长度0，调用对端 TCP CloseWrite），RST=3（最多1 KiB结构化错误）；双向 FIN 后释放流，FIN 后 DATA 为协议错误。此层避免依赖不同 yamux/WS 实现对 half-close 的隐含行为。禁止从网络申请无界长度分配内存。

初始上限：每 Agent64 数据流、每流窗口256 KiB、单连接所有应用缓冲预算64 MiB、控制队列128项、拨号10秒、握手10秒、写停滞15秒。yamux 自带流控不替代总体内存和流数限制；阻塞仅影响该流，控制心跳使用独立调度。入口反向代理须支持 Upgrade、超时≥120秒且不缓存；Agent 的入站只需出站443。

lease_update 每5秒携带 sessionId/epoch/leaseExpiresAt，Agent仅接受当前epoch及不超过当前接收时间15秒的租约，按单调时钟设置本地deadline；不得用heartbeat续长会话授权。close_session包含sessionId/epoch/reason，幂等关闭；Agent回报关闭结果到控制流。各节点时钟漂移超过2秒时停止新预约并告警。

多跳由 M05 预编译 DialPlan，最大4跳、禁止环、每跳独立身份/超时；Agent 不提供任意 SOCKS 公网出口。会话租约到期时节点发送 RST，Agent 本地也按最近收到的租约截止时间关闭，控制通道分区不能无限续命。

## 自动更新与验收

升级使用 TUF 的 root/targets/snapshot/timestamp，发布清单按 OS/arch/digest 索引；客户端内置初始 root，验证版本、到期、目标长度/hash 后下载。自动检查10分钟，自有产品默认只通知；开启自动安装后先 drain 到0连接或维护窗再替换，旧版本保留一份。Windows 用独立更新服务替换停止的文件，Linux 使用同文件系统 rename；不执行服务端任意 shell 文本。[TUF 实现](https://github.com/theupdateframework/go-tuf)

测试必须覆盖：100 个并发 enroll 仅一个成功；旧 Key 轮换后失效；重复 epoch 不复活；错版本拒绝；64流满后拒绝第65流；畸形/超长记录；单流慢读不阻塞其他流；TCP半关闭；DNS重绑定；TLS中间人；代理认证和mTLS；断网恢复不重放命令；升级签名/过期/降级/健康检查失败。SSH 实机包括系统 OpenSSH 与 Termark；HTTPS bridge 包含公网无2022/3390出口环境。完整目录同步及官方 Agent 互通按总决策中的独立兼容任务验收。
