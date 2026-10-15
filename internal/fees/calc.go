package fees

// Pure fee calculation (ADR-044 §6–§7; MONEY.md §5–§6). No I/O, no clock, no globals: the same inputs always give the
// same result. Money is int64 minor units; every product and quotient is computed exactly on big integers and rounded
// once per component with the version's rounding mode. Rates are integer basis points (10 000 = 100 %).

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
)

// AlgorithmVersion identifies this calculation algorithm; stored with every calculation and policy version.
const AlgorithmVersion = "fees-calc/1"

// Rounding modes (applied once, to the exact rational value of a component).
const (
	HalfUp   = "HALF_UP"   // .5 rounds away from zero (fees are never negative: up)
	HalfEven = "HALF_EVEN" // .5 rounds to the even neighbour
	Floor    = "FLOOR"
	Ceiling  = "CEILING"
)

// Models.
const (
	ModelZero         = "ZERO_FEE"
	ModelPercentage   = "PERCENTAGE"
	ModelFixed        = "FIXED"
	ModelPctPlusFixed = "PERCENTAGE_PLUS_FIXED"
	ModelTiered       = "TIERED"
	ModelCapped       = "CAPPED"
	ModelMinimum      = "MINIMUM_FEE"
	ModelPassThrough  = "PROVIDER_PASS_THROUGH"
	maxBps            = 10_000
	maxTiers          = 20
)

// Responsibility models.
const (
	PlatformAbsorbed  = "PLATFORM_ABSORBED"
	DonorCovered      = "DONOR_COVERED"
	OrganiserCovered  = "ORGANISER_COVERED"
	Shared            = "SHARED"
	NotConfiguredResp = "" // no policy decides who bears it
)

// Categories.
const (
	CatPlatform   = "PLATFORM_FEE"
	CatProcessing = "PAYMENT_PROCESSING_FEE"
	CatPayout     = "PAYOUT_PROCESSING_FEE"
	CatRefund     = "REFUND_FEE"
	CatChargeback = "CHARGEBACK_FEE"
	CatFX         = "CURRENCY_CONVERSION_FEE"
	CatOther      = "OTHER_APPROVED_FEE"
)

// Bases: what a fee amount is (ADR-044 §8). They are never interchangeable.
const (
	BasisQuoted            = "QUOTED"
	BasisEstimated         = "ESTIMATED"
	BasisProviderReported  = "PROVIDER_REPORTED"
	BasisProviderConfirmed = "PROVIDER_CONFIRMED"
	BasisReconciled        = "RECONCILED"
	BasisUnknown           = "UNKNOWN"
	BasisNotConfigured     = "NOT_CONFIGURED"
)

// Refund treatments of a platform fee.
const (
	RefundProportional            = "PROPORTIONAL"
	RefundProportionalExceptFixed = "PROPORTIONAL_EXCEPT_FIXED"
	RefundNonRefundable           = "NON_REFUNDABLE"
)

// Transaction types.
const (
	TxDonation   = "DONATION"
	TxPayout     = "PAYOUT"
	TxRefund     = "REFUND"
	TxChargeback = "CHARGEBACK"
)

// Calculation errors (pure; the service maps them to API errors).
var (
	ErrCalcAmount   = errors.New("fees: the basis amount must be positive")
	ErrCalcTerms    = errors.New("fees: invalid fee terms")
	ErrCalcOverflow = errors.New("fees: arithmetic overflow")
	ErrCalcExceeds  = errors.New("fees: the fee borne by the organiser is not below the amount")
)

// Tier is one band of a TIERED model. The band containing the whole amount applies to the whole amount: the first
// tier with UpToMinor ≥ amount, or the last tier (UpToMinor nil, open-ended).
type Tier struct {
	UpToMinor  *int64
	RateBps    int64
	FixedMinor int64
}

// Terms are the parameters of a fee: a policy version's or a provider fee schedule's.
type Terms struct {
	Model    string
	RateBps  *int64
	Fixed    *int64
	Min      *int64
	Max      *int64
	Tiers    []Tier
	Rounding string
}

