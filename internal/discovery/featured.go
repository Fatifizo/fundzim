package discovery

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/campaigns"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Featured placements (docs/stage-16/discovery-architecture.md §6). Supersede the Stage 7 single-actor staff picks
// (ADR-048 amends ADR-038 §2): a placement is requested by one staff member, approved by another (never the requester or
// a linked account), shown only inside its window and only while the campaign is promotable now. "Featured" is an
// editorial choice, never an endorsement, a verification or a statement about money; the public label says so.

// Featured limits (engineering defaults).
const (
	MaxFeaturedWindow = 31 * 24 * time.Hour
	MaxFeaturedLead   = 90 * 24 * time.Hour // how far in the future a window may start
	MaxFeaturedShown  = 12
)

// Placement is a featured placement record.
type Placement struct {
	ID            string     `json:"id"`
	CampaignID    string     `json:"campaign_id"`
	CampaignSlug  string     `json:"campaign_slug"`
	CampaignTitle string     `json:"campaign_title"`
	Position      int        `json:"position"`
	Reason        string     `json:"reason"`
	StartsAt      time.Time  `json:"starts_at"`
	EndsAt        time.Time  `json:"ends_at"`
	Status        string     `json:"status"`
	RequestedBy   string     `json:"requested_by"`
	RequestedAt   time.Time  `json:"requested_at"`
	DecidedBy     *string    `json:"decided_by"`
	DecidedAt     *time.Time `json:"decided_at"`
	DecisionNote  *string    `json:"decision_note"`
	RemovedBy     *string    `json:"removed_by"`
	RemovedAt     *time.Time `json:"removed_at"`
	RemovalReason *string    `json:"removal_reason"`
	Live          bool       `json:"live"`       // approved and inside its window now
	Promotable    *bool      `json:"promotable"` // eligibility now (null when it could not be checked)
	Version       int        `json:"version"`
}

// PlacementInput is POST /admin/discovery/featured.
type PlacementInput struct {
	CampaignID string    `json:"campaign_id"`
	Position   int       `json:"position"`
	Reason     string    `json:"reason"`
	StartsAt   time.Time `json:"starts_at"`
	EndsAt     time.Time `json:"ends_at"`
}

