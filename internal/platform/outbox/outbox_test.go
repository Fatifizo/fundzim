package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/jobs"
)

// fakeTx records Exec calls; every other pgx.Tx method panics (nil embedded interface).
type fakeTx struct {
	pgx.Tx
	sql  string
	args []any
}

func (f *fakeTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.sql, f.args = sql, args
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func validEvent() Event {
	return Event{AggregateType: "user", AggregateID: ids.New(), EventType: "identity.email_verification_requested",
		Payload: map[string]string{"user_id": "x"}, CorrelationID: "req-1", OccurredAt: time.Date(2026, 10, 8, 12, 0, 0, 0, time.FixedZone("CAT", 7200))}
}

func TestWriteInsertsInCallersTransaction(t *testing.T) {
	tx := &fakeTx{}
	e := validEvent()
	id, err := Write(context.Background(), tx, e)
	if err != nil {
		t.Fatal(err)
	}
	if !ids.Valid(id) || tx.args[0] != id {
		t.Fatalf("event id %q not used", id)
	}
	if string(tx.args[4].([]byte)) != `{"user_id":"x"}` {
		t.Fatalf("payload %s", tx.args[4])
	}
	if got := tx.args[6].(time.Time); got.Location() != time.UTC || !got.Equal(e.OccurredAt) {
		t.Fatalf("occurred_at %v must be UTC", got)
	}
	if *tx.args[5].(*string) != "req-1" {
		t.Fatal("correlation id")
	}
}

func TestWriteRejectsInvalidEvents(t *testing.T) {
	mut := []func(*Event){
		func(e *Event) { e.AggregateType = "" },
		func(e *Event) { e.AggregateType = "User" },
		func(e *Event) { e.AggregateID = "not-a-uuid" },
		func(e *Event) { e.EventType = "nodot" },
		func(e *Event) { e.EventType = "Identity.X" },
		func(e *Event) { e.OccurredAt = time.Time{} },
		func(e *Event) { e.Payload = []int{1, 2} },
		func(e *Event) { e.Payload = "string" },
		func(e *Event) { e.Payload = map[string]any(nil) },
		func(e *Event) { e.Payload = func() {} },
	}
	for i, m := range mut {
		e := validEvent()
		m(&e)
		tx := &fakeTx{}
		if _, err := Write(context.Background(), tx, e); !errors.Is(err, ErrInvalidEvent) {
			t.Errorf("case %d: want ErrInvalidEvent, got %v", i, err)
		}
		if tx.sql != "" {
			t.Errorf("case %d: invalid event reached the database", i)
		}
	}
	e := validEvent()
	e.Payload = nil
	tx := &fakeTx{}
	if _, err := Write(context.Background(), tx, e); err != nil || string(tx.args[4].([]byte)) != `{}` {
		t.Fatalf("nil payload must become {}: %v", err)
	}
}

func TestRegistrySubscribe(t *testing.T) {
	r := NewRegistry()
	h := func(context.Context, Delivery) error { return nil }
	r.Subscribe("notifications.verify_email", "identity.email_verification_requested", h)
	r.Subscribe("audit.mirror", "identity.email_verification_requested", h)
	r.Subscribe("audit.mirror", "identity.password_reset_requested", h)
	if got := r.Consumers("identity.email_verification_requested"); len(got) != 2 || got[0] != "audit.mirror" {
		t.Fatalf("consumers %v", got)
	}
	if len(r.Consumers("nobody.listens")) != 0 {
		t.Fatal("unexpected consumers")
	}
	if len(r.EventTypes()) != 2 {
		t.Fatal(r.EventTypes())
	}
	for _, c := range []struct{ consumer, event string }{
		{"Bad", "a.b"}, {"nodot", "a.b"}, {"a.b", "bad"}, {"audit.mirror", "identity.password_reset_requested"},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Subscribe(%q,%q) must panic", c.consumer, c.event)
				}
			}()
			r.Subscribe(c.consumer, c.event, h)
		}()
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("nil handler must panic")
			}
		}()
		r.Subscribe("x.y", "a.b", nil)
	}()
}

func TestRelayPlanFansOutUniquely(t *testing.T) {
	r := NewRegistry()
	h := func(context.Context, Delivery) error { return nil }
	r.Subscribe("a.one", "x.created", h)
	r.Subscribe("b.two", "x.created", h)
	rel := &Relay{Registry: r}
	e1, e2 := ids.New(), ids.New()
	params, idList := rel.plan([]claimed{{e1, "x.created"}, {e2, "x.unsubscribed"}})
	if len(idList) != 2 {
		t.Fatal("every claimed event, subscribed or not, is marked dispatched")
	}
	if len(params) != 2 {
		t.Fatalf("want one delivery per subscriber, got %d", len(params))
	}
	for _, p := range params {
		a := p.Args.(DeliverArgs)
		if a.EventID != e1 || a.Kind() != KindDeliver {
			t.Fatalf("%+v", a)
		}
		o := p.InsertOpts
		if o.Queue != jobs.QueueDefault || o.MaxAttempts != DefaultDeliverMaxAttempts || !o.UniqueOpts.ByArgs || len(o.UniqueOpts.ByState) != 8 {
			t.Fatalf("insert opts %+v", o)
		}
	}
	b, _ := json.Marshal(params[0].Args)
	if string(b) != `{"consumer":"a.one","event_id":"`+e1+`"}` {
		t.Fatalf("args carry identifiers only: %s", b)
	}
}

func TestDeliverRejectsMalformedArgsPermanently(t *testing.T) {
	w := &DeliverWorker{Registry: NewRegistry()}
	job := &river.Job[DeliverArgs]{JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: 8}, Args: DeliverArgs{Consumer: "x.y", EventID: "nope"}}
	err := w.Work(context.Background(), job)
	var ce *rivertype.JobCancelError
	if !errors.As(err, &ce) {
		t.Fatalf("want JobCancel, got %v", err)
	}
}

func TestDeliverBackoffDefaults(t *testing.T) {
	w := &DeliverWorker{}
	job := &river.Job[DeliverArgs]{JobRow: &rivertype.JobRow{Attempt: 1}}
	d := time.Until(w.NextRetry(job))
	if d < 3*time.Second || d > 7*time.Second {
		t.Fatalf("first retry after %s, want ~5s", d)
	}
	if w.Timeout(job) != 30*time.Second {
		t.Fatal("default timeout")
	}
}

func TestPeriodicJobsDefined(t *testing.T) {
	if PeriodicRelay(0) == nil || len(PeriodicMaintenance(0)) != 2 {
		t.Fatal("periodic jobs")
	}
	if (RelayArgs{}).InsertOpts().Queue != jobs.QueueOutbox || (PurgeArgs{}).InsertOpts().Queue != jobs.QueueMaintenance {
		t.Fatal("queues")
	}
}
