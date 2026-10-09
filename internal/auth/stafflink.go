package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/crypto"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/users"
)

// Staff <-> personal account links (ADR-037 §4). A staff member (staff session, fresh step-up) asks to link the
// personal account whose verified login email they give. If such an eligible account exists, the worker emails
// it a single-use, expiring link token (only its SHA-256 is stored, in app.staff_link_requests). The person
// confirms from their personal session; the login email must still match. The link
// (app.users.staff_personal_user_id) is then set, audited on both accounts and announced to both by email. It
// cannot be removed through the API (unlinking would weaken the conflict checks; a future maker-checker
// procedure is required), and the database refuses any change by a runtime role.
//
// The request response is the same whether or not an eligible account exists, so the endpoint does not reveal
// which addresses have accounts.

// Staff link events.
const (
	EvStaffLinkRequested = "auth.staff_link_requested" // {request_id, staff_user_id}; the worker issues and mails the token
	EvStaffLinkConfirmed = "auth.staff_link_confirmed" // {staff_user_id, personal_user_id, request_id}
)

// StaffLinkTTL is how long a link token stays valid.
const StaffLinkTTL = 24 * time.Hour

func errStaffAlreadyLinked() error {
	return errs.New(errs.Conflict, "STAFF_ALREADY_LINKED", "Your staff account is already linked to a personal account. Links cannot be changed.")
}

func errPersonalAlreadyLinked() error {
	return errs.New(errs.Conflict, "PERSONAL_ACCOUNT_ALREADY_LINKED", "This personal account is already linked to a staff account.")
}

func errStaffLinkMismatch() error {
	return errs.New(errs.Forbidden, "STAFF_LINK_EMAIL_MISMATCH",
		"This link was sent to a different email address. Sign in with the personal account that received it.")
}

type staffLinkReq struct {
	Email string `json:"email"`
}

// StaffLinkStatus is the staff member's view (email masked).
type StaffLinkStatus struct {
	Linked              bool       `json:"linked"`
	PersonalEmailMasked *string    `json:"personal_email_masked"`
	LinkedAt            *time.Time `json:"linked_at"`
	Pending             *struct {
		EmailMasked string    `json:"email_masked"`
		ExpiresAt   time.Time `json:"expires_at"`
	} `json:"pending"`
}

// RequestStaffLink handles POST /admin/me/personal-account-link (staff session, fresh step-up).
func (s *Service) RequestStaffLink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	if p == nil || p.Kind != authz.KindStaff {
		httpx.WriteError(w, r, s.Logger, errs.New(errs.NotFound, errs.CodeRouteNotFound, "No such route."))
		return
	}
	if err := s.requireStepUp(p); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	var req staffLinkReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	email, err := users.NormalizeEmail(req.Email)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, httpx.Validation(errs.Detail{Field: "email", Code: "INVALID_EMAIL"}))
		return
	}
	if err := s.throttle(ctx, "auth.token.ip", clientKey(ctx)); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	var expires time.Time
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		staff, err := users.ByIDForUpdate(ctx, tx, p.UserID)
		if err != nil {
			return err
		}
		if staff.Kind != users.KindStaff || staff.Status != users.StatusActive {
			return errs.New(errs.Forbidden, "PERMISSION_DENIED", "You do not have permission for this action.")
		}
		if linked, err := users.PersonalAccount(ctx, tx, staff.ID); err != nil {
			return err
		} else if linked != "" {
			return errStaffAlreadyLinked()
		}
		// an eligible target: a personal, active account whose VERIFIED login email is this address and which is
		// not linked to any staff account. Anything else gets the same response and no email.
		var target any
		if acct, err := users.ByLoginEmail(ctx, tx, email); err == nil {
			if acct.Kind == users.KindUser && acct.Status == users.StatusActive && acct.EmailVerified && !acct.IsSystem {
				if other, err := users.StaffForPersonal(ctx, tx, acct.ID); err != nil {
					return err
				} else if other == "" {
					target = acct.ID
				}
			}
		} else if !errors.Is(err, users.ErrNotFound) {
			return err
		}
		now := s.now()
		expires = now.Add(StaffLinkTTL)
		if _, err := tx.Exec(ctx, `UPDATE app.staff_link_requests SET invalidated_at = $2
			WHERE staff_user_id = $1 AND consumed_at IS NULL AND invalidated_at IS NULL`, staff.ID, now); err != nil {
			return err
		}
		id := ids.New()
		if _, err := tx.Exec(ctx, `INSERT INTO app.staff_link_requests (id, staff_user_id, email_normalized, target_user_id, requested_ip,
			created_at, expires_at) VALUES ($1, $2, $3, $4, $5::inet, $6, $7)`, id, staff.ID, email, target, ipString(ctx), now, expires); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "auth.staff_link.requested", ActorType: "staff",
			ActorID: staff.ID, TargetType: "user", TargetID: staff.ID,
			Metadata: map[string]any{"request_id": id, "email_masked": MaskEmail(email)}}); err != nil {
			return err
		}
		if target == nil {
			return nil
		}
		return s.emit(ctx, tx, EvStaffLinkRequested, staff.ID, map[string]any{"user_id": staff.ID, "request_id": id})
	})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	httpx.WriteData(w, r, http.StatusAccepted, map[string]any{"status": "confirmation_sent_if_eligible", "expires_at": expires})
}

