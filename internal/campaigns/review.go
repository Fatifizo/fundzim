package campaigns

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/kyc"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
)

// Decision is a staff decision body (interface-contracts §6).
type Decision struct {
	AssigneeID  string `json:"assignee_id"`
	ReasonCode  string `json:"reason_code"`
	Note        string `json:"note"`
	UserMessage string `json:"user_message"`
}

func (d Decision) validate(needReason bool) (Decision, error) {
	var det []errs.Detail
	if needReason && !kyc.ValidReasonCode(d.ReasonCode) {
		det = append(det, errs.Detail{Field: "reason_code", Code: "INVALID_FORMAT"})
	}
	note, code := CleanText(d.Note, 3, 5000, true)
	if code != "" {
		det = append(det, errs.Detail{Field: "note", Code: code})
	}
	msg := ""
	if d.UserMessage != "" {
		if msg, code = CleanText(d.UserMessage, 3, 2000, true); code != "" {
			det = append(det, errs.Detail{Field: "user_message", Code: code})
		}
	}
	if len(det) > 0 {
		return d, httpx.Validation(det...)
	}
	d.Note, d.UserMessage = note, msg
	return d, nil
}

// reviewRow is the open review of a campaign.
type reviewRow struct {
	ID, CampaignID, VersionID, PolicyVersion, RiskTier, Kind, Status string
	RequiresSecond, Escalated                                        bool
	AssignedTo, PendingOutcome, PendingDecidedBy                     *string
}

func (s *Service) lockOpenReview(ctx context.Context, tx pgx.Tx, campaignID string) (reviewRow, error) {
	var r reviewRow
	err := tx.QueryRow(ctx, `SELECT id, campaign_id, campaign_version_id, policy_version, risk_tier, kind, status, requires_second_approval, escalated,
		assigned_to, pending_outcome, pending_decided_by FROM app.campaign_reviews
		WHERE campaign_id = $1 AND status IN ('QUEUED','ASSIGNED','IN_REVIEW') FOR UPDATE`, campaignID).
		Scan(&r.ID, &r.CampaignID, &r.VersionID, &r.PolicyVersion, &r.RiskTier, &r.Kind, &r.Status, &r.RequiresSecond, &r.Escalated,
			&r.AssignedTo, &r.PendingOutcome, &r.PendingDecidedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, errs.New(errs.Conflict, "NO_OPEN_REVIEW", "There is no open review for this campaign.")
	}
	return r, err
}

// conflicted reports whether a staff member (or any account linked to them) owns, created or belongs to the
// organisation of the campaign. The database trigger enforces the same rule (ADR-037 §4).
func (s *Service) conflicted(ctx context.Context, c campaignRow, staffID string) (bool, error) {
	actors, err := s.KYC.ActorIdentities(ctx, staffID)
	if err != nil {
		return false, err
	}
	for _, a := range actors {
		if a == c.CreatedBy || (c.OwnerUserID != nil && *c.OwnerUserID == a) {
			return true, nil
		}
		if c.OwnerOrgID != nil {
			if role, err := s.Orgs.MemberRole(ctx, *c.OwnerOrgID, a); err != nil || role != "" {
				return role != "", err
			}
		}
	}
	return false, nil
}

// staffLock loads and locks a campaign for a staff action and refuses conflicted staff.
func (s *Service) staffLock(ctx context.Context, tx pgx.Tx, staffID, id string) (campaignRow, error) {
	c, err := s.loadRow(ctx, tx, id, true)
	if err != nil {
		return c, err
	}
	if bad, err := s.conflicted(ctx, c, staffID); err != nil {
		return c, err
	} else if bad {
		return c, ErrSelfDecision
	}
	return c, nil
}

func (s *Service) reviewAudit(ctx context.Context, tx pgx.Tx, staffID, campaignID, action string, md map[string]any) error {
	return audit.Record(ctx, tx, audit.Event{Action: "campaign." + action, ActorType: "staff", ActorID: staffID, TargetType: "campaign",
		TargetID: campaignID, Metadata: md})
}

