package storage

import (
	"context"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"github.com/Fatifizo/fundzim/internal/platform/jobs"
)

// Job kinds (stable once shipped).
const (
	KindRescan        = "storage.rescan_due"
	KindExpireUploads = "storage.expire_uploads"
)

// RescanArgs is the periodic rescan job (FAILED_SCAN retries, interrupted scans, missed events).
type RescanArgs struct{}

// Kind implements river.JobArgs.
func (RescanArgs) Kind() string { return KindRescan }

// InsertOpts implements river.JobArgsWithInsertOpts.
func (RescanArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobs.QueueMaintenance, MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByPeriod: 30 * time.Second}}
}

// RescanWorker runs Service.RescanDue.
type RescanWorker struct {
	river.WorkerDefaults[RescanArgs]
	Service    *Service
	StaleAfter time.Duration // QUARANTINED this long without progress is rescanned (default 5 min)
	Limit      int           // objects per run (default 5; the job timeout grows with it and must stay below River's 15 min rescue)
}

func (w *RescanWorker) limit() int {
	if w.Limit <= 0 {
		return 5
	}
	return w.Limit
}

// Timeout implements river.Worker: room for limit sequential scans.
func (w *RescanWorker) Timeout(*river.Job[RescanArgs]) time.Duration {
	return time.Duration(w.limit()) * (w.Service.cfg.ScanTimeout + 10*time.Second)
}

// Work implements river.Worker.
func (w *RescanWorker) Work(ctx context.Context, _ *river.Job[RescanArgs]) error {
	stale := w.StaleAfter
	if stale <= 0 {
		stale = 5 * time.Minute
	}
	res, err := w.Service.RescanDue(ctx, stale, w.limit())
	if res.Interrupted > 0 || res.Exhausted > 0 {
		w.Service.logger.Warn("storage rescan", slog.Int("interrupted", res.Interrupted), slog.Int("scanned", res.Scanned),
			slog.Int("retries_exhausted", res.Exhausted))
	}
	return err
}

// ExpireUploadsArgs is the periodic interrupted-upload cleanup job.
type ExpireUploadsArgs struct{}

// Kind implements river.JobArgs.
func (ExpireUploadsArgs) Kind() string { return KindExpireUploads }

// InsertOpts implements river.JobArgsWithInsertOpts.
func (ExpireUploadsArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: jobs.QueueMaintenance, MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByPeriod: time.Minute}}
}

// ExpireUploadsWorker runs Service.ExpireUploads.
type ExpireUploadsWorker struct {
	river.WorkerDefaults[ExpireUploadsArgs]
	Service *Service
}

// Work implements river.Worker.
func (w *ExpireUploadsWorker) Work(ctx context.Context, _ *river.Job[ExpireUploadsArgs]) error {
	_, err := w.Service.ExpireUploads(ctx, 200)
	return err
}

// AddWorkers registers the storage job workers.
func (s *Service) AddWorkers(workers *river.Workers) {
	river.AddWorker(workers, &RescanWorker{Service: s})
	river.AddWorker(workers, &ExpireUploadsWorker{Service: s})
}

// PeriodicJobs returns the storage periodic jobs (rescan every 30 s, upload expiry every minute).
func PeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(30*time.Second),
			func() (river.JobArgs, *river.InsertOpts) { return RescanArgs{}, nil },
			&river.PeriodicJobOpts{ID: KindRescan}),
		river.NewPeriodicJob(river.PeriodicInterval(time.Minute),
			func() (river.JobArgs, *river.InsertOpts) { return ExpireUploadsArgs{}, nil },
			&river.PeriodicJobOpts{ID: KindExpireUploads}),
	}
}
