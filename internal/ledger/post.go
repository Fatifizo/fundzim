package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// Outbox events (published after commit by the relay; consumers must be idempotent on the event id).
const (
	EvJournalPosted           = "ledger.journal_posted"
	EvJournalReversed         = "ledger.journal_reversed"
	EvAccountFrozen           = "ledger.account_frozen"
	EvAccountStatusChanged    = "ledger.account_status_changed"
	EvPeriodClosed            = "ledger.period_closed"
	EvReconciliationException = "ledger.reconciliation_exception"
)

// Result is a posted (or replayed) journal.
type Result struct {
	TransactionID string `json:"transaction_id"`
	Replayed      bool   `json:"replayed"`
}

type account struct {
	ID, Code, Class, Status, Normal, Mode string
	NonNegative                           bool
}

// Post writes one journal inside tx (the caller commits; the deferred check runs at COMMIT). Idempotent: the same
// key with an equivalent journal returns the existing id; a different journal is ErrIdempotencyConflict. A
// concurrent writer winning the key race aborts tx with errRetry — use PostOnce, which retries the whole
// transaction and then finds the winner's journal.
func (s *Service) Post(ctx context.Context, tx pgx.Tx, p Posting) (Result, error) {
	env, err := s.environment(ctx, tx)
	if err != nil {
		return Result{}, err
	}
	r, err := s.validate(ctx, tx, env, p)
	if err != nil {
		return Result{}, err
	}
	// 1. idempotency
	if existing, found, err := journalByKey(ctx, tx, p.IdempotencyKey); err != nil {
		return Result{}, err
	} else if found {
		if !existing.equivalent(p) {
			s.Logger.Error("ledger: idempotency conflict (ALERT)", "idempotency_key", p.IdempotencyKey, "existing", existing.ID)
			return Result{}, ErrIdempotencyConflict
		}
		return Result{TransactionID: existing.ID, Replayed: true}, nil
	}
	// 2. accounts (lazy, idempotent), sorted by id for lock and insert order
	accts, err := s.ensureAccounts(ctx, tx, env, p.Currency, p.Lines, p.Actor)
	if err != nil {
		return Result{}, err
	}
	for _, a := range accts {
		switch {
		case a.Status == "CLOSED":
			return Result{}, ErrAccountClosed
		case a.Status == "FROZEN" && p.AdjustmentID == "":
			return Result{}, ErrAccountFrozen
		}
	}
	// 3. lock non-negative (SYNC) accounts this journal decreases, ascending id, and check sufficiency
	if err := lockAndCheck(ctx, tx, accts, p.Lines); err != nil {
		return Result{}, err
	}
	// 4. header, then entries sorted by account id
	id := ids.New()
	md, _ := json.Marshal(nonNilMap(p.Metadata))
	_, err = tx.Exec(ctx, `INSERT INTO ledger.ledger_transactions (id, posting_rule, posting_rule_version, environment, source_system,
		source_event_id, evidence_class, metadata, idempotency_key, currency, description, source_type, source_id, provider_code,
		provider_reference, occurred_at, accounting_period_id, reverses_transaction_id, adjustment_id, created_by_type, created_by_id,
		reason, correlation_id, fee_schedule_version_id)
		VALUES ($1, $2, $3, $4, $5, nullif($6, ''), $7, $8, $9, $10, $11, $12, $13, nullif($14, ''), nullif($15, ''), $16,
		'00000000-0000-0000-0000-000000000000', nullif($17, '')::uuid, nullif($18, '')::uuid, $19, $20, nullif($21, ''), nullif($22, ''),
		nullif($23, '')::uuid)`,
		id, r.Code, r.Version, env, p.SourceSystem, p.SourceEventID, p.EvidenceClass, md, p.IdempotencyKey, p.Currency, p.Description,
		p.SourceType, p.SourceID, p.ProviderCode, p.ProviderRef, p.OccurredAt.UTC(), p.Reverses, p.AdjustmentID, p.Actor.Type, p.Actor.ID,
		p.Reason, p.CorrelationID, p.FeePolicyVersionID)
	if err != nil {
		if isUnique(err, "uq_ledger_transactions_idempotency_key") {
			return Result{}, errRetry
		}
		return Result{}, mapDBError(err)
	}
	lines := append([]Line(nil), p.Lines...)
	sort.SliceStable(lines, func(i, j int) bool { return accts[lines[i].Account.Code()].ID < accts[lines[j].Account.Code()].ID })
	batch := &pgx.Batch{}
	for i, l := range lines {
		batch.Queue(`INSERT INTO ledger.ledger_entries (id, transaction_id, line_no, account_id, direction, amount_minor, currency)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, ids.New(), id, i+1, accts[l.Account.Code()].ID, l.Direction, l.Amount, p.Currency)
	}
	br := tx.SendBatch(ctx, batch)
	for range lines {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return Result{}, mapDBError(err)
		}
	}
	if err := br.Close(); err != nil {
		return Result{}, mapDBError(err)
	}
	// 5. source reference (business posting identity), audit, outbox — same transaction
	if p.SourceEventID != "" {
		var total int64
		for _, l := range p.Lines {
			if l.Direction == Debit {
				total += l.Amount
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ledger.ledger_source_references (id, environment, source_system, source_type, source_id,
			source_event_id, posting_rule, evidence_class, decision, reason_code, transaction_id, amount_minor, currency, observed_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'POSTED', 'POSTED', $9, $10, $11, $12)
			ON CONFLICT ON CONSTRAINT uq_ledger_source_references_identity DO NOTHING`,
			ids.New(), env, p.SourceSystem, p.SourceType, p.SourceID, p.SourceEventID, r.Code, p.EvidenceClass, id, total, p.Currency,
			s.now()); err != nil {
			return Result{}, mapDBError(err)
		}
	}
	actorType, actorID := "system", ""
	if p.Actor.Type == "STAFF" {
		actorType, actorID = "staff", p.Actor.ID
	}
	action, event := "ledger.journal.posted", EvJournalPosted
	if p.Reverses != "" {
		action, event = "ledger.journal.reversed", EvJournalReversed
	}
	if _, err := audit.RecordGateway(ctx, tx, audit.Event{Action: action, ActorType: actorType, ActorID: actorID,
		TargetType: "ledger_transaction", TargetID: id, Reason: p.Reason, Metadata: map[string]any{"posting_rule": r.Code,
			"rule_version": r.Version, "idempotency_key": p.IdempotencyKey, "environment": env, "currency": p.Currency,
			"source_system": p.SourceSystem, "source_type": p.SourceType, "source_ref": p.SourceID, "evidence_class": p.EvidenceClass,
			"line_count": len(p.Lines), "reverses": p.Reverses, "adjustment": p.AdjustmentID}}); err != nil {
		return Result{}, err
	}
	if _, err := outbox.WriteGateway(ctx, tx, outbox.Event{AggregateType: "ledger_transaction", AggregateID: id, EventType: event,
		Payload: map[string]any{"transaction_id": id, "posting_rule": r.Code, "environment": env, "currency": p.Currency,
			"source_system": p.SourceSystem, "source_type": p.SourceType, "source_ref": p.SourceID, "reverses": p.Reverses},
		CorrelationID: p.CorrelationID, OccurredAt: s.now()}); err != nil {
		return Result{}, err
	}
	return Result{TransactionID: id}, nil
}

