package reconciliation

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

const (
	pay1 = "0192a000-0000-7000-8000-000000000001"
	pay2 = "0192a000-0000-7000-8000-000000000002"
	out1 = "0192a000-0000-7000-8000-0000000000b1"
)

func csvFile(stmt []string, rows ...[]string) []byte {
	var b strings.Builder
	b.WriteString(strings.Join(CSVHeader, ",") + "\n")
	line := func(vals map[string]string) {
		cells := make([]string, len(CSVHeader))
		for i, h := range CSVHeader {
			cells[i] = vals[h]
		}
		b.WriteString(strings.Join(cells, ",") + "\n")
	}
	m := map[string]string{}
	for i := 0; i+1 < len(stmt); i += 2 {
		m[stmt[i]] = stmt[i+1]
	}
	line(m)
	for _, r := range rows {
		m := map[string]string{}
		for i := 0; i+1 < len(r); i += 2 {
			m[r[i]] = r[i+1]
		}
		line(m)
	}
	return []byte(b.String())
}

func stmtRow(declared int) []string {
	return []string{"record_type", "STATEMENT", "ref", "ST-1", "currency", "USD", "period_start", "2026-10-01T00:00:00Z",
		"period_end", "2026-10-21T00:00:00Z", "declared_lines", fmt.Sprint(declared)}
}

func payLine(ref, id, amount string, extra ...string) []string {
	r := []string{"record_type", "LINE", "ref", ref, "kind", "PAYMENT", "direction", "CREDIT", "status", "SUCCEEDED", "amount_minor", amount,
		"currency", "USD", "provider_ref", "sbx_" + ref, "merchant_ref", id, "booked_at", "2026-10-10T10:00:00+02:00"}
	return append(r, extra...)
}

func codes(err error) []string {
	var fe *FormatError
	if !errors.As(err, &fe) {
		return nil
	}
	out := []string{fe.Code}
	for _, e := range fe.Errors {
		out = append(out, e.Field+":"+e.Code)
	}
	return out
}

func hasCode(err error, want string) bool {
	for _, c := range codes(err) {
		if c == want || strings.HasSuffix(c, ":"+want) {
			return true
		}
	}
	return false
}

func TestParseCSVAndJSONAgree(t *testing.T) {
	c := csvFile(stmtRow(2), payLine("L1", pay1, "1250"), payLine("L2", pay2, "990", "fee_minor", "30", "net_minor", "960"))
	st, err := ParseStatement(c, "text/csv", KindTransactionReport, now)
	if err != nil {
		t.Fatalf("csv: %v %v", err, codes(err))
	}
	if len(st.Lines) != 2 || st.Lines[1].Amount != 990 || *st.Lines[1].Fee != 30 || st.Lines[0].BookedAt != time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC) {
		t.Fatalf("parsed %+v", st.Lines)
	}
	j := []byte(`{"format":"FUNDZIM_CANONICAL_V1","statement":{"ref":"ST-1","currency":"USD","period_start":"2026-10-01T00:00:00Z",
		"period_end":"2026-10-21T00:00:00Z","declared_lines":"1"},"lines":[{"ref":"L1","kind":"PAYMENT","direction":"CREDIT","status":"SUCCEEDED",
		"amount_minor":"1250","currency":"USD","merchant_ref":"` + pay1 + `","booked_at":"2026-10-10T08:00:00Z"}]}`)
	sj, err := ParseStatement(j, "application/json", KindTransactionReport, now)
	if err != nil {
		t.Fatalf("json: %v %v", err, codes(err))
	}
	if sj.Lines[0].Amount != 1250 || sj.Lines[0].MerchantRef != pay1 {
		t.Fatalf("json %+v", sj.Lines[0])
	}
}

