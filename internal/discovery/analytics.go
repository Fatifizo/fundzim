package discovery

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/opsview"
)

// Analytics read models (docs/stage-16/analytics.md). Every figure is an aggregate of first-party engagement in this
// environment, with a stated definition. None of them is a donation, an amount, a conversion or a measure of money:
// there are no real donations and analytics are never joined to payments.

// MetricDefinitions are shown with every analytics response.
var MetricDefinitions = map[string]string{
	"views":              "Page views of the public campaign page by browsers that did not identify as automated (raw, capped at 20 per visitor per day).",
	"unique_visitors":    "Estimated distinct visitors per day: a visitor is a keyed hash of network address and browser that changes every day and cannot be linked across days or campaigns. Summed over days, a returning visitor counts once per day.",
	"bot_views_excluded": "Views from clients that identified as crawlers, link-preview fetchers or scripts. Not counted as engagement.",
	"shares":             "Clicks on a share option (WhatsApp, Facebook, X, LinkedIn, email, copy link, the device share sheet). A click is not proof that anything was sent.",
	"referral_visits":    "Views that arrived through a referral link (a code naming only the campaign and the share channel), counted while the code is valid.",
	"traffic_sources":    "Where a view came from, as reported by the page (FundZim search, category, featured, trending, recommendations, internal link, direct or unknown). Unknown when it cannot be told.",
	"impressions":        "Times the campaign card was returned in FundZim search results or discovery lists (server-side count).",
	"clicks":             "Clicks on the campaign card from a FundZim discovery surface.",
	"click_through_bp":   "Clicks per impression in basis points (1/100 of a percent), integer arithmetic. Null when there were no impressions.",
	"donations":          "Not available: FundZim does not accept donations. Views, shares and clicks are never donations.",
}

// AnalyticsNature labels every analytics response (data in this environment are produced by development and test use).
const AnalyticsNature = "MEASURED_NON_PRODUCTION"

// CampaignAnalytics is GET /campaigns/{campaign_id}/analytics (owner and organisation members only).
type CampaignAnalytics struct {
	CampaignID     string            `json:"campaign_id"`
	From           string            `json:"from"`
	To             string            `json:"to"`
	Days           int               `json:"days"`
	Nature         string            `json:"nature"`
	Totals         map[string]int64  `json:"totals"`
	Shares         map[string]int64  `json:"shares_by_channel"`
	Referrals      map[string]int64  `json:"referral_visits_by_channel"`
	Sources        map[string]int64  `json:"traffic_sources"`
	Impressions    map[string]int64  `json:"impressions_by_surface"`
	Clicks         map[string]int64  `json:"clicks_by_surface"`
	ClickThroughBP *int64            `json:"click_through_bp"`
	Daily          []DailyPoint      `json:"daily"`
	Donations      map[string]any    `json:"donations"`
	Definitions    map[string]string `json:"definitions"`
}

// DailyPoint is one day of the main series.
type DailyPoint struct {
	Day            string `json:"day"`
	Views          int64  `json:"views"`
	UniqueVisitors int64  `json:"unique_visitors"`
	Shares         int64  `json:"shares"`
}