// RequestPlacement files a placement request (maker). The campaign must be promotable now.
func (s *Service) RequestPlacement(ctx context.Context, staffID string, in PlacementInput) (Placement, error) {
	var det []errs.Detail
	if !ids.Valid(in.CampaignID) {
		det = append(det, errs.Detail{Field: "campaign_id", Code: "INVALID_FORMAT"})
	}
	if in.Position < 1 || in.Position > 100 {
		det = append(det, errs.Detail{Field: "position", Code: "OUT_OF_RANGE"})
	}
	now := s.now()
	if in.StartsAt.IsZero() {
		in.StartsAt = now
	}
	switch {
	case in.StartsAt.Before(now.Add(-5 * time.Minute)), in.StartsAt.After(now.Add(MaxFeaturedLead)):
		det = append(det, errs.Detail{Field: "starts_at", Code: "OUT_OF_RANGE"})
	case !in.EndsAt.After(in.StartsAt), in.EndsAt.Sub(in.StartsAt) > MaxFeaturedWindow:
		det = append(det, errs.Detail{Field: "ends_at", Code: "OUT_OF_RANGE"})
	}
	r, err := cleanReason(in.Reason, 10, 500)
	if err != nil {
		var e *errs.Error
		if errors.As(err, &e) {
			det = append(det, e.Details...)
		}
	}
	if len(det) > 0 {
		return Placement{}, httpx.Validation(det...)
	}
	prom, err := s.Campaigns.Discoverable(ctx, []string{in.CampaignID}, true)
	if err != nil {
		return Placement{}, err
	}
	if !prom[in.CampaignID] {
		return Placement{}, ErrNotPromotable
	}
	id := ids.New()
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO app.discovery_featured_placements (id, campaign_id, position, reason, starts_at, ends_at, status, requested_by,
			requested_at) VALUES ($1, $2, $3, $4, $5, $6, 'REQUESTED', $7, $8)`, id, in.CampaignID, in.Position, r, in.StartsAt.UTC(), in.EndsAt.UTC(),
			staffID, now)
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return errs.New(errs.Conflict, "PLACEMENT_EXISTS", "This campaign already has an open featured placement or request.")
		}
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "discovery.featured_requested", ActorType: "staff", ActorID: staffID,
			TargetType: "discovery_featured_placement", TargetID: id, Reason: r,
			Metadata: map[string]any{"campaign_id": in.CampaignID, "position": in.Position, "starts_at": in.StartsAt.UTC(), "ends_at": in.EndsAt.UTC()}})
	})
	if err != nil {
		return Placement{}, err
	}
	return s.placement(ctx, id)
}

// DecidePlacement: approve or reject (checker; never the requester or a linked account), cancel (the requester), or
// remove an approved placement (any holder of discovery.feature.remove, reason required). Approval re-checks that the
// campaign is promotable now.
func (s *Service) DecidePlacement(ctx context.Context, staffID, id, action, note string) (Placement, error) {
	if !ids.Valid(id) {
		return Placement{}, ErrNotFound
	}
	n, err := cleanReason(note, 3, map[bool]int{true: 500, false: 1000}[action == "remove"])
	if err != nil {
		return Placement{}, err
	}
	idents, err := s.identities(ctx, staffID)
	if err != nil {
		return Placement{}, err
	}
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var status, requester, campaignID string
		if err := tx.QueryRow(ctx, `SELECT status, requested_by::text, campaign_id::text FROM app.discovery_featured_placements WHERE id = $1 FOR UPDATE`, id).
			Scan(&status, &requester, &campaignID); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		now := s.now()
		var sql string
		args := []any{id}
		switch action {
		case "approve", "reject":
			if status != "REQUESTED" {
				return ErrState
			}
			if idents[requester] {
				return ErrSelf
			}
			to := "REJECTED"
			if action == "approve" {
				prom, err := s.Campaigns.Discoverable(ctx, []string{campaignID}, true)
				if err != nil {
					return err
				}
				if !prom[campaignID] {
					return ErrNotPromotable
				}
				to = "APPROVED"
			}
			sql = `UPDATE app.discovery_featured_placements SET status = $2, decided_by = $3, decided_at = $4, decision_note = $5 WHERE id = $1`
			args = append(args, to, staffID, now, n)
		case "cancel":
			if status != "REQUESTED" {
				return ErrState
			}
			if requester != staffID {
				return ErrNotFound
			}
			sql = `UPDATE app.discovery_featured_placements SET status = 'CANCELLED' WHERE id = $1`
		case "remove":
			if status != "APPROVED" {
				return ErrState
			}
			sql = `UPDATE app.discovery_featured_placements SET status = 'REMOVED', removed_by = $2, removed_at = $3, removal_reason = $4 WHERE id = $1`
			args = append(args, staffID, now, n)
		default:
			return httpx.Validation(errs.Detail{Field: "action", Code: "INVALID_VALUE"})
		}
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			return mapCheck(err)
		}
		return audit.Record(ctx, tx, audit.Event{Action: "discovery.featured_" + map[string]string{"approve": "approved", "reject": "rejected",
			"cancel": "cancelled", "remove": "removed"}[action], ActorType: "staff", ActorID: staffID, TargetType: "discovery_featured_placement",
			TargetID: id, Reason: n, Metadata: map[string]any{"campaign_id": campaignID}})
	})
	if err != nil {
		return Placement{}, err
	}
	return s.placement(ctx, id)
}

// EndPlacements moves approved placements whose window has passed to ENDED (worker). Returns how many.
func (s *Service) EndPlacements(ctx context.Context) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `UPDATE app.discovery_featured_placements SET status = 'ENDED' WHERE status = 'APPROVED' AND ends_at <= $1`, s.now())
	return tag.RowsAffected(), err
}

func (s *Service) scanPlacements(rows pgx.Rows) ([]Placement, error) {
	defer rows.Close()
	out := []Placement{}
	now := s.now()
	for rows.Next() {
		var p Placement
		if err := rows.Scan(&p.ID, &p.CampaignID, &p.CampaignSlug, &p.CampaignTitle, &p.Position, &p.Reason, &p.StartsAt, &p.EndsAt, &p.Status,
			&p.RequestedBy, &p.RequestedAt, &p.DecidedBy, &p.DecidedAt, &p.DecisionNote, &p.RemovedBy, &p.RemovedAt, &p.RemovalReason, &p.Version); err != nil {
			return nil, err
		}
		p.Live = p.Status == "APPROVED" && !now.Before(p.StartsAt) && now.Before(p.EndsAt)
		out = append(out, p)
	}
	return out, rows.Err()
}

// placementRows selects placements with the campaign's slug and title from the projection (staff view; a campaign that
// was never indexed shows its id only).
const placementRows = `SELECT p.id::text, p.campaign_id::text, coalesce(nullif(i.slug, ''), ''), coalesce(nullif(i.title, ''), ''), p.position, p.reason,
	p.starts_at, p.ends_at, p.status, p.requested_by::text, p.requested_at, p.decided_by::text, p.decided_at, p.decision_note, p.removed_by::text,
	p.removed_at, p.removal_reason, p.version
	FROM app.discovery_featured_placements p LEFT JOIN app.discovery_campaign_index i ON i.campaign_id = p.campaign_id`

func (s *Service) placement(ctx context.Context, id string) (Placement, error) {
	rows, err := s.Pool.Query(ctx, placementRows+` WHERE p.id = $1`, id)
	if err != nil {
		return Placement{}, err
	}
	ps, err := s.scanPlacements(rows)
	if err != nil {
		return Placement{}, err
	}
	if len(ps) == 0 {
		return Placement{}, ErrNotFound
	}
	s.annotatePromotable(ctx, ps)
	return ps[0], nil
}

// Placements lists placements for staff (open ones first: REQUESTED, then APPROVED; then history), at most 200.
func (s *Service) Placements(ctx context.Context, status string) ([]Placement, error) {
	if status != "" && status != "REQUESTED" && status != "APPROVED" && status != "HISTORY" {
		return nil, httpx.Validation(errs.Detail{Field: "status", Code: "INVALID_VALUE"})
	}
	where := ""
	switch status {
	case "REQUESTED", "APPROVED":
		where = ` WHERE p.status = '` + status + `'`
	case "HISTORY":
		where = ` WHERE p.status NOT IN ('REQUESTED','APPROVED')`
	}
	rows, err := s.Pool.Query(ctx, placementRows+where+` ORDER BY CASE p.status WHEN 'REQUESTED' THEN 0 WHEN 'APPROVED' THEN 1 ELSE 2 END,
		p.position, p.requested_at DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	ps, err := s.scanPlacements(rows)
	if err != nil {
		return nil, err
	}
	s.annotatePromotable(ctx, ps)
	return ps, nil
}

