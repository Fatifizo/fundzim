package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/users"
)

// Backoff is the delay before attempt n+1 after a transient failure: base·2^(n-1), capped, ±20 % jitter. A provider's
// Retry-After wins when it is longer.
func Backoff(st Settings, attempt int, retryAfter time.Duration, jitter func() float64) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := st.BackoffBase
	for i := 1; i < attempt && d < st.BackoffCap; i++ {
		d *= 2
	}
	if d > st.BackoffCap {
		d = st.BackoffCap
	}
	if jitter == nil {
		jitter = rand.Float64
	}
	d = time.Duration(float64(d) * (0.8 + 0.4*jitter()))
	if retryAfter > d {
		d = retryAfter
	}
	return d
}

// SweepResult counts what one sweep did.
type SweepResult struct {
	Claimed, Sent, Retried, Failed, Suppressed, Unknown, Resolved, DeadLettered, Expired int
}

// Sweep is one delivery-worker run: expire stale claims, resolve unknown outcomes, send due deliveries, beat.
func (s *Service) Sweep(ctx context.Context) (SweepResult, error) {
	var r SweepResult
	n, err := s.expireLeases(ctx)
	if err != nil {
		return r, err
	}
	r.Expired = n
	if err := s.resolveUnknown(ctx, &r); err != nil {
		return r, err
	}
	claimed, err := s.claim(ctx)
	if err != nil {
		return r, err
	}
	r.Claimed = len(claimed)
	for _, c := range claimed {
		if err := s.process(ctx, c, &r); err != nil {
			return r, err
		}
	}
	return r, s.beat(ctx, r.Claimed)
}

