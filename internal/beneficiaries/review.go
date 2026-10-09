package beneficiaries

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/kyc"
	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/verifcase"
)

func actorOf(ctx context.Context) string {
	if p := authz.PrincipalFrom(ctx); p != nil {
		return p.UserID
	}
	return ""
}

func actorTypeOf(ctx context.Context) string {
	if p := authz.PrincipalFrom(ctx); p != nil && p.Kind == authz.KindStaff {
		return "STAFF"
	}
	return "USER"
}

// conflicted: the reviewer owns, created, is, or belongs to the owning organisation of the beneficiary.
// A staff account's linked personal account counts as the reviewer too (kyc.ActorIdentities).
func (s *Service) conflicted(ctx context.Context, b Beneficiary, actorID string) (bool, error) {
	actors, err := s.KYC.ActorIdentities(ctx, actorID)
	if err != nil {
		return false, err
	}
	for _, a := range actors {
		if b.createdBy == a || (b.ownerUser != nil && *b.ownerUser == a) {
			return true, nil
		}
		if b.ownerOrg != nil {
			if role, err := s.Orgs.MemberRole(ctx, *b.ownerOrg, a); err != nil || role != "" {
				return role != "", err
			}
		}
	}
	return false, nil
}

func (s *Service) setStatus(ctx context.Context, tx pgx.Tx, b Beneficiary, to, evType, reason, extra string, args ...any) error {
	sql := `UPDATE app.beneficiaries SET status = $2`
	if extra != "" {
		sql += ", " + extra
	}
	all := append([]any{b.ID, to}, args...)
	var version int
	if err := tx.QueryRow(ctx, sql+` WHERE id = $1 RETURNING version`, all...).Scan(&version); err != nil {
		return err
	}
	return s.event(ctx, tx, b.ID, version, evType, b.status, to, reason)
}

// Assign assigns the review (self by default).
func (s *Service) Assign(ctx context.Context, id, actorID, assignee string) error {
	if assignee == "" {
		assignee = actorID
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		b, err := s.load(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if !verifcase.Open(b.status) || b.status == verifcase.Draft {
			return errNotAllowed
		}
		if bad, err := s.conflicted(ctx, b, assignee); err != nil {
			return err
		} else if bad {
			return errSelf
		}
		if err := s.setStatus(ctx, tx, b, b.status, "ASSIGNED", "", "assigned_to = $3, assigned_at = $4", assignee, s.now()); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "beneficiary.verification.assigned", TargetType: "beneficiary", TargetID: id,
			Metadata: map[string]any{"assignee": assignee}})
	})
}

// StartReview moves SUBMITTED → UNDER_REVIEW.
func (s *Service) StartReview(ctx context.Context, id, actorID string) error {
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		b, err := s.load(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if verifcase.Target(verifcase.ActStartReview, b.status) == "" {
			return errNotAllowed
		}
		if b.assignedTo == nil || *b.assignedTo != actorID {
			return errAssigned
		}
		if err := s.setStatus(ctx, tx, b, verifcase.UnderReview, "REVIEW_STARTED", "", ""); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "beneficiary.verification.review_started", TargetType: "beneficiary", TargetID: id})
	})
}

// RequestInformation returns the case to the owner.
func (s *Service) RequestInformation(ctx context.Context, id, actorID, message string, items []string) error {
	message = strings.TrimSpace(message)
	if len(message) < 3 || len(message) > 2000 {
		return httpx.Validation(errs.Detail{Field: "message", Code: "INVALID_LENGTH"})
	}
	if items == nil {
		items = []string{}
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		b, err := s.load(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if verifcase.Target(verifcase.ActRequestInfo, b.status) == "" {
			return errNotAllowed
		}
		if b.assignedTo == nil || *b.assignedTo != actorID {
			return errAssigned
		}
		if _, err := tx.Exec(ctx, `INSERT INTO app.beneficiary_information_requests (id, beneficiary_id, message, items, requested_by)
			VALUES ($1, $2, $3, $4, $5)`, ids.New(), id, message, items, actorID); err != nil {
			return err
		}
		if err := s.setStatus(ctx, tx, b, verifcase.AdditionalInfoRequired, "INFORMATION_REQUESTED", "",
			"pending_outcome = NULL, pending_decided_by = NULL, pending_reason_code = NULL"); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{Action: "beneficiary.verification.information_requested", TargetType: "beneficiary", TargetID: id}); err != nil {
			return err
		}
		return s.emit(ctx, tx, "beneficiaries.verification_information_requested", b, nil)
	})
}

