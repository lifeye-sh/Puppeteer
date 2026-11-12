# 模块开发任务索引

本索引按功能模块组织独立交付。W0..W4 是首次交付顺序，完整范围及后续扩展见各任务卡。所有状态均为“待开发”，没有后端实现进度被隐含标记为完成。

| 模块 | 首次批次 | 首次交付 | 后续范围 |
|---|---|---|---|
| [F00 公共基础设施](modules/F00-platform.md) | W0 | 事务、迁移、outbox/jobs、存储与测试底座 | 按实际负载增加扩展适配器 |
| [M01 身份与组织](modules/M01-identity.md) | W1 | 本地登录、用户部门、会话、基础 MFA 与 API Key | W3 完善 Passkey；W4 外部身份与 OIDC 提供方 |
| [M02 权限与访问策略](modules/M02-authorization.md) | W1 | 角色操作权限、直接资源授权、基本策略决策 | W3 完善时段/地域/IP 集、命令策略和部门继承场景 |
| [M03 资源目录与主机资产](modules/M03-resource.md) | W1 | 统一资源注册、主机、分组、标签与导入导出 | W3/W4 接入网站/数据库资源投影与复杂批量操作 |
| [M04 凭据与密钥](modules/M04-vault.md) | W1 | 秘密加密、凭据引用、短期租约与查看审计 | W3 轮换状态机与逐资产补偿 |
| [M05 网关与网络连接](modules/M05-connectivity.md) | W2 | 直连、SSH 跳板、节点登记与容量分配 | W3 自有 Agent、网关组、多跳和网络代理 |
| [M06 远程会话](modules/M06-session.md) | W2 | SSH 会话、终止、授权复核和终端录屏接入 | W3 图形/Telnet、分享、原生 SSH、自有客户端与 FreeRDP 基础链路；W4 RDP 策略/录像/RemoteApp 分 profile 验收 |
| [M07 文件与共享存储](modules/M07-files.md) | W2 | 会话 SFTP、上传下载、编辑与容量控制 | W3 共享存储、图形驱动目录与批量文件能力 |
| [M08 审计与录像](modules/M08-audit.md) | W0 | 管理/登录审计接口、事务写入及查询骨架 | W2 终端录像；W3 图形录像/转码；W4 SQL 审计接入 |
| [M09 网站访问代理](modules/M09-webproxy.md) | W3 | 网站资源、登录认证反向代理与访问审计 | W3 同期完善公开访问、临时授权、响应改写和证书联动 |
| [M10 数据库访问代理](modules/M10-database.md) | W4 | 数据库资产、原生协议访问与 SQL 审计 | W4 工单执行、结果核对及客户端协议完整性验证 |
| [M11 申请与审批](modules/M11-approval.md) | W3 | 临时访问申请、审批、取消和授权联动 | W4 与 M10 集成 SQL 工单和 unknown 人工核对 |
| [M12 任务调度与脚本](modules/M12-automation.md) | W3 | 脚本片段、远程任务、cron、幂等调度 | 各业务就绪后注册轮换/续签/备份处理器 |
| [M13 证书与身份凭据签发](modules/M13-pki.md) | W3 | TLS 导入/续签、DNS challenge 与证书发布 | W3/W4 用户客户端证书和吊销联动 |
| [M14 通知与消息投递](modules/M14-notification.md) | W3 | 渠道、绑定、事件规则和可靠投递 | 随审批、告警和其他模块上线增加事件类型 |
| [M15 监控与诊断](modules/M15-operations.md) | W2 | 基础仪表盘、健康指标、告警与受控诊断 | 随新协议注册聚合指标与告警规则 |
| [M16 备份与恢复](modules/M16-backup.md) | W3 | 可验证的手动备份与隔离恢复 | W3 定时、异地复制；生产部署前完成恢复演练 |
| [M17 AI 运维助手](modules/M17-ai.md) | W4 | 模型/对话、只读工具、去重和用量控制 | W4 经审批的写工具、取消、故障恢复和预算控制 |
| [M18 系统设置与产品能力](modules/M18-system.md) | W0 | 初始化状态、设置注册、品牌、能力门控与 CLI 骨架 | 随业务增加配置 schema、节点 ACK 和领域 CLI；103 个文档键逐项接入 |

## 数据归属索引

下列每张逻辑表只有一个写入所有者。原数据模型按主题分组，不能代替本归属表。

