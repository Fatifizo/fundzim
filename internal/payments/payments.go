// Package payments is the provider-neutral payment orchestration of Stage 8 (ADR-039): donation intents, payments
// (the canonical Stage 2 payment; "payment attempt" in the API), provider calls, provider transactions and
// references, payment sessions, the append-only event history, refund REQUESTS (never executed), and
// reconciliation references. It holds operational records only: no balances, totals, ledger postings or settlement
// state (CLAUDE.md financial rules 5 and 11).
//
// Every payment state change goes through apply (payment-state-machine §3.1): edge AND precedence, sources
// checked (a browser can never confirm a payment; a timeout can never fail one), one transaction with its event,
// in the database too (app.guard_transition, ck_payment_events_*, deferred require-event triggers).
package payments

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/Fatifizo/fundzim/internal/campaigns"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/crypto"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/ratelimit"
	"github.com/Fatifizo/fundzim/internal/psp"
	"github.com/Fatifizo/fundzim/internal/risk"
)

// Donation intent statuses.
const (
	DonationOpen      = "OPEN"
	DonationPaid      = "PAID"
	DonationExpired   = "EXPIRED"
	DonationCancelled = "CANCELLED"
)

// Event sources (payment_events.source).
const (
	SrcSync     = "SYNC"
	SrcWebhook  = "WEBHOOK"
	SrcPoll     = "POLL"
	SrcRecon    = "RECON"
	SrcInternal = "INTERNAL"
	SrcStaff    = "STAFF"
	SrcDonor    = "DONOR"
)

// Not-applied reasons.
const (
	NotLowerRank = "LOWER_RANK"
	NotAnEdge    = "NOT_AN_EDGE"
	NotAnomaly   = "ANOMALY"
	NotDuplicate = "DUPLICATE"
	NotParked    = "PARKED"
)

// Technical limits (ADR-039 §8): code constants, not regulatory thresholds.
const (
	TechMinAmountMinor = 1
	TechMaxAmountMinor = 1_000_000_000_000
	// DonationIntentTTL is how long a donation intent accepts new payment attempts.
	DonationIntentTTL = 24 * time.Hour
	// CreateDeadline / StatusDeadline bound provider calls (payment-provider-interfaces §6).
	CreateDeadline = 20 * time.Second
	StatusDeadline = 10 * time.Second
	// InFlightGrace is added to a call's deadline before the sweeper declares CRASH_DURING_CREATE.
	InFlightGrace = 30 * time.Second
	// UnknownHorizon is the default resolution horizon for UNKNOWN when the provider documents none (PCR-010):
	// 72 h, flagged in docs/stage-8/known-issues.md.
	UnknownHorizon = 72 * time.Hour
	// SessionTTL bounds a payment session when the provider gives no expiry.
	SessionTTL = 30 * time.Minute
)

// Errors (interface-contracts §5).
var (
	ErrCampaignNotAccepting = errs.New(errs.Unprocessable, "CAMPAIGN_NOT_ACCEPTING_DONATIONS", "This campaign is not accepting donations.")
	ErrDonationNotAvailable = errs.New(errs.Forbidden, "DONATION_NOT_AVAILABLE", "Donating is not available for this account. Contact support if you have questions.")
	ErrCurrencyNotAccepted  = errs.New(errs.Unprocessable, "CURRENCY_NOT_ACCEPTED", "This campaign does not accept donations in this currency.")
	ErrCurrencyNotAvailable = errs.New(errs.Unprocessable, "CURRENCY_NOT_AVAILABLE", "This currency is not available.")
	ErrAmountOutOfRange     = errs.New(errs.Unprocessable, "AMOUNT_OUT_OF_RANGE", "The amount is outside the allowed range.")
	ErrIntentNotFound       = errs.New(errs.NotFound, "DONATION_INTENT_NOT_FOUND", "No such donation.")
	ErrIntentNotOpen        = errs.New(errs.Conflict, "DONATION_INTENT_NOT_OPEN", "This donation can no longer be paid. Start a new donation.")
	ErrAlreadyPaid          = errs.New(errs.Conflict, "DONATION_ALREADY_PAID", "This donation has already been paid.")
	ErrInProgress           = errs.New(errs.Conflict, "PAYMENT_IN_PROGRESS", "A payment for this donation is still in progress.")
	ErrOutcomeUnknown       = errs.New(errs.Conflict, "PAYMENT_OUTCOME_UNKNOWN", "We're confirming your earlier payment with the provider. Please don't pay again.")
	ErrPaymentNotFound      = errs.New(errs.NotFound, "PAYMENT_NOT_FOUND", "No such payment.")
	ErrProviderNotFound     = errs.New(errs.NotFound, "PAYMENT_PROVIDER_NOT_FOUND", "No such payment provider.")
	ErrSessionInvalid       = errs.New(errs.NotFound, "PAYMENT_SESSION_INVALID", "This payment link is invalid or has already been used.")
	ErrStaffNotAllowed      = errs.New(errs.Forbidden, "ACCOUNT_KIND_NOT_ALLOWED", "Staff accounts cannot donate. Use your personal account.")
	ErrRefundNotAllowed     = errs.New(errs.Conflict, "REFUND_NOT_ALLOWED", "This payment cannot be refunded.")
	ErrRefundExceeds        = errs.New(errs.Unprocessable, "REFUND_EXCEEDS_REFUNDABLE", "The refund exceeds the refundable amount.")
	ErrRefundNotFound       = errs.New(errs.NotFound, "REFUND_REQUEST_NOT_FOUND", "No such refund request.")
	ErrRefundSelfApproval   = errs.New(errs.Forbidden, "REFUND_SELF_APPROVAL_FORBIDDEN", "You cannot decide a refund you requested.")
	ErrRefundState          = errs.New(errs.Conflict, "REFUND_REQUEST_STATE", "This refund request cannot be changed in its current status.")
	ErrReconNotFound        = errs.New(errs.NotFound, "RECONCILIATION_REFERENCE_NOT_FOUND", "No such reconciliation reference.")
	ErrReconState           = errs.New(errs.Conflict, "RECONCILIATION_REFERENCE_STATE", "This reconciliation reference cannot be changed in its current status.")
	ErrSandboxDisabled      = errs.New(errs.NotFound, "SANDBOX_DISABLED", "Not found.")
)

