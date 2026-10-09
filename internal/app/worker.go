package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Fatifizo/fundzim/internal/notifications"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/config"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/health"
	"github.com/Fatifizo/fundzim/internal/platform/jobs"
	"github.com/Fatifizo/fundzim/internal/platform/metrics"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
	"github.com/Fatifizo/fundzim/internal/platform/version"
	"github.com/Fatifizo/fundzim/internal/storage"
	"github.com/Fatifizo/fundzim/migrations"
)

// WorkerDeps are the long-lived dependencies of the worker process (apps/api/cmd/worker). Consumers
// registered in consumers.go build their services from these.
type WorkerDeps struct {
	Config        config.Config
	Worker        WorkerConfig
	Logger        *slog.Logger
	DB            *pgxpool.Pool // role fundzim_worker (DATABASE_WORKER_URL)
	Metrics       *metrics.Metrics
	JobMetrics    *jobs.Metrics
	OutboxMetrics *outbox.Metrics
	Email         notifications.EmailSender
	SMS           notifications.SMSSender
	Clock         clock.Clock
	Version       version.Info
	// ExpectedSchemaVersion is the migration version this binary requires (defaults to the embedded one).
	ExpectedSchemaVersion int64

	Registry *outbox.Registry
	River    *river.Client[pgx.Tx]
	// Stage 5: verification modules (consumers, scans, sweeps)
	Verification *VerificationModules

	riverRunning atomic.Bool
}

