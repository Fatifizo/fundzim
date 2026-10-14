package ledger

import (
	"net/http"
	"strconv"

	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
)

// Route is one ledger route (staff only; the policy enforces the permission and, for permissions that require it,
// a fresh step-up). There is no public ledger endpoint and no free-form posting endpoint: postings come only from
// named rules, maker-checker adjustments and (TEST ledger only) synthetic fixtures.
type Route struct {
	Pattern    string
	Policy     httpx.Policy
	Handler    http.HandlerFunc
	Idempotent bool   // Idempotency-Key required (design REC-16)
	Scope      string // idempotency scope
}

// Routes is the ledger API.
func (s *Service) Routes() []Route {
	perm := httpx.Permission
	const b = "/api/v1/admin/ledger"
	view := perm("ledger.view")
	return []Route{
		{Pattern: "GET " + b + "/overview", Policy: view, Handler: s.hOverview},
		{Pattern: "GET " + b + "/accounts", Policy: view, Handler: s.hAccounts},
		{Pattern: "GET " + b + "/accounts/{account_id}", Policy: view, Handler: s.hAccount},
		{Pattern: "POST " + b + "/accounts/{account_id}/freeze", Policy: perm("ledger.account.manage"), Handler: s.hAccountStatus("FROZEN")},
		{Pattern: "POST " + b + "/accounts/{account_id}/unfreeze", Policy: perm("ledger.account.manage"), Handler: s.hAccountStatus("ACTIVE")},
		{Pattern: "POST " + b + "/accounts/{account_id}/close", Policy: perm("ledger.account.manage"), Handler: s.hAccountStatus("CLOSED")},
		{Pattern: "GET " + b + "/journals", Policy: view, Handler: s.hJournals},
		{Pattern: "GET " + b + "/journals/{transaction_id}", Policy: view, Handler: s.hJournal},
		{Pattern: "GET " + b + "/trial-balance", Policy: view, Handler: s.hTrialBalance},
		{Pattern: "GET " + b + "/periods", Policy: view, Handler: s.hPeriods},
		{Pattern: "POST " + b + "/periods/{period_id}/request-close", Policy: perm("ledger.period.close"), Handler: s.hPeriod("request")},
		{Pattern: "POST " + b + "/periods/{period_id}/approve-close", Policy: perm("ledger.period.close"), Handler: s.hPeriod("approve")},
		{Pattern: "POST " + b + "/periods/{period_id}/abort-close", Policy: perm("ledger.period.close"), Handler: s.hPeriod("abort")},
		{Pattern: "GET " + b + "/adjustments", Policy: view, Handler: s.hAdjustments},
		{Pattern: "POST " + b + "/adjustments", Policy: perm("ledger.adjustment.create"), Handler: s.hRequestAdjustment, Idempotent: true,
			Scope: "ledger.adjustments.create"},
		{Pattern: "POST " + b + "/adjustments/{adjustment_id}/approve", Policy: perm("ledger.adjustment.approve"), Handler: s.hAdjustment("approve")},
		{Pattern: "POST " + b + "/adjustments/{adjustment_id}/reject", Policy: perm("ledger.adjustment.approve"), Handler: s.hAdjustment("reject")},
		{Pattern: "POST " + b + "/adjustments/{adjustment_id}/withdraw", Policy: perm("ledger.adjustment.create"), Handler: s.hAdjustment("withdraw")},
		{Pattern: "GET " + b + "/reconciliation", Policy: view, Handler: s.hReconciliation},
		{Pattern: "POST " + b + "/reconciliation/run", Policy: perm("ledger.reconciliation.operate"), Handler: s.hReconcile},
		{Pattern: "POST " + b + "/reconciliation/{reference_id}/investigate", Policy: perm("ledger.reconciliation.operate"), Handler: s.hRecon("investigate")},
		{Pattern: "POST " + b + "/reconciliation/{reference_id}/resolve", Policy: perm("ledger.reconciliation.operate"), Handler: s.hRecon("resolve")},
		{Pattern: "GET " + b + "/exceptions", Policy: view, Handler: s.hExceptions},
		{Pattern: "POST " + b + "/integrity/run", Policy: perm("ledger.reconciliation.operate"), Handler: s.hIntegrity},
		{Pattern: "GET " + b + "/source-references", Policy: view, Handler: s.hSources},
		{Pattern: "POST " + b + "/synthetic-fixtures", Policy: perm("ledger.synthetic.post"), Handler: s.hFixture},
	}
}

