# Windows RDP 自动证书的项目端兼容

Windows Server 2016 的部分自动生成证书只有 Key Encipherment、Data Encipherment，没有 Digital Signature。即使系统列出 ECDHE_RSA 套件，该证书也不符合 ECDHE_RSA 的签名用途要求。Go 默认禁用静态 RSA 密钥交换时，证书发现连接可能收到 TLS handshake failure，Windows 记录 Schannel 36874。

项目从后端 r17-rc7 起，在专用证书发现 TLS 配置中显式添加 TLS_RSA_WITH_AES_128_GCM_SHA256 和 TLS_RSA_WITH_AES_256_GCM_SHA384，保留现代套件和 TLS 1.2 最低版本。不修改进程全局 GODEBUG，不影响 HTTPS/OIDC/数据库 TLS，不启用静态 RSA CBC、RC4、3DES、NULL、TLS 1.0 或 1.1。

该连接仅进行 X.224/RDP 安全协商、TLS 握手和证书指纹提取，不发送 Windows 用户名或密码，不建立桌面。兼容静态 RSA 不具备前向保密性，故限制在此无凭据发现路径。发现仍允许自签名证书用于管理员确认并绑定；实际 guacd 连接保持 ignore-cert=false 和证书指纹匹配。不会自动接受已有资产的证书更换，也不会回退到传统无 TLS 的 RDP 安全层。

## 验证

- 使用 RSA 2048、自签名、仅 Key Encipherment/Data Encipherment、Server Authentication 证书构建模拟 RDP 服务。
- 分别限制为 TLS 1.2 RSA-AES128-GCM 和 RSA-AES256-GCM，发现成功、SHA-256 指纹匹配；服务端检查发现完成后没有收到应用数据。
- 原有现代 TLS 自动抓取/保存测试、拒绝错误协议响应、取消测试及图形协议回归。
- 区分超时、安全协商拒绝码、TLS 握手错误；日志标识 rdp_certificate_probe_failed，不输出凭据。

实际内网 Windows 目标无法从开发环境访问，发布后须由管理员重新点击抓取证书并验证桌面连接。若远端拒绝所有 TLS 安全协商（包括 RSA-AES-GCM），本兼容方案不能将纯传统 RDP 连接变成 TLS 连接。

## r17-rc8：传统 RDP 安全层

对返回 RDP_NEG_FAILURE 0x00000002（SSL_NOT_ALLOWED_BY_SERVER）的资产，管理员可进入“资产 → 编辑 → 连接与认证 → RDP 安全模式”，明确选择“传统 RDP 兼容模式”。无需修改 Windows、重新生成证书或重启远端服务。

此模式按资产存储 RDP 安全策略（API 字段 `rdpSecurity: "rdp"`），清除不适用的 TLS 指纹，不执行 TLS 证书发现；网页图形网关使用 `security=rdp`。传统 RDP 安全层不提供平台的 TLS 证书指纹身份校验，仅建议在可信内网或受保护的网关路径使用。默认值仍为 TLS/NLA，没有协商失败自动降级。

原有授权、访问策略、录屏、只读及会话终止控制仍生效；管理员保存安全模式记录 `asset.rdp_security_saved` 审计。恢复默认模式时需重新获取 TLS 证书，目标不支持时拒绝保存。原生 RDP 客户端保持 TLS 要求，兼容资产仅支持网页桌面。

104 数据库迁移增加字段并保留所有现有资产默认模式；更新后端时自动执行。需同时升级 server 和 web 至 r17-rc8。

验证覆盖：显式启用、默认失败关闭、模式持久化、无效模式拒绝、未授权拒绝、图形网关参数、只读、录屏和终止会话。图形回归使用协议模拟网关，实际 Windows 桌面须部署后确认。
