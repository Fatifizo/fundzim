// Package outbox is FundZim's transactional outbox and consumer inbox (ADR-025, interface contract §2.2,
// docs/architecture/background-processing.md §4).
//
// Producers call Write inside the transaction that makes their change, so the change and the fact that it
// happened commit (or roll back) together. The worker's relay job claims undispatched rows with
// FOR UPDATE SKIP LOCKED and inserts one River job `outbox.deliver{consumer, event_id}` per subscriber in
// the same transaction that marks the row dispatched. The deliver job skips a (consumer, event) pair that
// already has an app.inbox_events row, otherwise runs the handler OUTSIDE any transaction and then records
// the inbox row. Delivery is therefore at least once: handlers must be idempotent or tolerate duplicates.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Event is a domain fact written to app.outbox_events.
type Event struct {
	AggregateType string    // e.g. "user"
	AggregateID   string    // UUID
	EventType     string    // e.g. "identity.email_verification_requested"
	Payload       any       // marshalled to a JSON object; IDs and minimal fields only, never secrets
	CorrelationID string    // request ID
	OccurredAt    time.Time // business time from the injected clock
}

var (
	eventTypeRE = regexp.MustCompile(`^[a-z_]+(\.[a-z_]+)+$`)
	consumerRE  = eventTypeRE // same shape: "<module>.<purpose>"
	aggTypeRE   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
)

// ErrInvalidEvent is returned by Write for an event that would violate the table's constraints.
var ErrInvalidEvent = errors.New("outbox: invalid event")

// Write inserts the event in the caller's transaction (app.outbox_events). Returns the event ID.
func Write(ctx context.Context, tx pgx.Tx, e Event) (string, error) {
	payload, err := validate(e)
	if err != nil {
		return "", err
	}
	id := ids.New()
	var corr *string
	if e.CorrelationID != "" {
		corr = &e.CorrelationID
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO app.outbox_events (id, aggregate_type, aggregate_id, event_type, payload, correlation_id, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id, e.AggregateType, e.AggregateID, e.EventType, payload, corr, e.OccurredAt.UTC())
	if err != nil {
		return "", fmt.Errorf("outbox: write %s: %w", e.EventType, err)
	}
	return id, nil
}

// validate checks the event and returns its payload as a JSON object.
func validate(e Event) ([]byte, error) {
	if !aggTypeRE.MatchString(e.AggregateType) {
		return nil, fmt.Errorf("%w: aggregate type must be lower snake case", ErrInvalidEvent)
	}
	if !ids.Valid(e.AggregateID) {
		return nil, fmt.Errorf("%w: aggregate ID must be a UUID", ErrInvalidEvent)
	}
	if !eventTypeRE.MatchString(e.EventType) {
		return nil, fmt.Errorf("%w: event type must look like module.event_name", ErrInvalidEvent)
	}
	if e.OccurredAt.IsZero() {
		return nil, fmt.Errorf("%w: OccurredAt is required", ErrInvalidEvent)
	}
	if e.Payload == nil {
		return []byte(`{}`), nil
	}
	b, err := json.Marshal(e.Payload)
	if err != nil {
		return nil, fmt.Errorf("%w: payload: %v", ErrInvalidEvent, err)
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(b, &obj) != nil || obj == nil {
		return nil, fmt.Errorf("%w: payload must marshal to a JSON object", ErrInvalidEvent)
	}
	return b, nil
}

// Delivery is one event handed to one consumer.
type Delivery struct {
	EventID, EventType, AggregateType, AggregateID, CorrelationID string
	Payload                                                       json.RawMessage
	OccurredAt                                                    time.Time
	Attempt                                                       int
}

// Handler processes one delivery. It runs OUTSIDE any database transaction, so it may call external
// services; it opens its own transactions as needed. Delivery is at least once: handlers must be
// idempotent or tolerate a duplicate.
type Handler func(ctx context.Context, d Delivery) error

type subscription struct {
	consumer string
	handler  Handler
}

// Registry maps event types to consumers. It is filled at start-up (internal/app/consumers.go) and read
// concurrently afterwards.
type Registry struct {
	mu   sync.RWMutex
	subs map[string][]subscription // event type -> consumers (sorted by name)
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{subs: map[string][]subscription{}} }

// Subscribe registers a consumer ("<module>.<purpose>", lower snake, dotted) for one event type. A
// consumer name is part of the dedupe key, so renaming one re-delivers history: treat names as stable.
// It panics on programming errors (bad names, nil handler, duplicate subscription).
func (r *Registry) Subscribe(consumer, eventType string, h Handler) {
	if !consumerRE.MatchString(consumer) || len(consumer) > 120 {
		panic(fmt.Sprintf("outbox: invalid consumer name %q (want module.purpose, lower snake)", consumer))
	}
	if !eventTypeRE.MatchString(eventType) {
		panic(fmt.Sprintf("outbox: invalid event type %q", eventType))
	}
	if h == nil {
		panic("outbox: nil handler for " + consumer)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.subs[eventType] {
		if s.consumer == consumer {
			panic(fmt.Sprintf("outbox: %s already subscribed to %s", consumer, eventType))
		}
	}
	list := append(r.subs[eventType], subscription{consumer: consumer, handler: h})
	sort.Slice(list, func(a, b int) bool { return list[a].consumer < list[b].consumer })
	r.subs[eventType] = list
}

// Consumers returns the consumers subscribed to eventType, sorted.
func (r *Registry) Consumers(eventType string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.subs[eventType]))
	for _, s := range r.subs[eventType] {
		out = append(out, s.consumer)
	}
	return out
}

// EventTypes returns every event type with at least one subscriber, sorted.
func (r *Registry) EventTypes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.subs))
	for t := range r.subs {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

func (r *Registry) handler(consumer, eventType string) Handler {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.subs[eventType] {
		if s.consumer == consumer {
			return s.handler
		}
	}
	return nil
}

// Consume records (consumer, event) in app.inbox_events inside the caller's transaction and reports
// whether this is the first time. A handler whose effect is a database change can call it in the same
// transaction as the effect for exactly-once effects: if first is false, it must skip the effect. The
// deliver job's own inbox insert afterwards is then a no-op.
func Consume(ctx context.Context, tx pgx.Tx, consumer string, d Delivery) (first bool, err error) {
	tag, err := tx.Exec(ctx, `
		INSERT INTO app.inbox_events (id, consumer, event_id, event_type) VALUES ($1, $2, $3, $4)
		ON CONFLICT (consumer, event_id) DO NOTHING`, ids.New(), consumer, d.EventID, d.EventType)
	if err != nil {
		return false, fmt.Errorf("outbox: inbox insert: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
