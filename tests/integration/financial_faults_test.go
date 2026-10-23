//go:build integration

package integration

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/ledger"
	"github.com/Fatifizo/fundzim/internal/payouts"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Stage 19 fault injection at worker and pool boundaries (docs/stage-19/fault-injection.md F-16, F-17, F-19).

// Pool exhaustion: a posting that cannot get a connection fails before anything is written and succeeds once one is
// free; nothing partial is ever visible.
func TestFinancialLedgerPoolExhaustionFailsCleanly(t *testing.T) {
	need(t, "DATABASE_LEDGER_URL")
	one, err := db.Open(ctx(t), db.Options{URL: os.Getenv("DATABASE_LEDGER_URL"), MaxConns: 1, ConnectTimeout: 5 * time.Second, AppName: "fundzim-it-s19-pool"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(one.Close)
	svc := ledger.New(one, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	camp, src := ids.New(), ids.New()
	post := synth("DONATION_CAPTURED", "fixture:"+src+":s19-pool", "payment", src,
		ledger.Dr(acct(ledger.ClassPSPClearing, ledger.SyntheticProviderID), 55), ledger.Cr(acct(ledger.ClassCampaignUnsettled, camp), 55))
	held, err := one.Acquire(ctx(t)) // the only connection
	if err != nil {
		t.Fatal(err)
	}
	c, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	_, err = svc.PostOnce(c, post)
	cancel()
	if err == nil {
		held.Release()
		t.Fatal("a posting succeeded without a connection")
	}
	t.Logf("posting with the pool exhausted: %v", err)
	held.Release()
	lp := pool(t, os.Getenv("DATABASE_LEDGER_URL"))
	var n int
	_ = lp.QueryRow(ctx(t), `SELECT count(*) FROM ledger.ledger_transactions WHERE idempotency_key = $1`, post.IdempotencyKey).Scan(&n)
	if n != 0 {
		t.Fatalf("%d journals after the refused posting", n)
	}
	if _, err := svc.PostOnce(ctx(t), post); err != nil {
		t.Fatal(err)
	}
	_ = lp.QueryRow(ctx(t), `SELECT count(*) FROM ledger.ledger_transactions WHERE idempotency_key = $1`, post.IdempotencyKey).Scan(&n)
	if n != 1 {
		t.Fatalf("%d journals after the retry", n)
	}
}

// Audit verification interrupted: a run cut short records nothing (no verification row, no checkpoint), so it can
// never vouch for rows it did not check; a run that finishes records exactly one INTACT result.
func TestFinancialAuditVerificationInterrupted(t *testing.T) {
	v := testVerifier(t, 8)
	const chain = "audit.audit_events"
	counts := func() (runs, cps int) {
		mig := pool(t, os.Getenv("DATABASE_MIGRATION_URL"))
		_ = mig.QueryRow(ctx(t), `SELECT (SELECT count(*) FROM audit.chain_verifications WHERE chain = $1),
			(SELECT count(*) FROM audit.chain_checkpoints WHERE chain = $1)`, chain).Scan(&runs, &cps)
		return
	}
	interrupted := 0
	for _, d := range []time.Duration{time.Millisecond, 20 * time.Millisecond, 200 * time.Millisecond, 2 * time.Second, 5 * time.Minute} {
		r0, c0 := counts()
		c, cancel := context.WithTimeout(context.Background(), d)
		res, err := v.VerifyFull(c, chain)
		cancel()
		r1, c1 := counts()
		if err != nil {
			interrupted++
			if r1 != r0 || c1 != c0 {
				t.Fatalf("interrupted after %s but recorded %d runs / %d checkpoints", d, r1-r0, c1-c0)
			}
			t.Logf("deadline %s: interrupted (%v); nothing recorded", d, err)
			continue
		}
		if !res.Intact() || r1 != r0+1 {
			t.Fatalf("deadline %s: completed run intact=%v recorded %d runs", d, res.Intact(), r1-r0)
		}
		t.Logf("deadline %s: completed (%d rows, %d ms); one INTACT run recorded", d, res.ToSeq, res.DurationMS)
	}
	if interrupted == 0 {
		t.Log("no deadline interrupted the verification on this chain size (every run completed and was recorded once)")
	}
}

// Reconciliation interrupted midway: runs cut short at various points and a final complete run record one genuine
// mismatch exactly once.
func TestFinancialPayoutReconciliationInterrupted(t *testing.T) {
	p := newPayoutIT(t)
	camp := p.campaign(t, 20_000)
	id := p.requestDirect(t, camp, 6_000)
	p.approve(t, id)
	if _, err := p.po.SubmitNow(ctx(t), id); err != nil {
		t.Fatal(err)
	}
	p.drive(t, 60*time.Second, "completed", status(payouts.StCompleted), id)
	waitFor(t, 30*time.Second, "consumed", func() bool {
		_ = p.po.Recover(ctx(t))
		return p.payout(t, id).Reservation.Status == payouts.ResConsumed
	})
	if _, err := p.mig.Exec(ctx(t), `UPDATE app.sandbox_provider_payouts SET raw_status = 'failed' WHERE our_reference = $1`, id); err != nil {
		t.Fatal(err)
	}
	for _, d := range []time.Duration{time.Millisecond, 50 * time.Millisecond, 300 * time.Millisecond, time.Second} {
		c, cancel := context.WithTimeout(context.Background(), d)
		rep, err := p.po.Reconcile(c, "it-s19-interrupted")
		cancel()
		t.Logf("run with %s: checked %d, err %v", d, rep.Checked, err)
	}
	if _, err := p.po.Reconcile(ctx(t), "it-s19-complete"); err != nil {
		t.Fatal(err)
	}
	refs := p.reconRefs(t, id)
	var exc int
	_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_exceptions WHERE payout_request_id = $1 AND exception_type = 'RECONCILIATION_MISMATCH'`, id).Scan(&exc)
	if len(refs) != 1 || refs[0] != "PROVIDER_STATUS_MISMATCH" || exc != 1 {
		t.Fatalf("after interrupted runs: references %v, exceptions %d", refs, exc)
	}
}
