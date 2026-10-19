package notifications

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/platform/opsview"
)

// Thresholds of the operations view (engineering defaults, docs/stage-15/retries.md; not service commitments).
const (
	QueueBacklogAfter   = 5 * time.Minute
	ErrorRateDegraded   = 0.10
	DeadLetterAlertOpen = 10
)

// OpsSnapshot is the notification engine's contribution to the operations portal (ADR-046 / ADR-047): queue and retry
// backlogs, unknown outcomes, dead letters, worker heartbeat, measured delivery error rate, webhook rejections,
// conflicting evidence, consent enforcement, the live-messaging gate, template approvals. Read-only, own tables only.
func (s *Service) OpsSnapshot(ctx context.Context) (opsview.Snapshot, error) {
	const m = "notifications"
	const view = "communications.view"
	now := s.now()
	snap := opsview.Snapshot{Module: m, Metrics: []opsview.Metric{}, Approvals: []opsview.Approval{}, Exceptions: []opsview.Exception{}}
	var queued, retry, unknown, dead, attempts, failures, rejections, conflicts, violations int64
	var oldestQueued, lastBeat *time.Time
	if err := s.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM app.notification_deliveries WHERE status = 'QUEUED'),
		(SELECT min(created_at) FROM app.notification_deliveries WHERE status = 'QUEUED'),
		(SELECT count(*) FROM app.notification_deliveries WHERE status = 'FAILED_RETRYABLE'),
		(SELECT count(*) FROM app.notification_deliveries WHERE status = 'UNKNOWN'),
		(SELECT count(*) FROM app.notification_dead_letters WHERE review_status = 'OPEN'),
		(SELECT count(*) FROM app.notification_attempts WHERE finished_at > $1 AND outcome <> 'STATUS_CHECK'),
		(SELECT count(*) FROM app.notification_attempts WHERE finished_at > $1 AND outcome IN ('TEMPORARY_FAILURE','RATE_LIMITED','PERMANENT_FAILURE','TIMEOUT')),
		(SELECT count(*) FROM app.notification_webhook_rejections WHERE received_at > $2),
		(SELECT count(*) FROM app.notification_deliveries WHERE conflicting_evidence),
		(SELECT max(last_seen_at) FROM app.notification_worker_heartbeats),
		-- consent enforcement check: an external delivery that reached a provider although the consent it needed is not
		-- GRANTED — SMS/WhatsApp service messages (index on status, channel) and marketing on any channel (partial index)
		(SELECT count(*) FROM app.notification_deliveries d
		  WHERE d.status IN ('SUBMITTED','ACCEPTED','DELIVERED','READ') AND d.channel IN ('SMS','WHATSAPP') AND d.created_at > $2
		  -- a scalar subquery (not flattened into a join): one primary-key lookup per sent SMS/WhatsApp delivery
		  AND (SELECT n.category <> 'MARKETING' AND NOT EXISTS (SELECT 1 FROM app.communication_consents c WHERE c.user_id = n.recipient_user_id
		    AND c.channel = d.channel AND c.purpose = 'SERVICE_MESSAGES' AND c.status = 'GRANTED') FROM app.notifications n WHERE n.id = d.notification_id))
		+ (SELECT count(*) FROM app.notifications n JOIN app.notification_deliveries d ON d.notification_id = n.id
		  WHERE n.category = 'MARKETING' AND n.created_at > $2 AND d.status IN ('SUBMITTED','ACCEPTED','DELIVERED','READ')
		  AND NOT EXISTS (SELECT 1 FROM app.communication_consents c WHERE c.user_id = n.recipient_user_id AND c.channel = d.channel
		    AND c.purpose = 'MARKETING' AND c.status = 'GRANTED'))`,
		now.Add(-time.Hour), now.Add(-24*time.Hour)).
		Scan(&queued, &oldestQueued, &retry, &unknown, &dead, &attempts, &failures, &rejections, &conflicts, &lastBeat, &violations); err != nil {
		return snap, err
	}
	q := opsview.Count(m, "notifications.queue.backlog", "Notifications waiting to be sent", queued, view, "app.notification_deliveries QUEUED",
		"/admin/communications/deliveries?status=QUEUED")
	q.Status = opsview.Healthy
	if oldestQueued != nil && now.Sub(*oldestQueued) > QueueBacklogAfter {
		q.Status = opsview.Degraded
	}
	worker := opsview.Metric{Key: "notifications.worker", Label: "Notification delivery worker", Module: m, Nature: opsview.Real, Permission: view,
		Source: "app.notification_worker_heartbeats (last sweep)", Href: "/admin/communications"}
	switch {
	case lastBeat == nil:
		worker.Status = opsview.StatusUnknown
	case now.Sub(*lastBeat) > WorkerStaleAfter:
		worker.Status = opsview.Unavailable
	default:
		worker.Status = opsview.Healthy
	}
	errRate := opsview.Metric{Key: "notifications.error_rate", Label: "Failed delivery attempts in the last hour", Module: m, Nature: opsview.Real,
		Permission: view, Source: "app.notification_attempts in the last hour (failures / attempts)", Href: "/admin/communications/reports"}
	if attempts == 0 {
		errRate.Status = opsview.StatusUnknown // nothing was attempted: no rate, never 0 %
	} else {
		errRate.Value = &failures
		errRate.Status = opsview.Healthy
		if float64(failures)/float64(attempts) > ErrorRateDegraded {
			errRate.Status = opsview.Degraded
		}
	}
	dl := opsview.Count(m, "notifications.dead_letters.open", "Dead-lettered notifications awaiting review", dead, view,
		"app.notification_dead_letters OPEN", "/admin/communications/dead-letters")
	consent := opsview.Count(m, "notifications.consent_violations", "External messages sent without the consent they need (should be 0)", violations, view,
		"app.notification_deliveries sent on SMS / WhatsApp / marketing without a GRANTED consent record (24 h)", "/admin/communications/consent")
	consent.Status = opsview.Healthy
	if violations > 0 {
		consent.Status = opsview.Degraded
	}
	snap.Metrics = append(snap.Metrics, q, worker, errRate, dl, consent,
		opsview.Count(m, "notifications.retry.backlog", "Notifications waiting to retry", retry, view, "app.notification_deliveries FAILED_RETRYABLE",
			"/admin/communications/deliveries?status=FAILED_RETRYABLE"),
		opsview.Count(m, "notifications.unknown", "Notifications with an unknown provider outcome", unknown, view, "app.notification_deliveries UNKNOWN",
			"/admin/communications/deliveries?status=UNKNOWN"),
		opsview.Count(m, "notifications.webhook_rejections", "Refused provider callbacks (24 h)", rejections, view, "app.notification_webhook_rejections", "/admin/communications/providers"),
		opsview.Count(m, "notifications.conflicting_evidence", "Deliveries with contradictory provider reports", conflicts, view,
			"app.notification_deliveries conflicting_evidence", "/admin/communications/deliveries"),
		opsview.Metric{Key: "notifications.live_messaging", Label: "Live e-mail, SMS and WhatsApp delivery", Module: m, Nature: opsview.Blocked,
			Status: opsview.StatusBlocked, Permission: view, Source: "constant notifications.LiveMessagingEnabled = false; no provider selected",
			Href: "/admin/communications/providers"})
	// approvals: template versions waiting for a checker (approval or activation)
	rows, err := s.Pool.Query(ctx, `SELECT v.id::text, t.id::text, t.template_key, v.status, coalesce(v.submitted_by::text, v.created_by::text, ''),
		coalesce(v.submitted_at, v.created_at), ARRAY[coalesce(v.created_by::text, ''), coalesce(v.submitted_by::text, '')]
		FROM app.notification_template_versions v JOIN app.notification_templates t ON t.id = v.template_id WHERE v.status IN ('PENDING_APPROVAL','APPROVED')`)
	if err != nil {
		return snap, err
	}
	approvals, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (opsview.Approval, error) {
		var a opsview.Approval
		var tid, key, status string
		err := r.Scan(&a.SourceRecordID, &tid, &key, &status, &a.RequestedBy, &a.RequestedAt, &a.Makers)
		a.ID, a.SourceModule, a.Status, a.RequiredPermission = m+":template_version:"+a.SourceRecordID, m, status, "communications.template.approve"
		a.Action = "TEMPLATE_VERSION_APPROVAL"
		if status == "APPROVED" {
			a.Action = "TEMPLATE_VERSION_ACTIVATION"
		}
		a.Summary, a.Href = "Notification template "+key, "/admin/communications/templates/"+tid
		return a, err
	})
	if err != nil {
		return snap, err
	}
	snap.Approvals = append(snap.Approvals, approvals...)
	rows, err = s.Pool.Query(ctx, `SELECT x.id::text, x.failure_category, x.created_at, d.status FROM app.notification_dead_letters x
		JOIN app.notification_deliveries d ON d.id = x.delivery_id WHERE x.review_status = 'OPEN' ORDER BY x.created_at LIMIT 100`)
	if err != nil {
		return snap, err
	}
	exc, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (opsview.Exception, error) {
		var e opsview.Exception
		err := r.Scan(&e.SourceRef, &e.Category, &e.CreatedAt, &e.DomainStatus)
		e.ID, e.SourceModule, e.Severity, e.Permission = m+":dead_letter:"+e.SourceRef, m, "SEV3", view
		e.Summary, e.Href = "Notification delivery dead-lettered ("+e.Category+")", "/admin/communications/dead-letters"
		return e, err
	})
	if err != nil {
		return snap, err
	}
	snap.Exceptions = append(snap.Exceptions, exc...)
	return snap, nil
}
