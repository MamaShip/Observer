package app

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/MamaShip/Observer/internal/api"
	"github.com/MamaShip/Observer/internal/auth"
	"github.com/MamaShip/Observer/internal/config"
	"github.com/MamaShip/Observer/internal/mail"
	"github.com/MamaShip/Observer/internal/store/storetest"
	"github.com/MamaShip/Observer/internal/watch"
	"github.com/MamaShip/Observer/internal/watch/watchtest"
)

// Renewing a watch through the API: extend an active one, watch a stopped
// one again within the open-watch limit, and refuse what cannot be renewed.
func TestRenewWatch(t *testing.T) {
	web := watchtest.NewWeb()
	web.Set(watchtest.AliveURL, watchtest.Fixture(t, "alive_article.html"))
	mailer := mail.NewMemory(nil)
	cfg := config.Config{
		DatabaseURL: storetest.EmptyDB(t), DBMaxConns: 8, BlobDir: t.TempDir(),
		WatchDays: 60, FetchMinInterval: time.Second,
		BaseURL: "http://" + site, SnapBaseURL: "http://" + snap, SigningKey: strings.Repeat("s", 32),
		RegistrationMode: auth.ModeOpen, ServiceName: "折光观察者", DailyEmailLimit: 100, MonthlyEmailLimit: 20,
		MaxOpenWatches: 2, MaxDailySubmissions: 30,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, err := New(ctx, cfg, storetest.Logger(), "test", Overrides{
		NewSession: func() watch.Session { return web.Session() }, Mailer: mailer,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx, ln) }()

	alice := &client{t: t, addr: ln.Addr().String()}
	alice.login(mailer, "alice@example.com")
	var w api.WatchDetail
	alice.api(http.MethodPost, "/watches", map[string]string{"url": watchtest.AliveURL}, http.StatusAccepted, &w)
	for deadline := time.Now().Add(30 * time.Second); w.Watch.Status != "active"; {
		if time.Now().After(deadline) {
			t.Fatalf("not active: %+v", w.Watch)
		}
		time.Sleep(200 * time.Millisecond)
		alice.api(http.MethodGet, "/watches/"+w.Watch.ID.String(), nil, 200, &w)
	}
	if !w.Watch.CanRenew {
		t.Error("an active watch should be renewable")
	}
	path := "/watches/" + w.Watch.ID.String()

	// Extend: the end moves to 60 days from now.
	if _, err := a.pool.Exec(ctx, `UPDATE watches SET expires_at = now() + interval '3 days'`); err != nil {
		t.Fatal(err)
	}
	var r api.WatchDetail
	alice.api(http.MethodPost, path+"/renew", nil, 200, &r)
	if r.Watch.Status != "active" || r.Watch.ExpiresAt.Before(time.Now().Add(59*24*time.Hour)) {
		t.Errorf("extended = %+v", r.Watch)
	}

	// Stopped: listed as renewable, and watched again.
	alice.api(http.MethodDelete, path, nil, http.StatusNoContent, nil)
	var list struct {
		Items []api.Watch `json:"items"`
	}
	alice.api(http.MethodGet, "/watches", nil, 200, &list)
	if len(list.Items) != 1 || list.Items[0].Status != "ended" || !list.Items[0].CanRenew {
		t.Fatalf("stopped watch in the list = %+v", list.Items)
	}
	// ...but not past the open-watch limit (two others are open).
	if _, err := a.pool.Exec(ctx, `
		WITH arts AS (INSERT INTO articles (status) VALUES ('pending'), ('pending') RETURNING id)
		INSERT INTO watches (user_id, article_id, submitted_url, status, expires_at)
		SELECT u.id, arts.id, 'https://mp.weixin.qq.com/s/x', 'resolving', now() + interval '60 days'
		FROM arts, users u WHERE u.email = 'alice@example.com'`); err != nil {
		t.Fatal(err)
	}
	alice.api(http.MethodPost, path+"/renew", nil, http.StatusTooManyRequests, nil)
	if _, err := a.pool.Exec(ctx, `DELETE FROM watches WHERE status = 'resolving'`); err != nil {
		t.Fatal(err)
	}
	alice.api(http.MethodPost, path+"/renew", nil, 200, &r)
	if r.Watch.Status != "active" || r.Watch.EndReason != "" || r.Watch.EndedAt != nil {
		t.Errorf("watched again = %+v", r.Watch)
	}

	// At the open-watch limit, submitting the same article again returns her
	// watch; only a new article is refused.
	if _, err := a.pool.Exec(ctx, `
		WITH art AS (INSERT INTO articles (status) VALUES ('pending') RETURNING id)
		INSERT INTO watches (user_id, article_id, submitted_url, status, expires_at)
		SELECT u.id, art.id, 'https://mp.weixin.qq.com/s/x', 'resolving', now() + interval '60 days'
		FROM art, users u WHERE u.email = 'alice@example.com'`); err != nil {
		t.Fatal(err)
	}
	var same api.WatchDetail
	alice.api(http.MethodPost, "/watches", map[string]string{"url": watchtest.AliveURL}, http.StatusCreated, &same)
	if same.Watch.ID != r.Watch.ID {
		t.Errorf("resubmitted at the limit: watch %s, want %s", same.Watch.ID, r.Watch.ID)
	}
	if resp, b := alice.do(http.MethodPost, site, api.Prefix+"/watches",
		map[string]string{"url": "https://mp.weixin.qq.com/s/aIWmD5E2y-Yg7oMltd2i8w"}); resp.StatusCode != http.StatusTooManyRequests ||
		!strings.Contains(string(b), "2 篇") {
		t.Errorf("new article at the limit: %d %s", resp.StatusCode, b)
	}
	if _, err := a.pool.Exec(ctx, `DELETE FROM watches WHERE status = 'resolving'`); err != nil {
		t.Fatal(err)
	}

	// Someone else's watch, a deleted article.
	bob := &client{t: t, addr: alice.addr}
	bob.login(mailer, "bob@example.com")
	bob.api(http.MethodPost, path+"/renew", nil, http.StatusNotFound, nil)
	alice.api(http.MethodDelete, path, nil, http.StatusNoContent, nil)
	if _, err := a.pool.Exec(ctx, `UPDATE articles SET status = 'deleted'`); err != nil {
		t.Fatal(err)
	}
	alice.api(http.MethodGet, path, nil, 200, &r)
	if r.Watch.CanRenew {
		t.Error("a deleted article offered for renewal")
	}
	alice.api(http.MethodPost, path+"/renew", nil, http.StatusConflict, nil)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}
