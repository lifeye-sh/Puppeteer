# M09 网站访问代理

完整管理网站资源定义及其认证反向代理业务。

状态：全量设计任务说明，模块未整体完成；阶段实现以[逐轮验收记录](../migration-rounds.md)为准。业务分组：审计与代理。首次交付批次：W3。目录：`backend/internal/modules/webproxy`。

## 开发范围

- 网站 CRUD、上游地址、入口域名、Header 与响应改写
- 公开/登录访问、访问口令、IP/地域/时段策略
- 临时 IP 授权、网站独立访问会话
- HTTP/WebSocket 流式转发、TLS/SNI 与错误页

## 本模块不负责

- 通用资源 ID、分组、标签由 M03 管理
- 证书材料由 M13，密钥由 M04
- 网站访问日志由 M08 持久化

## 交付切片

- 首次交付：网站资源、登录认证反向代理与访问审计。
- 后续交付：W3 同期完善公开访问、临时授权、响应改写和证书联动。
- 完整范围以本卡功能清单为准，首次批次不代表所有扩展功能已经完成。

## 数据所有权

- `websites`
- `website_temp_allows`
- `website_access_sessions`

仅本模块的 Repository 可以更改这些表。跨模块操作通过公开用例接口；共享事务由 application/workflows 编排，不直接传出数据库连接。

## 对外契约

- Website.Save/GetFacts
- WebsiteAccess.Authorize/IssueAccess
- ProxyRoute.Resolve
- ResponsePolicy.Apply
- WebsiteDomain.ValidateUnique
- TemporaryAccess.Grant/Renew/Revoke

这些是待实现的端口草案，不是已完成的 Go API。请求类型、错误、幂等键和授权前置条件必须在编码前补齐。

## 事件

- WebsiteChanged
- WebsiteTemporaryAccessGranted
- WebsiteAccessCompleted

事件 payload 仅包含必要 ID、revision、状态与脱敏信息。消费者不得把事件中的旧授权作为当前授权。

## 开发前置与集成

- [M02 权限与访问策略](M02-authorization.md)
- [M03 资源目录与主机资产](M03-resource.md)
- [M04 凭据与密钥](M04-vault.md)
- [M05 网关与网络连接](M05-connectivity.md)
- [M08 审计与录像](M08-audit.md)

前置表示开发顺序；运行时通过端口和应用层注入协作模块，不表示可绕过身份/权限检查。身份与权限同批次集成验收。涉及的跨模块流程见[模块开发规划](../module-development-plan.md)。

## 前端契约证据

- [src/api/website-api.ts](../../../src/api/website-api.ts)
- [src/api/website-temp-allow-api.ts](../../../src/api/website-temp-allow-api.ts)
- [src/api/http-proxy-api.ts](../../../src/api/http-proxy-api.ts)
- [src/api/portal-api.ts](../../../src/api/portal-api.ts)

同一个前端 API 文件可能聚合多个模块，不据文件名称判断所有权；每条路由的所有者在 OpenAPI operationId 与模块注册表中固定。

## 使用文档补充需求

来源与版本差异见[文档核对说明](../usage-docs-review.md)。下列仍是待开发要求。

### U17 Web 域名路由与登录回跳

- 阶段：W3；协作：M01、M02、M05、M13、M18。
- 功能要求：入口域名与上游地址分开；入口域名唯一且不能等于 SelfDomain；SelfProxyEnabled/SelfDomain/Root 决定登录入口；支持 HTTP 跳转 HTTPS、入口路径与网关。
- 验收条件：相同域名不同路径也不允许注册第二站点；未授权不能到达上游；回跳仅允许已登记站点；证书 SAN 覆盖域名，不只检查 CN。
- 来源：[Web 资产](https://www.next-terminal.com/zh/docs/usage/website.html)。

### U18 公开访问与临时 IP 白名单

- 阶段：W3；协作：M02、M08。
- 功能要求：匿名访问按 IP/CIDR/范围、国家省市、期限和口令判断；临时授权绑定站点与可信提取 IP，有 TTL 和 autoRenew 开关。
- 验收条件：关闭 autoRenew 时不续期；开启时仅成功访问续期并受本方案绝对期限约束；过期/撤销即失效；NAT 同出口共享效应可见。
- 来源：[Web 资产](https://www.next-terminal.com/zh/docs/usage/website.html)、[获取真实客户端 IP](https://www.next-terminal.com/zh/docs/install/real-ip.html)。

### U19 请求头与响应改写规则

- 阶段：W3；协作：M08、M18。
- 功能要求：按请求路径/方法/响应状态匹配；设置、追加、删除响应头和替换正文；支持自定义上游请求头与站点访问日志开关。
- 验收条件：压缩内容正确解压重算长度，限制可改 MIME/大小；WebSocket、流式和二进制内容不误改；不会把平台认证头泄漏给目标网站。
- 来源：[Web 资产](https://www.next-terminal.com/zh/docs/usage/website.html)。

协作项：U15（主责 M08）、U20（主责 M18）、U27（主责 M13）。

## 已确定的实施规范

先读[实施决策与开发门槛](../implementation-decisions.md)。本模块参与的接口、状态机、依赖和实机验收：

- [implementation/settings-entitlements.md](../implementation/settings-entitlements.md)

## 验收条件

- 网站与资源注册表原子创建
- 控制台 Cookie 和认证头不会泄露给上游
- 目标解析与实际拨号一致并受授权约束
- 大响应和 WebSocket 不经过无限缓冲改写

同时满足[统一交付标准](../module-development-plan.md#统一交付标准)。

---

由 `node scripts/backend-module-docs.mjs` 根据 `module-catalog.json` 、usage-requirements.json、usage-docs-sources.json 和脚本中的交付切片生成。修改功能、表归属、端口和验收项应先修改目录 JSON，再重新生成。
