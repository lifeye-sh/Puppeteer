# M01 身份与组织

管理谁可以登录、身份如何验证、用户属于哪些组织。

状态：全量设计任务说明，模块未整体完成；阶段实现以[逐轮验收记录](../migration-rounds.md)为准。业务分组：身份与安全。首次交付批次：W1。目录：`backend/internal/modules/identity`。

## 开发范围

- 用户、部门树、成员关系、个人资料与偏好
- 密码登录、验证码、MFA、Passkey、短信认证与登录会话
- API Key、用户 SSH 公钥、账号锁定与撤销
- LDAP、OIDC、企业微信登录、外部身份绑定
- OIDC/OAuth 服务端、客户端、授权同意与令牌生命周期

## 本模块不负责

- 角色权限、资源授权和 IP/地域策略由 M02 决定
- 客户端证书签发及吊销由 M13 执行
- 不直接修改资产、会话或审计表

## 交付切片

- 首次交付：本地登录、用户部门、会话、基础 MFA 与 API Key。
- 后续交付：W3 完善 Passkey；W4 外部身份与 OIDC 提供方。
- 完整范围以本卡功能清单为准，首次批次不代表所有扩展功能已经完成。

## 数据所有权

- `users`
- `departments`
- `department_closure`
- `user_departments`
- `auth_sessions`
- `auth_challenges`
- `mfa_factors`
- `webauthn_credentials`
- `user_ssh_keys`
- `api_tokens`
- `login_locks`
- `user_preferences`
- `identity_providers`
- `external_identities`
- `oauth_clients`
- `oauth_client_bindings`
- `oauth_authorizations`
- `oauth_auth_codes`
- `oauth_refresh_tokens`
- `signing_keys`
- `mfa_recovery_codes`
- `database_passwords`

仅本模块的 Repository 可以更改这些表。跨模块操作通过公开用例接口；共享事务由 application/workflows 编排，不直接传出数据库连接。

## 对外契约

- Identity.Authenticate/ResolveActor/Revoke
- Organization.GetSubjectFacts
- Preferences.Get/Update
- Federation.ExchangeCode/BindIdentity
- CredentialCipher（由 M04 实现）
- LoginPolicy（由 M02 实现）
- Identity.ConsumeRecoveryCode
- DatabaseIdentity.IssuePassword/VerifyHandshake/Revoke

这些是待实现的端口草案，不是已完成的 Go API。请求类型、错误、幂等键和授权前置条件必须在编码前补齐。

## 事件

- UserDisabled
- AuthSessionRevoked
- DepartmentMembershipChanged
- ExternalIdentityBound

事件 payload 仅包含必要 ID、revision、状态与脱敏信息。消费者不得把事件中的旧授权作为当前授权。

## 开发前置与集成

- [F00 公共基础设施](F00-platform.md)
- [M18 系统设置与产品能力](M18-system.md)

前置表示开发顺序；运行时通过端口和应用层注入协作模块，不表示可绕过身份/权限检查。身份与权限同批次集成验收。涉及的跨模块流程见[模块开发规划](../module-development-plan.md)。

## 前端契约证据

- [src/api/account-api.ts](../../../src/api/account-api.ts)
- [src/api/user-api.ts](../../../src/api/user-api.ts)
- [src/api/department-api.ts](../../../src/api/department-api.ts)
- [src/api/oidc-api.ts](../../../src/api/oidc-api.ts)
- [src/api/oidc-client-api.ts](../../../src/api/oidc-client-api.ts)
- [src/api/wechat-work-api.ts](../../../src/api/wechat-work-api.ts)

同一个前端 API 文件可能聚合多个模块，不据文件名称判断所有权；每条路由的所有者在 OpenAPI operationId 与模块注册表中固定。

## 使用文档补充需求

来源与版本差异见[文档核对说明](../usage-docs-review.md)。下列仍是待开发要求。

### U21 数据库专用密码

