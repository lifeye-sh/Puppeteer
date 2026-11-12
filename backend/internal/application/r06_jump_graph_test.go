package application

import (
	"next-terminal/backend/internal/modules/connectivity"
	"testing"
)

func TestR06JumpGraph(t *testing.T) {
	_, _, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	cred := postID(t, srv, "/api/admin/credentials", credInput("jump graph"), admin)
	create := func(name, parent string) connectivity.Jump {
		j := connectivity.Jump{Name: name, Host: "localhost", Port: 22, CredentialID: cred, Enabled: true, ParentID: parent}
		r := call(t, srv.URL, "POST", "/api/admin/ssh-jumps", j, admin, origin)
		expect(t, r, 200)
		j.ID = object(t, r)["id"].(string)
		j.Version = 1
		return j
	}
	a := create("a", "")
	b := create("b", a.ID)
	c := create("c", b.ID)
	d := create("d", c.ID)
	fifth := connectivity.Jump{Name: "fifth", Host: "localhost", Port: 22, CredentialID: cred, Enabled: true, ParentID: d.ID}
	expect(t, call(t, srv.URL, "POST", "/api/admin/ssh-jumps", fifth, admin, origin), 422)
	a.ParentID = c.ID
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ssh-jumps/"+a.ID, a, admin, origin), 422)
	a.ParentID = a.ID
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ssh-jumps/"+a.ID, a, admin, origin), 422)
	root := create("root", "")
	a.ParentID = root.ID
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ssh-jumps/"+a.ID, a, admin, origin), 422)
	// Move the leaf out first, then adding an outer hop is valid.
	d.ParentID = ""
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ssh-jumps/"+d.ID, d, admin, origin), 200)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/ssh-jumps/"+a.ID, a, admin, origin), 200)
}
