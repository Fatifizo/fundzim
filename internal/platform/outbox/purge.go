package outbox

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Fatifizo/fundzim/internal/platform/jobs"
)

// PurgeArgs deletes dispatched outbox rows older than the retention window. Undispatched rows are never
// deleted (a database trigger enforces it as well).
type PurgeArgs struct{}

// Kind implements river.JobArgs.
func (PurgeArgs) Kind() string { return KindPurge }

// InsertOpts implements river.JobArgsWithInsertOpts.
func (PurgeArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobs.QueueMaintenance, MaxAttempts: 3,
		UniqueOpts: river.UniqueOpts{ByPeriod: time.Minute}}
}

// PurgeWorker runs Purge as a River job.
type PurgeWorker struct {
	river.WorkerDefaults[PurgeArgs]
	Pool      *pgxpool.Pool
	Logger    *slog.Logger
	Metrics   *Metrics
	Retention time.Duration // default 7 days
}

var purgeBackoff = jobs.Backoff{Base: 30 * time.Second, Cap: 5 * time.Minute}

// NextRetry implements river.Worker.
func (w *PurgeWorker) NextRetry(job *river.Job[PurgeArgs]) time.Time {
	return purgeBackoff.NextRetry(job.JobRow)
}

// Timeout implements river.Worker.
func (w *PurgeWorker) Timeout(*river.Job[PurgeArgs]) time.Duration { return 60 * time.Second }

// Work implements river.Worker.
func (w *PurgeWorker) Work(ctx context.Context, _ *river.Job[PurgeArgs]) error {
	n, err := Purge(ctx, w.Pool, w.Retention, 5000)
	if w.Metrics != nil {
		w.Metrics.purged.Add(float64(n))
	}
	if n > 0 {
		w.Logger.Info("purged dispatched outbox events", slog.Int64("rows", n))
	}
	return err
}

// Purge deletes dispatched rows older than retention in batches.
func Purge(ctx context.Context, pool *pgxpool.Pool, retention time.Duration, batchSize int) (int64, error) {
	if retention <= 0 {
		retention = DefaultRetention
	}
	if batchSize <= 0 {
		batchSize = 5000
	}
	var total int64
	for {
		tag, err := pool.Exec(ctx, `
			DELETE FROM app.outbox_events
			 WHERE id IN (SELECT id FROM app.outbox_events
			               WHERE dispatched_at IS NOT NULL AND dispatched_at < now() - $1::interval
			               ORDER BY dispatched_at LIMIT $2 FOR UPDATE SKIP LOCKED)`, retention, batchSize)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < int64(batchSize) {
			return total, nil
		}
	}
}

// PeriodicMaintenance returns the maintenance periodic jobs: outbox purge and idempotency-key purge,
// both every interval (default 10 minutes).
func PeriodicMaintenance(interval time.Duration) []*river.PeriodicJob {
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	return []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(interval),
			func() (river.JobArgs, *river.InsertOpts) { return PurgeArgs{}, nil },
			&river.PeriodicJobOpts{ID: KindPurge}),
		river.NewPeriodicJob(river.PeriodicInterval(interval),
			func() (river.JobArgs, *river.InsertOpts) { return jobs.PurgeIdempotencyKeysArgs{}, nil },
			&river.PeriodicJobOpts{ID: jobs.PurgeIdempotencyKeysArgs{}.Kind()}),
	}
}
