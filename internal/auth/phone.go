package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/nyaruka/phonenumbers"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/crypto"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/logging"
	"github.com/Fatifizo/fundzim/internal/users"
)

// DefaultPhoneRegion is used for numbers entered without a country code (Zimbabwe-first).
const DefaultPhoneRegion = "ZW"

// NormalizePhone parses a user-entered number into E.164 and rejects numbers that are not valid
// (libphonenumber metadata). Only mobile-capable numbers can receive an SMS code.
func NormalizePhone(raw string) (string, error) {
	n, err := phonenumbers.Parse(raw, DefaultPhoneRegion)
	if err != nil || !phonenumbers.IsValidNumber(n) {
		return "", errors.New("invalid phone number")
	}
	switch phonenumbers.GetNumberType(n) {
	case phonenumbers.MOBILE, phonenumbers.FIXED_LINE_OR_MOBILE:
	default:
		return "", errors.New("not a mobile number")
	}
	return phonenumbers.Format(n, phonenumbers.E164), nil
}

func (s *Service) phoneHMAC(e164 string) []byte { return s.Keyed.Sum("otp_destination", "SMS", e164) }

func (s *Service) otpHMAC(challengeID, code string) []byte {
	return s.Keyed.Sum("otp_code", challengeID, code)
}

type phoneRequestReq struct {
	Phone string `json:"phone"`
}

// PhoneVerifyRequest sends a 6-digit code to the given number. A previous live code for that number is
// invalidated. The SMS is sent after the challenge commits (never inside a transaction).
func (s *Service) PhoneVerifyRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	var req phoneRequestReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	e164, err := NormalizePhone(req.Phone)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, httpx.Validation(errs.Detail{Field: "phone", Code: "INVALID_PHONE"}))
		return
	}
	if err := s.throttle(ctx, "auth.otp.send.user", p.UserID); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	if err := s.throttle(ctx, "auth.otp.send.phone", e164); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	if cur, err := users.PrimaryPhone(ctx, s.Pool, p.UserID); err == nil && cur.E164 == e164 {
		httpx.WriteError(w, r, s.Logger, errs.New(errs.Conflict, "PHONE_ALREADY_VERIFIED", "This number is already verified on your account."))
		return
	}
	code, err := crypto.NumericCode(6)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	id, flowID := ids.New(), ids.New()
	dest := s.phoneHMAC(e164)
	now := s.now()
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE app.otp_challenges SET invalidated_at = $2
			WHERE destination_hmac = $1 AND purpose = 'VERIFY_PHONE' AND consumed_at IS NULL AND invalidated_at IS NULL`, dest, now); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO app.otp_challenges (id, user_id, purpose, channel, destination_hmac, code_hmac, hmac_key_id,
			flow_id, requested_ip, created_at, expires_at) VALUES ($1, $2, 'VERIFY_PHONE', 'SMS', $3, $4, $5, $6, $7::inet, $8, $9)`,
			id, p.UserID, dest, s.otpHMAC(id, code), s.Keyed.KeyID(), flowID, ipString(ctx), now, now.Add(s.Cfg.OTPTTL))
		return err
	})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	if err := s.SMS.SendSMS(ctx, e164, "Your FundZim verification code is "+code+". It expires in 5 minutes. Never share this code."); err != nil {
		logging.FromContext(ctx, s.Logger).Error("sms send failed", slog.String("error_category", "dependency"), slog.String("error", err.Error()))
		_, _ = s.Pool.Exec(ctx, `UPDATE app.otp_challenges SET invalidated_at = $2 WHERE id = $1 AND consumed_at IS NULL AND invalidated_at IS NULL`, id, s.now())
		httpx.WriteError(w, r, s.Logger, errs.New(errs.Unavailable, errs.CodeServiceUnavailable, "We could not send a code right now. Please try again shortly."))
		return
	}
	s.secEvent(ctx, p.UserID, "OTP_SENT", map[string]any{"purpose": "VERIFY_PHONE"})
	httpx.WriteData(w, r, http.StatusAccepted, map[string]any{"phone_masked": users.MaskPhone(e164), "expires_at": now.Add(s.Cfg.OTPTTL)})
}

type phoneConfirmReq struct {
	Phone string `json:"phone"`
	Code  string `json:"code"`
}

// PhoneVerifyConfirm checks the code (bound to the user and the number), counts attempts and,
// on success, records the number as the user's verified primary phone.
func (s *Service) PhoneVerifyConfirm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	var req phoneConfirmReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	e164, err := NormalizePhone(req.Phone)
	if err != nil || len(req.Code) != 6 {
		httpx.WriteError(w, r, s.Logger, errOTPInvalid())
		return
	}
	if err := s.throttle(ctx, "auth.otp.verify", p.UserID); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	var ok bool
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var id string
		var codeHMAC []byte
		var attempts int
		err := tx.QueryRow(ctx, `SELECT id, code_hmac, attempts FROM app.otp_challenges
			WHERE destination_hmac = $1 AND purpose = 'VERIFY_PHONE' AND user_id = $2
			  AND consumed_at IS NULL AND invalidated_at IS NULL AND expires_at > $3 FOR UPDATE`,
			s.phoneHMAC(e164), p.UserID, s.now()).Scan(&id, &codeHMAC, &attempts)
		if errors.Is(err, pgx.ErrNoRows) {
			return errOTPInvalid()
		}
		if err != nil {
			return err
		}
		if !crypto.Equal(codeHMAC, s.otpHMAC(id, req.Code)) {
			attempts++
			var inval any
			if attempts >= s.Cfg.OTPMaxAttempts {
				inval = s.now()
			}
			_, err := tx.Exec(ctx, `UPDATE app.otp_challenges SET attempts = $2, invalidated_at = $3 WHERE id = $1`, id, attempts, inval)
			return err // commit the attempt count; ok stays false
		}
		if _, err := tx.Exec(ctx, `UPDATE app.otp_challenges SET consumed_at = $2 WHERE id = $1`, id, s.now()); err != nil {
			return err
		}
		if err := users.SetVerifiedPhone(ctx, tx, p.UserID, e164, s.now()); err != nil {
			if errors.Is(err, users.ErrPhoneTaken) {
				return errs.New(errs.Conflict, "PHONE_IN_USE", "This number is already verified on another account.")
			}
			return err
		}
		ok = true
		return audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "auth.phone.verified", TargetType: "user", TargetID: p.UserID})
	})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	if !ok {
		s.secEvent(ctx, p.UserID, "OTP_VERIFY_FAILED", map[string]any{"purpose": "VERIFY_PHONE"})
		httpx.WriteError(w, r, s.Logger, errOTPInvalid())
		return
	}
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"phone_verified": true, "phone_masked": users.MaskPhone(e164)})
}

func errOTPInvalid() error {
	return errs.New(errs.Unprocessable, "OTP_INVALID", "That code is incorrect or has expired.")
}
