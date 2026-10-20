package discovery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
)

// Route is one discovery route. RateLimited public reads get the discovery.search.ip limiter; Idempotent staff creations
// get the REQUIRED idempotency middleware (composition root).
type Route struct {
	Pattern     string
	Policy      httpx.Policy
	Handler     http.HandlerFunc
	Idempotent  bool
	Scope       string
	RateLimited bool
}

// HTTP serves the discovery API.
type HTTP struct{ S *Service }

// Routes lists the discovery API (api/openapi/fundzim-v1.yaml, tag discovery).
func (h *HTTP) Routes() []Route {
	public, user, perm := httpx.Public(), httpx.User(), httpx.Permission
	const pub = "/api/v1/discovery"
	const adm = "/api/v1/admin/discovery"
	return []Route{
		{Pattern: "GET " + pub + "/campaigns", Policy: public, Handler: h.search("DIRECTORY"), RateLimited: true},
		{Pattern: "GET " + pub + "/search", Policy: public, Handler: h.search("SEARCH"), RateLimited: true},
		{Pattern: "GET " + pub + "/categories", Policy: public, Handler: h.categories, RateLimited: true},
		{Pattern: "GET " + pub + "/locations", Policy: public, Handler: h.locations, RateLimited: true},
		{Pattern: "GET " + pub + "/trending", Policy: public, Handler: h.trending, RateLimited: true},
		{Pattern: "GET " + pub + "/featured", Policy: public, Handler: h.featured, RateLimited: true},
		{Pattern: "GET " + pub + "/recommendations", Policy: public, Handler: h.recommendations, RateLimited: true},
		{Pattern: "GET " + pub + "/sitemap", Policy: public, Handler: h.sitemap, RateLimited: true},
		{Pattern: "POST " + pub + "/events", Policy: public, Handler: h.events},
		{Pattern: "POST " + pub + "/referrals", Policy: public, Handler: h.referral},
		// Stage 7 compatibility: the home page's featured list (same envelope; selection FEATURED | RECENTLY_PUBLISHED)
		{Pattern: "GET /api/v1/public/campaigns/featured", Policy: public, Handler: h.featured, RateLimited: true},

		{Pattern: "GET /api/v1/campaigns/{campaign_id}/analytics", Policy: user, Handler: h.campaignAnalytics},

		{Pattern: "GET " + adm + "/overview", Policy: perm(PermView), Handler: h.overview},
		{Pattern: "GET " + adm + "/featured", Policy: perm(PermView), Handler: h.placements},
		{Pattern: "POST " + adm + "/featured", Policy: perm(PermFeatureRequest), Handler: h.requestPlacement, Idempotent: true, Scope: "discovery.featured.create"},
		{Pattern: "POST " + adm + "/featured/{placement_id}/approve", Policy: perm(PermFeatureApprove), Handler: h.decidePlacement("approve")},
		{Pattern: "POST " + adm + "/featured/{placement_id}/reject", Policy: perm(PermFeatureApprove), Handler: h.decidePlacement("reject")},
		{Pattern: "POST " + adm + "/featured/{placement_id}/cancel", Policy: perm(PermFeatureRequest), Handler: h.decidePlacement("cancel")},
		{Pattern: "POST " + adm + "/featured/{placement_id}/remove", Policy: perm(PermFeatureRemove), Handler: h.decidePlacement("remove")},
		{Pattern: "GET " + adm + "/ranking", Policy: perm(PermView), Handler: h.ranking},
		{Pattern: "POST " + adm + "/ranking", Policy: perm(PermRankingRequest), Handler: h.proposeRanking, Idempotent: true, Scope: "discovery.ranking.create"},
		{Pattern: "POST " + adm + "/ranking/{policy_id}/approve", Policy: perm(PermRankingApprove), Handler: h.decideRanking("approve")},
		{Pattern: "POST " + adm + "/ranking/{policy_id}/reject", Policy: perm(PermRankingApprove), Handler: h.decideRanking("reject")},
		{Pattern: "POST " + adm + "/ranking/{policy_id}/withdraw", Policy: perm(PermRankingRequest), Handler: h.decideRanking("withdraw")},
		{Pattern: "POST " + adm + "/referrals/{code}/revoke", Policy: perm(PermOperate), Handler: h.revokeReferral},
		{Pattern: "POST " + adm + "/index/reconcile", Policy: perm(PermOperate), Handler: h.reconcile},
	}
}