// Decide applies a reviewer decision; four-eyes approvals wait for SecondApprove.
func (s *Service) Decide(ctx context.Context, id, actorID string, act verifcase.Action, in kyc.DecisionInput) (kyc.Outcome, error) {
	if !kyc.ValidReasonCode(in.ReasonCode) {
		return kyc.Outcome{}, httpx.Validation(errs.Detail{Field: "reason_code", Code: "INVALID_FORMAT"})
	}
	if act == verifcase.ActReject && len(strings.TrimSpace(in.UserMessage)) < 3 {
		return kyc.Outcome{}, httpx.Validation(errs.Detail{Field: "user_message", Code: "INVALID_LENGTH"})
	}
	// quick pre-checks outside the lock (re-checked under the lock below)
	b0, err := s.load(ctx, s.Pool, id, false)
	if err != nil {
		return kyc.Outcome{}, err
	}
	if bad, err := s.conflicted(ctx, b0, actorID); err != nil {
		return kyc.Outcome{}, err
	} else if bad {
		return kyc.Outcome{}, errSelf
	}
	evidence, err := s.KYC.RecordExternalNote(ctx, "beneficiary_id", id, "beneficiary", in.Note)
	if err != nil {
		return kyc.Outcome{}, err
	}
	pol, err := s.KYC.ActivePolicy(ctx)
	if err != nil {
		return kyc.Outcome{}, err
	}
	var out kyc.Outcome
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		b, err := s.load(ctx, tx, id, true)
		if err != nil {
			return err
		}
		to := verifcase.Target(act, b.status)
		if to == "" {
			return errNotAllowed
		}
		if verifcase.Open(b.status) && (b.assignedTo == nil || *b.assignedTo != actorID) {
			return errAssigned
		}
		if b.pendingOutcome != nil {
			return errs.New(errs.Conflict, "AWAITING_SECOND_APPROVAL", "A first approval is waiting for a second reviewer.")
		}
		if act == verifcase.ActApprove && b.requiresSecond {
			if err := s.setStatus(ctx, tx, b, b.status, "FIRST_APPROVAL", in.ReasonCode,
				"pending_outcome = 'APPROVE_WITH_CONDITIONS', pending_decided_by = $3, pending_reason_code = $4", actorID, in.ReasonCode); err != nil {
				return err
			}
			out = kyc.Outcome{Status: b.status, AwaitingSecondApproval: true}
			return audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "beneficiary.verification.first_approval", TargetType: "beneficiary",
				TargetID: id, Reason: in.ReasonCode})
		}
		if err := s.finalise(ctx, tx, pol, b, act, to, actorID, "", in, evidence); err != nil {
			return err
		}
		out = kyc.Outcome{Status: to}
		return nil
	})
	return out, err
}

// SecondApprove confirms a pending approval (a different, unconflicted reviewer; also DB-enforced).
func (s *Service) SecondApprove(ctx context.Context, id, actorID, note string) (kyc.Outcome, error) {
	b0, err := s.load(ctx, s.Pool, id, false)
	if err != nil {
		return kyc.Outcome{}, err
	}
	if bad, err := s.conflicted(ctx, b0, actorID); err != nil {
		return kyc.Outcome{}, err
	} else if bad {
		return kyc.Outcome{}, errSelf
	}
	evidence, err := s.KYC.RecordExternalNote(ctx, "beneficiary_id", id, "beneficiary", note)
	if err != nil {
		return kyc.Outcome{}, err
	}
	pol, err := s.KYC.ActivePolicy(ctx)
	if err != nil {
		return kyc.Outcome{}, err
	}
	var out kyc.Outcome
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		b, err := s.load(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if b.pendingOutcome == nil {
			return errs.New(errs.Conflict, "NO_PENDING_APPROVAL", "There is no first approval to confirm.")
		}
		if *b.pendingBy == actorID {
			return errs.New(errs.Forbidden, "SECOND_APPROVER_MUST_DIFFER", "A different reviewer must give the second approval.")
		}
		to := verifcase.Target(verifcase.ActApprove, b.status)
		if to == "" {
			return errNotAllowed
		}
		in := kyc.DecisionInput{ReasonCode: *b.pendingReason}
		if err := s.finalise(ctx, tx, pol, b, verifcase.ActApprove, to, *b.pendingBy, actorID, in, evidence); err != nil {
			return err
		}
		out = kyc.Outcome{Status: to}
		return nil
	})
	return out, err
}

