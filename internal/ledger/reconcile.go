package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// PaymentFact is what the payments module knows about one payment (no personal data).
type PaymentFact struct {
	ID                   string
	Status               string
	AmountMinor          int64
	Currency             string
	ExecutionEnvironment string // LOCAL_SIMULATOR | PROVIDER_SANDBOX | PRODUCTION
	ProviderReference    string
	UpdatedAt            time.Time
}

// PaymentFacts is implemented over the payments module by the composition root (ledger never reads payment tables).
type PaymentFacts interface {
	Payment(ctx context.Context, id string) (PaymentFact, bool, error)
	// Payments returns payments in a status updated before the cut-off (oldest first, at most limit).
	PaymentsInStatus(ctx context.Context, status string, updatedBefore time.Time, limit int) ([]PaymentFact, error)
}

// Ledger posting eligibility of a payment execution environment: only PRODUCTION payments may ever be posted, and
// only into a PRODUCTION ledger — which Stage 10 does not have.
func eligibleForLedger(paymentEnv, ledgerEnv string) bool {
	return paymentEnv == "PRODUCTION" && ledgerEnv == EnvProduction
}

// ---- payments consumer -----------------------------------------------------------------------------------------

// ConsumerPayments is the outbox consumer name for payments.payment_succeeded.
const ConsumerPayments = "ledger.payments"

