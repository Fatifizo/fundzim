package campaigns

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/kyc"
	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/ratelimit"
	"github.com/Fatifizo/fundzim/internal/users"
)

// Stage 7 public experience (ADR-038): trust indicators, canonical slugs, featured selection, categories,
// sitemap data, first-party analytics counters and public reports with a staff triage queue.

// Trust indicators are shown only when true now (ADR-038 §5). They describe verification steps, never the
// truth of a campaign's claims.
type Trust struct {
	OrganiserIdentityVerified bool `json:"organiser_identity_verified"`
	OrganisationVerified      bool `json:"organisation_verified"`
	BeneficiaryVerified       bool `json:"beneficiary_verified"`
	CampaignReviewed          bool `json:"campaign_reviewed"`
}

func (s *Service) trust(ctx context.Context, ownerUser, ownerOrg, beneficiaryID *string, reviewed bool) (Trust, error) {
	t := Trust{CampaignReviewed: reviewed}
	if ownerUser != nil {
		st, err := s.KYC.Level(ctx, *ownerUser) // Level maps an expired level to UNVERIFIED
		if err != nil {
			return t, err
		}
		t.OrganiserIdentityVerified = st.Status == kyc.ProfileActive && kyc.AtLeast(st.Level, kyc.LevelIdentity)
	}
	if ownerOrg != nil {
		st, err := s.KYC.OrganisationLevel(ctx, *ownerOrg)
		if err != nil {
			return t, err
		}
		t.OrganisationVerified = st.Status == kyc.ProfileActive && kyc.AtLeast(st.Level, kyc.OrgLevelKYB)
	}
	if beneficiaryID != nil {
		bt, status, err := s.Beneficiaries.StatusOf(ctx, *beneficiaryID)
		if err != nil {
			return t, err
		}
		t.BeneficiaryVerified = status == "APPROVED" || (bt == "SELF" && t.OrganiserIdentityVerified)
	}
	return t, nil
}

var publicCodeTailRe = regexp.MustCompile(`-([0-9a-hjkmnp-tv-z]{10})$`)

// resolvePublic finds a live campaign by its exact slug or, failing that, by the public code at the end of the
// slug (an old or mistyped title part); the caller redirects to the canonical slug (ADR-038 §6).
func (s *Service) resolvePublic(ctx context.Context, slug string) (Access, error) {
	a, err := s.PublicBySlug(ctx, slug)
	if err == nil || !errors.Is(err, ErrNotFound) {
		return a, err
	}
	m := publicCodeTailRe.FindStringSubmatch(strings.ToLower(slug))
	if m == nil {
		return Access{}, ErrNotFound
	}
	return s.publicByCode(ctx, strings.ToUpper(m[1]))
}

