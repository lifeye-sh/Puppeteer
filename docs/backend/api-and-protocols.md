# API 兼容与通信协议

## 契约来源与覆盖边界

外部 HTTP 路径以 `src/api` 的具体调用为主，同时检查页面中的 fetch、下载、SSE、WebSocket 和 XHR。完整调用点见 [生成清单](frontend-contract-inventory.md)，模型及参数表达式见 [JSON 清单](frontend-contract-inventory.json)。清单不是已完成的 OpenAPI，不推断不存在的请求体字段，不把继承基类的所有 CRUD 方法自动注册到后端。

后续实现要将已核实接口整理为 OpenAPI 3.1，按 method+path 去重，补齐请求/响应 schema、权限、错误码和成功示例。动态 path 模板中的 qs/encodeURIComponent 需要人工转换，不能把表达式直接当正式路由。

## HTTP 基本约定

| 内容 | 兼容要求 |
|---|---|
| 根路径 | `/api`，与 SPA 同源；开发代理指向 server，生产静态资源与 API 由统一入口承载 |
| 成功对象 | 直接返回对象/数组，不能增加 `{code,data}` 包装 |
| 分页 | `{items: [], total: 0}`；current 从 1 开始，pageSize 默认 10、上限建议 200；sort/filter 参数逐页面映射 |
| 无返回更新 | HTTP 204 无 body；不能发送 Content-Type JSON 然后返回损坏的空 JSON |
| 创建 | 普通资源 200/201 + 资源 DTO；前端返回类型为 void 的保持无体语义 |
| 后台任务 | 仅明确接收任务的接口返回 `{taskId,statusUrl}` 或 `{sessionId,statusUrl}`，按原类型区分 |
| 错误 | HTTP 非 2xx，JSON `{code:number,message:string}`；可扩展 traceId 和已被前端使用的引用列表 |
| 未登录 | 401 触发登录跳转；权限不足通常 403；未初始化使用 418 |
| 限流 | 429 + Retry-After；错误体仍遵守通用 schema |
| 编码 | UTF-8 JSON；multipart 上传不用手写 Content-Type boundary；下载/SSE/WS 不经过 JSON 包装 |
| 数字时间 | 默认 Unix 毫秒，对照 dayjs(Date)/Date.now；GeoData 字符串时间和 BackupFile.createdAt 保持 RFC3339 字符串 |
| 大整数 | 对可能超过 JS 安全整数的值采用字符串 DTO 或限定范围，不能无声丢精度 |
| 缓存 | 身份、秘密、审计、任务状态 no-store；品牌与静态 logo 可按 revision 缓存 |

兼容错误码 10010=来源 IP，10011=登录时段，10025=地域，10026=身份访问策略。前端会对这些码跳转访问拒绝页，所以不要把普通文件或资产授权错误复用为登录策略码。错误语义在 registry 固定，消息可本地化，代码本身不依赖语言。

静态路由 `/paging`、`/tree`、`/export`、`/selected` 等必须正确区别于 `/{id}`。大小写、单复数和历史拼写保持兼容，例如 `admin/authorised-asset`、`admin/authorised-website`、`admin/authorised-database-assets` 不是统一复数形式。

### 认证与 CSRF

浏览器请求默认同源 Cookie。所有管理写入和 WS upgrade 检查 Origin/Fetch Metadata，跨站拒绝；OAuth 回调按单独的 state/nonce 流程处理。Cookie 的 SameSite 不能单独代替服务端检查。对缺失 Origin 的浏览器兼容场景要求同源 Referer，无法验证则拒绝或走显式 API Key 通道。可新增 CSRF token，但要同步更新 requests 和上传层，不能无修改就要求当前前端发送不存在的头。

API Key 通道接受 `X-Auth-Token`，验证 scope；Cookie 与 Key 同时存在时拒绝身份不一致，并按较窄权限处理。Cookie 名不依赖前端实现。所有 GET 都避免业务写入；当前 website/access 等可能涉及签发访问能力，兼容期要求来源检查、短期票据且不扩大持久授权。

