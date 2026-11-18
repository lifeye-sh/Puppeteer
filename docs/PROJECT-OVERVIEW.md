# Puppeteer 项目概览

更新日期：2026-11-18。当前发布 beta0.12 测试版，Linux amd64，数据库 schema 110、能力版本 90。

## 项目定位

Puppeteer 将远程运维的资产、身份、授权、连接、执行和审计集中到一个平台。管理员管理资源与策略，用户在授权范围内连接资产、提交访问申请或 SQL 工单，平台记录操作与执行结果。

项目独立维护源码、版本和部署，不是同名的浏览器自动化库。源码及发布入口为 [lifeye-sh/Puppeteer](https://github.com/lifeye-sh/Puppeteer)。

## 当前功能

| 领域 | 已提供能力 | 使用说明 |
| --- | --- | --- |
| 资产与连接 | 资产、分组、凭据、SSH/RDP/VNC/Telnet、Agent、跳板与网关 | [网页 RDP](BROWSER-RDP.md) |
| 身份与授权 | 用户、部门、角色、MFA、访问审批、多个主体与多个资源的批量授权 | [批量授权](BATCH-AUTHORIZATION.md) |
| 数据库与网站 | MySQL/PostgreSQL 代理、SQL 工单、网站代理及部署联动 | [代理部署](AUTO-DEPLOYMENT.md) |
| 审计与文件 | 会话记录、录屏回放、MP4 导出、受权限约束的文件操作 | [资源与转换优化](RESOURCE-OPTIMIZATION.md) |
| 备份与运维 | 恢复密钥管理、加密备份、复制、隔离恢复、通知及专项运维 | [备份恢复](backend/implementation/backup-restore.md)、[P2 运维](OPERATIONS-AUTO-DEPLOYMENT.md) |
| 系统更新 | 签名更新发现、预检、网页自升级、自动准备、维护窗口、任务历史与结果通知 | [系统更新](SYSTEM-UPDATES.md) |
| AI 助手 | 对话、模型与预算、工具提议/批准/执行，以及管理员更新状态只读查询 | [Agent 规划与现有差距](AI-OPERATIONS-AGENT-DESIGN.md) |

配置分为部署联动、外部依赖和业务策略。服务开关需结合配置应用、依赖就绪及实际服务状态判断；开启策略不等于对应服务或端口已经可用。完整范围见[配置与自动化说明](CONFIGURATION-AUTOMATION-ROADMAP.md)。

## 技术与组件

| 组件 | 职责 | 部署方式 |
| --- | --- | --- |
| server | Go API、鉴权、审计、连接与后台任务，镜像同时包含 CLI 和部署执行器 | `cloners/puppeteer-server:beta0.12` |
| web | React 管理界面、Nginx HTTPS 入口及进度路由 | `cloners/puppeteer-web:beta0.12` |
| graphics | guacd 图形连接组件，支持网页 RDP/VNC | `cloners/puppeteer-graphics:beta0.12` |
| renderer | MP4 渲染与导出 | `cloners/puppeteer-renderer:beta0.12` |
| PostgreSQL | 业务数据、授权、审计及任务记录 | 标准生产部署使用内部 PostgreSQL 18 |
| 宿主机执行器 | 在受管 Linux 主机执行部署预检、配置应用与独立升级任务 | 一键安装或升级登记 systemd 服务 |

安装与升级使用[发布清单](../deploy/production/dockerhub-beta0.12.json)中的固定摘要。图形与渲染组件按部署需求启停；网页 RDP 不要求原生 RDP Worker，内部组件端口不等于需要开放的公网入口。

Go 模块为 `puppeteer/backend`；前端位于 `src`，后端位于 `backend`，生产 Compose 位于 `deploy/production`。应用数据目录为 `/var/lib/puppeteer`；`NT_*` 环境变量与 `nt` CLI 作为稳定接口保留。

## 安装与升级路径

1. 新部署按[一键安装](ONE-CLICK-INSTALL.md)准备 Linux amd64、Docker Engine、Compose v2、systemd 和 Python 3.9+，安装后访问 `/setup` 创建管理员。
2. beta0.10 及更早的一键安装部署按[升级说明](UPGRADE.md)先升级至 beta0.11，接入新版执行器、冻结配置和独立进度入口。
3. 后续在“系统运维 → 系统更新”检查版本、预检并确认升级，或配置有版本上限、有效期和停机授权的维护窗口策略。

自动检查不下载镜像，自动准备不停止业务，自动安装默认关闭。网页自升级限制为受支持的受管布局；外部数据库、WAL 专用布局、RDP 共享盘等当前不支持的组合会明确拒绝。镜像发布本身不会替用户更新现有部署。

宿主机自动化检查端口占用和部署依赖，不管理防火墙、云安全组或 DNS。资源档位是部署预算，实际容量取决于连接数、桌面分辨率、录屏、备份和磁盘性能，见[资源预算](RESOURCE-OPTIMIZATION.md)。

## 已验证与待完成范围

beta0.11 已通过前端构建、相关回归、真实 PostgreSQL 集成，以及正式镜像的隔离 Linux/systemd 全组件新安装和 beta0.10 升级。自升级验证覆盖自动准备、维护窗口、执行器替换、后端停止期间进度查询和切换中断后的数据库及应用文件恢复。

这些记录不等同于物理服务器断电或任意生产布局验收。目标环境的 AppArmor、扩展故障演练及第二轮 10 小时混合负载复测不列为完成；具体范围见[发布说明](../deploy/production/beta0.11.md)。

完整智能运维 Agent 的自主巡检、任务级授权、跨模块编排和结果自动回传仍待开发。beta0.11 的更新状态只读工具沿用既有批准与审计流程，不能替代管理员升级确认。

## 文档与发布维护

当前版本以 [VERSION](../VERSION)、[发布元数据](../deploy/production/source-release.json)和[GitHub Release](https://github.com/lifeye-sh/Puppeteer/releases/tag/beta0.12)为准。R01–R17 文档保留历史设计和验收证据，不能仅凭历史阶段名称判断当前支持范围。

发布需同步源码版本、四个组件镜像与摘要清单、安装升级脚本、文档、GitHub 标签及 Release，最后发布签名更新索引。索引当前默认有效期 30 天，续期也需要递增序号并重新签名，私钥不得进入源码或镜像。详见[发布索引维护](SYSTEM-UPDATES.md#发布索引维护)。
