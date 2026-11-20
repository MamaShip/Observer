package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/MamaShip/Observer/internal/auth"
	"github.com/MamaShip/Observer/internal/notify"
	"github.com/MamaShip/Observer/internal/snapserver"
	"github.com/MamaShip/Observer/internal/store/db"
	"github.com/MamaShip/Observer/internal/watch"
)

type handlers struct{ d Deps }

// ---- auth ----

type codeInput struct {
	Body struct {
		Email string `json:"email" maxLength:"254" doc:"Where to send the login code"`
	}
}

type codeOutput struct {
	Status int
	Body   struct {
		Message     string `json:"message"`
		Remaining   int    `json:"remaining" doc:"Codes this email can still request today (Beijing time)"`
		ResendAfter int    `json:"resend_after" doc:"Seconds before another code can be requested; 0 when none are left today"`
	}
}

type verifyInput struct {
	UserAgent string `header:"User-Agent"`
	Body      struct {
		Email  string `json:"email" maxLength:"254"`
		Code   string `json:"code" maxLength:"10" doc:"The 6-digit code from the email"`
		Bearer bool   `json:"bearer,omitempty" doc:"Return the session token in the body instead of setting a cookie (for non-browser clients)"`
	}
}

type verifyOutput struct {
	SetCookie *http.Cookie `header:"Set-Cookie"`
	Body      struct {
		User  User   `json:"user"`
		Token string `json:"token,omitempty" doc:"Session token; only when bearer was requested"`
	}
}

type logoutOutput struct {
	SetCookie *http.Cookie `header:"Set-Cookie"`
}

func sessionCookie(value string, maxAge time.Duration) *http.Cookie {
	c := &http.Cookie{
		Name: auth.CookieName, Value: value, Path: "/",
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
		MaxAge: int(maxAge.Seconds()),
	}
	if maxAge < 0 {
		c.MaxAge = -1
	}
	return c
}

func (h *handlers) registerAuth(g huma.API) {
	huma.Register(g, huma.Operation{
		OperationID: "request-login-code", Method: http.MethodPost, Path: "/auth/code",
		Summary: "Email a login code", Tags: []string{"auth"}, DefaultStatus: http.StatusAccepted,
		Description: fmt.Sprintf("Each email can request %d codes a day (Beijing time), %d seconds apart. "+
			"Answers the same way whether or not the address has an account.", auth.CodesPerDay, int(auth.ResendAfter.Seconds())),
	}, func(ctx context.Context, in *codeInput) (*codeOutput, error) {
		q, err := h.d.Auth.RequestCode(ctx, in.Body.Email, clientIP(ctx))
		wait := int(math.Ceil(q.ResendIn.Seconds()))
		switch {
		case errors.Is(err, auth.ErrInvalidEmail):
			return nil, huma.Error422UnprocessableEntity("邮箱地址格式不正确。")
		case errors.Is(err, auth.ErrRateLimited):
			return nil, huma.Error429TooManyRequests("请求过于频繁，请稍后再试。")
		case errors.Is(err, auth.ErrTooSoon):
			return nil, huma.ErrorWithHeaders(huma.Error429TooManyRequests(fmt.Sprintf("验证码已发送，请 %d 秒后再重新获取。", wait)),
				http.Header{"Retry-After": {strconv.Itoa(wait)}})
		case errors.Is(err, auth.ErrTryTomorrow):
			return nil, huma.Error429TooManyRequests(fmt.Sprintf("这个邮箱今天已获取过 %d 次验证码，请明天再试。", auth.CodesPerDay))
		case errors.Is(err, notify.ErrDailyLimit):
			return nil, huma.Error503ServiceUnavailable("今天的邮件发送额度已用完，请明天再试。")
		case err != nil:
			return nil, err
		}
		out := &codeOutput{Status: http.StatusAccepted}
		out.Body.Message = fmt.Sprintf("如果该邮箱可以登录，几分钟内会收到验证码，%d 分钟内有效。", int(auth.CodeTTL.Minutes()))
		out.Body.Remaining, out.Body.ResendAfter = q.Remaining, wait
		return out, nil
	})

	huma.Register(g, huma.Operation{
		OperationID: "verify-login", Method: http.MethodPost, Path: "/auth/verify",
		Summary: "Log in with an emailed code", Tags: []string{"auth"},
		Description: "Each code allows a few attempts and works once.",
	}, func(ctx context.Context, in *verifyInput) (*verifyOutput, error) {
		sess, err := h.d.Auth.Verify(ctx, in.Body.Email, in.Body.Code, in.UserAgent)
		switch {
		case errors.Is(err, auth.ErrInvalidCode):
			return nil, huma.Error400BadRequest("验证码不正确或已失效。")
		case errors.Is(err, auth.ErrDisabled):
			return nil, huma.Error403Forbidden("该账号已停用。")
		case err != nil:
			return nil, err
		}
		out := &verifyOutput{}
		out.Body.User = userFromDB(sess.User)
		if in.Body.Bearer {
			out.Body.Token = sess.Token
		} else {
			out.SetCookie = sessionCookie(sess.Token, sess.TTL)
		}
		return out, nil
	})

	huma.Register(g, huma.Operation{
		OperationID: "logout", Method: http.MethodPost, Path: "/auth/logout",
		Summary: "End the current session", Tags: []string{"auth"}, Security: loggedIn,
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, _ *struct{}) (*logoutOutput, error) {
		if err := h.d.Auth.Logout(ctx, principalFrom(ctx).Token); err != nil {
			return nil, err
		}
		return &logoutOutput{SetCookie: sessionCookie("", -1)}, nil
	})
}

