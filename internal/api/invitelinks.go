package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/MamaShip/Observer/internal/auth"
	"github.com/MamaShip/Observer/internal/store/db"
)

// Public invitation links (plan.md §6.1): admins open them; the sign-up
// page (/join) checks them and passes the token with the login code.

func inviteLinkGone() error {
	return huma.Error410Gone("这个邀请链接已失效。已有账号可以直接用邮箱登录。")
}

type inviteLinkCheckInput struct {
	Body struct {
		Token string `json:"token" maxLength:"64" doc:"The token from the link (after #t=)"`
	}
}

type inviteLinkCheckOutput struct {
	Body struct {
		ExpiresAt time.Time `json:"expires_at" doc:"Until when the link accepts sign-ups, if places remain"`
	}
}

// InviteLink is a public invitation link as the admin console shows it.
type InviteLink struct {
	ID        uuid.UUID  `json:"id"`
	Note      string     `json:"note"`
	MaxUses   int        `json:"max_uses" doc:"Accounts the link may create"`
	Used      int        `json:"used" doc:"Accounts it has created, including ones deleted since"`
	Accounts  int        `json:"accounts" doc:"Accounts it created that still exist"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	CreatedBy string     `json:"created_by,omitempty" doc:"The creating admin's email; absent from the command line or once that account is deleted"`
	Status    string     `json:"status" enum:"active,full,expired,revoked"`
	URL       string     `json:"url" doc:"The link to share"`
}

type inviteLinksOutput struct {
	Body struct {
		Items []InviteLink `json:"items" doc:"Newest first, at most 200"`
	}
}

type createInviteLinkInput struct {
	Body struct {
		MaxUses    int    `json:"max_uses" minimum:"1" maximum:"1000" doc:"Accounts the link may create"`
		ValidHours int    `json:"valid_hours" minimum:"1" maximum:"720" doc:"How long the link stays open"`
		Note       string `json:"note,omitempty" maxLength:"100" doc:"For the admins, e.g. where the link was posted"`
	}
}

type inviteLinkOutput struct{ Body InviteLink }

// InviteLinkURL is the link to share for a link id.
func InviteLinkURL(base string, s *auth.Service, id uuid.UUID) string {
	return base + "/join#t=" + s.LinkToken(id)
}

func (h *handlers) inviteLink(r db.ListInviteLinksRow, now time.Time) InviteLink {
	l := r.InviteLink
	return InviteLink{
		ID: l.ID, Note: l.Note, MaxUses: int(l.MaxUses), Used: int(l.Used), Accounts: int(r.Accounts),
		ExpiresAt: l.ExpiresAt, RevokedAt: l.RevokedAt, CreatedAt: l.CreatedAt, CreatedBy: r.CreatorEmail,
		Status: auth.LinkStatus(l, now), URL: InviteLinkURL(h.d.BaseURL, h.d.Auth, l.ID),
	}
}

func (h *handlers) registerInviteLinks(g huma.API) {
	huma.Register(g, huma.Operation{
		OperationID: "check-invite-link", Method: http.MethodPost, Path: "/auth/invite-link",
		Summary: "Check a public invitation link", Tags: []string{"auth"},
		Description: "410 when the link has expired, has no places left or was revoked (one answer for all). " +
			"409 when registration is open and no link is needed.",
	}, func(ctx context.Context, in *inviteLinkCheckInput) (*inviteLinkCheckOutput, error) {
		exp, err := h.d.Auth.CheckInviteLink(ctx, in.Body.Token)
		switch {
		case errors.Is(err, auth.ErrRegistrationOpen):
			return nil, huma.Error409Conflict("现在开放注册，直接用邮箱登录即可。")
		case errors.Is(err, auth.ErrInviteLink):
			return nil, inviteLinkGone()
		case err != nil:
			return nil, err
		}
		out := &inviteLinkCheckOutput{}
		out.Body.ExpiresAt = exp
		return out, nil
	})

	tags := []string{"admin"}
	huma.Register(g, huma.Operation{
		OperationID: "admin-list-invite-links", Method: http.MethodGet, Path: "/admin/invite-links",
		Summary: "Public invitation links, newest first", Tags: tags, Security: loggedIn,
	}, func(ctx context.Context, _ *struct{}) (*inviteLinksOutput, error) {
		if err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		rows, err := h.d.Queries.ListInviteLinks(ctx, nil)
		if err != nil {
			return nil, err
		}
		now := time.Now()
		out := &inviteLinksOutput{}
		out.Body.Items = []InviteLink{}
		for _, r := range rows {
			out.Body.Items = append(out.Body.Items, h.inviteLink(r, now))
		}
		return out, nil
	})

	huma.Register(g, huma.Operation{
		OperationID: "admin-create-invite-link", Method: http.MethodPost, Path: "/admin/invite-links",
		Summary: "Open a public invitation link", Tags: tags, Security: loggedIn, DefaultStatus: http.StatusCreated,
		Description: "Anyone with the link can sign up during valid_hours, until max_uses accounts were created. " +
			"409 when registration is open.",
	}, func(ctx context.Context, in *createInviteLinkInput) (*inviteLinkOutput, error) {
		if err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		me := principalFrom(ctx).User.ID
		l, err := h.d.Auth.CreateInviteLink(ctx, &me, in.Body.MaxUses, time.Duration(in.Body.ValidHours)*time.Hour, in.Body.Note)
		switch {
		case errors.Is(err, auth.ErrRegistrationOpen):
			return nil, huma.Error409Conflict("现在开放注册，不需要邀请链接。")
		case errors.Is(err, auth.ErrLinkSettings):
			return nil, huma.Error422UnprocessableEntity(fmt.Sprintf("名额为 1～%d，有效期为 1 小时～%d 天，备注最多 %d 个字。",
				auth.LinkMaxUses, int(auth.LinkMaxValid.Hours()/24), auth.LinkNoteMax))
		case err != nil:
			return nil, err
		}
		rows, err := h.d.Queries.ListInviteLinks(ctx, &l.ID)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return nil, huma.Error404NotFound("没有这个邀请链接。")
		}
		return &inviteLinkOutput{Body: h.inviteLink(rows[0], time.Now())}, nil
	})

	huma.Register(g, huma.Operation{
		OperationID: "admin-revoke-invite-link", Method: http.MethodDelete, Path: "/admin/invite-links/{id}",
		Summary: "Close a public invitation link", Tags: tags, Security: loggedIn, DefaultStatus: http.StatusNoContent,
		Description: "The link stops working at once; accounts it created stay.",
	}, func(ctx context.Context, in *idInput) (*struct{}, error) {
		if err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		err := h.d.Auth.RevokeInviteLink(ctx, in.ID)
		if errors.Is(err, auth.ErrInviteNotFound) {
			return nil, huma.Error404NotFound("没有这个邀请链接。")
		}
		return nil, err
	})
}
