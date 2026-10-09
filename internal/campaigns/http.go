package campaigns

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/money"
)

// Route is one HTTP route for the composition root. Idempotent marks routes that accept an optional
// Idempotency-Key.
type Route struct {
	Pattern    string
	Policy     httpx.Policy
	Handler    http.HandlerFunc
	Idempotent bool
}

// Routes is the campaign API (interface-contracts §6). Object-level authorisation is in the handlers; staff
// permissions that require step-up are enforced by the route policy.
func (s *Service) Routes() []Route {
	user, public := httpx.User(), httpx.Public()
	perm := httpx.Permission
	const own = "/api/v1/campaigns/{campaign_id}"
	const adm = "/api/v1/admin/campaigns/{campaign_id}"
	rs := []Route{
		{Pattern: "GET /api/v1/campaign-categories", Policy: public, Handler: s.handleCategories},
		{Pattern: "POST /api/v1/campaigns", Policy: user, Handler: s.handleCreate, Idempotent: true},
		{Pattern: "GET /api/v1/campaigns/mine", Policy: user, Handler: s.handleMine},
		{Pattern: "GET /api/v1/campaign-currencies", Policy: public, Handler: s.handleCurrencies},
		{Pattern: "GET " + own, Policy: user, Handler: s.handleGet},
		{Pattern: "GET " + own + "/eligibility", Policy: user, Handler: s.handleEligibility},
		{Pattern: "PATCH " + own, Policy: user, Handler: s.handleUpdate},
		{Pattern: "POST " + own + "/submit", Policy: user, Handler: s.ownerAction("submit")},
		{Pattern: "POST " + own + "/withdraw", Policy: user, Handler: s.ownerAction("withdraw")},
		{Pattern: "POST " + own + "/revise", Policy: user, Handler: s.ownerAction("revise")},
		{Pattern: "POST " + own + "/beneficiaries", Policy: user, Handler: s.handleLinkBeneficiary},
		{Pattern: "DELETE " + own + "/beneficiaries/{beneficiary_id}", Policy: user, Handler: s.handleUnlinkBeneficiary},
		{Pattern: "GET /api/v1/admin/campaigns", Policy: perm("campaign.view"), Handler: s.handleAdminList},
		{Pattern: "GET /api/v1/admin/campaigns/review", Policy: perm("campaign.review"), Handler: s.handleQueue},
		{Pattern: "GET /api/v1/admin/campaigns/{campaign_id}/review", Policy: perm("campaign.review"), Handler: s.handleReviewDetail},
		{Pattern: "GET /api/v1/admin/campaign-categories", Policy: perm("campaign.view"), Handler: s.handleAdminCategories},
		{Pattern: "PATCH /api/v1/admin/campaign-categories/{code}", Policy: perm("campaign.category.manage"), Handler: s.handleUpdateCategory},
		{Pattern: "GET /api/v1/public/campaigns", Policy: public, Handler: s.handleSearch},
		{Pattern: "GET /api/v1/public/campaigns/{slug}", Policy: public, Handler: s.handlePublic},
	}
	for _, a := range []string{"cancel", "publish", "pause", "resume", "complete", "archive"} {
		rs = append(rs, Route{Pattern: "POST " + own + "/" + a, Policy: user, Handler: s.ownerAction(a)})
	}
	for a, p := range map[string]string{"assign": "campaign.review", "start-review": "campaign.review", "request-changes": "campaign.review",
		"escalate": "campaign.review", "approve": "campaign.decide", "reject": "campaign.decide", "reopen": "campaign.decide",
		"second-approval": "campaign.decide.high", "publish": "campaign.publish", "suspend": "campaign.suspend", "cancel": "campaign.suspend",
		"complete": "campaign.suspend", "archive": "campaign.suspend", "reactivate": "campaign.unsuspend"} {
		rs = append(rs, Route{Pattern: "POST " + adm + "/" + a, Policy: perm(p), Handler: s.staffActionHandler(a)})
	}
	return rs
}