// Assign assigns the open review (to the caller by default). Only staff who can review campaigns are eligible.
func (s *Service) Assign(ctx context.Context, staffID, id string, d Decision) error {
	assignee := d.AssigneeID
	if assignee == "" {
		assignee = staffID
	}
	if assignee != staffID {
		ok := false
		if s.StaffCan != nil && validID(assignee) {
			var err error
			if ok, err = s.StaffCan(ctx, assignee, "campaign.review"); err != nil {
				return err
			}
		}
		if !ok {
			return errs.New(errs.Unprocessable, "ASSIGNEE_NOT_ELIGIBLE", "That person cannot review campaigns.")
		}
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.staffLock(ctx, tx, assignee, id)
		if err != nil {
			return err
		}
		r, err := s.lockOpenReview(ctx, tx, c.ID)
		if err != nil {
			return err
		}
		if r.PendingOutcome != nil {
			return errs.New(errs.Conflict, "SECOND_APPROVAL_PENDING", "The review is waiting for a second approval.")
		}
		if _, err := tx.Exec(ctx, `UPDATE app.campaign_reviews SET status = 'ASSIGNED', assigned_to = $2, assigned_at = $3, started_at = NULL
			WHERE id = $1`, r.ID, assignee, s.now()); err != nil {
			return err
		}
		return s.reviewAudit(ctx, tx, staffID, c.ID, "review_assigned", map[string]any{"review_id": r.ID, "assignee": assignee})
	})
}

