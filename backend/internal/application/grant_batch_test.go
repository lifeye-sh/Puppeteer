package application

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"puppeteer/backend/internal/modules/authorization"
)

func TestGrantBatchBothDirectionsAndAtomicity(t *testing.T) {
	p, _, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	depA := postID(t, srv, "/api/admin/departments", map[string]any{"name": "Batch A"}, admin)
	depB := postID(t, srv, "/api/admin/departments", map[string]any{"name": "Batch B"}, admin)
	userA := postID(t, srv, "/api/admin/users", userInput("batch-a", nil, []string{depA}), admin)
	userB := postID(t, srv, "/api/admin/users", userInput("batch-b", nil, []string{depB}), admin)
	aCookie := loginAs(t, srv, "batch-a")
	bCookie := loginAs(t, srv, "batch-b")
	credential := postID(t, srv, "/api/admin/credentials", credInput("batch credential"), admin)
	group := postID(t, srv, "/api/admin/resource-groups", map[string]any{"name": "batch group"}, admin)
	child := postID(t, srv, "/api/admin/resource-groups", map[string]any{"name": "batch child", "parentId": group}, admin)
	assetA := postID(t, srv, "/api/admin/assets", assetInput("batch A", credential, child), admin)
	assetB := postID(t, srv, "/api/admin/assets", assetInput("batch B", credential, ""), admin)
	batch := func(in any, created, skipped int) authorization.GrantBatchResult {
		t.Helper()
		r := call(t, srv.URL, "POST", "/api/admin/grants/batch", in, admin, origin)
		expect(t, r, 200)
		var result authorization.GrantBatchResult
		if err := json.Unmarshal(r.body, &result); err != nil {
			t.Fatal(err)
		}
		if result.Created != created || result.Skipped != skipped || result.Total != created+skipped {
			t.Fatal("batch counts", result)
		}
		return result
	}
	first := map[string]any{"userIds": []string{userA, userA}, "departmentIds": []string{depA}, "resourceIds": []string{assetA, group, assetA}}
	expect(t, call(t, srv.URL, "POST", "/api/admin/grants/batch", first, nil, origin), 401)
	expect(t, call(t, srv.URL, "POST", "/api/admin/grants/batch", first, bCookie, origin), 403)
	batch(first, 4, 0)
	countAssets(t, srv, aCookie, "/api/portal/assets", 1)
	countAssets(t, srv, bCookie, "/api/portal/assets", 0)
	second := map[string]any{"userIds": []string{userA, userB}, "departmentIds": []string{depA, depB}, "resourceIds": []string{assetB}}
	batch(second, 4, 0)
	countAssets(t, srv, bCookie, "/api/portal/assets", 1)
	all := map[string]any{"userIds": []string{userA, userB}, "departmentIds": []string{depA, depB}, "resourceIds": []string{assetA, assetB, group}}
	batch(all, 4, 8)
	batch(all, 0, 12)
	countAssets(t, srv, bCookie, "/api/portal/assets", 2)
	// Existing expired relationships are not silently reactivated by a bulk retry.
	if _, err := p.Exec(ctx, `UPDATE grants SET valid_from=now()-interval '2 days',expires_at=now()-interval '1 day' WHERE user_id=$1 AND resource_id=$2`, userA, assetA); err != nil {
		t.Fatal(err)
	}
	batch(first, 0, 4)
	var expired bool
	if err := p.QueryRow(ctx, `SELECT expires_at<now() FROM grants WHERE user_id=$1 AND resource_id=$2`, userA, assetA).Scan(&expired); err != nil || !expired {
		t.Fatal("existing expiry changed", err)
	}
	missing := uuid.NewString()
	for _, in := range []any{
		map[string]any{"userIds": []string{userA}, "resourceIds": []string{assetA, missing}},
		map[string]any{"userIds": []string{userA, missing}, "resourceIds": []string{assetA}},
		map[string]any{"departmentIds": []string{missing}, "resourceIds": []string{assetA}},
		map[string]any{"userIds": []string{userA}, "resourceIds": []string{assetA}, "strategyId": missing},
		map[string]any{"userIds": []string{userA}, "resourceIds": []string{assetA}, "expiresAt": time.Now().Add(-time.Hour)},
	} {
		expect(t, call(t, srv.URL, "POST", "/api/admin/grants/batch", in, admin, origin), 422)
	}
	var grants, logs int
	p.QueryRow(ctx, `SELECT count(*) FROM grants`).Scan(&grants)
	p.QueryRow(ctx, `SELECT count(*) FROM operation_logs WHERE action='grant.saved'`).Scan(&logs)
	if grants != 12 || logs != 12 {
		t.Fatal("validation leaked writes or per-grant audit missing", grants, logs)
	}
	// An audit failure at the end must roll back every grant in the batch.
	extra := postID(t, srv, "/api/admin/resource-groups", map[string]any{"name": "rollback group"}, admin)
	if _, err := p.Exec(ctx, `CREATE FUNCTION reject_batch_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='grant.batch.saved' THEN RAISE EXCEPTION 'fixture audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_batch_audit BEFORE INSERT ON operation_logs FOR EACH ROW EXECUTE FUNCTION reject_batch_audit()`); err != nil {
		t.Fatal(err)
	}
	rejected := call(t, srv.URL, "POST", "/api/admin/grants/batch", map[string]any{"userIds": []string{userA, userB}, "resourceIds": []string{extra}}, admin, origin)
	if rejected.status < 400 {
		t.Fatal("audit failure accepted")
	}
	var count int
	p.QueryRow(ctx, `SELECT count(*) FROM grants WHERE resource_id=$1`, extra).Scan(&count)
	if count != 0 {
		t.Fatal("partial batch persisted")
	}
	if _, err := p.Exec(ctx, `DROP TRIGGER reject_batch_audit ON operation_logs`); err != nil {
		t.Fatal(err)
	}
	// Concurrent retries produce one set, not duplicates.
	var wg sync.WaitGroup
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- call(t, srv.URL, "POST", "/api/admin/grants/batch", map[string]any{"userIds": []string{userA, userB}, "resourceIds": []string{extra}}, admin, origin)
		}()
	}
	wg.Wait()
	close(results)
	created, skipped := 0, 0
	for r := range results {
		expect(t, r, 200)
		var out authorization.GrantBatchResult
		json.Unmarshal(r.body, &out)
		created += out.Created
		skipped += out.Skipped
	}
	if created != 2 || skipped != 2 {
		t.Fatal("concurrent retry duplicated grants", created, skipped)
	}
}

