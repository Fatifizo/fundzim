package fees

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/ledger"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// TEST_ONLY fee accounting fixtures (ADR-044 §10–§12). Real test payments are never posted (ADR-042), so the fee
// journals — capture with the platform-fee line, provider fee, settlement and release, refunds and chargebacks with
// their fee reversals — are demonstrated on synthetic donations: TEST ledger, SyntheticEnabled, SIMULATED evidence,
// source_system 'fees', labelled TEST_ONLY. They are never donations and never real revenue. Every step has a
// deterministic key, so a fixture run can be replayed safely.

// Fixture scenarios.
const (
	FixtureDonation   = "donation"
	FixtureRefund     = "refund"
	FixtureChargeback = "chargeback"
	FixtureReport     = "provider_fee_report"
)

// FixtureRequest runs one fixture scenario.
type FixtureRequest struct {
	Scenario         string  `json:"scenario"`
	RunID            *string `json:"run_id"`
	CampaignID       string  `json:"campaign_id"`
	AmountMinor      string  `json:"amount_minor"`
	Currency         string  `json:"currency"`
	PaymentMethod    *string `json:"payment_method"`
	Settle           bool    `json:"settle"`
	DonationID       string  `json:"donation_id"`
	ReportedFeeMinor string  `json:"reported_fee_minor"`
	// ReversalID optionally names a refund or chargeback (a UUID): repeating it replays the same reversal.
	ReversalID *string `json:"reversal_id"`
}

// FixtureJournal is a journal a fixture posted (or found already posted).
type FixtureJournal struct {
	Key           string `json:"key"`
	Rule          string `json:"rule"`
	TransactionID string `json:"transaction_id"`
}

// FixtureResult is what a fixture did.
type FixtureResult struct {
	Scenario            string           `json:"scenario"`
	RunID               string           `json:"run_id"`
	SyntheticDonationID string           `json:"synthetic_donation_id"`
	Label               string           `json:"label"`
	Journals            []FixtureJournal `json:"journals"`
	Assessments         []Assessment     `json:"assessments"`
	Adjustments         []Adjustment     `json:"adjustments"`
	Breakdown           *ResultDoc       `json:"breakdown,omitempty"`
	Exposure            map[string]any   `json:"exposure,omitempty"`
}

// seedID derives a deterministic UUID-formatted id from a seed (fixture replays).
func seedID(seed string) string {
	h := sha256.Sum256([]byte("fees:" + seed))
	h[6] = (h[6] & 0x0f) | 0x50 // version 5-style marker
	h[8] = (h[8] & 0x3f) | 0x80
	x := hex.EncodeToString(h[:16])
	return x[0:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:32]
}

// RunFixture runs a TEST_ONLY fee fixture (fee.fixtures, ADMIN).
func (s *Service) RunFixture(ctx context.Context, staffID string, in FixtureRequest) (FixtureResult, error) {
	if s.Ledger == nil {
		return FixtureResult{}, ErrLedgerUnavailable
	}
	if ok, err := s.Ledger.SyntheticAllowed(ctx); err != nil {
		return FixtureResult{}, err
	} else if !ok || ProductionFeesEnabled {
		return FixtureResult{}, ErrSyntheticDisabled
	}
	runID := ids.New()
	if in.RunID != nil {
		if !ids.Valid(*in.RunID) {
			return FixtureResult{}, validation("run_id", "INVALID_FORMAT")
		}
		runID = *in.RunID
	}
	var res FixtureResult
	var err error
	switch in.Scenario {
	case FixtureDonation:
		res, err = s.fixtureDonation(ctx, staffID, runID, in)
	case FixtureRefund, FixtureChargeback:
		res, err = s.fixtureReversal(ctx, staffID, in)
	case FixtureReport:
		res, err = s.fixtureReport(ctx, staffID, in)
	default:
		return FixtureResult{}, validation("scenario", "INVALID_VALUE")
	}
	if err != nil {
		return res, err
	}
	res.Scenario, res.Label = in.Scenario, "TEST_ONLY"
	if res.RunID == "" {
		res.RunID = runID
	}
	_ = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return audit.Record(ctx, tx, audit.Event{Action: "fee.fixture.run", TargetType: "fee_fixture", TargetID: res.SyntheticDonationID,
			Metadata: map[string]any{"scenario": in.Scenario, "label": "TEST_ONLY", "journals": len(res.Journals)}})
	})
	return res, nil
}

