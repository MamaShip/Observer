package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/MamaShip/Observer/internal/auth"
)

// ---- WeChat Official Account (plan.md §6.7) ----

// SiteWechat is present when the Official Account entry is on.
type SiteWechat struct {
	Name string `json:"name" doc:"The account users follow"`
}

// MeWechat is the user's WeChat binding.
type MeWechat struct {
	BoundAt time.Time `json:"bound_at"`
}

type wechatTokenInput struct {
	UserAgent string `header:"User-Agent"`
	Body      struct {
		Token string `json:"token" maxLength:"64" doc:"From the link in a reply (after #t=)"`
	}
}

type wechatBindOutput struct {
	Body struct {
		Submitted   bool       `json:"submitted,omitempty" doc:"The article sent before binding is now watched"`
		WatchID     *uuid.UUID `json:"watch_id,omitempty" doc:"Its watch, once its first fetch is done; absent while that runs, as the watch may then merge into one the user already has"`
		SubmitError string     `json:"submit_error,omitempty" doc:"Why that article could not be watched"`
	}
}

const wechatLinkGone = "链接已失效（只能使用一次，并且有时间限制）。请回到公众号重新获取。"

func (h *handlers) wechatOn() error {
	if h.d.MP == nil {
		return huma.Error404NotFound("没有开通公众号入口。")
	}
	return nil
}

func (h *handlers) registerWechat(g huma.API) {
	huma.Register(g, huma.Operation{
		OperationID: "wechat-login", Method: http.MethodPost, Path: "/wechat/login",
		Summary: "Log in with a link from the Official Account", Tags: []string{"wechat"},
		Description: "Each link works once, for the account bound to the WeChat user it was sent to. 410 when used, expired or no longer bound.",
	}, func(ctx context.Context, in *wechatTokenInput) (*verifyOutput, error) {
		if err := h.wechatOn(); err != nil {
			return nil, err
		}
		sess, err := h.d.Auth.WechatLogin(ctx, in.Body.Token, in.UserAgent)
		switch {
		case errors.Is(err, auth.ErrWechatLink):
			return nil, huma.Error410Gone(wechatLinkGone)
		case errors.Is(err, auth.ErrDisabled):
			return nil, huma.Error403Forbidden("该账号已停用。")
		case err != nil:
			return nil, err
		}
		out := &verifyOutput{SetCookie: sessionCookie(sess.Token, sess.TTL)}
		out.Body.User = userFromDB(sess.User)
		return out, nil
	})

	huma.Register(g, huma.Operation{
		OperationID: "wechat-bind", Method: http.MethodPost, Path: "/wechat/bind",
		Summary: "Bind the WeChat user of a link to my account", Tags: []string{"wechat"}, Security: loggedIn,
		Description: "Replaces any earlier binding of either side. If the WeChat user sent an article before binding, it is watched now.",
	}, func(ctx context.Context, in *wechatTokenInput) (*wechatBindOutput, error) {
		if err := h.wechatOn(); err != nil {
			return nil, err
		}
		r, err := h.d.MP.Bind(ctx, principalFrom(ctx).User, in.Body.Token)
		switch {
		case errors.Is(err, auth.ErrWechatLink):
			return nil, huma.Error410Gone(wechatLinkGone)
		case errors.Is(err, auth.ErrDisabled):
			return nil, huma.Error403Forbidden("该账号已停用。")
		case err != nil:
			return nil, err
		}
		out := &wechatBindOutput{}
		out.Body.Submitted, out.Body.WatchID, out.Body.SubmitError = r.Submitted, r.WatchID, r.SubmitError
		return out, nil
	})

	huma.Register(g, huma.Operation{
		OperationID: "wechat-unbind", Method: http.MethodDelete, Path: "/me/wechat",
		Summary: "Unbind WeChat", Tags: []string{"wechat"}, Security: loggedIn, DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, _ *struct{}) (*struct{}, error) {
		return nil, h.d.Auth.UnbindWechat(ctx, principalFrom(ctx).User.ID)
	})
}
