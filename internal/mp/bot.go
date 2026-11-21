package mp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/MamaShip/Observer/internal/auth"
	"github.com/MamaShip/Observer/internal/store/db"
	"github.com/MamaShip/Observer/internal/watch"
	"github.com/MamaShip/Observer/internal/wechat"
)

// Accounts is the part of auth.Service the bot uses.
type Accounts interface {
	WechatBindingFor(ctx context.Context, openid string) (auth.WechatBinding, bool, error)
	NewWechatBindLink(ctx context.Context, openid, pendingURL string) (string, error)
	NewWechatLoginLink(ctx context.Context, openid string, userID uuid.UUID) (string, error)
	BindWechat(ctx context.Context, userID uuid.UUID, token string) (string, error)
	ForgetWechat(ctx context.Context, openid string) (bool, error)
}

// Watches is the part of watch.Service the bot uses.
type Watches interface {
	SubmitLink(ctx context.Context, userID uuid.UUID, rawURL string, quota watch.Quota) (watch.Submission, error)
	MarkDeletionNews(ctx context.Context, userID uuid.UUID, ids []uuid.UUID) ([]uuid.UUID, error)
}

// Store is the part of db.Queries the bot reads.
type Store interface {
	GetArticle(ctx context.Context, id uuid.UUID) (db.Article, error)
	GetUserWatch(ctx context.Context, arg db.GetUserWatchParams) (db.GetUserWatchRow, error)
	ListWatchesPage(ctx context.Context, arg db.ListWatchesPageParams) ([]db.ListWatchesPageRow, error)
	CountOpenWatches(ctx context.Context, userID uuid.UUID) (int64, error)
	ListWechatNews(ctx context.Context, arg db.ListWechatNewsParams) ([]db.ListWechatNewsRow, error)
}

// Config configures the bot.
type Config struct {
	BaseURL     string // the site's origin, for links
	ServiceName string // the website's name
	AccountName string // the Official Account's name; ServiceName if empty
	InviteOnly  bool   // registration needs an invitation
	WatchDays   int
	MaxOpen     int // watch quota (admins have none)
	MaxDaily    int
	// ResolveWait is how long a reply may wait for a new article's first
	// fetch, so it can tell how that went (0: do not wait). It always stops
	// a second before the message's deadline.
	ResolveWait time.Duration
}

// Bot answers messages. Every reply is complete in itself: we cannot send
// anything later, so results come by email or on the website.
type Bot struct {
	acc     Accounts
	watches Watches
	store   Store
	cfg     Config
	host    string // the site's host name, shown next to links
	poll    time.Duration
	now     func() time.Time
}

// NewBot builds a bot.
func NewBot(acc Accounts, watches Watches, store Store, cfg Config) *Bot {
	if cfg.AccountName == "" {
		cfg.AccountName = cfg.ServiceName
	}
	host := cfg.BaseURL
	if u, err := url.Parse(cfg.BaseURL); err == nil && u.Host != "" {
		host = u.Host
	}
	return &Bot{acc: acc, watches: watches, store: store, cfg: cfg, host: host, poll: 250 * time.Millisecond, now: time.Now}
}

// Commands are whole messages.
var (
	cmdList   = []string{"我的", "列表", "我的观察"}
	cmdBind   = []string{"绑定", "绑定邮箱"}
	cmdUnbind = []string{"解绑", "解除绑定"}
)

// reply is a reply being written. Links to the website share one login
// token, made when the first link needs it. Deletion news goes first; a
// note about the website closes any reply with links.
type reply struct {
	b       *Bot
	ctx     context.Context
	openid  string
	binding *auth.WechatBinding
	token   string
	news    []deletionNotice
	sb      strings.Builder
}

type deletionNotice struct {
	id   uuid.UUID
	line string
}

