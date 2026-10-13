package psp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// Webhook ingestion limits and dispatch policy (interface-contracts §3).
const (
	MaxWebhookBytes     = 64 << 10
	MaxDispatchAttempts = 8
	// ParkDeadline is how long an event waits for its prerequisite before it raises a reconciliation case
	// (payment-state-machine §5; 72 h default). It is never discarded.
	ParkDeadline = 72 * time.Hour
)

// Outbox event and consumer for dispatch (the API has no job client; the outbox carries the work durably).
const (
	EvWebhookReceived  = "psp.webhook_received"
	ConsumerDispatch   = "psp.webhook_dispatch"
	aggregateInboxType = "provider_webhook"
)

// Inbox statuses.
const (
	InboxReceived       = "RECEIVED"
	InboxDispatched     = "DISPATCHED"
	InboxParked         = "PARKED"
	InboxFailedRetrying = "FAILED_RETRYING"
	InboxProcessed      = "PROCESSED"
	InboxUnrecognised   = "UNRECOGNISED"
	InboxDeadLettered   = "DEAD_LETTERED"
)

// Webhook errors.
var (
	ErrWebhookProviderUnknown = errs.New(errs.NotFound, "WEBHOOK_PROVIDER_UNKNOWN", "Unknown webhook endpoint.")
	ErrWebhookSignature       = errs.New(errs.Unauthenticated, "WEBHOOK_SIGNATURE_INVALID", "Webhook signature verification failed.")
	ErrWebhookTimestamp       = errs.New(errs.Unauthenticated, "WEBHOOK_TIMESTAMP_INVALID", "Webhook timestamp is outside the accepted window.")
	ErrWebhookMalformed       = errs.New(errs.Invalid, "WEBHOOK_MALFORMED", "Webhook payload could not be parsed.")
	ErrWebhookTooLarge        = errs.New(errs.TooLarge, errs.CodePayloadTooLarge, "Webhook payload is too large.")
	ErrWebhookMediaType       = errs.New(errs.UnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Webhooks must be application/json.")
	ErrInboxNotRequeueable    = errs.New(errs.Conflict, "INBOX_EVENT_NOT_REQUEUEABLE", "Only dead-lettered or unrecognised events can be re-queued.")
	ErrInboxNotFound          = errs.New(errs.NotFound, "INBOX_EVENT_NOT_FOUND", "No such webhook event.")
)

var providerCodeRE = regexp.MustCompile(`^[a-z][a-z0-9_]{1,31}$`)

// InboxEvent is a stored webhook handed to the owning module's handler.
type InboxEvent struct {
	ID                string
	ProviderID        string
	ProviderCode      string
	ProviderEventID   string
	Class             string
	TypeRaw           string
	ProviderReference string
	OurReference      string
	OccurredAt        *time.Time
	ReceivedAt        time.Time
	RawStatus         string
	ReportedAmount    *int64
	ReportedCcy       string
	Simulated         bool
	Attempt           int
}

// EventHandler applies an inbox event. It must, in ONE transaction, apply the event and call exactly one of
// MarkProcessed, Park or MarkUnrecognised. A returned error schedules a retry.
type EventHandler func(ctx context.Context, ev InboxEvent) error

// Handle registers the handler for an event class (PAYMENT, REFUND, …). Composition root only.
func (s *Service) Handle(class string, h EventHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[class] = h
}

func (s *Service) handler(class string) EventHandler {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.handlers[class]
}

// normalised is stored inside payload_redacted so the event can be re-applied without the raw body.
type normalised struct {
	RawStatus      string `json:"raw_status"`
	ReportedAmount *int64 `json:"reported_amount_minor,omitempty"`
	ReportedCcy    string `json:"reported_currency,omitempty"`
}

// HandleWebhook is POST /api/v1/webhooks/payments/{provider} (public; CSRF-exempt by path; rate limited by
// route). Rejections store no payload; accepted events are stored once and dispatched asynchronously.
func (s *Service) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	start := s.Clock.Now()
	code := r.PathValue("provider")
	received := s.now()
	if !providerCodeRE.MatchString(code) {
		httpx.WriteError(w, r, s.Logger, ErrWebhookProviderUnknown)
		return
	}
	p, err := s.ProviderByCode(r.Context(), code)
	if errors.Is(err, ErrProviderNotFound) {
		s.reject(r.Context(), nil, code, "UNKNOWN_PROVIDER", nil, 0, received)
		httpx.WriteError(w, r, s.Logger, ErrWebhookProviderUnknown)
		return
	}
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	usable, err := s.Usable(r.Context(), p)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	a, ok := s.Adapter(p.Code)
	if !usable || !ok {
		s.reject(r.Context(), &p, code, "PROVIDER_DISABLED", nil, 0, received)
		httpx.WriteError(w, r, s.Logger, ErrWebhookProviderUnknown)
		return
	}
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "application/json" {
		s.reject(r.Context(), &p, code, "UNSUPPORTED_MEDIA_TYPE", nil, 0, received)
		httpx.WriteError(w, r, s.Logger, ErrWebhookMediaType)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxWebhookBytes+1))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			s.reject(r.Context(), &p, code, "PAYLOAD_TOO_LARGE", nil, 0, received)
			httpx.WriteError(w, r, s.Logger, ErrWebhookTooLarge)
			return
		}
		httpx.WriteError(w, r, s.Logger, ErrWebhookMalformed)
		return
	}
	if len(body) > MaxWebhookBytes {
		s.reject(r.Context(), &p, code, "PAYLOAD_TOO_LARGE", nil, len(body), received)
		httpx.WriteError(w, r, s.Logger, ErrWebhookTooLarge)
		return
	}
	sum := sha256.Sum256(body)
	acct, err := s.ActiveAccount(r.Context(), p)
	if errors.Is(err, ErrNoAccount) {
		s.reject(r.Context(), &p, code, "NO_ACCOUNT", sum[:], len(body), received)
		httpx.WriteError(w, r, s.Logger, ErrWebhookProviderUnknown)
		return
	}
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	in := Inbound{Body: body, Header: r.Header.Clone(), ReceivedAt: received}
	vb, err := a.Verify(r.Context(), acct, in)
	if err != nil {
		reason, apiErr := verifyReason(err)
		s.reject(r.Context(), &p, code, reason, sum[:], len(body), received)
		s.Logger.Warn("psp: webhook rejected", "provider", code, "reason", reason, "body_sha256", fmt.Sprintf("%x", sum[:8]))
		httpx.WriteError(w, r, s.Logger, apiErr)
		return
	}
	events, err := a.Parse(r.Context(), in)
	if err == nil {
		for _, ev := range events {
			if ev.Simulated != p.IsSandbox {
				err = ErrSimulation
			}
		}
	}
	if err != nil || len(events) == 0 {
		reason := "MALFORMED_PAYLOAD"
		if errors.Is(err, ErrSimulation) {
			reason = "SIMULATION_MISMATCH"
		}
		s.reject(r.Context(), &p, code, reason, sum[:], len(body), received)
		httpx.WriteError(w, r, s.Logger, ErrWebhookMalformed)
		return
	}
	dup := true
	err = db.WithTx(r.Context(), s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		for _, ev := range events {
			stored, err := s.store(ctx, tx, p, acct, vb, ev, sum[:], received, r.Header)
			if err != nil {
				return err
			}
			if stored {
				dup = false
			}
		}
		return nil
	})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	s.Metrics.WebhooksReceived.WithLabelValues(code, strconv.FormatBool(dup)).Inc()
	s.Metrics.Duration.WithLabelValues("webhook_ingest").Observe(s.Clock.Now().Sub(start).Seconds())
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"received": true, "duplicate": dup})
}

