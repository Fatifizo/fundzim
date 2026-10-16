package beneficiaries

import (
	"context"

	"github.com/Fatifizo/fundzim/internal/platform/opsview"
)

// OpsSnapshot is the beneficiaries module's contribution to the operations portal (Stage 14): beneficiary verification
// backlog and second approvals waiting, from its own tables. Read-only.
func (s *Service) OpsSnapshot(ctx context.Context) (opsview.Snapshot, error) {
	const m = "beneficiaries"
	snap := opsview.Snapshot{Module: m, Metrics: []opsview.Metric{}, Approvals: []opsview.Approval{}, Exceptions: []opsview.Exception{}}
	var pending int64
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM app.beneficiaries WHERE status IN ('SUBMITTED','UNDER_REVIEW','ESCALATED')`).Scan(&pending); err != nil {
		return snap, err
	}
	const p = "beneficiary.verification.decide"
	snap.Metrics = append(snap.Metrics, opsview.Count(m, "beneficiaries.reviews.pending", "Beneficiary verifications pending", pending, p,
		"app.beneficiaries SUBMITTED/UNDER_REVIEW/ESCALATED", "/admin/verification/beneficiaries"))
	rows, err := s.Pool.Query(ctx, `SELECT id::text, pending_outcome, pending_decided_by::text, updated_at, status FROM app.beneficiaries
		WHERE pending_outcome IS NOT NULL AND status IN ('UNDER_REVIEW','ESCALATED') ORDER BY updated_at LIMIT 200`)
	if err != nil {
		return snap, err
	}
	defer rows.Close()
	for rows.Next() {
		var a opsview.Approval
		var outcome string
		if err := rows.Scan(&a.SourceRecordID, &outcome, &a.RequestedBy, &a.RequestedAt, &a.Status); err != nil {
			return snap, err
		}
		a.ID, a.SourceModule, a.Action, a.RequiredPermission = m+":"+a.SourceRecordID, m, "BENEFICIARY_SECOND_APPROVAL", p
		a.Summary, a.Makers, a.Href = "Beneficiary: second approval of "+outcome, []string{a.RequestedBy}, "/admin/verification/cases/"+a.SourceRecordID
		snap.Approvals = append(snap.Approvals, a)
	}
	return snap, rows.Err()
}
