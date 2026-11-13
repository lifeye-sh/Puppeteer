# Web 入口与内部服务边界

默认允许通过任意 HTTPS IP、端口或域名访问 Web；生产配置启用 `NT_ALLOW_REQUEST_ORIGIN=true`，API、终端和 AI WebSocket 接受与当前 Host 同源的浏览器请求。浏览器请求仍须满足登录、权限、会话票据和来源校验。不会接受任意网站的跨站认证请求。

可设置 `PUPPETEER_ALLOW_REQUEST_ORIGIN=false` 恢复仅允许 `PUPPETEER_ORIGIN` 指定来源的旧行为。此开关控制浏览器来源校验；若要连页面加载也限制，请使用下面的 Web 策略。

负载均衡必须保留原始 `Host`（包括非默认端口），转发 WebSocket Upgrade，后端使用 HTTPS 8443。域名证书可在负载均衡终止 TLS；LB 到 Web 应使用 HTTPS 并验证配置的上游证书。当前生产 Web 使用 Secure Cookie，不支持直接降级为 HTTP 登录。

Web 与 server 通过 Docker internal control 网络连接，server 的 8089 不映射宿主端口，数据库也不映射公网。Web 清除外部伪造的转发身份头。此边界依赖单机 Docker 网络隔离，内部 HTTP **不等于 mTLS**；不要让不受信任容器加入 control 网络。跨主机部署应另行配置内部 TLS/mTLS。

## 可选外部访问控制

在部署目录复制以下文件并按需编辑（首次启用时示例仍允许所有访问）：

```sh
cp web-access-http.example.conf web-access-http.conf
cp web-access-server.example.conf web-access-server.conf
cp web-access.example.yaml web-access.yaml
```

在 puppeteerctl 的 Compose 参数中追加 `-f /opt/puppeteer/web-access.yaml`，保留现有所有参数，包括图形 AppArmor 配置。

- 限制入口 IP/域名：将 `map $http_host` 的 `default 1` 改为 `default 0`，列出允许的域名或 IP，带非默认外部端口。
- 限制来源地址：将 `geo` 的 `default 1` 改为 `default 0`，列出允许直连 Web 的 IP/CIDR。LB 场景这里是负载均衡后端地址，最终用户来源限制应在 LB/WAF 配置。不要无条件信任 X-Forwarded-For。
- 保留所有入口开放：两个 default 都为 1，或不加载此 overlay。
- OIDC issuer/回调和 Passkey RP 域名仍需独立配置，入口开放不改变这些协议的身份绑定。

应用前先运行使用同样 Compose 配置的临时 Web 容器校验：

```sh
sudo /opt/puppeteer/puppeteerctl run --rm --no-deps web nginx -t
sudo /opt/puppeteer/puppeteerctl up -d --no-deps --no-build --wait web
```

拒绝规则对静态资源、API、WebSocket 建连统一生效。更改规则不主动终止已经建立的 WebSocket；紧急撤权使用平台的终止会话或重建 Web 容器。容器本地健康检查保留访问权。

## 2026-11-13 验证

- Go 来源校验及 R07 图形连接回归通过。
- 隔离生产容器中新域名同源 POST 进入登录校验；跨站 POST 返回 origin_denied。
- Nginx 配置校验通过：允许 Host 返回 200；未允许 Host 的页面、API、WebSocket 握手返回 403；来源 IP 拒绝返回 403；本地健康检查通过。
- 后端管理端口 8089 无宿主映射。已有可选协议监听器保持独立配置。
- Docker Hub 后端镜像：`cloners/puppeteer-server:r17-rc6`，摘要 `sha256:7911a55a53dd9cc3dfb2297f39c320e1024f48d7ffa8c3924cb6e9e26d0e746a`。

以上为隔离部署验收，公网负载均衡尚需部署后验证 Host 保留、证书和 WebSocket 转发。
