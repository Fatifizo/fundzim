//go:build integration

package integration

// Stage 10 ledger (ADR-042) against the real database: synthetic TEST_ONLY fixtures and balances, database invariants
// under the fundzim_ledger role (direct SQL bypassing Go), role separation (fundzim_app has no ledger access),
// idempotency under 100 concurrent duplicates, maker-checker reversals (incl. concurrent approvals), account freeze
// and period close racing postings, reconciliation with injected payment facts, the payments consumer (test payments
// never post), failure injection (rollback, killed connection, lock timeout, outbox gateway refusal), integrity
// checker incl. detected drift, and the staff API with RBAC and step-up.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/ledger"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/config"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

type ledgerIT struct {
	s   *itServer
	f   *verifStaff
	svc *ledger.Service
	lp  *pgxpool.Pool // fundzim_ledger
	app *pgxpool.Pool // fundzim_app
	mig *pgxpool.Pool // fundzim_migrator
}

func newLedgerIT(t *testing.T) *ledgerIT {
	t.Helper()
	need(t, "DATABASE_LEDGER_URL", "DATABASE_MIGRATION_URL", "DATABASE_URL")
	s := newITServer(t, func(c *config.Config) { c.Ledger.SyntheticFixturesEnabled = true })
	l := &ledgerIT{s: s, f: staffFixture(t, s), svc: s.d.Verification.Ledger, lp: pool(t, os.Getenv("DATABASE_LEDGER_URL")),
		app: pool(t, os.Getenv("DATABASE_URL")), mig: pool(t, os.Getenv("DATABASE_MIGRATION_URL"))}
	if err := l.svc.EnsureCurrentPeriods(ctx(t)); err != nil {
		t.Fatal(err)
	}
	return l
}

func ecode(err error) string {
	if e := errs.As(err); e != nil {
		return e.Code
	}
	if err == nil {
		return ""
	}
	return err.Error()
}

func (l *ledgerIT) balance(t *testing.T, code string) int64 {
	t.Helper()
	var b int64
	if err := l.lp.QueryRow(ctx(t), `SELECT c.balance_minor FROM ledger.ledger_accounts a JOIN ledger.v_balances_current c ON c.account_id = a.id
		WHERE a.code = $1 AND a.currency = 'USD'`, code).Scan(&b); err != nil {
		t.Fatalf("balance %s: %v", code, err)
	}
	return b
}

