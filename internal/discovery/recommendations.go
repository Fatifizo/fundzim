package discovery

import (
	"context"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/campaigns"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
)

// Recommendations (docs/stage-16/recommendations.md). Transparent rules, not a model: the same context gives everyone
// the same list. Nothing about the viewer is used (no account, history, location, KYC, screening or risk data): the
// response always says personalised: false. Every item carries the rule that selected it.

// Reason codes and their public wording.
const (
	ReasonFeatured      = "FEATURED"
	ReasonTrending      = "POPULAR_NOW"
	ReasonRecent        = "RECENTLY_PUBLISHED"
	ReasonSameCategory  = "MORE_IN_CATEGORY"
	ReasonSameProvince  = "SAME_PROVINCE"
	ReasonRelatedSearch = "RELATED_TO_SEARCH"
)

var reasonText = map[string]string{
	ReasonFeatured:      "Featured by FundZim",
	ReasonTrending:      "Popular right now",
	ReasonRecent:        "Recently published",
	ReasonSameCategory:  "More in this category",
	ReasonSameProvince:  "In the same province",
	ReasonRelatedSearch: "Related to your search",
}

// Reason explains why an item is shown.
type Reason struct {
	Code string `json:"code"`
	Text string `json:"text"`
}

func reason(code string) Reason { return Reason{Code: code, Text: reasonText[code]} }

// RecommendInput is GET /discovery/recommendations.
type RecommendInput struct {
	Campaign string // slug of the campaign being viewed (excluded from the list)
	Category string
	Q        string
	Limit    int
}

// Recommendations is the response.
type Recommendations struct {
	Items        []Card `json:"items"`
	Personalised bool   `json:"personalised"`
	Rules        string `json:"rules"`
}

const rulesText = "Chosen by fixed rules, the same for everyone: same category, same province, featured, popular right now and " +
	"recently published. Nothing about you is used."

// Recommend applies the rules in order and fills up to limit with promotable campaigns, deduplicated.
func (s *Service) Recommend(ctx context.Context, in RecommendInput) (Recommendations, error) {
	out := Recommendations{Items: []Card{}, Rules: rulesText}
	limit := in.Limit
	if limit <= 0 || limit > 12 {
		limit = 4
	}
	if in.Campaign != "" && !slugRe.MatchString(in.Campaign) {
		return out, httpx.Validation(errs.Detail{Field: "campaign", Code: "INVALID_FORMAT"})
	}
	if in.Category != "" && !catRe.MatchString(in.Category) {
		return out, httpx.Validation(errs.Detail{Field: "category", Code: "INVALID_VALUE"})
	}
	q, code := NormalizeQuery(in.Q)
	if code != "" {
		return out, httpx.Validation(errs.Detail{Field: "q", Code: code})
	}
	seen := map[string]bool{}
	var ctxCategory, ctxProvince string
	if in.Campaign != "" {
		var id string
		err := s.Pool.QueryRow(ctx, `SELECT campaign_id::text, coalesce(category_code, ''), coalesce(province_code, '')
			FROM app.discovery_campaign_index WHERE slug = $1 AND discoverable`, in.Campaign).Scan(&id, &ctxCategory, &ctxProvince)
		if err != nil && err != pgx.ErrNoRows {
			return out, err
		}
		if id != "" {
			seen[id] = true
		}
	}
	if in.Category != "" {
		ref, err := s.reference(ctx)
		if err != nil {
			return out, err
		}
		ctxCategory = ref.categoryCode(in.Category)
	}
	add := func(ids []string, why string) error {
		if len(out.Items) >= limit {
			return nil
		}
		var fresh []string
		for _, id := range ids {
			if !seen[id] {
				fresh = append(fresh, id)
			}
		}
		if len(fresh) == 0 {
			return nil
		}
		cards, err := s.Campaigns.PublicCards(ctx, fresh, true)
		if err != nil {
			return err
		}
		for _, c := range cards {
			if len(out.Items) >= limit || seen[c.ID] {
				continue
			}
			seen[c.ID] = true
			r := reason(why)
			out.Items = append(out.Items, Card{DiscoveryCard: c, Reason: &r})
			s.recordImpression(c.ID, "RECOMMENDED")
		}
		return nil
	}
	pick := func(sql string, args ...any) ([]string, error) {
		rows, err := s.Pool.Query(ctx, sql, args...)
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, pgx.RowTo[string])
	}
	n := limit * 3
	steps := []struct {
		on   bool
		why  string
		sql  string
		args []any
	}{
		{!q.Empty(), ReasonRelatedSearch, `SELECT campaign_id::text FROM (SELECT * FROM app.discovery_campaign_index WHERE discoverable AND status = 'ACTIVE'
			AND search_doc @@ to_tsquery('simple', $1) ORDER BY published_at DESC, campaign_id DESC LIMIT ` + strconv.Itoa(RelevanceWindow) + `) w
			ORDER BY ts_rank_cd(search_doc, to_tsquery('simple', $1), 32) DESC, published_at DESC, campaign_id DESC LIMIT $2`, []any{q.TSQuery(), n}},
		{ctxCategory != "", ReasonSameCategory, `SELECT i.campaign_id::text FROM app.discovery_campaign_index i
			LEFT JOIN app.discovery_trending_scores t ON t.campaign_id = i.campaign_id AND t.rank_eligible
			WHERE i.discoverable AND i.status = 'ACTIVE' AND i.category_code = $1
			ORDER BY coalesce(t.score, 0) DESC, i.published_at DESC, i.campaign_id DESC LIMIT $2`, []any{ctxCategory, n}},
		{ctxProvince != "", ReasonSameProvince, `SELECT campaign_id::text FROM app.discovery_campaign_index WHERE discoverable AND status = 'ACTIVE'
			AND province_code = $1 ORDER BY published_at DESC, campaign_id DESC LIMIT $2`, []any{ctxProvince, n}},
		{true, ReasonFeatured, `SELECT campaign_id::text FROM app.discovery_featured_placements WHERE status = 'APPROVED' AND starts_at <= $1 AND ends_at > $1
			ORDER BY position, starts_at, id LIMIT $2`, []any{s.now(), n}},
		{true, ReasonTrending, `SELECT t.campaign_id::text FROM app.discovery_trending_scores t JOIN app.discovery_campaign_index i
			ON i.campaign_id = t.campaign_id AND i.discoverable WHERE t.rank_eligible ORDER BY t.score DESC, i.published_at DESC, t.campaign_id DESC LIMIT $1`,
			[]any{n}},
		{true, ReasonRecent, `SELECT campaign_id::text FROM app.discovery_campaign_index WHERE discoverable AND status = 'ACTIVE'
			ORDER BY published_at DESC, campaign_id DESC LIMIT $1`, []any{n}},
	}
	for _, st := range steps {
		if !st.on || len(out.Items) >= limit {
			continue
		}
		ids, err := pick(st.sql, st.args...)
		if err != nil {
			return out, err
		}
		if err := add(ids, st.why); err != nil {
			return out, err
		}
	}
	return out, nil
}

