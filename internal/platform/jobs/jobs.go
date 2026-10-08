// Package jobs configures River, FundZim's PostgreSQL-backed job queue (ADR-025,
// docs/architecture/background-processing.md). River's tables live in schema `queue`; its migrations are
// committed verbatim as goose migrations (migrations/20261008130000_*, 20261008130100_*).
//
// Two kinds of client exist:
//   - NewInsertClient: insert-only (no queues, never started). For a process that enqueues jobs with
//     InsertTx but never works them. The API does not need one today: domain events go through the
//     transactional outbox (plain SQL), and the worker turns them into jobs.
//   - NewWorkerClient: works the queues below in the worker process.
//
// Jobs that exhaust their attempts are discarded by River (its dead-letter state). Every discard is
// logged at error level and counted in fundzim_jobs_discarded_total{kind} (alerting: ALR-Q02).
package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
)

// Schema is the PostgreSQL schema holding River's tables.
const Schema = "queue"

// Queues (contract §2.1). Kinds choose their queue through InsertOpts.
const (
	QueueDefault       = "default"
	QueueOutbox        = "outbox"
	QueueNotifications = "notifications"
	QueueMaintenance   = "maintenance"
)

// DefaultQueues returns the per-process worker counts (background-processing §3, [default] values).
// concurrency scales the default and notifications queues (WORKER_CONCURRENCY).
func DefaultQueues(concurrency int) map[string]river.QueueConfig {
	if concurrency < 1 {
		concurrency = 1
	}
	return map[string]river.QueueConfig{
		QueueDefault:       {MaxWorkers: concurrency},
		QueueOutbox:        {MaxWorkers: concurrency},
		QueueNotifications: {MaxWorkers: max(1, concurrency/2)},
		QueueMaintenance:   {MaxWorkers: 2},
	}
}

// Backoff is exponential backoff with jitter: delay = min(Cap, Base·2^(attempt−1)) × U[0.8, 1.2]
// (background-processing §2.1). Kinds declare their own Backoff; nothing relies on River's default.
type Backoff struct {
	Base time.Duration
	Cap  time.Duration
}

// Delay returns the wait before the retry that follows failed attempt number attempt (1-based).
func (b Backoff) Delay(attempt int) time.Duration {
	return b.delay(attempt, 0.8+0.4*rand.Float64())
}

func (b Backoff) delay(attempt int, jitter float64) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	base := float64(b.Base)
	d := base * math.Pow(2, float64(attempt-1))
	if b.Cap > 0 && d > float64(b.Cap) {
		d = float64(b.Cap)
	}
	return time.Duration(d * jitter)
}

// NextRetry is the absolute retry time for a River job row.
func (b Backoff) NextRetry(job *rivertype.JobRow) time.Time {
	return time.Now().UTC().Add(b.Delay(job.Attempt))
}

// Metrics are the job-queue metric families. They are registered on the process registry
// (metrics.Metrics.Registry) so they appear on the internal /metrics endpoint.
type Metrics struct {
	completed *prometheus.CounterVec
	failed    *prometheus.CounterVec
	discarded *prometheus.CounterVec
	cancelled *prometheus.CounterVec
}

// NewMetrics registers the job metric families on reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		completed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fundzim_jobs_completed_total", Help: "Jobs that completed successfully, by kind."}, []string{"kind"}),
		failed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fundzim_jobs_failed_total", Help: "Failed job attempts that will be retried, by kind."}, []string{"kind"}),
		discarded: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fundzim_jobs_discarded_total", Help: "Jobs discarded after their last attempt (dead letter), by kind."}, []string{"kind"}),
		cancelled: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "fundzim_jobs_cancelled_total", Help: "Jobs cancelled permanently by their worker, by kind."}, []string{"kind"}),
	}
	reg.MustRegister(m.completed, m.failed, m.discarded, m.cancelled)
	return m
}

// Discarded returns the discarded counter for kind (tests and alert rules).
func (m *Metrics) Discarded(kind string) prometheus.Counter { return m.discarded.WithLabelValues(kind) }

