package kyc

import (
	"context"
	"crypto/sha256"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// Errors shared by handlers (stable codes, contract §7).
var (
	ErrCaseNotFound      = errs.New(errs.NotFound, "CASE_NOT_FOUND", "No such verification case.")
	ErrCaseNotEditable   = errs.New(errs.Conflict, "CASE_NOT_EDITABLE", "This case can no longer be changed.")
	ErrCaseStateChanged  = errs.New(errs.Conflict, "CASE_STATE_CHANGED", "The case changed in the meantime. Reload and try again.")
	ErrCaseAlreadyOpen   = errs.New(errs.Conflict, "CASE_ALREADY_OPEN", "A verification case is already open.")
	ErrNotAssigned       = errs.New(errs.Conflict, "NOT_ASSIGNED", "Assign the case to yourself before deciding it.")
	ErrSelfDecision      = errs.New(errs.Forbidden, "SELF_DECISION_FORBIDDEN", "You cannot review a verification that concerns you or your organisation.")
	ErrSecondApproverSam = errs.New(errs.Forbidden, "SECOND_APPROVER_MUST_DIFFER", "A different reviewer must give the second approval.")
	ErrActionNotAllowed  = errs.New(errs.Conflict, "ACTION_NOT_ALLOWED", "This action is not allowed in the case's current status.")
	ErrDocumentNotFound  = errs.New(errs.NotFound, "DOCUMENT_NOT_FOUND", "No such document.")
)

var reasonCodeRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,63}$`)

// ValidReasonCode reports whether c is an UPPER_SNAKE reason code.
func ValidReasonCode(c string) bool { return reasonCodeRe.MatchString(c) }

func isUnique(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

func isCheck(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && (pg.Code == "23514" || pg.Code == "23000" || pg.Code == "23001")
}

// actor returns the audit actor type and id of the request principal.
func actor(ctx context.Context) (typ, id string) {
	p := authz.PrincipalFrom(ctx)
	if p == nil {
		return "SYSTEM", ""
	}
	if p.Kind == authz.KindStaff {
		return "STAFF", p.UserID
	}
	return "USER", p.UserID
}

func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// caseEvent records the timeline row for the case's current version (required by a deferred constraint).
func (s *Service) caseEvent(ctx context.Context, tx pgx.Tx, kind, caseID string, version int, typ, from, to, reason string, payload map[string]any) error {
	at, aid := actor(ctx)
	if payload == nil {
		payload = map[string]any{}
	}
	col := "kyc_case_id"
	if kind == KindKYB {
		col = "kyb_case_id"
	}
	_, err := tx.Exec(ctx, `INSERT INTO kyc.case_events (id, `+col+`, case_version, event_type, from_status, to_status, actor_type, actor_id,
		reason_code, payload, occurred_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		ids.New(), caseID, version, typ, nullable(from), to, at, nullable(aid), nullable(reason), payload, s.now())
	return err
}

// emit writes an outbox event through the gateway (contract §6: ids and codes only).
func (s *Service) emit(ctx context.Context, tx pgx.Tx, eventType, aggType, aggID string, payload map[string]any) error {
	_, err := outbox.WriteGateway(ctx, tx, outbox.Event{AggregateType: aggType, AggregateID: aggID, EventType: eventType,
		Payload: payload, CorrelationID: httpx.RequestID(ctx), OccurredAt: s.now()})
	return err
}

