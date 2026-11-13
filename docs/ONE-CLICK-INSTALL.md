# 一条命令安装 Puppeteer

适用于已安装 **Docker Engine 和 Docker Compose v2 的 Linux amd64** 服务器。还需系统提供 Bash、curl、OpenSSL；无需 Node.js、Git 或编译工具。Docker须可用且至少分配2CPU/4GiB；图形和MP4需要额外资源，磁盘需容纳镜像、数据库及录屏。

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
4. 拉取按摘要固定的发布镜像，启动Web、后端、PostgreSQL、图形网关和MP4服务。
5. 等待服务健康，检查后端就绪并输出访问地址。

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

核心模式只启动Web、后端和数据库，不提供图形会话、共享盘及MP4渲染。其他参数见`bash install-puppeteer.sh --help`，可指定绑定IP、安装目录和Compose项目名。

## 安装后管理

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