秘密查看接口返回 no-store，日志统一屏蔽 password/privateKey/passphrase/apiKey/securityToken/token/sharerToken。前端 URL 中出现令牌的兼容入口同时设置 Referrer-Policy: no-referrer；反向代理访问日志也必须过滤，不仅应用日志脱敏。

## 首批闭环接口

| 操作 | 请求与响应要求 | 后端服务 |
|---|---|---|
| 初始化 | `GET /api/setup-status`、`POST /api/setup-user`；SetupStatus 与 setup 表单 DTO；全局一次 | Bootstrap |
| 登录状态 | `GET /api/login-status`；status 为 Unlogged / OTP Required / Logged In | Identity |
| 登录 | `POST /api/login`，source/username/password/key；Set-Cookie，返回 token/needTotp | Identity |
| 当前账号 | `GET /api/account/info`；用户、menus、roles、permissions、MFA 状态 | Identity + Authorization |
| 能力 | `GET /api/license`；独立后端的能力兼容 DTO | Capability |
| 资产分页 | `GET /api/admin/assets/paging`；与 Asset 字段一致，秘密脱敏 | AssetCatalog |
| 可访问资产 | `GET /api/portal/assets?type=asset`、assets/tree/group-tree | Portal 投影 + Authorization |
| 创建会话 | `POST /api/portal/sessions`，body `{assetId}`，可选 X-Security-Token | Session |
| 获得已有会话 | `GET /api/portal/sessions/{id}?sharerToken=...` | Session + Share capability |
| 终端连接 | `WS /api/access/terminal?sessionId=...&cols=...&rows=...` | Terminal connector |
| 图形连接 | `WS /api/access/graphics?sessionId=...&width=...&height=...&dpi=...` | Graphics connector |
| 会话列表/断开 | `GET /api/admin/sessions/paging`、`POST /api/admin/sessions/{id}/disconnect` | Session + Audit |
| 录像 | `GET /api/admin/sessions/{id}/recording` / video-recording | Audit + Storage |

`ExportSession` 必须保留 id、protocol、assetName、strategy、url、watermark、readonly、idle、fileSystem、width、height、attrs。strategy 是文件/剪贴板能力 DTO，密码不在响应中。attrs 可包含 restricted-shell、sftp-directory-follow、backspaceMode，只返回前端需要的安全字段。

新设计中的连接预约、nodeId、epoch 和真实目标凭据仅在后端，不强迫旧前端加入 ticket 查询参数。Cookie+所有权控制即可兼容现有 sessionId 连接；若将来引入一次性浏览器连接 ticket，须显式更新前端与重连策略。

## 终端 WebSocket

当前协议不是 JSON，而是 **一位数字类型 + 内容字符串**。以 `src/pages/access/Terminal.ts` 的导出常量为准，其内嵌旧 Go 注释的编号已与真实常量不一致，不能复制注释实现后端。

| 首字符 | 类型 | 内容与方向 |
|---|---|---|
| 0 | Error | 服务端 → 客户端，用户可见错误文本 |
| 1 | Data | 双向 UTF-8 终端文本 |
| 2 | Resize | 客户端 → 服务端，`cols,rows`；限制合法尺寸 |
| 3 | Join | 服务端 → 客户端，分享参与者提示 |
| 4 | Exit | 服务端 → 客户端，参与者离开提示 |
| 5 | DirChanged | 服务端 → 客户端，目录跟随提示；不能因此自动授予目录访问权 |
| 6 | BinaryData | 双向 Base64 原始字节，用于 ZMODEM 等二进制数据 |
| 7 | AuthPrompt | 服务端 → 客户端，交互认证请求，详细 schema 按组件处理逻辑固定 |
| 8 | AuthReply | 客户端 → 服务端，交互认证响应；不可记录秘密 |
| 9 | Ping | 客户端发送毫秒时间戳；服务端回显以计算延迟，不推进输入空闲计时 |

示例：`2` + `120,40` 表示调整终端为 120 列 40 行；`1` + `ls\r` 表示输入。解析拒绝空帧、未知类型、超长帧、非法 Base64 和非法尺寸。该协议只支持 0..9 类型，新增双位类型必须版本协商，不能直接拼接 10。

