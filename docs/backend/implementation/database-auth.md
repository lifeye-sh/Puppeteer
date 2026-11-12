# D07 数据库代理认证与协议交付

主责 M10，协作 M01/M02/M04/M08/M11。关联 U21–U23/U36。MySQL 为使用文档基线，PostgreSQL 原生代理为独立扩展；内部业务库使用 PostgreSQL 不等于两种代理都已实现。

## MySQL 身份材料不再等待选型

首版只接受 TLS1.2+ 的 MySQL 客户端连接，认证插件固定 `caching_sha2_password`，每次走 **full authentication**。服务端发送握手/nonce，客户端先提交 SSLRequest，完成 TLS 后提交 HandshakeResponse41；服务端不提供 fast-auth 缓存，返回 AuthMoreData 的 full-auth 请求，接收 TLS 内的 NUL 结尾密码并验证。拒绝非TLS、旧认证插件和公钥换取的非TLS密码通道，不向客户端下发目标库密码。该完整认证路径有官方协议依据：[caching_sha2_password](https://dev.mysql.com/doc/dev/mysql-server/latest/page_caching_sha2_authentication_exchanges.html)。

M01 个人数据库密码生成32随机字节并Base64URL显示一次；存储Argon2id PHC（初始 m=64MiB、t=3、p=1、随机16字节salt，部署前基准调整），auth_plugin=`caching_sha2_password`、verifier_format=`argon2id-fullauth-v1`，验证材料仍由M04加密封装。这不是MySQL原生fast-auth缓存格式，代理完整认证后可用自己的验证器；**不能拿普通API token摘要去校验scramble**。采用有限并发密码验证池，初始8并发、等待队列32、超额拒绝；再按实际登录负载调节，防止内存耗尽。凭据轮换/吊销及过期归M01。

用户名沿用 `username@asset_name`；首版平台username和数据库asset alias禁止 `@`，使用最后一个分隔符解析并唯一匹配。个人数据库密码首版每用户一个active值，多名称历史版本允许保存但不同时逐个验证。因为已有wire用户名没有keyId，不提供无限多个并行密码；未来多凭据必须设计显式版本化编码，不偷改此契约。

连接认证后重新检查资源授权和数据库策略；上游连接由M04目标凭据建立，两段TLS分别验证证书。登录失败对外统一错误，内部区分平台认证/资源授权/目标不可用；不暴露资产是否存在。禁用用户或撤权通过租约结束连接。

## 协议组件与审计边界

选 `go-mysql-org/go-mysql` 的 packet/client/server 组件作为实现基础，认证适配层实现上述TLS/full-auth和M01 verifier回调；若默认server API要求明文密码，不把明文写回数据库，而是在独立适配包修改认证回调，保持固定版本补丁和握手fixture。库提供协议工具，不自动满足审计产品规则。[官方仓库](https://github.com/go-mysql-org/go-mysql)

首版一个客户端连接对应一个目标连接。禁止跨用户复用目标会话，保持schema、session variables、事务状态和prepared statement ID映射。对每个 command 建立 audit operation ID，记录执行前意图与执行后状态。最大单条SQL1MiB、最大协议包64MiB、结果流式背压；超限返回明确错误并恢复协议状态或断开，不能把全结果载入内存。

| 协议功能 | 实现要求 |
|---|---|
| COM_QUERY / INIT_DB / PING | SQL逐条解析、目标schema授权、错误码和状态标记透传 |
| PREPARE / EXECUTE / CLOSE / RESET | stmt ID两端映射；prepare保存AST摘要，execute重新鉴权；参数按类型解析/脱敏，不拼SQL字符串 |
| SEND_LONG_DATA | 每stmt有界spool，默认64MiB；关闭/RESET清理；执行时仍检查策略 |
| 结果集与多结果 | 按协商能力处理EOF/OK及MORE_RESULTS，保留事务标志；支持逐包输出 |
| 多语句 | 首版不声明CLIENT_MULTI_STATEMENTS；工单由服务端解析拆分并按事务能力执行；以后逐语句审计再开放 |
| LOCAL INFILE / replication /压缩 | 首版不声明相应能力；收到未协商命令拒绝，禁止文件读取绕过 |
| cancel /断线 | 管理取消须关联目标connection ID且校验owner；网络断线无法确认提交时记unknown，不自动重试 |

SQL分类使用版本固定的 Vitess sqlparser，并以目标MySQL8语法建立fixture；解析不支持时在受限策略下拒绝，不能字符串前缀放行。[解析器源代码](https://github.com/vitessio/vitess/tree/main/go/vt/sqlparser)

db-proxy-block-dml 是“限制写入”的兼容开关，不足以仅拦 INSERT/UPDATE/DELETE。受限profile使用**目标库真正的只读账户**，最小SELECT/必要元数据权限，无FILE/EXECUTE/DDL/管理权限；代理AST另拒绝多语句、写CTE、INTO OUTFILE/DUMPFILE、CALL和非白名单函数。未知UDF/存储函数不能因外层SELECT而放行。事务只读设置作为补充，不能替代数据库权限。若资产仅有全权限目标凭据且无法提供只读映射，严格只读profile拒绝启用。

审批写入使用独立凭据和受控执行器，按固化SQL hash、目标、审批版本和当前权限执行。单事务支持的语句用同连接事务；DDL隐式提交/非事务引擎按语句记录partial/unknown。唯一executionId只阻止本系统重复派发，不宣称网络故障下恰好一次。

## PostgreSQL 扩展的明确实现路径

使用 pgx 的 pgproto3 编解码前后端协议，SCRAM使用 xdg-go/scram；平台专用数据库密码增加 `scram-sha-256-v1` verifier（salt、iterations、StoredKey、ServerKey），仅用于PG代理，不使用MySQL的PHC直接参与SCRAM挑战。初始iterations=32768并在部署压测确认。个人密码创建/轮换时在内存一次计算两种verifier、加密保存，之后不能从Argon2id逆推出SCRAM。旧记录启用PG时要求重置专用密码。

TLS强制，SCRAM-SHA-256首版不宣称PLUS；明确拒绝客户强制channel-binding的profile，后续实现TLS channel binding才开放。处理SSLRequest/Startup/Parse/Bind/Describe/Execute/Flush/Sync、portal状态、错误后的ignore-until-Sync和COPY状态机；CancelRequest使用本代理随机映射token，不能把任意目标pid/key交给用户。协议基准见 [PostgreSQL18 Message Flow](https://www.postgresql.org/docs/18/protocol-flow.html)，工具见 [pgproto3](https://github.com/jackc/pgx/tree/master/pgproto3)、[SCRAM](https://github.com/xdg-go/scram)。

PG只读同样依赖受限数据库角色与显式数据库权限，不能只依赖SQL parser。COPY FROM、写函数和不支持的pipeline组合在对应profile未通过前明确拒绝；扩展完整范围保持在M10任务卡，不将部分协议标作全兼容。

## 交付和验收

D07.1（W4，M01/M02/M04前置）：MySQL TLS/full-auth、专用密码、上下游连接、text/prepare审计和真实只读权限；D07.2（W4，D07.1/M11前置）：SQL工单、取消/unknown、协议故障；D07.3（后续独立扩展）：PG SCRAM、extended/COPY/pipeline和取消。每项需提交握手/命令状态机fixture、版本锁定、迁移和客户端矩阵。

MySQL至少CLI、Connector/J、DBeaver，目标MySQL8.4；分别覆盖TLS证书错误、不支持插件、密码吊销、prepare类型/大数据、事务、元数据、多结果、错误后下一条命令、只读绕过、断线提交unknown。PG至少psql、JDBC、DBeaver及PG18目标，覆盖Bind参数、Sync恢复、COPY取消、pipeline报错、租约终止。协议库单测通过不能替代这些跨端测试。

## R12 实际实现与客户端配置（2026-10-30）

实现使用 go-mysql v1.16.0、Vitess v0.24.5，迁移版本 035。实际交付与验收状态见 [R12 验收记录](../acceptance/R12.md)；本节后面的 PostgreSQL 规范仍是后续扩展，不代表已实现。

代理通过 `NT_DATABASE_PROXY_ADDR` 和 `NT_DATABASE_PROXY_NAME` 配置监听地址与证书主机名；当前 compose 发布 13306，测试名称为 backend.localhost。目标连接始终验证其独立 CA 和 TLS 服务器名称。代理证书从门户下载，测试签发证书有效期 365 天；尚未宣称自动续期。生产部署应提供可解析且与证书匹配的代理域名。

DBeaver 配置必须同时保存主机、端口、数据库以及 JDBC URL：仅预填 URL 后切换到按主机编辑，可能被客户端默认的 localhost:3306 覆盖。测试环境对应 backend.localhost / 13306 / inventory。用户名是平台用户名@资产连接别名，密码使用个人数据库专用密码，不能使用目标账户密码。

TLS 可在 SSL 页选择下载的代理 CA；采用 JDBC 信任库配置时设置 sslMode=VERIFY_IDENTITY、trustCertificateKeyStoreUrl、trustCertificateKeyStoreType=PKCS12 以及本地信任库口令。不要同时配置相互覆盖的空 SSL 信任库，也不要通过关闭验证解决证书错误。测试配置中的库仅包含代理公开证书。

原生协议已覆盖 DBeaver 的引擎/插件/字符集/排序规则/表状态等只读元数据查询；JDBC setMaxRows 使用的 SESSION sql_select_limit 只允许非负整数与 DEFAULT，拒绝 GLOBAL、表达式、变量引用和借此更改只读属性。失败 PREPARE 后对未知句柄的 COM_STMT_CLOSE 不返回数据、不转发，也不终止连接，保持 JDBC 回退到普通查询的协议行为。

## R16 当前实际状态（2026-11-11）

schema60 新增 PostgreSQL 资产类型、强制 TLS/SCRAM 的目标诊断和个人密码双验证材料。新密码使用 Vault 加密的版本2 bundle（Argon2id + SCRAM salt/iterations/StoredKey/ServerKey）；旧版仅保留 MySQL 支持，用户重置后才能获得 SCRAM 材料。真实 PG18 验证了材料兼容性。目标诊断后已追加 PG 原生只读预览：TLS/SCRAM、查询/绑定、portal、COPY TO STDOUT、pipeline/Sync恢复、取消映射、撤销租约与SQL审计，psql与pgx已通过隔离验收。`database.postgres` 为 available / readonly-preview；JDBC42.7.14元数据/事务/分页/取消矩阵已通过；DBeaver实际桌面、PG工单及其他限制仍未完成。详细证据及剩余项见 [R16 验收记录](../acceptance/R16.md)，资产契约见 [database-assets.openapi.yaml](../../../backend/api/database-assets.openapi.yaml)。

工单只对 InnoDB 基表使用事务执行，COMMIT 前复查用户、授权、资产和凭据版本。无法确认提交、回滚有警告或 Worker 租约失效时记录 unknown。后台任务不自动重试不确定的 SQL。结果行数/字节预算、长参数 spool、审计绑定值保护和客户端矩阵见验收记录。
