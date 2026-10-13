package ledger

import (
	"context"
	"fmt"
	"time"

	"github.com/riverqueue/river"
)

// Periodic jobs (worker): fold DEFERRED balances and housekeeping every 5 s, integrity hourly, reconciliation every
// 15 minutes. Each kind runs at most once per period (unique by args and period).
const (
	KindMaintenance = "ledger.maintenance"
	KindIntegrity   = "ledger.integrity"
	KindReconcile   = "ledger.reconcile"
)

type MaintenanceArgs struct{}

func (MaintenanceArgs) Kind() string { return KindMaintenance }
func (MaintenanceArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: 5 * time.Second}}
}

type IntegrityArgs struct{}

func (IntegrityArgs) Kind() string { return KindIntegrity }
func (IntegrityArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: time.Hour}}
}

type ReconcileArgs struct{}

func (ReconcileArgs) Kind() string { return KindReconcile }
func (ReconcileArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: 15 * time.Minute}}
}

// MaintenanceWorker folds deferred balances, keeps the current and next period open and expires adjustment requests.
type MaintenanceWorker struct {
	river.WorkerDefaults[MaintenanceArgs]
	Service *Service
}

func (w *MaintenanceWorker) Timeout(*river.Job[MaintenanceArgs]) time.Duration {
	return 30 * time.Second
}
func (w *MaintenanceWorker) Work(ctx context.Context, _ *river.Job[MaintenanceArgs]) error {
	if err := w.Service.EnsureCurrentPeriods(ctx); err != nil {
		return fmt.Errorf("ledger: periods: %w", err)
	}
	if _, err := w.Service.Fold(ctx); err != nil {
		return fmt.Errorf("ledger: fold: %w", err)
	}
	if _, err := w.Service.ExpireAdjustments(ctx); err != nil {
		return fmt.Errorf("ledger: expire adjustments: %w", err)
	}
	return nil
}

// IntegrityWorker runs the integrity checker.
type IntegrityWorker struct {
	river.WorkerDefaults[IntegrityArgs]
	Service *Service
}

func (w *IntegrityWorker) Timeout(*river.Job[IntegrityArgs]) time.Duration { return 10 * time.Minute }
func (w *IntegrityWorker) Work(ctx context.Context, _ *river.Job[IntegrityArgs]) error {
	_, err := w.Service.RunIntegrity(ctx, "SYSTEM", "ledger.integrity_job")
	return err
}

// ReconcileWorker runs ledger reconciliation.
type ReconcileWorker struct {
	river.WorkerDefaults[ReconcileArgs]
	Service *Service
}

func (w *ReconcileWorker) Timeout(*river.Job[ReconcileArgs]) time.Duration { return 10 * time.Minute }
func (w *ReconcileWorker) Work(ctx context.Context, _ *river.Job[ReconcileArgs]) error {
	_, err := w.Service.Reconcile(ctx, "system:ledger.reconcile_job")
	return err
}

// Register adds the workers and returns the periodic jobs.
func Register(workers *river.Workers, s *Service) []*river.PeriodicJob {
	river.AddWorker(workers, &MaintenanceWorker{Service: s})
	river.AddWorker(workers, &IntegrityWorker{Service: s})
	river.AddWorker(workers, &ReconcileWorker{Service: s})
	return []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(5*time.Second), func() (river.JobArgs, *river.InsertOpts) { return MaintenanceArgs{}, nil },
			&river.PeriodicJobOpts{ID: KindMaintenance, RunOnStart: true}),
		river.NewPeriodicJob(river.PeriodicInterval(time.Hour), func() (river.JobArgs, *river.InsertOpts) { return IntegrityArgs{}, nil },
			&river.PeriodicJobOpts{ID: KindIntegrity, RunOnStart: true}),
		river.NewPeriodicJob(river.PeriodicInterval(15*time.Minute), func() (river.JobArgs, *river.InsertOpts) { return ReconcileArgs{}, nil },
			&river.PeriodicJobOpts{ID: KindReconcile, RunOnStart: false}),
	}
}