func verifyReason(err error) (string, error) {
	switch {
	case errors.Is(err, ErrSignatureMissing):
		return "SIGNATURE_MISSING", ErrWebhookSignature
	case errors.Is(err, ErrUnknownKey):
		return "UNKNOWN_KEY", ErrWebhookSignature
	case errors.Is(err, ErrTimestamp):
		return "TIMESTAMP_OUT_OF_WINDOW", ErrWebhookTimestamp
	case errors.Is(err, ErrMalformed):
		return "MALFORMED_PAYLOAD", ErrWebhookMalformed
	}
	return "SIGNATURE_INVALID", ErrWebhookSignature
}

// store inserts one verified event (dedupe on (provider, provider_event_id)) and its dispatch outbox event.
func (s *Service) store(ctx context.Context, tx pgx.Tx, p Provider, acct Account, vb VerifiedBy, ev WebhookEvent, sum []byte,
	received time.Time, h http.Header) (bool, error) {
	class := ev.Class
	switch class {
	case "PAYMENT", "REFUND", "DISPUTE", "PAYOUT":
	default:
		class = "OTHER"
	}
	payload := map[string]any{"provider": ev.Redacted, "normalised": normalised{RawStatus: ev.RawStatus,
		ReportedAmount: ev.ReportedAmount, ReportedCcy: ev.ReportedCcy}}
	pj, err := json.Marshal(payload)
	if err != nil {
		return false, err
	}
	hj, _ := json.Marshal(headerSubset(h))
	var our *string
	if ids.Valid(ev.OurReference) {
		v := ev.OurReference
		our = &v
	}
	id := ids.New()
	tag, err := tx.Exec(ctx, `INSERT INTO app.provider_webhook_inbox (id, provider_id, provider_account_id, provider_event_id, event_class,
		event_type_raw, provider_reference, our_reference, provider_occurred_at, received_at, signature_verified, verification_method,
		signature_key_slot, payload_redacted, raw_body_sha256, headers_subset, is_simulated, status)
		VALUES ($1, $2, $3, $4, $5, $6, nullif($7, ''), $8, $9, $10, true, $11, nullif($12, ''), $13, $14, $15, $16, 'RECEIVED')
		ON CONFLICT (provider_id, provider_event_id) DO NOTHING`,
		id, p.ID, acct.ID, ev.ProviderEventID, class, ev.TypeRaw, ev.ProviderReference, our, ev.OccurredAt, received,
		vb.Method, vb.KeySlot, pj, sum, hj, p.IsSandbox)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil // duplicate: acknowledged, not reprocessed
	}
	_, err = outbox.Write(ctx, tx, outbox.Event{AggregateType: aggregateInboxType, AggregateID: id, EventType: EvWebhookReceived,
		Payload: map[string]string{"inbox_id": id}, CorrelationID: httpx.RequestID(ctx), OccurredAt: received})
	return err == nil, err
}

