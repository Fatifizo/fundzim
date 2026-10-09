// Package updates holds campaign updates: short plain-text news posts an organiser publishes on a live
// campaign (interface-contracts §1, §6, §9; campaign-schema §9). It is part of the campaigns module and builds
// on the core access API (OwnerAccess, Load, PublicBySlug, Restricted); the core never imports it.
//
// Lifecycle (machine campaign_update, migration 20261009171200): DRAFT → PENDING_MODERATION | PUBLISHED;
// PENDING_MODERATION → PUBLISHED (staff approve) | HIDDEN (staff hide); PUBLISHED → HIDDEN; any of them →
// DELETED (owner soft delete; nothing is ever removed). Publication is pre-moderated when the category's
// moderation policy is PRE_MODERATE_UPDATES, when the tier policy says so (rules.tiers.<tier>.pre_moderate_updates)
// or when the owner, organisation, beneficiary or campaign carries a RESTRICTED compliance restriction.
// SUSPENDED or OFFBOARDED refuses every owner write. Updates carry no financial information.
package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/campaigns"
	"github.com/Fatifizo/fundzim/internal/organisations"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
	"github.com/Fatifizo/fundzim/internal/users"
)

// Statuses (machine campaign_update).
const (
	StatusDraft             = "DRAFT"
	StatusPendingModeration = "PENDING_MODERATION"
	StatusPublished         = "PUBLISHED"
	StatusHidden            = "HIDDEN"
	StatusDeleted           = "DELETED"
)

// Transitions is the campaign_update edge list; it must match migration 20261009171200 (unit-tested).
var Transitions = map[string][]string{
	"":                      {StatusDraft, StatusPendingModeration, StatusPublished},
	StatusDraft:             {StatusPendingModeration, StatusPublished, StatusDeleted},
	StatusPendingModeration: {StatusPublished, StatusHidden, StatusDeleted},
	StatusPublished:         {StatusHidden, StatusDeleted},
	StatusHidden:            {StatusDeleted},
}

// Moderation reasons (codes only; never shown to the owner).
const (
	ReasonCategoryPolicy  = "CATEGORY_POLICY"
	ReasonTierPolicy      = "TIER_POLICY"
	ReasonOwnerRestricted = "OWNER_RESTRICTED"
)

// Audit actions and outbox events.
const (
	AuditCreated   = "campaign.update_created"
	AuditEdited    = "campaign.update_edited"
	AuditSubmitted = "campaign.update_submitted_for_moderation"
	AuditPublished = "campaign.update_published"
	AuditHidden    = "campaign.update_hidden"
	AuditDeleted   = "campaign.update_deleted"

	EventPublished = "campaigns.update_published"
	EventSubmitted = "campaigns.update_submitted_for_moderation"
)

// PermModerate is the staff permission for the moderation queue, approve and hide.
const PermModerate = "content.moderate"

// MaxMedia is the number of media references an update may carry (also a database CHECK).
const MaxMedia = 10

var (
	ErrNotFound      = errs.New(errs.NotFound, "UPDATE_NOT_FOUND", "No such campaign update.")
	ErrNotEditable   = errs.New(errs.Conflict, "UPDATE_NOT_EDITABLE", "Only drafts and updates awaiting moderation can be changed.")
	ErrUpdateStatus  = errs.New(errs.Conflict, "INVALID_STATUS", "This action is not allowed in the update's current status.")
	ErrVersion       = errs.New(errs.Conflict, "VERSION_CONFLICT", "The update changed in the meantime. Reload and try again.")
	ErrRestricted    = errs.New(errs.Forbidden, "ACCOUNT_RESTRICTED", "This action is not available for this campaign.")
	ErrRestrictedNow = errs.New(errs.Conflict, "ACCOUNT_RESTRICTED", "The campaign or its owner is restricted; this update cannot be published.")
	ErrMedia         = errs.New(errs.Unprocessable, "MEDIA_NOT_APPROVED", "Every attached image must be an approved image of this campaign.")
	ErrSelfModerate  = errs.New(errs.Forbidden, "SELF_DECISION_FORBIDDEN", "You cannot moderate content that concerns you or your organisation.")
	errPolicy        = errs.New(errs.Unavailable, "CAMPAIGN_POLICY_UNAVAILABLE", "Campaign updates are temporarily unavailable.")
)

