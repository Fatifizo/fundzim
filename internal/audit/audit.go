// Package audit records append-only, hash-chained audit events (AUDIT.md, ADR-019) inside the caller's
// database transaction, so a committed change always has its audit event and a rolled-back one never does.
//
// Two streams: Business events go to audit.audit_events; Security events (authentication, sessions, MFA, role
// and account administration) go to audit.security_audit_events, readable with security_audit.read. The hash
// chain and sequence numbers are assigned by the database trigger.
//
// Metadata must never contain secrets, credentials, tokens, codes or identity documents (AUDIT §6): Record
// rejects metadata whose keys look sensitive.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/logging"
)

// Stream selects the audit table.
type Stream int

const (
	Business Stream = iota
	Security
)

// Event is one audit record. Action is dotted lower snake case, e.g. "auth.user.registered".
type Event struct {
	Stream        Stream
	Action        string
	ActorType     string // user | staff | system | provider | vendor (defaults from the principal or system)
	ActorID       string
	ActorRole     string
	TargetType    string
	TargetID      string
	Outcome       string // success | denied | failed (default success)
	Reason        string
	Justification string
	Metadata      map[string]any
	OccurredAt    time.Time
}

var actionRe = regexp.MustCompile(`^[a-z_]+(\.[a-z_*]+)+$`)

// Record inserts e in tx. Actor, request and correlation IDs and the client IP default from ctx.
func Record(ctx context.Context, tx pgx.Tx, e Event) error {
	if !actionRe.MatchString(e.Action) {
		return fmt.Errorf("audit: invalid action %q", e.Action)
	}
	for k := range e.Metadata {
		if logging.IsSensitiveKey(k) {
			return fmt.Errorf("audit: metadata key %q is not allowed (never-embed rule)", k)
		}
	}
	if e.Outcome == "" {
		e.Outcome = "success"
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now().UTC()
	}
	if e.ActorType == "" {
		if p := authz.PrincipalFrom(ctx); p != nil {
			e.ActorID = p.UserID
			e.ActorType = "user"
			if p.Kind == authz.KindStaff {
				e.ActorType = "staff"
			}
		} else {
			e.ActorType = "system"
		}
	}
	md := e.Metadata
	if md == nil {
		md = map[string]any{}
	}
	mdJSON, err := json.Marshal(md)
	if err != nil {
		return fmt.Errorf("audit: metadata: %w", err)
	}
	reqID := httpx.RequestID(ctx)
	var ip any
	if a := authz.ClientIPFrom(ctx); a.IsValid() {
		ip = netip.PrefixFrom(a, a.BitLen()).String()
	}
	table := "audit.audit_events"
	if e.Stream == Security {
		table = "audit.security_audit_events"
	}
	// seq, prev_hash and hash are assigned by audit.chain_append(); the placeholder values are ignored.
	_, err = tx.Exec(ctx, `INSERT INTO `+table+` (id, seq, occurred_at, actor_type, actor_id, actor_role, action, target_type,
		target_id, outcome, request_id, correlation_id, ip, reason, justification, metadata, hash)
		VALUES ($1, 0, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10, $11::inet, $12, $13, $14, '\x00')`,
		ids.New(), e.OccurredAt, e.ActorType, nullUUID(e.ActorID), nullStr(e.ActorRole), e.Action, e.TargetType,
		nullUUID(e.TargetID), e.Outcome, nullStr(reqID), ip, nullStr(e.Reason), nullStr(e.Justification), mdJSON)
	return err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullUUID(s string) any {
	if s == "" {
		return nil
	}
	return s
}
