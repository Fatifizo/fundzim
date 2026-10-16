package operations

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Access reviews and internal SLA targets (brief §19, §58). An access review is a campaign over (staff member, role)
// pairs: its creator (access.review.run) names a different reviewer (access.review.certify) who certifies each pair or
// recommends revocation; a recommendation files an ordinary dual-controlled role REVOKE request (the reviewer is its
// maker, another approver must decide it). Nobody certifies their own access. No review frequency is invented.

// AccessReview is a review campaign.
type AccessReview struct {
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	ScopeRole   *string      `json:"scope_role"`
	Status      string       `json:"status"`
	CreatedBy   string       `json:"created_by"`
	ReviewerID  string       `json:"reviewer_id"`
	DueAt       *time.Time   `json:"due_at"`
	CompletedAt *time.Time   `json:"completed_at"`
	Version     int          `json:"version"`
	CreatedAt   time.Time    `json:"created_at"`
	Items       []ReviewItem `json:"items,omitempty"`
	Pending     int          `json:"pending_items"`
}

// ReviewItem is one (staff, role) pair.
type ReviewItem struct {
	ID              string     `json:"id"`
	StaffUserID     string     `json:"staff_user_id"`
	RoleCode        string     `json:"role_code"`
	Decision        string     `json:"decision"`
	DecidedBy       *string    `json:"decided_by"`
	DecidedAt       *time.Time `json:"decided_at"`
	Reason          *string    `json:"reason"`
	RevokeRequestID *string    `json:"revoke_request_id"`
}

// NewAccessReview starts a review.
type NewAccessReview struct {
	Title      string     `json:"title"`
	ScopeRole  string     `json:"scope_role"` // "" = every role
	ReviewerID string     `json:"reviewer_id"`
	DueAt      *time.Time `json:"due_at"`
}

// StartAccessReview snapshots the current role inventory into review items.
func (s *Service) StartAccessReview(ctx context.Context, staffID string, in NewAccessReview) (AccessReview, error) {
	title, err := text("title", in.Title, 3, 200)
	if err != nil {
		return AccessReview{}, err
	}
	if !ids.Valid(in.ReviewerID) {
		return AccessReview{}, validation("reviewer_id", "INVALID_FORMAT")
	}
	if in.ReviewerID == staffID {
		return AccessReview{}, ErrSelf
	}
	if s.Perms != nil {
		ps, err := s.Perms.EffectivePermissions(ctx, in.ReviewerID)
		if err != nil {
			return AccessReview{}, err
		}
		if !contains(ps, PermReviewCertify) {
			return AccessReview{}, ErrAssigneeDenied
		}
	}
	if s.StaffRoles == nil {
		return AccessReview{}, errors.New("operations: staff roles not wired")
	}
	pairs, err := s.StaffRoles.ActiveRoleAssignments(ctx, in.ScopeRole)
	if err != nil {
		return AccessReview{}, err
	}
	if len(pairs) == 0 {
		return AccessReview{}, validation("scope_role", "NO_ASSIGNMENTS")
	}
	id := ids.New()
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var scope any
		if in.ScopeRole != "" {
			scope = in.ScopeRole
		}
		if _, err := tx.Exec(ctx, `INSERT INTO app.ops_access_reviews (id, title, scope_role, status, created_by, reviewer_id, due_at)
			VALUES ($1, $2, $3, 'OPEN', $4, $5, $6)`, id, title, scope, staffID, in.ReviewerID, in.DueAt); err != nil {
			return mapDB(err)
		}
		for _, p := range pairs {
			if _, err := tx.Exec(ctx, `INSERT INTO app.ops_access_review_items (id, review_id, staff_user_id, role_code) VALUES ($1, $2, $3, $4)
				ON CONFLICT DO NOTHING`, ids.New(), id, p[0], p[1]); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, audit.Event{Action: "ops.access_review.started", TargetType: "ops_access_review", TargetID: id,
			Metadata: map[string]any{"items": len(pairs), "reviewer_id": in.ReviewerID, "scope_role": in.ScopeRole}})
	})
	if err != nil {
		return AccessReview{}, err
	}
	return s.AccessReview(ctx, id)
}

