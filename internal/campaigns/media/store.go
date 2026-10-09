package media

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/campaigns"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// actor is who performs a media action.
type actor struct {
	Type string // USER | STAFF | SYSTEM
	ID   string
}

var systemActor = actor{Type: "SYSTEM"}

func (a actor) idOrNil() any {
	if a.ID == "" {
		return nil
	}
	return a.ID
}

func (a actor) auditType() string {
	switch a.Type {
	case "STAFF":
		return "staff"
	case "SYSTEM":
		return "system"
	}
	return "user"
}

func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

const mediaSelect = `SELECT id::text, campaign_id::text, kind, position, alt_text, status, rejected_reason, content_type, width, height,
	approved_at, removed_at, removed_reason, version, created_at, updated_at, stored_object_id::text, processed_object_id::text,
	created_by::text FROM app.campaign_media`

// order: cover first, then gallery by position (ties by creation)
const mediaOrder = ` ORDER BY (kind = 'COVER') DESC, position, created_at, id`

func scanMedia(row pgx.Row) (Media, error) {
	var m Media
	err := row.Scan(&m.ID, &m.CampaignID, &m.Kind, &m.Position, &m.AltText, &m.Status, &m.RejectedReason, &m.ContentType, &m.Width,
		&m.Height, &m.ApprovedAt, &m.RemovedAt, &m.RemovedReason, &m.Version, &m.CreatedAt, &m.UpdatedAt, &m.storedObjectID,
		&m.processedObjectID, &m.createdBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

func (s *Service) tx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	return db.WithTx(ctx, s.d.Pool, db.TxOptions{}, fn)
}

// load returns one media item of a campaign (campaignID "" = any campaign; used by the worker).
func (s *Service) load(ctx context.Context, q querier, campaignID, mediaID string, lock bool) (Media, error) {
	if !ids.Valid(mediaID) || (campaignID != "" && !ids.Valid(campaignID)) {
		return Media{}, ErrNotFound
	}
	sql, args := mediaSelect+` WHERE id = $1`, []any{mediaID}
	if campaignID != "" {
		sql += ` AND campaign_id = $2`
		args = append(args, campaignID)
	}
	if lock {
		sql += ` FOR UPDATE`
	}
	return scanMedia(q.QueryRow(ctx, sql, args...))
}

func (s *Service) listFor(ctx context.Context, campaignID string) ([]Media, error) {
	rows, err := s.d.Pool.Query(ctx, mediaSelect+` WHERE campaign_id = $1`+mediaOrder, campaignID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Media, error) { return scanMedia(r) })
	if out == nil {
		out = []Media{}
	}
	return out, err
}

