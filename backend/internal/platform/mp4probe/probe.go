// Package mp4probe verifies renderer authentication without reading user jobs.
package mp4probe

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var client = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func request(ctx context.Context, base, token, method, path, body string, status int) ([]byte, error) {
	r, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, strings.NewReader(body))
	if err != nil {
		return nil, errors.New("MP4 地址无效")
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.Header.Set("Content-Type", "application/x-ndjson")
	res, err := client.Do(r)
	if err != nil {
		return nil, errors.New("MP4 服务不可达")
	}
	defer res.Body.Close()
	if res.StatusCode != status {
		return nil, errors.New("MP4 服务协议或认证验证失败")
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil || len(b) >= 2<<20 {
		return nil, errors.New("MP4 验证响应无效")
	}
	return b, nil
}

// Ready is read-only: a nonexistent reserved UUID must reject anonymous access
// and authenticate the caller before returning not found.
func Ready(ctx context.Context, base, token string) error {
	if len(token) < 32 || len(token) > 4096 {
		return errors.New("MP4 通信密钥长度无效")
	}
	b, err := request(ctx, base, "", "GET", "/healthz", "", 200)
	if err != nil {
		return err
	}
	var health struct{ Status string }
	if json.Unmarshal(b, &health) != nil || health.Status != "ready" {
		return errors.New("MP4 健康检查未通过")
	}
	path := "/jobs/00000000-0000-0000-0000-000000000000"
	if _, err = request(ctx, base, "", "GET", path, "", 401); err != nil {
		return err
	}
	_, err = request(ctx, base, token, "GET", path, "", 404)
	return err
}

// Sample is used only by the deployment executor after the business worker has
// stopped. It renders synthetic text, and always deletes its own remote job.
func Sample(ctx context.Context, base, token string) error {
	if err := Ready(ctx, base, token); err != nil {
		return err
	}
	const sample = "{\"type\":\"header\",\"format\":\"terminal-events-v1\",\"durationMs\":500,\"events\":1}\n{\"sequence\":1,\"kind\":\"output\",\"elapsedMs\":0,\"payload\":\"TVA0IGRlcGxveW1lbnQgY2hlY2sNCg==\"}\n{\"type\":\"end\",\"events\":1}\n"
	b, err := request(ctx, base, token, "POST", "/jobs", sample, 202)
	if err != nil {
		return err
	}
	var job struct{ ID string }
	if json.Unmarshal(b, &job) != nil || !regexp.MustCompile(`^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$`).MatchString(job.ID) {
		return errors.New("MP4 样例任务响应无效")
	}
	path := "/jobs/" + job.ID
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = request(cleanup, base, token, "DELETE", path, "", 200)
	}()
	for {
		b, err = request(ctx, base, token, "GET", path, "", 200)
		if err != nil {
			return err
		}
		var state struct{ State string }
		if json.Unmarshal(b, &state) != nil {
			return errors.New("MP4 样例状态无效")
		}
		if state.State == "succeeded" {
			break
		}
		if state.State != "running" && state.State != "queued" {
			return errors.New("MP4 样例转换失败")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	b, err = request(ctx, base, token, "GET", path+"/result", "", 200)
	if err != nil {
		return err
	}
	if len(b) < 1000 || string(b[4:8]) != "ftyp" {
		return errors.New("MP4 样例产物无效")
	}
	return nil
}
