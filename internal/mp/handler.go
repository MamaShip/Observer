package mp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
	"golang.org/x/time/rate"
)

// Path is where WeChat calls us (the URL set in the Official Account
// platform's server configuration).
const Path = "/wechat/callback"

const (
	maxBody = 64 << 10
	// WeChat waits 5 seconds for a reply, then retries (three times).
	replyTimeout = 4 * time.Second
	// A retry gets the reply already given (the action is not repeated).
	dedupFor = time.Minute
	// Per WeChat user: 30 messages, refilled one per 20 seconds.
	userBurst = 30
	userEvery = 20 * time.Second
)

const (
	busyText    = "系统繁忙，请稍后再试。"
	tooManyText = "发送太频繁了，请稍后再试。"
)

// Handler serves Path.
type Handler struct {
	bot     *Bot
	token   string
	account string // gh_…; checked when set
	log     *slog.Logger
	now     func() time.Time

	inflight singleflight.Group
	mu       sync.Mutex
	replies  map[string]cached
	users    map[string]*userBucket
	swept    time.Time
}

type cached struct {
	body []byte
	at   time.Time
}

type userBucket struct {
	lim  *rate.Limiter
	seen time.Time
}

// NewHandler builds the handler. token is the one set in the platform's
// server configuration; account, if not empty, is our original ID.
func NewHandler(bot *Bot, token, account string, log *slog.Logger) *Handler {
	return &Handler{
		bot: bot, token: token, account: account, log: log.With("component", "mp"), now: time.Now,
		replies: map[string]cached{}, users: map[string]*userBucket{},
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if !verify(h.token, q.Get("signature"), q.Get("timestamp"), q.Get("nonce"), h.now()) {
		http.Error(w, "bad signature", http.StatusForbidden)
		return
	}
	switch r.Method {
	case http.MethodGet: // the platform checking the URL
		echo := q.Get("echostr")
		if len(echo) > 128 {
			http.Error(w, "bad echostr", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, echo)
	case http.MethodPost:
		h.message(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) message(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	m, err := parseMessage(body)
	if err != nil || m.FromUserName == "" || m.ToUserName == "" {
		http.Error(w, "bad message", http.StatusBadRequest)
		return
	}
	if h.account != "" && m.ToUserName != h.account {
		h.log.Warn("message for another account", "to", m.ToUserName)
		http.Error(w, "wrong account", http.StatusBadRequest)
		return
	}
	key := m.dedupKey()
	result, _, _ := h.inflight.Do(key, func() (any, error) {
		if b, ok := h.cached(key); ok {
			return b, nil
		}
		// Complete the shared reply even if the first connection drops.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), replyTimeout)
		defer cancel()
		b, err := h.reply(ctx, m)
		if err == nil { // after a failure, a retry may do better
			h.remember(key, b)
		}
		return b, err
	})
	write(w, result.([]byte))
}

func (h *Handler) reply(ctx context.Context, m Message) ([]byte, error) {
	var content string
	var err error
	if !m.isUnsubscribe() && !h.allow(m.FromUserName) {
		content = tooManyText
	} else {
		content, err = h.bot.Handle(ctx, m)
		if err != nil {
			h.log.Error("handling message", "user", pseudonym(m.FromUserName), "type", m.MsgType, "err", err)
			content = busyText
		}
	}
	// "success" tells WeChat there is no reply (an empty body would too, but
	// some clients then show "该公众号暂时无法提供服务").
	if content == "" {
		return []byte("success"), err
	}
	return encodeText(m.FromUserName, m.ToUserName, content, h.now()), err
}

func write(w http.ResponseWriter, b []byte) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = w.Write(b)
}

func (h *Handler) cached(key string) ([]byte, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.replies[key]
	if !ok || h.now().Sub(c.at) > dedupFor {
		return nil, false
	}
	return c.body, true
}

func (h *Handler) remember(key string, b []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	h.replies[key] = cached{body: b, at: now}
	h.sweep(now)
}

func (h *Handler) allow(openid string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	u := h.users[openid]
	if u == nil {
		u = &userBucket{lim: rate.NewLimiter(rate.Every(userEvery), userBurst)}
		h.users[openid] = u
	}
	u.seen = now
	ok := u.lim.AllowN(now, 1)
	h.sweep(now)
	return ok
}

// sweep forgets old replies and idle users, at most once a minute. The
// caller holds mu.
func (h *Handler) sweep(now time.Time) {
	if now.Sub(h.swept) < time.Minute {
		return
	}
	h.swept = now
	for k, c := range h.replies {
		if now.Sub(c.at) > dedupFor {
			delete(h.replies, k)
		}
	}
	// A bucket refills completely in userBurst*userEvery.
	for k, u := range h.users {
		if now.Sub(u.seen) > userBurst*userEvery {
			delete(h.users, k)
		}
	}
}

// pseudonym identifies a WeChat user in logs without recording the OpenID.
func pseudonym(openid string) string {
	h := sha256.Sum256([]byte(openid))
	return hex.EncodeToString(h[:4])
}
