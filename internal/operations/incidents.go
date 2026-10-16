package operations

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Operational incidents (brief §51–§52): declared by staff, or from an alert (one open incident per alert key). The
// timeline is append-only. Incidents are distinct from support cases (customer questions) and from risk cases.

// IncidentEdges mirrors the 'ops_incident' machine (keep identical; TestOpsEdgesMatchDatabase).
var IncidentEdges = map[string][]string{
	"":              {"DETECTED"},
	"DETECTED":      {"ACKNOWLEDGED"},
	"ACKNOWLEDGED":  {"INVESTIGATING"},
	"INVESTIGATING": {"MITIGATING", "MONITORING", "RESOLVED"},
	"MITIGATING":    {"MONITORING", "RESOLVED"},
	"MONITORING":    {"INVESTIGATING", "RESOLVED"},
	"RESOLVED":      {"CLOSED"},
}

var moduleRE = regexp.MustCompile(`^[a-z][a-z_]{1,40}$`)
var severities = map[string]bool{"SEV1": true, "SEV2": true, "SEV3": true, "SEV4": true}

// Incident is an operational incident.
type Incident struct {
	ID                string     `json:"id"`
	IncidentNumber    int64      `json:"incident_number"`
	Title             string     `json:"title"`
	Severity          string     `json:"severity"`
	AffectedModule    string     `json:"affected_module"`
	Status            string     `json:"status"`
	OwnerID           *string    `json:"owner_id"`
	StartedAt         time.Time  `json:"started_at"`
	DetectedBy        string     `json:"detected_by"`
	AlertKey          *string    `json:"alert_key"`
	Resolution        *string    `json:"resolution"`
	ResolvedAt        *time.Time `json:"resolved_at"`
	PostIncidentNotes *string    `json:"post_incident_notes"`
	CreatedBy         string     `json:"created_by"`
	Version           int        `json:"version"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

const incidentCols = `id, incident_number, title, severity, affected_module, status, owner_id::text, started_at, detected_by, alert_key, resolution,
	resolved_at, post_incident_notes, created_by::text, version, created_at, updated_at`

func scanIncident(r pgx.Row) (Incident, error) {
	var x Incident
	err := r.Scan(&x.ID, &x.IncidentNumber, &x.Title, &x.Severity, &x.AffectedModule, &x.Status, &x.OwnerID, &x.StartedAt, &x.DetectedBy, &x.AlertKey,
		&x.Resolution, &x.ResolvedAt, &x.PostIncidentNotes, &x.CreatedBy, &x.Version, &x.CreatedAt, &x.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Incident{}, ErrNotFound
	}
	return x, err
}

// NewIncident declares an incident.
type NewIncident struct {
	Title          string     `json:"title"`
	Severity       string     `json:"severity"`
	AffectedModule string     `json:"affected_module"`
	StartedAt      *time.Time `json:"started_at"`
	AlertKey       string     `json:"alert_key"` // declared from an alert (one open incident per alert)
	Note           string     `json:"note"`
}

// DeclareIncident declares an incident (ops.incident.manage). From an alert, an open incident for the same alert is
// returned instead of a duplicate.
func (s *Service) DeclareIncident(ctx context.Context, staffID string, in NewIncident) (Incident, error) {
	title, err := text("title", in.Title, 3, 200)
	if err != nil {
		return Incident{}, err
	}
	if !severities[in.Severity] {
		return Incident{}, validation("severity", "INVALID_VALUE")
	}
	if !moduleRE.MatchString(in.AffectedModule) {
		return Incident{}, validation("affected_module", "INVALID_FORMAT")
	}
	now := s.now()
	started := now
	if in.StartedAt != nil {
		if in.StartedAt.After(now) {
			return Incident{}, validation("started_at", "IN_THE_FUTURE")
		}
		started = in.StartedAt.UTC()
	}
	detected, alert := "STAFF", strings.TrimSpace(in.AlertKey)
	if alert != "" {
		if !regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,80}$`).MatchString(alert) {
			return Incident{}, validation("alert_key", "INVALID_FORMAT")
		}
		detected = "ALERT"
	}
	id := ids.New()
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if alert != "" {
			var existing string
			err := tx.QueryRow(ctx, `SELECT id::text FROM app.ops_incidents WHERE alert_key = $1 AND status NOT IN ('RESOLVED','CLOSED')`, alert).Scan(&existing)
			if err == nil {
				id = existing
				return nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		var ak any
		if alert != "" {
			ak = alert
		}
		tag, err := tx.Exec(ctx, `INSERT INTO app.ops_incidents (id, title, severity, affected_module, status, owner_id, started_at, detected_by, alert_key,
			created_by) VALUES ($1, $2, $3, $4, 'DETECTED', $5, $6, $7, $8, $5) ON CONFLICT DO NOTHING`,
			id, title, in.Severity, in.AffectedModule, staffID, started, detected, ak)
		if err != nil {
			return mapDB(err)
		}
		if tag.RowsAffected() == 0 { // declared concurrently from the same alert
			return tx.QueryRow(ctx, `SELECT id::text FROM app.ops_incidents WHERE alert_key = $1 AND status NOT IN ('RESOLVED','CLOSED')`, alert).Scan(&id)
		}
		if err := incidentEvent(ctx, tx, id, "DECLARED", staffID, "", "DETECTED", strings.TrimSpace(in.Note), map[string]any{"alert_key": alert}, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "ops.incident.declared", TargetType: "ops_incident", TargetID: id,
			Metadata: map[string]any{"severity": in.Severity, "affected_module": in.AffectedModule, "alert_key": alert}})
	})
	if err != nil {
		return Incident{}, err
	}
	return s.Incident(ctx, id)
}

