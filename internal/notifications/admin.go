package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Overview is the communications dashboard: every figure is a query over the engine's own tables, measured now.
type Overview struct {
	GeneratedAt         time.Time         `json:"generated_at"`
	Environment         string            `json:"environment"`
	LiveMessaging       bool              `json:"live_messaging_enabled"`
	MarketingEnabled    bool              `json:"marketing_enabled"`
	ByStatus            map[string]int64  `json:"deliveries_by_status"`
	ByChannel           map[string]int64  `json:"deliveries_by_channel_24h"`
	DeadLettersOpen     int64             `json:"dead_letters_open"`
	RetryBacklog        int64             `json:"retry_backlog"`
	OldestRetryAgeSecs  *int64            `json:"oldest_retry_age_seconds"`
	QueueBacklog        int64             `json:"queue_backlog"`
	OldestQueuedSecs    *int64            `json:"oldest_queued_age_seconds"`
	Attempts1h          int64             `json:"attempts_last_1h"`
	Failures1h          int64             `json:"failed_attempts_last_1h"`
	ErrorRate1h         *float64          `json:"error_rate_last_1h"` // nil: no attempts (not measurable), never 0
	LatencyP50Ms        *int64            `json:"latency_p50_ms_24h"`
	LatencyP95Ms        *int64            `json:"latency_p95_ms_24h"`
	ConsentRestricted   int64             `json:"consent_restricted_24h"`
	WebhookRejections   int64             `json:"webhook_rejections_24h"`
	ConflictingEvidence int64             `json:"conflicting_evidence"`
	Workers             []WorkerHeartbeat `json:"workers"`
	Providers           []ProviderView    `json:"providers"`
	Notice              string            `json:"notice"`
}

// WorkerHeartbeat is one delivery worker's last sweep.
type WorkerHeartbeat struct {
	WorkerID   string    `json:"worker_id"`
	LastSeenAt time.Time `json:"last_seen_at"`
	Status     string    `json:"status"` // HEALTHY (within WorkerStaleAfter) | UNAVAILABLE
}

// WorkerStaleAfter: a worker that has not swept for this long is shown UNAVAILABLE (engineering default).
const WorkerStaleAfter = 2 * time.Minute

