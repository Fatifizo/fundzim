package db

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TxOptions configure WithTx.
type TxOptions struct {
	IsoLevel   pgx.TxIsoLevel // default READ COMMITTED (DATABASE §12)
	MaxRetries int            // retries on serialization failure / deadlock; default 3
}

type inTxKey struct{}

// InTx reports whether ctx belongs to code running inside WithTx. Outbound network calls must check it
// and refuse to run while a database transaction is open (DATABASE §12, background-processing BP-3).
func InTx(ctx context.Context) bool { v, _ := ctx.Value(inTxKey{}).(bool); return v }

// WithTx runs fn in one transaction, committing on nil and rolling back on error or panic. It retries
// the whole function on SQLSTATE 40001 (serialization failure) and 40P01 (deadlock) with jittered
// backoff; this is safe only because every operation wrapped this way is idempotent.
func WithTx(ctx context.Context, pool *pgxpool.Pool, opts TxOptions, fn func(ctx context.Context, tx pgx.Tx) error) error {
	if opts.MaxRetries == 0 {
		opts.MaxRetries = 3
	}
	if opts.IsoLevel == "" {
		opts.IsoLevel = pgx.ReadCommitted
	}
	var err error
	for attempt := 0; ; attempt++ {
		err = runOnce(ctx, pool, opts, fn)
		if err == nil || !retryable(err) || attempt >= opts.MaxRetries {
			return err
		}
		backoff := time.Duration(10*(1<<attempt))*time.Millisecond + time.Duration(rand.IntN(10))*time.Millisecond
		select {
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		case <-time.After(backoff):
		}
	}
}

func runOnce(ctx context.Context, pool *pgxpool.Pool, opts TxOptions, fn func(context.Context, pgx.Tx) error) (err error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: opts.IsoLevel})
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	if err = fn(context.WithValue(ctx, inTxKey{}, true), tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func retryable(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && (pe.Code == "40001" || pe.Code == "40P01")
}

// IsUniqueViolation reports whether err is a unique-constraint violation, optionally on a named constraint.
func IsUniqueViolation(err error, constraint string) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505" && (constraint == "" || pe.ConstraintName == constraint)
}
