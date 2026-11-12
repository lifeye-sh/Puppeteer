# 前端逐轮迁移执行卡

由 migration-rounds.json 生成。配套[迁移主计划](frontend-migration-plan.md)和[单轮验收记录模板](migration-acceptance-template.md)。当前后端自测完成 12 轮，用户验收完成 4 轮；未执行用例不能标通过。

| 轮次 | 交付目标 | 模块批次 | 用户验收状态 |
|---|---|---|---|
| [R00](#r00) | 建立新旧对照验证环境 | W0 | frontend_harness_ready / 未验收 |
| [R01](#r01) | 新后端初始化、登录与可用首页 | W0/W1 | accepted / 已验收 |
| [R02](#r02) | 身份、组织、权限与MFA | W1 | accepted / 已验收 |
| [R03](#r03) | 资产、凭据与授权目录 | W1 | accepted / 已验收 |
| [R04](#r04) | SSH远程会话与在线审计 | W2 | accepted / 已验收 |
| [R05](#r05) | 文件操作与终端回放 | W2 | ready_for_user / 未验收 |
| [R06](#r06) | 内网Agent、跳板与网络诊断 | W3 | ready_for_user / 未验收 |
| [R07](#r07) | 浏览器图形、Telnet、共享与存储 | W3 | in_progress / 未验收 |
| [R08](#r08) | 高级访问策略、申请审批与通知 | W3 | planned / 未验收 |
| [R09](#r09) | 网站代理、证书与mTLS | W3 | planned / 未验收 |
| [R10](#r10) | 任务调度、片段与凭据轮换 | W3 | implemented-awaiting-user-acceptance / 未验收 |
| [R11](#r11) | 备份、隔离恢复与运维CLI | W3 | planned / 未验收 |
| [R12](#r12) | MySQL代理与SQL工单 | W4 | 开发完成：内网验收通过，DBeaver查询已确认，待用户整轮验收 / 未验收 |
| [R13](#r13) | 联合身份、Passkey与账户能力补全 | W4 | 开发完成：全量后端回归与内网浏览器验收通过；Passkey 实体设备按用户要求跳过，待用户整轮验收 / 未验收 |
| [R14](#r14) | 原生SSH/RDP与自有客户端 | W3/W4 | in_progress / PKCE、目录、HTTPS SSH桥接、个人公钥TCP网关、门户配置/Termark链接、TCP及HTTPS受控转发已实现；原生RDP、客户端实机矩阵、系统密钥库/设备管理及完整客户端未完成 / 未验收 |
| [R15](#r15) | AI对话、工具审批与审计 | W4 | implemented_verified / R15 全部规划功能已完成：三协议模型、队列与 WS 恢复、分支/摘要、五种工具、参数审批/取消、预算报表及个人偏好；全量 Go 回归、前端构建、内网模型页面与真实 SSH/SFTP 效果验收通过，已部署 r15-complete/schema59。待用户签收；R14 未完成项按用户要求暂缓 / 未验收 |
| [R16](#r16) | PG扩展、配置与运营完整收口 | W4 | implemented_verified / R16 已完成开发与部署（schema102）；后端351项、前端60项及Windows JDBC矩阵通过；DBeaver相关验收按用户2026-11-12明确指示免测关闭，历史结果不改写。R14暂缓项与R17切换独立；整轮用户签收未代填 / 未验收 |
| [R17](#r17) | 全新生产部署、试运行与上线 | 发布 | implemented_verified / 按用户认可的Docker模拟生产环境完成：全新部署、HTTPS浏览器/审计、持续会话容量、远程WAL/PITR与恢复回退、Agent双节点排空推广及根轮换均通过；后端短回归348项、前端67项通过，专项证据独立记录。实际生产开放与用户签收未代填 / 未验收 |

<a id="r00"></a>

## R00 建立新旧对照验证环境

前置：无。模块：F00、M18。使用需求：验证环境，不独占业务需求。

**后端交付**

- 约定旧8088/新8089、独立数据库/对象/密钥/测试资产；新后端本轮未实现

**前端交付**

- 已提供dev:legacy/dev:backend、独立域名、环境角标、固定端口
- 已提供真实Vite HTTP/WS代理测试；R01再增加runtime身份与能力发现

**验证入口**：`/login`（均在新环境域名内，R00旧站点对照除外）。

**用户操作**

1. 启动npm run dev:legacy并在legacy.localhost:5173查看旧站点
2. 启动npm run dev:backend并在backend.localhost:5174查看新环境角标
3. 访问/__migration/profile核对配置；新服务未启动时应连接失败，不能显示模拟成功

**反向用例**：使用localhost而非指定域名访问返回421；设置共享DEV_API_TARGET不能改写两个专用mode。

**通过门槛**：代理测试通过、旧环境可对照；本轮完成不等于新后端可登录。

**当前状态**：frontend_harness_ready；后端自测 未通过/未执行；用户验收 待确认。

每轮同时执行主计划中的登录/权限/前轮业务回归。原生客户端、外部IdP及灾备需相应真实环境；未执行记not_run。

**源码入口**

- [src/api/core/requests.ts](../../src/api/core/requests.ts)

<a id="r01"></a>

## R01 新后端初始化、登录与可用首页

前置：R00。模块：F00、M01、M08、M18。使用需求：U01、U20、U30、U31、U32。

**后端交付**

- 真实迁移/PG/初始化、Cookie登录退出、branding/logo/license兼容、runtime/capabilities
- 最小配置注册与审计；setup并发保护、明确开发Origin与Cookie

**前端交付**

- 新增轻量联调首页和能力Provider；新后端按能力进入可用首页
- 隐藏未完菜单/拦深链接并停止后台轮询；保持旧mode行为；未实现501不退出登录

**验证入口**：`/setup`、`/login`、`/info`（均在新环境域名内，R00旧站点对照除外）。

**用户操作**

1. 本机已用真实前端初始化；从 backend/.local/acceptance-account.json 读取验收账号后登录，空库流程见验收记录
2. 检查联调首页实例ID/版本和本轮功能
3. 刷新、退出、再次登录，确认信息持久化
4. 尝试未开放功能，看到明确提示且不会转到旧站点

**反向用例**：重复初始化被拒绝；坏密码保持登录页；未授权API401；停新后端不能自动回旧后端。

**通过门槛**：新环境从空库通过真实前端完成初始化→登录→首页→退出，无mock和启动依赖漏项。

**当前状态**：accepted；后端自测 已通过；用户验收 已通过。

每轮同时执行主计划中的登录/权限/前轮业务回归。原生客户端、外部IdP及灾备需相应真实环境；未执行记not_run。

**源码入口**

- [src/api/account-api.ts](../../src/api/account-api.ts)
- [src/api/branding-api.ts](../../src/api/branding-api.ts)
- [src/api/license-api.ts](../../src/api/license-api.ts)
- [src/api/user-api.ts](../../src/api/user-api.ts)
- [src/hook/LicenseContext.tsx](../../src/hook/LicenseContext.tsx)
- [src/layout/RedirectPage.tsx](../../src/layout/RedirectPage.tsx)
- [backend/README.md](../../backend/README.md)
- [backend/api/openapi.yaml](../../backend/api/openapi.yaml)
- [backend/internal/application/app_test.go](../../backend/internal/application/app_test.go)
- [src/hook/BackendRuntimeContext.tsx](../../src/hook/BackendRuntimeContext.tsx)
- [src/pages/backend/BackendHomePage.tsx](../../src/pages/backend/BackendHomePage.tsx)
- [docs/backend/acceptance/R01.md](../../docs/backend/acceptance/R01.md)

<a id="r02"></a>

## R02 身份、组织、权限与MFA

前置：R01。模块：M01、M02、M08、M18。使用需求：U05、U24。

**后端交付**

- 用户/部门/角色/权限；验证码、登录锁定、TOTP恢复码、会话撤销；本轮六项角色查看权限，身份修改为系统管理员专属；资源授权随 R03 开发
- 四类验收账号和两个部门fixture；操作及登录审计

**前端交付**

- 用户/角色/部门表单与权限字段对齐
- 增加恢复码操作；登录和错误状态适配；权限变更清缓存

**验证入口**：`/user`、`/department`、`/role`、`/login-locked`、`/info`（均在新环境域名内，R00旧站点对照除外）。

**用户操作**

1. 使用已有管理员登录；operator/reviewer/denied 和两个部门已通过本地 fixture CLI 创建，密码在 backend/.local/r02-fixtures.json
2. 在用户、部门、角色页面创建/编辑并刷新验证持久化；角色授予查看权限，写操作为系统管理员专属
3. 用 reviewer/denied 分别登录观察菜单与直接 URL 权限差异；修改角色后验证权限刷新
4. operator 下次登录先绑定自己的 TOTP 并保存十个恢复码；使用恢复码必须重新绑定后才可进入业务页
5. 管理员撤销会话或禁用账号，验证旧会话失效；测试密码五次失败锁定和管理员解锁；首页查看审计

**反向用例**：普通用户手改URL访问管理页被拒绝；同一恢复码并发只成功一次。

**通过门槛**：菜单、路由、API三层权限一致，服务端鉴权不可被前端按钮隐藏替代。

**当前状态**：accepted；后端自测 已通过；用户验收 已通过。

每轮同时执行主计划中的登录/权限/前轮业务回归。原生客户端、外部IdP及灾备需相应真实环境；未执行记not_run。

**源码入口**

- [src/api/user-api.ts](../../src/api/user-api.ts)
- [src/api/department-api.ts](../../src/api/department-api.ts)
- [src/api/role-api.ts](../../src/api/role-api.ts)
- [src/api/login-locked-api.ts](../../src/api/login-locked-api.ts)
- [backend/internal/application/r02.go](../../backend/internal/application/r02.go)
- [backend/internal/application/r02_test.go](../../backend/internal/application/r02_test.go)
- [src/pages/backend/BackendIdentityPage.tsx](../../src/pages/backend/BackendIdentityPage.tsx)
- [src/pages/backend/BackendSecurityPanel.tsx](../../src/pages/backend/BackendSecurityPanel.tsx)
- [docs/backend/acceptance/R02.md](../../docs/backend/acceptance/R02.md)

<a id="r03"></a>

## R03 资产、凭据与授权目录

前置：R02。模块：M03、M04、M02、M08。使用需求：U01、U02、U03、U04、U05、U06。

**后端交付**

- 资源分组/标签、SSH资产、凭据加密引用、导入导出、授权与普通用户门户
- 只做明确的连通测试，远程会话入口到R04再开放

**前端交付**

- 字段/分页/过滤/表单错误对齐，连接按钮依能力禁用
- 列表及详情掩码、删除引用冲突展示；导入错误逐行展示

**验证入口**：`/asset`、`/credential`、`/authorised-asset`、`/x-asset`（均在新环境域名内，R00旧站点对照除外）。

**用户操作**

1. 已有 R03 SSH A/B、共享凭据和分组；示例地址是 TEST-NET，真实连通测试需替换成自有主机
2. operator 已仅授权 A；独立浏览器登录后 /x-asset 只见 A，搜索 B 为空
3. 修改用户/部门到资产/分组授权，移动子组、禁用资产或撤销后确认可见范围变化
4. 新建/更新凭据，检查掩码、版本、公钥和引用删除保护；秘密查看需管理员密码与已绑定 TOTP
5. 下载脱敏 JSON、修改名称/别名后导入，加入坏端口行验证逐行错误；查看操作审计

**反向用例**：猜B的ID仍403；删除被引用凭据报冲突；接口和导出不泄露秘密。

**通过门槛**：管理员建资产→普通用户看见授权资产闭环通过，关联ID和数据持久化一致。

**当前状态**：accepted；后端自测 已通过；用户验收 已通过。

每轮同时执行主计划中的登录/权限/前轮业务回归。原生客户端、外部IdP及灾备需相应真实环境；未执行记not_run。

**源码入口**

- [src/api/asset-api.ts](../../src/api/asset-api.ts)
- [src/api/credential-api.ts](../../src/api/credential-api.ts)
- [src/api/authorised-asset-api.ts](../../src/api/authorised-asset-api.ts)
- [src/api/portal-api.ts](../../src/api/portal-api.ts)
- [backend/internal/application/r03.go](../../backend/internal/application/r03.go)
- [backend/internal/application/r03_test.go](../../backend/internal/application/r03_test.go)
- [backend/internal/modules/resource/service.go](../../backend/internal/modules/resource/service.go)
- [backend/internal/modules/vault/service.go](../../backend/internal/modules/vault/service.go)
- [src/pages/backend/BackendResourcesPage.tsx](../../src/pages/backend/BackendResourcesPage.tsx)
- [docs/backend/acceptance/R03.md](../../docs/backend/acceptance/R03.md)

<a id="r04"></a>

## R04 SSH远程会话与在线审计

前置：R03。模块：M05、M06、M08、M02。使用需求：U01、U15、U33。

**后端交付**

- SSH 直连、人工核对主机密钥、一次性登录绑定票据、持久化会话/10秒租约、授权复核与服务端只读
- 访问成功/失败审计；显式 SSH exec 命令和退出码；交互输出/resize 录屏写入供 R05 回放，交互按键不冒充精确命令

**前端交付**

- 对齐现有WS帧、resize/心跳/错误；页面显式显示连接中/失败/断开
- 在线终止和会话详情关联，不因WS业务错误清除网站登录

**验证入口**：`/x-asset`、`/access`、`/terminal`、`/online-session`、`/access-log`、`/exec-command-log`（均在新环境域名内，R00旧站点对照除外）。

**用户操作**

1. operator 从门户打开新增 R04 本地 SSH（独立 OpenSSH 容器），输入 whoami/pwd 并调整终端大小
2. 另开在线会话页，查看并终止；访问记录中检查结束原因、录屏事件；通过执行命令入口验证退出码
3. 重新连接后撤销本机 SSH 资源授权，观察租约内关闭；按需重新授权
4. 自有资产先核对并信任主机指纹，再验证连接；未知密钥、密钥变化、坏凭据或不可达时检查失败记录

**反向用例**：只读会话发送伪造输入仍被拦；断网不重放命令；无权sessionId不能附着。

**通过门槛**：真实目标输出、在线断连、撤权和失败审计均在当前前端可验证。

**当前状态**：accepted；后端自测 已通过；用户验收 已通过。

每轮同时执行主计划中的登录/权限/前轮业务回归。原生客户端、外部IdP及灾备需相应真实环境；未执行记not_run。

**源码入口**

- [src/pages/access/AccessTerminal.tsx](../../src/pages/access/AccessTerminal.tsx)
- [src/pages/access/terminal/terminal-runtime.ts](../../src/pages/access/terminal/terminal-runtime.ts)
- [src/api/session-api.ts](../../src/api/session-api.ts)
- [src/api/access-log-api.ts](../../src/api/access-log-api.ts)
- [backend/internal/application/r04.go](../../backend/internal/application/r04.go)
- [backend/internal/application/r04_transport.go](../../backend/internal/application/r04_transport.go)
- [backend/internal/application/r04_test.go](../../backend/internal/application/r04_test.go)
- [src/pages/backend/BackendTerminalPage.tsx](../../src/pages/backend/BackendTerminalPage.tsx)
- [src/pages/backend/BackendSessionsPage.tsx](../../src/pages/backend/BackendSessionsPage.tsx)
- [docs/backend/acceptance/R04.md](../../docs/backend/acceptance/R04.md)

<a id="r05"></a>

## R05 文件操作与终端回放

前置：R04。模块：M07、M08、M06。使用需求：U14、U16。

**后端交付**

- 基于存活 SSH 会话的主目录 SFTP 列表、创建目录、上传、下载、UTF-8 文本编辑、重命名、文件及空目录删除
- 16 MiB 单文件、1 MiB 分片、64 MiB/用户与 256 MiB 全局暂存配额；取消、幂等分片、内容哈希冲突、临时文件追踪回收
- 终端录像封段摘要、完整性校验、回放鉴权和只读保留预览（不自动删除）

**前端交付**

- 终端右侧文件管理；上传显示接收、传输校验和最终结果，可取消与核对结果
- 中文文件名下载、编辑冲突提示、文件审计；已有回放继续可用

**验证入口**：`/terminal`、`/access-log`、`/filesystem-log`（均在新环境域名内，R00旧站点对照除外）。

**用户操作**

1. 在专用测试目录上传含中文文件名文件，下载核对内容
2. 编辑、重命名并删除测试副本，查看文件审计
3. 执行一次SSH会话后关闭，从历史会话回放
4. 尝试超配额文件和取消上传，核对空间回收

**反向用例**：路径穿越/符号链接越界和其他用户下载拒绝；取消重试不产生重复计费。

**通过门槛**：HTTP文件传输、实际目标内容、配额、日志与回放形成闭环。

**当前状态**：ready_for_user；后端自测 已通过；用户验收 待确认。

每轮同时执行主计划中的登录/权限/前轮业务回归。原生客户端、外部IdP及灾备需相应真实环境；未执行记not_run。

**源码入口**

- [src/pages/backend/BackendFiles.tsx](../../src/pages/backend/BackendFiles.tsx)
- [src/pages/backend/BackendFileLogs.tsx](../../src/pages/backend/BackendFileLogs.tsx)
- [src/pages/backend/BackendRecordingPlayer.tsx](../../src/pages/backend/BackendRecordingPlayer.tsx)
- [backend/internal/application/r05.go](../../backend/internal/application/r05.go)
- [backend/internal/modules/files/service.go](../../backend/internal/modules/files/service.go)
- [docs/backend/acceptance/R05.md](../../docs/backend/acceptance/R05.md)

<a id="r06"></a>

## R06 内网Agent、跳板与网络诊断

前置：R05。模块：M05、M15、M06。使用需求：U07、U08、U09、U33。

**后端交付**

- D02 Agent登记/设备Key/WS/yamux/epoch/吊销/升级；SSH跳板、网关组/网络代理
- 实际采集/心跳和诊断、不可达状态、TUF发布与回滚；真实远程目标经过拨号链

**前端交付**

- 安装说明区分自有Agent和官方旧Agent；版本/更新/连接错误可见
- 监控页仅启用已接入指标，网关表单及拓扑引用对齐

**验证入口**：`/ssh-gateway`、`/agent-gateway`、`/gateway-group`、`/network-proxy`、`/tools`、`/monitoring`（均在新环境域名内，R00旧站点对照除外）。

**用户操作**

1. 创建注册令牌并在测试内网机器安装自有Agent
2. 网页看到上线与CPU/磁盘数据，配置一台通过Agent连接的SSH资产
3. 使用当前终端连入该资产；切换跳板/组验证
4. 撤销Agent或断网，再更新测试版本并验证恢复/回滚

**反向用例**：注册令牌重放、旧设备Key、超范围目标和循环跳板拒绝；假在线不能成功拨号。

**通过门槛**：内网链路由真实Agent承载；重连、吊销、更新与资源指标有证据。

**当前状态**：ready_for_user；后端自测 已通过；用户验收 待确认。

每轮同时执行主计划中的登录/权限/前轮业务回归。原生客户端、外部IdP及灾备需相应真实环境；未执行记not_run。

**源码入口**

- [src/api/agent-gateway-api.ts](../../src/api/agent-gateway-api.ts)
- [src/api/ssh-gateway-api.ts](../../src/api/ssh-gateway-api.ts)
- [src/api/network-proxy-api.ts](../../src/api/network-proxy-api.ts)
- [src/api/gateway-group-api.ts](../../src/api/gateway-group-api.ts)
- [src/api/monitoring-api.ts](../../src/api/monitoring-api.ts)

<a id="r07"></a>

## R07 浏览器图形、Telnet、共享与存储

前置：R06。模块：M06、M07、M08、M03。使用需求：U03、U12、U14、U16。

**后端交付**

- guacd RDP/VNC、Telnet、图形录像、监控/分享和逐入口能力
- 共享存储ACL、RDP平台驱动目录、读写及剪贴板限制

**前端交付**

- 协议表单与guacamole帧一致，分享/监控/文件入口按能力显示
- 存储页面和图形回放参数适配，不把平台存储等同原生客户端磁盘

**验证入口**：`/x-asset`、`/graphics`、`/storage`、`/online-session`、`/graphics-playback`（均在新环境域名内，R00旧站点对照除外）。

**用户操作**

1. 创建并连接Windows RDP、VNC和Telnet测试资产
2. 验证键鼠/剪贴板/只读，管理员监控和终止
3. 邀请授权观察者共享会话并撤销分享
4. 挂载共享存储，上传测试文件并回放图形会话

**反向用例**：只读输入、未授权分享和跨用户存储访问均被服务端拒绝；录屏失败状态可见。

**通过门槛**：三种实际协议、图形审计、共享和存储隔离完整验收。

**当前状态**：in_progress；后端自测 未通过/未执行；用户验收 待确认。

每轮同时执行主计划中的登录/权限/前轮业务回归。原生客户端、外部IdP及灾备需相应真实环境；未执行记not_run。

**源码入口**

- [src/pages/access/AccessGuacamole.tsx](../../src/pages/access/AccessGuacamole.tsx)
- [src/pages/access/GuacamolePage.tsx](../../src/pages/access/GuacamolePage.tsx)
- [src/api/storage-api.ts](../../src/api/storage-api.ts)
- [src/pages/access/GuacdPlayback.tsx](../../src/pages/access/GuacdPlayback.tsx)
- [src/pages/backend/BackendGraphicsSession.tsx](../../src/pages/backend/BackendGraphicsSession.tsx)
- [src/pages/backend/BackendGraphicsPlayback.tsx](../../src/pages/backend/BackendGraphicsPlayback.tsx)
- [backend/internal/application/r07_graphics.go](../../backend/internal/application/r07_graphics.go)
- [backend/internal/application/r07_graphics_test.go](../../backend/internal/application/r07_graphics_test.go)
- [backend/internal/application/r07_observers.go](../../backend/internal/application/r07_observers.go)
- [backend/internal/application/r07_observers_test.go](../../backend/internal/application/r07_observers_test.go)
- [src/pages/backend/BackendObserverPage.tsx](../../src/pages/backend/BackendObserverPage.tsx)
- [src/pages/backend/BackendSessionShares.tsx](../../src/pages/backend/BackendSessionShares.tsx)

<a id="r08"></a>

## R08 高级访问策略、申请审批与通知

前置：R07。模块：M02、M11、M14、M01。使用需求：U05、U36。

**后端交付**

- 时段/IP/地域/动作策略、命令过滤、访问审批状态机和临时grant
- 真实邮件/企业微信等已选渠道、绑定/偏好/去重与失败重试；GeoData导入更新

**前端交付**

- 规则编辑/申请/审批详情和通知状态对齐
- 区分业务拒绝与登录失效，审批成功刷新目录而非伪造授权

**验证入口**：`/access-policy`、`/strategy`、`/command-filter`、`/ip-set`、`/access-request`、`/x-access-request`、`/setting`（均在新环境域名内，R00旧站点对照除外）。

**用户操作**

1. operator为未授权测试资产申请临时访问
2. reviewer审批后operator看到并连接资产
3. 到期后目录和已有连接撤权
4. 配置测试收件箱，检查通知与重试记录

**反向用例**：自批/重复审批/修改申请后用旧批准执行拒绝；通知失败不撤销批准或重复授权。

**通过门槛**：申请→审批→授权→访问→到期回收及通知闭环，策略真正在连接端执行。

**当前状态**：planned；后端自测 未通过/未执行；用户验收 待确认。

每轮同时执行主计划中的登录/权限/前轮业务回归。原生客户端、外部IdP及灾备需相应真实环境；未执行记not_run。

**源码入口**

- [src/api/access-policy-api.ts](../../src/api/access-policy-api.ts)
- [src/api/strategy-api.ts](../../src/api/strategy-api.ts)
- [src/api/command-filter-api.ts](../../src/api/command-filter-api.ts)
- [src/api/ip-set-api.ts](../../src/api/ip-set-api.ts)
- [src/api/access-request-api.ts](../../src/api/access-request-api.ts)
- [src/api/notification-api.ts](../../src/api/notification-api.ts)

<a id="r09"></a>

## R09 网站代理、证书与mTLS

前置：R08。模块：M09、M13、M02、M08。使用需求：U17、U18、U19、U20、U27。

**后端交付**

- 网站唯一域名、登录回跳、公开/临时IP策略、Header/响应改写及WS反代
- TLS导入/ACME/DNS续签、客户端证书信任/吊销、分别配置网站与API真实IP

**前端交付**

- 网站入口使用真实测试域名、证书错误/续签状态可见
- 临时白名单autoRenew及到期展示；前端验证代理后台日志与访问结果

**验证入口**：`/website`、`/authorised-website`、`/x-website`、`/certificate`、`/setting`（均在新环境域名内，R00旧站点对照除外）。

**用户操作**

1. 配置HTTPS测试网站和证书，授权operator后从门户打开
2. 比较匿名/登录/临时IP三种策略，核对到期
3. 测试上游WebSocket和Header改写
4. 签发客户端证书访问mTLS网站，再吊销后重试

**反向用例**：伪造XFF/Host、恶意回跳、错证书及越权网站ID拒绝。

**通过门槛**：真实浏览器跨业务域访问闭环，TLS、回跳、身份、授权与审计一致。

**当前状态**：planned；后端自测 未通过/未执行；用户验收 待确认。

每轮同时执行主计划中的登录/权限/前轮业务回归。原生客户端、外部IdP及灾备需相应真实环境；未执行记not_run。

**源码入口**

- [src/api/website-api.ts](../../src/api/website-api.ts)
- [src/api/website-temp-allow-api.ts](../../src/api/website-temp-allow-api.ts)
- [src/api/certificate-api.ts](../../src/api/certificate-api.ts)
- [src/api/dns-provider-api.ts](../../src/api/dns-provider-api.ts)
- [src/api/authorised-website-api.ts](../../src/api/authorised-website-api.ts)

<a id="r10"></a>

## R10 任务调度、片段与凭据轮换

前置：R09。模块：M12、M04、M14、M08。使用需求：U06、U34。

**后端交付**

- 手动/cron任务、取消/超时/租约/去重/运行日志，个人及系统片段
- 密码/密钥轮换、逐资产验证、部分失败和unknown核对

**前端交付**

- 片段插入与执行按钮分开；异步任务轮询和重试状态对齐
- 轮换详情显示逐目标结果，unknown不得提供盲目重做按钮

**验证入口**：`/scheduled-task`、`/snippet`、`/x-snippet`、`/credential-rotation`（均在新环境域名内，R00旧站点对照除外）。

**用户操作**

1. 创建片段并插入终端，确认未自动执行
2. 创建测试任务手动运行并设置短期测试调度，查运行日志
3. 对专用资产执行凭据轮换，使用新凭据连接验证
4. 模拟部分失败并取消任务，查看需要人工核对项

**反向用例**：双击执行/双Worker不能重复派发；远端成功但回包丢失时unknown不可自动重做。

**通过门槛**：任务与轮换可通过前端追踪到最终状态，故障不会静默重复外部写入。

**当前状态**：implemented-awaiting-user-acceptance；后端自测 已通过；用户验收 待确认。

每轮同时执行主计划中的登录/权限/前轮业务回归。原生客户端、外部IdP及灾备需相应真实环境；未执行记not_run。

**源码入口**

- [src/api/scheduled-task-api.ts](../../src/api/scheduled-task-api.ts)
- [src/api/snippet-api.ts](../../src/api/snippet-api.ts)
- [src/api/snippet-user-api.ts](../../src/api/snippet-user-api.ts)
- [src/api/credential-rotation-api.ts](../../src/api/credential-rotation-api.ts)

<a id="r11"></a>

## R11 备份、隔离恢复与运维CLI

前置：R10。模块：M16、M18、F00、M04、M13。使用需求：U28、U29、U30。

**后端交付**

- D04自有归档、CA/KEK、独立对象清单、远端复制/保留、离线恢复日志
- 可执行CLI和隔离staging恢复→验证→切换；pgBackRest灾备演练

**前端交付**

- 设置页显示备份包含范围/格式/进度；安全备份和恢复维护状态
- 恢复重启后重新登录并展示独立restoreId；不等待被恢复库中的旧任务轮询

**验证入口**：`/setting`、`/system-event`（均在新环境域名内，R00旧站点对照除外）。

**用户操作**

1. 在新测试环境备份并下载归档
2. 更改一个测试资源后恢复到独立环境验证数据回到备份时点
3. 核对秘密可解密、CA、录像/驱动盘清单
4. 通过维护入口执行切换与旧任务隔离，重新登录复核

**反向用例**：错密钥/坏hash/不兼容版本/安全备份失败必须中止；旧SQL/轮换任务不重放。

**通过门槛**：前端发起与观察、CLI执行恢复、重新登录验证闭环；记录实测RPO/RTO。

**当前状态**：planned；后端自测 未通过/未执行；用户验收 待确认。

每轮同时执行主计划中的登录/权限/前轮业务回归。原生客户端、外部IdP及灾备需相应真实环境；未执行记not_run。

**源码入口**

- [src/api/backup-api.ts](../../src/api/backup-api.ts)
- [src/api/property-api.ts](../../src/api/property-api.ts)
- [src/api/system-event-api.ts](../../src/api/system-event-api.ts)

<a id="r12"></a>

## R12 MySQL代理与SQL工单

前置：R11。模块：M10、M01、M11、M08、M14。使用需求：U03、U21、U22、U23、U36。

**后端交付**

- D07.1/.2 TLS完整认证、个人数据库密码、text/prepare、只读目标角色
- SQL申请/审批后自动执行/partial/unknown/审计/通知

**前端交付**

- 数据库密码创建一次展示；连接说明和拷贝命令不含目标密码
- 工单列表/详情/结果/分页及unknown状态适配，避免重复执行按钮

**验证入口**：`/database-asset`、`/authorised-database-asset`、`/x-database-asset`、`/db-work-order`、`/x-db-work-order`、`/database-sql-log`、`/info`（均在新环境域名内，R00旧站点对照除外）。

**用户操作**

1. 网页创建MySQL资产并授权，个人中心生成专用密码
2. 复制连接参数用CLI或DBeaver连库执行只读查询
3. 前端申请写SQL，reviewer审批后查看自动执行结果
4. 从SQL审计与邮件核对同一executionId

**反向用例**：越权资产/过期密码/只读绕过拒绝；提交后断网不自动重复SQL。

**通过门槛**：前端配置→真实数据库客户端→审批执行→前端审计闭环；前端HTTP成功不能代替协议测试。

**当前状态**：开发完成：内网验收通过，DBeaver查询已确认，待用户整轮验收；后端自测 已通过；用户验收 待确认。

每轮同时执行主计划中的登录/权限/前轮业务回归。原生客户端、外部IdP及灾备需相应真实环境；未执行记not_run。

**源码入口**

- [src/api/database-asset-api.ts](../../src/api/database-asset-api.ts)
- [src/api/authorised-database-asset-api.ts](../../src/api/authorised-database-asset-api.ts)
- [src/api/db-work-order-api.ts](../../src/api/db-work-order-api.ts)
- [src/api/database-sql-log-api.ts](../../src/api/database-sql-log-api.ts)

<a id="r13"></a>

## R13 联合身份、Passkey与账户能力补全

前置：R12。模块：M01、M13、M14、M18。使用需求：U24、U25、U26、U27。

**后端交付**

- LDAP/OIDC/企业微信联邦登录及绑定/解绑；OIDC提供方/客户端/consent/JWKS
- Passkey、客户端证书、API Key、SSH公钥、邮箱/手机验证与选定SMS通道、身份恢复
- LDAP 分页目录同步、受控导入及离职停用；本地密码登录策略、最后管理员防锁死与 CLI 恢复

**前端交付**

- 所有账户tabs、第三方回调、同意页和无能力query gate补齐
- 使用可信HTTPS域验证Passkey/mTLS；处理登录中间态与绑定冲突

**验证入口**：`/info`、`/x-info`、`/oidc-client`、`/setting`、`/oidc/callback`、`/wechat-work/callback`、`/oidc/server/consent`（均在新环境域名内，R00旧站点对照除外）。

**用户操作**

1. 配置测试IdP分别登录与绑定已有用户
2. Passkey 开发及自动化测试通过；实体 Windows Hello / 安全密钥注册登录按用户要求跳过
3. 创建OIDC客户端由测试应用发起授权并校验身份
4. 个人中心验证API Key、SSH公钥和证书创建/撤销，完成邮箱手机验证

**反向用例**：错误state/nonce/redirect/PKCE和refresh重用拒绝；禁用本地密码前防管理员锁死。

**通过门槛**：全部身份入口与恢复流程通过；需外部服务的用例未执行时明确not_run。

**当前状态**：开发完成：全量后端回归与内网浏览器验收通过；Passkey 实体设备按用户要求跳过，待用户整轮验收；后端自测 已通过；用户验收 待确认。

每轮同时执行主计划中的登录/权限/前轮业务回归。原生客户端、外部IdP及灾备需相应真实环境；未执行记not_run。

**源码入口**

- [src/api/oidc-api.ts](../../src/api/oidc-api.ts)
- [src/api/oidc-client-api.ts](../../src/api/oidc-client-api.ts)
- [src/api/wechat-work-api.ts](../../src/api/wechat-work-api.ts)
- [src/api/account-api.ts](../../src/api/account-api.ts)
- [backend/internal/application/r13_oauth_server.go](../../backend/internal/application/r13_oauth_server.go)
- [backend/internal/application/r13_contacts.go](../../backend/internal/application/r13_contacts.go)
- [backend/internal/application/r13_login_policy.go](../../backend/internal/application/r13_login_policy.go)
- [backend/internal/modules/identity/ldap_sync.go](../../backend/internal/modules/identity/ldap_sync.go)
- [src/pages/backend/BackendOAuthClients.tsx](../../src/pages/backend/BackendOAuthClients.tsx)
- [src/pages/backend/BackendContactSettings.tsx](../../src/pages/backend/BackendContactSettings.tsx)
- [docs/backend/acceptance/R13.md](../../docs/backend/acceptance/R13.md)

<a id="r14"></a>

## R14 原生SSH/RDP与自有客户端

前置：R13。模块：M06、M05、M08、M07、M13。使用需求：U04、U09、U10、U11、U12、U13。

**后端交付**

- D01 PKCE/目录/HTTPS bridge和Termark公开URI；原生SSH及端口转发白名单
- D03 FreeRDP票据/NLA/TLS、通道策略/录像/终止；RemoteApp和磁盘按独立profile完整验收

**前端交付**

- 门户下载.rdp/唤起URI/客户端授权页适配；无效票据重新申请
- 复用VideoPlayback接MP4 manifest；原生监控/终止能力显式展示，官方专有兼容差异登记

**验证入口**：`/x-asset`、`/agent-access`、`/asset-authorization`、`/online-session`、`/offline-session`、`/video-playback`、`/setting`、`/oidc-client`、`/access-log`、`/x-info`（均在新环境域名内，R00旧站点对照除外）。

**用户操作**

1. 网页授权SSH后用OpenSSH及Termark URI连接
2. 网页生成.rdp，用mstsc连接并从网页终止/回放
3. 仅允许出站443环境用nt-client完成SSH/RDP
4. 分别验证完整桌面、剪贴板方向和磁盘限制（RemoteApp 已跳过），记录所有profile结果

**反向用例**：票据重放/撤权/错误证书及通道绕过拒绝；官方Termark同步未验收不标兼容。

**通过门槛**：除用户明确跳过的 RemoteApp 外，协议和真实客户端矩阵通过；任何其他未实现能力必须保持未完成。。

**当前状态**：in_progress / PKCE、目录、HTTPS SSH桥接、个人公钥TCP网关、门户配置/Termark链接、TCP及HTTPS受控转发已实现；原生RDP、客户端实机矩阵、系统密钥库/设备管理及完整客户端未完成；后端自测 未通过/未执行；用户验收 待确认。

每轮同时执行主计划中的登录/权限/前轮业务回归。原生客户端、外部IdP及灾备需相应真实环境；未执行记not_run。

**源码入口**

- [src/api/portal-api.ts](../../src/api/portal-api.ts)
- [src/pages/access/VideoPlayback.tsx](../../src/pages/access/VideoPlayback.tsx)
- [src/pages/agent/AgentAccessPage.tsx](../../src/pages/agent/AgentAccessPage.tsx)
- [src/pages/sysconf/RdpProxySetting.tsx](../../src/pages/sysconf/RdpProxySetting.tsx)
- [backend/internal/application/r14_native.go](../../backend/internal/application/r14_native.go)
- [backend/internal/modules/identity/native_oauth.go](../../backend/internal/modules/identity/native_oauth.go)
- [backend/cmd/nt-client/main.go](../../backend/cmd/nt-client/main.go)
- [backend/internal/nativeclient/client.go](../../backend/internal/nativeclient/client.go)
- [docs/backend/acceptance/R14.md](../../docs/backend/acceptance/R14.md)
- [backend/internal/application/r14_forward.go](../../backend/internal/application/r14_forward.go)
- [backend/internal/application/r14_forward_settings.go](../../backend/internal/application/r14_forward_settings.go)
- [backend/internal/application/r14_forward_test.go](../../backend/internal/application/r14_forward_test.go)
- [backend/migrations/052_native_forward.sql](../../backend/migrations/052_native_forward.sql)
- [src/pages/backend/BackendNativeForwardSettings.tsx](../../src/pages/backend/BackendNativeForwardSettings.tsx)
- [src/pages/backend/BackendForwardAudit.tsx](../../src/pages/backend/BackendForwardAudit.tsx)
- [backend/internal/application/r14_https_forward_test.go](../../backend/internal/application/r14_https_forward_test.go)
- [backend/internal/nativeclient/forward.go](../../backend/internal/nativeclient/forward.go)

<a id="r15"></a>

## R15 AI对话、工具审批与审计

前置：R14。模块：M17、M02、M08、M18。使用需求：U35。

**后端交付**

- 模型配置/会话消息/流式输出/取消恢复/预算；只读工具和写工具审批
- 系统与资产AI双门控、tool参数hash、去重、撤权复核和审计助手

**前端交付**

- WS/流式帧与断线状态、审批卡、费用上限/失败原因适配
- 禁止将模型文本当执行指令；工具输出按结构显示并关联执行记录

**验证入口**：`/ai`、`/access`、`/setting`（均在新环境域名内，R00旧站点对照除外）。

**用户操作**

1. 配置受控测试模型并启用一个资产AI
2. 发起只读查询，查看流式响应及审计
3. 申请写工具先拒绝一次再批准安全测试操作
4. 取消响应/断网重连，确认消息与工具未重复执行

**反向用例**：重复批准、参数变更、审批后撤权、预算超限均不能执行。

**通过门槛**：模型调用和真实工具效果均留证，UI流式展示与服务端执行状态一致。

**当前状态**：implemented_verified / R15 全部规划功能已完成：三协议模型、队列与 WS 恢复、分支/摘要、五种工具、参数审批/取消、预算报表及个人偏好；全量 Go 回归、前端构建、内网模型页面与真实 SSH/SFTP 效果验收通过，已部署 r15-complete/schema59。待用户签收；R14 未完成项按用户要求暂缓；后端自测 已通过；用户验收 待确认。

每轮同时执行主计划中的登录/权限/前轮业务回归。原生客户端、外部IdP及灾备需相应真实环境；未执行记not_run。

**源码入口**

- [src/api/ai-api.ts](../../src/api/ai-api.ts)
- [src/api/ai-settings-api.ts](../../src/api/ai-settings-api.ts)
- [src/api/ai-preferences-api.ts](../../src/api/ai-preferences-api.ts)

<a id="r16"></a>

## R16 PG扩展、配置与运营完整收口

前置：R15。模块：M10、M15、M18、M02、M08、M05、M14。使用需求：U08、U15、U16、U20、U30、U31、U32、U33。

**后端交付**

- D07.3 PG SCRAM/extended/COPY/pipeline/取消；告警/诊断/聚合统计完整实现
- 103文档键及所有当前前端扩展键、revision/ACK、CLI、配额/许可/品牌、日志保留和运维runbook全量收口

**前端交付**

- 移除已完成页面的临时禁用；dashboard接真实聚合；设置页展示生效/失败revision
- 全量页面/按钮/API台账核对，填补前轮遗漏，原生/外部兼容差异逐项列出

**验证入口**：`/dashboard`、`/monitoring`、`/system-event`、`/access-log-stats`、`/setting`、`/x-database-asset`（均在新环境域名内，R00旧站点对照除外）。

**用户操作**

1. 用网页配置PG资产，psql/JDBC执行后核对SQL审计
2. 修改安全/监听/通知配置观察ACK与实际行为
3. 触发测试告警/恢复，核对仪表盘和通知
4. 逐页遍历管理端与用户端，核对无隐藏未完功能和未知请求

**反向用例**：过期配置revision/失联节点/配额并发/未实现能力许可开关不能绕过；PG错误后Sync恢复正确。

**通过门槛**：全量前端契约/属性/模块功能有测试结果；仅覆盖表完整不能标功能完成。

**当前状态**：implemented_verified / R16 已完成开发与部署（schema102）；后端351项、前端60项及Windows JDBC矩阵通过；DBeaver相关验收按用户2026-11-12明确指示免测关闭，历史结果不改写。R14暂缓项与R17切换独立；整轮用户签收未代填；后端自测 已通过；用户验收 待确认。

每轮同时执行主计划中的登录/权限/前轮业务回归。原生客户端、外部IdP及灾备需相应真实环境；未执行记not_run。

**源码入口**

- [src/api/dashboard-api.ts](../../src/api/dashboard-api.ts)
- [src/api/monitoring-api.ts](../../src/api/monitoring-api.ts)
- [src/api/property-api.ts](../../src/api/property-api.ts)
- [src/api/system-event-api.ts](../../src/api/system-event-api.ts)
- [src/api/geodata-api.ts](../../src/api/geodata-api.ts)
- [docs/backend/acceptance/R16.md](../../docs/backend/acceptance/R16.md)
- [docs/backend/acceptance/R16-final-regression.json](../../docs/backend/acceptance/R16-final-regression.json)
- [docs/backend/acceptance/R16-dbeaver-closure.json](../../docs/backend/acceptance/R16-dbeaver-closure.json)

<a id="r17"></a>

## R17 全新生产部署、试运行与上线

前置：R16。模块：F00、M01、M02、M03、M04、M05、M06、M07、M08、M09、M10、M11、M12、M13、M14、M15、M16、M17、M18。使用需求：U01、U28、U29、U30、U31、U32。

**后端交付**

- 生产固定版本镜像、独立部署配置、环境预检查、安装升级与健康检查；不依赖.local二进制挂载或测试服务
- 空数据库初始化生产管理员/权限/密钥，配置正式资产、证书、通知、Agent及启用的协议入口；不导入旧系统数据或验收数据
- 生产持久化、异地备份/WAL归档与隔离恢复演练；并发/磁盘/录屏/MP4资源限额和告警验证
- 小范围新建用户与资产试运行、发布窗口和升级回退演练、生产运维与最终验收包

**前端交付**

- 构建前端静态发布包，以正式域名同源访问新后端；移除生产构建的legacy回退及联调角标
- 验证初始化、登录、授权、会话、文件、录屏、审批和审计完整旅程；不依赖旧后端即可运行

**验证入口**：`/login`、`/x-asset`、`/dashboard`、`/setting`、`/system-status`、`/access-log`、`/agent-gateway`（均在新环境域名内，R00旧站点对照除外）。

**用户操作**

1. 在空白生产同等环境安装，初始化管理员并配置正式域名、证书和备份
2. 新建少量用户、资产与授权进行试运行，验证真实连接及审计
3. 完成容量、断电重启/故障恢复、升级与备份恢复演练，核对恢复目标
4. 按首发启用功能清单正式开放并观察，完成用户签收

**反向用例**：初始化重复执行不能覆盖已有账号/数据；缺失密钥、持久化、证书或恢复校验失败阻止上线；升级后已有新写入时不得直接换旧镜像声称无损回退。

**通过门槛**：首发范围明确，干净环境可重复部署；启用功能、权限与审计回归、容量和恢复演练通过；生产独立运行并由用户签收。DBeaver按既定免测关闭，R14暂缓和其他既定排除项不自动加入首发。

**当前状态**：implemented_verified / 按用户认可的Docker模拟生产环境完成：全新部署、HTTPS浏览器/审计、持续会话容量、远程WAL/PITR与恢复回退、Agent双节点排空推广及根轮换均通过；后端短回归348项、前端67项通过，专项证据独立记录。实际生产开放与用户签收未代填；后端自测 已通过；用户验收 待确认。

每轮同时执行主计划中的登录/权限/前轮业务回归。原生客户端、外部IdP及灾备需相应真实环境；未执行记not_run。

**源码入口**

- [src/api/core/requests.ts](../../src/api/core/requests.ts)
- [src/App.tsx](../../src/App.tsx)
- [scripts/frontend-release.mjs](../../scripts/frontend-release.mjs)
- [tests/frontend-release.test.mjs](../../tests/frontend-release.test.mjs)
- [docs/backend/acceptance/R17.md](../../docs/backend/acceptance/R17.md)
- [deploy/production/compose.yaml](../../deploy/production/compose.yaml)
- [deploy/production/README.md](../../deploy/production/README.md)
- [scripts/production-preflight.mjs](../../scripts/production-preflight.mjs)
- [backend/internal/application/r17_production_test.go](../../backend/internal/application/r17_production_test.go)
- [deploy/production/services.yaml](../../deploy/production/services.yaml)
- [deploy/production/protocols.yaml](../../deploy/production/protocols.yaml)
- [scripts/production-addons.mjs](../../scripts/production-addons.mjs)
- [docs/backend/acceptance/R17-services.json](../../docs/backend/acceptance/R17-services.json)
- [deploy/production/agents.yaml](../../deploy/production/agents.yaml)
- [deploy/production/operations.md](../../deploy/production/operations.md)
- [backend/internal/agentupdate/export.go](../../backend/internal/agentupdate/export.go)
- [docs/backend/acceptance/R17-operations.json](../../docs/backend/acceptance/R17-operations.json)
- [deploy/production/wal.yaml](../../deploy/production/wal.yaml)
- [deploy/production/wal.md](../../deploy/production/wal.md)
- [scripts/production-wal.mjs](../../scripts/production-wal.mjs)
- [backend/test/deployment/wal-drill.sh](../../backend/test/deployment/wal-drill.sh)
- [backend/test/deployment/proxy-headers.mjs](../../backend/test/deployment/proxy-headers.mjs)
- [backend/test/deployment/remote-wal.mjs](../../backend/test/deployment/remote-wal.mjs)
- [backend/fixtures/r17-remote-wal/README.md](../../backend/fixtures/r17-remote-wal/README.md)
- [docs/backend/acceptance/R17-docker-recovery.json](../../docs/backend/acceptance/R17-docker-recovery.json)
- [scripts/wal-maintenance.mjs](../../scripts/wal-maintenance.mjs)
- [deploy/production/systemd/README.md](../../deploy/production/systemd/README.md)
- [tests/wal-maintenance.test.mjs](../../tests/wal-maintenance.test.mjs)
- [docs/backend/acceptance/R17-maintenance.json](../../docs/backend/acceptance/R17-maintenance.json)
- [scripts/test-renderer-capacity.mjs](../../scripts/test-renderer-capacity.mjs)
- [deploy/production/capacity.md](../../deploy/production/capacity.md)
- [docs/backend/acceptance/R17-renderer-capacity.json](../../docs/backend/acceptance/R17-renderer-capacity.json)
- [deploy/production/cutover.md](../../deploy/production/cutover.md)
- [docs/backend/acceptance/R17-cutover.json](../../docs/backend/acceptance/R17-cutover.json)
- [scripts/test-r11-restore.ps1](../../scripts/test-r11-restore.ps1)
- [docs/backend/acceptance/R17-final.json](../../docs/backend/acceptance/R17-final.json)
- [deploy/production/r17-release-manifest.json](../../deploy/production/r17-release-manifest.json)
- [scripts/verify-production-release.mjs](../../scripts/verify-production-release.mjs)
- [scripts/test-browser-journey.mjs](../../scripts/test-browser-journey.mjs)
- [backend/test/deployment/browser-journey.mjs](../../backend/test/deployment/browser-journey.mjs)
- [backend/internal/application/r17_browser_test.go](../../backend/internal/application/r17_browser_test.go)
- [backend/internal/application/r17_capacity_test.go](../../backend/internal/application/r17_capacity_test.go)
- [backend/internal/agentupdate/rotate.go](../../backend/internal/agentupdate/rotate.go)
- [backend/internal/agentupdate/rotate_test.go](../../backend/internal/agentupdate/rotate_test.go)

## 使用需求覆盖（仅规划）

| 需求 | 负责开发/验证轮次 |
|---|---|
| U01 首次使用闭环 | [R01](#r01)、[R03](#r03)、[R04](#r04)、[R17](#r17) |
| U02 按接入方式筛选资源目录 | [R03](#r03) |
| U03 资产类型化配置 | [R03](#r03)、[R07](#r07)、[R12](#r12) |
| U04 SSH 名称与别名解析 | [R03](#r03)、[R14](#r14) |
| U05 动态分组授权与逐动作控制 | [R02](#r02)、[R03](#r03)、[R08](#r08) |
| U06 凭证引用完整生命周期 | [R03](#r03)、[R10](#r10) |
| U07 安全网关注册与生命周期 | [R06](#r06) |
| U08 Agent 配置和升级契约 | [R06](#r06)、[R16](#r16) |
| U09 入口 SSH 与出站 SSH 跳板分离 | [R06](#r06)、[R14](#r14) |
| U10 原生 SSH 入口与受控转发 | [R14](#r14) |
| U11 原生 RDP 一次性票据入口 | [R14](#r14) |
| U12 按入口发布会话能力矩阵 | [R07](#r07)、[R14](#r14) |
| U13 Termark 授权与两类传输 | [R14](#r14) |
| U14 两类文件系统与上传状态 | [R05](#r05)、[R07](#r07) |
| U15 资产访问日志与调查关联 | [R04](#r04)、[R16](#r16) |
| U16 分类保留与录屏配置 | [R05](#r05)、[R07](#r07)、[R16](#r16) |
| U17 Web 域名路由与登录回跳 | [R09](#r09) |
| U18 公开访问与临时 IP 白名单 | [R09](#r09) |
| U19 请求头与响应改写规则 | [R09](#r09) |
| U20 两套真实 IP 配置与信任边界 | [R01](#r01)、[R09](#r09)、[R16](#r16) |
| U21 数据库专用密码 | [R12](#r12) |
| U22 MySQL 代理兼容基线 | [R12](#r12) |
| U23 SQL 审批后自动执行 | [R12](#r12) |
| U24 TOTP 与恢复码 | [R02](#r02)、[R13](#r13) |
| U25 Passkey 域与来源配置 | [R13](#r13) |
| U26 OIDC 身份提供方完整闭环 | [R13](#r13) |
| U27 mTLS 用户证书与默认继承 | [R09](#r09)、[R13](#r13) |
| U28 明确备份边界与格式兼容 | [R11](#r11)、[R17](#r17) |
| U29 恢复编排与 PostgreSQL 升级 | [R11](#r11)、[R17](#r17) |
| U30 运维 CLI 作为统一应用入口 | [R01](#r01)、[R11](#r11)、[R16](#r16)、[R17](#r17) |
| U31 系统属性兼容注册表 | [R01](#r01)、[R16](#r16)、[R17](#r17) |
| U32 许可生命周期与能力分层 | [R01](#r01)、[R16](#r16)、[R17](#r17) |
| U33 协议故障与指标来源 | [R04](#r04)、[R06](#r06)、[R16](#r16) |
| U34 命令片段插入与任务执行分离 | [R10](#r10) |
| U35 AI 双重开关与审计助手 | [R15](#r15) |
| U36 执行反馈与邮件配置 | [R08](#r08)、[R12](#r12) |

## 模块覆盖（仅规划）

| 模块 | 涉及轮次 |
|---|---|
| [F00 公共基础设施](modules/F00-platform.md) | [R00](#r00)、[R01](#r01)、[R11](#r11)、[R17](#r17) |
| [M01 身份与组织](modules/M01-identity.md) | [R01](#r01)、[R02](#r02)、[R08](#r08)、[R12](#r12)、[R13](#r13)、[R17](#r17) |
| [M02 权限与访问策略](modules/M02-authorization.md) | [R02](#r02)、[R03](#r03)、[R04](#r04)、[R08](#r08)、[R09](#r09)、[R15](#r15)、[R16](#r16)、[R17](#r17) |
| [M03 资源目录与主机资产](modules/M03-resource.md) | [R03](#r03)、[R07](#r07)、[R17](#r17) |
| [M04 凭据与密钥](modules/M04-vault.md) | [R03](#r03)、[R10](#r10)、[R11](#r11)、[R17](#r17) |
| [M05 网关与网络连接](modules/M05-connectivity.md) | [R04](#r04)、[R06](#r06)、[R14](#r14)、[R16](#r16)、[R17](#r17) |
| [M06 远程会话](modules/M06-session.md) | [R04](#r04)、[R05](#r05)、[R06](#r06)、[R07](#r07)、[R14](#r14)、[R17](#r17) |
| [M07 文件与共享存储](modules/M07-files.md) | [R05](#r05)、[R07](#r07)、[R14](#r14)、[R17](#r17) |
| [M08 审计与录像](modules/M08-audit.md) | [R01](#r01)、[R02](#r02)、[R03](#r03)、[R04](#r04)、[R05](#r05)、[R07](#r07)、[R09](#r09)、[R10](#r10)、[R12](#r12)、[R14](#r14)、[R15](#r15)、[R16](#r16)、[R17](#r17) |
| [M09 网站访问代理](modules/M09-webproxy.md) | [R09](#r09)、[R17](#r17) |
| [M10 数据库访问代理](modules/M10-database.md) | [R12](#r12)、[R16](#r16)、[R17](#r17) |
| [M11 申请与审批](modules/M11-approval.md) | [R08](#r08)、[R12](#r12)、[R17](#r17) |
| [M12 任务调度与脚本](modules/M12-automation.md) | [R10](#r10)、[R17](#r17) |
| [M13 证书与身份凭据签发](modules/M13-pki.md) | [R09](#r09)、[R11](#r11)、[R13](#r13)、[R14](#r14)、[R17](#r17) |
| [M14 通知与消息投递](modules/M14-notification.md) | [R08](#r08)、[R10](#r10)、[R12](#r12)、[R13](#r13)、[R16](#r16)、[R17](#r17) |
| [M15 监控与诊断](modules/M15-operations.md) | [R06](#r06)、[R16](#r16)、[R17](#r17) |
| [M16 备份与恢复](modules/M16-backup.md) | [R11](#r11)、[R17](#r17) |
| [M17 AI 运维助手](modules/M17-ai.md) | [R15](#r15)、[R17](#r17) |
| [M18 系统设置与产品能力](modules/M18-system.md) | [R00](#r00)、[R01](#r01)、[R02](#r02)、[R11](#r11)、[R13](#r13)、[R15](#r15)、[R16](#r16)、[R17](#r17) |