// AccessReview returns a review with its items.
func (s *Service) AccessReview(ctx context.Context, id string) (AccessReview, error) {
	if !ids.Valid(id) {
		return AccessReview{}, ErrNotFound
	}
	var r AccessReview
	err := s.Pool.QueryRow(ctx, `SELECT id::text, title, scope_role, status, created_by::text, reviewer_id::text, due_at, completed_at, version, created_at
		FROM app.ops_access_reviews WHERE id = $1`, id).Scan(&r.ID, &r.Title, &r.ScopeRole, &r.Status, &r.CreatedBy, &r.ReviewerID, &r.DueAt, &r.CompletedAt,
		&r.Version, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AccessReview{}, ErrNotFound
	}
	if err != nil {
		return AccessReview{}, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT id::text, staff_user_id::text, role_code, decision, decided_by::text, decided_at, reason, revoke_request_id::text
		FROM app.ops_access_review_items WHERE review_id = $1 ORDER BY role_code, staff_user_id`, id)
	if err != nil {
		return AccessReview{}, err
	}
	defer rows.Close()
	r.Items = []ReviewItem{}
	for rows.Next() {
		var it ReviewItem
		if err := rows.Scan(&it.ID, &it.StaffUserID, &it.RoleCode, &it.Decision, &it.DecidedBy, &it.DecidedAt, &it.Reason, &it.RevokeRequestID); err != nil {
			return AccessReview{}, err
		}
		if it.Decision == "PENDING" {
			r.Pending++
		}
		r.Items = append(r.Items, it)
	}
	return r, rows.Err()
}

// AccessReviews lists reviews (newest first).
func (s *Service) AccessReviews(ctx context.Context) ([]AccessReview, error) {
	rows, err := s.Pool.Query(ctx, `SELECT r.id::text, r.title, r.scope_role, r.status, r.created_by::text, r.reviewer_id::text, r.due_at, r.completed_at,
		r.version, r.created_at, (SELECT count(*) FROM app.ops_access_review_items i WHERE i.review_id = r.id AND i.decision = 'PENDING')
		FROM app.ops_access_reviews r ORDER BY r.created_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AccessReview{}
	for rows.Next() {
		var r AccessReview
		if err := rows.Scan(&r.ID, &r.Title, &r.ScopeRole, &r.Status, &r.CreatedBy, &r.ReviewerID, &r.DueAt, &r.CompletedAt, &r.Version, &r.CreatedAt,
			&r.Pending); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DecideReviewItem certifies an item or recommends revocation (the review's reviewer only; never their own access or a
// linked account's). A revocation recommendation files a role REVOKE request (dual control) in the same flow.
func (s *Service) DecideReviewItem(ctx context.Context, v Viewer, reviewID, itemID, decision, reason string) (AccessReview, error) {
	if decision != "CERTIFIED" && decision != "REVOKE_RECOMMENDED" {
		return AccessReview{}, validation("decision", "INVALID_VALUE")
	}
	r, err := text("reason", reason, 5, 2000)
	if err != nil {
		return AccessReview{}, err
	}
	if !ids.Valid(reviewID) || !ids.Valid(itemID) {
		return AccessReview{}, ErrNotFound
	}
	var staffUser, role string
	now := s.now()
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var reviewer, status, cur string
		if err := tx.QueryRow(ctx, `SELECT r.reviewer_id::text, r.status, i.staff_user_id::text, i.role_code, i.decision
			FROM app.ops_access_review_items i JOIN app.ops_access_reviews r ON r.id = i.review_id WHERE r.id = $1 AND i.id = $2 FOR UPDATE OF i`,
			reviewID, itemID).Scan(&reviewer, &status, &staffUser, &role, &cur); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if reviewer != v.StaffID {
			return ErrNotReviewer
		}
		if status != "OPEN" || cur != "PENDING" {
			return ErrState
		}
		if v.Identities[staffUser] {
			return ErrConflict
		}
		if _, err := tx.Exec(ctx, `UPDATE app.ops_access_review_items SET decision = $2, decided_by = $3, decided_at = $4, reason = $5 WHERE id = $1`,
			itemID, decision, v.StaffID, now, r); err != nil {
			return mapDB(err)
		}
		return audit.Record(ctx, tx, audit.Event{Action: "ops.access_review.item_decided", TargetType: "ops_access_review", TargetID: reviewID,
			Reason: r, Metadata: map[string]any{"item_id": itemID, "staff_user_id": staffUser, "role": role, "decision": decision}})
	})
	if err != nil {
		return AccessReview{}, err
	}
	if decision == "REVOKE_RECOMMENDED" && s.Revoker != nil {
		reqID, err := s.Revoker.RequestRoleRevocation(ctx, v.StaffID, staffUser, role, "Access review "+reviewID+": "+r)
		if err == nil {
			_, err = s.Pool.Exec(ctx, `UPDATE app.ops_access_review_items SET revoke_request_id = $2 WHERE id = $1`, itemID, reqID)
		}
		if err != nil && s.Logger != nil {
			// the recommendation is recorded; the revoke request can be filed again from the staff page
			s.Logger.Warn("operations: access review revocation request not filed", "item", itemID, "error", err.Error())
		}
	}
	return s.AccessReview(ctx, reviewID)
}

// CompleteAccessReview closes a review whose items are all decided (its creator or reviewer).
func (s *Service) CompleteAccessReview(ctx context.Context, staffID, reviewID string) (AccessReview, error) {
	if !ids.Valid(reviewID) {
		return AccessReview{}, ErrNotFound
	}
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var creator, reviewer, status string
		var pending int
		if err := tx.QueryRow(ctx, `SELECT created_by::text, reviewer_id::text, status, (SELECT count(*) FROM app.ops_access_review_items
			WHERE review_id = $1 AND decision = 'PENDING') FROM app.ops_access_reviews WHERE id = $1 FOR UPDATE`, reviewID).Scan(&creator, &reviewer, &status, &pending); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if staffID != creator && staffID != reviewer {
			return ErrNotFound
		}
		if status != "OPEN" || pending > 0 {
			return ErrState
		}
		if _, err := tx.Exec(ctx, `UPDATE app.ops_access_reviews SET status = 'COMPLETED', completed_at = $2, version = version + 1 WHERE id = $1`,
			reviewID, s.now()); err != nil {
			return mapDB(err)
		}
		return audit.Record(ctx, tx, audit.Event{Action: "ops.access_review.completed", TargetType: "ops_access_review", TargetID: reviewID})
	})
	if err != nil {
		return AccessReview{}, err
	}
	return s.AccessReview(ctx, reviewID)
}