// Handle returns the reply to m, or "" for none.
func (b *Bot) Handle(ctx context.Context, m Message) (string, error) {
	openid := m.FromUserName
	if m.isUnsubscribe() {
		_, err := b.acc.ForgetWechat(ctx, openid)
		return "", err
	}
	if m.MsgType == "event" && m.Event != "subscribe" {
		return "", nil // menu clicks, scans… we have none
	}
	r := &reply{b: b, ctx: ctx, openid: openid}
	if bd, ok, err := b.acc.WechatBindingFor(ctx, openid); err != nil {
		return "", err
	} else if ok {
		r.binding = &bd
	}

	text := strings.TrimSpace(m.Content)
	unbinding := m.MsgType == "text" && in(text, cmdUnbind)
	if r.binding != nil && !unbinding {
		if r.binding.User.Status != "active" {
			return "这个账号已停用。", nil
		}
		if err := r.deletionNews(); err != nil {
			return "", err
		}
	}

	var err error
	switch {
	case m.MsgType == "event": // subscribe
		r.line("欢迎关注「" + b.cfg.AccountName + "」。")
		r.line("")
		err = r.help()
	case m.MsgType == "text" && in(text, cmdList):
		err = r.list()
	case m.MsgType == "text" && in(text, cmdBind):
		err = r.bind()
	case unbinding:
		err = r.unbind()
	default:
		link := ""
		switch m.MsgType {
		case "text":
			link = wechat.ExtractLink(text)
		case "link":
			link = wechat.ExtractLink(m.URL)
		}
		if link == "" {
			err = r.help()
		} else {
			err = r.submit(link)
		}
	}
	if err != nil {
		return "", err
	}
	return r.finish()
}

func in(s string, set []string) bool {
	for _, v := range set {
		if s == v {
			return true
		}
	}
	return false
}

func (r *reply) line(s string) { r.sb.WriteString(s + "\n") }

func (r *reply) finish() (string, error) {
	var prefix strings.Builder
	if len(r.news) > 0 {
		ids := make([]uuid.UUID, 0, len(r.news))
		for _, n := range r.news {
			ids = append(ids, n.id)
		}
		// All links and the command's reply are ready. A failed command
		// never reaches this update, and concurrent replies share no IDs.
		marked, err := r.b.watches.MarkDeletionNews(r.ctx, r.binding.User.ID, ids)
		if err != nil {
			return "", err
		}
		for _, n := range r.news {
			if slices.Contains(marked, n.id) {
				prefix.WriteString(n.line)
			}
		}
		if prefix.Len() > 0 {
			prefix.WriteByte('\n')
		}
	}
	s := prefix.String() + r.sb.String()
	if strings.Contains(s, `<a href="`) {
		s = strings.TrimRight(s, "\n") + "\n\n" + r.b.linkNote()
	}
	return strings.TrimRight(s, "\n"), nil
}

// linkNote says where the links lead. Without an ICP filing WeChat may warn
// before opening the site, so the note says that is expected.
func (b *Bot) linkNote() string {
	return "链接打开的是本号配套的「" + b.cfg.ServiceName + "」网站（" + b.host + "）。微信可能提示「该网站未完成ICP备案」，可以放心继续访问。"
}

// site names the website and, if it has another name, its relation to the
// account.
func (b *Bot) site() string {
	if b.cfg.ServiceName == b.cfg.AccountName {
		return "本号的网站"
	}
	return "本号配套的「" + b.cfg.ServiceName + "」网站"
}

// a is a link to a page of the website, signing the bound user in.
func (r *reply) a(path, text string) (string, error) {
	if r.token == "" {
		t, err := r.b.acc.NewWechatLoginLink(r.ctx, r.openid, r.binding.User.ID)
		if err != nil {
			return "", err
		}
		r.token = t
	}
	return `<a href="` + r.b.cfg.BaseURL + "/wx/go?to=" + path + "#t=" + r.token + `">` + text + "</a>", nil
}

// bindLink is a link to bind this WeChat user, submitting pending once bound.
func (r *reply) bindLink(pending string) (string, error) {
	t, err := r.b.acc.NewWechatBindLink(r.ctx, r.openid, pending)
	if err != nil {
		return "", err
	}
	return `<a href="` + r.b.cfg.BaseURL + "/wx/bind#t=" + t + `">绑定邮箱</a>（` + minutes(auth.WechatBindTTL) + "内有效）", nil
}

func minutes(d time.Duration) string { return fmt.Sprintf("%d 分钟", int(d/time.Minute)) }