// StartReview starts the assigned review; a first submission moves SUBMITTED → UNDER_REVIEW.
func (s *Service) StartReview(ctx context.Context, staffID, id string) error {
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, r, err := s.assigned(ctx, tx, staffID, id)
		if err != nil {
			return err
		}
		if r.Status == "IN_REVIEW" {
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE app.campaign_reviews SET status = 'IN_REVIEW', started_at = $2 WHERE id = $1`, r.ID, s.now()); err != nil {
			return err
		}
		if c.Status == StatusSubmitted {
			return s.apply(ctx, tx, &c, staffActor(staffID), change{to: StatusUnderReview, event: "REVIEW_STARTED", action: "review_started",
				reasonCode: "REVIEW_STARTED", payload: map[string]any{"review_id": r.ID}})
		}
		return s.reviewAudit(ctx, tx, staffID, c.ID, "review_started", map[string]any{"review_id": r.ID})
	})
}

// assigned locks the campaign and its open review and checks the caller is the assignee.
func (s *Service) assigned(ctx context.Context, tx pgx.Tx, staffID, id string) (campaignRow, reviewRow, error) {
	c, err := s.staffLock(ctx, tx, staffID, id)
	if err != nil {
		return c, reviewRow{}, err
	}
	r, err := s.lockOpenReview(ctx, tx, c.ID)
	if err != nil {
		return c, r, err
	}
	if r.AssignedTo == nil || *r.AssignedTo != staffID {
		return c, r, ErrNotAssigned
	}
	return c, r, nil
}

func (s *Service) inReview(ctx context.Context, tx pgx.Tx, staffID, id string) (campaignRow, reviewRow, error) {
	c, r, err := s.assigned(ctx, tx, staffID, id)
	if err == nil && r.Status != "IN_REVIEW" {
		err = errs.New(errs.Conflict, "REVIEW_NOT_STARTED", "Start the review first.")
	}
	return c, r, err
}

func (s *Service) decideReview(ctx context.Context, tx pgx.Tx, r reviewRow, outcome, staffID, second string, d Decision) error {
	var sec any
	if second != "" {
		sec = second
	}
	_, err := tx.Exec(ctx, `UPDATE app.campaign_reviews SET status = 'DECIDED', outcome = $2, decided_by = $3, second_approver = $4, decided_at = $5,
		reason_code = $6, note = $7, user_message = $8 WHERE id = $1`, r.ID, outcome, staffID, sec, s.now(), d.ReasonCode, d.Note, nullable(d.UserMessage))
	return err
}

// RequestChanges returns a submission to the owner (UNDER_REVIEW → CHANGES_REQUESTED) or declines a re-review.
func (s *Service) RequestChanges(ctx context.Context, staffID, id string, d Decision) error {
	d, err := d.validate(true)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, r, err := s.inReview(ctx, tx, staffID, id)
		if err != nil {
			return err
		}
		if err := s.decideReview(ctx, tx, r, "REQUEST_CHANGES", staffID, "", d); err != nil {
			return err
		}
		ch := change{event: "CHANGES_REQUESTED", action: "changes_requested", outboxType: "campaigns.changes_requested", reasonCode: d.ReasonCode,
			note: d.Note, set: map[string]any{"pending_version_id": nil}, payload: map[string]any{"review_id": r.ID}}
		if r.Kind != "RE_REVIEW" {
			ch.to = StatusChangesRequested
		} else {
			ch.set["re_review_required"], ch.set["re_review_reason"] = false, nil
		}
		return s.apply(ctx, tx, &c, staffActor(staffID), ch)
	})
}

// Approve approves the review. Tiers whose policy requires four eyes record a pending approval (202) that a
// different staff member with campaign.decide.high completes (SecondApprove).
func (s *Service) Approve(ctx context.Context, staffID, id string, d Decision) (bool, error) {
	d, err := d.validate(true)
	if err != nil {
		return false, err
	}
	pending := false
	var refused *Result
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, r, err := s.inReview(ctx, tx, staffID, id)
		if err != nil {
			return err
		}
		if r.PendingOutcome != nil {
			return errs.New(errs.Conflict, "SECOND_APPROVAL_PENDING", "The review is waiting for a second approval.")
		}
		ev, err := s.evaluateVersion(ctx, tx, ActApprove, c, r.VersionID)
		if err != nil {
			return err
		}
		if !ev.Allowed {
			refused = &ev
			return notEligible(ev)
		}
		if err := s.recordEvaluation(ctx, tx, c.ID, staffID, ev); err != nil {
			return err
		}
		if r.RequiresSecond {
			pending = true
			if _, err := tx.Exec(ctx, `UPDATE app.campaign_reviews SET pending_outcome = 'APPROVE', pending_decided_by = $2, pending_decided_at = $3,
				pending_reason_code = $4, pending_note = $5 WHERE id = $1`, r.ID, staffID, s.now(), d.ReasonCode, d.Note); err != nil {
				return err
			}
			return s.reviewAudit(ctx, tx, staffID, c.ID, "first_approval", map[string]any{"review_id": r.ID, "reason_code": d.ReasonCode})
		}
		return s.finishApproval(ctx, tx, &c, r, staffID, "", d)
	})
	if refused != nil {
		s.recordRefusal(ctx, id, staffID, *refused)
	}
	return pending, err
}

// SecondApprove completes a four-eyes approval; the second approver differs from the first (DB CHECK too).
func (s *Service) SecondApprove(ctx context.Context, staffID, id string, d Decision) error {
	d, err := d.validate(false)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.staffLock(ctx, tx, staffID, id)
		if err != nil {
			return err
		}
		r, err := s.lockOpenReview(ctx, tx, c.ID)
		if err != nil {
			return err
		}
		if r.PendingOutcome == nil {
			return errs.New(errs.Conflict, "NO_PENDING_APPROVAL", "There is no first approval waiting.")
		}
		if *r.PendingDecidedBy == staffID {
			return errs.New(errs.Forbidden, "SECOND_APPROVER_MUST_DIFFER", "A different reviewer must give the second approval.")
		}
		ev, err := s.evaluateVersion(ctx, tx, ActApprove, c, r.VersionID)
		if err != nil {
			return err
		}
		if !ev.Allowed {
			return notEligible(ev)
		}
		if err := s.recordEvaluation(ctx, tx, c.ID, staffID, ev); err != nil {
			return err
		}
		var reason, note string
		if err := tx.QueryRow(ctx, `SELECT pending_reason_code, pending_note FROM app.campaign_reviews WHERE id = $1`, r.ID).Scan(&reason, &note); err != nil {
			return err
		}
		first := Decision{ReasonCode: reason, Note: note + "\n\nSecond approval: " + d.Note}
		return s.finishApproval(ctx, tx, &c, r, *r.PendingDecidedBy, staffID, first)
	})
}

func (s *Service) finishApproval(ctx context.Context, tx pgx.Tx, c *campaignRow, r reviewRow, decidedBy, second string, d Decision) error {
	if err := s.decideReview(ctx, tx, r, "APPROVE", decidedBy, second, d); err != nil {
		return err
	}
	set := map[string]any{"approved_version_id": r.VersionID, "pending_version_id": nil}
	ch := change{event: "APPROVED", action: "approved", outboxType: "campaigns.approved", reasonCode: d.ReasonCode, note: d.Note, set: set,
		payload: map[string]any{"review_id": r.ID, "version_id": r.VersionID, "second_approver": second != ""}}
	if r.Kind == "RE_REVIEW" {
		set["re_review_required"], set["re_review_reason"] = false, nil
	} else {
		ch.to = StatusApproved
		set["approved_at"] = s.now()
	}
	actorID := decidedBy
	if second != "" {
		actorID = second
	}
	return s.apply(ctx, tx, c, staffActor(actorID), ch)
}

// evaluateVersion evaluates an action against the owner side and the reviewed snapshot (what the reviewer saw).
func (s *Service) evaluateVersion(ctx context.Context, tx pgx.Tx, action string, c campaignRow, versionID string) (Result, error) {
	snap := c
	if err := tx.QueryRow(ctx, `SELECT title, summary, story, category_code, goal_amount_minor, goal_currency FROM app.campaign_versions WHERE id = $1`,
		versionID).Scan(&snap.Title, &snap.Summary, &snap.Story, &snap.Category, &snap.GoalAmountMinor, &snap.GoalCurrency); err != nil {
		return Result{}, err
	}
	return s.Evaluate(ctx, tx, action, "", snap)
}

// Reject rejects a submission (UNDER_REVIEW → REJECTED) or a re-review (the live version stays).
func (s *Service) Reject(ctx context.Context, staffID, id string, d Decision) error {
	d, err := d.validate(true)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, r, err := s.inReview(ctx, tx, staffID, id)
		if err != nil {
			return err
		}
		if err := s.decideReview(ctx, tx, r, "REJECT", staffID, "", d); err != nil {
			return err
		}
		ch := change{event: "REJECTED", action: "rejected", outboxType: "campaigns.rejected", reasonCode: d.ReasonCode, note: d.Note,
			set: map[string]any{"pending_version_id": nil}, payload: map[string]any{"review_id": r.ID}}
		if r.Kind == "RE_REVIEW" {
			ch.set["re_review_required"], ch.set["re_review_reason"] = false, nil
		} else {
			ch.to = StatusRejected
		}
		return s.apply(ctx, tx, &c, staffActor(staffID), ch)
	})
}

// Escalate flags the open review and opens a compliance case through the outbox; the campaign stays in review.
func (s *Service) Escalate(ctx context.Context, staffID, id string, d Decision) error {
	d, err := d.validate(true)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, r, err := s.assigned(ctx, tx, staffID, id)
		if err != nil {
			return err
		}
		if r.Escalated {
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE app.campaign_reviews SET escalated = true, escalated_at = $2, escalated_by = $3, escalation_reason = $4
			WHERE id = $1`, r.ID, s.now(), staffID, d.ReasonCode); err != nil {
			return err
		}
		if err := s.reviewAudit(ctx, tx, staffID, c.ID, "review_escalated", map[string]any{"review_id": r.ID, "reason_code": d.ReasonCode}); err != nil {
			return err
		}
		return s.emit(ctx, tx, &c, "campaigns.review_escalated", map[string]any{"review_id": r.ID, "reason_code": d.ReasonCode})
	})
}

