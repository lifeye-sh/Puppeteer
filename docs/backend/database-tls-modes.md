# r17-rc10 数据库目标 TLS 模式

MySQL 和 PostgreSQL 资产均支持在“新建/编辑数据库 → 凭据与 TLS → 目标数据库传输模式”中选择：

- TLS 证书校验（默认）：要求 CA 和服务器名称，TLS 1.2 或更高，验证身份；失败不自动降级。
- 无 TLS 兼容模式：管理员明确选择后，平台到目标数据库不使用 TLS，证书字段不必填写；仅用于可信内网或受保护链路。

API 字段为 `tlsMode`，接受 `verify-full`、`disable`；空字符串保持历史默认校验行为。不支持 prefer/allow 自动回退。106 数据库迁移为所有现有资产保留默认模式。变更保存记录 database.transport_saved 审计，修改后应断开旧客户端连接并重连。

覆盖数据库资产的 MySQL/PostgreSQL 连接诊断与原生代理目标连接，以及 MySQL SQL 工单和原生受控 DML。共享连接器继续检查目标权限；PostgreSQL 保持 SCRAM 和只读要求，本功能不开放 PostgreSQL 写操作/SQL 工单，也不新增其 MD5 或明文密码认证兼容。

客户端（DBeaver/mysql/psql）到平台代理仍须 TLS，不要在客户端关闭 SSL。关闭 TLS 仅作用于平台到指定目标这一段；SQL 与结果在该段未加密，认证的保护取决于目标认证协议。

## 验证记录

- 独立 MySQL 8.4.7（tls_version 为空）、PostgreSQL 18（ssl=off）容器，没有宿主端口映射。
- 两者真实查询成功，分别检查 Ssl_cipher 为空、pg_stat_ssl.ssl=false。
- MySQL 只读拒写、拒绝高权限账号充当只读账号、独立 DML 写入及回滚通过。
- PostgreSQL 只读拒写、SCRAM 要求保留、无 TLS 回退列表为空。
- 两种资产 API 保存、按别名读取、连接诊断通过；默认模式缺少 CA 拒绝，无效模式拒绝。
- 原有连接器单元测试和前端生产构建通过。真实外部客户数据库需部署后使用资产连接诊断确认网络与账号权限。
