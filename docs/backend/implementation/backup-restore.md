# D04 / R11 备份与隔离恢复实施说明

主责 M16，协作 M18/F00/M04/M08/M13。本文描述已实现的 v1，开发验收见 [R11](../acceptance/R11.md)。运行版本 r11-preview、schema 30、PostgreSQL 18；整个项目仍保留 development-only 启动门禁。

## 归档和数据边界

自有扩展名 `.ntbackup.age`，使用 age X25519 加密 POSIX tar，包含以下条目：

| 条目 | 内容 |
|---|---|
| manifest.json | 首项；格式、备份/实例 ID、时间、应用/schema/PG 版本、范围、每项大小和 SHA-256 |
| database.dump | PG18 pg_dump 自定义格式，public schema，不保留原所有者和 ACL |
| keys/recovery-bundle.json | 当前 MFA 和 Vault 根密钥，仅存在于加密归档及受限离线恢复目录 |
| objects/manifest.json | postgres-chunks-v1 对象清单：载荷行标识、长度、摘要 |

证书、CA、凭据版本、通知/DNS/ACME/远端备份秘密的密文在数据库内，不另建 pki 明文目录。验证器核对全部受支持秘密的解密及证书公私钥。当前支持 local-v1 凭据 KEK；未知密钥版本会拒绝恢复，不宣称已支持 KMS/HSM。

- 控制面（默认）：业务配置、授权、审计记录、密文凭据、证书、解密材料和对象清单；排除 recording_chunks、storage_chunks、upload_chunks、storage_upload_parts 的载荷。恢复后相关录屏和共享文件标记不可用，不能下载空文件或覆盖缺失内容。
- 完整：同时包含上述四张表的载荷，来自同一个 PostgreSQL 导出快照。当前文件在 PG 分块表中，因此完整备份是自包含归档；不是独立 S3 对象版本仓库。
- 两种模式都排除外部资产文件系统、此前备份归档和外部 WAL 仓库。远端复制处理整个加密归档。

manifest 上限 1 MiB，恢复密钥包 16 KiB，对象清单 256 MiB，条目总载荷 100 GiB；这些是 v1 代码限制，没有可任意调大的配置项。拒绝非白名单/重复路径、链接、设备文件、长度/hash 错误、截断和多余密文，流式导出与解密。部署恢复仅接受 schema 30、PG18，不自动跨版本降级，也不接受官方 .ntbak。

age 校验完整性，不证明来源。pg_restore 会执行归档 SQL，必须通过 --trust-source 或 -TrustSource 声明可信来源；非 superuser 目标角色不能替代恶意 SQL 沙箱。

## 页面与身份验证

入口为 `/system-event`，菜单“备份与恢复”，或 `/setting` 的备份标签。

1. 离线执行 `nt backup keygen --output recovery.agekey`。文件不会被覆盖，命令只输出公钥；私钥应保存在独立受控位置。
2. 编辑策略，填公钥并下载私钥验证文件。离线执行 `nt backup verify-recipient --identity-file recovery.agekey --challenge-file recipient-challenge.txt`，将一次性结果填回页面。
3. 输入当前密码，已绑定 MFA 时输入动态验证码。挑战 30 分钟有效，绑定会话且只消费一次。服务端不接收私钥；改变公钥必须重新证明持有私钥。
4. 配置控制面/完整范围、保留天数、每日时间及时区。自动备份默认关闭；启用或重启时会补执行当天已过时而尚未执行的计划。自动备份也关闭现有会话，应选空闲时间。
5. “立即备份”确认维护影响后创建任务，页面显示准备、快照、校验、复制及完成状态。下载需再次验证密码/MFA，使用绑定会话的一分钟一次性票据；下载前复核 SHA-256。

主机没有 nt 二进制时，可使用已构建镜像进行离线操作，例如先创建受控的 D:\offline 目录，再执行 `docker run --rm --network none -v D:/offline:/offline --entrypoint /app/nt nt-backend-r01-server backup keygen --output /offline/recovery.agekey`。该命令无需连接数据库。验证挑战时使用相同离线目录，并将子命令替换为 verify-recipient 和对应容器内文件路径。

管理查询使用 backup.manage；敏感配置变更和导出使用 backup.export。HTTP 不返回私钥、运行密钥、数据库密码或 plan.json。

## 一致性、任务与保留

维护先冻结业务入口和新登录，停止现有会话，取消并等待后台 Worker 实际退出，最多等待 60 秒排空请求。用专用 NOLOGIN 只读角色 nt_r11_backup_reader 读取同一 REPEATABLE READ 导出快照，核对密钥指纹、生成对象清单并执行 pg_dump。只授予 public 表和序列读取权限；数据库密码通过私有 .pgpass 传递，不进入 argv。

私有临时目录内完成空间预检和归档，fsync 后不覆盖地发布。加密文件登记成功即释放维护，再复制到远端。远端故障不会删除可用本地备份。任务具有租约、心跳、幂等键和执行 fencing；重试复制使用原文件和原目标配置快照，不重新取快照。未知结果不能直接标成功。

保留默认 31 天，可设 1–3650，每小时清理。至少保护最新可用归档和最近整体成功归档；进行中任务、有效下载票据、正在下载的文件不会被清理。删除状态持久化，可重试；远端最终文件和本任务临时文件随对应过期本地归档清理。恢复出的旧复制记录待复核，不会自动删除原部署的远端对象。

