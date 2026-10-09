package campaigns

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// CreateInput is POST /campaigns.
type CreateInput struct {
	Title          string  `json:"title"`
	Summary        string  `json:"summary"`
	Category       string  `json:"category"`
	Goal           *Money  `json:"goal"`
	OrganisationID *string `json:"organisation_id"`
}

// Create creates a DRAFT campaign after the CREATE_DRAFT evaluation.
func (s *Service) Create(ctx context.Context, userID string, in CreateInput) (string, error) {
	pol, err := s.ActivePolicy(ctx)
	if err != nil {
		return "", err
	}
	org := ""
	if in.OrganisationID != nil {
		if !validID(*in.OrganisationID) {
			return "", httpx.Validation(errs.Detail{Field: "organisation_id", Code: "INVALID_FORMAT"})
		}
		org = *in.OrganisationID
		if role, err := s.Orgs.MemberRole(ctx, org, userID); err != nil {
			return "", err
		} else if role == "" {
			return "", errs.New(errs.NotFound, "ORGANISATION_NOT_FOUND", "No such organisation.")
		}
	}
	var det []errs.Detail
	lim := pol.Rules.Content
	title, code := CleanText(in.Title, lim.Title[0], lim.Title[1], false)
	if code != "" {
		det = append(det, errs.Detail{Field: "title", Code: code})
	}
	summary, code := CleanText(in.Summary, lim.Summary[0], lim.Summary[1], false)
	if code != "" {
		det = append(det, errs.Detail{Field: "summary", Code: code})
	}
	var tier string
	if err := s.Pool.QueryRow(ctx, `SELECT default_risk_tier FROM app.campaign_categories WHERE code = $1 AND active`, in.Category).Scan(&tier); errors.Is(err, pgx.ErrNoRows) {
		det = append(det, errs.Detail{Field: "category", Code: "INVALID_VALUE"})
	} else if err != nil {
		return "", err
	}
	var amount int64
	if in.Goal == nil {
		det = append(det, errs.Detail{Field: "goal", Code: "MISSING_FIELD"})
	} else {
		var gd []errs.Detail
		amount, gd = s.validateGoal(ctx, pol, in.Goal.AmountMinor, in.Goal.Currency)
		det = append(det, gd...)
	}
	if len(det) > 0 {
		return "", httpx.Validation(det...)
	}
	ev, err := s.EvaluateCreate(ctx, userID, org, in.Category)
	if err != nil {
		return "", err
	}
	if !ev.Allowed {
		return "", notEligible(ev)
	}
	id := ids.New()
	for attempt := 0; attempt < 5; attempt++ {
		pc, err := newPublicCode()
		if err != nil {
			return "", err
		}
		err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			var ownerUser, ownerOrg any = userID, nil
			if org != "" {
				ownerUser, ownerOrg = nil, org
			}
			if _, err := tx.Exec(ctx, `INSERT INTO app.campaigns (id, public_code, slug, owner_user_id, owner_organisation_id, created_by,
				category_code, title, summary, goal_amount_minor, goal_currency, status, risk_tier, policy_version, created_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'DRAFT',$12,$13,$14)`,
				id, pc, slugFor(title, pc), ownerUser, ownerOrg, userID, in.Category, title, summary, amount, in.Goal.Currency,
				tier, pol.Version, s.now()); err != nil {
				return err
			}
			if err := s.history(ctx, tx, id, 1, "CREATED", nil, StatusDraft, userActor(userID), "", "", nil); err != nil {
				return err
			}
			if err := s.addGoal(ctx, tx, id, amount, in.Goal.Currency, userID); err != nil {
				return err
			}
			c := campaignRow{ID: id, Status: StatusDraft}
			if org != "" {
				c.OwnerOrgID = &org
			} else {
				c.OwnerUserID = &userID
			}
			if err := audit.Record(ctx, tx, audit.Event{Action: "campaign.draft_created", ActorID: userID, TargetType: "campaign", TargetID: id,
				Metadata: map[string]any{"to_status": StatusDraft, "category": in.Category}}); err != nil {
				return err
			}
			return s.emit(ctx, tx, &c, "campaigns.created", map[string]any{"to_status": StatusDraft})
		})
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" && pg.ConstraintName == "uq_campaigns_public_code" {
			continue // 50-bit collision: retry with a new code
		}
		return id, err
	}
	return "", errors.New("campaigns: could not allocate a public code")
}

