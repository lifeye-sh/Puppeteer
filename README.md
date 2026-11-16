# Puppeteer

Puppeteer 是基于 Go + PostgreSQL 的远程运维与审计平台，提供资产管理、SSH/RDP/VNC/Telnet 接入、文件交互、录屏回放、数据库代理、权限审批、自动化及 AI 助手。

[English](README-en.md)

## 项目状态与文档

| 交付 | 版本与范围 |
| --- | --- |
| 当前版本 | **beta0.9**，schema 110、能力版本 90；包含 P0–P2 自动化和独立项目标识，见 [发布说明](deploy/production/beta0.9.md) |
| Docker 镜像 | **beta0.9**，server、web、graphics、renderer；安装和升级脚本固定各组件摘要 |

Linux 一键安装和升级会接入匹配的部署执行器。已有部署使用升级脚本，保留密钥和数据卷；不要重新运行安装器。源码版本与发布状态见 [VERSION](VERSION) 和 [版本元数据](deploy/production/source-release.json)。

**快速安装：** 已安装Docker的Linux amd64服务器，替换服务器IP后执行：

```bash
curl -fsSL https://raw.githubusercontent.com/lifeye-sh/Puppeteer/main/scripts/install.sh -o install-puppeteer.sh && sudo bash install-puppeteer.sh --host 192.168.1.100
```

新安装默认配置本机MySQL/PostgreSQL代理端口；远程访问可加 `--database-proxy-bind 0.0.0.0` 并限制安全组来源。旧安装补齐方式见[升级说明](docs/UPGRADE.md#同时补齐数据库代理)。

完成后访问`https://服务器IP/setup`创建管理员。默认自动生成自签名HTTPS证书；正式证书、端口及管理命令见[一键安装说明](docs/ONE-CLICK-INSTALL.md)。

- [已有部署版本升级（备份与健康检查）](docs/UPGRADE.md)
- [网页内 RDP：准备、创建资产与排错](docs/BROWSER-RDP.md)
- [自动部署执行器与预检进度](docs/AUTO-DEPLOYMENT.md)
- [资源优化：档位、按需渲染、数据库预算与任务调度（未发布）](docs/RESOURCE-OPTIMIZATION.md)

beta0.9 网页 RDP 新增省资源、均衡、清晰三档及分阶段连接诊断；MP4 转换在资源不足时保留排队并自动恢复。第二轮 10 小时稳定性复测尚未完成。使用方式与验收边界见 [资源优化说明](docs/RESOURCE-OPTIMIZATION.md)。
- [配置菜单及 P0–P2 功能范围](docs/CONFIGURATION-AUTOMATION-ROADMAP.md)
- [P2 专项运维](docs/OPERATIONS-AUTO-DEPLOYMENT.md)
- [后端设计与模块规划](docs/backend/README.md)
- [全新部署指南（从空白服务器安装）](docs/FRESH-INSTALL.md)
- [Docker Hub 镜像安装（免编译）](deploy/production/dockerhub.md)
- [开发轮次与验收状态](docs/backend/migration-rounds.md)
- [R16 验收记录](docs/backend/acceptance/R16.md)
- [全新生产部署规划](docs/backend/frontend-migration-plan.md)
- [后端运行说明](backend/README.md)

全新源码安装使用 `deploy/production`，`backend/compose.yaml` 用于开发验收。rc13 已在隔离 Docker/systemd 环境验证完整组件新安装、rc11 核心部署从 schema 107 升级到 110，以及执行器自动接管；升级保留数据、密钥和数据卷。远程整机与 AppArmor 内核访问控制仍需在目标环境验证，具体范围见发布说明。

## 网页内 RDP

只在浏览器连接 Windows 桌面时，使用 guacd 图形服务。目标 Windows 开启远程桌面，创建 RDP 资产并填写目标地址、端口和凭据，授权后从“我的资产”连接。

**无需原生 RDP Worker，也无需开启“原生 RDP”或开放平台的 3389/13389 端口。** “当前节点未部署原生 RDP Worker 接入配置”仅描述 mstsc 等原生客户端能力，不影响网页内 RDP。guacd 的 4822 和 MP4 的 8090 仅用于内部网络。完整步骤见 [网页内 RDP](docs/BROWSER-RDP.md)。

## 自动部署与版本维护

rc13 将配置分为服务部署、外部依赖、业务策略、状态与发布。服务操作先预检，再显示持久化任务进度；失败给出原因并执行适用的恢复流程。自动化检查端口占用和部署依赖，不修改防火墙、云安全组或 DNS。普通 Compose 启动成功不代表执行器已经接入。

维护源码版本时先编写对应发布说明，再执行 `node scripts/version.mjs --set beta0.9`；`node scripts/version.mjs --check` 校验源码、后端、schema 和能力版本。脚本管理源码元数据，不推送 Git、创建标签或发布镜像，不修改安装器及配置生成器的镜像默认值。

## 项目标识

Puppeteer 独立维护源码、版本、部署和支持入口，项目仓库为 [lifeye-sh/Puppeteer](https://github.com/lifeye-sh/Puppeteer)。本项目不是同名的浏览器自动化库。Go 模块为 `puppeteer/backend`，应用数据目录为 `/var/lib/puppeteer`，Agent 安装目录为 `/opt/puppeteer-agent`。现有 `NT_*` 环境变量和 `nt` 命令作为稳定接口继续使用。

## 许可

许可证和已有版权署名见 [LICENSE](LICENSE)。图形客户端使用 Apache Guacamole 官方源码，版本与摘要记录在 [依赖来源](backend/renderer/vendor/guacamole-common-js/SOURCE.json)，附带其 LICENSE 和 NOTICE。
