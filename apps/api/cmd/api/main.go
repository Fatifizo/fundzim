// Command api runs the FundZim HTTP API (public listener + internal metrics/diagnostics listener).
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
		fmt.Fprintln(os.Stderr, "fundzim-api:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.FromEnv()
	if err != nil {
		return err // lists variable names only, never values
	}
	info := version.Get(cfg.App.Name)
	logger := logging.New(os.Stdout, logging.Options{Service: "fundzim-api", Env: string(cfg.App.Env),
		Version: info.Version, Level: cfg.Log.Level, Format: cfg.Log.Format})
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	deps, err := app.NewDeps(ctx, cfg, logger)
	if err != nil {
		return err
	}
	servers, _ := app.NewServers(deps)
	pubLn, err := net.Listen("tcp", cfg.HTTP.Addr())
	if err != nil {
		deps.Close()
		return fmt.Errorf("listen %s: %w", cfg.HTTP.Addr(), err)
	}
	intLn, err := net.Listen("tcp", cfg.HTTP.InternalAddr())
	if err != nil {
		_ = pubLn.Close()
		deps.Close()
		return fmt.Errorf("listen %s: %w", cfg.HTTP.InternalAddr(), err)
	}
	if err := app.Serve(ctx, deps, servers, pubLn, intLn); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
