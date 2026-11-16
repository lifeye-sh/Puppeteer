# Docker Hub 镜像部署

当前发布 `beta0.11`，Linux amd64，schema 110、能力版本 90。镜像与构建提交见 [发布清单](dockerhub-beta0.11.json)，验证范围见 [发布说明](beta0.11.md)。

| 组件 | 镜像 |
| --- | --- |
| 后端、CLI、部署执行器 | `cloners/puppeteer-server:beta0.11` |
| Web 前端 | `cloners/puppeteer-web:beta0.11` |
| 网页 RDP/VNC、共享文件 | `cloners/puppeteer-graphics:beta0.11` |
| MP4 渲染 | `cloners/puppeteer-renderer:beta0.11` |

安装器固定镜像摘要，自动生成私有配置、证书、Compose 管理脚本，并接入 systemd 部署执行器。无需在服务器安装 Node.js 或 Go。网页内 RDP 不需要原生 Worker；4822、8090 仅用于内部网络。

## 全新安装

准备 Linux amd64、Docker Engine、Compose v2、Python 3、systemd、flock、curl 和 OpenSSL；图形共享盘需要 FUSE，AppArmor 主机需要 apparmor_parser。替换下方示例域名：

```bash
curl -fsSL https://raw.githubusercontent.com/lifeye-sh/Puppeteer/main/scripts/install.sh -o install-puppeteer.sh
sudo bash install-puppeteer.sh --host puppeteer.example.com
```

完成后访问 `https://puppeteer.example.com/setup` 创建管理员。默认自签名证书；正式证书、端口及核心安装选项见 [一键安装](../../docs/ONE-CLICK-INSTALL.md)。没有预置账号或密码。

手工定制时，检出 `beta0.11`，按[全新部署](../../docs/FRESH-INSTALL.md)生成私有配置及配置证书；将 production.env 中四个镜像替换为清单的 pinnedReference。用 `dc pull` 替代 `dc build`，启动时使用 `--no-build`，随后完成指南中的执行器接管。全部 Compose 叠加文件、AppArmor 策略、数据卷与管理脚本必须保持一致。

## 已有部署升级

```bash
curl -fsSL https://raw.githubusercontent.com/lifeye-sh/Puppeteer/main/scripts/upgrade.sh -o upgrade-puppeteer.sh
sudo bash upgrade-puppeteer.sh --check
sudo bash upgrade-puppeteer.sh
```

默认目录 `/opt/puppeteer`；自定义目录增加 `--dir`。脚本拉取摘要镜像，在维护窗口备份配置、数据库和应用文件，同步标准组件镜像，保留数据、密钥及停用状态，更新执行器并检查健康。原手动 `/etc/puppeteer` 布局没有 puppeteerctl，需先按全新部署指南明确现有卷与配置，再制定接管方案，不能重新初始化已有数据。

数据库迁移后不可直接切回旧镜像，恢复步骤见[升级说明](../../docs/UPGRADE.md)。WAL/PITR 属于专项运维任务，使用固定 PG18 基础镜像构建，不是上述四个镜像的第五个常驻服务。脚本不改动防火墙、云安全组或 DNS。
