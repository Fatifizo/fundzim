package updates

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Route is one HTTP route for the composition root: register each with r.HandleFunc(rt.Pattern, rt.Policy, rt.Handler).
type Route struct {
	Pattern string
	Policy  httpx.Policy
	Handler http.HandlerFunc
}

// Routes lists the campaign updates API (interface-contracts §6): owner routes need a personal session (CSRF on
// unsafe methods is enforced by the middleware chain); staff routes need content.moderate; the public route is
// anonymous and exposes published updates of publicly exposed campaigns only.
func (s *Service) Routes() []Route {
	user, mod, public := httpx.User(), httpx.Permission(PermModerate), httpx.Public()
	return []Route{
		{"POST /api/v1/campaigns/{id}/updates", user, s.HandleCreate},
		{"GET /api/v1/campaigns/{id}/updates", user, s.HandleListOwner},
		{"PATCH /api/v1/campaigns/{id}/updates/{update_id}", user, s.HandleEdit},
		{"DELETE /api/v1/campaigns/{id}/updates/{update_id}", user, s.HandleDelete},
		{"GET /api/v1/admin/campaigns/updates/moderation", mod, s.HandleModerationQueue},
		{"POST /api/v1/admin/campaigns/updates/{update_id}/approve", mod, s.HandleApprove},
		{"POST /api/v1/admin/campaigns/updates/{update_id}/hide", mod, s.HandleHide},
		{"GET /api/v1/public/campaigns/{slug}/updates", public, s.HandleListPublic},
	}
}

func (s *Service) fail(w http.ResponseWriter, r *http.Request, err error) {
	httpx.WriteError(w, r, s.Logger, err)
}

func userID(r *http.Request) (string, error) {
	p := authz.PrincipalFrom(r.Context())
	if p == nil || p.Kind != authz.KindUser {
		return "", errs.New(errs.NotFound, errs.CodeRouteNotFound, "No such route.")
	}
	return p.UserID, nil
}

func staffID(r *http.Request) (string, error) {
	p := authz.PrincipalFrom(r.Context())
	if p == nil || p.Kind != authz.KindStaff {
		return "", errs.New(errs.NotFound, errs.CodeRouteNotFound, "No such route.")
	}
	return p.UserID, nil
}

// ifMatch parses If-Match ("3" or "\"3\""); 0 when absent. A malformed value is a validation error.
func ifMatch(r *http.Request) (int, error) {
	v := strings.Trim(strings.TrimSpace(r.Header.Get("If-Match")), `"`)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 0, httpx.Validation(errs.Detail{Field: "If-Match", Code: "INVALID_VERSION"})
	}
	return n, nil
}

// page parses ?cursor=&limit=.
func page(r *http.Request) (string, int, error) {
	q := r.URL.Query()
	var details []errs.Detail
	cursor := q.Get("cursor")
	if cursor != "" && !ids.Valid(cursor) {
		details = append(details, errs.Detail{Field: "cursor", Code: "INVALID_VALUE"})
	}
	limit := 0
	if l := q.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 || n > MaxLimit {
			details = append(details, errs.Detail{Field: "limit", Code: "INVALID_VALUE"})
		}
		limit = n
	}
	if len(details) > 0 {
		return "", 0, httpx.Validation(details...)
	}
	return cursor, limit, nil
}

func writeOne(w http.ResponseWriter, r *http.Request, status int, u Update) {
	w.Header().Set("ETag", `"`+strconv.Itoa(u.Version)+`"`)
	httpx.WriteData(w, r, status, u)
}

// HandleCreate is POST /campaigns/{id}/updates ({title, body, publish, media_ids?}).
func (s *Service) HandleCreate(w http.ResponseWriter, r *http.Request) {
	uid, err := userID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in Input
	if err := httpx.DecodeJSON(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	u, err := s.Create(r.Context(), uid, r.PathValue("id"), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeOne(w, r, http.StatusCreated, u)
}

// HandleListOwner is GET /campaigns/{id}/updates (owner or organisation member; every status except DELETED).
func (s *Service) HandleListOwner(w http.ResponseWriter, r *http.Request) {
	uid, err := userID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	cursor, limit, err := page(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list, next, err := s.ListOwner(r.Context(), uid, r.PathValue("id"), cursor, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"updates": list, "next_cursor": next})
}

// HandleEdit is PATCH /campaigns/{id}/updates/{update_id} (If-Match required; drafts and pending only).
func (s *Service) HandleEdit(w http.ResponseWriter, r *http.Request) {
	uid, err := userID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := ifMatch(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if v == 0 {
		s.fail(w, r, httpx.Validation(errs.Detail{Field: "If-Match", Code: "REQUIRED"}))
		return
	}
	var p Patch
	if err := httpx.DecodeJSON(r, &p); err != nil {
		s.fail(w, r, err)
		return
	}
	u, err := s.Edit(r.Context(), uid, r.PathValue("id"), r.PathValue("update_id"), v, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeOne(w, r, http.StatusOK, u)
}

// HandleDelete is DELETE /campaigns/{id}/updates/{update_id} (soft delete; If-Match optional).
func (s *Service) HandleDelete(w http.ResponseWriter, r *http.Request) {
	uid, err := userID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := ifMatch(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Delete(r.Context(), uid, r.PathValue("id"), r.PathValue("update_id"), v); err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// HandleModerationQueue is GET /admin/campaigns/updates/moderation?cursor=&limit= (oldest first).
func (s *Service) HandleModerationQueue(w http.ResponseWriter, r *http.Request) {
	if _, err := staffID(r); err != nil {
		s.fail(w, r, err)
		return
	}
	cursor, limit, err := page(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list, next, err := s.ModerationQueue(r.Context(), cursor, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"updates": list, "next_cursor": next})
}

// HandleApprove is POST /admin/campaigns/updates/{update_id}/approve ({reason_code, note}).
func (s *Service) HandleApprove(w http.ResponseWriter, r *http.Request) { s.handleDecision(w, r, true) }

// HandleHide is POST /admin/campaigns/updates/{update_id}/hide ({reason_code, note}).
func (s *Service) HandleHide(w http.ResponseWriter, r *http.Request) { s.handleDecision(w, r, false) }

func (s *Service) handleDecision(w http.ResponseWriter, r *http.Request, approve bool) {
	sid, err := staffID(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var d Decision
	if err := httpx.DecodeJSON(r, &d); err != nil {
		s.fail(w, r, err)
		return
	}
	var u Update
	if approve {
		u, err = s.Approve(r.Context(), sid, r.PathValue("update_id"), d)
	} else {
		u, err = s.Hide(r.Context(), sid, r.PathValue("update_id"), d)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeOne(w, r, http.StatusOK, u)
}

// HandleListPublic is GET /public/campaigns/{slug}/updates?cursor=&limit= (published only, newest first).
func (s *Service) HandleListPublic(w http.ResponseWriter, r *http.Request) {
	cursor, limit, err := page(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list, next, err := s.ListPublic(r.Context(), r.PathValue("slug"), cursor, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"updates": list, "next_cursor": next})
}
