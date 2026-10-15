package fees

import (
	"errors"
	"math"
	"math/rand/v2"
	"testing"
)

func p64(v int64) *int64 { return &v }

func mustFee(t *testing.T, terms Terms, basis int64) Fee {
	t.Helper()
	f, err := terms.Amount(basis)
	if err != nil {
		t.Fatalf("Amount(%d): %v", basis, err)
	}
	return f
}

func TestModels(t *testing.T) {
	cases := []struct {
		name  string
		terms Terms
		basis int64
		want  int64
	}{
		{"zero fee", Terms{Model: ModelZero, Rounding: HalfUp}, 10_000, 0},
		{"percentage (MONEY.md worked example: US$50 at 5%)", Terms{Model: ModelPercentage, RateBps: p64(500), Rounding: HalfUp}, 5000, 250},
		{"percentage rounding example (US$3.33 at 5% → 16.65 → 17)", Terms{Model: ModelPercentage, RateBps: p64(500), Rounding: HalfUp}, 333, 17},
		{"fixed", Terms{Model: ModelFixed, Fixed: p64(30), Rounding: HalfUp}, 10_000, 30},
		{"percentage plus fixed: round(pct + fixed) once", Terms{Model: ModelPctPlusFixed, RateBps: p64(290), Fixed: p64(30), Rounding: HalfUp}, 2000, 88},
		{"minimum applies", Terms{Model: ModelMinimum, RateBps: p64(100), Min: p64(50), Rounding: HalfUp}, 1000, 50},
		{"minimum not needed", Terms{Model: ModelMinimum, RateBps: p64(100), Min: p64(50), Rounding: HalfUp}, 10_000, 100},
		{"cap applies", Terms{Model: ModelCapped, RateBps: p64(500), Max: p64(1000), Rounding: HalfUp}, 100_000, 1000},
		{"cap not reached", Terms{Model: ModelCapped, RateBps: p64(500), Max: p64(1000), Rounding: HalfUp}, 10_000, 500},
		{"percentage with min and max", Terms{Model: ModelPercentage, RateBps: p64(500), Min: p64(20), Max: p64(40), Rounding: HalfUp}, 100, 20},
		{"100 % rate", Terms{Model: ModelPercentage, RateBps: p64(10_000), Rounding: HalfUp}, 1234, 1234},
		{"0 % rate", Terms{Model: ModelPercentage, RateBps: p64(0), Rounding: HalfUp}, 1234, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mustFee(t, c.terms, c.basis).Amount; got != c.want {
				t.Fatalf("got %d, want %d", got, c.want)
			}
		})
	}
}

func TestTiered(t *testing.T) {
	terms := Terms{Model: ModelTiered, Rounding: HalfUp, Tiers: []Tier{
		{UpToMinor: p64(1000), RateBps: 0, FixedMinor: 10},
		{UpToMinor: p64(10_000), RateBps: 300, FixedMinor: 0},
		{UpToMinor: nil, RateBps: 200, FixedMinor: 50},
	}}
	for _, c := range []struct {
		basis, want int64
		tier        int
	}{{1, 10, 0}, {1000, 10, 0}, {1001, 30, 1}, {10_000, 300, 1}, {10_001, 250, 2}, {1_000_000, 20_050, 2}} {
		f := mustFee(t, terms, c.basis)
		if f.Amount != c.want || f.TierIndex != c.tier {
			t.Fatalf("basis %d: got %d (tier %d), want %d (tier %d)", c.basis, f.Amount, f.TierIndex, c.want, c.tier)
		}
	}
}

func TestRoundingModesAtBoundaries(t *testing.T) {
	// 1.5 bp of 1 minor unit: exact values x.5 and non-halves
	cases := []struct {
		basis, bps int64
		mode       string
		want       int64
	}{
		{333, 500, HalfUp, 17}, {333, 500, HalfEven, 17}, {333, 500, Floor, 16}, {333, 500, Ceiling, 17}, // 16.65
		{330, 500, HalfUp, 17}, {330, 500, HalfEven, 16}, {330, 500, Floor, 16}, {330, 500, Ceiling, 17}, // 16.5
		{350, 500, HalfUp, 18}, {350, 500, HalfEven, 18}, {350, 500, Floor, 17}, {350, 500, Ceiling, 18}, // 17.5
		{322, 500, HalfUp, 16}, {322, 500, HalfEven, 16}, {322, 500, Floor, 16}, {322, 500, Ceiling, 17}, // 16.1
		{400, 500, HalfUp, 20}, {400, 500, HalfEven, 20}, {400, 500, Floor, 20}, {400, 500, Ceiling, 20}, // exact
		{1, 1, HalfUp, 0}, {1, 1, Ceiling, 1}, {5000, 1, HalfUp, 1}, {5000, 1, HalfEven, 0}, // 0.0001, 0.5
	}
	for _, c := range cases {
		f := mustFee(t, Terms{Model: ModelPercentage, RateBps: p64(c.bps), Rounding: c.mode}, c.basis)
		if f.Amount != c.want {
			t.Errorf("%d × %d bp %s = %d, want %d (exact %s)", c.basis, c.bps, c.mode, f.Amount, c.want, f.Exact)
		}
	}
}

