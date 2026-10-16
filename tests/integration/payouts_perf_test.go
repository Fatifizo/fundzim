//go:build integration

package integration

// Stage 11 payout performance (docs/stage-11/testing.md §perf). Opt-in: FUNDZIM_IT_PERF=1 (it writes hundreds of
// TEST payouts, reservations and ledger journals into the development database). Every number is measured; the
// results table is printed and written to $FUNDZIM_PERF_OUT/payouts-perf.md when set.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/payouts"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

type poRes struct {
	name          string
	n, c          int
	p50, p95, max time.Duration
	rps           float64
	errors        int
}

func poMeasure(name string, n, c int, f func(worker, i int) error) poRes {
	lat := make([]time.Duration, n)
	var next atomic.Int64
	var errsN atomic.Int32
	var wg sync.WaitGroup
	t0 := time.Now()
	for w := 0; w < c; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= n {
					return
				}
				s := time.Now()
				if err := f(w, i); err != nil {
					errsN.Add(1)
				}
				lat[i] = time.Since(s)
			}
		}(w)
	}
	wg.Wait()
	wall := time.Since(t0)
	sort.Slice(lat, func(a, b int) bool { return lat[a] < lat[b] })
	pc := func(p float64) time.Duration { return lat[int(float64(n)*p+0.999999)-1] }
	return poRes{name, n, c, pc(.5), pc(.95), lat[n-1], float64(n) / wall.Seconds(), int(errsN.Load())}
}

func (r poRes) row() string {
	ms := func(d time.Duration) string { return strconv.FormatFloat(float64(d.Microseconds())/1000, 'f', 1, 64) }
	return fmt.Sprintf("| %s | %d | %d | %s | %s | %s | %.0f | %d |\n", r.name, r.c, r.n, ms(r.p50), ms(r.p95), ms(r.max), r.rps, r.errors)
}

