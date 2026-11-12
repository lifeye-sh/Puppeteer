package application

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"next-terminal/backend/internal/modules/audit"
	"strconv"
	"testing"
)

func TestNativeMediaInterruptedPrefix(t *testing.T) {
	host, port, _ := sshTarget(t)
	db, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	asset := r04Asset(t, srv, admin, host, port, true)
	id, _ := reserve(t, srv, admin, asset, false)
	ctx := context.Background()
	if err := a.audit.BeginMedia(ctx, id); err != nil {
		t.Fatal(err)
	}
	appendSegment := func(seq int, start, duration int64) error {
		data := []byte(fmt.Sprintf("isolated catalog segment %d", seq))
		return a.audit.AppendMedia(ctx, id, seq, start, duration, fmt.Sprintf("%x", sha256.Sum256(data)), data)
	}
	if err := appendSegment(1, 0, 10000); err != nil {
		t.Fatal(err)
	}
	if appendSegment(1, 0, 10000) == nil || appendSegment(3, 10000, 300) == nil || appendSegment(2, 9000, 300) == nil {
		t.Fatal("accepted duplicate, gap or overlapping segment")
	}
	if err := appendSegment(2, 10000, 300); err != nil {
		t.Fatal(err)
	}
	a.finishSession(id, "interrupted", "fixture_disconnect")
	index := call(t, srv.URL, "GET", "/api/sessions/"+id+"/recording", nil, admin, origin)
	expect(t, index, 200)
	var page audit.RecordingPage
	if err := json.Unmarshal(index.body, &page); err != nil {
		t.Fatal(err)
	}
	if page.State != "interrupted" || page.Total != 2 || len(page.Segments) != 2 || page.Segments[1].StartMS != 10000 || page.DurationMS != 10300 {
		t.Fatalf("invalid prefix: %+v", page)
	}
	if err := a.db.Within(ctx, func(c context.Context) error { return a.audit.PurgeAccess(c, id) }); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM recording_chunks WHERE session_id=$1`, id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("retention left %d segments: %v", count, err)
	}
}

func TestRecordingReplayPaginationPermissionsAndStates(t *testing.T) {
	host, port, _ := sshTarget(t)
	p, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	asset := r04Asset(t, srv, admin, host, port, true)
	ownerID := postID(t, srv, "/api/admin/users", userInput("replay-owner", nil, nil), admin)
	postID(t, srv, "/api/admin/grants", map[string]any{"userId": ownerID, "resourceId": asset}, admin)
	owner := loginAs(t, srv, "replay-owner")
	id, _ := reserve(t, srv, owner, asset, false)
	path := "/api/sessions/" + id + "/recording"
	expect(t, call(t, srv.URL, "GET", path, nil, owner, origin), 409)
	postID(t, srv, "/api/admin/users", userInput("replay-denied", nil, nil), admin)
	denied := loginAs(t, srv, "replay-denied")
	expect(t, call(t, srv.URL, "GET", path, nil, denied, origin), 403)
	expect(t, call(t, srv.URL, "GET", path, nil, nil, origin), 401)
	var expected []byte
	for i := 0; i < 130; i++ {
		kind := "output"
		payload := []byte{0xe4, 0xb8, 0xad, 0x1b, '[', '3', '2', 'm', byte(i)}
		if i == 64 {
			kind = "resize"
			payload = []byte("100,30")
		} else if i == 129 {
			kind = "exec-output"
		}
		expected = append(expected, payload...)
		if e := a.audit.Record(context.Background(), id, kind, int64(i*20), payload); e != nil {
			t.Fatal(e)
		}
	}
	a.finishSession(id, "closed", "test_completed")
	download := call(t, srv.URL, "GET", path+"/download", nil, owner, origin)
	expect(t, download, 200)
	scanner := bufio.NewScanner(bytes.NewReader(download.body))
	lines := 0
	var exported []byte
	for scanner.Scan() {
		lines++
		var line map[string]json.RawMessage
		if e := json.Unmarshal(scanner.Bytes(), &line); e != nil {
			t.Fatal(e)
		}
		if _, ok := line["payload"]; ok {
			var payload []byte
			if e := json.Unmarshal(line["payload"], &payload); e != nil {
				t.Fatal(e)
			}
			exported = append(exported, payload...)
		}
	}
	if scanner.Err() != nil || lines != 132 || !bytes.Equal(exported, expected) {
		t.Fatal("recording archive incomplete or binary payload changed", scanner.Err(), lines)
	}
	expect(t, call(t, srv.URL, "GET", path+"/download", nil, denied, origin), 403)
	expect(t, call(t, srv.URL, "GET", path+"/download", nil, nil, origin), 401)
	var got []byte
	var after int64
	for _, count := range []int{64, 64, 2} {
		r := call(t, srv.URL, "GET", path+"?after="+strconv.FormatInt(after, 10), nil, owner, origin)
		expect(t, r, 200)
		var page audit.RecordingPage
		if e := json.Unmarshal(r.body, &page); e != nil {
			t.Fatal(e)
		}
		if len(page.Events) != count || page.Total != 130 || page.State != "complete" || page.DurationMS != 2580 {
			t.Fatalf("bad page %+v", page)
		}
		for _, event := range page.Events {
			after++
			if event.Sequence != after {
				t.Fatal("sequence gap")
			}
			got = append(got, event.Payload...)
		}
		if page.Next != after {
			t.Fatal("wrong cursor")
		}
	}
	if !bytes.Equal(got, expected) {
		t.Fatal("binary payload changed")
	}
	expect(t, call(t, srv.URL, "GET", path+"?after=130", nil, owner, origin), 200)
	for _, cursor := range []string{"-1", "131", "abc", "99999999999999999999"} {
		expect(t, call(t, srv.URL, "GET", path+"?after="+cursor, nil, owner, origin), 422)
	}
	expect(t, call(t, srv.URL, "GET", path, nil, admin, origin), 200)
	// Merely listing sessions does not authorize reading their output.
	role := postID(t, srv, "/api/admin/roles", map[string]any{"name": "replay-list-only", "permissions": []string{"session.read"}}, admin)
	postID(t, srv, "/api/admin/users", userInput("replay-reviewer", []string{role}, nil), admin)
	reviewer := loginAs(t, srv, "replay-reviewer")
	expect(t, call(t, srv.URL, "GET", path, nil, reviewer, origin), 403)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/roles/"+role, map[string]any{"name": "replay-list-only", "version": 1, "permissions": []string{"session.read", "audit.read"}}, admin, origin), 200)
	expect(t, call(t, srv.URL, "GET", path, nil, reviewer, origin), 200)
	expect(t, call(t, srv.URL, "PUT", "/api/admin/roles/"+role, map[string]any{"name": "replay-list-only", "version": 2, "permissions": []string{"session.read"}}, admin, origin), 200)
	expect(t, call(t, srv.URL, "GET", path+"?after=64", nil, reviewer, origin), 403)
	// Interrupted and zero-output recordings are explicit, not reported as missing.
	id2, _ := reserve(t, srv, owner, asset, false)
	a.finishSession(id2, "interrupted", "test_interrupted")
	r := call(t, srv.URL, "GET", "/api/sessions/"+id2+"/recording", nil, owner, origin)
	expect(t, r, 200)
	var empty audit.RecordingPage
	if e := json.Unmarshal(r.body, &empty); e != nil || empty.State != "interrupted" || len(empty.Events) != 0 || empty.Total != 0 {
		t.Fatalf("bad empty recording %s", r.body)
	}
	req, _ := http.NewRequest("GET", srv.URL+path+"/download", nil)
	req.AddCookie(owner)
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("recording response must not cache")
	}
	if res.Header.Get("Content-Type") != "application/x-ndjson" || res.Header.Get("Content-Disposition") != `attachment; filename="recording-`+id+`.ntrec.jsonl"` {
		t.Fatal("download headers missing")
	}
	// A sealed recording detects later changes even when the event size is unchanged.
	if _, e = p.Exec(context.Background(), `UPDATE recording_chunks SET payload=set_byte(payload,0,0) WHERE session_id=$1 AND sequence=1`, id); e != nil {
		t.Fatal(e)
	}
	expect(t, call(t, srv.URL, "GET", path, nil, owner, origin), 409)
	expect(t, call(t, srv.URL, "GET", path+"/download", nil, owner, origin), 409)
	expect(t, call(t, srv.URL, "GET", "/api/recordings/retention-preview?days=30", nil, admin, origin), 200)
	expect(t, call(t, srv.URL, "GET", "/api/recordings/retention-preview?days=30", nil, owner, origin), 403)
	expect(t, call(t, srv.URL, "GET", "/api/recordings/retention-preview?days=0", nil, admin, origin), 422)
	var views int
	if e = p.QueryRow(context.Background(), `SELECT count(*) FROM operation_logs WHERE action='recording.viewed'`).Scan(&views); e != nil || views < 1 {
		t.Fatal("missing replay audit", e)
	}
}