// headerSubset keeps only non-sensitive, non-identifying headers (never signatures or credentials).
func headerSubset(h http.Header) map[string]string {
	out := map[string]string{}
	for _, k := range []string{"Content-Type", "User-Agent"} {
		if v := h.Get(k); v != "" && len(v) <= 200 {
			out[k] = v
		}
	}
	return out
}

// authReasons are the rejection reasons that mean a delivery failed authentication or integrity checks.
var authReasons = map[string]bool{"SIGNATURE_MISSING": true, "SIGNATURE_INVALID": true, "UNKNOWN_KEY": true,
	"TIMESTAMP_OUT_OF_WINDOW": true, "SIMULATION_MISMATCH": true, "MALFORMED_PAYLOAD": true}

func (s *Service) reject(ctx context.Context, p *Provider, code, reason string, sum []byte, n int, at time.Time) {
	prov := code
	if !providerCodeRE.MatchString(prov) {
		prov = "unknown"
	}
	s.Metrics.WebhooksRejected.WithLabelValues(prov, reason).Inc()
	var pid *string
	if p != nil {
		pid = &p.ID
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	id := ids.New()
	if _, err := s.Pool.Exec(ctx, `INSERT INTO app.provider_webhook_rejections (id, provider_id, provider_code, reason_code, body_sha256,
		body_bytes, received_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`, id, pid, prov, reason, sum, n, at); err != nil {
		s.Logger.Warn("psp: record webhook rejection failed", "err", err)
	}
	// WEBHOOK_REJECTED is audited for authentication failures against a known provider (security stream). Unknown
	// endpoints and transport refusals stay in the rejection log only, so scanners cannot flood the audit chain.
	if p != nil && authReasons[reason] {
		if err := db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			return audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "psp.webhook.rejected", ActorType: "provider",
				TargetType: "provider_webhook_rejection", TargetID: id, Outcome: "denied", Reason: reason,
				Metadata: map[string]any{"audit_event": "WEBHOOK_REJECTED", "provider": p.Code, "reason_code": reason}})
		}); err != nil {
			s.Logger.Warn("psp: webhook rejection audit failed", "err", err)
		}
	}
}