// ---- me ----

// User is the account as its owner sees it.
type User struct {
	ID             uuid.UUID `json:"id"`
	Email          string    `json:"email"`
	Role           string    `json:"role" enum:"user,admin"`
	NotifyOnExpire bool      `json:"notify_on_expire" doc:"Email when a watch period ends (deletions are always emailed)"`
	CreatedAt      time.Time `json:"created_at"`
}

func userFromDB(u db.User) User {
	return User{ID: u.ID, Email: u.Email, Role: u.Role, NotifyOnExpire: u.NotifyOnExpire, CreatedAt: u.CreatedAt}
}

type userOutput struct{ Body User }

type patchMeInput struct {
	Body struct {
		NotifyOnExpire *bool `json:"notify_on_expire,omitempty"`
	}
}

func (h *handlers) registerMe(g huma.API) {
	huma.Register(g, huma.Operation{
		OperationID: "get-me", Method: http.MethodGet, Path: "/me",
		Summary: "Current user", Tags: []string{"me"}, Security: loggedIn,
	}, func(ctx context.Context, _ *struct{}) (*userOutput, error) {
		return &userOutput{Body: userFromDB(principalFrom(ctx).User)}, nil
	})

	huma.Register(g, huma.Operation{
		OperationID: "update-me", Method: http.MethodPatch, Path: "/me",
		Summary: "Change settings", Tags: []string{"me"}, Security: loggedIn,
	}, func(ctx context.Context, in *patchMeInput) (*userOutput, error) {
		u := principalFrom(ctx).User
		if in.Body.NotifyOnExpire != nil {
			var err error
			u, err = h.d.Queries.UpdateUserSettings(ctx, db.UpdateUserSettingsParams{ID: u.ID, NotifyOnExpire: *in.Body.NotifyOnExpire})
			if err != nil {
				return nil, err
			}
		}
		return &userOutput{Body: userFromDB(u)}, nil
	})

	huma.Register(g, huma.Operation{
		OperationID: "delete-me", Method: http.MethodDelete, Path: "/me",
		Summary: "Delete the account", Tags: []string{"me"}, Security: loggedIn, DefaultStatus: http.StatusNoContent,
		Description: "Deletes the account, its sessions and its watches, and removes the address from login and email records. Backups of articles are shared and stay.",
	}, func(ctx context.Context, _ *struct{}) (*logoutOutput, error) {
		if err := h.d.Auth.DeleteAccount(ctx, principalFrom(ctx).User.ID); err != nil {
			return nil, err
		}
		return &logoutOutput{SetCookie: sessionCookie("", -1)}, nil
	})
}

