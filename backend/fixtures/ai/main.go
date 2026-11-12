// ai-fixture is an explicit internal protocol fixture, not an AI model.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"id": "r15-protocol-fixture"}, map[string]string{"id": "r15-tool-fixture"}}})
	})
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Stream bool `json:"stream"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&v) != nil || !strings.HasPrefix(v.Model, "r15-") || len(v.Messages) == 0 {
			http.Error(w, "invalid fixture request", 400)
			return
		}
		answer := "【内网协议测试模型 · 固定回复】\n请求已通过平台鉴权、模型转发及历史存储链路。本服务不进行智能推理，也不会执行命令。\n\n收到的问题：" + v.Messages[len(v.Messages)-1].Content
		if v.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			if v.Model == "r15-tool-fixture" {
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"fixture-inspect\",\"function\":{\"name\":\"asset_inspect\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":4}}\n\ndata: [DONE]\n\n")
				return
			}
			runes := []rune(answer)
			for start := 0; start < len(runes); start += 4 {
				end := min(start+4, len(runes))
				data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]string{"content": string(runes[start:end])}}}})
				if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
					return
				}
				w.(http.Flusher).Flush()
				select {
				case <-r.Context().Done():
					return
				case <-time.After(300 * time.Millisecond):
				}
			}
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":0,\"completion_tokens\":0}}\n\ndata: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": answer}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 0, "completion_tokens": 0}})
	})
	s := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 60 * time.Second}
	if e := s.ListenAndServe(); e != nil {
		panic(e)
	}
}