func TestGrantBatchMixedDatabasePolicies(t *testing.T) {
	p, _, srv := r02Server(t, false)
	ctx := context.Background()
	admin := loginAs(t, srv, "accept-admin")
	user := postID(t, srv, "/api/admin/users", userInput("mixed-reader", nil, nil), admin)
	var strategy, filter, database, group string
	for _, entry := range []struct {
		query string
		id    *string
	}{
		{`INSERT INTO strategies(document) VALUES('{}') RETURNING id::text`, &strategy},
		{`INSERT INTO command_filters(document) VALUES('{}') RETURNING id::text`, &filter},
		{`INSERT INTO resource_registry(kind,display_name) VALUES('database','batch database') RETURNING id::text`, &database},
		{`INSERT INTO resource_registry(kind,display_name) VALUES('group','batch group') RETURNING id::text`, &group},
	} {
		if err := p.QueryRow(ctx, entry.query).Scan(entry.id); err != nil {
			t.Fatal(err)
		}
	}
	from := time.Now().UTC().Truncate(time.Second).Add(time.Hour)
	until := from.Add(time.Hour)
	input := map[string]any{"userIds": []string{user}, "resourceIds": []string{database, group}, "strategyId": strategy, "commandFilterId": filter, "validFrom": from, "expiresAt": until}
	expect(t, call(t, srv.URL, "POST", "/api/admin/grants/batch", input, admin, origin), 200)
	for _, id := range []string{database, group} {
		var s, f string
		var start, end time.Time
		if err := p.QueryRow(ctx, `SELECT coalesce(strategy_id::text,''),coalesce(command_filter_id::text,''),valid_from,expires_at FROM grants WHERE user_id=$1 AND resource_id=$2`, user, id).Scan(&s, &f, &start, &end); err != nil {
			t.Fatal(err)
		}
		if !start.Equal(from) || !end.Equal(until) {
			t.Fatal("validity not preserved")
		}
		if id == database {
			if s != "" || f != "" {
				t.Fatal("database inherited SSH policies")
			}
		} else if s != strategy || f != filter {
			t.Fatal("group policies lost")
		}
	}
	bad := fmt.Sprintf(`{"userIds":["%s"],"resourceIds":["%s"],"approvalId":"%s"}`, user, group, uuid.NewString())
	var object map[string]any
	json.Unmarshal([]byte(bad), &object)
	expect(t, call(t, srv.URL, "POST", "/api/admin/grants/batch", object, admin, origin), 400)
}
