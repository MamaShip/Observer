package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/MamaShip/Observer/internal/auth"
	"github.com/MamaShip/Observer/internal/notify"
	"github.com/MamaShip/Observer/internal/store/db"
	"github.com/MamaShip/Observer/internal/watch"
)

// The admin console's API (plan.md §10.1). Every operation needs a session
// of an account with role admin.

// Thresholds for flagging a backup as possibly incomplete; keep in step
// with the AdminListArticles query.
const (
	shortTextRunes = 100
	failingChecks  = 3
	trendDays      = 30
)

func requireAdmin(ctx context.Context) error {
	if p := principalFrom(ctx); p == nil || p.User.Role != "admin" {
		return huma.Error403Forbidden("需要管理员权限。")
	}
	return nil
}

// ---- overview ----

// AdminTotals are counts over all time.
type AdminTotals struct {
	Users           int   `json:"users"`
	DisabledUsers   int   `json:"disabled_users"`
	ActiveUsers30d  int   `json:"active_users_30d" doc:"Logged in during the last 30 days"`
	OpenWatches     int   `json:"open_watches"`
	Watches         int   `json:"watches"`
	Articles        int   `json:"articles"`
	ArticlesAlive   int   `json:"articles_alive"`
	ArticlesDeleted int   `json:"articles_deleted"`
	ArticlesInvalid int   `json:"articles_invalid"`
	ArticlesDormant int   `json:"articles_dormant"`
	ArticlesPending int   `json:"articles_pending"`
	Snapshots       int   `json:"snapshots"`
	SnapshotBytes   int64 `json:"snapshot_bytes" doc:"Reading pages; raw page copies are not counted"`
	Images          int   `json:"images"`
	ImageBytes      int64 `json:"image_bytes"`
}

// AdminUsage is activity since a point in time.
type AdminUsage struct {
	Emails             int   `json:"emails"`
	LoginEmails        int   `json:"login_emails"`
	NotificationEmails int   `json:"notification_emails"`
	AlertEmails        int   `json:"alert_emails"`
	FailedEmails       int   `json:"failed_emails"`
	Checks             int   `json:"checks"`
	Blocked            int   `json:"blocked"`
	Errors             int   `json:"errors"`
	PageBytes          int64 `json:"page_bytes" doc:"WeChat pages fetched (compressed size on the wire); images not included"`
	NewImageBytes      int64 `json:"new_image_bytes" doc:"Images stored for the first time; roughly the image download traffic"`
	NewUsers           int   `json:"new_users"`
	NewWatches         int   `json:"new_watches"`
	NewArticles        int   `json:"new_articles"`
	Deletions          int   `json:"deletions"`
	NewSnapshots       int   `json:"new_snapshots"`
}

// AdminDay is one Beijing day of activity.
type AdminDay struct {
	Day       string `json:"day" doc:"YYYY-MM-DD, Beijing time"`
	Watches   int    `json:"watches"`
	Users     int    `json:"users"`
	Deletions int    `json:"deletions"`
	Checks    int    `json:"checks"`
	Blocked   int    `json:"blocked"`
	Emails    int    `json:"emails"`
	Snapshots int    `json:"snapshots"`
}

// AdminLimits are the configured limits the usage is measured against.
type AdminLimits struct {
	DailyEmails    int `json:"daily_emails" doc:"Provider's daily sending limit (DAILY_EMAIL_LIMIT), counted from midnight UTC"`
	MonthlyEmails  int `json:"monthly_emails" doc:"Default notification emails per account per month"`
	MaxOpenWatches int `json:"max_open_watches"`
}

// AdminOverview is the console's front page.
type AdminOverview struct {
	Totals      AdminTotals `json:"totals"`
	Month       AdminUsage  `json:"month" doc:"Since the start of this month, Beijing time"`
	Today       AdminUsage  `json:"today" doc:"Since midnight Beijing time"`
	EmailsToday int         `json:"emails_today" doc:"Emails sent since midnight UTC, against limits.daily_emails"`
	Limits      AdminLimits `json:"limits"`
	MonthStart  time.Time   `json:"month_start"`
	Days        []AdminDay  `json:"days" doc:"The last 30 days, oldest first"`
	Health      Health      `json:"health"`
}

