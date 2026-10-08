//go:build integration

// Worker, River, outbox/inbox and notifications against the REAL local stack (Stage 4 work stream A).
//
// The tests run River in-process against the real database with their own queue and their own event
// types, so they never compete with the fundzim-worker container:
//   - delivery jobs go to a per-test queue the container does not serve;
//   - test events are written with available_at one hour ahead (the container's relay ignores them) and
//     relayed by an in-test Relay restricted to the test's event types with a clock two hours ahead;
//   - the job kind is the production outbox.deliver, which the container knows, so if it is the River
//     leader its rescuer/scheduler treat the test jobs correctly.
//
// Needs: DATABASE_URL, POSTGRES_WORKER_PASSWORD (from .env), Mailpit on 127.0.0.1:1025/8025
// (FUNDZIM_IT_MAILPIT_URL, MAIL_SMTP_HOST_PORT override).
package integration

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/Fatifizo/fundzim/internal/notifications"
	"github.com/Fatifizo/fundzim/internal/platform/config"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/jobs"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// letters returns n random lowercase letters (event types and consumers allow [a-z_] only).
func letters(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		x, _ := rand.Int(rand.Reader, big.NewInt(26))
		b.WriteByte(byte('a' + x.Int64()))
	}
	return b.String()
}

type harness struct {
	t          *testing.T
	app        *pgxpool.Pool // fundzim_app: producers write events
	wrk        *pgxpool.Pool // fundzim_worker: relay and jobs
	reg        *outbox.Registry
	relay      *outbox.Relay
	queue      string
	eventType  string
	jobMetrics *jobs.Metrics
	deliver    *outbox.DeliverWorker
	logger     *slog.Logger
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	need(t, "DATABASE_URL", "POSTGRES_WORKER_PASSWORD")
	suffix := letters(10)
	h := &harness{
		t: t, app: pool(t, os.Getenv("DATABASE_URL")), wrk: pool(t, workerURL(t)),
		reg: outbox.NewRegistry(), queue: "it_" + suffix, eventType: "it_outbox." + suffix,
		jobMetrics: jobs.NewMetrics(prometheus.NewRegistry()),
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	h.relay = &outbox.Relay{Pool: h.wrk, Registry: h.reg, Metrics: outbox.NewMetrics(prometheus.NewRegistry()),
		DeliverQueue: h.queue, DeliverMaxAttempts: 5, EventTypes: []string{h.eventType},
		Now: func() time.Time { return time.Now().Add(2 * time.Hour) }}
	h.deliver = &outbox.DeliverWorker{Pool: h.wrk, Registry: h.reg, Logger: h.logger,
		Backoff: jobs.Backoff{Base: 100 * time.Millisecond, Cap: 500 * time.Millisecond}, JobLimit: 10 * time.Second}
	return h
}

// client builds and starts an in-process River client working only the test queue.
func (h *harness) client(id string, rescueAfter time.Duration) *river.Client[pgx.Tx] {
	h.t.Helper()
	workers := river.NewWorkers()
	river.AddWorker(workers, h.deliver)
	// Known kinds, so this client never discards another kind's stuck job if it becomes leader.
	river.AddWorker(workers, &outbox.RelayWorker{Relay: h.relay})
	river.AddWorker(workers, &outbox.PurgeWorker{Pool: h.wrk, Logger: h.logger})
	river.AddWorker(workers, &jobs.PurgeIdempotencyKeysWorker{Pool: h.wrk, Logger: h.logger})
	c, err := jobs.NewWorkerClient(jobs.WorkerConfig{Pool: h.wrk, Logger: h.logger, Metrics: h.jobMetrics,
		Workers: workers, Queues: map[string]river.QueueConfig{h.queue: {MaxWorkers: 4}}, ID: id + "_" + h.queue,
		JobTimeout: 10 * time.Second, RescueStuckJobsAfter: rescueAfter, FetchPollInterval: 100 * time.Millisecond})
	if err != nil {
		h.t.Fatal(err)
	}
	if err := c.Start(context.Background()); err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = c.StopAndCancel(sctx)
	})
	return c
}

