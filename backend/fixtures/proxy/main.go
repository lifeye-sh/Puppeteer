// Local acceptance fixture: accepts CONNECT only for one explicitly configured target.
package main

import (
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"time"
)

func main() {
	target := os.Getenv("FIXTURE_TARGET")
	if target == "" {
		log.Fatal("FIXTURE_TARGET required")
	}
	server := &http.Server{Addr: ":3128", ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Host != target {
			w.WriteHeader(403)
			return
		}
		remote, e := net.DialTimeout("tcp", target, 5*time.Second)
		if e != nil {
			w.WriteHeader(502)
			return
		}
		client, b, e := w.(http.Hijacker).Hijack()
		if e != nil {
			remote.Close()
			return
		}
		_, _ = b.WriteString("HTTP/1.1 200 Connection established\r\n\r\n")
		_ = b.Flush()
		log.Print("accepted fixture CONNECT")
		go func() {
			defer client.Close()
			defer remote.Close()
			done := make(chan struct{})
			go func() { _, _ = io.Copy(remote, client); _ = remote.(*net.TCPConn).CloseWrite(); close(done) }()
			_, _ = io.Copy(client, remote)
			_ = client.Close()
			_ = remote.Close()
			<-done
		}()
	})}
	log.Fatal(server.ListenAndServe())
}