func noStore(w http.ResponseWriter) { w.Header().Set("Cache-Control", "no-store") }

func (s *Service) fail(w http.ResponseWriter, r *http.Request, err error) {
	noStore(w)
	httpx.WriteError(w, r, s.Logger, err)
}

func (s *Service) ok(w http.ResponseWriter, r *http.Request, v any) {
	noStore(w)
	httpx.WriteData(w, r, http.StatusOK, v)
}

func staff(r *http.Request) string { return authz.PrincipalFrom(r.Context()).UserID }

func limit(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	return n
}

type reasonBody struct {
	Reason       string `json:"reason"`
	AdjustmentID string `json:"adjustment_id"`
}

func (s *Service) hOverview(w http.ResponseWriter, r *http.Request) {
	v, err := s.Overview(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, v)
}

func (s *Service) hAccounts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, err := s.Accounts(r.Context(), AccountFilter{Class: q.Get("class"), OwnerType: q.Get("owner_type"), Status: q.Get("status"),
		Currency: q.Get("currency")}, limit(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, map[string]any{"items": items, "next_cursor": nil})
}

func (s *Service) hAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("account_id")
	a, err := s.Account(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	journals, next, err := s.Journals(r.Context(), JournalFilter{AccountID: id}, r.URL.Query().Get("cursor"), limit(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, map[string]any{"account": a, "activity": map[string]any{"items": journals, "next_cursor": next}})
}

func (s *Service) hAccountStatus(to string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in reasonBody
		if err := httpx.DecodeJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		id := r.PathValue("account_id")
		if err := s.SetAccountStatus(r.Context(), staff(r), id, to, in.Reason); err != nil {
			s.fail(w, r, err)
			return
		}
		s.hAccount(w, r)
	}
}

