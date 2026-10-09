package kyc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/verifcase"
	"github.com/Fatifizo/fundzim/internal/users"
)

// DecisionInput is a reviewer's decision payload.
type DecisionInput struct {
	ReasonCode  string
	Note        string // reviewer-only, encrypted, evidence-hashed
	UserMessage string // shown to the subject (no internal detail)
	NameMatch   *bool  // unused for KYC/KYB; kept for symmetry with payout destinations
}

// Outcome of a decision request.
type Outcome struct {
	Status                 string `json:"status"`
	AwaitingSecondApproval bool   `json:"awaiting_second_approval"`
}

// conflict reports whether actorID may not review the case (own verification / organisation member).
// A staff account linked to a personal account (app.users.staff_personal_user_id) is treated as that person too.
func (s *Service) conflict(ctx context.Context, kind, subjectUserID, orgID, createdBy string, submittedBy *string, actorID string) (bool, error) {
	actors, err := s.ActorIdentities(ctx, actorID)
	if err != nil {
		return false, err
	}
	for _, a := range actors {
		if kind == KindKYC {
			if subjectUserID == a {
				return true, nil
			}
			continue
		}
		if createdBy == a || (submittedBy != nil && *submittedBy == a) {
			return true, nil
		}
		role, err := s.Orgs.MemberRole(ctx, orgID, a)
		if err != nil || role != "" {
			return role != "", err
		}
	}
	return false, nil
}

// ActorIdentities returns the actor plus the personal account linked to it when the actor is a staff account.
// Every self-review check compares against all of them: a reviewer never decides their own verification
// through a second account.
func (s *Service) ActorIdentities(ctx context.Context, actorID string) ([]string, error) {
	personal, err := users.PersonalAccount(ctx, s.AppPool, actorID)
	if err != nil {
		return nil, err
	}
	if personal == "" || personal == actorID {
		return []string{actorID}, nil
	}
	return []string{actorID, personal}, nil
}

// generic view of a locked KYC or KYB case for the review engine
type reviewCase struct {
	kind, id, status, subjectUserID, orgID, kybOrgID, profileID, createdBy, targetLevel, orgType, riskLevel, policyVersion string
	submittedBy, assignedTo, pendingOutcome, pendingDecidedBy, pendingReason, identityID                                   *string
	requiresSecond                                                                                                         bool
	version                                                                                                                int
}

func (s *Service) lockReviewCase(ctx context.Context, tx pgx.Tx, kind, id string) (reviewCase, error) {
	switch kind {
	case KindKYC:
		c, err := s.loadCase(ctx, tx, id, true)
		if err != nil {
			return reviewCase{}, err
		}
		return reviewCase{kind: KindKYC, id: c.ID, status: c.Status, subjectUserID: c.UserID, profileID: c.ProfileID, targetLevel: c.TargetLevel,
			riskLevel: c.RiskLevel, policyVersion: c.PolicyVersion, assignedTo: c.AssignedTo, pendingOutcome: c.PendingOutcome,
			pendingDecidedBy: c.PendingDecidedBy, pendingReason: c.PendingReason, identityID: c.IdentityID, requiresSecond: c.RequiresSecond,
			version: c.Version}, nil
	case KindKYB:
		k, err := s.loadKYB(ctx, tx, id, true)
		if err != nil {
			return reviewCase{}, err
		}
		return reviewCase{kind: KindKYB, id: k.ID, status: k.Status, orgID: k.OrganisationID, kybOrgID: k.KYBOrgID, createdBy: k.CreatedBy,
			targetLevel: k.TargetLevel, orgType: k.OrgType, riskLevel: k.RiskLevel, policyVersion: k.PolicyVersion, submittedBy: k.SubmittedBy,
			assignedTo: k.AssignedTo, pendingOutcome: k.PendingOutcome, pendingDecidedBy: k.PendingDecidedBy, pendingReason: k.PendingReason,
			requiresSecond: k.RequiresSecond, version: k.Version}, nil
	}
	return reviewCase{}, ErrCaseNotFound
}

func (c reviewCase) table() string {
	if c.kind == KindKYB {
		return "kyc.kyb_cases"
	}
	return "kyc.kyc_cases"
}

func (c reviewCase) col() string {
	if c.kind == KindKYB {
		return "kyb_case_id"
	}
	return "kyc_case_id"
}

