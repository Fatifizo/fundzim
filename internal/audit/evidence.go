package audit

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/logging"
)

// Evidence is one evidence record (audit-evidence-model, ADR-019): metadata and a SHA-256 of the evidence,
// never its content. StorageRef is the stored object's ID (or another opaque locator) — never a URL.
type Evidence struct {
	Type            string // e.g. ID_DOCUMENT, REVIEW_NOTE, BANK_LETTER
	SubjectType     string // lower snake, e.g. kyc_case, beneficiary, payout_destination
	SubjectID       string
	RelatedRefs     map[string]any // IDs only
	CollectedAt     time.Time
	CollectedByType string // user | staff | system | provider | vendor
	CollectedByID   string
	Source          string // upload | staff_note | system_snapshot | ...
	StorageRef      string
	SHA256          []byte
	SizeBytes       int64
	MediaType       string
	Classification  string // C2 | C3
	RetentionClass  string // KYC | FINANCIAL | AUDIT | CASE | CONSENT | OPERATIONAL
	AuditEventID    string // the business audit event that recorded the collection (same transaction)
}

var evidenceTypeRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

func (e Evidence) prepare() ([]byte, error) {
	if !evidenceTypeRe.MatchString(e.Type) || len(e.SHA256) != 32 || e.AuditEventID == "" || e.SubjectID == "" {
		return nil, errors.New("audit: invalid evidence record")
	}
	for k := range e.RelatedRefs {
		if logging.IsSensitiveKey(k) {
			return nil, errors.New("audit: evidence related_refs key not allowed")
		}
	}
	refs := e.RelatedRefs
	if refs == nil {
		refs = map[string]any{}
	}
	return json.Marshal(refs)
}

// RecordEvidence inserts an evidence record (app pool) and returns its ID.
func RecordEvidence(ctx context.Context, tx pgx.Tx, e Evidence) (string, error) {
	return recordEvidence(ctx, tx, e, false)
}

// RecordEvidenceGateway inserts an evidence record through audit.record_evidence (restricted pools).
func RecordEvidenceGateway(ctx context.Context, tx pgx.Tx, e Evidence) (string, error) {
	return recordEvidence(ctx, tx, e, true)
}

func recordEvidence(ctx context.Context, tx pgx.Tx, e Evidence, gateway bool) (string, error) {
	refs, err := e.prepare()
	if err != nil {
		return "", err
	}
	if e.CollectedAt.IsZero() {
		e.CollectedAt = time.Now().UTC()
	}
	id := ids.New()
	args := []any{id, e.Type, e.SubjectType, e.SubjectID, refs, e.CollectedAt, e.CollectedByType, nullUUID(e.CollectedByID),
		e.Source, nullStr(e.StorageRef), e.SHA256, nullSize(e.SizeBytes), nullStr(e.MediaType), e.Classification, e.RetentionClass,
		e.AuditEventID}
	if gateway {
		_, err = tx.Exec(ctx, `SELECT audit.record_evidence($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`, args...)
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO audit.evidence_records (id, evidence_type, subject_type, subject_id, related_refs, collected_at,
			collected_by_type, collected_by_id, source, storage_ref, content_sha256, size_bytes, media_type, classification,
			retention_class, audit_event_id) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`, args...)
	}
	return id, err
}

func nullSize(n int64) any {
	if n <= 0 {
		return nil
	}
	return n
}