func (s *Service) annotatePromotable(ctx context.Context, ps []Placement) {
	var idl []string
	for _, p := range ps {
		if p.Status == "REQUESTED" || p.Status == "APPROVED" {
			idl = append(idl, p.CampaignID)
		}
	}
	if len(idl) == 0 {
		return
	}
	m, err := s.Campaigns.Discoverable(ctx, idl, true)
	if err != nil {
		return // unknown: left null
	}
	for i := range ps {
		if ps[i].Status == "REQUESTED" || ps[i].Status == "APPROVED" {
			v := m[ps[i].CampaignID]
			ps[i].Promotable = &v
		}
	}
}

// FeaturedCard is a public featured item (selection explains why it is shown).
type FeaturedCard struct {
	Card
	Selection string `json:"selection"` // FEATURED (approved placement) | RECENTLY_PUBLISHED (fill)
}

// Featured returns live approved placements (position order) that are promotable now, then fills with recently published
// promotable campaigns. Never popularity, never payment.
func (s *Service) Featured(ctx context.Context, limit int, fill bool) ([]FeaturedCard, error) {
	if limit <= 0 || limit > MaxFeaturedShown {
		limit = 6
	}
	now := s.now()
	rows, err := s.Pool.Query(ctx, `SELECT campaign_id::text FROM app.discovery_featured_placements WHERE status = 'APPROVED' AND starts_at <= $1 AND ends_at > $1
		ORDER BY position, starts_at, id LIMIT $2`, now, MaxFeaturedShown)
	if err != nil {
		return nil, err
	}
	picks, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	cards, err := s.Campaigns.PublicCards(ctx, picks, true)
	if err != nil {
		return nil, err
	}
	out := []FeaturedCard{}
	seen := map[string]bool{}
	for _, c := range cards {
		if len(out) == limit {
			break
		}
		r := reason(ReasonFeatured)
		out = append(out, FeaturedCard{Card: Card{DiscoveryCard: c, Reason: &r}, Selection: "FEATURED"})
		seen[c.ID] = true
		s.recordImpression(c.ID, "FEATURED")
	}
	if fill && len(out) < limit {
		recent, err := s.recentPromotable(ctx, limit-len(out), seen)
		if err != nil {
			return nil, err
		}
		for _, c := range recent {
			r := reason(ReasonRecent)
			out = append(out, FeaturedCard{Card: Card{DiscoveryCard: c, Reason: &r}, Selection: "RECENTLY_PUBLISHED"})
		}
	}
	return out, nil
}

// recentPromotable returns up to n recently published promotable campaigns not in skip.
func (s *Service) recentPromotable(ctx context.Context, n int, skip map[string]bool) ([]campaigns.DiscoveryCard, error) {
	if n <= 0 {
		return nil, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT campaign_id::text FROM app.discovery_campaign_index WHERE discoverable AND status = 'ACTIVE'
		ORDER BY published_at DESC, campaign_id DESC LIMIT $1`, n*3+len(skip))
	if err != nil {
		return nil, err
	}
	all, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	var idl []string
	for _, id := range all {
		if !skip[id] {
			idl = append(idl, id)
		}
	}
	cards, err := s.Campaigns.PublicCards(ctx, idl, true)
	if err != nil {
		return nil, err
	}
	if len(cards) > n {
		cards = cards[:n]
	}
	return cards, nil
}

// ---- shared helpers -----------------------------------------------------------------------------------------------

// cleanReason validates a staff reason or note as plain text (the campaigns content rules).
func cleanReason(v string, min, max int) (string, error) {
	out, code := campaigns.CleanText(v, min, max, true)
	if code != "" {
		return "", httpx.Validation(errs.Detail{Field: "reason", Code: code})
	}
	return out, nil
}

// mapCheck turns a maker-checker database refusal into ErrSelf.
func mapCheck(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23514" && (strings.Contains(pg.Message, "maker-checker") || strings.HasSuffix(pg.ConstraintName, "_checker")) {
		return ErrSelf
	}
	return err
}

func hexString(b []byte) string { return hex.EncodeToString(b) }
