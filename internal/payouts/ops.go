package payouts

import (
	"context"

	"github.com/Fatifizo/fundzim/internal/platform/opsview"
)

// OpsSnapshot is the payouts module's contribution to the operations portal (Stage 14, ADR-046): TEST withdrawal
// backlog, approvals waiting, UNKNOWN outcomes, destination verification backlog and open payout exceptions, from its
// own tables. Read-only: decisions stay on the payout approval routes (FINANCE maker-checker, step-up).
func (s *Service) OpsSnapshot(ctx context.Context) (opsview.Snapshot, error) {
	const m = "payouts"
	snap := opsview.Snapshot{Module: m, Metrics: []opsview.Metric{}, Approvals: []opsview.Approval{}, Exceptions: []opsview.Exception{}}
	var review, approved, inflight, unknown, exc, dests int64
	if err := s.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM app.payout_requests WHERE status = 'PENDING_REVIEW'),
		(SELECT count(*) FROM app.payout_requests WHERE status = 'APPROVED'),
		(SELECT count(*) FROM app.payout_requests WHERE status IN ('SUBMITTED','PROCESSING')),
		(SELECT count(*) FROM app.payout_requests WHERE status = 'UNKNOWN'),
		(SELECT count(*) FROM app.payout_exceptions WHERE status IN ('OPEN','INVESTIGATING')),
		(SELECT count(*) FROM app.payout_destinations WHERE status = 'PENDING_VERIFICATION')`).
		Scan(&review, &approved, &inflight, &unknown, &exc, &dests); err != nil {
		return snap, err
	}
	const view = "payout.view"
	tm := func(key, label string, v int64, src, href string) opsview.Metric {
		x := opsview.Count(m, key, label, v, view, src, href)
		x.Nature = opsview.TestOnly // every payout is a TEST payout through the simulator (Stage 11)
		return x
	}
	snap.Metrics = append(snap.Metrics,
		tm("payouts.awaiting_approval", "Withdrawals awaiting approval (TEST)", review, "app.payout_requests PENDING_REVIEW", "/admin/finance/payouts/approvals"),
		tm("payouts.approved_not_submitted", "Approved withdrawals not yet submitted (TEST)", approved, "app.payout_requests APPROVED", "/admin/finance/payouts"),
		tm("payouts.in_flight", "Payouts in flight (TEST)", inflight, "app.payout_requests SUBMITTED/PROCESSING", "/admin/finance/payouts"),
		tm("payouts.unknown", "Payouts with an UNKNOWN outcome (TEST)", unknown, "app.payout_requests UNKNOWN (never resubmitted)", "/admin/finance/payouts"),
		tm("payouts.exceptions.open", "Unresolved payout exceptions", exc, "app.payout_exceptions OPEN/INVESTIGATING", "/admin/finance/payouts/exceptions"),
		opsview.Count(m, "payouts.destinations.pending", "Payout destinations awaiting verification", dests, "payout_destination.verify",
			"app.payout_destinations PENDING_VERIFICATION", "/admin/verification/payout-destinations"),
		opsview.Metric{Key: "payouts.live_execution", Label: "Real payout execution", Module: m, Nature: opsview.Blocked, Status: opsview.StatusBlocked,
			Source: "payouts.LiveExecutionEnabled = false; no payout provider confirmed (ADR-040, ADR-043)", Permission: view},
	)
	rows, err := s.Pool.Query(ctx, `SELECT p.id::text, p.requested_by::text, p.updated_at, p.status, p.approval_policy, p.currency, p.amount_minor,
		ARRAY(SELECT a.approver_id::text FROM app.payout_approvals a WHERE a.payout_request_id = p.id AND a.approval_round = p.approval_round)
		FROM app.payout_requests p WHERE p.status = 'PENDING_REVIEW' ORDER BY p.updated_at LIMIT 200`)
	if err != nil {
		return snap, err
	}
	for rows.Next() {
		var a opsview.Approval
		var policy, ccy string
		var amount int64
		var approvers []string
		if err := rows.Scan(&a.SourceRecordID, &a.RequestedBy, &a.RequestedAt, &a.Status, &policy, &ccy, &amount, &approvers); err != nil {
			rows.Close()
			return snap, err
		}
		a.ID, a.SourceModule, a.Action, a.RequiredPermission = m+":"+a.SourceRecordID, m, "PAYOUT_APPROVAL", "payout.approve"
		a.Summary = "TEST withdrawal " + ccy + " " + minorString(amount) + " (" + policy + " approval)"
		a.Makers, a.Href = append([]string{a.RequestedBy}, approvers...), "/admin/finance/withdrawals/"+a.SourceRecordID
		snap.Approvals = append(snap.Approvals, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return snap, err
	}
	rows, err = s.Pool.Query(ctx, `SELECT id::text, severity, exception_type, source, status, detected_at, coalesce(payout_request_id::text, '')
		FROM app.payout_exceptions WHERE status IN ('OPEN','INVESTIGATING') ORDER BY detected_at DESC LIMIT 200`)
	if err != nil {
		return snap, err
	}
	defer rows.Close()
	for rows.Next() {
		var e opsview.Exception
		var src, payout string
		if err := rows.Scan(&e.SourceRef, &e.Severity, &e.Category, &src, &e.DomainStatus, &e.CreatedAt, &payout); err != nil {
			return snap, err
		}
		e.ID, e.SourceModule, e.Permission = m+":"+e.SourceRef, m, view
		e.Summary, e.EvidenceRef, e.Href = e.Category+" ("+src+")", payout, "/admin/finance/payouts/exceptions"
		snap.Exceptions = append(snap.Exceptions, e)
	}
	return snap, rows.Err()
}

// OpsSearch finds payout requests by id (staff with payout.view).
func (s *Service) OpsSearch(ctx context.Context, q string) ([]opsview.SearchHit, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id::text, status, currency, amount_minor FROM app.payout_requests WHERE id::text = $1 OR campaign_id::text = $1
		ORDER BY requested_at DESC LIMIT 20`, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []opsview.SearchHit{}
	for rows.Next() {
		var h opsview.SearchHit
		var ccy string
		var amount int64
		if err := rows.Scan(&h.ID, &h.Status, &ccy, &amount); err != nil {
			return nil, err
		}
		h.Kind, h.Label, h.Permission, h.Href = "PAYOUT_REQUEST", "TEST withdrawal "+ccy+" "+minorString(amount), "payout.view", "/admin/finance/withdrawals/"+h.ID
		out = append(out, h)
	}
	return out, rows.Err()
}

// minorString renders integer minor units as a digit string (MONEY.md: amounts travel as strings).
func minorString(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:]) + " minor units"
}
