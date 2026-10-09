// Package media is the campaign media part of the campaigns module (Stage 6 work stream M; interface-contracts
// §1, §4, §6; ADR-036). It owns app.campaign_media and app.campaign_media_events and builds on the campaigns core
// access API (OwnerAccess, Load, PublicBySlug, Restricted); the core reaches it only through campaigns.MediaInfo.
//
// Pipeline (see migrations/20261009171100_campaign_media.sql):
//
//	Upload (API)  : multipart fields first (kind, position?, alt_text, depicts_minor), then the file. The campaign
//	                is authorised (owner write access, editable status, no SUSPENDED/OFFBOARDED restriction) BEFORE
//	                the file is read. The bytes go through storage.Upload into the PUBLIC_MEDIA quarantine (size cap,
//	                magic-byte sniffing, declared type = sniffed type); the client filename is never read or stored.
//	                The media row is created UPLOADED → QUARANTINED in one transaction with its events, audit
//	                campaign.media_added and outbox campaigns.media_added.
//	Scan (worker) : the storage pipeline scans the original. On storage.object_scanned the media consumer advances
//	                the media: a REJECTED original rejects it; a CLEAN original is processed (Process: dimension
//	                limits from the header BEFORE decoding, stdlib decode, re-encode without any metadata) and the
//	                derivative is uploaded as a NEW PUBLIC_MEDIA object, which goes through quarantine and the scan
//	                too (QUARANTINED → SCANNING). When the derivative is CLEAN the media becomes APPROVED.
//	                A scanner outage leaves the objects FAILED_SCAN (retried by the storage rescan job) and the media
//	                SCANNING: nothing is ever approved without a CLEAN verdict on both objects.
//	Serve         : only the derivative of APPROVED media, and publicly only when the media id is in the campaign's
//	                approved version (campaign_versions.media_ids of campaigns.approved_version_id) while the
//	                campaign is publicly exposed.
package media

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/campaigns"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/storage"
)

// Kinds.
const (
	KindCover   = "COVER"
	KindGallery = "GALLERY"
)

// Statuses (machine campaign_media). Keep in sync with migration 20261009171100.
const (
	StatusUploaded    = "UPLOADED"
	StatusQuarantined = "QUARANTINED"
	StatusScanning    = "SCANNING"
	StatusApproved    = "APPROVED"
	StatusRejected    = "REJECTED"
	StatusRemoved     = "REMOVED"
)

// Transitions is the Go copy of the campaign_media edge list in migration 20261009171100 ("" = insert).
var Transitions = map[string][]string{
	"":                {StatusUploaded},
	StatusUploaded:    {StatusQuarantined, StatusRejected, StatusRemoved},
	StatusQuarantined: {StatusScanning, StatusRejected, StatusRemoved},
	StatusScanning:    {StatusApproved, StatusRejected, StatusRemoved},
	StatusApproved:    {StatusRemoved},
	StatusRejected:    {StatusRemoved},
}

