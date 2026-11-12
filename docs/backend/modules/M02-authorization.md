# M02 权限与访问策略

统一决定某主体在特定环境下能对某资源做什么。

状态：全量设计任务说明，模块未整体完成；阶段实现以[逐轮验收记录](../migration-rounds.md)为准。业务分组：身份与安全。首次交付批次：W1。目录：`backend/internal/modules/authorization`。

## 开发范围

- 角色、操作权限、菜单权限投影
- 用户/部门对主机、网站、数据库和分组的授权
- IP、地域、时段、白名单/黑名单与授权有效期
- 文件/剪贴板操作策略、命令过滤策略
- IP 集维护与同步、GeoData 版本维护
- 权限变更 revision、可解释的决策结果

## 本模块不负责

- 不负责登录、远程连接或审批流程
- 不把交互终端字符串过滤作为强隔离沙箱
- 不直接读取其他模块数据库表获取身份/资产事实

## 交付切片

- 首次交付：角色操作权限、直接资源授权、基本策略决策。
- 后续交付：W3 完善时段/地域/IP 集、命令策略和部门继承场景。
- 完整范围以本卡功能清单为准，首次批次不代表所有扩展功能已经完成。

## 数据所有权

- `roles`
- `user_roles`
- `permissions`
- `role_permissions`
- `grants`
- `strategies`
- `command_filters`
- `command_filter_rules`
- `policy_groups`
- `policy_rules`
- `policy_rule_ip_sets`
- `policy_bindings`
- `ip_sets`
- `geodata_versions`

仅本模块的 Repository 可以更改这些表。跨模块操作通过公开用例接口；共享事务由 application/workflows 编排，不直接传出数据库连接。

## 对外契约

- Authorization.Evaluate/Explain
- Grant.Create/Revoke
- Role.Assign
- SubjectFacts（由 M01 提供）
- ResourceFacts（由 M03 注册表与资源拥有者提供）
- GeoResolver.Lookup

这些是待实现的端口草案，不是已完成的 Go API。请求类型、错误、幂等键和授权前置条件必须在编码前补齐。

## 事件

- GrantCreated
- GrantRevoked
- PolicyChanged
- RolePermissionsChanged
- SecurityDatasetPublished

事件 payload 仅包含必要 ID、revision、状态与脱敏信息。消费者不得把事件中的旧授权作为当前授权。

## 开发前置与集成

- [F00 公共基础设施](F00-platform.md)
- [M18 系统设置与产品能力](M18-system.md)

前置表示开发顺序；运行时通过端口和应用层注入协作模块，不表示可绕过身份/权限检查。身份与权限同批次集成验收。涉及的跨模块流程见[模块开发规划](../module-development-plan.md)。

## 前端契约证据

- [src/api/role-api.ts](../../../src/api/role-api.ts)
- [src/api/access-policy-api.ts](../../../src/api/access-policy-api.ts)
- [src/api/authorised-asset-api.ts](../../../src/api/authorised-asset-api.ts)
- [src/api/authorised-website-api.ts](../../../src/api/authorised-website-api.ts)
- [src/api/authorised-database-asset-api.ts](../../../src/api/authorised-database-asset-api.ts)
- [src/api/strategy-api.ts](../../../src/api/strategy-api.ts)
- [src/api/command-filter-api.ts](../../../src/api/command-filter-api.ts)
- [src/api/command-filter-rule-api.ts](../../../src/api/command-filter-rule-api.ts)
- [src/api/ip-set-api.ts](../../../src/api/ip-set-api.ts)
- [src/api/geodata-api.ts](../../../src/api/geodata-api.ts)

同一个前端 API 文件可能聚合多个模块，不据文件名称判断所有权；每条路由的所有者在 OpenAPI operationId 与模块注册表中固定。

## 使用文档补充需求

来源与版本差异见[文档核对说明](../usage-docs-review.md)。下列仍是待开发要求。

### U05 动态分组授权与逐动作控制

- 阶段：W1/W3；协作：M01、M03、M06、M07。
- 功能要求：用户/部门与资源/分组可组合授权；分组新增资产动态生效；到期采用服务器时间。上传、下载、创建文件/目录、编辑、删除、重命名、复制、粘贴分别决策。
- 验收条件：UI 隐藏按钮后直接请求 API 仍被拒绝；授权到期和撤销影响新接入及可控制的现存操作；文件 chmod 单列动作，不由编辑权限暗中推出。
- 来源：[资源授权与访问策略](https://www.next-terminal.com/zh/docs/usage/authorization.html)、[文件管理](https://www.next-terminal.com/zh/docs/usage/file-management.html)。

协作项：U01（主责 M18）、U02（主责 M03）、U10（主责 M06）、U11（主责 M06）、U12（主责 M06）、U13（主责 M06）、U14（主责 M07）、U17（主责 M09）、U18（主责 M09）、U20（主责 M18）、U22（主责 M10）、U26（主责 M01）、U30（主责 M18）、U31（主责 M18）、U32（主责 M18）、U34（主责 M12）。

## 已确定的实施规范

先读[实施决策与开发门槛](../implementation-decisions.md)。本模块参与的接口、状态机、依赖和实机验收：

- [implementation/native-client-agent.md](../implementation/native-client-agent.md)
- [implementation/settings-entitlements.md](../implementation/settings-entitlements.md)
- [implementation/database-auth.md](../implementation/database-auth.md)

## 验收条件

- 无授权默认拒绝，多个约束按已定义规则合并
- 直接访问隐藏菜单对应 API 仍然被拒绝
- 审批创建 grant 与审批记录在共享事务中原子提交
- 策略 revision 变化可触发现存连接重新检查

同时满足[统一交付标准](../module-development-plan.md#统一交付标准)。

---

由 `node scripts/backend-module-docs.mjs` 根据 `module-catalog.json` 、usage-requirements.json、usage-docs-sources.json 和脚本中的交付切片生成。修改功能、表归属、端口和验收项应先修改目录 JSON，再重新生成。
