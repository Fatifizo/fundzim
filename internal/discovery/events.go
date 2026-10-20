package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/crypto"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ratelimit"
)

// Engagement pipeline (docs/stage-16/analytics.md). The HTTP handler validates and enqueues; nothing is written to the
// database per request. A Recorder buffers events in memory (bounded) and flushes every FlushInterval: slugs are
// resolved and eligibility re-checked in one batch, per-visitor dedupe keys are upserted in one statement, and
// aggregate counters in another. Losing the buffer (process crash) loses at most one interval of analytics — never
// anything else. Analytics are aggregate: no IP, user id, user agent, cookie or query text is stored.

// Pipeline limits (engineering defaults).
const (
	FlushInterval      = 2 * time.Second
	MaxBuffered        = 5000 // events waiting for a flush; more are dropped (counted as INGEST_DROPPED)
	MaxEventsPerBatch  = 20   // events in one POST
	VisitorDailyCap    = 20   // raw events counted per visitor, campaign, kind and day; beyond is VISITOR_CAP abuse
	EngagementKeysDays = 2    // dedupe keys older than this are purged
)

// Event kinds accepted from clients.
const (
	EvView        = "CAMPAIGN_VIEW"
	EvShare       = "SHARE_CLICK"
	EvSearchClick = "RESULT_CLICK"
)

// Traffic sources and share channels (closed sets; anything else is UNKNOWN or refused).
var (
	ShareChannels  = map[string]bool{"WHATSAPP": true, "FACEBOOK": true, "X": true, "LINKEDIN": true, "EMAIL": true, "COPY_LINK": true, "WEB_SHARE": true}
	TrafficSources = map[string]bool{"DIRECT": true, "SEARCH": true, "CATEGORY": true, "FEATURED": true, "TRENDING": true, "RECOMMENDED": true,
		"INTERNAL": true, "UNKNOWN": true}
	clickSurfaces = map[string]bool{"SEARCH": true, "CATEGORY": true, "FEATURED": true, "TRENDING": true, "RECOMMENDED": true}
	refCodeRe     = regexp.MustCompile(`^[0-9a-hjkmnp-tv-z]{12}$`)
)

// botRe matches user agents that are crawlers, link-preview fetchers, monitors or scripts. Their views are counted as
// VIEW_BOT_EXCLUDED only (never as engagement). A missing user agent counts as automated.
var botRe = regexp.MustCompile(`(?i)bot|crawl|spider|slurp|preview|facebookexternalhit|whatsapp|telegram|discord|skype|embedly|quora link|` +
	`linkedinbot|pinterest|curl|wget|python|java/|go-http-client|okhttp|axios|node-fetch|headless|phantomjs|lighthouse|pagespeed|monitor|uptime|httpclient|scrapy`)

// IsAutomated reports whether a user agent is a known automated client.
func IsAutomated(userAgent string) bool {
	return strings.TrimSpace(userAgent) == "" || botRe.MatchString(userAgent)
}

// EventInput is one client event.
type EventInput struct {
	Type    string `json:"type"`
	Slug    string `json:"slug"`
	Channel string `json:"channel"` // share channel (SHARE_CLICK)
	Source  string `json:"source"`  // traffic source (CAMPAIGN_VIEW) or surface (RESULT_CLICK)
	Ref     string `json:"ref"`     // referral code seen on the landing URL (CAMPAIGN_VIEW)
}

// EventsInput is POST /discovery/events.
type EventsInput struct {
	Events []EventInput `json:"events"`
}

type engagement struct {
	day      string
	kind     string // VIEW | SHARE | SEARCH_CLICK
	slug     string
	channel  string
	ref      string
	bot      bool
	client   string // address, kept in memory only until the flush
	uaDigest string
}

