package campaigns

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/users"
)

// OwnerView is the owner's view: the working copy, lifecycle, current review feedback (never reviewer
// identities or internal notes), beneficiary link and eligibility for the next actions.
type OwnerView struct {
	ID                string           `json:"id"`
	Slug              string           `json:"slug"`
	PublicCode        string           `json:"public_code"`
	Owner             map[string]any   `json:"owner"`
	Title             string           `json:"title"`
	Summary           string           `json:"summary"`
	Story             string           `json:"story"`
	Category          string           `json:"category"`
	Goal              Money            `json:"goal"`
	Status            string           `json:"status"`
	Visibility        string           `json:"visibility"`
	RiskTier          string           `json:"-"`
	ReReviewRequired  bool             `json:"re_review_pending"`
	Beneficiary       *beneficiaryLink `json:"beneficiary"`
	Feedback          *Feedback        `json:"feedback"`
	CompletionReason  *string          `json:"completion_reason"`
	CreatedAt         time.Time        `json:"created_at"`
	SubmittedAt       *time.Time       `json:"submitted_at"`
	ApprovedAt        *time.Time       `json:"approved_at"`
	PublishedAt       *time.Time       `json:"published_at"`
	PausedAt          *time.Time       `json:"paused_at"`
	SuspendedAt       *time.Time       `json:"suspended_at"`
	CompletedAt       *time.Time       `json:"completed_at"`
	CancelledAt       *time.Time       `json:"cancelled_at"`
	ArchivedAt        *time.Time       `json:"archived_at"`
	ResubmissionCount int              `json:"resubmission_count"`
	Donations         map[string]any   `json:"donations"`
	Version           int              `json:"version"`
}

// Feedback is the latest decided review as the owner may see it.
type Feedback struct {
	Outcome     string    `json:"outcome"`
	ReasonCode  string    `json:"reason_code"`
	UserMessage *string   `json:"user_message"`
	DecidedAt   time.Time `json:"decided_at"`
}

// donationsNotAvailable is shown wherever a campaign is displayed: there is no payment processing yet, so
// there are no totals, raised amounts or progress (CLAUDE.md financial rules; ADR-036 §5).
func donationsNotAvailable() map[string]any {
	return map[string]any{"available": false, "message": "Donations are not yet available."}
}

func (s *Service) ownerView(ctx context.Context, c campaignRow) (OwnerView, error) {
	v := OwnerView{ID: c.ID, Slug: c.Slug, PublicCode: c.PublicCode, Title: c.Title, Summary: c.Summary, Story: c.Story, Category: c.Category,
		Goal: Money{AmountMinor: itoa(c.GoalAmountMinor), Currency: c.GoalCurrency}, Status: c.Status, Visibility: c.Visibility,
		ReReviewRequired: c.ReReviewRequired, CompletionReason: c.CompletionReason, CreatedAt: c.CreatedAt, SubmittedAt: c.SubmittedAt,
		ApprovedAt: c.ApprovedAt, PublishedAt: c.PublishedAt, PausedAt: c.PausedAt, SuspendedAt: c.SuspendedAt, CompletedAt: c.CompletedAt,
		CancelledAt: c.CancelledAt, ArchivedAt: c.ArchivedAt, ResubmissionCount: c.ResubmissionCount, Donations: donationsNotAvailable(),
		Version: c.Version}
	if c.OwnerUserID != nil {
		v.Owner = map[string]any{"type": "USER", "id": *c.OwnerUserID}
	} else {
		v.Owner = map[string]any{"type": "ORGANISATION", "id": *c.OwnerOrgID}
	}
	var err error
	if v.Beneficiary, err = s.currentLink(ctx, s.Pool, c.ID); err != nil {
		return v, err
	}
	if v.Beneficiary != nil {
		b, err := s.Beneficiaries.View(ctx, v.Beneficiary.BeneficiaryID)
		if err != nil {
			return v, err
		}
		v.Beneficiary.DisplayName, v.Beneficiary.BeneficiaryType, v.Beneficiary.VerificationStatus = b.DisplayName, b.BeneficiaryType, b.Status()
	}
	var f Feedback
	err = s.Pool.QueryRow(ctx, `SELECT outcome, reason_code, user_message, decided_at FROM app.campaign_reviews
		WHERE campaign_id = $1 AND status = 'DECIDED' ORDER BY decided_at DESC LIMIT 1`, c.ID).Scan(&f.Outcome, &f.ReasonCode, &f.UserMessage, &f.DecidedAt)
	if err == nil {
		v.Feedback = &f
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return v, err
	}
	return v, nil
}

