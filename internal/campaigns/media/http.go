package media

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/Fatifizo/fundzim/internal/campaigns"
	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/storage"
)

// Route is one HTTP route for the composition root: register each with r.HandleFunc(rt.Pattern, rt.Policy, rt.Handler).
type Route struct {
	Pattern string
	Policy  httpx.Policy
	Handler http.HandlerFunc
}

// UploadPattern is the upload route; it needs a body-limit exemption (multipart file).
const UploadPattern = "POST /api/v1/campaigns/{id}/media"

// Routes lists the campaign media API (interface-contracts §6). Object-level authorisation is in the handlers:
// owner routes go through campaigns OwnerAccess (404 for anyone else), staff routes through the route permission.
func (s *Service) Routes() []Route {
	user := httpx.User()
	return []Route{
		{UploadPattern, user, s.HandleUpload},
		{"GET /api/v1/campaigns/{id}/media", user, s.HandleList},
		{"PATCH /api/v1/campaigns/{id}/media/{media_id}", user, s.HandleUpdate},
		{"DELETE /api/v1/campaigns/{id}/media/{media_id}", user, s.HandleRemove},
		{"GET /api/v1/campaigns/{id}/media/{media_id}/content", httpx.Authenticated(), s.HandleContent},
		// "/admin/campaigns/{id}/media" would conflict with the core's "GET /admin/campaigns/review/{campaign_id}"
		// (ServeMux: overlapping, neither more specific), hence the extra segment.
		{"GET /api/v1/admin/campaigns/{id}/media/all", httpx.Permission(PermView), s.HandleStaffList},
		{"POST /api/v1/admin/campaigns/{id}/media/{media_id}/remove", httpx.Permission(PermModerate), s.HandleStaffRemove},
		{"GET /api/v1/public/campaigns/{slug}/media/{media_id}", httpx.Public(), s.HandlePublicContent},
	}
}

func (s *Service) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, storage.ErrNotFound) || errors.Is(err, storage.ErrNotClean) {
		err = ErrNotAvailable
	}
	httpx.WriteError(w, r, s.d.Logger, err)
}

func principal(r *http.Request) *authz.Principal { return authz.PrincipalFrom(r.Context()) }

func noStore(w http.ResponseWriter) { w.Header().Set("Cache-Control", "private, no-store") }

func (s *Service) withURL(m Media) Media {
	if m.Status == StatusApproved {
		m.ContentURL = "/api/v1/campaigns/" + m.CampaignID + "/media/" + m.ID + "/content"
	}
	return m
}

func (s *Service) withURLs(list []Media) []Media {
	for i := range list {
		list[i] = s.withURL(list[i])
	}
	return list
}

// HandleUpload: POST /campaigns/{id}/media (multipart: kind, position?, alt_text, depicts_minor, then file).
func (s *Service) HandleUpload(w http.ResponseWriter, r *http.Request) {
	up, err := storage.ParseMultipart(w, r, storage.MultipartSpec{FileField: "file",
		Fields:   []string{"kind", "position", "alt_text", "depicts_minor"},
		Required: []string{"kind", "alt_text", "depicts_minor"}, MaxFileBytes: s.d.MaxUploadBytes, MaxFieldBytes: 1024})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	in := UploadInput{CampaignID: r.PathValue("id"), UserID: principal(r).UserID, Kind: up.Fields["kind"], AltText: up.Fields["alt_text"],
		DeclaredContentType: up.DeclaredContentType, Body: up.File}
	switch up.Fields["depicts_minor"] {
	case "false":
	case "true":
		in.DepictsMinor = true
	default:
		s.fail(w, r, httpx.Validation(errs.Detail{Field: "depicts_minor", Code: "INVALID_VALUE"}))
		return
	}
	if v, ok := up.Fields["position"]; ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			s.fail(w, r, httpx.Validation(errs.Detail{Field: "position", Code: "INVALID_VALUE"}))
			return
		}
		in.Position = &n
	}
	m, err := s.Upload(r.Context(), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	noStore(w)
	w.Header().Set("ETag", strconv.Quote(strconv.Itoa(m.Version)))
	httpx.WriteData(w, r, http.StatusCreated, s.withURL(m))
}

// HandleList: GET /campaigns/{id}/media (owner or organisation member; every status).
func (s *Service) HandleList(w http.ResponseWriter, r *http.Request) {
	list, err := s.List(r.Context(), principal(r).UserID, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"media": s.withURLs(list)})
}

type updateReq struct {
	Position *int    `json:"position"`
	AltText  *string `json:"alt_text"`
}

func ifMatch(r *http.Request) (int, error) {
	v := strings.TrimSpace(r.Header.Get("If-Match"))
	if v == "" {
		return 0, ErrVersionRequired
	}
	n, err := strconv.Atoi(strings.Trim(strings.TrimPrefix(v, "W/"), `"`))
	if err != nil || n < 1 {
		return 0, httpx.Validation(errs.Detail{Field: "If-Match", Code: "INVALID_VERSION"})
	}
	return n, nil
}

// HandleUpdate: PATCH /campaigns/{id}/media/{media_id} {position?, alt_text?} with If-Match.
func (s *Service) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	v, err := ifMatch(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var req updateReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	m, err := s.Update(r.Context(), UpdateInput{CampaignID: r.PathValue("id"), MediaID: r.PathValue("media_id"), UserID: principal(r).UserID,
		Position: req.Position, AltText: req.AltText, IfVersion: v})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	noStore(w)
	w.Header().Set("ETag", strconv.Quote(strconv.Itoa(m.Version)))
	httpx.WriteData(w, r, http.StatusOK, s.withURL(m))
}

