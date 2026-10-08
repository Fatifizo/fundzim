// Command fundzimctl is the FundZim operator CLI.
//
//	fundzimctl config check          validate configuration (prints variable names only)
//	fundzimctl migrate up            apply all pending migrations (DATABASE_MIGRATION_URL, role fundzim_migrator)
//	fundzimctl migrate status        list migrations and whether they are applied
//	fundzimctl migrate version       print the applied and the embedded (expected) version
//	fundzimctl migrate down          roll back ONE migration — development and test only
//	fundzimctl version               print build information
//	fundzimctl healthcheck [url]     exit 0 if url (default http://127.0.0.1:8080/healthz) answers 200;
//	                                 used as the container HEALTHCHECK (the runtime image has no shell or curl)
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx" for goose
	"github.com/pressly/goose/v3"

	"github.com/Fatifizo/fundzim/internal/platform/config"
	"github.com/Fatifizo/fundzim/internal/platform/version"
	"github.com/Fatifizo/fundzim/migrations"
)

const usage = `usage: fundzimctl <command>

  config check       validate configuration from the environment
  migrate up         apply all pending migrations
  migrate status     show migration status
  migrate version    show applied and expected schema versions
  migrate down       roll back the most recent migration (development/test only)
  version            show build information
  healthcheck [url]  probe a health URL (container health checks)`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "fundzimctl:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch args[0] {
	case "version":
		info := version.Get("FundZim")
		fmt.Printf("%s %s (commit %s, built %s)\n", info.Name, info.Version, info.Commit, info.Build)
		return nil
	case "healthcheck":
		url := "http://127.0.0.1:8080/healthz"
		if len(args) == 2 {
			url = args[1]
		}
		return healthcheck(url)
	case "config":
		if len(args) != 2 || args[1] != "check" {
			return errors.New(usage)
		}
		if _, err := config.FromEnv(); err != nil {
			return err
		}
		fmt.Println("configuration OK")
		return nil
	case "migrate":
		if len(args) != 2 {
			return errors.New(usage)
		}
		return migrate(args[1])
	default:
		return errors.New(usage)
	}
}

func healthcheck(url string) error {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("unhealthy: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: status %d", resp.StatusCode)
	}
	return nil
}

func migrate(cmd string) error {
	env := config.Env(os.Getenv("APP_ENV"))
	url := os.Getenv("DATABASE_MIGRATION_URL")
	if url == "" {
		return errors.New("DATABASE_MIGRATION_URL is required (connects as fundzim_migrator)")
	}
	if cmd == "down" && !env.IsLocal() {
		return errors.New("migrate down is allowed only when APP_ENV is development or test; production is forward-only (ADR-028)")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		return errors.New("invalid DATABASE_MIGRATION_URL")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("cannot connect to the database: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		return err
	}
	switch cmd {
	case "up":
		res, err := p.Up(ctx)
		for _, r := range res {
			fmt.Printf("applied %s (%s)\n", r.Source.Path, r.Duration.Round(time.Millisecond))
		}
		if err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
		if len(res) == 0 {
			fmt.Println("no pending migrations")
		}
	case "down":
		r, err := p.Down(ctx)
		if err != nil {
			return fmt.Errorf("rollback failed: %w", err)
		}
		fmt.Printf("rolled back %s\n", r.Source.Path)
	case "status":
		st, err := p.Status(ctx)
		if err != nil {
			return err
		}
		for _, s := range st {
			applied := "pending"
			if s.State == goose.StateApplied {
				applied = "applied " + s.AppliedAt.UTC().Format(time.RFC3339)
			}
			fmt.Printf("%-50s %s\n", s.Source.Path, applied)
		}
	case "version":
		v, err := p.GetDBVersion(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("database version %d, expected %d\n", v, migrations.ExpectedVersion())
		if v < migrations.ExpectedVersion() {
			return errors.New("database schema is behind this binary")
		}
	default:
		return errors.New(usage)
	}
	return nil
}
