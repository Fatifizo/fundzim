//go:build integration

package integration

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/payouts"
)

// Concurrent status queries of one payout (the poll job of several workers, a staff status check): every query is
// recorded once with its own attempt number, none deadlocks, and the payout's state is applied once.
func TestFinancialPayoutConcurrentStatusQueries(t *testing.T) {
	p := newPayoutIT(t)
	camp := p.campaign(t, 20_000)
	id := p.requestDirect(t, camp, 2_001) // suffix 01: the simulated provider keeps the payout processing
	p.approve(t, id)
	if _, err := p.po.SubmitNow(ctx(t), id); err != nil {
		t.Fatal(err)
	}
	p.drive(t, 30*time.Second, "processing", status(payouts.StProcessing), id)
	d0 := p.dbCounters(t)
	var wg sync.WaitGroup
	var ok, failed atomic.Int32
	errs := make(chan error, 64)
	for round := 0; round < 4; round++ {
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := p.po.StatusQuery(context.Background(), id); err != nil {
					failed.Add(1)
					select {
					case errs <- err:
					default:
					}
					return
				}
				ok.Add(1)
			}()
		}
		wg.Wait()
	}
	close(errs)
	d1 := p.dbCounters(t)
	seen := map[string]int{}
	for err := range errs {
		seen[ecode(err)+": "+err.Error()]++
	}
	t.Logf("64 concurrent status queries: %d ok, %d failed; database deadlocks during the run: %d; errors: %v", ok.Load(), failed.Load(), d1[0]-d0[0], seen)
	var attempts, inflight int
	_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FILTER (WHERE operation = 'GET_STATUS'), count(*) FILTER (WHERE classification = 'IN_FLIGHT')
		FROM app.payout_attempts WHERE payout_request_id = $1`, id).Scan(&attempts, &inflight)
	if inflight != 0 {
		t.Fatalf("%d attempts left IN_FLIGHT", inflight)
	}
	if failed.Load() != 0 || d1[0] != d0[0] {
		t.Fatalf("%d concurrent status queries failed, %d deadlocks: %v", failed.Load(), d1[0]-d0[0], seen)
	}
	var dupNos int
	_ = p.mig.QueryRow(ctx(t), `SELECT count(*) - count(DISTINCT attempt_no) FROM app.payout_attempts WHERE payout_request_id = $1`, id).Scan(&dupNos)
	if dupNos != 0 {
		t.Fatalf("%d duplicate attempt numbers", dupNos)
	}
	if st := p.payout(t, id).Status; st != payouts.StProcessing {
		t.Fatalf("status %s", st)
	}
	t.Logf("%d GET_STATUS attempts recorded", attempts)
}
