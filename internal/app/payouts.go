package app

import (
	"log/slog"

	"github.com/riverqueue/river"

	"github.com/Fatifizo/fundzim/internal/payouts"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/idempotency"
	"github.com/Fatifizo/fundzim/internal/platform/ratelimit"
)

// newPayouts completes the payouts module with its Stage 11 dependencies (ADR-043). The destinations part exists
// since Stage 5; withdrawal requests need the campaigns, ledger, provider registry, risk limits and the payments
// refund exposure (read through an interface: payouts never imports payments).
func newPayouts(m *VerificationModules, d VerificationDeps) {
	p := m.Payouts
	p.Campaigns, p.Ledger, p.PSP, p.Risk = m.Campaigns, m.Ledger, m.PSP, m.Risk
	p.Refunds = m.Payments
	p.ExecEnv = d.Config.Payouts.ExecutionEnvironment
	p.StepUpMaxAge = d.Config.Auth.StepUpMaxAge
	p.SubmitRetryBudget, p.SubmitRetryBase, p.SubmitRetryMax = d.Config.Payouts.SubmitRetryBudget, d.Config.Payouts.SubmitRetryBase,
		d.Config.Payouts.SubmitRetryMax
	m.PSP.Handle("PAYOUT", p.HandleInbox(m.PSP))
	if p.ExecEnv == payouts.ExecLocalSimulator {
		d.Logger.Warn("payouts: TEST payouts through the sandbox SIMULATOR enabled (development/test only; no real payouts)")
	}
}

// registerPayoutRoutes registers the withdrawal and payout API.
func registerPayoutRoutes(r *httpx.Router, m *VerificationModules, idem *idempotency.Store, limiter ratelimit.Limiter, logger *slog.Logger) {
	h := &payouts.HTTP{S: m.Payouts, Limiter: limiter, Sandbox: m.Sandbox}
	for _, rt := range h.Routes() {
		var opts []httpx.RouteOption
		if rt.Idempotent {
			opts = append(opts, httpx.With(idempotency.Middleware(idem, idempotency.Required, rt.Scope, logger)))
		}
		r.HandleFunc(rt.Pattern, rt.Policy, rt.Handler, opts...)
	}
}

// payoutJobs registers the payout workers and periodic jobs (worker).
func payoutJobs(workers *river.Workers, m *VerificationModules) []*river.PeriodicJob {
	return payouts.Register(workers, m.Payouts)
}
