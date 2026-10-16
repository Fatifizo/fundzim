package risk

import (
	"context"
	"fmt"
	"time"

	"github.com/riverqueue/river"
)

// Periodic risk jobs (worker): every minute expire temporary holds, poll due screenings and deliver simulated
// screening callbacks; every 15 minutes record stale-case monitoring events and reconcile. Each kind runs at most once
// per period (unique by args and period).
const (
	KindMaintenance = "risk.maintenance"
	KindReconcile   = "risk.reconcile"
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

// MaintenanceWorker expires holds and drives screening.
type MaintenanceWorker struct {
	river.WorkerDefaults[MaintenanceArgs]
	Service *Service
	Sandbox *SandboxScreening // nil: no simulator in this process
}

func (w *MaintenanceWorker) Timeout(*river.Job[MaintenanceArgs]) time.Duration {
	return 30 * time.Second
}
func (w *MaintenanceWorker) Work(ctx context.Context, _ *river.Job[MaintenanceArgs]) error {
	if _, err := w.Service.ExpireHolds(ctx, 200); err != nil {
		return fmt.Errorf("risk: expire holds: %w", err)
	}
	if w.Sandbox != nil {
		if _, err := w.Sandbox.DeliverDue(ctx, 50); err != nil {
			return fmt.Errorf("risk: sandbox screening delivery: %w", err)
		}
	}
	if _, err := w.Service.PollScreenings(ctx, 50); err != nil {
		return fmt.Errorf("risk: poll screenings: %w", err)
	}
	return nil
}

// ReconcileWorker records stale cases and runs risk reconciliation.
type ReconcileWorker struct {
	river.WorkerDefaults[ReconcileArgs]
	Service *Service
}

func (w *ReconcileWorker) Timeout(*river.Job[ReconcileArgs]) time.Duration { return 5 * time.Minute }
func (w *ReconcileWorker) Work(ctx context.Context, _ *river.Job[ReconcileArgs]) error {
	if _, err := w.Service.StaleCases(ctx); err != nil {
		return fmt.Errorf("risk: stale cases: %w", err)
	}
	_, err := w.Service.Reconcile(ctx, "risk.reconcile_job")
	return err
}

// Register adds the risk workers and returns their periodic jobs.
func Register(workers *river.Workers, s *Service, sb *SandboxScreening) []*river.PeriodicJob {
	river.AddWorker(workers, &MaintenanceWorker{Service: s, Sandbox: sb})
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
