//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/ledger"
	"github.com/Fatifizo/fundzim/internal/payments"
	"github.com/Fatifizo/fundzim/internal/payouts"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/psp"
)

// Stage 19 load, contention and endurance (docs/stage-19/load-test-plan.md). Opt-in, against the local disposable
// stack only; every record is TEST data owned by this run (new campaigns, synthetic ledger owners, simulator
// payments and payouts). Every number in the report is measured in this run; nothing is extrapolated.
//
//	FUNDZIM_IT_S19_LOAD=1 [FUNDZIM_IT_S19_TIERS=1,4,8,16] [FUNDZIM_IT_S19_ENDURANCE=30m] [FUNDZIM_PERF_OUT=dir] \
//	  [FUNDZIM_IT_S19_STATS_CMD='docker stats --no-stream --format {{.Name}}={{.MemUsage}}']
//
// Workload per worker (one campaign each: a campaign has at most one payout in progress): a full payout lifecycle
// (request + reservation, two FINANCE approvals, submission, simulator completion, consumption), a donation through
// the simulator (intent, attempt, webhook success), and ledger postings (captures across campaigns, freeze/unfreeze
// pairs on the worker's campaign — the opposite-order pattern of KI-S19-01). Payout reconciliation and ledger
// integrity run concurrently.

type loadSample struct {
	at                          time.Time
	lockWaiters, active, idleTx int
	outboxPending, riverReady   int
}

type loadTier struct {
	c                               int
	dur                             time.Duration
	payoutLat, donationLat, postLat []time.Duration
	payoutErr, donationErr, postErr atomic.Int32
	reconRuns, integrityRuns        atomic.Int32
	reconMax, integrityMax          atomic.Int64
	samples                         []loadSample
	deadlocks, rollbacks, commits   int64
	payoutIDs, paymentIDs           []string
	mu                              sync.Mutex
}

func pctl(d []time.Duration, q float64) time.Duration {
	if len(d) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(a, b int) bool { return s[a] < s[b] })
	i := int(float64(len(s))*q+0.999999) - 1
	if i < 0 {
		i = 0
	}
	return s[i]
}

func msf(d time.Duration) string {
	return strconv.FormatFloat(float64(d.Microseconds())/1000, 'f', 1, 64)
}

