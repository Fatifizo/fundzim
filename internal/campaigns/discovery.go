package campaigns

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/risk"
)

// Stage 16 (ADR-048): what the discovery module needs from campaigns. Campaigns stays the authority on whether a
// campaign may be shown: discovery keeps a search projection for candidate generation and ranking, and every card it
// serves comes from PublicCards, which re-checks eligibility now. A stale projection can drop a result; it can never
// expose a campaign that is not public.

// Eligibility levels for discovery surfaces (docs/stage-16/discovery-architecture.md §3).
//   - Discoverable (search, categories, locations, sitemap): live (ACTIVE, PAUSED, COMPLETED), PUBLIC visibility,
//     an approved version, and no compliance restriction of any level on the campaign, its owner, owning
//     organisation or current beneficiary.
//   - Promotable (featured, trending, recommendations): discoverable, ACTIVE, and no blocking risk hold (campaign,
//     donation or account-review hold on the campaign, owner or organisation).
//
// The campaign page itself keeps the ADR-036 rule (live status) and additionally hides at SUSPENDED / OFFBOARDED
// restrictions, closing the window before the asynchronous suspension consumer runs.

// ErrEligibilityUnavailable: eligibility could not be established (restriction or hold registry error). Discovery
// fails closed: it shows nothing rather than unchecked results.
var ErrEligibilityUnavailable = errs.New(errs.Unavailable, "DISCOVERY_UNAVAILABLE", "Campaign discovery is temporarily unavailable. Please try again shortly.")

// promotionHoldTypes are the holds that keep a campaign out of featured, trending and recommendations.
var promotionHoldTypes = []string{risk.HoldCampaign, risk.HoldDonation, risk.HoldAccountReview}

// ---- locations ------------------------------------------------------------------------------------------------

// Location is an entry of the reference list (a Zimbabwe province or town). Coarse by design: never an address.
type Location struct {
	Code         string  `json:"code"`
	Kind         string  `json:"kind"` // PROVINCE | TOWN
	Slug         string  `json:"slug"`
	Name         string  `json:"name"`
	ProvinceCode *string `json:"province_code"`
	DisplayOrder int     `json:"display_order"`
}

// PublicLocation is a campaign's location as shown publicly: the town (when chosen) and its province.
type PublicLocation struct {
	Code         string  `json:"code"`
	Name         string  `json:"name"`
	Kind         string  `json:"kind"`
	ProvinceCode string  `json:"province_code"`
	ProvinceName string  `json:"province_name"`
	ProvinceSlug string  `json:"province_slug"`
	TownSlug     *string `json:"town_slug"`
}

// Locations lists the active reference locations (provinces first, then towns, each in display order).
func (s *Service) Locations(ctx context.Context) ([]Location, error) {
	rows, err := s.Pool.Query(ctx, `SELECT code, kind, slug, name, province_code, display_order FROM app.campaign_locations
		WHERE active ORDER BY kind, display_order, code`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Location])
}

// publicLocations resolves location codes to their public shape (one query for any number of codes).
func (s *Service) publicLocations(ctx context.Context, q querier2, codes []string) (map[string]PublicLocation, error) {
	out := map[string]PublicLocation{}
	if len(codes) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT l.code, l.name, l.kind, coalesce(p.code, l.code), coalesce(p.name, l.name), coalesce(p.slug, l.slug),
		CASE WHEN l.kind = 'TOWN' THEN l.slug END
		FROM app.campaign_locations l LEFT JOIN app.campaign_locations p ON p.code = l.province_code WHERE l.code = ANY($1)`, codes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var l PublicLocation
		if err := rows.Scan(&l.Code, &l.Name, &l.Kind, &l.ProvinceCode, &l.ProvinceName, &l.ProvinceSlug, &l.TownSlug); err != nil {
			return nil, err
		}
		out[l.Code] = l
	}
	return out, rows.Err()
}

type querier2 interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// validLocation reports whether code is an active reference location.
func validLocation(ctx context.Context, q querier, code string) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM app.campaign_locations WHERE code = $1 AND active)`, code).Scan(&ok)
	return ok, err
}

// ---- eligibility ------------------------------------------------------------------------------------------------