func (l *ledgerIT) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := l.lp.QueryRow(ctx(t), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// synthetic posting helper (TEST ledger)
func synth(rule, key, srcType, srcID string, lines ...ledger.Line) ledger.Posting {
	return ledger.Posting{Rule: rule, IdempotencyKey: key, Currency: "USD", OccurredAt: time.Now().UTC(), Description: "TEST_ONLY integration " + rule,
		SourceSystem: ledger.SourceSynthetic, SourceType: srcType, SourceID: srcID, SourceEventID: key, EvidenceClass: ledger.EvidenceSimulated,
		Actor: ledger.Actor{Type: "SYSTEM", ID: "integration"}, Metadata: map[string]string{"fixture": "TEST_ONLY"}, Lines: lines}
}

func acct(class, owner string) ledger.AccountRef {
	return ledger.AccountRef{Class: class, OwnerID: owner}
}

// ===== fixtures, balances, trial balance, idempotent replay ========================================================

func TestLedgerSyntheticFixturesBalancesAndTrialBalance(t *testing.T) {
	l := newLedgerIT(t)
	runs := map[string]ledger.FixtureRun{}
	for _, sc := range ledger.Fixtures {
		run, err := l.svc.PostFixture(ctx(t), l.f.ledgerAdmin.id, sc, "")
		if err != nil {
			t.Fatalf("%s: %v", sc, err)
		}
		runs[sc] = run
	}
	prov := ledger.SyntheticProviderID
	_ = prov
	// donations: 10.00 + 25.00 captured, 0.50 provider fee
	c := runs[ledger.FixtureDonations].CampaignID
	if got := l.balance(t, "liability:campaign_unsettled:"+c); got != 3450 {
		t.Fatalf("donations: campaign unsettled %d, want 3450", got)
	}
	// partial refund: 100.00 − 30.00 = 70.00 remains; refund payable back to 0
	c = runs[ledger.FixturePartialRefund].CampaignID
	if got := l.balance(t, "liability:campaign_unsettled:"+c); got != 7000 {
		t.Fatalf("partial refund: unsettled %d, want 7000", got)
	}
	// settlement: 100.00 settled and released to available
	c = runs[ledger.FixtureSettlement].CampaignID
	if l.balance(t, "liability:campaign_payable:"+c) != 10000 || l.balance(t, "liability:campaign_unsettled:"+c) != 0 {
		t.Fatal("settlement: payable/unsettled wrong")
	}
	// every fixture journal is TEST, SIMULATED, synthetic and labelled TEST_ONLY
	for _, run := range runs {
		for _, id := range run.Journals {
			if l.count(t, `SELECT count(*) FROM ledger.ledger_transactions WHERE id = $1 AND environment = 'TEST' AND evidence_class = 'SIMULATED'
				AND source_system = 'synthetic_fixture' AND metadata->>'fixture' = 'TEST_ONLY' AND description LIKE 'TEST_ONLY%'`, id) != 1 {
				t.Fatalf("journal %s is not labelled TEST_ONLY", id)
			}
		}
	}
	// replaying a run with the same run id posts nothing new
	before := l.count(t, `SELECT count(*) FROM ledger.ledger_transactions`)
	again, err := l.svc.PostFixture(ctx(t), l.f.ledgerAdmin.id, ledger.FixtureDonations, runs[ledger.FixtureDonations].RunID)
	if err != nil {
		t.Fatal(err)
	}
	if l.count(t, `SELECT count(*) FROM ledger.ledger_transactions`) != before ||
		strings.Join(again.Journals, ",") != strings.Join(runs[ledger.FixtureDonations].Journals, ",") {
		t.Fatal("idempotent replay changed the ledger")
	}
	// trial balance balances and equals an independent aggregation of the entries
	tb, err := l.svc.TrialBalance(ctx(t), "USD", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(tb.Currencies) != 1 || !tb.Currencies[0].Balanced || tb.Environment != "TEST" {
		t.Fatalf("trial balance: %+v", tb.Currencies)
	}
	var dr, cr int64
	if err := l.mig.QueryRow(ctx(t), `SELECT sum(amount_minor) FILTER (WHERE direction = 'DEBIT'), sum(amount_minor) FILTER (WHERE direction = 'CREDIT')
		FROM ledger.ledger_entries WHERE currency = 'USD'`).Scan(&dr, &cr); err != nil {
		t.Fatal(err)
	}
	if dr != cr {
		t.Fatalf("independent aggregation unbalanced: %d %d", dr, cr)
	}
	// net trial balance totals = sum of per-account positive nets (independently)
	var netDr int64
	if err := l.mig.QueryRow(ctx(t), `SELECT coalesce(sum(GREATEST(n, 0)), 0) FROM (SELECT sum(CASE direction WHEN 'DEBIT' THEN amount_minor ELSE -amount_minor END) n
		FROM ledger.ledger_entries WHERE currency = 'USD' GROUP BY account_id) x`).Scan(&netDr); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(netDr) != tb.Currencies[0].TotalDebit {
		t.Fatalf("trial balance debit %s, independent %d", tb.Currencies[0].TotalDebit, netDr)
	}
	// fold and drift: projections equal a recompute
	if _, err := l.svc.Fold(ctx(t)); err != nil {
		t.Fatal(err)
	}
	if n := l.count(t, `SELECT count(*) FROM ledger.v_balance_drift`); n != 0 {
		t.Fatalf("balance drift rows: %d", n)
	}
	rep, err := l.svc.RunIntegrity(ctx(t), "SYSTEM", "integration")
	if err != nil || !rep.Passed {
		t.Fatalf("integrity: %v %+v", err, rep.Checks)
	}
}

// ===== database invariants under the ledger role (direct SQL, bypassing Go) ==========================================

func TestLedgerDatabaseInvariantsDirectSQL(t *testing.T) {
	l := newLedgerIT(t)
	run, err := l.svc.PostFixture(ctx(t), "", ledger.FixtureSettlement, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.svc.PostFixture(ctx(t), "", ledger.FixturePartialRefund, ""); err != nil { // creates liability:refund_payable
		t.Fatal(err)
	}
	campaign := run.CampaignID
	var refundPayableID string
	var payableID, unsettledID, clearingID, journalID, entryID string
	q := func(sql string, dst *string, args ...any) {
		t.Helper()
		if err := l.lp.QueryRow(ctx(t), sql, args...).Scan(dst); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	q(`SELECT id FROM ledger.ledger_accounts WHERE code = $1`, &payableID, "liability:campaign_payable:"+campaign)
	q(`SELECT id FROM ledger.ledger_accounts WHERE code = $1`, &unsettledID, "liability:campaign_unsettled:"+campaign)
	q(`SELECT id FROM ledger.ledger_accounts WHERE code = $1`, &clearingID, "asset:psp_clearing:"+ledger.SyntheticProviderID)
	q(`SELECT id FROM ledger.ledger_accounts WHERE code = 'liability:refund_payable' AND currency = 'USD'`, &refundPayableID)
	journalID = run.Journals[0]
	q(`SELECT id FROM ledger.ledger_entries WHERE transaction_id = $1 LIMIT 1`, &entryID, journalID)

	// a journal inserted directly; lines given as (account, direction, amount); returns the commit error
	direct := func(env, sourceSystem, evidence, rule string, lines [][3]any) error {
		srcType := "payment"
		if rule == "REFUND_RESERVED" {
			srcType = "refund"
		}
		tx, err := l.lp.Begin(ctx(t))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		id := ids.New()
		if _, err := tx.Exec(ctx(t), `INSERT INTO ledger.ledger_transactions (id, posting_rule, posting_rule_version, environment, source_system,
			evidence_class, idempotency_key, currency, description, source_type, source_id, occurred_at, accounting_period_id, created_by_type,
			created_by_id) VALUES ($1, $2, 1, $3, $4, $5, $6, 'USD', 'direct SQL test', $9, $7, now(), $8, 'SYSTEM', 'sql')`,
			id, rule, env, sourceSystem, evidence, "direct:"+id, ids.New(), ids.New(), srcType); err != nil {
			return err
		}
		for i, ln := range lines {
			if _, err := tx.Exec(ctx(t), `INSERT INTO ledger.ledger_entries (id, transaction_id, line_no, account_id, direction, amount_minor, currency)
				VALUES ($1, $2, $3, $4, $5, $6, 'USD')`, ids.New(), id, i+1, ln[0], ln[1], ln[2]); err != nil {
				return err
			}
		}
		return tx.Commit(ctx(t))
	}
	mustFail := func(name, want string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: want %q, got %v", name, want, err)
		}
	}
	mustFail("unbalanced", "unbalanced", direct("TEST", "synthetic_fixture", "SIMULATED", "DONATION_CAPTURED",
		[][3]any{{clearingID, "DEBIT", 100}, {unsettledID, "CREDIT", 99}}))
	mustFail("single line", "at least 2 required", direct("TEST", "synthetic_fixture", "SIMULATED", "DONATION_CAPTURED",
		[][3]any{{clearingID, "DEBIT", 100}}))
	mustFail("negative amount", "ck_ledger_entries_amount_positive", direct("TEST", "synthetic_fixture", "SIMULATED", "DONATION_CAPTURED",
		[][3]any{{clearingID, "DEBIT", -100}, {unsettledID, "CREDIT", -100}}))
	mustFail("line not allowed by rule", "does not allow", direct("TEST", "synthetic_fixture", "SIMULATED", "DONATION_CAPTURED",
		[][3]any{{clearingID, "DEBIT", 100}, {payableID, "CREDIT", 100}}))
	// refused by the environment designation trigger first (the Stage 10 CHECK is the second line)
	mustFail("production journal", "differs from this database's ledger environment TEST", direct("PRODUCTION", "payments", "PROVIDER_VERIFIED",
		"DONATION_CAPTURED", [][3]any{{clearingID, "DEBIT", 100}, {unsettledID, "CREDIT", 100}}))
	mustFail("payments-sourced journal in a TEST ledger", "ck_ledger_transactions_no_payment_postings_in_test", direct("TEST", "payments",
		"PROVIDER_REPORTED", "DONATION_CAPTURED", [][3]any{{clearingID, "DEBIT", 100}, {unsettledID, "CREDIT", 100}}))
	mustFail("synthetic with real evidence", "ck_ledger_transactions_synthetic_is_test", direct("TEST", "synthetic_fixture", "PROVIDER_VERIFIED",
		"DONATION_CAPTURED", [][3]any{{clearingID, "DEBIT", 100}, {unsettledID, "CREDIT", 100}}))
	mustFail("overdraw a non-negative account", "ck_ledger_balances_non_negative", direct("TEST", "synthetic_fixture", "SIMULATED",
		"REFUND_RESERVED", [][3]any{{payableID, "DEBIT", 10001}, {refundPayableID, "CREDIT", 10001}}))
	mustFail("reversal without approval", "requires an APPROVED adjustment", func() error {
		_, err := l.lp.Exec(ctx(t), `INSERT INTO ledger.ledger_transactions (id, posting_rule, posting_rule_version, environment, source_system,
			evidence_class, idempotency_key, currency, description, source_type, source_id, occurred_at, accounting_period_id, created_by_type,
			created_by_id, reverses_transaction_id, reason) VALUES ($1, 'REVERSAL', 1, 'TEST', 'staff', 'MANUALLY_REVIEWED', $2, 'USD', 'x',
			'adjustment', $3, now(), $3, 'SYSTEM', 'sql', $4, 'unapproved reversal attempt')`, ids.New(), "reversal:"+journalID, ids.New(), journalID)
		return err
	}())
	// immutability
	for name, sql := range map[string]string{
		"update journal": `UPDATE ledger.ledger_transactions SET description = 'tampered' WHERE id = $1`,
		"delete journal": `DELETE FROM ledger.ledger_transactions WHERE id = $1`,
	} {
		_, err := l.lp.Exec(ctx(t), sql, journalID)
		mustFail(name, "", err)
	}
	for name, sql := range map[string]string{
		"update line": `UPDATE ledger.ledger_entries SET amount_minor = 1 WHERE id = $1`,
		"delete line": `DELETE FROM ledger.ledger_entries WHERE id = $1`,
	} {
		_, err := l.lp.Exec(ctx(t), sql, entryID)
		mustFail(name, "", err)
	}
	// a sealed journal cannot gain lines later (L11)
	_, err = l.lp.Exec(ctx(t), `INSERT INTO ledger.ledger_entries (id, transaction_id, line_no, account_id, direction, amount_minor, currency)
		VALUES ($1, $2, 99, $3, 'DEBIT', 1, 'USD')`, ids.New(), journalID, clearingID)
	mustFail("late line", "sealed", err)
	// duplicate idempotency key
	var key string
	q(`SELECT idempotency_key FROM ledger.ledger_transactions WHERE id = $1`, &key, journalID)
	_, err = l.lp.Exec(ctx(t), `INSERT INTO ledger.ledger_transactions (id, posting_rule, posting_rule_version, environment, source_system, evidence_class,
		idempotency_key, currency, description, source_type, source_id, occurred_at, accounting_period_id, created_by_type, created_by_id)
		VALUES ($1, 'DONATION_CAPTURED', 1, 'TEST', 'synthetic_fixture', 'SIMULATED', $2, 'USD', 'dup', 'payment', $3, now(), $3, 'SYSTEM', 'sql')`,
		ids.New(), key, ids.New())
	mustFail("duplicate key", "uq_ledger_transactions_idempotency_key", err)
	// unverified currency: no ZWG account can exist
	_, err = l.lp.Exec(ctx(t), `INSERT INTO ledger.ledger_accounts (id, code, account_class, type, normal_balance, kind, currency, owner_type, environment)
		VALUES ($1, 'revenue:platform_fees', 'revenue:platform_fees', 'REVENUE', 'CREDIT', 'RESULT', 'ZWG', 'PLATFORM', 'TEST')`, ids.New())
	mustFail("ZWG account", "unverified minor units", err)
	// direct balance update, even with the maintenance flag set by the runtime role
	tx, _ := l.lp.Begin(ctx(t))
	_, _ = tx.Exec(ctx(t), `SELECT set_config('ledger.projection_maintenance', 'on', true)`)
	_, err = tx.Exec(ctx(t), `UPDATE ledger.ledger_balances SET balance_minor = balance_minor + 1, credit_total_minor = credit_total_minor + 1
		WHERE account_id = $1`, payableID)
	_ = tx.Rollback(ctx(t))
	mustFail("direct balance update", "projection maintained only by ledger postings", err)
	// environment designation cannot change and cannot be PRODUCTION
	_, err = l.mig.Exec(ctx(t), `UPDATE ledger.ledger_environment SET environment = 'PRODUCTION'`)
	mustFail("re-designate environment", "", err)
	_, err = l.mig.Exec(ctx(t), `INSERT INTO ledger.ledger_environment (singleton, environment, designated_by, note) VALUES (false, 'PRODUCTION', 'x', 'x')`)
	mustFail("second designation", "", err)
	// no deletes or truncates for the ledger role at all
	for _, sql := range []string{`TRUNCATE ledger.ledger_entries`, `DELETE FROM ledger.ledger_accounts WHERE false`} {
		_, err := l.lp.Exec(ctx(t), sql)
		mustFail(sql, "permission denied", err)
	}
}

// ===== role separation ============================================================================================

func TestLedgerApplicationRoleHasNoLedgerAccess(t *testing.T) {
	l := newLedgerIT(t)
	for _, sql := range []string{
		`SELECT count(*) FROM ledger.ledger_transactions`,
		`SELECT count(*) FROM ledger.ledger_entries`,
		`INSERT INTO ledger.ledger_periods (id, code, starts_at, ends_at, status) VALUES (gen_random_uuid(), 'x', now(), now() + interval '1 day', 'OPEN')`,
		`SELECT ledger.fold_deferred_balances()`,
	} {
		_, err := l.app.Exec(ctx(t), sql)
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("fundzim_app %q: %v", sql, err)
		}
	}
}

// ===== idempotency under concurrency ==============================================================================

func TestLedgerHundredConcurrentDuplicatePostingsYieldOneJournal(t *testing.T) {
	l := newLedgerIT(t)
	camp, src := ids.New(), ids.New()
	p := synth("DONATION_CAPTURED", "fixture:"+src+":dup", "payment", src,
		ledger.Dr(acct(ledger.ClassPSPClearing, ledger.SyntheticProviderID), 1234), ledger.Cr(acct(ledger.ClassCampaignUnsettled, camp), 1234))
	var wg sync.WaitGroup
	results := make([]string, 100)
	errsN := atomic.Int32{}
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := l.svc.PostOnce(context.Background(), p)
			if err != nil {
				errsN.Add(1)
				t.Errorf("post %d: %v", i, err)
				return
			}
			results[i] = r.TransactionID
		}(i)
	}
	wg.Wait()
	if errsN.Load() != 0 {
		t.Fatalf("%d postings failed", errsN.Load())
	}
	for _, r := range results {
		if r != results[0] {
			t.Fatal("concurrent duplicates returned different journals")
		}
	}
	if n := l.count(t, `SELECT count(*) FROM ledger.ledger_transactions WHERE idempotency_key = $1`, p.IdempotencyKey); n != 1 {
		t.Fatalf("journals for one key: %d", n)
	}
	if l.balance(t, "liability:campaign_unsettled:"+camp) != 1234 {
		t.Fatal("balance counted a duplicate")
	}
	// same key, different journal: conflict, nothing written
	q := p
	q.Lines = []ledger.Line{ledger.Dr(acct(ledger.ClassPSPClearing, ledger.SyntheticProviderID), 999), ledger.Cr(acct(ledger.ClassCampaignUnsettled, camp), 999)}
	if _, err := l.svc.PostOnce(ctx(t), q); ecode(err) != "LEDGER_IDEMPOTENCY_CONFLICT" {
		t.Fatalf("conflict: %v", err)
	}
}