func TestFinancialLoad(t *testing.T) {
	if os.Getenv("FUNDZIM_IT_S19_LOAD") != "1" {
		t.Skip("opt-in: FUNDZIM_IT_S19_LOAD=1 (local disposable stack only)")
	}
	tiers := []int{1, 4, 8, 16}
	if s := os.Getenv("FUNDZIM_IT_S19_TIERS"); s != "" {
		tiers = nil
		for _, x := range strings.Split(s, ",") {
			n, _ := strconv.Atoi(x)
			tiers = append(tiers, n)
		}
	}
	tierDur := 2 * time.Minute
	if s := os.Getenv("FUNDZIM_IT_S19_TIER_DURATION"); s != "" {
		tierDur, _ = time.ParseDuration(s)
	}
	var endurance time.Duration
	if s := os.Getenv("FUNDZIM_IT_S19_ENDURANCE"); s != "" {
		endurance, _ = time.ParseDuration(s)
	}
	p := newPayoutIT(t)
	pay := newPayIT(t)
	maxC := 0
	for _, c := range tiers {
		maxC = max(maxC, c)
	}
	camps := make([]string, maxC)
	for i := range camps {
		camps[i] = p.campaign(t, 100_000_000)
		if _, err := p.led.FundCampaign(ctx(t), "", camps[i], 100_000_000, ""); err != nil { // held side of freeze/unfreeze
			t.Fatal(err)
		}
	}
	var b strings.Builder
	f := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	var pgVersion, maxConns string
	_ = p.mig.QueryRow(ctx(t), `SHOW server_version`).Scan(&pgVersion)
	_ = p.mig.QueryRow(ctx(t), `SHOW max_connections`).Scan(&maxConns)
	f("## Stage 19 financial load (TestFinancialLoad)\n\nstarted %s UTC; host NumCPU=%d GOMAXPROCS=%d; %s; PostgreSQL %s (compose, max_connections %s, 1 GiB limit); "+
		"in-process API pools (DATABASE_MAX_CONNS=%q); compose worker running alongside; simulator provider; tier duration %s\n\n",
		time.Now().UTC().Format(time.RFC3339), runtime.NumCPU(), runtime.GOMAXPROCS(0), runtime.Version(), pgVersion, maxConns,
		os.Getenv("DATABASE_MAX_CONNS"), tierDur)
	stats := func(label string) {
		cmd := os.Getenv("FUNDZIM_IT_S19_STATS_CMD")
		if cmd == "" {
			return
		}
		fl := strings.Fields(cmd)
		out, err := exec.Command(fl[0], fl[1:]...).Output()
		if err == nil {
			f("container memory %s: `%s`\n\n", label, strings.Join(strings.Fields(string(out)), " "))
		}
	}
	stats("before")
	var all []*loadTier
	run := func(c int, dur time.Duration) *loadTier {
		lt := &loadTier{c: c, dur: dur}
		d0 := p.dbCounters(t)
		stop := make(chan struct{})
		var wg sync.WaitGroup
		// sampler
		wg.Add(1)
		go func() {
			defer wg.Done()
			tk := time.NewTicker(time.Second)
			defer tk.Stop()
			for {
				select {
				case <-stop:
					return
				case <-tk.C:
					s := loadSample{at: time.Now()}
					_ = p.mig.QueryRow(context.Background(), `SELECT count(*) FILTER (WHERE wait_event_type = 'Lock'), count(*) FILTER (WHERE state = 'active'),
						count(*) FILTER (WHERE state = 'idle in transaction') FROM pg_stat_activity WHERE datname = current_database()`).
						Scan(&s.lockWaiters, &s.active, &s.idleTx)
					// relay backlog: outbox events not yet dispatched to their consumers' queues
					_ = p.mig.QueryRow(context.Background(), `SELECT count(*) FROM app.outbox_events WHERE dispatched_at IS NULL
						AND dead_lettered_at IS NULL`).Scan(&s.outboxPending)
					_ = p.mig.QueryRow(context.Background(), `SELECT count(*) FROM queue.river_job WHERE state IN ('available','retryable')`).Scan(&s.riverReady)
					lt.mu.Lock()
					lt.samples = append(lt.samples, s)
					lt.mu.Unlock()
				}
			}
		}()
		// reconciliation and integrity alongside
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				case <-time.After(10 * time.Second):
				}
				s := time.Now()
				if _, err := p.po.Reconcile(context.Background(), "it-s19-load"); err == nil {
					lt.reconRuns.Add(1)
					if d := time.Since(s); int64(d) > lt.reconMax.Load() {
						lt.reconMax.Store(int64(d))
					}
				}
				if i%3 == 2 {
					s = time.Now()
					if rep, err := p.led.RunIntegrity(context.Background(), "SYSTEM", "s19-load"); err == nil && rep.Passed {
						lt.integrityRuns.Add(1)
						if d := time.Since(s); int64(d) > lt.integrityMax.Load() {
							lt.integrityMax.Store(int64(d))
						}
					} else {
						t.Errorf("integrity during load: %+v %v", rep, err)
					}
				}
			}
		}()
		deadline := time.Now().Add(dur)
		for w := 0; w < c; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				camp := camps[w]
				n := 0
				for time.Now().Before(deadline) {
					n++
					// ledger postings: a capture to a fresh owner and a freeze/unfreeze pair on the worker's campaign
					s := time.Now()
					src := ids.New()
					_, err := p.led.PostOnce(context.Background(), synth("DONATION_CAPTURED", "fixture:"+src+":s19-load", "payment", src,
						ledger.Dr(acct(ledger.ClassPSPClearing, ledger.SyntheticProviderID), 100), ledger.Cr(acct(ledger.ClassCampaignUnsettled, ids.New()), 100)))
					for _, rule := range []string{"CAMPAIGN_FROZEN", "CAMPAIGN_UNFROZEN"} {
						if err != nil {
							break
						}
						from, to, k := ledger.ClassCampaignPayable, ledger.ClassCampaignHeld, "freeze"
						if rule == "CAMPAIGN_UNFROZEN" {
							from, to, k = to, from, "unfreeze"
						}
						_, err = p.led.PostOnce(context.Background(), synth(rule, fmt.Sprintf("campaign:%s:%s:s19-%s", camp, k, ids.New()), "campaign", camp,
							ledger.Dr(acct(from, camp), 10), ledger.Cr(acct(to, camp), 10)))
					}
					lt.record(&lt.postLat, &lt.postErr, time.Since(s), err)
					// a donation through the simulator (suffix 00: success webhook after 1 s)
					s = time.Now()
					di, err := pay.s.d.Verification.Payments.CreateIntent(context.Background(), pay.donor.id, ids.New(), "s19-load",
						payments.CreateIntentInput{CampaignID: pay.campaign, AmountMinor: "1000", Currency: "USD"})
					if err == nil {
						var a payments.PaymentAttempt
						a, err = pay.s.d.Verification.Payments.CreateAttempt(context.Background(), pay.donor.id, di.ID, ids.New(), "s19-load",
							payments.CreateAttemptInput{Method: psp.MethodEcoCash})
						if err == nil {
							lt.mu.Lock()
							lt.paymentIDs = append(lt.paymentIDs, a.ID)
							lt.mu.Unlock()
						}
					}
					lt.record(&lt.donationLat, &lt.donationErr, time.Since(s), err)
					// a full payout lifecycle on the worker's campaign
					s = time.Now()
					err = p.loadPayoutCycle(camp, lt)
					lt.record(&lt.payoutLat, &lt.payoutErr, time.Since(s), err)
					if err != nil {
						t.Logf("worker %d cycle %d: %v", w, n, err)
					}
				}
			}(w)
		}
		time.Sleep(dur)
		// let the workers finish their current cycle
		for time.Now().Before(deadline.Add(3 * time.Minute)) {
			lt.mu.Lock()
			done := len(lt.payoutLat)+int(lt.payoutErr.Load()) >= len(lt.payoutIDs)
			lt.mu.Unlock()
			if done {
				break
			}
			time.Sleep(time.Second)
		}
		close(stop)
		wg.Wait()
		d1 := p.dbCounters(t)
		lt.deadlocks, lt.rollbacks, lt.commits = d1[0]-d0[0], d1[1]-d0[1], d1[2]-d0[2]
		return lt
	}
	for _, c := range tiers {
		lt := run(c, tierDur)
		all = append(all, lt)
		t.Logf("tier c=%d done: %d payout cycles, %d donations, %d posting rounds", c, len(lt.payoutLat), len(lt.donationLat), len(lt.postLat))
	}
	if endurance > 0 {
		lt := run(maxC, endurance)
		all = append(all, lt)
	}
	stats("after")
	// correctness over every record this run created
	var allPayouts, allPayments []string
	for _, lt := range all {
		allPayouts = append(allPayouts, lt.payoutIDs...)
		allPayments = append(allPayments, lt.paymentIDs...)
	}
	waitFor(t, 3*time.Minute, "donations settled", func() bool {
		var open int
		_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payment_intents WHERE id = ANY($1::uuid[]) AND status NOT IN ('SUCCEEDED')`, allPayments).Scan(&open)
		return open == 0
	})
	dup := func(q string, args ...any) int {
		var n int
		if err := p.mig.QueryRow(ctx(t), q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	dupCreates := dup(`SELECT count(*) FROM (SELECT payout_request_id FROM app.payout_attempts WHERE payout_request_id = ANY($1::uuid[])
		AND operation = 'CREATE_PAYOUT' AND classification = 'ACCEPTED' GROUP BY 1 HAVING count(*) > 1) x`, allPayouts)
	dupConsumed := dup(`SELECT count(*) FROM (SELECT payout_request_id FROM app.payout_events WHERE payout_request_id = ANY($1::uuid[])
		AND event_type = 'RESERVATION_CONSUMED' GROUP BY 1 HAVING count(*) > 1) x`, allPayouts)
	notConsumed := dup(`SELECT count(*) FROM app.payout_requests p JOIN app.payout_reservations r ON r.payout_request_id = p.id
		WHERE p.id = ANY($1::uuid[]) AND p.status = 'COMPLETED' AND r.status <> 'CONSUMED'`, allPayouts)
	dupSucceeded := dup(`SELECT count(*) FROM (SELECT payment_intent_id FROM app.payment_events WHERE payment_intent_id = ANY($1::uuid[])
		AND to_status = 'SUCCEEDED' AND applied GROUP BY 1 HAVING count(*) > 1) x`, allPayments)
	dupNotices := dup(`SELECT count(*) FROM (SELECT aggregate_id FROM app.outbox_events WHERE aggregate_id = ANY($1::uuid[])
		AND event_type = 'payments.payment_succeeded' GROUP BY 1 HAVING count(*) > 1) x`, allPayments)
	mismatches := dup(`SELECT count(*) FROM app.payout_reconciliation_references WHERE payout_request_id = ANY($1::uuid[])`, allPayouts)
	rep, err := p.led.RunIntegrity(ctx(t), "SYSTEM", "s19-load-final")
	if err != nil {
		t.Fatal(err)
	}

	f("| tier | c | duration | payout lifecycles (n / err) | payout p50 / p95 / p99 s | donations (n / err) | donation p50 / p95 / p99 ms | posting rounds (n / err) | posting p50 / p95 / p99 ms | recon runs (max s) | integrity runs (max s) | deadlocks | rollbacks / commits | max lock waiters | max active | max idle-in-tx | max outbox undispatched | max River jobs ready |\n")
	f("|---|---:|---|---|---|---|---|---|---|---|---|---:|---|---:|---:|---:|---:|---:|\n")
	for i, lt := range all {
		name := "B/D"
		if lt.c == 1 {
			name = "A"
		}
		if endurance > 0 && i == len(all)-1 {
			name = "E"
		}
		var mlw, ma, mi, mo, mr int
		for _, s := range lt.samples {
			mlw, ma, mi, mo, mr = max(mlw, s.lockWaiters), max(ma, s.active), max(mi, s.idleTx), max(mo, s.outboxPending), max(mr, s.riverReady)
		}
		sec := func(d time.Duration) string { return strconv.FormatFloat(d.Seconds(), 'f', 2, 64) }
		f("| %s | %d | %s | %d / %d | %s / %s / %s | %d / %d | %s / %s / %s | %d / %d | %s / %s / %s | %d (%s) | %d (%s) | %d | %d / %d | %d | %d | %d | %d | %d |\n",
			name, lt.c, lt.dur, len(lt.payoutLat), lt.payoutErr.Load(), sec(pctl(lt.payoutLat, .5)), sec(pctl(lt.payoutLat, .95)), sec(pctl(lt.payoutLat, .99)),
			len(lt.donationLat), lt.donationErr.Load(), msf(pctl(lt.donationLat, .5)), msf(pctl(lt.donationLat, .95)), msf(pctl(lt.donationLat, .99)),
			len(lt.postLat), lt.postErr.Load(), msf(pctl(lt.postLat, .5)), msf(pctl(lt.postLat, .95)), msf(pctl(lt.postLat, .99)),
			lt.reconRuns.Load(), sec(time.Duration(lt.reconMax.Load())), lt.integrityRuns.Load(), sec(time.Duration(lt.integrityMax.Load())),
			lt.deadlocks, lt.rollbacks, lt.commits, mlw, ma, mi, mo, mr)
	}
	f("\nA posting round = one capture plus one freeze/unfreeze pair (3 journals). Deadlocks, rollbacks and commits are database-wide deltas over the tier (pg_stat_database; the compose worker's own work is included). Samples every second.\n\n")
	f("Correctness over this run's %d payouts and %d donations: payouts with more than one accepted CREATE_PAYOUT %d; payouts consumed more than once %d; COMPLETED payouts not CONSUMED %d; payments SUCCEEDED more than once %d; duplicate success notices %d; payout reconciliation references %d; ledger integrity after the run: passed=%v.\n",
		len(allPayouts), len(allPayments), dupCreates, dupConsumed, notConsumed, dupSucceeded, dupNotices, mismatches, rep.Passed)
	t.Log("\n" + b.String())
	if dir := os.Getenv("FUNDZIM_PERF_OUT"); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(dir+"/stage19-financial-load.md", []byte(b.String()), 0o644)
	}
	if dupCreates+dupConsumed+notConsumed+dupSucceeded+dupNotices+mismatches != 0 || !rep.Passed {
		t.Fatal("a financial invariant failed under load (see the report)")
	}
}

func (lt *loadTier) record(lat *[]time.Duration, errsN *atomic.Int32, d time.Duration, err error) {
	if err != nil {
		errsN.Add(1)
		return
	}
	lt.mu.Lock()
	*lat = append(*lat, d)
	lt.mu.Unlock()
}

// dbCounters: deadlocks, rollbacks, commits of the database so far.
func (p *payoutIT) dbCounters(t *testing.T) [3]int64 {
	var c [3]int64
	_ = p.mig.QueryRow(ctx(t), `SELECT deadlocks, xact_rollback, xact_commit FROM pg_stat_database WHERE datname = current_database()`).Scan(&c[0], &c[1], &c[2])
	return c
}

// loadPayoutCycle runs one payout from request to consumption on camp (service calls; FINANCE approvals with a fresh
// step-up timestamp as the route would record).
func (p *payoutIT) loadPayoutCycle(camp string, lt *loadTier) error {
	c, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	po, err := p.po.RequestPayout(c, p.owner.id, true, camp, payouts.RequestInput{Currency: "USD", AmountMinor: "1000", DestinationID: p.dest}, "s19-load-"+ids.New())
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	lt.mu.Lock()
	lt.payoutIDs = append(lt.payoutIDs, po.ID)
	lt.mu.Unlock()
	for _, f := range []staffCred{p.f.finance1, p.f.finance2} {
		for {
			cur, err := p.po.StaffDetail(c, po.ID)
			if err != nil {
				return err
			}
			if cur.Payout.Status == payouts.StReview {
				break
			}
			if cur.Payout.Status == payouts.StApproved {
				goto approved
			}
			select {
			case <-c.Done():
				return fmt.Errorf("not reviewable: %s", cur.Payout.Status)
			case <-time.After(100 * time.Millisecond):
			}
		}
		if _, err := p.po.ApprovePayout(c, f.id, time.Now(), po.ID, payouts.DecisionInput{Reason: "stage 19 load approval", ConflictAttested: true}); err != nil {
			return fmt.Errorf("approve: %w", err)
		}
	}
approved:
	if _, err := p.po.SubmitNow(c, po.ID); err != nil {
		return fmt.Errorf("submit: %w", err)
	}
	for {
		_, _ = p.s.d.Verification.Sandbox.DeliverDue(c, 20)
		_, _ = p.po.PollDue(c, 10)
		cur, err := p.po.StaffDetail(c, po.ID)
		if err != nil {
			return err
		}
		if cur.Payout.Status == payouts.StCompleted && cur.Payout.Reservation != nil && cur.Payout.Reservation.Status == payouts.ResConsumed {
			return nil
		}
		if cur.Payout.Status == payouts.StFailed || cur.Payout.Status == payouts.StRejected || cur.Payout.Status == payouts.StCancelled {
			return fmt.Errorf("payout ended %s", cur.Payout.Status)
		}
		select {
		case <-c.Done():
			return fmt.Errorf("payout not settled: %s", cur.Payout.Status)
		case <-time.After(200 * time.Millisecond):
		}
	}
}
