# R06 Linux Agent 部署与更新

交付基线：Linux amd64、自有 nt-agent v1 协议、Go 后端 r06.6。不是官方旧 Agent 的兼容替代程序。Windows 服务和其他 CPU 架构不在本轮交付基线。

## 安装

### 从资产页面选择安装

进入“Agent 网关”顶部的“选择资产安装”，搜索并选择已启用的 SSH 资产。资产必须已配置凭据，并在资产页面核验主机密钥；安装沿用直连、Agent、跳板、网关组或网络代理路径。

填写 Agent 名称、目标机器实际可达的后端 HTTPS origin、允许拨号的 CIDR 与端口。后端生成受限设备身份，通过 SSH 的私有 tar 数据流上传程序和 0600 配置，不需要人工复制令牌。目标须为 Linux x86_64、运行中的 systemd，SSH 账号须为 root 或具有免密 sudo 权限，并具备 tar、sha256sum、useradd、install 等基础命令。

安装创建 nt-agent 系统账户（若不存在）、/opt/next-terminal-agent、/var/lib/next-terminal-agent 和 /etc/systemd/system/nt-agent.service，启用开机启动。已有安装或同名服务会被拒绝，不覆盖。程序传输后校验 SHA256；只有服务启动且后端确认 Agent 可拨号后才报告成功。

任务进度持久化，关闭弹窗不影响执行；单任务最多三分钟，全局最多四个，同一资产仅一个运行任务。身份、资产、凭据或主机信任变更会中断任务。失败时本次设备 Key 撤销；若已经创建远端目录或服务，需运维检查并清理该失败安装后重试，系统不会自动删除可能需要排查的现场。重启遗留任务在超时后显示失败。

此入口安装普通 systemd Agent。TUF 更新、私有 CA、入口 mTLS/Header/代理等高级配置仍按下方说明配置，安装器不关闭 TLS 校验。不能把前端 Vite 地址或另一台机器的 localhost 作为后端地址。

### 手工安装与高级配置

1. 管理员在“Agent 网关”下载 Linux amd64 程序，创建限定目标 CIDR/端口的注册令牌。令牌十分钟有效且只能使用一次。
2. Linux 创建专用普通用户 nt-agent；创建 /opt/next-terminal-agent 和 /var/lib/next-terminal-agent，所有者设为该用户，目录权限 0700。将程序安装到前者，权限 0700。将令牌通过安全渠道存为后者的 token.txt，权限 0600。
3. 以 nt-agent 用户运行以下命令。endpoint 必须是 Agent 可以访问的后端 HTTPS 地址。成功登记后进程进入运行；确认在线后停止前台进程，并移除已消费的 token.txt。

```sh
/opt/next-terminal-agent/nt-agent --endpoint https://backend.example.com --name site-a --token-file /var/lib/next-terminal-agent/token.txt --config /var/lib/next-terminal-agent/agent.json
```

4. 保留自动生成配置中的 agentId、deviceKey，参考 [配置示例](agent.example.json) 补充采集与更新配置。配置文件必须是普通文件，最大 16 KiB，0600 或更严格。不要把令牌、设备 Key 或完整配置放进命令行、日志或代码库。
5. 将经过可信渠道分发的 TUF 1.root.json 保存为 trusted-root.json；配置 updates 后，安装 [systemd 单元](nt-agent.service) 到 /etc/systemd/system/，执行 systemctl daemon-reload 和 systemctl enable --now nt-agent。没有更新仓库时不使用 --supervise，直接运行普通 Agent。

systemd 使用专用用户、只读系统目录和有限可写目录；可执行目录必须允许该用户替换二进制。服务停止会结束子进程与当前连接。注册配置应另外安全备份，不得为修复连接问题重新生成设备 Key。

## 网络与指标配置

schemaVersion 为 1；collectionSeconds 为 5–300，默认 5；heartbeatSeconds 为 10–60，默认 30，协商后连续三个周期缺失即离线。diskPaths 最多 32 个绝对路径，networkPattern 为接口名正则。磁盘与网络按 Agent 所在主机/容器可见范围采集；网络值是累计字节，指标缺失明确显示，不补零。

