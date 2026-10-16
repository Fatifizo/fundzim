package fees

import (
	"context"

	"github.com/Fatifizo/fundzim/internal/platform/opsview"
)

// OpsSnapshot is the fee module's contribution to the operations portal (Stage 14, ADR-046): fee changes and manual
// adjustments awaiting a checker and open fee reconciliation items, from its own tables. Every fee is a TEST fee.
// Read-only.
func (s *Service) OpsSnapshot(ctx context.Context) (opsview.Snapshot, error) {
	const m = "fees"
	snap := opsview.Snapshot{Module: m, Metrics: []opsview.Metric{}, Approvals: []opsview.Approval{}, Exceptions: []opsview.Exception{}}
	var versions, schedules, adjustments, recon int64
	if err := s.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM app.fee_policy_versions WHERE status = 'PENDING_APPROVAL'),
		(SELECT count(*) FROM app.fee_provider_schedules WHERE status = 'PENDING_APPROVAL'),
		(SELECT count(*) FROM app.fee_adjustments WHERE status = 'PENDING_APPROVAL'),
		(SELECT count(*) FROM app.fee_reconciliation_references WHERE status = 'OPEN')`).Scan(&versions, &schedules, &adjustments, &recon); err != nil {
		return snap, err
	}
	const view = "fee.view"
	tm := func(key, label string, v int64, src, href string) opsview.Metric {
		x := opsview.Count(m, key, label, v, view, src, href)
		x.Nature = opsview.TestOnly
		return x
	}
	snap.Metrics = append(snap.Metrics,
		tm("fees.policy_versions.pending", "Fee policy versions awaiting a checker (TEST)", versions, "app.fee_policy_versions PENDING_APPROVAL", "/admin/finance/fees/policies"),
		tm("fees.provider_schedules.pending", "Provider fee evidence awaiting a checker", schedules, "app.fee_provider_schedules PENDING_APPROVAL",
			"/admin/finance/fees/provider-schedules"),
		tm("fees.adjustments.pending", "Manual fee adjustments awaiting a checker (TEST)", adjustments, "app.fee_adjustments PENDING_APPROVAL",
			"/admin/finance/fees/adjustments"),
		tm("fees.reconciliation.open", "Open fee reconciliation items (TEST)", recon, "app.fee_reconciliation_references OPEN", "/admin/finance/fees/reconciliation"),
		opsview.Metric{Key: "fees.production", Label: "Production fee charging and revenue recognition", Module: m, Nature: opsview.Blocked,
			Status: opsview.StatusBlocked, Permission: view, Source: "fees.ProductionFeesEnabled = false (ADR-044); no fee model (PD-01)"},
	)
	rows, err := s.Pool.Query(ctx, `
		SELECT 'policy_version:' || v.id, v.id::text, 'FEE_POLICY_VERSION_APPROVAL', 'Fee policy ' || p.code || ' v' || v.version_no, coalesce(v.submitted_by::text, ''),
		       coalesce(v.submitted_at, v.created_at), 'fee.config.approve', ARRAY[v.created_by::text, coalesce(v.submitted_by::text, '')], '/admin/finance/fees/policies/' || p.id
		FROM app.fee_policy_versions v JOIN app.fee_policies p ON p.id = v.policy_id WHERE v.status = 'PENDING_APPROVAL'
		UNION ALL
		SELECT 'schedule:' || id, id::text, 'PROVIDER_FEE_EVIDENCE_APPROVAL', 'Provider fee evidence ' || schedule_version || ' (' || confirmation_status || ')', created_by::text,
		       created_at, 'fee.config.approve', ARRAY[created_by::text], '/admin/finance/fees/provider-schedules'
		FROM app.fee_provider_schedules WHERE status = 'PENDING_APPROVAL'
		UNION ALL
		SELECT 'adjustment:' || id, id::text, 'FEE_ADJUSTMENT_APPROVAL', 'Fee adjustment ' || reason || ' ' || direction || ' (' || currency || ')', coalesce(requested_by::text, ''),
		       created_at, 'fee.adjust.approve', ARRAY[coalesce(requested_by::text, '')], '/admin/finance/fees/adjustments'
		FROM app.fee_adjustments WHERE status = 'PENDING_APPROVAL'
		ORDER BY 6 LIMIT 200`)
	if err != nil {
		return snap, err
	}
	for rows.Next() {
		var a opsview.Approval
		if err := rows.Scan(&a.ID, &a.SourceRecordID, &a.Action, &a.Summary, &a.RequestedBy, &a.RequestedAt, &a.RequiredPermission, &a.Makers, &a.Href); err != nil {
			rows.Close()
			return snap, err
		}
		a.ID, a.SourceModule, a.Status = m+":"+a.ID, m, "PENDING_APPROVAL"
		snap.Approvals = append(snap.Approvals, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return snap, err
	}
	rows, err = s.Pool.Query(ctx, `SELECT id::text, kind, severity, status, detected_at FROM app.fee_reconciliation_references WHERE status = 'OPEN'
		ORDER BY detected_at DESC LIMIT 200`)
	if err != nil {
		return snap, err
	}
	defer rows.Close()
	for rows.Next() {
		var e opsview.Exception
		if err := rows.Scan(&e.SourceRef, &e.Category, &e.Severity, &e.DomainStatus, &e.CreatedAt); err != nil {
			return snap, err
		}
		e.ID, e.SourceModule, e.Permission = m+":recon:"+e.SourceRef, m, view
		e.Summary, e.Href = "Fee reconciliation: "+e.Category+" (TEST)", "/admin/finance/fees/reconciliation"
		snap.Exceptions = append(snap.Exceptions, e)
	}
	return snap, rows.Err()
}