func (r *reply) help() error {
	days := r.b.cfg.WatchDays
	if r.binding == nil {
		r.line(fmt.Sprintf("把公众号文章的链接发给我，%s会备份全文，并在 %d 天内定期检查；文章被删除时发邮件通知你。", r.b.site(), days))
		r.line("")
		return r.askToBind("", "第一次使用请先绑定邮箱：")
	}
	r.line(fmt.Sprintf("把公众号文章的链接发给我，%s会备份全文，并在 %d 天内定期检查；文章被删除时发邮件到 %s。", r.b.site(), days, maskEmail(r.binding.User.Email)))
	r.line("")
	r.line("发送「我的」查看最近的观察")
	r.line("发送「解绑」解除与邮箱的绑定")
	l, err := r.a("/watches", "打开网站")
	if err != nil {
		return err
	}
	r.line(l)
	return nil
}

func (r *reply) bind() error {
	if r.binding != nil {
		r.line("已绑定 " + maskEmail(r.binding.User.Email) + "。如需换绑，请先发送「解绑」。")
		return nil
	}
	return r.askToBind("", "点击下面的链接，在网站上登录并确认，就完成了绑定：")
}

// askToBind writes intro and a bind link that submits pending once bound.
// When registration needs an invitation it says so: such a visitor would
// otherwise wait for a login code that never comes.
func (r *reply) askToBind(pending, intro string) error {
	l, err := r.bindLink(pending)
	if err != nil {
		return err
	}
	if r.b.cfg.InviteOnly {
		r.line("网站目前仅限受邀用户使用。已有账号或收到过邀请邮件的，" + strings.TrimPrefix(intro, "第一次使用"))
	} else {
		r.line(intro)
	}
	r.line(l)
	return nil
}

func (r *reply) unbind() error {
	if r.binding == nil {
		r.line("还没有绑定邮箱。发送「绑定」开始绑定。")
		return nil
	}
	if _, err := r.b.acc.ForgetWechat(r.ctx, r.openid); err != nil {
		return err
	}
	r.line("已解除与 " + maskEmail(r.binding.User.Email) + " 的绑定。已有的观察和邮件通知不受影响，可以在网站上继续查看。")
	r.line("如需重新绑定，发送「绑定」。")
	return nil
}

func (r *reply) submit(link string) error {
	if r.binding == nil {
		r.line("收到链接。")
		return r.askToBind(link, "第一次使用请先绑定邮箱，绑定后会自动开始观察这篇文章：")
	}
	u := r.binding.User
	s, err := r.b.watches.SubmitLink(r.ctx, u.ID, link, watch.QuotaFor(u.Role, r.b.cfg.MaxOpen, r.b.cfg.MaxDaily))
	if msg := SubmitErrorText(err, r.b.cfg.MaxOpen, r.b.cfg.MaxDaily); msg != "" {
		r.line(msg)
		return nil
	}
	if err != nil {
		return err
	}
	w := s.Watch
	if !s.Existing && w.Status == "resolving" {
		got, gone := r.b.waitResolved(r.ctx, u.ID, w.ID)
		if gone { // the link was another address of an article already watched
			r.line("这篇文章已经在观察中。")
			l, err := r.a("/watches", "我的观察")
			if err != nil {
				return err
			}
			r.line(l)
			return nil
		}
		if got != nil {
			w = *got
		}
	}
	var art db.Article
	if w.Status != "resolving" && w.ArticleID != nil {
		if art, err = r.b.store.GetArticle(r.ctx, *w.ArticleID); err != nil {
			return err
		}
	}
	title := "这篇文章"
	if art.Title != nil && *art.Title != "" {
		title = "《" + clip(*art.Title, 40) + "》"
	}
	endReason := ""
	if w.EndReason != nil {
		endReason = *w.EndReason
	}
	view := "查看备份"
	switch {
	case w.Status == "resolving":
		r.line(fmt.Sprintf("已收到，正在备份，通常 1 分钟内完成。之后观察 %d 天，文章被删除时会发邮件通知你。", r.b.cfg.WatchDays))
		view = "查看进度"
	case s.Existing:
		r.line(title + "已经在观察中，到 " + day(w.ExpiresAt) + " 结束。")
		view = "查看"
	case w.Status == "active":
		r.line("已开始观察" + title + "，到 " + day(w.ExpiresAt) + " 结束。文章被删除时会发邮件通知你。")
	case endReason == "deleted" || (endReason == "invalid" && art.DeletedReasonCode != nil):
		msg := title + "已经被删除了"
		if art.DeletedReasonText != nil && *art.DeletedReasonText != "" {
			msg += "（页面显示：" + clip(*art.DeletedReasonText, 40) + "）"
		}
		r.line(msg + "。")
		if endReason == "invalid" { // already gone at the first fetch
			return nil
		}
		view = "查看已有的备份"
	case endReason == "unreachable":
		r.line("暂时打不开这篇文章，没能开始观察。请稍后再发送一次。")
		return nil
	default: // failed: invalid at first fetch
		r.line("这个链接打不开（已失效或参数错误），没能开始观察。")
		return nil
	}
	l, err := r.a(watchPath(w.ID, w.Status), view)
	if err != nil {
		return err
	}
	r.line(l)
	return nil
}