## 远端存储

目标配置按任务取快照，凭据由 Vault 加密。测试连接真正写入、读回校验和删除小型加密测试文件。正式复制先临时上传、读回校验，再不覆盖地发布并再次读取核对。

| 类型 | 配置及前提 |
|---|---|
| S3 | 预建 bucket；endpoint、region、access/secret key、前缀；MinIO Go SDK，支持 multipart |
| SFTP | host:port、用户、密码或私钥、固定 SHA256 主机指纹、可写目录；要求 OpenSSH hardlink 扩展 |
| WebDAV | 预建 collection、Basic 认证和 PUT/GET/MOVE/DELETE 权限；MOVE 禁止覆盖 |

HTTPS 默认验证证书，可配置私有 CA；不关闭证书验证，不跟随重定向。HTTP 必须明确勾选“仅隔离内网测试”。SFTP 拒绝未知或变化的指纹。失败复制可重试；匹配的已发布对象可确认成功，摘要不同的对象不会被覆盖。

## 离线恢复

推荐在仓库根目录执行 [restore-backend.ps1](../../../scripts/restore-backend.ps1)。归档和私钥以只读文件挂载给临时 CLI，不上传网页。

```powershell
# 恢复、验证并启动服务，业务仍冻结
./scripts/restore-backend.ps1 -Action Restore -Archive 'D:\offline\backup.ntbackup.age' -IdentityFile 'D:\offline\recovery.agekey' -TrustSource
# 以下编号替换为真实返回的 restoreId
./scripts/restore-backend.ps1 -Action Status -RestoreId 'restore-实际编号'
./scripts/restore-backend.ps1 -Action Activate -RestoreId 'restore-实际编号'
# 仅开放写入前允许回退；中断后可继续
./scripts/restore-backend.ps1 -Action Rollback -RestoreId 'restore-实际编号'
./scripts/restore-backend.ps1 -Action Resume -RestoreId 'restore-实际编号'
```

脚本支持 ComposeFile、EnvFile、ProjectName、ReadyUrl；只停止当前部署的 API 和已存在的图形服务，不停止 PostgreSQL。

1. 完整验证归档，在 restore/restoreId 下建立独立日志。校验失败不切换。
2. 独占工作目录锁和数据库离线锁，拒绝仍存在 API、图形进程或其他数据库客户端的情况。
3. 先创建当前环境的完整安全备份，使用当前环境已验证的公钥。跨实例恢复时需同时保管当前环境和待恢复归档的对应私钥。安全备份失败立即终止，原数据库和运行配置不变。
4. 持久化私有计划，再建立随机隔离数据库及非 superuser 所有者；PG18 pg_restore 使用单事务、遇错终止、忽略原所有者和 ACL，不对原库执行 --clean。
5. 核对实例/schema、有效管理员、秘密、证书及完整对象清单。旧登录、刷新令牌、客户端票据、Agent 信任及无法核实的旧客户端证书失效；旧任务、通知、轮换、计划及外部集成被隔离，不能自动重放。
6. 两个服务均停止时，分别原子写入 API 的“数据库+密钥”配置和图形服务的“数据库”配置。并非跨文件原子事务；半切换保持停止/冻结，由 resume/rollback 核对并修复，脚本不会直接开放业务。
7. 等待匹配 restoreId 的 API 和图形存储健康。Activate 先持久化已开放写入边界，再开放服务。用户重新登录查看独立恢复记录，并按业务需要重新启用集成。

state.jsonl 逐条 fsync，目录/文件为 0700/0600，页面只展示事件。崩溃留下的不完整尾行可修复；resume 重新核对暂存摘要和实际目标状态，不只相信最后一条日志。plan.json 和解密暂存文件含秘密，仅本机运维可访问。

开放前可切回旧库与匹配密钥；开放后自动回退被永久拒绝，脚本先检查再决定是否停止服务。旧库、安全备份、恢复计划不自动删除，建议至少观察 7 天再由运维核对清理。异常临时目录应在确认没有相关进程后清理，不能删除当前计划仍依赖的 staging。

底层 CLI 还提供 backup create/inspect/pack/stage、restore prepare/run/status/resume/rollback/activate。backup create 要求离线；restore prepare 仅隔离装载、不切换。运行镜像已包含 PG18 客户端。

## 物理灾备与兼容边界

每日逻辑备份不能因此承诺 15 分钟 RPO。R11 另交付独立 PG18 + pgBackRest 2.58.0 演练，覆盖加密基础备份、WAL 归档、时间点恢复、目标前后事务核对、密钥/文件验证、旧认证/任务隔离。入口为 backend/fixtures/pitr、TestR11PITRFixture；部署切换演练见 [test-r11-restore.ps1](../../../scripts/test-r11-restore.ps1)，结果和可复现命令见验收记录。

生产仍需独立故障域仓库、周完整/日差异备份、持续 WAL、archive_timeout=60s、至少两条完整链、超过 5 分钟未归档告警，并按实际容量重复测量。当前单机内网演练不代表已部署生产异地灾备，不自动改变当前业务库 archive_mode。恢复私钥、仓库密钥和对应应用根密钥需独立保管。

官方 .ntbak 和闭源表结构没有可验证契约，不属于 v1 格式支持。旧系统迁移走授权导出/API、DTO 映射及 dry-run；无法安全解密的凭据重新录入，旧审计保留为外部只读档案。
