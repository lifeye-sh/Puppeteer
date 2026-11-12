# WAL 定时维护

任务运行于部署主机，调用已运行的 PostgreSQL 容器；不把 Docker socket 挂载给网页、后端或图形服务。需要 Node.js 24、Docker CLI，以及已完成 stanza-create/full/check 的 WAL 部署。

将项目部署到 `/opt/puppeteer`（或修改 service 的脚本绝对路径）。创建只有管理员可读的 `/etc/puppeteer/wal-maintenance.env`：

```ini
WAL_CONTAINER=puppeteer-postgres-1
WAL_STATE_DIR=/var/lib/puppeteer-wal-maintenance
WAL_WEBHOOK_FILE=/etc/puppeteer/wal-webhook-url
```

容器名称按实际 Compose project 确认。不同容器使用不同状态目录。webhook-url 文件仅存放 HTTPS 通知接收地址，可使用 URL 中的私有随机令牌；不要在聊天或日志打印。设置文件0600、状态目录0700，均由运行任务的管理员拥有。接收器使用可验证证书，返回2xx才确认投递成功，不跟随重定向。可通过 Node 的 NODE_EXTRA_CA_CERTS 配置内网CA；不得禁用TLS校验。

复制本目录 `.service` 和 `.timer` 到 `/etc/systemd/system/` 后运行：

```sh
systemctl daemon-reload
systemctl start puppeteer-wal@check.service
systemctl enable --now puppeteer-wal-check.timer puppeteer-wal-full.timer puppeteer-wal-diff.timer
systemctl list-timers 'puppeteer-wal-*'
```

check每5分钟执行；周日UTC02:00执行full，周一至周六UTC02:00执行diff。两种备份时间均明确使用UTC，不受主机夏令时影响。安装模板本身不会启用调度，应先执行单次check确认连接与通知。

每次任务记录状态并发送成功/失败事件，只包含事件ID、类型、操作、状态与起止时间，不转发子进程原始输出、数据库地址或密钥。非2xx、超时和网络异常保留待发送队列，之后同状态目录的任务按顺序重试。接收方按事件ID幂等：接收成功但本地确认前进程中断时可能重复投递，保证的是至少一次而非恰好一次。

`state.json` 记录 lastSuccess、lastRun 和 pending；备份失败不推进成功时间，任务失败或仍有未送达通知时返回非零。通知接收服务自身故障需由主机监控任务退出码与 pending 数量，不能依赖同一个失联接收器报告自身故障。

同一状态目录通过原子创建running.lock防止重叠。普通异常释放锁；强制终止可能留锁，须先确认没有仍在执行的pgBackRest进程再移除该空目录。命令等待上限15分钟，超时不代表数据库内任务已被终止；不要直接删锁后并发重跑。对于大型备份应调整时间限制并做容量验收。

Docker模拟复现：`node backend/test/deployment/remote-wal.mjs --maintenance`。该测试以加速时间间隔触发真实任务，使用Docker通知接收器并模拟503；不声称Docker Desktop运行了systemd。本批验证任务行为与重试，Linux计时器模板安装后的实际触发时间由部署主机检查。