func (s *Service) addGoal(ctx context.Context, tx pgx.Tx, campaignID string, amount int64, currency, by string) error {
	_, err := tx.Exec(ctx, `INSERT INTO app.campaign_goals (id, campaign_id, goal_version, amount_minor, currency, set_by, created_at)
		SELECT $1, $2, coalesce(max(goal_version), 0) + 1, $3, $4, $5, $6 FROM app.campaign_goals WHERE campaign_id = $2`,
		ids.New(), campaignID, amount, currency, by, s.now())
	return err
}

// Patch is PATCH /campaigns/{id} (nil = unchanged).
type Patch struct {
	Title      *string `json:"title"`
	Summary    *string `json:"summary"`
	Story      *string `json:"story"`
	Category   *string `json:"category"`
	Goal       *Money  `json:"goal"`
	Visibility *string `json:"visibility"`
}

// Update edits the working copy with optimistic concurrency (ifMatch = the version the client saw). DRAFT and
// CHANGES_REQUESTED are edited in place; edits of a live campaign stay off the public page until a re-review
// approves them (ADR-036 §3).
func (s *Service) Update(ctx context.Context, userID, id string, p Patch, ifMatch int) error {
	if ifMatch <= 0 {
		return errs.New(errs.Unprocessable, "IF_MATCH_REQUIRED", "Send If-Match with the version you are editing.")
	}
	pol, err := s.ActivePolicy(ctx)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.lockOwned(ctx, tx, userID, id, true)
		if err != nil {
			return err
		}
		if c.Version != ifMatch {
			return ErrStateChanged
		}
		live := c.Status == StatusActive || c.Status == StatusPaused
		contentChange := p.Title != nil || p.Summary != nil || p.Story != nil || p.Category != nil || p.Goal != nil
		switch {
		case c.Status == StatusDraft || c.Status == StatusChangesRequested:
		case live:
		case !contentChange && p.Visibility != nil && Live(c.Status) || !contentChange && p.Visibility != nil && c.Status == StatusApproved:
		default:
			return ErrInvalidStatus
		}
		action := ActEditDraft
		if live {
			action = ActUpdatePublished
		}
		ev, err := s.Evaluate(ctx, tx, action, userID, c)
		if err != nil {
			return err
		}
		if !ev.Allowed {
			return notEligible(ev)
		}
		set := map[string]any{}
		var det []errs.Detail
		var kinds []string
		lim := pol.Rules.Content
		text := func(field string, in *string, min, max int, multi bool, kind string, cur string) {
			if in == nil {
				return
			}
			v, code := CleanText(*in, min, max, multi)
			if code != "" {
				det = append(det, errs.Detail{Field: field, Code: code})
				return
			}
			if v != cur {
				set[field] = v
				kinds = append(kinds, kind)
			}
		}
		text("title", p.Title, lim.Title[0], lim.Title[1], false, "TITLE", c.Title)
		text("summary", p.Summary, lim.Summary[0], lim.Summary[1], false, "SUMMARY", c.Summary)
		storyMin := 0 // a draft may hold an incomplete story; submission checks the minimum
		if live {
			storyMin = lim.Story[0]
		}
		text("story", p.Story, storyMin, lim.Story[1], true, "STORY", c.Story)
		if p.Category != nil && *p.Category != c.Category {
			var tier string
			if err := tx.QueryRow(ctx, `SELECT default_risk_tier FROM app.campaign_categories WHERE code = $1 AND active`, *p.Category).Scan(&tier); errors.Is(err, pgx.ErrNoRows) {
				det = append(det, errs.Detail{Field: "category", Code: "INVALID_VALUE"})
			} else if err != nil {
				return err
			} else {
				set["category_code"], set["risk_tier"] = *p.Category, upgradeTier(c.RiskTier, tier, c.SubmittedAt != nil)
				kinds = append(kinds, "CATEGORY")
			}
		}
		if p.Goal != nil {
			if p.Goal.Currency != c.GoalCurrency && c.SubmittedAt != nil {
				det = append(det, errs.Detail{Field: "goal.currency", Code: "CURRENCY_LOCKED"})
			} else if amount, gd := s.validateGoal(ctx, pol, p.Goal.AmountMinor, p.Goal.Currency); len(gd) > 0 {
				det = append(det, gd...)
			} else if amount != c.GoalAmountMinor || p.Goal.Currency != c.GoalCurrency {
				set["goal_amount_minor"], set["goal_currency"] = amount, p.Goal.Currency
				kind := "GOAL_CHANGE"
				if ratio := pol.Rules.MaterialEdit.GoalIncreaseRatioPercent; amount > c.GoalAmountMinor && (amount-c.GoalAmountMinor)*100 > c.GoalAmountMinor*ratio {
					kind = "GOAL_INCREASE"
				}
				kinds = append(kinds, kind)
				if err := s.addGoal(ctx, tx, c.ID, amount, p.Goal.Currency, userID); err != nil {
					return err
				}
			}
		}
		if p.Visibility != nil {
			if *p.Visibility != "PUBLIC" && *p.Visibility != "UNLISTED" {
				det = append(det, errs.Detail{Field: "visibility", Code: "INVALID_VALUE"})
			} else if *p.Visibility != c.Visibility {
				set["visibility"] = *p.Visibility
			}
		}
		if len(det) > 0 {
			return httpx.Validation(det...)
		}
		if len(set) == 0 {
			return nil
		}
		if c.Status == StatusDraft || c.Status == StatusChangesRequested {
			if _, ok := set["title"]; ok && c.PublishedAt == nil {
				set["slug"] = slugFor(set["title"].(string), c.PublicCode)
			}
		}
		if err := s.apply(ctx, tx, &c, userActor(userID), change{event: "UPDATED", action: "updated", set: set,
			payload: map[string]any{"fields": keys(set)}}); err != nil {
			return err
		}
		if live && len(kinds) > 0 {
			return s.openReReview(ctx, tx, &c, userActor(userID), kinds)
		}
		return nil
	})
}

