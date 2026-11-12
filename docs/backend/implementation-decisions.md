# 可落地实施决策与开发门槛

本文件补齐原“尚需实施验证”部分。决策基线为 2026-10-26，沿用 Go + PostgreSQL、18 个业务模块及 F00。**技术路径已选定，产品代码尚未实现，集成验收尚未执行**。下面的任务、接口和指标是本项目的开发规范，不是对闭源后端实现的推断。

## 已确定的交付路径

| 编号 | 主责 / 协作 | 实现决策 | 开发说明 |
|---|---|---|---|
| D01 | M06 / M01、M02、M05 | 原生 SSH + 自有 Go 客户端，OAuth PKCE 登录、WSS 桥接；Termark 使用公开 SSH URI | [客户端与 Agent](implementation/native-client-agent.md) |
| D02 | M05 / M04、M13、M15 | 自有 Agent v1：WSS、设备凭据、yamux、限定目标拨号、TUF 更新 | [客户端与 Agent](implementation/native-client-agent.md) |
| D03 | M06 / M05、M07、M08、M13 | FreeRDP 代理侧车，Go supervisor + 每会话 C worker；服务端票据、通道策略和录屏 | [原生 RDP](implementation/native-rdp.md) |
| D04 | M16 / F00、M04、M08、M13、M18 | pg_dump 自定义格式 + tar + age，自有版本化备份；隔离恢复后切换 | [备份与迁移](implementation/backup-restore.md) |
| D05 | M18 / M01、M02 | 自有能力提供器；需要商业发行时使用独立 Ed25519 签名许可证 | [配置与能力](implementation/settings-entitlements.md) |
| D06 | M18 / 各领域 owner | 103 个文档属性逐项定义内部类型、默认值、校验和应用方式 | [配置目录](system-property-catalog.md) |
| D07 | M10 / M01、M02、M04、M08、M11 | MySQL TLS完整认证、专用密码验证器、协议审计与PG扩展 | [数据库认证](implementation/database-auth.md) |

原生 RDP 是 Go 主架构中的独立 C 协议进程，不要求用 Go 重写 RDP。浏览器 RDP/VNC 继续使用 guacd；两者分别验收，不能相互代替。Termark 公开 URI 接入不等于实现其 Next Terminal 目录同步和私有 HTTPS 帧协议。自有归档不宣称可导入官方 `.ntbak`。

## 技术组件与采购边界

| 用途 | 选用方式 | 固定与升级规则 |
|---|---|---|
| 控制面、客户端、Agent、Worker | Go 标准库、pgx/v5，沿用模块化单体 | 开始编码时锁定受支持 Go 补丁版、go.sum；不使用浮动容器 latest |
| 业务数据库 | PostgreSQL 18，独立迁移角色和运行角色 | pg_dump/pg_restore 使用同主版本客户端；恢复兼容表由版本清单控制 |
| WebSocket | R04 浏览器终端采用 gorilla/websocket v1.5.3；Agent/原生客户端的 coder/websocket v1.8.15 仍为后续研究基线 | R04 采用显式单读/串行写与 socket deadline，适配现有文本帧；固定版本，应用层限制大小、超时并做真实协议回归 |
| Agent 多路复用 | hashicorp/yamux，研究基线 v0.1.2 | 固定协议版本和窗口上限，端到端故障测试后发布 |
| 原生 RDP | FreeRDP 3.32.2 的 server/proxy，定制模块及必要补丁 | 固定 tag/commit、镜像 digest；补丁单独维护，升级重跑协议矩阵 |
| 归档加密 | age，研究基线 v1.3.2；Go age 库 | X25519 接收者，恢复私钥单独保管；不用自创文件加密算法 |
| 许可证签名 | go-jose/v4，JWS EdDSA/Ed25519 | 固定允许算法和内置公钥；与 OIDC RS256 密钥完全隔离 |
| Agent 软件更新 | go-tuf/v2，TUF 元数据 | 离线 root、targets 签名、版本和到期校验；先灰度再扩大 |

上述版本来自本次官方仓库/发布记录核查，是复现基线而不是永久最新版本承诺。W0 输出依赖锁定清单、SBOM、许可证清单与漏洞扫描结果；无法满足安全维护要求时更新补丁版本并重跑对应测试。目标 Windows 测试机和 RemoteApp/RDS 环境由部署方提供有效授权，本方案不包含这些商业软件的分发。

