package app

// Stage 18 (ADR-050, docs/stage-18/security-monitoring.md): the security scan. The worker builds an operations service
// over the module snapshots it has (each module reads only its own tables), the identity security signals and the audit
// chain verification status, and runs operations.SecurityScan every minute on the maintenance queue.

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/auth"
	"github.com/Fatifizo/fundzim/internal/operations"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/jobs"
	"github.com/Fatifizo/fundzim/internal/platform/opsview"
)

const jobSecurityScan = "operations.security_scan"

// securitySources are the snapshot sources every security scan reads (identity signals and audit verification status).
func securitySources(pool *pgxpool.Pool, clk clock.Clock) []operations.Source {
	return []operations.Source{
		{Module: "identity_security", Snapshot: func(ctx context.Context) (opsview.Snapshot, error) {
			return auth.SecuritySignals(ctx, pool, clk.Now().UTC())
		}},
		{Module: "audit", Snapshot: func(ctx context.Context) (opsview.Snapshot, error) {
			return audit.OpsSnapshot(ctx, pool, clk.Now().UTC(), time.Hour)
		}},
	}
}

// newSecurityScanner builds the worker's operations service for the security scan.
func newSecurityScanner(d *WorkerDeps) *operations.Service {
	s := &operations.Service{Pool: d.DB, Clock: d.Clock, Logger: d.Logger, Environment: string(d.Config.App.Env)}
	s.Sources = append(s.Sources, securitySources(d.DB, d.Clock)...)
	add := func(module string, snap func(context.Context) (opsview.Snapshot, error)) {
		s.Sources = append(s.Sources, operations.Source{Module: module, Snapshot: snap})
	}
	if m := d.Verification; m != nil {
		if m.Payments != nil {
			add("payments", m.Payments.OpsSnapshot)
		}
		if m.PSP != nil {
			add("providers", m.PSP.OpsSnapshot)
		}
		if m.Ledger != nil {
			add("ledger", m.Ledger.OpsSnapshot)
		}
		if m.Payouts != nil {
			add("payouts", m.Payouts.OpsSnapshot)
		}
		if m.Risk != nil {
			add("risk", m.Risk.OpsSnapshot)
		}
	}
	if d.Notifications != nil {
		add("notifications", d.Notifications.OpsSnapshot)
	}
	if d.Reconciliation != nil {
		add("reconciliation", d.Reconciliation.OpsSnapshot)
	}
	if d.DB != nil {
		s.Probes = append(s.Probes, operations.HealthProbe{Name: "database", Check: func(ctx context.Context) error { return db.Ping(ctx, d.DB) }})
	}
	return s
}

type securityScanArgs struct{}

func (securityScanArgs) Kind() string { return jobSecurityScan }

type securityScanWorker struct {
	river.WorkerDefaults[securityScanArgs]
	S      *operations.Service
	Logger *slog.Logger
}

func (w *securityScanWorker) Timeout(*river.Job[securityScanArgs]) time.Duration {
	return 2 * time.Minute
}

func (w *securityScanWorker) Work(ctx context.Context, _ *river.Job[securityScanArgs]) error {
	res, err := w.S.SecurityScan(ctx)
	if err != nil {
		return err
	}
	if res.Raised+res.Recurred+res.Cleared > 0 {
		w.Logger.Info("security scan", "findings", res.Findings, "raised", res.Raised, "recurred", res.Recurred, "cleared", res.Cleared)
	}
	return nil
}

func securityJobs(workers *river.Workers, d *WorkerDeps) []*river.PeriodicJob {
	river.AddWorker(workers, &securityScanWorker{S: newSecurityScanner(d), Logger: d.Logger})
	return []*river.PeriodicJob{river.NewPeriodicJob(river.PeriodicInterval(time.Minute), func() (river.JobArgs, *river.InsertOpts) {
		return securityScanArgs{}, &river.InsertOpts{Queue: jobs.QueueMaintenance, UniqueOpts: river.UniqueOpts{ByPeriod: time.Minute}}
	}, &river.PeriodicJobOpts{ID: jobSecurityScan, RunOnStart: true})}
}
