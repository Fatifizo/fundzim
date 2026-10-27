package reconsweep

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/opsview"
)

// Remediation phase 6 (F-R5-01/02): every verification outcome is recorded — a failure of the verification itself
// replaces a previous VERIFIED as the latest outcome — and the integrity state keeps the last success apart from it.

// faultyStore fails chosen reads and refuses a record made with an ended context (the outcome of an interrupted
// verification must still be written).
type faultyStore struct {
	*memStore
	cyclesErr, evidenceErr error
}

func (f faultyStore) IntegrityCycles(ctx context.Context, d, s string) (*CycleRecord, *CycleRecord, *CycleRecord, error) {
	if f.cyclesErr != nil {
		return nil, nil, nil, f.cyclesErr
	}
	return f.memStore.IntegrityCycles(ctx, d, s)
}

func (f faultyStore) Evidence(ctx context.Context, id string) ([]Evidence, error) {
	if f.evidenceErr != nil {
		return nil, f.evidenceErr
	}
	return f.memStore.Evidence(ctx, id)
}

func (f faultyStore) RecordIntegrity(ctx context.Context, d, s string, r IntegrityReport) error {
	if ctx.Err() != nil {
		return errors.New("record with an ended context")
	}
	return f.memStore.RecordIntegrity(ctx, d, s, r)
}

var errDB = errors.New(`pq: relation "x" password=secret does not exist`)

func verifyWith(ctx context.Context, st IntegrityStore, src Source) (IntegrityReport, error) {
	return Verify(ctx, st, src, IntegrityOptions{Detector: "D", Scope: "test", Margin: time.Nanosecond, MaxRuntime: time.Minute})
}

func wantRecorded(t *testing.T, what string, st *memStore, status, code string) IntegrityReport {
	t.Helper()
	if st.integrity == nil {
		t.Fatalf("%s: nothing recorded", what)
	}
	r := *st.integrity
	if r.Status != status || r.Error != code {
		t.Fatalf("%s: recorded %s/%q, want %s/%q", what, r.Status, r.Error, status, code)
	}
	if strings.Contains(r.Error, "secret") || strings.Contains(r.Error, "relation") {
		t.Fatalf("%s: the reason is not sanitised: %q", what, r.Error)
	}
	return r
}

func TestVerifyRecordsSuccessThenFailure(t *testing.T) {
	tb, ms := newTable(500), newMemStore()
	run(t, ms, tb, opts(100, 200))
	st := faultyStore{memStore: ms}
	if _, err := verifyWith(context.Background(), st, tb.source); err != nil {
		t.Fatal(err)
	}
	wantRecorded(t, "success", ms, IntegrityVerified, "")
	failing := func(context.Context, string, string) ([]Stamp, error) { return nil, errDB }
	r, err := verifyWith(context.Background(), st, failing)
	if !errors.Is(err, errDB) || r.Status != IntegrityError {
		t.Fatalf("source failure: %s %v", r.Status, err)
	}
	wantRecorded(t, "source failure after a success", ms, IntegrityError, IntegrityErrSourceRead)
	st.evidenceErr = errDB
	if _, err := verifyWith(context.Background(), st, tb.source); !errors.Is(err, errDB) {
		t.Fatalf("evidence failure: %v", err)
	}
	wantRecorded(t, "evidence failure", ms, IntegrityError, IntegrityErrEvidenceRead)
	st.evidenceErr, st.cyclesErr = nil, errDB
	if _, err := verifyWith(context.Background(), st, tb.source); !errors.Is(err, errDB) {
		t.Fatalf("cycles failure: %v", err)
	}
	wantRecorded(t, "cycles failure", ms, IntegrityError, IntegrityErrCyclesRead)
	st.cyclesErr = nil
	if _, err := verifyWith(context.Background(), st, tb.source); err != nil {
		t.Fatal(err)
	}
	wantRecorded(t, "recovery", ms, IntegrityVerified, "")
	// the failures are not erased by the recovery: every outcome was recorded in order
	var got []string
	for _, r := range ms.recorded {
		got = append(got, r.Status+":"+r.Error)
	}
	want := "VERIFIED:,ERROR:SOURCE_READ_FAILED,ERROR:EVIDENCE_READ_FAILED,ERROR:CYCLES_READ_FAILED,VERIFIED:"
	if strings.Join(got, ",") != want {
		t.Fatalf("recorded outcomes %v, want %s", got, want)
	}
}