// Core is the campaigns core access API (implemented by *campaigns.Service).
type Core interface {
	OwnerAccess(ctx context.Context, userID, campaignID string, write bool) (campaigns.Access, error)
	Load(ctx context.Context, campaignID string) (campaigns.Access, error)
	PublicBySlug(ctx context.Context, slug string) (campaigns.Access, error)
	Restricted(ctx context.Context, a campaigns.Access) (string, error)
}

// Media lists a campaign's APPROVED media ids (implemented by package media). Nil: no media can be attached.
type Media interface {
	ApprovedIDs(ctx context.Context, campaignID string) ([]string, error)
}

// Deps are the collaborators of the updates package.
type Deps struct {
	Pool   *pgxpool.Pool // fundzim_app
	Core   Core
	Media  Media
	Orgs   *organisations.Service
	Clock  clock.Clock
	Logger *slog.Logger
}

// Service is the campaign updates package.
type Service struct{ Deps }

// New builds the service.
func New(d Deps) *Service { return &Service{Deps: d} }

func (s *Service) now() time.Time { return s.Clock.Now().UTC() }

func (s *Service) tx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	return db.WithTx(ctx, s.Pool, db.TxOptions{}, fn)
}

// Update is the owner/staff view of an update.
type Update struct {
	ID           string     `json:"id"`
	CampaignID   string     `json:"campaign_id"`
	Title        string     `json:"title"`
	Body         string     `json:"body"`
	MediaIDs     []string   `json:"media_ids"`
	Status       string     `json:"status"`
	SubmittedAt  *time.Time `json:"submitted_at"`
	PublishedAt  *time.Time `json:"published_at"`
	HiddenAt     *time.Time `json:"hidden_at"`
	HiddenReason *string    `json:"hidden_reason"`
	Version      int        `json:"version"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`

	authorID string
}

// QueueItem is a moderation-queue entry (staff only).
type QueueItem struct {
	Update
	AuthorID          string   `json:"author_id"`
	ModerationReasons []string `json:"moderation_reasons"`
	CampaignStatus    string   `json:"campaign_status"`
}

// PublicUpdate is what anyone may see: no author, moderator or moderation data.
type PublicUpdate struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	Body          string    `json:"body"`
	MediaIDs      []string  `json:"media_ids"`
	PublishedAt   time.Time `json:"published_at"`
	OrganiserName string    `json:"organiser_name"`
}

const updateCols = `id, campaign_id, author_id, title, body, media_ids::text[], status, submitted_at, published_at, hidden_at,
	hidden_reason, version, created_at, updated_at`

func scanUpdate(row pgx.Row, extra ...any) (Update, error) {
	var u Update
	dest := append([]any{&u.ID, &u.CampaignID, &u.authorID, &u.Title, &u.Body, &u.MediaIDs, &u.Status, &u.SubmittedAt,
		&u.PublishedAt, &u.HiddenAt, &u.HiddenReason, &u.Version, &u.CreatedAt, &u.UpdatedAt}, extra...)
	err := row.Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, ErrNotFound
	}
	if u.MediaIDs == nil {
		u.MediaIDs = []string{}
	}
	return u, err
}

// ---- policy -------------------------------------------------------------------------------------------------

type policy struct {
	title, body Limits
	tierPreMod  map[string]bool
}

// loadPolicy reads the APPROVED campaign policy. Missing or malformed limits fail closed.
func (s *Service) loadPolicy(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) (policy, error) {
	var raw []byte
	if err := q.QueryRow(ctx, `SELECT rules FROM app.campaign_policies WHERE status = 'APPROVED'`).Scan(&raw); err != nil {
		return policy{}, errs.Wrap(err, errs.Unavailable, errPolicy.Code, errPolicy.Message)
	}
	return parsePolicy(raw)
}

