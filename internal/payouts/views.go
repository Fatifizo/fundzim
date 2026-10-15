package payouts

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/campaigns"
	"github.com/Fatifizo/fundzim/internal/ledger"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Read models. Owners see their payouts with owner-safe fields only (no approver identities, no risk indicators,
// no compliance detail — LR-008); staff with payout.view see everything.

// OwnerView is a payout request as its campaign owner sees it.
type OwnerView struct {
	ID                string     `json:"id"`
	CampaignID        string     `json:"campaign_id"`
	Currency          string     `json:"currency"`
	AmountMinor       int64      `json:"amount_minor,string"`
	Purpose           *string    `json:"purpose"`
	Status            string     `json:"status"`
	StatusLabel       string     `json:"status_label"`
	DestinationMasked string     `json:"destination_masked"`
	DestinationRail   string     `json:"destination_rail"`
	ReasonCategory    *string    `json:"reason_category"`
	TestPayout        bool       `json:"test_payout"`
	FundsLabel        string     `json:"funds_label"`
	RequestedAt       time.Time  `json:"requested_at"`
	SubmittedAt       *time.Time `json:"submitted_at"`
	CompletedAt       *time.Time `json:"completed_at"`
	CanCancel         bool       `json:"can_cancel"`
	Version           int        `json:"version"`
	// Stage 12: the payout fee estimate (nil amount: UNKNOWN — no provider fee evidence) and who bears it.
	FeeEstimateMinor    *int64  `json:"fee_estimate_minor,string"`
	FeeBasis            *string `json:"fee_basis"`
	FeeResponsibility   *string `json:"fee_responsibility"`
	BeneficiaryReceives *int64  `json:"beneficiary_receives_minor,string"`
}

var ownerLabels = map[string]string{StRequested: "Requested", StReview: "Under review", StApproved: "Approved", StSubmitted: "Processing",
	StProcessing: "Processing", StUnknown: "Processing — we're confirming with the provider", StCompleted: "Paid (test)",
	StFailed: "Failed — funds returned to your balance", StRejected: "Rejected", StCancelled: "Cancelled",
	StReversed: "Returned — please check your payout details"}

// Owner projects a payout for its owner.
func (p Payout) Owner() OwnerView {
	v := OwnerView{ID: p.ID, CampaignID: p.CampaignID, Currency: p.Currency, AmountMinor: p.AmountMinor, Purpose: p.Purpose, Status: p.Status,
		StatusLabel: ownerLabels[p.Status], DestinationMasked: p.DestinationMasked, DestinationRail: p.DestinationRail, TestPayout: p.TestPayout,
		RequestedAt: p.RequestedAt, SubmittedAt: p.SubmittedAt, CompletedAt: p.CompletedAt, CanCancel: p.Status == StReview || p.Status == StApproved,
		Version: p.Version, FundsLabel: "TEST_ONLY", FeeEstimateMinor: p.FeeEstimateMinor, FeeBasis: p.FeeBasis, FeeResponsibility: p.FeeResponsibility}
	switch {
	case p.FeeResponsibility != nil && *p.FeeResponsibility == "ORGANISER_COVERED" && p.FeeEstimateMinor != nil:
		r := p.AmountMinor - *p.FeeEstimateMinor
		v.BeneficiaryReceives = &r
	case p.FeeResponsibility != nil && *p.FeeResponsibility == "PLATFORM_ABSORBED":
		r := p.AmountMinor
		v.BeneficiaryReceives = &r
	}
	if p.Status == StRejected || p.Status == StCancelled || p.Status == StFailed {
		c := "NOT_AVAILABLE"
		switch {
		case p.Status == StFailed:
			c = "PROVIDER_FAILED"
		case p.DecisionReasonCode != nil && (*p.DecisionReasonCode == "OWNER_CANCELLED" || *p.DecisionReasonCode == "INSUFFICIENT_FUNDS" ||
			*p.DecisionReasonCode == "REQUEST_EXPIRED" || *p.DecisionReasonCode == "VERIFICATION_REQUIRED" ||
			*p.DecisionReasonCode == "DESTINATION_NOT_READY" || *p.DecisionReasonCode == "CAMPAIGN_NOT_ELIGIBLE"):
			c = *p.DecisionReasonCode
		}
		v.ReasonCategory = &c
	}
	return v
}

// OwnerPayout returns a payout if the actor may see it (campaign owner or any member of the owning organisation);
// anything else is not-found.
func (s *Service) OwnerPayout(ctx context.Context, actorID, id string) (Payout, error) {
	p, err := s.payout(ctx, s.Pool, id, false)
	if err != nil {
		return Payout{}, err
	}
	if _, err := s.Campaigns.OwnerAccess(ctx, actorID, p.CampaignID, false); err != nil {
		if errors.Is(err, campaigns.ErrNotFound) {
			return Payout{}, ErrPayoutNotFound
		}
		return Payout{}, err
	}
	return p, nil
}

// OwnerList lists the payouts of the campaigns the actor can see (one campaign when campaignID is set).
func (s *Service) OwnerList(ctx context.Context, actorID, campaignID string, limit int) ([]OwnerView, error) {
	if campaignID != "" {
		if _, err := s.Campaigns.OwnerAccess(ctx, actorID, campaignID, false); err != nil {
			return nil, err
		}
		return s.ownerQuery(ctx, `WHERE p.campaign_id = $1 ORDER BY p.created_at DESC, p.id DESC LIMIT $2`, campaignID, limit)
	}
	orgs, err := s.Orgs.OrganisationsOf(ctx, actorID) // any member may read the organisation's payouts
	if err != nil {
		return nil, err
	}
	if orgs == nil {
		orgs = []string{}
	}
	return s.ownerQuery(ctx, `WHERE p.owner_user_id = $1 OR p.owner_organisation_id = ANY($3::uuid[]) OR p.requested_by = $1
		ORDER BY p.created_at DESC, p.id DESC LIMIT $2`, actorID, limit, orgs)
}

func (s *Service) ownerQuery(ctx context.Context, where string, args ...any) ([]OwnerView, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+payoutCols+payoutFrom+` `+where, args...)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Payout, error) { return scanPayout(r) })
	if err != nil {
		return nil, err
	}
	out := make([]OwnerView, 0, len(list))
	for _, p := range list {
		out = append(out, p.Owner())
	}
	return out, nil
}

