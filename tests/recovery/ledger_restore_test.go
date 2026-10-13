//go:build recovery

// Stage 10 backup/restore verification (docs/stage-10/financial-integrity-tests.md §5). Run after restoring a
// pg_dump of the development database into a SCRATCH database (never the development database itself):
//
//	FUNDZIM_RESTORE_LEDGER_URL=postgres://fundzim_ledger:…@127.0.0.1:5432/fundzim_restore \
//	FUNDZIM_SOURCE_LEDGER_URL=postgres://fundzim_ledger:…@127.0.0.1:5432/fundzim \
//	  go test -tags recovery -count=1 -v ./tests/recovery/
//
// It checks that the restored ledger is identical to the source up to the dump point (content hashes of journals
// and entries), that the integrity checker passes on the restore, that the trial balance balances, and that the
// database invariants (idempotency uniqueness, append-only, environment designation) survived the restore.
package recovery

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/ledger"
)

const journalsHash = `SELECT count(*), coalesce(md5(string_agg(concat_ws('|', id, posting_rule, posting_rule_version, environment, idempotency_key,
	currency, source_system, source_type, source_id, evidence_class, occurred_at, posted_at, accounting_period_id, reverses_transaction_id,
	adjustment_id, description, metadata::text), ',' ORDER BY id)), '') FROM ledger.ledger_transactions WHERE posted_at <= $1`

const entriesHash = `SELECT count(*), coalesce(md5(string_agg(concat_ws('|', e.id, e.transaction_id, e.line_no, e.account_id, e.direction,
	e.amount_minor, e.currency), ',' ORDER BY e.id)), '') FROM ledger.ledger_entries e JOIN ledger.ledger_transactions t ON t.id = e.transaction_id
	WHERE t.posted_at <= $1`

func TestLedgerRestoreVerification(t *testing.T) {
	restoreURL, sourceURL := os.Getenv("FUNDZIM_RESTORE_LEDGER_URL"), os.Getenv("FUNDZIM_SOURCE_LEDGER_URL")
	if restoreURL == "" || sourceURL == "" || !strings.Contains(restoreURL, "fundzim_restore") {
		t.Skip("set FUNDZIM_RESTORE_LEDGER_URL (scratch database fundzim_restore) and FUNDZIM_SOURCE_LEDGER_URL")
	}
	ctx := context.Background()
	restore, err := pgxpool.New(ctx, restoreURL)
	if err != nil {
		t.Fatal(err)
	}
	defer restore.Close()
	source, err := pgxpool.New(ctx, sourceURL)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()

	// 1. identical history up to the dump point
	var cutoff time.Time
	if err := restore.QueryRow(ctx, `SELECT coalesce(max(posted_at), '-infinity') FROM ledger.ledger_transactions`).Scan(&cutoff); err != nil {
		t.Fatal(err)
	}
	for name, q := range map[string]string{"journals": journalsHash, "entries": entriesHash} {
		var nr, ns int64
		var hr, hs string
		if err := restore.QueryRow(ctx, q, cutoff).Scan(&nr, &hr); err != nil {
			t.Fatal(err)
		}
		if err := source.QueryRow(ctx, q, cutoff).Scan(&ns, &hs); err != nil {
			t.Fatal(err)
		}
		t.Logf("%s up to %s: restored %d (md5 %s), source %d (md5 %s)", name, cutoff.UTC().Format(time.RFC3339Nano), nr, hr, ns, hs)
		if nr != ns || hr != hs || nr == 0 {
			t.Fatalf("%s differ after restore", name)
		}
	}

	// 2. the integrity checker and trial balance on the restored ledger
	svc := ledger.New(restore, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.Warm(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Fold(ctx); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.RunIntegrity(ctx, "SYSTEM", "restore-verification")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rep.Checks {
		t.Logf("%-4s %-8s checked=%d violations=%d", c.Code, c.Status, c.Checked, c.Violations)
	}
	if !rep.Passed || rep.Environment != ledger.EnvTest {
		t.Fatalf("integrity on the restore: passed=%v environment=%s", rep.Passed, rep.Environment)
	}
	tb, err := svc.TrialBalance(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range tb.Currencies {
		t.Logf("trial balance %s: debits %s credits %s balanced=%v", c.Currency, c.TotalDebit, c.TotalCredit, c.Balanced)
		if !c.Balanced {
			t.Fatalf("trial balance %s unbalanced after restore", c.Currency)
		}
	}

	// 3. invariants survived: a replayed idempotency key is refused, history is append-only
	var key string
	if err := restore.QueryRow(ctx, `SELECT idempotency_key FROM ledger.ledger_transactions ORDER BY posted_at LIMIT 1`).Scan(&key); err != nil {
		t.Fatal(err)
	}
	if _, err := restore.Exec(ctx, `UPDATE ledger.ledger_transactions SET description = 'tampered' WHERE idempotency_key = $1`, key); err == nil {
		t.Fatal("restored journals are mutable")
	}
	if _, err := restore.Exec(ctx, `DELETE FROM ledger.ledger_entries WHERE true`); err == nil {
		t.Fatal("restored entries are deletable")
	}
	var uniq bool
	if err := restore.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'uq_ledger_transactions_idempotency_key')`).Scan(&uniq); err != nil || !uniq {
		t.Fatalf("idempotency constraint missing after restore: %v", err)
	}
}
