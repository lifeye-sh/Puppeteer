# Docker 恢复切换验收

本轮按用户确认在Docker模拟环境验收，不要求真实服务器。使用独立Compose项目、临时数据库与持久卷；不对业务库执行恢复。

## 复现

先在Go构建环境把当前代码的 `./internal/application` 编译为Linux测试程序 `backend/.local/r17-pitr-application.test`，准备当前受测server与graphics镜像。仓库根目录PowerShell执行：

```powershell
./scripts/test-r11-restore.ps1 `
  -TestBinary backend/.local/r17-pitr-application.test `
  -ServerImage puppeteer/server:r17-operations-preview `
  -GraphicsImage puppeteer/graphics:r17-services-preview `
  -ProjectName nt-r17-restore-drill -Port 18092
```

脚本拒绝已有同名演练容器，且在拒绝前不会重写其连接配置。项目名限定为演练格式，默认结束后删除自身Compose容器和卷。日志/离线恢复材料仍留在私有 `.local` 目录供排查，不挂载到业务服务。`-Keep`仅用于保留故障现场。

流程覆盖安全归档、独立受限恢复库、API/图形双服务冻结就绪、开放前回退、继续恢复、开放业务、旧会话失效、新登录、恢复记录、共享文件与秘密校验，以及开放后禁止自动回退且服务保持就绪。

本次使用生产构建的镜像，但演练内部为development配置、HTTP loopback及生成的测试凭据；隔离恢复/回退状态机通过，不等同于生产HTTPS入口完整浏览器旅程通过。测试程序和镜像必须来自同一schema，不得拿旧测试程序验证新镜像。

## Agent 灰度与回滚

当前代码 `TestR06ManagedUpdateAndUnhealthyRollback` 在Docker运行，真实编译并启动Linux Agent supervisor，注册受限设备，发布签名版本并由管理API触发安装；健康握手成功后发布立即退出的不健康版本，断言rolled_back和原ELF程序恢复。`TestR06AgentChannelEpochHeartbeatAndRevoke`验证重连epoch、旧连接关闭、心跳与撤销；`TestR06SSHAndSFTPOverAgent`验证真实协议转发。

这些测试使用隔离数据库和明确允许的测试HTTP入口。R17另有HTTPS/TUF公开发布链验证，两者分别提供证据；未把它们描述成同一次生产HTTPS灰度。最终复验扩展为两个真实Agent进程，验证活跃SSH会话排空、逐节点显式推广及坏版本回滚；根密钥轮换通过离线双签名校验和实际HTTPS旧信任根更新，见[运维步骤](operations.md)及R17最终记录。
