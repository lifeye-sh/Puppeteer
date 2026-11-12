# R06.6 从资产安装 Agent

新增 Agent 网关页面“选择资产安装”：选择启用的 SSH 资产，填写可达后端地址和网络范围，提交后显示连接、预检、登记、上传安装、等待上线及结果。资产凭据和连接路径沿用原配置，不会将所选资产自动改为通过自身 Agent 连接。

后端接口 GET/POST /api/admin/agent-installations；管理员权限及写请求 Origin 校验；任务存储在 016_agent_installations.sql。预检要求 Linux amd64、systemd、root/免密 sudo、无既有安装。固定脚本与配置数据分离，私密配置通过 SSH stdin 上传，程序 SHA256 校验；凭据使用与安装结果记录操作审计。

开发验证：真实 SSH 传输测试覆盖私有配置上传、真实 Agent WSS/yamux 上线确认、重复任务拒绝、失败安装凭据撤销、匿名/错误 Origin/未显式允许 HTTP 拒绝、秘密不出现在任务查询中；shell 语法和归档权限检查。测试远端 systemd 执行被模拟，不能等同于所有发行版实机安装验证。当前本机 SSH 测试容器没有运行中的 systemd，应被预检拒绝。

用户验收：在 Agent 网关点击“选择资产安装”，选择已核验主机密钥的 Linux systemd 测试资产，填写该资产可访问的后端地址与 CIDR/端口。观察进度到“成功”，然后在监控确认设备上线；再次安装同一机器应被既有目录/服务检查拒绝。无需复制注册令牌或手动上传二进制。

运行限制和清理失败安装说明见 [部署手册](../../../backend/deploy/README.md)。升级前备份 backend/.local/r06-before-asset-installer.dump。

浏览器实测（2026-10-28）：从下拉框选择“R04 本地 SSH”，填写开发后端地址与网络范围，任务 15d21c2a-9d1f-4693-b695-75375299727e 正确停在 preflight 并显示环境不符合条件，未生成设备身份；未对业务资产“测试”安装。运行接口确认 r06.6 / schemaVersion=16。TypeScript、Vite backend 构建和 go vet 通过。

最终全量 Go 回归通过（application 244.447 秒），日志 backend/.local/agent-install-regression.log；部署健康检查 /readyz 返回 ready。真实 Linux systemd 主机安装仍需按上述步骤验收。
