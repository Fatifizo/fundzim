package reconciliation

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Fatifizo/fundzim/internal/payments"
	"github.com/Fatifizo/fundzim/internal/payouts"
)

// The matcher (docs/stage-17/matching-engine.md). Match is a pure function of its input: the same statement lines,
// FundZim records, journals, fee expectations and rule set always produce the same decisions, in the same order, with
// the same explanation. It never looks for a candidate by amount: a line is linked to a payment or payout only through a
// reference (our id, sent to the provider as merchant reference, or the provider's own reference); amount, currency,
// status, fee and journal are then verified. Ambiguity is never resolved automatically.

// RuleSet is the active matching rule set (maker-checker approved; none seeded).
type RuleSet struct {
	ID                    string
	Version               int
	DateWindowDays        int  // RULE_BASED_MATCH: |booked_at − record time| must be inside this window
	SettlementHorizonDays int  // a record older than this inside a covered period must appear on the statement
	StaleAfterHours       int  // no new statement for a used source within this → STALE_RECONCILIATION
	EscalateAfterHours    *int // optional: open exceptions older than this are escalated (nil: never)
}

// Item is one statement line as the matcher sees it.
type Item struct {
	ID, SourceID, Kind, Direction, Status string
	Amount                                int64
	Fee                                   *int64
	Currency, RawCurrency                 string
	ProviderRef, MerchantRef              string
	BatchID, BatchRef, CounterpartyRef    string
	BookedAt                              time.Time
}

// Subject is a payment or payout as the matcher sees it.
type Subject struct {
	Type         string // PAYMENT or PAYOUT
	ID           string
	Status       string
	Amount       int64
	Currency     string
	ProviderRefs []string
	At           time.Time // succeeded (payment) / completed or submitted (payout) / created
	Test         bool
}

// Journal is the ledger journal a matched subject is expected to have.
type Journal struct {
	TransactionID string
	Currency      string
	Amount        int64
	Reversed      bool
}

// FeeExpectation is the provider fee FundZim assessed for a subject (nil Fee: UNKNOWN).
type FeeExpectation struct {
	Fee   *int64
	Basis string
}

// MatchInput is everything one run decides from.
type MatchInput struct {
	SourceID   string
	SourceKind string
	Currency   string
	Rules      RuleSet
	Now        time.Time
	Items      []Item
	Payments   []Subject
	Payouts    []Subject
	Journals   map[string]Journal        // by ledger key (JournalKey)
	Fees       map[string]FeeExpectation // by subject id
	// AlreadyMatched: subject keys (SubjectKey) with an active match from another line of this source (duplicates).
	AlreadyMatched map[string]string
	// Expected: records a statement covering their time must contain (MISSING_SETTLEMENT), with Covered telling whether
	// a parsed import of this source covers a time.
	Expected []Subject
	Covered  func(time.Time) bool
	// Bank runs: fee-remittance lines of the provider settlement reports (candidates), payment ids found printed on
	// transfers (Model A alarm) and remittances that must reach the bank (MISSING_BANK_CREDIT).
	Remittances     []Item
	KnownPaymentIDs map[string]bool
}

// MatchDraft is a decided match.
type MatchDraft struct {
	SubjectType, SubjectID, Confidence, RuleCode string
	Amount                                       int64
	LedgerStatus, LedgerTxID, FeeStatus          string
	Explanation                                  map[string]any
}

// ExceptionDraft is a detected difference.
type ExceptionDraft struct {
	Type, Severity, DedupeKey        string
	ItemID, SubjectType, SubjectID   string
	BatchID, LedgerTxID, RawCurrency string
	Expected, Actual                 *int64
	Detail                           map[string]any
}

// Decision is what the matcher decided for one line.
type Decision struct {
	ItemID     string
	Match      *MatchDraft
	Exceptions []ExceptionDraft
	// Status is the line's new match status: MATCHED, EXCEPTION or UNMATCHED (informational lines: nothing to decide).
	Status string
}

// BatchDecision is a settlement batch's state after the run.
type BatchDecision struct {
	BatchID     string
	ExpectedNet int64
	Status      string // MATCHED or DISCREPANCY
	Exception   *ExceptionDraft
}

// MatchResult is a run's complete outcome.
type MatchResult struct {
	Decisions []Decision
	Subjects  []ExceptionDraft // record-level exceptions (MISSING_SETTLEMENT)
	// Hash commits to every decision (stored on the run; a replay of the same input yields the same hash).
	Hash [32]byte
}