func (s *Service) post(ctx context.Context, staffID string, key, rule, srcType, srcID, ccy, desc, policyVersionID string, lines []ledger.Line) (FixtureJournal, error) {
	actor := ledger.Actor{Type: "STAFF", ID: staffID}
	if staffID == "" {
		actor = ledger.Actor{Type: "SYSTEM", ID: "fees.fixture"}
	}
	r, err := s.Ledger.PostOnce(ctx, ledger.Posting{Rule: rule, IdempotencyKey: key, Currency: ccy, OccurredAt: s.now(),
		Description: "TEST_ONLY synthetic fee fixture / " + desc, SourceSystem: ledger.SourceFees, SourceType: srcType, SourceID: srcID,
		SourceEventID: key, EvidenceClass: ledger.EvidenceSimulated, Actor: actor, FeePolicyVersionID: policyVersionID,
		Metadata: map[string]string{"fixture": "TEST_ONLY", "label": "synthetic fee fixture"}, Lines: lines})
	if err != nil {
		return FixtureJournal{}, err
	}
	return FixtureJournal{Key: key, Rule: rule, TransactionID: r.TransactionID}, nil
}

// nonZero drops zero-amount lines (a journal line is never zero).
func nonZero(lines ...ledger.Line) []ledger.Line {
	out := lines[:0]
	for _, l := range lines {
		if l.Amount > 0 {
			out = append(out, l)
		}
	}
	return out
}