// A resolving watch can disappear when its article is merged into another.
func watchPath(id uuid.UUID, status string) string {
	if status == "resolving" {
		return "/watches"
	}
	return "/watches/" + id.String()
}

// waitResolved waits a little for the first fetch of a new watch's article
// (usually a few seconds), so the reply can tell how it went. It returns the
// watch once resolved, nil if it is still resolving, or gone if it was
// dropped as a duplicate of one the user already has.
func (b *Bot) waitResolved(ctx context.Context, userID, id uuid.UUID) (w *db.Watch, gone bool) {
	if b.cfg.ResolveWait <= 0 {
		return nil, false
	}
	stop := time.Now().Add(b.cfg.ResolveWait)
	if d, ok := ctx.Deadline(); ok && d.Add(-time.Second).Before(stop) {
		stop = d.Add(-time.Second)
	}
	pollCtx, cancel := context.WithDeadline(ctx, stop)
	defer cancel()
	t := time.NewTicker(b.poll)
	defer t.Stop()
	for {
		select {
		case <-pollCtx.Done():
			return nil, false
		case <-t.C:
		}
		row, err := b.store.GetUserWatch(pollCtx, db.GetUserWatchParams{ID: id, UserID: userID})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil, true
		case err != nil: // say it is being backed up, as without waiting
			return nil, false
		case row.Watch.Status != "resolving":
			return &row.Watch, false
		}
	}
}

// newsSize is how many deletions one reply mentions; newsDays is how far
// back it looks.
const (
	newsSize = 3
	newsDays = 30
)

// deletionNews starts the reply with what was deleted since the last one.
func (r *reply) deletionNews() error {
	since := r.b.now().AddDate(0, 0, -newsDays)
	if r.binding.Since.After(since) {
		since = r.binding.Since
	}
	rows, err := r.b.store.ListWechatNews(r.ctx, db.ListWechatNewsParams{UserID: r.binding.User.ID, Since: since, MaxRows: newsSize})
	if err != nil || len(rows) == 0 {
		return err
	}
	for _, w := range rows {
		title := "（标题未知）"
		if w.Title != nil && *w.Title != "" {
			title = clip(*w.Title, 24)
		}
		when := ""
		if w.EndedAt != nil {
			when = "于 " + w.EndedAt.In(auth.Beijing).Format("01-02") + " "
		}
		l, err := r.a("/watches/"+w.ID.String(), "查看备份")
		if err != nil {
			return err
		}
		r.news = append(r.news, deletionNotice{id: w.ID, line: "【删除提醒】你观察的《" + title + "》已" + when + "被删除，" + l + "\n"})
	}
	return nil
}

// SubmitErrorText explains a Submit error to the user, or returns "" if it
// is not one the user can do anything about.
func SubmitErrorText(err error, maxOpen, maxDaily int) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, wechat.ErrTemporaryLink):
		return "这是会过期的临时链接，无法观察。请在文章右上角选择「复制链接」后再发送。"
	case errors.Is(err, watch.ErrBadLink):
		return "这不是有效的公众号文章链接（需要 mp.weixin.qq.com 开头的完整链接）。"
	case errors.Is(err, watch.ErrOpenLimit):
		return fmt.Sprintf("同时观察的文章已达上限（%d 篇），请先在网站上停止一些观察。", maxOpen)
	case errors.Is(err, watch.ErrDailyLimit):
		return fmt.Sprintf("24 小时内提交的链接已达上限（%d 个），请稍后再试。", maxDaily)
	}
	return ""
}

