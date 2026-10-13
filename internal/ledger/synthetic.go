package ledger

import (
	"context"
	"crypto/md5"
	"fmt"

	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Synthetic fixtures (brief §21) are TEST_ONLY journals that exercise the posting rules with deterministic amounts.
// They exist only in a TEST ledger (source_system = synthetic_fixture ⇒ environment TEST and evidence SIMULATED,
// enforced by CHECK), carry metadata {"fixture":"TEST_ONLY"} and a "TEST_ONLY" description, use synthetic campaign
// and provider ids that belong to no real campaign or provider, and are never a donor contribution.

// SyntheticProviderID is the synthetic provider every fixture uses (md5('ledger:synthetic_provider')).
var SyntheticProviderID = uuidFromSeed("ledger:synthetic_provider")

func uuidFromSeed(seed string) string {
	h := md5.Sum([]byte(seed))
	return fmt.Sprintf("%x-%x-%x-%x-%x", h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

// Fixture scenarios.
const (
	FixtureDonations      = "donations"      // USD 10.00 and USD 25.00 captured, USD 0.50 provider fee
	FixturePartialRefund  = "partial_refund" // USD 100.00 captured, USD 30.00 partial refund reserved and settled (USD 70.00 remains)
	FixtureSettlement     = "settlement"     // USD 100.00 captured, settled and released to available
	FixtureRefundReserved = "refund_5"       // USD 25.00 captured, USD 5.00 refund reserved and settled
)

// Fixtures lists the scenarios.
var Fixtures = []string{FixtureDonations, FixturePartialRefund, FixtureSettlement, FixtureRefundReserved}

var ErrSyntheticDisabled = errs.New(errs.Forbidden, "LEDGER_SYNTHETIC_DISABLED", "Synthetic fixtures are available only in a TEST ledger outside production.")

// FixtureRun is the result of one fixture scenario.
type FixtureRun struct {
	RunID      string   `json:"run_id"`
	Scenario   string   `json:"scenario"`
	CampaignID string   `json:"synthetic_campaign_id"`
	Journals   []string `json:"transaction_ids"`
	Label      string   `json:"label"`
}

// PostFixture posts one scenario under a fresh synthetic campaign (each run is independent and idempotent per run id).
func (s *Service) PostFixture(ctx context.Context, staffID, scenario, runID string) (FixtureRun, error) {
	env, err := s.Environment(ctx)
	if err != nil {
		return FixtureRun{}, err
	}
	if !s.SyntheticEnabled || env != EnvTest {
		return FixtureRun{}, ErrSyntheticDisabled
	}
	if runID == "" {
		runID = ids.New()
	}
	if !uuidRE.MatchString(runID) {
		return FixtureRun{}, invalid("run_id", "INVALID_FORMAT")
	}
	camp := uuidFromSeed("ledger:synthetic_campaign:" + runID)
	prov := SyntheticProviderID
	clearing, settled := AccountRef{Class: ClassPSPClearing, OwnerID: prov}, AccountRef{Class: ClassPSPSettled, OwnerID: prov}
	unsettled, payable := AccountRef{Class: ClassCampaignUnsettled, OwnerID: camp}, AccountRef{Class: ClassCampaignPayable, OwnerID: camp}
	refundPayable := AccountRef{Class: ClassRefundPayable}
	run := FixtureRun{RunID: runID, Scenario: scenario, CampaignID: camp, Label: "TEST_ONLY"}
	actor := Actor{Type: "STAFF", ID: staffID}
	if staffID == "" {
		actor = Actor{Type: "SYSTEM", ID: "ledger.synthetic_fixture"}
	}
	post := func(rule, step, srcType, srcID string, lines ...Line) error {
		res, err := s.PostOnce(ctx, Posting{Rule: rule, IdempotencyKey: "fixture:" + runID + ":" + step, Currency: "USD", OccurredAt: s.now(),
			Description: "TEST_ONLY synthetic fixture " + scenario + " / " + step, SourceSystem: SourceSynthetic, SourceType: srcType,
			SourceID: srcID, SourceEventID: "fixture:" + runID + ":" + step, EvidenceClass: EvidenceSimulated, Actor: actor,
			Metadata: map[string]string{"fixture": "TEST_ONLY", "run": runID, "scenario": scenario, "step": step}, Lines: lines})
		if err != nil {
			return err
		}
		run.Journals = append(run.Journals, res.TransactionID)
		return nil
	}
	pay := func(n string) string { return uuidFromSeed("ledger:synthetic_payment:" + runID + ":" + n) }
	switch scenario {
	case FixtureDonations:
		p1, p2 := pay("1"), pay("2")
		if err := post("DONATION_CAPTURED", "capture_1", "payment", p1, Dr(clearing, 1000), Cr(unsettled, 1000)); err != nil {
			return run, err
		}
		if err := post("DONATION_CAPTURED", "capture_2", "payment", p2, Dr(clearing, 2500), Cr(unsettled, 2500)); err != nil {
			return run, err
		}
		if err := post("PSP_FEE", "psp_fee_1", "payment", p1, Dr(unsettled, 50), Cr(clearing, 50)); err != nil {
			return run, err
		}
	case FixturePartialRefund:
		p1, r1 := pay("1"), uuidFromSeed("ledger:synthetic_refund:"+runID)
		if err := post("DONATION_CAPTURED", "capture_1", "payment", p1, Dr(clearing, 10000), Cr(unsettled, 10000)); err != nil {
			return run, err
		}
		// a partial refund is a new journal for the refunded part — never a reversal of the whole donation
		if err := post("REFUND_RESERVED", "refund_reserved", "refund", r1, Dr(unsettled, 3000), Cr(refundPayable, 3000)); err != nil {
			return run, err
		}
		if err := post("REFUND_SETTLED", "refund_settled", "refund", r1, Dr(refundPayable, 3000), Cr(clearing, 3000)); err != nil {
			return run, err
		}
	case FixtureSettlement:
		p1, b1 := pay("1"), uuidFromSeed("ledger:synthetic_settlement_item:"+runID)
		if err := post("DONATION_CAPTURED", "capture_1", "payment", p1, Dr(clearing, 10000), Cr(unsettled, 10000)); err != nil {
			return run, err
		}
		if err := post("SETTLEMENT_MATCHED", "settlement", "settlement_item", b1, Dr(settled, 10000), Cr(clearing, 10000)); err != nil {
			return run, err
		}
		if err := post("RELEASE", "release", "payment", p1, Dr(unsettled, 10000), Cr(payable, 10000)); err != nil {
			return run, err
		}
	case FixtureRefundReserved:
		p1, r1 := pay("1"), uuidFromSeed("ledger:synthetic_refund:"+runID)
		if err := post("DONATION_CAPTURED", "capture_1", "payment", p1, Dr(clearing, 2500), Cr(unsettled, 2500)); err != nil {
			return run, err
		}
		if err := post("REFUND_RESERVED", "refund_reserved", "refund", r1, Dr(unsettled, 500), Cr(refundPayable, 500)); err != nil {
			return run, err
		}
		if err := post("REFUND_SETTLED", "refund_settled", "refund", r1, Dr(refundPayable, 500), Cr(clearing, 500)); err != nil {
			return run, err
		}
	default:
		return run, invalid("scenario", "UNKNOWN_SCENARIO")
	}
	return run, nil
}