func pv(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// fixtureDonation: a synthetic donation with the fees of the policies in force, posted as capture (+ platform-fee
// line), provider fee and — with settle — settlement and release, so the campaign's TEST_ONLY payable reflects
// the allocation after fees.
func (s *Service) fixtureDonation(ctx context.Context, staffID, runID string, in FixtureRequest) (FixtureResult, error) {
	amount, err := parseAmount(in.AmountMinor)
	if err != nil {
		return FixtureResult{}, err
	}
	if in.Currency == "" {
		in.Currency = "USD"
	}
	if err := s.currencyUsable(ctx, in.Currency); err != nil {
		return FixtureResult{}, err
	}
	if !ids.Valid(in.CampaignID) {
		return FixtureResult{}, validation("campaign_id", "INVALID_FORMAT")
	}
	sc, err := s.Campaigns.FeeScope(ctx, in.CampaignID)
	if err != nil {
		return FixtureResult{}, err
	}
	method, provider, err := s.routeMethod(ctx, in.PaymentMethod, in.Currency, amount)
	if err != nil {
		return FixtureResult{}, err
	}
	donation := seedID("synthetic_donation:" + runID)
	res := FixtureResult{RunID: runID, SyntheticDonationID: donation}
	// replay: the assessments of this donation already exist ⇒ reuse their calculation
	var calcID string
	err = s.Pool.QueryRow(ctx, `SELECT calculation_id FROM app.fee_assessments WHERE source_type = 'SYNTHETIC_DONATION' AND source_id = $1 LIMIT 1`,
		donation).Scan(&calcID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return res, err
	}
	var out Outcome
	if calcID == "" {
		inputs, err := s.donationInputs(ctx, nil, sc, in.Currency, method, provider, amount, s.now())
		if err != nil {
			return res, err
		}
		if out, err = Compute(inputs); err != nil {
			return res, calcError(err)
		}
	} else {
		var raw []byte
		if err := s.Pool.QueryRow(ctx, `SELECT inputs FROM app.fee_calculations WHERE id = $1`, calcID).Scan(&raw); err != nil {
			return res, err
		}
		var inputs Inputs
		if err := jsonUnmarshal(raw, &inputs); err != nil {
			return res, err
		}
		if out, err = Compute(inputs); err != nil {
			return res, calcError(err)
		}
	}
	d := out.Donation
	P, V := d.Platform, d.Provider
	// the capture needs the donor-covered provider part; settlement needs the provider fee
	if V.Parts.Donor == nil || (in.Settle && V.Amount == nil) {
		return res, errs.New(errs.Unprocessable, "FEE_PROVIDER_FEE_UNKNOWN",
			"The provider fee is UNKNOWN (no provider fee evidence or no policy deciding who bears it); record a SYNTHETIC provider fee schedule first.")
	}
	res.Breakdown = &out.Result
	var assessed map[string]string
	if err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if calcID == "" {
			if calcID, _, err = storeCalculation(ctx, tx, out, amount, s.now()); err != nil {
				return err
			}
		}
		assessed, err = s.insertAssessments(ctx, tx, assessInput{SourceType: SrcSyntheticDonation, SourceID: donation, CampaignID: sc.ID,
			Currency: in.Currency, CalcID: calcID, Method: method, ProviderID: provider, Basis: amount, Synthetic: true,
			AccountingFor: func(c componentRow) string {
				if c.Amount == nil {
					return AcctNotApplicable
				}
				return AcctPending
			}}, staff(staffID))
		return err
	}); err != nil {
		return res, err
	}
	owner := provider
	if owner == "" {
		owner = ledger.SyntheticProviderID
	}
	clearing, settled := ledger.AccountRef{Class: ledger.ClassPSPClearing, OwnerID: owner}, ledger.AccountRef{Class: ledger.ClassPSPSettled, OwnerID: owner}
	unsettled := ledger.AccountRef{Class: ledger.ClassCampaignUnsettled, OwnerID: sc.ID}
	payable := ledger.AccountRef{Class: ledger.ClassCampaignPayable, OwnerID: sc.ID}
	revenue, expense := ledger.AccountRef{Class: ledger.ClassPlatformFees}, ledger.AccountRef{Class: ledger.ClassPSPProcessingFees}
	pvID := P.PolicyVersionID
	key := func(step string) string { return "fee_fixture:" + donation + ":" + step }

	// capture: Dr clearing (D + P_d + V_d) / Cr unsettled (D − P_o + V_d) / Cr revenue (P)
	charge := amount + pv(P.Parts.Donor) + pv(V.Parts.Donor)
	j, err := s.post(ctx, staffID, key("capture"), "DONATION_CAPTURED", "payment", donation, in.Currency, "capture with platform fee", pvID,
		nonZero(ledger.Dr(clearing, charge), ledger.Cr(unsettled, amount-pv(P.Parts.Organiser)+pv(V.Parts.Donor)), ledger.Cr(revenue, pv(P.Amount))))
	if err != nil {
		return res, err
	}
	res.Journals = append(res.Journals, j)
	if id := assessed[CatPlatform]; id != "" {
		if err := s.markPosted(ctx, id, "", j, "CAPTURE", in.Currency, pv(P.Amount), pvID); err != nil {
			return res, err
		}
	}
	// provider fee deducted at source: Dr unsettled (V_d + V_o) / Dr expense (V_p) / Cr clearing (V)
	if V.Amount != nil && *V.Amount > 0 {
		j, err := s.post(ctx, staffID, key("psp_fee"), "PSP_FEE", "payment", donation, in.Currency, "provider fee (estimated, synthetic evidence)",
			V.PolicyVersionID, nonZero(ledger.Dr(unsettled, pv(V.Parts.Donor)+pv(V.Parts.Organiser)), ledger.Dr(expense, pv(V.Parts.Platform)),
				ledger.Cr(clearing, *V.Amount)))
		if err != nil {
			return res, err
		}
		res.Journals = append(res.Journals, j)
		if id := assessed[CatProcessing]; id != "" {
			if err := s.markPosted(ctx, id, "", j, "PROVIDER_FEE", in.Currency, *V.Amount, V.PolicyVersionID); err != nil {
				return res, err
			}
		}
	} else if id := assessed[CatProcessing]; id != "" && V.Amount != nil { // a zero provider fee needs no journal
		if err := s.markPostedNoJournal(ctx, id); err != nil {
			return res, err
		}
	}
	if in.Settle {
		net := charge - pv(V.Amount)
		j, err := s.post(ctx, staffID, key("settlement"), "SETTLEMENT_MATCHED", "settlement_item", seedID("settlement_item:"+donation), in.Currency,
			"settlement (synthetic)", "", []ledger.Line{ledger.Dr(settled, net), ledger.Cr(clearing, net)})
		if err != nil {
			return res, err
		}
		res.Journals = append(res.Journals, j)
		alloc := pv(d.CampaignAllocation)
		j, err = s.post(ctx, staffID, key("release"), "RELEASE", "payment", donation, in.Currency, "release of the allocation after fees", "",
			[]ledger.Line{ledger.Dr(unsettled, alloc), ledger.Cr(payable, alloc)})
		if err != nil {
			return res, err
		}
		res.Journals = append(res.Journals, j)
	}
	res.Assessments, err = s.Assessments(ctx, AssessmentFilter{SourceType: SrcSyntheticDonation, CampaignID: sc.ID, Limit: 500})
	if err != nil {
		return res, err
	}
	res.Assessments = filterBySource(res.Assessments, donation)
	return res, nil
}

