// Package api implements the JSON API under /api/v1 with Huma, which also
// serves the OpenAPI document at /api/v1/openapi.json.
package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

// Prefix is the path prefix of every API operation.
const Prefix = "/api/v1"

// Deps are the services the handlers use.
type Deps struct {
	Version string
	Health  HealthChecker
	// StallAfter is how long the worker may go without a heartbeat before
	// /healthz reports it as stalled.
	StallAfter time.Duration
}

// New registers all operations on mux and returns the Huma API.
func New(mux *http.ServeMux, d Deps) huma.API {
	cfg := huma.DefaultConfig("Observer API", d.Version)
	cfg.OpenAPIPath = Prefix + "/openapi"
	cfg.SchemasPath = Prefix + "/schemas"
	cfg.DocsPath = "" // the built-in docs page loads scripts from a CDN
	api := humago.New(mux, cfg)

	g := huma.NewGroup(api, Prefix)
	registerHealth(g, d)
	return api
}

// HealthChecker reports the state of the service's dependencies.
type HealthChecker interface {
	PingDB(ctx context.Context) error
	LastHeartbeat() time.Time
	Started() time.Time
}

// Health is the /healthz response body.
type Health struct {
	Status        string     `json:"status" enum:"ok,starting,degraded" doc:"ok: all good; starting: worker has not reported yet; degraded: a dependency is down"`
	Database      string     `json:"database" enum:"ok,unavailable"`
	Worker        string     `json:"worker" enum:"ok,starting,stalled"`
	LastHeartbeat *time.Time `json:"last_heartbeat,omitempty" doc:"when the background worker last completed a job"`
	Version       string     `json:"version"`
}

type healthOutput struct {
	Status int
	Body   Health
}

func registerHealth(g huma.API, d Deps) {
	huma.Register(g, huma.Operation{
		OperationID: "get-health",
		Method:      http.MethodGet,
		Path:        "/healthz",
		Summary:     "Service health",
		Description: "Returns 200 when the service is usable and 503 when the database or background worker is down.",
		Tags:        []string{"system"},
		Responses: map[string]*huma.Response{
			"503": {Description: "A dependency is down; the body says which."},
		},
	}, func(ctx context.Context, _ *struct{}) (*healthOutput, error) {
		return checkHealth(ctx, d), nil
	})
}

func checkHealth(ctx context.Context, d Deps) *healthOutput {
	h := Health{Status: "ok", Database: "ok", Worker: "ok", Version: d.Version}

	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := d.Health.PingDB(pingCtx); err != nil {
		h.Database = "unavailable"
	}

	now := time.Now()
	switch last := d.Health.LastHeartbeat(); {
	case !last.IsZero() && now.Sub(last) <= d.StallAfter:
		h.LastHeartbeat = &last
	case last.IsZero() && now.Sub(d.Health.Started()) <= d.StallAfter:
		h.Worker = "starting"
	default:
		h.Worker = "stalled"
		if !last.IsZero() {
			h.LastHeartbeat = &last
		}
	}

	code := http.StatusOK
	switch {
	case h.Database != "ok" || h.Worker == "stalled":
		h.Status = "degraded"
		code = http.StatusServiceUnavailable
	case h.Worker == "starting":
		h.Status = "starting"
	}
	return &healthOutput{Status: code, Body: h}
}
