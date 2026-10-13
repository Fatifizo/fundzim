// Package psp is the provider layer (design-baseline §2, ADR-007, ADR-029, ADR-039): the provider registry and
// versioned capabilities, provider accounts with secret REFERENCES, the adapter interfaces, routing, webhook
// ingestion (verify → inbox → dispatch), rejected-webhook records and provider health. Adapters translate only:
// they classify every call outcome and map provider statuses, but make no state, fee, eligibility or ledger
// decision. psp imports only platform and audit; payments registers its webhook handler at composition time.
//
// Stage 8 registers ONLY the sandbox simulator (internal/psp/sandbox). Real payment execution is impossible:
// LiveExecutionEnabled is a constant false, the payments.live_execution.enabled kill switch is off, and a real
// provider cannot be ENABLED without selection under an ADR (DB CHECK).
package psp

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// LiveExecutionEnabled gates every non-sandbox provider (ADR-039 §6). It is a constant on purpose: enabling real
// payments needs a code change reviewed under an ADR, legal sign-off and stage acceptance, not configuration.
const LiveExecutionEnabled = false

// KillSwitchFlag is the maker-checker kill switch checked in addition to LiveExecutionEnabled.
const KillSwitchFlag = "payments.live_execution.enabled"

// Method codes (Stage 2 enum). Categories are provider-neutral (ADR-039 §5).
const (
	MethodEcoCash      = "ECOCASH"
	MethodOneMoney     = "ONEMONEY"
	MethodInnBucks     = "INNBUCKS"
	MethodOMari        = "OMARI"
	MethodZimSwitch    = "ZIMSWITCH"
	MethodBankTransfer = "BANK_TRANSFER"
	MethodCard         = "CARD"
)

// Method categories.
const (
	CategoryMobileMoney      = "MOBILE_MONEY"
	CategoryCard             = "CARD"
	CategoryBankTransfer     = "BANK_TRANSFER"
	CategoryAccountToAccount = "ACCOUNT_TO_ACCOUNT"
	CategoryOther            = "OTHER"
)

var methodCategory = map[string]string{
	MethodEcoCash: CategoryMobileMoney, MethodOneMoney: CategoryMobileMoney, MethodInnBucks: CategoryMobileMoney,
	MethodOMari: CategoryMobileMoney, MethodZimSwitch: CategoryAccountToAccount, MethodBankTransfer: CategoryBankTransfer,
	MethodCard: CategoryCard,
}

// ValidMethod reports whether m is a known method code.
func ValidMethod(m string) bool { _, ok := methodCategory[m]; return ok }

// MethodCategory maps a method code to its provider-neutral category (OTHER for unknown codes).
func MethodCategory(m string) string {
	if c, ok := methodCategory[m]; ok {
		return c
	}
	return CategoryOther
}

// Class is the adapter's classification of one provider call (payment-provider-interfaces §5).
type Class string

const (
	ClassAccepted            Class = "ACCEPTED"
	ClassDefiniteFailure     Class = "DEFINITE_FAILURE"
	ClassRetryableBeforeSend Class = "RETRYABLE_BEFORE_SEND"
	ClassIndeterminate       Class = "INDETERMINATE"
	ClassNotFound            Class = "NOT_FOUND"
	ClassAuthConfig          Class = "AUTH_CONFIG_ERROR"
	ClassRateLimited         Class = "RATE_LIMITED"
	ClassNotSupported        Class = "NOT_SUPPORTED"
	ClassInvalidRequest      Class = "INVALID_REQUEST"
)

// Unknown-outcome reasons (attempt and payment unknown_reason).
const (
	UnknownTimeout          = "TIMEOUT"
	UnknownConnResetAfter   = "CONN_RESET_AFTER_SEND"
	UnknownHTTP5xx          = "HTTP_5XX"
	UnknownMalformed        = "MALFORMED_RESPONSE"
	UnknownSignatureInvalid = "SIGNATURE_INVALID_RESPONSE"
	UnknownCrashDuring      = "CRASH_DURING_CREATE"
)

// Canonical payment statuses (baseline §8). Adapters map provider statuses onto these; unmapped ⇒ UNKNOWN with
// Mapped=false (never guessed).
const (
	StatusCreated           = "CREATED"
	StatusPending           = "PENDING"
	StatusRequiresAction    = "REQUIRES_ACTION"
	StatusAuthorised        = "AUTHORISED"
	StatusUnknown           = "UNKNOWN"
	StatusSucceeded         = "SUCCEEDED"
	StatusFailed            = "FAILED"
	StatusExpired           = "EXPIRED"
	StatusCancelled         = "CANCELLED"
	StatusPartiallyRefunded = "PARTIALLY_REFUNDED"
	StatusRefunded          = "REFUNDED"
	StatusDisputed          = "DISPUTED"
	StatusChargedBack       = "CHARGED_BACK"
)

// Next-action types shown to the donor.
const (
	NextNone             = "NONE"
	NextRedirect         = "REDIRECT"
	NextAwaitHandset     = "AWAIT_HANDSET_APPROVAL"
	NextShowInstructions = "SHOW_INSTRUCTIONS"
)