type adminOverviewOutput struct{ Body AdminOverview }

func usageFrom(r db.AdminUsageRow) AdminUsage {
	return AdminUsage{
		Emails: int(r.Emails), LoginEmails: int(r.LoginEmails), NotificationEmails: int(r.NotificationEmails),
		AlertEmails: int(r.AlertEmails), FailedEmails: int(r.FailedEmails), Checks: int(r.Checks),
		Blocked: int(r.Blocked), Errors: int(r.Errors), PageBytes: r.PageBytes, NewImageBytes: r.NewAssetBytes,
		NewUsers: int(r.NewUsers), NewWatches: int(r.NewWatches), NewArticles: int(r.NewArticles),
		Deletions: int(r.Deletions), NewSnapshots: int(r.NewSnapshots),
	}
}

// beijingDay is midnight Beijing time on t's day.
func beijingDay(t time.Time) time.Time {
	y, m, d := t.In(auth.Beijing).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, auth.Beijing)
}

func (h *handlers) overview(ctx context.Context) (*AdminOverview, error) {
	now := time.Now()
	q := h.d.Queries
	t, err := q.AdminTotals(ctx)
	if err != nil {
		return nil, err
	}
	o := &AdminOverview{
		Totals: AdminTotals{
			Users: int(t.Users), DisabledUsers: int(t.DisabledUsers), ActiveUsers30d: int(t.ActiveUsers30d),
			OpenWatches: int(t.OpenWatches), Watches: int(t.Watches), Articles: int(t.Articles),
			ArticlesAlive: int(t.ArticlesAlive), ArticlesDeleted: int(t.ArticlesDeleted), ArticlesInvalid: int(t.ArticlesInvalid),
			ArticlesDormant: int(t.ArticlesDormant), ArticlesPending: int(t.ArticlesPending),
			Snapshots: int(t.Snapshots), SnapshotBytes: t.SnapshotBytes, Images: int(t.Assets), ImageBytes: t.AssetBytes,
		},
		MonthStart: notify.MonthStart(now),
		Limits: AdminLimits{
			DailyEmails: h.d.Limits.DailyEmails, MonthlyEmails: h.d.Site.MonthlyEmails, MaxOpenWatches: h.d.Limits.MaxOpenWatches,
		},
	}
	month, err := q.AdminUsage(ctx, o.MonthStart)
	if err != nil {
		return nil, err
	}
	o.Month = usageFrom(month)
	today := beijingDay(now)
	day, err := q.AdminUsage(ctx, today)
	if err != nil {
		return nil, err
	}
	o.Today = usageFrom(day)
	sent, err := q.CountEmailsSince(ctx, now.UTC().Truncate(24*time.Hour))
	if err != nil {
		return nil, err
	}
	o.EmailsToday = int(sent)

	first := today.AddDate(0, 0, -(trendDays - 1))
	rows, err := q.AdminDailyCounts(ctx, first)
	if err != nil {
		return nil, err
	}
	index := map[string]*AdminDay{}
	for i := range trendDays {
		d := first.AddDate(0, 0, i).Format("2006-01-02")
		o.Days = append(o.Days, AdminDay{Day: d})
	}
	for i := range o.Days {
		index[o.Days[i].Day] = &o.Days[i]
	}
	for _, r := range rows {
		d := index[r.Day.Format("2006-01-02")]
		if d == nil {
			continue
		}
		n := int(r.N)
		switch r.Metric {
		case "watches":
			d.Watches = n
		case "users":
			d.Users = n
		case "deletions":
			d.Deletions = n
		case "checks":
			d.Checks = n
		case "blocked":
			d.Blocked = n
		case "emails":
			d.Emails = n
		case "snapshots":
			d.Snapshots = n
		}
	}
	if h.d.Health != nil {
		o.Health = checkHealth(ctx, h.d).Body
	}
	return o, nil
}

// ---- users ----

