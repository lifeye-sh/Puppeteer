package main

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"time"
)

func registerSMSFixture(mux *http.ServeMux, conf config, box *inbox) {
	mux.HandleFunc("POST /sms/send", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+conf.Secret {
			http.Error(w, "denied", 403)
			return
		}
		var body map[string]string
		if json.NewDecoder(io.LimitReader(r.Body, 65536)).Decode(&body) != nil || body["id"] == "" || body["id"] != r.Header.Get("Idempotency-Key") || !regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`).MatchString(body["to"]) || len(body["text"]) > 8192 {
			http.Error(w, "invalid", 400)
			return
		}
		box.mu.Lock()
		defer box.mu.Unlock()
		for _, entry := range box.Messages {
			if entry["channel"] == "sms" {
				if previous, ok := entry["message"].(map[string]string); ok && previous["id"] == body["id"] {
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"accepted":true}`)
					return
				}
			}
		}
		box.Messages = append(box.Messages, map[string]any{"channel": "sms", "receivedAt": time.Now().UTC(), "message": body})
		if len(box.Messages) > 1000 {
			box.Messages = box.Messages[len(box.Messages)-1000:]
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"accepted":true}`)
	})
}