// note encrypts a reviewer note and records evidence (SHA-256 of the note) in one transaction; it returns the
// evidence record id that decisions reference. The note text never leaves reviewer endpoints.
func (s *Service) note(ctx context.Context, tx pgx.Tx, subjectCol, subjectID, subjectType, body string) (string, error) {
	body = strings.TrimSpace(body)
	if len(body) < 3 || len(body) > 5000 {
		return "", httpx.Validation(errs.Detail{Field: "note", Code: "INVALID_LENGTH"})
	}
	_, staffID := actor(ctx)
	noteID := ids.New()
	ct, err := s.AEAD.Seal([]byte(body), aad("review_notes", noteID, "body"))
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO kyc.review_notes (id, `+subjectCol+`, body_ciphertext, body_key_id, author_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`, noteID, subjectID, ct, s.AEAD.KeyID(), staffID, s.now()); err != nil {
		return "", err
	}
	evID, err := audit.RecordGateway(ctx, tx, audit.Event{Action: "evidence.review_note.recorded", TargetType: subjectType, TargetID: subjectID,
		Metadata: map[string]any{"note_id": noteID}})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(body))
	return audit.RecordEvidenceGateway(ctx, tx, audit.Evidence{Type: "REVIEW_NOTE", SubjectType: strings.ToLower(subjectType), SubjectID: subjectID,
		RelatedRefs: map[string]any{"note_id": noteID}, CollectedByType: "staff", CollectedByID: staffID, Source: "staff_note",
		SHA256: sum[:], SizeBytes: int64(len(body)), Classification: "C3", RetentionClass: "KYC", AuditEventID: evID, CollectedAt: s.now()})
}

// Note is a decrypted reviewer note (reviewer endpoints only).
type Note struct {
	ID        string    `json:"id"`
	AuthorID  string    `json:"author_id"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// Notes returns the reviewer notes of a subject (column kyc_case_id | kyb_case_id | beneficiary_id | destination_id).
func (s *Service) Notes(ctx context.Context, subjectCol, subjectID string) ([]Note, error) {
	switch subjectCol {
	case "kyc_case_id", "kyb_case_id", "beneficiary_id", "destination_id":
	default:
		return nil, errors.New("kyc: invalid note subject")
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, author_id, body_ciphertext, created_at FROM kyc.review_notes WHERE `+subjectCol+` = $1
		ORDER BY created_at`, subjectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Note{}
	for rows.Next() {
		var n Note
		var ct []byte
		if err := rows.Scan(&n.ID, &n.AuthorID, &ct, &n.CreatedAt); err != nil {
			return nil, err
		}
		pt, err := s.AEAD.Open(ct, aad("review_notes", n.ID, "body"))
		if err != nil {
			return nil, err
		}
		n.Body = string(pt)
		out = append(out, n)
	}
	return out, rows.Err()
}

// RecordExternalNote stores a reviewer note for a subject owned by another module (beneficiary or payout
// destination) and returns the evidence record id (the note lives in the kyc schema because it may contain
// identity details).
func (s *Service) RecordExternalNote(ctx context.Context, subjectCol, subjectID, subjectType, body string) (string, error) {
	if subjectCol != "beneficiary_id" && subjectCol != "destination_id" {
		return "", errors.New("kyc: external notes are for beneficiaries and payout destinations")
	}
	var id string
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		id, err = s.note(ctx, tx, subjectCol, subjectID, subjectType, body)
		return err
	})
	return id, err
}

// profileEvent writes the profile_events row for a profile or KYB organisation's new version.
func (s *Service) profileEvent(ctx context.Context, tx pgx.Tx, profileID, kybOrgID string, version int, fromLevel, toLevel, fromStatus,
	toStatus, reason, kycCaseID, kybCaseID string) error {
	at, aid := actor(ctx)
	_, err := tx.Exec(ctx, `INSERT INTO kyc.profile_events (id, profile_id, kyb_organisation_id, aggregate_version, from_level, to_level,
		from_status, to_status, reason_code, kyc_case_id, kyb_case_id, actor_type, actor_id, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		ids.New(), nullable(profileID), nullable(kybOrgID), version, nullable(fromLevel), toLevel, nullable(fromStatus), toStatus, reason,
		nullable(kycCaseID), nullable(kybCaseID), at, nullable(aid), s.now())
	return err
}

// setProfile moves an individual profile to (level, status) with its event and the projection event.
func (s *Service) setProfile(ctx context.Context, tx pgx.Tx, profileID, level, status, reason, caseID string, expiresAt *time.Time) error {
	var userID, oldLevel, oldStatus string
	if err := tx.QueryRow(ctx, `SELECT user_id, level, status FROM kyc.verification_profiles WHERE id = $1 FOR UPDATE`, profileID).
		Scan(&userID, &oldLevel, &oldStatus); err != nil {
		return err
	}
	if oldLevel == level && oldStatus == status && expiresAt == nil {
		return nil
	}
	var granted any
	if level != LevelUnverified {
		granted = s.now()
	}
	var version int
	if err := tx.QueryRow(ctx, `UPDATE kyc.verification_profiles SET level = $2, status = $3, level_granted_at = CASE WHEN $2 <> level THEN $4 ELSE level_granted_at END,
		level_expires_at = CASE WHEN $2 <> level OR $5::timestamptz IS NOT NULL THEN $5 ELSE level_expires_at END,
		status_changed_at = CASE WHEN $3 <> status THEN now() ELSE status_changed_at END, version = version + 1
		WHERE id = $1 RETURNING version`, profileID, level, status, granted, expiresAt).Scan(&version); err != nil {
		return err
	}
	if err := s.profileEvent(ctx, tx, profileID, "", version, oldLevel, level, oldStatus, status, reason, caseID, ""); err != nil {
		return err
	}
	if _, err := audit.RecordGateway(ctx, tx, audit.Event{Stream: audit.Security, Action: "kyc.level.changed", TargetType: "user", TargetID: userID,
		Reason: reason, Metadata: map[string]any{"from_level": oldLevel, "to_level": level, "from_status": oldStatus, "to_status": status}}); err != nil {
		return err
	}
	return s.emit(ctx, tx, "kyc.level_changed", "user", userID, map[string]any{"subject_type": "USER", "subject_id": userID,
		"level": level, "status": status})
}

// setOrgLevel is setProfile for KYB organisations.
func (s *Service) setOrgLevel(ctx context.Context, tx pgx.Tx, kybOrgID, level, status, reason, caseID string, expiresAt *time.Time) error {
	var orgID, oldLevel, oldStatus string
	if err := tx.QueryRow(ctx, `SELECT organisation_id, level, status FROM kyc.kyb_organisations WHERE id = $1 FOR UPDATE`, kybOrgID).
		Scan(&orgID, &oldLevel, &oldStatus); err != nil {
		return err
	}
	if oldLevel == level && oldStatus == status && expiresAt == nil {
		return nil
	}
	var granted any
	if level != OrgLevelUnverified {
		granted = s.now()
	}
	var version int
	if err := tx.QueryRow(ctx, `UPDATE kyc.kyb_organisations SET level = $2, status = $3, level_granted_at = CASE WHEN $2 <> level THEN $4 ELSE level_granted_at END,
		level_expires_at = CASE WHEN $2 <> level OR $5::timestamptz IS NOT NULL THEN $5 ELSE level_expires_at END,
		status_changed_at = CASE WHEN $3 <> status THEN now() ELSE status_changed_at END, version = version + 1
		WHERE id = $1 RETURNING version`, kybOrgID, level, status, granted, expiresAt).Scan(&version); err != nil {
		return err
	}
	if err := s.profileEvent(ctx, tx, "", kybOrgID, version, oldLevel, level, oldStatus, status, reason, "", caseID); err != nil {
		return err
	}
	if _, err := audit.RecordGateway(ctx, tx, audit.Event{Stream: audit.Security, Action: "kyb.level.changed", TargetType: "organisation", TargetID: orgID,
		Reason: reason, Metadata: map[string]any{"from_level": oldLevel, "to_level": level, "from_status": oldStatus, "to_status": status}}); err != nil {
		return err
	}
	return s.emit(ctx, tx, "kyc.level_changed", "organisation", orgID, map[string]any{"subject_type": "ORGANISATION", "subject_id": orgID,
		"level": level, "status": status})
}