func TestParseRejectsBadEvidence(t *testing.T) {
	ok := payLine("L1", pay1, "1250")
	cases := []struct {
		name  string
		file  []byte
		media string
		kind  string
		want  string
	}{
		{"decimal amount", csvFile(stmtRow(1), payLine("L1", pay1, "12.50")), "text/csv", KindTransactionReport, "NOT_MINOR_UNITS"},
		{"negative amount", csvFile(stmtRow(1), payLine("L1", pay1, "-5")), "text/csv", KindTransactionReport, "NOT_MINOR_UNITS"},
		{"exponent", csvFile(stmtRow(1), payLine("L1", pay1, "1e3")), "text/csv", KindTransactionReport, "NOT_MINOR_UNITS"},
		{"zero amount", csvFile(stmtRow(1), payLine("L1", pay1, "0")), "text/csv", KindTransactionReport, "OUT_OF_RANGE"},
		{"declared count", csvFile(stmtRow(2), ok), "text/csv", KindTransactionReport, "DECLARED_COUNT_MISMATCH"},
		{"duplicate row", csvFile(stmtRow(2), ok, ok), "text/csv", KindTransactionReport, "DUPLICATE_ROW"},
		{"conflicting line", csvFile(stmtRow(2), ok, payLine("L1", pay1, "1251")), "text/csv", KindTransactionReport, "CONFLICTING_LINE_IN_FILE"},
		{"unsupported currency", csvFile(stmtRow(1), append(payLine("L1", pay1, "5"), "currency", "EUR")), "text/csv", KindTransactionReport, "UNSUPPORTED_CURRENCY"},
		{"outside period", csvFile(stmtRow(1), append(payLine("L1", pay1, "5"), "booked_at", "2026-11-01T00:00:00Z")), "text/csv", KindTransactionReport, "OUTSIDE_PERIOD"},
		{"no reference", csvFile(stmtRow(1), append(payLine("L1", "", "5"), "provider_ref", "")), "text/csv", KindTransactionReport, "A_REFERENCE_IS_REQUIRED"},
		{"formula in reference", csvFile(stmtRow(1), payLine("=CMD()", pay1, "5")), "text/csv", KindTransactionReport, "INVALID_REFERENCE"},
		{"bank line in provider report", csvFile(stmtRow(1), []string{"record_type", "LINE", "ref", "B1", "kind", "BANK_TRANSFER", "direction", "CREDIT",
			"status", "BOOKED", "amount_minor", "5", "currency", "USD", "counterparty_ref", "X", "booked_at", "2026-10-10T00:00:00Z"}), "text/csv",
			KindTransactionReport, "NOT_ALLOWED_FOR_SOURCE"},
		{"batch outside settlement report", csvFile(stmtRow(0), []string{"record_type", "BATCH", "ref", "B", "currency", "USD", "value_date", "2026-10-11",
			"gross_credits_minor", "0", "gross_debits_minor", "0", "fees_minor", "0", "net_minor", "0"}), "text/csv", KindTransactionReport,
			"BATCHES_IN_SETTLEMENT_REPORTS_ONLY"},
		{"bank without account", csvFile(stmtRow(0)), "text/csv", KindBankStatement, "REQUIRED"},
		{"future period", csvFile([]string{"record_type", "STATEMENT", "ref", "S", "period_start", "2026-10-01T00:00:00Z", "period_end",
			"2026-12-30T00:00:00Z", "declared_lines", "0"}), "text/csv", KindTransactionReport, "PERIOD_IN_FUTURE"},
		{"wrong header", []byte("record_type,ref\nSTATEMENT,S\n"), "text/csv", KindTransactionReport, "MALFORMED_CSV"},
		{"json number amount", []byte(`{"format":"FUNDZIM_CANONICAL_V1","statement":{"ref":"S","period_start":"2026-10-01T00:00:00Z",
			"period_end":"2026-10-02T00:00:00Z","declared_lines":1},"lines":[]}`), "application/json", KindTransactionReport, "NOT_A_STRING"},
		{"json unknown field", []byte(`{"format":"FUNDZIM_CANONICAL_V1","statement":{"ref":"S","evil":"x"},"lines":[]}`), "application/json",
			KindTransactionReport, "UNKNOWN_FIELD"},
		{"json other format", []byte(`{"format":"PROVIDER_X","statement":{}}`), "application/json", KindTransactionReport, "NOT_CANONICAL_V1"},
		{"nul byte", []byte("record_type\x00"), "text/csv", KindTransactionReport, "INVALID_ENCODING"},
	}
	for _, c := range cases {
		_, err := ParseStatement(c.file, c.media, c.kind, now)
		if !hasCode(err, c.want) {
			t.Errorf("%s: want %s, got %v", c.name, c.want, codes(err))
		}
	}
}

