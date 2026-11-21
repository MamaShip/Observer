// Package api implements the JSON API under /api/v1 with Huma, which also
// serves the OpenAPI document at /api/v1/openapi.json (plan.md §9).
package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/MamaShip/Observer/internal/auth"
	"github.com/MamaShip/Observer/internal/mp"
	"github.com/MamaShip/Observer/internal/notify"
	"github.com/MamaShip/Observer/internal/snapserver"
	"github.com/MamaShip/Observer/internal/store/db"
	"github.com/MamaShip/Observer/internal/watch"
)

func init() {
	// Every list in a response is a non-nil slice; say so in the schema so
	// clients need not handle null.
	huma.DefaultArrayNullable = false
}

// Prefix is the path prefix of every API operation.
const Prefix = "/api/v1"

// Limits are per-user quotas (plan.md §18); admins are exempt.
type Limits struct {
	MaxOpenWatches     int
	MaxDailySubmission int
	DailyEmails        int // provider's daily limit, shown in the admin console
	MonthlyInvites     int // invitations per user per month; 0: only admins invite
}

// Deps are the services the handlers use. Only Version, Health and
// StallAfter are required; the rest enable the corresponding operations.
type Deps struct {
	Version string
	Health  HealthChecker
	// StallAfter is how long the worker may go without a heartbeat before
	// /healthz reports it as stalled.
	StallAfter time.Duration

	Auth        *auth.Service
	Notify      *notify.Notifier
	Watch       *watch.Service
	Queries     *db.Queries
	Signer      snapserver.Signer
	BaseURL     string // the main site; requests from other origins may not write
	SnapBaseURL string
	TrustProxy  bool // take the client address from X-Forwarded-For (behind Caddy)
	Limits      Limits
	Site        Site
	MP          *mp.Bot // the Official Account entry; nil when off
}

// Site is public information the web app needs before login.
type Site struct {
	ServiceName      string      `json:"service_name"`
	RegistrationMode string      `json:"registration_mode" enum:"open,invite" doc:"invite: only addresses an admin invited receive login codes"`
	WatchDays        int         `json:"watch_days" doc:"How long each article is watched"`
	CodesPerDay      int         `json:"codes_per_day" doc:"Login codes one email can request per day (Beijing time)"`
	CodeTTLMinutes   int         `json:"code_ttl_minutes"`
	MaxOpenWatches   int         `json:"max_open_watches"`
	MonthlyEmails    int         `json:"monthly_emails" doc:"Notification emails per account per month by default; 0 means no limit"`
	Version          string      `json:"version" doc:"The running release (v1.2.3), or a development build (git describe output, or dev)"`
	Wechat           *SiteWechat `json:"wechat,omitempty" doc:"Present when users can use the Official Account"`
}

type siteOutput struct{ Body Site }

// New builds the API handler (with its request middleware) and the Huma API.
func New(d Deps) (http.Handler, huma.API) { return build(d, d.Auth != nil) }

// Spec is the OpenAPI document of every operation; the web client's types
// are generated from it (observer openapi, make openapi).
func Spec() *huma.OpenAPI {
	_, api := build(Deps{}, true)
	return api.OpenAPI()
}

func build(d Deps, all bool) (http.Handler, huma.API) {
	mux := http.NewServeMux()
	cfg := huma.DefaultConfig("Observer API", d.Version)
	cfg.OpenAPIPath = Prefix + "/openapi"
	cfg.SchemasPath = Prefix + "/schemas"
	cfg.DocsPath = "" // the built-in docs page loads scripts from a CDN
	// No $schema links in responses: they add nothing for our clients, and
	// behind the TLS-terminating proxy they would say http://.
	cfg.CreateHooks = nil
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"session": {Type: "apiKey", In: "cookie", Name: auth.CookieName},
		"bearer":  {Type: "http", Scheme: "bearer", Description: "The token from POST /auth/verify with bearer: true."},
	}
	api := humago.New(mux, cfg)
	api.UseMiddleware(requireLogin(api))

	g := huma.NewGroup(api, Prefix)
	registerHealth(g, d)
	huma.Register(g, huma.Operation{
		OperationID: "get-site", Method: http.MethodGet, Path: "/site",
		Summary: "Public service settings", Tags: []string{"system"},
	}, func(context.Context, *struct{}) (*siteOutput, error) {
		s := d.Site
		s.CodesPerDay, s.CodeTTLMinutes = auth.CodesPerDay, int(auth.CodeTTL.Minutes())
		s.Version = d.Version
		return &siteOutput{Body: s}, nil
	})
	if all {
		h := &handlers{d: d}
		h.registerAuth(g)
		h.registerMe(g)
		h.registerInvites(g)
		h.registerWatches(g)
		h.registerSnapshots(g)
		h.registerAdmin(g)
		h.registerWechat(g)
	}
	return d.middleware(mux), api
}

// loggedIn marks an operation as requiring a session.
var loggedIn = []map[string][]string{{"session": {}}, {"bearer": {}}}

type principalKey struct{}

// principal is the authenticated caller.
type principal struct {
	User  db.User
	Token string
}

func principalFrom(ctx context.Context) *principal {
	p, _ := ctx.Value(principalKey{}).(*principal)
	return p
}

type clientIPKey struct{}

func clientIP(ctx context.Context) netip.Addr {
	ip, _ := ctx.Value(clientIPKey{}).(netip.Addr)
	return ip
}

// sessionToken reads the session from the cookie or an Authorization header.
func sessionToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	if c, err := r.Cookie(auth.CookieName); err == nil {
		return c.Value
	}
	return ""
}

// middleware resolves the caller, its address, and rejects cross-site writes.
func (d Deps) middleware(next http.Handler) http.Handler {
	origin := ""
	if u, err := url.Parse(d.BaseURL); err == nil && u.Host != "" {
		origin = u.Scheme + "://" + u.Host
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			o := r.Header.Get("Origin")
			if (o != "" && o != origin) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				writeProblem(w, http.StatusForbidden, "跨站请求被拒绝。")
				return
			}
		}
		ctx := context.WithValue(r.Context(), clientIPKey{}, d.clientAddr(r))
		if tok := sessionToken(r); tok != "" && d.Auth != nil {
			if u, err := d.Auth.Authenticate(ctx, tok); err == nil {
				ctx = context.WithValue(ctx, principalKey{}, &principal{User: u, Token: tok})
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (d Deps) clientAddr(r *http.Request) netip.Addr {
	if d.TrustProxy {
		// Caddy appends the address it saw; the rightmost entry is the one we trust.
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip, err := netip.ParseAddr(strings.TrimSpace(parts[len(parts)-1])); err == nil {
				return ip.Unmap()
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip, _ := netip.ParseAddr(host)
	return ip.Unmap()
}

// requireLogin rejects operations that declare a security requirement when
// the request has no valid session.
func requireLogin(api huma.API) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		if len(ctx.Operation().Security) > 0 && principalFrom(ctx.Context()) == nil {
			_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, "请先登录。")
			return
		}
		next(ctx)
	}
}

func writeProblem(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"title": http.StatusText(status), "status": status, "detail": detail})
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
