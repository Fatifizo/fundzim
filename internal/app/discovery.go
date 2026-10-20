package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Fatifizo/fundzim/internal/discovery"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/config"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/idempotency"
	"github.com/Fatifizo/fundzim/internal/platform/jobs"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
	"github.com/Fatifizo/fundzim/internal/platform/ratelimit"
)

// Stage 16 discovery wiring (ADR-048). Discovery reads campaigns through the campaigns service only, linked identities
// through kyc, and the referral kill switch through feature flags. The API process owns the engagement recorder
// (in-memory buffer, batched writes); the worker runs the indexer consumer and the periodic jobs on the discovery queue.

// NewDiscovery builds the discovery service. withRecorder starts the engagement buffer (API only).
func NewDiscovery(cfg config.Config, pool *pgxpool.Pool, m *VerificationModules, limiter ratelimit.Limiter, logger *slog.Logger, withRecorder bool) *discovery.Service {
	if m == nil || m.Campaigns == nil {
		return nil
	}
	s := &discovery.Service{Pool: pool, Campaigns: m.Campaigns, Flags: campaignFlags{pool}, Limiter: limiter, Keyed: m.appKeyed,
		Clock: clock.System, Logger: logger, Production: cfg.App.Env == config.EnvProduction}
	if m.KYC != nil {
		s.Identities = m.KYC
	}
	if withRecorder && m.appKeyed != nil {
		s.Recorder = discovery.NewRecorder(pool, m.Campaigns, m.appKeyed, s.Clock, logger)
		s.Recorder.Start()
	}
	return s
}

// registerDiscoveryRoutes registers the discovery API (public reads rate limited per client address; staff creations
// require an Idempotency-Key).
func registerDiscoveryRoutes(r *httpx.Router, s *discovery.Service, idem *idempotency.Store, limiter ratelimit.Limiter, logger *slog.Logger) {
	h := &discovery.HTTP{S: s}
	pol := ratelimit.DefaultPolicies()
	for _, rt := range h.Routes() {
		var opts []httpx.RouteOption
		if rt.Idempotent {
			opts = append(opts, httpx.With(idempotency.Middleware(idem, idempotency.Required, rt.Scope, logger)))
		}
		if limiter != nil && rt.RateLimited {
			opts = append(opts, httpx.With(ratelimit.Middleware(limiter, pol.DiscoverySearchIP, ratelimit.ByClientIP, logger)))
		}
		r.HandleFunc(rt.Pattern, rt.Policy, rt.Handler, opts...)
	}
}

// registerDiscoveryConsumers subscribes the indexer (worker). Its deliveries run on the discovery queue
// (outbox.Relay.ConsumerQueues in buildRiver).
func registerDiscoveryConsumers(reg *outbox.Registry, s *discovery.Service) {
	if s != nil {
		s.Register(reg)
	}
}

// ---- worker jobs ----------------------------------------------------------------------------------------------------

const (
	kindDiscoveryReconcile = "discovery.reconcile"
	kindDiscoveryTrending  = "discovery.trending"
	kindDiscoveryMaintain  = "discovery.maintenance"
)

type discoveryReconcileArgs struct{}

func (discoveryReconcileArgs) Kind() string { return kindDiscoveryReconcile }

type discoveryReconcileWorker struct {
	river.WorkerDefaults[discoveryReconcileArgs]
	S *discovery.Service
}

func (w *discoveryReconcileWorker) Timeout(*river.Job[discoveryReconcileArgs]) time.Duration {
	return 9 * time.Minute
}

func (w *discoveryReconcileWorker) Work(ctx context.Context, _ *river.Job[discoveryReconcileArgs]) error {
	res, err := w.S.Reconcile(ctx, 0)
	if res.Reindexed > 0 || res.Failed > 0 {
		w.S.Logger.InfoContext(ctx, "discovery index reconciled", "checked", res.Checked, "reindexed", res.Reindexed, "failed", res.Failed)
	}
	return err
}

type discoveryTrendingArgs struct{}

func (discoveryTrendingArgs) Kind() string { return kindDiscoveryTrending }

type discoveryTrendingWorker struct {
	river.WorkerDefaults[discoveryTrendingArgs]
	S *discovery.Service
}

func (w *discoveryTrendingWorker) Timeout(*river.Job[discoveryTrendingArgs]) time.Duration {
	return 5 * time.Minute
}

func (w *discoveryTrendingWorker) Work(ctx context.Context, _ *river.Job[discoveryTrendingArgs]) error {
	_, err := w.S.ComputeTrending(ctx)
	return err
}

type discoveryMaintainArgs struct{}

func (discoveryMaintainArgs) Kind() string { return kindDiscoveryMaintain }

type discoveryMaintainWorker struct {
	river.WorkerDefaults[discoveryMaintainArgs]
	S *discovery.Service
}

// Work ends placements whose window has passed and purges expired dedupe keys.
func (w *discoveryMaintainWorker) Work(ctx context.Context, _ *river.Job[discoveryMaintainArgs]) error {
	if _, err := w.S.EndPlacements(ctx); err != nil {
		return err
	}
	_, err := discovery.PurgeEngagementKeys(ctx, w.S.Pool, w.S.Clock.Now())
	return err
}

// discoveryJobs registers the discovery workers and their periodic schedule (all on the discovery queue).
func discoveryJobs(workers *river.Workers, s *discovery.Service) []*river.PeriodicJob {
	if s == nil {
		return nil
	}
	river.AddWorker(workers, &discoveryReconcileWorker{S: s})
	river.AddWorker(workers, &discoveryTrendingWorker{S: s})
	river.AddWorker(workers, &discoveryMaintainWorker{S: s})
	every := func(d time.Duration, id string, args river.JobArgs) *river.PeriodicJob {
		return river.NewPeriodicJob(river.PeriodicInterval(d), func() (river.JobArgs, *river.InsertOpts) {
			return args, &river.InsertOpts{Queue: jobs.QueueDiscovery, UniqueOpts: river.UniqueOpts{ByPeriod: d}}
		}, &river.PeriodicJobOpts{ID: id, RunOnStart: true})
	}
	return []*river.PeriodicJob{
		every(10*time.Minute, kindDiscoveryReconcile, discoveryReconcileArgs{}),
		every(15*time.Minute, kindDiscoveryTrending, discoveryTrendingArgs{}),
		every(5*time.Minute, kindDiscoveryMaintain, discoveryMaintainArgs{}),
	}
}