func TestTermsValidation(t *testing.T) {
	bad := []Terms{
		{Model: ModelPercentage, Rounding: HalfUp},                                                             // missing rate
		{Model: ModelPercentage, RateBps: p64(10_001), Rounding: HalfUp},                                       // > 100 %
		{Model: ModelPercentage, RateBps: p64(-1), Rounding: HalfUp},                                           // negative
		{Model: ModelFixed, Fixed: p64(-5), Rounding: HalfUp},                                                  // negative fixed
		{Model: ModelCapped, RateBps: p64(100), Rounding: HalfUp},                                              // CAPPED without max
		{Model: ModelMinimum, RateBps: p64(100), Rounding: HalfUp},                                             // MINIMUM without min
		{Model: ModelPercentage, RateBps: p64(100), Min: p64(10), Max: p64(5), Rounding: HalfUp},               // max < min
		{Model: ModelZero, RateBps: p64(0), Rounding: HalfUp},                                                  // zero with params
		{Model: ModelPercentage, RateBps: p64(100), Rounding: "BANKERS"},                                       // unknown rounding
		{Model: ModelTiered, Rounding: HalfUp},                                                                 // no tiers
		{Model: ModelTiered, Rounding: HalfUp, Tiers: []Tier{{UpToMinor: p64(100)}}},                           // last tier bounded
		{Model: ModelTiered, Rounding: HalfUp, Tiers: []Tier{{UpToMinor: p64(100)}, {UpToMinor: p64(50)}, {}}}, // not ascending
		{Model: "FLOAT_RATE", Rounding: HalfUp},
	}
	for i, terms := range bad {
		if _, err := terms.Amount(1000); !errors.Is(err, ErrCalcTerms) {
			t.Errorf("case %d: want ErrCalcTerms, got %v", i, err)
		}
	}
	if _, err := (Terms{Model: ModelZero, Rounding: HalfUp}).Amount(0); !errors.Is(err, ErrCalcAmount) {
		t.Fatalf("zero basis must be refused: %v", err)
	}
	if _, err := (Terms{Model: ModelZero, Rounding: HalfUp}).Amount(-1); !errors.Is(err, ErrCalcAmount) {
		t.Fatalf("negative basis (underflow) must be refused: %v", err)
	}
}

func TestOverflow(t *testing.T) {
	// the percentage of MaxInt64 fits, the added fixed part does not
	if _, err := (Terms{Model: ModelPctPlusFixed, RateBps: p64(10_000), Fixed: p64(1), Rounding: HalfUp}).Amount(math.MaxInt64); !errors.Is(err, ErrCalcOverflow) {
		t.Fatalf("want overflow, got %v", err)
	}
	f := mustFee(t, Terms{Model: ModelPercentage, RateBps: p64(10_000), Rounding: HalfUp}, math.MaxInt64)
	if f.Amount != math.MaxInt64 {
		t.Fatalf("exact big-integer intermediate lost precision: %d", f.Amount)
	}
	// donor-covered: donation + fee overflows the donor charge
	pol := &PolicyInput{VersionID: "v", Terms: Terms{Model: ModelPercentage, RateBps: p64(100), Rounding: HalfUp}, Responsibility: DonorCovered,
		RefundTreatment: RefundProportional}
	if _, err := DonationFees(math.MaxInt64, pol, nil, nil); !errors.Is(err, ErrCalcOverflow) {
		t.Fatalf("donor charge overflow must be refused: %v", err)
	}
}

