package ledger

import (
	"context"

	"github.com/Fatifizo/fundzim/internal/platform/opsview"
)

// OpsSnapshot is the ledger's contribution to the operations portal (Stage 14, ADR-046): adjustments awaiting a
// checker, open reconciliation exceptions and the latest integrity result, from its own tables on the ledger pool.
// The ledger is a TEST ledger; no figure here is business performance. Read-only — corrections stay maker-checker
// adjustments through this module.
func (s *Service) OpsSnapshot(ctx context.Context) (opsview.Snapshot, error) {
	const m = "ledger"
	snap := opsview.Snapshot{Module: m, Metrics: []opsview.Metric{}, Approvals: []opsview.Approval{}, Exceptions: []opsview.Exception{}}
	var pending, recon int64
	var lastStatus *string
	if err := s.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM ledger.ledger_adjustments WHERE status = 'PENDING_APPROVAL' AND expires_at > now()),
		(SELECT count(*) FROM ledger.ledger_reconciliation_references WHERE status IN ('OPEN','INVESTIGATING')),
		(SELECT CASE WHEN bool_or(status <> 'PASSED') THEN 'FAILED' ELSE 'PASSED' END FROM (
		   SELECT DISTINCT ON (invariant_code) status FROM ledger.ledger_invariant_runs ORDER BY invariant_code, started_at DESC) x)`).
		Scan(&pending, &recon, &lastStatus); err != nil {
		return snap, err
	}
	const view = "ledger.view"
	adj := opsview.Count(m, "ledger.adjustments.pending", "Ledger adjustments awaiting a checker (TEST ledger)", pending, view,
		"ledger.ledger_adjustments PENDING_APPROVAL, not expired", "/admin/finance/ledger")
	adj.Nature = opsview.TestOnly
	rec := opsview.Count(m, "ledger.reconciliation.open", "Open ledger reconciliation exceptions (TEST ledger)", recon, view,
		"ledger.ledger_reconciliation_references OPEN/INVESTIGATING", "/admin/finance/exceptions")
	rec.Nature = opsview.TestOnly
	integrity := opsview.Metric{Key: "ledger.integrity", Label: "Ledger integrity (latest run per invariant)", Module: m, Nature: opsview.TestOnly,
		Permission: view, Source: "ledger.ledger_invariant_runs, latest per invariant", Href: "/admin/finance/ledger"}
	switch {
	case lastStatus == nil:
		integrity.Status = opsview.StatusUnknown // never run
	case *lastStatus == "PASSED":
		integrity.Status = opsview.Healthy
	default:
		integrity.Status = opsview.Unavailable
	}
	snap.Metrics = append(snap.Metrics, adj, rec, integrity, opsview.Metric{Key: "ledger.production_posting", Label: "Production ledger posting",
		Module: m, Nature: opsview.Blocked, Status: opsview.StatusBlocked, Permission: view,
		Source: "ledger.ProductionPostingEnabled = false; ledger database designated TEST (ADR-042)"})
	rows, err := s.Pool.Query(ctx, `SELECT id::text, kind, currency, requested_by::text, requested_at, status FROM ledger.ledger_adjustments
		WHERE status = 'PENDING_APPROVAL' AND expires_at > now() ORDER BY requested_at LIMIT 200`)
	if err != nil {
		return snap, err
	}
	for rows.Next() {
		var a opsview.Approval
		var kind, ccy string
		if err := rows.Scan(&a.SourceRecordID, &kind, &ccy, &a.RequestedBy, &a.RequestedAt, &a.Status); err != nil {
			rows.Close()
			return snap, err
		}
		a.ID, a.SourceModule, a.Action, a.RequiredPermission = m+":adjustment:"+a.SourceRecordID, m, "LEDGER_ADJUSTMENT", "ledger.adjustment.approve"
		a.Summary, a.Makers, a.Href = "Ledger "+kind+" ("+ccy+", TEST ledger)", []string{a.RequestedBy}, "/admin/finance/ledger"
		snap.Approvals = append(snap.Approvals, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return snap, err
	}
	rows, err = s.Pool.Query(ctx, `SELECT id::text, exception_type, status, detected_at FROM ledger.ledger_reconciliation_references
		WHERE status IN ('OPEN','INVESTIGATING') ORDER BY detected_at DESC LIMIT 200`)
	if err != nil {
		return snap, err
	}
	defer rows.Close()
	for rows.Next() {
		var e opsview.Exception
		if err := rows.Scan(&e.SourceRef, &e.Category, &e.DomainStatus, &e.CreatedAt); err != nil {
			return snap, err
		}
		e.ID, e.SourceModule, e.Severity, e.Permission = m+":recon:"+e.SourceRef, m, "SEV2", view
		e.Summary, e.Href = "Ledger reconciliation: "+e.Category+" (TEST ledger)", "/admin/finance/exceptions"
		snap.Exceptions = append(snap.Exceptions, e)
	}
	return snap, rows.Err()
}
