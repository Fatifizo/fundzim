package main

// fundzimctl verify (Stage 18, docs/stage-18/backup-recovery.md): integrity checks for routine operations and restore
// drills. Output is JSON (counts, outcomes, ids); a failed check exits non-zero. Nothing here repairs anything.
//
//	fundzimctl verify audit [-incremental]   recompute both audit chains (worker role: DATABASE_WORKER_URL, AUDIT_CHECKPOINT_KEY)
//	fundzimctl verify ledger                 run the ledger integrity checks (DATABASE_LEDGER_URL)
//	fundzimctl verify objects [-sample N]    open stored objects and check their SHA-256 (DATABASE_URL, storage, SSE-C key)

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/Fatifizo/fundzim/internal/app"
	"github.com/Fatifizo/fundzim/internal/platform/config"
)

func verify(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: fundzimctl verify audit [-incremental] | ledger | objects [-sample N]")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	out := json.NewEncoder(os.Stdout)
	out.SetIndent("", "  ")
	switch args[0] {
	case "audit":
		fs := flag.NewFlagSet("verify audit", flag.ContinueOnError)
		incremental := fs.Bool("incremental", false, "verify from the last checkpoint instead of recomputing everything")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		cfg, err := config.FromEnv()
		if err != nil {
			return err
		}
		wcfg, err := app.LoadWorkerConfig(os.LookupEnv, cfg.App.Env)
		if err != nil {
			return err
		}
		res, err := app.VerifyAuditChains(ctx, wcfg, !*incremental)
		if err != nil {
			return err
		}
		_ = out.Encode(res)
		for _, r := range res {
			if !r.Intact() {
				return errors.New("audit chain verification found problems")
			}
		}
		return nil
	case "ledger":
		cfg, err := config.FromEnv()
		if err != nil {
			return err
		}
		rep, err := app.VerifyLedger(ctx, cfg, logger)
		if err != nil {
			return err
		}
		_ = out.Encode(rep)
		if !rep.Passed {
			return errors.New("ledger integrity check FAILED")
		}
		return nil
	case "objects":
		fs := flag.NewFlagSet("verify objects", flag.ContinueOnError)
		sample := fs.Int("sample", 200, "objects to verify (0: all)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		cfg, err := config.FromEnv()
		if err != nil {
			return err
		}
		rep, err := app.VerifyObjects(ctx, cfg, *sample, logger)
		if err != nil {
			return err
		}
		_ = out.Encode(rep)
		if rep.Failed() {
			return errors.New("stored objects failed verification")
		}
		return nil
	}
	return errors.New("usage: fundzimctl verify audit [-incremental] | ledger | objects [-sample N]")
}
