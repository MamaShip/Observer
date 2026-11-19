package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeHealth struct {
	dbErr   error
	last    time.Time
	started time.Time
}

func (f fakeHealth) PingDB(context.Context) error { return f.dbErr }
func (f fakeHealth) LastHeartbeat() time.Time     { return f.last }
func (f fakeHealth) Started() time.Time           { return f.started }

func get(t *testing.T, h fakeHealth, path string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	New(mux, Deps{Version: "test", Health: h, StallAfter: 5 * time.Minute})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHealthz(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name               string
		h                  fakeHealth
		code               int
		status, db, worker string
	}{
		{"healthy", fakeHealth{last: now.Add(-time.Minute), started: now.Add(-time.Hour)}, 200, "ok", "ok", "ok"},
		{"just started", fakeHealth{started: now.Add(-10 * time.Second)}, 200, "starting", "ok", "starting"},
		{"worker never reported", fakeHealth{started: now.Add(-time.Hour)}, 503, "degraded", "ok", "stalled"},
		{"worker stopped", fakeHealth{last: now.Add(-10 * time.Minute), started: now.Add(-time.Hour)}, 503, "degraded", "ok", "stalled"},
		{"database down", fakeHealth{dbErr: errors.New("boom"), last: now, started: now.Add(-time.Hour)}, 503, "degraded", "unavailable", "ok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := get(t, tc.h, Prefix+"/healthz")
			if rec.Code != tc.code {
				t.Fatalf("code = %d, want %d; body %s", rec.Code, tc.code, rec.Body)
			}
			var h Health
			if err := json.Unmarshal(rec.Body.Bytes(), &h); err != nil {
				t.Fatal(err)
			}
			if h.Status != tc.status || h.Database != tc.db || h.Worker != tc.worker || h.Version != "test" {
				t.Fatalf("health = %+v", h)
			}
			if strings.Contains(rec.Body.String(), "boom") {
				t.Fatal("database error leaked into the response")
			}
		})
	}
}

func TestOpenAPI(t *testing.T) {
	rec := get(t, fakeHealth{}, Prefix+"/openapi.json")
	if rec.Code != 200 {
		t.Fatalf("code = %d", rec.Code)
	}
	var doc struct {
		Paths map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc.Paths[Prefix+"/healthz"]; !ok {
		t.Fatalf("healthz missing from OpenAPI paths: %v", doc.Paths)
	}
	if rec := get(t, fakeHealth{}, "/docs"); rec.Code != http.StatusNotFound {
		t.Fatalf("docs page should be disabled, got %d", rec.Code)
	}
}