// NextAction is what the donor must do next, as the provider asked.
type NextAction struct {
	Type             string
	RedirectURL      string
	InstructionsCode string
	ExpiresAt        *time.Time
}

// Observation is one provider answer: canonical AND raw (raw is never discarded).
type Observation struct {
	Class          Class
	UnknownReason  string // set for INDETERMINATE
	HTTPStatus     int
	FailureCode    string // provider's definitive failure reason, normalised (e.g. DECLINED)
	ProviderRef    string
	RawStatus      string
	Canonical      string
	Mapped         bool
	MappingVersion string
	Next           NextAction
	ReportedAmount *int64
	ReportedCcy    string
	PayerToken     []byte // provider payer token; HMAC'd by payments, never stored raw
	PayerMasked    string
	OccurredAt     *time.Time
	NotFoundFinal  bool // NOT_FOUND and the provider documents that the reference can no longer appear
	Latency        time.Duration
}

// Account is a provider account resolved for the current environment. Secret fields are REFERENCES.
type Account struct {
	ID                       string
	ProviderID               string
	Environment              string
	Label                    string
	MerchantReference        string
	APISecretRef             string
	WebhookSecretRef         string
	WebhookSecretRefPrevious string
	ReplayWindow             time.Duration
	Status                   string
}

// CallMeta accompanies every outbound call.
type CallMeta struct {
	CorrelationID      string
	AttemptID          string // the payment_attempts row committed IN_FLIGHT before the call
	Deadline           time.Time
	Account            Account
	ReferenceCreatedAt time.Time // when our reference was first sent (status-query finality windows)
}

// CreatePaymentRequest is a provider-neutral payment creation. PaymentID is our reference AND the provider
// idempotency key; it never changes.
type CreatePaymentRequest struct {
	PaymentID   string
	AmountMinor int64
	Currency    string
	Method      string
	Description string
	ReturnURL   string            // where the provider sends the donor back (carries a single-use session token)
	Metadata    map[string]string // allow-listed keys only (e.g. sandbox_scenario outside production)
}

// Limits are an adapter's declared per-method/currency amount limits (provider limits, ADR-039 §8).
type Limits struct {
	MinMinor int64
	MaxMinor int64
}

// PaymentProvider creates and queries payments. Unsupported operations return Class NOT_SUPPORTED.
type PaymentProvider interface {
	CreatePayment(ctx context.Context, m CallMeta, r CreatePaymentRequest) Observation
	GetPayment(ctx context.Context, m CallMeta, paymentID string) Observation
	CancelPayment(ctx context.Context, m CallMeta, paymentID string) Observation
}

// Inbound is a raw webhook delivery (exact bytes as received).
type Inbound struct {
	Body       []byte
	Header     http.Header
	ReceivedAt time.Time
}

// VerifiedBy records how a webhook was authenticated.
type VerifiedBy struct {
	Method  string // SIGNATURE | STATUS_API_CONFIRM
	KeySlot string // CURRENT | PREVIOUS
}

// WebhookEvent is a parsed provider event. RawStatus is verbatim; mapping happens when the event is applied
// (MapPaymentStatus), so an UNRECOGNISED event can be re-dispatched after the mapping is extended.
type WebhookEvent struct {
	ProviderEventID   string
	Class             string // PAYMENT | REFUND | DISPUTE | PAYOUT | OTHER
	TypeRaw           string
	ProviderReference string
	OurReference      string
	OccurredAt        *time.Time
	RawStatus         string
	ReportedAmount    *int64
	ReportedCcy       string
	Redacted          map[string]any // allow-listed provider fields only
	Simulated         bool
}

// Verification errors (each maps to a rejection reason code; nothing is stored for a rejected webhook).
var (
	ErrSignatureMissing = errors.New("psp: webhook signature missing")
	ErrSignatureInvalid = errors.New("psp: webhook signature invalid")
	ErrUnknownKey       = errors.New("psp: webhook key id unknown")
	ErrTimestamp        = errors.New("psp: webhook timestamp outside the replay window")
	ErrMalformed        = errors.New("psp: webhook payload malformed")
	ErrSimulation       = errors.New("psp: webhook simulation flag does not match the provider")
)

// WebhookVerifier authenticates and parses a provider's webhooks with the provider's DOCUMENTED procedure.
type WebhookVerifier interface {
	Verify(ctx context.Context, acct Account, in Inbound) (VerifiedBy, error)
	Parse(ctx context.Context, in Inbound) ([]WebhookEvent, error)
}

// Adapter is one provider integration.
type Adapter interface {
	Code() string
	PaymentProvider
	WebhookVerifier
	// MapPaymentStatus maps a raw provider payment status (versioned table); unmapped ⇒ ("UNKNOWN", false).
	MapPaymentStatus(raw string) (canonical string, mapped bool, version string)
	// Limits returns the provider's amount limits for (method, currency); ok=false when none are known
	// (an unknown provider limit excludes the provider: fail closed).
	Limits(method, currency string) (Limits, bool)
	// RedirectHosts lists the hosts the provider may send donors to (redirect validation).
	RedirectHosts() []string
}