AuthPrompt 的 JSON 内容含 `kind=username/password/passphrase/keyboard-interactive`，可选 message/retry；password 带 username，keyboard-interactive 带 `challenge:{name,instruction,questions,echos}`。AuthReply 的 JSON 按 kind 携 username、password、passphrase 或 answers[]。同一连接最多一个未完成挑战，校验回答数量与类型，超时/超次即断开。其余 Data 字节不得被解释成认证响应。

建议初值：文本帧 256 KiB、解码后二进制帧 256 KiB、每会话输出缓冲 1 MiB；超过限额背压或关闭，指标可见。帧限制须与实际 ZMODEM 分块协同测试，不可任意降低造成大文件失败。心跳 5 秒与控制租约分开，WebSocket TCP 活着不代表资源授权仍有效。

分享页面 TerminalPage 目前只处理 Data，主页面支持 BinaryData；分享终端应使用 UTF-8 文本投影或补前端二进制处理。监控连接服务器只输出，不接收输入；主会话退出或租约过期时广播结束并关闭所有参与者。关闭码与原因提供稳定映射，3886 的历史含义要保留兼容，不能用于所有错误。

## 图形 WebSocket 与原生客户端

图形接口使用 Guacamole 协议帧和其 WebSocket 子协议，不能复用终端前缀协议，也不能把每帧当 JSON。Go 端负责认证、连接编排、协议边界校验、能力过滤和流转发；guacd 负责 RDP/VNC 协议转换。握手包含会话参数、显示尺寸及协议版本，兼容当前前端 `@dushixiang/guacamole-common-js` 的定制行为需要真实互通验证。

分享 token 在 query 中校验，绑定 readonly 策略；服务器不能信任客户端 interactive=false。剪贴板通过 Guacamole stream 传输，按 mimetype、最大长度、copy/paste 权限控制。录像流回放需要支持播放器的读取和定位行为，使用当前 GuacdPlayback 做验收。

`POST /api/portal/rdp-proxy/tickets` 返回 ticketId、username、secret、expiresAt、proxyAddr、rdpFileUrl。ticket 是一次性短期客户端凭据；下载 .rdp 文件须鉴权/短期能力，文件不能包含目标永久密码。

**原生 RDP 采用 FreeRDP 3.32.2 代理侧车。** Go supervisor 管理每会话 C worker，服务端实现票据认证、目标 NLA/TLS、通道过滤和录屏。具体 hook、内部接口、进程隔离和实机门禁见 [D03](implementation/native-rdp.md)。仅提供 TCP 转发不能满足录屏要求；能力开关仍需实测通过后启用。

原生入口复用平台身份、资源授权与审计，但认证材料按入口区分：SSH 密码/公钥，MySQL 个人数据库密码，RDP 一次性票据。MySQL 是官方使用文档基线；PostgreSQL 代理为独立扩展。

## 文件上传与下载

| 路径或动作 | 契约 |
|---|---|
| `GET /api/access/filesystem/{fsId}/ls` | dir/hiddenFileVisible，返回 FileInfo[] |
| rm/mkdir/touch/rename/chmod | 保留现有 POST + query；路径参数一次解码，服务端根目录约束 |
| `POST .../{fsId}/edit` | JSON filename/fileContent，限长，UTF-8 编辑约束 |
| `POST .../{fsId}/upload/tasks` | 客户端 id、directory、filename、size；相同 owner+id 同内容幂等 |
| `POST .../{fsId}/upload?dir=...&taskId=...` | multipart file，完成目标落盘才返回 2xx |
| `GET .../{fsId}/upload/tasks/{id}` | created/receiving/transmitting/success/error/cancelled；进度是远端 written |
| `POST .../upload/tasks/{id}/cancel` | 取消原子状态变更，不能把已完成上传改成 cancelled |
| `DELETE .../upload/tasks/{id}` | 清理终态任务元数据，不能作为任意删除目标文件接口 |
| download | Content-Disposition 安全文件名，流式输出；每次校验 download 权限 |
| batch/download | 前端传 Base64 filenames；仅是编码，解码后仍逐路径校验，限制数量和压缩资源 |