func (c reviewCase) targetType() string { return strings.ToLower(c.kind) + "_case" }

func (c reviewCase) actionPrefix() string { return strings.ToLower(c.kind) }

func (c reviewCase) subject() (string, string) {
	if c.kind == KindKYB {
		return "ORGANISATION", c.orgID
	}
	return "USER", c.subjectUserID
}

// CaseKind returns KYC or KYB for a case id, or "" when it is neither.
func (s *Service) CaseKind(ctx context.Context, id string) (string, error) {
	if !ids.Valid(id) {
		return "", nil
	}
	var kind string
	err := s.Pool.QueryRow(ctx, `SELECT 'KYC' FROM kyc.kyc_cases WHERE id = $1 UNION ALL SELECT 'KYB' FROM kyc.kyb_cases WHERE id = $1`, id).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return kind, err
}

// Assign assigns the case to assignee (self by default). A conflicted reviewer cannot be assigned.
func (s *Service) Assign(ctx context.Context, kind, caseID, actorID, assignee string) error {
	if assignee == "" {
		assignee = actorID
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.lockReviewCase(ctx, tx, kind, caseID)
		if err != nil {
			return err
		}
		if !verifcase.Open(c.status) || c.status == verifcase.Draft {
			return ErrActionNotAllowed
		}
		if bad, err := s.conflict(ctx, c.kind, c.subjectUserID, c.orgID, c.createdBy, c.submittedBy, assignee); err != nil {
			return err
		} else if bad {
			return ErrSelfDecision
		}
		if _, err := tx.Exec(ctx, `UPDATE `+c.table()+` SET assigned_to = $2, assigned_at = $3, version = version + 1 WHERE id = $1`,
			c.id, assignee, s.now()); err != nil {
			return err
		}
		if err := s.caseEvent(ctx, tx, c.kind, c.id, c.version+1, "ASSIGNED", c.status, c.status, "", map[string]any{"assignee": assignee}); err != nil {
			return err
		}
		_, err = audit.RecordGateway(ctx, tx, audit.Event{Action: c.actionPrefix() + ".case.assigned", TargetType: c.targetType(), TargetID: c.id,
			Metadata: map[string]any{"assignee": assignee}})
		return err
	})
}

// transition moves a locked case to `to` with its event and audit.
func (s *Service) transition(ctx context.Context, tx pgx.Tx, c reviewCase, to, eventType, reason string, extraSet string, extraArgs ...any) error {
	sets := "status = $2, version = version + 1"
	args := []any{c.id, to}
	if extraSet != "" {
		sets += ", " + extraSet
		args = append(args, extraArgs...)
	}
	if _, err := tx.Exec(ctx, `UPDATE `+c.table()+` SET `+sets+` WHERE id = $1`, args...); err != nil {
		if isCheck(err) {
			return ErrActionNotAllowed
		}
		return err
	}
	return s.caseEvent(ctx, tx, c.kind, c.id, c.version+1, eventType, c.status, to, reason, nil)
}

func (s *Service) requireAssigned(c reviewCase, actorID string) error {
	if c.assignedTo == nil || *c.assignedTo != actorID {
		return ErrNotAssigned
	}
	return nil
}

// StartReview moves a SUBMITTED case to UNDER_REVIEW (assigned reviewer).
func (s *Service) StartReview(ctx context.Context, kind, caseID, actorID string) error {
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.lockReviewCase(ctx, tx, kind, caseID)
		if err != nil {
			return err
		}
		if verifcase.Target(verifcase.ActStartReview, c.status) == "" {
			return ErrActionNotAllowed
		}
		if err := s.requireAssigned(c, actorID); err != nil {
			return err
		}
		if err := s.transition(ctx, tx, c, verifcase.UnderReview, "REVIEW_STARTED", "", ""); err != nil {
			return err
		}
		_, err = audit.RecordGateway(ctx, tx, audit.Event{Action: c.actionPrefix() + ".case.review_started", TargetType: c.targetType(), TargetID: c.id})
		return err
	})
}

var infoItems = map[string]bool{"IDENTITY_DETAILS": true, "ID_DOCUMENT": true, "SELFIE": true, "PROOF_OF_ADDRESS": true,
	"REGISTRATION_DOCUMENT": true, "PERSONS": true, "BENEFICIAL_OWNERS": true, "AUTHORITY_EVIDENCE": true, "OTHER": true}