func (s *Service) beat(ctx context.Context, claimed int) error {
	if s.WorkerID == "" {
		return nil
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO app.notification_worker_heartbeats (worker_id, last_seen_at, last_claimed) VALUES ($1, $2, $3)
		ON CONFLICT (worker_id) DO UPDATE SET last_seen_at = EXCLUDED.last_seen_at, last_claimed = EXCLUDED.last_claimed`, s.WorkerID, s.now(), claimed)
	return err
}

func (s *Service) providerCodes() []string {
	out := []string{}
	for code := range s.Providers {
		out = append(out, code)
	}
	return out
}

// expireLeases: a claim whose lease ran out belongs to a worker that died mid-call — the provider may have the
// message, so the outcome is UNKNOWN (looked up before any resend), never FAILED.
func (s *Service) expireLeases(ctx context.Context) (int, error) {
	tag, err := s.Pool.Exec(ctx, `UPDATE app.notification_deliveries SET status = 'UNKNOWN', lease_until = NULL, failure_code = 'WORKER_LEASE_EXPIRED',
		version = version + 1 WHERE id IN (SELECT id FROM app.notification_deliveries WHERE status = 'PROCESSING' AND lease_until < $1
		AND provider_code = ANY($2) FOR UPDATE SKIP LOCKED)`, s.now(), s.providerCodes())
	return int(tag.RowsAffected()), err
}

type claimed struct {
	ID, NotificationID, Channel, ProviderCode string
	Attempt, MaxAttempts                      int
}

func (s *Service) claim(ctx context.Context) ([]claimed, error) {
	st := s.settings()
	now := s.now()
	rows, err := s.Pool.Query(ctx, `UPDATE app.notification_deliveries d SET status = 'PROCESSING', lease_until = $1, attempt_count = d.attempt_count + 1,
		version = d.version + 1 FROM (SELECT id FROM app.notification_deliveries WHERE status IN ('QUEUED','FAILED_RETRYABLE') AND next_attempt_at <= $2
		AND provider_code = ANY($3) ORDER BY next_attempt_at LIMIT $4 FOR UPDATE SKIP LOCKED) due
		WHERE d.id = due.id RETURNING d.id::text, d.notification_id::text, d.channel, d.provider_code, d.attempt_count, d.max_attempts`,
		now.Add(st.Lease), now, s.providerCodes(), st.Batch)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (claimed, error) {
		var c claimed
		err := r.Scan(&c.ID, &c.NotificationID, &c.Channel, &c.ProviderCode, &c.Attempt, &c.MaxAttempts)
		return c, err
	})
}

type pending struct {
	UserID, Category, TemplateKey, Kind string
	Synthetic                           bool
	Vars                                map[string]string
	Content                             Content
	WhatsAppStatus                      string
}

func (s *Service) loadPending(ctx context.Context, q users.Querier, notificationID string) (pending, error) {
	var p pending
	var raw []byte
	err := q.QueryRow(ctx, `SELECT n.recipient_user_id::text, n.category, n.template_key, n.recipient_kind, n.synthetic, n.variables,
		v.email_subject, v.email_text, v.sms_text, v.whatsapp_template_name, v.whatsapp_language, v.inapp_title, v.inapp_body, v.whatsapp_provider_status
		FROM app.notifications n JOIN app.notification_template_versions v ON v.id = n.template_version_id WHERE n.id = $1`, notificationID).
		Scan(&p.UserID, &p.Category, &p.TemplateKey, &p.Kind, &p.Synthetic, &raw, &p.Content.EmailSubject, &p.Content.EmailText, &p.Content.SMSText,
			&p.Content.WhatsAppName, &p.Content.WhatsAppLanguage, &p.Content.InAppTitle, &p.Content.InAppBody, &p.WhatsAppStatus)
	if err != nil {
		return p, err
	}
	p.Vars = map[string]string{}
	_ = json.Unmarshal(raw, &p.Vars)
	return p, nil
}

// process sends one claimed delivery: re-check the routing (consent, preference, suppression and contact may have
// changed since creation), render from the pinned template version, call the provider outside any transaction, record.
func (s *Service) process(ctx context.Context, c claimed, r *SweepResult) error {
	started := s.now()
	p, err := s.loadPending(ctx, s.Pool, c.NotificationID)
	if err != nil {
		return err
	}
	ct, err := loadContact(ctx, s.Pool, p.UserID)
	if err != nil && !errors.Is(err, users.ErrNotFound) {
		return err
	}
	reason, to, err := s.routeFor(ctx, s.Pool, p.UserID, p.TemplateKey, p.Category, c.Channel, p.Kind == "STAFF", p.Synthetic, ct, p.Content, p.WhatsAppStatus)
	if err != nil {
		// consent or suppression state could not be read: never send on a failed check (retry later)
		r.Retried++
		return s.finish(ctx, c, started, outcome{status: "FAILED_RETRYABLE", attempt: "TEMPORARY_FAILURE", code: "ROUTING_CHECK_FAILED",
			retryAt: s.now().Add(Backoff(s.settings(), c.Attempt, 0, nil))}, r)
	}
	if reason != "" {
		r.Suppressed++
		return s.finish(ctx, c, started, outcome{status: "SUPPRESSED", attempt: "SUPPRESSED", code: reason, suppression: reason}, r)
	}
	if _, ok := s.Active[c.Channel]; !ok || s.Providers[c.ProviderCode] == nil {
		r.Suppressed++
		return s.finish(ctx, c, started, outcome{status: "SUPPRESSED", attempt: "SUPPRESSED", code: SupNoProvider, suppression: SupNoProvider}, r)
	}
	msg, err := Render(p.Content, withLinks(p.Vars, s.PublicURL, ct.DisplayName), c.Channel, p.Synthetic)
	if err != nil {
		r.DeadLettered++
		return s.finish(ctx, c, started, outcome{status: "FAILED_PERMANENT", attempt: "PERMANENT_FAILURE", code: "RENDER_FAILED", dead: "RENDER_FAILURE"}, r)
	}
	prov := s.Providers[c.ProviderCode]
	sctx, cancel := context.WithTimeout(ctx, s.settings().SendTimeout)
	res, sendErr := prov.Send(sctx, Outbound{DeliveryID: c.ID, IdempotencyKey: idemKey(c.ID, c.Attempt), Channel: c.Channel, To: to, Message: msg})
	cancel()
	o := outcome{hash: ContentHash(p.Content), destination: to}
	if sendErr == nil {
		r.Sent++
		o.status, o.attempt, o.messageID = res.Status, res.Status, res.ProviderMessageID
		return s.finish(ctx, c, started, o, r)
	}
	se := classify(sendErr)
	o.code = se.Code
	switch se.Class {
	case ClassTimeout:
		r.Unknown++
		o.status, o.attempt = "UNKNOWN", "TIMEOUT"
	case ClassPermanent:
		r.Failed++
		o.status, o.attempt, o.dead = "FAILED_PERMANENT", "PERMANENT_FAILURE", "PERMANENT_FAILURE"
		if se.InvalidDestination {
			o.suppressDestination = "PROVIDER_REJECTION"
		}
	default: // temporary or rate limited
		o.attempt = "TEMPORARY_FAILURE"
		if se.Class == ClassRateLimited {
			o.attempt = "RATE_LIMITED"
		}
		if c.Attempt >= c.MaxAttempts {
			r.DeadLettered++
			o.status, o.dead = "FAILED_PERMANENT", "RETRIES_EXHAUSTED"
		} else {
			r.Retried++
			o.status = "FAILED_RETRYABLE"
			o.retryAt = s.now().Add(Backoff(s.settings(), c.Attempt, se.RetryAfter, nil))
		}
	}
	return s.finish(ctx, c, started, o, r)
}

func withLinks(vars map[string]string, publicURL, displayName string) map[string]string {
	out := map[string]string{"display_name": displayName}
	for k, v := range vars {
		out[k] = v
	}
	if publicURL != "" {
		out["forgot_password_url"] = publicURL + "/forgot-password"
	}
	return out
}

func idemKey(deliveryID string, attempt int) string { return fmt.Sprintf("%s:%d", deliveryID, attempt) }

type outcome struct {
	status, attempt, code, messageID, suppression, dead, suppressDestination, destination string
	hash                                                                                  []byte
	retryAt                                                                               time.Time
}

// finish records the attempt and the delivery's new state in one transaction (and a dead letter / suppression).
func (s *Service) finish(ctx context.Context, c claimed, started time.Time, o outcome, r *SweepResult) error {
	now := s.now()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var code, msg any
	if o.code != "" {
		code = o.code
	}
	if o.messageID != "" {
		msg = o.messageID
	}
	if _, err := tx.Exec(ctx, `INSERT INTO app.notification_attempts (id, delivery_id, attempt_no, provider_code, started_at, finished_at, outcome, error_code,
		provider_message_id, content_hash, latency_ms) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		ids.New(), c.ID, c.Attempt, c.ProviderCode, started, now, o.attempt, code, msg, o.hash, now.Sub(started).Milliseconds()); err != nil {
		return err
	}
	set := `status = $2, lease_until = NULL, version = version + 1, failure_code = $3`
	args := []any{c.ID, o.status, code}
	switch o.status {
	case "SUBMITTED", "ACCEPTED", "DELIVERED":
		args[2] = nil // a successful send clears any earlier failure code
		set += `, provider_message_id = $4, submitted_at = coalesce(submitted_at, $5)`
		args = append(args, msg, now)
		if o.status != "SUBMITTED" {
			set += `, accepted_at = coalesce(accepted_at, $5)`
		}
		if o.status == "DELIVERED" {
			set += `, delivered_at = coalesce(delivered_at, $5)`
		}
	case "FAILED_RETRYABLE":
		set += `, next_attempt_at = $4`
		args = append(args, o.retryAt)
	case "FAILED_PERMANENT":
		set += `, failed_at = $4`
		args = append(args, now)
	case "SUPPRESSED":
		set += `, suppression_reason = $4, decision = $5`
		args = append(args, o.suppression, "SUPPRESSED_AT_SEND:"+o.suppression)
	}
	if _, err := tx.Exec(ctx, `UPDATE app.notification_deliveries SET `+set+` WHERE id = $1`, args...); err != nil {
		return fmt.Errorf("notifications: record outcome: %w", err)
	}
	if o.dead != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO app.notification_dead_letters (id, delivery_id, failure_category, attempt_count, last_error)
			VALUES ($1, $2, $3, $4, $5) ON CONFLICT DO NOTHING`, ids.New(), c.ID, o.dead, c.Attempt, code); err != nil {
			return err
		}
	}
	if o.suppressDestination != "" && o.destination != "" {
		if err := addSuppression(ctx, tx, c.Channel, o.destination, "", o.suppressDestination, "PROVIDER", ""); err != nil {
			return err
		}
	}
	if o.messageID != "" {
		// callbacks that arrived while the send was in flight are applied now
		if err := s.applyPending(ctx, tx, c.ProviderCode, o.messageID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// addSuppression records an active suppression of a destination (idempotent).
func addSuppression(ctx context.Context, tx pgx.Tx, channel, destination, userID, reason, source, staffID string) error {
	var u, by any
	if ids.Valid(userID) {
		u = userID
	}
	if ids.Valid(staffID) {
		by = staffID
	}
	_, err := tx.Exec(ctx, `INSERT INTO app.notification_suppressions (id, channel, destination_hash, user_id, reason, source, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (channel, destination_hash) WHERE lifted_at IS NULL DO NOTHING`,
		ids.New(), channel, destinationHash(channel, destination), u, reason, source, by)
	return err
}

// resolveUnknown looks UNKNOWN outcomes up before anything is resent.
func (s *Service) resolveUnknown(ctx context.Context, r *SweepResult) error {
	st := s.settings()
	now := s.now()
	rows, err := s.Pool.Query(ctx, `SELECT id::text, provider_code, attempt_count, created_at FROM app.notification_deliveries
		WHERE status = 'UNKNOWN' AND updated_at < $1 AND provider_code = ANY($2) ORDER BY updated_at LIMIT $3`,
		now.Add(-st.UnknownGrace), s.providerCodes(), st.Batch)
	if err != nil {
		return err
	}
	type u struct {
		id, provider string
		attempt      int
		created      time.Time
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (u, error) {
		var x u
		return x, row.Scan(&x.id, &x.provider, &x.attempt, &x.created)
	})
	if err != nil {
		return err
	}
	for _, x := range list {
		prov := s.Providers[x.provider]
		if prov == nil {
			continue
		}
		res, err := prov.Status(ctx, idemKey(x.id, x.attempt))
		next := ""
		var msgID string
		switch {
		case errors.Is(err, ErrStatusUnsupported):
			next = "FAILED_RETRYABLE" // no lookup: retry (a duplicate is possible; documented for the dev senders)
		case err != nil:
			if now.Sub(x.created) > st.UnknownMax {
				next = "DEAD_LETTER"
			} else {
				continue // look again next sweep
			}
		case !res.Found:
			next = "FAILED_RETRYABLE" // the provider never received it: safe to send again
		default:
			next, msgID = res.Status, res.ProviderMessageID
			if next == "FAILED" {
				next = "FAILED_PERMANENT"
			}
		}
		if err := s.settleUnknown(ctx, x.id, next, msgID); err != nil {
			return err
		}
		r.Resolved++
	}
	return nil
}

func (s *Service) settleUnknown(ctx context.Context, id, next, msgID string) error {
	now := s.now()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status, provider string
	var attempts int
	if err := tx.QueryRow(ctx, `SELECT status, provider_code, attempt_count FROM app.notification_deliveries WHERE id = $1 FOR UPDATE`, id).
		Scan(&status, &provider, &attempts); err != nil {
		return err
	}
	if status != "UNKNOWN" {
		return nil // resolved meanwhile (a callback)
	}
	// a status lookup is recorded as an attempt row; it does not use up the retry budget
	if err := tx.QueryRow(ctx, `UPDATE app.notification_deliveries SET attempt_count = attempt_count + 1, max_attempts = least(20, max_attempts + 1)
		WHERE id = $1 RETURNING attempt_count`, id).Scan(&attempts); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO app.notification_attempts (id, delivery_id, attempt_no, provider_code, started_at, finished_at, outcome,
		error_code, provider_message_id, latency_ms) VALUES ($1, $2, $3, $4, $5, $5, 'STATUS_CHECK', $6, $7, 0)`,
		ids.New(), id, attempts, provider, now, nullIf(next == "DEAD_LETTER", "OUTCOME_UNKNOWN"), nullStr(msgID)); err != nil {
		return err
	}
	switch next {
	case "DEAD_LETTER":
		_, err = tx.Exec(ctx, `INSERT INTO app.notification_dead_letters (id, delivery_id, failure_category, attempt_count, last_error)
			VALUES ($1, $2, 'OUTCOME_UNKNOWN', $3, 'OUTCOME_UNKNOWN') ON CONFLICT DO NOTHING`, ids.New(), id, attempts)
	case "FAILED_RETRYABLE":
		_, err = tx.Exec(ctx, `UPDATE app.notification_deliveries SET status = 'FAILED_RETRYABLE', next_attempt_at = $2, version = version + 1 WHERE id = $1`, id, now)
	case "FAILED_PERMANENT":
		_, err = tx.Exec(ctx, `UPDATE app.notification_deliveries SET status = 'FAILED_PERMANENT', failed_at = $2, version = version + 1 WHERE id = $1`, id, now)
	default: // SUBMITTED / ACCEPTED / DELIVERED / READ: the provider has it
		st := next
		if st == "SUBMITTED" {
			st = "ACCEPTED" // UNKNOWN has no edge back to SUBMITTED; the provider holds the message
		}
		_, err = tx.Exec(ctx, `UPDATE app.notification_deliveries SET status = $2, provider_message_id = coalesce(provider_message_id, $3),
			accepted_at = coalesce(accepted_at, $4), failure_code = NULL, version = version + 1 WHERE id = $1`, id, st, nullStr(msgID), now)
		if err == nil && msgID != "" {
			err = s.applyPending(ctx, tx, provider, msgID)
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullIf(cond bool, s string) any {
	if !cond {
		return nil
	}
	return s
}