| 逻辑表 | 写入所有者 |
|---|---|
| `access_requests` | [M11 申请与审批](modules/M11-approval.md) |
| `agent_gateway_configs` | [M05 网关与网络连接](modules/M05-connectivity.md) |
| `agent_registration_tokens` | [M05 网关与网络连接](modules/M05-connectivity.md) |
| `ai_conversations` | [M17 AI 运维助手](modules/M17-ai.md) |
| `ai_events` | [M17 AI 运维助手](modules/M17-ai.md) |
| `ai_messages` | [M17 AI 运维助手](modules/M17-ai.md) |
| `ai_profiles` | [M17 AI 运维助手](modules/M17-ai.md) |
| `ai_tool_approvals` | [M17 AI 运维助手](modules/M17-ai.md) |
| `ai_tool_executions` | [M17 AI 运维助手](modules/M17-ai.md) |
| `ai_turns` | [M17 AI 运维助手](modules/M17-ai.md) |
| `ai_usage_ledger` | [M17 AI 运维助手](modules/M17-ai.md) |
| `api_tokens` | [M01 身份与组织](modules/M01-identity.md) |
| `approval_scopes` | [M11 申请与审批](modules/M11-approval.md) |
| `asset_access_logs` | [M08 审计与录像](modules/M08-audit.md) |
| `assets` | [M03 资源目录与主机资产](modules/M03-resource.md) |
| `audit_commands` | [M08 审计与录像](modules/M08-audit.md) |
| `audit_files` | [M08 审计与录像](modules/M08-audit.md) |
| `audit_logins` | [M08 审计与录像](modules/M08-audit.md) |
| `audit_operations` | [M08 审计与录像](modules/M08-audit.md) |
| `auth_challenges` | [M01 身份与组织](modules/M01-identity.md) |
| `auth_sessions` | [M01 身份与组织](modules/M01-identity.md) |
| `backup_files` | [M16 备份与恢复](modules/M16-backup.md) |
| `backup_tasks` | [M16 备份与恢复](modules/M16-backup.md) |
| `branding` | [M18 系统设置与产品能力](modules/M18-system.md) |
| `certificate_dns_bindings` | [M13 证书与身份凭据签发](modules/M13-pki.md) |
| `certificate_orders` | [M13 证书与身份凭据签发](modules/M13-pki.md) |
| `certificate_revocations` | [M13 证书与身份凭据签发](modules/M13-pki.md) |
| `certificates` | [M13 证书与身份凭据签发](modules/M13-pki.md) |
| `client_tickets` | [M06 远程会话](modules/M06-session.md) |
| `command_filter_rules` | [M02 权限与访问策略](modules/M02-authorization.md) |
| `command_filters` | [M02 权限与访问策略](modules/M02-authorization.md) |
| `credentials` | [M04 凭据与密钥](modules/M04-vault.md) |
| `database_assets` | [M10 数据库访问代理](modules/M10-database.md) |
| `database_passwords` | [M01 身份与组织](modules/M01-identity.md) |
| `database_sql_logs` | [M08 审计与录像](modules/M08-audit.md) |
| `db_connections` | [M10 数据库访问代理](modules/M10-database.md) |
| `db_work_orders` | [M11 申请与审批](modules/M11-approval.md) |
| `department_closure` | [M01 身份与组织](modules/M01-identity.md) |
| `departments` | [M01 身份与组织](modules/M01-identity.md) |
| `dial_plan_hops` | [M05 网关与网络连接](modules/M05-connectivity.md) |
| `dial_plans` | [M05 网关与网络连接](modules/M05-connectivity.md) |
| `dns_providers` | [M13 证书与身份凭据签发](modules/M13-pki.md) |
| `entitlement_head` | [M18 系统设置与产品能力](modules/M18-system.md) |
| `entitlement_licenses` | [M18 系统设置与产品能力](modules/M18-system.md) |
| `event_deliveries` | [F00 公共基础设施](modules/F00-platform.md) |
| `external_identities` | [M01 身份与组织](modules/M01-identity.md) |
| `filesystem_mounts` | [M07 文件与共享存储](modules/M07-files.md) |
| `gateway_group_configs` | [M05 网关与网络连接](modules/M05-connectivity.md) |
| `gateway_group_members` | [M05 网关与网络连接](modules/M05-connectivity.md) |
| `gateways` | [M05 网关与网络连接](modules/M05-connectivity.md) |
| `geodata_versions` | [M02 权限与访问策略](modules/M02-authorization.md) |
| `grants` | [M02 权限与访问策略](modules/M02-authorization.md) |
| `idempotency_records` | [F00 公共基础设施](modules/F00-platform.md) |
| `identity_providers` | [M01 身份与组织](modules/M01-identity.md) |
| `instance_state` | [M18 系统设置与产品能力](modules/M18-system.md) |
| `ip_sets` | [M02 权限与访问策略](modules/M02-authorization.md) |
| `job_runs` | [F00 公共基础设施](modules/F00-platform.md) |
| `jobs` | [F00 公共基础设施](modules/F00-platform.md) |
| `login_locks` | [M01 身份与组织](modules/M01-identity.md) |
| `logo_assets` | [M18 系统设置与产品能力](modules/M18-system.md) |
| `mfa_factors` | [M01 身份与组织](modules/M01-identity.md) |
| `mfa_recovery_codes` | [M01 身份与组织](modules/M01-identity.md) |
| `migration_history` | [F00 公共基础设施](modules/F00-platform.md) |
| `monitor_rules` | [M15 监控与诊断](modules/M15-operations.md) |
| `network_proxies` | [M05 网关与网络连接](modules/M05-connectivity.md) |
| `node_registry` | [M05 网关与网络连接](modules/M05-connectivity.md) |
| `notification_bindings` | [M14 通知与消息投递](modules/M14-notification.md) |
| `notification_channels` | [M14 通知与消息投递](modules/M14-notification.md) |
| `notification_deliveries` | [M14 通知与消息投递](modules/M14-notification.md) |
| `notification_rules` | [M14 通知与消息投递](modules/M14-notification.md) |
| `oauth_auth_codes` | [M01 身份与组织](modules/M01-identity.md) |
| `oauth_authorizations` | [M01 身份与组织](modules/M01-identity.md) |
| `oauth_client_bindings` | [M01 身份与组织](modules/M01-identity.md) |
| `oauth_clients` | [M01 身份与组织](modules/M01-identity.md) |
| `oauth_refresh_tokens` | [M01 身份与组织](modules/M01-identity.md) |
| `outbox_events` | [F00 公共基础设施](modules/F00-platform.md) |
| `permissions` | [M02 权限与访问策略](modules/M02-authorization.md) |
| `policy_bindings` | [M02 权限与访问策略](modules/M02-authorization.md) |
| `policy_groups` | [M02 权限与访问策略](modules/M02-authorization.md) |
| `policy_rule_ip_sets` | [M02 权限与访问策略](modules/M02-authorization.md) |
| `policy_rules` | [M02 权限与访问策略](modules/M02-authorization.md) |
| `recording_segments` | [M08 审计与录像](modules/M08-audit.md) |
| `recordings` | [M08 审计与录像](modules/M08-audit.md) |
| `resource_group_closure` | [M03 资源目录与主机资产](modules/M03-resource.md) |
| `resource_groups` | [M03 资源目录与主机资产](modules/M03-resource.md) |
| `resource_registry` | [M03 资源目录与主机资产](modules/M03-resource.md) |
| `resource_tags` | [M03 资源目录与主机资产](modules/M03-resource.md) |
| `role_permissions` | [M02 权限与访问策略](modules/M02-authorization.md) |
| `roles` | [M02 权限与访问策略](modules/M02-authorization.md) |
| `rotation_policies` | [M04 凭据与密钥](modules/M04-vault.md) |
| `rotation_policy_targets` | [M04 凭据与密钥](modules/M04-vault.md) |
| `rotation_runs` | [M04 凭据与密钥](modules/M04-vault.md) |
| `rotation_targets` | [M04 凭据与密钥](modules/M04-vault.md) |
| `scheduled_runs` | [M12 任务调度与脚本](modules/M12-automation.md) |
| `schedules` | [M12 任务调度与脚本](modules/M12-automation.md) |
| `secret_access_events` | [M04 凭据与密钥](modules/M04-vault.md) |
| `secret_versions` | [M04 凭据与密钥](modules/M04-vault.md) |
| `session_grant_sources` | [M06 远程会话](modules/M06-session.md) |
| `session_participants` | [M06 远程会话](modules/M06-session.md) |
| `session_shares` | [M06 远程会话](modules/M06-session.md) |
| `sessions` | [M06 远程会话](modules/M06-session.md) |
| `settings` | [M18 系统设置与产品能力](modules/M18-system.md) |
| `settings_node_acks` | [M18 系统设置与产品能力](modules/M18-system.md) |
| `settings_revisions` | [M18 系统设置与产品能力](modules/M18-system.md) |
| `signing_keys` | [M01 身份与组织](modules/M01-identity.md) |
| `snippets` | [M12 任务调度与脚本](modules/M12-automation.md) |
| `ssh_gateway_configs` | [M05 网关与网络连接](modules/M05-connectivity.md) |
| `ssh_known_hosts` | [M05 网关与网络连接](modules/M05-connectivity.md) |
| `storage_acl` | [M07 文件与共享存储](modules/M07-files.md) |
| `storages` | [M07 文件与共享存储](modules/M07-files.md) |
| `strategies` | [M02 权限与访问策略](modules/M02-authorization.md) |
| `system_events` | [M15 监控与诊断](modules/M15-operations.md) |
| `upload_tasks` | [M07 文件与共享存储](modules/M07-files.md) |
| `user_client_certs` | [M13 证书与身份凭据签发](modules/M13-pki.md) |
| `user_departments` | [M01 身份与组织](modules/M01-identity.md) |
| `user_preferences` | [M01 身份与组织](modules/M01-identity.md) |
| `user_roles` | [M02 权限与访问策略](modules/M02-authorization.md) |
| `user_ssh_keys` | [M01 身份与组织](modules/M01-identity.md) |
| `users` | [M01 身份与组织](modules/M01-identity.md) |
| `webauthn_credentials` | [M01 身份与组织](modules/M01-identity.md) |
| `website_access_logs` | [M08 审计与录像](modules/M08-audit.md) |
| `website_access_sessions` | [M09 网站访问代理](modules/M09-webproxy.md) |
| `website_temp_allows` | [M09 网站访问代理](modules/M09-webproxy.md) |
| `websites` | [M09 网站访问代理](modules/M09-webproxy.md) |
| `work_order_executions` | [M11 申请与审批](modules/M11-approval.md) |
