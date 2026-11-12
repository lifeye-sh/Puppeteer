# M13 证书与身份凭据签发

集中管理服务证书、客户端证书和签发吊销流程。

状态：全量设计任务说明，模块未整体完成；阶段实现以[逐轮验收记录](../migration-rounds.md)为准。业务分组：流程与自动化。首次交付批次：W3。目录：`backend/internal/modules/pki`。

## 开发范围

- TLS 证书导入、默认项、有效期与私钥匹配
- ACME 订单、DNS provider、自动续签与签发日志
- 用户客户端证书签发、下载、吊销
- 证书配置发布与旧证书安全退役

## 本模块不负责

- 不拥有用户账号；账户接口通过应用层调用本模块
- OIDC signing_keys 的轮换语义由 M01 拥有
- 秘密加密由 M04，定时触发由 M12

## 交付切片

- 首次交付：TLS 导入/续签、DNS challenge 与证书发布。
- 后续交付：W3/W4 用户客户端证书和吊销联动。
- 完整范围以本卡功能清单为准，首次批次不代表所有扩展功能已经完成。

## 数据所有权

- `user_client_certs`
- `certificates`
- `certificate_orders`
- `dns_providers`
- `certificate_dns_bindings`
- `certificate_revocations`

仅本模块的 Repository 可以更改这些表。跨模块操作通过公开用例接口；共享事务由 application/workflows 编排，不直接传出数据库连接。

## 对外契约

- Certificate.Import/Issue/Renew/Revoke
- ClientCertificate.Issue/Revoke
- CertificateMaterial.Lease
- DNSChallenge.Present/Cleanup
- ClientCertificate.ExportPKCS12/Validate

这些是待实现的端口草案，不是已完成的 Go API。请求类型、错误、幂等键和授权前置条件必须在编码前补齐。

## 事件

- CertificateIssued
- CertificateRenewalFailed
- ClientCertificateRevoked

事件 payload 仅包含必要 ID、revision、状态与脱敏信息。消费者不得把事件中的旧授权作为当前授权。

## 开发前置与集成

- [M01 身份与组织](M01-identity.md)
- [M04 凭据与密钥](M04-vault.md)
- [M08 审计与录像](M08-audit.md)

前置表示开发顺序；运行时通过端口和应用层注入协作模块，不表示可绕过身份/权限检查。身份与权限同批次集成验收。涉及的跨模块流程见[模块开发规划](../module-development-plan.md)。

## 前端契约证据

- [src/api/certificate-api.ts](../../../src/api/certificate-api.ts)
- [src/api/dns-provider-api.ts](../../../src/api/dns-provider-api.ts)
- [src/api/account-api.ts](../../../src/api/account-api.ts)
- [src/api/user-api.ts](../../../src/api/user-api.ts)

同一个前端 API 文件可能聚合多个模块，不据文件名称判断所有权；每条路由的所有者在 OpenAPI operationId 与模块注册表中固定。

## 使用文档补充需求

来源与版本差异见[文档核对说明](../usage-docs-review.md)。下列仍是待开发要求。

### U27 mTLS 用户证书与默认继承

- 阶段：W3；协作：M01、M04、M09、M16。
- 功能要求：自签/导入/ACME 服务证书；客户端证书生成及 .p12 下载、状态/指纹/序列号/最近使用、吊销重颁；站点证书优先于默认项。
- 验收条件：无证书、过期、吊销及其他 CA 证书不能通过；变更默认项展示受影响站点；证书通过不等于获得资源授权；CA 备份/恢复和轮换联动。
- 来源：[mTLS 双向认证](https://www.next-terminal.com/zh/docs/usage/mtls.html)、[Web 资产](https://www.next-terminal.com/zh/docs/usage/website.html)、[命令行工具参考](https://www.next-terminal.com/zh/docs/usage/cli.html)。

协作项：U11（主责 M06）、U13（主责 M06）、U17（主责 M09）、U28（主责 M16）、U30（主责 M18）。

## 已确定的实施规范

先读[实施决策与开发门槛](../implementation-decisions.md)。本模块参与的接口、状态机、依赖和实机验收：

- [implementation/native-client-agent.md](../implementation/native-client-agent.md)
- [implementation/native-rdp.md](../implementation/native-rdp.md)
- [implementation/backup-restore.md](../implementation/backup-restore.md)
- [implementation/settings-entitlements.md](../implementation/settings-entitlements.md)

## 验收条件

- 默认有效证书最多一个且切换原子
- 私钥不经过普通列表或事件暴露
- 客户端证书吊销能影响后续认证及应关闭的连接
- 续签失败保留旧有效证书并告警

同时满足[统一交付标准](../module-development-plan.md#统一交付标准)。

---

由 `node scripts/backend-module-docs.mjs` 根据 `module-catalog.json` 、usage-requirements.json、usage-docs-sources.json 和脚本中的交付切片生成。修改功能、表归属、端口和验收项应先修改目录 JSON，再重新生成。