func filterBySource(list []Assessment, src string) []Assessment {
	out := []Assessment{}
	for _, a := range list {
		if a.SourceID == src {
			out = append(out, a)
		}
	}
	return out
}

// markPosted links an assessment (or adjustment) to its journal and marks the assessment POSTED.
func (s *Service) markPosted(ctx context.Context, assessmentID, adjustmentID string, j FixtureJournal, purpose, ccy string, fee int64, pvID string) error {
	return s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := insertAccountingRef(ctx, tx, assessmentID, adjustmentID, j.TransactionID, j.Key, j.Rule, purpose, ccy, fee, pvID); err != nil {
			return err
		}
		if adjustmentID != "" {
			_, err := tx.Exec(ctx, `UPDATE app.fee_adjustments SET accounting_status = 'POSTED' WHERE id = $1 AND accounting_status = 'NOT_POSTED'`, adjustmentID)
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE app.fee_assessments SET accounting_status = 'POSTED' WHERE id = $1 AND accounting_status = 'PENDING_POSTING'`, assessmentID)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		return event(ctx, tx, "fee_assessment", assessmentID, "FEE_POSTED", systemActor, map[string]any{"journal_key": j.Key,
			"ledger_transaction_id": j.TransactionID}, s.now())
	})
}

// markPostedNoJournal settles a zero fee that needs no journal.
func (s *Service) markPostedNoJournal(ctx context.Context, assessmentID string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE app.fee_assessments SET accounting_status = 'POSTED' WHERE id = $1 AND accounting_status = 'PENDING_POSTING'`, assessmentID)
	return err
}

// donationFacts loads what a refund or chargeback of a synthetic donation needs.
type donationFacts struct {
	ID, CampaignID, Currency, ProviderOwner string
	Amount                                  int64
	Platform                                *Assessment
	Component                               ComponentDoc
	PolicyVersion                           *Version
	Released, Settled                       bool
	RefundedBefore, ReversedBefore          int64
	Count                                   int
}