// Ingest validates a batch and enqueues it. Unknown campaigns, bots and invalid referral codes are accepted silently
// (no existence oracle); only malformed input is refused.
func (s *Service) Ingest(ctx context.Context, in EventsInput, userAgent string) (int, error) {
	if len(in.Events) == 0 || len(in.Events) > MaxEventsPerBatch {
		return 0, httpx.Validation(errs.Detail{Field: "events", Code: "INVALID_LENGTH"})
	}
	var det []errs.Detail
	for i, e := range in.Events {
		f := "events[" + itoa(i) + "]"
		if !slugRe.MatchString(e.Slug) || len(e.Slug) > 96 {
			det = append(det, errs.Detail{Field: f + ".slug", Code: "INVALID_FORMAT"})
		}
		switch e.Type {
		case EvView:
			if e.Source != "" && !TrafficSources[e.Source] {
				det = append(det, errs.Detail{Field: f + ".source", Code: "INVALID_VALUE"})
			}
			if e.Ref != "" && !refCodeRe.MatchString(e.Ref) {
				det = append(det, errs.Detail{Field: f + ".ref", Code: "INVALID_FORMAT"})
			}
		case EvShare:
			if !ShareChannels[e.Channel] {
				det = append(det, errs.Detail{Field: f + ".channel", Code: "INVALID_VALUE"})
			}
		case EvSearchClick:
			if !clickSurfaces[e.Source] {
				det = append(det, errs.Detail{Field: f + ".source", Code: "INVALID_VALUE"})
			}
		default:
			det = append(det, errs.Detail{Field: f + ".type", Code: "INVALID_VALUE"})
		}
	}
	if len(det) > 0 {
		return 0, httpx.Validation(det...)
	}
	client := clientKey(ctx)
	if s.Limiter != nil {
		d, err := s.Limiter.Allow(ctx, ratelimit.DefaultPolicies().DiscoveryEventIP, client)
		if err != nil {
			return 0, err
		}
		if !d.Allowed {
			if s.Recorder != nil {
				s.Recorder.Abuse(s.now(), "RATE_LIMITED", "", 1)
			}
			return 0, rateLimited{after: d.RetryAfter}
		}
	}
	if s.Recorder == nil {
		return len(in.Events), nil
	}
	uaSum := sha256.Sum256([]byte(userAgent))
	bot := IsAutomated(userAgent)
	day := s.now().Format("2006-01-02")
	refsOn := false
	for _, e := range in.Events {
		if e.Ref != "" {
			on, err := s.referralsEnabled(ctx)
			if err != nil {
				return 0, err
			}
			refsOn = on
			break
		}
	}
	for _, e := range in.Events {
		g := engagement{day: day, slug: e.Slug, bot: bot, client: client, uaDigest: hex.EncodeToString(uaSum[:8])}
		switch e.Type {
		case EvView:
			g.kind, g.channel = "VIEW", e.Source
			if g.channel == "" {
				g.channel = "UNKNOWN"
			}
			if refsOn {
				g.ref = e.Ref
			}
		case EvShare:
			g.kind, g.channel = "SHARE", e.Channel
		case EvSearchClick:
			g.kind, g.channel = "SEARCH_CLICK", e.Source
		}
		s.Recorder.enqueue(g)
	}
	return len(in.Events), nil
}

func clientKey(ctx context.Context) string {
	if ip := authz.ClientIPFrom(ctx); ip.IsValid() {
		return ip.String()
	}
	return "unknown"
}

// rateLimited carries the limiter's wait so httpx.WriteError sends an accurate Retry-After.
type rateLimited struct{ after time.Duration }

func (e rateLimited) Error() string { return "rate limited" }
func (e rateLimited) Unwrap() error {
	return errs.New(errs.RateLimited, errs.CodeRateLimited, "Too many requests. Try again later.")
}
func (e rateLimited) RetryAfter() time.Duration { return e.after }

// ---- Recorder -----------------------------------------------------------------------------------------------------

type countKey struct{ day, campaign, metric, channel string }
type searchKey struct {
	day, sort, outcome string
	q, f               bool
}
type abuseKey struct{ day, kind, campaign string }

// Recorder buffers engagement and aggregate counts in memory and writes them in batches.
type Recorder struct {
	Pool      *pgxpool.Pool
	Campaigns Campaigns
	Keyed     *crypto.Keyed
	Clock     clock.Clock
	Logger    *slog.Logger

	mu      sync.Mutex
	pending []engagement
	counts  map[countKey]int64
	search  map[searchKey]int64
	abuse   map[abuseKey]int64
	kick    chan struct{}
	done    chan struct{}
	stopped chan struct{}
}

