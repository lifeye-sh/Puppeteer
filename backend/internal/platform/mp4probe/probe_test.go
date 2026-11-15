package mp4probe

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReadinessChecksAuthAndSampleCleansFailedConversion(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprint(failed), func(t *testing.T) {
			posts, deletes := 0, 0
			token := strings.Repeat("m", 32)
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/healthz" {
					fmt.Fprint(w, `{"status":"ready"}`)
					return
				}
				if r.Header.Get("Authorization") != "Bearer "+token {
					w.WriteHeader(401)
					return
				}
				if strings.HasSuffix(r.URL.Path, "00000000-0000-0000-0000-000000000000") {
					w.WriteHeader(404)
					return
				}
				switch {
				case r.Method == "POST":
					posts++
					w.WriteHeader(202)
					fmt.Fprint(w, `{"id":"11111111-1111-4111-8111-111111111111"}`)
				case r.Method == "DELETE":
					deletes++
				case strings.HasSuffix(r.URL.Path, "/result"):
					fmt.Fprint(w, "0000ftyp"+strings.Repeat("m", 1100))
				default:
					state := "succeeded"
					if failed {
						state = "failed"
					}
					fmt.Fprintf(w, `{"state":%q}`, state)
				}
			}))
			defer s.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := Ready(ctx, s.URL, token); err != nil {
				t.Fatal(err)
			}
			if posts != 0 {
				t.Fatal("readiness mutated queue")
			}
			if err := Sample(ctx, s.URL, token); (err != nil) != failed {
				t.Fatal(err)
			}
			if posts != 1 || deletes != 1 {
				t.Fatal(posts, deletes)
			}
			if Ready(ctx, s.URL, strings.Repeat("x", 32)) == nil {
				t.Fatal("wrong token accepted")
			}
		})
	}
}

func TestReadinessRejectsAnonymousRenderer(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"status":"ready"}`) }))
	defer s.Close()
	if Ready(context.Background(), s.URL, strings.Repeat("m", 32)) == nil {
		t.Fatal("anonymous renderer accepted")
	}
}
