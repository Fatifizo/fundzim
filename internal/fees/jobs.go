package fees

import (
	"context"
	"fmt"
	"time"

	"github.com/riverqueue/river"
)

// Periodic jobs (worker): expire quotes and retry pending payout fee journals every minute, reconcile every 15
// minutes. Each kind runs at most once per period (unique by args and period).
const (
	KindMaintenance = "fees.maintenance"
	KindReconcile   = "fees.reconcile"
)

type MaintenanceArgs struct{}

func (MaintenanceArgs) Kind() string { return KindMaintenance }
func (MaintenanceArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: time.Minute}}
}

type ReconcileArgs struct{}

func (ReconcileArgs) Kind() string { return KindReconcile }
func (ReconcileArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: 15 * time.Minute}}
}

// MaintenanceWorker expires quotes and retries pending fee journals.
type MaintenanceWorker struct {
	river.WorkerDefaults[MaintenanceArgs]
	Service *Service
}

func (w *MaintenanceWorker) Timeout(*river.Job[MaintenanceArgs]) time.Duration {
	return 30 * time.Second
}
func (w *MaintenanceWorker) Work(ctx context.Context, _ *river.Job[MaintenanceArgs]) error {
	if _, err := w.Service.ExpireQuotes(ctx); err != nil {
		return fmt.Errorf("fees: expire quotes: %w", err)
	}
	if _, err := w.Service.PostPending(ctx); err != nil {
		return fmt.Errorf("fees: pending postings: %w", err)
	}
	return nil
}

// ReconcileWorker runs fee reconciliation.
type ReconcileWorker struct {
	river.WorkerDefaults[ReconcileArgs]
	Service *Service
}

func (w *ReconcileWorker) Timeout(*river.Job[ReconcileArgs]) time.Duration { return 10 * time.Minute }
func (w *ReconcileWorker) Work(ctx context.Context, _ *river.Job[ReconcileArgs]) error {
	_, err := w.Service.Reconcile(ctx, "fees.reconcile_job")
	return err
}

// Register adds the fee workers and returns their periodic jobs.
func Register(workers *river.Workers, s *Service) []*river.PeriodicJob {
	river.AddWorker(workers, &MaintenanceWorker{Service: s})
	river.AddWorker(workers, &ReconcileWorker{Service: s})
	return []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(time.Minute), func() (river.JobArgs, *river.InsertOpts) {
			return MaintenanceArgs{}, nil
		}, &river.PeriodicJobOpts{ID: KindMaintenance, RunOnStart: true}),
		river.NewPeriodicJob(river.PeriodicInterval(15*time.Minute), func() (river.JobArgs, *river.InsertOpts) {
			return ReconcileArgs{}, nil
		}, &river.PeriodicJobOpts{ID: KindReconcile, RunOnStart: false}),
	}
}