func (h *HTTP) fail(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteError(w, r, h.S.Logger, err)
}

func staffID(r *http.Request) string { return authz.PrincipalFrom(r.Context()).UserID }

// writePublic: public data with a strong ETag and `public, no-cache` (ADR-038 §1: always revalidated, so a campaign that
// leaves public view disappears at once). Never shared-cached without revalidation.
func (h *HTTP) writePublic(w http.ResponseWriter, r *http.Request, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	sum := sha256.Sum256(raw)
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	w.Header().Set("Cache-Control", "public, no-cache")
	w.Header().Set("ETag", etag)
	w.Header().Set("Vary", "Accept-Encoding")
	for _, v := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		if strings.TrimSpace(v) == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	httpx.WriteData(w, r, http.StatusOK, json.RawMessage(raw))
}

func writePrivate(w http.ResponseWriter, r *http.Request, status int, data any) {
	w.Header().Set("Cache-Control", "private, no-store")
	httpx.WriteData(w, r, status, data)
}

func intParam(r *http.Request, name string) int {
	n, _ := strconv.Atoi(r.URL.Query().Get(name))
	return n
}

func (h *HTTP) search(surface string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		in := SearchInput{Q: q.Get("q"), Category: q.Get("category"), Province: q.Get("province"), Town: q.Get("town"), Status: q.Get("status"),
			Published: q.Get("published"), Organiser: q.Get("organiser"), Sort: q.Get("sort"), Cursor: q.Get("cursor"), Limit: intParam(r, "limit"),
			Surface: surface}
		switch {
		case in.Category != "" && surface == "DIRECTORY" && in.Q == "":
			in.Surface = "CATEGORY"
		case (in.Province != "" || in.Town != "") && surface == "DIRECTORY" && in.Q == "":
			in.Surface = "LOCATION"
		}
		res, err := h.S.Search(r.Context(), in)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		h.writePublic(w, r, res)
	}
}

