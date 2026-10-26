package reconsweep

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"
)

// memStore is an in-memory Store with the SQL store's semantics (compare-and-swap on version, one RUNNING cycle,
// cursor never backwards, completion opens the next cycle carrying deferrals). failAdvance injects a failure of the
// checkpoint write (a crash or database error after the batch was examined).
type memStore struct {
	mu          sync.Mutex
	cycles      []Checkpoint // index = cycle-1
	completed   map[int64]bool
	failAdvance error
	beforeCAS   func(ctx context.Context) // runs inside Advance before the version check (concurrency interleavings)
	attempts    []string                  // stop reason of every recorded run
	failed      int                       // consecutive failed runs
}

func newMemStore() *memStore { return &memStore{completed: map[int64]bool{}} }

func (m *memStore) Active(_ context.Context, detector, scope string) (Checkpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.cycles) == 0 {
		m.cycles = append(m.cycles, Checkpoint{ID: "c1", Detector: detector, Scope: scope, Cycle: 1, StartedAt: time.Unix(0, 0)})
	}
	c := m.cycles[len(m.cycles)-1]
	c.Deferred = append([]Deferral(nil), c.Deferred...)
	return c, nil
}

func (m *memStore) Advance(ctx context.Context, cur, next Checkpoint, complete bool, _ string) (bool, error) {
	if m.beforeCAS != nil {
		m.beforeCAS(ctx)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failAdvance != nil {
		return false, m.failAdvance
	}
	i := int(cur.Cycle - 1)
	if i != len(m.cycles)-1 || m.completed[cur.Cycle] || m.cycles[i].Version != cur.Version {
		return false, nil
	}
	if m.cycles[i].Cursor != "" && next.Cursor < m.cycles[i].Cursor {
		return false, fmt.Errorf("cursor moved backwards: %s -> %s", m.cycles[i].Cursor, next.Cursor)
	}
	next.Version = cur.Version + 1
	next.Deferred = append([]Deferral(nil), next.Deferred...)
	m.cycles[i] = next
	if complete {
		m.completed[cur.Cycle] = true
		m.cycles = append(m.cycles, Checkpoint{ID: fmt.Sprintf("c%d", cur.Cycle+1), Detector: cur.Detector, Scope: cur.Scope, Cycle: cur.Cycle + 1,
			Deferred: append([]Deferral(nil), next.Deferred...)})
	}
	return true, nil
}

func (m *memStore) Coverage(context.Context, string, string) (Coverage, error) {
	return Coverage{}, nil
}

// RecordAttempt keeps the latest outcome and the consecutive failures, as the SQL store does on the RUNNING row.
func (m *memStore) RecordAttempt(_ context.Context, _, _ string, rep Report) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.attempts = append(m.attempts, rep.StoppedBy)
	if rep.Error != "" {
		m.failed++
	} else {
		m.failed = 0
	}
	return nil
}

func (m *memStore) cycle() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.cycles) == 0 {
		return 1
	}
	return m.cycles[len(m.cycles)-1].Cycle
}

// table is a keyed record set whose state the tests change between and during runs.
type table struct {
	mu       sync.Mutex
	keys     map[string]bool
	state    map[string]string // record -> current state ("ok", "bad", "hot" = inside grace, "gone" = out of scope)
	examined map[string]int    // per cycle-insensitive count of examinations
	seen     map[string]string // last state examined
	findings map[string]bool   // deduplicated findings: "key:state"
	failOn   string            // examine of this key fails (database error)
	failNext error             // next() fails
	onExam   func(k string)    // runs at each examination (to change data mid-run)
}

func newTable(n int) *table {
	t := &table{keys: map[string]bool{}, state: map[string]string{}, examined: map[string]int{}, seen: map[string]string{}, findings: map[string]bool{}}
	for i := 0; i < n; i++ {
		t.add(key(i), "ok")
	}
	return t
}

func key(i int) string { return fmt.Sprintf("k%08d", i) }

func (t *table) add(k, st string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.keys[k], t.state[k] = true, st
}

func (t *table) set(k, st string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state[k] = st
}