func (s *Service) finalise(ctx context.Context, tx pgx.Tx, pol kyc.Policy, b Beneficiary, act verifcase.Action, to, decidedBy, second string,
	in kyc.DecisionInput, evidence string) error {
	now := s.now()
	outcome := map[verifcase.Action]string{verifcase.ActApprove: "APPROVE", verifcase.ActReject: "REJECT", verifcase.ActEscalate: "ESCALATE",
		verifcase.ActReturn: "REOPEN", verifcase.ActSuspend: "SUSPEND", verifcase.ActReinstate: "REINSTATE", verifcase.ActReopen: "REOPEN",
		verifcase.ActRevoke: "REVOKE"}[act]
	extra := "pending_outcome = NULL, pending_decided_by = NULL, pending_reason_code = NULL"
	var args []any
	switch act {
	case verifcase.ActApprove, verifcase.ActSuspend, verifcase.ActReinstate:
		extra += ", decided_at = $3"
		args = []any{now}
	case verifcase.ActReject, verifcase.ActRevoke:
		extra += ", decided_at = $3, closed_at = $3, assigned_to = NULL, assigned_at = NULL"
		args = []any{now}
	}
	evType := map[verifcase.Action]string{verifcase.ActApprove: "APPROVED", verifcase.ActReject: "REJECTED", verifcase.ActEscalate: "ESCALATED",
		verifcase.ActReturn: "RETURNED", verifcase.ActSuspend: "SUSPENDED", verifcase.ActReinstate: "REINSTATED", verifcase.ActReopen: "REOPENED",
		verifcase.ActRevoke: "REVOKED"}[act]
	if err := s.setStatus(ctx, tx, b, to, evType, in.ReasonCode, extra, args...); err != nil {
		return err
	}
	auditID, err := audit.Append(ctx, tx, audit.Event{Stream: audit.Security, Action: "beneficiary.verification." + strings.ToLower(outcome),
		TargetType: "beneficiary", TargetID: b.ID, Reason: in.ReasonCode,
		Metadata: map[string]any{"from_status": b.status, "to_status": to, "policy_version": pol.Version, "second_approver": second != ""}})
	if err != nil {
		return err
	}
	evidenceIDs, err := s.KYC.EvidenceIDs(ctx, "beneficiary_id", b.ID)
	if err != nil {
		return err
	}
	evidenceIDs = append(evidenceIDs, evidence)
	var secondArg any
	if second != "" {
		secondArg = second
	}
	if _, err := tx.Exec(ctx, `INSERT INTO app.beneficiary_verifications (id, beneficiary_id, outcome, from_status, to_status, decided_by,
		second_approver_id, second_approval_required, reason_code, user_message, evidence_record_ids, policy_version, audit_event_id, decided_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, ids.New(), b.ID, outcome, b.status, to, decidedBy, secondArg,
		b.requiresSecond, in.ReasonCode, nullable(strings.TrimSpace(in.UserMessage)), evidenceIDs, pol.Version, auditID, now); err != nil {
		return err
	}
	ev := map[verifcase.Action]string{verifcase.ActApprove: "beneficiaries.verification_approved", verifcase.ActReject: "beneficiaries.verification_rejected",
		verifcase.ActEscalate: "beneficiaries.verification_escalated", verifcase.ActSuspend: "beneficiaries.verification_suspended",
		verifcase.ActRevoke: "beneficiaries.verification_revoked"}[act]
	if ev == "" {
		return nil
	}
	return s.emit(ctx, tx, ev, b, map[string]any{"reason_code": in.ReasonCode})
}

// QueueItem mirrors kyc.QueueItem for beneficiaries.
func (s *Service) Queue(ctx context.Context, f kyc.QueueFilter) ([]kyc.QueueItem, error) {
	statuses := f.Statuses
	if len(statuses) == 0 {
		statuses = []string{verifcase.Submitted, verifcase.UnderReview, verifcase.Escalated}
	}
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	assigned := f.Assigned
	if assigned == "" {
		assigned = "any"
	}
	var actor any
	if f.ActorID != "" {
		actor = f.ActorID
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, status, risk_level, assigned_to, submitted_at, requires_second_approval, pending_outcome IS NOT NULL
		FROM app.beneficiaries WHERE status = ANY($1) AND ($2 = 'any' OR ($2 = 'me' AND assigned_to = $3::uuid) OR ($2 = 'unassigned' AND assigned_to IS NULL))
		AND ($4::timestamptz IS NULL OR (submitted_at, id) > ($4, $6::uuid)) ORDER BY submitted_at NULLS LAST, id LIMIT $5`, statuses, assigned, actor, f.Before, limit, f.CursorID())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []kyc.QueueItem{}
	for rows.Next() {
		q := kyc.QueueItem{Type: "BENEFICIARY", SubjectType: "BENEFICIARY"}
		if err := rows.Scan(&q.ID, &q.Status, &q.RiskLevel, &q.AssignedTo, &q.SubmittedAt, &q.FourEyes, &q.Pending); err != nil {
			return nil, err
		}
		q.SubjectID = q.ID
		out = append(out, q)
	}
	return out, rows.Err()
}