// CampaignAnalytics returns a campaign's aggregates for the last days (1–90). Authorisation: campaigns.OwnerAccess (the
// owner or a member of the owning organisation); anyone else gets 404 (existence is not revealed).
func (s *Service) CampaignAnalytics(ctx context.Context, userID, campaignID string, days int) (CampaignAnalytics, error) {
	if days == 0 {
		days = 30
	}
	if days < 1 || days > 90 {
		return CampaignAnalytics{}, httpx.Validation(errs.Detail{Field: "days", Code: "OUT_OF_RANGE"})
	}
	if _, err := s.Campaigns.OwnerAccess(ctx, userID, campaignID, false); err != nil {
		if isNotFound(err) {
			return CampaignAnalytics{}, ErrCampaignMissing
		}
		return CampaignAnalytics{}, err
	}
	to := s.now()
	from := to.AddDate(0, 0, -(days - 1))
	out := CampaignAnalytics{CampaignID: campaignID, From: from.Format("2006-01-02"), To: to.Format("2006-01-02"), Days: days, Nature: AnalyticsNature,
		Totals: map[string]int64{"views": 0, "unique_visitors": 0, "bot_views_excluded": 0, "shares": 0, "referral_visits": 0, "impressions": 0, "clicks": 0},
		Shares: map[string]int64{}, Referrals: map[string]int64{}, Sources: map[string]int64{}, Impressions: map[string]int64{}, Clicks: map[string]int64{},
		Daily: []DailyPoint{}, Donations: map[string]any{"available": false, "message": "Donations are not yet available."}, Definitions: MetricDefinitions}
	rows, err := s.Pool.Query(ctx, `SELECT day::text, metric, channel, count FROM app.discovery_campaign_daily
		WHERE campaign_id = $1 AND day BETWEEN $2::date AND $3::date ORDER BY day`, campaignID, out.From, out.To)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	daily := map[string]*DailyPoint{}
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		k := d.Format("2006-01-02")
		daily[k] = &DailyPoint{Day: k}
		out.Daily = append(out.Daily, DailyPoint{Day: k})
	}
	for rows.Next() {
		var day, metric, channel string
		var n int64
		if err := rows.Scan(&day, &metric, &channel, &n); err != nil {
			return out, err
		}
		p := daily[day]
		switch metric {
		case "VIEW_RAW":
			out.Totals["views"] += n
			out.Sources[channel] += n
			if p != nil {
				p.Views += n
			}
		case "VIEW_UNIQUE":
			out.Totals["unique_visitors"] += n
			if p != nil {
				p.UniqueVisitors += n
			}
		case "VIEW_BOT_EXCLUDED":
			out.Totals["bot_views_excluded"] += n
		case "SHARE_RAW":
			out.Totals["shares"] += n
			out.Shares[channel] += n
			if p != nil {
				p.Shares += n
			}
		case "REFERRAL_VISIT_RAW":
			out.Totals["referral_visits"] += n
			out.Referrals[channel] += n
		case "SEARCH_IMPRESSION", "LISTING_IMPRESSION":
			out.Totals["impressions"] += n
			key := channel
			if key == "" {
				key = map[string]string{"SEARCH_IMPRESSION": "SEARCH", "LISTING_IMPRESSION": "DIRECTORY"}[metric]
			}
			out.Impressions[key] += n
		case "SEARCH_CLICK":
			out.Totals["clicks"] += n
			out.Clicks[channel] += n
		}
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	for i := range out.Daily {
		out.Daily[i] = *daily[out.Daily[i].Day]
	}
	out.ClickThroughBP = ratioBP(out.Totals["clicks"], out.Totals["impressions"])
	return out, nil
}

// ratioBP is num/den in basis points (integer, rounded down); nil when den is 0.
func ratioBP(num, den int64) *int64 {
	if den <= 0 {
		return nil
	}
	v := num * 10000 / den
	return &v
}

// ---- staff overview ---------------------------------------------------------------------------------------------------

// Overview is GET /admin/discovery/overview.
type Overview struct {
	GeneratedAt time.Time         `json:"generated_at"`
	Nature      string            `json:"nature"`
	Index       IndexHealth       `json:"index"`
	Search      map[string]int64  `json:"search_7d_by_outcome"`
	SearchSorts map[string]int64  `json:"search_7d_by_sort"`
	Engagement  map[string]int64  `json:"engagement_7d"`
	Referrals   map[string]int64  `json:"referral_visits_7d_by_channel"`
	Shares      map[string]int64  `json:"shares_7d_by_channel"`
	Abuse       map[string]int64  `json:"abuse_7d_by_kind"`
	Featured    FeaturedSummary   `json:"featured"`
	Trending    []TrendingRow     `json:"trending_top"`
	Ranking     RankingSummary    `json:"ranking"`
	Referral    ReferralSummary   `json:"referral_codes"`
	Definitions map[string]string `json:"definitions"`
}

// IndexHealth describes the search projection.
type IndexHealth struct {
	Discoverable   int64      `json:"discoverable"`
	NotDiscover    int64      `json:"not_discoverable"` // indexed once, now removed (moderation, restriction, unlisting, end)
	Dirty          int64      `json:"backlog"`          // rows waiting for a successful reindex
	OldestDirty    *time.Time `json:"oldest_backlog_at"`
	Failing        int64      `json:"failing"` // dirty rows with 5 or more failed attempts
	LastIndexedAt  *time.Time `json:"last_indexed_at"`
	Status         string     `json:"status"`
	TrendingAt     *time.Time `json:"trending_computed_at"`
	TrendingRanked int64      `json:"trending_ranked"`
}

// FeaturedSummary counts placements.
type FeaturedSummary struct {
	Live      int64 `json:"live"`
	Requested int64 `json:"pending_requests"`
	Scheduled int64 `json:"scheduled"`
}

