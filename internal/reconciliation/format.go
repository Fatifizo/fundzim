package reconciliation

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// FUNDZIM_CANONICAL_V1 (docs/stage-17/settlement-management.md §3). FundZim's own provider-neutral statement contract:
// one statement record, optional settlement-batch records, statement lines. Integer minor units only, ISO 4217 codes,
// RFC 3339 timestamps. It is NOT any provider's format — a real provider's file needs a parser for that provider's
// documented format (ADR-049 §2). Parsing is all-or-nothing: one invalid record fails the whole file and nothing from it
// is recorded (evidence that is only partly understood is not evidence).

// FormatCanonicalV1 is the only statement format.
const FormatCanonicalV1 = "FUNDZIM_CANONICAL_V1"

// Bounds.
const (
	MaxLines          = 100_000
	MaxBatches        = 1_000
	MaxPeriod         = 93 * 24 * time.Hour
	maxErrors         = 200
	maxCSVFieldLength = 256
	maxAmountMinor    = 1_000_000_000_000_000 // 10^15 minor units: far above any statement, far below int64 overflow when summed
)

// CSVHeader is the canonical CSV header (exact names, this order).
var CSVHeader = []string{"record_type", "ref", "kind", "direction", "status", "amount_minor", "fee_minor", "net_minor", "currency",
	"provider_ref", "merchant_ref", "batch_ref", "counterparty_ref", "value_date", "booked_at", "period_start", "period_end",
	"opening_balance_minor", "closing_balance_minor", "declared_lines", "gross_credits_minor", "gross_debits_minor", "fees_minor",
	"account_ref"}

// Statement is a parsed, validated statement.
type Statement struct {
	Ref           string
	Currency      string // optional (single-currency statement)
	PeriodStart   time.Time
	PeriodEnd     time.Time
	Opening       *int64
	Closing       *int64
	DeclaredLines int
	AccountRef    string // bank statements only; never stored (keyed hash + last four only)
	Batches       []Batch
	Lines         []Line
}

// Batch is a settlement batch header.
type Batch struct {
	Ref            string
	SettlementDate time.Time
	Currency       string
	Credits        int64
	Debits         int64
	Fees           int64
	Net            int64
	DeclaredLines  *int
	Locator        string
}

// Line is one statement line.
type Line struct {
	Number          int
	ID              string
	Kind            string
	Direction       string
	Status          string
	Amount          int64
	Fee             *int64
	Net             *int64
	Currency        string
	RawCurrency     string
	ProviderRef     string
	MerchantRef     string
	BatchRef        string
	CounterpartyRef string
	ValueDate       *time.Time
	BookedAt        time.Time
	RawHash         [32]byte
	Locator         string
}

// ValidationError is one problem found in a file (no file content is echoed beyond the field name).
type ValidationError struct {
	Locator string `json:"locator"`
	Field   string `json:"field,omitempty"`
	Code    string `json:"code"`
}

// FormatError is a failed parse: a failure code and the first validation errors.
type FormatError struct {
	Code   string
	Errors []ValidationError
}

func (e *FormatError) Error() string { return "reconciliation: statement rejected: " + e.Code }

