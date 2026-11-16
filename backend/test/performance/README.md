# 隔离混合负载验收

从仓库根目录运行，需要 Python 3.10+、Docker Linux 引擎和可创建 cgroup 的权限。当前执行脚本要求 `cgroupfs` 驱动；Docker Desktop Linux/cgroup v1 已实测。包含 cgroup v2 路径，但尚未在当前环境验收；不自动修改 systemd slice 或 Docker 全局资源设置。

## 构建与运行

先构建当前源码的专用负载程序及渲染器，并准备依赖镜像：

```sh
docker build -f backend/test/performance/soak.Dockerfile -t puppeteer/soak:optimization backend
docker build -t puppeteer/renderer:optimization-round4 backend/renderer
docker pull postgres:18-alpine
docker pull cloners/puppeteer-graphics:r17-rc13
docker pull python:3.13-alpine

# 先跑短测，完整通过后才开始 10 小时测试。
python scripts/optimization-soak.py --seconds 600
python scripts/optimization-soak.py --seconds 36000
```

可以用 `--load-image`、`--renderer-image`、`--graphics-image`、`--helper-image` 指定已有的对应镜像。图形镜像需提供 `/opt/guacamole/sbin/guacd`，辅助镜像需有 Python 3 和 `sleep`。运行阶段只使用本地镜像，不自动拉取；报告保存镜像 ID。

## 覆盖与限制

- 一次性内部网络，没有宿主发布端口；独立空数据库与卷，不使用业务账号或真实桌面。
- 测试应用、PostgreSQL、guacd、渲染器共享父 cgroup：合计 2 CPU、4 GiB、无额外 swap。各容器另有限额；辅助容器和 Docker/操作系统开销不包含在内。
- 生产应用处理器和后台工作进程在测试进程内运行，轻量档 8 连接池；每分钟 SSH 命令、测试 VNC 画面、两类 MP4 下载，后台每 200 ms 查询数据库，每 60 轮做一次加密完整备份。
- 每 10 秒采集父 cgroup 内存/CPU 节流、OOM、容器运行和健康状态；每轮记录 Go 堆、协程数、连接池等待/取消计数。父 cgroup 内存包含缓存，与 Docker stats 排除部分缓存的口径不同。
- 出现负载失败、依赖退出/不健康、重启、OOM、限额漂移或超时即失败；采样间隔超过 90 秒或系统时间倒退也会拒绝验收，避免休眠/阻塞后误判连续运行。完成标记还要求负载提供实际持续时间、成功循环与备份证据。

结果保存在 `backend/.local/puppeteer-soak-<id>/`：`state.json` 为状态，`samples.jsonl` 为采样，结束时写入各服务日志。运行期间 `state.json` 每分钟更新且 `complete=false`；只有成功结束才为 `true`。短测通过不能替代 10 小时验收，VNC 测试服务不能替代 Windows RDP，容器合计限额不能替代整机验收。

脚本退出时只删除本次唯一名称的容器、网络、卷和空 cgroup，保留报告。Ctrl+C 也会进入清理并标记未完成；强制终止 Python、关机或 Docker 重启可能留下测试资源，此时不能判定通过。清理时必须按报告中的完整项目名核对，不使用全局 prune。

判定长时间结果时，还需比较开始稳定后与结束阶段的堆、协程、连接池和每轮耗时，区分录屏/备份增长带来的数据库与文件缓存增长；不能仅凭没有 OOM 就声明没有泄漏或性能达标。


## 资源报告与泄漏筛查

每 10 秒额外记录各容器的匿名内存、文件缓存、CPU 累计时间与 PID 数；每分钟采样渲染器进程、僵尸进程与可读文件描述符。应用每轮记录 Go 堆占用、在用堆、保留堆、对象数、GC 次数、协程及连接池数据。CPU 百分比以一个核心为 100%，不能与合计 2 核配额百分比混用。

运行期间每 5 分钟、结束时自动生成 `resource-report.html`（图表和表格）、`resource-report.json` 与 `resources.csv`。也可在运行中或结束后重新生成：

```sh
python scripts/soak_report.py backend/.local/puppeteer-soak-具体编号
```

筛查跳过前 10 分钟预热，只有至少两个覆盖 40 分钟的小时窗口才比较匿名内存与 Go 堆低位、协程、PID 的变化。匿名内存低位超过起始窗口的 1.35 倍加 32 MiB、Go 堆超过 1.5 倍加 16 MiB、协程中位增加超过 5、PID 低位增加超过 8，或多次观察到僵尸进程时标记需复核。这些阈值是筛查规则，不是无泄漏证明或通用生产标准；还应审阅小时趋势、GC/对象数和工作负载增长。文件缓存单列，避免把数据库与备份缓存增长直接认定为泄漏。

`state.json.complete` 仅表示负载运行完整结束；发布验收还必须审阅资源报告。增长告警、时长不足或报告生成错误不能当作资源验收通过。

内存同时保存采样值和 cgroup 内核高水位，避免 10 秒采样漏掉转换尖峰。高水位包含启动峰值和缓存，不能单独用于判断泄漏。


复测补充：长测连接由每轮显式关闭，不再把 WebSocket 缓冲区保留在父测试的 cleanup 闭包中；SSH 测试目标仅保留尚未关闭的连接。`TestOptimizationCleanupRetention` 可通过 `NT_SOAK_RETENTION=1` 和 `NT_SOAK_PROFILE_DIR` 显式启用，隔离比较 600 次连接后的堆保留并输出 pprof。该诊断会执行 GC，不与长测的自然 GC 指标混用。

渲染器进程探针记录 PID 和启动时间，观察到僵尸进程后每 100 ms 复查、最长 2 秒，区分短暂退出状态与同一进程持续残留。负载结束后还执行最终空闲探针；最终仍存在未回收进程则运行失败。报告保留瞬时值及复查值，不以提高 PID 上限掩盖问题。另增加连续多小时堆低位增长筛查，避免较小但持续的增长被绝对阈值忽略。
