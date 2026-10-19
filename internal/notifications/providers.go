package notifications

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Provider is one delivery channel's provider adapter (email: SendEmail; SMS: SendSMS; WhatsApp: SendTemplateMessage —
// one interface, the channel is part of the message). No production provider exists (ADR-047): the implementations are
// the TEST_ONLY simulators and the development senders (Mailpit).
type Provider interface {
	Code() string
	Channel() string
	Simulated() bool
	// Send submits one message. Idempotency key: the provider must not create a second message for the same key where it
	// supports keys (the simulators do; SMTP does not — a retry after an ambiguous outcome can duplicate an e-mail).
	Send(ctx context.Context, m Outbound) (SendResult, error)
	// Status looks a message up by its idempotency key (GetDeliveryStatus). ErrStatusUnsupported when the provider cannot.
	Status(ctx context.Context, idempotencyKey string) (StatusResult, error)
	// VerifyWebhook authenticates a callback and parses it (ErrWebhook* on refusal). Providers without callbacks return
	// ErrWebhookUnsupported.
	VerifyWebhook(h http.Header, body []byte, now time.Time) (Callback, error)
	Capabilities() Capabilities
	ValidateConfiguration() error
}

// Outbound is one rendered message for one destination.
type Outbound struct {
	DeliveryID, IdempotencyKey, Channel, To string
	Message                                 Rendered
}

// SendResult is a provider's synchronous answer. Accepted is never delivery.
type SendResult struct {
	Status            string // SUBMITTED | ACCEPTED | DELIVERED (a synchronous channel)
	ProviderMessageID string
}

// StatusResult is a provider status lookup.
type StatusResult struct {
	Found             bool
	Status            string // SUBMITTED | ACCEPTED | DELIVERED | READ | FAILED
	ProviderMessageID string
}

// Callback is one verified provider status report.
type Callback struct {
	ProviderEventID, ProviderMessageID string
	Status                             string // SUBMITTED ACCEPTED DELIVERED READ BOUNCED REJECTED UNDELIVERABLE FAILED TEMPORARY_FAILURE
	OccurredAt                         time.Time
}

// Capabilities is what an adapter can do (documented, not assumed of any real provider).
type Capabilities struct {
	DeliveryStatus bool `json:"delivery_status"`
	Webhooks       bool `json:"webhooks"`
	HTML           bool `json:"html"`
	ReadReceipts   bool `json:"read_receipts"`
	FreeForm       bool `json:"free_form"` // WhatsApp: false — template messages only
	IdempotencyKey bool `json:"idempotency_key"`
}

// Error classes of a send.
const (
	ClassTemporary   = "TEMPORARY"
	ClassRateLimited = "RATE_LIMITED"
	ClassPermanent   = "PERMANENT"
	ClassTimeout     = "TIMEOUT" // the provider may have received it: the outcome is UNKNOWN
)

// SendError is a classified provider error.
type SendError struct {
	Class      string
	Code       string // UPPER_SNAKE, stored on the attempt (never provider text)
	RetryAfter time.Duration
	// InvalidDestination: a permanent failure caused by the destination itself (suppress it).
	InvalidDestination bool
}

func (e *SendError) Error() string {
	return "notifications: " + strings.ToLower(e.Class) + ": " + e.Code
}

var (
	ErrStatusUnsupported  = errors.New("notifications: provider has no status lookup")
	ErrWebhookUnsupported = errors.New("notifications: provider has no callbacks")
	ErrWebhookSignature   = errors.New("notifications: webhook signature invalid")
	ErrWebhookTimestamp   = errors.New("notifications: webhook timestamp outside the window")
	ErrWebhookPayload     = errors.New("notifications: webhook payload invalid")
)

