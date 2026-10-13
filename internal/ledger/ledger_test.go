package ledger

import (
	"context"
	"errors"
	"math/rand"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/errs"
)

const (
	campA = "11111111-1111-4111-8111-111111111111"
	campB = "22222222-2222-4222-8222-222222222222"
	prov  = "33333333-3333-4333-8333-333333333333"
	src   = "44444444-4444-4444-8444-444444444444"
)

// testService has a fixed rule registry (no database): a subset of the migration's rules.
func testService() *Service {
	s := &Service{Clock: fixedClock{time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)}}
	mk := func(code string, src []string, mirror, approval, single bool, lines ...string) Rule {
		r := Rule{Code: code, Version: 1, SourceTypes: src, MirrorOfReversed: mirror, RequiresApproval: approval, SingleOwnerPerType: single,
			Lines: map[string]bool{}}
		for _, l := range lines {
			req := strings.HasSuffix(l, "!")
			l = strings.TrimSuffix(l, "!")
			r.Lines[l] = true
			if req {
				r.Required = append(r.Required, l)
			}
		}
		return r
	}
	s.rules = map[string]Rule{
		"DONATION_CAPTURED": mk("DONATION_CAPTURED", []string{"payment"}, false, false, true,
			"DEBIT|asset:psp_clearing!", "CREDIT|liability:campaign_unsettled!", "CREDIT|revenue:platform_fees"),
		"PSP_FEE": mk("PSP_FEE", []string{"payment"}, false, false, true,
			"DEBIT|liability:campaign_unsettled", "DEBIT|expense:psp_processing_fees", "CREDIT|asset:psp_clearing!"),
		"PAYOUT_RESERVED": mk("PAYOUT_RESERVED", []string{"payout"}, false, false, true,
			"DEBIT|liability:campaign_payable!", "CREDIT|liability:campaign_payout_pending!"),
		"REVERSAL":   mk("REVERSAL", []string{"adjustment"}, true, true, false),
		"ADJUSTMENT": mk("ADJUSTMENT", []string{"adjustment"}, false, true, false, "DEBIT|liability:campaign_unsettled", "CREDIT|liability:campaign_unsettled"),
	}
	return s
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

func capture(gross, fee int64) Posting {
	lines := []Line{Dr(AccountRef{ClassPSPClearing, prov}, gross), Cr(AccountRef{ClassCampaignUnsettled, campA}, gross-fee)}
	if fee > 0 {
		lines = append(lines, Cr(AccountRef{Class: ClassPlatformFees}, fee))
	}
	return Posting{Rule: "DONATION_CAPTURED", IdempotencyKey: "payment:" + src + ":capture", Currency: "USD",
		OccurredAt: time.Date(2026, 10, 13, 11, 0, 0, 0, time.UTC), Description: "TEST_ONLY capture", SourceSystem: SourceSynthetic,
		SourceType: "payment", SourceID: src, EvidenceClass: EvidenceSimulated, Actor: Actor{Type: "SYSTEM", ID: "test"}, Lines: lines}
}

