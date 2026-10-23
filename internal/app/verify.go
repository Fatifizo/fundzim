package app

// Stage 18 (docs/stage-18/backup-recovery.md): operator verification used after a restore and in routine operations
// (fundzimctl verify …). Each check reads through the owning module and reports counts and outcomes only — never content.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/ledger"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/config"
	"github.com/Fatifizo/fundzim/internal/platform/crypto"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/storage"
)

// VerifyAuditChains runs a FULL (or INCREMENTAL) verification of both audit chains as the worker role.
func VerifyAuditChains(ctx context.Context, wcfg WorkerConfig, full bool) ([]audit.Result, error) {
	pool, err := db.NewPool(ctx, db.Options{URL: wcfg.DatabaseURL.Reveal(), MaxConns: 10, ConnectTimeout: 10 * time.Second, AppName: "fundzimctl-verify"})
	if err != nil {
		return nil, err
	}
	defer pool.Close()
	key, err := crypto.NewKeyed(auditCheckpointKeyID(wcfg.AuditCheckpointKey.Reveal()), wcfg.AuditCheckpointKey.Reveal())
	if err != nil {
		return nil, err
	}
	v := &audit.Verifier{Pool: pool, Key: key, Workers: 8}
	var out []audit.Result
	for _, chain := range audit.Chains {
		var res audit.Result
		if full {
			res, err = v.VerifyFull(ctx, chain)
		} else {
			res, err = v.VerifyIncremental(ctx, chain)
		}
		if err != nil {
			return out, fmt.Errorf("%s: %w", chain, err)
		}
		out = append(out, res)
	}
	return out, nil
}

// VerifyLedger runs the ledger integrity checks (and records the run, as the ledger always does).
func VerifyLedger(ctx context.Context, cfg config.Config, logger *slog.Logger) (ledger.IntegrityReport, error) {
	pool, err := db.NewPool(ctx, db.Options{URL: cfg.Database.LedgerURL.Reveal(), MaxConns: 2, ConnectTimeout: cfg.Database.ConnectTimeout, AppName: "fundzimctl-verify"})
	if err != nil {
		return ledger.IntegrityReport{}, err
	}
	defer pool.Close()
	return ledger.New(pool, clock.System, logger).RunIntegrity(ctx, "SYSTEM", "fundzimctl.verify")
}

// ObjectsReport is the outcome of an object-store verification. Objects recorded under a key this deployment does not
// hold (for example written by tests with throwaway keys, or under a retired key) cannot be opened and are counted apart;
// they are not integrity failures. Integrity failures and missing objects under a configured key fail the check.
type ObjectsReport struct {
	Sampled             int      `json:"sampled"`
	Verified            int      `json:"verified"`
	KeyNotConfigured    int      `json:"key_not_configured"`
	IntegrityFailures   int      `json:"integrity_failures"`
	MissingOrUnreadable int      `json:"missing_or_unreadable"`
	FailedIDs           []string `json:"failed_ids"`
	TotalClean          int64    `json:"total_clean_objects"`
	DurationMS          int64    `json:"duration_ms"`
	Description         string   `json:"description"`
}

// Failed reports whether an object under a configured key is missing, unreadable or altered.
func (r ObjectsReport) Failed() bool { return r.IntegrityFailures > 0 || r.MissingOrUnreadable > 0 }

// VerifyObjects opens a sample of CLEAN stored objects through the storage module (SSE-C key per object, SHA-256 checked
// against the value recorded at upload) and reads them to the end. sample <= 0 checks every CLEAN object.
func VerifyObjects(ctx context.Context, cfg config.Config, sample int, logger *slog.Logger) (ObjectsReport, error) {
	start := time.Now()
	cfg, err := resolveManagedKeys(ctx, cfg)
	if err != nil {
		return ObjectsReport{}, err
	}
	pool, err := db.NewPool(ctx, db.Options{URL: cfg.Database.URL.Reveal(), MaxConns: 4, ConnectTimeout: cfg.Database.ConnectTimeout, AppName: "fundzimctl-verify"})
	if err != nil {
		return ObjectsReport{}, err
	}
	defer pool.Close()
	blobs, err := privateBlobs(cfg)
	if err != nil {
		return ObjectsReport{}, err
	}
	if blobs == nil {
		return ObjectsReport{}, errors.New("STORAGE_ENDPOINT is not configured")
	}
	svc, err := storage.New(storage.Config{AppEnv: string(cfg.App.Env), TicketKey: decodeKey(cfg.Security.DocumentTicketKey), TicketTTL: cfg.Verification.DocumentTicketTTL,
		SSEKey: decodeKey(cfg.Security.StorageSSEKey), SSEKeyManaged: cfg.Security.Managed, SSEPreviousKeys: decodeKeys(cfg.Security.StorageSSEPrevious)},
		storage.Deps{Pool: pool, Blobs: blobs, Clock: clock.System, Logger: logger})
	if err != nil {
		return ObjectsReport{}, err
	}
	objs, total, err := svc.SampleClean(ctx, sample)
	if err != nil {
		return ObjectsReport{}, err
	}
	rep := ObjectsReport{Sampled: len(objs), TotalClean: total, FailedIDs: []string{},
		Description: "each object opened with its recorded SSE-C key and read to the end; content SHA-256 compared with the upload record"}
	for _, o := range objs {
		if !o.KeyConfigured {
			rep.KeyNotConfigured++
			continue
		}
		rc, _, err := svc.Open(ctx, o.ID)
		if err == nil {
			_, err = io.Copy(io.Discard, rc)
			if cerr := rc.Close(); err == nil {
				err = cerr
			}
		}
		switch {
		case err == nil:
			rep.Verified++
			continue
		case errors.Is(err, storage.ErrIntegrity):
			rep.IntegrityFailures++
		default:
			rep.MissingOrUnreadable++
		}
		if len(rep.FailedIDs) < 50 {
			rep.FailedIDs = append(rep.FailedIDs, o.ID)
		}
	}
	rep.DurationMS = time.Since(start).Milliseconds()
	return rep, nil
}