// upgradeTier never silently downgrades the tier of a campaign that has been submitted (approval policy §4).
func upgradeTier(cur, next string, submitted bool) string {
	rank := map[string]int{"STANDARD": 0, "ELEVATED": 1, "HIGH": 2}
	if submitted && rank[next] < rank[cur] {
		return cur
	}
	return next
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// lockOwned locks a campaign the user may access (write = owner or ORG_ADMIN); others get ErrNotFound.
func (s *Service) lockOwned(ctx context.Context, tx pgx.Tx, userID, id string, write bool) (campaignRow, error) {
	c, err := s.loadRow(ctx, tx, id, true)
	if err != nil {
		return c, err
	}
	ok, err := s.canAccess(ctx, userID, c.access(), write)
	if err != nil {
		return c, err
	}
	if !ok {
		return campaignRow{}, ErrNotFound
	}
	return c, nil
}

// snapshot appends a campaign_versions row of the current working copy, beneficiary link and approved media.
func (s *Service) snapshot(ctx context.Context, tx pgx.Tx, c campaignRow, kind string, types []string, by string) (string, error) {
	l, err := s.currentLink(ctx, tx, c.ID)
	if err != nil {
		return "", err
	}
	var media []string
	if s.media != nil {
		if media, err = s.media.ApprovedIDs(ctx, c.ID); err != nil {
			return "", err
		}
	}
	if media == nil {
		media = []string{}
	}
	var benef any
	disclosure := "NONE"
	if l != nil {
		benef, disclosure = l.BeneficiaryID, l.Disclosure
	}
	canon, _ := json.Marshal(map[string]any{"title": c.Title, "summary": c.Summary, "story": c.Story, "category": c.Category,
		"goal_amount_minor": itoa(c.GoalAmountMinor), "goal_currency": c.GoalCurrency, "beneficiary_id": benef, "disclosure": disclosure, "media": media})
	sum := sha256.Sum256(canon)
	if types == nil {
		types = []string{}
	}
	id := ids.New()
	_, err = tx.Exec(ctx, `INSERT INTO app.campaign_versions (id, campaign_id, version_number, change_kind, material_change_types, title, summary,
		story, category_code, goal_amount_minor, goal_currency, beneficiary_id, beneficiary_disclosure, media_ids, content_sha256, created_by, created_at)
		SELECT $1, $2, coalesce(max(version_number), 0) + 1, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16
		FROM app.campaign_versions WHERE campaign_id = $2`,
		id, c.ID, kind, types, c.Title, c.Summary, c.Story, c.Category, c.GoalAmountMinor, c.GoalCurrency, benef, disclosure, media, sum[:], by, s.now())
	return id, err
}

// openReview queues a review pinned to a version and the policy in force.
func (s *Service) openReview(ctx context.Context, tx pgx.Tx, c campaignRow, versionID, kind string) (string, error) {
	pol, err := s.ActivePolicy(ctx)
	if err != nil {
		return "", err
	}
	id := ids.New()
	_, err = tx.Exec(ctx, `INSERT INTO app.campaign_reviews (id, campaign_id, campaign_version_id, policy_version, risk_tier, requires_second_approval,
		kind, status, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,'QUEUED',$8)`,
		id, c.ID, versionID, pol.Version, c.RiskTier, pol.SecondApproval(c.RiskTier), kind, s.now())
	return id, err
}

// openReReview snapshots a live campaign's edited working copy and queues a RE_REVIEW; the public page keeps
// the approved version meanwhile. A previous open re-review is cancelled (superseded by the newer edit).
func (s *Service) openReReview(ctx context.Context, tx pgx.Tx, c *campaignRow, a actor, kinds []string) error {
	if _, err := tx.Exec(ctx, `UPDATE app.campaign_reviews SET status = 'CANCELLED' WHERE campaign_id = $1 AND kind = 'RE_REVIEW'
		AND status IN ('QUEUED','ASSIGNED','IN_REVIEW')`, c.ID); err != nil {
		return err
	}
	fresh, err := s.loadRow(ctx, tx, c.ID, false) // the edit just applied in this tx (c holds the pre-edit content)
	if err != nil {
		return err
	}
	vid, err := s.snapshot(ctx, tx, fresh, "MATERIAL_EDIT", dedupe(kinds), a.ID)
	if err != nil {
		return err
	}
	if _, err := s.openReview(ctx, tx, *c, vid, "RE_REVIEW"); err != nil {
		return err
	}
	return s.apply(ctx, tx, c, a, change{event: "RE_REVIEW_REQUESTED", action: "re_review_requested", outboxType: "campaigns.re_review_requested",
		set:     map[string]any{"pending_version_id": vid, "re_review_required": true, "re_review_reason": "MATERIAL_EDIT"},
		payload: map[string]any{"material_change_types": dedupe(kinds)}})
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// RecordMaterialChange is the hook for media changes on a live campaign: it opens a re-review. For drafts it
// does nothing (the next submission snapshots everything).
func (s *Service) RecordMaterialChange(ctx context.Context, campaignID, kind, actorID string) error {
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.loadRow(ctx, tx, campaignID, true)
		if err != nil {
			return err
		}
		if c.Status != StatusActive && c.Status != StatusPaused {
			return nil
		}
		return s.openReReview(ctx, tx, &c, userActor(actorID), []string{kind})
	})
}