func (s *Service) publicByCode(ctx context.Context, code string) (Access, error) {
	if !regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{10}$`).MatchString(code) {
		return Access{}, ErrNotFound
	}
	a, err := scanAccess(s.Pool.QueryRow(ctx, accessSelect+` WHERE public_code = $1`, code))
	if err != nil || !a.Public {
		return Access{}, ErrNotFound
	}
	return a, nil
}

// CanonicalByCode returns the canonical slug of a live campaign (short links /c/{code}).
func (s *Service) CanonicalByCode(ctx context.Context, code string) (string, error) {
	a, err := s.publicByCode(ctx, strings.ToUpper(code))
	if err != nil {
		return "", err
	}
	var slug string
	err = s.Pool.QueryRow(ctx, `SELECT slug FROM app.campaigns WHERE id = $1`, a.CampaignID).Scan(&slug)
	return slug, err
}

// ---- cards (search, featured, category pages) -------------------------------------------------------------

// CardRow is the internal row behind a public card.
type cardRow struct {
	PublicCard
	id                       string
	ownerUser, ownerOrg, ben *string
	orderKey                 time.Time
}

const cardSelect = `SELECT c.id, c.slug, v.title, v.summary, v.category_code, v.goal_amount_minor, v.goal_currency, c.status,
	(SELECT u.m::text FROM unnest(v.media_ids) WITH ORDINALITY AS u(m, o) JOIN app.campaign_media cm ON cm.id = u.m
	   AND cm.status = 'APPROVED' AND cm.kind = 'COVER' ORDER BY u.o LIMIT 1),
	c.published_at, c.created_at, c.owner_user_id, c.owner_organisation_id, v.beneficiary_id`

func scanCards(rows pgx.Rows, withKey bool) ([]cardRow, error) {
	defer rows.Close()
	var out []cardRow
	for rows.Next() {
		var c cardRow
		var amount int64
		dst := []any{&c.id, &c.Slug, &c.Title, &c.Summary, &c.Category, &amount, &c.Goal.Currency, &c.Status, &c.CoverMedia, &c.PublishedAt,
			&c.CreatedAt, &c.ownerUser, &c.ownerOrg, &c.ben}
		if withKey {
			dst = append(dst, &c.orderKey)
		}
		if err := rows.Scan(dst...); err != nil {
			return nil, err
		}
		c.Goal.AmountMinor = itoa(amount)
		out = append(out, c)
	}
	return out, rows.Err()
}

// enrichCards adds organiser names and trust indicators with batched lookups (one query per kind, not per
// card; docs/stage-7/performance.md R5).
func (s *Service) enrichCards(ctx context.Context, rows []cardRow) ([]PublicCard, error) {
	var userIDs, orgIDs, benIDs []string
	for _, r := range rows {
		if r.ownerUser != nil {
			userIDs = append(userIDs, *r.ownerUser)
		}
		if r.ownerOrg != nil {
			orgIDs = append(orgIDs, *r.ownerOrg)
		}
		if r.ben != nil {
			benIDs = append(benIDs, *r.ben)
		}
	}
	names, err := users.DisplayNames(ctx, s.Pool, userIDs)
	if err != nil {
		return nil, err
	}
	userLevels, err := s.KYC.Levels(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	orgLevels, err := s.KYC.OrganisationLevels(ctx, orgIDs)
	if err != nil {
		return nil, err
	}
	bens, err := s.Beneficiaries.StatusesOf(ctx, benIDs)
	if err != nil {
		return nil, err
	}
	orgNames := map[string]string{}
	out := make([]PublicCard, 0, len(rows))
	for _, r := range rows {
		card := r.PublicCard
		if r.ownerUser != nil {
			card.Organiser = map[string]any{"type": "INDIVIDUAL", "display_name": names[*r.ownerUser]}
		} else {
			n, ok := orgNames[*r.ownerOrg]
			if !ok {
				if n, _, _, err = s.Orgs.Summary(ctx, *r.ownerOrg); err != nil {
					return nil, err
				}
				orgNames[*r.ownerOrg] = n
			}
			card.Organiser = map[string]any{"type": "ORGANISATION", "display_name": n}
		}
		t := Trust{CampaignReviewed: true}
		if r.ownerUser != nil {
			st := userLevels[*r.ownerUser]
			t.OrganiserIdentityVerified = st.Status == kyc.ProfileActive && kyc.AtLeast(st.Level, kyc.LevelIdentity)
		}
		if r.ownerOrg != nil {
			st := orgLevels[*r.ownerOrg]
			t.OrganisationVerified = st.Status == kyc.ProfileActive && kyc.AtLeast(st.Level, kyc.OrgLevelKYB)
		}
		if r.ben != nil {
			b := bens[*r.ben]
			t.BeneficiaryVerified = b[1] == "APPROVED" || (b[0] == "SELF" && t.OrganiserIdentityVerified)
		}
		card.Trust = &t
		out = append(out, card)
	}
	return out, nil
}

// Count is an exact count capped for cost (ADR-038; contract §3).
type Count struct {
	Value  int  `json:"value"`
	Capped bool `json:"capped"`
}

const searchCountCap = 1000

// FeaturedCard is a card with the reason it was selected.
type FeaturedCard struct {
	PublicCard
	Selection string `json:"selection"` // STAFF_PICK | RECENTLY_PUBLISHED
}

// Featured returns staff picks first, then recently published live PUBLIC campaigns (ADR-038 §2). No
// popularity or paid ranking exists.
func (s *Service) Featured(ctx context.Context, limit int) ([]FeaturedCard, error) {
	if limit <= 0 || limit > 12 {
		limit = 6
	}
	rows, err := s.Pool.Query(ctx, cardSelect+`, f.position::text FROM app.campaign_featured f
		JOIN app.campaigns c ON c.id = f.campaign_id AND f.removed_at IS NULL
		JOIN app.campaign_versions v ON v.id = c.approved_version_id
		WHERE c.status IN ('ACTIVE','PAUSED','COMPLETED') AND c.visibility = 'PUBLIC'
		ORDER BY f.position, f.added_at LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	picks, err := scanCardsWithIgnored(rows)
	if err != nil {
		return nil, err
	}
	seen := []string{}
	for _, p := range picks {
		seen = append(seen, p.id)
	}
	if len(picks) < limit {
		rows, err := s.Pool.Query(ctx, cardSelect+` FROM app.campaigns c JOIN app.campaign_versions v ON v.id = c.approved_version_id
			WHERE c.status IN ('ACTIVE','PAUSED') AND c.visibility = 'PUBLIC' AND NOT (c.id = ANY ($1::uuid[]))
			ORDER BY c.published_at DESC, c.id DESC LIMIT $2`, seen, limit-len(picks))
		if err != nil {
			return nil, err
		}
		recent, err := scanCards(rows, false)
		if err != nil {
			return nil, err
		}
		picks = append(picks, recent...)
	}
	cards, err := s.enrichCards(ctx, picks)
	if err != nil {
		return nil, err
	}
	out := make([]FeaturedCard, 0, len(cards))
	for i, c := range cards {
		sel := "RECENTLY_PUBLISHED"
		if i < len(seen) {
			sel = "STAFF_PICK"
		}
		out = append(out, FeaturedCard{PublicCard: c, Selection: sel})
	}
	return out, nil
}