// NewRecorder builds a recorder (Start begins the flush loop).
func NewRecorder(pool *pgxpool.Pool, c Campaigns, keyed *crypto.Keyed, clk clock.Clock, logger *slog.Logger) *Recorder {
	return &Recorder{Pool: pool, Campaigns: c, Keyed: keyed, Clock: clk, Logger: logger, counts: map[countKey]int64{}, search: map[searchKey]int64{},
		abuse: map[abuseKey]int64{}, kick: make(chan struct{}, 1), done: make(chan struct{}), stopped: make(chan struct{})}
}

// Count adds n to an aggregate metric.
func (r *Recorder) Count(at time.Time, campaignID, metric, channel string, n int64) {
	if campaignID == "" || n <= 0 {
		return
	}
	r.mu.Lock()
	r.counts[countKey{at.UTC().Format("2006-01-02"), campaignID, metric, channel}] += n
	r.mu.Unlock()
}

// Search counts one search (never the query text).
func (r *Recorder) Search(at time.Time, sort string, hasQuery, hasFilters bool, outcome string) {
	r.mu.Lock()
	r.search[searchKey{at.UTC().Format("2006-01-02"), sort, outcome, hasQuery, hasFilters}]++
	r.mu.Unlock()
}

// Abuse counts an abuse indicator (campaign "" when none).
func (r *Recorder) Abuse(at time.Time, kind, campaignID string, n int64) {
	r.mu.Lock()
	r.abuse[abuseKey{at.UTC().Format("2006-01-02"), kind, campaignID}] += n
	r.mu.Unlock()
}

func (r *Recorder) enqueue(e engagement) {
	r.mu.Lock()
	if len(r.pending) >= MaxBuffered {
		r.abuse[abuseKey{e.day, "INGEST_DROPPED", ""}]++
		r.mu.Unlock()
		return
	}
	r.pending = append(r.pending, e)
	full := len(r.pending) >= MaxBuffered/10
	r.mu.Unlock()
	if full {
		select {
		case r.kick <- struct{}{}:
		default:
		}
	}
}