// ===== reversals: maker-checker, double reversal, concurrent approvals ===================================================

func TestLedgerReversalsMakerCheckerAndNoDoubleReversal(t *testing.T) {
	l := newLedgerIT(t)
	camp, src := ids.New(), ids.New()
	res, err := l.svc.PostOnce(ctx(t), synth("DONATION_CAPTURED", "fixture:"+src+":rev", "payment", src,
		ledger.Dr(acct(ledger.ClassPSPClearing, ledger.SyntheticProviderID), 5000), ledger.Cr(acct(ledger.ClassCampaignUnsettled, camp), 5000)))
	if err != nil {
		t.Fatal(err)
	}
	maker, checker := l.f.finance1.id, l.f.finance2.id
	req := ledger.AdjustmentRequest{Kind: ledger.AdjReversal, Target: res.TransactionID, ReasonCode: "TEST_FIXTURE_CORRECTION",
		Reason: "integration test reversal of a synthetic capture"}
	a1, err := l.svc.RequestAdjustment(ctx(t), maker, req)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := l.svc.RequestAdjustment(ctx(t), maker, req) // a second request for the same target (still pending)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.svc.ApproveAdjustment(ctx(t), maker, a1.ID, "self approval must be refused"); ecode(err) != "LEDGER_SELF_APPROVAL_FORBIDDEN" {
		t.Fatalf("self approval: %v", err)
	}
	// both requests approved concurrently by the checker: exactly one reversal is posted
	var wg sync.WaitGroup
	var okN, failN atomic.Int32
	for _, id := range []string{a1.ID, a2.ID} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if _, err := l.svc.ApproveAdjustment(context.Background(), checker, id, "checker approves the reversal"); err == nil {
				okN.Add(1)
			} else {
				failN.Add(1)
			}
		}(id)
	}
	wg.Wait()
	if okN.Load() != 1 || failN.Load() != 1 {
		t.Fatalf("concurrent approvals: %d posted, %d failed", okN.Load(), failN.Load())
	}
	if n := l.count(t, `SELECT count(*) FROM ledger.ledger_transactions WHERE reverses_transaction_id = $1`, res.TransactionID); n != 1 {
		t.Fatalf("reversals of one journal: %d", n)
	}
	if l.balance(t, "liability:campaign_unsettled:"+camp) != 0 {
		t.Fatal("reversal did not cancel the original")
	}
	// the original is untouched; a further reversal request is refused; a reversal cannot be reversed
	if l.count(t, `SELECT count(*) FROM ledger.ledger_entries WHERE transaction_id = $1`, res.TransactionID) != 2 {
		t.Fatal("original journal changed")
	}
	if _, err := l.svc.RequestAdjustment(ctx(t), maker, req); ecode(err) != "LEDGER_ALREADY_REVERSED" {
		t.Fatalf("double reversal request: %v", err)
	}
	var revID string
	_ = l.lp.QueryRow(ctx(t), `SELECT id FROM ledger.ledger_transactions WHERE reverses_transaction_id = $1`, res.TransactionID).Scan(&revID)
	if _, err := l.svc.RequestAdjustment(ctx(t), maker, ledger.AdjustmentRequest{Kind: ledger.AdjReversal, Target: revID, ReasonCode: "OTHER",
		Reason: "reversal of a reversal must be refused"}); ecode(err) != "LEDGER_REVERSAL_OF_REVERSAL" {
		t.Fatalf("reversal of reversal: %v", err)
	}
	// audit trail
	for _, action := range []string{"ledger.adjustment.requested", "ledger.adjustment.approved", "ledger.journal.reversed"} {
		var n int
		if err := l.mig.QueryRow(ctx(t), `SELECT count(*) FROM audit.audit_events WHERE action = $1 AND occurred_at > now() - interval '5 minutes'`,
			action).Scan(&n); err != nil || n == 0 {
			t.Fatalf("%s not audited: %v", action, err)
		}
	}
}