// Overview builds the dashboard.
func (s *Service) Overview(ctx context.Context) (Overview, error) {
	now := s.now()
	o := Overview{GeneratedAt: now, Environment: "TEST", LiveMessaging: LiveMessagingEnabled, MarketingEnabled: MarketingEnabled,
		ByStatus: map[string]int64{}, ByChannel: map[string]int64{},
		Notice: "TEST environment. No production messaging provider is selected; simulators send nothing. Acceptance by a provider is not delivery."}
	rows, err := s.Pool.Query(ctx, `SELECT status, count(*) FROM app.notification_deliveries WHERE channel <> 'IN_APP' GROUP BY status`)
	if err != nil {
		return o, err
	}
	for rows.Next() {
		var k string
		var n int64
		if err := rows.Scan(&k, &n); err != nil {
			rows.Close()
			return o, err
		}
		o.ByStatus[k] = n
	}
	rows.Close()
	rows, err = s.Pool.Query(ctx, `SELECT channel, count(*) FROM app.notification_deliveries WHERE created_at > $1 GROUP BY channel`, now.Add(-24*time.Hour))
	if err != nil {
		return o, err
	}
	for rows.Next() {
		var k string
		var n int64
		if err := rows.Scan(&k, &n); err != nil {
			rows.Close()
			return o, err
		}
		o.ByChannel[k] = n
	}
	rows.Close()
	var oldestRetry, oldestQueued *time.Time
	var p50, p95 *float64
	if err := s.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM app.notification_dead_letters WHERE review_status = 'OPEN'),
		(SELECT count(*) FROM app.notification_deliveries WHERE status = 'FAILED_RETRYABLE'),
		(SELECT min(updated_at) FROM app.notification_deliveries WHERE status = 'FAILED_RETRYABLE'),
		(SELECT count(*) FROM app.notification_deliveries WHERE status = 'QUEUED'),
		(SELECT min(created_at) FROM app.notification_deliveries WHERE status = 'QUEUED'),
		(SELECT count(*) FROM app.notification_attempts WHERE finished_at > $1 AND outcome <> 'STATUS_CHECK'),
		(SELECT count(*) FROM app.notification_attempts WHERE finished_at > $1 AND outcome IN ('TEMPORARY_FAILURE','RATE_LIMITED','PERMANENT_FAILURE','TIMEOUT')),
		(SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY latency_ms) FROM app.notification_attempts WHERE finished_at > $2 AND outcome IN ('SUBMITTED','ACCEPTED','DELIVERED')),
		(SELECT percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms) FROM app.notification_attempts WHERE finished_at > $2 AND outcome IN ('SUBMITTED','ACCEPTED','DELIVERED')),
		(SELECT count(*) FROM app.notification_deliveries WHERE created_at > $2 AND suppression_reason LIKE 'CONSENT%'),
		(SELECT count(*) FROM app.notification_webhook_rejections WHERE received_at > $2),
		(SELECT count(*) FROM app.notification_deliveries WHERE conflicting_evidence)`, now.Add(-time.Hour), now.Add(-24*time.Hour)).
		Scan(&o.DeadLettersOpen, &o.RetryBacklog, &oldestRetry, &o.QueueBacklog, &oldestQueued, &o.Attempts1h, &o.Failures1h, &p50, &p95,
			&o.ConsentRestricted, &o.WebhookRejections, &o.ConflictingEvidence); err != nil {
		return o, err
	}
	age := func(t *time.Time) *int64 {
		if t == nil {
			return nil
		}
		v := int64(now.Sub(*t).Seconds())
		return &v
	}
	o.OldestRetryAgeSecs, o.OldestQueuedSecs = age(oldestRetry), age(oldestQueued)
	if o.Attempts1h > 0 {
		r := float64(o.Failures1h) / float64(o.Attempts1h)
		o.ErrorRate1h = &r
	}
	ms := func(f *float64) *int64 {
		if f == nil {
			return nil
		}
		v := int64(*f)
		return &v
	}
	o.LatencyP50Ms, o.LatencyP95Ms = ms(p50), ms(p95)
	if o.Workers, err = s.workers(ctx); err != nil {
		return o, err
	}
	o.Providers, err = s.ProviderViews(ctx)
	return o, err
}

func (s *Service) workers(ctx context.Context) ([]WorkerHeartbeat, error) {
	rows, err := s.Pool.Query(ctx, `SELECT worker_id, last_seen_at FROM app.notification_worker_heartbeats ORDER BY last_seen_at DESC LIMIT 20`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (WorkerHeartbeat, error) {
		var w WorkerHeartbeat
		if err := r.Scan(&w.WorkerID, &w.LastSeenAt); err != nil {
			return w, err
		}
		w.Status = "HEALTHY"
		if s.now().Sub(w.LastSeenAt) > WorkerStaleAfter {
			w.Status = "UNAVAILABLE"
		}
		return w, nil
	})
}

// ProviderView is one provider configuration plus what this process knows of it.
type ProviderView struct {
	Code             string          `json:"provider_code"`
	Channel          string          `json:"channel"`
	DisplayName      string          `json:"display_name"`
	Environment      string          `json:"environment"`
	Readiness        string          `json:"readiness"`
	Simulated        bool            `json:"simulated"`
	Capabilities     json.RawMessage `json:"capabilities"`
	CredentialRef    *string         `json:"credential_ref"`
	KnownLimitations string          `json:"known_limitations"`
	ActiveHere       bool            `json:"active_in_this_process"`
	Attempts24h      int64           `json:"attempts_24h"`
	Failures24h      int64           `json:"failed_attempts_24h"`
	Health           string          `json:"health"` // derived from attempts: UNKNOWN without attempts, never HEALTHY by default
}

// ProviderViews lists providers with derived health (from recorded attempts only).
func (s *Service) ProviderViews(ctx context.Context) ([]ProviderView, error) {
	rows, err := s.Pool.Query(ctx, `SELECT c.provider_code, c.channel, c.display_name, c.environment, c.readiness, c.simulated, c.capabilities, c.credential_ref,
		c.known_limitations,
		(SELECT count(*) FROM app.notification_attempts a WHERE a.provider_code = c.provider_code AND a.finished_at > $1 AND a.outcome <> 'STATUS_CHECK'),
		(SELECT count(*) FROM app.notification_attempts a WHERE a.provider_code = c.provider_code AND a.finished_at > $1
		   AND a.outcome IN ('TEMPORARY_FAILURE','RATE_LIMITED','PERMANENT_FAILURE','TIMEOUT'))
		FROM app.notification_provider_configs c ORDER BY c.channel, c.provider_code`, s.now().Add(-24*time.Hour))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ProviderView, error) {
		var p ProviderView
		if err := r.Scan(&p.Code, &p.Channel, &p.DisplayName, &p.Environment, &p.Readiness, &p.Simulated, &p.Capabilities, &p.CredentialRef,
			&p.KnownLimitations, &p.Attempts24h, &p.Failures24h); err != nil {
			return p, err
		}
		if a := s.Active[p.Channel]; a != nil && a.Code() == p.Code {
			p.ActiveHere = true
		}
		switch {
		case p.Readiness == "NOT_SELECTED" || p.Readiness == "NOT_CONFIGURED":
			p.Health = "NOT_CONFIGURED"
		case p.Attempts24h == 0:
			p.Health = "UNKNOWN"
		case p.Failures24h*10 > p.Attempts24h: // more than 10 % failed (engineering default)
			p.Health = "DEGRADED"
		default:
			p.Health = "HEALTHY"
		}
		return p, nil
	})
}

// DeliveryView is one delivery (masked destination; no message body).
type DeliveryView struct {
	ID                  string     `json:"id"`
	NotificationID      string     `json:"notification_id"`
	TemplateKey         string     `json:"template_key"`
	Category            string     `json:"category"`
	RecipientUserID     string     `json:"recipient_user_id"`
	Synthetic           bool       `json:"test_data"`
	Channel             string     `json:"channel"`
	Status              string     `json:"status"`
	ProviderCode        *string    `json:"provider_code"`
	DestinationMasked   *string    `json:"destination_masked"`
	Decision            string     `json:"decision"`
	SuppressionReason   *string    `json:"suppression_reason"`
	AttemptCount        int        `json:"attempt_count"`
	MaxAttempts         int        `json:"max_attempts"`
	NextAttemptAt       *time.Time `json:"next_attempt_at"`
	ProviderMessageID   *string    `json:"provider_message_id"`
	SubmittedAt         *time.Time `json:"submitted_at"`
	AcceptedAt          *time.Time `json:"accepted_at"`
	DeliveredAt         *time.Time `json:"delivered_at"`
	ReadAt              *time.Time `json:"read_at"`
	FailedAt            *time.Time `json:"failed_at"`
	FailureCode         *string    `json:"failure_code"`
	ConflictingEvidence bool       `json:"conflicting_evidence"`
	Version             int        `json:"version"`
	CreatedAt           time.Time  `json:"created_at"`
}

const deliveryCols = `d.id::text, d.notification_id::text, n.template_key, n.category, n.recipient_user_id::text, n.synthetic, d.channel, d.status,
	d.provider_code, d.destination_masked, d.decision, d.suppression_reason, d.attempt_count, d.max_attempts, d.next_attempt_at, d.provider_message_id,
	d.submitted_at, d.accepted_at, d.delivered_at, d.read_at, d.failed_at, d.failure_code, d.conflicting_evidence, d.version, d.created_at`

func scanDelivery(r pgx.Row) (DeliveryView, error) {
	var d DeliveryView
	err := r.Scan(&d.ID, &d.NotificationID, &d.TemplateKey, &d.Category, &d.RecipientUserID, &d.Synthetic, &d.Channel, &d.Status, &d.ProviderCode,
		&d.DestinationMasked, &d.Decision, &d.SuppressionReason, &d.AttemptCount, &d.MaxAttempts, &d.NextAttemptAt, &d.ProviderMessageID, &d.SubmittedAt,
		&d.AcceptedAt, &d.DeliveredAt, &d.ReadAt, &d.FailedAt, &d.FailureCode, &d.ConflictingEvidence, &d.Version, &d.CreatedAt)
	return d, err
}

// DeliveryFilter filters the delivery list.
type DeliveryFilter struct {
	Status, Channel, TemplateKey, RecipientUserID, NotificationID string
	Limit                                                         int
	Cursor                                                        string
}

// Deliveries lists deliveries, newest first.
func (s *Service) Deliveries(ctx context.Context, f DeliveryFilter) ([]DeliveryView, string, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	for _, v := range []string{f.RecipientUserID, f.NotificationID} {
		if v != "" && !ids.Valid(v) {
			return nil, "", ErrInvalid
		}
	}
	var curAt *time.Time
	var curID *string
	if f.Cursor != "" {
		at, id, ok := strings.Cut(f.Cursor, "|")
		t, err := time.Parse(time.RFC3339Nano, at)
		if !ok || err != nil || !ids.Valid(id) {
			return nil, "", ErrInvalid
		}
		curAt, curID = &t, &id
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+deliveryCols+` FROM app.notification_deliveries d JOIN app.notifications n ON n.id = d.notification_id
		WHERE ($1 = '' OR d.status = $1) AND ($2 = '' OR d.channel = $2) AND ($3 = '' OR n.template_key = $3)
		AND ($4 = '' OR n.recipient_user_id::text = $4) AND ($5 = '' OR d.notification_id::text = $5)
		AND ($6::timestamptz IS NULL OR (d.created_at, d.id) < ($6, $7::uuid)) ORDER BY d.created_at DESC, d.id DESC LIMIT $8`,
		f.Status, f.Channel, f.TemplateKey, f.RecipientUserID, f.NotificationID, curAt, curID, f.Limit+1)
	if err != nil {
		return nil, "", err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (DeliveryView, error) { return scanDelivery(r) })
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(list) > f.Limit {
		list = list[:f.Limit]
		last := list[len(list)-1]
		next = last.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + last.ID
	}
	return list, next, nil
}

// AttemptView and EvidenceView are a delivery's history.
type AttemptView struct {
	AttemptNo         int       `json:"attempt_no"`
	ProviderCode      string    `json:"provider_code"`
	Outcome           string    `json:"outcome"`
	ErrorCode         *string   `json:"error_code"`
	ProviderMessageID *string   `json:"provider_message_id"`
	LatencyMs         int       `json:"latency_ms"`
	StartedAt         time.Time `json:"started_at"`
	FinishedAt        time.Time `json:"finished_at"`
}

type EvidenceView struct {
	ProviderEventID  string     `json:"provider_event_id"`
	ReportedStatus   string     `json:"reported_status"`
	OccurredAt       time.Time  `json:"occurred_at"`
	ReceivedAt       time.Time  `json:"received_at"`
	Applied          bool       `json:"applied"`
	NotAppliedReason *string    `json:"not_applied_reason"`
	AppliedAt        *time.Time `json:"applied_at"`
}

// DeliveryDetail is a delivery with attempts and provider evidence.
type DeliveryDetail struct {
	Delivery        DeliveryView   `json:"delivery"`
	Attempts        []AttemptView  `json:"attempts"`
	Evidence        []EvidenceView `json:"provider_evidence"`
	TemplateVersion int            `json:"template_version_no"`
}

// Delivery returns one delivery's detail.
func (s *Service) Delivery(ctx context.Context, id string) (DeliveryDetail, error) {
	if !ids.Valid(id) {
		return DeliveryDetail{}, ErrNotFound
	}
	d, err := scanDelivery(s.Pool.QueryRow(ctx, `SELECT `+deliveryCols+` FROM app.notification_deliveries d JOIN app.notifications n ON n.id = d.notification_id
		WHERE d.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return DeliveryDetail{}, ErrNotFound
	}
	if err != nil {
		return DeliveryDetail{}, err
	}
	out := DeliveryDetail{Delivery: d}
	if err := s.Pool.QueryRow(ctx, `SELECT v.version_no FROM app.notifications n JOIN app.notification_template_versions v ON v.id = n.template_version_id
		WHERE n.id = $1`, d.NotificationID).Scan(&out.TemplateVersion); err != nil {
		return out, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT attempt_no, provider_code, outcome, error_code, provider_message_id, latency_ms, started_at, finished_at
		FROM app.notification_attempts WHERE delivery_id = $1 ORDER BY attempt_no`, id)
	if err != nil {
		return out, err
	}
	if out.Attempts, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (AttemptView, error) {
		var a AttemptView
		err := r.Scan(&a.AttemptNo, &a.ProviderCode, &a.Outcome, &a.ErrorCode, &a.ProviderMessageID, &a.LatencyMs, &a.StartedAt, &a.FinishedAt)
		return a, err
	}); err != nil {
		return out, err
	}
	rows, err = s.Pool.Query(ctx, `SELECT provider_event_id, reported_status, occurred_at, received_at, applied, not_applied_reason, applied_at
		FROM app.notification_provider_events WHERE delivery_id = $1 OR (provider_code = $2 AND provider_message_id = $3) ORDER BY received_at`,
		id, nullStr(strOr(d.ProviderCode)), nullStr(strOr(d.ProviderMessageID)))
	if err != nil {
		return out, err
	}
	out.Evidence, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (EvidenceView, error) {
		var e EvidenceView
		err := r.Scan(&e.ProviderEventID, &e.ReportedStatus, &e.OccurredAt, &e.ReceivedAt, &e.Applied, &e.NotAppliedReason, &e.AppliedAt)
		return e, err
	})
	return out, err
}

func strOr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// ---- dead letters -------------------------------------------------------------------------------------------------

// DeadLetter is one dead-lettered delivery.
type DeadLetter struct {
	ID              string       `json:"id"`
	DeliveryID      string       `json:"delivery_id"`
	FailureCategory string       `json:"failure_category"`
	AttemptCount    int          `json:"attempt_count"`
	LastError       *string      `json:"last_error"`
	NextAction      string       `json:"next_action"`
	ReviewStatus    string       `json:"review_status"`
	ReviewedBy      *string      `json:"reviewed_by"`
	ReviewedAt      *time.Time   `json:"reviewed_at"`
	ReviewReason    *string      `json:"review_reason"`
	CreatedAt       time.Time    `json:"created_at"`
	Delivery        DeliveryView `json:"delivery"`
}

// DeadLetters lists dead letters (status OPEN by default).
func (s *Service) DeadLetters(ctx context.Context, status string) ([]DeadLetter, error) {
	if status == "" {
		status = "OPEN"
	}
	rows, err := s.Pool.Query(ctx, `SELECT x.id::text, x.delivery_id::text, x.failure_category, x.attempt_count, x.last_error, x.next_action, x.review_status,
		x.reviewed_by::text, x.reviewed_at, x.review_reason, x.created_at, `+deliveryCols+`
		FROM app.notification_dead_letters x JOIN app.notification_deliveries d ON d.id = x.delivery_id JOIN app.notifications n ON n.id = d.notification_id
		WHERE ($1 = 'ALL' OR x.review_status = $1) ORDER BY x.created_at DESC LIMIT 200`, status)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (DeadLetter, error) {
		var x DeadLetter
		d := &x.Delivery
		err := r.Scan(&x.ID, &x.DeliveryID, &x.FailureCategory, &x.AttemptCount, &x.LastError, &x.NextAction, &x.ReviewStatus, &x.ReviewedBy, &x.ReviewedAt,
			&x.ReviewReason, &x.CreatedAt, &d.ID, &d.NotificationID, &d.TemplateKey, &d.Category, &d.RecipientUserID, &d.Synthetic, &d.Channel, &d.Status,
			&d.ProviderCode, &d.DestinationMasked, &d.Decision, &d.SuppressionReason, &d.AttemptCount, &d.MaxAttempts, &d.NextAttemptAt,
			&d.ProviderMessageID, &d.SubmittedAt, &d.AcceptedAt, &d.DeliveredAt, &d.ReadAt, &d.FailedAt, &d.FailureCode, &d.ConflictingEvidence,
			&d.Version, &d.CreatedAt)
		return x, err
	})
}

// ReplayResult explains a replay decision.
type ReplayResult struct {
	Replayed bool   `json:"replayed"`
	Reason   string `json:"blocked_reason,omitempty"`
	Status   string `json:"delivery_status"`
}

// Replay re-queues a dead-lettered delivery after re-checking everything a send would: the delivery's current state
// (an UNKNOWN outcome is looked up first), the notification's source, the recipient's eligibility, consent,
// preferences and suppressions. Concurrent replays of one dead letter: exactly one wins (row lock + OPEN check).
func (s *Service) Replay(ctx context.Context, staffID, deadLetterID, reason string) (ReplayResult, error) {
	reason = strings.TrimSpace(reason)
	if len([]rune(reason)) < 5 || len([]rune(reason)) > 2000 || !ids.Valid(deadLetterID) {
		return ReplayResult{}, ErrInvalid
	}
	var out ReplayResult
	// an UNKNOWN outcome is looked up first, outside any transaction (an outbound call never runs inside one)
	providerHas := false
	{
		var deliveryID, status string
		var provider *string
		var attempts int
		err := s.Pool.QueryRow(ctx, `SELECT d.id::text, d.status, d.provider_code, d.attempt_count FROM app.notification_dead_letters x
			JOIN app.notification_deliveries d ON d.id = x.delivery_id WHERE x.id = $1`, deadLetterID).Scan(&deliveryID, &status, &provider, &attempts)
		if errors.Is(err, pgx.ErrNoRows) {
			return ReplayResult{}, ErrNotFound
		}
		if err != nil {
			return ReplayResult{}, err
		}
		if status == "UNKNOWN" && provider != nil {
			if p := s.Providers[*provider]; p != nil {
				if res, err := p.Status(ctx, idemKey(deliveryID, attempts)); err == nil && res.Found {
					providerHas = true
				}
			}
		}
	}
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var deliveryID, review, status, channel, notificationID string
		err := tx.QueryRow(ctx, `SELECT x.delivery_id::text, x.review_status, d.status, d.channel, d.notification_id::text
			FROM app.notification_dead_letters x JOIN app.notification_deliveries d ON d.id = x.delivery_id
			WHERE x.id = $1 FOR UPDATE OF x, d`, deadLetterID).Scan(&deliveryID, &review, &status, &channel, &notificationID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if review != "OPEN" {
			return ErrState
		}
		out.Status = status
		if status != "FAILED_PERMANENT" && status != "UNKNOWN" {
			out.Reason = "DELIVERY_NOT_FAILED"
			return nil
		}
		if status == "UNKNOWN" && providerHas {
			out.Reason = "PROVIDER_HAS_MESSAGE" // the outcome is being resolved, not resent (the sweep settles it)
			return nil
		}
		p, err := s.loadPending(ctx, tx, notificationID)
		if err != nil {
			return err
		}
		var sourceOK bool
		if err := tx.QueryRow(ctx, `SELECT n.event_id IS NULL OR EXISTS (SELECT 1 FROM app.notification_events e WHERE e.id = n.event_id)
			FROM app.notifications n WHERE n.id = $1`, notificationID).Scan(&sourceOK); err != nil {
			return err
		}
		if !sourceOK {
			out.Reason = "SOURCE_EVENT_MISSING"
			return nil
		}
		ct, err := loadContact(ctx, tx, p.UserID)
		if err != nil {
			out.Reason = "RECIPIENT_MISSING"
			return nil
		}
		r, _, err := s.routeFor(ctx, tx, p.UserID, p.TemplateKey, p.Category, channel, p.Kind == "STAFF", p.Synthetic, ct, p.Content, p.WhatsAppStatus)
		if err != nil {
			return err
		}
		if r != "" {
			out.Reason = r
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE app.notification_deliveries SET status = 'QUEUED', next_attempt_at = $2, max_attempts = least(20, attempt_count + 3),
			failure_code = NULL, version = version + 1 WHERE id = $1`, deliveryID, s.now()); err != nil {
			return mapPg(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE app.notification_dead_letters SET review_status = 'REPLAYED', reviewed_by = $2, reviewed_at = $3, review_reason = $4,
			replay_count = replay_count + 1 WHERE id = $1`, deadLetterID, staffID, s.now(), reason); err != nil {
			return err
		}
		out.Replayed, out.Status = true, "QUEUED"
		return audit.Record(ctx, tx, audit.Event{Action: "notifications.dead_letter.replayed", ActorType: "staff", ActorID: staffID,
			TargetType: "notification_delivery", TargetID: deliveryID, Reason: reason})
	})
	if err != nil {
		return ReplayResult{}, err
	}
	if !out.Replayed && out.Reason == "" {
		out.Reason = "BLOCKED"
	}
	return out, nil
}

// Discard closes a dead letter without resending.
func (s *Service) Discard(ctx context.Context, staffID, deadLetterID, reason string) error {
	reason = strings.TrimSpace(reason)
	if len([]rune(reason)) < 5 || len([]rune(reason)) > 2000 || !ids.Valid(deadLetterID) {
		return ErrInvalid
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var deliveryID string
		tag, err := tx.Exec(ctx, `UPDATE app.notification_dead_letters SET review_status = 'DISCARDED', reviewed_by = $2, reviewed_at = $3, review_reason = $4
			WHERE id = $1 AND review_status = 'OPEN'`, deadLetterID, staffID, s.now(), reason)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrState
		}
		if err := tx.QueryRow(ctx, `SELECT delivery_id::text FROM app.notification_dead_letters WHERE id = $1`, deadLetterID).Scan(&deliveryID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "notifications.dead_letter.discarded", ActorType: "staff", ActorID: staffID,
			TargetType: "notification_delivery", TargetID: deliveryID, Reason: reason})
	})
}

// ---- suppressions ---------------------------------------------------------------------------------------------------

// Suppression is one destination suppression (the destination itself is stored only as a hash).
type Suppression struct {
	ID         string     `json:"id"`
	Channel    string     `json:"channel"`
	UserID     *string    `json:"user_id"`
	Reason     string     `json:"reason"`
	Source     string     `json:"source"`
	CreatedAt  time.Time  `json:"created_at"`
	LiftedAt   *time.Time `json:"lifted_at"`
	LiftReason *string    `json:"lift_reason"`
}

// Suppressions lists active (or all) suppressions.
func (s *Service) Suppressions(ctx context.Context, includeLifted bool) ([]Suppression, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id::text, channel, user_id::text, reason, source, created_at, lifted_at, lift_reason FROM app.notification_suppressions
		WHERE $1 OR lifted_at IS NULL ORDER BY created_at DESC LIMIT 200`, includeLifted)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Suppression, error) {
		var x Suppression
		return x, r.Scan(&x.ID, &x.Channel, &x.UserID, &x.Reason, &x.Source, &x.CreatedAt, &x.LiftedAt, &x.LiftReason)
	})
}

