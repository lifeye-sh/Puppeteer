package ai

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestR15ProviderProtocolsAndTools(t *testing.T) {
	for _, protocol := range []string{"openai", "responses", "anthropic"} {
		t.Run(protocol, func(t *testing.T) {
			var raw, stream string
			route := "/chat/completions"
			switch protocol {
			case "openai":
				raw = `{"choices":[{"message":{"content":"ok","tool_calls":[{"id":"c1","type":"function","function":{"name":"asset_inspect","arguments":"{}"}}]}}],"usage":{"prompt_tokens":7,"completion_tokens":2}}`
				stream = "data: {\"choices\":[{\"delta\":{\"content\":\"ok\",\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"asset_inspect\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n"
			case "responses":
				route = "/responses"
				raw = `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]},{"type":"function_call","call_id":"c1","name":"asset_inspect","arguments":"{}"}],"usage":{"input_tokens":7,"output_tokens":2}}`
				stream = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\ndata: {\"type\":\"response.completed\",\"response\":" + raw + "}\n\n"
			case "anthropic":
				route = "/messages"
				raw = `{"stop_reason":"tool_use","content":[{"type":"text","text":"ok"},{"type":"tool_use","id":"c1","name":"asset_inspect","input":{}}],"usage":{"input_tokens":7,"output_tokens":2}}`
				stream = "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":7}}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"c1\",\"name\":\"asset_inspect\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{}\"}}\n\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":2}}\n\ndata: {\"type\":\"message_stop\"}\n\n"
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1"+route {
					t.Errorf("route %s", r.URL.Path)
				}
				if protocol == "anthropic" {
					if r.Header.Get("x-api-key") != "test-key" || r.Header.Get("anthropic-version") == "" {
						t.Error("anthropic headers")
					}
				} else if r.Header.Get("Authorization") != "Bearer test-key" {
					t.Error("auth header")
				}
				body, _ := io.ReadAll(r.Body)
				if !strings.Contains(string(body), "asset_inspect") {
					t.Error("tools missing")
				}
				if strings.Contains(string(body), `"stream":true`) {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, stream)
				} else {
					fmt.Fprint(w, raw)
				}
			}))
			defer srv.Close()
			p := Profile{BaseURL: srv.URL + "/v1", APIKey: "test-key", Model: "fixture", AllowPrivate: true, MaxOutputTokens: 64, ProviderOptions: ProviderOptions{Protocol: protocol}, Tools: ToolDefinitions(false, false)}
			for _, streaming := range []bool{false, true} {
				var reply Reply
				var e error
				if streaming {
					reply, e = GenerateStream(context.Background(), p, []Message{{Role: "user", Content: "hello"}}, func(Reply) error { return nil })
				} else {
					reply, e = Generate(context.Background(), p, []Message{{Role: "user", Content: "hello"}})
				}
				if e != nil || reply.Text != "ok" || len(reply.ToolCalls) != 1 || !reply.UsageKnown || reply.PromptTokens != 7 || reply.CompletionTokens != 2 {
					t.Fatalf("stream=%v reply=%+v err=%v", streaming, reply, e)
				}
			}
			if _, e := readProtocolStream(strings.NewReader(strings.TrimSuffix(stream, "data: [DONE]\n\n")), p, func(Reply) error { return nil }); protocol == "openai" && e == nil {
				t.Fatal("missing final event accepted")
			}
		})
	}
}
func TestR15PinnedProxyAndModelDiscovery(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("request contract")
		}
		fmt.Fprint(w, `{"data":[{"id":"z"},{"id":"a"},{"id":"a"}]}`)
	}))
	defer target.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" {
			t.Error("CONNECT required")
			return
		}
		host, _, _ := net.SplitHostPort(r.Host)
		if net.ParseIP(host) == nil {
			t.Error("proxy-side DNS forbidden")
		}
		remote, e := net.Dial("tcp", r.Host)
		if e != nil {
			t.Error(e)
			return
		}
		local, _, e := w.(http.Hijacker).Hijack()
		if e != nil {
			t.Error(e)
			return
		}
		fmt.Fprint(local, "HTTP/1.1 200 Connection established\r\n\r\n")
		go func() { defer local.Close(); defer remote.Close(); io.Copy(remote, local) }()
		go func() { defer local.Close(); defer remote.Close(); io.Copy(local, remote) }()
	}))
	defer proxy.Close()
	p := Profile{BaseURL: target.URL + "/v1", APIKey: "fixture", AllowPrivate: true, ProviderOptions: ProviderOptions{ProxyURL: proxy.URL}}
	models, e := Models(context.Background(), p)
	if e != nil || strings.Join(models, ",") != "a,z" {
		t.Fatal(models, e)
	}
	p.BaseURL = "http://169.254.169.254"
	if _, e = Models(context.Background(), p); e == nil {
		t.Fatal("metadata endpoint allowed through proxy")
	}
}
