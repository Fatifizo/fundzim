package payouts

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/kyc"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// conflicted: owner, creator, last changer, or member of the owning organisation.
func (s *Service) conflicted(ctx context.Context, d Destination, actorID string) (bool, error) {
	actors, err := s.KYC.ActorIdentities(ctx, actorID) // includes a staff account's linked personal account
	if err != nil {
		return false, err
	}
	for _, a := range actors {
		if a == d.createdBy || a == d.lastChangedBy || (d.ownerUser != nil && *d.ownerUser == a) {
			return true, nil
		}
		if d.ownerOrg != nil {
			if role, err := s.Orgs.MemberRole(ctx, *d.ownerOrg, a); err != nil || role != "" {
				return role != "", err
			}
		}
	}
	return false, nil
}

// Assign assigns a PENDING_VERIFICATION destination to a reviewer (self by default).
func (s *Service) Assign(ctx context.Context, id, actorID, assignee string) error {
	if assignee == "" {
		assignee = actorID
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		d, err := s.load(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if d.Status != "PENDING_VERIFICATION" {
			return errNotAllowed
		}
		if bad, err := s.conflicted(ctx, d, assignee); err != nil {
			return err
		} else if bad {
			return errSelf
		}
		var version int
		if err := tx.QueryRow(ctx, `UPDATE app.payout_destinations SET assigned_to = $2, assigned_at = $3 WHERE id = $1 RETURNING version`,
			id, assignee, s.now()).Scan(&version); err != nil {
			return err
		}
		if err := s.event(ctx, tx, id, version, "ASSIGNED", d.Status, d.Status, "", actorID, "STAFF"); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "payout_destination.assigned", TargetType: "payout_destination", TargetID: id,
			Metadata: map[string]any{"assignee": assignee}})
	})
}

// ReviewInput is a reviewer decision on a destination.
type ReviewInput struct {
	ReasonCode  string
	Note        string
	UserMessage string
	NameMatch   bool // the holder name matches the owner's verified legal name / registered name (reviewer attests)
}