// SuppressUser suppresses a user's current destination on a channel (opt-out recorded by support, security or legal
// restriction). The destination is read from the account, never typed in.
func (s *Service) SuppressUser(ctx context.Context, staffID, userID, channel, reason string) error {
	if !ids.Valid(userID) || !contains(consentChannels, channel) || !contains([]string{"USER_OPT_OUT", "SECURITY_RESTRICTION", "LEGAL_RESTRICTION", "INVALID_DESTINATION"}, reason) {
		return ErrInvalid
	}
	ct, err := loadContact(ctx, s.Pool, userID)
	if err != nil {
		return ErrNotFound
	}
	to, _ := ct.destination(channel)
	if to == "" {
		return ErrState
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := addSuppression(ctx, tx, channel, to, userID, reason, "STAFF", staffID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "notifications.suppression.added", ActorType: "staff", ActorID: staffID, TargetType: "user",
			TargetID: userID, Metadata: map[string]any{"channel": channel, "reason": reason}})
	})
}

// LiftSuppression lifts one suppression (reason required). A suppression is never deleted.
func (s *Service) LiftSuppression(ctx context.Context, staffID, id, reason string) error {
	reason = strings.TrimSpace(reason)
	if !ids.Valid(id) || len([]rune(reason)) < 5 || len([]rune(reason)) > 2000 {
		return ErrInvalid
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE app.notification_suppressions SET lifted_at = $2, lifted_by = $3, lift_reason = $4 WHERE id = $1 AND lifted_at IS NULL`,
			id, s.now(), staffID, reason)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrState
		}
		return audit.Record(ctx, tx, audit.Event{Action: "notifications.suppression.lifted", ActorType: "staff", ActorID: staffID,
			TargetType: "notification_suppression", TargetID: id, Reason: reason})
	})
}

// ---- consent (staff view) ---------------------------------------------------------------------------------------------

// ConsentRecord is a consent with its history (communications.consent.view).
type ConsentRecord struct {
	ID            string         `json:"id"`
	UserID        string         `json:"user_id"`
	Channel       string         `json:"channel"`
	Purpose       string         `json:"purpose"`
	Status        string         `json:"status"`
	Source        string         `json:"source"`
	PolicyVersion string         `json:"policy_version"`
	EvidenceRef   string         `json:"evidence_ref"`
	GrantedAt     *time.Time     `json:"granted_at"`
	WithdrawnAt   *time.Time     `json:"withdrawn_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	History       []ConsentEvent `json:"history"`
}

type ConsentEvent struct {
	FromStatus    *string   `json:"from_status"`
	ToStatus      string    `json:"to_status"`
	Source        string    `json:"source"`
	PolicyVersion string    `json:"policy_version"`
	EvidenceRef   string    `json:"evidence_ref"`
	OccurredAt    time.Time `json:"occurred_at"`
}

// ConsentSummary counts consent records by channel, purpose and status (no personal data).
type ConsentSummary struct {
	Channel string `json:"channel"`
	Purpose string `json:"purpose"`
	Status  string `json:"status"`
	Count   int64  `json:"count"`
}

// ConsentOverview returns counts, and one user's records with history when userID is given.
func (s *Service) ConsentOverview(ctx context.Context, userID string) ([]ConsentSummary, []ConsentRecord, error) {
	rows, err := s.Pool.Query(ctx, `SELECT channel, purpose, status, count(*) FROM app.communication_consents GROUP BY 1, 2, 3 ORDER BY 1, 2, 3`)
	if err != nil {
		return nil, nil, err
	}
	sum, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ConsentSummary, error) {
		var c ConsentSummary
		return c, r.Scan(&c.Channel, &c.Purpose, &c.Status, &c.Count)
	})
	if err != nil || userID == "" {
		return sum, nil, err
	}
	if !ids.Valid(userID) {
		return nil, nil, ErrInvalid
	}
	rows, err = s.Pool.Query(ctx, `SELECT id::text, user_id::text, channel, purpose, status, source, policy_version, evidence_ref, granted_at, withdrawn_at,
		updated_at FROM app.communication_consents WHERE user_id = $1 ORDER BY channel, purpose`, userID)
	if err != nil {
		return nil, nil, err
	}
	recs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ConsentRecord, error) {
		var c ConsentRecord
		return c, r.Scan(&c.ID, &c.UserID, &c.Channel, &c.Purpose, &c.Status, &c.Source, &c.PolicyVersion, &c.EvidenceRef, &c.GrantedAt, &c.WithdrawnAt, &c.UpdatedAt)
	})
	if err != nil {
		return nil, nil, err
	}
	for i := range recs {
		rows, err := s.Pool.Query(ctx, `SELECT from_status, to_status, source, policy_version, evidence_ref, occurred_at FROM app.communication_consent_events
			WHERE consent_id = $1 ORDER BY occurred_at`, recs[i].ID)
		if err != nil {
			return nil, nil, err
		}
		if recs[i].History, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ConsentEvent, error) {
			var e ConsentEvent
			return e, r.Scan(&e.FromStatus, &e.ToStatus, &e.Source, &e.PolicyVersion, &e.EvidenceRef, &e.OccurredAt)
		}); err != nil {
			return nil, nil, err
		}
	}
	return sum, recs, nil
}