// Reopen returns a REJECTED campaign to review (REJECTED → UNDER_REVIEW) with the caller as assignee.
func (s *Service) Reopen(ctx context.Context, staffID, id string, d Decision) error {
	d, err := d.validate(true)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.staffLock(ctx, tx, staffID, id)
		if err != nil {
			return err
		}
		if c.Status != StatusRejected {
			return ErrInvalidStatus
		}
		var vid string
		if err := tx.QueryRow(ctx, `SELECT campaign_version_id FROM app.campaign_reviews WHERE campaign_id = $1 AND outcome = 'REJECT'
			ORDER BY decided_at DESC LIMIT 1`, c.ID).Scan(&vid); err != nil {
			return err
		}
		rid, err := s.openReview(ctx, tx, c, vid, "REOPEN")
		if err != nil {
			return err
		}
		now := s.now()
		if _, err := tx.Exec(ctx, `UPDATE app.campaign_reviews SET status = 'ASSIGNED', assigned_to = $2, assigned_at = $3 WHERE id = $1`, rid, staffID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app.campaign_reviews SET status = 'IN_REVIEW', started_at = $2 WHERE id = $1`, rid, now); err != nil {
			return err
		}
		return s.apply(ctx, tx, &c, staffActor(staffID), change{to: StatusUnderReview, event: "REOPENED", action: "reopened",
			outboxType: "campaigns.reopened", reasonCode: d.ReasonCode, note: d.Note, set: map[string]any{"pending_version_id": vid},
			payload: map[string]any{"review_id": rid}})
	})
}

// StaffAction covers suspend, reactivate, publish, cancel and complete by staff.
func (s *Service) StaffAction(ctx context.Context, staffID, id, action string, d Decision) error {
	d, err := d.validate(true)
	if err != nil {
		return err
	}
	var refused *Result
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.staffLock(ctx, tx, staffID, id)
		if err != nil {
			return err
		}
		a := staffActor(staffID)
		switch action {
		case "suspend":
			return s.suspend(ctx, tx, &c, a, d.ReasonCode, d.Note)
		case "reactivate":
			if c.Status != StatusSuspended {
				return ErrInvalidStatus
			}
			ev, err := s.Evaluate(ctx, tx, ActReactivate, "", c)
			if err != nil {
				return err
			}
			if !ev.Allowed {
				refused = &ev
				return notEligible(ev)
			}
			if err := s.recordEvaluation(ctx, tx, c.ID, staffID, ev); err != nil {
				return err
			}
			to := StatusActive
			if c.PublishedAt == nil {
				to = StatusApproved // never published: back to APPROVED, publication stays a separate decision
			}
			return s.apply(ctx, tx, &c, a, change{to: to, event: "REACTIVATED", action: "reactivated", outboxType: "campaigns.reactivated",
				reasonCode: d.ReasonCode, note: d.Note, set: map[string]any{"suspended_at": nil}})
		case "publish":
			return s.publish(ctx, tx, &c, a, &refused)
		case "cancel":
			if c.Status != StatusApproved && c.Status != StatusActive && c.Status != StatusPaused && c.Status != StatusSuspended {
				return ErrInvalidStatus
			}
			return s.apply(ctx, tx, &c, a, change{to: StatusCancelled, event: "CANCELLED", action: "cancelled", outboxType: "campaigns.cancelled",
				reasonCode: d.ReasonCode, note: d.Note, set: map[string]any{"cancelled_at": s.now(), "suspended_at": nil, "paused_at": nil}})
		case "complete":
			if c.Status != StatusActive && c.Status != StatusPaused {
				return ErrInvalidStatus
			}
			return s.apply(ctx, tx, &c, a, change{to: StatusCompleted, event: "COMPLETED", action: "completed", outboxType: "campaigns.completed",
				reasonCode: d.ReasonCode, note: d.Note, set: map[string]any{"completed_at": s.now(), "completion_reason": "ADMIN_COMPLETED", "paused_at": nil}})
		case "archive":
			if c.Status != StatusCompleted && c.Status != StatusCancelled && c.Status != StatusRejected {
				return ErrInvalidStatus
			}
			return s.apply(ctx, tx, &c, a, change{to: StatusArchived, event: "ARCHIVED", action: "archived", outboxType: "campaigns.archived",
				reasonCode: d.ReasonCode, note: d.Note, set: map[string]any{"archived_at": s.now()}})
		}
		return errs.New(errs.NotFound, errs.CodeRouteNotFound, "No such route.")
	})
	if refused != nil {
		s.recordRefusal(ctx, id, staffID, *refused)
	}
	return err
}

func (s *Service) suspend(ctx context.Context, tx pgx.Tx, c *campaignRow, a actor, reason, note string) error {
	if c.Status != StatusApproved && c.Status != StatusActive && c.Status != StatusPaused {
		return ErrInvalidStatus
	}
	return s.apply(ctx, tx, c, a, change{to: StatusSuspended, event: "SUSPENDED", action: "suspended", outboxType: "campaigns.suspended",
		reasonCode: reason, note: note, set: map[string]any{"suspended_at": s.now(), "paused_at": nil}})
}

// SuspendForRestriction suspends a campaign because of a compliance restriction (system actor; ADR-036 §6).
// Idempotent: campaigns not in APPROVED, ACTIVE or PAUSED are left alone.
func (s *Service) SuspendForRestriction(ctx context.Context, campaignID string) error {
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.loadRow(ctx, tx, campaignID, true)
		if err != nil {
			return err
		}
		if c.Status != StatusApproved && c.Status != StatusActive && c.Status != StatusPaused {
			return nil
		}
		return s.suspend(ctx, tx, &c, systemActor, "COMPLIANCE_RESTRICTION", "")
	})
}

// CampaignsForSubject lists the campaigns affected by a restriction on a subject.
func (s *Service) CampaignsForSubject(ctx context.Context, subjectType, subjectID string) ([]string, error) {
	var sql string
	switch subjectType {
	case "USER":
		sql = `SELECT id FROM app.campaigns WHERE owner_user_id = $1 OR created_by = $1`
	case "ORGANISATION":
		sql = `SELECT id FROM app.campaigns WHERE owner_organisation_id = $1`
	case "CAMPAIGN":
		sql = `SELECT id FROM app.campaigns WHERE id = $1`
	case "BENEFICIARY":
		sql = `SELECT campaign_id FROM app.campaign_beneficiaries WHERE beneficiary_id = $1 AND unlinked_at IS NULL`
	default:
		return nil, nil
	}
	rows, err := s.Pool.Query(ctx, sql, subjectID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
