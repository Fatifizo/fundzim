package campaigns

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/users"
)

// PublicCampaign is what anyone may see: the approved version only, and nothing private (no ids of the owner,
// beneficiary or reviewers, no documents, no identity or payout details, no risk or compliance data).
type PublicCampaign struct {
	Slug          string          `json:"slug"`
	CanonicalSlug string          `json:"canonical_slug"`
	PublicCode    string          `json:"public_code"`
	CategoryName  string          `json:"category_name"`
	Trust         Trust           `json:"trust"`
	Title         string          `json:"title"`
	Summary       string          `json:"summary"`
	Story         string          `json:"story"`
	Category      string          `json:"category"`
	Location      *PublicLocation `json:"location"` // coarse (province / town), owner-chosen and reviewed; null when not specified
	Goal          Money           `json:"goal"`
	Status        string          `json:"status"` // ACTIVE | PAUSED | COMPLETED
	Organiser     map[string]any  `json:"organiser"`
	Beneficiary   map[string]any  `json:"beneficiary"`
	CoverMedia    *string         `json:"cover_media_id"`
	MediaIDs      []string        `json:"media_ids"`
	Media         []PublicMedia   `json:"media"`
	PublishedAt   *time.Time      `json:"published_at"`
	CompletedAt   *time.Time      `json:"completed_at"`
	Donations     map[string]any  `json:"donations"`
}

// PublicMedia is an image of the approved version that is still APPROVED (removed media drop out).
type PublicMedia struct {
	ID       string               `json:"id"`
	Kind     string               `json:"kind"`
	AltText  string               `json:"alt_text"`
	Variants []PublicMediaVariant `json:"variants"` // srcset widths (ascending); fetch with ?w=<width>
}

// PublicMediaVariant is a resized JPEG width variant of a public image (app.campaign_media_variants). Serving falls
// back to the full image while a variant is not yet scanned, so a listed width is always fetchable.
type PublicMediaVariant struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// publicMedia keeps the approved version's order and drops media removed or rejected since approval.
func (s *Service) publicMedia(ctx context.Context, ids []string) ([]PublicMedia, error) {
	rows, err := s.Pool.Query(ctx, `SELECT cm.id::text, cm.kind, cm.alt_text,
		  coalesce((SELECT json_agg(json_build_object('width', v.width, 'height', v.height) ORDER BY v.width)
		            FROM app.campaign_media_variants v WHERE v.media_id = cm.id), '[]'::json)
		FROM unnest($1::uuid[]) WITH ORDINALITY AS u(m, o)
		JOIN app.campaign_media cm ON cm.id = u.m AND cm.status = 'APPROVED' ORDER BY u.o`, ids)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (PublicMedia, error) {
		var m PublicMedia
		err := r.Scan(&m.ID, &m.Kind, &m.AltText, &m.Variants)
		if m.Variants == nil {
			m.Variants = []PublicMediaVariant{}
		}
		return m, err
	})
}