func parsePolicy(raw []byte) (policy, error) {
	var r struct {
		Content map[string][]int `json:"content"`
		Tiers   map[string]struct {
			PreModerateUpdates bool `json:"pre_moderate_updates"`
		} `json:"tiers"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return policy{}, errs.Wrap(err, errs.Unavailable, errPolicy.Code, errPolicy.Message)
	}
	lim := func(k string) (Limits, bool) {
		v := r.Content[k]
		if len(v) != 2 || v[0] < 1 || v[1] < v[0] {
			return Limits{}, false
		}
		return Limits{Min: v[0], Max: v[1]}, true
	}
	t, ok1 := lim("update_title")
	b, ok2 := lim("update_body")
	if !ok1 || !ok2 {
		return policy{}, errs.Wrap(fmt.Errorf("campaign policy has no update_title/update_body limits"), errs.Unavailable, errPolicy.Code, errPolicy.Message)
	}
	p := policy{title: t, body: b, tierPreMod: map[string]bool{}}
	for tier, v := range r.Tiers {
		p.tierPreMod[tier] = v.PreModerateUpdates
	}
	return p, nil
}

// ---- owner writes -------------------------------------------------------------------------------------------

// Input creates an update.
type Input struct {
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	Publish  bool     `json:"publish"`
	MediaIDs []string `json:"media_ids"`
}

// Patch changes a draft or pending update; nil fields are unchanged.
type Patch struct {
	Title    *string   `json:"title"`
	Body     *string   `json:"body"`
	Publish  *bool     `json:"publish"`
	MediaIDs *[]string `json:"media_ids"`
}

// ownerWrite authorises an owner write: write access (404 otherwise), a live campaign (409 INVALID_STATUS) and
// no SUSPENDED/OFFBOARDED restriction (403 ACCOUNT_RESTRICTED). It returns the restriction level.
func (s *Service) ownerWrite(ctx context.Context, userID, campaignID string) (campaigns.Access, string, error) {
	a, err := s.Core.OwnerAccess(ctx, userID, campaignID, true)
	if err != nil {
		return a, "", err
	}
	if !campaigns.Live(a.Status) {
		return a, "", campaigns.ErrInvalidStatus
	}
	lvl, err := s.Core.Restricted(ctx, a)
	if err != nil {
		return a, "", fmt.Errorf("campaign updates: restriction check: %w", err) // fail closed
	}
	if lvl == campaigns.RestrictionSuspended || lvl == campaigns.RestrictionOffboarded {
		return a, lvl, ErrRestricted
	}
	return a, lvl, nil
}

// campaignRow locks the campaign row (FOR SHARE) inside tx and re-checks that it is live; it returns the risk tier
// and the category's moderation policy.
func campaignRow(ctx context.Context, tx pgx.Tx, campaignID string) (tier, moderation string, err error) {
	var st string
	err = tx.QueryRow(ctx, `SELECT c.status, c.risk_tier, k.moderation_policy FROM app.campaigns c
		JOIN app.campaign_categories k ON k.code = c.category_code WHERE c.id = $1 FOR SHARE OF c`, campaignID).Scan(&st, &tier, &moderation)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", campaigns.ErrNotFound
	}
	if err == nil && !campaigns.Live(st) {
		err = campaigns.ErrInvalidStatus
	}
	return tier, moderation, err
}

// ModerationReasons decides whether publication needs a moderator (empty = publish directly).
func ModerationReasons(categoryPolicy string, tierPreModerated bool, restriction string) []string {
	var out []string
	if categoryPolicy == "PRE_MODERATE_UPDATES" {
		out = append(out, ReasonCategoryPolicy)
	}
	if tierPreModerated {
		out = append(out, ReasonTierPolicy)
	}
	if restriction != campaigns.RestrictionNone {
		out = append(out, ReasonOwnerRestricted) // RESTRICTED forces moderation (SUSPENDED/OFFBOARDED never get here)
	}
	return out
}

func cleanMediaIDs(in []string) ([]string, error) {
	if len(in) > MaxMedia {
		return nil, validation("media_ids", "TOO_MANY")
	}
	out := make([]string, 0, len(in))
	for _, id := range in {
		if !ids.Valid(id) {
			return nil, validation("media_ids", "INVALID_VALUE")
		}
		if slices.Contains(out, id) {
			return nil, validation("media_ids", "DUPLICATE")
		}
		out = append(out, id)
	}
	return out, nil
}

// checkMedia verifies that every id is an APPROVED media item of the campaign right now.
func (s *Service) checkMedia(ctx context.Context, campaignID string, mediaIDs []string) error {
	if len(mediaIDs) == 0 {
		return nil
	}
	if s.Media == nil {
		return ErrMedia
	}
	approved, err := s.Media.ApprovedIDs(ctx, campaignID)
	if err != nil {
		return err
	}
	for _, id := range mediaIDs {
		if !slices.Contains(approved, id) {
			return ErrMedia
		}
	}
	return nil
}

func validateContent(p policy, title, body string) (string, string, error) {
	t, err := Validate("title", title, false, p.title)
	if err != nil {
		return "", "", err
	}
	b, err := Validate("body", body, true, p.body)
	if err != nil {
		return "", "", err
	}
	return t, b, nil
}

// Create adds an update (a draft, or published/pending when in.Publish).
func (s *Service) Create(ctx context.Context, userID, campaignID string, in Input) (Update, error) {
	a, lvl, err := s.ownerWrite(ctx, userID, campaignID)
	if err != nil {
		return Update{}, err
	}
	pol, err := s.loadPolicy(ctx, s.Pool)
	if err != nil {
		return Update{}, err
	}
	title, body, err := validateContent(pol, in.Title, in.Body)
	if err != nil {
		return Update{}, err
	}
	media, err := cleanMediaIDs(in.MediaIDs)
	if err != nil {
		return Update{}, err
	}
	if in.Publish {
		if err := s.checkMedia(ctx, a.CampaignID, media); err != nil {
			return Update{}, err
		}
	}
	id := ids.New()
	now := s.now()
	var out Update
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tier, moderation, err := campaignRow(ctx, tx, a.CampaignID)
		if err != nil {
			return err
		}
		status, event, action := StatusDraft, "CREATED", AuditCreated
		var reasons []string
		var submitted, published *time.Time
		if in.Publish {
			submitted = &now
			reasons = ModerationReasons(moderation, pol.tierPreMod[tier], lvl)
			if len(reasons) > 0 {
				status, event, action = StatusPendingModeration, "SUBMITTED", AuditSubmitted
			} else {
				status, event, action = StatusPublished, "PUBLISHED", AuditPublished
				published = &now
			}
		}
		if reasons == nil {
			reasons = []string{}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO app.campaign_updates (id, campaign_id, author_id, title, body, media_ids, status,
			moderation_reasons, submitted_at, published_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6::uuid[], $7, $8, $9, $10, $11, $11)`,
			id, a.CampaignID, userID, title, body, media, status, reasons, submitted, published, now); err != nil {
			return err
		}
		if err := s.event(ctx, tx, id, a.CampaignID, 1, event, "", status, "USER", userID, "", ""); err != nil {
			return err
		}
		if err := s.record(ctx, tx, action, id, a.CampaignID, "", status, nil); err != nil {
			return err
		}
		if err := s.emitFor(ctx, tx, status, id, a.CampaignID, "OWNER"); err != nil {
			return err
		}
		out, err = scanUpdate(tx.QueryRow(ctx, `SELECT `+updateCols+` FROM app.campaign_updates WHERE id = $1`, id))
		return err
	})
	return out, err
}