// TimelineItem is an owner-safe event.
type TimelineItem struct {
	At     time.Time `json:"at"`
	Status string    `json:"status"`
	Label  string    `json:"label"`
}

// OwnerTimeline lists the payout's applied status changes for its owner.
func (s *Service) OwnerTimeline(ctx context.Context, actorID, id string) ([]TimelineItem, error) {
	if _, err := s.OwnerPayout(ctx, actorID, id); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT recorded_at, to_status FROM app.payout_events WHERE payout_request_id = $1 AND applied
		AND to_status IS NOT NULL ORDER BY recorded_at, id`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (TimelineItem, error) {
		var t TimelineItem
		err := r.Scan(&t.At, &t.Status)
		t.Label = ownerLabels[t.Status]
		return t, err
	})
}

// Availability is the owner's view of what can be withdrawn (TEST_ONLY in Stage 11).
type Availability struct {
	CampaignID           string        `json:"campaign_id"`
	Currency             string        `json:"currency"`
	Environment          string        `json:"environment"`
	FundsLabel           string        `json:"funds_label"`
	SettledReleasedMinor int64         `json:"settled_released_minor,string"`
	RefundExposureMinor  int64         `json:"refund_exposure_minor,string"`
	ReservedMinor        int64         `json:"reserved_minor,string"`
	AvailableMinor       int64         `json:"available_minor,string"`
	PayoutsEnabled       bool          `json:"payouts_enabled"`
	ProductionStatus     string        `json:"production_status"`
	Blockers             []string      `json:"blockers"`
	Checks               []CheckResult `json:"checks,omitempty"`
}

// ownerSafe masks checks that could tip off a subject (risk indicators, compliance).
func ownerSafe(rs []CheckResult) []CheckResult {
	out := make([]CheckResult, 0, len(rs))
	for _, r := range rs {
		if r.CheckID == "EC-13" || r.CheckID == "EC-16" {
			if r.Result != Pass && r.Result != NA {
				r.Detail = "NOT_AVAILABLE"
			} else {
				r.Detail = ""
			}
		}
		out = append(out, r)
	}
	return out
}

// Availability is a dry run of the request-time engine for the owner (nothing is recorded): the amounts, and the
// blockers by owner-safe category. Production availability is always BLOCKED in Stage 11.
func (s *Service) Availability(ctx context.Context, actorID, campaignID, currency, destinationID string, amount int64) (Availability, error) {
	if !ccyRE.MatchString(currency) {
		currency = "USD"
	}
	acc, err := s.Campaigns.OwnerAccess(ctx, actorID, campaignID, false)
	if err != nil {
		return Availability{}, err
	}
	env, err := s.Ledger.Environment(ctx)
	if err != nil {
		return Availability{}, err
	}
	bal, err := s.Ledger.CampaignBalances(ctx, acc.CampaignID, currency)
	if err != nil {
		return Availability{}, err
	}
	exp, err := s.Refunds.OpenRefundExposure(ctx, acc.CampaignID, currency)
	if err != nil {
		return Availability{}, err
	}
	a := Availability{CampaignID: acc.CampaignID, Currency: currency, Environment: env, FundsLabel: "TEST_ONLY",
		SettledReleasedMinor: bal[ledger.ClassCampaignPayable].BalanceMinor, RefundExposureMinor: exp,
		ReservedMinor:  bal[ledger.ClassCampaignPayoutPend].BalanceMinor + bal[ledger.ClassPayoutInTransit].BalanceMinor,
		PayoutsEnabled: s.ExecEnv == ExecLocalSimulator, ProductionStatus: "BLOCKED", Blockers: []string{}}
	a.AvailableMinor = a.SettledReleasedMinor - exp
	if a.AvailableMinor < 0 {
		a.AvailableMinor = 0
	}
	if !a.PayoutsEnabled {
		a.Blockers = append(a.Blockers, "PAYOUTS_UNAVAILABLE")
	}
	if destinationID != "" && ids.Valid(destinationID) {
		if amount <= 0 {
			amount = 1
		}
		f, _, _, err := s.gather(ctx, PhaseRequest, subject{CampaignID: acc.CampaignID, RequesterID: actorID, DestinationID: destinationID,
			Currency: currency, Amount: amount})
		if err != nil {
			if errors.Is(err, campaigns.ErrNotFound) {
				return a, nil // read access only: the requester cannot request (not the owner or an ORG_ADMIN)
			}
			return Availability{}, err
		}
		d := Evaluate(f)
		a.Checks = ownerSafe(d.Results)
		if d.Outcome == OutcomeRejected {
			a.Blockers = append(a.Blockers, d.ReasonCode)
		}
	}
	return a, nil
}

// ---- staff --------------------------------------------------------------------------------------------------------

// StaffFilter filters the staff list.
type StaffFilter struct {
	Status, CampaignID  string
	NeedsReconciliation bool
}

// ListPayouts lists payouts for staff (keyset on created_at, id).
func (s *Service) ListPayouts(ctx context.Context, f StaffFilter, cursor string, limit int) ([]Payout, *string, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	where, args := []string{"true"}, []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+strconv.Itoa(len(args))))
	}
	if f.Status != "" {
		add("p.status = ?", f.Status)
	}
	if f.CampaignID != "" && ids.Valid(f.CampaignID) {
		add("p.campaign_id = ?", f.CampaignID)
	}
	if f.NeedsReconciliation {
		where = append(where, "p.needs_reconciliation")
	}
	if at, id, ok := parseCursor(cursor); ok {
		args = append(args, at, id)
		where = append(where, "(p.created_at, p.id) < ($"+strconv.Itoa(len(args)-1)+", $"+strconv.Itoa(len(args))+")")
	}
	args = append(args, limit+1)
	rows, err := s.Pool.Query(ctx, `SELECT `+payoutCols+payoutFrom+` WHERE `+strings.Join(where, " AND ")+
		` ORDER BY p.created_at DESC, p.id DESC LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Payout, error) { return scanPayout(r) })
	if err != nil {
		return nil, nil, err
	}
	var next *string
	if len(list) > limit {
		list = list[:limit]
		c := list[limit-1].CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + list[limit-1].ID
		next = &c
	}
	return list, next, nil
}