// GetStaffLink handles GET /admin/me/personal-account-link (staff session): the current link or pending request.
func (s *Service) GetStaffLink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	if p == nil || p.Kind != authz.KindStaff {
		httpx.WriteError(w, r, s.Logger, errs.New(errs.NotFound, errs.CodeRouteNotFound, "No such route."))
		return
	}
	var out StaffLinkStatus
	personal, err := users.PersonalAccount(ctx, s.Pool, p.UserID)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	if personal != "" {
		out.Linked = true
		if a, err := users.ByID(ctx, s.Pool, personal); err == nil {
			m := MaskEmail(a.Email)
			out.PersonalEmailMasked = &m
		}
		var at time.Time
		if err := s.Pool.QueryRow(ctx, `SELECT consumed_at FROM app.staff_link_requests WHERE staff_user_id = $1 AND consumed_at IS NOT NULL
			ORDER BY consumed_at DESC LIMIT 1`, p.UserID).Scan(&at); err == nil {
			out.LinkedAt = &at
		}
	} else {
		var email string
		var exp time.Time
		err := s.Pool.QueryRow(ctx, `SELECT email_normalized, expires_at FROM app.staff_link_requests WHERE staff_user_id = $1
			AND consumed_at IS NULL AND invalidated_at IS NULL AND expires_at > $2`, p.UserID, s.now()).Scan(&email, &exp)
		if err == nil {
			out.Pending = &struct {
				EmailMasked string    `json:"email_masked"`
				ExpiresAt   time.Time `json:"expires_at"`
			}{MaskEmail(email), exp}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			httpx.WriteError(w, r, s.Logger, err)
			return
		}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	httpx.WriteData(w, r, http.StatusOK, out)
}