type eligRow struct {
	id, status, visibility string
	ownerUser, ownerOrg    *string
	beneficiary            *string
	hasApproved            bool
}

// eligibility returns, for each id, whether it is discoverable and whether it is promotable now. Unknown ids are
// neither. Errors (restriction registry, hold registry) are returned as ErrEligibilityUnavailable: callers fail closed.
func (s *Service) eligibility(ctx context.Context, ids []string, needPromotable bool) (map[string]bool, map[string]bool, error) {
	disc, prom := map[string]bool{}, map[string]bool{}
	valid := make([]string, 0, len(ids))
	for _, id := range ids {
		if validID(id) {
			valid = append(valid, id)
		}
	}
	if len(valid) == 0 {
		return disc, prom, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT c.id::text, c.status, c.visibility, c.owner_user_id::text, c.owner_organisation_id::text,
		(SELECT b.beneficiary_id::text FROM app.campaign_beneficiaries b WHERE b.campaign_id = c.id AND b.unlinked_at IS NULL),
		c.approved_version_id IS NOT NULL
		FROM app.campaigns c WHERE c.id = ANY($1::uuid[])`, valid)
	if err != nil {
		return nil, nil, err
	}
	var list []eligRow
	for rows.Next() {
		var r eligRow
		if err := rows.Scan(&r.id, &r.status, &r.visibility, &r.ownerUser, &r.ownerOrg, &r.beneficiary, &r.hasApproved); err != nil {
			rows.Close()
			return nil, nil, err
		}
		list = append(list, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var subjects []Subject
	var cands []eligRow
	for _, r := range list {
		if !Live(r.status) || r.visibility != "PUBLIC" || !r.hasApproved {
			continue
		}
		cands = append(cands, r)
		subjects = append(subjects, Subject{"CAMPAIGN", r.id})
		if r.ownerUser != nil {
			subjects = append(subjects, Subject{"USER", *r.ownerUser})
		}
		if r.ownerOrg != nil {
			subjects = append(subjects, Subject{"ORGANISATION", *r.ownerOrg})
		}
		if r.beneficiary != nil {
			subjects = append(subjects, Subject{"BENEFICIARY", *r.beneficiary})
		}
	}
	if len(cands) == 0 {
		return disc, prom, nil
	}
	if s.Restrictions == nil {
		return nil, nil, ErrEligibilityUnavailable
	}
	levels, err := s.Restrictions.Levels(ctx, subjects)
	if err != nil {
		s.Logger.WarnContext(ctx, "discovery eligibility: restriction lookup failed", "error_category", "dependency", "error", err.Error())
		return nil, nil, ErrEligibilityUnavailable
	}
	restricted := func(t string, id *string) bool {
		return id != nil && levels[Subject{t, *id}] != "" && levels[Subject{t, *id}] != RestrictionNone
	}
	var promCands []eligRow
	var holdSubjects []risk.HoldSubject
	for _, r := range cands {
		if restricted("CAMPAIGN", &r.id) || restricted("USER", r.ownerUser) || restricted("ORGANISATION", r.ownerOrg) || restricted("BENEFICIARY", r.beneficiary) {
			continue
		}
		disc[r.id] = true
		if needPromotable && r.status == StatusActive {
			promCands = append(promCands, r)
			holdSubjects = append(holdSubjects, risk.HoldSubject{Type: "CAMPAIGN", ID: r.id})
			if r.ownerUser != nil {
				holdSubjects = append(holdSubjects, risk.HoldSubject{Type: "USER", ID: *r.ownerUser})
			}
			if r.ownerOrg != nil {
				holdSubjects = append(holdSubjects, risk.HoldSubject{Type: "ORGANISATION", ID: *r.ownerOrg})
			}
		}
	}
	if len(promCands) == 0 {
		return disc, prom, nil
	}
	if s.Holds == nil {
		return nil, nil, ErrEligibilityUnavailable
	}
	holds, err := s.Holds.BlockingHolds(ctx, promotionHoldTypes, holdSubjects...)
	if err != nil {
		s.Logger.WarnContext(ctx, "discovery eligibility: hold lookup failed", "error_category", "dependency", "error", err.Error())
		return nil, nil, ErrEligibilityUnavailable
	}
	held := map[string]bool{}
	for _, h := range holds {
		held[h.SubjectType+":"+h.SubjectID] = true
	}
	for _, r := range promCands {
		if held["CAMPAIGN:"+r.id] || (r.ownerUser != nil && held["USER:"+*r.ownerUser]) || (r.ownerOrg != nil && held["ORGANISATION:"+*r.ownerOrg]) {
			continue
		}
		prom[r.id] = true
	}
	return disc, prom, nil
}

// Discoverable returns the subset of ids that may appear in discovery now (and, with promotable, the subset that may
// be promoted). Used by discovery for counts and filters it cannot render as cards (sitemap, analytics scoping).
func (s *Service) Discoverable(ctx context.Context, ids []string, promotable bool) (map[string]bool, error) {
	disc, prom, err := s.eligibility(ctx, ids, promotable)
	if promotable {
		return prom, err
	}
	return disc, err
}

// DiscoveryCard is a public card for discovery: the Stage 7 card plus the public location.
type DiscoveryCard struct {
	PublicCard
	ID       string          `json:"-"`
	Location *PublicLocation `json:"location"`
}

// PublicCards renders cards for ids in the given order, keeping only campaigns that are discoverable now (or
// promotable now, when promotable is set). Fails closed with ErrEligibilityUnavailable.
func (s *Service) PublicCards(ctx context.Context, ids []string, promotable bool) ([]DiscoveryCard, error) {
	disc, prom, err := s.eligibility(ctx, ids, promotable)
	if err != nil {
		return nil, err
	}
	keep := disc
	if promotable {
		keep = prom
	}
	var wanted []string
	for _, id := range ids {
		if keep[id] {
			wanted = append(wanted, id)
		}
	}
	if len(wanted) == 0 {
		return []DiscoveryCard{}, nil
	}
	rows, err := s.Pool.Query(ctx, cardSelect+`, v.location_code FROM unnest($1::uuid[]) WITH ORDINALITY AS u(cid, o)
		JOIN app.campaigns c ON c.id = u.cid JOIN app.campaign_versions v ON v.id = c.approved_version_id ORDER BY u.o`, wanted)
	if err != nil {
		return nil, err
	}
	var crs []cardRow
	var locs []*string
	for rows.Next() {
		var c cardRow
		var amount int64
		var loc *string
		if err := rows.Scan(&c.id, &c.Slug, &c.Title, &c.Summary, &c.Category, &amount, &c.Goal.Currency, &c.Status, &c.CoverMedia, &c.PublishedAt,
			&c.CreatedAt, &c.ownerUser, &c.ownerOrg, &c.ben, &loc); err != nil {
			rows.Close()
			return nil, err
		}
		c.Goal.AmountMinor = itoa(amount)
		crs = append(crs, c)
		locs = append(locs, loc)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	cards, err := s.enrichCards(ctx, crs)
	if err != nil {
		return nil, err
	}
	var codes []string
	for _, l := range locs {
		if l != nil {
			codes = append(codes, *l)
		}
	}
	lm, err := s.publicLocations(ctx, s.Pool, codes)
	if err != nil {
		return nil, err
	}
	out := make([]DiscoveryCard, 0, len(cards))
	for i, c := range cards {
		d := DiscoveryCard{PublicCard: c, ID: crs[i].id}
		if locs[i] != nil {
			if l, ok := lm[*locs[i]]; ok {
				d.Location = &l
			}
		}
		out = append(out, d)
	}
	return out, nil
}

// ---- indexing -----------------------------------------------------------------------------------------------------

// DiscoveryDoc is what the discovery indexer may copy for one campaign: public, approved-version fields only.
// Discoverable false means "remove from the index" (the other fields are then empty).
type DiscoveryDoc struct {
	CampaignID    string
	Discoverable  bool
	Slug          string
	Title         string
	Summary       string
	Category      string
	CategoryName  string
	LocationCode  *string
	LocationName  string
	ProvinceCode  *string
	ProvinceName  string
	OrganiserType string
	OrganiserName string
	Status        string
	PublishedAt   *time.Time
	VersionID     string
}

// DiscoverySnapshot returns the indexable document for a campaign. ErrNotFound for an unknown id; registry errors are
// returned (the indexer retries) — a campaign is never indexed as discoverable without a successful check.
func (s *Service) DiscoverySnapshot(ctx context.Context, campaignID string) (DiscoveryDoc, error) {
	d := DiscoveryDoc{CampaignID: campaignID}
	c, err := s.loadRow(ctx, s.Pool, campaignID, false)
	if err != nil {
		return d, err
	}
	disc, _, err := s.eligibility(ctx, []string{campaignID}, false)
	if err != nil {
		return d, err
	}
	if !disc[campaignID] || c.ApprovedVersionID == nil || c.PublishedAt == nil {
		return d, nil
	}
	v, err := s.versionContent(ctx, *c.ApprovedVersionID)
	if err != nil {
		return d, err
	}
	org, err := s.organiser(ctx, c)
	if err != nil {
		return d, err
	}
	d.Discoverable, d.Slug, d.Title, d.Summary, d.Category, d.Status, d.PublishedAt, d.VersionID =
		true, c.Slug, v.Title, v.Summary, v.Category, c.Status, c.PublishedAt, v.ID
	d.OrganiserType, _ = org["type"].(string)
	d.OrganiserName, _ = org["display_name"].(string)
	if err := s.Pool.QueryRow(ctx, `SELECT name FROM app.campaign_categories WHERE code = $1`, v.Category).Scan(&d.CategoryName); err != nil {
		return d, err
	}
	if v.Location != nil {
		lm, err := s.publicLocations(ctx, s.Pool, []string{*v.Location})
		if err != nil {
			return d, err
		}
		if l, ok := lm[*v.Location]; ok {
			code, prov := l.Code, l.ProvinceCode
			d.LocationCode, d.LocationName, d.ProvinceCode, d.ProvinceName = &code, l.Name, &prov, l.ProvinceName
		}
	}
	return d, nil
}

// CampaignIDsPage lists campaign ids in id order after a cursor (the indexer's reconciliation sweep walks every
// campaign that has ever been published; drafts are never indexed).
func (s *Service) CampaignIDsPage(ctx context.Context, after string, limit int) ([]string, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	if after == "" {
		after = "00000000-0000-0000-0000-000000000000"
	} else if !validID(after) {
		return nil, errors.New("campaigns: invalid cursor")
	}
	rows, err := s.Pool.Query(ctx, `SELECT id::text FROM app.campaigns WHERE published_at IS NOT NULL AND id > $1::uuid ORDER BY id LIMIT $2`, after, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// CampaignIDBySlug resolves a public campaign's id from its slug (discovery events and referral codes; existence of
// non-public campaigns is never revealed: ErrNotFound).
func (s *Service) CampaignIDBySlug(ctx context.Context, slug string) (string, error) {
	a, err := s.PublicBySlug(ctx, slug)
	if err != nil {
		return "", err
	}
	return a.CampaignID, nil
}

// SlugByID returns a campaign's slug (owner analytics and staff views; the caller authorises).
func (s *Service) SlugByID(ctx context.Context, campaignID string) (string, string, error) {
	if !validID(campaignID) {
		return "", "", ErrNotFound
	}
	var slug, title string
	err := s.Pool.QueryRow(ctx, `SELECT slug, title FROM app.campaigns WHERE id = $1`, campaignID).Scan(&slug, &title)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return slug, title, err
}

// IDsBySlugs maps slugs of live campaigns to their ids (discovery event ingestion; anything else is absent).
func (s *Service) IDsBySlugs(ctx context.Context, slugs []string) (map[string]string, error) {
	out := map[string]string{}
	var valid []string
	for _, sl := range slugs {
		if validSlug(sl) {
			valid = append(valid, sl)
		}
	}
	if len(valid) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT slug, id::text FROM app.campaigns WHERE slug = ANY($1) AND status IN ('ACTIVE','PAUSED','COMPLETED')`, valid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sl, id string
		if err := rows.Scan(&sl, &id); err != nil {
			return nil, err
		}
		out[sl] = id
	}
	return out, rows.Err()
}
