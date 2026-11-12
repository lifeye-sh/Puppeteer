# M18 系统设置与产品能力

管理初始化状态、统一配置发布、品牌和功能可用性。

状态：全量设计任务说明，模块未整体完成；阶段实现以[逐轮验收记录](../migration-rounds.md)为准。业务分组：平台运营。首次交付批次：W0。目录：`backend/internal/modules/system`。

## 开发范围

- 初始化状态、版本信息、品牌、Logo 和外部链接
- 类型化设置注册表、校验、revision 与节点 ACK
- 产品能力、资源额度与前端 license DTO 适配
- 维护模式和功能启停的配置控制

## 本模块不负责

- setup 创建用户/角色由应用流程调用 M01/M02
- 各领域设置的业务校验仍归领域模块
- 不冒充官方许可证签发或验证实现

## 交付切片

- 首次交付：初始化状态、设置注册、品牌、能力门控与 CLI 骨架。
- 后续交付：随业务增加配置 schema、节点 ACK 和领域 CLI；103 个文档键逐项接入。
- 完整范围以本卡功能清单为准，首次批次不代表所有扩展功能已经完成。

## 数据所有权

- `instance_state`
- `settings`
- `settings_revisions`
- `settings_node_acks`
- `branding`
- `logo_assets`
- `entitlement_licenses`
- `entitlement_head`

仅本模块的 Repository 可以更改这些表。跨模块操作通过公开用例接口；共享事务由 application/workflows 编排，不直接传出数据库连接。

## 对外契约

- Settings.Get/Update/RegisterSchema
- Capabilities.Check/Get
- Branding.Get/Update
- Initialization.Get/MarkComplete
- CLI.RegisterCommands
- PropertyCompatibility.Get/Set/List

这些是待实现的端口草案，不是已完成的 Go API。请求类型、错误、幂等键和授权前置条件必须在编码前补齐。

## 事件

- SettingsChanged
- CapabilityChanged
- InstanceInitialized

事件 payload 仅包含必要 ID、revision、状态与脱敏信息。消费者不得把事件中的旧授权作为当前授权。

## 开发前置与集成

- [F00 公共基础设施](F00-platform.md)

前置表示开发顺序；运行时通过端口和应用层注入协作模块，不表示可绕过身份/权限检查。身份与权限同批次集成验收。涉及的跨模块流程见[模块开发规划](../module-development-plan.md)。

## 前端契约证据

- [src/api/property-api.ts](../../../src/api/property-api.ts)
- [src/api/branding-api.ts](../../../src/api/branding-api.ts)
- [src/api/license-api.ts](../../../src/api/license-api.ts)
- [src/api/logo-api.ts](../../../src/api/logo-api.ts)
- [src/api/access-setting-api.ts](../../../src/api/access-setting-api.ts)

同一个前端 API 文件可能聚合多个模块，不据文件名称判断所有权；每条路由的所有者在 OpenAPI operationId 与模块注册表中固定。

## 使用文档补充需求

来源与版本差异见[文档核对说明](../usage-docs-review.md)。下列仍是待开发要求。

### U01 首次使用闭环

