# MP4 后台转换验收

日期：2026-10-30。已部署 schema 31，API 和独立转换服务健康。

已通过：

- 真实终端和 Guacamole 图形录屏编码；ffprobe 确认 H.264、1280×720、yuv420p，并检查视频包含实际渲染内容。
- PostgreSQL 集成测试：访问权限、重复任务合并、下载内容、摘要损坏检测与重试、取消、租约恢复、缓存到期和访问记录删除联动。
- 真实转换服务集成：提交请求结束后由 Worker 完成转换，并通过授权 API 下载成品。
- `go test ./internal/application -run 'TestMP4|TestRecording' -count=1` 与 `go vet ./...`。
- 前端 TypeScript 检查通过；此前 Vite 构建通过。

浏览器操作验收通过：管理员在访问记录点击生成后离开页面，返回已显示下载入口；下载返回 HTTP 200、video/mp4、附件文件名及有效 ftyp 标识，测试文件 8,886 字节。小文件大小改为 B/KiB 显示。完整后端回归曾因磁盘空间不足中断，不计为通过；此次重新执行的是 MP4 和录屏相关测试。

相关日志保存在私有 `backend/.local/mp4-real-integration.log`、`mp4-renderer-tests.log`、`mp4-final-checks.log`、`mp4-deploy.log`。

环境恢复：磁盘不足导致数据库写入阻塞；释放空间并恢复 Docker/PostgreSQL 后，验证码接口返回 HTTP 200。前端开发服务已重新启动。本项目依赖通过 `node_modules` 目录联接指向 C 盘缓存，位置记录在私有 `backend/.local/backup-locations.txt`；数据库备份和历史构建文件的迁移位置也记录于该文件。
