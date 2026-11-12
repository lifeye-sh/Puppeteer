//go:build ignore

// Creates only new, explicitly named R12 acceptance fixtures in the development
// instance. Secrets are written to .local, never to stdout or application logs.
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"next-terminal/backend/internal/modules/audit"
	"next-terminal/backend/internal/modules/authorization"
	"next-terminal/backend/internal/modules/database"
	"next-terminal/backend/internal/modules/databaseauth"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/vault"
	"next-terminal/backend/internal/platform/postgres"
	"os"
	"time"
)

func main() {
	if _, e := os.Stat(".local/r12-native-client.json"); e == nil {
		fmt.Println("Existing R12 native fixture retained")
		return
	}
	ctx := context.Background()
	pool, e := pgxpool.New(ctx, os.Getenv("NT_TEST_DATABASE_URL"))
	must(e)
	defer pool.Close()
	db := postgres.New(pool)
	key, e := hex.DecodeString(os.Getenv("NT_VAULT_KEY"))
	must(e)
	if len(key) != 32 {
		panic("vault key missing")
	}
	raw, e := os.ReadFile(".local/r12-mysql/credentials.json")
	must(e)
	var passwords map[string]string
	must(json.Unmarshal(raw, &passwords))
	clear(raw)
	ca, e := os.ReadFile(".local/r12-mysql/server.pem")
	must(e)
	var proxyCertificate string
	must(db.Row(ctx, `SELECT pem FROM certificates WHERE id=(SELECT (typed_value#>>'{}')::uuid FROM settings WHERE key='database-proxy-certificate-id')`).Scan(&proxyCertificate))
	loginPassword := identity.NewToken()
	databasePassword, verifier, e := databaseauth.NewVerifier().Create(ctx)
	must(e)
	suffix := time.Now().Format("0102150405")
	username := "r12_native_" + suffix
	alias := "r12_mysql_" + suffix
	var user, asset, work, readonly, writer string
	must(db.Within(ctx, func(c context.Context) error {
		if e := db.Row(c, `INSERT INTO users(username,nickname,password_hash,type,remark) VALUES($1,'R12 内网验收用户',$2,'user','R12 isolated acceptance fixture') RETURNING id`, username, identity.HashPassword(loginPassword)).Scan(&user); e != nil {
			return e
		}
		secrets := vault.New(db, key)
		var e error
		readonly, e = secrets.Save(c, vault.Write{Credential: vault.Credential{Name: "R12 MySQL 只读 " + suffix, Kind: "password", Username: "reader"}, Secret: &vault.Secret{Password: passwords["reader"]}})
		if e != nil {
			return e
		}
		writer, e = secrets.Save(c, vault.Write{Credential: vault.Credential{Name: "R12 MySQL 执行 " + suffix, Kind: "password", Username: "writer"}, Secret: &vault.Secret{Password: passwords["writer"]}})
		if e != nil {
			return e
		}
		service := database.New(db)
		asset, e = service.Save(c, database.Asset{Name: "R12 MySQL 内网验收环境", Alias: alias, Host: "nt-r12-mysql-fixture", Port: 3306, Database: "inventory", ReadonlyCredentialID: readonly, WriteCredentialID: writer, TLSServerName: "nt-r12-mysql-fixture", TLSCA: string(ca), Description: "仅用于 R12 协议和前端验收的独立 MySQL 8.4 实例", ConnectTimeout: 10, Enabled: true})
		if e != nil {
			return e
		}
		if _, e = authorization.New(db).SaveGrant(c, authorization.Grant{UserID: user, ResourceID: asset}); e != nil {
			return e
		}
		if e = secrets.StoreDatabaseVerifier(c, user, verifier, 1); e != nil {
			return e
		}
		work, e = service.CreateWork(c, database.WorkOrder{AssetID: asset, RequesterID: user, Title: "R12 浏览器审批验收", SQL: "SELECT id,label FROM items ORDER BY id", Mode: "transaction", AssetVersion: 1, CredentialVersion: 1})
		if e != nil {
			return e
		}
		return audit.New(db).Detail(c, "", "database.fixture_created", "r12-fixture", "", map[string]string{"user": user, "asset": asset, "workOrder": work})
	}))
	must(os.WriteFile(".local/r12-proxy.pem", []byte(proxyCertificate), 0600))
	output, e := json.MarshalIndent(map[string]string{"username": username, "userId": user, "alias": alias, "assetId": asset, "workOrderId": work, "readonlyCredentialId": readonly, "writeCredentialId": writer, "loginPassword": loginPassword, "databasePassword": databasePassword}, "", "  ")
	must(e)
	must(os.WriteFile(".local/r12-native-client.json", output, 0600))
	fmt.Println("R12 isolated acceptance user, asset and work order created; credentials saved privately")
}
func must(e error) {
	if e != nil {
		panic(e)
	}
}