// Edit changes a DRAFT or PENDING_MODERATION update (optimistic concurrency on version). A draft with
// Publish=true is published or sent to moderation; a pending update stays pending.
func (s *Service) Edit(ctx context.Context, userID, campaignID, updateID string, expectVersion int, p Patch) (Update, error) {
	if !ids.Valid(updateID) {
		return Update{}, ErrNotFound
	}
	a, lvl, err := s.ownerWrite(ctx, userID, campaignID)
	if err != nil {
		return Update{}, err
	}
	pol, err := s.loadPolicy(ctx, s.Pool)
	if err != nil {
		return Update{}, err
	}
	var out Update
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tier, moderation, err := campaignRow(ctx, tx, a.CampaignID)
		if err != nil {
			return err
		}
		cur, err := scanUpdate(tx.QueryRow(ctx, `SELECT `+updateCols+` FROM app.campaign_updates WHERE id = $1 AND campaign_id = $2
			AND status <> 'DELETED' FOR UPDATE`, updateID, a.CampaignID))
		if err != nil {
			return err
		}
		if cur.Version != expectVersion {
			return ErrVersion
		}
		if cur.Status != StatusDraft && cur.Status != StatusPendingModeration {
			return ErrNotEditable
		}
		title, body, media := cur.Title, cur.Body, cur.MediaIDs
		if p.Title != nil {
			title = *p.Title
		}
		if p.Body != nil {
			body = *p.Body
		}
		if title, body, err = validateContent(pol, title, body); err != nil {
			return err
		}
		if p.MediaIDs != nil {
			if media, err = cleanMediaIDs(*p.MediaIDs); err != nil {
				return err
			}
		}
		publish := p.Publish != nil && *p.Publish
		if cur.Status == StatusPendingModeration && p.Publish != nil && !*p.Publish {
			return validation("publish", "INVALID_VALUE") // there is no way back from moderation to draft
		}
		status, event, action := cur.Status, "EDITED", AuditEdited
		var reasons []string
		var submitted, published any
		now := s.now()
		if cur.Status == StatusDraft && publish {
			if err := s.checkMedia(ctx, a.CampaignID, media); err != nil {
				return err
			}
			submitted = now
			reasons = ModerationReasons(moderation, pol.tierPreMod[tier], lvl)
			if len(reasons) > 0 {
				status, event, action = StatusPendingModeration, "SUBMITTED", AuditSubmitted
			} else {
				status, event, action = StatusPublished, "PUBLISHED", AuditPublished
				published = now
			}
		} else if cur.Status == StatusPendingModeration && lvl != campaigns.RestrictionNone {
			reasons = ModerationReasons(moderation, pol.tierPreMod[tier], lvl)
		}
		if status == cur.Status && title == cur.Title && body == cur.Body && slices.Equal(media, cur.MediaIDs) {
			out = cur // nothing changed
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE app.campaign_updates SET title = $3, body = $4, media_ids = $5::uuid[], status = $6,
			moderation_reasons = CASE WHEN $7::text[] IS NULL THEN moderation_reasons ELSE $7::text[] END,
			submitted_at = coalesce($8, submitted_at), published_at = coalesce($9, published_at)
			WHERE id = $1 AND version = $2`, cur.ID, cur.Version, title, body, media, status, reasons, submitted, published); err != nil {
			return err
		}
		if err := s.event(ctx, tx, cur.ID, a.CampaignID, cur.Version+1, event, cur.Status, status, "USER", userID, "", ""); err != nil {
			return err
		}
		if err := s.record(ctx, tx, action, cur.ID, a.CampaignID, cur.Status, status, nil); err != nil {
			return err
		}
		if status != cur.Status {
			if err := s.emitFor(ctx, tx, status, cur.ID, a.CampaignID, "OWNER"); err != nil {
				return err
			}
		}
		out, err = scanUpdate(tx.QueryRow(ctx, `SELECT `+updateCols+` FROM app.campaign_updates WHERE id = $1`, cur.ID))
		return err
	})
	return out, err
}

// Delete soft-deletes an update (status DELETED; the row and its history stay). expectVersion 0 skips the check.
func (s *Service) Delete(ctx context.Context, userID, campaignID, updateID string, expectVersion int) error {
	if !ids.Valid(updateID) {
		return ErrNotFound
	}
	a, _, err := s.ownerWrite(ctx, userID, campaignID)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, _, err := campaignRow(ctx, tx, a.CampaignID); err != nil {
			return err
		}
		cur, err := scanUpdate(tx.QueryRow(ctx, `SELECT `+updateCols+` FROM app.campaign_updates WHERE id = $1 AND campaign_id = $2
			AND status <> 'DELETED' FOR UPDATE`, updateID, a.CampaignID))
		if err != nil {
			return err
		}
		if expectVersion != 0 && cur.Version != expectVersion {
			return ErrVersion
		}
		if _, err := tx.Exec(ctx, `UPDATE app.campaign_updates SET status = 'DELETED', deleted_by = $2, deleted_at = $3 WHERE id = $1`,
			cur.ID, userID, s.now()); err != nil {
			return err
		}
		if err := s.event(ctx, tx, cur.ID, a.CampaignID, cur.Version+1, "DELETED", cur.Status, StatusDeleted, "USER", userID, "", ""); err != nil {
			return err
		}
		return s.record(ctx, tx, AuditDeleted, cur.ID, a.CampaignID, cur.Status, StatusDeleted, nil)
	})
}

// ---- reads -------------------------------------------------------------------------------------------------

// Page bounds.
const (
	DefaultLimit = 20
	MaxLimit     = 100
)

func clampLimit(n int) int {
	if n <= 0 {
		return DefaultLimit
	}
	return min(n, MaxLimit)
}

// ListOwner returns the campaign's updates (not deleted) for anyone with read access, newest first.
func (s *Service) ListOwner(ctx context.Context, userID, campaignID, cursor string, limit int) ([]Update, string, error) {
	a, err := s.Core.OwnerAccess(ctx, userID, campaignID, false)
	if err != nil {
		return nil, "", err
	}
	limit = clampLimit(limit)
	args := []any{a.CampaignID, limit + 1}
	q := `SELECT ` + updateCols + ` FROM app.campaign_updates WHERE campaign_id = $1 AND status <> 'DELETED'`
	if cursor != "" {
		q += ` AND id < $3`
		args = append(args, cursor)
	}
	rows, err := s.Pool.Query(ctx, q+` ORDER BY id DESC LIMIT $2`, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []Update{}
	for rows.Next() {
		u, err := scanUpdate(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = out[limit-1].ID
	}
	return out, next, nil
}

// ModerationQueue lists PENDING_MODERATION updates, oldest first (staff, content.moderate).
func (s *Service) ModerationQueue(ctx context.Context, cursor string, limit int) ([]QueueItem, string, error) {
	limit = clampLimit(limit)
	args := []any{limit + 1}
	q := `SELECT ` + updateCols + `, moderation_reasons, campaign_status FROM (SELECT u.*, c.status AS campaign_status
		FROM app.campaign_updates u JOIN app.campaigns c ON c.id = u.campaign_id WHERE u.status = 'PENDING_MODERATION') q WHERE true`
	if cursor != "" {
		q += ` AND id > $2`
		args = append(args, cursor)
	}
	rows, err := s.Pool.Query(ctx, q+` ORDER BY id LIMIT $1`, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []QueueItem{}
	for rows.Next() {
		var it QueueItem
		u, err := scanUpdate(rows, &it.ModerationReasons, &it.CampaignStatus)
		if err != nil {
			return nil, "", err
		}
		it.Update, it.AuthorID = u, u.authorID
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = out[limit-1].ID
	}
	return out, next, nil
}

// ListPublic returns PUBLISHED updates of a publicly exposed campaign, newest first. Media ids are limited to
// those still APPROVED. The organiser name is the campaign owner's display name (or the organisation's name).
func (s *Service) ListPublic(ctx context.Context, slug, cursor string, limit int) ([]PublicUpdate, string, error) {
	a, err := s.Core.PublicBySlug(ctx, slug)
	if err != nil {
		return nil, "", err
	}
	limit = clampLimit(limit)
	args := []any{a.CampaignID, limit + 1}
	q := `SELECT id, title, body, media_ids::text[], published_at FROM app.campaign_updates WHERE campaign_id = $1 AND status = 'PUBLISHED'`
	if cursor != "" {
		q += ` AND (published_at, id) < (SELECT published_at, id FROM app.campaign_updates WHERE id = $3 AND campaign_id = $1 AND status = 'PUBLISHED')`
		args = append(args, cursor)
	}
	rows, err := s.Pool.Query(ctx, q+` ORDER BY published_at DESC, id DESC LIMIT $2`, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []PublicUpdate{}
	for rows.Next() {
		var u PublicUpdate
		if err := rows.Scan(&u.ID, &u.Title, &u.Body, &u.MediaIDs, &u.PublishedAt); err != nil {
			return nil, "", err
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = out[limit-1].ID
	}
	if len(out) == 0 {
		return out, "", nil
	}
	var approved []string
	if s.Media != nil {
		if approved, err = s.Media.ApprovedIDs(ctx, a.CampaignID); err != nil {
			return nil, "", err
		}
	}
	name := s.organiserName(ctx, a)
	for i := range out {
		keep := []string{}
		for _, m := range out[i].MediaIDs {
			if slices.Contains(approved, m) {
				keep = append(keep, m)
			}
		}
		out[i].MediaIDs, out[i].OrganiserName = keep, name
	}
	return out, next, nil
}

func (s *Service) organiserName(ctx context.Context, a campaigns.Access) string {
	const fallback = "The organiser"
	if a.OwnerOrgID != nil {
		if name, _, _, err := s.Orgs.Summary(ctx, *a.OwnerOrgID); err == nil && name != "" {
			return name
		}
		return fallback
	}
	if a.OwnerUserID == nil {
		return fallback
	}
	names, err := users.DisplayNames(ctx, s.Pool, []string{*a.OwnerUserID})
	if err != nil || names[*a.OwnerUserID] == "" {
		return fallback // anonymised profiles have no display name
	}
	return names[*a.OwnerUserID]
}

// ---- staff moderation ----------------------------------------------------------------------------------------

// Decision is a staff moderation decision.
type Decision struct {
	ReasonCode string `json:"reason_code"`
	Note       string `json:"note"`
}

// Approve publishes a PENDING_MODERATION update. The moderator must not be (or be linked to) the author, the
// campaign owner or creator, or a member of the owning organisation; the campaign must still be live and
// unrestricted (SUSPENDED/OFFBOARDED) and attached media still approved.
func (s *Service) Approve(ctx context.Context, staffID, updateID string, d Decision) (Update, error) {
	return s.decide(ctx, staffID, updateID, d, true)
}

// Hide hides a PENDING_MODERATION or PUBLISHED update (any campaign status).
func (s *Service) Hide(ctx context.Context, staffID, updateID string, d Decision) (Update, error) {
	return s.decide(ctx, staffID, updateID, d, false)
}

func (s *Service) decide(ctx context.Context, staffID, updateID string, d Decision, approve bool) (Update, error) {
	if !ids.Valid(updateID) {
		return Update{}, ErrNotFound
	}
	d, err := validDecision(d)
	if err != nil {
		return Update{}, err
	}
	var campaignID string
	if err := s.Pool.QueryRow(ctx, `SELECT campaign_id FROM app.campaign_updates WHERE id = $1 AND status <> 'DELETED'`, updateID).Scan(&campaignID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Update{}, ErrNotFound
		}
		return Update{}, err
	}
	a, err := s.Core.Load(ctx, campaignID)
	if err != nil {
		return Update{}, err
	}
	if approve {
		if !campaigns.Live(a.Status) {
			return Update{}, campaigns.ErrInvalidStatus
		}
		lvl, err := s.Core.Restricted(ctx, a)
		if err != nil {
			return Update{}, fmt.Errorf("campaign updates: restriction check: %w", err)
		}
		if lvl == campaigns.RestrictionSuspended || lvl == campaigns.RestrictionOffboarded {
			return Update{}, ErrRestrictedNow
		}
	}
	var out Update
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if approve {
			if _, _, err := campaignRow(ctx, tx, campaignID); err != nil {
				return err
			}
		}
		cur, err := scanUpdate(tx.QueryRow(ctx, `SELECT `+updateCols+` FROM app.campaign_updates WHERE id = $1 FOR UPDATE`, updateID))
		if err != nil {
			return err
		}
		if err := s.notSelf(ctx, tx, staffID, cur.authorID, a); err != nil {
			return err
		}
		now := s.now()
		if approve {
			if cur.Status != StatusPendingModeration {
				return ErrUpdateStatus
			}
			if err := s.checkMedia(ctx, campaignID, cur.MediaIDs); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE app.campaign_updates SET status = 'PUBLISHED', published_at = $2, approved_by = $3, approved_at = $2
				WHERE id = $1`, cur.ID, now, staffID); err != nil {
				return err
			}
			if err := s.event(ctx, tx, cur.ID, campaignID, cur.Version+1, "APPROVED", cur.Status, StatusPublished, "STAFF", staffID, d.ReasonCode, d.Note); err != nil {
				return err
			}
			if err := s.record(ctx, tx, AuditPublished, cur.ID, campaignID, cur.Status, StatusPublished, &d); err != nil {
				return err
			}
			if err := s.emitFor(ctx, tx, StatusPublished, cur.ID, campaignID, "MODERATOR"); err != nil {
				return err
			}
		} else {
			if cur.Status != StatusPendingModeration && cur.Status != StatusPublished {
				return ErrUpdateStatus
			}
			if _, err := tx.Exec(ctx, `UPDATE app.campaign_updates SET status = 'HIDDEN', hidden_by = $2, hidden_at = $3, hidden_reason = $4, hidden_note = $5
				WHERE id = $1`, cur.ID, staffID, now, d.ReasonCode, d.Note); err != nil {
				return err
			}
			if err := s.event(ctx, tx, cur.ID, campaignID, cur.Version+1, "HIDDEN", cur.Status, StatusHidden, "STAFF", staffID, d.ReasonCode, d.Note); err != nil {
				return err
			}
			if err := s.record(ctx, tx, AuditHidden, cur.ID, campaignID, cur.Status, StatusHidden, &d); err != nil {
				return err
			}
		}
		out, err = scanUpdate(tx.QueryRow(ctx, `SELECT `+updateCols+` FROM app.campaign_updates WHERE id = $1`, cur.ID))
		return err
	})
	return out, err
}

var reasonRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,63}$`)

// validDecision checks a decision body ({reason_code UPPER_SNAKE, note 3–5000}) and returns it with the note
// cleaned (control characters stripped).
func validDecision(d Decision) (Decision, error) {
	var details []errs.Detail
	if !reasonRE.MatchString(d.ReasonCode) {
		details = append(details, errs.Detail{Field: "reason_code", Code: "INVALID_VALUE"})
	}
	d.Note = Clean(d.Note, true)
	if n := utf8.RuneCountInString(d.Note); n < 3 || n > 5000 {
		details = append(details, errs.Detail{Field: "note", Code: "INVALID_LENGTH"})
	}
	if len(details) > 0 {
		return d, httpx.Validation(details...)
	}
	return d, nil
}

// notSelf refuses a moderator who is (or is linked to) the author, the personal owner or creator, or a member of
// the owning organisation. The database trigger campaign_updates_not_self enforces the same rule.
func (s *Service) notSelf(ctx context.Context, tx pgx.Tx, staffID, authorID string, a campaigns.Access) error {
	idents := []string{staffID}
	personal, err := users.PersonalAccount(ctx, tx, staffID)
	if err != nil {
		return err
	}
	if personal != "" {
		idents = append(idents, personal)
	}
	for _, id := range idents {
		if id == authorID || id == a.CreatedBy || (a.OwnerUserID != nil && id == *a.OwnerUserID) {
			return ErrSelfModerate
		}
		if a.OwnerOrgID != nil {
			role, err := s.Orgs.MemberRole(ctx, *a.OwnerOrgID, id)
			if err != nil {
				return err
			}
			if role != "" {
				return ErrSelfModerate
			}
		}
	}
	return nil
}

// ---- history, audit, outbox -----------------------------------------------------------------------------------

func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func (s *Service) event(ctx context.Context, tx pgx.Tx, updateID, campaignID string, version int, typ, from, to, actorType, actorID, reason, note string) error {
	_, err := tx.Exec(ctx, `INSERT INTO app.campaign_update_events (id, update_id, campaign_id, update_version, event_type, from_status, to_status,
		actor_type, actor_id, reason_code, note, correlation_id, occurred_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		ids.New(), updateID, campaignID, version, typ, nullable(from), to, actorType, actorID, nullable(reason), nullable(note),
		nullable(httpx.RequestID(ctx)), s.now())
	return err
}

func (s *Service) record(ctx context.Context, tx pgx.Tx, action, updateID, campaignID, from, to string, d *Decision) error {
	md := map[string]any{"campaign_id": campaignID, "from_status": from, "to_status": to}
	e := audit.Event{Action: action, TargetType: "campaign_update", TargetID: updateID, Metadata: md, OccurredAt: s.now()}
	if d != nil {
		md["reason_code"] = d.ReasonCode
		e.Reason = d.ReasonCode
		e.Justification = d.Note
	}
	return audit.Record(ctx, tx, e)
}

// emitFor writes the outbox event for a status reached (ids, statuses and codes only).
func (s *Service) emitFor(ctx context.Context, tx pgx.Tx, status, updateID, campaignID, via string) error {
	typ := ""
	switch status {
	case StatusPublished:
		typ = EventPublished
	case StatusPendingModeration:
		typ = EventSubmitted
	default:
		return nil
	}
	_, err := outbox.Write(ctx, tx, outbox.Event{AggregateType: "campaign_update", AggregateID: updateID, EventType: typ,
		Payload:       map[string]any{"update_id": updateID, "campaign_id": campaignID, "status": status, "via": via},
		CorrelationID: httpx.RequestID(ctx), OccurredAt: s.now()})
	return err
}
