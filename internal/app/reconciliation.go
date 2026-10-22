package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Fatifizo/fundzim/internal/auth"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/config"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/idempotency"
	"github.com/Fatifizo/fundzim/internal/platform/jobs"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
	"github.com/Fatifizo/fundzim/internal/reconciliation"
	"github.com/Fatifizo/fundzim/internal/storage"
)

// Stage 17 reconciliation wiring (ADR-049). Reconciliation reads payments, payouts, fees and the ledger through their
// services, stores statement files through storage, resolves linked identities through kyc and investigators through
// auth. The worker parses statements when their scan finishes (outbox consumer on the reconciliation queue), matches,
// and runs the ageing housekeeping; everything is TEST (no provider is selected).

// NewReconciliation builds the reconciliation service (nil when a dependency is missing, e.g. storage disabled).
func NewReconciliation(cfg config.Config, pool *pgxpool.Pool, m *VerificationModules, a *auth.Service, logger *slog.Logger) *reconciliation.Service {
	if m == nil || m.Payments == nil || m.Payouts == nil || m.Fees == nil || m.Ledger == nil || m.Storage == nil {
		return nil
	}
	s := &reconciliation.Service{Pool: pool, Payments: m.Payments, Payouts: m.Payouts, Fees: m.Fees, Ledger: m.Ledger, Storage: m.Storage,
		LedgerReads: m.Ledger, FeeReads: m.Fees, PayoutReads: m.Payouts, Keyed: m.appKeyed, Clock: clock.System, Logger: logger,
		Production: cfg.App.Env == config.EnvProduction, MaxFileBytes: cfg.Verification.UploadMaxBytes}
	if m.KYC != nil {
		s.Identities = m.KYC
	}
	if a != nil {
		s.Directory = a
	}
	if m.Sandbox != nil && cfg.App.Env != config.EnvProduction {
		s.Sandbox = m.Sandbox
	}
	return s
}

// reconciliationExemptions lets the statement upload exceed the global JSON body limit.
func reconciliationExemptions(cfg config.Config) []httpx.BodyLimitExemption {
	return []httpx.BodyLimitExemption{{Pattern: "POST /api/v1/admin/reconciliation/imports", Max: cfg.Verification.UploadMaxBytes + 64<<10}}
}

// registerReconciliationRoutes registers the reconciliation API (staff; creations require an Idempotency-Key except the
// upload, which is idempotent by content hash).
func registerReconciliationRoutes(r *httpx.Router, s *reconciliation.Service, idem *idempotency.Store, logger *slog.Logger) {
	h := &reconciliation.HTTP{S: s}
	for _, rt := range h.Routes() {
		var opts []httpx.RouteOption
		if rt.Idempotent {
			opts = append(opts, httpx.With(idempotency.Middleware(idem, idempotency.Required, rt.Scope, logger)))
		}
		r.HandleFunc(rt.Pattern, rt.Policy, rt.Handler, opts...)
	}
}

// registerReconciliationConsumers subscribes the statement parser to storage's scan outcome (worker).
func registerReconciliationConsumers(reg *outbox.Registry, s *reconciliation.Service) {
	if s != nil {
		reg.Subscribe(reconciliation.ConsumerImports, storage.EventObjectScanned, s.HandleObjectScanned)
	}
}

// ---- worker jobs ----------------------------------------------------------------------------------------------------

type reconParseArgs struct{}

func (reconParseArgs) Kind() string { return reconciliation.JobParse }

type reconParseWorker struct {
	river.WorkerDefaults[reconParseArgs]
	S *reconciliation.Service
}

func (w *reconParseWorker) Timeout(*river.Job[reconParseArgs]) time.Duration { return 5 * time.Minute }

// Work parses statements whose scan finished but whose event was missed, and imports a crash left in PARSING.
func (w *reconParseWorker) Work(ctx context.Context, _ *river.Job[reconParseArgs]) error {
	start := w.S.Clock.Now()
	_, err := w.S.ParsePending(ctx, 20)
	w.S.RecordJob(ctx, reconciliation.JobParse, start, err)
	return err
}

type reconMatchArgs struct{}

func (reconMatchArgs) Kind() string { return reconciliation.JobMatch }

type reconMatchWorker struct {
	river.WorkerDefaults[reconMatchArgs]
	S *reconciliation.Service
}

func (w *reconMatchWorker) Timeout(*river.Job[reconMatchArgs]) time.Duration { return 9 * time.Minute }

// Work reaps lost runs, schedules runs for scopes with open lines or new statements, and executes queued runs.
func (w *reconMatchWorker) Work(ctx context.Context, _ *river.Job[reconMatchArgs]) error {
	start := w.S.Clock.Now()
	_, err := w.S.ReapStaleRuns(ctx)
	if err == nil {
		_, err = w.S.ScheduleRuns(ctx)
	}
	if err == nil {
		_, err = w.S.ExecuteQueued(ctx, 5)
	}
	w.S.RecordJob(ctx, reconciliation.JobMatch, start, err)
	return err
}

type reconAgeingArgs struct{}

func (reconAgeingArgs) Kind() string { return reconciliation.JobAgeing }

type reconAgeingWorker struct {
	river.WorkerDefaults[reconAgeingArgs]
	S *reconciliation.Service
}

func (w *reconAgeingWorker) Timeout(*river.Job[reconAgeingArgs]) time.Duration {
	return 5 * time.Minute
}

// Work expires proposals, retries approved executions, flags stale sources and escalates old exceptions.
func (w *reconAgeingWorker) Work(ctx context.Context, _ *river.Job[reconAgeingArgs]) error {
	start := w.S.Clock.Now()
	_, err := w.S.Ageing(ctx)
	w.S.RecordJob(ctx, reconciliation.JobAgeing, start, err)
	return err
}

// reconciliationJobs registers the workers and their periodic schedule (all on the reconciliation queue).
func reconciliationJobs(workers *river.Workers, s *reconciliation.Service) []*river.PeriodicJob {
	if s == nil {
		return nil
	}
	river.AddWorker(workers, &reconParseWorker{S: s})
	river.AddWorker(workers, &reconMatchWorker{S: s})
	river.AddWorker(workers, &reconAgeingWorker{S: s})
	every := func(d time.Duration, id string, args river.JobArgs) *river.PeriodicJob {
		return river.NewPeriodicJob(river.PeriodicInterval(d), func() (river.JobArgs, *river.InsertOpts) {
			return args, &river.InsertOpts{Queue: jobs.QueueReconciliation, UniqueOpts: river.UniqueOpts{ByPeriod: d}}
		}, &river.PeriodicJobOpts{ID: id, RunOnStart: true})
	}
	return []*river.PeriodicJob{
		every(time.Minute, reconciliation.JobParse, reconParseArgs{}),
		every(time.Minute, reconciliation.JobMatch, reconMatchArgs{}),
		every(10*time.Minute, reconciliation.JobAgeing, reconAgeingArgs{}),
	}
}