- 阶段：W1；协作：M01、M02、M08、M15。
- 功能要求：初始化管理员、登录、创建并测试资产、用户授权、普通用户接入与审计核验组成一条交付用例。
- 验收条件：并发初始化只有一个成功；普通用户看不到未授权资源；一次连接能关联访问、会话和操作记录。
- 来源：[快速开始](https://www.next-terminal.com/zh/docs/usage/readme.html)。

### U20 两套真实 IP 配置与信任边界

- 阶段：W1/W3；协作：M01、M02、M09、M15。
- 功能要求：管理 API 与 Web 反代分别配置 direct/XFF/X-Real-IP 和可信代理；前者动态生效，反代 YAML 按文档需重启。
- 验收条件：直连伪造 XFF 不生效；多跳由右向左剥离可信代理；SelfProxy 联动测试；默认自动信任私网的官方行为与本方案显式信任差异必须公开。
- 来源：[获取真实客户端 IP](https://www.next-terminal.com/zh/docs/install/real-ip.html)、[Web 资产](https://www.next-terminal.com/zh/docs/usage/website.html)、[系统属性配置参考](https://www.next-terminal.com/zh/docs/usage/system-properties.html)。

### U30 运维 CLI 作为统一应用入口

- 阶段：W1/W3；协作：M01、M02、M13、M15、M08。
- 功能要求：新增 cmd/nt，覆盖 user list/passwd/otpclr/passkeyclr、sec list/delete、cert generate/revoke、config list/get/set、geodata download/-f、status/version/help/completion。
- 验收条件：CLI 复用模块用例与配置校验；应急离线模式限制 OS 身份并留审计，不能由 CLI 直接随意改表；密码使用交互或安全输入通道。
- 来源：[命令行工具参考](https://www.next-terminal.com/zh/docs/usage/cli.html)。

### U31 系统属性兼容注册表

- 阶段：W0/W4；协作：M01、M02、M06、M07、M08、M10、M14、M17。
- 功能要求：逐键登记官方 property 键、领域所有者、解析类型/单位、秘密标记、验证和生效方式；property string 兼容外观不改变内部类型化设置。 本项目设计默认值与官方未知默认值分字段保留；103键均定义类型/范围/effect；M18按 D06 实现revision/ACK及安全收紧。
- 验收条件：未知键拒绝写入；秘密默认掩码；UI/API/CLI 同规则；官方默认值未知须注明；本项目默认值与单位依据显式登记；废弃分组树键由资源模块迁移，避免两份权威数据。
- 来源：[系统属性配置参考](https://www.next-terminal.com/zh/docs/usage/system-properties.html)。

### U32 许可生命周期与能力分层

- 阶段：W0/W3；协作：M02、M07。
- 功能要求：设备标识、导入/绑定/更新状态、资产/用户/并发额度及到期展示；文件编辑、目录上传、批量下载、GeoIP 和高级策略独立能力项。 按 D05 提供 local 或本项目 Ed25519/JWS provider；不向官方服务伪造授权。
- 验收条件：实际实现能力、产品许可和用户授权取交集；外部许可校验协议未知则显示未接入；不得伪造官方许可证，也不把官方授权门户订单业务纳入本后端。
- 来源：[绑定授权许可](https://www.next-terminal.com/zh/docs/usage/license.html)、[资源授权与访问策略](https://www.next-terminal.com/zh/docs/usage/authorization.html)、[文件管理](https://www.next-terminal.com/zh/docs/usage/file-management.html)、[命令行工具参考](https://www.next-terminal.com/zh/docs/usage/cli.html)。

协作项：U08（主责 M05）、U14（主责 M07）、U16（主责 M08）、U17（主责 M09）、U19（主责 M09）、U25（主责 M01）、U26（主责 M01）、U29（主责 M16）、U35（主责 M17）、U36（主责 M14）。

## 已确定的实施规范

先读[实施决策与开发门槛](../implementation-decisions.md)。本模块参与的接口、状态机、依赖和实机验收：

- [implementation/backup-restore.md](../implementation/backup-restore.md)
- [implementation/settings-entitlements.md](../implementation/settings-entitlements.md)

## 验收条件

- 未注册设置键不能任意写入
- 未实现能力不能通过 license 字段伪装启用
- 配置更新失败的节点保留旧可用版本并报告
- 初始化状态和首个管理员创建原子提交

同时满足[统一交付标准](../module-development-plan.md#统一交付标准)。

---

由 `node scripts/backend-module-docs.mjs` 根据 `module-catalog.json` 、usage-requirements.json、usage-docs-sources.json 和脚本中的交付切片生成。修改功能、表归属、端口和验收项应先修改目录 JSON，再重新生成。