func (t *table) next(_ context.Context, after string, limit int) ([]string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.failNext != nil {
		return nil, t.failNext
	}
	var ks []string
	for k := range t.keys {
		if k > after {
			ks = append(ks, k)
		}
	}
	sort.Strings(ks)
	if len(ks) > limit {
		ks = ks[:limit]
	}
	return ks, nil
}

func (t *table) examine(_ context.Context, k string) (Outcome, error) {
	if t.onExam != nil {
		t.onExam(k)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if k == t.failOn {
		return Outcome{}, errors.New("db: connection reset")
	}
	st := t.state[k]
	t.examined[k]++
	t.seen[k] = st
	switch st {
	case "hot":
		return Outcome{Deferred: true, Reason: "GRACE_PERIOD"}, nil
	case "down": // a required external check could not be made (provider unreachable): not judged, carried
		return Outcome{Deferred: true, Reason: "PROVIDER_UNVERIFIED"}, nil
	case "gone":
		return Outcome{OutOfScope: true}, nil
	case "bad":
		f := k + ":bad"
		if t.findings[f] {
			return Outcome{}, nil // already recorded (deduplicated by the owner)
		}
		t.findings[f] = true
		return Outcome{Exceptions: 1}, nil
	}
	return Outcome{}, nil
}

func (t *table) resetExamined() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.examined = map[string]int{}
}

func opts(batch, max int) Options {
	return Options{Config: Config{Scope: "test", BatchSize: batch, MaxRecords: max, MaxDuration: time.Hour}, Detector: "D", RunID: "r"}
}

func run(t *testing.T, st Store, tb *table, o Options) Report {
	t.Helper()
	rep, err := Run(context.Background(), st, o, tb.next, tb.examine)
	if err != nil {
		t.Fatalf("run: %v (%+v)", err, rep)
	}
	return rep
}

// runCycle runs until the current cycle completes and returns the number of runs it took.
func runCycle(t *testing.T, st *memStore, tb *table, o Options) int {
	t.Helper()
	c := st.cycle()
	for n := 1; n <= 10000; n++ {
		run(t, st, tb, o)
		if st.cycle() > c {
			return n
		}
	}
	t.Fatal("cycle never completed")
	return 0
}

func assertAllExamined(t *testing.T, tb *table) {
	t.Helper()
	tb.mu.Lock()
	defer tb.mu.Unlock()
	for k := range tb.keys {
		if tb.examined[k] == 0 {
			t.Fatalf("record %s was never examined", k)
		}
	}
}

// 1, 2, 21, 25: far more records than one batch or one run's budget; a cycle spans many runs and examines every
// record exactly once, old and new alike; the walk resumes where the previous run stopped.
func TestSweepCoversEveryRecordAcrossRuns(t *testing.T) {
	tb, st := newTable(12345), newMemStore()
	runs := runCycle(t, st, tb, opts(100, 1000))
	if runs != 13 {
		t.Fatalf("12 345 records at 1 000 per run should take 13 runs, took %d", runs)
	}
	assertAllExamined(t, tb)
	for k, n := range tb.examined {
		if n != 1 {
			t.Fatalf("%s examined %d times in one cycle", k, n)
		}
	}
	// resumption: the run after a partial walk starts after the checkpoint, not from the start
	tb2, st2 := newTable(500), newMemStore()
	r1 := run(t, st2, tb2, opts(50, 200))
	r2 := run(t, st2, tb2, opts(50, 200))
	if r1.CursorTo != key(199) || r2.CursorFrom != key(199) || r2.CursorTo != key(399) || tb2.examined[key(0)] != 1 {
		t.Fatalf("resume: r1 %s→%s, r2 %s→%s", r1.CursorFrom, r1.CursorTo, r2.CursorFrom, r2.CursorTo)
	}
}