// Dispatch is the outbox consumer for psp.webhook_received: claim the inbox row, call the class handler,
// record failures, and dead-letter after MaxDispatchAttempts (staff re-queue only). It is idempotent: a
// processed, parked, unrecognised or dead-lettered row is left alone.
func (s *Service) Dispatch(ctx context.Context, d outbox.Delivery) error {
	var p struct {
		InboxID string `json:"inbox_id"`
	}
	if err := json.Unmarshal(d.Payload, &p); err != nil || !ids.Valid(p.InboxID) {
		return fmt.Errorf("psp: malformed dispatch payload")
	}
	start := s.Clock.Now()
	ev, run, err := s.claim(ctx, p.InboxID)
	if err != nil || !run {
		return err
	}
	ev.Attempt = d.Attempt
	h := s.handler(ev.Class)
	if h == nil {
		return db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			return s.MarkUnrecognised(ctx, tx, ev.ID, "NO_HANDLER_FOR_CLASS")
		})
	}
	herr := h(ctx, ev)
	s.Metrics.Duration.WithLabelValues("webhook_apply").Observe(s.Clock.Now().Sub(start).Seconds())
	if herr == nil {
		return nil
	}
	code := "HANDLER_ERROR"
	if e := errs.As(herr); e != nil && e.Code != "" {
		code = e.Code
	}
	dead := d.Attempt >= MaxDispatchAttempts
	if err := db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		next := InboxFailedRetrying
		if dead {
			next = InboxDeadLettered
		}
		var cur string
		if err := tx.QueryRow(ctx, `SELECT status FROM app.provider_webhook_inbox WHERE id = $1 FOR UPDATE`, ev.ID).Scan(&cur); err != nil {
			return err
		}
		if cur != InboxDispatched {
			return nil // the handler finished the row after all
		}
		if !dead {
			// FAILED_RETRYING is reached only from DISPATCHED; the retry moves it back to DISPATCHED.
			_, err := tx.Exec(ctx, `UPDATE app.provider_webhook_inbox SET status = $2, last_error_code = $3, next_attempt_at = $4,
				version = version + 1 WHERE id = $1`, ev.ID, next, code, s.now().Add(time.Minute))
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app.provider_webhook_inbox SET status = 'FAILED_RETRYING', last_error_code = $2,
			version = version + 1 WHERE id = $1`, ev.ID, code); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE app.provider_webhook_inbox SET status = 'DEAD_LETTERED', version = version + 1 WHERE id = $1`, ev.ID)
		return err
	}); err != nil {
		return err
	}
	if dead {
		s.Logger.Error("psp: webhook dead-lettered", "inbox_id", ev.ID, "provider", ev.ProviderCode, "error_code", code)
		return nil
	}
	return herr
}