- 阶段：W4；协作：M04、M10。
- 功能要求：个人中心签发数据库用途凭据，与 API Token、平台密码和目标数据库密码分开；代理身份必须匹配凭据所属账号。 按D07采用TLS完整认证及Argon2id专用verifier；PG另存SCRAM材料。
- 验收条件：A 用户凭据不能给 B 用户认证；撤销后新连接拒绝；MySQL challenge-response 使用专用 verifier，不能拿随机 token 的普通摘要直接实现协议认证。
- 来源：[数据库审计](https://www.next-terminal.com/zh/docs/usage/database.html)。

### U24 TOTP 与恢复码

- 阶段：W1；协作：M04、M08。
- 功能要求：6 位/30 秒 TOTP，绑定前验证；同一时间步防重放；一次性恢复码和管理员恢复入口，恢复后要求重新绑定。
- 验收条件：并发 TOTP/恢复码只有一次消费成功；恢复码独立哈希且仅签发时展示；CLI 清理 MFA 留下操作者、原因和会话撤销审计；前端恢复码流程缺口单列联调任务。
- 来源：[OTP 一次性密码](https://www.next-terminal.com/zh/docs/usage/otp.html)、[命令行工具参考](https://www.next-terminal.com/zh/docs/usage/cli.html)。

### U25 Passkey 域与来源配置

- 阶段：W3；协作：M18。
- 功能要求：HTTPS、无协议和端口的 RP ID、明确 Origin 白名单；注册、登录、删除及 CLI 全部清理。
- 验收条件：跨 Origin/RP 的挑战拒绝，挑战一次性；清理用户全部 Passkey 后旧凭据不可用，不把浏览器删除成功当服务端已撤销。
- 来源：[通行密钥](https://www.next-terminal.com/zh/docs/usage/passkey.html)、[系统属性配置参考](https://www.next-terminal.com/zh/docs/usage/system-properties.html)、[命令行工具参考](https://www.next-terminal.com/zh/docs/usage/cli.html)。

### U26 OIDC 身份提供方完整闭环

- 阶段：W4；协作：M02、M04、M08、M18。
- 功能要求：Discovery/JWKS、授权同意、授权码/刷新/客户端凭据、scope 与用户部门范围、客户端密钥仅生成时展示、用户撤销同意与声明映射。
- 验收条件：issuer 固定且 discovery 端点自洽；redirect_uri 精确匹配；禁用客户端和撤销同意后刷新拒绝；正文 oauth2 路径与前端 oidc/server 差异须按兼容台账处理。
- 来源：[OIDC 单点登录完整配置 — 用 Next Terminal 作 IdP 对接 Proxmox VE](https://www.next-terminal.com/zh/docs/usage/oidc_server.html)、[系统属性配置参考](https://www.next-terminal.com/zh/docs/usage/system-properties.html)。

协作项：U01（主责 M18）、U05（主责 M02）、U06（主责 M04）、U10（主责 M06）、U11（主责 M06）、U13（主责 M06）、U17（主责 M09）、U20（主责 M18）、U22（主责 M10）、U27（主责 M13）、U30（主责 M18）、U31（主责 M18）。

## 已确定的实施规范

先读[实施决策与开发门槛](../implementation-decisions.md)。本模块参与的接口、状态机、依赖和实机验收：

- [implementation/native-client-agent.md](../implementation/native-client-agent.md)
- [implementation/settings-entitlements.md](../implementation/settings-entitlements.md)
- [implementation/database-auth.md](../implementation/database-auth.md)

## 验收条件

- MFA 中间态无法调用业务 API
- 相同邮箱不会自动绑定两个外部身份
- 禁用用户后会话及 API Key 的有效权限同步收缩
- 部门移动拒绝环；身份和组织事实可供权限模块独立测试

同时满足[统一交付标准](../module-development-plan.md#统一交付标准)。

---

由 `node scripts/backend-module-docs.mjs` 根据 `module-catalog.json` 、usage-requirements.json、usage-docs-sources.json 和脚本中的交付切片生成。修改功能、表归属、端口和验收项应先修改目录 JSON，再重新生成。
