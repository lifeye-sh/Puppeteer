package application

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"github.com/go-mysql-org/go-mysql/client"
	"github.com/go-mysql-org/go-mysql/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"net"
	"net/http"
	"next-terminal/backend/internal/modules/database"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/mysqlproxy"
	"next-terminal/backend/internal/platform/pgproxy"
	"strconv"
	"time"
)

func (a *App) testDatabaseAsset(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if !resource.ValidID(id) {
		return fault.Invalid
	}
	var asset database.Asset
	var actor string
	if e := a.securityTx(r, func(c context.Context, u *identity.User) error {
		if e := a.allowed(c, u, "identity.manage"); e != nil {
			return e
		}
		actor = u.ID
		var e error
		asset, e = database.New(a.db).Asset(c, id)
		return e
	}); e != nil {
		return e
	}
	results := []map[string]any{}
	for _, profile := range []struct {
		name, id string
		readonly bool
	}{{"readonly", asset.ReadonlyCredentialID, true}, {"workorder", asset.WriteCredentialID, false}, {"native-dml", asset.NativeWriteCredentialID, false}} {
		if profile.id == "" {
			continue
		}
		var target mysqlproxy.Target
		if e := a.securityTx(r, func(c context.Context, u *identity.User) error {
			if e := a.allowed(c, u, "identity.manage"); e != nil {
				return e
			}
			current, e := database.New(a.db).Asset(c, id)
			if e != nil {
				return e
			}
			if current.Version != asset.Version {
				return fault.Conflict
			}
			credential, secret, e := a.vault.DatabaseAuth(c, profile.id, u.ID, "database_connection_test")
			if e != nil {
				return e
			}
			target = mysqlproxy.Target{Address: net.JoinHostPort(asset.Host, strconv.Itoa(asset.Port)), User: credential.Username, Password: secret.Password, Database: asset.Database, ServerName: asset.TLSServerName, CA: asset.TLSCA, Timeout: time.Duration(asset.ConnectTimeout) * time.Second}
			return nil
		}); e != nil {
			return e
		}
		start := time.Now()
		var e error
		if asset.Engine == "postgresql" {
			connection, err := pgproxy.ConnectTarget(r.Context(), pgproxy.Target{Address: target.Address, User: target.User, Password: target.Password, Database: target.Database, ServerName: target.ServerName, CA: target.CA, Timeout: target.Timeout})
			e = err
			if connection != nil {
				_ = connection.Close(context.Background())
			}
		} else {
			connect := func() (*client.Conn, error) {
				if profile.name == "native-dml" {
					return mysqlproxy.ConnectDMLTarget(r.Context(), target)
				}
				return mysqlproxy.ConnectTarget(r.Context(), target, profile.readonly)
			}
			connection, err := connect()
			e = err
			if e == nil {
				stop := context.AfterFunc(r.Context(), func() { connection.Close() })
				_, e = connection.Execute("SELECT 1")
				stop()
				connection.Close()
			}
		}
		target.Password = ""
		message := "TLS、身份与目标权限校验通过"
		if e != nil {
			message = databaseDiagnosticMessage(e)
		}
		results = append(results, map[string]any{"profile": profile.name, "ok": e == nil, "message": message, "durationMs": time.Since(start).Milliseconds()})
	}
	encoded, _ := json.Marshal(results)
	if e := a.audit.Detail(r.Context(), actor, "database.asset_test", requestID(r), sourceIP(r), map[string]string{"assetId": id, "results": string(encoded)}); e != nil {
		return e
	}
	writeJSON(w, 200, map[string]any{"items": results})
	return nil
}
func databaseDiagnosticMessage(e error) string {
	var host x509.HostnameError
	var trust x509.UnknownAuthorityError
	var certificate x509.CertificateInvalidError
	var dns *net.DNSError
	var operation *net.OpError
	var target *mysql.MyError
	var pgTarget *pgconn.PgError
	var pgConnect *pgconn.ConnectError
	switch {
	case errors.As(e, &host):
		return "TLS 服务器名称不匹配，请核对证书 SAN 与资产 TLS 名称"
	case errors.As(e, &trust):
		return "目标证书不受信任，请配置正确的 CA 证书链"
	case errors.As(e, &certificate):
		return "目标证书无效或已到期，请更新证书"
	case errors.Is(e, mysqlproxy.ErrDMLPrivileges):
		return "原生 DML 凭据仅允许当前数据库的 SELECT / INSERT / UPDATE / DELETE；不得含 DDL、管理、角色或其他数据库权限"
	case errors.Is(e, pgproxy.ErrPrivileges):
		return "PostgreSQL 只读角色权限过大：请移除管理/角色继承、对象所有权、CREATE/TEMP、写入、序列修改及自定义函数执行权限"
	case errors.As(e, &pgTarget):
		return "目标数据库拒绝请求（PostgreSQL " + pgTarget.Code + "），请检查 SCRAM 凭据、数据库及权限"
	case errors.As(e, &dns):
		return "目标主机无法解析，请检查 DNS 或主机地址"
	case errors.As(e, &operation):
		return "目标网络连接失败，请检查地址、端口、防火墙和数据库服务"
	case errors.As(e, &target):
		return "目标数据库拒绝请求（MySQL " + strconv.Itoa(int(target.Code)) + "），请检查凭据和数据库权限"
	case errors.As(e, &pgConnect):
		return "PostgreSQL 连接失败，请检查 TLS、SCRAM 认证配置、数据库名称和服务状态"
	default:
		return "目标权限或协议不符合访问策略：只读凭据仅允许 SELECT / SHOW VIEW，执行凭据不得含管理权限或其他数据库授权"
	}
}
