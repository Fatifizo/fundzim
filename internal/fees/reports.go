package fees

import (
	"context"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/ledger"
)

// Revenue reporting (brief §42–§43). Restricted (fee.view). Every figure is per currency — currencies are never
// combined — and labelled TEST: quotes are not revenue, assessments are not revenue, cash received is not available
// (no settlement evidence), and recognised revenue is only what the TEST ledger holds (synthetic fixtures).

// CurrencyRevenue is one currency's figures.
type CurrencyRevenue struct {
	Currency                    string `json:"currency"`
	QuotedPlatformFeesMinor     string `json:"quoted_platform_fees_minor"`
	Quotes                      int64  `json:"quotes"`
	AssessedPlatformFeesMinor   string `json:"assessed_platform_fees_minor"`
	PlatformFeeAssessments      int64  `json:"platform_fee_assessments"`
	AssessedOnTestPaymentsMinor string `json:"assessed_on_test_payments_minor"`
	AssessedOnSyntheticMinor    string `json:"assessed_on_synthetic_donations_minor"`
	RefundedPlatformFeesMinor   string `json:"refunded_platform_fees_minor"`
	ChargebackReversedMinor     string `json:"chargeback_reversed_platform_fees_minor"`
	ManualAdjustmentsNetMinor   string `json:"manual_adjustments_net_minor"`
	ProviderFeesEstimatedMinor  string `json:"provider_fees_estimated_minor"`
	ProviderFeesUnknown         int64  `json:"provider_fees_unknown"`
	PayoutFeesMinor             string `json:"payout_fees_minor"`
	ChargebackFeeExposureMinor  string `json:"chargeback_fee_exposure_minor"`
	RecognisedRevenueMinor      string `json:"recognised_revenue_minor"`
	RevenueReversalsMinor       string `json:"revenue_reversals_minor"`
	ProviderFeeExpenseMinor     string `json:"provider_fee_expense_minor"`
	NetRevenueMinor             string `json:"net_revenue_minor"`
	CashReceived                string `json:"cash_received"`
}

// PeriodRevenue is the ledger's fee movements of one currency and month.
type PeriodRevenue struct {
	Currency                string `json:"currency"`
	Month                   string `json:"month"`
	RecognisedRevenueMinor  string `json:"recognised_revenue_minor"`
	RevenueReversalsMinor   string `json:"revenue_reversals_minor"`
	ProviderFeeExpenseMinor string `json:"provider_fee_expense_minor"`
	Journals                int64  `json:"journals"`
}

// GroupRevenue is assessed platform fees per currency and payment method or provider.
type GroupRevenue struct {
	Currency                  string `json:"currency"`
	Key                       string `json:"key"`
	AssessedPlatformFeesMinor string `json:"assessed_platform_fees_minor"`
	Assessments               int64  `json:"assessments"`
}

// RevenueReport is the restricted fee and revenue report.
type RevenueReport struct {
	FeeEnvironment string            `json:"fee_environment"`
	Label          string            `json:"label"`
	From           time.Time         `json:"from"`
	To             time.Time         `json:"to"`
	Currencies     []CurrencyRevenue `json:"currencies"`
	ByPeriod       []PeriodRevenue   `json:"by_period"`
	ByMethod       []GroupRevenue    `json:"by_payment_method"`
	ByProvider     []GroupRevenue    `json:"by_provider"`
	Notes          []string          `json:"notes"`
}

type sums struct {
	quoted, assessed, assessedTest, assessedSyn, refunded, chargeback, manual, provEst, payout, cbExposure int64
	quotes, assessedN, provUnknown                                                                         int64
	revenue, reversals, expense                                                                            int64
}