func parseCursor(c string) (time.Time, string, bool) {
	at, id, ok := strings.Cut(c, "|")
	if !ok || !ids.Valid(id) {
		return time.Time{}, "", false
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	return t, id, err == nil
}

// Detail is the staff view of one payout: everything recorded about it.
type Detail struct {
	Payout     Payout           `json:"payout"`
	Approvals  []map[string]any `json:"approvals"`
	Attempts   []map[string]any `json:"attempts"`
	Events     []map[string]any `json:"events"`
	Decisions  []map[string]any `json:"eligibility_decisions"`
	Exceptions []Exception      `json:"exceptions"`
	Journals   []JournalLink    `json:"ledger_journals"`
}

// JournalLink is a payout journal in the ledger.
type JournalLink struct {
	Key           string  `json:"idempotency_key"`
	TransactionID *string `json:"transaction_id"`
}

// StaffDetail returns the staff view.
func (s *Service) StaffDetail(ctx context.Context, id string) (Detail, error) {
	p, err := s.payout(ctx, s.Pool, id, false)
	if err != nil {
		return Detail{}, err
	}
	d := Detail{Payout: p}
	q := func(sql string) ([]map[string]any, error) {
		rows, err := s.Pool.Query(ctx, `SELECT to_jsonb(x) FROM (`+sql+`) x`, id)
		if err != nil {
			return nil, err
		}
		out := []map[string]any{}
		for rows.Next() {
			var b []byte
			if err := rows.Scan(&b); err != nil {
				rows.Close()
				return nil, err
			}
			m := map[string]any{}
			_ = json.Unmarshal(b, &m)
			out = append(out, m)
		}
		rows.Close()
		return out, rows.Err()
	}
	if d.Approvals, err = q(`SELECT id, approval_round, approver_id, approver_role, decision, reason, step_up_at, decided_at, expires_at
		FROM app.payout_approvals WHERE payout_request_id = $1 ORDER BY decided_at`); err != nil {
		return d, err
	}
	if d.Attempts, err = q(`SELECT id, attempt_no, operation, classification, unknown_reason, http_status, provider_status_raw,
		provider_reference_returned, failure_code, latency_ms, started_at, completed_at FROM app.payout_attempts WHERE payout_request_id = $1
		ORDER BY attempt_no`); err != nil {
		return d, err
	}
	if d.Events, err = q(`SELECT id, event_type, from_status, to_status, applied, not_applied_reason, transition_code, source, provider_event_id,
		raw_status, actor_type, actor_id, reason, ledger_transaction_ids, eligibility_decision_id, payout_attempt_id, recorded_at
		FROM app.payout_events WHERE payout_request_id = $1 ORDER BY recorded_at, id`); err != nil {
		return d, err
	}
	if d.Decisions, err = q(`SELECT id, phase, outcome, approval_tier, engine_version, results, inputs, evaluated_at, encode(record_hash, 'hex') AS record_hash
		FROM app.payout_eligibility_decisions WHERE payout_request_id = $1 ORDER BY created_at`); err != nil {
		return d, err
	}
	if d.Exceptions, err = s.Exceptions(ctx, ExceptionFilter{PayoutID: id, All: true}); err != nil {
		return d, err
	}
	for _, step := range []string{"reserved", "submitted", "completed", "failed", "released", "reversed"} {
		key := "payout:" + id + ":" + step
		j, ok, err := s.Ledger.JournalByKey(ctx, key)
		if err != nil {
			return d, err
		}
		jl := JournalLink{Key: key}
		if ok {
			jl.TransactionID = &j.TransactionID
		}
		d.Journals = append(d.Journals, jl)
	}
	return d, nil
}

// Reservations lists reservations (staff).
func (s *Service) Reservations(ctx context.Context, status string, limit int) ([]Reservation, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, payout_request_id, campaign_id, currency, amount_minor, account_scope, status, ledger_reserve_txn_id,
		ledger_submit_txn_id, ledger_release_txn_id, ledger_consume_txn_id, expires_at, created_at, updated_at FROM app.payout_reservations
		WHERE ($1 = '' OR status = $1) ORDER BY created_at DESC, id DESC LIMIT $2`, status, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Reservation, error) {
		var x Reservation
		err := r.Scan(&x.ID, &x.PayoutID, &x.CampaignID, &x.Currency, &x.AmountMinor, &x.AccountScope, &x.Status, &x.LedgerReserveTxnID,
			&x.LedgerSubmitTxnID, &x.LedgerReleaseTxnID, &x.LedgerConsumeTxnID, &x.ExpiresAt, &x.CreatedAt, &x.UpdatedAt)
		return x, err
	})
}

// Overview summarises payouts for the operations dashboard.
type Overview struct {
	ExecutionEnvironment     string            `json:"execution_environment"`
	LiveExecution            bool              `json:"live_execution"`
	ProductionStatus         string            `json:"production_status"`
	LedgerEnvironment        string            `json:"ledger_environment"`
	ByStatus                 map[string]int    `json:"by_status"`
	OpenExceptions           map[string]int    `json:"open_exceptions_by_severity"`
	ActiveReservedMinor      map[string]string `json:"active_reserved_minor_by_currency"`
	SettlementReconciliation string            `json:"settlement_reconciliation"`
}

// StaffOverview returns the overview.
func (s *Service) StaffOverview(ctx context.Context) (Overview, error) {
	o := Overview{ExecutionEnvironment: s.ExecEnv, LiveExecution: LiveExecutionEnabled, ProductionStatus: "BLOCKED", ByStatus: map[string]int{},
		OpenExceptions: map[string]int{}, ActiveReservedMinor: map[string]string{},
		SettlementReconciliation: "NOT_PERFORMED: no provider settlement or disbursement statements (PCR-007, PCR-016)"}
	env, err := s.Ledger.Environment(ctx)
	if err != nil {
		return o, err
	}
	o.LedgerEnvironment = env
	for _, q := range []struct {
		sql string
		m   map[string]int
	}{{`SELECT status, count(*) FROM app.payout_requests GROUP BY status`, o.ByStatus},
		{`SELECT severity, count(*) FROM app.payout_exceptions WHERE status <> 'RESOLVED' GROUP BY severity`, o.OpenExceptions}} {
		rows, err := s.Pool.Query(ctx, q.sql)
		if err != nil {
			return o, err
		}
		for rows.Next() {
			var k string
			var n int
			if err := rows.Scan(&k, &n); err != nil {
				rows.Close()
				return o, err
			}
			q.m[k] = n
		}
		rows.Close()
	}
	rows, err := s.Pool.Query(ctx, `SELECT currency, sum(amount_minor)::text FROM app.payout_reservations WHERE status IN ('ACTIVE','BLOCKED') GROUP BY currency`)
	if err != nil {
		return o, err
	}
	defer rows.Close()
	for rows.Next() {
		var c, v string
		if err := rows.Scan(&c, &v); err != nil {
			return o, err
		}
		o.ActiveReservedMinor[c] = v
	}
	return o, rows.Err()
}