// Validate checks the shape of terms (mirrors ck_fee_policy_versions_shape / ck_fee_provider_schedules_shape).
func (t Terms) Validate() error {
	bad := func(msg string) error { return fmt.Errorf("%w: %s", ErrCalcTerms, msg) }
	switch t.Rounding {
	case HalfUp, HalfEven, Floor, Ceiling:
	default:
		return bad("rounding_mode")
	}
	if t.RateBps != nil && (*t.RateBps < 0 || *t.RateBps > maxBps) {
		return bad("rate_bps")
	}
	for _, v := range []*int64{t.Fixed, t.Min, t.Max} {
		if v != nil && *v < 0 {
			return bad("negative amount")
		}
	}
	if t.Min != nil && t.Max != nil && *t.Max < *t.Min {
		return bad("max_fee_minor below min_fee_minor")
	}
	has := func(p *int64) bool { return p != nil }
	switch t.Model {
	case ModelZero, ModelPassThrough:
		if has(t.RateBps) || has(t.Fixed) || has(t.Min) || has(t.Max) || len(t.Tiers) > 0 {
			return bad(t.Model + " takes no parameters")
		}
	case ModelPercentage:
		if !has(t.RateBps) || has(t.Fixed) || len(t.Tiers) > 0 {
			return bad("PERCENTAGE needs rate_bps only (min/max optional)")
		}
	case ModelFixed:
		if has(t.RateBps) || !has(t.Fixed) || len(t.Tiers) > 0 {
			return bad("FIXED needs fixed_minor only (min/max optional)")
		}
	case ModelPctPlusFixed:
		if !has(t.RateBps) || !has(t.Fixed) || len(t.Tiers) > 0 {
			return bad("PERCENTAGE_PLUS_FIXED needs rate_bps and fixed_minor")
		}
	case ModelCapped:
		if !has(t.RateBps) || has(t.Fixed) || !has(t.Max) || len(t.Tiers) > 0 {
			return bad("CAPPED needs rate_bps and max_fee_minor")
		}
	case ModelMinimum:
		if !has(t.RateBps) || has(t.Fixed) || !has(t.Min) || len(t.Tiers) > 0 {
			return bad("MINIMUM_FEE needs rate_bps and min_fee_minor")
		}
	case ModelTiered:
		if has(t.RateBps) || has(t.Fixed) || len(t.Tiers) == 0 || len(t.Tiers) > maxTiers {
			return bad("TIERED needs 1 to 20 tiers and no rate_bps/fixed_minor")
		}
		var prev int64
		for i, tr := range t.Tiers {
			if tr.RateBps < 0 || tr.RateBps > maxBps || tr.FixedMinor < 0 {
				return bad("tier rate or fixed amount")
			}
			last := i == len(t.Tiers)-1
			if last != (tr.UpToMinor == nil) {
				return bad("only the last tier is open-ended")
			}
			if tr.UpToMinor != nil {
				if *tr.UpToMinor <= prev {
					return bad("tier bounds must be positive and strictly ascending")
				}
				prev = *tr.UpToMinor
			}
		}
	default:
		return bad("model")
	}
	return nil
}

// Rational is an exact non-negative value Num/Den (Den > 0) before rounding.
type Rational struct{ Num, Den *big.Int }

func (r Rational) String() string { return r.Num.String() + "/" + r.Den.String() }

// RoundRational rounds an exact non-negative rational once.
func RoundRational(r Rational, mode string) (int64, error) {
	if r.Num.Sign() < 0 || r.Den.Sign() <= 0 {
		return 0, ErrCalcTerms
	}
	q, m := new(big.Int).QuoRem(r.Num, r.Den, new(big.Int))
	if m.Sign() != 0 {
		twice := new(big.Int).Lsh(m, 1)
		c := twice.Cmp(r.Den)
		switch mode {
		case HalfUp:
			if c >= 0 {
				q.Add(q, big.NewInt(1))
			}
		case HalfEven:
			if c > 0 || (c == 0 && q.Bit(0) == 1) {
				q.Add(q, big.NewInt(1))
			}
		case Floor:
		case Ceiling:
			q.Add(q, big.NewInt(1))
		default:
			return 0, ErrCalcTerms
		}
	}
	if !q.IsInt64() {
		return 0, ErrCalcOverflow
	}
	return q.Int64(), nil
}

// pct is the exact value amount × bps / 10 000.
func pct(amount, bps int64) Rational {
	return Rational{Num: new(big.Int).Mul(big.NewInt(amount), big.NewInt(bps)), Den: big.NewInt(maxBps)}
}

func addInt(r Rational, v int64) Rational {
	return Rational{Num: new(big.Int).Add(r.Num, new(big.Int).Mul(big.NewInt(v), r.Den)), Den: r.Den}
}

