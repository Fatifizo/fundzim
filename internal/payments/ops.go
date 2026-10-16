package payments

import (
	"context"
	"strconv"

	"github.com/Fatifizo/fundzim/internal/platform/opsview"
)

// OpsSnapshot is the payments module's contribution to the operations portal (Stage 14, ADR-046): UNKNOWN outcomes,
// open reconciliation references and refund requests awaiting a checker, from its own tables. Every payment is a TEST
// payment through the sandbox simulator; live execution is BLOCKED. Read-only.
func (s *Service) OpsSnapshot(ctx context.Context) (opsview.Snapshot, error) {
	const m = "payments"
	snap := opsview.Snapshot{Module: m, Metrics: []opsview.Metric{}, Approvals: []opsview.Approval{}, Exceptions: []opsview.Exception{}}
	var unknown, recon, refunds int64
	if err := s.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM app.payment_intents WHERE status = 'UNKNOWN'),
		(SELECT count(*) FROM app.payment_reconciliation_references WHERE status IN ('OPEN','AWAITING_SETTLEMENT_DATA')),
		(SELECT count(*) FROM app.refund_requests WHERE status = 'PENDING_APPROVAL')`).Scan(&unknown, &recon, &refunds); err != nil {
		return snap, err
	}
	const view = "payment.view"
	tm := func(key, label string, v int64, src, href string) opsview.Metric {
		x := opsview.Count(m, key, label, v, view, src, href)
		x.Nature = opsview.TestOnly
		return x
	}
	snap.Metrics = append(snap.Metrics,
		tm("payments.unknown", "Payments with an UNKNOWN outcome (TEST)", unknown, "app.payment_intents UNKNOWN (never FAILED by assumption)", "/admin/payments/unknown"),
		tm("payments.reconciliation.open", "Open payment reconciliation references (TEST)", recon, "app.payment_reconciliation_references OPEN/AWAITING_SETTLEMENT_DATA",
			"/admin/payments/reconciliation"),
		tm("payments.refunds.pending_approval", "Refund requests awaiting a checker (cannot execute)", refunds, "app.refund_requests PENDING_APPROVAL",
			"/admin/payments/refunds"),
		opsview.Metric{Key: "payments.live_execution", Label: "Live donations", Module: m, Nature: opsview.Blocked, Status: opsview.StatusBlocked,
			Source: "psp.LiveExecutionEnabled = false; no provider selected (ADR-040)", Permission: view},
	)
	rows, err := s.Pool.Query(ctx, `SELECT id::text, requested_by::text, created_at, status, currency, amount_minor FROM app.refund_requests
		WHERE status = 'PENDING_APPROVAL' ORDER BY created_at LIMIT 200`)
	if err != nil {
		return snap, err
	}
	for rows.Next() {
		var a opsview.Approval
		var ccy string
		var amount int64
		if err := rows.Scan(&a.SourceRecordID, &a.RequestedBy, &a.RequestedAt, &a.Status, &ccy, &amount); err != nil {
			rows.Close()
			return snap, err
		}
		a.ID, a.SourceModule, a.Action, a.RequiredPermission = m+":refund:"+a.SourceRecordID, m, "REFUND_APPROVAL", "refund.approve"
		a.Summary = "Refund request " + ccy + " " + strconv.FormatInt(amount, 10) + " minor units (TEST; cannot execute)"
		a.Makers, a.Href = []string{a.RequestedBy}, "/admin/payments/refunds"
		snap.Approvals = append(snap.Approvals, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return snap, err
	}
	rows, err = s.Pool.Query(ctx, `SELECT 'unknown:' || id, id::text, 'SEV2', 'UNKNOWN_OUTCOME', 'Payment outcome UNKNOWN (TEST)', coalesce(unknown_since, updated_at), status,
		coalesce(correlation_id, ''), '/admin/payments/unknown'
		FROM app.payment_intents WHERE status = 'UNKNOWN'
		UNION ALL
		SELECT 'recon:' || id, id::text, 'SEV3', reason, 'Reconciliation reference ' || reason, created_at, status, '', '/admin/payments/reconciliation'
		FROM app.payment_reconciliation_references WHERE status IN ('OPEN','AWAITING_SETTLEMENT_DATA')
		ORDER BY 6 DESC LIMIT 200`)
	if err != nil {
		return snap, err
	}
	defer rows.Close()
	for rows.Next() {
		var e opsview.Exception
		if err := rows.Scan(&e.ID, &e.SourceRef, &e.Severity, &e.Category, &e.Summary, &e.CreatedAt, &e.DomainStatus, &e.CorrelationID, &e.Href); err != nil {
			return snap, err
		}
		e.ID, e.SourceModule, e.Permission = m+":"+e.ID, m, view
		snap.Exceptions = append(snap.Exceptions, e)
	}
	return snap, rows.Err()
}

// OpsSearch finds payments by id or donation intent id (staff with payment.view; no payer data).
func (s *Service) OpsSearch(ctx context.Context, q string) ([]opsview.SearchHit, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id::text, status FROM app.payment_intents WHERE id::text = $1 OR donation_intent_id::text = $1 LIMIT 20`, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []opsview.SearchHit{}
	for rows.Next() {
		var h opsview.SearchHit
		if err := rows.Scan(&h.ID, &h.Status); err != nil {
			return nil, err
		}
		h.Kind, h.Label, h.Permission, h.Href = "PAYMENT", "TEST payment "+h.ID[:8], "payment.view", "/admin/payments/attempts/"+h.ID
		out = append(out, h)
	}
	return out, rows.Err()
}