// AdminUser is an account with its usage.
type AdminUser struct {
	ID               uuid.UUID  `json:"id"`
	Email            string     `json:"email"`
	Role             string     `json:"role" enum:"user,admin"`
	Status           string     `json:"status" enum:"active,disabled"`
	CreatedAt        time.Time  `json:"created_at"`
	LastLoginAt      *time.Time `json:"last_login_at,omitempty"`
	OpenWatches      int        `json:"open_watches"`
	TotalWatches     int        `json:"total_watches"`
	WatchesThisMonth int        `json:"watches_this_month"`
	EmailsThisMonth  int        `json:"emails_this_month" doc:"Notification emails this month"`
	MonthlyEmails    int        `json:"monthly_emails" doc:"This account's limit in effect; 0 means none"`
	CustomEmailLimit bool       `json:"custom_email_limit" doc:"The limit is set for this account rather than the default"`
	Invited          bool       `json:"invited" doc:"The account was created by someone's invitation (false: by the command line or before invitations were recorded)"`
	InvitedByID      *uuid.UUID `json:"invited_by_id,omitempty" doc:"Absent when the inviter deleted their account"`
	InvitedBy        string     `json:"invited_by,omitempty" doc:"The inviter's email"`
	Invitees         int        `json:"invitees" doc:"Accounts this user's invitations created"`
	InvitesThisMonth int        `json:"invites_this_month" doc:"Invitations sent this month, including to addresses that had an account"`
}

func (h *handlers) adminUser(r db.AdminListUsersRow) AdminUser {
	u := AdminUser{
		ID: r.ID, Email: r.Email, Role: r.Role, Status: r.Status, CreatedAt: r.CreatedAt, LastLoginAt: r.LastLoginAt,
		OpenWatches: int(r.OpenWatches), TotalWatches: int(r.TotalWatches), WatchesThisMonth: int(r.WatchesThisMonth),
		EmailsThisMonth: int(r.EmailsThisMonth), MonthlyEmails: h.d.Site.MonthlyEmails,
		Invited: r.Invited, InvitedByID: r.InviterID, InvitedBy: r.InviterEmail,
		Invitees: int(r.Invitees), InvitesThisMonth: int(r.InvitesThisMonth),
	}
	if r.MonthlyEmailLimit != nil {
		u.MonthlyEmails, u.CustomEmailLimit = int(*r.MonthlyEmailLimit), true
	}
	return u
}

func (h *handlers) getAdminUser(ctx context.Context, id uuid.UUID) (*AdminUser, error) {
	rows, err := h.d.Queries.AdminListUsers(ctx, db.AdminListUsersParams{MonthStart: notify.MonthStart(time.Now()), OnlyID: &id, MaxRows: 1})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, huma.Error404NotFound("没有这个账户。")
	}
	u := h.adminUser(rows[0])
	return &u, nil
}

type adminUsersInput struct {
	Search    string `query:"search" maxLength:"254" doc:"Part of an email address"`
	InvitedBy string `query:"invited_by" doc:"Only accounts created by this user's invitations (a user id)"`
	Cursor    string `query:"cursor"`
	Limit     int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type adminUsersOutput struct {
	Body struct {
		Items      []AdminUser `json:"items"`
		NextCursor string      `json:"next_cursor,omitempty"`
	}
}

type adminUserOutput struct {
	Status int
	Body   AdminUser
}

type adminInviteInput struct {
	Body struct {
		Email string `json:"email" maxLength:"254"`
	}
}

type adminPatchUserInput struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		Status            *string `json:"status,omitempty" enum:"active,disabled" doc:"disabled also ends every session"`
		Role              *string `json:"role,omitempty" enum:"user,admin"`
		MonthlyEmails     *int    `json:"monthly_emails,omitempty" minimum:"0" maximum:"100000" doc:"Notification emails per month for this account; 0 means no limit"`
		DefaultEmailLimit bool    `json:"default_email_limit,omitempty" doc:"Go back to the service default"`
	}
}

// ---- articles ----

// AdminSnapshot describes one stored backup.
type AdminSnapshot struct {
	ID            uuid.UUID `json:"id"`
	CapturedAt    time.Time `json:"captured_at"`
	Images        int       `json:"images"`
	MissingImages int       `json:"missing_images"`
	TextRunes     *int      `json:"text_runes,omitempty" doc:"Characters of text; absent for backups made before it was recorded"`
	SizeBytes     int64     `json:"size_bytes,omitempty"`
}

