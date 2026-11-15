package deploytask

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// Handler is exposed only through the daemon's Unix socket. The API backend
// performs user authorization and MFA before calling this privileged interface.
func Handler(lifetime context.Context, e *Engine, token string) (http.Handler, error) {
	if len(token) < 32 || strings.TrimSpace(token) != token {
		return nil, errors.New("deployment token must contain at least 32 characters")
	}
	expected := sha256.Sum256([]byte(token))
	mux := http.NewServeMux()
	respond := func(w http.ResponseWriter, status int, value any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(value)
	}
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) {
		generation, task := e.View()
		history := e.Tasks()
		for i := range history {
			history[i].ProgressToken = ""
		}
		response := map[string]any{"generation": generation, "task": task, "version": 1, "history": history}
		if source, ok := e.driver.(interface {
			Operations(context.Context) map[string]OperationStatus
		}); ok {
			operations := source.Operations(r.Context())
			if task != nil && task.State == "running" {
				if row, exists := operations[task.Request.Protocol]; exists {
					row.Desired = "disabled"
					if task.Request.Enabled {
						row.Desired = "enabled"
					}
					row.Applied, row.Dependencies, row.Service = "applying", "checking", "unknown"
					row.Reason = "任务执行中；等待应用、验证或失败恢复完成"
					operations[task.Request.Protocol] = row
				}
			}
			response["operations"] = operations
		}
		if source, ok := e.driver.(interface {
			Mappings() (map[string]*Mapping, error)
		}); ok {
			if mappings, err := source.Mappings(); err == nil {
				response["mappings"] = mappings
			}
		}
		if source, ok := e.driver.(interface {
			Components(context.Context) map[string]ComponentStatus
		}); ok {
			response["components"] = source.Components(r.Context())
		}
		respond(w, 200, response)
	})
	submit := func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 65536)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var request Request
		if err := decoder.Decode(&request); err != nil {
			respond(w, 400, map[string]string{"error": "invalid deployment request"})
			return
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			respond(w, 400, map[string]string{"error": "request must contain one JSON object"})
			return
		}
		if err := Validate(request); err != nil {
			respond(w, 422, map[string]string{"error": err.Error()})
			return
		}
		if r.URL.Path == "/v1/preflight" {
			if err := e.Preflight(r.Context(), request); err != nil {
				status := 422
				if errors.Is(err, ErrBusy) || errors.Is(err, ErrConflict) {
					status = 409
				}
				respond(w, status, map[string]any{"passed": false, "error": err.Error()})
				return
			}
			respond(w, 200, map[string]any{"passed": true, "generation": request.Generation})
			return
		}
		task, err := e.Start(lifetime, request)
		if err != nil {
			status, message := 503, "deployment journal unavailable"
			if errors.Is(err, ErrBusy) || errors.Is(err, ErrConflict) {
				status = 409
				message = err.Error()
			}
			respond(w, status, map[string]string{"error": message})
			return
		}
		respond(w, 202, task)
	}
	mux.HandleFunc("POST /v1/tasks", submit)
	mux.HandleFunc("POST /v1/preflight", submit)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/deployment-progress/") {
			task := e.Find(strings.TrimPrefix(r.URL.Path, "/deployment-progress/"))
			supplied := sha256.Sum256([]byte(r.URL.Query().Get("ticket")))
			valid := false
			if task != nil && task.ProgressToken != "" && time.Since(task.Started) < 2*time.Hour && r.URL.Path == "/deployment-progress/"+task.ID {
				expectedTicket := sha256.Sum256([]byte(task.ProgressToken))
				valid = subtle.ConstantTimeCompare(supplied[:], expectedTicket[:]) == 1
			}
			if !valid {
				respond(w, 401, map[string]string{"error": "progress ticket invalid or expired"})
				return
			}
			respond(w, 200, map[string]any{"id": task.ID, "state": task.State, "stage": task.Stage, "error": task.Error, "updated": task.Updated})
			return
		}

		actual := sha256.Sum256([]byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")))
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
			respond(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		mux.ServeHTTP(w, r)
	}), nil
}