// scanCardsWithIgnored scans card rows followed by one extra ignored text column.
func scanCardsWithIgnored(rows pgx.Rows) ([]cardRow, error) {
	defer rows.Close()
	var out []cardRow
	for rows.Next() {
		var c cardRow
		var amount int64
		var ignored string
		if err := rows.Scan(&c.id, &c.Slug, &c.Title, &c.Summary, &c.Category, &amount, &c.Goal.Currency, &c.Status, &c.CoverMedia,
			&c.PublishedAt, &c.CreatedAt, &c.ownerUser, &c.ownerOrg, &c.ben, &ignored); err != nil {
			return nil, err
		}
		c.Goal.AmountMinor = itoa(amount)
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---- categories ---------------------------------------------------------------------------------------------

// PublicCategory is an active category with its live public campaign count.
type PublicCategory struct {
	Code          string    `json:"code"`
	Slug          string    `json:"slug"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	DisplayOrder  int       `json:"display_order"`
	Sensitive     bool      `json:"sensitive"`
	LiveCampaigns int       `json:"live_campaigns"`
	UpdatedAt     time.Time `json:"-"`
}

// PublicCategories lists active categories (or one by slug) with exact live PUBLIC campaign counts.
func (s *Service) PublicCategories(ctx context.Context, slug string) ([]PublicCategory, error) {
	// one grouped pass over live PUBLIC campaigns (performance.md R2), not one count per category
	rows, err := s.Pool.Query(ctx, `SELECT k.code, k.slug, k.name, k.description, k.display_order, k.sensitive, k.updated_at, coalesce(n.live, 0)
		FROM app.campaign_categories k
		LEFT JOIN (SELECT v.category_code, count(*) AS live FROM app.campaigns c
		           JOIN app.campaign_versions v ON v.id = c.approved_version_id
		           WHERE c.status IN ('ACTIVE','PAUSED','COMPLETED') AND c.visibility = 'PUBLIC'
		             AND ($1 = '' OR v.category_code = (SELECT code FROM app.campaign_categories WHERE slug = $1))
		           GROUP BY v.category_code) n ON n.category_code = k.code
		WHERE k.active AND ($1 = '' OR k.slug = $1) ORDER BY k.display_order, k.code`, slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PublicCategory{}
	for rows.Next() {
		var c PublicCategory
		if err := rows.Scan(&c.Code, &c.Slug, &c.Name, &c.Description, &c.DisplayOrder, &c.Sensitive, &c.UpdatedAt, &c.LiveCampaigns); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SitemapEntry is a URL candidate for the sitemap (live PUBLIC campaigns and active categories only).
type SitemapEntry struct {
	Slug      string    `json:"slug"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Sitemap returns live PUBLIC campaigns (capped at 50 000) and active categories.
func (s *Service) Sitemap(ctx context.Context) ([]SitemapEntry, []SitemapEntry, error) {
	rows, err := s.Pool.Query(ctx, `SELECT slug, updated_at FROM app.campaigns WHERE status IN ('ACTIVE','PAUSED','COMPLETED')
		AND visibility = 'PUBLIC' ORDER BY published_at DESC LIMIT 50000`)
	if err != nil {
		return nil, nil, err
	}
	camps, err := pgx.CollectRows(rows, pgx.RowToStructByPos[SitemapEntry])
	if err != nil {
		return nil, nil, err
	}
	cats, err := s.PublicCategories(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	out := make([]SitemapEntry, 0, len(cats))
	for _, c := range cats {
		out = append(out, SitemapEntry{Slug: c.Slug, UpdatedAt: c.UpdatedAt})
	}
	if camps == nil {
		camps = []SitemapEntry{}
	}
	return camps, out, nil
}

// ---- analytics (aggregate counters only; ADR-038 §4) -------------------------------------------------------

var eventTypes = map[string]bool{"CAMPAIGN_PAGE_VIEW": true, "CAMPAIGN_SHARE_CLICK": true, "CAMPAIGN_SEARCH": true, "CATEGORY_VIEW": true}
var shareChannels = map[string]bool{"WHATSAPP": true, "FACEBOOK": true, "X": true, "EMAIL": true, "COPY_LINK": true, "WEB_SHARE": true}

// EventInput is POST /public/events.
type EventInput struct {
	Type     string `json:"type"`
	Slug     string `json:"slug"`
	Category string `json:"category"`
	Channel  string `json:"channel"`
}

// RecordEvent increments a daily counter. Unknown campaigns or categories are ignored silently (no
// existence oracle); nothing about the visitor is stored.
func (s *Service) RecordEvent(ctx context.Context, in EventInput) error {
	if !eventTypes[in.Type] {
		return httpx.Validation(errs.Detail{Field: "type", Code: "INVALID_VALUE"})
	}
	if in.Channel != "" && !shareChannels[in.Channel] {
		return httpx.Validation(errs.Detail{Field: "channel", Code: "INVALID_VALUE"})
	}
	if err := s.throttle(ctx, ratelimit.DefaultPolicies().PublicEventIP, clientKey(ctx)); err != nil {
		return err
	}
	subject, channel := "", ""
	switch in.Type {
	case "CAMPAIGN_PAGE_VIEW", "CAMPAIGN_SHARE_CLICK":
		a, err := s.PublicBySlug(ctx, in.Slug)
		if err != nil {
			return nil // ignored: not a public campaign
		}
		subject = in.Slug
		_ = a
		if in.Type == "CAMPAIGN_SHARE_CLICK" {
			if in.Channel == "" {
				return httpx.Validation(errs.Detail{Field: "channel", Code: "MISSING_FIELD"})
			}
			channel = in.Channel
		}
	case "CATEGORY_VIEW":
		cats, err := s.PublicCategories(ctx, in.Category)
		if err != nil || len(cats) != 1 || in.Category == "" {
			return nil
		}
		subject = cats[0].Code
	case "CAMPAIGN_SEARCH":
		channel = "WITHOUT_CATEGORY"
		if in.Category != "" {
			channel = "WITH_CATEGORY"
		}
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO app.public_event_counters (day, event_type, subject, channel, count) VALUES ($1,$2,$3,$4,1)
		ON CONFLICT (day, event_type, subject, channel) DO UPDATE SET count = app.public_event_counters.count + 1`,
		s.now().Format("2006-01-02"), in.Type, subject, channel)
	return err
}

func (s *Service) throttle(ctx context.Context, p ratelimit.Policy, key string) error {
	if s.Limiter == nil {
		return errors.New("campaigns: rate limiter not configured")
	}
	d, err := s.Limiter.Allow(ctx, p, key)
	if err != nil {
		return err
	}
	if !d.Allowed {
		return rateLimited{after: d.RetryAfter, err: errs.New(errs.RateLimited, errs.CodeRateLimited, "Too many requests. Try again later.")}
	}
	return nil
}

// rateLimited carries the limiter's retry delay so the handler can send an accurate Retry-After.
type rateLimited struct {
	after time.Duration
	err   *errs.Error
}

func (e rateLimited) Error() string { return e.err.Error() }
func (e rateLimited) Unwrap() error { return e.err }

// RetryAfter lets httpx.WriteError send the limiter's real wait.
func (e rateLimited) RetryAfter() time.Duration { return e.after }

// failRated writes a (possibly rate-limited) error; Retry-After comes from the error itself.
func (s *Service) failRated(w http.ResponseWriter, r *http.Request, err error) { s.fail(w, r, err) }

func clientKey(ctx context.Context) string {
	if ip := authz.ClientIPFrom(ctx); ip.IsValid() {
		return ip.String()
	}
	return "unknown"
}

// ---- reports (ADR-038 §3) ----------------------------------------------------------------------------------

var reportReasons = map[string]bool{"MISLEADING_INFORMATION": true, "IMPERSONATION": true, "SUSPICIOUS_ACTIVITY": true,
	"INAPPROPRIATE_CONTENT": true, "PRIVACY_CONCERN": true, "OTHER": true}

// ReportInput is POST /public/campaigns/{slug}/reports. Website is a honeypot and must be empty.
type ReportInput struct {
	Reason       string `json:"reason"`
	Details      string `json:"details"`
	ContactEmail string `json:"contact_email"`
	Website      string `json:"website"`
}

// Report records a public report. Accepted reports, duplicates and honeypot hits all look the same to the
// caller. A report never changes the campaign's status.
func (s *Service) Report(ctx context.Context, slug string, in ReportInput) error {
	ip := clientKey(ctx)
	if err := s.throttle(ctx, ratelimit.DefaultPolicies().PublicReportIP, ip); err != nil {
		return err // before anything else: honeypot hits and invalid submissions count too
	}
	a, err := s.resolvePublic(ctx, slug)
	if err != nil {
		return err
	}
	var det []errs.Detail
	if !reportReasons[in.Reason] {
		det = append(det, errs.Detail{Field: "reason", Code: "INVALID_VALUE"})
	}
	minDetails := 0
	if in.Reason == "OTHER" {
		minDetails = 10
	}
	details, code := CleanText(in.Details, minDetails, 2000, true)
	if code != "" {
		det = append(det, errs.Detail{Field: "details", Code: code})
	}
	email := ""
	if strings.TrimSpace(in.ContactEmail) != "" {
		if email, err = users.NormalizeEmail(in.ContactEmail); err != nil {
			det = append(det, errs.Detail{Field: "contact_email", Code: "INVALID_FORMAT"})
		}
	}
	if len(det) > 0 {
		return httpx.Validation(det...)
	}
	if strings.TrimSpace(in.Website) != "" {
		return nil // honeypot: silently dropped
	}
	if err := s.throttle(ctx, ratelimit.DefaultPolicies().PublicReportCampaign, a.CampaignID); err != nil {
		return err
	}
	if s.Keyed == nil || s.AEAD == nil {
		return errors.New("campaigns: report keys not configured")
	}
	var reporterUser any
	reporterKey := s.Keyed.Sum("campaign_report.reporter", "ip", ip)
	if p := authz.PrincipalFrom(ctx); p != nil && p.Kind == authz.KindUser {
		reporterUser, reporterKey = p.UserID, s.Keyed.Sum("campaign_report.reporter", "user", p.UserID)
	}
	id := ids.New()
	var bidx, ct any
	if email != "" {
		bidx = s.Keyed.Sum("campaign_report.contact", email)
		sealed, err := s.AEAD.Seal([]byte(email), []byte("app.campaign_reports:"+id+":contact_email"))
		if err != nil {
			return err
		}
		ct = sealed
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO app.campaign_reports (id, campaign_id, reason, details, reporter_user_id, reporter_key, report_day,
			contact_email_bidx, contact_email_ct, status, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'OPEN',$10)
			ON CONFLICT ON CONSTRAINT uq_campaign_reports_dedupe DO NOTHING`,
			id, a.CampaignID, in.Reason, nullable(details), reporterUser, reporterKey, s.now().Format("2006-01-02"), bidx, ct, s.now())
		if err != nil || tag.RowsAffected() == 0 {
			return err // duplicate for today: accepted silently
		}
		return audit.Record(ctx, tx, audit.Event{Action: "campaign.report_received", TargetType: "campaign", TargetID: a.CampaignID,
			Metadata: map[string]any{"report_id": id, "reason": in.Reason}})
	})
}

// ReportItem is a staff queue row. The contact email is never listed.
type ReportItem struct {
	ID          string     `json:"id"`
	CampaignID  string     `json:"campaign_id"`
	Title       string     `json:"campaign_title"`
	Slug        string     `json:"campaign_slug"`
	Status      string     `json:"status"`
	Reason      string     `json:"reason"`
	Details     *string    `json:"details"`
	HasContact  bool       `json:"has_contact"`
	Signed      bool       `json:"reporter_signed_in"`
	CreatedAt   time.Time  `json:"created_at"`
	TriagedAt   *time.Time `json:"triaged_at"`
	TriageCode  *string    `json:"triage_reason_code"`
	TriageNote  *string    `json:"triage_note"`
	CampaignNow string     `json:"campaign_status"`
}

// Reports lists reports for staff (oldest first within a status; keyset cursor).
func (s *Service) Reports(ctx context.Context, status, cursor string, limit int) ([]ReportItem, string, error) {
	if status == "" {
		status = "OPEN"
	}
	if status != "OPEN" && status != "ESCALATED" && status != "DISMISSED" {
		return nil, "", httpx.Validation(errs.Detail{Field: "status", Code: "INVALID_VALUE"})
	}
	limit = pageLimit(limit)
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
	rows, err := s.Pool.Query(ctx, `SELECT r.id, r.campaign_id, c.title, c.slug, r.status, r.reason, r.details, r.contact_email_ct IS NOT NULL,
		r.reporter_user_id IS NOT NULL, r.created_at, r.triaged_at, r.triage_reason_code, r.triage_note, c.status
		FROM app.campaign_reports r JOIN app.campaigns c ON c.id = r.campaign_id
		WHERE r.status = $1 AND ($2::timestamptz IS NULL OR (r.created_at, r.id) > ($2, $3::uuid))
		ORDER BY r.created_at, r.id LIMIT $4`, status, after, afterID, limit)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []ReportItem{}
	for rows.Next() {
		var x ReportItem
		if err := rows.Scan(&x.ID, &x.CampaignID, &x.Title, &x.Slug, &x.Status, &x.Reason, &x.Details, &x.HasContact, &x.Signed, &x.CreatedAt,
			&x.TriagedAt, &x.TriageCode, &x.TriageNote, &x.CampaignNow); err != nil {
			return nil, "", err
		}
		out = append(out, x)
	}
	next := ""
	if len(out) == limit {
		last := out[len(out)-1]
		next = last.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + last.ID
	}
	return out, next, rows.Err()
}

// TriageReport dismisses or escalates an OPEN report. Escalation opens a compliance case through the outbox;
// neither action changes the campaign's status.
func (s *Service) TriageReport(ctx context.Context, staffID, reportID string, escalate bool, d Decision) error {
	d, err := d.validate(true)
	if err != nil {
		return err
	}
	if !validID(reportID) {
		return errs.New(errs.NotFound, "REPORT_NOT_FOUND", "No such report.")
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var campaignID, status string
		err := tx.QueryRow(ctx, `SELECT campaign_id, status FROM app.campaign_reports WHERE id = $1 FOR UPDATE`, reportID).Scan(&campaignID, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return errs.New(errs.NotFound, "REPORT_NOT_FOUND", "No such report.")
		}
		if err != nil {
			return err
		}
		if status != "OPEN" {
			return errs.New(errs.Conflict, "REPORT_ALREADY_TRIAGED", "This report has already been triaged.")
		}
		c, err := s.loadRow(ctx, tx, campaignID, false)
		if err != nil {
			return err
		}
		if bad, err := s.conflicted(ctx, c, staffID); err != nil {
			return err
		} else if bad {
			return ErrSelfDecision
		}
		to, action := "DISMISSED", "report_dismissed"
		if escalate {
			to, action = "ESCALATED", "report_escalated"
		}
		if _, err := tx.Exec(ctx, `UPDATE app.campaign_reports SET status = $2, triaged_by = $3, triaged_at = $4, triage_reason_code = $5,
			triage_note = $6 WHERE id = $1`, reportID, to, staffID, s.now(), d.ReasonCode, d.Note); err != nil {
			return err
		}
		if err := s.reviewAudit(ctx, tx, staffID, campaignID, action, map[string]any{"report_id": reportID, "reason_code": d.ReasonCode}); err != nil {
			return err
		}
		if escalate {
			return s.emit(ctx, tx, &c, "campaigns.report_escalated", map[string]any{"report_id": reportID, "reason_code": d.ReasonCode})
		}
		return nil
	})
}

// ---- staff picks ----------------------------------------------------------------------------------------

// FeatureInput adds a staff pick.
type FeatureInput struct {
	Position int    `json:"position"`
	Note     string `json:"note"`
	Reason   string `json:"reason"`
}

// Feature adds or removes a staff pick; only live PUBLIC campaigns can be picked.
func (s *Service) Feature(ctx context.Context, staffID, campaignID string, add bool, in FeatureInput) error {
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		c, err := s.loadRow(ctx, tx, campaignID, true)
		if err != nil {
			return err
		}
		if !add {
			reason, code := CleanText(in.Reason, 3, 500, false)
			if code != "" {
				return httpx.Validation(errs.Detail{Field: "reason", Code: code})
			}
			tag, err := tx.Exec(ctx, `UPDATE app.campaign_featured SET removed_by = $2, removed_at = $3, removal_reason = $4
				WHERE campaign_id = $1 AND removed_at IS NULL`, c.ID, staffID, s.now(), reason)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return errs.New(errs.NotFound, "NOT_FEATURED", "This campaign is not featured.")
			}
			return s.reviewAudit(ctx, tx, staffID, c.ID, "unfeatured", map[string]any{"reason": reason})
		}
		if !Live(c.Status) || c.Status == StatusCompleted || c.Visibility != "PUBLIC" {
			return errs.New(errs.Conflict, "NOT_FEATURABLE", "Only live, listed campaigns can be featured.")
		}
		note, code := CleanText(in.Note, 3, 500, false)
		if code != "" || in.Position < 1 || in.Position > 100 {
			return httpx.Validation(errs.Detail{Field: "note", Code: "INVALID_VALUE"}, errs.Detail{Field: "position", Code: "INVALID_VALUE"})
		}
		_, err = tx.Exec(ctx, `INSERT INTO app.campaign_featured (id, campaign_id, position, note, added_by, added_at) VALUES ($1,$2,$3,$4,$5,$6)`,
			ids.New(), c.ID, in.Position, note, staffID, s.now())
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return errs.New(errs.Conflict, "ALREADY_FEATURED", "This campaign is already featured.")
		}
		if err != nil {
			return err
		}
		return s.reviewAudit(ctx, tx, staffID, c.ID, "featured", map[string]any{"position": in.Position})
	})
}

