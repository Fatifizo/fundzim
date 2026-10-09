package media

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/campaigns"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/storage"
)

// UploadInput is one owner upload. Body is read only after every check on the campaign has passed. There is no
// filename field: the client filename is never read or stored.
type UploadInput struct {
	CampaignID, UserID  string
	Kind, AltText       string
	Position            *int
	DepictsMinor        bool
	DeclaredContentType string
	Body                io.Reader
}

func editable(status string) bool {
	return status == campaigns.StatusDraft || status == campaigns.StatusChangesRequested
}

func live(status string) bool {
	return status == campaigns.StatusActive || status == campaigns.StatusPaused
}

// validateAlt applies the content rules (plain text, no HTML, no control characters, 3–250 characters).
func validateAlt(v string) (string, error) {
	clean, code := campaigns.CleanText(v, 3, 250, false)
	if code != "" {
		return "", httpx.Validation(errs.Detail{Field: "alt_text", Code: code})
	}
	return clean, nil
}

func validatePosition(kind string, p *int) error {
	if p == nil {
		return nil
	}
	if *p < 0 || *p > 1000 || (kind == KindCover && *p != 0) {
		return httpx.Validation(errs.Detail{Field: "position", Code: "INVALID_VALUE"})
	}
	return nil
}

// authoriseWrite checks owner write access, the campaign status and compliance restrictions (fail closed). It
// returns the access and whether the campaign is live (a change then is a material change).
func (s *Service) authoriseWrite(ctx context.Context, userID, campaignID string) (campaigns.Access, bool, error) {
	if s.d.Core == nil {
		return campaigns.Access{}, false, ErrNotConfigured
	}
	a, err := s.d.Core.OwnerAccess(ctx, userID, campaignID, true)
	if err != nil {
		return a, false, err
	}
	isLive := live(a.Status)
	if !editable(a.Status) && !isLive {
		return a, false, campaigns.ErrInvalidStatus
	}
	lvl, err := s.d.Core.Restricted(ctx, a)
	if err != nil {
		return a, false, errors.Join(ErrUnavailable, err)
	}
	if lvl == campaigns.RestrictionSuspended || lvl == campaigns.RestrictionOffboarded {
		return a, false, ErrRestricted
	}
	if isLive && s.d.MaterialChanges == nil {
		return a, false, ErrMaterialRequired
	}
	return a, isLive, nil
}

func (s *Service) maxGallery(ctx context.Context) (int, error) {
	pol, err := s.d.Core.ActivePolicy(ctx)
	if err != nil {
		return 0, errors.Join(ErrUnavailable, err)
	}
	if pol.Rules.MaxGalleryImages > 0 {
		return pol.Rules.MaxGalleryImages, nil
	}
	return defaultMaxGallery, nil
}

// slotCheck refuses a second cover or a full gallery (q is the pool before the upload, the tx under the campaign
// lock after it). Rejected gallery images do not count; a rejected cover still does until it is removed.
func (s *Service) slotCheck(ctx context.Context, q querier, campaignID, kind string, maxGallery int) error {
	var covers, gallery int
	if err := q.QueryRow(ctx, `SELECT count(*) FILTER (WHERE kind = 'COVER'),
		  count(*) FILTER (WHERE kind = 'GALLERY' AND status <> 'REJECTED')
		FROM app.campaign_media WHERE campaign_id = $1 AND status <> 'REMOVED'`, campaignID).Scan(&covers, &gallery); err != nil {
		return err
	}
	if kind == KindCover && covers > 0 {
		return ErrCoverExists
	}
	if kind == KindGallery && gallery >= maxGallery {
		return ErrGalleryFull
	}
	return nil
}