// A line in another currency than the statement's is kept (the matcher compares it with the record and raises
// CURRENCY_MISMATCH); the provider's spelling is normalised for comparison and kept verbatim as evidence.
func TestParseCurrencyAliasKeepsRawCode(t *testing.T) {
	st, err := ParseStatement(csvFile(stmtRow(1), append(payLine("L1", pay1, "5"), "currency", "ZiG")), "text/csv", KindTransactionReport, now)
	if err != nil {
		t.Fatal(codes(err))
	}
	if st.Lines[0].Currency != "ZWG" || st.Lines[0].RawCurrency != "ZiG" {
		t.Fatalf("currency %s raw %s", st.Lines[0].Currency, st.Lines[0].RawCurrency)
	}
}

func settlementFile(net string, lineFee string) []byte {
	return csvFile([]string{"record_type", "STATEMENT", "ref", "SET-1", "currency", "USD", "period_start", "2026-10-01T00:00:00Z",
		"period_end", "2026-10-21T00:00:00Z", "declared_lines", "2"},
		[]string{"record_type", "BATCH", "ref", "B-1", "currency", "USD", "value_date", "2026-10-12", "gross_credits_minor", "2000",
			"gross_debits_minor", "0", "fees_minor", "60", "net_minor", net, "declared_lines", "2"},
		payLine("L1", pay1, "1000", "batch_ref", "B-1", "fee_minor", "30"),
		payLine("L2", pay2, "1000", "batch_ref", "B-1", "fee_minor", lineFee))
}

func TestSettlementArithmetic(t *testing.T) {
	if _, err := ParseStatement(settlementFile("1940", "30"), "text/csv", KindSettlementReport, now); err != nil {
		t.Fatalf("valid settlement: %v", codes(err))
	}
	if _, err := ParseStatement(settlementFile("1950", "30"), "text/csv", KindSettlementReport, now); !hasCode(err, "SETTLEMENT_ARITHMETIC") {
		t.Fatalf("gross − fees ≠ net must fail: %v", codes(err))
	}
	if _, err := ParseStatement(settlementFile("1940", "20"), "text/csv", KindSettlementReport, now); !hasCode(err, "BATCH_TOTALS_DIFFER_FROM_LINES") {
		t.Fatalf("header fees ≠ Σ line fees must fail: %v", codes(err))
	}
}

// ---- matcher -----------------------------------------------------------------------------------------------------------

var at = time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)

func rules() RuleSet {
	return RuleSet{ID: "rs", Version: 3, DateWindowDays: 2, SettlementHorizonDays: 3, StaleAfterHours: 48}
}

func base(items ...Item) MatchInput {
	return MatchInput{SourceID: "src", SourceKind: KindTransactionReport, Currency: "USD", Rules: rules(), Now: now, Items: items,
		Payments: []Subject{
			{Type: SubjPayment, ID: pay1, Status: "SUCCEEDED", Amount: 1000, Currency: "USD", ProviderRefs: []string{"sbx_1"}, At: at, Test: true},
			{Type: SubjPayment, ID: pay2, Status: "UNKNOWN", Amount: 500, Currency: "USD", At: at, Test: true},
		},
		Payouts:  []Subject{{Type: SubjPayout, ID: out1, Status: "COMPLETED", Amount: 700, Currency: "USD", ProviderRefs: []string{"po_1"}, At: at, Test: true}},
		Journals: map[string]Journal{JournalKey(SubjPayout, out1): {TransactionID: "j1", Currency: "USD", Amount: 700}},
		Fees:     map[string]FeeExpectation{},
	}
}

func item(id, kind, status string, amount int64, merchant, provider string) Item {
	dir := "CREDIT"
	if kind != "PAYMENT" {
		dir = "DEBIT"
	}
	return Item{ID: id, SourceID: "src", Kind: kind, Direction: dir, Status: status, Amount: amount, Currency: "USD", RawCurrency: "USD",
		MerchantRef: merchant, ProviderRef: provider, BookedAt: at.Add(time.Hour)}
}

