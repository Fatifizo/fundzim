//go:build integration

package integration

// Stage 18 failure injection: a migration that fails half-way leaves nothing behind — no schema object, no version row —
// because every goose SQL migration runs in one transaction. Uses its own version table so the real schema history is
// untouched, and removes that table at the end.

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

func TestSecurityFailedMigrationLeavesNothing(t *testing.T) {
	need(t, "DATABASE_MIGRATION_URL")
	sdb, err := sql.Open("pgx", os.Getenv("DATABASE_MIGRATION_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()
	const table = "stage18_failed_migration_drill"
	t.Cleanup(func() { _, _ = sdb.Exec(`DROP TABLE IF EXISTS public.` + table) })
	fsys := fstest.MapFS{
		"00001_ok.sql": {Data: []byte("-- +goose Up\nCREATE SCHEMA drill_ok_s18;\n-- +goose Down\nDROP SCHEMA drill_ok_s18;\n")},
		"00002_fails.sql": {Data: []byte("-- +goose Up\nCREATE TABLE drill_ok_s18.half_done (id int);\nINSERT INTO drill_ok_s18.half_done VALUES (1);\n" +
			"SELECT 1 / 0;\n-- +goose Down\nDROP TABLE drill_ok_s18.half_done;\n")},
	}
	p, err := goose.NewProvider(goose.DialectPostgres, sdb, fsys, goose.WithTableName(table))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = sdb.Exec(`DROP SCHEMA IF EXISTS drill_ok_s18 CASCADE`) })
	if _, err := p.Up(ctx(t)); err == nil {
		t.Fatal("a failing migration reported success")
	}
	v, err := p.GetDBVersion(ctx(t))
	if err != nil || v != 1 {
		t.Fatalf("version after the failure = %d (%v), want 1 (the failed migration is not recorded)", v, err)
	}
	var half bool
	if err := sdb.QueryRow(`SELECT to_regclass('drill_ok_s18.half_done') IS NOT NULL`).Scan(&half); err != nil || half {
		t.Fatalf("the failed migration left a table behind (%v)", err)
	}
	// the previous migration's effect is intact and the failed one can be retried after a fix
	var schema bool
	_ = sdb.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = 'drill_ok_s18')`).Scan(&schema)
	if !schema {
		t.Fatal("the successful migration before the failure was lost")
	}
}

// Concurrent deployments: with the session locker (as fundzimctl uses), two concurrent runs of the same migrations
// serialise — both succeed and the migration is applied exactly once.
func TestSecurityConcurrentMigrationsSerialise(t *testing.T) {
	need(t, "DATABASE_MIGRATION_URL")
	sdb, err := sql.Open("pgx", os.Getenv("DATABASE_MIGRATION_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()
	const table = "stage18_concurrent_migration_drill"
	t.Cleanup(func() {
		_, _ = sdb.Exec(`DROP TABLE IF EXISTS public.` + table)
		_, _ = sdb.Exec(`DROP TABLE IF EXISTS public.stage18_concurrent_applied`)
	})
	fsys := fstest.MapFS{"00001_once.sql": {Data: []byte("-- +goose Up\nCREATE TABLE public.stage18_concurrent_applied (id int);\n" +
		"INSERT INTO public.stage18_concurrent_applied VALUES (1);\nSELECT pg_sleep(1);\n-- +goose Down\nDROP TABLE public.stage18_concurrent_applied;\n")}}
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range errs {
		locker, err := lock.NewPostgresSessionLocker()
		if err != nil {
			t.Fatal(err)
		}
		p, err := goose.NewProvider(goose.DialectPostgres, sdb, fsys, goose.WithTableName(table), goose.WithSessionLocker(locker))
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(i int) { defer wg.Done(); _, errs[i] = p.Up(context.Background()) }(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("concurrent migrate: %v", err)
		}
	}
	var rows int
	if err := sdb.QueryRow(`SELECT count(*) FROM public.stage18_concurrent_applied`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("migration applied %d times (%v), want once", rows, err)
	}
}
