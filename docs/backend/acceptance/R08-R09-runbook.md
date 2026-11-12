# R08 / R09 内网验收与前端操作

## 已部署环境

- 管理前端：<http://backend.localhost:5174/>。
- 后端版本 `r09.0-preview`，数据库迁移 25，能力版本 12。
- 网站 HTTPS 入口：本机 `18443`。管理 API 与网站网关独立监听。
- 内网服务：`fixture-services` 提供 SMTP STARTTLS、企业微信 HTTPS、HTTP/WebSocket 上游、带 TSIG 校验的 RFC2136 DNS；`acme-fixture` 运行 Pebble，执行真正的 DNS-01 验证。
- 模拟服务只在 Compose 网络内提供业务端口；模拟收件箱查看端口仅发布到本机 `127.0.0.1:18580`。没有使用公网通知账号或公网 DNS 密钥。

## 可以直接测试

| 功能 | 入口及操作 |
|---|---|
| 访问策略 | `/access-policy`：用户/部门/资源范围、时区、时段、IP/地域规则；地域数据按钮导入 JSON 前缀数据 |
| 动作与命令 | `/strategy`、`/command-filter`、`/ip-set`；资源授权页可附加动作策略和命令过滤 |
| 临时访问 | 普通用户 `/x-access-request` 申请 `R08 审批测试 SSH`；管理员或指定审批人在 `/access-request` 批准；门户出现资产，期限到达后目录撤权且已有连接断开 |
| 通知 | `/setting` 已配置内网 SMTP、企业微信、审批事件规则与用户绑定；支持测试、投递状态、失败重试、个人免打扰；投递记录区分渠道测试与业务通知 |
| 网站 | `/website` 管理，`/x-website` 使用，`/authorised-website` 授权；访问日志与临时 IP 记录可在列表操作中打开 |
| 证书 | `/certificate` 自签/导入服务器证书、客户端 CA、客户端证书；P12 导出需要管理员密码和 TOTP；支持吊销及重签 |
| DNS/ACME | 证书页的 ACME 账号、DNS 服务、签发与续签页签已准备内网配置和签发成功的订单；可立即续签并查看日志 |
| 网站设置 | `/website-setting` 载入设置、预览默认服务器证书对网站的影响、配置 API 与网站各自的可信代理 |
| SFTP | 终端文件管理的“权限”按钮支持三位八进制 chmod，服务器执行独立动作策略校验 |

测试账号 `r08-operator`、`r08-reviewer` 的随机密码保存在本地 `backend/.local/r08-r09-test-accounts.json`，未写入仓库或聊天。原管理员账号保持原样。审批员只能审核其配置范围内的申请，不能自批。

预置网站：

- <https://public.localhost:18443/>：公开网站，仍执行访问策略。
- <https://private.localhost:18443/>：经平台登录及资源授权后回跳。
- <https://temporary.localhost:18443/>：登录授权当前 IP，默认 5 分钟，成功访问可续期，最长 30 分钟。
- <https://mtls.localhost:18443/>：需要导入绑定用户的客户端 P12，且该用户仍须具有网站授权。

这些网站使用本次生成的内网自签名证书，首次访问会提示证书不受信任。验收中仅对上述本机测试域名通过浏览器证书提示继续访问；没有关闭整个浏览器的 TLS 校验，也没有安装系统级根证书。上线时换成受信任的服务器证书并将域名解析到网关。`acme.example.test` 是内网 ACME 协议验收域名，和上述浏览器入口分开。

## 本次实际执行的验收