// Submit moves DRAFT / CHANGES_REQUESTED → SUBMITTED after the SUBMIT_FOR_REVIEW evaluation. Submitting an
// already SUBMITTED campaign is an idempotent no-op.
func (s *Service) Submit(ctx context.Context, userID, id string) error {
	var refused *Result
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.lockOwned(ctx, tx, userID, id, true)
		if err != nil {
			return err
		}
		if c.Status == StatusSubmitted {
			return nil
		}
		if c.Status != StatusDraft && c.Status != StatusChangesRequested {
			return ErrInvalidStatus
		}
		ev, err := s.Evaluate(ctx, tx, ActSubmit, userID, c)
		if err != nil {
			return err
		}
		if !ev.Allowed {
			refused = &ev
			return notEligible(ev)
		}
		if err := s.recordEvaluation(ctx, tx, c.ID, userID, ev); err != nil {
			return err
		}
		basis, err := s.fundraisingBasis(ctx, tx, c)
		if err != nil {
			return err
		}
		kind := "SUBMISSION"
		if c.SubmittedAt != nil {
			kind = "RESUBMISSION"
		}
		vid, err := s.snapshot(ctx, tx, c, kind, nil, userID)
		if err != nil {
			return err
		}
		if _, err := s.openReview(ctx, tx, c, vid, "SUBMISSION"); err != nil {
			return err
		}
		return s.apply(ctx, tx, &c, userActor(userID), change{to: StatusSubmitted, event: "SUBMITTED", action: "submitted",
			outboxType: "campaigns.submitted", set: map[string]any{"submitted_at": s.now(), "fundraising_basis": basis, "pending_version_id": vid},
			payload: map[string]any{"version_id": vid, "policy_version": ev.PolicyVersion}})
	})
	if refused != nil {
		s.recordRefusal(ctx, id, userID, *refused)
	}
	return err
}

