package payouts

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/Fatifizo/fundzim/internal/campaigns"
	"github.com/Fatifizo/fundzim/internal/ledger"
	"github.com/Fatifizo/fundzim/internal/platform/money"
	"github.com/Fatifizo/fundzim/internal/psp"
	"github.com/Fatifizo/fundzim/internal/risk"
)

// RefundExposure reads the open refund exposure of a campaign (implemented over payments in internal/app: payouts
// and payments never import each other, design-baseline §3).
type RefundExposure interface {
	OpenRefundExposure(ctx context.Context, campaignID, currency string) (int64, error)
}

// Limit keys (risk.limits). Values are never assumed: an unconfigured limit is REVIEW and the DUAL tier.
const (
	LimitMinAmount            = "payout.min_amount"
	LimitMaxPerTransaction    = "payout.max_per_transaction"
	LimitDualApprovalMin      = "payout.dual_approval_min"
	LimitVelocityCount7d      = "payout.velocity.count_per_7d"
	LimitVelocityAmount7d     = "payout.velocity.amount_per_7d"
	LimitApprovalValidity     = "payout.approval_validity"
	LimitRequestValidity      = "payout.request_validity"
	LimitRiskDestChangeWindow = "payout.risk.destination_change_window"
	LimitRiskFailed30d        = "payout.risk.failed_payouts_30d"
	LimitRiskDestinations30d  = "payout.risk.destinations_30d"
)

// subject is what a request or a payout is about.
type subject struct {
	CampaignID, RequesterID, DestinationID, Currency string
	Amount                                           int64
	PayoutID                                         string // "" at REQUEST
	SnapshotVersion                                  int
	ApprovalPolicy                                   string
	ApprovalRound                                    int
}