// Approve records OWNERSHIP PASS (documentary evidence reviewed by staff) and COMPLIANCE PASS, then VERIFIED.
// A provider lookup without a provider can never be approved: there is no ownership evidence.
func (s *Service) Approve(ctx context.Context, id, actorID string, in ReviewInput) error {
	if !kyc.ValidReasonCode(in.ReasonCode) {
		return httpx.Validation(errs.Detail{Field: "reason_code", Code: "INVALID_FORMAT"})
	}
	if !in.NameMatch {
		return errs.New(errs.Unprocessable, "NAME_MATCH_REQUIRED", "Confirm that the account holder name matches the verified owner before approving.")
	}
	d0, err := s.load(ctx, s.Pool, id, false)
	if err != nil {
		return err
	}
	if bad, err := s.conflicted(ctx, d0, actorID); err != nil {
		return err
	} else if bad {
		return errSelf
	}
	pol, err := s.KYC.ActivePolicy(ctx)
	if err != nil {
		return err
	}
	if det, err := s.KYC.RequirementsCheck(ctx, "destination_id", id, []kyc.Requirement{pol.Rules.PayoutDestination.OwnershipEvidence}); err != nil {
		return err
	} else if len(det) > 0 {
		return errs.New(errs.Conflict, "OWNERSHIP_EVIDENCE_MISSING", "There is no clean ownership evidence to confirm ownership. Provider confirmation is not available.")
	}
	evidence, err := s.KYC.EvidenceIDs(ctx, "destination_id", id)
	if err != nil {
		return err
	}
	// ownership is confirmed from the documentary evidence actually reviewed (never from the dev mock)
	docMethod := ""
	docs, err := s.KYC.DocumentsFor(ctx, "destination_id", id)
	if err != nil {
		return err
	}
	for _, doc := range docs {
		if doc.Status == "CLEAN" && (doc.DocumentType == "BANK_LETTER" || doc.DocumentType == "BANK_STATEMENT" || doc.DocumentType == "MOBILE_MONEY_STATEMENT") {
			docMethod = doc.DocumentType
			break
		}
	}
	if docMethod == "" {
		return errs.New(errs.Conflict, "OWNERSHIP_EVIDENCE_MISSING", "There is no clean ownership evidence to confirm ownership.")
	}
	note, err := s.KYC.RecordExternalNote(ctx, "destination_id", id, "payout_destination", in.Note)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		d, err := s.load(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if d.Status != "PENDING_VERIFICATION" {
			return errNotAllowed
		}
		if d.assignedTo == nil || *d.assignedTo != actorID {
			return errAssigned
		}
		method := docMethod
		if d.VerificationMethod != nil && *d.VerificationMethod != "PROVIDER_LOOKUP" {
			method = *d.VerificationMethod
		}
		if _, err := tx.Exec(ctx, `INSERT INTO app.payout_destination_checks (id, destination_id, details_version, check_kind, method, result, performed_by,
			evidence_record_ids, detail_code) VALUES ($1, $2, $3, 'OWNERSHIP', $4, 'PASS', $5, $6, 'NAME_MATCH_CONFIRMED')`,
			ids.New(), id, d.detailsVersion, method, actorID, append(evidence, note)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO app.payout_destination_checks (id, destination_id, details_version, check_kind, method, result, performed_by,
			evidence_record_ids, detail_code) VALUES ($1, $2, $3, 'COMPLIANCE', 'STAFF_REVIEW', 'PASS', $4, $5, $6)`,
			ids.New(), id, d.detailsVersion, actorID, []string{note}, in.ReasonCode); err != nil {
			return err
		}
		now := s.now()
		var exp any
		if v := pol.Rules.PayoutDestination.ValidityDays; v > 0 {
			exp = now.AddDate(0, 0, v)
		}
		var version int
		if err := tx.QueryRow(ctx, `UPDATE app.payout_destinations SET status = 'VERIFIED', ownership_status = 'CONFIRMED', compliance_status = 'APPROVED',
			verified_at = $2, verified_by = $3, expires_at = $4 WHERE id = $1 RETURNING version`, id, now, actorID, exp).Scan(&version); err != nil {
			return err
		}
		if err := s.event(ctx, tx, id, version, "VERIFIED", d.Status, "VERIFIED", in.ReasonCode, actorID, "STAFF"); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "payout_destination.verified", TargetType: "payout_destination",
			TargetID: id, Reason: in.ReasonCode, Metadata: map[string]any{"method": method}}); err != nil {
			return err
		}
		return s.emit(ctx, tx, "payouts.destination_verified", d, map[string]any{"reason_code": in.ReasonCode})
	})
}

// Reject records a failed ownership or compliance review.
func (s *Service) Reject(ctx context.Context, id, actorID string, in ReviewInput) error {
	if !kyc.ValidReasonCode(in.ReasonCode) || len(strings.TrimSpace(in.UserMessage)) < 3 {
		return httpx.Validation(errs.Detail{Field: "reason_code", Code: "INVALID_FORMAT"})
	}
	d0, err := s.load(ctx, s.Pool, id, false)
	if err != nil {
		return err
	}
	if bad, err := s.conflicted(ctx, d0, actorID); err != nil {
		return err
	} else if bad {
		return errSelf
	}
	note, err := s.KYC.RecordExternalNote(ctx, "destination_id", id, "payout_destination", in.Note)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		d, err := s.load(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if d.Status != "PENDING_VERIFICATION" {
			return errNotAllowed
		}
		if d.assignedTo == nil || *d.assignedTo != actorID {
			return errAssigned
		}
		if _, err := tx.Exec(ctx, `INSERT INTO app.payout_destination_checks (id, destination_id, details_version, check_kind, method, result, performed_by,
			evidence_record_ids, detail_code) VALUES ($1, $2, $3, 'COMPLIANCE', 'STAFF_REVIEW', 'FAIL', $4, $5, $6)`,
			ids.New(), id, d.detailsVersion, actorID, []string{note}, in.ReasonCode); err != nil {
			return err
		}
		var version int
		if err := tx.QueryRow(ctx, `UPDATE app.payout_destinations SET status = 'REJECTED', ownership_status = CASE WHEN ownership_status = 'PENDING'
			THEN 'FAILED' ELSE ownership_status END, compliance_status = 'REJECTED', assigned_to = NULL, assigned_at = NULL WHERE id = $1 RETURNING version`,
			id).Scan(&version); err != nil {
			return err
		}
		if err := s.event(ctx, tx, id, version, "REJECTED", d.Status, "REJECTED", in.ReasonCode, actorID, "STAFF"); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "payout_destination.rejected", TargetType: "payout_destination",
			TargetID: id, Reason: in.ReasonCode}); err != nil {
			return err
		}
		return s.emit(ctx, tx, "payouts.destination_rejected", d, map[string]any{"reason_code": in.ReasonCode})
	})
}

// RequestInformation returns a pending destination to UNVERIFIED with a message for the owner.
func (s *Service) RequestInformation(ctx context.Context, id, actorID, message string) error {
	message = strings.TrimSpace(message)
	if len(message) < 3 || len(message) > 2000 {
		return httpx.Validation(errs.Detail{Field: "message", Code: "INVALID_LENGTH"})
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		d, err := s.load(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if d.Status != "PENDING_VERIFICATION" {
			return errNotAllowed
		}
		if d.assignedTo == nil || *d.assignedTo != actorID {
			return errAssigned
		}
		var version int
		if err := tx.QueryRow(ctx, `UPDATE app.payout_destinations SET status = 'UNVERIFIED', ownership_status = 'NOT_STARTED', compliance_status = 'PENDING',
			assigned_to = NULL, assigned_at = NULL WHERE id = $1 RETURNING version`, id).Scan(&version); err != nil {
			return err
		}
		if err := s.event(ctx, tx, id, version, "INFORMATION_REQUESTED", d.Status, "UNVERIFIED", "", actorID, "STAFF"); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{Action: "payout_destination.information_requested", TargetType: "payout_destination", TargetID: id}); err != nil {
			return err
		}
		return s.emit(ctx, tx, "payouts.destination_information_requested", d, map[string]any{"message": message})
	})
}

// Suspend / Reinstate a verified destination.
func (s *Service) SetSuspended(ctx context.Context, id, actorID, reason string, suspend bool) error {
	if !kyc.ValidReasonCode(reason) {
		return httpx.Validation(errs.Detail{Field: "reason_code", Code: "INVALID_FORMAT"})
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		d, err := s.load(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if bad, err := s.conflicted(ctx, d, actorID); err != nil {
			return err
		} else if bad {
			return errSelf
		}
		from, to, ev := "VERIFIED", "SUSPENDED", "payouts.destination_suspended"
		if !suspend {
			from, to, ev = "SUSPENDED", "VERIFIED", ""
		}
		if d.Status != from {
			return errNotAllowed
		}
		var version int
		if err := tx.QueryRow(ctx, `UPDATE app.payout_destinations SET status = $2 WHERE id = $1 RETURNING version`, id, to).Scan(&version); err != nil {
			return err
		}
		if err := s.event(ctx, tx, id, version, strings.ToUpper(to), from, to, reason, actorID, "STAFF"); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "payout_destination." + strings.ToLower(to), TargetType: "payout_destination",
			TargetID: id, Reason: reason}); err != nil {
			return err
		}
		if ev == "" {
			return nil
		}
		return s.emit(ctx, tx, ev, d, map[string]any{"reason_code": reason})
	})
}

// Queue lists destinations waiting for review.
func (s *Service) Queue(ctx context.Context, f kyc.QueueFilter) ([]kyc.QueueItem, error) {
	statuses := f.Statuses
	if len(statuses) == 0 {
		statuses = []string{"PENDING_VERIFICATION"}
	}
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	assigned := f.Assigned
	if assigned == "" {
		assigned = "any"
	}
	var actor any
	if f.ActorID != "" {
		actor = f.ActorID
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, status, assigned_to, requested_at FROM app.payout_destinations WHERE status = ANY($1)
		AND ($2 = 'any' OR ($2 = 'me' AND assigned_to = $3::uuid) OR ($2 = 'unassigned' AND assigned_to IS NULL))
		AND ($4::timestamptz IS NULL OR (requested_at, id) > ($4, $6::uuid)) ORDER BY requested_at NULLS LAST, id LIMIT $5`, statuses, assigned, actor, f.Before, limit, f.CursorID())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []kyc.QueueItem{}
	for rows.Next() {
		q := kyc.QueueItem{Type: "PAYOUT_DESTINATION", SubjectType: "PAYOUT_DESTINATION", RiskLevel: "STANDARD"}
		if err := rows.Scan(&q.ID, &q.Status, &q.AssignedTo, &q.SubmittedAt); err != nil {
			return nil, err
		}
		q.SubjectID = q.ID
		out = append(out, q)
	}
	return out, rows.Err()
}

// Exists reports whether id is a payout destination.
func (s *Service) Exists(ctx context.Context, id string) (bool, error) {
	if !ids.Valid(id) {
		return false, nil
	}
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM app.payout_destinations WHERE id = $1`, id).Scan(&n)
	return n > 0, err
}

// CheckView is a check row for reviewers.
type CheckView struct {
	Kind           string    `json:"kind"`
	Method         string    `json:"method"`
	Result         string    `json:"result"`
	NonProduction  bool      `json:"non_production"`
	DetailCode     *string   `json:"detail_code"`
	DetailsVersion int       `json:"details_version"`
	PerformedAt    time.Time `json:"performed_at"`
}

// ChecksOf returns all checks of a destination.
func (s *Service) ChecksOf(ctx context.Context, id string) ([]CheckView, error) {
	rows, err := s.Pool.Query(ctx, `SELECT check_kind, method, result, non_production, detail_code, details_version, performed_at
		FROM app.payout_destination_checks WHERE destination_id = $1 ORDER BY performed_at`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CheckView{}
	for rows.Next() {
		var c CheckView
		if err := rows.Scan(&c.Kind, &c.Method, &c.Result, &c.NonProduction, &c.DetailCode, &c.DetailsVersion, &c.PerformedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// History returns the timeline.
func (s *Service) History(ctx context.Context, id string) ([]kyc.HistoryItem, error) {
	rows, err := s.Pool.Query(ctx, `SELECT event_type, from_status, to_status, actor_type, actor_id, reason_code, '{}'::jsonb, occurred_at
		FROM app.payout_destination_events WHERE destination_id = $1 ORDER BY destination_version`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []kyc.HistoryItem{}
	for rows.Next() {
		var h kyc.HistoryItem
		if err := rows.Scan(&h.EventType, &h.FromStatus, &h.ToStatus, &h.ActorType, &h.ActorID, &h.ReasonCode, &h.Payload, &h.OccurredAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// Sweep expires verified destinations past their validity.
func (s *Service) Sweep(ctx context.Context, limit int) (int, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id FROM app.payout_destinations WHERE status = 'VERIFIED' AND expires_at <= $1 LIMIT $2`, s.now(), limit)
	if err != nil {
		return 0, err
	}
	var idList []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		idList = append(idList, id)
	}
	rows.Close()
	n := 0
	for _, id := range idList {
		err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			d, err := s.load(ctx, tx, id, true)
			if err != nil || d.Status != "VERIFIED" || d.ExpiresAt == nil || d.ExpiresAt.After(s.now()) {
				return err
			}
			var version int
			if err := tx.QueryRow(ctx, `UPDATE app.payout_destinations SET status = 'EXPIRED' WHERE id = $1 RETURNING version`, id).Scan(&version); err != nil {
				return err
			}
			if err := s.event(ctx, tx, id, version, "EXPIRED", "VERIFIED", "EXPIRED", "VERIFICATION_EXPIRED", "", ""); err != nil {
				return err
			}
			return s.emit(ctx, tx, "payouts.destination_expired", d, nil)
		})
		if err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