// recordRefusal persists a refused gated evaluation outside the rolled-back transaction (best effort).
func (s *Service) recordRefusal(ctx context.Context, campaignID, actorID string, r Result) {
	if err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error { return s.recordEvaluation(ctx, tx, campaignID, actorID, r) }); err != nil {
		s.Logger.Warn("campaign eligibility refusal not recorded", "error_category", "db")
	}
}

func (s *Service) fundraisingBasis(ctx context.Context, q querier, c campaignRow) (string, error) {
	if c.OwnerOrgID != nil {
		return "ORGANISATION_OWN_CAUSE", nil
	}
	l, err := s.currentLink(ctx, q, c.ID)
	if err != nil || l == nil {
		return "", err
	}
	b, err := s.Beneficiaries.View(ctx, l.BeneficiaryID)
	if err != nil {
		return "", err
	}
	if b.BeneficiaryType == "SELF" {
		return "SELF_FUNDRAISING", nil
	}
	return "INDIVIDUAL_FOR_OTHERS", nil
}

// Withdraw takes a queued submission back to DRAFT (not once a reviewer has started).
func (s *Service) Withdraw(ctx context.Context, userID, id string) error {
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.lockOwned(ctx, tx, userID, id, true)
		if err != nil {
			return err
		}
		if c.Status != StatusSubmitted {
			return ErrInvalidStatus
		}
		if _, err := tx.Exec(ctx, `UPDATE app.campaign_reviews SET status = 'CANCELLED' WHERE campaign_id = $1 AND status IN ('QUEUED','ASSIGNED')`, c.ID); err != nil {
			return err
		}
		return s.apply(ctx, tx, &c, userActor(userID), change{to: StatusDraft, event: "WITHDRAWN", action: "withdrawn",
			set: map[string]any{"pending_version_id": nil}})
	})
}

// Revise takes a REJECTED campaign back to DRAFT, within the policy's resubmission limit.
func (s *Service) Revise(ctx context.Context, userID, id string) error {
	pol, err := s.ActivePolicy(ctx)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.lockOwned(ctx, tx, userID, id, true)
		if err != nil {
			return err
		}
		if c.Status != StatusRejected {
			return ErrInvalidStatus
		}
		if c.ResubmissionCount >= pol.Rules.MaxResubmissions {
			return notEligible(Result{Reasons: []Reason{{Code: "RESUBMISSION_LIMIT_REACHED", Message: messages["RESUBMISSION_LIMIT_REACHED"]}}})
		}
		return s.apply(ctx, tx, &c, userActor(userID), change{to: StatusDraft, event: "REVISED", action: "revised",
			set: map[string]any{"resubmission_count": c.ResubmissionCount + 1}})
	})
}