func (s *Service) loadDonation(ctx context.Context, donationID, reasonCode string) (donationFacts, error) {
	f := donationFacts{ID: donationID}
	if !ids.Valid(donationID) {
		return f, validation("donation_id", "INVALID_FORMAT")
	}
	a, err := scanAssessment(s.Pool.QueryRow(ctx, `SELECT `+assessmentCols+` FROM app.fee_assessments a
		WHERE a.source_type = 'SYNTHETIC_DONATION' AND a.source_id = $1 ORDER BY (a.category = 'PLATFORM_FEE') DESC, a.category LIMIT 1`, donationID))
	if errors.Is(err, ErrNotFound) {
		return f, errs.New(errs.NotFound, "FEE_FIXTURE_DONATION_NOT_FOUND", "No synthetic fee fixture donation has this id.")
	}
	if err != nil {
		return f, err
	}
	if a.Category != CatPlatform || a.fee == nil {
		return f, errs.New(errs.Unprocessable, "FEE_NO_PLATFORM_FEE", "This synthetic donation has no assessed platform fee to adjust.")
	}
	f.Platform = &a
	any_ := &a
	f.CampaignID, f.Currency, f.Amount = deref(any_.CampaignID), any_.Currency, any_.basis
	f.ProviderOwner = deref(any_.ProviderID)
	if f.ProviderOwner == "" {
		f.ProviderOwner = ledger.SyntheticProviderID
	}
	var raw []byte
	if err := s.Pool.QueryRow(ctx, `SELECT result FROM app.fee_calculations WHERE id = $1`, f.Platform.CalculationID).Scan(&raw); err != nil {
		return f, err
	}
	var doc ResultDoc
	if err := jsonUnmarshal(raw, &doc); err != nil {
		return f, err
	}
	for _, c := range doc.Components {
		if c.Category == CatPlatform {
			f.Component = c
		}
	}
	if f.Platform.PolicyVersionID != nil {
		v, err := s.Version(ctx, *f.Platform.PolicyVersionID)
		if err != nil {
			return f, err
		}
		f.PolicyVersion = &v
	}
	if _, ok, err := s.Ledger.JournalAmountsByKey(ctx, "fee_fixture:"+donationID+":release"); err != nil {
		return f, err
	} else {
		f.Released = ok
	}
	if _, ok, err := s.Ledger.JournalAmountsByKey(ctx, "fee_fixture:"+donationID+":settlement"); err != nil {
		return f, err
	} else {
		f.Settled = ok
	}
	// refunds and chargebacks so far reduce what can still be reversed (one cumulative basis for both)
	if err := s.Pool.QueryRow(ctx, `SELECT coalesce(sum(basis_minor), 0), coalesce(sum(amount_minor), 0), count(*) FILTER (WHERE reason = $2)
		FROM app.fee_adjustments WHERE assessment_id = $1 AND reason IN ('REFUND','CHARGEBACK')`, f.Platform.ID, reasonCode).
		Scan(&f.RefundedBefore, &f.ReversedBefore, &f.Count); err != nil {
		return f, err
	}
	return f, nil
}

