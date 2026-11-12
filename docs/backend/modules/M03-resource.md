# M03 资源目录与主机资产

提供统一资源身份、分组、标签和主机资产定义。

状态：全量设计任务说明，模块未整体完成；阶段实现以[逐轮验收记录](../migration-rounds.md)为准。业务分组：资源与连接。首次交付批次：W1。目录：`backend/internal/modules/resource`。

## 开发范围

- 资源注册表、分组树、标签、排序与名称快照
- SSH/Telnet/RDP/VNC 主机资产 CRUD
- 主机批量修改、导入导出、连通性检查请求、OS 发现请求
- 门户资源目录查询与资源事实接口

## 本模块不负责

- websites 表由 M09 拥有，database_assets 表由 M10 拥有
- 不保存明文凭据，不执行远程会话
- 检查和 OS 发现通过受控连接能力执行

## 交付切片

- 首次交付：统一资源注册、主机、分组、标签与导入导出。
- 后续交付：W3/W4 接入网站/数据库资源投影与复杂批量操作。
- 完整范围以本卡功能清单为准，首次批次不代表所有扩展功能已经完成。

## 数据所有权

- `resource_registry`
- `resource_groups`
- `resource_group_closure`
- `assets`
- `resource_tags`

仅本模块的 Repository 可以更改这些表。跨模块操作通过公开用例接口；共享事务由 application/workflows 编排，不直接传出数据库连接。

## 对外契约

- ResourceRegistry.Register/Update/Tombstone
- Asset.Get/Save/Import
- ResourceCatalog.List/GetFacts
- Probe（由 M05/M06 能力适配）
- AssetTarget.ResolveAuthorizedNameOrAlias

这些是待实现的端口草案，不是已完成的 Go API。请求类型、错误、幂等键和授权前置条件必须在编码前补齐。

## 事件

- ResourceRegistered
- ResourceChanged
- ResourceMoved
- ResourceDisabled

事件 payload 仅包含必要 ID、revision、状态与脱敏信息。消费者不得把事件中的旧授权作为当前授权。

## 开发前置与集成

- [F00 公共基础设施](F00-platform.md)
- [M18 系统设置与产品能力](M18-system.md)

前置表示开发顺序；运行时通过端口和应用层注入协作模块，不表示可绕过身份/权限检查。身份与权限同批次集成验收。涉及的跨模块流程见[模块开发规划](../module-development-plan.md)。

## 前端契约证据

- [src/api/asset-api.ts](../../../src/api/asset-api.ts)
- [src/api/portal-api.ts](../../../src/api/portal-api.ts)

同一个前端 API 文件可能聚合多个模块，不据文件名称判断所有权；每条路由的所有者在 OpenAPI operationId 与模块注册表中固定。

## 使用文档补充需求

来源与版本差异见[文档核对说明](../usage-docs-review.md)。下列仍是待开发要求。

### U02 按接入方式筛选资源目录

- 阶段：W2；协作：M02、M06。
- 功能要求：分组树、搜索、分页和数量统计均限定授权范围；浏览器、原生客户端分别返回支持的协议与能力。
- 验收条件：数据库资产不会被当成浏览器 SQL 会话；多标签独立建立或离开会话，不误结束其他标签连接。
- 来源：[浏览器访问工作区](https://www.next-terminal.com/zh/docs/usage/access.html)、[资产管理总览](https://www.next-terminal.com/zh/docs/usage/asset.html)。

### U03 资产类型化配置

- 阶段：W1/W3；协作：M05、M06。
- 功能要求：分别校验协议端口、账号模式、SSH 超时 1–300 秒、保活、环境变量、退格模式、OS 探测、RDP 域/安全/显示/音频/PDU/RemoteApp/驱动器，以及 WOL 网络参数。
- 验收条件：RDP/VNC 拒绝私钥账号模式；Telnet 使用交互登录且不创建 SFTP 能力；导入模板与单条保存使用同一校验器。
- 来源：[资产管理总览](https://www.next-terminal.com/zh/docs/usage/asset.html)、[SSH 资产](https://www.next-terminal.com/zh/docs/usage/ssh.html)、[Windows / RDP 资产](https://www.next-terminal.com/zh/docs/usage/rdp.html)、[VNC 与 Telnet 资产](https://www.next-terminal.com/zh/docs/usage/vnc-telnet.html)。

### U04 SSH 名称与别名解析

- 阶段：W3；协作：M06。
- 功能要求：别名以字母开头，仅含英文字母、数字、下划线、中划线；支持文档中的 username:asset-name 入口，并结合前端别名提示制定解析规则。
- 验收条件：本方案采用别名优先、名称兼容回退；只解析已授权 SSH 资产；重名或别名与名称碰撞时拒绝歧义，不静默选中另一主机。
- 来源：[资产管理总览](https://www.next-terminal.com/zh/docs/usage/asset.html)、[SSH 代理服务器](https://www.next-terminal.com/zh/docs/usage/ssh-server.html)。

协作项：U05（主责 M02）、U06（主责 M04）、U10（主责 M06）、U11（主责 M06）、U13（主责 M06）、U22（主责 M10）、U35（主责 M17）。

## 已确定的实施规范

先读[实施决策与开发门槛](../implementation-decisions.md)。本模块参与的接口、状态机、依赖和实机验收：

- [implementation/settings-entitlements.md](../implementation/settings-entitlements.md)

## 验收条件

- 资源与注册表创建同事务提交
- 分组移动不会产生环且会使权限事实失效
- 导出不含凭据明文，导入给出逐行错误
- 门户查询先限定可访问范围再分页，不泄露其他资产 total

同时满足[统一交付标准](../module-development-plan.md#统一交付标准)。

---

由 `node scripts/backend-module-docs.mjs` 根据 `module-catalog.json` 、usage-requirements.json、usage-docs-sources.json 和脚本中的交付切片生成。修改功能、表归属、端口和验收项应先修改目录 JSON，再重新生成。
