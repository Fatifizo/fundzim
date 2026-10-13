package app

import (
	"context"
	"crypto/md5"
	"fmt"
	"log/slog"
	"os"

	"github.com/riverqueue/river"

	"github.com/Fatifizo/fundzim/internal/compliance"
	"github.com/Fatifizo/fundzim/internal/payments"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/idempotency"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
	"github.com/Fatifizo/fundzim/internal/platform/ratelimit"
	"github.com/Fatifizo/fundzim/internal/psp"
	"github.com/Fatifizo/fundzim/internal/psp/sandbox"
)

// Stage 8 payment layer wiring (ADR-039). psp owns providers, webhooks and the sandbox simulator; payments owns
// donation intents and payments. Real execution is impossible here: psp.LiveExecutionEnabled is a constant false,
// and the sandbox adapter is registered only when PAYMENTS_SANDBOX_ENABLED is set (refused in production).

// seededID reproduces the migrations' md5('<seed>')::uuid ids.
func seededID(seed string) string {
	h := md5.Sum([]byte(seed))
	return fmt.Sprintf("%x-%x-%x-%x-%x", h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

// SandboxProviderID is the seeded id of the sandbox simulator provider.
var SandboxProviderID = seededID("payment_provider:sandbox")

type donorRestrictions struct{ c *compliance.Service }

// Restricted: any compliance restriction on the donor's account blocks new donations (ADR-039 §7, fail closed).
func (d donorRestrictions) Restricted(ctx context.Context, userID string) (bool, error) {
	sub := compliance.Subject{Type: "USER", ID: userID}
	got, err := d.c.Restrictions(ctx, []compliance.Subject{sub})
	if err != nil {
		return true, err
	}
	return got[sub] != compliance.LevelNone, nil
}

func newPayments(m *VerificationModules, d VerificationDeps) {
	cfg := d.Config.Payments
	secrets := psp.EnvResolver{Lookup: os.LookupEnv}
	m.PSP = psp.New(d.AppPool, d.Clock, d.Logger, secrets, psp.Config{SandboxEnabled: cfg.SandboxEnabled, AccountEnv: cfg.ProviderAccountEnv},
		psp.NewMetrics(d.Registry))
	if cfg.SandboxEnabled {
		m.Sandbox = sandbox.New(d.AppPool, d.Clock, d.Logger, secrets, SandboxProviderID, cfg.SandboxWebhookURL)
		m.PSP.Register(m.Sandbox)
		d.Logger.Warn("payments: sandbox SIMULATOR enabled (development/test only; no real payments)")
	}
	m.Payments = payments.New(payments.Deps{Pool: d.AppPool, Clock: d.Clock, Logger: d.Logger, PSP: m.PSP, Campaigns: m.Campaigns,
		Restrictions: donorRestrictions{m.Compliance}, Limits: m.Risk, Limiter: d.Limiter, Keyed: m.appKeyed, Registerer: d.Registry,
		PublicSiteURL: cfg.PublicSiteURL})
	m.PSP.Handle("PAYMENT", m.Payments.ApplyWebhook)
}

// registerPaymentRoutes registers the payments API and the webhook endpoint.
func registerPaymentRoutes(r *httpx.Router, m *VerificationModules, idem *idempotency.Store, limiter ratelimit.Limiter, logger *slog.Logger) {
	for _, rt := range m.Payments.Routes(m.Sandbox) {
		var opts []httpx.RouteOption
		if rt.Idempotent {
			opts = append(opts, httpx.With(idempotency.Middleware(idem, idempotency.Required, rt.Scope, logger)))
		}
		r.HandleFunc(rt.Pattern, rt.Policy, rt.Handler, opts...)
	}
	var webhookOpts []httpx.RouteOption
	if limiter != nil {
		webhookOpts = append(webhookOpts, httpx.With(ratelimit.Middleware(limiter, ratelimit.DefaultPolicies().PaymentsWebhookIP,
			ratelimit.ByClientIP, logger)))
	}
	r.HandleFunc("POST /api/v1/webhooks/payments/{provider}", httpx.Public(), m.PSP.HandleWebhook, webhookOpts...)
}

// registerPaymentConsumers subscribes webhook dispatch (worker).
func registerPaymentConsumers(reg *outbox.Registry, m *VerificationModules) {
	reg.Subscribe(psp.ConsumerDispatch, psp.EvWebhookReceived, m.PSP.Dispatch)
}

// paymentJobs registers the payments sweep (and the simulator's webhook delivery when it runs) and returns
// their periodic jobs (worker).
func paymentJobs(workers *river.Workers, m *VerificationModules) []*river.PeriodicJob {
	river.AddWorker(workers, &payments.SweepWorker{Service: m.Payments})
	periodic := payments.PeriodicJobs()
	if m.Sandbox != nil {
		river.AddWorker(workers, &sandbox.DeliverWorker{Adapter: m.Sandbox})
		periodic = append(periodic, sandbox.PeriodicJobs()...)
	}
	return periodic
}