func only(t *testing.T, r MatchResult) Decision {
	t.Helper()
	if len(r.Decisions) != 1 {
		t.Fatalf("decisions %d", len(r.Decisions))
	}
	return r.Decisions[0]
}

func excTypes(d Decision) []string {
	var out []string
	for _, e := range d.Exceptions {
		out = append(out, e.Type+"/"+e.Severity)
	}
	return out
}

func TestMatchConfidence(t *testing.T) {
	d := only(t, Match(base(item("i1", "PAYMENT", "SUCCEEDED", 1000, pay1, "sbx_1"))))
	if d.Match == nil || d.Match.Confidence != "EXACT_MATCH" || d.Match.LedgerStatus != "NOT_POSTED_TEST_PAYMENT" || d.Status != "MATCHED" {
		t.Fatalf("exact: %+v %v", d.Match, excTypes(d))
	}
	d = only(t, Match(base(item("i1", "PAYMENT", "SUCCEEDED", 1000, pay1, ""))))
	if d.Match == nil || d.Match.Confidence != "RULE_BASED_MATCH" || d.Match.RuleCode != RuleMerchantRef {
		t.Fatalf("rule-based: %+v", d.Match)
	}
	far := item("i1", "PAYMENT", "SUCCEEDED", 1000, pay1, "")
	far.BookedAt = at.Add(5 * 24 * time.Hour)
	d = only(t, Match(base(far)))
	if d.Match != nil || excTypes(d)[0] != "AMBIGUOUS_MATCH/MEDIUM" {
		t.Fatalf("outside window must not auto-match: %v", excTypes(d))
	}
	// references pointing at different records are never resolved automatically
	d = only(t, Match(base(item("i1", "PAYMENT", "SUCCEEDED", 1000, pay2, "sbx_1"))))
	if d.Match != nil || excTypes(d)[0] != "AMBIGUOUS_MATCH/HIGH" {
		t.Fatalf("conflicting references: %v", excTypes(d))
	}
}

func TestMatchNeverByAmount(t *testing.T) {
	// a line with the exact amount of a payment but no reference to it is unknown funds, not a match
	d := only(t, Match(base(item("i1", "PAYMENT", "SUCCEEDED", 1000, "", "unknown_ref"))))
	if d.Match != nil || excTypes(d)[0] != "MISSING_PAYMENT/HIGH" {
		t.Fatalf("amount-only: %+v %v", d.Match, excTypes(d))
	}
	d = only(t, Match(base(item("i1", "PAYOUT", "COMPLETED", 700, "", "nobody"))))
	if d.Match != nil || excTypes(d)[0] != "MISSING_PAYOUT/CRITICAL" {
		t.Fatalf("unknown payout debit: %v", excTypes(d))
	}
}

func TestMatchDifferences(t *testing.T) {
	cases := []struct {
		name string
		it   Item
		want string
	}{
		{"amount", item("i1", "PAYMENT", "SUCCEEDED", 999, pay1, "sbx_1"), "AMOUNT_MISMATCH/HIGH"},
		{"unknown payment", item("i1", "PAYMENT", "SUCCEEDED", 500, pay2, ""), "UNKNOWN_PAYMENT_OUTCOME/HIGH"},
		{"refund not recorded", item("i1", "REFUND", "SUCCEEDED", 100, pay1, "sbx_1"), "STATUS_MISMATCH/CRITICAL"},
		{"provider says failed", item("i1", "PAYMENT", "FAILED", 1000, pay1, "sbx_1"), "STATUS_MISMATCH/CRITICAL"},
	}
	for _, c := range cases {
		d := only(t, Match(base(c.it)))
		if d.Match != nil || len(d.Exceptions) != 1 || excTypes(d)[0] != c.want {
			t.Errorf("%s: %+v %v", c.name, d.Match, excTypes(d))
		}
	}
	cur := item("i1", "PAYMENT", "SUCCEEDED", 1000, pay1, "sbx_1")
	cur.Currency, cur.RawCurrency = "ZWG", "ZiG"
	d := only(t, Match(base(cur)))
	if d.Match != nil || excTypes(d)[0] != "CURRENCY_MISMATCH/CRITICAL" || d.Exceptions[0].RawCurrency != "ZiG" {
		t.Fatalf("currency: %v", excTypes(d))
	}
}

