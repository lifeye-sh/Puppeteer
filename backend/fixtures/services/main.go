// Internal-only acceptance services. Never deploy this executable on a public network.
package main

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"io"
	"log"
	"net/http"
	"next-terminal/backend/internal/modules/pki"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

type config struct {
	Secret    string `json:"secret"`
	DNSSecret string `json:"dnsSecret"`
}
type inbox struct {
	mu                           sync.Mutex
	Messages                     []map[string]any
	FailSMTP, FailWeCom, FailDNS bool
}

func (b *inbox) add(kind string, value any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Messages = append(b.Messages, map[string]any{"channel": kind, "receivedAt": time.Now().UTC(), "message": value})
	if len(b.Messages) > 1000 {
		b.Messages = b.Messages[len(b.Messages)-1000:]
	}
}
func initialize(dir string) error {
	if _, e := os.Stat(filepath.Join(dir, "config.json")); e == nil {
		return nil
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	cert, key, e := pki.Generate("Internal acceptance services", []string{"fixture-services", "acme-fixture", "localhost", "127.0.0.1"}, 30, false, nil)
	if e != nil {
		return e
	}
	defer clear(key)
	for name, data := range map[string][]byte{"cert.pem": cert, "key.pem": key} {
		if e = os.WriteFile(filepath.Join(dir, name), data, 0600); e != nil {
			return e
		}
	}
	secret := make([]byte, 32)
	dnsSecret := make([]byte, 32)
	if _, e = rand.Read(secret); e != nil {
		return e
	}
	if _, e = rand.Read(dnsSecret); e != nil {
		return e
	}
	c := config{Secret: base64.RawURLEncoding.EncodeToString(secret), DNSSecret: base64.StdEncoding.EncodeToString(dnsSecret)}
	data, _ := json.Marshal(c)
	if e = os.WriteFile(filepath.Join(dir, "config.json"), data, 0600); e != nil {
		return e
	}
	pebble := map[string]any{"pebble": map[string]any{"listenAddress": "0.0.0.0:14000", "managementListenAddress": "0.0.0.0:15000", "certificate": "/fixture/cert.pem", "privateKey": "/fixture/key.pem", "httpPort": 5002, "tlsPort": 5001, "externalAccountBindingRequired": false, "retryAfter": map[string]int{"authz": 1, "order": 1}, "keyAlgorithm": "ecdsa", "profiles": map[string]any{"default": map[string]any{"description": "internal acceptance", "validityPeriod": 604800}}}}
	data, _ = json.MarshalIndent(pebble, "", "  ")
	return os.WriteFile(filepath.Join(dir, "pebble.json"), data, 0600)
}
func main() {
	dir := os.Getenv("FIXTURE_DIR")
	if dir == "" {
		dir = "/fixture"
	}
	if len(os.Args) > 1 && os.Args[1] == "init" {
		if len(os.Args) > 2 {
			dir = os.Args[2]
		}
		if e := initialize(dir); e != nil {
			log.Fatal(e)
		}
		if runtime.GOOS != "windows" && os.Geteuid() == 0 {
			for _, name := range []string{"", "config.json", "cert.pem", "key.pem", "pebble.json"} {
				if e := os.Chown(filepath.Join(dir, name), 10001, 10001); e != nil {
					log.Fatal(e)
				}
			}
		}
		fmt.Println("Internal fixture configuration ready")
		return
	}
	data, e := os.ReadFile(filepath.Join(dir, "config.json"))
	if e != nil {
		log.Fatal(e)
	}
	var conf config
	if e = json.Unmarshal(data, &conf); e != nil {
		log.Fatal(e)
	}
	cert, e := tls.LoadX509KeyPair(filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"))
	if e != nil {
		log.Fatal(e)
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
	box := &inbox{Messages: []map[string]any{}}
	go serveSMTP(tlsConfig, conf, box)
	go serveDNS(conf, box)
	mux := http.NewServeMux()
	registerSMSFixture(mux, conf, box)
	mux.HandleFunc("/cgi-bin/gettoken", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("corpsecret") != conf.Secret || r.URL.Query().Get("corpid") != "internal-fixture" {
			_, _ = io.WriteString(w, `{"errcode":40001}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "access_token": conf.Secret, "expires_in": 7200})
	})
	mux.HandleFunc("/cgi-bin/message/send", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		box.mu.Lock()
		fail := box.FailWeCom
		box.mu.Unlock()
		if fail || r.URL.Query().Get("access_token") != conf.Secret {
			_, _ = io.WriteString(w, `{"errcode":40014}`)
			return
		}
		var body map[string]any
		if json.NewDecoder(io.LimitReader(r.Body, 65536)).Decode(&body) != nil {
			w.WriteHeader(400)
			return
		}
		box.add("wecom", body)
		_, _ = io.WriteString(w, `{"errcode":0}`)
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		upgrade := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, e := upgrade.Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer conn.Close()
		conn.SetReadLimit(65536)
		for {
			kind, data, e := conn.ReadMessage()
			if e != nil {
				return
			}
			if conn.WriteMessage(kind, data) != nil {
				return
			}
		}
	})
	mux.HandleFunc("/headers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"host": r.Host, "headers": r.Header})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><html lang="zh"><meta charset="utf-8"><title>内网验收上游</title><body><h1>INTERNAL_UPSTREAM</h1><p>HTTPS 网站代理已连通。</p><a href="/headers">检查上游收到的请求头</a><button onclick="const s=new WebSocket('wss://'+location.host+'/ws');s.onopen=()=>s.send('WebSocket 验收');s.onmessage=e=>document.querySelector('pre').textContent=e.data">测试 WebSocket</button><pre></pre></body></html>`)
	})
	go func() {
		server := &http.Server{Addr: ":8443", Handler: mux, TLSConfig: tlsConfig, ReadHeaderTimeout: 5 * time.Second}
		log.Fatal(server.ListenAndServeTLS("", ""))
	}()
	admin := http.NewServeMux()
	admin.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") })
	admin.HandleFunc("GET /messages", func(w http.ResponseWriter, r *http.Request) {
		box.mu.Lock()
		defer box.mu.Unlock()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(box.Messages)
	})
	admin.HandleFunc("POST /control", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Fixture-Control") != conf.Secret {
			http.Error(w, "denied", 403)
			return
		}
		var v struct{ SMTP, WeCom, DNS, Reset bool }
		if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&v) != nil {
			w.WriteHeader(400)
			return
		}
		box.mu.Lock()
		defer box.mu.Unlock()
		box.FailSMTP, box.FailWeCom, box.FailDNS = v.SMTP, v.WeCom, v.DNS
		if v.Reset {
			box.Messages = []map[string]any{}
		}
		w.WriteHeader(204)
	})
	server := &http.Server{Addr: ":8080", Handler: admin, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(server.ListenAndServe())
}
