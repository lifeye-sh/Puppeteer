# M05 网关与网络连接

提供经过授权的可达路径和目标拨号能力。

状态：全量设计任务说明，模块未整体完成；阶段实现以[逐轮验收记录](../migration-rounds.md)为准。业务分组：资源与连接。首次交付批次：W2。目录：`backend/internal/modules/connectivity`。

## 开发范围

- SSH 跳板、Agent 网关、网关组、HTTP/SOCKS5 代理
- DialPlan 编译、默认链继承、循环检测、健康与熔断
- 自有 Agent 注册、证书身份、主动隧道与受限拨号
- 连接节点登记、容量、排空与心跳
- 网络位置相关 WOL 与探活能力

## 本模块不负责

- 不决定资源授权，不管理终端业务会话
- 不把旧 nt-tunnel 二进制协议视为已知兼容
- 节点通道不提供不受限的任意命令执行接口

## 交付切片

- 首次交付：直连、SSH 跳板、节点登记与容量分配。
- 后续交付：W3 自有 Agent、网关组、多跳和网络代理。
- 完整范围以本卡功能清单为准，首次批次不代表所有扩展功能已经完成。

## 数据所有权

- `dial_plans`
- `dial_plan_hops`
- `gateways`
- `ssh_gateway_configs`
- `agent_gateway_configs`
- `agent_registration_tokens`
- `gateway_group_members`
- `gateway_group_configs`
- `network_proxies`
- `node_registry`
- `ssh_known_hosts`

仅本模块的 Repository 可以更改这些表。跨模块操作通过公开用例接口；共享事务由 application/workflows 编排，不直接传出数据库连接。

## 对外契约

- DialPlan.Compile/Resolve
- Transport.Dial
- Gateway.Register/Revoke
- NodeRegistry.ReserveCapacity/Drain
- AgentConfig.Validate
- AgentRelease.Check/Download

这些是待实现的端口草案，不是已完成的 Go API。请求类型、错误、幂等键和授权前置条件必须在编码前补齐。

## 事件

- GatewayOnlineChanged
- GatewayRevoked
- DialPlanChanged
- NodeDraining

事件 payload 仅包含必要 ID、revision、状态与脱敏信息。消费者不得把事件中的旧授权作为当前授权。

## 开发前置与集成

- [M03 资源目录与主机资产](M03-resource.md)
- [M04 凭据与密钥](M04-vault.md)

前置表示开发顺序；运行时通过端口和应用层注入协作模块，不表示可绕过身份/权限检查。身份与权限同批次集成验收。涉及的跨模块流程见[模块开发规划](../module-development-plan.md)。

## 前端契约证据

- [src/api/ssh-gateway-api.ts](../../../src/api/ssh-gateway-api.ts)
- [src/api/agent-gateway-api.ts](../../../src/api/agent-gateway-api.ts)
- [src/api/agent-gateway-token-api.ts](../../../src/api/agent-gateway-token-api.ts)
- [src/api/gateway-group-api.ts](../../../src/api/gateway-group-api.ts)
- [src/api/network-proxy-api.ts](../../../src/api/network-proxy-api.ts)

同一个前端 API 文件可能聚合多个模块，不据文件名称判断所有权；每条路由的所有者在 OpenAPI operationId 与模块注册表中固定。

## 使用文档补充需求

来源与版本差异见[文档核对说明](../usage-docs-review.md)。下列仍是待开发要求。

### U07 安全网关注册与生命周期

- 阶段：W3；协作：M04、M15。
- 功能要求：Agent 从内网主动连接 WebSocket；服务端提供各 OS 安装指引、网关专属注册凭证、在线状态、撤销、重连及版本发布能力。 按 D02 实现一次性注册令牌、设备 Key 轮换、WSS/yamux 和epoch fencing。
- 验收条件：仅开放 Web 入口即可连接内网目标；吊销后无法新建流；旧 nt-tunnel 二进制互通需专项验证，自有协议不得标成官方兼容。
- 来源：[安全网关](https://www.next-terminal.com/zh/docs/usage/agent-gateway.html)、[安全网关配置文件](https://www.next-terminal.com/zh/docs/usage/agent-gateway-config.html)。

### U08 Agent 配置和升级契约

- 阶段：W3；协作：M15、M18。
- 功能要求：根 endpoint、API Key、TLS 校验、自定义 Header、HTTP/SOCKS5/SOCKS5H 出站代理、网关名称、采集/心跳间隔、网卡正则、磁盘白名单和自动更新配置均纳入版本化 schema。 自有 Agent 按 D02 采用 TUF；官方自动更新 true 仅为来源值，本项目自动安装默认 false 并明确展示差异。
- 验收条件：保留文档默认值及优先级清单；校验代理 host/port；采集 5 秒、心跳 30 秒；白名单覆盖黑名单，挂载点精确匹配；升级包校验、失败回滚和暂停升级属于本方案增强。
- 来源：[安全网关配置文件](https://www.next-terminal.com/zh/docs/usage/agent-gateway-config.html)。

### U09 入口 SSH 与出站 SSH 跳板分离

- 阶段：W2；协作：M04、M06。
- 功能要求：SSH 网关通过跳板的端口转发访问目标，安全网关通过主动隧道访问目标，资产地址从最后一跳解释。
- 验收条件：跳板禁用 AllowTcpForwarding 时返回具体失败阶段；M05 不拥有平台 SSH 登录会话，M06 不复制网关配置。
- 来源：[SSH 网关](https://www.next-terminal.com/zh/docs/usage/ssh-gateway.html)、[安全网关](https://www.next-terminal.com/zh/docs/usage/agent-gateway.html)、[资产管理总览](https://www.next-terminal.com/zh/docs/usage/asset.html)。

协作项：U03（主责 M03）、U06（主责 M04）、U10（主责 M06）、U11（主责 M06）、U13（主责 M06）、U17（主责 M09）、U22（主责 M10）、U33（主责 M15）。

## 已确定的实施规范

先读[实施决策与开发门槛](../implementation-decisions.md)。本模块参与的接口、状态机、依赖和实机验收：

- [implementation/native-client-agent.md](../implementation/native-client-agent.md)
- [implementation/native-rdp.md](../implementation/native-rdp.md)
- [implementation/settings-entitlements.md](../implementation/settings-entitlements.md)

## 验收条件

- 循环链和越权目标拨号被拒绝
- 删除有引用的网关返回前端需要的关联列表
- Agent 撤销后不能重新拨号，已有流被关闭
- 故障切换不迁移或重放现存远程操作

同时满足[统一交付标准](../module-development-plan.md#统一交付标准)。

---

由 `node scripts/backend-module-docs.mjs` 根据 `module-catalog.json` 、usage-requirements.json、usage-docs-sources.json 和脚本中的交付切片生成。修改功能、表归属、端口和验收项应先修改目录 JSON，再重新生成。