// Revenue builds the report for [from, to).
func (s *Service) Revenue(ctx context.Context, from, to time.Time) (RevenueReport, error) {
	r := RevenueReport{FeeEnvironment: EnvTest, Label: "TEST — synthetic data, not FundZim's financial performance", From: from.UTC(), To: to.UTC(),
		Notes: []string{
			"Quoted fees are not charges and not revenue.",
			"Assessed fees on test payments are records only: test payments are never posted (ADR-042).",
			"Recognised revenue is what the TEST ledger holds from TEST_ONLY synthetic fixtures; the recognition trigger is ACCOUNTING_REVIEW_REQUIRED.",
			"Cash received is not available: there is no settlement evidence (PCR-007).",
			"Currencies are never combined: there is no approved FX policy.",
		}}
	by := map[string]*sums{}
	get := func(c string) *sums {
		if by[c] == nil {
			by[c] = &sums{}
		}
		return by[c]
	}
	scan := func(q string, f func(rows pgx.Rows) error, args ...any) error {
		rows, err := s.Pool.Query(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			if err := f(rows); err != nil {
				return err
			}
		}
		return rows.Err()
	}
	if err := scan(`SELECT currency, coalesce(sum(platform_fee_minor), 0), count(*) FROM app.fee_quotes WHERE created_at >= $1 AND created_at < $2
		GROUP BY currency`, func(rows pgx.Rows) error {
		var c string
		var v, n int64
		if err := rows.Scan(&c, &v, &n); err != nil {
			return err
		}
		get(c).quoted, get(c).quotes = v, n
		return nil
	}, from, to); err != nil {
		return r, err
	}
	if err := scan(`SELECT currency, category, synthetic, basis, coalesce(sum(fee_minor), 0), count(*), count(*) FILTER (WHERE fee_minor IS NULL)
		FROM app.fee_assessments WHERE assessed_at >= $1 AND assessed_at < $2 GROUP BY 1, 2, 3, 4`, func(rows pgx.Rows) error {
		var c, cat, basis string
		var syn bool
		var v, n, unknown int64
		if err := rows.Scan(&c, &cat, &syn, &basis, &v, &n, &unknown); err != nil {
			return err
		}
		x := get(c)
		switch cat {
		case CatPlatform:
			x.assessed += v
			x.assessedN += n
			if syn {
				x.assessedSyn += v
			} else {
				x.assessedTest += v
			}
		case CatProcessing:
			x.provEst += v
			x.provUnknown += unknown
		case CatPayout:
			x.payout += v
		case CatChargeback:
			x.cbExposure += v
		}
		return nil
	}, from, to); err != nil {
		return r, err
	}
	if err := scan(`SELECT j.currency, j.reason, coalesce(sum(CASE j.direction WHEN 'INCREASE' THEN j.amount_minor ELSE -j.amount_minor END), 0)
		FROM app.fee_adjustments j JOIN app.fee_assessments a ON a.id = j.assessment_id
		WHERE j.status IN ('APPLIED','APPROVED') AND a.category = 'PLATFORM_FEE' AND j.created_at >= $1 AND j.created_at < $2 GROUP BY 1, 2`,
		func(rows pgx.Rows) error {
			var c, reasonCode string
			var v int64
			if err := rows.Scan(&c, &reasonCode, &v); err != nil {
				return err
			}
			x := get(c)
			switch reasonCode {
			case AdjRefund:
				x.refunded += -v
			case AdjChargeback:
				x.chargeback += -v
			default:
				x.manual += v
			}
			return nil
		}, from, to); err != nil {
		return r, err
	}
	periods := map[string]*PeriodRevenue{}
	periodSums := map[string]*sums{}
	if s.Ledger != nil {
		moves, err := s.Ledger.FeeMovements(ctx, from, to)
		if err != nil {
			return r, err
		}
		for _, m := range moves {
			if m.Environment != ledger.EnvTest { // a PRODUCTION ledger cannot exist in Stage 12; never mixed in
				continue
			}
			x := get(m.Currency)
			k := m.Currency + "|" + m.Month.Format("2006-01")
			if periodSums[k] == nil {
				periodSums[k] = &sums{}
				periods[k] = &PeriodRevenue{Currency: m.Currency, Month: m.Month.Format("2006-01")}
			}
			ps := periodSums[k]
			periods[k].Journals += m.Journals
			switch m.Class {
			case ledger.ClassPlatformFees:
				x.revenue += m.CreditMinor
				x.reversals += m.DebitMinor
				ps.revenue += m.CreditMinor
				ps.reversals += m.DebitMinor
			case ledger.ClassPSPProcessingFees:
				x.expense += m.DebitMinor - m.CreditMinor
				ps.expense += m.DebitMinor - m.CreditMinor
			}
		}
	}
	for c, x := range by {
		r.Currencies = append(r.Currencies, CurrencyRevenue{Currency: c, QuotedPlatformFeesMinor: amt(x.quoted), Quotes: x.quotes,
			AssessedPlatformFeesMinor: amt(x.assessed), PlatformFeeAssessments: x.assessedN, AssessedOnTestPaymentsMinor: amt(x.assessedTest),
			AssessedOnSyntheticMinor: amt(x.assessedSyn), RefundedPlatformFeesMinor: amt(x.refunded), ChargebackReversedMinor: amt(x.chargeback),
			ManualAdjustmentsNetMinor: signed(x.manual), ProviderFeesEstimatedMinor: amt(x.provEst), ProviderFeesUnknown: x.provUnknown,
			PayoutFeesMinor: amt(x.payout), ChargebackFeeExposureMinor: amt(x.cbExposure), RecognisedRevenueMinor: amt(x.revenue),
			RevenueReversalsMinor: amt(x.reversals), ProviderFeeExpenseMinor: signed(x.expense),
			NetRevenueMinor: signed(x.revenue - x.reversals - x.expense), CashReceived: "NOT_AVAILABLE"})
	}
	sort.Slice(r.Currencies, func(i, j int) bool { return r.Currencies[i].Currency < r.Currencies[j].Currency })
	for k, p := range periods {
		ps := periodSums[k]
		p.RecognisedRevenueMinor, p.RevenueReversalsMinor, p.ProviderFeeExpenseMinor = amt(ps.revenue), amt(ps.reversals), signed(ps.expense)
		r.ByPeriod = append(r.ByPeriod, *p)
	}
	sort.Slice(r.ByPeriod, func(i, j int) bool {
		return r.ByPeriod[i].Currency+r.ByPeriod[i].Month < r.ByPeriod[j].Currency+r.ByPeriod[j].Month
	})
	group := func(col string) ([]GroupRevenue, error) {
		out := []GroupRevenue{}
		err := scan(`SELECT currency, coalesce(`+col+`, 'NONE'), coalesce(sum(fee_minor), 0), count(*) FROM app.fee_assessments
			WHERE category = 'PLATFORM_FEE' AND assessed_at >= $1 AND assessed_at < $2 GROUP BY 1, 2 ORDER BY 1, 2`, func(rows pgx.Rows) error {
			var g GroupRevenue
			var v int64
			if err := rows.Scan(&g.Currency, &g.Key, &v, &g.Assessments); err != nil {
				return err
			}
			g.AssessedPlatformFeesMinor = amt(v)
			out = append(out, g)
			return nil
		}, from, to)
		return out, err
	}
	var err error
	if r.ByMethod, err = group("payment_method"); err != nil {
		return r, err
	}
	if r.ByProvider, err = group("provider_id::text"); err != nil {
		return r, err
	}
	if r.Currencies == nil {
		r.Currencies = []CurrencyRevenue{}
	}
	if r.ByPeriod == nil {
		r.ByPeriod = []PeriodRevenue{}
	}
	return r, nil
}

