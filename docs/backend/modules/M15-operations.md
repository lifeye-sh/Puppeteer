# M15 监控与诊断

展示系统健康、业务运行指标并提供受控诊断。

状态：全量设计任务说明，模块未整体完成；阶段实现以[逐轮验收记录](../migration-rounds.md)为准。业务分组：平台运营。首次交付批次：W2。目录：`backend/internal/modules/operations`。

## 开发范围

- 仪表盘、主机/数据库池/存储/会话指标
- 阈值、恢复条件、告警确认与状态聚合
- ping/tcping SSE、客户端来源 IP 诊断
- 系统版本与运行健康信息

## 本模块不负责

- 不拥有所有业务模块的统计真相
- 配置保存在 M18，IP 集和 GeoData 属于 M02
- 不会以监控权限赋予任意远程命令或扫描权限

## 交付切片

- 首次交付：基础仪表盘、健康指标、告警与受控诊断。
- 后续交付：随新协议注册聚合指标与告警规则。
- 完整范围以本卡功能清单为准，首次批次不代表所有扩展功能已经完成。

## 数据所有权

- `monitor_rules`
- `system_events`

仅本模块的 Repository 可以更改这些表。跨模块操作通过公开用例接口；共享事务由 application/workflows 编排，不直接传出数据库连接。

## 对外契约

- Monitoring.GetOverview/Acknowledge
- MetricProvider.Snapshot
- Diagnostics.Ping/TCPing
- Dashboard.QueryReadModel

这些是待实现的端口草案，不是已完成的 Go API。请求类型、错误、幂等键和授权前置条件必须在编码前补齐。

## 事件

- MonitorAlertChanged
- DiagnosticCompleted

事件 payload 仅包含必要 ID、revision、状态与脱敏信息。消费者不得把事件中的旧授权作为当前授权。

## 开发前置与集成

- [F00 公共基础设施](F00-platform.md)
- [M08 审计与录像](M08-audit.md)
- [M18 系统设置与产品能力](M18-system.md)

前置表示开发顺序；运行时通过端口和应用层注入协作模块，不表示可绕过身份/权限检查。身份与权限同批次集成验收。涉及的跨模块流程见[模块开发规划](../module-development-plan.md)。

## 前端契约证据

- [src/api/monitoring-api.ts](../../../src/api/monitoring-api.ts)
- [src/api/dashboard-api.ts](../../../src/api/dashboard-api.ts)
- [src/pages/sysops/ToolsPing.tsx](../../../src/pages/sysops/ToolsPing.tsx)
- [src/pages/sysops/ToolsTcping.tsx](../../../src/pages/sysops/ToolsTcping.tsx)

同一个前端 API 文件可能聚合多个模块，不据文件名称判断所有权；每条路由的所有者在 OpenAPI operationId 与模块注册表中固定。

## 使用文档补充需求

来源与版本差异见[文档核对说明](../usage-docs-review.md)。下列仍是待开发要求。

### U33 协议故障与指标来源

- 阶段：W2/W3；协作：M05、M06、M08。
- 功能要求：保留 Guacamole 数字错误码，诊断区分用户入口、代理、网关、目标认证、协议协商与存储；SSH 主机指标和 Agent 所在机器指标不得混用。
- 验收条件：514/522/776 等超时能定位不同层；769 不误判为平台已退出登录；命令不可用时指标 unknown；排障不会自动修改目标安全策略。
- 来源：[RDP/VNC 错误码说明](https://www.next-terminal.com/zh/docs/usage/error-codes.html)、[RDP 连不上/黑屏/凭证失效？Windows 远程桌面 5 步排查](https://www.next-terminal.com/zh/docs/blog/rdp-black-screen-failed.html)、[SSH 资产](https://www.next-terminal.com/zh/docs/usage/ssh.html)、[安全网关配置文件](https://www.next-terminal.com/zh/docs/usage/agent-gateway-config.html)、[快速开始](https://www.next-terminal.com/zh/docs/usage/readme.html)。

协作项：U01（主责 M18）、U07（主责 M05）、U08（主责 M05）、U15（主责 M08）、U20（主责 M18）、U30（主责 M18）。

## 已确定的实施规范

先读[实施决策与开发门槛](../implementation-decisions.md)。本模块参与的接口、状态机、依赖和实机验收：

- [implementation/native-client-agent.md](../implementation/native-client-agent.md)
- [implementation/settings-entitlements.md](../implementation/settings-entitlements.md)

## 验收条件

- 告警确认与故障恢复不是同一个动作
- 诊断目标/次数/时长受限，断开 SSE 取消任务
- 聚合故障不阻塞登录和现存远程会话
- 指标不带高基数用户或会话标签

同时满足[统一交付标准](../module-development-plan.md#统一交付标准)。

---

由 `node scripts/backend-module-docs.mjs` 根据 `module-catalog.json` 、usage-requirements.json、usage-docs-sources.json 和脚本中的交付切片生成。修改功能、表归属、端口和验收项应先修改目录 JSON，再重新生成。
