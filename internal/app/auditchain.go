package app

// Stage 18 (ADR-050, F-S18-08): periodic audit chain verification on the maintenance queue. INCREMENTAL runs verify from
// the last MAC-protected checkpoint; FULL runs recompute both chains in parallel ranges. Every run is recorded in
// audit.chain_verifications (read by the operations snapshot and the security alerts); a problem is logged at error level.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/jobs"
)

const (
	jobAuditVerifyIncremental = "audit.chain.verify_incremental"
	jobAuditVerifyFull        = "audit.chain.verify_full"
)

type auditVerifyIncrementalArgs struct{}

func (auditVerifyIncrementalArgs) Kind() string { return jobAuditVerifyIncremental }

type auditVerifyFullArgs struct{}

func (auditVerifyFullArgs) Kind() string { return jobAuditVerifyFull }

type auditVerifyWorker[T river.JobArgs] struct {
	river.WorkerDefaults[T]
	V      *audit.Verifier
	Full   bool
	Logger *slog.Logger
}

func (w *auditVerifyWorker[T]) Timeout(*river.Job[T]) time.Duration { return time.Hour }

func (w *auditVerifyWorker[T]) Work(ctx context.Context, _ *river.Job[T]) error {
	for _, chain := range audit.Chains {
		var res audit.Result
		var err error
		if w.Full {
			res, err = w.V.VerifyFull(ctx, chain)
		} else {
			res, err = w.V.VerifyIncremental(ctx, chain)
		}
		if err != nil {
			return err
		}
		attrs := []any{"chain", chain, "mode", res.Mode, "from_seq", res.FromSeq, "to_seq", res.ToSeq, "outcome", res.Outcome,
			"problems", len(res.Problems), "duration_ms", res.Duration.Milliseconds(), "workers", res.Workers}
		if res.Intact() {
			w.Logger.Info("audit chain verified", attrs...)
		} else {
			w.Logger.Error("audit chain verification found problems", attrs...)
		}
	}
	return nil
}

// auditCheckpointKeyID names the checkpoint key by a fingerprint, so a rotated key is recognised (its checkpoints are
// not trusted and the next run recomputes the chains) without ever storing or logging the key.
func auditCheckpointKeyID(hexKey string) string {
	fp := sha256.Sum256([]byte("fundzim/audit-checkpoint-key-id/" + hexKey))
	return "audit-checkpoint/" + hex.EncodeToString(fp[:8])
}

func auditChainJobs(workers *river.Workers, v *audit.Verifier, wcfg WorkerConfig, logger *slog.Logger) []*river.PeriodicJob {
	river.AddWorker(workers, &auditVerifyWorker[auditVerifyIncrementalArgs]{V: v, Logger: logger})
	river.AddWorker(workers, &auditVerifyWorker[auditVerifyFullArgs]{V: v, Full: true, Logger: logger})
	every := func(d time.Duration, id string, args river.JobArgs, onStart bool) *river.PeriodicJob {
		return river.NewPeriodicJob(river.PeriodicInterval(d), func() (river.JobArgs, *river.InsertOpts) {
			return args, &river.InsertOpts{Queue: jobs.QueueMaintenance, UniqueOpts: river.UniqueOpts{ByPeriod: d}}
		}, &river.PeriodicJobOpts{ID: id, RunOnStart: onStart})
	}
	// the incremental run on start does a FULL run when there is no checkpoint yet; restarts never queue extra full runs
	return []*river.PeriodicJob{
		every(wcfg.AuditVerifyInterval, jobAuditVerifyIncremental, auditVerifyIncrementalArgs{}, true),
		every(wcfg.AuditFullVerifyEvery, jobAuditVerifyFull, auditVerifyFullArgs{}, false),
	}
}