func TestPayoutsPerf(t *testing.T) {
	if os.Getenv("FUNDZIM_IT_PERF") != "1" {
		t.Skip("set FUNDZIM_IT_PERF=1")
	}
	p := newPayoutIT(t)
	const workers = 8
	camps := make([]string, workers)
	for i := range camps {
		camps[i] = p.campaign(t, 100_000_000)
	}
	var out string
	add := func(r poRes) { out += r.row(); t.Log(r.row()) }
	header := "| scenario | c | n | p50 ms | p95 ms | max ms | ops/s | errors |\n|---|---:|---:|---:|---:|---:|---:|---:|\n"

	// request (request-time eligibility + in-flight slot + ledger reservation), then cancel (release): one campaign
	// per worker, because a campaign has at most one non-terminal payout
	var reqLat, cancelLat []time.Duration
	var mu sync.Mutex
	cycle := func(c int) poRes {
		return poMeasure("request (eligibility + reserve) then cancel (release)", 400, c, func(w, i int) error {
			s := time.Now()
			po, err := p.po.RequestPayout(ctx(t), p.owner.id, true, camps[w], payouts.RequestInput{Currency: "USD", AmountMinor: "1000",
				DestinationID: p.dest}, "perf-"+ids.New())
			d1 := time.Since(s)
			if err != nil {
				return err
			}
			s = time.Now()
			_, err = p.po.CancelByOwner(ctx(t), p.owner.id, true, po.ID)
			mu.Lock()
			reqLat, cancelLat = append(reqLat, d1), append(cancelLat, time.Since(s))
			mu.Unlock()
			return err
		})
	}
	split := func(name string, c int, lat []time.Duration) poRes {
		sort.Slice(lat, func(a, b int) bool { return lat[a] < lat[b] })
		pc := func(q float64) time.Duration { return lat[int(float64(len(lat))*q+0.999999)-1] }
		return poRes{name: name, n: len(lat), c: c, p50: pc(.5), p95: pc(.95), max: lat[len(lat)-1]}
	}
	for _, c := range []int{1, workers} {
		reqLat, cancelLat = nil, nil
		r := cycle(c)
		add(r)
		add(split("  of which: withdrawal request incl. eligibility and reservation", c, reqLat))
		add(split("  of which: cancellation incl. reservation release", c, cancelLat))
	}
	// eligibility evaluation alone (owner dry run: gather + 22 checks, nothing recorded)
	add(poMeasure("eligibility evaluation (owner preview with destination)", 300, workers, func(w, i int) error {
		_, err := p.po.Availability(ctx(t), p.owner.id, camps[w], "USD", p.dest, 1000)
		return err
	}))
	// approval transition (one FINANCE approval of a DUAL payout; the second approval completes the transition)
	ids_ := make([]string, workers)
	for w := range ids_ {
		po, err := p.po.RequestPayout(ctx(t), p.owner.id, true, camps[w], payouts.RequestInput{Currency: "USD", AmountMinor: "1000",
			DestinationID: p.dest}, "perf-appr-"+ids.New())
		if err != nil {
			t.Fatal(err)
		}
		ids_[w] = po.ID
	}
	now := time.Now()
	add(poMeasure("approval decision (FINANCE, recorded with step-up time)", workers, workers, func(w, i int) error {
		_, err := p.po.ApprovePayout(ctx(t), p.f.finance1.id, now, ids_[i], payouts.DecisionInput{Reason: "perf approval one", ConflictAttested: true})
		return err
	}))
	add(poMeasure("approval decision completing DUAL (→ APPROVED)", workers, workers, func(w, i int) error {
		_, err := p.po.ApprovePayout(ctx(t), p.f.finance2.id, now, ids_[i], payouts.DecisionInput{Reason: "perf approval two", ConflictAttested: true})
		return err
	}))
	for _, id := range ids_ {
		// the compose worker may already have submitted an APPROVED payout (it shares the database): only those may stay
		if _, err := p.po.CancelByStaff(ctx(t), p.f.finance1.id, id, "perf cleanup cancel"); err != nil {
			var st string
			_ = p.mig.QueryRow(ctx(t), `SELECT status FROM app.payout_requests WHERE id = $1`, id).Scan(&st)
			if st == "APPROVED" || st == "PENDING_REVIEW" || st == "PAYOUT_REQUESTED" {
				t.Fatal(err)
			}
			t.Logf("perf cleanup: payout %s already %s (taken by the worker)", id, st)
		}
	}
	// reads
	var anyID string
	_ = p.mig.QueryRow(ctx(t), `SELECT id FROM app.payout_requests WHERE campaign_id = $1 LIMIT 1`, camps[0]).Scan(&anyID)
	add(poMeasure("payout status retrieval (owner view)", 500, workers, func(w, i int) error {
		_, err := p.po.OwnerPayout(ctx(t), p.owner.id, anyID)
		return err
	}))
	add(poMeasure("payout detail (staff: events, decisions, attempts, journals)", 300, workers, func(w, i int) error {
		_, err := p.po.StaffDetail(ctx(t), anyID)
		return err
	}))
	add(poMeasure("staff payout list, first page", 300, workers, func(w, i int) error {
		_, _, err := p.po.ListPayouts(ctx(t), payouts.StaffFilter{}, "", 50)
		return err
	}))
	add(poMeasure("exception queue", 300, workers, func(w, i int) error {
		_, err := p.po.Exceptions(ctx(t), payouts.ExceptionFilter{})
		return err
	}))
	add(poMeasure("reconciliation references (latest 200)", 300, workers, func(w, i int) error {
		_, err := p.po.ReconReferences(ctx(t), 200)
		return err
	}))
	add(poMeasure("reconciliation run (all payouts past the grace period)", 3, 1, func(w, i int) error {
		_, err := p.po.Reconcile(ctx(t), "perf")
		return err
	}))
	var payoutsN, eventsN, decisionsN, journalsN int
	_ = p.mig.QueryRow(ctx(t), `SELECT (SELECT count(*) FROM app.payout_requests), (SELECT count(*) FROM app.payout_events),
		(SELECT count(*) FROM app.payout_eligibility_decisions), (SELECT count(*) FROM ledger.ledger_transactions WHERE source_system = 'payouts')`).
		Scan(&payoutsN, &eventsN, &decisionsN, &journalsN)
	md := fmt.Sprintf("## Payout performance\n\nmeasured %s UTC; GOMAXPROCS=%d; development stack (compose PostgreSQL 17.11, default settings); "+
		"in-process service calls; dataset after the run: %d payout requests, %d payout events, %d eligibility decisions, %d payout ledger journals.\n\n",
		time.Now().UTC().Format(time.RFC3339), runtime.GOMAXPROCS(0), payoutsN, eventsN, decisionsN, journalsN) + header + out
	fmt.Println(md)
	if dir := os.Getenv("FUNDZIM_PERF_OUT"); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "payouts-perf.md"), []byte(md), 0o644)
	}
}
