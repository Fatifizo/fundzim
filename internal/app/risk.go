package app

import (
	"context"
	"log/slog"

	"github.com/riverqueue/river"

	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/idempotency"
	"github.com/Fatifizo/fundzim/internal/platform/ratelimit"
	"github.com/Fatifizo/fundzim/internal/risk"
)

// newRisk completes the risk module with its Stage 13 dependencies (ADR-045) and hands the hold registry to the
// enforcement points (payments: intents and attempts; campaigns: publication; payouts reads it directly).
func newRisk(m *VerificationModules, d VerificationDeps) {
	r := m.Risk
	r.Identities = m.KYC
	if d.Auth != nil {
		r.Staff = d.Auth
	}
	if d.Config.Risk.ScreeningSandboxEnabled {
		key := m.appKeyed.Sum("risk.screening.sandbox.callback.v1")
		sb := &risk.SandboxScreening{Pool: d.AppPool, Clock: d.Clock, Key: key}
		sb.Deliver = func(ctx context.Context, header string, body []byte) error {
			return r.HandleScreeningCallback(ctx, header, body)
		}
		r.Screening, r.ScreeningKey = sb, key
		m.RiskSandbox = sb
		d.Logger.Warn("risk: TEST screening SIMULATOR enabled (synthetic results only; no screening provider is selected)")
	}
	if m.Payments != nil {
		m.Payments.Holds = r
	}
	if m.Campaigns != nil {
		m.Campaigns.Holds = r
	}
}

// registerRiskRoutes registers the risk operations API and the screening callback endpoint.
func registerRiskRoutes(r *httpx.Router, m *VerificationModules, idem *idempotency.Store, limiter ratelimit.Limiter, logger *slog.Logger) {
	h := &risk.HTTP{S: m.Risk, Sandbox: m.RiskSandbox}
	for _, rt := range h.Routes() {
		var opts []httpx.RouteOption
		if rt.Idempotent {
			opts = append(opts, httpx.With(idempotency.Middleware(idem, idempotency.Required, rt.Scope, logger)))
		}
		r.HandleFunc(rt.Pattern, rt.Policy, rt.Handler, opts...)
	}
	var webhookOpts []httpx.RouteOption
	if limiter != nil {
		webhookOpts = append(webhookOpts, httpx.With(ratelimit.Middleware(limiter, ratelimit.DefaultPolicies().ScreeningWebhookIP,
			ratelimit.ByClientIP, logger)))
	}
	r.HandleFunc("POST /api/v1/webhooks/screening/{provider}", httpx.Public(), h.HandleScreeningWebhook, webhookOpts...)
}

// riskJobs registers the risk workers and periodic jobs (worker).
func riskJobs(workers *river.Workers, m *VerificationModules) []*river.PeriodicJob {
	return risk.Register(workers, m.Risk, m.RiskSandbox)
}