// gather collects the facts for one evaluation through the owning modules' public interfaces.
func (s *Service) gather(ctx context.Context, phase string, sub subject) (Facts, campaigns.Access, *psp.PayoutRoute, error) {
	now := s.now()
	f := Facts{Phase: phase, At: now, Amount: sub.Amount, Currency: sub.Currency, TestMode: true,
		ExecutionEnvironment: s.ExecEnv, Indicators: []string{}}

	// campaign: the requester must be its owner or an ORG_ADMIN (others get not-found: existence is not revealed)
	var acc campaigns.Access
	var err error
	if phase == PhaseRequest {
		acc, err = s.Campaigns.OwnerAccess(ctx, sub.RequesterID, sub.CampaignID, true)
		if err != nil {
			return f, acc, nil, err
		}
		f.RequesterAuthorised = true
	} else {
		acc, err = s.Campaigns.Load(ctx, sub.CampaignID)
		if err != nil {
			return f, acc, nil, err
		}
		if _, aerr := s.Campaigns.OwnerAccess(ctx, sub.RequesterID, sub.CampaignID, true); aerr == nil {
			f.RequesterAuthorised = true
		} else if !errors.Is(aerr, campaigns.ErrNotFound) {
			return f, acc, nil, aerr
		}
	}
	f.CampaignStatus = acc.Status
	if lvl, err := s.Campaigns.Restricted(ctx, acc); err != nil {
		return f, acc, nil, err // fail closed
	} else {
		f.RestrictionLevel = lvl
	}

	// parties' verification (levels and statuses only, never documents)
	rq, err := s.KYC.Level(ctx, sub.RequesterID)
	if err != nil {
		return f, acc, nil, err
	}
	f.RequesterStatus, f.RequesterLevel = rq.Status, rq.Level
	if acc.OwnerUserID != nil {
		f.OwnerKind = "USER"
		ow, err := s.KYC.Level(ctx, *acc.OwnerUserID)
		if err != nil {
			return f, acc, nil, err
		}
		f.OwnerLevel, f.OwnerStatus = ow.Level, ow.Status
	} else {
		f.OwnerKind = "ORGANISATION"
		lv, err := s.KYC.OrganisationLevels(ctx, []string{*acc.OwnerOrgID})
		if err != nil {
			return f, acc, nil, err
		}
		st := lv[*acc.OwnerOrgID]
		f.OwnerLevel, f.OwnerStatus = st.Level, st.Status
		if f.OwnerStatus == "" {
			f.OwnerStatus = "ACTIVE"
		}
	}
	if b, err := s.Campaigns.CurrentBeneficiary(ctx, sub.CampaignID); err != nil {
		return f, acc, nil, err
	} else if b != nil {
		f.BeneficiaryID = b
		f.BeneficiaryType, f.BeneficiaryStatus, err = s.Benefs.StatusOf(ctx, *b)
		if err != nil {
			return f, acc, nil, err
		}
	}

	// destination
	d, derr := s.load(ctx, s.Pool, sub.DestinationID, false)
	switch {
	case errors.Is(derr, ErrNotFound):
	case derr != nil:
		return f, acc, nil, derr
	default:
		f.DestinationFound = true
		f.DestinationStatus, f.DestinationOwnership, f.DestinationCompliance = d.Status, d.Checks.Ownership, d.Checks.Compliance
		f.DestinationCurrency, f.DestinationPayeeType, f.DestinationBeneficiaryID = d.Currency, d.Payee.Type, d.Payee.BeneficiaryID
		f.DestinationOwnedByCampaign = (d.ownerUser != nil && acc.OwnerUserID != nil && *d.ownerUser == *acc.OwnerUserID) ||
			(d.ownerOrg != nil && acc.OwnerOrgID != nil && *d.ownerOrg == *acc.OwnerOrgID)
		cool := d.CoolingOffUntil
		f.DestinationCoolingOffUntil, f.DestinationExpiresAt, f.DestinationVersion = &cool, d.ExpiresAt, d.detailsVersion
	}
	f.SnapshotVersion = sub.SnapshotVersion

	// environment, route and provider
	env, err := s.Ledger.Environment(ctx)
	if err != nil {
		return f, acc, nil, err
	}
	f.LedgerEnvironment = env
	usable, err := money.UsableCurrencies(ctx, s.Pool) // enabled with verified minor units (ZWG is not: LR-043)
	if err != nil {
		return f, acc, nil, err
	}
	f.CurrencyPostable = slices.Contains(usable, sub.Currency)
	var route *psp.PayoutRoute
	if s.ExecEnv == ExecLocalSimulator && f.DestinationFound && !LiveExecutionEnabled {
		r, rerr := s.PSP.RoutePayout(ctx, d.Rail, sub.Currency, true)
		switch {
		case errors.Is(rerr, psp.ErrNoPayoutRoute):
		case rerr != nil:
			return f, acc, nil, rerr
		default:
			route = &r
			f.RouteFound, f.RouteCapabilityID = true, r.Capability.ID
			if pp, ok := s.PSP.PayoutAdapter(r.Provider.Code); ok {
				if lim, ok := pp.PayoutLimits(d.Rail, sub.Currency); ok {
					mn, mx := lim.MinMinor, lim.MaxMinor
					f.ProviderMinMinor, f.ProviderMaxMinor = &mn, &mx
				}
			}
			h, herr := s.PSP.ProviderHealth(ctx, r.Provider)
			if herr != nil {
				return f, acc, nil, herr
			}
			f.ProviderAvailable = !h.OutageDeclared && h.Circuit != "OPEN"
		}
	}

	// money: settled-and-released funds, open refund exposure, the reservation
	bal, err := s.Ledger.CampaignBalances(ctx, sub.CampaignID, sub.Currency)
	if err != nil {
		return f, acc, nil, err
	}
	if a, ok := bal[ledger.ClassCampaignPayable]; ok {
		f.PayableMinor, f.PayableAccountStatus = a.BalanceMinor, a.Status
	}
	f.PendingMinor = bal[ledger.ClassCampaignPayoutPend].BalanceMinor
	if s.Refunds != nil {
		if f.RefundExposureMinor, err = s.Refunds.OpenRefundExposure(ctx, sub.CampaignID, sub.Currency); err != nil {
			return f, acc, nil, err
		}
	} else {
		return f, acc, nil, errors.New("payouts: refund exposure reader not configured") // fail closed
	}
	if sub.PayoutID != "" {
		var st string
		if err := s.Pool.QueryRow(ctx, `SELECT coalesce((SELECT status FROM app.payout_reservations WHERE payout_request_id = $1), '')`,
			sub.PayoutID).Scan(&st); err != nil {
			return f, acc, nil, err
		}
		f.ReservationActive = st == ResActive
	}

	// limits (most restrictive of every configured type; none ⇒ unconfigured)
	if f.MinAmount, err = s.moneyLimit(ctx, LimitMinAmount, sub.Currency, now, true); err != nil {
		return f, acc, nil, err
	}
	if f.MaxPerTransaction, err = s.moneyLimit(ctx, LimitMaxPerTransaction, sub.Currency, now, false); err != nil {
		return f, acc, nil, err
	}
	if f.DualApprovalMin, err = s.moneyLimit(ctx, LimitDualApprovalMin, sub.Currency, now, false); err != nil {
		return f, acc, nil, err
	}
	if f.VelocityAmount7d, err = s.moneyLimit(ctx, LimitVelocityAmount7d, sub.Currency, now, false); err != nil {
		return f, acc, nil, err
	}
	if f.VelocityCount7d, err = s.valueLimit(ctx, LimitVelocityCount7d, "COUNT", now); err != nil {
		return f, acc, nil, err
	}

	// history: in flight, velocity, risk indicators, first organisation payout
	if err := s.Pool.QueryRow(ctx, `SELECT
		  EXISTS (SELECT 1 FROM app.payout_requests WHERE campaign_id = $1 AND currency = $2 AND id IS DISTINCT FROM $3::uuid
		           AND status IN ('PAYOUT_REQUESTED','PENDING_REVIEW','APPROVED','SUBMITTED','PROCESSING','UNKNOWN')),
		  (SELECT count(*) FROM app.payout_requests WHERE campaign_id = $1 AND currency = $2 AND id IS DISTINCT FROM $3::uuid
		    AND status NOT IN ('REJECTED','CANCELLED') AND requested_at > $4),
		  (SELECT coalesce(sum(amount_minor), 0) FROM app.payout_requests WHERE campaign_id = $1 AND currency = $2 AND id IS DISTINCT FROM $3::uuid
		    AND status NOT IN ('REJECTED','CANCELLED') AND requested_at > $4),
		  NOT EXISTS (SELECT 1 FROM app.payout_requests WHERE campaign_id = $1 AND status = 'COMPLETED')`,
		sub.CampaignID, sub.Currency, nilIfEmpty(sub.PayoutID), now.Add(-7*24*time.Hour)).
		Scan(&f.OtherInFlight, &f.RecentPayoutCount7d, &f.RecentPayoutAmount7d, &f.FirstOrgPayout); err != nil {
		return f, acc, nil, err
	}
	f.FirstOrgPayout = f.FirstOrgPayout && f.OwnerKind == "ORGANISATION"
	if err := s.indicators(ctx, &f, sub, d, derr == nil, now); err != nil {
		return f, acc, nil, err
	}
	if err := s.riskFacts(ctx, &f, sub, acc); err != nil {
		return f, acc, nil, err
	}

	// ledger integrity (EC-19)
	if f.LedgerIntegrityOK, err = s.Ledger.IntegrityConfirmed(ctx); err != nil {
		return f, acc, nil, err
	}

	// approvals (PRE_SUBMIT)
	if phase == PhasePreSubmit {
		f.RequiredApprovals = 1
		if sub.ApprovalPolicy == "DUAL" {
			f.RequiredApprovals = 2
		}
		if err := s.Pool.QueryRow(ctx, `SELECT count(DISTINCT approver_id) FILTER (WHERE decision = 'APPROVE'),
			count(*) FILTER (WHERE decision = 'REJECT') FROM app.payout_approvals
			WHERE payout_request_id = $1 AND approval_round = $2 AND (expires_at IS NULL OR expires_at > $3)`,
			sub.PayoutID, sub.ApprovalRound, now).Scan(&f.ValidApprovals, &f.RejectingApprovals); err != nil {
			return f, acc, nil, err
		}
		var codes []string
		if err := s.Pool.QueryRow(ctx, `SELECT coalesce((SELECT ARRAY(SELECT (e->>'check_id') || ':' || (e->>'detail_code')
			FROM jsonb_array_elements(d.results) e WHERE e->>'result' IN ('REVIEW','DEFER'))
			FROM app.payout_eligibility_decisions d WHERE d.payout_request_id = $1 AND d.phase <> 'PRE_SUBMIT'
			ORDER BY d.created_at DESC, d.id DESC LIMIT 1), '{}')`, sub.PayoutID).Scan(&codes); err != nil {
			return f, acc, nil, err
		}
		f.AcknowledgedReviews = codes
	}
	return f, acc, route, nil
}

