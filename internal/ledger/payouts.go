package ledger

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
)

// Read API for the payouts module (Stage 11, ADR-043). Payouts never read ledger tables: they ask the ledger for
// a campaign's balances by account class and for the journal behind a deterministic key.

// CampaignAccount is one campaign-owned account's current exact balance and status.
type CampaignAccount struct {
	AccountID    string
	Code         string
	Class        string
	Status       string // ACTIVE | FROZEN | CLOSED
	BalanceMinor int64
}

// CampaignBalances returns the campaign's accounts in one currency (by class). A class with no account yet is
// absent (balance zero). Exact: v_balances_current includes unfolded entries.
func (s *Service) CampaignBalances(ctx context.Context, campaignID, currency string) (map[string]CampaignAccount, error) {
	if !uuidRE.MatchString(campaignID) {
		return nil, ErrNotFound
	}
	rows, err := s.Pool.Query(ctx, `SELECT a.id::text, a.code, a.account_class, a.status, c.balance_minor
		FROM ledger.ledger_accounts a JOIN ledger.v_balances_current c ON c.account_id = a.id
		WHERE a.owner_type = 'CAMPAIGN' AND a.owner_id = $1 AND a.currency = $2`, campaignID, currency)
	if err != nil {
		return nil, err
	}
	out := map[string]CampaignAccount{}
	for rows.Next() {
		var a CampaignAccount
		if err := rows.Scan(&a.AccountID, &a.Code, &a.Class, &a.Status, &a.BalanceMinor); err != nil {
			rows.Close()
			return nil, err
		}
		out[a.Class] = a
	}
	rows.Close()
	return out, rows.Err()
}

// JournalRef is a posted journal found by its idempotency key.
type JournalRef struct {
	TransactionID string
	Rule          string
	Currency      string
	Environment   string
	DebitMinor    int64
	ReversedBy    *string
}

// JournalByKey returns the journal posted under a key (ok=false if none).
func (s *Service) JournalByKey(ctx context.Context, key string) (JournalRef, bool, error) {
	var j JournalRef
	err := s.Pool.QueryRow(ctx, `SELECT t.id::text, t.posting_rule, t.currency, t.environment,
		(SELECT coalesce(sum(e.amount_minor), 0) FROM ledger.ledger_entries e WHERE e.transaction_id = t.id AND e.direction = 'DEBIT'),
		(SELECT r.id::text FROM ledger.ledger_transactions r WHERE r.reverses_transaction_id = t.id)
		FROM ledger.ledger_transactions t WHERE t.idempotency_key = $1`, key).
		Scan(&j.TransactionID, &j.Rule, &j.Currency, &j.Environment, &j.DebitMinor, &j.ReversedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return JournalRef{}, false, nil
	}
	return j, err == nil, err
}

