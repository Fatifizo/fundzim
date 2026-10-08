// Package app is FundZim's composition root: it wires configuration, dependencies, the HTTP router and
// servers. Nothing imports it except the cmd entrypoints and tests.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/platform/cache"
	"github.com/Fatifizo/fundzim/internal/platform/config"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/health"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/metrics"
	"github.com/Fatifizo/fundzim/internal/platform/storage"
	"github.com/Fatifizo/fundzim/internal/platform/version"
	"github.com/Fatifizo/fundzim/migrations"
)

// Deps are the long-lived dependencies of the API process. Optional dependencies are nil when not
// configured.
type Deps struct {
	Config  config.Config
	Logger  *slog.Logger
	DB      *pgxpool.Pool
	Redis   *cache.Client
	Storage *storage.Client
	Metrics *metrics.Metrics
	Version version.Info
	// ExpectedSchemaVersion is the migration version this binary requires (defaults to the embedded one).
	ExpectedSchemaVersion int64
	// Now is the clock used by rate limiting (injected for tests).
	Now func() time.Time
}

// NewDeps connects the dependencies described by cfg. The database pool is lazy: a database that is
// down at startup makes the service not-ready instead of crashing it. A required Redis that is
// unreachable is a startup error.
func NewDeps(ctx context.Context, cfg config.Config, logger *slog.Logger) (*Deps, error) {
	info := version.Get(cfg.App.Name)
	if cfg.App.Version != "" {
		info.Version = cfg.App.Version
	}
	d := &Deps{Config: cfg, Logger: logger, Version: info, Metrics: metrics.New(info.Version, info.Commit),
		ExpectedSchemaVersion: migrations.ExpectedVersion(), Now: time.Now}

	pool, err := db.NewPool(ctx, db.Options{URL: cfg.Database.URL.Reveal(), MaxConns: cfg.Database.MaxConns,
		ConnectTimeout: cfg.Database.ConnectTimeout, AppName: "fundzim-api"})
	if err != nil {
		return nil, err
	}
	d.DB = pool

	if cfg.Redis.Enabled() {
		rc, err := cache.Open(cfg.Redis.URL.Reveal())
		if err != nil {
			d.Close()
			return nil, err
		}
		d.Redis = rc
		pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err = rc.Ping(pctx)
		cancel()
		if err != nil {
			if cfg.Redis.Required {
				d.Close()
				return nil, fmt.Errorf("redis required but unreachable: %w", err)
			}
			logger.Warn("redis unavailable at startup; continuing without it", slog.String("error_category", "dependency"))
		}
	}

	if cfg.Storage.Enabled() {
		creds := map[storage.Class]storage.Credential{}
		add := func(class storage.Class, c config.StorageCredential) {
			creds[class] = storage.Credential{Bucket: c.Bucket, AccessKeyID: c.AccessKeyID.Reveal(), SecretAccessKey: c.SecretAccessKey.Reveal()}
		}
		add(storage.PublicCampaignMedia, cfg.Storage.Public)
		add(storage.PrivateIdentityDocuments, cfg.Storage.KYC)
		add(storage.PrivateComplianceDocuments, cfg.Storage.Evidence)
		sc, err := storage.New(storage.Options{Endpoint: cfg.Storage.Endpoint, Region: cfg.Storage.Region,
			ForcePathStyle: cfg.Storage.ForcePathStyle, Credentials: creds})
		if err != nil {
			d.Close()
			return nil, err
		}
		d.Storage = sc
	}
	return d, nil
}

// Close releases dependency resources.
func (d *Deps) Close() {
	if d.DB != nil {
		d.DB.Close()
	}
	if d.Redis != nil {
		_ = d.Redis.Close()
	}
}

// Checker builds the readiness checks. The database and the schema version are critical; storage is
// critical when configured; Redis is critical only when REDIS_REQUIRED=true.
func (d *Deps) Checker() *health.Checker {
	checks := []health.Check{}
	if d.DB != nil {
		checks = append(checks,
			health.Check{Name: "database", Critical: true, Timeout: 2 * time.Second,
				Run: func(ctx context.Context) error { return db.Ping(ctx, d.DB) }},
			health.Check{Name: "migrations", Critical: true, Timeout: 2 * time.Second,
				Run: func(ctx context.Context) error { return db.CheckSchemaCurrent(ctx, d.DB, d.ExpectedSchemaVersion) }},
		)
	}
	if d.Redis != nil {
		checks = append(checks, health.Check{Name: "redis", Critical: d.Config.Redis.Required, Timeout: time.Second,
			Run: d.Redis.Ping})
	}
	if d.Storage != nil {
		for _, class := range []storage.Class{storage.PublicCampaignMedia, storage.PrivateIdentityDocuments, storage.PrivateComplianceDocuments} {
			checks = append(checks, health.Check{Name: "storage_" + string(class), Critical: true, Timeout: 3 * time.Second,
				Run: func(ctx context.Context) error { return d.Storage.Check(ctx, class) }})
		}
	}
	// Public probes reuse a result for one second; the internal endpoint always runs fresh checks.
	return health.NewChecker(checks...).WithCache(time.Second, d.Now)
}

// Servers are the public API server and the internal (metrics/diagnostics) server.
type Servers struct {
	Public   *http.Server
	Internal *http.Server
}

// NewServers builds both HTTP servers with hardened timeouts.
func NewServers(d *Deps) (*Servers, *httpx.Router) {
	checker := d.Checker()
	router := NewRouter(d, checker)
	pub := &http.Server{
		Addr:              d.Config.HTTP.Addr(),
		Handler:           PublicHandler(d, router),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      d.Config.HTTP.RequestTimeout + 5*time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(d.Logger.Handler(), slog.LevelWarn),
	}
	internal := &http.Server{
		Addr:              d.Config.HTTP.InternalAddr(),
		Handler:           InternalHandler(d, checker),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(d.Logger.Handler(), slog.LevelWarn),
	}
	return &Servers{Public: pub, Internal: internal}, router
}

// Serve runs both servers on the given listeners until ctx is cancelled, then shuts down gracefully
// within the configured shutdown timeout: stop accepting, finish in-flight requests, close dependencies.
func Serve(ctx context.Context, d *Deps, s *Servers, pubLn, intLn net.Listener) error {
	errc := make(chan error, 2)
	go func() { errc <- serve(s.Public, pubLn) }()
	go func() { errc <- serve(s.Internal, intLn) }()
	d.Logger.Info("api started", slog.String("addr", pubLn.Addr().String()),
		slog.String("internal_addr", intLn.Addr().String()), slog.String("app_version", d.Version.Version))

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-errc:
	}
	d.Logger.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), d.Config.HTTP.ShutdownTimeout)
	defer cancel()
	e1 := s.Public.Shutdown(sctx)
	e2 := s.Internal.Shutdown(sctx)
	d.Close()
	d.Logger.Info("shutdown complete")
	return errors.Join(runErr, e1, e2)
}

func serve(srv *http.Server, ln net.Listener) error {
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
