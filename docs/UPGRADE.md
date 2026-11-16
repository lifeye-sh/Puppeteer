# 已有部署版本升级

> 开发分支新增资源档位、流式 MP4、按需渲染、转换质量、数据库预算及空闲退避，详见 [资源优化与运行方式](RESOURCE-OPTIMIZATION.md)。公开默认镜像仍为 rc13；新功能需要同批构建的服务端、前端、渲染器及执行器，不能仅更新网页后使用旧镜像。

适用于 `scripts/install.sh` 创建的 Linux amd64 部署。默认安装目录 `/opt/puppeteer`，通过原有 `puppeteerctl` 自动沿用项目名及 Compose 叠加文件。当前镜像升级默认目标为 `beta0.9`（schema 110、能力版本 90）。支持从 rc11 升级并自动接入部署执行器；r17-rc12 保留为源码标签，没有镜像清单。不要从 schema 110 回退到 rc11。

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
sudo bash upgrade-puppeteer.sh --dir /opt/my-puppeteer --version beta0.9
```

依赖 Docker Compose v2、curl、Python 3、tar、flock 和常用 Linux 工具，需要 root。目标版本必须提供 `deploy/production/dockerhub-版本.json` 清单。当前脚本支持 beta主版本.次版本（如 beta0.9）及历史 r17-rcN 版本，不支持降低数据库 schema 版本；不适用于无法识别布局的手工安装或开发 Compose 部署。

使用新资源档位、按需渲染或质量设置的开发部署升级时，会保留档位、运行方式、质量、临时卷、配额和组件关闭状态，并在镜像拉取后、服务停机前检查目标镜像能力。`--check` 不拉镜像，不能完成目标镜像能力验证；旧镜像缺少能力时正式升级会提前停止。

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
2. 校验目标配置，并在停机前拉取清单中的四个组件镜像。失败时现有服务继续运行。
3. 暂停原来运行的非 postgres 服务。备份安装配置（包含密钥和证书）、数据库自定义格式 dump、应用数据及嵌套 storageconfig 卷；检查备份可读性并生成 SHA256SUMS。
4. 原子替换镜像设置，同步受管 Compose 与共享盘恢复配方中的标准组件镜像，保留 Nginx、证书、端口、密钥及自定义镜像。跨 schema 110 时迁移容器内数据路径，保留原数据卷身份；使用 `--enable-database-proxy` 时另补缺少的代理配置，见下文。不重新生成账号或删除数据卷。
5. 启动升级前正在运行的应用服务，不升级 PostgreSQL 或改变原先停用的可选服务。重建Web以刷新后端地址解析，等待健康检查，核对运行镜像摘要引用，并执行应用健康探测。
6. 成功后记录 `installed-version`。备份留在 `安装目录/backups/upgrade-UTC时间-PID/`。

为数据库和应用文件的备份预留足够磁盘空间。此脚本从原运行时配置识别应用数据根目录并在原位置备份，避免目录改名后备份空目录；外部对象存储、独立数据库写入者或其他节点需要单独协调停写和备份。密钥备份必须与数据库备份一起妥善保管。

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

## 自动部署执行器

rc13 脚本会在修改前检查 Python 3、systemd 和 flock，升级后自动接入匹配的部署执行器。依赖、操作步骤与边界见[代理自动部署](AUTO-DEPLOYMENT.md)。

P1-0 统一配置状态使用自动数据库迁移 108，无需新增容器或端口。更新匹配前后端后，可在“平台配置 → 状态与发布 → 预检与服务”查看；宿主预检需要已接管的 P0 执行器。详见 [统一配置状态与只读预检](CONFIGURATION-STATUS.md)。

## 图形组件启停（rc13）

匹配版本的安装/升级程序会接管本项目内部 guacd，管理员在“平台配置 → 服务部署 → 图形组件”完成预检和自动启停。无需公开 `4822` 端口；停用配置和数据卷在升级后保留。操作会中断已有连接；共享盘可在独立页签管理，见 [RDP 共享盘自动部署](RDP-DRIVE-AUTO-DEPLOYMENT.md)。适用条件、恢复及验证见 [图形组件自动部署](GRAPHICS-AUTO-DEPLOYMENT.md)。

rc13 已接入共享盘自动预检与部署。新安装自动准备项目专用 AppArmor 策略，升级接管保留共享盘停用状态；页面操作会验证挂载读写，停用时移除设备与额外权限，保留 MyZone 数据。宿主不具备 FUSE 或必要策略解析能力时明确失败。详见 [共享盘部署条件与验收边界](RDP-DRIVE-AUTO-DEPLOYMENT.md)。

## MP4 渲染器启停（rc13）

匹配的安装/升级程序自动接管内部 `mp4-renderer`；在“平台配置 → 服务部署 → MP4 渲染”完成预检、启停及样例转换验证。无需映射宿主端口。停用暂停并保留队列、原始录屏和缓存；授权缓存下载仍可用，重新启用恢复队列。升级保留停用 profile、地址、密钥和数据目录；外部或不兼容配方显示原因。详见 [MP4 自动部署](MP4-AUTO-DEPLOYMENT.md)。

## 网页内 RDP 与原生 RDP

网页内 RDP 使用 guacd，保持图形服务健康即可；无需原生 Worker，操作步骤见[网页内 RDP](BROWSER-RDP.md)。监听与证书页面的原生能力报告面向 mstsc 等原生客户端，未配置 Worker 时显示原因，不是网页 RDP 故障。不会新增原生 RDP 服务或端口。专用部署需同步更新支持 `--capabilities` 的 Worker；旧 Worker 会报告检测协议不足，现有监听限制保留。详见 [原生 RDP 能力识别](NATIVE-RDP-CAPABILITY.md)。

## 外部依赖与专项运维（rc13）

rc13 包含迁移 109（统一依赖验证的清理记录）和 110（产品默认名称），当前能力版本 90。邮件、身份、AI、备份、ACME 和连接资源可从原页面执行统一预检；备份写入/摘要/清理通过独立显式按钮进行。见 [外部依赖预检](EXTERNAL-DEPENDENCY-CHECKS.md)。

安装/升级自动登记兼容的专项运维能力。管理员在「平台配置 → 服务部署」启停 Agent 签名更新仓库、WAL 归档或网页入口限制，系统自动完成预检、部署、验证和失败恢复，无需手工改 Compose 或安装定时任务。升级保留启停状态、WAL 原镜像恢复配方、数据/归档卷、密钥及仓库防回退记录，不自动启用未开启的功能。

WAL 首次启用会自动构建固定 PG18 镜像并重建 PostgreSQL，须安排数据库连接中断窗口和备份空间；完整备份、命名恢复点归档与隔离恢复演练通过后才显示可恢复。停用保留数据和密钥。本地数据库备份不替代异地副本及应用文件/离线密钥备份。详见 [专项运维自动部署](OPERATIONS-AUTO-DEPLOYMENT.md)。旧已发布镜像不因下载新版脚本自动获得这些功能，须使用配套发布版本。

应用备份的显式兼容版本同步到 schema 110；恢复隔离会使源预检证据过期并移除源测试清理记录。备份/恢复支持范围见 [备份与隔离恢复](backend/implementation/backup-restore.md)。


## 项目标识与数据目录

包含 schema 110 的版本使用 `puppeteer/backend` 模块和 `/var/lib/puppeteer` 应用目录。升级脚本从原配置的运行时文件地址识别数据根目录，先在原路径备份，再把同一个应用卷和共享存储卷挂载到新路径；不会创建空卷替换旧数据，也不修改宿主机绑定源或密钥。托管执行器的共享盘配置同时更新。自定义数据根目录无法明确识别时停止并报告原因。

数据库迁移保留用户、资产、密码、自定义客户端名称和历史校验记录，仅更新生成的默认名称。四个带产品名称的早期迁移只接受已登记的原始与新文件摘要；其他 SQL 改动仍会被拒绝，纯 LF/CRLF 换行差异不视为内容变更。数据库升至 110 后不要直接回退旧镜像，应使用升级前的配套数据库和文件备份。

Agent 新安装目录为 `/opt/puppeteer-agent` 和 `/var/lib/puppeteer-agent`；API Skill 下载入口为 `/agent/puppeteer/`。数据库代理证书下载名为 `puppeteer-mysql.pem`，已有证书内容和信任关系不因文件名变更而改变。原生客户端使用 Puppeteer 凭据存储标识，更新客户端后需重新登录。独立许可采用 Puppeteer 的产品、签发者和受众标识，旧签名许可需由自己的签发者重新签发，不能改写已签名载荷。

`NT_*` 环境变量、`nt` 命令及既有协议版本作为稳定接口保留。项目入口、帮助、源码远端和维护文档均指向 Puppeteer。许可证与版权署名继续保留。

## 组件镜像同步

rc13 清单包含 server、web、graphics 和 renderer。升级前拉取全部摘要；已安装的官方图形/MP4组件及共享盘恢复配方使用配套镜像，停用 profile 与原运行服务集合保留，不会因更新镜像启动已关闭组件。自定义图形/MP4镜像保留，需自行确认与目标版本兼容。原有 PostgreSQL/WAL 镜像及数据卷不因该更新替换。

已接管部署的 compose-managed.json 会同步镜像；普通部署升级至 schema 110 时生成保留原卷身份的 compose-runtime.json，再自动接管。之后始终使用 puppeteerctl，不能直接启动旧 Compose 覆盖受管配置。


第四轮优化的网页 RDP 档位与连接诊断需要匹配的新 server/web；转换资源预检还需要匹配的 renderer。没有新增 schema、端口或安装参数，旧客户端不传画质档位时保留原设置。旧渲染器缺少预检接口时兼容原提交流程，不能视为已具备完整预检。详见 [资源优化说明](RESOURCE-OPTIMIZATION.md)。本段描述未发布开发分支，不表示公开镜像已更新。

开发分支的渲染镜像还修复了断开前最后一批绘图指令的导出，以及 Chromium 子进程退出后未回收的问题。镜像内置 Tini 作为入口；升级到匹配镜像即可生效，不增加安装参数、不提高 PID 上限。自定义覆盖镜像入口的部署需保留进程回收机制。