// HandleRemove: DELETE /campaigns/{id}/media/{media_id} (owner; soft).
func (s *Service) HandleRemove(w http.ResponseWriter, r *http.Request) {
	if err := s.Remove(r.Context(), principal(r).UserID, r.PathValue("id"), r.PathValue("media_id")); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleStaffList: GET /admin/campaigns/{id}/media/all (campaign.view).
func (s *Service) HandleStaffList(w http.ResponseWriter, r *http.Request) {
	list, err := s.StaffList(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"media": s.withURLs(list)})
}

type removeReq struct {
	ReasonCode string `json:"reason_code"`
	Note       string `json:"note"`
}

var reasonRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,63}$`)

// HandleStaffRemove: POST /admin/campaigns/{id}/media/{media_id}/remove {reason_code, note} (content.moderate).
func (s *Service) HandleStaffRemove(w http.ResponseWriter, r *http.Request) {
	var req removeReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		s.fail(w, r, err)
		return
	}
	var details []errs.Detail
	if !reasonRe.MatchString(req.ReasonCode) || req.ReasonCode == RemovedByOwner {
		details = append(details, errs.Detail{Field: "reason_code", Code: "INVALID_VALUE"})
	}
	note, code := campaigns.CleanText(req.Note, 3, 5000, true)
	if code != "" {
		details = append(details, errs.Detail{Field: "note", Code: code})
	}
	if len(details) > 0 {
		s.fail(w, r, httpx.Validation(details...))
		return
	}
	m, err := s.StaffRemove(r.Context(), principal(r).UserID, r.PathValue("id"), r.PathValue("media_id"), req.ReasonCode, note)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, http.StatusOK, m)
}

// HandleContent: GET /campaigns/{id}/media/{media_id}/content — owner (or organisation member) or staff with
// campaign.view; only the processed derivative of APPROVED media, never the original.
func (s *Service) HandleContent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principal(r)
	campaignID, mediaID := r.PathValue("id"), r.PathValue("media_id")
	if s.d.Core == nil {
		s.fail(w, r, ErrNotConfigured)
		return
	}
	var err error
	if p.Kind == authz.KindStaff {
		if !p.Has(PermView) {
			s.fail(w, r, errs.New(errs.Forbidden, "PERMISSION_DENIED", "You do not have permission for this action."))
			return
		}
		_, err = s.d.Core.Load(ctx, campaignID)
	} else {
		_, err = s.d.Core.OwnerAccess(ctx, p.UserID, campaignID, false)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	m, err := s.load(ctx, s.d.Pool, campaignID, mediaID, false)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if m.Status != StatusApproved || m.processedObjectID == nil {
		s.fail(w, r, ErrNotAvailable)
		return
	}
	s.serve(w, r, *m.processedObjectID, "private, no-store", "same-origin")
}

// HandlePublicContent: GET /public/campaigns/{slug}/media/{media_id} — image bytes of APPROVED media that is part of
// the approved version of a publicly exposed campaign. Everything else is 404.
func (s *Service) HandlePublicContent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if s.d.Core == nil {
		s.fail(w, r, ErrNotConfigured)
		return
	}
	a, err := s.d.Core.PublicBySlug(ctx, r.PathValue("slug"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	obj, err := s.publicObject(ctx, a.CampaignID, r.PathValue("media_id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.serve(w, r, obj, "public, max-age="+strconv.Itoa(int(s.d.PublicMaxAge.Seconds())), "cross-origin")
}

// serve streams a CLEAN derivative with strict headers. The ETag is the content SHA-256, so a conditional request
// is answered 304 without reading the bytes.
func (s *Service) serve(w http.ResponseWriter, r *http.Request, objectID, cacheControl, corp string) {
	ctx := r.Context()
	o, err := s.d.Storage.Get(ctx, objectID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if o.Status != storage.StatusClean || (o.SniffedType != storage.TypeJPEG && o.SniffedType != storage.TypePNG) || len(o.SHA256) == 0 {
		s.fail(w, r, ErrNotAvailable)
		return
	}
	etag := `"` + hex.EncodeToString(o.SHA256) + `"`
	h := w.Header()
	h.Set("ETag", etag)
	h.Set("Cache-Control", cacheControl)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cross-Origin-Resource-Policy", corp)
	if inm := r.Header.Get("If-None-Match"); inm != "" && etagMatch(inm, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	rc, obj, err := s.d.Storage.Open(ctx, objectID)
	if err != nil {
		h.Del("ETag")
		h.Set("Cache-Control", "no-store")
		s.fail(w, r, err)
		return
	}
	defer rc.Close()
	ext := map[string]string{storage.TypeJPEG: "jpg", storage.TypePNG: "png"}[obj.SniffedType]
	h.Set("Content-Type", obj.SniffedType)
	h.Set("Content-Length", strconv.FormatInt(obj.SizeBytes, 10))
	h.Set("Content-Disposition", `inline; filename="image.`+ext+`"`)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, rc); err != nil && !errors.Is(err, context.Canceled) {
		s.d.Logger.Warn("campaign media stream interrupted", "error_category", "io")
	}
}

func etagMatch(header, etag string) bool {
	for _, part := range strings.Split(header, ",") {
		p := strings.TrimSpace(part)
		if p == "*" || strings.TrimPrefix(p, "W/") == etag {
			return true
		}
	}
	return false
}