proxyUrl 支持 HTTP/HTTPS CONNECT、SOCKS5、SOCKS5H；SOCKS5 本地解析目标，SOCKS5H 交给代理解析。caFile 用于自建 CA，certFile/keyFile 用于入口 mTLS。headers 最多 16 个并仅发给配置的后端域名，禁止覆盖安全相关保留头。basicUsername/basicPassword 可用于入口代理认证，此时设备认证使用 X-NT-Agent-Key，入口代理必须保留该头。

首次注册需要上述设置时，将不含 deviceKey 的私有 JSON 传给 --transport-config。默认不允许明文后端，隔离开发环境才可显式开启 --allow-insecure-development。TLS 校验不会关闭，也不跟随重定向。更新仓库独立使用系统 CA 和系统 HTTP 代理环境；Agent 的 mTLS/headers 不会泄露给更新仓库。

## 签名发布与灰度

R17 独立部署增加 `nt-agent-release --repo ... --export 新公开目录`：导出前验证签名、时效、版本和二进制摘要，只导出公开白名单文件。签名目录与仓库嵌套会被拒绝。HTTPS 挂载步骤见[生产运维说明](../../deploy/production/operations.md)。

在可信离线构建/签名机器构建 cmd/nt-agent-release。发布目录和私钥目录严格分离；以下命令中的目录为示例。

```sh
nt-agent-release --init --repo /srv/agent-public --keys /secure/agent-signing
nt-agent-release --repo /srv/agent-public --keys /secure/agent-signing --binary ./nt-agent-linux-amd64 --version r06.5 --sequence 1
```

初始化创建 root/targets/snapshot/timestamp 四个独立 Ed25519 私钥。将 root.key 离线保存；后续发布无需 root.key。只通过 HTTPS 静态站点发布 agent-public 的内容，绝不能发布 agent-signing。初始根元数据须经独立可信渠道交付，不从未验证的网络动态信任。根有效期一年；发布元数据有效期 24 小时，必须在过期前重新签名发布更高 sequence。根轮换使用`nt-agent-release --rotate-root NEW_REPO --new-keys NEW_KEYS`及原`--repo`/`--keys`目录，生成旧新双签名版本化根；完整步骤见[生产运维](../../deploy/production/operations.md)。须在当前根到期前执行，不直接覆盖客户端信任根。

sequence 必须严格递增，version 是显示标签。发布先目标和元数据、最后 timestamp；并发发布须由运维串行化。生产推荐在独立目录生成完整发布后切换静态站点目录，避免发布中途读取。客户端使用 go-tuf 校验签名、过期时间、版本回退、目标长度和 SHA256；安装前再次验摘要。最大目标 128 MiB。

autoInstall 默认为 false：每十分钟检查并展示更新，管理员在“监控”展开指定节点，先“检查更新”，再“安装更新”。先选一台灰度节点，验证在线、拨号、指标和访问审计后，再逐个推广。自动安装仅在明确配置 autoInstall=true 时执行。

## 排空、回滚与恢复

安装请求先停止接收新拨号，保留已有会话，等流计数为零再更新；长会话会延后安装，管理员可在在线会话中主动结束测试会话。排空完成后重新校验最新签名元数据，将候选复制到同一文件系统，保留带 sequence 后缀的旧程序，再启动新工作进程。

新进程须在 30 秒内完成后端认证握手且连续存活 5 秒。失败自动恢复旧程序并重启，监控显示 rolled_back；失败 sequence 被记录并拒绝重复安装。修复后必须发布更高 sequence，不要删除 rejected.json 来强行重试。旧版本备份保留在程序目录，运维在确认稳定且没有更新事务后管理历史备份空间。

中断安装在 supervisor 重启时按 transaction.json 恢复上一个程序。数据库、Agent 私有状态目录和信任根须备份。恢复失败时停止服务，保留现场日志和事务文件，由运维修复路径权限/磁盘空间后再启动；不跳过签名验证。设备 Key 轮换有五分钟旧 Key 重试窗口，新 Key 连接确认后旧 Key 立即失效；轮换会中断旧连接，适合维护窗口操作。

## 验证入口

见 [R06 验收记录](../../docs/backend/acceptance/R06.md)。监控只存最新采样，不是历史时序平台；诊断检查已配置资产的网络路径和 SSH 主机指纹，不自动信任、不执行远端命令。网关组仅在新连接建立时按顺序故障切换，存量 SSH 连接不会自动重放。所有管理接口要求 identity.manage。
