# Puppeteer R17 全新部署

首次安装请先阅读[全新部署指南](../../docs/FRESH-INSTALL.md)，按步骤完成源码构建、证书安装、初始化及验证。本文提供部署配置细节。

可独立运行管理前端、新后端和空 PostgreSQL 数据库，并提供图形网关、MP4 和 SSH/数据库/网站协议入口的可选叠加配置。Agent签名发布与根轮换、双节点升级排空、备份/WAL/PITR、容量与恢复切换均有Docker模拟验收流程，见[运维步骤](operations.md)、[容量基线](capacity.md)和[恢复切换](cutover.md)。最终回归状态以[R17验收记录](../../docs/backend/acceptance/R17.md)为准。用户明确接受Docker模拟环境；正式部署不复制测试账号、证书、凭据或数据，原生RDP及RemoteApp保持既定暂缓范围。

## 准备与构建

目标为单台 Linux Docker 主机，至少 2 CPU、4 GiB 内存。使用新的 Compose 项目名称和新数据卷，不复制开发数据库、验收账号或旧系统数据。生产依赖镜像固定摘要，应用镜像使用版本标签；推广时保存验收镜像的实际摘要，禁止重新使用同一个标签覆盖已发布版本。

本次Docker验收的5个应用镜像ID和发布文件SHA-256见[r17-release-manifest.json](r17-release-manifest.json)。镜像ID是本地内容标识，不是仓库RepoDigest；离线用`docker save`/`docker load`转运并用`docker image inspect IMAGE --format '{{.Id}}'`核对，推送仓库后记录并使用该仓库返回的digest。若重新构建，应生成新版本和重新验收，不能沿用本清单宣称二进制完全相同。

镜像导入后在仓库根目录运行`node scripts/verify-production-release.mjs`，逐项核对镜像ID和发布文件SHA-256；任一不符以非零退出。该校验不读取密钥或业务数据。

仓库根目录执行：

```sh
npm ci
npm run release:frontend
npm run verify:frontend
# 新目录必须不存在；Linux 上需 root 才能按容器 UID 分配文件所有权。
sudo node scripts/production-config.mjs /etc/puppeteer https://puppeteer.example.com 443
```

生成器拒绝覆盖已有目录，即使上次执行中断也不会重建密钥。生成后保留整个配置目录的离线加密副本。父目录权限为 0700；密钥文件为所有者只读 0400，数据库密码归 UID 70，应用密钥归 UID 10001。Windows Docker Desktop 仅用于隔离开发验收，不视为 Linux 文件权限验收。

将正式证书链和私钥安装到生成目录；私钥仅让 Nginx UID 101 读取：

```sh
sudo install -o 101 -g 101 -m 0400 /secure/fullchain.pem /etc/puppeteer/tls/server.crt
sudo install -o 101 -g 101 -m 0400 /secure/privkey.pem /etc/puppeteer/tls/server.key
```

`production.env` 初始仅绑定 127.0.0.1。准备好主机防火墙及正式入口后再配置 PUPPETEER_BIND；配置公开来源必须是证书覆盖的 HTTPS origin，端口也必须一致。配置目录仅 root 可读，因此后续生产 Compose 命令同样用 root 执行。

```sh
sudo docker compose --env-file /etc/puppeteer/production.env -p puppeteer -f deploy/production/compose.yaml build
sudo node scripts/production-preflight.mjs /etc/puppeteer/production.env
sudo docker compose --env-file /etc/puppeteer/production.env -p puppeteer -f deploy/production/compose.yaml up -d --no-build --wait
```

先在浏览器打开正式地址，创建唯一初始管理员。生产配置不提供预置账号。第二次初始化不能覆盖管理员或数据。验证码保持开启。

## 边界与检查

