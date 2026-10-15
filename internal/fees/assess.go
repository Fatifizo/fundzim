package fees

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/ledger"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Fee assessments (ADR-044 §10). An assessment is a calculated obligation under the policy version used — not cash,
// not revenue, not settlement. One per (source, category): a duplicate event can never assess twice.

// Assessment source types.
const (
	SrcDonationPayment   = "DONATION_PAYMENT"
	SrcSyntheticDonation = "SYNTHETIC_DONATION"
	SrcPayout            = "PAYOUT"
)

// Accounting statuses.
const (
	AcctNotPostedTestPayment = "NOT_POSTED_TEST_PAYMENT"
	AcctPending              = "PENDING_POSTING"
	AcctPosted               = "POSTED"
	AcctNotApplicable        = "NOT_APPLICABLE"
)

// ConsumerPayments is the outbox consumer of payments.payment_succeeded.
const ConsumerPayments = "fees.payments"

// Assessment is an assessed fee.
type Assessment struct {
	ID                 string    `json:"id"`
	SourceType         string    `json:"source_type"`
	SourceID           string    `json:"source_id"`
	Category           string    `json:"category"`
	CampaignID         *string   `json:"campaign_id"`
	Currency           string    `json:"currency"`
	BasisMinor         string    `json:"basis_minor"`
	FeeMinor           *string   `json:"fee_minor"`
	Basis              string    `json:"basis"`
	Responsibility     *string   `json:"responsibility"`
	DonorPartMinor     *string   `json:"donor_part_minor"`
	OrganiserPartMinor *string   `json:"organiser_part_minor"`
	PlatformPartMinor  *string   `json:"platform_part_minor"`
	PolicyVersionID    *string   `json:"policy_version_id"`
	ProviderScheduleID *string   `json:"provider_schedule_id"`
	CalculationID      string    `json:"calculation_id"`
	QuoteID            *string   `json:"quote_id"`
	PaymentMethod      *string   `json:"payment_method"`
	ProviderID         *string   `json:"provider_id"`
	AccountingStatus   string    `json:"accounting_status"`
	TestPayment        bool      `json:"test_payment"`
	Synthetic          bool      `json:"synthetic"`
	FeeEnvironment     string    `json:"fee_environment"`
	AssessedAt         time.Time `json:"assessed_at"`
	// NetFeeMinor is the fee after APPLIED/APPROVED adjustments (nil when UNKNOWN).
	NetFeeMinor *string `json:"net_fee_minor"`
	Label       string  `json:"label"`
	fee         *int64
	basis       int64
	net         *int64
}

const assessmentCols = `a.id, a.source_type, a.source_id, a.category, a.campaign_id::text, a.currency, a.basis_minor, a.fee_minor, a.basis,
	a.responsibility, a.donor_part_minor, a.organiser_part_minor, a.platform_part_minor, a.policy_version_id::text, a.provider_schedule_id::text,
	a.calculation_id, a.quote_id::text, a.payment_method, a.provider_id::text, a.accounting_status, a.test_payment, a.synthetic, a.fee_environment,
	a.assessed_at, (SELECT a.fee_minor + coalesce(sum(CASE j.direction WHEN 'INCREASE' THEN j.amount_minor ELSE -j.amount_minor END), 0)
	  FROM app.fee_adjustments j WHERE j.assessment_id = a.id AND j.status IN ('APPLIED','APPROVED'))`

func scanAssessment(r pgx.Row) (Assessment, error) {
	var a Assessment
	var donor, org, plat *int64
	err := r.Scan(&a.ID, &a.SourceType, &a.SourceID, &a.Category, &a.CampaignID, &a.Currency, &a.basis, &a.fee, &a.Basis, &a.Responsibility,
		&donor, &org, &plat, &a.PolicyVersionID, &a.ProviderScheduleID, &a.CalculationID, &a.QuoteID, &a.PaymentMethod, &a.ProviderID,
		&a.AccountingStatus, &a.TestPayment, &a.Synthetic, &a.FeeEnvironment, &a.AssessedAt, &a.net)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	if err != nil {
		return a, err
	}
	a.BasisMinor, a.FeeMinor, a.NetFeeMinor = amt(a.basis), amtP(a.fee), amtP(a.net)
	a.DonorPartMinor, a.OrganiserPartMinor, a.PlatformPartMinor = amtP(donor), amtP(org), amtP(plat)
	a.Label = TestLabel
	return a, nil
}

