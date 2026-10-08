// Command worker runs FundZim's background worker: the River job queue (schema `queue`), the outbox
// relay and delivery jobs, notifications and maintenance jobs. Its internal listener
// (WORKER_INTERNAL_HTTP_PORT, default 9091) serves /healthz, /readyz and /metrics only.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/Fatifizo/fundzim/internal/app"
	"github.com/Fatifizo/fundzim/internal/platform/config"
	"github.com/Fatifizo/fundzim/internal/platform/logging"
	"github.com/Fatifizo/fundzim/internal/platform/version"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fundzim-worker:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.FromEnv()
	if err != nil {
		return err // lists variable names only, never values
	}
	wcfg, err := app.LoadWorkerConfig(os.LookupEnv, cfg.App.Env)
	if err != nil {
		return err
	}
	info := version.Get(cfg.App.Name)
	logger := logging.New(os.Stdout, logging.Options{Service: "fundzim-worker", Env: string(cfg.App.Env),
		Version: info.Version, Level: cfg.Log.Level, Format: cfg.Log.Format})
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	deps, err := app.NewWorkerDeps(ctx, cfg, wcfg, logger)
	if err != nil {
		return err
	}
	addr := app.WorkerInternalAddr(deps)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		deps.Close()
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	if err := app.RunWorker(ctx, deps, ln); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
