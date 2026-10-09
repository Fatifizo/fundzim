package verification

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/kyc"
	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/storage"
)

var purposeBySubject = map[string]string{kyc.SubjectKYCCase: "KYC_DOCUMENT", kyc.SubjectKYBCase: "KYB_DOCUMENT", kyc.SubjectKYBPerson: "KYB_DOCUMENT",
	kyc.SubjectBeneficiary: "BENEFICIARY_EVIDENCE", kyc.SubjectPayoutDestination: "PAYOUT_DESTINATION_EVIDENCE"}

func (s *Service) isOrgAdmin(ctx context.Context, userID string) func(string) (bool, error) {
	return func(orgID string) (bool, error) { return s.Orgs.IsAdmin(ctx, orgID, userID) }
}

// UploadDocument: POST /verification/documents (multipart: subject_type, subject_id, document_type, side?, file).
// The subject is authorised from the form fields BEFORE the file is read; the object is stored and queued for
// scanning, then attached under the subject's lock. If attaching fails the stored object is soft-deleted.
func (s *Service) UploadDocument(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principal(r)
	up, err := storage.ParseMultipart(w, r, storage.MultipartSpec{FileField: "file",
		Fields: []string{"subject_type", "subject_id", "document_type", "side"}, Required: []string{"subject_type", "subject_id", "document_type"},
		MaxFileBytes: s.MaxUpload})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	in := kyc.AttachInput{SubjectType: up.Fields["subject_type"], SubjectID: up.Fields["subject_id"], DocumentType: up.Fields["document_type"],
		Side: up.Fields["side"], UploadedBy: p.UserID}
	if in.Side == "" {
		in.Side = "NA"
	}
	purpose, ok := purposeBySubject[in.SubjectType]
	if !ok || !kyc.IsDocumentType(in.DocumentType) {
		s.fail(w, r, httpx.Validation(errs.Detail{Field: "subject_type", Code: "INVALID_VALUE"}))
		return
	}
	if in.DocumentType == "SELFIE" {
		purpose = "KYC_SELFIE"
	}
	// authorise before reading the file (cheap rejection, no storage write for strangers)
	if err := s.preAuthoriseSubject(ctx, p.UserID, in.SubjectType, in.SubjectID); err != nil {
		s.fail(w, r, err)
		return
	}
	obj, err := s.Storage.Upload(ctx, storage.UploadInput{BucketClass: storage.BucketPrivateKYC, Purpose: purpose, OwnerModule: "kyc",
		UploaderUserID: p.UserID, DeclaredContentType: up.DeclaredContentType, Body: up.File, MaxBytes: s.MaxUpload})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	in.Object = obj
	var doc kyc.Document
	switch in.SubjectType {
	case kyc.SubjectKYCCase, kyc.SubjectKYBCase, kyc.SubjectKYBPerson:
		doc, err = s.KYC.AttachToOwnCase(ctx, in, s.isOrgAdmin(ctx, p.UserID))
	case kyc.SubjectBeneficiary:
		err = s.Benefs.WithTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			if err := s.Benefs.LockEditable(ctx, tx, p.UserID, in.SubjectID); err != nil {
				return err
			}
			var e error
			doc, e = s.KYC.AttachExternal(ctx, in)
			return e
		})
	case kyc.SubjectPayoutDestination:
		err = s.Payouts.WithTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			if err := s.Payouts.LockEditable(ctx, tx, p.UserID, in.SubjectID); err != nil {
				return err
			}
			var e error
			doc, e = s.KYC.AttachExternal(ctx, in)
			return e
		})
	}
	if err != nil {
		_ = s.Storage.Delete(context.WithoutCancel(ctx), obj.ID, p.UserID)
		s.fail(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, http.StatusCreated, doc)
}

// preAuthoriseSubject checks the caller may add documents to the subject right now (re-checked under lock).
func (s *Service) preAuthoriseSubject(ctx context.Context, userID, subjectType, subjectID string) error {
	switch subjectType {
	case kyc.SubjectKYCCase:
		owner, err := s.KYC.CaseOwner(ctx, subjectID)
		if err != nil || owner != userID {
			return kyc.ErrCaseNotFound
		}
	case kyc.SubjectKYBCase:
		org, err := s.KYC.KYBCaseOrganisation(ctx, subjectID)
		if err != nil {
			return kyc.ErrCaseNotFound
		}
		if ok, err := s.Orgs.IsAdmin(ctx, org, userID); err != nil || !ok {
			return kyc.ErrCaseNotFound
		}
	case kyc.SubjectKYBPerson:
		return nil // resolved and authorised inside AttachToOwnCase
	case kyc.SubjectBeneficiary:
		if ok, err := s.Benefs.CanView(ctx, userID, subjectID); err != nil || !ok {
			return errs.New(errs.NotFound, "BENEFICIARY_NOT_FOUND", "No such beneficiary.")
		}
	case kyc.SubjectPayoutDestination:
		if ok, err := s.Payouts.CanView(ctx, userID, subjectID); err != nil || !ok {
			return errs.New(errs.NotFound, "DESTINATION_NOT_FOUND", "No such payout destination.")
		}
	}
	return nil
}