// An overdue maker request expires (maintenance job), is audited, and can no longer be approved.
func TestLedgerAdjustmentExpiry(t *testing.T) {
	l := newLedgerIT(t)
	camp, src := ids.New(), ids.New()
	res, err := l.svc.PostOnce(ctx(t), synth("DONATION_CAPTURED", "fixture:"+src+":exp", "payment", src,
		ledger.Dr(acct(ledger.ClassPSPClearing, ledger.SyntheticProviderID), 700), ledger.Cr(acct(ledger.ClassCampaignUnsettled, camp), 700)))
	if err != nil {
		t.Fatal(err)
	}
	a, err := l.svc.RequestAdjustment(ctx(t), l.f.finance1.id, ledger.AdjustmentRequest{Kind: ledger.AdjReversal, Target: res.TransactionID,
		ReasonCode: "TEST_FIXTURE_CORRECTION", Reason: "integration test: this request is left to expire"})
	if err != nil {
		t.Fatal(err)
	}
	later := ledger.New(l.lp, clock.NewFake(time.Now().Add(ledger.AdjustmentTTL+time.Hour)), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := later.Warm(ctx(t)); err != nil {
		t.Fatal(err)
	}
	if n, err := later.ExpireAdjustments(ctx(t)); err != nil || n < 1 {
		t.Fatalf("expire: %d %v", n, err)
	}
	got, err := l.svc.AdjustmentByID(ctx(t), a.ID)
	if err != nil || got.Status != "EXPIRED" {
		t.Fatalf("status after expiry: %+v %v", got.Status, err)
	}
	var audited int
	if err := l.mig.QueryRow(ctx(t), `SELECT count(*) FROM audit.audit_events WHERE action = 'ledger.adjustment.expired' AND target_id = $1`,
		a.ID).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("expiry not audited: %d %v", audited, err)
	}
	if _, err := l.svc.ApproveAdjustment(ctx(t), l.f.finance2.id, a.ID, "approval after expiry must be refused"); err == nil {
		t.Fatal("expired adjustment approved")
	}
	if l.count(t, `SELECT count(*) FROM ledger.ledger_transactions WHERE reverses_transaction_id = $1`, res.TransactionID) != 0 {
		t.Fatal("expired adjustment posted")
	}
}