// fixtureReversal: a refund or chargeback of part of a synthetic donation, with the platform-fee reversal of the
// policy version's refund treatment and rounding, posted as REFUND_RESERVED + REFUND_SETTLED or DISPUTE_OPENED
// (+ DISPUTE_FEE when a chargeback fee is known).
func (s *Service) fixtureReversal(ctx context.Context, staffID string, in FixtureRequest) (FixtureResult, error) {
	reasonCode, isRefund := AdjChargeback, in.Scenario == FixtureRefund
	if isRefund {
		reasonCode = AdjRefund
	}
	part, err := parseAmount(in.AmountMinor)
	if err != nil {
		return FixtureResult{}, err
	}
	f, err := s.loadDonation(ctx, in.DonationID, reasonCode)
	if err != nil {
		return FixtureResult{}, err
	}
	res := FixtureResult{SyntheticDonationID: f.ID}
	srcType := map[bool]string{true: "refund", false: "dispute"}[isRefund]
	srcID := seedID(srcType + ":" + f.ID + ":" + strconv.Itoa(f.Count+1))
	if in.ReversalID != nil {
		if !ids.Valid(*in.ReversalID) {
			return res, validation("reversal_id", "INVALID_FORMAT")
		}
		srcID = *in.ReversalID
	}
	// replay: this reversal was already recorded (its journal is posted before the adjustment is recorded)
	if existing, err := scanAdjustment(s.Pool.QueryRow(ctx, `SELECT `+adjustmentCols+` FROM app.fee_adjustments
		WHERE assessment_id = $1 AND reason = $2 AND source_type = $3 AND source_id = $4`, f.Platform.ID, reasonCode, srcType, srcID)); err == nil {
		res.Adjustments = []Adjustment{existing}
		return res, nil
	} else if !errors.Is(err, ErrNotFound) {
		return res, err
	}
	if f.PolicyVersion == nil || f.PolicyVersion.RefundTreatment == nil {
		return res, errs.New(errs.Unprocessable, "FEE_NO_PLATFORM_FEE", "This synthetic donation has no platform fee policy to apply.")
	}
	fee := pv(f.Platform.fee)
	fixed, _ := parseAmt(f.Component.FixedPart)
	rv, err := FeeReversal(fee, fixed, f.Amount, f.RefundedBefore, part, f.ReversedBefore, *f.PolicyVersion.RefundTreatment,
		f.PolicyVersion.Terms.Rounding)
	if err != nil {
		return res, calcError(err)
	}
	var share int64
	if f.PolicyVersion.DonorShareBps != nil {
		share = *f.PolicyVersion.DonorShareBps
	}
	split, err := Split(rv.Amount, f.PolicyVersion.Responsibility, share)
	if err != nil {
		return res, calcError(err)
	}
	calc := map[string]any{"treatment": rv.Treatment, "rounding_mode": rv.Rounding, "exact_value": rv.Exact, "fee_minor": amt(fee),
		"fixed_part_minor": amt(fixed), "donation_minor": amt(f.Amount), "refunded_before_minor": amt(f.RefundedBefore),
		"reversed_before_minor": amt(f.ReversedBefore), "cumulative_reversed_minor": amt(rv.Cumulative), "retained_minor": amt(rv.Retained),
		"donor_part_minor": amt(*split.Donor), "organiser_part_minor": amt(*split.Organiser),
		"provider_fee_on_" + srcType: "UNKNOWN (no provider terms; never assumed refundable)"}
	owner := f.ProviderOwner
	campaignAcct := ledger.AccountRef{Class: ledger.ClassCampaignUnsettled, OwnerID: f.CampaignID}
	if f.Released {
		campaignAcct.Class = ledger.ClassCampaignPayable
	}
	providerAcct := ledger.AccountRef{Class: ledger.ClassPSPClearing, OwnerID: owner}
	if f.Settled {
		providerAcct.Class = ledger.ClassPSPSettled
	}
	revenue := ledger.AccountRef{Class: ledger.ClassPlatformFees}
	key := func(step string) string {
		return "fee_fixture:" + f.ID + ":" + srcType + ":" + srcID + ":" + step
	}
	// the donor gets back the disputed/refunded part plus their share of the reversed fee; the campaign returns the
	// part less its share of the reversed fee; FundZim returns the reversed fee
	toDonor := part + *split.Donor
	fromCampaign := part - *split.Organiser
	pvID := f.PolicyVersion.ID
	var main FixtureJournal
	if isRefund {
		payableAcct := ledger.AccountRef{Class: ledger.ClassRefundPayable} // platform-owned obligation to the donor
		main, err = s.post(ctx, staffID, key("reserved"), "REFUND_RESERVED", "refund", srcID, f.Currency, "refund with platform-fee reversal", pvID,
			nonZero(ledger.Dr(campaignAcct, fromCampaign), ledger.Dr(revenue, rv.Amount), ledger.Cr(payableAcct, toDonor)))
		if err != nil {
			return res, err
		}
		res.Journals = append(res.Journals, main)
		j, err := s.post(ctx, staffID, key("settled"), "REFUND_SETTLED", "refund", srcID, f.Currency, "refund settled (synthetic)", "",
			[]ledger.Line{ledger.Dr(payableAcct, toDonor), ledger.Cr(providerAcct, toDonor)})
		if err != nil {
			return res, err
		}
		res.Journals = append(res.Journals, j)
	} else {
		main, err = s.post(ctx, staffID, key("opened"), "DISPUTE_OPENED", "dispute", srcID, f.Currency, "chargeback with platform-fee reversal", pvID,
			nonZero(ledger.Dr(campaignAcct, fromCampaign), ledger.Dr(revenue, rv.Amount), ledger.Cr(providerAcct, toDonor)))
		if err != nil {
			return res, err
		}
		res.Journals = append(res.Journals, main)
		exp, j, err := s.chargebackFee(ctx, staffID, f, part, srcID, key("fee"))
		if err != nil {
			return res, err
		}
		res.Exposure = exp
		if j != nil {
			res.Journals = append(res.Journals, *j)
		}
	}
	// the journals are posted: now record the adjustment (a ledger refusal above leaves nothing recorded)
	var adjID string
	if err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		adjID, _, err = s.systemAdjustment(ctx, tx, f.Platform.ID, f.Currency, reasonCode, rv.Amount, srcType, srcID, part, calc,
			"TEST_ONLY synthetic "+srcType+" of part of a synthetic donation", AcctNotPosted)
		return err
	}); err != nil {
		return res, err
	}
	if err := s.markPosted(ctx, "", adjID, main, map[bool]string{true: "REFUND", false: "CHARGEBACK"}[isRefund], f.Currency, rv.Amount, pvID); err != nil {
		return res, err
	}
	a, err := s.Adjustment(ctx, adjID)
	if err != nil {
		return res, err
	}
	res.Adjustments = []Adjustment{a}
	return res, nil
}

