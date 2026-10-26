package reconsweep

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

// Remediation phase 4: checkpoint integrity. The engine writes evidence with every advance; Verify compares it with the
// checkpoints and — independently of the walk's keyset reads — with the scope itself (table.source).

func verify(t *testing.T, st *memStore, tb *table) IntegrityReport {
	t.Helper()
	r, err := Verify(context.Background(), st, tb.source, IntegrityOptions{Detector: "D", Scope: "test", Margin: time.Nanosecond, MaxRuntime: time.Minute})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	return r
}

func codes(r IntegrityReport) []string {
	var out []string
	for _, p := range r.Problems {
		out = append(out, p.Code)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func wantVerified(t *testing.T, what string, r IntegrityReport) {
	t.Helper()
	if r.Status != IntegrityVerified {
		t.Fatalf("%s: integrity %s, problems %+v", what, r.Status, r.Problems)
	}
}

// Every way the engine legitimately moves — budgets, deferrals (grace and failed external checks), out-of-scope records,
// findings, failures before the checkpoint, a crash after it, concurrent runs, cycle completion — leaves evidence that
// verifies; deferred records are accounted for as deferrals, never as reconciled.
func TestVerifyEngineEvidenceIsConsistent(t *testing.T) {
	tb, st := newTable(1000), newMemStore()
	tb.set(key(5), "hot")
	tb.set(key(77), "down")
	tb.set(key(300), "gone")
	tb.set(key(42), "bad")
	o := opts(100, 250)
	run(t, st, tb, o)
	wantVerified(t, "after one run", verify(t, st, tb))
	// interrupted: examine fails mid-batch — nothing is written for the failed batch
	tb.failOn = key(410)
	if _, err := Run(context.Background(), st, o, tb.next, tb.examine); err == nil {
		t.Fatal("expected the examine failure")
	}
	tb.failOn = ""
	wantVerified(t, "after an interrupted batch", verify(t, st, tb))
	// the checkpoint write fails (crash after the batch was examined, before the advance)
	st.failAdvance = errors.New("db: connection lost")
	_, _ = Run(context.Background(), st, o, tb.next, tb.examine)
	st.failAdvance = nil
	wantVerified(t, "after a failed advance", verify(t, st, tb))
	// two concurrent runs: only the winner's evidence exists
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = Run(context.Background(), st, o, tb.next, tb.examine) }()
	}
	wg.Wait()
	wantVerified(t, "after concurrent runs", verify(t, st, tb))
	runCycle(t, st, tb, o) // completes cycle 1 with deferrals carried into cycle 2
	r := verify(t, st, tb)
	wantVerified(t, "after the cycle completed", r)
	if len(r.Cycles) != 2 || !r.Cycles[0].TailReRead {
		t.Fatalf("expected the completed cycle (with its tail) and the running one: %+v", r.Cycles)
	}
	// a deferral resolved in the next cycle; the cycle after that too
	tb.set(key(5), "ok")
	tb.set(key(77), "ok")
	runCycle(t, st, tb, o)
	wantVerified(t, "after deferrals resolved", verify(t, st, tb))
	if st.integrity == nil || st.integrity.Status != IntegrityVerified {
		t.Fatalf("the verification was not recorded: %+v", st.integrity)
	}
}

// A defect in the keyset walk that skips a record is found by the independent re-read: the record is in scope,
// unchanged since before its batch, and absent from the batch's evidence.
func TestVerifyDetectsAWalkThatSkipsARecord(t *testing.T) {
	tb, st := newTable(400), newMemStore()
	buggy := func(ctx context.Context, after string, limit int) ([]string, error) {
		ks, err := tb.next(ctx, after, limit+1)
		var out []string
		for _, k := range ks {
			if k != key(150) {
				out = append(out, k)
			}
		}
		if len(out) > limit {
			out = out[:limit]
		}
		return out, err
	}
	if _, err := Run(context.Background(), st, opts(100, 1000), buggy, tb.examine); err != nil {
		t.Fatal(err)
	}
	r := verify(t, st, tb)
	if r.Status != IntegrityInconsistent || !slices.Contains(codes(r), ProblemRecordSkipped) {
		t.Fatalf("skipped record not detected: %s %+v", r.Status, r.Problems)
	}
	for _, p := range r.Problems {
		if p.Code == ProblemRecordSkipped && (p.Count != 1 || p.Keys[0] != key(150)) {
			t.Fatalf("skipped keys: %+v", p)
		}
	}
	if h := (Coverage{RunningStartedAt: ptrTime(time.Now()), IntegrityStatus: r.Status}).Health(time.Now(), DefaultThresholds, 100, 0); h.State != HealthIntegrityFailure {
		t.Fatalf("health: %s", h.State)
	}
}

// A walk that completes its cycle early (a keyset read that returns nothing although records follow) is found by the
// completed cycle's tail re-read.
func TestVerifyDetectsACycleCompletedEarly(t *testing.T) {
	tb, st := newTable(300), newMemStore()
	early := func(ctx context.Context, after string, limit int) ([]string, error) {
		if after >= key(199) {
			return nil, nil
		}
		return tb.next(ctx, after, limit)
	}
	runCycleWith(t, st, tb, opts(100, 1000), early)
	r := verify(t, st, tb)
	if !slices.Contains(codes(r), ProblemCompletedEarly) {
		t.Fatalf("early completion not detected: %s %+v", r.Status, r.Problems)
	}
}