// AdminArticle is an article as the console lists it.
type AdminArticle struct {
	ID                  uuid.UUID      `json:"id"`
	Status              string         `json:"status" enum:"pending,alive,deleted,invalid,dormant"`
	Title               string         `json:"title,omitempty"`
	AccountName         string         `json:"account_name,omitempty"`
	FirstSeenAt         time.Time      `json:"first_seen_at"`
	LastCheckedAt       *time.Time     `json:"last_checked_at,omitempty"`
	DeletedAt           *time.Time     `json:"deleted_at,omitempty"`
	DeletedReason       string         `json:"deleted_reason,omitempty"`
	ConsecutiveFailures int            `json:"consecutive_failures"`
	Watchers            int            `json:"watchers"`
	Snapshots           int            `json:"snapshots"`
	Latest              *AdminSnapshot `json:"latest,omitempty" doc:"The newest backup"`
	Issues              []string       `json:"issues" doc:"backup_failed, no_snapshot, missing_images, short_text, failing_checks"`
}

// articleIssues flags a backup that may be incomplete. captureStatus is the
// newest capture's (pending, done, failed or "" for none).
func articleIssues(status string, latest *AdminSnapshot, failures int, captureStatus string) []string {
	issues := []string{}
	if captureStatus == "failed" {
		issues = append(issues, "backup_failed")
	}
	if latest == nil {
		// backup_failed already says why there is none.
		if captureStatus != "failed" && (status == "alive" || status == "deleted" || status == "dormant") {
			issues = append(issues, "no_snapshot")
		}
	} else {
		if latest.MissingImages > 0 {
			issues = append(issues, "missing_images")
		}
		if latest.TextRunes != nil && *latest.TextRunes < shortTextRunes {
			issues = append(issues, "short_text")
		}
	}
	if failures >= failingChecks {
		issues = append(issues, "failing_checks")
	}
	return issues
}

func intPtr(p *int32) *int {
	if p == nil {
		return nil
	}
	v := int(*p)
	return &v
}

type adminArticlesInput struct {
	Status string `query:"status" enum:"pending,alive,deleted,invalid,dormant" required:"false"`
	Issues bool   `query:"issues" doc:"Only articles whose backup looks incomplete"`
	Search string `query:"search" maxLength:"200" doc:"Part of the title or account name"`
	Cursor string `query:"cursor"`
	Limit  int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
}

type adminArticlesOutput struct {
	Body struct {
		Items      []AdminArticle `json:"items"`
		NextCursor string         `json:"next_cursor,omitempty"`
	}
}

// AdminCheck is one check with its technical details.
type AdminCheck struct {
	CheckedAt  time.Time `json:"checked_at"`
	Mode       string    `json:"mode"`
	Result     string    `json:"result" enum:"alive,deleted,invalid,blocked,error"`
	Reason     string    `json:"reason,omitempty"`
	Rule       string    `json:"rule,omitempty"`
	HTTPStatus *int      `json:"http_status,omitempty"`
	WireBytes  *int      `json:"wire_bytes,omitempty"`
	DurationMs *int      `json:"duration_ms,omitempty"`
}