// Upload stores an owner upload in quarantine and records the media (UPLOADED → QUARANTINED). Processing happens in
// the worker after the scan.
func (s *Service) Upload(ctx context.Context, in UploadInput) (Media, error) {
	if in.Kind != KindCover && in.Kind != KindGallery {
		return Media{}, httpx.Validation(errs.Detail{Field: "kind", Code: "INVALID_VALUE"})
	}
	if in.DepictsMinor {
		return Media{}, ErrMinorMedia // LR-070: no images of minors until the legal basis is confirmed
	}
	alt, err := validateAlt(in.AltText)
	if err != nil {
		return Media{}, err
	}
	if err := validatePosition(in.Kind, in.Position); err != nil {
		return Media{}, err
	}
	if !ids.Valid(in.CampaignID) {
		return Media{}, campaigns.ErrNotFound
	}
	_, isLive, err := s.authoriseWrite(ctx, in.UserID, in.CampaignID)
	if err != nil {
		return Media{}, err
	}
	maxGallery, err := s.maxGallery(ctx)
	if err != nil {
		return Media{}, err
	}
	if err := s.slotCheck(ctx, s.d.Pool, in.CampaignID, in.Kind, maxGallery); err != nil {
		return Media{}, err
	}

	obj, err := s.d.Storage.Upload(ctx, storage.UploadInput{BucketClass: storage.BucketPublicMedia, Purpose: PurposeCampaignMedia,
		OwnerModule: OwnerModule, UploaderUserID: in.UserID, DeclaredContentType: in.DeclaredContentType, Body: in.Body,
		MaxBytes: s.d.MaxUploadBytes})
	if err != nil {
		return Media{}, err
	}
	discard := func() {
		if derr := s.d.Storage.Delete(context.WithoutCancel(ctx), obj.ID, in.UserID); derr != nil {
			s.d.Logger.Warn("campaign media: stored object not discarded", slog.String("object_id", obj.ID), slog.String("error", derr.Error()))
		}
	}
	if isLive {
		// conservative: flag re-review before the media exists (a spurious flag is harmless; a missing one is not)
		if err := s.d.MaterialChanges.RecordMaterialChange(ctx, in.CampaignID, materialChangeKindMedia, in.UserID); err != nil {
			discard()
			return Media{}, err
		}
	}

	a := actor{Type: "USER", ID: in.UserID}
	var m Media
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		st, err := lockCampaignStatus(ctx, tx, in.CampaignID)
		if err != nil {
			return err
		}
		if !editable(st) && !(live(st) && isLive) {
			return campaigns.ErrInvalidStatus // the campaign moved on while the file was uploading
		}
		if err := s.slotCheck(ctx, tx, in.CampaignID, in.Kind, maxGallery); err != nil {
			return err
		}
		pos := 0
		if in.Position != nil {
			pos = *in.Position
		} else if in.Kind == KindGallery {
			if err := tx.QueryRow(ctx, `SELECT coalesce(max(position) + 1, 1) FROM app.campaign_media
				 WHERE campaign_id = $1 AND kind = 'GALLERY' AND status <> 'REMOVED'`, in.CampaignID).Scan(&pos); err != nil {
				return err
			}
			if pos > 1000 {
				pos = 1000
			}
		}
		id := ids.New()
		now := s.now()
		if _, err := tx.Exec(ctx, `INSERT INTO app.campaign_media (id, campaign_id, stored_object_id, stored_object_bucket, kind, position,
			  alt_text, status, created_by, created_at, updated_at) VALUES ($1, $2, $3, 'PUBLIC_MEDIA', $4, $5, $6, 'UPLOADED', $7, $8, $8)`,
			id, in.CampaignID, obj.ID, in.Kind, pos, alt, in.UserID, now); err != nil {
			if isUniqueViolation(err, "uq_campaign_media_one_cover") {
				return ErrCoverExists
			}
			return err
		}
		if err := s.event(ctx, tx, id, 1, "UPLOADED", nil, StatusUploaded, a, "", "", map[string]any{"object_id": obj.ID}); err != nil {
			return err
		}
		if m, err = s.load(ctx, tx, in.CampaignID, id, true); err != nil {
			return err
		}
		// the bytes are already in the quarantine of the public-media bucket, waiting for the malware scan
		if err := s.apply(ctx, tx, &m, a, step{to: StatusQuarantined, event: "QUARANTINED"}); err != nil {
			return err
		}
		extra := map[string]any{"live_campaign": isLive}
		if err := s.audit(ctx, tx, a, AuditMediaAdded, m, "", "", extra); err != nil {
			return err
		}
		return s.emit(ctx, tx, m, EventMediaAdded, nil)
	})
	if err != nil {
		discard()
		return Media{}, err
	}
	return m, nil
}

// List returns every media item of a campaign the user may read (owner or organisation member), all statuses.
func (s *Service) List(ctx context.Context, userID, campaignID string) ([]Media, error) {
	if s.d.Core == nil {
		return nil, ErrNotConfigured
	}
	if _, err := s.d.Core.OwnerAccess(ctx, userID, campaignID, false); err != nil {
		return nil, err
	}
	return s.listFor(ctx, campaignID)
}

// StaffList returns every media item of any campaign (the route requires campaign.view).
func (s *Service) StaffList(ctx context.Context, campaignID string) ([]Media, error) {
	if s.d.Core == nil {
		return nil, ErrNotConfigured
	}
	if _, err := s.d.Core.Load(ctx, campaignID); err != nil {
		return nil, err
	}
	return s.listFor(ctx, campaignID)
}

// UpdateInput changes the order and/or the alt text. IfVersion is the media version the client saw (required).
type UpdateInput struct {
	CampaignID, MediaID, UserID string
	Position                    *int
	AltText                     *string
	IfVersion                   int
}

