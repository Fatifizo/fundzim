package campaigns

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// Edges is the campaign machine (ADR-036 §2), identical to app.status_transitions machine 'campaign'
// (TestEdgesMatchMigration parses the migration).
var Edges = map[string][]string{
	"":                     {StatusDraft},
	StatusDraft:            {StatusSubmitted, StatusCancelled},
	StatusSubmitted:        {StatusUnderReview, StatusDraft},
	StatusUnderReview:      {StatusApproved, StatusRejected, StatusChangesRequested},
	StatusChangesRequested: {StatusSubmitted, StatusCancelled},
	StatusApproved:         {StatusActive, StatusCancelled, StatusSuspended},
	StatusActive:           {StatusPaused, StatusSuspended, StatusCompleted, StatusCancelled},
	StatusPaused:           {StatusActive, StatusSuspended, StatusCompleted, StatusCancelled},
	StatusSuspended:        {StatusActive, StatusApproved, StatusCancelled},
	StatusRejected:         {StatusDraft, StatusUnderReview, StatusArchived},
	StatusCompleted:        {StatusArchived},
	StatusCancelled:        {StatusArchived},
}

// Can reports whether from → to is a legal transition.
func Can(from, to string) bool {
	for _, t := range Edges[from] {
		if t == to {
			return true
		}
	}
	return false
}

// actor is who performs an action.
type actor struct {
	Type string // USER | STAFF | SYSTEM
	ID   string // "" for SYSTEM
}

func userActor(id string) actor  { return actor{Type: "USER", ID: id} }
func staffActor(id string) actor { return actor{Type: "STAFF", ID: id} }

var systemActor = actor{Type: "SYSTEM"}

func (a actor) idOrNil() any {
	if a.ID == "" {
		return nil
	}
	return a.ID
}

// change is one lifecycle step applied to a locked campaign row.
type change struct {
	to         string         // target status ("" = no status change, e.g. edit/assign)
	event      string         // history event_type, e.g. SUBMITTED
	action     string         // audit action suffix, e.g. "submitted" → campaign.submitted
	outboxType string         // outbox event type ("" = none)
	reasonCode string         // UPPER_SNAKE
	note       string         // free text (history only)
	set        map[string]any // extra columns
	payload    map[string]any // extra outbox/audit metadata (ids and codes only)
}

// apply writes the campaign update, the history row (one per version), the audit event and the outbox
// event in tx. c must have been loaded FOR UPDATE in tx.
func (s *Service) apply(ctx context.Context, tx pgx.Tx, c *campaignRow, a actor, ch change) error {
	if ch.to != "" && ch.to != c.Status && !Can(c.Status, ch.to) {
		return ErrInvalidStatus
	}
	to := c.Status
	if ch.to != "" {
		to = ch.to
	}
	sets, args := "status = $2", []any{c.ID, to}
	for col, v := range ch.set {
		args = append(args, v)
		sets += fmt.Sprintf(", %s = $%d", col, len(args))
	}
	args = append(args, c.Version)
	var version int
	if err := tx.QueryRow(ctx, `UPDATE app.campaigns SET `+sets+fmt.Sprintf(` WHERE id = $1 AND version = $%d RETURNING version`, len(args)),
		args...).Scan(&version); err != nil {
		if err == pgx.ErrNoRows {
			return ErrStateChanged
		}
		return err
	}
	from := c.Status
	if err := s.history(ctx, tx, c.ID, version, ch.event, &from, to, a, ch.reasonCode, ch.note, ch.payload); err != nil {
		return err
	}
	md := map[string]any{"from_status": from, "to_status": to}
	for k, v := range ch.payload {
		md[k] = v
	}
	if ch.reasonCode != "" {
		md["reason_code"] = ch.reasonCode
	}
	if ch.action != "" {
		if err := audit.Record(ctx, tx, audit.Event{Action: "campaign." + ch.action, ActorType: auditActorType(a), ActorID: a.ID,
			TargetType: "campaign", TargetID: c.ID, Reason: ch.reasonCode, Metadata: md}); err != nil {
			return err
		}
	}
	if ch.outboxType != "" {
		if err := s.emit(ctx, tx, c, ch.outboxType, md); err != nil {
			return err
		}
	}
	c.Status, c.Version = to, version
	return nil
}

func (s *Service) history(ctx context.Context, tx pgx.Tx, campaignID string, version int, event string, from *string, to string, a actor,
	reasonCode, note string, payload map[string]any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	_, err := tx.Exec(ctx, `INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, event_type, from_status, to_status,
		actor_type, actor_id, reason_code, note, correlation_id, payload, occurred_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		ids.New(), campaignID, version, event, from, to, a.Type, a.idOrNil(), nullable(reasonCode), nullable(note),
		nullable(httpx.RequestID(ctx)), payload, s.now())
	return err
}

// emit writes an outbox event whose payload carries ids, statuses and codes only.
func (s *Service) emit(ctx context.Context, tx pgx.Tx, c *campaignRow, eventType string, md map[string]any) error {
	p := map[string]any{"campaign_id": c.ID, "status": c.Status}
	for k, v := range md {
		p[k] = v
	}
	if c.OwnerUserID != nil {
		p["owner_user_id"] = *c.OwnerUserID
	}
	if c.OwnerOrgID != nil {
		p["owner_organisation_id"] = *c.OwnerOrgID
	}
	_, err := outbox.Write(ctx, tx, outbox.Event{AggregateType: "campaign", AggregateID: c.ID, EventType: eventType, Payload: p,
		CorrelationID: httpx.RequestID(ctx), OccurredAt: s.now()})
	return err
}

func auditActorType(a actor) string {
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