// RequestInformation returns the case to the subject with a message.
func (s *Service) RequestInformation(ctx context.Context, kind, caseID, actorID, message string, items []string) error {
	message = strings.TrimSpace(message)
	if len(message) < 3 || len(message) > 2000 {
		return httpx.Validation(errs.Detail{Field: "message", Code: "INVALID_LENGTH"})
	}
	for _, it := range items {
		if !infoItems[it] {
			return httpx.Validation(errs.Detail{Field: "items", Code: "INVALID_VALUE"})
		}
	}
	if items == nil {
		items = []string{}
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.lockReviewCase(ctx, tx, kind, caseID)
		if err != nil {
			return err
		}
		if verifcase.Target(verifcase.ActRequestInfo, c.status) == "" {
			return ErrActionNotAllowed
		}
		if err := s.requireAssigned(c, actorID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kyc.information_requests (id, `+c.col()+`, message, items, requested_by, requested_at)
			VALUES ($1, $2, $3, $4, $5, $6)`, ids.New(), c.id, message, items, actorID, s.now()); err != nil {
			return err
		}
		if err := s.transition(ctx, tx, c, verifcase.AdditionalInfoRequired, "INFORMATION_REQUESTED", "", "pending_outcome = NULL, pending_decided_by = NULL, pending_reason_code = NULL"); err != nil {
			return err
		}
		if _, err := audit.RecordGateway(ctx, tx, audit.Event{Action: c.actionPrefix() + ".case.information_requested", TargetType: c.targetType(),
			TargetID: c.id, Metadata: map[string]any{"items": items}}); err != nil {
			return err
		}
		st, sid := c.subject()
		return s.emit(ctx, tx, "kyc.information_requested", c.targetType(), c.id, map[string]any{"case_id": c.id, "kind": c.kind,
			"subject_type": st, "subject_id": sid})
	})
}

// Decide applies a reviewer decision (approve, reject, escalate, return, suspend, reinstate, reopen, revoke).
// Approvals (and fraud rejections) that need four eyes record a first approval and wait for a second,
// different reviewer (SecondApprove).
func (s *Service) Decide(ctx context.Context, kind, caseID, actorID string, act verifcase.Action, in DecisionInput) (Outcome, error) {
	if !ValidReasonCode(in.ReasonCode) {
		return Outcome{}, httpx.Validation(errs.Detail{Field: "reason_code", Code: "INVALID_FORMAT"})
	}
	if act == verifcase.ActReject && len(strings.TrimSpace(in.UserMessage)) < 3 {
		return Outcome{}, httpx.Validation(errs.Detail{Field: "user_message", Code: "INVALID_LENGTH"})
	}
	pol, err := s.ActivePolicy(ctx)
	if err != nil {
		return Outcome{}, err
	}
	var out Outcome
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.lockReviewCase(ctx, tx, kind, caseID)
		if err != nil {
			return err
		}
		to := verifcase.Target(act, c.status)
		if to == "" {
			return ErrActionNotAllowed
		}
		if bad, err := s.conflict(ctx, c.kind, c.subjectUserID, c.orgID, c.createdBy, c.submittedBy, actorID); err != nil {
			return err
		} else if bad {
			return ErrSelfDecision
		}
		// decisions on an active review belong to the assigned reviewer; post-approval actions (suspend,
		// reinstate, reopen, revoke) may be taken by any authorised reviewer
		if verifcase.Open(c.status) {
			if err := s.requireAssigned(c, actorID); err != nil {
				return err
			}
		}
		if c.pendingOutcome != nil {
			return errs.New(errs.Conflict, "AWAITING_SECOND_APPROVAL", "A first approval is waiting for a second reviewer.")
		}
		evidence, err := s.note(ctx, tx, c.col(), c.id, c.targetType(), in.Note)
		if err != nil {
			return err
		}
		fraud := act == verifcase.ActReject && strings.HasPrefix(in.ReasonCode, "FRAUD_")
		if (act == verifcase.ActApprove && c.requiresSecond) || fraud {
			outcome := "APPROVE_WITH_CONDITIONS"
			if fraud {
				outcome = "REJECT"
			}
			if _, err := tx.Exec(ctx, `UPDATE `+c.table()+` SET pending_outcome = $2, pending_decided_by = $3, pending_reason_code = $4,
				pending_evidence_id = $5, version = version + 1 WHERE id = $1`, c.id, outcome, actorID, in.ReasonCode, evidence); err != nil {
				return err
			}
			if err := s.caseEvent(ctx, tx, c.kind, c.id, c.version+1, "FIRST_APPROVAL", c.status, c.status, in.ReasonCode,
				map[string]any{"pending_outcome": outcome}); err != nil {
				return err
			}
			_, err := audit.RecordGateway(ctx, tx, audit.Event{Stream: audit.Security, Action: c.actionPrefix() + ".case.first_approval",
				TargetType: c.targetType(), TargetID: c.id, Reason: in.ReasonCode, Metadata: map[string]any{"pending_outcome": outcome}})
			out = Outcome{Status: c.status, AwaitingSecondApproval: true}
			return err
		}
		if err := s.finalise(ctx, tx, pol, c, act, to, actorID, "", in, evidence); err != nil {
			return err
		}
		out = Outcome{Status: to}
		return nil
	})
	return out, err
}

// SecondApprove confirms a pending first decision; the second reviewer must differ from the first and be
// unconflicted (also enforced by CHECK and trigger on kyc.kyc_decisions).
func (s *Service) SecondApprove(ctx context.Context, kind, caseID, actorID, note string) (Outcome, error) {
	pol, err := s.ActivePolicy(ctx)
	if err != nil {
		return Outcome{}, err
	}
	var out Outcome
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.lockReviewCase(ctx, tx, kind, caseID)
		if err != nil {
			return err
		}
		if c.pendingOutcome == nil {
			return errs.New(errs.Conflict, "NO_PENDING_APPROVAL", "There is no first approval to confirm.")
		}
		if *c.pendingDecidedBy == actorID {
			return ErrSecondApproverSam
		}
		if bad, err := s.conflict(ctx, c.kind, c.subjectUserID, c.orgID, c.createdBy, c.submittedBy, actorID); err != nil {
			return err
		} else if bad {
			return ErrSelfDecision
		}
		act := verifcase.ActApprove
		if *c.pendingOutcome == "REJECT" {
			act = verifcase.ActReject
		}
		to := verifcase.Target(act, c.status)
		if to == "" {
			return ErrActionNotAllowed
		}
		var evidence string
		if err := tx.QueryRow(ctx, `SELECT pending_evidence_id FROM `+c.table()+` WHERE id = $1`, c.id).Scan(&evidence); err != nil {
			return err
		}
		if _, err := s.note(ctx, tx, c.col(), c.id, c.targetType(), note); err != nil {
			return err
		}
		in := DecisionInput{ReasonCode: *c.pendingReason, UserMessage: "Your verification was not approved."}
		if err := s.finalise(ctx, tx, pol, c, act, to, *c.pendingDecidedBy, actorID, in, evidence); err != nil {
			return err
		}
		out = Outcome{Status: to}
		return nil
	})
	return out, err
}

func (s *Service) finalise(ctx context.Context, tx pgx.Tx, pol Policy, c reviewCase, act verifcase.Action, to, decidedBy, second string,
	in DecisionInput, evidenceID string) error {
	now := s.now()
	outcome := map[verifcase.Action]string{verifcase.ActApprove: "APPROVE_WITH_CONDITIONS", verifcase.ActReject: "REJECT",
		verifcase.ActEscalate: "ESCALATE", verifcase.ActReturn: "REOPEN", verifcase.ActSuspend: "SUSPEND", verifcase.ActReinstate: "REINSTATE",
		verifcase.ActReopen: "REOPEN", verifcase.ActRevoke: "REVOKE"}[act]
	conditions := map[string]any{}
	var grantedLevel any
	var expiresAt *time.Time
	extraSet := "pending_outcome = NULL, pending_decided_by = NULL, pending_reason_code = NULL, pending_evidence_id = NULL"
	var extraArgs []any
	switch act {
	case verifcase.ActApprove:
		// screening is never claimed: no provider is approved (prerequisite-assessment #14)
		conditions["sanctions_pep_screening"] = "NOT_PERFORMED_" + screeningNotPerformed
		grantedLevel = c.targetLevel
		validity := pol.KYCRule(c.riskLevel).ValidityDays
		if c.kind == KindKYB {
			validity = pol.KYBRule(c.orgType).ValidityDays
		}
		if validity > 0 {
			t := now.AddDate(0, 0, validity)
			expiresAt = &t
		}
		extraSet += ", decided_at = $3, expires_at = $4"
		extraArgs = []any{now, expiresAt}
		if err := s.applyApproval(ctx, tx, c, decidedBy, expiresAt); err != nil {
			return err
		}
	case verifcase.ActReject, verifcase.ActRevoke:
		extraSet += ", decided_at = $3, closed_at = $3, assigned_to = NULL, assigned_at = NULL"
		extraArgs = []any{now}
	case verifcase.ActSuspend, verifcase.ActReinstate:
		extraSet += ", decided_at = $3"
		extraArgs = []any{now}
	}
	if err := s.transition(ctx, tx, c, to, map[verifcase.Action]string{verifcase.ActApprove: "APPROVED", verifcase.ActReject: "REJECTED",
		verifcase.ActEscalate: "ESCALATED", verifcase.ActReturn: "RETURNED", verifcase.ActSuspend: "SUSPENDED", verifcase.ActReinstate: "REINSTATED",
		verifcase.ActReopen: "REOPENED", verifcase.ActRevoke: "REVOKED"}[act], in.ReasonCode, extraSet, extraArgs...); err != nil {
		return err
	}
	// subject-side effects
	switch act {
	case verifcase.ActReject:
		if c.identityID != nil {
			if _, err := tx.Exec(ctx, `UPDATE kyc.identities SET status = 'INACTIVE' WHERE id = $1 AND status = 'PENDING'`, *c.identityID); err != nil {
				return err
			}
		}
		if err := s.setSubject(ctx, tx, c, "", ProfileRejected, "CASE_REJECTED", nil); err != nil {
			return err
		}
	case verifcase.ActSuspend:
		if err := s.setSubject(ctx, tx, c, "", ProfileSuspended, "VERIFICATION_SUSPENDED", nil); err != nil {
			return err
		}
	case verifcase.ActReinstate:
		if err := s.setSubject(ctx, tx, c, "", ProfileActive, "VERIFICATION_REINSTATED", nil); err != nil {
			return err
		}
	case verifcase.ActReopen:
		if err := s.setSubject(ctx, tx, c, "", ProfilePendingReview, "VERIFICATION_REOPENED", nil); err != nil {
			return err
		}
	case verifcase.ActRevoke:
		if c.identityID != nil {
			if _, err := tx.Exec(ctx, `UPDATE kyc.identities SET status = 'INACTIVE' WHERE id = $1 AND status = 'ACTIVE'`, *c.identityID); err != nil {
				return err
			}
		}
		lvl := LevelUnverified
		if c.kind == KindKYB {
			lvl = OrgLevelUnverified
		}
		if err := s.setSubject(ctx, tx, c, lvl, ProfileActive, "VERIFICATION_REVOKED", nil); err != nil {
			return err
		}
	}
	auditID, err := audit.RecordGateway(ctx, tx, audit.Event{Stream: audit.Security, Action: c.actionPrefix() + ".case." + strings.ToLower(outcome),
		TargetType: c.targetType(), TargetID: c.id, Reason: in.ReasonCode,
		Metadata: map[string]any{"from_status": c.status, "to_status": to, "policy_version": pol.Version, "second_approver": second != ""}})
	if err != nil {
		return err
	}
	condJSON, _ := json.Marshal(conditions)
	var secondArg any
	if second != "" {
		secondArg = second
	}
	if _, err := tx.Exec(ctx, `INSERT INTO kyc.kyc_decisions (id, `+c.col()+`, outcome, from_status, resulting_status, granted_level, reason_code,
		user_message, conditions, evidence_record_id, decided_by, second_approval_required, second_approver_id, policy_version, audit_event_id, decided_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
		ids.New(), c.id, outcome, c.status, to, grantedLevel, in.ReasonCode, nullable(strings.TrimSpace(in.UserMessage)), condJSON, evidenceID,
		decidedBy, second != "", secondArg, pol.Version, auditID, now); err != nil {
		if isCheck(err) {
			return ErrSelfDecision
		}
		return err
	}
	st, sid := c.subject()
	payload := map[string]any{"case_id": c.id, "kind": c.kind, "subject_type": st, "subject_id": sid, "reason_code": in.ReasonCode}
	switch act {
	case verifcase.ActApprove:
		payload["level"] = c.targetLevel
		return s.emit(ctx, tx, "kyc.case_approved", c.targetType(), c.id, payload)
	case verifcase.ActReject:
		return s.emit(ctx, tx, "kyc.case_rejected", c.targetType(), c.id, payload)
	case verifcase.ActEscalate:
		return s.emit(ctx, tx, "kyc.case_escalated", c.targetType(), c.id, payload)
	case verifcase.ActSuspend:
		return s.emit(ctx, tx, "kyc.verification_suspended", c.targetType(), c.id, payload)
	case verifcase.ActRevoke:
		return s.emit(ctx, tx, "kyc.verification_revoked", c.targetType(), c.id, payload)
	}
	return nil
}

// setSubject moves the profile (KYC) or KYB organisation; level "" keeps the current level.
func (s *Service) setSubject(ctx context.Context, tx pgx.Tx, c reviewCase, level, status, reason string, expires *time.Time) error {
	if c.kind == KindKYB {
		if level == "" {
			if err := tx.QueryRow(ctx, `SELECT level FROM kyc.kyb_organisations WHERE id = $1`, c.kybOrgID).Scan(&level); err != nil {
				return err
			}
		}
		return s.setOrgLevel(ctx, tx, c.kybOrgID, level, status, reason, c.id, expires)
	}
	if level == "" {
		if err := tx.QueryRow(ctx, `SELECT level FROM kyc.verification_profiles WHERE id = $1`, c.profileID).Scan(&level); err != nil {
			return err
		}
	}
	return s.setProfile(ctx, tx, c.profileID, level, status, reason, c.id, expires)
}

// applyApproval performs the subject-side effects of an approval inside the decision transaction.
func (s *Service) applyApproval(ctx context.Context, tx pgx.Tx, c reviewCase, decidedBy string, expires *time.Time) error {
	staff := decidedBy
	if c.kind == KindKYC {
		if c.identityID == nil {
			return ErrActionNotAllowed
		}
		var dobCT []byte
		var docExpiry *time.Time
		if err := tx.QueryRow(ctx, `SELECT dob_ciphertext, doc_expiry FROM kyc.identities WHERE id = $1`, *c.identityID).Scan(&dobCT, &docExpiry); err != nil {
			return err
		}
		dob, err := s.AEAD.Open(dobCT, aad("identities", *c.identityID, "dob"))
		if err != nil {
			return err
		}
		age := "FAIL"
		if t, err := time.Parse("2006-01-02", string(dob)); err == nil && !t.AddDate(18, 0, 0).After(s.now()) {
			age = "PASS"
		}
		if age != "PASS" {
			return errs.New(errs.Conflict, "UNDERAGE", "The date of birth shows the person is under 18; this case cannot be approved.")
		}
		// supersede the previous approved identity, then activate this one (unique index: one ACTIVE identity
		// per profile and one ACTIVE account holder per document number)
		if _, err := tx.Exec(ctx, `UPDATE kyc.identities SET status = 'SUPERSEDED' WHERE profile_id = $1 AND status = 'ACTIVE' AND id <> $2`,
			c.profileID, *c.identityID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SAVEPOINT activate_identity`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kyc.identities SET status = 'ACTIVE' WHERE id = $1`, *c.identityID); err != nil {
			_, _ = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT activate_identity`)
			if isUnique(err) {
				return errs.New(errs.Conflict, "DUPLICATE_IDENTITY_ACTIVE", "This identity document is already verified on another account. Reject or escalate instead.")
			}
			return err
		}
		if _, err := tx.Exec(ctx, `RELEASE SAVEPOINT activate_identity`); err != nil {
			return err
		}
		expiryResult, expiryReason := "PASS", ""
		if docExpiry == nil {
			expiryResult, expiryReason = "N_A", "NO_EXPIRY_ON_DOCUMENT"
		}
		checks := []struct{ code, result, by, reason string }{
			{"MANUAL_IDENTITY_REVIEW", "PASS", staff, ""}, {"ID_DOCUMENT", "PASS", staff, ""}, {"IDENTITY_DATA", "PASS", staff, ""},
			{"AGE_18_PLUS", "PASS", "", ""}, {"DUPLICATE_IDENTITY", "PASS", staff, ""}, {"DOCUMENT_EXPIRY", expiryResult, "", expiryReason},
			{"SANCTIONS_PEP_SCREEN", "N_A", "", screeningNotPerformed},
		}
		for _, ch := range checks {
			byType, byID := "SYSTEM", any(nil)
			if ch.by != "" {
				byType, byID = "STAFF", ch.by
			}
			if _, err := tx.Exec(ctx, `INSERT INTO kyc.kyc_checks (id, kyc_case_id, check_code, result, rule_version, performed_by_type, performed_by_id, reason_code)
				VALUES ($1, $2, $3, $4, 'v1', $5, $6, $7)`, ids.New(), c.id, ch.code, ch.result, byType, byID, nullable(ch.reason)); err != nil {
				return err
			}
		}
		var cur string
		if err := tx.QueryRow(ctx, `SELECT level FROM kyc.verification_profiles WHERE id = $1`, c.profileID).Scan(&cur); err != nil {
			return err
		}
		lvl := c.targetLevel
		if AtLeast(cur, lvl) && cur != LevelUnverified {
			lvl = cur
		}
		return s.setProfile(ctx, tx, c.profileID, lvl, ProfileActive, "LEVEL_GRANTED", c.id, expires)
	}
	// KYB: the representative must still be an ORG_ADMIN and identity-verified at decision time
	if c.submittedBy == nil {
		return ErrActionNotAllowed
	}
	if ok, err := s.Orgs.IsAdmin(ctx, c.orgID, *c.submittedBy); err != nil {
		return err
	} else if !ok {
		return errs.New(errs.Conflict, "REPRESENTATIVE_NO_LONGER_AUTHORISED", "The submitting representative is no longer an administrator of the organisation.")
	}
	if st, err := s.Level(ctx, *c.submittedBy); err != nil {
		return err
	} else if !AtLeast(st.Level, LevelIdentity) || st.Status != ProfileActive {
		return errs.New(errs.Conflict, "REPRESENTATIVE_NOT_VERIFIED", "The submitting representative is no longer identity-verified.")
	}
	var authorityEvidence string
	err := tx.QueryRow(ctx, `SELECT evidence_record_id FROM kyc.kyc_documents WHERE kyb_case_id = $1 AND removed_at IS NULL
		AND document_type IN ('AUTHORITY_LETTER','BOARD_RESOLUTION') ORDER BY recorded_at DESC LIMIT 1`, c.id).Scan(&authorityEvidence)
	if errors.Is(err, pgx.ErrNoRows) {
		return errs.New(errs.Conflict, "AUTHORITY_EVIDENCE_MISSING", "An authority letter or board resolution is required.")
	}
	if err != nil {
		return err
	}
	now := s.now()
	validTo := now.AddDate(1, 0, 0)
	if expires != nil {
		validTo = *expires
	}
	if _, err := tx.Exec(ctx, `UPDATE kyc.representative_authorities SET revoked_at = $2, revoked_by = $3, revoke_reason = 'SUPERSEDED_BY_NEW_KYB'
		WHERE kyb_organisation_id = $1 AND revoked_at IS NULL`, c.kybOrgID, now, staff); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO kyc.representative_authorities (id, kyb_organisation_id, kyb_case_id, user_id, permissions, evidence_record_id,
		verified_by, valid_from, valid_to) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, ids.New(), c.kybOrgID, c.id, *c.submittedBy,
		[]string{"org.verification.submit", "org.campaign.create", "org.campaign.submit"}, authorityEvidence, staff, now, validTo); err != nil {
		if isCheck(err) {
			return ErrSelfDecision
		}
		return err
	}
	// persons: identities become ACTIVE; beneficial owners determined by the reviewer's approval
	if _, err := tx.Exec(ctx, `UPDATE kyc.identities SET status = 'ACTIVE' WHERE status = 'PENDING' AND id IN
		(SELECT identity_id FROM kyc.organisation_persons WHERE kyb_case_id = $1 AND valid_to IS NULL)`, c.id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO kyc.beneficial_owners (id, kyb_organisation_id, organisation_person_id, kyb_case_id, threshold_limit_key,
		ownership_bp, control_basis, determined_by, determined_at)
		SELECT gen_random_uuid(), p.kyb_organisation_id, p.id, $1, 'kyb.bo_threshold.default', p.ownership_bp,
		       CASE WHEN p.ownership_bp IS NULL THEN 'OTHER' ELSE 'SHAREHOLDING' END, $2, $3
		  FROM kyc.organisation_persons p WHERE p.kyb_case_id = $1 AND p.valid_to IS NULL AND 'BENEFICIAL_OWNER' = ANY (p.roles)`, c.id, staff, now); err != nil {
		return err
	}
	for _, ch := range []struct{ code, result, reason string }{{"ORG_LEGAL_IDENTITY", "PASS", ""}, {"ORG_REGISTRATION", "PASS", ""},
		{"ORG_CONTROLLERS", "PASS", ""}, {"ORG_BENEFICIAL_OWNERS", "PASS", ""}, {"ORG_REPRESENTATIVE_AUTHORITY", "PASS", ""},
		{"ORG_REPRESENTATIVE_IDENTITY", "PASS", ""}, {"ORG_SCREEN_ENTITY", "N_A", screeningNotPerformed}} {
		byType, byID := "STAFF", any(staff)
		if ch.result == "N_A" {
			byType, byID = "SYSTEM", nil
		}
		if _, err := tx.Exec(ctx, `INSERT INTO kyc.kyb_checks (id, kyb_case_id, check_code, result, rule_version, performed_by_type, performed_by_id, reason_code)
			VALUES ($1, $2, $3, $4, 'v1', $5, $6, $7)`, ids.New(), c.id, ch.code, ch.result, byType, byID, nullable(ch.reason)); err != nil {
			return err
		}
	}
	// the address ciphertext is bound (AAD) to the case row: re-encrypt it for the organisation record
	var caseAddr []byte
	if err := tx.QueryRow(ctx, `SELECT registered_address_ciphertext FROM kyc.kyb_cases WHERE id = $1`, c.id).Scan(&caseAddr); err != nil {
		return err
	}
	pt, err := s.AEAD.Open(caseAddr, aad("kyb_cases", c.id, "registered_address"))
	if err != nil {
		return err
	}
	orgAddr, err := s.AEAD.Seal(pt, aad("kyb_organisations", c.kybOrgID, "registered_address"))
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE kyc.kyb_organisations o SET legal_name = c.registered_name, trading_name = c.trading_name,
		registration_number = c.registration_number, registry = c.registry, country_of_registration = c.country_of_registration,
		registered_address_ciphertext = $2, registered_address_key_id = $3,
		version = o.version + 1 FROM kyc.kyb_cases c WHERE c.id = $1 AND o.id = c.kyb_organisation_id`, c.id, orgAddr, s.AEAD.KeyID()); err != nil {
		return err
	}
	return s.setOrgLevel(ctx, tx, c.kybOrgID, c.targetLevel, ProfileActive, "LEVEL_GRANTED", c.id, expires)
}

// ReviewAssignment returns the assigned reviewer of a KYC or KYB case (nil when unassigned).
func (s *Service) ReviewAssignment(ctx context.Context, kind, caseID string) (*string, error) {
	table := "kyc.kyc_cases"
	if kind == KindKYB {
		table = "kyc.kyb_cases"
	}
	var a *string
	err := s.Pool.QueryRow(ctx, `SELECT assigned_to FROM `+table+` WHERE id = $1`, caseID).Scan(&a)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCaseNotFound
	}
	return a, err
}

// ReviewState is what a reviewer needs beside the case view: risk level and the four-eyes state.
type ReviewState struct {
	RiskLevel              string  `json:"risk_level"`
	RequiresSecondApproval bool    `json:"requires_second_approval"`
	PendingOutcome         *string `json:"pending_outcome"`
	PendingDecidedBy       *string `json:"pending_decided_by"`
}

// ReviewStateOf returns the review state of a KYC or KYB case.
func (s *Service) ReviewStateOf(ctx context.Context, kind, caseID string) (ReviewState, error) {
	table := "kyc.kyc_cases"
	if kind == KindKYB {
		table = "kyc.kyb_cases"
	}
	var st ReviewState
	err := s.Pool.QueryRow(ctx, `SELECT risk_level, requires_second_approval, pending_outcome, pending_decided_by FROM `+table+` WHERE id = $1`, caseID).
		Scan(&st.RiskLevel, &st.RequiresSecondApproval, &st.PendingOutcome, &st.PendingDecidedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, ErrCaseNotFound
	}
	return st, err
}