依据：[FreeRDP 发布](https://github.com/FreeRDP/FreeRDP/releases/tag/3.32.2)、[WebSocket 库](https://github.com/coder/websocket)、[yamux](https://github.com/hashicorp/yamux)、[age](https://github.com/FiloSottile/age)、[JOSE](https://github.com/go-jose/go-jose)、[TUF Go 实现](https://github.com/theupdateframework/go-tuf)。具体协议决策为本项目设计。

## 可直接建立开发任务的顺序

| 任务 | 批次 / 前置 | 必交付物 | 验收出口 |
|---|---|---|---|
| D06.1 | W0 | 属性注册表、读写适配、跨字段校验、revision/ACK 状态机 | 103 键各有合法/非法输入用例；只读、掩码、冲突、节点失败用例通过 |
| D05.1 | W0，D06.1 | local 能力提供器、真实 implemented 集合、配额预留 | 未实现能力不可开启；并发额度不超卖；前端显示 disabledReason |
| D01.1 | W1/W2，M01/M02/M06 | PKCE、原生 SSH、资产目录、一次性 bridge ticket | 权限/MFA/票据重放测试及真实 SSH 登录通过 |
| D02.1 | W3，D01.1 与 M05 | 注册、轮换、WSS/yamux、拨号许可、心跳、指标 | 跨 NAT 连接、断链、吊销、DNS 变化、背压和半关闭测试通过 |
| D01.2 | W3，D02.1 | nt-client loopback bridge、Termark URI、客户端安全存储 | 仅 443 出站环境完成 SSH；资源撤权后 15 秒租约内关闭 |
| D04.1 | W3，M04/M13/F00 | 加密备份、校验、独立恢复日志、安全备份、隔离恢复 | 新实例恢复身份/秘密/CA，旧票据失效；中断后可重入或回退 |
| D03.1 | W3，M06/M05 | 固定版本侧车构建、ticket hook、目标 NLA/TLS、连接进程隔离 | mstsc → proxy → Windows 实机连接；坏票据/坏证书拒绝 |
| D03.2 | W3/W4，D03.1 与 M08 | 通道能力矩阵、录像、终止、资源限额 | 录屏与限制策略实机通过；禁止仅用自生成 .rdp 验证 |
| D03.3 | W4，D03.2 | RemoteApp、剪贴板方向过滤、磁盘重定向完整支持 | 每个入口/客户端/策略组合分别签署结果；失败组合不可发布 |
| D02.2 | W3/W4，D02.1 | 更新仓库、签名验证、灰度、服务重启和回滚 | 错签名/过期/降级拒绝，升级失败能恢复前版及登记告警 |
| D05.2 | W4，可独立于核心部署 | 自有签名许可、导入/到期/撤销与审计 | 篡改/错实例/错算法/并发额度用例通过 |
| D04.2 | 生产发布前，D04.1 | WAL/PITR、对象版本保留、密钥异地恢复、运维手册 | 约定环境 RPO ≤ 15 分钟、RTO ≤ 60 分钟演练实测报告 |
| D07.1 | W4，M01/M02/M04 | MySQL TLS完整认证、Argon2id verifier、text/prepare和只读目标角色 | CLI/JDBC/DBeaver认证与SQL审计通过 |
| D07.2 | W4，D07.1/M11 | 工单执行、取消、unknown、协议故障恢复 | 提交后断网不重复执行，解析/事务错误无越权 |
| D07.3 | 独立扩展，M10 | PG SCRAM、extended/COPY/pipeline和取消 | psql/JDBC/DBeaver矩阵和故障用例通过 |

任务详情中的端口是服务契约。开工时必须提交 OpenAPI/内部 RPC schema、SQL 迁移和真实客户端 fixture 到同一变更，不允许以“未来补接口”结束任务。新协议全部放 `/api/native/v1`、`/api/agent/v1` 等显式版本命名空间；已有前端 `/api` 路径保留兼容适配。

## 验证环境和证据格式

W0 创建 `backend/tests/lab/`：Compose 负责 PG18、控制面、Worker、对象存储、SSH 测试机、Agent、故障注入代理；Windows VM 提供 mstsc/RDP server，Linux VM 提供 xfreerdp；RemoteApp 额外提供 RDS 测试资产。CI 的 Linux 容器测试不替代 Windows 实机测试。

每次协议报告保存 `caseId, commit, imageDigest, serverVersion, clientVersion, OS, configRevision, policyRevision, expected, actual, duration, evidenceRefs, passed`。证据包括脱敏日志、session/recording ID、资源曲线和故障注入时间线。票据、密码、CA 私钥不进入报告。未执行记录 `not_run`，失败记录 `failed`，不可写“支持但待确认”。

三层发布门禁：①确定性单元/契约测试；②真实数据库、目标系统和故障注入集成测试；③协议客户端矩阵和恢复演练。能力注册需要同时满足 `implemented && entitled && configured && testedForProfile`，用户权限另行计算。缺少必需的审计/策略能力时拒绝会话。

## 私有兼容工作如何关闭

| 工作 | 不阻塞的独立交付路径 | 官方兼容的完成条件 |
|---|---|---|
| Termark 专有目录同步/HTTPS 隧道 | 公开 URI 接 SSH 代理；HTTPS-only 用 nt-client | 提供准确客户端版本、授权测试账号和合法协议材料；授权/同步/撤权/帧/前置认证/代理/mTLS 全部真实联调 |
| 官方 nt-tunnel/Agent | 自有 Agent v1、配置迁移工具 | 官方二进制版本与协议说明齐备；登记/拨号/心跳/更新互通矩阵通过 |
| 官方 .ntbak | 自有 .ntbackup.age 与逐领域导入 | 样本、密码和官方授权导出渠道；识别格式后还须完成数据模型映射，不能直接恢复进新表 |
| 官方商业许可证 | local 能力配置或本项目独立签名许可 | 只有获得官方集成契约后新增 provider；不模拟其签名或许可证状态 |

这些是独立兼容交付项，状态明确为 `external_contract_required`，不能通过 mock 宣称完成，也不再作为自有系统架构选型的前置阻碍。使用文档嵌入视频未读取仍保留在来源审计中；它不是上述技术方案的实现依赖。