func TestMatchPayoutsAndLedger(t *testing.T) {
	d := only(t, Match(base(item("i1", "PAYOUT", "COMPLETED", 700, out1, "po_1"))))
	if d.Match == nil || d.Match.LedgerStatus != "POSTED" || d.Match.LedgerTxID != "j1" || len(d.Exceptions) != 0 {
		t.Fatalf("payout: %+v %v", d.Match, excTypes(d))
	}
	in := base(item("i1", "PAYOUT", "COMPLETED", 700, out1, "po_1"))
	in.Journals = map[string]Journal{}
	d = only(t, Match(in))
	if d.Match == nil || d.Match.LedgerStatus != "MISSING" || excTypes(d)[0] != "MISSING_LEDGER_ENTRY/HIGH" {
		t.Fatalf("missing journal: %+v %v", d.Match, excTypes(d))
	}
	in = base(item("i1", "PAYOUT", "COMPLETED", 700, out1, "po_1"))
	in.Payouts[0].Status = "UNKNOWN"
	d = only(t, Match(in))
	if d.Match != nil || excTypes(d)[0] != "UNKNOWN_PAYOUT_OUTCOME/CRITICAL" {
		t.Fatalf("unknown payout: %v", excTypes(d))
	}
	in.Payouts[0].Status = "FAILED"
	if d = only(t, Match(in)); excTypes(d)[0] != "PAYOUT_MISMATCH/CRITICAL" {
		t.Fatalf("paid while failed: %v", excTypes(d))
	}
}

func TestMatchDuplicatesAndFees(t *testing.T) {
	r := Match(base(item("i1", "PAYMENT", "SUCCEEDED", 1000, pay1, "sbx_1"), item("i2", "PAYMENT", "SUCCEEDED", 1000, pay1, "sbx_1")))
	if r.Decisions[0].Match == nil || r.Decisions[1].Match != nil || excTypes(r.Decisions[1])[0] != "DUPLICATE_TRANSACTION/HIGH" {
		t.Fatalf("duplicate: %v", excTypes(r.Decisions[1]))
	}
	in := base(item("i1", "PAYMENT", "SUCCEEDED", 1000, pay1, "sbx_1"))
	in.AlreadyMatched = map[string]string{SubjectKey(SubjPayment, pay1, "PAYMENT"): "earlier"}
	if d := only(t, Match(in)); excTypes(d)[0] != "DUPLICATE_TRANSACTION/HIGH" {
		t.Fatalf("duplicate across runs: %v", excTypes(d))
	}
	fee := int64(30)
	it := item("i1", "PAYMENT", "SUCCEEDED", 1000, pay1, "sbx_1")
	it.Fee = &fee
	in = base(it)
	exp := int64(25)
	in.Fees[pay1] = FeeExpectation{Fee: &exp, Basis: "ESTIMATED"}
	d := only(t, Match(in))
	if d.Match == nil || d.Match.FeeStatus != "MISMATCH" || excTypes(d)[0] != "FEE_MISMATCH/MEDIUM" {
		t.Fatalf("fee mismatch: %+v %v", d.Match, excTypes(d))
	}
	exp = 30
	if d = only(t, Match(in)); d.Match.FeeStatus != "MATCHED" || len(d.Exceptions) != 0 {
		t.Fatalf("fee match: %+v", d.Match)
	}
	in.Fees = map[string]FeeExpectation{pay1: {Basis: "UNKNOWN"}}
	if d = only(t, Match(in)); d.Match.FeeStatus != "NO_EXPECTATION" {
		t.Fatalf("unknown fee is never 0: %+v", d.Match)
	}
}