func TestVerifyRecordsAFirstAttemptFailure(t *testing.T) {
	tb, ms := newTable(300), newMemStore()
	run(t, ms, tb, opts(100, 200))
	failing := func(context.Context, string, string) ([]Stamp, error) { return nil, errDB }
	if _, err := verifyWith(context.Background(), faultyStore{memStore: ms}, failing); err == nil {
		t.Fatal("expected the source error")
	}
	wantRecorded(t, "first attempt", ms, IntegrityError, IntegrityErrSourceRead)
	if len(ms.recorded) != 1 {
		t.Fatalf("recorded %d outcomes", len(ms.recorded))
	}
}

// A verification whose context ends (job time-out, shutdown) is INCOMPLETE/INTERRUPTED — recorded with a context that
// is not cancelled — and the time budget is INCOMPLETE/TIME_BUDGET; neither is ever VERIFIED.
func TestVerifyRecordsTimeoutsAndInterruptions(t *testing.T) {
	tb, ms := newTable(500), newMemStore()
	run(t, ms, tb, opts(100, 300))
	st := faultyStore{memStore: ms}
	if _, err := verifyWith(context.Background(), st, tb.source); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancelling := func(c context.Context, from, to string) ([]Stamp, error) {
		cancel()
		return nil, c.Err()
	}
	r, err := verifyWith(ctx, st, cancelling)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted: %v", err)
	}
	if r.Status != IntegrityIncomplete {
		t.Fatalf("interrupted: status %s", r.Status)
	}
	wantRecorded(t, "interrupted after a success", ms, IntegrityIncomplete, IntegrityErrInterrupted)
	// the deadline variant
	dctx, dcancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer dcancel()
	if _, err := verifyWith(dctx, st, func(c context.Context, _, _ string) ([]Stamp, error) { return nil, c.Err() }); err == nil {
		t.Fatal("expected the deadline error")
	}
	wantRecorded(t, "deadline", ms, IntegrityIncomplete, IntegrityErrInterrupted)
	// repeated time-budget exhaustion: each one is recorded as not verified
	now := time.Now()
	slow := func(ctx context.Context, from, to string) ([]Stamp, error) {
		now = now.Add(time.Second)
		return tb.source(ctx, from, to)
	}
	for i := 0; i < 3; i++ {
		r, err := Verify(context.Background(), st, slow, IntegrityOptions{Detector: "D", Scope: "test", Margin: time.Nanosecond,
			MaxRuntime: 2 * time.Second, Now: func() time.Time { return now }})
		if err != nil || r.Status != IntegrityIncomplete {
			t.Fatalf("budget %d: %s %v", i, r.Status, err)
		}
		wantRecorded(t, "time budget", ms, IntegrityIncomplete, IntegrityErrTimeBudget)
	}
}

// Problems found before a read failed stand: the outcome is INCONSISTENT (never ERROR, never VERIFIED), with the code.
func TestVerifyKeepsProblemsFoundBeforeAFailure(t *testing.T) {
	tb, ms := newTable(400), newMemStore()
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
	if _, err := Run(context.Background(), ms, opts(100, 300), buggy, tb.examine); err != nil {
		t.Fatal(err)
	}
	calls := 0
	src := func(ctx context.Context, from, to string) ([]Stamp, error) {
		calls++
		if calls > 2 {
			return nil, errDB
		}
		return tb.source(ctx, from, to)
	}
	r, err := verifyWith(context.Background(), faultyStore{memStore: ms}, src)
	if err == nil {
		t.Fatal("expected the source error")
	}
	if len(r.Problems) == 0 {
		t.Fatalf("arrangement: the failing read came before the problem was found (calls=%d)", calls)
	}
	wantRecorded(t, "problems then failure", ms, IntegrityInconsistent, IntegrityErrSourceRead)
}

