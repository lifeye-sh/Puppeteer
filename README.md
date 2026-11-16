# Puppeteer

Puppeteer 是基于 Go、React 和 PostgreSQL 的远程运维与审计平台，统一管理资产、远程连接、数据库与网站访问、授权审批、录屏审计、备份和部署任务。

[English](README-en.md) · [项目概览](docs/PROJECT-OVERVIEW.md) · [发布版本](https://github.com/lifeye-sh/Puppeteer/releases/tag/beta0.11) · [安装说明](docs/ONE-CLICK-INSTALL.md)

## 当前版本

**beta0.11 测试版**，Linux amd64，数据库 schema 110、能力版本 90。server、web、graphics、renderer 四个组件镜像已发布，安装和升级脚本固定各组件摘要。

本版新增系统更新中心、管理员确认的网页自升级、自动准备镜像及维护窗口自动升级，包含签名校验、预检、独立进度、配套备份与失败恢复。自动升级默认关闭。详见[发布说明](deploy/production/beta0.11.md)、[镜像清单](deploy/production/dockerhub-beta0.11.json)和[系统更新说明](docs/SYSTEM-UPDATES.md)。

## 主要功能

| 功能 | 内容 |
| --- | --- |
| 资产与远程访问 | 资产、分组、凭据管理，SSH/RDP/VNC/Telnet 接入，Agent、跳板和网关 |
| 授权与审批 | 用户、部门、角色、批量资产授权，访问审批与 SQL 工单 |
| 数据库与网站 | MySQL/PostgreSQL 代理、数据库访问策略、网站代理和入口配置 |
| 审计与文件 | 会话审计、录屏回放、MP4 导出及授权范围内的文件交互 |
| 部署与运维 | 服务预检、持久化部署进度、组件启停、资源档位、备份恢复、证书与通知 |
| AI 助手 | 对话、模型与用量管理、经批准的受控工具，以及管理员专用的更新状态只读查询 |

AI 自主巡检、跨模块连续操作和全功能任务编排仍属[设计规划](docs/AI-OPERATIONS-AGENT-DESIGN.md)，不代表当前版本可以自动操作全部系统功能。

## 快速安装

准备 Linux amd64 服务器、Docker Engine、Compose v2、systemd 和 Python 3.9+。将示例 IP 替换为实际地址：

```bash
curl -fsSL https://raw.githubusercontent.com/lifeye-sh/Puppeteer/main/scripts/install.sh -o install-puppeteer.sh
sudo bash install-puppeteer.sh --host 192.168.1.100
```

完成后访问 `https://服务器IP/setup` 创建管理员，无预置账号或密码。默认生成自签名 HTTPS 证书；证书、端口和管理命令见[一键安装说明](docs/ONE-CLICK-INSTALL.md)。新安装默认配置绑定本机的 MySQL/PostgreSQL 代理端口，远程访问配置见安装说明。

资源档位提供 2 核/4 GiB、4 核/8 GiB、8 核/16 GiB 的部署预算目标，不是固定并发容量承诺。桌面分辨率、录屏及备份负载会影响实际资源需求，见[资源优化与验收边界](docs/RESOURCE-OPTIMIZATION.md)。

## 已有部署升级

一键安装创建的已有部署使用升级脚本，保留密钥、数据卷及原有组件启停状态；不要重复运行安装器。默认安装目录为 `/opt/puppeteer`：

```bash
curl -fsSL https://raw.githubusercontent.com/lifeye-sh/Puppeteer/main/scripts/upgrade.sh -o upgrade-puppeteer.sh
sudo bash upgrade-puppeteer.sh --dir /opt/puppeteer --version beta0.11 --check
sudo bash upgrade-puppeteer.sh --dir /opt/puppeteer --version beta0.11
```

升级会中断已有连接。beta0.10 及更早安装需要先执行这次脚本升级，接入新版宿主机执行器、冻结配置和独立进度入口；之后在 **系统运维 → 系统更新** 管理未来版本。

自动检查只获取版本信息，自动准备只准备镜像，维护窗口升级需要管理员明确授权版本上限、有效期及停机。网页自升级支持标准 Linux/systemd 受管部署、内部 PostgreSQL 18 和受管本地命名卷；Docker Desktop、外部数据库、WAL 专用布局及 RDP 共享盘等不支持的布局会被拒绝。详见[升级操作与恢复](docs/UPGRADE.md)。

## 网页内 RDP 与部署边界

浏览器连接 Windows 桌面使用 guacd 图形服务：目标 Windows 开启远程桌面，在平台创建 RDP 资产、填写连接信息并授权后，从“我的资产”连接。无需原生 RDP Worker，也无需开放平台的 3389/13389 端口；guacd 的 4822 和 MP4 渲染器的 8090 为内部端口。见[网页 RDP 指南](docs/BROWSER-RDP.md)。

服务部署先预检，再执行并显示进度；只检查端口占用及部署依赖，不修改防火墙、云安全组或 DNS。普通容器启动成功不等于执行器已接入，也不等于公网可达。

beta0.11 已通过实际发布镜像的隔离 Linux/systemd 全组件新安装及 beta0.10 升级验证；自升级验证覆盖维护窗口、执行器替换、独立进度和切换中断后的配套恢复。物理服务器断电、目标环境 AppArmor 及第二轮 10 小时混合负载复测不列为已完成，具体证据见发布说明。

## 文档导航

- [项目概览与组件职责](docs/PROJECT-OVERVIEW.md)
- [系统更新与自升级](docs/SYSTEM-UPDATES.md)
- [自动部署执行器](docs/AUTO-DEPLOYMENT.md) · [配置功能范围](docs/CONFIGURATION-AUTOMATION-ROADMAP.md) · [P2 专项运维](docs/OPERATIONS-AUTO-DEPLOYMENT.md)
- [批量访问授权](docs/BATCH-AUTHORIZATION.md) · [备份与恢复](docs/backend/implementation/backup-restore.md)
- [Docker Hub 镜像部署](deploy/production/dockerhub.md) · [生产部署配置](deploy/production/README.md) · [源码部署](docs/FRESH-INSTALL.md)
- [后端运行说明](backend/README.md) · [后端设计](docs/backend/README.md) · [历史开发与验收](docs/backend/migration-rounds.md)

## 开发与发布

生产部署使用 `deploy/production`；`backend/compose.yaml` 用于开发验收。源码版本见 [VERSION](VERSION)，发布状态见[版本元数据](deploy/production/source-release.json)。

先编写发布说明，再执行 `node scripts/version.mjs --set 版本号`；`node scripts/version.mjs --check` 校验源码、后端和数据库版本信息。版本脚本不会推送 Git 或镜像。完整发布还需镜像摘要清单、安装升级脚本、GitHub 标签与 Release，以及最后发布的签名更新索引，见[发布索引维护](docs/SYSTEM-UPDATES.md#发布索引维护)。

## 项目标识与许可

Puppeteer 独立维护源码、版本、部署和支持入口，仓库为 [lifeye-sh/Puppeteer](https://github.com/lifeye-sh/Puppeteer)。本项目不是同名的浏览器自动化库。Go 模块为 `puppeteer/backend`，应用数据目录为 `/var/lib/puppeteer`，Agent 安装目录为 `/opt/puppeteer-agent`；现有 `NT_*` 环境变量和 `nt` 命令作为稳定接口保留。

许可证与已有版权署名见 [LICENSE](LICENSE)。图形客户端采用 Apache Guacamole 官方源码，[依赖来源](backend/renderer/vendor/guacamole-common-js/SOURCE.json)记录版本与摘要，并附带其 LICENSE 和 NOTICE。