// OwnerTransition covers the simple owner lifecycle actions.
func (s *Service) OwnerTransition(ctx context.Context, userID, id, action, reason, note string) error {
	pol, err := s.ActivePolicy(ctx)
	if err != nil {
		return err
	}
	var refused *Result
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.lockOwned(ctx, tx, userID, id, true)
		if err != nil {
			return err
		}
		a := userActor(userID)
		switch action {
		case "cancel":
			if c.Status != StatusDraft && c.Status != StatusChangesRequested && c.Status != StatusApproved {
				return ErrInvalidStatus // a live campaign is completed, not cancelled, by its owner (LR-019)
			}
			return s.apply(ctx, tx, &c, a, change{to: StatusCancelled, event: "CANCELLED", action: "cancelled", outboxType: "campaigns.cancelled",
				note: note, set: map[string]any{"cancelled_at": s.now()}})
		case "publish":
			return s.publish(ctx, tx, &c, a, &refused)
		case "pause":
			if !pol.Rules.OwnerPauseAllowed {
				return errs.New(errs.Forbidden, "PAUSE_NOT_ALLOWED", "Pausing is not available.")
			}
			if c.Status != StatusActive {
				return ErrInvalidStatus
			}
			return s.apply(ctx, tx, &c, a, change{to: StatusPaused, event: "PAUSED", action: "paused", outboxType: "campaigns.paused", note: note,
				set: map[string]any{"paused_at": s.now()}})
		case "resume":
			if c.Status != StatusPaused {
				return ErrInvalidStatus
			}
			ev, err := s.Evaluate(ctx, tx, ActReactivate, userID, c)
			if err != nil {
				return err
			}
			if !ev.Allowed {
				refused = &ev
				return notEligible(ev)
			}
			if err := s.recordEvaluation(ctx, tx, c.ID, userID, ev); err != nil {
				return err
			}
			return s.apply(ctx, tx, &c, a, change{to: StatusActive, event: "RESUMED", action: "resumed", outboxType: "campaigns.resumed",
				set: map[string]any{"paused_at": nil}})
		case "complete":
			if c.Status != StatusActive && c.Status != StatusPaused {
				return ErrInvalidStatus
			}
			if reason != "ORGANISER_COMPLETED" && reason != "OTHER" {
				// GOAL_REACHED would imply money was collected; there are no donations before the payment stages.
				return httpx.Validation(errs.Detail{Field: "reason", Code: "INVALID_VALUE"})
			}
			return s.apply(ctx, tx, &c, a, change{to: StatusCompleted, event: "COMPLETED", action: "completed", outboxType: "campaigns.completed",
				note: note, set: map[string]any{"completed_at": s.now(), "completion_reason": reason, "paused_at": nil}})
		case "archive":
			if c.Status != StatusCompleted && c.Status != StatusCancelled && c.Status != StatusRejected {
				return ErrInvalidStatus
			}
			return s.apply(ctx, tx, &c, a, change{to: StatusArchived, event: "ARCHIVED", action: "archived", outboxType: "campaigns.archived",
				set: map[string]any{"archived_at": s.now()}})
		}
		return errs.New(errs.NotFound, errs.CodeRouteNotFound, "No such route.")
	})
	if refused != nil {
		s.recordRefusal(ctx, id, userID, *refused)
	}
	return err
}

// publish moves APPROVED → ACTIVE after the PUBLISH evaluation, inside the caller's transaction with the
// campaign row locked (ADR-036 §4). The slug is fixed from the approved title at first publication.
func (s *Service) publish(ctx context.Context, tx pgx.Tx, c *campaignRow, a actor, refused **Result) error {
	if c.Status != StatusApproved {
		return ErrInvalidStatus
	}
	ev, err := s.Evaluate(ctx, tx, ActPublish, "", *c)
	if err != nil {
		return err
	}
	if !ev.Allowed {
		*refused = &ev
		return notEligible(ev)
	}
	if err := s.recordEvaluation(ctx, tx, c.ID, a.ID, ev); err != nil {
		return err
	}
	set := map[string]any{"published_at": s.now()}
	if c.PublishedAt == nil && c.ApprovedVersionID != nil {
		var title string
		if err := tx.QueryRow(ctx, `SELECT title FROM app.campaign_versions WHERE id = $1`, *c.ApprovedVersionID).Scan(&title); err != nil {
			return err
		}
		set["slug"] = slugFor(title, c.PublicCode)
	}
	ch := change{to: StatusActive, event: "PUBLISHED", action: "published", outboxType: "campaigns.published", set: set}
	if a.Type == "STAFF" {
		ch.reasonCode = "STAFF_PUBLISHED"
	}
	return s.apply(ctx, tx, c, a, ch)
}

// BeneficiaryInput links a beneficiary (change control: a live campaign goes to re-review).
type BeneficiaryInput struct {
	BeneficiaryID   string `json:"beneficiary_id"`
	Disclosure      string `json:"disclosure"`
	ConsentDeclared bool   `json:"consent_declared"`
	Reason          string `json:"reason"`
}