// Fee is one computed fee amount with the audit of its rounding step and its fixed portion (for refund treatment).
type Fee struct {
	Amount     int64
	Exact      Rational // the value before rounding (fixed parts included)
	FixedPart  int64    // the fixed amount inside Amount, after the min/max clamp (≤ Amount)
	Clamped    string   // "", "MIN" or "MAX"
	TierIndex  int      // TIERED: the band used (-1 otherwise)
	RoundingOf string
}

// Amount computes the fee on a positive basis amount: one rounding of the percentage part (plus any fixed part),
// then the minimum, then the maximum.
func (t Terms) Amount(basis int64) (Fee, error) {
	if basis <= 0 {
		return Fee{}, ErrCalcAmount
	}
	if err := t.Validate(); err != nil {
		return Fee{}, err
	}
	f := Fee{TierIndex: -1, RoundingOf: t.Rounding}
	var exact Rational
	var fixed int64
	switch t.Model {
	case ModelZero, ModelPassThrough:
		exact = Rational{Num: big.NewInt(0), Den: big.NewInt(1)}
	case ModelPercentage, ModelCapped, ModelMinimum:
		exact = pct(basis, *t.RateBps)
	case ModelFixed:
		fixed = *t.Fixed
		exact = Rational{Num: big.NewInt(fixed), Den: big.NewInt(1)}
	case ModelPctPlusFixed:
		fixed = *t.Fixed
		exact = addInt(pct(basis, *t.RateBps), fixed)
	case ModelTiered:
		i := len(t.Tiers) - 1
		for j, tr := range t.Tiers {
			if tr.UpToMinor != nil && basis <= *tr.UpToMinor {
				i = j
				break
			}
		}
		f.TierIndex = i
		fixed = t.Tiers[i].FixedMinor
		exact = addInt(pct(basis, t.Tiers[i].RateBps), fixed)
	}
	amount, err := RoundRational(exact, t.Rounding)
	if err != nil {
		return Fee{}, err
	}
	if t.Min != nil && amount < *t.Min {
		amount, f.Clamped = *t.Min, "MIN"
	}
	if t.Max != nil && amount > *t.Max {
		amount, f.Clamped = *t.Max, "MAX"
	}
	f.Amount, f.Exact, f.FixedPart = amount, exact, min(fixed, amount)
	return f, nil
}

// Parts is the split of a fee between who bears it. A nil part is unknown.
type Parts struct {
	Donor, Organiser, Platform *int64
}

func i64(v int64) *int64 { return &v }

// Split divides a known fee by responsibility. SHARED uses largest-remainder allocation of the donor share and the
// organiser share (ties to the donor: MONEY.md §6), so donor + organiser == amount exactly.
func Split(amount int64, responsibility string, donorShareBps int64) (Parts, error) {
	if amount < 0 {
		return Parts{}, ErrCalcAmount
	}
	switch responsibility {
	case PlatformAbsorbed:
		return Parts{i64(0), i64(0), i64(amount)}, nil
	case DonorCovered:
		return Parts{i64(amount), i64(0), i64(0)}, nil
	case OrganiserCovered:
		return Parts{i64(0), i64(amount), i64(0)}, nil
	case Shared:
		if donorShareBps <= 0 || donorShareBps >= maxBps {
			return Parts{}, ErrCalcTerms
		}
		d, o := allocate2(amount, donorShareBps, maxBps-donorShareBps)
		return Parts{i64(d), i64(o), i64(0)}, nil
	}
	return Parts{}, ErrCalcTerms
}

// UnknownParts is the split of an unknown fee: a part is known (zero) when the responsibility excludes that party.
func UnknownParts(responsibility string) Parts {
	z := func() *int64 { return i64(0) }
	switch responsibility {
	case PlatformAbsorbed:
		return Parts{z(), z(), nil}
	case DonorCovered:
		return Parts{nil, z(), z()}
	case OrganiserCovered:
		return Parts{z(), nil, z()}
	case Shared:
		return Parts{nil, nil, z()}
	}
	return Parts{} // not configured: nothing is known
}

// allocate2 splits total by weights (w1, w2) with the largest-remainder method, ties to the first part.
func allocate2(total, w1, w2 int64) (int64, int64) {
	sum := big.NewInt(w1 + w2)
	p1 := new(big.Int).Mul(big.NewInt(total), big.NewInt(w1))
	q1, r1 := new(big.Int).QuoRem(p1, sum, new(big.Int))
	p2 := new(big.Int).Mul(big.NewInt(total), big.NewInt(w2))
	q2, r2 := new(big.Int).QuoRem(p2, sum, new(big.Int))
	a, b := q1.Int64(), q2.Int64()
	if left := total - a - b; left > 0 { // 0 or 1 for two parts
		if r1.Cmp(r2) >= 0 {
			a += left
		} else {
			b += left
		}
	}
	return a, b
}