// Rule codes recorded on matches.
const (
	RuleRefBoth        = "MERCHANT_AND_PROVIDER_REF"
	RuleMerchantRef    = "MERCHANT_REF_IN_WINDOW"
	RuleProviderRef    = "PROVIDER_REF_IN_WINDOW"
	RuleConsistentFail = "CONSISTENT_FAILURE"
	RuleRemittance     = "REMITTANCE_REFERENCE"
)

// Subject types.
const (
	SubjPayment    = "PAYMENT"
	SubjPayout     = "PAYOUT"
	SubjRemittance = "FEE_REMITTANCE_LINE"
)

// Severities are engineering defaults by type (docs/stage-17/exceptions.md §3), not regulatory classifications.
const (
	SevLow      = "LOW"
	SevMedium   = "MEDIUM"
	SevHigh     = "HIGH"
	SevCritical = "CRITICAL"
)

// SubjectKey identifies a subject for duplicate detection: one active match per subject per kind of line per source.
func SubjectKey(subjectType, subjectID, lineKind string) string {
	return subjectType + ":" + subjectID + ":" + lineKind
}

// JournalKey is the ledger idempotency key a matched record's journal must carry (LEDGER.md §5; payouts requests.go).
func JournalKey(subjectType, subjectID string) string {
	if subjectType == SubjPayout {
		return "payout:" + subjectID + ":completed"
	}
	return "payment:" + subjectID + ":capture"
}

var (
	paymentSuccess = set("SUCCEEDED", "PARTIALLY_REFUNDED", "REFUNDED", "DISPUTED", "CHARGED_BACK")
	paymentFailed  = set("FAILED", "EXPIRED", "CANCELLED")
	paymentRefund  = set("REFUNDED", "PARTIALLY_REFUNDED")
	paymentDispute = set("DISPUTED", "CHARGED_BACK")
	payoutDone     = set("COMPLETED", "REVERSED")
	payoutInFlight = set("SUBMITTED", "PROCESSING")
	payoutEnded    = set("FAILED", "CANCELLED", "REJECTED")
	payoutBefore   = set("PAYOUT_REQUESTED", "PENDING_REVIEW", "APPROVED")
	uuidInText     = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
)

func i64(v int64) *int64 { return &v }

func itemKey(typ, itemID string) string { return typ + ":item:" + itemID }

func subjectExcKey(typ, sourceID, subjectType, subjectID string) string {
	return typ + ":source:" + sourceID + ":" + subjectType + ":" + subjectID
}

// Match decides every line of the input. It never mutates the input.
func Match(in MatchInput) MatchResult {
	items := append([]Item(nil), in.Items...)
	sort.Slice(items, func(i, j int) bool {
		if !items[i].BookedAt.Equal(items[j].BookedAt) {
			return items[i].BookedAt.Before(items[j].BookedAt)
		}
		return items[i].ID < items[j].ID
	})
	var res MatchResult
	if in.SourceKind == KindBankStatement {
		res = matchBank(in, items)
	} else {
		res = matchProvider(in, items)
	}
	res.Hash = hashResult(res)
	return res
}

type index struct {
	byID  map[string]*Subject
	byRef map[string][]*Subject
}

func newIndex(list []Subject) index {
	ix := index{byID: map[string]*Subject{}, byRef: map[string][]*Subject{}}
	for i := range list {
		s := &list[i]
		ix.byID[s.ID] = s
		for _, r := range s.ProviderRefs {
			ix.byRef[r] = append(ix.byRef[r], s)
		}
	}
	return ix
}

