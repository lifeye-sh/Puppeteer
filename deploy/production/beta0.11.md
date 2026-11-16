# Puppeteer beta0.11

2026-11-16，Linux amd64，数据库 schema 110、能力版本 90。本次发布系统更新中心和受管部署自升级，不新增数据库迁移。

## 本次变更

- 系统运维新增系统更新：固定发布源、Ed25519 签名验证、版本与渠道检查、实际组件状态、预检和检查历史。
- 管理员确认升级：密码/MFA 校验，独立宿主机任务完成镜像准备、会话排空、配套备份、切换、验证及业务恢复；浏览器关闭不取消任务。
- 后端停止期间可通过独立进度入口继续查看状态；切换中断后按持久化阶段恢复，开放写入后禁止自动恢复旧备份覆盖新数据。
- 自动准备和维护窗口升级默认关闭；授权限定渠道、版本上限和最长 30 天有效期，停机需明确确认，失败后暂停。
- 增加任务历史、受保护的升级产物保留策略及管理员通知；AI 助手可通过批准后的只读工具查询更新状态。
- 安装和升级同步冻结标记、独立进度入口及宿主机执行器。四个组件统一版本；图形和渲染功能保持原有行为。

## 首次接入

已有 beta0.10 及更早安装需先使用受管升级脚本更新一次，不能仅替换后端镜像：

```sh
curl -fsSL https://raw.githubusercontent.com/lifeye-sh/Puppeteer/main/scripts/upgrade.sh -o upgrade-puppeteer.sh
sudo bash upgrade-puppeteer.sh --dir /opt/puppeteer --version beta0.11 --check
sudo bash upgrade-puppeteer.sh --dir /opt/puppeteer --version beta0.11
```

完成后进入“系统运维 → 系统更新”，检查版本并查看执行器状态。未来发布版本可在页面预检、确认或配置自动策略。新安装直接接入，无需新增公网端口。

网页自升级首批支持标准 Linux amd64/systemd、Python 3.9+、内部 PostgreSQL 18 及受管本地命名卷。Docker Desktop、外部数据库、WAL 专用布局、RDP 共享盘和不受支持的自定义挂载会在预检中拒绝。普通网页 RDP 不要求共享盘。

## 验证与边界

前端生产构建、80 项 Node 回归、相关 Go 单元测试通过；Python 回归 71 项，70 项通过、1 项跳过。真实 PostgreSQL 集成覆盖更新权限、AI 批准与撤销、通知去重与投递权限、冻结启动及加密备份恢复。

独立 systemd/Docker/PostgreSQL 验收通过自动准备、维护窗口升级、执行器替换和独立 Nginx 进度查询；切换后强制终止升级进程，重启后验证数据库与应用文件成套恢复。测试不等同于物理服务器断电验收；磁盘耗尽等扩展故障演练及此前第二轮 10 小时混合负载复测不列为已完成。

镜像和实际摘要见[发布清单](dockerhub-beta0.11.json)。操作、授权、保留与恢复边界见[系统更新](../../docs/SYSTEM-UPDATES.md)、[一键安装](../../docs/ONE-CLICK-INSTALL.md)和[升级说明](../../docs/UPGRADE.md)。AI 全功能运维 Agent 仍属独立规划，本版仅增加更新状态只读工具。
