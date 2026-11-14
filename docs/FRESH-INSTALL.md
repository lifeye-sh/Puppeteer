# Puppeteer 全新部署指南

适用版本：`r17-rc11` 候选发布版本（schema107）。部署方式：单台 Linux amd64 主机、Docker Compose、全新 PostgreSQL 数据卷，不迁移旧系统数据。

最快方式是[一条命令安装](ONE-CLICK-INSTALL.md)。需要手动选择组件时使用[Docker Hub免编译安装](../deploy/production/dockerhub.md)；本文以下步骤用于源码构建。

本文按“源码构建 → 初始化配置 → 安装证书 → 启动 → 创建管理员 → 验证”的顺序执行。Docker 模拟环境已验收；正式环境请使用自己的域名、证书及独立密钥。仓库内的开发夹具、测试账号和 `backend/compose.yaml` 不用于正式安装。

## 1. 准备环境

需要预先安装：Git、Node.js 24（含 npm）、Docker Engine、Docker Compose v2 插件。本文命令使用 Bash，执行账户需要 sudo 权限及 Docker 构建权限。Go 和 PostgreSQL 工具由镜像构建提供，无需在宿主机安装。

```bash
git --version
node --version
npm --version
sudo docker version
sudo docker compose version
```

资源按启用组件分配：核心配置文档基线为 2 CPU / 4 GiB；启用图形和 MP4 后，模板额外允许图形服务使用 2 GiB、渲染器使用 2 GiB / 2 CPU，不能仍按核心配置预留容量。构建、镜像、数据库、录屏和备份均需磁盘空间，请同时检查 Docker 数据目录所在磁盘。容量实测范围见[容量基线](../deploy/production/capacity.md)。

准备一个解析到服务器的域名，例如 `puppeteer.example.com`，以及对应证书链和私钥。内网部署可使用内网 CA，但访问设备必须信任该 CA。本文域名、证书源文件路径均须替换为实际值。

仅管理网页需要 TCP 443；核心部署不对宿主机开放数据库或 API 端口。首次创建管理员前，将入口限制为管理员来源地址，创建完成后再放行使用者。

## 2. 获取源码并构建前端

选择一个普通用户可写的持久目录执行：

```bash
git clone https://github.com/lifeye-sh/Puppeteer.git
cd Puppeteer
git checkout r17-rc11
git rev-parse HEAD
npm ci
npm run release:frontend
npm run verify:frontend
```

记录上述提交号，便于定位安装版本。前端发布目录为 `dist/production`，后续 web 镜像构建依赖它。不要使用开发服务器或 `npm run dev` 对外提供服务。后续命令均从此仓库根目录执行。

## 3. 生成全新的私有配置

```bash
sudo node scripts/production-config.mjs /etc/puppeteer https://puppeteer.example.com 443
```

`/etc/puppeteer` 必须不存在。命令随机生成数据库密码、MFA/凭据加密密钥和 MP4 通信密钥，不提供默认管理员密码。生成结果包括：

| 路径 | 用途 |
| --- | --- |
| `/etc/puppeteer/production.env` | 镜像、HTTPS 来源、绑定地址、端口 |
| `/etc/puppeteer/secrets/` | 数据库连接和应用密钥 |
| `/etc/puppeteer/tls/` | 网页入口的证书及私钥 |

生成器拒绝覆盖已有配置。安装中断后先查明原因，不能删除配置重新生成密钥来连接已有数据库，否则已加密内容可能无法解密。配置目录应独立加密备份；不要提交到 Git 或发送到聊天中。

## 4. 安装证书并设置入口

以下源路径为示例；`fullchain.pem` 应包含服务器证书及所需中间证书：

```bash
sudo install -o 101 -g 101 -m 0400 /secure/fullchain.pem /etc/puppeteer/tls/server.crt
sudo install -o 101 -g 101 -m 0400 /secure/privkey.pem /etc/puppeteer/tls/server.key
sudoedit /etc/puppeteer/production.env
```

初始配置只监听 `127.0.0.1`。服务器直接提供网页时，在防火墙已限制访问来源的前提下，将配置改为：

