package kyc

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/verifcase"
	"github.com/Fatifizo/fundzim/internal/storage"
)

// Document subject types.
const (
	SubjectKYCCase           = "KYC_CASE"
	SubjectKYBCase           = "KYB_CASE"
	SubjectKYBPerson         = "KYB_PERSON"
	SubjectBeneficiary       = "BENEFICIARY"
	SubjectPayoutDestination = "PAYOUT_DESTINATION"
)

// Document is document metadata (never content). Status is the stored object's scan status.
type Document struct {
	ID             string     `json:"id"`
	SubjectType    string     `json:"subject_type"`
	SubjectID      string     `json:"subject_id"`
	DocumentType   string     `json:"document_type"`
	Side           string     `json:"side"`
	Status         string     `json:"status"`
	MediaType      string     `json:"media_type"`
	SizeBytes      int64      `json:"size_bytes"`
	UploadedAt     time.Time  `json:"uploaded_at"`
	RejectedReason *string    `json:"rejected_reason"`
	RemovedAt      *time.Time `json:"removed_at,omitempty"`
	ObjectID       string     `json:"-"`
	UploadedBy     string     `json:"-"`
	KYCCaseID      string     `json:"-"`
	KYBCaseID      string     `json:"-"`
}

// AttachInput links an uploaded PRIVATE_KYC object to a verification subject.
type AttachInput struct {
	SubjectType  string
	SubjectID    string
	DocumentType string
	Side         string
	Object       storage.Object
	UploadedBy   string
}

func validSide(s string) bool { return s == "FRONT" || s == "BACK" || s == "PHOTO_PAGE" || s == "NA" }

// AttachToOwnCase attaches a document to the caller's KYC case, or to a KYB case / KYB person of an
// organisation where the caller may manage verification (isOrgAdmin is decided by the caller of this module
// through organisations). The case row is locked, so a document can never be added once the case is under
// review (document replacement during review is impossible).
func (s *Service) AttachToOwnCase(ctx context.Context, in AttachInput, isOrgAdmin func(orgID string) (bool, error)) (Document, error) {
	if !IsDocumentType(in.DocumentType) || !validSide(in.Side) {
		return Document{}, errs.New(errs.Unprocessable, "VALIDATION_FAILED", "Unknown document type or side.")
	}
	var doc Document
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var cols []string
		var vals []any
		switch in.SubjectType {
		case SubjectKYCCase:
			c, err := s.ownCase(ctx, tx, in.UploadedBy, in.SubjectID)
			if err != nil {
				return err
			}
			if !verifcase.Editable(c.Status) {
				return ErrCaseNotEditable
			}
			cols, vals = []string{"profile_id", "kyc_case_id"}, []any{c.ProfileID, c.ID}
			doc.KYCCaseID = c.ID
		case SubjectKYBCase, SubjectKYBPerson:
			caseID := in.SubjectID
			var personID string
			if in.SubjectType == SubjectKYBPerson {
				personID = in.SubjectID
				if !ids.Valid(personID) {
					return ErrCaseNotFound
				}
				if err := tx.QueryRow(ctx, `SELECT c.id FROM kyc.organisation_persons p JOIN kyc.kyb_cases c ON c.id = p.kyb_case_id
					WHERE p.id = $1 AND p.valid_to IS NULL`, personID).Scan(&caseID); err != nil {
					if errors.Is(err, pgx.ErrNoRows) {
						return ErrCaseNotFound
					}
					return err
				}
			}
			k, err := s.loadKYB(ctx, tx, caseID, true)
			if err != nil {
				return err
			}
			ok, err := isOrgAdmin(k.OrganisationID)
			if err != nil {
				return err
			}
			if !ok {
				return ErrCaseNotFound
			}
			if !verifcase.Editable(k.Status) {
				return ErrCaseNotEditable
			}
			if personID != "" {
				cols, vals = []string{"organisation_person_id", "kyb_case_id"}, []any{personID, k.ID}
			} else {
				cols, vals = []string{"kyb_organisation_id", "kyb_case_id"}, []any{k.KYBOrgID, k.ID}
			}
			doc.KYBCaseID = k.ID
		default:
			return errs.New(errs.Unprocessable, "VALIDATION_FAILED", "Unknown subject type.")
		}
		var err error
		doc, err = s.insertDocument(ctx, tx, in, cols, vals)
		return err
	})
	return doc, err
}