// TrendingRow is a staff view of a trending score.
type TrendingRow struct {
	CampaignID string     `json:"campaign_id"`
	Slug       string     `json:"slug"`
	Title      string     `json:"title"`
	Score      string     `json:"score"`
	Components Components `json:"components"`
}

// RankingSummary says which weights are in force.
type RankingSummary struct {
	Source  string  `json:"source"`
	Version *int    `json:"version"`
	Pending int64   `json:"pending"`
	InForce Weights `json:"in_force"`
}

// ReferralSummary counts codes.
type ReferralSummary struct {
	Enabled bool  `json:"enabled"`
	Active  int64 `json:"active"`
	Revoked int64 `json:"revoked"`
	Expired int64 `json:"expired"`
}

// IndexBacklogAfter: a dirty row older than this makes the index DEGRADED (engineering default).
const IndexBacklogAfter = 5 * time.Minute

// Overview builds the staff dashboard (aggregates only; no personal data exists in discovery).
func (s *Service) Overview(ctx context.Context) (Overview, error) {
	now := s.now()
	since := now.AddDate(0, 0, -6).Format("2006-01-02")
	o := Overview{GeneratedAt: now, Nature: AnalyticsNature, Search: map[string]int64{}, SearchSorts: map[string]int64{}, Engagement: map[string]int64{},
		Referrals: map[string]int64{}, Shares: map[string]int64{}, Abuse: map[string]int64{}, Trending: []TrendingRow{}, Definitions: MetricDefinitions}
	ix := &o.Index
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE discoverable), count(*) FILTER (WHERE NOT discoverable AND indexed_at IS NOT NULL),
		count(*) FILTER (WHERE dirty_since IS NOT NULL), min(dirty_since), count(*) FILTER (WHERE dirty_since IS NOT NULL AND attempts >= 5), max(indexed_at),
		(SELECT max(computed_at) FROM app.discovery_trending_scores), (SELECT count(*) FROM app.discovery_trending_scores WHERE rank_eligible)
		FROM app.discovery_campaign_index`).Scan(&ix.Discoverable, &ix.NotDiscover, &ix.Dirty, &ix.OldestDirty, &ix.Failing, &ix.LastIndexedAt,
		&ix.TrendingAt, &ix.TrendingRanked); err != nil {
		return o, err
	}
	ix.Status = opsview.Healthy
	if ix.Failing > 0 || (ix.OldestDirty != nil && now.Sub(*ix.OldestDirty) > IndexBacklogAfter) {
		ix.Status = opsview.Degraded
	}
	if ix.LastIndexedAt == nil {
		ix.Status = opsview.StatusUnknown
	}
	if err := scanCounts(ctx, s.Pool, o.Search, `SELECT outcome, sum(count) FROM app.discovery_search_daily WHERE day >= $1::date GROUP BY 1`, since); err != nil {
		return o, err
	}
	if err := scanCounts(ctx, s.Pool, o.SearchSorts, `SELECT sort, sum(count) FROM app.discovery_search_daily WHERE day >= $1::date GROUP BY 1`, since); err != nil {
		return o, err
	}
	if err := scanCounts(ctx, s.Pool, o.Engagement, `SELECT metric, sum(count) FROM app.discovery_campaign_daily WHERE day >= $1::date GROUP BY 1`, since); err != nil {
		return o, err
	}
	if err := scanCounts(ctx, s.Pool, o.Referrals, `SELECT channel, sum(count) FROM app.discovery_campaign_daily WHERE day >= $1::date
		AND metric = 'REFERRAL_VISIT_RAW' GROUP BY 1`, since); err != nil {
		return o, err
	}
	if err := scanCounts(ctx, s.Pool, o.Shares, `SELECT channel, sum(count) FROM app.discovery_campaign_daily WHERE day >= $1::date
		AND metric = 'SHARE_RAW' GROUP BY 1`, since); err != nil {
		return o, err
	}
	if err := scanCounts(ctx, s.Pool, o.Abuse, `SELECT kind, sum(count) FROM app.discovery_abuse_daily WHERE day >= $1::date GROUP BY 1`, since); err != nil {
		return o, err
	}
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status = 'APPROVED' AND starts_at <= $1 AND ends_at > $1),
		count(*) FILTER (WHERE status = 'REQUESTED'), count(*) FILTER (WHERE status = 'APPROVED' AND starts_at > $1)
		FROM app.discovery_featured_placements`, now).Scan(&o.Featured.Live, &o.Featured.Requested, &o.Featured.Scheduled); err != nil {
		return o, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT t.campaign_id::text, i.slug, i.title, t.score::text, t.components FROM app.discovery_trending_scores t
		JOIN app.discovery_campaign_index i ON i.campaign_id = t.campaign_id AND i.discoverable WHERE t.rank_eligible
		ORDER BY t.score DESC, i.published_at DESC, t.campaign_id DESC LIMIT 10`)
	if err != nil {
		return o, err
	}
	for rows.Next() {
		var r TrendingRow
		var raw []byte
		if err := rows.Scan(&r.CampaignID, &r.Slug, &r.Title, &r.Score, &raw); err != nil {
			rows.Close()
			return o, err
		}
		_ = json.Unmarshal(raw, &r.Components)
		o.Trending = append(o.Trending, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return o, err
	}
	w, v, err := s.activeWeights(ctx)
	if err != nil {
		return o, err
	}
	o.Ranking = RankingSummary{Source: "ENGINEERING_DEFAULT", Version: v, InForce: w}
	if v != nil {
		o.Ranking.Source = "APPROVED_POLICY"
	}
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM app.discovery_ranking_policies WHERE status = 'SUBMITTED'`).Scan(&o.Ranking.Pending); err != nil {
		return o, err
	}
	if o.Referral.Enabled, err = s.referralsEnabled(ctx); err != nil {
		return o, err
	}
	err = s.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE revoked_at IS NULL AND expires_at > $1), count(*) FILTER (WHERE revoked_at IS NOT NULL),
		count(*) FILTER (WHERE revoked_at IS NULL AND expires_at <= $1) FROM app.discovery_referral_tokens`, now).
		Scan(&o.Referral.Active, &o.Referral.Revoked, &o.Referral.Expired)
	return o, err
}

func scanCounts(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, into map[string]int64, sql string, args ...any) error {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int64
		if err := rows.Scan(&k, &n); err != nil {
			return err
		}
		if k == "" {
			k = "NONE"
		}
		into[k] = n
	}
	return rows.Err()
}

// ---- sitemap ------------------------------------------------------------------------------------------------------------

// SitemapEntry is a URL candidate.
type SitemapEntry struct {
	Slug      string    `json:"slug"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Sitemap is GET /discovery/sitemap: discoverable campaigns (re-checked in batches), categories with at least one