// listSize is how many watches 我的 shows.
const listSize = 5

func (r *reply) list() error {
	if r.binding == nil {
		r.line("还没有绑定邮箱。")
		return r.askToBind("", "第一次使用请先绑定邮箱：")
	}
	u := r.binding.User
	open, err := r.b.store.CountOpenWatches(r.ctx, u.ID)
	if err != nil {
		return err
	}
	rows, err := r.b.store.ListWatchesPage(r.ctx, db.ListWatchesPageParams{UserID: u.ID, MaxRows: listSize})
	if err != nil {
		return err
	}
	r.line(fmt.Sprintf("%s · 观察中 %d 篇", maskEmail(u.Email), open))
	if len(rows) == 0 {
		r.line("")
		r.line("还没有观察的文章。把文章链接发给我就能开始。")
		return nil
	}
	all, err := r.a("/watches", "全部观察")
	if err != nil {
		return err
	}
	r.line("")
	// Stop before the reply would pass WeChat's limit.
	budget := maxReplyBytes - len(all) - r.sb.Len() - len(r.b.linkNote()) - 16
	for _, n := range r.news {
		budget -= len(n.line)
	}
	for _, w := range rows {
		title := "（标题未知）"
		if w.Title != nil && *w.Title != "" {
			title = clip(*w.Title, 24)
		}
		l, err := r.a(watchPath(w.ID, w.Status), title)
		if err != nil {
			return err
		}
		item := "· " + l + " " + r.b.statusWord(w) + "\n"
		if len(item) > budget {
			break
		}
		budget -= len(item)
		r.sb.WriteString(item)
	}
	r.line("")
	r.line(all)
	return nil
}

// soonDays is when a watch shows as 即将到期, as on the website.
const soonDays = 7

// statusWord uses the design system's words, as the website does.
func (b *Bot) statusWord(w db.ListWatchesPageRow) string {
	switch w.Status {
	case "resolving":
		return "备份中"
	case "failed":
		return "无法访问"
	case "active":
		if w.ExpiresAt.Sub(b.now()) < soonDays*24*time.Hour {
			return "即将到期"
		}
		return "观察中"
	}
	if w.EndReason != nil && *w.EndReason == "deleted" {
		return "已删除"
	}
	return "已结束"
}

// BindResult is what binding from the website did.
type BindResult struct {
	WatchID     *uuid.UUID // the article sent before binding, now watched
	SubmitError string     // why that article could not be watched
}

// Bind binds the WeChat user of a bind token to user and starts watching the
// article they sent before binding, if any. These are two transactions: if
// the submission fails the binding stays, and SubmitError says why.
func (b *Bot) Bind(ctx context.Context, user db.User, token string) (BindResult, error) {
	pending, err := b.acc.BindWechat(ctx, user.ID, token)
	if err != nil || pending == "" {
		return BindResult{}, err
	}
	s, err := b.watches.SubmitLink(ctx, user.ID, pending, watch.QuotaFor(user.Role, b.cfg.MaxOpen, b.cfg.MaxDaily))
	if msg := SubmitErrorText(err, b.cfg.MaxOpen, b.cfg.MaxDaily); msg != "" {
		return BindResult{SubmitError: msg}, nil
	}
	if err != nil {
		return BindResult{}, err
	}
	return BindResult{WatchID: &s.Watch.ID}, nil
}

// maskEmail shows enough of an address for its owner to recognise it.
func maskEmail(e string) string {
	at := strings.LastIndexByte(e, '@')
	if at < 1 {
		return "***"
	}
	first, _ := utf8.DecodeRuneInString(e)
	return string(first) + "***" + e[at:]
}

// clip shortens s to n runes. Angle brackets become full-width ones, so a
// title cannot break the reply's <a> markup.
func clip(s string, n int) string {
	s = strings.NewReplacer("<", "＜", ">", "＞").Replace(strings.Join(strings.Fields(s), " "))
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// day is a date in Beijing time, as the website shows it.
func day(t time.Time) string { return t.In(auth.Beijing).Format("2006-01-02") }