func (h *HTTP) categories(w http.ResponseWriter, r *http.Request) {
	out, err := h.S.Categories(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writePublic(w, r, map[string]any{"items": out})
}

func (h *HTTP) locations(w http.ResponseWriter, r *http.Request) {
	out, err := h.S.Locations(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writePublic(w, r, out)
}

func (h *HTTP) trending(w http.ResponseWriter, r *http.Request) {
	cat := r.URL.Query().Get("category")
	if cat != "" && !catRe.MatchString(cat) {
		h.fail(w, r, httpx.Validation(detail("category", "INVALID_VALUE")))
		return
	}
	items, err := h.S.Trending(r.Context(), cat, intParam(r, "limit"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w2, v, err := h.S.activeWeights(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writePublic(w, r, map[string]any{"items": items, "next_cursor": nil, "rule": TrendingRuleText, "policy_version": v, "window_days": w2.WindowDays})
}

// TrendingRuleText is the public explanation of trending.
const TrendingRuleText = "Ranked by how many different people viewed and shared each campaign recently (automated traffic excluded, each " +
	"person counted once a day), with newer campaigns given more weight. Money is never part of the ranking, and nobody can pay to trend."

func (h *HTTP) featured(w http.ResponseWriter, r *http.Request) {
	fill := r.URL.Query().Get("fill") != "false"
	items, err := h.S.Featured(r.Context(), intParam(r, "limit"), fill)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writePublic(w, r, map[string]any{"items": items, "next_cursor": nil})
}

func (h *HTTP) recommendations(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	out, err := h.S.Recommend(r.Context(), RecommendInput{Campaign: q.Get("campaign"), Category: q.Get("category"), Q: q.Get("q"), Limit: intParam(r, "limit")})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writePublic(w, r, out)
}

func (h *HTTP) sitemap(w http.ResponseWriter, r *http.Request) {
	out, err := h.S.Sitemap(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writePublic(w, r, out)
}

func (h *HTTP) events(w http.ResponseWriter, r *http.Request) {
	var in EventsInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	if _, err := h.S.Ingest(r.Context(), in, r.UserAgent()); err != nil {
		h.fail(w, r, err)
		return
	}
	// 204 without a body (as Stage 7's counters): browsers send these as keepalive requests nobody reads, and an unread
	// response body kept the request open in Chromium (the campaign page never reached network idle)
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTP) referral(w http.ResponseWriter, r *http.Request) {
	var in ReferralInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	out, err := h.S.IssueReferral(r.Context(), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writePrivate(w, r, http.StatusCreated, out)
}

func (h *HTTP) campaignAnalytics(w http.ResponseWriter, r *http.Request) {
	out, err := h.S.CampaignAnalytics(r.Context(), staffID(r), r.PathValue("campaign_id"), intParam(r, "days"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writePrivate(w, r, http.StatusOK, out)
}

func (h *HTTP) overview(w http.ResponseWriter, r *http.Request) {
	out, err := h.S.Overview(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writePrivate(w, r, http.StatusOK, out)
}

func (h *HTTP) placements(w http.ResponseWriter, r *http.Request) {
	out, err := h.S.Placements(r.Context(), r.URL.Query().Get("status"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writePrivate(w, r, http.StatusOK, map[string]any{"items": out, "next_cursor": nil})
}

func (h *HTTP) requestPlacement(w http.ResponseWriter, r *http.Request) {
	var in PlacementInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	out, err := h.S.RequestPlacement(r.Context(), staffID(r), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writePrivate(w, r, http.StatusCreated, out)
}

type noteInput struct {
	Note string `json:"note"`
}

func (h *HTTP) decidePlacement(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in noteInput
		if err := httpx.DecodeJSON(r, &in); err != nil {
			h.fail(w, r, err)
			return
		}
		out, err := h.S.DecidePlacement(r.Context(), staffID(r), r.PathValue("placement_id"), action, in.Note)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		writePrivate(w, r, http.StatusOK, out)
	}
}

func (h *HTTP) ranking(w http.ResponseWriter, r *http.Request) {
	out, err := h.S.Ranking(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writePrivate(w, r, http.StatusOK, out)
}

type rankingInput struct {
	Weights Weights `json:"weights"`
	Reason  string  `json:"reason"`
}

func (h *HTTP) proposeRanking(w http.ResponseWriter, r *http.Request) {
	var in rankingInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	out, err := h.S.ProposeRanking(r.Context(), staffID(r), in.Weights, in.Reason)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writePrivate(w, r, http.StatusCreated, out)
}

func (h *HTTP) decideRanking(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in noteInput
		if err := httpx.DecodeJSON(r, &in); err != nil {
			h.fail(w, r, err)
			return
		}
		out, err := h.S.DecideRanking(r.Context(), staffID(r), r.PathValue("policy_id"), action, in.Note)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		writePrivate(w, r, http.StatusOK, out)
	}
}

func (h *HTTP) revokeReferral(w http.ResponseWriter, r *http.Request) {
	var in noteInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.S.RevokeReferral(r.Context(), staffID(r), r.PathValue("code"), in.Note); err != nil {
		h.fail(w, r, err)
		return
	}
	writePrivate(w, r, http.StatusOK, map[string]string{"code": r.PathValue("code"), "status": "REVOKED"})
}

// reconcile runs one bounded reconciliation pass now (at most 2 000 campaigns; the periodic job covers the rest).
func (h *HTTP) reconcile(w http.ResponseWriter, r *http.Request) {
	res, err := h.S.Reconcile(r.Context(), 2000)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writePrivate(w, r, http.StatusOK, map[string]int{"checked": res.Checked, "reindexed": res.Reindexed, "failed": res.Failed})
}

func detail(field, code string) errs.Detail { return errs.Detail{Field: field, Code: code} }