// 3, 16: a discrepancy introduced in an already-examined old record is found by the next cycle; a later new
// discrepancy on a previously reconciled record is recorded too.
func TestSweepReexaminesChangedRecordsNextCycle(t *testing.T) {
	tb, st := newTable(1000), newMemStore()
	runCycle(t, st, tb, opts(100, 300))
	if len(tb.findings) != 0 {
		t.Fatal("clean data produced findings")
	}
	tb.set(key(3), "bad") // the oldest records changed after the cycle examined them
	runCycle(t, st, tb, opts(100, 300))
	if !tb.findings[key(3)+":bad"] {
		t.Fatal("a discrepancy in an old record was not found by the next cycle")
	}
	// persistent discrepancy (14): later cycles do not record it again
	before := len(tb.findings)
	runCycle(t, st, tb, opts(100, 300))
	runCycle(t, st, tb, opts(100, 300))
	if len(tb.findings) != before {
		t.Fatal("a persistent discrepancy was recorded more than once")
	}
}

// 4, 5: records committed during a cycle — above the cursor they are reached in the same cycle; below it, by the next
// cycle (which starts after their commit). A record updated during the run is examined in its new state no later
// than the next cycle.
func TestSweepRecordsCommittedDuringACycle(t *testing.T) {
	tb, st := newTable(1000), newMemStore()
	run(t, st, tb, opts(100, 500)) // cursor now at k499
	tb.add("k00000250x", "bad")    // below the cursor (an id generated "late")
	tb.add(key(5000), "bad")       // above the cursor
	tb.set(key(10), "bad")         // already examined in this cycle, then changed
	runCycle(t, st, tb, opts(100, 500))
	if !tb.findings[key(5000)+":bad"] {
		t.Fatal("a record above the cursor was not reached in the same cycle")
	}
	if tb.findings["k00000250x:bad"] || tb.findings[key(10)+":bad"] {
		t.Fatal("unexpected: a record behind the cursor was examined again in the same cycle")
	}
	runCycle(t, st, tb, opts(100, 500))
	if !tb.findings["k00000250x:bad"] || !tb.findings[key(10)+":bad"] {
		t.Fatal("the next cycle did not examine records committed or changed behind the cursor")
	}
	// boundary: a record inserted with exactly the next key after a checkpoint is not skipped
	tb2, st2 := newTable(10), newMemStore()
	run(t, st2, tb2, opts(5, 5)) // cursor k00000004
	tb2.add("k00000004a", "bad")
	runCycle(t, st2, tb2, opts(5, 5))
	if !tb2.findings["k00000004a:bad"] {
		t.Fatal("a record committed right after the checkpoint key was skipped")
	}
}

// 7, 8, 10: a failure while examining a batch (database error, crash) or while writing the checkpoint leaves the
// checkpoint at the last fully examined batch; the next run re-examines the batch and nothing is skipped.
func TestSweepFailureBeforeCheckpointNeverSkips(t *testing.T) {
	tb, st := newTable(300), newMemStore()
	run(t, st, tb, opts(100, 100)) // k0..k99 done
	tb.failOn = key(150)
	if _, err := Run(context.Background(), st, opts(100, 1000), tb.next, tb.examine); err == nil {
		t.Fatal("the examine failure was not reported")
	}
	if c, _ := st.Active(context.Background(), "D", "test"); c.Cursor != key(99) {
		t.Fatalf("checkpoint advanced past a failed batch: %s", c.Cursor)
	}
	tb.failOn = ""
	st.failAdvance = errors.New("db: checkpoint write failed (crash after processing)")
	if _, err := Run(context.Background(), st, opts(100, 1000), tb.next, tb.examine); err == nil {
		t.Fatal("the checkpoint failure was not reported")
	}
	if c, _ := st.Active(context.Background(), "D", "test"); c.Cursor != key(99) {
		t.Fatalf("checkpoint moved without a successful write: %s", c.Cursor)
	}
	st.failAdvance = nil
	tb.failNext = errors.New("db: query canceled")
	if rep, err := Run(context.Background(), st, opts(100, 1000), tb.next, tb.examine); err == nil || rep.StoppedBy != StopError {
		t.Fatal("a failing key query was not reported")
	}
	tb.failNext = nil
	tb.resetExamined()
	runCycle(t, st, tb, opts(100, 1000)) // worker restart (11): a fresh run resumes from the store
	for i := 100; i < 300; i++ {
		if tb.examined[key(i)] == 0 {
			t.Fatalf("%s skipped after the failures", key(i))
		}
	}
	if tb.examined[key(0)] != 0 {
		t.Fatal("records before the checkpoint were re-walked in the same cycle")
	}
}