// FeaturedAdmin lists the active staff picks for staff.
func (s *Service) FeaturedAdmin(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.Pool.Query(ctx, `SELECT f.campaign_id, c.title, c.slug, c.status, c.visibility, f.position, f.note, f.added_by, f.added_at
		FROM app.campaign_featured f JOIN app.campaigns c ON c.id = f.campaign_id WHERE f.removed_at IS NULL ORDER BY f.position, f.added_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, title, slug, status, vis, note, by string
		var pos int
		var at time.Time
		if err := rows.Scan(&id, &title, &slug, &status, &vis, &pos, &note, &by, &at); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"campaign_id": id, "title": title, "slug": slug, "status": status, "visibility": vis, "position": pos,
			"note": note, "added_by": by, "added_at": at, "shown": Live(status) && status != StatusCompleted && vis == "PUBLIC"})
	}
	return out, rows.Err()
}

// ---- conditional public responses (ADR-038 §1) --------------------------------------------------------------

// writePublic writes public data with a strong ETag over the data and the given Cache-Control; a matching
// If-None-Match gets 304.
func (s *Service) writePublic(w http.ResponseWriter, r *http.Request, data any, cacheControl string) {
	raw, err := json.Marshal(data)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	sum := sha256.Sum256(raw)
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	w.Header().Set("Cache-Control", cacheControl)
	w.Header().Set("ETag", etag)
	w.Header().Set("Vary", "Accept-Encoding")
	for _, v := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		if strings.TrimSpace(v) == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	httpx.WriteData(w, r, http.StatusOK, json.RawMessage(raw))
}

const (
	cacheRevalidate = "public, no-cache"
	cacheConfig     = "public, max-age=300"
)
