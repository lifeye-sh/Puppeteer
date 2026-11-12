package application

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"next-terminal/backend/internal/modules/ai"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestR15StreamDisconnectCancelAndRevocation(t *testing.T) {
	for _, mode := range []string{"disconnect", "cancel", "revoke", "maintenance", "asset_revoke"} {
		t.Run(mode, func(t *testing.T) {
			pool, app, srv := r02Server(t, false)
			admin := loginAs(t, srv, "accept-admin")
			postID(t, srv, "/api/admin/users", userInput("stream-other", nil, nil), admin)
			other := loginAs(t, srv, "stream-other")
			release := make(chan struct{})
			var calls atomic.Int32
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var input map[string]any
				if json.NewDecoder(r.Body).Decode(&input) != nil || input["stream"] != true {
					t.Error("not streaming")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"第一段\"}}]}\n\n")
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				_, _ = io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"，完成\"}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer model.Close()
			defer close(release)
			p := postID(t, srv, "/api/admin/ai/profiles", map[string]any{"name": "stream", "baseUrl": model.URL, "model": "fixture", "allowPrivate": true, "enabled": true, "maxOutputTokens": 64}, admin)
			expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/settings", map[string]any{"enabled": true, "version": 1}, admin, origin), 200)
			scope := map[string]any{"profileId": p, "title": "stream"}
			var contextAsset, digest string
			if mode == "asset_revoke" {
				credential := postID(t, srv, "/api/admin/credentials", credInput("stream scope"), admin)
				contextAsset = postID(t, srv, "/api/admin/assets", assetInput("stream scope", credential, ""), admin)
				expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/settings", map[string]any{"enabled": true, "readonlyEnabled": true, "version": 2}, admin, origin), 200)
				expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/assets/"+contextAsset, map[string]any{"enabled": true, "version": 0}, admin, origin), 200)
				preview := call(t, srv.URL, "GET", "/api/ai/context?type=asset&targetId="+contextAsset, nil, admin, origin)
				expect(t, preview, 200)
				digest = object(t, preview)["digest"].(string)
				scope["contextType"] = "asset"
				scope["contextId"] = contextAsset
			}
			c := postID(t, srv, "/api/ai/conversations", scope, admin)
			path := "/api/ai/conversations/" + c
			msg := map[string]any{"text": "hello", "clientMessageId": "a401f918-799c-45f8-a3b3-74c0548a390a"}
			msg["contextDigest"] = digest
			raw, _ := json.Marshal(msg)
			requestCtx, abort := context.WithCancel(context.Background())
			defer abort()
			req, _ := http.NewRequestWithContext(requestCtx, "POST", srv.URL+path+"/messages/stream", bytes.NewReader(raw))
			req.AddCookie(admin)
			req.Header.Set("Origin", origin)
			req.Header.Set("Content-Type", "application/json")
			client := &http.Client{Timeout: 10 * time.Second}
			res, e := client.Do(req)
			if e != nil {
				t.Fatal(e)
			}
			defer res.Body.Close()
			if res.StatusCode != 200 {
				t.Fatal(res.StatusCode)
			}
			scan := bufio.NewScanner(res.Body)
			var turn ai.Turn
			for scan.Scan() {
				line := scan.Text()
				if strings.HasPrefix(line, "data: ") {
					if e = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &turn); e != nil {
						t.Fatal(e)
					}
					if turn.Output != "" {
						break
					}
				}
			}
			if turn.Output != "第一段" || turn.State != "running" || turn.Revision < 1 {
				t.Fatal("no live partial", turn, scan.Err())
			}
			// Durable partial and owner boundaries hold before the provider finishes.
			var saved string
			if e = pool.QueryRow(context.Background(), `SELECT output FROM ai_turns WHERE id=$1`, turn.ID).Scan(&saved); e != nil || saved != turn.Output {
				t.Fatal("partial not durable", e)
			}
			expect(t, call(t, srv.URL, "POST", path+"/turns/"+turn.ID+"/cancel", map[string]any{}, other, origin), 404)
			replay := call(t, srv.URL, "POST", path+"/messages/stream", msg, admin, origin)
			expect(t, replay, 200)
			if calls.Load() != 1 || !strings.Contains(string(replay.body), "第一段") {
				t.Fatal("duplicate model call or lost progress")
			}
			switch mode {
			case "disconnect":
				abort()
				res.Body.Close()
				release <- struct{}{}
			case "cancel":
				expect(t, call(t, srv.URL, "POST", path+"/turns/"+turn.ID+"/cancel", map[string]any{}, admin, origin), 200)
			case "revoke":
				expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/settings", map[string]any{"enabled": false, "version": 2}, admin, origin), 200)
			case "asset_revoke":
				expect(t, call(t, srv.URL, "PUT", "/api/admin/ai/assets/"+contextAsset, map[string]any{"enabled": false, "version": 1}, admin, origin), 200)
			case "maintenance":
				app.maintenance.Store(true)
				defer app.maintenance.Store(false)
			}
			var state, reason, output string
			deadline := time.Now().Add(6 * time.Second)
			for time.Now().Before(deadline) {
				if e = pool.QueryRow(context.Background(), `SELECT state,reason,output FROM ai_turns WHERE id=$1`, turn.ID).Scan(&state, &reason, &output); e != nil {
					t.Fatal(e)
				}
				if state != "running" {
					break
				}
				time.Sleep(25 * time.Millisecond)
			}
			if calls.Load() != 1 {
				t.Fatal("duplicate")
			}
			switch mode {
			case "disconnect":
				if state != "succeeded" || output != "第一段，完成" {
					t.Fatal(state, reason, output)
				}
			case "cancel":
				if state != "interrupted" || reason != "user_cancelled" || output != "第一段" {
					t.Fatal(state, reason, output)
				}
			case "revoke", "asset_revoke":
				if state != "failed" || reason != "authorization_changed" || output != "" {
					t.Fatal(state, reason, output)
				}
			case "maintenance":
				if state != "interrupted" || reason != "request_interrupted" {
					t.Fatal(state, reason, output)
				}
			}
		})
	}
}
