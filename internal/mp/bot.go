package mp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

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
}

// Store is the part of db.Queries the bot reads.
type Store interface {
	GetArticle(ctx context.Context, id uuid.UUID) (db.Article, error)
	ListWatchesPage(ctx context.Context, arg db.ListWatchesPageParams) ([]db.ListWatchesPageRow, error)
	CountOpenWatches(ctx context.Context, userID uuid.UUID) (int64, error)
}

// Config configures the bot.
type Config struct {
	BaseURL     string // the site's origin, for links
	ServiceName string
	WatchDays   int
	MaxOpen     int // watch quota (admins have none)
	MaxDaily    int
}

// Bot answers messages. Every reply is complete in itself: we cannot send
// anything later, so results come by email or on the website.
type Bot struct {
	acc     Accounts
	watches Watches
	store   Store
	cfg     Config
	now     func() time.Time
}

// NewBot builds a bot.
func NewBot(acc Accounts, watches Watches, store Store, cfg Config) *Bot {
	return &Bot{acc: acc, watches: watches, store: store, cfg: cfg, now: time.Now}
}

// Commands are whole messages.
var (
	cmdList   = []string{"我的", "列表", "我的观察"}
	cmdBind   = []string{"绑定", "绑定邮箱"}
	cmdUnbind = []string{"解绑", "解除绑定"}
)

// reply is a reply being written. Links to the website share one login
// token, made when the first link needs it.
type reply struct {
	b       *Bot
	ctx     context.Context
	openid  string
	binding *auth.WechatBinding
	token   string
	sb      strings.Builder
}

// Handle returns the reply to m, or "" for none.
func (b *Bot) Handle(ctx context.Context, m Message) (string, error) {
	openid := m.FromUserName
	if m.MsgType == "event" && m.Event == "unsubscribe" {
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

	var err error
	text := strings.TrimSpace(m.Content)
	switch {
	case m.MsgType == "event": // subscribe
		r.line("欢迎关注" + b.cfg.ServiceName + "。")
		r.line("")
		err = r.help()
	case m.MsgType == "text" && in(text, cmdList):
		err = r.list()
	case m.MsgType == "text" && in(text, cmdBind):
		err = r.bind()
	case m.MsgType == "text" && in(text, cmdUnbind):
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
	return r.String(), nil
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

func (r *reply) String() string { return strings.TrimRight(r.sb.String(), "\n") }

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
		r.line(fmt.Sprintf("把公众号文章的链接发给我，我会备份全文，并在 %d 天内定期检查；文章被删除时发邮件通知你。", days))
		r.line("")
		l, err := r.bindLink("")
		if err != nil {
			return err
		}
		r.line("第一次使用请先绑定邮箱：" + l)
		return nil
	}
	r.line(fmt.Sprintf("把公众号文章的链接发给我，我会备份全文，并在 %d 天内定期检查；文章被删除时发邮件到 %s。", days, maskEmail(r.binding.User.Email)))
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
	l, err := r.bindLink("")
	if err != nil {
		return err
	}
	r.line("点击" + l + "。登录后确认，就完成了绑定。")
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
		l, err := r.bindLink(link)
		if err != nil {
			return err
		}
		r.line("收到链接。第一次使用请先绑定邮箱，绑定后会自动开始观察这篇文章：")
		r.line(l)
		return nil
	}
	u := r.binding.User
	if u.Status != "active" {
		r.line("这个账号已停用。")
		return nil
	}
	s, err := r.b.watches.SubmitLink(r.ctx, u.ID, link, watch.QuotaFor(u.Role, r.b.cfg.MaxOpen, r.b.cfg.MaxDaily))
	if msg := SubmitErrorText(err, r.b.cfg.MaxOpen, r.b.cfg.MaxDaily); msg != "" {
		r.line(msg)
		return nil
	}
	if err != nil {
		return err
	}
	w := s.Watch
	var art db.Article
	if w.ArticleID != nil {
		if art, err = r.b.store.GetArticle(r.ctx, *w.ArticleID); err != nil {
			return err
		}
	}
	title := "这篇文章"
	if art.Title != nil && *art.Title != "" {
		title = "《" + clip(*art.Title, 40) + "》"
	}
	view := "查看备份"
	switch {
	case s.Existing:
		r.line(title + "已经在观察中，到 " + day(w.ExpiresAt) + " 结束。")
		view = "查看"
	case w.Status == "resolving":
		r.line(fmt.Sprintf("已收到，正在备份，通常 1 分钟内完成。之后观察 %d 天，文章被删除时会发邮件通知你。", r.b.cfg.WatchDays))
		view = "查看进度"
	case w.Status == "active":
		r.line("已开始观察" + title + "，到 " + day(w.ExpiresAt) + " 结束。文章被删除时会发邮件通知你。")
	case w.EndReason != nil && *w.EndReason == "deleted":
		msg := title + "已经被删除了"
		if art.DeletedReasonText != nil && *art.DeletedReasonText != "" {
			msg += "（页面显示：" + clip(*art.DeletedReasonText, 40) + "）"
		}
		r.line(msg + "。")
		view = "查看已有的备份"
	default: // failed: invalid at first fetch
		r.line("这个链接打不开（已失效或参数错误），没能开始观察。")
		return nil
	}
	l, err := r.a("/watches/"+w.ID.String(), view)
	if err != nil {
		return err
	}
	r.line(l)
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
		l, err := r.bindLink("")
		if err != nil {
			return err
		}
		r.line("还没有绑定邮箱。请先" + l + "。")
		return nil
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
	budget := maxReplyBytes - len(all) - r.sb.Len() - 16
	for _, w := range rows {
		title := "（标题未知）"
		if w.Title != nil && *w.Title != "" {
			title = clip(*w.Title, 24)
		}
		l, err := r.a("/watches/"+w.ID.String(), title)
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
