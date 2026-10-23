//go:build integration

package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/reconciliation"
)

// Stage 19 volume sample (opt-in, FUNDZIM_IT_S19_LOAD=1): one Stage 17 run over the sandbox transaction-report source
// with a clock past the settlement horizon, so that every succeeded sandbox payment in the window without a statement
// line must become (or be seen again as) a MISSING_SETTLEMENT exception. Run twice: the first opens them, the second
// sees them. Measures how long a run with that many expected-but-missing records takes on this host.
func TestFinancialReconciliationVolumeSample(t *testing.T) {
	if os.Getenv("FUNDZIM_IT_S19_LOAD") != "1" {
		t.Skip("opt-in: FUNDZIM_IT_S19_LOAD=1 (local disposable stack only)")
	}
	p := newPayIT(t)
	r := newReconIT(t, p.s, p.f)
	later := r.later(60 * 24 * time.Hour)
	for pass := 1; pass <= 2; pass++ {
		var run reconciliation.Run
		waitFor(t, 2*time.Minute, "a run slot", func() bool {
			var err error
			run, err = later.RequestRun(ctx(t), r.f.finance1.id, reconciliation.RunInput{Source: "sandbox_transactions_test", Currency: "USD",
				Reason: "stage 19 volume sample"})
			return err == nil
		})
		c, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		start := time.Now()
		err := later.ExecuteRun(c, run.ID)
		d := time.Since(start)
		cancel()
		got, _ := later.RunByID(ctx(t), run.ID)
		t.Logf("pass %d: %s in %s (err %v): items considered %d, matched %d, exceptions opened %d, seen %d, closed %d", pass, got.Status, d.Round(time.Millisecond),
			err, got.ItemsConsidered, got.ItemsMatched, got.ExceptionsOpened, got.ExceptionsSeen, got.ExceptionsClosed)
		if err != nil {
			t.Fatal(err)
		}
	}
}