```dotenv
PUPPETEER_ORIGIN=https://puppeteer.example.com
PUPPETEER_BIND=0.0.0.0
PUPPETEER_HTTPS_PORT=443
```

也可把 `PUPPETEER_BIND` 设为服务器指定内网 IP。如果使用 8443，则来源应为 `https://puppeteer.example.com:8443`，HTTPS 端口改为 `8443`；证书仍须覆盖该域名。`PUPPETEER_ORIGIN` 不带路径或末尾斜杠。

## 5. 选择组件并构建镜像

### 核心安装：资产、SSH/SFTP、权限与审计

在当前 Bash 会话定义命令，固定项目名、配置目录及叠加文件：

```bash
dc() {
  sudo docker compose --env-file /etc/puppeteer/production.env -p puppeteer \
    -f deploy/production/compose.yaml "$@"
}
dc build
sudo node scripts/production-preflight.mjs /etc/puppeteer/production.env
```

### 图形与 MP4：浏览器 RDP/VNC、共享文件、MP4 下载

如果需要这些功能，使用下面的 `dc` 定义替代核心定义：

```bash
sudo modprobe fuse
test -c /dev/fuse
dc() {
  sudo docker compose --env-file /etc/puppeteer/production.env -p puppeteer \
    -f deploy/production/compose.yaml -f deploy/production/services.yaml "$@"
}
dc build
sudo node scripts/production-preflight.mjs /etc/puppeteer/production.env --services
```

此模式增加 guacd 与 MP4 渲染器。共享文件系统需要 `/dev/fuse` 和模板中限定的挂载权限；仅 FUSE 守护进程使用 root，guacd 低权限运行。不要把容器改成 `privileged` 作为故障处理方式。宿主环境不支持 FUSE 时先采用核心安装。

`dc` 是当前终端的 Bash 函数，新开终端需重新定义。后续维护必须使用实际安装时的同一组文件；不能遗漏 `services.yaml`。

### 构建产物与历史验收镜像的区别

上述命令从本次检出的源码构建，不要求已有镜像仓库账号。生成器中的 `r17-*-preview` 是本地构建标签，不表示这些镜像已发布到公共镜像仓库。

`deploy/production/r17-release-manifest.json` 记录的是已有隔离验收镜像。重新构建后镜像 ID 和前端构建清单可能不同，不能要求它们匹配历史清单。只有导入原验收镜像及配套前端产物时才运行 `node scripts/verify-production-release.mjs`。源码重建后应记录自己的镜像 ID，并完成下面的部署验证。

## 6. 启动及创建管理员

确保前面的预检查成功后执行：

```bash
dc up -d --no-build --wait
dc ps
dc exec -T server /app/nt health
curl --fail --show-error https://puppeteer.example.com/healthz
```

核心模式应有 postgres、server、web 三个健康服务；图形模式另有 guacd、mp4-renderer。`/healthz` 验证网页入口，`nt health` 验证后端就绪，两者都要检查。内网 CA 使用 `curl --cacert /path/to/ca.pem ...`，不要以 `-k` 代替信任配置。

浏览器打开 `https://puppeteer.example.com/setup`，创建自己的初始管理员，然后登录。没有预置账号；初始化完成后，重复初始化不能覆盖已有数据。不要运行任何 `seed`、`fixture` 或验收账号生成脚本。

首次登录依次完成：

1. 核对平台名称、登录策略和 MFA，保留验证码。
2. 创建日常使用的用户、角色和资产授权。
3. 创建凭据及一台测试资产，建立 SSH 或浏览器 RDP 会话。
4. 在平台设置配置审计保留天数、共享空间容量等业务策略。
5. 配置通知渠道，核对一次实际通知投递结果。
6. 按[客户端 IP 配置](../deploy/production/client-ip.md)为 Nginx 配置固定内网地址，仅信任该代理地址，核对访问记录的客户端 IP。默认直连模式会记录代理 IP。

## 7. 可选协议与更新服务

