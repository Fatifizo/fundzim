package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"github.com/Fatifizo/fundzim/internal/ledger"
	"github.com/Fatifizo/fundzim/internal/payments"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/idempotency"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// Stage 10 ledger wiring (ADR-042). The ledger connects as fundzim_ledger — the only writer of the ledger schema —
// and reads payment facts through paymentFacts (it never reads payment tables). Payments never post: the consumer
// records each payments.payment_succeeded as a NOT_ELIGIBLE/BLOCKED source reference.

// paymentFacts adapts payments.Service to ledger.PaymentFacts.
type paymentFacts struct{ p *payments.Service }

func toFact(f payments.Fact) ledger.PaymentFact {
	return ledger.PaymentFact{ID: f.ID, Status: f.Status, AmountMinor: f.AmountMinor, Currency: f.Currency,
		ExecutionEnvironment: f.ExecutionEnvironment, ProviderReference: f.ProviderReference, UpdatedAt: f.UpdatedAt}
}

func (a paymentFacts) Payment(ctx context.Context, id string) (ledger.PaymentFact, bool, error) {
	f, ok, err := a.p.PaymentFact(ctx, id)
	return toFact(f), ok, err
}

func (a paymentFacts) PaymentsInStatus(ctx context.Context, status string, before time.Time, limit int) ([]ledger.PaymentFact, error) {
	fs, err := a.p.PaymentFactsInStatus(ctx, status, before, limit)
	out := make([]ledger.PaymentFact, 0, len(fs))
	for _, f := range fs {
		out = append(out, toFact(f))
	}
	return out, err
}

func (a paymentFacts) PaymentIDsAfter(ctx context.Context, after string, statuses []string, limit int) ([]string, error) {
	return a.p.PaymentFactIDsAfter(ctx, after, statuses, limit)
}

func newLedger(ctx context.Context, m *VerificationModules, d VerificationDeps) error {
	cfg := d.Config
	pool, err := db.NewPool(ctx, db.Options{URL: cfg.Database.LedgerURL.Reveal(), MaxConns: 8, ConnectTimeout: cfg.Database.ConnectTimeout,
		AppName: "fundzim-ledger"})
	if err != nil {
		return fmt.Errorf("ledger pool: %w", err)
	}
	m.LedgerPool = pool
	m.Ledger = ledger.New(pool, d.Clock, d.Logger)
	m.Ledger.SyntheticEnabled = cfg.Ledger.SyntheticFixturesEnabled
	if m.Payments != nil {
		m.Ledger.Facts = paymentFacts{m.Payments}
	}
	if err := m.Ledger.Warm(ctx); err != nil {
		d.Logger.Warn("ledger: warm-up deferred (database not reachable yet?)", "err", err) // loaded lazily per transaction
	}
	return nil
}

// registerLedgerRoutes registers the staff ledger API.
func registerLedgerRoutes(r *httpx.Router, m *VerificationModules, idem *idempotency.Store, logger *slog.Logger) {
	for _, rt := range m.Ledger.Routes() {
		var opts []httpx.RouteOption
		if rt.Idempotent {
			opts = append(opts, httpx.With(idempotency.Middleware(idem, idempotency.Required, rt.Scope, logger)))
		}
		r.HandleFunc(rt.Pattern, rt.Policy, rt.Handler, opts...)
	}
}

// registerLedgerConsumers subscribes the payments → ledger evaluation (worker).
func registerLedgerConsumers(reg *outbox.Registry, m *VerificationModules) {
	reg.Subscribe(ledger.ConsumerPayments, "payments.payment_succeeded", func(ctx context.Context, d outbox.Delivery) error {
		return m.Ledger.RecordPaymentSucceeded(ctx, d.EventID, d.Payload)
	})
}

// ledgerJobs registers the ledger workers and periodic jobs (worker).
func ledgerJobs(workers *river.Workers, m *VerificationModules) []*river.PeriodicJob {
	return ledger.Register(workers, m.Ledger)
}