// ownerCanSee reports whether a (non-staff) user may see a document: they own its subject.
func (s *Service) ownerCanSee(ctx context.Context, userID string, d kyc.Document) (bool, error) {
	switch {
	case d.KYCCaseID != "":
		owner, err := s.KYC.CaseOwner(ctx, d.KYCCaseID)
		return err == nil && owner == userID, nil
	case d.KYBCaseID != "":
		org, err := s.KYC.KYBCaseOrganisation(ctx, d.KYBCaseID)
		if err != nil {
			return false, nil
		}
		return s.Orgs.IsAdmin(ctx, org, userID)
	case d.SubjectType == kyc.SubjectBeneficiary:
		return s.Benefs.CanView(ctx, userID, d.SubjectID)
	case d.SubjectType == kyc.SubjectPayoutDestination:
		return s.Payouts.CanView(ctx, userID, d.SubjectID)
	}
	return false, nil
}

// reviewerCanSee: staff with kyc.document.view and a fresh step-up, assigned to the subject's open review
// (support, admin and super-admin roles do not hold kyc.document.view).
func (s *Service) reviewerCanSee(ctx context.Context, p *authz.Principal, d kyc.Document) error {
	if !p.Has("kyc.document.view") {
		return errs.New(errs.Forbidden, "PERMISSION_DENIED", "You do not have permission to view identity documents.")
	}
	if !p.StepUpFresh(s.Clock.Now(), s.StepUpMaxAge) {
		return errs.New(errs.Forbidden, "STEP_UP_REQUIRED", "Confirm it's you to continue.")
	}
	var assigned *string
	switch {
	case d.KYCCaseID != "":
		c, err := s.KYC.ReviewAssignment(ctx, kyc.KindKYC, d.KYCCaseID)
		if err != nil {
			return err
		}
		assigned = c
	case d.KYBCaseID != "":
		c, err := s.KYC.ReviewAssignment(ctx, kyc.KindKYB, d.KYBCaseID)
		if err != nil {
			return err
		}
		assigned = c
	case d.SubjectType == kyc.SubjectBeneficiary:
		b, err := s.Benefs.View(ctx, d.SubjectID)
		if err != nil {
			return err
		}
		assigned = b.AssignedTo()
	case d.SubjectType == kyc.SubjectPayoutDestination:
		dst, err := s.Payouts.View(ctx, d.SubjectID)
		if err != nil {
			return err
		}
		assigned = dst.AssignedTo()
	}
	if assigned == nil || *assigned != p.UserID {
		return errs.New(errs.Forbidden, "NOT_ASSIGNED", "Only the assigned reviewer can view this case's documents.")
	}
	return nil
}

func (s *Service) authoriseDocument(r *http.Request, d kyc.Document) (string, error) {
	p := principal(r)
	if p.Kind == authz.KindStaff {
		if err := s.reviewerCanSee(r.Context(), p, d); err != nil {
			return "", err
		}
		return "reviewer", nil
	}
	ok, err := s.ownerCanSee(r.Context(), p.UserID, d)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", kyc.ErrDocumentNotFound
	}
	return "owner", nil
}

// ListDocuments: GET /verification/documents?subject_type=&subject_id=
func (s *Service) ListDocuments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principal(r)
	st, sid := r.URL.Query().Get("subject_type"), r.URL.Query().Get("subject_id")
	col := map[string]string{kyc.SubjectKYCCase: "kyc_case_id", kyc.SubjectKYBCase: "kyb_case_id", kyc.SubjectBeneficiary: "beneficiary_id",
		kyc.SubjectPayoutDestination: "destination_id"}[st]
	if col == "" {
		s.fail(w, r, httpx.Validation(errs.Detail{Field: "subject_type", Code: "INVALID_VALUE"}))
		return
	}
	if p.Kind == authz.KindStaff {
		s.fail(w, r, errs.New(errs.NotFound, "ROUTE_NOT_FOUND", "Use the reviewer case view."))
		return
	}
	if err := s.preAuthoriseSubject(ctx, p.UserID, st, sid); err != nil {
		s.fail(w, r, err)
		return
	}
	docs, err := s.KYC.DocumentsFor(ctx, col, sid)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, http.StatusOK, docs)
}