func addP(a, b *int64) (*int64, error) {
	if a == nil || b == nil {
		return nil, nil
	}
	if (*b > 0 && *a > math.MaxInt64-*b) || (*b < 0 && *a < math.MinInt64-*b) {
		return nil, ErrCalcOverflow
	}
	return i64(*a + *b), nil
}

func subP(a, b *int64) (*int64, error) {
	if b == nil {
		return nil, nil
	}
	return addP(a, i64(-*b))
}

// ---- donation breakdown ----------------------------------------------------------------------------------------

// PolicyInput is the policy version that decides one component (nil: no active policy for that category).
type PolicyInput struct {
	VersionID       string
	Terms           Terms
	Responsibility  string
	DonorShareBps   int64
	RefundTreatment string
}

// ScheduleInput is the provider fee schedule used to estimate a provider fee (nil: no evidence, the fee is UNKNOWN).
type ScheduleInput struct {
	ScheduleID         string
	Terms              Terms // Rounding "" means the provider's rounding is unknown: HALF_UP is used and recorded
	ConfirmationStatus string
}

// Component is one computed fee of a calculation.
type Component struct {
	Category           string
	Basis              string
	Amount             *int64
	Responsibility     string
	Parts              Parts
	Rounding           string
	Exact              string
	FixedPart          int64
	PolicyVersionID    string
	ProviderScheduleID string
	Note               string
}

// DonationResult is the fee breakdown of a donation (ADR-044 §7). Nil totals are UNKNOWN (an unknown provider fee
// or an undecided responsibility), never zero.
type DonationResult struct {
	Donation           int64
	Platform           Component
	Provider           Component
	DonorCharge        *int64
	CampaignAllocation *int64
	PlatformNet        *int64
	ProviderFee        *int64
}

func platformComponent(basis int64, p *PolicyInput) (Component, error) {
	c := Component{Category: CatPlatform}
	if p == nil {
		// no approved, active platform-fee policy: nothing is charged (ADR-044 §5)
		c.Basis, c.Amount, c.Parts, c.Note = BasisNotConfigured, i64(0), Parts{i64(0), i64(0), i64(0)}, "no active platform-fee policy"
		return c, nil
	}
	if p.Responsibility == PlatformAbsorbed {
		return Component{}, fmt.Errorf("%w: a platform fee cannot be platform-absorbed", ErrCalcTerms)
	}
	f, err := p.Terms.Amount(basis)
	if err != nil {
		return Component{}, err
	}
	parts, err := Split(f.Amount, p.Responsibility, p.DonorShareBps)
	if err != nil {
		return Component{}, err
	}
	c.Basis, c.Amount, c.Responsibility, c.Parts = BasisQuoted, i64(f.Amount), p.Responsibility, parts
	c.Rounding, c.Exact, c.FixedPart, c.PolicyVersionID = p.Terms.Rounding, f.Exact.String(), f.FixedPart, p.VersionID
	return c, nil
}

// providerComponent estimates a provider-cost fee (category) from schedule evidence and splits it by the policy's
// responsibility. No schedule ⇒ UNKNOWN amount; no policy ⇒ unknown split.
func providerComponent(category string, basis int64, p *PolicyInput, s *ScheduleInput) (Component, error) {
	c := Component{Category: category, Basis: BasisUnknown, Note: "no provider fee evidence"}
	resp := NotConfiguredResp
	var share int64
	if p != nil {
		resp, share, c.PolicyVersionID = p.Responsibility, p.DonorShareBps, p.VersionID
	} else {
		c.Note = "no policy decides who bears this fee"
	}
	c.Responsibility = resp
	if s == nil {
		c.Parts = UnknownParts(resp)
		return c, nil
	}
	t := s.Terms
	if t.Rounding == "" {
		t.Rounding = HalfUp
		c.Note = "provider rounding UNKNOWN; estimated with HALF_UP"
	}
	f, err := t.Amount(basis)
	if err != nil {
		return Component{}, err
	}
	c.Basis, c.Amount, c.Rounding, c.Exact, c.FixedPart, c.ProviderScheduleID = BasisEstimated, i64(f.Amount), t.Rounding, f.Exact.String(),
		f.FixedPart, s.ScheduleID
	if s.Terms.Rounding != "" {
		c.Note = "estimated from " + s.ConfirmationStatus + " provider fee evidence"
	}
	if resp == NotConfiguredResp {
		c.Parts = UnknownParts(resp)
		return c, nil
	}
	if c.Parts, err = Split(f.Amount, resp, share); err != nil {
		return Component{}, err
	}
	return c, nil
}