// Incident returns one incident.
func (s *Service) Incident(ctx context.Context, id string) (Incident, error) {
	if !ids.Valid(id) {
		return Incident{}, ErrNotFound
	}
	return scanIncident(s.Pool.QueryRow(ctx, `SELECT `+incidentCols+` FROM app.ops_incidents WHERE id = $1`, id))
}

// Incidents lists incidents (open first, then newest).
func (s *Service) Incidents(ctx context.Context, status string) ([]Incident, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+incidentCols+` FROM app.ops_incidents WHERE ($1 = '' OR status = $1 OR ($1 = 'OPEN_ANY' AND status NOT IN ('RESOLVED','CLOSED')))
		ORDER BY (status IN ('RESOLVED','CLOSED')), severity, started_at DESC LIMIT 200`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Incident{}
	for rows.Next() {
		x, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// IncidentOp is a staff action on an incident.
type IncidentOp struct {
	StaffID         string
	IncidentID      string
	ExpectedVersion int
	To              string // target status ("" = note or owner change only)
	OwnerID         string
	Note            string
	Severity        string // optional re-rating
}

// UpdateIncident applies a transition, an owner change, a severity change and/or a timeline note, atomically.
func (s *Service) UpdateIncident(ctx context.Context, op IncidentOp) (Incident, error) {
	note := strings.TrimSpace(op.Note)
	if len([]rune(note)) > 4000 {
		return Incident{}, validation("note", "INVALID_LENGTH")
	}
	if op.To == "RESOLVED" && len([]rune(note)) < 5 {
		return Incident{}, validation("note", "REQUIRED")
	}
	if op.OwnerID != "" && !ids.Valid(op.OwnerID) {
		return Incident{}, validation("owner_id", "INVALID_FORMAT")
	}
	if op.Severity != "" && !severities[op.Severity] {
		return Incident{}, validation("severity", "INVALID_VALUE")
	}
	if op.OwnerID != "" && s.Perms != nil {
		ps, err := s.Perms.EffectivePermissions(ctx, op.OwnerID)
		if err != nil {
			return Incident{}, err
		}
		if !contains(ps, PermIncidentManage) {
			return Incident{}, ErrAssigneeDenied
		}
	}
	if !ids.Valid(op.IncidentID) {
		return Incident{}, ErrNotFound
	}
	now := s.now()
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		x, err := scanIncident(tx.QueryRow(ctx, `SELECT `+incidentCols+` FROM app.ops_incidents WHERE id = $1 FOR UPDATE`, op.IncidentID))
		if err != nil {
			return err
		}
		if op.ExpectedVersion > 0 && x.Version != op.ExpectedVersion {
			return ErrStale
		}
		if x.Status == "CLOSED" {
			if op.To != "" || op.OwnerID != "" || op.Severity != "" {
				return ErrState
			}
			// post-incident notes may still be appended after closure
		}
		if op.OwnerID != "" {
			if _, err := tx.Exec(ctx, `UPDATE app.ops_incidents SET owner_id = $2, version = version + 1 WHERE id = $1`, x.ID, op.OwnerID); err != nil {
				return mapDB(err)
			}
			if err := incidentEvent(ctx, tx, x.ID, "OWNER_CHANGED", op.StaffID, "", "", "", map[string]any{"owner_id": op.OwnerID}, now); err != nil {
				return err
			}
		}
		if op.Severity != "" && op.Severity != x.Severity {
			if _, err := tx.Exec(ctx, `UPDATE app.ops_incidents SET severity = $2, version = version + 1 WHERE id = $1`, x.ID, op.Severity); err != nil {
				return mapDB(err)
			}
			if err := incidentEvent(ctx, tx, x.ID, "SEVERITY_CHANGED", op.StaffID, "", "", "", map[string]any{"from": x.Severity, "to": op.Severity}, now); err != nil {
				return err
			}
		}
		switch {
		case op.To != "":
			if !allowed(IncidentEdges, x.Status, op.To) {
				return ErrState
			}
			var err error
			if op.To == "RESOLVED" {
				_, err = tx.Exec(ctx, `UPDATE app.ops_incidents SET status = 'RESOLVED', resolution = $2, resolved_at = $3, version = version + 1 WHERE id = $1`,
					x.ID, note, now)
			} else {
				_, err = tx.Exec(ctx, `UPDATE app.ops_incidents SET status = $2, version = version + 1 WHERE id = $1`, x.ID, op.To)
			}
			if err != nil {
				return mapDB(err)
			}
			if err := incidentEvent(ctx, tx, x.ID, "STATUS_"+op.To, op.StaffID, x.Status, op.To, note, nil, now); err != nil {
				return err
			}
		case note != "" && (x.Status == "RESOLVED" || x.Status == "CLOSED"):
			if _, err := tx.Exec(ctx, `UPDATE app.ops_incidents SET post_incident_notes = left(coalesce(post_incident_notes || E'\n\n', '') || $2, 8000),
				version = version + 1 WHERE id = $1`, x.ID, note); err != nil {
				return mapDB(err)
			}
			if err := incidentEvent(ctx, tx, x.ID, "POST_INCIDENT_NOTE", op.StaffID, "", "", note, nil, now); err != nil {
				return err
			}
		case note != "":
			if err := incidentEvent(ctx, tx, x.ID, "NOTE_ADDED", op.StaffID, "", "", note, nil, now); err != nil {
				return err
			}
		}
		return audit.Record(ctx, tx, audit.Event{Action: "ops.incident.updated", TargetType: "ops_incident", TargetID: x.ID,
			Metadata: map[string]any{"to": op.To, "owner_id": op.OwnerID, "severity": op.Severity}})
	})
	if err != nil {
		return Incident{}, err
	}
	return s.Incident(ctx, op.IncidentID)
}

// IncidentTimeline returns an incident's timeline.
func (s *Service) IncidentTimeline(ctx context.Context, id string) ([]HistoryEntry, error) {
	return s.history(ctx, `SELECT event_type, actor_id::text, coalesce(from_status, ''), coalesce(to_status, ''), coalesce(note, ''), occurred_at
		FROM app.ops_incident_events WHERE incident_id = $1 ORDER BY occurred_at, id`, id)
}

func incidentEvent(ctx context.Context, tx pgx.Tx, id, typ, actor, from, to, note string, detail map[string]any, at time.Time) error {
	if detail == nil {
		detail = map[string]any{}
	}
	b, _ := json.Marshal(detail)
	_, err := tx.Exec(ctx, `INSERT INTO app.ops_incident_events (id, incident_id, event_type, actor_id, from_status, to_status, note, detail, occurred_at)
		VALUES ($1, $2, $3, nullif($4, '')::uuid, nullif($5, ''), nullif($6, ''), nullif($7, ''), $8, $9)`, ids.New(), id, typ, actor, from, to, note, b, at)
	return err
}
