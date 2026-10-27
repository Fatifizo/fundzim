//go:build integration

package integration

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	"github.com/Fatifizo/fundzim/internal/payouts"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/reconsweep"
	"github.com/Fatifizo/fundzim/migrations"
)

// Remediation phase 6 (F-R5-03): code and schema compatibility across the remediation migrations, on a scratch database
// (fundzim_perf_compat_*, created and dropped by the test as the local superuser; never the shared database). The
// statements of the earlier code are copied verbatim from their commits:
//   - phase 3 code (c8fb8c6): the checkpoint advance without evidence;
//   - phase 4 code (dfa91b6): Coverage's RUNNING read and RecordIntegrity.
// Opt-in: FUNDZIM_IT_MIGRATION_COMPAT=1 (it applies every migration to a new database, several minutes).

const (
	vPhase3 = 20261026120000 // run outcome columns
	vPhase4 = 20261027090000 // checkpoint evidence, integrity columns
	vPhase6 = 20261028090000 // integrity status reliability

	phase3Advance = `UPDATE app.payout_reconciliation_sweeps SET cursor_key = $2::uuid, examined = examined + 1, batches = batches + 1,
		version = version + 1, last_advanced_at = now() WHERE scope = $1 AND status = 'RUNNING'`
	phase4Coverage = `SELECT cycle, started_at, examined, deferred_since, last_advanced_at, last_attempt_at, last_attempt_outcome,
		last_attempt_error, failed_attempts, integrity_status, integrity_checked_at FROM app.payout_reconciliation_sweeps
		WHERE detector = $1 AND scope = $2 AND status = 'RUNNING'`
	phase4RecordIntegrity = `UPDATE app.payout_reconciliation_sweeps SET integrity_checked_at = now(), integrity_status = $3, integrity_detail = $4
		WHERE detector = $1 AND scope = $2 AND status = 'RUNNING'`
)