// RecordPaymentSucceeded evaluates one payments.payment_succeeded event for posting and records the decision as a
// source reference. In Stage 10 nothing is posted: test payments are NOT_ELIGIBLE (they never count as donations),
// production payments are BLOCKED (production posting disabled). Idempotent per (event id, rule).
func (s *Service) RecordPaymentSucceeded(ctx context.Context, eventID string, payload []byte) error {
	var p struct {
		PaymentID   string `json:"payment_id"`
		Env         string `json:"execution_environment"`
		TestPayment *bool  `json:"test_payment"`
		Simulated   bool   `json:"simulated"`
	}
	if err := json.Unmarshal(payload, &p); err != nil || !uuidRE.MatchString(p.PaymentID) {
		return fmt.Errorf("ledger: malformed payments.payment_succeeded payload")
	}
	env, err := s.Environment(ctx)
	if err != nil {
		return err
	}
	evidence, decision, reason := EvidenceProviderReported, "NOT_ELIGIBLE", "TEST_PAYMENT"
	if p.Simulated {
		evidence = EvidenceSimulated
	}
	switch {
	case p.TestPayment == nil || *p.TestPayment || p.Env != "PRODUCTION":
		// a test payment (simulator or provider sandbox) never enters any ledger as a donation
	case env != EnvProduction:
		decision, reason = "NOT_ELIGIBLE", "LEDGER_ENVIRONMENT_MISMATCH"
	case !ProductionPostingEnabled:
		decision, reason = "BLOCKED", "PRODUCTION_POSTING_DISABLED"
	default:
		// unreachable in Stage 10: production posting needs provider-verified capture evidence and the
		// DONATION_CAPTURED posting command (Stage 11+ under an ADR)
		decision, reason = "BLOCKED", "POSTING_NOT_IMPLEMENTED"
	}
	var amt *int64
	var ccy *string
	if s.Facts != nil {
		if f, ok, err := s.Facts.Payment(ctx, p.PaymentID); err != nil {
			return err
		} else if ok && f.AmountMinor > 0 {
			a, c := f.AmountMinor, f.Currency
			amt, ccy = &a, &c
		}
	}
	return db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO ledger.ledger_source_references (id, environment, source_system, source_type, source_id,
			source_event_id, posting_rule, evidence_class, decision, reason_code, amount_minor, currency, observed_at)
			VALUES ($1, $2, 'payments', 'payment', $3, $4, 'DONATION_CAPTURED', $5, $6, $7, $8, $9, $10)
			ON CONFLICT ON CONSTRAINT uq_ledger_source_references_identity DO NOTHING`,
			ids.New(), env, p.PaymentID, eventID, evidence, decision, reason, amt, ccy, s.now())
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		_, err = audit.RecordGateway(ctx, tx, audit.Event{Action: "ledger.source.evaluated", ActorType: "system", TargetType: "payment",
			TargetID: p.PaymentID, Metadata: map[string]any{"decision": decision, "reason_code": reason, "evidence_class": evidence,
				"environment": env, "posting_rule": "DONATION_CAPTURED"}})
		return err
	})
}

// ---- reconciliation --------------------------------------------------------------------------------------------

// Reconciliation exception types.
const (
	ExMissingPosting   = "MISSING_POSTING"
	ExDuplicatePosting = "DUPLICATE_POSTING"
	ExAmountMismatch   = "AMOUNT_MISMATCH"
	ExCurrencyMismatch = "CURRENCY_MISMATCH"
	ExUnknownOutcome   = "UNKNOWN_OUTCOME"
	ExUnmatchedRef     = "UNMATCHED_PROVIDER_REFERENCE"
	ExOrphanPosting    = "ORPHAN_POSTING"
	ExSuspenseBalance  = "SUSPENSE_BALANCE"
)

// ReconcileReport summarises one run.
type ReconcileReport struct {
	RunID                    string         `json:"run_id"`
	Environment              string         `json:"environment"`
	StartedAt                time.Time      `json:"started_at"`
	Checked                  map[string]int `json:"checked"`
	NewExceptions            int            `json:"new_exceptions"`
	ByType                   map[string]int `json:"by_type"`
	SettlementReconciliation string         `json:"settlement_reconciliation"`
}

type exception struct {
	typ, sourceType, sourceID, txID, accountID string
	expected, actual                           *int64
	currency                                   string
	detail                                     map[string]any
}

// UnknownOutcomeAge is how long a payment may stay UNKNOWN before reconciliation reports it.
const UnknownOutcomeAge = time.Hour

// Reconcile compares operational payment facts, ledger source references and ledger journals:
//   - journals from payments whose payment is missing (orphan), or whose amount/currency/provider reference differ;
//   - more than one capture journal for one payment (duplicate);
//   - succeeded payments eligible for this ledger with no journal after a grace period (missing posting), and
//     succeeded payments never evaluated by the ledger consumer (missing source reference);
//   - payments UNKNOWN for longer than UnknownOutcomeAge (informational);
//   - suspense accounts with a non-zero balance (each needs an investigation owner).
//
// Settlement reconciliation is NOT performed: no provider settlement evidence exists (PCR-007, PCR-016; Stage 17).
func (s *Service) Reconcile(ctx context.Context, triggeredBy string) (ReconcileReport, error) {
	env, err := s.Environment(ctx)
	if err != nil {
		return ReconcileReport{}, err
	}
	rep := ReconcileReport{RunID: "recon:" + ids.New(), Environment: env, StartedAt: s.now(), Checked: map[string]int{}, ByType: map[string]int{},
		SettlementReconciliation: "NOT_PERFORMED: no provider settlement evidence (PCR-007, PCR-016; Stage 17)"}
	var found []exception

	// 1. journals sourced from payments
	rows, err := s.Pool.Query(ctx, `SELECT t.id, t.source_id::text, t.currency, coalesce(t.provider_reference, ''),
		(SELECT coalesce(sum(e.amount_minor) FILTER (WHERE e.direction = 'DEBIT'), 0) FROM ledger.ledger_entries e
		  JOIN ledger.ledger_accounts a ON a.id = e.account_id WHERE e.transaction_id = t.id AND a.account_class = 'asset:psp_clearing')
		FROM ledger.ledger_transactions t WHERE t.source_system = 'payments' AND t.source_type = 'payment' AND t.posting_rule = 'DONATION_CAPTURED'
		ORDER BY t.posted_at LIMIT 10000`)
	if err != nil {
		return rep, err
	}
	type pj struct {
		id, payment, ccy, ref string
		gross                 int64
	}
	var journals []pj
	for rows.Next() {
		var j pj
		if err := rows.Scan(&j.id, &j.payment, &j.ccy, &j.ref, &j.gross); err != nil {
			rows.Close()
			return rep, err
		}
		journals = append(journals, j)
	}
	rows.Close()
	perPayment := map[string]int{}
	for _, j := range journals {
		rep.Checked["payment_journals"]++
		perPayment[j.payment]++
		if perPayment[j.payment] == 2 {
			found = append(found, exception{typ: ExDuplicatePosting, sourceType: "payment", sourceID: j.payment, txID: j.id, currency: j.ccy})
		}
		if s.Facts == nil {
			continue
		}
		f, ok, err := s.Facts.Payment(ctx, j.payment)
		if err != nil {
			return rep, err
		}
		g := j.gross
		switch {
		case !ok:
			found = append(found, exception{typ: ExOrphanPosting, sourceType: "payment", sourceID: j.payment, txID: j.id, currency: j.ccy})
		case f.Currency != j.ccy:
			found = append(found, exception{typ: ExCurrencyMismatch, sourceType: "payment", sourceID: j.payment, txID: j.id, currency: j.ccy,
				detail: map[string]any{"payment_currency": f.Currency}})
		case f.AmountMinor != j.gross:
			a := f.AmountMinor
			found = append(found, exception{typ: ExAmountMismatch, sourceType: "payment", sourceID: j.payment, txID: j.id, currency: j.ccy,
				expected: &a, actual: &g})
		case j.ref != "" && f.ProviderReference != "" && j.ref != f.ProviderReference:
			found = append(found, exception{typ: ExUnmatchedRef, sourceType: "payment", sourceID: j.payment, txID: j.id, currency: j.ccy})
		}
	}

	// 2. succeeded payments: evaluated by the consumer? posted when eligible?
	if s.Facts != nil {
		grace := s.now().Add(-5 * time.Minute)
		facts, err := s.Facts.PaymentsInStatus(ctx, "SUCCEEDED", grace, 5000)
		if err != nil {
			return rep, err
		}
		for _, f := range facts {
			rep.Checked["succeeded_payments"]++
			var decision string
			err := s.Pool.QueryRow(ctx, `SELECT decision FROM ledger.ledger_source_references WHERE source_system = 'payments'
				AND source_type = 'payment' AND source_id = $1 AND posting_rule = 'DONATION_CAPTURED' ORDER BY created_at LIMIT 1`, f.ID).Scan(&decision)
			if errors.Is(err, pgx.ErrNoRows) {
				a := f.AmountMinor
				found = append(found, exception{typ: ExMissingPosting, sourceType: "payment", sourceID: f.ID, currency: f.Currency, expected: &a,
					detail: map[string]any{"reason": "NOT_EVALUATED_BY_LEDGER"}})
				continue
			}
			if err != nil {
				return rep, err
			}
			if eligibleForLedger(f.ExecutionEnvironment, env) && decision != "POSTED" {
				a := f.AmountMinor
				found = append(found, exception{typ: ExMissingPosting, sourceType: "payment", sourceID: f.ID, currency: f.Currency, expected: &a,
					detail: map[string]any{"reason": "ELIGIBLE_NOT_POSTED", "decision": decision}})
			}
		}
		unknown, err := s.Facts.PaymentsInStatus(ctx, "UNKNOWN", s.now().Add(-UnknownOutcomeAge), 1000)
		if err != nil {
			return rep, err
		}
		for _, f := range unknown {
			rep.Checked["unknown_payments"]++
			a := f.AmountMinor
			found = append(found, exception{typ: ExUnknownOutcome, sourceType: "payment", sourceID: f.ID, currency: f.Currency, expected: &a})
		}
	}

	// 3. suspense balances
	srows, err := s.Pool.Query(ctx, `SELECT a.id, a.currency, b.balance_minor FROM ledger.ledger_accounts a
		JOIN ledger.v_balances_current b ON b.account_id = a.id WHERE a.kind = 'SUSPENSE' AND b.balance_minor <> 0`)
	if err != nil {
		return rep, err
	}
	for srows.Next() {
		var id, ccy string
		var bal int64
		if err := srows.Scan(&id, &ccy, &bal); err != nil {
			srows.Close()
			return rep, err
		}
		rep.Checked["suspense_accounts"]++
		b := bal
		found = append(found, exception{typ: ExSuspenseBalance, sourceType: "ledger_account", sourceID: id, accountID: id, currency: ccy, actual: &b})
	}
	srows.Close()

	// record new exceptions (one unresolved reference per type and source)
	err = db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		for _, e := range found {
			d, _ := json.Marshal(nonNilAny(e.detail))
			id := ids.New()
			var ccy any
			if e.currency != "" {
				ccy = e.currency
			}
			tag, err := tx.Exec(ctx, `INSERT INTO ledger.ledger_reconciliation_references (id, environment, exception_type, source_type, source_id,
				transaction_id, account_id, expected_amount_minor, actual_amount_minor, currency, detail, status, detected_at, detected_by_run)
				VALUES ($1, $2, $3, $4, $5, nullif($6, '')::uuid, nullif($7, '')::uuid, $8, $9, $10, $11, 'OPEN', $12, $13)
				ON CONFLICT (exception_type, source_type, source_id) WHERE status <> 'RESOLVED' DO NOTHING`,
				id, env, e.typ, e.sourceType, e.sourceID, e.txID, e.accountID, e.expected, e.actual, ccy, d, s.now(), rep.RunID)
			if err != nil {
				return mapDBError(err)
			}
			if tag.RowsAffected() == 0 {
				continue
			}
			rep.NewExceptions++
			rep.ByType[e.typ]++
			if _, err := outbox.WriteGateway(ctx, tx, outbox.Event{AggregateType: "ledger_reconciliation_reference", AggregateID: id,
				EventType: EvReconciliationException, Payload: map[string]any{"reference_id": id, "exception_type": e.typ, "source_type": e.sourceType,
					"source_ref": e.sourceID, "environment": env}, OccurredAt: s.now()}); err != nil {
				return err
			}
		}
		_, err := audit.RecordGateway(ctx, tx, audit.Event{Action: "ledger.reconciliation.run", ActorType: "system", TargetType: "ledger_reconciliation",
			Metadata: map[string]any{"run": rep.RunID, "environment": env, "new_exceptions": rep.NewExceptions, "triggered_by": triggeredBy,
				"settlement": "NOT_PERFORMED"}})
		return err
	})
	return rep, err
}

func nonNilAny(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// ReconException is a reconciliation reference as staff see it.
type ReconException struct {
	ID             string          `json:"id"`
	Environment    string          `json:"environment"`
	ExceptionType  string          `json:"exception_type"`
	SourceType     string          `json:"source_type"`
	SourceID       string          `json:"source_id"`
	TransactionID  *string         `json:"transaction_id"`
	AccountID      *string         `json:"account_id"`
	ExpectedMinor  *string         `json:"expected_amount_minor"`
	ActualMinor    *string         `json:"actual_amount_minor"`
	Currency       *string         `json:"currency"`
	Detail         json.RawMessage `json:"detail"`
	Status         string          `json:"status"`
	OwnerID        *string         `json:"owner_id"`
	DetectedAt     time.Time       `json:"detected_at"`
	AgeSeconds     int64           `json:"age_seconds"`
	DetectedByRun  string          `json:"detected_by_run"`
	ResolvedBy     *string         `json:"resolved_by"`
	ResolvedAt     *time.Time      `json:"resolved_at"`
	ResolutionNote *string         `json:"resolution_note"`
}

const reconSelect = `SELECT id, environment, exception_type, source_type, source_id::text, transaction_id::text, account_id::text,
	expected_amount_minor::text, actual_amount_minor::text, currency, detail, status, owner_id::text, detected_at, detected_by_run,
	resolved_by::text, resolved_at, resolution_note FROM ledger.ledger_reconciliation_references`

func (s *Service) scanRecon(r pgx.Row) (ReconException, error) {
	var e ReconException
	err := r.Scan(&e.ID, &e.Environment, &e.ExceptionType, &e.SourceType, &e.SourceID, &e.TransactionID, &e.AccountID, &e.ExpectedMinor,
		&e.ActualMinor, &e.Currency, &e.Detail, &e.Status, &e.OwnerID, &e.DetectedAt, &e.DetectedByRun, &e.ResolvedBy, &e.ResolvedAt,
		&e.ResolutionNote)
	e.AgeSeconds = int64(s.now().Sub(e.DetectedAt).Seconds())
	return e, err
}

// ReconExceptions lists exceptions (optionally by status and type), newest first.
func (s *Service) ReconExceptions(ctx context.Context, status, typ string, limit int) ([]ReconException, error) {
	rows, err := s.Pool.Query(ctx, reconSelect+` WHERE ($1 = '' OR status = $1) AND ($2 = '' OR exception_type = $2)
		ORDER BY detected_at DESC, id DESC LIMIT $3`, status, typ, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ReconException, error) { return s.scanRecon(r) })
}

var ErrReconState = errs.New(errs.Conflict, "LEDGER_RECONCILIATION_STATE", "The exception is not in a state that allows this action.")

// InvestigateException assigns an investigation owner (OPEN → INVESTIGATING).
func (s *Service) InvestigateException(ctx context.Context, staffID, id, note string) error {
	note, err := cleanReason(note)
	if err != nil {
		return err
	}
	return s.reconStep(ctx, id, func(ctx context.Context, tx pgx.Tx, status string) error {
		if status != "OPEN" {
			return ErrReconState
		}
		if _, err := tx.Exec(ctx, `UPDATE ledger.ledger_reconciliation_references SET status = 'INVESTIGATING', owner_id = $2 WHERE id = $1`,
			id, staffID); err != nil {
			return mapDBError(err)
		}
		_, err := audit.RecordGateway(ctx, tx, audit.Event{Action: "ledger.reconciliation.investigating", ActorType: "staff", ActorID: staffID,
			TargetType: "ledger_reconciliation_reference", TargetID: id, Reason: note})
		return err
	})
}

// ResolveException resolves an exception with a note (and, where a correction was needed, the approved adjustment).
// It never changes the ledger itself.
func (s *Service) ResolveException(ctx context.Context, staffID, id, note, adjustmentID string) error {
	note, err := cleanReason(note)
	if err != nil {
		return err
	}
	return s.reconStep(ctx, id, func(ctx context.Context, tx pgx.Tx, status string) error {
		if status == "RESOLVED" {
			return ErrReconState
		}
		if _, err := tx.Exec(ctx, `UPDATE ledger.ledger_reconciliation_references SET status = 'RESOLVED', resolved_by = $2, resolved_at = $3,
			resolution_note = $4, resolution_adjustment_id = nullif($5, '')::uuid, owner_id = coalesce(owner_id, $2) WHERE id = $1`,
			id, staffID, s.now(), note, adjustmentID); err != nil {
			return mapDBError(err)
		}
		_, err := audit.RecordGateway(ctx, tx, audit.Event{Action: "ledger.reconciliation.resolved", ActorType: "staff", ActorID: staffID,
			TargetType: "ledger_reconciliation_reference", TargetID: id, Reason: note, Metadata: map[string]any{"adjustment": adjustmentID}})
		return err
	})
}

func (s *Service) reconStep(ctx context.Context, id string, f func(context.Context, pgx.Tx, string) error) error {
	if !uuidRE.MatchString(id) {
		return ErrNotFound
	}
	return db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		var st string
		err := tx.QueryRow(ctx, `SELECT status FROM ledger.ledger_reconciliation_references WHERE id = $1 FOR UPDATE`, id).Scan(&st)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		return f(ctx, tx, st)
	})
}