func code(err error) string {
	if e := errs.As(err); e != nil {
		if len(e.Details) > 0 {
			return e.Code + ":" + e.Details[0].Code
		}
		return e.Code
	}
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestValidateAcceptsBalancedJournal(t *testing.T) {
	s := testService()
	if _, err := s.Validate(context.Background(), EnvTest, capture(5000, 250)); err != nil {
		t.Fatalf("balanced journal refused: %v", err)
	}
}

func TestValidateRejectsEveryInvalidShape(t *testing.T) {
	s := testService()
	ctx := context.Background()
	cases := []struct {
		name string
		mut  func(*Posting)
		env  string
		want string
	}{
		{"unbalanced", func(p *Posting) { p.Lines[0].Amount++ }, EnvTest, "LEDGER_UNBALANCED"},
		{"single line", func(p *Posting) { p.Lines = p.Lines[:1] }, EnvTest, "LEDGER_INVALID_POSTING:AT_LEAST_TWO"},
		{"zero amount", func(p *Posting) { p.Lines = []Line{Dr(p.Lines[0].Account, 0), Cr(p.Lines[1].Account, 0)} }, EnvTest, "LEDGER_INVALID_POSTING:AMOUNT_NOT_POSITIVE"},
		{"negative amount", func(p *Posting) { p.Lines[0].Amount, p.Lines[1].Amount = -5000, -5000; p.Lines = p.Lines[:2] }, EnvTest, "LEDGER_INVALID_POSTING:AMOUNT_NOT_POSITIVE"},
		{"bad currency", func(p *Posting) { p.Currency = "usd" }, EnvTest, "LEDGER_CURRENCY_NOT_POSTABLE"},
		{"unknown account class", func(p *Posting) { p.Lines[0].Account.Class = "asset:wallet" }, EnvTest, "LEDGER_INVALID_POSTING:UNKNOWN_ACCOUNT_CLASS"},
		{"platform account with owner", func(p *Posting) { p.Lines[2].Account.OwnerID = campA }, EnvTest, "LEDGER_INVALID_POSTING:INVALID_OWNER"},
		{"owned account without owner", func(p *Posting) { p.Lines[1].Account.OwnerID = "" }, EnvTest, "LEDGER_INVALID_POSTING:INVALID_OWNER"},
		{"line not allowed by rule", func(p *Posting) { p.Lines[1].Account = AccountRef{ClassCampaignPayable, campA} }, EnvTest, "LEDGER_RULE_VIOLATION"},
		{"required line missing", func(p *Posting) {
			p.Lines = []Line{Dr(AccountRef{ClassPSPClearing, prov}, 100), Cr(AccountRef{Class: ClassPlatformFees}, 100)}
		}, EnvTest, "LEDGER_RULE_VIOLATION"},
		{"two campaigns in one journal", func(p *Posting) {
			p.Lines = append(p.Lines, Cr(AccountRef{ClassCampaignUnsettled, campB}, 1))
			p.Lines[0].Amount++
		}, EnvTest, "LEDGER_RULE_VIOLATION"},
		{"unknown rule", func(p *Posting) { p.Rule = "MAKE_MONEY" }, EnvTest, "LEDGER_INVALID_POSTING:UNKNOWN_RULE"},
		{"source type not allowed", func(p *Posting) { p.SourceType = "payout" }, EnvTest, "LEDGER_INVALID_POSTING:NOT_ALLOWED_BY_RULE"},
		{"bad key", func(p *Posting) { p.IdempotencyKey = "Payment 1" }, EnvTest, "LEDGER_INVALID_POSTING:INVALID_FORMAT"},
		{"future occurred_at", func(p *Posting) { p.OccurredAt = p.OccurredAt.Add(48 * time.Hour) }, EnvTest, "LEDGER_INVALID_POSTING:INVALID_VALUE"},
		{"rejected evidence", func(p *Posting) { p.EvidenceClass = EvidenceRejected }, EnvTest, "LEDGER_EVIDENCE_NOT_POSTABLE"},
		{"simulated evidence in production", func(p *Posting) {}, EnvProduction, "LEDGER_PRODUCTION_POSTING_DISABLED"},
		{"synthetic outside TEST", func(p *Posting) { p.EvidenceClass = EvidenceProviderVerified }, EnvTest, "LEDGER_ENVIRONMENT_MISMATCH"},
		{"payments-sourced journal in a TEST ledger", func(p *Posting) { p.SourceSystem, p.EvidenceClass = SourcePayments, EvidenceProviderReported },
			EnvTest, "LEDGER_ENVIRONMENT_MISMATCH"},
		{"reversal field on a non-reversal rule", func(p *Posting) { p.Reverses = src }, EnvTest, "LEDGER_INVALID_POSTING:REVERSAL_RULE_MISMATCH"},
		{"approval rule without adjustment", func(p *Posting) {
			p.Rule, p.SourceType = "ADJUSTMENT", "adjustment"
			p.Lines = []Line{Dr(AccountRef{ClassCampaignUnsettled, campA}, 1), Cr(AccountRef{ClassCampaignUnsettled, campB}, 1)}
		}, EnvTest, "LEDGER_INVALID_POSTING:APPROVAL_REQUIRED"},
		{"amount overflow", func(p *Posting) {
			p.Lines = []Line{Dr(AccountRef{ClassPSPClearing, prov}, 1<<62), Dr(AccountRef{ClassPSPClearing, prov}, 1<<62),
				Cr(AccountRef{ClassCampaignUnsettled, campA}, 1)}
		}, EnvTest, "LEDGER_INVALID_POSTING:AMOUNT_TOO_LARGE"},
	}
	for _, c := range cases {
		p := capture(5000, 250)
		c.mut(&p)
		_, err := s.Validate(ctx, c.env, p)
		if got := code(err); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
	if ProductionPostingEnabled {
		t.Fatal("ProductionPostingEnabled must stay false in Stage 10")
	}
}

// TestChartMatchesSQL keeps the Go chart identical to ledger.account_class_signature in the migration.
func TestChartMatchesSQL(t *testing.T) {
	b, err := os.ReadFile("../../migrations/20261013120000_ledger.sql")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`WHEN '([a-z_:]+)'\s+THEN '([A-Z]+)\|([A-Z]+)\|([A-Z_]+)\|([A-Z]+)'`)
	m := re.FindAllStringSubmatch(string(b), -1)
	if len(m) != len(Chart) {
		t.Fatalf("SQL chart has %d classes, Go %d", len(m), len(Chart))
	}
	for _, x := range m {
		got, ok := Chart[x[1]]
		if !ok || got != (ClassSpec{x[2], x[3], x[4], x[5]}) {
			t.Errorf("%s: Go %+v SQL %v", x[1], got, x[2:])
		}
	}
}

func TestParseAccountCode(t *testing.T) {
	for _, c := range []struct {
		code string
		ok   bool
	}{{"revenue:platform_fees", true}, {"liability:campaign_payable:" + campA, true}, {"liability:campaign_payable", false},
		{"revenue:platform_fees:" + campA, false}, {"liability:campaign_payable:NOT-A-UUID", false}, {"asset:wallet:" + campA, false}} {
		ref, ok := ParseAccountCode(c.code)
		if ok != c.ok || (ok && ref.Code() != c.code) {
			t.Errorf("%s: ok=%v ref=%+v", c.code, ok, ref)
		}
	}
}

// ---- property-based tests (reproducible seeds) -----------------------------------------------------------------

var propertySeeds = []int64{1, 7, 42, 20261013, 987654321}

// randomJournal builds a valid ADJUSTMENT-shaped journal on campaign accounts: random positive debits, credits
// partitioned to the same total.
func randomJournal(r *rand.Rand) []Line {
	n := 1 + r.Intn(5)
	var lines []Line
	var total int64
	for i := 0; i < n; i++ {
		a := 1 + r.Int63n(1_000_000)
		total += a
		lines = append(lines, Dr(AccountRef{ClassCampaignUnsettled, campA}, a))
	}
	k := 1 + r.Intn(4)
	rest := total
	for i := 0; i < k-1 && rest > 1; i++ {
		a := 1 + r.Int63n(rest-1)
		lines = append(lines, Cr(AccountRef{ClassCampaignUnsettled, campA}, a))
		rest -= a
	}
	lines = append(lines, Cr(AccountRef{ClassCampaignUnsettled, campA}, rest))
	return lines
}

func adj(lines []Line) Posting {
	return Posting{Rule: "ADJUSTMENT", IdempotencyKey: "adjustment:" + src, Currency: "USD", OccurredAt: time.Date(2026, 10, 13, 11, 0, 0, 0, time.UTC),
		Description: "property", SourceSystem: SourceStaff, SourceType: "adjustment", SourceID: src, EvidenceClass: EvidenceManuallyReviewed,
		AdjustmentID: src, Reason: "property-based test journal", Actor: Actor{Type: "STAFF", ID: src}, Lines: lines}
}

func TestPropertyValidJournalsBalanceAndInvalidOnesAreRejected(t *testing.T) {
	s := testService()
	ctx := context.Background()
	for _, seed := range propertySeeds {
		r := rand.New(rand.NewSource(seed))
		for i := 0; i < 2000; i++ {
			lines := randomJournal(r)
			var dr, cr int64
			for _, l := range lines {
				if l.Direction == Debit {
					dr += l.Amount
				} else {
					cr += l.Amount
				}
			}
			if dr != cr {
				t.Fatalf("seed %d: generator produced an unbalanced journal", seed)
			}
			if _, err := s.Validate(ctx, EnvTest, adj(lines)); err != nil {
				t.Fatalf("seed %d iter %d: valid journal refused: %v", seed, i, err)
			}
			// any single mutation of an amount, a direction or the line count breaks validity
			bad := append([]Line(nil), lines...)
			j := r.Intn(len(bad))
			switch r.Intn(3) {
			case 0:
				bad[j].Amount += 1 + r.Int63n(100)
			case 1:
				if bad[j].Direction == Debit {
					bad[j].Direction = Credit
				} else {
					bad[j].Direction = Debit
				}
			case 2:
				bad = bad[:1]
			}
			if _, err := s.Validate(ctx, EnvTest, adj(bad)); err == nil {
				t.Fatalf("seed %d iter %d: invalid journal accepted: %+v", seed, i, bad)
			}
		}
	}
}

// in-memory model: balances from journal lines in normal-balance terms; a reversal (mirror) cancels exactly.
func TestPropertyReversalCancelsAndAggregationMatches(t *testing.T) {
	for _, seed := range propertySeeds {
		r := rand.New(rand.NewSource(seed))
		balances := map[string]int64{}
		apply := func(lines []Line) {
			for _, l := range lines {
				spec := Chart[l.Account.Class]
				if l.Direction == spec.NormalBalance {
					balances[l.Account.Code()] += l.Amount
				} else {
					balances[l.Account.Code()] -= l.Amount
				}
			}
		}
		var journals [][]Line
		var dr, cr int64
		for i := 0; i < 500; i++ {
			gross := 1 + r.Int63n(10_000_000)
			fee := r.Int63n(gross)
			p := capture(gross, fee)
			if fee == 0 {
				p.Lines = p.Lines[:2]
			}
			journals = append(journals, p.Lines)
			apply(p.Lines)
			for _, l := range p.Lines {
				if l.Direction == Debit {
					dr += l.Amount
				} else {
					cr += l.Amount
				}
			}
		}
		if dr != cr {
			t.Fatalf("seed %d: trial balance %d != %d", seed, dr, cr)
		}
		before := map[string]int64{}
		for k, v := range balances {
			before[k] = v
		}
		// reverse a random subset twice-safe: mirror each chosen journal once
		chosen := r.Perm(len(journals))[:len(journals)/3]
		for _, i := range chosen {
			var mirror []Line
			for _, l := range journals[i] {
				inv := Credit
				if l.Direction == Credit {
					inv = Debit
				}
				mirror = append(mirror, Line{Account: l.Account, Direction: inv, Amount: l.Amount})
			}
			apply(mirror)
			apply(journals[i]) // re-posting the original restores the state: reversal ∘ original = identity
		}
		for k, v := range before {
			if balances[k] != v {
				t.Fatalf("seed %d: %s %d != %d after reversal+repost", seed, k, balances[k], v)
			}
		}
		// idempotent replay: the equivalence key of a journal is order-independent and amount-sensitive
		p := capture(5000, 250)
		q := p
		q.Lines = []Line{p.Lines[2], p.Lines[0], p.Lines[1]}
		if strings.Join(sortedKeys(p.Lines), ",") != strings.Join(sortedKeys(q.Lines), ",") {
			t.Fatal("equivalence depends on line order")
		}
		q.Lines = append([]Line(nil), q.Lines...)
		q.Lines[0].Amount++
		if strings.Join(sortedKeys(p.Lines), ",") == strings.Join(sortedKeys(q.Lines), ",") {
			t.Fatal("equivalence ignores amounts")
		}
	}
}

func TestMapDBErrorPassesThroughKnownErrors(t *testing.T) {
	if !errors.Is(mapDBError(errRetry), errRetry) || mapDBError(nil) != nil {
		t.Fatal("mapDBError changed a sentinel")
	}
	if e := errs.As(mapDBError(ErrUnbalanced)); e == nil || e.Code != "LEDGER_UNBALANCED" {
		t.Fatal("mapDBError changed a ledger error")
	}
}