func TestMissingSettlementOnlyWhenCoveredAndDue(t *testing.T) {
	in := base()
	in.Expected = []Subject{in.Payments[0]}
	in.Covered = func(time.Time) bool { return true }
	if r := Match(in); len(r.Subjects) != 1 || r.Subjects[0].Type != "MISSING_SETTLEMENT" {
		t.Fatalf("missing settlement: %+v", r.Subjects)
	}
	in.Covered = func(time.Time) bool { return false }
	if r := Match(in); len(r.Subjects) != 0 {
		t.Fatal("no statement covers the payment's time: not missing")
	}
	in.Covered = func(time.Time) bool { return true }
	in.Now = at.Add(24 * time.Hour)
	if r := Match(in); len(r.Subjects) != 0 {
		t.Fatal("inside the settlement horizon: not yet missing")
	}
}

func TestMatchIsDeterministic(t *testing.T) {
	items := []Item{item("i1", "PAYMENT", "SUCCEEDED", 1000, pay1, "sbx_1"), item("i2", "PAYMENT", "SUCCEEDED", 500, pay2, ""),
		item("i3", "PAYOUT", "COMPLETED", 700, out1, "po_1"), item("i4", "PAYMENT", "SUCCEEDED", 5, "", "x")}
	h := Match(base(items...)).Hash
	for i := 0; i < 20; i++ {
		shuffled := append([]Item(nil), items...)
		rand.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		if Match(base(shuffled...)).Hash != h {
			t.Fatal("the same input in another order produced different decisions")
		}
	}
}

func TestBankMatching(t *testing.T) {
	rem := Item{ID: "rem1", Kind: "FEE_REMITTANCE", Direction: "DEBIT", Status: "BOOKED", Amount: 300, Currency: "USD", BatchRef: "B-7", BookedAt: at}
	bank := func(id, dir, ref string, amount int64) Item {
		return Item{ID: id, Kind: "BANK_TRANSFER", Direction: dir, Status: "BOOKED", Amount: amount, Currency: "USD", RawCurrency: "USD",
			CounterpartyRef: ref, BookedAt: at.Add(time.Hour)}
	}
	in := MatchInput{SourceID: "bank", SourceKind: KindBankStatement, Currency: "USD", Rules: rules(), Now: now, Remittances: []Item{rem},
		KnownPaymentIDs: map[string]bool{pay1: true},
		Items: []Item{bank("b1", "CREDIT", "FEE REMIT B-7", 300), bank("b2", "CREDIT", "DONATION "+pay1, 1000), bank("b3", "DEBIT", "CHARGES", 12),
			bank("b4", "CREDIT", "UNKNOWN", 50)}}
	r := Match(in)
	got := map[string]string{}
	for _, d := range r.Decisions {
		if d.Match != nil {
			got[d.ItemID] = "MATCH:" + d.Match.SubjectID
		} else {
			got[d.ItemID] = excTypes(d)[0]
		}
	}
	want := map[string]string{"b1": "MATCH:rem1", "b2": "POSSIBLE_DONOR_FUNDS_IN_OPERATING_ACCOUNT/CRITICAL", "b3": "UNMATCHED_BANK_ENTRY/CRITICAL",
		"b4": "UNMATCHED_BANK_ENTRY/HIGH"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %s want %s", k, got[k], v)
		}
	}
	in.Items = nil
	in.Covered = func(time.Time) bool { return true }
	if r := Match(in); len(r.Subjects) != 1 || r.Subjects[0].Type != "MISSING_BANK_CREDIT" {
		t.Fatalf("missing bank credit: %+v", r.Subjects)
	}
}

func TestBatchExpectation(t *testing.T) {
	fee30, fee25 := int64(30), int64(25)
	lines := []BatchLine{{Direction: "CREDIT", Amount: 1000, Fee: &fee30, Settled: true}, {Direction: "CREDIT", Amount: 1000, Fee: &fee30,
		ExpectedFee: &fee25, Settled: true}, {Direction: "DEBIT", Amount: 100, Settled: true}}
	net, complete := BatchExpectation(lines)
	if net != 1000-30+1000-25-100 || !complete {
		t.Fatalf("net %d complete %v", net, complete)
	}
	d := CompareBatch("s", BatchDecision{BatchID: "b", ExpectedNet: net, Status: "MATCHED"}, 1840)
	if d.Status != "DISCREPANCY" || d.Exception == nil || d.Exception.Type != "SETTLEMENT_SHORT" {
		t.Fatalf("short: %+v", d)
	}
	if d := CompareBatch("s", BatchDecision{ExpectedNet: 10, Status: "MATCHED"}, 10); d.Exception != nil || d.Status != "MATCHED" {
		t.Fatalf("equal: %+v", d)
	}
	if d := CompareBatch("s", BatchDecision{ExpectedNet: 10}, 11); d.Exception.Type != "SETTLEMENT_OVER" {
		t.Fatalf("over: %+v", d)
	}
}