// DonationFees computes the breakdown of a donation of `donation` minor units:
//
//	donor_charge        = D + P_donor + V_donor
//	campaign_allocation = D − P_organiser − V_organiser
//	platform_net        = P − V_platform
//	donor_charge        = campaign_allocation + platform_net + V   (checked)
func DonationFees(donation int64, platform, processing *PolicyInput, schedule *ScheduleInput) (DonationResult, error) {
	if donation <= 0 {
		return DonationResult{}, ErrCalcAmount
	}
	r := DonationResult{Donation: donation}
	var err error
	if r.Platform, err = platformComponent(donation, platform); err != nil {
		return DonationResult{}, err
	}
	// the organiser-borne platform fee alone must leave something for the campaign, whatever the provider fee is
	if *r.Platform.Parts.Organiser >= donation {
		return DonationResult{}, ErrCalcExceeds
	}
	if r.Provider, err = providerComponent(CatProcessing, donation, processing, schedule); err != nil {
		return DonationResult{}, err
	}
	D := i64(donation)
	P, V := r.Platform, r.Provider
	if r.DonorCharge, err = addP(D, P.Parts.Donor); err == nil && r.DonorCharge != nil {
		r.DonorCharge, err = addP(r.DonorCharge, V.Parts.Donor)
	}
	if err != nil {
		return DonationResult{}, err
	}
	if r.CampaignAllocation, err = subP(D, P.Parts.Organiser); err == nil && r.CampaignAllocation != nil {
		r.CampaignAllocation, err = subP(r.CampaignAllocation, V.Parts.Organiser)
	}
	if err != nil {
		return DonationResult{}, err
	}
	if r.PlatformNet, err = subP(P.Amount, V.Parts.Platform); err != nil {
		return DonationResult{}, err
	}
	r.ProviderFee = V.Amount
	if r.CampaignAllocation != nil && *r.CampaignAllocation <= 0 {
		return DonationResult{}, ErrCalcExceeds
	}
	if r.DonorCharge != nil && r.CampaignAllocation != nil && r.PlatformNet != nil && r.ProviderFee != nil &&
		*r.DonorCharge != *r.CampaignAllocation+*r.PlatformNet+*r.ProviderFee {
		return DonationResult{}, errors.New("fees: breakdown does not reconcile") // programming error
	}
	return r, nil
}

// PayoutResult is the provider fee of a payout and what the beneficiary receives.
type PayoutResult struct {
	Amount              int64
	Fee                 Component
	BeneficiaryReceives *int64 // amount − organiser part; nil when unknown
}

// PayoutFees estimates the provider payout fee. An organiser-borne fee is carved out of the reserved amount; a
// platform-absorbed fee is a FundZim expense. A known organiser-borne fee that is not below the amount is refused.
func PayoutFees(amount int64, policy *PolicyInput, schedule *ScheduleInput) (PayoutResult, error) {
	if amount <= 0 {
		return PayoutResult{}, ErrCalcAmount
	}
	if policy != nil && policy.Responsibility != OrganiserCovered && policy.Responsibility != PlatformAbsorbed {
		return PayoutResult{}, fmt.Errorf("%w: payout fees are borne by the organiser or the platform", ErrCalcTerms)
	}
	c, err := providerComponent(CatPayout, amount, policy, schedule)
	if err != nil {
		return PayoutResult{}, err
	}
	r := PayoutResult{Amount: amount, Fee: c}
	if c.Parts.Organiser != nil {
		if *c.Parts.Organiser >= amount {
			return PayoutResult{}, ErrCalcExceeds
		}
		r.BeneficiaryReceives = i64(amount - *c.Parts.Organiser)
	}
	return r, nil
}

// ---- refunds and chargebacks -----------------------------------------------------------------------------------

// Reversal is the platform-fee reversal for a refund or chargeback.
type Reversal struct {
	Amount     int64 // this reversal
	Cumulative int64 // all reversals including this one
	Retained   int64 // the fee that stays with FundZim after all reversals so far
	Exact      string
	Treatment  string
	Rounding   string
}