// ===== account freeze racing postings =================================================================================

func TestLedgerAccountFreezeVersusConcurrentPostings(t *testing.T) {
	l := newLedgerIT(t)
	camp := ids.New()
	post := func(i int) error {
		src := ids.New()
		_, err := l.svc.PostOnce(context.Background(), synth("DONATION_CAPTURED", fmt.Sprintf("fixture:%s:f%d", src, i), "payment", src,
			ledger.Dr(acct(ledger.ClassPSPClearing, ledger.SyntheticProviderID), 100), ledger.Cr(acct(ledger.ClassCampaignUnsettled, camp), 100)))
		return err
	}
	if err := post(0); err != nil {
		t.Fatal(err)
	}
	var accountID string
	_ = l.lp.QueryRow(ctx(t), `SELECT id FROM ledger.ledger_accounts WHERE code = $1`, "liability:campaign_unsettled:"+camp).Scan(&accountID)
	var wg sync.WaitGroup
	var posted, frozen, other atomic.Int32
	for i := 1; i <= 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			switch err := post(i); {
			case err == nil:
				posted.Add(1)
			case ecode(err) == "LEDGER_ACCOUNT_FROZEN":
				frozen.Add(1)
			default:
				other.Add(1)
				t.Errorf("post: %v", err)
			}
		}(i)
		if i == 20 {
			if err := l.svc.SetAccountStatus(ctx(t), l.f.finance1.id, accountID, "FROZEN", "integration freeze while posting"); err != nil {
				t.Fatal(err)
			}
		}
	}
	wg.Wait()
	if other.Load() != 0 {
		t.Fatal("unexpected posting errors")
	}
	if err := post(99); ecode(err) != "LEDGER_ACCOUNT_FROZEN" {
		t.Fatalf("posting after freeze: %v", err)
	}
	// every committed entry on the account was committed before the freeze (row lock serialisation)
	var after int
	_ = l.lp.QueryRow(ctx(t), `SELECT count(*) FROM ledger.ledger_entries e JOIN ledger.ledger_transactions t ON t.id = e.transaction_id
		JOIN ledger.ledger_accounts a ON a.id = e.account_id WHERE a.id = $1 AND t.posted_at > a.status_changed_at`, accountID).Scan(&after)
	if after != 0 {
		t.Fatalf("%d entries posted after the freeze", after)
	}
	if got := l.balance(t, "liability:campaign_unsettled:"+camp); got != int64(100*(1+posted.Load())) {
		t.Fatalf("balance %d with %d postings", got, posted.Load())
	}
	// closing needs a zero balance; unfreeze works
	if err := l.svc.SetAccountStatus(ctx(t), l.f.finance1.id, accountID, "CLOSED", "attempt to close with balance"); ecode(err) != "LEDGER_ACCOUNT_BALANCE_NOT_ZERO" {
		t.Fatalf("close with balance: %v", err)
	}
	if err := l.svc.SetAccountStatus(ctx(t), l.f.finance1.id, accountID, "ACTIVE", "integration unfreeze after test"); err != nil {
		t.Fatal(err)
	}
}

// ===== period close racing postings ===================================================================================

