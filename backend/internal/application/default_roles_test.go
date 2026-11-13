package application

import (
	"context"
	"next-terminal/backend/migrations"
	"testing"
)

func TestDefaultRoleTemplates(t *testing.T) {
	p, a, srv := r02Server(t, false)
	ctx := context.Background()
	roles, err := a.authz.Roles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var adminID, operatorID string
	catalog, err := a.authz.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range roles {
		switch role.Name {
		case "管理员":
			adminID = role.ID
			if len(role.Permissions) != len(catalog) {
				t.Fatal("incomplete administrator template")
			}
		case "操作员":
			operatorID = role.ID
			if len(role.Permissions) != 0 {
				t.Fatal("operator gained global permissions")
			}
		}
	}
	if adminID == "" || operatorID == "" {
		t.Fatal("missing default roles")
	}
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("default-operator", []string{operatorID}, nil), admin)
	op := loginAs(t, srv, "default-operator")
	expect(t, call(t, srv.URL, "GET", "/api/admin/roles", nil, op, origin), 403)
	expect(t, call(t, srv.URL, "GET", "/api/account/info", nil, op, origin), 200)
	// Startup migration re-entry must not restore privileges edited by the admin.
	if _, err = p.Exec(ctx, "DELETE FROM role_permissions WHERE role_id=$1", adminID); err != nil {
		t.Fatal(err)
	}
	if err = migrations.Apply(ctx, p); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = p.QueryRow(ctx, "SELECT count(*) FROM role_permissions WHERE role_id=$1", adminID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("role edits overwritten: %d %v", n, err)
	}
	if err = p.QueryRow(ctx, "SELECT count(*) FROM roles WHERE name IN ('管理员','操作员')").Scan(&n); err != nil || n != 2 {
		t.Fatal("duplicate roles", n, err)
	}
}