// ConfirmStaffLink handles POST /me/staff-link/confirm (personal session). The token is single use and
// expiring; the caller's verified login email must be the requested address.
func (s *Service) ConfirmStaffLink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	var req tokenReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	if err := s.throttle(ctx, "auth.token.ip", clientKey(ctx)); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	var staffID string
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if !crypto.ValidTokenFormat(req.Token) {
			return errTokenInvalid()
		}
		var id, email string
		var target *string
		err := tx.QueryRow(ctx, `SELECT id, staff_user_id, email_normalized, target_user_id FROM app.staff_link_requests
			WHERE token_hash = $1 AND consumed_at IS NULL AND invalidated_at IS NULL AND expires_at > $2 FOR UPDATE`,
			crypto.HashToken(req.Token), s.now()).Scan(&id, &staffID, &email, &target)
		if errors.Is(err, pgx.ErrNoRows) {
			return errTokenInvalid()
		}
		if err != nil {
			return err
		}
		// lock both accounts in a fixed order (no deadlock between concurrent confirmations)
		first, second := staffID, p.UserID
		if second < first {
			first, second = second, first
		}
		for _, uid := range []string{first, second} {
			if _, err := users.ByIDForUpdate(ctx, tx, uid); err != nil && !errors.Is(err, users.ErrNotFound) {
				return err
			}
		}
		me, err := users.ByID(ctx, tx, p.UserID)
		if err != nil {
			return err
		}
		if me.Kind != users.KindUser || me.Status != users.StatusActive || !me.EmailVerified || me.Email != email ||
			target == nil || *target != me.ID {
			return errStaffLinkMismatch() // the token stays usable by the right account until it expires
		}
		staff, err := users.ByID(ctx, tx, staffID)
		if err != nil {
			return err
		}
		if staff.Kind != users.KindStaff || staff.Status != users.StatusActive {
			return errTokenInvalid()
		}
		if other, err := users.StaffForPersonal(ctx, tx, me.ID); err != nil {
			return err
		} else if other != "" {
			return errPersonalAlreadyLinked()
		}
		switch err := users.LinkStaffPersonal(ctx, tx, staffID, me.ID); {
		case errors.Is(err, users.ErrStaffAlreadyLinked):
			return errStaffAlreadyLinked()
		case errors.Is(err, users.ErrPersonalAlreadyLinked):
			return errPersonalAlreadyLinked()
		case err != nil:
			return err
		}
		now := s.now()
		if _, err := tx.Exec(ctx, `UPDATE app.staff_link_requests SET consumed_at = $2, consumed_by = $3 WHERE id = $1`, id, now, me.ID); err != nil {
			return err
		}
		md := map[string]any{"request_id": id, "staff_user_id": staffID, "personal_user_id": me.ID}
		for _, target := range []string{staffID, me.ID} {
			if err := audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "auth.staff_link.confirmed", ActorType: "user",
				ActorID: me.ID, TargetType: "user", TargetID: target, Metadata: md}); err != nil {
				return err
			}
		}
		return s.emit(ctx, tx, EvStaffLinkConfirmed, staffID, map[string]any{"user_id": staffID, "staff_user_id": staffID,
			"personal_user_id": me.ID, "request_id": id})
	})
	if err != nil {
		if e := errs.As(err); e.Code == "TOKEN_INVALID" || e.Code == "STAFF_LINK_EMAIL_MISMATCH" {
			s.secEvent(ctx, p.UserID, "STAFF_LINK_CONFIRM_FAILED", map[string]any{"code": e.Code})
		}
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"status": "linked", "staff_user_id": staffID})
}

// sendStaffLinkEmail (worker) issues the token of a live request and mails the link to the requested address.
// A duplicate delivery replaces the token, so only the latest link works. Requests that are no longer live,
// or whose target became ineligible, are skipped.
func (s *Service) sendStaffLinkEmail(ctx context.Context, mail Mailer, requestID string) error {
	if !ids.Valid(requestID) {
		return fmt.Errorf("auth consumer: staff link without request_id")
	}
	tok, err := crypto.NewToken()
	if err != nil {
		return err
	}
	var to string
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var target *string
		err := tx.QueryRow(ctx, `SELECT email_normalized, target_user_id FROM app.staff_link_requests
			WHERE id = $1 AND consumed_at IS NULL AND invalidated_at IS NULL AND expires_at > $2 FOR UPDATE`, requestID, s.now()).Scan(&to, &target)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && target == nil {
			to = ""
			return nil
		}
		if err != nil {
			return err
		}
		acct, err := users.ByID(ctx, tx, *target)
		if err != nil || acct.Email != to || !acct.EmailVerified || acct.Status != users.StatusActive {
			to = ""
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE app.staff_link_requests SET token_hash = $2, token_issued_at = $3 WHERE id = $1`,
			requestID, crypto.HashToken(string(tok)), s.now())
		return err
	})
	if err != nil || to == "" {
		return err
	}
	return mail.SendEmail(ctx, to, "Confirm the link to your FundZim staff account",
		fmt.Sprintf("A FundZim staff member asked to link their staff account to this personal account, so that the system can stop them "+
			"reviewing anything that concerns you. Links cannot be removed.\n\nIf you are that staff member, sign in to this personal account "+
			"and open this link within %s:\n\n%s\n\nIf this was not you, ignore this email and tell the FundZim security team.",
			humanDuration(StaffLinkTTL), s.link("/dashboard/account/staff-link", string(tok))))
}
