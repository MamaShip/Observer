// Package wechat recognizes, fetches and analyzes WeChat Official Account
// (mp.weixin.qq.com) article pages.
package wechat

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// ArticleHost is the only host that serves article pages.
const ArticleHost = "mp.weixin.qq.com"

// LinkKind tells which of the two public article link forms a link uses.
type LinkKind string

const (
	// LinkShort is https://mp.weixin.qq.com/s/<token>.
	LinkShort LinkKind = "short"
	// LinkLong is https://mp.weixin.qq.com/s?__biz=..&mid=..&idx=..&sn=..[&chksm=..].
	LinkLong LinkKind = "long"
)

var (
	ErrNotArticle    = errors.New("wechat: not a mp.weixin.qq.com article link")
	ErrTemporaryLink = errors.New("wechat: temporary link (with timestamp/signature) expires and cannot be watched")
	ErrMalformed     = errors.New("wechat: malformed article link")
)

// Link is an article link normalized before any network access.
type Link struct {
	Kind LinkKind
	// Key identifies the link for alias lookup. It drops chksm and every
	// tracking parameter, so different shares of one link map to one key.
	Key string
	// FetchURL is the URL to request. For long links it keeps chksm:
	// without it WeChat serves the verification page (observed 2026-11).
	FetchURL string

	Token                    string // short links only
	Biz, Mid, Idx, Sn, Chksm string // long links only
}

var (
	reToken = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)
	reBiz   = regexp.MustCompile(`^[A-Za-z0-9+/]+={0,2}$`)
	reDigit = regexp.MustCompile(`^[0-9]{1,20}$`)
	reHex   = regexp.MustCompile(`^[0-9a-fA-F]{8,128}$`)
)

// ParseLink validates and normalizes a user-submitted article link.
func ParseLink(raw string) (Link, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return Link{}, ErrMalformed
	}
	if (u.Scheme != "https" && u.Scheme != "http") || !strings.EqualFold(u.Hostname(), ArticleHost) {
		return Link{}, ErrNotArticle
	}
	if u.User != nil || (u.Port() != "" && u.Port() != "443" && u.Port() != "80") {
		return Link{}, ErrMalformed
	}

	path := strings.TrimSuffix(u.EscapedPath(), "/")
	switch {
	case strings.HasPrefix(path, "/s/"):
		token := strings.TrimPrefix(path, "/s/")
		if !reToken.MatchString(token) {
			return Link{}, ErrMalformed
		}
		key := "https://" + ArticleHost + "/s/" + token
		return Link{Kind: LinkShort, Key: key, FetchURL: key, Token: token}, nil

	case path == "/s":
		q := u.Query()
		biz, mid, idx, sn, chksm := q.Get("__biz"), q.Get("mid"), q.Get("idx"), q.Get("sn"), q.Get("chksm")
		if biz == "" && q.Get("signature") != "" && q.Get("timestamp") != "" {
			return Link{}, ErrTemporaryLink
		}
		if !reBiz.MatchString(biz) || !reDigit.MatchString(mid) || !reDigit.MatchString(idx) || !reHex.MatchString(sn) {
			return Link{}, ErrMalformed
		}
		if chksm != "" && !reHex.MatchString(chksm) {
			chksm = ""
		}
		return Link{
			Kind:     LinkLong,
			Key:      LongURL(biz, mid, idx, sn, ""),
			FetchURL: LongURL(biz, mid, idx, sn, chksm),
			Biz:      biz, Mid: mid, Idx: idx, Sn: sn, Chksm: chksm,
		}, nil
	}
	return Link{}, ErrNotArticle
}

// PlaceholderChksm stands in for a missing chksm. WeChat answers a long link
// without chksm with the verification page, but accepts any value (M0,
// 2026-11); requesting without one would only teach WeChat to distrust us.
const PlaceholderChksm = "00000000"

// ServiceFetchURL is the URL the service requests: FetchURL, plus
// PlaceholderChksm for long links that came without a chksm.
func (l Link) ServiceFetchURL() string {
	if l.Kind == LinkLong && l.Chksm == "" {
		return LongURL(l.Biz, l.Mid, l.Idx, l.Sn, PlaceholderChksm)
	}
	return l.FetchURL
}

// LongURL builds a long article link with parameters in a fixed order.
// "=" in __biz is kept literal, as WeChat itself writes it.
func LongURL(biz, mid, idx, sn, chksm string) string {
	s := "https://" + ArticleHost + "/s?__biz=" + strings.ReplaceAll(url.QueryEscape(biz), "%3D", "=") +
		"&mid=" + url.QueryEscape(mid) + "&idx=" + url.QueryEscape(idx) + "&sn=" + url.QueryEscape(sn)
	if chksm != "" {
		s += "&chksm=" + url.QueryEscape(chksm)
	}
	return s
}

// CanonicalKey is the global identity of an article: biz:mid:idx.
func CanonicalKey(biz, mid, idx string) string {
	if biz == "" || mid == "" || idx == "" {
		return ""
	}
	return biz + ":" + mid + ":" + idx
}

// reInText matches an article link inside shared text. It mirrors
// web/src/lib/link.ts: users paste whatever WeChat's "copy link" or a share
// gives them, and ParseLink does the real validation.
var reInText = regexp.MustCompile(`(?i)https?://mp\.weixin\.qq\.com/s[^\s<>"'，。；、）)】]*`)

// ExtractLink returns the first article link in text, or "".
func ExtractLink(text string) string {
	return strings.TrimRight(reInText.FindString(text), ".,;!?")
}