// LinkBeneficiary sets the campaign's primary beneficiary.
func (s *Service) LinkBeneficiary(ctx context.Context, userID, id string, in BeneficiaryInput) error {
	if in.Disclosure == "" {
		in.Disclosure = "NONE"
	}
	if in.Disclosure != "NONE" && in.Disclosure != "DISPLAY_NAME" {
		return httpx.Validation(errs.Detail{Field: "disclosure", Code: "INVALID_VALUE"})
	}
	if in.Disclosure == "DISPLAY_NAME" && !in.ConsentDeclared {
		return httpx.Validation(errs.Detail{Field: "consent_declared", Code: "CONSENT_REQUIRED"})
	}
	if !validID(in.BeneficiaryID) {
		return httpx.Validation(errs.Detail{Field: "beneficiary_id", Code: "INVALID_FORMAT"})
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.lockOwned(ctx, tx, userID, id, true)
		if err != nil {
			return err
		}
		live := c.Status == StatusActive || c.Status == StatusPaused
		if c.Status != StatusDraft && c.Status != StatusChangesRequested && !live {
			return ErrInvalidStatus // never during review or after approval: withdraw first
		}
		reason, code := CleanText(in.Reason, 0, 500, false)
		if code != "" || live && len(reason) < 3 {
			return httpx.Validation(errs.Detail{Field: "reason", Code: "INVALID_LENGTH"})
		}
		b, err := s.Beneficiaries.View(ctx, in.BeneficiaryID)
		if err != nil || !ownsBeneficiary(c, b.Owner.Type, b.Owner.ID) {
			return httpx.Validation(errs.Detail{Field: "beneficiary_id", Code: "BENEFICIARY_NOT_AUTHORISED"})
		}
		if b.BeneficiaryType == "MINOR" && in.Disclosure != "NONE" {
			return httpx.Validation(errs.Detail{Field: "disclosure", Code: "MINOR_DISCLOSURE_NOT_ALLOWED"})
		}
		level, err := s.Restricted(ctx, c.access())
		if err != nil {
			return err
		}
		if level != RestrictionNone {
			return notEligible(Result{Reasons: []Reason{{Code: "ACCOUNT_RESTRICTED", Message: messages["ACCOUNT_RESTRICTED"]}}})
		}
		cur, err := s.currentLink(ctx, tx, c.ID)
		if err != nil {
			return err
		}
		if cur != nil && cur.BeneficiaryID == in.BeneficiaryID && cur.Disclosure == in.Disclosure && cur.ConsentDeclared == in.ConsentDeclared {
			return nil
		}
		now := s.now()
		if cur != nil {
			ur := reason
			if ur == "" {
				ur = "REPLACED"
			}
			if _, err := tx.Exec(ctx, `UPDATE app.campaign_beneficiaries SET unlinked_by = $2, unlinked_at = $3, unlink_reason = $4 WHERE id = $1`,
				cur.ID, userID, now, ur); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO app.campaign_beneficiaries (id, campaign_id, beneficiary_id, disclosure, consent_declared, linked_by,
			linked_at, link_reason) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, ids.New(), c.ID, in.BeneficiaryID, in.Disclosure, in.ConsentDeclared, userID,
			now, nullable(reason)); err != nil {
			return err
		}
		prev := ""
		if cur != nil {
			prev = cur.BeneficiaryID
		}
		if err := s.apply(ctx, tx, &c, userActor(userID), change{event: "BENEFICIARY_CHANGED", action: "beneficiary_changed",
			outboxType: "campaigns.beneficiary_changed", note: reason,
			payload: map[string]any{"beneficiary_id": in.BeneficiaryID, "previous_beneficiary_id": prev, "disclosure": in.Disclosure}}); err != nil {
			return err
		}
		if live {
			return s.openReReview(ctx, tx, &c, userActor(userID), []string{"BENEFICIARY"})
		}
		return nil
	})
}

// UnlinkBeneficiary removes the beneficiary of a draft.
func (s *Service) UnlinkBeneficiary(ctx context.Context, userID, id, beneficiaryID, reason string) error {
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.lockOwned(ctx, tx, userID, id, true)
		if err != nil {
			return err
		}
		if c.Status != StatusDraft && c.Status != StatusChangesRequested {
			return ErrInvalidStatus
		}
		r, code := CleanText(reason, 3, 500, false)
		if code != "" {
			return httpx.Validation(errs.Detail{Field: "reason", Code: code})
		}
		tag, err := tx.Exec(ctx, `UPDATE app.campaign_beneficiaries SET unlinked_by = $3, unlinked_at = $4, unlink_reason = $5
			WHERE campaign_id = $1 AND beneficiary_id = $2 AND unlinked_at IS NULL`, c.ID, beneficiaryID, userID, s.now(), r)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.NotFound, "BENEFICIARY_NOT_LINKED", "This beneficiary is not linked to the campaign.")
		}
		return s.apply(ctx, tx, &c, userActor(userID), change{event: "BENEFICIARY_CHANGED", action: "beneficiary_changed",
			outboxType: "campaigns.beneficiary_changed", note: r, payload: map[string]any{"previous_beneficiary_id": beneficiaryID}})
	})
}
