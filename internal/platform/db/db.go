// Package db manages PostgreSQL connection pools (pgx v5). PostgreSQL is the only authoritative store.
package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Options configure a pool.
type Options struct {
	URL            string
	MaxConns       int32
	ConnectTimeout time.Duration
	AppName        string // shown in pg_stat_activity
}

// NewPool creates a pool without connecting. Connections are made lazily, so the API can start (and
// report not-ready) while PostgreSQL is still coming up. The URL never appears in returned errors.
func NewPool(ctx context.Context, o Options) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(o.URL)
	if err != nil {
		return nil, errors.New("db: invalid database URL")
	}
	if o.MaxConns > 0 {
		cfg.MaxConns = o.MaxConns
	}
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second
	cfg.ConnConfig.ConnectTimeout = o.ConnectTimeout
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["application_name"] = o.AppName
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: create pool: %w", err)
	}
	return pool, nil
}

// Open creates a pool and verifies connectivity within ConnectTimeout (used by tools such as
// fundzimctl that cannot do anything useful without the database).
func Open(ctx context.Context, o Options) (*pgxpool.Pool, error) {
	pool, err := NewPool(ctx, o)
	if err != nil {
		return nil, err
	}
	pctx, cancel := context.WithTimeout(ctx, o.ConnectTimeout)
	defer cancel()
	if err := pool.Ping(pctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: connect: %w", err)
	}
	return pool, nil
}

// Ping checks connectivity.
func Ping(ctx context.Context, pool *pgxpool.Pool) error {
	if err := pool.Ping(ctx); err != nil {
		return err
	}
	return nil
}

// SchemaVersion returns the highest applied goose migration version (0 if none or the table is absent).
func SchemaVersion(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var v int64
	err := pool.QueryRow(ctx, `SELECT coalesce(max(version_id), 0) FROM public.goose_db_version WHERE is_applied`).Scan(&v)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return v, nil
}

// CheckSchemaCurrent fails unless the database has at least the expected migration version.
func CheckSchemaCurrent(ctx context.Context, pool *pgxpool.Pool, expected int64) error {
	v, err := SchemaVersion(ctx, pool)
	if err != nil {
		return err
	}
	if v < expected {
		return fmt.Errorf("db: schema version %d is behind expected %d", v, expected)
	}
	return nil
}