// FeeReversal computes the platform fee to reverse when `refunded` more of a donation basis is refunded, given what
// was refunded and reversed before. It works on cumulative totals so a series of partial refunds reverses exactly
// what one refund of the same total would (no drift): reversal = round(refundable × cumulative_refunded / basis) −
// reversed_before. A full refund of a PROPORTIONAL fee reverses all of it.
func FeeReversal(fee, fixedPart, basis, refundedBefore, refunded, reversedBefore int64, treatment, rounding string) (Reversal, error) {
	if basis <= 0 || refunded <= 0 || fee < 0 || fixedPart < 0 || fixedPart > fee || refundedBefore < 0 || reversedBefore < 0 {
		return Reversal{}, ErrCalcAmount
	}
	if refundedBefore > basis || refunded > basis-refundedBefore {
		return Reversal{}, fmt.Errorf("%w: refunds exceed the original amount", ErrCalcAmount)
	}
	cum := refundedBefore + refunded
	var refundable int64
	switch treatment {
	case RefundProportional:
		refundable = fee
	case RefundProportionalExceptFixed:
		refundable = fee - fixedPart
	case RefundNonRefundable:
		refundable = 0
	default:
		return Reversal{}, ErrCalcTerms
	}
	exact := Rational{Num: new(big.Int).Mul(big.NewInt(refundable), big.NewInt(cum)), Den: big.NewInt(basis)}
	target, err := RoundRational(exact, rounding)
	if err != nil {
		return Reversal{}, err
	}
	if target > refundable { // CEILING can never exceed the refundable fee; defensive
		target = refundable
	}
	if target < reversedBefore {
		target = reversedBefore // never re-charge a reversed fee
	}
	return Reversal{Amount: target - reversedBefore, Cumulative: target, Retained: fee - target, Exact: exact.String(),
		Treatment: treatment, Rounding: rounding}, nil
}

// ---- canonical documents and hashes ----------------------------------------------------------------------------

// Doc helpers: amounts are JSON strings of digits (MONEY.md §3.3); nil is JSON null (UNKNOWN).
func amt(v int64) string { return strconv.FormatInt(v, 10) }

func amtP(v *int64) *string {
	if v == nil {
		return nil
	}
	s := amt(*v)
	return &s
}

// TermsDoc is the canonical JSON form of terms.
type TermsDoc struct {
	Model    string    `json:"model"`
	RateBps  *int64    `json:"rate_bps"`
	Fixed    *string   `json:"fixed_minor"`
	Min      *string   `json:"min_fee_minor"`
	Max      *string   `json:"max_fee_minor"`
	Tiers    []TierDoc `json:"tiers"`
	Rounding string    `json:"rounding_mode"`
}

// TierDoc is the canonical JSON form of a tier (also the stored form of fee_policy_versions.tiers).
type TierDoc struct {
	UpToMinor  *string `json:"up_to_minor"`
	RateBps    int64   `json:"rate_bps"`
	FixedMinor string  `json:"fixed_minor"`
}

// Doc returns the canonical JSON form.
func (t Terms) Doc() TermsDoc {
	d := TermsDoc{Model: t.Model, RateBps: t.RateBps, Fixed: amtP(t.Fixed), Min: amtP(t.Min), Max: amtP(t.Max), Rounding: t.Rounding,
		Tiers: []TierDoc{}}
	for _, tr := range t.Tiers {
		d.Tiers = append(d.Tiers, TierDoc{UpToMinor: amtP(tr.UpToMinor), RateBps: tr.RateBps, FixedMinor: amt(tr.FixedMinor)})
	}
	return d
}

func parseAmt(s string) (int64, error) {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 0 || s != strconv.FormatInt(v, 10) {
		return 0, fmt.Errorf("%w: amount %q", ErrCalcTerms, s)
	}
	return v, nil
}

func parseAmtP(s *string) (*int64, error) {
	if s == nil {
		return nil, nil
	}
	v, err := parseAmt(*s)
	return &v, err
}

// Terms converts the canonical form back (validating amounts).
func (d TermsDoc) Terms() (Terms, error) {
	t := Terms{Model: d.Model, RateBps: d.RateBps, Rounding: d.Rounding}
	var err error
	if t.Fixed, err = parseAmtP(d.Fixed); err != nil {
		return Terms{}, err
	}
	if t.Min, err = parseAmtP(d.Min); err != nil {
		return Terms{}, err
	}
	if t.Max, err = parseAmtP(d.Max); err != nil {
		return Terms{}, err
	}
	tiers, err := TiersFromDocs(d.Tiers)
	if err != nil {
		return Terms{}, err
	}
	t.Tiers = tiers
	return t, nil
}

