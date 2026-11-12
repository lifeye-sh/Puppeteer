package application

import (
	"context"
	"encoding/json"
	"errors"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
	"testing"
)

func TestR16FederationDefaultDepartments(t *testing.T) {
	pool, a, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	first := postID(t, srv, "/api/admin/departments", map[string]string{"name": "External onboarding"}, admin)
	second := postID(t, srv, "/api/admin/departments", map[string]string{"name": "Other onboarding"}, admin)
	head := func() system.Revision {
		t.Helper()
		v, e := a.system.Configuration(ctx, 0)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	save := func(key, value, password string, want int) {
		t.Helper()
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(head().ID), map[string]any{"set": map[string]any{key: value}, "password": password}, admin)
		expect(t, r, want)
	}
	original := head().ID
	save("oidc-department", first, "", 422)
	save("oidc-department", "not-a-uuid", testPassword, 422)
	save("oidc-department", "12345678-1234-4000-8000-123456789012", testPassword, 422)
	if head().ID != original {
		t.Fatal("bad department committed")
	}
	e := a.db.Within(ctx, func(c context.Context) error {
		raw, _ := json.Marshal(first)
		_, e := a.system.SaveConfiguration(c, nil, map[string]json.RawMessage{"oidc-department": raw}, "")
		return e
	})
	if !errors.Is(e, fault.Denied) {
		t.Fatal("service proof bypass", e)
	}
	save("oidc-department", first, testPassword, 202)
	save("wechat-work-department", second, testPassword, 202)
	directError := a.db.Within(ctx, func(c context.Context) error { return a.identity.DeleteDepartment(c, first) })
	if !errors.Is(directError, fault.Conflict) {
		t.Fatal("department reference guard", directError)
	}
	// The dedicated FK prevents deletion even if no users have been assigned yet.
	expect(t, call(t, srv.URL, "DELETE", "/api/admin/departments/"+first, nil, admin, origin), 409)
	prepare := func(name, kind, department string) map[string]any {
		v := userInput(name, nil, nil)
		v["defaultDepartmentSource"] = kind
		v["defaultDepartmentId"] = department
		return v
	}
	for _, kind := range []string{"oidc", "wecom"} {
		target := first
		if kind == "wecom" {
			target = second
		}
		id := postID(t, srv, "/api/admin/users", prepare("prepared-"+kind, kind, target), admin)
		var actual, typ string
		var roles, external int
		if e = pool.QueryRow(ctx, `SELECT u.type,(SELECT department_id::text FROM user_departments WHERE user_id=u.id),(SELECT count(*) FROM user_roles WHERE user_id=u.id),(SELECT count(*) FROM external_identities WHERE user_id=u.id) FROM users u WHERE id=$1`, id).Scan(&typ, &actual, &roles, &external); e != nil {
			t.Fatal(e)
		}
		if typ != "user" || actual != target || roles != 0 || external != 0 {
			t.Fatal("provisioning expanded privileges")
		}
		// New mapping cannot alter any existing account.
		if kind == "oidc" {
			save("oidc-department", second, testPassword, 202)
			departments, e := a.identity.DepartmentIDs(ctx, id)
			if e != nil || len(departments) != 1 || departments[0] != first {
				t.Fatal("existing membership changed", e)
			}
		}
	}
	expect(t, call(t, srv.URL, "POST", "/api/admin/users", prepare("stale-onboarding", "oidc", first), admin, origin), 409)
	bad := prepare("admin-onboarding", "oidc", second)
	bad["type"] = "admin"
	expect(t, call(t, srv.URL, "POST", "/api/admin/users", bad, admin, origin), 422)
	bad = prepare("extra-onboarding", "oidc", second)
	bad["departmentIds"] = []string{first}
	expect(t, call(t, srv.URL, "POST", "/api/admin/users", bad, admin, origin), 422)
	bad = prepare("invalid-onboarding", "untrusted", second)
	expect(t, call(t, srv.URL, "POST", "/api/admin/users", bad, admin, origin), 422)
	save("oidc-department", "", testPassword, 202)
	expect(t, call(t, srv.URL, "POST", "/api/admin/users", prepare("unset-onboarding", "oidc", second), admin, origin), 409)
	postID(t, srv, "/api/admin/users", userInput("onboarding-member", nil, nil), admin)
	member := loginAs(t, srv, "onboarding-member")
	expect(t, call(t, srv.URL, "POST", "/api/admin/users", prepare("member-created", "wecom", second), member, origin), 403)
	bad = prepare("role-onboarding", "wecom", second)
	bad["roleIds"] = []string{"12345678-1234-4000-8000-123456789012"}
	expect(t, call(t, srv.URL, "POST", "/api/admin/users", bad, admin, origin), 422)
	if _, e = pool.Exec(ctx, `DELETE FROM user_departments WHERE department_id=$1`, first); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "DELETE", "/api/admin/departments/"+first, nil, admin, origin), 204)
	// Keep the selected default protected, including isolated restore (login stays disabled).
	if e = a.db.Within(ctx, a.identity.QuarantineRestore); e != nil {
		t.Fatal(e)
	}
	var department string
	if e = pool.QueryRow(ctx, `SELECT department_id::text FROM federation_default_departments WHERE kind='wecom'`).Scan(&department); e != nil || department != second {
		t.Fatal("restore mapping", e)
	}
}