// chargebackFee estimates the provider chargeback fee (CHARGEBACK_FEE policy + CHARGEBACK provider evidence), assesses
// it once per donation and posts DISPUTE_FEE when known. An organiser-borne fee is recovered from the campaign's
// payable balance only if it suffices; otherwise it stays an UNRECOVERED exposure (never assumed recoverable,
// LR-020/LR-080).
func (s *Service) chargebackFee(ctx context.Context, staffID string, f donationFacts, disputed int64, disputeID, key string) (map[string]any, *FixtureJournal, error) {
	now := s.now()
	sc, err := s.Campaigns.FeeScope(ctx, f.CampaignID)
	if err != nil {
		return nil, nil, err
	}
	providerID := ""
	if f.ProviderOwner != ledger.SyntheticProviderID {
		providerID = f.ProviderOwner
	}
	pol, err := s.Resolve(ctx, nil, Scope{Category: CatChargeback, TransactionType: TxChargeback, Currency: f.Currency, ProviderID: providerID,
		CampaignCategory: sc.CategoryCode, CampaignID: sc.ID, At: now})
	if err != nil {
		return nil, nil, err
	}
	sch, err := s.resolveSchedule(ctx, nil, providerID, FeeTypeChargeback, f.Currency, "", now)
	if err != nil {
		return nil, nil, err
	}
	var pin *PolicyInput
	var sin *ScheduleInput
	if pol != nil {
		if pin, err = pol.input(); err != nil {
			return nil, nil, err
		}
	}
	if sch != nil {
		if sin, err = sch.input(); err != nil {
			return nil, nil, err
		}
		if !sch.RoundingKnown {
			sin.Terms.Rounding = ""
		}
	}
	out, err := Compute(NewInputs(TxChargeback, f.Currency, disputed, "", providerID, sc.ID, sc.CategoryCode, now.Format(time.RFC3339Nano), nil, pin, sin))
	if err != nil {
		return nil, nil, calcError(err)
	}
	c := out.Components[0]
	exp := map[string]any{"disputed_minor": amt(disputed), "provider_chargeback_fee_minor": amtP(c.Amount), "basis": c.Basis,
		"responsibility": c.Responsibility, "platform_exposure_minor": amtP(c.Parts.Platform), "campaign_exposure_minor": amtP(c.Parts.Organiser)}
	var assessed map[string]string
	if err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		calcID, _, err := storeCalculation(ctx, tx, out, disputed, now)
		if err != nil {
			return err
		}
		assessed, err = s.insertAssessments(ctx, tx, assessInput{SourceType: SrcSyntheticDonation, SourceID: f.ID, CampaignID: f.CampaignID,
			Currency: f.Currency, CalcID: calcID, ProviderID: providerID, Basis: disputed, Synthetic: true,
			AccountingFor: func(c componentRow) string {
				if c.Amount == nil || *c.Amount == 0 {
					return AcctNotApplicable
				}
				return AcctPending
			}}, staff(staffID))
		return err
	}); err != nil {
		return nil, nil, err
	}
	if c.Amount == nil {
		exp["recovery_status"] = "UNKNOWN_FEE"
		return exp, nil, nil
	}
	if *c.Amount == 0 {
		exp["recovery_status"] = "NOT_APPLICABLE"
		return exp, nil, nil
	}
	if c.Parts.Platform == nil || c.Parts.Organiser == nil {
		exp["recovery_status"] = "RESPONSIBILITY_NOT_CONFIGURED"
		return exp, nil, nil
	}
	settled := ledger.AccountRef{Class: ledger.ClassPSPSettled, OwnerID: f.ProviderOwner}
	j, err := s.post(ctx, staffID, key, "DISPUTE_FEE", "dispute", disputeID, f.Currency, "provider chargeback fee (estimated, synthetic evidence)",
		c.PolicyVersionID, nonZero(ledger.Dr(ledger.AccountRef{Class: ledger.ClassPSPProcessingFees}, *c.Parts.Platform),
			ledger.Dr(ledger.AccountRef{Class: ledger.ClassCampaignPayable, OwnerID: f.CampaignID}, *c.Parts.Organiser), ledger.Cr(settled, *c.Amount)))
	if errors.Is(err, ledger.ErrInsufficientFunds) {
		exp["recovery_status"] = "UNRECOVERED_INSUFFICIENT_CAMPAIGN_BALANCE"
		return exp, nil, nil // the assessment stays PENDING_POSTING: reconciliation reports it
	}
	if err != nil {
		return nil, nil, err
	}
	exp["recovery_status"] = map[bool]string{true: "RECOVERED_FROM_CAMPAIGN_BALANCE", false: "BORNE_BY_PLATFORM"}[*c.Parts.Organiser > 0]
	if id := assessed[CatChargeback]; id != "" {
		if err := s.markPosted(ctx, id, "", j, "CHARGEBACK_FEE", f.Currency, *c.Amount, c.PolicyVersionID); err != nil {
			return nil, nil, err
		}
	}
	return exp, &j, nil
}