// ---- watches ----

// Article is what the service knows about an article.
type Article struct {
	ID             uuid.UUID  `json:"id"`
	Status         string     `json:"status" enum:"pending,alive,deleted,invalid,dormant"`
	Title          string     `json:"title,omitempty"`
	AccountName    string     `json:"account_name,omitempty"`
	Author         string     `json:"author,omitempty"`
	PublishedAt    *time.Time `json:"published_at,omitempty"`
	URL            string     `json:"url,omitempty" doc:"Link to the original article"`
	DeletedReason  string     `json:"deleted_reason,omitempty" doc:"author_deleted, violation, complaint, account_blocked or unknown"`
	DeletedMessage string     `json:"deleted_message,omitempty" doc:"WeChat's own wording"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`
	LastCheckedAt  *time.Time `json:"last_checked_at,omitempty"`
	NextCheckAt    *time.Time `json:"next_check_at,omitempty"`
}

// Watch is one user's watch of one article.
type Watch struct {
	ID           uuid.UUID  `json:"id"`
	Status       string     `json:"status" enum:"resolving,active,ended,failed"`
	EndReason    string     `json:"end_reason,omitempty" doc:"deleted, expired, user_stopped, invalid or unreachable"`
	SubmittedURL string     `json:"submitted_url"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    time.Time  `json:"expires_at"`
	EndedAt      *time.Time `json:"ended_at,omitempty"`
	Article      *Article   `json:"article,omitempty"`
}

// Snapshot is a stored backup.
type Snapshot struct {
	ID            uuid.UUID `json:"id"`
	CapturedAt    time.Time `json:"captured_at"`
	Images        int32     `json:"images"`
	MissingImages int32     `json:"missing_images"`
}

// Check is one check of an article.
type Check struct {
	CheckedAt time.Time `json:"checked_at"`
	Mode      string    `json:"mode"`
	Result    string    `json:"result" enum:"alive,deleted,invalid,blocked,error"`
	Reason    string    `json:"reason,omitempty"`
}

// WatchDetail is a watch with its article's backups and recent checks.
type WatchDetail struct {
	Watch     Watch      `json:"watch"`
	Snapshots []Snapshot `json:"snapshots" doc:"Empty while the backup is being made"`
	Checks    []Check    `json:"checks" doc:"Most recent first, at most 20"`
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

type submitInput struct {
	Body struct {
		URL string `json:"url" maxLength:"2000" doc:"A mp.weixin.qq.com article link"`
	}
}

type watchOutput struct {
	Status int
	Body   WatchDetail
}

type listInput struct {
	Status string `query:"status" enum:"resolving,active,ended,failed" required:"false"`
	Cursor string `query:"cursor" doc:"next_cursor from the previous page"`
	Limit  int    `query:"limit" minimum:"1" maximum:"100" default:"20"`
}

type listOutput struct {
	Body struct {
		Items      []Watch `json:"items"`
		NextCursor string  `json:"next_cursor,omitempty"`
	}
}

type idInput struct {
	ID uuid.UUID `path:"id"`
}

func (h *handlers) registerWatches(g huma.API) {
	huma.Register(g, huma.Operation{
		OperationID: "create-watch", Method: http.MethodPost, Path: "/watches",
		Summary: "Watch an article", Tags: []string{"watches"}, Security: loggedIn,
		Description: "201 when the article is already known (backed up, or already deleted); 202 while it is fetched for the first time — poll GET /watches/{id}.",
	}, func(ctx context.Context, in *submitInput) (*watchOutput, error) {
		u := principalFrom(ctx).User
		if u.Role != "admin" {
			if err := h.checkQuota(ctx, u.ID); err != nil {
				return nil, err
			}
		}
		w, err := h.d.Watch.Submit(ctx, u.ID, in.Body.URL)
		if errors.Is(err, watch.ErrBadLink) {
			return nil, huma.Error422UnprocessableEntity("这不是有效的微信公众号文章链接（需要 mp.weixin.qq.com 开头的完整链接）。")
		}
		if err != nil {
			return nil, err
		}
		detail, err := h.detail(ctx, u.ID, w.ID)
		if err != nil {
			return nil, err
		}
		status := http.StatusCreated
		if w.Status == "resolving" {
			status = http.StatusAccepted
		}
		return &watchOutput{Status: status, Body: *detail}, nil
	})

	huma.Register(g, huma.Operation{
		OperationID: "list-watches", Method: http.MethodGet, Path: "/watches",
		Summary: "My watches, newest first", Tags: []string{"watches"}, Security: loggedIn,
	}, func(ctx context.Context, in *listInput) (*listOutput, error) {
		p := db.ListWatchesPageParams{UserID: principalFrom(ctx).User.ID, MaxRows: int32(in.Limit) + 1}
		if in.Status != "" {
			p.Status = &in.Status
		}
		if in.Cursor != "" {
			c, err := uuid.Parse(in.Cursor)
			if err != nil {
				return nil, huma.Error400BadRequest("无效的分页参数。")
			}
			p.BeforeID = &c
		}
		rows, err := h.d.Queries.ListWatchesPage(ctx, p)
		if err != nil {
			return nil, err
		}
		out := &listOutput{}
		out.Body.Items = []Watch{}
		for i, r := range rows {
			if i == in.Limit {
				out.Body.NextCursor = rows[i-1].ID.String()
				break
			}
			w := Watch{ID: r.ID, Status: r.Status, EndReason: str(r.EndReason), SubmittedURL: r.SubmittedUrl,
				CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt, EndedAt: r.EndedAt}
			if r.ArticleID != nil {
				w.Article = &Article{ID: *r.ArticleID, Status: str(r.ArticleStatus), Title: str(r.Title),
					AccountName: str(r.AccountName), DeletedReason: str(r.DeletedReasonCode)}
			}
			out.Body.Items = append(out.Body.Items, w)
		}
		return out, nil
	})

	huma.Register(g, huma.Operation{
		OperationID: "get-watch", Method: http.MethodGet, Path: "/watches/{id}",
		Summary: "A watch with its backups and checks", Tags: []string{"watches"}, Security: loggedIn,
		Description: "404 also when a second link of an already watched article was merged into the existing watch; reload the list.",
	}, func(ctx context.Context, in *idInput) (*watchOutput, error) {
		d, err := h.detail(ctx, principalFrom(ctx).User.ID, in.ID)
		if err != nil {
			return nil, err
		}
		return &watchOutput{Status: http.StatusOK, Body: *d}, nil
	})

	huma.Register(g, huma.Operation{
		OperationID: "stop-watch", Method: http.MethodDelete, Path: "/watches/{id}",
		Summary: "Stop watching", Tags: []string{"watches"}, Security: loggedIn, DefaultStatus: http.StatusNoContent,
		Description: "Ends the watch. Its backups stay available to this user.",
	}, func(ctx context.Context, in *idInput) (*struct{}, error) {
		uid := principalFrom(ctx).User.ID
		n, err := h.d.Queries.StopWatch(ctx, db.StopWatchParams{ID: in.ID, UserID: uid, Now: time.Now()})
		if err != nil {
			return nil, err
		}
		if n == 0 { // already over, or not ours
			if _, err := h.d.Queries.GetUserWatch(ctx, db.GetUserWatchParams{ID: in.ID, UserID: uid}); errors.Is(err, pgx.ErrNoRows) {
				return nil, huma.Error404NotFound("没有找到这个观察。")
			} else if err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
}

func (h *handlers) checkQuota(ctx context.Context, userID uuid.UUID) error {
	open, err := h.d.Queries.CountOpenWatches(ctx, userID)
	if err != nil {
		return err
	}
	if lim := h.d.Limits.MaxOpenWatches; lim > 0 && open >= int64(lim) {
		return huma.Error429TooManyRequests("同时观察的文章已达上限（" + strconv.Itoa(lim) + " 篇），请先停止一些观察。")
	}
	today, err := h.d.Queries.CountWatchesSince(ctx, db.CountWatchesSinceParams{UserID: userID, Since: time.Now().Add(-24 * time.Hour)})
	if err != nil {
		return err
	}
	if lim := h.d.Limits.MaxDailySubmission; lim > 0 && today >= int64(lim) {
		return huma.Error429TooManyRequests("24 小时内提交的链接已达上限（" + strconv.Itoa(lim) + " 个），请稍后再试。")
	}
	return nil
}

func (h *handlers) detail(ctx context.Context, userID, watchID uuid.UUID) (*WatchDetail, error) {
	r, err := h.d.Queries.GetUserWatch(ctx, db.GetUserWatchParams{ID: watchID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error404NotFound("没有找到这个观察。")
	}
	if err != nil {
		return nil, err
	}
	w := r.Watch
	d := &WatchDetail{
		Watch: Watch{ID: w.ID, Status: w.Status, EndReason: str(w.EndReason), SubmittedURL: w.SubmittedUrl,
			CreatedAt: w.CreatedAt, ExpiresAt: w.ExpiresAt, EndedAt: w.EndedAt},
		Snapshots: []Snapshot{}, Checks: []Check{},
	}
	if w.ArticleID == nil {
		return d, nil
	}
	d.Watch.Article = &Article{
		ID: *w.ArticleID, Status: str(r.ArticleStatus), Title: str(r.Title), AccountName: str(r.AccountName),
		Author: str(r.Author), PublishedAt: r.PublishedAt, URL: str(r.CanonicalUrl),
		DeletedReason: str(r.DeletedReasonCode), DeletedMessage: str(r.DeletedReasonText), DeletedAt: r.DeletedAt,
		LastCheckedAt: r.LastCheckedAt, NextCheckAt: r.NextCheckAt,
	}
	if d.Watch.Article.URL == "" {
		d.Watch.Article.URL = w.SubmittedUrl
	}
	snaps, err := h.d.Queries.ListSnapshots(ctx, *w.ArticleID)
	if err != nil {
		return nil, err
	}
	for _, s := range snaps {
		d.Snapshots = append(d.Snapshots, Snapshot{ID: s.ID, CapturedAt: s.CapturedAt, Images: s.AssetCount, MissingImages: s.MissingAssets})
	}
	checks, err := h.d.Queries.ListRecentCheckRuns(ctx, db.ListRecentCheckRunsParams{ArticleID: *w.ArticleID, MaxRows: 20})
	if err != nil {
		return nil, err
	}
	for _, c := range checks {
		d.Checks = append(d.Checks, Check{CheckedAt: c.CheckedAt, Mode: c.Mode, Result: c.Result, Reason: str(c.Reason)})
	}
	return d, nil
}

// ---- snapshots ----

type linkOutput struct {
	Body struct {
		URL       string    `json:"url"`
		ExpiresAt time.Time `json:"expires_at"`
	}
}

func (h *handlers) registerSnapshots(g huma.API) {
	for _, op := range []struct{ id, path, kind, summary string }{
		{"snapshot-view-url", "/snapshots/{id}/view-url", snapserver.KindView, "Signed link to read a backup online (1 hour)"},
		{"snapshot-download-url", "/snapshots/{id}/download-url", snapserver.KindDownload, "Signed link to download a backup as one HTML file (10 minutes)"},
	} {
		huma.Register(g, huma.Operation{
			OperationID: op.id, Method: http.MethodPost, Path: op.path,
			Summary: op.summary, Tags: []string{"snapshots"}, Security: loggedIn,
			Description: "Only users who have watched the article may read its backups.",
		}, func(ctx context.Context, in *idInput) (*linkOutput, error) {
			ok, err := h.d.Queries.UserCanViewSnapshot(ctx, db.UserCanViewSnapshotParams{UserID: principalFrom(ctx).User.ID, SnapshotID: in.ID})
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, huma.Error404NotFound("没有找到这份备份。")
			}
			out := &linkOutput{}
			out.Body.URL, out.Body.ExpiresAt = h.d.Signer.URL(h.d.SnapBaseURL, op.kind, in.ID, time.Now())
			return out, nil
		})
	}
}
