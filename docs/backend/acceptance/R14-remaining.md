# R14 剩余交付与验收台账

更新于 2026-11-10。验收证据见 R14.md，整轮仍在开发中。

| 交付项 | 当前状态 | 完成所需证据 |
| --- | --- | --- |
| FreeRDP 3.32.2 Linux amd64 SDK | 构建及启动/符号检查通过，C 认证探测 worker 已接通 | 仍需完整桌面 worker 与实机协议验收 |
| 单会话 RDP worker / supervisor | 已接通认证探测、开发环境只读桌面和按 worker PID/UID 隔离的录像通道 | 正式低权限部署、可交互桌面及完整故障矩阵待验收 |
| NTICKET 与平台授权 | 开发环境已接通原生 OAuth 签发、短时 .rdp 下载、只读监听、消费、续租与审计。签发接口到客户端画面与录像均已在隔离环境验证 | 宿主发布、Windows 桌面实机及正式终止验收 |
| 原生桌面与目标认证 | 隔离 NLA 目标已验证正确密码、拒绝错误密码以及传送两次位图更新，客户端与录像均复现颜色变化 | Windows 桌面实机、mstsc / xfreerdp、跳板及 Agent 的 RDP 协议验收 |
| 原生 RDP 通道策略 | 策略框架已交付并完成沙盒验证:Go 按 user_channel_policy 下发 clipboardCopy/clipboardPaste(paste 随只读关闭),worker 仅放行 cliprdr 并按方向过滤 MS-RDPECLIP 文本消息;其余静态与全部动态通道保持拒绝。纯 C 单测已并入 worker 镜像构建;Linux 沙盒内 PostgreSQL 16 集成测试(默认双向/关闭/只读+copy)通过 | 仍需:宿主机 Docker 镜像构建(`-BuildSdk` 不需重跑)与既有脚本回归;Windows 实机 mstsc 剪贴板双向与单向实测;未知静态通道注入与 DVC 旁路用例;只读桌面确认粘贴不可用 |
| 原生 RDP 录屏与终止 | 已接通首帧登记、审计库分段入库、访问记录鉴权回放与 MP4 下载。容量超限或撤权会断流 | 崩溃后的临时目录核对、真实磁盘满、管理员在线终止、Windows 实机及负载验收 |
| RemoteApp | 用户明确跳过 | 不开发、不验收、不作为本次完成门槛 |
| 客户端磁盘 | 独立 profile 政策框架已交付并完成沙盒验证：平台 RDPDriveEnabled 开关 + 用户通道策略决定 upload(目标读用户盘, readonly 禁用)/download(目标写用户盘)；worker 启用 Intercept=rdpdr 由 FreeRDP 代理中继，rdpdr 通道仅放行 FILESYSTEM 设备公告，单 IRP 限 1 MiB、会话累计 256 MiB，任一溢出后对应方向 fail-closed；逐操作审计经 ReportDeviceIO 批次入 operation_logs(每会话预算 10000 行)，Finalize 汇总读写总量与拒绝/溢出计数 | 仍需：宿主机镜像构建验证(rdpdr-policy.patch 应用与构建期单测)、Windows 实机 mstsc 端到端磁盘映射、无盘/只读会话确认设备不可见、超限与审计预算实测 |
| 门户 .rdp / HTTPS RDP | 原生 OAuth 文件签发与 Windows 宿主只读网关已部署到本地验收环境，回环端口、系统证书信任及指纹比对通过 | 仍需用户 PKCE 同意后的 mstsc 实机登录、录像回放，以及门户界面与正式发布 |
| 客户端安全密钥库 | Windows Credential Manager 与 Linux Secret Service、显式授权、串行轮换、pending 恢复和注销已实现；Windows 实测及独立 GNOME Keyring 测试已过 | 仍需 macOS Keychain；Linux 桌面/KWallet/交互解锁、多登录会话及 Windows 生产账号持久登录矩阵待验收；不做明文回退 |
| 设备管理 | 按 OAuth 授权族登记的登录设备、分页列表及单设备撤销已部署；列表/刷新/密码复核撤销已通过浏览器实测 | 登记/隔离/续租/撤销及真实 SSH 双连接断流已测。跨次登录归并与持久安装标识随系统密钥库继续开发，不将上报信息当作硬件身份；本次页面验收未覆盖启用 MFA 或多页数据 |
| 完整客户端交付 | CLI 0.3.0-r14 支持 Windows/Linux 记住登录；完整客户端仍未完成 | 原生 RDP 等入口集成、安装器/自动更新、各平台完整实机验证；便携 CLI 使用/移除说明见 native-client-agent.md |
| MobaXterm 矩阵 | 环境已确认，未验收 | 用户提供的 21.1.0.4628；真实平台 SSH 登录、主机信任、终止与审计 |
| Termark 矩阵 | 缺客户端环境 | 公开 URI 唤起、真实平台 SSH 登录及审计；不声称支持专有同步 |
| 原生 RDP 矩阵与负载 | 已有 Windows 桌面目标；其余矩阵环境缺失且实现未完成 | Windows 桌面、Windows Server、Linux 客户端（RemoteApp 已跳过），20 会话 2 小时负载及资源记录 |

## 环境事实

- MobaXterm：`C:\Program Files (x86)\Mobatek\MobaXterm\MobaXterm.exe`，文件版本 21.1.0.4628。
- 桌面自动化的主入口和备用入口均因 `helper_sandbox_lock_failed / SetNamedSecurityInfoW ... 5` 无法启动。不得将未操作的 MobaXterm 标记通过。
- 用户已提供 Linux 182.92.67.143 与 Windows 192.168.3.51。平台已有 SSH 资产“测试”和 RDP 资产“测试2”，均启用且绑定凭据；后端容器到 22/3389 的 TCP 探测成功。用户已确认 Windows 桌面版，并明确跳过 RemoteApp；不再要求提供 RDS 或应用别名。网络可达不等于登录验收通过。
- 已有网页 Guacamole RDP 能力、SSH bridge 和受控 TCP 转发，不能用于替代上表的原生 RDP 实现证明。

## SDK 边界

`backend/native-rdp.Dockerfile` 仅构建开发 SDK；未配置正式服务、未暴露监听、未开启 capability。固定源码压缩包 SHA-256 为 `9ccb8357bf0188044a460de775c7ea289ca7d2a2a917eca0f0a7c4dc9c8b08ea`。

开发 worker 提供认证探测和强制录屏的只读桌面两种模式。票据消费、目标准备、NLA 与证书指纹验证沿用同一流程。只读模式等到 Go 录屏器提交初始清单后才接受目标桌面，编码器收到首帧时更新平台连接状态。复现入口为 `scripts/test-native-recording.ps1`。Windows 实机、访问记录回放和正式入口仍待验收。
