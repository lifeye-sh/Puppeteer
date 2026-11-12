# 生产入口的客户端 IP

当前模板把 Nginx 作为直接面向客户端的 HTTPS 入口。它覆盖 X-Forwarded-For、X-Real-IP、X-Client-IP 和 X-Forwarded-Proto，并删除 Forwarded、X-Forwarded-Host、X-Forwarded-Port。不能直接把此模板放在另一个负载均衡器后面并假定仍能获得最终用户 IP：此时记录的是负载均衡器地址。

## 应用侧配置

新实例默认 `ip-extractor=direct`，因此在完成配置前，应用审计记录的是 Nginx 的连接地址。通过管理员平台设置选择 `x-forwarded-for`，信任列表只填写 Nginx 在 control 网络的固定地址 `/32`（IPv6 使用 `/128`）。不信任整个私有地址范围、Docker 默认网段或 `0.0.0.0/0`。不要选择模板未覆盖的自定义请求头。

部署方应在 Compose override 中为 control 网络分配与本机及 VPN 不冲突的专用子网，并为 web 指定固定地址，再设置应用信任列表。例如下面地址仅为示例，必须先检查部署环境是否冲突：

```yaml
services:
  web:
    networks:
      control:
        ipv4_address: 172.30.90.10
      edge: {}
networks:
  control:
    ipam:
      config:
        - subnet: 172.30.90.0/24
```

上述例子的信任列表为 `172.30.90.10/32`。所有后续 Compose 操作都必须包含同一 override，避免重建后地址改变。不要仅信任当前动态分配地址并把它作为长期配置。保存后检查配置版本已应用，再核对一次登录审计的来源地址。未信任的直连请求以及无效地址链仍回退到实际连接方。

若前面还有负载均衡器，需单独配置 Nginx real_ip 模块，只信任明确的负载均衡器地址，并限制入口网络；完成伪造链验证后才能上线。当前交付未自动开启此多层代理配置。Docker Desktop 的端口转发还可能把地址转换为宿主机网关地址，不能用它证明 Linux 生产主机能保留外部客户端地址。

## 自动验收与部署核对

运行 `node backend/test/deployment/proxy-headers.mjs <测试TLS目录>`，会创建临时隔离上游与真实 Nginx，验证正常请求和伪造 IP/协议/转发头。测试校验 TLS，并自动清理自己创建的容器和网络；只在本机随机端口监听，不读取数据库或应用密钥。需要本地已有 `node:24.13.0-alpine` 和 `puppeteer/web:r17-core-preview` 测试镜像。

Docker浏览器验收为Nginx分配固定地址，仅信任其/32；HTTPS登录后通过WSS建立SSH会话，结束后从实际会话审计核对客户端IP为127.0.0.1。配置版本和无效链/未信任地址另由`TestR16ClientIPSettingsAndTrust`覆盖。结合上面的真实Nginx伪造头测试，已完成本轮单层入口范围。

真实部署时仍要代入实际网络地址并核对最终审计；多层负载均衡器不是当前模板默认拓扑，启用时需按上文单独配置。