func principal(r *http.Request) *authz.Principal { return authz.PrincipalFrom(r.Context()) }

func (s *Service) fail(w http.ResponseWriter, r *http.Request, err error) {
	httpx.WriteError(w, r, s.Logger, err)
}

func noStore(w http.ResponseWriter) { w.Header().Set("Cache-Control", "private, no-store") }

func ifMatch(r *http.Request) int {
	n, _ := strconv.Atoi(strings.Trim(r.Header.Get("If-Match"), `" `))
	return n
}

func (s *Service) writeOwner(w http.ResponseWriter, r *http.Request, id string, status int) {
	v, err := s.Get(r.Context(), principal(r).UserID, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	noStore(w)
	w.Header().Set("ETag", strconv.Quote(strconv.Itoa(v.Version)))
	httpx.WriteData(w, r, status, v)
}

func (s *Service) handleCategories(w http.ResponseWriter, r *http.Request) {
	cs, err := s.Categories(r.Context(), false)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	httpx.WriteData(w, r, http.StatusOK, cs)
}

func (s *Service) handleCurrencies(w http.ResponseWriter, r *http.Request) {
	cs, err := money.UsableCurrencyInfo(r.Context(), s.Pool)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	httpx.WriteData(w, r, http.StatusOK, cs)
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	var in CreateInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	id, err := s.Create(r.Context(), principal(r).UserID, in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeOwner(w, r, id, http.StatusCreated)
}

func (s *Service) handleMine(w http.ResponseWriter, r *http.Request) {
	org := r.URL.Query().Get("organisation_id")
	if org != "" && !validID(org) {
		s.fail(w, r, httpx.Validation(errs.Detail{Field: "organisation_id", Code: "INVALID_FORMAT"}))
		return
	}
	out, err := s.Mine(r.Context(), principal(r).UserID, org)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, http.StatusOK, out)
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	s.writeOwner(w, r, r.PathValue("campaign_id"), http.StatusOK)
}

func (s *Service) handleEligibility(w http.ResponseWriter, r *http.Request) {
	action := r.URL.Query().Get("action")
	if action == "" {
		action = ActSubmit
	}
	res, err := s.EligibilityFor(r.Context(), principal(r).UserID, r.PathValue("campaign_id"), action)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, http.StatusOK, res)
}

func (s *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
	var p Patch
	if err := httpx.DecodeJSON(r, &p); err != nil {
		s.fail(w, r, err)
		return
	}
	id := r.PathValue("campaign_id")
	if err := s.Update(r.Context(), principal(r).UserID, id, p, ifMatch(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeOwner(w, r, id, http.StatusOK)
}

type ownerBody struct {
	Reason string `json:"reason"`
	Note   string `json:"note"`
}

func (s *Service) ownerAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body ownerBody
		if r.ContentLength != 0 {
			if err := httpx.DecodeJSON(r, &body); err != nil {
				s.fail(w, r, err)
				return
			}
		}
		ctx, uid, id := r.Context(), principal(r).UserID, r.PathValue("campaign_id")
		note, code := CleanText(body.Note, 0, 2000, true)
		if code != "" {
			s.fail(w, r, httpx.Validation(errs.Detail{Field: "note", Code: code}))
			return
		}
		var err error
		switch action {
		case "submit":
			err = s.Submit(ctx, uid, id)
		case "withdraw":
			err = s.Withdraw(ctx, uid, id)
		case "revise":
			err = s.Revise(ctx, uid, id)
		default:
			err = s.OwnerTransition(ctx, uid, id, action, body.Reason, note)
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.writeOwner(w, r, id, http.StatusOK)
	}
}

func (s *Service) handleLinkBeneficiary(w http.ResponseWriter, r *http.Request) {
	var in BeneficiaryInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	id := r.PathValue("campaign_id")
	if err := s.LinkBeneficiary(r.Context(), principal(r).UserID, id, in); err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeOwner(w, r, id, http.StatusOK)
}

func (s *Service) handleUnlinkBeneficiary(w http.ResponseWriter, r *http.Request) {
	var body ownerBody
	if r.ContentLength != 0 {
		if err := httpx.DecodeJSON(r, &body); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	id := r.PathValue("campaign_id")
	if err := s.UnlinkBeneficiary(r.Context(), principal(r).UserID, id, r.PathValue("beneficiary_id"), body.Reason); err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeOwner(w, r, id, http.StatusOK)
}

// ---- staff ------------------------------------------------------------------------------------------------

func (s *Service) handleAdminList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	out, err := s.AdminList(r.Context(), q.Get("status"), q.Get("q"), limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, http.StatusOK, out)
}

func (s *Service) handleQueue(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	items, next, err := s.Queue(r.Context(), principal(r).UserID, q.Get("assigned"), q.Get("category"), q.Get("cursor"), limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	noStore(w)
	var nc any
	if next != "" {
		nc = next
	}
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"items": items, "next_cursor": nc})
}