// ---- SLA targets -------------------------------------------------------------------------------------------------------

// SLATarget is an internal operational target.
type SLATarget struct {
	ID             string     `json:"id"`
	MetricKey      string     `json:"metric_key"`
	TargetSeconds  int        `json:"target_seconds"`
	Status         string     `json:"status"`
	Reason         string     `json:"reason"`
	ProposedBy     string     `json:"proposed_by"`
	ProposedAt     time.Time  `json:"proposed_at"`
	DecidedBy      *string    `json:"decided_by"`
	DecidedAt      *time.Time `json:"decided_at"`
	DecisionReason *string    `json:"decision_reason"`
	Notice         string     `json:"notice"`
}

// SLANotice accompanies every target.
const SLANotice = "An internal operational target set by FundZim staff — not a contractual or regulatory deadline."

// SLAMetrics are the metrics a target may be set for.
var SLAMetrics = []string{"VERIFICATION_REVIEW_TURNAROUND", "CAMPAIGN_MODERATION_TURNAROUND", "SUPPORT_FIRST_RESPONSE", "SUPPORT_RESOLUTION",
	"RISK_INVESTIGATION_AGE", "APPROVAL_PROCESSING", "EXCEPTION_RESOLUTION", "INCIDENT_ACKNOWLEDGEMENT"}