| 需要的功能 | 增加的文件/操作 | 注意事项 |
| --- | --- | --- |
| 原生 SSH、MySQL/PostgreSQL 代理、网站入口 | `protocols.yaml` | 默认回环端口分别为 2022、13306、15432、18443；额外配置平台开关、授权及客户端证书信任 |
| Agent 签名更新仓库 | `agents.yaml` | 先按[运维说明](../deploy/production/operations.md)生成签名仓库并导出公开文件；私钥不能挂到 web |
| PostgreSQL WAL/PITR | `wal.yaml` | 先按[WAL 指南](../deploy/production/wal.md)生成配置、构建镜像、初始化 stanza 和首份备份 |

例如图形模式加协议入口时，重新定义：

```bash
dc() {
  sudo docker compose --env-file /etc/puppeteer/production.env -p puppeteer \
    -f deploy/production/compose.yaml -f deploy/production/services.yaml \
    -f deploy/production/protocols.yaml "$@"
}
sudo node scripts/production-preflight.mjs /etc/puppeteer/production.env --services --protocols
dc up -d --no-build --wait
dc up -d --no-deps --force-recreate --wait web
```

需要其他主机连接协议端口时，再修改 `PUPPETEER_PROTOCOL_BIND` 并设置对应防火墙规则。发布端口不会自动授予资产权限，也不会自动启用平台访问策略。协议证书/CA信任须按平台客户端说明配置，网页入口证书不能替代所有协议信任配置。原生 RDP/RemoteApp 属于既定暂缓范围；本节的浏览器 RDP 不受此排除项影响。

数据库代理的容器内部监听地址、宿主机映射端口、权限控制模式及目标TLS选项见[数据库代理配置](../deploy/production/README.md#数据库代理启用与凭据)。

## 8. 部署验收

- 用新管理员完成初始化、退出及重新登录；用普通用户检查未授权资产不可访问。
- 检查真实资产连接、文件上传/下载、命令审计、会话关闭与访问记录。
- 启用图形模式时验证 RDP/VNC、共享目录、录屏回放及 MP4 生成下载。
- 确认访问记录显示正确的来源 IP，日志中没有持续连接或权限错误。
- 创建一份加密备份，在独立项目/空库验证恢复；不能仅凭“备份任务成功”判断可恢复。
- 在维护时段重启应用后确认数据仍在、服务恢复健康。

不要导入开发数据库或历史验收数据。平台默认访问记录保留策略、录屏及共享目录配额可在管理页调整；容量需求以自己的使用量为准。

## 9. 日常维护与排错

```bash
dc ps
dc logs --tail=100 server web
# 仅图形模式存在下面两个服务
dc logs --tail=100 guacd mp4-renderer
# 停止/恢复服务，保留数据卷
dc stop
dc start
```

| 现象 | 检查方向 |
| --- | --- |
| 其他电脑打不开网页 | `PUPPETEER_BIND` 是否仍为127.0.0.1；域名解析、HTTPS端口及防火墙 |
| 证书错误 | 域名覆盖、有效期、中间证书链及客户端CA信任 |
| 网页502 | server健康状态；更新server后重新创建web，使Nginx解析新容器地址 |
| 预检查提示权限/密钥错误 | 通过sudo运行配置生成器；保留脚本分配的UID与权限，不使用chmod 777 |
| 图形服务不健康 | 宿主机FUSE设备、容器挂载权限、guacd日志及实际目标连通性 |
| MP4任务繁忙或失败 | 渲染器健康状态、临时磁盘、任务清理；429表示繁忙，507表示存储不足 |

一键安装创建的已有部署可使用[升级脚本](UPGRADE.md)；本文源码安装布局不满足该脚本的目录约定，应使用原项目与叠加文件手动维护。

应用更新和证书变更后的操作见[生产部署说明](../deploy/production/README.md)。不要执行 `docker compose down -v` 或删除数据卷来处理普通启动失败。数据库已有新写入后，不可简单切换旧镜像声称无损回滚；使用[恢复切换流程](../deploy/production/cutover.md)。

备份需要分别管理应用数据归档、部署配置和离线恢复密钥；正式维护方案见[运维说明](../deploy/production/operations.md)与[备份定时任务](../deploy/production/systemd/README.md)。