func TestRuleSetBoundsAndHash(t *testing.T) {
	ok := RuleSetConfig{DateWindowDays: 2, SettlementHorizonDays: 3, StaleAfterHours: 48}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []RuleSetConfig{{DateWindowDays: 15, SettlementHorizonDays: 3, StaleAfterHours: 48},
		{DateWindowDays: 1, SettlementHorizonDays: 0, StaleAfterHours: 48}, {DateWindowDays: 1, SettlementHorizonDays: 3, StaleAfterHours: 0}} {
		if bad.Validate() == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if ok.Hash() != ok.Hash() {
		t.Fatal("hash unstable")
	}
	raw, _ := json.Marshal(ok)
	if strings.Contains(string(raw), "escalate") {
		t.Fatal("unset optional key must be omitted (closed key set in SQL)")
	}
}

func TestCSVSafe(t *testing.T) {
	for in, want := range map[string]string{"=SUM(A1)": "'=SUM(A1)", "+1": "'+1", "-5": "'-5", "@x": "'@x", "\tx": "'\tx", "1250": "1250",
		"": "", "PAYMENT": "PAYMENT"} {
		if got := CSVSafe(in); got != want {
			t.Errorf("CSVSafe(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProposalHashBindsEveryField(t *testing.T) {
	h := ProposalHash("e", ResExplained, "because the provider confirmed it", []string{"import:x"}, map[string]any{"a": "1"})
	for _, other := range [][32]byte{ProposalHash("e", ResExplained, "because the provider confirmed it!", []string{"import:x"}, map[string]any{"a": "1"}),
		ProposalHash("e", ResEscalated, "because the provider confirmed it", []string{"import:x"}, map[string]any{"a": "1"}),
		ProposalHash("e", ResExplained, "because the provider confirmed it", []string{"import:y"}, map[string]any{"a": "1"}),
		ProposalHash("e", ResExplained, "because the provider confirmed it", []string{"import:x"}, map[string]any{"a": "2"})} {
		if other == h {
			t.Fatal("a changed proposal kept its hash")
		}
	}
}

// The Go edge list and the migration's recon_exception transitions must be identical.
func TestExceptionEdgesMatchSQL(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/20261022090000_reconciliation.sql")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`\('recon_exception', '([A-Z_]*)', '([A-Z_]+)'\)`)
	var sql []string
	for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
		sql = append(sql, m[1]+">"+m[2])
	}
	var goEdges []string
	for _, e := range ExceptionEdges {
		goEdges = append(goEdges, e[0]+">"+e[1])
	}
	sort.Strings(sql)
	sort.Strings(goEdges)
	if strings.Join(sql, ",") != strings.Join(goEdges, ",") {
		t.Fatalf("edges differ:\nsql %v\ngo  %v", sql, goEdges)
	}
	// closable types in Go equal the close guard's list in SQL
	guard := regexp.MustCompile(`(?s)OLD.exception_type NOT IN \((.*?)\)\) THEN`).FindStringSubmatch(string(raw))
	if guard == nil {
		t.Fatal("close guard not found")
	}
	var sqlClosable []string
	for _, m := range regexp.MustCompile(`'([A-Z_]+)'`).FindAllStringSubmatch(guard[1], -1) {
		sqlClosable = append(sqlClosable, m[1])
	}
	sort.Strings(sqlClosable)
	if strings.Join(sqlClosable, ",") != strings.Join(closableList(), ",") {
		t.Fatalf("closable types differ: sql %v go %v", sqlClosable, closableList())
	}
}
