// Generates only the isolated R16 PostgreSQL fixture's TLS files.
package main

import (
	"log"
	"next-terminal/backend/internal/modules/pki"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: postgres-fixture-init DIRECTORY")
	}
	dir := os.Args[1]
	if err := os.MkdirAll(dir, 0700); err != nil {
		log.Fatal(err)
	}
	cert, key, err := pki.Generate("R16 isolated PostgreSQL", []string{"r16-postgres-fixture", "localhost", "127.0.0.1"}, 30, true, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer clear(key)
	for name, data := range map[string][]byte{"server.pem": cert, "server.key": key} {
		if err = os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			log.Fatal(err)
		}
	}
}
