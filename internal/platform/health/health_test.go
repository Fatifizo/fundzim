package health

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCriticalFailureMakesNotReady(t *testing.T) {
	c := NewChecker(
		Check{Name: "db", Critical: true, Run: func(context.Context) error { return errors.New("down") }},
		Check{Name: "redis", Critical: false, Run: func(context.Context) error { return nil }},
	)
	if c.Run(context.Background()).Ready {
		t.Fatal("expected not ready")
	}
}

func TestNonCriticalFailureStaysReady(t *testing.T) {
	c := NewChecker(
		Check{Name: "db", Critical: true, Run: func(context.Context) error { return nil }},
		Check{Name: "redis", Critical: false, Run: func(context.Context) error { return errors.New("down") }},
	)
	rep := c.Run(context.Background())
	if !rep.Ready || rep.Results[1].OK {
		t.Fatalf("unexpected report %+v", rep)
	}
}

func TestChecksAreBoundedByTimeoutEvenIfTheyIgnoreContext(t *testing.T) {
	c := NewChecker(Check{Name: "slow", Critical: true, Timeout: 30 * time.Millisecond,
		Run: func(context.Context) error { time.Sleep(time.Second); return nil }})
	start := time.Now()
	rep := c.Run(context.Background())
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("check not bounded")
	}
	if rep.Ready || !errors.Is(rep.Results[0].Err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline failure, got %+v", rep.Results[0])
	}
}

func TestRunCachedLimitsCheckExecutions(t *testing.T) {
	calls := 0
	now := time.Unix(1_700_000_000, 0)
	c := NewChecker(Check{Name: "db", Critical: true, Run: func(context.Context) error { calls++; return nil }}).
		WithCache(time.Second, func() time.Time { return now })
	for i := 0; i < 5; i++ {
		c.RunCached(context.Background())
	}
	if calls != 1 {
		t.Fatalf("checks ran %d times within the TTL", calls)
	}
	now = now.Add(time.Second)
	c.RunCached(context.Background())
	if calls != 2 {
		t.Fatalf("cache not refreshed after TTL (calls=%d)", calls)
	}
}
