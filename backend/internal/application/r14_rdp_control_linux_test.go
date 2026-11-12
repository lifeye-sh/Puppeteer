//go:build linux

package application

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/session"
	"next-terminal/backend/internal/platform/rdpworker"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A real child receives the ticket through FD3, never argv/environment. This
// exercises the private control and target bridges; it is not a FreeRDP client.
func TestR14RDPControlWorkerHelper(t *testing.T) {
	args := os.Args
	at := -1
	for i, s := range args {
		if s == "--" {
			at = i
			break
		}
	}
	if at < 0 || len(args) != at+3 {
		t.Skip("worker helper")
	}
	socket, mode := args[at+1], args[at+2]
	file := os.NewFile(3, "accepted")
	client, e := net.FileConn(file)
	file.Close()
	if e != nil {
		t.Fatal("accepted fd")
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(15 * time.Second))
	var ticket string
	if e = json.NewDecoder(client).Decode(&ticket); e != nil {
		t.Fatal("ticket input")
	}
	nonce := make([]byte, 32)
	bootstrap := os.NewFile(4, "bootstrap")
	_, e = io.ReadFull(bootstrap, nonce)
	bootstrap.Close()
	if e != nil {
		t.Fatal("nonce input")
	}
	c, e := net.Dial("unix", socket)
	if e != nil {
		t.Fatal("control connect")
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(15 * time.Second))
	if _, e = c.Write(nonce); e != nil {
		t.Fatal("nonce write")
	}
	clear(nonce)
	send := func(id uint64, method, proof string) {
		t.Helper()
		raw, _ := json.Marshal(rdpworker.ControlRequest{ID: id, Method: method, Ticket: proof})
		var h [4]byte
		binary.BigEndian.PutUint32(h[:], uint32(len(raw)))
		if _, e = c.Write(append(h[:], raw...)); e != nil {
			t.Fatal("RPC write", method)
		}
		clear(raw)
	}
	call := func(id uint64, method, proof string) rdpworker.ControlReply {
		t.Helper()
		send(id, method, proof)
		var h [4]byte
		if _, e = io.ReadFull(c, h[:]); e != nil {
			t.Fatal("RPC header", method)
		}
		n := binary.BigEndian.Uint32(h[:])
		if n == 0 || n > 65536 {
			t.Fatal("RPC size")
		}
		raw := make([]byte, n)
		defer clear(raw)
		if _, e = io.ReadFull(c, raw); e != nil {
			t.Fatal("RPC body", method)
		}
		var out rdpworker.ControlReply
		if json.Unmarshal(raw, &out) != nil || out.ID != id {
			t.Fatal("RPC reply", method)
		}
		return out
	}
	call(1, "ConsumeTicket", ticket)
	ticket = ""
	ready := os.NewFile(5, "ready")
	ready.Write([]byte("READY\n"))
	ready.Close()
	reply := call(2, "PrepareTarget", "")
	var target nativeRDPTarget
	if json.Unmarshal(reply.Payload, &target) != nil {
		t.Fatal("target payload")
	}
	clear(reply.Payload)
	if target.Audio || target.Password != "remote-only-secret-R03" || target.Username != "tester" || target.Security != "nla" || target.ServerName != "127.0.0.1" || target.CertificateFingerprint != "sha256:"+strings.Repeat("ab", 32) {
		t.Fatal("target properties")
	}
	// Clipboard policy comes from the platform user channel policy, never from
	// worker input. Paste is impossible on a readonly session.
	switch mode {
	case "clipboard-off":
		if target.ClipboardCopy || target.ClipboardPaste {
			t.Fatal("clipboard enabled with off policy")
		}
	case "readonly-policy":
		if !target.ClipboardCopy || target.ClipboardPaste {
			t.Fatal("readonly clipboard policy")
		}
	case "drives-on":
		if !target.Drives || !target.DriveUpload || !target.DriveDownload ||
			target.DriveMaxReadOp != 1024*1024 || target.DriveMaxWriteOp != 1024*1024 ||
			target.DriveMaxReadTotal != 256*1024*1024 || target.DriveMaxWriteTotal != 256*1024*1024 {
			t.Fatal("drive profile policy")
		}
	case "drives-readonly":
		if !target.Drives || target.DriveUpload || !target.DriveDownload {
			t.Fatal("readonly drive policy")
		}
	default:
		if !target.ClipboardCopy || !target.ClipboardPaste {
			t.Fatal("default clipboard policy")
		}
		if target.Drives {
			t.Fatal("drives enabled without the profile")
		}
	}
	target.Password = ""
	conn, e := net.Dial("unix", target.SocketPath)
	if e != nil {
		t.Fatal("private target")
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if mode == "target-failure" {
		if _, e = conn.Read(make([]byte, 1)); e != io.EOF {
			t.Fatal("failed target returned data")
		}
	} else {
		conn.Write([]byte("rdp-bridge"))
		b := make([]byte, 10)
		if _, e = io.ReadFull(conn, b); e != nil || string(b) != "rdp-bridge" {
			t.Fatal("echo")
		}

		if mode == "revoked" {
			io.WriteString(client, "REVOKE\n")
			signal := make([]byte, 3)
			if _, e = io.ReadFull(client, signal); e != nil || string(signal) != "GO\n" {
				t.Fatal("revocation signal")
			}
			send(3, "RenewSession", "")
			if _, e = c.Read(make([]byte, 1)); e != io.EOF {
				t.Fatal("revoked control stayed open")
			}
			if _, e = conn.Read(make([]byte, 1)); e != io.EOF {
				t.Fatal("revoked target stayed open")
			}
			io.WriteString(client, "OK\n")
			return
		}
		call(3, "RenewSession", "")
	}
	call(4, "Finalize", "")
	io.WriteString(client, "OK\n")
}

func TestR14RDPControlBusinessWorker(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	cred := postID(t, srv, "/api/admin/credentials", credInput("rdp-control"), admin)
	access := nativeFixtureToken(t, a, admin, []string{"assets:read", "sessions:connect"})
	var family, actor string
	if e := pool.QueryRow(context.Background(), `SELECT f.id,f.user_id FROM oauth_token_families f JOIN oauth_tokens t ON t.family_id=f.id WHERE t.token_hash=$1`, identity.Digest(access)).Scan(&family, &actor); e != nil {
		t.Fatal(e)
	}
	// "revoked" revokes the shared oauth token family for good, so it must run
	// last; the policy-dependent modes run before it.
	for _, mode := range []string{"echo", "target-failure", "clipboard-off", "drives-on", "drives-readonly", "readonly-policy", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			// The drive profile is gated by the platform switch; enable it only
			// for the dedicated modes so the other modes keep proving it off.
			a.config.RDPDriveEnabled = mode == "drives-on" || mode == "drives-readonly"
			root, e := os.MkdirTemp("", "nt-rdp-")
			if e != nil {
				t.Fatal(e)
			}
			defer os.Remove(root)
			switch mode {
			case "clipboard-off":
				if _, e = pool.Exec(context.Background(), `INSERT INTO user_channel_policy(user_id,upload,download,clipboard_copy,clipboard_paste) VALUES($1,true,true,false,false) ON CONFLICT(user_id) DO UPDATE SET clipboard_copy=false,clipboard_paste=false`, actor); e != nil {
					t.Fatal(e)
				}
			case "readonly-policy":
				if _, e = pool.Exec(context.Background(), `INSERT INTO user_channel_policy(user_id,upload,download,clipboard_copy,clipboard_paste) VALUES($1,true,true,true,true) ON CONFLICT(user_id) DO UPDATE SET clipboard_copy=true,clipboard_paste=true`, actor); e != nil {
					t.Fatal(e)
				}
			}
			target, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			defer target.Close()
			port := target.Addr().(*net.TCPAddr).Port
			if mode == "target-failure" {
				target.Close()
			} else {
				go func() {
					conn, e := target.Accept()
					if e == nil {
						defer conn.Close()
						io.Copy(conn, conn)
					}
				}()
			}
			input := assetInput("native-rdp-"+mode, cred, "")
			input["host"] = "127.0.0.1"
			input["port"] = port
			input["protocol"] = "rdp"
			input["certFingerprint"] = "sha256:" + strings.Repeat("ab", 32)
			asset := postID(t, srv, "/api/admin/assets", input, admin)
			var id, ticket string
			e = a.db.Within(context.Background(), func(ctx context.Context) error {
				var e error
				id, e = a.sessions.Reserve(ctx, session.Session{ActorID: actor, AssetID: asset, Protocol: "rdp", ClientIP: "127.0.0.1", AssetVersion: 1, CredentialVersion: 1, HostKeyVersion: 1, Readonly: mode == "readonly-policy" || mode == "drives-readonly"}, "", "auth")
				if e != nil {
					return e
				}
				ticket = "NTICKET:" + id + ":" + identity.NewToken()
				if e = a.sessions.IssueNativeRDPTicket(ctx, id, a.nativeRDPTicketHash(ticket), family); e != nil {
					return e
				}
				return a.audit.OpenAccess(ctx, id, actor, asset, "accept-admin", "rdp-control", "fixture", "127.0.0.1")
			})
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			path := filepath.Join(root, "control.sock")
			control, e := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
			if e != nil {
				t.Fatal(e)
			}
			defer control.Close()
			exe, e := os.Executable()
			if e != nil {
				t.Fatal(e)
			}
			supervisor, e := rdpworker.New(exe, []string{"-test.run=^TestR14RDPControlWorkerHelper$", "--", path, mode}, 1, 5*time.Second, time.Second)
			if e != nil {
				t.Fatal(e)
			}
			controlled := make(chan error, 1)
			go func() {
				c, e := control.AcceptUnix()
				if e != nil {
					controlled <- e
					return
				}
				controlled <- supervisor.ServeControlFactory(ctx, c, a.nativeRDPControlFactory(ctx, root))
			}()
			entrance, e := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if e != nil {
				t.Fatal(e)
			}
			defer entrance.Close()
			client, e := net.Dial("tcp", entrance.Addr().String())
			if e != nil {
				t.Fatal(e)
			}
			defer client.Close()
			client.SetDeadline(time.Now().Add(15 * time.Second))
			accepted, e := entrance.AcceptTCP()
			if e != nil {
				t.Fatal(e)
			}
			exited := make(chan error, 1)
			go func() { exited <- supervisor.Run(ctx, accepted) }()
			if e = json.NewEncoder(client).Encode(ticket); e != nil {
				t.Fatal(e)
			}

			if mode == "revoked" {
				signal := make([]byte, 7)
				if _, e = io.ReadFull(client, signal); e != nil || string(signal) != "REVOKE\n" {
					t.Fatal("revocation request", e)
				}
				if _, e = pool.Exec(context.Background(), `UPDATE oauth_token_families SET revoked_at=now() WHERE id=$1`, family); e != nil {
					t.Fatal(e)
				}
				if _, e = io.WriteString(client, "GO\n"); e != nil {
					t.Fatal(e)
				}
			}
			got := make([]byte, 3)
			if _, e = io.ReadFull(client, got); e != nil || string(got) != "OK\n" {
				t.Fatalf("worker response %q: %v", got, e)
			}
			if e = <-exited; e != nil {
				t.Fatal(e)
			}
			if e = <-controlled; (mode != "revoked" && e != nil) || (mode == "revoked" && e == nil) {
				t.Fatal(e)
			}
			v, e := a.sessions.Get(context.Background(), id)
			if e != nil || v.ClosedAt == nil || v.State != "interrupted" {
				t.Fatalf("finalization %s %v", v.State, e)
			}
			var worker string
			if e = pool.QueryRow(context.Background(), `SELECT worker_id FROM native_rdp_tickets WHERE session_id=$1 AND consumed_at IS NOT NULL`, id).Scan(&worker); e != nil {
				t.Fatal(e)
			}
			if _, _, e = a.consumeNativeRDPTicket(context.Background(), ticket, worker, "127.0.0.1"); e == nil {
				t.Fatal("ticket replay")
			}
			var count int
			if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM operation_logs WHERE details->>'sessionId'=$1 AND action IN ('native.rdp_ticket_consumed','native.rdp_target_prepared','native.rdp_worker_finalized')`, id).Scan(&count); e != nil || count != 3 {
				t.Fatalf("audit %d %v", count, e)
			}
			if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM operation_logs WHERE details::text LIKE '%remote-only-secret-R03%'`).Scan(&count); e != nil || count != 0 {
				t.Fatal("secret in operation audit")
			}
		})
	}
}