// Completed returns the completed counter for kind.
func (m *Metrics) Completed(kind string) prometheus.Counter { return m.completed.WithLabelValues(kind) }

// Failed returns the failed-attempt counter for kind.
func (m *Metrics) Failed(kind string) prometheus.Counter { return m.failed.WithLabelValues(kind) }

// Outcome classifies the result of one job attempt.
type Outcome string

const (
	OutcomeCompleted Outcome = "completed"
	OutcomeRetry     Outcome = "retry"
	OutcomeDiscarded Outcome = "discarded"
	OutcomeCancelled Outcome = "cancelled"
	OutcomeSnoozed   Outcome = "snoozed"
)

// Classify decides what River will do with a job after an attempt that returned err. attempt is the
// 1-based number of the attempt just made.
func Classify(attempt, maxAttempts int, err error) Outcome {
	if err == nil {
		return OutcomeCompleted
	}
	var snooze *rivertype.JobSnoozeError
	if errors.As(err, &snooze) {
		return OutcomeSnoozed
	}
	var cancel *rivertype.JobCancelError
	if errors.As(err, &cancel) {
		return OutcomeCancelled
	}
	if attempt >= maxAttempts {
		return OutcomeDiscarded
	}
	return OutcomeRetry
}

// observer records metrics and logs for each finished attempt. Errors are logged by kind and job ID
// only plus the error text; handlers must never put secrets, codes or links into errors.
type observer struct {
	m      *Metrics
	logger *slog.Logger
}

func (o *observer) record(job *rivertype.JobRow, err error) {
	switch Classify(job.Attempt, job.MaxAttempts, err) {
	case OutcomeCompleted:
		o.m.completed.WithLabelValues(job.Kind).Inc()
	case OutcomeRetry:
		o.m.failed.WithLabelValues(job.Kind).Inc()
		o.logger.Warn("job attempt failed; will retry", slog.String("job_kind", job.Kind), slog.Int64("job_id", job.ID),
			slog.Int("attempt", job.Attempt), slog.Int("max_attempts", job.MaxAttempts), slog.String("queue", job.Queue),
			slog.String("error", errText(err)))
	case OutcomeDiscarded:
		o.m.discarded.WithLabelValues(job.Kind).Inc()
		o.logger.Error("job discarded after final attempt (dead letter)", slog.String("job_kind", job.Kind),
			slog.Int64("job_id", job.ID), slog.Int("attempt", job.Attempt), slog.String("queue", job.Queue),
			slog.String("alert", "ALR-Q02"), slog.String("error", errText(err)))
	case OutcomeCancelled:
		o.m.cancelled.WithLabelValues(job.Kind).Inc()
		o.logger.Error("job cancelled permanently", slog.String("job_kind", job.Kind), slog.Int64("job_id", job.ID),
			slog.String("error", errText(err)))
	case OutcomeSnoozed:
	}
}

// WorkEnd implements rivertype.HookWorkEnd. It passes err through unchanged.
func (o *observer) WorkEnd(_ context.Context, job *rivertype.JobRow, err error) error {
	o.record(job, err)
	return err
}

func (o *observer) IsHook() bool { return true }

// HandleError implements river.ErrorHandler (errors are recorded by WorkEnd).
func (o *observer) HandleError(context.Context, *rivertype.JobRow, error) *river.ErrorHandlerResult {
	return nil
}

// HandlePanic records a panicking job (WorkEnd does not see panics). The stack trace is not logged
// because it may contain argument values.
func (o *observer) HandlePanic(_ context.Context, job *rivertype.JobRow, _ any, _ string) *river.ErrorHandlerResult {
	o.record(job, errors.New("job panicked"))
	return nil
}

const maxErrLen = 300

func errText(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > maxErrLen {
		s = s[:maxErrLen] + "…"
	}
	return s
}

