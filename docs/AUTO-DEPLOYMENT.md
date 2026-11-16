# 代理自动部署（rc13）

> 开发分支新增资源档位、流式 MP4、按需渲染、转换质量、数据库预算及空闲退避，详见 [资源优化与运行方式](RESOURCE-OPTIMIZATION.md)。公开默认镜像仍为 rc13；新功能需要同批构建的服务端、前端、渲染器及执行器，不能仅更新网页后使用旧镜像。

当前发布为 beta0.9（schema 110、能力版本 90），配套镜像包含 `puppeteer-deployd`。使用匹配的一键安装或升级脚本会自动接入执行器；rc11 用户需要升级。手工源码部署仍需明确执行接管步骤。

## 操作入口

管理员在“平台配置 → 服务部署 → 代理部署”选择 MySQL、PostgreSQL、SSH 或网站 HTTPS 网关，填写宿主机绑定地址和端口，输入管理员密码与已绑定的 MFA 动态口令后执行。页面先预检，再提交宿主机任务，显示预检、部署、验证、回滚或保持关闭等阶段。

网站列表提供“启用部署／停用部署”。已接管部署中新建或编辑网站改变启停状态时，先保存配置，再进入网站部署窗口完成复核。启用前按配置的直连、Agent、跳板、网关组或网络代理路径检查证书和上游 TCP/TLS；网关验证成功后才启用网站。停用先撤销网站访问；仍有其他启用网站时保留共享网关，最后一个网站停用时关闭网关。已启用网站须先完成停用部署再删除；网站部署期间禁止编辑、删除。间接连接按实际路由验证，预检期间再次检查授权和资源版本；无法验证时拒绝启用，不回退到本机直连。

管理 HTTP 选项修改的是容器内部管理端口，不修改用户访问的外部 HTTPS 端口或域名。绑定地址固定为 `0.0.0.0`，入口不可关闭。执行器联动更新 `NT_ADDR`、健康检查及 Nginx 的标准 `proxy_pass http://server:端口` 目标，预检 Nginx 配置，并在部署后从 web 容器检查后端就绪状态。自定义或歧义上游配置会拒绝自动修改。原 Nginx 文件保留，回滚恢复原 Compose 引用。

## 安装与升级

Linux amd64 单机 Docker Compose 为当前支持范围。匹配新镜像包含执行器及接管脚本时，安装和升级自动提取同镜像脚本并接管；旧镜像沿用原流程。宿主机还需 Python 3、systemd、flock；安装和升级在开始修改前检查这些依赖。执行器以独立 systemd 服务运行；应用容器不挂载 Docker socket。

接管保留原包装脚本、配置及既有服务，生成独立 `compose-managed.json`、私有任务状态和 Unix Socket。后续升级更新该 Compose 中的镜像引用及宿主机二进制，升级期间停止执行器；升级失败时保留恢复资料并明确提示服务状态。首次接管若刷新失败，会自动停止新执行器、恢复原服务并归档本次接管文件；恢复成功后可以直接重试接管。

## 预检和实际影响

- 检查本机地址、TCP 端口占用、其他 Docker 容器的端口映射冲突、映射所有权、证书身份与有效期、上游连接及配置版本。
- 防火墙、云安全组及 DNS 自动修改不在本次范围。容器和端口验证通过不代表公网一定可达。
- 开关会重建 server 并刷新 web，现有 server 连接会中断。窗口关闭不取消宿主机已受理的任务。
- 应用 API 暂不可用时，短期只读票据可通过 Nginx 查询对应任务。web 自身重建时仍会短暂断线，随后恢复轮询。
- 关闭失败不会自动重新开放所关闭的入口；开启失败尝试恢复原配置。失败原因显示在任务中。
- 当前任务及最近 100 条历史任务保存在宿主机，页面可查询结果；进度票据有效期为两小时。日志写入失败会阻止新的部署，避免覆盖恢复信息。
- 执行器重启时自动处理已记录的中断任务：开启操作验证或回滚，关闭操作继续完成关闭。
- 托管入口不能再通过“监听与证书”中的托管字段修改启停和地址，以免配置显示与实际部署不一致。

## 验证范围