现有 ls/rm 等页面部分 query 未统一 encodeURIComponent；包含 `&/#/+/?` 的文件路径可能歧义。后端无法可靠推断被错误切分的原始路径，应列为前端小改动：全部路径 query 使用 URLSearchParams 或 qs。

备份、证书、资产样例/导出、用户导入、logo 和 GeoData 上传分别定义 MIME、大小、权限与秘密处理，不共用一个可访问任意磁盘路径的文件下载 handler。证书下载可能含私钥，须提升权限和审计；不能因为前端只有普通链接就降低服务端保护，可先生成短时下载能力。

## SSE、Agent 与非 API 入口

当前 SSE：`/api/admin/tools/ping`、`/api/admin/tools/tcping`、`/api/admin/certificates/issued/log`。响应 `text/event-stream`，每条 `data: ...\n\n`，保持现有消费者读取的文本形式；comment 心跳不渲染。限制任务期限和次数，客户端断连取消操作，不允许自动重连创建无限探测任务。

Agent 客户端还要求 `GET /swagger/doc.json`，且当前脚本对该地址不携 Key。兼容实现公开不含敏感配置的完整 schema，再按 `/api/account/info` 提供实际 permissions；schema 的可见性不等于权限。另需提供 `/agent/next-terminal/...` 安装文件、`/api/agent/version` 和受控下载接口。对脚本中的旧产品名/二进制名进行明确适配，不能下载无校验的任意外部可执行文件。

OIDC discovery/JWKS/token/userinfo、ACME challenge、网站域名入口、Agent 控制入口及原生协议 listener 不在普通 API 清单的覆盖范围，要单独注册和测试。

前端 OidcServerSetting 已展示以下精确路径：`/api/.well-known/openid-configuration`、`/api/oidc/server/.well-known/jwks.json`、`/api/oidc/server/authorize`、`/api/oidc/server/token`、`/api/oidc/server/userinfo`。discovery 返回的 issuer 必须与正式外部访问地址一致，不能按任意 Host 请求头拼接；`/swagger/index.html` 也要提供可用的文档入口。

## AI WebSocket

`WS /api/ai/ws` 使用 JSON。入站保持 prompt、send_now、retry_from_message、tool_decision、cancel、reset、load_history、compact；出站保持 assistant_text、assistant_reasoning、tool_execution、usage、retrying、done、idle、error、user_message、history_loaded、conversation_created、conversation_rewound。

服务端接受 prompt 后，先持久化去重记录，再返回 user_message/clientMessageId，之后执行模型调用。tool_decision 在事务中校验执行状态、审批者、参数 hash 与有效期。重复审批返回现状，不重复执行工具。并发 cancel 与批准以服务端状态 CAS 决定，不以消息到达 UI 的顺序决定。

重连后前端加载历史；服务端提供持久化消息和工具状态，不假定客户端保存了所有 delta。可新增可选 eventId/sequence 字段与恢复游标，但旧客户端未发送游标时仍按 load_history 工作。retry_from_message 创建新分支并保存父关系，不删除已有执行审计；compact 只压缩上下文，不替代原始审计记录。

## 内部控制契约草案

内部接口不对浏览器开放，版本与数据库 schema 分开管理。共同 envelope：`request_id, actor_ref, session_id, node_id, lease_epoch, config_revision, deadline`；状态报告另有 event_id 和 monotonic sequence。

| 操作 | 输入 | 输出与幂等 |
|---|---|---|
| ReserveSession | sessionId、协议、DialPlan revision、配额 | reservationId、nodeId、expiresAt；requestId 去重 |
| ActivateSession | reservationId、epoch、认证绑定 | ready/failed；只允许一次主连接激活 |
| IssueCredentialLease | sessionId、targetId、secretVersion、purpose | 短期秘密或本节点可解密 envelope；同 session/epoch 绑定 |
| DisconnectSession | sessionId、epoch、reason | accepted/currentState；重复请求安全 |
| Heartbeat | nodeId、capacity、活动 session+epoch | lease duration、撤销列表；不能提升 epoch |
| ReportSessionEvents | eventId、sequence、state、recording manifest | 持久化 ACK；乱序检测、重复丢弃 |
| WatchCommands | nodeId、lastAck | 断连/配置/排空命令流；断开后重放未 ACK 事件 |
| OpenDataStream | sessionId、epoch、role、capability | 双向字节流；无后台业务事件广播 |

