# R17 Agent 发布与离线恢复

WAL 可选部署及已验证范围见 [WAL 与时间点恢复](wal.md)。本机加密 WAL 演练不等于异地恢复或正式生产切换。

备份恢复试验不得直接操作业务实例；先在独立环境验证完整链路，再安排维护窗口。Docker模拟环境已覆盖加密远程仓库、WAL/PITR、全应用隔离恢复以及冻结/开放/回退生命周期，见[恢复切换](cutover.md)和[R17验收记录](../../docs/backend/acceptance/R17.md)。真实生产环境的正式开放由部署方执行，不把模拟耗时当作生产恢复承诺。

## Agent 更新发布

使用当前受测镜像内的 `/app/nt-agent-linux-amd64` 和 `/app/nt-agent-release`。签名操作在离线可信机器执行，私钥目录与仓库、公开导出目录必须互不包含；工具拒绝同目录、嵌套路径和符号链接别名导致的嵌套。

```sh
nt-agent-release --init --repo /secure/agent-repo --keys /secure/agent-signing
nt-agent-release --repo /secure/agent-repo --keys /secure/agent-signing \
  --binary ./nt-agent-linux-amd64 --version RELEASE_LABEL --sequence 1
nt-agent-release --repo /secure/agent-repo --export /srv/agent-public-RELEASE_LABEL
```

初始化只执行一次。后续发布不用 root.key，该密钥应离线保存；sequence 严格递增。导出校验连续版本根链的旧/新双签名、当前角色签名、有效期、版本关联和 Agent 长度/摘要，只导出已验证的根链、三个发布元数据文件和目标程序。额外文件不会被复制，已有导出目录不会被覆盖。

根密钥轮换在离线可信机器准备新目录，当前仅支持初始化器创建的单根密钥/阈值1模式：

```sh
nt-agent-release --repo /secure/agent-repo --keys /secure/agent-signing \
  --rotate-root /secure/agent-repo-next --new-keys /secure/agent-signing-next
nt-agent-release --repo /secure/agent-repo-next --export /srv/agent-public-next
```

新根包含旧根和新根两份签名，版本逐次递增，旧根文件全部保留；旧客户端不需要人工替换初始信任根。新私钥目录包含新root.key和原在线发布角色密钥，均为0600，目录0700。旧仓库/密钥保持原样；验证新公开目录后切换站点挂载，后续发布使用新私钥目录。不能用重新初始化替代轮换，不能删除历史根文件。现有仓库过期时先按原在线密钥重新发布有效元数据后再导出；只保留有效的当前信任根，历史根可以已过期但签名链必须完整。

将导出目录复制到生产静态入口机器。在 production.env 设置 `PUPPETEER_AGENT_REPOSITORY` 为该目录，部署时叠加 agents.yaml；私钥目录绝不能挂到 web。该路径的公开文件应允许 Nginx UID101读取，签名私钥仍为0600或更严格。新导出目录默认目录0755、公开文件0444。

```sh
sudo node scripts/production-preflight.mjs /etc/puppeteer/production.env --services --protocols --agents
sudo docker compose --env-file /etc/puppeteer/production.env -p puppeteer \
  -f deploy/production/compose.yaml -f deploy/production/services.yaml \
  -f deploy/production/protocols.yaml -f deploy/production/agents.yaml \
  up -d --no-deps --force-recreate --wait web
```

更新地址为 `https://正式域名/agent-updates/`。Nginx仅允许已知元数据和二进制路径，其余返回404。客户端初始信任根必须通过独立可信渠道分发；不能因为通过HTTPS下载就自动信任新根。客户端配置详见 [Agent部署说明](../../backend/deploy/README.md)。更新元数据24小时过期，需在期限前重新签名发布更高sequence；预检查检测过期，但不替代持续告警。

先灰度一台节点，验证在线、拨号和更新回退后再推广。autoInstall仍默认关闭。R17在Docker内以两个真实Agent进程验证：第一节点排空时原SSH流继续可用且停止新拨号，第二节点不受影响；第一节点更新完成后显式推广第二节点；坏版本启动失败时第一节点回滚，第二节点继续在线。HTTPS/TUF还验证了从旧初始根升级到双签名新根，公开站点不暴露签名私钥。此演练不更改任何业务节点的自动安装开关。

## 离线加密备份

生产镜像的备份/恢复命令支持 NT_DATABASE_URL_FILE、NT_MFA_KEY_FILE、NT_VAULT_KEY_FILE，以及 NT_RESTORE_DATABASE_URL_FILE、NT_RESTORE_ADMIN_URL_FILE。值与对应_FILE同时存在会拒绝执行，避免误用密钥。私有文件保持服务所有者可读；不通过放宽为全用户可读解决权限问题。

1. 在离线机器用 `nt backup keygen --output recovery.key` 生成恢复私钥，输出的是可传入备份命令的 age 公钥。私钥独立保管，不挂载到长期运行的API/图形/网页服务。
2. 维护窗口停止API和图形写入者（server与guacd）。离线备份使用数据库排他锁及连接检查，仍有写入进程时拒绝执行；这不是在线备份命令。
3. 使用与运行版本匹配的镜像、相同Compose环境和叠加文件，执行一次性服务：`docker compose ... run --rm --no-deps --entrypoint /app/nt server backup create --output /var/lib/next-terminal/backups/NEW_NAME.ntbackup.age --recipient AGE_PUBLIC_RECIPIENT --scope full`。
4. 无论备份成功与否都应恢复原服务、刷新web并检查健康。不要把“备份成功”当作“服务已恢复”。
5. 将加密归档另存并核对SHA-256。full包含数据库内录屏/共享存储对象和恢复密钥；不包含远端资产主机文件、旧备份、外部WAL仓库和可重建的MP4缓存。不能把此归档当作整个宿主机镜像。

## 隔离恢复准备

使用独立PG18容器或服务器，新建名称以 `nt_restore_` 开头的空数据库，由专用非超级用户拥有；恢复角色不得指向源数据库。恢复CLI仅连接该独立网络，挂载可信加密归档、离线恢复私钥和私有工作目录。

```sh
# NT_RESTORE_DATABASE_URL_FILE 指向上述独立目标的私有连接文件。
nt restore prepare --archive /backup.ntbackup.age \
  --identity-file /offline/recovery.key --workspace /private/restore-work --trust-source
```

`--trust-source` 只用于已确认来源的归档，因为数据库备份可能包含源定义SQL。CLI校验加密归档、schema、实例ID、恢复密钥与对象，拒绝超级用户目标、非PG18及非空数据库；成功后隔离旧会话、任务和设备状态，输出 `prepared`、`switched:false`。这一步不会替换当前实例。

恢复私钥和解密后的工作目录必须保持私有，使用其所有者运行离线工具。源实例应继续保持原实例ID和数据；目标恢复后按业务恢复手册重新核验授权、凭据、录屏、共享存储及设备隔离，再决定是否切换。已有新写入时不得直接换旧镜像声称无损回退。