// GetDocument: GET /verification/documents/{document_id}
func (s *Service) GetDocument(w http.ResponseWriter, r *http.Request) {
	d, err := s.KYC.GetDocument(r.Context(), r.PathValue("document_id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err := s.authoriseDocument(r, d); err != nil {
		s.fail(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, http.StatusOK, d)
}

// DocumentAccess: POST /verification/documents/{document_id}/access → {url, expires_at} (60 s, session-bound).
func (s *Service) DocumentAccess(w http.ResponseWriter, r *http.Request) {
	d, err := s.KYC.GetDocument(r.Context(), r.PathValue("document_id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err := s.authoriseDocument(r, d); err != nil {
		s.fail(w, r, err)
		return
	}
	if d.Status != storage.StatusClean || d.RemovedAt != nil {
		s.fail(w, r, storage.ErrNotClean)
		return
	}
	p := principal(r)
	ticket, exp := s.Storage.IssueTicket(d.ObjectID, p.UserID, p.SessionID, s.TicketTTL)
	noStore(w)
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"url": "/api/v1/verification/documents/" + d.ID + "/content?ticket=" + url.QueryEscape(ticket),
		"expires_at": exp})
}

// DocumentContent: GET /verification/documents/{document_id}/content?ticket= — re-authorises, verifies the
// ticket (bound to this session), audits, and streams the bytes as an attachment.
func (s *Service) DocumentContent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d, err := s.KYC.GetDocument(ctx, r.PathValue("document_id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	viewer, err := s.authoriseDocument(r, d)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := principal(r)
	if err := s.Storage.VerifyTicket(r.URL.Query().Get("ticket"), d.ObjectID, p.UserID, p.SessionID); err != nil {
		s.fail(w, r, err)
		return
	}
	if d.RemovedAt != nil {
		s.fail(w, r, kyc.ErrDocumentNotFound)
		return
	}
	rc, obj, err := s.Storage.Open(ctx, d.ObjectID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer rc.Close()
	if err := s.KYC.RecordDocumentAccess(ctx, d, viewer); err != nil {
		s.fail(w, r, err)
		return
	}
	ext := map[string]string{storage.TypePDF: ".pdf", storage.TypePNG: ".png", storage.TypeJPEG: ".jpg"}[obj.SniffedType]
	h := w.Header()
	h.Set("Content-Type", obj.SniffedType)
	h.Set("Content-Length", strconv.FormatInt(obj.SizeBytes, 10))
	h.Set("Content-Disposition", `attachment; filename="document`+ext+`"`)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	h.Set("Cache-Control", "private, no-store")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, rc); err != nil && !errors.Is(err, context.Canceled) {
		s.Logger.Warn("document stream interrupted", "error_category", "io")
	}
}

// DeleteDocument: DELETE /verification/documents/{document_id} (owner, editable subject only).
func (s *Service) DeleteDocument(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principal(r)
	d, err := s.KYC.GetDocument(ctx, r.PathValue("document_id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if p.Kind == authz.KindStaff {
		s.fail(w, r, kyc.ErrDocumentNotFound)
		return
	}
	if ok, err := s.ownerCanSee(ctx, p.UserID, d); err != nil || !ok {
		s.fail(w, r, kyc.ErrDocumentNotFound)
		return
	}
	check := func(ctx context.Context, tx pgx.Tx, doc kyc.Document) error {
		switch doc.SubjectType {
		case kyc.SubjectBeneficiary:
			return s.Benefs.WithTx(ctx, func(ctx context.Context, btx pgx.Tx) error {
				return s.Benefs.LockEditable(ctx, btx, p.UserID, doc.SubjectID)
			})
		case kyc.SubjectPayoutDestination:
			return s.Payouts.WithTx(ctx, func(ctx context.Context, ptx pgx.Tx) error {
				return s.Payouts.LockEditable(ctx, ptx, p.UserID, doc.SubjectID)
			})
		}
		return s.KYC.CaseEditableForOwner(p.UserID, s.isOrgAdmin(ctx, p.UserID))(ctx, tx, doc)
	}
	if err := s.KYC.RemoveDocument(ctx, d.ID, p.UserID, check); err != nil {
		s.fail(w, r, err)
		return
	}
	_ = s.Storage.Delete(context.WithoutCancel(ctx), d.ObjectID, p.UserID)
	w.WriteHeader(http.StatusNoContent)
}
