# R17 Docker 模拟远程 WAL 验收

按用户确认，本轮以 Docker 模拟生产环境验收，不等待真实异地服务器。

```powershell
docker build -t puppeteer/postgres:r17-wal-preview -f deploy/production/postgres.Dockerfile deploy/production
docker build -t puppeteer/wal-drill:r17-preview backend/fixtures/r17-remote-wal
node backend/test/deployment/remote-wal.mjs
# 加速触发维护任务、通知503和持久化重试
node backend/test/deployment/remote-wal.mjs --maintenance
```

脚本创建随机前缀的独立 internal 网络、源库/仓库/恢复容器和各自数据卷，无宿主服务端口。仅仓库可以挂载仓库卷，恢复容器不能访问源库卷。临时 SSH 客户端密钥、仓库主机密钥和加密密钥在独立卷生成，不使用业务凭据。SSH 固定主机密钥，禁用密码和 root 登录；测试镜像不能替换生产数据库镜像。

验证顺序：

1. 空库初始化，远程加密全量备份及 WAL 归档。
2. 创建命名恢复点，写入恢复点之后的事务。
3. 停止仓库，继续写源库并触发 WAL 切换；断言归档失败计数增长、业务表仍可读写。
4. 重启仓库，补传保留的 WAL，执行归档检查及差异备份。
5. 停止源库，从远程仓库恢复到另一空卷；明确选择目标之前的 full 备份，避免使用目标之后的 diff。
6. 断言仅目标前事务存在、恢复完成、恢复库归档关闭。

`--maintenance` 增加独立Docker通知接收器（仅本机随机端口）、两次加速定时触发、同状态目录并发拒绝、仓库故障时成功时间不变、通知503后的事件持久化，以及下一次执行按顺序补投失败/成功事件。该模式结果为 `backend/.local/r17-maintenance-result.json`，临时状态目录和接收器一并清理。仅测试允许loopback HTTP，正式维护任务要求HTTPS。

结束或异常时清理本脚本创建的容器、卷和网络，结果保存在 `backend/.local/r17-remote-wal-result.json`。强制终止 Node 或 Docker 引擎故障可能中断 finally，须按日志中的随机前缀核对遗留资源，不能清理整个 Docker。

这是协议真实、数据独立的 Docker 模拟故障域。多容器仍共用物理宿主机，不证明机房级耐久性，但符合本次约定的开发验收范围。恢复耗时为小样本自动步骤测量，不是生产 SLA。周期调度、通知送达、容量压力和整个业务切换须另行记录，不能由此脚本推断通过。
