package compliance

import (
	"context"

	"github.com/Fatifizo/fundzim/internal/platform/opsview"
)

// OpsSnapshot is the compliance module's contribution to the operations portal (Stage 14, ADR-046): open compliance
// cases and restricting resolutions awaiting approval, from its own tables (compliance pool). Cases flagged
// RESTRICTED_STR are counted but never listed (no tipping-off detail leaves the module). Read-only.
func (s *Service) OpsSnapshot(ctx context.Context) (opsview.Snapshot, error) {
	const m = "compliance"
	snap := opsview.Snapshot{Module: m, Metrics: []opsview.Metric{}, Approvals: []opsview.Approval{}, Exceptions: []opsview.Exception{}}
	var open, s1, awaiting, restrictions int64
	if err := s.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM compliance.compliance_cases WHERE status NOT IN ('RESOLVED','CLOSED')),
		(SELECT count(*) FROM compliance.compliance_cases WHERE status NOT IN ('RESOLVED','CLOSED') AND severity = 'S1'),
		(SELECT count(*) FROM compliance.compliance_cases WHERE resolution_status = 'PROPOSED'),
		(SELECT count(*) FROM compliance.subject_restrictions)`).Scan(&open, &s1, &awaiting, &restrictions); err != nil {
		return snap, err
	}
	snap.Metrics = append(snap.Metrics,
		opsview.Count(m, "compliance.cases.open", "Open compliance cases", open, PermView, "compliance.compliance_cases not RESOLVED/CLOSED", "/admin/compliance/cases"),
		opsview.Count(m, "compliance.cases.s1", "Open S1 compliance cases", s1, PermView, "open compliance.compliance_cases with severity S1", "/admin/compliance/cases"),
		opsview.Count(m, "compliance.resolutions.pending", "Restricting resolutions awaiting approval", awaiting, PermView,
			"compliance.compliance_cases resolution_status PROPOSED", "/admin/compliance/cases"),
		opsview.Count(m, "compliance.restrictions.active", "Subjects under a compliance restriction", restrictions, PermView,
			"compliance.subject_restrictions (projection of approved resolutions)", "/admin/compliance/cases"),
	)
	rows, err := s.pool.Query(ctx, `SELECT id::text, case_number, decision, decided_by::text, decided_at, status, severity FROM compliance.compliance_cases
		WHERE resolution_status = 'PROPOSED' AND confidentiality = 'NORMAL' ORDER BY decided_at LIMIT 200`)
	if err != nil {
		return snap, err
	}
	defer rows.Close()
	for rows.Next() {
		var a opsview.Approval
		var number, decision string
		if err := rows.Scan(&a.SourceRecordID, &number, &decision, &a.RequestedBy, &a.RequestedAt, &a.Status, &a.RiskLevel); err != nil {
			return snap, err
		}
		a.ID, a.SourceModule, a.Action, a.RequiredPermission = m+":"+a.SourceRecordID, m, "COMPLIANCE_RESOLUTION_APPROVAL", PermManage
		a.Summary, a.Makers, a.Href = "Compliance case "+number+": "+decision, []string{a.RequestedBy}, "/admin/compliance/cases/"+a.SourceRecordID
		snap.Approvals = append(snap.Approvals, a)
	}
	return snap, rows.Err()
}
