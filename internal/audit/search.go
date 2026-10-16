package audit

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Audit center reads (Stage 14, ADR-046 §7). The audit log is append-only (triggers and the hash chain); these functions
// only read it. Results are bounded and keyset-paginated; IP address and user agent are returned only when the caller
// may see security detail.

// SearchFilter filters audit events.
type SearchFilter struct {
	From, To      *time.Time
	Action        string // exact or prefix ending in "."
	ActorID       string
	TargetType    string
	TargetID      string
	CorrelationID string
	RequestID     string
	Outcome       string
	Security      bool // the security stream (audit.security_audit_events) instead of the platform stream
	Limit         int
	BeforeSeq     int64 // keyset cursor (seq strictly lower)
	WithNetwork   bool  // include ip and user_agent (security_audit.read)
}

// Record is one audit event as staff see it.
type RecordView struct {
	ID            string          `json:"id"`
	Seq           int64           `json:"seq"`
	OccurredAt    time.Time       `json:"occurred_at"`
	ActorType     string          `json:"actor_type"`
	ActorID       *string         `json:"actor_id"`
	ActorRole     *string         `json:"actor_role"`
	Action        string          `json:"action"`
	TargetType    string          `json:"target_type"`
	TargetID      *string         `json:"target_id"`
	Outcome       string          `json:"outcome"`
	RequestID     *string         `json:"request_id"`
	CorrelationID *string         `json:"correlation_id"`
	Reason        *string         `json:"reason"`
	Justification *string         `json:"justification"`
	Metadata      json.RawMessage `json:"metadata"`
	BreakGlass    bool            `json:"break_glass"`
	IP            *string         `json:"ip,omitempty"`
	UserAgent     *string         `json:"user_agent,omitempty"`
	Hash          string          `json:"hash"`
}

// ErrBadFilter marks an unusable filter.
var ErrBadFilter = errors.New("audit: invalid filter")

const maxRange = 366 * 24 * time.Hour

// Search returns matching events, newest first, and the next cursor ("" when there are no more).
func Search(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, f SearchFilter) ([]RecordView, string, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	if f.From != nil && f.To != nil && (f.To.Before(*f.From) || f.To.Sub(*f.From) > maxRange) {
		return nil, "", ErrBadFilter
	}
	table := "audit.audit_events"
	if f.Security {
		table = "audit.security_audit_events"
	}
	where, args := []string{"true"}, []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+strconv.Itoa(len(args))))
	}
	if f.From != nil {
		add("occurred_at >= ?", *f.From)
	}
	if f.To != nil {
		add("occurred_at < ?", *f.To)
	}
	switch a := strings.TrimSpace(f.Action); {
	case a == "":
	case strings.HasSuffix(a, "."):
		add("starts_with(action, ?)", a)
	default:
		add("action = ?", a)
	}
	if f.ActorID != "" {
		add("actor_id = ?::uuid", f.ActorID)
	}
	if f.TargetType != "" {
		add("target_type = ?", f.TargetType)
	}
	if f.TargetID != "" {
		add("target_id = ?::uuid", f.TargetID)
	}
	if f.CorrelationID != "" {
		add("correlation_id = ?", f.CorrelationID)
	}
	if f.RequestID != "" {
		add("request_id = ?", f.RequestID)
	}
	if f.Outcome != "" {
		add("outcome = ?", f.Outcome)
	}
	if f.BeforeSeq > 0 {
		add("seq < ?", f.BeforeSeq)
	}
	args = append(args, f.Limit+1)
	rows, err := q.Query(ctx, `SELECT id::text, seq, occurred_at, actor_type, actor_id::text, actor_role, action, target_type, target_id::text, outcome,
		request_id, correlation_id, reason, justification, metadata, break_glass, host(ip), user_agent, encode(hash, 'hex')
		FROM `+table+` WHERE `+strings.Join(where, " AND ")+` ORDER BY seq DESC LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []RecordView{}
	for rows.Next() {
		var r RecordView
		if err := rows.Scan(&r.ID, &r.Seq, &r.OccurredAt, &r.ActorType, &r.ActorID, &r.ActorRole, &r.Action, &r.TargetType, &r.TargetID, &r.Outcome,
			&r.RequestID, &r.CorrelationID, &r.Reason, &r.Justification, &r.Metadata, &r.BreakGlass, &r.IP, &r.UserAgent, &r.Hash); err != nil {
			return nil, "", err
		}
		if !f.WithNetwork {
			r.IP, r.UserAgent = nil, nil
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > f.Limit {
		out = out[:f.Limit]
		next = strconv.FormatInt(out[len(out)-1].Seq, 10)
	}
	return out, next, nil
}

// ExportRow is one exported audit event (fixed columns; metadata as compact JSON; no IP or user agent).
func ExportRow(r RecordView) []string {
	s := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	return []string{r.ID, strconv.FormatInt(r.Seq, 10), r.OccurredAt.UTC().Format(time.RFC3339Nano), r.ActorType, s(r.ActorID), s(r.ActorRole), r.Action,
		r.TargetType, s(r.TargetID), r.Outcome, s(r.RequestID), s(r.CorrelationID), s(r.Reason), string(r.Metadata), r.Hash}
}

// ExportHeader names the ExportRow columns.
var ExportHeader = []string{"id", "seq", "occurred_at", "actor_type", "actor_id", "actor_role", "action", "target_type", "target_id", "outcome",
	"request_id", "correlation_id", "reason", "metadata", "hash"}