secret lease 仅批准进程和目标可使用，退出即清理内存引用；Go 运行时不保证所有敏感内存能立即物理擦除，因此同时使用进程隔离、禁用 core dump 和最少驻留时间。

## 兼容缺口与必须决策的地方

| 缺口 | 本方案处理 | 对前端影响 |
|---|---|---|
| 缺少完整服务端 OpenAPI | 静态证据转换为逐接口 schema，any 字段读表单补齐 | 不应凭空认定契约已经完整 |
| `userGroupId` 与 departmentId 混用 | 授权 DTO 适配历史名称，领域统一 department | 兼容期无需重命名全部页面 |
| duration/idle、某些枚举未严格定义 | 根据实际使用处确认单位与枚举后冻结；idle 设计为秒 | 若现有消费不同，适配层转换 |
| 配置以字符串/布尔值混合返回 | 字段注册表进行类型投影 | 保留 property-api 转换行为 |
| 轮换接口同步返回 RotationResult[] | 小批次有界同步执行；大批次新建异步接口 | 大批次任务进度需新增 UI |
| 恢复/证书下载等缺少显式二次验证流程 | 使用近期认证/能力票据；长期补二次确认组件 | 需要明确的交互补充 |
| 原生 RDP 与旧 Agent 实现不可见 | FreeRDP 侧车与自有 Agent v1 按 D02/D03 实施；官方旧 Agent 另列兼容任务 | 注册命令/说明及能力门控调整 |
| 上传接口不能直接改 202 | 保留完成时响应；新协议另行设计 | 无改动即可兼容当前 UI |
| SQL unknown 缺少核对界面 | 新增人工核对服务和审计，禁止自动重试 | 增加受控 reconcile 界面 |
| 版本许可证与功能粒度耦合 | 兼容 type，同时新增 capabilities | 分阶段替换按版本名称判断 |

这些缺口不影响先开发登录、资产、授权和 SSH 闭环，但必须在对应功能宣称可用之前解决。

## 使用文档确认后的接入契约

来源与冲突处理见[使用文档核对](usage-docs-review.md)，此节优先于早期自行假定的原生登录编码。

| 入口 | 外部契约 | 后端责任 |
|---|---|---|
| SSH 菜单 | `ssh username@host -p port`，默认代理端口 2022 | M01 认证，M06 菜单只列授权 SSH 资源；选择时再次复核 |
| SSH 直连 | `ssh username:asset-name@host -p port`；当前前端还支持别名提示 | M03 有界解析名称/别名，歧义拒绝，M06 不自行绕过目录鉴权 |
| SSH 转发 | SSH forwarding channel；全局开关及目标 host:port 列表 | 新建每条转发时重新检查；默认禁止远程监听、agent/X11 forwarding 等未明确实现的通道 |
| MySQL | `mysql -h host -P port -u username@asset_name -p` | M01 校验个人数据库密码，M10 解析唯一授权资产并使用托管目标凭据 |
| 原生 RDP | `POST /api/portal/rdp-proxy/tickets`，现有 DTO 不变 | 绑定用户、assetId、用途、有效期；秘密不写日志/URL访问日志；返回敏感响应禁缓存 |
| 客户端资产授权 | `GET /api/portal/access-authorizations/{authorizeId}`，`POST .../complete` / `.../cancel` | 保留 `assetId/protocol/expiresAt`、`redirectUrl` 外观；授权 challenge 绑定用户、客户端、回跳地址、一次性状态 |
| Termark | 上述授权流程及 SSH/RDP 代理，传输支持直连或 WS | 保留旧网页契约；公开 URI 先接原生 SSH，自有 nt-client 承担 PKCE/HTTPS 桥接；专有同步/帧协议按独立兼容任务验收，详见 [D01](implementation/native-client-agent.md) |
| OIDC | Discovery + 当前 `/api/oidc/server/*` | 文档正文 `/api/oauth2/*` 只能作显式兼容别名；Issuer/JWKS/签发状态统一 |