// claim moves a runnable row to DISPATCHED (RECEIVED, FAILED_RETRYING, or DISPATCHED after a crash) and loads it.
func (s *Service) claim(ctx context.Context, inboxID string) (InboxEvent, bool, error) {
	var ev InboxEvent
	run := false
	err := db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		var status, method, slot string
		var payload []byte
		err := tx.QueryRow(ctx, `SELECT i.id, i.provider_id, p.code, i.provider_event_id, i.event_class, i.event_type_raw,
			coalesce(i.provider_reference, ''), coalesce(i.our_reference::text, ''), i.provider_occurred_at, i.received_at,
			i.payload_redacted, i.is_simulated, i.status, i.verification_method, coalesce(i.signature_key_slot, '')
			FROM app.provider_webhook_inbox i JOIN app.payment_providers p ON p.id = i.provider_id WHERE i.id = $1 FOR UPDATE OF i`, inboxID).
			Scan(&ev.ID, &ev.ProviderID, &ev.ProviderCode, &ev.ProviderEventID, &ev.Class, &ev.TypeRaw, &ev.ProviderReference,
				&ev.OurReference, &ev.OccurredAt, &ev.ReceivedAt, &payload, &ev.Simulated, &status, &method, &slot)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var pl struct {
			Normalised normalised `json:"normalised"`
		}
		_ = json.Unmarshal(payload, &pl)
		ev.RawStatus, ev.ReportedAmount, ev.ReportedCcy = pl.Normalised.RawStatus, pl.Normalised.ReportedAmount, pl.Normalised.ReportedCcy
		switch status {
		case InboxReceived, InboxFailedRetrying, InboxDispatched:
		default:
			return nil
		}
		// WEBHOOK_VERIFIED is audited on the first claim, not in the ingest transaction: the hash-chained audit log
		// serialises writers, and the provider-facing endpoint must stay fast (Stage 9 performance, testing.md §4).
		// Every stored event is claimed before anything is applied, so the record precedes any effect.
		if status == InboxReceived {
			if err := audit.Record(ctx, tx, audit.Event{Action: "psp.webhook.verified", ActorType: "provider", TargetType: "provider_webhook",
				TargetID: ev.ID, OccurredAt: ev.ReceivedAt, Metadata: map[string]any{"audit_event": "WEBHOOK_VERIFIED", "provider": ev.ProviderCode,
					"event_class": ev.Class, "verification_method": method, "key_slot": slot, "simulated": ev.Simulated}}); err != nil {
				return err
			}
		}
		if ev.Class == "OTHER" {
			return s.MarkUnrecognised(ctx, tx, ev.ID, "UNMAPPED_EVENT_TYPE")
		}
		run = true
		_, err = tx.Exec(ctx, `UPDATE app.provider_webhook_inbox SET status = 'DISPATCHED', dispatched_to = 'payments',
			attempts = attempts + 1, next_attempt_at = NULL, version = version + 1 WHERE id = $1`, ev.ID)
		return err
	})
	return ev, run, err
}

// MarkProcessed completes a dispatched row in the handler's transaction.
func (s *Service) MarkProcessed(ctx context.Context, tx pgx.Tx, inboxID string) error {
	_, err := tx.Exec(ctx, `UPDATE app.provider_webhook_inbox SET status = 'PROCESSED', processed_at = $2, last_error_code = NULL,
		parked_subject_id = NULL, parked_until_status = NULL, version = version + 1 WHERE id = $1 AND status = 'DISPATCHED'`, inboxID, s.now())
	return err
}

// Park keeps an event until its subject reaches untilStatus (never discarded; deadline ⇒ reconciliation case).
func (s *Service) Park(ctx context.Context, tx pgx.Tx, inboxID, subjectID, untilStatus string) error {
	now := s.now()
	_, err := tx.Exec(ctx, `UPDATE app.provider_webhook_inbox SET status = 'PARKED', parked_subject_id = $2, parked_until_status = $3,
		parked_at = $4, park_deadline_at = coalesce(park_deadline_at, $5), version = version + 1 WHERE id = $1 AND status = 'DISPATCHED'`,
		inboxID, subjectID, untilStatus, now, now.Add(ParkDeadline))
	return err
}

