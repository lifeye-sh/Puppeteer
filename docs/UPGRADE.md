# 已有部署版本升级

适用于 `scripts/install.sh` 创建的 Linux amd64 部署。默认安装目录 `/opt/puppeteer`，通过原有 `puppeteerctl` 自动沿用项目名及 Compose 叠加文件。当前默认目标为 `r17-rc11`。

## 执行

```bash
curl -fsSL https://raw.githubusercontent.com/lifeye-sh/Puppeteer/main/scripts/upgrade.sh -o upgrade-puppeteer.sh
# 预检查：不会拉取镜像、暂停或更新服务
sudo bash upgrade-puppeteer.sh --check
# 升级，期间现有连接会断开
sudo bash upgrade-puppeteer.sh
```

自定义安装目录或显式指定已发布版本：

```bash
sudo bash upgrade-puppeteer.sh --dir /opt/my-puppeteer --version r17-rc11
```

依赖 Docker Compose v2、curl、Python 3、tar、flock 和常用 Linux 工具，需要 root。目标版本必须提供 `deploy/production/dockerhub-版本.json` 清单。当前脚本仅支持 r17-rcN 版本，不支持降低数据库 schema 版本；不能用于旧版 Next Terminal 或开发 Compose 部署。

## 同时补齐数据库代理

旧安装没有映射数据库端口时，可以升级到当前同一版本并补齐配置，无需重装：

```bash
sudo bash upgrade-puppeteer.sh --enable-database-proxy --database-proxy-bind 0.0.0.0 --check
sudo bash upgrade-puppeteer.sh --enable-database-proxy --database-proxy-bind 0.0.0.0
```

不指定 `--database-proxy-bind` 时新增端口绑定127.0.0.1；外部连接还需要防火墙/云安全组放行可信来源。`--database-proxy-host` 默认取原 `PUPPETEER_ORIGIN` 的主机名。该模式补齐容器内部13306/15432监听、缺少的宿主机映射和证书主机名，将自动管理的 `database-proxy-managed.yaml` 加入原 `puppeteerctl`，保留已有的全部 `-f` 参数。

已有端口映射不会覆盖或重复追加（包括此前手动添加的 `database-proxy.yaml`）；已配置的主机名保留。自定义内部监听地址或无法识别的管理脚本会在停机前拒绝自动修改。`--check` 只在临时目录验证候选配置，不修改实际管理脚本。配置备份完成后才应用修改。

此选项不修改平台监听开关。仍需在“网关监听”开启MySQL/PostgreSQL。已经生成的数据库代理证书也会保留：若SAN不覆盖实际代理主机名，应在平台配置匹配的服务端证书并更新客户端信任，不能关闭TLS验证来规避。即使容器健康，平台监听发布失败或证书名称不匹配仍需单独处理。

## 升级流程

1. 加独占锁，读取 GitHub 版本标签下的镜像清单，校验产品、版本、平台、仓库和 SHA-256 摘要。确认 server、web、postgres 正在运行。
2. 校验目标配置，并在停机前拉取 server/web 镜像。失败时现有服务继续运行。
3. 暂停原来运行的非 postgres 服务。备份安装配置（包含密钥和证书）、数据库自定义格式 dump、应用数据及嵌套 storageconfig 卷；检查备份可读性并生成 SHA256SUMS。
4. 原子替换 `config/production.env` 中 server/web 镜像引用。普通升级保留 Compose、Nginx、证书、端口、密钥和可选组件镜像；使用 `--enable-database-proxy` 时另补缺少的代理配置，见下文。不重新生成账号或删除数据卷。
5. 启动升级前正在运行的应用服务，不升级 PostgreSQL 或改变原先停用的可选服务。重建Web以刷新后端地址解析，等待健康检查，核对运行镜像摘要引用，并执行应用健康探测。
6. 成功后记录 `installed-version`。备份留在 `安装目录/backups/upgrade-UTC时间-PID/`。

为数据库和应用文件的备份预留足够磁盘空间。此脚本备份 Docker 挂载在 `/var/lib/next-terminal` 下的数据；外部对象存储、独立数据库写入者或其他节点需要单独协调停写和备份。密钥备份必须与数据库备份一起妥善保管。

## 失败处理

- 拉取阶段失败：应用未停止，修复网络后可重试。
- 停机或备份失败：镜像设置尚未切换，但部分或全部应用可能已停止。检查错误，确认旧配置仍有效后，使用 `puppeteerctl up -d --no-build --wait` 恢复；若曾手动停用可选服务，请仅指定 `running-services.txt` 中原来运行的应用服务。
- 配置切换或启动失败：保留工作目录和备份。启动/健康检查失败时脚本会尝试停止应用服务，须使用 `puppeteerctl ps` 确认状态。数据库可能已迁移，不能直接改回旧镜像启动。
- 优先检查 `puppeteerctl logs --tail=200 server web`，修复环境后重试目标版本。需要回退时，应在隔离环境中验证备份，将 `configuration.tar`、`database.dump` 和 `appdata.tar` 作为同一时间点恢复；先验证 `sha256sum -c SHA256SUMS`，再由管理员协调数据库、密钥和文件数据恢复。脚本不会自动执行数据库回滚或删除卷。

备份校验只证明文件可读及摘要一致，不替代实际恢复演练。成功后确认业务和备份保留周期，再手动清理旧备份。

## 开发验证

```bash
docker run --rm --network none -v "$PWD:/repo:ro" python:3.11-slim python3 /repo/tests/upgrade_test.py
```

使用命令替身验证升级流程及失败处理，无 Docker socket、无真实业务数据；另有 Bash 语法检查。此测试不代表完成真实部署升级或数据恢复演练。