// fixtureReport records a SIMULATED provider-reported processing fee for a synthetic donation (reconciliation input).
func (s *Service) fixtureReport(ctx context.Context, staffID string, in FixtureRequest) (FixtureResult, error) {
	reported, err := parseAmt(in.ReportedFeeMinor)
	if err != nil {
		return FixtureResult{}, validation("reported_fee_minor", "INVALID_FORMAT")
	}
	if !ids.Valid(in.DonationID) {
		return FixtureResult{}, validation("donation_id", "INVALID_FORMAT")
	}
	a, err := scanAssessment(s.Pool.QueryRow(ctx, `SELECT `+assessmentCols+` FROM app.fee_assessments a
		WHERE a.source_type = 'SYNTHETIC_DONATION' AND a.source_id = $1 AND a.category = 'PAYMENT_PROCESSING_FEE'`, in.DonationID))
	if err != nil {
		return FixtureResult{}, err
	}
	now := s.now()
	ref := "simulated-report:" + in.DonationID
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO app.fee_provider_reports (id, assessment_id, currency, reported_fee_minor, basis, evidence_class,
			source_reference, reported_at, recorded_by) VALUES ($1, $2, $3, $4, 'PROVIDER_REPORTED', 'SIMULATED', $5, $6, $7)
			ON CONFLICT ON CONSTRAINT uq_fee_provider_reports_source DO NOTHING`, ids.New(), a.ID, a.Currency, reported, ref, now, "staff:"+staffID)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		return event(ctx, tx, "fee_assessment", a.ID, "PROVIDER_FEE_REPORTED", staff(staffID), map[string]any{"reported_fee_minor": amt(reported),
			"evidence_class": "SIMULATED"}, now)
	})
	if err != nil {
		return FixtureResult{}, err
	}
	a, err = s.Assessment(ctx, a.ID)
	return FixtureResult{SyntheticDonationID: in.DonationID, Assessments: []Assessment{a}}, err
}
