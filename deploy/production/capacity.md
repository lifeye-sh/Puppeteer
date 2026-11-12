# Docker 容量与异常恢复验收

## MP4 渲染

```sh
docker build -t puppeteer/renderer:r17-capacity-preview backend/renderer
node scripts/test-renderer-capacity.mjs
```

脚本新建无网络、无业务挂载的独立容器，2GiB内存、2核CPU、160 PID、256MiB tmpfs，测试后删除该容器。不向当前渲染服务发压测任务。结果写入 `backend/.local/r17-renderer-capacity-result.json`。

已验证16个同时请求中仅1个接收、15个返回429，取消释放槽位；4个完成结果占满保留数量后拒绝新任务，删除结果后恢复；SIGKILL真实编码进程后任务失败并可清理；填满测试tmpfs时提交返回507，健康检查仍200，释放空间后生成有效MP4且临时任务目录清空。修复了编码退出时管道等待可能不结束的问题。容器没有发生OOM。

这些阈值是单渲染实例的资源保护，不是“平台最多支持16人”。当前同时编码上限为1、未清理结果上限为4；API消费者应对429稍后重试、对507排查临时存储，对failed任务清理后按业务策略重试。生产模板tmpfs为768MiB，本次故意缩小到256MiB以触发容量故障；不能据此承诺任意时长/大小的录屏都能转换。

Docker OOMKilled=false仅说明本次容器未被OOM终止，不是持续高负载的内存上界证明。会话和数据库代理的独立结果如下，不由MP4结果推断。

## 会话与数据库基线

2026-11-12在Docker隔离环境通过以下基线；测试进程限制为2 CPU、2 GiB、256 PID：

| 通路 | 负载 | 验收结果 |
| --- | --- | --- |
| HTTPS/WSS SSH | 4用户、16会话，持续121.964秒、896条命令 | 所有命令有响应、16份录屏完成；第17个会话返回409，关闭后可重新连接 |
| 图形协议 | 4会话、持续60.274秒、240轮键盘输入/同步响应 | 并发通路与录屏完成；只读、终止、录屏限额保护继续通过 |
| PostgreSQL | 16客户端、320次查询 | 查询成功；空闲、执行中、COPY及目标断开清理通过 |
| MySQL | 实际MySQL容器及代理协议集成 | TLS模式下应用控制、结果处理回归通过 |

图形负载使用Guacamole协议模拟服务与TCP目标，覆盖RDP/VNC共享传输及录制管线，不是4台Windows真实桌面。数据库使用专用实际数据库容器，未重做已免测的DBeaver桌面验收。这些是本轮可重复的验收基线，不是长期耐久性、最大用户数或生产SLA承诺。

复现入口：`TestR17SustainedSessionCapacity`（不要传`-short`）、`TestR07GraphicsAuditReadonlyAndTermination`（`NT_R17_GRAPHICS_LOAD=1`），位于`backend/internal/application`。使用专用测试数据库设置`NT_TEST_DATABASE_URL`；设置`NT_TEST_TRANSPORT=https`启用受信任测试证书的HTTPS/WSS。数据库协议测试还需`NT_R12_MYSQL_FIXTURE`和`NT_R16_PG_FIXTURE`指向隔离凭据目录。测试数据库不能指向业务库。

浏览器旅程：编译`TestR17BrowserFixture`所在测试程序为`backend/.local/r17-browser.test`，构建`backend/fixtures/r17-browser/Dockerfile`为`puppeteer/browser-drill:r17-preview`，执行`node scripts/test-browser-journey.mjs`。脚本依赖本机隔离测试数据库网络和已构建发布镜像；创建临时CA、Nginx、后端测试进程与渲染容器，向独立浏览器信任库导入CA，不关闭TLS校验。测试结束清理临时网络/容器/证书，截图及MP4保留在`backend/.local/r17-browser-evidence`。该固定测试账号、关闭验证码和测试辅助端点仅存在于显式启用的测试程序，不进入生产server入口。
