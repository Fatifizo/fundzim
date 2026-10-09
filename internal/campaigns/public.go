package campaigns

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/users"
)

// PublicCampaign is what anyone may see: the approved version only, and nothing private (no ids of the owner,
// beneficiary or reviewers, no documents, no identity or payout details, no risk or compliance data).
type PublicCampaign struct {
	Slug        string         `json:"slug"`
	Title       string         `json:"title"`
	Summary     string         `json:"summary"`
	Story       string         `json:"story"`
	Category    string         `json:"category"`
	Goal        Money          `json:"goal"`
	Status      string         `json:"status"` // ACTIVE | PAUSED | COMPLETED
	Organiser   map[string]any `json:"organiser"`
	Beneficiary map[string]any `json:"beneficiary"`
	CoverMedia  *string        `json:"cover_media_id"`
	MediaIDs    []string       `json:"media_ids"`
	PublishedAt *time.Time     `json:"published_at"`
	CompletedAt *time.Time     `json:"completed_at"`
	Donations   map[string]any `json:"donations"`
}

// PublicView returns a live campaign by slug (404 for anything not public; existence is not revealed).
func (s *Service) PublicView(ctx context.Context, slug string) (PublicCampaign, error) {
	a, err := s.PublicBySlug(ctx, slug)
	if err != nil {
		return PublicCampaign{}, err
	}
	c, err := s.loadRow(ctx, s.Pool, a.CampaignID, false)
	if err != nil {
		return PublicCampaign{}, err
	}
	if c.ApprovedVersionID == nil {
		return PublicCampaign{}, ErrNotFound
	}
	v, err := s.versionContent(ctx, *c.ApprovedVersionID)
	if err != nil {
		return PublicCampaign{}, err
	}
	p := PublicCampaign{Slug: c.Slug, Title: v.Title, Summary: v.Summary, Story: v.Story, Category: v.Category, Goal: v.Goal, Status: c.Status,
		MediaIDs: v.MediaIDs, PublishedAt: c.PublishedAt, CompletedAt: c.CompletedAt, Donations: donationsNotAvailable()}
	if len(v.MediaIDs) > 0 {
		p.CoverMedia = &v.MediaIDs[0]
	}
	if p.Organiser, err = s.organiser(ctx, c); err != nil {
		return p, err
	}
	p.Beneficiary = map[string]any{"disclosed": false}
	if v.BeneficiaryID != nil && v.Disclosure == "DISPLAY_NAME" {
		b, err := s.Beneficiaries.View(ctx, *v.BeneficiaryID)
		if err != nil {
			return p, err
		}
		if b.BeneficiaryType != "MINOR" { // never disclosed for minors (LR-070), whatever the snapshot says
			p.Beneficiary = map[string]any{"disclosed": true, "display_name": b.DisplayName}
		}
	}
	return p, nil
}

func (s *Service) organiser(ctx context.Context, c campaignRow) (map[string]any, error) {
	if c.OwnerUserID != nil {
		names, err := users.DisplayNames(ctx, s.Pool, []string{*c.OwnerUserID})
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "INDIVIDUAL", "display_name": names[*c.OwnerUserID]}, nil
	}
	name, _, _, err := s.Orgs.Summary(ctx, *c.OwnerOrgID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"type": "ORGANISATION", "display_name": name}, nil
}

