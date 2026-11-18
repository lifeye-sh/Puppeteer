# 一条命令安装 Puppeteer

> beta0.11 新增系统更新中心、网页自升级及维护窗口自动升级，保留恢复密钥管理和批量授权，并包含资源档位、流式 MP4、按需渲染、转换质量、数据库预算及空闲退避，详见 [资源优化与运行方式](RESOURCE-OPTIMIZATION.md)。安装和升级使用同版本服务端、前端、渲染器及执行器；第二轮 10 小时稳定性复测尚未完成。

适用于已安装 **Docker Engine 和 Docker Compose v2 的 Linux amd64** 服务器。还需系统提供 Bash、curl、OpenSSL、Python 3、flock、systemd；无需 Node.js、Git 或编译工具。Docker 须可用，至少 2 CPU、实际可见内存 3.5 GiB，推荐 4 GiB 或更多；图形和MP4需要额外资源，磁盘需容纳镜像、数据库及录屏。

安装预检使用 Docker 守护进程报告的 `OSType`、`NCPU` 和 `MemTotal`，不是当前空闲内存。安装与轻量资源档位的内存下限统一为 **3584 MiB（3.5 GiB，3758096384 字节）**，不再额外扣除内核保留比例；例如 `3822956544` 字节可通过容量检查。此门槛不是完整负载稳定性保证，3.5 GiB 整机验收尚未完成。清理缓存或增加 swap 不会增大 `MemTotal`。可执行 `docker info --format 'OS={{.OSType}} CPUs={{.NCPU}} MemoryBytes={{.MemTotal}}'` 核对；旧版脚本需更新后才使用新门槛。

当前安装脚本固定 server、web、graphics 和 renderer 为 `beta0.11` 的发布摘要（schema 110、能力版本 90），包含自动部署执行器。见[镜像说明](../deploy/production/beta0.11.md)。已有安装请使用本文的升级入口，不要重复运行安装器。

beta0.11 已接通管理员确认的网页自升级，以及默认关闭的自动准备和维护窗口升级。首次使用需先通过[受管升级脚本](UPGRADE.md)更新到 beta0.11 或后续兼容版本，以同步冻结配置、独立进度入口及宿主机执行器；此后在“系统运维 → 系统更新”预检并确认升级，或由管理员授权明确版本上限和最长 30 天有效期的自动策略。自升级额外要求 Python 3.9+；自动检查本身不会自动安装。维护窗口停机会要求明确确认，失败后暂停自动执行；支持布局、保留策略和恢复说明见[系统更新](SYSTEM-UPDATES.md)。