// TiersFromDocs parses tiers.
func TiersFromDocs(docs []TierDoc) ([]Tier, error) {
	var out []Tier
	for _, td := range docs {
		up, err := parseAmtP(td.UpToMinor)
		if err != nil {
			return nil, err
		}
		fx, err := parseAmt(td.FixedMinor)
		if err != nil {
			return nil, err
		}
		out = append(out, Tier{UpToMinor: up, RateBps: td.RateBps, FixedMinor: fx})
	}
	return out, nil
}

// PolicyDoc and ScheduleDoc are the canonical inputs of a policy version and a provider schedule.
type PolicyDoc struct {
	VersionID       string   `json:"version_id"`
	Terms           TermsDoc `json:"terms"`
	Responsibility  string   `json:"responsibility"`
	DonorShareBps   int64    `json:"donor_share_bps"`
	RefundTreatment string   `json:"refund_treatment"`
}

type ScheduleDoc struct {
	ScheduleID         string   `json:"schedule_id"`
	Terms              TermsDoc `json:"terms"`
	ConfirmationStatus string   `json:"confirmation_status"`
}

func policyDoc(p *PolicyInput) *PolicyDoc {
	if p == nil {
		return nil
	}
	return &PolicyDoc{VersionID: p.VersionID, Terms: p.Terms.Doc(), Responsibility: p.Responsibility, DonorShareBps: p.DonorShareBps,
		RefundTreatment: p.RefundTreatment}
}

func scheduleDoc(s *ScheduleInput) *ScheduleDoc {
	if s == nil {
		return nil
	}
	return &ScheduleDoc{ScheduleID: s.ScheduleID, Terms: s.Terms.Doc(), ConfirmationStatus: s.ConfirmationStatus}
}

func (d *PolicyDoc) input() (*PolicyInput, error) {
	if d == nil {
		return nil, nil
	}
	t, err := d.Terms.Terms()
	if err != nil {
		return nil, err
	}
	return &PolicyInput{VersionID: d.VersionID, Terms: t, Responsibility: d.Responsibility, DonorShareBps: d.DonorShareBps,
		RefundTreatment: d.RefundTreatment}, nil
}

func (d *ScheduleDoc) input() (*ScheduleInput, error) {
	if d == nil {
		return nil, nil
	}
	t, err := d.Terms.Terms()
	if err != nil {
		return nil, err
	}
	return &ScheduleInput{ScheduleID: d.ScheduleID, Terms: t, ConfirmationStatus: d.ConfirmationStatus}, nil
}

// Inputs is the canonical input document of a calculation: everything the result depends on.
type Inputs struct {
	AlgorithmVersion string       `json:"algorithm_version"`
	TransactionType  string       `json:"transaction_type"`
	Currency         string       `json:"currency"`
	BasisMinor       string       `json:"basis_minor"`
	PaymentMethod    string       `json:"payment_method"`
	ProviderID       string       `json:"provider_id"`
	CampaignID       string       `json:"campaign_id"`
	CampaignCategory string       `json:"campaign_category"`
	At               string       `json:"at"` // RFC 3339 UTC: the time the policies were selected for
	Platform         *PolicyDoc   `json:"platform_policy"`
	Provider         *PolicyDoc   `json:"provider_cost_policy"`
	Schedule         *ScheduleDoc `json:"provider_schedule"`
}

// ComponentDoc and ResultDoc are the canonical result document.
type ComponentDoc struct {
	Category           string  `json:"category"`
	Basis              string  `json:"basis"`
	Amount             *string `json:"amount_minor"`
	Responsibility     string  `json:"responsibility"`
	Donor              *string `json:"donor_part_minor"`
	Organiser          *string `json:"organiser_part_minor"`
	Platform           *string `json:"platform_part_minor"`
	Rounding           string  `json:"rounding_mode"`
	Exact              string  `json:"exact_value"`
	FixedPart          string  `json:"fixed_part_minor"`
	PolicyVersionID    string  `json:"policy_version_id"`
	ProviderScheduleID string  `json:"provider_schedule_id"`
	Note               string  `json:"note"`
}

type ResultDoc struct {
	Components          []ComponentDoc `json:"components"`
	DonorCharge         *string        `json:"donor_charge_minor,omitempty"`
	CampaignAllocation  *string        `json:"campaign_allocation_minor,omitempty"`
	PlatformNet         *string        `json:"platform_net_minor,omitempty"`
	ProviderFee         *string        `json:"provider_fee_minor"`
	BeneficiaryReceives *string        `json:"beneficiary_receives_minor,omitempty"`
}