// event appends the timeline row for a media version.
func (s *Service) event(ctx context.Context, tx pgx.Tx, mediaID string, version int, typ string, from *string, to string, a actor,
	reason, note string, payload map[string]any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	_, err := tx.Exec(ctx, `INSERT INTO app.campaign_media_events (id, media_id, media_version, event_type, from_status, to_status,
		actor_type, actor_id, reason_code, note, correlation_id, payload, occurred_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		ids.New(), mediaID, version, typ, from, to, a.Type, a.idOrNil(), nullable(reason), nullable(note), nullable(httpx.RequestID(ctx)),
		payload, s.now())
	return err
}

// step is one change applied to a locked media row.
type step struct {
	to      string         // target status ("" = unchanged)
	event   string         // timeline event_type
	reason  string         // UPPER_SNAKE code
	note    string         // free text (timeline only; staff moderation)
	set     map[string]any // extra columns
	payload map[string]any // timeline payload (ids and codes only)
}

// apply updates the row (optimistic version check), writes the timeline row and updates m. m must have been loaded
// in tx (FOR UPDATE where the caller needs serialisation).
func (s *Service) apply(ctx context.Context, tx pgx.Tx, m *Media, a actor, st step) error {
	to := m.Status
	if st.to != "" && st.to != m.Status {
		if !Can(m.Status, st.to) {
			return ErrInvalidStatus
		}
		to = st.to
	}
	sets, args := "status = $2", []any{m.ID, to}
	for col, v := range st.set {
		args = append(args, v)
		sets += fmt.Sprintf(", %s = $%d", col, len(args))
	}
	args = append(args, m.Version)
	var version int
	err := tx.QueryRow(ctx, `UPDATE app.campaign_media SET `+sets+fmt.Sprintf(` WHERE id = $1 AND version = $%d RETURNING version`, len(args)),
		args...).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrStateChanged
	}
	if err != nil {
		return err
	}
	from := m.Status
	if err := s.event(ctx, tx, m.ID, version, st.event, &from, to, a, st.reason, st.note, st.payload); err != nil {
		return err
	}
	fresh, err := s.load(ctx, tx, "", m.ID, false)
	if err != nil {
		return err
	}
	*m = fresh
	return nil
}

// emit writes a campaigns.* outbox event (ids, statuses and codes only).
func (s *Service) emit(ctx context.Context, tx pgx.Tx, m Media, eventType string, extra map[string]any) error {
	p := map[string]any{"campaign_id": m.CampaignID, "media_id": m.ID, "kind": m.Kind, "status": m.Status}
	for k, v := range extra {
		p[k] = v
	}
	_, err := outbox.Write(ctx, tx, outbox.Event{AggregateType: "campaign", AggregateID: m.CampaignID, EventType: eventType, Payload: p,
		CorrelationID: httpx.RequestID(ctx), OccurredAt: s.now()})
	return err
}

func (s *Service) audit(ctx context.Context, tx pgx.Tx, a actor, action string, m Media, from, reason string, extra map[string]any) error {
	md := map[string]any{"campaign_id": m.CampaignID, "media_id": m.ID, "kind": m.Kind, "from_status": from, "to_status": m.Status}
	for k, v := range extra {
		md[k] = v
	}
	if reason != "" {
		md["reason_code"] = reason
	}
	ev := audit.Event{Action: action, ActorType: a.auditType(), ActorID: a.ID, TargetType: "campaign_media", TargetID: m.ID,
		Reason: reason, Metadata: md, OccurredAt: s.now()}
	return audit.Record(ctx, tx, ev)
}

func isUniqueViolation(err error, constraint string) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505" && (constraint == "" || pe.ConstraintName == constraint)
}

// lockCampaignStatus locks the campaign row (serialising media changes with lifecycle transitions) and returns its
// status.
func lockCampaignStatus(ctx context.Context, tx pgx.Tx, campaignID string) (string, error) {
	var st string
	err := tx.QueryRow(ctx, `SELECT status FROM app.campaigns WHERE id = $1 FOR UPDATE`, campaignID).Scan(&st)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", campaigns.ErrNotFound
	}
	return st, err
}

// ---- campaigns.MediaInfo --------------------------------------------------------------------------------

// Readiness summarises a campaign's media for the submission/publication gates.
func (s *Service) Readiness(ctx context.Context, campaignID string) (campaigns.MediaReadiness, error) {
	var r campaigns.MediaReadiness
	err := s.d.Pool.QueryRow(ctx, `SELECT
		  coalesce(bool_or(kind = 'COVER' AND status = 'APPROVED'), false),
		  count(*) FILTER (WHERE status IN ('UPLOADED', 'QUARANTINED', 'SCANNING')),
		  count(*) FILTER (WHERE status = 'REJECTED')
		FROM app.campaign_media WHERE campaign_id = $1`, campaignID).Scan(&r.CoverApproved, &r.Pending, &r.Rejected)
	return r, err
}

// ApprovedIDs returns the APPROVED media ids in display order (cover first), for version snapshots.
func (s *Service) ApprovedIDs(ctx context.Context, campaignID string) ([]string, error) {
	rows, err := s.d.Pool.Query(ctx, `SELECT id::text FROM app.campaign_media WHERE campaign_id = $1 AND status = 'APPROVED'`+mediaOrder,
		campaignID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if out == nil {
		out = []string{}
	}
	return out, err
}

// publicObject returns the derivative object id of APPROVED media that is part of the campaign's approved version.
func (s *Service) publicObject(ctx context.Context, campaignID, mediaID string) (string, error) {
	if !ids.Valid(mediaID) {
		return "", ErrNotAvailable
	}
	var obj string
	err := s.d.Pool.QueryRow(ctx, `SELECT m.processed_object_id::text FROM app.campaign_media m
		  JOIN app.campaigns c ON c.id = m.campaign_id
		  JOIN app.campaign_versions v ON v.id = c.approved_version_id AND v.campaign_id = c.id
		 WHERE m.id = $1 AND m.campaign_id = $2 AND m.status = 'APPROVED' AND m.processed_object_id IS NOT NULL
		   AND m.id = ANY (v.media_ids)`, mediaID, campaignID).Scan(&obj)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotAvailable
	}
	return obj, err
}
