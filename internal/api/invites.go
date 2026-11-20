package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/MamaShip/Observer/internal/auth"
	"github.com/MamaShip/Observer/internal/store/db"
)

// Invitations by users (plan.md §6.1). Answers are the same whether or not
// the address already had an account.

// Invitation is one address a user invited.
type Invitation struct {
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

// Invitations are the user's invitations and this month's allowance.
type Invitations struct {
	Items         []Invitation `json:"items" doc:"Newest first, at most 100"`
	CanInvite     bool         `json:"can_invite" doc:"False when registration is open or invitations are only for admins"`
	UsedThisMonth int          `json:"used_this_month" doc:"Invitations sent this month (Beijing time)"`
	MonthlyLimit  int          `json:"monthly_limit" doc:"Invitations per month; 0 means no limit"`
}

type invitationsOutput struct{ Body Invitations }

type inviteInput struct {
	Body struct {
		Email string `json:"email" maxLength:"254"`
	}
}

type inviteOutput struct {
	Body struct {
		Invitation    Invitation `json:"invitation"`
		UsedThisMonth int        `json:"used_this_month"`
		MonthlyLimit  int        `json:"monthly_limit" doc:"0 means no limit"`
	}
}

// inviteLimit is the user's allowance: 0 for no limit, -1 when they may not
// invite at all.
func (h *handlers) inviteLimit(u db.User) int {
	switch {
	case h.d.Site.RegistrationMode != auth.ModeInvite:
		return -1
	case u.Role == "admin":
		return 0
	case h.d.Limits.MonthlyInvites <= 0:
		return -1
	}
	return h.d.Limits.MonthlyInvites
}

func (h *handlers) registerInvites(g huma.API) {
	tags := []string{"invites"}
	huma.Register(g, huma.Operation{
		OperationID: "list-invites", Method: http.MethodGet, Path: "/invites",
		Summary: "My invitations", Tags: tags, Security: loggedIn,
	}, func(ctx context.Context, _ *struct{}) (*invitationsOutput, error) {
		u := principalFrom(ctx).User
		limit := h.inviteLimit(u)
		quota, err := h.d.Auth.InviteQuotaOf(ctx, u.ID, max(limit, 0))
		if err != nil {
			return nil, err
		}
		rows, err := h.d.Queries.ListInvitesByInviter(ctx, &u.ID)
		if err != nil {
			return nil, err
		}
		out := &invitationsOutput{Body: Invitations{
			Items: []Invitation{}, CanInvite: limit >= 0, UsedThisMonth: quota.Used, MonthlyLimit: max(limit, 0),
		}}
		for _, r := range rows {
			out.Body.Items = append(out.Body.Items, Invitation{Email: r.Email, CreatedAt: r.CreatedAt})
		}
		return out, nil
	})

	huma.Register(g, huma.Operation{
		OperationID: "create-invite", Method: http.MethodPost, Path: "/invites",
		Summary: "Invite an email address", Tags: tags, Security: loggedIn, DefaultStatus: http.StatusCreated,
		Description: "Lets the address log in. Nobody is emailed; tell the person yourself. " +
			"Every invitation counts toward the monthly allowance, except repeating one of your own.",
	}, func(ctx context.Context, in *inviteInput) (*inviteOutput, error) {
		u := principalFrom(ctx).User
		limit := h.inviteLimit(u)
		if limit < 0 {
			if h.d.Site.RegistrationMode != auth.ModeInvite {
				return nil, huma.Error409Conflict("现在开放注册，任何人都可以直接登录，不需要邀请。")
			}
			return nil, huma.Error403Forbidden("目前只有管理员可以邀请。")
		}
		inv, quota, err := h.d.Auth.Invite(ctx, u.ID, in.Body.Email, limit)
		switch {
		case errors.Is(err, auth.ErrInvalidEmail):
			return nil, huma.Error422UnprocessableEntity("邮箱地址格式不正确。")
		case errors.Is(err, auth.ErrInviteSelf):
			return nil, huma.Error422UnprocessableEntity("这是你自己的邮箱。")
		case errors.Is(err, auth.ErrNoInvitesLeft):
			return nil, huma.Error429TooManyRequests("本月的 " + strconv.Itoa(limit) + " 个邀请名额已用完，下月 1 日（北京时间）恢复。")
		case err != nil:
			return nil, err
		}
		out := &inviteOutput{}
		out.Body.Invitation = Invitation{Email: inv.Email, CreatedAt: inv.CreatedAt}
		out.Body.UsedThisMonth, out.Body.MonthlyLimit = quota.Used, quota.Limit
		return out, nil
	})
}