// PublicView returns a live campaign by slug (404 for anything not public; existence is not revealed).
func (s *Service) PublicView(ctx context.Context, slug string) (PublicCampaign, error) {
	a, err := s.resolvePublic(ctx, slug)
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
	p := PublicCampaign{Slug: c.Slug, CanonicalSlug: c.Slug, PublicCode: strings.ToLower(c.PublicCode), Title: v.Title, Summary: v.Summary, Story: v.Story, Category: v.Category, Goal: v.Goal, Status: c.Status,
		MediaIDs: []string{}, PublishedAt: c.PublishedAt, CompletedAt: c.CompletedAt, Donations: donationsNotAvailable()}
	if p.Media, err = s.publicMedia(ctx, v.MediaIDs); err != nil {
		return p, err
	}
	for i, m := range p.Media {
		p.MediaIDs = append(p.MediaIDs, m.ID)
		if m.Kind == "COVER" && p.CoverMedia == nil {
			p.CoverMedia = &p.Media[i].ID
		}
	}
	if p.Organiser, err = s.organiser(ctx, c); err != nil {
		return p, err
	}
	if p.Trust, err = s.trust(ctx, c.OwnerUserID, c.OwnerOrgID, v.BeneficiaryID, true); err != nil {
		return p, err
	}
	if err := s.Pool.QueryRow(ctx, `SELECT name FROM app.campaign_categories WHERE code = $1`, v.Category).Scan(&p.CategoryName); err != nil {
		return p, err
	}
	if v.Location != nil {
		lm, err := s.publicLocations(ctx, s.Pool, []string{*v.Location})
		if err != nil {
			return p, err
		}
		if l, ok := lm[*v.Location]; ok {
			p.Location = &l
		}
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
	Slug        string         `json:"slug"`
	Title       string         `json:"title"`
	Summary     string         `json:"summary"`
	Category    string         `json:"category"`
	Goal        Money          `json:"goal"`
	Status      string         `json:"status"`
	CoverMedia  *string        `json:"cover_media_id"`
	PublishedAt *time.Time     `json:"published_at"`
	CreatedAt   time.Time      `json:"created_at"`
	Organiser   map[string]any `json:"organiser"`
	Trust       *Trust         `json:"trust"`
}

// SearchInput is GET /public/campaigns.
type SearchInput struct {
	Q, Category, Sort, Cursor string
	Limit                     int
}

// Search lists live, PUBLIC (listed) campaigns. Text search runs over the approved snapshots only; the
// query is bounded (length, statement timeout, page size) and the count is exact up to a cap.
func (s *Service) Search(ctx context.Context, in SearchInput) ([]PublicCard, string, Count, error) {
	var total Count
	if in.Limit <= 0 || in.Limit > 50 {
		in.Limit = 20
	}
	orderCol := "c.published_at"
	switch in.Sort {
	case "", "published":
	case "created":
		orderCol = "c.created_at"
	default:
		return nil, "", total, httpx.Validation(errs.Detail{Field: "sort", Code: "INVALID_VALUE"})
	}
	q, code := CleanText(in.Q, 0, 100, false)
	if code != "" && code != "HTML_NOT_ALLOWED" && code != "UNSAFE_LINK" {
		return nil, "", total, httpx.Validation(errs.Detail{Field: "q", Code: code})
	}
	if code != "" {
		q = strings.Join(strings.Fields(in.Q), " ") // markup in a search box is just text to match (never rendered as HTML)
		if utf8.RuneCountInString(q) > 100 {
			return nil, "", total, httpx.Validation(errs.Detail{Field: "q", Code: "INVALID_LENGTH"})
		}
	}
	category := strings.TrimSpace(in.Category)
	if len(category) > 40 {
		return nil, "", total, httpx.Validation(errs.Detail{Field: "category", Code: "INVALID_LENGTH"})
	}
	var before *time.Time
	beforeID := "ffffffff-ffff-ffff-ffff-ffffffffffff"
	if in.Cursor != "" {
		ts, id, _ := strings.Cut(in.Cursor, "|")
		t, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil || !validID(id) {
			return nil, "", total, httpx.Validation(errs.Detail{Field: "cursor", Code: "INVALID_FORMAT"})
		}
		before, beforeID = &t, id
	}
	const where = ` FROM app.campaigns c JOIN app.campaign_versions v ON v.id = c.approved_version_id
		WHERE c.status IN ('ACTIVE','PAUSED','COMPLETED') AND c.visibility = 'PUBLIC'
		  AND ($1 = '' OR v.category_code = $1 OR v.category_code = (SELECT code FROM app.campaign_categories WHERE slug = $1))
		  AND ($2 = '' OR v.search_tsv @@ websearch_to_tsquery('simple', $2))`
	var rows []cardRow
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// bounded and always planned for the actual parameters (a cached generic plan ignores the GIN index;
		// performance.md R4)
		if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout = '2s'; SET LOCAL plan_cache_mode = force_custom_plan`); err != nil {
			return err
		}
		r, err := tx.Query(ctx, cardSelect+`, `+orderCol+where+`
		  AND ($3::timestamptz IS NULL OR (`+orderCol+`, c.id) < ($3, $4::uuid))
		ORDER BY `+orderCol+` DESC, c.id DESC LIMIT $5`, category, q, before, beforeID, in.Limit)
		if err != nil {
			return err
		}
		if rows, err = scanCards(r, true); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM (SELECT 1`+where+` LIMIT `+itoa(searchCountCap+1)+`) x`, category, q).Scan(&total.Value)
	})
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "57014" {
		return nil, "", total, errs.New(errs.Unavailable, "SEARCH_TIMEOUT", "Search took too long. Try a simpler search.")
	}
	if err != nil {
		return nil, "", total, err
	}
	if total.Value > searchCountCap {
		total = Count{Value: searchCountCap, Capped: true}
	}
	next := ""
	if len(rows) == in.Limit {
		last := rows[len(rows)-1]
		next = last.orderKey.UTC().Format(time.RFC3339Nano) + "|" + last.id
	}
	// Stage 16: restricted campaigns never appear in a listing (the cursor still advances past them)
	idl := make([]string, 0, len(rows))
	for _, r := range rows {
		idl = append(idl, r.id)
	}
	disc, _, err := s.eligibility(ctx, idl, false)
	if err != nil {
		return nil, "", total, err
	}
	kept := rows[:0]
	for _, r := range rows {
		if disc[r.id] {
			kept = append(kept, r)
		}
	}
	cards, err := s.enrichCards(ctx, kept)
	return cards, next, total, err
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
		tag, err := tx.Exec(ctx, `UPDATE app.campaign_categories SET `+strings.Join(sets, ", ")+` WHERE code = $1 AND version = $`+
			itoa(int64(len(args))), args...)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var exists bool
			_ = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM app.campaign_categories WHERE code = $1)`, code).Scan(&exists)
			if !exists {
				return errs.New(errs.NotFound, "CATEGORY_NOT_FOUND", "No such category.")
			}
			return httpx.ErrPreconditionFailed
		}
		return audit.Record(ctx, tx, audit.Event{Action: "campaign.category_updated", ActorType: "staff", ActorID: staffID, TargetType: "campaign_category",
			TargetID: "", Metadata: map[string]any{"code": code, "fields": len(sets)}})
	})
}
