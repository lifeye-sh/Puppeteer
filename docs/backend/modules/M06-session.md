# M06 远程会话

统一管理从会话申请到连接、共享、监控与终止的生命周期。

状态：全量设计任务说明，模块未整体完成；阶段实现以[逐轮验收记录](../migration-rounds.md)为准。业务分组：资源与连接。首次交付批次：W2。目录：`backend/internal/modules/session`。

## 开发范围

- 会话预约、策略快照、节点归属、租约与断连
- SSH/Telnet 终端、浏览器 RDP/VNC 协议适配
- 分享参与者、只读能力、在线监控与管理员终止
- 原生 SSH 接入、原生客户端短期票据
- 原生 RDP 作为独立可关闭子功能

## 本模块不负责

- 不拥有录像表和历史审计数据
- 文件 CRUD 由 M07，网关拨号由 M05
- 不在掉线后自动重放输入或命令

## 交付切片

- 首次交付：SSH 会话、终止、授权复核和终端录屏接入。
- 后续交付：W3 图形/Telnet、分享、原生 SSH、自有客户端与 FreeRDP 基础链路；W4 RDP 策略/录像/RemoteApp 分 profile 验收。
- 完整范围以本卡功能清单为准，首次批次不代表所有扩展功能已经完成。

## 数据所有权

- `sessions`
- `session_grant_sources`
- `session_participants`
- `session_shares`
- `client_tickets`

仅本模块的 Repository 可以更改这些表。跨模块操作通过公开用例接口；共享事务由 application/workflows 编排，不直接传出数据库连接。

## 对外契约

- Session.Create/Attach/Disconnect/Join
- Session.GetAuthorizedContext
- ProtocolDriver.Connect/Resize/Close
- Recorder（由 M08 提供）
- ClientTicket.Issue/Consume
- NativeSSH.Authenticate/SelectTarget/Forward
- RDPTicket.Issue/Consume
- ClientAccess.ListCapabilities/OpenTunnel

这些是待实现的端口草案，不是已完成的 Go API。请求类型、错误、幂等键和授权前置条件必须在编码前补齐。

## 事件

- SessionRequested
- SessionConnected
- SessionClosed
- SessionInterrupted
- ParticipantChanged

事件 payload 仅包含必要 ID、revision、状态与脱敏信息。消费者不得把事件中的旧授权作为当前授权。

## 开发前置与集成

- [M01 身份与组织](M01-identity.md)
- [M02 权限与访问策略](M02-authorization.md)
- [M03 资源目录与主机资产](M03-resource.md)
- [M04 凭据与密钥](M04-vault.md)
- [M05 网关与网络连接](M05-connectivity.md)
- [M08 审计与录像](M08-audit.md)

前置表示开发顺序；运行时通过端口和应用层注入协作模块，不表示可绕过身份/权限检查。身份与权限同批次集成验收。涉及的跨模块流程见[模块开发规划](../module-development-plan.md)。

## 前端契约证据

- [src/api/session-api.ts](../../../src/api/session-api.ts)
- [src/api/portal-api.ts](../../../src/api/portal-api.ts)
- [src/pages/access/Terminal.ts](../../../src/pages/access/Terminal.ts)
- [src/pages/access/AccessGuacamole.tsx](../../../src/pages/access/AccessGuacamole.tsx)

同一个前端 API 文件可能聚合多个模块，不据文件名称判断所有权；每条路由的所有者在 OpenAPI operationId 与模块注册表中固定。

## 使用文档补充需求

来源与版本差异见[文档核对说明](../usage-docs-review.md)。下列仍是待开发要求。

### U10 原生 SSH 入口与受控转发