// 9: a crash after the checkpoint advanced loses nothing and repeats nothing: the next run starts after it.
func TestSweepCrashAfterCheckpoint(t *testing.T) {
	tb, st := newTable(300), newMemStore()
	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	exam := func(ctx context.Context, k string) (Outcome, error) {
		n++
		return tb.examine(ctx, k)
	}
	// the process dies right after the first checkpoint: simulate by cancelling and failing the next examine
	exam2 := func(c context.Context, k string) (Outcome, error) {
		if n >= 100 {
			cancel()
			return Outcome{}, context.Canceled
		}
		return exam(c, k)
	}
	if _, err := Run(ctx, st, opts(100, 1000), tb.next, exam2); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected the simulated crash, got %v", err)
	}
	c, _ := st.Active(context.Background(), "D", "test")
	if c.Cursor != key(99) || c.Examined != 100 {
		t.Fatalf("checkpoint after crash: %s / %d", c.Cursor, c.Examined)
	}
	tb.resetExamined()
	r := run(t, st, tb, opts(100, 1000))
	if r.CursorFrom != key(99) || tb.examined[key(99)] != 0 || tb.examined[key(100)] != 1 {
		t.Fatalf("resume after a completed checkpoint: from %s", r.CursorFrom)
	}
}

// 12, 13: two runs read the same checkpoint and examine the same batch; only one advances it, the other stops with a
// conflict; findings are recorded once; the union covers everything and the cursor never regresses.
func TestSweepConcurrentRunsNeverSkipOrDuplicate(t *testing.T) {
	tb, st := newTable(1000), newMemStore()
	tb.set(key(42), "bad")
	var a, b Report
	var errA, errB error
	type runKey struct{}
	gate, ready := make(chan struct{}), make(chan struct{})
	st.beforeCAS = func(ctx context.Context) {
		if ctx.Value(runKey{}) == "A" { // A has examined its batch; it waits until B has examined and advanced
			close(ready)
			<-gate
		}
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		a, errA = Run(context.WithValue(context.Background(), runKey{}, "A"), st, opts(100, 100), tb.next, tb.examine)
	}()
	<-ready
	b, errB = Run(context.Background(), st, opts(100, 100), tb.next, tb.examine)
	close(gate)
	wg.Wait()
	if errA != nil || errB != nil {
		t.Fatal(errA, errB)
	}
	if (a.Advanced && b.Advanced) || (!a.Advanced && !b.Advanced) {
		t.Fatalf("exactly one run must advance the shared checkpoint: a=%v b=%v", a.Advanced, b.Advanced)
	}
	loser := a
	if a.Advanced {
		loser = b
	}
	if loser.StoppedBy != StopConflict {
		t.Fatalf("the losing run stopped by %s", loser.StoppedBy)
	}
	if tb.examined[key(42)] != 2 || len(tb.findings) != 1 {
		t.Fatalf("duplicate batch: examined %d times, %d findings", tb.examined[key(42)], len(tb.findings))
	}
	st.beforeCAS = nil
	runCycle(t, st, tb, opts(100, 300))
	assertAllExamined(t, tb)
}