// Exists reports whether id is a beneficiary.
func (s *Service) Exists(ctx context.Context, id string) (bool, error) {
	if !ids.Valid(id) {
		return false, nil
	}
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM app.beneficiaries WHERE id = $1`, id).Scan(&n)
	return n > 0, err
}

// History returns the timeline.
func (s *Service) History(ctx context.Context, id string) ([]kyc.HistoryItem, error) {
	rows, err := s.Pool.Query(ctx, `SELECT event_type, from_status, to_status, actor_type, actor_id, reason_code, payload, occurred_at
		FROM app.beneficiary_events WHERE beneficiary_id = $1 ORDER BY beneficiary_version`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []kyc.HistoryItem{}
	for rows.Next() {
		var h kyc.HistoryItem
		if err := rows.Scan(&h.EventType, &h.FromStatus, &h.ToStatus, &h.ActorType, &h.ActorID, &h.ReasonCode, &h.Payload, &h.OccurredAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// Sweep closes information requests with no response within the policy period.
func (s *Service) Sweep(ctx context.Context, limit int) (int, error) {
	pol, err := s.KYC.ActivePolicy(ctx)
	if err != nil {
		return 0, err
	}
	cutoff := s.now().AddDate(0, 0, -pol.Rules.InformationRequestExpiryDays)
	rows, err := s.Pool.Query(ctx, `SELECT id FROM app.beneficiaries WHERE status = 'ADDITIONAL_INFORMATION_REQUIRED' AND updated_at <= $1 LIMIT $2`, cutoff, limit)
	if err != nil {
		return 0, err
	}
	var idList []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		idList = append(idList, id)
	}
	rows.Close()
	n := 0
	for _, id := range idList {
		err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			b, err := s.load(ctx, tx, id, true)
			if err != nil || b.status != verifcase.AdditionalInfoRequired {
				return err
			}
			if err := s.setStatus(ctx, tx, b, verifcase.Expired, "EXPIRED", "INFORMATION_NOT_PROVIDED", "closed_at = $3", s.now()); err != nil {
				return err
			}
			return s.emit(ctx, tx, "beneficiaries.verification_expired", b, nil)
		})
		if err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