// MarkUnrecognised keeps an event the owning module cannot map (alert; re-dispatchable after a mapping change).
func (s *Service) MarkUnrecognised(ctx context.Context, tx pgx.Tx, inboxID, code string) error {
	_, err := tx.Exec(ctx, `UPDATE app.provider_webhook_inbox SET status = 'UNRECOGNISED', last_error_code = $2, version = version + 1
		WHERE id = $1 AND status IN ('RECEIVED','DISPATCHED')`, inboxID, code)
	if err == nil {
		s.Logger.Warn("psp: webhook event unrecognised (ALERT)", "inbox_id", inboxID, "code", code)
	}
	return err
}

// ReleaseParked re-dispatches, in the caller's transaction, every event parked on subjectID waiting for one of
// the reached statuses, in provider order (payment-state-machine §5).
func (s *Service) ReleaseParked(ctx context.Context, tx pgx.Tx, subjectID string, reached ...string) (int, error) {
	rows, err := tx.Query(ctx, `SELECT id FROM app.provider_webhook_inbox WHERE status = 'PARKED' AND parked_subject_id = $1
		AND parked_until_status = ANY($2) ORDER BY provider_occurred_at NULLS LAST, provider_event_id, received_at FOR UPDATE`, subjectID, reached)
	if err != nil {
		return 0, err
	}
	list, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	for _, id := range list {
		if err := s.redispatch(ctx, tx, id); err != nil {
			return 0, err
		}
	}
	return len(list), nil
}

func (s *Service) redispatch(ctx context.Context, tx pgx.Tx, id string) error {
	if _, err := tx.Exec(ctx, `UPDATE app.provider_webhook_inbox SET status = 'DISPATCHED', version = version + 1 WHERE id = $1`, id); err != nil {
		return err
	}
	_, err := outbox.Write(ctx, tx, outbox.Event{AggregateType: aggregateInboxType, AggregateID: id, EventType: EvWebhookReceived,
		Payload: map[string]string{"inbox_id": id}, OccurredAt: s.now()})
	return err
}

// Requeue is the audited staff re-queue of a DEAD_LETTERED or UNRECOGNISED event (interface-contracts §4).
func (s *Service) Requeue(ctx context.Context, staffID, inboxID, reason string) error {
	if !ids.Valid(inboxID) {
		return ErrInboxNotFound
	}
	return db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		err := tx.QueryRow(ctx, `SELECT status FROM app.provider_webhook_inbox WHERE id = $1 FOR UPDATE`, inboxID).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInboxNotFound
		}
		if err != nil {
			return err
		}
		if status != InboxDeadLettered && status != InboxUnrecognised {
			return ErrInboxNotRequeueable
		}
		if err := s.redispatch(ctx, tx, inboxID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "payments.webhook.requeued", ActorType: "staff", ActorID: staffID,
			TargetType: "provider_webhook", TargetID: inboxID, Metadata: map[string]any{"from_status": status, "reason": reason}})
	})
}

// OverdueParked lists parked events past their deadline (payments raises reconciliation references).
func (s *Service) OverdueParked(ctx context.Context, limit int) ([]InboxEvent, error) {
	rows, err := s.Pool.Query(ctx, `SELECT i.id, i.provider_id, p.code, i.provider_event_id, i.event_class, coalesce(i.parked_subject_id::text, '')
		FROM app.provider_webhook_inbox i JOIN app.payment_providers p ON p.id = i.provider_id
		WHERE i.status = 'PARKED' AND i.park_deadline_at < $1 ORDER BY i.park_deadline_at LIMIT $2`, s.now(), limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (InboxEvent, error) {
		var e InboxEvent
		err := r.Scan(&e.ID, &e.ProviderID, &e.ProviderCode, &e.ProviderEventID, &e.Class, &e.OurReference)
		return e, err
	})
}
