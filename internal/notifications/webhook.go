package notifications

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// DeliveryEdges mirrors the notification_delivery machine of migration 20261018090000 (a unit test compares them).
var DeliveryEdges = map[string][]string{
	"":                 {"QUEUED", "SUPPRESSED", "DELIVERED"},
	"QUEUED":           {"PROCESSING", "SUPPRESSED", "CANCELLED"},
	"PROCESSING":       {"SUBMITTED", "ACCEPTED", "DELIVERED", "FAILED_RETRYABLE", "FAILED_PERMANENT", "SUPPRESSED", "UNKNOWN"},
	"SUBMITTED":        {"ACCEPTED", "DELIVERED", "READ", "FAILED_PERMANENT"},
	"ACCEPTED":         {"DELIVERED", "READ", "FAILED_PERMANENT"},
	"DELIVERED":        {"READ"},
	"FAILED_RETRYABLE": {"PROCESSING", "FAILED_PERMANENT", "SUPPRESSED", "CANCELLED"},
	"UNKNOWN":          {"ACCEPTED", "DELIVERED", "READ", "FAILED_RETRYABLE", "FAILED_PERMANENT", "QUEUED"},
	"FAILED_PERMANENT": {"QUEUED", "CANCELLED"},
}

var rank = map[string]int{"SUBMITTED": 1, "ACCEPTED": 2, "DELIVERED": 3, "READ": 4}

// Not-applied reasons of a provider callback.
const (
	NotInFlight        = "IN_FLIGHT"  // the send result is not recorded yet; applied when it is
	NotLowerRank       = "LOWER_RANK" // out of order: the delivery is already further along
	NotNoChange        = "NO_CHANGE"
	NotConflict        = "CONFLICTS_WITH_DELIVERED" // a failure after delivery: kept as evidence, flagged, never regresses
	NotConflictFailure = "CONFLICTS_WITH_FAILURE"
	NotTerminal        = "DELIVERY_CLOSED"
	NotEdge            = "NOT_AN_EDGE"
	NotInformational   = "INFORMATIONAL"
	NotUnknownMessage  = "UNKNOWN_MESSAGE"
)

// ApplyCallback decides how a reported provider status changes a delivery: the next status ("" for none), the reason a
// report is not applied, and whether it contradicts the recorded state. Deterministic, so duplicate and out-of-order
// callbacks always end in the same state.
func ApplyCallback(current, reported string) (next, reason string, conflict bool) {
	switch reported {
	case "SUBMITTED", "ACCEPTED", "DELIVERED", "READ":
		switch current {
		case "PROCESSING":
			return "", NotInFlight, false
		case "FAILED_PERMANENT":
			return "", NotConflictFailure, true
		case "SUPPRESSED", "CANCELLED", "QUEUED":
			return "", NotTerminal, false
		}
		if rank[reported] == rank[current] {
			return "", NotNoChange, false
		}
		if rank[reported] < rank[current] {
			return "", NotLowerRank, false
		}
		if !contains(DeliveryEdges[current], reported) {
			return "", NotEdge, false
		}
		return reported, "", false
	case "BOUNCED", "REJECTED", "UNDELIVERABLE", "FAILED":
		switch current {
		case "PROCESSING":
			return "", NotInFlight, false
		case "DELIVERED", "READ":
			return "", NotConflict, true
		case "FAILED_PERMANENT":
			return "", NotNoChange, false
		case "SUBMITTED", "ACCEPTED", "UNKNOWN":
			return "FAILED_PERMANENT", "", false
		}
		return "", NotEdge, false
	}
	return "", NotInformational, false
}

// suppressionFor: a destination-level failure suppresses the destination.
func suppressionFor(reported string) string {
	return map[string]string{"BOUNCED": "HARD_BOUNCE", "UNDELIVERABLE": "INVALID_DESTINATION", "REJECTED": "PROVIDER_REJECTION"}[reported]
}

// WebhookResult is the HTTP status a webhook answer should carry.
type WebhookResult struct {
	Status    int
	Duplicate bool
	Applied   bool
}

func (s *Service) reject(ctx context.Context, provider, reason string) {
	_, _ = s.Pool.Exec(ctx, `INSERT INTO app.notification_webhook_rejections (id, provider_code, reason) VALUES ($1, $2, $3)`, ids.New(), provider, reason)
}