// NewWorkerDeps connects the worker's dependencies and builds the River client with every job kind,
// queue and periodic job. The pool is lazy: a database that is down makes the worker not-ready.
func NewWorkerDeps(ctx context.Context, cfg config.Config, wcfg WorkerConfig, logger *slog.Logger) (*WorkerDeps, error) {
	info := version.Get(cfg.App.Name)
	if cfg.App.Version != "" {
		info.Version = cfg.App.Version
	}
	m := metrics.New(info.Version, info.Commit)
	d := &WorkerDeps{Config: cfg, Worker: wcfg, Logger: logger, Metrics: m, Version: info, Clock: clock.System,
		JobMetrics: jobs.NewMetrics(m.Registry), OutboxMetrics: outbox.NewMetrics(m.Registry),
		ExpectedSchemaVersion: migrations.ExpectedVersion(), Registry: outbox.NewRegistry()}

	email, err := notifications.NewEmailSender(cfg.Email)
	if err != nil {
		return nil, err
	}
	sms, err := notifications.NewSMSSender(cfg.SMS, email)
	if err != nil {
		return nil, err
	}
	d.Email, d.SMS = email, sms

	pool, err := db.NewPool(ctx, db.Options{URL: wcfg.DatabaseURL.Reveal(), MaxConns: wcfg.MaxConns,
		ConnectTimeout: cfg.Database.ConnectTimeout, AppName: "fundzim-worker"})
	if err != nil {
		return nil, err
	}
	d.DB = pool

	if err := registerConsumers(ctx, d, d.Registry); err != nil {
		d.Close()
		return nil, err
	}
	if err := d.buildRiver(); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

func (d *WorkerDeps) buildRiver() error {
	workers := river.NewWorkers()
	relay := &outbox.Relay{Pool: d.DB, Registry: d.Registry, Metrics: d.OutboxMetrics, Logger: d.Logger}
	river.AddWorker(workers, &outbox.RelayWorker{Relay: relay})
	// a delivery may run a malware scan: allow the scan timeout plus margin (default 30 s otherwise)
	river.AddWorker(workers, &outbox.DeliverWorker{Pool: d.DB, Registry: d.Registry, Logger: d.Logger,
		JobLimit: d.Config.Verification.ScanTimeout + 30*time.Second})
	river.AddWorker(workers, &outbox.PurgeWorker{Pool: d.DB, Logger: d.Logger, Metrics: d.OutboxMetrics,
		Retention: d.Worker.OutboxRetention})
	river.AddWorker(workers, &jobs.PurgeIdempotencyKeysWorker{Pool: d.DB, Logger: d.Logger})

	periodic := append([]*river.PeriodicJob{outbox.PeriodicRelay(time.Second)}, outbox.PeriodicMaintenance(10*time.Minute)...)
	if d.Verification != nil {
		d.Verification.Storage.AddWorkers(workers)
		river.AddWorker(workers, &VerificationSweepWorker{M: d.Verification, Logger: d.Logger})
		periodic = append(periodic, storage.PeriodicJobs()...)
		periodic = append(periodic, verificationPeriodic()...)
	}
	client, err := jobs.NewWorkerClient(jobs.WorkerConfig{
		Pool: d.DB, Logger: d.Logger, Metrics: d.JobMetrics, Workers: workers,
		Queues: jobs.DefaultQueues(d.Worker.Concurrency), PeriodicJobs: periodic,
	})
	if err != nil {
		return err
	}
	d.River = client
	return nil
}

// Close releases dependency resources.
func (d *WorkerDeps) Close() {
	d.Verification.Close()
	if d.DB != nil {
		d.DB.Close()
	}
}

// Checker builds the worker readiness checks: database, schema version and River running.
func (d *WorkerDeps) Checker() *health.Checker {
	return health.NewChecker(
		health.Check{Name: "database", Critical: true, Timeout: 2 * time.Second,
			Run: func(ctx context.Context) error { return db.Ping(ctx, d.DB) }},
		health.Check{Name: "migrations", Critical: true, Timeout: 2 * time.Second,
			Run: func(ctx context.Context) error { return db.CheckSchemaCurrent(ctx, d.DB, d.ExpectedSchemaVersion) }},
		health.Check{Name: "river", Critical: true, Timeout: time.Second,
			Run: func(context.Context) error {
				if !d.riverRunning.Load() {
					return errors.New("river client is not running")
				}
				return nil
			}},
	).WithCache(time.Second, nil)
}

// WorkerInternalHandler serves /healthz, /readyz (with check details: internal listener only) and /metrics.
func WorkerInternalHandler(d *WorkerDeps, checker *health.Checker) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", d.Metrics.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, req *http.Request) {
		rep := checker.RunCached(req.Context())
		type item struct {
			Name  string `json:"name"`
			OK    bool   `json:"ok"`
			Error string `json:"error,omitempty"`
		}
		out := struct {
			Status string `json:"status"`
			Checks []item `json:"checks"`
		}{Status: "ok"}
		for _, r := range rep.Results {
			d.Metrics.SetDependency(r.Name, r.OK)
			out.Checks = append(out.Checks, item{Name: r.Name, OK: r.OK, Error: errString(r.Err)})
			if !r.OK {
				d.Logger.Warn("readiness check failed", slog.String("check", r.Name),
					slog.String("error_category", "dependency"), slog.String("error", errString(r.Err)))
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if !rep.Ready {
			out.Status = "unavailable"
			w.Header().Set("Retry-After", "5")
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	return mux
}

var errRiverStopped = errors.New("river client stopped unexpectedly")

// WorkerInternalAddr is the worker's internal listen address.
func WorkerInternalAddr(d *WorkerDeps) string {
	return net.JoinHostPort(d.Config.HTTP.InternalHost, strconv.Itoa(d.Worker.InternalHTTPPort))
}

// RunWorker starts River and the internal listener on ln, and blocks until ctx is cancelled. Shutdown:
// River stops fetching and lets running jobs finish within SHUTDOWN_TIMEOUT; jobs still running then are
// cancelled (their contexts end, River records the attempt as failed and retries it later). Finally the
// listener and the pool close.
func RunWorker(ctx context.Context, d *WorkerDeps, ln net.Listener) error {
	checker := d.Checker()
	srv := &http.Server{
		Handler:           WorkerInternalHandler(d, checker),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(d.Logger.Handler(), slog.LevelWarn),
	}
	errc := make(chan error, 2)
	go func() { errc <- serve(srv, ln) }()

	// River gets a context that the shutdown signal does not cancel: we stop it explicitly below so
	// running jobs can drain.
	if err := d.River.Start(context.WithoutCancel(ctx)); err != nil {
		_ = srv.Close()
		d.Close()
		return err
	}
	d.riverRunning.Store(true)
	go func() {
		<-d.River.Stopped()
		d.riverRunning.Store(false)
		errc <- errRiverStopped
	}()
	d.Logger.Info("worker started", slog.String("internal_addr", ln.Addr().String()),
		slog.String("app_version", d.Version.Version), slog.Int("concurrency", d.Worker.Concurrency),
		slog.Any("subscribed_event_types", d.Registry.EventTypes()))

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-errc:
	}
	d.Logger.Info("worker shutting down: draining running jobs", slog.Duration("timeout", d.Config.HTTP.ShutdownTimeout))
	sctx, cancel := context.WithTimeout(context.Background(), d.Config.HTTP.ShutdownTimeout)
	defer cancel()
	stopErr := d.River.Stop(sctx)
	if stopErr != nil {
		d.Logger.Warn("jobs did not finish within the shutdown timeout; cancelling them", slog.String("error", stopErr.Error()))
		hctx, hcancel := context.WithTimeout(context.Background(), 5*time.Second)
		stopErr = d.River.StopAndCancel(hctx)
		hcancel()
	}
	d.riverRunning.Store(false)
	hctx, hcancel := context.WithTimeout(context.Background(), 5*time.Second)
	srvErr := srv.Shutdown(hctx)
	hcancel()
	d.Close()
	d.Logger.Info("worker shutdown complete")
	if errors.Is(runErr, errRiverStopped) && ctx.Err() != nil {
		runErr = nil
	}
	return errors.Join(runErr, stopErr, srvErr)
}