// Campaigns is the campaigns module's donation gate.
type Campaigns interface {
	EvaluateDonation(ctx context.Context, campaignID string) (campaigns.DonationTarget, campaigns.Result, error)
}

// DonorRestrictions reports whether any compliance restriction applies to a donor (implemented over
// compliance.Service in internal/app; fail closed).
type DonorRestrictions interface {
	Restricted(ctx context.Context, userID string) (bool, error)
}

// Holds reads the risk hold registry (risk.Service; Stage 13, ADR-045 §7).
type Holds interface {
	DonationHolds(ctx context.Context, campaignID, donorUserID string) ([]risk.Hold, error)
}

// ErrDonationsOnHold: a risk hold (campaign or donation hold) stops new donations. No detail is exposed.
var ErrDonationsOnHold = errs.New(errs.Conflict, "DONATIONS_ON_HOLD", "Donations to this campaign are temporarily unavailable while it is reviewed.")

// Limits reads approved money limits (risk.limits).
type Limits interface {
	MoneyLimits(ctx context.Context, key, currency string, at time.Time) ([]risk.MoneyLimit, error)
}

// PostingRequest is what Stage 10's ledger will post on SUCCEEDED (T1 'payment:{id}:capture').
type PostingRequest struct {
	Key         string
	Rule        string
	PaymentID   string
	CampaignID  string
	Currency    string
	AmountMinor int64
}

// LedgerPoster is the ledger seam (ROADMAP Stage 8: a test-only stub). Stage 8 wires NO poster at runtime: no
// ledger exists and the brief forbids postings. Tests inject a recorder to assert exactly one request per key.
type LedgerPoster interface {
	Post(ctx context.Context, tx pgx.Tx, r PostingRequest) error
}

// Deps are the service dependencies.
type Deps struct {
	Pool         *pgxpool.Pool
	Clock        clock.Clock
	Logger       *slog.Logger
	PSP          *psp.Service
	Campaigns    Campaigns
	Restrictions DonorRestrictions
	Limits       Limits
	// Holds enforces risk holds at intent and attempt creation (nil: no hold registry wired — tests only).
	Holds   Holds
	Limiter ratelimit.Limiter
	Keyed   *crypto.Keyed // payer instrument fingerprints (HMAC); never the raw value
	Ledger  LedgerPoster  // nil in Stage 8 runtime
	// Fees binds a fee quote to every donation intent (Stage 12, ADR-044 §9); nil: intents carry no quote.
	Fees       FeeQuoter
	Registerer prometheus.Registerer
	// PublicSiteURL builds the donor return URL given to providers (redirect flows).
	PublicSiteURL string
}

// Service is the payments public service.
type Service struct {
	Deps
	m *metrics
}

// New builds the service.
func New(d Deps) *Service {
	if d.Clock == nil {
		d.Clock = clock.System
	}
	return &Service{Deps: d, m: newMetrics(d.Registerer)}
}

func (s *Service) now() time.Time { return s.Clock.Now().UTC() }

type metrics struct {
	intents    *prometheus.CounterVec
	attempts   *prometheus.CounterVec
	unknown    *prometheus.CounterVec
	recon      *prometheus.CounterVec
	notApplied *prometheus.CounterVec
}

func newMetrics(reg prometheus.Registerer) *metrics {
	m := &metrics{
		intents: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "payment_intents_created_total",
			Help: "Donation intents created."}, []string{"simulated"}),
		attempts: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "payment_attempts_total",
			Help: "Payment attempts by method and initial outcome."}, []string{"method", "outcome"}),
		unknown: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "payment_unknown_outcomes_total",
			Help: "Payments that entered UNKNOWN, by provider and reason."}, []string{"provider", "reason"}),
		recon: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "payment_reconciliation_required_total",
			Help: "Reconciliation references opened, by reason."}, []string{"reason"}),
		notApplied: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "payment_events_not_applied_total",
			Help: "Provider observations recorded but not applied, by reason."}, []string{"reason"}),
	}
	if reg != nil {
		reg.MustRegister(m.intents, m.attempts, m.unknown, m.recon, m.notApplied)
	}
	return m
}
