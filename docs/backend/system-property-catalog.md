# 系统属性兼容目录

以下103键来自[官方系统属性文档](https://www.next-terminal.com/zh/docs/usage/system-properties.html)。官方 wireType 原样保留为 string；**本表默认值是本项目设计，不代表官方默认值**。运行实现按批交付；逐键状态以同目录JSON的validationStatus和[R16验收记录](acceptance/R16.md)为准，设计目录不代表全部完成。默认空值表示未配置，生成型私钥由相应启用流程生成；版本取构建元数据。JSON中 officialDefault 保留来源缺口。跨字段规则及effect语义见[配置实施规范](implementation/settings-entitlements.md)。当前前端还有文档页未列出的扩展键，见该规范。

## 类型与默认值

| 键 | 内部类型 | 本项目默认 | 单位 | owner |
|---|---|---|---|---|
| `mail-enabled` | bool | false | 不适用/见校验 | [M14 通知与消息投递](modules/M14-notification.md) |
| `mail-use-ssl` | bool | true | 不适用/见校验 | [M14 通知与消息投递](modules/M14-notification.md) |
| `mail-host` | hostname |  | 不适用/见校验 | [M14 通知与消息投递](modules/M14-notification.md) |
| `mail-port` | int | 465 | 不适用/见校验 | [M14 通知与消息投递](modules/M14-notification.md) |
| `mail-username` | string |  | 不适用/见校验 | [M14 通知与消息投递](modules/M14-notification.md) |
| `mail-password` | secret-ref | null | 不适用/见校验 | [M14 通知与消息投递](modules/M14-notification.md) |
| `mail-insecure-skip-verify` | bool | false | 不适用/见校验 | [M14 通知与消息投递](modules/M14-notification.md) |
| `mail-from` | email |  | 不适用/见校验 | [M14 通知与消息投递](modules/M14-notification.md) |
| `login-lock-enabled` | bool | true | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `login-lock-failed-duration` | int | 15 | minutes | [M01 身份与组织](modules/M01-identity.md) |
| `login-lock-failed-times` | int | 5 | count | [M01 身份与组织](modules/M01-identity.md) |
| `login-lock-account-duration` | int | 15 | minutes | [M01 身份与组织](modules/M01-identity.md) |
| `login-lock-ip-duration` | int | 0 | minutes | [M01 身份与组织](modules/M01-identity.md) |
| `login-captcha-enabled` | bool | true | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `login-force-totp-enabled` | bool | false | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `password-strength-type` | enum | recommend | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `password-strength-policy` | json | {"minLength":12,"minCharacterType":2,"mustNotContainUsername":true,"mustNotBePalindrome":false,"mustNotWeak":true} | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `login-session-duration` | int | 480 | minutes | [M01 身份与组织](modules/M01-identity.md) |
| `login-session-count-custom` | bool | false | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `login-session-count-limit` | int | 5 | count | [M01 身份与组织](modules/M01-identity.md) |
| `password-expiration-period` | int | -1 | days | [M01 身份与组织](modules/M01-identity.md) |
| `disable-password-login` | bool | false | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `access-require-mfa` | bool | false | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `access-mfa-expires-at` | int | 5 | minutes | [M01 身份与组织](modules/M01-identity.md) |
| `security-level` | enum | custom | 不适用/见校验 | [M18 系统设置与产品能力](modules/M18-system.md) |
| `user-client-cert-valid-days` | int | 90 | days | [M13 证书与身份凭据签发](modules/M13-pki.md) |
| `access-max-idle-second` | int | 900 | seconds | [M06 远程会话](modules/M06-session.md) |
| `ssh-server-enabled` | bool | false | 不适用/见校验 | [M06 远程会话](modules/M06-session.md) |
| `ssh-server-addr` | hostport | 0.0.0.0:2022 | 不适用/见校验 | [M06 远程会话](modules/M06-session.md) |
| `ssh-server-private-key` | secret-ref | null | 不适用/见校验 | [M06 远程会话](modules/M06-session.md) |
| `ssh-server-private-key-exists` | bool | false | 不适用/见校验 | [M06 远程会话](modules/M06-session.md) |
| `ssh-server-port-forwarding-enabled` | bool | false | 不适用/见校验 | [M06 远程会话](modules/M06-session.md) |
| `ssh-server-port-forwarding-host-port` | hostport-list | [] | 不适用/见校验 | [M06 远程会话](modules/M06-session.md) |
| `ssh-server-disable-password-auth` | bool | false | 不适用/见校验 | [M06 远程会话](modules/M06-session.md) |
| `db-proxy-enabled` | bool | false | 不适用/见校验 | [M10 数据库访问代理](modules/M10-database.md) |
| `db-proxy-addr` | hostport | 0.0.0.0:3307 | 不适用/见校验 | [M10 数据库访问代理](modules/M10-database.md) |
| `db-proxy-block-dml` | bool | true | 不适用/见校验 | [M10 数据库访问代理](modules/M10-database.md) |
| `recording-enabled` | bool | true | 不适用/见校验 | [M08 审计与录像](modules/M08-audit.md) |
| `session-saved-limit-days` | int | 90 | days | [M08 审计与录像](modules/M08-audit.md) |
| `login-log-saved-limit-days` | int | -1 | days | [M01 身份与组织](modules/M01-identity.md) |
| `cron-log-saved-limit-days` | int | -1 | days | [M12 任务调度与脚本](modules/M12-automation.md) |
| `access-log-saved-limit-days` | int | 31 | days | [M08 审计与录像](modules/M08-audit.md) |
| `db-sql-log-saved-limit-days` | int | -1 | days | [M08 审计与录像](modules/M08-audit.md) |
| `system-name` | string | Puppeteer | 不适用/见校验 | [M18 系统设置与产品能力](modules/M18-system.md) |
| `system-logo` | object-ref |  | 不适用/见校验 | [M18 系统设置与产品能力](modules/M18-system.md) |
| `system-copyright` | string |  | 不适用/见校验 | [M18 系统设置与产品能力](modules/M18-system.md) |
| `system-icp` | string |  | 不适用/见校验 | [M18 系统设置与产品能力](modules/M18-system.md) |
| `version` | string | build.version | 不适用/见校验 | [M18 系统设置与产品能力](modules/M18-system.md) |
| `watermark-enabled` | bool | true | 不适用/见校验 | [M06 远程会话](modules/M06-session.md) |
| `watermark-content` | string | 受控访问 | 不适用/见校验 | [M06 远程会话](modules/M06-session.md) |
| `watermark-content-asset-username` | bool | false | 不适用/见校验 | [M06 远程会话](modules/M06-session.md) |
| `watermark-content-user-account` | bool | true | 不适用/见校验 | [M06 远程会话](modules/M06-session.md) |
| `watermark-font-color` | color | rgba(128, 128, 128, 0.3) | 不适用/见校验 | [M06 远程会话](modules/M06-session.md) |
| `watermark-font-size` | int | 16 | px | [M06 远程会话](modules/M06-session.md) |
| `ldap-enabled` | bool | false | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `ldap-url` | url |  | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `ldap-user` | string |  | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `ldap-password` | secret-ref | null | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `ldap-base-dn` | string |  | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `ldap-user-search-size-limit` | int | 1000 | count | [M01 身份与组织](modules/M01-identity.md) |
| `ldap-user-search-filter` | ldap-filter | (objectClass=inetOrgPerson) | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `ldap-user-property-mapping` | json | {"username":"uid","nickname":"cn","mail":"mail"} | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `user-default-storage-size` | int | 5368709120 | bytes | [M07 文件与共享存储](modules/M07-files.md) |
| `agent-gateway-endpoint` | url |  | 不适用/见校验 | [M05 网关与网络连接](modules/M05-connectivity.md) |
| `asset-group-tree` | json | [] | 不适用/见校验 | [M03 资源目录与主机资产](modules/M03-resource.md) |
| `website-group-tree` | json | [] | 不适用/见校验 | [M03 资源目录与主机资产](modules/M03-resource.md) |
| `ip-extractor` | header-selector | direct | 不适用/见校验 | [M18 系统设置与产品能力](modules/M18-system.md) |
| `ip-trust-list` | cidr-list | [] | 不适用/见校验 | [M18 系统设置与产品能力](modules/M18-system.md) |
| `passkey-enabled` | bool | true（兼容允许；有效RP仍必需） | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `passkey-domain` | hostname |  | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `passkey-origins` | JSON数组字符串 | [] | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `wechat-work-enabled` | bool | false | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `wechat-work-corp-id` | string |  | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `wechat-work-agent-id` | string |  | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `wechat-work-secret` | secret-ref | null | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `wechat-work-redirect-uri` | url |  | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `wechat-work-department` | uuid | null | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `oidc-enabled` | bool | false | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `oidc-issuer` | url |  | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `oidc-client-id` | string |  | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `oidc-client-secret` | secret-ref | null | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `oidc-redirect-uri` | url |  | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `oidc-scopes` | string-list | ["openid","profile","email"] | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `oidc-department` | uuid | null | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `oidc-server-enabled` | bool | false | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `oidc-server-issuer` | url |  | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `oidc-server-signing-key` | secret-ref | null | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `oidc-server-hmac-secret` | secret-ref | null | 不适用/见校验 | [M01 身份与组织](modules/M01-identity.md) |
| `oidc-server-access-token-ttl` | int | 300 | seconds | [M01 身份与组织](modules/M01-identity.md) |
| `oidc-server-refresh-token-ttl` | int | 2592000（兼容30天；原生最多7天） | seconds | [M01 身份与组织](modules/M01-identity.md) |
| `oidc-server-id-token-ttl` | int | 300 | seconds | [M01 身份与组织](modules/M01-identity.md) |
| `oidc-server-auth-code-ttl` | int | 60 | seconds | [M01 身份与组织](modules/M01-identity.md) |
| `llm-enabled` | bool | false | 不适用/见校验 | [M17 AI 运维助手](modules/M17-ai.md) |
| `llm-api-key` | secret-ref | null | 不适用/见校验 | [M17 AI 运维助手](modules/M17-ai.md) |
| `llm-base-url` | url |  | 不适用/见校验 | [M17 AI 运维助手](modules/M17-ai.md) |
| `llm-model` | string |  | 不适用/见校验 | [M17 AI 运维助手](modules/M17-ai.md) |
| `llm-temperature` | float | 0.2 | 不适用/见校验 | [M17 AI 运维助手](modules/M17-ai.md) |
| `llm-max-tokens` | int | 4096 | tokens | [M17 AI 运维助手](modules/M17-ai.md) |
| `llm-proxy-url` | url |  | 不适用/见校验 | [M17 AI 运维助手](modules/M17-ai.md) |
| `llm-shell-prompt` | string |  | 不适用/见校验 | [M17 AI 运维助手](modules/M17-ai.md) |
| `llm-shell-enabled` | bool | false | 不适用/见校验 | [M17 AI 运维助手](modules/M17-ai.md) |
| `llm-audit-prompt` | string |  | 不适用/见校验 | [M17 AI 运维助手](modules/M17-ai.md) |
| `llm-audit-enabled` | bool | false | 不适用/见校验 | [M17 AI 运维助手](modules/M17-ai.md) |

## 校验与应用方式

M18拥有写入入口，各owner校验；所有秘密普通读取掩码。只读投影不形成第二份业务权威数据。

| 键 | 校验 | 生效方式 | 访问 | 秘密 |
|---|---|---|---|---|
| `mail-enabled` | 仅 true/false；开启前满足所属功能的跨字段校验 | next-job | read-write | 否 |
| `mail-use-ssl` | 仅 true/false；禁止通过字符串真值判断 | next-job | read-write | 否 |
| `mail-host` | 域名或IP，≤253字符；邮件开启必填 | next-job | read-write | 否 |
| `mail-port` | 1..65535 | next-job | read-write | 否 |
| `mail-username` | ≤1024字符，无NUL；所属服务开启时必填 | next-job | read-write | 否 |
| `mail-password` | 空默认不清除；显式clear；≤64KiB；开启所属服务时按认证方式检查 | next-job | read-write | 是 |
| `mail-insecure-skip-verify` | 仅 true/false；开启前满足所属功能的跨字段校验 | next-job | read-write | 否 |
| `mail-from` | 合法邮箱，邮件开启必填 | next-job | read-write | 否 |
| `login-lock-enabled` | 仅 true/false；禁止通过字符串真值判断 | security-live | read-write | 否 |
| `login-lock-failed-duration` | 1..1440；每次失败重置计时，无新失败达到该时长后计数过期 | security-live | read-write | 否 |
| `login-lock-failed-times` | 1..100 | security-live | read-write | 否 |
| `login-lock-account-duration` | 0..10080；0表示禁用此类锁定，与前端提示一致 | security-live | read-write | 否 |
| `login-lock-ip-duration` | 0..10080；0表示禁用此类锁定，与前端提示一致 | security-live | read-write | 否 |
| `login-captcha-enabled` | 仅 true/false；禁止通过字符串真值判断 | security-live | read-write | 否 |
| `login-force-totp-enabled` | 仅 true/false；开启前满足所属功能的跨字段校验 | security-live | read-write | 否 |
| `password-strength-type` | recommend/customize | security-live | read-write | 否 |
| `password-strength-policy` | minLength 8..128；minCharacterType 1..3；其余bool；拒绝未知字段；customize时执行 | security-live | read-write | 否 |
| `login-session-duration` | 1..525600 | new-session | read-write | 否 |
| `login-session-count-custom` | 仅 true/false；禁止通过字符串真值判断 | new-session | read-write | 否 |
| `login-session-count-limit` | 1..1000；custom=false时不限制 | new-session | read-write | 否 |
| `password-expiration-period` | -1或1..3650；0拒绝；-1无限 | security-live | read-write | 否 |
| `disable-password-login` | 仅 true/false；开启前满足所属功能的跨字段校验 | security-live | read-write | 否 |
| `access-require-mfa` | 仅 true/false；开启前满足所属功能的跨字段校验 | security-live | read-write | 否 |
| `access-mfa-expires-at` | 0..1440；0表示每次申请会话重新完成MFA | security-live | read-write | 否 |
| `security-level` | 只读兼容投影custom；不接受未定义等级触发隐式批量配置 | readonly | read-only | 否 |
| `user-client-cert-valid-days` | 1..825；新证书实际到期时间取配置期限与签发CA到期时间的较早者；已有证书不变 | new-session | read-write | 否 |
| `access-max-idle-second` | -1或1..86400；-1无限 | security-live | read-write | 否 |
| `ssh-server-enabled` | 仅 true/false；开启前满足所属功能的跨字段校验 | listener | read-write | 否 |
| `ssh-server-addr` | 使用net.SplitHostPort校验；端口1..65535；端口冲突拒绝 | listener | read-write | 否 |
| `ssh-server-private-key` | 首次启用自动生成Ed25519 Host Key；导入需有效SSH私钥；禁止读取明文 | listener | read-write | 是 |
| `ssh-server-private-key-exists` | 根据M04私钥存在性计算；禁止写入 | readonly | read-only | 否 |
| `ssh-server-port-forwarding-enabled` | 仅 true/false；开启前满足所属功能的跨字段校验 | new-session | read-write | 否 |
| `ssh-server-port-forwarding-host-port` | 每项明确host:port；最多1000项；默认无目标可转发；域名解析后再校验 | security-live | read-write | 否 |
| `ssh-server-disable-password-auth` | 仅 true/false；开启前满足所属功能的跨字段校验 | new-session | read-write | 否 |
| `db-proxy-enabled` | 仅 true/false；开启前满足所属功能的跨字段校验 | listener | read-write | 否 |
| `db-proxy-addr` | 使用net.SplitHostPort校验；端口1..65535；端口冲突拒绝 | listener | read-write | 否 |
| `db-proxy-block-dml` | 仅 true/false；禁止通过字符串真值判断 | new-session | read-write | 否 |
| `recording-enabled` | 仅 true/false；禁止通过字符串真值判断 | new-session | read-write | 否 |
| `session-saved-limit-days` | -1或1..3650；禁止0；清理前dry-run并遵守legal hold | next-job | read-write | 否 |
| `login-log-saved-limit-days` | -1或1..3650，0拒绝；缩短需同管理员/版本/值的十分钟预览；清理跳过保全与运行中记录 | next-job | read-write | 否 |
| `cron-log-saved-limit-days` | -1或1..3650，0拒绝；缩短需同管理员/版本/值的十分钟预览；清理跳过保全与运行中记录 | next-job | read-write | 否 |
| `access-log-saved-limit-days` | -1或1..3650，0拒绝；缩短需同管理员/版本/值的十分钟预览；清理跳过保全与运行中记录 | next-job | read-write | 否 |
| `db-sql-log-saved-limit-days` | -1或1..3650，0拒绝；缩短需同管理员/版本/值的十分钟预览；清理跳过保全与运行中记录 | next-job | read-write | 否 |
| `system-name` | 1..128字符，按文本转义 | live | read-write | 否 |
| `system-logo` | 已上传PNG/JPEG重新编码PNG后的对象ID；原图<=1MiB，尺寸<=1024x1024；空值恢复默认，拒绝URL/SVG | live | read-write | 否 |
| `system-copyright` | ≤1024字符；仅文本渲染 | live | read-write | 否 |
| `system-icp` | ≤1024字符；仅文本渲染 | live | read-write | 否 |
| `version` | 来自构建元数据，禁止写入 | readonly | read-only | 否 |
| `watermark-enabled` | 仅 true/false；禁止通过字符串真值判断 | new-session | read-write | 否 |
| `watermark-content` | ≤256字符；仅白名单模板变量；不允许HTML | new-session | read-write | 否 |
| `watermark-content-asset-username` | 仅 true/false；开启前满足所属功能的跨字段校验 | new-session | read-write | 否 |
| `watermark-content-user-account` | 仅 true/false；禁止通过字符串真值判断 | new-session | read-write | 否 |
| `watermark-font-color` | 解析rgba/hex；RGB 0..255、alpha 0..1；禁止任意CSS | new-session | read-write | 否 |
| `watermark-font-size` | 1..100 | new-session | read-write | 否 |
| `ldap-enabled` | 仅 true/false；开启前满足所属功能的跨字段校验 | security-live | read-write | 否 |
| `ldap-url` | ldaps://或ldap://+强制StartTLS；默认目录创建后不可更换地址 | new-session | read-write | 否 |
| `ldap-user` | 合法完整服务账号DN；更换DN必须重新填写或明确清除密码 | new-session | read-write | 否 |
| `ldap-password` | 空默认不清除；显式clear；≤64KiB；开启所属服务时按认证方式检查 | new-session | read-write | 是 |
| `ldap-base-dn` | 合法非空DN；默认目录创建后不可更换身份域 | new-session | read-write | 否 |
| `ldap-user-search-size-limit` | 1..10000；启用LDAP分页 | next-job | read-write | 否 |
| `ldap-user-search-filter` | 解析合法LDAP filter；值按RFC4515转义；≤4096字符 | new-session | read-write | 否 |
| `ldap-user-property-mapping` | JSON字符串；username必填；nickname/mail可选；拒绝未知键与非法属性名；邮箱仅作未验证预览 | new-session | read-write | 否 |
| `user-default-storage-size` | 0..1099511627776；0 无新增写入容量；仅首次创建个人空间时应用，已有配额不变 | new-session | read-write | 否 |
| `agent-gateway-endpoint` | HTTPS根URL；无userinfo/query/fragment/API子路径；启用注册时必填 | new-session | read-write | 否 |
| `asset-group-tree` | 只读兼容资源分组投影；一次性旧值迁移走M03/M09用例；不可直接写settings | readonly | read-only | 否 |
| `website-group-tree` | 只读兼容资源分组投影；一次性旧值迁移走M03/M09用例；不可直接写settings | readonly | read-only | 否 |
| `ip-extractor` | direct/x-real-ip/x-forwarded-for或合法自定义header；禁止敏感header | security-live | read-write | 否 |
| `ip-trust-list` | 最多1024项IPv4/IPv6 CIDR；wire逗号分隔；不自动信任私网 | security-live | read-write | 否 |
| `passkey-enabled` | 仅 true/false；开启前满足所属功能的跨字段校验 | security-live | read-write | 否 |
| `passkey-domain` | RP ID非URL；必须匹配允许origin的注册域；开启必填 | new-session | read-write | 否 |
| `passkey-origins` | 1–16个精确HTTPS origin；必须同时在部署API来源白名单；RP空与[]组合继承部署配置 | new-session | read-write | 否 |
| `wechat-work-enabled` | 仅 true/false；开启前满足所属功能的跨字段校验 | security-live | read-write | 否 |
| `wechat-work-corp-id` | ≤1024字符，无NUL；所属服务开启时必填 | new-session | read-write | 否 |
| `wechat-work-agent-id` | 十进制数字字符串；1..20位；避免JS整数精度丢失 | new-session | read-write | 否 |
| `wechat-work-secret` | 空默认不清除；显式clear；≤64KiB；开启所属服务时按认证方式检查 | new-session | read-write | 是 |
| `wechat-work-redirect-uri` | 精确HTTPS回调；禁止通配符/open redirect；开启必填 | new-session | read-write | 否 |
| `wechat-work-department` | 可空；非空需现存部门；不得自动赋管理角色 | new-session | read-write | 否 |
| `oidc-enabled` | 仅 true/false；开启前满足所属功能的跨字段校验 | security-live | read-write | 否 |
| `oidc-issuer` | 固定HTTPS issuer；无query/fragment；开启必填；变更需吊销相关token | new-session | read-write | 否 |
| `oidc-client-id` | ≤1024字符，无NUL；所属服务开启时必填 | new-session | read-write | 否 |
| `oidc-client-secret` | 空默认不清除；显式clear；≤64KiB；开启所属服务时按认证方式检查 | new-session | read-write | 是 |
| `oidc-redirect-uri` | 精确HTTPS回调；禁止通配符/open redirect；开启必填 | new-session | read-write | 否 |
| `oidc-scopes` | 以空格分隔；必须含openid；最多32项；按provider允许范围 | new-session | read-write | 否 |
| `oidc-department` | 可空；非空需现存部门；不得自动赋管理角色 | new-session | read-write | 否 |
| `oidc-server-enabled` | 仅 true/false；开启前满足所属功能的跨字段校验 | security-live | read-write | 否 |
| `oidc-server-issuer` | 固定HTTPS issuer；无query/fragment；开启必填；变更需吊销相关token | new-session | read-write | 否 |
| `oidc-server-signing-key` | 首次启用生成RSA>=3072位私钥；导入>=2048位；kid轮换并保留旧公钥到token过期 | new-session | read-write | 是 |
| `oidc-server-hmac-secret` | 首次启用生成32随机字节；客户端不能取得；不得与其他签名密钥共用 | new-session | read-write | 是 |
| `oidc-server-access-token-ttl` | 60..3600 | new-session | read-write | 否 |
| `oidc-server-refresh-token-ttl` | 3600..2592000 | new-session | read-write | 否 |
| `oidc-server-id-token-ttl` | 60..3600 | new-session | read-write | 否 |
| `oidc-server-auth-code-ttl` | 30..300；一次消费 | new-session | read-write | 否 |
| `llm-enabled` | 仅 true/false；开启前满足所属功能的跨字段校验 | new-session | read-write | 否 |
| `llm-api-key` | 空串/掩码保持；显式clear；最长4096字符，拒绝控制字符；修改需管理员密码和已绑定MFA | new-session | read-write | 是 |
| `llm-base-url` | HTTPS；明确允许内网时可用HTTP；拒绝凭据/query/fragment；域名解析后固定合法IP | next-job | read-write | 否 |
| `llm-model` | 启用必填，最多200字符；模型发现或模拟服务验证，未知模型由服务明确报错 | next-job | read-write | 否 |
| `llm-temperature` | -1省略参数或有限数0..2；不支持时设为-1使用模型默认 | next-job | read-write | 否 |
| `llm-max-tokens` | 64..4096；保留本平台有界输出和预算上限；不宣称实现规划的131072上限 | next-job | read-write | 否 |
| `llm-proxy-url` | HTTP/HTTPS CONNECT与SOCKS5/SOCKS5h；SOCKS明确端口；不接受URL凭据；两跳本地DNS校验与固定，包括SOCKS5h | next-job | read-write | 否 |
| `llm-shell-prompt` | ≤32768字符；空表示使用版本化内置prompt；不能覆盖工具授权 | next-job | read-write | 否 |
| `llm-shell-enabled` | 仅 true/false；开启前满足所属功能的跨字段校验 | new-session | read-write | 否 |
| `llm-audit-prompt` | ≤32768字符；空表示使用版本化内置prompt；不能覆盖工具授权 | next-job | read-write | 否 |
| `llm-audit-enabled` | 仅 true/false；开启前满足所属功能的跨字段校验 | new-session | read-write | 否 |

机器可读源：[system-property-catalog.json](system-property-catalog.json)。由模块文档脚本生成；检查覆盖和类型，不表示后端已实现这些规则。
