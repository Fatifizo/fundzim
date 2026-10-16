package kyc

import (
	"context"

	"github.com/Fatifizo/fundzim/internal/platform/opsview"
)

// OpsSnapshot is the kyc module's contribution to the operations portal (Stage 14, ADR-046): KYC/KYB review backlog
// and four-eyes decisions waiting, from its own tables (case identifiers only — never identity data). Read-only.
func (s *Service) OpsSnapshot(ctx context.Context) (opsview.Snapshot, error) {
	const m = "kyc"
	snap := opsview.Snapshot{Module: m, Metrics: []opsview.Metric{}, Approvals: []opsview.Approval{}, Exceptions: []opsview.Exception{}}
	var kycQ, kycInfo, kybQ, kybInfo, escalated int64
	if err := s.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM kyc.kyc_cases WHERE status IN ('SUBMITTED','UNDER_REVIEW','ESCALATED')),
		(SELECT count(*) FROM kyc.kyc_cases WHERE status = 'ADDITIONAL_INFORMATION_REQUIRED'),
		(SELECT count(*) FROM kyc.kyb_cases WHERE status IN ('SUBMITTED','UNDER_REVIEW','ESCALATED')),
		(SELECT count(*) FROM kyc.kyb_cases WHERE status = 'ADDITIONAL_INFORMATION_REQUIRED'),
		(SELECT count(*) FROM kyc.kyc_cases WHERE status = 'ESCALATED') + (SELECT count(*) FROM kyc.kyb_cases WHERE status = 'ESCALATED')`).
		Scan(&kycQ, &kycInfo, &kybQ, &kybInfo, &escalated); err != nil {
		return snap, err
	}
	const p = "kyc.case.review"
	snap.Metrics = append(snap.Metrics,
		opsview.Count(m, "kyc.reviews.pending", "KYC reviews pending", kycQ, p, "kyc.kyc_cases SUBMITTED/UNDER_REVIEW/ESCALATED", "/admin/verification/kyc"),
		opsview.Count(m, "kyc.reviews.awaiting_information", "KYC cases awaiting the applicant", kycInfo, p, "kyc.kyc_cases ADDITIONAL_INFORMATION_REQUIRED",
			"/admin/verification/kyc"),
		opsview.Count(m, "kyb.reviews.pending", "KYB reviews pending", kybQ, p, "kyc.kyb_cases SUBMITTED/UNDER_REVIEW/ESCALATED", "/admin/verification/kyb"),
		opsview.Count(m, "kyb.reviews.awaiting_information", "KYB cases awaiting the organisation", kybInfo, p, "kyc.kyb_cases ADDITIONAL_INFORMATION_REQUIRED",
			"/admin/verification/kyb"),
		opsview.Count(m, "verification.escalated", "Escalated verification cases", escalated, p, "kyc.kyc_cases + kyc.kyb_cases ESCALATED",
			"/admin/verification"),
	)
	rows, err := s.Pool.Query(ctx, `SELECT 'KYC', id::text, pending_outcome, pending_decided_by::text, updated_at, status, risk_level FROM kyc.kyc_cases
		WHERE pending_outcome IS NOT NULL AND status IN ('UNDER_REVIEW','ESCALATED')
		UNION ALL
		SELECT 'KYB', id::text, pending_outcome, pending_decided_by::text, updated_at, status, risk_level FROM kyc.kyb_cases
		WHERE pending_outcome IS NOT NULL AND status IN ('UNDER_REVIEW','ESCALATED') ORDER BY 5 LIMIT 200`)
	if err != nil {
		return snap, err
	}
	defer rows.Close()
	for rows.Next() {
		var a opsview.Approval
		var kind, outcome string
		if err := rows.Scan(&kind, &a.SourceRecordID, &outcome, &a.RequestedBy, &a.RequestedAt, &a.Status, &a.RiskLevel); err != nil {
			return snap, err
		}
		a.ID, a.SourceModule, a.Action = m+":"+kind+":"+a.SourceRecordID, m, kind+"_SECOND_APPROVAL"
		a.Summary, a.RequiredPermission, a.Makers = kind+" case: second approval of "+outcome, p, []string{a.RequestedBy}
		a.Href = "/admin/verification/cases/" + a.SourceRecordID
		snap.Approvals = append(snap.Approvals, a)
	}
	return snap, rows.Err()
}

// OpsSearch finds verification cases by id (no identity data is searchable).
func (s *Service) OpsSearch(ctx context.Context, q string) ([]opsview.SearchHit, error) {
	rows, err := s.Pool.Query(ctx, `SELECT 'KYC_CASE', id::text, status FROM kyc.kyc_cases WHERE id::text = $1
		UNION ALL SELECT 'KYB_CASE', id::text, status FROM kyc.kyb_cases WHERE id::text = $1 LIMIT 20`, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []opsview.SearchHit{}
	for rows.Next() {
		var h opsview.SearchHit
		if err := rows.Scan(&h.Kind, &h.ID, &h.Status); err != nil {
			return nil, err
		}
		h.Label, h.Permission, h.Href = h.Kind+" "+h.ID[:8], "kyc.case.review", "/admin/verification/cases/"+h.ID
		out = append(out, h)
	}
	return out, rows.Err()
}