// riskFacts reads the risk module's hold registry, screening status and classification (Stage 13; fail closed: an
// error stops the evaluation).
func (s *Service) riskFacts(ctx context.Context, f *Facts, sub subject, acc campaigns.Access) error {
	f.RiskHolds = []string{}
	if s.Risk == nil {
		return nil
	}
	owner, org, benef := "", "", ""
	if acc.OwnerUserID != nil {
		owner = *acc.OwnerUserID
	}
	if acc.OwnerOrgID != nil {
		org = *acc.OwnerOrgID
	}
	if f.BeneficiaryID != nil {
		benef = *f.BeneficiaryID
	}
	hs, err := s.Risk.WithdrawalHolds(ctx, risk.WithdrawalScope{CampaignID: sub.CampaignID, OwnerUserID: owner, OwnerOrgID: org,
		DestinationID: sub.DestinationID, BeneficiaryID: benef})
	if err != nil {
		return err
	}
	f.RiskHolds = risk.HoldTypes(hs)
	if f.ScreeningStatus, err = s.Risk.MostSignificantScreening(ctx, risk.HoldSubject{Type: "USER", ID: owner},
		risk.HoldSubject{Type: "ORGANISATION", ID: org}, risk.HoldSubject{Type: "BENEFICIARY", ID: benef}); err != nil {
		return err
	}
	class, err := s.Risk.HighestClassification(ctx, risk.HoldSubject{Type: "USER", ID: owner}, risk.HoldSubject{Type: "ORGANISATION", ID: org},
		risk.HoldSubject{Type: "CAMPAIGN", ID: sub.CampaignID}, risk.HoldSubject{Type: "PAYOUT_DESTINATION", ID: sub.DestinationID},
		risk.HoldSubject{Type: "USER", ID: sub.RequesterID})
	if err != nil {
		return err
	}
	if class == risk.ClassHigh || class == risk.ClassCritical {
		f.Indicators = append(f.Indicators, "RISK_CLASSIFICATION_"+class)
	}
	return nil
}