// Start runs the flush loop until Stop.
func (r *Recorder) Start() {
	go func() {
		defer close(r.stopped)
		t := time.NewTicker(FlushInterval)
		defer t.Stop()
		for {
			select {
			case <-r.done:
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				r.Flush(ctx)
				cancel()
				return
			case <-t.C:
			case <-r.kick:
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			r.Flush(ctx)
			cancel()
		}
	}()
}

// Stop flushes once more and ends the loop.
func (r *Recorder) Stop() {
	select {
	case <-r.done:
		return
	default:
	}
	close(r.done)
	<-r.stopped
}

// Flush writes everything buffered. Errors are logged and the batch is discarded (analytics are best effort; the
// discarded events are counted as INGEST_DROPPED when that count itself can be written).
func (r *Recorder) Flush(ctx context.Context) {
	r.mu.Lock()
	pending, counts, search, abuse := r.pending, r.counts, r.search, r.abuse
	r.pending, r.counts, r.search, r.abuse = nil, map[countKey]int64{}, map[searchKey]int64{}, map[abuseKey]int64{}
	r.mu.Unlock()
	if len(pending) > 0 {
		if err := r.resolve(ctx, pending, counts, abuse); err != nil {
			r.Logger.WarnContext(ctx, "discovery: engagement flush failed", "error_category", "dependency", "error", err.Error(), "events", len(pending))
			abuse[abuseKey{pending[0].day, "INGEST_DROPPED", ""}] += int64(len(pending))
		}
	}
	if err := r.write(ctx, counts, search, abuse); err != nil {
		r.Logger.WarnContext(ctx, "discovery: counter flush failed", "error_category", "dependency", "error", err.Error())
	}
}

type dedupeKey struct {
	day, campaign, kind string
	visitor             string
}

// resolve turns raw engagement into counts: slug → id, eligibility, referral validity, per-visitor dedupe and caps.
func (r *Recorder) resolve(ctx context.Context, pending []engagement, counts map[countKey]int64, abuse map[abuseKey]int64) error {
	slugSet := map[string]bool{}
	refSet := map[string]bool{}
	for _, e := range pending {
		slugSet[e.slug] = true
		if e.ref != "" {
			refSet[e.ref] = true
		}
	}
	slugs := make([]string, 0, len(slugSet))
	for s := range slugSet {
		slugs = append(slugs, s)
	}
	idm, err := r.Campaigns.IDsBySlugs(ctx, slugs)
	if err != nil {
		return err
	}
	idl := make([]string, 0, len(idm))
	for _, id := range idm {
		idl = append(idl, id)
	}
	disc, err := r.Campaigns.Discoverable(ctx, idl, false)
	if err != nil {
		return err
	}
	refs := map[string]refToken{}
	if len(refSet) > 0 {
		codes := make([]string, 0, len(refSet))
		for c := range refSet {
			codes = append(codes, c)
		}
		if refs, err = lookupRefs(ctx, r.Pool, codes); err != nil {
			return err
		}
	}
	now := r.Clock.Now().UTC()
	// group events per dedupe key
	type agg struct {
		n       int64
		channel map[string]int64
	}
	groups := map[dedupeKey]*agg{}
	var order []dedupeKey
	add := func(k dedupeKey, channel string) {
		g := groups[k]
		if g == nil {
			g = &agg{channel: map[string]int64{}}
			groups[k] = g
			order = append(order, k)
		}
		g.n++
		g.channel[channel]++
	}
	for _, e := range pending {
		id, ok := idm[e.slug]
		if !ok || !disc[id] {
			abuse[abuseKey{e.day, "NOT_DISCOVERABLE", ""}]++
			continue
		}
		if e.bot {
			if e.kind == "VIEW" {
				counts[countKey{e.day, id, "VIEW_BOT_EXCLUDED", ""}]++
			}
			abuse[abuseKey{e.day, "BOT_FILTERED", id}]++
			continue
		}
		visitor := e.client + "|" + e.uaDigest
		add(dedupeKey{e.day, id, e.kind, visitor}, e.channel)
		if e.kind == "VIEW" && e.ref != "" {
			t, ok := refs[e.ref]
			if !ok || t.campaignID != id || t.revoked || !now.Before(t.expiresAt) {
				abuse[abuseKey{e.day, "INVALID_REFERRAL", id}]++
				continue
			}
			add(dedupeKey{e.day, id, "REFERRAL_VISIT", visitor}, t.channel)
		}
	}
	if len(order) == 0 {
		return nil
	}
	days, camps, kinds := make([]string, len(order)), make([]string, len(order)), make([]string, len(order))
	keys, hits := make([][]byte, len(order)), make([]int64, len(order))
	for i, k := range order {
		days[i], camps[i], kinds[i], hits[i] = k.day, k.campaign, k.kind, groups[k].n
		keys[i] = r.Keyed.Sum("discovery.visitor.v1", k.day, k.campaign, k.kind, k.visitor)
	}
	rows, err := r.Pool.Query(ctx, `INSERT INTO app.discovery_engagement_keys AS k (day, campaign_id, kind, visitor_key, hits)
		SELECT d::date, c::uuid, kd, vk, h FROM unnest($1::text[], $2::text[], $3::text[], $4::bytea[], $5::int[]) AS u(d, c, kd, vk, h)
		ON CONFLICT (day, campaign_id, kind, visitor_key) DO UPDATE SET hits = k.hits + EXCLUDED.hits
		RETURNING day::text, campaign_id::text, kind, visitor_key, hits`, days, camps, kinds, keys, hits)
	if err != nil {
		return err
	}
	after := map[string]int64{}
	for rows.Next() {
		var d, c, kd string
		var vk []byte
		var h int64
		if err := rows.Scan(&d, &c, &kd, &vk, &h); err != nil {
			rows.Close()
			return err
		}
		after[d+"|"+c+"|"+kd+"|"+string(vk)] = h
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for i, k := range order {
		g := groups[k]
		total := after[k.day+"|"+k.campaign+"|"+k.kind+"|"+string(keys[i])]
		prev := total - g.n
		allowed := VisitorDailyCap - prev
		if allowed < 0 {
			allowed = 0
		}
		counted := g.n
		if counted > allowed {
			abuse[abuseKey{k.day, "VISITOR_CAP", k.campaign}] += counted - allowed
			counted = allowed
		}
		raw, uniq := map[string]string{"VIEW": "VIEW_RAW", "SHARE": "SHARE_RAW", "REFERRAL_VISIT": "REFERRAL_VISIT_RAW", "SEARCH_CLICK": "SEARCH_CLICK"}[k.kind],
			map[string]string{"VIEW": "VIEW_UNIQUE", "SHARE": "SHARE_UNIQUE", "REFERRAL_VISIT": "REFERRAL_VISIT_UNIQUE"}[k.kind]
		// raw counts by channel, within the cap (channels in a stable order)
		left := counted
		for _, ch := range sortedKeys(g.channel) {
			n := g.channel[ch]
			if n > left {
				n = left
			}
			if n > 0 {
				counts[countKey{k.day, k.campaign, raw, ch}] += n
				left -= n
			}
		}
		if prev == 0 && uniq != "" {
			ch := ""
			if k.kind == "REFERRAL_VISIT" {
				ch = sortedKeys(g.channel)[0]
			}
			counts[countKey{k.day, k.campaign, uniq, ch}]++
		}
	}
	return nil
}

func (r *Recorder) write(ctx context.Context, counts map[countKey]int64, search map[searchKey]int64, abuse map[abuseKey]int64) error {
	if len(counts)+len(search)+len(abuse) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	if len(counts) > 0 {
		var d, c, m, ch []string
		var n []int64
		for k, v := range counts {
			d, c, m, ch, n = append(d, k.day), append(c, k.campaign), append(m, k.metric), append(ch, k.channel), append(n, v)
		}
		batch.Queue(`INSERT INTO app.discovery_campaign_daily AS t (day, campaign_id, metric, channel, count)
			SELECT d::date, c::uuid, m, ch, n FROM unnest($1::text[], $2::text[], $3::text[], $4::text[], $5::bigint[]) AS u(d, c, m, ch, n)
			ON CONFLICT (day, campaign_id, metric, channel) DO UPDATE SET count = t.count + EXCLUDED.count`, d, c, m, ch, n)
	}
	if len(search) > 0 {
		var d, so, o []string
		var q, f []bool
		var n []int64
		for k, v := range search {
			d, so, o, q, f, n = append(d, k.day), append(so, k.sort), append(o, k.outcome), append(q, k.q), append(f, k.f), append(n, v)
		}
		batch.Queue(`INSERT INTO app.discovery_search_daily AS t (day, sort, has_query, has_filters, outcome, count)
			SELECT d::date, so, q, f, o, n FROM unnest($1::text[], $2::text[], $3::bool[], $4::bool[], $5::text[], $6::bigint[]) AS u(d, so, q, f, o, n)
			ON CONFLICT (day, sort, has_query, has_filters, outcome) DO UPDATE SET count = t.count + EXCLUDED.count`, d, so, q, f, o, n)
	}
	for k, v := range abuse {
		var camp any
		if k.campaign != "" {
			camp = k.campaign
		}
		batch.Queue(`INSERT INTO app.discovery_abuse_daily AS t (day, kind, campaign_id, count) VALUES ($1::date, $2, $3::uuid, $4)
			ON CONFLICT (day, kind, coalesce(campaign_id, '00000000-0000-0000-0000-000000000000'::uuid)) DO UPDATE SET count = t.count + EXCLUDED.count`,
			k.day, k.kind, camp, v)
	}
	return r.Pool.SendBatch(ctx, batch).Close()
}

// PurgeEngagementKeys deletes dedupe keys older than EngagementKeysDays (worker job; they are not history).
func PurgeEngagementKeys(ctx context.Context, pool *pgxpool.Pool, now time.Time) (int64, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM app.discovery_engagement_keys WHERE day < $1::date`, now.UTC().AddDate(0, 0, -EngagementKeysDays).Format("2006-01-02"))
	return tag.RowsAffected(), err
}