// Update changes position and alt text of non-removed, non-rejected media (optimistic concurrency).
func (s *Service) Update(ctx context.Context, in UpdateInput) (Media, error) {
	if in.IfVersion < 1 {
		return Media{}, ErrVersionRequired
	}
	if in.Position == nil && in.AltText == nil {
		return Media{}, httpx.Validation(errs.Detail{Field: "body", Code: "NOTHING_TO_UPDATE"})
	}
	_, isLive, err := s.authoriseWrite(ctx, in.UserID, in.CampaignID)
	if err != nil {
		return Media{}, err
	}
	cur, err := s.load(ctx, s.d.Pool, in.CampaignID, in.MediaID, false)
	if err != nil {
		return Media{}, err
	}
	set := map[string]any{}
	if in.AltText != nil {
		alt, err := validateAlt(*in.AltText)
		if err != nil {
			return Media{}, err
		}
		set["alt_text"] = alt
	}
	if in.Position != nil {
		if err := validatePosition(cur.Kind, in.Position); err != nil {
			return Media{}, err
		}
		set["position"] = *in.Position
	}
	if cur.Status == StatusRemoved || cur.Status == StatusRejected {
		return Media{}, ErrInvalidStatus
	}
	if cur.Version != in.IfVersion {
		return Media{}, ErrStateChanged
	}
	if isLive && cur.Status == StatusApproved {
		if err := s.d.MaterialChanges.RecordMaterialChange(ctx, in.CampaignID, materialChangeKindMedia, in.UserID); err != nil {
			return Media{}, err
		}
	}
	a := actor{Type: "USER", ID: in.UserID}
	var m Media
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		st, err := lockCampaignStatus(ctx, tx, in.CampaignID)
		if err != nil {
			return err
		}
		if !editable(st) && !(live(st) && isLive) {
			return campaigns.ErrInvalidStatus
		}
		if m, err = s.load(ctx, tx, in.CampaignID, in.MediaID, true); err != nil {
			return err
		}
		if m.Version != in.IfVersion {
			return ErrStateChanged
		}
		if m.Status == StatusRemoved || m.Status == StatusRejected {
			return ErrInvalidStatus
		}
		from := m.Status
		if err := s.apply(ctx, tx, &m, a, step{event: "UPDATED", set: set}); err != nil {
			return err
		}
		return s.audit(ctx, tx, a, AuditMediaUpdated, m, from, "", map[string]any{"live_campaign": isLive})
	})
	return m, err
}

// Remove is the owner delete: the media becomes REMOVED (soft; the row and its timeline stay) and its objects are
// soft-deleted in storage, so nothing of it can be served again.
func (s *Service) Remove(ctx context.Context, userID, campaignID, mediaID string) error {
	_, isLive, err := s.authoriseWrite(ctx, userID, campaignID)
	if err != nil {
		return err
	}
	cur, err := s.load(ctx, s.d.Pool, campaignID, mediaID, false)
	if err != nil {
		return err
	}
	if cur.Status == StatusRemoved {
		return ErrInvalidStatus
	}
	if isLive && cur.Status == StatusApproved {
		if err := s.d.MaterialChanges.RecordMaterialChange(ctx, campaignID, materialChangeKindMedia, userID); err != nil {
			return err
		}
	}
	a := actor{Type: "USER", ID: userID}
	var m Media
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		st, err := lockCampaignStatus(ctx, tx, campaignID)
		if err != nil {
			return err
		}
		if !editable(st) && !(live(st) && isLive) {
			return campaigns.ErrInvalidStatus
		}
		if m, err = s.load(ctx, tx, campaignID, mediaID, true); err != nil {
			return err
		}
		return s.remove(ctx, tx, &m, a, RemovedByOwner, "")
	})
	if err != nil {
		return err
	}
	for _, id := range []*string{&m.storedObjectID, m.processedObjectID} {
		if id == nil {
			continue
		}
		if derr := s.d.Storage.Delete(context.WithoutCancel(ctx), *id, userID); derr != nil {
			s.d.Logger.Warn("campaign media: object not soft-deleted after removal", slog.String("media_id", m.ID),
				slog.String("error", derr.Error()))
		}
	}
	return nil
}

func (s *Service) remove(ctx context.Context, tx pgx.Tx, m *Media, a actor, reason, note string) error {
	if m.Status == StatusRemoved {
		return ErrInvalidStatus
	}
	from := m.Status
	if err := s.apply(ctx, tx, m, a, step{to: StatusRemoved, event: "REMOVED", reason: reason, note: note,
		set: map[string]any{"removed_by": a.ID, "removed_at": s.now(), "removed_reason": reason}}); err != nil {
		return err
	}
	if err := s.audit(ctx, tx, a, AuditMediaRemoved, *m, from, reason, nil); err != nil {
		return err
	}
	return s.emit(ctx, tx, *m, EventMediaRemoved, map[string]any{"from_status": from, "reason_code": reason})
}

// StaffRemove is content moderation (content.moderate): any non-removed media becomes REMOVED with a reason. The
// stored objects are kept (evidence for the moderation record); removed media is never served publicly or to the
// owner again.
func (s *Service) StaffRemove(ctx context.Context, staffID, campaignID, mediaID, reasonCode, note string) (Media, error) {
	if s.d.Core == nil {
		return Media{}, ErrNotConfigured
	}
	if _, err := s.d.Core.Load(ctx, campaignID); err != nil {
		return Media{}, err
	}
	a := actor{Type: "STAFF", ID: staffID}
	var m Media
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if m, err = s.load(ctx, tx, campaignID, mediaID, true); err != nil {
			return err
		}
		return s.remove(ctx, tx, &m, a, reasonCode, note)
	})
	return m, err
}