// discoverable campaign, provinces with at least one. Capped at 50 000 campaigns (the sitemap protocol limit per file).
type Sitemap struct {
	Campaigns  []SitemapEntry `json:"campaigns"`
	Categories []SitemapEntry `json:"categories"`
	Provinces  []SitemapEntry `json:"provinces"`
}

// Sitemap builds the sitemap data.
func (s *Service) Sitemap(ctx context.Context) (Sitemap, error) {
	out := Sitemap{Campaigns: []SitemapEntry{}, Categories: []SitemapEntry{}, Provinces: []SitemapEntry{}}
	rows, err := s.Pool.Query(ctx, `SELECT campaign_id::text, slug, coalesce(indexed_at, published_at) FROM app.discovery_campaign_index
		WHERE discoverable ORDER BY published_at DESC, campaign_id DESC LIMIT 50000`)
	if err != nil {
		return out, err
	}
	var idl []string
	var all []SitemapEntry
	for rows.Next() {
		var id string
		var e SitemapEntry
		if err := rows.Scan(&id, &e.Slug, &e.UpdatedAt); err != nil {
			rows.Close()
			return out, err
		}
		idl, all = append(idl, id), append(all, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	for i := 0; i < len(idl); i += 1000 {
		end := min(i+1000, len(idl))
		m, err := s.Campaigns.Discoverable(ctx, idl[i:end], false)
		if err != nil {
			return out, err
		}
		for j := i; j < end; j++ {
			if m[idl[j]] {
				out.Campaigns = append(out.Campaigns, all[j])
			}
		}
	}
	cats, err := s.Categories(ctx)
	if err != nil {
		return out, err
	}
	for _, c := range cats {
		if c.Discoverable > 0 {
			out.Categories = append(out.Categories, SitemapEntry{Slug: c.Slug, UpdatedAt: c.UpdatedAt})
		}
	}
	locs, err := s.Locations(ctx)
	if err != nil {
		return out, err
	}
	for _, p := range locs.Provinces {
		if p.Discoverable > 0 {
			out.Provinces = append(out.Provinces, SitemapEntry{Slug: p.Slug, UpdatedAt: s.now().Truncate(24 * time.Hour)})
		}
	}
	return out, nil
}

// ---- operations portal ----------------------------------------------------------------------------------------------

// OpsSnapshot is discovery's contribution to the operations portal (ADR-046): index backlog and failures, pending
// featured and ranking approvals. Own tables only.
func (s *Service) OpsSnapshot(ctx context.Context) (opsview.Snapshot, error) {
	const m = "discovery"
	snap := opsview.Snapshot{Module: m, Metrics: []opsview.Metric{}, Approvals: []opsview.Approval{}, Exceptions: []opsview.Exception{}}
	now := s.now()
	var dirty, failing, discoverable int64
	var oldest *time.Time
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE discoverable), count(*) FILTER (WHERE dirty_since IS NOT NULL),
		count(*) FILTER (WHERE dirty_since IS NOT NULL AND attempts >= 5), min(dirty_since) FROM app.discovery_campaign_index`).
		Scan(&discoverable, &dirty, &failing, &oldest); err != nil {
		return snap, err
	}
	b := opsview.Count(m, "discovery.index.backlog", "Campaigns waiting for a search index update", dirty, PermView,
		"app.discovery_campaign_index rows with dirty_since set", "/admin/discovery")
	b.Status = opsview.Healthy
	if failing > 0 || (oldest != nil && now.Sub(*oldest) > IndexBacklogAfter) {
		b.Status = opsview.Degraded
	}
	snap.Metrics = append(snap.Metrics, b, opsview.Count(m, "discovery.index.discoverable", "Campaigns in search", discoverable, PermView,
		"app.discovery_campaign_index discoverable rows", "/admin/discovery"))
	rows, err := s.Pool.Query(ctx, `SELECT campaign_id::text, dirty_since, coalesce(last_error, 'INDEX_FAILED') FROM app.discovery_campaign_index
		WHERE dirty_since IS NOT NULL AND attempts >= 5 ORDER BY dirty_since LIMIT 50`)
	if err != nil {
		return snap, err
	}
	for rows.Next() {
		var id, code string
		var at time.Time
		if err := rows.Scan(&id, &at, &code); err != nil {
			rows.Close()
			return snap, err
		}
		snap.Exceptions = append(snap.Exceptions, opsview.Exception{ID: m + ":index:" + id, SourceModule: m, SourceRef: id, Severity: "SEV3",
			Category: "SEARCH_INDEX_FAILING", Summary: "Search index update keeps failing (" + code + ")", CreatedAt: at, DomainStatus: "DIRTY",
			Permission: PermView, Href: "/admin/discovery"})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return snap, err
	}
	pr, err := s.Pool.Query(ctx, `SELECT id::text, requested_by::text, requested_at, position FROM app.discovery_featured_placements
		WHERE status = 'REQUESTED' ORDER BY requested_at LIMIT 50`)
	if err != nil {
		return snap, err
	}
	for pr.Next() {
		var id, by string
		var at time.Time
		var pos int
		if err := pr.Scan(&id, &by, &at, &pos); err != nil {
			pr.Close()
			return snap, err
		}
		snap.Approvals = append(snap.Approvals, opsview.Approval{ID: m + ":featured:" + id, SourceModule: m, SourceRecordID: id,
			Action: "FEATURED_PLACEMENT", Summary: "Featured placement request (position " + strconv.Itoa(pos) + ")", RequestedBy: by, RequestedAt: at,
			RequiredPermission: PermFeatureApprove, Status: "REQUESTED", Makers: []string{by}, Href: "/admin/discovery/featured"})
	}
	pr.Close()
	if err := pr.Err(); err != nil {
		return snap, err
	}
	rr, err := s.Pool.Query(ctx, `SELECT id::text, proposed_by::text, proposed_at, policy_version FROM app.discovery_ranking_policies
		WHERE status = 'SUBMITTED' ORDER BY proposed_at LIMIT 20`)
	if err != nil {
		return snap, err
	}
	defer rr.Close()
	for rr.Next() {
		var id, by string
		var at time.Time
		var v int
		if err := rr.Scan(&id, &by, &at, &v); err != nil {
			return snap, err
		}
		snap.Approvals = append(snap.Approvals, opsview.Approval{ID: m + ":ranking:" + id, SourceModule: m, SourceRecordID: id,
			Action: "RANKING_POLICY", Summary: "Trending ranking policy v" + strconv.Itoa(v), RequestedBy: by, RequestedAt: at,
			RequiredPermission: PermRankingApprove, Status: "SUBMITTED", Makers: []string{by}, Href: "/admin/discovery/ranking"})
	}
	return snap, rr.Err()
}