// Get returns the owner view (owner or organisation member).
func (s *Service) Get(ctx context.Context, userID, id string) (OwnerView, error) {
	c, err := s.loadRow(ctx, s.Pool, id, false)
	if err != nil {
		return OwnerView{}, err
	}
	if ok, err := s.canAccess(ctx, userID, c.access(), false); err != nil {
		return OwnerView{}, err
	} else if !ok {
		return OwnerView{}, ErrNotFound
	}
	return s.ownerView(ctx, c)
}

// EligibilityFor evaluates an action for the owner view (no side effects).
func (s *Service) EligibilityFor(ctx context.Context, userID, id, action string) (Result, error) {
	c, err := s.loadRow(ctx, s.Pool, id, false)
	if err != nil {
		return Result{}, err
	}
	if ok, err := s.canAccess(ctx, userID, c.access(), false); err != nil {
		return Result{}, err
	} else if !ok {
		return Result{}, ErrNotFound
	}
	switch action {
	case ActEditDraft, ActSubmit, ActPublish, ActUpdatePublished, ActReactivate:
	default:
		return Result{}, httpx.Validation(errs.Detail{Field: "action", Code: "INVALID_VALUE"})
	}
	actor := userID
	if action == ActPublish || action == ActReactivate {
		actor = ""
	}
	return s.Evaluate(ctx, s.Pool, action, actor, c)
}

// Summary is a list card.
type Summary struct {
	ID          string     `json:"id"`
	Slug        string     `json:"slug"`
	Title       string     `json:"title"`
	Category    string     `json:"category"`
	Goal        Money      `json:"goal"`
	Status      string     `json:"status"`
	Owner       Owner      `json:"owner"`
	CreatedAt   time.Time  `json:"created_at"`
	SubmittedAt *time.Time `json:"submitted_at"`
	PublishedAt *time.Time `json:"published_at"`
	Version     int        `json:"version"`
}

// Owner names a campaign owner.
type Owner struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

const summarySelect = `SELECT id, slug, title, category_code, goal_amount_minor, goal_currency, status, owner_user_id, owner_organisation_id,
	created_at, submitted_at, published_at, version FROM app.campaigns`

