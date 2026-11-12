package application

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/postgres"
)

type configurationProcessReply struct {
	PID      int   `json:"pid"`
	Revision int64 `json:"revision"`
	Rows     int   `json:"rows"`
	Denied   bool  `json:"denied"`
	Locks    bool  `json:"locks"`
}

// The subprocess uses its own pool and the real configuration consumer. No
// business server or database is stopped; only this test's TCP proxy is cut.
func TestR16ConfigurationProcessHelper(t *testing.T) {
	if os.Getenv("NT_R16_CONFIG_HELPER") != "1" {
		t.Skip("subprocess only")
	}
	cfg, err := pgxpool.ParseConfig(os.Getenv("NT_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal("invalid fixture configuration")
	}
	host, port, err := net.SplitHostPort(os.Getenv("NT_R16_CONFIG_PROXY"))
	if err != nil {
		t.Fatal("invalid proxy")
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal("invalid proxy port")
	}
	cfg.ConnConfig.Host, cfg.ConnConfig.Port = host, uint16(n)
	cfg.ConnConfig.Fallbacks = nil
	cfg.ConnConfig.RuntimeParams["search_path"] = os.Getenv("NT_R16_CONFIG_SCHEMA")
	cfg.MaxConns = 2
	cfg.ConnConfig.ConnectTimeout = time.Second
	p, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal("cannot initialize fixture pool")
	}
	defer p.Close()
	svc := system.New(postgres.New(p))
	in, out := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	for {
		var command string
		if in.Decode(&command) != nil || command == "stop" {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		v, err := svc.ApplyConfiguration(ctx, os.Getenv("NT_R16_CONFIG_NODE"), func(v system.Revision) error { return system.ValidateConfiguration(v.Values) })
		if err == nil {
			v, err = svc.SecurityConfiguration(ctx)
		}
		cancel()
		reply := configurationProcessReply{PID: os.Getpid(), Denied: err != nil}
		if err == nil {
			reply.Revision, reply.Rows = v.ID, v.Values.Int("postgres-result-max-rows")
			reply.Locks = v.Values.Bool("login-lock-enabled")
		}
		if out.Encode(reply) != nil {
			return
		}
	}
}

func TestR16SecurityRelaxationWaitsForIndependentNodeACK(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	cfg := pool.Config().ConnConfig
	target := net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port)))
	proxyA, proxyB := newConfigurationFaultProxy(t, target), newConfigurationFaultProxy(t, target)
	aNode := startConfigurationTestProcess(t, proxyA.listener.Addr().String(), cfg.RuntimeParams["search_path"], "security-a")
	bNode := startConfigurationTestProcess(t, proxyB.listener.Addr().String(), cfg.RuntimeParams["search_path"], "security-b")
	if !aNode.apply(t).Locks || !bNode.apply(t).Locks {
		t.Fatal("fixture must start with lock protection")
	}
	change := func(enabled bool) {
		t.Helper()
		v, e := a.system.Configuration(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), map[string]any{"set": map[string]any{"login-lock-enabled": enabled}}, admin)
		expect(t, r, 202)
	}
	proxyB.setBlocked(true)
	change(false)
	if got := aNode.apply(t); got.Denied || !got.Locks {
		t.Fatal("first ACK prematurely relaxed policy", got)
	}
	if got := bNode.apply(t); !got.Denied {
		t.Fatal("partitioned process admitted work", got)
	}
	view, e := a.system.ConfigurationView(ctx, 0)
	if e != nil || !view.SecurityPending || view.State == "effective" {
		t.Fatal("pending security barrier missing", e, view.State)
	}
	// Actual account lock checks must use the stricter enforced snapshot.
	if _, e = pool.Exec(ctx, `INSERT INTO login_locks(username,failures,locked_until,last_failed_at) VALUES('barrier-target',5,now()+interval '1 hour',now())`); e != nil {
		t.Fatal(e)
	}
	if e = a.identity.CheckLock(ctx, "barrier-target"); e == nil {
		t.Fatal("business authentication bypassed pending barrier")
	}
	proxyB.setBlocked(false)
	if got := bNode.apply(t); got.Denied || got.Locks {
		t.Fatal("all ACKs did not activate relaxation", got)
	}
	if e = a.identity.CheckLock(ctx, "barrier-target"); e != nil {
		t.Fatal("relaxation did not reach business check", e)
	}
	// Tightening does not wait for either consumer to ACK.
	change(true)
	if e = a.identity.CheckLock(ctx, "barrier-target"); e == nil {
		t.Fatal("tightening waited for node ACK")
	}
	// A later relaxation still waits; an expired process lease may be excluded,
	// but the returning process must re-read the current database policy.
	change(false)
	if _, e = pool.Exec(ctx, `UPDATE configuration_nodes SET last_seen=now()-interval '31 seconds' WHERE id='security-b'`); e != nil {
		t.Fatal(e)
	}
	if got := aNode.apply(t); got.Denied || got.Locks {
		t.Fatal("expired node lease blocked rollout", got)
	}
	if got := bNode.apply(t); got.Denied || got.Locks {
		t.Fatal("returning node did not load current security policy", got)
	}
}

type configurationFaultProxy struct {
	listener    net.Listener
	mu          sync.Mutex
	blocked     bool
	connections map[net.Conn]bool
	wg          sync.WaitGroup
}

