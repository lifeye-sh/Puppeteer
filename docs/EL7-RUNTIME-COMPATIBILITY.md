# EL7 容器写入兼容修复

适用此次已确认的环境：EL7 x86_64、Linux 3.10、Docker CE 26.1.4、containerd.io 1.6.33、runc 1.1.12 动态链接 libseccomp 2.3.1。PostgreSQL 在初始化时写 `postmaster.pid` / `pg_wal/xlogtemp` 返回 `Operation not permitted`，且不是 OOM。

当前固定 PostgreSQL 镜像使用 musl 1.2.6。旧 libseccomp 对新系统调用的识别不足可能使 Docker 默认策略返回 EPERM，阻止 libc 正常走“不支持”的兼容回退。本地隔离测试已复现相同错误及 ENOSYS 回退成功；这不代替目标 EL7 内核的实际验证。

## 修复范围

`scripts/repair-el7-libseccomp.sh` 使用在 CentOS 7 / glibc 2.17 环境构建的官方 libseccomp 2.6.1，安装到 `/opt/puppeteer-compat/libseccomp-2.6.1`。不覆盖 RPM 管理的 `/lib64` 文件，通过 `/etc/ld.so.conf.d/00-puppeteer-libseccomp.conf` 和 `ldconfig` 激活。

**链接选择影响整台宿主机后续启动且动态链接 libseccomp 的程序，不仅是 Puppeteer。** 已有进程及容器不会自动重启；不关闭 seccomp，不增加 privileged，不修改项目凭据或卷。存在自定义运行时/库配置时应先核查，脚本版本与架构检查不通过会停止。

## 操作

在 root shell 中下载主分支脚本并检查：

```bash
curl -fsSL https://raw.githubusercontent.com/lifeye-sh/Puppeteer/main/scripts/repair-el7-libseccomp.sh -o repair-el7-libseccomp.sh
bash repair-el7-libseccomp.sh --check
bash repair-el7-libseccomp.sh --apply
```

`--apply` 下载固定 SHA-256 的包，检查库文件和 runc ABI，验证 `pwritev2` 解析为 x86_64 调用号 328。激活后使用原固定 PostgreSQL 镜像，在无网络、无业务挂载的临时内存目录执行真实 initdb。若新激活后的验证失败，撤回本次链接配置；包和日志保留。操作通过 flock 互斥。

对本次 `/opt/puppeteer`、默认项目名 `puppeteer`、beta0.12 首次安装中断，验证通过后继续：

```bash
bash repair-el7-libseccomp.sh --finish-install
```

此步骤沿用原 `puppeteerctl` 启动服务，检查健康，再从固定 beta0.12 服务镜像取出官方接管助手，完成宿主机部署执行器接管。只有服务及执行器健康后才写入 `installed-version`。它不是已有部署的升级入口，不生成新凭据、不删除卷；若安装已经标记完成，使用原 `puppeteerctl` 管理。

## 撤销

```bash
bash repair-el7-libseccomp.sh --rollback
```

仅移走该脚本管理的链接配置并重建缓存，保留包与日志。不停止现有容器；之后新启动的进程恢复原系统库选择，原兼容故障也可能重新出现。不要删除数据卷作为回退手段。

## 可追溯性

- 上游源码：[libseccomp v2.6.1](https://github.com/seccomp/libseccomp/releases/tag/v2.6.1)。源码 SHA-256：`501f66c667225d53791b97e1d7cf85ab764c297d04881f60f38f451c4b0ee1be`。
- 构建配方：[EL7 Dockerfile](../scripts/compat/el7-libseccomp.Dockerfile)，固定 CentOS 7 镜像摘要。将官方源码压缩包放入构建上下文后运行；配方执行 `make check`。
- 发布包附带 LGPL 许可证、完整对应源码及各文件校验清单，作为 beta0.12 的额外附件，不替换原发布镜像及历史安装脚本附件。
- 本地上游回归：7928 项通过，86 项跳过（包括未安装 Valgrind、未选择的 live 测试），0 失败。
- 当前本地 Docker 的内核不是 EL7 3.10；真实宿主运行结果以目标机器 `--apply` 的 initdb 探测为准。

已在独立 CentOS 7 用户态使用 Docker 官方相同 containerd.io RPM 验证 runc 1.1.12 的实际 ABI 加载、重复启用、显式回退及探测失败回退，`rpm -V libseccomp` 确认原系统库文件未改变。该脚本回归中的 Docker CLI 为测试桩；目标主机真实 initdb 探测由 `--apply` 执行，不能把此项视为远端修复已完成。