// classify turns any adapter error into a SendError.
func classify(err error) *SendError {
	var se *SendError
	switch {
	case errors.As(err, &se):
		return se
	case errors.Is(err, context.DeadlineExceeded):
		return &SendError{Class: ClassTimeout, Code: "PROVIDER_TIMEOUT"}
	case errors.Is(err, ErrInvalidMessage):
		return &SendError{Class: ClassPermanent, Code: "INVALID_MESSAGE"}
	case errors.Is(err, ErrDisabled):
		return &SendError{Class: ClassPermanent, Code: "PROVIDER_DISABLED"}
	case errors.Is(err, ErrInTransaction):
		return &SendError{Class: ClassTemporary, Code: "SEND_IN_TRANSACTION"}
	}
	return &SendError{Class: ClassTemporary, Code: "PROVIDER_ERROR"}
}

// ---- development senders ------------------------------------------------------------------------------------------

// DevSMTP adapts the Stage 3 SMTP sender (Mailpit locally). Acceptance by the SMTP server is reported as ACCEPTED, never
// DELIVERED. Refused outside development and test (configuration).
type DevSMTP struct{ Sender EmailSender }

func (DevSMTP) Code() string               { return "email_dev_smtp" }
func (DevSMTP) Channel() string            { return ChEmail }
func (DevSMTP) Simulated() bool            { return false }
func (DevSMTP) Capabilities() Capabilities { return Capabilities{} }
func (p DevSMTP) ValidateConfiguration() error {
	if p.Sender == nil {
		return errors.New("notifications: dev SMTP has no sender")
	}
	return nil
}
func (p DevSMTP) Send(ctx context.Context, m Outbound) (SendResult, error) {
	if err := p.Sender.Send(ctx, Email{To: m.To, Subject: m.Message.Subject, Text: m.Message.Text, HTML: m.Message.HTML}); err != nil {
		return SendResult{}, classify(err)
	}
	return SendResult{Status: "ACCEPTED"}, nil
}
func (DevSMTP) Status(context.Context, string) (StatusResult, error) {
	return StatusResult{}, ErrStatusUnsupported
}
func (DevSMTP) VerifyWebhook(http.Header, []byte, time.Time) (Callback, error) {
	return Callback{}, ErrWebhookUnsupported
}

// DevSMS adapts the development SMS sender (an e-mail to <digits>@sms.dev.invalid in Mailpit).
type DevSMS struct{ Sender SMSSender }

func (DevSMS) Code() string               { return "sms_dev_mailpit" }
func (DevSMS) Channel() string            { return ChSMS }
func (DevSMS) Simulated() bool            { return false }
func (DevSMS) Capabilities() Capabilities { return Capabilities{} }
func (p DevSMS) ValidateConfiguration() error {
	if p.Sender == nil {
		return errors.New("notifications: dev SMS has no sender")
	}
	return nil
}
func (p DevSMS) Send(ctx context.Context, m Outbound) (SendResult, error) {
	if err := p.Sender.Send(ctx, SMS{To: m.To, Body: m.Message.SMS}); err != nil {
		return SendResult{}, classify(err)
	}
	return SendResult{Status: "ACCEPTED"}, nil
}
func (DevSMS) Status(context.Context, string) (StatusResult, error) {
	return StatusResult{}, ErrStatusUnsupported
}
func (DevSMS) VerifyWebhook(http.Header, []byte, time.Time) (Callback, error) {
	return Callback{}, ErrWebhookUnsupported
}

// maskEmail keeps the first character and the domain: alice@example.test → a****@example.test.
func maskEmail(addr string) string {
	at := strings.LastIndexByte(addr, '@')
	if at < 1 {
		return "***"
	}
	return addr[:1] + strings.Repeat("*", max(1, min(at-1, 6))) + addr[at:]
}

func maskDestination(channel, to string) string {
	if channel == ChEmail {
		return maskEmail(to)
	}
	return MaskPhone(to)
}

func fmtCode(prefix string, n int) string { return fmt.Sprintf("%s_%d", prefix, n) }