// AttachExternal attaches a document to a beneficiary or payout destination. The owning module has already
// authorised the caller and holds a row lock on the subject while this runs (checked editable).
func (s *Service) AttachExternal(ctx context.Context, in AttachInput) (Document, error) {
	if !IsDocumentType(in.DocumentType) || !validSide(in.Side) {
		return Document{}, errs.New(errs.Unprocessable, "VALIDATION_FAILED", "Unknown document type or side.")
	}
	col := map[string]string{SubjectBeneficiary: "beneficiary_id", SubjectPayoutDestination: "destination_id"}[in.SubjectType]
	if col == "" || !ids.Valid(in.SubjectID) {
		return Document{}, errs.New(errs.Unprocessable, "VALIDATION_FAILED", "Unknown subject type.")
	}
	var doc Document
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		doc, err = s.insertDocument(ctx, tx, in, []string{col}, []any{in.SubjectID})
		return err
	})
	return doc, err
}

func (s *Service) insertDocument(ctx context.Context, tx pgx.Tx, in AttachInput, cols []string, vals []any) (Document, error) {
	if in.Object.BucketClass != storage.BucketPrivateKYC || in.Object.UploadedBy != in.UploadedBy {
		return Document{}, errors.New("kyc: document object must be a PRIVATE_KYC object uploaded by the caller")
	}
	id := ids.New()
	evID, err := audit.RecordGateway(ctx, tx, audit.Event{Action: "kyc.document.uploaded", TargetType: strings.ToLower(in.SubjectType),
		TargetID: in.SubjectID, Metadata: map[string]any{"document_id": id, "document_type": in.DocumentType, "object_id": in.Object.ID}})
	if err != nil {
		return Document{}, err
	}
	evidence, err := audit.RecordEvidenceGateway(ctx, tx, audit.Evidence{Type: "VERIFICATION_DOCUMENT", SubjectType: strings.ToLower(in.SubjectType),
		SubjectID: in.SubjectID, RelatedRefs: map[string]any{"document_id": id, "document_type": in.DocumentType}, CollectedByType: "user",
		CollectedByID: in.UploadedBy, Source: "upload", StorageRef: in.Object.ID, SHA256: in.Object.SHA256, SizeBytes: in.Object.SizeBytes,
		MediaType: in.Object.SniffedType, Classification: "C3", RetentionClass: "KYC", AuditEventID: evID, CollectedAt: s.now()})
	if err != nil {
		return Document{}, err
	}
	colList := "id, subject_type, document_type, side, stored_object_id, evidence_record_id, uploaded_by, recorded_at"
	placeholders := "$1, $2, $3, $4, $5, $6, $7, $8"
	args := []any{id, in.SubjectType, in.DocumentType, in.Side, in.Object.ID, evidence, in.UploadedBy, s.now()}
	for i, c := range cols {
		colList += ", " + c
		placeholders += ", $" + strconv.Itoa(len(args)+1)
		args = append(args, vals[i])
	}
	if _, err := tx.Exec(ctx, `INSERT INTO kyc.kyc_documents (`+colList+`) VALUES (`+placeholders+`)`, args...); err != nil {
		return Document{}, err
	}
	d := Document{ID: id, SubjectType: in.SubjectType, SubjectID: in.SubjectID, DocumentType: in.DocumentType, Side: in.Side,
		Status: in.Object.Status, MediaType: in.Object.SniffedType, SizeBytes: in.Object.SizeBytes, UploadedAt: s.now(), ObjectID: in.Object.ID,
		UploadedBy: in.UploadedBy}
	return d, nil
}

const docSelect = `SELECT d.id, d.subject_type, coalesce(d.kyc_case_id::text, d.kyb_case_id::text, d.beneficiary_id::text, d.destination_id::text),
	d.organisation_person_id, d.document_type, d.side, d.stored_object_id, d.uploaded_by, d.recorded_at, d.removed_at,
	coalesce(d.kyc_case_id::text, ''), coalesce(d.kyb_case_id::text, '')
	FROM kyc.kyc_documents d`

