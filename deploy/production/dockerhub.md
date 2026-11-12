# 从 Docker Hub 全新安装

希望自动完成配置、证书和启动时，优先使用[一键安装](../../docs/ONE-CLICK-INSTALL.md)。下文用于手动部署或自定义组件。

命名空间：`cloners`。版本：`r17-rc1`。支持平台：`linux/amd64`。

该版本为通过R17隔离验收的候选发布镜像。这里使用预构建镜像，不需要在服务器运行npm、Go或镜像构建命令；宿主机仍需Git、Node.js 24、Docker Engine和Compose v2，用于获取配置及运行配置生成器、预检查。

## 镜像

| 组件 | 镜像 |
| --- | --- |
| 后端/API/SSH/审计 | `cloners/puppeteer-server:r17-rc1` |
| Web前端 | `cloners/puppeteer-web:r17-rc1` |
| 浏览器RDP/VNC与共享文件 | `cloners/puppeteer-graphics:r17-rc1` |
| MP4渲染 | `cloners/puppeteer-renderer:r17-rc1` |
| 可选PostgreSQL WAL归档 | `cloners/puppeteer-postgres-wal:r17-rc1` |

核心部署的数据库使用Compose中固定摘要的官方PostgreSQL镜像。只有启用`wal.yaml`时才使用第五个镜像。发布摘要见[dockerhub-r17-rc1.json](dockerhub-r17-rc1.json)，可使用其中的`pinnedReference`固定安装内容。

## 安装步骤

以下使用Linux Bash。域名和证书源路径必须替换，准备环境与端口要求见[全新部署指南](../../docs/FRESH-INSTALL.md)。

```bash
git clone https://github.com/lifeye-sh/Puppeteer.git
cd Puppeteer
sudo node scripts/production-config.mjs /etc/puppeteer https://puppeteer.example.com 443
sudo install -o 101 -g 101 -m 0400 /secure/fullchain.pem /etc/puppeteer/tls/server.crt
sudo install -o 101 -g 101 -m 0400 /secure/privkey.pem /etc/puppeteer/tls/server.key
sudoedit /etc/puppeteer/production.env
```

替换已有同名配置项，添加尚不存在的WAL镜像项，保留生成的其他配置：

```dotenv
PUPPETEER_SERVER_IMAGE=cloners/puppeteer-server:r17-rc1
PUPPETEER_WEB_IMAGE=cloners/puppeteer-web:r17-rc1
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
