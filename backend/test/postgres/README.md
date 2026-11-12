# PostgreSQL 原生客户端验收

从项目根目录运行 `./backend/test/postgres/run.ps1`。先启动 `compose.r16.yaml` 的 `r16-postgres-fixture`，并保留 `.local/r16-pg/server.pem`。脚本创建专用测试工具镜像，下载并校验固定版本 pgJDBC 42.7.14，使用 PostgreSQL18 内网夹具与一次性业务测试 schema。不会轮换实际用户密码，也不连接外部数据库。

自动化矩阵：

- TLS verify-full、SCRAM、日期/时区初始化、错误密码与未加密连接拒绝。
- pgx / psql / pgJDBC 查询，二进制/文本/NULL 参数与重复预编译。
- JDBC 表、字段、主键、索引、schema、类型、过程、外键和权限元数据。
- JDBC 隔离级别、BEGIN READ ONLY、readOnlyMode=always、保存点、commit/rollback、fetchSize、maxRows。
- 取消查询及恢复；写入/READ WRITE/默认只读绕过拒绝；Sync/pipeline/portal 恢复。
- 参数值不落审计，推断 OID 落库；10万行上限关闭超限连接且不影响其他会话。
- startup options/search_path/role/replication/异常日期与时区值拒绝。
- 密码轮换、授权撤销和空闲租约关闭；取消令牌伪造/跨地址/过期拒绝。

`ClientMatrix.java` 从临时子进程环境读取测试密码，不输出密码。当前通过驱动矩阵不等于通过 DBeaver 桌面验收；后者需实际展开对象树、打开数据和执行 SQL 后单独签收。

参考：[pgJDBC 官方下载](https://jdbc.postgresql.org/download/)、[连接参数](https://jdbc.postgresql.org/documentation/use/)、[42.7.14 初始化源码](https://github.com/pgjdbc/pgjdbc/blob/REL42.7.14/pgjdbc/src/main/java/org/postgresql/core/v3/ConnectionFactoryImpl.java)。

驱动 SHA256：`73914527305a40cce504b0d3d90b23caf565136912d607ae8ae7c5895512332c`。

## DBeaver 桌面验收

从仓库根目录运行 `./backend/test/postgres/desktop.ps1`，等终端显示 `isolated DBeaver fixture ready`。此环境仅监听本机 `localhost:15433`，默认 120 分钟后关闭（`-Minutes 5..240` 可配置），使用独立测试 schema；不会修改平台账号或业务资产。启动前应先结束使用同一个 PostgreSQL 夹具的自动化测试；使用独立测试网络与数据库时，可传 `-Network nt-r16-dbeaver-isolated` 并行验收。

1. 在 DBeaver 新建 **PostgreSQL** 连接，名称填 `R16 isolated PostgreSQL`。主机 `localhost`，端口 `15433`，数据库 `inventory`，用户名 `pg-desktop@desktop`。
2. 用本地编辑器打开 `backend/.local/r16-dbeaver/connection.json`，仅把其中临时 `password` 填到密码框；不要发到聊天或写入验收日志。
3. 驱动属性设置 `sslmode=verify-full`、`sslrootcert=E:/AIAgents/codex/Next-Terminal/backend/.local/r16-dbeaver/proxy-ca.pem`、`requireAuth=scram-sha-256`、`readOnlyMode=always`。不要关闭证书校验。若驱动未安装，可在“编辑驱动设置 → 库”使用本地 `backend/.local/r16-jdbc/postgresql-42.7.14.jar`。
4. 开启连接的“只读”选项，测试连接并保存。展开 `inventory → Schemas → public → Tables → items`，检查列 `id`、`label`，然后打开数据页。
5. 新建 SQL 编辑器执行 `SELECT id, label FROM items ORDER BY id;`。预期两行：`1 / R16 PostgreSQL fixture`、`2 / NULL`。
6. 再执行连接文件中的 `acceptanceQuery`（每次启动有唯一标记，预期一行），确认对象树、数据页和 SQL 结果都正常后关闭连接，向验收人员反馈三个结果。验收人员再创建 `backend/.local/r16-dbeaver/complete` 文件；脚本要求找到本次标记的成功执行审计和一行结果，其他客户端的普通查询不会替代该证据。缺少标记时保留环境至原有效期，便于核对；成功后销毁临时 schema 并移除临时密码文件。

自动化测试不得自己创建完成标记代替桌面观察。若超时或失败，保留错误信息，重新启动后使用新密码。此桌面夹具默认不参加 `go test ./...`。