func (c Component) Doc() ComponentDoc {
	return ComponentDoc{Category: c.Category, Basis: c.Basis, Amount: amtP(c.Amount), Responsibility: c.Responsibility,
		Donor: amtP(c.Parts.Donor), Organiser: amtP(c.Parts.Organiser), Platform: amtP(c.Parts.Platform), Rounding: c.Rounding, Exact: c.Exact,
		FixedPart: amt(c.FixedPart), PolicyVersionID: c.PolicyVersionID, ProviderScheduleID: c.ProviderScheduleID, Note: c.Note}
}

// Outcome is a computed calculation: the canonical documents, their hashes and the typed results.
type Outcome struct {
	Inputs     Inputs
	Result     ResultDoc
	InputsJSON []byte
	ResultJSON []byte
	InputsHash [32]byte
	ResultHash [32]byte
	Components []Component
	Donation   *DonationResult
	Payout     *PayoutResult
}

// Compute runs the calculation named by in.TransactionType on canonical inputs. It is the single entry point used
// for quotes, assessments, payout estimates and reconciliation's recomputation.
func Compute(in Inputs) (Outcome, error) {
	if in.AlgorithmVersion != AlgorithmVersion {
		return Outcome{}, fmt.Errorf("%w: algorithm %q", ErrCalcTerms, in.AlgorithmVersion)
	}
	basis, err := parseAmt(in.BasisMinor)
	if err != nil {
		return Outcome{}, err
	}
	platform, err := in.Platform.input()
	if err != nil {
		return Outcome{}, err
	}
	providerPolicy, err := in.Provider.input()
	if err != nil {
		return Outcome{}, err
	}
	schedule, err := in.Schedule.input()
	if err != nil {
		return Outcome{}, err
	}
	out := Outcome{Inputs: in}
	switch in.TransactionType {
	case TxDonation:
		r, err := DonationFees(basis, platform, providerPolicy, schedule)
		if err != nil {
			return Outcome{}, err
		}
		out.Donation, out.Components = &r, []Component{r.Platform, r.Provider}
		out.Result = ResultDoc{DonorCharge: amtP(r.DonorCharge), CampaignAllocation: amtP(r.CampaignAllocation), PlatformNet: amtP(r.PlatformNet),
			ProviderFee: amtP(r.ProviderFee)}
	case TxPayout:
		if platform != nil {
			return Outcome{}, fmt.Errorf("%w: a payout has no platform fee", ErrCalcTerms)
		}
		r, err := PayoutFees(basis, providerPolicy, schedule)
		if err != nil {
			return Outcome{}, err
		}
		out.Payout, out.Components = &r, []Component{r.Fee}
		out.Result = ResultDoc{ProviderFee: amtP(r.Fee.Amount), BeneficiaryReceives: amtP(r.BeneficiaryReceives)}
	case TxRefund, TxChargeback:
		// provider refund/chargeback fees: estimated from evidence, split by the category's policy
		cat := CatRefund
		if in.TransactionType == TxChargeback {
			cat = CatChargeback
		}
		c, err := providerComponent(cat, basis, providerPolicy, schedule)
		if err != nil {
			return Outcome{}, err
		}
		out.Components = []Component{c}
		out.Result = ResultDoc{ProviderFee: amtP(c.Amount)}
	default:
		return Outcome{}, fmt.Errorf("%w: transaction type", ErrCalcTerms)
	}
	for _, c := range out.Components {
		out.Result.Components = append(out.Result.Components, c.Doc())
	}
	if out.InputsJSON, err = json.Marshal(in); err != nil {
		return Outcome{}, err
	}
	if out.ResultJSON, err = json.Marshal(out.Result); err != nil {
		return Outcome{}, err
	}
	out.InputsHash, out.ResultHash = sha256.Sum256(out.InputsJSON), sha256.Sum256(out.ResultJSON)
	return out, nil
}

// NewInputs builds canonical inputs.
func NewInputs(txType, currency string, basis int64, method, providerID, campaignID, campaignCategory, at string,
	platform, providerPolicy *PolicyInput, schedule *ScheduleInput) Inputs {
	return Inputs{AlgorithmVersion: AlgorithmVersion, TransactionType: txType, Currency: currency, BasisMinor: amt(basis),
		PaymentMethod: method, ProviderID: providerID, CampaignID: campaignID, CampaignCategory: campaignCategory, At: at,
		Platform: policyDoc(platform), Provider: policyDoc(providerPolicy), Schedule: scheduleDoc(schedule)}
}
