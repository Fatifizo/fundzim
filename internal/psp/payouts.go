package psp

import (
	"context"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"
)

// Payout provider abstraction (Stage 11, ADR-043; payout-lifecycle.md §5). Payouts reuse the call classification
// (Class), the unknown-outcome reasons and the Observation of payments: a payout call is ACCEPTED, a DEFINITE_FAILURE,
// RETRYABLE_BEFORE_SEND (nothing reached the provider: the same reference may be retried), INDETERMINATE (the
// outcome is UNKNOWN: never resubmitted), NOT_FOUND, … Unsupported operations return Class NOT_SUPPORTED.
// No real provider implements this interface: the only implementation is the sandbox simulator.

// Canonical payout statuses. Adapters map provider statuses onto these; unmapped ⇒ PayoutUnknown, Mapped=false.
const (
	PayoutProcessing = "PROCESSING" // accepted / in progress at the provider
	PayoutCompleted  = "COMPLETED"  // the provider confirms the disbursement
	PayoutFailed     = "FAILED"     // the provider confirms it did not and will not happen
	PayoutReturned   = "RETURNED"   // a completed disbursement came back to the pool
	PayoutUnknown    = "UNKNOWN"
)

// CreatePayoutRequest is a provider-neutral disbursement instruction. PayoutID is our reference AND the
// idempotency reference on every attempt; it never changes. Stage 11 passes only the masked destination: the
// simulator needs no account number, and no real adapter (which would need a decryption path for the C3 account
// identifier) exists.
type CreatePayoutRequest struct {
	PayoutID          string
	AmountMinor       int64
	Currency          string
	Rail              string
	DestinationMasked string
	Metadata          map[string]string // allow-listed keys only (e.g. sandbox_scenario outside production)
}

// PayoutProvider is implemented by adapters whose provider supports payouts.
type PayoutProvider interface {
	CreatePayout(ctx context.Context, m CallMeta, r CreatePayoutRequest) Observation
	GetPayout(ctx context.Context, m CallMeta, payoutID string) Observation
	CancelPayout(ctx context.Context, m CallMeta, payoutID string) Observation
	// MapPayoutStatus maps a raw provider payout status (versioned table); unmapped ⇒ ("UNKNOWN", false).
	MapPayoutStatus(raw string) (canonical string, mapped bool, version string)
	// PayoutLimits returns the provider's payout amount limits for (rail, currency); ok=false when none are known
	// (an unknown provider limit is a REVIEW, never an assumption).
	PayoutLimits(rail, currency string) (Limits, bool)
}

// PayoutAdapter returns the registered adapter for a provider code if it supports payouts.
func (s *Service) PayoutAdapter(code string) (PayoutProvider, bool) {
	a, ok := s.Adapter(code)
	if !ok {
		return nil, false
	}
	p, ok := a.(PayoutProvider)
	return p, ok
}

// PayoutCapability is a current capability row that declares payouts.
type PayoutCapability struct {
	ID             string
	ProviderID     string
	Version        int
	Currency       string
	Rails          []string
	Routable       bool
	EvidenceStatus string
	StatusAPI      bool
	SignedWebhooks bool
	SupportsCancel bool
}

// PayoutRoute is the provider, account and capability a payout is bound to.
type PayoutRoute struct {
	Provider   Provider
	Account    Account
	Capability PayoutCapability
}

// ErrNoPayoutRoute means no provider may execute a payout for (rail, currency) in this process.
var ErrNoPayoutRoute = errors.New("psp: no payout route")

// PayoutCapabilities lists current capability rows that declare payouts (for the registry view and routing).
func (s *Service) PayoutCapabilities(ctx context.Context) ([]PayoutCapability, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, provider_id, capability_version, currency, payout_rails, routable, evidence_status, status_api,
		signed_webhooks, supports_cancel FROM app.provider_capabilities_current WHERE payout_api ORDER BY provider_id, currency`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (PayoutCapability, error) {
		var c PayoutCapability
		err := r.Scan(&c.ID, &c.ProviderID, &c.Version, &c.Currency, &c.Rails, &c.Routable, &c.EvidenceStatus, &c.StatusAPI, &c.SignedWebhooks,
			&c.SupportsCancel)
		return c, err
	})
}

// RoutePayout selects the provider for a TEST payout (simulated = true is the only mode Stage 11 permits): a
// routable current capability with payouts to the rail in the currency, a sandbox provider enabled for sandbox
// use, an enabled sandbox account, a registered adapter that implements PayoutProvider, and an authoritative status
// path (status API or signed webhooks). Real providers are never routed: there is no payout provider selection
// (ADR-040) and no live execution (ADR-043).
func (s *Service) RoutePayout(ctx context.Context, rail, currency string, simulated bool) (PayoutRoute, error) {
	if !simulated || !s.Cfg.SandboxEnabled {
		return PayoutRoute{}, ErrNoPayoutRoute
	}
	caps, err := s.PayoutCapabilities(ctx)
	if err != nil {
		return PayoutRoute{}, err
	}
	for _, c := range caps {
		if c.Currency != currency || !c.Routable || !slices.Contains(c.Rails, rail) || !(c.StatusAPI || c.SignedWebhooks) {
			continue
		}
		p, err := s.ProviderByID(ctx, c.ProviderID)
		if err != nil {
			return PayoutRoute{}, err
		}
		if !p.IsSandbox || p.SandboxStatus != "ENABLED" {
			continue
		}
		if _, ok := s.PayoutAdapter(p.Code); !ok {
			continue
		}
		acct, err := s.ActiveAccount(ctx, p)
		if errors.Is(err, ErrNoAccount) {
			continue
		}
		if err != nil {
			return PayoutRoute{}, err
		}
		if acct.Environment != EnvSandbox || !CredentialScopeOK(acct) {
			continue
		}
		return PayoutRoute{Provider: p, Account: acct, Capability: c}, nil
	}
	return PayoutRoute{}, ErrNoPayoutRoute
}