// write records one test event in a fundzim_app transaction, held back from the container's relay.
func (h *harness) write(ctx context.Context, rollback bool) (string, error) {
	var id string
	errRollback := errors.New("rollback requested")
	err := db.WithTx(ctx, h.app, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		id, err = outbox.Write(ctx, tx, outbox.Event{AggregateType: "user", AggregateID: ids.New(), EventType: h.eventType,
			Payload: map[string]string{"marker": "integration"}, CorrelationID: "it-" + h.queue, OccurredAt: time.Now()})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app.outbox_events SET available_at = now() + interval '1 hour' WHERE id = $1`, id); err != nil {
			return err
		}
		if rollback {
			return errRollback
		}
		return nil
	})
	if rollback && errors.Is(err, errRollback) {
		return id, nil
	}
	return id, err
}

func (h *harness) inboxCount(ctx context.Context, consumer, eventID string) int {
	var n int
	if err := h.wrk.QueryRow(ctx, `SELECT count(*) FROM app.inbox_events WHERE consumer = $1 AND event_id = $2`,
		consumer, eventID).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

// jobFor returns the delivery job of (consumer, event) in the test queue.
func (h *harness) jobsFor(ctx context.Context, consumer, eventID string) []int64 {
	rows, err := h.wrk.Query(ctx, `SELECT id FROM queue.river_job WHERE kind = 'outbox.deliver' AND queue = $1
		AND args->>'consumer' = $2 AND args->>'event_id' = $3 ORDER BY id`, h.queue, consumer, eventID)
	if err != nil {
		h.t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		h.t.Fatal(err)
	}
	return out
}

// waitJob polls until the job reaches one of the states.
func waitJob(t *testing.T, c *river.Client[pgx.Tx], id int64, timeout time.Duration, states ...rivertype.JobState) *rivertype.JobRow {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last *rivertype.JobRow
	for time.Now().Before(deadline) {
		j, err := c.JobGet(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		last = j
		for _, s := range states {
			if j.State == s {
				return j
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("job %d did not reach %v within %s (state %s, attempt %d)", id, states, timeout, last.State, last.Attempt)
	return nil
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// (a) An event written in a transaction reaches each subscriber exactly once, even if the relay runs
// twice and a duplicate delivery job is inserted.
func TestOutboxDeliversExactlyOncePerSubscriber(t *testing.T) {
	h := newHarness(t)
	ctx := ctx(t)
	var callsA, callsB atomic.Int32
	consumerA, consumerB := "it_a."+letters(6), "it_b."+letters(6)
	var seen outbox.Delivery
	var mu sync.Mutex
	h.reg.Subscribe(consumerA, h.eventType, func(ctx context.Context, d outbox.Delivery) error {
		if db.InTx(ctx) {
			return errors.New("handler ran inside a transaction")
		}
		mu.Lock()
		seen = d
		mu.Unlock()
		callsA.Add(1)
		return nil
	})
	h.reg.Subscribe(consumerB, h.eventType, func(context.Context, outbox.Delivery) error { callsB.Add(1); return nil })
	c := h.client("a", 0)

	eventID, err := h.write(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	n1, err := h.relay.RunOnce(ctx, c)
	if err != nil || n1 != 1 {
		t.Fatalf("first relay dispatched %d (%v), want 1", n1, err)
	}
	n2, err := h.relay.RunOnce(ctx, c)
	if err != nil || n2 != 0 {
		t.Fatalf("second relay dispatched %d (%v), want 0", n2, err)
	}
	// Re-inserting the same delivery with the relay's unique options is skipped by River.
	res, err := c.InsertMany(ctx, []river.InsertManyParams{{Args: outbox.DeliverArgs{Consumer: consumerA, EventID: eventID},
		InsertOpts: &river.InsertOpts{Queue: h.queue, MaxAttempts: 5, UniqueOpts: river.UniqueOpts{ByArgs: true,
			ByState: []rivertype.JobState{rivertype.JobStateAvailable, rivertype.JobStateCancelled, rivertype.JobStateCompleted,
				rivertype.JobStateDiscarded, rivertype.JobStatePending, rivertype.JobStateRetryable, rivertype.JobStateRunning,
				rivertype.JobStateScheduled}}}}})
	if err != nil || !res[0].UniqueSkippedAsDuplicate {
		t.Fatalf("duplicate delivery was not unique-skipped (%v)", err)
	}

	for _, consumer := range []string{consumerA, consumerB} {
		ids := h.jobsFor(ctx, consumer, eventID)
		if len(ids) != 1 {
			t.Fatalf("%s: %d delivery jobs, want 1", consumer, len(ids))
		}
		waitJob(t, c, ids[0], 30*time.Second, rivertype.JobStateCompleted)
	}
	// A forced duplicate job (no uniqueness) finds the inbox row and does not re-run the handler.
	dup, err := c.Insert(ctx, outbox.DeliverArgs{Consumer: consumerA, EventID: eventID}, &river.InsertOpts{Queue: h.queue, MaxAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, c, dup.Job.ID, 30*time.Second, rivertype.JobStateCompleted)

	if callsA.Load() != 1 || callsB.Load() != 1 {
		t.Fatalf("handler calls A=%d B=%d, want 1 each", callsA.Load(), callsB.Load())
	}
	if h.inboxCount(ctx, consumerA, eventID) != 1 || h.inboxCount(ctx, consumerB, eventID) != 1 {
		t.Fatal("want exactly one inbox row per consumer")
	}
	mu.Lock()
	defer mu.Unlock()
	if seen.EventID != eventID || seen.EventType != h.eventType || seen.AggregateType != "user" ||
		seen.CorrelationID != "it-"+h.queue || string(seen.Payload) != `{"marker": "integration"}` || seen.Attempt != 1 {
		t.Fatalf("delivery content %+v payload %s", seen, seen.Payload)
	}
	var dispatched bool
	if err := h.wrk.QueryRow(ctx, `SELECT dispatched_at IS NOT NULL FROM app.outbox_events WHERE id = $1`, eventID).Scan(&dispatched); err != nil || !dispatched {
		t.Fatalf("event not marked dispatched (%v)", err)
	}
}

// Events without subscribers are marked dispatched; concurrent relays never double-claim (SKIP LOCKED).
func TestOutboxRelayConcurrentAndUnsubscribed(t *testing.T) {
	h := newHarness(t)
	ctx := ctx(t)
	c := h.client("concurrent", 0)
	for i := 0; i < 30; i++ {
		if _, err := h.write(ctx, false); err != nil {
			t.Fatal(err)
		}
	}
	h.relay.BatchSize = 5
	var wg sync.WaitGroup
	var total atomic.Int32
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := h.relay.RunOnce(ctx, c)
			if err != nil {
				t.Error(err)
			}
			total.Add(int32(n))
		}()
	}
	wg.Wait()
	if total.Load() != 30 {
		t.Fatalf("relays dispatched %d events in total, want exactly 30", total.Load())
	}
	var left int
	_ = h.wrk.QueryRow(ctx, `SELECT count(*) FROM app.outbox_events WHERE event_type = $1 AND dispatched_at IS NULL`, h.eventType).Scan(&left)
	if left != 0 {
		t.Fatalf("%d events left undispatched", left)
	}
}

// (b) A rolled-back transaction leaves no event and produces no delivery.
func TestOutboxRolledBackTransactionDeliversNothing(t *testing.T) {
	h := newHarness(t)
	ctx := ctx(t)
	var calls atomic.Int32
	h.reg.Subscribe("it_rollback."+letters(6), h.eventType, func(context.Context, outbox.Delivery) error { calls.Add(1); return nil })
	c := h.client("b", 0)
	id, err := h.write(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := h.wrk.QueryRow(ctx, `SELECT count(*) FROM app.outbox_events WHERE id = $1`, id).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rolled-back event exists (%d, %v)", n, err)
	}
	if got, err := h.relay.RunOnce(ctx, c); err != nil || got != 0 {
		t.Fatalf("relay dispatched %d (%v)", got, err)
	}
	time.Sleep(time.Second)
	if calls.Load() != 0 {
		t.Fatal("handler ran for a rolled-back event")
	}
}

// (c) A handler failing twice then succeeding is retried with backoff and completes.
func TestOutboxHandlerRetriedUntilSuccess(t *testing.T) {
	h := newHarness(t)
	ctx := ctx(t)
	consumer := "it_retry." + letters(6)
	var calls atomic.Int32
	var attempts []int
	var mu sync.Mutex
	h.reg.Subscribe(consumer, h.eventType, func(_ context.Context, d outbox.Delivery) error {
		mu.Lock()
		attempts = append(attempts, d.Attempt)
		mu.Unlock()
		if calls.Add(1) <= 2 {
			return errors.New("transient failure")
		}
		return nil
	})
	c := h.client("c", 0)
	eventID, err := h.write(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.relay.RunOnce(ctx, c); err != nil {
		t.Fatal(err)
	}
	jobIDs := h.jobsFor(ctx, consumer, eventID)
	if len(jobIDs) != 1 {
		t.Fatalf("%d jobs", len(jobIDs))
	}
	j := waitJob(t, c, jobIDs[0], 60*time.Second, rivertype.JobStateCompleted, rivertype.JobStateDiscarded)
	if j.State != rivertype.JobStateCompleted || j.Attempt != 3 || len(j.Errors) != 2 {
		t.Fatalf("state %s attempt %d errors %d, want completed on attempt 3 after 2 errors", j.State, j.Attempt, len(j.Errors))
	}
	if gap := j.Errors[1].At.Sub(j.Errors[0].At); gap < 50*time.Millisecond {
		t.Fatalf("no backoff between attempts (%s)", gap)
	}
	mu.Lock()
	if fmt.Sprint(attempts) != "[1 2 3]" {
		t.Fatalf("Delivery.Attempt sequence %v", attempts)
	}
	mu.Unlock()
	if h.inboxCount(ctx, consumer, eventID) != 1 {
		t.Fatal("inbox row missing after success")
	}
	if testutil.ToFloat64(h.jobMetrics.Failed(outbox.KindDeliver)) != 2 || testutil.ToFloat64(h.jobMetrics.Completed(outbox.KindDeliver)) != 1 {
		t.Fatal("failed/completed metrics wrong")
	}
}

// (d) A handler that always fails ends discarded (dead letter), counted in the metric, no inbox row.
func TestOutboxHandlerAlwaysFailingIsDiscarded(t *testing.T) {
	h := newHarness(t)
	ctx := ctx(t)
	consumer := "it_dead." + letters(6)
	h.relay.DeliverMaxAttempts = 3
	var calls atomic.Int32
	h.reg.Subscribe(consumer, h.eventType, func(context.Context, outbox.Delivery) error {
		calls.Add(1)
		return errors.New("permanent failure")
	})
	c := h.client("d", 0)
	eventID, err := h.write(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.relay.RunOnce(ctx, c); err != nil {
		t.Fatal(err)
	}
	jobIDs := h.jobsFor(ctx, consumer, eventID)
	j := waitJob(t, c, jobIDs[0], 60*time.Second, rivertype.JobStateDiscarded, rivertype.JobStateCompleted)
	if j.State != rivertype.JobStateDiscarded || j.Attempt != 3 || calls.Load() != 3 {
		t.Fatalf("state %s attempt %d calls %d, want discarded after 3", j.State, j.Attempt, calls.Load())
	}
	waitFor(t, 5*time.Second, "discard metric", func() bool {
		return testutil.ToFloat64(h.jobMetrics.Discarded(outbox.KindDeliver)) == 1
	})
	if h.inboxCount(ctx, consumer, eventID) != 0 {
		t.Fatal("a failed delivery must not record an inbox row")
	}
}

// (e1) Worker stopped mid-job (contexts cancelled): the job is retried and completed by another worker.
func TestWorkerCancelledMidJobIsRetriedElsewhere(t *testing.T) {
	h := newHarness(t)
	ctx := ctx(t)
	consumer := "it_cancel." + letters(6)
	var calls atomic.Int32
	started := make(chan struct{}, 1)
	h.reg.Subscribe(consumer, h.eventType, func(ctx context.Context, _ outbox.Delivery) error {
		if calls.Add(1) == 1 {
			started <- struct{}{}
			<-ctx.Done() // first worker "dies" while this runs
			return ctx.Err()
		}
		return nil
	})
	c1 := h.client("e1a", 0)
	eventID, err := h.write(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.relay.RunOnce(ctx, c1); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(30 * time.Second):
		t.Fatal("job never started")
	}
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := c1.StopAndCancel(sctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	c2 := h.client("e1b", 0)
	jobIDs := h.jobsFor(ctx, consumer, eventID)
	j := waitJob(t, c2, jobIDs[0], 60*time.Second, rivertype.JobStateCompleted, rivertype.JobStateDiscarded)
	if j.State != rivertype.JobStateCompleted || calls.Load() != 2 || h.inboxCount(ctx, consumer, eventID) != 1 {
		t.Fatalf("state %s calls %d", j.State, calls.Load())
	}
}

// (e2) Worker crash: a job left `running` by a dead process is rescued by River's rescuer and completed.
func TestWorkerCrashedJobIsRescued(t *testing.T) {
	h := newHarness(t)
	ctx := ctx(t)
	consumer := "it_crash." + letters(6)
	var calls atomic.Int32
	h.reg.Subscribe(consumer, h.eventType, func(context.Context, outbox.Delivery) error { calls.Add(1); return nil })
	eventID, err := h.write(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	// What a SIGKILLed worker leaves behind: the job is `running`, attempted an hour ago, never finished.
	args, _ := json.Marshal(outbox.DeliverArgs{Consumer: consumer, EventID: eventID})
	var jobID int64
	if err := h.wrk.QueryRow(ctx, `
		INSERT INTO queue.river_job (state, attempt, max_attempts, attempted_at, attempted_by, kind, args, queue, scheduled_at)
		VALUES ('running', 1, 5, now() - interval '1 hour', '{dead-worker}', 'outbox.deliver', $1, $2, now() - interval '1 hour')
		RETURNING id`, args, h.queue).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	c := h.client("e2", 15*time.Second)
	// The rescuer runs on the River leader every 30 s (the container worker or this client).
	j := waitJob(t, c, jobID, 110*time.Second, rivertype.JobStateCompleted, rivertype.JobStateDiscarded)
	if j.State != rivertype.JobStateCompleted || j.Attempt != 2 || calls.Load() != 1 {
		t.Fatalf("state %s attempt %d calls %d", j.State, j.Attempt, calls.Load())
	}
	if len(j.Errors) == 0 || !strings.Contains(j.Errors[0].Error, "Stuck job rescued") {
		t.Fatalf("job was not rescued by River: %+v", j.Errors)
	}
	if h.inboxCount(ctx, consumer, eventID) != 1 {
		t.Fatal("inbox row missing")
	}
}

// --- notifications against Mailpit -----------------------------------------------------------------

func mailpitURL() string {
	if u := os.Getenv("FUNDZIM_IT_MAILPIT_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "http://127.0.0.1:8025"
}

func smtpPort() int {
	if p, err := strconv.Atoi(os.Getenv("MAIL_SMTP_HOST_PORT")); err == nil {
		return p
	}
	return 1025
}

func mailpitEmailConfig(port int) config.Email {
	return config.Email{Provider: "smtp", SMTPHost: "127.0.0.1", SMTPPort: port, SMTPTLS: "none",
		From: "FundZim <no-reply@fundzim.invalid>"}
}

type mailpitMessage struct {
	ID      string `json:"ID"`
	Subject string `json:"Subject"`
	To      []struct {
		Address string `json:"Address"`
	} `json:"To"`
}

// findMail waits until Mailpit holds a message to `to`, and returns its plain-text body.
func findMail(t *testing.T, to string) (mailpitMessage, string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(mailpitURL() + "/api/v1/messages?limit=200")
		if err != nil {
			t.Fatalf("mailpit: %v", err)
		}
		var list struct {
			Messages []mailpitMessage `json:"messages"`
		}
		err = json.NewDecoder(resp.Body).Decode(&list)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range list.Messages {
			for _, a := range m.To {
				if strings.EqualFold(a.Address, to) {
					r2, err := http.Get(mailpitURL() + "/api/v1/message/" + m.ID)
					if err != nil {
						t.Fatal(err)
					}
					var full struct {
						Text string `json:"Text"`
					}
					_ = json.NewDecoder(r2.Body).Decode(&full)
					r2.Body.Close()
					return m, full.Text
				}
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("no Mailpit message to %s", to)
	return mailpitMessage{}, ""
}

// (f) SMTP email and dev SMS arrive in Mailpit; both refuse to send inside a real db.WithTx.
func TestNotificationsDeliverToMailpit(t *testing.T) {
	need(t, "DATABASE_URL")
	email, err := notifications.NewEmailSender(mailpitEmailConfig(smtpPort()))
	if err != nil {
		t.Fatal(err)
	}
	sms, err := notifications.NewSMSSender(config.SMS{Provider: "dev_mailpit"}, email)
	if err != nil {
		t.Fatal(err)
	}
	c := ctx(t)
	tag := letters(8)
	to := "it-" + tag + "@example.test"
	if err := email.Send(c, notifications.Email{To: to, Subject: "Integration " + tag, Text: "plain " + tag, HTML: "<p>html " + tag + "</p>"}); err != nil {
		t.Fatal(err)
	}
	m, text := findMail(t, to)
	if m.Subject != "Integration "+tag || !strings.Contains(text, "plain "+tag) {
		t.Fatalf("email content: %+v %q", m, text)
	}

	n, _ := rand.Int(rand.Reader, big.NewInt(9_000_000))
	phone := fmt.Sprintf("+26377%07d", n.Int64()+1_000_000)
	if err := sms.Send(c, notifications.SMS{To: phone, Body: "Your FundZim code is " + tag}); err != nil {
		t.Fatal(err)
	}
	m, text = findMail(t, strings.TrimPrefix(phone, "+")+"@sms.dev.invalid")
	if m.Subject != "SMS to "+notifications.MaskPhone(phone) || strings.Contains(m.Subject, phone[4:10]) ||
		!strings.Contains(text, "Your FundZim code is "+tag) {
		t.Fatalf("sms content: %+v %q", m, text)
	}

	p := pool(t, os.Getenv("DATABASE_URL"))
	err = db.WithTx(c, p, db.TxOptions{}, func(ctx context.Context, _ pgx.Tx) error {
		if err := email.Send(ctx, notifications.Email{To: to, Subject: "x", Text: "x"}); !errors.Is(err, notifications.ErrInTransaction) {
			return fmt.Errorf("email inside tx: %v", err)
		}
		if err := sms.Send(ctx, notifications.SMS{To: phone, Body: "x"}); !errors.Is(err, notifications.ErrInTransaction) {
			return fmt.Errorf("sms inside tx: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// switchSender forwards to whichever sender is current (lets a test "repair" SMTP between attempts).
type switchSender struct {
	cur atomic.Pointer[notifications.EmailSender]
}

func (s *switchSender) Send(ctx context.Context, m notifications.Email) error {
	return (*s.cur.Load()).Send(ctx, m)
}

// (g) SMTP unreachable: the delivery fails, is retried (not lost) and succeeds once SMTP is back.
func TestNotificationSMTPUnreachableIsRetriedNotLost(t *testing.T) {
	h := newHarness(t)
	ctx := ctx(t)
	ln, _ := net.Listen("tcp", "127.0.0.1:0") // a port with nothing listening
	deadPort := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	dead, _ := notifications.NewEmailSender(mailpitEmailConfig(deadPort))
	live, _ := notifications.NewEmailSender(mailpitEmailConfig(smtpPort()))
	sw := &switchSender{}
	sw.cur.Store(&dead)

	tag := letters(8)
	to := "it-retry-" + tag + "@example.test"
	consumer := "it_notify." + letters(6)
	var failures atomic.Int32
	h.reg.Subscribe(consumer, h.eventType, func(ctx context.Context, d outbox.Delivery) error {
		err := sw.Send(ctx, notifications.Email{To: to, Subject: "Retry " + tag, Text: "event " + d.EventID})
		if err != nil {
			if failures.Add(1) == 2 {
				sw.cur.Store(&live) // SMTP "comes back" after two failed attempts
			}
		}
		return err
	})
	c := h.client("g", 0)
	eventID, err := h.write(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.relay.RunOnce(ctx, c); err != nil {
		t.Fatal(err)
	}
	jobIDs := h.jobsFor(ctx, consumer, eventID)
	j := waitJob(t, c, jobIDs[0], 60*time.Second, rivertype.JobStateCompleted, rivertype.JobStateDiscarded)
	if j.State != rivertype.JobStateCompleted || j.Attempt != 3 || failures.Load() != 2 {
		t.Fatalf("state %s attempt %d failures %d", j.State, j.Attempt, failures.Load())
	}
	for _, e := range j.Errors {
		if !strings.Contains(e.Error, "smtp: connect: connection refused") || strings.Contains(e.Error, to) {
			t.Fatalf("unexpected or leaking job error %q", e.Error)
		}
	}
	if _, text := findMail(t, to); !strings.Contains(text, "event "+eventID) {
		t.Fatalf("mail body %q", text)
	}
	if h.inboxCount(ctx, consumer, eventID) != 1 {
		t.Fatal("inbox row missing")
	}
}

// The worker container is healthy and ready (DB, schema version, River running) and exposes metrics.
func TestWorkerContainerReady(t *testing.T) {
	base := os.Getenv("FUNDZIM_IT_WORKER_URL")
	if base == "" {
		base = "http://127.0.0.1:9091"
	}
	resp, err := http.Get(base + "/readyz")
	if err != nil {
		t.Fatalf("worker internal listener: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"name":"river","ok":true`) {
		t.Fatalf("readyz %d %s", resp.StatusCode, body)
	}
	resp, err = http.Get(base + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, m := range []string{"fundzim_outbox_lag_seconds", "fundzim_jobs_completed_total{kind=\"outbox.relay\"}", "fundzim_outbox_relay_batch_size"} {
		if !strings.Contains(string(body), m) {
			t.Fatalf("metric %s missing", m)
		}
	}
}
