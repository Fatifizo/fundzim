package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/platform/crypto"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/users"
)

// Outbox consumers (run in the worker). Raw link tokens are generated here, at send time, so they exist
// only in the outgoing email: the database stores the SHA-256 hash, the outbox payload holds IDs only,
// and nothing is logged. Delivery is at least once; a duplicate delivery issues a fresh token that
// invalidates the previous one (uq_auth_tokens_live), so at most one link per purpose ever works.

// Consumer is one outbox subscription offered by this module.
type Consumer struct {
	Name, EventType string
	Handle          func(ctx context.Context, payload json.RawMessage) error
}

type eventPayload struct {
	UserID  string `json:"user_id"`
	EmailID string `json:"email_id"`
	Change  string `json:"change"`
	Via     string `json:"via"`
	// staff links (ADR-037 §4)
	RequestID      string `json:"request_id"`
	PersonalUserID string `json:"personal_user_id"`
}

// Consumers lists the auth module's outbox consumers; internal/app registers them with the outbox
// registry. mail must be non-nil.
func (s *Service) Consumers(mail Mailer) []Consumer {
	h := func(fn func(context.Context, eventPayload) error) func(context.Context, json.RawMessage) error {
		return func(ctx context.Context, raw json.RawMessage) error {
			var p eventPayload
			if err := json.Unmarshal(raw, &p); err != nil || !ids.Valid(p.UserID) {
				return fmt.Errorf("auth consumer: malformed payload")
			}
			return fn(ctx, p)
		}
	}
	return []Consumer{
		{"identity.send_verification_email", EvEmailVerificationRequested, h(func(ctx context.Context, p eventPayload) error {
			return s.sendTokenEmail(ctx, mail, p.UserID, p.EmailID, "EMAIL_VERIFICATION", s.Cfg.EmailVerificationTTL, "/verify-email",
				"Confirm your FundZim email address",
				"Confirm your email address by opening this link within %s:\n\n%s\n\nIf you did not create a FundZim account, ignore this email.")
		})},
		{"identity.send_password_reset_email", EvPasswordResetRequested, h(func(ctx context.Context, p eventPayload) error {
			return s.sendTokenEmail(ctx, mail, p.UserID, p.EmailID, "PASSWORD_RESET", s.Cfg.PasswordResetTTL, "/reset-password",
				"Reset your FundZim password",
				"Reset your password by opening this link within %s:\n\n%s\n\nIf you did not ask to reset your password, ignore this email; your password has not changed.")
		})},
		{"identity.send_email_change_confirmation", EvEmailChangeRequested, h(func(ctx context.Context, p eventPayload) error {
			// the notice to the current address is a SECURITY notification of the Stage 15 engine (EMAIL_CHANGE_REQUESTED)
			return s.sendTokenEmail(ctx, mail, p.UserID, p.EmailID, "EMAIL_CHANGE", s.Cfg.EmailVerificationTTL, "/verify-email",
				"Confirm your new FundZim email address",
				"Confirm this as the new sign-in address for your FundZim account by opening this link within %s:\n\n%s\n\nIf you did not ask for this, ignore this email.")
		})},
		{"identity.send_staff_invitation", EvStaffInvited, h(func(ctx context.Context, p eventPayload) error {
			return s.sendTokenEmail(ctx, mail, p.UserID, p.EmailID, "STAFF_INVITATION", s.Cfg.StaffInvitationTTL, "/staff/accept-invitation",
				"Your FundZim staff account invitation",
				"You have been invited to a FundZim staff account. Set your password and two-step verification within %s:\n\n%s\n\nIf you were not expecting this, ignore this email and tell the FundZim security team.")
		})},
		{"identity.send_staff_link_email", EvStaffLinkRequested, h(func(ctx context.Context, p eventPayload) error {
			return s.sendStaffLinkEmail(ctx, mail, p.RequestID)
		})},
	}
}

func (s *Service) link(path, token string) string {
	if token == "" {
		return s.Cfg.PublicURL + path
	}
	return s.Cfg.PublicURL + path + "?token=" + url.QueryEscape(token)
}

// issueToken creates a fresh token for (user, purpose), invalidating any live one, and returns the raw token.
func (s *Service) issueToken(ctx context.Context, userID, emailID, purpose string, ttl time.Duration) (crypto.Token, error) {
	tok, err := crypto.NewToken()
	if err != nil {
		return "", err
	}
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		now := s.now()
		if _, err := tx.Exec(ctx, `UPDATE app.auth_tokens SET invalidated_at = $3 WHERE user_id = $1 AND purpose = $2
			AND consumed_at IS NULL AND invalidated_at IS NULL`, userID, purpose, now); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO app.auth_tokens (id, user_id, purpose, user_email_id, token_hash, created_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, ids.New(), userID, purpose, nullable(emailID), crypto.HashToken(string(tok)), now, now.Add(ttl))
		return err
	})
	return tok, err
}

// sendTokenEmail issues a token and emails the link to the address emailID. It skips silently when the
// action no longer applies (address already verified, account gone or suspended, staff already active).
func (s *Service) sendTokenEmail(ctx context.Context, mail Mailer, userID, emailID, purpose string, ttl time.Duration, path, subject, body string) error {
	if !ids.Valid(emailID) {
		return fmt.Errorf("auth consumer: %s without email_id", purpose)
	}
	acct, err := users.ByID(ctx, s.Pool, userID)
	if errors.Is(err, users.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if acct.Status != users.StatusActive || acct.IsSystem {
		return nil
	}
	to, verified, err := users.EmailByID(ctx, s.Pool, userID, emailID)
	if errors.Is(err, users.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	switch purpose {
	case "EMAIL_VERIFICATION", "EMAIL_CHANGE":
		if verified {
			return nil
		}
	case "PASSWORD_RESET":
		if emailID != acct.EmailID {
			return nil // the sign-in address changed since the request
		}
	case "STAFF_INVITATION":
		if _, err := s.currentPasswordHash(ctx, userID); err == nil {
			return nil // already activated
		}
	}
	tok, err := s.issueToken(ctx, userID, emailID, purpose, ttl)
	if err != nil {
		return err
	}
	return mail.SendEmail(ctx, to, subject, fmt.Sprintf(body, humanDuration(ttl), s.link(path, string(tok))))
}

func humanDuration(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%d days", int(d/(24*time.Hour)))
	case d >= 2*time.Hour:
		return fmt.Sprintf("%d hours", int(d/time.Hour))
	default:
		return fmt.Sprintf("%d minutes", int(d/time.Minute))
	}
}
