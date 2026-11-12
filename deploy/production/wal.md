# PostgreSQL WAL 与时间点恢复预览

2026-11-12 验收范围更新：按用户确认，使用 Docker 模拟生产环境作为本轮验收环境，不等待真实异地服务。已增加独立 SSH 仓库容器、源库与恢复卷的故障恢复脚本，见[模拟远程仓库](../../backend/fixtures/r17-remote-wal/README.md)。部署到实际主机时仍需按自身容量与故障域配置，但不作为本轮等待用户提供环境的条件。

此可选组件为 PostgreSQL 18 提供在线加密基础备份和 WAL 归档。它不迁移旧数据，不自动启用，不替代应用 full 归档、离线应用密钥和异地副本。本地 `walrepo` 卷与数据库在同一 Docker 主机，主机丢失时可能同时丢失；不能据此宣称异地容灾完成。

## 新部署启用

在 Linux 部署主机执行（配置目录已由 production-config 初始化）：

```sh
docker build -t puppeteer/postgres:r17-wal-preview -f deploy/production/postgres.Dockerfile deploy/production
sudo node scripts/production-wal.mjs /etc/puppeteer
sudo node scripts/production-preflight.mjs /etc/puppeteer/production.env --services --protocols --agents --wal
```

按实际启用组件选择预检查参数。WAL 密钥独立随机生成，0400、UID70，仅 PostgreSQL 挂载。再次运行不替换已有密钥或指定镜像；密钥需离线备份，丢失后无法解密仓库。不能直接替换密钥来“轮换”既有加密仓库。

在所有部署命令中保留 `-f deploy/production/wal.yaml`，其他叠加文件也保留。示例仅开启核心与 WAL：

```sh
docker compose --env-file /etc/puppeteer/production.env -p puppeteer \
  -f deploy/production/compose.yaml -f deploy/production/wal.yaml up -d --wait
```

下文的 `docker compose ...` 表示上面完整的环境参数与实际全部叠加文件，不能直接把省略号作为参数执行。启动后立即初始化归档 stanza 并创建首份基础备份：

```sh
docker compose ... exec -T --user postgres postgres pgbackrest-file --stanza=puppeteer stanza-create
docker compose ... exec -T --user postgres postgres pgbackrest-file --stanza=puppeteer check
docker compose ... exec -T --user postgres postgres pgbackrest-file --stanza=puppeteer --type=full --start-fast backup
docker compose ... exec -T --user postgres postgres pgbackrest-file --stanza=puppeteer --output=json info
```

首次 stanza 创建前归档可能暂时失败；不能在基础备份及 check 成功之前宣布受保护。已有实例启用需要重建 PostgreSQL 容器，属于维护窗口操作，本批未在现有业务实例执行。

## 调度与告警

已提供[定时维护任务及 systemd 模板](systemd/README.md)：每周full、其余每天diff、每5分钟check。单次任务通过Docker CLI执行，保存最近成功时间和通知待发送队列，失败返回非零；通知接收失败后按事件ID重试。模板保留两份full及依赖链；这不是默认31天访问记录保留策略，也不保证31天恢复窗口。模板需按文档安装启用，不能把一次备份通过视为计时器已运行。

归档使用同步推送，未成功写入仓库时不报告成功；不配置达到队列上限就丢弃 WAL 的策略。持续检查 `pg_stat_archiver` 的 failed_count 增量、最近成功时间、基础备份年龄及数据库/WAL卷空间。`archive_timeout=60` 不是60秒RPO承诺，仓库故障、无写入和调度状态都影响观测值。仓库不可写时必须告警，不能通过删除 pg_wal 文件腾空间。

## 隔离恢复

只在独立主机/私有网络及全新空卷恢复，使用匹配的PG18镜像、WAL仓库和离线WAL密钥。仓库应以只读恢复凭据提供；源实例不停止归档、不改数据库地址。恢复命令示意：

```sh
pgbackrest-file --stanza=puppeteer --pg1-path=/private/empty-restore \
  --type=time --target='2026-11-12 09:00:00+00' --target-action=promote --archive-mode=off restore
```

目标时间须处于已验证的基础备份/WAL范围，包含时区；目录必须全新，不能使用 delta 覆盖业务库。恢复 PostgreSQL 子进程使用 `pgbackrest-file` 读取密钥，避免丢失归档解密配置。先检查恢复状态和目标前后事务，再执行应用层密钥、共享文件、录屏、旧会话/设备/任务隔离验证。物理恢复不会自动撤销旧会话，不能直接对外开放或切换业务；按已验证的[隔离切换流程](cutover.md)完成应用隔离及开放。

## 异地副本边界

应用备份目标现有 SFTP/S3/WebDAV 上传支持摘要校验和凭据保护，可用于加密应用归档的远程副本。目标应位于独立故障域，SFTP固定主机指纹、HTTPS验证CA，凭据只授予专用前缀所需权限。恢复私钥另存，不能随归档一起上传。内网模拟协议测试只证明协议实现，不证明真实异地耐久性。

WAL持续异地归档需要配置pgBackRest远程仓库或受保护对象存储。Docker远程SSH仓库、固定主机指纹、加密归档、仓库停机/重试、源库离线恢复已经通过；复现见[远程仓库夹具](../../backend/fixtures/r17-remote-wal/README.md)。加速调度、失败通知持久重试、完整应用PITR及切换结果见[R17验收](../../docs/backend/acceptance/R17.md)。不能对运行中的WAL仓库做一次普通文件复制就宣称得到一致的可恢复异地副本，Docker模拟也不能证明真实独立故障域的耐久性。

依据：[pgBackRest 配置参考](https://pgbackrest.org/configuration.html)、[恢复命令参考](https://pgbackrest.org/command.html)。