func matchProvider(in MatchInput, items []Item) MatchResult {
	pays := newIndex(append([]Subject(nil), in.Payments...))
	outs := newIndex(append([]Subject(nil), in.Payouts...))
	matched := map[string]string{}
	for k, v := range in.AlreadyMatched {
		matched[k] = v
	}
	var res MatchResult
	window := time.Duration(in.Rules.DateWindowDays) * 24 * time.Hour
	matchedSubjects := map[string]bool{}
	for _, it := range items {
		d := Decision{ItemID: it.ID, Status: "UNMATCHED"}
		exc := func(typ, sev string, e ExceptionDraft) {
			e.Type, e.Severity, e.ItemID = typ, sev, it.ID
			if e.DedupeKey == "" {
				e.DedupeKey = itemKey(typ, it.ID)
			}
			if e.Detail == nil {
				e.Detail = map[string]any{}
			}
			e.Detail["line_kind"], e.Detail["line_status"] = it.Kind, it.Status
			d.Exceptions = append(d.Exceptions, e)
			d.Status = "EXCEPTION"
		}
		switch it.Kind {
		case "FEE", "ADJUSTMENT":
			exc("PROVIDER_ADJUSTMENT", map[string]string{"FEE": SevLow, "ADJUSTMENT": SevMedium}[it.Kind], ExceptionDraft{Actual: i64(it.Amount),
				Detail: map[string]any{"direction": it.Direction, "reason": "a provider line that names no FundZim record needs an explanation"}})
			res.Decisions = append(res.Decisions, d)
			continue
		case "FEE_REMITTANCE":
			res.Decisions = append(res.Decisions, d) // matched by the operating-bank run
			continue
		}
		ix, subjType := pays, SubjPayment
		if it.Kind == "PAYOUT" || it.Kind == "PAYOUT_REVERSAL" {
			ix, subjType = outs, SubjPayout
		}
		// candidates by reference only (never by amount)
		var byMerchant *Subject
		if it.MerchantRef != "" {
			byMerchant = ix.byID[strings.ToLower(it.MerchantRef)]
		}
		byRef := ix.byRef[it.ProviderRef]
		cands := map[string]*Subject{}
		if byMerchant != nil {
			cands[byMerchant.ID] = byMerchant
		}
		for _, c := range byRef {
			cands[c.ID] = c
		}
		if len(cands) == 0 {
			if it.Status == "PENDING" {
				res.Decisions = append(res.Decisions, d)
				continue
			}
			typ, sev := "MISSING_PAYMENT", SevHigh
			if subjType == SubjPayout {
				typ, sev = "MISSING_PAYOUT", SevCritical // money left the provider for a payout FundZim never made
			}
			exc(typ, sev, ExceptionDraft{Actual: i64(it.Amount), Detail: map[string]any{"merchant_ref_given": it.MerchantRef != "",
				"provider_ref_given": it.ProviderRef != ""}})
			res.Decisions = append(res.Decisions, d)
			continue
		}
		if len(cands) > 1 {
			ids := make([]string, 0, len(cands))
			for id := range cands {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			exc("AMBIGUOUS_MATCH", SevHigh, ExceptionDraft{Actual: i64(it.Amount), Detail: map[string]any{"candidates": ids,
				"reason": "the references point at different records"}})
			res.Decisions = append(res.Decisions, d)
			continue
		}
		var s *Subject
		for _, c := range cands {
			s = c
		}
		merchantAgrees := byMerchant != nil && byMerchant.ID == s.ID
		refAgrees := it.ProviderRef != "" && contains(s.ProviderRefs, it.ProviderRef)
		expl := map[string]any{"merchant_ref": refState(it.MerchantRef, merchantAgrees), "provider_ref": refState(it.ProviderRef, refAgrees),
			"record_status": s.Status, "line_status": it.Status, "amount_checked": true, "currency_checked": true,
			"rule_set_version": in.Rules.Version, "matcher_version": MatcherVersion}
		confidence, rule := "EXACT_MATCH", RuleRefBoth
		if !(merchantAgrees && refAgrees) {
			gap := it.BookedAt.Sub(s.At)
			if gap < 0 {
				gap = -gap
			}
			expl["days_between_line_and_record"] = int(gap.Hours() / 24)
			if gap > window {
				exc("AMBIGUOUS_MATCH", SevMedium, ExceptionDraft{SubjectType: subjType, SubjectID: s.ID, Actual: i64(it.Amount),
					Expected: i64(s.Amount), Detail: map[string]any{"reason": "one reference agrees but the line is outside the rule set's date window",
						"date_window_days": in.Rules.DateWindowDays}})
				res.Decisions = append(res.Decisions, d)
				continue
			}
			confidence, rule = "RULE_BASED_MATCH", RuleMerchantRef
			if !merchantAgrees {
				rule = RuleProviderRef
			}
		}
		if it.Currency != s.Currency {
			exc("CURRENCY_MISMATCH", SevCritical, ExceptionDraft{SubjectType: subjType, SubjectID: s.ID, RawCurrency: it.RawCurrency,
				Expected: i64(s.Amount), Actual: i64(it.Amount), Detail: map[string]any{"record_currency": s.Currency, "line_currency": it.Currency,
					"note": "currencies are never converted or compared by amount"}})
			res.Decisions = append(res.Decisions, d)
			continue
		}
		sk := SubjectKey(subjType, s.ID, it.Kind)
		if other, dup := matched[sk]; dup && other != it.ID {
			exc("DUPLICATE_TRANSACTION", SevHigh, ExceptionDraft{SubjectType: subjType, SubjectID: s.ID, Actual: i64(it.Amount),
				Detail: map[string]any{"first_line_item_id": other, "note": "a second statement line for the same record; nothing is deleted"}})
			res.Decisions = append(res.Decisions, d)
			continue
		}
		var outcome lineOutcome
		if subjType == SubjPayment {
			outcome = paymentOutcome(it, s)
		} else {
			outcome = payoutOutcome(it, s)
		}
		if outcome.informational {
			res.Decisions = append(res.Decisions, d)
			continue
		}
		if outcome.excType != "" {
			e := ExceptionDraft{SubjectType: subjType, SubjectID: s.ID, Expected: i64(s.Amount), Actual: i64(it.Amount),
				Detail: map[string]any{"record_status": s.Status, "reason": outcome.reason}}
			if outcome.subjectDedupe {
				e.DedupeKey = subjectExcKey(outcome.excType, in.SourceID, subjType, s.ID)
			}
			exc(outcome.excType, outcome.severity, e)
			res.Decisions = append(res.Decisions, d)
			continue
		}
		// a match: ledger journal and fee
		m := &MatchDraft{SubjectType: subjType, SubjectID: s.ID, Confidence: confidence, RuleCode: rule, Amount: it.Amount,
			LedgerStatus: "NOT_APPLICABLE", FeeStatus: "NOT_APPLICABLE", Explanation: expl}
		if outcome.consistentFailure {
			m.RuleCode = RuleConsistentFail
		}
		if outcome.expectJournal {
			switch {
			case subjType == SubjPayment && s.Test:
				m.LedgerStatus = "NOT_POSTED_TEST_PAYMENT" // test payments are never posted (ADR-042)
			default:
				j, ok := in.Journals[JournalKey(subjType, s.ID)]
				switch {
				case ok && j.Currency == s.Currency && !j.Reversed:
					m.LedgerStatus, m.LedgerTxID = "POSTED", j.TransactionID
				default:
					m.LedgerStatus = "MISSING"
					e := ExceptionDraft{SubjectType: subjType, SubjectID: s.ID, Expected: i64(s.Amount),
						DedupeKey: subjectExcKey("MISSING_LEDGER_ENTRY", in.SourceID, subjType, s.ID),
						Detail:    map[string]any{"journal_key": JournalKey(subjType, s.ID), "found": ok, "reversed": ok && j.Reversed}}
					if ok {
						e.LedgerTxID = j.TransactionID
					}
					exc("MISSING_LEDGER_ENTRY", SevHigh, e)
				}
			}
		}
		if outcome.feeApplies {
			fe, known := in.Fees[s.ID]
			switch {
			case it.Fee == nil:
				m.FeeStatus = "NOT_REPORTED"
			case !known || fe.Fee == nil:
				m.FeeStatus = "NO_EXPECTATION"
				expl["fee_basis"] = map[bool]string{true: fe.Basis, false: "NOT_ASSESSED"}[known]
			case *fe.Fee == *it.Fee:
				m.FeeStatus = "MATCHED"
				expl["fee_basis"], expl["expected_fee_minor"] = fe.Basis, fmt.Sprint(*fe.Fee)
			default:
				m.FeeStatus = "MISMATCH"
				expl["fee_basis"], expl["expected_fee_minor"] = fe.Basis, fmt.Sprint(*fe.Fee)
				exc("FEE_MISMATCH", SevMedium, ExceptionDraft{SubjectType: subjType, SubjectID: s.ID, Expected: i64(*fe.Fee), Actual: i64(*it.Fee),
					Detail: map[string]any{"fee_basis": fe.Basis, "note": "provider fee evidence is SYNTHETIC or UNVERIFIED until a provider is selected"}})
			}
		}
		// a match with a ledger or fee exception is still a match: the line is linked, the exception tracks what is wrong
		d.Match, d.Status = m, "MATCHED"
		matched[sk] = it.ID
		matchedSubjects[subjType+":"+s.ID] = true
		res.Decisions = append(res.Decisions, d)
	}

	// records that a covered statement must contain
	horizon := time.Duration(in.Rules.SettlementHorizonDays) * 24 * time.Hour
	exp := append([]Subject(nil), in.Expected...)
	sort.Slice(exp, func(i, j int) bool { return exp[i].Type+exp[i].ID < exp[j].Type+exp[j].ID })
	for _, s := range exp {
		if matchedSubjects[s.Type+":"+s.ID] || in.AlreadyMatched[SubjectKey(s.Type, s.ID, map[string]string{SubjPayment: "PAYMENT", SubjPayout: "PAYOUT"}[s.Type])] != "" {
			continue
		}
		if in.Now.Sub(s.At) < horizon || in.Covered == nil || !in.Covered(s.At) {
			continue // not yet due, or no statement of this source covers its time
		}
		sev := SevHigh
		if s.Type == SubjPayout {
			sev = SevCritical
		}
		res.Subjects = append(res.Subjects, ExceptionDraft{Type: "MISSING_SETTLEMENT", Severity: sev, SubjectType: s.Type, SubjectID: s.ID,
			DedupeKey: subjectExcKey("MISSING_SETTLEMENT", in.SourceID, s.Type, s.ID), Expected: i64(s.Amount),
			Detail: map[string]any{"record_status": s.Status, "record_time": s.At.UTC().Format(time.RFC3339),
				"settlement_horizon_days": in.Rules.SettlementHorizonDays}})
	}

	return res
}

type lineOutcome struct {
	excType, severity, reason string
	subjectDedupe             bool // the exception is about the record (closes when the record catches up)
	informational             bool // a pending line: nothing to decide yet
	expectJournal, feeApplies bool
	consistentFailure         bool
}

func paymentOutcome(it Item, s *Subject) lineOutcome {
	switch it.Kind {
	case "PAYMENT":
		switch it.Status {
		case "SUCCEEDED":
			switch {
			case paymentSuccess[s.Status]:
				if it.Amount != s.Amount {
					return lineOutcome{excType: "AMOUNT_MISMATCH", severity: SevHigh, reason: "the provider reports a different gross amount"}
				}
				return lineOutcome{expectJournal: true, feeApplies: true}
			case s.Status == "UNKNOWN":
				return lineOutcome{excType: "UNKNOWN_PAYMENT_OUTCOME", severity: SevHigh, subjectDedupe: true,
					reason: "the statement reports success for a payment whose outcome is unknown; nothing is applied automatically"}
			case paymentFailed[s.Status]:
				return lineOutcome{excType: "STATUS_MISMATCH", severity: SevCritical, reason: "the provider reports success for a payment FundZim recorded as failed"}
			default:
				return lineOutcome{excType: "STATUS_MISMATCH", severity: SevLow, subjectDedupe: true, reason: "FundZim has not yet recorded the outcome the provider reports"}
			}
		case "FAILED":
			switch {
			case paymentSuccess[s.Status]:
				return lineOutcome{excType: "STATUS_MISMATCH", severity: SevCritical, reason: "the provider reports failure for a payment FundZim recorded as succeeded"}
			case s.Status == "UNKNOWN":
				return lineOutcome{excType: "UNKNOWN_PAYMENT_OUTCOME", severity: SevHigh, subjectDedupe: true,
					reason: "the statement reports failure for a payment whose outcome is unknown; nothing is applied automatically"}
			case paymentFailed[s.Status]:
				return lineOutcome{consistentFailure: true}
			default:
				return lineOutcome{excType: "STATUS_MISMATCH", severity: SevLow, subjectDedupe: true, reason: "FundZim has not yet recorded the outcome the provider reports"}
			}
		}
		return lineOutcome{informational: true}
	case "REFUND":
		if it.Status != "SUCCEEDED" {
			if it.Status == "FAILED" && paymentRefund[s.Status] {
				return lineOutcome{excType: "STATUS_MISMATCH", severity: SevHigh, reason: "the provider reports a failed refund for a payment FundZim recorded as refunded"}
			}
			return lineOutcome{informational: true}
		}
		if !paymentRefund[s.Status] {
			return lineOutcome{excType: "STATUS_MISMATCH", severity: SevCritical,
				reason: "the provider reports a refund FundZim has not recorded (FundZim cannot execute refunds)"}
		}
		if it.Amount > s.Amount {
			return lineOutcome{excType: "AMOUNT_MISMATCH", severity: SevCritical, reason: "the refund exceeds the payment"}
		}
		return lineOutcome{}
	case "CHARGEBACK", "REVERSAL":
		if it.Status != "SUCCEEDED" {
			return lineOutcome{informational: true}
		}
		if !paymentDispute[s.Status] && !paymentRefund[s.Status] {
			return lineOutcome{excType: "STATUS_MISMATCH", severity: SevHigh, reason: "the provider reports a chargeback or reversal FundZim has not recorded"}
		}
		if it.Amount > s.Amount {
			return lineOutcome{excType: "AMOUNT_MISMATCH", severity: SevHigh, reason: "the reversal exceeds the payment"}
		}
		return lineOutcome{}
	}
	return lineOutcome{informational: true}
}

func payoutOutcome(it Item, s *Subject) lineOutcome {
	if it.Kind == "PAYOUT_REVERSAL" {
		if s.Status != "REVERSED" {
			return lineOutcome{excType: "STATUS_MISMATCH", severity: SevHigh, reason: "the provider reports a returned payout FundZim has not recorded"}
		}
		return lineOutcome{}
	}
	switch it.Status {
	case "COMPLETED":
		switch {
		case payoutDone[s.Status]:
			if it.Amount != s.Amount {
				return lineOutcome{excType: "PAYOUT_MISMATCH", severity: SevCritical, reason: "the provider paid a different amount"}
			}
			return lineOutcome{expectJournal: true, feeApplies: true}
		case s.Status == "UNKNOWN":
			return lineOutcome{excType: "UNKNOWN_PAYOUT_OUTCOME", severity: SevCritical, subjectDedupe: true,
				reason: "the statement reports a completed payout whose outcome is unknown; nothing is applied or resubmitted automatically"}
		case payoutInFlight[s.Status]:
			return lineOutcome{excType: "STATUS_MISMATCH", severity: SevLow, subjectDedupe: true, reason: "FundZim has not yet recorded the completion the provider reports"}
		case payoutEnded[s.Status], payoutBefore[s.Status]:
			return lineOutcome{excType: "PAYOUT_MISMATCH", severity: SevCritical, reason: "the provider paid out a payout FundZim did not complete"}
		}
	case "FAILED":
		switch {
		case payoutDone[s.Status]:
			return lineOutcome{excType: "PAYOUT_MISMATCH", severity: SevCritical, reason: "the provider reports failure for a payout FundZim recorded as completed"}
		case s.Status == "UNKNOWN":
			return lineOutcome{excType: "UNKNOWN_PAYOUT_OUTCOME", severity: SevCritical, subjectDedupe: true,
				reason: "the statement reports a failed payout whose outcome is unknown; nothing is applied or resubmitted automatically"}
		case payoutEnded[s.Status]:
			return lineOutcome{consistentFailure: true}
		case payoutInFlight[s.Status]:
			return lineOutcome{excType: "STATUS_MISMATCH", severity: SevLow, subjectDedupe: true, reason: "FundZim has not yet recorded the failure the provider reports"}
		}
	case "RETURNED":
		switch {
		case s.Status == "REVERSED":
			return lineOutcome{}
		case s.Status == "COMPLETED":
			return lineOutcome{excType: "STATUS_MISMATCH", severity: SevHigh, subjectDedupe: true, reason: "the provider reports a return FundZim has not recorded"}
		default:
			return lineOutcome{excType: "PAYOUT_MISMATCH", severity: SevCritical, reason: "the provider reports a return for a payout FundZim did not complete"}
		}
	}
	return lineOutcome{informational: true}
}

// BatchLine is one line of a settlement batch as it stands after a run.
type BatchLine struct {
	Direction   string
	Amount      int64
	Fee         *int64 // reported
	ExpectedFee *int64 // FundZim's assessed provider fee, when the line is matched and FundZim knows it
	Settled     bool   // MATCHED or explained (IGNORED)
}

// BatchExpectation is FundZim's expectation of a batch's signed net (credits − debits − fees, using FundZim's own fee
// where it knows it) and whether every line is settled. A batch with an unsettled line stays in DISCREPANCY.
func BatchExpectation(lines []BatchLine) (net int64, complete bool) {
	complete = true
	for _, l := range lines {
		fee := int64(0)
		switch {
		case l.ExpectedFee != nil:
			fee = *l.ExpectedFee
		case l.Fee != nil:
			fee = *l.Fee
		}
		if l.Direction == "CREDIT" {
			net += l.Amount - fee
		} else {
			net -= l.Amount + fee
		}
		if !l.Settled {
			complete = false
		}
	}
	return net, complete
}

// CompareBatch turns a batch decision into SETTLEMENT_SHORT / SETTLEMENT_OVER when the provider's header net differs
// from FundZim's expectation (called with the stored header).
func CompareBatch(sourceID string, d BatchDecision, headerNet int64) BatchDecision {
	if d.ExpectedNet == headerNet {
		return d
	}
	typ := "SETTLEMENT_SHORT"
	if headerNet > d.ExpectedNet {
		typ = "SETTLEMENT_OVER"
	}
	exp, act := d.ExpectedNet, headerNet
	e := ExceptionDraft{Type: typ, Severity: SevHigh, BatchID: d.BatchID, DedupeKey: typ + ":batch:" + d.BatchID,
		Detail: map[string]any{"expected_net_minor": fmt.Sprint(exp), "reported_net_minor": fmt.Sprint(act), "source_id": sourceID,
			"note": "signed nets; the difference is never released or converted"}}
	// amounts on the exception row are non-negative magnitudes; the signed values in detail are authoritative
	e.Expected, e.Actual = i64(abs(exp)), i64(abs(act))
	d.Status, d.Exception = "DISCREPANCY", &e
	return d
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func matchBank(in MatchInput, items []Item) MatchResult {
	var res MatchResult
	horizon := time.Duration(in.Rules.SettlementHorizonDays) * 24 * time.Hour
	rem := map[string][]*Item{}
	remList := append([]Item(nil), in.Remittances...)
	for i := range remList {
		r := &remList[i]
		for _, k := range []string{r.BatchRef, r.ProviderRef, r.ID} {
			if k != "" {
				rem[strings.ToUpper(k)] = append(rem[strings.ToUpper(k)], r)
			}
		}
	}
	usedRem := map[string]bool{}
	for k := range in.AlreadyMatched {
		usedRem[k] = true
	}
	for _, it := range items {
		d := Decision{ItemID: it.ID, Status: "EXCEPTION"}
		add := func(typ, sev string, e ExceptionDraft) {
			e.Type, e.Severity, e.ItemID = typ, sev, it.ID
			if e.DedupeKey == "" {
				e.DedupeKey = itemKey(typ, it.ID)
			}
			if e.Detail == nil {
				e.Detail = map[string]any{}
			}
			e.Detail["direction"] = it.Direction
			d.Exceptions = append(d.Exceptions, e)
		}
		// Model A alarm first: a FundZim payment id printed on a transfer into FundZim's own account
		var donor []string
		for _, m := range uuidInText.FindAllString(it.CounterpartyRef, 4) {
			if in.KnownPaymentIDs[strings.ToLower(m)] {
				donor = append(donor, strings.ToLower(m))
			}
		}
		if len(donor) > 0 {
			add("POSSIBLE_DONOR_FUNDS_IN_OPERATING_ACCOUNT", SevCritical, ExceptionDraft{Actual: i64(it.Amount),
				Detail: map[string]any{"payment_ids": donor, "note": "Model A (ADR-013): donor funds must never reach FundZim's own account"}})
			res.Decisions = append(res.Decisions, d)
			continue
		}
		var cands []*Item
		seen := map[string]bool{}
		for _, tok := range strings.Fields(strings.ToUpper(it.CounterpartyRef)) {
			for _, r := range rem[tok] {
				if !seen[r.ID] {
					seen[r.ID] = true
					cands = append(cands, r)
				}
			}
		}
		if len(cands) == 0 {
			if r := rem[strings.ToUpper(it.CounterpartyRef)]; len(r) > 0 {
				cands = r
			}
		}
		switch {
		case len(cands) == 0 || it.Direction != "CREDIT":
			sev := SevHigh
			if it.Direction == "DEBIT" {
				sev = SevCritical
			}
			add("UNMATCHED_BANK_ENTRY", sev, ExceptionDraft{Actual: i64(it.Amount), Detail: map[string]any{
				"note": "a bank line is never allocated by amount alone"}})
		case len(cands) > 1:
			ids := make([]string, 0, len(cands))
			for _, c := range cands {
				ids = append(ids, c.ID)
			}
			sort.Strings(ids)
			add("AMBIGUOUS_MATCH", SevHigh, ExceptionDraft{Actual: i64(it.Amount), Detail: map[string]any{"candidates": ids}})
		default:
			r := cands[0]
			switch {
			case usedRem[SubjectKey(SubjRemittance, r.ID, "BANK_TRANSFER")]:
				add("DUPLICATE_TRANSACTION", SevHigh, ExceptionDraft{SubjectType: SubjRemittance, SubjectID: r.ID, Actual: i64(it.Amount)})
			case r.Currency != it.Currency:
				add("CURRENCY_MISMATCH", SevCritical, ExceptionDraft{SubjectType: SubjRemittance, SubjectID: r.ID, RawCurrency: it.RawCurrency,
					Expected: i64(r.Amount), Actual: i64(it.Amount)})
			case r.Amount != it.Amount:
				add("AMOUNT_MISMATCH", SevHigh, ExceptionDraft{SubjectType: SubjRemittance, SubjectID: r.ID, Expected: i64(r.Amount), Actual: i64(it.Amount)})
			default:
				d.Status = "MATCHED"
				d.Match = &MatchDraft{SubjectType: SubjRemittance, SubjectID: r.ID, Confidence: "RULE_BASED_MATCH", RuleCode: RuleRemittance,
					Amount: it.Amount, LedgerStatus: "NOT_APPLICABLE", FeeStatus: "NOT_APPLICABLE",
					Explanation: map[string]any{"remittance_line": r.ID, "reference_token_matched": true, "amount_checked": true, "currency_checked": true,
						"rule_set_version": in.Rules.Version, "matcher_version": MatcherVersion}}
				usedRem[SubjectKey(SubjRemittance, r.ID, "BANK_TRANSFER")] = true
			}
		}
		res.Decisions = append(res.Decisions, d)
	}
	// remittances that should have reached the bank inside a covered period
	sort.Slice(remList, func(i, j int) bool { return remList[i].ID < remList[j].ID })
	for _, r := range remList {
		if usedRem[SubjectKey(SubjRemittance, r.ID, "BANK_TRANSFER")] || r.Currency != in.Currency {
			continue
		}
		if in.Now.Sub(r.BookedAt) < horizon || in.Covered == nil || !in.Covered(r.BookedAt) {
			continue
		}
		res.Subjects = append(res.Subjects, ExceptionDraft{Type: "MISSING_BANK_CREDIT", Severity: SevMedium, ItemID: r.ID,
			DedupeKey: subjectExcKey("MISSING_BANK_CREDIT", in.SourceID, SubjRemittance, r.ID), Expected: i64(r.Amount),
			Detail: map[string]any{"remittance_line": r.ID, "settlement_horizon_days": in.Rules.SettlementHorizonDays}})
	}
	return res
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func refState(given string, agrees bool) string {
	switch {
	case given == "":
		return "ABSENT"
	case agrees:
		return "AGREES"
	}
	return "NOT_KNOWN_FOR_RECORD"
}

// hashResult commits to the decisions (order is deterministic) so a replay with the same input is verifiably identical.
func hashResult(r MatchResult) [32]byte {
	type row struct {
		Item, Status, Subject, Confidence, Rule, Ledger, Fee string
		Exceptions                                           []string
	}
	var rows []row
	for _, d := range r.Decisions {
		x := row{Item: d.ItemID, Status: d.Status}
		if d.Match != nil {
			x.Subject, x.Confidence, x.Rule, x.Ledger, x.Fee = d.Match.SubjectType+":"+d.Match.SubjectID, d.Match.Confidence, d.Match.RuleCode,
				d.Match.LedgerStatus, d.Match.FeeStatus
		}
		for _, e := range d.Exceptions {
			x.Exceptions = append(x.Exceptions, e.DedupeKey+"/"+e.Severity)
		}
		rows = append(rows, x)
	}
	var subj []string
	for _, e := range r.Subjects {
		subj = append(subj, e.DedupeKey)
	}
	raw, _ := json.Marshal(map[string]any{"decisions": rows, "subjects": subj, "matcher": MatcherVersion})
	return sha256.Sum256(raw)
}

type paymentFact = payments.ReconFact
type payoutFact = payouts.ReconFact

func paymentSubject(p payments.ReconFact) Subject {
	at := p.CreatedAt
	if p.SucceededAt != nil {
		at = *p.SucceededAt
	}
	return Subject{Type: SubjPayment, ID: p.ID, Status: p.Status, Amount: p.AmountMinor, Currency: p.Currency, ProviderRefs: p.ProviderReferences,
		At: at, Test: p.TestPayment()}
}

func payoutSubject(p payouts.ReconFact) Subject {
	var at time.Time
	switch {
	case p.CompletedAt != nil:
		at = *p.CompletedAt
	case p.SubmittedAt != nil:
		at = *p.SubmittedAt
	}
	var refs []string
	if p.ProviderReference != "" {
		refs = []string{p.ProviderReference}
	}
	return Subject{Type: SubjPayout, ID: p.ID, Status: p.Status, Amount: p.AmountMinor, Currency: p.Currency, ProviderRefs: refs, At: at, Test: p.TestPayout}
}