// Deferrals (grace period): a record that may not be judged yet is carried — never passed — across batches, runs and
// cycles, retried at the start of each run, and examined as soon as it may be. 23: a record leaving the scope during
// processing is counted out of scope.
func TestSweepDeferralsAreCarriedUntilExamined(t *testing.T) {
	tb, st := newTable(200), newMemStore()
	tb.set(key(5), "hot")
	tb.set(key(150), "hot")
	tb.set(key(7), "gone")
	r := run(t, st, tb, opts(100, 100))
	if r.DeferredNew != 1 || r.DeferredCarried != 1 || r.OldestDeferral == nil || r.OutOfScope != 1 {
		t.Fatalf("first run: %+v", r)
	}
	r = run(t, st, tb, opts(100, 1000)) // completes the cycle with both deferrals carried
	if !r.CycleCompleted || r.DeferredCarried != 2 {
		t.Fatalf("cycle end: %+v", r)
	}
	c, _ := st.Active(context.Background(), "D", "test")
	if c.Cycle != 2 || len(c.Deferred) != 2 {
		t.Fatalf("deferrals not carried into the next cycle: %+v", c)
	}
	tb.set(key(5), "bad") // left its grace period, now inconsistent
	tb.set(key(150), "ok")
	r = run(t, st, tb, opts(100, 1000))
	if r.DeferralsRetried != 2 || r.DeferralsResolved != 2 || r.DeferredCarried != 0 || !tb.findings[key(5)+":bad"] {
		t.Fatalf("deferral retry: %+v", r)
	}
	// fail closed: more deferrals than allowed → the checkpoint is not advanced
	tb2, st2 := newTable(50), newMemStore()
	for i := 0; i < 50; i++ {
		tb2.set(key(i), "hot")
	}
	o := opts(50, 1000)
	o.MaxDeferred = 10
	rep, err := Run(context.Background(), st2, o, tb2.next, tb2.examine)
	if !errors.Is(err, ErrDeferralLimit) || rep.StoppedBy != StopDeferralLimit || rep.Advanced {
		t.Fatalf("deferral limit: %v %+v", err, rep)
	}
	if c, _ := st2.Active(context.Background(), "D", "test"); c.Cursor != "" {
		t.Fatal("checkpoint advanced past more deferrals than allowed")
	}
}

// 22: an empty scope completes a cycle and records nothing.
func TestSweepEmptyScope(t *testing.T) {
	tb, st := newTable(0), newMemStore()
	r := run(t, st, tb, opts(100, 1000))
	if !r.CycleCompleted || r.Examined != 0 || r.Exceptions != 0 || st.cycle() != 2 {
		t.Fatalf("empty scope: %+v", r)
	}
}

// 24: a run that exceeds its time budget stops — since remediation phase 3 also inside a batch (F-R2-02: slow
// external checks must not hold a batch past the budget) — with its checkpoint at the last examined key; the next run
// resumes right after it and nothing is skipped.
func TestSweepTimeBudget(t *testing.T) {
	tb, st := newTable(1000), newMemStore()
	now := time.Unix(1000, 0)
	tb.onExam = func(string) { now = now.Add(10 * time.Millisecond) } // 1 s per 100-record batch
	o := opts(100, 100000)
	o.MaxDuration = 2500 * time.Millisecond
	o.Now = func() time.Time { return now }
	r := run(t, st, tb, o)
	if r.StoppedBy != StopTimeBudget || r.Examined != 250 || r.CursorTo != key(249) || r.CycleCompleted {
		t.Fatalf("time budget: %+v", r)
	}
	r = run(t, st, tb, o)
	if r.CursorFrom != key(249) || tb.examined[key(250)] != 1 || tb.examined[key(249)] != 1 {
		t.Fatalf("resume after the time budget: %+v", r)
	}
}

// Record budget smaller than the batch and deferral retries consuming the budget still move the walk forward.
func TestSweepBudgetAlwaysMakesProgress(t *testing.T) {
	tb, st := newTable(100), newMemStore()
	for i := 0; i < 5; i++ {
		tb.set(key(i), "hot")
	}
	run(t, st, tb, opts(5, 5)) // defers k0..k4
	for i := 0; i < 20; i++ {
		r := run(t, st, tb, opts(5, 5)) // retries use the whole budget
		if r.Examined == 0 && !r.CycleCompleted {
			t.Fatalf("run %d made no progress: %+v", i, r)
		}
	}
}