### RDP 票据与客户端文件

票据状态为 `issued → consumed` 或 `expired/revoked`。在认证解析成功时，通过条件更新原子消费；连接目标失败也不能恢复票据为 issued。消费事务绑定会话尝试 ID，失败记录访问审计；重新连接必须签发新票据。TTL 默认为 300 秒，输入限制 60–3600 秒。用户/资产被禁用、授权过期或 MFA 要求未满足时，签发和消费都拒绝。

`.rdp` 的用户名为 `NTICKET:<ticketId>:<secret>`。代理监听默认 `0.0.0.0:3390`，对外地址优先使用显式配置；自动推导只接受受信任请求主机，不允许任意 Host 注入。官方客户端段使用 `enablecredsspsupport:i:0`，代理到 Windows 段仍启用 NLA，不能将关闭 NLA 错误推广到目标 Windows。

剪贴板、磁盘与 RemoteApp 参数按适配器已实现能力和授权产生。官方示例启用 `redirectclipboard:i:1` 和 `drivestoredirect:s:*`；本方案若策略不允许，必须在代理端限制通道或拒绝此入口。普通浏览器 RDP 的映射盘权限和本机磁盘重定向是两个不同执行面。原生 RDP 返回 `canMonitor=false`、`canDisconnect=false` 直到扩展实现通过专项验收，旧接口应明确拒绝不支持的动作。

### Web、真实 IP 与错误协议

管理 API 的 IP 提取设置与 Web 反代设置独立，均经 F00 的纯解析组件，M18 提供配置。解析结果携带 peer_ip、client_ip、extraction_mode、trust_revision，供 M01 登录锁定、M02 IP 策略和 M08 审计一致使用。拒绝无效头和不可信对端的伪造来源，内部 dispatch 不重复信任未经认证的来源信息。

站点入口域名唯一，入口路径不作为另一个站点的唯一键。登录回调绑定目标站点和原请求；匿名公开策略仅用于匿名分支，登录用户走其授权检查，不能因无资源授权而自动降为匿名绕过。临时白名单在正确来源 IP 上激活，autoRenew 按配置决定；站点设置禁用访问日志不隐式禁用平台管理操作审计。

Guacamole 状态码保留原值，并增加可选 `reasonCode/stage/traceId`，不直接转成相同数字的 HTTP 状态：

| 数值 | 原协议含义 |
|---|---|
| 0 / 256 | SUCCESS / UNSUPPORTED |
| 512 / 513 / 514 / 515 | SERVER_ERROR / SERVER_BUSY / UPSTREAM_TIMEOUT / UPSTREAM_ERROR |
| 516 / 517 / 518 | RESOURCE_NOT_FOUND / RESOURCE_CONFLICT / RESOURCE_CLOSED |
| 519 / 520 | UPSTREAM_NOT_FOUND / UPSTREAM_UNAVAILABLE |
| 521 / 522 / 523 | SESSION_CONFLICT / SESSION_TIMEOUT / SESSION_CLOSED |
| 768 / 769 / 771 | CLIENT_BAD_REQUEST / CLIENT_UNAUTHORIZED / CLIENT_FORBIDDEN |
| 776 / 781 / 783 / 797 | CLIENT_TIMEOUT / CLIENT_OVERRUN / CLIENT_BAD_TYPE / CLIENT_TOO_MANY |

769 可能是目标桌面认证失败，不应一律清除平台登录 Cookie。诊断必须记录失败所在网络段和组件，但不得把目标密码、私钥或票据返回错误内容。

### 需新增或验证的接口范围

恢复码生成/再生成/消费、能力查询、数据库密码专用握手、Agent 更新包清单、CLI 应急审计、备份格式 inspect、SQL unknown 核对属于待设计或联调能力；具体路由冻结于各模块 OpenAPI，不在此伪造为现有端点。前端恢复码入口与原生会话不支持提示需随对应后端一起交付。