func newConfigurationFaultProxy(t *testing.T, target string) *configurationFaultProxy {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &configurationFaultProxy{listener: l, connections: map[net.Conn]bool{}}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		for {
			client, err := l.Accept()
			if err != nil {
				return
			}
			p.wg.Add(1)
			go func() {
				defer p.wg.Done()
				defer client.Close()
				upstream, err := net.DialTimeout("tcp", target, time.Second)
				if err != nil {
					return
				}
				defer upstream.Close()
				p.mu.Lock()
				if p.blocked {
					p.mu.Unlock()
					return
				}
				p.connections[client], p.connections[upstream] = true, true
				p.mu.Unlock()
				defer func() { p.mu.Lock(); delete(p.connections, client); delete(p.connections, upstream); p.mu.Unlock() }()
				done := make(chan struct{})
				go func() { io.Copy(upstream, client); upstream.Close(); close(done) }()
				io.Copy(client, upstream)
				client.Close()
				<-done
			}()
		}
	}()
	t.Cleanup(func() { l.Close(); p.setBlocked(true); p.wg.Wait() })
	return p
}

func (p *configurationFaultProxy) setBlocked(blocked bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.blocked = blocked
	if blocked {
		for c := range p.connections {
			c.Close()
		}
	}
}

type configurationTestProcess struct {
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *json.Decoder
	done chan error
}

func startConfigurationTestProcess(t *testing.T, proxy, schema, node string) *configurationTestProcess {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(path, "-test.run=^TestR16ConfigurationProcessHelper$", "-test.timeout=90s")
	cmd.Env = append(os.Environ(), "NT_R16_CONFIG_HELPER=1", "NT_R16_CONFIG_PROXY="+proxy, "NT_R16_CONFIG_SCHEMA="+schema, "NT_R16_CONFIG_NODE="+node)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &configurationTestProcess{cmd: cmd, in: in, out: json.NewDecoder(out), done: make(chan error, 1)}
	go func() { p.done <- cmd.Wait() }()
	t.Cleanup(func() {
		in.Close()
		cmd.Process.Kill()
		select {
		case <-p.done:
		case <-time.After(3 * time.Second):
			t.Error("child did not exit")
		}
	})
	return p
}

func (p *configurationTestProcess) apply(t *testing.T) configurationProcessReply {
	t.Helper()
	if err := json.NewEncoder(p.in).Encode("apply"); err != nil {
		t.Fatal(err)
	}
	type result struct {
		reply configurationProcessReply
		err   error
	}
	done := make(chan result, 1)
	go func() { var r result; r.err = p.out.Decode(&r.reply); done <- r }()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatal("configuration child response failed", r.err)
		}
		return r.reply
	case <-time.After(6 * time.Second):
		t.Fatal("configuration child response timed out")
		return configurationProcessReply{}
	}
}

func TestR16ConfigurationIndependentProcessesAndNetworkLoss(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	cfg := pool.Config().ConnConfig
	target := net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port)))
	proxyA, proxyB := newConfigurationFaultProxy(t, target), newConfigurationFaultProxy(t, target)
	nodeA := startConfigurationTestProcess(t, proxyA.listener.Addr().String(), cfg.RuntimeParams["search_path"], "process-a")
	nodeB := startConfigurationTestProcess(t, proxyB.listener.Addr().String(), cfg.RuntimeParams["search_path"], "process-b")
	firstA, firstB := nodeA.apply(t), nodeB.apply(t)
	if firstA.Denied || firstB.Denied || firstA.PID == firstB.PID || firstA.PID == os.Getpid() || firstA.Revision != firstB.Revision {
		t.Fatal("not independent synchronized consumers", firstA, firstB)
	}
	change := func(rows int) int64 {
		t.Helper()
		v, err := a.system.Configuration(ctx, 0)
		if err != nil {
			t.Fatal(err)
		}
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(v.ID), map[string]any{"set": map[string]any{"postgres-result-max-rows": rows}}, admin)
		expect(t, r, 202)
		v, err = a.system.Configuration(ctx, 0)
		if err != nil {
			t.Fatal(err)
		}
		return v.ID
	}
	proxyB.setBlocked(true)
	current := change(7)
	if got := nodeA.apply(t); got.Denied || got.Revision != current || got.Rows != 7 {
		t.Fatal(got)
	}
	if got := nodeB.apply(t); !got.Denied || got.Revision != 0 || got.Rows != 0 {
		t.Fatal("partitioned node admitted cached configuration", got)
	}
	view, err := a.system.ConfigurationView(ctx, 0)
	if err != nil || view.State == "effective" {
		t.Fatal("missing ACK presented as effective", view.State, err)
	}
	proxyB.setBlocked(false)
	if got := nodeB.apply(t); got.Denied || got.Revision != current || got.Rows != 7 {
		t.Fatal("reconnect did not catch up", got)
	}
	view, err = a.system.ConfigurationView(ctx, 0)
	if err != nil || view.State != "effective" {
		t.Fatal(view.State, err)
	}
	// Kill one process, publish twice, and restart with the same node identity.
	if err = nodeB.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	change(6)
	current = change(5)
	restarted := startConfigurationTestProcess(t, proxyB.listener.Addr().String(), cfg.RuntimeParams["search_path"], "process-b")
	if got := restarted.apply(t); got.Denied || got.Revision != current || got.Rows != 5 || got.PID == firstB.PID {
		t.Fatal("restart reused stale state", got)
	}
	if got := nodeA.apply(t); got.Denied || got.Revision != current {
		t.Fatal(got)
	}
	// Full database network unavailability closes both independent admission paths.
	proxyA.setBlocked(true)
	proxyB.setBlocked(true)
	for _, p := range []*configurationTestProcess{nodeA, restarted} {
		if got := p.apply(t); !got.Denied {
			t.Fatal("database outage admitted work", got)
		}
	}
	proxyA.setBlocked(false)
	proxyB.setBlocked(false)
	for _, p := range []*configurationTestProcess{nodeA, restarted} {
		if got := p.apply(t); got.Denied || got.Revision != current {
			t.Fatal("database recovery failed", got)
		}
	}
}