func (s *Service) scanDocs(ctx context.Context, rows pgx.Rows) ([]Document, error) {
	defer rows.Close()
	out := []Document{}
	for rows.Next() {
		var d Document
		var personID *string
		if err := rows.Scan(&d.ID, &d.SubjectType, &d.SubjectID, &personID, &d.DocumentType, &d.Side, &d.ObjectID, &d.UploadedBy,
			&d.UploadedAt, &d.RemovedAt, &d.KYCCaseID, &d.KYBCaseID); err != nil {
			return nil, err
		}
		if d.SubjectType == SubjectKYBPerson && personID != nil {
			d.SubjectID = *personID
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.fillObject(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Service) fillObject(ctx context.Context, d *Document) error {
	if s.Storage == nil {
		return nil
	}
	o, err := s.Storage.Get(ctx, d.ObjectID)
	if err != nil {
		return err
	}
	d.Status, d.MediaType, d.SizeBytes = o.Status, o.SniffedType, o.SizeBytes
	if o.RejectReason != "" {
		r := o.RejectReason
		d.RejectedReason = &r
	}
	if d.RemovedAt != nil {
		d.Status = storage.StatusDeleted
	}
	return nil
}

// DocumentsFor lists the non-removed documents of a subject column (kyc_case_id, kyb_case_id,
// organisation_person_id, beneficiary_id, destination_id).
func (s *Service) DocumentsFor(ctx context.Context, col, id string) ([]Document, error) {
	switch col {
	case "kyc_case_id", "kyb_case_id", "organisation_person_id", "beneficiary_id", "destination_id":
	default:
		return nil, errors.New("kyc: invalid document subject")
	}
	if col == "kyb_case_id" { // KYB case documents include those of its persons
		rows, err := s.Pool.Query(ctx, docSelect+` WHERE d.kyb_case_id = $1 AND d.removed_at IS NULL ORDER BY d.recorded_at`, id)
		if err != nil {
			return nil, err
		}
		return s.scanDocs(ctx, rows)
	}
	rows, err := s.Pool.Query(ctx, docSelect+` WHERE d.`+col+` = $1 AND d.removed_at IS NULL ORDER BY d.recorded_at`, id)
	if err != nil {
		return nil, err
	}
	return s.scanDocs(ctx, rows)
}

// GetDocument returns one document's metadata (authorisation is the caller's job).
func (s *Service) GetDocument(ctx context.Context, id string) (Document, error) {
	if !ids.Valid(id) {
		return Document{}, ErrDocumentNotFound
	}
	rows, err := s.Pool.Query(ctx, docSelect+` WHERE d.id = $1`, id)
	if err != nil {
		return Document{}, err
	}
	docs, err := s.scanDocs(ctx, rows)
	if err != nil {
		return Document{}, err
	}
	if len(docs) == 0 {
		return Document{}, ErrDocumentNotFound
	}
	return docs[0], nil
}

// RemoveDocument records removal of a document while its subject is editable (the row stays as history; the
// object is soft-deleted by the caller). checkEditable must lock and check the subject.
func (s *Service) RemoveDocument(ctx context.Context, docID, actorID string, checkEditable func(ctx context.Context, tx pgx.Tx, d Document) error) error {
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, docSelect+` WHERE d.id = $1 FOR UPDATE OF d`, docID)
		if err != nil {
			return err
		}
		var docs []Document
		for rows.Next() {
			var d Document
			var personID *string
			if err := rows.Scan(&d.ID, &d.SubjectType, &d.SubjectID, &personID, &d.DocumentType, &d.Side, &d.ObjectID, &d.UploadedBy,
				&d.UploadedAt, &d.RemovedAt, &d.KYCCaseID, &d.KYBCaseID); err != nil {
				rows.Close()
				return err
			}
			if d.SubjectType == SubjectKYBPerson && personID != nil {
				d.SubjectID = *personID
			}
			docs = append(docs, d)
		}
		rows.Close()
		if len(docs) == 0 || docs[0].RemovedAt != nil {
			return ErrDocumentNotFound
		}
		d := docs[0]
		if err := checkEditable(ctx, tx, d); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE kyc.kyc_documents SET removed_at = $2, removed_by = $3 WHERE id = $1`, d.ID, s.now(), actorID); err != nil {
			return err
		}
		_, err = audit.RecordGateway(ctx, tx, audit.Event{Action: "kyc.document.removed", TargetType: strings.ToLower(d.SubjectType),
			TargetID: d.SubjectID, Metadata: map[string]any{"document_id": d.ID}})
		return err
	})
}

// CaseEditableForOwner checks (with a lock) that a KYC or KYB document's case is editable by actorID.
func (s *Service) CaseEditableForOwner(actorID string, isOrgAdmin func(orgID string) (bool, error)) func(ctx context.Context, tx pgx.Tx, d Document) error {
	return func(ctx context.Context, tx pgx.Tx, d Document) error {
		switch {
		case d.KYCCaseID != "":
			c, err := s.ownCase(ctx, tx, actorID, d.KYCCaseID)
			if err != nil {
				return ErrDocumentNotFound
			}
			if !verifcase.Editable(c.Status) {
				return ErrCaseNotEditable
			}
		case d.KYBCaseID != "":
			k, err := s.loadKYB(ctx, tx, d.KYBCaseID, true)
			if err != nil {
				return ErrDocumentNotFound
			}
			if ok, err := isOrgAdmin(k.OrganisationID); err != nil || !ok {
				return ErrDocumentNotFound
			}
			if !verifcase.Editable(k.Status) {
				return ErrCaseNotEditable
			}
		default:
			return ErrDocumentNotFound
		}
		return nil
	}
}

// requirementsMet checks that every requirement has a CLEAN, non-removed document of an allowed type.
func (s *Service) requirementsMet(ctx context.Context, tx pgx.Tx, col, id string, reqs []Requirement) ([]errs.Detail, error) {
	rows, err := tx.Query(ctx, `SELECT document_type, stored_object_id FROM kyc.kyc_documents WHERE `+col+` = $1 AND removed_at IS NULL`, id)
	if err != nil {
		return nil, err
	}
	type dd struct{ typ, obj string }
	var docs []dd
	for rows.Next() {
		var d dd
		if err := rows.Scan(&d.typ, &d.obj); err != nil {
			rows.Close()
			return nil, err
		}
		docs = append(docs, d)
	}
	rows.Close()
	status := map[string]string{}
	for _, d := range docs {
		o, err := s.Storage.Get(ctx, d.obj)
		if err != nil {
			return nil, err
		}
		status[d.obj] = o.Status
	}
	var det []errs.Detail
	for _, req := range reqs {
		clean, present := false, false
		for _, d := range docs {
			for _, t := range req {
				if d.typ == t {
					present = true
					if status[d.obj] == storage.StatusClean {
						clean = true
					}
				}
			}
		}
		field := "documents." + strings.Join(req, "|")
		switch {
		case clean:
		case present:
			det = append(det, errs.Detail{Field: field, Code: "DOCUMENT_NOT_CLEAN"})
		default:
			det = append(det, errs.Detail{Field: field, Code: "DOCUMENT_REQUIRED"})
		}
	}
	return det, nil
}

// RequirementStatus describes one requirement for display.
type RequirementStatus struct {
	DocumentTypes []string `json:"document_types"`
	Satisfied     bool     `json:"satisfied"`
}

// Requirements reports the requirement status of a subject's documents.
func (s *Service) Requirements(ctx context.Context, col, id string, reqs []Requirement) ([]RequirementStatus, error) {
	var out []RequirementStatus
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		det, err := s.requirementsMet(ctx, tx, col, id, reqs)
		if err != nil {
			return err
		}
		missing := map[string]bool{}
		for _, d := range det {
			missing[d.Field] = true
		}
		for _, r := range reqs {
			out = append(out, RequirementStatus{DocumentTypes: r, Satisfied: !missing["documents."+strings.Join(r, "|")]})
		}
		return nil
	})
	if out == nil {
		out = []RequirementStatus{}
	}
	return out, err
}

// RequirementsCheck is requirementsMet for other modules (beneficiaries, payouts) in their own flows.
func (s *Service) RequirementsCheck(ctx context.Context, col, id string, reqs []Requirement) ([]errs.Detail, error) {
	var det []errs.Detail
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		det, err = s.requirementsMet(ctx, tx, col, id, reqs)
		return err
	})
	return det, err
}

// EvidenceIDs returns the evidence record ids of a subject's current documents (decisions reference them).
func (s *Service) EvidenceIDs(ctx context.Context, col, id string) ([]string, error) {
	switch col {
	case "beneficiary_id", "destination_id", "kyc_case_id", "kyb_case_id":
	default:
		return nil, errors.New("kyc: invalid subject")
	}
	rows, err := s.Pool.Query(ctx, `SELECT evidence_record_id FROM kyc.kyc_documents WHERE `+col+` = $1 AND removed_at IS NULL`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// RecordDocumentAccess writes the security audit event for a document view (contract §7.2; brief §15).
func (s *Service) RecordDocumentAccess(ctx context.Context, d Document, viewerKind string) error {
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := audit.RecordGateway(ctx, tx, audit.Event{Stream: audit.Security, Action: "kyc.document.accessed",
			TargetType: strings.ToLower(d.SubjectType), TargetID: d.SubjectID,
			Metadata: map[string]any{"document_id": d.ID, "document_type": d.DocumentType, "viewer": viewerKind}})
		return err
	})
}