func TestSplitShared(t *testing.T) {
	for _, c := range []struct{ amount, share, donor, organiser int64 }{
		{5, 5000, 3, 2},     // tie → donor (MONEY.md §6)
		{100, 3333, 33, 67}, // 33.33 / 66.67 → remainder to the organiser
		{1, 1, 0, 1},
		{0, 5000, 0, 0},
	} {
		p, err := Split(c.amount, Shared, c.share)
		if err != nil {
			t.Fatal(err)
		}
		if *p.Donor != c.donor || *p.Organiser != c.organiser || *p.Platform != 0 {
			t.Fatalf("%+v: got %d/%d/%d", c, *p.Donor, *p.Organiser, *p.Platform)
		}
	}
	if _, err := Split(10, Shared, 0); err == nil {
		t.Fatal("a 0 % donor share is not SHARED")
	}
}

// The brief's synthetic example: donation USD 100.00, platform fee 5 % (USD 5.00), provider fee 2 % (USD 2.00).
func syntheticExample(platformResp, providerResp string) (DonationResult, error) {
	pol := &PolicyInput{VersionID: "pv", Terms: Terms{Model: ModelPercentage, RateBps: p64(500), Rounding: HalfUp}, Responsibility: platformResp,
		DonorShareBps: 5000, RefundTreatment: RefundProportional}
	proc := &PolicyInput{VersionID: "pp", Terms: Terms{Model: ModelPassThrough, Rounding: HalfUp}, Responsibility: providerResp, DonorShareBps: 5000}
	sch := &ScheduleInput{ScheduleID: "s", Terms: Terms{Model: ModelPercentage, RateBps: p64(200), Rounding: HalfUp}, ConfirmationStatus: "SYNTHETIC"}
	return DonationFees(10_000, pol, proc, sch)
}

func TestResponsibilityModels(t *testing.T) {
	cases := []struct {
		platform, provider                 string
		charge, allocation, net, providerF int64
	}{
		{OrganiserCovered, OrganiserCovered, 10_000, 9_300, 500, 200},
		{DonorCovered, DonorCovered, 10_700, 10_000, 500, 200},
		{DonorCovered, PlatformAbsorbed, 10_500, 10_000, 300, 200},
		{OrganiserCovered, PlatformAbsorbed, 10_000, 9_500, 300, 200},
		{Shared, Shared, 10_350, 9_650, 500, 200},
		{DonorCovered, OrganiserCovered, 10_500, 9_800, 500, 200},
	}
	for _, c := range cases {
		r, err := syntheticExample(c.platform, c.provider)
		if err != nil {
			t.Fatal(err)
		}
		if *r.DonorCharge != c.charge || *r.CampaignAllocation != c.allocation || *r.PlatformNet != c.net || *r.ProviderFee != c.providerF {
			t.Errorf("%s/%s: charge %d allocation %d net %d provider %d", c.platform, c.provider, *r.DonorCharge, *r.CampaignAllocation,
				*r.PlatformNet, *r.ProviderFee)
		}
	}
	pol := &PolicyInput{VersionID: "pv", Terms: Terms{Model: ModelPercentage, RateBps: p64(500), Rounding: HalfUp}, Responsibility: PlatformAbsorbed,
		RefundTreatment: RefundProportional}
	if _, err := DonationFees(10_000, pol, nil, nil); !errors.Is(err, ErrCalcTerms) {
		t.Fatalf("a platform fee cannot be platform-absorbed: %v", err)
	}
}