func scanSummary(rows pgx.Rows) ([]Summary, error) {
	out := []Summary{}
	for rows.Next() {
		var x Summary
		var amount int64
		var u, o *string
		if err := rows.Scan(&x.ID, &x.Slug, &x.Title, &x.Category, &amount, &x.Goal.Currency, &x.Status, &u, &o, &x.CreatedAt, &x.SubmittedAt,
			&x.PublishedAt, &x.Version); err != nil {
			return nil, err
		}
		x.Goal.AmountMinor = itoa(amount)
		if u != nil {
			x.Owner = Owner{"USER", *u}
		} else {
			x.Owner = Owner{"ORGANISATION", *o}
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// Mine lists the user's own campaigns and those of their organisations (or one organisation).
func (s *Service) Mine(ctx context.Context, userID, orgID string) ([]Summary, error) {
	if orgID != "" {
		role, err := s.Orgs.MemberRole(ctx, orgID, userID)
		if err != nil {
			return nil, err
		}
		if role == "" {
			return nil, errs.New(errs.NotFound, "ORGANISATION_NOT_FOUND", "No such organisation.")
		}
		rows, err := s.Pool.Query(ctx, summarySelect+` WHERE owner_organisation_id = $1 ORDER BY created_at DESC LIMIT 200`, orgID)
		if err != nil {
			return nil, err
		}
		return scanSummary(rows)
	}
	orgs, err := s.Orgs.OrganisationsOf(ctx, userID)
	if err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, summarySelect+` WHERE owner_user_id = $1 OR owner_organisation_id = ANY ($2::uuid[]) ORDER BY created_at DESC LIMIT 200`,
		userID, orgs)
	if err != nil {
		return nil, err
	}
	return scanSummary(rows)
}

// ---- staff ------------------------------------------------------------------------------------------------

// QueueItem is a review queue row.
type QueueItem struct {
	CampaignID     string    `json:"campaign_id"`
	ReviewID       string    `json:"review_id"`
	Title          string    `json:"title"`
	Category       string    `json:"category"`
	Status         string    `json:"status"`
	ReviewStatus   string    `json:"review_status"`
	ReviewKind     string    `json:"review_kind"`
	RiskTier       string    `json:"risk_tier"`
	AssignedTo     *string   `json:"assigned_to"`
	Escalated      bool      `json:"escalated"`
	AwaitingSecond bool      `json:"awaiting_second_approval"`
	QueuedAt       time.Time `json:"queued_at"`
}

// Queue lists open reviews oldest first with a (queued_at, review id) keyset cursor.
func (s *Service) Queue(ctx context.Context, staffID, assigned, category, cursor string, limit int) ([]QueueItem, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var after *time.Time
	afterID := "00000000-0000-0000-0000-000000000000"
	if cursor != "" {
		ts, id, _ := strings.Cut(cursor, "|")
		t, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil || !validID(id) {
			return nil, "", httpx.Validation(errs.Detail{Field: "cursor", Code: "INVALID_FORMAT"})
		}
		after, afterID = &t, id
	}
	switch assigned {
	case "", "any", "me", "unassigned":
	default:
		return nil, "", httpx.Validation(errs.Detail{Field: "assigned", Code: "INVALID_VALUE"})
	}
	if assigned == "" {
		assigned = "any"
	}
	rows, err := s.Pool.Query(ctx, `SELECT c.id, r.id, c.title, c.category_code, c.status, r.status, r.kind, r.risk_tier, r.assigned_to, r.escalated,
		r.pending_outcome IS NOT NULL, r.created_at
		FROM app.campaign_reviews r JOIN app.campaigns c ON c.id = r.campaign_id
		WHERE r.status IN ('QUEUED','ASSIGNED','IN_REVIEW')
		  AND ($1 = 'any' OR ($1 = 'me' AND r.assigned_to = $2::uuid) OR ($1 = 'unassigned' AND r.assigned_to IS NULL))
		  AND ($3 = '' OR c.category_code = $3)
		  AND ($4::timestamptz IS NULL OR (r.created_at, r.id) > ($4, $5::uuid))
		ORDER BY r.created_at, r.id LIMIT $6`, assigned, staffID, category, after, afterID, limit)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []QueueItem{}
	for rows.Next() {
		var q QueueItem
		if err := rows.Scan(&q.CampaignID, &q.ReviewID, &q.Title, &q.Category, &q.Status, &q.ReviewStatus, &q.ReviewKind, &q.RiskTier, &q.AssignedTo,
			&q.Escalated, &q.AwaitingSecond, &q.QueuedAt); err != nil {
			return nil, "", err
		}
		out = append(out, q)
	}
	next := ""
	if len(out) == limit {
		last := out[len(out)-1]
		next = last.QueuedAt.UTC().Format(time.RFC3339Nano) + "|" + last.ReviewID
	}
	return out, next, rows.Err()
}

// AdminList lists campaigns of any status for staff with campaign.view (newest first, simple filters).
func (s *Service) AdminList(ctx context.Context, status, q string, limit int) ([]Summary, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.Pool.Query(ctx, summarySelect+` WHERE ($1 = '' OR status = $1) AND ($2 = '' OR title ILIKE '%' || $2 || '%' OR id::text = $2 OR slug = $2)
		ORDER BY created_at DESC LIMIT $3`, status, escapeLike(q), limit)
	if err != nil {
		return nil, err
	}
	return scanSummary(rows)
}

func escapeLike(q string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.TrimSpace(q))
}

// ReviewDetail is the reviewer's view of a campaign.
type ReviewDetail struct {
	Campaign     OwnerView        `json:"campaign"`
	RiskTier     string           `json:"risk_tier"`
	Owner        map[string]any   `json:"owner"`
	Beneficiary  map[string]any   `json:"beneficiary"`
	Restriction  string           `json:"restriction"`
	Media        *MediaReadiness  `json:"media"`
	Review       map[string]any   `json:"review"`
	Versions     map[string]any   `json:"versions"`
	History      []map[string]any `json:"history"`
	Eligibility  map[string]any   `json:"eligibility"`
	PolicyStatus map[string]any   `json:"policy"`
}

// ReviewDetailFor builds the reviewer view; viewing is audited.
func (s *Service) ReviewDetailFor(ctx context.Context, staffID, id string) (ReviewDetail, error) {
	c, err := s.loadRow(ctx, s.Pool, id, false)
	if err != nil {
		return ReviewDetail{}, err
	}
	var d ReviewDetail
	if d.Campaign, err = s.ownerView(ctx, c); err != nil {
		return d, err
	}
	d.RiskTier = c.RiskTier
	if c.OwnerUserID != nil {
		names, _ := users.DisplayNames(ctx, s.Pool, []string{*c.OwnerUserID})
		st, err := s.KYC.Level(ctx, *c.OwnerUserID)
		if err != nil {
			return d, err
		}
		d.Owner = map[string]any{"type": "USER", "id": *c.OwnerUserID, "display_name": names[*c.OwnerUserID], "kyc_level": st.Level, "kyc_status": st.Status}
	} else {
		name, orgType, _, _ := s.Orgs.Summary(ctx, *c.OwnerOrgID)
		st, err := s.KYC.OrganisationLevel(ctx, *c.OwnerOrgID)
		if err != nil {
			return d, err
		}
		d.Owner = map[string]any{"type": "ORGANISATION", "id": *c.OwnerOrgID, "display_name": name, "org_type": orgType, "kyb_level": st.Level}
	}
	if d.Campaign.Beneficiary != nil {
		b, err := s.Beneficiaries.View(ctx, d.Campaign.Beneficiary.BeneficiaryID)
		if err != nil {
			return d, err
		}
		d.Beneficiary = map[string]any{"id": b.ID, "type": b.BeneficiaryType, "display_name": b.DisplayName, "verification_status": b.Status(),
			"relationship": b.Relationship.Type, "authority_basis": b.AuthorityBasis, "disclosure": d.Campaign.Beneficiary.Disclosure,
			"consent_declared": d.Campaign.Beneficiary.ConsentDeclared}
	}
	if d.Restriction, err = s.Restricted(ctx, c.access()); err != nil {
		return d, err
	}
	if s.media != nil {
		m, err := s.media.Readiness(ctx, c.ID)
		if err != nil {
			return d, err
		}
		d.Media = &m
	}
	var r struct {
		ID, Kind, Status, Policy, Tier string
		Assigned, PendingBy            *string
		Second, Escalated              bool
		VersionID                      string
	}
	err = s.Pool.QueryRow(ctx, `SELECT id, kind, status, policy_version, risk_tier, assigned_to, pending_decided_by, requires_second_approval, escalated,
		campaign_version_id FROM app.campaign_reviews WHERE campaign_id = $1 AND status IN ('QUEUED','ASSIGNED','IN_REVIEW')`, c.ID).
		Scan(&r.ID, &r.Kind, &r.Status, &r.Policy, &r.Tier, &r.Assigned, &r.PendingBy, &r.Second, &r.Escalated, &r.VersionID)
	if err == nil {
		d.Review = map[string]any{"id": r.ID, "kind": r.Kind, "status": r.Status, "policy_version": r.Policy, "risk_tier": r.Tier,
			"assigned_to": r.Assigned, "pending_decided_by": r.PendingBy, "requires_second_approval": r.Second, "escalated": r.Escalated,
			"version_id": r.VersionID}
		if snap, err := s.versionContent(ctx, r.VersionID); err == nil {
			d.Versions = map[string]any{"reviewed": snap}
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return d, err
	}
	if d.Versions == nil {
		d.Versions = map[string]any{}
	}
	if c.ApprovedVersionID != nil {
		if snap, err := s.versionContent(ctx, *c.ApprovedVersionID); err == nil {
			d.Versions["approved"] = snap
		}
	}
	if d.History, err = s.History(ctx, c.ID); err != nil {
		return d, err
	}
	d.Eligibility = map[string]any{}
	for _, act := range []string{ActApprove, ActPublish} {
		ev, err := s.Evaluate(ctx, s.Pool, act, "", c)
		if err != nil {
			return d, err
		}
		d.Eligibility[act] = ev
	}
	pol, err := s.ActivePolicy(ctx)
	if err != nil {
		return d, err
	}
	d.PolicyStatus = map[string]any{"version": pol.Version, "screening": pol.Rules.Screening.Status}
	return d, s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return audit.Record(ctx, tx, audit.Event{Action: "campaign.review_viewed", ActorType: "staff", ActorID: staffID, TargetType: "campaign", TargetID: c.ID})
	})
}

