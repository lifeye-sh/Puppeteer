package application

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"testing"
)

func TestR16LogoUploadAndRevision(t *testing.T) {
	_, a, srv := r02Server(t, false)
	admin := loginAs(t, srv, "accept-admin")
	postID(t, srv, "/api/admin/users", userInput("logo-member", nil, nil), admin)
	member := loginAs(t, srv, "logo-member")
	picture := image.NewRGBA(image.Rect(0, 0, 32, 16))
	picture.Set(0, 0, color.RGBA{R: 37, G: 125, B: 255, A: 255})
	var imageData bytes.Buffer
	if err := png.Encode(&imageData, picture); err != nil {
		t.Fatal(err)
	}
	raw := append(append([]byte{}, imageData.Bytes()...), []byte("trailing-content-must-disappear")...)
	upload := func(data []byte, cookie *http.Cookie) (int, []byte) {
		t.Helper()
		req, _ := http.NewRequest("POST", srv.URL+"/api/admin/branding/logo", bytes.NewReader(data))
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", "image/png")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, body
	}
	for _, c := range []*http.Cookie{nil, member} {
		status, _ := upload(raw, c)
		if status != 401 && status != 403 {
			t.Fatal(status)
		}
	}
	if status, _ := upload([]byte(`<svg onload="alert(1)"/>`), admin); status != 422 {
		t.Fatal("SVG", status)
	}
	if status, _ := upload(make([]byte, 1024*1024+1), admin); status != 413 {
		t.Fatal("size limit", status)
	}
	status, body := upload(raw, admin)
	if status != 201 {
		t.Fatal(status, string(body))
	}
	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	id := result["id"]
	status, duplicate := upload(raw, admin)
	if status != 201 || !bytes.Contains(duplicate, []byte(id)) {
		t.Fatal("dedup", status)
	}
	data, err := a.system.Logo(context.Background(), id)
	if err != nil || bytes.Contains(data, []byte("trailing-content")) {
		t.Fatal("unsafe content", err)
	}
	config, kind, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || kind != "png" || config.Width != 32 || config.Height != 16 {
		t.Fatal(config, kind, err)
	}
	expect(t, call(t, srv.URL, "GET", "/api/admin/branding/logo/"+id, nil, member, origin), 403)
	expect(t, call(t, srv.URL, "GET", "/api/admin/branding/logo/"+id, nil, admin, origin), 200)
	before, err := a.system.Configuration(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"https://external.example/logo.png", "<svg/>", "00000000-0000-0000-0000-000000000001"} {
		r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(before.ID), map[string]any{"set": map[string]string{"system-logo": invalid, "system-name": "must-rollback"}}, admin)
		expect(t, r, 422)
	}
	r, _ := settingsRequest(t, srv.URL, "PATCH", "/api/settings", configTag(before.ID), map[string]any{"set": map[string]string{"system-logo": id}}, admin)
	expect(t, r, 202)
	public := object(t, call(t, srv.URL, "GET", "/api/branding", nil, nil, origin))
	if public["logo"] != "/api/logo?v="+id {
		t.Fatal(public)
	}
	res, err := http.Get(srv.URL + "/api/logo")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	served, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/png" || !bytes.Equal(served, data) {
		t.Fatal("public logo mismatch")
	}
	old, err := a.system.Configuration(context.Background(), before.ID)
	if err != nil || old.Values.String("system-logo") != "" {
		t.Fatal("history changed", err)
	}
	_, tag := settingsRequest(t, srv.URL, "GET", "/api/settings", "", nil, admin)
	r, _ = settingsRequest(t, srv.URL, "PATCH", "/api/settings", tag, map[string]any{"set": map[string]string{"system-logo": ""}}, admin)
	expect(t, r, 202)
	fallback := call(t, srv.URL, "GET", "/api/logo", nil, nil, origin)
	expect(t, fallback, 200)
	if !bytes.Contains(fallback.body, []byte("<svg")) {
		t.Fatal("default missing")
	}
}
