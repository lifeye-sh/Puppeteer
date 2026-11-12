//go:build ignore

// Run from backend: go run ./test/mysql/fixture.go
package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"next-terminal/backend/internal/modules/pki"
	"os"
	"path/filepath"
)

func main() {
	directory := ".local/r12-mysql"
	if e := os.MkdirAll(directory, 0700); e != nil {
		panic(e)
	}
	if _, e := os.Stat(filepath.Join(directory, "credentials.json")); e == nil {
		fmt.Println("Existing isolated fixture credentials retained")
		return
	}
	write := func(name string, data []byte) {
		if e := os.WriteFile(filepath.Join(directory, name), data, 0600); e != nil {
			panic(e)
		}
	}
	cert, key, e := pki.Generate("R12 isolated MySQL", []string{"nt-r12-mysql-fixture", "127.0.0.1", "localhost"}, 30, true, nil)
	if e != nil {
		panic(e)
	}
	defer clear(key)
	write("server.pem", cert)
	write("server-key.pem", key)
	password := func() string {
		var bytes [32]byte
		if _, e := rand.Read(bytes[:]); e != nil {
			panic(e)
		}
		return base64.RawURLEncoding.EncodeToString(bytes[:])
	}
	reader, writer := password(), password()
	write("root-password", []byte(password()))
	sql := fmt.Sprintf("CREATE DATABASE inventory; CREATE TABLE inventory.items(id INT PRIMARY KEY,label VARCHAR(200),payload LONGBLOB); INSERT INTO inventory.items VALUES(1,'R12 fixture',NULL),(2,NULL,NULL); CREATE USER 'reader'@'%%' IDENTIFIED BY '%s' REQUIRE SSL; GRANT SELECT,SHOW VIEW ON inventory.* TO 'reader'@'%%'; CREATE USER 'writer'@'%%' IDENTIFIED BY '%s' REQUIRE SSL; GRANT SELECT,INSERT,UPDATE,DELETE,CREATE,ALTER,DROP,INDEX ON inventory.* TO 'writer'@'%%';", reader, writer)
	write("init.sql", []byte(sql))
	credentials, _ := json.Marshal(map[string]string{"reader": reader, "writer": writer})
	write("credentials.json", credentials)
	fmt.Println("Isolated MySQL fixture configuration generated; credentials remain local")
}
