package app

import (
	"context"
	"errors"
	"log/slog"

	"github.com/riverqueue/river"

	"github.com/Fatifizo/fundzim/internal/campaigns"
	"github.com/Fatifizo/fundzim/internal/fees"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/idempotency"
	"github.com/Fatifizo/fundzim/internal/platform/money"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
	"github.com/Fatifizo/fundzim/internal/platform/ratelimit"
	"github.com/Fatifizo/fundzim/internal/psp"
)

// Stage 12 fee wiring (ADR-044). fees imports platform, audit and ledger only: campaign facts, provider routing and
// linked identities reach it through the adapters below. Payments bind a fee quote to every donation intent and
// payouts capture a fee estimate at request time; both call the fee service.

// feeCampaigns adapts campaigns.Service to fees.Campaigns.
type feeCampaigns struct{ c *campaigns.Service }

func (a feeCampaigns) FeeScope(ctx context.Context, campaignID string) (fees.CampaignScope, error) {
	acc, err := a.c.Load(ctx, campaignID)
	if errors.Is(err, campaigns.ErrNotFound) {
		return fees.CampaignScope{}, fees.ErrCampaignNotFound
	}
	if err != nil {
		return fees.CampaignScope{}, err
	}
	sc := fees.CampaignScope{ID: acc.CampaignID, CategoryCode: acc.Category}
	if acc.OwnerUserID != nil {
		sc.OwnerUserID = *acc.OwnerUserID
	}
	if acc.OwnerOrgID != nil {
		sc.OwnerOrganisationID = *acc.OwnerOrgID
	}
	// the same rule donation intents use (campaigns.EvaluateDonation)
	if _, r, err := a.c.EvaluateDonation(ctx, campaignID); err != nil && !errors.Is(err, campaigns.ErrNotFound) {
		return fees.CampaignScope{}, err
	} else if err == nil {
		sc.AcceptsDonations = r.Allowed
	}
	return sc, nil
}

func (a feeCampaigns) CanManage(ctx context.Context, userID, campaignID string) (bool, error) {
	_, err := a.c.OwnerAccess(ctx, userID, campaignID, false)
	if errors.Is(err, campaigns.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// feeRouting adapts psp.Service to fees.Routing (simulated: this process's donation intents are simulations).
type feeRouting struct {
	p         *psp.Service
	simulated func() bool
}

func (a feeRouting) AvailableMethods(ctx context.Context, currency string) ([]string, error) {
	opts, err := a.p.AvailableMethods(ctx, currency, a.simulated())
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(opts))
	for _, o := range opts {
		out = append(out, o.Method)
	}
	return out, nil
}

func (a feeRouting) RouteMethod(ctx context.Context, method, currency string, amountMinor int64) (string, error) {
	r, err := a.p.Route(ctx, psp.RouteQuery{Method: method, Currency: currency, AmountMinor: amountMinor, Simulated: a.simulated()})
	if err != nil {
		return "", err
	}
	return r.Provider.ID, nil
}

func (a feeRouting) ProviderIsSandbox(ctx context.Context, providerID string) (bool, error) {
	ps, err := a.p.Providers(ctx)
	if err != nil {
		return false, err
	}
	for _, p := range ps {
		if p.ID == providerID {
			return p.IsSandbox, nil
		}
	}
	return false, fees.ErrNotFound
}

// newFees builds the fee module and hands it to payments and payouts.
func newFees(m *VerificationModules, d VerificationDeps) {
	pool := d.AppPool
	s := &fees.Service{Pool: pool, Clock: d.Clock, Logger: d.Logger, Ledger: m.Ledger, Campaigns: feeCampaigns{m.Campaigns},
		Identities: m.KYC, QuoteTTL: d.Config.Fees.QuoteTTL,
		UsableCurrencies: func(ctx context.Context) ([]string, error) { return money.UsableCurrencies(ctx, pool) }}
	if m.PSP != nil && m.Payments != nil {
		s.Routing = feeRouting{p: m.PSP, simulated: m.Payments.Simulated}
	}
	m.Fees = s
	if m.Payments != nil {
		m.Payments.Fees = s
	}
	if m.Payouts != nil {
		m.Payouts.Fees = s
	}
}

// registerFeeRoutes registers the fee API.
func registerFeeRoutes(r *httpx.Router, m *VerificationModules, idem *idempotency.Store, limiter ratelimit.Limiter, logger *slog.Logger) {
	h := &fees.HTTP{S: m.Fees, Limiter: limiter}
	for _, rt := range h.Routes() {
		var opts []httpx.RouteOption
		if rt.Idempotent {
			opts = append(opts, httpx.With(idempotency.Middleware(idem, idempotency.Required, rt.Scope, logger)))
		}
		r.HandleFunc(rt.Pattern, rt.Policy, rt.Handler, opts...)
	}
}

// registerFeeConsumers subscribes the payments → fees assessment (worker).
func registerFeeConsumers(reg *outbox.Registry, m *VerificationModules) {
	reg.Subscribe(fees.ConsumerPayments, "payments.payment_succeeded", func(ctx context.Context, d outbox.Delivery) error {
		return m.Fees.RecordPaymentSucceeded(ctx, d.Payload)
	})
}

// feeJobs registers the fee workers and periodic jobs (worker).
func feeJobs(workers *river.Workers, m *VerificationModules) []*river.PeriodicJob {
	return fees.Register(workers, m.Fees)
}