// ProposeSLATarget proposes a target (maker).
func (s *Service) ProposeSLATarget(ctx context.Context, staffID, metric string, seconds int, reason string) (SLATarget, error) {
	if !contains(SLAMetrics, metric) {
		return SLATarget{}, validation("metric_key", "INVALID_VALUE")
	}
	if seconds < 60 || seconds > 31536000 {
		return SLATarget{}, validation("target_seconds", "OUT_OF_RANGE")
	}
	r, err := text("reason", reason, 5, 2000)
	if err != nil {
		return SLATarget{}, err
	}
	id := ids.New()
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO app.ops_sla_targets (id, metric_key, target_seconds, status, reason, proposed_by, proposed_at)
			VALUES ($1, $2, $3, 'PROPOSED', $4, $5, $6)`, id, metric, seconds, r, staffID, s.now()); err != nil {
			return mapDB(err)
		}
		return audit.Record(ctx, tx, audit.Event{Action: "ops.sla_target.proposed", TargetType: "ops_sla_target", TargetID: id,
			Metadata: map[string]any{"metric_key": metric, "target_seconds": seconds}})
	})
	if err != nil {
		return SLATarget{}, err
	}
	return s.slaTarget(ctx, id)
}

// DecideSLATarget approves (the previous ACTIVE target is superseded) or rejects a proposal (checker; never the
// proposer or a linked account — Go and database).
func (s *Service) DecideSLATarget(ctx context.Context, v Viewer, id string, approve bool, reason string) (SLATarget, error) {
	r, err := text("reason", reason, 5, 2000)
	if err != nil {
		return SLATarget{}, err
	}
	if !ids.Valid(id) {
		return SLATarget{}, ErrNotFound
	}
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var metric, status, proposer string
		if err := tx.QueryRow(ctx, `SELECT metric_key, status, proposed_by::text FROM app.ops_sla_targets WHERE id = $1 FOR UPDATE`, id).
			Scan(&metric, &status, &proposer); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if status != "PROPOSED" {
			return ErrState
		}
		if v.Identities[proposer] {
			return ErrSelf
		}
		now := s.now()
		to := "REJECTED"
		if approve {
			to = "ACTIVE"
			if _, err := tx.Exec(ctx, `UPDATE app.ops_sla_targets SET status = 'SUPERSEDED', version = version + 1 WHERE metric_key = $1 AND status = 'ACTIVE'`,
				metric); err != nil {
				return mapDB(err)
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE app.ops_sla_targets SET status = $2, decided_by = $3, decided_at = $4, decision_reason = $5, version = version + 1
			WHERE id = $1`, id, to, v.StaffID, now, r); err != nil {
			return mapDB(err)
		}
		return audit.Record(ctx, tx, audit.Event{Action: "ops.sla_target." + map[bool]string{true: "approved", false: "rejected"}[approve],
			TargetType: "ops_sla_target", TargetID: id, Reason: r})
	})
	if err != nil {
		return SLATarget{}, err
	}
	return s.slaTarget(ctx, id)
}

func (s *Service) slaTarget(ctx context.Context, id string) (SLATarget, error) {
	var t SLATarget
	err := s.Pool.QueryRow(ctx, `SELECT id::text, metric_key, target_seconds, status, reason, proposed_by::text, proposed_at, decided_by::text, decided_at,
		decision_reason FROM app.ops_sla_targets WHERE id = $1`, id).Scan(&t.ID, &t.MetricKey, &t.TargetSeconds, &t.Status, &t.Reason, &t.ProposedBy,
		&t.ProposedAt, &t.DecidedBy, &t.DecidedAt, &t.DecisionReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return SLATarget{}, ErrNotFound
	}
	t.Notice = SLANotice
	return t, err
}

// SLATargets lists current and pending targets.
func (s *Service) SLATargets(ctx context.Context) ([]SLATarget, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id::text FROM app.ops_sla_targets WHERE status IN ('ACTIVE','PROPOSED') ORDER BY metric_key, status`)
	if err != nil {
		return nil, err
	}
	var idl []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		idl = append(idl, id)
	}
	rows.Close()
	out := []SLATarget{}
	for _, id := range idl {
		t, err := s.slaTarget(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}
