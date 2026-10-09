package storage

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/errs"
	pstorage "github.com/Fatifizo/fundzim/internal/platform/storage"
)

// Bucket classes (app.stored_objects.bucket_class).
const (
	BucketPublicMedia     = "PUBLIC_MEDIA"
	BucketPrivateKYC      = "PRIVATE_KYC"
	BucketPrivateEvidence = "PRIVATE_EVIDENCE"
)

// Scan statuses (ADR-034 §3). Keep in sync with migration 20261009150100 (machine stored_object_scan).
const (
	StatusUploaded    = "UPLOADED"
	StatusQuarantined = "QUARANTINED"
	StatusScanning    = "SCANNING"
	StatusClean       = "CLEAN"
	StatusRejected    = "REJECTED"
	StatusFailedScan  = "FAILED_SCAN"
	StatusDeleted     = "DELETED"
)

// Transitions is the Go copy of the stored_object_scan edge list in migration 20261009150100 ("" = insert).
var Transitions = map[string][]string{
	"":                {StatusUploaded},
	StatusUploaded:    {StatusQuarantined, StatusRejected, StatusDeleted},
	StatusQuarantined: {StatusScanning, StatusDeleted},
	StatusScanning:    {StatusClean, StatusRejected, StatusFailedScan, StatusDeleted},
	StatusFailedScan:  {StatusScanning, StatusDeleted},
	StatusClean:       {StatusDeleted},
	StatusRejected:    {StatusDeleted},
}

// Reject reasons (reject_reason codes).
const (
	RejectMalware      = "MALWARE_DETECTED"
	RejectEmpty        = "EMPTY_FILE"
	RejectTooLarge     = "FILE_TOO_LARGE"
	RejectUnsupported  = "UNSUPPORTED_FILE_TYPE"
	RejectTypeMismatch = "FILE_TYPE_MISMATCH"
	RejectMalformed    = "MALFORMED_UPLOAD"
)

// Scan error codes (last_scan_error).
const (
	ScanErrUnavailable = "SCANNER_UNAVAILABLE"
	ScanErrTimeout     = "SCANNER_TIMEOUT"
	ScanErrScanner     = "SCANNER_ERROR"
	ScanErrInterrupted = "SCAN_INTERRUPTED"
	ScanErrStorage     = "STORAGE_ERROR"
)

// Delete reasons (delete_reason).
const (
	DeleteOwner         = "OWNER_DELETED"
	DeleteUploadExpired = "UPLOAD_EXPIRED"
	DeleteUploadFailed  = "UPLOAD_FAILED"
	DeleteStaff         = "STAFF_DELETED"
)

// Media types on the allow-list.
const (
	TypeJPEG = "image/jpeg"
	TypePNG  = "image/png"
	TypePDF  = "application/pdf"
)

// Outbox event types.
const (
	EventObjectUploaded = "storage.object_uploaded"
	EventObjectScanned  = "storage.object_scanned"
)

type purposeInfo struct {
	bucket         string
	classification string
	retention      string
}

// purposes mirrors ck_stored_objects_purpose plus the classification/retention derived for each purpose.
var purposes = map[string]purposeInfo{
	"CAMPAIGN_MEDIA":               {BucketPublicMedia, "C1", "OPERATIONAL"},
	"CAMPAIGN_UPDATE_MEDIA":        {BucketPublicMedia, "C1", "OPERATIONAL"},
	"PROFILE_AVATAR":               {BucketPublicMedia, "C1", "OPERATIONAL"},
	"ORGANISATION_LOGO":            {BucketPublicMedia, "C1", "OPERATIONAL"},
	"KYC_DOCUMENT":                 {BucketPrivateKYC, "C3", "KYC"},
	"KYC_SELFIE":                   {BucketPrivateKYC, "C3", "KYC"},
	"KYB_DOCUMENT":                 {BucketPrivateKYC, "C3", "KYC"},
	"BENEFICIARY_EVIDENCE":         {BucketPrivateKYC, "C3", "KYC"},
	"PAYOUT_DESTINATION_EVIDENCE":  {BucketPrivateKYC, "C3", "KYC"},
	"CAMPAIGN_SUPPORTING_DOCUMENT": {BucketPrivateEvidence, "C2", "CASE"},
	"DISPUTE_EVIDENCE":             {BucketPrivateEvidence, "C3", "FINANCIAL"},
	"COMPLIANCE_EVIDENCE":          {BucketPrivateEvidence, "C3", "CASE"},
	"RECONCILIATION_REPORT":        {BucketPrivateEvidence, "C2", "FINANCIAL"},
}

