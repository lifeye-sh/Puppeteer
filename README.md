# Puppeteer

Puppeteer 是基于 Go + PostgreSQL 的远程运维与审计平台，提供资产管理、SSH/RDP/VNC/Telnet 接入、文件交互、录屏回放、数据库代理、权限审批、自动化及 AI 助手。

[English](README-en.md)

## 项目状态与文档

- [后端设计与模块规划](docs/backend/README.md)
- [全新部署指南（从空白服务器安装）](docs/FRESH-INSTALL.md)
- [开发轮次与验收状态](docs/backend/migration-rounds.md)
- [R16 验收记录](docs/backend/acceptance/R16.md)
- [全新生产部署规划](docs/backend/frontend-migration-plan.md)
- [后端运行说明](backend/README.md)

R17 已完成约定的 Docker 模拟环境技术验收，当前按候选发布版本交付，不迁移旧数据。全新安装使用 `deploy/production` 配置；`backend/compose.yaml` 用于开发验收。实际生产开放及既定暂缓功能的范围见 [R17 验收记录](docs/backend/acceptance/R17.md)。

## 名称与兼容性

项目名称为 **Puppeteer**，前端包名为 `puppeteer`。此项目不是同名的浏览器自动化库。现有 `NT_*` 环境变量、`nt` 命令、Go 模块路径、协议标识、数据卷及安装路径保留兼容；工作区目录名不参与产品显示。

## 来源与许可

前端基于 [Next Terminal](https://github.com/next-terminal/next-terminal)，本工作区开发了独立 Go 后端。保留原有 [LICENSE](LICENSE)、版权及来源声明。原项目说明存档于 [docs/upstream](docs/upstream/next-terminal-README.md)；其中官网、演示和商业支持信息属于上游项目，不代表 Puppeteer 的服务。