// VersionContent is a stored snapshot.
type VersionContent struct {
	ID            string    `json:"id"`
	Number        int       `json:"version_number"`
	Kind          string    `json:"change_kind"`
	MaterialTypes []string  `json:"material_change_types"`
	Title         string    `json:"title"`
	Summary       string    `json:"summary"`
	Story         string    `json:"story"`
	Category      string    `json:"category"`
	Goal          Money     `json:"goal"`
	BeneficiaryID *string   `json:"beneficiary_id"`
	Disclosure    string    `json:"beneficiary_disclosure"`
	MediaIDs      []string  `json:"media_ids"`
	CreatedAt     time.Time `json:"created_at"`
}

func (s *Service) versionContent(ctx context.Context, id string) (VersionContent, error) {
	var v VersionContent
	var amount int64
	err := s.Pool.QueryRow(ctx, `SELECT id, version_number, change_kind, material_change_types, title, summary, story, category_code, goal_amount_minor,
		goal_currency, beneficiary_id, beneficiary_disclosure, media_ids::text[], created_at FROM app.campaign_versions WHERE id = $1`, id).
		Scan(&v.ID, &v.Number, &v.Kind, &v.MaterialTypes, &v.Title, &v.Summary, &v.Story, &v.Category, &amount, &v.Goal.Currency, &v.BeneficiaryID,
			&v.Disclosure, &v.MediaIDs, &v.CreatedAt)
	v.Goal.AmountMinor = itoa(amount)
	return v, err
}

// History is the campaign's status history (actor ids for staff views only).
func (s *Service) History(ctx context.Context, id string) ([]map[string]any, error) {
	rows, err := s.Pool.Query(ctx, `SELECT campaign_version, event_type, from_status, to_status, actor_type, actor_id, reason_code, occurred_at
		FROM app.campaign_status_history WHERE campaign_id = $1 ORDER BY campaign_version`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var v int
		var ev, to, at string
		var from, actor, reason *string
		var when time.Time
		if err := rows.Scan(&v, &ev, &from, &to, &at, &actor, &reason, &when); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"version": v, "event_type": ev, "from_status": from, "to_status": to, "actor_type": at, "actor_id": actor,
			"reason_code": reason, "occurred_at": when})
	}
	return out, rows.Err()
}