func compatDatabase(t *testing.T) (mig *sql.DB, app *pgxpool.Pool) {
	t.Helper()
	if os.Getenv("FUNDZIM_IT_MIGRATION_COMPAT") != "1" {
		t.Skip("opt-in: FUNDZIM_IT_MIGRATION_COMPAT=1 (creates a scratch database and applies every migration)")
	}
	need(t, "DATABASE_URL", "DATABASE_MIGRATION_URL", "POSTGRES_SUPERUSER_PASSWORD")
	su := superuser(t)
	name := "fundzim_perf_compat_" + ids.New()[24:]
	for _, q := range []string{`CREATE DATABASE ` + name + ` OWNER fundzim_migrator`, `REVOKE ALL ON DATABASE ` + name + ` FROM PUBLIC`,
		`GRANT CONNECT ON DATABASE ` + name + ` TO fundzim_app, fundzim_worker, fundzim_kyc, fundzim_compliance, fundzim_readonly, fundzim_ledger`,
		`ALTER DATABASE ` + name + ` SET timezone TO 'UTC'`} {
		if _, err := su.Exec(ctx(t), q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, err := su.Exec(c, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`); err != nil {
			t.Logf("drop %s: %v", name, err)
		}
	})
	withDB := func(raw, user, password string) string {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		u.Path = "/" + name
		if user != "" {
			u.User = url.UserPassword(user, password)
		}
		return u.String()
	}
	sp := pool(t, withDB(os.Getenv("DATABASE_MIGRATION_URL"), "postgres", os.Getenv("POSTGRES_SUPERUSER_PASSWORD")))
	for _, q := range []string{`ALTER SCHEMA public OWNER TO fundzim_migrator`, `REVOKE ALL ON SCHEMA public FROM PUBLIC`} {
		if _, err := sp.Exec(ctx(t), q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	sp.Close()
	mig, err := sql.Open("pgx", withDB(os.Getenv("DATABASE_MIGRATION_URL"), "", ""))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mig.Close() })
	app, err = db.Open(ctx(t), db.Options{URL: withDB(os.Getenv("DATABASE_URL"), "", ""), MaxConns: 4, ConnectTimeout: 5 * time.Second, AppName: "fundzim-it-compat"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	return mig, app
}

func pgCode(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

func TestReconciliationMigrationCompatibility(t *testing.T) {
	mig, app := compatDatabase(t)
	long, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	p, err := goose.NewProvider(goose.DialectPostgres, mig, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	upTo := func(v int64) {
		t.Helper()
		if _, err := p.UpTo(long, v); err != nil {
			t.Fatalf("up to %d: %v", v, err)
		}
	}
	scope := "it.compat." + ids.New()[24:]
	st := reconsweep.SQLStore{Pool: app, Table: payoutSweeps}
	newCoverage := func() error {
		_, err := st.Coverage(ctx(t), payouts.SweepDetector, scope)
		return err
	}
	phase4 := func(status string) error {
		if _, err := app.Exec(ctx(t), phase4RecordIntegrity, payouts.SweepDetector, scope, status, `{"compat":true}`); err != nil {
			return err
		}
		rows, err := app.Query(ctx(t), phase4Coverage, payouts.SweepDetector, scope)
		if err != nil {
			return err
		}
		rows.Close()
		return rows.Err()
	}

	// phase 3 schema: the phase 3 code advances freely; the phase 6 code cannot read its checkpoints
	upTo(vPhase3)
	if _, err := app.Exec(ctx(t), `INSERT INTO app.payout_reconciliation_sweeps (id, detector, scope, cycle, status, started_at)
		VALUES ($1, $2, $3, 1, 'RUNNING', now())`, ids.New(), payouts.SweepDetector, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Exec(ctx(t), phase3Advance, scope, ids.New()); err != nil {
		t.Fatalf("phase 3 code on the phase 3 schema: %v", err)
	}
	if err := newCoverage(); pgCode(err) != "42703" {
		t.Fatalf("phase 6 code on the phase 3 schema: want undefined column, got %v", err)
	}

	// phase 4 schema: the phase 3 advance (no evidence) is refused at commit — the F-R5-03 hazard: old code stops
	// advancing, it does not corrupt; the phase 4 code works; the phase 6 code cannot read
	upTo(vPhase4)
	if _, err := app.Exec(ctx(t), phase3Advance, scope, ids.New()); pgCode(err) != "23514" {
		t.Fatalf("phase 3 code on the phase 4 schema: want a check violation, got %v", err)
	}
	if err := phase4("VERIFIED"); err != nil {
		t.Fatalf("phase 4 code on the phase 4 schema: %v", err)
	}
	if err := newCoverage(); pgCode(err) != "42703" {
		t.Fatalf("phase 6 code on the phase 4 schema: want undefined column, got %v", err)
	}
	var v int64
	if err := app.QueryRow(ctx(t), `SELECT version FROM app.payout_reconciliation_sweeps WHERE scope = $1 AND status = 'RUNNING'`, scope).Scan(&v); err != nil || v != 1 {
		t.Fatalf("refused advance changed the checkpoint: version %d %v", v, err)
	}

	// phase 6 schema: the phase 4 code keeps working (old code on the new schema); the phase 3 advance stays refused;
	// the phase 6 code records every outcome
	upTo(vPhase6)
	if err := phase4("VERIFIED"); err != nil {
		t.Fatalf("phase 4 code on the phase 6 schema: %v", err)
	}
	if _, err := app.Exec(ctx(t), phase3Advance, scope, ids.New()); pgCode(err) != "23514" {
		t.Fatalf("phase 3 code on the phase 6 schema: want a check violation, got %v", err)
	}
	cov, err := st.Coverage(ctx(t), payouts.SweepDetector, scope)
	if err != nil {
		t.Fatalf("phase 6 code on the phase 6 schema: %v", err)
	}
	// written by the phase 4 code: VERIFIED without integrity_verified_at — read as verified at its checked_at
	if h := cov.Health(time.Now(), reconsweep.DefaultThresholds, 10000, 0); cov.IntegrityStatus != "VERIFIED" || h.IntegrityState != reconsweep.IntegrityStateVerified {
		t.Fatalf("a phase 4 VERIFIED read by the phase 6 code: %s / %s", cov.IntegrityStatus, h.IntegrityState)
	}
	// a fresh scope on the phase 6 schema: a cycle completes while the latest outcome is ERROR (the completed row keeps it)
	scope2 := "it.compat2." + ids.New()[24:]
	keys := []string{ids.New(), ids.New(), ids.New()}
	slices.Sort(keys)
	next := func(_ context.Context, after string, limit int) ([]string, error) {
		var out []string
		for _, k := range keys {
			if k > after && len(out) < limit {
				out = append(out, k)
			}
		}
		return out, nil
	}
	examine := func(context.Context, string) (reconsweep.Outcome, error) { return reconsweep.Outcome{}, nil }
	o := reconsweep.Options{Config: reconsweep.Config{Scope: scope2, BatchSize: 2, MaxRecords: 2, MaxDuration: time.Minute}, Detector: payouts.SweepDetector, RunID: "compat"}
	if _, err := reconsweep.Run(ctx(t), st, o, next, examine); err != nil {
		t.Fatal(err)
	}
	if r, _ := verifyScope(ctx(t), app, scope2, failingSource); r.Status != reconsweep.IntegrityError {
		t.Fatalf("phase 6 verify with a failing read: %s", r.Status)
	}
	for i := 0; i < 3; i++ {
		if _, err := reconsweep.Run(ctx(t), st, o, next, examine); err != nil {
			t.Fatal(err)
		}
	}
	var completedErr, runningErr string
	if err := app.QueryRow(ctx(t), `SELECT
		(SELECT integrity_status FROM app.payout_reconciliation_sweeps WHERE scope = $1 AND status = 'COMPLETED' AND cycle = 1),
		(SELECT integrity_status FROM app.payout_reconciliation_sweeps WHERE scope = $1 AND status = 'RUNNING')`, scope2).Scan(&completedErr, &runningErr); err != nil ||
		completedErr != "ERROR" || runningErr != "ERROR" {
		t.Fatalf("ERROR on the completed and the carried running cycle: %q %q %v", completedErr, runningErr, err)
	}

	// rollback to the phase 4 schema (development only; production is forward-only): the running ERROR becomes
	// UNVERIFIABLE, the completed (immutable) cycle keeps its ERROR under a NOT VALID check, the phase 4 code works
	// again and the phase 6 code cannot read
	if _, err := p.DownTo(long, vPhase4); err != nil {
		t.Fatalf("down to %d: %v", vPhase4, err)
	}
	if err := app.QueryRow(ctx(t), `SELECT
		(SELECT integrity_status FROM app.payout_reconciliation_sweeps WHERE scope = $1 AND status = 'COMPLETED' AND cycle = 1),
		(SELECT integrity_status FROM app.payout_reconciliation_sweeps WHERE scope = $1 AND status = 'RUNNING')`, scope2).Scan(&completedErr, &runningErr); err != nil ||
		completedErr != "ERROR" || runningErr != "UNVERIFIABLE" {
		t.Fatalf("after the rollback: completed %q, running %q %v", completedErr, runningErr, err)
	}
	if err := phase4("VERIFIED"); err != nil {
		t.Fatalf("phase 4 code after the rollback: %v", err)
	}
	if _, err := app.Exec(ctx(t), phase4RecordIntegrity, payouts.SweepDetector, scope2, "ERROR", `{}`); pgCode(err) != "23514" {
		t.Fatalf("ERROR after the rollback: want a check violation, got %v", err)
	}
	if err := newCoverage(); pgCode(err) != "42703" {
		t.Fatalf("phase 6 code after the rollback: want undefined column, got %v", err)
	}
	// and forward again
	if _, err := p.Up(long); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if err := newCoverage(); err != nil {
		t.Fatalf("phase 6 code after up again: %v", err)
	}
}