// Can reports whether from → to is an edge.
func Can(from, to string) bool {
	for _, t := range Transitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

// Rejection reason codes (ck_campaign_media_rejected_reason).
const (
	ReasonMalware            = "MALWARE_DETECTED"
	ReasonUploadRejected     = "UPLOAD_REJECTED"
	ReasonTypeMismatch       = "IMAGE_TYPE_MISMATCH"
	ReasonUnsupported        = "IMAGE_UNSUPPORTED"
	ReasonDecodeFailed       = "IMAGE_DECODE_FAILED"
	ReasonTooLarge           = "IMAGE_TOO_LARGE"
	ReasonActiveContent      = "IMAGE_ACTIVE_CONTENT"
	ReasonTrailingData       = "IMAGE_TRAILING_DATA"
	ReasonDerivativeTooLarge = "DERIVATIVE_TOO_LARGE"
	ReasonDerivativeRejected = "DERIVATIVE_REJECTED"
	ReasonObjectDeleted      = "OBJECT_DELETED"
	RemovedByOwner           = "OWNER_REMOVED"
	PurposeCampaignMedia     = "CAMPAIGN_MEDIA"
	OwnerModule              = "campaigns"
	materialChangeKindMedia  = "MEDIA"
	EventMediaAdded          = "campaigns.media_added"
	EventMediaRemoved        = "campaigns.media_removed"
	AuditMediaAdded          = "campaign.media_added"
	AuditMediaRemoved        = "campaign.media_removed"
	AuditMediaApproved       = "campaign.media_approved"
	AuditMediaRejected       = "campaign.media_rejected"
	AuditMediaUpdated        = "campaign.media_updated"
	PermView                 = "campaign.view"
	PermModerate             = "content.moderate"
	defaultMaxGallery        = 10
)

// Errors (stable codes).
var (
	ErrNotFound         = errs.New(errs.NotFound, "MEDIA_NOT_FOUND", "No such media.")
	ErrMinorMedia       = errs.New(errs.Unprocessable, "MINOR_MEDIA_NOT_SUPPORTED", "Images that show a child cannot be added to a campaign.")
	ErrRestricted       = errs.New(errs.Forbidden, "ACCOUNT_RESTRICTED", "This action is not available on your account.")
	ErrCoverExists      = errs.New(errs.Conflict, "COVER_ALREADY_EXISTS", "The campaign already has a cover image. Remove it first.")
	ErrGalleryFull      = errs.New(errs.Unprocessable, "GALLERY_LIMIT_REACHED", "The campaign has the maximum number of gallery images.")
	ErrVersionRequired  = errs.New(errs.Invalid, "PRECONDITION_REQUIRED", "Send If-Match with the media version.")
	ErrStateChanged     = errs.New(errs.Conflict, "MEDIA_STATE_CHANGED", "The media changed in the meantime. Reload and try again.")
	ErrInvalidStatus    = errs.New(errs.Conflict, "INVALID_STATUS", "This action is not allowed in the media's current status.")
	ErrNotAvailable     = errs.New(errs.NotFound, "MEDIA_NOT_AVAILABLE", "This image is not available.")
	ErrUnavailable      = errs.New(errs.Unavailable, "SERVICE_UNAVAILABLE", "The service is temporarily unavailable. Please retry shortly.")
	ErrNotConfigured    = errs.New(errs.Unavailable, "SERVICE_UNAVAILABLE", "Campaign media is not available in this process.")
	ErrMaterialRequired = errs.New(errs.Unavailable, "SERVICE_UNAVAILABLE", "Media changes to a live campaign are temporarily unavailable.")
)

// Core is the campaigns core API used here (*campaigns.Service implements it).
type Core interface {
	OwnerAccess(ctx context.Context, userID, campaignID string, write bool) (campaigns.Access, error)
	Load(ctx context.Context, campaignID string) (campaigns.Access, error)
	PublicBySlug(ctx context.Context, slug string) (campaigns.Access, error)
	Restricted(ctx context.Context, a campaigns.Access) (string, error)
	ActivePolicy(ctx context.Context) (campaigns.Policy, error)
}

// MaterialChangeRecorder is the core hook for changes to a live (ACTIVE/PAUSED) campaign (interface-contracts §4).
type MaterialChangeRecorder interface {
	RecordMaterialChange(ctx context.Context, campaignID, kind, actorID string) error
}

// ObjectStore is the subset of *storage.Service used here.
type ObjectStore interface {
	Upload(ctx context.Context, in storage.UploadInput) (storage.Object, error)
	Get(ctx context.Context, id string) (storage.Object, error)
	Open(ctx context.Context, id string) (io.ReadCloser, storage.Object, error)
	Delete(ctx context.Context, id, actorID string) error
}

// Deps are the collaborators. Core and MaterialChanges are nil in the worker (processing needs neither).
type Deps struct {
	Pool            *pgxpool.Pool // fundzim_app (API) or fundzim_worker (worker)
	Core            Core
	MaterialChanges MaterialChangeRecorder
	Storage         ObjectStore
	Clock           clock.Clock
	Logger          *slog.Logger
	MaxUploadBytes  int64         // UPLOAD_MAX_BYTES
	Limits          Limits        // decode limits (zero = DefaultLimits)
	PublicMaxAge    time.Duration // public Cache-Control max-age (default 5 min)
}

// Service is the campaign media service.
type Service struct {
	d      Deps
	decode chan struct{} // one decode at a time per process (bounded memory)
}

// New builds the service.
func New(d Deps) *Service {
	if d.Clock == nil {
		d.Clock = clock.System
	}
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	if d.Limits == (Limits{}) {
		d.Limits = DefaultLimits
	}
	if d.MaxUploadBytes <= 0 {
		d.MaxUploadBytes = 10 << 20
	}
	if d.PublicMaxAge <= 0 {
		d.PublicMaxAge = 5 * time.Minute
	}
	return &Service{d: d, decode: make(chan struct{}, 1)}
}

func (s *Service) now() time.Time { return s.d.Clock.Now().UTC() }

// Media is the owner/staff view of a media item. It never carries a filename or object keys.
type Media struct {
	ID             string     `json:"id"`
	CampaignID     string     `json:"campaign_id"`
	Kind           string     `json:"kind"`
	Position       int        `json:"position"`
	AltText        string     `json:"alt_text"`
	Status         string     `json:"status"`
	RejectedReason *string    `json:"rejected_reason,omitempty"`
	ContentType    *string    `json:"content_type,omitempty"`
	Width          *int       `json:"width,omitempty"`
	Height         *int       `json:"height,omitempty"`
	ApprovedAt     *time.Time `json:"approved_at,omitempty"`
	RemovedAt      *time.Time `json:"removed_at,omitempty"`
	RemovedReason  *string    `json:"removed_reason,omitempty"`
	Version        int        `json:"version"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	ContentURL     string     `json:"content_url,omitempty"`

	storedObjectID    string
	processedObjectID *string
	createdBy         string
}

// Ensure the core interface is satisfied by the campaigns service at compile time.
var _ Core = (*campaigns.Service)(nil)

// Ensure Service implements campaigns.MediaInfo.
var _ campaigns.MediaInfo = (*Service)(nil)