本地 P0 验收已完成。24 项安装/升级/接管回归通过，Go 相关单元和应用集成测试、静态检查、前端完整类型检查与生产构建通过。源码测试覆盖协议隔离、任务持久化、并发/版本限制、证书检查、网站启停、共享网关、端口映射、配置回滚、进度票据以及安装升级分支。`tests/deployd_docker_integration.py` 使用唯一 Docker 项目和专属数据卷进行真实服务重建测试，结束后仅清理测试资源。真实 Docker 验证已覆盖四类代理启停、管理端口迁移及恢复、非 root Nginx 在 API 停止期间查询进度，以及强制终止执行器后的开启回滚、关闭恢复。浏览器交互使用隔离 API 夹具验证，覆盖端口预检失败、任务成功、全部 API 短暂失联、刷新后的只读进度及认证失效返回登录；这不替代生产账号登录验收。

运行命令：

```sh
python tests/deployd_docker_integration.py --recovery
```

测试镜像可从仓库构建：

```sh
docker build -f backend/production.Dockerfile -t puppeteer/server:p0-integration backend
docker build -f backend/test/deployment/runner.Dockerfile -t puppeteer/installer-test:local backend/test/deployment
docker build -f backend/test/deployment/systemd.Dockerfile -t puppeteer/deployd-systemd-test:local backend/test/deployment
python tests/deployd_docker_integration.py --systemd
```

`--systemd` 使用带私有 cgroup 命名空间的临时特权测试容器，以运行真正的 systemd；Docker 守护进程仍来自测试宿主机。只有该临时执行器容器挂载 Docker socket，server/web 不挂载。它与 `--recovery` 为独立验收场景。可传入 `--executor` 指定新编译的 Linux 执行器。脚本仅删除它生成并带唯一标签的测试项目和卷。

当前开发环境为 Windows + Docker Desktop。已在隔离 Linux 容器中运行真正的 systemd，使用服务端镜像内的接管脚本验证服务安装、套接字权限、全部代理开关、管理端口联动及独立进度通道；同时验证了严格 umask 下的权限设置。rc13 已发布配套镜像，发布验收范围见[版本说明](../deploy/production/r17-rc13.md)。远程服务器未自动更新，须由管理员执行升级；远程整机环境仍需核验宿主能力。

后续组件、外部依赖与业务策略的分阶段安排见 [配置分类与自动化开发计划](CONFIGURATION-AUTOMATION-ROADMAP.md)。

## 图形组件启停（开发分支）

匹配版本的安装/升级程序会接管本项目内部 guacd，管理员在“平台配置 → 服务部署 → 图形组件”完成预检和自动启停。无需公开 `4822` 端口；停用配置和数据卷在升级后保留。操作会中断已有连接；共享盘可在独立页签管理，见 [RDP 共享盘自动部署](RDP-DRIVE-AUTO-DEPLOYMENT.md)。适用条件、恢复及验证见 [图形组件自动部署](GRAPHICS-AUTO-DEPLOYMENT.md)。

当前开发分支已接入共享盘自动预检与部署。新安装自动准备项目专用 AppArmor 策略，升级接管保留共享盘停用状态；页面操作会验证挂载读写，停用时移除设备与额外权限，保留 MyZone 数据。宿主不具备 FUSE 或必要策略解析能力时明确失败。详见 [共享盘部署条件与验收边界](RDP-DRIVE-AUTO-DEPLOYMENT.md)。

MP4 渲染器采用内部组件任务 `mp4`，不接受端口或网站参数；队列暂停恢复、真实样例验证及升级保留行为见 [MP4 自动部署](MP4-AUTO-DEPLOYMENT.md)。


## “无法连接部署执行器”排查

应用升级成功或 Compose 容器健康不代表执行器已接入。先核对镜像与脚本版本：rc11 不包含该能力；升级到 rc13 会自动接管。Linux 源码接管步骤见[全新部署指南](FRESH-INSTALL.md#8-接入自动部署执行器)。

Linux 一键安装布局可检查：

```bash
sudo /opt/puppeteer/puppeteerctl ps
# 以下服务名中的 puppeteer 替换为实际 Compose 项目名
sudo systemctl status puppeteer-deployd-puppeteer.service --no-pager
sudo journalctl -u puppeteer-deployd-puppeteer.service -n 100 --no-pager
```

核对已登记的执行器配置、应用侧 NT_DEPLOY_SOCKET/NT_DEPLOY_TOKEN_FILE 和 Unix Socket 挂载是否匹配。不要公开令牌、把 socket 改成 777，或将 Docker socket 挂到应用容器。接管后统一使用生成的 puppeteerctl，直接重建原 Compose 会丢失执行器挂载。

Windows Docker Desktop 的本地验证使用独立 Linux 执行器容器、持久化配置卷和 unless-stopped 重启策略；需要已完成该本地接管，不能靠页面开关补装。仅网页内 RDP 不需要原生 Worker，相关提示见[网页内 RDP](BROWSER-RDP.md)。