1. 全量 `go test ./... -count=1 -timeout=12m`、`go vet ./...` 通过；应用包 262.430 秒。包含前轮 SSH、SFTP、图形连接、存储与观察者回归。
2. R08/R09 应用包 `go test -race` 通过（43.222 秒）。后续新增命令过滤回归及通知记录标记修改再次执行针对性测试与 `go vet`。
3. TypeScript 类型检查和 Vite backend 构建通过。Vite 仍有现有大 chunk 的体积提示，不影响构建结果。
4. 真实 SSH 协议验证批准后连接、到期撤权、持续连接关闭；命令过滤拒绝原始输入和不匹配的显式命令，匹配命令可执行；动态访问策略立即终止已有连接。
5. 浏览器以 `r08-operator` 提交一分钟申请，管理员通过页面批准，连接 Docker OpenSSH。到期后门户资产数从 1 变为 0，终端显示授权失效，申请状态变为 `expired`。申请编号 `889636e1-2c0c-4539-be64-5659a2600a58`，保留作验收记录。
6. 内网 SMTP 与企业微信收到渠道测试、提交、批准、到期通知；检查时 12 条投递为 `delivered`。另用协议测试覆盖供应方故障、TLS 拒绝、退避重试、并发去重、免打扰。
7. 浏览器完成 `private.localhost` → 管理控制台 → 一次性回调 → HTTPS 上游，WebSocket 按钮收到回显。上游请求头检查确认平台 Cookie 没有转发，配置的请求/响应头生效。
8. 浏览器完成临时 IP 网站回跳；协议测试覆盖错误 state、授权码重放、恶意 return、错误 Host、伪造 XFF、撤销网站授权后 WebSocket 断开、临时 IP 续期不越过绝对期限。
9. 实际 TLS 客户端证书握手验证：证书有效但没有资源授权时拒绝，授权后成功，吊销后拒绝；P12 经密码解密成功、错误密码失败、管理员 TOTP 重放拒绝、重签原子吊销旧证书。
10. Pebble 真实 DNS-01：写入 TXT、完成签发、清理 TXT；模拟 DNS 更新失败时原证书保持有效，恢复后续签更新指纹。部署环境的内网订单也达到 `issued`。
11. 浏览器打开策略、IP 集合、审批、网站、证书、通知及网关设置页面；网站编辑保存成功，默认服务器证书预览显示四个网站均匹配。

本轮为开发自测与约定内网环境验收通过。用户在自己的实际业务资源上的验收仍由用户进行；没有将测试结果记为公网生产验收。

## 复现与运维

从项目根目录执行（本地 Compose 凭据由现有启动脚本管理）：

```powershell
docker compose --env-file backend/.local/compose.env -f backend/compose.yaml --profile test run --rm tests go test ./... -count=1 -timeout=12m
docker compose --env-file backend/.local/compose.env -f backend/compose.yaml --profile test run --rm tests go vet ./...
docker compose --env-file backend/.local/compose.env -f backend/compose.yaml --profile test run --rm -e NT_TEST_ACME_FIXTURE=true tests go test ./internal/application -run TestR09InternalACME -count=1 -timeout=6m
npx tsc --noEmit -p tsconfig.app.json
npx vite build --mode backend
```

新建内网协议环境时，先执行 `go run ./fixtures/services init /src/.local/r09-fixtures`（在 Compose tests 服务中），再执行 `docker compose ... --profile r09-fixtures up -d --build fixture-services acme-fixture`。初始化生成随机密码、TSIG 密钥和 30 天服务证书；Linux 下设置为仅容器 UID 10001 可读。不要将 `.local` 纳入版本控制。测试服务证书更新后，须同步更新通知渠道、网站上游和 ACME 账号中的受信任 PEM。

`scripts/seed-r08-r09-browser.js` 是可重复使用的配置种子逻辑，需要在已登录的本机验收管理员页面调用，并从本地配置传入 `password/secret/dnsSecret/ca`；源码不含密钥。不向公网网站执行此脚本。

迁移前已保存公共 schema 备份 `backend/.local/pre-r08-r09.dump`，并检查归档目录。测试证据保存在同目录的 `r08-r09-final-tests.log`、`r08-r09-extra-tests.log`、`r08-r09-race.log`、`r08-command-filter-tests.log`、`r09-acme-final.log`、`r08-r09-final-ui.log`。恢复数据库会覆盖恢复范围内的数据，需按专门的恢复流程操作，不在日常重启中自动恢复。

## 明确的运行边界

- 免安装 SSH 审计和正则过滤不识别脚本内部命令。绑定过滤器时禁止自由终端输入，仅允许显式执行框；不把它描述为远端 shell 沙箱。
- SMTP/企业微信采用至少一次投递。幂等键与租约避免并发重复业务授权，但供应方已接收、响应丢失时仍可能出现重复通知。
- 临时 IP 模式的授权对象是出口 IP，同一 NAT 出口的访问者可能共享通行资格，应根据业务选择登录模式或 mTLS。
- 响应文本替换限制输入 1 MiB、替换后 4 MiB；二进制、SSE、WebSocket 和超限输入不做文本改写。Header 配置禁止覆盖平台身份和转发信任头。
- ACME DNS 接口当前实现 RFC2136 HMAC-SHA256 TSIG；其他 DNS 厂商须接入该接口或后续实现对应适配器。内网没有关闭 ACME 域名验证。
- 目前列表投递与网站日志页面展示最近记录；网站日志最多 200 条、单网站临时 IP 最近 500 条，完整记录保存在数据库中。