// indicators computes the minimal Stage 11 risk rules (EC-16). A rule whose threshold is not configured is itself
// an indicator (fail closed: review and DUAL approval).
func (s *Service) indicators(ctx context.Context, f *Facts, sub subject, d Destination, found bool, now time.Time) error {
	win, err := s.valueLimit(ctx, LimitRiskDestChangeWindow, "DURATION", now)
	if err != nil {
		return err
	}
	switch {
	case !win.Configured:
		f.Indicators = append(f.Indicators, "RULE_UNCONFIGURED:"+LimitRiskDestChangeWindow)
	case found && now.Sub(d.UpdatedAt) < time.Duration(win.Value)*time.Second:
		f.Indicators = append(f.Indicators, "RECENT_DESTINATION_CHANGE")
	}
	var failed, dests int64
	if err := s.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM app.payout_requests WHERE campaign_id = $1 AND status IN ('FAILED','REJECTED','REVERSED') AND requested_at > $2),
		(SELECT count(DISTINCT destination_id) FROM app.payout_requests WHERE campaign_id = $1 AND requested_at > $2 AND destination_id <> $3)`,
		sub.CampaignID, now.Add(-30*24*time.Hour), sub.DestinationID).Scan(&failed, &dests); err != nil {
		return err
	}
	for _, r := range []struct {
		key, name string
		observed  int64
	}{{LimitRiskFailed30d, "REPEATED_FAILED_PAYOUTS", failed}, {LimitRiskDestinations30d, "MULTIPLE_DESTINATIONS", dests + 1}} {
		l, err := s.valueLimit(ctx, r.key, "COUNT", now)
		if err != nil {
			return err
		}
		switch {
		case !l.Configured:
			f.Indicators = append(f.Indicators, "RULE_UNCONFIGURED:"+r.key)
		case r.observed >= l.Value:
			f.Indicators = append(f.Indicators, r.name)
		}
	}
	return nil
}

// moneyLimit combines the configured MONEY limits for a key: the highest minimum or the lowest maximum.
func (s *Service) moneyLimit(ctx context.Context, key, currency string, at time.Time, isMinimum bool) (Limit, error) {
	ls, err := s.Risk.MoneyLimits(ctx, key, currency, at)
	if err != nil || len(ls) == 0 {
		return Limit{LimitIDs: []string{}}, err
	}
	out := Limit{Configured: true, Value: ls[0].ValueMinor, LimitIDs: []string{}}
	for _, l := range ls {
		out.LimitIDs = append(out.LimitIDs, l.ID)
		if (isMinimum && l.ValueMinor > out.Value) || (!isMinimum && l.ValueMinor < out.Value) {
			out.Value = l.ValueMinor
		}
	}
	return out, nil
}

// valueLimit combines DURATION/COUNT limits: the lowest value is the most restrictive.
func (s *Service) valueLimit(ctx context.Context, key, kind string, at time.Time) (Limit, error) {
	ls, err := s.Risk.ValueLimits(ctx, key, kind, at)
	if err != nil || len(ls) == 0 {
		return Limit{LimitIDs: []string{}}, err
	}
	out := Limit{Configured: true, Value: ls[0].Value, LimitIDs: []string{}}
	for _, l := range ls {
		out.LimitIDs = append(out.LimitIDs, l.ID)
		if l.Value < out.Value {
			out.Value = l.Value
		}
	}
	return out, nil
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