// F-R2-02: records whose required external check keeps failing are carried, never passed, and never stop the walk:
// the rest of the scope completes its cycles, each run retries at most half its budget of deferrals, rotation reaches
// every deferral, and a deferred record found inconsistent once its check succeeds is recorded then.
func TestSweepFailingChecksNeverStopTheWalk(t *testing.T) {
	tb, st := newTable(1000), newMemStore()
	for i := 0; i < 1000; i += 10 { // 100 records whose provider check fails
		tb.set(key(i), "down")
	}
	o := opts(50, 100)
	runs := runCycle(t, st, tb, o)
	c, _ := st.Active(context.Background(), "D", "test")
	if len(c.Deferred) != 100 {
		t.Fatalf("expected the 100 unverifiable records carried, got %d", len(c.Deferred))
	}
	for i := 0; i < 1000; i++ {
		if tb.examined[key(i)] == 0 {
			t.Fatalf("%s never examined although other records' checks failed", key(i))
		}
	}
	t.Logf("cycle of 1 000 records with 100 failing checks completed in %d runs", runs)
	// bounded retries with rotation: every deferral is retried within a few runs, none starves
	tb.resetExamined()
	for r := 0; r < 4; r++ {
		rep := run(t, st, tb, o)
		if rep.DeferralsRetried > 50 {
			t.Fatalf("run retried %d deferrals, more than half its 100-record budget", rep.DeferralsRetried)
		}
		if rep.DeferredReasons["PROVIDER_UNVERIFIED"] == 0 {
			t.Fatalf("deferral reasons not reported: %+v", rep.DeferredReasons)
		}
		if rep.Examined == 0 && !rep.CycleCompleted {
			t.Fatalf("the walk made no progress while deferrals were retried: %+v", rep)
		}
	}
	for i := 0; i < 1000; i += 10 {
		if tb.examined[key(i)] == 0 {
			t.Fatalf("deferral %s was never retried in 4 runs (rotation starves it)", key(i))
		}
	}
	// the provider recovers: one record is genuinely inconsistent, the rest are fine
	for i := 0; i < 1000; i += 10 {
		tb.set(key(i), "ok")
	}
	tb.set(key(500), "bad")
	for r := 0; r < 5; r++ {
		run(t, st, tb, o)
	}
	c, _ = st.Active(context.Background(), "D", "test")
	if len(c.Deferred) != 0 || !tb.findings[key(500)+":bad"] {
		t.Fatalf("after recovery: %d deferrals left, finding recorded = %v", len(c.Deferred), tb.findings[key(500)+":bad"])
	}
}

// F-R2-02: slow checks (each taking long, e.g. timeouts) inside one batch never hold the run past its time budget,
// deferral retries stop at the budget too, and the next run resumes at the last examined key.
func TestSweepSlowChecksRespectTheTimeBudget(t *testing.T) {
	tb, st := newTable(400), newMemStore()
	now := time.Unix(1000, 0)
	tb.onExam = func(string) { now = now.Add(10 * time.Second) } // a 10 s timeout per check
	o := opts(200, 100000)
	o.MaxDuration = time.Minute
	o.Now = func() time.Time { return now }
	r := run(t, st, tb, o)
	if r.StoppedBy != StopTimeBudget || r.Examined != 6 || r.CursorTo != key(5) || !r.Advanced {
		t.Fatalf("one 200-record batch of slow checks must stop at the budget: %+v", r)
	}
	for i := 6; i < 400; i++ {
		tb.set(key(i), "down")
	}
	tb.onExam = func(string) { now = now.Add(10 * time.Second) }
	r = run(t, st, tb, o) // defers 6 more slow records, then a third run retries them within the budget
	r = run(t, st, tb, o)
	if r.DeferralsRetried == 0 || r.DeferralsRetried > 6 || r.DeferralsNotRetried+r.DeferralsRetried != 6 {
		t.Fatalf("deferral retries must stop at the time budget and keep the rest: %+v", r)
	}
	if r.Examined == 0 {
		t.Fatalf("the walk must still examine at least one record: %+v", r)
	}
}

// F-R2-03: every run's outcome is recorded — successes reset the failure count, errors and the deferral limit raise it.
func TestSweepRecordsEveryAttempt(t *testing.T) {
	tb, st := newTable(100), newMemStore()
	run(t, st, tb, opts(50, 50))
	tb.failOn = key(60)
	for i := 0; i < 2; i++ {
		if _, err := Run(context.Background(), st, opts(50, 50), tb.next, tb.examine); err == nil {
			t.Fatal("expected the examine failure")
		}
	}
	if st.failed != 2 || st.attempts[len(st.attempts)-1] != StopError {
		t.Fatalf("failed attempts %d, outcomes %v", st.failed, st.attempts)
	}
	tb.failOn = ""
	run(t, st, tb, opts(50, 50))
	if st.failed != 0 || len(st.attempts) != 4 {
		t.Fatalf("a successful run must reset the count: %d %v", st.failed, st.attempts)
	}
	tb2, st2 := newTable(30), newMemStore()
	for i := 0; i < 30; i++ {
		tb2.set(key(i), "down")
	}
	o := opts(30, 100)
	o.MaxDeferred = 5
	if _, err := Run(context.Background(), st2, o, tb2.next, tb2.examine); !errors.Is(err, ErrDeferralLimit) {
		t.Fatal(err)
	}
	if st2.failed != 1 || st2.attempts[0] != StopDeferralLimit {
		t.Fatalf("deferral limit not recorded: %d %v", st2.failed, st2.attempts)
	}
}