// ---- categories and locations with counts ---------------------------------------------------------------------------

// CategoryFacet is a category with its number of discoverable campaigns.
type CategoryFacet struct {
	campaigns.PublicCategory
	Discoverable int `json:"discoverable_campaigns"`
}

// Categories lists active categories with counts from the projection (counts are of indexed, discoverable campaigns;
// a campaign restricted since its last reindex may be counted until the consumer runs — listings re-check).
func (s *Service) Categories(ctx context.Context) ([]CategoryFacet, error) {
	ref, err := s.reference(ctx)
	if err != nil {
		return nil, err
	}
	counts, err := s.countBy(ctx, "category_code")
	if err != nil {
		return nil, err
	}
	out := make([]CategoryFacet, 0, len(ref.categories))
	for _, c := range ref.categories {
		out = append(out, CategoryFacet{PublicCategory: c, Discoverable: counts[c.Code]})
	}
	return out, nil
}

// LocationFacet is a province or town with its number of discoverable campaigns.
type LocationFacet struct {
	campaigns.Location
	Discoverable int `json:"discoverable_campaigns"`
}

// LocationsResult is GET /discovery/locations.
type LocationsResult struct {
	Country     string          `json:"country"` // ZW
	Provinces   []LocationFacet `json:"provinces"`
	Towns       []LocationFacet `json:"towns"`
	Unspecified int             `json:"unspecified_campaigns"` // discoverable campaigns without a location
	Note        string          `json:"note"`
}

// Locations lists provinces and towns with counts.
func (s *Service) Locations(ctx context.Context) (LocationsResult, error) {
	out := LocationsResult{Country: "ZW", Provinces: []LocationFacet{}, Towns: []LocationFacet{},
		Note: "A campaign's location is where the cause is, chosen by the organiser from a list of provinces and towns. It is never an address."}
	ref, err := s.reference(ctx)
	if err != nil {
		return out, err
	}
	byProv, err := s.countBy(ctx, "province_code")
	if err != nil {
		return out, err
	}
	byLoc, err := s.countBy(ctx, "location_code")
	if err != nil {
		return out, err
	}
	out.Unspecified = byProv[""]
	for _, l := range ref.locations {
		if l.Kind == "PROVINCE" {
			out.Provinces = append(out.Provinces, LocationFacet{Location: l, Discoverable: byProv[l.Code]})
		} else {
			out.Towns = append(out.Towns, LocationFacet{Location: l, Discoverable: byLoc[l.Code]})
		}
	}
	return out, nil
}

// facetTTL: category and location counts are cached in process (they are approximate by design — KI-S16-11 — and a
// GROUP BY over every discoverable row cost ~100 ms at 54 000 campaigns; listings themselves are never cached).
const facetTTL = 30 * time.Second

type facetEntry struct {
	at     time.Time
	counts map[string]int
}

func (s *Service) countBy(ctx context.Context, col string) (map[string]int, error) {
	if col != "category_code" && col != "province_code" && col != "location_code" {
		return nil, errs.New(errs.Internal, "INTERNAL", "bad facet")
	}
	s.facetMu.Lock()
	if e, ok := s.facets[col]; ok && s.now().Sub(e.at) < facetTTL {
		s.facetMu.Unlock()
		return e.counts, nil
	}
	s.facetMu.Unlock()
	out, err := s.countByUncached(ctx, col)
	if err != nil {
		return nil, err
	}
	s.facetMu.Lock()
	if s.facets == nil {
		s.facets = map[string]facetEntry{}
	}
	s.facets[col] = facetEntry{at: s.now(), counts: out}
	s.facetMu.Unlock()
	return out, nil
}

func (s *Service) countByUncached(ctx context.Context, col string) (map[string]int, error) {
	rows, err := s.Pool.Query(ctx, `SELECT coalesce(`+col+`, ''), count(*) FROM app.discovery_campaign_index WHERE discoverable GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}