var ownerModules = map[string]bool{"users": true, "organisations": true, "campaigns": true, "kyc": true, "beneficiaries": true,
	"compliance": true, "payments": true, "payouts": true, "reconciliation": true, "audit": true}

// allowedTypes per bucket class (mirrors ck_stored_objects_content_type).
var allowedTypes = map[string]map[string]bool{
	BucketPublicMedia:     {TypeJPEG: true, TypePNG: true},
	BucketPrivateKYC:      {TypeJPEG: true, TypePNG: true, TypePDF: true},
	BucketPrivateEvidence: {TypeJPEG: true, TypePNG: true, TypePDF: true},
}

func platformClass(bucket string) pstorage.Class {
	switch bucket {
	case BucketPublicMedia:
		return pstorage.PublicCampaignMedia
	case BucketPrivateKYC:
		return pstorage.PrivateIdentityDocuments
	case BucketPrivateEvidence:
		return pstorage.PrivateComplianceDocuments
	}
	return ""
}

// Object is a stored object's metadata (contract §3).
type Object struct {
	ID, BucketClass, Purpose, OwnerModule, UploadedBy string
	Status                                            string
	SniffedType                                       string
	SizeBytes                                         int64
	SHA256                                            []byte
	CreatedAt                                         time.Time
	ScannedAt                                         *time.Time
	RejectReason                                      string
}

// UploadInput describes one upload. Body is read to EOF (or until MaxBytes+1). MaxBytes ≤ 0 or above the
// configured UPLOAD_MAX_BYTES uses the configured maximum. There is deliberately no filename field.
type UploadInput struct {
	BucketClass, Purpose, OwnerModule, UploaderUserID, DeclaredContentType string
	Body                                                                   io.Reader
	MaxBytes                                                               int64
}

// Errors (errs kinds fix the HTTP status). Compare with errors.Is.
var (
	ErrTooLarge           = errs.New(errs.TooLarge, "FILE_TOO_LARGE", "The file is too large.")
	ErrUnsupportedType    = errs.New(errs.Unprocessable, "UNSUPPORTED_FILE_TYPE", "Only JPEG, PNG and PDF files are accepted.")
	ErrTypeMismatch       = errs.New(errs.Unprocessable, "FILE_TYPE_MISMATCH", "The file content does not match its declared type.")
	ErrEmptyFile          = errs.New(errs.Unprocessable, "EMPTY_FILE", "The file is empty.")
	ErrMalformedUpload    = errs.New(errs.Invalid, "MALFORMED_UPLOAD", "The upload is malformed.")
	ErrNotClean           = errs.New(errs.Conflict, "DOCUMENT_NOT_AVAILABLE", "The document is not available.")
	ErrNotFound           = errs.New(errs.NotFound, "DOCUMENT_NOT_FOUND", "Document not found.")
	ErrStorageUnavailable = errs.New(errs.Unavailable, "STORAGE_UNAVAILABLE", "Document storage is temporarily unavailable.")
	ErrTicketExpired      = errs.New(errs.Forbidden, "DOCUMENT_TICKET_EXPIRED", "The document link has expired.")
	ErrTicketInvalid      = errs.New(errs.Forbidden, "DOCUMENT_TICKET_INVALID", "The document link is not valid.")
	ErrInvalidInput       = errs.New(errs.Internal, "INTERNAL_ERROR", "An unexpected error occurred.") // programming errors
)

func invalidInput(format string, a ...any) error {
	return fmt.Errorf("%w: storage: "+format, append([]any{ErrInvalidInput}, a...)...)
}

// IsUploadRejection reports whether err is a content rejection (as opposed to an infrastructure failure).
func IsUploadRejection(err error) bool {
	for _, e := range []error{ErrTooLarge, ErrUnsupportedType, ErrTypeMismatch, ErrEmptyFile, ErrMalformedUpload} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}