// HandleWebhook authenticates a provider callback, stores it once as evidence and applies it.
func (s *Service) HandleWebhook(ctx context.Context, provider string, h http.Header, body []byte) (WebhookResult, error) {
	prov := s.Providers[provider]
	if prov == nil {
		s.reject(ctx, "unknown", "UNKNOWN_PROVIDER")
		return WebhookResult{Status: http.StatusNotFound}, nil
	}
	cb, err := prov.VerifyWebhook(h, body, s.now())
	switch {
	case errors.Is(err, ErrWebhookUnsupported):
		s.reject(ctx, provider, "UNKNOWN_PROVIDER")
		return WebhookResult{Status: http.StatusNotFound}, nil
	case errors.Is(err, ErrWebhookSignature):
		s.reject(ctx, provider, "SIGNATURE_INVALID")
		return WebhookResult{Status: http.StatusUnauthorized}, nil
	case errors.Is(err, ErrWebhookTimestamp):
		s.reject(ctx, provider, "TIMESTAMP_OUT_OF_WINDOW")
		return WebhookResult{Status: http.StatusUnauthorized}, nil
	case err != nil:
		s.reject(ctx, provider, "PAYLOAD_INVALID")
		return WebhookResult{Status: http.StatusBadRequest}, nil
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return WebhookResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var deliveryID, status, channel string
	var hash []byte
	err = tx.QueryRow(ctx, `SELECT id::text, status, channel, destination_hash FROM app.notification_deliveries
		WHERE provider_code = $1 AND provider_message_id = $2 FOR UPDATE`, provider, cb.ProviderMessageID).Scan(&deliveryID, &status, &channel, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		// the message may still be in flight (the send result not yet recorded): keep the report as IN_FLIGHT evidence
		tag, err := tx.Exec(ctx, `INSERT INTO app.notification_provider_events (id, provider_code, provider_event_id, provider_message_id, reported_status,
			occurred_at, received_at, applied, not_applied_reason) VALUES ($1,$2,$3,$4,$5,$6,$7,false,'IN_FLIGHT') ON CONFLICT DO NOTHING`,
			ids.New(), provider, cb.ProviderEventID, cb.ProviderMessageID, cb.Status, cb.OccurredAt, s.now())
		if err != nil {
			return WebhookResult{}, err
		}
		return WebhookResult{Status: http.StatusOK, Duplicate: tag.RowsAffected() == 0}, tx.Commit(ctx)
	}
	if err != nil {
		return WebhookResult{}, err
	}
	next, reason, conflict := ApplyCallback(status, cb.Status)
	var applied bool
	var notApplied any = reason
	if reason == "" {
		applied, notApplied = true, nil
	}
	var appliedAt any
	if applied {
		appliedAt = s.now()
	}
	tag, err := tx.Exec(ctx, `INSERT INTO app.notification_provider_events (id, provider_code, provider_event_id, provider_message_id, delivery_id,
		reported_status, occurred_at, received_at, applied, not_applied_reason, applied_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT DO NOTHING`,
		ids.New(), provider, cb.ProviderEventID, cb.ProviderMessageID, deliveryID, cb.Status, cb.OccurredAt, s.now(), applied, notApplied, appliedAt)
	if err != nil {
		return WebhookResult{}, err
	}
	if tag.RowsAffected() == 0 { // the same provider event again: idempotent, nothing changes
		return WebhookResult{Status: http.StatusOK, Duplicate: true}, tx.Commit(ctx)
	}
	if err := s.applyStatus(ctx, tx, deliveryID, channel, hash, next, cb.Status, conflict); err != nil {
		return WebhookResult{}, err
	}
	return WebhookResult{Status: http.StatusOK, Applied: applied}, tx.Commit(ctx)
}

func (s *Service) applyStatus(ctx context.Context, tx pgx.Tx, deliveryID, channel string, hash []byte, next, reported string, conflict bool) error {
	now := s.now()
	if conflict {
		if _, err := tx.Exec(ctx, `UPDATE app.notification_deliveries SET conflicting_evidence = true, version = version + 1 WHERE id = $1`, deliveryID); err != nil {
			return err
		}
	}
	if next == "" {
		return nil
	}
	set := `status = $2, version = version + 1`
	args := []any{deliveryID, next, now}
	switch next {
	case "ACCEPTED":
		set += `, accepted_at = coalesce(accepted_at, $3)`
	case "DELIVERED":
		set += `, accepted_at = coalesce(accepted_at, $3), delivered_at = coalesce(delivered_at, $3)`
	case "READ":
		set += `, accepted_at = coalesce(accepted_at, $3), delivered_at = coalesce(delivered_at, $3), read_at = coalesce(read_at, $3)`
	case "FAILED_PERMANENT":
		set += `, failed_at = $3, failure_code = $4`
		args = append(args, "PROVIDER_"+reported)
	}
	if _, err := tx.Exec(ctx, `UPDATE app.notification_deliveries SET `+set+` WHERE id = $1`, args...); err != nil {
		return err
	}
	if sup := suppressionFor(reported); sup != "" && len(hash) == 32 {
		_, err := tx.Exec(ctx, `INSERT INTO app.notification_suppressions (id, channel, destination_hash, reason, source)
			VALUES ($1, $2, $3, $4, 'PROVIDER') ON CONFLICT (channel, destination_hash) WHERE lifted_at IS NULL DO NOTHING`, ids.New(), channel, hash, sup)
		return err
	}
	return nil
}

// applyPending applies callbacks that arrived while a send was in flight, in the order they occurred.
func (s *Service) applyPending(ctx context.Context, tx pgx.Tx, provider, messageID string) error {
	rows, err := tx.Query(ctx, `SELECT id::text, reported_status FROM app.notification_provider_events
		WHERE provider_code = $1 AND provider_message_id = $2 AND not_applied_reason = 'IN_FLIGHT' ORDER BY occurred_at, received_at`, provider, messageID)
	if err != nil {
		return err
	}
	type ev struct{ id, status string }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ev, error) {
		var e ev
		return e, r.Scan(&e.id, &e.status)
	})
	if err != nil || len(list) == 0 {
		return err
	}
	var deliveryID, status, channel string
	var hash []byte
	if err := tx.QueryRow(ctx, `SELECT id::text, status, channel, destination_hash FROM app.notification_deliveries
		WHERE provider_code = $1 AND provider_message_id = $2 FOR UPDATE`, provider, messageID).Scan(&deliveryID, &status, &channel, &hash); err != nil {
		return err
	}
	for _, e := range list {
		next, reason, conflict := ApplyCallback(status, e.status)
		if reason == NotInFlight {
			continue
		}
		if reason == "" {
			if _, err := tx.Exec(ctx, `UPDATE app.notification_provider_events SET applied = true, not_applied_reason = NULL, applied_at = $2, delivery_id = delivery_id
				WHERE id = $1`, e.id, s.now()); err != nil {
				return err
			}
		}
		if err := s.applyStatus(ctx, tx, deliveryID, channel, hash, next, e.status, conflict); err != nil {
			return err
		}
		if next != "" {
			status = next
		}
	}
	return nil
}
