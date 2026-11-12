package application

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"golang.org/x/crypto/ssh"
	"net"
	"net/http"
	"net/http/httptest"
	"next-terminal/backend/internal/modules/automation"
	"next-terminal/backend/internal/platform/jobs"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func r10TaskInput(asset, command string) map[string]any {
	return map[string]any{"name": "内网任务", "kind": "ssh.command", "command": command, "assetIds": []string{asset}, "cron": "", "timezone": "Asia/Shanghai", "enabled": false, "misfire": "skip", "timeoutSeconds": 5}
}
func triggerTask(t *testing.T, srv *httptest.Server, cookie *http.Cookie, id, key string) string {
	return postID(t, srv, "/api/tasks/"+id+"/trigger", map[string]any{"key": key, "version": 1}, cookie)
}

const taskKey = "10000000-0000-4000-8000-000000000001"

func taskSSH(t *testing.T) (string, int, *atomic.Int32) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	key, _ := ssh.NewSignerFromKey(priv)
	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(key)
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	count := &atomic.Int32{}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var sockets []net.Conn
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			raw, e := ln.Accept()
			if e != nil {
				return
			}
			mu.Lock()
			sockets = append(sockets, raw)
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer raw.Close()
				conn, chans, reqs, e := ssh.NewServerConn(raw, cfg)
				if e != nil {
					return
				}
				defer conn.Close()
				go ssh.DiscardRequests(reqs)
				for next := range chans {
					if next.ChannelType() != "session" {
						_ = next.Reject(ssh.UnknownChannelType, "")
						continue
					}
					ch, requests, e := next.Accept()
					if e != nil {
						return
					}
					wg.Add(1)
					go func() {
						defer wg.Done()
						defer ch.Close()
						for r := range requests {
							if r.Type != "exec" {
								_ = r.Reply(false, nil)
								continue
							}
							var v struct{ Command string }
							_ = ssh.Unmarshal(r.Payload, &v)
							count.Add(1)
							_ = r.Reply(true, nil)
							if v.Command == "lost" {
								return
							}
							if v.Command == "sleep" {
								select {
								case <-time.After(10 * time.Second):
								case <-requests:
									return
								}
							}
							if v.Command == "large" {
								_, _ = ch.Write([]byte(strings.Repeat("x", 100000)))
							} else {
								_, _ = fmt.Fprintln(ch, "TASK OUTPUT "+v.Command)
							}
							code := uint32(0)
							if v.Command == "fail" {
								code = 7
							}
							_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{code}))
							return
						}
					}()
				}
			}()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		for _, c := range sockets {
			_ = c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	h, p, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(p)
	return h, port, count
}
func TestR10TaskDispatchDedupAndOutput(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	h, p, count := taskSSH(t)
	asset := r04Asset(t, srv, admin, h, p, true)
	id := postID(t, srv, "/api/tasks", r10TaskInput(asset, "large"), admin)
	ids := make(chan string, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); ids <- triggerTask(t, srv, admin, id, taskKey) }()
	}
	wg.Wait()
	close(ids)
	runID := ""
	for v := range ids {
		if runID != "" && runID != v {
			t.Fatal("duplicate dispatch", runID, v)
		}
		runID = v
	}
	expect(t, call(t, srv.URL, "POST", "/api/tasks/"+id+"/trigger", map[string]any{"key": "10000000-0000-4000-8000-000000000002", "version": 1}, admin, origin), 409)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := a.RunTaskOnce(context.Background()); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	v, e := automation.New(a.db).Run(context.Background(), runID)
	if e != nil {
		t.Fatal(e)
	}
	if v.State != "succeeded" || len(v.Targets) != 1 || !v.Targets[0].Truncated || len(v.Targets[0].Output) != 65536 || count.Load() != 1 {
		t.Fatalf("run=%+v count=%d output=%d", v.Job, count.Load(), len(v.Targets[0].Output))
	}
	// Even after completion, a retry of the same trigger key returns the original run.
	if again := triggerTask(t, srv, admin, id, taskKey); again != runID {
		t.Fatal(again)
	}
	if e = a.RunTaskOnce(context.Background()); e != nil {
		t.Fatal(e)
	}
	if count.Load() != 1 {
		t.Fatal("replayed")
	}
}
func TestR10TaskCancelTimeoutLostAndRecovery(t *testing.T) {
	p, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	h, port, count := taskSSH(t)
	asset := r04Asset(t, srv, admin, h, port, true)
	s := automation.New(a.db)
	id := postID(t, srv, "/api/tasks", r10TaskInput(asset, "ok"), admin)
	rid := triggerTask(t, srv, admin, id, taskKey)
	expect(t, call(t, srv.URL, "POST", "/api/task-runs/"+rid+"/cancel", nil, admin, origin), 204)
	if e := a.RecoverTaskJobs(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e := a.RunTaskOnce(context.Background()); e != nil {
		t.Fatal(e)
	}
	v, _ := s.Run(context.Background(), rid)
	if v.State != "cancelled" || count.Load() != 0 {
		t.Fatal(v.Job, count.Load())
	}
	for _, command := range []string{"lost", "sleep"} {
		in := r10TaskInput(asset, command)
		in["timeoutSeconds"] = 1
		in["cron"] = "* * * * *"
		in["enabled"] = true
		id = postID(t, srv, "/api/tasks", in, admin)
		rid = triggerTask(t, srv, admin, id, taskKey)
		if e := a.RunTaskOnce(context.Background()); e != nil {
			t.Fatal(e)
		}
		v, _ = s.Run(context.Background(), rid)
		def, _ := s.Schedule(context.Background(), id, false)
		if v.State != "unknown" || def.Enabled || def.PauseReason == "" {
			t.Fatal("ambiguous execution must pause", v.Job, def)
		}
	}
	id = postID(t, srv, "/api/tasks", r10TaskInput(asset, "ok"), admin)
	rid = triggerTask(t, srv, admin, id, taskKey)
	q := jobs.New(a.db)
	job, e := q.Claim(context.Background(), "ssh.command")
	if e != nil {
		t.Fatal(e)
	}
	e = a.db.Within(context.Background(), func(c context.Context) error {
		if e := q.Fence(c, job); e != nil {
			return e
		}
		return s.Dispatch(c, rid, asset)
	})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(context.Background(), `UPDATE jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, rid); e != nil {
		t.Fatal(e)
	}
	if e = a.RecoverTaskJobs(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = q.Finish(context.Background(), job, "succeeded", ""); e == nil {
		t.Fatal("stale worker overwrote result")
	}
	v, _ = s.Run(context.Background(), rid)
	if v.State != "unknown" || v.Targets[0].State != "unknown" {
		t.Fatal(v)
	}
	if _, e = q.Claim(context.Background(), "ssh.command"); !jobs.IsEmpty(e) {
		t.Fatal("expired execution was requeued", e)
	}
}
func TestR10ScheduleConcurrentMisfireAndAccess(t *testing.T) {
	p, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	h, port, _ := taskSSH(t)
	asset := r04Asset(t, srv, admin, h, port, true)
	id := postID(t, srv, "/api/tasks", r10TaskInput(asset, "ok"), admin)
	if _, e := p.Exec(context.Background(), `UPDATE schedules SET cron='* * * * *',enabled=true,next_run=now()-interval '5 minutes',misfire='skip' WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	s := automation.New(a.db)
	q := jobs.New(a.db)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := s.Tick(context.Background(), q); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	var n int
	var state, reason string
	if e := p.QueryRow(context.Background(), `SELECT count(*),min(j.state),min(j.reason) FROM scheduled_runs r JOIN jobs j ON j.id=r.id WHERE r.schedule_id=$1`, id).Scan(&n, &state, &reason); e != nil {
		t.Fatal(e)
	}
	if n != 1 || state != "skipped" || reason != "misfire_skipped" {
		t.Fatal(n, state, reason)
	}
	role := postID(t, srv, "/api/admin/roles", map[string]any{"name": "task operators", "permissions": []string{"task.manage"}}, admin)
	postID(t, srv, "/api/admin/users", userInput("task-other", []string{role}, nil), admin)
	other := loginAs(t, srv, "task-other")
	expect(t, call(t, srv.URL, "POST", "/api/tasks", r10TaskInput(asset, "ok"), other, origin), 403)
	expect(t, call(t, srv.URL, "POST", "/api/tasks/"+id+"/trigger", map[string]any{"key": taskKey, "version": 1}, other, origin), 403)
	out := object(t, call(t, srv.URL, "GET", "/api/tasks", nil, other, origin))
	if out["total"].(float64) != 0 {
		t.Fatal("owner list leak", out)
	}
	// Revoke the delegator after queueing: worker must recheck and not reach SSH.
	in := r10TaskInput(asset, "ok")
	owned := postID(t, srv, "/api/tasks", in, admin)
	rid := triggerTask(t, srv, admin, owned, taskKey)
	if _, e := p.Exec(context.Background(), `UPDATE users SET status='disabled' WHERE username='accept-admin'`); e != nil {
		t.Fatal(e)
	}
	if e := a.RunTaskOnce(context.Background()); e != nil {
		t.Fatal(e)
	}
	v, e := s.Run(context.Background(), rid)
	if e != nil || v.State != "failed" || v.Targets[0].Reason != "authorization_denied" {
		t.Fatal(v, e)
	}
}
func TestR10TaskOutputDrains(t *testing.T) {
	w := &taskOutput{}
	b := []byte(strings.Repeat("a", 70000))
	n, e := w.Write(b)
	if n != len(b) || e != nil {
		t.Fatal(n, e)
	}
	v, truncated := w.snapshot()
	if !truncated || len(v) != 65536 {
		t.Fatal(len(v))
	}
	if _, e = w.Write([]byte("more")); errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

func TestR10TaskPartialAndRunningCancellation(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	host, port, count := taskSSH(t)
	trusted := r04Asset(t, srv, admin, host, port, true)
	cred := postID(t, srv, "/api/admin/credentials", credInput("untrusted task credential"), admin)
	untrustedBody := assetInput("untrusted task target", cred, "")
	untrustedBody["host"] = host
	untrustedBody["port"] = port
	untrusted := postID(t, srv, "/api/admin/assets", untrustedBody, admin)
	in := r10TaskInput(trusted, "ok")
	in["assetIds"] = []string{trusted, untrusted}
	id := postID(t, srv, "/api/tasks", in, admin)
	rid := triggerTask(t, srv, admin, id, taskKey)
	if e := a.RunTaskOnce(context.Background()); e != nil {
		t.Fatal(e)
	}
	s := automation.New(a.db)
	v, e := s.Run(context.Background(), rid)
	if e != nil || v.State != "partial" || count.Load() != 1 {
		t.Fatal(v, e, count.Load())
	}
	in = r10TaskInput(trusted, "sleep")
	in["timeoutSeconds"] = 30
	id = postID(t, srv, "/api/tasks", in, admin)
	rid = triggerTask(t, srv, admin, id, taskKey)
	done := make(chan error, 1)
	go func() { done <- a.RunTaskOnce(context.Background()) }()
	deadline := time.Now().Add(5 * time.Second)
	for count.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if count.Load() != 2 {
		t.Fatal("command did not start")
	}
	expect(t, call(t, srv.URL, "POST", "/api/task-runs/"+rid+"/cancel", nil, admin, origin), 204)
	select {
	case e = <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not close SSH")
	}
	v, e = s.Run(context.Background(), rid)
	if e != nil || v.State != "unknown" || !v.CancelRequested {
		t.Fatal(v, e)
	}
}

func TestR10ScheduleCatchUpCoalesces(t *testing.T) {
	p, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	host, port, _ := taskSSH(t)
	asset := r04Asset(t, srv, admin, host, port, true)
	id := postID(t, srv, "/api/tasks", r10TaskInput(asset, "ok"), admin)
	if _, e := p.Exec(context.Background(), `UPDATE schedules SET cron='* * * * *',enabled=true,next_run=now()-interval '1 day',misfire='fire_once' WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	s := automation.New(a.db)
	q := jobs.New(a.db)
	for i := 0; i < 3; i++ {
		if e := s.Tick(context.Background(), q); e != nil {
			t.Fatal(e)
		}
	}
	var n int
	if e := p.QueryRow(context.Background(), `SELECT count(*) FROM scheduled_runs WHERE schedule_id=$1`, id).Scan(&n); e != nil || n != 1 {
		t.Fatal("catchup flood", e, n)
	}
	if _, e := p.Exec(context.Background(), `UPDATE schedules SET next_run=now()-interval '1 second' WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	if e := s.Tick(context.Background(), q); e != nil {
		t.Fatal(e)
	}
	var reason string
	if e := p.QueryRow(context.Background(), `SELECT j.reason FROM jobs j JOIN scheduled_runs r ON r.id=j.id WHERE r.schedule_id=$1 AND state='skipped'`, id).Scan(&reason); e != nil || reason != "overlap_skipped" {
		t.Fatal(reason, e)
	}
	in := r10TaskInput(asset, "ok")
	in["kind"] = "unregistered.handler"
	expect(t, call(t, srv.URL, "POST", "/api/tasks", in, admin, origin), 422)
}
