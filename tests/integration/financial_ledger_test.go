//go:build integration

package integration

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/ledger"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Stage 19 ledger concurrency (docs/stage-19/concurrency-test-plan.md §3). Every journal is a TEST_ONLY synthetic
// fixture on accounts owned by ids the test generates, so nothing touches another test's balances.

// Same idempotency key, concurrently, with two different payloads: exactly one journal; every duplicate of the winner
// returns it; every request with the other payload is an idempotency conflict; nothing else is written.
func TestFinancialLedgerConcurrentConflictingPayloads(t *testing.T) {
	l := newLedgerIT(t)
	camp, src := ids.New(), ids.New()
	key := "fixture:" + src + ":s19-conflict"
	mk := func(amt int64) ledger.Posting {
		return synth("DONATION_CAPTURED", key, "payment", src,
			ledger.Dr(acct(ledger.ClassPSPClearing, ledger.SyntheticProviderID), amt), ledger.Cr(acct(ledger.ClassCampaignUnsettled, camp), amt))
	}
	var wg sync.WaitGroup
	type out struct {
		amt  int64
		tx   string
		code string
	}
	res := make([]out, 60)
	for i := range res {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			amt := int64(500)
			if i%2 == 1 {
				amt = 700
			}
			r, err := l.svc.PostOnce(context.Background(), mk(amt))
			res[i] = out{amt: amt, tx: r.TransactionID}
			if err != nil {
				res[i].code = ecode(err)
			}
			if err != nil && ecode(err) != "LEDGER_IDEMPOTENCY_CONFLICT" {
				t.Errorf("post %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	var winner int64
	var txID string
	for _, r := range res {
		if r.code == "" {
			if winner != 0 && (r.amt != winner || r.tx != txID) {
				t.Fatalf("two payloads were accepted under one key: %d/%s and %d/%s", winner, txID, r.amt, r.tx)
			}
			winner, txID = r.amt, r.tx
		}
	}
	for _, r := range res {
		if r.amt != winner && r.code != "LEDGER_IDEMPOTENCY_CONFLICT" {
			t.Fatalf("the losing payload was not refused as a conflict: %+v", r)
		}
	}
	if n := l.count(t, `SELECT count(*) FROM ledger.ledger_transactions WHERE idempotency_key = $1`, key); n != 1 {
		t.Fatalf("journals for one key: %d", n)
	}
	if b := l.balance(t, ledger.ClassCampaignUnsettled+":"+camp); b != winner {
		t.Fatalf("balance %d, winner %d", b, winner)
	}
}

// Opposite lock orders: CAMPAIGN_FROZEN (payable → held) and CAMPAIGN_UNFROZEN (held → payable) touch the same two
// accounts in opposite directions, and each locks the balance row of the account it decreases before updating the
// other. Posted concurrently on one campaign they must never deadlock (balance rows are locked and updated in
// account-id order) and the two balances must equal what was posted.
func TestFinancialLedgerOppositeOrderPostingsDoNotDeadlock(t *testing.T) {
	l := newLedgerIT(t)
	camp := ids.New()
	if _, err := l.svc.FundCampaign(ctx(t), "", camp, 100_000, ""); err != nil {
		t.Fatal(err)
	}
	move := func(rule, n string, amt int64) error {
		from, to := ledger.ClassCampaignPayable, ledger.ClassCampaignHeld
		if rule == "CAMPAIGN_UNFROZEN" {
			from, to = to, from
		}
		p := synth(rule, "campaign:"+camp+":"+map[string]string{"CAMPAIGN_FROZEN": "freeze", "CAMPAIGN_UNFROZEN": "unfreeze"}[rule]+":"+n, "campaign", camp,
			ledger.Dr(acct(from, camp), amt), ledger.Cr(acct(to, camp), amt))
		_, err := l.svc.PostOnce(context.Background(), p)
		return err
	}
	if err := move("CAMPAIGN_FROZEN", "seed-"+ids.New(), 50_000); err != nil { // both accounts hold funds
		t.Fatal(err)
	}
	const n = 200
	var deadlocks, other, posted atomic.Int32
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for i := 0; i < n; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			rule := "CAMPAIGN_FROZEN"
			if i%2 == 1 {
				rule = "CAMPAIGN_UNFROZEN"
			}
			err := move(rule, fmt.Sprintf("s19-%d-%s", i, ids.New()[:8]), 10)
			switch {
			case err == nil:
				posted.Add(1)
			case sqlState(err) == "40P01" || ecode(err) == "LEDGER_DEADLOCK":
				deadlocks.Add(1)
			default:
				other.Add(1)
				t.Errorf("post %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	t.Logf("opposite-direction postings: %d posted, %d deadlocks, %d other errors", posted.Load(), deadlocks.Load(), other.Load())
	if deadlocks.Load() != 0 || posted.Load() != n {
		t.Fatalf("%d deadlocks, %d of %d posted", deadlocks.Load(), posted.Load(), n)
	}
	// 100 freezes and 100 unfreezes of 10 cancel out: payable 50 000, held 50 000
	if pb, hb := l.balance(t, ledger.ClassCampaignPayable+":"+camp), l.balance(t, ledger.ClassCampaignHeld+":"+camp); pb != 50_000 || hb != 50_000 {
		t.Fatalf("payable %d held %d", pb, hb)
	}
}

// Connection loss before commit: a posting blocked on its account row loses its connection; nothing of it is visible;
// the retry (same key) posts exactly once. Then the ambiguous case: the first attempt committed but the caller never
// saw the answer — the retry resolves to the existing journal instead of posting again.
func TestFinancialLedgerConnectionLossAndAmbiguousRetry(t *testing.T) {
	l := newLedgerIT(t)
	su := superuser(t)
	camp, src := ids.New(), ids.New()
	key := "fixture:" + src + ":s19-connloss"
	post := synth("DONATION_CAPTURED", key, "payment", src,
		ledger.Dr(acct(ledger.ClassPSPClearing, ledger.SyntheticProviderID), 321), ledger.Cr(acct(ledger.ClassCampaignUnsettled, camp), 321))
	// create the campaign account with a first, unrelated journal, then hold its row
	seed := ids.New()
	if _, err := l.svc.PostOnce(ctx(t), synth("DONATION_CAPTURED", "fixture:"+seed+":s19-seed", "payment", seed,
		ledger.Dr(acct(ledger.ClassPSPClearing, ledger.SyntheticProviderID), 1), ledger.Cr(acct(ledger.ClassCampaignUnsettled, camp), 1))); err != nil {
		t.Fatal(err)
	}
	var accountID string
	if err := l.mig.QueryRow(ctx(t), `SELECT id FROM ledger.ledger_accounts WHERE code = $1`, ledger.ClassCampaignUnsettled+":"+camp).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	lock := lockRow(t, l.mig, "ledger.ledger_accounts", accountID)
	done := make(chan error, 1)
	go func() {
		c, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, err := l.svc.PostOnce(c, post)
		done <- err
	}()
	waitFor(t, 30*time.Second, "posting blocked on the account row", func() bool { return len(lock.waiters("")) >= 1 })
	terminate(t, su, lock.waiters(""))
	err := <-done
	if err == nil {
		t.Fatal("a posting whose connection was killed before commit reported success")
	}
	t.Logf("posting after connection loss: %v", err)
	lock.release()
	if n := l.count(t, `SELECT count(*) FROM ledger.ledger_transactions WHERE idempotency_key = $1`, key); n != 0 {
		t.Fatalf("a rolled-back posting is visible: %d journals", n)
	}
	r1, err := l.svc.PostOnce(ctx(t), post)
	if err != nil {
		t.Fatal(err)
	}
	// ambiguous commit: the caller retries without knowing whether r1 committed
	for i := 0; i < 5; i++ {
		r, err := l.svc.PostOnce(ctx(t), post)
		if err != nil || r.TransactionID != r1.TransactionID {
			t.Fatalf("retry %d after an ambiguous commit: %v %s", i, err, r.TransactionID)
		}
	}
	if n := l.count(t, `SELECT count(*) FROM ledger.ledger_transactions WHERE idempotency_key = $1`, key); n != 1 {
		t.Fatalf("journals after retries: %d", n)
	}
	if b := l.balance(t, ledger.ClassCampaignUnsettled+":"+camp); b != 322 {
		t.Fatalf("balance %d", b)
	}
}

// Integrity verification while journals are being committed: every run passes; the projection never disagrees with
// the entries it summarises (L1–L15 are evaluated on a consistent snapshot).
func TestFinancialLedgerIntegrityDuringConcurrentPosting(t *testing.T) {
	l := newLedgerIT(t)
	camp := ids.New()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var posted atomic.Int32
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				src := ids.New()
				if _, err := l.svc.PostOnce(context.Background(), synth("DONATION_CAPTURED", "fixture:"+src+":s19-int", "payment", src,
					ledger.Dr(acct(ledger.ClassPSPClearing, ledger.SyntheticProviderID), 7), ledger.Cr(acct(ledger.ClassCampaignUnsettled, camp), 7))); err == nil {
					posted.Add(1)
				}
			}
		}()
	}
	runs := 0
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		rep, err := l.svc.RunIntegrity(ctx(t), "SYSTEM", "s19-concurrent")
		if err != nil {
			close(stop)
			wg.Wait()
			t.Fatal(err)
		}
		if !rep.Passed {
			close(stop)
			wg.Wait()
			t.Fatalf("integrity failed while posting: %+v", rep)
		}
		runs++
	}
	close(stop)
	wg.Wait()
	t.Logf("%d integrity runs passed during %d concurrent postings", runs, posted.Load())
	if b := l.balance(t, ledger.ClassCampaignUnsettled+":"+camp); b != int64(7*posted.Load()) {
		t.Fatalf("balance %d for %d postings", b, posted.Load())
	}
}