// AdminWatcher is one account's watch of the article.
type AdminWatcher struct {
	Email     string     `json:"email"`
	Status    string     `json:"status" enum:"resolving,active,ended,failed"`
	EndReason string     `json:"end_reason,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
}

// AdminArticleDetail is everything about one article.
type AdminArticleDetail struct {
	Article       Article         `json:"article"`
	Issues        []string        `json:"issues"`
	Failures      int             `json:"consecutive_failures"`
	FirstSeenAt   time.Time       `json:"first_seen_at"`
	Snapshots     []AdminSnapshot `json:"snapshots" doc:"Newest first"`
	Checks        []AdminCheck    `json:"checks" doc:"Newest first, at most 50"`
	Watchers      []AdminWatcher  `json:"watchers"`
	CanResnapshot bool            `json:"can_resnapshot" doc:"The article is online, so a new backup can be made"`
	Captures      []AdminCapture  `json:"captures" doc:"Pages fetched to be backed up, newest first, at most 50"`
}

// AdminCapture is a page fetched to be backed up, and what became of it.
type AdminCapture struct {
	ID         uuid.UUID `json:"id" doc:"Also the snapshot's id once built"`
	CapturedAt time.Time `json:"captured_at"`
	Status     string    `json:"status" enum:"pending,done,failed"`
	ErrorCode  string    `json:"error_code,omitempty" doc:"no_content (no article body: picture or video posts) or error"`
	Error      string    `json:"error,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type adminArticleOutput struct{ Body AdminArticleDetail }

func (h *handlers) registerAdmin(g huma.API) {
	tags := []string{"admin"}

	huma.Register(g, huma.Operation{
		OperationID: "admin-overview", Method: http.MethodGet, Path: "/admin/overview",
		Summary: "Totals, this month's usage and 30-day trends", Tags: tags, Security: loggedIn,
	}, func(ctx context.Context, _ *struct{}) (*adminOverviewOutput, error) {
		if err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		o, err := h.overview(ctx)
		if err != nil {
			return nil, err
		}
		return &adminOverviewOutput{Body: *o}, nil
	})

	huma.Register(g, huma.Operation{
		OperationID: "admin-list-users", Method: http.MethodGet, Path: "/admin/users",
		Summary: "Accounts with their usage, newest first", Tags: tags, Security: loggedIn,
	}, func(ctx context.Context, in *adminUsersInput) (*adminUsersOutput, error) {
		if err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		p := db.AdminListUsersParams{MonthStart: notify.MonthStart(time.Now()), MaxRows: int32(in.Limit) + 1}
		if in.Search != "" {
			p.Search = &in.Search
		}
		if in.InvitedBy != "" {
			id, err := uuid.Parse(in.InvitedBy)
			if err != nil {
				return nil, huma.Error400BadRequest("invited_by 应为用户 ID。")
			}
			p.InvitedBy = &id
		}
		if in.Cursor != "" {
			c, err := uuid.Parse(in.Cursor)
			if err != nil {
				return nil, huma.Error400BadRequest("无效的分页参数。")
			}
			p.Before = &c
		}
		rows, err := h.d.Queries.AdminListUsers(ctx, p)
		if err != nil {
			return nil, err
		}
		out := &adminUsersOutput{}
		out.Body.Items = []AdminUser{}
		for i, r := range rows {
			if i == in.Limit {
				out.Body.NextCursor = rows[i-1].ID.String()
				break
			}
			out.Body.Items = append(out.Body.Items, h.adminUser(r))
		}
		return out, nil
	})

	huma.Register(g, huma.Operation{
		OperationID: "admin-invite-user", Method: http.MethodPost, Path: "/admin/users",
		Summary: "Invite an email address (create the account)", Tags: tags, Security: loggedIn,
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *adminInviteInput) (*adminUserOutput, error) {
		if err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		u, created, err := h.d.Auth.CreateInvited(ctx, &principalFrom(ctx).User.ID, in.Body.Email)
		if errors.Is(err, auth.ErrInvalidEmail) {
			return nil, huma.Error422UnprocessableEntity("邮箱地址格式不正确。")
		}
		if err != nil {
			return nil, err
		}
		status := http.StatusOK // already there
		if created {
			status = http.StatusCreated
		}
		au, err := h.getAdminUser(ctx, u.ID)
		if err != nil {
			return nil, err
		}
		return &adminUserOutput{Status: status, Body: *au}, nil
	})

	huma.Register(g, huma.Operation{
		OperationID: "admin-update-user", Method: http.MethodPatch, Path: "/admin/users/{id}",
		Summary: "Disable or enable an account, change its role or email limit", Tags: tags, Security: loggedIn,
	}, func(ctx context.Context, in *adminPatchUserInput) (*adminUserOutput, error) {
		if err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		me := principalFrom(ctx).User
		target, err := h.d.Queries.GetUser(ctx, in.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("没有这个账户。")
		}
		if err != nil {
			return nil, err
		}
		b := in.Body
		if target.ID == me.ID && ((b.Status != nil && *b.Status != "active") || (b.Role != nil && *b.Role != "admin")) {
			return nil, huma.Error400BadRequest("不能停用自己，也不能取消自己的管理员身份。")
		}
		if b.Status != nil {
			if _, err := h.d.Queries.SetUserStatus(ctx, db.SetUserStatusParams{Email: target.Email, Status: *b.Status}); err != nil {
				return nil, err
			}
			if *b.Status == "disabled" {
				if _, err := h.d.Queries.DeleteUserSessions(ctx, target.ID); err != nil {
					return nil, err
				}
			}
		}
		if b.Role != nil {
			if _, err := h.d.Queries.SetUserRole(ctx, db.SetUserRoleParams{Email: target.Email, Role: *b.Role}); err != nil {
				return nil, err
			}
		}
		switch {
		case b.DefaultEmailLimit:
			_, err = h.d.Queries.SetMonthlyEmailLimit(ctx, db.SetMonthlyEmailLimitParams{Email: target.Email})
		case b.MonthlyEmails != nil:
			v := int32(*b.MonthlyEmails)
			_, err = h.d.Queries.SetMonthlyEmailLimit(ctx, db.SetMonthlyEmailLimitParams{Email: target.Email, MonthlyEmailLimit: &v})
		}
		if err != nil {
			return nil, err
		}
		au, err := h.getAdminUser(ctx, target.ID)
		if err != nil {
			return nil, err
		}
		return &adminUserOutput{Status: http.StatusOK, Body: *au}, nil
	})

	huma.Register(g, huma.Operation{
		OperationID: "admin-list-articles", Method: http.MethodGet, Path: "/admin/articles",
		Summary: "Backed-up articles, newest first, with completeness flags", Tags: tags, Security: loggedIn,
	}, func(ctx context.Context, in *adminArticlesInput) (*adminArticlesOutput, error) {
		if err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		p := db.AdminListArticlesParams{IssuesOnly: in.Issues, MaxRows: int32(in.Limit) + 1}
		if in.Status != "" {
			p.Status = &in.Status
		}
		if in.Search != "" {
			p.Search = &in.Search
		}
		if in.Cursor != "" {
			c, err := uuid.Parse(in.Cursor)
			if err != nil {
				return nil, huma.Error400BadRequest("无效的分页参数。")
			}
			p.Before = &c
		}
		rows, err := h.d.Queries.AdminListArticles(ctx, p)
		if err != nil {
			return nil, err
		}
		out := &adminArticlesOutput{}
		out.Body.Items = []AdminArticle{}
		for i, r := range rows {
			if i == in.Limit {
				out.Body.NextCursor = rows[i-1].ID.String()
				break
			}
			a := AdminArticle{
				ID: r.ID, Status: r.Status, Title: str(r.Title), AccountName: str(r.AccountName),
				FirstSeenAt: r.FirstSeenAt, LastCheckedAt: r.LastCheckedAt, DeletedAt: r.DeletedAt,
				DeletedReason: str(r.DeletedReasonCode), ConsecutiveFailures: int(r.ConsecutiveFailures),
				Watchers: int(r.Watchers), Snapshots: int(r.Snapshots),
			}
			if r.HasSnapshot {
				a.Latest = &AdminSnapshot{ID: r.SnapshotID, CapturedAt: r.CapturedAt, Images: int(r.AssetCount),
					MissingImages: int(r.MissingAssets), TextRunes: intPtr(r.TextRunes)}
			}
			a.Issues = articleIssues(a.Status, a.Latest, a.ConsecutiveFailures, r.CaptureStatus)
			out.Body.Items = append(out.Body.Items, a)
		}
		return out, nil
	})

	huma.Register(g, huma.Operation{
		OperationID: "admin-get-article", Method: http.MethodGet, Path: "/admin/articles/{id}",
		Summary: "An article with all its backups, checks and watchers", Tags: tags, Security: loggedIn,
	}, func(ctx context.Context, in *idInput) (*adminArticleOutput, error) {
		if err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		q := h.d.Queries
		art, err := q.GetArticle(ctx, in.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, huma.Error404NotFound("没有这篇文章。")
		}
		if err != nil {
			return nil, err
		}
		d := AdminArticleDetail{
			Article: Article{
				ID: art.ID, Status: art.Status, Title: str(art.Title), AccountName: str(art.AccountName), Author: str(art.Author),
				PublishedAt: art.PublishedAt, URL: str(art.CanonicalUrl), DeletedReason: str(art.DeletedReasonCode),
				DeletedMessage: str(art.DeletedReasonText), DeletedAt: art.DeletedAt, LastCheckedAt: art.LastCheckedAt,
				NextCheckAt: art.NextCheckAt,
			},
			Failures: int(art.ConsecutiveFailures), FirstSeenAt: art.FirstSeenAt,
			Snapshots: []AdminSnapshot{}, Checks: []AdminCheck{}, Watchers: []AdminWatcher{}, Captures: []AdminCapture{},
			CanResnapshot: (art.Status == "alive" || art.Status == "dormant") && art.CanonicalUrl != nil,
		}
		snaps, err := q.ListSnapshots(ctx, art.ID)
		if err != nil {
			return nil, err
		}
		for _, s := range snaps {
			d.Snapshots = append(d.Snapshots, AdminSnapshot{ID: s.ID, CapturedAt: s.CapturedAt, Images: int(s.AssetCount),
				MissingImages: int(s.MissingAssets), TextRunes: intPtr(s.TextRunes), SizeBytes: s.SizeBytes})
		}
		var latest *AdminSnapshot
		if len(d.Snapshots) > 0 {
			latest = &d.Snapshots[0]
		}
		caps, err := q.ListCaptures(ctx, art.ID)
		if err != nil {
			return nil, err
		}
		for _, c := range caps {
			d.Captures = append(d.Captures, AdminCapture{ID: c.ID, CapturedAt: c.CapturedAt, Status: c.Status,
				ErrorCode: str(c.ErrorCode), Error: str(c.Error), UpdatedAt: c.UpdatedAt})
		}
		captureStatus := ""
		if len(caps) > 0 {
			captureStatus = caps[0].Status
		}
		d.Issues = articleIssues(art.Status, latest, d.Failures, captureStatus)
		checks, err := q.AdminCheckRuns(ctx, db.AdminCheckRunsParams{ArticleID: art.ID, MaxRows: 50})
		if err != nil {
			return nil, err
		}
		for _, c := range checks {
			d.Checks = append(d.Checks, AdminCheck{CheckedAt: c.CheckedAt, Mode: c.Mode, Result: c.Result, Reason: str(c.Reason),
				Rule: str(c.Rule), HTTPStatus: intPtr(c.HttpStatus), WireBytes: intPtr(c.WireBytes), DurationMs: intPtr(c.DurationMs)})
		}
		watchers, err := q.AdminArticleWatchers(ctx, &art.ID)
		if err != nil {
			return nil, err
		}
		for _, w := range watchers {
			d.Watchers = append(d.Watchers, AdminWatcher{Email: w.Email, Status: w.Status, EndReason: str(w.EndReason),
				CreatedAt: w.CreatedAt, EndedAt: w.EndedAt})
		}
		return &adminArticleOutput{Body: d}, nil
	})

	huma.Register(g, huma.Operation{
		OperationID: "admin-resnapshot", Method: http.MethodPost, Path: "/admin/articles/{id}/resnapshot",
		Summary: "Fetch an online article again and make a new backup", Tags: tags, Security: loggedIn,
		DefaultStatus: http.StatusAccepted,
		Description:   "Queued: the page is fetched within the global rate limit, then the snapshot is built. Earlier backups stay.",
	}, func(ctx context.Context, in *idInput) (*struct{}, error) {
		if err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		err := h.d.Watch.QueueResnapshot(ctx, in.ID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil, huma.Error404NotFound("没有这篇文章。")
		case errors.Is(err, watch.ErrNotOnline):
			return nil, huma.Error409Conflict("文章已不在线（或从未抓取成功），无法重新备份。")
		}
		return nil, err
	})

	huma.Register(g, huma.Operation{
		OperationID: "admin-rebuild-capture", Method: http.MethodPost, Path: "/admin/captures/{id}/rebuild",
		Summary: "Build a failed backup again from its stored page", Tags: tags, Security: loggedIn,
		DefaultStatus: http.StatusAccepted,
		Description:   "Queued. WeChat is not contacted, so this works after the article was deleted.",
	}, func(ctx context.Context, in *idInput) (*struct{}, error) {
		if err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		err := h.d.Watch.RebuildCapture(ctx, in.ID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil, huma.Error404NotFound("没有这次抓取。")
		case errors.Is(err, watch.ErrCaptureDone):
			return nil, huma.Error409Conflict("这次抓取已经生成了备份。")
		}
		return nil, err
	})
}