- 阶段：W3；协作：M01、M02、M03、M04、M05、M08。
- 功能要求：交互选资产、直连、用户公钥、可禁用密码认证；服务端 Host Key 独立于用户密钥；SSH 转发开关和 host:port 白名单单独控制。
- 验收条件：关闭转发仍能登录；绕过白名单的 forwarding 请求拒绝且审计；主机密钥持久化和轮换有指纹变更记录；普通 SSH 隧道不得冒充 SQL 协议审计。
- 来源：[SSH 代理服务器](https://www.next-terminal.com/zh/docs/usage/ssh-server.html)、[SSH 里的“认证私钥”到底是什么，它认证的是谁？](https://www.next-terminal.com/zh/docs/blog/ssh-authentication.html)、[系统属性配置参考](https://www.next-terminal.com/zh/docs/usage/system-properties.html)。

### U11 原生 RDP 一次性票据入口

- 阶段：W3-基础链路/W4-能力验收；协作：M01、M02、M03、M05、M08、M13。
- 功能要求：输出 .rdp 与 `NTICKET:<ticketId>:<secret>`；默认 TTL 300 秒、允许 60–3600 秒；默认监听 3390；独立对外地址；解析成功即消费；两段 NLA 行为分开处理。 按 D03.1 实现 FreeRDP 侧车、票据 hook、目标 TLS/NLA 和每会话隔离。
- 验收条件：并发消费只有一次成功；过期/已用/撤权/非 RDP 资产拒绝；失败重连重新签发；文件不含目标 Windows 密码；创建并结束审计会话。
- 来源：[RDP 代理服务器](https://www.next-terminal.com/zh/docs/usage/rdp-server.html)。

### U12 按入口发布会话能力矩阵

- 阶段：W3/W4；协作：M02、M07、M08。
- 功能要求：区分浏览器 SSH/RDP/VNC/Telnet、原生 SSH/RDP 和 Termark；共享、文件、剪贴板、监控、终止和录像分别声明能力。 本项目可新增原生 RDP 主动断连，只有 D03.2 验证通过才声明能力；未实现仍返回 unsupported。
- 验收条件：官方当前不支持的监控/主动断开不得冒充兼容；本项目终止扩展须D03.2实测后才返回成功；原生磁盘重定向不等同浏览器存储；无法执行必需策略时拒绝该入口。
- 来源：[浏览器访问工作区](https://www.next-terminal.com/zh/docs/usage/access.html)、[Windows / RDP 资产](https://www.next-terminal.com/zh/docs/usage/rdp.html)、[RDP 代理服务器](https://www.next-terminal.com/zh/docs/usage/rdp-server.html)、[VNC 与 Telnet 资产](https://www.next-terminal.com/zh/docs/usage/vnc-telnet.html)。

### U13 Termark 授权与两类传输

- 阶段：W3-基础链路/W4-能力验收；协作：M01、M02、M03、M05、M13。
- 功能要求：本地客户端授权、已授权 SSH/RDP 目录同步、直连代理或 WebSocket 隧道；截图还包含前置 Basic、自定义 Header、上游代理与 mTLS 选项。 按 D01 先提供公开 Termark URI 与自有 nt-client PKCE/HTTPS 接入；官方专有同步和帧协议单列 external_contract_required，原需求保留，不标兼容完成。
- 验收条件：仅开放 HTTPS 的网络中完成 SSH 与 RDP 接入；用户撤权立即影响同步和新接入；设备授权/帧格式未公开，保留独立真实客户端兼容验收，自有路径按D01先交付，客户端本地偏好不写入服务端业务表。
- 来源：[使用 Termark 接入 NextTerminal 资产](https://www.next-terminal.com/zh/docs/usage/termark.html)、[RDP 代理服务器](https://www.next-terminal.com/zh/docs/usage/rdp-server.html)。

协作项：U02（主责 M03）、U03（主责 M03）、U04（主责 M03）、U05（主责 M02）、U09（主责 M05）、U14（主责 M07）、U15（主责 M08）、U31（主责 M18）、U33（主责 M15）、U34（主责 M12）、U35（主责 M17）。

## 已确定的实施规范

先读[实施决策与开发门槛](../implementation-decisions.md)。本模块参与的接口、状态机、依赖和实机验收：

- [implementation/native-client-agent.md](../implementation/native-client-agent.md)
- [implementation/native-rdp.md](../implementation/native-rdp.md)
- [implementation/settings-entitlements.md](../implementation/settings-entitlements.md)

## 验收条件

- 会话 ID 不能作为裸访问凭证
- 只读输入、剪贴板和 resize 在服务端受限
- 租约过期后连接节点停止 I/O
- SSH、图形、原生 RDP 分别通过测试才开放能力

同时满足[统一交付标准](../module-development-plan.md#统一交付标准)。

---

由 `node scripts/backend-module-docs.mjs` 根据 `module-catalog.json` 、usage-requirements.json、usage-docs-sources.json 和脚本中的交付切片生成。修改功能、表归属、端口和验收项应先修改目录 JSON，再重新生成。
