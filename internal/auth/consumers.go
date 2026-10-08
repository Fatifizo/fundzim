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
		{"identity.send_existing_account_notice", EvRegistrationExisting, h(func(ctx context.Context, p eventPayload) error {
			return s.sendNotice(ctx, mail, p.UserID, "Someone tried to register with your email",
				"Someone tried to create a FundZim account with this email address. You already have an account.\n\n"+
					"If this was you, sign in or reset your password at "+s.link("/forgot-password", "")+".\nIf it was not you, you can ignore this email.")
		})},
		{"identity.send_password_reset_email", EvPasswordResetRequested, h(func(ctx context.Context, p eventPayload) error {
			return s.sendTokenEmail(ctx, mail, p.UserID, p.EmailID, "PASSWORD_RESET", s.Cfg.PasswordResetTTL, "/reset-password",
				"Reset your FundZim password",
				"Reset your password by opening this link within %s:\n\n%s\n\nIf you did not ask to reset your password, ignore this email; your password has not changed.")
		})},
		{"identity.send_password_changed_notice", EvPasswordChanged, h(func(ctx context.Context, p eventPayload) error {
			return s.sendNotice(ctx, mail, p.UserID, "Your FundZim password was changed",
				"The password for your FundZim account was just changed and other devices were signed out.\n\n"+
					"If you did not do this, reset your password now at "+s.link("/forgot-password", "")+" and contact support.")
		})},
		{"identity.send_mfa_changed_notice", EvMFAChanged, h(func(ctx context.Context, p eventPayload) error {
			what := map[string]string{"enabled": "Two-step verification was turned on", "disabled": "Two-step verification was turned off",
				"recovery_code_used": "A recovery code was used to sign in", "recovery_codes_regenerated": "New recovery codes were generated"}[p.Change]
			if what == "" {
				what = "Your two-step verification settings changed"
			}
			return s.sendNotice(ctx, mail, p.UserID, "Security alert: two-step verification",
				what+" on your FundZim account.\n\nIf you did not do this, reset your password at "+s.link("/forgot-password", "")+" and contact support.")
		})},
		{"identity.send_email_change_confirmation", EvEmailChangeRequested, h(func(ctx context.Context, p eventPayload) error {
			if err := s.sendTokenEmail(ctx, mail, p.UserID, p.EmailID, "EMAIL_CHANGE", s.Cfg.EmailVerificationTTL, "/verify-email",
				"Confirm your new FundZim email address",
				"Confirm this as the new sign-in address for your FundZim account by opening this link within %s:\n\n%s\n\nIf you did not ask for this, ignore this email."); err != nil {
				return err
			}
			return s.sendNotice(ctx, mail, p.UserID, "Your FundZim email address is being changed",
				"Someone asked to change the sign-in email address of your FundZim account. The change happens only after the new address is confirmed.\n\n"+
					"If this was not you, reset your password at "+s.link("/forgot-password", "")+" and contact support.")
		})},
		{"identity.send_staff_invitation", EvStaffInvited, h(func(ctx context.Context, p eventPayload) error {
			return s.sendTokenEmail(ctx, mail, p.UserID, p.EmailID, "STAFF_INVITATION", s.Cfg.StaffInvitationTTL, "/staff/accept-invitation",
				"Your FundZim staff account invitation",
				"You have been invited to a FundZim staff account. Set your password and two-step verification within %s:\n\n%s\n\nIf you were not expecting this, ignore this email and tell the FundZim security team.")
		})},
		{"identity.send_account_suspended_notice", EvAccountSuspended, h(func(ctx context.Context, p eventPayload) error {
			return s.sendNotice(ctx, mail, p.UserID, "Your FundZim account has been suspended",
				"Your FundZim account has been suspended and all sessions were signed out. Contact support if you have questions.")
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

// sendNotice emails the account's current sign-in address.
func (s *Service) sendNotice(ctx context.Context, mail Mailer, userID, subject, body string) error {
	acct, err := users.ByID(ctx, s.Pool, userID)
	if errors.Is(err, users.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if acct.IsSystem {
		return nil
	}
	return mail.SendEmail(ctx, acct.Email, subject, body)
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
