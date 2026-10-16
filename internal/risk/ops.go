package risk

import (
	"context"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/opsview"
)

// OpsSnapshot is the risk module's contribution to the operations portal (Stage 14, ADR-046): counts from its own
// tables, pending maker-checker decisions and open exceptions. Read-only.
func (s *Service) OpsSnapshot(ctx context.Context) (opsview.Snapshot, error) {
	const m = "risk"
	snap := opsview.Snapshot{Module: m, Metrics: []opsview.Metric{}, Approvals: []opsview.Approval{}, Exceptions: []opsview.Exception{}}
	var open, p1, overdue, unassignedP1, holds, rulesActive, failing, scrError, scrReview int64
	if err := s.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM risk.risk_cases WHERE status NOT IN ('RESOLVED','CLOSED')),
		(SELECT count(*) FROM risk.risk_cases WHERE status NOT IN ('RESOLVED','CLOSED') AND priority IN ('P1','P2')),
		(SELECT count(*) FROM risk.risk_cases WHERE status NOT IN ('RESOLVED','CLOSED') AND due_at < now()),
		(SELECT count(*) FROM risk.risk_cases WHERE status NOT IN ('RESOLVED','CLOSED') AND priority = 'P1' AND assigned_to IS NULL),
		(SELECT count(*) FROM risk.risk_holds WHERE status IN ('ACTIVE','UNDER_REVIEW','RELEASE_PENDING')),
		(SELECT count(*) FROM risk.risk_rule_versions WHERE status = 'ACTIVE'),
		(SELECT count(DISTINCT f.rule_version_id) FROM risk.risk_rule_failures f JOIN risk.risk_rule_versions v ON v.id = f.rule_version_id WHERE v.status = 'ACTIVE'),
		(SELECT count(*) FROM risk.risk_screening_requests WHERE status = 'ERROR'),
		(SELECT count(*) FROM risk.risk_screening_requests WHERE status IN ('POTENTIAL_MATCH','REVIEW_REQUIRED'))`).
		Scan(&open, &p1, &overdue, &unassignedP1, &holds, &rulesActive, &failing, &scrError, &scrReview); err != nil {
		return snap, err
	}
	const view = "risk.operations.view"
	snap.Metrics = append(snap.Metrics,
		opsview.Count(m, "risk.cases.open", "Open risk cases", open, view, "risk.risk_cases not RESOLVED/CLOSED", "/admin/risk/cases?status=OPEN_ANY"),
		opsview.Count(m, "risk.cases.high_priority", "High-priority risk cases (P1/P2)", p1, view, "risk.risk_cases open with priority P1 or P2", "/admin/risk/cases"),
		opsview.Count(m, "risk.cases.overdue", "Risk cases past their internal target", overdue, view, "risk.risk_cases open with due_at in the past (ENGINEERING_DEFAULT targets)", "/admin/risk/cases"),
		opsview.Count(m, "risk.holds.active", "Active compliance/risk holds", holds, view, "risk.risk_holds ACTIVE, UNDER_REVIEW or RELEASE_PENDING", "/admin/risk/holds"),
		opsview.Count(m, "risk.screening.awaiting_review", "Screenings awaiting analyst review (TEST)", scrReview, view, "risk.risk_screening_requests POTENTIAL_MATCH/REVIEW_REQUIRED (simulator)", "/admin/risk/screening"),
	)
	rules := opsview.Count(m, "risk.rules.active", "Active risk rule versions", rulesActive, view, "risk.risk_rule_versions ACTIVE", "/admin/risk/rules")
	switch {
	case rulesActive == 0:
		rules.Status = opsview.NotConfigured // nothing is evaluated until two people approve a rule (KI-S13-06)
	case failing > 0:
		rules.Status = opsview.Degraded
	default:
		rules.Status = opsview.Healthy
	}
	snap.Metrics = append(snap.Metrics, rules)
	screening := opsview.Metric{Key: "risk.screening.production", Label: "Production sanctions screening", Module: m, Nature: opsview.Blocked,
		Status: opsview.StatusBlocked, Source: "risk.ScreeningProductionEnabled = false; no screening provider selected", Permission: view}
	snap.Metrics = append(snap.Metrics, screening)
	snap.Metrics = append(snap.Metrics, opsview.Count(m, "risk.cases.unassigned_p1", "Unassigned P1 risk cases", unassignedP1, view,
		"risk.risk_cases open, priority P1, no assignee", "/admin/risk/cases?assigned_to=unassigned"))

	// approvals: rule versions, requested holds, release requests, case decisions, screening reviews
	rows, err := s.Pool.Query(ctx, `
		SELECT 'rule_version:' || v.id, v.id::text, 'RULE_VERSION_APPROVAL', r.code || ' v' || v.version_no, coalesce(v.submitted_by::text, ''), coalesce(v.submitted_at, v.created_at),
		       '', v.status, v.severity, ARRAY[v.created_by::text, coalesce(v.submitted_by::text, '')], '/admin/risk/rules/' || r.id
		FROM risk.risk_rule_versions v JOIN risk.risk_rules r ON r.id = v.rule_id WHERE v.status = 'PENDING_APPROVAL'
		UNION ALL
		SELECT 'hold_activation:' || h.id, h.id::text, 'HOLD_ACTIVATION', h.hold_type || ' on ' || h.subject_type, h.created_by, h.created_at,
		       '', h.status, '', ARRAY[h.created_by], '/admin/risk/holds'
		FROM risk.risk_holds h WHERE h.status = 'REQUESTED'
		UNION ALL
		SELECT 'hold_release:' || h.id, h.id::text, 'HOLD_RELEASE', h.hold_type || ' on ' || h.subject_type, coalesce(h.release_requested_by::text, ''),
		       coalesce(h.release_requested_at, h.updated_at), '', h.status, '', ARRAY[coalesce(h.release_requested_by::text, '')], '/admin/risk/holds'
		FROM risk.risk_holds h WHERE h.status = 'RELEASE_PENDING'
		UNION ALL
		SELECT 'case_decision:' || c.id, c.id::text, 'CASE_DECISION', 'Case ' || c.case_number || ': ' || coalesce(c.proposed_outcome, ''),
		       coalesce(c.proposed_by::text, ''), coalesce(c.proposed_at, c.updated_at), '', c.status, c.priority,
		       ARRAY[coalesce(c.proposed_by::text, '')], '/admin/risk/cases/' || c.id
		FROM risk.risk_cases c WHERE c.status = 'DECISION_PENDING'
		UNION ALL
		SELECT 'screening_review:' || q.id, q.id::text, 'SCREENING_REVIEW', 'Potential match (TEST) on ' || q.subject_type, q.requested_by::text, q.updated_at,
		       '', q.status, '', ARRAY[q.requested_by::text], '/admin/risk/screening/' || q.id
		FROM risk.risk_screening_requests q WHERE q.status IN ('POTENTIAL_MATCH','REVIEW_REQUIRED')
		ORDER BY 6 LIMIT 200`)
	if err != nil {
		return snap, err
	}
	for rows.Next() {
		var a opsview.Approval
		if err := rows.Scan(&a.ID, &a.SourceRecordID, &a.Action, &a.Summary, &a.RequestedBy, &a.RequestedAt, &a.RequiredPermission, &a.Status,
			&a.RiskLevel, &a.Makers, &a.Href); err != nil {
			rows.Close()
			return snap, err
		}
		a.ID, a.SourceModule = m+":"+a.ID, m
		a.RequiredPermission = map[string]string{"RULE_VERSION_APPROVAL": "risk.rule.approve", "HOLD_ACTIVATION": "risk.hold.release",
			"HOLD_RELEASE": "risk.hold.release", "CASE_DECISION": "risk.case.approve", "SCREENING_REVIEW": "risk.screening.review"}[a.Action]
		snap.Approvals = append(snap.Approvals, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return snap, err
	}

	// exceptions: failing rule versions, screening errors, monitoring events that need a person (last 7 days)
	rows, err = s.Pool.Query(ctx, `
		SELECT 'rule_failure:' || f.rule_version_id, f.rule_version_id::text, 'SEV2', 'RULE_EVALUATION_FAILED', r.code || ': ' || max(f.error_code),
		       min(f.occurred_at), 'ACTIVE_RULE_FAILING', '/admin/risk/rules/' || r.id
		FROM risk.risk_rule_failures f JOIN risk.risk_rule_versions v ON v.id = f.rule_version_id JOIN risk.risk_rules r ON r.id = v.rule_id
		WHERE v.status = 'ACTIVE' GROUP BY f.rule_version_id, r.code, r.id
		UNION ALL
		SELECT 'screening_error:' || q.id, q.id::text, 'SEV3', 'SCREENING_ERROR', 'Screening (TEST) ' || coalesce(q.last_error, 'ERROR'), q.updated_at, q.status,
		       '/admin/risk/screening/' || q.id
		FROM risk.risk_screening_requests q WHERE q.status = 'ERROR'
		UNION ALL
		SELECT 'monitoring:' || e.id, e.id::text, CASE e.severity WHEN 'HIGH' THEN 'SEV2' ELSE 'SEV3' END, e.event_type, e.event_type, e.occurred_at, 'RECORDED',
		       '/admin/risk/monitoring'
		FROM risk.risk_monitoring_events e WHERE e.occurred_at > $1 AND e.event_type IN ('CASE_UNASSIGNED_CRITICAL','SCREENING_STALE',
		  'RECON_MISSING_EVALUATION','RECON_DUPLICATE_CASES','RECON_ORPHANED_HOLD','RECON_EVIDENCE_HASH_MISMATCH','RECON_RULE_UNPARSEABLE','RECON_HOLD_PAST_EXPIRY')
		ORDER BY 6 DESC LIMIT 200`, time.Now().Add(-7*24*time.Hour))
	if err != nil {
		return snap, err
	}
	defer rows.Close()
	for rows.Next() {
		var e opsview.Exception
		if err := rows.Scan(&e.ID, &e.SourceRef, &e.Severity, &e.Category, &e.Summary, &e.CreatedAt, &e.DomainStatus, &e.Href); err != nil {
			return snap, err
		}
		e.ID, e.SourceModule, e.Permission = m+":"+e.ID, m, view
		snap.Exceptions = append(snap.Exceptions, e)
	}
	return snap, rows.Err()
}

// OpsSearch finds risk cases by number or id (staff with risk.operations.view; identifiers only).
func (s *Service) OpsSearch(ctx context.Context, q string) ([]opsview.SearchHit, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id::text, case_number, case_type, status FROM risk.risk_cases
		WHERE id::text = $1 OR case_number::text = $1 OR subject_id::text = $1 ORDER BY created_at DESC LIMIT 20`, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []opsview.SearchHit{}
	for rows.Next() {
		var id, typ, st string
		var n int64
		if err := rows.Scan(&id, &n, &typ, &st); err != nil {
			return nil, err
		}
		out = append(out, opsview.SearchHit{Kind: "RISK_CASE", ID: id, Label: "Risk case " + itoa64(n) + " (" + typ + ")", Status: st,
			Permission: "risk.operations.view", Href: "/admin/risk/cases/" + id})
	}
	return out, rows.Err()
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
