package jobs

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

func TestBackoffExponentialCappedWithJitter(t *testing.T) {
	b := Backoff{Base: time.Second, Cap: 30 * time.Second}
	cases := []struct {
		attempt int
		want    time.Duration
	}{{0, time.Second}, {1, time.Second}, {2, 2 * time.Second}, {3, 4 * time.Second}, {5, 16 * time.Second}, {6, 30 * time.Second}, {50, 30 * time.Second}}
	for _, c := range cases {
		if got := b.delay(c.attempt, 1); got != c.want {
			t.Errorf("attempt %d: got %s want %s", c.attempt, got, c.want)
		}
	}
	for i := 0; i < 1000; i++ {
		d := b.Delay(3)
		if d < 3200*time.Millisecond || d > 4800*time.Millisecond {
			t.Fatalf("jitter out of [0.8,1.2]: %s", d)
		}
	}
}

func TestClassify(t *testing.T) {
	if Classify(1, 3, nil) != OutcomeCompleted {
		t.Fatal("nil error must complete")
	}
	if Classify(1, 3, errors.New("x")) != OutcomeRetry {
		t.Fatal("early failure must retry")
	}
	if Classify(3, 3, errors.New("x")) != OutcomeDiscarded {
		t.Fatal("last attempt failure must discard")
	}
	if Classify(3, 3, river.JobCancel(errors.New("x"))) != OutcomeCancelled {
		t.Fatal("JobCancel must cancel")
	}
	if Classify(3, 3, river.JobSnooze(time.Second)) != OutcomeSnoozed {
		t.Fatal("snooze is not a failure")
	}
}

func TestObserverCountsAndLogsDiscardAtErrorLevel(t *testing.T) {
	var buf bytes.Buffer
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	o := &observer{m: m, logger: slog.New(slog.NewJSONHandler(&buf, nil))}
	job := &rivertype.JobRow{ID: 7, Kind: "outbox.deliver", Attempt: 1, MaxAttempts: 2, Queue: "outbox"}
	ctx := context.Background()

	if err := o.WorkEnd(ctx, job, nil); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	if err := o.WorkEnd(ctx, job, boom); !errors.Is(err, boom) {
		t.Fatal("WorkEnd must pass the error through unchanged")
	}
	job.Attempt = 2
	_ = o.WorkEnd(ctx, job, boom)
	o.HandlePanic(ctx, job, "p", "trace")

	if v := testutil.ToFloat64(m.Completed("outbox.deliver")); v != 1 {
		t.Fatalf("completed %v", v)
	}
	if v := testutil.ToFloat64(m.Failed("outbox.deliver")); v != 1 {
		t.Fatalf("failed %v", v)
	}
	if v := testutil.ToFloat64(m.Discarded("outbox.deliver")); v != 2 {
		t.Fatalf("discarded %v", v)
	}
	if !strings.Contains(buf.String(), `"level":"ERROR","msg":"job discarded after final attempt (dead letter)"`) {
		t.Fatalf("discard not logged at error level: %s", buf.String())
	}
	if strings.Contains(buf.String(), "trace") {
		t.Fatal("panic trace must not be logged")
	}
}

func TestDefaultQueues(t *testing.T) {
	q := DefaultQueues(0)
	for _, name := range []string{QueueDefault, QueueOutbox, QueueNotifications, QueueMaintenance} {
		if q[name].MaxWorkers < 1 {
			t.Fatalf("queue %s has no workers", name)
		}
	}
}

func TestWorkerClientRequiresDependencies(t *testing.T) {
	if _, err := NewWorkerClient(WorkerConfig{}); err == nil {
		t.Fatal("expected error")
	}
}