- 仅 HTTPS 入口发布宿主端口；API、数据库留在私有容器网络。数据库连接仅允许 verify-full TLS 或 Compose 内隔离的 postgres 服务。当前核心部署为后者。
- API 进程以 UID 10001 运行，根文件系统只读，业务数据进入命名卷。Nginx 以 UID 101 运行。日志轮转，临时目录容量和容器资源上限已配置。
- 预检查验证镜像存在、HTTPS origin、证书域名/有效期/私钥匹配、密钥格式及一致性、Linux 所有权、宿主配置磁盘剩余空间和 Docker 资源。**不验证正式 CA 信任链、Docker 数据卷所在磁盘容量、异地备份、负载容量或恢复目标。** selfIssued=true 仅适用于隔离验收，不应当作正式信任链验收通过。
- 正式前端只同源访问 API，Nginx 配置保留 WebSocket Upgrade、关闭流式缓冲。Docker浏览器已通过受信任测试CA的HTTPS/WSS登录、SSH、SFTP、录屏回放、后台MP4下载与设置页面；初始化、权限撤销、审批和图形通路有独立API集成验证。
- 当前源 IP 默认仍为直连模式。对外开放前应在平台配置仅信任实际反代地址的客户端 IP 提取策略，并验收地址伪造防护，避免把全部用户都视作反代 IP。
- 更新应用镜像后重新创建 web 容器，确保反代重新解析新 server 容器地址。升级前先做可恢复备份；不得对已写入的新 schema 直接换旧镜像宣称无损回退。
- 配置文件 secrets 遵循 [Docker Compose 官方说明](https://docs.docker.com/compose/how-tos/use-secrets/)。代理配置参考 [Nginx WebSocket 官方说明](https://nginx.org/en/docs/http/websocket.html)。

隔离验收记录见 [R17](../../docs/backend/acceptance/R17.md)。

## 图形、录屏和协议入口

新配置生成器会同时创建独立 MP4 通信密钥和扩展镜像参数。已有核心配置只需执行以下命令补齐；不会替换既有密钥，也不会覆盖已指定的镜像：

```sh
sudo node scripts/production-addons.mjs /etc/puppeteer
```

`services.yaml` 增加 guacd/FUSE 和 MP4 渲染器。只有 FUSE 守护进程使用 root 与已授权的 SYS_ADMIN、/dev/fuse；guacd 以 UID1000 运行，不继承数据库环境变量。图形服务仅挂载独立 storageconfig 目录，不接触宿主业务目录或整个备份卷。FUSE 挂载包含 nosuid/nodev/noexec；共享存储仍由会话 ACL 和用户容量限制约束。渲染器为 UID10001、只读根文件系统、私有网络、2 CPU/2 GiB/160 PID 上限；正常 API 同时只接收一个转换任务，繁忙时返回429。

`protocols.yaml` 为 SSH、MySQL、PostgreSQL 和网站 TLS 配置独立监听。端口默认仅绑定回环：2022、13306、15432、18443。可通过 PUPPETEER_SSH_PORT、PUPPETEER_MYSQL_PORT、PUPPETEER_POSTGRES_PORT、PUPPETEER_WEBSITE_PORT 调整。PUPPETEER_PROTOCOL_HOST 应为客户端使用的证书域名。平台的认证、资产授权、SQL策略、证书和监听开关继续生效；打开端口不代表授予用户访问权。

```sh
sudo docker compose --env-file /etc/puppeteer/production.env -p puppeteer \
  -f deploy/production/compose.yaml -f deploy/production/services.yaml -f deploy/production/protocols.yaml build
sudo node scripts/production-preflight.mjs /etc/puppeteer/production.env --services --protocols
sudo docker compose --env-file /etc/puppeteer/production.env -p puppeteer \
  -f deploy/production/compose.yaml -f deploy/production/services.yaml -f deploy/production/protocols.yaml up -d --no-build --wait
# 应用容器更新后刷新 Nginx 的后端地址解析。
sudo docker compose --env-file /etc/puppeteer/production.env -p puppeteer \
  -f deploy/production/compose.yaml -f deploy/production/services.yaml -f deploy/production/protocols.yaml up -d --no-deps --force-recreate web
```

不需要协议端口时省略 protocols.yaml 和 --protocols。不需要图形/MP4 时省略 services.yaml 和 --services。后续维护必须继续使用同一组叠加文件。

实际编码验收应串行执行，或为各组测试创建独立受限容器；不要一边运行业务队列，一边在相同渲染容器 exec 启动第二套 Chromium/FFmpeg。已通过的容器容量、磁盘耗尽及编码进程故障结果见[容量验收](capacity.md)，其阈值不能解释为生产最大承载量。
