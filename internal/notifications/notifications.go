// Package notifications sends transactional email and SMS (interface contract §2.4).
//
// Rules: senders never log message bodies, links, codes or full recipient numbers; every send refuses
// to run inside a database transaction (db.InTx) because it is an outbound network call (BP-3); a
// disabled sender returns ErrDisabled instead of silently dropping the message.
//
// Only development providers exist today: SMTP (Mailpit locally) for email, and dev_mailpit for SMS,
// which turns an SMS into an email to <e164-without-plus>@sms.dev.invalid so codes reach a developer's
// Mailpit inbox and never the logs. Real providers are chosen in Stage 15.
package notifications

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Fatifizo/fundzim/internal/platform/config"
	"github.com/Fatifizo/fundzim/internal/platform/db"
)

// Email is one message to one recipient. HTML is optional.
type Email struct{ To, Subject, Text, HTML string }

// EmailSender sends email.
type EmailSender interface {
	Send(ctx context.Context, m Email) error
}

// SMS is one text message. To is E.164.
type SMS struct{ To, Body string }

// SMSSender sends SMS.
type SMSSender interface {
	Send(ctx context.Context, m SMS) error
}

var (
	// ErrDisabled is returned by a sender whose provider is "disabled".
	ErrDisabled = errors.New("notifications: provider disabled")
	// ErrInTransaction is returned when a send is attempted inside a database transaction.
	ErrInTransaction = errors.New("notifications: refusing an outbound call inside a database transaction")
	// ErrInvalidMessage is returned for a message that cannot be sent as given.
	ErrInvalidMessage = errors.New("notifications: invalid message")
)

// NewEmailSender returns the configured email sender (smtp | disabled).
func NewEmailSender(cfg config.Email) (EmailSender, error) {
	switch cfg.Provider {
	case "smtp":
		return newSMTPSender(cfg)
	case "disabled":
		return disabledEmail{}, nil
	default:
		return nil, fmt.Errorf("notifications: unknown EMAIL_PROVIDER")
	}
}

// NewSMSSender returns the configured SMS sender (dev_mailpit | disabled). dev_mailpit needs an email
// sender.
func NewSMSSender(cfg config.SMS, email EmailSender) (SMSSender, error) {
	switch cfg.Provider {
	case "dev_mailpit":
		if email == nil {
			return nil, errors.New("notifications: dev_mailpit SMS needs an email sender")
		}
		return &devMailpitSMS{email: email}, nil
	case "disabled":
		return disabledSMS{}, nil
	default:
		return nil, fmt.Errorf("notifications: unknown SMS_PROVIDER")
	}
}

type disabledEmail struct{}

func (disabledEmail) Send(ctx context.Context, _ Email) error {
	if inTx(ctx) {
		return ErrInTransaction
	}
	return ErrDisabled
}

type disabledSMS struct{}

func (disabledSMS) Send(ctx context.Context, _ SMS) error {
	if inTx(ctx) {
		return ErrInTransaction
	}
	return ErrDisabled
}

// inTx reports whether ctx is inside a database transaction (db.InTx; replaceable in unit tests only).
var inTx = db.InTx

var e164RE = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)

// devMailpitSMS delivers an SMS as an email to <digits>@sms.dev.invalid (development and test only;
// configuration refuses it elsewhere).
type devMailpitSMS struct{ email EmailSender }

func (s *devMailpitSMS) Send(ctx context.Context, m SMS) error {
	if inTx(ctx) {
		return ErrInTransaction
	}
	if !e164RE.MatchString(m.To) {
		return fmt.Errorf("%w: SMS recipient must be E.164", ErrInvalidMessage)
	}
	if strings.TrimSpace(m.Body) == "" {
		return fmt.Errorf("%w: empty SMS body", ErrInvalidMessage)
	}
	return s.email.Send(ctx, Email{
		To:      strings.TrimPrefix(m.To, "+") + "@sms.dev.invalid",
		Subject: "SMS to " + MaskPhone(m.To),
		Text:    m.Body,
	})
}

// MaskPhone keeps the country-code-ish prefix and the last two digits: +263771234567 → +263*******67.
func MaskPhone(e164 string) string {
	if len(e164) < 7 {
		return "***"
	}
	return e164[:4] + strings.Repeat("*", len(e164)-6) + e164[len(e164)-2:]
}