func TestUnknownProviderFeeIsNeverZero(t *testing.T) {
	pol := &PolicyInput{VersionID: "pv", Terms: Terms{Model: ModelPercentage, RateBps: p64(500), Rounding: HalfUp}, Responsibility: OrganiserCovered,
		RefundTreatment: RefundProportional}
	// no provider evidence, organiser bears the provider fee: the donor charge is known, the allocation is not
	proc := &PolicyInput{VersionID: "pp", Terms: Terms{Model: ModelPassThrough, Rounding: HalfUp}, Responsibility: OrganiserCovered}
	r, err := DonationFees(10_000, pol, proc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Provider.Basis != BasisUnknown || r.ProviderFee != nil || r.CampaignAllocation != nil || r.DonorCharge == nil || *r.DonorCharge != 10_000 {
		t.Fatalf("unexpected: %+v", r)
	}
	// no policy decides who bears it: nothing provider-dependent is known
	r, err = DonationFees(10_000, pol, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.DonorCharge != nil || r.CampaignAllocation != nil || r.PlatformNet != nil {
		t.Fatalf("totals must be UNKNOWN without a provider-cost policy: %+v", r)
	}
	// no platform policy: nothing is charged, and that is labelled
	r, err = DonationFees(10_000, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Platform.Basis != BasisNotConfigured || *r.Platform.Amount != 0 {
		t.Fatalf("platform: %+v", r.Platform)
	}
}

func TestOrganiserBorneFeeMustBeBelowAmount(t *testing.T) {
	pol := &PolicyInput{VersionID: "pv", Terms: Terms{Model: ModelFixed, Fixed: p64(500), Rounding: HalfUp}, Responsibility: OrganiserCovered,
		RefundTreatment: RefundProportional}
	if _, err := DonationFees(500, pol, nil, nil); !errors.Is(err, ErrCalcExceeds) {
		t.Fatalf("want ErrCalcExceeds, got %v", err)
	}
	payPol := &PolicyInput{VersionID: "pp", Terms: Terms{Model: ModelPassThrough, Rounding: HalfUp}, Responsibility: OrganiserCovered}
	sch := &ScheduleInput{ScheduleID: "s", Terms: Terms{Model: ModelFixed, Fixed: p64(100), Rounding: HalfUp}, ConfirmationStatus: "SYNTHETIC"}
	if _, err := PayoutFees(100, payPol, sch); !errors.Is(err, ErrCalcExceeds) {
		t.Fatalf("payout: want ErrCalcExceeds, got %v", err)
	}
	r, err := PayoutFees(10_000, payPol, sch)
	if err != nil || *r.BeneficiaryReceives != 9_900 || *r.Fee.Amount != 100 {
		t.Fatalf("payout: %+v %v", r, err)
	}
	abs := &PolicyInput{VersionID: "pp", Terms: Terms{Model: ModelPassThrough, Rounding: HalfUp}, Responsibility: PlatformAbsorbed}
	if r, err := PayoutFees(10_000, abs, sch); err != nil || *r.BeneficiaryReceives != 10_000 || *r.Fee.Parts.Platform != 100 {
		t.Fatalf("platform-absorbed payout fee is not deducted from the payout: %+v %v", r, err)
	}
	donor := &PolicyInput{VersionID: "pp", Terms: Terms{Model: ModelPassThrough, Rounding: HalfUp}, Responsibility: DonorCovered}
	if _, err := PayoutFees(10_000, donor, sch); !errors.Is(err, ErrCalcTerms) {
		t.Fatalf("donors never bear payout fees: %v", err)
	}
}

func TestRefundReversals(t *testing.T) {
	// the brief's example: donation 100.00, platform fee 5.00, partial refund 40.00 → proportional reversal 2.00
	r, err := FeeReversal(500, 0, 10_000, 0, 4_000, 0, RefundProportional, HalfUp)
	if err != nil || r.Amount != 200 || r.Retained != 300 {
		t.Fatalf("partial refund: %+v %v", r, err)
	}
	// full refund reverses all of a proportional fee
	if r, _ := FeeReversal(500, 0, 10_000, 0, 10_000, 0, RefundProportional, HalfUp); r.Amount != 500 || r.Retained != 0 {
		t.Fatalf("full refund: %+v", r)
	}
	// fixed retained: fee 88 = 58 + 30 fixed; full refund reverses 58
	if r, _ := FeeReversal(88, 30, 2000, 0, 2000, 0, RefundProportionalExceptFixed, HalfUp); r.Amount != 58 || r.Retained != 30 {
		t.Fatalf("except fixed: %+v", r)
	}
	if r, _ := FeeReversal(500, 0, 10_000, 0, 10_000, 0, RefundNonRefundable, HalfUp); r.Amount != 0 || r.Retained != 500 {
		t.Fatalf("non-refundable: %+v", r)
	}
	// three partial refunds of 33.33, 33.33, 33.34 reverse exactly the fee (cumulative method: no drift)
	var reversed, refunded int64
	for _, part := range []int64{3333, 3333, 3334} {
		r, err := FeeReversal(500, 0, 10_000, refunded, part, reversed, RefundProportional, HalfUp)
		if err != nil {
			t.Fatal(err)
		}
		refunded += part
		reversed += r.Amount
	}
	if reversed != 500 {
		t.Fatalf("partial refunds reversed %d, want 500", reversed)
	}
	if _, err := FeeReversal(500, 0, 10_000, 6_000, 5_000, 300, RefundProportional, HalfUp); err == nil {
		t.Fatal("refunds above the original amount must be refused")
	}
}

func TestComputeIsDeterministicAndReproducible(t *testing.T) {
	pol := &PolicyInput{VersionID: "0199c0de-0000-7000-8000-000000000001", Terms: Terms{Model: ModelPctPlusFixed, RateBps: p64(250), Fixed: p64(25),
		Max: p64(5000), Rounding: HalfEven}, Responsibility: Shared, DonorShareBps: 2500, RefundTreatment: RefundProportionalExceptFixed}
	proc := &PolicyInput{VersionID: "0199c0de-0000-7000-8000-000000000002", Terms: Terms{Model: ModelPassThrough, Rounding: HalfUp},
		Responsibility: PlatformAbsorbed}
	sch := &ScheduleInput{ScheduleID: "0199c0de-0000-7000-8000-000000000003", Terms: Terms{Model: ModelPercentage, RateBps: p64(175)},
		ConfirmationStatus: "SYNTHETIC"}
	in := NewInputs(TxDonation, "USD", 12_345, "ECOCASH", "p", "c", "medical", "2026-10-15T00:00:00Z", pol, proc, sch)
	a, err := Compute(in)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Compute(in)
	if err != nil {
		t.Fatal(err)
	}
	if a.InputsHash != b.InputsHash || a.ResultHash != b.ResultHash || string(a.ResultJSON) != string(b.ResultJSON) {
		t.Fatal("same inputs gave different results")
	}
	// the provider rounding is unknown: the estimate says so
	if a.Components[1].Basis != BasisEstimated || a.Components[1].Rounding != HalfUp || a.Components[1].Note == "" {
		t.Fatalf("provider component: %+v", a.Components[1])
	}
	in.BasisMinor = "12346"
	c, _ := Compute(in)
	if c.InputsHash == a.InputsHash {
		t.Fatal("different inputs must hash differently")
	}
	in.AlgorithmVersion = "fees-calc/0"
	if _, err := Compute(in); err == nil {
		t.Fatal("an unknown algorithm version must be refused")
	}
}

// ---- property-based tests (reproducible seeds) ------------------------------------------------------------------

func randTerms(r *rand.Rand) Terms {
	modes := []string{HalfUp, HalfEven, Floor, Ceiling}
	t := Terms{Rounding: modes[r.IntN(4)]}
	rate := func() *int64 { return p64(r.Int64N(10_001)) }
	amtv := func() *int64 { return p64(r.Int64N(100_000)) }
	switch r.IntN(7) {
	case 0:
		t.Model = ModelZero
	case 1:
		t.Model, t.RateBps = ModelPercentage, rate()
	case 2:
		t.Model, t.Fixed = ModelFixed, amtv()
	case 3:
		t.Model, t.RateBps, t.Fixed = ModelPctPlusFixed, rate(), amtv()
	case 4:
		t.Model, t.RateBps = ModelCapped, rate()
		t.Max = amtv()
	case 5:
		t.Model, t.RateBps = ModelMinimum, rate()
		t.Min = amtv()
	case 6:
		t.Model = ModelTiered
		var up int64
		n := 1 + r.IntN(4)
		for i := 0; i < n; i++ {
			tr := Tier{RateBps: r.Int64N(10_001), FixedMinor: r.Int64N(1000)}
			if i < n-1 {
				up += 1 + r.Int64N(1_000_000)
				tr.UpToMinor = p64(up)
			}
			t.Tiers = append(t.Tiers, tr)
		}
	}
	return t
}

func TestPropertyFees(t *testing.T) {
	for _, seed := range []uint64{1, 2, 3, 20261015} {
		r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
		for i := 0; i < 20_000; i++ {
			terms := randTerms(r)
			basis := 1 + r.Int64N(1_000_000_000_000)
			f, err := terms.Amount(basis)
			if err != nil {
				t.Fatalf("seed %d #%d: %v (%+v, basis %d)", seed, i, err, terms, basis)
			}
			again, _ := terms.Amount(basis)
			if again.Amount != f.Amount {
				t.Fatalf("seed %d #%d: not deterministic", seed, i)
			}
			if f.Amount < 0 || (terms.Max != nil && f.Amount > *terms.Max) || (terms.Min != nil && terms.Max == nil && f.Amount < *terms.Min) {
				t.Fatalf("seed %d #%d: fee %d outside its bounds (%+v)", seed, i, f.Amount, terms)
			}
			if f.FixedPart > f.Amount || f.FixedPart < 0 {
				t.Fatalf("seed %d #%d: fixed part %d of %d", seed, i, f.FixedPart, f.Amount)
			}
			// rounding stays within one minor unit of the exact value (before clamps)
			if f.Clamped == "" {
				num, den := f.Exact.Num, f.Exact.Den
				lo, _ := RoundRational(Rational{num, den}, Floor)
				if f.Amount < lo || f.Amount > lo+1 {
					t.Fatalf("seed %d #%d: rounded %d far from floor %d", seed, i, f.Amount, lo)
				}
			}
		}
	}
}

func TestPropertyBreakdownReconciles(t *testing.T) {
	resps := []string{DonorCovered, OrganiserCovered, Shared}
	provResps := []string{DonorCovered, OrganiserCovered, Shared, PlatformAbsorbed}
	for _, seed := range []uint64{7, 11, 20261015} {
		r := rand.New(rand.NewPCG(seed, seed+1))
		for i := 0; i < 20_000; i++ {
			pt := randTerms(r)
			pol := &PolicyInput{VersionID: "pv", Terms: pt, Responsibility: resps[r.IntN(3)], DonorShareBps: 1 + r.Int64N(9_999),
				RefundTreatment: RefundProportional}
			proc := &PolicyInput{VersionID: "pp", Terms: Terms{Model: ModelPassThrough, Rounding: HalfUp}, Responsibility: provResps[r.IntN(4)],
				DonorShareBps: 1 + r.Int64N(9_999)}
			st := randTerms(r)
			for st.Model == ModelZero {
				st = randTerms(r)
			}
			sch := &ScheduleInput{ScheduleID: "s", Terms: st, ConfirmationStatus: "SYNTHETIC"}
			d := 1 + r.Int64N(10_000_000_000)
			res, err := DonationFees(d, pol, proc, sch)
			if errors.Is(err, ErrCalcExceeds) {
				continue // organiser-borne fees ≥ donation are refused, not computed
			}
			if err != nil {
				t.Fatalf("seed %d #%d: %v", seed, i, err)
			}
			for _, c := range []Component{res.Platform, res.Provider} {
				if *c.Parts.Donor+*c.Parts.Organiser+*c.Parts.Platform != *c.Amount {
					t.Fatalf("seed %d #%d: parts do not sum to the fee", seed, i)
				}
			}
			if *res.DonorCharge != *res.CampaignAllocation+*res.PlatformNet+*res.ProviderFee {
				t.Fatalf("seed %d #%d: breakdown does not reconcile", seed, i)
			}
			if *res.DonorCharge < d || *res.CampaignAllocation > d || *res.CampaignAllocation <= 0 {
				t.Fatalf("seed %d #%d: charge %d allocation %d donation %d", seed, i, *res.DonorCharge, *res.CampaignAllocation, d)
			}
		}
	}
}

func TestPropertyReversalsNeverExceedTheFee(t *testing.T) {
	treatments := []string{RefundProportional, RefundProportionalExceptFixed, RefundNonRefundable}
	modes := []string{HalfUp, HalfEven, Floor, Ceiling}
	r := rand.New(rand.NewPCG(42, 4242))
	for i := 0; i < 20_000; i++ {
		basis := 1 + r.Int64N(1_000_000)
		fee := r.Int64N(basis + 1)
		fixed := r.Int64N(fee + 1)
		tr, mode := treatments[r.IntN(3)], modes[r.IntN(4)]
		var refunded, reversed int64
		for refunded < basis {
			part := 1 + r.Int64N(basis-refunded)
			rv, err := FeeReversal(fee, fixed, basis, refunded, part, reversed, tr, mode)
			if err != nil {
				t.Fatalf("#%d: %v", i, err)
			}
			if rv.Amount < 0 {
				t.Fatalf("#%d: negative reversal", i)
			}
			refunded += part
			reversed += rv.Amount
		}
		want := map[string]int64{RefundProportional: fee, RefundProportionalExceptFixed: fee - fixed, RefundNonRefundable: 0}[tr]
		if reversed != want {
			t.Fatalf("#%d %s: fully refunded donation reversed %d, want %d", i, tr, reversed, want)
		}
	}
}
