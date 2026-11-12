// Local acceptance fixture. Only mount the public TUF repository here, never signing keys.
package main

import (
	"log"
	"net/http"
	"time"
)

func main() {
	log.Fatal((&http.Server{Addr: ":8080", Handler: http.FileServer(http.Dir("/repository")), ReadHeaderTimeout: 5 * time.Second}).ListenAndServe())
}