// PostOnce posts in its own transaction, retrying when a concurrent writer won the key race (the retry returns the
// winner's journal) or on a deadlock/serialization failure. A final failure is audited as ledger.journal.rejected.
func (s *Service) PostOnce(ctx context.Context, p Posting) (Result, error) {
	var res Result
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		err = db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			var e error
			res, e = s.Post(ctx, tx, p)
			return e
		})
		if err == nil {
			return res, nil
		}
		err = mapDBError(err)
		if !errors.Is(err, errRetry) && !isTransient(err) {
			break
		}
		time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
	}
	if errors.Is(err, errRetry) {
		err = ErrRejectedByDatabase
	}
	s.auditRejected(ctx, p, err)
	return Result{}, err
}

func (s *Service) auditRejected(ctx context.Context, p Posting, cause error) {
	code := "LEDGER_ERROR"
	var e *errs.Error
	if errors.As(cause, &e) {
		code = e.Code
	}
	actorType, actorID := "system", ""
	if p.Actor.Type == "STAFF" {
		actorType, actorID = "staff", p.Actor.ID
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err := db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := audit.RecordGateway(ctx, tx, audit.Event{Action: "ledger.journal.rejected", ActorType: actorType, ActorID: actorID,
			TargetType: "ledger_posting", Outcome: "failed", Reason: code, Metadata: map[string]any{"posting_rule": p.Rule,
				"idempotency_key": p.IdempotencyKey, "source_system": p.SourceSystem, "source_type": p.SourceType, "error_code": code}})
		return err
	})
	if err != nil {
		s.Logger.Warn("ledger: rejection audit failed", "err", err)
	}
}