var (
	refRE          = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,99}$`)
	providerRefRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,127}$`)
	counterpartyRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _.:/-]{0,139}$`)
	accountRefRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 -]{3,39}$`)
	amountRE       = regexp.MustCompile(`^(0|[1-9][0-9]{0,17})$`)
	signedRE       = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,17})$`)
	currencyRE     = regexp.MustCompile(`^[A-Za-z]{3}$`)
)

// currencyAliases normalises provider spellings to ISO 4217 (the raw code is always kept: CURRENCY_MISMATCH evidence).
var currencyAliases = map[string]string{"ZIG": "ZWG"}

// SupportedCurrencies are the currencies FundZim records (app.currencies); a line in any other currency fails the file.
var SupportedCurrencies = map[string]bool{"USD": true, "ZWG": true}

// lineRules: kind → allowed directions and statuses (docs/stage-17/settlement-management.md §3.4).
var lineRules = map[string]struct {
	dirs, statuses map[string]bool
	bank           bool
}{
	"PAYMENT":         {set("CREDIT"), set("SUCCEEDED", "FAILED", "PENDING"), false},
	"REFUND":          {set("DEBIT"), set("SUCCEEDED", "FAILED", "PENDING"), false},
	"CHARGEBACK":      {set("DEBIT"), set("SUCCEEDED", "PENDING"), false},
	"REVERSAL":        {set("DEBIT"), set("SUCCEEDED", "PENDING"), false},
	"PAYOUT":          {set("DEBIT"), set("COMPLETED", "FAILED", "PENDING", "RETURNED"), false},
	"PAYOUT_REVERSAL": {set("CREDIT"), set("COMPLETED"), false},
	"FEE":             {set("DEBIT"), set("BOOKED"), false},
	"FEE_REMITTANCE":  {set("DEBIT"), set("BOOKED"), false},
	"ADJUSTMENT":      {set("CREDIT", "DEBIT"), set("BOOKED"), false},
	"BANK_TRANSFER":   {set("CREDIT", "DEBIT"), set("BOOKED"), true},
}

func set(v ...string) map[string]bool {
	m := make(map[string]bool, len(v))
	for _, x := range v {
		m[x] = true
	}
	return m
}

// rawRecord is one record before validation: field name → value ("" = absent), plus its hash and locator.
type rawRecord struct {
	fields  map[string]string
	hash    [32]byte
	locator string
}

// ParseStatement parses and validates a canonical CSV or JSON statement for a source kind. now bounds the period (no
// future statements). It returns a *FormatError for any problem.
func ParseStatement(content []byte, mediaType, sourceKind string, now time.Time) (Statement, error) {
	if len(content) == 0 {
		return Statement{}, &FormatError{Code: "EMPTY_FILE"}
	}
	content = bytes.TrimPrefix(content, []byte{0xEF, 0xBB, 0xBF})
	if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return Statement{}, &FormatError{Code: "INVALID_ENCODING"}
	}
	var recs []rawRecord
	var err error
	switch mediaType {
	case "text/csv":
		recs, err = readCSV(content)
	case "application/json":
		recs, err = readJSON(content)
	default:
		return Statement{}, &FormatError{Code: "UNSUPPORTED_MEDIA_TYPE"}
	}
	if err != nil {
		return Statement{}, err
	}
	return validate(recs, sourceKind, now)
}

func readCSV(content []byte) ([]rawRecord, error) {
	r := csv.NewReader(bytes.NewReader(content))
	r.FieldsPerRecord = len(CSVHeader)
	r.ReuseRecord = false
	head, err := r.Read()
	if err != nil {
		return nil, &FormatError{Code: "MALFORMED_CSV", Errors: []ValidationError{{Locator: "row:1", Code: "HEADER_UNREADABLE"}}}
	}
	for i, h := range head {
		if strings.TrimSpace(h) != CSVHeader[i] {
			return nil, &FormatError{Code: "INVALID_HEADER", Errors: []ValidationError{{Locator: "row:1", Field: CSVHeader[i], Code: "UNEXPECTED_COLUMN"}}}
		}
	}
	var out []rawRecord
	row := 1
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		row++
		if err != nil {
			return nil, &FormatError{Code: "MALFORMED_CSV", Errors: []ValidationError{{Locator: fmt.Sprintf("row:%d", row), Code: "UNREADABLE_ROW"}}}
		}
		if len(out) > MaxLines+MaxBatches+1 {
			return nil, &FormatError{Code: "TOO_MANY_RECORDS"}
		}
		f := make(map[string]string, len(rec))
		for i, v := range rec {
			if len(v) > maxCSVFieldLength {
				return nil, &FormatError{Code: "FIELD_TOO_LONG", Errors: []ValidationError{{Locator: fmt.Sprintf("row:%d", row), Field: CSVHeader[i], Code: "TOO_LONG"}}}
			}
			f[CSVHeader[i]] = strings.TrimSpace(v)
		}
		out = append(out, rawRecord{fields: f, hash: sha256.Sum256([]byte(strings.Join(rec, "\x1f"))), locator: fmt.Sprintf("row:%d", row)})
	}
	return out, nil
}

// jsonStatement is the canonical JSON envelope; numbers must be JSON strings of digits (never floats).
type jsonStatement struct {
	Format    string                     `json:"format"`
	Statement map[string]json.RawMessage `json:"statement"`
	Batches   []json.RawMessage          `json:"batches"`
	Lines     []json.RawMessage          `json:"lines"`
}

func readJSON(content []byte) ([]rawRecord, error) {
	dec := json.NewDecoder(bytes.NewReader(content))
	dec.DisallowUnknownFields()
	var doc jsonStatement
	if err := dec.Decode(&doc); err != nil {
		return nil, &FormatError{Code: "MALFORMED_JSON"}
	}
	if dec.More() {
		return nil, &FormatError{Code: "MALFORMED_JSON"}
	}
	if doc.Format != FormatCanonicalV1 {
		return nil, &FormatError{Code: "UNSUPPORTED_FORMAT", Errors: []ValidationError{{Locator: "json:$.format", Code: "NOT_CANONICAL_V1"}}}
	}
	if len(doc.Lines) > MaxLines || len(doc.Batches) > MaxBatches {
		return nil, &FormatError{Code: "TOO_MANY_RECORDS"}
	}
	toFields := func(raw map[string]json.RawMessage, typ, loc string) (rawRecord, error) {
		f := map[string]string{"record_type": typ}
		keys := make([]string, 0, len(raw))
		for k := range raw {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		allowed := set(CSVHeader...)
		for _, k := range keys {
			if !allowed[k] || k == "record_type" {
				return rawRecord{}, &FormatError{Code: "UNKNOWN_FIELD", Errors: []ValidationError{{Locator: loc, Field: k, Code: "UNKNOWN_FIELD"}}}
			}
			var v string
			if string(raw[k]) == "null" {
				continue
			}
			if err := json.Unmarshal(raw[k], &v); err != nil {
				// a JSON number (or anything but a string) is refused: amounts are digit strings, never floats
				return rawRecord{}, &FormatError{Code: "INVALID_FIELD_TYPE", Errors: []ValidationError{{Locator: loc, Field: k, Code: "NOT_A_STRING"}}}
			}
			if len(v) > maxCSVFieldLength {
				return rawRecord{}, &FormatError{Code: "FIELD_TOO_LONG", Errors: []ValidationError{{Locator: loc, Field: k, Code: "TOO_LONG"}}}
			}
			f[k] = strings.TrimSpace(v)
		}
		canon, _ := json.Marshal(f)
		return rawRecord{fields: f, hash: sha256.Sum256(canon), locator: loc}, nil
	}
	var out []rawRecord
	if doc.Statement == nil {
		return nil, &FormatError{Code: "MISSING_STATEMENT"}
	}
	st, err := toFields(doc.Statement, "STATEMENT", "json:$.statement")
	if err != nil {
		return nil, err
	}
	out = append(out, st)
	for i, b := range doc.Batches {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, &FormatError{Code: "MALFORMED_JSON"}
		}
		rec, err := toFields(m, "BATCH", fmt.Sprintf("json:$.batches[%d]", i))
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	for i, l := range doc.Lines {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(l, &m); err != nil {
			return nil, &FormatError{Code: "MALFORMED_JSON"}
		}
		rec, err := toFields(m, "LINE", fmt.Sprintf("json:$.lines[%d]", i))
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

// fieldsAllowed lists, per record type, the fields that may be present (anything else must be empty).
var fieldsAllowed = map[string]map[string]bool{
	"STATEMENT": set("record_type", "ref", "currency", "period_start", "period_end", "opening_balance_minor", "closing_balance_minor",
		"declared_lines", "account_ref"),
	"BATCH": set("record_type", "ref", "currency", "value_date", "gross_credits_minor", "gross_debits_minor", "fees_minor", "net_minor",
		"declared_lines"),
	"LINE": set("record_type", "ref", "kind", "direction", "status", "amount_minor", "fee_minor", "net_minor", "currency", "provider_ref",
		"merchant_ref", "batch_ref", "counterparty_ref", "value_date", "booked_at"),
}

type collector struct{ errs []ValidationError }

func (c *collector) add(loc, field, code string) {
	if len(c.errs) < maxErrors {
		c.errs = append(c.errs, ValidationError{Locator: loc, Field: field, Code: code})
	}
}

func (c *collector) amount(rec rawRecord, field string, required, positive bool) *int64 {
	v := rec.fields[field]
	if v == "" {
		if required {
			c.add(rec.locator, field, "REQUIRED")
		}
		return nil
	}
	if !amountRE.MatchString(v) {
		c.add(rec.locator, field, "NOT_MINOR_UNITS") // digits only: no sign, decimal point, exponent or thousands separator
		return nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n > maxAmountMinor || (positive && n == 0) {
		c.add(rec.locator, field, "OUT_OF_RANGE")
		return nil
	}
	return &n
}

func (c *collector) signed(rec rawRecord, field string) *int64 {
	v := rec.fields[field]
	if v == "" {
		return nil
	}
	if !signedRE.MatchString(v) {
		c.add(rec.locator, field, "NOT_MINOR_UNITS")
		return nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n > maxAmountMinor || n < -maxAmountMinor {
		c.add(rec.locator, field, "OUT_OF_RANGE")
		return nil
	}
	return &n
}

func (c *collector) count(rec rawRecord, field string) *int {
	v := rec.fields[field]
	if v == "" {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > MaxLines || !amountRE.MatchString(v) {
		c.add(rec.locator, field, "INVALID_COUNT")
		return nil
	}
	return &n
}

func (c *collector) currency(rec rawRecord, required bool) (norm, raw string) {
	raw = rec.fields["currency"]
	if raw == "" {
		if required {
			c.add(rec.locator, "currency", "REQUIRED")
		}
		return "", ""
	}
	if !currencyRE.MatchString(raw) {
		c.add(rec.locator, "currency", "INVALID_CURRENCY")
		return "", raw
	}
	norm = strings.ToUpper(raw)
	if a, ok := currencyAliases[norm]; ok {
		norm = a
	}
	if !SupportedCurrencies[norm] {
		c.add(rec.locator, "currency", "UNSUPPORTED_CURRENCY")
		return "", raw
	}
	return norm, raw
}

func (c *collector) timestamp(rec rawRecord, field string, required bool) *time.Time {
	v := rec.fields[field]
	if v == "" {
		if required {
			c.add(rec.locator, field, "REQUIRED")
		}
		return nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		c.add(rec.locator, field, "INVALID_TIMESTAMP") // RFC 3339 with an explicit offset
		return nil
	}
	t = t.UTC()
	return &t
}

func (c *collector) date(rec rawRecord, field string, required bool) *time.Time {
	v := rec.fields[field]
	if v == "" {
		if required {
			c.add(rec.locator, field, "REQUIRED")
		}
		return nil
	}
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		c.add(rec.locator, field, "INVALID_DATE")
		return nil
	}
	return &t
}

func (c *collector) ref(rec rawRecord, field string, re *regexp.Regexp, required bool) string {
	v := rec.fields[field]
	if v == "" {
		if required {
			c.add(rec.locator, field, "REQUIRED")
		}
		return ""
	}
	if !re.MatchString(v) {
		c.add(rec.locator, field, "INVALID_REFERENCE")
		return ""
	}
	return v
}

func validate(recs []rawRecord, sourceKind string, now time.Time) (Statement, error) {
	c := &collector{}
	var st Statement
	if len(recs) == 0 || recs[0].fields["record_type"] != "STATEMENT" {
		return st, &FormatError{Code: "MISSING_STATEMENT", Errors: []ValidationError{{Locator: "row:2", Field: "record_type", Code: "STATEMENT_FIRST"}}}
	}
	for _, r := range recs {
		typ := r.fields["record_type"]
		allowed, ok := fieldsAllowed[typ]
		if !ok {
			c.add(r.locator, "record_type", "UNKNOWN_RECORD_TYPE")
			continue
		}
		for k, v := range r.fields {
			if v != "" && !allowed[k] {
				c.add(r.locator, k, "NOT_ALLOWED_FOR_RECORD")
			}
		}
	}

	// statement
	s := recs[0]
	st.Ref = c.ref(s, "ref", refRE, true)
	st.Currency, _ = c.currency(s, false)
	ps, pe := c.timestamp(s, "period_start", true), c.timestamp(s, "period_end", true)
	if ps != nil && pe != nil {
		switch {
		case !pe.After(*ps):
			c.add(s.locator, "period_end", "PERIOD_NOT_POSITIVE")
		case pe.Sub(*ps) > MaxPeriod:
			c.add(s.locator, "period_end", "PERIOD_TOO_LONG")
		case pe.After(now.Add(24 * time.Hour)):
			c.add(s.locator, "period_end", "PERIOD_IN_FUTURE")
		}
		st.PeriodStart, st.PeriodEnd = *ps, *pe
	}
	st.Opening, st.Closing = c.signed(s, "opening_balance_minor"), c.signed(s, "closing_balance_minor")
	if (st.Opening != nil || st.Closing != nil) && st.Currency == "" {
		c.add(s.locator, "currency", "REQUIRED_WITH_BALANCES")
	}
	if (st.Opening == nil) != (st.Closing == nil) {
		c.add(s.locator, "closing_balance_minor", "BALANCES_COME_IN_PAIRS")
	}
	if d := c.count(s, "declared_lines"); d != nil {
		st.DeclaredLines = *d
	} else {
		c.add(s.locator, "declared_lines", "REQUIRED")
	}
	if a := s.fields["account_ref"]; a != "" {
		if sourceKind != KindBankStatement {
			c.add(s.locator, "account_ref", "BANK_STATEMENTS_ONLY")
		} else if !accountRefRE.MatchString(a) {
			c.add(s.locator, "account_ref", "INVALID_REFERENCE")
		} else {
			st.AccountRef = a
		}
	} else if sourceKind == KindBankStatement {
		c.add(s.locator, "account_ref", "REQUIRED")
	}

	batches := map[string]*Batch{}
	lineIDs := map[string]int{}
	var lineCount int
	for _, r := range recs[1:] {
		switch r.fields["record_type"] {
		case "STATEMENT":
			c.add(r.locator, "record_type", "SECOND_STATEMENT")
		case "BATCH":
			if sourceKind != KindSettlementReport {
				c.add(r.locator, "record_type", "BATCHES_IN_SETTLEMENT_REPORTS_ONLY")
				continue
			}
			b := Batch{Ref: c.ref(r, "ref", refRE, true), Locator: r.locator}
			b.Currency, _ = c.currency(r, true)
			if d := c.date(r, "value_date", true); d != nil {
				b.SettlementDate = *d
			}
			cr, dr, fe := c.amount(r, "gross_credits_minor", true, false), c.amount(r, "gross_debits_minor", true, false), c.amount(r, "fees_minor", true, false)
			net := c.signed(r, "net_minor")
			if net == nil {
				c.add(r.locator, "net_minor", "REQUIRED")
			}
			b.DeclaredLines = c.count(r, "declared_lines")
			if cr != nil && dr != nil && fe != nil && net != nil {
				b.Credits, b.Debits, b.Fees, b.Net = *cr, *dr, *fe, *net
				if b.Net != b.Credits-b.Debits-b.Fees {
					c.add(r.locator, "net_minor", "SETTLEMENT_ARITHMETIC") // gross credits − debits − fees must equal net
				}
			}
			if st.Currency != "" && b.Currency != "" && b.Currency != st.Currency {
				c.add(r.locator, "currency", "DIFFERS_FROM_STATEMENT")
			}
			if b.Ref != "" {
				if _, dup := batches[b.Ref]; dup {
					c.add(r.locator, "ref", "DUPLICATE_BATCH")
				}
				bb := b
				batches[b.Ref] = &bb
				st.Batches = append(st.Batches, b)
			}
		case "LINE":
			lineCount++
			l := Line{Number: lineCount, Locator: r.locator, RawHash: r.hash}
			l.ID = c.ref(r, "ref", refRE, true)
			l.Kind, l.Direction, l.Status = r.fields["kind"], r.fields["direction"], r.fields["status"]
			rule, ok := lineRules[l.Kind]
			switch {
			case !ok:
				c.add(r.locator, "kind", "UNKNOWN_KIND")
			case rule.bank != (sourceKind == KindBankStatement):
				c.add(r.locator, "kind", "NOT_ALLOWED_FOR_SOURCE")
			default:
				if !rule.dirs[l.Direction] {
					c.add(r.locator, "direction", "NOT_ALLOWED_FOR_KIND")
				}
				if !rule.statuses[l.Status] {
					c.add(r.locator, "status", "NOT_ALLOWED_FOR_KIND")
				}
			}
			if a := c.amount(r, "amount_minor", true, true); a != nil {
				l.Amount = *a
			}
			l.Fee = c.amount(r, "fee_minor", false, false)
			l.Net = c.amount(r, "net_minor", false, false)
			if l.Fee != nil && l.Net != nil && l.Amount > 0 && *l.Net != l.Amount-*l.Fee {
				c.add(r.locator, "net_minor", "LINE_ARITHMETIC")
			}
			if l.Fee != nil && l.Amount > 0 && *l.Fee > l.Amount {
				c.add(r.locator, "fee_minor", "FEE_ABOVE_AMOUNT")
			}
			l.Currency, l.RawCurrency = c.currency(r, true)
			l.ProviderRef = c.ref(r, "provider_ref", providerRefRE, false)
			l.MerchantRef = c.ref(r, "merchant_ref", providerRefRE, false)
			l.BatchRef = c.ref(r, "batch_ref", refRE, false)
			l.CounterpartyRef = c.ref(r, "counterparty_ref", counterpartyRE, false)
			if sourceKind == KindBankStatement && l.CounterpartyRef == "" {
				c.add(r.locator, "counterparty_ref", "REQUIRED")
			}
			if sourceKind != KindBankStatement && l.ProviderRef == "" && l.MerchantRef == "" && l.Kind != "FEE" && l.Kind != "ADJUSTMENT" {
				c.add(r.locator, "provider_ref", "A_REFERENCE_IS_REQUIRED") // never matched on amount alone
			}
			l.ValueDate = c.date(r, "value_date", false)
			if b := c.timestamp(r, "booked_at", true); b != nil {
				l.BookedAt = *b
				if ps != nil && pe != nil && (b.Before(*ps) || !b.Before(*pe)) {
					c.add(r.locator, "booked_at", "OUTSIDE_PERIOD")
				}
			}
			if l.BatchRef != "" {
				b, ok := batches[l.BatchRef]
				switch {
				case sourceKind != KindSettlementReport:
					c.add(r.locator, "batch_ref", "BATCHES_IN_SETTLEMENT_REPORTS_ONLY")
				case !ok:
					c.add(r.locator, "batch_ref", "UNKNOWN_BATCH") // batches precede their lines
				case b.Currency != "" && l.Currency != "" && b.Currency != l.Currency:
					c.add(r.locator, "currency", "DIFFERS_FROM_BATCH")
				}
			}
			if l.ID != "" {
				if prev, dup := lineIDs[l.ID]; dup {
					if st.Lines[prev].RawHash == l.RawHash {
						c.add(r.locator, "ref", "DUPLICATE_ROW")
					} else {
						c.add(r.locator, "ref", "CONFLICTING_LINE_IN_FILE")
					}
					continue
				}
				lineIDs[l.ID] = len(st.Lines)
			}
			st.Lines = append(st.Lines, l)
		}
	}
	if lineCount != st.DeclaredLines {
		c.add(s.locator, "declared_lines", "DECLARED_COUNT_MISMATCH")
	}
	if lineCount > MaxLines {
		c.add(s.locator, "declared_lines", "TOO_MANY_LINES")
	}
	// batch headers must add up to their lines (fees from line fees; FEE_REMITTANCE lines are not part of a batch's net)
	for _, b := range st.Batches {
		var cr, dr, fe int64
		n := 0
		for _, l := range st.Lines {
			if l.BatchRef != b.Ref || l.Kind == "FEE_REMITTANCE" {
				continue
			}
			n++
			if l.Direction == "CREDIT" {
				cr += l.Amount
			} else {
				dr += l.Amount
			}
			if l.Fee != nil {
				fe += *l.Fee
			}
		}
		if cr != b.Credits || dr != b.Debits || fe != b.Fees {
			c.add(b.Locator, "net_minor", "BATCH_TOTALS_DIFFER_FROM_LINES")
		}
		if b.DeclaredLines != nil && *b.DeclaredLines != n {
			c.add(b.Locator, "declared_lines", "DECLARED_COUNT_MISMATCH")
		}
	}
	if len(c.errs) > 0 {
		return Statement{}, &FormatError{Code: firstCode(c.errs), Errors: c.errs}
	}
	return st, nil
}

// firstCode picks the failure code reported for the file: structural problems first, then the first line problem.
func firstCode(errs []ValidationError) string {
	for _, e := range errs {
		switch e.Code {
		case "SETTLEMENT_ARITHMETIC", "BATCH_TOTALS_DIFFER_FROM_LINES", "DECLARED_COUNT_MISMATCH", "CONFLICTING_LINE_IN_FILE", "DUPLICATE_ROW":
			return e.Code
		}
	}
	return "INVALID_RECORDS"
}