func (s *Service) hJournals(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, next, err := s.Journals(r.Context(), JournalFilter{Rule: q.Get("posting_rule"), SourceType: q.Get("source_type"),
		SourceID: q.Get("source_id"), SourceSystem: q.Get("source_system"), Currency: q.Get("currency"), AccountID: q.Get("account_id")},
		q.Get("cursor"), limit(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, map[string]any{"items": items, "next_cursor": next})
}

func (s *Service) hJournal(w http.ResponseWriter, r *http.Request) {
	v, err := s.Journal(r.Context(), r.PathValue("transaction_id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, v)
}

func (s *Service) hTrialBalance(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ccy, period := q.Get("currency"), q.Get("period_id")
	if ccy != "" && !currencyRE.MatchString(ccy) || period != "" && !uuidRE.MatchString(period) {
		s.fail(w, r, httpx.Validation(errs.Detail{Field: "currency", Code: "INVALID_FORMAT"}))
		return
	}
	v, err := s.TrialBalance(r.Context(), ccy, period)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, v)
}

func (s *Service) hPeriods(w http.ResponseWriter, r *http.Request) {
	v, err := s.Periods(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, map[string]any{"items": v, "next_cursor": nil})
}

func (s *Service) hPeriod(step string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in reasonBody
		if err := httpx.DecodeJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		id := r.PathValue("period_id")
		var err error
		switch step {
		case "request":
			err = s.RequestPeriodClose(r.Context(), staff(r), id, in.Reason)
		case "approve":
			err = s.ApprovePeriodClose(r.Context(), staff(r), id, in.Reason)
		case "abort":
			err = s.AbortPeriodClose(r.Context(), staff(r), id, in.Reason)
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.hPeriods(w, r)
	}
}

func (s *Service) hAdjustments(w http.ResponseWriter, r *http.Request) {
	v, err := s.Adjustments(r.Context(), r.URL.Query().Get("status"), PageLimit(limit(r)))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, map[string]any{"items": v, "next_cursor": nil})
}

func (s *Service) hRequestAdjustment(w http.ResponseWriter, r *http.Request) {
	var in AdjustmentRequest
	if err := httpx.DecodeJSON(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	v, err := s.RequestAdjustment(r.Context(), staff(r), in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, http.StatusCreated, v)
}

func (s *Service) hAdjustment(step string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in reasonBody
		if err := httpx.DecodeJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		id := r.PathValue("adjustment_id")
		var v Adjustment
		var err error
		switch step {
		case "approve":
			v, err = s.ApproveAdjustment(r.Context(), staff(r), id, in.Reason)
		case "reject":
			v, err = s.RejectAdjustment(r.Context(), staff(r), id, in.Reason)
		case "withdraw":
			v, err = s.WithdrawAdjustment(r.Context(), staff(r), id, in.Reason)
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.ok(w, r, v)
	}
}

func (s *Service) hReconciliation(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	v, err := s.ReconExceptions(r.Context(), q.Get("status"), q.Get("exception_type"), PageLimit(limit(r)))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, map[string]any{"items": v, "next_cursor": nil,
		"settlement_reconciliation": "NOT_PERFORMED: no provider settlement evidence (PCR-007, PCR-016; Stage 17)"})
}

func (s *Service) hReconcile(w http.ResponseWriter, r *http.Request) {
	v, err := s.Reconcile(r.Context(), "staff:"+staff(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, v)
}

func (s *Service) hRecon(step string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in reasonBody
		if err := httpx.DecodeJSON(r, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		id := r.PathValue("reference_id")
		var err error
		if step == "investigate" {
			err = s.InvestigateException(r.Context(), staff(r), id, in.Reason)
		} else {
			err = s.ResolveException(r.Context(), staff(r), id, in.Reason, in.AdjustmentID)
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.hReconciliation(w, r)
	}
}

func (s *Service) hExceptions(w http.ResponseWriter, r *http.Request) {
	integrity, err := s.LatestIntegrity(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	open, err := s.ReconExceptions(r.Context(), "OPEN", "", 100)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rejected, err := s.SourceReferences(r.Context(), "BLOCKED")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, map[string]any{"integrity": integrity, "open_reconciliation_exceptions": open, "blocked_sources": rejected})
}

func (s *Service) hIntegrity(w http.ResponseWriter, r *http.Request) {
	v, err := s.RunIntegrity(r.Context(), "STAFF", staff(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, v)
}

func (s *Service) hSources(w http.ResponseWriter, r *http.Request) {
	v, err := s.SourceReferences(r.Context(), r.URL.Query().Get("decision"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ok(w, r, map[string]any{"items": v, "next_cursor": nil})
}

func (s *Service) hFixture(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Scenario    string `json:"scenario"`
		RunID       string `json:"run_id"`
		CampaignID  string `json:"campaign_id"`  // fund_campaign only
		AmountMinor string `json:"amount_minor"` // fund_campaign only (digits)
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	var v FixtureRun
	var err error
	if in.Scenario == FixtureFundCampaign {
		n, perr := strconv.ParseInt(in.AmountMinor, 10, 64)
		if perr != nil || !amountRE.MatchString(in.AmountMinor) {
			s.fail(w, r, invalid("amount_minor", "INVALID_FORMAT"))
			return
		}
		v, err = s.FundCampaign(r.Context(), staff(r), in.CampaignID, n, in.RunID)
	} else {
		v, err = s.PostFixture(r.Context(), staff(r), in.Scenario, in.RunID)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	noStore(w)
	httpx.WriteData(w, r, http.StatusCreated, v)
}