// componentRow is a stored calculation component.
type componentRow struct {
	ID, Category, Basis                    string
	Amount, Donor, Organiser, Platform     *int64
	Responsibility, PolicyVersionID, Sched *string
	Rounding                               *string
}

func components(ctx context.Context, q querier, calcID string) ([]componentRow, error) {
	rows, err := q.Query(ctx, `SELECT id, category, basis, amount_minor, donor_part_minor, organiser_part_minor, platform_part_minor,
		responsibility, policy_version_id::text, provider_schedule_id::text, rounding_mode FROM app.fee_components WHERE calculation_id = $1 ORDER BY seq`, calcID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (componentRow, error) {
		var c componentRow
		err := r.Scan(&c.ID, &c.Category, &c.Basis, &c.Amount, &c.Donor, &c.Organiser, &c.Platform, &c.Responsibility, &c.PolicyVersionID,
			&c.Sched, &c.Rounding)
		return c, err
	})
}

// assessmentBasis: a policy-quoted fee is QUOTED; a provider estimate ESTIMATED; no evidence UNKNOWN.
func assessmentBasis(c componentRow) string {
	switch c.Basis {
	case BasisQuoted, BasisEstimated:
		return c.Basis
	}
	return BasisUnknown
}

type assessInput struct {
	SourceType, SourceID, CampaignID, Currency, CalcID, QuoteID, Method, ProviderID string
	Basis                                                                           int64
	Accounting                                                                      string
	// AccountingFor, when set, decides the accounting status per component (overrides Accounting).
	AccountingFor          func(componentRow) string
	TestPayment, Synthetic bool
}

