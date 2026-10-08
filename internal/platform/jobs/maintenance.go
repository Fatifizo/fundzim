package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// PurgeIdempotencyKeysArgs deletes app.idempotency_keys rows past expires_at. The idempotency package
// (work stream B) owns the table's semantics; this job only runs the retention DELETE so the worker does
// not depend on that package (contract §2.2).
type PurgeIdempotencyKeysArgs struct{}

// Kind implements river.JobArgs.
func (PurgeIdempotencyKeysArgs) Kind() string { return "maintenance.purge_idempotency_keys" }

// InsertOpts implements river.JobArgsWithInsertOpts.
func (PurgeIdempotencyKeysArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueMaintenance, MaxAttempts: 3,
		UniqueOpts: river.UniqueOpts{ByPeriod: time.Minute}}
}

// PurgeIdempotencyKeysWorker runs the purge in bounded batches.
type PurgeIdempotencyKeysWorker struct {
	river.WorkerDefaults[PurgeIdempotencyKeysArgs]
	Pool      *pgxpool.Pool
	Logger    *slog.Logger
	BatchSize int // default 5000
}

var maintenanceBackoff = Backoff{Base: 30 * time.Second, Cap: 5 * time.Minute}

// NextRetry implements river.Worker.
func (w *PurgeIdempotencyKeysWorker) NextRetry(job *river.Job[PurgeIdempotencyKeysArgs]) time.Time {
	return maintenanceBackoff.NextRetry(job.JobRow)
}

// Timeout implements river.Worker.
func (w *PurgeIdempotencyKeysWorker) Timeout(*river.Job[PurgeIdempotencyKeysArgs]) time.Duration {
	return 60 * time.Second
}

// Work implements river.Worker.
func (w *PurgeIdempotencyKeysWorker) Work(ctx context.Context, _ *river.Job[PurgeIdempotencyKeysArgs]) error {
	n, err := PurgeExpiredIdempotencyKeys(ctx, w.Pool, w.BatchSize)
	if n > 0 {
		w.Logger.Info("purged expired idempotency keys", slog.Int64("rows", n))
	}
	return err
}

// PurgeExpiredIdempotencyKeys deletes expired keys in batches of batchSize (default 5000).
func PurgeExpiredIdempotencyKeys(ctx context.Context, pool *pgxpool.Pool, batchSize int) (int64, error) {
	if batchSize <= 0 {
		batchSize = 5000
	}
	var total int64
	for {
		tag, err := pool.Exec(ctx, `
			DELETE FROM app.idempotency_keys
			 WHERE id IN (SELECT id FROM app.idempotency_keys WHERE expires_at < now()
			               ORDER BY expires_at LIMIT $1 FOR UPDATE SKIP LOCKED)`, batchSize)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < int64(batchSize) {
			return total, nil
		}
	}
}
