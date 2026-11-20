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

// The admin console's API on the real stack: access control, overview,
// accounts, articles with completeness flags, and a new backup.
func TestAdminConsole(t *testing.T) {
	web := watchtest.NewWeb()
	web.Set(watchtest.AliveURL, watchtest.Fixture(t, "alive_article.html"))
	mailer := mail.NewMemory(nil)
	cfg := config.Config{
		DatabaseURL: storetest.EmptyDB(t), DBMaxConns: 8, BlobDir: t.TempDir(),
		WatchDays: 60, FetchMinInterval: time.Second,
		BaseURL: "http://" + site, SnapBaseURL: "http://" + snap, SigningKey: strings.Repeat("s", 32),
		RegistrationMode: auth.ModeOpen, ServiceName: "折光观察者", DailyEmailLimit: 100, MonthlyEmailLimit: 20,
		MaxOpenWatches: 50, MaxDailySubmissions: 30,
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
	alice.api(http.MethodGet, "/admin/overview", nil, http.StatusForbidden, nil)
	alice.api(http.MethodGet, "/admin/users", nil, http.StatusForbidden, nil)
	(&client{t: t, addr: alice.addr}).api(http.MethodGet, "/admin/overview", nil, http.StatusUnauthorized, nil)
	if _, err := a.pool.Exec(ctx, `UPDATE users SET role = 'admin' WHERE email = 'alice@example.com'`); err != nil {
		t.Fatal(err)
	}

	// One backed-up article.
	var w api.WatchDetail
	alice.api(http.MethodPost, "/watches", map[string]string{"url": watchtest.AliveURL}, http.StatusAccepted, &w)
	for deadline := time.Now().Add(30 * time.Second); w.Watch.Status != "active" || len(w.Snapshots) == 0; {
		if time.Now().After(deadline) {
			t.Fatalf("no backup: %+v", w)
		}
		time.Sleep(200 * time.Millisecond)
		alice.api(http.MethodGet, "/watches/"+w.Watch.ID.String(), nil, 200, &w)
	}
	articleID := w.Watch.Article.ID

	var o api.AdminOverview
	alice.api(http.MethodGet, "/admin/overview", nil, 200, &o)
	if o.Totals.Users != 1 || o.Totals.Articles != 1 || o.Totals.ArticlesAlive != 1 || o.Totals.Snapshots != 1 ||
		o.Totals.Images != 8 || o.Totals.ImageBytes == 0 || o.Totals.OpenWatches != 1 {
		t.Errorf("totals = %+v", o.Totals)
	}
	if o.Today.NewWatches != 1 || o.Month.NewWatches != 1 || o.Month.LoginEmails != 1 || o.Month.Checks != 1 || o.Month.PageBytes == 0 {
		t.Errorf("today = %+v, month = %+v", o.Today, o.Month)
	}
	if o.EmailsToday != 1 || o.Limits.DailyEmails != 100 || o.Limits.MonthlyEmails != 20 || o.Health.Database != "ok" {
		t.Errorf("emails today %d, limits %+v, health %+v", o.EmailsToday, o.Limits, o.Health)
	}
	if len(o.Days) != 30 {
		t.Fatalf("%d days of trends", len(o.Days))
	}
	if last := o.Days[29]; last.Watches != 1 || last.Users != 1 || last.Snapshots != 1 || last.Emails != 1 || last.Checks != 1 ||
		last.Day != time.Now().In(auth.Beijing).Format("2006-01-02") {
		t.Errorf("today's trend point = %+v", last)
	}

	// Accounts.
	var bob api.AdminUser
	alice.api(http.MethodPost, "/admin/users", map[string]string{"email": "Bob@Example.com"}, http.StatusCreated, &bob)
	if bob.Email != "bob@example.com" || bob.Status != "active" || bob.MonthlyEmails != 20 || bob.CustomEmailLimit ||
		!bob.Invited || bob.InvitedBy != "alice@example.com" {
		t.Errorf("invited = %+v", bob)
	}
	// Registration is open here: users have nothing to invite.
	var invs api.Invitations
	alice.api(http.MethodGet, "/invites", nil, 200, &invs)
	if invs.CanInvite {
		t.Errorf("open mode invitations = %+v", invs)
	}
	alice.api(http.MethodPost, "/invites", map[string]string{"email": "carol@example.com"}, http.StatusConflict, nil)
	alice.api(http.MethodPost, "/admin/users", map[string]string{"email": "bob@example.com"}, http.StatusOK, nil)
	alice.api(http.MethodPost, "/admin/users", map[string]string{"email": "nope"}, http.StatusUnprocessableEntity, nil)
	var users struct {
		Items      []api.AdminUser `json:"items"`
		NextCursor string          `json:"next_cursor"`
	}
	alice.api(http.MethodGet, "/admin/users?limit=1", nil, 200, &users)
	if len(users.Items) != 1 || users.Items[0].Email != "bob@example.com" || users.NextCursor == "" {
		t.Fatalf("first page = %+v", users)
	}
	cursor := users.NextCursor
	users.Items, users.NextCursor = nil, "" // fields absent from a response keep their old values
	alice.api(http.MethodGet, "/admin/users?limit=1&cursor="+cursor, nil, 200, &users)
	if len(users.Items) != 1 || users.Items[0].Email != "alice@example.com" || users.Items[0].OpenWatches != 1 ||
		users.Items[0].WatchesThisMonth != 1 || users.Items[0].Role != "admin" || users.NextCursor != "" {
		t.Fatalf("second page = %+v", users)
	}
	users.Items = nil
	alice.api(http.MethodGet, "/admin/users?search=BOB", nil, 200, &users)
	if len(users.Items) != 1 || users.Items[0].ID != bob.ID {
		t.Errorf("search = %+v", users.Items)
	}
	path := "/admin/users/" + bob.ID.String()
	alice.api(http.MethodPatch, path, map[string]any{"monthly_emails": 5}, 200, &bob)
	if bob.MonthlyEmails != 5 || !bob.CustomEmailLimit {
		t.Errorf("custom limit = %+v", bob)
	}
	alice.api(http.MethodPatch, path, map[string]any{"default_email_limit": true}, 200, &bob)
	if bob.MonthlyEmails != 20 || bob.CustomEmailLimit {
		t.Errorf("default limit = %+v", bob)
	}
	alice.api(http.MethodPatch, "/admin/users/"+w.Watch.ID.String(), map[string]any{"status": "disabled"}, http.StatusNotFound, nil) // not a user id
	var self api.Me
	alice.api(http.MethodGet, "/me", nil, 200, &self)
	alice.api(http.MethodPatch, "/admin/users/"+self.ID.String(), map[string]any{"status": "disabled"}, http.StatusBadRequest, nil)
	alice.api(http.MethodPatch, "/admin/users/"+self.ID.String(), map[string]any{"role": "user"}, http.StatusBadRequest, nil)

	// Bob, made admin, may read a backup of an article he never watched.
	alice.api(http.MethodPatch, path, map[string]any{"role": "admin"}, 200, &bob)
	b := &client{t: t, addr: alice.addr}
	b.login(mailer, "bob@example.com")
	b.api(http.MethodPost, "/snapshots/"+w.Snapshots[0].ID.String()+"/view-url", nil, 200, nil)
	// Disabled, his session stops working at once.
	alice.api(http.MethodPatch, path, map[string]any{"status": "disabled"}, 200, &bob)
	if bob.Status != "disabled" {
		t.Errorf("status = %s", bob.Status)
	}
	b.api(http.MethodGet, "/me", nil, http.StatusUnauthorized, nil)

	// Articles and their completeness.
	var arts struct {
		Items []api.AdminArticle `json:"items"`
	}
	alice.api(http.MethodGet, "/admin/articles", nil, 200, &arts)
	if len(arts.Items) != 1 || arts.Items[0].Latest == nil || arts.Items[0].Latest.Images != 8 ||
		arts.Items[0].Latest.TextRunes == nil || *arts.Items[0].Latest.TextRunes < 100 || len(arts.Items[0].Issues) != 0 ||
		arts.Items[0].Watchers != 1 {
		t.Fatalf("articles = %+v", arts.Items)
	}
	alice.api(http.MethodGet, "/admin/articles?issues=true", nil, 200, &arts)
	if len(arts.Items) != 0 {
		t.Errorf("complete backup flagged: %+v", arts.Items)
	}
	if _, err := a.pool.Exec(ctx, `UPDATE snapshots SET missing_assets = 2`); err != nil {
		t.Fatal(err)
	}
	alice.api(http.MethodGet, "/admin/articles?issues=true&status=alive", nil, 200, &arts)
	if len(arts.Items) != 1 || strings.Join(arts.Items[0].Issues, ",") != "missing_images" {
		t.Errorf("incomplete backup: %+v", arts.Items)
	}
	alice.api(http.MethodGet, "/admin/articles?status=deleted", nil, 200, &arts)
	if len(arts.Items) != 0 {
		t.Errorf("status filter: %+v", arts.Items)
	}
	alice.api(http.MethodGet, "/admin/articles?search=空调", nil, 200, &arts)
	if len(arts.Items) != 1 {
		t.Errorf("title search found %d", len(arts.Items))
	}

	var d api.AdminArticleDetail
	alice.api(http.MethodGet, "/admin/articles/"+articleID.String(), nil, 200, &d)
	if len(d.Snapshots) != 1 || len(d.Checks) != 1 || d.Checks[0].WireBytes == nil || len(d.Watchers) != 1 ||
		d.Watchers[0].Email != "alice@example.com" || !d.CanResnapshot || d.Article.URL == "" {
		t.Fatalf("detail = %+v", d)
	}

	// A new backup: fetched again from the article's own link.
	web.Set(d.Article.URL, watchtest.Fixture(t, "alive_article.html"))
	alice.api(http.MethodPost, "/admin/articles/"+articleID.String()+"/resnapshot", nil, http.StatusAccepted, nil)
	for deadline := time.Now().Add(30 * time.Second); len(d.Snapshots) < 2; {
		if time.Now().After(deadline) {
			t.Fatalf("no new backup: %+v", d.Snapshots)
		}
		time.Sleep(200 * time.Millisecond)
		alice.api(http.MethodGet, "/admin/articles/"+articleID.String(), nil, 200, &d)
	}
	if d.Snapshots[0].MissingImages != 0 || len(d.Issues) != 0 {
		t.Errorf("the new backup should be complete: %+v, issues %v", d.Snapshots[0], d.Issues)
	}

	if _, err := a.pool.Exec(ctx, `UPDATE articles SET status = 'deleted'`); err != nil {
		t.Fatal(err)
	}
	alice.api(http.MethodPost, "/admin/articles/"+articleID.String()+"/resnapshot", nil, http.StatusConflict, nil)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}