func TestLedgerPeriodCloseMakerCheckerAndPriorPeriodPostings(t *testing.T) {
	l := newLedgerIT(t)
	// a dedicated past period (unique per run): a random month in 1900–1999
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	month := time.Date(1900+r.Intn(100), time.Month(1+r.Intn(12)), 1, 0, 0, 0, 0, time.UTC)
	if err := l.svc.EnsurePeriod(ctx(t), month); err != nil {
		t.Fatal(err)
	}
	var periodID string
	_ = l.lp.QueryRow(ctx(t), `SELECT id FROM ledger.ledger_periods WHERE code = $1`, month.Format("2006-01")).Scan(&periodID)
	camp := ids.New()
	postAt := func(rule, step string, at time.Time, lines ...ledger.Line) error {
		src := ids.New()
		p := synth(rule, "fixture:"+src+":"+step, "payment", src, lines...)
		p.OccurredAt = at
		_, err := l.svc.PostOnce(context.Background(), p)
		return err
	}
	capture := func(step string) error {
		return postAt("DONATION_CAPTURED", step, month.Add(48*time.Hour), ledger.Dr(acct(ledger.ClassPSPClearing, ledger.SyntheticProviderID), 100),
			ledger.Cr(acct(ledger.ClassCampaignUnsettled, camp), 100))
	}
	if err := capture("before"); err != nil {
		t.Fatal(err)
	}
	// maker requests close; postings race the checker's approval
	if err := l.svc.RequestPeriodClose(ctx(t), l.f.finance1.id, periodID, "month end close for the test period"); err != nil {
		t.Fatal(err)
	}
	if err := l.svc.ApprovePeriodClose(ctx(t), l.f.finance1.id, periodID, "maker cannot approve own close"); ecode(err) != "LEDGER_SELF_APPROVAL_FORBIDDEN" {
		t.Fatalf("self close: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _ = capture(fmt.Sprintf("race%d", i)) }(i)
	}
	if err := l.svc.ApprovePeriodClose(ctx(t), l.f.finance2.id, periodID, "checker approves the test period close"); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	var closedAt time.Time
	_ = l.lp.QueryRow(ctx(t), `SELECT closed_at FROM ledger.ledger_periods WHERE id = $1 AND status = 'CLOSED' AND close_evidence_record_id IS NOT NULL`,
		periodID).Scan(&closedAt)
	if closedAt.IsZero() {
		t.Fatal("period not CLOSED with evidence")
	}
	// nothing was booked into the period after it closed; late captures went to the current period as prior-period
	if n := l.count(t, `SELECT count(*) FROM ledger.ledger_transactions WHERE accounting_period_id = $1 AND posted_at > $2`, periodID, closedAt); n != 0 {
		t.Fatalf("%d journals booked into a closed period", n)
	}
	if err := capture("after"); err != nil {
		t.Fatalf("prior-period capture refused: %v", err)
	}
	if n := l.count(t, `SELECT count(*) FROM ledger.ledger_transactions WHERE idempotency_key LIKE '%:after' AND is_prior_period
		AND accounting_period_id <> $1 AND occurred_at >= $2 AND occurred_at < $3`, periodID, month, month.AddDate(0, 1, 0)); n != 1 {
		t.Fatal("late capture not marked prior-period")
	}
	// a rule that does not allow prior periods is refused
	if err := postAt("RELEASE", "release", month.Add(72*time.Hour), ledger.Dr(acct(ledger.ClassCampaignUnsettled, camp), 100),
		ledger.Cr(acct(ledger.ClassCampaignPayable, camp), 100)); ecode(err) != "LEDGER_PERIOD_CLOSED" {
		t.Fatalf("release into closed period: %v", err)
	}
}

// ===== reconciliation with injected payment facts =====================================================================

type fakeFacts struct{ m map[string]ledger.PaymentFact }

func (f fakeFacts) Payment(_ context.Context, id string) (ledger.PaymentFact, bool, error) {
	p, ok := f.m[id]
	return p, ok, nil
}
func (f fakeFacts) PaymentsInStatus(_ context.Context, status string, _ time.Time, _ int) ([]ledger.PaymentFact, error) {
	var out []ledger.PaymentFact
	for _, p := range f.m {
		if p.Status == status {
			out = append(out, p)
		}
	}
	return out, nil
}

func TestLedgerReconciliationFindsExceptionsAndWorkflow(t *testing.T) {
	l := newLedgerIT(t)
	mismatch, orphan, missing, unknown := ids.New(), ids.New(), ids.New(), ids.New()
	camp := ids.New()
	paymentJournal := func(pid string, amount int64) {
		// a TEST ledger refuses payments-sourced journals (Go and ck_ledger_transactions_no_payment_postings_in_test)
		p := synth("DONATION_CAPTURED", "payment:"+pid+":capture", "payment", pid,
			ledger.Dr(acct(ledger.ClassPSPClearing, ledger.SyntheticProviderID), amount), ledger.Cr(acct(ledger.ClassCampaignUnsettled, camp), amount))
		p.SourceSystem, p.EvidenceClass = ledger.SourcePayments, ledger.EvidenceProviderReported
		if _, err := l.svc.PostOnce(ctx(t), p); ecode(err) != "LEDGER_ENVIRONMENT_MISMATCH" {
			t.Fatalf("payments-sourced journal in a TEST ledger: %v", err)
		}
		// the reconciliation logic is exercised on honestly labelled synthetic capture journals instead
		if _, err := l.svc.PostOnce(ctx(t), synth("DONATION_CAPTURED", "fixture:"+pid+":capture", "payment", pid,
			ledger.Dr(acct(ledger.ClassPSPClearing, ledger.SyntheticProviderID), amount), ledger.Cr(acct(ledger.ClassCampaignUnsettled, camp), amount))); err != nil {
			t.Fatal(err)
		}
	}
	paymentJournal(mismatch, 1000) // facts say 1500
	paymentJournal(orphan, 700)    // no such payment
	svc := ledger.New(l.lp, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.ReconcileJournalsFrom = ledger.SourceSynthetic
	svc.Facts = fakeFacts{m: map[string]ledger.PaymentFact{
		mismatch: {ID: mismatch, Status: "SUCCEEDED", AmountMinor: 1500, Currency: "USD", ExecutionEnvironment: "LOCAL_SIMULATOR"},
		missing:  {ID: missing, Status: "SUCCEEDED", AmountMinor: 900, Currency: "USD", ExecutionEnvironment: "LOCAL_SIMULATOR"},
		unknown:  {ID: unknown, Status: "UNKNOWN", AmountMinor: 400, Currency: "USD", ExecutionEnvironment: "LOCAL_SIMULATOR"},
	}}
	// a suspense balance (short settlement to suspense)
	item := ids.New()
	if _, err := l.svc.PostOnce(ctx(t), synth("SETTLEMENT_DISCREPANCY", "fixture:"+item+":short", "settlement_item", item,
		ledger.Dr(acct(ledger.ClassSuspense, ledger.SyntheticProviderID), 25), ledger.Cr(acct(ledger.ClassPSPClearing, ledger.SyntheticProviderID), 25))); err != nil {
		t.Fatal(err)
	}
	rep, err := svc.Reconcile(ctx(t), "integration")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rep.SettlementReconciliation, "NOT_PERFORMED") {
		t.Fatal("settlement reconciliation must be reported as not performed")
	}
	want := map[string]string{mismatch: "AMOUNT_MISMATCH", orphan: "ORPHAN_POSTING", missing: "MISSING_POSTING", unknown: "UNKNOWN_OUTCOME"}
	for src, typ := range want {
		if l.count(t, `SELECT count(*) FROM ledger.ledger_reconciliation_references WHERE source_id = $1 AND exception_type = $2 AND status = 'OPEN'`,
			src, typ) != 1 {
			t.Fatalf("%s for %s not recorded", typ, src)
		}
	}
	if l.count(t, `SELECT count(*) FROM ledger.ledger_reconciliation_references WHERE exception_type = 'SUSPENSE_BALANCE' AND status <> 'RESOLVED'`) < 1 {
		t.Fatal("suspense balance not reported")
	}
	// rerun: no duplicate open references
	if _, err := svc.Reconcile(ctx(t), "integration"); err != nil {
		t.Fatal(err)
	}
	if l.count(t, `SELECT count(*) FROM ledger.ledger_reconciliation_references WHERE source_id = $1 AND status <> 'RESOLVED'`, mismatch) != 1 {
		t.Fatal("rerun duplicated an open exception")
	}
	// workflow: investigate (owner) → resolve (note); detection fields immutable; resolved is final
	var refID string
	_ = l.lp.QueryRow(ctx(t), `SELECT id FROM ledger.ledger_reconciliation_references WHERE source_id = $1`, mismatch).Scan(&refID)
	if err := svc.InvestigateException(ctx(t), l.f.finance1.id, refID, "looking into the amount mismatch"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ResolveException(ctx(t), l.f.finance1.id, refID, "synthetic mismatch confirmed; no correction needed", ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.ResolveException(ctx(t), l.f.finance1.id, refID, "second resolution must be refused", ""); ecode(err) != "LEDGER_RECONCILIATION_STATE" {
		t.Fatalf("re-resolve: %v", err)
	}
	if _, err := l.lp.Exec(ctx(t), `UPDATE ledger.ledger_reconciliation_references SET expected_amount_minor = 1 WHERE id = $1`, refID); err == nil {
		t.Fatal("detection fields mutable")
	}
}

// ===== payments consumer: test payments never post =====================================================================

func TestLedgerPaymentsConsumerNeverPostsTestPayments(t *testing.T) {
	l := newLedgerIT(t)
	pid, ev := ids.New(), ids.New()
	payload, _ := json.Marshal(map[string]any{"payment_id": pid, "execution_environment": "LOCAL_SIMULATOR", "test_payment": true, "simulated": true})
	before := l.count(t, `SELECT count(*) FROM ledger.ledger_transactions`)
	for i := 0; i < 3; i++ { // duplicate deliveries
		if err := l.svc.RecordPaymentSucceeded(ctx(t), ev, payload); err != nil {
			t.Fatal(err)
		}
	}
	if l.count(t, `SELECT count(*) FROM ledger.ledger_transactions`) != before {
		t.Fatal("a test payment created a journal")
	}
	if l.count(t, `SELECT count(*) FROM ledger.ledger_source_references WHERE source_id = $1 AND decision = 'NOT_ELIGIBLE'
		AND reason_code = 'TEST_PAYMENT' AND evidence_class = 'SIMULATED'`, pid) != 1 {
		t.Fatal("source reference not recorded exactly once")
	}
	// a PRODUCTION payment event (cannot exist yet) would be NOT_ELIGIBLE in a TEST ledger, never posted
	pid2 := ids.New()
	payload, _ = json.Marshal(map[string]any{"payment_id": pid2, "execution_environment": "PRODUCTION", "test_payment": false})
	if err := l.svc.RecordPaymentSucceeded(ctx(t), ids.New(), payload); err != nil {
		t.Fatal(err)
	}
	if l.count(t, `SELECT count(*) FROM ledger.ledger_source_references WHERE source_id = $1 AND decision = 'NOT_ELIGIBLE'
		AND reason_code = 'LEDGER_ENVIRONMENT_MISMATCH'`, pid2) != 1 {
		t.Fatal("production event in a TEST ledger not refused")
	}
}

// ===== failure injection ==============================================================================================

func TestLedgerFailureInjection(t *testing.T) {
	l := newLedgerIT(t)
	camp := ids.New()
	mk := func(step string, amount int64) ledger.Posting {
		src := ids.New()
		return synth("DONATION_CAPTURED", "fixture:"+src+":"+step, "payment", src,
			ledger.Dr(acct(ledger.ClassPSPClearing, ledger.SyntheticProviderID), amount), ledger.Cr(acct(ledger.ClassCampaignUnsettled, camp), amount))
	}
	// 1. rollback after a successful Post: nothing remains
	p := mk("rollback", 100)
	tx, _ := l.lp.Begin(ctx(t))
	if _, err := l.svc.Post(ctx(t), tx, p); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback(ctx(t))
	if l.count(t, `SELECT count(*) FROM ledger.ledger_transactions WHERE idempotency_key = $1`, p.IdempotencyKey) != 0 {
		t.Fatal("rolled-back journal persisted")
	}
	// 2. connection killed mid-transaction (worker crash): no partial journal
	p = mk("killed", 100)
	conn, err := l.lp.Acquire(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	tx2, _ := conn.Begin(ctx(t))
	if _, err := l.svc.Post(ctx(t), tx2, p); err != nil {
		t.Fatal(err)
	}
	// the connection disappears mid-transaction (process crash): the server rolls the transaction back
	raw := conn.Hijack()
	_ = raw.Close(context.Background())
	_ = tx2
	if n := l.count(t, `SELECT count(*) FROM ledger.ledger_transactions WHERE idempotency_key = $1`, p.IdempotencyKey); n != 0 {
		t.Fatalf("journal from a killed connection persisted: %d", n)
	}
	// 3. lock timeout: another transaction holds the balance row of an account the posting decreases
	src := ids.New()
	if _, err := l.svc.PostOnce(ctx(t), synth("RELEASE", "fixture:"+src+":seed", "payment", src,
		ledger.Dr(acct(ledger.ClassCampaignUnsettled, camp), 1), ledger.Cr(acct(ledger.ClassCampaignPayable, camp), 1))); err == nil {
		t.Fatal("release without unsettled funds must fail (balance 0 before the captures)")
	}
	if _, err := l.svc.PostOnce(ctx(t), mk("funding", 1000)); err != nil {
		t.Fatal(err)
	}
	var unsettledID string
	_ = l.lp.QueryRow(ctx(t), `SELECT id FROM ledger.ledger_accounts WHERE code = $1`, "liability:campaign_unsettled:"+camp).Scan(&unsettledID)
	holder, _ := l.lp.Begin(ctx(t))
	if _, err := holder.Exec(ctx(t), `SELECT 1 FROM ledger.ledger_balances WHERE account_id = $1 FOR UPDATE`, unsettledID); err != nil {
		t.Fatal(err)
	}
	src2 := ids.New()
	release := synth("RELEASE", "fixture:"+src2+":locked", "payment", src2, ledger.Dr(acct(ledger.ClassCampaignUnsettled, camp), 10),
		ledger.Cr(acct(ledger.ClassCampaignPayable, camp), 10))
	lockCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_, err = l.svc.PostOnce(lockCtx, release)
	cancel()
	_ = holder.Rollback(ctx(t))
	if err == nil {
		t.Fatal("posting succeeded while the balance row was locked past its timeout")
	}
	if l.count(t, `SELECT count(*) FROM ledger.ledger_transactions WHERE idempotency_key = $1`, release.IdempotencyKey) != 0 {
		t.Fatal("partial journal after lock timeout")
	}
	// 4. outbox gateway refusal inside the posting transaction rolls the journal back
	p = mk("outbox", 100)
	tx3, _ := l.lp.Begin(ctx(t))
	if _, err := l.svc.Post(ctx(t), tx3, p); err != nil {
		t.Fatal(err)
	}
	_, err = tx3.Exec(ctx(t), `SELECT app.enqueue_outbox(gen_random_uuid(), 'x', gen_random_uuid(), 'payments.forged', '{}'::jsonb, 'c', now())`)
	if err == nil {
		t.Fatal("ledger role enqueued a non-ledger event")
	}
	_ = tx3.Commit(context.Background()) // aborted transaction: commit rolls back
	if l.count(t, `SELECT count(*) FROM ledger.ledger_transactions WHERE idempotency_key = $1`, p.IdempotencyKey) != 0 {
		t.Fatal("journal survived an outbox failure")
	}
	// 5. projection corruption is detected (L10) and only an owner rebuild repairs it
	var anyAcct string
	_ = l.lp.QueryRow(ctx(t), `SELECT id FROM ledger.ledger_accounts WHERE code = $1`, "liability:campaign_unsettled:"+camp).Scan(&anyAcct)
	if _, err := l.mig.Exec(ctx(t), `SELECT set_config('ledger.projection_maintenance', 'on', false);
		UPDATE ledger.ledger_balances SET balance_minor = balance_minor + 7, credit_total_minor = credit_total_minor + 7 WHERE account_id = '`+anyAcct+`'`); err != nil {
		t.Fatalf("corrupt projection as owner: %v", err)
	}
	rep, err := l.svc.RunIntegrity(ctx(t), "SYSTEM", "integration-failure-injection")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range rep.Checks {
		if c.Code == "L10" && c.Status == "FAILED" {
			found = true
		}
	}
	if !found {
		t.Fatal("drift not detected")
	}
	if _, err := l.mig.Exec(ctx(t), `SELECT ledger.rebuild_balance($1)`, anyAcct); err != nil {
		t.Fatal(err)
	}
	if rep, err := l.svc.RunIntegrity(ctx(t), "SYSTEM", "integration-failure-injection"); err != nil || !rep.Passed {
		t.Fatalf("after rebuild: %v %+v", err, rep.Checks)
	}
}

// ===== balance reads during posting =================================================================================

func TestLedgerTrialBalanceStaysBalancedDuringPosting(t *testing.T) {
	l := newLedgerIT(t)
	ctxb := context.Background()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var reads, bad atomic.Int32
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				tb, err := l.svc.TrialBalance(ctxb, "USD", "")
				if err != nil || len(tb.Currencies) == 0 || !tb.Currencies[0].Balanced {
					bad.Add(1)
				}
				reads.Add(1)
			}
		}()
	}
	for i := 0; i < 8; i++ {
		run, err := l.svc.PostFixture(ctxb, "", ledger.Fixtures[i%len(ledger.Fixtures)], "")
		if err != nil || len(run.Journals) == 0 {
			t.Fatalf("fixture: %v", err)
		}
	}
	close(stop)
	wg.Wait()
	if bad.Load() != 0 || reads.Load() == 0 {
		t.Fatalf("%d of %d trial balance reads unbalanced", bad.Load(), reads.Load())
	}
}

// ===== staff API: RBAC, step-up, maker-checker over HTTP ============================================================

func TestLedgerStaffAPI(t *testing.T) {
	l := newLedgerIT(t)
	fin := l.s.staff(t, l.f.finance3)
	for _, path := range []string{"/admin/ledger/overview", "/admin/ledger/accounts", "/admin/ledger/journals", "/admin/ledger/trial-balance",
		"/admin/ledger/periods", "/admin/ledger/adjustments", "/admin/ledger/reconciliation", "/admin/ledger/exceptions", "/admin/ledger/source-references"} {
		r := fin.do("GET", path, nil)
		fin.expect(r, 200, "")
		if r.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: cache-control", path)
		}
	}
	ov := fin.do("GET", "/admin/ledger/overview", nil)
	if !strings.Contains(string(ov.Data), `"environment":"TEST"`) || !strings.Contains(string(ov.Data), `"production_posting_enabled":false`) ||
		strings.Contains(strings.ToLower(string(ov.Data)), "withdrawable_balance") {
		t.Fatalf("overview: %.300s", ov.Data)
	}
	// synthetic fixture via the API (ADMIN); FINANCE cannot
	fin.expect(fin.do("POST", "/admin/ledger/synthetic-fixtures", map[string]string{"scenario": "donations"}), 403, "")
	adm := l.s.staff(t, l.f.ledgerAdmin)
	r := adm.do("POST", "/admin/ledger/synthetic-fixtures", map[string]string{"scenario": "donations"})
	adm.expect(r, 201, "")
	var run ledger.FixtureRun
	_ = json.Unmarshal(r.Data, &run)
	if run.Label != "TEST_ONLY" || len(run.Journals) != 3 {
		t.Fatalf("fixture run: %s", r.Data)
	}
	jr := fin.do("GET", "/admin/ledger/journals/"+run.Journals[0], nil)
	fin.expect(jr, 200, "")
	if !strings.Contains(string(jr.Data), `"balanced":true`) || !strings.Contains(string(jr.Data), `"test_only":true`) {
		t.Fatalf("journal detail: %.300s", jr.Data)
	}
	// non-staff and staff without ledger.view
	donor := verifiedUser(t, l.s, "ldonor")
	donor.expect(donor.do("GET", "/admin/ledger/overview", nil), 404, "")
	sup := l.s.staff(t, l.f.support)
	if r := sup.do("GET", "/admin/ledger/overview", nil); r.Status != 403 {
		t.Fatalf("support: %d", r.Status)
	}
	// adjustment over HTTP: Idempotency-Key required; maker ≠ checker; step-up enforced by the policy (fresh login = fresh MFA)
	fin.expect(fin.do("POST", "/admin/ledger/adjustments", map[string]any{"kind": "REVERSAL", "target_transaction_id": run.Journals[2],
		"reason_code": "TEST_FIXTURE_CORRECTION", "reason": "reverse the synthetic provider fee"}), 400, "IDEMPOTENCY_KEY_REQUIRED")
	cr := fin.do("POST", "/admin/ledger/adjustments", map[string]any{"kind": "REVERSAL", "target_transaction_id": run.Journals[2],
		"reason_code": "TEST_FIXTURE_CORRECTION", "reason": "reverse the synthetic provider fee"}, "Idempotency-Key", ids.New())
	fin.expect(cr, 201, "")
	adjID := cr.field(t, "id")
	fin.expect(fin.do("POST", "/admin/ledger/adjustments/"+adjID+"/approve", map[string]string{"reason": "self approval attempt"}), 403,
		"LEDGER_SELF_APPROVAL_FORBIDDEN")
	checker := l.s.freshStaff(t, l.f.finance2) // fresh login = fresh step-up
	ap := checker.do("POST", "/admin/ledger/adjustments/"+adjID+"/approve", map[string]string{"reason": "checker approves the reversal"})
	checker.expect(ap, 200, "")
	if !strings.Contains(string(ap.Data), `"status":"POSTED"`) {
		t.Fatalf("approval: %s", ap.Data)
	}
	// no free-form posting route exists
	for _, path := range []string{"/admin/ledger/journals", "/admin/ledger/accounts/" + ids.New() + "/balance"} {
		if r := fin.do("POST", path, map[string]any{"lines": []any{}}); r.Status != 404 && r.Status != 405 {
			t.Fatalf("POST %s: %d", path, r.Status)
		}
	}
	_ = errors.New
	_ = pgx.ErrNoRows
}