本安装器不支持Snap封装的Docker。其文件访问和容器选项限制可能导致宿主机存在的`/opt/puppeteer/config/production.env`被Compose报告为不存在。安装器会提前拒绝此环境；不要通过删除安装目录、放宽密钥权限或关闭容器安全选项解决。Snap与普通Docker Engine可能管理不同的数据目录，已有容器时需先核对现有工作负载再安排切换。限制依据见[Canonical Docker Snap说明](https://github.com/canonical/docker-snap)。

如果是Ubuntu amd64、Snap Docker中没有任何容器或数据卷，并且之前已在`/opt/puppeteer`生成完整配置，可使用下列恢复命令。脚本从[Docker官方apt源](https://docs.docker.com/engine/install/ubuntu/)安装Engine，禁用而不删除Snap，校验现有密钥未变后继续启动；遇到已有工作负载、其他Engine数据或冲突软件包会拒绝操作：

```bash
curl -fsSL https://raw.githubusercontent.com/lifeye-sh/Puppeteer/main/scripts/repair-snap-docker.sh -o repair-puppeteer.sh && sudo bash repair-puppeteer.sh
```

该脚本不是有业务数据的Docker迁移工具。不要在未知数据目录上强行绕过检查。恢复失败时保留现场并检查错误，不运行全新安装器覆盖密钥。

将下面的`192.168.1.100`换成服务器的实际IP或域名，复制整行执行：

```bash
curl -fsSL https://raw.githubusercontent.com/lifeye-sh/Puppeteer/main/scripts/install.sh -o install-puppeteer.sh && sudo bash install-puppeteer.sh --host 192.168.1.100
```

脚本会自动：

1. 检查Docker、资源、FUSE设备，以及安装目录和项目是否已存在。
2. 下载固定版本的部署配置，生成独立数据库密码和应用密钥。
3. 为该域名/IP生成HTTPS证书。
4. 拉取按摘要固定的发布镜像，启动Web、后端、PostgreSQL、图形网关和MP4服务；默认配置MySQL/PostgreSQL代理监听及本机端口映射。
5. 等待服务健康，检查后端就绪并输出访问地址。自动安装 systemd 部署执行器，校验套接字与服务健康后才报告完成。

完成后打开 **`https://服务器IP/setup`**，创建管理员即可。没有默认账号或密码。默认监听443端口，安装目录为`/opt/puppeteer`。首次初始化前请将服务器入口限制为管理员来源，完成管理员创建后再放行使用者；脚本不改动宿主机防火墙。

默认生成的是自签名证书，浏览器会提示不受信任。应把`/opt/puppeteer/config/tls/server.crt`导入客户端信任，或使用下方正式证书选项；脚本不会修改客户端的信任设置。

## 可选参数

使用已有正式证书（证书与私钥必须匹配，私钥无交互密码）：

```bash
sudo bash install-puppeteer.sh --host ops.example.com --cert /secure/fullchain.pem --key /secure/privkey.pem
```

改用8443端口：

```bash
sudo bash install-puppeteer.sh --host 192.168.1.100 --port 8443
```

服务器没有FUSE或只需要SSH/SFTP时采用核心模式：

```bash
sudo bash install-puppeteer.sh --host 192.168.1.100 --core
```

核心模式只启动Web、后端和数据库，不提供图形会话、共享盘及MP4渲染；仍默认配置数据库代理。其他参数见`bash install-puppeteer.sh --help`，可指定绑定IP、安装目录和Compose项目名。

## 网页内 RDP

默认完整安装包含 guacd；`--core` 不包含。目标 Windows 开启远程桌面，在平台添加目标地址、RDP 端口及凭据并授权，然后从“我的资产”打开浏览器连接。

网页内 RDP 无需原生 Worker，也无需开启“原生 RDP”或映射平台的 3389/13389。“当前节点未部署原生 RDP Worker 接入配置”只描述原生客户端能力。guacd 的 4822 和 MP4 的 8090 保持内部网络使用。见[准备、示例与排错](BROWSER-RDP.md)。

## 安装后管理

当前分支的新安装已自动加载项目专用 AppArmor 策略；下面的修复仅适用于已发布旧版本、尚未接管的默认安装。

Ubuntu 启用 AppArmor 时，默认容器策略禁止 FUSE 挂载。若图形服务反复报告
`/bin/fusermount: no such file or directory`，直接挂载失败可能被备用工具错误掩盖。
默认安装目录和项目名可使用以下修复；它校验专用策略，只允许
`fuse.nt-storage` 在 `/mnt/nt-rdp` 以 `nosuid,nodev,noexec` 挂载，
只重建图形服务，不修改密钥或数据卷，也不关闭 AppArmor：

```bash
curl -fsSL https://raw.githubusercontent.com/lifeye-sh/Puppeteer/main/scripts/repair-graphics-apparmor.sh -o repair-graphics-apparmor.sh
sudo bash repair-graphics-apparmor.sh
```

需要宿主机已有 `apparmor_parser`。策略持久保存在
`/etc/apparmor.d/puppeteer-rdp-storage`，管理脚本会自动携带
`graphics-apparmor.yaml`。自定义安装应先加载同一策略，再将该覆盖文件加入自己的
Compose 命令。此项不会替代 `/dev/fuse`、`SYS_ADMIN` 等既有挂载条件；
若健康检查仍失败，应继续检查图形服务及内核日志。

```bash
sudo /opt/puppeteer/puppeteerctl ps
sudo /opt/puppeteer/puppeteerctl logs --tail=100
sudo /opt/puppeteer/puppeteerctl stop
sudo /opt/puppeteer/puppeteerctl up -d --no-build --wait
```

`puppeteerctl`已经保存安装时的目录、项目和组件选择，不需要记忆多组Compose参数。自定义`--dir`时使用该目录下的`puppeteerctl`。

重复运行安装器会拒绝覆盖已有目录或复用已有项目数据卷。启动阶段失败时保留配置和数据，解决端口冲突、镜像拉取或资源问题后，通过`puppeteerctl`继续启动；不要重新生成加密密钥。若失败在配置下载阶段，则先检查错误及保留目录，尚未生成管理脚本时不能直接运行它。

Docker Hub限流时可先运行`sudo docker login`再通过`puppeteerctl pull`重试。不需要将密码或令牌写入脚本。

安装后仍需在管理页配置资产、授权、正确的[客户端IP提取策略](../deploy/production/client-ip.md)和备份。原生协议端口、Agent更新仓库、WAL归档属于可选扩展，参见[完整部署说明](FRESH-INSTALL.md)。本安装器不启用尚在暂缓范围的原生RDP/RemoteApp，也不自动初始化业务测试数据。

## 已有安装升级

```bash
curl -fsSL https://raw.githubusercontent.com/lifeye-sh/Puppeteer/main/scripts/upgrade.sh -o upgrade-puppeteer.sh
sudo bash upgrade-puppeteer.sh --check
sudo bash upgrade-puppeteer.sh
```

自定义目录时，两条命令都添加 `--dir /实际安装目录`。升级额外需要 Python 3 和 flock；默认目标是 `beta0.11`。脚本先拉取固定摘要镜像，再暂停应用服务并备份配置、数据库和应用数据，更新后检查健康。已有连接会中断，须安排维护时段并预留备份空间。密钥、证书、组件停用状态和数据卷保留；官方图形/MP4镜像同步更新，自定义镜像保留并提示兼容性检查；普通升级不会自动开放数据库代理端口；需要时使用下方 `--enable-database-proxy`。完整适用范围和失败恢复见[版本升级说明](UPGRADE.md)。

## 数据库代理配置

新版一键安装默认创建 `database-proxy.yaml` 并加入 `puppeteerctl`：MySQL和PostgreSQL在容器内监听13306、15432，宿主机默认只绑定 `127.0.0.1`。证书主机名取 `--host`，客户端从平台下载代理证书。使用 `--no-database-proxy` 可在新安装时跳过此配置。

需要其他电脑直接连接时，新安装显式指定绑定地址，并在防火墙/云安全组中允许可信客户端来源：

```bash
sudo bash install-puppeteer.sh --host 192.168.1.100 --database-proxy-bind 0.0.0.0
```

旧安装或已经升级到rc11但缺少端口映射的部署，重新下载最新升级脚本后执行：

```bash
sudo bash upgrade-puppeteer.sh --enable-database-proxy --database-proxy-bind 0.0.0.0 --check
sudo bash upgrade-puppeteer.sh --enable-database-proxy --database-proxy-bind 0.0.0.0
```

不指定绑定参数则新映射仅限本机。此选项只补缺失项，不重置现有端口、证书主机名、平台开关或其他叠加文件；已有本机映射不会被参数自动扩大为远程映射。默认主机名从原HTTPS来源提取，也可用 `--database-proxy-host` 指定；与已配置的主机名冲突时会拒绝。

若旧实例已生成覆盖 `localhost` 的代理证书，它不会自动变成服务器证书：需在平台证书管理中准备覆盖实际代理主机名的服务端证书及私钥，在“网关监听”选择该数据库代理证书，并重新下载/配置客户端信任。网页HTTPS证书与数据库代理证书是两套独立配置。两个代理的平台监听开关也须开启。详细边界见[数据库代理启用说明](../deploy/production/README.md#数据库代理启用与凭据)。

## 自动部署执行器

rc13 安装/升级脚本在修改前检查 Python 3、systemd 和 flock，并自动接管部署执行器。rc11 升级到 rc13 后获得该能力。依赖、操作步骤与边界见[代理自动部署](AUTO-DEPLOYMENT.md)。

P1-0 统一配置状态使用自动数据库迁移 108，无需新增容器或端口。更新匹配前后端后，可在“平台配置 → 状态与发布 → 预检与服务”查看；宿主预检需要已接管的 P0 执行器。详见 [统一配置状态与只读预检](CONFIGURATION-STATUS.md)。

## 图形组件启停（rc13）

匹配版本的安装/升级程序会接管本项目内部 guacd，管理员在“平台配置 → 服务部署 → 图形组件”完成预检和自动启停。无需公开 `4822` 端口；停用配置和数据卷在升级后保留。操作会中断已有连接；共享盘可在独立页签管理，见 [RDP 共享盘自动部署](RDP-DRIVE-AUTO-DEPLOYMENT.md)。适用条件、恢复及验证见 [图形组件自动部署](GRAPHICS-AUTO-DEPLOYMENT.md)。

rc13 已接入共享盘自动预检与部署。新安装自动准备项目专用 AppArmor 策略，升级接管保留共享盘停用状态；页面操作会验证挂载读写，停用时移除设备与额外权限，保留 MyZone 数据。宿主不具备 FUSE 或必要策略解析能力时明确失败。详见 [共享盘部署条件与验收边界](RDP-DRIVE-AUTO-DEPLOYMENT.md)。

## MP4 渲染器启停（rc13）

匹配的安装/升级程序自动接管内部 `mp4-renderer`；在“平台配置 → 服务部署 → MP4 渲染”完成预检、启停及样例转换验证。无需映射宿主端口。停用暂停并保留队列、原始录屏和缓存；授权缓存下载仍可用，重新启用恢复队列。升级保留停用 profile、地址、密钥和数据目录；外部或不兼容配方显示原因。详见 [MP4 自动部署](MP4-AUTO-DEPLOYMENT.md)。

## 原生 RDP 能力识别（rc13）

监听与证书页面新增部署能力报告和只读预检，普通生产部署未配置 Worker 时明确显示原因。不会新增原生 RDP 服务或端口。专用部署需同步更新支持 `--capabilities` 的 Worker；旧 Worker 会报告检测协议不足，现有监听限制保留。详见 [原生 RDP 能力识别](NATIVE-RDP-CAPABILITY.md)。

## 外部依赖与专项运维（rc13）

rc13 自动应用迁移至 schema 110，能力版本 90。邮件、身份、AI、备份、ACME 和连接资源可从原页面执行统一预检；备份写入/摘要/清理通过独立显式按钮进行。见 [外部依赖预检](EXTERNAL-DEPENDENCY-CHECKS.md)。

安装/升级自动登记兼容的专项运维能力。管理员在「平台配置 → 服务部署」启停 Agent 签名更新仓库、WAL 归档或网页入口限制，系统自动完成预检、部署、验证和失败恢复，无需手工改 Compose 或安装定时任务。升级保留启停状态、WAL 原镜像恢复配方、数据/归档卷、密钥及仓库防回退记录，不自动启用未开启的功能。

WAL 首次启用会自动构建固定 PG18 镜像并重建 PostgreSQL，须安排数据库连接中断窗口和备份空间；完整备份、命名恢复点归档与隔离恢复演练通过后才显示可恢复。停用保留数据和密钥。本地数据库备份不替代异地副本及应用文件/离线密钥备份。详见 [专项运维自动部署](OPERATIONS-AUTO-DEPLOYMENT.md)。旧已发布镜像不因下载新版脚本自动获得这些功能，须使用配套发布版本。

## 自动恢复密钥（beta0.10）

beta0.10 起提供此功能。新版本后端在可写启动阶段自动生成未配置的备份恢复密钥，初始化失败会阻止启动；无需增加安装参数、挂载或手工命令。已有公钥与历史密钥保留，重复启动不会重新生成。管理员可在“备份与恢复 → 备份策略”下载恢复私钥或重新生成，请将下载的私钥保存在服务器之外。详见[备份与恢复说明](backend/implementation/backup-restore.md#页面与身份验证)。