// F-R2-03: the health states are distinguished from the checkpoint evidence alone.
func TestHealthClassification(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { x := now.Add(-d); return &x }
	th := DefaultThresholds
	cases := []struct {
		name      string
		c         Coverage
		open      int
		want      string
		wantCycle string
	}{
		{"never ran", Coverage{}, 0, HealthNotStarted, LastCycleNone},
		{"progressing", Coverage{RunningStartedAt: at(30 * time.Minute), LastAdvancedAt: at(10 * time.Minute)}, 0, HealthProgressing, LastCycleNone},
		{"progressing slowly", Coverage{RunningStartedAt: at(30 * time.Hour), LastAdvancedAt: at(10 * time.Minute)}, 0, HealthProgressingSlowly, LastCycleNone},
		{"stalled", Coverage{RunningStartedAt: at(5 * time.Hour), LastAdvancedAt: at(2 * time.Hour), LastAttemptOutcome: StopTimeBudget}, 0, HealthStalled, LastCycleNone},
		{"stalled: worker stopped, nothing recorded", Coverage{RunningStartedAt: at(3 * time.Hour)}, 0, HealthStalled, LastCycleNone},
		{"failing", Coverage{RunningStartedAt: at(time.Hour), LastAdvancedAt: at(50 * time.Minute), FailedAttempts: 3, LastAttemptOutcome: StopError},
			0, HealthFailing, LastCycleNone},
		{"one failure while advancing is not yet failing", Coverage{RunningStartedAt: at(time.Hour), LastAdvancedAt: at(5 * time.Minute), FailedAttempts: 1},
			0, HealthProgressing, LastCycleNone},
		{"blocked: deferral limit", Coverage{RunningStartedAt: at(time.Hour), LastAdvancedAt: at(5 * time.Minute), LastAttemptOutcome: StopDeferralLimit,
			FailedAttempts: 1}, 0, HealthBlockedOnDeferrals, LastCycleNone},
		{"blocked: old deferral", Coverage{RunningStartedAt: at(time.Hour), LastAdvancedAt: at(5 * time.Minute), DeferredOutstanding: 1,
			OldestDeferralSince: at(30 * time.Hour)}, 0, HealthBlockedOnDeferrals, LastCycleNone},
		{"completed, clean", Coverage{LastCompletedCycleStartedAt: at(20 * time.Minute), RunningStartedAt: at(5 * time.Minute)}, 0, HealthProgressing, LastCycleClean},
		{"completed, discrepancies open", Coverage{LastCompletedCycleStartedAt: at(20 * time.Minute), RunningStartedAt: at(5 * time.Minute)}, 3,
			HealthProgressing, LastCycleDiscrepancies},
	}
	for _, c := range cases {
		h := c.c.Health(now, th, 10000, c.open)
		if h.State != c.want || h.LastCycle != c.wantCycle {
			t.Errorf("%s: state %s / %s, want %s / %s (%s)", c.name, h.State, h.LastCycle, c.want, c.wantCycle, h.Reason)
		}
		m := h.Metric("m", "m.k", "l", "p", "s")
		if m.State != h.State || m.Status == "HEALTHY" {
			t.Errorf("%s: metric %+v", c.name, m)
		}
	}
	if h := (Coverage{RunningStartedAt: at(time.Hour), LastAdvancedAt: at(time.Minute), DeferredOutstanding: 10000}).Health(now, th, 10000, 0); h.State != HealthBlockedOnDeferrals {
		t.Errorf("full deferral list: %s", h.State)
	}
}
