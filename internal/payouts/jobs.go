package payouts

import (
	"context"
	"fmt"
	"time"

	"github.com/riverqueue/river"
)

// Periodic jobs (worker): submission and dispatch every 2 s, status polling every 5 s, recovery every 30 s,
// reconciliation every 15 minutes. Each kind runs at most once per period (unique by args and period). Submission,
// dispatch and polling do nothing unless PAYOUTS_EXECUTION_ENVIRONMENT is LOCAL_SIMULATOR.
const (
	KindSubmit    = "payouts.submit"
	KindPoll      = "payouts.poll"
	KindRecover   = "payouts.recover"
	KindReconcile = "payouts.reconcile"
)

type SubmitArgs struct{}

func (SubmitArgs) Kind() string { return KindSubmit }
func (SubmitArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: 2 * time.Second}}
}

type PollArgs struct{}

func (PollArgs) Kind() string { return KindPoll }
func (PollArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: 5 * time.Second}}
}

type RecoverArgs struct{}

func (RecoverArgs) Kind() string { return KindRecover }
func (RecoverArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: 30 * time.Second}}
}

type ReconcileArgs struct{}

func (ReconcileArgs) Kind() string { return KindReconcile }
func (ReconcileArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{MaxAttempts: 3, UniqueOpts: river.UniqueOpts{ByArgs: true, ByPeriod: 15 * time.Minute}}
}

// SubmitWorker re-checks and submits approved payouts, then sends due provider instructions.
type SubmitWorker struct {
	river.WorkerDefaults[SubmitArgs]
	Service *Service
}

func (w *SubmitWorker) Timeout(*river.Job[SubmitArgs]) time.Duration { return time.Minute }
func (w *SubmitWorker) Work(ctx context.Context, _ *river.Job[SubmitArgs]) error {
	if _, err := w.Service.SubmitDue(ctx, 20); err != nil {
		return fmt.Errorf("payouts: submit: %w", err)
	}
	if _, err := w.Service.DispatchDue(ctx, 20); err != nil {
		return fmt.Errorf("payouts: dispatch: %w", err)
	}
	return nil
}

// PollWorker queries the provider for payouts whose status query is due.
type PollWorker struct {
	river.WorkerDefaults[PollArgs]
	Service *Service
}

func (w *PollWorker) Timeout(*river.Job[PollArgs]) time.Duration { return time.Minute }
func (w *PollWorker) Work(ctx context.Context, _ *river.Job[PollArgs]) error {
	_, err := w.Service.PollDue(ctx, 50)
	return err
}

// RecoverWorker re-drives work a crash may have interrupted.
type RecoverWorker struct {
	river.WorkerDefaults[RecoverArgs]
	Service *Service
}

func (w *RecoverWorker) Timeout(*river.Job[RecoverArgs]) time.Duration { return 2 * time.Minute }
func (w *RecoverWorker) Work(ctx context.Context, _ *river.Job[RecoverArgs]) error {
	return w.Service.Recover(ctx)
}

// ReconcileWorker runs payout reconciliation.
type ReconcileWorker struct {
	river.WorkerDefaults[ReconcileArgs]
	Service *Service
}

func (w *ReconcileWorker) Timeout(*river.Job[ReconcileArgs]) time.Duration { return 10 * time.Minute }
func (w *ReconcileWorker) Work(ctx context.Context, _ *river.Job[ReconcileArgs]) error {
	_, err := w.Service.Reconcile(ctx, "system:payouts.reconcile_job")
	return err
}

// Register adds the workers and returns the periodic jobs.
func Register(workers *river.Workers, s *Service) []*river.PeriodicJob {
	river.AddWorker(workers, &SubmitWorker{Service: s})
	river.AddWorker(workers, &PollWorker{Service: s})
	river.AddWorker(workers, &RecoverWorker{Service: s})
	river.AddWorker(workers, &ReconcileWorker{Service: s})
	return []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(2*time.Second), func() (river.JobArgs, *river.InsertOpts) { return SubmitArgs{}, nil },
			&river.PeriodicJobOpts{ID: KindSubmit, RunOnStart: true}),
		river.NewPeriodicJob(river.PeriodicInterval(5*time.Second), func() (river.JobArgs, *river.InsertOpts) { return PollArgs{}, nil },
			&river.PeriodicJobOpts{ID: KindPoll, RunOnStart: true}),
		river.NewPeriodicJob(river.PeriodicInterval(30*time.Second), func() (river.JobArgs, *river.InsertOpts) { return RecoverArgs{}, nil },
			&river.PeriodicJobOpts{ID: KindRecover, RunOnStart: true}),
		river.NewPeriodicJob(river.PeriodicInterval(15*time.Minute), func() (river.JobArgs, *river.InsertOpts) { return ReconcileArgs{}, nil },
			&river.PeriodicJobOpts{ID: KindReconcile, RunOnStart: false}),
	}
}