// signed formats a possibly negative amount (net figures).
func signed(v int64) string {
	if v < 0 {
		return "-" + amt(-v)
	}
	return amt(v)
}

// Overview is the staff fee dashboard summary.
type Overview struct {
	FeeEnvironment       string         `json:"fee_environment"`
	ProductionFees       string         `json:"production_fees"`
	Label                string         `json:"label"`
	PolicyVersions       map[string]int `json:"policy_versions_by_status"`
	ProviderSchedules    map[string]int `json:"provider_schedules_by_status"`
	ActiveQuotes         int            `json:"active_quotes"`
	Assessments          int            `json:"assessments"`
	PendingAdjustments   int            `json:"pending_adjustments"`
	OpenReconciliation   int            `json:"open_reconciliation_items"`
	LatestReconciliation *ReconRun      `json:"latest_reconciliation"`
	Notes                []string       `json:"notes"`
}

// StaffOverview returns the fee dashboard summary.
func (s *Service) StaffOverview(ctx context.Context) (Overview, error) {
	o := Overview{FeeEnvironment: EnvTest, ProductionFees: "BLOCKED", Label: TestLabel, PolicyVersions: map[string]int{}, ProviderSchedules: map[string]int{},
		Notes: []string{
			"No fee model is decided (PD-01, LR-028); tax treatment is open (LR-016, LR-045, LR-059): no fee is charged to anyone.",
			"No payment provider is selected (ADR-040): provider fees are SYNTHETIC or UNVERIFIED estimates, never verified.",
			"Revenue recognition needs an accounting review (ACCOUNTING_REVIEW_REQUIRED).",
		}}
	count := func(q string, m map[string]int) error {
		rows, err := s.Pool.Query(ctx, q)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k string
			var n int
			if err := rows.Scan(&k, &n); err != nil {
				return err
			}
			m[k] = n
		}
		return rows.Err()
	}
	if err := count(`SELECT status, count(*) FROM app.fee_policy_versions GROUP BY 1`, o.PolicyVersions); err != nil {
		return o, err
	}
	if err := count(`SELECT status, count(*) FROM app.fee_provider_schedules GROUP BY 1`, o.ProviderSchedules); err != nil {
		return o, err
	}
	if err := s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM app.fee_quotes WHERE status = 'ACTIVE' AND expires_at > $1),
		(SELECT count(*) FROM app.fee_assessments), (SELECT count(*) FROM app.fee_adjustments WHERE status = 'PENDING_APPROVAL'),
		(SELECT count(*) FROM app.fee_reconciliation_references WHERE status = 'OPEN')`, s.now()).
		Scan(&o.ActiveQuotes, &o.Assessments, &o.PendingAdjustments, &o.OpenReconciliation); err != nil {
		return o, err
	}
	var err error
	o.LatestReconciliation, err = s.LatestRun(ctx)
	return o, err
}

// CampaignFees is the organiser's fee view of one campaign (disclosure, ADR-044; brief §47). It carries no staff
// identities, staff notes, decision reasons or internal references (projections below).
type CampaignFees struct {
	CampaignID     string                `json:"campaign_id"`
	FeeEnvironment string                `json:"fee_environment"`
	Label          string                `json:"label"`
	Policies       []ActivePolicy        `json:"policies"`
	Assessments    []OrganiserAssessment `json:"assessments"`
	Adjustments    []OrganiserAdjustment `json:"adjustments"`
	Restrictions   []string              `json:"restrictions"`
}

// OrganiserAssessment is an assessed fee as its campaign's organiser sees it.
type OrganiserAssessment struct {
	ID                 string    `json:"id"`
	SourceType         string    `json:"source_type"`
	Category           string    `json:"category"`
	Currency           string    `json:"currency"`
	BasisMinor         string    `json:"basis_minor"`
	FeeMinor           *string   `json:"fee_minor"`
	Basis              string    `json:"basis"`
	Responsibility     *string   `json:"responsibility"`
	DonorPartMinor     *string   `json:"donor_part_minor"`
	OrganiserPartMinor *string   `json:"organiser_part_minor"`
	PlatformPartMinor  *string   `json:"platform_part_minor"`
	NetFeeMinor        *string   `json:"net_fee_minor"`
	AccountingStatus   string    `json:"accounting_status"`
	TestPayment        bool      `json:"test_payment"`
	Synthetic          bool      `json:"synthetic"`
	FeeEnvironment     string    `json:"fee_environment"`
	AssessedAt         time.Time `json:"assessed_at"`
	Label              string    `json:"label"`
}

// OrganiserAdjustment is a fee adjustment as the organiser sees it: no staff identities, notes or decision reasons.
type OrganiserAdjustment struct {
	ID           string    `json:"id"`
	AssessmentID string    `json:"assessment_id"`
	Currency     string    `json:"currency"`
	Reason       string    `json:"reason"`
	Direction    string    `json:"direction"`
	AmountMinor  string    `json:"amount_minor"`
	BasisMinor   *string   `json:"basis_minor"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
}