func (s *Service) handleReviewDetail(w http.ResponseWriter, r *http.Request) {
	d, err := s.ReviewDetailFor(r.Context(), principal(r).UserID, r.PathValue("campaign_id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, http.StatusOK, d)
}

func (s *Service) staffActionHandler(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var d Decision
		if r.ContentLength != 0 {
			if err := httpx.DecodeJSON(r, &d); err != nil {
				s.fail(w, r, err)
				return
			}
		}
		ctx, sid, id := r.Context(), principal(r).UserID, r.PathValue("campaign_id")
		var err error
		status := http.StatusOK
		switch action {
		case "assign":
			err = s.Assign(ctx, sid, id, d)
		case "start-review":
			err = s.StartReview(ctx, sid, id)
		case "request-changes":
			err = s.RequestChanges(ctx, sid, id, d)
		case "escalate":
			err = s.Escalate(ctx, sid, id, d)
		case "approve":
			var pending bool
			if pending, err = s.Approve(ctx, sid, id, d); pending {
				status = http.StatusAccepted
			}
		case "second-approval":
			err = s.SecondApprove(ctx, sid, id, d)
		case "reject":
			err = s.Reject(ctx, sid, id, d)
		case "reopen":
			err = s.Reopen(ctx, sid, id, d)
		default:
			err = s.StaffAction(ctx, sid, id, action, d)
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		c, err := s.Load(ctx, id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		noStore(w)
		httpx.WriteData(w, r, status, map[string]any{"campaign_id": id, "status": c.Status, "awaiting_second_approval": status == http.StatusAccepted})
	}
}

func (s *Service) handleAdminCategories(w http.ResponseWriter, r *http.Request) {
	cs, err := s.Categories(r.Context(), true)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, http.StatusOK, cs)
}

func (s *Service) handleUpdateCategory(w http.ResponseWriter, r *http.Request) {
	var p CategoryPatch
	if err := httpx.DecodeJSON(r, &p); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.UpdateCategory(r.Context(), principal(r).UserID, r.PathValue("code"), p, ifMatch(r)); err != nil {
		s.fail(w, r, err)
		return
	}
	s.handleAdminCategories(w, r)
}

// ---- public -----------------------------------------------------------------------------------------------

func (s *Service) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	items, next, err := s.Search(r.Context(), SearchInput{Q: q.Get("q"), Category: q.Get("category"), Sort: q.Get("sort"), Cursor: q.Get("cursor"), Limit: limit})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var nc any
	if next != "" {
		nc = next
	}
	w.Header().Set("Cache-Control", "public, max-age=30")
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"items": items, "next_cursor": nc})
}

func (s *Service) handlePublic(w http.ResponseWriter, r *http.Request) {
	p, err := s.PublicView(r.Context(), r.PathValue("slug"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=30")
	httpx.WriteData(w, r, http.StatusOK, p)
}
