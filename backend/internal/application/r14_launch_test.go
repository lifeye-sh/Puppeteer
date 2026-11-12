package application

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

func TestR14NativeSSHLaunch(t *testing.T) {
	p, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	uid := postID(t, srv, "/api/admin/users", userInput("launch-user", nil, nil), admin)
	user := loginAs(t, srv, "launch-user")
	cred := postID(t, srv, "/api/admin/credentials", credInput("launch"), admin)
	in := assetInput("launch target", cred, "")
	in["alias"] = "launch-target"
	id := postID(t, srv, "/api/admin/assets", in, admin)
	path := "/api/native/ssh-launch/" + id
	expect(t, call(t, srv.URL, "GET", path, nil, nil, origin), 401)
	expect(t, call(t, srv.URL, "GET", path, nil, user, origin), 403)
	grant := postID(t, srv, "/api/admin/grants", map[string]any{"userId": uid, "resourceId": id}, admin)
	ctx := context.Background()
	if e := nativeFixtureSetting(ctx, a, true); e != nil {
		t.Fatal(e)
	}
	a.config.NativeSSHAddr = ":2022"
	a.config.NativeSSHName = "gateway.example.test"
	expect(t, call(t, srv.URL, "GET", path, nil, user, origin), 503)
	a.nativeSSHReady.Store(true)
	if _, e := a.nativeGatewayKey(ctx); e != nil {
		t.Fatal(e)
	}
	r := call(t, srv.URL, "GET", path, nil, user, origin)
	expect(t, r, 200)
	d := object(t, r)
	config := d["config"].(string)
	for _, wanted := range []string{"HostName gateway.example.test", "User launch-user:launch-target", "StrictHostKeyChecking yes", "ClearAllForwardings yes", "PasswordAuthentication no"} {
		if !strings.Contains(config, wanted) {
			t.Fatal("missing OpenSSH setting", wanted)
		}
	}
	if !strings.HasPrefix(d["knownHosts"].(string), "[gateway.example.test]:2022 ssh-ed25519 ") {
		t.Fatal("invalid known_hosts")
	}
	u, e := url.Parse(d["termarkUri"].(string))
	if e != nil || u.Scheme != "termark" || u.Host != "connect" || u.Query().Get("username") != "launch-user:launch-target" || len(u.Query()) != 3 {
		t.Fatal("invalid Termark URI", e)
	}
	for _, secret := range []string{"remote-only-secret-R03", "192.0.2.10", "tester", "bridgeTicket", "access_token"} {
		if strings.Contains(string(r.body), secret) {
			t.Fatal("target metadata leaked", secret)
		}
	}
	for _, host := range []string{"evil\nProxyCommand curl", "host%h", "-oProxyCommand=bad", "host/path", "host:2022", "a..b"} {
		a.config.NativeSSHName = host
		expect(t, call(t, srv.URL, "GET", path, nil, user, origin), 503)
	}
	a.config.NativeSSHName = "2001:db8::1"
	r = call(t, srv.URL, "GET", path, nil, user, origin)
	expect(t, r, 200)
	if !strings.HasPrefix(object(t, r)["knownHosts"].(string), "[2001:db8::1]:2022 ") {
		t.Fatal("IPv6 known_hosts")
	}
	a.config.NativeSSHAddr = ":22"
	a.config.NativeSSHName = "gateway.example.test"
	r = call(t, srv.URL, "GET", path, nil, user, origin)
	expect(t, r, 200)
	if !strings.HasPrefix(object(t, r)["knownHosts"].(string), "gateway.example.test ") {
		t.Fatal("default-port known_hosts")
	}
	if _, e = p.Exec(ctx, `UPDATE assets SET alias='' WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "GET", path, nil, user, origin), 409)
	if _, e = p.Exec(ctx, `UPDATE assets SET alias='launch-target' WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `UPDATE resource_registry SET enabled=false WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "GET", path, nil, user, origin), 403)
	if _, e = p.Exec(ctx, `UPDATE resource_registry SET enabled=true WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	if e = nativeFixtureSetting(ctx, a, false); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "GET", path, nil, user, origin), 409)
	if e = nativeFixtureSetting(ctx, a, true); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "DELETE", "/api/admin/grants/"+grant, nil, admin, origin), 204)
	expect(t, call(t, srv.URL, "GET", path, nil, user, origin), 403)
}