// OrganiserFees returns the fee policies that would apply to a campaign and its assessed fees (owner or manager only).
func (s *Service) OrganiserFees(ctx context.Context, userID, campaignID string) (CampaignFees, error) {
	ok, err := s.Campaigns.CanManage(ctx, userID, campaignID)
	if err != nil {
		return CampaignFees{}, err
	}
	if !ok {
		return CampaignFees{}, ErrCampaignNotFound
	}
	sc, err := s.Campaigns.FeeScope(ctx, campaignID)
	if err != nil {
		return CampaignFees{}, err
	}
	out := CampaignFees{CampaignID: campaignID, FeeEnvironment: EnvTest, Label: TestLabel, Policies: []ActivePolicy{},
		Restrictions: []string{
			"Donations are not yet available: no fee is charged and nothing here is a real donation.",
			"Provider fees are estimates from synthetic or unverified evidence, or UNKNOWN.",
			"FundZim does not convert currencies; fees are in the donation's currency.",
		}}
	now := s.now()
	currencies := []string{"USD"}
	if s.UsableCurrencies != nil {
		if currencies, err = s.UsableCurrencies(ctx); err != nil {
			return out, err
		}
	}
	for _, cat := range []string{CatPlatform, CatProcessing, CatPayout} {
		tx := map[string]string{CatPlatform: TxDonation, CatProcessing: TxDonation, CatPayout: TxPayout}[cat]
		for _, ccy := range currencies {
			v, err := s.Resolve(ctx, nil, Scope{Category: cat, TransactionType: tx, Currency: ccy, CampaignCategory: sc.CategoryCode, CampaignID: sc.ID, At: now})
			if err != nil {
				return out, err
			}
			if v == nil {
				continue
			}
			p, err := scanPolicy(s.Pool.QueryRow(ctx, `SELECT `+policyCols+` FROM app.fee_policies WHERE id = $1`, v.PolicyID))
			if err != nil {
				return out, err
			}
			out.Policies = append(out.Policies, ActivePolicy{VersionID: v.ID, PolicyID: p.ID, Name: p.Name, Category: p.Category,
				TransactionType: p.TransactionType, Currency: p.Currency, Scope: scopeLabel(p), Terms: v.Terms, Responsibility: v.Responsibility,
				DonorShareBps: v.DonorShareBps, RefundTreatment: v.RefundTreatment, EffectiveFrom: v.EffectiveFrom, FeeEnvironment: v.FeeEnvironment,
				Label: TestLabel})
		}
	}
	as, err := s.Assessments(ctx, AssessmentFilter{CampaignID: campaignID, Limit: 100})
	if err != nil {
		return out, err
	}
	out.Assessments = make([]OrganiserAssessment, 0, len(as))
	for _, a := range as {
		out.Assessments = append(out.Assessments, OrganiserAssessment{ID: a.ID, SourceType: a.SourceType, Category: a.Category, Currency: a.Currency,
			BasisMinor: a.BasisMinor, FeeMinor: a.FeeMinor, Basis: a.Basis, Responsibility: a.Responsibility, DonorPartMinor: a.DonorPartMinor,
			OrganiserPartMinor: a.OrganiserPartMinor, PlatformPartMinor: a.PlatformPartMinor, NetFeeMinor: a.NetFeeMinor,
			AccountingStatus: a.AccountingStatus, TestPayment: a.TestPayment, Synthetic: a.Synthetic, FeeEnvironment: a.FeeEnvironment,
			AssessedAt: a.AssessedAt, Label: a.Label})
	}
	adj, err := s.Adjustments(ctx, AdjustmentFilter{CampaignID: campaignID, Limit: 100})
	if err != nil {
		return out, err
	}
	out.Adjustments = make([]OrganiserAdjustment, 0, len(adj))
	for _, j := range adj {
		if j.Status == AdjPending || j.Status == AdjRejected {
			continue // staff-internal until decided; a rejected request changed nothing
		}
		out.Adjustments = append(out.Adjustments, OrganiserAdjustment{ID: j.ID, AssessmentID: j.AssessmentID, Currency: j.Currency, Reason: j.Reason,
			Direction: j.Direction, AmountMinor: j.AmountMinor, BasisMinor: j.BasisMinor, Status: j.Status, CreatedAt: j.CreatedAt})
	}
	return out, nil
}