// ---- reports ----------------------------------------------------------------------------------------------------------

// ReportRow is one grouped figure.
type ReportRow struct {
	Group string   `json:"group"`
	Key   string   `json:"key"`
	Count int64    `json:"count"`
	Rate  *float64 `json:"rate,omitempty"`
}

// Report is the communications report for a period.
type Report struct {
	From        time.Time   `json:"from"`
	To          time.Time   `json:"to"`
	Environment string      `json:"environment"`
	Rows        []ReportRow `json:"rows"`
	LatencyP50  *int64      `json:"latency_p50_ms"`
	LatencyP95  *int64      `json:"latency_p95_ms"`
	Limitations []string    `json:"limitations"`
}

// Reports computes channel, category, outcome, provider error and template usage figures for [from, to).
func (s *Service) Reports(ctx context.Context, from, to time.Time) (Report, error) {
	r := Report{From: from, To: to, Environment: "TEST", Rows: []ReportRow{}, Limitations: []string{
		"TEST environment: simulators and development senders only; no production delivery",
		"provider acceptance is not delivery; delivered counts need provider evidence (the development senders give none)",
		"rates are over deliveries created in the period; in-app deliveries are excluded from delivery rates"}}
	q := func(group, sql string) error {
		rows, err := s.Pool.Query(ctx, sql, from, to)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var row ReportRow
			if err := rows.Scan(&row.Key, &row.Count); err != nil {
				return err
			}
			row.Group = group
			r.Rows = append(r.Rows, row)
		}
		return rows.Err()
	}
	for _, x := range []struct{ group, sql string }{
		{"channel", `SELECT channel, count(*) FROM app.notification_deliveries WHERE created_at >= $1 AND created_at < $2 GROUP BY 1 ORDER BY 1`},
		{"category", `SELECT category, count(*) FROM app.notifications WHERE created_at >= $1 AND created_at < $2 GROUP BY 1 ORDER BY 1`},
		{"status", `SELECT status, count(*) FROM app.notification_deliveries WHERE channel <> 'IN_APP' AND created_at >= $1 AND created_at < $2 GROUP BY 1 ORDER BY 1`},
		{"suppression_reason", `SELECT suppression_reason, count(*) FROM app.notification_deliveries WHERE suppression_reason IS NOT NULL AND created_at >= $1 AND created_at < $2 GROUP BY 1 ORDER BY 1`},
		{"provider_error", `SELECT provider_code || ':' || coalesce(error_code, outcome), count(*) FROM app.notification_attempts WHERE finished_at >= $1 AND finished_at < $2
			AND outcome IN ('TEMPORARY_FAILURE','RATE_LIMITED','PERMANENT_FAILURE','TIMEOUT') GROUP BY 1 ORDER BY 2 DESC`},
		{"template_usage", `SELECT template_key, count(*) FROM app.notifications WHERE created_at >= $1 AND created_at < $2 GROUP BY 1 ORDER BY 2 DESC LIMIT 50`},
	} {
		if err := q(x.group, x.sql); err != nil {
			return r, err
		}
	}
	var total, delivered, failed, retried, suppressed int64
	if err := s.Pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE status IN ('DELIVERED','READ')), count(*) FILTER (WHERE status = 'FAILED_PERMANENT'),
		count(*) FILTER (WHERE attempt_count > 1), count(*) FILTER (WHERE status = 'SUPPRESSED')
		FROM app.notification_deliveries WHERE channel <> 'IN_APP' AND created_at >= $1 AND created_at < $2`, from, to).Scan(&total, &delivered, &failed, &retried, &suppressed); err != nil {
		return r, err
	}
	rate := func(n int64) *float64 {
		if total == 0 {
			return nil // nothing to measure: no rate (never 0 %)
		}
		v := float64(n) / float64(total)
		return &v
	}
	r.Rows = append(r.Rows, ReportRow{Group: "rate", Key: "delivery_success", Count: delivered, Rate: rate(delivered)},
		ReportRow{Group: "rate", Key: "failure", Count: failed, Rate: rate(failed)}, ReportRow{Group: "rate", Key: "retry", Count: retried, Rate: rate(retried)},
		ReportRow{Group: "rate", Key: "suppression", Count: suppressed, Rate: rate(suppressed)})
	var p50, p95 *float64
	if err := s.Pool.QueryRow(ctx, `SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY latency_ms), percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms)
		FROM app.notification_attempts WHERE finished_at >= $1 AND finished_at < $2 AND outcome IN ('SUBMITTED','ACCEPTED','DELIVERED')`, from, to).Scan(&p50, &p95); err != nil {
		return r, err
	}
	if p50 != nil {
		a, b := int64(*p50), int64(*p95)
		r.LatencyP50, r.LatencyP95 = &a, &b
	}
	return r, nil
}
