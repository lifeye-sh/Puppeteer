# 从 Docker Hub 全新安装

希望自动完成配置、证书和启动时，优先使用[一键安装](../../docs/ONE-CLICK-INSTALL.md)。下文用于手动部署或自定义组件。

命名空间：`cloners`。当前后端/Web版本：`r17-rc11`（schema107）；图形、渲染和WAL镜像沿用 `r17-rc1`。支持平台：`linux/amd64`。

当前仍为候选发布版本。本次验证范围见[r17-rc11发布说明](r17-rc11.md)，历史R17验收不代表对每个后续版本重新完成全量验收。这里使用预构建镜像，不需要在服务器运行npm、Go或镜像构建命令；宿主机仍需Git、Node.js 24、Docker Engine和Compose v2，用于获取配置及运行配置生成器、预检查。

## 镜像

| 组件 | 镜像 |
| --- | --- |
| 后端/API/SSH/审计 | `cloners/puppeteer-server:r17-rc11` |
| Web前端 | `cloners/puppeteer-web:r17-rc11` |
| 浏览器RDP/VNC与共享文件 | `cloners/puppeteer-graphics:r17-rc1` |
| MP4渲染 | `cloners/puppeteer-renderer:r17-rc1` |
| 可选PostgreSQL WAL归档 | `cloners/puppeteer-postgres-wal:r17-rc1` |

核心部署的数据库使用Compose中固定摘要的官方PostgreSQL镜像。只有启用`wal.yaml`时才使用第五个镜像。后端/Web摘要见[dockerhub-r17-rc11.json](dockerhub-r17-rc11.json)，其余组件摘要见[历史组件清单](dockerhub-r17-rc1.json)。下方配置固定后端/Web摘要，避免安装时内容漂移。

## 安装步骤

以下使用Linux Bash。域名和证书源路径必须替换，准备环境与端口要求见[全新部署指南](../../docs/FRESH-INSTALL.md)。

```bash
git clone https://github.com/lifeye-sh/Puppeteer.git
cd Puppeteer
git checkout r17-rc11
sudo node scripts/production-config.mjs /etc/puppeteer https://puppeteer.example.com 443
sudo install -o 101 -g 101 -m 0400 /secure/fullchain.pem /etc/puppeteer/tls/server.crt
sudo install -o 101 -g 101 -m 0400 /secure/privkey.pem /etc/puppeteer/tls/server.key
sudoedit /etc/puppeteer/production.env
```

替换已有同名配置项，添加尚不存在的WAL镜像项，保留生成的其他配置：

```dotenv
PUPPETEER_SERVER_IMAGE=cloners/puppeteer-server@sha256:b3c418c2c858dbb8795d6a08a5101c66d63854b4c1c6641562d6a88a7c77eecc
PUPPETEER_WEB_IMAGE=cloners/puppeteer-web@sha256:8db8d80ad99cf7abf63b6e56d5cea1d942a66b4d2c72eb517d08759d01c27add
PUPPETEER_GRAPHICS_IMAGE=cloners/puppeteer-graphics:r17-rc1
PUPPETEER_RENDERER_IMAGE=cloners/puppeteer-renderer:r17-rc1
PUPPETEER_POSTGRES_WAL_IMAGE=cloners/puppeteer-postgres-wal:r17-rc1
```

默认入口绑定`127.0.0.1`。需要其他电脑访问时，在防火墙限制首次初始化来源的前提下，将`PUPPETEER_BIND`改为服务器指定内网IP或`0.0.0.0`。

核心模式启动：

```bash
dc() {
  sudo docker compose --env-file /etc/puppeteer/production.env -p puppeteer \
    -f deploy/production/compose.yaml "$@"
}
dc pull
sudo node scripts/production-preflight.mjs /etc/puppeteer/production.env
dc up -d --no-build --wait
dc ps
dc exec -T server /app/nt health
```

需要浏览器RDP/VNC、共享文件与MP4时，使用以下命令替代上述核心启动命令：

```bash
sudo modprobe fuse
test -c /dev/fuse
dc() {
  sudo docker compose --env-file /etc/puppeteer/production.env -p puppeteer \
    -f deploy/production/compose.yaml -f deploy/production/services.yaml "$@"
}
dc pull
sudo node scripts/production-preflight.mjs /etc/puppeteer/production.env --services
dc up -d --no-build --wait
dc ps
dc exec -T server /app/nt health
```

使用`--no-build`确保运行下载的发布镜像。不要执行`dc build`覆盖同名版本。Compose文件虽保留源码构建入口，此路径不需要本地`dist/production`或node_modules。

打开`https://puppeteer.example.com/setup`创建初始管理员，再按[全新部署指南](../../docs/FRESH-INSTALL.md)完成用户权限、资产、客户端IP、备份及业务验证。没有默认管理员账号。

原生SSH/数据库代理端口见[协议叠加配置](README.md)，WAL配置与首份归档见[WAL指南](wal.md)。启用额外叠加文件后，后续维护始终使用同一组文件。服务器数据、证书、管理员账号和部署密钥均在首次安装时创建，不包含在这些镜像中。

## 已有部署升级

由一键安装器创建的部署使用[升级脚本](../../docs/UPGRADE.md)。本文的手动安装布局为仓库目录加 `/etc/puppeteer`，没有安装器生成的 `puppeteerctl`，不能直接套用该脚本。手动部署需备份配置、数据库和应用文件，维护时段暂停写入，修改原 `production.env` 中 server/web 的镜像引用，并用原来相同的项目名及全部叠加文件执行 `dc pull`、`dc up -d --no-build --wait`，随后重新创建 web 并检查后端健康。数据库迁移后不能直接退回旧镜像；恢复要求见[恢复切换](cutover.md)。不要重新运行配置生成器覆盖原密钥。