// ---- accounts --------------------------------------------------------------------------------------------------

func (s *Service) ensureAccounts(ctx context.Context, tx pgx.Tx, env, ccy string, lines []Line, actor Actor) (map[string]account, error) {
	refs := map[string]AccountRef{}
	for _, l := range lines {
		refs[l.Account.Code()] = l.Account
	}
	codes := make([]string, 0, len(refs))
	for c := range refs {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	out := map[string]account{}
	for _, code := range codes {
		ref := refs[code]
		spec := Chart[ref.Class]
		var owner any
		if ref.OwnerID != "" {
			owner = ref.OwnerID
		}
		id := ids.New()
		tag, err := tx.Exec(ctx, `INSERT INTO ledger.ledger_accounts (id, code, account_class, type, normal_balance, kind, currency, owner_type,
			owner_id, environment) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT ON CONSTRAINT uq_ledger_accounts_code_currency DO NOTHING`,
			id, code, ref.Class, spec.Type, spec.NormalBalance, spec.Kind, ccy, spec.OwnerType, owner, env)
		if err != nil {
			return nil, mapDBError(err)
		}
		if tag.RowsAffected() == 1 {
			actorType, actorID := "system", ""
			if actor.Type == "STAFF" {
				actorType, actorID = "staff", actor.ID
			}
			if _, err := audit.RecordGateway(ctx, tx, audit.Event{Action: "ledger.account.created", ActorType: actorType, ActorID: actorID,
				TargetType: "ledger_account", TargetID: id, Metadata: map[string]any{"code": code, "currency": ccy, "environment": env,
					"account_class": ref.Class}}); err != nil {
				return nil, err
			}
		}
		var a account
		if err := tx.QueryRow(ctx, `SELECT id, code, account_class, status, normal_balance, projection_mode, non_negative
			FROM ledger.ledger_accounts WHERE code = $1 AND currency = $2`, code, ccy).
			Scan(&a.ID, &a.Code, &a.Class, &a.Status, &a.Normal, &a.Mode, &a.NonNegative); err != nil {
			return nil, err
		}
		out[code] = a
	}
	return out, nil
}

// lockAndCheck locks, in ascending account id, the SYNC rows of non-negative accounts whose net movement in this
// journal is a decrease, and checks that the balance covers it (the CHECK is the backstop).
func lockAndCheck(ctx context.Context, tx pgx.Tx, accts map[string]account, lines []Line) error {
	net := map[string]int64{} // account id → net change in normal-balance terms
	for _, l := range lines {
		a := accts[l.Account.Code()]
		if !a.NonNegative {
			continue
		}
		if l.Direction == a.Normal {
			net[a.ID] += l.Amount
		} else {
			net[a.ID] -= l.Amount
		}
	}
	var dec []string
	for id, n := range net {
		if n < 0 {
			dec = append(dec, id)
		}
	}
	sort.Strings(dec)
	for _, id := range dec {
		var bal int64
		if err := tx.QueryRow(ctx, `SELECT balance_minor FROM ledger.ledger_balances WHERE account_id = $1 FOR UPDATE`, id).Scan(&bal); err != nil {
			return mapDBError(err)
		}
		if bal+net[id] < 0 {
			return ErrInsufficientFunds
		}
	}
	return nil
}

// ---- idempotency -----------------------------------------------------------------------------------------------

type storedJournal struct {
	ID, Rule, Currency, SourceType, SourceID string
	Lines                                    []string
}

func journalByKey(ctx context.Context, tx pgx.Tx, key string) (storedJournal, bool, error) {
	var j storedJournal
	err := tx.QueryRow(ctx, `SELECT id, posting_rule, currency, source_type, source_id::text FROM ledger.ledger_transactions
		WHERE idempotency_key = $1`, key).Scan(&j.ID, &j.Rule, &j.Currency, &j.SourceType, &j.SourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, false, nil
	}
	if err != nil {
		return j, false, err
	}
	rows, err := tx.Query(ctx, `SELECT a.code, e.direction, e.amount_minor FROM ledger.ledger_entries e
		JOIN ledger.ledger_accounts a ON a.id = e.account_id WHERE e.transaction_id = $1`, j.ID)
	if err != nil {
		return j, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var code, dir string
		var amt int64
		if err := rows.Scan(&code, &dir, &amt); err != nil {
			return j, false, err
		}
		j.Lines = append(j.Lines, lineKey(code, dir, amt))
	}
	sort.Strings(j.Lines)
	return j, true, rows.Err()
}

// equivalent: same rule, currency, source and the same multiset of (account code, direction, amount).
func (j storedJournal) equivalent(p Posting) bool {
	if j.Rule != p.Rule || j.Currency != p.Currency || j.SourceType != p.SourceType || j.SourceID != p.SourceID {
		return false
	}
	want := sortedKeys(p.Lines)
	if len(want) != len(j.Lines) {
		return false
	}
	for i := range want {
		if want[i] != j.Lines[i] {
			return false
		}
	}
	return true
}

// ---- database errors -------------------------------------------------------------------------------------------

func isUnique(err error, constraint string) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505" && (constraint == "" || pe.ConstraintName == constraint)
}