// insertAssessments records one assessment per assessable component of a calculation (idempotent). A
// NOT_CONFIGURED platform fee charged nothing and is not assessed. It returns the assessment id per category.
func (s *Service) insertAssessments(ctx context.Context, tx pgx.Tx, in assessInput, actor Actor) (map[string]string, error) {
	cs, err := components(ctx, tx, in.CalcID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := map[string]string{}
	for _, c := range cs {
		if c.Basis == BasisNotConfigured {
			continue
		}
		id := ids.New()
		acct := in.Accounting
		if in.AccountingFor != nil {
			acct = in.AccountingFor(c)
		}
		var got string
		err := tx.QueryRow(ctx, `INSERT INTO app.fee_assessments (id, source_type, source_id, category, campaign_id, currency, basis_minor, fee_minor,
			basis, responsibility, donor_part_minor, organiser_part_minor, platform_part_minor, policy_version_id, provider_schedule_id,
			calculation_id, component_id, quote_id, payment_method, provider_id, accounting_status, test_payment, synthetic, assessed_at)
			VALUES ($1, $2, $3, $4, nullif($5, '')::uuid, $6, $7, $8, $9, $10, $11, $12, $13, $14::uuid, $15::uuid, $16, $17, nullif($18, '')::uuid,
			        nullif($19, ''), nullif($20, '')::uuid, $21, $22, $23, $24)
			ON CONFLICT ON CONSTRAINT uq_fee_assessments_source DO NOTHING RETURNING id`,
			id, in.SourceType, in.SourceID, c.Category, in.CampaignID, in.Currency, in.Basis, c.Amount, assessmentBasis(c), c.Responsibility,
			c.Donor, c.Organiser, c.Platform, c.PolicyVersionID, c.Sched, in.CalcID, c.ID, in.QuoteID, in.Method, in.ProviderID, acct,
			in.TestPayment, in.Synthetic, now).Scan(&got)
		if errors.Is(err, pgx.ErrNoRows) { // already assessed (duplicate delivery or replay)
			if err := tx.QueryRow(ctx, `SELECT id FROM app.fee_assessments WHERE source_type = $1 AND source_id = $2 AND category = $3`,
				in.SourceType, in.SourceID, c.Category).Scan(&got); err != nil {
				return nil, err
			}
			out[c.Category] = got
			continue
		}
		if err != nil {
			return nil, err
		}
		out[c.Category] = got
		if err := event(ctx, tx, "fee_assessment", got, "FEE_ASSESSED", actor, map[string]any{"source_type": in.SourceType,
			"source_id": in.SourceID, "category": c.Category, "basis": assessmentBasis(c), "accounting_status": acct}, now); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// RecordPaymentSucceeded assesses the fees of a succeeded payment from the quote bound to its donation intent
// (consumer fees.payments). Test payments are never posted (ADR-042): their assessments are records only. A
// non-test payment cannot exist in Stage 12; it is refused, never assessed or posted (fail closed).
func (s *Service) RecordPaymentSucceeded(ctx context.Context, raw []byte) error {
	var payload struct {
		PaymentID   string `json:"payment_id"`
		IntentID    string `json:"donation_intent_id"`
		TestPayment bool   `json:"test_payment"`
	}
	if err := jsonUnmarshal(raw, &payload); err != nil {
		s.Logger.Warn("fees: malformed payment_succeeded payload; not assessed")
		return nil
	}
	paymentID, intentID, test := payload.PaymentID, payload.IntentID, payload.TestPayment
	if !ids.Valid(paymentID) || !ids.Valid(intentID) {
		s.Logger.Warn("fees: payment_succeeded without payment or intent id; not assessed")
		return nil
	}
	if !test || ProductionFeesEnabled {
		s.Logger.Error("fees: a non-test payment reached the fee consumer; production fees are disabled, not assessed", "payment_id", paymentID)
		return nil
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var quoteID, campaignID, currency, calcID string
		var method, provider *string
		var amount int64
		err := tx.QueryRow(ctx, `SELECT id, campaign_id, currency, calculation_id, payment_method, provider_id::text, amount_minor
			FROM app.fee_quotes WHERE donation_intent_id = $1`, intentID).Scan(&quoteID, &campaignID, &currency, &calcID, &method, &provider, &amount)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // an intent created before Stage 12 has no quote: nothing to assess
		}
		if err != nil {
			return err
		}
		_, err = s.insertAssessments(ctx, tx, assessInput{SourceType: SrcDonationPayment, SourceID: paymentID, CampaignID: campaignID,
			Currency: currency, CalcID: calcID, QuoteID: quoteID, Method: deref(method), ProviderID: deref(provider), Basis: amount,
			Accounting: AcctNotPostedTestPayment, TestPayment: true}, systemActor)
		return err
	})
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// Assessment returns one assessment.
func (s *Service) Assessment(ctx context.Context, id string) (Assessment, error) {
	if !ids.Valid(id) {
		return Assessment{}, ErrNotFound
	}
	return scanAssessment(s.Pool.QueryRow(ctx, `SELECT `+assessmentCols+` FROM app.fee_assessments a WHERE a.id = $1`, id))
}

// AssessmentFilter filters assessment lists.
type AssessmentFilter struct {
	CampaignID, SourceType, Category, Currency string
	Limit                                      int
}

// Assessments lists assessments (newest first).
func (s *Service) Assessments(ctx context.Context, f AssessmentFilter) ([]Assessment, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+assessmentCols+` FROM app.fee_assessments a
		WHERE ($1 = '' OR a.campaign_id::text = $1) AND ($2 = '' OR a.source_type = $2) AND ($3 = '' OR a.category = $3) AND ($4 = '' OR a.currency = $4)
		ORDER BY a.assessed_at DESC, a.id DESC LIMIT $5`, f.CampaignID, f.SourceType, f.Category, f.Currency, f.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Assessment{}
	for rows.Next() {
		a, err := scanAssessment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---- payout fees (called by payouts) -----------------------------------------------------------------------------

// PayoutFeeRequest describes a payout for fee estimation.
type PayoutFeeRequest struct {
	CampaignID, CampaignCategory, Currency, Rail, ProviderID string
	AmountMinor                                              int64
}

// PayoutFeeEstimate is the payout fee captured at request time.
type PayoutFeeEstimate struct {
	CalculationID       string
	EstimateMinor       *int64 // nil: UNKNOWN (no provider fee evidence)
	Basis               string // ESTIMATED | UNKNOWN
	Responsibility      *string
	BeneficiaryReceives *int64
}

// EstimatePayoutFee computes and stores the payout fee estimate in the payouts transaction. The reservation is
// never changed by it: an organiser-borne fee is carved out of the reserved amount, a platform-absorbed fee is an
// expense (ADR-044 §13). A known organiser-borne fee that is not below the amount is refused (FEE_EXCEEDS_AMOUNT).
func (s *Service) EstimatePayoutFee(ctx context.Context, tx pgx.Tx, r PayoutFeeRequest) (PayoutFeeEstimate, error) {
	now := s.now()
	pv, err := s.Resolve(ctx, tx, Scope{Category: CatPayout, TransactionType: TxPayout, Currency: r.Currency, PaymentMethod: r.Rail,
		ProviderID: r.ProviderID, CampaignCategory: r.CampaignCategory, CampaignID: r.CampaignID, At: now})
	if err != nil {
		return PayoutFeeEstimate{}, err
	}
	sch, err := s.resolveSchedule(ctx, tx, r.ProviderID, FeeTypePayout, r.Currency, r.Rail, now)
	if err != nil {
		return PayoutFeeEstimate{}, err
	}
	var pol *PolicyInput
	var schedule *ScheduleInput
	if pv != nil {
		if pol, err = pv.input(); err != nil {
			return PayoutFeeEstimate{}, err
		}
	}
	if sch != nil {
		if schedule, err = sch.input(); err != nil {
			return PayoutFeeEstimate{}, err
		}
		if !sch.RoundingKnown {
			schedule.Terms.Rounding = ""
		}
	}
	in := NewInputs(TxPayout, r.Currency, r.AmountMinor, r.Rail, r.ProviderID, r.CampaignID, r.CampaignCategory, now.Format(time.RFC3339Nano),
		nil, pol, schedule)
	out, err := Compute(in)
	if err != nil {
		return PayoutFeeEstimate{}, calcError(err)
	}
	calcID, _, err := storeCalculation(ctx, tx, out, r.AmountMinor, now)
	if err != nil {
		return PayoutFeeEstimate{}, err
	}
	c := out.Payout.Fee
	e := PayoutFeeEstimate{CalculationID: calcID, EstimateMinor: c.Amount, Basis: BasisUnknown, BeneficiaryReceives: out.Payout.BeneficiaryReceives}
	if c.Amount != nil {
		e.Basis = BasisEstimated
	}
	if c.Responsibility != "" {
		resp := c.Responsibility
		e.Responsibility = &resp
	}
	return e, nil
}

// AssessPayoutFee records the payout fee of a COMPLETED TEST payout from its request-time calculation (idempotent).
// A platform-absorbed known fee is posted as a PAYOUT_FEE expense journal (TEST ledger, SIMULATED evidence). An
// organiser-borne fee is part of the reserved amount (no extra journal). An UNKNOWN fee is a record for
// reconciliation (UNRECONCILED_PROVIDER_FEE).
func (s *Service) AssessPayoutFee(ctx context.Context, payoutID, campaignID, currency, providerID, rail, calcID string, amount int64) error {
	if !ids.Valid(payoutID) || !ids.Valid(calcID) {
		return nil
	}
	var ids_ map[string]string
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		ids_, err = s.insertAssessments(ctx, tx, assessInput{SourceType: SrcPayout, SourceID: payoutID, CampaignID: campaignID, Currency: currency,
			CalcID: calcID, Method: rail, ProviderID: providerID, Basis: amount, AccountingFor: payoutAccounting}, systemActor)
		return err
	})
	if err != nil {
		return err
	}
	if id := ids_[CatPayout]; id != "" {
		if err := s.postPayoutFee(ctx, id, payoutID, providerID); err != nil {
			// the assessment stays PENDING_POSTING: the fees maintenance job retries; reconciliation reports it
			s.Logger.Warn("fees: payout fee journal deferred", "payout_request_id", payoutID, "err", err)
		}
	}
	return nil
}

// payoutAccounting: only a known platform-absorbed fee is journalled (PAYOUT_FEE). An organiser-borne fee is carved
// out of the reserved payout amount and an UNKNOWN fee cannot be posted: both need no fee journal.
func payoutAccounting(c componentRow) string {
	if c.Amount != nil && c.Platform != nil && *c.Platform > 0 {
		return AcctPending
	}
	return AcctNotApplicable
}

// postPayoutFee posts the PAYOUT_FEE journal of a platform-absorbed payout fee. Safe to repeat (deterministic key
// payout:{id}:fee).
func (s *Service) postPayoutFee(ctx context.Context, assessmentID, payoutID, providerID string) error {
	a, err := s.Assessment(ctx, assessmentID)
	if err != nil || a.AccountingStatus != AcctPending {
		return err
	}
	platformPart, err := parseAmt(deref(a.PlatformPartMinor))
	if err != nil {
		return err
	}
	if s.Ledger == nil {
		return ErrLedgerUnavailable
	}
	key := "payout:" + payoutID + ":fee"
	res, err := s.Ledger.PostOnce(ctx, ledger.Posting{Rule: "PAYOUT_FEE", IdempotencyKey: key, Currency: a.Currency, OccurredAt: s.now(),
		Description: "TEST payout fee (platform-absorbed, estimated from synthetic provider evidence)", SourceSystem: ledger.SourceFees,
		SourceType: "payout", SourceID: payoutID, SourceEventID: "fee_assessment:" + assessmentID, EvidenceClass: ledger.EvidenceSimulated,
		Actor: ledger.Actor{Type: "SYSTEM", ID: "fees.payouts"}, FeePolicyVersionID: deref(a.PolicyVersionID),
		Metadata: map[string]string{"fee_assessment_id": assessmentID, "label": "TEST"},
		Lines: []ledger.Line{ledger.Dr(ledger.AccountRef{Class: ledger.ClassPSPProcessingFees}, platformPart),
			ledger.Cr(ledger.AccountRef{Class: ledger.ClassPSPSettled, OwnerID: providerID}, platformPart)}})
	if err != nil {
		return err // stays PENDING_POSTING; the fee job retries
	}
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := insertAccountingRef(ctx, tx, assessmentID, "", res.TransactionID, key, "PAYOUT_FEE", "PAYOUT_FEE", a.Currency, platformPart,
			deref(a.PolicyVersionID)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app.fee_assessments SET accounting_status = 'POSTED' WHERE id = $1 AND accounting_status = 'PENDING_POSTING'`,
			assessmentID); err != nil {
			return err
		}
		return event(ctx, tx, "fee_assessment", assessmentID, "FEE_POSTED", systemActor, map[string]any{"journal_key": key,
			"ledger_transaction_id": res.TransactionID}, s.now())
	})
}

// insertAccountingRef links a fee subject to the ledger journal that carries it (idempotent per journal key).
func insertAccountingRef(ctx context.Context, tx pgx.Tx, assessmentID, adjustmentID, txID, key, rule, purpose, currency string, fee int64,
	policyVersionID string) error {
	_, err := tx.Exec(ctx, `INSERT INTO app.fee_accounting_references (id, assessment_id, adjustment_id, ledger_transaction_id, journal_key,
		posting_rule, purpose, currency, fee_amount_minor, policy_version_id)
		VALUES ($1, nullif($2, '')::uuid, nullif($3, '')::uuid, $4, $5, $6, $7, $8, $9, nullif($10, '')::uuid)
		ON CONFLICT ON CONSTRAINT uq_fee_accounting_references_key DO NOTHING`,
		ids.New(), assessmentID, adjustmentID, txID, key, rule, purpose, currency, fee, policyVersionID)
	return err
}

// PostPending retries the fee journals of PENDING_POSTING payout assessments (job).
func (s *Service) PostPending(ctx context.Context) (int, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, source_id::text, coalesce(provider_id::text, '') FROM app.fee_assessments
		WHERE accounting_status = 'PENDING_POSTING' AND source_type = 'PAYOUT' ORDER BY created_at LIMIT 100`)
	if err != nil {
		return 0, err
	}
	type pend struct{ id, payout, provider string }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (pend, error) {
		var p pend
		return p, r.Scan(&p.id, &p.payout, &p.provider)
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, p := range list {
		if err := s.postPayoutFee(ctx, p.id, p.payout, p.provider); err != nil {
			s.Logger.Warn("fees: payout fee posting deferred", "assessment_id", p.id, "err", err)
			continue
		}
		n++
	}
	return n, nil
}