// Checkpoint moves the evidence does not support: a forward jump of the cursor, a dropped deferral, missing,
// duplicated or out-of-range evidence. (In the database the constraint trigger refuses the first two; the verifier
// finds them where a trigger was bypassed and for evidence damage.)
func TestVerifyDetectsUnsupportedCheckpointMoves(t *testing.T) {
	setup := func() (*table, *memStore) {
		tb, st := newTable(500), newMemStore()
		tb.set(key(10), "hot")
		run(t, st, tb, opts(100, 300))
		wantVerified(t, "setup", verify(t, st, tb))
		return tb, st
	}
	cases := []struct {
		name   string
		tamper func(st *memStore)
		want   []string
	}{
		{"cursor jumped forward", func(st *memStore) { st.cycles[0].Cursor = key(450) }, []string{ProblemCursorBeyond}},
		{"cursor and examined jumped forward", func(st *memStore) { st.cycles[0].Cursor, st.cycles[0].Examined = key(450), 450 },
			[]string{ProblemCursorBeyond, ProblemExaminedMismatch}},
		{"deferral dropped", func(st *memStore) { st.cycles[0].Deferred = nil }, []string{ProblemDeferralsMismatch}},
		{"batch evidence missing", func(st *memStore) { st.evidence[1] = append(st.evidence[1][:1], st.evidence[1][2:]...) },
			[]string{ProblemBatchMissing, ProblemChainBroken, ProblemExaminedMismatch}},
		{"evidence key duplicated", func(st *memStore) {
			e := &st.evidence[1][1]
			e.Keys = append([]string{st.evidence[1][0].Keys[0]}, e.Keys...)
		}, []string{ProblemDuplicateKey, ProblemExaminedMismatch, ProblemKeyOutOfRange}},
		{"batch counter without evidence", func(st *memStore) { st.cycles[0].Batches++ }, []string{ProblemBatchMissing}},
	}
	for _, c := range cases {
		tb, st := setup()
		c.tamper(st)
		r := verify(t, st, tb)
		if r.Status != IntegrityInconsistent {
			t.Errorf("%s: status %s", c.name, r.Status)
			continue
		}
		for _, w := range c.want {
			if !slices.Contains(codes(r), w) {
				t.Errorf("%s: problems %v, want %s", c.name, codes(r), w)
			}
		}
	}
}

// A moving dataset is not a skip: a record committed behind the cursor after its batch (or changed afterwards) is a
// late change, examined by the next cycle — and the next cycle's evidence contains it.
func TestVerifyLateChangesAreNotSkips(t *testing.T) {
	tb, st := newTable(500), newMemStore()
	run(t, st, tb, opts(100, 300))
	tb.add("k00000150x", "ok") // behind the cursor, committed after its batch
	tb.set(key(20), "bad")     // changed after its batch
	r := verify(t, st, tb)
	wantVerified(t, "late changes", r)
	late := 0
	for _, c := range r.Cycles {
		late += c.LateChanges
	}
	if late != 1 { // the new key; k20 is in its batch's evidence
		t.Fatalf("late changes counted: %d", late)
	}
	runCycle(t, st, tb, opts(100, 300))
	runCycle(t, st, tb, opts(100, 300))
	wantVerified(t, "next cycles", verify(t, st, tb))
	if !tb.findings[key(20)+":bad"] || tb.examined["k00000150x"] == 0 {
		t.Fatal("the next cycle did not examine the late changes")
	}
}

// The re-read stops at its time budget: INCOMPLETE, never VERIFIED. A cycle with batches from before the evidence
// existed is UNVERIFIABLE, not VERIFIED.
func TestVerifyIncompleteAndUnverifiable(t *testing.T) {
	tb, st := newTable(1000), newMemStore()
	run(t, st, tb, opts(100, 1000))
	now := time.Now()
	slow := func(ctx context.Context, from, to string) ([]Stamp, error) {
		now = now.Add(time.Second)
		return tb.source(ctx, from, to)
	}
	r, err := Verify(context.Background(), st, slow, IntegrityOptions{Detector: "D", Scope: "test", Margin: time.Nanosecond, MaxRuntime: 3 * time.Second,
		Now: func() time.Time { return now }})
	if err != nil || r.Status != IntegrityIncomplete {
		t.Fatalf("budget: %s %v", r.Status, err)
	}
	legacy := newMemStore()
	legacy.cycles = []Checkpoint{{ID: "c1", Detector: "D", Scope: "test", Cycle: 1, Cursor: key(99), Examined: 100, Batches: 1}}
	if r := verify(t, legacy, tb); r.Status != IntegrityUnverifiable {
		t.Fatalf("legacy cycle: %s %+v", r.Status, r.Problems)
	}
}

func runCycleWith(t *testing.T, st *memStore, tb *table, o Options, next Next) {
	t.Helper()
	c := st.cycle()
	for n := 0; n < 1000; n++ {
		if _, err := Run(context.Background(), st, o, next, tb.examine); err != nil {
			t.Fatal(err)
		}
		if st.cycle() > c {
			return
		}
	}
	t.Fatal("cycle never completed")
}

func ptrTime(t time.Time) *time.Time { return &t }