// JournalsByKeys looks up many journals by idempotency key in one query (Stage 17 reconciliation: a run checks the
// expected journal of every matched subject). Keys that name no journal are absent from the map.
func (s *Service) JournalsByKeys(ctx context.Context, keys []string) (map[string]JournalRef, error) {
	out := make(map[string]JournalRef, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT t.idempotency_key, t.id::text, t.posting_rule, t.currency, t.environment,
		(SELECT coalesce(sum(e.amount_minor), 0) FROM ledger.ledger_entries e WHERE e.transaction_id = t.id AND e.direction = 'DEBIT'),
		(SELECT r.id::text FROM ledger.ledger_transactions r WHERE r.reverses_transaction_id = t.id)
		FROM ledger.ledger_transactions t WHERE t.idempotency_key = ANY ($1::text[])`, keys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var j JournalRef
		if err := rows.Scan(&k, &j.TransactionID, &j.Rule, &j.Currency, &j.Environment, &j.DebitMinor, &j.ReversedBy); err != nil {
			return nil, err
		}
		out[k] = j
	}
	return out, rows.Err()
}

// FixtureFundCampaign posts TEST_ONLY synthetic settled funds for a given campaign (capture → settlement →
// release to campaign_payable), so payout engineering tests have something to reserve. TEST ledger and
// SyntheticEnabled only, like every fixture; the funds are labelled TEST_ONLY and are never a donation.
const FixtureFundCampaign = "fund_campaign"

// FundCampaign posts the fund_campaign fixture for campaignID (amount in USD minor units).
func (s *Service) FundCampaign(ctx context.Context, staffID, campaignID string, amount int64, runID string) (FixtureRun, error) {
	env, err := s.Environment(ctx)
	if err != nil {
		return FixtureRun{}, err
	}
	if !s.SyntheticEnabled || env != EnvTest {
		return FixtureRun{}, ErrSyntheticDisabled
	}
	if !uuidRE.MatchString(campaignID) {
		return FixtureRun{}, invalid("campaign_id", "INVALID_FORMAT")
	}
	if amount <= 0 || amount > 100_000_000 {
		return FixtureRun{}, invalid("amount_minor", "INVALID_VALUE")
	}
	if runID == "" {
		runID = uuidFromSeed("ledger:fund_campaign:" + campaignID + ":" + strconv.FormatInt(s.now().UnixNano(), 10))
	}
	if !uuidRE.MatchString(runID) {
		return FixtureRun{}, invalid("run_id", "INVALID_FORMAT")
	}
	prov := SyntheticProviderID
	clearing, settled := AccountRef{Class: ClassPSPClearing, OwnerID: prov}, AccountRef{Class: ClassPSPSettled, OwnerID: prov}
	unsettled, payable := AccountRef{Class: ClassCampaignUnsettled, OwnerID: campaignID}, AccountRef{Class: ClassCampaignPayable, OwnerID: campaignID}
	run := FixtureRun{RunID: runID, Scenario: FixtureFundCampaign, CampaignID: campaignID, Label: "TEST_ONLY"}
	actor := Actor{Type: "STAFF", ID: staffID}
	if staffID == "" {
		actor = Actor{Type: "SYSTEM", ID: "ledger.synthetic_fixture"}
	}
	pay, item := uuidFromSeed("ledger:synthetic_payment:"+runID+":1"), uuidFromSeed("ledger:synthetic_settlement_item:"+runID)
	steps := []struct {
		rule, step, srcType, srcID string
		lines                      []Line
	}{
		{"DONATION_CAPTURED", "capture_1", "payment", pay, []Line{Dr(clearing, amount), Cr(unsettled, amount)}},
		{"SETTLEMENT_MATCHED", "settlement", "settlement_item", item, []Line{Dr(settled, amount), Cr(clearing, amount)}},
		{"RELEASE", "release", "payment", pay, []Line{Dr(unsettled, amount), Cr(payable, amount)}},
	}
	for _, st := range steps {
		res, err := s.PostOnce(ctx, Posting{Rule: st.rule, IdempotencyKey: "fixture:" + runID + ":" + st.step, Currency: "USD", OccurredAt: s.now(),
			Description: "TEST_ONLY synthetic fixture fund_campaign / " + st.step, SourceSystem: SourceSynthetic, SourceType: st.srcType,
			SourceID: st.srcID, SourceEventID: "fixture:" + runID + ":" + st.step, EvidenceClass: EvidenceSimulated, Actor: actor,
			Metadata: map[string]string{"fixture": "TEST_ONLY", "run": runID, "scenario": FixtureFundCampaign, "step": st.step}, Lines: st.lines})
		if err != nil {
			return run, err
		}
		run.Journals = append(run.Journals, res.TransactionID)
	}
	return run, nil
}

// IntegrityConfirmed reports whether the latest integrity run of every invariant passed (EC-19). No run yet ⇒ false.
func (s *Service) IntegrityConfirmed(ctx context.Context) (bool, error) {
	checks, err := s.LatestIntegrity(ctx)
	if err != nil || len(checks) == 0 {
		return false, err
	}
	for _, c := range checks {
		if c.Status != "PASSED" {
			return false, nil
		}
	}
	return true, nil
}