// Concurrent verifications of one detector each record a complete outcome (the SQL store orders them by start time;
// see TestReconSweepIntegrityStatus*).
func TestVerifyConcurrent(t *testing.T) {
	tb, ms := newTable(500), newMemStore()
	run(t, ms, tb, opts(100, 300))
	st := faultyStore{memStore: ms}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			src := Source(tb.source)
			if i%2 == 1 {
				src = func(context.Context, string, string) ([]Stamp, error) { return nil, errDB }
			}
			_, _ = verifyWith(context.Background(), st, src)
		}(i)
	}
	wg.Wait()
	ms.mu.Lock()
	defer ms.mu.Unlock()
	if len(ms.recorded) != 8 {
		t.Fatalf("recorded %d of 8", len(ms.recorded))
	}
	for _, r := range ms.recorded {
		if (r.Status == IntegrityVerified) == (r.Error != "") {
			t.Fatalf("incoherent outcome %+v", r)
		}
	}
}

// The integrity state of every recorded combination, the health state it leads to and the metric shown.
func TestIntegrityState(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { x := now.Add(-d); return &x }
	running := at(30 * time.Minute)
	adv := at(time.Minute)
	cases := []struct {
		name       string
		c          Coverage
		want       string
		wantHealth string
		wantStatus string
	}{
		{"first cycle, not yet verified", Coverage{RunningStartedAt: running, LastAdvancedAt: adv}, IntegrityStateNotRun, HealthProgressing, opsview.StatusUnknown},
		{"verified", Coverage{RunningStartedAt: running, LastAdvancedAt: adv, IntegrityStatus: IntegrityVerified, IntegrityCheckedAt: at(10 * time.Minute),
			IntegrityVerifiedAt: at(10 * time.Minute)}, IntegrityStateVerified, HealthProgressing, opsview.StatusUnknown},
		{"verified before phase 6 (no verified_at)", Coverage{RunningStartedAt: running, LastAdvancedAt: adv, IntegrityStatus: IntegrityVerified,
			IntegrityCheckedAt: at(10 * time.Minute)}, IntegrityStateVerified, HealthProgressing, opsview.StatusUnknown},
		{"success then one failure", Coverage{RunningStartedAt: running, LastAdvancedAt: adv, IntegrityStatus: IntegrityError, IntegrityError: IntegrityErrSourceRead,
			IntegrityCheckedAt: at(5 * time.Minute), IntegrityVerifiedAt: at(20 * time.Minute), IntegrityUnverifiedSince: at(5 * time.Minute), IntegrityFailures: 1},
			IntegrityStateLatestFailed, HealthProgressing, opsview.Degraded},
		{"success then a time-out", Coverage{RunningStartedAt: running, LastAdvancedAt: adv, IntegrityStatus: IntegrityIncomplete, IntegrityError: IntegrityErrTimeBudget,
			IntegrityCheckedAt: at(5 * time.Minute), IntegrityVerifiedAt: at(20 * time.Minute), IntegrityUnverifiedSince: at(5 * time.Minute), IntegrityFailures: 1},
			IntegrityStateLatestFailed, HealthProgressing, opsview.Degraded},
		{"repeated time-outs past the threshold", Coverage{RunningStartedAt: running, LastAdvancedAt: adv, IntegrityStatus: IntegrityIncomplete,
			IntegrityError: IntegrityErrTimeBudget, IntegrityCheckedAt: at(time.Minute), IntegrityVerifiedAt: at(2 * time.Hour), IntegrityUnverifiedSince: at(105 * time.Minute),
			IntegrityFailures: 7}, IntegrityStateStale, HealthIntegrityUnverified, opsview.Degraded},
		{"first attempt failed, recently", Coverage{RunningStartedAt: running, LastAdvancedAt: adv, IntegrityStatus: IntegrityError, IntegrityError: IntegrityErrCyclesRead,
			IntegrityCheckedAt: at(5 * time.Minute), IntegrityUnverifiedSince: at(5 * time.Minute), IntegrityFailures: 1}, IntegrityStateLatestFailed, HealthProgressing, opsview.Degraded},
		{"never succeeded, failing for long", Coverage{RunningStartedAt: at(3 * time.Hour), LastAdvancedAt: adv, IntegrityStatus: IntegrityError,
			IntegrityError: IntegrityErrSourceRead, IntegrityCheckedAt: at(time.Minute), IntegrityUnverifiedSince: at(2 * time.Hour), IntegrityFailures: 8},
			IntegrityStateStale, HealthIntegrityUnverified, opsview.Degraded},
		{"blocked by deferrals: unverifiable for long", Coverage{RunningStartedAt: running, LastAdvancedAt: adv, IntegrityStatus: IntegrityUnverifiable,
			IntegrityCheckedAt: at(time.Minute), IntegrityVerifiedAt: at(3 * time.Hour), IntegrityUnverifiedSince: at(2 * time.Hour), IntegrityFailures: 8,
			DeferredOutstanding: 1, OldestDeferralSince: at(30 * time.Hour)}, IntegrityStateStale, HealthBlockedOnDeferrals, opsview.Degraded},
		{"recovered after failures", Coverage{RunningStartedAt: running, LastAdvancedAt: adv, IntegrityStatus: IntegrityVerified, IntegrityCheckedAt: at(time.Minute),
			IntegrityVerifiedAt: at(time.Minute), IntegrityLastFailureAt: at(16 * time.Minute), IntegrityLastFailure: "ERROR:SOURCE_READ_FAILED"},
			IntegrityStateVerified, HealthProgressing, opsview.StatusUnknown},
		{"verified long ago, nothing since", Coverage{RunningStartedAt: running, LastAdvancedAt: adv, IntegrityStatus: IntegrityVerified,
			IntegrityCheckedAt: at(2 * time.Hour), IntegrityVerifiedAt: at(2 * time.Hour)}, IntegrityStateStale, HealthIntegrityUnverified, opsview.Degraded},
		{"verified timestamp in the future", Coverage{RunningStartedAt: running, LastAdvancedAt: adv, IntegrityStatus: IntegrityVerified,
			IntegrityCheckedAt: at(-time.Hour), IntegrityVerifiedAt: at(-time.Hour)}, IntegrityStateStale, HealthIntegrityUnverified, opsview.Degraded},
		{"small clock skew is tolerated", Coverage{RunningStartedAt: running, LastAdvancedAt: adv, IntegrityStatus: IntegrityVerified,
			IntegrityCheckedAt: at(-time.Minute), IntegrityVerifiedAt: at(-time.Minute)}, IntegrityStateVerified, HealthProgressing, opsview.StatusUnknown},
		{"latest check in the future", Coverage{RunningStartedAt: running, LastAdvancedAt: adv, IntegrityStatus: IntegrityError, IntegrityCheckedAt: at(-2 * time.Hour),
			IntegrityVerifiedAt: at(10 * time.Minute)}, IntegrityStateStale, HealthIntegrityUnverified, opsview.Degraded},
		{"completed cycle, never verified", Coverage{LastCompletedCycleStartedAt: at(time.Hour), RunningStartedAt: at(10 * time.Minute), LastAdvancedAt: adv},
			IntegrityStateStale, HealthIntegrityUnverified, opsview.Degraded},
		{"inconsistent", Coverage{RunningStartedAt: running, LastAdvancedAt: adv, IntegrityStatus: IntegrityInconsistent, IntegrityCheckedAt: at(time.Minute),
			IntegrityVerifiedAt: at(20 * time.Minute)}, IntegrityStateInconsistent, HealthIntegrityFailure, opsview.Unavailable},
	}
	for _, c := range cases {
		h := c.c.Health(now, DefaultThresholds, 100, 0)
		if h.IntegrityState != c.want || h.State != c.wantHealth {
			t.Errorf("%s: integrity %s / health %s, want %s / %s (%s)", c.name, h.IntegrityState, h.State, c.want, c.wantHealth, h.Reason)
		}
		m := h.IntegrityMetric("m", "m.integrity", "l", "p", "s")
		if m.State != c.want || m.Status != c.wantStatus || m.Status == opsview.Healthy {
			t.Errorf("%s: metric %s/%s, want %s/%s", c.name, m.State, m.Status, c.want, c.wantStatus)
		}
		if c.c.IntegrityVerifiedAt != nil && (m.Value == nil || *m.Value != int64(now.Sub(*c.c.IntegrityVerifiedAt).Seconds())) {
			t.Errorf("%s: value %v is not the age of the last success", c.name, m.Value)
		}
		if c.c.IntegrityVerifiedAt == nil && m.Value != nil {
			t.Errorf("%s: value %v without a success", c.name, *m.Value)
		}
		if h.IntegrityLastFailure != c.c.IntegrityLastFailure || h.IntegrityFailures != c.c.IntegrityFailures {
			t.Errorf("%s: failure history not reported", c.name)
		}
	}
}
