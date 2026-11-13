# 无 TLS 数据库隔离测试

仅使用一次性容器、测试密码及内存存储，不发布宿主端口，不要用于生产。

`docker compose -p puppeteer-no-tls -f backend/test/database-no-tls/compose.yaml up -d`

确认两个服务初始化完成后，在同一测试网络中的 Go 容器运行：

```sh
NT_TEST_NO_TLS=1 go test ./internal/platform/mysqlproxy ./internal/platform/pgproxy -count=1
NT_TEST_NO_TLS=1 go test ./internal/application -run TestDatabaseTLSModePersistence -count=1
```

应用测试另需项目通用的 NT_TEST_DATABASE_URL（独立验收数据库，不得指向生产）。测试检查真实明文目标会话、查询、只读拒写、DML 回滚及默认 TLS 不降级。

结束后使用相同 Compose 文件 `down`，仅清除此测试项目。内存数据库随容器停止销毁。