// PublicCard is a search result.
type PublicCard struct {
	Slug        string     `json:"slug"`
	Title       string     `json:"title"`
	Summary     string     `json:"summary"`
	Category    string     `json:"category"`
	Goal        Money      `json:"goal"`
	Status      string     `json:"status"`
	CoverMedia  *string    `json:"cover_media_id"`
	PublishedAt *time.Time `json:"published_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

// SearchInput is GET /public/campaigns.
type SearchInput struct {
	Q, Category, Sort, Cursor string
	Limit                     int
}

// Search lists live, PUBLIC (listed) campaigns. Text search runs over the approved snapshots only.
func (s *Service) Search(ctx context.Context, in SearchInput) ([]PublicCard, string, error) {
	if in.Limit <= 0 || in.Limit > 50 {
		in.Limit = 20
	}
	orderCol := "c.published_at"
	switch in.Sort {
	case "", "published":
	case "created":
		orderCol = "c.created_at"
	default:
		return nil, "", httpx.Validation(errs.Detail{Field: "sort", Code: "INVALID_VALUE"})
	}
	q := strings.TrimSpace(in.Q)
	if len(q) > 100 {
		return nil, "", httpx.Validation(errs.Detail{Field: "q", Code: "INVALID_LENGTH"})
	}
	var before *time.Time
	beforeID := "ffffffff-ffff-ffff-ffff-ffffffffffff"
	if in.Cursor != "" {
		ts, id, _ := strings.Cut(in.Cursor, "|")
		t, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil || !validID(id) {
			return nil, "", httpx.Validation(errs.Detail{Field: "cursor", Code: "INVALID_FORMAT"})
		}
		before, beforeID = &t, id
	}
	rows, err := s.Pool.Query(ctx, `SELECT c.id, c.slug, v.title, v.summary, v.category_code, v.goal_amount_minor, v.goal_currency, c.status,
		v.media_ids[1]::text, c.published_at, c.created_at, `+orderCol+`
		FROM app.campaigns c JOIN app.campaign_versions v ON v.id = c.approved_version_id
		WHERE c.status IN ('ACTIVE','PAUSED','COMPLETED') AND c.visibility = 'PUBLIC'
		  AND ($1 = '' OR v.category_code = $1)
		  AND ($2 = '' OR to_tsvector('simple', v.title || ' ' || v.summary) @@ websearch_to_tsquery('simple', $2))
		  AND ($3::timestamptz IS NULL OR (`+orderCol+`, c.id) < ($3, $4::uuid))
		ORDER BY `+orderCol+` DESC, c.id DESC LIMIT $5`, in.Category, q, before, beforeID, in.Limit)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []PublicCard{}
	var lastKey time.Time
	var lastID string
	for rows.Next() {
		var p PublicCard
		var id string
		var amount int64
		if err := rows.Scan(&id, &p.Slug, &p.Title, &p.Summary, &p.Category, &amount, &p.Goal.Currency, &p.Status, &p.CoverMedia, &p.PublishedAt,
			&p.CreatedAt, &lastKey); err != nil {
			return nil, "", err
		}
		p.Goal.AmountMinor = itoa(amount)
		lastID = id
		out = append(out, p)
	}
	next := ""
	if len(out) == in.Limit {
		next = lastKey.UTC().Format(time.RFC3339Nano) + "|" + lastID
	}
	return out, next, rows.Err()
}

// Category is a campaign category.
type Category struct {
	Code                 string `json:"code"`
	Slug                 string `json:"slug"`
	Name                 string `json:"name"`
	Description          string `json:"description"`
	DisplayOrder         int    `json:"display_order"`
	Active               bool   `json:"active"`
	RequiresOrganisation bool   `json:"requires_organisation"`
	Sensitive            bool   `json:"sensitive"`
	RiskTier             string `json:"risk_tier,omitempty"`
	ModerationPolicy     string `json:"moderation_policy,omitempty"`
	Version              int    `json:"version"`
}

// Categories lists categories (active only for the public; staff see tiers and inactive ones).
func (s *Service) Categories(ctx context.Context, staff bool) ([]Category, error) {
	rows, err := s.Pool.Query(ctx, `SELECT code, slug, name, description, display_order, active, requires_organisation, sensitive, default_risk_tier,
		moderation_policy, version FROM app.campaign_categories WHERE active OR $1 ORDER BY display_order, code`, staff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Category{}
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.Code, &c.Slug, &c.Name, &c.Description, &c.DisplayOrder, &c.Active, &c.RequiresOrganisation, &c.Sensitive, &c.RiskTier,
			&c.ModerationPolicy, &c.Version); err != nil {
			return nil, err
		}
		if !staff {
			c.RiskTier, c.ModerationPolicy = "", ""
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CategoryPatch changes presentation fields and the active flag only (risk tier etc. change by policy).
type CategoryPatch struct {
	Name         *string `json:"name"`
	Description  *string `json:"description"`
	DisplayOrder *int    `json:"display_order"`
	Active       *bool   `json:"active"`
}

// UpdateCategory applies a category patch (campaign.category.manage), audited.
func (s *Service) UpdateCategory(ctx context.Context, staffID, code string, p CategoryPatch, ifMatch int) error {
	var det []errs.Detail
	sets, args := []string{}, []any{code}
	if p.Name != nil {
		v, c := CleanText(*p.Name, 2, 60, false)
		if c != "" {
			det = append(det, errs.Detail{Field: "name", Code: c})
		}
		args = append(args, v)
		sets = append(sets, "name = $"+itoa(int64(len(args))))
	}
	if p.Description != nil {
		v, c := CleanText(*p.Description, 2, 500, false)
		if c != "" {
			det = append(det, errs.Detail{Field: "description", Code: c})
		}
		args = append(args, v)
		sets = append(sets, "description = $"+itoa(int64(len(args))))
	}
	if p.DisplayOrder != nil {
		if *p.DisplayOrder < 0 || *p.DisplayOrder > 10000 {
			det = append(det, errs.Detail{Field: "display_order", Code: "INVALID_VALUE"})
		}
		args = append(args, *p.DisplayOrder)
		sets = append(sets, "display_order = $"+itoa(int64(len(args))))
	}
	if p.Active != nil {
		args = append(args, *p.Active)
		sets = append(sets, "active = $"+itoa(int64(len(args))))
	}
	if len(det) > 0 {
		return httpx.Validation(det...)
	}
	if len(sets) == 0 {
		return nil
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		args = append(args, ifMatch)
		tag, err := tx.Exec(ctx, `UPDATE app.campaign_categories SET `+strings.Join(sets, ", ")+` WHERE code = $1 AND ($`+itoa(int64(len(args)))+
			` = 0 OR version = $`+itoa(int64(len(args)))+`)`, args...)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var exists bool
			_ = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM app.campaign_categories WHERE code = $1)`, code).Scan(&exists)
			if !exists {
				return errs.New(errs.NotFound, "CATEGORY_NOT_FOUND", "No such category.")
			}
			return ErrStateChanged
		}
		return audit.Record(ctx, tx, audit.Event{Action: "campaign.category_updated", ActorType: "staff", ActorID: staffID, TargetType: "campaign_category",
			TargetID: "", Metadata: map[string]any{"code": code, "fields": len(sets)}})
	})
}