func isTransient(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && (pe.Code == "40001" || pe.Code == "40P01")
}

// mapDBError turns a database refusal into a stable ledger error (the database is the last line of defence; a
// refusal Go did not predict is logged by the caller as an alert).
func mapDBError(err error) error {
	var known *errs.Error
	if err == nil || errors.As(err, &known) || errors.Is(err, errRetry) {
		return err
	}
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		return err
	}
	if pe.Code == "40001" || pe.Code == "40P01" {
		return err
	}
	if pe.Code == "23505" && pe.ConstraintName == "uq_ledger_transactions_idempotency_key" {
		return errRetry
	}
	msg := pe.Message
	wrap := func(e *errs.Error) error { return errs.Wrap(err, e.Kind, e.Code, e.Message) }
	switch {
	case pe.ConstraintName == "ck_ledger_balances_non_negative":
		return wrap(ErrInsufficientFunds)
	case strings.Contains(msg, "unbalanced") || strings.Contains(msg, "at least 2 required"):
		return wrap(ErrUnbalanced)
	case strings.Contains(msg, "is FROZEN"):
		return wrap(ErrAccountFrozen)
	case strings.Contains(msg, "ledger account") && strings.Contains(msg, "is CLOSED"):
		return wrap(ErrAccountClosed)
	case strings.Contains(msg, "period") && (strings.Contains(msg, "CLOSED") || strings.Contains(msg, "no ledger period")):
		return wrap(ErrPeriodClosed)
	case strings.Contains(msg, "environment"):
		return wrap(ErrEnvironment)
	case strings.Contains(msg, "unverified minor units") || pe.Code == "23503" && strings.Contains(pe.ConstraintName, "currency"):
		return wrap(ErrCurrency)
	case strings.Contains(msg, "posting rule") || strings.Contains(msg, "mirror") || strings.Contains(msg, "approved lines") ||
		strings.Contains(msg, "adjustment"):
		return wrap(ErrRuleViolation)
	case pe.Code == "23514" || pe.Code == "23505" || pe.Code == "23503" || pe.Code == "23P01":
		return wrap(ErrRejectedByDatabase)
	}
	return err
}

func nonNilMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
