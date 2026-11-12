# 官方使用文档覆盖矩阵

这是阅读与规划覆盖，不是功能实现状态。正文和图示已核对，嵌入视频缺口见[核对说明](usage-docs-review.md)。来源元数据见[JSON](usage-docs-sources.json)，每个页面至少对应一项有主责模块的开发需求。

| 页面 | 范围 | 图示引用 | 对应需求及主责模块 |
|---|---|---|---|
| [浏览器访问工作区](https://www.next-terminal.com/zh/docs/usage/access.html) | 使用文档 | 0 | U02 / M03、U12 / M06 |
| [安全网关配置文件](https://www.next-terminal.com/zh/docs/usage/agent-gateway-config.html) | 使用文档 | 0 | U07 / M05、U08 / M05、U33 / M15 |
| [安全网关](https://www.next-terminal.com/zh/docs/usage/agent-gateway.html) | 使用文档 | 2 | U07 / M05、U09 / M05 |
| [资产管理总览](https://www.next-terminal.com/zh/docs/usage/asset.html) | 使用文档 | 2 | U02 / M03、U03 / M03、U04 / M03、U09 / M05 |
| [日志审计](https://www.next-terminal.com/zh/docs/usage/audit.html) | 使用文档 | 0 | U15 / M08、U16 / M08 |
| [资源授权与访问策略](https://www.next-terminal.com/zh/docs/usage/authorization.html) | 使用文档 | 0 | U05 / M02、U32 / M18 |
| [系统备份与恢复](https://www.next-terminal.com/zh/docs/usage/backup.html) | 使用文档 | 0 | U28 / M16、U29 / M16 |
| [命令行工具参考](https://www.next-terminal.com/zh/docs/usage/cli.html) | 使用文档 | 0 | U24 / M01、U25 / M01、U27 / M13、U30 / M18、U32 / M18 |
| [授权凭证](https://www.next-terminal.com/zh/docs/usage/credential.html) | 使用文档 | 1 | U06 / M04 |
| [数据库审计](https://www.next-terminal.com/zh/docs/usage/database.html) | 使用文档 | 8 | U21 / M01、U22 / M10、U23 / M11、U36 / M14 |
| [RDP/VNC 错误码说明](https://www.next-terminal.com/zh/docs/usage/error-codes.html) | 使用文档 | 0 | U33 / M15 |
| [文件管理](https://www.next-terminal.com/zh/docs/usage/file-management.html) | 使用文档 | 1 | U05 / M02、U14 / M07、U32 / M18 |
| [绑定授权许可](https://www.next-terminal.com/zh/docs/usage/license.html) | 使用文档 | 2 | U32 / M18 |
| [mTLS 双向认证](https://www.next-terminal.com/zh/docs/usage/mtls.html) | 使用文档 | 10 | U27 / M13 |
| [OIDC 单点登录完整配置 — 用 Next Terminal 作 IdP 对接 Proxmox VE](https://www.next-terminal.com/zh/docs/usage/oidc_server.html) | 使用文档 | 3 | U26 / M01 |
| [OTP 一次性密码](https://www.next-terminal.com/zh/docs/usage/otp.html) | 使用文档 | 0 | U24 / M01 |
| [通行密钥](https://www.next-terminal.com/zh/docs/usage/passkey.html) | 使用文档 | 2 | U25 / M01 |
| [RDP 代理服务器](https://www.next-terminal.com/zh/docs/usage/rdp-server.html) | 使用文档 | 0 | U11 / M06、U12 / M06、U13 / M06 |
| [Windows / RDP 资产](https://www.next-terminal.com/zh/docs/usage/rdp.html) | 使用文档 | 5 | U03 / M03、U12 / M06、U14 / M07 |
| [快速开始](https://www.next-terminal.com/zh/docs/usage/readme.html) | 使用文档 | 3 | U01 / M18、U33 / M15 |
| [SSH 网关](https://www.next-terminal.com/zh/docs/usage/ssh-gateway.html) | 使用文档 | 1 | U09 / M05 |
| [SSH 代理服务器](https://www.next-terminal.com/zh/docs/usage/ssh-server.html) | 使用文档 | 6 | U04 / M03、U10 / M06 |
| [SSH 资产](https://www.next-terminal.com/zh/docs/usage/ssh.html) | 使用文档 | 3 | U03 / M03、U14 / M07、U33 / M15、U34 / M12、U35 / M17 |
| [系统属性配置参考](https://www.next-terminal.com/zh/docs/usage/system-properties.html) | 使用文档 | 0 | U10 / M06、U16 / M08、U20 / M18、U22 / M10、U25 / M01、U26 / M01、U31 / M18、U35 / M17、U36 / M14 |
| [使用 Termark 接入 NextTerminal 资产](https://www.next-terminal.com/zh/docs/usage/termark.html) | 使用文档 | 3 | U13 / M06 |
| [VNC 与 Telnet 资产](https://www.next-terminal.com/zh/docs/usage/vnc-telnet.html) | 使用文档 | 0 | U03 / M03、U12 / M06 |
| [Web 资产](https://www.next-terminal.com/zh/docs/usage/website.html) | 使用文档 | 6 | U17 / M09、U18 / M09、U19 / M09、U20 / M18、U27 / M13 |
| [RDP 连不上/黑屏/凭证失效？Windows 远程桌面 5 步排查](https://www.next-terminal.com/zh/docs/blog/rdp-black-screen-failed.html) | 关联资料 | 0 | U33 / M15 |
| [SSH 里的“认证私钥”到底是什么，它认证的是谁？](https://www.next-terminal.com/zh/docs/blog/ssh-authentication.html) | 关联资料 | 0 | U10 / M06 |
| [从 PostgreSQL 16 迁移到 PostgreSQL 18](https://www.next-terminal.com/zh/docs/install/postgresql-16-to-18.html) | 关联资料 | 0 | U29 / M16 |
| [获取真实客户端 IP](https://www.next-terminal.com/zh/docs/install/real-ip.html) | 关联资料 | 0 | U18 / M09、U20 / M18 |

## 需求分配

| 需求 | 主责 | 协作 | 阶段 | 验收要点 |
|---|---|---|---|---|
| U01 首次使用闭环 | [M18 系统设置与产品能力](modules/M18-system.md) | M01、M02、M08、M15 | W1 | 并发初始化只有一个成功；普通用户看不到未授权资源；一次连接能关联访问、会话和操作记录。 |
| U02 按接入方式筛选资源目录 | [M03 资源目录与主机资产](modules/M03-resource.md) | M02、M06 | W2 | 数据库资产不会被当成浏览器 SQL 会话；多标签独立建立或离开会话，不误结束其他标签连接。 |
| U03 资产类型化配置 | [M03 资源目录与主机资产](modules/M03-resource.md) | M05、M06 | W1/W3 | RDP/VNC 拒绝私钥账号模式；Telnet 使用交互登录且不创建 SFTP 能力；导入模板与单条保存使用同一校验器。 |
| U04 SSH 名称与别名解析 | [M03 资源目录与主机资产](modules/M03-resource.md) | M06 | W3 | 本方案采用别名优先、名称兼容回退；只解析已授权 SSH 资产；重名或别名与名称碰撞时拒绝歧义，不静默选中另一主机。 |
| U05 动态分组授权与逐动作控制 | [M02 权限与访问策略](modules/M02-authorization.md) | M01、M03、M06、M07 | W1/W3 | UI 隐藏按钮后直接请求 API 仍被拒绝；授权到期和撤销影响新接入及可控制的现存操作；文件 chmod 单列动作，不由编辑权限暗中推出。 |
| U06 凭证引用完整生命周期 | [M04 凭据与密钥](modules/M04-vault.md) | M01、M03、M05 | W1 | 资产与 SSH 网关任何一方仍有引用时禁止删除；复制公钥不返回私钥；生成密钥不会被描述为已安装到目标 authorized_keys。 |
| U07 安全网关注册与生命周期 | [M05 网关与网络连接](modules/M05-connectivity.md) | M04、M15 | W3 | 仅开放 Web 入口即可连接内网目标；吊销后无法新建流；旧 nt-tunnel 二进制互通需专项验证，自有协议不得标成官方兼容。 |
| U08 Agent 配置和升级契约 | [M05 网关与网络连接](modules/M05-connectivity.md) | M15、M18 | W3 | 保留文档默认值及优先级清单；校验代理 host/port；采集 5 秒、心跳 30 秒；白名单覆盖黑名单，挂载点精确匹配；升级包校验、失败回滚和暂停升级属于本方案增强。 |
| U09 入口 SSH 与出站 SSH 跳板分离 | [M05 网关与网络连接](modules/M05-connectivity.md) | M04、M06 | W2 | 跳板禁用 AllowTcpForwarding 时返回具体失败阶段；M05 不拥有平台 SSH 登录会话，M06 不复制网关配置。 |
| U10 原生 SSH 入口与受控转发 | [M06 远程会话](modules/M06-session.md) | M01、M02、M03、M04、M05、M08 | W3 | 关闭转发仍能登录；绕过白名单的 forwarding 请求拒绝且审计；主机密钥持久化和轮换有指纹变更记录；普通 SSH 隧道不得冒充 SQL 协议审计。 |
| U11 原生 RDP 一次性票据入口 | [M06 远程会话](modules/M06-session.md) | M01、M02、M03、M05、M08、M13 | W3-基础链路/W4-能力验收 | 并发消费只有一次成功；过期/已用/撤权/非 RDP 资产拒绝；失败重连重新签发；文件不含目标 Windows 密码；创建并结束审计会话。 |
| U12 按入口发布会话能力矩阵 | [M06 远程会话](modules/M06-session.md) | M02、M07、M08 | W3/W4 | 官方当前不支持的监控/主动断开不得冒充兼容；本项目终止扩展须D03.2实测后才返回成功；原生磁盘重定向不等同浏览器存储；无法执行必需策略时拒绝该入口。 |
| U13 Termark 授权与两类传输 | [M06 远程会话](modules/M06-session.md) | M01、M02、M03、M05、M13 | W3-基础链路/W4-能力验收 | 仅开放 HTTPS 的网络中完成 SSH 与 RDP 接入；用户撤权立即影响同步和新接入；设备授权/帧格式未公开，保留独立真实客户端兼容验收，自有路径按D01先交付，客户端本地偏好不写入服务端业务表。 |
| U14 两类文件系统与上传状态 | [M07 文件与共享存储](modules/M07-files.md) | M02、M06、M08、M18 | W2/W3 | 99% 浏览器上传不算成功；新驱动配置需新会话；并发上传和映射盘写入均执行字节配额；编辑/目录上传/批量下载单列能力门控。 |
| U15 资产访问日志与调查关联 | [M08 审计与录像](modules/M08-audit.md) | M06、M07、M09、M10、M11、M15 | W2/W4 | 按用户/资源/时间/结果定位并关联 trace/session/workOrder；Windows 内部复制不能伪造为浏览器文件日志；没有录像显示原因而非空成功回放。 |
| U16 分类保留与录屏配置 | [M08 审计与录像](modules/M08-audit.md) | M18、M12 | W3 | 对象删除与索引清理可重入；受保留锁保护的证据不删除；网站禁用访问日志明确降级范围，不连带关闭登录和管理操作审计。 |
| U17 Web 域名路由与登录回跳 | [M09 网站访问代理](modules/M09-webproxy.md) | M01、M02、M05、M13、M18 | W3 | 相同域名不同路径也不允许注册第二站点；未授权不能到达上游；回跳仅允许已登记站点；证书 SAN 覆盖域名，不只检查 CN。 |
| U18 公开访问与临时 IP 白名单 | [M09 网站访问代理](modules/M09-webproxy.md) | M02、M08 | W3 | 关闭 autoRenew 时不续期；开启时仅成功访问续期并受本方案绝对期限约束；过期/撤销即失效；NAT 同出口共享效应可见。 |
| U19 请求头与响应改写规则 | [M09 网站访问代理](modules/M09-webproxy.md) | M08、M18 | W3 | 压缩内容正确解压重算长度，限制可改 MIME/大小；WebSocket、流式和二进制内容不误改；不会把平台认证头泄漏给目标网站。 |
| U20 两套真实 IP 配置与信任边界 | [M18 系统设置与产品能力](modules/M18-system.md) | M01、M02、M09、M15 | W1/W3 | 直连伪造 XFF 不生效；多跳由右向左剥离可信代理；SelfProxy 联动测试；默认自动信任私网的官方行为与本方案显式信任差异必须公开。 |
| U21 数据库专用密码 | [M01 身份与组织](modules/M01-identity.md) | M04、M10 | W4 | A 用户凭据不能给 B 用户认证；撤销后新连接拒绝；MySQL challenge-response 使用专用 verifier，不能拿随机 token 的普通摘要直接实现协议认证。 |
| U22 MySQL 代理兼容基线 | [M10 数据库访问代理](modules/M10-database.md) | M01、M02、M03、M05、M08 | W4 | 真实 MySQL 客户端可握手、选择数据库、查询元数据和使用 prepared statement；变更受阻并产生审计；PG 是独立扩展适配器，不称官方使用文档已确认。 |
| U23 SQL 审批后自动执行 | [M11 申请与审批](modules/M11-approval.md) | M10、M08、M14 | W4 | 审批事务与执行意图原子提交，重复审批不重复执行；SQL 变动重审；超时结果不明进入 unknown，DDL 隐式提交和部分成功单独记录。 |
| U24 TOTP 与恢复码 | [M01 身份与组织](modules/M01-identity.md) | M04、M08 | W1 | 并发 TOTP/恢复码只有一次消费成功；恢复码独立哈希且仅签发时展示；CLI 清理 MFA 留下操作者、原因和会话撤销审计；前端恢复码流程缺口单列联调任务。 |
| U25 Passkey 域与来源配置 | [M01 身份与组织](modules/M01-identity.md) | M18 | W3 | 跨 Origin/RP 的挑战拒绝，挑战一次性；清理用户全部 Passkey 后旧凭据不可用，不把浏览器删除成功当服务端已撤销。 |
| U26 OIDC 身份提供方完整闭环 | [M01 身份与组织](modules/M01-identity.md) | M02、M04、M08、M18 | W4 | issuer 固定且 discovery 端点自洽；redirect_uri 精确匹配；禁用客户端和撤销同意后刷新拒绝；正文 oauth2 路径与前端 oidc/server 差异须按兼容台账处理。 |
| U27 mTLS 用户证书与默认继承 | [M13 证书与身份凭据签发](modules/M13-pki.md) | M01、M04、M09、M16 | W3 | 无证书、过期、吊销及其他 CA 证书不能通过；变更默认项展示受影响站点；证书通过不等于获得资源授权；CA 备份/恢复和轮换联动。 |
| U28 明确备份边界与格式兼容 | [M16 备份与恢复](modules/M16-backup.md) | M04、M07、M08、M13 | W3 | 备份页明确包含/排除内容；data/recordings 与 data/drive 独立恢复验证；格式未公开时不得宣称能导入官方 .ntbak，也不得把 ZIP 改扩展名冒充兼容。 |
| U29 恢复编排与 PostgreSQL 升级 | [M16 备份与恢复](modules/M16-backup.md) | M12、M14、M18 | W3 | 验证 pg_dump/pg_restore 版本矩阵；禁止直接复用不同主版本数据目录；保留旧库和回滚方案；恢复不重放 SQL/轮换任务；异地复制失败与本地备份成功分开呈现。 |
| U30 运维 CLI 作为统一应用入口 | [M18 系统设置与产品能力](modules/M18-system.md) | M01、M02、M13、M15、M08 | W1/W3 | CLI 复用模块用例与配置校验；应急离线模式限制 OS 身份并留审计，不能由 CLI 直接随意改表；密码使用交互或安全输入通道。 |
| U31 系统属性兼容注册表 | [M18 系统设置与产品能力](modules/M18-system.md) | M01、M02、M06、M07、M08、M10、M14、M17 | W0/W4 | 未知键拒绝写入；秘密默认掩码；UI/API/CLI 同规则；官方默认值未知须注明；本项目默认值与单位依据显式登记；废弃分组树键由资源模块迁移，避免两份权威数据。 |
| U32 许可生命周期与能力分层 | [M18 系统设置与产品能力](modules/M18-system.md) | M02、M07 | W0/W3 | 实际实现能力、产品许可和用户授权取交集；外部许可校验协议未知则显示未接入；不得伪造官方许可证，也不把官方授权门户订单业务纳入本后端。 |
| U33 协议故障与指标来源 | [M15 监控与诊断](modules/M15-operations.md) | M05、M06、M08 | W2/W3 | 514/522/776 等超时能定位不同层；769 不误判为平台已退出登录；命令不可用时指标 unknown；排障不会自动修改目标安全策略。 |
| U34 命令片段插入与任务执行分离 | [M12 任务调度与脚本](modules/M12-automation.md) | M06、M02 | W3 | 使用片段不自动附加换行执行；系统片段与个人片段范围明确；命令过滤和审计仍在真实执行入口生效。 |
| U35 AI 双重开关与审计助手 | [M17 AI 运维助手](modules/M17-ai.md) | M03、M06、M08、M18 | W4 | 系统或资产关闭时不能通过直接 API 执行工具；审计助手只读取已授权日志；模型密钥不进入配置列表和会话审计。 |
| U36 执行反馈与邮件配置 | [M14 通知与消息投递](modules/M14-notification.md) | M11、M18 | W3/W4 | 发送失败可独立重试，不重做 SQL；不把数据库密码/完整秘密放进消息；配置测试与生产投递记录可区分。 |

由 `node scripts/backend-module-docs.mjs` 生成；修改来源 JSON 或需求 JSON 后重新生成。
