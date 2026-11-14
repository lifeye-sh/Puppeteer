# Puppeteer R17 全新部署

首次安装优先使用[一键安装](../../docs/ONE-CLICK-INSTALL.md)；手动部署可选择[预构建镜像](dockerhub.md)或[源码构建](../../docs/FRESH-INSTALL.md)。一键安装创建的已有部署使用[升级脚本](../../docs/UPGRADE.md)。当前后端/Web版本为 `r17-rc11`（schema107），详见[发布说明](r17-rc11.md)；本文提供部署配置细节。

可独立运行管理前端、新后端和空 PostgreSQL 数据库，并提供图形网关、MP4 和 SSH/数据库/网站协议入口的可选叠加配置。Agent签名发布与根轮换、双节点升级排空、备份/WAL/PITR、容量与恢复切换均有Docker模拟验收流程，见[运维步骤](operations.md)、[容量基线](capacity.md)和[恢复切换](cutover.md)。最终回归状态以[R17验收记录](../../docs/backend/acceptance/R17.md)为准。用户明确接受Docker模拟环境；正式部署不复制测试账号、证书、凭据或数据，原生RDP及RemoteApp保持既定暂缓范围。

## 准备与构建

目标为单台 Linux Docker 主机，至少 2 CPU、4 GiB 内存。使用新的 Compose 项目名称和新数据卷，不复制开发数据库、验收账号或旧系统数据。生产依赖镜像固定摘要，应用镜像使用版本标签；推广时保存验收镜像的实际摘要，禁止重新使用同一个标签覆盖已发布版本。

历史R17 Docker验收的5个应用镜像ID和发布文件SHA-256见[r17-release-manifest.json](r17-release-manifest.json)。镜像ID是本地内容标识，不是仓库RepoDigest；离线用`docker save`/`docker load`转运并用`docker image inspect IMAGE --format '{{.Id}}'`核对，推送仓库后记录并使用该仓库返回的digest。若重新构建，应生成新版本和重新验收，不能沿用本清单宣称二进制完全相同。

仅在导入上述历史验收镜像及配套产物后，在仓库根目录运行`node scripts/verify-production-release.mjs`，逐项核对镜像ID和发布文件SHA-256；任一不符以非零退出。该校验不读取密钥或业务数据。当前发布的后端/Web应核对[dockerhub-r17-rc11.json](dockerhub-r17-rc11.json)中的仓库摘要，不套用历史镜像ID清单。

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

- 原始核心Compose仅发布HTTPS入口；新版一键安装额外配置本机MySQL/PostgreSQL代理端口。API和平台元数据数据库仍留在私有容器网络。平台自身元数据数据库连接仅允许 verify-full TLS 或 Compose 内隔离的 postgres 服务；当前核心部署为后者。资产所连接的目标数据库使用独立TLS选项，见下文。
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

## 数据库代理启用与凭据

新版一键安装默认配置本机13306/15432映射及代理证书主机名；旧版一键安装可用[升级脚本的代理补齐选项](../../docs/UPGRADE.md#同时补齐数据库代理)，不用手工编辑管理脚本。手动部署仍使用 `protocols.yaml`。

在“平台设置 → 网关监听 → 编辑 → SSH 与数据库”开启对应代理。编辑时空的数据库监听地址会预填下表值，已有自定义地址保留；保存后等待配置发布成功，再到数据库资产页查看代理是否就绪。

| 代理 | 容器内部监听地址 | protocols.yaml 默认宿主机映射 |
| --- | --- | --- |
| MySQL | `0.0.0.0:13306` | `127.0.0.1:13306 → 13306` |
| PostgreSQL | `0.0.0.0:15432` | `127.0.0.1:15432 → 15432` |

Docker部署时，表单填写容器内部监听地址；客户端填写宿主机地址及映射端口。界面设置不能创建Docker映射。`PUPPETEER_MYSQL_PORT`、`PUPPETEER_POSTGRES_PORT`只改变宿主机端口；其他电脑访问时还需配置 `PUPPETEER_PROTOCOL_BIND` 和防火墙。监听地址清空会继承部署环境配置，二者均为空则不监听。

数据库代理证书可留空，由平台首次生成；已生成证书会复用，变更代理主机名后须检查SAN覆盖范围，必要时在平台选择匹配的服务端证书。自选证书须覆盖 `PUPPETEER_PROTOCOL_HOST`。用户在数据库资产页下载代理证书，在“我的账号”创建数据库专用密码。客户端用户名为 `平台用户名@资产别名`，经平台代理连接，仍需用户资源授权。客户端到代理必须验证TLS；PostgreSQL使用SCRAM认证。

数据库资产的“凭据与 TLS”提供两种权限控制模式：

| 模式 | 目标账号要求 | 平台限制 |
| --- | --- | --- |
| 账号控制（默认） | 校验最小权限，各用途使用独立凭据 | 同时执行SQL、审批与授权策略 |
| 程序控制 | 允许更宽账号权限，三类凭据可复用 | 经过平台的SQL仍受通道限制；目标账号自身权限不足仍会失败 |

MySQL只读凭据允许当前库级的 `SELECT / SHOW VIEW`；审批执行按工单需要授予 `SELECT / SHOW VIEW / INSERT / UPDATE / DELETE / CREATE / ALTER / DROP / INDEX / REFERENCES`；原生DML仅允许 `SELECT / INSERT / UPDATE / DELETE`。这些账号权限上限适用于账号控制模式。平台保存凭据，不会自动创建数据库账号或执行GRANT。

MySQL原生DML还需要配置DML凭据、给用户 `database.write` 权限，并在平台数据库设置关闭“阻止数据库原生DML”；重新连接后生效。PostgreSQL仍只有只读入口，不因切换权限模式开放写入或SQL工单。程序控制不约束绕过平台代理的目标账号直连。

目标数据库传输默认 `verify-full`，需要目标CA及证书名称；目标未启用TLS时可明确选择“无TLS兼容模式”，仅影响代理到目标的链路。它不关闭客户端到代理的TLS，也不改变权限模式。测试连接检查连接和身份，账号控制额外检查授权上限；测试不实际执行写入。模式或资产配置变更后需重新连接。
