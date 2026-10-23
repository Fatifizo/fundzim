//go:build integration

package integration

import (
	"context"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/platform/db"
)

// Stage 19 deterministic interleavings (docs/stage-19/concurrency-test-plan.md). A rowLock holds FOR UPDATE on ONE row
// the test itself created, in an open transaction: every writer of that row — in this process or in the compose stack —
// blocks exactly at that write until release. The test observes the interleaving through pg_blocking_pids, never by
// sleeping. Faults are injected only into backends blocked by the test's own lock (pg_terminate_backend through the
// local superuser), so nothing outside the test's records is disturbed.

type rowLock struct {
	t   *testing.T
	tx  pgx.Tx
	pid int
	obs *pgxpool.Pool
}

// lockRow locks one row (table must be a fixed identifier from the test, never input).
func lockRow(t *testing.T, mig *pgxpool.Pool, table, id string) *rowLock {
	t.Helper()
	tx, err := mig.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	if err := tx.QueryRow(ctx(t), `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := tx.QueryRow(ctx(t), `SELECT id::text FROM `+table+` WHERE id = $1 FOR UPDATE`, id).Scan(&got); err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatalf("lock %s %s: %v", table, id, err)
	}
	l := &rowLock{t: t, tx: tx, pid: pid, obs: mig}
	t.Cleanup(l.release)
	return l
}

// waiters lists the backends blocked by this lock — directly or queued behind another waiter for the same row
// (PostgreSQL queues later row-lock waiters behind the first one's tuple lock) — optionally of one application_name.
func (l *rowLock) waiters(appName string) []int {
	rows, err := l.obs.Query(context.Background(), `WITH RECURSIVE w(pid) AS (
			SELECT pid FROM pg_stat_activity WHERE $1 = ANY (pg_blocking_pids(pid))
			UNION SELECT a.pid FROM pg_stat_activity a JOIN w ON w.pid = ANY (pg_blocking_pids(a.pid)))
		SELECT w.pid FROM w JOIN pg_stat_activity a ON a.pid = w.pid WHERE $2 = '' OR a.application_name = $2 ORDER BY 1`, l.pid, appName)
	if err != nil {
		l.t.Fatal(err)
	}
	pids, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil {
		l.t.Fatal(err)
	}
	return pids
}

func (l *rowLock) release() {
	if l.tx != nil {
		_ = l.tx.Rollback(context.Background())
		l.tx = nil
	}
}

// namedPool opens a pool on the same database and role as url whose sessions carry appName (so a test can tell its
// own backends apart in pg_stat_activity).
func namedPool(t *testing.T, url, appName string) *pgxpool.Pool {
	t.Helper()
	p, err := db.Open(ctx(t), db.Options{URL: url, MaxConns: 4, ConnectTimeout: 5 * time.Second, AppName: appName})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// superuser connects as the local stack's postgres superuser (fault injection only: terminating a backend another
// role owns needs pg_signal_backend). The URL is derived from DATABASE_MIGRATION_URL; the password comes from .env.
func superuser(t *testing.T) *pgxpool.Pool {
	t.Helper()
	need(t, "DATABASE_MIGRATION_URL", "POSTGRES_SUPERUSER_PASSWORD")
	u, err := url.Parse(os.Getenv("DATABASE_MIGRATION_URL"))
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword("postgres", os.Getenv("POSTGRES_SUPERUSER_PASSWORD"))
	return namedPool(t, u.String(), "fundzim-it-s19-fault")
}

// terminate kills the given backends (connection loss as seen by their clients) and waits until they are gone.
func terminate(t *testing.T, su *pgxpool.Pool, pids []int) {
	t.Helper()
	for _, pid := range pids {
		var ok bool
		if err := su.QueryRow(ctx(t), `SELECT pg_terminate_backend($1, 5000)`, pid).Scan(&ok); err != nil || !ok {
			t.Fatalf("terminate backend %d: %v %v", pid, ok, err)
		}
	}
}