// WorkerConfig configures a working River client.
type WorkerConfig struct {
	Pool    *pgxpool.Pool
	Logger  *slog.Logger
	Metrics *Metrics
	Workers *river.Workers
	// Queues defaults to DefaultQueues(4).
	Queues       map[string]river.QueueConfig
	PeriodicJobs []*river.PeriodicJob
	// ID identifies the client in River's leader table and job attempted_by (default: River generates one).
	ID string
	// JobTimeout is the default per-job timeout (default 60 s); kinds override it with Timeout().
	JobTimeout time.Duration
	// RescueStuckJobsAfter recovers jobs whose worker died (default 15 min, > every kind's timeout).
	RescueStuckJobsAfter time.Duration
	// FetchPollInterval bounds pickup latency when LISTEN/NOTIFY misses (default River's 1 s).
	FetchPollInterval time.Duration
}

// NewWorkerClient builds a River client that works jobs. Call Start to begin and Stop to drain.
func NewWorkerClient(cfg WorkerConfig) (*river.Client[pgx.Tx], error) {
	if cfg.Pool == nil || cfg.Workers == nil || cfg.Metrics == nil || cfg.Logger == nil {
		return nil, errors.New("jobs: pool, workers, metrics and logger are required")
	}
	queues := cfg.Queues
	if queues == nil {
		queues = DefaultQueues(4)
	}
	obs := &observer{m: cfg.Metrics, logger: cfg.Logger}
	rc := &river.Config{
		ID:                          cfg.ID,
		Schema:                      Schema,
		Queues:                      queues,
		Workers:                     cfg.Workers,
		PeriodicJobs:                cfg.PeriodicJobs,
		Logger:                      riverLogger(cfg.Logger),
		ErrorHandler:                obs,
		Hooks:                       []rivertype.Hook{obs},
		JobTimeout:                  cmpOr(cfg.JobTimeout, 60*time.Second),
		RescueStuckJobsAfter:        cmpOr(cfg.RescueStuckJobsAfter, 15*time.Minute),
		FetchPollInterval:           cfg.FetchPollInterval,
		CompletedJobRetentionPeriod: 24 * time.Hour,
		CancelledJobRetentionPeriod: 7 * 24 * time.Hour,
		DiscardedJobRetentionPeriod: 90 * 24 * time.Hour,
		// Every kind sets MaxAttempts explicitly; this is only a backstop.
		MaxAttempts: 10,
	}
	c, err := river.NewClient(riverpgxv5.New(cfg.Pool), rc)
	if err != nil {
		return nil, fmt.Errorf("jobs: new worker client: %w", err)
	}
	return c, nil
}

// NewInsertClient builds an insert-only client (no queues; never started). workers may be nil; when
// given, River verifies that inserted kinds have a worker.
func NewInsertClient(pool *pgxpool.Pool, logger *slog.Logger, workers *river.Workers) (*river.Client[pgx.Tx], error) {
	c, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Schema: Schema, Workers: workers, Logger: riverLogger(logger), MaxAttempts: 10,
	})
	if err != nil {
		return nil, fmt.Errorf("jobs: new insert client: %w", err)
	}
	return c, nil
}

// riverLogger keeps River's own chatter at warn level and above.
func riverLogger(l *slog.Logger) *slog.Logger {
	return slog.New(minLevel{h: l.Handler(), min: slog.LevelWarn}).With(slog.String("component", "river"))
}

type minLevel struct {
	h   slog.Handler
	min slog.Level
}

func (m minLevel) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= m.min && m.h.Enabled(ctx, l)
}
func (m minLevel) Handle(ctx context.Context, r slog.Record) error { return m.h.Handle(ctx, r) }
func (m minLevel) WithAttrs(a []slog.Attr) slog.Handler {
	return minLevel{h: m.h.WithAttrs(a), min: m.min}
}
func (m minLevel) WithGroup(n string) slog.Handler { return minLevel{h: m.h.WithGroup(n), min: m.min} }

func cmpOr(d, def time.Duration) time.Duration {
	if d == 0 {
		return def
	}
	return d
}
